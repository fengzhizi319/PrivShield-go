package config

import (
	"errors"
	"testing"
)

func TestValidateFailClosed(t *testing.T) {
	base := SecurityRequirements{
		ServiceName: "audit-log",
		Hosts:       []string{"127.0.0.1", "127.0.0.1"},
		APIKey:      "secret",
		GRPCEnabled: true,
	}

	t.Run("loopback without api key is allowed", func(t *testing.T) {
		req := base
		req.APIKey = ""
		if err := ValidateFailClosed(req); err != nil {
			t.Fatalf("local development bind must start without a key, got %v", err)
		}
	})

	t.Run("remote bind without api key fails closed", func(t *testing.T) {
		req := base
		req.APIKey = ""
		req.Hosts = []string{"0.0.0.0", "127.0.0.1"}
		if err := ValidateFailClosed(req); !errors.Is(err, ErrAPIKeyRequired) {
			t.Fatalf("expected ErrAPIKeyRequired, got %v", err)
		}
	})

	t.Run("require tls without tls fails", func(t *testing.T) {
		req := base
		req.RequireTLS = true
		if err := ValidateFailClosed(req); !errors.Is(err, ErrTLSRequired) {
			t.Fatalf("expected ErrTLSRequired, got %v", err)
		}
	})

	t.Run("grpc tls without whitelist fails", func(t *testing.T) {
		req := base
		req.TLSEnabled = true
		if err := ValidateFailClosed(req); !errors.Is(err, ErrMTLSWhitelistRequired) {
			t.Fatalf("expected ErrMTLSWhitelistRequired, got %v", err)
		}
		req.MTLSWhitelistFile = "config/mtls-whitelist.yaml"
		if err := ValidateFailClosed(req); err != nil {
			t.Fatalf("whitelist present must pass, got %v", err)
		}
	})

	t.Run("missing encryption key on remote bind fails", func(t *testing.T) {
		req := base
		req.Hosts = []string{"0.0.0.0"}
		req.RequireEncryptionKey = true
		if err := ValidateFailClosed(req); !errors.Is(err, ErrEncryptionKeyRequired) {
			t.Fatalf("expected ErrEncryptionKeyRequired, got %v", err)
		}
		req.EncryptionKey = "kms-held"
		if err := ValidateFailClosed(req); err != nil {
			t.Fatalf("encryption key present must pass, got %v", err)
		}
	})
}

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"":             true,
		"localhost":    true,
		"127.0.0.1":    true,
		"127.0.1.5":    true,
		"::1":          true,
		"127.0.0.1:80": true,
		"0.0.0.0":      false,
		"::":           false,
		"10.0.0.7":     false,
		"0.0.0.0:8084": false,
		"hub.internal": false,
	}
	for host, want := range cases {
		if got := IsLoopbackHost(host); got != want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}
