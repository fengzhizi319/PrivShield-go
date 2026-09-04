#!/usr/bin/env bash
# ==============================================================================
# PrivShield - Go 原生引擎全平台 Prometheus 指标端点一键巡检脚本
# Check Prometheus /metrics endpoints across all Go services & Gateway
# ==============================================================================
set -euo pipefail
export NO_PROXY="*"
export no_proxy="*"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo "========================================================"
echo " 📊 PrivShield-Go 全平台 Prometheus 指标端点巡检"
echo "========================================================"

TARGETS=(
  "PrivShield-Go Agent|http://127.0.0.1:8079/metrics"
  "PrivShield-Go Gateway|http://127.0.0.1:8000/metrics"
  "Console Go BFF Gateway|http://127.0.0.1:8081/metrics"
  "Service Hub 调度中枢|http://127.0.0.1:8082/metrics"
  "Datasource Manager 数据源|http://127.0.0.1:8083/metrics"
  "Audit Log 审计存证|http://127.0.0.1:8084/metrics"
  "App-LZ Go BFF|http://127.0.0.1:8085/metrics"
)

SUCCESS_COUNT=0
TOTAL_COUNT=${#TARGETS[@]}

for item in "${TARGETS[@]}"; do
  NAME="${item%%|*}"
  URL="${item##*|}"
  
  printf "%-28s %-36s " "$NAME" "$URL"
  
  if HTTP_CODE=$(curl -s -o /tmp/metric_out.txt -w "%{http_code}" --max-time 2 "$URL" 2>/dev/null); then
    if [ "$HTTP_CODE" = "200" ]; then
      LINE_COUNT=$(wc -l < /tmp/metric_out.txt | tr -d ' ')
      echo -e "${GREEN}[OK 200]${NC} (${LINE_COUNT} lines of metrics)"
      SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
    else
      echo -e "${YELLOW}[HTTP ${HTTP_CODE}]${NC}"
    fi
  else
    echo -e "${RED}[DOWN / UNREACHABLE]${NC}"
  fi
done

rm -f /tmp/metric_out.txt

echo "--------------------------------------------------------"
if [ "$SUCCESS_COUNT" -eq "$TOTAL_COUNT" ]; then
  echo -e "${GREEN}✅ 全部 $TOTAL_COUNT 个指标端点均正常运行且可被 Prometheus 抓取！${NC}"
else
  echo -e "${YELLOW}ℹ️  已就绪: $SUCCESS_COUNT / $TOTAL_COUNT (请先启动相关服务或微服务集群)${NC}"
fi
echo "========================================================"
