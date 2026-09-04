#!/usr/bin/env bash
# ============================================================================
# Start All Real Services for E2E Integration Testing (Go Engine)
# 启动全部真实服务（Go 原生引擎版，用于全流程集成测试）
#
# 与 e2e-start-all-services.sh 的区别：
#   - e2e-start-all-services.sh 使用 Python 引擎（$PYTHON -m engine.main）
#   - 本脚本使用 Go 原生引擎（go run ./cmd/privshield-agent）
#
# 启动服务：
#   1. PrivShield Go Agent (REST + gRPC) :8079  — Go 原生分级脱敏核心引擎
#   2. service-hub         (Go)           :8082  — 数据服务调度中枢
#   3. datasource-mgr      (Go)           :8083  — 数据源管理
#   4. audit-log           (Go)           :8084  — 脱敏审计日志
#
# Usage:
#   bash scripts/dev/e2e-start-all-services-go.sh
#
# 停止：
#   bash scripts/dev/e2e-stop-all-services.sh
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
        *) shift ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PIDS_DIR="${PROJECT_ROOT}/.pids"
LOGS_DIR="${PROJECT_ROOT}/.logs"
DATA_DIR="${PROJECT_ROOT}/data"
ENGINE_GO_DIR="${PROJECT_ROOT}/services/privacy-engine"
GO_BIN="${GO_BIN:-go}"

mkdir -p "$PIDS_DIR" "$LOGS_DIR" "$DATA_DIR"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }
log_step()  { echo -e "${CYAN}[STEP]${NC}  $*"; }

check_go() {
    if ! command -v "$GO_BIN" &>/dev/null; then
        for p in /usr/local/go/bin/go /opt/homebrew/bin/go; do
            if [ -x "$p" ]; then GO_BIN="$p"; break; fi
        done
    fi
    if ! command -v "$GO_BIN" &>/dev/null; then
        log_error "Go compiler not found. Set GO_BIN env var."
        exit 1
    fi
    log_info "Go: $GO_BIN ($($GO_BIN version))"
}

wait_for_service() {
    local name="$1"
    local url="$2"
    local max_wait="${3:-30}"
    local count=0
    while [ $count -lt $max_wait ]; do
        if curl -sf -o /dev/null "$url" 2>/dev/null; then
            log_info "$name is ready at $url"
            return 0
        fi
        sleep 1
        count=$((count + 1))
    done
    log_error "$name failed to start within ${max_wait}s at $url"
    return 1
}

_ensure_port_free() {
    local port="$1"
    local name="$2"
    if command -v docker >/dev/null 2>&1; then
        local cids
        cids=$(docker ps -q --filter "publish=$port" 2>/dev/null || true)
        if [[ -n "$cids" ]]; then
            log_warn "Port $port ($name) is occupied by Docker container(s). Automatically stopping..."
            for cid in $cids; do
                docker stop "$cid" >/dev/null 2>&1 || true
            done
            sleep 1
        fi
    fi
}

# ── 1. PrivShield Go Agent (REST + gRPC) ────────────────────────────────
start_go_agent() {
    local port="${PRIVACY_REST_PORT:-8079}"
    local pid_file="${PIDS_DIR}/agent-go.pid"

    _ensure_port_free "$port" "PrivShield Go Agent REST"
    _ensure_port_free "${PRIVACY_GRPC_PORT:-50051}" "PrivShield Go Agent gRPC"

    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "PrivShield Go Agent already running (PID $(cat "$pid_file"))"
        return
    fi

    log_step "Starting PrivShield Go Agent on :${port}..."
    cd "$ENGINE_GO_DIR"
    PRIVACY_REST_HOST=127.0.0.1 PRIVACY_REST_PORT="$port" \
        PRIVACY_GRPC_HOST=127.0.0.1 PRIVACY_GRPC_PORT="${PRIVACY_GRPC_PORT:-50051}" \
        PRIVACY_LOG_LEVEL="${PRIVACY_LOG_LEVEL:-INFO}" \
        nohup "$GO_BIN" run ./cmd/privshield-agent > "${LOGS_DIR}/agent_go_e2e.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "Go Agent started (PID $(cat "$pid_file"))"

    if ! wait_for_service "PrivShield Go Agent" "http://127.0.0.1:${port}/health" 30; then
        log_error "Go Agent startup failed. Check ${LOGS_DIR}/agent_go_e2e.log"
        exit 1
    fi
}

