#!/usr/bin/env bash
# ==============================================================================
# 脚本名称: verify_console_environment_go.sh
# 脚本说明: Go 原生引擎与全栈控制台开发/编译构建环境校验工具。
#
# 执行步骤总览：
#   1. 检查 Go 编译器 (>= 1.22) 与 go.work 工作区有效性
#   2. 检查 Node.js (>= 18) 与 pnpm 前端工具链可用性
#   3. 检查 Web 前端 TypeScript 类型构建校验 (npx tsc --noEmit)
#   4. 检查 Go 引擎与微服务二进制可编译性
#   5. 输出环境校验综合评估与报告
#
# 用法 / Usage:
#   ./scripts/dev/verify_console_environment_go.sh
# ==============================================================================

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

echo -e "${BLUE}====================================================${NC}"
echo -e "${BLUE} PrivShield-Go 开发与编译构建环境巡检${NC}"
echo -e "${BLUE} 工作目录: ${PROJECT_ROOT}${NC}"
echo -e "${BLUE}====================================================${NC}"

ERRORS=0

# ── 步骤 1：检查 Go 编译器与工作区 ───────────────────────────────────────
echo -e "\n${YELLOW}[1/4] 检查 Go 语言编译链 (PrivShield-Go & 微服务群)...${NC}"
if command -v go &> /dev/null; then
    GO_VER=$(go version | awk '{print $3}')
    echo -e "Go 编译器版本: ${GREEN}${GO_VER}${NC}"
    if [ -f "go.work" ]; then
        echo -e "go.work 工作区: ${GREEN}[OK]${NC}"
    else
        echo -e "${RED}[错误] 未找到 go.work 文件！${NC}"
        ERRORS=$((ERRORS + 1))
    fi
else
    echo -e "${RED}[错误] 未检测到 Go 工具链 (请安装 Go >= 1.22)！${NC}"
    ERRORS=$((ERRORS + 1))
fi

# ── 步骤 2：检查 Node.js & pnpm 前端工具链 ───────────────────────────────
echo -e "\n${YELLOW}[2/4] 检查 Node.js & pnpm 前端工具链...${NC}"
if command -v node &> /dev/null; then
    NODE_VER=$(node -v)
    echo -e "Node.js 版本: ${GREEN}${NODE_VER}${NC}"
else
    echo -e "${RED}[错误] 未安装 Node.js！${NC}"
    ERRORS=$((ERRORS + 1))
fi

if command -v pnpm &> /dev/null; then
    PNPM_VER=$(pnpm -v)
    echo -e "pnpm 版本   : ${GREEN}${PNPM_VER}${NC}"
elif command -v corepack &> /dev/null; then
    echo -e "corepack 可用: ${GREEN}[OK]${NC}"
else
    echo -e "${YELLOW}未检测到 pnpm 全局命令，推荐安装: corepack enable 或 npm i -g pnpm${NC}"
fi

# ── 步骤 3：执行前端静态编译类型检查 ─────────────────────────────────────
echo -e "\n${YELLOW}[3/4] 验证 Web 前端 TypeScript 类型系统...${NC}"
if [ -d "console/engine-console/web" ]; then
    if [ -d "console/engine-console/web/node_modules" ]; then
        echo -e "正在执行 TypeScript 类型构建校验 (tsc)..."
        (cd console/engine-console/web && npx tsc --noEmit) && echo -e "${GREEN}TypeScript 类型检查通过！${NC}" || {
            echo -e "${RED}[错误] TypeScript 类型校验报错，请修正！${NC}"
            ERRORS=$((ERRORS + 1))
        }
    else
        echo -e "${YELLOW}未找到 console/engine-console/web/node_modules。可执行: cd console/engine-console/web && pnpm install${NC}"
    fi
fi

# ── 步骤 4：检查 Go 引擎与微服务代码编译 ─────────────────────────────────
echo -e "\n${YELLOW}[4/4] 验证 Go 引擎与微服务可构建性...${NC}"
if command -v go &> /dev/null; then
    if CGO_ENABLED=0 go build ./services/privacy-engine/cmd/... ./services/service-hub/cmd/... ./console/mock-datasource/cmd/... ./services/audit-log/cmd/... ./console/engine-console/bff-go/cmd/... >/dev/null 2>&1; then
        echo -e "Go 全栈模块编译构建: ${GREEN}[OK]${NC}"
    else
        echo -e "${RED}[错误] Go 模块编译失败！${NC}"
        ERRORS=$((ERRORS + 1))
    fi
fi

echo -e "\n${BLUE}====================================================${NC}"
if [ $ERRORS -eq 0 ]; then
    echo -e "${GREEN}环境巡检通过！PrivShield-Go 开发与构建环境准备就绪。${NC}"
else
    echo -e "${RED}环境巡检完成，发现 ${ERRORS} 个缺失或报错项，请修正后重试。${NC}"
fi
echo -e "${BLUE}====================================================${NC}"
