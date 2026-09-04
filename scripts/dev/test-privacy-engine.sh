#!/usr/bin/env bash
# 运行 PrivShield Privacy Engine 及其核心依赖全量测试
set -euo pipefail

for arg in "$@"; do
    case "$arg" in
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "说明: 运行 Privacy Engine SDK、Engine、Pkg、Services 及 Engine-Console BFF 全量测试"
            exit 0
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo "=== PrivShield Go Engine Tests ==="
echo ""

# services/privacy-engine/sdk 测试
echo "--- services/privacy-engine/sdk tests ---"
cd "$PROJECT_ROOT/services/privacy-engine/sdk"
CGO_ENABLED=0 go test -v -count=1 ./...
echo ""

# services/privacy-engine 测试
echo "--- services/privacy-engine tests ---"
cd "$PROJECT_ROOT/services/privacy-engine"
CGO_ENABLED=0 go test -v -count=1 ./...
echo ""

# pkg 共享库测试
echo "--- pkg tests ---"
cd "$PROJECT_ROOT/pkg"
CGO_ENABLED=0 go test -v -count=1 ./...
echo ""

# services 微服务测试
echo "--- services tests ---"
cd "$PROJECT_ROOT"
CGO_ENABLED=0 go test -v -count=1 ./services/service-hub/... ./console/mock-datasource/... ./services/audit-log/...
echo ""

# console/engine-console/bff-go 测试
echo "--- console/engine-console/bff-go tests ---"
cd "$PROJECT_ROOT/console/engine-console/bff-go"
CGO_ENABLED=0 go test -v -count=1 ./...
echo ""

echo "=== All Go engine tests passed 100% ==="
