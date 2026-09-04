// Package rest — 路由权限映射完整性门禁测试（方案三 · CI 门禁）。
//
// 目标：privacy-engine 采用「path→permission 集中映射」(pkg/auth.PermissionForRESTPath)，
// 与路由注册分离。新增接口若遗漏映射，会静默落入 fail-closed 兜底权限 "admin"，既可能被误
// 锁死、又缺少细粒度隔离。本测试遍历全部已注册路由，断言没有任何路由落入兜底 "admin"，
// 一旦有人「加了路由忘配权限」，CI 立即失败并列出具体端点。
package rest

import (
	"testing"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
)

// TestAllRoutesHaveExplicitPermission 断言 privacy-engine 全部 REST 路由均显式映射到细粒度
// scope，未落入 fail-closed 兜底权限 "admin"。
func TestAllRoutesHaveExplicitPermission(t *testing.T) {
	r, _ := setupRouter(t)

	// privacy-engine 无「有意使用兜底权限」的基础设施路由白名单：全部业务/探针/运维端点均已显式映射。
	// 若未来新增故意仅 admin 可见的端点，在此登记 path 即可。
	allowFallback := map[string]bool{}

	issues := pkgauth.AuditRoutePermissions(
		r.Routes(),
		func(method, path string) string { return pkgauth.PermissionForRESTPath(path) },
		map[string]bool{"admin": true},
		allowFallback,
	)
	for _, is := range issues {
		t.Errorf("route %s %s has no explicit scope mapping (fell through to fail-closed %q); "+
			"add it to pkg/auth.PermissionForRESTPath or登记到 allowFallback 白名单",
			is.Method, is.Path, is.FallbackPerm)
	}
}
