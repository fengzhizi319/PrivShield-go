#!/usr/bin/env bash
# ============================================================================
# Privacy Engine E2E Integration Test Script
# 隐私计算引擎端到端 REST API 全量集成测试脚本
#
# 测试内容：
#   1. Go Agent 健康检查（/health, /livez, /readyz）
#   2. Masking 隐私脱敏 API (/v1/privacy/mask, /v1/privacy/mask/record)
#   3. Differential Privacy 差分隐私 API (/v1/privacy/dp/*)
#   4. K-Anonymity K-匿名 API (/v1/privacy/k_anonymize/*)
#   5. Query Obfuscation 查询混淆 API (/v1/privacy/qol/obfuscate)
#   6. LDP 本地差分隐私 API (/v1/privacy/ldp/*)
#   7. Medical 流水线脱敏 API (/v1/medical/sanitize)
#   8. Agent 通用处理流水线 (/v1/agent/process)
#   9. 动态分类分级 API (/v1/dynclassification/classify)
#   10. Ops 运维诊断与指标 (/v1/ops/diagnostics, /metrics)
#
# 前置条件：
#   - Privacy Engine Agent 已启动（端口 REST: 8079）
#
# Usage:
#   bash scripts/dev/test-e2e-privacy-engine.sh
# ============================================================================

set -euo pipefail
export NO_PROXY="*"
export no_proxy="*"

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "选项 / Options:"
            echo "  -h, --help    显示帮助信息并退出"
            exit 0
            ;;
        *)
            shift
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[PASS]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[FAIL]${NC}  $*"; }
log_step()  { echo -e "${CYAN}[STEP]${NC}  $*"; }

AGENT_URL="${PRIVSHIELD_AGENT_URL:-http://127.0.0.1:8079}"

PASS_COUNT=0
FAIL_COUNT=0

assert_http() {
    local desc="$1"
    local method="$2"
    local url="$3"
    local expected_code="${4:-200}"
    local body="${5:-}"

    local code
    if [[ -n "$body" ]]; then
        code=$(curl --noproxy "*" -s -o /tmp/go_test_resp.json -w "%{http_code}" \
            -X "$method" -H "Content-Type: application/json" -d "$body" \
            --max-time 10 "${url}" 2>/dev/null || echo "000")
    else
        code=$(curl --noproxy "*" -s -o /tmp/go_test_resp.json -w "%{http_code}" \
            -X "$method" --max-time 10 "${url}" 2>/dev/null || echo "000")
    fi
    code="${code: -3}"

    if [ "$code" = "$expected_code" ]; then
        log_info "$desc (HTTP ${code})"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        log_error "$desc (HTTP ${code}, expected ${expected_code})"
        log_warn "Response: $(cat /tmp/go_test_resp.json 2>/dev/null | head -c 200)"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
}

echo "============================================================================"
echo "🧪 Go Engine 集成测试"
echo "   Agent URL: ${AGENT_URL}"
echo "============================================================================"

# ── 1. 健康检查 ─────────────────────────────────────────────────────────
log_step "1. 健康检查端点"
assert_http "GET /health" "GET" "${AGENT_URL}/health"
assert_http "GET /livez" "GET" "${AGENT_URL}/livez"
assert_http "GET /readyz" "GET" "${AGENT_URL}/readyz"

# ── 2. Masking 脱敏 ─────────────────────────────────────────────────────
log_step "2. Masking 隐私脱敏"
assert_http "POST /v1/privacy/mask" "POST" "${AGENT_URL}/v1/privacy/mask" "200" \
    '{"field": "phone", "value": "13800138000", "type": "phone"}'

assert_http "POST /v1/privacy/mask/record" "POST" "${AGENT_URL}/v1/privacy/mask/record" "200" \
    '{"record": {"name": "张三", "phone": "13800138000", "id_card": "110101199001011234"}}'

# ── 3. Differential Privacy 差分隐私 ────────────────────────────────────
log_step "3. Differential Privacy 差分隐私"
assert_http "POST /v1/privacy/dp/count" "POST" "${AGENT_URL}/v1/privacy/dp/count" "200" \
    '{"count": 100, "sensitivity": 1.0, "epsilon": 1.0}'

assert_http "POST /v1/privacy/dp/sum" "POST" "${AGENT_URL}/v1/privacy/dp/sum" "200" \
    '{"values": [1.0, 2.0, 3.0], "clip_lower": 0.0, "clip_upper": 10.0, "epsilon": 1.0}'

