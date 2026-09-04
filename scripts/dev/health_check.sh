#!/usr/bin/env bash
# ==============================================================================
# 脚本名称: health_check.sh
# 脚本说明: PrivShield 核心算力服务、BFF 网关与中台微服务群健康状态诊断与环境巡检工具。
#
# 执行步骤总览：
#   0. 自动定位并切换至项目根目录
#   1. 解析命令行参数（--rest-host、--rest-port、--grpc-host、--grpc-port、--all）
#   2. 检查 Go 编译环境与版本
#   3. 探测核心 Agent REST API 端口连通性及 /health 端点
#   4. 探测核心 Agent gRPC 服务端口 TCP 连通性
#   5. 可选探测 BFF 网关与微服务群
#   6. 输出巡检统计汇总与退出码
#
# 用法 / Usage:
#   ./scripts/dev/health_check.sh [选项]
# ==============================================================================

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || (cd "$SCRIPT_DIR/../.." && pwd -P))"
cd "$PROJECT_ROOT"

REST_HOST="${PRIVACY_REST_HOST:-127.0.0.1}"
REST_PORT="${PRIVACY_REST_PORT:-8079}"
GRPC_HOST="${PRIVACY_GRPC_HOST:-127.0.0.1}"
GRPC_PORT="${PRIVACY_GRPC_PORT:-50051}"
CHECK_ALL=false

export no_proxy="127.0.0.1,localhost,${REST_HOST},${no_proxy:-}"
export NO_PROXY="127.0.0.1,localhost,${REST_HOST},${NO_PROXY:-}"

TOTAL_CHECKS=0
PASSED_CHECKS=0
FAILED_CHECKS=0
WARNING_CHECKS=0

# ── 解析命令行参数 ─────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --rest-host)  REST_HOST="$2"; shift 2 ;;
        --rest-port)  REST_PORT="$2"; shift 2 ;;
        --grpc-host)  GRPC_HOST="$2"; shift 2 ;;
        --grpc-port)  GRPC_PORT="$2"; shift 2 ;;
        --all)        CHECK_ALL=true; shift ;;
        -h|--help)
            echo "用法 / Usage: $0 [--rest-host HOST] [--rest-port PORT] [--grpc-host HOST] [--grpc-port PORT] [--all]"
            echo ""
            echo "选项 / Options:"
            echo "  --rest-host   REST 服务地址 (默认: 127.0.0.1)"
            echo "  --rest-port   REST 服务端口 (默认: 8079)"
            echo "  --grpc-host   gRPC 服务地址 (默认: 127.0.0.1)"
            echo "  --grpc-port   gRPC 服务端口 (默认: 50051)"
            echo "  --all         同时检查 BFF 网关与微服务群"
            echo "  -h, --help    显示帮助信息"
            exit 0
            ;;
        *) echo "未知参数: $1"; exit 1 ;;
    esac
done

echo -e "${BLUE}====================================================${NC}"
echo -e "${BOLD} PrivShield 基础环境与服务健康巡检${NC}"
echo -e "${BLUE} 工作目录: ${PROJECT_ROOT}${NC}"
echo -e "${BLUE}====================================================${NC}"

# 1. 检查 Go 编译环境
echo -e "\n${YELLOW}[1/4] 检查 Go 编译环境与核心组件...${NC}"
TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
if command -v go &> /dev/null; then
    GO_VER=$(go version | awk '{print $3}')
    echo -e "Go 版本       : ${GREEN}${GO_VER}${NC}"
    PASSED_CHECKS=$((PASSED_CHECKS + 1))
else
    echo -e "${RED}[错误] 未检测到 go 命令，请先安装 Go 1.25+${NC}"
    FAILED_CHECKS=$((FAILED_CHECKS + 1))
fi

# 检查 Go 引擎源码
TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
if [[ -d "$PROJECT_ROOT/services/privacy-engine" && -f "$PROJECT_ROOT/services/privacy-engine/go.mod" ]]; then
    echo -e "Go 引擎源码   : ${GREEN}services/privacy-engine/ 目录存在${NC}"
    PASSED_CHECKS=$((PASSED_CHECKS + 1))
else
    echo -e "${RED}[错误] 未检测到 services/privacy-engine/ 目录${NC}"
    FAILED_CHECKS=$((FAILED_CHECKS + 1))
fi

# 2. REST 服务端口及 HTTP 端点探针
echo -e "\n${YELLOW}[2/4] 检查 Agent REST 连通性 (http://${REST_HOST}:${REST_PORT})...${NC}"
REST_URL="http://${REST_HOST}:${REST_PORT}/health"
TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
if command -v curl &> /dev/null; then
    HTTP_CODE=$(curl --noproxy "*" -s -o /tmp/privshield_health.json -w "%{http_code}" --max-time 5 "${REST_URL}" 2>/dev/null || echo "000")
    HTTP_CODE="${HTTP_CODE: -3}"
    if [ "$HTTP_CODE" = "200" ]; then
        echo -e "Agent REST 健康探针: ${GREEN}HTTP 200 OK${NC}"
        echo -e "返回报文内容       : $(cat /tmp/privshield_health.json 2>/dev/null || true)"
        PASSED_CHECKS=$((PASSED_CHECKS + 1))
    else
        echo -e "Agent REST 健康探针: ${RED}HTTP ${HTTP_CODE} (服务未启动或不可达)${NC}"
        FAILED_CHECKS=$((FAILED_CHECKS + 1))
    fi
