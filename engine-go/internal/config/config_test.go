// Package config 的 fail-closed 启动门禁测试（P0-1 零信任默认态）。
// Unit tests for the engine's fail-closed startup gate (P0-1 zero-trust default posture).
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
)

// engineEnvKeys 是门禁读取的全部环境变量，逐用例清空以隔绝宿主 shell 污染。
var engineEnvKeys = []string{
	"DOTENV_DISABLED", "DOTENV_PATH", "AGENT_CONFIG_FILE",
	"AGENT_REST_HOST", "AGENT_REST_PORT", "AGENT_REST_ENABLED",
	"AGENT_GRPC_HOST", "AGENT_GRPC_PORT", "AGENT_GRPC_ENABLED",
	"AGENT_TLS_ENABLED", "AGENT_TLS_CERT_FILE", "AGENT_TLS_KEY_FILE", "AGENT_TLS_CA_FILE",
	"AGENT_REQUIRE_TLS", "AGENT_AUTH_ENABLED", "AGENT_AUTH_INTERNAL_API_KEYS", "AGENT_AUTH_API_KEY",
	"AGENT_AUTH_EXTERNAL_API_KEYS", "AGENT_AUTH_STATIC_API_KEYS",
	"AGENT_AUTH_INTERNAL_MTLS_ENABLED", "AGENT_AUTH_MTLS_WHITELIST_FILE",
	"AGENT_ALLOWED_CIDRS",
	"ENGINE_GATEWAY_HOST", "ENGINE_GATEWAY_PORT", "ENGINE_GATEWAY_GRPC_HOST", "ENGINE_GATEWAY_GRPC_PORT",
	"ENGINE_GATEWAY_REQUIRE_TLS", "ENGINE_GATEWAY_AUTH_ENABLED", "ENGINE_GATEWAY_ALLOWED_CIDRS",
}

// clearEngineEnv 将上述变量置空（EnvString/EnvBool 视空串为未设置，回退到默认值）。
func clearEngineEnv(t *testing.T) {
	t.Helper()
	for _, key := range engineEnvKeys {
		t.Setenv(key, "")
	}
	t.Setenv("DOTENV_DISABLED", "true")
}

// TestAgentDefaultsAreLoopbackKeyless 断言裸 `go run ./engine-go/cmd/privshield-agent` 形态：
// 默认监听收敛到环回地址，无 Key、无 TLS 也能通过门禁（本地开发不受影响）。
func TestAgentDefaultsAreLoopbackKeyless(t *testing.T) {
	clearEngineEnv(t)

	cfg := LoadAgent()
	if cfg.RESTHost != "127.0.0.1" || cfg.GRPCHost != "127.0.0.1" {
		t.Fatalf("engine must default to loopback binds, got rest=%s grpc=%s", cfg.RESTHost, cfg.GRPCHost)
	}
	if cfg.RESTAddress() != "127.0.0.1:8079" || cfg.GRPCAddress() != "127.0.0.1:50051" {
		t.Fatalf("unexpected default addresses: rest=%s grpc=%s", cfg.RESTAddress(), cfg.GRPCAddress())
	}
	if cfg.RequireTLS {
		t.Fatal("AGENT_REQUIRE_TLS must default to false")
	}
	if cfg.AuthEffectivelyEnabled() {
		t.Fatal("auth must default to disabled")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("loopback keyless development config must validate, got %v", err)
	}
}

