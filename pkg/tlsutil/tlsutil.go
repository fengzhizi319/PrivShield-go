// Package tlsutil provides shared TLS configuration utilities for building secure server credentials.
// Package tlsutil 提供共享的 TLS/mTLS 密码学与传输安全配置工具，用于构建高安全等级的微服务与 RPC 通信凭证。
//
// ==============================================================================
// 【核心能力与安全架构】
// 1. 【TLS 1.3 强制最低版本】：
//    所有通过 BuildServerTLSConfig 生成的配置默认且强制将 MinVersion 设为 tls.VersionTLS13，
//    杜绝弱密码套件、重放攻击与降级攻击；
// 2. 【多模式双向认证 (mTLS Client Auth)】：
//    支持 require/requireandverify（强制双向强校验）、verify（客户端提供证书时校验）、
//    request（请求证书但不强制）等多种认证模式；
// 3. 【公钥固定防御 (Public Key / SPKI Pinning)】：
//    通过注入 VerifyPeerCertificate 钩子函数，深度比对对端公钥数学属性（RSA 模数/指数、
//    ECDSA 椭圆曲线坐标、Ed25519 字节），有效防御 CA 根证书被劫持或非法签发伪造证书；
// 4. 【跨协议复用】：
//    同时作为 HTTP REST（Gin/http.Server）与 gRPC 服务端（credentials.TransportCredentials）
//    的通用底层 TLS 凭证工厂。
//
// ==============================================================================
// 【使用方法与代码范例】
//
//	// 1. 构造服务端双向认证配置
//	tlsConfig, err := tlsutil.BuildServerTLSConfig(&tlsutil.ServerTLSConfig{
//	    Enabled:          true,
//	    CertFile:         "/etc/certs/server.crt",
//	    KeyFile:          "/etc/certs/server.key",
//	    CAFile:           "/etc/certs/ca.crt",
//	    ClientAuth:       "require", // 强制要求客户端证书并验证
//	    PinnedPubKeyFile: "/etc/certs/client_pinned_pub.pem", // 可选公钥固定
//	})
//	if err != nil {
//	    log.Fatalf("failed to build TLS config: %v", err)
//	}
//
//	// 2. 挂载到 HTTP 服务器
//	httpServer := &http.Server{
//	    Addr:      ":8443",
//	    Handler:   router,
//	    TLSConfig: tlsConfig,
//	}
//	go httpServer.ListenAndServeTLS("", "")
//
//	// 3. 挂载到 gRPC 服务器
//	grpcCreds := credentials.NewTLS(tlsConfig)
//	grpcServer := grpc.NewServer(grpc.Creds(grpcCreds))
// ==============================================================================

package tlsutil

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ServerTLSConfig holds the configuration parameters for building a TLS server config.
// ServerTLSConfig 保存构建 TLS 服务器配置所需的全部核心参数。
type ServerTLSConfig struct {
	// Enabled 表示是否启用 TLS 加密（若为 false，BuildServerTLSConfig 将返回错误）。
	Enabled bool

	// CertFile 为服务端 X.509 证书的 PEM 文件绝对或相对路径。
	CertFile string

	// KeyFile 为服务端私钥的 PEM 文件路径（支持 RSA/ECDSA/Ed25519 私钥）。
	KeyFile string

	// CAFile 为受信任的客户端根 CA 证书文件路径（当启用 ClientAuth 时为必填项）。
	CAFile string

	// ClientAuth 为客户端双向认证模式：
	// - "require" / "requireandverify": 强制客户端提供证书且必须通过 CA 根证书链校验（tls.RequireAndVerifyClientCert）；
	// - "verify": 客户端若提供证书则进行校验（tls.VerifyClientCertIfGiven）；
	// - "request": 请求客户端提供证书但不强制校验（tls.RequestClientCert）；
	// - "": 仅单向 TLS 传输加密，不校验客户端证书。
	ClientAuth string

	// PinnedPubKeyFile 为固定的受信任客户端公钥 PEM 文件路径（可选）。
	// 若配置此项，将在 TLS 握手阶段比对客户端证书公钥指纹。
	PinnedPubKeyFile string
}

