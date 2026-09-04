# -*- coding: utf-8 -*-
"""
vLLM 推理引擎模块 / vLLM inference engine (QwenPrivacyVLLMEngine).

基于 vLLM 的高性能推理后端，支持 PagedAttention 和连续批处理。
High-performance inference backend with PagedAttention and continuous batching.

特性 / Features:
- 与 PyTorch 引擎相同的接口 / Same interface as PyTorch engine.
- vLLM PagedAttention 加速 / PagedAttention acceleration.
- 支持合并模型和基座 + LoRA / Supports merged models and base + LoRA.
- CUDA Graphs 默认启用 / CUDA Graphs enabled by default.
"""
from __future__ import annotations

import os
import threading
from typing import Any, Dict, List, Optional

from llmlora.src.dataset.loader import render_prompt_text
from llmlora.src.utils.logger import setup_logger
from llmlora.src.utils.metrics import extract_json_from_text

logger = setup_logger("inference")


class QwenPrivacyVLLMEngine:
    """基于 vLLM 的高性能推理引擎。

    High-performance inference engine based on vLLM.

    Args:
        model_path: 基座或合并模型路径 / Base or merged model path.
        adapter_path: LoRA adapter 目录（合并模型场景传 None） /
            LoRA adapter directory (None for merged models).
        max_new_tokens: 单次生成上限 / Generation token budget.
        max_model_len: 最大模型序列长度（默认 4096，避免 KV cache OOM） /
            Max model sequence length (default 4096 to avoid KV cache OOM).
        gpu_memory_utilization: GPU 显存利用率（默认 0.85） /
            GPU memory utilization ratio (default 0.85).
        enforce_eager: 是否禁用 CUDA Graphs（默认 False，启用加速） /
            Whether to disable CUDA Graphs (default False, enabled for speed).
    """

    def __init__(
        self,
        model_path: str,
        adapter_path: Optional[str] = None,
        max_new_tokens: int = 384,
        max_model_len: int = 4096,
        gpu_memory_utilization: float = 0.85,
        enforce_eager: bool = False,
    ):
        """记录配置但不加载权重（延迟初始化） / Store config without loading weights."""
        self.model_path = model_path
        self.adapter_path = adapter_path
        self.max_new_tokens = max_new_tokens
        self.max_model_len = max_model_len
        self.gpu_memory_utilization = gpu_memory_utilization
        self.enforce_eager = enforce_eager

        self._llm = None
        self._tokenizer = None
        self._lora_request = None
        self._initialized = False
        self._init_lock = threading.Lock()

    def _lazy_init(self) -> None:
        """延迟加载（首次调用时初始化，线程安全） / Lazy thread-safe init."""
        if self._initialized:
            return
        with self._init_lock:
            if self._initialized:
                return

            logger.info(f"初始化 QwenPrivacyVLLMEngine, model_path: {self.model_path}")

            # 禁用 vLLM V1 多进程模式（单 GPU 场景更稳定）
            # Disable vLLM V1 multiprocessing (more stable for single GPU)
            os.environ.setdefault("VLLM_USE_V1", "0")
            os.environ.setdefault("VLLM_ENABLE_V1_MULTIPROCESSING", "0")

            from vllm import LLM

            # vLLM 加载模型（支持合并模型直接加载，或基座 + LoRA adapter）
            # vLLM loads model (supports merged model directly, or base + LoRA adapter)
            llm_kwargs: Dict[str, Any] = dict(
                model=self.model_path,
                trust_remote_code=True,
                tensor_parallel_size=1,
                gpu_memory_utilization=self.gpu_memory_utilization,
                max_model_len=self.max_model_len,
                enforce_eager=self.enforce_eager,
                disable_log_stats=True,
            )

            # LoRA adapter 支持（vLLM 0.26 正确 API：构造时仅 enable_lora，
            # generate 时传 LoRARequest；EngineArgs 无 lora_modules 字段，
            # 传入会抛 TypeError）
            # LoRA adapter support (vLLM 0.26 API: enable_lora at construction,
            # LoRARequest at generate time; EngineArgs has no lora_modules
            # field and passing it raises TypeError)
            if self.adapter_path:
                llm_kwargs["enable_lora"] = True
                # 覆盖常见 LoRA 秩（默认 16 放不下 r=32/64 的 adapter）
                llm_kwargs["max_lora_rank"] = 64
                logger.info(f"启用 LoRA 支持，适配器: {self.adapter_path}")

            self._llm = LLM(**llm_kwargs)

            if self.adapter_path:
                from vllm.lora.request import LoRARequest

                self._lora_request = LoRARequest("privacy_lora", 1, self.adapter_path)

            # 获取 tokenizer 用于 prompt 构造
            # Get tokenizer for prompt construction
            from transformers import AutoTokenizer
            self._tokenizer = AutoTokenizer.from_pretrained(
                self.model_path, trust_remote_code=True
            )

            self._initialized = True
            logger.info("QwenPrivacyVLLMEngine 初始化完成")

    def _build_sampling_params(self, max_new_tokens: Optional[int] = None):
        """构造采样参数 / Build sampling params."""
        from vllm import SamplingParams
        return SamplingParams(
            temperature=0.0,
            max_tokens=max_new_tokens or self.max_new_tokens,
        )

    def generate_raw(self, text: str, max_new_tokens: Optional[int] = None) -> str:
        """生成原始输出文本（不做 JSON 解析） / Generate raw output text.

        Args:
            text: 待分析文本 / Text to analyze.
            max_new_tokens: 覆盖默认生成长度 / Override the default token budget.

        Returns:
            模型生成的原始字符串 / Raw generated string.
        """
        self._lazy_init()
        prompt_text = render_prompt_text(self._tokenizer, text)
        sampling_params = self._build_sampling_params(max_new_tokens)

        outputs = self._llm.generate(
            [prompt_text], sampling_params, lora_request=self._lora_request
        )
        return outputs[0].outputs[0].text

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
        """批量推理（vLLM 连续批处理） / Batched inference with continuous batching.

        vLLM 自动处理动态批处理和 PagedAttention，无需手动 padding。
        vLLM handles dynamic batching and PagedAttention automatically.
        """
        if not texts:
            return []
        self._lazy_init()

        prompts = [render_prompt_text(self._tokenizer, t) for t in texts]
        sampling_params = self._build_sampling_params(max_new_tokens)

        outputs = self._llm.generate(
            prompts, sampling_params, lora_request=self._lora_request
        )

        results: List[Optional[Dict[str, Any]]] = []
        for output in outputs:
            response = output.outputs[0].text
            results.append(extract_json_from_text(response))
        return results
