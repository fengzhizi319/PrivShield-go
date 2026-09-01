package observability

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// RequestLogger returns a Gin middleware that records structured HTTP access logs.
//
// 字段与行为对齐 engine-go/internal/observability/logger.go 中的历史实现：
// - msg: "HTTP request"
// - 字段: method, path, query, status, duration, client_ip, request_id
// - request_id 优先从 gin.Context（TraceMiddleware / RequestID）读取，其次从请求头回退。
func RequestLogger() gin.HandlerFunc {
	return RequestLoggerWithModule("")
}

// RequestLoggerWithModule returns a Gin middleware that records structured HTTP access logs
// with an optional module tag.
//
// 输出字段与历史 pkg/middleware.StructuredLogger 保持一致，便于全仓库统一日志解析：
// - msg: "request completed"
// - 字段: request_id, method, path, status, latency_ms, client_ip, module
// - path 包含原始 query string（与 StructuredLogger 历史行为一致）
func RequestLoggerWithModule(module string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		if c.Request.URL.RawQuery != "" {
			path = path + "?" + c.Request.URL.RawQuery
		}

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		requestID := GetTraceID(c)

		args := []any{
			"request_id", requestID,
			"method", c.Request.Method,
			"path", path,
			"status", status,
			"latency_ms", latency.Milliseconds(),
			"client_ip", c.ClientIP(),
		}
		if module != "" {
			args = append(args, "module", module)
		}

		slog.Info("request completed", args...)
	}
}
