#!/usr/bin/env bash
# bench.sh — 独立运行 44 项隐私原语基准压测
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENGINE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$ENGINE_DIR/../.." && pwd)"

echo "Running 44 Privacy Primitives Benchmark..."
cd "$REPO_ROOT"
(cd services/privacy-engine/sdk && go test -run '^$' -bench . -benchmem -count=3 ./ldp/... ./masking/... ./kano/... ./dp/...)
(cd services/privacy-engine && go test -run '^$' -bench . -benchmem -count=3 ./internal/dynclassification/... ./internal/service/...)
