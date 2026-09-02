// Package middleware provides shared Gin middleware for console Go modules.
// Package middleware 为控制台与各 Go 微服务提供标准化的 Gin 中间件套件。
//
// ==============================================================================
// 【包含组件与架构职责】
// 1. 【CORS 跨域资源共享】：支持配置精确来源白名单与预检请求（Preflight OPTIONS 204）处理；
// 2. 【RequestID 追踪注入】：提取或基于安全随机源生成唯一请求标识，注入 Context 与响应头；
// 3. 【StructuredLogger 结构化日志】：基于 log/slog 记录每笔 HTTP 请求的耗时、状态码、客户端 IP 与模块标签；
// 4. 【Recovery 全局异常恢复】：拦截 panic，打印内部堆栈日志，并向外部输出安全标准的 500 错误信封（防代码泄露）；
// 5. 【SecurityHeaders 纵深安全头】：设置 HSTS、X-Frame-Options、nosniff、CSP 等标准安全响应头。
// ==============================================================================

package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
)

// CORS returns a CORS middleware that allows requests from the specified origins.
//
// CORS 返回可配置白名单来源的跨域资源共享中间件。
//
// 安全说明与执行逻辑：
// 1. 若 origins 为空或仅含 "*"：允许任意来源（开发模式），设置 Access-Control-Allow-Origin: *；
// 2. 若 origins 为明确白名单列表：精确匹配客户端的 Origin 请求头，匹配成功设置对应的 Allow-Origin 并添加 Vary: Origin 响应头；
// 3. 预检请求（Method == "OPTIONS"）：直接响应 204 No Content 并中断后续 handler，提升跨域协商效率；
// 4. 配置标准 Allow-Methods、Allow-Headers 及 86400 秒缓存有效期。
func CORS(origins []string) gin.HandlerFunc {
	allowAll := len(origins) == 0 || (len(origins) == 1 && origins[0] == "*")
	originSet := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		originSet[strings.TrimRight(o, "/")] = struct{}{}
	}

	return func(c *gin.Context) {
		reqOrigin := c.GetHeader("Origin")

		if allowAll {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		} else if reqOrigin != "" {
			normalized := strings.TrimRight(reqOrigin, "/")
			if _, ok := originSet[normalized]; ok {
				c.Writer.Header().Set("Access-Control-Allow-Origin", reqOrigin)
				c.Writer.Header().Set("Vary", "Origin")
			}
		}

		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, X-PrivShield-Protocol, X-Forward-Protocol, Accept, Origin")
		c.Writer.Header().Set("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// RequestID returns a middleware that extracts or generates a unique request ID.
//
// RequestID 返回一个提取或自动生成唯一请求 ID 的 Gin 中间件。
//
// 执行逻辑：
// 1. 读取入站 X-Request-ID 请求头（上游 API 网关或负载均衡器可能已生成）；
// 2. 若不存在则调用 generateRequestID() 生成安全随机 ID；
// 3. 写入 gin.Context（键名: "request_id"）供后续日志与 Handler 使用；
// 4. 写入 HTTP 响应头 X-Request-ID 供客户端跟踪；
// 5. 将该 ID 封装进 request.Context()，以便下游 HTTP 客户端（如 pkg/agent）能够自动向下游微服务透传追踪头。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-ID")
		if rid == "" {
			rid = pkgobs.GenerateRequestID()
		}
		c.Set("request_id", rid)
		c.Writer.Header().Set("X-Request-ID", rid)
		// Inject request ID into request context so downstream HTTP clients
		// (e.g. pkg/agent) automatically propagate it as X-Request-ID header.
		// 将请求 ID 注入 request context，使下游 HTTP 客户端
		//（如 pkg/agent）自动将其作为 X-Request-ID 头传播。
		ctx := pkgobs.ContextWithRequestID(c.Request.Context(), rid)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// Recovery returns a Gin middleware that recovers from panics and logs structured errors.
//
// Recovery 返回一个捕获未处理 Panic 的异常恢复中间件。
//
// 安全设计：
// 1. 通过 defer recover() 拦截所有运行时 panic，防止单个请求崩溃导致整个服务进程退出；
// 2. 将详细 panic 堆栈仅输出到内部日志系统（logger.Error），防止敏感源码与系统信息泄漏；
// 3. 调用 AbortWithError 向客户端返回标准的 500 INTERNAL_ERROR 统一错误信封。
func Recovery(logger *slog.Logger, module string) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				requestID := GetTraceID(c)
				logger.Error("panic recovered in handler",
					"request_id", requestID,
					"module", module,
					"panic", fmt.Sprintf("%v", r),
					"path", c.Request.URL.Path,
				)
				AbortWithError(c, http.StatusInternalServerError,
					"INTERNAL_ERROR",
					"Internal Server Error",
					nil,
				)
			}
		}()
		c.Next()
	}
}

// SecurityHeaders returns a middleware that sets recommended HTTP security response headers.
//
// SecurityHeaders 返回设置企业级 HTTP 安全响应头的中间件。
//
// 设置的安全响应头：
//   - X-Content-Type-Options: nosniff — 禁止浏览器对响应 MIME 类型进行猜测嗅探；
//   - X-Frame-Options: SAMEORIGIN — 禁止外部站点通过 iframe 嵌入本页面，防点击劫持（Clickjacking）；
//   - X-XSS-Protection: 1; mode=block — 启用浏览器内置 XSS 过滤器并在检测到攻击时停止渲染；
//   - Strict-Transport-Security (HSTS) — 强制客户端在 1 年内仅使用 HTTPS 访问；
//   - Referrer-Policy: strict-origin-when-cross-origin — 跨域请求时仅发送协议与域名，保护路径隐私；
//   - Permissions-Policy — 严格禁用摄像头、麦克风与地理定位等敏感硬件权限。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		SecurityHeadersTo(c.Writer)
		c.Next()
	}
}

// SecurityHeadersTo writes the standard security response headers to w.
// It is exposed so callers can apply the same headers and override specific
// values (e.g. X-Frame-Options) before the response is sent.
func SecurityHeadersTo(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("X-XSS-Protection", "1; mode=block")
	w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}

// ConfigureTrustedProxies 配置 Gin 引擎的可信代理列表（三级等保 G-02）。
// 仅信任指定 IP/CIDR 的 X-Forwarded-For / X-Real-IP 头部，防止源地址伪造绕过限流。
// 传入 nil 表示不信任任何代理（直接连接模式），传入空 slice 恢复 Gin 默认行为。
func ConfigureTrustedProxies(r *gin.Engine, trustedProxies []string) {
	_ = r.SetTrustedProxies(trustedProxies)
}

// RealClientIP 从请求中提取真实客户端 IP（三级等保 G-02）。
// 优先使用 Gin 的 ClientIP()（已受 TrustedProxies 约束），
// 若为空则回退到 RemoteAddr。该值可安全用于限流 key 与审计日志。
func RealClientIP(c *gin.Context) string {
	if ip := c.ClientIP(); ip != "" {
		return ip
	}
	addr := c.Request.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		return addr[:idx]
	}
	return addr
}
