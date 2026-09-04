# -*- coding: utf-8 -*-
"""
vLLM 高性能推理性能测试 (Fast Sub-20s Mode).

基于 vLLM 工业级高并发推理引擎对 Qwen3.5-0.8B 执行 Batch 基准测试。
说明：
vLLM v0.26 通过 Qwen3_5ForConditionalGeneration 多模态架构统一加载模型，
需要合并模型目录包含完整的 config.json（含嵌套 vision_config 与 text_config）以及 model.visual.* 兼容补丁权重。
"""
# 启用 Python 3.7+ 的类型注解延迟求值机制
from __future__ import annotations

# 导入操作系统接口模块，用于配置底层环境变量
import os
# 显式关闭 vLLM V1 实验性后端引擎，强制使用稳定的 V0 核心调度引擎 (确保对 3:1 Hybrid SSM 架构的完美兼容)
os.environ["VLLM_USE_V1"] = "0"
# 禁用 V1 引擎的多进程工作模式，避免单卡环境下的子进程通信开销与多进程锁竞争
os.environ["VLLM_ENABLE_V1_MULTIPROCESSING"] = "0"

# 导入命令行入参解析库
import argparse
# 导入 Python 内存垃圾回收器模块
import gc
# 导入 JSON 序列化与格式化工具库
import json
# 导入高精度时钟计时器模块
import time
# 导入 Python 系统环境接口，用于动态操作 Python 模块搜索路径
import sys
# 导入跨平台文件路径处理类 Path
from pathlib import Path
# 导入类型注解字典、列表与任意类型
from typing import Dict, List, Any

_LLMLORA_DIR = Path(__file__).resolve().parents[1]
_MODEL_TRAINING_DIR = _LLMLORA_DIR.parent
_REPO_ROOT = _MODEL_TRAINING_DIR.parent.parent.parent

for _p in (_MODEL_TRAINING_DIR, _REPO_ROOT):
    if str(_p) not in sys.path:
        sys.path.insert(0, str(_p))

# 从 vLLM 核心推理引擎库导入离线 LLM 推理器与采样控制参数类
from vllm import LLM, SamplingParams
# 从 HuggingFace Transformers 导入分词器类，用于辅助解析 Token ID 与特殊终止符
from transformers import AutoTokenizer
# 从 llmlora 数据加载模块导入 ChatML 提示词格式化函数与 JSONL 数据读取函数
from llmlora.src.dataset.loader import render_prompt_text, load_jsonl


