#!/usr/bin/env bash
# ============================================================================
# Service Hub Health Check Script
# 数据服务调度中枢运行状态健康探针脚本
#
# 探测端点与检测目标：
#   1. /health:       服务本体存活状态与上游 Python Agent 连通性探针；
#   2. /v1/hub/status:   调度中枢排队、活跃、成功与失败任务计数及运行时间；
#   3. /v1/hub/pipeline: 6 阶段流水线活跃状态与 Agent 依赖可用性检查。
#
# 环境变量配置：
#   SERVICE_HUB_HOST: 调度中枢主机（默认 127.0.0.1）
#   SERVICE_HUB_PORT: 调度中枢端口（默认 8082）
#
# 使用方法：
#   bash ./scripts/health-check.sh
# ============================================================================

set -euo pipefail

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "选项 / Options:"
            echo "  -h, --help    显示帮助信息并退出"
            echo ""
            echo "环境变量 / Env vars:"
            echo "  SERVICE_HUB_HOST   主机地址 (默认: 127.0.0.1)"
            echo "  SERVICE_HUB_PORT   端口 (默认: 8082)"
            exit 0
            ;;
        *)
            shift
            ;;
    esac
done

HOST="${SERVICE_HUB_HOST:-127.0.0.1}"
PORT="${SERVICE_HUB_PORT:-8082}"
BASE_URL="http://${HOST}:${PORT}"

echo "=== Service Hub Health Check ==="
echo ""

# ── 1. 基础健康检查 (/health) ─────────────────────────────────────────────
# 验证 service-hub 后端自身及与上游 Agent 的网络连通性
echo -n "Health (/health): "
if resp=$(curl -sf --max-time 5 "${BASE_URL}/health" 2>/dev/null); then
    echo "OK"
    echo "  $resp" | python3 -m json.tool 2>/dev/null || echo "  $resp"
else
    echo "FAILED (unreachable)"
fi

echo ""

# ── 2. 调度中枢运行态指标 (/v1/hub/status) ──────────────────────────────────
# 验证任务队列深度与历史执行统计
echo -n "Hub Status (/v1/hub/status): "
if resp=$(curl -sf --max-time 5 "${BASE_URL}/v1/hub/status" 2>/dev/null); then
    echo "OK"
    echo "  $resp" | python3 -m json.tool 2>/dev/null || echo "  $resp"
else
    echo "FAILED"
fi

echo ""

# ── 3. 流水线阶段状态 (/v1/hub/pipeline) ────────────────────────────────────
# 验证流水线 6 个阶段（ingest/fetch/classify/desensitize/return/audit）的处理状态
echo -n "Pipeline (/v1/hub/pipeline): "
if resp=$(curl -sf --max-time 5 "${BASE_URL}/v1/hub/pipeline" 2>/dev/null); then
    echo "OK"
    echo "  $resp" | python3 -m json.tool 2>/dev/null || echo "  $resp"
else
    echo "FAILED"
fi

