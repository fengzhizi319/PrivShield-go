// Package observability provides shared observability primitives for all PrivShield Go modules.
package observability

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	pkgagent "github.com/fengzhizi319/PrivShield/pkg/agent"
)

const (
	// TraceIDContextKey 是在 gin.Context 中存储追踪 ID 的专属键名。
	TraceIDContextKey = "PrivShield-Trace-ID"

	// TraceHeader 是入站与出站的主追踪 HTTP 头部字段（X-Request-ID）。
	TraceHeader = "X-Request-ID"

	// TraceIDHeader 是入站与出站的辅助追踪 HTTP 头部字段（X-Trace-ID）。
	TraceIDHeader = "X-Trace-ID"
)

// GenerateRequestID 生成包含高精度时间戳与 4 字节加密级安全随机数的唯一追踪 ID。
// 格式规范：req-<YYYYMMDDHHMMSS-纳秒>-<8位十六进制随机数>
func GenerateRequestID() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	return "req-" + strings.Replace(
		time.Unix(0, time.Now().UnixNano()).Format("20060102150405.000000000"),
		".", "-", 1,
	) + "-" + hex.EncodeToString(buf[:])
}

// GetTraceID retrieves the trace ID from gin.Context.
//
// GetTraceID 从 gin.Context 中安全提取当前请求的 TraceID。
//
// 查找降级顺序（4 级防空兜底）：
// 1. 专属追踪键 TraceIDContextKey（由 TraceMiddleware 写入）；
// 2. "request_id" 键（向后兼容）；
// 3. 入站 HTTP 请求头 X-Request-ID；
// 4. 若以上均未找到，即时生成并返回一个新的唯一 ID。
func GetTraceID(c *gin.Context) string {
	if val, ok := c.Get(TraceIDContextKey); ok {
		if s, ok := val.(string); ok && s != "" {
			return s
		}
	}
	if val, ok := c.Get("request_id"); ok {
		if s, ok := val.(string); ok && s != "" {
			return s
		}
	}
	if rid := c.GetHeader(TraceHeader); rid != "" {
		return rid
	}
	return GenerateRequestID()
}

// TraceMiddleware returns a Gin middleware that propagates distributed tracing context.
//
// 执行逻辑：
// 1. 优先复用上游 RequestID() 中间件已生成的 request_id；
// 2. 若无，则读取客户端传入的 X-Request-ID 请求头；
// 3. 若仍无，则自动生成唯一 TraceID；
// 4. 将 TraceID 同时以 "request_id" 和 TraceIDContextKey 两个键名存入 gin.Context；
// 5. 将 TraceID 写入 HTTP 响应头 X-Request-ID 与 X-Trace-ID；
// 6. 将 TraceID 注入 request.Context()，以便下游 HTTP/gRPC 客户端自动透传。
func TraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var traceID string

		if rid, exists := c.Get("request_id"); exists {
			if s, ok := rid.(string); ok && s != "" {
				traceID = s
			}
		}

		if traceID == "" {
			traceID = c.GetHeader(TraceHeader)
		}

		if traceID == "" {
			traceID = GenerateRequestID()
		}

		c.Set("request_id", traceID)
		c.Set(TraceIDContextKey, traceID)

		c.Header(TraceHeader, traceID)
		c.Header(TraceIDHeader, traceID)

		ctx := pkgagent.ContextWithRequestID(c.Request.Context(), traceID)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
