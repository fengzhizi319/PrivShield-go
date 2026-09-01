package audit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pkgagent "github.com/fengzhizi319/PrivShield/pkg/agent"
	"github.com/fengzhizi319/PrivShield/pkg/store"

	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/config"
)

func init() {
	// 存证客户端在成功路径上会打 Info 级日志，测试中静默以免污染输出。
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// capturedRequest is one evidence submission observed by the stub audit-log server.
// capturedRequest 记录桩存证服务观测到的一次提交（请求头 + 解码后的请求体）。
type capturedRequest struct {
	header         http.Header
	body           map[string]any
	rawBody        []byte
	method         string
	path           string
	authorization  string
	requestID      string
	idempotencyKey string
}

// evidenceStub is a programmable stand-in for audit-log's POST /api/audit/logs endpoint.
// evidenceStub 是 audit-log 建单端点（POST /api/audit/logs）的可编程替身：
// 记录全部请求，并可切换为返回指定的状态码/响应体，用于验证 fail-closed 语义。
type evidenceStub struct {
	t    *testing.T
	srv  *httptest.Server
	want int // 期望返回的 HTTP 状态码（默认 201 Created）

	mu      sync.Mutex
	request []capturedRequest
	body    string
}

func newEvidenceStub(t *testing.T) *evidenceStub {
	t.Helper()
	stub := &evidenceStub{t: t, want: http.StatusCreated}
	stub.srv = httptest.NewServer(http.HandlerFunc(stub.handle))
	t.Cleanup(stub.srv.Close)
	return stub
}

func (s *evidenceStub) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	rec := capturedRequest{
		header:         r.Header.Clone(),
		method:         r.Method,
		path:           r.URL.Path,
		authorization:  r.Header.Get("Authorization"),
		requestID:      r.Header.Get("X-Request-ID"),
		idempotencyKey: r.Header.Get("X-Idempotency-Key"),
		rawBody:        raw,
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		s.t.Errorf("evidence request body is not valid JSON: %v (raw=%s)", err, raw)
	}
	rec.body = decoded

	s.mu.Lock()
	s.request = append(s.request, rec)
	status, body := s.want, s.body
	s.mu.Unlock()

	if status != http.StatusCreated {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body == "" {
			body = `{"code":"INVALID_ARGUMENT","message":"evidence stub rejected the record"}`
		}
		_, _ = io.WriteString(w, body)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, `{"id":"audit-stub-1","snapshot_id":"snap-stub-1","integrity_hash":"ih-1","prev_hash":"ph-0","via":"audit-log"}`)
}

func (s *evidenceStub) url() string { return s.srv.URL }

func (s *evidenceStub) rejectWith(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.want = status
	s.body = body
}

func (s *evidenceStub) calls() []capturedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]capturedRequest, len(s.request))
	copy(out, s.request)
	return out
}

// clientFor builds an evidence client pointing at the given endpoints.
// clientFor 构造仅指向指定存证端点的客户端：重试次数固定为 1，保证单测耗时可控。
func clientFor(urls []string, apiKey string, maxRetries int) *Client {
	return New(&config.Config{
		AuditLogBaseURLs:   urls,
		AuditLogAPIKey:     apiKey,
		AuditLogTimeout:    2,
		AuditLogMaxRetries: maxRetries,
	}, nil)
}

// sampleFlow is the canonical outbound flow exercised by most cases.
// sampleFlow 为多数用例复用的标准出域事实（医保数据源 + mask 算子 + L4 定级）。
func sampleFlow() OutboundFlow {
	started := time.Now().Add(-250 * time.Millisecond)
	return OutboundFlow{
		Task: &store.Task{
			ID:           "task-abc-123",
			APICode:      "api1_yibao",
			DatasourceID: "ds_yibao",
			Operation:    "mask",
			Stage:        "audit",
			Status:       "running",
			CreatedAt:    started,
			StartedAt:    &started,
			TraceID:      "trace-xyz",
		},
		Protocol:      "rest",
		SecurityLevel: "L4",
		Input:         []map[string]any{{"name": "张三", "id_card": "110101199001011234"}},
		Output:        []map[string]any{{"name": "张*", "id_card": "1101**********1234"}},
		InputHash:     "sha256:in",
		OutputHash:    "sha256:out",
	}
}