assert_http "POST /v1/privacy/dp/mean" "POST" "${AGENT_URL}/v1/privacy/dp/mean" "200" \
    '{"values": [1.0, 2.0, 3.0, 4.0, 5.0], "delta": 0.0001, "epsilon": 1.0}'

assert_http "POST /v1/privacy/dp/histogram" "POST" "${AGENT_URL}/v1/privacy/dp/histogram" "200" \
    '{"values": ["A", "B", "A"], "categories": ["A", "B", "C"], "epsilon": 1.0}'

assert_http "POST /v1/privacy/dp/noisy_count" "POST" "${AGENT_URL}/v1/privacy/dp/noisy_count" "200" \
    '{"count": 100, "epsilon": 1.0}'

# ── 4. K-Anonymity K-匿名 ──────────────────────────────────────────────
log_step "4. K-Anonymity K-匿名"
assert_http "POST /v1/privacy/k_anonymize/table" "POST" "${AGENT_URL}/v1/privacy/k_anonymize/table" "200" \
    '{"records": [{"age": "25", "city": "Beijing"}, {"age": "26", "city": "Shanghai"}], "k": 2, "qi_cols": ["age", "city"]}'

# ── 5. Query Obfuscation 查询混淆 ──────────────────────────────────────
log_step "5. Query Obfuscation 查询混淆"
assert_http "POST /v1/privacy/qol/obfuscate" "POST" "${AGENT_URL}/v1/privacy/qol/obfuscate" "200" \
    '{"query": "SELECT * FROM patients WHERE name = '\''张三'\''", "num_decoys": 3, "domain": "medical"}'

# ── 6. LDP 本地差分隐私 ────────────────────────────────────────────────
log_step "6. LDP 本地差分隐私"
assert_http "POST /v1/privacy/ldp/perturb/binary" "POST" "${AGENT_URL}/v1/privacy/ldp/perturb/binary" "200" \
    '{"values": [1, 0, 1], "epsilon": 1.0}'

assert_http "POST /v1/privacy/ldp/perturb/categorical" "POST" "${AGENT_URL}/v1/privacy/ldp/perturb/categorical" "200" \
    '{"values": ["A", "B"], "categories": ["A", "B", "C"], "epsilon": 1.0}'

# ── 7. Medical 医疗流水线 ──────────────────────────────────────────────
log_step "7. Medical 医疗合规脱敏"
assert_http "POST /v1/medical/sanitize" "POST" "${AGENT_URL}/v1/medical/sanitize" "200" \
    '{"record": {"name": "张三", "phone": "13800138000", "id_card_no": "110101199001011234"}, "domain": "yibao"}'

# ── 8. Agent 通用流水线 ────────────────────────────────────────────────
log_step "8. Agent 通用处理流水线"
assert_http "POST /v1/agent/process" "POST" "${AGENT_URL}/v1/agent/process" "200" \
    '{"records": [{"name": "李四", "phone": "13900139000"}]}'

# ── 9. 动态分类分级 ────────────────────────────────────────────────────
log_step "9. 动态分类分级"
assert_http "POST /v1/dynclassification/classify" "POST" "${AGENT_URL}/v1/dynclassification/classify" "200" \
    '{"field": "id_card_no", "value": "110101199003072381"}'

# ── 10. Ops 运维诊断与指标 ────────────────────────────────────────────
log_step "10. Ops 诊断与 Prometheus 指标"
assert_http "GET /v1/ops/diagnostics" "GET" "${AGENT_URL}/v1/ops/diagnostics"
assert_http "GET /metrics" "GET" "${AGENT_URL}/metrics"

# ── 结果汇总 ────────────────────────────────────────────────────────────
echo ""
echo "============================================================================"
echo "📊 Go Engine 集成测试结果汇总"
echo "   ✅ 通过: ${PASS_COUNT}"
echo "   ❌ 失败: ${FAIL_COUNT}"
echo "   📝 总计: $((PASS_COUNT + FAIL_COUNT))"
echo "============================================================================"

if [ "$FAIL_COUNT" -eq 0 ]; then
    echo -e "${GREEN}🎉 恭喜！Go Engine 所有 REST API 端点集成测试 100% 全部通过！${NC}"
    exit 0
else
    echo -e "${RED}❌ 存在 ${FAIL_COUNT} 个失败测试，请检查 Go 引擎服务状态。${NC}"
    exit 1
fi
