package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/memory"

	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/datasource"
)

func init() {
	// 设置 Gin 为测试模式，避免打印冗余调试路由日志
	gin.SetMode(gin.TestMode)
}

// testDeps bundles shared test dependencies (store, logger, metrics).
// testDeps 聚合测试所需的通用基础依赖：内存任务仓库、日志记录器与指标收集器。
type testDeps struct {
	tasks  *memory.TaskStore
	logger *slog.Logger
	mc     *metrics.Collector
}

// newTestDeps creates a new instance of testDeps.
func newTestDeps() *testDeps {
	return &testDeps{
		tasks:  memory.NewTaskStore(),
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		mc:     metrics.NewCollector("service-hub-test"),
	}
}

type failingUpdateTaskStore struct {
	*memory.TaskStore
	updateCalls int
}

func (s *failingUpdateTaskStore) Update(task *store.Task) error {
	s.updateCalls++
	return errors.New("simulated task state persistence failure")
}

// newTestServer creates a Server with a mock upstream (httptest server).
// newTestServer 启动一个 Mock Upstream Agent HTTP 服务器，并返回初始化的 Server 实例与 Mock 服务。
func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()

	// 构造 Mock Upstream Agent 路由处理器
	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "namespace": "default"})
		case "/v1/dynclassification/classify":
			json.NewEncoder(w).Encode(map[string]any{
				"level":    "L3",
				"fields":   []string{"name", "id_card"},
				"category": "PII",
			})
		case "/v1/privacy/mask":
			json.NewEncoder(w).Encode(map[string]any{
				"masked_value": "张*",
				"field_name":   "name",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"detail": "not found"})
		}
	}))

	cfg := &config.Config{
		Host:            "127.0.0.1",
		Port:            0,
		AgentRESTHost:   "127.0.0.1",
		AgentRESTPort:   19999, // 设置为不可达端口，用于单元测试快速验证错误分支
		MaxQueueDepth:   100,
		ScheduleTimeout: 5,
		// P0-6：⑥ audit 阶段真实提交存证，桩服务在位任务才可能推进至 done。
		AuditLogBaseURLs: []string{startEvidenceStub(t).server.URL},
	}
	d := newTestDeps()
	ag, err := agent.New(cfg, d.mc)
	if err != nil {
		t.Fatalf("new agent client: %v", err)
	}
	ds := datasource.New(cfg)
	srv := New(ag, ds, cfg, nil, d.tasks, d.logger, d.mc)
	return srv, mockAgent
}

// newSimpleTestServer creates a standalone test Server with in-memory store and mock config.
// newSimpleTestServer 快速创建无外部依赖的单测用 Server 实例（附带 audit-log 存证桩服务，
// 使 6 阶段流水线的 ⑥ 存证阶段可以真实提交成功）。
func newSimpleTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{
		Host:             "127.0.0.1",
		Port:             0,
		AgentRESTHost:    "127.0.0.1",
		AgentRESTPort:    19999, // 不可达端口，用于孤立单元测试
		MaxQueueDepth:    100,
		ScheduleTimeout:  5,
		AuditLogBaseURLs: []string{startEvidenceStub(t).server.URL},
	}
	d := newTestDeps()
	ag, err := agent.New(cfg, d.mc)
	if err != nil {
		t.Fatalf("new agent client: %v", err)
	}
	ds := datasource.New(cfg)
	return New(ag, ds, cfg, nil, d.tasks, d.logger, d.mc)
}

// newMockE2EServer creates a Server connected to a mock agent (httptest.Server)
// that classifies every record as L3.
// newMockE2EServer 创建对接模拟引擎的全流程测试 Server，模拟引擎恒定定级 L3。
func newMockE2EServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	return newMockEngineServer(t, "L3", true)
}

