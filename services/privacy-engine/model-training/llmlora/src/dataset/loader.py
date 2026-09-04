# -*- coding: utf-8 -*-
"""
llmlora SFT 数据加载与 Tokenization 模块 / SFT data loading & tokenization.

核心机制：Prompt Labels Masking（仅对 Assistant 输出计算损失）。
Core mechanism: prompt labels masking (loss computed only on assistant tokens).

实现要点 / Implementation notes:
1. 使用 tokenizer.apply_chat_template 一次性编码整段对话（system + user + assistant），
   Uses tokenizer.apply_chat_template to encode the whole conversation in one pass,
   避免「分段编码再手动拼接」导致的模板格式不一致。
   avoiding template-format inconsistency caused by encode-then-concatenate.
2. Qwen3.5 chat template 不含 {% generation %} 标记，官方的
   return_assistant_tokens_mask 恒为全 0；因此改用「prompt 前缀长度定位」：
   用渲染好的推理 prompt 文本（render_prompt_text，enable_thinking=False）
   重新编码得到 prompt token 序列，其长度即 assistant 输出起点。
   prompt 部分不包含任何 assistant 内容，前后两次编码结果完全一致，
   不存在边界 token 漂移问题。
   The Qwen3.5 chat template lacks the {% generation %} tag so the official
   assistant mask is always zero; we therefore locate the assistant start by
   re-encoding the rendered inference prompt (enable_thinking=False). Since
   the prompt part contains no assistant content, both encodings agree
   exactly and no boundary token drift can occur.
3. assistant 输出之外的所有 token（system/user/模板标记）labels 置为 -100。
   All tokens outside the assistant output (system/user/template markers) get -100.
4. 训练与推理共用同一套消息构建函数，保证分布一致。
   Training and inference share the same message builders to keep distributions aligned.
"""
from __future__ import annotations

import json
from typing import Any, Dict, List, Optional

from transformers import PreTrainedTokenizerBase

# 与推理引擎共享的系统提示词（显式包含分类分级标准与解耦规则说明）
# System prompt shared with inference engine (explicitly includes standard taxonomy definitions)
SYSTEM_PROMPT = (
    "你是一个专业的隐私安全Sidecar助手。请分析输入的文本，识别敏感信息，"
    "输出分类分级结果（JSON格式），并提供语义连贯的无痕抹平脱敏重写文本。\n\n"
    "【数据分类分级标准指南】\n"
    "- L1 (公开数据): 无敏感信息的公开资讯、通用日常文本。\n"
    "- L2 (内部数据): 业务统计指标、系统日志、设备运维等低敏感内部数据。\n"
    "- L3 (敏感数据/个人基本信息): 姓名、身份证号、手机号、银行卡号、电子邮箱等个人基础标识与资产信息。\n"
    "- L4 (高敏感数据/诊疗与金融敏感): 疾病诊断（如重度抑郁症、高血压、冠心病）、病历主诉、处方药品等医疗健康敏感信息。\n"
    "- L5 (极敏感数据): 基因组、生物特征、特级商业机密等核心数据。"
)

# labels 掩码值（PyTorch CrossEntropyLoss 的 ignore_index）
# Label ignore index (matches PyTorch CrossEntropyLoss ignore_index)
IGNORE_INDEX = -100


