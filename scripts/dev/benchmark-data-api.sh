#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# benchmark-data-api.sh — 预设数据 API 全链路性能基准压测脚本
#
# 通过 curl 并发请求直接测量 BFF → 微服务群全链路延迟与吞吐量，
# 规避浏览器 JS 主线程阻塞对测量精度的影响。
#
# 测量指标：
#   - 端到端延迟 (P50 / P90 / P95 / P99 / Min / Max / Mean)
#   - 吞吐量 (QPS)
#   - 服务端 5 阶段耗时拆解 (ingest / fetch / classify_desensitize / return / audit)
#   - 成功率 / 429 限流计数 / 失败计数
#
# 用法：
#   bash ./scripts/dev/benchmark-data-api.sh [选项]
#
# 选项：
#   -u, --url <URL>          BFF 地址 (默认: http://127.0.0.1:8085)
#   -a, --api-id <ID>        数据 API ID: 1=医保, 2=康养 (默认: 1)
#   -l, --limit <N>          每次请求返回记录数 (默认: 5)
#   -c, --concurrency <N>    并发数 (默认: 10)
#   -n, --requests <N>       总请求数 (默认: 100)
#   --lean                   启用 lean 模式 (不返回 raw_records/sanitized_data)
#   --warmup <N>             预热请求数 (默认: 5)
#   -h, --help               显示帮助
#
# 示例：
#   # 标准压测：10 并发 × 100 请求
#   bash ./scripts/dev/benchmark-data-api.sh
#
#   # 高并发突发脉冲：50 并发 × 300 请求
#   bash ./scripts/dev/benchmark-data-api.sh -c 50 -n 300
#
#   # 康养场景 + lean 模式
#   bash ./scripts/dev/benchmark-data-api.sh -a 2 --lean
#
#   # 自定义 BFF 地址
#   bash ./scripts/dev/benchmark-data-api.sh -u http://192.168.1.100:8085
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

# ── 默认参数 ──
BFF_URL="http://127.0.0.1:8085"
API_ID=1
LIMIT=5
CONCURRENCY=10
TOTAL_REQUESTS=100
LEAN=false
WARMUP=5

# ── 颜色定义 ──
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# ── 参数解析 ──
while [[ $# -gt 0 ]]; do
  case $1 in
    -u|--url)         BFF_URL="$2"; shift 2 ;;
    -a|--api-id)      API_ID="$2"; shift 2 ;;
    -l|--limit)       LIMIT="$2"; shift 2 ;;
    -c|--concurrency) CONCURRENCY="$2"; shift 2 ;;
    -n|--requests)    TOTAL_REQUESTS="$2"; shift 2 ;;
    --lean)           LEAN=true; shift ;;
    --warmup)         WARMUP="$2"; shift 2 ;;
    -h|--help)
      sed -n '3,/^# ──/p' "$0" | sed 's/^# \?//; /^──/d'
      exit 0
      ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

INVOKE_URL="${BFF_URL}/api/lz/data-api/invoke"
LEAN_FLAG="$LEAN"

# ── 前置检查 ──
check_service() {
  local url="$1"
  local name="$2"
  if ! curl -sf -o /dev/null --connect-timeout 3 "${url}" 2>/dev/null; then
    echo -e "${RED}✗ ${name} 不可达 (${url})${NC}"
    return 1
  fi
  echo -e "${GREEN}✓ ${name} 就绪${NC}"
  return 0
}

echo -e "${BOLD}${CYAN}"
echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║   PrivShield 预设数据 API 全链路性能基准压测                    ║"
echo "╚══════════════════════════════════════════════════════════════════╝"
echo -e "${NC}"

echo -e "${BOLD}── 环境检查 ──${NC}"
check_service "${BFF_URL}/api/lz/topology" "App-LZ BFF" || exit 1
echo ""

# ── 场景名称 ──
if [[ "$API_ID" == "1" ]]; then
  SCENARIO_NAME="医保结算 (18 字段)"
elif [[ "$API_ID" == "2" ]]; then
  SCENARIO_NAME="康养慢病 (27 字段)"
else
  SCENARIO_NAME="API #${API_ID}"
fi