// newMockEngineServer builds a Server whose upstream engine reports the given
// security level for every processed record.
//
// P1-1 之后生效算子完全由引擎定级推导，测试必须能控制「引擎说这条数据是几级」，
// 否则无法验证 L1→none / L2→mask / L3→k_anon / L4→dp 的映射与只许上调的收敛策略。
// withEvidence=false 时不配置 audit-log 存证端点，用于验证 P0-6 fail-closed：
// 引擎健康但存证端点缺失，任务同样必须失败。
func newMockEngineServer(t *testing.T, level string, withEvidence bool) (*Server, *httptest.Server) {
	t.Helper()

	// 构造 Mock Upstream Agent：模拟动态分类三层漏斗与隐私脱敏 API
	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "namespace": "default"})

		case "/v1/dynclassification/eval_record":
			// 模拟 Agent 动态分类三层漏斗（Rule -> NER -> LLM）评估
			var payload map[string]any
			json.NewDecoder(r.Body).Decode(&payload)
			json.NewEncoder(w).Encode(map[string]any{
				"level":      level,
				"level_id":   level,
				"confidence": 0.92,
				"fields":     []string{"patient_name", "id_card", "diagnosis"},
				"categories": map[string]string{
					"patient_name": "PII",
					"id_card":      "PII",
					"diagnosis":    "PHI",
				},
				"engine": "rule",
			})

		case "/v1/privacy/mask":
			// 模拟字段级掩码脱敏
			var payload map[string]any
			json.NewDecoder(r.Body).Decode(&payload)
			json.NewEncoder(w).Encode(map[string]any{
				"result": "张*",
			})

		case "/v1/privacy/mask_record":
			// 模拟整行记录脱敏
			var payload map[string]any
			json.NewDecoder(r.Body).Decode(&payload)
			json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]string{
					"patient_name": "张*",
					"id_card":      "110***********1234",
					"diagnosis":    "高血压",
				},
			})

		case "/v1/medical/process":
			// 模拟医疗流水线：分类+脱敏一体化（3-Layer 分类 + PII 掩码 + ICD-10 脱敏）
			var payload map[string]any
			json.NewDecoder(r.Body).Decode(&payload)
			records, _ := payload["records"].([]any)
			sanitized := make([]map[string]any, 0, len(records))
			for _, rec := range records {
				if m, ok := rec.(map[string]any); ok {
					s := make(map[string]any, len(m))
					for k, v := range m {
						s[k] = v
					}
					if name, ok := s["patient_name"].(string); ok && len(name) > 1 {
						s["patient_name"] = string(name[0]) + "*"
					}
					if id, ok := s["id_card"].(string); ok && len(id) > 8 {
						s["id_card"] = id[:4] + "***********" + id[len(id)-4:]
					}
					sanitized = append(sanitized, s)
				}
			}
			resp := map[string]any{
				"sanitized_data": sanitized,
				"summary":        map[string]any{"total_records": len(records), "pipeline": "medical"},
			}
			// level == "" 模拟「引擎跑完但没有给出任何定级」的异常契约（P1-1 fail-closed 分支）。
			if level != "" {
				resp["level"] = level
				resp["classification_report"] = []map[string]any{
					{"level": level, "level_id": level, "confidence": 0.92, "engine": "rule"},
				}
				resp["summary"].(map[string]any)["overall_level"] = level
			}
			json.NewEncoder(w).Encode(resp)

		default:
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"detail": "not found"})
		}
	}))

	// 解析 mockServer 的主机与动态端口
	mockURL, _ := url.Parse(mockAgent.URL)
	mockHost, mockPortStr, _ := net.SplitHostPort(mockURL.Host)
	mockPort, _ := strconv.Atoi(mockPortStr)

	cfg := &config.Config{
		Host:            "127.0.0.1",
		Port:            0,
		AgentRESTHost:   mockHost,
		AgentRESTPort:   mockPort,
		MaxQueueDepth:   100,
		ScheduleTimeout: 10,
	}
	if withEvidence {
		// P0-6：全流程 E2E 必须打通 ⑥ 存证阶段，否则任务会在 audit 处 fail-closed 失败。
		cfg.AuditLogBaseURLs = []string{startEvidenceStub(t).server.URL}
	}
	d := newTestDeps()
	ag, err := agent.New(cfg, d.mc)
	if err != nil {
		t.Fatalf("new agent client: %v", err)
	}
	ds := datasource.New(cfg)
	srv := New(ag, ds, cfg, nil, d.tasks, d.logger, d.mc)
	return srv, mockAgent
}

// newTestRouter constructs a test Gin engine with all routes registered.
func newTestRouter(s *Server) *gin.Engine {
	r := gin.New()
	s.RegisterRoutes(r)
	return r
}

