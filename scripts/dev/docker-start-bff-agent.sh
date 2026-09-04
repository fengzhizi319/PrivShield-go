#!/usr/bin/env bash
# ============================================================================
# 【Docker 模式】启动控制台三件套（Agent + Go BFF + Web UI）
# Launch Console Trio (Agent + Go BFF + Web UI) in Docker Compose
#
# 用法 / Usage:
#   ./scripts/dev/docker-start-bff-agent.sh [--build] [--no-build] [--force] [--mtls|--no-mtls]
#
# 模式说明 / Modes:
#   1. 标准非 mTLS 模式 (默认 / Standard):
#      - REST: http://localhost:8079 (Agent) / http://localhost:8081 (Go BFF)
#      - gRPC: 明文端口 localhost:50051 (Agent)
#   2. mTLS 双向认证模式 (--mtls / Mutual TLS):
#      - REST: https://localhost:8079 (Agent TLS) / http://localhost:8081 (Go BFF)
#      - gRPC: mTLS 加密与双向证书认证 localhost:50051 (Agent)
# ============================================================================

set -euo pipefail

# ── 解析脚本所在目录，定位项目根目录 ────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BUILD_FLAG="--build"   # 默认启动前重新构建镜像
MTLS_MODE=false
FORCE=false

# ── 解析命令行参数 ─────────────────────────────────────────────────────
for arg in "$@"; do
    case "$arg" in
        --no-build)
            BUILD_FLAG=""
            ;;
        --build)
            BUILD_FLAG="--build"
            ;;
        --mtls)
            MTLS_MODE=true
            ;;
        --no-mtls)
            MTLS_MODE=false
            ;;
        --force)
            FORCE=true
            ;;
        -h|--help)
            echo "用法 / Usage: $0 [选项 / Options]"
            echo ""
            echo "选项 / Options:"
            echo "  --mtls       以 mTLS 双向证书认证模式启动 (REST/HTTPS + gRPC/mTLS)"
            echo "  --no-mtls    以标准明文模式启动 (REST/HTTP + gRPC/Plain) (默认)"
            echo "  --no-build   跳过镜像构建，使用本地已有镜像"
            echo "  --build      启动前重新构建本地镜像 (默认)"
            echo "  --force      端口被占用时自动释放"
            echo "  -h, --help   显示帮助信息"
            exit 0
            ;;
    esac
done

echo "============================================================================"
if [[ "$MTLS_MODE" == "true" ]]; then
    echo "🌟 [Docker Mode] 正在启动 PrivShield 控制台套件 (mTLS 双向认证版本)..."
else
    echo "🌟 [Docker Mode] 正在启动 PrivShield 控制台套件 (标准非 mTLS 版本)..."
fi
echo "============================================================================"

# ── 端口清理 ──────────────────────────────────────────────────────────
PORTS=(8079 50051 8081 5173)
if [[ "$FORCE" == "true" ]]; then
    for p in "${PORTS[@]}"; do
        fuser -k -9 "${p}/tcp" 2>/dev/null || true
    done
fi

# ── mTLS 证书检查与就绪 ────────────────────────────────────────────────
CERT_DIR="$PROJECT_ROOT/console/engine-console/bff-go/certs"
if [[ "$MTLS_MODE" == "true" ]]; then
    if [[ ! -f "$CERT_DIR/ca.crt" || ! -f "$CERT_DIR/server.crt" || ! -f "$CERT_DIR/client.crt" ]]; then
        echo "🔐 未检测到完整 mTLS 证书，自动生成测试证书链..."
        bash "$PROJECT_ROOT/console/engine-console/bff-go/scripts/gen-certs.sh" "$CERT_DIR"
    fi
fi

# ── 前置准备：确保前端与 Go BFF 二进制已就绪 ──────────────────────────────
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

if [[ "$BUILD_FLAG" == "--build" ]]; then
    echo "🔨 准备 Go 微服务 Linux 二进制构建产物 (加速 Docker 本地构建)..."
    export GOPROXY="${GOPROXY:-https://goproxy.cn,https://goproxy.io,https://mirrors.aliyun.com/goproxy/,direct}"
    (cd "$PROJECT_ROOT/console/engine-console/bff-go" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
    (cd "$PROJECT_ROOT/services/service-hub" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
    (cd "$PROJECT_ROOT/console/mock-datasource" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
    (cd "$PROJECT_ROOT/services/audit-log" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
fi

# ── 清理可能残留的同名独立单容器 ─────────────────────────────────────────
docker rm -f PrivShield privacy-console-backend-go privacy-console-web 2>/dev/null || true

# ── 进入 docker-compose 目录，按模式启动容器 ───────────────────────────
cd "$PROJECT_ROOT/deploy/docker-compose"

COMPOSE_FILES=("-f" "docker-compose.yml")
if [[ "$MTLS_MODE" == "true" ]]; then
    COMPOSE_FILES+=("-f" "docker-compose.mtls.yml")
fi

# shellcheck disable=SC2086
docker compose "${COMPOSE_FILES[@]}" up -d $BUILD_FLAG PrivShield console-backend-go console-web

echo ""
echo "============================================================================"
if [[ "$MTLS_MODE" == "true" ]]; then
    echo " ✨ PrivShield 控制台套件 [mTLS 双向认证版本] 已成功启动！"
    echo " 🌐 React 控制台 Web UI   : http://localhost:5173"
    echo " 🔌 Go BFF 代理网关 REST   : http://localhost:8081"
    echo " 🛡️ Privacy Agent REST    : https://localhost:8079 (TLS 加密)"
    echo " ⚡ Privacy Agent gRPC    : localhost:50051 (mTLS 双向证书鉴权)"
    echo " 🔒 受信任根 CA 证书路径   : $CERT_DIR/ca.crt"
else
    echo " ✨ PrivShield 控制台套件 [标准非 mTLS 版本] 已成功启动！"
    echo " 🌐 React 控制台 Web UI   : http://localhost:5173"
    echo " 🔌 Go BFF 代理网关 REST   : http://localhost:8081"
    echo " 🛡️ Privacy Agent REST    : http://localhost:8079 (明文 HTTP)"
    echo " ⚡ Privacy Agent gRPC    : localhost:50051 (明文 gRPC)"
fi
echo " 停止服务命令            : ./scripts/dev/docker-stop.sh"
echo "============================================================================"
