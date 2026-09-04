package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/config"
	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/models"
	naming "github.com/fengzhizi319/PrivShield-go/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
)

// TestD01_GetDatasourcesPathAndFallback 验证 D-01 修复：
// 1. 发起请求路径必须为 /v1/hub/datasources（通过 service-hub 编排）
// 2. 真实上游 200 时 Source 标为 "service-hub"
// 3. 上游不可达时返回 fallback 兜底且 Source="fallback"
func TestD01_GetDatasourcesPathAndFallback(t *testing.T) {
	requestedPath := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total": 2,
			"datasources": []map[string]any{
				{"id": "ds_yibao", "name": "医保数据", "category": "medical"},
				{"id": "ds_kangyang", "name": "康养数据", "category": "healthcare"},
			},
			"via": "service-hub",
		})
	}))
	defer server.Close()

	cfg := &config.Config{HubURL: server.URL}
	pool := NewClientPool(cfg)

	resp, err := pool.GetDatasources(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requestedPath != "/v1/hub/datasources" {
		t.Errorf("expected path /v1/hub/datasources, got %s", requestedPath)
	}
	if resp.Source != "service-hub" {
		t.Errorf("expected Source=service-hub, got %s", resp.Source)
	}
	if len(resp.Datasources) != 2 {
		t.Errorf("expected 2 datasources, got %d", len(resp.Datasources))
	}
	// 验证 canonical ID 归一化双写
	if resp.Datasources[0].DatasourceID != naming.DSYibao {
		t.Errorf("expected datasource_id %s, got %s", naming.DSYibao, resp.Datasources[0].DatasourceID)
	}

	// 验证降级分支
	cfgDead := &config.Config{HubURL: "http://127.0.0.1:59999"}
	poolDead := NewClientPool(cfgDead)
	fbResp, err := poolDead.GetDatasources(context.Background())
	if err != nil {
		t.Fatalf("degraded mode should return nil error, got %v", err)
	}
	if fbResp.Source != sourceFallback {
		t.Errorf("expected Source=fallback on unreachable upstream, got %s", fbResp.Source)
	}
	if len(fbResp.Datasources) == 0 {
		t.Errorf("expected non-empty fallback datasources")
	}
}

// TestD02_AuditPathsAndVerify 验证 D-02 修复：
// 1. GetAuditLogs 请求路径为 /v1/hub/audit/logs（通过 service-hub 编排）
// 2. VerifyAudit 调 /v1/hub/audit/verify
// 3. 失败时绝不合成 MerkleValid: true
func TestD02_AuditPathsAndVerify(t *testing.T) {
	paths := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/hub/audit/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total": 1,
				"logs": []map[string]any{
					{
						"id":             "audit-123",
						"timestamp":      "2026-08-27T00:00:00Z",
						"datasource":     "ds_yibao",
						"operation":      "mask",
						"input_hash":     "hash-in",
						"output_hash":    "hash-out",
						"algorithm":      "three_layer_funnel",
						"user":           "test-user",
						"status":         "success",
						"security_level": "L3",
					},
				},
				"via": "service-hub",
			})
		case "/v1/hub/audit/verify":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"snapshot_id":  "snap-001",
				"merkle_valid": true,
				"actual":       "sha256:abc",
				"expected":     "sha256:abc",
				"via":          "service-hub",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := &config.Config{HubURL: server.URL}
	pool := NewClientPool(cfg)

	// 1. GetAuditLogs
	logsResp, err := pool.GetAuditLogs(context.Background(), 10, 0, "yibao")
	if err != nil {
		t.Fatalf("unexpected GetAuditLogs error: %v", err)
	}
	if logsResp.Source != "service-hub" {
		t.Errorf("expected Source=service-hub, got %s", logsResp.Source)
	}
	if len(logsResp.Logs) != 1 || logsResp.Logs[0].Datasource != naming.DSYibao {
		t.Errorf("unexpected logs response: %+v", logsResp)
	}

	// 2. VerifyAudit
	vResp, err := pool.VerifyAudit(context.Background())
	if err != nil {
		t.Fatalf("unexpected VerifyAudit error: %v", err)
	}
	if !vResp.MerkleValid {
		t.Errorf("expected MerkleValid=true from live server")
	}
	if vResp.SnapshotID != "snap-001" {
		t.Errorf("expected SnapshotID=snap-001, got %s", vResp.SnapshotID)
	}

	// 验证请求路径全为 canonical（无旧版 /api/ 前缀）
	for _, p := range paths {
		if strings.HasPrefix(p, "/api/") {
			t.Errorf("D-02 violation: deprecated path called: %s", p)
		}
	}

	// 3. 验证上游不可达时绝不合成 MerkleValid: true
	cfgDead := &config.Config{HubURL: "http://127.0.0.1:59999"}
	poolDead := NewClientPool(cfgDead)
	vDead, _ := poolDead.VerifyAudit(context.Background())
	if vDead.MerkleValid {
		t.Errorf("D-02 violation: fallback must never claim MerkleValid=true")
	}
	if vDead.Source != sourceFallback {
		t.Errorf("expected Source=fallback on dead upstream, got %s", vDead.Source)
	}
}

