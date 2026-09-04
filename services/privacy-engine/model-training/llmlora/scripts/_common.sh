#!/usr/bin/env bash
# 公共前置：定位目录、设置 PYTHONPATH、校验独立虚拟环境、切换到仓库根。
# Shared preamble: locate directories, set PYTHONPATH, verify the isolated venv, cd to repo root.
# 用法（被其他脚本 source）/ Usage (sourced by other scripts):
#   source "$(dirname "${BASH_SOURCE[0]}")/_common.sh"
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LLMLORA_DIR="$(dirname "$SCRIPT_DIR")"
MODEL_TRAINING_DIR="$(dirname "$LLMLORA_DIR")"
ENGINE_DIR="$(dirname "$MODEL_TRAINING_DIR")"
REPO_ROOT="$(cd "$ENGINE_DIR/../.." && pwd)"
VENV_PY="$LLMLORA_DIR/.venv/bin/python"
VENV_BIN="$LLMLORA_DIR/.venv/bin"

# 将 venv bin 加入 PATH（确保 ninja 等工具可被 vLLM flashinfer 找到）
export PATH="$VENV_BIN:$PATH"

# 将 model-training 和 REPO_ROOT 加入 PYTHONPATH，确保可直接 import llmlora.xxx
export PYTHONPATH="$MODEL_TRAINING_DIR:$REPO_ROOT:${PYTHONPATH:-}"

if [[ ! -x "$VENV_PY" ]]; then
    echo "[错误] 未找到独立训练环境: $VENV_PY" >&2
    echo "       请先运行: ./services/privacy-engine/model-training/llmlora/scripts/setup_env.sh" >&2
    exit 1
fi

# 所有 python -m llmlora.* 命令从仓库根目录执行
cd "$REPO_ROOT"
