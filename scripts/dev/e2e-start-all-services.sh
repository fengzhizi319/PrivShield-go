#!/usr/bin/env bash
# ============================================================================
# Start All Real Services for E2E Integration Testing
# 启动全部真实服务（用于全流程集成测试）
#
# 启动服务：
#   1. PrivShield Agent  (REST + gRPC)  :8079  — 分级脱敏核心引擎
#   2. service-hub       (Go)           :8082  — 数据服务调度中枢
#   3. datasource-mgr    (Go)           :8083  — 数据源管理
#   4. audit-log         (Go)           :8084  — 脱敏审计日志
#
# Usage:
#   bash scripts/dev/e2e-start-all-services.sh
#
# 停止：
#   bash scripts/dev/e2e-stop-all-services.sh
# ============================================================================

set -euo pipefail
export CGO_ENABLED=0

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

# ── 解析脚本目录，初始化全局变量 ──────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || (cd "$SCRIPT_DIR/../.." && pwd -P))"
PIDS_DIR="${PROJECT_ROOT}/.pids"
LOGS_DIR="${PROJECT_ROOT}/.logs"
DATA_DIR="${PROJECT_ROOT}/data"
GO_BIN="${GO_BIN:-go}"

mkdir -p "$PIDS_DIR" "$LOGS_DIR" "$DATA_DIR"

# ── Python 解释器自动探测：优先 venv，回退到系统 python3 ──────────────
if [ -x "${PROJECT_ROOT}/.venv/bin/python" ]; then
    PYTHON="${PYTHON:-${PROJECT_ROOT}/.venv/bin/python}"
else
    PYTHON="${PYTHON:-python3}"
fi

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
            if [ -x "$p" ]; then
                GO_BIN="$p"
                break
            fi
        done
    fi
    if ! command -v "$GO_BIN" &>/dev/null; then
        log_error "Go compiler not found. Set GO_BIN env var."
        exit 1
    fi
    log_info "Go: $GO_BIN ($($GO_BIN version))"
}

check_python() {
    if ! command -v "$PYTHON" &>/dev/null; then
        if [ -x "${PROJECT_ROOT}/.venv/bin/python" ]; then
            PYTHON="${PROJECT_ROOT}/.venv/bin/python"
        fi
    fi
    if ! command -v "$PYTHON" &>/dev/null; then
        log_error "Python not found. Set PYTHON env var."
        exit 1
    fi
    log_info "Python: $PYTHON ($($PYTHON --version))"
}

# ── wait_for_service: HTTP 健康检查轮询，等待服务就绪 ────────────────
# 参数: $1=服务名  $2=健康检查 URL  $3=最大等待秒数（默认 30）
# 返回: 0=就绪  1=超时
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

# ── 每个服务的启动流程：─────────────────────────────────────────────
#   1. 检查 PID 文件，若已运行则跳过（幂等性）
#   2. 设置环境变量并后台启动进程
#   3. 写入 PID 文件
#   4. 通过 HTTP 健康检查轮询等待服务就绪
#   5. 超时则报错退出
#
# ── 1. PrivShield Agent (REST + gRPC) ────────────────────────────────
start_agent() {
    local port="${PRIVACY_REST_PORT:-8079}"
    local pid_file="${PIDS_DIR}/agent.pid"

    _ensure_port_free "$port" "PrivShield Agent REST"
    _ensure_port_free "${PRIVACY_GRPC_PORT:-50051}" "PrivShield Agent gRPC"

    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "PrivShield Agent already running (PID $(cat "$pid_file"))"
        return
    fi

    log_step "Starting PrivShield Go Agent on :${port}..."
    cd "$PROJECT_ROOT"
    if [[ -f "$PROJECT_ROOT/bin/privshield-agent" ]]; then
        PRIVACY_REST_HOST=127.0.0.1 PRIVACY_REST_PORT="$port" \
        PRIVACY_GRPC_HOST=127.0.0.1 PRIVACY_GRPC_PORT="${PRIVACY_GRPC_PORT:-50051}" \
            "$PROJECT_ROOT/bin/privshield-agent" > "${LOGS_DIR}/agent_e2e.log" 2>&1 &
    else
        PRIVACY_REST_HOST=127.0.0.1 PRIVACY_REST_PORT="$port" \
        PRIVACY_GRPC_HOST=127.0.0.1 PRIVACY_GRPC_PORT="${PRIVACY_GRPC_PORT:-50051}" \
            "$GO_BIN" run ./engine-go/cmd/privshield-agent > "${LOGS_DIR}/agent_e2e.log" 2>&1 &
    fi
    echo $! > "$pid_file"
    log_info "Agent started (PID $(cat "$pid_file"))"

    if ! wait_for_service "PrivShield Agent" "http://127.0.0.1:${port}/health" 30; then
        log_error "Agent startup failed. Check ${LOGS_DIR}/agent_e2e.log"
        exit 1
    fi
}

