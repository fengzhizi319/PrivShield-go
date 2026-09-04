package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildBackendTLSConfigMissingCA(t *testing.T) {
	_, err := BuildBackendTLSConfig("/nonexistent/ca.crt", "/nonexistent/client.crt", "/nonexistent/client.key")
	if err == nil {
		t.Error("expected error for missing CA cert")
	}
}

func TestBuildBackendTLSConfigInvalidCA(t *testing.T) {
	// 创建临时无效 CA 文件
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, []byte("not a cert"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := BuildBackendTLSConfig(caPath, "/nonexistent/client.crt", "/nonexistent/client.key")
	if err == nil {
		t.Error("expected error for invalid CA cert")
	}
}

func TestBuildInsecureBackendTLSConfig(t *testing.T) {
	cfg := BuildInsecureBackendTLSConfig()
	if cfg == nil {
		t.Fatal("expected non-nil tls.Config")
	}
	if cfg.MinVersion != 0x0303 { // TLS 1.2
		t.Errorf("MinVersion = %x, want TLS 1.2 (0x0303)", cfg.MinVersion)
	}
}
