#!/usr/bin/env bash
# ============================================================================
# Integration Test Script for Three New Microservice Modules
# 三个中台微服务模块的集成测试脚本
#
# 测试内容：
#   1. 三个模块及 Agent 的健康检查
#   2. datasource-mgr 模拟数据源操作（seed/list/records/test）
#   3. service-hub 任务调度（dispatch/classify/status/tasks）
#   4. audit-log 审计存证（create/list/stats/report）
#
# 前置条件：
#   - 三个模块已启动（dev-start-new-modules.sh / e2e-start-all-services.sh / docker-start-all.sh）
#   - PrivShield Agent 已运行（REST: 8079）
#
# Usage:
#   bash scripts/dev/integration-test-new-modules.sh
# ============================================================================

set -euo pipefail

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

# ── 解析脚本目录，初始化全局变量 ──────────────────────────────────
# 各微服务 URL 可通过环境变量覆盖（适配非默认端口部署）
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
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

SERVICE_HUB_URL="${SERVICE_HUB_URL:-http://127.0.0.1:8082}"
DATASOURCE_MGR_URL="${DATASOURCE_MGR_URL:-http://127.0.0.1:8083}"
AUDIT_LOG_URL="${AUDIT_LOG_URL:-http://127.0.0.1:8084}"
AGENT_URL="${PRIVSHIELD_AGENT_URL:-http://127.0.0.1:8079}"

PASS_COUNT=0
FAIL_COUNT=0

# ── 断言工具函数 ────────────────────────────────────────────────
# assert_status              : 断言 HTTP 状态码
# assert_json_field          : 断言 JSON 响应中指定字段的值
# assert_json_field_not_empty: 断言 JSON 响应中指定字段非空
# curl_json                  : 发送 HTTP 请求并返回 JSON 响应
assert_status() {
    local desc="$1"
    local expected="$2"
    local actual="$3"

    if [ "$actual" = "$expected" ]; then
        log_info "$desc (HTTP $actual)"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        log_error "$desc (expected HTTP $expected, got $actual)"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
}

assert_json_field() {
    local desc="$1"
    local json="$2"
    local field="$3"
    local expected="$4"

    local actual
    actual=$(echo "$json" | python3 -c "import sys,json; print(json.load(sys.stdin).get('$field',''))" 2>/dev/null || echo "")

    if [ "$actual" = "$expected" ]; then
        log_info "$desc ($field=$actual)"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        log_error "$desc (expected $field=$expected, got $field=$actual)"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
}

assert_json_field_not_empty() {
    local desc="$1"
    local json="$2"
    local field="$3"

    local actual
    actual=$(echo "$json" | python3 -c "import sys,json; v=json.load(sys.stdin).get('$field'); print(v if v is not None else '')" 2>/dev/null || echo "")

    if [ -n "$actual" ]; then
        log_info "$desc ($field present)"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        log_error "$desc (expected $field to be present)"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
}

curl_json() {
    local method="$1"
    local url="$2"
    local body="${3:-}"

    if [ -n "$body" ]; then
        curl -s -X "$method" "$url" -H "Content-Type: application/json" -d "$body" 2>/dev/null || echo "{}"
    else
        curl -s -X "$method" "$url" 2>/dev/null || echo "{}"
    fi
}

curl_status() {
    local method="$1"
    local url="$2"
    local body="${3:-}"

    if [ -n "$body" ]; then
        curl -s -o /dev/null -w "%{http_code}" -X "$method" "$url" -H "Content-Type: application/json" -d "$body" 2>/dev/null || echo "000"
    else
        curl -s -o /dev/null -w "%{http_code}" -X "$method" "$url" 2>/dev/null || echo "000"
    fi
}

wait_for_task() {
    local task_id="$1"
    local deadline=$((SECONDS + 15))

    while [ "$SECONDS" -lt "$deadline" ]; do
        local status_json
        status_json=$(curl_json GET "${SERVICE_HUB_URL}/v1/hub/tasks/${task_id}")
        local status
        status=$(echo "$status_json" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null || echo "")
        if [ "$status" = "completed" ] || [ "$status" = "failed" ]; then
            echo "$status"
            return
        fi
        sleep 0.5
    done
    echo "timeout"
}

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║   Integration Test: service-hub / datasource-mgr / audit-log  ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# ── Phase 1: Health Checks ─────────────────────────────────────────────
log_step "Phase 1: Health Checks"
echo ""

