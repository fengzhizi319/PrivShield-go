#!/usr/bin/env bash
# ============================================================================
# dev-start-services.sh — 一键启动 PrivShield 隐私计算引擎与中台三大微服务群
#
# 启动服务：
#   1. privshield-agent (Go 隐私计算引擎)     :8079 (gRPC: :50051)
#   2. datasource-mgr   (数据源资产管理)     :8083
#   3. audit-log        (脱敏审计存证日志)   :8084
#   4. service-hub      (数据服务调度中枢)   :8082
#
# 用法 / Usage:
#   bash scripts/dev/dev-start-services.sh [--force]
#
# 选项 / Options:
#   --force      端口被占用时自动终止占用进程（非交互式/CI模式）
#   -h, --help   显示帮助信息并退出
# ============================================================================

set -euo pipefail
export CGO_ENABLED=0
export NO_PROXY="*"
export no_proxy="*"

FORCE=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --force)
            FORCE=true
            shift
            ;;
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "说明: 一键编译并后台启动 PrivShield 隐私计算引擎及三大中台微服务群"
            echo "服务列表:"
            echo "  1. privshield-agent :8079 (gRPC :50051)"
            echo "  2. datasource-mgr   :8083"
            echo "  3. audit-log        :8084"
            echo "  4. service-hub      :8082"
            echo ""
            echo "选项 / Options:"
            echo "  --force       端口被占用时自动杀死占用进程"
            echo "  -h, --help    显示帮助信息并退出"
            exit 0
            ;;
        *)
            echo "未知参数: $1"
            exit 1
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PIDS_DIR="${PROJECT_ROOT}/.pids"
LOGS_DIR="${PROJECT_ROOT}/.logs"
DATA_DIR="${PROJECT_ROOT}/data"

mkdir -p "${PIDS_DIR}" "${LOGS_DIR}" "${DATA_DIR}"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }
log_step()  { echo -e "${CYAN}[STEP]${NC}  $*"; }

GO_BIN="${GO_BIN:-go}"
if ! command -v "$GO_BIN" &>/dev/null; then
    for p in /usr/local/go/bin/go /opt/homebrew/bin/go; do
        if [ -x "$p" ]; then
            GO_BIN="$p"
            break
        fi
    done
fi

if ! command -v "$GO_BIN" &>/dev/null; then
    log_error "未找到 Go 编译器，请先安装 Go 1.25+"
    exit 1
fi

_kill_port() {
    local port="$1"
    local pids=""
    if command -v lsof >/dev/null 2>&1; then
        pids=$(lsof -ti ":$port" 2>/dev/null || true)
    elif command -v ss >/dev/null 2>&1; then
        pids=$(ss -tlnp "sport = :$port" 2>/dev/null | grep -oP 'pid=\K[0-9]+' | sort -u || true)
    elif command -v fuser >/dev/null 2>&1; then
        pids=$(fuser "$port/tcp" 2>/dev/null || true)
    fi

    if [[ -n "$pids" ]]; then
        log_warn "终止占用端口 $port 的进程 (PID: $pids)..."
        echo "$pids" | xargs kill -9 2>/dev/null || true
        sleep 0.5
    fi
}

_ensure_port_free() {
    local port="$1"
    local name="$2"

    if command -v docker >/dev/null 2>&1; then
        local cids
        cids=$(docker ps -q --filter "publish=$port" 2>/dev/null || true)
        if [[ -n "$cids" ]]; then
            log_warn "端口 $port ($name) 被 Docker 容器占用，正在自动停止..."
            for cid in $cids; do
                docker stop "$cid" >/dev/null 2>&1 || true
            done
            sleep 0.5
        fi
    fi

    local in_use=false
    if command -v nc >/dev/null 2>&1; then
        nc -z 127.0.0.1 "$port" >/dev/null 2>&1 && in_use=true || true
    elif command -v lsof >/dev/null 2>&1; then
        lsof -i ":$port" >/dev/null 2>&1 && in_use=true || true
    fi

    if [ "$in_use" = true ]; then
        if [ "$FORCE" = true ]; then
            _kill_port "$port"
        else
            log_error "端口 $port ($name) 已被占用！请使用 --force 或先执行 dev-stop-services.sh"
            exit 1
        fi
    fi
}

_wait_for_health() {
    local name="$1"
    local url="$2"
    local max_wait="${3:-20}"
    local count=0

    while [ $count -lt $max_wait ]; do
        if curl -s -o /dev/null -w "%{http_code}" --max-time 2 "$url" 2>/dev/null | grep -q "200"; then
            log_info "$name is healthy and ready at $url"
            return 0
        fi
        sleep 0.5
        count=$((count + 1))
    done

    log_error "$name failed to become ready within ${max_wait}s at $url"
    return 1
}