// TestHealth tests the /health liveness probe endpoint.
// TestHealth 验证存活探针端点：进程存活即返回 200。
func TestHealth(t *testing.T) {
	s := newSimpleTestServer(t)
	router := newTestRouter(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
	if resp["via"] != "service-hub" {
		t.Errorf("expected via=service-hub, got %v", resp["via"])
	}
}

// TestReadyzAgentUnreachable tests the /readyz readiness probe when the upstream agent is unreachable.
// TestReadyzAgentUnreachable 验证就绪探针在 Agent 不可达时返回 503。
func TestReadyzAgentUnreachable(t *testing.T) {
	s := newSimpleTestServer(t)
	router := newTestRouter(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/readyz", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp["status"] != "not_ready" {
		t.Errorf("expected status=not_ready, got %v", resp["status"])
	}
	if resp["agent"] != "unreachable" {
		t.Errorf("expected agent=unreachable, got %v", resp["agent"])
	}
}

// TestHubStatus tests the /v1/hub/status telemetry overview endpoint.
// TestHubStatus 测试调度中枢状态概览端点返回指标。
func TestHubStatus(t *testing.T) {
	s := newSimpleTestServer(t)
	router := newTestRouter(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/hub/status", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp["status"] != "running" {
		t.Errorf("expected status=running, got %v", resp["status"])
	}
	if resp["active_tasks"].(float64) != 0 {
		t.Errorf("expected 0 active tasks, got %v", resp["active_tasks"])
	}
}

// TestListTasksEmpty tests querying the task list when the repository is empty.
// TestListTasksEmpty 测试空仓库时的任务列表查询。
func TestListTasksEmpty(t *testing.T) {
	s := newSimpleTestServer(t)
	router := newTestRouter(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/hub/tasks", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["total"].(float64) != 0 {
		t.Errorf("expected 0 tasks, got %v", resp["total"])
	}
}

// TestDispatchInvalidBody tests input validation failure on malformed dispatch payloads.
// TestDispatchInvalidBody 测试提交空体或缺失必填字段时的 400 Bad Request 校验阻断。
func TestDispatchInvalidBody(t *testing.T) {
	s := newSimpleTestServer(t)
	router := newTestRouter(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/hub/dispatch", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestDispatchAccepted tests normal dispatch flow returning 202 Accepted.
// TestDispatchAccepted 测试任务合法提交后正确受理并返回 202 Accepted 与 TaskID。
func TestDispatchAccepted(t *testing.T) {
	s := newSimpleTestServer(t)
	router := newTestRouter(s)

	body := map[string]any{
		"source":    "ds_yibao",
		"operation": "mask",
		"payload":   map[string]any{"field_name": "name", "value": "test"},
	}
	b, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/hub/dispatch", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "accepted" {
		t.Errorf("expected status=accepted, got %v", resp["status"])
	}
	if resp["task_id"] == nil || resp["task_id"] == "" {
		t.Error("expected non-empty task_id")
	}

	// 等待后台异步流水线处理
	time.Sleep(200 * time.Millisecond)

	// 校验任务列表已包含该任务
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/hub/tasks", nil)
	router.ServeHTTP(w2, req2)

	var resp2 map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2["total"].(float64) != 1 {
		t.Errorf("expected 1 task, got %v", resp2["total"])
	}
}

func TestProcessTask_StopsWhenStatePersistenceFails(t *testing.T) {
	s := newSimpleTestServer(t)
	failingStore := &failingUpdateTaskStore{TaskStore: memory.NewTaskStore()}
	s.tasks = failingStore

	task := &store.Task{
		ID:        "task-persist-failure",
		Status:    "pending",
		Stage:     "queued",
		Source:    "ds_yibao",
		Operation: "none",
		CreatedAt: time.Now(),
	}
	if err := failingStore.Save(task); err != nil {
		t.Fatalf("save task: %v", err)
	}

	s.processTask(task, dispatchRequest{DatasourceID: task.Source, Source: task.Source, Operation: task.Operation}, "test-req")

	if failingStore.updateCalls != 1 {
		t.Fatalf("expected one failed stage-state write before stopping, got %d", failingStore.updateCalls)
	}
}

// TestPipeline tests the 6-stage pipeline telemetry status endpoint.
// TestPipeline 测试 /v1/hub/pipeline 端点能够准确返回 6 个流水线阶段的实时状态。
func TestPipeline(t *testing.T) {
	s := newSimpleTestServer(t)
	router := newTestRouter(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/hub/pipeline", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	stages := resp["stages"].([]any)
	if len(stages) != 6 {
		t.Errorf("expected 6 stages, got %d", len(stages))
	}
}

// TestListTasksWithFilter tests task list querying with status filtering (completed vs pending).
// TestListTasksWithFilter 测试基于 status 查询参数的任务列表过滤能力。
func TestListTasksWithFilter(t *testing.T) {
	// P1-1 之后任何带数据的任务都必须过引擎定级，因此这里使用可达的模拟引擎。
	s, mockAgent := newMockE2EServer(t)
	defer mockAgent.Close()
	router := newTestRouter(s)

	// 分发一个 operation=none 任务（生效算子由定级推导，请求算子只是强度提示）
	taskID := dispatchTask(t, router, map[string]any{
		"source":    "ds_yibao",
		"operation": "none",
		"payload":   []map[string]any{{"data": "sample"}},
	})
	if task := waitForTaskTerminal(t, s, taskID); task.Status != "completed" {
		t.Fatalf("task must complete, got status=%q stage=%q error=%q", task.Status, task.Stage, task.Error)
	}

	// 过滤已完成任务
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/hub/tasks?status=completed", nil)
	router.ServeHTTP(w2, req2)

	var resp map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &resp)
	total := resp["total"].(float64)
	if total != 1 {
		t.Errorf("expected 1 completed task, got %v", total)
	}

	// 过滤排队中任务（完成后应为 0）
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/v1/hub/tasks?status=pending", nil)
	router.ServeHTTP(w3, req3)

	var resp3 map[string]any
	_ = json.Unmarshal(w3.Body.Bytes(), &resp3)
	if resp3["total"].(float64) != 0 {
		t.Errorf("expected 0 pending tasks, got %v", resp3["total"])
	}
}

// ============================================================================
// E2E Tests: Full pipeline flow (申请数据 → 分类分级 → 脱敏 → 拿到脱敏数据)
// ============================================================================

// TestE2E_FullPipeline_DispatchMasking tests the complete data desensitization flow:
//  1. Submit a masking task via POST /v1/hub/dispatch (operation=mask)
//  2. Pipeline processes 6 stages: ingest → fetch → classify → desensitize → return → audit
//  3. Task completes successfully with masked result from mock agent
//  4. Verify task status = completed, stage = done, duration > 0
//
// TestE2E_FullPipeline_DispatchMasking 测试完整的脱敏数据全流程：
//  1. 提交脱敏任务（operation=mask）
//  2. 流水线跑完 6 阶段：请求接入 → 申请原数 → 分类分级 → 下发脱敏 → 返回结果 → 存证写日志
//  3. 任务成功完成，模拟 Agent 返回脱敏结果
//  4. 验证任务状态=completed，阶段=done，耗时>0
func TestE2E_FullPipeline_DispatchMasking(t *testing.T) {
	// 引擎定级 L2（内部数据）⇒ 推导算子恰为 mask，请求算子与生效算子一致。
	// P1-1 之后生效算子只由定级推导，调用方请求仅在更强时生效（见 TestE2E_CallerCannotDowngradeOperator）。
	srv, mockAgent := newMockEngineServer(t, "L2", true)
	defer mockAgent.Close()
	defer srv.Shutdown()
	router := newTestRouter(srv)

	// Step 1: 申请数据 — 提交包含医疗 PII 的脱敏请求
	dispatchBody := map[string]any{
		"source":    "ds_yibao",
		"operation": "mask",
		"payload": map[string]any{
			"patient_name": "张三",
			"id_card":      "110101199001011234",
			"diagnosis":    "高血压",
		},
		"priority": 40,
	}
	b, _ := json.Marshal(dispatchBody)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/hub/dispatch", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("dispatch: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var dispatchResp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &dispatchResp)
	taskID := dispatchResp["task_id"].(string)
	if taskID == "" {
		t.Fatal("dispatch: expected non-empty task_id")
	}
	if dispatchResp["status"] != "accepted" {
		t.Errorf("dispatch: expected status=accepted, got %v", dispatchResp["status"])
	}
	t.Logf("✅ Step 1 passed: 任务已提交 task_id=%s", taskID)

	// Step 2: 等待流水线处理完成 (6 stages × 100ms each + buffer)
	time.Sleep(1200 * time.Millisecond)

	// Step 3: 拿到脱敏数据 — 根据 TaskID 直接查询任务详情
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/hub/tasks/"+taskID, nil)
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("get task by id: expected 200, got %d", w2.Code)
	}

	var getResp map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &getResp)
	task := getResp["task"].(map[string]any)

	// 校验任务已跨越全部 6 个流水线阶段成功完成
	if task["status"] != "completed" {
		t.Errorf("expected status=completed, got %v", task["status"])
	}
	if task["stage"] != "done" {
		t.Errorf("expected stage=done, got %v", task["stage"])
	}
	if task["source"] != "ds_yibao" {
		t.Errorf("expected source=ds_yibao, got %v", task["source"])
	}
	if task["operation"] != "mask" {
		t.Errorf("expected operation=mask, got %v", task["operation"])
	}
	durationMs := task["duration_ms"].(float64)
	if durationMs <= 0 {
		t.Errorf("expected duration_ms > 0, got %v", durationMs)
	}
	if errMsg, ok := task["error"].(string); ok && errMsg != "" {
		t.Errorf("unexpected error: %s", errMsg)
	}
	t.Logf("✅ Step 2 passed: 流水线完成 status=completed stage=done duration=%.0fms", durationMs)

	// Step 4: 校验调度中枢状态中的完成任务计数已增加
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/v1/hub/status", nil)
	router.ServeHTTP(w3, req3)

	var hubStatus map[string]any
	_ = json.Unmarshal(w3.Body.Bytes(), &hubStatus)
	if hubStatus["completed_total"].(float64) != 1 {
		t.Errorf("expected completed_total=1, got %v", hubStatus["completed_total"])
	}
	t.Logf("✅ Step 3 passed: 调度中枢状态已更新 completed_total=1")
}