def load_jsonl(file_path: str) -> List[Dict[str, Any]]:
    """读取 JSONL 数据文件 / Read a JSONL data file.

    跳过空行；非法 JSON 行直接抛错（数据管道应保证合法性）。
    Empty lines are skipped; malformed JSON raises immediately
    (the data pipeline guarantees validity upstream).
    """
    data: List[Dict[str, Any]] = []
    with open(file_path, "r", encoding="utf-8") as f:
        for line_no, line in enumerate(f, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                data.append(json.loads(line))
            except json.JSONDecodeError as exc:
                raise ValueError(
                    f"JSONL 第 {line_no} 行解析失败 ({file_path}): {exc}"
                ) from exc
    return data


def build_messages(user_input: str, assistant_output: Optional[str] = None) -> List[Dict[str, str]]:
    """构建 ChatML 对话消息列表 / Build the ChatML message list.

    Args:
        user_input: 用户输入文本 / User input text.
        assistant_output: Assistant 期望输出；为 None 表示仅构建 Prompt 前缀
            （推理用） / Desired assistant output; None builds the prompt
            prefix only (for inference).

    Returns:
        messages 列表 / The messages list.
    """
    messages: List[Dict[str, str]] = [
        {"role": "system", "content": SYSTEM_PROMPT},
        {"role": "user", "content": user_input},
    ]
    if assistant_output is not None:
        messages.append({"role": "assistant", "content": assistant_output})
    return messages


def render_prompt_text(tokenizer: PreTrainedTokenizerBase, user_input: str) -> str:
    """渲染推理用 Prompt 文本（关闭 thinking 模式）/ Render inference prompt text.

    基座 chat_template 在 add_generation_prompt 时会注入 think 标记前缀，
    必须显式传入 enable_thinking=False 使模板走非思考分支，
    否则推理输出会带思考标记导致 JSON 解析失败。
    The base chat template injects a ``<think>...</think>`` prefix when
    add_generation_prompt is set; enable_thinking=False must be passed so the
    non-thinking branch is used, otherwise generation output carries thinking
    markers and JSON parsing breaks.
    """
    messages = build_messages(user_input, assistant_output=None)
    kwargs: Dict[str, Any] = {
        "tokenize": False,
        "add_generation_prompt": True,
    }
    try:
        return tokenizer.apply_chat_template(
            messages, enable_thinking=False, **kwargs
        )
    except TypeError:
        # 兼容不支持 enable_thinking 参数的旧模板
        # Fallback for templates that do not accept enable_thinking
        return tokenizer.apply_chat_template(messages, **kwargs)


def extract_user_input(sample: Dict[str, Any]) -> str:
    """从样本中提取用户输入 / Extract the user input from a sample.

    兼容两种数据形态 / Supports two sample shapes:
    - {"input": "..."}（instruction 已并入 system prompt）
    - {"instruction": "...", "input": "..."}（两者拼接）

    训练（tokenize_sft_sample）与评估（scripts/evaluate.py 等）共用本函数，
    保证输入构造一致。
    Shared by training and evaluation so input construction stays consistent.
    """
    instruction = sample.get("instruction") or ""
    user_input = sample.get("input") or ""
    if instruction and user_input:
        return f"{instruction}\n{user_input}"
    return user_input or instruction


def _extract_output_text(sample: Dict[str, Any]) -> str:
    """从样本中提取 Assistant 目标输出文本 / Extract assistant target text."""
    output = sample.get("output", "")
    if isinstance(output, str):
        return output
    # 非字符串输出（dict/list）序列化为紧凑 JSON
    # Non-string outputs are serialized to compact JSON
    return json.dumps(output, ensure_ascii=False)


def tokenize_sft_sample(
    sample: Dict[str, Any],
    tokenizer: PreTrainedTokenizerBase,
    max_length: int = 512,
) -> Dict[str, List[int]]:
    """对单条 SFT 样本执行 ChatML Tokenization 与 Labels Masking。

    Tokenize one SFT sample with ChatML formatting and labels masking.

    流程 / Flow:
    1. 构建 system + user + assistant 完整对话并一次性编码 /
       Build the full conversation and encode in one pass.
    2. 重新编码推理 prompt 前缀，以其长度定位 assistant 输出起点 /
       Re-encode the inference prompt prefix to locate the assistant start.
    3. labels = assistant 位置保留 token id，其余置 -100 /
       labels keep ids at assistant positions, -100 elsewhere.
    4. 超过 max_length 截断（保留尾部 assistant 输出的完整性优先）/
       Truncate to max_length (tail = assistant output kept intact).

    Returns:
        {"input_ids", "attention_mask", "labels"} 三个等长 list。
    """
    user_input = extract_user_input(sample)
    output_text = _extract_output_text(sample)

    messages = build_messages(user_input, assistant_output=output_text)

    # 一次性编码整段对话（避免分段拼接的模板格式不一致）
    # Encode the full conversation in one pass
    encoded = tokenizer.apply_chat_template(
        messages,
        tokenize=True,
        return_dict=True,
        add_generation_prompt=False,
    )
    input_ids: List[int] = list(encoded["input_ids"])

    # 定位 assistant 输出起点：重新编码渲染好的推理 prompt 前缀。
    # Locate the assistant start by re-encoding the rendered inference prompt.
    # prompt 文本与推理时完全一致（enable_thinking=False），
    # 且不含 assistant 内容，编码结果与完整序列前缀严格对齐。
    # The prompt text is identical to inference time and contains no
    # assistant content, so it aligns exactly with the full-sequence prefix.
    prompt_text = render_prompt_text(tokenizer, user_input)
    prompt_ids = tokenizer(prompt_text, add_special_tokens=False)["input_ids"]
    assistant_start = len(prompt_ids)

    # 防御：若前缀对齐异常（不应发生），退化为全序列计算损失而非静默全掩码
    # Defensive guard: on misalignment, fall back to full-sequence loss
    # instead of silently producing an all-masked sample
    if not (0 < assistant_start < len(input_ids)):
        assistant_start = 0

    # 截断保护：尾部截断可能砍掉 im_end，可接受；需同步截断。
    # Truncation guard: tail truncation may cut the closing <|im_end|>,
    # which is acceptable; input_ids must be truncated in sync.
    if len(input_ids) > max_length:
        input_ids = input_ids[:max_length]

    labels = [
        IGNORE_INDEX if idx < assistant_start else token_id
        for idx, token_id in enumerate(input_ids)
    ]

    return {
        "input_ids": input_ids,
        "attention_mask": [1] * len(input_ids),
        "labels": labels,
    }


def make_tokenize_fn(tokenizer: PreTrainedTokenizerBase, max_length: int):
    """构造绑定 tokenizer 的 map 函数（供 datasets.Dataset.map 使用）。

    Build a tokenizer-bound mapping function for datasets.Dataset.map.
    """

    def _tokenize(sample: Dict[str, Any]) -> Dict[str, List[int]]:
        return tokenize_sft_sample(sample, tokenizer, max_length)

    return _tokenize
