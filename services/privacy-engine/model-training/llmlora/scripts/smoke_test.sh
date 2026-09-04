#!/usr/bin/env bash
# 端到端冒烟测试：小规模数据生成 → 10 步训练 + 合并导出 → 快速评估
# End-to-end smoke test: tiny data generation -> 10-step training + merge -> quick evaluation.
source "$(dirname "${BASH_SOURCE[0]}")/_common.sh"

echo "=========================================================="
echo "[1/3] 生成小规模训练数据 (200/40/20)"
echo "=========================================================="
"$VENV_PY" -m llmlora.scripts.generate_data --train-size 200 --dev-size 40 --test-size 20

echo "=========================================================="
echo "[2/3] 冒烟训练 (max-steps=10) + 合并导出"
echo "=========================================================="
"$VENV_PY" -m llmlora.scripts.train --max-steps 10 --batch-size 2 --epochs 1 --no-copy-to-agent

echo "=========================================================="
echo "[3/3] 快速评估合并模型 (10 条样本)"
echo "=========================================================="
"$VENV_PY" -m llmlora.scripts.evaluate \
    --model-path "$LLMLORA_DIR/output/models/Qwen3.5-0.8B-Privacy-Classifier-Smoother" \
    --max-samples 10

echo "[smoke_test] 全链路冒烟通过 ✅"
