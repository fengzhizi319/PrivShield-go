// Package config_test contains unit tests for the configuration loader of datasource-mgr.
// Package config_test 包含 datasource-mgr 模块运行时配置加载器的单元测试套件。
package config

import (
	"errors"
	"os"
	"testing"

	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
)

// TestConfigDefaults verifies that Load() correctly falls back to default settings when no env vars are set.
// TestConfigDefaults 测试默认配置加载逻辑：
// 1. 显式清除相关的环境变量，确保测试环境纯净；
// 2. 调用 Load() 实例化配置对象；
// 3. 断言各个字段（HTTP Host/Port, gRPC Host/Port, TLS 启用状态, 拼接地址等）均符合预设的安全默认值。
func TestConfigDefaults(t *testing.T) {
	// 清理可能存在的主机与端口环境变量
	os.Unsetenv("DATASOURCE_MGR_HOST")
	os.Unsetenv("DATASOURCE_MGR_PORT")
	os.Unsetenv("DATASOURCE_MGR_GRPC_HOST")
	os.Unsetenv("DATASOURCE_MGR_GRPC_PORT")
	os.Unsetenv("DATASOURCE_MGR_TLS_ENABLED")

	cfg := Load()

	// 验证 HTTP 默认监听参数
	if cfg.Host != "127.0.0.1" {
		t.Errorf("expected default Host 127.0.0.1, got %s", cfg.Host)
	}
	if cfg.Port != 8083 {
		t.Errorf("expected default Port 8083, got %d", cfg.Port)
	}

	// 验证 gRPC 默认监听参数
	if cfg.GRPCHost != "127.0.0.1" {
		t.Errorf("expected default GRPCHost 127.0.0.1, got %s", cfg.GRPCHost)
	}
	if cfg.GRPCPort != 50053 {
		t.Errorf("expected default GRPCPort 50053, got %d", cfg.GRPCPort)
	}

	// 验证默认关闭 TLS
	if cfg.TLSEnabled {
		t.Errorf("expected default TLSEnabled to be false")
	}

	// 验证拼接地址格式
	if cfg.Address() != "127.0.0.1:8083" {
		t.Errorf("expected Address() 127.0.0.1:8083, got %s", cfg.Address())
	}
	if cfg.GRPCAddress() != "127.0.0.1:50053" {
		t.Errorf("expected GRPCAddress() 127.0.0.1:50053, got %s", cfg.GRPCAddress())
	}
}

// TestConfigCustomEnv verifies that Load() correctly parses custom environment variables.
// TestConfigCustomEnv 测试自定义环境变量覆盖逻辑：
// 1. 使用 t.Setenv 注入自定义的 HTTP、gRPC、mTLS 证书链、公钥固定、API Key 及跨域 CORS 等环境变量；
// 2. 调用 Load() 执行配置加载；
// 3. 逐项比对 Config 对象字段是否被自定义环境变量正确覆盖。
func TestConfigCustomEnv(t *testing.T) {
	// 注入自定义网络与安全配置环境变量
	t.Setenv("DATASOURCE_MGR_HOST", "0.0.0.0")
	t.Setenv("DATASOURCE_MGR_PORT", "9083")
	t.Setenv("DATASOURCE_MGR_GRPC_HOST", "0.0.0.0")
	t.Setenv("DATASOURCE_MGR_GRPC_PORT", "60053")
	t.Setenv("DATASOURCE_MGR_TLS_ENABLED", "true")
	t.Setenv("DATASOURCE_MGR_TLS_CERT_FILE", "/tmp/cert.pem")
	t.Setenv("DATASOURCE_MGR_TLS_KEY_FILE", "/tmp/key.pem")
	t.Setenv("DATASOURCE_MGR_TLS_CA_FILE", "/tmp/ca.pem")
	t.Setenv("DATASOURCE_MGR_TLS_CLIENT_AUTH", "require")
	t.Setenv("DATASOURCE_MGR_TLS_PINNED_PUBKEY_FILE", "/tmp/pinned.pem")
	t.Setenv("DATASOURCE_MGR_API_KEY", "secret-key")
	t.Setenv("DATASOURCE_MGR_CORS_ORIGINS", "http://localhost:3000,https://example.com")
	t.Setenv("DATASOURCE_MGR_LOG_FORMAT", "text")
	t.Setenv("DATASOURCE_MGR_LOG_LEVEL", "debug")

	cfg := Load()

	// 验证 HTTP 自定义监听地址
	if cfg.Host != "0.0.0.0" || cfg.Port != 9083 {
		t.Errorf("custom address mismatch: %s:%d", cfg.Host, cfg.Port)
	}

	// 验证 gRPC 自定义监听地址
	if cfg.GRPCHost != "0.0.0.0" || cfg.GRPCPort != 60053 {
		t.Errorf("custom grpc address mismatch: %s:%d", cfg.GRPCHost, cfg.GRPCPort)
	}

	// 验证 TLS 启用状态与证书链参数
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

	// 验证 API 鉴权密钥与 CORS 配置
	if cfg.APIKey != "secret-key" {
		t.Errorf("custom api key mismatch: %s", cfg.APIKey)
	}
	if len(cfg.CORSOrigins) != 2 {
		t.Errorf("expected 2 CORS origins, got %d", len(cfg.CORSOrigins))
	}
}

