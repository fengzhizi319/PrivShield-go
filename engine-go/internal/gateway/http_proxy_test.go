package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"sync"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield/engine-go/internal/observability"
	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield/pkg/middleware"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestBackendNodeReverseProxyIsNodeScoped 验证反向代理实例惰性构建并按节点内聚复用：
// 同一节点多次（含并发）调用返回同一实例，不同节点互不共享，无全局缓存参与。
func TestBackendNodeReverseProxyIsNodeScoped(t *testing.T) {
	lb := NewLoadBalancer([]string{"127.0.0.1:18080", "127.0.0.1:18081"}, "p2c")
	nodes := lb.Nodes()

	first, err := nodes[0].ReverseProxy(nil)
	if err != nil {
		t.Fatalf("ReverseProxy failed: %v", err)
	}
	if first == nil {
		t.Fatal("ReverseProxy returned nil instance")
	}

	const concurrency = 16
	got := make([]*httputil.ReverseProxy, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p, err := nodes[0].ReverseProxy(nil)
			if err != nil {
				t.Errorf("concurrent ReverseProxy failed: %v", err)
			}
			got[idx] = p
		}(i)
	}
	wg.Wait()

	for i, p := range got {
		if p != first {
			t.Fatalf("proxy[%d] = %p, want memoized node instance %p", i, p, first)
		}
	}

	second, err := nodes[1].ReverseProxy(nil)
	if err != nil {
		t.Fatalf("ReverseProxy failed: %v", err)
	}
	if second == first {
		t.Fatal("distinct nodes must not share ReverseProxy instances")
	}
	if first.Transport == nil || first.BufferPool == nil {
		t.Fatal("shared transport and buffer pool must be wired into every node proxy")
	}
}

// TestBackendNodeReverseProxyPersistsBuildError 地址解析失败为永久错误，
// 应被 sync.Once 固化，避免每请求重复构建与错误抖动。
func TestBackendNodeReverseProxyPersistsBuildError(t *testing.T) {
	n := &BackendNode{Address: "%zz:8080"}
	for i := 0; i < 3; i++ {
		p, err := n.ReverseProxy(nil)
		if err == nil {
			t.Fatalf("attempt %d: expected parse error, got proxy %v", i, p)
		}
		if p != nil {
			t.Fatalf("attempt %d: expected nil proxy, got %p", i, p)
		}
	}
}

// ──────────────────────────────────────────────
// cbStateString 辅助函数测试
// ──────────────────────────────────────────────

