// 出站传输安全测试：标准 TLS（httptest.NewTLSServer）与国密 TLCP（gmtls 服务端）双栈验证。

package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjfoc/gmsm/gmtls"
	"github.com/tjfoc/gmsm/sm2"
	tjfoctx509 "github.com/tjfoc/gmsm/x509"
)

// TestHTTPDefaultBehaviorUnchanged 验证未提供 TLS 配置时 http 基础地址行为不变。
func TestHTTPDefaultBehaviorUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Timeout: 5 * time.Second, Logger: newTestLogger()})
	got, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() over plain http should succeed, got %v", err)
	}
	if got["ok"] != true {
		t.Fatalf("unexpected response: %v", got)
	}
}

// TestHTTPSWithCAFile 验证注入根 CA 后 https 基础地址校验通过。
func TestHTTPSWithCAFile(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	caFile := writeServerCAFile(t, srv)

	tlsCfg, err := NewTLSConfig(caFile, false)
	if err != nil {
		t.Fatalf("NewTLSConfig() error: %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("NewTLSConfig() should not return nil when caFile provided")
	}

	c := New(Config{BaseURL: srv.URL, Timeout: 5 * time.Second, Logger: newTestLogger(), TLSConfig: tlsCfg})
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health() over https with CA should succeed, got %v", err)
	}
}

// TestHTTPSInsecureSkipVerify 验证 skip-verify 模式下 https 成功（不依赖 CA 文件）。
func TestHTTPSInsecureSkipVerify(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tlsCfg, err := NewTLSConfig("", true)
	if err != nil {
		t.Fatalf("NewTLSConfig() error: %v", err)
	}

	c := New(Config{BaseURL: srv.URL, Timeout: 5 * time.Second, Logger: newTestLogger(), TLSConfig: tlsCfg})
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health() over https with InsecureSkipVerify should succeed, got %v", err)
	}
}

// TestHTTPSWithoutCATrustFails 验证未注入 CA 时 https 自签证书按默认行为失败。
func TestHTTPSWithoutCATrustFails(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Timeout: 5 * time.Second, Logger: newTestLogger(), MaxRetries: 0})
	if _, err := c.Health(context.Background()); err == nil {
		t.Fatal("Health() over https without CA trust should fail")
	}
}

// TestTLSConfigFromEnvPrefix 验证机制/策略分离：由调用方传入前缀构建 TLS 配置。
func TestTLSConfigFromEnvPrefix(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	caFile := writeServerCAFile(t, srv)
	t.Setenv("TESTAGENT_TLS_CA_FILE", caFile)
	t.Setenv("TESTAGENT_TLS_INSECURE_SKIP_VERIFY", "")

	tlsCfg, err := TLSConfigFromEnv("TESTAGENT_")
	if err != nil || tlsCfg == nil {
		t.Fatalf("TLSConfigFromEnv() = %v, %v; want non-nil config", tlsCfg, err)
	}
	// 未配置任何变量时返回 nil（默认行为）。
	t.Setenv("TESTAGENT_TLS_CA_FILE", "")
	tlsCfg, err = TLSConfigFromEnv("TESTAGENT_")
	if err != nil || tlsCfg != nil {
		t.Fatalf("TLSConfigFromEnv() with empty env = %v, %v; want nil, nil", tlsCfg, err)
	}
	// 空前缀不读取任何环境变量（机制与策略分离）。
	if cfg, err := TLSConfigFromEnv(""); cfg != nil || err != nil {
		t.Fatalf("TLSConfigFromEnv(\"\") = %v, %v; want nil, nil", cfg, err)
	}
}

// TestTLCPRoundTrip 验证 tlcp:// 基础地址经 TLCP 传输完成国密握手并返回 200。
func TestTLCPRoundTrip(t *testing.T) {
	caFile, serverCertFile, serverKeyFile, encCertFile, encKeyFile := generateTLCPTestCerts(t)

	tlcpCfg, err := NewTLCPConfig(caFile, false)
	if err != nil || tlcpCfg == nil {
		t.Fatalf("NewTLCPConfig() = %v, %v", tlcpCfg, err)
	}

	// 启动 TLCP 国密服务端（签名+加密双证书）。
	srv := newTLCPServer(t, serverCertFile, serverKeyFile, encCertFile, encKeyFile)

	c := New(Config{
		BaseURL:    "tlcp://" + srv.Addr,
		Timeout:    5 * time.Second,
		Logger:     newTestLogger(),
		TLCPConfig: tlcpCfg,
	})
	got, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() over tlcp:// should succeed, got %v", err)
	}
	if got["ok"] != true {
		t.Fatalf("unexpected response: %v", got)
	}
}

