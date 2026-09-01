package config

import (
	"errors"
	"os"
	"strings"
	"testing"

	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
)

func TestConfigDefaults(t *testing.T) {
	os.Unsetenv("AUDIT_LOG_HOST")
	os.Unsetenv("AUDIT_LOG_PORT")
	os.Unsetenv("AUDIT_LOG_GRPC_HOST")
	os.Unsetenv("AUDIT_LOG_GRPC_PORT")
	os.Unsetenv("PRIVACY_AGENT_REST_HOST")
	os.Unsetenv("PRIVACY_REST_PORT")
	os.Unsetenv("AUDIT_LOG_TLS_ENABLED")

	cfg := Load()

	if cfg.Host != "127.0.0.1" {
		t.Errorf("expected default Host 127.0.0.1, got %s", cfg.Host)
	}
	if cfg.Port != 8084 {
		t.Errorf("expected default Port 8084, got %d", cfg.Port)
	}
	if cfg.GRPCHost != "127.0.0.1" {
		t.Errorf("expected default GRPCHost 127.0.0.1, got %s", cfg.GRPCHost)
	}
	if cfg.GRPCPort != 50054 {
		t.Errorf("expected default GRPCPort 50054, got %d", cfg.GRPCPort)
	}
	if cfg.TLSEnabled {
		t.Errorf("expected default TLSEnabled to be false")
	}
	if cfg.Address() != "127.0.0.1:8084" {
		t.Errorf("expected Address() 127.0.0.1:8084, got %s", cfg.Address())
	}
	if cfg.GRPCAddress() != "127.0.0.1:50054" {
		t.Errorf("expected GRPCAddress() 127.0.0.1:50054, got %s", cfg.GRPCAddress())
	}
	if cfg.AgentBaseURL() != "http://127.0.0.1:8079" {
		t.Errorf("expected AgentBaseURL() http://127.0.0.1:8079, got %s", cfg.AgentBaseURL())
	}
}

func TestConfigCustomEnv(t *testing.T) {
	t.Setenv("AUDIT_LOG_HOST", "0.0.0.0")
	t.Setenv("AUDIT_LOG_PORT", "9084")
	t.Setenv("AUDIT_LOG_GRPC_HOST", "0.0.0.0")
	t.Setenv("AUDIT_LOG_GRPC_PORT", "60054")
	t.Setenv("AUDIT_LOG_TLS_ENABLED", "true")
	t.Setenv("AUDIT_LOG_TLS_CERT_FILE", "/tmp/cert.pem")
	t.Setenv("AUDIT_LOG_TLS_KEY_FILE", "/tmp/key.pem")
	t.Setenv("AUDIT_LOG_TLS_CA_FILE", "/tmp/ca.pem")
	t.Setenv("AUDIT_LOG_TLS_CLIENT_AUTH", "require")
	t.Setenv("AUDIT_LOG_TLS_PINNED_PUBKEY_FILE", "/tmp/pinned.pem")
	t.Setenv("AUDIT_LOG_API_KEY", "secret-audit-key")
	t.Setenv("AUDIT_LOG_CORS_ORIGINS", "http://localhost:3000,https://audit.example.com")
	t.Setenv("AUDIT_LOG_DB_PATH", "/tmp/audit_test.db")
	t.Setenv("AUDIT_LOG_LOG_FORMAT", "text")
	t.Setenv("AUDIT_LOG_LOG_LEVEL", "debug")
	t.Setenv("PRIVACY_AGENT_URLS", "http://agent1:8079,http://agent2:8079")

	cfg := Load()

	if cfg.Host != "0.0.0.0" || cfg.Port != 9084 {
		t.Errorf("custom address mismatch: %s:%d", cfg.Host, cfg.Port)
	}
	if cfg.GRPCHost != "0.0.0.0" || cfg.GRPCPort != 60054 {
		t.Errorf("custom grpc address mismatch: %s:%d", cfg.GRPCHost, cfg.GRPCPort)
	}
	if !cfg.TLSEnabled {
		t.Errorf("expected TLSEnabled true")
	}
	if cfg.TLSCertFile != "/tmp/cert.pem" || cfg.TLSKeyFile != "/tmp/key.pem" {
		t.Errorf("custom tls cert/key mismatch")
	}
	if cfg.TLSCAFile != "/tmp/ca.pem" || cfg.TLSClientAuth != "require" {
		t.Errorf("custom tls ca/client auth mismatch")
	}
	if cfg.TLSPinnedPubKeyFile != "/tmp/pinned.pem" {
		t.Errorf("custom pinned pub key mismatch")
	}
	if cfg.APIKey != "secret-audit-key" {
		t.Errorf("custom api key mismatch: %s", cfg.APIKey)
	}
	if len(cfg.CORSOrigins) != 2 {
		t.Errorf("expected 2 CORS origins, got %d", len(cfg.CORSOrigins))
	}
	if cfg.DBPath != "/tmp/audit_test.db" || cfg.LogFormat != "text" || cfg.LogLevel != "debug" {
		t.Errorf("custom db/log mismatch")
	}
	urls := cfg.AgentBaseURLs()
	if len(urls) != 2 || urls[0] != "http://agent1:8079" {
		t.Errorf("custom AgentBaseURLs() mismatch: %v", urls)
	}
}

