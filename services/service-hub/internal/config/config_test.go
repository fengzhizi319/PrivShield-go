package config

import (
	"errors"
	"os"
	"testing"

	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
)

// TestLoadDefaults tests that Load() populates expected default values when no environment variables are set.
// TestLoadDefaults 测试在未设置任何环境变量时，Load() 能正确赋予安全的默认配置值。
func TestLoadDefaults(t *testing.T) {
	// 清理可能存在的环境变量，确保测试默认值不受外部干扰
	os.Unsetenv("SERVICE_HUB_HOST")
	os.Unsetenv("SERVICE_HUB_PORT")
	os.Unsetenv("PRIVACY_AGENT_REST_HOST")
	os.Unsetenv("PRIVACY_REST_PORT")
	os.Unsetenv("PRIVACY_AGENT_API_KEY")

	// 执行配置加载
	cfg := Load()

	// 校验默认 HTTP 主机、端口与上游 Agent 地址
	if cfg.Host != "127.0.0.1" {
		t.Errorf("expected host=127.0.0.1, got %s", cfg.Host)
	}
	if cfg.Port != 8082 {
		t.Errorf("expected port=8082, got %d", cfg.Port)
	}
	if cfg.AgentRESTHost != "127.0.0.1" {
		t.Errorf("expected agent host=127.0.0.1, got %s", cfg.AgentRESTHost)
	}
	if cfg.AgentRESTPort != 8079 {
		t.Errorf("expected agent port=8079, got %d", cfg.AgentRESTPort)
	}
	if cfg.AgentAPIKey != "" {
		t.Errorf("expected empty API key, got %s", cfg.AgentAPIKey)
	}
}

// TestLoadFromEnv tests that custom environment variables correctly override the default configuration.
// TestLoadFromEnv 测试当注入自定义环境变量时，Load() 能够精确读取并覆盖默认配置。
func TestLoadFromEnv(t *testing.T) {
	os.Setenv("SERVICE_HUB_HOST", "0.0.0.0")
	os.Setenv("SERVICE_HUB_PORT", "9090")
	os.Setenv("PRIVACY_AGENT_REST_HOST", "10.0.0.1")
	os.Setenv("PRIVACY_REST_PORT", "9079")
	os.Setenv("PRIVACY_AGENT_API_KEY", "test-key")
	defer func() {
		os.Unsetenv("SERVICE_HUB_HOST")
		os.Unsetenv("SERVICE_HUB_PORT")
		os.Unsetenv("PRIVACY_AGENT_REST_HOST")
		os.Unsetenv("PRIVACY_REST_PORT")
		os.Unsetenv("PRIVACY_AGENT_API_KEY")
	}()

	cfg := Load()

	if cfg.Host != "0.0.0.0" {
		t.Errorf("expected host=0.0.0.0, got %s", cfg.Host)
	}
	if cfg.Port != 9090 {
		t.Errorf("expected port=9090, got %d", cfg.Port)
	}
	if cfg.AgentRESTHost != "10.0.0.1" {
		t.Errorf("expected agent host=10.0.0.1, got %s", cfg.AgentRESTHost)
	}
	if cfg.AgentRESTPort != 9079 {
		t.Errorf("expected agent port=9079, got %d", cfg.AgentRESTPort)
	}
	if cfg.AgentAPIKey != "test-key" {
		t.Errorf("expected API key=test-key, got %s", cfg.AgentAPIKey)
	}
}

// TestAddress tests the Address() helper string formatting.
// TestAddress 测试 Address() 方法能正确输出 "host:port" 格式的 HTTP 监听地址字符串。
func TestAddress(t *testing.T) {
	cfg := &Config{Host: "127.0.0.1", Port: 8082}
	if addr := cfg.Address(); addr != "127.0.0.1:8082" {
		t.Errorf("expected 127.0.0.1:8082, got %s", addr)
	}
}

// TestAgentBaseURL tests the AgentBaseURL() helper method.
// TestAgentBaseURL 测试 AgentBaseURL() 方法能正确拼接上游 Agent 的 HTTP REST 基础 URL。
func TestAgentBaseURL(t *testing.T) {
	cfg := &Config{AgentRESTHost: "10.0.0.1", AgentRESTPort: 8079}
	if url := cfg.AgentBaseURL(); url != "http://10.0.0.1:8079" {
		t.Errorf("expected http://10.0.0.1:8079, got %s", url)
	}
}