def run_vllm_benchmark(
    model_path: str,                  # 待评测的合并大模型物理权重目录路径
    test_data_path: str,              # 评测测试集 JSONL 文件路径
    batch_sizes: List[int] = [1, 4],  # 需要依次执行性能评测的 Batch Size 列表 (默认评测 Batch=1 与 Batch=4)
    gpu_utilization: float = 0.5,     # vLLM 预分配的 GPU 显存占比上限 (默认预留 50% 显存作为 KV 物理分页池)
    max_model_len: int = 4096,        # vLLM 允许的最大上下文长度 (硬上限截断，有效限制 KV Cache 显存膨胀)
) -> Dict[str, Any]:
    """运行 vLLM 高性能推理基准测试并返回详细的性能指标数据字典."""
    # 打印终端分割线
    print("=" * 64)
    # 打印基准测试启动标题日志
    print("🚀 启动 vLLM 高性能推理性能 Benchmark (快速模式)")
    # 打印待测模型所在的本地文件路径
    print(f"  模型路径: {model_path}")
    # 打印设定的最大模型上下文长度限制
    print(f"  max_model_len: {max_model_len}")
    # 打印终端分割线
    print("=" * 64)

    # 读取测试集 JSONL 文件中的样本数据列表
    samples = load_jsonl(test_data_path)
    # 若测试样本数据为空，则打印告警信息并直接返回空字典
    if not samples:
        print("❌ 测试集数据为空")
        return {}

    # 加载分词器以获取模型的特殊终止符配置 (例如 eos_token_id)
    tokenizer = AutoTokenizer.from_pretrained(model_path, trust_remote_code=True)
    # 若分词器未显式配置 pad_token，则将 eos_token (<|im_end|>) 作为安全兜底标记
    if tokenizer.pad_token is None:
        tokenizer.pad_token = tokenizer.eos_token

    # 记录 vLLM 引擎初始化开始的高精度时间戳
    start_init = time.perf_counter()
    # =========================================================================
    # [vLLM Batch 核心特性 1: PagedAttention 显存分页与连续批处理 (Continuous Batching)]
    # 区别于 PyTorch 原生需要手动执行 Left Padding 并构造 [B, max_len] 矩形张量，
    # vLLM 底层采用了革命性的 PagedAttention 显存分页管理：
    # 1. 显存按固定物理块 (Block, 默认 16 tokens/block) 动态按需分配，零内部/外部内存碎片；
    # 2. 无需手动 Padding：Batch 内各请求长度不同时，不会在显存中浪费无用的 Padding 空间；
    # 3. 连续批处理 (Continuous Batching)：Batch 内较短的请求率先生成完毕 (遇到 EOS) 时，
    #    其占用的物理块会被引擎即刻回收并分配给新请求，无需等待长请求结束。
    # =========================================================================
    llm = LLM(
        model=model_path,                           # 模型权重目录绝对路径
        trust_remote_code=True,                     # 允许执行自定义架构代码
        tensor_parallel_size=1,                     # 单卡推理，张量并行度设置为 1
        gpu_memory_utilization=gpu_utilization,     # 显存预分配配额比率
        enforce_eager=True,                         # 启用 Eager 模式执行，跳过耗时的全量 CUDA Graph 预捕获，实现秒级快速启动
        disable_log_stats=True,                     # 禁用内部冗余的运行统计日志输出
        max_model_len=max_model_len,                # 限制最大序列长度以节约预分配显存
    )
    # 计算 vLLM 引擎初始化与权重加载的耗时 (秒)
    init_time = time.perf_counter() - start_init
    # 打印引擎初始化成功日志与耗时
    print(f"✅ vLLM 引擎初始化完成，耗时: {init_time:.2f}s\n")

    # =========================================================================
    # [vLLM Batch 逻辑 2: 全局采样参数配置 (SamplingParams)]
    # 该配置将作为 Batch 内所有并发请求的统一自回归解码约束：
    # 1. temperature=0.0: 启用确定性贪心解码 (Greedy Search)，彻底消除随机采样波动；
    # 2. max_tokens=64: 单条请求自回归生成的最大 Token 数上限；
    # 3. stop_token_ids: 终止 Token 监听列表，一旦生成 <|im_end|> 即刻触发该请求的提前截断。
    # =========================================================================
    sampling_params = SamplingParams(
        temperature=0.0,                                                            # 贪心解码
        max_tokens=64,                                                              # 最大生成长度
        stop_token_ids=[tokenizer.eos_token_id] if tokenizer.eos_token_id else None, # 遇到结束符提前退出
    )

    # 初始化用于存储所有 Batch Size 测试结果的字典
    results = {}

    # 打印测试结果表头分割线
    print("-" * 64)
    # 格式化输出表头各字段名称
    print(f"{'Batch Size':<12} | {'总耗时(ms)':<12} | {'单条延迟(ms)':<14} | {'吞吐(tokens/s)':<14}")
    # 打印测试结果表头分割线
    print("-" * 64)

    # 遍历所有待测试的 Batch 大小 (例如 1, 4)
    for bsize in batch_sizes:
        # =====================================================================
        # [vLLM Batch 逻辑 3: 样本集合切片与多提示词隔离机制 (Prompt Isolation Architecture)]
        #
        # ❓ 核心疑问：不同的提示词组合在一起时，是如何隔开并防止混淆的？
        # 答：vLLM 中不同提示词通过 3 层机制进行严格的物理与逻辑隔离：
        #
        # 【隔离层 1: Python 列表级独立对象 (List[str])】
        # prompts 并不是一个用逗号或换行拼接的大字符串，而是包含 B 个独立元素的 Python 字符串列表：
        # prompts = [
        #     "<|im_start|>system\n...<|im_end|>\n<|im_start|>user\n患者张伟确诊冠心病<|im_end|>\n<|im_start|>assistant\n",
        #     "<|im_start|>system\n...<|im_end|>\n<|im_start|>user\n血常规报告WBC升高<|im_end|>\n<|im_start|>assistant\n",
        #     ...
        # ]
        # 每个 Prompt 在 Python 内存中均是互不干扰的独立 String 对象。
        #
        # 【隔离层 2: 文本内部 ChatML 结构自洽闭合】
        # 每个 Prompt 内部通过 <|im_start|> 与 <|im_end|> 形成自包含角色块，单条样本内部结构完整闭合。
        #
        # 【隔离层 3: vLLM 引擎级 Request ID 与 PagedAttention 物理块表隔离】
        # 1. 独立请求句柄: vLLM 为 prompts 列表中的每个字符串分配全局唯一的 request_id；
        # 2. 独立块表映射 (Block Table): PagedAttention 为每个请求独立维护逻辑块到 GPU 物理块的映射表；
        # 3. 零跨请求串扰: 即使多个请求在同一个 GPU 调度 Iteration 中并发解码，它们的 KV Cache 与
        #    SSM 状态在显存物理页与逻辑空间上完全隔离，绝不存在样本间数据交叉！
        # =====================================================================
        batch_items = (samples * ((bsize // len(samples)) + 2))[:bsize]
        prompts = [render_prompt_text(tokenizer, s["input"]) for s in batch_items]
        print(f"  Batch Size: {bsize}, Prompts: {prompts}")

        # 记录批量生成开始的高精度时间戳
        start_gen = time.perf_counter()
        # =====================================================================
        # [vLLM Batch 逻辑 4: 批量原生提交与引擎内部并行调度 (llm.generate)]
        # 1. 一次性将包含 bsize 条字符串的 prompts 列表传入 vLLM 引擎；
        # 2. vLLM 内部 C++ 调度器自动完成分词、逻辑块到物理块映射，并派发 Prefill/Decode 算子；
        # 3. 24 层 Hybrid 网络 (6 层 GQA KV Cache 与 18 层 SSM 循环隐状态) 在 GPU 上按批次高度并行执行；
        # 4. 返回 RequestOutput 列表，每个元素封装了对应单个请求的生成结果与 token_ids。
        # =====================================================================

        outputs = llm.generate(prompts, sampling_params)
        print(f"  Batch Size: {bsize}, Outputs: {outputs}")

        # 计算本次 Batch 生成的实际物理时间 (秒)
        elapsed_sec = time.perf_counter() - start_gen
        # 将秒换算为毫秒 (ms)
        elapsed_ms = elapsed_sec * 1000.0

        # =====================================================================
        # [vLLM Batch 逻辑 5: 真实生成 Token 统计与系统级吞吐量折算]
        # 1. 动态遍历 outputs 列表中每个请求的 token_ids 长度并求和，精准统计实际生成的有效 Token 总数 (无 Pad 干扰)；
        # 2. 系统综合吞吐量 (tokens/s) = 全 Batch 累计生成 Token 总数 / 本次 Batch 总耗时 (秒)；
        # 3. 单条平均延迟 (avg_latency_ms) = Batch 总耗时 / Batch Size (反映并发批处理对单请求的均摊加速)。
        # =====================================================================
        total_gen_tokens = sum(len(o.outputs[0].token_ids) for o in outputs)
        tokens_per_sec = total_gen_tokens / elapsed_sec if elapsed_sec > 0 else 0.0
        avg_latency = elapsed_ms / bsize

        # 将当前 Batch 大小的评测指标保存到结果字典中
        results[f"batch_{bsize}"] = {
            "batch_size": bsize,                  # 测试的 Batch 大小
            "elapsed_ms": elapsed_ms,              # Batch 全流程总耗时 (ms)
            "avg_latency_ms": avg_latency,          # 单条平摊延迟 (ms)
            "tokens_per_sec": tokens_per_sec,      # 系统每秒生成 Token 吞吐量 (tokens/s)
        }

        # 格式化打印当前 Batch Size 的评测结果行到控制台
        print(f"{bsize:<12} | {elapsed_ms:<12.1f} | {avg_latency:<14.1f} | {tokens_per_sec:<14.2f}")

    # 打印测试结果表格底部分割线
    print("-" * 64)
    # 返回包含所有 Batch Size 评测数据的字典
    return results


def main():
    """命令行主程序入口，负责解析入参并触发基准评测流程."""
    # 创建命令行参数解析器
    parser = argparse.ArgumentParser(description="vLLM 快速推理性能测试")
    # 添加待评测模型路径参数
    parser.add_argument(
        "--model-path",
        type=str,
        default=str(_LLMLORA_DIR / "output" / "models" / "Qwen3.5-0.8B-Privacy-Classifier-Smoother"),
        help="待评测模型所在的物理路径",
    )
    # 添加测试集数据文件路径参数
    parser.add_argument(
        "--test-data",
        type=str,
        default=str(_LLMLORA_DIR / "data" / "test.jsonl"),
        help="评测测试集 JSONL 文件路径",
    )
    # 添加最大上下文序列长度参数
    parser.add_argument(
        "--max-model-len",
        type=int,
        default=4096,
        help="vLLM 允许的最大上下文长度 (有效控制 KV cache 显存占用)",
    )
    # 添加可选的结果 JSON 文件落盘路径参数
    parser.add_argument(
        "--json-out",
        type=str,
        default="",
        help="测试结果保存的 JSON 文件路径 (可选)",
    )
    # 解析命令行传入的所有实际参数
    args = parser.parse_args()

    # 调用核心评测函数执行测试
    results = run_vllm_benchmark(args.model_path, args.test_data, max_model_len=args.max_model_len)
    # 若配置了 --json-out 且测试产生了有效指标，则将结果持久化保存为 JSON 文件
    if args.json_out and results:
        Path(args.json_out).write_text(json.dumps(results, indent=2), encoding="utf-8")


# 标准 Python 程序启动入口
if __name__ == "__main__":
    main()
