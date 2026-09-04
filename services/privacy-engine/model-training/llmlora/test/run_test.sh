#!/usr/bin/env bash
# 推理性能对比测试启动脚本 / Performance Test Launcher Script.
#
# 用法 / Usage:
#   ./llmlora/test/run_test.sh                          # 运行 PyTorch vs vLLM 全量对比
#   ./llmlora/test/run_test.sh --pytorch-only           # 仅测试 PyTorch
#   ./llmlora/test/run_test.sh --vllm-only              # 仅测试 vLLM

set -e
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LLMLORA_DIR="$(dirname "$SCRIPT_DIR")"
MODEL_TRAINING_DIR="$(dirname "$LLMLORA_DIR")"
ENGINE_DIR="$(dirname "$MODEL_TRAINING_DIR")"
REPO_ROOT="$(cd "$ENGINE_DIR/../.." && pwd)"

VENV_PY="$LLMLORA_DIR/.venv/bin/python"
if [ ! -f "$VENV_PY" ]; then
    VENV_PY="python3"
fi

# 确保 venv bin 目录在 PATH 中（ninja 等工具）
export PATH="$LLMLORA_DIR/.venv/bin:$PATH"
export PYTHONPATH="$MODEL_TRAINING_DIR:$REPO_ROOT:${PYTHONPATH:-}"

export VLLM_USE_V1=0
export VLLM_USE_V2_MODEL_RUNNER=0
export VLLM_ENABLE_V1_MULTIPROCESSING=0

cd "$REPO_ROOT"

if [[ "$1" == "--pytorch-only" ]]; then
    exec "$VENV_PY" -m llmlora.test.benchmark_pytorch "${@:2}"
elif [[ "$1" == "--vllm-only" ]]; then
    exec "$VENV_PY" -m llmlora.test.benchmark_vllm "${@:2}"
else
    exec "$VENV_PY" -m llmlora.test.run_benchmark_comparison "$@"
fi
