#!/usr/bin/env bash
# ==============================================================================
# 脚本名称: start_all_services.sh
# 脚本说明: 一键后台启动 PrivShield 核心侧边栏服务 (REST + gRPC)、
#           Web 测试控制台代理后端服务 (BFF)，以及可选的中台微服务群 (services/*)，
#           并自动检测健康就绪探针。
#
# 执行步骤总览：
#   1. 初始化工作目录与端口配置（REST: 8079, gRPC: 50051, BFF: 8081, Services: 8082~8084）
#   2. 检查端口占用并执行日志文件轮转备份（保留最近 5 份）
#   3. 使用 nohup 后台拉起 PrivShield Core REST & gRPC Agent 主进程并记录 PID
#   4. 后台拉起 Console BFF (Go) 代理服务并记录 PID
#   5. 可选拉起 中台 3 大微服务群 (service-hub, datasource-mgr, audit-log) 并记录 PID
#   6. 轮询健康探针（GET /health 最长等待 15 秒）直至服务完全就绪
#
# 用法 / Usage:
#   ./scripts/dev/start_all_services.sh [选项]
#
# 选项:
#   --with-services      同时启动中台三大微服务群 (service-hub: 8082, datasource-mgr: 8083, audit-log: 8084)
#   -h, --help           显示帮助信息
# ==============================================================================

set -euo pipefail
export CGO_ENABLED=0

# ANSI 终端颜色代码
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# ── 步骤 1：定位项目根目录与参数解析 ──────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || (cd "$SCRIPT_DIR/../.." && pwd -P))"
cd "$PROJECT_ROOT"

LOG_DIR="$PROJECT_ROOT/.logs"
PIDS_DIR="$PROJECT_ROOT/.pids"
mkdir -p "$LOG_DIR" "$PIDS_DIR"

REST_PORT="${PRIVACY_REST_PORT:-8079}"
GRPC_PORT="${PRIVACY_GRPC_PORT:-50051}"
CONSOLE_PORT="${PRIVACY_CONSOLE_PORT:-8081}"
WITH_SERVICES=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --with-services)
            WITH_SERVICES=true
            shift 1
            ;;
        -h|--help)
            echo "用法: $0 [--with-services]"
            echo "  --with-services: 联动启动中台三大微服务 (service-hub, datasource-mgr, audit-log)"
            exit 0
            ;;
        *)
            echo "未知参数: $1"
            exit 1
            ;;
    esac
done

echo -e "${BLUE}====================================================${NC}"
echo -e "${BLUE} PrivShield 一键拉起服务全家桶${NC}"
echo -e "${BLUE} 工作目录     : ${PROJECT_ROOT}${NC}"
echo -e "${BLUE} 日志输出目录 : ${LOG_DIR}/${NC}"
echo -e "${BLUE} REST 端口    : ${REST_PORT}${NC}"
echo -e "${BLUE} gRPC 端口    : ${GRPC_PORT}${NC}"
echo -e "${BLUE} 控制台 BFF   : ${CONSOLE_PORT} (Go BFF)${NC}"
if [ "$WITH_SERVICES" = true ]; then
    echo -e "${BLUE} 微服务集群   : service-hub(:8082), datasource-mgr(:8083), audit-log(:8084)${NC}"
fi
echo -e "${BLUE}====================================================${NC}"

# 1. 检查端口占用情况
check_port() {
    local port=$1
    if command -v lsof &> /dev/null; then
        if lsof -i:"$port" &> /dev/null; then
            echo -e "${YELLOW}警告: 端口 ${port} 已被占用（建议先运行 stop_all_services.sh）...${NC}"
        fi
    fi
}

# 日志轮转
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
    check_port 8082
    check_port 8083
    check_port 8084
fi

# 2. 启动核心 REST + gRPC Agent
echo -e "\n${YELLOW}[1/3] 启动 Go Engine REST & gRPC Agent 算力引擎...${NC}"
AGENT_LOG="${LOG_DIR}/agent_server.log"
rotate_log "$AGENT_LOG"

if [[ -f "$PROJECT_ROOT/bin/privshield-agent" ]]; then
    nohup "$PROJECT_ROOT/bin/privshield-agent" < /dev/null > "$AGENT_LOG" 2>&1 &
else
    nohup go run ./services/privacy-engine/cmd/privshield-agent < /dev/null > "$AGENT_LOG" 2>&1 &
fi
AGENT_PID=$!
echo $AGENT_PID > "${PIDS_DIR}/agent.pid"
echo -e "Agent 进程 PID: ${GREEN}${AGENT_PID}${NC} (日志: ${AGENT_LOG})"