// TestTLCPInsecureSkipVerify 验证 TLCP skip-verify 模式不依赖 CA 文件。
func TestTLCPInsecureSkipVerify(t *testing.T) {
	_, serverCertFile, serverKeyFile, encCertFile, encKeyFile := generateTLCPTestCerts(t)
	tlcpCfg, err := NewTLCPConfig("", true)
	if err != nil || tlcpCfg == nil {
		t.Fatalf("NewTLCPConfig() = %v, %v", tlcpCfg, err)
	}
	srv := newTLCPServer(t, serverCertFile, serverKeyFile, encCertFile, encKeyFile)

	c := New(Config{
		BaseURL:    "tlcp://" + srv.Addr,
		Timeout:    5 * time.Second,
		Logger:     newTestLogger(),
		TLCPConfig: tlcpCfg,
	})
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health() over tlcp:// with InsecureSkipVerify should succeed, got %v", err)
	}
}

// TestTLCPSchemeWithoutConfigFails 验证未装配 TLCP 传输时 tlcp:// 显式失败（无静默兜底）。
func TestTLCPSchemeWithoutConfigFails(t *testing.T) {
	c := New(Config{BaseURL: "tlcp://127.0.0.1:8079", Timeout: 2 * time.Second, Logger: newTestLogger(), MaxRetries: 0})
	if _, err := c.Health(context.Background()); err == nil {
		t.Fatal("Health() over tlcp:// without TLCPConfig should fail")
	}
}

// TestTLCPConfigFromEnvPrefix 验证 TLCP env 前缀构建与未配置时返回 nil。
func TestTLCPConfigFromEnvPrefix(t *testing.T) {
	t.Setenv("TESTAGENT_TLCP_CA_FILE", "")
	t.Setenv("TESTAGENT_TLCP_INSECURE_SKIP_VERIFY", "")
	if cfg, err := TLCPConfigFromEnv("TESTAGENT_"); cfg != nil || err != nil {
		t.Fatalf("TLCPConfigFromEnv() with empty env = %v, %v; want nil, nil", cfg, err)
	}
	if cfg, err := TLCPConfigFromEnv(""); cfg != nil || err != nil {
		t.Fatalf("TLCPConfigFromEnv(\"\") = %v, %v; want nil, nil", cfg, err)
	}
}

// writeServerCAFile 把 httptest TLS 服务端证书写入临时 PEM 文件，返回路径。
func writeServerCAFile(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	// httptest 服务端证书为自签，直接作为信任根。
	pemData := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	path := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(path, pemData, 0o600); err != nil {
		t.Fatalf("write CA file: %v", err)
	}
	return path
}

