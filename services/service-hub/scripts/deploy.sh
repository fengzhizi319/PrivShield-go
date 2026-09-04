#!/usr/bin/env bash
# ============================================================================
# Service Hub (数据服务调度中枢) Production Deployment Script
# 数据服务调度中枢生产容器化部署脚本
#
# 部署流程与执行逻辑：
#   1. 解析多级目录结构，将项目根目录设为 Docker 构建上下文（以支持 pkg/ 共享依赖编译）；
#   2. 执行 docker build 构建最新生产镜像（默认标签：privshield-service-hub:1.8.0）；
#   3. 检查并安全删除旧容器实例；
#   4. 挂载持久化存储卷（SQLite 数据持久化至 /app/data）并注入生产环境变量启动容器；
#   5. 启动后自动进行最多 30 次（每次 1 秒）的健康检查轮询（/health），确保服务真正就绪。
#
# 支持的环境变量配置：
#   SERVICE_HUB_IMAGE: 镜像名称与标签（默认 privshield-service-hub:1.8.0）
#   SERVICE_HUB_CONTAINER: 容器运行名称（默认 privshield-service-hub）
#   SERVICE_HUB_HOST: 容器内监听的主机地址（默认 0.0.0.0）
#   SERVICE_HUB_PORT: 宿主机对外映射的 HTTP 端口（默认 8082）
#   SERVICE_HUB_GRPC_PORT: 宿主机对外映射的 gRPC 端口（默认 50052）
#   SERVICE_HUB_DATA_DIR: SQLite 数据持久化目录或 Docker 卷名（默认 privshield-service-hub-data）
#   PRIVACY_AGENT_REST_HOST: Python Agent 所在容器名或 IP（默认 privshield-agent）
#   PRIVACY_REST_PORT: Python Agent 端口（默认 8079）
#   SERVICE_HUB_MAX_QUEUE: 最大任务排队深度（默认 1000）
#   SERVICE_HUB_SCHEDULE_TIMEOUT: 流水线调度超时秒数（默认 30）
#   SERVICE_HUB_DB_PATH: SQLite 数据库在容器内的绝对路径（默认 /app/data/service-hub.db）
# ============================================================================

set -euo pipefail

# ── 1. 定位上下文路径 ────────────────────────────────────────────────────────
# Dockerfile 要求构建上下文为项目根目录（包含 pkg/ 与 services/）
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODULE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PROJECT_ROOT="$(cd "$MODULE_DIR/../.." && pwd)"

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "环境变量 / Env vars:"
            echo "  SERVICE_HUB_IMAGE      镜像名称与标签 (默认: privshield-service-hub:1.8.0)"
            echo "  SERVICE_HUB_CONTAINER  容器名称 (默认: privshield-service-hub)"
            echo "  SERVICE_HUB_PORT       REST 端口 (默认: 8082)"
            echo "  SERVICE_HUB_GRPC_PORT  gRPC 端口 (默认: 50052)"
            echo "  -h, --help             显示帮助信息并退出"
            exit 0
            ;;
        *)
            shift
            ;;
    esac
done

# ── 2. 读取部署环境变量与默认值 ──────────────────────────────────────────────
IMAGE_NAME="${SERVICE_HUB_IMAGE:-privshield-service-hub:1.8.0}"
CONTAINER_NAME="${SERVICE_HUB_CONTAINER:-privshield-service-hub}"
HOST="${SERVICE_HUB_HOST:-0.0.0.0}"
PORT="${SERVICE_HUB_PORT:-8082}"
GRPC_PORT="${SERVICE_HUB_GRPC_PORT:-50052}"
# SQLite 数据持久化目录（默认使用 Docker 命名卷）
DATA_DIR="${SERVICE_HUB_DATA_DIR:-${CONTAINER_NAME}-data}"

echo "=========================================="
echo "  Deploy Service Hub (调度中枢)"
echo "=========================================="

# ── 3. 构建 Docker 镜像 ──────────────────────────────────────────────────────
# 构建上下文设置为 PROJECT_ROOT，确保可以成功导入和编译 pkg/ 下的公共模块
echo "[1/3] Building Docker image: $IMAGE_NAME ..."
docker build -f "$MODULE_DIR/Dockerfile" -t "$IMAGE_NAME" "$PROJECT_ROOT"

# ── 4. 清理旧容器 ────────────────────────────────────────────────────────────
echo "[2/3] Removing old container (if exists)..."
docker rm -f "$CONTAINER_NAME" 2>/dev/null || true

# ── 5. 启动新容器 ────────────────────────────────────────────────────────────
# 挂载数据卷保障任务与审计元数据在容器重启后不丢失
echo "[3/3] Starting container on REST port $PORT, gRPC port $GRPC_PORT ..."
docker run -d \
  --name "$CONTAINER_NAME" \
  -p "${PORT}:8082" \
  -p "${GRPC_PORT}:50052" \
  -v "${DATA_DIR}:/app/data" \
  -e SERVICE_HUB_HOST="$HOST" \
  -e SERVICE_HUB_PORT=8082 \
  -e SERVICE_HUB_GRPC_HOST="$HOST" \
  -e SERVICE_HUB_GRPC_PORT=50052 \
  -e PRIVACY_AGENT_REST_HOST="${PRIVACY_AGENT_REST_HOST:-privshield-agent}" \
  -e PRIVACY_REST_PORT="${PRIVACY_REST_PORT:-8079}" \
  -e PRIVACY_AGENT_API_KEY="${PRIVACY_AGENT_API_KEY:-}" \
  -e SERVICE_HUB_MAX_QUEUE="${SERVICE_HUB_MAX_QUEUE:-1000}" \
  -e SERVICE_HUB_SCHEDULE_TIMEOUT="${SERVICE_HUB_SCHEDULE_TIMEOUT:-30}" \
  -e SERVICE_HUB_DB_PATH="${SERVICE_HUB_DB_PATH:-/app/data/service-hub.db}" \
  --restart unless-stopped \
  "$IMAGE_NAME"

# ── 6. 部署后健康巡检 ────────────────────────────────────────────────────────
# 轮询探测 /health 端点，最多等待 30 秒
echo -n "Waiting for service-hub to be healthy"
for i in $(seq 1 30); do
  if curl -sf --max-time 3 "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
    echo " OK"
    echo ""
    echo "Service Hub deployed successfully!"
    echo "  REST Health: http://127.0.0.1:${PORT}/health"
    echo "  Status:      http://127.0.0.1:${PORT}/v1/hub/status"
    echo "  gRPC:        127.0.0.1:${GRPC_PORT}"
    echo "  Data:        ${DATA_DIR} → /app/data (SQLite persistent)"
    exit 0
  fi
  echo -n "."
  sleep 1
done
echo " TIMEOUT"
echo "WARNING: container started but health check did not respond within 30s"
echo "  Logs: docker logs $CONTAINER_NAME"
exit 1