// TestD11_GetDatasourceSliceFailClosed 验证 D-11 修复：
// 1. 未知数据源（如 shebao/custom）返回 400 INVALID_DATASOURCE_ID，绝不静默落到医保
// 2. 预留数据源（如 mock3）返回 409 RESERVED_DATASOURCE
// 3. 别名（yibao / api1_yibao / 医保）正确归一化为 ds_yibao
func TestD11_GetDatasourceSliceFailClosed(t *testing.T) {
	requestedDatasource := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) >= 5 && parts[1] == "api" && parts[2] == "hub" && parts[3] == "datasources" {
			requestedDatasource = parts[4]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"datasource_id": requestedDatasource,
			"total":         1,
			"records": []map[string]any{
				{"field1": "val1"},
			},
		})
	}))
	defer server.Close()

	cfg := &config.Config{HubURL: server.URL}
	pool := NewClientPool(cfg)

	// 1. 未知 ID 必须 fail-closed 报错（400 INVALID_DATASOURCE_ID）
	for _, unknownID := range []string{"shebao", "ds_shebao", "ds_custom", "unknown_123"} {
		_, err := pool.GetDatasourceSlice(context.Background(), unknownID, 5, 0)
		if err == nil {
			t.Fatalf("D-11 violation: GetDatasourceSlice(%q) must fail closed with error", unknownID)
		}
		ue, ok := err.(*UpstreamError)
		if !ok || ue.Code != CodeInvalidDatasourceID {
			t.Errorf("GetDatasourceSlice(%q) error = %v, want Code=%s", unknownID, err, CodeInvalidDatasourceID)
		}
	}

	// 2. 预留位必须返回 409 RESERVED_DATASOURCE
	for _, reservedID := range []string{"mock3", "ds_mock3", "mock4", "ds_mock4"} {
		_, err := pool.GetDatasourceSlice(context.Background(), reservedID, 5, 0)
		if err == nil {
			t.Fatalf("GetDatasourceSlice(%q) must reject reserved datasource", reservedID)
		}
		ue, ok := err.(*UpstreamError)
		if !ok || ue.Code != CodeReservedDatasource {
			t.Errorf("GetDatasourceSlice(%q) error = %v, want Code=%s", reservedID, err, CodeReservedDatasource)
		}
	}

	// 3. 别名正常通过并映射到 canonical
	aliases := []struct {
		in   string
		want string
	}{
		{"yibao", naming.DSYibao},
		{"api1_yibao", naming.DSYibao},
		{"医保", naming.DSYibao},
		{"kangyang", naming.DSKangyang},
		{"api2_kangyang", naming.DSKangyang},
		{"康养", naming.DSKangyang},
	}
	for _, a := range aliases {
		resp, err := pool.GetDatasourceSlice(context.Background(), a.in, 5, 0)
		if err != nil {
			t.Errorf("GetDatasourceSlice(%q) unexpected error: %v", a.in, err)
			continue
		}
		if resp.DatasourceID != a.want {
			t.Errorf("GetDatasourceSlice(%q) returned datasource_id=%s, want %s", a.in, resp.DatasourceID, a.want)
		}
	}
}