// TestE2E_FullPipeline_MultiLevelDesensitize proves the P1-1 derivation ladder:
// the operator a task actually applies is decided by the level the engine reports,
// never by what the caller asked for.
// TestE2E_FullPipeline_MultiLevelDesensitize 验证 P1-1 定级推导阶梯：
// 生效算子由引擎定级结果决定，调用方请求只允许上调，绝不允许下调。
//   - L1 → none    (公开数据，明文流转)
//   - L2 → mask    (内部数据，字段掩码)
//   - L3 → k_anon  (敏感数据，K-匿名泛化)
//   - L4 → dp      (高敏感数据，差分隐私)
//   - L5 → dp      (极敏感数据，强差分隐私)
func TestE2E_FullPipeline_MultiLevelDesensitize(t *testing.T) {
	testCases := []struct {
		name      string
		level     string // 模拟引擎给出的定级结果
		operation string // 调用方请求算子（一律低于或等于定级推导结果，验证其被采纳）
		source    string
		want      string // 期望生效算子
	}{
		{"L1-公开数据-无脱敏", "L1", "none", "ds_yibao", "none"},
		{"L2-内部数据-字段脱敏", "L2", "mask", "ds_yibao", "mask"},
		{"L3-敏感数据-K匿名", "L3", "k_anon", "ds_kangyang", "k_anon"},
		{"L4-机密数据-差分隐私", "L4", "dp", "ds_yibao", "dp"},
		{"L5-极敏感数据-差分隐私", "L5", "dp", "ds_kangyang", "dp"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srv, mockAgent := newMockEngineServer(t, tc.level, true)
			defer mockAgent.Close()
			defer srv.Shutdown()
			router := newTestRouter(srv)

			// 1. 提交任务
			taskID := dispatchTask(t, router, map[string]any{
				"source":    tc.source,
				"operation": tc.operation,
				"payload": map[string]any{
					"name":    "测试用户",
					"id_card": "110101199001011234",
				},
			})

			// 2. 等待流水线进入终态并校验生效算子
			task := waitForTaskTerminal(t, srv, taskID)
			if task.Status != "completed" {
				t.Fatalf("expected completed, got status=%q stage=%q error=%q", task.Status, task.Stage, task.Error)
			}
			if task.Operation != tc.want {
				t.Errorf("applied operation = %q, want %q (derived from level %s)", task.Operation, tc.want, tc.level)
			}
			t.Logf("  ✅ 定级 %s ⇒ 生效算子 %s (请求 %s)", tc.level, task.Operation, tc.operation)
		})
	}
}

