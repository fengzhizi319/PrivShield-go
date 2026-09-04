#!/usr/bin/env bash
# check_taxonomy_consistency.sh — 数据安全分级词表一致性门禁（整改项 P1-5）。
#
# 词表唯一事实源是 rules/taxonomies/default.yaml。本脚本静态比对其下游四处副本，
# 防止「某处私自加一级 / 改中文名 / 换枚举值」这类只能靠人肉发现的漂移：
#   1. pkg/validation        —— SensitivityLevels 对外契约枚举
#   2. pkg/naming/levels.go  —— 跨服务词表实现（id / canonical name / 中文名 / rank）
#   3. services/privacy-engine 常量        —— 三层漏斗内部 canonical 名称集合
#   4. 全仓 Go 源码          —— 中文名只允许在 pkg/naming/levels.go 定义一处
#
# Go 侧另有 pkg/naming/levels_test.go 做同构断言；本脚本的价值是无需编译即可在
# 任意开发机上给出可定位到文件的报错，因此被 make check 调用。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TAXONOMY="$ROOT/services/privacy-engine/rules/taxonomies/default.yaml"
[[ -f "$TAXONOMY" ]] || TAXONOMY="$ROOT/rules/taxonomies/default.yaml"
VALIDATION="$ROOT/pkg/validation/validation.go"
LEVELS_GO="$ROOT/pkg/naming/levels.go"
ENGINE_GO="$ROOT/services/privacy-engine/internal/dynclassification/engine.go"
[[ -f "$ENGINE_GO" ]] || ENGINE_GO="$ROOT/services/privacy-engine/internal/dynclassification/engine.go"

fail=0
err() {
  printf '  ✗ %s\n' "$*" >&2
  fail=1
}
ok() { printf '  ✓ %s\n' "$*"; }

[[ -f "$TAXONOMY" ]] || {
  printf 'taxonomy file not found: %s\n' "$TAXONOMY" >&2
  exit 1
}

# 从 default.yaml 的 levels: 段落抽取 "L1|公开数据|1"（id|中文名|rank），按 rank 升序。
taxonomy_levels() {
  awk '
    /^levels:/ { in_levels = 1; next }
    in_levels && /^[^[:space:]#]/ { in_levels = 0 }
    in_levels && /^  L[0-9]+:/ {
      cur = $1; gsub(/:/, "", cur)
    }
    in_levels && cur != "" && /id:[[:space:]]*"/ {
      match($0, /"L[0-9]+"/)
      if (RSTART > 0) id[cur] = substr($0, RSTART + 1, RLENGTH - 2)
    }
    in_levels && cur != "" && /name:[[:space:]]*"/ {
      match($0, /"[^"]*"/)
      if (RSTART > 0) name[cur] = substr($0, RSTART + 1, RLENGTH - 2)
    }
    in_levels && cur != "" && /rank:[[:space:]]*[0-9]+/ {
      rank[cur] = $NF
    }
    END {
      for (k in id) printf "%s|%s|%s\n", id[k], name[k], rank[k]
    }
  ' "$TAXONOMY" | sort -t'|' -k3,3n
}

TAXONOMY_LEVELS="$(taxonomy_levels)"
if [[ -z "$TAXONOMY_LEVELS" ]]; then
  printf 'ERROR: no levels parsed from %s\n' "$TAXONOMY" >&2
  exit 1
fi

echo "1) pkg/validation.SensitivityLevels 必须是 pkg/naming 的别名"
if grep -q 'SensitivityLevels = naming.SecurityLevelIDs()' "$VALIDATION"; then
  ok "SensitivityLevels 由 naming 词表派生，未在本文件维护字面量副本"
else
  err "pkg/validation 未通过 naming.SecurityLevelIDs() 派生 SensitivityLevels（P1-5 词表副本回潮）"
fi
literal_copies="$(grep -rn --include='*.go' -e '\[\]string{"L1"' "$ROOT" 2>/dev/null |
  grep -v -e '/pkg/naming/' -e '_test\.go$' -e '/node_modules/' || true)"
if [[ -z "$literal_copies" ]]; then
  ok "全仓未出现第二份 L1~L5 字面量枚举"
else
  err "以下位置重新硬编码了 L1~L5 枚举:"
  printf '%s\n' "$literal_copies" | sed 's|^|       |' >&2
fi

echo "2) rules/taxonomies/default.yaml → pkg/naming/levels.go"
naming_levels="$(sed -n \
  's/^[[:space:]]*{SecurityLevel\(L[0-9]*\), "[^"]*", "\([^"]*\)", \([0-9]*\)},$/\1|\2|\3/p' "$LEVELS_GO" |
  sort -t'|' -k3,3n)"
if [[ -z "$naming_levels" ]]; then
  err "无法从 pkg/naming/levels.go 抽取等级表（词表结构被改动，请同步本脚本）"
elif [[ "$TAXONOMY_LEVELS" == "$naming_levels" ]]; then
  ok "$(printf '%s\n' "$naming_levels" | wc -l | tr -d ' ') 个等级与 pkg/naming 词表逐字段一致"
else
  err "pkg/naming/levels.go 与分类体系不一致（左=YAML 右=Go）:"
  diff <(printf '%s\n' "$TAXONOMY_LEVELS") <(printf '%s\n' "$naming_levels") >&2 || true
fi

echo "3) pkg/naming canonical 名称 → services/privacy-engine SecurityLevel 常量"
naming_canonical="$(sed -n 's/^[[:space:]]*{SecurityLevelL[0-9]*, "\([^"]*\)", .*$/\1/p' "$LEVELS_GO" | sort | tr '\n' ' ' | sed 's/ $//')"
engine_canonical="$(sed -n 's/^[[:space:]]*Level[A-Za-z]*[[:space:]]*SecurityLevel[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$ENGINE_GO" | sort | tr '\n' ' ' | sed 's/ $//')"
# 两侧都抽空时字符串相等会给出假绿，因此先判定抽取结果非空。
if [[ -z "$naming_canonical" || -z "$engine_canonical" ]]; then
  err "canonical 名称抽取结果为空（naming=\"$naming_canonical\" engine=\"$engine_canonical\"），比对无效"
elif [[ "$naming_canonical" == "$engine_canonical" ]]; then
  ok "engine canonical 集合与词表一致: $engine_canonical"
else
  err "services/privacy-engine 的等级常量与 pkg/naming canonical 名称不一致:
       naming: $naming_canonical
       engine: $engine_canonical"
fi

echo "4) 等级中文名只允许定义在 pkg/naming/levels.go"
duplicates="$(grep -rl --include='*.go' -e '"公开数据"' -e '"内部数据"' -e '"高敏感数据"' -e '"极敏感数据"' "$ROOT" 2>/dev/null |
  grep -v -e '/pkg/naming/levels\.go$' -e '_test\.go$' -e '/node_modules/' || true)"
if [[ -z "$duplicates" ]]; then
  ok "未发现第二份中文等级词表副本"
else
  err "以下文件重新定义了等级中文名（应改用 naming.SecurityLevelLabel）:"
  printf '%s\n' "$duplicates" | sed 's|^|       |' >&2
fi

echo
if [[ "$fail" -ne 0 ]]; then
  cat >&2 <<'EOF'
等级词表一致性检查失败（P1-5）。
唯一事实源是 rules/taxonomies/default.yaml；Go 侧请只改 pkg/naming/levels.go，
其余位置一律通过 naming.SecurityLevelIDs / SecurityLevelLabel / NormalizeSecurityLevelID 取用。
EOF
  exit 1
fi
echo "等级词表一致性检查全部通过。"