else
    echo -e "${YELLOW}未检测到 curl，跳过 HTTP 端口探针。${NC}"
    WARNING_CHECKS=$((WARNING_CHECKS + 1))
fi

# 检查 Go 引擎特有端点
TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
LIVEZ_URL="http://${REST_HOST}:${REST_PORT}/livez"
if command -v curl &> /dev/null; then
    LIVEZ_CODE=$(curl --noproxy "*" -s -o /dev/null -w "%{http_code}" --max-time 5 "${LIVEZ_URL}" 2>/dev/null || echo "000")
    LIVEZ_CODE="${LIVEZ_CODE: -3}"
    if [ "$LIVEZ_CODE" = "200" ]; then
        echo -e "Agent /livez 探针   : ${GREEN}HTTP 200 OK${NC}"
        PASSED_CHECKS=$((PASSED_CHECKS + 1))
    else
        echo -e "Agent /livez 探针   : ${YELLOW}HTTP ${LIVEZ_CODE} (服务可能未启动)${NC}"
        WARNING_CHECKS=$((WARNING_CHECKS + 1))
    fi
fi

# 3. gRPC 服务端口连通性检测
echo -e "\n${YELLOW}[3/4] 检查 Agent gRPC 端口 (${GRPC_HOST}:${GRPC_PORT})...${NC}"
TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
if command -v nc &> /dev/null; then
    if nc -z -w 3 "$GRPC_HOST" "$GRPC_PORT" &> /dev/null; then
        echo -e "Agent gRPC 端口状态: ${GREEN}端口 ${GRPC_PORT} 开放且可达${NC}"
        PASSED_CHECKS=$((PASSED_CHECKS + 1))
    else
        echo -e "Agent gRPC 端口状态: ${RED}端口 ${GRPC_PORT} 无法连接${NC}"
        FAILED_CHECKS=$((FAILED_CHECKS + 1))
    fi
elif command -v timeout &> /dev/null && command -v bash &> /dev/null; then
    if timeout 3 bash -c "</dev/tcp/${GRPC_HOST}/${GRPC_PORT}" &> /dev/null; then
        echo -e "Agent gRPC 端口状态: ${GREEN}端口 ${GRPC_PORT} 开放且可达${NC}"
        PASSED_CHECKS=$((PASSED_CHECKS + 1))
    else
        echo -e "Agent gRPC 端口状态: ${RED}端口 ${GRPC_PORT} 无法连接${NC}"
        FAILED_CHECKS=$((FAILED_CHECKS + 1))
    fi
else
    echo -e "${YELLOW}缺少 nc/tcp 工具，跳过端口侦听检查。${NC}"
    WARNING_CHECKS=$((WARNING_CHECKS + 1))
fi

# 4. 可选探测微服务群
if [ "$CHECK_ALL" = true ]; then
    echo -e "\n${YELLOW}[4/4] 巡检中台微服务群与 BFF 网关...${NC}"
    check_http_svc() {
        local name="$1"
        local url="$2"
        local code
        TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
        code=$(curl --noproxy "*" -s -o /dev/null -w "%{http_code}" --max-time 3 "$url" 2>/dev/null || echo "000")
        code="${code: -3}"
        if [ "$code" = "200" ]; then
            echo -e "  • ${name} ($url): ${GREEN}HTTP 200 OK${NC}"
            PASSED_CHECKS=$((PASSED_CHECKS + 1))
        else
            echo -e "  • ${name} ($url): ${RED}HTTP ${code} (未就绪)${NC}"
            FAILED_CHECKS=$((FAILED_CHECKS + 1))
        fi
    }
    check_http_svc "BFF-Go 网关" "http://127.0.0.1:8081/health"
    check_http_svc "Service Hub 调度中枢" "http://127.0.0.1:8082/health"
    check_http_svc "Datasource Mgr 数据源" "http://127.0.0.1:8083/health"
    check_http_svc "Audit Log 审计日志" "http://127.0.0.1:8084/health"
else
    echo -e "\n${YELLOW}[4/4] 跳过微服务群检查 (使用 --all 启用)${NC}"
fi

# ── 巡检结果汇总 ────────────────────────────────────────────────────────
echo -e "\n${BLUE}====================================================${NC}"
echo -e "${BLUE}               健康诊断结果汇总                        ${NC}"
echo -e "${BLUE}====================================================${NC}"
echo -e "  • 检查总项: ${CYAN}${TOTAL_CHECKS}${NC}"
echo -e "  • 通过项目: ${GREEN}${PASSED_CHECKS}${NC}"
echo -e "  • 警告项目: ${YELLOW}${WARNING_CHECKS}${NC}"
echo -e "  • 失败项目: ${RED}${FAILED_CHECKS}${NC}"
echo -e "${BLUE}----------------------------------------------------${NC}"

if [ "$FAILED_CHECKS" -eq 0 ]; then
    echo -e "${GREEN}✅ 基础服务与运行环境巡检全部通过！${NC}"
    echo -e "${BLUE}====================================================${NC}"
    exit 0
else
    echo -e "${RED}❌ 存在 ${FAILED_CHECKS} 项未通过的检查，请排查相关服务或端口！${NC}"
    echo -e "${BLUE}====================================================${NC}"
    exit 1
fi
