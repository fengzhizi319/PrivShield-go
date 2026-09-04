#!/usr/bin/env bash
# 启动 Go 隐私计算引擎 Agent (开发模式)
set -euo pipefail
export CGO_ENABLED=0
export NO_PROXY="*"
export no_proxy="*"

for arg in "$@"; do
    case "$arg" in
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "说明: 启动 PrivShield Privacy Engine Agent (REST :8079 + gRPC :50051)"
            echo "环境变量支持: PRIVACY_REST_PORT / AGENT_REST_PORT, PRIVACY_GRPC_PORT / AGENT_GRPC_PORT"
            exit 0
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || (cd "$SCRIPT_DIR/../.." && pwd -P))"

cd "$PROJECT_ROOT"

REST_PORT="${PRIVACY_REST_PORT:-${AGENT_REST_PORT:-8079}}"
GRPC_PORT="${PRIVACY_GRPC_PORT:-${AGENT_GRPC_PORT:-50051}}"
REST_HOST="${PRIVACY_REST_HOST:-${AGENT_REST_HOST:-127.0.0.1}}"
GRPC_HOST="${PRIVACY_GRPC_HOST:-${AGENT_GRPC_HOST:-127.0.0.1}}"

export PRIVACY_REST_HOST="$REST_HOST"
export PRIVACY_REST_PORT="$REST_PORT"
export AGENT_REST_HOST="$REST_HOST"
export AGENT_REST_PORT="$REST_PORT"
export PRIVACY_GRPC_HOST="$GRPC_HOST"
export PRIVACY_GRPC_PORT="$GRPC_PORT"
export AGENT_GRPC_HOST="$GRPC_HOST"
export AGENT_GRPC_PORT="$GRPC_PORT"
export PRIVACY_LOG_LEVEL="${PRIVACY_LOG_LEVEL:-INFO}"

echo "=== PrivShield Privacy Engine Agent (Dev) ==="
echo "REST:  http://${REST_HOST}:${REST_PORT}"
echo "gRPC:  ${GRPC_HOST}:${GRPC_PORT}"
echo ""

if [[ -f "$PROJECT_ROOT/services/privacy-engine/bin/privshield-agent" ]]; then
    exec "$PROJECT_ROOT/services/privacy-engine/bin/privshield-agent" "$@"
elif [[ -f "$PROJECT_ROOT/bin/privshield-agent" ]]; then
    exec "$PROJECT_ROOT/bin/privshield-agent" "$@"
else
    exec go run ./services/privacy-engine/cmd/privshield-agent "$@"
fi
