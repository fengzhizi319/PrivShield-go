#!/usr/bin/env bash
# ============================================================================
# 【Docker 模式】一键启动 PrivShield 调度之眼全栈测试集群 (Go 原生引擎版)
# Launch PrivShield App-LZ Full Stack in Docker Compose with Go Engine
#
# 用法 / Usage: ./scripts/dev/docker-start-app-lz-go.sh [--build] [--no-build] [--force]
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BUILD_FLAG="--build"
FORCE=false

for arg in "$@"; do
    case "$arg" in
        --no-build)
            BUILD_FLAG=""
            ;;
        --build)
            BUILD_FLAG="--build"
            ;;
        --force)
            FORCE=true
            ;;
        -h|--help)
            echo "用法 / Usage: $0 [--build] [--no-build] [--force]"
            echo ""
            echo "选项 / Options:"
            echo "  --no-build   跳过镜像构建，使用本地已有镜像"
            echo "  --build      启动前重新构建本地镜像 (默认)"
            echo "  --force      自动终止占用端口的非 Docker 进程"
            echo "  -h, --help   显示帮助信息"
            exit 0
            ;;
    esac
done

echo "============================================================================"
echo "🌟 [Docker Mode] 正在启动 PrivShield 调度之眼全景测试集群 (Go 原生引擎版)..."
echo "============================================================================"

# ── 端口清理 ──────────────────────────────────────────────────────────
PORTS=(8079 50051 8082 50052 8083 50053 8084 50054 8085 50055 5174)
if [[ "$FORCE" == "true" ]]; then
    for p in "${PORTS[@]}"; do
        fuser -k -9 "${p}/tcp" 2>/dev/null || true
    done
fi

# ── 确保 Go 引擎镜像已构建 ──────────────────────────────────────────
echo "📦 准备 PrivShield-Go 原生引擎 Docker 镜像..."
(
    cd "$PROJECT_ROOT"
    docker build -f services/privacy-engine/Dockerfile -t privshield-go:1.0.0 . >/dev/null 2>&1 || true
)

# ── 前置准备：确保前端与 Go 二进制已就绪（加速 Docker 本地构建） ───────────
if [[ ! -d "$PROJECT_ROOT/console/app-lz/web/dist" || "$BUILD_FLAG" == "--build" ]]; then
    echo "📦 准备 App-LZ 前端静态资源 (Vite build)..."
    (
        cd "$PROJECT_ROOT/console/app-lz/web"
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
    echo "🔨 准备 Go 微服务 Linux 二进制构建产物..."
    export GOPROXY="${GOPROXY:-https://goproxy.cn,https://goproxy.io,https://mirrors.aliyun.com/goproxy/,direct}"
    (cd "$PROJECT_ROOT/console/app-lz/bff-go" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
    (cd "$PROJECT_ROOT/services/service-hub" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
    (cd "$PROJECT_ROOT/console/mock-datasource" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
    (cd "$PROJECT_ROOT/services/audit-log" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
fi

# ── 清理可能残留的同名容器 ──────────────────────────────────────────────
docker rm -f PrivShield PrivShield-Go privshield-service-hub privshield-datasource-mgr privshield-audit-log privshield-app-lz-bff privshield-app-lz-web 2>/dev/null || true

# ── 启动 Docker Compose 编排 (叠加 Go 引擎覆盖层) ────────────────────────
cd "$PROJECT_ROOT/deploy/docker-compose"
# shellcheck disable=SC2086
docker compose -f docker-compose.app-lz.yml -f docker-compose.app-lz-go-engine.yml up -d $BUILD_FLAG

echo ""
echo "============================================================================"
echo " ✨ PrivShield App-LZ (Go Engine) 调度之眼容器集群已成功启动！"
echo " 🌐 React 控制台 Web 大屏   : http://localhost:5174"
echo " 🔌 App-LZ Go BFF 聚合接口  : http://localhost:8085"
echo " 🚀 Service Hub 调度中枢    : http://localhost:8082 (gRPC: 50052)"
echo " 📊 Datasource Mgr 数据源   : http://localhost:8083 (gRPC: 50053)"
echo " 🛡️ Audit Log 审计存证      : http://localhost:8084 (gRPC: 50054)"
echo " ⚡ Privacy Agent 算力引擎  : http://localhost:8079 (gRPC: 50051) [Go 原生]"
echo " 停止服务命令              : ./scripts/dev/docker-stop-app-lz.sh"
echo "============================================================================"
