# -*- coding: utf-8 -*-
"""
llmlora 推理引擎模块 / Inference engine (QwenPrivacyLoRAEngine).

供 PrivShield Layer-3 调用的纯文本隐私分类分级与无痕抹平推理接口。
Pure-text privacy classification & smoothing inference API for Layer-3 of
PrivShield.

特性 / Features:
- 延迟加载（首次推理才加载权重） / Lazy loading (weights load on first call).
- 线程安全初始化与推理锁 / Thread-safe initialization and inference lock.
- ChatML Prompt 与训练侧完全一致（enable_thinking=False） /
  Prompt construction identical to training (enable_thinking=False).
- 支持基座 + LoRA adapter 或直接加载合并模型 /
  Supports base + LoRA adapter or a merged standalone model.
"""
from __future__ import annotations

import threading
from typing import Any, Dict, List, Optional

import torch
from transformers import AutoModelForCausalLM, AutoTokenizer

from llmlora.src.dataset.loader import render_prompt_text
from llmlora.src.utils.logger import setup_logger
from llmlora.src.utils.metrics import extract_json_from_text

logger = setup_logger("inference")


class QwenPrivacyLoRAEngine:
    """基于微调 Qwen3.5-0.8B 的纯文本分类分级与无痕抹平推理引擎。

    Inference engine built on the fine-tuned Qwen3.5-0.8B base.

    Args:
        model_path: 基座或合并模型路径 / Base or merged model path.
        adapter_path: LoRA adapter 目录（合并模型场景传 None） /
            LoRA adapter directory (None for merged models).
        device: "auto" / "cuda" / "cpu" / Device placement.
        max_new_tokens: 单次生成上限 / Generation token budget.
    """

    def __init__(
        self,
        model_path: str,
        adapter_path: Optional[str] = None,
        device: str = "auto",
        max_new_tokens: int = 384,
    ):
        """记录配置但不加载权重（延迟初始化） / Store config without loading weights."""
        self.model_path = model_path
        self.adapter_path = adapter_path
        self.device = device
        self.max_new_tokens = max_new_tokens

        self.tokenizer = None
        self.model = None
        self._initialized = False
        # 双锁职责：_init_lock 保护一次性初始化，_infer_lock 串行化 generate
        # _init_lock guards one-time init; _infer_lock serializes generate()
        self._init_lock = threading.Lock()
        self._infer_lock = threading.Lock()

    # ------------------------------------------------------------------
    # 初始化 / Initialization
    # ------------------------------------------------------------------

    def _resolve_device_map(self) -> Optional[str]:
        """解析 device_map / Resolve device_map for from_pretrained."""
        if not torch.cuda.is_available():
            return None
        if self.device in ("auto", "cuda"):
            return self.device
        return None

    def _lazy_init(self) -> None:
        """延迟加载（首次调用时初始化，线程安全） / Lazy thread-safe init."""
        if self._initialized:
            return
        with self._init_lock:
            if self._initialized:
                return

            logger.info(f"初始化 QwenPrivacyLoRAEngine, model_path: {self.model_path}")

            # 1. Tokenizer：推理侧左 padding 便于批处理
            # Tokenizer: left padding enables batched generation
            self.tokenizer = AutoTokenizer.from_pretrained(
                self.model_path,
                trust_remote_code=True,
                padding_side="left",
            )
            if self.tokenizer.pad_token is None:
                self.tokenizer.pad_token = self.tokenizer.eos_token

            # 2. 精度选择：CUDA 优先 bf16，CPU 用 fp32 避免精度损失
            # Dtype: prefer bf16 on CUDA; fp32 on CPU to avoid accuracy loss
            if torch.cuda.is_available() and torch.cuda.is_bf16_supported():
                dtype = torch.bfloat16
            elif torch.cuda.is_available():
                dtype = torch.float16
            else:
                dtype = torch.float32

            self.model = AutoModelForCausalLM.from_pretrained(
                self.model_path,
                torch_dtype=dtype,
                device_map=self._resolve_device_map(),
                trust_remote_code=True,
            )

            # 3. 可选挂载 LoRA adapter（延迟导入避免无 adapter 场景的依赖开销）
            # Optionally attach a LoRA adapter (lazy import keeps the no-adapter
            # path dependency-free)
            if self.adapter_path:
                from peft import PeftModel

                logger.info(f"挂载 LoRA 适配器: {self.adapter_path}")
                self.model = PeftModel.from_pretrained(self.model, self.adapter_path)

            self.model.eval()
            self._initialized = True
            logger.info("QwenPrivacyLoRAEngine 初始化完成")

    @property
    def _input_device(self) -> torch.device:
        """输入张量应放置的设备 / Device where input tensors should live."""
        if self.model is None:
            return torch.device("cpu")
        try:
            return next(self.model.parameters()).device
        except StopIteration:
            return torch.device("cpu")

    # ------------------------------------------------------------------
    # 推理 / Inference
    # ------------------------------------------------------------------

    def generate_raw(self, text: str, max_new_tokens: Optional[int] = None) -> str:
        """生成原始输出文本（不做 JSON 解析） / Generate raw output text.

        Args:
            text: 待分析文本 / Text to analyze.
            max_new_tokens: 覆盖默认生成长度 / Override the default token budget.

        Returns:
            模型生成的原始字符串 / Raw generated string.
        """
        self._lazy_init()
        prompt_text = render_prompt_text(self.tokenizer, text)
        inputs = self.tokenizer(prompt_text, return_tensors="pt")
        inputs = {k: v.to(self._input_device) for k, v in inputs.items()}

        with self._infer_lock, torch.inference_mode():
            outputs = self.model.generate(
                **inputs,
                max_new_tokens=max_new_tokens or self.max_new_tokens,
                do_sample=False,
                pad_token_id=self.tokenizer.pad_token_id,
                eos_token_id=self.tokenizer.eos_token_id,
            )

        generated = outputs[0][inputs["input_ids"].shape[1]:]
        return self.tokenizer.decode(generated, skip_special_tokens=True)

    def classify(self, text: str, max_new_tokens: Optional[int] = None) -> Optional[Dict[str, Any]]:
        """执行分类分级及无痕抹平推理 / Run classification & smoothing inference.

        Returns:
            解析后的 JSON dict（含 final_level / sanitized_text 等），
            Parsed JSON dict (with final_level / sanitized_text etc.);
            解析失败返回 None / None when the output is not valid JSON.
        """
        response = self.generate_raw(text, max_new_tokens=max_new_tokens)
        parsed = extract_json_from_text(response)
        if parsed is None:
            logger.warning(f"无法解析模型输出为 JSON: {response[:120]!r}")
        return parsed

    def classify_batch(
        self, texts: List[str], max_new_tokens: Optional[int] = None
    ) -> List[Optional[Dict[str, Any]]]:
        """批量推理（左 padding 一次生成） / Batched inference with left padding.

        对长尾输入建议分桶控制 padding 开销；此处提供正确性优先的实现。
        Bucketing by length reduces padding overhead for skewed lengths;
        this implementation prioritizes correctness.
        """
        if not texts:
            return []
        self._lazy_init()

        prompts = [render_prompt_text(self.tokenizer, t) for t in texts]
        inputs = self.tokenizer(
            prompts, return_tensors="pt", padding=True, truncation=False
        )
        input_len = inputs["input_ids"].shape[1]
        inputs = {k: v.to(self._input_device) for k, v in inputs.items()}

        with self._infer_lock, torch.inference_mode():
            outputs = self.model.generate(
                **inputs,
                max_new_tokens=max_new_tokens or self.max_new_tokens,
                do_sample=False,
                pad_token_id=self.tokenizer.pad_token_id,
                eos_token_id=self.tokenizer.eos_token_id,
            )

        results: List[Optional[Dict[str, Any]]] = []
        for row in outputs:
            response = self.tokenizer.decode(row[input_len:], skip_special_tokens=True)
            results.append(extract_json_from_text(response))
        return results
