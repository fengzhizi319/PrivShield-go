#!/usr/bin/env bash
# ============================================================================
# Audit Log (脱敏审计日志) Production Deployment Script
# 脱敏审计日志生产部署脚本
# ============================================================================

set -euo pipefail

# Dockerfile 要求构建上下文为项目根目录（包含 pkg/ 与 services/）
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODULE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PROJECT_ROOT="$(cd "$MODULE_DIR/../.." && pwd)"

IMAGE_NAME="${AUDIT_LOG_IMAGE:-privshield-audit-log:1.8.0}"
CONTAINER_NAME="${AUDIT_LOG_CONTAINER:-privshield-audit-log}"
HOST="${AUDIT_LOG_HOST:-0.0.0.0}"
PORT="${AUDIT_LOG_PORT:-8084}"
GRPC_PORT="${AUDIT_LOG_GRPC_PORT:-50054}"
# P63 fix: SQLite data directory for persistent storage (default: named volume)
DATA_DIR="${AUDIT_LOG_DATA_DIR:-${CONTAINER_NAME}-data}"

echo "=========================================="
echo "  Deploy Audit Log (脱敏审计日志与存证)"
echo "=========================================="

# Build image (build context = PROJECT_ROOT for shared pkg/ dependency)
echo "[1/3] Building Docker image: $IMAGE_NAME ..."
docker build -f "$MODULE_DIR/Dockerfile" -t "$IMAGE_NAME" "$PROJECT_ROOT"

# Stop old container
echo "[2/3] Removing old container (if exists)..."
docker rm -f "$CONTAINER_NAME" 2>/dev/null || true

# Run new container
# P63 fix: mount data volume for SQLite persistence
# P64 fix: add post-deploy health check verification
echo "[3/3] Starting container on REST port $PORT, gRPC port $GRPC_PORT ..."
docker run -d \
  --name "$CONTAINER_NAME" \
  -p "${PORT}:8084" \
  -p "${GRPC_PORT}:50054" \
  -v "${DATA_DIR}:/app/data" \
  -e AUDIT_LOG_HOST="$HOST" \
  -e AUDIT_LOG_PORT=8084 \
  -e AUDIT_LOG_GRPC_HOST="$HOST" \
  -e AUDIT_LOG_GRPC_PORT=50054 \
  -e PRIVACY_AGENT_REST_HOST="${PRIVACY_AGENT_REST_HOST:-privshield-agent}" \
  -e PRIVACY_REST_PORT="${PRIVACY_REST_PORT:-8079}" \
  -e PRIVACY_AGENT_API_KEY="${PRIVACY_AGENT_API_KEY:-}" \
  -e AUDIT_LOG_MAX_ENTRIES="${AUDIT_LOG_MAX_ENTRIES:-100000}" \
  -e AUDIT_LOG_DB_PATH="${AUDIT_LOG_DB_PATH:-/app/data/audit-log.db}" \
  --restart unless-stopped \
  "$IMAGE_NAME"

# P64 fix: wait for container to become healthy
echo -n "Waiting for audit-log to be healthy"
for i in $(seq 1 30); do
  if curl -sf --max-time 3 "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
    echo " OK"
    echo ""
    echo "Audit Log deployed successfully!"
    echo "  REST Health: http://127.0.0.1:${PORT}/health"
    echo "  gRPC:        127.0.0.1:${GRPC_PORT}"
    echo "  Logs:        http://127.0.0.1:${PORT}/v1/audit/logs"
    echo "  Stats:       http://127.0.0.1:${PORT}/v1/audit/stats"
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
