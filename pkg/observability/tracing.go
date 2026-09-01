// Package observability provides shared observability primitives for all PrivShield Go modules.
package observability

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
)

// Tracer 是抽象分布式追踪接口，支持 NoOp 与 OpenTelemetry 两种实现。
type Tracer interface {
	// StartSpan 开启一个新的追踪跨度（Span），返回携带 Span 上下文的副本与结束回调函数。
	// 调用方必须在 Span 结束时调用返回的 func() 以完成上报。
	StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func())
}

// NoOpTracer 是默认的空操作追踪器，不产生任何 Span 数据，零开销。
type NoOpTracer struct{}

// StartSpan NoOp 实现：直接返回原上下文与空回调。
func (t *NoOpTracer) StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	return ctx, func() {}
}

// OTelTracer 是 OpenTelemetry 追踪器的占位实现（待引入 go.opentelemetry.io/otel SDK 后完善）。
type OTelTracer struct {
	Endpoint    string // OTLP 导出器目标地址（如 "http://localhost:4318"）
	ServiceName string // 服务名称标签（用于 Span 资源标识）
}

// StartSpan OTel 占位实现：当前降级为 NoOp，待引入 OTel SDK 后创建真实 Span。
func (t *OTelTracer) StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	// TODO: implement real span creation when go.opentelemetry.io/otel is introduced.
	return ctx, func() {}
}

var (
	// tracer 全局追踪器实例，通过 atomic.Pointer 保证无锁安全读写。
	tracer atomic.Pointer[Tracer]
	// tracerOnce 确保 InitTracing 在整个进程生命周期内仅执行一次初始化。
	tracerOnce sync.Once
)

// InitTracing 初始化全局追踪器实例。
// 若 endpoint 为空，回退读取 OTEL_EXPORTER_OTLP_ENDPOINT 环境变量；
// 若最终无任何端点配置，则使用 NoOpTracer（零开销空操作）。
func InitTracing(endpoint, serviceName string) Tracer {
	tracerOnce.Do(func() {
		if endpoint == "" {
			endpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		}
		if serviceName == "" {
			serviceName = os.Getenv("PRIVACY_SERVICE_NAME")
			if serviceName == "" {
				serviceName = "PrivShield"
			}
		}

		var t Tracer
		if endpoint != "" {
			t = &OTelTracer{
				Endpoint:    endpoint,
				ServiceName: serviceName,
			}
		} else {
			t = &NoOpTracer{}
		}
		tracer.Store(&t)
	})
	return *tracer.Load()
}

// GetTracer 返回当前全局追踪器。未初始化时返回 NoOpTracer（零开销）。
func GetTracer() Tracer {
	if p := tracer.Load(); p != nil {
		return *p
	}
	return &NoOpTracer{}
}

// ResetTracing 重置全局追踪器单例（仅限单元测试使用，生产环境禁止调用）。
func ResetTracing() {
	tracerOnce = sync.Once{}
	tracer.Store(nil)
}

// StartSpan 是获取全局 Tracer 并开启 Span 的便捷函数。
func StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	return GetTracer().StartSpan(ctx, name, attrs)
}

// TracingEnabled 报告是否配置了 OTLP 导出端点（即是否启用了分布式追踪）。
func TracingEnabled() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}