# ── 2. service-hub (Go) ────────────────────────────────────────────────
start_service_hub() {
    local port="${SERVICE_HUB_PORT:-8082}"
    local pid_file="${PIDS_DIR}/service-hub.pid"
    _ensure_port_free "$port" "service-hub"
    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "service-hub already running (PID $(cat "$pid_file"))"
        return
    fi
    log_step "Building & starting service-hub on :${port}..."
    cd "${PROJECT_ROOT}/services/service-hub"
    "$GO_BIN" build -o bin/service-hub ./cmd/server
    SERVICE_HUB_HOST=127.0.0.1 SERVICE_HUB_PORT="$port" \
        SERVICE_HUB_AGENT_REST_HOST=127.0.0.1 SERVICE_HUB_AGENT_REST_PORT=8079 \
        SERVICE_HUB_DB_PATH="${DATA_DIR}/service-hub.db" \
        ./bin/service-hub > "${LOGS_DIR}/service-hub_e2e.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "service-hub started (PID $(cat "$pid_file"))"
    wait_for_service "service-hub" "http://127.0.0.1:${port}/health" 10
}

# ── 3. datasource-mgr (Go) ─────────────────────────────────────────────
start_datasource_mgr() {
    local port="${DATASOURCE_MGR_PORT:-8083}"
    local pid_file="${PIDS_DIR}/datasource-mgr.pid"
    _ensure_port_free "$port" "datasource-mgr"
    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "datasource-mgr already running (PID $(cat "$pid_file"))"
        return
    fi
    log_step "Building & starting datasource-mgr on :${port}..."
    cd "${PROJECT_ROOT}/console/mock-datasource"
    "$GO_BIN" build -o bin/datasource-mgr ./cmd/server
    DATASOURCE_MGR_HOST=127.0.0.1 DATASOURCE_MGR_PORT="$port" \
        DATASOURCE_MGR_AGENT_REST_HOST=127.0.0.1 DATASOURCE_MGR_AGENT_REST_PORT=8079 \
        DATASOURCE_MGR_DB_PATH="${DATA_DIR}/datasource-mgr.db" \
        ./bin/datasource-mgr > "${LOGS_DIR}/datasource-mgr_e2e.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "datasource-mgr started (PID $(cat "$pid_file"))"
    wait_for_service "datasource-mgr" "http://127.0.0.1:${port}/health" 10
}

# ── 4. audit-log (Go) ──────────────────────────────────────────────────
start_audit_log() {
    local port="${AUDIT_LOG_PORT:-8084}"
    local pid_file="${PIDS_DIR}/audit-log.pid"
    _ensure_port_free "$port" "audit-log"
    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "audit-log already running (PID $(cat "$pid_file"))"
        return
    fi
    log_step "Building & starting audit-log on :${port}..."
    cd "${PROJECT_ROOT}/services/audit-log"
    "$GO_BIN" build -o bin/audit-log ./cmd/server
    AUDIT_LOG_HOST=127.0.0.1 AUDIT_LOG_PORT="$port" \
        AUDIT_LOG_AGENT_REST_HOST=127.0.0.1 AUDIT_LOG_AGENT_REST_PORT=8079 \
        AUDIT_LOG_DB_PATH="${DATA_DIR}/audit-log.db" \
        ./bin/audit-log > "${LOGS_DIR}/audit-log_e2e.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "audit-log started (PID $(cat "$pid_file"))"
    wait_for_service "audit-log" "http://127.0.0.1:${port}/health" 10
}

# ── 主流程 ──────────────────────────────────────────────────────────────
cd "$PROJECT_ROOT"
log_step "Checking Go dependencies..."
check_go

echo ""
log_step "Starting all services (Go Engine)..."
echo ""

start_go_agent
start_service_hub
start_datasource_mgr
start_audit_log

echo ""
log_info "╔══════════════════════════════════════════════════════════════╗"
log_info "║     All 4 services (Go Engine) started successfully!        ║"
log_info "╠══════════════════════════════════════════════════════════════╣"
log_info "║  PrivShield Go Agent → http://127.0.0.1:8079  (Go 原生引擎)  ║"
log_info "║  service-hub         → http://127.0.0.1:8082  (调度中枢)     ║"
log_info "║  datasource-mgr      → http://127.0.0.1:8083  (数据源管理)   ║"
log_info "║  audit-log           → http://127.0.0.1:8084  (审计日志)     ║"
log_info "╠══════════════════════════════════════════════════════════════╣"
log_info "║  Run E2E tests:                                              ║"
log_info "║    PRIVSHIELD_E2E=1 go test -v -run TestRealE2E ./services/service-hub/internal/handlers/ ║"
log_info "║  Stop all:                                                   ║"
log_info "║    bash scripts/dev/e2e-stop-all-services.sh                 ║"
log_info "╚══════════════════════════════════════════════════════════════╝"