// TestGetDatasourceRecordByIDCard 验证按身份证号查询单条记录的新接口：
// 1. 未知数据源返回 400 INVALID_DATASOURCE_ID
// 2. 预留数据源返回 409 RESERVED_DATASOURCE
// 3. 合法数据源别名正确归一化并通过 service-hub 的 FetchAndDesensitize 编排
func TestGetDatasourceRecordByIDCard(t *testing.T) {
	requestedPath := ""
	requestedIDCard := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		requestedIDCard = body["id_card_no"]
		_ = json.NewEncoder(w).Encode(map[string]any{
			"datasource_id":  body["datasource_id"],
			"sanitized_data": map[string]any{"id_card_no": body["id_card_no"], "gender": "男"},
			"found":          true,
			"via":            "service-hub",
		})
	}))
	defer server.Close()

	cfg := &config.Config{HubURL: server.URL}
	pool := NewClientPool(cfg)

	// 1. 未知 ID 必须 fail-closed
	for _, unknownID := range []string{"shebao", "ds_unknown"} {
		_, err := pool.GetDatasourceRecordByIDCard(context.Background(), unknownID, "110101196809171010")
		if err == nil {
			t.Fatalf("GetDatasourceRecordByIDCard(%q) must fail closed", unknownID)
		}
		ue, ok := err.(*UpstreamError)
		if !ok || ue.Code != CodeInvalidDatasourceID {
			t.Errorf("GetDatasourceRecordByIDCard(%q) error = %v, want Code=%s", unknownID, err, CodeInvalidDatasourceID)
		}
	}

	// 2. 预留位必须返回 409
	for _, reservedID := range []string{"mock3", "ds_mock3"} {
		_, err := pool.GetDatasourceRecordByIDCard(context.Background(), reservedID, "110101196809171010")
		if err == nil {
			t.Fatalf("GetDatasourceRecordByIDCard(%q) must reject reserved datasource", reservedID)
		}
		ue, ok := err.(*UpstreamError)
		if !ok || ue.Code != CodeReservedDatasource {
			t.Errorf("GetDatasourceRecordByIDCard(%q) error = %v, want Code=%s", reservedID, err, CodeReservedDatasource)
		}
	}

	// 3. 合法别名正常通过并调用正确路径
	resp, err := pool.GetDatasourceRecordByIDCard(context.Background(), "yibao", "110101196809171010")
	if err != nil {
		t.Fatalf("GetDatasourceRecordByIDCard(yibao) unexpected error: %v", err)
	}
	if requestedPath != "/v1/hub/fetch-and-desensitize" {
		t.Errorf("expected path /v1/hub/fetch-and-desensitize, got %s", requestedPath)
	}
	if requestedIDCard != "110101196809171010" {
		t.Errorf("expected id_card_no 110101196809171010, got %s", requestedIDCard)
	}
	if resp.DatasourceID != naming.DSYibao {
		t.Errorf("expected datasource_id=%s, got %s", naming.DSYibao, resp.DatasourceID)
	}
}

