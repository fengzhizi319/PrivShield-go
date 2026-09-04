package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
)

// TestAgentClient tests the agent Client methods against a mock HTTP server.
// TestAgentClient 启动一个本地 Mock HTTP 服务器，模拟 PrivShield Agent 的各核心 REST 端点，
// 并对 Client 的 Health, Classify, Mask 与 MaskRecord 方法进行全流程单元测试。
func TestAgentClient(t *testing.T) {
	// 1. 构建 Mock Upstream Agent HTTP 服务器
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/health":
			// 模拟 Agent 存活与命名空间探针
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":    "ok",
				"namespace": "default",
			})
		case "/v1/dynclassification/eval_record":
			// 模拟动态分类分级三层漏斗评估
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"level": "L3",
				"tags":  []string{"PII", "Healthcare"},
				"fields": map[string]any{
					"name": map[string]any{"level": "L3", "category": "PII"},
				},
			})
		case "/v1/privacy/mask":
			// 模拟字段级脱敏
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": "张**",
				"field":  body["field_name"],
			})
		case "/v1/privacy/mask_record":
			// 模拟整行记录脱敏
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"name":  "张**",
					"phone": "138****0000",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	// 2. 将 PRIVACY_AGENT_URLS 指向 mockServer 并初始化 Client
	t.Setenv("PRIVACY_AGENT_URLS", mockServer.URL)
	cfg := config.Load()

	client, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New(cfg, nil) failed: %v", err)
	}
	if client == nil {
		t.Fatal("New(cfg, nil) returned nil")
	}

	ctx := context.Background()

	// 3. 测试 Health 端点健康探测
	t.Run("Health", func(t *testing.T) {
		res, err := client.Health(ctx)
		if err != nil {
			t.Fatalf("Health() failed: %v", err)
		}
		if res["status"] != "ok" {
			t.Errorf("Health() got status %v, want ok", res["status"])
		}
	})

	// 4. 测试 Classify 动态分类分级评估
	t.Run("Classify", func(t *testing.T) {
		payload := map[string]any{
			"name":  "张三",
			"phone": "13800138000",
		}
		res, err := client.Classify(ctx, []map[string]any{payload})
		if err != nil {
			t.Fatalf("Classify() failed: %v", err)
		}
		if res["level"] != "L3" {
			t.Errorf("Classify() got level %v, want L3", res["level"])
		}
	})

	// 5. 测试 Mask 字段级脱敏
	t.Run("Mask", func(t *testing.T) {
		payload := map[string]any{
			"field_name": "name",
			"value":      "张三",
		}
		res, err := client.Mask(ctx, payload)
		if err != nil {
			t.Fatalf("Mask() failed: %v", err)
		}
		if res["result"] != "张**" {
			t.Errorf("Mask() got result %v, want 张**", res["result"])
		}
	})

	// 6. 测试 MaskRecord 整条记录脱敏
	t.Run("MaskRecord", func(t *testing.T) {
		record := map[string]string{
			"name":  "张三",
			"phone": "13800138000",
		}
		res, err := client.MaskRecord(ctx, record)
		if err != nil {
			t.Fatalf("MaskRecord() failed: %v", err)
		}
		resultMap, ok := res["result"].(map[string]any)
		if !ok || resultMap["name"] != "张**" {
			t.Errorf("MaskRecord() unexpected result: %+v", res)
		}
	})
}
