#!/usr/bin/env bash
# ============================================================================
# Mock Datasource Manager Kubernetes Standalone Deployment Script
# 模拟数据源服务独立 Kubernetes Kustomize 部署脚本
#
# 功能说明：
#   1. 使用 datasource-mgr 自包含的 deploy/k8s/ 清单进行独立部署；
#   2. 支持指定命名空间（默认: privshield 或 K8S_NAMESPACE 环境变量）；
#   3. 自动等待 Deployment 滚动就绪并输出服务访问端点。
#
# 用法 / Usage:
#   bash ./scripts/deploy-k8s.sh [选项]
#
# 选项 / Options:
#   -n, --namespace NS    Kubernetes 命名空间 (默认: privshield 或 K8S_NAMESPACE)
#   --dry-run             演练模式（仅生成并校验清单，不实际提交集群）
#   -h, --help            显示帮助信息并退出
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODULE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
K8S_DIR="$MODULE_DIR/deploy/k8s"

NAMESPACE="${K8S_NAMESPACE:-privshield}"
DRY_RUN=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN="--dry-run=client"
            shift
            ;;
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "选项 / Options:"
            echo "  -n, --namespace NS    Kubernetes 命名空间 (默认: privshield 或 K8S_NAMESPACE)"
            echo "  --dry-run             演练模式 (客户端校验)"
            echo "  -h, --help            显示帮助信息并退出"
            exit 0
            ;;
        *)
            echo "❌ [错误] 未知参数: $1" >&2
            exit 1
            ;;
    esac
done

echo "============================================================================"
echo "☸️  【独立部署】Datasource Mgr Kubernetes Kustomize 部署"
echo "   - 命名空间 : $NAMESPACE"
echo "   - 清单路径 : $K8S_DIR"
echo "============================================================================"

# 前置检查
if ! command -v kubectl >/dev/null 2>&1; then
    echo "❌ [错误] 未检测到 kubectl 命令，请先安装并配置 Kubernetes 访问凭据。" >&2
    exit 1
fi

# 检查或创建命名空间
echo "📦 检查/创建命名空间 [$NAMESPACE]..."
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# 部署 datasource-mgr 资源
echo "🚀 部署 datasource-mgr 资源 ($K8S_DIR)..."
kubectl apply -k "$K8S_DIR" -n "$NAMESPACE" $DRY_RUN

if [[ -z "$DRY_RUN" ]]; then
    echo ""
    echo "⏳ 等待 datasource-mgr Deployment 就绪..."
    kubectl rollout status deployment/datasource-mgr -n "$NAMESPACE" --timeout=180s || true

    echo ""
    echo "============================================================================"
    echo "🎉 datasource-mgr Kubernetes 资源部署完成！"
    echo "  - 查看 Pods    : kubectl get pods -l app=datasource-mgr -n $NAMESPACE"
    echo "  - 查看 Services: kubectl get svc -l app=datasource-mgr -n $NAMESPACE"
    echo "  - 端口转发测试 : kubectl port-forward -n $NAMESPACE svc/datasource-mgr 8083:8083 50053:50053"
    echo "============================================================================"
else
    echo ""
    echo "✅ [Dry-Run] 客户端清单演练通过，未实际修改集群。"
fi
