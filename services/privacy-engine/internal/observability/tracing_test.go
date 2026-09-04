package observability

import (
	"context"
	"os"
	"testing"
)

func TestNoOpTracer(t *testing.T) {
	tracer := &NoOpTracer{}
	ctx := context.Background()
	ctx2, finish := tracer.StartSpan(ctx, "test-span", nil)
	if ctx2 != ctx {
		t.Error("NoOp tracer should return same context")
	}
	finish() // should not panic
}

func TestInitTracing_NoEndpoint(t *testing.T) {
	ResetTracing()
	defer ResetTracing()

	os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	tracer := InitTracing("", "test-service")

	if _, ok := tracer.(*NoOpTracer); !ok {
		t.Error("expected NoOp tracer when no endpoint configured")
	}
}

func TestInitTracing_WithEndpoint(t *testing.T) {
	ResetTracing()
	defer ResetTracing()

	tracer := InitTracing("http://localhost:4318/v1/traces", "test-service")

	otelTracer, ok := tracer.(*OTelTracer)
	if !ok {
		t.Fatal("expected OTelTracer when endpoint configured")
	}
	if otelTracer.Endpoint != "http://localhost:4318/v1/traces" {
		t.Errorf("unexpected endpoint: %s", otelTracer.Endpoint)
	}
	if otelTracer.ServiceName != "test-service" {
		t.Errorf("unexpected service name: %s", otelTracer.ServiceName)
	}
}

func TestInitTracing_FromEnv(t *testing.T) {
	ResetTracing()
	defer ResetTracing()

	os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://jaeger:4318")
	defer os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")

	tracer := InitTracing("", "")
	otelTracer, ok := tracer.(*OTelTracer)
	if !ok {
		t.Fatal("expected OTelTracer from env var")
	}
	if otelTracer.Endpoint != "http://jaeger:4318" {
		t.Errorf("unexpected endpoint: %s", otelTracer.Endpoint)
	}
}

func TestGetTracer_Default(t *testing.T) {
	ResetTracing()
	defer ResetTracing()

	tracer := GetTracer()
	if _, ok := tracer.(*NoOpTracer); !ok {
		t.Error("expected NoOp tracer by default")
	}
}

func TestStartSpan_Convenience(t *testing.T) {
	ResetTracing()
	defer ResetTracing()

	ctx := context.Background()
	ctx2, finish := StartSpan(ctx, "test", map[string]string{"key": "value"})
	if ctx2 == nil {
		t.Error("expected non-nil context")
	}
	finish()
}

func TestTracingEnabled(t *testing.T) {
	os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if TracingEnabled() {
		t.Error("expected tracing disabled without env var")
	}

	os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	defer os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if !TracingEnabled() {
		t.Error("expected tracing enabled with env var")
	}
}