echo -e "${BOLD}── 压测参数 ──${NC}"
echo -e "  场景:         ${GREEN}${SCENARIO_NAME}${NC}"
echo -e "  BFF 地址:     ${BFF_URL}"
echo -e "  API ID:       ${API_ID}"
echo -e "  记录数/请求:   ${LIMIT}"
echo -e "  并发数:       ${BOLD}${CONCURRENCY}${NC}"
echo -e "  总请求数:     ${BOLD}${TOTAL_REQUESTS}${NC}"
echo -e "  Lean 模式:    ${LEAN_FLAG}"
echo -e "  预热请求数:   ${WARMUP}"
echo ""

# ── 预热阶段 ──
if [[ "$WARMUP" -gt 0 ]]; then
  echo -e "${BOLD}── 预热阶段 (${WARMUP} 请求) ──${NC}"
  for i in $(seq 1 "$WARMUP"); do
    curl -sf -o /dev/null "${INVOKE_URL}" \
      -X POST -H "Content-Type: application/json" \
      -d "{\"api_id\":${API_ID},\"limit\":${LIMIT},\"lean\":${LEAN_FLAG}}" \
      --connect-timeout 5 --max-time 30 2>/dev/null || true
  done
  echo -e "${GREEN}✓ 预热完成${NC}"
  echo ""
fi

# ── 临时目录 ──
TMPDIR_BENCH=$(mktemp -d)
trap 'rm -rf "$TMPDIR_BENCH"' EXIT

# ── 压测阶段 ──
echo -e "${BOLD}── 压测阶段 (${CONCURRENCY} 并发 × ${TOTAL_REQUESTS} 请求) ──${NC}"
echo -e "  开始时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""

START_EPOCH=$(date +%s%N)

# 使用 xargs 实现并发控制
seq 1 "$TOTAL_REQUESTS" | xargs -P "$CONCURRENCY" -I{} \
  curl -s -o "${TMPDIR_BENCH}/resp_{}" -w "%{http_code} %{time_total}\n" \
    "${INVOKE_URL}" \
    -X POST -H "Content-Type: application/json" \
    -d "{\"api_id\":${API_ID},\"limit\":${LIMIT},\"lean\":${LEAN_FLAG}}" \
    --connect-timeout 5 --max-time 30 \
    > "${TMPDIR_BENCH}/timing.txt" 2>/dev/null

END_EPOCH=$(date +%s%N)
TOTAL_DURATION_MS=$(( (END_EPOCH - START_EPOCH) / 1000000 ))

echo -e "  结束时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo -e "  总耗时:   ${TOTAL_DURATION_MS} ms"
echo ""

# ── 统计延迟分位数 ──
echo -e "${BOLD}── 延迟分位数 (ms) ──${NC}"

# 提取所有耗时（秒 → 毫秒）
awk '{print $2 * 1000}' "${TMPDIR_BENCH}/timing.txt" | sort -n > "${TMPDIR_BENCH}/latencies.txt"

TOTAL_LINES=$(wc -l < "${TMPDIR_BENCH}/latencies.txt")
if [[ "$TOTAL_LINES" -eq 0 ]]; then
  echo -e "${RED}✗ 无有效请求数据${NC}"
  exit 1
fi

# 分位数计算
calc_percentile() {
  local pct=$1
  local idx=$(( (TOTAL_LINES * pct + 99) / 100 ))
  [[ $idx -lt 1 ]] && idx=1
  [[ $idx -gt $TOTAL_LINES ]] && idx=$TOTAL_LINES
  sed -n "${idx}p" "${TMPDIR_BENCH}/latencies.txt"
}

MIN_MS=$(head -1 "${TMPDIR_BENCH}/latencies.txt")
MAX_MS=$(tail -1 "${TMPDIR_BENCH}/latencies.txt")
P50_MS=$(calc_percentile 50)
P90_MS=$(calc_percentile 90)
P95_MS=$(calc_percentile 95)
P99_MS=$(calc_percentile 99)
MEAN_MS=$(awk '{sum+=$1; n++} END {printf "%.1f", sum/n}' "${TMPDIR_BENCH}/latencies.txt")

# 格式化输出
fmt() { printf "%8.1f" "$1"; }