# ── 2. service-hub (Go) ──────────────────────────────────────────────
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
        SERVICE_HUB_AUDIT_LOG_URLS="http://127.0.0.1:${AUDIT_LOG_PORT:-8084}" \
        SERVICE_HUB_DB_PATH="${DATA_DIR}/service-hub.db" \
        ./bin/service-hub > "${LOGS_DIR}/service-hub_e2e.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "service-hub started (PID $(cat "$pid_file"))"

    wait_for_service "service-hub" "http://127.0.0.1:${port}/api/health" 10
}

# ── 3. datasource-mgr (Go) ───────────────────────────────────────────
start_datasource_mgr() {
    local port="${DATASOURCE_MGR_PORT:-8083}"
    local pid_file="${PIDS_DIR}/datasource-mgr.pid"

    _ensure_port_free "$port" "datasource-mgr"

    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "datasource-mgr already running (PID $(cat "$pid_file"))"
        return
    fi

    log_step "Building & starting datasource-mgr on :${port}..."
    cd "${PROJECT_ROOT}/services/datasource-mgr"
    "$GO_BIN" build -o bin/datasource-mgr ./cmd/server

    DATASOURCE_MGR_HOST=127.0.0.1 DATASOURCE_MGR_PORT="$port" \
        DATASOURCE_MGR_AGENT_REST_HOST=127.0.0.1 DATASOURCE_MGR_AGENT_REST_PORT=8079 \
        DATASOURCE_MGR_DB_PATH="${DATA_DIR}/datasource-mgr.db" \
        ./bin/datasource-mgr > "${LOGS_DIR}/datasource-mgr_e2e.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "datasource-mgr started (PID $(cat "$pid_file"))"

    wait_for_service "datasource-mgr" "http://127.0.0.1:${port}/api/health" 10
}

# ── 4. audit-log (Go) ────────────────────────────────────────────────
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

    wait_for_service "audit-log" "http://127.0.0.1:${port}/api/health" 10
}

# ── 主流程：先检查依赖，再按顺序启动全部服务 ────────────────────────
# 启动顺序：Agent → service-hub → datasource-mgr → audit-log
# Agent 必须先启动，因为 Go 微服务依赖 Agent REST API
cd "$PROJECT_ROOT"

log_step "Checking dependencies..."
check_go

echo ""
log_step "Starting all services..."
echo ""

start_agent
start_service_hub
start_datasource_mgr
start_audit_log

echo ""
log_info "╔══════════════════════════════════════════════════════════════╗"
log_info "║          All 4 services started successfully!                ║"
log_info "╠══════════════════════════════════════════════════════════════╣"
log_info "║  PrivShield Agent  → http://127.0.0.1:8079  (分级脱敏引擎)   ║"
log_info "║  service-hub       → http://127.0.0.1:8082  (调度中枢)       ║"
log_info "║  datasource-mgr    → http://127.0.0.1:8083  (数据源管理)     ║"
log_info "║  audit-log         → http://127.0.0.1:8084  (审计日志)       ║"
log_info "╠══════════════════════════════════════════════════════════════╣"
log_info "║  Run E2E tests:                                              ║"
log_info "║    PRIVSHIELD_E2E=1 go test -v -run TestRealE2E ./services/service-hub/internal/handlers/ ║"
log_info "║  Stop all:                                                   ║"
log_info "║    bash scripts/dev/e2e-stop-all-services.sh                 ║"
log_info "╚══════════════════════════════════════════════════════════════╝"
