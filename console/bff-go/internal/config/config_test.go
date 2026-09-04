package config

import (
	"os"
	"testing"

	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
)

// TestEnvBool 验证 pkgconfig.EnvBool 布尔环境变量解析逻辑：
// 真值字面量（true/1/yes/on，大小写不敏感）返回 true，其余返回 false，
// 未设置或为空时返回默认值。
func TestEnvBool(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		set      bool
		def      bool
		expected bool
	}{
		{"unset uses default true", "", false, true, true},
		{"unset uses default false", "", false, false, false},
		{"empty uses default", "", true, true, true},
		{"true literal", "true", true, false, true},
		{"TRUE uppercase", "TRUE", true, false, true},
		{"one literal", "1", true, false, true},
		{"yes literal", "yes", true, false, true},
		{"on literal", "on", true, false, true},
		{"padded true", "  true  ", true, false, true},
		{"false literal", "false", true, true, false},
		{"zero literal", "0", true, true, false},
		{"random string is false", "banana", true, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const key = "TEST_ENV_BOOL"
			if tc.set {
				t.Setenv(key, tc.value)
			} else {
				// 模拟未设置：先记录原值由 t.Setenv 负责恢复，再清除
				t.Setenv(key, "")
				if err := os.Unsetenv(key); err != nil {
					t.Fatalf("unsetenv %s: %v", key, err)
				}
			}
			got := pkgconfig.EnvBool(key, tc.def)
			if got != tc.expected {
				t.Errorf("EnvBool(%q, %v) = %v, want %v", tc.value, tc.def, got, tc.expected)
			}
		})
	}
}

// TestLoadTLSDefaults 验证 TLS 相关配置项的默认值：全部关闭/为空。
func TestLoadTLSDefaults(t *testing.T) {
	// 清理可能影响断言的环境变量
	for _, k := range []string{
		"BFF_AGENT_TLS_ENABLED",
		"BFF_AGENT_TLS_CERT_FILE",
		"BFF_AGENT_TLS_KEY_FILE",
		"BFF_AGENT_TLS_CA_FILE",
		"BFF_AGENT_TLS_SERVER_NAME",
		"BFF_AGENT_TLS_INSECURE_SKIP_VERIFY",
	} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unsetenv %s: %v", k, err)
		}
	}

	cfg := Load()
	if cfg.AgentTLSEnabled {
		t.Error("AgentTLSEnabled default should be false")
	}
	if cfg.AgentTLSCertFile != "" || cfg.AgentTLSKeyFile != "" || cfg.AgentTLSCAFile != "" {
		t.Error("TLS file paths should default to empty")
	}
	if cfg.AgentTLSServerName != "" {
		t.Error("AgentTLSServerName should default to empty")
	}
	if cfg.AgentTLSInsecureSkipVerify {
		t.Error("AgentTLSInsecureSkipVerify default should be false")
	}
}

// TestLoadTLSEnabled 验证通过环境变量启用 TLS 并填充各证书路径。
func TestLoadTLSEnabled(t *testing.T) {
	t.Setenv("BFF_AGENT_TLS_ENABLED", "true")
	t.Setenv("BFF_AGENT_TLS_CERT_FILE", "/tmp/client.crt")
	t.Setenv("BFF_AGENT_TLS_KEY_FILE", "/tmp/client.key")
	t.Setenv("BFF_AGENT_TLS_CA_FILE", "/tmp/ca.crt")
	t.Setenv("BFF_AGENT_TLS_SERVER_NAME", "localhost")
	t.Setenv("BFF_AGENT_TLS_INSECURE_SKIP_VERIFY", "1")

	cfg := Load()
	if !cfg.AgentTLSEnabled {
		t.Error("AgentTLSEnabled should be true")
	}
	if cfg.AgentTLSCertFile != "/tmp/client.crt" {
		t.Errorf("AgentTLSCertFile = %q", cfg.AgentTLSCertFile)
	}
	if cfg.AgentTLSKeyFile != "/tmp/client.key" {
		t.Errorf("AgentTLSKeyFile = %q", cfg.AgentTLSKeyFile)
	}
	if cfg.AgentTLSCAFile != "/tmp/ca.crt" {
		t.Errorf("AgentTLSCAFile = %q", cfg.AgentTLSCAFile)
	}
	if cfg.AgentTLSServerName != "localhost" {
		t.Errorf("AgentTLSServerName = %q", cfg.AgentTLSServerName)
	}
	if !cfg.AgentTLSInsecureSkipVerify {
		t.Error("AgentTLSInsecureSkipVerify should be true")
	}
}
