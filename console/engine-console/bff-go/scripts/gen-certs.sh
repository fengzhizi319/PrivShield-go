#!/usr/bin/env bash
# ============================================================================
# Generate mTLS test certificate chain for Go BFF proxy and Agent.
# 为 Go BFF 代理网关与 PrivShield 算力 Agent 生成 mTLS 双向认证测试证书链。
#
# 生成的文件清单 (默认输出目录: console/bff-go/certs/)：
#   ca.crt / ca.key         受信任根 CA 证书与私钥 (RSA 4096-bit)
#   server.crt / server.key 服务端证书与私钥 (Agent/BFF 服务端，SAN: localhost/127.0.0.1)
#   client.crt / client.key 客户端证书与私钥 (BFF/Agent 客户端，EKU: clientAuth)
#   client.pub              客户端公钥 PEM (用于 SPKI 公钥固定 Pinning 校验)
#
# 用法 / Usage：
#   ./scripts/gen-certs.sh [output_dir]
#
# 环境变量：
#   CERT_DAYS: 证书有效天数（默认 3650 天 / 10 年）
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET_DIR="${1:-$SCRIPT_DIR/../certs}"
mkdir -p "$TARGET_DIR"
OUT_DIR="$(cd "$TARGET_DIR" && pwd)"
DAYS="${CERT_DAYS:-3650}"
SERVER_CN="${SERVER_CN:-localhost}"

if ! command -v openssl >/dev/null 2>&1; then
    echo "错误：未找到 openssl，请先安装。" >&2
    exit 1
fi

cd "$OUT_DIR"

echo ">> 输出目录: $OUT_DIR"
echo ">> 有效期:   ${DAYS} 天"
echo ">> 服务端 CN: ${SERVER_CN}"

# ── 1. 根 CA ──────────────────────────────────────────────────────────
echo ">> [1/4] 生成根 CA (4096-bit RSA)..."
openssl genrsa -out ca.key 4096
openssl req -x509 -new -nodes -key ca.key -sha256 -days "$DAYS" \
    -subj "/CN=PrivShield-test-ca" \
    -out ca.crt

# ── 2. 服务端证书（Agent / Go BFF gRPC 服务端）──────────────────────────
echo ">> [2/4] 生成服务端证书（含 localhost/127.0.0.1 SAN）..."
openssl genrsa -out server.key 2048
openssl req -new -key server.key -subj "/CN=${SERVER_CN}" -out server.csr
cat > server.ext <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=@alt_names

[alt_names]
DNS.1=localhost
DNS.2=PrivShield
DNS.3=privshield
DNS.4=console-backend-go
DNS.5=app-lz-bff
DNS.6=service-hub
DNS.7=datasource-mgr
DNS.8=audit-log
IP.1=127.0.0.1
IP.2=0.0.0.0
EOF
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
    -out server.crt -days "$DAYS" -sha256 -extfile server.ext

# ── 3. 客户端证书（Go BFF / Agent gRPC 客户端）──────────────────────────────
echo ">> [3/4] 生成客户端证书（EKU: clientAuth）..."
openssl genrsa -out client.key 2048
openssl req -new -key client.key -subj "/CN=privacy-console-go-client" -out client.csr
cat > client.ext <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth
EOF
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
    -out client.crt -days "$DAYS" -sha256 -extfile client.ext

# ── 4. 导出客户端公钥（用于 SPKI 公钥固定）───────────────────────────
echo ">> [4/4] 提取客户端公钥（用于公钥固定）..."
openssl rsa -in client.key -pubout -out client.pub

# ── 清理中间文件并设置容器读取权限 ────────────────────────────────────
rm -f server.csr client.csr server.ext client.ext ca.srl
chmod 644 ./*.key ./*.crt ./*.pub

echo ""
echo ">> 完成，生成文件："
ls -1 "$OUT_DIR"