// TestE2E_CallerCannotDowngradeOperator is the core P1-1 security assertion: a caller
// asking for "none" (raw egress) against L4 data must still get differential privacy.
// TestE2E_CallerCannotDowngradeOperator 是 P1-1 的核心断言：调用方以 operation=none
// 请求「原值直传」时，只要定级为 L4，服务端仍强制走差分隐私，越权降级路径被彻底消除。
func TestE2E_CallerCannotDowngradeOperator(t *testing.T) {
	cases := []struct {
		requested string
		level     string
		want      string
	}{
		{"none", "L4", "dp"},     // 直传请求被强制升级为差分隐私
		{"mask", "L3", "k_anon"}, // 弱掩码请求被强制升级为 K-匿名
		{"classify", "L5", "dp"}, // 仅定级请求被强制升级为差分隐私
		{"dp", "L2", "dp"},       // 请求更强时允许上调（不降回 mask）
		{"", "L3", "k_anon"},     // 未请求时完全由定级推导
	}
	for _, tc := range cases {
		name := tc.requested + "@" + tc.level
		t.Run(name, func(t *testing.T) {
			srv, mockAgent := newMockEngineServer(t, tc.level, true)
			defer mockAgent.Close()
			defer srv.Shutdown()
			router := newTestRouter(srv)

			taskID := dispatchTask(t, router, map[string]any{
				"source":    "ds_yibao",
				"operation": tc.requested,
				"payload":   map[string]any{"id_card": "110101199001011234"},
			})

			task := waitForTaskTerminal(t, srv, taskID)
			if task.Status != "completed" {
				t.Fatalf("task must complete, got status=%q stage=%q error=%q", task.Status, task.Stage, task.Error)
			}
			if task.Operation != tc.want {
				t.Errorf("operation = %q, want %q (requested %q, level %s must set the floor)",
					task.Operation, tc.want, tc.requested, tc.level)
			}
		})
	}
}

// TestE2E_MissingClassificationLevelFailsTask closes the silent-downgrade branch: an
// engine answer without any recognizable level must fail the task, not fall back to a
// default operator.
// TestE2E_MissingClassificationLevelFailsTask 关闭静默降级分支：引擎未返回可识别定级时
// 任务必须以 failed 终态收场，绝不能套用默认算子继续出域。
func TestE2E_MissingClassificationLevelFailsTask(t *testing.T) {
	srv, mockAgent := newMockEngineServer(t, "", true)
	defer mockAgent.Close()
	defer srv.Shutdown()
	router := newTestRouter(srv)

	taskID := dispatchTask(t, router, map[string]any{
		"source":    "ds_yibao",
		"operation": "mask",
		"payload":   map[string]any{"id_card": "110101199001011234"},
	})

	task := waitForTaskTerminal(t, srv, taskID)
	if task.Status != "failed" {
		t.Fatalf("missing level must fail the task, got status=%q stage=%q", task.Status, task.Stage)
	}
	if !strings.Contains(task.Error, "no security level") {
		t.Errorf("task error must name the missing level, got %q", task.Error)
	}
}