// TestAgentProtocolSelectiveActivation 测试根据配置与环境变量动态选择开启 REST、gRPC 或双协议。
func TestAgentProtocolSelectiveActivation(t *testing.T) {
	clearEngineEnv(t)

	// 1. 默认形态：REST 与 gRPC 同时开启
	cfgDefault := LoadAgent()
	if !cfgDefault.RESTEnabled || !cfgDefault.GRPCEnabled {
		t.Fatalf("both REST and gRPC should be enabled by default, got rest=%v, grpc=%v",
			cfgDefault.RESTEnabled, cfgDefault.GRPCEnabled)
	}
	if len(cfgDefault.Hosts()) != 2 {
		t.Fatalf("expected 2 hosts in default dual-protocol mode, got %v", cfgDefault.Hosts())
	}
	if err := cfgDefault.Validate(); err != nil {
		t.Fatalf("default dual-protocol should validate, got %v", err)
	}

	// 2. 仅启用 gRPC，禁用 REST
	clearEngineEnv(t)
	t.Setenv("AGENT_REST_ENABLED", "false")
	cfgGRPC := LoadAgent()
	if cfgGRPC.RESTEnabled {
		t.Fatal("REST should be disabled when AGENT_REST_ENABLED=false")
	}
	if !cfgGRPC.GRPCEnabled {
		t.Fatal("gRPC should remain enabled")
	}
	if hosts := cfgGRPC.Hosts(); len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("expected only gRPC host in hosts list, got %v", hosts)
	}
	if err := cfgGRPC.Validate(); err != nil {
		t.Fatalf("gRPC-only config should validate, got %v", err)
	}

	// 3. 仅启用 REST，禁用 gRPC
	clearEngineEnv(t)
	t.Setenv("AGENT_GRPC_ENABLED", "false")
	cfgREST := LoadAgent()
	if !cfgREST.RESTEnabled {
		t.Fatal("REST should remain enabled")
	}
	if cfgREST.GRPCEnabled {
		t.Fatal("gRPC should be disabled when AGENT_GRPC_ENABLED=false")
	}
	if hosts := cfgREST.Hosts(); len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("expected only REST host in hosts list, got %v", hosts)
	}
	if err := cfgREST.Validate(); err != nil {
		t.Fatalf("REST-only config should validate, got %v", err)
	}

	// 4. 双协议全部关闭：门禁拦截必须报错快速失败
	clearEngineEnv(t)
	t.Setenv("AGENT_REST_ENABLED", "false")
	t.Setenv("AGENT_GRPC_ENABLED", "false")
	cfgNone := LoadAgent()
	if err := cfgNone.Validate(); err == nil || !strings.Contains(err.Error(), "at least one of REST or gRPC must be enabled") {
		t.Fatalf("expected error when both protocols are disabled, got %v", err)
	}
}

// TestAgentConfigYAMLLoading 测试从 YAML 文件加载 agent 基础配置，以及环境变量能够覆盖 YAML。
func TestAgentConfigYAMLLoading(t *testing.T) {
	clearEngineEnv(t)

	tempDir := t.TempDir()
	yamlFile := filepath.Join(tempDir, "privacy.yaml")
	yamlContent := `
agent:
  rest_host: "127.0.0.1"
  rest_port: 8888
  grpc_port: 55555
  rest_enabled: true
  grpc_enabled: false
`
	if err := os.WriteFile(yamlFile, []byte(yamlContent), 0o600); err != nil {
		t.Fatalf("failed to write test yaml: %v", err)
	}

	t.Setenv("AGENT_CONFIG_FILE", yamlFile)

	// 1. 验证 YAML 基准值生效
	cfg := LoadAgent()
	if cfg.RESTPort != 8888 {
		t.Errorf("expected YAML rest_port=8888, got %d", cfg.RESTPort)
	}
	if cfg.GRPCPort != 55555 {
		t.Errorf("expected YAML grpc_port=55555, got %d", cfg.GRPCPort)
	}
	if cfg.GRPCEnabled != false {
		t.Errorf("expected YAML grpc_enabled=false, got %v", cfg.GRPCEnabled)
	}

	// 2. 验证系统环境变量能够覆盖 YAML 中的值（最高优先级）
	t.Setenv("AGENT_REST_PORT", "9999")
	cfgOverridden := LoadAgent()
	if cfgOverridden.RESTPort != 9999 {
		t.Errorf("expected env AGENT_REST_PORT=9999 to override YAML, got %d", cfgOverridden.RESTPort)
	}
}

