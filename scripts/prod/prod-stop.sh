#!/usr/bin/env bash
# ============================================================================
# 【正式部署/生产模式】一键停止控制台全部服务
# Stop all prod mode console services
#
# ⚠️ 注意 / WARNING:
#   本脚本按 .pids/ 中的 PID 文件精确停止，并对生产端口
#   (8081/8079/50051) 上残留的任何进程执行清理。
#   清理策略为先 SIGTERM 优雅退出、1 秒后仍存活再 SIGKILL 强杀。
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

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CONSOLE_DIR="$PROJECT_ROOT/console"
PIDS_DIR="$PROJECT_ROOT/.pids"
LEGACY_PIDS_DIR="$CONSOLE_DIR/.pids"

kill_by_pid_file() {
    local file="$1"
    local name="$2"
    if [[ -f "$file" ]]; then
        local pid
        pid=$(cat "$file")
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            echo "停止 $name (PID $pid)..."
            kill "$pid" 2>/dev/null || true
            sleep 0.5
            if kill -0 "$pid" 2>/dev/null; then
                kill -9 "$pid" 2>/dev/null || true
            fi
        fi
        rm -f "$file"
    fi
}

kill_by_port() {
    local port="$1"
    local name="$2"
    local pids=""
    if command -v lsof >/dev/null 2>&1; then
        pids=$( (lsof -t -nP -i :"$port" 2>/dev/null || true) | sort -u | tr '\n' ' ')
    elif command -v ss >/dev/null 2>&1; then
        pids=$( (ss -tlnp 2>/dev/null || true) | (grep -E "LISTEN.*:$port\\s" || true) | sed -n 's/.*pid=\([0-9]*\).*/\1/p' | sort -u | tr '\n' ' ')
    elif command -v fuser >/dev/null 2>&1; then
        pids=$(fuser "$port"/tcp 2>/dev/null | tr -s ' ' || true)
    fi

    if [[ -n "$pids" ]]; then
        echo "清理端口 $port 上的残余进程 ($name: $pids)..."
        for pid in $pids; do
            kill -15 "$pid" 2>/dev/null || true
        done
        sleep 1
        for pid in $pids; do
            if kill -0 "$pid" 2>/dev/null; then
                kill -9 "$pid" 2>/dev/null || true
            fi
        done
    fi
}

echo "正在停止【生产模式】控制台所有服务..."

for dir in "$PIDS_DIR" "$LEGACY_PIDS_DIR"; do
    if [[ -d "$dir" ]]; then
        kill_by_pid_file "$dir/app-lz-prod.pid" "App-LZ BFF 生产服务"
        kill_by_pid_file "$dir/console-go-mtls.pid" "Go gRPC 代理后端 (mTLS)"
        kill_by_pid_file "$dir/console-go-all.pid" "Go gRPC 代理后端 (all)"
        kill_by_pid_file "$dir/console-go.pid" "Go BFF 代理后端"
        kill_by_pid_file "$dir/service-hub.pid" "service-hub 调度中枢"
        kill_by_pid_file "$dir/datasource-mgr.pid" "datasource-mgr 数据源"
        kill_by_pid_file "$dir/audit-log.pid" "audit-log 审计日志"
        kill_by_pid_file "$dir/agent-go-mtls.pid" "PrivShield (mTLS)"
        kill_by_pid_file "$dir/agent-all.pid" "PrivShield (all)"
        kill_by_pid_file "$dir/agent-go.pid" "PrivShield (gRPC)"
        kill_by_pid_file "$dir/agent.pid" "PrivShield (REST)"
    fi
done

kill_by_port 8085 "App-LZ BFF 调度之眼"
kill_by_port 8084 "audit-log 审计日志"
kill_by_port 8083 "datasource-mgr 数据源管理"
kill_by_port 8082 "service-hub 调度中枢"
kill_by_port 8081 "Go BFF 代理后端"
kill_by_port 50051 "PrivShield gRPC"
kill_by_port 8079 "PrivShield REST"

echo "✅ 生产模式所有服务已安全停止。"
