# -*- coding: utf-8 -*-
"""
llmlora vLLM 推理引擎模块 / vLLM Inference engine (QwenPrivacyVLLMEngine).

使用 vLLM 库提供的高性能推理引擎，用于替代 HuggingFace 原生 generate。
High-performance inference engine built on vLLM, replacing native HuggingFace generate.

特性 / Features:
- 基于 vLLM PagedAttention / Continuous Batching，推理吞吐与延迟提升 5-10 倍。
- 延迟加载（首次推理才初始化 vLLM Engine）。
- 保持与 QwenPrivacyLoRAEngine 完全一致的公开 API 签名。
"""
from __future__ import annotations

import threading
from typing import Any, Dict, List, Optional

import os

# 强制使用 vLLM V0 引擎，避免本地 Linux/WSL 环境下触发 V1 UVA (Unified Memory) 错误
os.environ["VLLM_USE_V1"] = "0"

from transformers import AutoTokenizer

from llmlora.src.dataset.loader import render_prompt_text
from llmlora.src.utils.logger import setup_logger
from llmlora.src.utils.metrics import extract_json_from_text

logger = setup_logger("vllm_inference")


class QwenPrivacyVLLMEngine:
    """基于 vLLM 的纯文本分类分级与无痕抹平推理引擎。

    Args:
        model_path: 基座或合并模型路径 / Base or merged model path.
        adapter_path: LoRA adapter 目录（合并模型场景传 None） /
            LoRA adapter directory (None for merged models).
        gpu_memory_utilization: GPU 显存占用上限（默认 0.8） /
            vLLM GPU memory utilization cap.
        max_new_tokens: 单次生成上限 / Generation token budget.
        tensor_parallel_size: 张量并行 GPU 数量（默认 1） / Tensor parallel size.
    """

    def __init__(
        self,
        model_path: str,
        adapter_path: Optional[str] = None,
        gpu_memory_utilization: float = 0.8,
        max_new_tokens: int = 384,
        tensor_parallel_size: int = 1,
    ):
        self.model_path = model_path
        self.adapter_path = adapter_path
        self.gpu_memory_utilization = gpu_memory_utilization
        self.max_new_tokens = max_new_tokens
        self.tensor_parallel_size = tensor_parallel_size

        self.tokenizer = None
        self.llm = None
        self.sampling_params = None
        self._initialized = False
        self._init_lock = threading.Lock()

    def _lazy_init(self) -> None:
        """延迟初始化 vLLM Engine（线程安全）."""
        if self._initialized:
            return
        with self._init_lock:
            if self._initialized:
                return

            try:
                from vllm import LLM, SamplingParams
                from vllm.lora.request import LoRARequest
            except ImportError as err:
                raise ImportError(
                    "使用 vLLM 推理引擎需要先安装 vllm 依赖。\n"
                    "请运行: llmlora/.venv/bin/pip install vllm"
                ) from err

            logger.info(f"正在初始化 vLLM 引擎, model_path: {self.model_path}")

            # 1. Tokenizer 初始化
            self.tokenizer = AutoTokenizer.from_pretrained(
                self.model_path,
                trust_remote_code=True,
                padding_side="left",
            )
            if self.tokenizer.pad_token is None:
                self.tokenizer.pad_token = self.tokenizer.eos_token

            # 2. 默认 SamplingParams (temperature=0 保证判定结果确定性)
            self.sampling_params = SamplingParams(
                temperature=0.0,
                max_tokens=self.max_new_tokens,
                stop_token_ids=[self.tokenizer.eos_token_id] if self.tokenizer.eos_token_id else None,
            )

            # 3. 初始化 vLLM
            enable_lora = bool(self.adapter_path)
            self.llm = LLM(
                model=self.model_path,
                trust_remote_code=True,
                tensor_parallel_size=self.tensor_parallel_size,
                gpu_memory_utilization=self.gpu_memory_utilization,
                enable_lora=enable_lora,
                max_lora_rank=64 if enable_lora else 16,
            )
            self._initialized = True
            logger.info("vLLM 引擎初始化完成 ✅")

    def generate_raw_batch(
        self, texts: List[str], max_new_tokens: Optional[int] = None
    ) -> List[str]:
        """批量生成原始文本（vLLM 原生并行加速）."""
        if not texts:
            return []
        self._lazy_init()

        prompts = [render_prompt_text(self.tokenizer, t) for t in texts]

        sampling_params = self.sampling_params
        if max_new_tokens and max_new_tokens != self.max_new_tokens:
            from vllm import SamplingParams
            sampling_params = SamplingParams(
                temperature=0.0,
                max_tokens=max_new_tokens,
                stop_token_ids=[self.tokenizer.eos_token_id] if self.tokenizer.eos_token_id else None,
            )

        lora_request = None
        if self.adapter_path:
            from vllm.lora.request import LoRARequest
            lora_request = LoRARequest("privacy_lora", 1, self.adapter_path)

        outputs = self.llm.generate(
            prompts,
            sampling_params=sampling_params,
            lora_request=lora_request,
            use_tqdm=False,
        )

        results = []
        for output in outputs:
            generated_text = output.outputs[0].text if output.outputs else ""
            results.append(generated_text)
        return results

    def generate_raw(self, text: str, max_new_tokens: Optional[int] = None) -> str:
        """生成单条原始输出文本."""
        res = self.generate_raw_batch([text], max_new_tokens=max_new_tokens)
        return res[0] if res else ""

    def classify(self, text: str, max_new_tokens: Optional[int] = None) -> Optional[Dict[str, Any]]:
        """单条文本分类分级与脱敏抹平推理."""
        response = self.generate_raw(text, max_new_tokens=max_new_tokens)
        parsed = extract_json_from_text(response)
        if parsed is None:
            logger.warning(f"无法解析 vLLM 输出为 JSON: {response[:120]!r}")
        return parsed

    def classify_batch(
        self, texts: List[str], max_new_tokens: Optional[int] = None
    ) -> List[Optional[Dict[str, Any]]]:
        """批量文本分类分级与脱敏抹平推理（vLLM 自动 Continuous Batching）."""
        responses = self.generate_raw_batch(texts, max_new_tokens=max_new_tokens)
        results = []
        for resp in responses:
            results.append(extract_json_from_text(resp))
        return results
