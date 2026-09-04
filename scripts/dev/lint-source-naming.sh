#!/usr/bin/env bash
# ==============================================================================
# PrivShield Source & Naming Consistency Guard / 源码命名一致性门禁检查
# ==============================================================================
# 检查项：
# 1. 拦截已废弃的非法历史标识字面量（如 mock1, mock2, /api/v1/datasources 等）
# 2. 检查 Go (pkg/naming)、Python (engine/naming.py) 与 TS (web/src/types/naming.ts) 的 SSOT 对齐
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "=== [Lint] Starting PrivShield Source & Naming Consistency Check ==="
echo ""
echo "Checks:"
echo "  [1/4] Deprecated API routes"
echo "  [2/4] Obsolete mock identifiers"
echo "  [3/4] Hardcoded datasource name literals"
echo "  [4/4] Cross-language SSOT parity tests"

ERRORS=0

# 1. 检查遗留错误 URL 路径
echo "[1/3] Checking for deprecated API routes in source code..."
DEPRECATED_ROUTES=(
    "/api/v1/datasources"
    "/api/v1/audit/logs"
    "/api/v1/audit/verify"
)

for route in "${DEPRECATED_ROUTES[@]}"; do
    MATCHES=$(grep -rn "${route}" "${ROOT_DIR}/services" "${ROOT_DIR}/console" "${ROOT_DIR}/pkg" 2>/dev/null | grep -v "_test.go" | grep -v "\.md" | grep -v "//" || true)
    if [ -n "${MATCHES}" ]; then
        echo "❌ ERROR: Found deprecated route '${route}' in active code:"
        echo "${MATCHES}"
        ERRORS=$((ERRORS + 1))
    fi
done

# 2. 检查非法 mock1 / mock2 标识
echo "[2/3] Checking for obsolete mock1 / mock2 identifiers..."
OBSOLETE_IDS=(
    "mock1"
    "mock2"
    "ds_mock1"
    "ds_mock2"
)

for obs in "${OBSOLETE_IDS[@]}"; do
    MATCHES=$(grep -rn "\"${obs}\"" "${ROOT_DIR}/services" "${ROOT_DIR}/console" "${ROOT_DIR}/services/privacy-engine" "${ROOT_DIR}/pkg" 2>/dev/null | grep -v "_test.go" | grep -v "test_" | grep -v "\.md" || true)
    if [ -n "${MATCHES}" ]; then
        echo "❌ ERROR: Found obsolete identifier '${obs}' in active code:"
        echo "${MATCHES}"
        ERRORS=$((ERRORS + 1))
    fi
done

# 3. 检查硬编码数据源名称（应使用 pkg/naming 常量而非裸字符串字面量）
echo "[3/4] Checking for hardcoded datasource name literals..."
HARDCODED_IDS=(
    "ds_yibao"
    "ds_kangyang"
)

for hid in "${HARDCODED_IDS[@]}"; do
    # 排除 pkg/naming（SSOT 定义处）、测试文件、文档、注释、E2E 测试运行器和前端 UI 组件
    # E2E runner 中的字面量是归一化测试的故意输入（验证别名→canonical 映射），必须保留裸字符串
    # 前端 UI 组件中的字面量是 HTML 表单值，无法引用 Go 常量，属于数据契约而非业务逻辑
    MATCHES=$(grep -rn "\"${hid}\"" "${ROOT_DIR}/services" "${ROOT_DIR}/console" "${ROOT_DIR}/pkg" 2>/dev/null \
        | grep -v 'pkg/naming' \
        | grep -v '_test.go' \
        | grep -v 'test_' \
        | grep -v '\.md' \
        | grep -v '// ' \
        | grep -v 'runner/runner.go' \
        | grep -v 'console/.*/web/src/' \
        | grep -v '/dist/' \
        || true)
    if [ -n "${MATCHES}" ]; then
        echo "❌ ERROR: Found hardcoded datasource ID '${hid}' — use naming constants instead:"
        echo "${MATCHES}"
        ERRORS=$((ERRORS + 1))
    fi
done

# 4. 运行 Go 命名一致性单元测试
echo "[4/4] Running Go SSOT Parity Unit Tests..."
cd "${ROOT_DIR}"
if ! CGO_ENABLED=0 go test ./pkg/naming/... > /dev/null 2>&1; then
    echo "❌ ERROR: Go pkg/naming unit tests failed!"
    ERRORS=$((ERRORS + 1))
fi

if [ "${ERRORS}" -eq 0 ]; then
    echo "✅ [Lint Passed] All PrivShield naming standards & SSOT parity verified successfully!"
    exit 0
else
    echo "❌ [Lint Failed] Found ${ERRORS} naming consistency errors."
    exit 1
fi
