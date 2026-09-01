// Package agent 单元测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证 Package agent（上游 Agent 共享 HTTP 客户端）的核心功能与高可用保障：
//  1. 【基础配置与初始化】：验证默认值兜底（30s 超时、5 次失败阈值、30s 冷却）与自定义配置生效；
//  2. 【HTTP 通信与协议】：验证 GET/POST 请求、健康检查 Health()、JSON 序列化与反序列化；
//  3. 【请求头注入与追踪】：验证 APIKey Bearer Token 注入、X-Request-ID 显式与 Context 透传；
//  4. 【多节点负载均衡】：验证多节点集群配置下 Round-Robin 算法请求分布的绝对均匀性；
//  5. 【三态熔断器完整生命周期】：
//     - 连续失败达到阈值进入 Open，本地快速阻断请求；
//     - 冷却时间过后进入 Half-Open 允许单请求试探；
//     - 试探失败重新回退 Open；试探成功恢复 Closed；
//     - 间歇性成功调用重置失败计数，防止误熔断；
//     - 4xx 客户端参数/业务错误不计入服务端节点故障，绝不误触发熔断。
//  6. 【结构化重试判定（P2-7）】：验证重试与否由 errors.Is / errors.As 决策而非错误文案匹配 ——
//     仅「写着 connection refused」的普通错误绝不被误判为可重试，真实类型化故障（含逐层包装者）
//     必然可重试；并验证哨兵错误 ErrTransport / ErrCircuitOpen / ErrEndpointUnavailable 的
//     对外错误口径与改造前逐字节一致。
// ==============================================================================

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// newTestLogger 创建一个测试专用的结构化日志器，过滤低级别日志以保持测试输出整洁。
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// ─────────────────────────────────────────────────────────────
// 1. Client 基础配置与实例化测试
// ─────────────────────────────────────────────────────────────

// TestNew_Defaults 验证未提供可选参数时，Client 是否正确加载默认配置。
//
// 测试目的与断言：
// - BaseURL 准确解析；
// - CBThreshold 默认为 5 次连续失败；
// - CBCooldown 默认为 30 秒。
func TestNew_Defaults(t *testing.T) {
	c := New(Config{BaseURL: "http://localhost:8079"})
	if c.BaseURL() != "http://localhost:8079" {
		t.Errorf("BaseURL() = %q, want %q", c.BaseURL(), "http://localhost:8079")
	}
	if c.cbThreshold != 5 {
		t.Errorf("cbThreshold = %d, want 5", c.cbThreshold)
	}
	if c.cbCooldown != 30*time.Second {
		t.Errorf("cbCooldown = %v, want 30s", c.cbCooldown)
	}
}

// TestNew_CustomConfig 验证传入自定义配置时，所有字段正确覆盖默认值。
func TestNew_CustomConfig(t *testing.T) {
	c := New(Config{
		BaseURL:     "http://example.com",
		APIKey:      "secret",
		Timeout:     10 * time.Second,
		CBThreshold: 3,
		CBCooldown:  15 * time.Second,
		Logger:      newTestLogger(),
	})
	if c.apiKey != "secret" {
		t.Errorf("apiKey = %q, want %q", c.apiKey, "secret")
	}
	if c.cbThreshold != 3 {
		t.Errorf("cbThreshold = %d, want 3", c.cbThreshold)
	}
	if c.cbCooldown != 15*time.Second {
		t.Errorf("cbCooldown = %v, want 15s", c.cbCooldown)
	}
}

// TestBaseURL 验证单节点配置下 BaseURL() 方法能够正确返回基础地址。
func TestBaseURL(t *testing.T) {
	c := New(Config{BaseURL: "http://test:9090"})
	if got := c.BaseURL(); got != "http://test:9090" {
		t.Errorf("BaseURL() = %q, want %q", got, "http://test:9090")
	}
}

// ─────────────────────────────────────────────────────────────
// 2. GET / POST / Health / 协议请求测试
// ─────────────────────────────────────────────────────────────

// TestGet_Success 验证标准 HTTP GET 请求的发送、响应解析与状态码校验。
func TestGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/health" {
			t.Errorf("path = %s, want /health", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Logger: newTestLogger()})
	result, err := c.Get(context.Background(), "/health")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %v, want ok", result["status"])
	}
}

