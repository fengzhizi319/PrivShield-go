#!/usr/bin/env bash
# ==============================================================================
# 脚本名称: run_console_e2e_tests.sh
# 脚本说明: 一键运行 Console 控制台前后端 (Web + Go BFF Gateway + Services)
#           的全套端到端 (E2E) 集成自动化回归测试。
#
# 执行步骤总览：
#   0. 自动定位并切换至项目根目录，注册退出清理钩子
#   1. 启动轻量 Mock Agent 桩服务 (端口 8079)
#   2. 运行 Console BFF-Go (REST/gRPC/mTLS 双协议) 与 Pkg 基础库测试
#   3. 运行 Services 微服务群 (service-hub / datasource-mgr / audit-log) 测试
#   4. 运行 Console Web (React 前端) Vitest 自动化单元与组件测试
#   5. 统计并输出测试执行汇总（防止全跳过误报成功）
#
# 用法 / Usage:
#   ./scripts/dev/run_console_e2e_tests.sh
#   # 或在任意目录下执行:
#   bash /path/to/PrivShield/scripts/dev/run_console_e2e_tests.sh
# ==============================================================================

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

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# ── 步骤 0.1：定位并切换至项目根目录 ──────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
PROJECT_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null || (cd "$SCRIPT_DIR/../.." && pwd -P))"
cd "$PROJECT_ROOT"

MOCK_PID=""
BACKEND_PID=""

TESTS_RUN=0
TESTS_PASSED=0
TESTS_SKIPPED=0

