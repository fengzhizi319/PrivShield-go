#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
llmlora vLLM 推理性能 Benchmark 测试脚本 / vLLM Performance Benchmark Script.

专门针对 Qwen3.5-0.8B 合并模型测试 vLLM 推理吞吐与延迟性能：
- 单条请求端到端延迟（ms） / Single-request end-to-end latency
- 批量请求吞吐量（req/s, tokens/s） / Batched throughput
- JSON 合法解析率 / JSON legal parse success rate

用法 / Usage:
    llmlora/.venv/bin/python -m llmlora.scripts.benchmark_vllm_performance
"""
from __future__ import annotations

import os
os.environ["VLLM_USE_V1"] = "0"
os.environ["VLLM_WORKER_MULTIPROC_METHOD"] = "spawn"

import argparse
import json
import sys
import time
from pathlib import Path
from typing import Any, Dict, List

# 保证当前模块正确导入
_LLMLORA_DIR = Path(__file__).resolve().parents[1]
_MODEL_TRAINING_DIR = _LLMLORA_DIR.parent
_REPO_ROOT = _MODEL_TRAINING_DIR.parent.parent.parent

for _p in (_MODEL_TRAINING_DIR, _REPO_ROOT):
    if str(_p) not in sys.path:
        sys.path.insert(0, str(_p))

# 强制禁用实验性 V1 引擎，保证本地 GPU 稳定兼容
os.environ["VLLM_USE_V1"] = "0"
os.environ["VLLM_WORKER_MULTIPROC_METHOD"] = "spawn"

from vllm import LLM, SamplingParams  # noqa: E402
from llmlora.src.dataset.loader import render_prompt_text, load_jsonl  # noqa: E402
from llmlora.src.utils.metrics import extract_json_from_text, summarize_latency  # noqa: E402


def run_vllm_benchmark(
    model_path: str,
    test_data_path: str,
    batch_sizes: List[int] = [1, 8, 16, 32],
    gpu_memory_utilization: float = 0.8,
) -> None:
    """运行 vLLM 推理性能基准测试 / Run vLLM performance benchmark."""
    print("=" * 68)
    print(f"🚀 启动 vLLM 推理性能 Benchmark 测试")
    print(f"  模型路径 : {model_path}")
    print(f"  测试数据 : {test_data_path}")
    print("=" * 68)

    # 1. 检查模型路径
    model_dir = Path(model_path)
    if not model_dir.exists():
        print(f"❌ 错误: 未找到模型路径 {model_path}")
        return

    # 2. 读取测试数据
    samples = load_jsonl(test_data_path)
    if not samples:
        print("❌ 错误: 测试集为空")
        return
    print(f"已加载 {len(samples)} 条测试集数据，正在初始化 vLLM 引擎...")

    # 3. 初始化 vLLM 引擎
    start_init = time.perf_counter()
    from transformers import AutoTokenizer
    tokenizer = AutoTokenizer.from_pretrained(model_path, trust_remote_code=True)
    if tokenizer.pad_token is None:
        tokenizer.pad_token = tokenizer.eos_token

    llm = LLM(
        model=model_path,
        trust_remote_code=True,
        tensor_parallel_size=1,
        gpu_memory_utilization=gpu_memory_utilization,
        disable_log_stats=True,
    )
    init_time = time.perf_counter() - start_init
    print(f"✅ vLLM 引擎初始化完成，耗时: {init_time:.2f}s\n")

    sampling_params = SamplingParams(
        temperature=0.0,
        max_tokens=384,
        stop_token_ids=[tokenizer.eos_token_id] if tokenizer.eos_token_id else None,
    )

    # 预热预编译 CUDA Graph
    print("🔥 正在执行 2 条样本 CUDA 预热...")
    warmup_prompts = [render_prompt_text(tokenizer, s["input"]) for s in samples[:2]]
    llm.generate(warmup_prompts, sampling_params)
    print("✅ 预热完成！开始测试多 Batch 性能...\n")

    # 4. 按 Batch Size 压测
    print("-" * 68)
    print(f"{'Batch Size':<12} | {'总耗时(ms)':<12} | {'单次延迟(ms)':<12} | {'吞吐(req/s)':<14} | {'吞吐(tokens/s)':<14}")
    print("-" * 68)

    for bsize in batch_sizes:
        test_samples = (samples * ((bsize // len(samples)) + 2))[:bsize]
        prompts = [render_prompt_text(tokenizer, s["input"]) for s in test_samples]

        start_time = time.perf_counter()
        outputs = llm.generate(prompts, sampling_params)
        elapsed_sec = time.perf_counter() - start_time
        elapsed_ms = elapsed_sec * 1000.0

        total_gen_tokens = sum(len(o.outputs[0].token_ids) for o in outputs)
        req_per_sec = bsize / elapsed_sec if elapsed_sec > 0 else 0.0
        tokens_per_sec = total_gen_tokens / elapsed_sec if elapsed_sec > 0 else 0.0
        avg_latency = elapsed_ms / bsize

        print(f"{bsize:<12} | {elapsed_ms:<12.1f} | {avg_latency:<12.1f} | {req_per_sec:<14.2f} | {tokens_per_sec:<14.2f}")

    print("-" * 68)

    # 5. 单条生成质量与 JSON 合法率验证
    print("\n🔍 正在抽样自检 5 条推理 JSON 格式合法率与脱敏效果...")
    eval_prompts = [render_prompt_text(tokenizer, s["input"]) for s in samples[:5]]
    eval_outputs = llm.generate(eval_prompts, sampling_params)

    valid_json_count = 0
    for i, out in enumerate(eval_outputs, start=1):
        gen_text = out.outputs[0].text
        parsed = extract_json_from_text(gen_text)
        is_valid = parsed is not None
        if is_valid:
            valid_json_count += 1

        print(f"\n--- 示例 {i} ---")
        print(f"📥 输入 : {samples[i-1]['input']}")
        print(f"🤖 输出 : {gen_text.strip()}")
        print(f"解析状态 : {'✅ 成功' if is_valid else '❌ 失败'}")

    print("\n" + "=" * 68)
    print(f"📊 测试总结报告:")
    print(f"  JSON 解析成功率 : {valid_json_count / len(eval_prompts) * 100:.1f}% ({valid_json_count}/{len(eval_prompts)})")
    print(f"  vLLM 极限吞吐量  : {tokens_per_sec:.1f} tokens/s (Batch Size = {batch_sizes[-1]})")
    print("=" * 68)


def main():
    parser = argparse.ArgumentParser(description="vLLM 推理性能 Benchmark 测试")
    parser.add_argument(
        "--model-path",
        type=str,
        default=str(_LLMLORA_DIR / "output" / "models" / "Qwen3.5-0.8B-Privacy-Classifier-Smoother"),
        help="合并模型路径或基座模型路径",
    )
    parser.add_argument(
        "--test-data",
        type=str,
        default=str(_LLMLORA_DIR / "data" / "test.jsonl"),
        help="测试集数据路径",
    )
    parser.add_argument(
        "--gpu-utilization",
        type=float,
        default=0.8,
        help="vLLM GPU 显存利用率上限",
    )
    args = parser.parse_args()

    run_vllm_benchmark(
        model_path=args.model_path,
        test_data_path=args.test_data,
        gpu_memory_utilization=args.gpu_utilization,
    )


if __name__ == "__main__":
    main()