// TestE2E_FullPipeline_HealthCheckWithAgent verifies that the /readyz endpoint
// correctly reports agent connectivity when the mock agent is reachable.
// TestE2E_FullPipeline_HealthCheckWithAgent 验证 Agent 可达时就绪探针正确报告连通状态。
func TestE2E_FullPipeline_HealthCheckWithAgent(t *testing.T) {
	srv, mockAgent := newMockE2EServer(t)
	defer mockAgent.Close()
	router := newTestRouter(srv)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/readyz", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["status"] != "ready" {
		t.Errorf("expected status=ready, got %v", resp["status"])
	}
	if resp["agent"] == "unreachable" {
		t.Error("expected agent to be reachable via mock server")
	}
	t.Logf("✅ Agent 可达: agent=%v", resp["agent"])
}

// TestE2E_FullPipeline_PipelineStagesWithAgent verifies pipeline stage status
// when tasks are actively processing through the mock agent.
// TestE2E_FullPipeline_PipelineStagesWithAgent 验证任务处理期间流水线各阶段状态。
func TestE2E_FullPipeline_PipelineStagesWithAgent(t *testing.T) {
	srv, mockAgent := newMockE2EServer(t)
	defer mockAgent.Close()
	router := newTestRouter(srv)

	// 提交一个会调用 mock agent 的任务
	body := map[string]any{
		"source":    "ds_yibao",
		"operation": "mask",
		"payload":   map[string]any{"name": "测试"},
	}
	b, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/hub/dispatch", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}

	// 检查处理中的流水线阶段遥测
	time.Sleep(50 * time.Millisecond)
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/hub/pipeline", nil)
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	var pipelineResp map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &pipelineResp)

	if pipelineResp["agent_ok"] != true {
		t.Error("expected agent_ok=true")
	}

	stages := pipelineResp["stages"].([]any)
	if len(stages) != 6 {
		t.Errorf("expected 6 stages, got %d", len(stages))
	}
	t.Logf("✅ 流水线 6 阶段正常, Agent 连接正常")

	// 等待流水线全部收敛完成
	time.Sleep(1200 * time.Millisecond)
}

// TestGetTask_SuccessAndNotFound tests single task lookup with existing ID and non-existing ID.
// TestGetTask_SuccessAndNotFound 测试单任务详情查询（命中返回 200 与未命中返回 404）。
func TestGetTask_SuccessAndNotFound(t *testing.T) {
	s := newSimpleTestServer(t)
	router := newTestRouter(s)

	now := time.Now()
	_ = s.tasks.Save(&store.Task{ID: "task-abc-123", Status: "running", Source: "source1", CreatedAt: now})

	t.Run("Found", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/hub/tasks/task-abc-123", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		taskMap, ok := resp["task"].(map[string]any)
		if !ok || taskMap["id"] != "task-abc-123" || taskMap["status"] != "running" {
			t.Errorf("unexpected task payload: %+v", resp)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/hub/tasks/nonexistent", nil)
		router.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})
}

// TestDispatch_OversizedSource tests rejection of oversized source strings.
// TestDispatch_OversizedSource 测试超长源名称（>1024 字节）被安全拦截。
func TestDispatch_OversizedSource(t *testing.T) {
	s := newSimpleTestServer(t)
	router := newTestRouter(s)

	oversized := map[string]any{
		"source":    string(make([]byte, 1025)),
		"operation": "mask",
	}
	body, _ := json.Marshal(oversized)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/hub/dispatch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized source, got %d", w.Code)
	}
}

// TestListTasks_InvalidStatusFilter tests rejection of illegal status filters.
// TestListTasks_InvalidStatusFilter 测试非法状态过滤参数被正确拦截。
func TestListTasks_InvalidStatusFilter(t *testing.T) {
	s := newSimpleTestServer(t)
	router := newTestRouter(s)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/hub/tasks?status=illegal_status", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid status filter, got %d", w.Code)
	}
}

// TestAuthMiddleware_Protection tests API Key authentication middleware protection.
// TestAuthMiddleware_Protection 测试 API Key 鉴权中间件的防护拦截（无认证头 401、携带有效 Bearer 头 200、Health 接口免密放行）。
func TestAuthMiddleware_Protection(t *testing.T) {
	cfg := &config.Config{
		Host:          "127.0.0.1",
		Port:          0,
		AgentRESTHost: "127.0.0.1",
		AgentRESTPort: 19999,
		APIKey:        "secret-token-123",
	}
	d := newTestDeps()
	ag, err := agent.New(cfg, d.mc)
	if err != nil {
		t.Fatalf("new agent client: %v", err)
	}
	ds := datasource.New(cfg)
	s := New(ag, ds, cfg, nil, d.tasks, d.logger, d.mc)

	r := gin.New()
	s.RegisterRoutes(r)

	t.Run("Unauthorized_NoHeader", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/hub/status", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})

	t.Run("Authorized_Bearer", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/hub/status", nil)
		req.Header.Set("Authorization", "Bearer secret-token-123")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	t.Run("Health_ExemptFromAuth", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/health", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for health exempt from auth, got %d", w.Code)
		}
	})
}

