#!/usr/bin/env bash
# LoRA 训练一键启动（默认训练完成后自动合并导出并同步复制至 Agent 部署目录 .models/Qwen3.5-0.8B-Privacy-Classifier-Smoother）
# One-command LoRA training (auto merge & export & copy to .models by default).
#
# 用法 / Usage:
#   ./llmlora/scripts/train.sh                                # 默认 3 epoch, bs=4，自动合并并复制至 .models
#   ./llmlora/scripts/train.sh --epochs 5 --lr 1e-4           # 自定义参数透传
#   ./llmlora/scripts/train.sh --max-steps 10 --no-merge      # 冒烟快跑
#   ./llmlora/scripts/train.sh --no-copy-to-agent             # 仅合并，不自动同步复制到 .models 目录
#   ./llmlora/scripts/train.sh --resume-from-checkpoint <dir> # 断点续训
source "$(dirname "${BASH_SOURCE[0]}")/_common.sh"

exec "$VENV_PY" -m llmlora.scripts.train "$@"

