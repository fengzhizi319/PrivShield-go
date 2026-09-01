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

	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
)

// 常量别名：保持历史调用方兼容，实际取值来自 pkg/observability。
const (
	// TraceIDContextKey 是在 gin.Context 中存储追踪 ID 的专属键名。
	TraceIDContextKey = pkgobs.TraceIDContextKey

	// TraceHeader 是入站与出站的主追踪 HTTP 头部字段（X-Request-ID）。
	TraceHeader = pkgobs.TraceHeader

	// TraceIDHeader 是入站与出站的辅助追踪 HTTP 头部字段（X-Trace-ID）。
	TraceIDHeader = pkgobs.TraceIDHeader
)

// TraceMiddleware returns a Gin middleware that propagates distributed tracing context.
//
// TraceMiddleware 返回传播分布式追踪上下文的 Gin 中间件。
// 实际实现已下沉至 pkg/observability.TraceMiddleware；本函数保留为兼容别名。
func TraceMiddleware() gin.HandlerFunc {
	return pkgobs.TraceMiddleware()
}

// GetTraceID retrieves the trace ID from gin.Context.
//
// GetTraceID 从 gin.Context 中安全提取当前请求的 TraceID。
// 实际实现已下沉至 pkg/observability.GetTraceID；本函数保留为兼容别名。
func GetTraceID(c *gin.Context) string {
	return pkgobs.GetTraceID(c)
}