// TestScopeAuthMiddleware_AccessControl 验证 service-hub Scope-based 鉴权的细粒度权限控制，
// 包括不同 Scope Key 的允许/拒绝以及带尾部斜杠路径不会被绕过。
func TestScopeAuthMiddleware_AccessControl(t *testing.T) {
	cfg := &config.Config{
		Host:          "127.0.0.1",
		Port:          0,
		AgentRESTHost: "127.0.0.1",
		AgentRESTPort: 19999,
		ScopeKeys: map[string]*pkgauth.KeyConfig{
			"read-only-token":   {Name: "reader", Scopes: []string{"hub:read"}},
			"dispatch-token":    {Name: "dispatcher", Scopes: []string{"hub:dispatch"}},
			"full-access-token": {Name: "admin", Scopes: []string{"*"}},
		},
	}
	d := newTestDeps()
	ag, err := agent.New(cfg, d.mc)
	if err != nil {
		t.Fatalf("new agent client: %v", err)
	}
	ds := datasource.New(cfg)
	s := New(ag, ds, cfg, nil, d.tasks, d.logger, d.mc)

	r := gin.New()
	s.RegisterRoutes(r)

	cases := []struct {
		name       string
		token      string
		path       string
		method     string
		wantStatus int
	}{
		{"reader can read tasks", "read-only-token", "/v1/hub/tasks", "GET", http.StatusOK},
		{"reader cannot dispatch", "read-only-token", "/v1/hub/dispatch", "POST", http.StatusForbidden},
		{"dispatcher can dispatch", "dispatch-token", "/v1/hub/dispatch", "POST", http.StatusAccepted},
		{"dispatcher cannot read", "dispatch-token", "/v1/hub/tasks", "GET", http.StatusForbidden},
		{"admin can do both", "full-access-token", "/v1/hub/tasks", "GET", http.StatusOK},
		{"admin can dispatch", "full-access-token", "/v1/hub/dispatch", "POST", http.StatusAccepted},
		{"invalid token rejected", "bad-token", "/v1/hub/tasks", "GET", http.StatusUnauthorized},
		{"missing token rejected", "", "/v1/hub/tasks", "GET", http.StatusUnauthorized},
		{"health exempt", "", "/health", "GET", http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body *strings.Reader
			if tc.method == "POST" {
				body = strings.NewReader(`{"api_code":"api1_yibao","payload":{"records":[{"name":"test"}]}}`)
			} else {
				body = strings.NewReader("")
			}
			req, _ := http.NewRequest(tc.method, tc.path, body)
			if tc.method == "POST" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d: %s", tc.wantStatus, w.Code, w.Body.String())
			}
		})
	}
}

// TestServer_ShutdownGraceful tests graceful shutdown execution without panic.
// TestServer_ShutdownGraceful 测试优雅停机方法能平滑执行完毕。
func TestServer_ShutdownGraceful(t *testing.T) {
	s := newSimpleTestServer(t)
	s.Shutdown()
}