// TestFailClosedDefaults 断言 P0-4 与 P0-1 的默认态：
// 1. 严格存储模式默认即为 true（禁止静默降级回退），显式置空两个变量仍为 true；
// 2. 默认 127.0.0.1 本地环回形态允许无 API Key 启动（本地开发不受门禁影响）。
func TestFailClosedDefaults(t *testing.T) {
	t.Setenv("DATASOURCE_MGR_STRICT_STORAGE", "")
	t.Setenv("STRICT_STORAGE", "")
	t.Setenv("DATASOURCE_MGR_HOST", "127.0.0.1")
	t.Setenv("DATASOURCE_MGR_GRPC_HOST", "127.0.0.1")
	t.Setenv("DATASOURCE_MGR_API_KEY", "")
	t.Setenv("DATASOURCE_MGR_TLS_ENABLED", "")
	t.Setenv("DATASOURCE_MGR_REQUIRE_TLS", "")

	cfg := Load()
	if !cfg.StrictStorage {
		t.Fatal("strict storage must default to true (no silent fallback to volatile state)")
	}
	if cfg.RequireTLS {
		t.Fatal("RequireTLS must default to false so local development keeps working")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("loopback default config must validate, got %v", err)
	}
}

// TestFailClosedRejections 断言 P0-1 零信任门禁的四条启动红线。
func TestFailClosedRejections(t *testing.T) {
	t.Run("strict storage can be disabled explicitly", func(t *testing.T) {
		t.Setenv("STRICT_STORAGE", "true")
		t.Setenv("DATASOURCE_MGR_STRICT_STORAGE", "false")
		if cfg := Load(); cfg.StrictStorage {
			t.Fatal("DATASOURCE_MGR_STRICT_STORAGE=false must win over the global STRICT_STORAGE")
		}
	})

	t.Run("remote bind requires api key", func(t *testing.T) {
		t.Setenv("DATASOURCE_MGR_HOST", "0.0.0.0")
		t.Setenv("DATASOURCE_MGR_GRPC_HOST", "127.0.0.1")
		t.Setenv("DATASOURCE_MGR_API_KEY", "")
		t.Setenv("DATASOURCE_MGR_TLS_ENABLED", "false")

		cfg := Load()
		if err := cfg.Validate(); !errors.Is(err, pkgconfig.ErrAPIKeyRequired) {
			t.Fatalf("expected ErrAPIKeyRequired, got %v", err)
		}

		// 配置入站密钥后同一形态必须放行。
		cfg.APIKey = "secret-key"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("remote bind with api key must validate, got %v", err)
		}
	})

	t.Run("require tls without tls aborts", func(t *testing.T) {
		cfg := Load()
		cfg.Host, cfg.GRPCHost = "127.0.0.1", "127.0.0.1"
		cfg.APIKey = ""
		cfg.RequireTLS, cfg.TLSEnabled = true, false
		if err := cfg.Validate(); !errors.Is(err, pkgconfig.ErrTLSRequired) {
			t.Fatalf("expected ErrTLSRequired, got %v", err)
		}
	})

	t.Run("grpc tls requires cn whitelist", func(t *testing.T) {
		t.Setenv("PRIVACY_AUTH_MTLS_WHITELIST_FILE", "")

		cfg := Load()
		cfg.Host, cfg.GRPCHost = "127.0.0.1", "127.0.0.1"
		cfg.APIKey = "secret-key"
		cfg.RequireTLS = false
		cfg.TLSEnabled = true
		cfg.TLSCertFile, cfg.TLSKeyFile = writeTempPEM(t), writeTempPEM(t)
		cfg.MTLSWhitelistFile = ""
		if err := cfg.Validate(); !errors.Is(err, pkgconfig.ErrMTLSWhitelistRequired) {
			t.Fatalf("expected ErrMTLSWhitelistRequired, got %v", err)
		}

		// 注入 CN 白名单文件后同一形态必须放行。
		cfg.MTLSWhitelistFile = writeTempPEM(t)
		if err := cfg.Validate(); err != nil {
			t.Fatalf("tls + grpc with cn whitelist file must validate, got %v", err)
		}
	})
}

// writeTempPEM 生成一个占位证书/私钥文件，仅用于满足 Validate() 的文件可达性检查。
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
