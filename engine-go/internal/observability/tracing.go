// Package observability 提供可观测性基础设施。
package observability

import (
	"context"

	pkgobs "github.com/fengzhizi319/PrivShield/pkg/observability"
)

// Tracer 抽象追踪器接口（已下沉至 pkg/observability）。
type Tracer = pkgobs.Tracer

// NoOpTracer 不执行任何操作的追踪器（已下沉至 pkg/observability）。
type NoOpTracer = pkgobs.NoOpTracer

// OTelTracer 包装 OpenTelemetry tracer（已下沉至 pkg/observability）。
type OTelTracer = pkgobs.OTelTracer

// InitTracing 初始化追踪器（委托给 pkg/observability）。
func InitTracing(endpoint, serviceName string) Tracer {
	return pkgobs.InitTracing(endpoint, serviceName)
}

// GetTracer 返回当前追踪器（委托给 pkg/observability）。
func GetTracer() Tracer {
	return pkgobs.GetTracer()
}

// ResetTracing 重置追踪器（委托给 pkg/observability）。
func ResetTracing() {
	pkgobs.ResetTracing()
}

// StartSpan 便捷函数（委托给 pkg/observability）。
func StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	return pkgobs.StartSpan(ctx, name, attrs)
}

// TracingEnabled 返回是否配置了 OTLP endpoint（委托给 pkg/observability）。
func TracingEnabled() bool {
	return pkgobs.TracingEnabled()
}
