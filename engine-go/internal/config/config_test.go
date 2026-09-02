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
	"PRIVACY_REST_HOST", "PRIVACY_REST_PORT",
	"PRIVACY_GRPC_HOST", "PRIVACY_GRPC_PORT",
	"PRIVACY_TLS_ENABLED", "PRIVACY_TLS_CERT_FILE", "PRIVACY_TLS_KEY_FILE", "PRIVACY_TLS_CA_FILE",
	"PRIVACY_REQUIRE_TLS",
	"PRIVACY_AUTH_ENABLED", "PRIVACY_AUTH_API_KEY", "PRIVACY_API_KEY",
	"PRIVACY_AUTH_INTERNAL_API_KEYS", "PRIVACY_AUTH_EXTERNAL_API_KEYS", "PRIVACY_AUTH_STATIC_API_KEYS",
	"PRIVACY_AUTH_INTERNAL_MTLS_ENABLED", "PRIVACY_AUTH_MTLS_WHITELIST_FILE",
	"GATEWAY_HOST", "GATEWAY_PORT", "GATEWAY_GRPC_HOST", "GATEWAY_GRPC_PORT", "GATEWAY_REQUIRE_TLS",
}

// clearEngineEnv 将上述变量置空（EnvString/EnvBool 视空串为未设置，回退到默认值）。
func clearEngineEnv(t *testing.T) {
	t.Helper()
	for _, key := range engineEnvKeys {
		t.Setenv(key, "")
	}
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
		t.Fatal("PRIVACY_REQUIRE_TLS must default to false")
	}
	if cfg.AuthEffectivelyEnabled() {
		t.Fatal("auth must default to disabled")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("loopback keyless development config must validate, got %v", err)
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
				"PRIVACY_REST_HOST": "127.0.0.1",
				"PRIVACY_GRPC_HOST": "127.0.0.1",
			},
		},
		{
			name: "remote rest bind without credentials aborts",
			env: map[string]string{
				"PRIVACY_REST_HOST": "0.0.0.0",
				"PRIVACY_GRPC_HOST": "127.0.0.1",
			},
			wantErr: pkgconfig.ErrAPIKeyRequired,
		},
		{
			name: "remote grpc bind without credentials aborts",
			env: map[string]string{
				"PRIVACY_REST_HOST": "127.0.0.1",
				"PRIVACY_GRPC_HOST": "10.20.30.40",
			},
			wantErr: pkgconfig.ErrAPIKeyRequired,
		},
		{
			name: "auth switch on but no key is still unauthenticated",
			env: map[string]string{
				"PRIVACY_REST_HOST":    "0.0.0.0",
				"PRIVACY_AUTH_ENABLED": "true",
			},
			wantErr: pkgconfig.ErrAPIKeyRequired,
		},
		{
			name: "remote bind with auth enabled and a key passes",
			env: map[string]string{
				"PRIVACY_REST_HOST":              "0.0.0.0",
				"PRIVACY_GRPC_HOST":              "0.0.0.0",
				"PRIVACY_AUTH_ENABLED":           "true",
				"PRIVACY_AUTH_INTERNAL_API_KEYS": "tok:hub:privacy:mask",
				"PRIVACY_TLS_ENABLED":            "true",
				"PRIVACY_TLS_CERT_FILE":          "__CERT__",
				"PRIVACY_TLS_KEY_FILE":           "__KEY__",
				"PRIVACY_AUTH_MTLS_WHITELIST_FILE": "__WHITELIST__",
			},
		},
		{
			name: "require tls without tls aborts",
			env: map[string]string{
				"PRIVACY_REQUIRE_TLS": "true",
				"PRIVACY_TLS_ENABLED": "false",
			},
			wantErr: pkgconfig.ErrTLSRequired,
		},
		{
			name: "grpc tls without cn whitelist aborts",
			env: map[string]string{
				"PRIVACY_TLS_ENABLED":   "true",
				"PRIVACY_TLS_CERT_FILE": "__CERT__",
				"PRIVACY_TLS_KEY_FILE":  "__KEY__",
				"PRIVACY_AUTH_ENABLED":  "true",
				"PRIVACY_AUTH_API_KEY":  "tok",
				"PRIVACY_REST_HOST":     "0.0.0.0",
			},
			wantErr: pkgconfig.ErrMTLSWhitelistRequired,
		},
		{
			name: "grpc tls with cn whitelist file passes",
			env: map[string]string{
				"PRIVACY_TLS_ENABLED":              "true",
				"PRIVACY_TLS_CERT_FILE":            "__CERT__",
				"PRIVACY_TLS_KEY_FILE":             "__KEY__",
				"PRIVACY_AUTH_ENABLED":             "true",
				"PRIVACY_AUTH_API_KEY":             "tok",
				"PRIVACY_AUTH_MTLS_WHITELIST_FILE": "__WHITELIST__",
			},
		},
		{
			name: "internal mtls on without cn whitelist file aborts even with tls off",
			env: map[string]string{
				"PRIVACY_AUTH_INTERNAL_MTLS_ENABLED": "true",
				"PRIVACY_TLS_ENABLED":                "false",
			},
			wantErr: pkgconfig.ErrMTLSWhitelistRequired,
		},
		{
			name: "internal mtls on with unreadable cn whitelist file aborts",
			env: map[string]string{
				"PRIVACY_AUTH_INTERNAL_MTLS_ENABLED": "true",
				"PRIVACY_AUTH_MTLS_WHITELIST_FILE":   "/nonexistent/mtls-whitelist.yaml",
			},
			wantMsg: "not accessible",
		},
		{
			name: "tls enabled with missing cert file aborts instead of serving plaintext",
			env: map[string]string{
				"PRIVACY_TLS_ENABLED":  "true",
				"PRIVACY_AUTH_API_KEY": "tok",
				"PRIVACY_AUTH_ENABLED": "true",
			},
			wantMsg: "PRIVACY_TLS_CERT_FILE is not set",
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

	t.Setenv("GATEWAY_HOST", "0.0.0.0")
	if err := LoadGateway().Validate(); !errors.Is(err, pkgconfig.ErrAPIKeyRequired) {
		t.Fatalf("remote gateway bind without credentials must return ErrAPIKeyRequired, got %v", err)
	}

	t.Setenv("GATEWAY_HOST", "127.0.0.1")
	t.Setenv("PRIVACY_AUTH_ENABLED", "true")
	t.Setenv("PRIVACY_AUTH_API_KEY", "tok")
	if err := LoadGateway().Validate(); err != nil {
		t.Fatalf("remote gateway with credentials configured must validate, got %v", err)
	}

	t.Setenv("GATEWAY_REQUIRE_TLS", "true")
	if err := LoadGateway().Validate(); !errors.Is(err, pkgconfig.ErrTLSRequired) {
		t.Fatalf("GATEWAY_REQUIRE_TLS without inbound TLS termination must return ErrTLSRequired, got %v", err)
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
