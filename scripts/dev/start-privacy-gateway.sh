#!/usr/bin/env bash
# 启动 Privacy Gateway 反向代理与负载均衡网关 (开发模式)
set -euo pipefail
export CGO_ENABLED=0
export NO_PROXY="*"
export no_proxy="*"

for arg in "$@"; do
    case "$arg" in
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "说明: 启动 PrivShield Privacy Gateway 网关 (REST :8000 + gRPC :50000)"
            echo "环境变量支持: ENGINE_GATEWAY_PORT, ENGINE_GATEWAY_BACKENDS, ENGINE_GATEWAY_STRATEGY"
            exit 0
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || (cd "$SCRIPT_DIR/../.." && pwd -P))"

cd "$PROJECT_ROOT"

export ENGINE_GATEWAY_HOST="${ENGINE_GATEWAY_HOST:-127.0.0.1}"
export ENGINE_GATEWAY_PORT="${ENGINE_GATEWAY_PORT:-8000}"
export ENGINE_GATEWAY_BACKENDS="${ENGINE_GATEWAY_BACKENDS:-127.0.0.1:8079}"
export ENGINE_GATEWAY_STRATEGY="${ENGINE_GATEWAY_STRATEGY:-p2c}"
export ENGINE_GATEWAY_LOG_LEVEL="${ENGINE_GATEWAY_LOG_LEVEL:-INFO}"

echo "=== PrivShield Privacy Engine Gateway (Dev) ==="
echo "Gateway REST: http://${ENGINE_GATEWAY_HOST}:${ENGINE_GATEWAY_PORT}"
echo "Backends:     ${ENGINE_GATEWAY_BACKENDS}"
echo "Strategy:     ${ENGINE_GATEWAY_STRATEGY}"
echo ""

if [[ -f "$PROJECT_ROOT/services/privacy-engine/bin/privshield-gateway" ]]; then
    exec "$PROJECT_ROOT/services/privacy-engine/bin/privshield-gateway" "$@"
elif [[ -f "$PROJECT_ROOT/bin/privshield-gateway" ]]; then
    exec "$PROJECT_ROOT/bin/privshield-gateway" "$@"
else
    exec go run ./services/privacy-engine/cmd/privshield-gateway "$@"
fi
