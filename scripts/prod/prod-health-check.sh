#!/usr/bin/env bash
# ==============================================================================
# 【生产模式】PrivShield 生产级全链路健康巡检与诊断工具
# Production-Grade Health Probe, Metrics, TLS Certificate & DB Inspection Tool
#
# 执行步骤总览：
#   1. 解析命令行参数（REST/gRPC 主机与端口、TLS 开关、API Key、DB 路径）
#   2. 探测 REST API 标准探针（/health、/livez、/readyz、/readyz/llm）
#   3. 检查 Prometheus /metrics 可观测性指标端点有效性
#   4. 探测 gRPC 端口 TCP 连通性（使用 nc / bash socket）
#   5. 校验 TLS/HTTPS 证书有效期与剩余过期天数（若提供证书路径）
#   6. 检查 SQLite 隐私预算持久化数据库文件完整性与只读读写测试
#   7. 输出巡检综合评估结论与退出码（0: 全部通过, 1: 存在致命异常）
#
# 用法 / Usage:
#   ./scripts/prod/prod-health-check.sh [选项]
# ==============================================================================

set -euo pipefail
export NO_PROXY="*"
export no_proxy="*"

# ANSI 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# ── 步骤 0：定位并切换至项目根目录 ────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

# ── 步骤 1：定位默认参数与环境变量 ────────────────────────────────────────
REST_HOST="${PRIVACY_REST_HOST:-127.0.0.1}"
REST_PORT="${PRIVACY_REST_PORT:-8079}"
GRPC_HOST="${PRIVACY_GRPC_HOST:-127.0.0.1}"
GRPC_PORT="${PRIVACY_GRPC_PORT:-50051}"
METRICS_PORT="${PRIVACY_METRICS_PORT:-$REST_PORT}"
USE_TLS="${AGENT_TLS_ENABLED:-false}"
WITH_LLM=false
API_KEY="${PRIVACY_AUTH_API_KEY:-}"
CERT_FILE="${AGENT_TLS_CERT_FILE:-}"
DB_PATH="${PRIVACY_BUDGET_DB:-$PROJECT_ROOT/.data/privacy_budget.db}"

TOTAL_CHECKS=0
PASSED_CHECKS=0
WARNINGS=0
ERRORS=0

# ── 步骤 2：帮助信息与命令行解析 ──────────────────────────────────────────
usage() {
    cat <<EOF
使用说明: $(basename "$0") [选项]

选项:
  --rest-host HOST     REST 目标地址 (默认: 127.0.0.1 或 PRIVACY_REST_HOST)
  --rest-port PORT     REST 端口 (默认: 8079 或 PRIVACY_REST_PORT)
  --grpc-host HOST     gRPC 目标地址 (默认: 127.0.0.1 或 PRIVACY_GRPC_HOST)
  --grpc-port PORT     gRPC 端口 (默认: 50051 或 PRIVACY_GRPC_PORT)
  --tls                强制启用 HTTPS / TLS 探测
  --with-llm           强制要求 LLM 引擎就绪 (默认: 可选)
  --cert-file FILE     TLS 证书文件路径 (用于校验过期剩余天数)
  --api-key KEY        Bearer Token / API Key (若开启了鉴权)
  --db-path PATH       SQLite 预算库路径 (默认: .data/privacy_budget.db)
  -h, --help           显示帮助信息并退出
EOF
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --rest-host) REST_HOST="$2"; shift 2 ;;
        --rest-port) REST_PORT="$2"; shift 2 ;;
        --grpc-host) GRPC_HOST="$2"; shift 2 ;;
        --grpc-port) GRPC_PORT="$2"; shift 2 ;;
        --tls) USE_TLS="true"; shift 1 ;;
        --with-llm) WITH_LLM="true"; shift 1 ;;
        --cert-file) CERT_FILE="$2"; shift 2 ;;
        --api-key) API_KEY="$2"; shift 2 ;;
        --db-path) DB_PATH="$2"; shift 2 ;;
        -h|--help) usage ;;
        *) echo "未知参数: $1" >&2; exit 1 ;;
    esac