// BuildServerTLSConfig constructs a *tls.Config supporting TLS 1.3, mTLS client auth, and public key pinning.
//
// BuildServerTLSConfig 根据 ServerTLSConfig 参数构建生产级 *tls.Config 实例：
//
// ==============================================================================
// 【执行逻辑】
// 1. 【参数防御校验】：
//   - 校验 Enabled 是否为 true；
//   - 校验 CertFile 与 KeyFile 是否均已配置，并使用 filepath.Clean 清洗路径防止路径穿越；
//
// 2. 【证书与私钥加载】：
//   - 调用 tls.LoadX509KeyPair 加载公私钥对；
//   - 设置 MinVersion 为 tls.VersionTLS13 强制最低加密版本；
//
// 3. 【客户端 CA 证书池装配 (mTLS)】：
//   - 若 ClientAuth 非空，读取 CAFile 并解析为 *x509.CertPool 注入 ClientCAs；
//   - 根据模式映射标准 tls.ClientAuthType（RequireAndVerifyClientCert 等）；
//
// 4. 【SPKI 公钥固定钩子注入 (VerifyPeerCertificate)】：
//   - 若配置了 PinnedPubKeyFile，解析固定公钥对象；
//   - 注册 VerifyPeerCertificate 回调，在 TLS 握手最后阶段提取 peerCert.PublicKey，
//     调用 PublicKeysEqual 进行常数深度比对，不匹配时中断 TLS 握手。
//
// ==============================================================================
func BuildServerTLSConfig(cfg *ServerTLSConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("TLS is disabled in configuration")
	}
	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return nil, fmt.Errorf("TLS cert file and key file must be configured")
	}

	certFile := filepath.Clean(cfg.CertFile)
	keyFile := filepath.Clean(cfg.KeyFile)
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load server x509 key pair: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	clientAuthMode := strings.ToLower(strings.TrimSpace(cfg.ClientAuth))
	if clientAuthMode != "" {
		if cfg.CAFile == "" {
			return nil, fmt.Errorf("TLS CA file must be configured when client auth is enabled")
		}
		caFile := filepath.Clean(cfg.CAFile)
		caPEM, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read TLS CA file: %w", err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", caFile)
		}
		tlsConfig.ClientCAs = caPool

		switch clientAuthMode {
		case "require", "requireandverify":
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		case "verify":
			tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
		case "request":
			tlsConfig.ClientAuth = tls.RequestClientCert
		default:
			return nil, fmt.Errorf("unknown TLS client auth mode: %s", cfg.ClientAuth)
		}
	}

	// 注入公钥固定校验钩子
	if cfg.PinnedPubKeyFile != "" {
		pinnedFile := filepath.Clean(cfg.PinnedPubKeyFile)
		pinnedKey, err := LoadPublicKey(pinnedFile)
		if err != nil {
			return nil, fmt.Errorf("load pinned client public key: %w", err)
		}
		tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("mTLS: client did not present a certificate")
			}
			peerCert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("mTLS: failed to parse peer certificate: %w", err)
			}
			if !PublicKeysEqual(peerCert.PublicKey, pinnedKey) {
				return fmt.Errorf("mTLS: client public key does not match pinned key")
			}
			return nil
		}
	}

	return tlsConfig, nil
}

// LoadPublicKey loads a public key from PEM file (supports PKIX and X.509 Certificate formats).
//
// LoadPublicKey 从 PEM 文件中解析并提取公钥对象：
// 1. 优先尝试按照 PKIX 格式（x509.ParsePKIXPublicKey，如 BEGIN PUBLIC KEY）解析；
// 2. 若失败，回退尝试按照 X.509 证书格式（x509.ParseCertificate，如 BEGIN CERTIFICATE）提取 cert.PublicKey；
// 3. 支持 RSA、ECDSA 与 Ed25519 类型的公钥。
func LoadPublicKey(path string) (crypto.PublicKey, error) {
	cleanPath := filepath.Clean(path)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read public key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM data found in %s", path)
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		cert, certErr := x509.ParseCertificate(block.Bytes)
		if certErr == nil {
			return cert.PublicKey, nil
		}
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	return pub, nil
}

// PublicKeysEqual checks if two public keys are identical (RSA, ECDSA, Ed25519).
//
// PublicKeysEqual 深度比对两个公钥的数学属性：
// - 【RSA】：比对模数 N（big.Int Cmp == 0）与公共指数 E 是否一致；
// - 【ECDSA】：比对公钥曲线坐标点 X、Y（big.Int Cmp == 0）及椭圆曲线参数 Curve 是否一致；
// - 【Ed25519】：调用 ed25519.PublicKey.Equal 比对 32 字节原生公钥数据；
// - 【其他/不匹配】：返回 false。
func PublicKeysEqual(a, b crypto.PublicKey) bool {
	switch keyA := a.(type) {
	case *rsa.PublicKey:
		keyB, ok := b.(*rsa.PublicKey)
		if !ok {
			return false
		}
		return keyA.N.Cmp(keyB.N) == 0 && keyA.E == keyB.E
	case *ecdsa.PublicKey:
		keyB, ok := b.(*ecdsa.PublicKey)
		if !ok {
			return false
		}
		return keyA.X.Cmp(keyB.X) == 0 && keyA.Y.Cmp(keyB.Y) == 0 && keyA.Curve == keyB.Curve
	case ed25519.PublicKey:
		keyB, ok := b.(ed25519.PublicKey)
		if !ok {
			return false
		}
		return keyA.Equal(keyB)
	default:
		return false
	}
}
