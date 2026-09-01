// Package observability provides shared observability primitives for all PrivShield Go modules.
package observability

import (
	"context"
	"os"
	"sync"
)

// Tracer is the abstract tracing interface.
type Tracer interface {
	// StartSpan starts a new span and returns the context and a finish function.
	StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func())
}

// NoOpTracer is a no-op tracer (default).
type NoOpTracer struct{}

// StartSpan no-op implementation.
func (t *NoOpTracer) StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	return ctx, func() {}
}

// OTelTracer wraps an OpenTelemetry tracer (placeholder until OTel SDK is introduced).
type OTelTracer struct {
	Endpoint    string
	ServiceName string
}

// StartSpan OTel implementation (currently degraded to no-op).
func (t *OTelTracer) StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	// TODO: implement real span creation when go.opentelemetry.io/otel is introduced.
	return ctx, func() {}
}

var (
	tracer     Tracer
	tracerOnce sync.Once
)

// InitTracing initializes the tracer.
// If endpoint is empty, it reads OTEL_EXPORTER_OTLP_ENDPOINT.
// If no endpoint is configured, a NoOpTracer is returned.
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

		if endpoint != "" {
			tracer = &OTelTracer{
				Endpoint:    endpoint,
				ServiceName: serviceName,
			}
		} else {
			tracer = &NoOpTracer{}
		}
	})
	return tracer
}

// GetTracer returns the current tracer. Returns a NoOpTracer if not initialized.
func GetTracer() Tracer {
	if tracer == nil {
		return &NoOpTracer{}
	}
	return tracer
}

// ResetTracing resets the tracer singleton (testing only).
func ResetTracing() {
	tracerOnce = sync.Once{}
	tracer = nil
}

// StartSpan is a convenience function.
func StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	return GetTracer().StartSpan(ctx, name, attrs)
}

// TracingEnabled reports whether an OTLP endpoint is configured.
func TracingEnabled() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}
