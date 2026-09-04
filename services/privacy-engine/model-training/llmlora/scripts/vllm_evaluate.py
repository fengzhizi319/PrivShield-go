#!/usr/bin/env python3
# -*- coding: utf-8 -*-
from __future__ import annotations

import os
os.environ["VLLM_USE_V1"] = "0"

"""
llmlora vLLM 推理评估与 Benchmark 验证脚本 / vLLM Evaluation & benchmark script.

在 test.jsonl 上使用 vLLM 评估微调模型：
Evaluates the fine-tuned model on test.jsonl using vLLM:

- JSON 合法解析率 / JSON validity rate
- 密级 (final_level) 准确率 / Sensitivity level accuracy
- 无痕抹平零泄漏率（规则引擎复扫） / Zero-leakage rate
- vLLM 推理延迟统计（mean / P50 / P95 / max） / Latency statistics

用法 / Usage:
    # 默认评估合并模型 / Merged model evaluation
    python -m llmlora.scripts.vllm_evaluate \
        --model-path llmlora/output/models/Qwen3.5-0.8B-Privacy-Classifier-Smoother

    # 带 LoRA adapter 评估 / Evaluation with adapter
    python -m llmlora.scripts.vllm_evaluate \
        --model-path llmlora/basemodels/qwen3.5-0.8b \
        --adapter-path llmlora/output/saves/qwen35-privacy-lora
"""
import argparse
import json
import os
import sys
import time

os.environ["VLLM_USE_V1"] = "0"
from pathlib import Path
from typing import Any, Dict, List, Optional

# 支持从任意工作目录启动 / Allow launching from any cwd
_LLMLORA_DIR = Path(__file__).resolve().parents[1]
_MODEL_TRAINING_DIR = _LLMLORA_DIR.parent
_ENGINE_DIR = _MODEL_TRAINING_DIR.parent
_REPO_ROOT = _ENGINE_DIR.parent.parent

for _p in (_MODEL_TRAINING_DIR, _REPO_ROOT):
    if str(_p) not in sys.path:
        sys.path.insert(0, str(_p))

from llmlora.src.dataset.loader import extract_user_input, load_jsonl  # noqa: E402
from llmlora.src.inference.vllm_engine import QwenPrivacyVLLMEngine  # noqa: E402
from llmlora.src.utils.metrics import (  # noqa: E402
    calculate_classification_metrics,
    calculate_entity_f1,
    summarize_latency,
)


def _build_leakage_scanner(rules_dir: str):
    """构建规则引擎复扫器；导入失败时降级为仅字面检查。"""
    try:
        from engine.dynclassification.engine import ConfigurableRuleEngine
        from engine.dynclassification.profile_loader import ProfileLoader

        loader = ProfileLoader(rules_dir)
        taxonomy = loader.load_taxonomy("default")
        profiles = [loader.load_profile(d) for d in ("general-pii", "medical")]
        engine = ConfigurableRuleEngine(
            taxonomy=taxonomy, profiles=profiles, domain="llmlora-eval"
        )

        def _scan(text: str) -> bool:
            tags, _ = engine.evaluate("content", text)
            return any((taxonomy.levels.get(t.level).rank if taxonomy.levels.get(t.level) else 0) >= 2 for t in tags)

        return _scan, f"规则引擎复扫（{engine.rule_count} 条规则）"
    except Exception as exc:  # noqa: BLE001 - 评估降级而非失败
        print(f"[WARN] 规则引擎不可用，零泄漏仅做字面残留检查: {exc}")
        return None, "仅字面残留检查"


