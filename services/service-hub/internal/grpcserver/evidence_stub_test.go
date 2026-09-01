package grpcserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// ─────────────────────────────────────────────────────────────
// audit-log 存证桩服务（P0-6：gRPC / 租约工作器的 ⑥ 存证阶段）
// ─────────────────────────────────────────────────────────────
//
// evidenceStub 模拟 audit-log 的建单端点 POST /api/audit/logs：
// 默认 201 受理并回写标识，可编程切换为 4xx/5xx 以验证 fail-closed 语义，
// 并完整记录每一次提交的请求体，供断言 task_id / datasource_id / 指纹绑定。
type evidenceStub struct {
	t      *testing.T
	server *httptest.Server

	mu      sync.Mutex
	records []map[string]any
	calls   int
	status  int
	deny    bool // true 时按 audit-log 契约拒绝（如携带 prev_hash）
}

var (
	evidenceRegistryMu sync.Mutex
	evidenceRegistry   = map[string]*evidenceStub{}
)

func startEvidenceStub(t *testing.T) *evidenceStub {
	t.Helper()
	stub := &evidenceStub{t: t, status: http.StatusCreated}
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

func (s *evidenceStub) url() string { return s.server.URL }

func (s *evidenceStub) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		s.t.Errorf("evidence payload is not valid JSON: %v", err)
	}

	s.mu.Lock()
	s.calls++
	if record != nil {
		s.records = append(s.records, record)
	}
	status, deny := s.status, s.deny
	s.mu.Unlock()

	if r.Method != http.MethodPost || r.URL.Path != "/api/audit/logs" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if status != http.StatusCreated {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"code":"INTERNAL_ERROR","message":"evidence stub is simulating a failure"}`)
		return
	}
	// 链头由存储层指派：客户端一旦携带非空 prev_hash，真实 audit-log 必定 400。
	if prev, _ := record["prev_hash"].(string); deny && prev != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":"INVALID_ARGUMENT","message":"prev_hash must be omitted"}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, `{"id":"audit-grpc-1","snapshot_id":"snap-grpc-1","integrity_hash":"integrity-grpc-1","prev_hash":"genesis","via":"audit-log"}`)
}

// failWith programs every subsequent submission to be answered with the given status.
// failWith 让桩服务对后续所有提交返回指定状态码（用于验证 4xx 拒绝与 5xx 不可用）。
func (s *evidenceStub) failWith(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *evidenceStub) submissions() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, len(s.records))
	copy(out, s.records)
	return out
}

func (s *evidenceStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// evidenceStubOf resolves the stub a server's evidence client points at.
// evidenceStubOf 依据实例中存证客户端的端点地址反查其桩服务，
// 使测试无需扩展 New/setupTestGRPCServer 的返回值即可断言存证内容。
func evidenceStubOf(s *GRPCServer) *evidenceStub {
	if s == nil || s.audit == nil {
		return nil
	}
	endpoint := s.audit.Endpoint()
	evidenceRegistryMu.Lock()
	defer evidenceRegistryMu.Unlock()
	return evidenceRegistry[endpoint]
}
