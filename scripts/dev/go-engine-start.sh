#!/usr/bin/env bash
# 启动 Go 引擎 Agent (开发模式)
set -euo pipefail
export CGO_ENABLED=0

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || (cd "$SCRIPT_DIR/../.." && pwd -P))"

cd "$PROJECT_ROOT"

echo "=== PrivShield Go Engine Agent (Dev) ==="
echo "REST:  http://127.0.0.1:8079"
echo "gRPC:  127.0.0.1:50051"
echo ""

export PRIVACY_REST_HOST="${PRIVACY_REST_HOST:-127.0.0.1}"
export PRIVACY_REST_PORT="${PRIVACY_REST_PORT:-8079}"
export PRIVACY_GRPC_PORT="${PRIVACY_GRPC_PORT:-50051}"
export PRIVACY_LOG_LEVEL="${PRIVACY_LOG_LEVEL:-INFO}"

exec go run ./services/privacy-engine/cmd/privshield-agent "$@"
