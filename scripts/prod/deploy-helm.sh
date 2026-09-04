#!/usr/bin/env bash
# ============================================================================
# 执行步骤总览：
#   1. 解析命令行参数（命名空间、Release 名、values 文件、TLS/Auth Secret 等）
#   2. 打印部署摘要信息（Namespace / Release / Values / Chart 路径）
#   3. 前置检查：helm 与 kubectl 命令是否可用
#   4. 前置检查：values 文件是否存在
#   5. Helm Lint 静态校验（模板语法 + values 类型检查，不实际部署）
#   6. 组装 --set 覆盖参数（TLS Secret 名、Auth Secret 名）
#   7. 执行 helm upgrade --install（首次安装或平滑升级，零停机）
#   8. 输出部署结果与后续验证命令
#
# 用法 / Usage:
#   ./scripts/prod/deploy-helm.sh [选项]
#
# 选项 / Options:
#   -n, --namespace NS       部署命名空间 (默认: privshield)
#   -r, --release RELEASE    Helm Release 名称 (默认: privshield)
#   -f, --values VALUES      生产 values 文件 (默认: deploy/helm/PrivShield/values-production.yaml)
#   --tls-secret SECRET      已有 TLS Secret 名称 (生产强制建议提供)
#   --auth-secret SECRET     已有 API Key Auth Secret 名称
#   --dry-run                执行试运行演练 (不实际修改集群)
#   --with-postgres          同时部署 Phase B PostgreSQL 资源（多副本 Hub 模式）
#   -h, --help               显示帮助信息
# ============================================================================

# set -e: 任何命令返回非零状态码立即退出（防止错误级联）
# set -u: 引用未定义变量时报错（防止拼写错误导致静默失败）
# set -o pipefail: 管道中任一命令失败则整体返回非零（防止 | 后掩盖错误）
set -euo pipefail

# ── 步骤 0：定位路径 ──────────────────────────────────────────────────────
# 通过 $0（脚本自身路径）反推项目根目录，确保无论从哪里调用都能正确定位 Chart
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"          # 脚本所在目录：scripts/prod/
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"       # 上溯两级：项目根目录
CHART_DIR="$PROJECT_ROOT/deploy/helm/PrivShield"       # Helm Chart 模板目录

# ── 步骤 1：设置参数默认值 ──────────────────────────────────────────────────
# 未通过命令行指定时使用以下默认值；生产 values 覆盖 values.yaml 中的开发默认
NAMESPACE="privshield"                                 # K8s 命名空间（资源隔离边界）
RELEASE_NAME="privshield"                              # Helm Release 名称（标识一次部署实例）
VALUES_FILE=""                                         # 生产环境 values 覆盖文件
GO_ENGINE=true                                         # 默认使用 Go 原生引擎
TLS_SECRET=""                                          # 外部 TLS Secret 名称（部署时 --set 注入）
AUTH_SECRET=""                                         # 外部 API Key Secret 名称（部署时 --set 注入）
DRY_RUN=""                                             # 非空时 helm 仅演练不实际变更
WITH_POSTGRES=false                                    # 是否同时部署 Phase B PostgreSQL

# ── 步骤 2：解析命令行参数 ──────────────────────────────────────────────────
# 遍历所有位置参数，按 --key value 配对消费（shift 2 跳过已处理的两个参数）
while [[ $# -gt 0 ]]; do
    case "$1" in
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        -r|--release)
            RELEASE_NAME="$2"
            shift 2
            ;;
        -f|--values)
            VALUES_FILE="$2"
            shift 2
            ;;
        --go-engine)
            GO_ENGINE=true
            shift 1
            ;;
        --tls-secret)
            TLS_SECRET="$2"
            shift 2
            ;;
        --auth-secret)
            AUTH_SECRET="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN="--dry-run"
            shift 1
            ;;
        --with-postgres)
            WITH_POSTGRES=true
            shift 1
            ;;
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "选项 / Options:"
            echo "  -n, --namespace NS       Kubernetes 命名空间 (默认: privshield)"
            echo "  -r, --release RELEASE    Helm Release 实例名称 (默认: privshield)"
            echo "  -f, --values VALUES      生产 values 配置文件路径"
            echo "  --go-engine              部署 Go 原生引擎 (启用 values-production-go.yaml / engineType=go)"
            echo "  --tls-secret SECRET      Kubernetes TLS Secret 资源名称"
            echo "  --auth-secret SECRET     Kubernetes API Key Auth Secret 资源名称"
            echo "  --dry-run                执行 dry-run 演练测试"
            echo "  --with-postgres          同时部署 Phase B PostgreSQL 资源"
            echo "  -h, --help               显示帮助信息并退出"
            exit 0
            ;;
        *)
            echo "❌ [错误] 未知参数: $1" >&2
            exit 1
            ;;
    esac
