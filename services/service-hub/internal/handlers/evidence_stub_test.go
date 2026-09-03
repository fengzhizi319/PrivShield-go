package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/validation"
)

// ─────────────────────────────────────────────────────────────
// audit-log 存证桩服务（P0-6 出域 ↔ 存证绑定）
// ─────────────────────────────────────────────────────────────
//
// evidenceStub 复刻 audit-log 真实建单端点 POST /api/audit/logs 的契约：
//   - operation / status 为 binding:"required"，非白名单枚举一律 400；
//   - 调用方携带非空 prev_hash 一律 400（链头只能由存储层指派）；
//   - 成功返回 201 + {id, snapshot_id, integrity_hash, prev_hash, via}。
//
// 因此流水线写入的存证一旦偏离真实契约（枚举、字段名、必填项），
// 这里会立刻以 4xx 暴露，并被 fail-closed 分支转成任务失败——正是期望的可观测行为。
type evidenceStub struct {
	server *httptest.Server

	mu       sync.Mutex
	records  []map[string]any
	authors  []string
	traceIDs []string
	// status/body 用于编程化注入失败（模拟 audit-log 拒绝或不可用）。
	status int
	body   string
	calls  int
}

var (
	evidenceRegistryMu sync.Mutex
	evidenceRegistry   = map[string]*evidenceStub{}
)

// startEvidenceStub launches a contract-checking audit-log stub bound to this test.
// startEvidenceStub 启动一个契约校验版存证桩服务，并在测试结束时自动回收。
func startEvidenceStub(t *testing.T) *evidenceStub {
	t.Helper()
	stub := &evidenceStub{status: http.StatusCreated}
	stub.server = httptest.NewServer(http.HandlerFunc(stub.handle))
	t.Cleanup(stub.close)

	evidenceRegistryMu.Lock()
	evidenceRegistry[stub.server.URL] = stub
	evidenceRegistryMu.Unlock()
	return stub
}

func (s *evidenceStub) close() {
	s.server.Close()
	evidenceRegistryMu.Lock()
	delete(evidenceRegistry, s.server.URL)
	evidenceRegistryMu.Unlock()
}

func (s *evidenceStub) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)

	s.mu.Lock()
	s.calls++
	status, body := s.status, s.body
	s.mu.Unlock()

	if r.Method == http.MethodGet && (r.URL.Path == "/api/health" || r.URL.Path == "/health") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "via": "audit-log"})
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/audit/logs" {
		w.Header().Set("Content-Type", "application/json")
		s.mu.Lock()
		recs := s.records
		s.mu.Unlock()
		if recs == nil {
			recs = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total": len(recs), "logs": recs, "via": "audit-log"})
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/audit/snapshots" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total": 1,
			"snapshots": []map[string]any{
				{
					"id":             "snap-test-1",
					"integrity_hash": "hash123",
					"timestamp":      time.Now().UTC(),
				},
			},
			"via": "audit-log",
		})
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/audit/snapshots/verify" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"snapshot_id": "snap-test-1",
			"valid":       true,
			"actual":      "hash123",
			"expected":    "hash123",
			"via":         "audit-log",
		})
		return
	}

	if r.Method != http.MethodPost || r.URL.Path != "/api/audit/logs" {
		writeEnvelope(w, http.StatusNotFound, "NOT_FOUND", "evidence stub only serves POST /api/audit/logs")
		return
	}

	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		writeEnvelope(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request: "+err.Error())
		return
	}

	operation, _ := record["operation"].(string)
	logStatus, _ := record["status"].(string)
	level, _ := record["security_level"].(string)
	authorization := r.Header.Get("Authorization")

	s.mu.Lock()
	s.records = append(s.records, record)
	s.authors = append(s.authors, authorization)
	s.traceIDs = append(s.traceIDs, r.Header.Get("X-Request-ID"))
	s.mu.Unlock()

	// 与 audit-log CreateLog 一致的拒绝规则（任何一条不满足都会让存证链路断裂）。
	if prev, _ := record["prev_hash"].(string); prev != "" {
		writeEnvelope(w, http.StatusBadRequest, "INVALID_ARGUMENT", "prev_hash is assigned by the audit store and must be omitted")
		return
	}
	if operation == "" || logStatus == "" {
		writeEnvelope(w, http.StatusBadRequest, "INVALID_ARGUMENT", "operation and status are required")
		return
	}
	if !oneOf(validation.AuditOperations, operation) {
		writeEnvelope(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid operation "+operation)
		return
	}
	if !oneOf(validation.AuditStatuses, logStatus) {
		writeEnvelope(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid status "+logStatus)
		return
	}
	if level != "" && !oneOf(validation.SensitivityLevels, level) {
		writeEnvelope(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid security_level "+level)
		return
	}
	if _, ok := record["task_id"].(string); !ok {
		writeEnvelope(w, http.StatusBadRequest, "INVALID_ARGUMENT", "task_id is required")
		return
	}

	// 注入的故障优先于成功响应（模拟 4xx 拒绝 / 5xx 不可用）。
	if status != http.StatusCreated {
		if body == "" {
			body = `{"code":"INTERNAL_ERROR","message":"evidence stub is simulating a failure"}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, `{"id":"audit-e2e-1","snapshot_id":"snap-e2e-1","integrity_hash":"integrity-e2e-1","prev_hash":"genesis","via":"audit-log"}`)
}

// failWith programs the stub to answer every submission with the given status.
// failWith 让桩服务对后续所有提交返回指定状态码（body 为空时使用统一错误信封）。
func (s *evidenceStub) failWith(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
	s.body = body
}

func (s *evidenceStub) submissions() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, len(s.records))
	copy(out, s.records)
	return out
}

func (s *evidenceStub) authorizations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.authors))
	copy(out, s.authors)
	return out
}

func (s *evidenceStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// evidenceStubOf resolves the stub a Server's evidence client points at.
// evidenceStubOf 依据流水线实例中存证客户端的端点地址反查其桩服务，
// 使各测试无需改变 helper 签名即可断言真实提交的存证内容。
func evidenceStubOf(s *Server) *evidenceStub {
	if s == nil || s.audit == nil {
		return nil
	}
	endpoint := s.audit.Endpoint()
	evidenceRegistryMu.Lock()
	defer evidenceRegistryMu.Unlock()
	return evidenceRegistry[endpoint]
}

func writeEnvelope(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": message})
}

// oneOf reports whether v appears in the given whitelist slice.
// oneOf 判断取值是否命中白名单（复刻 audit-log 侧 validation.AllowedValues 的判定）。
func oneOf(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
