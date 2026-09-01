// Package middleware — unified API error envelope for cross-language consistency.
// Package middleware — 跨语言统一 API 响应信封（5 字段规范）。
//
// ==============================================================================
// 【架构规范与跨语言一致性】
// 所有 Go 微服务（service-hub / datasource-mgr / audit-log / bff-go / app-lz）
// 与 Python 隐私计算引擎（engine/observability/envelope.py）必须严格共享统一响应格式：
//
// 统一错误信封（ErrorEnvelope）：
//
//	{
//	  "code":      "INVALID_ARGUMENT",      // 机器可读错误码枚举（全大写下划线）
//	  "message":   "请求参数校验失败",        // 人类可读简短摘要
//	  "detail":    "...",                   // 详细错误细节或字段列表（可选）
//	  "trace_id":  "req-1787554500-abc123", // 全链路分布式追踪 ID
//	  "timestamp": "2026-08-31T09:30:00.000Z" // UTC ISO8601/RFC3339 纳秒级时间戳
//	}
//
// 统一成功信封（SuccessEnvelope）：
//
//	{
//	  "code":      "OK",
//	  "message":   "操作成功",
//	  "data":      { ... },
//	  "trace_id":  "req-1787554500-abc123",
//	  "timestamp": "2026-08-31T09:30:00.000Z"
//	}
//
// 响应头强制注入：X-Request-ID 与 X-Trace-ID 双头，与响应体内部的 trace_id 严格一致。
// ==============================================================================

package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// ErrorEnvelope is the unified error response body structure.
// ErrorEnvelope 统一错误响应体结构（标准 5 字段信封）。
type ErrorEnvelope struct {
	Code      string `json:"code"`             // 机器可读标准错误码（如 "INVALID_ARGUMENT", "UNAUTHORIZED"）
	Message   string `json:"message"`          // 人类可读错误摘要
	Detail    any    `json:"detail,omitempty"` // 详细上下文或字段级错误（可选，若为 nil 则不序列化）
	TraceID   string `json:"trace_id"`         // 分布式链路追踪 ID
	Timestamp string `json:"timestamp"`        // UTC 纳秒级时间戳（RFC3339Nano 格式）
}

// AbortWithError aborts the request and responds with a unified error envelope.
//
// AbortWithError 中断当前请求并以统一错误信封格式输出 JSON 错误响应。
//
// 使用方法：
// 业务 Handler 参数校验失败、鉴权失败或执行出错时调用：
// ```go
// middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "参数缺失", gin.H{"field": "name"})
// ```
//
// 执行逻辑：
// 1. 调用 GetTraceID(c) 提取当前链路的 traceID；
// 2. 将 traceID 注入 X-Request-ID 与 X-Trace-ID 响应头；
// 3. 构建 ErrorEnvelope 结构体，并调用 c.AbortWithStatusJSON(httpStatus, env) 终止请求链。
func AbortWithError(c *gin.Context, httpStatus int, code string, message string, detail any) {
	traceID := GetTraceID(c)
	c.Header("X-Request-ID", traceID)
	c.Header("X-Trace-ID", traceID)
	c.AbortWithStatusJSON(httpStatus, ErrorEnvelope{
		Code:      code,
		Message:   message,
		Detail:    detail,
		TraceID:   traceID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// SuccessEnvelope is the unified success response body structure (optional).
// SuccessEnvelope 统一成功响应体结构。
type SuccessEnvelope struct {
	Code      string `json:"code"`           // 固定为 "OK"
	Message   string `json:"message"`        // 人类可读成功摘要
	Data      any    `json:"data,omitempty"` // 成功返回的业务载荷（若为 nil 则忽略）
	TraceID   string `json:"trace_id"`       // 分布式链路追踪 ID
	Timestamp string `json:"timestamp"`      // UTC 纳秒级时间戳
}

// RespondWithSuccess responds with a unified success envelope.
//
// RespondWithSuccess 以统一成功信封格式向客户端输出响应。
//
// 执行逻辑：
// 提取 traceID，注入双响应头，并以 JSON 格式输出 SuccessEnvelope。
func RespondWithSuccess(c *gin.Context, httpStatus int, message string, data any) {
	traceID := GetTraceID(c)
	c.Header("X-Request-ID", traceID)
	c.Header("X-Trace-ID", traceID)
	c.JSON(httpStatus, SuccessEnvelope{
		Code:      "OK",
		Message:   message,
		Data:      data,
		TraceID:   traceID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// ErrorCodeFromStatus maps HTTP status codes to standard error code strings.
//
// ErrorCodeFromStatus 将标准 HTTP 状态码映射为全系统统一的机器可读错误码字符串。
// 与 Python FastAPI 引擎的 code_map 保持严格映射一致。
func ErrorCodeFromStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "CONFLICT"
	case http.StatusRequestEntityTooLarge:
		return "PAYLOAD_TOO_LARGE"
	case http.StatusTooManyRequests:
		return "RATE_LIMITED"
	case http.StatusInternalServerError:
		return "INTERNAL_ERROR"
	case http.StatusServiceUnavailable:
		return "UPSTREAM_UNAVAILABLE"
	default:
		return "UNKNOWN_ERROR"
	}
}

// ExtractErrorMessage extracts the best error message from various response formats.
//
// ExtractErrorMessage 从多种响应格式与上下文存储中自适应提取最合适的人类可读错误消息。
// 依次查找："error_code" -> "message" -> "detail" -> "error" -> fallback -> http.StatusText。
func ExtractErrorMessage(c *gin.Context, fallback string) string {
	// Try unified envelope format / 尝试统一信封格式
	if code, exists := c.Get("error_code"); exists {
		if s, ok := code.(string); ok && s != "" {
			return s
		}
	}
	// Try common error fields / 尝试常见错误字段
	for _, key := range []string{"message", "detail", "error"} {
		if val, exists := c.Get(key); exists {
			if s, ok := val.(string); ok && s != "" {
				return s
			}
		}
	}
	// Fallback to HTTP status text / 回退到 HTTP 状态文本
	if fallback != "" {
		return fallback
	}
	return http.StatusText(c.Writer.Status())
}
