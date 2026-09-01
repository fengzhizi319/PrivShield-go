package agent

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/config"
)

// testCerts 保存测试用证书/密钥的临时文件路径。
type testCerts struct {
	caFile     string
	clientCert string
	clientKey  string
}

// genTestCerts 在临时目录生成一套自签名 CA 与客户端证书，供 mTLS 测试使用。
// 返回各 PEM 文件路径，测试结束后由 t.Cleanup 自动清理临时目录。
func genTestCerts(t *testing.T) testCerts {
	t.Helper()
	dir := t.TempDir()

	// 生成 CA 私钥与自签名 CA 证书
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

	// 生成客户端私钥与由 CA 签发的客户端证书
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	clientTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTmpl, caTmpl, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create client cert: %v", err)
	}

	// 写入 PEM 文件
	caFile := filepath.Join(dir, "ca.crt")
	writePEM(t, caFile, "CERTIFICATE", caDER)

	clientCertFile := filepath.Join(dir, "client.crt")
	writePEM(t, clientCertFile, "CERTIFICATE", clientDER)

	clientKeyFile := filepath.Join(dir, "client.key")
	writePEM(t, clientKeyFile, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(clientKey))

	return testCerts{caFile: caFile, clientCert: clientCertFile, clientKey: clientKeyFile}
}

// writePEM 将 DER 字节以指定 type 编码为 PEM 并写入文件。
func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		t.Fatalf("encode PEM %s: %v", path, err)
	}
}