// TestRecordOutboundSuccess asserts the wire contract of an accepted submission:
// required fields present, prev_hash absent, Bearer auth and trace headers set,
// and the audit-log identifiers decoded back to the caller.
// TestRecordOutboundSuccess 校验成功提交的完整契约（P0-6 / G-05 验收点）：
// 必携字段齐全、绝不携带 prev_hash、Bearer 鉴权与链路头正确、返回体标识被解出。
func TestRecordOutboundSuccess(t *testing.T) {
	stub := newEvidenceStub(t)
	client := clientFor([]string{stub.url()}, "evidence-secret", 0)

	result, err := client.RecordOutbound(context.Background(), sampleFlow())
	if err != nil {
		t.Fatalf("RecordOutbound() unexpected error: %v", err)
	}
	if result == nil || result.LogID != "audit-stub-1" || result.SnapshotID != "snap-stub-1" {
		t.Fatalf("RecordOutbound() result mismatch: %+v", result)
	}

	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 evidence POST, got %d", len(calls))
	}
	call := calls[0]
	if call.method != http.MethodPost || call.path != RecordPath {
		t.Errorf("request line mismatch: %s %s, want POST %s", call.method, call.path, RecordPath)
	}
	if call.authorization != "Bearer evidence-secret" {
		t.Errorf("Authorization header = %q, want %q", call.authorization, "Bearer evidence-secret")
	}
	if got := call.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	// 绑定主键与出域指纹：缺一即无法把「这次出域」与「这条存证」对账。
	for field, want := range map[string]string{
		"task_id":        "task-abc-123",
		"api_code":       "api1_yibao",
		"datasource_id":  "ds_yibao",
		"datasource":     "ds_yibao",
		"operation":      "mask",
		"security_level": "L4",
		"status":         "success",
		"user":           EvidenceUser,
		"input_hash":     "sha256:in",
		"output_hash":    "sha256:out",
	} {
		if got := call.body[field]; got != want {
			t.Errorf("field %q = %#v, want %q", field, got, want)
		}
	}

	// 时间戳与行数：audit-log 侧的 operation/status 为 binding:"required"，
	// timestamp 缺失虽不致命，但会让存证失去时序证据价值，必须为 RFC3339。
	ts, ok := call.body["timestamp"].(string)
	if !ok {
		t.Fatalf("timestamp missing or not a string: %#v", call.body["timestamp"])
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("timestamp %q is not RFC3339Nano: %v", ts, err)
	}
	if rows, _ := call.body["input_rows"].(float64); rows != 1 {
		t.Errorf("input_rows = %v, want 1", call.body["input_rows"])
	}
	if rows, _ := call.body["output_rows"].(float64); rows != 1 {
		t.Errorf("output_rows = %v, want 1", call.body["output_rows"])
	}

	// 禁止伪造链头：请求结构中绝不能出现 prev_hash。
	if _, exists := call.body["prev_hash"]; exists {
		t.Error("evidence request must not carry prev_hash (server-assigned chain tail)")
	}

	params, ok := call.body["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters missing or not an object: %#v", call.body["parameters"])
	}
	if params["service"] != EvidenceUser || params["stage"] != "audit" || params["protocol"] != "rest" {
		t.Errorf("parameters provenance mismatch: %#v", params)
	}
	if params["trace_id"] != "trace-xyz" {
		t.Errorf("parameters.trace_id = %#v, want trace-xyz", params["trace_id"])
	}
}

// TestRecordOutboundHeaders asserts trace headers are propagated when present and
// omitted when the context carries nothing, matching the sibling HTTP clients.
// TestRecordOutboundHeaders 校验链路头透传：Context 中存在时注入，缺失时不写空头。
func TestRecordOutboundHeaders(t *testing.T) {
	stub := newEvidenceStub(t)
	client := clientFor([]string{stub.url()}, "", 0)

	ctx := pkgagent.ContextWithIdempotencyKey(pkgagent.ContextWithRequestID(context.Background(), "req-42"), "idem-42")
	if _, err := client.RecordOutbound(ctx, sampleFlow()); err != nil {
		t.Fatalf("RecordOutbound() unexpected error: %v", err)
	}
	call := stub.calls()[0]
	if call.requestID != "req-42" || call.header.Get("X-Trace-ID") != "req-42" {
		t.Errorf("trace headers mismatch: X-Request-ID=%q X-Trace-ID=%q", call.requestID, call.header.Get("X-Trace-ID"))
	}
	if call.idempotencyKey != "idem-42" {
		t.Errorf("X-Idempotency-Key = %q, want idem-42", call.idempotencyKey)
	}
	if call.authorization != "" {
		t.Errorf("Authorization must be omitted without an API key, got %q", call.authorization)
	}

	if _, err := client.RecordOutbound(context.Background(), sampleFlow()); err != nil {
		t.Fatalf("RecordOutbound() second call failed: %v", err)
	}
	second := stub.calls()[1]
	if second.requestID != "" || second.idempotencyKey != "" {
		t.Errorf("trace headers must stay absent: request-id=%q idempotency=%q", second.requestID, second.idempotencyKey)
	}
}

