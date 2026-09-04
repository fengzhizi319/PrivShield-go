#!/usr/bin/env bash
# ============================================================================
# Mock Datasource Manager Kubernetes Standalone Cleanup Script
# 模拟数据源服务独立 Kubernetes 资源卸载与停止脚本
#
# 用法 / Usage:
#   bash ./scripts/stop-k8s.sh [选项]
#
# 选项 / Options:
#   -n, --namespace NS    Kubernetes 命名空间 (默认: privshield 或 K8S_NAMESPACE)
#   -h, --help            显示帮助信息并退出
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODULE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
K8S_DIR="$MODULE_DIR/deploy/k8s"

NAMESPACE="${K8S_NAMESPACE:-privshield}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        -h|--help)
            echo "用法 / Usage: $0 [选项]"
            echo ""
            echo "选项 / Options:"
            echo "  -n, --namespace NS    Kubernetes 命名空间 (默认: privshield 或 K8S_NAMESPACE)"
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
echo "🛑 正在卸载 datasource-mgr Kubernetes 资源 (Namespace: $NAMESPACE)..."
echo "============================================================================"

if ! command -v kubectl >/dev/null 2>&1; then
    echo "❌ [错误] 未检测到 kubectl 命令。" >&2
    exit 1
fi

kubectl delete -k "$K8S_DIR" -n "$NAMESPACE" --ignore-not-found=true

echo "✅ datasource-mgr Kubernetes 资源已成功删除。"
echo "============================================================================"
