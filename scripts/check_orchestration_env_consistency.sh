#!/usr/bin/env bash
#
# check_orchestration_env_consistency.sh —— 编排环境变量 ↔ Go 代码读取点一致性检查（第十二章 P2-1）
#
# 背景：deploy/docker-compose/*.yml、deploy/k8s/*.yaml、services/*/deploy/k8s/*.yaml、
# deploy/helm/**/templates/*.yaml 里声明的环境变量若从未被任何 Go 代码读取，就是「配置漂移」：
# 运维按变量改配置却毫不生效，甚至误判某项能力（持久化、上游地址、鉴权）已经开启。
# 本仓已实际发现并清理过 DATASOURCE_MGR_DB_PATH / SERVICE_HUB_STORE_BACKEND / SERVICE_HUB_SQLITE_PATH /
# PRIVACY_AUTH_EXTERNAL_KEYS_JSON / SERVICE_HUB_AGENT_ADDR / PRIVACY_LOG_FORMAT(Go 栈) 等一批幽灵变量，
# 本脚本把该类漂移固化成 CI 红线，防止再被写回来。
#
# 判定一个变量「已被消费」需满足下列任一条：
#   1) 在任何 Go 源码里以字符串字面量形式出现（EnvString("NAME") / getEnv("NAME") 等）；
#   2) 在同一编排文件中被 ${NAME} 插值使用（compose 插值 / k8s 变量引用）；
#   3) 命中第三方镜像自有变量白名单（postgres / grafana / vllm / prometheus 等）；
#   4) 声明所在容器跑的不是本仓 Go 程序 —— compose/k8s 按镜像名自动判定（Python 引擎镜像
#      privshield、前端 -web、第三方镜像），Helm 模板因双引擎共用一份模板无法静态判定镜像，
#      改由人工标记 `# env-scope: python-engine`（声明行上方 4 行内）声明豁免。
#
# 唯一事实源仍是代码：新增读取点请同时更新编排；删除读取点请同步删掉编排里的变量。
#
# Usage:
#   bash scripts/check_orchestration_env_consistency.sh

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

fail=0
ok()  { printf '  [OK]   %s\n' "$*"; }
err() { printf '  [FAIL] %s\n' "$*"; fail=1; }

# 第三方镜像/外部组件自有变量：这些容器不是本仓 Go 程序，Go 侧永远不会有读取点。
THIRD_PARTY_RE='^(POSTGRES_|PGDATA$|GF_|VLLM_|LLM_|PROMETHEUS_|GRAFANA_|REDIS_|TZ$|LANG$|LC_|PYTHON[A-Z_]*$|NGINX_|OTEL_)'
# 非 Go 实现镜像（服务块 image 仓库名）：Python 引擎 privshield、前端 *-web、第三方组件。
NON_GO_IMAGE_RE='^(privshield|privshield-python|.*-web|nginx|vllm/vllm-openai|postgres|prom/prometheus|grafana/grafana|redis|curlimages/curl)$'
# 豁免标记（Helm 模板等无法静态判定镜像的编排）：声明行上方 4 行内出现即视为有意豁免。
SCOPE_MARKER='env-scope:[[:space:]]*(python-engine|third-party)'

# 1) 收集 Go 侧出现过的全部全大写环境变量字面量
echo "1) 提取 Go 代码读取点"
go_envs="$(mktemp)"
report="$(mktemp)"
skipped="$(mktemp)"
trap 'rm -f "$go_envs" "$report" "$skipped"' EXIT
grep -rhoE '"[A-Z][A-Z0-9_]{2,}"' --include='*.go' . 2>/dev/null \
  | tr -d '"' | sort -u > "$go_envs" || true
if [[ ! -s "$go_envs" ]]; then
  err "Go 读取点集合为空（提取失败即检查无效）"
  exit 1
fi
ok "Go 代码中出现的环境变量字面量：$(wc -l < "$go_envs") 个"