// TestGRPCAddress tests the GRPCAddress() helper method.
// TestGRPCAddress 测试 GRPCAddress() 方法能正确输出 gRPC 监听网络地址。
func TestGRPCAddress(t *testing.T) {
	cfg := &Config{GRPCHost: "127.0.0.1", GRPCPort: 50052}
	if addr := cfg.GRPCAddress(); addr != "127.0.0.1:50052" {
		t.Errorf("expected 127.0.0.1:50052, got %s", addr)
	}
}

// TestLoadProductionHardeningDefaults tests the production hardening defaults (DB, API Key, Log).
// TestLoadProductionHardeningDefaults 测试生产加固相关的配置默认值（空 API Key、空 DB 路径、json 日志格式、info 级别）。
func TestLoadProductionHardeningDefaults(t *testing.T) {
	os.Unsetenv("SERVICE_HUB_API_KEY")
	os.Unsetenv("SERVICE_HUB_CORS_ORIGINS")
	os.Unsetenv("SERVICE_HUB_DB_PATH")
	os.Unsetenv("SERVICE_HUB_LOG_FORMAT")
	os.Unsetenv("SERVICE_HUB_LOG_LEVEL")

	cfg := Load()

	if cfg.APIKey != "" {
		t.Errorf("expected empty API key, got %s", cfg.APIKey)
	}
	if len(cfg.CORSOrigins) != 0 {
		t.Errorf("expected empty CORS origins, got %v", cfg.CORSOrigins)
	}
	if cfg.DBPath != "" {
		t.Errorf("expected empty DB path, got %s", cfg.DBPath)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("expected log format=json, got %s", cfg.LogFormat)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected log level=info, got %s", cfg.LogLevel)
	}
}

// TestAgentBaseURLs tests single URL fallback and multiple upstream agent URLs parsing.
// TestAgentBaseURLs 测试上游 Agent URL 列表获取：
// 1) 未设置 PRIVACY_AGENT_URLS 时回退为单个 AgentBaseURL；
// 2) 设置了以逗号分隔的多个 URL 时能正确拆分为切片，供多活/故障转移调用。
func TestAgentBaseURLs(t *testing.T) {
	t.Run("DefaultSingleURL", func(t *testing.T) {
		t.Setenv("PRIVACY_AGENT_URLS", "")
		cfg := &Config{AgentRESTHost: "127.0.0.1", AgentRESTPort: 8079}
		urls := cfg.AgentBaseURLs()
		if len(urls) != 1 || urls[0] != "http://127.0.0.1:8079" {
			t.Errorf("expected [http://127.0.0.1:8079], got %v", urls)
		}
	})

	t.Run("MultipleURLsFromEnv", func(t *testing.T) {
		t.Setenv("PRIVACY_AGENT_URLS", "http://node1:8079,http://node2:8079")
		cfg := &Config{AgentRESTHost: "127.0.0.1", AgentRESTPort: 8079}
		urls := cfg.AgentBaseURLs()
		if len(urls) != 2 || urls[0] != "http://node1:8079" || urls[1] != "http://node2:8079" {
			t.Errorf("expected 2 URLs, got %v", urls)
		}
	})
}

// TestLoadAllEnvVariables tests that all service-hub environment variables are mapped accurately.
// TestLoadAllEnvVariables 综合测试所有环境变量（gRPC、队列、超时、mTLS 证书/私钥/CA/ClientAuth/公钥固定、跨域、DB 路径、日志）的完整映射。
func TestLoadAllEnvVariables(t *testing.T) {
	t.Setenv("SERVICE_HUB_GRPC_HOST", "0.0.0.0")
	t.Setenv("SERVICE_HUB_GRPC_PORT", "50059")
	t.Setenv("SERVICE_HUB_MAX_QUEUE", "500")
	t.Setenv("SERVICE_HUB_SCHEDULE_TIMEOUT", "60")
	t.Setenv("SERVICE_HUB_TLS_ENABLED", "true")
	t.Setenv("SERVICE_HUB_TLS_CERT_FILE", "/path/server.crt")
	t.Setenv("SERVICE_HUB_TLS_KEY_FILE", "/path/server.key")
	t.Setenv("SERVICE_HUB_TLS_CA_FILE", "/path/ca.crt")
	t.Setenv("SERVICE_HUB_TLS_CLIENT_AUTH", "require")
	t.Setenv("SERVICE_HUB_TLS_PINNED_PUBKEY_FILE", "/path/client.pub")
	t.Setenv("SERVICE_HUB_CORS_ORIGINS", "http://localhost:3000,http://localhost:5173")
	t.Setenv("SERVICE_HUB_DB_PATH", "/tmp/hub.db")
	t.Setenv("SERVICE_HUB_LOG_FORMAT", "text")
	t.Setenv("SERVICE_HUB_LOG_LEVEL", "debug")

	cfg := Load()

	if cfg.GRPCHost != "0.0.0.0" || cfg.GRPCPort != 50059 {
		t.Errorf("gRPC host/port mismatch: %s:%d", cfg.GRPCHost, cfg.GRPCPort)
	}
	if cfg.MaxQueueDepth != 500 || cfg.ScheduleTimeout != 60 {
		t.Errorf("queue depth / timeout mismatch: depth=%d, timeout=%d", cfg.MaxQueueDepth, cfg.ScheduleTimeout)
	}
	if !cfg.TLSEnabled || cfg.TLSCertFile != "/path/server.crt" || cfg.TLSClientAuth != "require" {
		t.Errorf("TLS config mismatch: %+v", cfg)
	}
	if cfg.TLSPinnedPubKeyFile != "/path/client.pub" {
		t.Errorf("pinned pubkey mismatch: %s", cfg.TLSPinnedPubKeyFile)
	}
	if len(cfg.CORSOrigins) != 2 || cfg.DBPath != "/tmp/hub.db" || cfg.LogFormat != "text" || cfg.LogLevel != "debug" {
		t.Errorf("hardening configs mismatch: %+v", cfg)
	}
}

