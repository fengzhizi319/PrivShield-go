#!/usr/bin/env bash
# ==============================================================================
# 脚本名称: benchmark_performance.sh
# 脚本说明: PrivShield 隐私原语与分类漏斗性能基准测试与吞吐压测工具。
#
# 执行步骤总览：
#   1. 解析命令行参数（--host、--port、--requests、--concurrency）
#   2. 初始化 Python 多线程并发 HTTP 压测执行引擎
#   3. 对脱敏原语（/v1/privacy/mask）执行高并发吞吐压测并统计 P50/P95/P99 时延
#   4. 对差分隐私原语（/v1/privacy/dp/count）执行加噪性能压测
#   5. 对动态分类分级漏斗（/v1/dynclassification/classify）执行端到端压测
#   6. 汇总结算 RPS (Requests Per Second) 与延迟百分位数分布
#
# 用法 / Usage:
#   ./scripts/dev/benchmark_performance.sh [选项]
# ==============================================================================

set -euo pipefail

# ANSI 终端颜色代码
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# ── 步骤 1：定位默认参数与并发配置 ────────────────────────────────────────
REST_HOST="${PRIVACY_REST_HOST:-127.0.0.1}"
REST_PORT="${PRIVACY_REST_PORT:-8079}"
NUM_REQUESTS=200
CONCURRENCY=10

# ── 步骤 2：帮助说明与命令行解析 ──────────────────────────────────────────
usage() {
    cat <<EOF
使用说明: $(basename "$0") [选项]

选项:
  --host HOST          REST 服务主机 (默认: 127.0.0.1 或 PRIVACY_REST_HOST)
  --port PORT          REST 服务端口 (默认: 8079 或 PRIVACY_REST_PORT)
  -n, --requests NUM   基准测试请求总数 (默认: 200)
  -c, --concurrency C  并发线程数 (默认: 10)
  -h, --help           显示帮助信息并退出

使用示例:
  ./scripts/dev/benchmark_performance.sh
  ./scripts/dev/benchmark_performance.sh -n 500 -c 20
EOF
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --host)
            REST_HOST="$2"
            shift 2
            ;;
        --port)
            REST_PORT="$2"
            shift 2
            ;;
        -n|--requests)
            NUM_REQUESTS="$2"
            shift 2
            ;;
        -c|--concurrency)
            CONCURRENCY="$2"
            shift 2
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo -e "${RED}错误: 未知选项 $1${NC}"
            usage
            ;;
    esac
done

BASE_URL="http://${REST_HOST}:${REST_PORT}"

echo -e "${BLUE}====================================================${NC}"
echo -e "${BLUE} PrivShield 性能基准测试与吞吐压测${NC}"
echo -e "${BLUE} 目标服务器 : ${BASE_URL}${NC}"
echo -e "${BLUE} 总请求量   : ${NUM_REQUESTS} 次${NC}"
echo -e "${BLUE} 并发线程数 : ${CONCURRENCY} 线程${NC}"
echo -e "${BLUE}====================================================${NC}"

# 使用内嵌 Python 脚本并发发送 HTTP 请求，精准统计时延分布与 RPS
python3 -c "
import concurrent.futures
import json
import sys
import time
import urllib.request

base_url = '${BASE_URL}'
num_requests = ${NUM_REQUESTS}
concurrency = ${CONCURRENCY}

def send_request(endpoint, payload):
    url = f'{base_url}{endpoint}'
    data = json.dumps(payload).encode('utf-8')
    req = urllib.request.Request(url, data=data, headers={'Content-Type': 'application/json'})
    
    start_t = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            _ = resp.read()
            elapsed_ms = (time.perf_counter() - start_t) * 1000
            return elapsed_ms, resp.status == 200
    except Exception as e:
        elapsed_ms = (time.perf_counter() - start_t) * 1000
        return elapsed_ms, False

def run_bench(name, endpoint, payload):
    print(f'\n[*] 开始测试 [{name}] 接口...')
    latencies = []
    successes = 0
    
    start_total = time.perf_counter()
    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as executor:
        futures = [executor.submit(send_request, endpoint, payload) for _ in range(num_requests)]
        for f in concurrent.futures.as_completed(futures):
            elapsed_ms, ok = f.result()
            latencies.append(elapsed_ms)
            if ok:
                successes += 1
                
    total_time_sec = time.perf_counter() - start_total
    rps = num_requests / total_time_sec if total_time_sec > 0 else 0
    
    latencies.sort()
    p50 = latencies[int(len(latencies) * 0.50)] if latencies else 0
    p95 = latencies[int(len(latencies) * 0.95)] if latencies else 0
    p99 = latencies[int(len(latencies) * 0.99)] if latencies else 0
    avg = sum(latencies) / len(latencies) if latencies else 0

    print(f'---- [{name}] 性能测算报告 ----')
    print(f'成功率       : {successes}/{num_requests} ({successes/num_requests*100:.1f}%)')
    print(f'吞吐率 (RPS) : {rps:.2f} req/sec')
    print(f'平均响应时间 : {avg:.2f} ms')
    print(f'P50 响应延迟 : {p50:.2f} ms')
    print(f'P95 响应延迟 : {p95:.2f} ms')
    print(f'P99 响应延迟 : {p99:.2f} ms')

# 1. 字段脱敏 Masking 接口测试
mask_payload = {
    'field': 'phone',
    'value': '13800138000',
    'type': 'phone'
}
run_bench('数据脱敏 Masking', '/v1/privacy/mask', mask_payload)

# 2. 差分隐私 DP 接口测试 (先自动重置预算确保压测基准不受历史消耗干扰)
try:
    req_reset = urllib.request.Request(f'{base_url}/v1/privacy/budget/reset', data=b'{}', headers={'Content-Type': 'application/json'})
    with urllib.request.urlopen(req_reset, timeout=5) as r:
        pass
except Exception:
    pass

dp_payload = {
    'count': 100,
    'epsilon': 0.001
}
run_bench('差分隐私 DP Count', '/v1/privacy/dp/count', dp_payload)

# 3. 动态分类分级接口测试
class_payload = {
    'field': 'id_card',
    'value': '110101199003072381'
}
run_bench('动态分类分级三层漏斗', '/v1/dynclassification/classify', class_payload)
"

echo -e "\n${GREEN}====================================================${NC}"
echo -e "${GREEN} 性能基准测试完成！${NC}"
echo -e "${GREEN}====================================================${NC}"
