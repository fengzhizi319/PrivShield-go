#!/usr/bin/env bash
# ============================================================================
# 【Docker 模式】启动控制台三件套（Go Engine + Go BFF + Web UI）
# Launch Console Trio (Go Engine + Go BFF + Web UI) in Docker Compose
#
# 与 docker-start-bff-agent.sh 的区别：
#   - docker-start-bff-agent.sh 使用 Python 引擎作为 Agent
#   - 本脚本使用 Go 原生引擎（services/privacy-engine/Dockerfile）替代 Python 引擎
#
# 用法 / Usage:
#   ./scripts/dev/docker-start-bff-go-agent.sh [--build] [--no-build] [--force] [--mtls|--no-mtls]
#
# 模式说明 / Modes:
#   1. 标准模式 (默认):
#      - REST: http://localhost:8079 (Go Engine) / http://localhost:8081 (Go BFF)
#      - gRPC: 明文端口 localhost:50051 (Go Engine)
#   2. mTLS 双向认证模式 (--mtls):
#      - REST: https://localhost:8079 (Go Engine TLS) / http://localhost:8081 (Go BFF)
#      - gRPC: mTLS 加密 localhost:50051 (Go Engine)
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BUILD_FLAG="--build"
MTLS_MODE=false
FORCE=false

for arg in "$@"; do
    case "$arg" in
        --no-build)   BUILD_FLAG="" ;;
        --build)      BUILD_FLAG="--build" ;;
        --mtls)       MTLS_MODE=true ;;
        --no-mtls)    MTLS_MODE=false ;;
        --force)      FORCE=true ;;
        -h|--help)
            echo "用法 / Usage: $0 [--build] [--no-build] [--force] [--mtls|--no-mtls]"
            echo ""
            echo "选项 / Options:"
            echo "  --mtls       以 mTLS 模式启动 (Go Engine TLS + gRPC mTLS)"
            echo "  --no-mtls    以标准明文模式启动 (默认)"
            echo "  --build      启动前重新构建镜像 (默认)"
            echo "  --no-build   跳过镜像构建"
            echo "  --force      强制重新构建所有依赖"
            exit 0
            ;;
    esac
done

echo "============================================================================"
echo "🚀 [Docker Mode - Go Engine] 启动控制台三件套 (Go Engine + Go BFF + Web)"
echo "============================================================================"

# ── 前置准备：构建前端 ──────────────────────────────────────────────────
if [[ ! -d "$PROJECT_ROOT/console/engine-console/web/dist" || "$BUILD_FLAG" == "--build" ]]; then
    echo "📦 准备前端静态资源 (Vite build)..."
    (
        cd "$PROJECT_ROOT/console/engine-console/web"
        if command -v corepack >/dev/null 2>&1; then
            corepack pnpm build 2>/dev/null || npm run build
        elif command -v pnpm >/dev/null 2>&1; then
            pnpm build 2>/dev/null || npm run build
        elif command -v npm >/dev/null 2>&1; then
            npm run build
        fi
    )
fi

# ── 前置准备：构建 Go 微服务二进制 ─────────────────────────────────────
if [[ "$BUILD_FLAG" == "--build" ]]; then
    echo "🔨 准备 Go 微服务二进制构建产物..."
    export GOPROXY="${GOPROXY:-https://goproxy.cn,https://goproxy.io,direct}"
    (cd "$PROJECT_ROOT/console/engine-console/bff-go" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
    (cd "$PROJECT_ROOT/services/service-hub" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
    (cd "$PROJECT_ROOT/console/mock-datasource" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
    (cd "$PROJECT_ROOT/services/audit-log" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
fi

# ── 清理旧容器 ──────────────────────────────────────────────────────────
docker rm -f PrivShield-Go privacy-console-backend-go privacy-console-web privshield-service-hub privshield-datasource-mgr privshield-audit-log 2>/dev/null || true

# ── 使用 docker-compose 启动（Go Engine 替代 Python Agent）─────────────
cd "$PROJECT_ROOT/deploy/docker-compose"

# 设置环境变量告知 compose 使用 Go 引擎
export PRIVSHIELD_ENGINE_TYPE=go
export PRIVSHIELD_GO_IMAGE="privshield-go:1.0.0"

# 先构建 Go 引擎镜像
echo "📦 构建 Go 原生引擎镜像..."
docker build -t "$PRIVSHIELD_GO_IMAGE" -f "$PROJECT_ROOT/services/privacy-engine/Dockerfile" "$PROJECT_ROOT"

# shellcheck disable=SC2086
if [[ "$MTLS_MODE" == "true" ]]; then
    echo "🔐 mTLS 模式已启用"
    docker compose -f docker-compose.yml -f docker-compose.mtls.yml up -d $BUILD_FLAG
else
    docker compose up -d $BUILD_FLAG
fi

echo ""
echo "✅ 控制台三件套 (Go Engine) 已成功启动！"
echo "   - React 控制台 Web UI     : http://localhost:5173"
echo "   - Go BFF 代理网关 REST     : http://localhost:8081"
echo "   - Go Engine REST          : http://localhost:8079"
echo "   - Go Engine gRPC          : localhost:50051"
echo "   - Service Hub 调度中枢    : http://localhost:8082"
echo "   - Datasource Mgr 数据源   : http://localhost:8083"
echo "   - Audit Log 脱敏审计日志  : http://localhost:8084"
if [[ "$MTLS_MODE" == "true" ]]; then
    echo "   - TLS/mTLS 模式          : 已启用"
fi
echo "============================================================================"
