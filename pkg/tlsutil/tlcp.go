// Package tlsutil provides shared TLS configuration utilities for building secure server credentials.
//
// 国密 TLCP (GB/T 38636-2020) 支持：
//  Go 标准库 crypto/tls 不原生支持 SM2/SM3/SM4 密码套件。本文件基于 tjfoc/gmsm/gmtls
//  提供 TLCP 服务端配置构造，需配套 SM2 签名证书与加密证书。
//
//  TLCP 与 TLS 在证书体系上的差异：
//   - TLCP 需要「签名证书链」与「加密证书链」两套 SM2 证书；
//   - 单向 TLCP 至少配置签名证书；双向 TLCP 还需客户端 SM2 证书。
//   - 生产部署可在 Ingress/Envoy 层做国密 TLS 终结，本文件提供应用层替代方案。

package tlsutil

import (
	"encoding/pem"
	"fmt"
	"net"
	"os"

	"github.com/tjfoc/gmsm/gmtls"
	"github.com/tjfoc/gmsm/sm2"
	tjfoctx509 "github.com/tjfoc/gmsm/x509"
)

// TLCPConfig holds the parameters for building a TLCP (national cipher) server config.
// TLCP 服务端配置参数，支持单向与双向国密 TLS。
type TLCPConfig struct {
	// Enabled 是否启用 TLCP。
	Enabled bool

	// SignCertFile 服务端 SM2 签名证书 PEM 路径。
	SignCertFile string

	// SignKeyFile 服务端 SM2 签名证书私钥 PEM 路径。
	SignKeyFile string

	// EncCertFile 服务端 SM2 加密证书 PEM 路径（双向/双证书场景）。
	EncCertFile string

	// EncKeyFile 服务端 SM2 加密证书私钥 PEM 路径。
	EncKeyFile string

	// ClientCAFile 客户端根 CA 证书 PEM 路径（启用双向认证时必填）。
	ClientCAFile string

	// ClientAuth 是否要求客户端证书："require" / "requireandverify" 开启双向。
	ClientAuth string
}

// BuildTLCPConfig 构造国密 TLCP 服务端配置。
// 若未启用或缺少签名证书，返回 nil（调用方应回退到标准 TLS）。
func BuildTLCPConfig(cfg *TLCPConfig) (*gmtls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.SignCertFile == "" || cfg.SignKeyFile == "" {
		return nil, fmt.Errorf("TLCP: sign cert file and sign key file are required")
	}

	signCertPEM, err := os.ReadFile(cfg.SignCertFile)
	if err != nil {
		return nil, fmt.Errorf("TLCP: read sign cert: %w", err)
	}
	signKeyPEM, err := os.ReadFile(cfg.SignKeyFile)
	if err != nil {
		return nil, fmt.Errorf("TLCP: read sign key: %w", err)
	}

	signCert, signKey, err := loadSM2CertFromPEM(signCertPEM, signKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("TLCP: load sign cert/key: %w", err)
	}

	var encCert *tjfoctx509.Certificate
	var encKey *sm2.PrivateKey
	if cfg.EncCertFile != "" && cfg.EncKeyFile != "" {
		encCertPEM, err := os.ReadFile(cfg.EncCertFile)
		if err != nil {
			return nil, fmt.Errorf("TLCP: read enc cert: %w", err)
		}
		encKeyPEM, err := os.ReadFile(cfg.EncKeyFile)
		if err != nil {
			return nil, fmt.Errorf("TLCP: read enc key: %w", err)
		}
		encCert, encKey, err = loadSM2CertFromPEM(encCertPEM, encKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("TLCP: load enc cert/key: %w", err)
		}
	}

	certs := []gmtls.Certificate{
		{
			Certificate: [][]byte{signCert.Raw},
			PrivateKey:  signKey,
			Leaf:        signCert,
		},
	}
	if encCert != nil && encKey != nil {
		certs = append(certs, gmtls.Certificate{
			Certificate: [][]byte{encCert.Raw},
			PrivateKey:  encKey,
			Leaf:        encCert,
		})
	}

	config := &gmtls.Config{
		Certificates: certs,
		GMSupport: &gmtls.GMSupport{
			WorkMode: gmtls.ModeGMSSLOnly,
		},
	}

	clientAuthMode := cfg.ClientAuth
	if clientAuthMode != "" {
		if cfg.ClientCAFile == "" {
			return nil, fmt.Errorf("TLCP: client CA file is required when client auth is enabled")
		}
		caPEM, err := os.ReadFile(cfg.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("TLCP: read client CA file: %w", err)
		}
		caPool := tjfoctx509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("TLCP: failed to parse client CA certificate")
		}
		config.ClientCAs = caPool
		switch clientAuthMode {
		case "require", "requireandverify", "require_and_verify":
			config.ClientAuth = gmtls.RequireAndVerifyClientCert
		case "verify", "verify_if_given":
			config.ClientAuth = gmtls.VerifyClientCertIfGiven
		case "request":
			config.ClientAuth = gmtls.RequestClientCert
		default:
			return nil, fmt.Errorf("TLCP: unknown client auth mode: %s", clientAuthMode)
		}
	}

	return config, nil
}