// TestRecordOutboundNonSuccess tests every non-2xx answer surfaces as an error,
// with 4xx classified as a contract rejection (never retried) and 5xx as unavailable.
// TestRecordOutboundNonSuccess 校验非 2xx 一律返回错误（P0-6 红线一）：
// 4xx 归为契约拒绝且不重试，5xx 归为服务不可用并按重试次数耗尽后上抛。
func TestRecordOutboundNonSuccess(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantIs    error
		wantCalls int
		wantCode  string
	}{
		{name: "400 invalid argument", status: http.StatusBadRequest, body: `{"code":"INVALID_ARGUMENT","message":"invalid request: operation must be one of"}`, wantIs: ErrRejected, wantCalls: 1, wantCode: "INVALID_ARGUMENT"},
		{name: "401 unauthorized", status: http.StatusUnauthorized, wantIs: ErrRejected, wantCalls: 1, wantCode: "UNAUTHORIZED"},
		{name: "403 forbidden", status: http.StatusForbidden, wantIs: ErrRejected, wantCalls: 1},
		{name: "409 reserved datasource", status: http.StatusConflict, wantIs: ErrRejected, wantCalls: 1},
		{name: "500 server error retried", status: http.StatusInternalServerError, wantIs: ErrUnavailable, wantCalls: 2},
		{name: "503 unavailable retried", status: http.StatusServiceUnavailable, wantIs: ErrUnavailable, wantCalls: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := newEvidenceStub(t)
			stub.rejectWith(tc.status, tc.body)
			client := clientFor([]string{stub.url()}, "k", 1)

			result, err := client.RecordOutbound(context.Background(), sampleFlow())
			if err == nil {
				t.Fatalf("RecordOutbound() with stub status %d must fail (fail-closed), got result %+v", tc.status, result)
			}
			if result != nil {
				t.Errorf("result must be nil on failure, got %+v", result)
			}
			if !errors.Is(err, tc.wantIs) {
				t.Errorf("errors.Is(err, %v) = false, got: %v", tc.wantIs, err)
			}

			var rej *RejectionError
			if !errors.As(err, &rej) {
				t.Fatalf("error must expose the HTTP status via *RejectionError, got %T: %v", err, err)
			}
			if rej.Status != tc.status {
				t.Errorf("RejectionError.Status = %d, want %d", rej.Status, tc.status)
			}
			if rej.DatasourceID != "ds_yibao" || rej.Op != "mask" {
				t.Errorf("RejectionError context mismatch: datasource=%q op=%q", rej.DatasourceID, rej.Op)
			}
			if tc.wantCode != "" && rej.Code != tc.wantCode {
				t.Errorf("RejectionError.Code = %q, want %q", rej.Code, tc.wantCode)
			}
			if tc.body != "" && rej.Message == "" {
				t.Error("RejectionError.Message must carry the audit-log error envelope message")
			}

			if got := len(stub.calls()); got != tc.wantCalls {
				t.Errorf("submission attempts = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}

// TestRecordOutboundUnreachable verifies a dead endpoint is reported as unavailable.
// TestRecordOutboundUnreachable 校验存证节点不可达（连接失败）时归入 ErrUnavailable，
// 且重试次数用尽后必然返回错误，绝不返回 nil。
func TestRecordOutboundUnreachable(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // 立即关闭端口，制造稳定的连接失败

	client := clientFor([]string{deadURL}, "", 1)
	_, err := client.RecordOutbound(context.Background(), sampleFlow())
	if err == nil {
		t.Fatal("RecordOutbound() against an unreachable endpoint must fail")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("errors.Is(err, ErrUnavailable) = false, got: %v", err)
	}
}

// TestRecordOutboundContextCancelled verifies shutdown cancellation propagates.
// TestRecordOutboundContextCancelled 校验停机取消信号能及时中断存证重试等待。
func TestRecordOutboundContextCancelled(t *testing.T) {
	stub := newEvidenceStub(t)
	stub.rejectWith(http.StatusServiceUnavailable, "")
	client := clientFor([]string{stub.url()}, "", 5)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.RecordOutbound(ctx, sampleFlow()); err == nil {
		t.Fatal("cancelled context must not yield a successful evidence write")
	}
}

// TestRecordOutboundNotConfigured pins the "evidence link absent" semantics: the client
// reports ErrNotConfigured so the pipeline fails the task instead of silently completing.
// TestRecordOutboundNotConfigured 固定「存证链路未配置」的行为（P0-6 fail-closed）：
// 一律返回 ErrNotConfigured，由流水线 audit 阶段判任务失败；绝不允许 nil 错误静默通过。
func TestRecordOutboundNotConfigured(t *testing.T) {
	tests := []struct {
		name   string
		client *Client
	}{
		{name: "nil client", client: nil},
		{name: "nil config", client: New(nil, nil)},
		{name: "no endpoints", client: New(&config.Config{Host: "127.0.0.1"}, nil)},
		{name: "blank endpoints only", client: clientFor([]string{"", "   "}, "", 0)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := RecordOutboundEvidence(context.Background(), tc.client, sampleFlow())
			if !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("expected ErrNotConfigured, got %v (err=%v)", result, err)
			}
			if result != nil {
				t.Errorf("result must be nil when unconfigured, got %+v", result)
			}
			if tc.client.Configured() {
				t.Error("Configured() must be false when no usable endpoint exists")
			}
			class, retryable := FailureClass(err)
			if class != "evidence_unconfigured" || retryable {
				t.Errorf("FailureClass() = (%q, %v), want (\"evidence_unconfigured\", false)", class, retryable)
			}
		})
	}

	t.Run("configured client reports endpoint", func(t *testing.T) {
		stub := newEvidenceStub(t)
		client := clientFor([]string{stub.url() + "/"}, "", 0)
		if !client.Configured() {
			t.Fatal("Configured() must be true with an endpoint")
		}
		if client.Endpoint() != stub.url() {
			t.Errorf("Endpoint() = %q, want trailing slash trimmed %q", client.Endpoint(), stub.url())
		}
	})
}

// TestBuildRecordContract checks the normalization rules that keep audit-log from
// rejecting (or silently mis-recording) an evidence row.
// TestBuildRecordContract 校验存证记录归一化规则：算子/状态/级别白名单映射、
// 数据源回退顺序、指纹兜底，以及缺上下文时的拒绝构造。
func TestBuildRecordContract(t *testing.T) {
	tests := []struct {
		name        string
		flow        OutboundFlow
		wantErr     error
		wantOp      string
		wantStatus  string
		wantLevel   string
		wantDS      string
		wantAPICode string
		wantHubOp   string
		wantHashIn  string
	}{
		{
			name:        "mask stays mask",
			flow:        sampleFlow(),
			wantOp:      "mask",
			wantStatus:  "success",
			wantLevel:   "L4",
			wantDS:      "ds_yibao",
			wantAPICode: "api1_yibao",
			wantHubOp:   "mask",
			wantHashIn:  "sha256:in",
		},
		{
			name: "k_anon and dp are whitelisted",
			flow: OutboundFlow{Task: &store.Task{ID: "t1", DatasourceID: "ds_kangyang", APICode: "api2_kangyang", Operation: "k_anon"},
				Input: []any{map[string]any{"a": 1}}},
			wantOp: "k_anon", wantStatus: "success", wantDS: "ds_kangyang", wantAPICode: "api2_kangyang", wantHubOp: "k_anon",
		},
		{
			name: "passthrough none maps to classify but keeps the truth",
			flow: OutboundFlow{Task: &store.Task{ID: "t2", DatasourceID: "ds_yibao", Operation: "none"},
				Input: []any{map[string]any{"a": 1}}},
			wantOp: "classify", wantStatus: "success", wantDS: "ds_yibao", wantAPICode: "api1_yibao", wantHubOp: "none",
		},
		{
			name: "unknown operation is never sent verbatim",
			flow: OutboundFlow{Task: &store.Task{ID: "t3", DatasourceID: "ds_yibao", Operation: "drop-table"},
				Input: []any{map[string]any{"a": 1}}},
			wantOp: "classify", wantStatus: "success", wantHubOp: "drop-table",
		},
		{
			name: "failed flow records failed status and error",
			flow: OutboundFlow{Task: &store.Task{ID: "t4", DatasourceID: "ds_yibao", Operation: "mask"},
				Input: []any{map[string]any{"a": 1}}, Error: "engine timeout"},
			wantOp: "mask", wantStatus: "failed", wantHubOp: "mask",
		},
		{
			name: "alias source is canonicalized",
			flow: OutboundFlow{Task: &store.Task{ID: "t5", Source: "yibao", Operation: "mask"},
				Input: []any{map[string]any{"a": 1}}},
			wantOp: "mask", wantStatus: "success", wantDS: "ds_yibao", wantAPICode: "api1_yibao", wantHubOp: "mask",
		},
		{
			name: "api code used as last datasource fallback",
			flow: OutboundFlow{Task: &store.Task{ID: "t6", APICode: "api2_kangyang", Operation: "mask"},
				Input: []any{map[string]any{"a": 1}}},
			wantOp: "mask", wantStatus: "success", wantDS: "ds_kangyang", wantAPICode: "api2_kangyang", wantHubOp: "mask",
		},
		{
			name: "level outside L1-L5 enum is dropped",
			flow: OutboundFlow{Task: &store.Task{ID: "t7", DatasourceID: "ds_yibao", Operation: "mask"},
				SecurityLevel: "L9", Input: []any{map[string]any{"a": 1}}},
			wantOp: "mask", wantStatus: "success", wantLevel: "", wantHubOp: "mask",
		},
		{
			name:    "no datasource at all cannot be bound to a task",
			flow:    OutboundFlow{Task: &store.Task{ID: "t8", Operation: "mask"}, Input: []any{map[string]any{"a": 1}}},
			wantErr: ErrMissingTask,
		},
		{
			name:    "nil task is refused",
			flow:    OutboundFlow{Input: []any{map[string]any{"a": 1}}},
			wantErr: ErrMissingTask,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := buildRecord(tc.flow)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("buildRecord() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildRecord() unexpected error: %v", err)
			}
			if rec.Operation != tc.wantOp {
				t.Errorf("operation = %q, want %q", rec.Operation, tc.wantOp)
			}
			if rec.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", rec.Status, tc.wantStatus)
			}
			if tc.wantLevel != "" && rec.SecurityLevel != tc.wantLevel {
				t.Errorf("security_level = %q, want %q", rec.SecurityLevel, tc.wantLevel)
			}
			if tc.wantLevel == "" && rec.SecurityLevel != "" {
				t.Errorf("security_level = %q, want dropped/empty", rec.SecurityLevel)
			}
			if tc.wantDS != "" && rec.DatasourceID != tc.wantDS {
				t.Errorf("datasource_id = %q, want %q", rec.DatasourceID, tc.wantDS)
			}
			if tc.wantAPICode != "" && rec.APICode != tc.wantAPICode {
				t.Errorf("api_code = %q, want %q", rec.APICode, tc.wantAPICode)
			}
			if rec.DataSource != rec.DatasourceID {
				t.Errorf("datasource (%q) must equal canonical datasource_id (%q)", rec.DataSource, rec.DatasourceID)
			}
			if rec.User != EvidenceUser {
				t.Errorf("user = %q, want %q", rec.User, EvidenceUser)
			}
			if rec.TaskID != tc.flow.Task.ID {
				t.Errorf("task_id = %q, want %q", rec.TaskID, tc.flow.Task.ID)
			}
			if rec.InputHash == "" || rec.OutputHash == "" {
				t.Errorf("fingerprints must never be empty: in=%q out=%q", rec.InputHash, rec.OutputHash)
			}
			if tc.wantHashIn != "" && rec.InputHash != tc.wantHashIn {
				t.Errorf("input_hash = %q, want engine fingerprint %q", rec.InputHash, tc.wantHashIn)
			}
			params, ok := rec.Parameters.(map[string]any)
			if !ok {
				t.Fatalf("parameters must be an object, got %#v", rec.Parameters)
			}
			if params["hub_operation"] != tc.wantHubOp {
				t.Errorf("parameters.hub_operation = %#v, want %q (the real hub operation must stay visible)", params["hub_operation"], tc.wantHubOp)
			}
		})
	}
}

