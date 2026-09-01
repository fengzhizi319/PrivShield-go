package observability

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	pkgmiddleware "github.com/fengzhizi319/PrivShield/pkg/middleware"
)

// RequestLogger returns a Gin middleware that records structured HTTP access logs.
//
// 字段与行为对齐 engine-go/internal/observability/logger.go 中的历史实现：
// - msg: "HTTP request"
// - 字段: method, path, query, status, duration, client_ip, request_id
// - request_id 优先从 gin.Context（TraceMiddleware / RequestID）读取，其次从请求头回退。
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method
		requestID := pkgmiddleware.GetTraceID(c)

		slog.Info("HTTP request",
			"method", method,
			"path", path,
			"query", query,
			"status", status,
			"duration", duration,
			"client_ip", clientIP,
			"request_id", requestID,
		)
	}
}
