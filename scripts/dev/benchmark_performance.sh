#!/usr/bin/env bash
# ==============================================================================
# 脚本名称: benchmark_performance.sh
# 脚本说明: PrivShield Privacy Engine 隐私原语与分类分级 HTTP 吞吐量与时延基准压测工具。
#
# 执行步骤总览：
#   1. 解析命令行参数（--host、--port、--requests、--concurrency）
#   2. 对脱敏原语（/v1/privacy/mask/record）执行高并发吞吐压测并统计时延
#   3. 对差分隐私原语（/v1/privacy/dp/count, /v1/privacy/dp/mean）执行加噪性能压测
#   4. 对 K-匿名原语（/v1/privacy/k_anonymize/table）执行泛化性能压测
#   5. 对 LDP 本地差分隐私（/v1/privacy/ldp/perturb/binary）执行扰动压测
#   6. 对查询混淆（/v1/privacy/qol/obfuscate）执行置乱压测
#   7. 对动态分类分级漏斗（/v1/dynclassification/classify）执行端到端压测
#   8. 汇总结算 RPS (Requests Per Second) 与延迟百分位数分布 (P50/P95/P99)
#
# 用法 / Usage:
#   ./scripts/dev/benchmark_performance.sh [选项]
# ==============================================================================

set -euo pipefail
export NO_PROXY="*"
export no_proxy="*"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

REST_HOST="127.0.0.1"
REST_PORT="8079"
NUM_REQUESTS=100
CONCURRENCY=10

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
        --requests)
            NUM_REQUESTS="$2"
            shift 2
            ;;
        --concurrency)
            CONCURRENCY="$2"
            shift 2
            ;;
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "选项 / Options:"
            echo "  --host <IP>          目标 Agent 主机 (默认: 127.0.0.1)"
            echo "  --port <PORT>        目标 Agent 端口 (默认: 8079)"
            echo "  --requests <N>       每项测试总请求数 (默认: 100)"
            echo "  --concurrency <N>    并发请求数 (默认: 10)"
            echo "  -h, --help           显示此帮助信息"
            exit 0
            ;;
        *)
            echo "未知参数: $1"
            exit 1
            ;;
    esac
done

BASE_URL="http://${REST_HOST}:${REST_PORT}"

echo -e "${BLUE}====================================================${NC}"
echo -e "${BLUE} PrivShield Privacy Engine 性能基准测试${NC}"
echo -e "${BLUE} 目标地址  : ${BASE_URL}${NC}"
echo -e "${BLUE} 请求总数  : ${NUM_REQUESTS}${NC}"
echo -e "${BLUE} 并发线程  : ${CONCURRENCY}${NC}"
echo -e "${BLUE}====================================================${NC}"