done

if [[ -z "$VALUES_FILE" ]]; then
    if [[ "$GO_ENGINE" == "true" && -f "$CHART_DIR/values-production-go.yaml" ]]; then
        VALUES_FILE="$CHART_DIR/values-production-go.yaml"
    else
        VALUES_FILE="$CHART_DIR/values-production.yaml"
    fi
fi

# ── 步骤 3：打印部署摘要 ──────────────────────────────────────────────────
# 在执行任何操作前展示本次部署的关键参数，便于运维确认和审计追溯
echo "============================================================================"
echo "☸️  【生产模式】PrivShield Helm 生产部署与发布"
echo "============================================================================"
echo "  • 命名空间 (Namespace) : $NAMESPACE"
echo "  • Release 名称         : $RELEASE_NAME"
echo "  • Values 文件          : $VALUES_FILE"
echo "  • Chart 目录           : $CHART_DIR"
if [[ "$WITH_POSTGRES" == "true" ]]; then
    echo "  • Phase B PostgreSQL   : 已启用 (多副本 Hub 模式)"
fi

# ── 步骤 4：前置检查 — 工具链可用性 ────────────────────────────────────────
# command -v 检查命令是否存在于 PATH 中；缺失则提示安装方式并退出
# 检查 Helm CLI（模板渲染 + Release 生命周期管理）
if ! command -v helm >/dev/null 2>&1; then
    echo "❌ [错误] 未检测到 helm 命令，请先安装 Helm: https://helm.sh/docs/intro/install/" >&2
    exit 1
fi

# 检查 kubectl CLI（K8s API Server 客户端，helm 底层也依赖它）
if ! command -v kubectl >/dev/null 2>&1; then
    echo "❌ [错误] 未检测到 kubectl 命令，请先配置 Kubernetes 客户端工具。" >&2
    exit 1
fi

# ── 步骤 5：前置检查 — values 文件存在性 ───────────────────────────────────
# 防止传入错误路径导致 helm lint/install 失败
if [[ ! -f "$VALUES_FILE" ]]; then
    echo "❌ [错误] Values 文件不存在: $VALUES_FILE" >&2
    exit 1
fi

# ── 步骤 6：Helm Lint 静态校验 ────────────────────────────────────────────
# helm lint 在不连接集群的情况下检查：
#   - Chart.yaml 格式是否正确
#   - templates/ 中 Go 模板语法是否合法（如 {{ }} 配对、函数调用正确）
#   - values.yaml 与模板引用的字段是否匹配
# 相当于编译器的"语法检查"阶段，尽早发现错误避免部署失败
# shellcheck disable=SC2086
echo ""
echo "🔍 正在进行 Helm Chart 静态校验 (helm lint)..."
helm lint "$CHART_DIR" -f "$VALUES_FILE"

# ── 步骤 7：组装 --set 覆盖参数 ────────────────────────────────────────────
# 将命令行传入的 Secret 名称转为 helm --set 参数，覆盖 values-production.yaml 中的空值
# 使用数组收集，便于后续展开为多个 --set 参数
EXTRA_SETS=()
if [[ -n "$TLS_SECRET" ]]; then
    # 覆盖 security.tls.existingSecret → deployment.yaml 中引用此 Secret 挂载 TLS 证书
    EXTRA_SETS+=("--set" "security.tls.existingSecret=$TLS_SECRET")
fi
if [[ -n "$AUTH_SECRET" ]]; then
    # 覆盖 security.auth.apiKeysSecret → deployment.yaml 中引用此 Secret 注入 API Key
    EXTRA_SETS+=("--set" "security.auth.apiKeysSecret=$AUTH_SECRET")
