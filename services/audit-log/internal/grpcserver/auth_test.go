package grpcserver

import (
	"testing"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
)

func TestAuditLogPermissionForGRPCMethod(t *testing.T) {
	cases := []struct {
		method string
		want   string
	}{
		{"/auditlog.AuditLogService/Health", ""},
		{"/auditlog.AuditLogService/RecordAudit", "audit:write"},
		{"/auditlog.AuditLogService/GetAuditLog", "audit:read"},
		{"/auditlog.AuditLogService/ListAuditLogs", "audit:read"},
		{"/auditlog.AuditLogService/GetAuditStats", "audit:read"},
		{"/auditlog.AuditLogService/ListSnapshots", "audit:read"},
		{"/auditlog.AuditLogService/GenerateReport", "audit:read"},
		{"/auditlog.AuditLogService/VerifyIntegrity", "audit:verify"},
		{"/auditlog.AuditLogService/VerifyChain", "audit:verify"},
		{"/unknown.Service/Foo", ""},
	}
	for _, tc := range cases {
		if got := AuditLogPermissionForGRPCMethod(tc.method); got != tc.want {
			t.Errorf("AuditLogPermissionForGRPCMethod(%q) = %q, want %q", tc.method, got, tc.want)
		}
	}
}

func TestIdentityScopeCheck(t *testing.T) {
	t.Run("wildcard grants all", func(t *testing.T) {
		id := &pkgauth.Identity{Scopes: []string{"*"}}
		if !id.HasPermission("audit:write") {
			t.Error("wildcard should grant audit:write")
		}
		if !id.HasPermission("audit:read") {
			t.Error("wildcard should grant audit:read")
		}
		if !id.HasPermission("audit:verify") {
			t.Error("wildcard should grant audit:verify")
		}
	})

	t.Run("read scope denies write", func(t *testing.T) {
		id := &pkgauth.Identity{Scopes: []string{"audit:read"}}
		if !id.HasPermission("audit:read") {
			t.Error("audit:read scope should grant audit:read")
		}
		if id.HasPermission("audit:write") {
			t.Error("audit:read scope should NOT grant audit:write")
		}
		if id.HasPermission("audit:verify") {
			t.Error("audit:read scope should NOT grant audit:verify")
		}
	})

	t.Run("write scope denies read", func(t *testing.T) {
		id := &pkgauth.Identity{Scopes: []string{"audit:write"}}
		if id.HasPermission("audit:read") {
			t.Error("audit:write scope should NOT grant audit:read")
		}
	})
}
