package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/flusher"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/memory"

	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/config"
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
	return New(ag, cfg, nil, d.audit, d.logger, d.mc)
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
	req, _ := http.NewRequest("GET", "/health", nil)
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
	req, _ := http.NewRequest("GET", "/v1/audit/logs", nil)
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
		"input_hash":     "caller_input_hash_abc123",
		"output_hash":    "caller_output_hash_xyz789",
		"input_rows":     1000,
		"output_rows":    1000,
		"duration_ms":    45,
		"user":           "admin",
		"status":         "success",
		"security_level": "L3",
	}
	b, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(b))
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
	req2, _ := http.NewRequest("GET", "/v1/audit/logs", nil)
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
	req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader([]byte("{}")))
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
	req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(body))
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
			"operation":   "mask",
			"datasource":  "ds_yibao",
			"input_hash":  "hash_in_" + strconv.Itoa(int(time.Now().UnixNano())),
			"output_hash": "hash_out_" + strconv.Itoa(int(time.Now().UnixNano())),
			"status":      "success",
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(body))
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
	req, _ := http.NewRequest("GET", "/v1/audit/chain/verify?limit=100", nil)
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
	req, _ := http.NewRequest("GET", "/v1/audit/logs/nonexistent", nil)
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
		"input_hash":     "hash_in_kanon",
		"output_hash":    "hash_out_kanon",
		"input_rows":     5000,
		"output_rows":    5000,
		"duration_ms":    120,
		"user":           "data_scientist",
		"status":         "success",
		"security_level": "L4",
	}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	var createResp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &createResp)
	id := createResp["id"].(string)

	// Get the log
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/audit/logs/"+id, nil)
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
			"input_hash":     "hash_in_" + op,
			"output_hash":    "hash_out_" + op,
			"status":         "success",
			"security_level": "L3",
			"duration_ms":    50,
		}
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
	}

	// Get stats
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/audit/stats", nil)
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
		"datasource":  "ds_yibao",
		"operation":   "mask",
		"algorithm":   "field_mask",
		"input_hash":  "hash_in_snap",
		"output_hash": "hash_out_snap",
		"status":      "success",
	}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	// List snapshots
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/audit/snapshots", nil)
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
		"datasource":  "ds_yibao",
		"operation":   "mask",
		"algorithm":   "field_mask",
		"input_hash":  "hash_in_verify",
		"output_hash": "hash_out_verify",
		"status":      "success",
	}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create log: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// List snapshots to get the actual snapshot ID
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/audit/snapshots", nil)
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
	req3, _ := http.NewRequest("POST", "/v1/audit/snapshots/verify", bytes.NewReader(vb))
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
	req, _ := http.NewRequest("POST", "/v1/audit/snapshots/verify", bytes.NewReader(b))
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
			"input_hash":     "hash_in_report",
			"output_hash":    "hash_out_report",
			"status":         "success",
			"security_level": "L3",
			"duration_ms":    50,
		}
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
	}

	// Generate report
	reportBody := map[string]any{"period": "24h"}
	rb, _ := json.Marshal(reportBody)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/audit/report", bytes.NewReader(rb))
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
			"input_hash":     "hash_in_" + op,
			"output_hash":    "hash_out_" + op,
			"status":         "success",
			"security_level": "L3",
		}
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
	}

	// Filter by operation
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/audit/logs?operation=mask", nil)
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
	body1 := `{"operation":"mask","datasource":"ds_yibao","input_hash":"hash_in_1","output_hash":"hash_out_1","status":"success"}`
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewBufferString(body1))
	req1.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("create log 1 failed: %d", w1.Code)
	}

	body2 := `{"operation":"dp","datasource":"ds_yibao","input_hash":"hash_in_2","output_hash":"hash_out_2","status":"success"}`
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewBufferString(body2))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("create log 2 failed: %d", w2.Code)
	}

	// Verify chain
	wVerify := httptest.NewRecorder()
	reqVerify, _ := http.NewRequest("POST", "/v1/audit/chain/verify", bytes.NewBufferString(`{"limit":10}`))
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
	req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(b))
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
		"input_hash":    "hash_in_env",
		"output_hash":   "hash_out_env",
		"status":        "success",
		"input_sample":  "secret_input_sample",
		"output_sample": "secret_output_sample",
	}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(b))
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
	reqList, _ := http.NewRequest("GET", "/v1/audit/snapshots", nil)
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

