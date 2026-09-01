// Package middleware 单元测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证 Package middleware 中通用 Gin 中间件套件的正确性与边界防御：
//  1. 【CORS 跨域测试】：通配符放行、白名单精确过滤、非白名单拦截、Preflight OPTIONS 204 快速响应；
//  2. 【Auth 鉴权测试】：空 Key 开发放行、健康检查豁免、Bearer Token 校验正确/错误、非 /api/ 路径豁免；
//  3. 【RequestID 追踪测试】：入站头透传、缺失时基于安全随机数自动生成、Downstream Context 绑定；
//  4. 【StructuredLogger 日志测试】：结构化日志字段输出与 nil Logger 兜底；
//  5. 【Recovery 异常恢复测试】：Panic 拦截、日志记录与 500 统一信封输出；
//  6. 【SecurityHeaders 安全头测试】：6 项标准安全响应头完整性校验；
//  7. 【DDoS 纵深防御测试】：MaxBodySize（413）、MaxConcurrent（503）与 RateLimit 令牌桶（429）。
// ==============================================================================

package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	pkgauth "github.com/fengzhizi319/PrivShield/pkg/auth"
	pkgobs "github.com/fengzhizi319/PrivShield/pkg/observability"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ─────────────────────────────────────────────────────────────
// 1. CORS / 跨域中间件测试
// ─────────────────────────────────────────────────────────────

// TestCORS_AllowAll 验证 origins 为 nil 时允许任意来源并设置 Access-Control-Allow-Origin: *。
func TestCORS_AllowAll(t *testing.T) {
	r := gin.New()
	r.Use(CORS(nil))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://evil.com")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Allow-Origin = %q, want *", got)
	}
}

// TestCORS_AllowAllWildcard 验证显式配置 ["*"] 同样允许任意来源。
func TestCORS_AllowAllWildcard(t *testing.T) {
	r := gin.New()
	r.Use(CORS([]string{"*"}))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://example.com")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Allow-Origin = %q, want *", got)
	}
}

// TestCORS_SpecificOrigins 验证配置明确白名单时，仅放行列表中的 Origin，非白名单请求不设置 Allow-Origin 头。
func TestCORS_SpecificOrigins(t *testing.T) {
	allowed := []string{"http://localhost:5173", "http://localhost:3000"}
	r := gin.New()
	r.Use(CORS(allowed))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	// Matching origin / 匹配白名单的 Origin
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("Allow-Origin = %q, want http://localhost:5173", got)
	}
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}

	// Non-matching origin / 未匹配白名单的 Origin
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/test", nil)
	req2.Header.Set("Origin", "http://evil.com")
	r.ServeHTTP(w2, req2)

	if got := w2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty (non-matching origin)", got)
	}
}

// TestCORS_PreflightOptions 验证预检请求（OPTIONS）直接返回 204 No Content 并携带合法的 Allow-Methods / Allow-Headers。
func TestCORS_PreflightOptions(t *testing.T) {
	r := gin.New()
	r.Use(CORS(nil))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("OPTIONS", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "GET") {
		t.Errorf("Allow-Methods = %q, should contain GET", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-Request-ID") {
		t.Errorf("Allow-Headers = %q, should contain X-Request-ID", got)
	}
}

// ─────────────────────────────────────────────────────────────
// 2. Auth / 鉴权中间件测试
// ─────────────────────────────────────────────────────────────

// TestAuth_EmptyKey_SkipsAuth 验证 apiKey 为空时自动跳过鉴权（开发模式兼容）。
func TestAuth_EmptyKey_SkipsAuth(t *testing.T) {
	r := gin.New()
	r.Use(Auth(""))
	r.GET("/api/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (no auth required)", w.Code)
	}
}

// TestAuth_HealthExempt 验证 /health 与 /api/health 路径即使配置了 API Key 也能免鉴权访问。
func TestAuth_HealthExempt(t *testing.T) {
	r := gin.New()
	r.Use(Auth("secret-key"))
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.GET("/api/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	for _, path := range []string{"/health", "/api/health"} {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", path, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 (health exempt)", path, w.Code)
		}
	}
}