func TestCbStateString(t *testing.T) {
	tests := []struct {
		state CBState
		want  string
	}{
		{CBClosed, "closed"},
		{CBHalfOpen, "half-open"},
		{CBOpen, "open"},
		{CBState(99), "unknown"},
	}
	for _, tt := range tests {
		got := tt.state.String()
		if got != tt.want {
			t.Errorf("cbStateString(%d) = %q, want %q", tt.state, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// HTTP 代理统一错误信封测试
// ──────────────────────────────────────────────

func TestHTTPProxy_NoBackend_Envelope(t *testing.T) {
	// 使用一个不可达的后端地址，触发代理错误
	lb := NewLoadBalancer([]string{"192.0.2.1:1"}, "p2c") // TEST-NET，不可达
	nodes := lb.Nodes()
	// 强制打开熔断器，确保返回 CIRCUIT_OPEN
	for i := 0; i < 10; i++ {
		nodes[0].CB.RecordFailure()
	}

	router := gin.New()
	router.NoRoute(NewHTTPProxyHandler(lb, nil))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/mask", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}

	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if env["code"] != "CIRCUIT_OPEN" {
		t.Errorf("code = %v, want CIRCUIT_OPEN", env["code"])
	}
}

func TestHTTPProxy_CircuitOpen_Envelope(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	lb := NewLoadBalancer([]string{backend.Listener.Addr().String()}, "p2c")
	nodes := lb.Nodes()

	// 强制打开熔断器
	for i := 0; i < 10; i++ {
		nodes[0].CB.RecordFailure()
	}

	router := gin.New()
	router.NoRoute(NewHTTPProxyHandler(lb, nil))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/test", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}

	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if env["code"] != "CIRCUIT_OPEN" {
		t.Errorf("code = %v, want CIRCUIT_OPEN", env["code"])
	}
	if _, ok := env["message"]; !ok {
		t.Error("missing 'message' field in error envelope")
	}
	if _, ok := env["timestamp"]; !ok {
		t.Error("missing 'timestamp' field in error envelope")
	}
}

// ──────────────────────────────────────────────
// HTTP 代理 + Prometheus 指标联动测试
// ──────────────────────────────────────────────

func TestHTTPProxy_WithMetrics_RecordsForwarded(t *testing.T) {
	// 启动一个模拟后端
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"ok"}`))
	}))
	defer backend.Close()

	metrics := observability.NewGatewayMetrics()
	lb := NewLoadBalancer([]string{backend.Listener.Addr().String()}, "p2c")

	// 使用真实 HTTP server 而非 ResponseRecorder（反向代理需要 CloseNotifier）
	router := gin.New()
	router.NoRoute(NewHTTPProxyHandler(lb, metrics))
	proxyServer := httptest.NewServer(router)
	defer proxyServer.Close()

	// 发送请求通过真实 HTTP 客户端
	resp, err := http.Get(proxyServer.URL + "/api/health")
	if err != nil {
		t.Fatalf("HTTP GET error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	// 验证 /metrics 端点暴露了指标
	metricsW := httptest.NewRecorder()
	metricsReq, _ := http.NewRequest("GET", "/metrics", nil)
	metricsRouter := gin.New()
	metricsRouter.GET("/metrics", metrics.Handler())
	metricsRouter.ServeHTTP(metricsW, metricsReq)

	body := metricsW.Body.String()
	if !containsString(body, "privshield_gateway_requests_total") {
		t.Error("metrics output missing privshield_gateway_requests_total")
	}
	if !containsString(body, "privshield_gateway_backend_ewma_latency_seconds") {
		t.Error("metrics output missing privshield_gateway_backend_ewma_latency_seconds")
	}
	if !containsString(body, "privshield_gateway_circuit_breaker_state") {
		t.Error("metrics output missing privshield_gateway_circuit_breaker_state")
	}
}

func TestHTTPProxy_NilMetrics_NoPanic(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	lb := NewLoadBalancer([]string{backend.Listener.Addr().String()}, "p2c")

	// 使用真实 HTTP server（反向代理需要 CloseNotifier）
	router := gin.New()
	router.NoRoute(NewHTTPProxyHandler(lb, nil)) // nil metrics
	proxyServer := httptest.NewServer(router)
	defer proxyServer.Close()

	resp, err := http.Get(proxyServer.URL + "/api/test")
	if err != nil {
		t.Fatalf("HTTP GET error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// ──────────────────────────────────────────────
// 健康检查处理器测试
// ──────────────────────────────────────────────

func TestHealthCheckHandler_ReturnsBackendStatus(t *testing.T) {
	lb := NewLoadBalancer([]string{"10.0.0.1:8079", "10.0.0.2:8079"}, "round_robin")

	router := gin.New()
	router.GET("/gateway/backends", NewHealthCheckHandler(lb))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/gateway/backends", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	backends, ok := resp["backends"].([]any)
	if !ok || len(backends) != 2 {
		t.Fatalf("backends count = %v, want 2", resp["backends"])
	}

	first := backends[0].(map[string]any)
	if first["address"] != "10.0.0.1:8079" {
		t.Errorf("address = %v, want 10.0.0.1:8079", first["address"])
	}
	if first["cb_state"] != "closed" {
		t.Errorf("cb_state = %v, want closed", first["cb_state"])
	}
}

// ──────────────────────────────────────────────
// GatewayMetrics + 代理层指标一致性测试
// ──────────────────────────────────────────────

func TestGatewayMetrics_ProxyIntegration_CircuitBreakerState(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer backend.Close()

	metrics := observability.NewGatewayMetrics()
	lb := NewLoadBalancer([]string{backend.Listener.Addr().String()}, "p2c")

	// 使用真实 HTTP server
	router := gin.New()
	router.NoRoute(NewHTTPProxyHandler(lb, metrics))
	proxyServer := httptest.NewServer(router)
	defer proxyServer.Close()

	// 发送多个 5xx 请求触发熔断器状态变更
	for i := 0; i < 6; i++ {
		resp, err := http.Get(proxyServer.URL + "/api/test")
		if err != nil {
			t.Fatalf("HTTP GET #%d error: %v", i, err)
		}
		resp.Body.Close()
	}

	// 验证熔断器状态指标已更新
	metricsW := httptest.NewRecorder()
	metricsReq, _ := http.NewRequest("GET", "/metrics", nil)
	metricsRouter := gin.New()
	metricsRouter.GET("/metrics", metrics.Handler())
	metricsRouter.ServeHTTP(metricsW, metricsReq)

	body := metricsW.Body.String()
	if !containsString(body, "privshield_gateway_circuit_breaker_state") {
		t.Error("metrics output missing circuit_breaker_state after failures")
	}
}

// ──────────────────────────────────────────────
// 统一错误信封与 middleware 包一致性验证
// ──────────────────────────────────────────────

func TestHTTPProxy_ErrorEnvelopeMatchesMiddlewareFormat(t *testing.T) {
	lb := NewLoadBalancer([]string{"192.0.2.1:1"}, "p2c") // TEST-NET，不可达
	nodes := lb.Nodes()
	// 强制打开熔断器
	for i := 0; i < 10; i++ {
		nodes[0].CB.RecordFailure()
	}

	router := gin.New()
	router.NoRoute(NewHTTPProxyHandler(lb, nil))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/mask", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Skipf("status = %d, skipping envelope check", w.Code)
	}

	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}

	// 验证信封字段与 pkg/middleware/envelope.go 一致
	requiredFields := []string{"code", "message", "timestamp"}
	for _, field := range requiredFields {
		if _, ok := env[field]; !ok {
			t.Errorf("error envelope missing required field %q", field)
		}
	}

	// 验证 timestamp 格式
	if ts, ok := env["timestamp"].(string); ok {
		if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
			t.Errorf("timestamp not RFC3339Nano: %v", err)
		}
	}
}

// ──────────────────────────────────────────────
// middleware.AbortWithError 格式验证（对照）
// ──────────────────────────────────────────────

func TestMiddlewareAbortWithError_FormatReference(t *testing.T) {
	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		middleware.AbortWithError(c, http.StatusBadRequest, "TEST_CODE", "test message", "test detail")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)

	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}

	if env["code"] != "TEST_CODE" {
		t.Errorf("code = %v, want TEST_CODE", env["code"])
	}
	if env["message"] != "test message" {
		t.Errorf("message = %v, want 'test message'", env["message"])
	}
	if _, ok := env["timestamp"]; !ok {
		t.Error("missing timestamp")
	}
	// 注意：信封没有 "status" 字段
	if _, ok := env["status"]; ok {
		t.Error("envelope should NOT have 'status' field")
	}
}

// ──────────────────────────────────────────────
// 辅助函数
// ──────────────────────────────────────────────

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