// TestPost_Success 验证标准 HTTP POST 请求的 JSON Payload 编码与响应反序列化。
func TestPost_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %s, want application/json", r.Header.Get("Content-Type"))
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"result": body["input"]})
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Logger: newTestLogger()})
	result, err := c.Post(context.Background(), "/v1/privacy/mask", map[string]any{"input": "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["result"] != "test" {
		t.Errorf("result = %v, want test", result["result"])
	}
}

// TestPostWithRequestID 验证通过 PostWithRequestID 显式传入的追踪 ID 正确设置到 X-Request-ID 请求头。
func TestPostWithRequestID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-ID")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"request_id": rid})
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Logger: newTestLogger()})
	result, err := c.PostWithRequestID(context.Background(), "/test", nil, "req-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["request_id"] != "req-123" {
		t.Errorf("request_id = %v, want req-123", result["request_id"])
	}
}

// TestBearerTokenInjection 验证配置 APIKey 时，请求中是否自动注入 Authorization: Bearer <key> 头。
func TestBearerTokenInjection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"auth": auth})
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "my-key", Logger: newTestLogger()})
	result, err := c.Get(context.Background(), "/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["auth"] != "Bearer my-key" {
		t.Errorf("auth = %v, want Bearer my-key", result["auth"])
	}
}

// TestGet_AgentError 验证上游返回 500 内部错误时，Client 能够正确捕获并返回错误。
func TestGet_AgentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Logger: newTestLogger()})
	_, err := c.Get(context.Background(), "/fail")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestHealth_DelegatesToGet 验证 Health() 方法正确代理至 GET /health 请求。
func TestHealth_DelegatesToGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("path = %s, want /health", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Logger: newTestLogger()})
	result, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %v, want ok", result["status"])
	}
}

// ─────────────────────────────────────────────────────────────
// 3. Circuit Breaker / 熔断器生命周期状态机测试
// ─────────────────────────────────────────────────────────────

// TestCircuitBreaker_OpensAfterThreshold 验证连续失败达到阈值后，熔断器进入 Open 状态并秒级拦截后续请求。
//
// 测试逻辑：
// 1. 设置 CBThreshold=2，MaxRetries=1；
// 2. 发起 1 次请求（包含 1 次初始请求 + 1 次重试 = 2 次失败），刚好达到阈值 2；
// 3. 断言熔断器状态切换为 "open"；
// 4. 发起下一次请求，断言请求在进入网络前被客户端断路器直接秒级拒绝。
func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error"))
	}))
	defer srv.Close()

	// MaxRetries=1 + RetryBaseDelay=1ms: each c.Get() makes 2 attempts (1 initial + 1 retry),
	// each recording a failure. CBThreshold=2 means 1 c.Get() call (2 failures) trips the circuit.
	// 每个 c.Get() 调用产生 2 次尝试（1 次初始 + 1 次重试），每次均记录失败。
	// CBThreshold=2 表示 1 次 c.Get() 调用（2 次失败）即触发熔断。
	c := New(Config{
		BaseURL:        srv.URL,
		CBThreshold:    2,
		CBCooldown:     1 * time.Second,
		MaxRetries:     1,
		RetryBaseDelay: time.Millisecond,
		Logger:         newTestLogger(),
	})

	// 1 request with 2 attempts → 2 failures >= threshold(2) → circuit opens
	// 1 次请求含 2 次尝试 → 2 次失败 >= 阈值(2) → 熔断器打开
	c.Get(context.Background(), "/test")

	// Circuit should now be open
	if state := c.CircuitStateString(); state != "open" {
		t.Errorf("state = %s, want open", state)
	}

	// Next request should be rejected immediately (circuit open)
	_, err := c.Get(context.Background(), "/test")
	if err == nil {
		t.Fatal("expected circuit breaker error, got nil")
	}
	if got := err.Error(); got != "circuit breaker open (cooldown remaining)" {
		t.Errorf("error = %q, want circuit breaker open", got)
	}
}