def run_vllm_evaluation(
    model_path: str,
    adapter_path: Optional[str],
    test_data_path: str,
    rules_dir: str,
    max_samples: int = 0,
    gpu_memory_utilization: float = 0.8,
) -> Dict[str, Any]:
    """执行完整 vLLM Benchmark 评估."""
    print(f"正在加载 vLLM 推理引擎, model_path: {model_path}, adapter_path: {adapter_path}")
    engine = QwenPrivacyVLLMEngine(
        model_path=model_path,
        adapter_path=adapter_path,
        gpu_memory_utilization=gpu_memory_utilization,
    )

    print(f"读取测试集数据: {test_data_path}")
    test_samples = load_jsonl(test_data_path)
    if max_samples > 0:
        test_samples = test_samples[:max_samples]
    print(f"测试集中包含 {len(test_samples)} 条样本")

    scanner, scanner_desc = _build_leakage_scanner(rules_dir)

    predictions: List[str] = []
    references: List[Dict[str, Any]] = []
    latencies_ms: List[float] = []
    leak_checked = 0
    leak_clean = 0

    print("开始执行 vLLM 推理...")
    for i, sample in enumerate(test_samples):
        # 与训练侧 loader.extract_user_input 保持一致（instruction+"\n"+input）
        input_text = extract_user_input(sample)
        gt_output_str = sample.get("output", "{}")
        try:
            ref_json = (
                json.loads(gt_output_str)
                if isinstance(gt_output_str, str)
                else gt_output_str
            )
        except json.JSONDecodeError:
            ref_json = {}

        start = time.perf_counter()
        result = engine.classify(input_text)
        latencies_ms.append((time.perf_counter() - start) * 1000.0)

        pred_str = json.dumps(result, ensure_ascii=False) if result else ""
        predictions.append(pred_str)
        references.append(ref_json)

        # 零泄漏校验：对模型输出的 sanitized_text 做规则引擎复扫
        # scanner 不可用（规则引擎降级）时跳过本校验，指标记为 N/A，
        # 杜绝 scanner=None 时 rule_leak 恒 False 造成的静默满分。
        sanitized = str(result.get("sanitized_text", "") or result.get("smoothed_text", "")) if result else ""
        if sanitized and scanner is not None:
            rule_leak = bool(scanner(sanitized))
            leak_checked += 1
            if not rule_leak:
                leak_clean += 1

        if (i + 1) % 10 == 0 or (i + 1) == len(test_samples):
            print(f"已使用 vLLM 评估 [{i + 1}/{len(test_samples)}] 条样本")

    cls_metrics = calculate_classification_metrics(predictions, references)
    entity_metrics = calculate_entity_f1(predictions, references)
    latency = summarize_latency(latencies_ms)
    # scanner 缺失或无 sanitized_text 可检时指标为 None（N/A），不得按满分计
    zero_leak_available = scanner is not None and leak_checked > 0
    zero_leak_rate: Optional[float] = (
        leak_clean / leak_checked if zero_leak_available else None
    )

    print("\n" + "=" * 56)
    print("vLLM 评估完成，结果报告:")
    print(f"  JSON 格式合法解析率 : {cls_metrics['json_valid_rate'] * 100:.2f}%")
    print(f"  分类密级 Accuracy  : {cls_metrics['level_accuracy'] * 100:.2f}%")
    if zero_leak_rate is not None:
        print(f"  二次扫描零泄漏率    : {zero_leak_rate * 100:.2f}% ({scanner_desc}, n={leak_checked})")
    else:
        print(f"  二次扫描零泄漏率    : N/A ({scanner_desc}，指标不可用，不计分)")
    print(
        f"  vLLM 推理延迟       : mean={latency['mean']:.1f}ms "
        f"p50={latency['p50']:.1f}ms p95={latency['p95']:.1f}ms max={latency['max']:.1f}ms"
    )
    print("=" * 56)

    return {
        **cls_metrics,
        **entity_metrics,
        "zero_leakage_rate": zero_leak_rate,
        "zero_leak_rate_available": zero_leak_available,
        "latency": latency,
    }


def main() -> None:
    """vLLM 评估入口."""
    parser = argparse.ArgumentParser(description="使用 vLLM 评估 llmlora 模型效果与延迟")
    parser.add_argument(
        "--model-path", type=str,
        default=str(_LLMLORA_DIR / "output" / "models" / "Qwen3.5-0.8B-Privacy-Classifier-Smoother"),
        help="基座模型路径或合并后的模型路径",
    )
    parser.add_argument(
        "--adapter-path", type=str,
        default=str(_LLMLORA_DIR / "output" / "saves" / "qwen35-privacy-lora"),
        help="LoRA 适配器路径（目录不存在时自动忽略；合并模型默认不叠加）",
    )
    parser.add_argument(
        "--test-data-path", type=str,
        default=str(_LLMLORA_DIR / "data" / "test.jsonl"),
        help="测试集路径",
    )
    parser.add_argument(
        "--rules-dir", type=str,
        default=str(_ENGINE_DIR / "rules" if (_ENGINE_DIR / "rules").exists() else _REPO_ROOT / "rules"),
        help="规则库目录（零泄漏复扫用）",
    )
    parser.add_argument("--max-samples", type=int, default=0, help="最多评估条数（0=全部）")
    parser.add_argument("--gpu-memory-utilization", type=float, default=0.8, help="vLLM GPU 显存利用率")
    args = parser.parse_args()

    adapter_explicit = "--adapter-path" in sys.argv
    adapter_path: Optional[str] = (
        args.adapter_path if Path(args.adapter_path).exists() else None
    )
    if (
        adapter_path is not None
        and not adapter_explicit
        and "merged" in Path(args.model_path).name.lower()
    ):
        print(
            f"检测到合并模型 ({args.model_path})，跳过默认 LoRA 适配器挂载"
            f"（如需叠加请显式传 --adapter-path）"
        )
        adapter_path = None

    run_vllm_evaluation(
        args.model_path,
        adapter_path,
        args.test_data_path,
        args.rules_dir,
        args.max_samples,
        args.gpu_memory_utilization,
    )


if __name__ == "__main__":
    main()
