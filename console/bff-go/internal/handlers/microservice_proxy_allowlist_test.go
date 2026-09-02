package handlers

// P0-7 / 门禁 G-01 回归测试：中台透明代理必须收敛为「方法 + 路径」默认拒绝白名单。
//
// 覆盖三类断言：
//  1. 原始数据旁路（/records、/sample、/api/v1/yibao|kangyang|mock3|mock4）一律 403；
//  2. 控制台真实需要的只读元数据 / 统计 / 调度端点仍然放行；
//  3. ".."、重复斜杠与 %2e%2e 编码穿越在到达白名单前即被 WAF 拦截（G-12）。

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/config"
	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
)

// proxyTestEnv 一套带命中计数与日志捕获的 BFF + 假上游测试环境。
type proxyTestEnv struct {
	router       http.Handler
	upstreamHits *int64
	upstreamPath *atomic.Value // 上游最后一次收到的路径
	logs         *bytes.Buffer
}

// newProxyTestEnv 构造三个代理目标指向同一个假上游服务的 BFF 路由。
func newProxyTestEnv(t *testing.T) *proxyTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var hits int64
	var lastPath atomic.Value // string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		lastPath.Store(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	cfg := &config.Config{
		HubURL:           upstream.URL,
		DatasourceURL:    upstream.URL,
		AuditURL:         upstream.URL,
		ConsoleRateLimit: 0, // 关闭限流，避免用例间干扰
	}
	logs := &bytes.Buffer{}
	server := New(nil, cfg, slog.New(slog.NewJSONHandler(logs, nil)), metrics.NewCollector("test"))
	t.Cleanup(server.Shutdown)

	router := gin.New()
	server.RegisterRoutes(router)
	return &proxyTestEnv{router: router, upstreamHits: &hits, upstreamPath: &lastPath, logs: logs}
}

// serve 发起一次请求并返回响应码与响应体。
func (e *proxyTestEnv) serve(t *testing.T, method, target string, body string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("X-Request-ID", "req-p0-7")
	req.Header.Set("Authorization", "Bearer console-secret")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// resetLogs 清空已捕获日志，便于按用例断言。
func (e *proxyTestEnv) resetLogs() { e.logs.Reset() }

// envelopeCode 从标准 5 字段错误信封中取出机器可读错误码。
func envelopeCode(t *testing.T, body string) string {
	t.Helper()
	var env struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("response is not the standard error envelope: %v, body=%s", err, body)
	}
	return env.Code
}

