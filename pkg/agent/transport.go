// 出站传输安全（标准 TLS / 国密 TLCP）配置构造。
//
// 机制与策略分离：本文件只提供「由显式参数或显式传入的 env 前缀构建 *tls.Config /
// *gmtls.Config」的机制，自身不硬编码任何特定环境变量前缀，也不维护次级兼容兜底。
// 调用方（service-hub / audit-log 等）用各自服务约定的前缀（如 PRIVACY_AGENT_）
// 调用 *FromEnv，或直接用 NewTLSConfig / NewTLCPConfig 传入显式值。

package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"

	"github.com/tjfoc/gmsm/gmtls"
	tjfoctx509 "github.com/tjfoc/gmsm/x509"
)

// NewTLSConfig 由显式参数构造标准 TLS 客户端信任配置。
// NewTLSConfig builds a standard TLS client trust config from explicit arguments.
//
// caFile 指向校验上游 agent 服务端证书的根 CA PEM 文件；insecureSkipVerify 为 true 时
// 跳过服务端证书校验（仅开发/演练）。两者都未提供时返回 (nil, nil)，
// 表示不覆盖默认行为（系统根 CA 池校验）。
func NewTLSConfig(caFile string, insecureSkipVerify bool) (*tls.Config, error) {
	if caFile == "" {
		if insecureSkipVerify {
			return &tls.Config{InsecureSkipVerify: true}, nil
		}
		return nil, nil
	}
	pemData, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("agent TLS: read CA file %s: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("agent TLS: failed to parse CA certificate: %s", caFile)
	}
	return &tls.Config{RootCAs: pool, InsecureSkipVerify: insecureSkipVerify}, nil
}

// TLSConfigFromEnv 以调用方显式传入的前缀从环境变量构建标准 TLS 客户端信任配置。
// TLSConfigFromEnv builds the TLS trust config from env vars with the caller-provided prefix:
//
//	<prefix>TLS_CA_FILE                  根 CA PEM 文件路径（校验上游 agent 服务端证书）
//	<prefix>TLS_INSECURE_SKIP_VERIFY     跳过服务端证书校验（true/1/yes/on，仅开发/演练）
//
// 前缀为空或两个变量均未设置时返回 (nil, nil)（保持默认行为）。
func TLSConfigFromEnv(prefix string) (*tls.Config, error) {
	if prefix == "" {
		return nil, nil
	}
	caFile := os.Getenv(prefix + "TLS_CA_FILE")
	insecure := envBool(prefix+"TLS_INSECURE_SKIP_VERIFY", false)
	if caFile == "" && !insecure {
		return nil, nil
	}
	return NewTLSConfig(caFile, insecure)
}

// NewTLCPConfig 由显式参数构造国密 TLCP 客户端配置（GM/T 0024，gmtls 国密套件）。
// NewTLCPConfig builds a TLCP (national cipher) client config from explicit arguments.
//
// caFile 指向校验上游 agent 服务端 SM2 证书链的根 CA PEM 文件；insecureSkipVerify 为 true 时
// 跳过校验（仅开发/演练）。两者都未提供时返回 (nil, nil)，表示未启用 TLCP 传输。
func NewTLCPConfig(caFile string, insecureSkipVerify bool) (*gmtls.Config, error) {
	if caFile == "" && !insecureSkipVerify {
		return nil, nil
	}
	cfg := &gmtls.Config{
		GMSupport: &gmtls.GMSupport{
			WorkMode: gmtls.ModeGMSSLOnly,
		},
		InsecureSkipVerify: insecureSkipVerify,
	}
	if caFile != "" {
		pemData, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("agent TLCP: read CA file %s: %w", caFile, err)
		}
		pool := tjfoctx509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("agent TLCP: failed to parse CA certificate: %s", caFile)
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

// TLCPConfigFromEnv 以调用方显式传入的前缀从环境变量构建 TLCP 客户端配置：
//
//	<prefix>TLCP_CA_FILE                  SM2 根 CA PEM 文件路径
//	<prefix>TLCP_INSECURE_SKIP_VERIFY     跳过服务端证书校验（true/1/yes/on，仅开发/演练）
//
// 前缀为空或两个变量均未设置时返回 (nil, nil)（未启用 TLCP 传输）。
func TLCPConfigFromEnv(prefix string) (*gmtls.Config, error) {
	if prefix == "" {
		return nil, nil
	}
	caFile := os.Getenv(prefix + "TLCP_CA_FILE")
	insecure := envBool(prefix+"TLCP_INSECURE_SKIP_VERIFY", false)
	if caFile == "" && !insecure {
		return nil, nil
	}
	return NewTLCPConfig(caFile, insecure)
}

// tlcpSchemePrefix 标记上游 agent 基础地址使用国密 TLCP 传输的 URL scheme。
// http.Client 不识别该 scheme；Client 在装配 TLCP 传输时将其归一为 https://，
// 实际国密握手由 tlcpDialer 完成。未装配 TLCP 传输时保留原 scheme，
// 请求将在构造阶段以 "unsupported protocol scheme" 失败（显式报错，不做静默兜底）。
const tlcpSchemePrefix = "tlcp://"

// hasTLCPScheme 判断基础地址列表中是否存在 TLCP 传输节点。
func hasTLCPScheme(urls []string) bool {
	for _, u := range urls {
		if len(u) >= len(tlcpSchemePrefix) && u[:len(tlcpSchemePrefix)] == tlcpSchemePrefix {
			return true
		}
	}
	return false
}

// requestURL 拼接 endpoint 与 path，并在 TLCP 传输已启用时将 tlcp:// 归一为 https://。
func requestURL(endpoint, path string, tlcpEnabled bool) string {
	if tlcpEnabled {
		return "https://" + endpoint[len(tlcpSchemePrefix):] + path
	}
	return endpoint + path
}

// tlcpDialer 构造 http.Transport.DialTLSContext：对每次拨号执行 GM/T 0024 国密握手。
// ServerName 缺省时以拨号地址的主机名填充（逐连接 Clone，避免污染共享配置）。
func tlcpDialer(cfg *gmtls.Config) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := net.Dialer{}
		conn, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		cc := cfg.Clone()
		if cc.ServerName == "" {
			host, _, splitErr := net.SplitHostPort(addr)
			if splitErr == nil {
				cc.ServerName = host
			}
		}
		tlsConn := gmtls.Client(conn, cc)
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return nil, fmt.Errorf("agent TLCP handshake with %s: %w", addr, err)
		}
		return tlsConn, nil
	}
}

// envBool 读取布尔环境变量，支持 true/1/yes/on（大小写不敏感）。
func envBool(key string, defaultValue bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	switch v {
	case "true", "1", "yes", "on", "TRUE", "True":
		return true
	default:
		return false
	}
}
