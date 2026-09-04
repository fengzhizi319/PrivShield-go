#!/usr/bin/env bash
# ============================================================================
# 【生产模式】PrivShield 原生 Kubernetes Kustomize 生产发布脚本
# Deploy PrivShield via Native Kubernetes Kustomize
#
# 与 Helm 部署的区别：
#   - Helm（deploy-helm.sh）：Go 模板 + values 参数驱动，功能全面（HPA/Ingress/PDB 等）
#   - 本脚本（deploy-k8s.sh）：直接 apply 静态 YAML 清单，适合无 Helm 环境或轻量部署
#
# 执行步骤总览：
#   1. 解析命令行参数（命名空间）
#   2. 前置检查：kubectl 命令是否可用
#   3. 确保 K8s 命名空间存在（幂等创建）
#   4. 通过 Kustomize 应用全部资源清单（Namespace/ConfigMap/Deployment/Service）
#   5. 等待 Deployment 滚动更新就绪
#   6. 输出部署结果与后续验证命令
#
# 用法 / Usage:
#   ./scripts/prod/deploy-k8s.sh [选项]
#
# 选项 / Options:
#   -n, --namespace NS    Kubernetes 命名空间 (默认: privshield 或环境变量 K8S_NAMESPACE)
#   --with-postgres       同时部署 Phase B PostgreSQL 资源（service-hub 多副本模式）
#   -h, --help            显示帮助信息并退出
# ============================================================================

# set -e: 任何命令返回非零状态码立即退出（防止错误级联）
# set -u: 引用未定义变量时报错（防止拼写错误导致静默失败）
# set -o pipefail: 管道中任一命令失败则整体返回非零（防止 | 后掩盖错误）
set -euo pipefail

# ── 步骤 0：定位路径 ──────────────────────────────────────────────────────
# 通过 $0（脚本自身路径）反推项目根目录，确保无论从哪里调用都能正确定位 K8s 清单
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"          # 脚本所在目录：scripts/prod/
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"       # 上溯两级：项目根目录
K8S_DIR="$PROJECT_ROOT/deploy/k8s"                     # 原生 K8s 清单目录（含 kustomization.yaml）

# ── 步骤 1：设置参数默认值并解析命令行参数 ────────────────────────────────
# 命名空间优先级：命令行 -n > 环境变量 K8S_NAMESPACE > 默认值 privshield
NAMESPACE="${K8S_NAMESPACE:-privshield}"
WITH_POSTGRES=false
GO_ENGINE=true

# 遍历所有位置参数，按 --key value 配对消费（shift 2 跳过已处理的两个参数）
while [[ $# -gt 0 ]]; do
    case "$1" in
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        --go-engine)
            GO_ENGINE=true
            shift
            ;;
        --with-postgres)
            WITH_POSTGRES=true
            shift
            ;;
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "选项 / Options:"
            echo "  -n, --namespace NS    Kubernetes 命名空间 (默认: privshield 或 K8S_NAMESPACE)"
            echo "  --go-engine           使用 Go 原生引擎清单 (deployment-go / service-go)"
            echo "  --with-postgres       同时部署 Phase B PostgreSQL 资源"
            echo "  -h, --help            显示帮助信息并退出"
            exit 0
            ;;
        *)
            echo "❌ [错误] 未知参数: $1" >&2
            exit 1
            ;;
    esac
done

# ── 步骤 2：打印部署摘要 ──────────────────────────────────────────────────
echo "============================================================================"
echo "☸️  【生产模式】PrivShield 原生 Kubernetes Kustomize 部署"
echo "============================================================================"
if [[ "$GO_ENGINE" == "true" ]]; then
    echo "  • 引擎架构 : Go 原生高性能引擎 (privshield-go:1.0.0)"
else
    echo "  • 引擎架构 : Python 核心引擎 (privshield:1.8.0)"
fi

# ── 步骤 3：前置检查 — kubectl 可用性 ────────────────────────────────────
if ! command -v kubectl >/dev/null 2>&1; then
    echo "❌ [错误] 未检测到 kubectl 命令，请先安装并配置 kubectl。" >&2
    exit 1
fi

# ── 步骤 4：幂等创建命名空间 ──────────────────────────────────────────────
echo "📦 检查或创建命名空间 [$NAMESPACE]..."
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# ── 步骤 5：通过 Kustomize 应用全部资源清单 ───────────────────────────────
if [[ "$GO_ENGINE" == "true" ]]; then
    echo "🚀 应用 Go 引擎 K8s 资源清单..."
    kubectl apply -f "$K8S_DIR/namespace.yaml" -n "$NAMESPACE"
    kubectl apply -f "$K8S_DIR/configmap.yaml" -n "$NAMESPACE"
    kubectl apply -f "$K8S_DIR/deployment-go.yaml" -n "$NAMESPACE"
    kubectl apply -f "$K8S_DIR/service-go.yaml" -n "$NAMESPACE"
    kubectl apply -k "$PROJECT_ROOT/services/service-hub/deploy/k8s" -n "$NAMESPACE"
    kubectl apply -k "$PROJECT_ROOT/console/mock-datasource/deploy/k8s" -n "$NAMESPACE"
    kubectl apply -k "$PROJECT_ROOT/services/audit-log/deploy/k8s" -n "$NAMESPACE"
    kubectl apply -k "$PROJECT_ROOT/console/deploy/k8s" -n "$NAMESPACE"
else
    echo "🚀 应用 Kustomize 资源清单 ($K8S_DIR)..."
    kubectl apply -k "$K8S_DIR" -n "$NAMESPACE"
fi

# ── 步骤 5b：可选 — 部署 Phase B PostgreSQL 资源 ─────────────────────────
if [[ "$WITH_POSTGRES" == "true" ]]; then
    PG_DIR="$PROJECT_ROOT/services/service-hub/deploy/k8s/postgres"
    echo ""
    echo "🐘 应用 Phase B PostgreSQL 资源 ($PG_DIR)..."
    kubectl apply -k "$PG_DIR" -n "$NAMESPACE"
    echo "⏳ 等待 PostgreSQL Deployment 就绪..."
    kubectl rollout status deployment/privshield-postgres -n "$NAMESPACE" --timeout=120s || true
fi

# ── 步骤 6：等待 Deployment 滚动更新就绪 ────────────────────────────────
echo ""
echo "⏳ 等待 Deployment 滚动更新就绪..."
if [[ "$GO_ENGINE" == "true" ]]; then
    kubectl rollout status deployment/privshield-go -n "$NAMESPACE" --timeout=180s || true
else
    kubectl rollout status deployment/privshield -n "$NAMESPACE" --timeout=180s || true
fi

# ── 步骤 7：输出部署结果与后续验证命令 ────────────────────────────────────
# 提供常用 kubectl 命令帮助运维快速确认部署状态
echo ""
echo "============================================================================"
echo "🎉 Kubernetes 资源部署完成！"
if [[ "$WITH_POSTGRES" == "true" ]]; then
    echo "  🐘 PostgreSQL 已部署 (Phase B)"
fi
echo "  - 查看 Pods    : kubectl get pods -n $NAMESPACE"
echo "  - 查看 Services: kubectl get svc -n $NAMESPACE"
echo "============================================================================"