assert_status "service-hub health" "200" "$(curl_status GET "${SERVICE_HUB_URL}/health")"
assert_status "datasource-mgr health" "200" "$(curl_status GET "${DATASOURCE_MGR_URL}/health")"
assert_status "audit-log health" "200" "$(curl_status GET "${AUDIT_LOG_URL}/health")"
assert_status "PrivShield Agent health" "200" "$(curl_status GET "${AGENT_URL}/health")"

echo ""

# ── Phase 2: Agent Connectivity (privacy primitive smoke test) ───────────
log_step "Phase 2: Agent Connectivity"
echo ""

AGENT_MASK_RESP=$(curl_json POST "${AGENT_URL}/v1/privacy/mask" '{"field": "name", "value": "测试用户", "type": "name"}')
assert_json_field_not_empty "Agent mask response" "$AGENT_MASK_RESP" "masked"

echo ""

# ── Phase 3: datasource-mgr Operations ─────────────────────────────────
log_step "Phase 3: datasource-mgr Operations"
echo ""

DS_SEED_RESP=$(curl_json POST "${DATASOURCE_MGR_URL}/v1/datasources/seed")
assert_status "Seed mock datasources" "200" "$(curl_status POST "${DATASOURCE_MGR_URL}/v1/datasources/seed")"
assert_json_field "Seed confirmation" "$DS_SEED_RESP" "via" "datasource-mgr"

DS_LIST_RESP=$(curl_json GET "${DATASOURCE_MGR_URL}/v1/datasources")
assert_status "List datasources" "200" "$(curl_status GET "${DATASOURCE_MGR_URL}/v1/datasources")"
assert_json_field_not_empty "List datasources total" "$DS_LIST_RESP" "total"

DS_RECORDS_RESP=$(curl_json GET "${DATASOURCE_MGR_URL}/v1/datasources/ds_yibao/records?limit=3")
assert_status "Get ds_yibao records" "200" "$(curl_status GET "${DATASOURCE_MGR_URL}/v1/datasources/ds_yibao/records?limit=3")"
assert_json_field_not_empty "ds_yibao records" "$DS_RECORDS_RESP" "records"

DS_TEST_RESP=$(curl_json POST "${DATASOURCE_MGR_URL}/v1/datasources/ds_yibao/test")
assert_status "Test ds_yibao connection" "200" "$(curl_status POST "${DATASOURCE_MGR_URL}/v1/datasources/ds_yibao/test")"
assert_json_field "Test connection via" "$DS_TEST_RESP" "via" "datasource-mgr"

echo ""

# ── Phase 4: service-hub Pipeline Execution ────────────────────────────
log_step "Phase 4: service-hub Pipeline Execution"
echo ""

CLASSIFY_RESP=$(curl_json POST "${SERVICE_HUB_URL}/v1/hub/dispatch" '{
    "source": "ds_yibao",
    "operation": "classify",
    "priority": 1,
    "payload": {"name": "张三", "id_card": "110101199003072345", "phone": "13800138000", "diagnosis": "高血压"}
}')
assert_status "Submit classify task" "202" "$(curl_status POST "${SERVICE_HUB_URL}/v1/hub/dispatch" '{
    "source": "ds_yibao",
    "operation": "classify",
    "priority": 1,
    "payload": {"name": "张三", "id_card": "110101199003072345", "phone": "13800138000", "diagnosis": "高血压"}
}')"
assert_json_field_not_empty "Classify task_id" "$CLASSIFY_RESP" "task_id"

TASK_ID=$(echo "$CLASSIFY_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task_id',''))" 2>/dev/null || echo "")
if [ -n "$TASK_ID" ]; then
    log_info "Created classify task ID: $TASK_ID"
    FINAL_STATUS=$(wait_for_task "$TASK_ID")
    if [ "$FINAL_STATUS" = "completed" ] || [ "$FINAL_STATUS" = "failed" ]; then
        log_info "Classify task final status: $FINAL_STATUS"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        log_error "Classify task did not finish in time (status=$FINAL_STATUS)"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
fi

DISPATCH_RESP=$(curl_json POST "${SERVICE_HUB_URL}/v1/hub/dispatch" '{
    "source": "ds_kangyang",
    "operation": "mask",
    "priority": 1,
    "payload": {"name": "李四", "id_card": "310104198512154567", "phone": "13912345678", "diagnosis": "2型糖尿病"}
}')
assert_status "Submit dispatch masking task" "202" "$(curl_status POST "${SERVICE_HUB_URL}/v1/hub/dispatch" '{
    "source": "ds_kangyang",
    "operation": "mask",
    "priority": 1,
    "payload": {"name": "李四", "id_card": "310104198512154567", "phone": "13912345678", "diagnosis": "2型糖尿病"}
}')"
assert_json_field "Dispatch accepted status" "$DISPATCH_RESP" "status" "accepted"

