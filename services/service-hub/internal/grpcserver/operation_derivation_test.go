package grpcserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
)

// engineLevelHandler returns a mock PrivShield engine that classifies every record
// at the given level; an empty level reproduces the malformed "no level" answer the
// pipeline must refuse.
// engineLevelHandler 返回一个把每条记录都定级为给定级别的模拟引擎；
// level 为空时复现「引擎跑完却没给出任何定级」的异常契约。
func engineLevelHandler(level string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		case "/v1/medical/process", "/v1/agent/process":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			records, _ := payload["records"].([]any)
			resp := map[string]any{
				"sanitized_data": records,
				"summary":        map[string]any{"total_records": len(records)},
			}
			if level != "" {
				resp["level"] = level
				resp["classification_report"] = []map[string]any{{"level": level, "level_id": level}}
				resp["summary"].(map[string]any)["overall_level"] = level
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}
}

// TestGRPCServer_ProcessTask_DerivesOperatorFromClassification pins the gRPC half of
// the P1-1 contract: the applied operator is a function of the engine's level, and a
// weaker caller request can never downgrade it.
// TestGRPCServer_ProcessTask_DerivesOperatorFromClassification 固化 P1-1 的 gRPC 半边：
// 生效算子完全由引擎定级推导，调用方请求只能上调、不能下调。
func TestGRPCServer_ProcessTask_DerivesOperatorFromClassification(t *testing.T) {
	cases := []struct {
		requested string
		level     string
		want      string
	}{
		{"", "L1", "none"},
		{"none", "L2", "mask"},
		{"mask", "L3", "k_anon"},
		{"classify", "L4", "dp"},
		{"none", "L5", "dp"},
		{"dp", "L2", "dp"}, // 只允许上调：请求更强时保留请求
	}

	for _, tc := range cases {
		name := tc.requested + "@" + tc.level
		t.Run(name, func(t *testing.T) {
			srv, mockServer, taskStore := setupTestGRPCServer(t, engineLevelHandler(tc.level))
			defer mockServer.Close()
			defer srv.Shutdown()

			task := &store.Task{
				ID:           "task-p11-" + strings.ReplaceAll(name, "/", "-"),
				Status:       "running",
				Stage:        "ingest",
				Source:       "ds_yibao",
				DatasourceID: "ds_yibao",
				APICode:      "api1_yibao",
				Operation:    tc.requested,
				PayloadJSON:  `[{"id_card":"110101199001011234"}]`,
				CreatedAt:    time.Now(),
			}
			if err := taskStore.Save(task); err != nil {
				t.Fatalf("save task: %v", err)
			}

			srv.processTask(task, task.Operation, task.PayloadJSON, "test-req")

			updated, err := taskStore.Get(task.ID)
			if err != nil {
				t.Fatalf("get task: %v", err)
			}
			if updated.Status != "completed" {
				t.Fatalf("task must complete, got status=%q stage=%q error=%q", updated.Status, updated.Stage, updated.Error)
			}
			if updated.Operation != tc.want {
				t.Errorf("applied operation = %q, want %q (level %s derives the floor, request %q cannot lower it)",
					updated.Operation, tc.want, tc.level, tc.requested)
			}
		})
	}
}

// TestGRPCServer_ProcessTask_FailsWhenEngineReturnsNoLevel proves the gRPC path has no
// silent default left: an unclassified answer stops the task instead of egressing it.
// TestGRPCServer_ProcessTask_FailsWhenEngineReturnsNoLevel 证明 gRPC 路径已无静默兜底：
// 引擎未定级时任务直接失败，绝不带着未知级别出域。
func TestGRPCServer_ProcessTask_FailsWhenEngineReturnsNoLevel(t *testing.T) {
	srv, mockServer, taskStore := setupTestGRPCServer(t, engineLevelHandler(""))
	defer mockServer.Close()
	defer srv.Shutdown()

	task := &store.Task{
		ID:           "task-p11-no-level",
		Status:       "running",
		Stage:        "ingest",
		Source:       "ds_yibao",
		DatasourceID: "ds_yibao",
		APICode:      "api1_yibao",
		Operation:    "mask",
		PayloadJSON:  `[{"id_card":"110101199001011234"}]`,
		CreatedAt:    time.Now(),
	}
	if err := taskStore.Save(task); err != nil {
		t.Fatalf("save task: %v", err)
	}

	srv.processTask(task, task.Operation, task.PayloadJSON, "test-req")

	updated, err := taskStore.Get(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated.Status != "failed" {
		t.Fatalf("missing level must fail the task, got status=%q stage=%q", updated.Status, updated.Stage)
	}
	if !strings.Contains(updated.Error, "no security level") {
		t.Errorf("task error must name the missing level, got %q", updated.Error)
	}
}

// TestGRPCServer_ExecuteLeasedTask_DerivesOperator checks the PG lease worker applies
// exactly the same derivation as the REST and gRPC entry points.
// TestGRPCServer_ExecuteLeasedTask_DerivesOperator 校验 PG 租约工作器与 REST / gRPC
// 入口套用完全一致的定级推导，三条路径不产生第二套口径。
func TestGRPCServer_ExecuteLeasedTask_DerivesOperator(t *testing.T) {
	srv, mockServer, _ := setupTestGRPCServer(t, engineLevelHandler("L4"))
	defer mockServer.Close()
	defer srv.Shutdown()

	task := &store.Task{
		ID:           "leased-p11",
		Status:       "running",
		Stage:        "ingest",
		Source:       "ds_yibao",
		DatasourceID: "ds_yibao",
		APICode:      "api1_yibao",
		Operation:    "none",
		PayloadJSON:  `[{"id_card":"110101199001011234"}]`,
		CreatedAt:    time.Now(),
	}

	failure := srv.executeLeasedTask(context.Background(), task)
	if failure != nil {
		t.Fatalf("leased task must succeed, got failure %+v", failure)
	}
	if task.Operation != "dp" {
		t.Errorf("leased task operation = %q, want dp (L4 floor must beat the caller's none)", task.Operation)
	}
}
