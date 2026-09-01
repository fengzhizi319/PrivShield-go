// Package middleware 测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证基于角色的 API Key 认证中间件（AuthWithRoles）的核心功能：
//  1. 【只读核验员权限】：验证 readerKey 能访问白名单内的 GET/POST 验真端点（审计日志查询、
//     快照验真、哈希链验真），保证核验专区可用；
//  2. 【写端点拒绝】：验证 readerKey 无法访问白名单外的写端点（POST 写存证、报表导出、
//     快照写入），同路径不同方法的越权被正确拦截；
//  3. 【写入身份不受约束】：验证 apiKey（写入身份）能访问所有端点，权责分离不卡业务；
//  4. 【未知/缺失 Key 处理】：验证未知 Key 和缺失 Token 均返回 401（不因角色机制降级为放行）；
//  5. 【readerKey 为空降级】：验证 readerKey 为空时与 Auth(apiKey) 完全同构（存量部署零影响）；
//  6. 【健康探活豁免】：验证 /health 路径豁免认证；
//  7. 【/metrics 鉴权】：验证 /metrics 纳入鉴权（P1-6），无 Key 返回 401；
//  8. 【路径边界安全】：验证前缀匹配必须以 "/" 为边界，防止 /api/audit/logs 越到 /api/audit/logs-backup；
//  9. 【响应体不泄露】：验证拒绝响应体包含统一 FORBIDDEN 文案，不泄露可枚举信息。
// ==============================================================================

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// readerEndpoints 复刻 services/audit-log 的只读核验白名单：
// 读端点 + 验真端点放行，写存证与报表导出刻意排除。
var readerEndpoints = []ReadOnlyEndpoint{
	{Method: http.MethodGet, Path: "/api/audit/logs"},
	{Method: http.MethodGet, Path: "/api/audit/stats"},
	{Method: http.MethodGet, Path: "/api/audit/snapshots"},
	{Method: http.MethodPost, Path: "/api/audit/snapshots/verify"},
	{Method: http.MethodGet, Path: "/api/audit/chain/verify"},
	{Method: http.MethodPost, Path: "/api/audit/chain/verify"},
}

func newRoleRouter(apiKey, readerKey string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthWithRoles(apiKey, readerKey, readerEndpoints))
	// 覆盖 GET/POST 两类方法，让「同一资源不同方法」的越权路径可被断言。
	for _, p := range []string{
		"/api/audit/logs", "/api/audit/logs/:id", "/api/audit/stats",
		"/api/audit/snapshots", "/api/audit/snapshots/verify",
		"/api/audit/chain/verify", "/api/audit/report", "/api/audit/logs-backup",
	} {
		r.GET(p, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
		r.POST(p, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	}
	return r
}

func doWithKey(r *gin.Engine, method, path, key string) int {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	r.ServeHTTP(w, req)
	return w.Code
}

// TestAuthWithRoles_ReaderAllowsVerificationReads 只读核验员必须能完成验真与查询（否则核验专区不可用）。
func TestAuthWithRoles_ReaderAllowsVerificationReads(t *testing.T) {
	r := newRoleRouter("write-key", "reader-key")
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/audit/logs"},
		{http.MethodGet, "/api/audit/logs/abc123"},
		{http.MethodGet, "/api/audit/stats"},
		{http.MethodGet, "/api/audit/snapshots"},
		{http.MethodPost, "/api/audit/snapshots/verify"},
		{http.MethodGet, "/api/audit/chain/verify"},
		{http.MethodPost, "/api/audit/chain/verify"},
	}
	for _, tc := range cases {
		if code := doWithKey(r, tc.method, tc.path, "reader-key"); code != http.StatusOK {
			t.Errorf("reader %s %s: status = %d, want 200", tc.method, tc.path, code)
		}
	}
}

// TestAuthWithRoles_ReaderDeniedOnWriteEndpoints 白名单必须带方法：同路径 POST 写入不能因 GET 白名单被放行。
func TestAuthWithRoles_ReaderDeniedOnWriteEndpoints(t *testing.T) {
	r := newRoleRouter("write-key", "reader-key")
	cases := []struct{ method, path string }{
		{http.MethodPost, "/api/audit/logs"},       // 写存证
		{http.MethodPost, "/api/audit/report"},     // 报表导出
		{http.MethodGet, "/api/audit/logs-backup"}, // 前缀近似但非白名单子路径
		{http.MethodPost, "/api/audit/snapshots"},  // 快照写入面
	}
	for _, tc := range cases {
		code := doWithKey(r, tc.method, tc.path, "reader-key")
		if code != http.StatusForbidden {
			t.Errorf("reader %s %s: status = %d, want 403", tc.method, tc.path, code)
		}
	}
}