// TestRecordOutboundMissingTaskSentinel checks the client refuses to submit without
// a task context, so no evidence row can exist without a task_id binding.
// TestRecordOutboundMissingTaskSentinel 校验缺任务上下文时客户端直接拒绝提交，
// 杜绝出现「有存证但无 task_id 绑定」或「有出域但无存证」的孤儿记录。
func TestRecordOutboundMissingTaskSentinel(t *testing.T) {
	stub := newEvidenceStub(t)
	client := clientFor([]string{stub.url()}, "", 0)

	_, err := client.RecordOutbound(context.Background(), OutboundFlow{Input: []any{}})
	if !errors.Is(err, ErrMissingTask) {
		t.Fatalf("expected ErrMissingTask, got %v", err)
	}
	if got := len(stub.calls()); got != 0 {
		t.Errorf("no HTTP call must be attempted without a task, got %d", got)
	}
	class, retryable := FailureClass(err)
	if class != "evidence_invalid" || retryable {
		t.Errorf("FailureClass() = (%q, %v), want (\"evidence_invalid\", false)", class, retryable)
	}
}

// TestFailureClass asserts every pipeline site shares one bounded verdict.
// TestFailureClass 校验三条流水线共用的存证错误分类判定（错误枚举有界，便于指标聚合）。
func TestFailureClass(t *testing.T) {
	tests := []struct {
		err           error
		wantClass     string
		wantRetryable bool
	}{
		{err: ErrRejected, wantClass: "evidence_rejected"},
		{err: ErrNotConfigured, wantClass: "evidence_unconfigured"},
		{err: ErrMissingTask, wantClass: "evidence_invalid"},
		{err: ErrUnavailable, wantClass: "evidence_unavailable", wantRetryable: true},
		{err: errors.New("boom"), wantClass: "evidence", wantRetryable: true},
	}
	for _, tc := range tests {
		t.Run(tc.err.Error(), func(t *testing.T) {
			class, retryable := FailureClass(tc.err)
			if class != tc.wantClass || retryable != tc.wantRetryable {
				t.Errorf("FailureClass() = (%q, %v), want (%q, %v)", class, retryable, tc.wantClass, tc.wantRetryable)
			}
		})
	}
}

