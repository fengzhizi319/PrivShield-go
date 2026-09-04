#!/usr/bin/env bash
# ============================================================================
# 【Docker 模式】启动全栈服务（Agent + Go BFF + Web UI + 可选 vLLM/PG/监控）
# Launch Full Stack Container Suite in Docker Compose
#
# 用法 / Usage: ./scripts/dev/docker-start-all.sh [--with-llm] [--with-postgres] [--with-monitoring] [--no-build]
# ============================================================================

set -euo pipefail

# ── 解析脚本所在目录，定位项目根目录 ────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
WITH_LLM=false
WITH_POSTGRES=false
WITH_MONITORING=false
BUILD_FLAG="--build"   # 默认启动前重新构建镜像

# ── 解析命令行参数 ─────────────────────────────────────────────────────
#   --with-llm        : 同时启动 vLLM 大模型推理容器（需 GPU 支持）
#   --with-postgres   : 同时启动 Phase B PostgreSQL（多副本 Hub 模式）
#   --with-monitoring : 同时启动 Prometheus + Grafana 监控栈
#   --no-build        : 跳过镜像构建，直接使用本地已有镜像
#   --build           : 启动前重新构建本地镜像（默认行为）
for arg in "$@"; do
    case "$arg" in
        --with-llm)
            WITH_LLM=true
            ;;
        --with-postgres)
            WITH_POSTGRES=true
            ;;
        --with-monitoring)
            WITH_MONITORING=true
            ;;
        --no-build)
            BUILD_FLAG=""
            ;;
        --build)
            BUILD_FLAG="--build"
            ;;
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
echo "🌟 [Docker Mode] 正在启动 PrivShield 全栈容器套件..."
echo "============================================================================"

# ── 前置准备：确保前端与 Go 微服务二进制已就绪 ───────────────────────────
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
    echo "🔨 准备 Go 引擎与微服务二进制构建产物 (加速 Docker 本地构建)..."
    export GOPROXY="${GOPROXY:-https://goproxy.cn,https://goproxy.io,https://mirrors.aliyun.com/goproxy/,direct}"
    (cd "$PROJECT_ROOT/services/privacy-engine" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/privshield-agent ./cmd/privshield-agent 2>/dev/null || true)
    (cd "$PROJECT_ROOT/console/engine-console/bff-go" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
    (cd "$PROJECT_ROOT/services/service-hub" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
    (cd "$PROJECT_ROOT/console/mock-datasource" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
    (cd "$PROJECT_ROOT/services/audit-log" && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server 2>/dev/null || true)
fi

# ── 清理可能残留的同名独立单容器 ─────────────────────────────────────────
docker rm -f PrivShield PrivShield-vllm privacy-console-backend-go privacy-console-web privshield-service-hub privshield-datasource-mgr privshield-audit-log 2>/dev/null || true

# ── 进入 docker-compose 目录，启动容器 ──────────────────────────────────
# 默认仅启动核心服务（Agent + Go BFF + Web UI + 3大中台微服务）
# 传入 --with-llm 时激活 llm profile，额外启动 vLLM 推理容器
# 传入 --with-postgres 时激活 phase-b profile，启动 PostgreSQL
cd "$PROJECT_ROOT/deploy/docker-compose"

# 构建 compose 命令，动态添加 profile
COMPOSE_CMD="docker compose"
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
echo "✅ 全栈 Docker 容器服务已成功启动！"
echo "   - React 控制台 Web UI     : http://localhost:5173"
echo "   - Go BFF 代理网关 REST     : http://localhost:8081"
echo "   - Service Hub 调度中枢    : http://localhost:8082"
echo "   - Datasource Mgr 数据源   : http://localhost:8083"
echo "   - Audit Log 脱敏审计日志  : http://localhost:8084"
echo "   - Privacy Agent REST      : http://localhost:8079"
echo "   - Privacy Agent gRPC      : localhost:50051"
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
