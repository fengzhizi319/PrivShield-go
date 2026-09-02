#!/usr/bin/env bash
# ==============================================================================
# 脚本名称: start_all_services_go.sh
# 脚本说明: 一键后台启动 PrivShield Go 原生引擎 + BFF + 可选微服务群
#
# 与 start_all_services.sh 的区别：
#   - start_all_services.sh 使用 Python 引擎（python3 -m engine.server）
#   - 本脚本使用 Go 原生引擎（go run ./cmd/privshield-agent）
#
# 执行步骤总览：
#   1. 初始化工作目录与端口配置
#   2. 检查端口占用并执行日志文件轮转
#   3. 使用 nohup 后台启动 Go Agent 主进程
#   4. 后台启动 Console BFF 代理服务
#   5. 可选启动中台微服务群
#   6. 轮询健康探针直至服务就绪
#
# 用法 / Usage:
#   ./scripts/dev/start_all_services_go.sh [选项]
#
# 选项:
#   --with-services      同时启动中台三大微服务群
#   -h, --help           显示帮助信息
# ==============================================================================

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

LOG_DIR="$PROJECT_ROOT/.logs"
PIDS_DIR="$PROJECT_ROOT/.pids"
ENGINE_GO_DIR="$PROJECT_ROOT/engine-go"
mkdir -p "$LOG_DIR" "$PIDS_DIR"

REST_PORT="${PRIVACY_REST_PORT:-8079}"
GRPC_PORT="${PRIVACY_GRPC_PORT:-50051}"
CONSOLE_PORT="${PRIVACY_CONSOLE_PORT:-8081}"
WITH_SERVICES=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --with-services) WITH_SERVICES=true; shift 1 ;;
        -h|--help)
            echo "用法: $0 [--with-services]"
            echo "  --with-services: 联动启动中台三大微服务"
            exit 0
            ;;
        *) echo "未知参数: $1"; exit 1 ;;
    esac
done

echo -e "${BLUE}====================================================${NC}"
echo -e "${BLUE} PrivShield Go Engine 一键拉起服务全家桶${NC}"
echo -e "${BLUE} 工作目录     : ${PROJECT_ROOT}${NC}"
echo -e "${BLUE} 日志输出目录 : ${LOG_DIR}/${NC}"
echo -e "${BLUE} REST 端口    : ${REST_PORT}${NC}"
echo -e "${BLUE} gRPC 端口    : ${GRPC_PORT}${NC}"
echo -e "${BLUE} 控制台 BFF   : ${CONSOLE_PORT} (Go BFF)${NC}"
if [ "$WITH_SERVICES" = true ]; then
    echo -e "${BLUE} 微服务集群   : service-hub(:8082), datasource-mgr(:8083), audit-log(:8084)${NC}"
fi
echo -e "${BLUE}====================================================${NC}"

check_port() {
    local port=$1
    if command -v lsof &> /dev/null; then
        if lsof -i:"$port" &> /dev/null; then
            echo -e "${YELLOW}警告: 端口 ${port} 已被占用...${NC}"
        fi
    fi
}

rotate_log() {
    local log_file=$1 keep=5 i
    [ -f "$log_file" ] || return 0
    for (( i=keep-1; i>=1; i-- )); do
        [ -f "${log_file}.$i" ] && mv "${log_file}.$i" "${log_file}.$((i+1))"
    done
    mv "$log_file" "${log_file}.1"
}

check_port "$REST_PORT"
check_port "$GRPC_PORT"
check_port "$CONSOLE_PORT"
if [ "$WITH_SERVICES" = true ]; then
    check_port 8082; check_port 8083; check_port 8084
fi

# 1. 启动 Go Agent 核心引擎
echo -e "\n${YELLOW}[1/3] 启动 Go Engine REST & gRPC Agent...${NC}"
AGENT_LOG="${LOG_DIR}/agent_go_server.log"
rotate_log "$AGENT_LOG"

(
    cd "$ENGINE_GO_DIR"
    export PRIVACY_REST_HOST="0.0.0.0"
    export PRIVACY_REST_PORT="$REST_PORT"
    export PRIVACY_GRPC_HOST="0.0.0.0"
    export PRIVACY_GRPC_PORT="$GRPC_PORT"
    export PRIVACY_LOG_LEVEL="${PRIVACY_LOG_LEVEL:-INFO}"
    nohup go run ./cmd/privshield-agent < /dev/null >> "$AGENT_LOG" 2>&1 &
    echo $! > "${PIDS_DIR}/agent-go.pid"
)
echo -e "Go Agent PID: ${GREEN}$(cat "${PIDS_DIR}/agent-go.pid")${NC} (日志: ${AGENT_LOG})"

