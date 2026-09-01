package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield/pkg/store"
)

// TestPipelineAuditStageRecordsEvidence proves Gate G-05 acceptance: after one egress
// call a row bound to that task_id exists in the audit log, carrying the canonical
// datasource/api identifiers, the real sensitivity level and both fingerprints.
// TestPipelineAuditStageRecordsEvidence 验证 G-05 验收点：一次出域调用后，
// audit-log 中必然存在一条以该 task_id 绑定的存证，且含 canonical 数据源/api_code、
// 引擎给出的最高敏感级别与输入输出指纹（出域事实可事后对账）。
func TestPipelineAuditStageRecordsEvidence(t *testing.T) {
	srv, mockAgent := newMockE2EServer(t)
	defer mockAgent.Close()
	stub := evidenceStubOf(srv)
	if stub == nil {
		t.Fatal("test server must carry an audit-log evidence stub")
	}
	router := newTestRouter(srv)

	taskID := dispatchTask(t, router, map[string]any{
		"source":    "ds_yibao",
		"operation": "mask",
		"payload": []map[string]any{
			{"patient_name": "张三", "id_card": "110101199001011234", "diagnosis": "高血压"},
		},
		"priority": 40,
	})

	task := waitForTaskTerminal(t, srv, taskID)
	if task.Status != "completed" || task.Stage != "done" {
		t.Fatalf("task must complete once evidence is accepted, got status=%q stage=%q error=%q",
			task.Status, task.Stage, task.Error)
	}

	subs := stub.submissions()
	if len(subs) != 1 {
		t.Fatalf("exactly one evidence row per egress call, got %d: %#v", len(subs), subs)
	}
	rec := subs[0]

	if rec["task_id"] != taskID {
		t.Errorf("evidence task_id = %v, want %q", rec["task_id"], taskID)
	}
	if rec["datasource_id"] != "ds_yibao" {
		t.Errorf("evidence datasource_id = %v, want ds_yibao", rec["datasource_id"])
	}
	if rec["api_code"] != "api1_yibao" {
		t.Errorf("evidence api_code = %v, want api1_yibao", rec["api_code"])
	}
	// 存证记录的是「真实生效算子」：P1-1 之后由定级推导（模拟引擎给出 L3 ⇒ k_anon），
	// 而不是调用方请求的 mask。留痕必须与出域事实一致，否则事后无法对账。
	if rec["operation"] != "k_anon" {
		t.Errorf("evidence operation = %v, want k_anon (derived from level L3)", rec["operation"])
	}
	if rec["status"] != "success" {
		t.Errorf("evidence status = %v, want success", rec["status"])
	}
	if rec["user"] != "service-hub" {
		t.Errorf("evidence user = %v, want service-hub", rec["user"])
	}
	// 敏感级别来自 ③ 分类分级结果（模拟引擎给出 L3），不是硬编码常量。
	if rec["security_level"] != "L3" {
		t.Errorf("evidence security_level = %v, want L3 (from classification report)", rec["security_level"])
	}

	inHash, _ := rec["input_hash"].(string)
	outHash, _ := rec["output_hash"].(string)
	if len(inHash) != 64 || len(outHash) != 64 {
		t.Fatalf("evidence fingerprints must be 64-hex digests, got in=%q out=%q", inHash, outHash)
	}
	if inHash == outHash {
		t.Error("input/output fingerprints must differ for a masking operation")
	}
	if rows, _ := rec["input_rows"].(float64); rows < 1 {
		t.Errorf("evidence input_rows = %v, want >= 1", rec["input_rows"])
	}
	if _, exists := rec["prev_hash"]; exists {
		t.Error("hub must never send prev_hash: the chain tail belongs to the audit store")
	}
}

