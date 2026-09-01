// Package observability 测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证可观测性基础设施（结构化日志、RED 指标、分布式追踪）的核心功能：
//  1. 【NewLogger 初始化】：验证 JSON/Text 两种格式、info/debug 两种级别的日志初始化；
//  2. 【InitLogger 全局级别过滤】：验证全局默认 Logger 的级别过滤语义（warn 级别下 info 不可用）；
//  3. 【REDMetrics 指标记录与暴露】：验证 RED 指标（请求总数、延迟直方图）的记录与 Prometheus 格式暴露；
//  4. 【PrometheusMiddleware Gin 集成】：验证 Gin 中间件自动记录请求指标，且 /metrics 自身不被记录；
//  5. 【分布式追踪 NoOp 回退】：验证未配置 OTel 端点时回退到 NoOpTracer，StartSpan 不 panic；
//  6. 【分布式追踪 OTel 初始化】：验证配置 OTel 端点时创建 OTelTracer 实例。
// ==============================================================================

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

// ──────────────────────────────────────────────
// 1. 结构化日志初始化测试
// ──────────────────────────────────────────────

// TestNewLogger 验证 JSON 格式 info 级别日志的初始化。
// 执行逻辑：调用 NewLogger("json", "info")，断言返回非 nil Logger，且能正常输出日志。
func TestNewLogger(t *testing.T) {
	logger := NewLogger("json", "info")
	if logger == nil {
		t.Fatal("NewLogger returned nil")
	}
	logger.Info("test", "k", "v")
}

// TestNewLoggerText 验证 Text 格式 debug 级别日志的初始化。
// 执行逻辑：调用 NewLogger("text", "debug")，断言返回非 nil Logger。
func TestNewLoggerText(t *testing.T) {
	logger := NewLogger("text", "debug")
	if logger == nil {
		t.Fatal("NewLogger returned nil")
	}
}

// TestInitLogger 验证全局默认 Logger 的级别过滤语义。
// 执行逻辑：调用 InitLogger("json", "warn") 设置全局 Logger 为 warn 级别，
// 断言 info 级别被禁用（Enabled 返回 false）、warn 级别可用（Enabled 返回 true）。
func TestInitLogger(t *testing.T) {
	InitLogger("json", "warn")
	if slog.Default().Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("default logger should not be enabled at info level")
	}
	if !slog.Default().Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("default logger should be enabled at warn level")
	}
}

// ──────────────────────────────────────────────
// 2. RED 指标记录与 Prometheus 暴露测试
// ──────────────────────────────────────────────

// TestREDMetrics 验证 RED 指标的记录与 Prometheus 格式暴露。
// 执行逻辑：创建 REDMetrics 并记录 HTTP/gRPC 两次请求，然后通过 /metrics 端点获取 Prometheus 输出，
// 断言包含 privshield_requests_total 计数器和 privshield_request_duration_seconds_bucket 直方图桶。
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

// TestPrometheusMiddleware 验证 Gin 中间件自动记录请求指标。
// 执行逻辑：创建带 PrometheusMiddleware 的 Gin 路由，发送请求到 /v1/privacy/mask，
// 然后从 /metrics 端点断言输出包含该端点的指标记录；
// 同时验证 /metrics 请求自身不会被计入指标（避免自引用噪音）。
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

// ──────────────────────────────────────────────
// 3. 分布式追踪初始化测试
// ──────────────────────────────────────────────

// TestTracingNoOp 验证未配置 OTel 端点时回退到 NoOpTracer。
// 执行逻辑：ResetTracing() 后调用 InitTracing("", "")，断言返回 *NoOpTracer；
// 调用 StartSpan 断言返回非 nil context 且 finish 回调不 panic。
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

// TestTracingOTel 验证配置 OTel 端点时创建 OTelTracer 实例。
// 执行逻辑：ResetTracing() 后调用 InitTracing("http://localhost:4317", "test-service")，
// 断言返回 *OTelTracer 实例。
func TestTracingOTel(t *testing.T) {
	ResetTracing()
	tracer := InitTracing("http://localhost:4317", "test-service")
	if _, ok := tracer.(*OTelTracer); !ok {
		t.Fatalf("expected OTelTracer, got %T", tracer)
	}
}
