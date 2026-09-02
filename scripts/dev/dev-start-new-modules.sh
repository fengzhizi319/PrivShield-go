#!/usr/bin/env bash
# ============================================================================
# Development Startup Script for Three New Microservice Modules
# 三个中台微服务模块的开发模式一键启动脚本
#
# 启动模块：
#   1. service-hub    (数据服务调度中枢)  :8082
#   2. datasource-mgr (数据源管理)        :8083
#   3. audit-log      (脱敏审计日志)      :8084
#
# 前置条件：
#   - Go 编译器已安装
#   - PrivShield Agent 已运行（REST: 8079）
#
# Usage:
#   bash scripts/dev/dev-start-new-modules.sh
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
# PIDS_DIR  : PID 文件存储目录（.pids/）
# LOGS_DIR  : 日志输出目录（.logs/）
# DATA_DIR  : SQLite DB 存储目录（data/）
# GO_BIN    : Go 编译器路径，可通过环境变量覆盖
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || (cd "$SCRIPT_DIR/../.." && pwd -P))"
PIDS_DIR="${PROJECT_ROOT}/.pids"
LOGS_DIR="${PROJECT_ROOT}/.logs"
DATA_DIR="${PROJECT_ROOT}/data"
GO_BIN="${GO_BIN:-go}"

mkdir -p "$PIDS_DIR" "$LOGS_DIR" "$DATA_DIR"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

# ── Go 编译器自动探测：优先 PATH，回退到常见安装路径 ──────────────────
if ! command -v "$GO_BIN" &>/dev/null; then
    for p in /usr/local/go/bin/go /opt/homebrew/bin/go; do
        if [ -x "$p" ]; then
            GO_BIN="$p"
            break
        fi
    done
fi

if ! command -v "$GO_BIN" &>/dev/null; then
    log_error "Go compiler not found. Set GO_BIN env var or install Go."
    exit 1
fi

log_info "Using Go: $GO_BIN ($($GO_BIN version))"

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

# ── 每个模块的启动流程：─────────────────────────────────────────────
#   1. 检查 PID 文件，若已存在且进程存活则跳过（幂等性）
#   2. go build 编译最新二进制
#   3. 设置环境变量（监听地址/端口、Agent 连接信息、DB 路径）
#   4. 后台启动，日志追加到 .logs/，PID 写入 .pids/
#
# ── 模块 1: service-hub ──────────────────────────────────────────────
start_service_hub() {
    local port="${SERVICE_HUB_PORT:-8082}"
    local pid_file="${PIDS_DIR}/service-hub.pid"

    log_info "Building service-hub..."
    cd "${PROJECT_ROOT}/services/service-hub"
    SERVICE_HUB_HOST=127.0.0.1 SERVICE_HUB_PORT="$port" \
        "$GO_BIN" build -o bin/service-hub ./cmd/server

    # 总是重启以确保加载最新编译的二进制
    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "停止旧 service-hub 进程 (PID $(cat "$pid_file"))..."
        kill "$(cat "$pid_file")" 2>/dev/null || true
        sleep 0.5
    fi
    _ensure_port_free "$port" "service-hub"

    log_info "Starting service-hub on :${port}..."
    SERVICE_HUB_HOST=127.0.0.1 SERVICE_HUB_PORT="$port" \
        SERVICE_HUB_AGENT_REST_HOST=127.0.0.1 SERVICE_HUB_AGENT_REST_PORT=8079 \
        SERVICE_HUB_DB_PATH="${DATA_DIR}/service-hub.db" \
        ./bin/service-hub >> "${LOGS_DIR}/service-hub.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "service-hub started (PID $(cat "$pid_file"))"
}

# ── 模块 2: datasource-mgr ───────────────────────────────────────────
start_datasource_mgr() {
    local port="${DATASOURCE_MGR_PORT:-8083}"
    local pid_file="${PIDS_DIR}/datasource-mgr.pid"

    log_info "Building datasource-mgr..."
    cd "${PROJECT_ROOT}/services/datasource-mgr"
    DATASOURCE_MGR_HOST=127.0.0.1 DATASOURCE_MGR_PORT="$port" \
        "$GO_BIN" build -o bin/datasource-mgr ./cmd/server

    # 总是重启以确保加载最新编译的二进制
    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "停止旧 datasource-mgr 进程 (PID $(cat "$pid_file"))..."
        kill "$(cat "$pid_file")" 2>/dev/null || true
        sleep 0.5
    fi
    _ensure_port_free "$port" "datasource-mgr"

    log_info "Starting datasource-mgr on :${port}..."
    DATASOURCE_MGR_HOST=127.0.0.1 DATASOURCE_MGR_PORT="$port" \
        DATASOURCE_MGR_AGENT_REST_HOST=127.0.0.1 DATASOURCE_MGR_AGENT_REST_PORT=8079 \
        DATASOURCE_MGR_DB_PATH="${DATA_DIR}/datasource-mgr.db" \
        ./bin/datasource-mgr >> "${LOGS_DIR}/datasource-mgr.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "datasource-mgr started (PID $(cat "$pid_file"))"
}

# ── 模块 3: audit-log ────────────────────────────────────────────────
start_audit_log() {
    local port="${AUDIT_LOG_PORT:-8084}"
    local pid_file="${PIDS_DIR}/audit-log.pid"

    log_info "Building audit-log..."
    cd "${PROJECT_ROOT}/services/audit-log"
    AUDIT_LOG_HOST=127.0.0.1 AUDIT_LOG_PORT="$port" \
        "$GO_BIN" build -o bin/audit-log ./cmd/server

    # 总是重启以确保加载最新编译的二进制
    if [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null; then
        log_warn "停止旧 audit-log 进程 (PID $(cat "$pid_file"))..."
        kill "$(cat "$pid_file")" 2>/dev/null || true
        sleep 0.5
    fi
    _ensure_port_free "$port" "audit-log"

    log_info "Starting audit-log on :${port}..."
    AUDIT_LOG_HOST=127.0.0.1 AUDIT_LOG_PORT="$port" \
        AUDIT_LOG_AGENT_REST_HOST=127.0.0.1 AUDIT_LOG_AGENT_REST_PORT=8079 \
        AUDIT_LOG_DB_PATH="${DATA_DIR}/audit-log.db" \
        ./bin/audit-log >> "${LOGS_DIR}/audit-log.log" 2>&1 &
    echo $! > "$pid_file"
    log_info "audit-log started (PID $(cat "$pid_file"))"
}

# ── 回到项目根目录，按顺序启动全部三个模块 ──────────────────────────
cd "$PROJECT_ROOT"
start_service_hub
start_datasource_mgr
start_audit_log

echo ""
log_info "=========================================="
log_info "  All 3 microservices started!"
log_info "  service-hub    : http://127.0.0.1:${SERVICE_HUB_PORT:-8082}"
log_info "  datasource-mgr : http://127.0.0.1:${DATASOURCE_MGR_PORT:-8083}"
log_info "  audit-log      : http://127.0.0.1:${AUDIT_LOG_PORT:-8084}"
log_info "=========================================="