// TestPipelineAuditStageFailureMarksTaskFailed is the fail-closed half of P0-6:
// whenever the evidence write does not succeed the task must end as failed —
// never completed, never silently retried into "done".
// TestPipelineAuditStageFailureMarksTaskFailed 覆盖 P0-6 红线一（提交失败即任务失败）：
// 存证被 4xx 拒绝、5xx 不可用、以及存证端点完全未配置三种情形下，
// 任务终态必须为 failed 且停留在 audit 阶段，绝不出现「已出域但无存证却 done」。
func TestPipelineAuditStageFailureMarksTaskFailed(t *testing.T) {
	const rejectedBody = `{"code":"INVALID_ARGUMENT","message":"invalid operation: must be one of [mask classify k_anon dp qol]"}`

	tests := []struct {
		name       string
		operation  string
		stubSetup  func(t *testing.T, stub *evidenceStub)
		noEvidence bool // true = 引擎可用但不配置任何 audit-log 存证端点
		wantCalls  int
		wantErrHas string
	}{
		{
			name:      "4xx rejection ends the task as failed without retry",
			operation: "mask",
			stubSetup: func(_ *testing.T, stub *evidenceStub) {
				stub.failWith(http.StatusBadRequest, rejectedBody)
			},
			wantCalls:  1, // 契约级拒绝不重试，避免重复产生被拒存证
			wantErrHas: "audit-log rejected the evidence record",
		},
		{
			name:      "409 reserved datasource ends the task as failed",
			operation: "mask",
			stubSetup: func(_ *testing.T, stub *evidenceStub) {
				stub.failWith(http.StatusConflict, `{"code":"CONFLICT","message":"reserved datasource"}`)
			},
			wantCalls:  1,
			wantErrHas: "audit-log rejected the evidence record",
		},
		{
			name:      "5xx outage ends the task as failed after retries",
			operation: "mask",
			stubSetup: func(_ *testing.T, stub *evidenceStub) {
				stub.failWith(http.StatusServiceUnavailable, `{"code":"UPSTREAM_UNAVAILABLE","message":"audit-log is down"}`)
			},
			// 测试用配置未设置 AuditLogMaxRetries（0 次重试），退避重试行为由 audit 客户端单测覆盖。
			wantCalls:  1,
			wantErrHas: "audit-log evidence service is unavailable",
		},
		{
			// P1-1 之后该任务同样必须过引擎：用健康的模拟引擎 + 缺失的存证端点，
			// 单独考察「存证端点未配置」这一条 fail-closed 路径。
			name:       "missing evidence endpoint ends the task as failed with no HTTP call",
			operation:  "mask",
			noEvidence: true,
			wantCalls:  0,
			wantErrHas: "audit-log evidence endpoint is not configured",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				srv  *Server
				stub *evidenceStub
			)
			switch {
			case tc.noEvidence:
				// 引擎可用但未配置任何存证端点：进程可运行，但每一条出域任务必须失败。
				noEv, mockAgent := newMockEngineServer(t, "L3", false)
				defer mockAgent.Close()
				srv = noEv
			default:
				mockSrv, mockAgent := newMockE2EServer(t)
				defer mockAgent.Close()
				srv = mockSrv
				stub = evidenceStubOf(srv)
				if tc.stubSetup != nil {
					tc.stubSetup(t, stub)
				}
			}
			if stub == nil && !tc.noEvidence {
				t.Fatal("test server must carry an audit-log evidence stub")
			}

			router := newTestRouter(srv)
			taskID := dispatchTask(t, router, map[string]any{
				"source":    "ds_yibao",
				"operation": tc.operation,
				"payload":   []map[string]any{{"patient_name": "张三", "id_card": "110101199001011234"}},
			})

			task := waitForTaskTerminal(t, srv, taskID)
			if task.Status != "failed" {
				t.Fatalf("evidence failure must mark the task failed (P0-6), got status=%q stage=%q", task.Status, task.Stage)
			}
			if task.Stage != "audit" {
				t.Errorf("task must stop at the audit stage, got stage=%q", task.Stage)
			}
			if task.CompletedAt == nil || task.DurationMs <= 0 {
				t.Errorf("failed task must still carry a terminal timestamp, got completed_at=%v duration=%dms", task.CompletedAt, task.DurationMs)
			}
			if !strings.Contains(task.Error, "audit evidence submission failed") {
				t.Errorf("task error must name the evidence submission failure, got %q", task.Error)
			}
			if tc.wantErrHas != "" && !strings.Contains(task.Error, tc.wantErrHas) {
				t.Errorf("task error must contain %q, got %q", tc.wantErrHas, task.Error)
			}

			if stub != nil {
				if got := stub.callCount(); got != tc.wantCalls {
					t.Errorf("evidence HTTP attempts = %d, want %d", got, tc.wantCalls)
				}
			}

			// 任务落库后不得被后续阶段改写为 done：状态位与存证在代码层面强绑定。
			reread, err := srv.tasks.Get(taskID)
			if err != nil {
				t.Fatalf("reload task: %v", err)
			}
			if reread.Status != "failed" {
				t.Errorf("persisted task status = %q, want failed", reread.Status)
			}
		})
	}
}

// dispatchTask submits a task through POST /api/hub/dispatch and returns its task_id.
// dispatchTask 通过 POST /api/hub/dispatch 提交任务并返回 task_id。
func dispatchTask(t *testing.T, router *gin.Engine, body map[string]any) string {
	t.Helper()
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/hub/dispatch", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("dispatch expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode dispatch response: %v", err)
	}
	taskID, _ := resp["task_id"].(string)
	if taskID == "" {
		t.Fatalf("dispatch response carries no task_id: %s", w.Body.String())
	}
	return taskID
}

// waitForTaskTerminal polls the store until the task leaves the running state.
// waitForTaskTerminal 轮询任务仓库直至任务进入终态（completed/failed），
// 取代脆弱的固定 sleep：6 阶段流水线（含 ⑥ 存证重试）耗时随环境波动。
func waitForTaskTerminal(t *testing.T, srv *Server, taskID string) *store.Task {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		task, err := srv.tasks.Get(taskID)
		if err != nil {
			t.Fatalf("get task %s: %v", taskID, err)
		}
		if task.Status == "completed" || task.Status == "failed" {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach a terminal state within 15s", taskID)
	return nil
}