// TestBufferedAuditStore_Handler_VerifyChain_E2E validates that multiple HTTP POST /v1/audit/logs
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
	server := New(ag, cfg, nil, bufStore, logger, mc)
	router := newTestRouter(server)

	const count = 30
	for i := 0; i < count; i++ {
		body := map[string]any{
			"task_id":        fmt.Sprintf("task-e2e-%03d", i),
			"operation":      "mask",
			"datasource":     "ds_yibao",
			"input_hash":     fmt.Sprintf("hash_in_e2e_%03d", i),
			"output_hash":    fmt.Sprintf("hash_out_e2e_%03d", i),
			"input_rows":     10,
			"output_rows":    10,
			"algorithm":      "SM3",
			"status":         "success",
			"security_level": "L2",
			"user":           "operator",
		}
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("POST /v1/audit/logs failed at %d: %d", i, w.Code)
		}
	}

	// Verify chain via HTTP endpoint GET /v1/audit/chain/verify
	wVerify := httptest.NewRecorder()
	reqVerify, _ := http.NewRequest("GET", "/v1/audit/chain/verify?limit=100", nil)
	router.ServeHTTP(wVerify, reqVerify)

	if wVerify.Code != http.StatusOK {
		t.Fatalf("GET /v1/audit/chain/verify returned status %d", wVerify.Code)
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

// ─────────────────────────────────────────────────────────────
// P2-4：验真响应 reason 枚举 + legacy_hashed 透出（REST 侧）
// ─────────────────────────────────────────────────────────────

// p24TestChainKey 模拟局方托管的存证 HMAC 密钥（AUDIT_LOG_HASH_KEY）。
const p24TestChainKey = "P2-4-局方托管存证密钥-32bytes-min-len"

// bogusIntegrityHash64 是一条长度合法、但不可能命中任何候选前映像的伪造摘要。
const bogusIntegrityHash64 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

// withAuditChainKey 临时注入进程级存证 HMAC 密钥并在用例结束后还原（密钥为全局状态，必须还原）。
func withAuditChainKey(t *testing.T, key string) {
	t.Helper()
	prev := store.AuditChainKey()
	t.Cleanup(func() { store.SetAuditChainKey(prev) })
	store.SetAuditChainKey(key)
}

// postAuditLog 通过 REST 写入一条审计日志，断言 201 Created。
func postAuditLog(t *testing.T, router *gin.Engine, body map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/audit/logs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /v1/audit/logs failed: %d %s", w.Code, w.Body.String())
	}
}

// doJSON 向指定端点 POST 一个 JSON 请求体并解析响应。
func doJSON(t *testing.T, router *gin.Engine, path string, payload map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST %s returned %d: %s", path, w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal %s response: %v", path, err)
	}
	return resp
}

// numField 读取响应中的数值字段（JSON 数字统一解码为 float64）。
func numField(t *testing.T, resp map[string]any, key string) int {
	t.Helper()
	v, ok := resp[key].(float64)
	if !ok {
		t.Fatalf("response field %q is missing or not a number: %+v", key, resp)
	}
	return int(v)
}

// TestVerifyChainEndpoint_ExposesReasonAndLegacyHashed 覆盖 P2-4 缺口 (a)(b) 的 REST 侧：
// `/v1/audit/chain/verify` 响应必须携带机器可读 `reason` 与 `legacy_hashed`；
// 注入存证密钥后，密钥化之前写入的历史存证必须报 `legacy_hashed`（`valid` 仍为 true），
// 而不是被误报为篡改。
func TestVerifyChainEndpoint_ExposesReasonAndLegacyHashed(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)
	withAuditChainKey(t, "") // 迁移前口径：无密钥 SM3 写入

	for _, in := range []string{"hash_in_p24_a", "hash_in_p24_b"} {
		postAuditLog(t, router, map[string]any{
			"operation":   "mask",
			"datasource":  "ds_yibao",
			"input_hash":  in,
			"output_hash": "hash_out_p24",
			"status":      "success",
		})
	}

	res := doJSON(t, router, "/v1/audit/chain/verify", map[string]any{"limit": 10})
	if res["valid"] != true {
		t.Fatalf("clean chain must be valid: %+v", res)
	}
	if res["reason"] != store.ChainReasonOK {
		t.Fatalf("reason = %v, want %q", res["reason"], store.ChainReasonOK)
	}
	if n := numField(t, res, "legacy_hashed"); n != 0 {
		t.Fatalf("legacy_hashed = %d, want 0 before keying", n)
	}

	// 上线密钥化口径后回验：证据真实、仅待重签。
	withAuditChainKey(t, p24TestChainKey)

	res = doJSON(t, router, "/v1/audit/chain/verify", map[string]any{"limit": 10})
	if res["valid"] != true {
		t.Fatalf("legacy evidence must stay valid, got %+v", res)
	}
	if res["reason"] != store.ChainReasonLegacyHashed {
		t.Fatalf("reason = %v, want %q", res["reason"], store.ChainReasonLegacyHashed)
	}
	if n := numField(t, res, "legacy_hashed"); n != 2 {
		t.Fatalf("legacy_hashed = %d, want 2", n)
	}

	// 密钥化之后新写入的记录属规范口径，待重签数不再增长。
	postAuditLog(t, router, map[string]any{
		"operation":   "mask",
		"datasource":  "ds_yibao",
		"input_hash":  "hash_in_p24_c",
		"output_hash": "hash_out_p24",
		"status":      "success",
	})
	res = doJSON(t, router, "/v1/audit/chain/verify", map[string]any{"limit": 10})
	if res["valid"] != true || res["reason"] != store.ChainReasonLegacyHashed {
		t.Fatalf("mixed chain must stay valid and report legacy pending: %+v", res)
	}
	if n := numField(t, res, "legacy_hashed"); n != 2 {
		t.Fatalf("legacy_hashed = %d, want 2 (the keyed record must not be counted)", n)
	}
}

