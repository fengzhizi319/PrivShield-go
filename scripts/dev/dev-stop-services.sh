#!/usr/bin/env bash
# ============================================================================
# dev-stop-services.sh — 优雅停止 PrivShield 隐私计算引擎与三大中台微服务群
#
# 停止模块：
#   1. service-hub      (数据服务调度中枢)
#   2. audit-log        (脱敏审计日志)
#   3. datasource-mgr   (数据源管理)
#   4. privshield-agent (Go 隐私计算引擎)
#   5. privshield-gateway (网关，若有)
#
# 用法 / Usage:
#   bash scripts/dev/dev-stop-services.sh
# ============================================================================

set -euo pipefail

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "说明: 停止由 dev-start-services.sh 启动的隐私计算引擎及三大中台微服务"
            echo "选项 / Options:"
            echo "  -h, --help    显示帮助信息并退出"
            exit 0
            ;;
        *)
            shift
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
PIDS_DIR="${PROJECT_ROOT}/.pids"
LEGACY_PIDS_DIR="${PROJECT_ROOT}/console/.pids"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

stop_module() {
    local name="$1"
    local pid_file=""

    if [ -f "${PIDS_DIR}/${name}.pid" ]; then
        pid_file="${PIDS_DIR}/${name}.pid"
    elif [ -f "${LEGACY_PIDS_DIR}/${name}.pid" ]; then
        pid_file="${LEGACY_PIDS_DIR}/${name}.pid"
    fi

    if [ -z "$pid_file" ] || [ ! -f "$pid_file" ]; then
        pkill -f "bin/${name}" 2>/dev/null || true
        log_warn "${name}: no PID file found"
        return
    fi

    local pid
    pid=$(cat "$pid_file")

    if kill -0 "$pid" 2>/dev/null; then
        log_info "Stopping ${name} (PID ${pid})..."
        kill "$pid" 2>/dev/null || true
        local count=0
        while kill -0 "$pid" 2>/dev/null && [ $count -lt 20 ]; do
            sleep 0.5
            count=$((count + 1))
        done

        if kill -0 "$pid" 2>/dev/null; then
            log_warn "${name} (PID ${pid}) did not exit, sending SIGKILL..."
            kill -9 "$pid" 2>/dev/null || true
        fi
        log_info "${name} stopped"
    else
        log_warn "${name} (PID ${pid}) is not running"
    fi

    rm -f "$pid_file"
}

# ── 按依赖反序停止各服务与 Go Agent ─────────────────────────────────
stop_module "service-hub"
stop_module "audit-log"
stop_module "datasource-mgr"
stop_module "privshield-agent"
stop_module "privshield-gateway"

# 兜底清理残留端口
for port in 8082 8084 8083 8079 50051 50052 50053 50054; do
    if command -v lsof >/dev/null 2>&1; then
        pids=$(lsof -ti ":$port" 2>/dev/null || true)
        if [ -n "$pids" ]; then
            echo "$pids" | xargs kill -9 2>/dev/null || true
        fi
    fi
done

echo ""
log_info "=========================================="
log_info "  Privacy Engine & All Microservices stopped."
log_info "=========================================="
