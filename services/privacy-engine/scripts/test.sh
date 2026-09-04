#!/usr/bin/env bash
# test.sh — 独立运行隐私引擎与 SDK 全量单元测试
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENGINE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$ENGINE_DIR/../.." && pwd)"

echo "Running Privacy-Engine and SDK unit tests..."
cd "$REPO_ROOT"
CGO_ENABLED=0 go test -v ./services/privacy-engine/... ./services/privacy-engine/sdk/...
