#!/usr/bin/env bash
# ============================================================================
# 【Docker 模式】停止 PrivShield 调度之眼全栈测试集群 (Go 原生引擎版)
# Stop PrivShield App-LZ Full Stack in Docker Compose (Go Engine)
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo "正在停止 PrivShield App-LZ (Go Engine) 容器集群..."

cd "$PROJECT_ROOT/deploy/docker-compose"
docker compose -f docker-compose.app-lz.yml -f docker-compose.app-lz-go-engine.yml down 2>/dev/null || true

# 清理独立容器
docker rm -f PrivShield PrivShield-Go privshield-service-hub privshield-datasource-mgr privshield-audit-log privshield-app-lz-bff privshield-app-lz-web 2>/dev/null || true

echo "PrivShield App-LZ (Go Engine) 容器服务已全部停止。"
