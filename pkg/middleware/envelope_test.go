// Package middleware 单元测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证 Package middleware 中统一 API 响应信封的核心逻辑：
//  1. 【错误信封格式】：验证 AbortWithError 正确输出 5 字段 JSON 结构（code, message, detail, trace_id, timestamp）与双头注入；
//  2. 【成功信封格式】：验证 RespondWithSuccess 正确输出 OK 状态与 trace_id；
//  3. 【状态码映射】：验证 ErrorCodeFromStatus 将 400、401、403、404、409、429、500、503 等状态码正确转换为标准字串；
//  4. 【自动生成 TraceID】：验证未设置 X-Request-ID 时，信封会自动生成非空 trace_id 并设置到响应头。
// ==============================================================================

package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestAbortWithError_Format 验证 AbortWithError 输出的 5 字段信封结构完整性与 X-Request-ID / X-Trace-ID 双头注入。
func TestAbortWithError_Format(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/v1/test", nil)
	c.Request.Header.Set("X-Request-ID", "req-test-envelope-001")

	AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "参数校验失败", "field 'name' is required")

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var env ErrorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if env.Code != "INVALID_ARGUMENT" {
		t.Errorf("expected code INVALID_ARGUMENT, got %s", env.Code)
	}
	if env.Message != "参数校验失败" {
		t.Errorf("expected message '参数校验失败', got %s", env.Message)
	}
	if env.TraceID != "req-test-envelope-001" {
		t.Errorf("expected trace_id 'req-test-envelope-001', got %s", env.TraceID)
	}
	if env.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}

	// Verify headers / 验证双响应头
	if got := w.Header().Get("X-Request-ID"); got != "req-test-envelope-001" {
		t.Errorf("expected X-Request-ID header 'req-test-envelope-001', got %s", got)
	}
	if got := w.Header().Get("X-Trace-ID"); got != "req-test-envelope-001" {
		t.Errorf("expected X-Trace-ID header 'req-test-envelope-001', got %s", got)
	}
}

// TestRespondWithSuccess_Format 验证 RespondWithSuccess 输出的成功信封格式与 TraceID 关联。
func TestRespondWithSuccess_Format(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/v1/test", nil)
	c.Request.Header.Set("X-Request-ID", "req-success-001")

	RespondWithSuccess(c, http.StatusOK, "操作成功", map[string]string{"key": "value"})

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var env SuccessEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if env.Code != "OK" {
		t.Errorf("expected code 'OK', got %s", env.Code)
	}
	if env.TraceID != "req-success-001" {
		t.Errorf("expected trace_id 'req-success-001', got %s", env.TraceID)
	}
}

// TestErrorCodeFromStatus 验证 HTTP 状态码到标准机器可读错误码的映射矩阵。
func TestErrorCodeFromStatus(t *testing.T) {
	tests := []struct {
		status   int
		expected string
	}{
		{http.StatusBadRequest, "INVALID_ARGUMENT"},
		{http.StatusUnauthorized, "UNAUTHORIZED"},
		{http.StatusForbidden, "FORBIDDEN"},
		{http.StatusNotFound, "NOT_FOUND"},
		{http.StatusConflict, "CONFLICT"},
		{http.StatusTooManyRequests, "RATE_LIMITED"},
		{http.StatusInternalServerError, "INTERNAL_ERROR"},
		{http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE"},
		{http.StatusTeapot, "UNKNOWN_ERROR"}, // unmapped
	}

	for _, tt := range tests {
		got := ErrorCodeFromStatus(tt.status)
		if got != tt.expected {
			t.Errorf("ErrorCodeFromStatus(%d) = %s, want %s", tt.status, got, tt.expected)
		}
	}
}

// TestAbortWithError_GeneratesTraceID 验证在入站请求未携带追踪头时，信封自动生成 TraceID 并注入响应头。
func TestAbortWithError_GeneratesTraceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// No X-Request-ID header set / 不携带请求头
	c.Request = httptest.NewRequest("GET", "/v1/test", nil)

	AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "服务器内部错误", nil)

	var env ErrorEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)

	if env.TraceID == "" {
		t.Error("expected auto-generated trace_id, got empty string")
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID header to be set")
	}
	if w.Header().Get("X-Trace-ID") == "" {
		t.Error("expected X-Trace-ID header to be set")
	}
}