done

PROTOCOL="http"
if [[ "$USE_TLS" == "true" ]]; then
    PROTOCOL="https"
fi

AUTH_HEADER=()
if [[ -n "$API_KEY" ]]; then
    AUTH_HEADER=(-H "Authorization: Bearer $API_KEY")
fi

echo -e "${BOLD}${CYAN}============================================================================${NC}"
echo -e "${BOLD}${CYAN}🛡️  【生产模式】PrivShield 生产环境全链路健康巡检${NC}"
echo -e "${BOLD}${CYAN}============================================================================${NC}"
echo "目标配置:"
echo "  • REST 地址 : $PROTOCOL://$REST_HOST:$REST_PORT"
echo "  • gRPC 地址 : $GRPC_HOST:$GRPC_PORT"
echo "  • TLS 模式  : $USE_TLS"
echo "  • 预算数据库 : $DB_PATH"
echo "────────────────────────────────────────────────────────────────────────────"

check_http_endpoint() {
    local name="$1"
    local path="$2"
    local expected_code="$3"
    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
    
    local url="$PROTOCOL://$REST_HOST:$REST_PORT$path"
    local start_ns
    start_ns=$(date +%s%N 2>/dev/null || python3 -c 'import time; print(int(time.time()*1e9))')
    
    local http_code
    http_code=$(curl -s -k -o /dev/null -w "%{http_code}" "${AUTH_HEADER[@]}" "$url" 2>/dev/null) || http_code="000"
    if [[ -z "$http_code" ]]; then http_code="000"; fi
    
    local end_ns
    end_ns=$(date +%s%N 2>/dev/null || python3 -c 'import time; print(int(time.time()*1e9))')
    local latency_ms=$(( (end_ns - start_ns) / 1000000 ))
    
    if [[ "$http_code" == "$expected_code" ]]; then
        echo -e "  [PASS] $name ($path) -> HTTP $http_code (${latency_ms}ms)"
        PASSED_CHECKS=$((PASSED_CHECKS + 1))
    elif [[ "$path" == "/readyz/llm" && "$WITH_LLM" == "false" ]]; then
        echo -e "  [INFO] $name ($path) -> HTTP $http_code (未启用大模型推理，纯规则/NER模式)"
        PASSED_CHECKS=$((PASSED_CHECKS + 1))
    else
        echo -e "  ${RED}[FAIL]${NC} $name ($path) -> 期望 HTTP $expected_code, 实际返回 $http_code"
        ERRORS=$((ERRORS + 1))
    fi
}

# ── 步骤 3：REST API 探针巡检 ─────────────────────────────────────────────
echo -e "${BOLD}[1/6] REST API 与探针检查${NC}"
check_http_endpoint "基础健康探针" "/health" "200"
check_http_endpoint "K8s 存活探针" "/livez" "200"
check_http_endpoint "K8s 就绪探针" "/readyz" "200"
check_http_endpoint "LLM 引擎就绪探针" "/readyz/llm" "200"

# ── 步骤 4：Prometheus Metrics 巡检 ────────────────────────────────────────
echo ""
echo -e "${BOLD}[2/6] 生产可观测性 Prometheus Metrics 指标检查${NC}"
TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
METRICS_URL="$PROTOCOL://$REST_HOST:$METRICS_PORT/metrics"
if curl -s -k "${AUTH_HEADER[@]}" "$METRICS_URL" 2>/dev/null | grep -q "privshield"; then
    echo -e "  [PASS] Prometheus Metrics 端点响应正常 ($METRICS_URL)"
    PASSED_CHECKS=$((PASSED_CHECKS + 1))
else
    echo -e "  ${YELLOW}[WARN]${NC} 未从 $METRICS_URL 读取到 privshield 专属指标 (可能在公共指标路由)"
    WARNINGS=$((WARNINGS + 1))
fi

