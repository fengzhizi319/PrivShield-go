package observability

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNewLogger(t *testing.T) {
	logger := NewLogger("json", "info")
	if logger == nil {
		t.Fatal("NewLogger returned nil")
	}
	logger.Info("test", "k", "v")
}

func TestNewLoggerText(t *testing.T) {
	logger := NewLogger("text", "debug")
	if logger == nil {
		t.Fatal("NewLogger returned nil")
	}
}

func TestInitLogger(t *testing.T) {
	InitLogger("json", "warn")
	if slog.Default().Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("default logger should not be enabled at info level")
	}
	if !slog.Default().Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("default logger should be enabled at warn level")
	}
}

func TestREDMetrics(t *testing.T) {
	m := NewREDMetrics()
	m.RecordRequest("http", "/v1/privacy/mask", 200, 0.01)
	m.RecordRequest("grpc", "/privacy.PrivacyService/Mask", 0, 0.02)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "privshield_requests_total") {
		t.Fatalf("expected privshield_requests_total in metrics output, got:\n%s", body)
	}
	if !strings.Contains(body, "privshield_request_duration_seconds_bucket") {
		t.Fatalf("expected histogram buckets in metrics output, got:\n%s", body)
	}
}

func TestPrometheusMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := NewREDMetrics()
	router := gin.New()
	router.Use(m.PrometheusMiddleware())
	router.GET("/v1/privacy/mask", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodGet, "/v1/privacy/mask", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// 指标请求自身不应被记录。
	metricsRouter := gin.New()
	metricsRouter.GET("/metrics", m.GinHandler())
	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	metricsRouter.ServeHTTP(metricsRec, metricsReq)
	body := metricsRec.Body.String()
	if !strings.Contains(body, "privshield_requests_total{endpoint=\"/v1/privacy/mask\"") {
		t.Fatalf("expected recorded endpoint in metrics, got:\n%s", body)
	}
}

func TestTracingNoOp(t *testing.T) {
	ResetTracing()
	tracer := InitTracing("", "")
	if _, ok := tracer.(*NoOpTracer); !ok {
		t.Fatalf("expected NoOpTracer, got %T", tracer)
	}

	ctx, finish := StartSpan(context.Background(), "test", nil)
	if ctx == nil {
		t.Fatal("StartSpan returned nil context")
	}
	finish()
}

func TestTracingOTel(t *testing.T) {
	ResetTracing()
	tracer := InitTracing("http://localhost:4317", "test-service")
	if _, ok := tracer.(*OTelTracer); !ok {
		t.Fatalf("expected OTelTracer, got %T", tracer)
	}
}
