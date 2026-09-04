#!/usr/bin/env bash
# run-gateway.sh — 独立启动高性能零内存反向代理网关 (:8000 / :50000)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENGINE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$ENGINE_DIR/../.." && pwd)"

export ENGINE_GATEWAY_HTTP_PORT="${ENGINE_GATEWAY_HTTP_PORT:-8000}"
export ENGINE_GATEWAY_GRPC_PORT="${ENGINE_GATEWAY_GRPC_PORT:-50000}"
export ENGINE_GATEWAY_HTTP_BACKENDS="${ENGINE_GATEWAY_HTTP_BACKENDS:-http://127.0.0.1:8079}"
export ENGINE_GATEWAY_GRPC_BACKENDS="${ENGINE_GATEWAY_GRPC_BACKENDS:-127.0.0.1:50051}"

echo "Starting PrivShield Privacy-Engine Gateway on :$ENGINE_GATEWAY_HTTP_PORT (HTTP) / :$ENGINE_GATEWAY_GRPC_PORT (gRPC)..."
cd "$REPO_ROOT"
exec go run ./services/privacy-engine/cmd/privshield-gateway "$@"
