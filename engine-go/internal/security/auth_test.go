package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	pkgmiddleware "github.com/fengzhizi319/PrivShield-go/pkg/middleware"
)

func init() {
	gin.SetMode(gin.TestMode)
}

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
		{"/debug/pprof/goroutine", "ops:admin"},
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

func TestAuthMiddleware_Disabled(t *testing.T) {
	ResetSettings()
	t.Setenv("PRIVACY_AUTH_ENABLED", "false")
	ResetSettings()

	r := gin.New()
	r.Use(AuthMiddleware())
	r.GET("/test", func(c *gin.Context) {
		id := GetIdentity(c)
		if id == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "no identity"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"name": id.Name})
	})

	w := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, w)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthMiddleware_Enabled_NoToken(t *testing.T) {
	ResetSettings()
	t.Setenv("PRIVACY_AUTH_ENABLED", "true")
	t.Setenv("PRIVACY_AUTH_INTERNAL_API_KEYS", "test-key-1234567890:internal-svc:*")
	ResetSettings()

	r := gin.New()
	r.Use(AuthMiddleware())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, w)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthMiddleware_Enabled_ValidToken(t *testing.T) {
	ResetSettings()
	t.Setenv("PRIVACY_AUTH_ENABLED", "true")
	t.Setenv("PRIVACY_AUTH_INTERNAL_API_KEYS", "test-key-1234567890:internal-svc:*")
	ResetSettings()

	r := gin.New()
	r.Use(AuthMiddleware())
	r.GET("/test", func(c *gin.Context) {
		id := GetIdentity(c)
		c.JSON(http.StatusOK, gin.H{"name": id.Name})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer test-key-1234567890")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRequirePermission(t *testing.T) {
	ResetSettings()
	t.Setenv("PRIVACY_AUTH_ENABLED", "true")
	t.Setenv("PRIVACY_AUTH_INTERNAL_API_KEYS", "limited-key-123456:limited-svc:privacy:mask")
	ResetSettings()

	r := gin.New()
	r.Use(AuthMiddleware())
	r.GET("/protected", RequirePermission("privacy:dp"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer limited-key-123456")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSecurityHeaders(t *testing.T) {
	r := gin.New()
	r.Use(SecurityHeadersMiddleware())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
}

// ──────────────────────────────────────────────
// P1: pprof 端点需要 ops:admin 权限
// ──────────────────────────────────────────────

func TestPprofRequiresOpsAdmin(t *testing.T) {
	ResetSettings()
	t.Setenv("PRIVACY_AUTH_ENABLED", "true")
	// 只有 ops:diagnostics 权限，没有 ops:admin
	t.Setenv("PRIVACY_AUTH_INTERNAL_API_KEYS", "diag-key-1234567890:diag-svc:ops:diagnostics")
	ResetSettings()

	r := gin.New()
	r.Use(AuthMiddleware())
	r.GET("/debug/pprof/heap", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/debug/pprof/heap", nil)
	req.Header.Set("Authorization", "Bearer diag-key-1234567890")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for pprof without ops:admin, got %d", rec.Code)
	}
}

func TestPprofAllowsOpsAdmin(t *testing.T) {
	ResetSettings()
	t.Setenv("PRIVACY_AUTH_ENABLED", "true")
	t.Setenv("PRIVACY_AUTH_INTERNAL_API_KEYS", "admin-key-1234567890:admin-svc:ops:admin")
	ResetSettings()

	r := gin.New()
	r.Use(AuthMiddleware())
	r.GET("/debug/pprof/heap", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest("GET", "/debug/pprof/heap", nil)
	req.Header.Set("Authorization", "Bearer admin-key-1234567890")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for pprof with ops:admin, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ──────────────────────────────────────────────
// P1: 限流器匿名调用者 IP 维度测试
// ──────────────────────────────────────────────

func TestRateLimiter_AnonymousIPDimension(t *testing.T) {
	ResetSettings()
	t.Setenv("PRIVACY_AUTH_ENABLED", "false")
	t.Setenv("PRIVACY_RATE_LIMIT_ENABLED", "true")
	// 极低限流：1 RPS，burst=2
	t.Setenv("PRIVACY_RATE_LIMIT_DEFAULT_RPS", "1")
	t.Setenv("PRIVACY_RATE_LIMIT_DEFAULT_BURST", "2")
	ResetSettings()

	r := gin.New()
	r.Use(func(c *gin.Context) {
		// 注入匿名身份
		c.Set(IdentityContextKey, &Identity{ServiceType: "external", Name: "anonymous"})
		c.Next()
	})
	r.Use(RateLimitMiddleware())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// 来自不同 IP 的请求应分别计数
	for _, ip := range []string{"1.1.1.1", "2.2.2.2"} {
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = ip + ":12345"
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("IP %s request %d: expected 200, got %d", ip, i+1, rec.Code)
			}
		}
	}
}

// ──────────────────────────────────────────────
// P2-19: 限流路径归一化测试
// ──────────────────────────────────────────────

func TestNormalizeRateLimitPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/v1/agent/process/123", "/v1/agent/process/:id"},
		{"/v1/privacy/mask", "/v1/privacy/mask"},
		{"/v1/datasource/42/tables", "/v1/datasource/:id/tables"},
		{"/v1/audit/550e8400-e29b-41d4-a716-446655440000/detail", "/v1/audit/:id/detail"},
		{"/health", "/health"},
	}
	for _, tt := range tests {
		got := pkgmiddleware.NormalizeRateLimitPath(tt.input)
		if got != tt.want {
			t.Errorf("normalizeRateLimitPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
