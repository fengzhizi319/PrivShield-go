#!/usr/bin/env bash
# PrivShield Go 引擎全栈基准测试脚本
#
# 用法：
#   bash scripts/dev/bench-privacy-engine.sh [--bench-time=1s] [--output=results.txt]
#
# 前提：Go 1.25+ 已安装，services/privacy-engine 模块编译通过。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENGINE_DIR="$PROJECT_ROOT/services/privacy-engine"
SDK_DIR="$PROJECT_ROOT/services/privacy-engine/sdk"

BENCH_TIME="1s"
OUTPUT=""

# 解析参数
for arg in "$@"; do
  case "$arg" in
    --bench-time=*) BENCH_TIME="${arg#*=}" ;;
    --output=*) OUTPUT="${arg#*=}" ;;
    --help|-h)
      echo "Usage: $0 [--bench-time=1s] [--output=results.txt]"
      exit 0
      ;;
  esac
done

echo "══════════════════════════════════════════════════"
echo "  PrivShield Go 引擎全栈基准测试"
echo "  时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo "  Go:   $(go version 2>/dev/null || echo 'N/A')"
echo "  OS:   $(uname -ms)"
echo "  CPU:  $(sysctl -n hw.ncpu 2>/dev/null || nproc 2>/dev/null || echo '?') cores"
echo "  Mem:  $(sysctl -n hw.memsize 2>/dev/null | awk '{printf "%.0f GB\n", $1/1073741824}' || echo '?')"
echo "══════════════════════════════════════════════════"
echo ""

# 临时文件收集输出
TMPFILE=$(mktemp /tmp/go-bench-XXXXXX.txt)
trap 'rm -f "$TMPFILE"' EXIT

run_bench() {
  local dir="$1"
  local pkg="$2"
  local label="$3"

  echo "── $label ──"
  echo ""

  if [ ! -d "$dir" ]; then
    echo "  ⚠️  目录不存在: $dir"
    echo ""
    return
  fi

  cd "$dir"
  # 运行基准测试，-count=3 取中位数，-benchmem 采集内存
  if go test -bench=. -benchtime="$BENCH_TIME" -count=3 -benchmem "$pkg" 2>&1 | tee -a "$TMPFILE"; then
    echo ""
  else
    echo "  ⚠️  基准测试失败或无基准函数: $label"
    echo ""
  fi
}

# ── services/privacy-engine/sdk 基准 ──
echo "▸ services/privacy-engine/sdk 基准测试"
echo ""
run_bench "$SDK_DIR" "./masking" "masking — PII 脱敏原语"
run_bench "$SDK_DIR" "./dp" "dp — 差分隐私原语"
run_bench "$SDK_DIR" "./ldp" "ldp — 本地差分隐私"
run_bench "$SDK_DIR" "./kano" "kano — K-匿名"
run_bench "$SDK_DIR" "./qol" "qol — 查询混淆"
run_bench "$SDK_DIR" "./budget" "budget — 隐私预算"

# ── services/privacy-engine 基准 ──
echo "▸ services/privacy-engine 基准测试"
echo ""
run_bench "$ENGINE_DIR" "./internal/dynclassification" "dynclassification — 规则引擎 + AC 自动机"

# ── 汇总 ──
echo ""
echo "══════════════════════════════════════════════════"
echo "  基准测试完成"
echo "══════════════════════════════════════════════════"

# 提取关键数据行
echo ""
echo "▸ 关键数据汇总:"
echo ""
grep -E "^Benchmark" "$TMPFILE" 2>/dev/null | sort || echo "  (无基准数据)"

# 保存到文件
if [ -n "$OUTPUT" ]; then
  cp "$TMPFILE" "$OUTPUT"
  echo ""
  echo "▸ 结果已保存至: $OUTPUT"
fi
