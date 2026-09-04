#!/usr/bin/env bash
# ============================================================================
# 脚本名称: generate_all_test_certs.sh
# 脚本说明: 全局自动化生成并预置 PrivShield 各微服务与网关 mTLS 测试证书链。
#
# 证书覆盖模块：
#   1. config/certs/                  全局通用 mTLS 测试证书 (Root CA, Server, Client, Client PubKey)
#   2. console/engine-console/bff-go/certs/          Go BFF 代理网关与 Agent 通信 mTLS 证书
#   3. services/service-hub/certs/    数据流通调度中枢 mTLS 证书
#   4. console/mock-datasource/certs/ 数据源管理微服务 mTLS 证书与 SPKI 公钥固定文件
#   5. services/audit-log/certs/      合规存证审计微服务 mTLS 证书与 SPKI 公钥固定文件
#
# 有效期与特性：
#   - 默认有效期: 3650 天 (10年)，避免测试证书频繁过期导致测试中断
#   - 包含 X.509 V3 扩展: SAN (localhost, 127.0.0.1), serverAuth, clientAuth
#   - 包含 SPKI 客户端公钥导出文件 (.pub)，方便公钥固定 (Pinned Public Key) 测试
#   - 证书文件受 Git 追踪管理，开发与 CI 环境开箱即用
#
# 用法 / Usage:
#   bash ./scripts/dev/generate_all_test_certs.sh
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DAYS="${CERT_DAYS:-3650}"

if ! command -v openssl >/dev/null 2>&1; then
    echo "❌ 错误: 未检测到 openssl 命令行工具，请先安装 openssl。" >&2
    exit 1
fi

echo "============================================================================"
echo "🔐 正在为 PrivShield 全项目生成统一 mTLS 测试证书与公钥链 (有效期: ${DAYS} 天)..."
echo "============================================================================"

generate_cert_chain() {
    local target_dir="$1"
    local ca_cn="$2"
    local server_cn="$3"
    local client_cn="$4"

    mkdir -p "$target_dir"
    cd "$target_dir"

    echo ">> [1/4] 生成根 CA: $ca_cn ..."
    openssl genrsa -out ca.key 4096 2>/dev/null
    openssl req -x509 -new -nodes -key ca.key -sha256 -days "$DAYS" \
        -subj "/CN=${ca_cn}" \
        -out ca.crt 2>/dev/null

    echo ">> [2/4] 生成服务端证书: $server_cn (SAN: localhost, 127.0.0.1) ..."
    openssl genrsa -out server.key 2048 2>/dev/null
    openssl req -new -key server.key -subj "/CN=${server_cn}" -out server.csr 2>/dev/null
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
        -out server.crt -days "$DAYS" -sha256 -extfile server.ext 2>/dev/null

    echo ">> [3/4] 生成客户端证书: $client_cn ..."
    openssl genrsa -out client.key 2048 2>/dev/null
    openssl req -new -key client.key -subj "/CN=${client_cn}" -out client.csr 2>/dev/null
    cat > client.ext <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth
EOF
    openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
        -out client.crt -days "$DAYS" -sha256 -extfile client.ext 2>/dev/null

    echo ">> [4/4] 导出客户端 SPKI 公钥 (client.pub) ..."
    openssl rsa -in client.key -pubout -out client.pub 2>/dev/null

    rm -f server.csr client.csr server.ext client.ext ca.srl
    chmod 600 ./*.key
    chmod 644 ./*.crt ./*.pub 2>/dev/null || true

    echo "   ✅ 成功写入证书至: $target_dir"
}

# 1. 全局通用测试证书
echo -e "\n[1/5] 生成全局通用测试证书 (config/certs)..."
generate_cert_chain "$PROJECT_ROOT/config/certs" "PrivShield-Global-Test-CA" "localhost" "PrivShield-Test-Client"

# 2. Go BFF 代理网关测试证书
echo -e "\n[2/5] 生成 Console BFF-Go 测试证书 (console/engine-console/bff-go/certs)..."
generate_cert_chain "$PROJECT_ROOT/console/engine-console/bff-go/certs" "PrivShield-BFF-Test-CA" "localhost" "privacy-console-go-client"

# 3. Service-Hub 调度中枢测试证书
echo -e "\n[3/5] 生成 Service-Hub 调度中枢测试证书 (services/service-hub/certs)..."
generate_cert_chain "$PROJECT_ROOT/services/service-hub/certs" "service-hub-test-ca" "localhost" "service-hub-client"

# 4. Datasource-Mgr 数据源微服务测试证书
echo -e "\n[4/5] 生成 Datasource-Mgr 微服务测试证书 (console/mock-datasource/certs)..."
generate_cert_chain "$PROJECT_ROOT/console/mock-datasource/certs" "datasource-mgr-test-ca" "localhost" "datasource-mgr-client"

# 5. Audit-Log 审计存证微服务测试证书
echo -e "\n[5/5] 生成 Audit-Log 微服务测试证书 (services/audit-log/certs)..."
generate_cert_chain "$PROJECT_ROOT/services/audit-log/certs" "audit-log-test-ca" "localhost" "audit-log-client"

cd "$PROJECT_ROOT"
echo ""
echo "============================================================================"
echo "🎉 全套 mTLS 测试证书生成完毕，已全部就绪并保存在各服务 certs/ 目录！"
echo "============================================================================"