// TestDatasourceConfig tests datasource URL and gRPC address formatting methods.
// TestDatasourceConfig 测试 DatasourceBaseURL() 与 DatasourceGRPCAddress() 方法的格式化正确性。
func TestDatasourceConfig(t *testing.T) {
	cfg := &Config{
		DatasourceRESTHost: "127.0.0.1",
		DatasourceRESTPort: 8083,
		DatasourceGRPCHost: "127.0.0.1",
		DatasourceGRPCPort: 50053,
	}
	if cfg.DatasourceBaseURL() != "http://127.0.0.1:8083" {
		t.Errorf("expected http://127.0.0.1:8083, got %s", cfg.DatasourceBaseURL())
	}
	if cfg.DatasourceGRPCAddress() != "127.0.0.1:50053" {
		t.Errorf("expected 127.0.0.1:50053, got %s", cfg.DatasourceGRPCAddress())
	}
}

// TestAuditLogEndpointResolution tests the evidence endpoint resolution order:
// SERVICE_HUB_AUDIT_LOG_URLS ➔ SERVICE_HUB_AUDIT_HTTP ➔ compose-matching default.
// TestAuditLogEndpointResolution 校验存证端点解析优先级：专用多副本变量 ➔
// 编排已注入的 SERVICE_HUB_AUDIT_HTTP 别名 ➔ 与 docker-compose 内置服务名一致的回退默认值。
func TestAuditLogEndpointResolution(t *testing.T) {
	t.Run("default matches compose", func(t *testing.T) {
		cfg := Load()
		urls := cfg.AuditLogURLs()
		if len(urls) != 1 || urls[0] != "http://audit-log:8084" {
			t.Fatalf("expected compose default [http://audit-log:8084], got %#v", urls)
		}
		if cfg.AuditLogTimeout != 10 || cfg.AuditLogMaxRetries != 3 {
			t.Errorf("evidence timeout/retry defaults mismatch: timeout=%d retries=%d", cfg.AuditLogTimeout, cfg.AuditLogMaxRetries)
		}
		if cfg.RequireTLS {
			t.Error("RequireTLS must default to false (local development stays usable)")
		}
	})

	t.Run("compose alias SERVICE_HUB_AUDIT_HTTP is honored", func(t *testing.T) {
		t.Setenv("SERVICE_HUB_AUDIT_LOG_URLS", "")
		t.Setenv("SERVICE_HUB_AUDIT_HTTP", "http://audit-log:8084")
		cfg := Load()
		if got := cfg.AuditLogURLs(); len(got) != 1 || got[0] != "http://audit-log:8084" {
			t.Fatalf("SERVICE_HUB_AUDIT_HTTP not honored: %#v", got)
		}
	})

	t.Run("explicit urls and key override everything", func(t *testing.T) {
		t.Setenv("SERVICE_HUB_AUDIT_LOG_URLS", "https://audit-a:8084, https://audit-b:8084")
		t.Setenv("SERVICE_HUB_AUDIT_HTTP", "http://should-be-ignored:1")
		t.Setenv("SERVICE_HUB_AUDIT_LOG_TIMEOUT", "4")
		t.Setenv("SERVICE_HUB_AUDIT_LOG_MAX_RETRIES", "0")
		t.Setenv("SERVICE_HUB_REQUIRE_TLS", "true")
		cfg := Load()
		if got := cfg.AuditLogURLs(); len(got) != 2 || got[1] != "https://audit-b:8084" {
			t.Fatalf("multi-replica urls mismatch: %#v", got)
		}
		if cfg.AuditLogTimeout != 4 || cfg.AuditLogMaxRetries != 0 {
			t.Errorf("timeout/retries mismatch: timeout=%d retries=%d", cfg.AuditLogTimeout, cfg.AuditLogMaxRetries)
		}
		if !cfg.RequireTLS {
			t.Error("SERVICE_HUB_REQUIRE_TLS=true must set RequireTLS")
		}
	})

	t.Run("audit-log inbound key is reused as fallback", func(t *testing.T) {
		t.Setenv("SERVICE_HUB_AUDIT_LOG_API_KEY", "")
		t.Setenv("AUDIT_LOG_API_KEY", "shared-audit-key")
		if got := Load().AuditLogAPIKey; got != "shared-audit-key" {
			t.Fatalf("expected AUDIT_LOG_API_KEY fallback, got %q", got)
		}
		t.Setenv("SERVICE_HUB_AUDIT_LOG_API_KEY", "hub-own-key")
		if got := Load().AuditLogAPIKey; got != "hub-own-key" {
			t.Fatalf("expected dedicated variable to win, got %q", got)
		}
	})
}