// TestCircuitBreaker_HalfOpenAfterCooldown 验证熔断器在冷却时间过后转为 Half-Open，并允许放行一次探测请求。
// 若探测请求仍然失败，熔断器重新回退至 Open 状态。
func TestCircuitBreaker_HalfOpenAfterCooldown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error"))
	}))
	defer srv.Close()

	c := New(Config{
		BaseURL:     srv.URL,
		CBThreshold: 2,
		CBCooldown:  50 * time.Millisecond,
		Logger:      newTestLogger(),
	})

	// Trip the circuit breaker
	for i := 0; i < 2; i++ {
		c.Get(context.Background(), "/test")
	}
	if state := c.CircuitStateString(); state != "open" {
		t.Fatalf("state = %s, want open", state)
	}

	// Wait for cooldown
	time.Sleep(60 * time.Millisecond)

	// Should transition to half-open and allow one probe
	// (will fail since server still returns 500, but it should be allowed through)
	c.Get(context.Background(), "/test")

	// After failed probe, should re-open
	if state := c.CircuitStateString(); state != "open" {
		t.Errorf("state after failed probe = %s, want open", state)
	}
}

// TestCircuitBreaker_RecoveryOnSuccess 验证在 Half-Open 状态下探测请求成功后，熔断器平滑恢复至 Closed 正常状态。
func TestCircuitBreaker_RecoveryOnSuccess(t *testing.T) {
	var shouldFail atomic.Bool
	shouldFail.Store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldFail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer srv.Close()

	c := New(Config{
		BaseURL:     srv.URL,
		CBThreshold: 2,
		CBCooldown:  50 * time.Millisecond,
		Logger:      newTestLogger(),
	})

	// Trip the circuit breaker
	for i := 0; i < 2; i++ {
		c.Get(context.Background(), "/test")
	}

	// Wait for cooldown
	time.Sleep(60 * time.Millisecond)

	// Make server healthy
	shouldFail.Store(false)

	// Probe request should succeed → circuit closes
	result, err := c.Get(context.Background(), "/test")
	if err != nil {
		t.Fatalf("unexpected error after recovery: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %v, want ok", result["status"])
	}

	if state := c.CircuitStateString(); state != "closed" {
		t.Errorf("state = %s, want closed", state)
	}
}

// TestCircuitBreaker_IntermittentSuccessResetsFailureCount 验证偶发的成功调用会重置连续失败计数器，防止偶发网络抖动导致误熔断。
func TestCircuitBreaker_IntermittentSuccessResetsFailureCount(t *testing.T) {
	var shouldFail atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldFail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer srv.Close()

	// MaxRetries=1 + RetryBaseDelay=1ms: each c.Get() makes 2 attempts.
	// CBThreshold=5: need 5 consecutive failures to trip.
	// 每次 c.Get() 调用产生 2 次尝试。CBThreshold=5：需要 5 次连续失败才触发熔断。
	c := New(Config{
		BaseURL:        srv.URL,
		CBThreshold:    5,
		MaxRetries:     1,
		RetryBaseDelay: time.Millisecond,
		Logger:         newTestLogger(),
	})

	// 2 failures (2 calls × 2 attempts = 4 consecutive failures)
	// 2 次失败（2 次调用 × 2 次尝试 = 4 次连续失败）
	shouldFail.Store(true)
	c.Get(context.Background(), "/test")
	c.Get(context.Background(), "/test")
	if c.CircuitStateString() != "closed" {
		t.Fatalf("expected closed after 4 consecutive failures, got %s", c.CircuitStateString())
	}

	// 1 success -> resets consecutive failure counter to 0
	// 1 次成功 → 重置连续失败计数器为 0
	shouldFail.Store(false)
	c.Get(context.Background(), "/test")

	// 2 more failures (2 calls × 2 attempts = 4 consecutive failures)
	// Should NOT trip since threshold is 5 and failures were reset by the success
	// 不应触发熔断，因为阈值为 5 且失败计数已被成功重置
	shouldFail.Store(true)
	c.Get(context.Background(), "/test")
	c.Get(context.Background(), "/test")

	if c.CircuitStateString() != "closed" {
		t.Errorf("expected circuit to stay closed because failures were non-consecutive, got %s", c.CircuitStateString())
	}
}

