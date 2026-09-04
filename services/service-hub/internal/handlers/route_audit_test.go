package handlers

import (
	"testing"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
)

// TestAllRoutesHaveExplicitPermission 断言 service-hub 全部 REST 路由均显式映射到细粒度 scope，
// 未落入 fail-closed 兜底权限 "admin"。service-hub 是唯一编排入口，接口权限遗漏会直接放大
// 外部申请方的越权面，故以 CI 门禁强制「加路由必配权限」。
func TestAllRoutesHaveExplicitPermission(t *testing.T) {
	s := newSimpleTestServer(t)
	r := newTestRouter(s)

	// service-hub 探针 /health、/readyz 与 /metrics 归一化后返回空权限（公开），不落入兜底；
	// 其余业务端点均显式映射 hub:read / hub:dispatch。当前无「有意使用兜底权限」的基础设施路由。
	allowFallback := map[string]bool{}

	issues := pkgauth.AuditRoutePermissions(
		r.Routes(),
		func(method, path string) string { return pkgauth.ServiceHubPermissionForPath(path) },
		map[string]bool{"admin": true},
		allowFallback,
	)
	for _, is := range issues {
		t.Errorf("route %s %s has no explicit scope mapping (fell through to fail-closed %q); "+
			"add it to pkg/auth.ServiceHubPermissionForPath or登记到 allowFallback 白名单",
			is.Method, is.Path, is.FallbackPerm)
	}
}
