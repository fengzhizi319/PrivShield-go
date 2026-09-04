// Package tests holds black-box contracts that must hold across the service-hub
// entry points. It only uses exported APIs so a change in one pipeline cannot be
// hidden behind an internal helper shared with its twin.
// Package tests 承载「跨入口黑盒契约」测试：只使用导出 API，
// 避免两条流水线共享的内部测试夹具把某一侧的行为漂移掩盖掉。
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/memory"

	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/datasource"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/grpcserver"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/handlers"
	pb "github.com/fengzhizi319/PrivShield-go/services/service-hub/proto"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newEngineStub answers the medical pipeline with a fixed classification level and
// 404s the single-pass endpoint, matching an engine build without /v1/agent/process.
// newEngineStub 用固定的定级结果应答医疗流水线，并对单趟端点返回 404，
// 等价于尚未提供 /v1/agent/process 的引擎版本（走回退分支）。
func newEngineStub(level string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case "/v1/medical/process":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			records, _ := payload["records"].([]any)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"level":                 level,
				"classification_report": []map[string]any{{"level": level, "level_id": level}},
				"sanitized_data":        records,
				"summary":               map[string]any{"total_records": len(records), "overall_level": level},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"detail": "not found"})
		}
	}))
}

func newEvidenceStub() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audit/logs" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             "audit-contract-1",
			"integrity_hash": "integrity-contract-1",
			"prev_hash":      "genesis",
		})
	}))
}

func testConfig(t *testing.T, engineURL, evidenceURL string) *config.Config {
	t.Helper()
	u, err := url.Parse(engineURL)
	if err != nil {
		t.Fatalf("parse engine URL: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split engine host/port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("engine port %q: %v", portStr, err)
	}
	return &config.Config{
		Host:             "127.0.0.1",
		Port:             0,
		AgentRESTHost:    host,
		AgentRESTPort:    port,
		MaxQueueDepth:    100,
		ScheduleTimeout:  10,
		AuditLogBaseURLs: []string{evidenceURL},
	}
}

// TestPipelineOperationDerivationIsIdenticalAcrossRESTandGRPC is the P1-1 dual-path
// guarantee: the same data and the same caller request must end in the same applied
// operator whether it entered through POST /v1/hub/dispatch or the Dispatch RPC.
// A second, drifting derivation rule in either entry point fails this test.
//
// TestPipelineOperationDerivationIsIdenticalAcrossRESTandGRPC 是 P1-1 的双路径一致性保证：
// 同一份数据 + 同一个调用方请求，无论走 REST 还是 gRPC，最终生效算子必须完全相同。
// 任何一侧偷偷长出第二套推导口径，本用例即失败。
func TestPipelineOperationDerivationIsIdenticalAcrossRESTandGRPC(t *testing.T) {
	cases := []struct {
		level     string
		requested string
		want      string
	}{
		{"L1", "none", "none"},
		{"L2", "mask", "mask"},
		{"L3", "mask", "k_anon"}, // 定级抬高请求：REST/gRPC 都必须升到 k_anon
		{"L4", "none", "dp"},     // 直传请求必须被强制升级为差分隐私
		{"L5", "k_anon", "dp"},
		{"L2", "dp", "dp"}, // 只允许上调：更强的请求保留
	}

	for _, tc := range cases {
		name := fmt.Sprintf("%s/req=%s", tc.level, tc.requested)
		t.Run(name, func(t *testing.T) {
			engine := newEngineStub(tc.level)
			defer engine.Close()
			evidence := newEvidenceStub()
			defer evidence.Close()

			cfg := testConfig(t, engine.URL, evidence.URL)
			logger := slog.Default()
			restStore := memory.NewTaskStore()
			grpcStore := memory.NewTaskStore()

			restSrv := handlers.New(agent.New(cfg, metrics.NewCollector("hub-rest-contract")),
				datasource.New(cfg), cfg, nil, restStore, logger, metrics.NewCollector("hub-rest-contract"))
			defer restSrv.Shutdown()
			grpcSrv := grpcserver.New(agent.New(cfg, metrics.NewCollector("hub-grpc-contract")),
				datasource.New(cfg), cfg, grpcStore, logger)
			defer grpcSrv.Shutdown()

			payload := map[string]any{"id_card": "110101199001011234", "patient_name": "张三"}

			router := gin.New()
			restSrv.RegisterRoutes(router)
			raw, _ := json.Marshal(map[string]any{
				"source": "ds_yibao", "operation": tc.requested, "payload": payload,
			})
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/v1/hub/dispatch", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			if w.Code != http.StatusAccepted {
				t.Fatalf("REST dispatch: expected 202, got %d: %s", w.Code, w.Body.String())
			}
			var restResp map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &restResp); err != nil {
				t.Fatalf("decode REST dispatch response: %v", err)
			}
			restTaskID, _ := restResp["task_id"].(string)
			if restTaskID == "" {
				t.Fatalf("REST dispatch returned no task_id: %s", w.Body.String())
			}

			payloadJSON, _ := json.Marshal([]map[string]any{payload})
			dispatchResp, err := grpcSrv.Dispatch(t.Context(), &pb.DispatchRequest{
				Source:      "ds_yibao",
				Operation:   tc.requested,
				PayloadJson: string(payloadJSON),
			})
			if err != nil {
				t.Fatalf("gRPC dispatch: %v", err)
			}

			restTask := waitForTerminal(t, restStore, restTaskID)
			grpcTask := waitForTerminal(t, grpcStore, dispatchResp.TaskId)

			if restTask.Operation != tc.want {
				t.Errorf("REST operation = %q, want %q (level %s)", restTask.Operation, tc.want, tc.level)
			}
			if grpcTask.Operation != tc.want {
				t.Errorf("gRPC operation = %q, want %q (level %s)", grpcTask.Operation, tc.want, tc.level)
			}
			if restTask.Operation != grpcTask.Operation {
				t.Errorf("entry points diverge: REST=%q gRPC=%q", restTask.Operation, grpcTask.Operation)
			}
		})
	}
}