# 3. 启动 Console BFF 代理 (Go BFF :8081)
echo -e "\n${YELLOW}[2/3] 启动 Console BFF 代理网关 (Go gRPC BFF)...${NC}"
if [ -d "$PROJECT_ROOT/console/engine-console/bff-go" ]; then
    (
        cd "$PROJECT_ROOT/console/engine-console/bff-go"
        go build -o bin/backend-go ./cmd/server
        nohup ./bin/backend-go < /dev/null > "${LOG_DIR}/console_bff_go.log" 2>&1 &
        echo $! > "${PIDS_DIR}/console-go.pid"
    )
    echo -e "Console Go BFF PID $(cat "${PIDS_DIR}/console-go.pid"), 日志: ${LOG_DIR}/console_bff_go.log"
fi

# 4. 可选拉起中台微服务群
if [ "$WITH_SERVICES" = true ]; then
    echo -e "\n${YELLOW}[3/3] 启动中台微服务群 (service-hub / datasource-mgr / audit-log)...${NC}"
    
    # 4.1 service-hub
    (
        cd "$PROJECT_ROOT/services/service-hub"
        go build -o bin/service-hub ./cmd/server
        SERVICE_HUB_AGENT_REST_HOST=127.0.0.1 SERVICE_HUB_AGENT_REST_PORT=8079 \
        SERVICE_HUB_AUDIT_LOG_URLS="http://127.0.0.1:8084" \
        nohup ./bin/service-hub < /dev/null > "${LOG_DIR}/service_hub.log" 2>&1 &
        echo $! > "${PIDS_DIR}/service-hub.pid"
    )
    echo -e "  • service-hub (PID $(cat "${PIDS_DIR}/service-hub.pid"), 日志: ${LOG_DIR}/service_hub.log)"

    # 4.2 datasource-mgr
    (
        cd "$PROJECT_ROOT/console/mock-datasource"
        go build -o bin/datasource-mgr ./cmd/server
        nohup ./bin/datasource-mgr < /dev/null > "${LOG_DIR}/datasource_mgr.log" 2>&1 &
        echo $! > "${PIDS_DIR}/datasource-mgr.pid"
    )
    echo -e "  • datasource-mgr (PID $(cat "${PIDS_DIR}/datasource-mgr.pid"), 日志: ${LOG_DIR}/datasource_mgr.log)"

    # 4.3 audit-log
    (
        cd "$PROJECT_ROOT/services/audit-log"
        go build -o bin/audit-log ./cmd/server
        nohup ./bin/audit-log < /dev/null > "${LOG_DIR}/audit_log.log" 2>&1 &
        echo $! > "${PIDS_DIR}/audit-log.pid"
    )
    echo -e "  • audit-log (PID $(cat "${PIDS_DIR}/audit-log.pid"), 日志: ${LOG_DIR}/audit_log.log)"
fi

# 5. 健康轮询就绪探针 (Health Readiness Probe)
echo -e "\n${YELLOW}正在等待服务探针响应 (最长等待 15 秒)...${NC}"
MAX_RETRIES=15
RETRY_COUNT=0
HEALTH_URL="http://127.0.0.1:${REST_PORT}/health"

while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
    if command -v curl &> /dev/null; then
        HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "${HEALTH_URL}" || echo "000")
        if [ "$HTTP_CODE" -eq 200 ]; then
            echo -e "\n${GREEN}====================================================${NC}"
            echo -e "${GREEN} 所有服务启动成功且健康探针已就绪！${NC}"
            echo -e "${GREEN} 核心 Agent REST : http://127.0.0.1:${REST_PORT}${NC}"
            echo -e "${GREEN} 核心 Agent gRPC : 127.0.0.1:${GRPC_PORT}${NC}"
            echo -e "${GREEN} 控制台 BFF 网关 : http://127.0.0.1:${CONSOLE_PORT}${NC}"
            if [ "$WITH_SERVICES" = true ]; then
                echo -e "${GREEN} 调度中枢 Service Hub : http://127.0.0.1:8082${NC}"
                echo -e "${GREEN} 数据源管理 Datasource: http://127.0.0.1:8083${NC}"
                echo -e "${GREEN} 脱敏审计日志 AuditLog: http://127.0.0.1:8084${NC}"
            fi
            echo -e "${GREEN} 停止服务命令    : ./scripts/dev/stop_all_services.sh${NC}"
            echo -e "${GREEN}====================================================${NC}"
            exit 0
        fi
    fi
    echo -n "."
    sleep 1
    RETRY_COUNT=$((RETRY_COUNT + 1))
done

echo -e "\n${RED}[错误] 服务启动超时，未在 15 秒内响应健康检查。${NC}"
echo -e "请检查日志文件获取详细报错: cat ${AGENT_LOG}"
exit 1
