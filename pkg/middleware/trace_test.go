// Package middleware 单元测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证 Package middleware 中分布式链路追踪中间件的核心特性：
//  1. 【双响应头注入】：验证 TraceMiddleware 同时向客户端注入一致的 X-Request-ID 与 X-Trace-ID；
//  2. 【自动生成唯一 ID】：验证未携带追踪头时自动生成合法 ID；
//  3. 【向后兼容性】：验证 TraceMiddleware 与旧版 RequestID() 中间件串联时能够精准复用同一 ID；
//  4. 【多级降级查找】：验证 GetTraceID 按照专属键 -> 旧版键 -> 请求头 -> 动态生成的 4 级优先级回退逻辑。
// ==============================================================================

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestTraceMiddleware_SetsBothHeaders 验证 TraceMiddleware 能够从入站头捕获并向出站响应同时设置 X-Request-ID 与 X-Trace-ID。
func TestTraceMiddleware_SetsBothHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(TraceMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "req-trace-001")
	router.ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got != "req-trace-001" {
		t.Errorf("X-Request-ID = %s, want req-trace-001", got)
	}
	if got := w.Header().Get("X-Trace-ID"); got != "req-trace-001" {
		t.Errorf("X-Trace-ID = %s, want req-trace-001", got)
	}
}

// TestTraceMiddleware_GeneratesID 验证入站未提供追踪头时自动生成一致的唯一追踪 ID。
func TestTraceMiddleware_GeneratesID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(TraceMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	// No X-Request-ID header / 未携带请求头
	router.ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got == "" {
		t.Error("expected auto-generated X-Request-ID, got empty")
	}
	if got := w.Header().Get("X-Trace-ID"); got == "" {
		t.Error("expected auto-generated X-Trace-ID, got empty")
	}
	// Both headers should be the same / 双头必须完全一致
	if w.Header().Get("X-Request-ID") != w.Header().Get("X-Trace-ID") {
		t.Error("X-Request-ID and X-Trace-ID should be identical")
	}
}

// TestTraceMiddleware_BackwardCompatWithRequestID 验证与旧版 RequestID 中间件混合使用时的向后兼容性与状态一致性。
func TestTraceMiddleware_BackwardCompatWithRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Use both: RequestID first, then TraceMiddleware
	router.Use(RequestID())
	router.Use(TraceMiddleware())

	var capturedTraceID, capturedRequestID string
	router.GET("/test", func(c *gin.Context) {
		if v, ok := c.Get("request_id"); ok {
			capturedRequestID, _ = v.(string)
		}
		capturedTraceID = GetTraceID(c)
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "req-compat-001")
	router.ServeHTTP(w, req)

	// Both should be the same value (TraceMiddleware reuses RequestID's value)
	if capturedRequestID != "req-compat-001" {
		t.Errorf("request_id = %s, want req-compat-001", capturedRequestID)
	}
	if capturedTraceID != "req-compat-001" {
		t.Errorf("GetTraceID = %s, want req-compat-001", capturedTraceID)
	}
	if capturedRequestID != capturedTraceID {
		t.Error("request_id and GetTraceID should return the same value")
	}
}

// TestGetTraceID_FallbackOrder 验证 GetTraceID 的 4 级优先级回退与安全兜底查找算法。
func TestGetTraceID_FallbackOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Case 1: TraceIDContextKey set / 优先专用键
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set(TraceIDContextKey, "trace-key-val")
	c.Set("request_id", "request-id-val")
	if got := GetTraceID(c); got != "trace-key-val" {
		t.Errorf("expected trace-key-val, got %s", got)
	}

	// Case 2: Only request_id set / 其次旧版键
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest("GET", "/", nil)
	c2.Set("request_id", "request-id-val")
	if got := GetTraceID(c2); got != "request-id-val" {
		t.Errorf("expected request-id-val, got %s", got)
	}

	// Case 3: Only header / 再次 HTTP 请求头
	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	c3.Request = httptest.NewRequest("GET", "/", nil)
	c3.Request.Header.Set("X-Request-ID", "header-val")
	if got := GetTraceID(c3); got != "header-val" {
		t.Errorf("expected header-val, got %s", got)
	}

	// Case 4: Nothing set → generates new / 最后自动生成兜底
	c4, _ := gin.CreateTestContext(httptest.NewRecorder())
	c4.Request = httptest.NewRequest("GET", "/", nil)
	if got := GetTraceID(c4); got == "" {
		t.Error("expected generated trace ID, got empty")
	}
}
