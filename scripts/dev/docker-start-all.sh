#!/usr/bin/env bash
# ============================================================================
# 【Docker 模式】启动全栈服务（Go Engine + Go BFF + Web UI + 可选 vLLM/PG/监控）
# Launch Full Stack (Go Engine + Go BFF + Web UI + optional vLLM/PG/Monitoring)
#
# 与 docker-start-all.sh 的区别：
#   - docker-start-all.sh 使用 Python 引擎作为 Agent
#   - 本脚本使用 Go 原生引擎（services/privacy-engine/Dockerfile，极小镜像 ~15MB）替代 Python 引擎
#
# 用法 / Usage: ./scripts/dev/docker-start-go-all.sh [--with-llm] [--with-postgres] [--with-monitoring] [--no-build]
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
WITH_LLM=false
WITH_POSTGRES=false
WITH_MONITORING=false
BUILD_FLAG="--build"

for arg in "$@"; do
    case "$arg" in
        --with-llm)        WITH_LLM=true ;;
        --with-postgres)   WITH_POSTGRES=true ;;
        --with-monitoring) WITH_MONITORING=true ;;
        --no-build)        BUILD_FLAG="" ;;
        --build)           BUILD_FLAG="--build" ;;
        -h|--help)
            echo "用法 / Usage: $0 [--with-llm] [--with-postgres] [--with-monitoring] [--build] [--no-build]"
            echo ""
            echo "选项 / Options:"
            echo "  --with-llm        同时启动 vLLM 大模型推理容器 (需 GPU)"
            echo "  --with-postgres   同时启动 Phase B PostgreSQL (多副本 Hub 模式)"
            echo "  --with-monitoring 同时启动 Prometheus + Grafana 监控栈"
            echo "  --no-build        跳过镜像构建，使用本地已有镜像"
            echo "  --build           启动前重新构建本地镜像 (默认)"
            echo "  -h, --help        显示帮助信息"
            exit 0
            ;;
    esac
done

echo "============================================================================"
echo "🌟 [Docker Mode - Go Engine] 正在启动 PrivShield 全栈容器套件..."
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

# ── 构建 Go 引擎镜像 ────────────────────────────────────────────────────
GO_IMAGE="privshield-go:1.0.0"
if [[ "$BUILD_FLAG" == "--build" ]]; then
    echo "📦 构建 Go 原生引擎镜像 (${GO_IMAGE})..."
    docker build -t "$GO_IMAGE" -f "$PROJECT_ROOT/services/privacy-engine/Dockerfile" "$PROJECT_ROOT"
fi

# ── 清理旧容器 ──────────────────────────────────────────────────────────
docker rm -f PrivShield-Go privacy-console-backend-go privacy-console-web privshield-service-hub privshield-datasource-mgr privshield-audit-log 2>/dev/null || true

# ── 进入 docker-compose 目录，启动容器 ──────────────────────────────────
cd "$PROJECT_ROOT/deploy/docker-compose"

# 设置环境变量告知 compose 使用 Go 引擎
export PRIVSHIELD_ENGINE_TYPE=go
export PRIVSHIELD_GO_IMAGE="$GO_IMAGE"

COMPOSE_CMD="docker compose -f docker-compose.yml -f docker-compose.go-engine.yml"
if [[ "$WITH_LLM" == "true" ]]; then
    COMPOSE_CMD="$COMPOSE_CMD --profile llm"
    echo "🤖 同时启动 vLLM 大模型推理容器 (GPU)..."
fi
if [[ "$WITH_POSTGRES" == "true" ]]; then
    COMPOSE_CMD="$COMPOSE_CMD --profile phase-b"
    echo "🐘 同时启动 Phase B PostgreSQL (多副本 Hub 模式)..."
fi
if [[ "$WITH_MONITORING" == "true" ]]; then
    COMPOSE_CMD="$COMPOSE_CMD --profile monitoring"
    echo "📊 同时启动 Prometheus + Grafana 监控栈..."
fi

# shellcheck disable=SC2086
$COMPOSE_CMD up -d $BUILD_FLAG

echo ""
echo "✅ 全栈 Docker 容器服务 (Go Engine) 已成功启动！"
echo "   - React 控制台 Web UI     : http://localhost:5173"
echo "   - Go BFF 代理网关 REST     : http://localhost:8081"
echo "   - Go Engine REST          : http://localhost:8079"
echo "   - Go Engine gRPC          : localhost:50051"
echo "   - Service Hub 调度中枢    : http://localhost:8082"
echo "   - Datasource Mgr 数据源   : http://localhost:8083"
echo "   - Audit Log 脱敏审计日志  : http://localhost:8084"
if [[ "$WITH_LLM" == "true" ]]; then
    echo "   - vLLM 本地大模型推理     : http://localhost:8000/v1"
fi
if [[ "$WITH_POSTGRES" == "true" ]]; then
    echo "   - PostgreSQL (Phase B)    : localhost:5432"
fi
if [[ "$WITH_MONITORING" == "true" ]]; then
    echo "   - Prometheus 监控         : http://localhost:9090"
    echo "   - Grafana 可视化面板      : http://localhost:3000"
fi
echo "============================================================================"
