#!/usr/bin/env bash
# ==============================================================================
# Service Hub - 流水线任务模拟与并发流量生成脚本
# Simulate pipeline task dispatch to generate metrics for Grafana dashboard
#
# 功能说明：
#   本脚本用于向 service-hub 调度中枢注入并发任务流，模拟真实生产环境下多批次、
#   多数据源的分类分级、任务分发与状态查询流量，为 Prometheus 指标采集与 Grafana 监控大屏提供数据源。
#
# 模拟执行逻辑：
#   1. 健康检查：探测目标 service-hub 是否正常运行，若未运行则提示启动命令并退出；
#   2. 循环分发任务（默认 20 批）：
#      - POST /v1/hub/classify: 提交包含患者医保敏感字段的自动分类与自适应脱敏请求；
#      - POST /v1/hub/dispatch: 穿插（每 3 批）提交普通脱敏任务；
#      - GET /v1/hub/status & GET /v1/hub/tasks: 穿插（每 5 批）执行状态与任务列表查询；
#   3. 终端打印执行进度指示符（绿色 ✓ 代表成功，红色 x 代表异常）；
#   4. 输出汇总与 Grafana 仪表盘访问指引。
#
# 参数与环境变量：
#   $1: 任务批次数（默认 20）
#   SERVICE_HUB_URL: 调度中枢目标地址（默认 http://127.0.0.1:8082）
#
# 使用方法：
#   bash ./scripts/simulate-pipeline.sh [count]
# ==============================================================================
set -e

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    echo "用法 / Usage: $0 [任务批次数 / count]"
    echo ""
    echo "选项 / Options:"
    echo "  -h, --help    显示帮助信息并退出"
    echo ""
    echo "环境变量 / Env vars:"
    echo "  SERVICE_HUB_URL   调度中枢目标地址 (默认: http://127.0.0.1:8082)"
    exit 0
fi

# 终端输出 ANSI 颜色配置
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

HUB_URL="${SERVICE_HUB_URL:-http://127.0.0.1:8082}"
COUNT="${1:-20}"

echo "========================================================"
echo " 🚀 Service Hub 流水线调度模拟器"
echo " 目标地址: $HUB_URL"
echo " 模拟批次: $COUNT 个任务"
echo "========================================================"

# ── 1. 检查目标服务健康状态 ──────────────────────────────────────────────────
if ! curl -s -f "$HUB_URL/health" > /dev/null; then
  echo "❌ 错误: Service Hub 未在 $HUB_URL 运行，请先启动服务！"
  echo "💡 提示: bash ./scripts/dev/dev-start-services.sh 或 cd services/service-hub && bash run.sh"
  exit 1
fi

echo -e "${BLUE}[*] 开始向调度中枢注入并发任务流...${NC}"

# ── 2. 循环注入任务流量 ──────────────────────────────────────────────────────
for ((i=1; i<=COUNT; i++)); do
  # 2a. 构造包含医疗与身份敏感信息的 JSON 载荷
  PAYLOAD="{\"source\":\"dept_hospital_test\",\"operation\":\"auto_desensitize\",\"priority\":$((i%3+1)),\"payload\":{\"patient_id\":\"P00$i\",\"name\":\"张测试$i\",\"id_card\":\"51010419900101123$((i%10))\",\"diagnosis\":\"急性胃肠炎\",\"medical_fee\":$((100+i*20))}}"
  
  # 提交分类分级与自动脱敏任务 (/v1/hub/classify)
  HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$HUB_URL/v1/hub/classify" \
    -H "Content-Type: application/json" \
    -d "$PAYLOAD" || echo "000")
  
  if [ "$HTTP_CODE" = "200" ]; then
    printf "${GREEN}✓${NC}"
  else
    printf "x"
  fi
  
  # 2b. 穿插提交直接脱敏任务 (/v1/hub/dispatch)
  if (( i % 3 == 0 )); then
    curl -s -o /dev/null -X POST "$HUB_URL/v1/hub/dispatch" \
      -H "Content-Type: application/json" \
      -d "{\"source\":\"yibao_settlement\",\"operation\":\"mask_id\",\"priority\":2,\"payload\":{\"record_id\":\"REC$i\"}}" || true
  fi
  
  # 2c. 穿插查询中枢状态与任务明细 (/v1/hub/status & /v1/hub/tasks)
  if (( i % 5 == 0 )); then
    curl -s -o /dev/null "$HUB_URL/v1/hub/status" || true
    curl -s -o /dev/null "$HUB_URL/v1/hub/tasks?limit=10" || true
  fi
  
  sleep 0.1
done

echo ""
echo "--------------------------------------------------------"
echo -e "${GREEN}✅ 模拟任务注入完成！${NC}"
echo "📊 请前往 Grafana (http://localhost:3000) 查看 Service Hub 调度监控大屏！"
echo "========================================================"

