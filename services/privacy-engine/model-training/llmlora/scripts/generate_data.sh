#!/usr/bin/env bash
# 生成训练/验证/测试数据（规则引擎打标 + 零泄漏 QA），输出到 llmlora/data/
# Generate train/dev/test datasets (rule-engine labeling + zero-leakage QA).
#
# 用法 / Usage:
#   ./llmlora/scripts/generate_data.sh                                # 默认 1000/100/50
#   ./llmlora/scripts/generate_data.sh --train-size 2000 --seed 123   # 自定义参数透传
source "$(dirname "${BASH_SOURCE[0]}")/_common.sh"

if [[ $# -eq 0 ]]; then
    set -- --train-size 1000 --dev-size 100 --test-size 50
fi

exec "$VENV_PY" -m llmlora.scripts.generate_data "$@"