// TestIsAllowedMicroserviceProxyPath 表驱动校验白名单判定函数本身。
func TestIsAllowedMicroserviceProxyPath(t *testing.T) {
	cases := []struct {
		name    string
		service string
		method  string
		path    string
		want    bool
	}{
		// ── 必须拒绝：原始数据旁路出域口 ──
		{"datasource records", "datasource", http.MethodGet, "/api/datasources/ds_yibao/records", false},
		{"datasource records kangyang", "datasource", http.MethodGet, "/api/datasources/ds_kangyang/records", false},
		{"datasource sample", "datasource", http.MethodGet, "/api/datasources/ds_yibao/sample", false},
		{"raw yibao api", "datasource", http.MethodGet, "/api/v1/yibao", false},
		{"raw kangyang api", "datasource", http.MethodGet, "/api/v1/kangyang", false},
		{"raw mock3 api", "datasource", http.MethodGet, "/api/v1/mock3", false},
		{"raw mock4 api", "datasource", http.MethodGet, "/api/v1/mock4", false},
		{"datasource seed write", "datasource", http.MethodPost, "/api/datasources/seed", false},
		{"datasource metrics", "datasource", http.MethodGet, "/metrics", false},
		{"hub upstream metrics", "hub", http.MethodGet, "/metrics", false},
		{"records-like hub task", "hub", http.MethodGet, "/api/hub/tasks/records", false},
		{"sample-like audit log", "audit", http.MethodGet, "/api/audit/logs/sample", false},
		{"unknown proxy target", "engine", http.MethodGet, "/api/datasources", false},
		{"empty path", "datasource", http.MethodGet, "", false},
		{"root path", "datasource", http.MethodGet, "/", false},
		// ── 必须拒绝：方法维度（同一只读路径不允许写） ──
		{"delete datasource", "datasource", http.MethodDelete, "/api/datasources", false},
		{"put datasource metadata", "datasource", http.MethodPut, "/api/datasources/ds_yibao/metadata", false},
		{"post datasource detail", "datasource", http.MethodPost, "/api/datasources/ds_yibao", false},
		{"get dispatch wrong method", "hub", http.MethodGet, "/api/hub/dispatch", false},
		{"hub route via datasource", "datasource", http.MethodGet, "/api/hub/tasks", false},
		{"audit route via datasource", "datasource", http.MethodGet, "/api/audit/stats", false},

		// ── 必须放行：控制台实际使用的只读元数据 / 统计 / 调度端点 ──
		{"datasource catalog", "datasource", http.MethodGet, "/api/datasources", true},
		{"datasource detail", "datasource", http.MethodGet, "/api/datasources/ds_yibao", true},
		{"datasource metadata", "datasource", http.MethodGet, "/api/datasources/ds_yibao/metadata", true},
		{"datasource access audit", "datasource", http.MethodGet, "/api/datasources/ds_yibao/audit", true},
		{"datasource connection test", "datasource", http.MethodPost, "/api/datasources/ds_yibao/test", true},
		{"hub status", "hub", http.MethodGet, "/api/hub/status", true},
		{"hub tasks", "hub", http.MethodGet, "/api/hub/tasks", true},
		{"hub task detail", "hub", http.MethodGet, "/api/hub/tasks/task-123", true},
		{"hub pipeline", "hub", http.MethodGet, "/api/hub/pipeline", true},
		{"hub dispatch", "hub", http.MethodPost, "/api/hub/dispatch", true},
		{"audit logs list", "audit", http.MethodGet, "/api/audit/logs", true},
		{"audit log detail", "audit", http.MethodGet, "/api/audit/logs/log-1", true},
		{"audit stats", "audit", http.MethodGet, "/api/audit/stats", true},
		{"audit chain verify get", "audit", http.MethodGet, "/api/audit/chain/verify", true},
		{"audit chain verify post", "audit", http.MethodPost, "/api/audit/chain/verify", true},
		{"audit snapshots verify", "audit", http.MethodPost, "/api/audit/snapshots/verify", true},
		{"probe health", "datasource", http.MethodGet, "/health", true},
		{"probe readyz", "hub", http.MethodGet, "/readyz", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAllowedMicroserviceProxyPath(tc.service, tc.method, tc.path); got != tc.want {
				t.Errorf("isAllowedMicroserviceProxyPath(%q, %q, %q) = %v, want %v",
					tc.service, tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// TestIsAllowedMicroserviceProxyPath_Traversal 校验 ".." 与重复斜杠无法绕过白名单：
// 判定前统一 path.Clean 规范化，穿越后的真实目标仍落到拒绝分支。
func TestIsAllowedMicroserviceProxyPath_Traversal(t *testing.T) {
	denied := []struct{ service, method, path string }{
		{"datasource", http.MethodGet, "/api/datasources/ds_yibao/metadata/../records"},
		{"datasource", http.MethodGet, "/api/datasources/../v1/yibao"},
		{"datasource", http.MethodGet, "/api/datasources/ds_yibao/../../v1/mock3"},
		{"datasource", http.MethodGet, "//api//datasources//ds_yibao//records"},
		{"datasource", http.MethodGet, "/api/datasources/ds_yibao/./records"},
		{"hub", http.MethodGet, "/api/hub/tasks/../../api/datasources/ds_yibao/records"},
		{"audit", http.MethodGet, "/api/audit/stats/../../../api/v1/kangyang"},
		{"audit", http.MethodPost, "/api/audit/logs/../../datasources/ds_yibao/records"},
	}
	for _, tc := range denied {
		if isAllowedMicroserviceProxyPath(tc.service, tc.method, tc.path) {
			t.Errorf("expected traversal path %q (%s %s) to be denied", tc.path, tc.method, tc.service)
		}
	}
}

// TestRewriteProxyRequestPath 校验前缀剥离、规范化与编码穿越识别。
func TestRewriteProxyRequestPath(t *testing.T) {
	cases := []struct {
		name     string
		target   string
		prefix   string
		wantPath string
		wantOK   bool
	}{
		{"plain upstream route", "/api/datasource/api/datasources", "/api/datasource", "/api/datasources", true},
		{"nested route", "/api/datasource/api/datasources/ds_yibao/metadata", "/api/datasource", "/api/datasources/ds_yibao/metadata", true},
		{"duplicate slashes collapse", "/api/datasource//api//datasources", "/api/datasource", "/api/datasources", true},
		{"trailing slash dropped", "/api/datasource/api/datasources/", "/api/datasource", "/api/datasources", true},
		{"inner dot-dot stays inside prefix", "/api/datasource/api/datasources/ds/metadata/../audit", "/api/datasource", "/api/datasources/ds/audit", true},
		{"bare proxy prefix", "/api/datasource", "/api/datasource", "", false},
		{"bare proxy prefix with slash", "/api/datasource/", "/api/datasource", "", false},
		{"escape own prefix", "/api/datasource/../audit/api/audit/stats", "/api/datasource", "", false},
		{"non segment-boundary prefix", "/api/datasourceX/api/datasources", "/api/datasource", "", false},
		{"encoded traversal %2e%2e", "/api/datasource/%2e%2e/api/audit/api/audit/stats", "/api/datasource", "", false},
		{"encoded traversal %2E%2E", "/api/datasource/%2E%2E/audit", "/api/datasource", "", false},
		{"double encoded traversal", "/api/datasource/%252e%252e/audit", "/api/datasource", "", false},
		{"encoded slash smuggling", "/api/datasource/api%2fdatasources", "/api/datasource", "", false},
		{"nul byte", "/api/datasource/api/datasources%00.txt", "/api/datasource", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.target)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", tc.target, err)
			}
			got, ok := rewriteProxyRequestPath(u, tc.prefix)
			if ok != tc.wantOK || got != tc.wantPath {
				t.Errorf("rewriteProxyRequestPath(%q, %q) = (%q, %v), want (%q, %v)",
					tc.target, tc.prefix, got, ok, tc.wantPath, tc.wantOK)
			}
		})
	}
}

// TestMicroserviceProxy_RawDataBypassDenied 端到端（gin 路由层）断言：原始数据旁路
// 一律 403 FORBIDDEN_PATH，且上游服务完全不会被触达。
func TestMicroserviceProxy_RawDataBypassDenied(t *testing.T) {
	env := newProxyTestEnv(t)

	cases := []struct {
		name   string
		method string
		target string
	}{
		{"datasource records via proxy", http.MethodGet, "/api/datasource/api/datasources/ds_yibao/records?limit=50"},
		{"datasource records limit 1", http.MethodGet, "/api/datasource/api/datasources/ds_yibao/records?limit=1"},
		{"datasource sample", http.MethodGet, "/api/datasource/api/datasources/ds_yibao/sample"},
		{"raw yibao", http.MethodGet, "/api/datasource/api/v1/yibao"},
		{"raw kangyang", http.MethodGet, "/api/datasource/api/v1/kangyang"},
		{"raw mock3", http.MethodGet, "/api/datasource/api/v1/mock3"},
		{"raw mock4", http.MethodGet, "/api/datasource/api/v1/mock4"},
		{"method not allowlisted", http.MethodDelete, "/api/datasource/api/datasources"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env.resetLogs()
			code, body := env.serve(t, tc.method, tc.target, "")
			if code != http.StatusForbidden {
				t.Errorf("%s %s = %d, want 403 (body=%s)", tc.method, tc.target, code, body)
			}
			if got := envelopeCode(t, body); got != "FORBIDDEN_PATH" {
				t.Errorf("error code = %q, want FORBIDDEN_PATH", got)
			}
			if h := atomic.LoadInt64(env.upstreamHits); h != 0 {
				t.Errorf("upstream was called %d times for a denied request, want 0", h)
			}
			logged := env.logs.String()
			if !strings.Contains(logged, "microservice_proxy_call") {
				t.Errorf("missing structured proxy log line, got: %s", logged)
			}
			if !strings.Contains(logged, `"denied":true`) {
				t.Errorf("denied proxy call was not logged as denied, got: %s", logged)
			}
			if !strings.Contains(logged, `"method":"`+tc.method+`"`) {
				t.Errorf("proxy log line is missing the request method, got: %s", logged)
			}
			if !strings.Contains(logged, `"request_id":"req-p0-7"`) {
				t.Errorf("proxy log line is missing the request id, got: %s", logged)
			}
			atomic.StoreInt64(env.upstreamHits, 0)
		})
	}
}

// TestMicroserviceProxy_TraversalBlockedByWAF 断言路径穿越请求在 WAF 层即被拦截，
// 不会到达上游，也不会绕过微服务代理白名单。
func TestMicroserviceProxy_TraversalBlockedByWAF(t *testing.T) {
	env := newProxyTestEnv(t)

	cases := []struct {
		name   string
		method string
		target string
	}{
		{"traversal into records", http.MethodGet, "/api/datasource/api/datasources/ds_yibao/metadata/../records"},
		{"encoded traversal", http.MethodGet, "/api/datasource/%2e%2e/api/datasource/api/datasources/ds_yibao/records"},
		{"audit raw passthrough", http.MethodGet, "/api/audit/api/audit/../../../../etc/passwd"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env.resetLogs()
			code, body := env.serve(t, tc.method, tc.target, "")
			if code != http.StatusForbidden {
				t.Errorf("%s %s = %d, want 403 (body=%s)", tc.method, tc.target, code, body)
			}
			if got := envelopeCode(t, body); got != "WAF_BLOCKED" {
				t.Errorf("error code = %q, want WAF_BLOCKED", got)
			}
			if h := atomic.LoadInt64(env.upstreamHits); h != 0 {
				t.Errorf("upstream was called %d times for a WAF-blocked request, want 0", h)
			}
			logged := env.logs.String()
			if !strings.Contains(logged, "WAF attack detected") {
				t.Errorf("missing WAF detection log, got: %s", logged)
			}
			if !strings.Contains(logged, `"category":"PATH_TRAVERSAL"`) {
				t.Errorf("WAF log missing PATH_TRAVERSAL category, got: %s", logged)
			}
			if !strings.Contains(logged, `"request_id":"req-p0-7"`) {
				t.Errorf("WAF log missing request id, got: %s", logged)
			}
			atomic.StoreInt64(env.upstreamHits, 0)
		})
	}
}