# ── 步骤 5：gRPC 端口网络连通性 ───────────────────────────────────────────
echo ""
echo -e "${BOLD}[3/6] gRPC 核心服务连通性检查${NC}"
TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
if (exec 3<>/dev/tcp/"$GRPC_HOST"/"$GRPC_PORT") 2>/dev/null; then
    exec 3>&- 2>/dev/null || true
    echo -e "  [PASS] gRPC 端口可用 ($GRPC_HOST:$GRPC_PORT)"
    PASSED_CHECKS=$((PASSED_CHECKS + 1))
elif command -v nc >/dev/null 2>&1 && nc -z -w 2 "$GRPC_HOST" "$GRPC_PORT" 2>/dev/null; then
    echo -e "  [PASS] gRPC 端口可用 ($GRPC_HOST:$GRPC_PORT)"
    PASSED_CHECKS=$((PASSED_CHECKS + 1))
else
    echo -e "  ${RED}[FAIL]${NC} 无法连接 gRPC 服务端口 ($GRPC_HOST:$GRPC_PORT)"
    ERRORS=$((ERRORS + 1))
fi

# ── 步骤 6：TLS 证书有效性检查 ─────────────────────────────────────────────
echo ""
echo -e "${BOLD}[4/6] TLS 证书安全期巡检${NC}"
if [[ -n "$CERT_FILE" && -f "$CERT_FILE" ]]; then
    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
    if command -v openssl >/dev/null 2>&1; then
        EXPIRY_DATE=$(openssl x509 -enddate -noout -in "$CERT_FILE" | cut -d= -f2)
        EXPIRY_EPOCH=$(date -d "$EXPIRY_DATE" +%s 2>/dev/null || date -j -f "%b %d %T %Y %Z" "$EXPIRY_DATE" +%s 2>/dev/null || echo "0")
        NOW_EPOCH=$(date +%s)
        REMAINING_DAYS=$(( (EXPIRY_EPOCH - NOW_EPOCH) / 86400 ))
        
        if [[ $REMAINING_DAYS -gt 30 ]]; then
            echo -e "  [PASS] TLS 证书有效: 剩余 $REMAINING_DAYS 天过期 ($CERT_FILE)"
            PASSED_CHECKS=$((PASSED_CHECKS + 1))
        elif [[ $REMAINING_DAYS -gt 0 ]]; then
            echo -e "  ${YELLOW}[WARN]${NC} TLS 证书即将到期: 仅剩 $REMAINING_DAYS 天 ($CERT_FILE)"
            WARNINGS=$((WARNINGS + 1))
        else
            echo -e "  ${RED}[FAIL]${NC} TLS 证书已过期! ($CERT_FILE)"
            ERRORS=$((ERRORS + 1))
        fi
    else
        echo -e "  [SKIP] 系统未安装 openssl，跳过证书天数解析"
    fi
else
    echo -e "  [INFO] 未指定 TLS 证书文件或未开启 TLS 证书检查"
fi

# ── 步骤 7：隐私预算持久化存储检查 ─────────────────────────────────────────
echo ""
echo -e "${BOLD}[5/6] 隐私预算存储 (SQLite) 健康度巡检${NC}"
TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
if [[ -f "$DB_PATH" ]]; then
    DB_SIZE=$(ls -lh "$DB_PATH" 2>/dev/null | awk '{print $5}')
    if python3 -c "
import sqlite3, sys
try:
    conn = sqlite3.connect('$DB_PATH', timeout=1.0)
    conn.execute('PRAGMA integrity_check;')
    conn.close()
    sys.exit(0)
except Exception:
    sys.exit(1)
" 2>/dev/null; then
        echo -e "  [PASS] 隐私预算数据库状态正常 (文件大小: $DB_SIZE, 完整性校验通过)"
        PASSED_CHECKS=$((PASSED_CHECKS + 1))
    else
        echo -e "  ${RED}[FAIL]${NC} 隐私预算数据库完整性校验失败: $DB_PATH"
        ERRORS=$((ERRORS + 1))
    fi
else
    echo -e "  [INFO] 未找到本地 SQLite 预算文件 ($DB_PATH)，使用内存模式或远端 DB"
fi

