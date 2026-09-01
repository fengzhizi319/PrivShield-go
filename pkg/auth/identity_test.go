package auth

import "testing"

func TestIdentity_HasPermission(t *testing.T) {
	tests := []struct {
		name       string
		identity   Identity
		permission string
		want       bool
	}{
		{"wildcard", Identity{Scopes: []string{"*"}}, "privacy:mask", true},
		{"exact match", Identity{Scopes: []string{"privacy:mask"}}, "privacy:mask", true},
		{"no match", Identity{Scopes: []string{"privacy:dp"}}, "privacy:mask", false},
		{"empty scopes", Identity{Scopes: []string{}}, "privacy:mask", false},
		{"multi scopes", Identity{Scopes: []string{"privacy:dp", "privacy:mask"}}, "privacy:mask", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.identity.HasPermission(tt.permission); got != tt.want {
				t.Errorf("HasPermission(%q) = %v, want %v", tt.permission, got, tt.want)
			}
		})
	}
}

func TestPermissionForRESTPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/health", "health:read"},
		{"/livez", "health:read"},
		{"/readyz", "health:read"},
		{"/v1/privacy/mask", "privacy:mask"},
		{"/v1/privacy/mask/record", "privacy:mask"},
		{"/v1/privacy/dp/count", "privacy:dp"},
		{"/v1/privacy/ldp/randomized_response", "privacy:dp"},
		{"/v1/privacy/k_anonymize", "privacy:kano"},
		{"/v1/privacy/qol/obfuscate", "privacy:qol"},
		{"/v1/privacy/budget", "privacy:budget"},
		{"/v1/agent/process", "agent:process"},
		{"/v1/ops/diagnostics", "ops:diagnostics"},
		{"/debug/pprof", "ops:admin"},
		{"/debug/pprof/heap", "ops:admin"},
		{"/unknown", "*"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := PermissionForRESTPath(tt.path); got != tt.want {
				t.Errorf("PermissionForRESTPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestPermissionForGRPCMethod(t *testing.T) {
	tests := []struct {
		method string
		want   string
	}{
		{"privacy.local.PrivacyService/Mask", "privacy:mask"},
		{"privacy.local.PrivacyService/DPCount", "privacy:dp"},
		{"privacy.local.PrivacyService/Health", "health:read"},
		{"privacy.local.PrivacyService/Unknown", "*"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			if got := PermissionForGRPCMethod(tt.method); got != tt.want {
				t.Errorf("PermissionForGRPCMethod(%q) = %q, want %q", tt.method, got, tt.want)
			}
		})
	}
}

func TestIsHealthPathOrMethod(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/health", true},
		{"/livez", true},
		{"/readyz", true},
		{"/readyz/llm", true},
		{"privacy.local.PrivacyService/Health", true},
		{"/v1/privacy/mask", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsHealthPathOrMethod(tt.path); got != tt.want {
				t.Errorf("IsHealthPathOrMethod(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