// TestAuditLogTimeoutDuration tests the evidence timeout accessor fallback.
// TestAuditLogTimeoutDuration 校验存证超时访问器：非正值（含未初始化的零值配置）回退 10s。
func TestAuditLogTimeoutDuration(t *testing.T) {
	if got := (&Config{AuditLogTimeout: 3}).AuditLogTimeoutDuration().String(); got != "3s" {
		t.Errorf("expected 3s, got %s", got)
	}
	if got := (&Config{}).AuditLogTimeoutDuration().String(); got != "10s" {
		t.Errorf("zero value must fall back to 10s, got %s", got)
	}
	var nilCfg *Config
	if got := nilCfg.AuditLogURLs(); got != nil {
		t.Errorf("nil config must report no endpoints, got %#v", got)
	}
}

// TestValidateFailClosedZeroTrust tests the P0-1 zero-trust default posture:
// loopback binds may start without an inbound key, remote binds must not.
// TestValidateFailClosedZeroTrust 校验 P0-1 零信任默认态（Gate G-02）：
// 纯环回监听允许免密本机开发；对外暴露（0.0.0.0 / 具体网卡地址）缺 Key 必须启动即拒绝；
// RequireTLS 声明为真但未启用 TLS 时同样必须拒绝启动。
func TestValidateFailClosedZeroTrust(t *testing.T) {
	base := func() *Config {
		return &Config{
			Host: "127.0.0.1", GRPCHost: "127.0.0.1",
			// 存证链路已配置：本用例只考察零信任默认态，避免与 P0-6 判定纠缠。
			AuditLogBaseURLs: []string{"http://audit-log:8084"},
		}
	}

	t.Run("loopback without api key is accepted", func(t *testing.T) {
		cfg := base()
		if err := cfg.Validate(); err != nil {
			t.Fatalf("loopback bind without API key must validate, got %v", err)
		}
	})

	t.Run("remote bind without api key is rejected", func(t *testing.T) {
		for _, host := range []string{"0.0.0.0", "10.20.30.40"} {
			cfg := base()
			cfg.Host = host
			err := cfg.Validate()
			if !errors.Is(err, pkgconfig.ErrAPIKeyRequired) {
				t.Fatalf("bind %s: expected ErrAPIKeyRequired, got %v", host, err)
			}
		}
	})

	t.Run("remote gRPC bind without api key is rejected", func(t *testing.T) {
		cfg := base()
		cfg.GRPCHost = "0.0.0.0"
		if err := cfg.Validate(); !errors.Is(err, pkgconfig.ErrAPIKeyRequired) {
			t.Fatalf("expected ErrAPIKeyRequired, got %v", err)
		}
	})

	t.Run("remote bind with api key is accepted", func(t *testing.T) {
		cert := t.TempDir() + "/server.crt"
		key := t.TempDir() + "/server.key"
		for _, p := range []string{cert, key} {
			if err := os.WriteFile(p, []byte("PEM"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cfg := base()
		cfg.Host = "0.0.0.0"
		cfg.GRPCHost = "0.0.0.0"
		cfg.APIKey = "hub-inbound-key"
		cfg.TLSEnabled = true
		cfg.TLSCertFile = cert
		cfg.TLSKeyFile = key
		cfg.MTLSWhitelistFile = "mtls-whitelist.yaml"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("remote bind with API key must validate, got %v", err)
		}
	})

	t.Run("require tls without tls is rejected", func(t *testing.T) {
		cfg := base()
		cfg.RequireTLS = true
		if err := cfg.Validate(); !errors.Is(err, pkgconfig.ErrTLSRequired) {
			t.Fatalf("expected ErrTLSRequired, got %v", err)
		}
	})

	t.Run("tls without whitelist file is rejected", func(t *testing.T) {
		cert := t.TempDir() + "/server.crt"
		key := t.TempDir() + "/server.key"
		for _, p := range []string{cert, key} {
			if err := os.WriteFile(p, []byte("PEM"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cfg := base()
		cfg.APIKey = "hub-inbound-key"
		cfg.TLSEnabled = true
		cfg.TLSCertFile = cert
		cfg.TLSKeyFile = key
		if err := cfg.Validate(); !errors.Is(err, pkgconfig.ErrMTLSWhitelistRequired) {
			t.Fatalf("expected ErrMTLSWhitelistRequired, got %v", err)
		}
		cfg.MTLSWhitelistFile = "mtls-whitelist.yaml"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("tls + whitelist must validate, got %v", err)
		}
	})
}

// TestValidateEvidenceEndpointRequired tests the P0-6 startup posture for a
// missing audit-log endpoint: loud rejection on a loopback-only dev bind, and
// (by contrast) a remote bind keeps Validate() free of that error so the
// pipeline can fail closed per task instead of the process refusing to boot.
// TestValidateEvidenceEndpointRequired 校验未配置存证端点时的启动策略：
// 全环回监听（本机开发形态）直接拒绝启动（loud startup rejection）；
// 远程绑定则放行进程，由 ⑥ audit 阶段逐条任务 fail-closed 失败。
func TestValidateEvidenceEndpointRequired(t *testing.T) {
	t.Run("loopback bind without evidence endpoint is rejected", func(t *testing.T) {
		cfg := &Config{Host: "127.0.0.1", GRPCHost: "localhost"}
		err := cfg.Validate()
		if !errors.Is(err, ErrAuditEndpointRequired) {
			t.Fatalf("expected ErrAuditEndpointRequired, got %v", err)
		}
	})

	t.Run("blank endpoints are treated as unconfigured", func(t *testing.T) {
		cfg := &Config{Host: "127.0.0.1", GRPCHost: "127.0.0.1", AuditLogBaseURLs: []string{"  ", ""}}
		if err := cfg.Validate(); !errors.Is(err, ErrAuditEndpointRequired) {
			t.Fatalf("expected ErrAuditEndpointRequired, got %v", err)
		}
	})

	t.Run("remote bind without evidence endpoint still enforces inbound key", func(t *testing.T) {
		cfg := &Config{Host: "0.0.0.0", GRPCHost: "0.0.0.0"}
		err := cfg.Validate()
		if errors.Is(err, ErrAuditEndpointRequired) {
			t.Fatalf("remote bind must not be refused at startup for a missing endpoint (tasks fail closed instead), got %v", err)
		}
		if !errors.Is(err, pkgconfig.ErrAPIKeyRequired) {
			t.Fatalf("expected ErrAPIKeyRequired for remote bind, got %v", err)
		}
	})

	t.Run("evidence tls without client keypair is rejected", func(t *testing.T) {
		cfg := &Config{
			Host: "127.0.0.1", GRPCHost: "127.0.0.1",
			AuditLogBaseURLs:   []string{"https://audit-log:8084"},
			AuditLogTLSEnabled: true,
		}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected error for evidence TLS without client certificate material")
		}
	})
}

// TestStrictStorageDefault tests the P0-4 no-silent-degradation default and its
// variable precedence (service-specific wins over the global STRICT_STORAGE).
// TestStrictStorageDefault 校验 P0-4 禁静音降级：默认即为严格模式，
// 且 SERVICE_HUB_STRICT_STORAGE 优先于全局 STRICT_STORAGE，便于按需单独放宽。
func TestStrictStorageDefault(t *testing.T) {
	t.Setenv("STRICT_STORAGE", "")
	t.Setenv("SERVICE_HUB_STRICT_STORAGE", "")
	if !Load().StrictStorage {
		t.Fatal("StrictStorage must default to true (no silent loss of lease semantics)")
	}

	t.Setenv("STRICT_STORAGE", "true")
	t.Setenv("SERVICE_HUB_STRICT_STORAGE", "false")
	if Load().StrictStorage {
		t.Fatal("SERVICE_HUB_STRICT_STORAGE=false must win over the global STRICT_STORAGE")
	}
}
