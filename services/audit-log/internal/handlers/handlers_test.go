package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield/pkg/metrics"
	"github.com/fengzhizi319/PrivShield/pkg/store"
	"github.com/fengzhizi319/PrivShield/pkg/store/flusher"
	"github.com/fengzhizi319/PrivShield/pkg/store/memory"

	"github.com/fengzhizi319/PrivShield/services/audit-log/internal/agent"
	"github.com/fengzhizi319/PrivShield/services/audit-log/internal/config"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// testDeps bundles shared test dependencies.
type testDeps struct {
	audit  *memory.AuditStore
	logger *slog.Logger
	mc     *metrics.Collector
}

func newTestServer() *Server {
	cfg := &config.Config{
		Host:          "127.0.0.1",
		Port:          0,
		AgentRESTHost: "127.0.0.1",
		AgentRESTPort: 19999, // unreachable
		AgentAPIKey:   "",
	}
	d := &testDeps{
		audit:  memory.NewAuditStore(),
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		mc:     metrics.NewCollector("audit-log-test"),
	}
	ag := agent.New(cfg)
	return New(ag, cfg, d.audit, d.logger, d.mc)
}

func newTestRouter(s *Server) *gin.Engine {
	r := gin.New()
	s.RegisterRoutes(r)
	return r
}

func TestHealth(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/health", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
	if resp["via"] != "audit-log" {
		t.Errorf("expected via=audit-log, got %v", resp["via"])
	}
}

func TestListLogsEmpty(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/audit/logs", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["total"].(float64) != 0 {
		t.Errorf("expected 0 logs, got %v", resp["total"])
	}
}

func TestCreateLog(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	body := map[string]any{
		"operation":      "mask",
		"datasource":     "ds_yibao",
		"algorithm":      "field_mask",
		"parameters":     map[string]any{"fields": []string{"name", "id_card"}},
		"input_rows":     1000,
		"output_rows":    1000,
		"duration_ms":    45,
		"user":           "admin",
		"status":         "success",
		"security_level": "L3",
	}
	b, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["id"] == nil || resp["id"] == "" {
		t.Error("expected non-empty id")
	}

	// Verify it appears in list
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/api/audit/logs", nil)
	router.ServeHTTP(w2, req2)

	var resp2 map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2["total"].(float64) != 1 {
		t.Errorf("expected 1 log, got %v", resp2["total"])
	}
}

