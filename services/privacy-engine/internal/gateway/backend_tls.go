// Package gateway 提供东西向零信任 mTLS 回源 TLS 配置。
//
// 网关作为 mTLS Client，使用内部私有 CA 证书与后端 Agent
// 建立双向加密通道，防止内网流量被嗅探或篡改。
//
// 参考设计文档 §9.5。
package gateway

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// BuildBackendTLSConfig 构建东西向 mTLS 回源 TLS 配置。
//
// 参数：
//   - caCertPath：内部 CA 证书路径（用于验证后端 Agent 证书）
//   - clientCertPath：网关客户端证书路径（用于后端验证网关身份）
//   - clientKeyPath：网关客户端密钥路径
//
// 返回 tls.Config，默认 TLS 1.3，若 CA 尚未升级可降级至 TLS 1.2。
func BuildBackendTLSConfig(caCertPath, clientCertPath, clientKeyPath string) (*tls.Config, error) {
	// 加载 CA 证书
	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("read CA cert %s: %w", caCertPath, err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA cert %s", caCertPath)
	}

	// 加载客户端证书
	clientCert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// BuildBackendTLSConfigWithMinVersion 构建 mTLS 配置，支持指定最低 TLS 版本。
func BuildBackendTLSConfigWithMinVersion(caCertPath, clientCertPath, clientKeyPath string, minVersion uint16) (*tls.Config, error) {
	cfg, err := BuildBackendTLSConfig(caCertPath, clientCertPath, clientKeyPath)
	if err != nil {
		return nil, err
	}
	cfg.MinVersion = minVersion
	return cfg, nil
}

// BuildInsecureBackendTLSConfig 构建非 mTLS 的简单 TLS 配置（仅加密，不验证客户端证书）。
// 用于开发/测试环境或内网可信场景。
func BuildInsecureBackendTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
}