// TestPipelineFailsClosedWithoutClassificationOnBothPaths proves neither entry point
// keeps an unclassified record on the egress path.
// TestPipelineFailsClosedWithoutClassificationOnBothPaths 证明两条入口都不会把
// 「未定级数据」留在出域链路上。
func TestPipelineFailsClosedWithoutClassificationOnBothPaths(t *testing.T) {
	engine := newEngineStub("")
	defer engine.Close()
	evidence := newEvidenceStub()
	defer evidence.Close()

	cfg := testConfig(t, engine.URL, evidence.URL)
	logger := slog.Default()

	restStore := memory.NewTaskStore()
	restMC := metrics.NewCollector("hub-rest-contract-failclosed")
	restSrv := handlers.New(agent.New(cfg, restMC), datasource.New(cfg), cfg, nil, restStore, logger, restMC)
	defer restSrv.Shutdown()
	grpcStore := memory.NewTaskStore()
	grpcSrv := grpcserver.New(agent.New(cfg, metrics.NewCollector("hub-grpc-contract-failclosed")), datasource.New(cfg), cfg, grpcStore, logger)
	defer grpcSrv.Shutdown()

	router := gin.New()
	restSrv.RegisterRoutes(router)
	raw, _ := json.Marshal(map[string]any{
		"source": "ds_yibao", "operation": "mask",
		"payload": []map[string]any{{"id_card": "110101199001011234"}},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/hub/dispatch", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("REST dispatch: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var restResp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &restResp)
	restTask := waitForTerminal(t, restStore, restResp["task_id"].(string))

	grpcResp, err := grpcSrv.Dispatch(t.Context(), &pb.DispatchRequest{
		Source:      "ds_yibao",
		Operation:   "mask",
		PayloadJson: `[{"id_card":"110101199001011234"}]`,
	})
	if err != nil {
		t.Fatalf("gRPC dispatch: %v", err)
	}
	grpcTask := waitForTerminal(t, grpcStore, grpcResp.TaskId)

	for _, task := range []*store.Task{restTask, grpcTask} {
		if task.Status != "failed" {
			t.Errorf("task %s must fail without a classification level, got status=%q stage=%q",
				task.ID, task.Status, task.Stage)
		}
	}
}

func waitForTerminal(t *testing.T, tasks store.TaskStore, id string) *store.Task {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		task, err := tasks.Get(id)
		if err == nil && (task.Status == "completed" || task.Status == "failed") {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach a terminal state within 20s", id)
	return nil
}