func TestFailClosedDefaults(t *testing.T) {
	t.Setenv("AUDIT_LOG_RETENTION_DAYS", "")
	t.Setenv("AUDIT_LOG_STRICT_STORAGE", "")
	t.Setenv("STRICT_STORAGE", "")

	cfg := Load()
	if cfg.RetentionDays != 0 {
		t.Fatalf("retention must default to 0 (never delete evidence), got %d", cfg.RetentionDays)
	}
	if !cfg.StrictStorage {
		t.Fatal("strict storage must default to true (no silent fallback to memory)")
	}
	// 默认监听 127.0.0.1 的本地形态允许无密钥启动。
	if err := cfg.Validate(); err != nil {
		t.Fatalf("loopback default config must validate, got %v", err)
	}
}

func TestFailClosedRejections(t *testing.T) {
	t.Run("remote bind requires api key", func(t *testing.T) {
		cfg := Load()
		cfg.Host, cfg.GRPCHost = "0.0.0.0", "0.0.0.0"
		cfg.APIKey, cfg.EncryptionKey = "", ""
		if err := cfg.Validate(); !errors.Is(err, pkgconfig.ErrAPIKeyRequired) {
			t.Fatalf("expected ErrAPIKeyRequired, got %v", err)
		}
	})

	t.Run("remote bind requires encryption key", func(t *testing.T) {
		cfg := Load()
		cfg.Host, cfg.GRPCHost = "0.0.0.0", "0.0.0.0"
		cfg.APIKey, cfg.EncryptionKey = "key", ""
		if err := cfg.Validate(); !errors.Is(err, pkgconfig.ErrEncryptionKeyRequired) {
			t.Fatalf("expected ErrEncryptionKeyRequired, got %v", err)
		}
	})

	t.Run("grpc tls requires cn whitelist", func(t *testing.T) {
		cfg := Load()
		cfg.APIKey, cfg.EncryptionKey = "key", "enc-key"
		cfg.TLSEnabled = true
		cfg.TLSCertFile, cfg.TLSKeyFile = writeTempPEM(t), writeTempPEM(t)
		cfg.MTLSWhitelistFile = ""
		if err := cfg.Validate(); !errors.Is(err, pkgconfig.ErrMTLSWhitelistRequired) {
			t.Fatalf("expected ErrMTLSWhitelistRequired, got %v", err)
		}
	})

	t.Run("require tls without tls aborts", func(t *testing.T) {
		cfg := Load()
		cfg.APIKey, cfg.EncryptionKey = "key", "enc-key"
		cfg.RequireTLS, cfg.TLSEnabled = true, false
		if err := cfg.Validate(); !errors.Is(err, pkgconfig.ErrTLSRequired) {
			t.Fatalf("expected ErrTLSRequired, got %v", err)
		}
	})

	t.Run("retention below three-year floor aborts", func(t *testing.T) {
		cfg := Load()
		cfg.APIKey, cfg.EncryptionKey = "key", "enc-key"
		cfg.RetentionDays = 90
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "1095") {
			t.Fatalf("expected retention floor error, got %v", err)
		}
		cfg.RetentionDays = 1095
		if err := cfg.Validate(); err != nil {
			t.Fatalf("1095-day retention must validate, got %v", err)
		}
		cfg.RetentionDays, cfg.ArchiveDir = 1095, ""
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "ARCHIVE_DIR") {
			t.Fatalf("expected archive-dir error when deleting, got %v", err)
		}
	})
}

// TestReaderAPIKeyIsLoaded 只读核验员 Key 的编排变量名必须有对应读取点（P2-1 门禁的正向断言）。
func TestReaderAPIKeyIsLoaded(t *testing.T) {
	t.Setenv("AUDIT_LOG_READER_API_KEY", "reader-key")
	if got := Load().ReaderAPIKey; got != "reader-key" {
		t.Fatalf("ReaderAPIKey = %q, want %q", got, "reader-key")
	}
}

// TestReaderKeyMustDifferFromWriteKey 两把 Key 相同等于没做权责分离（白名单形同虚设），
// 必须拒绝启动而不是静默降级；为空表示显式不启用该角色，保持存量行为。
func TestReaderKeyMustDifferFromWriteKey(t *testing.T) {
	cfg := Load()
	cfg.Host, cfg.GRPCHost = "127.0.0.1", "127.0.0.1"
	cfg.APIKey, cfg.ReaderAPIKey = "same-key", "same-key"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "READER_API_KEY") {
		t.Fatalf("expected reader-key rejection, got %v", err)
	}
	cfg.ReaderAPIKey = "reader-key"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("distinct reader key must validate, got %v", err)
	}
	cfg.ReaderAPIKey = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("empty reader key (role disabled) must validate, got %v", err)
	}
}

func writeTempPEM(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "pem-*.crt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("placeholder"); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}
