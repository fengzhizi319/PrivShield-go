#!/usr/bin/env bash
# ============================================================================
# Go gRPC Proxy Backend Development Startup Script
# Go gRPC 代理后端开发模式启动脚本
#
# Compiles and runs the Go gRPC proxy backend.
# 编译并运行 Go gRPC 代理后端。
# Default listen address: 127.0.0.1:8081, configurable via env vars.
# 默认监听地址：127.0.0.1:8081，可通过环境变量配置。
#
# Environment variables / 环境变量：
#   PRIVACY_CONSOLE_HOST - Listen host / 监听地址 (default: 127.0.0.1)
#   PRIVACY_CONSOLE_PORT - Listen port / 监听端口 (default: 8081)
# ============================================================================

# Enable strict mode: exit on error, undefined var, pipe failure
# 启用严格模式：遇错退出、未定义变量报错、管道失败报错
set -euo pipefail

# Change to script directory (console/bff-go/)
# 切换到脚本所在目录（console/bff-go/）
cd "$(dirname "$0")"

# Read listen host/port from env with defaults
# 从环境变量读取监听地址/端口，带默认值
HOST="${PRIVACY_CONSOLE_HOST:-127.0.0.1}"
PORT="${PRIVACY_CONSOLE_PORT:-8081}"

# Export for the Go binary to read via config.Load()
# 导出供 Go 二进制通过 config.Load() 读取
export PRIVACY_CONSOLE_HOST="$HOST"
export PRIVACY_CONSOLE_PORT="$PORT"

# Create bin directory for compiled binary
# 创建 bin 目录存放编译产物
mkdir -p bin
# Compile the Go server binary
# 编译 Go 服务器二进制
go build -o bin/backend-go ./cmd/server
# Replace current process with the compiled binary
# 用编译好的二进制替换当前进程
exec ./bin/backend-go