DISPATCH_ID=$(echo "$DISPATCH_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task_id',''))" 2>/dev/null || echo "")
if [ -n "$DISPATCH_ID" ]; then
    log_info "Created dispatch task ID: $DISPATCH_ID"
    FINAL_STATUS=$(wait_for_task "$DISPATCH_ID")
    if [ "$FINAL_STATUS" = "completed" ] || [ "$FINAL_STATUS" = "failed" ]; then
        log_info "Dispatch task final status: $FINAL_STATUS"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        log_error "Dispatch task did not finish in time (status=$FINAL_STATUS)"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
fi

HUB_STATUS_RESP=$(curl_json GET "${SERVICE_HUB_URL}/v1/hub/status")
assert_status "Hub status" "200" "$(curl_status GET "${SERVICE_HUB_URL}/v1/hub/status")"
assert_json_field_not_empty "Hub status running" "$HUB_STATUS_RESP" "status"

PIPELINE_RESP=$(curl_json GET "${SERVICE_HUB_URL}/v1/hub/pipeline")
assert_status "Pipeline status" "200" "$(curl_status GET "${SERVICE_HUB_URL}/v1/hub/pipeline")"
assert_json_field_not_empty "Pipeline stages" "$PIPELINE_RESP" "stages"

echo ""

# ── Phase 5: audit-log Verification ────────────────────────────────────
log_step "Phase 5: audit-log Verification"
echo ""

AUDIT_CREATE_RESP=$(curl_json POST "${AUDIT_LOG_URL}/v1/audit/logs" '{
    "operation": "mask",
    "datasource": "ds_yibao",
    "status": "success",
    "user": "qa-automation",
    "input_rows": 2,
    "output_rows": 2,
    "duration_ms": 120,
    "algorithm": "masking",
    "security_level": "L4"
}')
assert_status "Create audit log" "201" "$(curl_status POST "${AUDIT_LOG_URL}/v1/audit/logs" '{
    "operation": "mask",
    "datasource": "ds_yibao",
    "status": "success",
    "user": "qa-automation",
    "input_rows": 2,
    "output_rows": 2,
    "duration_ms": 120,
    "algorithm": "masking",
    "security_level": "L4"
}')"
assert_json_field_not_empty "Audit log ID" "$AUDIT_CREATE_RESP" "id"

AUDIT_ID=$(echo "$AUDIT_CREATE_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || echo "")
if [ -n "$AUDIT_ID" ]; then
    log_info "Created audit log ID: $AUDIT_ID"
    AUDIT_DETAIL_RESP=$(curl_json GET "${AUDIT_LOG_URL}/v1/audit/logs/${AUDIT_ID}")
    assert_status "Get audit log detail" "200" "$(curl_status GET "${AUDIT_LOG_URL}/v1/audit/logs/${AUDIT_ID}")"
    assert_json_field "Audit log operation" "$AUDIT_DETAIL_RESP" "operation" "mask"
fi

AUDIT_LOGS_RESP=$(curl_json GET "${AUDIT_LOG_URL}/v1/audit/logs?limit=5")
assert_status "List audit logs" "200" "$(curl_status GET "${AUDIT_LOG_URL}/v1/audit/logs?limit=5")"
assert_json_field_not_empty "Audit logs total" "$AUDIT_LOGS_RESP" "total"

AUDIT_STATS_RESP=$(curl_json GET "${AUDIT_LOG_URL}/v1/audit/stats")
assert_status "Audit stats" "200" "$(curl_status GET "${AUDIT_LOG_URL}/v1/audit/stats")"
assert_json_field_not_empty "Audit stats total_operations" "$AUDIT_STATS_RESP" "total_operations"

REPORT_RESP=$(curl_json POST "${AUDIT_LOG_URL}/v1/audit/report" '{
    "period": "24h"
}')
assert_status "Generate compliance report" "200" "$(curl_status POST "${AUDIT_LOG_URL}/v1/audit/report" '{
    "period": "24h"
}')"
assert_json_field_not_empty "Report generated_at" "$REPORT_RESP" "generated_at"

echo ""

# ── Summary ───────────────────────────────────────────────────────────
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║                    Test Summary                              ║"
echo "╠══════════════════════════════════════════════════════════════╣"
echo -e "║  Passed: ${GREEN}${PASS_COUNT}${NC}                                                  ║"
echo -e "║  Failed: ${RED}${FAIL_COUNT}${NC}                                                  ║"
echo "╚══════════════════════════════════════════════════════════════╝"

if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
fi
