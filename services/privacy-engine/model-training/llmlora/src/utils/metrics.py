# -*- coding: utf-8 -*-
"""
llmlora 评估指标工具模块 / Evaluation metrics utilities.

包含 / Includes:
- JSON 合法解析率 / JSON validity rate
- 密级 (final_level) 准确率 / Sensitivity level accuracy
- Zero-Leakage 零泄漏校验 / Zero-leakage verification helpers
- 推理延迟统计 / Inference latency statistics
"""
from __future__ import annotations

import json
import re
from typing import Any, Dict, List, Optional

# ```json ... ``` 代码块包裹匹配 / Markdown fenced JSON block matcher
_FENCE_PATTERN = re.compile(r"```(?:json)?\s*(.*?)\s*```", re.DOTALL)
# 最外层花括号兜底匹配 / Fallback matcher for the outermost braces
_BRACE_PATTERN = re.compile(r"\{.*\}", re.DOTALL)


def extract_json_from_text(text: str) -> Optional[Dict[str, Any]]:
    """从文本中提取 JSON（支持 markdown 代码块包裹与直接 JSON 串）。

    Extract JSON from model output (supports markdown fences and raw JSON).

    Returns:
        解析成功返回 dict，否则 None / Parsed dict on success, else None.
    """
    if not text:
        return None

    # 优先匹配 ```json ... ``` 包裹内容 / Prefer fenced code blocks
    match = _FENCE_PATTERN.search(text)
    json_str = match.group(1).strip() if match else text.strip()

    try:
        data = json.loads(json_str)
        if isinstance(data, dict):
            return data
    except (json.JSONDecodeError, ValueError):
        pass

    # 兜底：查找最外层 {} / Fallback: outermost braces
    brace_match = _BRACE_PATTERN.search(text)
    if brace_match:
        try:
            data = json.loads(brace_match.group(0))
            if isinstance(data, dict):
                return data
        except (json.JSONDecodeError, ValueError):
            pass

    return None


def evaluate_json_validity(predictions: List[str]) -> float:
    """计算模型输出可解析为合法 JSON 的比例 / JSON parse success rate (0~1)."""
    if not predictions:
        return 0.0
    valid_count = sum(
        1 for pred in predictions if extract_json_from_text(pred) is not None
    )
    return valid_count / len(predictions)


def _get_entities(parsed: Dict[str, Any]) -> List[Dict[str, Any]]:
    """安全提取 classification.entities 列表 / Safely extract entities list."""
    entities = parsed.get("classification", {}).get("entities", [])
    if not isinstance(entities, list):
        return []
    return [e for e in entities if isinstance(e, dict)]


def calculate_classification_metrics(
    predictions: List[str], references: List[Dict[str, Any]]
) -> Dict[str, float]:
    """计算 JSON 合法率与密级准确率 / Compute JSON validity and level accuracy.

    新格式（精简版）：{"final_level": "L3", "confidence": 0.95, "reasoning": "...", "sanitized_text": "..."}
    New format (slim): {"final_level": "L3", "confidence": 0.95, "reasoning": "...", "sanitized_text": "..."}

    Args:
        predictions: 模型原始输出字符串列表 / Raw model output strings.
        references: Ground Truth JSON 对象列表 / Ground-truth JSON objects.

    Returns:
        {"json_valid_rate", "level_accuracy"} 两个 0~1 指标。
    """
    total = len(references)
    if total == 0:
        return {"level_accuracy": 0.0, "json_valid_rate": 0.0}

    valid_json_cnt = 0
    level_match_cnt = 0

    for pred, ref in zip(predictions, references):
        pred_json = extract_json_from_text(pred)
        if pred_json is None:
            continue
        valid_json_cnt += 1

        # 新格式：顶层 final_level / New format: top-level final_level
        pred_level = pred_json.get("final_level", "")
        ref_level = ref.get("final_level", "")
        # 兼容旧格式 / Backward compatibility with old format
        if not pred_level:
            pred_level = pred_json.get("classification", {}).get("max_level", "")
        if not ref_level:
            ref_level = ref.get("classification", {}).get("max_level", "")
        if pred_level and pred_level == ref_level:
            level_match_cnt += 1

    return {
        "json_valid_rate": valid_json_cnt / total,
        "level_accuracy": level_match_cnt / total,
    }