// TestAgentConfigDotEnvLoading 测试自动读取 .env 文件的能力。
func TestAgentConfigDotEnvLoading(t *testing.T) {
	clearEngineEnv(t)

	tempDir := t.TempDir()
	dotEnvFile := filepath.Join(tempDir, ".env")
	envContent := `
AGENT_REST_PORT=7777
AGENT_GRPC_PORT=44444
`
	if err := os.WriteFile(dotEnvFile, []byte(envContent), 0o600); err != nil {
		t.Fatalf("failed to write test .env: %v", err)
	}

	// 启用 dotenv 加载并指定测试文件路径
	t.Setenv("DOTENV_DISABLED", "false")
	t.Setenv("DOTENV_PATH", dotEnvFile)

	cfg := LoadAgent()
	if cfg.RESTPort != 7777 {
		t.Errorf("expected .env AGENT_REST_PORT=7777, got %d", cfg.RESTPort)
	}
	if cfg.GRPCPort != 44444 {
		t.Errorf("expected .env AGENT_GRPC_PORT=44444, got %d", cfg.GRPCPort)
	}
}

// gateCase 描述一条门禁用例：wantErr 为哨兵错误，wantMsg 为错误信息子串（二者任选其一）。
type gateCase struct {
	name    string
	env     map[string]string
	wantErr error
	wantMsg string
}