# 2) 提取编排文件里的变量声明，附带「所属容器块」的 image 仓库名
#    compose：服务键 ^ 两空格缩进；块内 ^      NAME: value（6 空格）为环境变量声明
#    k8s    ：容器键 - name: <小写容器名>；块内 - name: <全大写> 为环境变量声明
#    Helm   ：同一份模板服务双引擎，块级 image 判定不适用，统一按 - name: 提取 + 标记豁免
extract_declarations() {
  local f="$1"
  awk -v F="$f" '
    { lines[NR] = $0 }
    END {
      n = 0
      for (i = 1; i <= NR; i++) {
        if (lines[i] ~ /^  [A-Za-z0-9_.-]+:[ \t]*$/ || lines[i] ~ /^[ \t]+- name: [a-z][A-Za-z0-9_.-]*[ \t]*$/)
          starts[++n] = i
      }
      starts[n + 1] = NR + 1
      cur_img = ""
      for (b = 1; b <= n; b++) {
        s = starts[b]; e = starts[b + 1] - 1
        img = ""
        for (i = s; i <= e; i++) {
          if (match(lines[i], /^[ \t]+image:[ \t]*/)) {
            img = substr(lines[i], RSTART + RLENGTH)
            sub(/[ \t\r]+$/, "", img)
            # ${VAR:-repo:tag} 形式取默认值部分
            sub(/^\$\{[A-Za-z0-9_]+:-/, "", img)
            sub(/^\$\{[A-Za-z0-9_]+\}$/, "UNRESOLVED", img)
            sub(/^\$/, "UNRESOLVED", img)
            sub(/\}$/, "", img)
            sub(/:.*$/, "", img)     # 去 tag
            sub(/@.*$/, "", img)     # 去 digest
            break
          }
        }
        # 块内无 image 时继承上一块（k8s 里 ports/volume 的 - name: 也会被切成独立块，
        # 而同一容器的 env 必然出现在其 image 之后）。
        if (img != "") cur_img = img
        else img = cur_img
        for (i = s; i <= e; i++) {
          line = lines[i]; name = ""
          if (line ~ /^      [A-Z][A-Z0-9_]+:/) {
            name = substr(line, 7); sub(/:.*/, "", name)
          } else if (line ~ /^[ \t]+- name: [A-Z][A-Z0-9_]+[ \t]*$/) {
            name = line; sub(/.*- name: /, "", name); sub(/[ \t\r]+$/, "", name)
          }
          if (name != "") printf "%d\t%s\t%s\n", i, name, img
        }
      }
    }
  ' "$f"
}

# 声明行上方 4 行内是否有豁免标记
has_scope_marker() {
  local f="$1" ln="$2" from
  from=$(( ln > 4 ? ln - 4 : 1 ))
  sed -n "${from},${ln}p" "$f" 2>/dev/null | grep -qE "$SCOPE_MARKER"
}

echo "2) 校验编排变量是否有代码读取点"
targets=()
while IFS= read -r f; do targets+=("$f"); done < <(
  find deploy/docker-compose deploy/k8s deploy/helm services -type f \
    \( -path 'deploy/docker-compose/*.yml' -o -path 'deploy/k8s/*.yaml' \
       -o -path 'deploy/helm/*/templates/*.yaml' -o -path 'services/*/deploy/k8s/*.yaml' \) 2>/dev/null | sort
)
[[ ${#targets[@]} -eq 0 ]] && { err "未找到任何编排文件（检查无效）"; exit 1; }

checked=0
exempt_impl=0
for f in "${targets[@]}"; do
  is_helm=0
  [[ "$f" == deploy/helm/* ]] && is_helm=1
  while IFS=$'\t' read -r ln name img; do
    [[ -z "$name" ]] && continue
    checked=$((checked + 1))
    grep -qxF "$name" "$go_envs" && continue
    # compose 插值 / k8s 变量引用 也算消费（由 compose 或 shell 使用）
    if grep -Eq "\\\$\{?${name}([}:,]|\$)" "$f"; then continue; fi
    [[ "$name" =~ $THIRD_PARTY_RE ]] && continue
    # 非 Go 实现容器（Python 引擎 / 前端 / 第三方镜像）：不属于 Go 读取点校验范围
    if [[ $is_helm -eq 0 && -n "$img" && "$img" =~ $NON_GO_IMAGE_RE ]]; then
      exempt_impl=$((exempt_impl + 1))
      printf '       %s:%s  %s  (镜像 %s → 非 Go 实现)\n' "$f" "$ln" "$name" "$img" >> "$skipped"
      continue
    fi
    # Helm 模板：双引擎共用一份模板，按 env-scope 标记豁免
    if [[ $is_helm -eq 1 ]] && has_scope_marker "$f" "$ln"; then
      exempt_impl=$((exempt_impl + 1))
      printf '       %s:%s  %s  (env-scope 标记豁免)\n' "$f" "$ln" "$name" >> "$skipped"
      continue
    fi
    printf '       %s:%s  %s\n' "$f" "$ln" "$name" >> "$report"
  done < <(extract_declarations "$f")
done

if [[ -s "$skipped" ]]; then
  printf '  [SKIP] %d 个声明按实现归属/标记豁免（非本仓 Go 程序读取点）：\n' "$exempt_impl"
  cat "$skipped"
fi

if [[ ! -s "$report" ]]; then
  ok "全部 ${checked} 个编排变量声明都能在 Go 代码/插值/白名单/豁免标记中找到消费点"
else
  err "以下编排变量没有任何消费点（幽灵变量，运维配置不生效）："
  cat "$report" >&2
fi

echo
if [[ "$fail" -ne 0 ]]; then
  cat >&2 <<'EOF'
编排变量一致性检查失败（P2-1）。
幽灵变量只有三种正确处理方式：
  · 该能力本就该存在 → 在对应服务的 config.go 里补读取点（并写进 docs/api.md 变量表）；
  · 该能力不存在/已改名 → 从编排里删除该变量，必要时留一行注释说明真实变量名；
  · 该变量确实只被非 Go 实现（Python 引擎/第三方）读取 → Go 栈里删除，其他编排里加
    `# env-scope: python-engine` 标记（Helm）或让容器镜像名可判定归属。
EOF
  exit 1
fi
echo "编排变量一致性检查全部通过。"
