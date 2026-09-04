package handlers

import (
	"testing"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
)

// TestAllRoutesHaveExplicitPermission 断言 audit-log 全部 REST 路由均显式映射到细粒度 scope，
// 未落入 fail-closed 兜底权限 "audit:admin"。作为不可篡改存证服务，权责分离要求极高，任何
// 新增端点必须显式声明 read/write/verify 权限，故以 CI 门禁强制「加路由必配权限」。
func TestAllRoutesHaveExplicitPermission(t *testing.T) {
	s := newTestServer()
	r := newTestRouter(s)

	// /health、/readyz、/metrics 返回空权限（探针/公开），业务端点均显式映射 audit:read/write/verify。
	// 当前无「有意使用兜底权限 audit:admin」的基础设施路由。
	allowFallback := map[string]bool{}

	issues := pkgauth.AuditRoutePermissions(
		r.Routes(),
		AuditLogPermissionForPath,
		map[string]bool{"audit:admin": true},
		allowFallback,
	)
	for _, is := range issues {
		t.Errorf("route %s %s has no explicit scope mapping (fell through to fail-closed %q); "+
			"add it to AuditLogPermissionForPath or登记到 allowFallback 白名单",
			is.Method, is.Path, is.FallbackPerm)
	}
}