// TestServer_LocalPendingWorker tests that StartLocalWorker picks up and processes pending tasks in SQLite/memory mode.
func TestServer_LocalPendingWorker(t *testing.T) {
	// 使用 L1 模拟引擎，让 operation=none 任务能直接收敛为 completed。
	s, mockAgent := newMockEngineServer(t, "L1", true)
	defer mockAgent.Close()
	defer s.Shutdown()

	task := &store.Task{
		ID:          "recovered-pending-task",
		Status:      "pending",
		Stage:       "queued",
		Source:      "ds_yibao",
		Operation:   "none",
		PayloadJSON: `[{"name":"test"}]`,
		CreatedAt:   time.Now(),
	}
	if err := s.tasks.Save(task); err != nil {
		t.Fatalf("save pending task: %v", err)
	}

	if err := s.StartLocalWorker(); err != nil {
		t.Fatalf("start local worker: %v", err)
	}

	// Wait for worker loop to pick up and complete the task (500ms poll + 6*100ms pipeline)
	deadline := time.Now().Add(3 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		tCheck, err := s.tasks.Get("recovered-pending-task")
		if err == nil && tCheck.Status == "completed" {
			completed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !completed {
		tCheck, _ := s.tasks.Get("recovered-pending-task")
		t.Fatalf("expected task to be completed by local worker, got state: %+v", tCheck)
	}
}

// TestHubOrchestrationEndpoints tests the orchestration endpoints provided by service-hub for app-lz.
func TestHubOrchestrationEndpoints(t *testing.T) {
	s, mockAgent := newMockEngineServer(t, "L1", true)
	defer mockAgent.Close()
	defer s.Shutdown()

	router := newTestRouter(s)

	// 1. GET /v1/hub/topology
	t.Run("HubTopology", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/hub/topology", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var topo map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &topo)
		if topo["status"] == "" {
			t.Errorf("expected non-empty status in topology: %+v", topo)
		}
		services, ok := topo["services"].([]any)
		if !ok || len(services) != 4 {
			t.Fatalf("expected 4 services in topology, got %d", len(services))
		}
	})

	// 2. GET /v1/hub/audit/logs
	t.Run("GetAuditLogs", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/hub/audit/logs?limit=10", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var logsResp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &logsResp)
		if logsResp["via"] != "service-hub" {
			t.Errorf("expected via=service-hub, got %v", logsResp["via"])
		}
	})

	// 3. POST /v1/hub/audit/verify
	t.Run("VerifyAudit", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/hub/audit/verify", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var verifyResp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &verifyResp)
		if verifyResp["merkle_valid"] != true {
			t.Errorf("expected merkle_valid=true, got %+v", verifyResp)
		}
	})

	// 4. POST /v1/hub/audit/logs
	t.Run("CreateAuditLog", func(t *testing.T) {
		payload := []byte(`{"task_id":"task-test-1","datasource":"ds_yibao","api_code":"api1_yibao","operation":"mask","status":"success"}`)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/hub/audit/logs", bytes.NewReader(payload))
		router.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})
}

// TestDatasourceLevelABACAuthorization 验证调度中枢数据源级细粒度授权（ABAC / 租户数据源隔离）：
// 1. 外部申请方持有特定数据源 scope (hub:dispatch:ds_yibao) 时，访问 ds_yibao 允许；
// 2. 尝试越权访问未授权数据源 (ds_kangyang) 返回 403 UNAUTHORIZED_DATASOURCE；
// 3. 超级管理员身份 (admin / *) 拥有全数据源访问权限。
func TestDatasourceLevelABACAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &Server{
		cfg: &config.Config{
			ScopeKeys: map[string]*pkgauth.KeyConfig{
				"token-yibao-only": {
					Name:   "app-yibao",
					Scopes: []string{"hub:dispatch", "hub:dispatch:ds_yibao"},
				},
				"token-kangyang-only": {
					Name:   "app-kangyang",
					Scopes: []string{"hub:dispatch", "data:apply:ds_kangyang"},
				},
			},
		},
		logger: slog.Default(),
	}

	r := gin.New()
	r.Use(srv.scopeAuthMiddleware())
	r.POST("/v1/hub/fetch-and-desensitize", srv.FetchAndDesensitize)

	// 1. app-yibao 访问已授权的 yibao（因无下游 mock，通过鉴权后进入上游不可用即代表鉴权通过）
	w1 := httptest.NewRecorder()
	body1 := bytes.NewBufferString(`{"datasource_id":"ds_yibao","id_card_no":"110101196809171010"}`)
	req1, _ := http.NewRequest(http.MethodPost, "/v1/hub/fetch-and-desensitize", body1)
	req1.Header.Set("Authorization", "Bearer token-yibao-only")
	req1.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w1, req1)
	if w1.Code == http.StatusForbidden {
		t.Fatalf("expected yibao authorized caller not to get 403, got %d: %s", w1.Code, w1.Body.String())
	}

	// 2. app-yibao 越权访问 kangyang -> 必须返回 403 UNAUTHORIZED_DATASOURCE
	w2 := httptest.NewRecorder()
	body2 := bytes.NewBufferString(`{"datasource_id":"ds_kangyang","id_card_no":"110101196809171010"}`)
	req2, _ := http.NewRequest(http.MethodPost, "/v1/hub/fetch-and-desensitize", body2)
	req2.Header.Set("Authorization", "Bearer token-yibao-only")
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for unauthorized datasource, got %d: %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), "UNAUTHORIZED_DATASOURCE") {
		t.Errorf("expected UNAUTHORIZED_DATASOURCE in response, got %s", w2.Body.String())
	}

	// 3. app-kangyang 访问已授权的 kangyang
	w3 := httptest.NewRecorder()
	body3 := bytes.NewBufferString(`{"datasource_id":"ds_kangyang","id_card_no":"110101196809171010"}`)
	req3, _ := http.NewRequest(http.MethodPost, "/v1/hub/fetch-and-desensitize", body3)
	req3.Header.Set("Authorization", "Bearer token-kangyang-only")
	req3.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w3, req3)
	if w3.Code == http.StatusForbidden {
		t.Fatalf("expected kangyang authorized caller not to get 403, got %d: %s", w3.Code, w3.Body.String())
	}
}