// TestTLSMaterialFailsClosed checks that unusable client certificate material is not
// silently downgraded to plaintext: every submission must fail.
// TestTLSMaterialFailsClosed 校验存证 TLS 材料不可用时不降级为明文：
// 构造期错误被记住，后续每次提交都必然失败（由流水线判定任务 failed）。
func TestTLSMaterialFailsClosed(t *testing.T) {
	client := New(&config.Config{
		AuditLogBaseURLs:    []string{"https://audit-log:8084"},
		AuditLogTLSEnabled:  true,
		AuditLogTLSCertFile: "/nonexistent/client.crt",
		AuditLogTLSKeyFile:  "/nonexistent/client.key",
		AuditLogMaxRetries:  0,
	}, nil)

	_, err := client.RecordOutbound(context.Background(), sampleFlow())
	if err == nil {
		t.Fatal("submission must fail when configured client TLS material is missing")
	}
	if !strings.Contains(err.Error(), "mTLS") {
		t.Errorf("error must name the TLS setup failure, got: %v", err)
	}
	if client.Configured() {
		t.Error("Configured() must be false when TLS setup failed")
	}
}

// TestMaxSensitivityLevel covers the recursive walk over classification reports that
// arrive as decoded JSON ([]any / map[string]any), the shape the pipeline actually sees.
// TestMaxSensitivityLevel 覆盖分类分级报告递归取最高敏感级别：
// 必须适配 JSON 反序列化后的 []any / map[string]any 实际形态。
func TestMaxSensitivityLevel(t *testing.T) {
	tests := []struct {
		name   string
		report any
		want   string
	}{
		{name: "nil report", report: nil, want: ""},
		{name: "typed slice of maps", report: []map[string]any{{"level": "L2"}, {"level": "L4"}}, want: "L4"},
		{name: "json generic shape", report: []any{map[string]any{"level": "L3"}, map[string]any{"nested": map[string]any{"level": "L5"}}}, want: "L5"},
		{name: "overall_level key", report: map[string]any{"overall_level": "l3"}, want: "L3"},
		{name: "single map record", report: map[string]any{"fields": []any{map[string]any{"level": "L1"}}}, want: "L1"},
		{name: "invalid levels ignored", report: []any{map[string]any{"level": "critical"}, map[string]any{"level": "L6"}}, want: ""},
		{name: "non string level ignored", report: []any{map[string]any{"level": 4}}, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaxSensitivityLevel(tc.report); got != tc.want {
				t.Errorf("MaxSensitivityLevel() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFingerprintAndRowCount checks the local fallback helpers.
// TestFingerprintAndRowCount 校验本地指纹兜底与行数统计辅助函数：
// 同一载荷必须得到稳定（可重放对账）的 SM3 指纹，空载荷不得产生伪指纹。
func TestFingerprintAndRowCount(t *testing.T) {
	payload := []map[string]any{{"name": "张三"}}
	first := Fingerprint(payload)
	if first == "" {
		t.Fatal("Fingerprint() must not be empty for a non-empty payload")
	}
	if second := Fingerprint(payload); second != first {
		t.Errorf("Fingerprint() must be deterministic: %q != %q", second, first)
	}
	if len(first) != 64 {
		t.Errorf("Fingerprint() length = %d, want 64 (SM3 hex)", len(first))
	}
	for _, empty := range []any{nil, "", "   ", "null", "{}", []byte{}} {
		if got := Fingerprint(empty); got != "" {
			t.Errorf("Fingerprint(%#v) = %q, want empty", empty, got)
		}
	}

	tests := []struct {
		name    string
		payload any
		want    int
	}{
		{name: "nil", payload: nil, want: 0},
		{name: "typed records", payload: []map[string]any{{}, {}}, want: 2},
		{name: "generic records", payload: []any{map[string]any{}, map[string]any{}, map[string]any{}}, want: 3},
		{name: "single object", payload: map[string]any{"a": 1}, want: 1},
		{name: "empty object", payload: map[string]any{}, want: 0},
		{name: "json string", payload: `[{"a":1},{"b":2}]`, want: 2},
		{name: "non json string", payload: "hello", want: 0},
		{name: "unknown shape", payload: 42, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RowCount(tc.payload); got != tc.want {
				t.Errorf("RowCount(%#v) = %d, want %d", tc.payload, got, tc.want)
			}
		})
	}
}

// TestEngineFingerprints checks the shared summary extractor used by both pipelines.
// TestEngineFingerprints 校验 REST 与 gRPC 两条流水线共用的引擎指纹提取逻辑。
func TestEngineFingerprints(t *testing.T) {
	tests := []struct {
		name    string
		summary map[string]any
		wantIn  string
		wantOut string
	}{
		{name: "nil summary", summary: nil},
		{name: "both present", summary: map[string]any{"input_hash": " in ", "output_hash": "out"}, wantIn: "in", wantOut: "out"},
		{name: "missing keys", summary: map[string]any{"total_records": 3}},
		{name: "wrong types", summary: map[string]any{"input_hash": 12, "output_hash": json.Number("7")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in, out := EngineFingerprints(tc.summary)
			if in != tc.wantIn || out != tc.wantOut {
				t.Errorf("EngineFingerprints() = (%q, %q), want (%q, %q)", in, out, tc.wantIn, tc.wantOut)
			}
		})
	}
}

// TestMultiReplicaEndpointsRoundRobin checks failover distribution across replicas.
// TestMultiReplicaEndpointsRoundRobin 校验多存证副本间的轮询均衡（副本级容灾）。
func TestMultiReplicaEndpointsRoundRobin(t *testing.T) {
	stubA := newEvidenceStub(t)
	stubB := newEvidenceStub(t)
	client := clientFor([]string{stubA.url(), stubB.url()}, "", 0)

	for i := 0; i < 4; i++ {
		if _, err := client.RecordOutbound(context.Background(), sampleFlow()); err != nil {
			t.Fatalf("submission %d failed: %v", i, err)
		}
	}
	if a, b := len(stubA.calls()), len(stubB.calls()); a != 2 || b != 2 {
		t.Errorf("round-robin distribution mismatch: a=%d b=%d, want 2/2", a, b)
	}
}
