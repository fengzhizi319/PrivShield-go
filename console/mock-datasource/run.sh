#!/usr/bin/env bash
# ============================================================================
# Datasource Manager (模拟数据源服务) 统一快速启动入口脚本
#
# 描述：
#   该脚本作为 datasource-mgr 模块的顶层 CLI 入口，支持根据传入的运行模式参数，
#   快速分发并启动本地开发调试环境（dev）或生产 mTLS 加固环境（prod）。
#
# 用法 (Usage)：
#   bash run.sh [dev|prod]
#
# 参数说明：
#   dev  (默认): 启动开发模式，禁用 mTLS，绑定 127.0.0.1，输出 text 日志；
#   prod        : 启动生产加固模式，强制启用双向 mTLS 与公钥固定，绑定 0.0.0.0，输出 json 结构化日志。
# ============================================================================

set -euo pipefail

# 切换工作目录至脚本所在目录（services/datasource-mgr/）
cd "$(dirname "$0")"

# 获取运行模式参数，默认为 dev 模式
MODE="${1:-dev}"

# 根据模式分发执行对应的启动脚本
if [[ "$MODE" == "prod" ]]; then
    echo ">> 以生产模式启动 datasource-mgr (调用 scripts/prod-run.sh)..."
    exec bash scripts/prod-run.sh
else
    echo ">> 以开发模式启动 datasource-mgr (调用 scripts/dev-run.sh)..."
    exec bash scripts/dev-run.sh
fi
