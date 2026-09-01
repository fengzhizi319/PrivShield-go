package handlers

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"
)

// testCerts holds paths to test certificate files.
// testCerts 保存测试证书文件路径集合。
type testCerts struct {
	caFile     string // CA 根证书文件路径
	caKeyFile  string // CA 私钥文件路径（用于签发额外测试证书）
	serverCert string // 服务端证书文件路径
	serverKey  string // 服务端私钥文件路径
	clientCert string // 客户端证书文件路径
	clientKey  string // 客户端私钥文件路径
	clientPub  string // 客户端公钥 PEM 文件路径
}

// genTestCerts generates a complete test certificate chain in a temp directory.
// genTestCerts 在临时目录中动态生成完整的测试证书链（CA 根证书 ➔ 服务端证书 ➔ 客户端证书 ➔ 客户端公钥）。
func genTestCerts(t *testing.T) testCerts {
	t.Helper()
	dir := t.TempDir()

	// 1. 生成 CA 私钥和自签名 CA 证书
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caFile := writePEM(t, dir, "ca.crt", "CERTIFICATE", caDER)
	caKeyFile := writePEM(t, dir, "ca.key", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(caKey))

	// 解析 CA 用于签发后续证书
	caCert, _ := x509.ParseCertificate(caDER)

	// 2. 生成由 CA 签发的服务端证书
	serverKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	serverTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	serverDER, _ := x509.CreateCertificate(rand.Reader, serverTmpl, caCert, &serverKey.PublicKey, caKey)
	serverCert := writePEM(t, dir, "server.crt", "CERTIFICATE", serverDER)
	serverKeyFile := writePEM(t, dir, "server.key", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey))

	// 3. 生成由 CA 签发的客户端证书
	clientKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	clientTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, _ := x509.CreateCertificate(rand.Reader, clientTmpl, caCert, &clientKey.PublicKey, caKey)
	clientCert := writePEM(t, dir, "client.crt", "CERTIFICATE", clientDER)
	clientKeyFile := writePEM(t, dir, "client.key", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(clientKey))

	// 4. 提取客户端公钥并保存为 PEM
	clientPubDER, _ := x509.MarshalPKIXPublicKey(&clientKey.PublicKey)
	clientPub := writePEM(t, dir, "client.pub", "PUBLIC KEY", clientPubDER)

	return testCerts{
		caFile:     caFile,
		caKeyFile:  caKeyFile,
		serverCert: serverCert,
		serverKey:  serverKeyFile,
		clientCert: clientCert,
		clientKey:  clientKeyFile,
		clientPub:  clientPub,
	}
}

// writePEM writes a DER-encoded block as PEM to a file.
// writePEM 将 DER 编码的字节切片以指定 blockType 写入 PEM 格式文件。
func writePEM(t *testing.T, dir, name, blockType string, der []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		t.Fatalf("encode PEM %s: %v", path, err)
	}
	return path
}

// loadCAKey loads the CA private key for signing additional test certificates.
func loadCAKey(t *testing.T, caKeyFile string) *rsa.PrivateKey {
	t.Helper()
	data, err := os.ReadFile(caKeyFile)
	if err != nil {
		t.Fatalf("read CA key: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("decode CA key PEM failed")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA key: %v", err)
	}
	return key
}

// startTLSServer creates and starts an HTTPS test server with the given TLS config.
// Returns the server and its listener address.
func startTLSServer(t *testing.T, tlsConfig *tls.Config) (*http.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:      "127.0.0.1:0",
		Handler:   mux,
		TLSConfig: tlsConfig,
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	tlsLn := tls.NewListener(ln, tlsConfig)
	go func() {
		_ = srv.Serve(tlsLn)
	}()
	t.Cleanup(func() { _ = srv.Close() })

	return srv, ln.Addr().String()
}

// trustedClientPool creates an http.Client that trusts the test CA.
func trustedClientPool(t *testing.T, caFile string) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatalf("read CA file: %v", err)
	}
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to parse CA certificate")
	}
	return pool
}

