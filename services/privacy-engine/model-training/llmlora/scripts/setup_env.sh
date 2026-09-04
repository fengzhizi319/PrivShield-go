#!/usr/bin/env bash
# 搭建 llmlora 独立训练环境（transformers 5.x，继承系统 torch）
# Setup the isolated llmlora training venv (transformers 5.x, inheriting system torch).
#
# 用法 / Usage:
#   ./llmlora/scripts/setup_env.sh              # 使用默认 python3
#   PYTHON_BIN=/path/to/python ./llmlora/scripts/setup_env.sh   # 指定解释器
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LLMLORA_DIR="$(dirname "$SCRIPT_DIR")"
VENV_DIR="$LLMLORA_DIR/.venv"
VENV_PY="$VENV_DIR/bin/python"
PYTHON_BIN="${PYTHON_BIN:-python3}"

if [[ -x "$VENV_PY" ]]; then
    echo "[setup_env] 已存在虚拟环境: $VENV_DIR，跳过创建"
else
    echo "[setup_env] 使用 $PYTHON_BIN 创建虚拟环境 (--system-site-packages 继承 torch)..."
    "$PYTHON_BIN" -m venv --system-site-packages "$VENV_DIR"
fi

echo "[setup_env] 安装/升级训练依赖 (transformers==5.14.1, peft, accelerate, datasets, faker)..."
"$VENV_PY" -m pip install --upgrade pip
"$VENV_PY" -m pip install \
    "transformers==5.14.1" \
    peft \
    accelerate \
    datasets \
    faker \
    pyyaml

echo "[setup_env] 环境校验:"
"$VENV_PY" - <<'EOF'
import torch, transformers, peft
print(f"  torch        = {torch.__version__} (cuda={torch.cuda.is_available()})")
print(f"  transformers = {transformers.__version__}")
print(f"  peft         = {peft.__version__}")
assert int(transformers.__version__.split('.')[0]) >= 5, "transformers 版本必须 >= 5.2"
EOF
echo "[setup_env] 完成 ✅"