def calculate_entity_f1(
    predictions: List[str], references: List[Dict[str, Any]]
) -> Dict[str, float]:
    """计算实体级 Micro Precision / Recall / F1（兼容新旧格式）。

    Compute micro entity Precision / Recall / F1 (backward-compatible).

    新格式无 entities 字段，此时所有指标返回 0（分类密级准确率由
    calculate_classification_metrics 负责）。
    The new slim format has no entities field; all metrics return 0 in that
    case (level accuracy is handled by calculate_classification_metrics).
    """
    tp = fp = fn = 0
    level_agree = 0
    has_entities = False

    for pred, ref in zip(predictions, references):
        pred_json = extract_json_from_text(pred) if pred else None
        pred_entities = _get_entities(pred_json) if pred_json else []
        ref_entities = _get_entities(ref)

        if ref_entities or pred_entities:
            has_entities = True

        # 按 text 建索引（同 text 多次出现取首个） / Index by text (first occurrence wins)
        ref_by_text: Dict[str, Dict[str, Any]] = {}
        for entity in ref_entities:
            text = str(entity.get("text", ""))
            if text and text not in ref_by_text:
                ref_by_text[text] = entity

        matched_texts = set()
        for entity in pred_entities:
            text = str(entity.get("text", ""))
            if text in ref_by_text and text not in matched_texts:
                tp += 1
                matched_texts.add(text)
                if entity.get("level") == ref_by_text[text].get("level"):
                    level_agree += 1
            else:
                fp += 1

        fn += len(ref_by_text) - len(matched_texts)

    # 新格式无 entities，返回零值表示不适用
    # New slim format has no entities; return zeros to indicate N/A
    if not has_entities:
        return {
            "entity_precision": 0.0,
            "entity_recall": 0.0,
            "entity_f1": 0.0,
            "entity_level_agreement": 0.0,
        }

    precision = tp / (tp + fp) if (tp + fp) else 0.0
    recall = tp / (tp + fn) if (tp + fn) else 0.0
    f1 = (
        2 * precision * recall / (precision + recall)
        if (precision + recall)
        else 0.0
    )
    return {
        "entity_precision": precision,
        "entity_recall": recall,
        "entity_f1": f1,
        "entity_level_agreement": level_agree / tp if tp else 0.0,
    }


def find_leaked_values(text: str, sensitive_values: List[str]) -> List[str]:
    """检查抹平文本中是否残留原始敏感值 / Check residual sensitive values in smoothed text.

    Args:
        text: 待检查文本（通常是 smoothed_text） / Text to scan.
        sensitive_values: 原始敏感实体值列表 / Original sensitive entity values.

    Returns:
        仍出现在 text 中的敏感值列表（空列表 = 零泄漏）。
        Values still present in text (empty list = zero leakage).
    """
    if not text:
        return list(dict.fromkeys(v for v in sensitive_values if v))
    return [v for v in sensitive_values if v and v in text]


def summarize_latency(latencies_ms: List[float]) -> Dict[str, float]:
    """汇总推理延迟统计 / Summarize inference latency statistics.

    Args:
        latencies_ms: 单次推理耗时列表（毫秒） / Per-request latency in ms.

    Returns:
        {"count", "mean", "p50", "p95", "max"} 毫秒值。
    """
    if not latencies_ms:
        return {"count": 0, "mean": 0.0, "p50": 0.0, "p95": 0.0, "max": 0.0}
    ordered = sorted(latencies_ms)
    n = len(ordered)

    def _percentile(p: float) -> float:
        idx = min(n - 1, max(0, int(round(p / 100.0 * (n - 1)))))
        return ordered[idx]

    return {
        "count": n,
        "mean": sum(ordered) / n,
        "p50": _percentile(50),
        "p95": _percentile(95),
        "max": ordered[-1],
    }