// TestBuildTransportCredentialsInsecure 验证 TLS 未启用时返回非安全凭证且无错误。
func TestBuildTransportCredentialsInsecure(t *testing.T) {
	cfg := &config.Config{AgentTLSEnabled: false}
	creds, err := buildTransportCredentials(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
	// 非安全凭证的 Info().SecurityProtocol 应为 "insecure"
	if got := creds.Info().SecurityProtocol; got != "insecure" {
		t.Errorf("security protocol = %q, want insecure", got)
	}
}

// TestBuildTransportCredentialsMissingCA 验证启用 TLS 但未提供 CA 时报错。
func TestBuildTransportCredentialsMissingCA(t *testing.T) {
	cfg := &config.Config{AgentTLSEnabled: true, AgentTLSCAFile: ""}
	if _, err := buildTransportCredentials(cfg); err == nil {
		t.Fatal("expected error when CA file missing")
	}
}

// TestBuildTransportCredentialsBadCAPath 验证 CA 文件不存在时报错。
func TestBuildTransportCredentialsBadCAPath(t *testing.T) {
	cfg := &config.Config{AgentTLSEnabled: true, AgentTLSCAFile: "/nonexistent/ca.crt"}
	if _, err := buildTransportCredentials(cfg); err == nil {
		t.Fatal("expected error when CA file unreadable")
	}
}

// TestBuildTransportCredentialsInvalidCA 验证 CA 文件内容非合法 PEM 时报错。
func TestBuildTransportCredentialsInvalidCA(t *testing.T) {
	dir := t.TempDir()
	badCA := filepath.Join(dir, "bad.crt")
	if err := os.WriteFile(badCA, []byte("not a pem"), 0o600); err != nil {
		t.Fatalf("write bad CA: %v", err)
	}
	cfg := &config.Config{AgentTLSEnabled: true, AgentTLSCAFile: badCA}
	if _, err := buildTransportCredentials(cfg); err == nil {
		t.Fatal("expected error when CA PEM invalid")
	}
}

// TestBuildTransportCredentialsTLSWithClientCert 验证完整 mTLS 配置成功构造 TLS 凭证。
func TestBuildTransportCredentialsTLSWithClientCert(t *testing.T) {
	certs := genTestCerts(t)
	cfg := &config.Config{
		AgentTLSEnabled:    true,
		AgentTLSCAFile:     certs.caFile,
		AgentTLSCertFile:   certs.clientCert,
		AgentTLSKeyFile:    certs.clientKey,
		AgentTLSServerName: "localhost",
	}
	creds, err := buildTransportCredentials(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := creds.Info().SecurityProtocol; got != "tls" {
		t.Errorf("security protocol = %q, want tls", got)
	}
}

// TestBuildTransportCredentialsTLSOnlyCA 验证仅提供 CA（单向 TLS）也能成功构造凭证。
func TestBuildTransportCredentialsTLSOnlyCA(t *testing.T) {
	certs := genTestCerts(t)
	cfg := &config.Config{
		AgentTLSEnabled: true,
		AgentTLSCAFile:  certs.caFile,
	}
	creds, err := buildTransportCredentials(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := creds.Info().SecurityProtocol; got != "tls" {
		t.Errorf("security protocol = %q, want tls", got)
	}
}

// TestBuildTransportCredentialsCertWithoutKey 验证只提供证书不提供私钥时报错。
func TestBuildTransportCredentialsCertWithoutKey(t *testing.T) {
	certs := genTestCerts(t)
	cfg := &config.Config{
		AgentTLSEnabled:  true,
		AgentTLSCAFile:   certs.caFile,
		AgentTLSCertFile: certs.clientCert,
		AgentTLSKeyFile:  "",
	}
	if _, err := buildTransportCredentials(cfg); err == nil {
		t.Fatal("expected error when cert provided without key")
	}
}

// TestBuildTransportCredentialsBadKeyPair 验证证书与私钥不匹配/损坏时报错。
func TestBuildTransportCredentialsBadKeyPair(t *testing.T) {
	certs := genTestCerts(t)
	dir := t.TempDir()
	badKey := filepath.Join(dir, "bad.key")
	if err := os.WriteFile(badKey, []byte("garbage"), 0o600); err != nil {
		t.Fatalf("write bad key: %v", err)
	}
	cfg := &config.Config{
		AgentTLSEnabled:  true,
		AgentTLSCAFile:   certs.caFile,
		AgentTLSCertFile: certs.clientCert,
		AgentTLSKeyFile:  badKey,
	}
	if _, err := buildTransportCredentials(cfg); err == nil {
		t.Fatal("expected error when key pair invalid")
	}
}

// TestWithTraceAttachesMetadata verifies WithTrace attaches x-request-id and x-trace-id
// to gRPC outgoing metadata when a non-empty trace ID is provided.
func TestWithTraceAttachesMetadata(t *testing.T) {
	c := &Client{cfg: &config.Config{}}
	ctx := context.Background()
	traceID := "req-1234567890-abcdef"

	result := c.WithTrace(ctx, traceID)

	md, ok := metadata.FromOutgoingContext(result)
	if !ok {
		t.Fatal("expected outgoing metadata to be present")
	}
	// 校验 x-request-id 双头下发
	reqIDs := md.Get("x-request-id")
	if len(reqIDs) != 1 || reqIDs[0] != traceID {
		t.Errorf("x-request-id = %v, want [%s]", reqIDs, traceID)
	}
	// 校验 x-trace-id 双头下发
	traceIDs := md.Get("x-trace-id")
	if len(traceIDs) != 1 || traceIDs[0] != traceID {
		t.Errorf("x-trace-id = %v, want [%s]", traceIDs, traceID)
	}
}

// TestWithTraceEmptyIDNoOp verifies WithTrace returns the original context unchanged
// when the trace ID is empty, avoiding unnecessary metadata allocation.
func TestWithTraceEmptyIDNoOp(t *testing.T) {
	c := &Client{cfg: &config.Config{}}
	ctx := context.Background()

	result := c.WithTrace(ctx, "")

	// 空 trace ID 时不应附加任何 metadata
	_, ok := metadata.FromOutgoingContext(result)
	if ok {
		t.Error("expected no outgoing metadata when trace ID is empty")
	}
}

// TestWithTraceAndAuthCombined verifies WithTrace and WithAuth can be chained
// to attach both trace and auth metadata to the same context.
func TestWithTraceAndAuthCombined(t *testing.T) {
	c := &Client{cfg: &config.Config{AgentAPIKey: "test-key-123"}}
	ctx := context.Background()
	traceID := "req-combined-test"

	// 模拟 handler 中的链式调用：WithTrace(WithAuth(ctx), traceID)
	result := c.WithTrace(c.WithAuth(ctx), traceID)

	md, ok := metadata.FromOutgoingContext(result)
	if !ok {
		t.Fatal("expected outgoing metadata to be present")
	}
	// 校验追踪元数据
	reqIDs := md.Get("x-request-id")
	if len(reqIDs) != 1 || reqIDs[0] != traceID {
		t.Errorf("x-request-id = %v, want [%s]", reqIDs, traceID)
	}
	// 校验认证元数据
	authIDs := md.Get("authorization")
	if len(authIDs) != 1 || authIDs[0] != "Bearer test-key-123" {
		t.Errorf("authorization = %v, want [Bearer test-key-123]", authIDs)
	}
}
