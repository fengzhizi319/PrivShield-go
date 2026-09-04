#!/usr/bin/env bash
# ============================================================================
# 【开发模式】使用 Go 原生引擎构建并启动 PrivShield (Docker)
# Build & Launch PrivShield Go Engine in Docker container
#
# 与 docker-start-agent.sh 的区别：
#   - docker-start-agent.sh 使用 Python 引擎（Dockerfile --target core/ml）
#   - 本脚本使用 Go 原生引擎（services/privacy-engine/Dockerfile，极小镜像 ~15MB）
#
# 执行步骤总览：
#   1. 前置检查 Docker CLI 与 Daemon
#   2. 使用 services/privacy-engine/Dockerfile 构建 Go 引擎镜像
#   3. 停止并清理旧容器
#   4. 启动 Go 引擎容器，映射 REST (8079) 与 gRPC (50051) 端口
#
# 用法 / Usage: ./scripts/dev/docker-start-go-agent.sh
# ============================================================================

for arg in "$@"; do
    case "$arg" in
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "说明: 使用 Docker 构建并后台启动 PrivShield Privacy Engine"
            echo "端口映射: REST :8079, gRPC :50051"
            exit 0
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# ── 步骤 1：Docker 环境检查 ─────────────────────────────────────────────
if ! command -v docker >/dev/null 2>&1; then
    echo "❌ [错误] 未检测到 docker 命令，请先安装 Docker" >&2
    exit 1
fi
if ! docker info >/dev/null 2>&1; then
    echo "❌ [错误] 无法连接到 Docker 守护进程" >&2
    exit 1
fi

echo "============================================================================"
echo "🚀 [Docker Mode - Go Engine] 正在构建并启动 PrivShield Go 引擎"
echo "============================================================================"

cd "$PROJECT_ROOT"

# ── 步骤 2：构建 Go 引擎镜像 ────────────────────────────────────────────
IMAGE_NAME="privshield-go:1.0.0"
echo "📦 构建 Go 原生引擎镜像 (${IMAGE_NAME})..."
docker build -t "$IMAGE_NAME" -f services/privacy-engine/Dockerfile .

# ── 步骤 3：清理旧容器 ──────────────────────────────────────────────────
docker rm -f PrivShield-Go 2>/dev/null || true

HOST_REST_PORT="${PRIVACY_REST_PORT:-8079}"
HOST_GRPC_PORT="${PRIVACY_GRPC_PORT:-50051}"

# ── 步骤 4：启动 Go 引擎容器 ────────────────────────────────────────────
docker run -d \
  --name PrivShield-Go \
  -p "${HOST_REST_PORT}:8079" \
  -p "${HOST_GRPC_PORT}:50051" \
  -e PRIVACY_REST_HOST="0.0.0.0" \
  -e PRIVACY_GRPC_HOST="0.0.0.0" \
  -e PRIVACY_LOG_LEVEL="${PRIVACY_LOG_LEVEL:-INFO}" \
  "$IMAGE_NAME"

echo ""
echo "✅ PrivShield Go Engine (Docker) 已成功启动！"
echo "   - REST API : http://127.0.0.1:${HOST_REST_PORT}"
echo "   - gRPC RPC : 127.0.0.1:${HOST_GRPC_PORT}"
echo "   - 查看日志 : docker logs -f PrivShield-Go"
echo "   - 停止容器 : bash scripts/dev/docker-stop-go-agent.sh"
echo "============================================================================"