// IsTLCPEnabled 判断环境是否显式要求启用国密 TLCP。
// 必须由调用方传入要检查的环境变量名（如 "AGENT_TLS_NATIONAL_CIPHER"）。
// 若 envKey 为空或未配置，则返回 false。
func IsTLCPEnabled(envKey string) bool {
	if envKey == "" {
		return false
	}
	return getEnvBool(envKey, false)
}

// TLCPConfigFromEnv 根据调用方显式传入的前缀从环境变量构建 TLCPConfig。
// 机制与策略完全分离：基础包自身不硬编码任何特定环境变量前缀，不维护次级兼容兜底。
func TLCPConfigFromEnv(prefix string) *TLCPConfig {
	if prefix == "" {
		return &TLCPConfig{}
	}
	return &TLCPConfig{
		Enabled:      getEnvBool(prefix+"TLS_NATIONAL_CIPHER", false),
		SignCertFile: getEnvString(prefix+"TLCP_SIGN_CERT_FILE", ""),
		SignKeyFile:  getEnvString(prefix+"TLCP_SIGN_KEY_FILE", ""),
		EncCertFile:  getEnvString(prefix+"TLCP_ENC_CERT_FILE", ""),
		EncKeyFile:   getEnvString(prefix+"TLCP_ENC_KEY_FILE", ""),
		ClientCAFile: getEnvString(prefix+"TLCP_CLIENT_CA_FILE", ""),
		ClientAuth:   getEnvString(prefix+"TLCP_CLIENT_AUTH", ""),
	}
}

// NewTLCPListener 创建 TLCP 国密 TLS 监听。
// 调用方可直接替换 net.Listen + tls 路径。
func NewTLCPListener(network, address string, config *gmtls.Config) (net.Listener, error) {
	return gmtls.Listen(network, address, config)
}

// getEnvString 读取环境变量，不存在返回默认值。
func getEnvString(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// getEnvBool 读取布尔环境变量，支持 true/1/yes/on。
func getEnvBool(key string, defaultValue bool) bool {
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

// loadSM2CertFromPEM 从 PEM 数据解析 SM2 证书与私钥。
func loadSM2CertFromPEM(certPEM, keyPEM []byte) (*tjfoctx509.Certificate, *sm2.PrivateKey, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode SM2 certificate PEM")
	}
	cert, err := tjfoctx509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse SM2 certificate: %w", err)
	}

	key, err := tjfoctx509.ReadPrivateKeyFromPem(keyPEM, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("parse SM2 private key: %w", err)
	}
	return cert, key, nil
}