# ── 步骤 0.2：注册退出资源自动清理钩子 ───────────────────────────────────
cleanup() {
    echo -e "\n${YELLOW}[清理] 正在释放测试模拟服务与临时资源...${NC}"
    if [ -n "$BACKEND_PID" ]; then
        kill "$BACKEND_PID" 2>/dev/null || true
    fi
    if [ -n "$MOCK_PID" ]; then
        kill "$MOCK_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

echo -e "${BLUE}====================================================${NC}"
echo -e "${BLUE} Console 端到端 (E2E) 全套自动化测试套件${NC}"
echo -e "${BLUE} 工作目录: ${PROJECT_ROOT}${NC}"
echo -e "${BLUE}====================================================${NC}"

# ── 检查端口占用情况 ──────────────────────────────────────────────────────
if command -v ss &>/dev/null && ss -tulpn | grep -q -E ':8079 '; then
    echo -e "${RED}[错误] 端口 8079 已被占用！${NC}"
    echo -e "${YELLOW}提示: 请先停止正在运行的 PrivShield 容器或本地服务 (例如执行: bash ./scripts/dev/docker-stop.sh 或 bash ./scripts/dev/dev-stop.sh)${NC}"
    exit 1
fi

# ── 步骤 1：启动 Go Agent 核心引擎 (端口 8079 / 50051) ──────────────────────
echo -e "\n${YELLOW}[步骤 1/5] 启动 PrivShield-Go 原生引擎 (REST: 8079, gRPC: 50051)...${NC}"
(
    cd "$PROJECT_ROOT/services/privacy-engine"
    CGO_ENABLED=0 go build -o bin/privshield-agent ./cmd/privshield-agent
)
PRIVACY_REST_HOST=127.0.0.1 PRIVACY_REST_PORT=8079 \
PRIVACY_GRPC_HOST=127.0.0.1 PRIVACY_GRPC_PORT=50051 \
PRIVACY_LOG_LEVEL=WARN \
"$PROJECT_ROOT/services/privacy-engine/bin/privshield-agent" >/dev/null 2>&1 &
MOCK_PID=$!
sleep 1

if ! kill -0 "$MOCK_PID" 2>/dev/null; then
    echo -e "${RED}[错误] PrivShield-Go Agent 启动失败！${NC}"
    exit 1
fi
echo -e "${GREEN}PrivShield-Go Agent 已启动 (PID: ${MOCK_PID})${NC}"

# ── 步骤 2：运行 Go 原生隐私 SDK 与 Engine 测试 ───────────────────────────
echo -e "\n${YELLOW}[步骤 2/5] 运行 services/privacy-engine/sdk 与 services/privacy-engine 核心引擎测试...${NC}"
TESTS_RUN=$((TESTS_RUN + 1))
if CGO_ENABLED=0 go test ./services/privacy-engine/sdk/... ./services/privacy-engine/...; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    echo -e "${GREEN}[成功] Go 原生隐私引擎与 SDK 测试全部通过！${NC}"
else
    echo -e "${RED}[失败] Go 原生引擎测试未通过！${NC}"
fi

# ── 步骤 3：运行 Go BFF 网关 (REST/gRPC/mTLS) 与共享库测试 ────────────────
echo -e "\n${YELLOW}[步骤 3/5] 运行 Console BFF-Go (REST/gRPC/mTLS) 与 Pkg 基础库测试...${NC}"
if [ -d "console/engine-console/bff-go" ]; then
    TESTS_RUN=$((TESTS_RUN + 1))
    if CGO_ENABLED=0 go test ./pkg/... ./console/engine-console/bff-go/...; then
        TESTS_PASSED=$((TESTS_PASSED + 1))
        echo -e "${GREEN}[成功] Go BFF 与 Pkg 基础库测试通过！${NC}"
    else
        echo -e "${RED}[失败] Go BFF 与 Pkg 基础库测试未通过！${NC}"
    fi
else
    TESTS_SKIPPED=$((TESTS_SKIPPED + 1))
    echo -e "${YELLOW}[跳过] 未发现 go 命令或 console/engine-console/bff-go 目录。${NC}"
fi

# ── 步骤 4：运行 Services 微服务群测试 ────────────────────────────────────
echo -e "\n${YELLOW}[步骤 4/5] 运行 Services 微服务群 (service-hub / datasource-mgr / audit-log) 测试...${NC}"
if command -v go &> /dev/null && [ -d "services" ]; then
    TESTS_RUN=$((TESTS_RUN + 1))
    if CGO_ENABLED=0 go test ./services/service-hub/... ./console/mock-datasource/... ./services/audit-log/...; then
        TESTS_PASSED=$((TESTS_PASSED + 1))
        echo -e "${GREEN}[成功] Services 中台微服务群测试通过！${NC}"
    else
        echo -e "${RED}[失败] Services 中台微服务群测试未通过！${NC}"
    fi
else
    TESTS_SKIPPED=$((TESTS_SKIPPED + 1))
    echo -e "${YELLOW}[跳过] 未发现 go 命令或 services 目录。${NC}"
fi

# ── 步骤 5：运行 Web 前端组件与单元测试 ───────────────────────────────────
echo -e "\n${YELLOW}[步骤 5/5] 运行 Console Web (React) 组件与自动化测试...${NC}"
if [ -d "console/engine-console/web" ]; then
    TESTS_RUN=$((TESTS_RUN + 1))
    WEB_TEST_OK=false
    if (cd console/engine-console/web && command -v corepack &> /dev/null && corepack pnpm test -- --run); then
        WEB_TEST_OK=true
    elif (cd console/engine-console/web && command -v pnpm &> /dev/null && pnpm test -- --run); then
        WEB_TEST_OK=true
    elif (cd console/engine-console/web && command -v npm &> /dev/null && npm test -- --run); then
        WEB_TEST_OK=true
    fi
    if [ "$WEB_TEST_OK" = true ]; then
        TESTS_PASSED=$((TESTS_PASSED + 1))
        echo -e "${GREEN}[成功] React 前端组件测试通过！${NC}"
    else
        echo -e "${RED}[失败] React 前端组件测试未通过！${NC}"
    fi
else
    TESTS_SKIPPED=$((TESTS_SKIPPED + 1))
    echo -e "${YELLOW}[跳过] 未发现 console/engine-console/web 目录。${NC}"
fi

# ── 步骤 4：汇总测试结果与状态输出 ────────────────────────────────────────
echo -e "\n${BLUE}====================================================${NC}"
echo -e "${BLUE}           E2E 集成测试执行结果汇总                ${NC}"
echo -e "${BLUE}====================================================${NC}"
echo -e "  已执行测试项: ${CYAN}${TESTS_RUN}${NC}"
echo -e "  成功通过项:   ${GREEN}${TESTS_PASSED}${NC}"
echo -e "  跳过测试项:   ${YELLOW}${TESTS_SKIPPED}${NC}"
echo -e "${BLUE}----------------------------------------------------${NC}"

if [ "$TESTS_RUN" -eq 0 ]; then
    echo -e "${RED}[警告] 没有执行任何测试模块（所有步骤均被跳过）！请检查环境配置与目录结构。${NC}"
    exit 1
elif [ "$TESTS_PASSED" -eq "$TESTS_RUN" ]; then
    if [ "$TESTS_SKIPPED" -gt 0 ]; then
        echo -e "${YELLOW}⚠️  已执行的 ${TESTS_PASSED} 项测试全部通过，但有 ${TESTS_SKIPPED} 项测试被跳过。${NC}"
    else
        echo -e "${GREEN}🎉 恭喜！Console 前后端端到端 (E2E) 全套测试 100% 全部通过！${NC}"
    fi
else
    echo -e "${RED}❌ 测试套件执行失败，存在未通过项！${NC}"
    exit 1
fi
echo -e "${BLUE}====================================================${NC}"
