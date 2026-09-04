#!/usr/bin/env bash
# ============================================================================
# 【开发模式】停止并清理 PrivShield Go 引擎容器
# Stop and remove PrivShield Go Engine Docker container
#
# 用法 / Usage: ./scripts/dev/docker-stop-go-agent.sh
# ============================================================================

set -euo pipefail

echo "============================================================================"
echo "🛑 [Docker Mode - Go Engine] 正在停止 PrivShield Go 引擎容器..."
echo "============================================================================"

docker rm -f PrivShield-Go 2>/dev/null || true

echo "✅ PrivShield Go 引擎容器已成功停止与清理！"
echo "============================================================================"
