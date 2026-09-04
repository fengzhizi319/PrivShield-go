#!/usr/bin/env bash
# ============================================================================
# Datasource Manager Health Check Script
# 数据源管理健康检查与连通性探针脚本
#
# 描述：
#   该脚本通过 HTTP curl 命令探测 datasource-mgr 服务的两个核心端点：
#   1. /health: 存活健康探针与服务标识校验；
#   2. /v1/datasources: 数据源资产目录元数据列表获取。
#
# 用法 (Usage)：
#   bash scripts/health-check.sh
#
# 环境变量 (Optional Env Vars)：
#   DATASOURCE_MGR_HOST: 目标服务主机地址 (默认 127.0.0.1)
#   DATASOURCE_MGR_PORT: 目标服务端口 (默认 8083)
# ============================================================================

set -euo pipefail

# 1. 解析目标地址
HOST="${DATASOURCE_MGR_HOST:-127.0.0.1}"
PORT="${DATASOURCE_MGR_PORT:-8083}"
BASE_URL="http://${HOST}:${PORT}"

echo "=== Datasource Manager Health Check ==="
echo "Target: ${BASE_URL}"
echo ""

# 2. 探测基础健康状态端点 (/health)
# 执行逻辑：
# - 发送 GET 请求，最长超时 5 秒；
# - 若请求成功且状态码为 200，则输出 OK 并使用 python3 -m json.tool 格式化输出 JSON；
# - 若失败则输出 FAILED。
echo -n "Health (/health): "
if resp=$(curl -sf --max-time 5 "${BASE_URL}/health" 2>/dev/null); then
    echo "OK"
    echo "  $resp" | python3 -m json.tool 2>/dev/null || echo "  $resp"
else
    echo "FAILED (unreachable)"
fi

echo ""

# 3. 探测数据源资产列表端点 (/v1/datasources)
# 执行逻辑：
# - 发送 GET 请求获取已注册的数据源列表；
# - 若成功则输出 OK 并美化打印返回的数据源列表 JSON；
# - 若失败则输出 FAILED。
echo -n "DataSources (/v1/datasources): "
if resp=$(curl -sf --max-time 5 "${BASE_URL}/v1/datasources" 2>/dev/null); then
    echo "OK"
    echo "  $resp" | python3 -m json.tool 2>/dev/null || echo "  $resp"
else
    echo "FAILED"
fi