// ─────────────────────────────────────────────────────────────
// Tests / 测试用例
// ─────────────────────────────────────────────────────────────

// TestHTTPServer_TLSDisabled verifies HTTP server works without TLS.
// TestHTTPServer_TLSDisabled 验证未启用 TLS 时 HTTP 服务器正常工作。
func TestHTTPServer_TLSDisabled(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:    "127.0.0.1:0",
		Handler: mux,
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/health")
	if err != nil {
		t.Fatalf("HTTP GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

// TestHTTPServer_TLSOnly verifies HTTPS server works with TLS enabled (no client cert required).
// TestHTTPServer_TLSOnly 验证启用 TLS 但未要求客户端证书时 HTTPS 服务器正常工作。
func TestHTTPServer_TLSOnly(t *testing.T) {
	certs := genTestCerts(t)

	tlsCfg := &tlsutil.ServerTLSConfig{
		Enabled:  true,
		CertFile: certs.serverCert,
		KeyFile:  certs.serverKey,
	}
	tlsConfig, err := tlsutil.BuildServerTLSConfig(tlsCfg)
	if err != nil {
		t.Fatalf("build TLS config: %v", err)
	}

	_, addr := startTLSServer(t, tlsConfig)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: trustedClientPool(t, certs.caFile),
			},
		},
	}

	resp, err := client.Get("https://" + addr + "/health")
	if err != nil {
		t.Fatalf("HTTPS GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

// TestHTTPServer_MTLSRequire verifies HTTPS server requires valid client certificate.
// TestHTTPServer_MTLSRequire 验证启用 mTLS (require 模式) 时服务器强制要求客户端提供有效证书。
func TestHTTPServer_MTLSRequire(t *testing.T) {
	certs := genTestCerts(t)

	tlsCfg := &tlsutil.ServerTLSConfig{
		Enabled:    true,
		CertFile:   certs.serverCert,
		KeyFile:    certs.serverKey,
		CAFile:     certs.caFile,
		ClientAuth: "require",
	}
	tlsConfig, err := tlsutil.BuildServerTLSConfig(tlsCfg)
	if err != nil {
		t.Fatalf("build TLS config: %v", err)
	}

	_, addr := startTLSServer(t, tlsConfig)
	pool := trustedClientPool(t, certs.caFile)

	t.Run("WithoutClientCert_ShouldFail", func(t *testing.T) {
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool},
			},
		}
		_, err := client.Get("https://" + addr + "/health")
		if err == nil {
			t.Error("expected error when client cert is missing, got nil")
		}
	})

	t.Run("WithValidClientCert_ShouldSucceed", func(t *testing.T) {
		clientCert, err := tls.LoadX509KeyPair(certs.clientCert, certs.clientKey)
		if err != nil {
			t.Fatalf("load client cert: %v", err)
		}
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs:      pool,
					Certificates: []tls.Certificate{clientCert},
				},
			},
		}
		resp, err := client.Get("https://" + addr + "/health")
		if err != nil {
			t.Fatalf("HTTPS GET with client cert failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}
	})
}

