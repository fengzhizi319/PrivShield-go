#!/usr/bin/env bash
# ============================================================================
# Datasource Manager (模拟数据源服务) — 生产启动脚本 (Production Run with mTLS)
#
# 特性与安全保障机制：
#   - 强制开启 mTLS 双向身份认证 (DATASOURCE_MGR_TLS_ENABLED=true)；
#   - 强制要求并校验客户端证书 (DATASOURCE_MGR_TLS_CLIENT_AUTH=require)；
#   - 启用应用层客户端公钥固定 (DATASOURCE_MGR_TLS_PINNED_PUBKEY_FILE=client.pub)，防 CA 签发伪造证书；
#   - 自动检测并生成测试环境缺失的证书链；
#   - 默认绑定 0.0.0.0，适用于容器编排或跨节点多微服务网络交互；
#   - 启用 json 结构化日志，LogLevel 设为 info，方便 Loki/ELK 采集分析。
#
# 端口监听：
#   - HTTPS REST (mTLS): https://0.0.0.0:8083 (强制 mTLS 握手校验)
#   - gRPC (mTLS): 0.0.0.0:50053 (为 service-hub 等调用方提供强加密与公钥固定连接)
# ============================================================================

# 启用严格 Shell 错误处理
set -euo pipefail

# 1. 计算脚本目录、模块根目录与证书存放目录
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODULE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
CERTS_DIR="${DATASOURCE_MGR_CERTS_DIR:-$MODULE_DIR/certs}"

# 2. 切换当前工作目录至模块根目录
cd "$MODULE_DIR"

# 3. 检查 mTLS 证书链完整性：
#    若 server.crt, server.key, ca.crt 或 client.pub 任一文件缺失，则自动调用 gen-certs.sh 重新生成测试证书链
if [[ ! -f "$CERTS_DIR/server.crt" || ! -f "$CERTS_DIR/server.key" || ! -f "$CERTS_DIR/ca.crt" || ! -f "$CERTS_DIR/client.pub" ]]; then
    echo ">> ⚠️ 未在 $CERTS_DIR 找到完整证书链，正在自动生成测试证书..."
    bash "$SCRIPT_DIR/gen-certs.sh" "$CERTS_DIR"
fi

# 4. 设置生产默认监听主机与端口（默认全网卡 0.0.0.0）
export DATASOURCE_MGR_HOST="${DATASOURCE_MGR_HOST:-0.0.0.0}"
export DATASOURCE_MGR_PORT="${DATASOURCE_MGR_PORT:-8083}"
export DATASOURCE_MGR_GRPC_HOST="${DATASOURCE_MGR_GRPC_HOST:-0.0.0.0}"
export DATASOURCE_MGR_GRPC_PORT="${DATASOURCE_MGR_GRPC_PORT:-50053}"

# 5. 注入生产 mTLS 传输安全与公钥固定环境变量
export DATASOURCE_MGR_TLS_ENABLED="true"
export DATASOURCE_MGR_TLS_CERT_FILE="${DATASOURCE_MGR_TLS_CERT_FILE:-$CERTS_DIR/server.crt}"
export DATASOURCE_MGR_TLS_KEY_FILE="${DATASOURCE_MGR_TLS_KEY_FILE:-$CERTS_DIR/server.key}"
export DATASOURCE_MGR_TLS_CA_FILE="${DATASOURCE_MGR_TLS_CA_FILE:-$CERTS_DIR/ca.crt}"
export DATASOURCE_MGR_TLS_CLIENT_AUTH="${DATASOURCE_MGR_TLS_CLIENT_AUTH:-require}"
export DATASOURCE_MGR_TLS_PINNED_PUBKEY_FILE="${DATASOURCE_MGR_TLS_PINNED_PUBKEY_FILE:-$CERTS_DIR/client.pub}"

# 6. 配置生产 JSON 结构化日志
export DATASOURCE_MGR_LOG_FORMAT="${DATASOURCE_MGR_LOG_FORMAT:-json}"
export DATASOURCE_MGR_LOG_LEVEL="${DATASOURCE_MGR_LOG_LEVEL:-info}"

# 7. 打印安全启动配置清单
echo "============================================================"
echo " 🔒 启动 datasource-mgr [生产加固模式 (双协议 mTLS + 公钥固定)]"
echo "============================================================"
echo "  HTTPS REST (mTLS): https://$DATASOURCE_MGR_HOST:$DATASOURCE_MGR_PORT"
echo "  gRPC (mTLS):       $DATASOURCE_MGR_GRPC_HOST:$DATASOURCE_MGR_GRPC_PORT"
echo "  Server Cert: $DATASOURCE_MGR_TLS_CERT_FILE"
echo "  CA File:     $DATASOURCE_MGR_TLS_CA_FILE"
echo "  Client Auth: $DATASOURCE_MGR_TLS_CLIENT_AUTH"
echo "  Pinned Key:  $DATASOURCE_MGR_TLS_PINNED_PUBKEY_FILE"
echo "  Log:         $DATASOURCE_MGR_LOG_FORMAT / $DATASOURCE_MGR_LOG_LEVEL"
echo "============================================================"

# 8. 创建编译产物输出目录
mkdir -p bin

# 9. 编译 Go 服务端可执行文件
go build -o bin/datasource-mgr ./cmd/server

# 10. 以 exec 执行二进制启动生产服务
exec ./bin/datasource-mgr
