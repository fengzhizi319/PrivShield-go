#!/usr/bin/env bash
# ============================================================================
# Generate mTLS test certificate chain for datasource-mgr gRPC server.
# 为 datasource-mgr 模拟数据源 gRPC 服务端生成 mTLS 测试证书链与公钥固定文件。
#
# 生成的文件清单 (默认输出目录: services/datasource-mgr/certs/)：
#   ca.crt / ca.key                 受信任的根 CA 证书与私钥 (RSA 4096-bit)
#   server.crt / server.key         服务端操作证书与私钥 (RSA 2048-bit, SAN: localhost/127.0.0.1, EKU: serverAuth)
#   client.crt / client.key         客户端操作证书与私钥 (RSA 2048-bit, CN: datasource-mgr-client, EKU: clientAuth)
#   client.pub                      客户端公钥 PEM 文件 (用于应用层公钥指纹固定 SPKI Pinning)
#
# 用法 (Usage)：
#   ./scripts/gen-certs.sh [output_dir]
#
# 安全说明 (Security Notice)：
#   本脚本生成的测试证书专供本地开发、集成测试与可复现验证使用。
#   生成的公钥 client.pub 可用于验证公钥固定机制（即客户端必须持有与此公钥匹配的私钥才能握手成功）。
# ============================================================================

set -euo pipefail

# 1. 解析目标输出目录与有效期参数（默认 3650 天）
OUT_DIR="${1:-$(dirname "$0")/../certs}"
DAYS="${CERT_DAYS:-3650}"

# 转换为绝对路径
OUT_DIR="$(cd "$(dirname "$OUT_DIR")" && pwd)/$(basename "$OUT_DIR")"

# 创建输出目录并切换
mkdir -p "$OUT_DIR"
cd "$OUT_DIR"

echo ">> 生成 datasource-mgr mTLS 测试证书到: $OUT_DIR"
echo "   有效期: $DAYS 天"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
# 1. 生成根 CA (Certificate Authority)
# ─────────────────────────────────────────────────────────────────────────────
# 执行逻辑：
# 1) 生成 4096-bit 高强度 RSA 私钥 (ca.key)；
# 2) 生成自签名的根证书 (ca.crt)，Subject CN 设为 "datasource-mgr-test-ca"，有效期 10 年。
echo ">> [1/4] 生成根 CA..."
openssl genrsa -out ca.key 4096
openssl req -x509 -new -nodes -key ca.key -sha256 -days "$DAYS" \
    -out ca.crt -subj "/CN=datasource-mgr-test-ca"

# ─────────────────────────────────────────────────────────────────────────────
# 2. 生成服务端证书 (Server Certificate with SAN)
# ─────────────────────────────────────────────────────────────────────────────
# 执行逻辑：
# 1) 生成 2048-bit RSA 服务端私钥 (server.key)；
# 2) 创建证书签名请求 (server.csr)，CN 设为 "localhost"；
# 3) 编写 X.509 V3 扩展配置文件 (server.ext)：
#    - basicConstraints: CA:FALSE（声明为终端实体证书而非 CA）；
#    - keyUsage: digitalSignature, keyEncipherment（数字签名与密钥加密）；
#    - extendedKeyUsage: serverAuth（用于 TLS 服务端身份验证）；
#    - subjectAltName: DNS:localhost, IP:127.0.0.1（支持本地主机名与回环 IP 安全访问）；
# 4) 使用根 CA 签发服务端证书 (server.crt)。
echo ">> [2/4] 生成服务端证书（SAN: localhost/127.0.0.1）..."
openssl genrsa -out server.key 2048
openssl req -new -key server.key -subj "/CN=localhost" -out server.csr

cat > server.ext <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=@alt_names

[alt_names]
DNS.1=localhost
IP.1=127.0.0.1
EOF

openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
    -out server.crt -days "$DAYS" -sha256 -extfile server.ext

# ─────────────────────────────────────────────────────────────────────────────
# 3. 生成客户端证书 (Client Certificate with clientAuth EKU)
# ─────────────────────────────────────────────────────────────────────────────
# 执行逻辑：
# 1) 生成 2048-bit RSA 客户端私钥 (client.key)；
# 2) 创建客户端证书签名请求 (client.csr)，CN 设为 "datasource-mgr-client"；
# 3) 编写 X.509 V3 客户端扩展配置文件 (client.ext)，声明 extendedKeyUsage = clientAuth；
# 4) 使用根 CA 签发客户端证书 (client.crt)。
echo ">> [3/4] 生成客户端证书（EKU: clientAuth）..."
openssl genrsa -out client.key 2048
openssl req -new -key client.key -subj "/CN=datasource-mgr-client" -out client.csr

cat > client.ext <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth
EOF

openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
    -out client.crt -days "$DAYS" -sha256 -extfile client.ext

# ─────────────────────────────────────────────────────────────────────────────
# 4. 提取客户端公钥 (Public Key Extraction for Key Pinning)
# ─────────────────────────────────────────────────────────────────────────────
# 执行逻辑：
# 从 client.key 导出标准 PEM 格式的主题公钥信息 (SubjectPublicKeyInfo)，保存为 client.pub，
# 供服务端运行时载入并执行严格的客户端公钥指纹固定比对。
echo ">> [4/4] 提取客户端公钥（用于公钥固定）..."
openssl rsa -in client.key -pubout -out client.pub

# ─────────────────────────────────────────────────────────────────────────────
# 5. 清理临时文件与设置安全文件权限
# ─────────────────────────────────────────────────────────────────────────────
# 移除 csr 签名请求与 ext 扩展临时文件
rm -f server.csr client.csr server.ext client.ext ca.srl

# 证书与公钥设置为 644（所有者可读写，他人只读），私钥严格设置为 600（仅所有者可读写）
chmod 644 ./*.crt ./*.pub 2>/dev/null || true
chmod 600 ./*.key 2>/dev/null || true

echo ""
echo ">> 完成，生成文件："
ls -la "$OUT_DIR"
echo ""
echo ">> datasource-mgr 生产运行 (prod-run.sh) 环境变量配置示例："
echo "   DATASOURCE_MGR_TLS_ENABLED=true \\"
echo "   DATASOURCE_MGR_TLS_CERT_FILE=$OUT_DIR/server.crt \\"
echo "   DATASOURCE_MGR_TLS_KEY_FILE=$OUT_DIR/server.key \\"
echo "   DATASOURCE_MGR_TLS_CA_FILE=$OUT_DIR/ca.crt \\"
echo "   DATASOURCE_MGR_TLS_CLIENT_AUTH=require \\"
echo "   DATASOURCE_MGR_TLS_PINNED_PUBKEY_FILE=$OUT_DIR/client.pub"