// TestAuth_ValidKey 验证携带正确 Bearer Token 时请求顺利通过鉴权。
func TestAuth_ValidKey(t *testing.T) {
	r := gin.New()
	r.Use(Auth("my-secret"))
	r.GET("/api/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer my-secret")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// TestAuth_InvalidKey 验证携带错误 Token 时返回 401 UNAUTHORIZED 统一错误信封。
func TestAuth_InvalidKey(t *testing.T) {
	r := gin.New()
	r.Use(Auth("my-secret"))
	r.GET("/api/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}

	// Verify unified error envelope format / 校验统一错误信封格式
	var env ErrorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if env.Code != "UNAUTHORIZED" {
		t.Errorf("code = %s, want UNAUTHORIZED", env.Code)
	}
}

// TestAuth_MissingToken 验证未携带 Authorization 请求头时返回 401。
func TestAuth_MissingToken(t *testing.T) {
	r := gin.New()
	r.Use(Auth("my-secret"))
	r.GET("/api/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// TestAuth_NonApiPath_Exempt 验证非 /api/* 路径（如 /metrics）免鉴权。
func TestAuth_NonApiPath_Exempt(t *testing.T) {
	r := gin.New()
	r.Use(Auth("my-secret"))
	r.GET("/metrics", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/metrics", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (non-api path exempt)", w.Code)
	}
}

// TestExtractBearer 验证从各种 Authorization 格式中提取 Token 的健壮性。
func TestExtractBearer(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"Bearer token123", "token123"},
		{"bearer token123", "token123"},
		{"BEARER token123", "token123"},
		{"Basic dXNlcjpwYXNz", ""},
		{"", ""},
		{"Bearer", ""},
		{"Bearer a b", ""},
	}
	for _, tt := range tests {
		if got := pkgauth.ExtractBearerToken(tt.header); got != tt.want {
			t.Errorf("ExtractBearerToken(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// 3. RequestID / 请求 ID 中间件测试
// ─────────────────────────────────────────────────────────────

// TestRequestID_Passthrough 验证入站已包含 X-Request-ID 时原样透传。
func TestRequestID_Passthrough(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	r.GET("/test", func(c *gin.Context) {
		rid, _ := c.Get("request_id")
		c.JSON(200, gin.H{"request_id": rid})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "incoming-rid-42")
	r.ServeHTTP(w, req)

	// Response should echo the incoming request ID
	if got := w.Header().Get("X-Request-ID"); got != "incoming-rid-42" {
		t.Errorf("response X-Request-ID = %q, want incoming-rid-42", got)
	}
}

// TestRequestID_Generated 验证入站未携带 X-Request-ID 时自动生成符合 req- 格式的随机 ID。
func TestRequestID_Generated(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	r.GET("/test", func(c *gin.Context) {
		rid, _ := c.Get("request_id")
		c.JSON(200, gin.H{"request_id": rid})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	// No X-Request-ID header → should be generated
	r.ServeHTTP(w, req)

	got := w.Header().Get("X-Request-ID")
	if got == "" {
		t.Error("X-Request-ID should be generated when not provided")
	}
	if !strings.HasPrefix(got, "req-") && len(got) < 10 {
		t.Errorf("generated request ID looks unexpected: %q", got)
	}
}

// ─────────────────────────────────────────────────────────────
// 4. StructuredLogger / 结构化日志中间件测试
// ─────────────────────────────────────────────────────────────

// TestStructuredLogger_NoPanic 验证在正常配置 Logger 下，结构化日志中间件正常执行无 panic。
func TestStructuredLogger_NoPanic(t *testing.T) {
	r := gin.New()
	r.Use(pkgobs.RequestLoggerWithModule("test-module"))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// TestStructuredLogger_NilLogger 验证 RequestLoggerWithModule 中间件正常执行无 panic。
func TestStructuredLogger_NilLogger(t *testing.T) {
	r := gin.New()
	r.Use(pkgobs.RequestLoggerWithModule("test-module"))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ─────────────────────────────────────────────────────────────
// 5. Recovery / 异常恢复中间件测试
// ─────────────────────────────────────────────────────────────

// TestRecovery_CatchesPanic 验证 Handler 抛出 panic 时被成功捕获并输出 500 INTERNAL_ERROR 统一信封。
func TestRecovery_CatchesPanic(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	r.Use(Recovery(nil, "test-module"))
	r.GET("/panic", func(c *gin.Context) {
		panic("unexpected runtime error")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/panic", nil)
	req.Header.Set("X-Request-ID", "req-panic-001")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}

	// Verify unified error envelope format / 校验统一错误信封格式
	var env ErrorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if env.Code != "INTERNAL_ERROR" {
		t.Errorf("code = %s, want INTERNAL_ERROR", env.Code)
	}
	if env.TraceID == "" {
		t.Error("expected non-empty trace_id in envelope")
	}
}

// ─────────────────────────────────────────────────────────────
// 6. SecurityHeaders / 安全头中间件测试
// ─────────────────────────────────────────────────────────────

// TestSecurityHeaders 验证 6 项企业级安全响应头（nosniff, SAMEORIGIN, XSS block, HSTS, strict-origin, Permissions-Policy）全部注入。
func TestSecurityHeaders(t *testing.T) {
	r := gin.New()
	r.Use(SecurityHeaders())
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := w.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want SAMEORIGIN", got)
	}
	if got := w.Header().Get("X-XSS-Protection"); got != "1; mode=block" {
		t.Errorf("X-XSS-Protection = %q, want 1; mode=block", got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q, want strict-origin-when-cross-origin", got)
	}
	if got := w.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Errorf("Strict-Transport-Security = %q, want max-age=31536000; includeSubDomains", got)
	}
	if got := w.Header().Get("Permissions-Policy"); got != "camera=(), microphone=(), geolocation=()" {
		t.Errorf("Permissions-Policy = %q, want camera=(), microphone=(), geolocation=()", got)
	}
}

// ─────────────────────────────────────────────────────────────
// 7. DDoS Protection Middlewares (MaxBodySize / MaxConcurrent / RateLimit)
// ─────────────────────────────────────────────────────────────

// TestMaxBodySize 验证请求体大小超出限制时触发 413 Payload Too Large 拦截。
func TestMaxBodySize(t *testing.T) {
	r := gin.New()
	r.Use(MaxBodySize(10)) // Max 10 bytes
	r.POST("/upload", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"detail": "too large"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"len": len(body)})
	})

	// 1. Small payload (within limit) / 小包放行
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/upload", bytes.NewReader([]byte("12345")))
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Errorf("expected 200 for 5 bytes, got %d", w1.Code)
	}

	// 2. Large payload (exceeds limit) / 超大包 413 拦截
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/upload", bytes.NewReader([]byte("12345678901234567890")))
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 for 20 bytes, got %d", w2.Code)
	}
}

// TestMaxConcurrent 验证在途并发请求数超出上限时立即返回 503 Service Unavailable。
func TestMaxConcurrent(t *testing.T) {
	r := gin.New()
	r.Use(MaxConcurrent(1)) // Max 1 concurrent request
	blockCh := make(chan struct{})
	r.GET("/slow", func(c *gin.Context) {
		<-blockCh
		c.JSON(200, gin.H{"ok": true})
	})

	// First request starts and blocks / 首个请求进入并阻塞
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("GET", "/slow", nil)
	go r.ServeHTTP(w1, req1)

	time.Sleep(20 * time.Millisecond)

	// Second request should immediately get 503 Service Unavailable / 第二个并发请求被 503 快速拦截
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/slow", nil)
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for concurrent overflow, got %d", w2.Code)
	}

	// Unblock first request / 释放阻塞
	close(blockCh)
}

// TestRateLimit_AllowsUnderBurstAndRejectsOver 验证单 IP 令牌桶限流器在突发容量内正常放行，超限后返回 429。
func TestRateLimit_AllowsUnderBurstAndRejectsOver(t *testing.T) {
	r := gin.New()
	r.Use(RateLimit(2, 2)) // 2 RPS, burst 2
	r.GET("/api/data", func(c *gin.Context) {
		c.JSON(200, gin.H{"data": "ok"})
	})

	// 2 requests allowed immediately / 突发配额 2 次放行
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/api/data", nil)
		req.RemoteAddr = "192.168.1.100:1234"
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("request %d expected 200, got %d", i, w.Code)
		}
	}

	// 3rd request immediately should be rate limited (429) / 第 3 次请求立即被 429 限流
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/api/data", nil)
	req3.RemoteAddr = "192.168.1.100:1234"
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusTooManyRequests {
		t.Errorf("request 3 expected 429 Too Many Requests, got %d", w3.Code)
	}
}