echo -e "  ┌────────────────────────────────────────┐"
echo -e "  │   Min     P50     P90     P95     P99    Max  │"
echo -e "  ├────────────────────────────────────────┤"
printf "  │ ${GREEN}%6.1f   %6.1f   %6.1f   %6.1f   %6.1f  %6.1f${NC} │\n" "$MIN_MS" "$P50_MS" "$P90_MS" "$P95_MS" "$P99_MS" "$MAX_MS"
echo -e "  │                                        │"
printf "  │   Mean: ${CYAN}%6.1f ms${NC}                        │\n" "$MEAN_MS"
echo -e "  └────────────────────────────────────────┘"
echo ""

# ── 吞吐量 ──
QPS=$(echo "scale=1; $TOTAL_LINES * 1000 / $TOTAL_DURATION_MS" | bc)
echo -e "${BOLD}── 吞吐量 ──${NC}"
echo -e "  QPS:            ${GREEN}${BOLD}${QPS}${NC}"
echo -e "  总请求:         ${TOTAL_LINES}"
echo -e "  总耗时:         ${TOTAL_DURATION_MS} ms"
echo ""

# ── HTTP 状态码统计 ──
echo -e "${BOLD}── HTTP 状态码分布 ──${NC}"
awk '{print $1}' "${TMPDIR_BENCH}/timing.txt" | sort | uniq -c | sort -rn | while read -r count code; do
  case $code in
    200) color="$GREEN" ;;
    429) color="$YELLOW" ;;
    *)   color="$RED" ;;
  esac
  printf "  ${color}%s${NC}  →  %d 次\n" "$code" "$count"
done
echo ""

# ── 服务端 5 阶段耗时拆解 ──
echo -e "${BOLD}── 服务端 5 阶段耗时拆解 (ms) ──${NC}"

# 从成功响应中提取 stage 耗时（使用 Python3 解析 JSON）
STAGE_COUNT=0

for f in "${TMPDIR_BENCH}"/resp_*; do
  [[ -f "$f" ]] || continue
  if python3 -c "import json,sys; d=json.load(open(sys.argv[1])); assert d.get('stages')" "$f" 2>/dev/null; then
    python3 -c "
import json,sys
d=json.load(open(sys.argv[1]))
for s in d.get('stages',[]):
    print(s['name'], s.get('duration_ms',0), s.get('compute_ms',0), s.get('network_ms',0))
" "$f" >> "${TMPDIR_BENCH}/all_stages.txt" 2>/dev/null
    STAGE_COUNT=$((STAGE_COUNT + 1))
  fi
done

if [[ "$STAGE_COUNT" -gt 0 && -f "${TMPDIR_BENCH}/all_stages.txt" ]]; then
  echo -e "  (基于 ${STAGE_COUNT} 个成功响应采样)"
  echo ""
  python3 -c "
import sys

titles = {
    'ingest': '会话请求接入与校验',
    'fetch': '数据源原始数据拉取',
    'classify_desensitize': '三层漏斗评级与脱敏',
    'return': '脱敏结果装配与交付',
    'audit': '不可篡改审计存证',
}
order = ['ingest', 'fetch', 'classify_desensitize', 'return', 'audit']
sums = {}; counts = 0
with open(sys.argv[1]) as f:
    for line in f:
        parts = line.split()
        if len(parts) < 2: continue
        name, dur = parts[0], float(parts[1])
        comp = float(parts[2]) if len(parts) > 2 else 0
        net = float(parts[3]) if len(parts) > 3 else 0
        if name not in sums: sums[name] = [0, 0, 0, 0]
        sums[name][0] += dur; sums[name][1] += comp; sums[name][2] += net; sums[name][3] += 1

total = 0
print('  ┌──────────────────────────────────────────────────────┐')
print('  │  阶段                    平均耗时   计算耗时   通信耗时 │')
print('  ├──────────────────────────────────────────────────────┤')
for k in order:
    if k not in sums: continue
    n = sums[k][3]
    avg = sums[k][0] / n if n > 0 else 0
    cavg = sums[k][1] / n if n > 0 else 0
    navg = sums[k][2] / n if n > 0 else 0
    total += avg
    title = titles.get(k, k)
    print(f'  │  {title:<22s}  {avg:6.1f}    {cavg:6.1f}    {navg:6.1f}  │')
