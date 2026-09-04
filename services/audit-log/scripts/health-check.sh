#!/usr/bin/env bash
# ============================================================================
# Audit Log Health Check Script
# 脱敏审计日志健康检查脚本
# ============================================================================

set -euo pipefail

HOST="${AUDIT_LOG_HOST:-127.0.0.1}"
PORT="${AUDIT_LOG_PORT:-8084}"
GRPC_PORT="${AUDIT_LOG_GRPC_PORT:-50054}"
BASE_URL="http://${HOST}:${PORT}"

echo "=== Audit Log Health Check ==="
echo ""

# 1. HTTP Health endpoint
echo -n "REST Health (/health): "
if resp=$(curl -sf --max-time 5 "${BASE_URL}/health" 2>/dev/null); then
    echo "OK"
    echo "  $resp" | python3 -m json.tool 2>/dev/null || echo "  $resp"
else
    echo "FAILED (unreachable)"
fi

echo ""

# 2. gRPC Health check (if grpcurl is available)
echo -n "gRPC Health (:50054): "
if command -v grpcurl >/dev/null 2>&1; then
    if grpcurl -plaintext -max-time 5 "${HOST}:${GRPC_PORT}" auditlog.AuditLogService/Health >/dev/null 2>&1; then
        echo "OK"
    else
        echo "FAILED (unreachable)"
    fi
else
    echo "SKIPPED (grpcurl not installed)"
fi

echo ""

# 3. Audit stats
echo -n "Stats (/v1/audit/stats): "
if resp=$(curl -sf --max-time 5 "${BASE_URL}/v1/audit/stats" 2>/dev/null); then
    echo "OK"
    echo "  $resp" | python3 -m json.tool 2>/dev/null || echo "  $resp"
else
    echo "FAILED"
fi

echo ""

# 4. Snapshot count
echo -n "Snapshots (/v1/audit/snapshots): "
if resp=$(curl -sf --max-time 5 "${BASE_URL}/v1/audit/snapshots?limit=1" 2>/dev/null); then
    echo "OK"
else
    echo "FAILED"
fi