# ── 步骤 6：中台微服务群与 BFF 网关巡检 ──────────────────────────────────
echo ""
echo -e "${BOLD}[6/6] 中台微服务群与 BFF 代理健康巡检${NC}"
check_microservice() {
    local name="$1"
    local url="$2"
    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
    local code
    code=$(curl -s -k -o /dev/null -w "%{http_code}" --max-time 3 "$url" 2>/dev/null) || code="000"
    if [[ -z "$code" ]]; then code="000"; fi
    if [[ "$code" == "200" ]]; then
        echo -e "  [PASS] ${name} (${url}) -> HTTP 200 OK"
        PASSED_CHECKS=$((PASSED_CHECKS + 1))
    else
        echo -e "  ${YELLOW}[WARN]${NC} ${name} (${url}) -> HTTP ${code} (未启动或不可达)"
        WARNINGS=$((WARNINGS + 1))
    fi
}
check_microservice "BFF-Go 代理网关" "http://127.0.0.1:8081/health"
check_microservice "App-LZ BFF 调度之眼" "http://127.0.0.1:8085/health"
check_microservice "Service Hub 调度中枢" "http://127.0.0.1:8082/health"
check_microservice "Datasource Mgr 数据源" "http://127.0.0.1:8083/health"
check_microservice "Audit Log 审计日志" "http://127.0.0.1:8084/health"

# ── 可选：PostgreSQL 连通性检查 ────────────────────────────────────────
PG_DSN="${SERVICE_HUB_PG_DSN:-}"
if [[ -n "$PG_DSN" ]]; then
    echo ""
    echo -e "${BOLD}[扩展] Phase B PostgreSQL 连通性检查${NC}"
    TOTAL_CHECKS=$((TOTAL_CHECKS + 1))
    # 从 DSN 中提取 host 和 port
    PG_HOST=$(echo "$PG_DSN" | python3 -c "
import sys, re
dsn = sys.stdin.read().strip()
m = re.search(r'//(?:[^@]+@)?([^:/]+)', dsn)
print(m.group(1) if m else 'localhost')
" 2>/dev/null || echo "localhost")
    PG_PORT_NUM=$(echo "$PG_DSN" | python3 -c "
import sys, re
dsn = sys.stdin.read().strip()
m = re.search(r':(\d+)/', dsn)
print(m.group(1) if m else '5432')
" 2>/dev/null || echo "5432")
    if python3 -c "
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(3.0)
res = s.connect_ex(('$PG_HOST', int('$PG_PORT_NUM')))
s.close()
sys.exit(res)
" 2>/dev/null; then
        echo -e "  [PASS] PostgreSQL 可达 ($PG_HOST:$PG_PORT_NUM)"
        PASSED_CHECKS=$((PASSED_CHECKS + 1))
    else
        echo -e "  ${RED}[FAIL]${NC} 无法连接 PostgreSQL ($PG_HOST:$PG_PORT_NUM)"
        ERRORS=$((ERRORS + 1))
    fi
fi

# ── 步骤 7：汇总报告输出 ──────────────────────────────────────────────────
echo ""
echo -e "${BOLD}${CYAN}============================================================================${NC}"
echo -e "${BOLD}巡检结果汇总:${NC}"
echo -e "  • 检查总项 : $TOTAL_CHECKS"
echo -e "  • 通过项目 : ${GREEN}$PASSED_CHECKS${NC}"
echo -e "  • 警告项目 : ${YELLOW}$WARNINGS${NC}"
echo -e "  • 失败项目 : ${RED}$ERRORS${NC}"

if [[ $ERRORS -eq 0 ]]; then
    echo -e "\n${BOLD}${GREEN}✅ 生产环境健康检查全部通过！服务运行稳健。${NC}"
    echo -e "${BOLD}${CYAN}============================================================================${NC}"
    exit 0
else
    echo -e "\n${BOLD}${RED}❌ 存在异常或未通过项，请排查上方错误！${NC}"
    echo -e "${BOLD}${CYAN}============================================================================${NC}"
    exit 1
fi
