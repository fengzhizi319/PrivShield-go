#!/usr/bin/env bash
# run-agent.sh — 独立启动分类分级与隐私脱敏核心 Agent (:8079 / :50051)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENGINE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$ENGINE_DIR/../.." && pwd)"

export AGENT_REST_HOST="${AGENT_REST_HOST:-0.0.0.0}"
export AGENT_REST_PORT="${AGENT_REST_PORT:-8079}"
export AGENT_GRPC_HOST="${AGENT_GRPC_HOST:-0.0.0.0}"
export AGENT_GRPC_PORT="${AGENT_GRPC_PORT:-50051}"
export AGENT_RULES_DIR="${AGENT_RULES_DIR:-$ENGINE_DIR/rules/domains}"
export AGENT_STANDARDS_DIR="${AGENT_STANDARDS_DIR:-$ENGINE_DIR/rules/standards}"
export AGENT_CONFIG_FILE="${AGENT_CONFIG_FILE:-$REPO_ROOT/config/privacy.yaml}"

echo "Starting PrivShield Privacy-Engine Agent on :$AGENT_REST_PORT (REST) / :$AGENT_GRPC_PORT (gRPC)..."
cd "$REPO_ROOT"
exec go run ./services/privacy-engine/cmd/privshield-agent "$@"
