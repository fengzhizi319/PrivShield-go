#!/usr/bin/env bash
# ============================================================================
# Datasource Manager (数据源管理) 生产 Docker 容器自动化部署脚本
#
# 描述：
#   本脚本用于在单机或测试服务器上通过 Docker 独立构建并运行 datasource-mgr 容器。
#   支持配置挂载数据持久化卷、设置网络映射与环境变量，并在容器启动后自动执行
#   最长 30 秒的健康检查探针轮询，保障服务就绪后再返回成功状态。
#
# 用法 (Usage)：
#   bash scripts/deploy.sh
#
# 环境变量配置选项 (Optional Env Vars)：
#   DATASOURCE_MGR_IMAGE      : Docker 镜像名 (默认 privshield-datasource-mgr:1.8.0)
#   DATASOURCE_MGR_CONTAINER  : 容器名 (默认 privshield-datasource-mgr)
#   DATASOURCE_MGR_PORT       : 宿主机对外暴露的 HTTP 端口 (默认 8083)
#   DATASOURCE_MGR_GRPC_PORT  : 宿主机对外暴露的 gRPC 端口 (默认 50053)
#   DATASOURCE_MGR_DATA_DIR   : 数据持久化命名卷/路径
# ============================================================================

set -euo pipefail

# 1. 解析路径：Dockerfile 要求构建上下文为项目根目录（包含共享基础库 pkg/ 与微服务 services/）
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODULE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PROJECT_ROOT="$(cd "$MODULE_DIR/../.." && pwd)"

# 2. 从环境变量读取部署参数或使用默认值
IMAGE_NAME="${DATASOURCE_MGR_IMAGE:-privshield-datasource-mgr:1.8.0}"
CONTAINER_NAME="${DATASOURCE_MGR_CONTAINER:-privshield-datasource-mgr}"
HOST="${DATASOURCE_MGR_HOST:-0.0.0.0}"
PORT="${DATASOURCE_MGR_PORT:-8083}"
GRPC_PORT="${DATASOURCE_MGR_GRPC_PORT:-50053}"
# 持久化存储命名卷（默认 privshield-datasource-mgr-data）
DATA_DIR="${DATASOURCE_MGR_DATA_DIR:-${CONTAINER_NAME}-data}"

echo "=========================================="
echo "  Deploy Datasource Manager (数据源管理)"
echo "=========================================="

# 3. 构建 Docker 镜像
# 构建上下文设置为 PROJECT_ROOT，使得 Dockerfile 内的 Go 模块能够顺利拉取本地 pkg/ 共享基础依赖
echo "[1/3] Building Docker image: $IMAGE_NAME ..."
docker build -f "$MODULE_DIR/Dockerfile" -t "$IMAGE_NAME" "$PROJECT_ROOT"

# 4. 停止并移除同名的历史旧容器（若存在）
echo "[2/3] Removing old container (if exists)..."
docker rm -f "$CONTAINER_NAME" 2>/dev/null || true

# 5. 启动新容器实例
# -p "${PORT}:8083": 宿主机端口映射至容器内 8083 端口；
# -v "${DATA_DIR}:/app/data": 挂载持久化数据目录；
# --restart unless-stopped: 容器崩溃或系统重启后自动恢复。
echo "[3/3] Starting container on REST port $PORT, gRPC port $GRPC_PORT ..."
docker run -d \
  --name "$CONTAINER_NAME" \
  -p "${PORT}:8083" \
  -p "${GRPC_PORT}:50053" \
  -v "${DATA_DIR}:/app/data" \
  -e DATASOURCE_MGR_HOST="$HOST" \
  -e DATASOURCE_MGR_PORT=8083 \
  -e DATASOURCE_MGR_GRPC_HOST="$HOST" \
  -e DATASOURCE_MGR_GRPC_PORT=50053 \
  -e PRIVACY_AGENT_REST_HOST="${PRIVACY_AGENT_REST_HOST:-privshield-agent}" \
  -e PRIVACY_REST_PORT="${PRIVACY_REST_PORT:-8079}" \
  -e PRIVACY_AGENT_API_KEY="${PRIVACY_AGENT_API_KEY:-}" \
  -e DATASOURCE_MGR_DB_PATH="${DATASOURCE_MGR_DB_PATH:-/app/data/datasource-mgr.db}" \
  --restart unless-stopped \
  "$IMAGE_NAME"

# 6. 执行启动后健康检查验证（轮询最长 30 秒）
echo -n "Waiting for datasource-mgr to be healthy"
for i in $(seq 1 30); do
  if curl -sf --max-time 3 "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
    echo " OK"
    echo ""
    echo "Datasource Manager deployed successfully!"
    echo "  REST Health: http://127.0.0.1:${PORT}/health"
    echo "  List:        http://127.0.0.1:${PORT}/v1/datasources"
    echo "  gRPC:        127.0.0.1:${GRPC_PORT}"
    echo "  Data:        ${DATA_DIR} → /app/data (SQLite persistent)"
    exit 0
  fi
  echo -n "."
  sleep 1
done

# 若 30 秒超时仍未响应 200，则输出告警并以退出码 1 退出
echo " TIMEOUT"
echo "WARNING: container started but health check did not respond within 30s"
echo "  Logs: docker logs $CONTAINER_NAME"
exit 1