// generateTLCPTestCerts 生成 SM2 自签 CA + 服务端签名/加密双证书，返回 (caFile, certFile, keyFile, encCertFile, encKeyFile)。
func generateTLCPTestCerts(t *testing.T) (string, string, string, string, string) {
	t.Helper()
	dir := t.TempDir()

	caKey, err := sm2.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate SM2 CA key: %v", err)
	}
	caTmpl := &tjfoctx509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "tlcp-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              tjfoctx509.KeyUsageCertSign | tjfoctx509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		// 显式声明 SM2-SM3 签名算法：缺省时 gmsm 自签 CA 在 Verify 中会出现 "SM2 verification failure"。
		SignatureAlgorithm: tjfoctx509.SM2WithSM3,
	}
	caDER, err := tjfoctx509.CreateCertificate(caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create SM2 CA cert: %v", err)
	}
	caCert, _ := tjfoctx509.ParseCertificate(caDER)

	issueServerCert := func(serial int64, commonName string, encipherment bool) (certDER, keyDER []byte) {
		srvKey, err := sm2.GenerateKey(nil)
		if err != nil {
			t.Fatalf("generate SM2 server key: %v", err)
		}
		keyUsage := tjfoctx509.KeyUsageDigitalSignature
		if encipherment {
			// TLCP 加密证书要求 KeyUsageDataEncipherment/KeyEncipherment/KeyAgreement（gmtls 握手强校验）。
			keyUsage = tjfoctx509.KeyUsageKeyAgreement | tjfoctx509.KeyUsageDataEncipherment
		}
		srvTmpl := &tjfoctx509.Certificate{
			SerialNumber:       big.NewInt(serial),
			Subject:            pkix.Name{CommonName: commonName},
			NotBefore:          time.Now().Add(-time.Hour),
			NotAfter:           time.Now().Add(24 * time.Hour),
			KeyUsage:           keyUsage,
			ExtKeyUsage:        []tjfoctx509.ExtKeyUsage{tjfoctx509.ExtKeyUsageServerAuth},
			DNSNames:           []string{"localhost"},
			IPAddresses:        []net.IP{net.ParseIP("127.0.0.1")},
			SignatureAlgorithm: tjfoctx509.SM2WithSM3,
		}
		der, err := tjfoctx509.CreateCertificate(srvTmpl, caCert, &srvKey.PublicKey, caKey)
		if err != nil {
			t.Fatalf("create SM2 server cert: %v", err)
		}
		derKey, err := tjfoctx509.MarshalSm2PrivateKey(srvKey, nil)
		if err != nil {
			t.Fatalf("marshal SM2 private key: %v", err)
		}
		return der, derKey
	}

	signDER, signKeyDER := issueServerCert(2, "localhost", false)
	encDER, encKeyDER := issueServerCert(3, "localhost-enc", true)

	caFile := filepath.Join(dir, "ca.crt")
	writePEM(t, caFile, "CERTIFICATE", caDER)
	certFile := filepath.Join(dir, "server.crt")
	writePEM(t, certFile, "CERTIFICATE", signDER)
	keyFile := filepath.Join(dir, "server.key")
	writePEM(t, keyFile, "PRIVATE KEY", signKeyDER)
	encCertFile := filepath.Join(dir, "server-enc.crt")
	writePEM(t, encCertFile, "CERTIFICATE", encDER)
	encKeyFile := filepath.Join(dir, "server-enc.key")
	writePEM(t, encKeyFile, "PRIVATE KEY", encKeyDER)
	return caFile, certFile, keyFile, encCertFile, encKeyFile
}

// newTLCPServer 启动最小 TLCP 国密服务端（签名+加密双证书），返回包装了 Addr 的服务器句柄。
func newTLCPServer(t *testing.T, certFile, keyFile, encCertFile, encKeyFile string) *tlcpServer {
	t.Helper()
	loadCert := func(certPath, keyPath string) (*tjfoctx509.Certificate, *sm2.PrivateKey) {
		certPEM, err := os.ReadFile(certPath)
		if err != nil {
			t.Fatalf("read TLCP cert %s: %v", certPath, err)
		}
		block, _ := pem.Decode(certPEM)
		if block == nil {
			t.Fatalf("decode TLCP cert PEM %s failed", certPath)
		}
		cert, err := tjfoctx509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("parse TLCP cert %s: %v", certPath, err)
		}
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			t.Fatalf("read TLCP key %s: %v", keyPath, err)
		}
		key, err := tjfoctx509.ReadPrivateKeyFromPem(keyPEM, nil)
		if err != nil {
			t.Fatalf("parse TLCP key %s: %v", keyPath, err)
		}
		return cert, key
	}
	signCert, signKey := loadCert(certFile, keyFile)
	encCert, encKey := loadCert(encCertFile, encKeyFile)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gmLn := gmtls.NewListener(ln, &gmtls.Config{
		Certificates: []gmtls.Certificate{
			{Certificate: [][]byte{signCert.Raw}, PrivateKey: signKey, Leaf: signCert},
			{Certificate: [][]byte{encCert.Raw}, PrivateKey: encKey, Leaf: encCert},
		},
		GMSupport: &gmtls.GMSupport{WorkMode: gmtls.ModeGMSSLOnly},
	})

	s := &tlcpServer{Addr: ln.Addr().String(), ln: gmLn}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	s.httpSrv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		_ = s.httpSrv.Serve(gmLn)
	}()
	t.Cleanup(func() { _ = s.httpSrv.Close() })
	return s
}

type tlcpServer struct {
	Addr    string
	ln      net.Listener
	httpSrv *http.Server
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// 确保标准库 tls/x509 引用不被裁剪（测试辅助以显式类型使用）。
var (
	_ = tls.VersionTLS13
	_ = x509.NewCertPool
)