// TestMicroserviceProxy_AllowedConsoleEndpoints 断言白名单内的只读 / 调度端点仍可用。
func TestMicroserviceProxy_AllowedConsoleEndpoints(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		target     string
		wantStatus int
		wantPath   string
	}{
		{"datasource catalog", http.MethodGet, "/api/datasource/api/datasources", http.StatusOK, "/api/datasources"},
		{"datasource metadata", http.MethodGet, "/api/datasource/api/datasources/ds_yibao/metadata", http.StatusOK, "/api/datasources/ds_yibao/metadata"},
		{"audit stats", http.MethodGet, "/api/audit/api/audit/stats", http.StatusOK, "/api/audit/stats"},
		{"hub tasks", http.MethodGet, "/api/hub/api/hub/tasks", http.StatusOK, "/api/hub/tasks"},
		{"hub dispatch", http.MethodPost, "/api/hub/api/hub/dispatch", http.StatusOK, "/api/hub/dispatch"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newProxyTestEnv(t)
			env.resetLogs()
			code, body := env.serve(t, tc.method, tc.target, `{"noop":true}`)
			if code != tc.wantStatus {
				t.Fatalf("%s %s = %d, want %d (body=%s)", tc.method, tc.target, code, tc.wantStatus, body)
			}
			if got, _ := env.upstreamPath.Load().(string); got != tc.wantPath {
				t.Errorf("upstream path = %q, want %q", got, tc.wantPath)
			}
			if h := atomic.LoadInt64(env.upstreamHits); h != 1 {
				t.Errorf("upstream hits = %d, want 1", h)
			}
			logged := env.logs.String()
			if !strings.Contains(logged, `"denied":false`) {
				t.Errorf("allowed proxy call was not logged, got: %s", logged)
			}
			if !strings.Contains(logged, `"caller":"subject=console-api-key;ip=192.0.2.1"`) {
				t.Errorf("proxy log line is missing caller identity, got: %s", logged)
			}
		})
	}
}