// TestHTTPServer_MTLSWithPinnedKey verifies HTTPS server with public key pinning.
// TestHTTPServer_MTLSWithPinnedKey 验证启用公钥固定时：匹配公钥的客户端通过，不匹配的拒绝。
func TestHTTPServer_MTLSWithPinnedKey(t *testing.T) {
	certs := genTestCerts(t)

	tlsCfg := &tlsutil.ServerTLSConfig{
		Enabled:          true,
		CertFile:         certs.serverCert,
		KeyFile:          certs.serverKey,
		CAFile:           certs.caFile,
		ClientAuth:       "require",
		PinnedPubKeyFile: certs.clientPub,
	}
	tlsConfig, err := tlsutil.BuildServerTLSConfig(tlsCfg)
	if err != nil {
		t.Fatalf("build TLS config: %v", err)
	}

	_, addr := startTLSServer(t, tlsConfig)
	pool := trustedClientPool(t, certs.caFile)

	t.Run("WithMatchingClientCert_ShouldSucceed", func(t *testing.T) {
		clientCert, err := tls.LoadX509KeyPair(certs.clientCert, certs.clientKey)
		if err != nil {
			t.Fatalf("load client cert: %v", err)
		}
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs:      pool,
					Certificates: []tls.Certificate{clientCert},
				},
			},
		}
		resp, err := client.Get("https://" + addr + "/health")
		if err != nil {
			t.Fatalf("HTTPS GET with pinned client cert failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}
	})

	t.Run("WithDifferentClientCert_ShouldFail", func(t *testing.T) {
		// 生成另一个由同一 CA 签发的客户端证书（公钥不同）
		caKey := loadCAKey(t, certs.caKeyFile)
		caCertDER, _ := os.ReadFile(certs.caFile)
		caBlock, _ := pem.Decode(caCertDER)
		caCert, _ := x509.ParseCertificate(caBlock.Bytes)

		otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
		otherTmpl := &x509.Certificate{
			SerialNumber: big.NewInt(99),
			Subject:      pkix.Name{CommonName: "other-client"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		}
		otherDER, err := x509.CreateCertificate(rand.Reader, otherTmpl, caCert, &otherKey.PublicKey, caKey)
		if err != nil {
			t.Fatalf("create other client cert: %v", err)
		}

		dir := t.TempDir()
		otherCertFile := writePEM(t, dir, "other.crt", "CERTIFICATE", otherDER)
		otherKeyFile := writePEM(t, dir, "other.key", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(otherKey))

		otherClientCert, err := tls.LoadX509KeyPair(otherCertFile, otherKeyFile)
		if err != nil {
			t.Fatalf("load other client cert: %v", err)
		}

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs:      pool,
					Certificates: []tls.Certificate{otherClientCert},
				},
			},
		}
		_, err = client.Get("https://" + addr + "/health")
		if err == nil {
			t.Error("expected error when client public key doesn't match pinned key, got nil")
		}
	})
}

// TestHTTPServer_MTLSVerifyMode verifies "verify" client auth mode (optional client cert).
// TestHTTPServer_MTLSVerifyMode 验证 "verify" 模式下客户端证书可选：有证书时校验，无证书时也放行。
func TestHTTPServer_MTLSVerifyMode(t *testing.T) {
	certs := genTestCerts(t)

	tlsCfg := &tlsutil.ServerTLSConfig{
		Enabled:    true,
		CertFile:   certs.serverCert,
		KeyFile:    certs.serverKey,
		CAFile:     certs.caFile,
		ClientAuth: "verify", // VerifyClientCertIfGiven
	}
	tlsConfig, err := tlsutil.BuildServerTLSConfig(tlsCfg)
	if err != nil {
		t.Fatalf("build TLS config: %v", err)
	}

	_, addr := startTLSServer(t, tlsConfig)
	pool := trustedClientPool(t, certs.caFile)

	t.Run("WithoutClientCert_ShouldSucceed", func(t *testing.T) {
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool},
			},
		}
		resp, err := client.Get("https://" + addr + "/health")
		if err != nil {
			t.Fatalf("HTTPS GET without client cert failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}
	})

	t.Run("WithValidClientCert_ShouldSucceed", func(t *testing.T) {
		clientCert, err := tls.LoadX509KeyPair(certs.clientCert, certs.clientKey)
		if err != nil {
			t.Fatalf("load client cert: %v", err)
		}
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs:      pool,
					Certificates: []tls.Certificate{clientCert},
				},
			},
		}
		resp, err := client.Get("https://" + addr + "/health")
		if err != nil {
			t.Fatalf("HTTPS GET with client cert failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}
	})
}
