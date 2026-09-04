#!/usr/bin/env bash
# dev-engine-console.sh — 启动 Privacy Engine 专用管理控制台全家桶 (Agent + Engine Console BFF + Web)
# 对应 console/engine-console，用于专门测试 services/privacy-engine
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
exec "$SCRIPT_DIR/dev-bff-agent.sh" "$@"
