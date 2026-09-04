#!/usr/bin/env bash
# ============================================================================
# Datasource Manager (模拟数据源服务) — 开发启动脚本 (Development Run)
#
# 特性与用途：
#   - 无需 mTLS 双向认证 (DATASOURCE_MGR_TLS_ENABLED=false)，降低本地联调门槛；
#   - 默认绑定 127.0.0.1 安全本地回环地址；
#   - 开启 text 纯文本高可读日志，LogLevel 设为 debug，便于调试排错；
#   - 自动在本地编译二进制并以 exec 替换当前进程启动。
#
# 端口监听：
#   - HTTP REST: http://127.0.0.1:8083 (通过 /health, /v1/yibao 等接口交互)
#   - gRPC (insecure): 127.0.0.1:50053 (为 service-hub 提供无加密快速 RPC 调试)
# ============================================================================

# 启用严格的 Shell 错误处理机制：
# -e: 命令执行失败（非 0 返回码）立即退出
# -u: 遇到未声明的变量立即报错
# -o pipefail: 管道中任一命令失败即认为整个管道失败
set -euo pipefail

# 1. 解析脚本所在目录与模块根目录绝对路径
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODULE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# 2. 切换当前工作目录至模块根目录，确保代码编译与相对路径定位正常
cd "$MODULE_DIR"

# 3. 设置 HTTP REST 监听主机与端口环境变量（未显式声明时回退至 127.0.0.1:8083）
export DATASOURCE_MGR_HOST="${DATASOURCE_MGR_HOST:-127.0.0.1}"
export DATASOURCE_MGR_PORT="${DATASOURCE_MGR_PORT:-8083}"

# 4. 设置 gRPC 监听主机与端口环境变量（未显式声明时回退至 127.0.0.1:50053）
export DATASOURCE_MGR_GRPC_HOST="${DATASOURCE_MGR_GRPC_HOST:-127.0.0.1}"
export DATASOURCE_MGR_GRPC_PORT="${DATASOURCE_MGR_GRPC_PORT:-50053}"

# 5. 显式禁用 TLS/mTLS 加密传输，启用开发文本调试日志
export DATASOURCE_MGR_TLS_ENABLED="false"
export DATASOURCE_MGR_LOG_FORMAT="${DATASOURCE_MGR_LOG_FORMAT:-text}"
export DATASOURCE_MGR_LOG_LEVEL="${DATASOURCE_MGR_LOG_LEVEL:-debug}"

# 6. 打印启动元数据信息横幅
echo "============================================================"
echo " 🚀 启动 datasource-mgr [开发调试模式 (Insecure / No-mTLS)]"
echo "============================================================"
echo "  HTTP REST: http://$DATASOURCE_MGR_HOST:$DATASOURCE_MGR_PORT"
echo "  gRPC:      $DATASOURCE_MGR_GRPC_HOST:$DATASOURCE_MGR_GRPC_PORT"
echo "  mTLS:      Disabled"
echo "  Log:       $DATASOURCE_MGR_LOG_FORMAT / $DATASOURCE_MGR_LOG_LEVEL"
echo "============================================================"

# 7. 创建 bin 编译产物目录
mkdir -p bin

# 8. 编译服务端可执行文件
go build -o bin/datasource-mgr ./cmd/server

# 9. 以 exec 执行编译好的二进制，使该进程接管当前 Shell，便于信号（SIGINT/SIGTERM）直接透传
exec ./bin/datasource-mgr