// TestAuthWithRoles_FullKeyKeepsWriteAccess 写入身份不受白名单约束（权责分离不能反过来卡住业务写入）。
func TestAuthWithRoles_FullKeyKeepsWriteAccess(t *testing.T) {
	r := newRoleRouter("write-key", "reader-key")
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/audit/logs"},
		{http.MethodPost, "/api/audit/report"},
		{http.MethodGet, "/api/audit/chain/verify"},
	} {
		if code := doWithKey(r, tc.method, tc.path, "write-key"); code != http.StatusOK {
			t.Errorf("writer %s %s: status = %d, want 200", tc.method, tc.path, code)
		}
	}
}

// TestAuthWithRoles_UnknownAndMissingKeys 未知 Key 与缺 Token 仍按 401/401 处理（不因角色机制降级为放行）。
func TestAuthWithRoles_UnknownAndMissingKeys(t *testing.T) {
	r := newRoleRouter("write-key", "reader-key")
	if code := doWithKey(r, http.MethodGet, "/api/audit/chain/verify", "guessed-key"); code != http.StatusUnauthorized {
		t.Errorf("unknown key: status = %d, want 401", code)
	}
	if code := doWithKey(r, http.MethodGet, "/api/audit/chain/verify", ""); code != http.StatusUnauthorized {
		t.Errorf("missing token: status = %d, want 401", code)
	}
}

// TestAuthWithRoles_EmptyReaderKeyDegradesToSingleKey readerKey 为空时必须与 Auth(apiKey) 完全同构（存量部署零影响）。
func TestAuthWithRoles_EmptyReaderKeyDegradesToSingleKey(t *testing.T) {
	r := newRoleRouter("write-key", "")
	if code := doWithKey(r, http.MethodGet, "/api/audit/chain/verify", "reader-key"); code != http.StatusUnauthorized {
		t.Errorf("empty readerKey, foreign key: status = %d, want 401", code)
	}
	if code := doWithKey(r, http.MethodPost, "/api/audit/logs", "write-key"); code != http.StatusOK {
		t.Errorf("empty readerKey, write key: status = %d, want 200", code)
	}
}

// TestAuthWithRoles_HealthExempt 探活路径豁免语义与 Auth 保持一致。
func TestAuthWithRoles_HealthExempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthWithRoles("write-key", "reader-key", readerEndpoints))
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	if code := doWithKey(r, http.MethodGet, "/health", ""); code != http.StatusOK {
		t.Errorf("/health: status = %d, want 200 (exempt)", code)
	}
}

// TestAuthWithRoles_MetricsRequiresAuth /metrics 纳入鉴权（P1-6）：无 Key 返回 401。
func TestAuthWithRoles_MetricsRequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthWithRoles("write-key", "reader-key", readerEndpoints))
	r.GET("/metrics", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	if code := doWithKey(r, http.MethodGet, "/metrics", ""); code != http.StatusUnauthorized {
		t.Errorf("/metrics without key: status = %d, want 401", code)
	}
	if code := doWithKey(r, http.MethodGet, "/metrics", "write-key"); code != http.StatusOK {
		t.Errorf("/metrics with write key: status = %d, want 200", code)
	}
}

// TestIsReadOnlyEndpoint_PathBoundary 前缀匹配必须以 "/" 为边界，防止 /api/audit/logs 越到 /api/audit/logs-backup。
func TestIsReadOnlyEndpoint_PathBoundary(t *testing.T) {
	getLogs := []ReadOnlyEndpoint{{Method: http.MethodGet, Path: "/api/audit/logs"}}
	if !isReadOnlyEndpoint(http.MethodGet, "/api/audit/logs/", getLogs) {
		t.Error("trailing slash form of the exact path should match")
	}
	if isReadOnlyEndpoint(http.MethodGet, "/api/audit/logs-backup", getLogs) {
		t.Error("sibling path must not match a /api/audit/logs prefix entry")
	}
	if isReadOnlyEndpoint(http.MethodPost, "/api/audit/logs", getLogs) {
		t.Error("method must be checked, not only path")
	}
}

// TestAuthWithRoles_ReaderKeyNeverTreatedAsWriteKey 断言拒绝响应体不会泄露可枚举信息（统一 403 文案）。
func TestAuthWithRoles_ReaderKeyNeverTreatedAsWriteKey(t *testing.T) {
	r := newRoleRouter("write-key", "reader-key")
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/audit/logs", nil)
	req.Header.Set("Authorization", "Bearer reader-key")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "FORBIDDEN") {
		t.Errorf("body = %s, want standard FORBIDDEN envelope", body)
	}
}