// TestAgentFailClosedGate 表驱动覆盖 agent 的 P0-1 启动红线。
func TestAgentFailClosedGate(t *testing.T) {
	cases := []gateCase{
		{
			name: "loopback rest+grpc without key keeps local dev working",
			env: map[string]string{
				"AGENT_REST_HOST": "127.0.0.1",
				"AGENT_GRPC_HOST": "127.0.0.1",
			},
		},
		{
			name: "remote rest bind without credentials aborts",
			env: map[string]string{
				"AGENT_REST_HOST": "0.0.0.0",
				"AGENT_GRPC_HOST": "127.0.0.1",
			},
			wantErr: pkgconfig.ErrAPIKeyRequired,
		},
		{
			name: "remote grpc bind without credentials aborts",
			env: map[string]string{
				"AGENT_REST_HOST": "127.0.0.1",
				"AGENT_GRPC_HOST": "10.20.30.40",
			},
			wantErr: pkgconfig.ErrAPIKeyRequired,
		},
		{
			name: "auth switch on but no key is still unauthenticated",
			env: map[string]string{
				"AGENT_REST_HOST":    "0.0.0.0",
				"AGENT_AUTH_ENABLED": "true",
			},
			wantErr: pkgconfig.ErrAPIKeyRequired,
		},
		{
			name: "remote bind with auth enabled and a key passes",
			env: map[string]string{
				"AGENT_REST_HOST":                "0.0.0.0",
				"AGENT_GRPC_HOST":                "0.0.0.0",
				"AGENT_AUTH_ENABLED":             "true",
				"AGENT_AUTH_INTERNAL_API_KEYS":   "tok:hub:privacy:mask",
				"AGENT_TLS_ENABLED":              "true",
				"AGENT_TLS_CERT_FILE":            "__CERT__",
				"AGENT_TLS_KEY_FILE":             "__KEY__",
				"AGENT_AUTH_MTLS_WHITELIST_FILE": "__WHITELIST__",
			},
		},
		{
			name: "require tls without tls aborts",
			env: map[string]string{
				"AGENT_REQUIRE_TLS": "true",
				"AGENT_TLS_ENABLED": "false",
			},
			wantErr: pkgconfig.ErrTLSRequired,
		},
		{
			name: "grpc tls without cn whitelist aborts",
			env: map[string]string{
				"AGENT_TLS_ENABLED":   "true",
				"AGENT_TLS_CERT_FILE": "__CERT__",
				"AGENT_TLS_KEY_FILE":  "__KEY__",
				"AGENT_AUTH_ENABLED":  "true",
				"AGENT_AUTH_API_KEY":  "tok",
				"AGENT_REST_HOST":     "0.0.0.0",
			},
			wantErr: pkgconfig.ErrMTLSWhitelistRequired,
		},
		{
			name: "grpc tls with cn whitelist file passes",
			env: map[string]string{
				"AGENT_TLS_ENABLED":              "true",
				"AGENT_TLS_CERT_FILE":            "__CERT__",
				"AGENT_TLS_KEY_FILE":             "__KEY__",
				"AGENT_AUTH_ENABLED":             "true",
				"AGENT_AUTH_API_KEY":             "tok",
				"AGENT_AUTH_MTLS_WHITELIST_FILE": "__WHITELIST__",
			},
		},
		{
			name: "internal mtls on without cn whitelist file aborts even with tls off",
			env: map[string]string{
				"AGENT_AUTH_INTERNAL_MTLS_ENABLED": "true",
				"AGENT_TLS_ENABLED":                "false",
			},
			wantErr: pkgconfig.ErrMTLSWhitelistRequired,
		},
		{
			name: "internal mtls on with unreadable cn whitelist file aborts",
			env: map[string]string{
				"AGENT_AUTH_INTERNAL_MTLS_ENABLED": "true",
				"AGENT_AUTH_MTLS_WHITELIST_FILE":   "/nonexistent/mtls-whitelist.yaml",
			},
			wantMsg: "not accessible",
		},
		{
			name: "tls enabled with missing cert file aborts instead of serving plaintext",
			env: map[string]string{
				"AGENT_TLS_ENABLED":  "true",
				"AGENT_AUTH_API_KEY": "tok",
				"AGENT_AUTH_ENABLED": "true",
			},
			wantMsg: "AGENT_TLS_CERT_FILE is not set",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEngineEnv(t)
			for k, v := range tc.env {
				switch v {
				case "__CERT__":
					v = writeTempFile(t, "server.crt")
				case "__KEY__":
					v = writeTempFile(t, "server.key")
				case "__WHITELIST__":
					v = writeTempFile(t, "mtls-whitelist.yaml")
				}
				t.Setenv(k, v)
			}

			err := LoadAgent().Validate()
			switch {
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			case tc.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tc.wantMsg)):
				t.Fatalf("expected error containing %q, got %v", tc.wantMsg, err)
			case tc.wantErr == nil && tc.wantMsg == "" && err != nil:
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// TestGatewayGate 断言网关形态：默认环回可无凭据启动，非环回或声明 TLS 时启动失败。
func TestGatewayGate(t *testing.T) {
	clearEngineEnv(t)

	gw := LoadGateway()
	if gw.RESTAddress() != "127.0.0.1:8000" || gw.GRPCAddress() != "127.0.0.1:50000" {
		t.Fatalf("gateway must default to loopback binds, got http=%s grpc=%s", gw.RESTAddress(), gw.GRPCAddress())
	}
	if err := gw.Validate(); err != nil {
		t.Fatalf("loopback gateway must validate without credentials, got %v", err)
	}

	t.Setenv("ENGINE_GATEWAY_HOST", "0.0.0.0")
	if err := LoadGateway().Validate(); !errors.Is(err, pkgconfig.ErrAPIKeyRequired) {
		t.Fatalf("remote gateway bind without credentials must return ErrAPIKeyRequired, got %v", err)
	}

	t.Setenv("ENGINE_GATEWAY_HOST", "127.0.0.1")
	t.Setenv("ENGINE_GATEWAY_AUTH_ENABLED", "true")
	t.Setenv("AGENT_AUTH_API_KEY", "tok")
	if err := LoadGateway().Validate(); err != nil {
		t.Fatalf("remote gateway with credentials configured must validate, got %v", err)
	}

	t.Setenv("ENGINE_GATEWAY_REQUIRE_TLS", "true")
	if err := LoadGateway().Validate(); !errors.Is(err, pkgconfig.ErrTLSRequired) {
		t.Fatalf("ENGINE_GATEWAY_REQUIRE_TLS without inbound TLS termination must return ErrTLSRequired, got %v", err)
	}
}

// writeTempFile 生成一个占位文件（仅用于满足门禁的文件可达性检查）。
func writeTempFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