print('  ├──────────────────────────────────────────────────────┤')
print(f'  │  {\"服务端合计\":<22s}  {total:6.1f}                        │')
print('  └──────────────────────────────────────────────────────┘')
" "${TMPDIR_BENCH}/all_stages.txt"
else
  echo -e "  ${YELLOW}⚠ 无法解析服务端阶段耗时${NC}"
fi
echo ""

# ── 开销分析 ──
echo -e "${BOLD}── 开销分析 ──${NC}"
STAGE_TOTAL=$(awk '{sum+=$2; n++} END {if(n>0) printf "%.1f", sum/n; else print "0"}' "${TMPDIR_BENCH}/all_stages.txt" 2>/dev/null || echo "0")
OVERHEAD=$(echo "scale=1; $MEAN_MS - $STAGE_TOTAL" | bc 2>/dev/null || echo "N/A")
if [[ "$STAGE_TOTAL" != "0" && "$OVERHEAD" != "N/A" ]]; then
  OVERHEAD_PCT=$(echo "scale=0; $OVERHEAD * 100 / $MEAN_MS" | bc 2>/dev/null || echo "N/A")
  echo -e "  客户端感知 RTT:   ${CYAN}${MEAN_MS} ms${NC}"
  echo -e "  服务端处理耗时:   ${GREEN}${STAGE_TOTAL} ms${NC}"
  echo -e "  网络 + 序列化开销: ${YELLOW}${OVERHEAD} ms (${OVERHEAD_PCT}%)${NC}"
else
  echo -e "  客户端感知 RTT:   ${CYAN}${MEAN_MS} ms${NC}"
fi
echo ""

# ── SLA 判定 ──
echo -e "${BOLD}── SLA 判定 ──${NC}"
P50_INT=$(printf "%.0f" "$P50_MS")
P99_INT=$(printf "%.0f" "$P99_MS")

if [[ "$P50_INT" -le 100 ]]; then
  echo -e "  P50 < 100ms:  ${GREEN}✓ PASS${NC} (${P50_MS} ms)"
else
  echo -e "  P50 < 100ms:  ${RED}✗ FAIL${NC} (${P50_MS} ms)"
fi

if [[ "$P99_INT" -le 500 ]]; then
  echo -e "  P99 < 500ms:  ${GREEN}✓ PASS${NC} (${P99_MS} ms)"
else
  echo -e "  P99 < 500ms:  ${RED}✗ FAIL${NC} (${P99_MS} ms)"
fi
echo ""

# ── 延迟分布直方图 ──
echo -e "${BOLD}── 延迟分布 ──${NC}"
awk '{
  if ($1 <= 5) bucket["0-5ms"]++
  else if ($1 <= 10) bucket["5-10ms"]++
  else if ($1 <= 20) bucket["10-20ms"]++
  else if ($1 <= 50) bucket["20-50ms"]++
  else if ($1 <= 100) bucket["50-100ms"]++
  else if ($1 <= 200) bucket["100-200ms"]++
  else if ($1 <= 500) bucket["200-500ms"]++
  else bucket["500ms+"]++
}
END {
  order[1]="0-5ms"; order[2]="5-10ms"; order[3]="10-20ms"; order[4]="20-50ms";
  order[5]="50-100ms"; order[6]="100-200ms"; order[7]="200-500ms"; order[8]="500ms+";
  max_count=0;
  for (i=1; i<=8; i++) if (bucket[order[i]] > max_count) max_count = bucket[order[i]];
  for (i=1; i<=8; i++) {
    k = order[i];
    c = bucket[k]+0;
    if (max_count > 0) bar_len = int(c * 40 / max_count);
    else bar_len = 0;
    bar = "";
    for (j=0; j<bar_len; j++) bar = bar "█";
    printf "  %-10s │%s %d\n", k, bar, c;
  }
}' "${TMPDIR_BENCH}/latencies.txt"
echo ""

echo -e "${BOLD}${GREEN}══ 压测完成 ══${NC}"