fi

# ── 步骤 8：执行 Helm 部署/升级 ───────────────────────────────────────────
# helm upgrade --install 的核心语义：
#   - 若 Release 不存在 → 等价 helm install（首次安装）
#   - 若 Release 已存在 → 等价 helm upgrade（平滑升级，滚动更新 Pod，零停机）
#
# Helm 与 kubectl 的关系（重要澄清）：
#   Helm 并不调用 kubectl CLI，而是通过 Go 语言 client-go 库直接与 K8s API Server
#   通信（HTTP REST / protobuf）。但效果等价于执行了一系列 kubectl 命令：
#
#   helm upgrade --install 实际等价于：
#     1. helm template          → 渲染 Go 模板为纯 K8s YAML（本地完成）
#     2. kubectl apply -f       → 将渲染后的 YAML 提交给 K8s API Server
#        （API Server 校验后写入 etcd，kubelet 根据新 spec 创建/更新 Pod）
#     3. kubectl rollout status → --wait 标志使 Helm 轮询各资源状态直至就绪
#
#   具体对应的 kubectl 等效命令（本脚本场景）：
#     kubectl create namespace $NAMESPACE              ← --create-namespace
#     kubectl apply -f <rendered-templates>             ← 核心部署动作
#     kubectl rollout status deployment/<name> --timeout=5m  ← --wait
#
#   因此脚本开头检查 kubectl 并非因为 Helm 调用它，而是因为：
#     - 运维后续验证（kubectl get pods / kubectl logs）需要它
#     - 部分集群环境 kubeconfig 由 kubectl 配置管理
#
# 关键参数说明：
#   --namespace       指定 K8s 命名空间（资源隔离边界）
#   --create-namespace 命名空间不存在时自动创建（避免手动 kubectl create ns）
#   -f                加载生产 values 覆盖文件（覆盖 values.yaml 中的开发默认值）
#   --set             命令行级覆盖（优先级最高，用于注入 Secret 名称等敏感参数）
#   --wait            等待所有资源就绪（Pod Ready、Service 分配 IP 等）再返回
#   --timeout 5m      --wait 的最大等待时间，超时则回滚并报错
# shellcheck disable=SC2086
echo ""
echo "🚀 正在执行 Helm 部署 (helm upgrade --install)..."
helm upgrade --install "$RELEASE_NAME" "$CHART_DIR" \
    --namespace "$NAMESPACE" \
    --create-namespace \
    -f "$VALUES_FILE" \
    "${EXTRA_SETS[@]}" \
    $DRY_RUN \
    --wait \
    --timeout 5m

# ── 步骤 8b：可选 — 部署 Phase B PostgreSQL 资源 ─────────────────────
if [[ "$WITH_POSTGRES" == "true" ]]; then
    PG_DIR="$PROJECT_ROOT/services/service-hub/deploy/k8s/postgres"
    echo ""
    echo "🐘 部署 Phase B PostgreSQL 资源..."
    kubectl apply -k "$PG_DIR" -n "$NAMESPACE"
    echo "⏳ 等待 PostgreSQL Deployment 就绪..."
    kubectl rollout status deployment/service-hub-postgres -n "$NAMESPACE" --timeout=120s || true
fi

# ── 步骤 9：输出部署结果 ──────────────────────────────────────────────────
# 根据是否为 dry-run 模式输出不同的成功提示和后续验证命令
echo ""
echo "============================================================================"
if [[ -n "$DRY_RUN" ]]; then
    # dry-run 模式：模板渲染通过但未实际变更集群，输出演练成功提示
    echo "✅ [Dry-Run 成功] Helm 模板演练通过，未对集群做实际变更。"
else
    # 实际部署模式：输出后续验证命令，帮助运维快速确认部署状态
    echo "🎉 PrivShield 生产 Helm Release [$RELEASE_NAME] 部署成功！"
    echo ""
    echo "查看 Deployment 状态:"
    echo "  kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=PrivShield"
    echo "查看 Service 状态:"
    echo "  kubectl get svc -n $NAMESPACE"
fi
echo "============================================================================"
