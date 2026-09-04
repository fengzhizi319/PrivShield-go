package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ──────────────────────────────────────────────
// EngineMetrics 测试
// ──────────────────────────────────────────────

func TestNewEngineMetrics_RegistersAll(t *testing.T) {
	m := NewEngineMetrics()
	if m.RequestsTotal == nil {
		t.Fatal("RequestsTotal should not be nil")
	}
	if m.RequestDuration == nil {
		t.Fatal("RequestDuration should not be nil")
	}
	if m.ClassificationTotal == nil {
		t.Fatal("ClassificationTotal should not be nil")
	}
	if m.BudgetConsumedTotal == nil {
		t.Fatal("BudgetConsumedTotal should not be nil")
	}
	if m.NerInferenceSeconds == nil {
		t.Fatal("NerInferenceSeconds should not be nil")
	}
}

func TestEngineMetrics_RecordRequest(t *testing.T) {
	m := NewEngineMetrics()
	m.RecordRequest("http", "/v1/privacy/mask", 200, 0.005)
	m.RecordRequest("http", "/v1/privacy/mask", 200, 0.010)
	m.RecordRequest("http", "/v1/privacy/classify/field", 400, 0.001)

	// 验证 /metrics 输出包含指标
	body := scrapeMetrics(t, m.Handler())
	assertContains(t, body, "privshield_requests_total")
	assertContains(t, body, "privshield_request_duration_seconds")
}

func TestEngineMetrics_RecordClassification(t *testing.T) {
	m := NewEngineMetrics()
	m.RecordClassification("rule", "L4", "medical")
	m.RecordClassification("ner", "L5", "identity")

	body := scrapeMetrics(t, m.Handler())
	assertContains(t, body, "privshield_classification_total")
}

func TestEngineMetrics_RecordBudgetConsumed(t *testing.T) {
	m := NewEngineMetrics()
	m.RecordBudgetConsumed("default", "laplace")
	m.RecordBudgetConsumed("default", "gaussian")

	body := scrapeMetrics(t, m.Handler())
	assertContains(t, body, "privshield_budget_consumed_total")
}

func TestEngineMetrics_RecordNerInference(t *testing.T) {
	m := NewEngineMetrics()
	m.RecordNerInference("cuda:0", 8, 0.003)
	m.RecordNerInference("cpu", 1, 0.015)

	body := scrapeMetrics(t, m.Handler())
	assertContains(t, body, "privshield_ner_inference_seconds")
}

func TestEngineMetrics_PrometheusMiddleware(t *testing.T) {
	m := NewEngineMetrics()

	r := gin.New()
	r.Use(m.PrometheusMiddleware())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// 发送几个请求
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	}

	body := scrapeMetrics(t, m.Handler())
	assertContains(t, body, "privshield_requests_total")
	assertContains(t, body, "privshield_request_duration_seconds")
}

func TestEngineMetrics_MiddlewareSkipsMetricsPath(t *testing.T) {
	m := NewEngineMetrics()

	r := gin.New()
	r.Use(m.PrometheusMiddleware())
	r.GET("/metrics", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	body := scrapeMetrics(t, m.Handler())
	// /metrics 路径自身不应被记录
	if strings.Contains(body, `path="/metrics"`) {
		t.Error("/metrics path should not be recorded in metrics")
	}
}

// ──────────────────────────────────────────────
// GatewayMetrics 测试
// ──────────────────────────────────────────────

func TestNewGatewayMetrics_RegistersAll(t *testing.T) {
	m := NewGatewayMetrics()
	if m.BackendInFlight == nil {
		t.Fatal("BackendInFlight should not be nil")
	}
	if m.BackendEWMALatency == nil {
		t.Fatal("BackendEWMALatency should not be nil")
	}
	if m.CircuitBreakerState == nil {
		t.Fatal("CircuitBreakerState should not be nil")
	}
	if m.RequestsTotal == nil {
		t.Fatal("RequestsTotal should not be nil")
	}
}

func TestGatewayMetrics_SetBackendInFlight(t *testing.T) {
	m := NewGatewayMetrics()
	m.SetBackendInFlight("node-1", "127.0.0.1:8079", 5)
	m.SetBackendInFlight("node-2", "127.0.0.1:8080", 3)

	body := scrapeMetrics(t, m.Handler())
	assertContains(t, body, "privshield_gateway_backend_in_flight")
}

func TestGatewayMetrics_SetBackendEWMALatency(t *testing.T) {
	m := NewGatewayMetrics()
	m.SetBackendEWMALatency("node-1", 0.025)

	body := scrapeMetrics(t, m.Handler())
	assertContains(t, body, "privshield_gateway_backend_ewma_latency_seconds")
}

func TestGatewayMetrics_SetCircuitBreakerState(t *testing.T) {
	m := NewGatewayMetrics()
	m.SetCircuitBreakerState("node-1", "closed")
	m.SetCircuitBreakerState("node-2", "open")

	body := scrapeMetrics(t, m.Handler())
	assertContains(t, body, "privshield_gateway_circuit_breaker_state")
}

func TestGatewayMetrics_RecordForwarded(t *testing.T) {
	m := NewGatewayMetrics()
	m.RecordForwarded("node-1", 200)
	m.RecordForwarded("node-1", 500)

	body := scrapeMetrics(t, m.Handler())
	assertContains(t, body, "privshield_gateway_requests_total")
}

func TestGatewayMetrics_MiddlewareSkipsHealthAndMetrics(t *testing.T) {
	m := NewGatewayMetrics()

	r := gin.New()
	r.Use(m.PrometheusMiddleware())
	r.GET("/health", func(c *gin.Context) { c.String(200, "ok") })
	r.GET("/metrics", func(c *gin.Context) { c.String(200, "ok") })
	r.GET("/v1/privacy/mask", func(c *gin.Context) { c.String(200, "ok") })

	// /health 和 /metrics 不应被记录
	for _, path := range []string{"/health", "/metrics"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}
	// /v1/privacy/mask 应被记录
	req := httptest.NewRequest("GET", "/v1/privacy/mask", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	body := scrapeMetrics(t, m.Handler())
	assertContains(t, body, "privshield_gateway_requests_total")
}

// ──────────────────────────────────────────────
// 辅助函数
// ──────────────────────────────────────────────

// scrapeMetrics 通过 Gin handler 抓取 /metrics 输出
func scrapeMetrics(t *testing.T, handler gin.HandlerFunc) string {
	t.Helper()
	r := gin.New()
	r.GET("/metrics", handler)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", w.Code)
	}
	return w.Body.String()
}

// assertContains 校验输出包含指定子串
func assertContains(t *testing.T, body, substr string) {
	t.Helper()
	if !strings.Contains(body, substr) {
		t.Errorf("metrics output missing %q, body:\n%s", substr, body)
	}
}
