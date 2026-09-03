// Package envelope provides cross-service standard API request and response envelopes.
package envelope

import "net/http"

// ErrorEnvelope is the unified error response body structure (standard 5-field envelope).
type ErrorEnvelope struct {
	Code      string `json:"code"`             // 机器可读标准错误码（如 "INVALID_ARGUMENT", "UNAUTHORIZED"）
	Message   string `json:"message"`          // 人类可读错误摘要
	Detail    any    `json:"detail,omitempty"` // 详细上下文或字段级错误（可选，若为 nil 则不序列化）
	TraceID   string `json:"trace_id"`         // 分布式链路追踪 ID
	Timestamp string `json:"timestamp"`        // UTC 纳秒级时间戳（RFC3339Nano 格式）
}

// SuccessEnvelope is the unified success response body structure.
type SuccessEnvelope struct {
	Code      string `json:"code"`           // 固定为 "OK"
	Message   string `json:"message"`        // 人类可读成功摘要
	Data      any    `json:"data,omitempty"` // 成功返回的业务载荷（若为 nil 则忽略）
	TraceID   string `json:"trace_id"`       // 分布式链路追踪 ID
	Timestamp string `json:"timestamp"`      // UTC 纳秒级时间戳
}

// ErrorCodeFromStatus maps an HTTP status code to a machine-readable error code.
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
	case http.StatusMethodNotAllowed:
		return "METHOD_NOT_ALLOWED"
	case http.StatusConflict:
		return "CONFLICT"
	case http.StatusRequestEntityTooLarge:
		return "PAYLOAD_TOO_LARGE"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusBadGateway:
		return "BAD_GATEWAY"
	case http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	default:
		if status >= 500 {
			return "INTERNAL_ERROR"
		}
		return "UNKNOWN_ERROR"
	}
}