# ── 模块 1: privshield-agent (Go 隐私计算引擎) ─────────────────────────
start_go_agent() {
    local rest_port="${PRIVACY_REST_PORT:-8079}"
    local grpc_port="${PRIVACY_GRPC_PORT:-50051}"
    local pid_file="${PIDS_DIR}/privshield-agent.pid"

    _ensure_port_free "$rest_port" "privshield-agent-rest"
    _ensure_port_free "$grpc_port" "privshield-agent-grpc"

    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "privshield-agent already running (PID $(cat "$pid_file"))"
        return
    fi

    log_info "Building privshield-agent..."
    cd "${PROJECT_ROOT}/services/privacy-engine"
    "$GO_BIN" build -o bin/privshield-agent ./cmd/privshield-agent

    log_info "Starting privshield-agent (REST :${rest_port}, gRPC :${grpc_port})..."
    cd "${PROJECT_ROOT}"
    PRIVACY_REST_HOST=127.0.0.1 PRIVACY_REST_PORT="$rest_port" \
    PRIVACY_GRPC_HOST=127.0.0.1 PRIVACY_GRPC_PORT="$grpc_port" \
    AGENT_REST_HOST=127.0.0.1 AGENT_REST_PORT="$rest_port" \
    AGENT_GRPC_HOST=127.0.0.1 AGENT_GRPC_PORT="$grpc_port" \
    PRIVACY_CONFIG_FILE="${PROJECT_ROOT}/config/privacy.yaml" \
    PRIVACY_LOG_LEVEL=INFO PRIVACY_LOG_FORMAT=json \
    nohup "${PROJECT_ROOT}/services/privacy-engine/bin/privshield-agent" < /dev/null >> "${LOGS_DIR}/privshield-agent.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "privshield-agent started (PID $(cat "$pid_file"))"

    _wait_for_health "privshield-agent" "http://127.0.0.1:${rest_port}/health" 20
}

# ── 模块 2: datasource-mgr ───────────────────────────────────────────
start_datasource_mgr() {
    local port="${DATASOURCE_MGR_PORT:-8083}"
    local pid_file="${PIDS_DIR}/datasource-mgr.pid"

    _ensure_port_free "$port" "datasource-mgr"

    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "datasource-mgr already running (PID $(cat "$pid_file"))"
        return
    fi

    log_info "Building datasource-mgr..."
    cd "${PROJECT_ROOT}/console/mock-datasource"
    "$GO_BIN" build -o bin/datasource-mgr ./cmd/server

    log_info "Starting datasource-mgr on :${port}..."
    cd "${PROJECT_ROOT}"
    DATASOURCE_MGR_HOST=127.0.0.1 DATASOURCE_MGR_PORT="$port" \
    DATASOURCE_MGR_DB_PATH="${DATA_DIR}/datasource-mgr.db" \
    nohup "${PROJECT_ROOT}/console/mock-datasource/bin/datasource-mgr" < /dev/null >> "${LOGS_DIR}/datasource-mgr.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "datasource-mgr started (PID $(cat "$pid_file"))"

    _wait_for_health "datasource-mgr" "http://127.0.0.1:${port}/health" 20
}

# ── 模块 3: audit-log ────────────────────────────────────────────────
start_audit_log() {
    local port="${AUDIT_LOG_PORT:-8084}"
    local pid_file="${PIDS_DIR}/audit-log.pid"

    _ensure_port_free "$port" "audit-log"

    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "audit-log already running (PID $(cat "$pid_file"))"
        return
    fi

    log_info "Building audit-log..."
    cd "${PROJECT_ROOT}/services/audit-log"
    "$GO_BIN" build -o bin/audit-log ./cmd/server

    log_info "Starting audit-log on :${port}..."
    cd "${PROJECT_ROOT}"
    AUDIT_LOG_HOST=127.0.0.1 AUDIT_LOG_PORT="$port" \
    AUDIT_LOG_DB_PATH="${DATA_DIR}/audit-log.db" \
    nohup "${PROJECT_ROOT}/services/audit-log/bin/audit-log" < /dev/null >> "${LOGS_DIR}/audit-log.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "audit-log started (PID $(cat "$pid_file"))"

    _wait_for_health "audit-log" "http://127.0.0.1:${port}/health" 20
}

# ── 模块 4: service-hub ──────────────────────────────────────────────
start_service_hub() {
    local port="${SERVICE_HUB_PORT:-8082}"
    local pid_file="${PIDS_DIR}/service-hub.pid"

    _ensure_port_free "$port" "service-hub"

    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "service-hub already running (PID $(cat "$pid_file"))"
        return
    fi

    log_info "Building service-hub..."
    cd "${PROJECT_ROOT}/services/service-hub"
    "$GO_BIN" build -o bin/service-hub ./cmd/server

    log_info "Starting service-hub on :${port}..."
    cd "${PROJECT_ROOT}"
    SERVICE_HUB_HOST=127.0.0.1 SERVICE_HUB_PORT="$port" \
    SERVICE_HUB_AGENT_REST_HOST=127.0.0.1 SERVICE_HUB_AGENT_REST_PORT=8079 \
    SERVICE_HUB_DATASOURCE_REST_HOST=127.0.0.1 SERVICE_HUB_DATASOURCE_REST_PORT=8083 \
    SERVICE_HUB_AUDIT_LOG_URLS="http://127.0.0.1:8084" \
    SERVICE_HUB_DB_PATH="${DATA_DIR}/service-hub.db" \
    nohup "${PROJECT_ROOT}/services/service-hub/bin/service-hub" < /dev/null >> "${LOGS_DIR}/service-hub.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "service-hub started (PID $(cat "$pid_file"))"

    _wait_for_health "service-hub" "http://127.0.0.1:${port}/health" 20
}

# ── 按依赖顺序启动全部模块 ───────────────────────────────────────────
cd "$PROJECT_ROOT"
start_go_agent
start_datasource_mgr
start_audit_log
start_service_hub

echo ""
log_info "================================================================"
log_info "  Privacy Engine & 3 Middle-Tier Microservices Ready!"
log_info "  privshield-agent : http://127.0.0.1:${PRIVACY_REST_PORT:-8079} (gRPC: :${PRIVACY_GRPC_PORT:-50051})"
log_info "  datasource-mgr   : http://127.0.0.1:${DATASOURCE_MGR_PORT:-8083}"
log_info "  audit-log        : http://127.0.0.1:${AUDIT_LOG_PORT:-8084}"
log_info "  service-hub      : http://127.0.0.1:${SERVICE_HUB_PORT:-8082}"
log_info "================================================================"
