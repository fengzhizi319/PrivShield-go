#!/usr/bin/env bash
# vLLM Benchmark 评估 (使用 vLLM 引擎加速推理，与原生 PyTorch evaluate.sh 做对比)
# Benchmark evaluation using high-performance vLLM engine.
#
# 用法 / Usage:
#   ./llmlora/scripts/vllm_evaluate.sh                                             # 默认评估合并模型
#   ./llmlora/scripts/vllm_evaluate.sh --max-samples 20                            # 限制评估条数
#   ./llmlora/scripts/vllm_evaluate.sh --model-path llmlora/basemodels/cmeee_merged \
#       --adapter-path llmlora/output/saves/qwen35-cmeee-privacy-lora         # 基座 + LoRA 模式
source "$(dirname "${BASH_SOURCE[0]}")/_common.sh"

export VLLM_USE_V1=0

if [[ $# -eq 0 ]]; then
    set -- --model-path "$LLMLORA_DIR/output/models/Qwen3.5-0.8B-Privacy-Classifier-Smoother"
fi

exec "$VENV_PY" -m llmlora.scripts.vllm_evaluate "$@"