// ─────────────────────────────────────────────────────────────
// 4. 多节点负载均衡与 4xx 业务错误防误熔断测试
// ─────────────────────────────────────────────────────────────

// TestMultiNode_RoundRobin 验证配置多节点时，Client 发起的请求能够通过 Round-Robin 算法绝对均匀地分发给每个节点。
func TestMultiNode_RoundRobin(t *testing.T) {
	var count1, count2 atomic.Int64

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count1.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"node": 1})
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count2.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"node": 2})
	}))
	defer srv2.Close()

	c := New(Config{
		BaseURLs: []string{srv1.URL, srv2.URL},
		Logger:   newTestLogger(),
	})

	if len(c.BaseURLs()) != 2 {
		t.Fatalf("expected 2 urls, got %d", len(c.BaseURLs()))
	}

	// Make 6 requests, should be evenly distributed (3 to srv1, 3 to srv2)
	for i := 0; i < 6; i++ {
		_, err := c.Get(context.Background(), "/node")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}

	if count1.Load() != 3 || count2.Load() != 3 {
		t.Errorf("expected 3 requests each, got srv1=%d, srv2=%d", count1.Load(), count2.Load())
	}
}

// TestCircuitBreaker_ClientError4xx_NoTrip 验证客户端错误（HTTP 4xx，如 400 Bad Request）绝不触发熔断。
//
// 架构安全保障：
// 4xx 代表客户端自身传入的参数、业务字段不合法，并非服务端节点宕机或网络中断。
// 若将 4xx 计入熔断，恶意攻击者或错误客户端可通过发送大量非法请求瘫痪正常服务的网关。
func TestCircuitBreaker_ClientError4xx_NoTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"detail":"invalid argument"}`))
	}))
	defer srv.Close()

	c := New(Config{
		BaseURL:     srv.URL,
		CBThreshold: 3,
		Logger:      newTestLogger(),
	})

	// Send 5 400 Bad Request calls
	for i := 0; i < 5; i++ {
		_, err := c.Get(context.Background(), "/bad-request")
		if err == nil {
			t.Errorf("expected error for 400, got nil")
		}
	}

	// Circuit should still be CLOSED because 4xx does not indicate an agent server outage
	if state := c.CircuitStateString(); state != "closed" {
		t.Errorf("state = %s, want closed (4xx errors must not trip circuit breaker)", state)
	}
}

// ─────────────────────────────────────────────────────────────
// 5. 按节点维度熔断与故障转移（P1-9）
// ─────────────────────────────────────────────────────────────

// TestPerEndpointBreaker_IsolatesDeadNode 验证单节点故障只熔断该节点：
// 故障节点进入 Open 冷却期后，其余健康节点继续承接全部流量，聚合状态仍为 closed。
func TestPerEndpointBreaker_IsolatesDeadNode(t *testing.T) {
	var badCalls, goodCalls atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		badCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer bad.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goodCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"node": "good"})
	}))
	defer good.Close()

	c := New(Config{
		BaseURLs:       []string{bad.URL, good.URL},
		CBThreshold:    2,
		CBCooldown:     30 * time.Second,
		MaxRetries:     1,
		RetryBaseDelay: time.Millisecond,
		Logger:         newTestLogger(),
	})

	succeeded := 0
	for i := 0; i < 6; i++ {
		if _, err := c.Get(context.Background(), "/test"); err == nil {
			succeeded++
		}
	}

	states := c.EndpointStates()
	if states[bad.URL] != "open" {
		t.Fatalf("dead node must be fused on its own breaker, got %v", states)
	}
	if states[good.URL] != "closed" {
		t.Fatalf("healthy node must stay closed despite the peer outage, got %v", states)
	}
	if got := c.CircuitStateString(); got != "closed" {
		t.Errorf("aggregate state = %s, want closed (one fused node must not black out the cluster)", got)
	}
	if succeeded == 0 {
		t.Fatal("requests must keep succeeding on the healthy node")
	}
}

// TestPerEndpointBreaker_AllNodesOpenFastFails 验证全集群熔断时客户端在触网前快速失败，
// 且错误口径与单节点熔断保持一致，供上层熔断降级逻辑识别。
func TestPerEndpointBreaker_AllNodesOpenFastFails(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}
	srv1 := httptest.NewServer(http.HandlerFunc(handler))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(handler))
	defer srv2.Close()

	c := New(Config{
		BaseURLs:       []string{srv1.URL, srv2.URL},
		CBThreshold:    1,
		MaxRetries:     1,
		RetryBaseDelay: time.Millisecond,
		Logger:         newTestLogger(),
	})

	c.Get(context.Background(), "/test")
	if got := c.CircuitStateString(); got != "open" {
		t.Fatalf("aggregate state = %s, want open once every node is fused", got)
	}

	_, err := c.Get(context.Background(), "/test")
	if err == nil || err.Error() != "circuit breaker open (cooldown remaining)" {
		t.Fatalf("error = %v, want the circuit breaker fast-fail error", err)
	}
}

// TestRetryFailoverServesFromHealthyNode 验证重试轮次会切换到其他节点，
// 使单次上游抖动不会演变成整请求失败。
func TestRetryFailoverServesFromHealthyNode(t *testing.T) {
	var badCalls, goodCalls atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		badCalls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("unavailable"))
	}))
	defer bad.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goodCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"node": "good"})
	}))
	defer good.Close()

	c := New(Config{
		BaseURLs:       []string{bad.URL, good.URL},
		CBThreshold:    100,
		MaxRetries:     1,
		RetryBaseDelay: time.Millisecond,
		Logger:         newTestLogger(),
	})

	for i := 0; i < 2; i++ {
		if _, err := c.Get(context.Background(), "/test"); err != nil {
			t.Fatalf("request %d should succeed via failover: %v", i, err)
		}
	}
	if badCalls.Load() == 0 {
		t.Fatal("expected the failing node to be attempted at least once")
	}
	if goodCalls.Load() < 2 {
		t.Fatalf("expected failover to the healthy node, good=%d bad=%d", goodCalls.Load(), badCalls.Load())
	}
}

// ─────────────────────────────────────────────────────────────
// 6. 结构化重试判定（P2-7：errors.Is / errors.As，不再匹配错误文案）
// ─────────────────────────────────────────────────────────────

// stubTimeoutError 是一个仅通过 net.Error 接口表达「超时」的错误，
// 其文案刻意不含 "timeout" 字样，用于证明超时判定走接口而非字符串匹配。
type stubTimeoutError struct{ msg string }

func (e stubTimeoutError) Error() string   { return e.msg }
func (e stubTimeoutError) Timeout() bool   { return true }
func (e stubTimeoutError) Temporary() bool { return true }

// TestIsRetryableError_StructuralNotTextual 证明重试判定已完全结构化：
// 文案「看起来像」网络故障的普通错误一律不重试，而真正的类型化错误（含被逐层包装者）必然重试。
func TestIsRetryableError_StructuralNotTextual(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	dnsFailure := &net.DNSError{Err: "no such host", Name: "agent", Server: "127.0.0.1", IsNotFound: true}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		// ── 仅文案相似、结构上并非网络故障：绝不可重试（旧字符串匹配会全部误判为可重试）──
		{name: "textual connection refused look-alike", err: errors.New("dial tcp 127.0.0.1:8079: connect: connection refused"), want: false},
		{name: "textual reset look-alike", err: errors.New("read tcp: connection reset by peer"), want: false},
		{name: "textual deadline look-alike", err: errors.New("context deadline exceeded"), want: false},
		{name: "textual EOF look-alike", err: errors.New("EOF"), want: false},
		{name: "textual closed-conn look-alike", err: errors.New("use of closed network connection"), want: false},
		{name: "wrapped textual refused look-alike", err: fmt.Errorf("agent request failed: %w", errors.New("connection refused")), want: false},
		{name: "4xx client error", err: errors.New("agent returned status 400: invalid argument"), want: false},
		{name: "response parse error", err: errors.New("parse agent response: invalid character '{'"), want: false},
		{name: "oversized response", err: errors.New("agent response too large: exceeds 67108864 bytes"), want: false},

		// ── 真实类型化错误：可重试 ──
		{name: "typed ECONNREFUSED", err: syscall.ECONNREFUSED, want: true},
		{name: "net.OpError wrapping ECONNREFUSED", err: refused, want: true},
		{name: "url.Error wrapping refused OpError", err: &url.Error{Op: "Post", URL: "http://127.0.0.1:8079/health", Err: refused}, want: true},
		{name: "fmt.Errorf-wrapped refused chain", err: fmt.Errorf("agent request failed: %w", &url.Error{Op: "Get", URL: "http://x/y", Err: refused}), want: true},
		{name: "typed ECONNRESET", err: syscall.ECONNRESET, want: true},
		{name: "net.OpError wrapping ECONNRESET", err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, want: true},
		{name: "context.DeadlineExceeded", err: context.DeadlineExceeded, want: true},
		{name: "wrapped context.DeadlineExceeded", err: fmt.Errorf("do: %w", context.DeadlineExceeded), want: true},
		{name: "os.ErrDeadlineExceeded", err: os.ErrDeadlineExceeded, want: true},
		{name: "io.ErrUnexpectedEOF", err: io.ErrUnexpectedEOF, want: true},
		{name: "wrapped io.ErrUnexpectedEOF", err: fmt.Errorf("read agent response: %w", io.ErrUnexpectedEOF), want: true},
		{name: "io.EOF", err: io.EOF, want: true},
		{name: "net.ErrClosed", err: net.ErrClosed, want: true},
		{name: "net.Error with Timeout()==true", err: stubTimeoutError{msg: "engine said no"}, want: true},

		// ── 客户端自身的快速失败判定：绝不重试（重试只会放大出站调用）──
		{name: "ErrCircuitOpen", err: ErrCircuitOpen, want: false},
		{name: "wrapped ErrCircuitOpen", err: fmt.Errorf("retry blocked: %w", ErrCircuitOpen), want: false},
		{name: "ErrEndpointUnavailable", err: ErrEndpointUnavailable, want: false},
		{name: "wrapped ErrEndpointUnavailable", err: fmt.Errorf("pick endpoint: %w", ErrEndpointUnavailable), want: false},

		// ── 传输故障整体口径：根因无法穷举（DNS/TLS/代理）也仍按瞬时故障重试 ──
		{name: "transport-tagged DNS error", err: newTransportError(dnsFailure), want: true},
		{name: "transport-tagged opaque error", err: newTransportError(errors.New("something odd from the proxy")), want: true},
		{name: "nil", err: nil, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableError(tc.err); got != tc.want {
				t.Errorf("isRetryableError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestClassifyTransportReason 校验故障根因分类完全由类型驱动，且有界可枚举。
func TestClassifyTransportReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want transportReason
	}{
		{name: "refused", err: os.NewSyscallError("connect", syscall.ECONNREFUSED), want: reasonConnectionRefused},
		{name: "reset", err: &net.OpError{Op: "accept", Net: "tcp", Err: syscall.ECONNRESET}, want: reasonConnectionReset},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: reasonTimeout},
		{name: "socket deadline", err: os.ErrDeadlineExceeded, want: reasonTimeout},
		{name: "net.Error timeout", err: stubTimeoutError{msg: "no keyword at all"}, want: reasonTimeout},
		{name: "unexpected eof", err: io.ErrUnexpectedEOF, want: reasonUnexpectedEOF},
		{name: "eof", err: &net.OpError{Op: "read", Net: "tcp", Err: io.EOF}, want: reasonUnexpectedEOF},
		{name: "closed conn", err: net.ErrClosed, want: reasonConnClosed},
		{name: "unclassifiable dns", err: &net.DNSError{Err: "server misbehaving", IsTemporary: true}, want: reasonUnknown},
		{name: "plain text error", err: errors.New("connection refused"), want: reasonUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyTransport(tc.err); got != tc.want {
				t.Errorf("classifyTransport(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestTransportErrorPreservesMessageAndExposesCause 校验 transportError 既透出哨兵与根因，
// 又不改变对外错误文案（既有依赖错误字符串的调用方与断言不受影响）。
func TestTransportErrorPreservesMessageAndExposesCause(t *testing.T) {
	cause := &url.Error{Op: "Post", URL: "http://127.0.0.1:8079/health", Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}
	terr := newTransportError(cause)

	if terr.Error() != cause.Error() {
		t.Errorf("transportError.Error() = %q, want unchanged cause text %q", terr.Error(), cause.Error())
	}
	if !errors.Is(terr, ErrTransport) {
		t.Error("errors.Is(terr, ErrTransport) = false, want true")
	}
	if !errors.Is(terr, syscall.ECONNREFUSED) {
		t.Error("root cause must stay visible through Unwrap for errors.Is")
	}
	if errors.Is(terr, ErrCircuitOpen) {
		t.Error("a transport failure must not be classified as a circuit-breaker verdict")
	}
	if terr.reason != reasonConnectionRefused {
		t.Errorf("reason = %v, want %v", terr.reason, reasonConnectionRefused)
	}
}

// TestDoTagsOutboundFailureWithTransportSentinel 用真实出站故障（端口未监听 → 连接被拒）
// 验证 do() 上抛的错误可被 errors.Is 结构化识别，且文案口径保持不变。
func TestDoTagsOutboundFailureWithTransportSentinel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadURL := "http://" + ln.Addr().String()
	if err := ln.Close(); err != nil { // 关闭监听后该端口必然拒绝连接
		t.Fatalf("close listener: %v", err)
	}

	c := New(Config{
		BaseURL:        deadURL,
		CBThreshold:    100,
		MaxRetries:     1,
		RetryBaseDelay: time.Millisecond,
		Logger:         newTestLogger(),
	})

	_, err = c.Get(context.Background(), "/health")
	if err == nil {
		t.Fatal("expected an error calling a dead endpoint, got nil")
	}
	if !errors.Is(err, ErrTransport) {
		t.Errorf("errors.Is(err, ErrTransport) = false, want true; err = %v", err)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Errorf("errors.Is(err, syscall.ECONNREFUSED) = false, want true; err = %v", err)
	}
	if errors.Is(err, ErrCircuitOpen) {
		t.Error("err must not be a circuit-breaker fast-fail (the breaker is far from tripping)")
	}
	if !strings.HasPrefix(err.Error(), "agent request failed after 2 attempts: agent request failed: ") {
		t.Errorf("error text drifted: %q", err.Error())
	}
}

// TestClientFastFailSentinels 校验客户端自身的快速失败路径以哨兵错误表达，
// 且错误文案与改造前逐字节一致（上层既有断言与看板口径不变）。
func TestClientFastFailSentinels(t *testing.T) {
	if got, want := ErrCircuitOpen.Error(), "circuit breaker open (cooldown remaining)"; got != want {
		t.Errorf("ErrCircuitOpen text = %q, want %q", got, want)
	}
	if got, want := ErrEndpointUnavailable.Error(), "no agent endpoint available"; got != want {
		t.Errorf("ErrEndpointUnavailable text = %q, want %q", got, want)
	}

	// 未配置任何上游节点 → 配置类快速失败哨兵，且判定为不可重试。
	noEndpoint := New(Config{Logger: newTestLogger()})
	if _, err := noEndpoint.Get(context.Background(), "/health"); !errors.Is(err, ErrEndpointUnavailable) {
		t.Errorf("errors.Is(err, ErrEndpointUnavailable) = false, want true; err = %v", err)
	}
	if _, err := noEndpoint.pickEndpoint(""); isRetryableError(err) {
		t.Error("a missing endpoint must not be classified as retryable")
	}

	// 单节点被熔断 → 熔断哨兵，且判定为不可重试（出站调用量为 0）。
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := New(Config{
		BaseURL:        srv.URL,
		CBThreshold:    1,
		CBCooldown:     time.Hour,
		MaxRetries:     0,
		RetryBaseDelay: time.Millisecond,
		Logger:         newTestLogger(),
	})
	if _, err := c.Get(context.Background(), "/test"); err == nil {
		t.Fatal("expected the first request to fail")
	}
	before := calls.Load()
	_, err := c.Get(context.Background(), "/test")
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("errors.Is(err, ErrCircuitOpen) = false, want true; err = %v", err)
	}
	if isRetryableError(err) {
		t.Error("a fused endpoint must not be classified as retryable")
	}
	if got := calls.Load(); got != before {
		t.Errorf("upstream calls grew from %d to %d: a fused node must reject before touching the network", before, got)
	}
}
