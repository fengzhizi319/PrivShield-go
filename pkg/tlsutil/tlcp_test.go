package tlsutil

import (
	"testing"
)

func TestIsTLCPEnabled(t *testing.T) {
	// 1. 空键名时返回 false
	if IsTLCPEnabled("") {
		t.Error("expected false for empty envKey")
	}

	// 2. 传入未设置的变量名返回 false
	t.Setenv("TEST_TLCP_CUSTOM", "")
	if IsTLCPEnabled("TEST_TLCP_CUSTOM") {
		t.Error("expected false for unset env")
	}

	// 3. 传入设置为 true 的变量名返回 true
	t.Setenv("TEST_TLCP_CUSTOM", "true")
	if !IsTLCPEnabled("TEST_TLCP_CUSTOM") {
		t.Error("expected true when set to true")
	}
}

func TestTLCPConfigFromEnv(t *testing.T) {
	// 1. 无前缀传参返回空配置
	cfgEmpty := TLCPConfigFromEnv("")
	if cfgEmpty.Enabled || cfgEmpty.SignCertFile != "" {
		t.Errorf("expected empty config without prefix, got %+v", cfgEmpty)
	}

	// 2. 传入前缀解析对应配置
	t.Setenv("MY_APP_TLS_NATIONAL_CIPHER", "true")
	t.Setenv("MY_APP_TLCP_SIGN_CERT_FILE", "/etc/ssl/sign.crt")
	t.Setenv("MY_APP_TLCP_SIGN_KEY_FILE", "/etc/ssl/sign.key")
	t.Setenv("MY_APP_TLCP_ENC_CERT_FILE", "/etc/ssl/enc.crt")
	t.Setenv("MY_APP_TLCP_ENC_KEY_FILE", "/etc/ssl/enc.key")

	cfg := TLCPConfigFromEnv("MY_APP_")
	if !cfg.Enabled {
		t.Error("expected Enabled to be true")
	}
	if cfg.SignCertFile != "/etc/ssl/sign.crt" {
		t.Errorf("got SignCertFile %q, want /etc/ssl/sign.crt", cfg.SignCertFile)
	}
	if cfg.SignKeyFile != "/etc/ssl/sign.key" {
		t.Errorf("got SignKeyFile %q, want /etc/ssl/sign.key", cfg.SignKeyFile)
	}
	if cfg.EncCertFile != "/etc/ssl/enc.crt" {
		t.Errorf("got EncCertFile %q, want /etc/ssl/enc.crt", cfg.EncCertFile)
	}
	if cfg.EncKeyFile != "/etc/ssl/enc.key" {
		t.Errorf("got EncKeyFile %q, want /etc/ssl/enc.key", cfg.EncKeyFile)
	}
}
