package auth

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestAuditRoutePermissions_FlagsUnmappedRoutes 验证审计能够识别落入兜底权限的路由，
// 并对 allowFallback 白名单中的 path 予以豁免。
func TestAuditRoutePermissions_FlagsUnmappedRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/known/read", func(c *gin.Context) {})
	r.POST("/known/write", func(c *gin.Context) {})
	r.GET("/brand/new", func(c *gin.Context) {})
	r.GET("/metrics", func(c *gin.Context) {})

	permFunc := func(method, path string) string {
		switch path {
		case "/known/read":
			return "known:read"
		case "/known/write":
			return "known:write"
		case "/metrics":
			return "admin" // 基础设施路由，故意落入兜底
		default:
			return "admin" // fail-closed：未显式映射
		}
	}
	fallback := map[string]bool{"admin": true}
	allow := map[string]bool{"/metrics": true}

	issues := AuditRoutePermissions(r.Routes(), permFunc, fallback, allow)

	// /metrics 命中白名单被豁免；仅 /brand/new 被报告为遗漏。
	if len(issues) != 1 {
		t.Fatalf("expected 1 unmapped route, got %d: %+v", len(issues), issues)
	}
	if issues[0].Path != "/brand/new" || issues[0].Method != http.MethodGet {
		t.Errorf("unexpected flagged route: %+v", issues[0])
	}
}

// TestAuditRoutePermissions_AllMapped 验证全部显式映射时返回空列表。
func TestAuditRoutePermissions_AllMapped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/a", func(c *gin.Context) {})
	r.POST("/b", func(c *gin.Context) {})

	permFunc := func(method, path string) string { return "explicit:perm" }
	if issues := AuditRoutePermissions(r.Routes(), permFunc, map[string]bool{"admin": true}, nil); len(issues) != 0 {
		t.Fatalf("expected no issues, got %+v", issues)
	}
}