// TestVerifyIntegrityEndpoint_ExposesReasonAndLegacyHashed 覆盖快照验真端点（架构文档指出的
// 「仅返回 6 字段」处）：响应须含 `reason`/`legacy_hashed`，且密钥化之前写入的快照在注入密钥后
// 判为 `legacy_hashed` 而非篡改。
func TestVerifyIntegrityEndpoint_ExposesReasonAndLegacyHashed(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)
	withAuditChainKey(t, "")

	postAuditLog(t, router, map[string]any{
		"operation":     "mask",
		"datasource":    "ds_yibao",
		"input_hash":    "hash_in_snap_p24",
		"output_hash":   "hash_out_snap_p24",
		"status":        "success",
		"input_sample":  "13800138000",
		"output_sample": "138****8000",
	})
	snaps, total, err := s.audit.ListSnapshots(10, 0)
	if err != nil || total == 0 {
		t.Fatalf("list snapshots: total=%d err=%v", total, err)
	}
	snapID := snaps[0].ID

	res := doJSON(t, router, "/v1/audit/snapshots/verify", map[string]any{"snapshot_id": snapID})
	if res["valid"] != true || res["reason"] != store.ChainReasonOK {
		t.Fatalf("un-keyed snapshot must verify as ok, got %+v", res)
	}
	if res["legacy_hashed"] != false {
		t.Fatalf("legacy_hashed = %v, want false", res["legacy_hashed"])
	}

	withAuditChainKey(t, p24TestChainKey)

	res = doJSON(t, router, "/v1/audit/snapshots/verify", map[string]any{"snapshot_id": snapID})
	if res["valid"] != true {
		t.Fatalf("pre-keying snapshot evidence must stay valid, got %+v", res)
	}
	if res["reason"] != store.ChainReasonLegacyHashed || res["legacy_hashed"] != true {
		t.Fatalf("reason = %v legacy_hashed = %v, want %q and true",
			res["reason"], res["legacy_hashed"], store.ChainReasonLegacyHashed)
	}
}

// TestVerifyIntegrityEndpoint_ReportsHashMismatchForBrokenSnapshot 覆盖缺口 (a) 的失败侧：
// 快照哈希对不上任何候选口径时报 `hash_mismatch` 且 `valid=false`（fail-closed 未弱化）。
func TestVerifyIntegrityEndpoint_ReportsHashMismatchForBrokenSnapshot(t *testing.T) {
	s := newTestServer()
	router := newTestRouter(s)
	withAuditChainKey(t, "")

	now := time.Now().UTC()
	if err := s.audit.SaveLog(&store.AuditLog{
		ID: "p24-broken-log", Timestamp: now, Operation: "mask", Algorithm: "SM3",
		InputHash: "in", OutputHash: "out", User: "op", Status: "success", SecurityLevel: "L2",
	}); err != nil {
		t.Fatalf("save parent log: %v", err)
	}
	// 直接落一条哈希与内容完全不匹配的快照，模拟密文样本在库内被整体替换。
	if err := s.audit.SaveSnapshot(&store.SnapshotRecord{
		ID: "p24-broken-snap", AuditLogID: "p24-broken-log", Timestamp: now,
		InputSample: "evil-in", OutputSample: "evil-out", Algorithm: "SM3",
		IntegrityHash: bogusIntegrityHash64,
	}); err != nil {
		t.Fatalf("save corrupted snapshot: %v", err)
	}

	res := doJSON(t, router, "/v1/audit/snapshots/verify", map[string]any{"snapshot_id": "p24-broken-snap"})
	if res["valid"] != false {
		t.Fatalf("a broken snapshot must stay invalid, got %+v", res)
	}
	if res["reason"] != store.ChainReasonHashMismatch {
		t.Fatalf("reason = %v, want %q", res["reason"], store.ChainReasonHashMismatch)
	}
	if res["legacy_hashed"] != false {
		t.Fatalf("a genuine mismatch must never be labelled legacy_hashed: %+v", res)
	}
}