func TestCreateLogInvalidBody(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	// Empty body should fail because operation and status are required
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateLogRejectsCallerSuppliedPrevHash asserts the chain predecessor is server-assigned:
// honouring a client value would let any caller fork or permanently break the tamper chain.
func TestCreateLogRejectsCallerSuppliedPrevHash(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	body, _ := json.Marshal(map[string]any{
		"operation":  "mask",
		"datasource": "ds_yibao",
		"status":     "success",
		"prev_hash":  "cafe0000_client_forged",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for caller-supplied prev_hash, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateLogChainIsServerAssigned asserts consecutive records are linked by the store, and
// that the linked values are what the 201 responses reported to the callers.
func TestCreateLogChainIsServerAssigned(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	post := func() map[string]any {
		body, _ := json.Marshal(map[string]any{
			"operation":  "mask",
			"datasource": "ds_yibao",
			"status":     "success",
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return resp
	}

	first := post()
	second := post()

	if first["integrity_hash"] == "" {
		t.Fatalf("first response missing integrity_hash: %+v", first)
	}
	if second["prev_hash"] != first["integrity_hash"] {
		t.Fatalf("chain not server-linked: second.prev=%v first.integrity=%v", second["prev_hash"], first["integrity_hash"])
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/audit/chain/verify?limit=100", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("verify endpoint returned %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		Valid         bool `json:"valid"`
		TotalVerified int  `json:"total_verified"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal verify response: %v (%s)", err, w.Body.String())
	}
	if !res.Valid || res.TotalVerified != 2 {
		t.Fatalf("expected a valid 2-record chain, got %+v", res)
	}
}

func TestGetLogNotFound(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/audit/logs/nonexistent", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetLog(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	// Create a log
	body := map[string]any{
		"operation":      "k_anon",
		"datasource":     "ds_yibao",
		"algorithm":      "k_anonymity",
		"input_rows":     5000,
		"output_rows":    5000,
		"duration_ms":    120,
		"user":           "data_scientist",
		"status":         "success",
		"security_level": "L4",
	}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	var createResp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &createResp)
	id := createResp["id"].(string)

	// Get the log
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/api/audit/logs/"+id, nil)
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	var log map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &log)
	if log["operation"] != "k_anon" {
		t.Errorf("expected operation=k_anon, got %v", log["operation"])
	}
	if log["security_level"] != "L4" {
		t.Errorf("expected security_level=L4, got %v", log["security_level"])
	}
}

func TestGetStats(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	// Create some logs
	for _, op := range []string{"mask", "mask", "k_anon", "dp"} {
		body := map[string]any{
			"operation":      op,
			"datasource":     "ds_yibao",
			"status":         "success",
			"security_level": "L3",
			"duration_ms":    50,
		}
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
	}

	// Get stats
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/audit/stats", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var stats map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &stats)
	if stats["total_operations"].(float64) != 4 {
		t.Errorf("expected 4 total ops, got %v", stats["total_operations"])
	}
	byOp := stats["by_operation"].(map[string]any)
	if byOp["mask"].(float64) != 2 {
		t.Errorf("expected 2 mask ops, got %v", byOp["mask"])
	}
}

func TestListSnapshots(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	// Create a log (which auto-generates a snapshot)
	body := map[string]any{
		"datasource": "ds_yibao",
		"operation":  "mask",
		"algorithm":  "field_mask",
		"status":     "success",
	}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	// List snapshots
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/api/audit/snapshots", nil)
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &resp)
	if resp["total"].(float64) != 1 {
		t.Errorf("expected 1 snapshot, got %v", resp["total"])
	}
}

func TestVerifyIntegrity(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	// Create a log (auto-generates snapshot)
	body := map[string]any{
		"datasource": "ds_yibao",
		"operation":  "mask",
		"algorithm":  "field_mask",
		"status":     "success",
	}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create log: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// List snapshots to get the actual snapshot ID
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/api/audit/snapshots", nil)
	router.ServeHTTP(w2, req2)

	var listResp map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &listResp)
	snaps := listResp["snapshots"].([]any)
	if len(snaps) == 0 {
		t.Fatal("expected at least 1 snapshot")
	}
	snapID := snaps[0].(map[string]any)["id"].(string)

	// Verify the snapshot
	verifyBody := map[string]any{"snapshot_id": snapID}
	vb, _ := json.Marshal(verifyBody)
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("POST", "/api/audit/snapshots/verify", bytes.NewReader(vb))
	req3.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w3.Code, w3.Body.String())
	}

	var result map[string]any
	_ = json.Unmarshal(w3.Body.Bytes(), &result)
	if result["valid"] != true {
		t.Errorf("expected valid=true, got %v", result["valid"])
	}
}

func TestVerifyIntegrityNotFound(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	body := map[string]any{"snapshot_id": "nonexistent"}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/audit/snapshots/verify", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGenerateReport(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	// Create some logs
	for i := 0; i < 5; i++ {
		body := map[string]any{
			"operation":      "mask",
			"datasource":     "ds_yibao",
			"status":         "success",
			"security_level": "L3",
			"duration_ms":    50,
		}
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
	}

	// Generate report
	reportBody := map[string]any{"period": "24h"}
	rb, _ := json.Marshal(reportBody)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/audit/report", bytes.NewReader(rb))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var report map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &report)
	if report["total_operations"].(float64) != 5 {
		t.Errorf("expected 5 total ops, got %v", report["total_operations"])
	}
	if report["success_rate"].(float64) != 100 {
		t.Errorf("expected 100%% success rate, got %v", report["success_rate"])
	}
	recs := report["recommendations"].([]any)
	if len(recs) == 0 {
		t.Error("expected at least 1 recommendation")
	}
}

func TestListLogsWithFilter(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	// Create logs with different operations
	for _, op := range []string{"mask", "k_anon", "dp"} {
		body := map[string]any{
			"operation":      op,
			"datasource":     "ds_yibao",
			"status":         "success",
			"security_level": "L3",
		}
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
	}

	// Filter by operation
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/audit/logs?operation=mask", nil)
	router.ServeHTTP(w, req)

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["total"].(float64) != 1 {
		t.Errorf("expected 1 mask log, got %v", resp["total"])
	}
}

func TestComputeIntegrityHash(t *testing.T) {
	ts := time.Now()
	hash1 := store.ComputeAuditIntegrityHash("log-1", "prev-1", ts, "field_mask", "abc", "def", "admin", "L3", "{}")
	hash2 := store.ComputeAuditIntegrityHash("log-1", "prev-1", ts, "field_mask", "abc", "def", "admin", "L3", "{}")
	hash3 := store.ComputeAuditIntegrityHash("log-2", "prev-1", ts, "field_mask", "abc", "def", "admin", "L3", "{}")

	if hash1 != hash2 {
		t.Error("same inputs should produce same hash")
	}
	if hash1 == hash3 {
		t.Error("different inputs should produce different hash")
	}
	if len(hash1) != 64 { // SM3 hex = 64 chars
		t.Errorf("expected 64-char hex hash, got %d chars", len(hash1))
	}
}

func TestVerifyChainEndpoint(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	// Create 2 logs via API
	body1 := `{"operation":"mask","datasource":"ds_yibao","status":"success"}`
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewBufferString(body1))
	req1.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("create log 1 failed: %d", w1.Code)
	}

	body2 := `{"operation":"dp","datasource":"ds_yibao","status":"success"}`
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewBufferString(body2))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("create log 2 failed: %d", w2.Code)
	}

	// Verify chain
	wVerify := httptest.NewRecorder()
	reqVerify, _ := http.NewRequest("POST", "/api/audit/chain/verify", bytes.NewBufferString(`{"limit":10}`))
	reqVerify.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wVerify, reqVerify)
	if wVerify.Code != http.StatusOK {
		t.Fatalf("verify chain failed: %d", wVerify.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(wVerify.Body.Bytes(), &resp)
	if resp["valid"] != true || resp["total_verified"].(float64) < 2 {
		t.Fatalf("expected valid chain, got %+v", resp)
	}
}

// TestCreateLogParametersTooLarge 验证 parameters 超过 1 MB 上限时返回 400。
// P44 fix: parameters size limit.
func TestCreateLogParametersTooLarge(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)

	// 构造一个超过 1 MB 的 parameters 对象
	bigParams := make(map[string]any)
	bigValue := strings.Repeat("x", 1024*1024+100) // > 1 MB
	bigParams["data"] = bigValue

	body := map[string]any{
		"operation":  "mask",
		"status":     "success",
		"parameters": bigParams,
	}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for parameters > 1 MB, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEnvelopeEncryptionOfSnapshots(t *testing.T) {
	s := newTestServer()
	s.cfg.EncryptionKey = "test-secret-key-12345"
	router := newTestRouter(s)

	body := map[string]any{
		"operation":     "mask",
		"datasource":    "ds_yibao",
		"status":        "success",
		"input_sample":  "secret_input_sample",
		"output_sample": "secret_output_sample",
	}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create log failed: %d", w.Code)
	}

	// Verify the snapshot in storage is encrypted
	snaps, total, err := s.audit.ListSnapshots(10, 0)
	if err != nil || total == 0 {
		t.Fatalf("list snapshots from store error: %v", err)
	}
	if snaps[0].InputSample == "secret_input_sample" {
		t.Fatal("expected stored sample to be encrypted, but found cleartext")
	}

	// Verify HTTP API decrypts transparently
	wList := httptest.NewRecorder()
	reqList, _ := http.NewRequest("GET", "/api/audit/snapshots", nil)
	router.ServeHTTP(wList, reqList)
	if wList.Code != http.StatusOK {
		t.Fatalf("list snapshots API failed: %d", wList.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(wList.Body.Bytes(), &resp)
	apiSnaps := resp["snapshots"].([]any)
	firstSnap := apiSnaps[0].(map[string]any)
	if firstSnap["input_sample"] != "secret_input_sample" {
		t.Errorf("expected API to return decrypted sample, got %v", firstSnap["input_sample"])
	}
}

// TestBufferedAuditStore_Handler_VerifyChain_E2E validates that multiple HTTP POST /api/audit/logs
// requests passing through flusher.BufferedAuditStore produce an unbroken cryptographic hash chain.
func TestBufferedAuditStore_Handler_VerifyChain_E2E(t *testing.T) {
	cfg := &config.Config{
		Host:          "127.0.0.1",
		Port:          0,
		AgentRESTHost: "127.0.0.1",
		AgentRESTPort: 19999,
	}
	memStore := memory.NewAuditStore()
	bufStore := flusher.NewBufferedAuditStore(memStore, flusher.Config{
		BufferSize:    500,
		MaxBatchSize:  10,
		FlushInterval: 10 * time.Millisecond,
	}, nil)
	defer bufStore.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mc := metrics.NewCollector("audit-log-e2e-test")
	ag := agent.New(cfg)
	server := New(ag, cfg, bufStore, logger, mc)
	router := newTestRouter(server)

	const count = 30
	for i := 0; i < count; i++ {
		body := map[string]any{
			"task_id":        fmt.Sprintf("task-e2e-%03d", i),
			"operation":      "mask",
			"datasource":     "ds_yibao",
			"input_rows":     10,
			"output_rows":    10,
			"algorithm":      "SM3",
			"status":         "success",
			"security_level": "L2",
			"user":           "operator",
		}
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/audit/logs", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("POST /api/audit/logs failed at %d: %d", i, w.Code)
		}
	}

	// Verify chain via HTTP endpoint GET /api/audit/chain/verify
	wVerify := httptest.NewRecorder()
	reqVerify, _ := http.NewRequest("GET", "/api/audit/chain/verify?limit=100", nil)
	router.ServeHTTP(wVerify, reqVerify)

	if wVerify.Code != http.StatusOK {
		t.Fatalf("GET /api/audit/chain/verify returned status %d", wVerify.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(wVerify.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal verify response failed: %v", err)
	}

	if valid, ok := resp["valid"].(bool); !ok || !valid {
		t.Fatalf("expected chain to be valid, got response: %+v", resp)
	}

	if total, ok := resp["total_verified"].(float64); !ok || int(total) != count {
		t.Fatalf("expected total_verified=%d, got %v", count, resp["total_verified"])
	}
}

