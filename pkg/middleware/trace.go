// Package middleware — full-chain distributed tracing middleware.
// Package middleware — 全链路分布式追踪与上下文传递中间件。
//
// ==============================================================================
// 【全链路追踪架构设计】
// 1. 【双响应头统一注入】：
//    同时维护 X-Request-ID（业界通用）与 X-Trace-ID（分布式追踪标准），确保前端 React UI、
//    网关层、BFF 层、调度中枢 service-hub 与核心隐私引擎 Agent 拥有统一的端到端调用链关联；
// 2. 【多级无缝兼容与继承】：
//    - 优先复用上游 RequestID() 中间件已生成的 request_id；
//    - 其次读取入站 HTTP 请求头 X-Request-ID；
//    - 若均为空则自动调用 generateRequestID() 生成高精度带加密随机后缀的 TraceID；
// 3. 【跨协议上下文自动传播】：
//    中间件自动将 TraceID 绑定到 request.Context()，当下游使用 pkg/agent 发起上游 HTTP 或 gRPC 调用时，
//    追踪上下文将无缝透传，保证跨机/跨进程日志的可追溯性。
// ==============================================================================

package middleware

import (
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

// TraceMiddleware returns a Gin middleware that propagates distributed tracing context.
//
// TraceMiddleware 返回传播分布式追踪上下文的 Gin 中间件。
//
// 执行逻辑：
// 1. 优先检查 gin.Context 中是否已存在 "request_id"（向后兼容旧版 RequestID 中间件）；
// 2. 若无，则读取客户端传入的 X-Request-ID 请求头；
// 3. 若仍无，则自动生成符合规范的唯一 TraceID；
// 4. 将 TraceID 同时以 "request_id" 和 TraceIDContextKey 两个键名存入 gin.Context；
// 5. 将 TraceID 写入 HTTP 响应头 X-Request-ID 与 X-Trace-ID；
// 6. 将 TraceID 注入 request.Context()（通过 pkgagent.ContextWithRequestID），确保后续下游 HTTP 请求自动携带该头；
// 7. 调用 c.Next() 继续执行请求链。
func TraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var traceID string

		// 1. Reuse existing request_id from RequestID() middleware if present
		// 若 RequestID() 中间件已设置 request_id，则复用（向后兼容）
		if rid, exists := c.Get("request_id"); exists {
			if s, ok := rid.(string); ok && s != "" {
				traceID = s
			}
		}

		// 2. Fall back to inbound header
		// 回退到入站头
		if traceID == "" {
			traceID = c.GetHeader(TraceHeader)
		}

		// 3. Generate if still empty
		// 若仍为空则生成
		if traceID == "" {
			traceID = generateRequestID()
		}

		// 4. Store in context under both keys for backward compatibility
		// 以两个键名存储于上下文中，保持向后兼容
		c.Set("request_id", traceID)
		c.Set(TraceIDContextKey, traceID)

		// 5. Set response headers
		// 设置响应头
		c.Header(TraceHeader, traceID)
		c.Header(TraceIDHeader, traceID)

		// 6. Inject into request context for downstream HTTP client propagation
		// 注入到 request context，使下游 HTTP 客户端（如 pkg/agent）自动传播
		ctx := pkgagent.ContextWithRequestID(c.Request.Context(), traceID)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
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
	// 1. Check dedicated trace key / 检查专用追踪键
	if val, ok := c.Get(TraceIDContextKey); ok {
		if s, ok := val.(string); ok && s != "" {
			return s
		}
	}
	// 2. Fall back to request_id key (backward compat) / 回退到 request_id 键
	if val, ok := c.Get("request_id"); ok {
		if s, ok := val.(string); ok && s != "" {
			return s
		}
	}
	// 3. Fall back to header / 回退到头
	if rid := c.GetHeader(TraceHeader); rid != "" {
		return rid
	}
	// 4. Generate new / 生成新 ID
	return generateRequestID()
}