# 2. 启动 Console BFF 代理
echo -e "\n${YELLOW}[2/3] 启动 Console BFF 代理网关...${NC}"
if [ -d "$PROJECT_ROOT/console/bff-go" ]; then
    (
        cd "$PROJECT_ROOT/console/bff-go"
        go build -o bin/backend-go ./cmd/server
        nohup ./bin/backend-go < /dev/null > "${LOG_DIR}/console_bff_go.log" 2>&1 &
        echo $! > "${PIDS_DIR}/console-go.pid"
    )
    echo -e "Console Go BFF PID $(cat "${PIDS_DIR}/console-go.pid")"
fi

# 3. 可选启动中台微服务群
if [ "$WITH_SERVICES" = true ]; then
    echo -e "\n${YELLOW}[3/3] 启动中台微服务群...${NC}"
    (
        cd "$PROJECT_ROOT/services/service-hub"
        go build -o bin/service-hub ./cmd/server
        SERVICE_HUB_AGENT_REST_HOST=127.0.0.1 SERVICE_HUB_AGENT_REST_PORT=8079 \
        SERVICE_HUB_AUDIT_LOG_URLS="http://127.0.0.1:8084" \
        nohup ./bin/service-hub < /dev/null > "${LOG_DIR}/service_hub.log" 2>&1 &
        echo $! > "${PIDS_DIR}/service-hub.pid"
    )
    echo -e "  • service-hub (PID $(cat "${PIDS_DIR}/service-hub.pid"))"

    (
        cd "$PROJECT_ROOT/services/datasource-mgr"
        go build -o bin/datasource-mgr ./cmd/server
        nohup ./bin/datasource-mgr < /dev/null > "${LOG_DIR}/datasource_mgr.log" 2>&1 &
        echo $! > "${PIDS_DIR}/datasource-mgr.pid"
    )
    echo -e "  • datasource-mgr (PID $(cat "${PIDS_DIR}/datasource-mgr.pid"))"

    (
        cd "$PROJECT_ROOT/services/audit-log"
        go build -o bin/audit-log ./cmd/server
        nohup ./bin/audit-log < /dev/null > "${LOG_DIR}/audit_log.log" 2>&1 &
        echo $! > "${PIDS_DIR}/audit-log.pid"
    )
    echo -e "  • audit-log (PID $(cat "${PIDS_DIR}/audit-log.pid"))"
fi

# 4. 健康就绪探针
echo -e "\n${YELLOW}正在等待 Go Engine 服务探针响应 (最长等待 15 秒)...${NC}"
MAX_RETRIES=15
RETRY_COUNT=0
HEALTH_URL="http://127.0.0.1:${REST_PORT}/health"

while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
    if command -v curl &> /dev/null; then
        HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "${HEALTH_URL}" || echo "000")
        if [ "$HTTP_CODE" -eq 200 ]; then
            echo -e "\n${GREEN}====================================================${NC}"
            echo -e "${GREEN} Go Engine 所有服务启动成功且健康探针已就绪！${NC}"
            echo -e "${GREEN} Go Agent REST : http://127.0.0.1:${REST_PORT}${NC}"
            echo -e "${GREEN} Go Agent gRPC : 127.0.0.1:${GRPC_PORT}${NC}"
            echo -e "${GREEN} 控制台 BFF 网关 : http://127.0.0.1:${CONSOLE_PORT}${NC}"
            if [ "$WITH_SERVICES" = true ]; then
                echo -e "${GREEN} Service Hub     : http://127.0.0.1:8082${NC}"
                echo -e "${GREEN} Datasource Mgr  : http://127.0.0.1:8083${NC}"
                echo -e "${GREEN} Audit Log       : http://127.0.0.1:8084${NC}"
            fi
            echo -e "${GREEN} 停止服务: ./scripts/dev/stop_all_services.sh${NC}"
            echo -e "${GREEN}====================================================${NC}"
            exit 0
        fi
    fi
    echo -n "."
    sleep 1
    RETRY_COUNT=$((RETRY_COUNT + 1))
done

echo -e "\n${RED}[错误] Go Engine 服务启动超时，请检查日志: cat ${AGENT_LOG}${NC}"
exit 1