run_bench() {
    local desc="$1"
    local endpoint="$2"
    local body="$3"

    echo -e "\n${YELLOW}[BENCH] ${desc}${NC}"
    echo -e "  端点: POST ${endpoint}"

    local tmp_dir
    tmp_dir=$(mktemp -d)
    local start_time end_time total_time

    start_time=$(date +%s%N)

    for (( i=0; i<NUM_REQUESTS; i++ )); do
        (
            local req_start req_end
            req_start=$(date +%s%N)
            curl --noproxy "*" -sf -o /dev/null -X POST \
                -H "Content-Type: application/json" \
                -d "$body" \
                --max-time 10 \
                "${BASE_URL}${endpoint}" 2>/dev/null
            req_end=$(date +%s%N)
            echo $(( (req_end - req_start) / 1000000 )) > "${tmp_dir}/latency_${i}.ms"
        ) &
        if (( (i + 1) % CONCURRENCY == 0 )); then
            wait
        fi
    done
    wait

    end_time=$(date +%s%N)
    total_time=$(( (end_time - start_time) / 1000000 ))

    local latencies
    latencies=$(cat "${tmp_dir}"/latency_*.ms 2>/dev/null | sort -n)
    local count
    count=$(echo "$latencies" | wc -l | tr -d ' ')

    if [ "$count" -gt 0 ] && [ -n "$latencies" ]; then
        local p50_idx=$(( count * 50 / 100 ))
        local p95_idx=$(( count * 95 / 100 ))
        local p99_idx=$(( count * 99 / 100 ))
        [ "$p50_idx" -eq 0 ] && p50_idx=1
        [ "$p95_idx" -eq 0 ] && p95_idx=1
        [ "$p99_idx" -eq 0 ] && p99_idx=1

        local p50 p95 p99
        p50=$(echo "$latencies" | sed -n "${p50_idx}p")
        p95=$(echo "$latencies" | sed -n "${p95_idx}p")
        p99=$(echo "$latencies" | sed -n "${p99_idx}p")

        local sum=0
        for lat in $latencies; do
            sum=$(( sum + lat ))
        done
        local avg=$(( sum / count ))

        local rps=0
        if [ "$total_time" -gt 0 ]; then
            rps=$(( count * 1000 / total_time ))
        fi

        echo -e "  ${GREEN}成功请求: ${count}/${NUM_REQUESTS}${NC}"
        echo -e "  ${GREEN}总耗时   : ${total_time} ms${NC}"
        echo -e "  ${GREEN}RPS      : ${rps} req/s${NC}"
        echo -e "  ${CYAN}延迟 P50 : ${p50} ms${NC}"
        echo -e "  ${CYAN}延迟 P95 : ${p95} ms${NC}"
        echo -e "  ${CYAN}延迟 P99 : ${p99} ms${NC}"
        echo -e "  ${CYAN}平均延迟 : ${avg} ms${NC}"
    else
        echo -e "  ${RED}无成功请求${NC}"
    fi

    rm -rf "$tmp_dir"
}

# ── 1. Masking 脱敏基准 ────────────────────────────────────────────────
run_bench "Masking 脱敏 (MaskRecord)" "/v1/privacy/mask/record" \
    '{"record": {"name": "张三", "phone": "13800138000", "email": "test@example.com", "id_card": "110101199003072345"}}'

# ── 2. Differential Privacy 基准 ───────────────────────────────────────
run_bench "DP Count (Laplace 加噪)" "/v1/privacy/dp/count" \
    '{"value": 100, "epsilon": 0.5, "delta": 0.00001}'

run_bench "DP Mean (Laplace 均值)" "/v1/privacy/dp/mean" \
    '{"values": [1.0, 2.0, 3.0, 4.0, 5.0], "epsilon": 0.5, "lower": 0.0, "upper": 10.0}'

# ── 3. K-Anonymity 基准 ───────────────────────────────────────────────
run_bench "K-Anonymity 表泛化" "/v1/privacy/k_anonymize/table" \
    '{"rows": [{"age": 25, "zip": "100000"}, {"age": 26, "zip": "100000"}], "k": 2, "quasi_identifiers": ["age", "zip"]}'

# ── 4. LDP 基准 ───────────────────────────────────────────────────────
run_bench "LDP Binary 扰动" "/v1/privacy/ldp/perturb/binary" \
    '{"value": 1, "epsilon": 1.0}'

# ── 5. Query Obfuscation 基准 ─────────────────────────────────────────
run_bench "Query Obfuscation 混淆" "/v1/privacy/qol/obfuscate" \
    '{"queries": ["SELECT * FROM patients WHERE name = '\''张三'\''"], "dummy_count": 3}'

# ── 6. 动态分类分级基准 ───────────────────────────────────────────────
run_bench "动态分类分级 (Layer-1 Rule + ONNX)" "/v1/dynclassification/classify" \
    '{"texts": ["身份证号: 110101199003072345, 手机: 13800138000"], "domain": "finance"}'

echo ""
echo -e "${BLUE}====================================================${NC}"
echo -e "${BLUE} Privacy Engine 性能基准测试完成${NC}"
echo -e "${BLUE}====================================================${NC}"
