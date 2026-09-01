// Package handlers 的单元测试。
//
// 测试策略：使用 httptest 构造本地 HTTP 请求，验证路由注册、响应格式和业务逻辑。
// 测试用例：
//   - TestHealthCheck: 验证健康检查端点返回 200 + status=ok
//   - TestGetTopology: 验证拓扑返回固定 4 服务顺序 + REST/gRPC 双协议
//   - TestGetSuitesAndRun: 验证获取套件列表 + 执行 TS-01/02/03
//   - TestGetLeases: 验证租约查询返回 sqlite 后端标识
package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/clients"
	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/config"
	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/models"
	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/runner"
)

// setupTestRouter 创建测试用的 Handler 实例。
// 使用本地默认地址构造配置，无需真实上游服务运行。
func setupTestRouter() *Handler {
	cfg := &config.Config{
		Host:          "127.0.0.1",
		Port:          "8085",
		HubURL:        "http://127.0.0.1:8082",
		DatasourceURL: "http://127.0.0.1:8083",
		AuditURL:      "http://127.0.0.1:8084",
		AgentURL:      "http://127.0.0.1:8079",
	}
	pool := clients.NewClientPool(cfg)
	testRunner := runner.NewTestRunner(pool)
	return NewHandler(cfg, pool, testRunner, nil, nil)
}

// TestHealthCheck 验证健康检查端点。
// 期望：HTTP 200，响应体包含 status="ok"。
func TestHealthCheck(t *testing.T) {
	h := setupTestRouter()
	router := SetupRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/health", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse json: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %v", body["status"])
	}
}

// TestGetTopology 验证服务拓扑探测端点。
//
// 测试步骤：
//  1. GET /api/lz/topology?protocol=rest → 验证返回 4 个服务
//  2. 验证固定顺序：service-hub → engine → datasource-mgr → audit-log
//  3. GET /api/lz/topology?protocol=grpc → 验证 gRPC 协议视角也能正常返回
func TestGetTopology(t *testing.T) {
	h := setupTestRouter()
	router := SetupRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/lz/topology?protocol=rest", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var topo models.TopologyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &topo); err != nil {
		t.Fatalf("failed to parse topology: %v", err)
	}
	if len(topo.Services) != 4 {
		t.Fatalf("expected 4 services, got %d", len(topo.Services))
	}

	// 验证固定 4 服务顺序（前端拓扑大屏依赖此顺序）
	expectedOrder := []string{"service-hub", "engine", "datasource-mgr", "audit-log"}
	for i, exp := range expectedOrder {
		if topo.Services[i].ID != exp {
			t.Errorf("expected service[%d] = %s, got %s", i, exp, topo.Services[i].ID)
		}
	}

	// 测试 gRPC 协议视角的拓扑查询
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodGet, "/api/lz/topology?protocol=grpc", nil)
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected status 200 for grpc query, got %d", w2.Code)
	}
}

// TestGetSuitesAndRun 验证测试套件的获取和执行。
//
// 测试步骤：
//  1. GET /api/lz/suites → 验证返回可用套件列表
//  2. POST /api/lz/suites/run → 执行 TS-01/02/03，验证返回 3 个结果
func TestGetSuitesAndRun(t *testing.T) {
	h := setupTestRouter()
	router := SetupRouter(h)

	// 步骤 1：获取可用套件列表
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/lz/suites", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	// 步骤 2：执行 TS-01/02/03/04 四个套件
	runPayload := models.RunTestSuiteRequest{
		SuiteIDs:          []string{"TS-01", "TS-02", "TS-03", "TS-04"},
		Concurrency:       5,
		BenchmarkRequests: 10,
	}
	data, _ := json.Marshal(runPayload)

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodPost, "/api/lz/suites/run", bytes.NewReader(data))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w2.Code)
	}

	var runResp models.RunTestSuiteResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &runResp); err != nil {
		t.Fatalf("failed to parse run response: %v", err)
	}
	if runResp.TotalCases != 4 {
		t.Errorf("expected 4 total suite items, got %d", runResp.TotalCases)
	}
}

// TestGetLeases 验证租约查询端点。
// 期望：HTTP 200，StoreBackend 为 "sqlite"（测试环境无真实 Hub）。
func TestGetLeases(t *testing.T) {
	h := setupTestRouter()
	router := SetupRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/lz/tasks/leases", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var leases models.LeasedTasksResponse
	if err := json.Unmarshal(w.Body.Bytes(), &leases); err != nil {
		t.Fatalf("failed to parse leases: %v", err)
	}
	if leases.StoreBackend != "sqlite" {
		t.Errorf("expected sqlite store, got %s", leases.StoreBackend)
	}
}