// TestD03_RecordAuditRealAndNoForging 验证 D-03 修复：
// 真实 RecordAudit 会向 service-hub 的 POST /v1/hub/audit/logs 发送请求并返回真实 ID
func TestD03_RecordAuditRealAndNoForging(t *testing.T) {
	receivedDatasource := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/hub/audit/logs" && r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			receivedDatasource, _ = body["datasource"].(string)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":   "real-audit-id-999",
				"via":  "service-hub",
				"task": "task-001",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := &config.Config{HubURL: server.URL}
	pool := NewClientPool(cfg)

	id, err := pool.RecordAudit(context.Background(), models.AuditRecordRequest{
		Datasource: "yibao",
		APICode:    "api1_yibao",
		Operation:  "mask",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "real-audit-id-999" {
		t.Errorf("expected real-audit-id-999, got %s", id)
	}
	if receivedDatasource != naming.DSYibao {
		t.Errorf("outbound datasource should be normalized to %s, got %s", naming.DSYibao, receivedDatasource)
	}
}

// TestP02_GetTaskEnvelopeUnpack 验证 P0-2 修复：
// 验证 BFF GetTask 能正确解包 service-hub 返回的 {"task": {...}, "via": "service-hub"} 外壳
func TestP02_GetTaskEnvelopeUnpack(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/hub/tasks/task-12345") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task": map[string]any{
					"id":            "task-12345",
					"status":        "completed",
					"stage":         "audit",
					"source":        "ds_yibao",
					"api_code":      "api1_yibao",
					"datasource_id": "ds_yibao",
					"operation":     "mask",
				},
				"via": "service-hub",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := &config.Config{HubURL: server.URL}
	pool := NewClientPool(cfg)

	task, err := pool.GetTask(context.Background(), "task-12345")
	if err != nil {
		t.Fatalf("unexpected GetTask error: %v", err)
	}
	if task.ID != "task-12345" {
		t.Errorf("P0-2 violation: expected task.ID=task-12345, got %q (unpacked zero value)", task.ID)
	}
	if task.DatasourceID != "ds_yibao" || task.APICode != "api1_yibao" {
		t.Errorf("expected canonical IDs in task, got datasource_id=%s api_code=%s", task.DatasourceID, task.APICode)
	}
	if task.Status != "completed" || task.Stage != "audit" {
		t.Errorf("expected status=completed stage=audit, got status=%s stage=%s", task.Status, task.Stage)
	}
}

// TestP1_OutboundHeadersInjected verifies that every outbound HTTP call from ClientPool
// carries the propagated X-Request-ID / X-Trace-ID headers and the per-service
// Authorization: Bearer <APIKey> header.
func TestP1_OutboundHeadersInjected(t *testing.T) {
	captured := make(map[string]http.Header)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured[r.URL.Path] = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/v1/hub/tasks":
			_ = json.NewEncoder(w).Encode(models.TasksResponse{Total: 0, Tasks: []models.Task{}})
		case "/v1/hub/datasources":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total":       1,
				"datasources": []map[string]any{{"id": naming.DSYibao, "datasource_id": naming.DSYibao, "name": "医保", "category": "medical"}},
				"via":         "service-hub",
			})
		case "/v1/hub/audit/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 0, "logs": []models.AuditLogItem{}, "via": "service-hub"})
		case "/v1/hub/pipeline":
			_ = json.NewEncoder(w).Encode(map[string]any{"mode": "pipeline_telemetry", "stages": []map[string]any{}})
		case "/v1/hub/dispatch":
			_ = json.NewEncoder(w).Encode(models.DispatchResponse{TaskID: "task-p1-123", Status: "accepted", Via: "service-hub"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		HubURL:    server.URL,
		HubAPIKey: "hub-key-123",
	}
	pool := NewClientPool(cfg)
	ctx := pkgobs.ContextWithRequestID(context.Background(), "test-trace-123")

	if _, err := pool.ListTasks(ctx, "pending", 10, 0); err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if _, err := pool.GetDatasources(ctx); err != nil {
		t.Fatalf("GetDatasources failed: %v", err)
	}
	if _, err := pool.GetAuditLogs(ctx, 10, 0, ""); err != nil {
		t.Fatalf("GetAuditLogs failed: %v", err)
	}
	if _, err := pool.GetPipelineStatus(ctx); err != nil {
		t.Fatalf("GetPipelineStatus failed: %v", err)
	}
	if _, err := pool.MaskRecordViaEngine(ctx, map[string]any{"name": "张老"}); err != nil {
		t.Fatalf("MaskRecordViaEngine failed: %v", err)
	}

	expectations := map[string]string{
		"/v1/hub/tasks":       cfg.HubAPIKey,
		"/v1/hub/datasources": cfg.HubAPIKey,
		"/v1/hub/audit/logs":  cfg.HubAPIKey,
		"/v1/hub/pipeline":    cfg.HubAPIKey,
		"/v1/hub/dispatch":    cfg.HubAPIKey,
	}

	for path, expectedKey := range expectations {
		hdrs, ok := captured[path]
		if !ok {
			t.Errorf("expected request to %s to be captured", path)
			continue
		}
		if got := hdrs.Get("X-Request-ID"); got != "test-trace-123" {
			t.Errorf("%s: X-Request-ID = %q, want %q", path, got, "test-trace-123")
		}
		if got := hdrs.Get("X-Trace-ID"); got != "test-trace-123" {
			t.Errorf("%s: X-Trace-ID = %q, want %q", path, got, "test-trace-123")
		}
		wantAuth := "Bearer " + expectedKey
		if got := hdrs.Get("Authorization"); got != wantAuth {
			t.Errorf("%s: Authorization = %q, want %q", path, got, wantAuth)
		}
	}
}
