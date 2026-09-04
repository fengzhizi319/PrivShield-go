package lbtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/models"
)

// startFakeBackend 启动一个返回指定状态码的假后端，并统计命中数。
func startFakeBackend(t *testing.T, statusCode int, hits *int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(hits, 1)
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunRoundRobinDistribution(t *testing.T) {
	var hitsA, hitsB int64
	srvA := startFakeBackend(t, http.StatusOK, &hitsA)
	srvB := startFakeBackend(t, http.StatusOK, &hitsB)

	req := models.LbTestRequest{
		Backends: []models.LbBackend{
			{Name: "a", URL: srvA.URL},
			{Name: "b", URL: srvB.URL},
		},
		NumRequests: 6,
		Strategy:    StrategyRoundRobin,
	}
	resp, err := Run(context.Background(), req, srvA.Client())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if resp.Total != 6 || resp.Success != 6 || resp.Failed != 0 {
		t.Fatalf("unexpected summary: %+v", resp)
	}
	if len(resp.Distribution) != 2 {
		t.Fatalf("expected 2 distribution items, got %d", len(resp.Distribution))
	}
	// 均匀分发：每个节点各命中 3 次。
	if resp.Distribution[0].Count != 3 || resp.Distribution[1].Count != 3 {
		t.Fatalf("expected even distribution, got %+v", resp.Distribution)
	}
	if atomic.LoadInt64(&hitsA) != 3 || atomic.LoadInt64(&hitsB) != 3 {
		t.Fatalf("expected 3 hits each backend, got a=%d b=%d", hitsA, hitsB)
	}
	for _, d := range resp.Distribution {
		if d.Success != d.Count || d.Failed != 0 {
			t.Fatalf("unexpected item stats: %+v", d)
		}
		if d.MinLatencyMs > d.AvgLatencyMs || d.AvgLatencyMs > d.MaxLatencyMs {
			t.Fatalf("latency ordering violated: %+v", d)
		}
	}
}

func TestRunFailedProbe(t *testing.T) {
	var hits int64
	srv := startFakeBackend(t, http.StatusInternalServerError, &hits)

	req := models.LbTestRequest{
		Backends:    []models.LbBackend{{Name: "a", URL: srv.URL}},
		NumRequests: 3,
		Strategy:    StrategyRoundRobin,
	}
	resp, err := Run(context.Background(), req, srv.Client())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.Total != 3 || resp.Success != 0 || resp.Failed != 3 {
		t.Fatalf("unexpected summary: %+v", resp)
	}
	if resp.Distribution[0].Failed != 3 {
		t.Fatalf("expected 3 failed, got %+v", resp.Distribution[0])
	}
}

func TestRunEmptyBackends(t *testing.T) {
	req := models.LbTestRequest{Backends: nil, NumRequests: 3, Strategy: StrategyRoundRobin}
	if _, err := Run(context.Background(), req, nil); err == nil {
		t.Fatalf("expected error for empty backends")
	}
}

func TestRunDefaults(t *testing.T) {
	var hits int64
	srv := startFakeBackend(t, http.StatusOK, &hits)

	// NumRequests/Strategy 缺省时应取 10 / round_robin。
	req := models.LbTestRequest{
		Backends: []models.LbBackend{{Name: "a", URL: srv.URL}},
	}
	resp, err := Run(context.Background(), req, srv.Client())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.Strategy != StrategyRoundRobin || resp.Total != 10 {
		t.Fatalf("expected defaults round_robin/10, got %s/%d", resp.Strategy, resp.Total)
	}
}

func TestPickBackendsStrategies(t *testing.T) {
	for _, strategy := range []string{StrategyRoundRobin, StrategyRandom, StrategyLeastConnections} {
		seq, err := PickBackends(strategy, 10, 3)
		if err != nil {
			t.Fatalf("PickBackends(%s) failed: %v", strategy, err)
		}
		if len(seq) != 10 {
			t.Fatalf("expected length 10, got %d", len(seq))
		}
		for _, idx := range seq {
			if idx < 0 || idx >= 3 {
				t.Fatalf("index out of range: %d", idx)
			}
		}
	}
}

func TestPickBackendsInvalidStrategy(t *testing.T) {
	if _, err := PickBackends("foobar", 5, 2); err == nil {
		t.Fatalf("expected error for invalid strategy")
	}
}

func TestValidateURLScheme(t *testing.T) {
	// 合法 scheme（http/https）应通过校验。
	for _, ok := range []string{"http://127.0.0.1:8079/health", "https://agent.local/v1"} {
		if err := ValidateURL(ok, nil); err != nil {
			t.Fatalf("expected %q to be valid, got %v", ok, err)
		}
	}
	// 非法 scheme（file/gopher/ftp）应被拦截（SSRF 防护）。
	for _, bad := range []string{"file:///etc/passwd", "gopher://127.0.0.1:25", "ftp://host/x"} {
		if err := ValidateURL(bad, nil); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
	// 缺少 host 应被拦截。
	if err := ValidateURL("http://", nil); err == nil {
		t.Fatalf("expected url without host to be rejected")
	}
}

func TestValidateURLAllowlist(t *testing.T) {
	allowed := []string{"trusted.local", "Agent.Internal"}
	// 命中白名单（大小写不敏感）应通过。
	if err := ValidateURL("http://TRUSTED.local:8079/health", allowed); err != nil {
		t.Fatalf("expected allowlisted host to pass, got %v", err)
	}
	if err := ValidateURL("https://agent.internal/x", allowed); err != nil {
		t.Fatalf("expected case-insensitive match to pass, got %v", err)
	}
	// 未命中白名单应被拦截。
	if err := ValidateURL("http://evil.example.com/x", allowed); err == nil {
		t.Fatalf("expected non-allowlisted host to be rejected")
	}
}

func TestValidateBackends(t *testing.T) {
	// 全部合法应通过。
	good := []models.LbBackend{
		{Name: "a", URL: "http://127.0.0.1:8079"},
		{Name: "b", URL: "https://agent.local"},
	}
	if err := ValidateBackends(good, nil); err != nil {
		t.Fatalf("expected valid backends to pass, got %v", err)
	}
	// 任一非法即返回错误。
	bad := []models.LbBackend{
		{Name: "a", URL: "http://127.0.0.1:8079"},
		{Name: "b", URL: "file:///etc/passwd"},
	}
	if err := ValidateBackends(bad, nil); err == nil {
		t.Fatalf("expected backends with invalid scheme to be rejected")
	}
}

func TestRunProbeBodyPost(t *testing.T) {
	// 提供 ProbeBody 时应以 POST 发送。
	var method atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method.Store(r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	req := models.LbTestRequest{
		Backends:    []models.LbBackend{{Name: "a", URL: srv.URL}},
		NumRequests: 1,
		Strategy:    StrategyRoundRobin,
		ProbePath:   "/v1/privacy/mask",
		ProbeBody:   []byte(`{"field_name":"email"}`),
	}
	if _, err := Run(context.Background(), req, srv.Client()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if m, _ := method.Load().(string); m != http.MethodPost {
		t.Fatalf("expected POST with probe_body, got %s", m)
	}
}