// TestGetDataApiDefinitions 验证预设数据 API 目录接口返回 canonical 字段（api_code / datasource_id）。
func TestGetDataApiDefinitions(t *testing.T) {
	h := setupTestRouter()
	router := SetupRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/lz/data-api/definitions", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var body struct {
		APIs []models.DataApiDef `json:"apis"`
		Via  string              `json:"via"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse definitions response: %v", err)
	}
	if len(body.APIs) != 4 {
		t.Fatalf("expected 4 api definitions, got %d", len(body.APIs))
	}
	if body.APIs[0].APICode != "api1_yibao" || body.APIs[0].DatasourceID != "ds_yibao" {
		t.Errorf("unexpected API1 definition: %+v", body.APIs[0])
	}
	if body.APIs[1].APICode != "api2_kangyang" || body.APIs[1].DatasourceID != "ds_kangyang" {
		t.Errorf("unexpected API2 definition: %+v", body.APIs[1])
	}
}

// TestInvokeDataApiContractAndFailClosed 验证 InvokeDataApi 会话调用契约与 fail-closed：
// 1. api_code=api1_yibao 正常产生 6 阶段流水线响应
// 2. 未知 api_code 返回 400 INVALID_API_CODE
// 3. 预留 API 返回 409 RESERVED_DATASOURCE
// 4. api_code 与 datasource_id 冲突返回 400 API_DATASOURCE_MISMATCH
func TestInvokeDataApiContractAndFailClosed(t *testing.T) {
	h := setupTestRouter()
	router := SetupRouter(h)

	// 1. 正常调用 API1
	invokePayload := models.DataApiInvokeRequest{
		APICode: "api1_yibao",
		Limit:   3,
	}
	data, _ := json.Marshal(invokePayload)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/lz/data-api/invoke", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for api1_yibao, got %d: %s", w.Code, w.Body.String())
	}

	var resp models.DataApiSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal session response: %v", err)
	}
	if resp.APICode != "api1_yibao" || resp.DatasourceID != "ds_yibao" {
		t.Errorf("expected api1_yibao / ds_yibao, got %s / %s", resp.APICode, resp.DatasourceID)
	}
	if len(resp.Stages) != 5 {
		t.Errorf("expected 5 pipeline stages (ingest->fetch->classify_desensitize->return->audit), got %d", len(resp.Stages))
	}
	expectedStages := []string{"ingest", "fetch", "classify_desensitize", "return", "audit"}
	for i, exp := range expectedStages {
		if i < len(resp.Stages) && resp.Stages[i].Name != exp {
			t.Errorf("stage[%d] = %s, want %s", i, resp.Stages[i].Name, exp)
		}
	}

	// 2. 未知 api_code (shebao)
	invUnknown, _ := json.Marshal(models.DataApiInvokeRequest{APICode: "api3_shebao"})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodPost, "/api/lz/data-api/invoke", bytes.NewReader(invUnknown))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown api_code, got %d", w2.Code)
	}

	// 3. 预留数据源 (api_id=3)
	invReserved, _ := json.Marshal(models.DataApiInvokeRequest{ApiID: 3})
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest(http.MethodPost, "/api/lz/data-api/invoke", bytes.NewReader(invReserved))
	req3.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w3, req3)
	if w3.Code != http.StatusConflict {
		t.Errorf("expected 409 for reserved api_id=3, got %d", w3.Code)
	}

	// 4. 冲突不自洽 (api1_yibao + ds_kangyang)
	invMismatch, _ := json.Marshal(models.DataApiInvokeRequest{
		APICode:      "api1_yibao",
		DatasourceID: "ds_kangyang",
	})
	w4 := httptest.NewRecorder()
	req4, _ := http.NewRequest(http.MethodPost, "/api/lz/data-api/invoke", bytes.NewReader(invMismatch))
	req4.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w4, req4)
	if w4.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for mismatched api/datasource, got %d", w4.Code)
	}
}

// TestTraceMiddlewareRegistered 验证 TraceMiddleware 已注册到路由中。
// 期望：响应头包含 X-Request-ID 和 X-Trace-ID。
func TestTraceMiddlewareRegistered(t *testing.T) {
	h := setupTestRouter()
	router := SetupRouter(h)

	// 1. 无请求头时自动生成 trace ID
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/health", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	reqID := w.Header().Get("X-Request-ID")
	traceID := w.Header().Get("X-Trace-ID")
	if reqID == "" {
		t.Error("expected X-Request-ID header to be set by TraceMiddleware")
	}
	if traceID == "" {
		t.Error("expected X-Trace-ID header to be set by TraceMiddleware")
	}
	if reqID != traceID {
		t.Errorf("expected X-Request-ID == X-Trace-ID, got %q != %q", reqID, traceID)
	}

	// 2. 上游传入 X-Request-ID 时应透传
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodGet, "/api/health", nil)
	req2.Header.Set("X-Request-ID", "req-test-upstream-123")
	router.ServeHTTP(w2, req2)

	if got := w2.Header().Get("X-Request-ID"); got != "req-test-upstream-123" {
		t.Errorf("expected X-Request-ID passthrough, got %q", got)
	}
	if got := w2.Header().Get("X-Trace-ID"); got != "req-test-upstream-123" {
		t.Errorf("expected X-Trace-ID passthrough, got %q", got)
	}
}

// TestRateLimitMiddleware 验证令牌桶限流中间件：
// 1. RPS > 0 时，超限请求返回 429 Too Many Requests
// 2. RPS = 0 时，限流中间件不启用，所有请求正常通过
func TestRateLimitMiddleware(t *testing.T) {
	// 1. 低 RPS 配置：突发 2，每秒 1 个请求
	cfg := &config.Config{
		Host:           "127.0.0.1",
		Port:           "8085",
		HubURL:         "http://127.0.0.1:8082",
		DatasourceURL:  "http://127.0.0.1:8083",
		AuditURL:       "http://127.0.0.1:8084",
		AgentURL:       "http://127.0.0.1:8079",
		RateLimitRPS:   1,
		RateLimitBurst: 2,
	}
	pool := clients.NewClientPool(cfg)
	h := NewHandler(cfg, pool, runner.NewTestRunner(pool), nil, nil)
	router := SetupRouter(h)

	// 突发 2 次应成功（桶容量 2）
	// 注意：/health 和 /api/health 被 RateLimit 中间件豁免，使用 /api/lz/topology 测试
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/lz/topology", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	// 第 3 次应被限流（桶已耗尽）
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/lz/topology", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 after burst exhausted, got %d", w.Code)
	}
	if retry := w.Header().Get("Retry-After"); retry == "" {
		t.Error("expected Retry-After header on 429 response")
	}

	// 2. RPS=0 配置：限流禁用，所有请求应通过
	cfg2 := &config.Config{
		Host:          "127.0.0.1",
		Port:          "8085",
		HubURL:        "http://127.0.0.1:8082",
		DatasourceURL: "http://127.0.0.1:8083",
		AuditURL:      "http://127.0.0.1:8084",
		AgentURL:      "http://127.0.0.1:8079",
		RateLimitRPS:  0, // 禁用限流
	}
	pool2 := clients.NewClientPool(cfg2)
	h2 := NewHandler(cfg2, pool2, runner.NewTestRunner(pool2), nil, nil)
	router2 := SetupRouter(h2)

	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/lz/topology", nil)
		router2.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("RPS=0: request %d: expected 200, got %d", i+1, w.Code)
		}
	}
}

// TestAuthMiddleware 验证 API Key 鉴权中间件：空密钥跳过、正确密钥通过、错误密钥 401。
func TestAuthMiddleware(t *testing.T) {
	// 场景 1：API Key 为空 → 全部通过
	cfg1 := &config.Config{RateLimitRPS: 0}
	pool1 := clients.NewClientPool(cfg1)
	h1 := NewHandler(cfg1, pool1, runner.NewTestRunner(pool1), nil, nil)
	r1 := SetupRouter(h1)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/lz/topology", nil)
	r1.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("empty APIKey: expected 200, got %d", w.Code)
	}

	// 场景 2：API Key 设置 + 正确密钥 → 通过
	cfg2 := &config.Config{APIKey: "test-secret-key", RateLimitRPS: 0}
	pool2 := clients.NewClientPool(cfg2)
	h2 := NewHandler(cfg2, pool2, runner.NewTestRunner(pool2), nil, nil)
	r2 := SetupRouter(h2)

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/api/lz/topology", nil)
	req2.Header.Set("Authorization", "Bearer test-secret-key")
	r2.ServeHTTP(w2, req2)
	// 200 或 502（上游不可达）均表示通过了鉴权
	if w2.Code == http.StatusUnauthorized {
		t.Errorf("correct APIKey: got 401, should pass auth")
	}

	// 场景 3：API Key 设置 + 缺失密钥 → 401
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/api/lz/topology", nil)
	r2.ServeHTTP(w3, req3)
	if w3.Code != http.StatusUnauthorized {
		t.Errorf("missing APIKey: expected 401, got %d", w3.Code)
	}

	// 场景 4：健康检查端点豁免
	w4 := httptest.NewRecorder()
	req4, _ := http.NewRequest("GET", "/api/health", nil)
	r2.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("health endpoint: expected 200, got %d", w4.Code)
	}
}
