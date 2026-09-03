// Package rest — REST API 路由集成测试。
//
// 覆盖全部端点的正常路径 + 错误信封格式校验。
// URL 路径与 Python engine 完全对齐。
package rest

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/security"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/service"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupRouter(t *testing.T) (*gin.Engine, *service.PrivacyService) {
	t.Helper()
	svc, err := service.NewPrivacyService(service.DefaultConfig())
	if err != nil {
		t.Fatalf("NewPrivacyService: %v", err)
	}
	r := gin.New()
	RegisterRoutes(r, svc)
	return r, svc
}

func doJSON(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func doMultipart(r *gin.Engine, path, fieldName, filename, fileContent, operation, params string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, _ := writer.CreateFormFile(fieldName, filename)
	part.Write([]byte(fileContent))

	if operation != "" {
		_ = writer.WriteField("operation", operation)
	}
	if params != "" {
		_ = writer.WriteField("params", params)
	}
	_ = writer.Close()

	req := httptest.NewRequest("POST", path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func parseEnvelope(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v, body: %s", err, w.Body.String())
	}
	return env
}

// ──────────────────────────────────────────────
// 健康检查
// ──────────────────────────────────────────────

func TestHealth(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "GET", "/health", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestLivez(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "GET", "/livez", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestReadyz(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "GET", "/readyz", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ──────────────────────────────────────────────
// 掩码端点
// ──────────────────────────────────────────────

func TestMask(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/mask", map[string]string{
		"field": "phone", "value": "13812345678", "type": "phone",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMask_InvalidArg(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/mask", map[string]string{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	env := parseEnvelope(t, w)
	if env["code"] != "INVALID_ARGUMENT" {
		t.Errorf("expected code=INVALID_ARGUMENT, got %v", env["code"])
	}
}

func TestMaskRecord(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/mask/record", map[string]any{
		"record": map[string]string{"phone": "13812345678", "email": "test@example.com"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMaskBatch(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/mask/batch", map[string]any{
		"records": []map[string]string{
			{"phone": "13812345678"},
			{"email": "test@example.com"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ──────────────────────────────────────────────
// 差分隐私端点
// ──────────────────────────────────────────────

func TestDPNoisyCount(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/dp/noisy_count", map[string]any{
		"count": 100, "epsilon": 0.5,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDPNoisySum(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/dp/noisy_sum", map[string]any{
		"values": []float64{1, 2, 3}, "epsilon": 0.5, "sensitivity": 1.0,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDPNoisyMean(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/dp/noisy_mean", map[string]any{
		"values": []float64{1, 2, 3}, "epsilon": 0.5, "delta": 1e-5, "clip_bound": 1.0,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ──────────────────────────────────────────────
// LDP 端点
// ──────────────────────────────────────────────

func TestLDPRandomizedResponse(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/ldp/randomized_response", map[string]any{
		"value": true, "epsilon": 1.0,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestLDPOrr(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/ldp/orr", map[string]any{
		"value": 1, "epsilon": 1.0, "domain_size": 5,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ──────────────────────────────────────────────
// K-匿名端点
// ──────────────────────────────────────────────

func TestKAnonymize(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/k_anonymize", map[string]any{
		"records": []map[string]string{
			{"age": "25", "zip": "10001"},
			{"age": "26", "zip": "10002"},
			{"age": "27", "zip": "10003"},
			{"age": "28", "zip": "10004"},
		},
		"qi_fields": []string{"age", "zip"},
		"k":         2,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestKAnonymizeTable_Mondrian(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/k_anonymize_table", map[string]any{
		"records": []map[string]string{
			{"name": "Tom", "age": "25", "salary": "5000"},
			{"name": "Jerry", "age": "30", "salary": "6000"},
			{"name": "Spike", "age": "35", "salary": "7000"},
			{"name": "Tyke", "age": "28", "salary": "5500"},
		},
		"qi_cols": []string{"age", "salary"},
		"k":       2,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["equivalence_classes_count"] == nil {
		t.Error("missing equivalence_classes_count")
	}
}

// ──────────────────────────────────────────────
// Agent 处理端点
// ──────────────────────────────────────────────

func TestAgentProcess_Basic(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/agent/process", map[string]any{
		"records": []map[string]string{
			{"phone": "13812345678", "email": "test@example.com"},
		},
		"api_code": "test_api",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["sanitized_data"] == nil {
		t.Error("missing sanitized_data")
	}
	if resp["classification_report"] == nil {
		t.Error("missing classification_report")
	}
	summary, ok := resp["summary"].(map[string]any)
	if !ok {
		t.Fatal("missing summary")
	}
	if summary["input_hash"] == nil {
		t.Error("missing input_hash")
	}
	if summary["api_code"] != "test_api" {
		t.Errorf("expected api_code=test_api, got %v", summary["api_code"])
	}
}

// ──────────────────────────────────────────────
// 运维诊断端点
// ──────────────────────────────────────────────

func TestDiagnostics_Basic(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "GET", "/v1/ops/diagnostics", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
	if resp["service"] == nil {
		t.Error("missing service info")
	}
}

// TestDiagnostics_NerCapability 断言 P1-3 的诚实能力口径：默认构建装配的是正则
// NER 桩（ONNX 模型未交付），因此 ner_available 必须存在且为 false。
func TestDiagnostics_NerCapability(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "GET", "/v1/ops/diagnostics", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	raw, ok := resp["ner_available"]
	if !ok {
		t.Fatal("missing ner_available field in /ops/diagnostics payload")
	}
	avail, isBool := raw.(bool)
	if !isBool {
		t.Fatalf("ner_available = %T (%v), want bool", raw, raw)
	}
	if avail {
		t.Error("ner_available must be false: Layer 2 是正则桩，ONNX NER 模型未交付")
	}

	backend, _ := resp["ner_backend"].(string)
	if backend != "rule-based-ner" {
		t.Errorf("ner_backend = %q, want %q", backend, "rule-based-ner")
	}

	if engines, ok := resp["engines"].(map[string]any); ok {
		if ner, ok := engines["ner"].(map[string]any); ok {
			if ner["available"] == true {
				t.Error("engines.ner.available must not claim true while the regex stand-in is wired")
			}
		}
		if llm, ok := engines["llm"].(map[string]any); ok {
			// Layer-3 默认关闭；即便启用，available 也必须来自真实探测而非写死常量。
			if llm["determined_by"] == nil || llm["determined_by"] == "" {
				t.Error("engines.llm.determined_by must name the real source of truth")
			}
			if llm["payload_deidentified"] != true {
				t.Error("engines.llm.payload_deidentified must be true: Layer-3 only ships de-identified fingerprints (P0-5)")
			}
		}
	}
}

// ──────────────────────────────────────────────
// 查询混淆端点
// ──────────────────────────────────────────────

func TestObfuscate(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/qol/obfuscate", map[string]any{
		"query": "SELECT * FROM users", "num_decoys": 3, "domain": "sql",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ──────────────────────────────────────────────
// HMAC 散列端点
// ──────────────────────────────────────────────

func TestHashHMAC(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/privacy/hash", map[string]any{
		"value": "test", "salt": "mysalt",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ──────────────────────────────────────────────
// 预算端点
// ──────────────────────────────────────────────

func TestBudget(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "GET", "/v1/privacy/budget", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ──────────────────────────────────────────────
// 医疗端点
// ──────────────────────────────────────────────

func TestMedicalSanitize(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/medical/sanitize", map[string]any{
		"record": map[string]string{"patient_name": "张三", "diagnosis": "感冒"},
		"domain": "yibao",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ──────────────────────────────────────────────
// 分类端点
// ──────────────────────────────────────────────

func TestClassify(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "POST", "/v1/dynclassification/classify", map[string]any{
		"field": "phone", "value": "13812345678",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestClassify_AliasRoutes 验证 /api/v1/dynclassification/* 别名路由已注册并可正常访问（SEC-09/SEC-11 完整覆盖）。
func TestClassify_AliasRoutes(t *testing.T) {
	r, _ := setupRouter(t)
	cases := []struct {
		path   string
		method string
		body   any
	}{
		{"/api/v1/dynclassification/classify", "POST", map[string]any{"field": "phone", "value": "13812345678"}},
		{"/api/v1/dynclassification/classify/batch", "POST", map[string]any{"records": []map[string]any{{"phone": "13812345678"}}}},
		{"/api/v1/dynclassification/eval_record", "POST", map[string]any{"record": map[string]any{"phone": "13812345678"}}},
		{"/api/v1/dynclassification/profiles/reload", "POST", nil},
	}
	for _, tc := range cases {
		w := doJSON(r, tc.method, tc.path, tc.body)
		if w.Code != http.StatusOK {
			t.Fatalf("[%s] expected 200, got %d: %s", tc.path, w.Code, w.Body.String())
		}
	}
}

// TestProfileRecommend_AliasRoute 验证 /api/v1/privacy/profile/recommend 别名路由已注册（SEC-09 完整覆盖）。
func TestProfileRecommend_AliasRoute(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "GET", "/api/v1/privacy/profile/recommend", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ──────────────────────────────────────────────
// Profile 推荐端点
// ──────────────────────────────────────────────

func TestProfileRecommend(t *testing.T) {
	r, _ := setupRouter(t)
	w := doJSON(r, "GET", "/v1/privacy/profile/recommend", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ──────────────────────────────────────────────
// P0: Agent & Medical 统一流水线测试
// ──────────────────────────────────────────────

func TestAgentProcess(t *testing.T) {
	r, _ := setupRouter(t)
	payload := map[string]any{
		"records": []map[string]any{
			{
				"name":       "张三",
				"id_card_no": "110101199001011234",
				"phone":      "13800138000",
				"diagnosis":  "高血压",
			},
		},
		"api_code":      "api1_yibao",
		"datasource_id": "ds_yibao",
	}

	for _, path := range []string{"/v1/agent/process", "/agent/process", "/api/v1/agent/process"} {
		w := doJSON(r, "POST", path, payload)
		if w.Code != http.StatusOK {
			t.Fatalf("[%s] expected 200, got %d: %s", path, w.Code, w.Body.String())
		}
		var res struct {
			ClassificationReport []map[string]any    `json:"classification_report"`
			SanitizedData        []map[string]string `json:"sanitized_data"`
			Summary              map[string]any      `json:"summary"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("[%s] unmarshal response: %v", path, err)
		}
		if len(res.SanitizedData) != 1 {
			t.Fatalf("[%s] expected 1 sanitized record, got %d", path, len(res.SanitizedData))
		}
		if res.Summary["input_hash"] == "" || res.Summary["output_hash"] == "" {
			t.Fatalf("[%s] expected non-empty hashes in summary", path)
		}
	}
}

func TestMedicalProcess(t *testing.T) {
	r, _ := setupRouter(t)
	payload := map[string]any{
		"records": []map[string]any{
			{
				"name":       "李四",
				"id_card_no": "110101199001011234",
				"diagnosis":  "糖尿病",
			},
		},
	}
	for _, path := range []string{"/v1/medical/process", "/medical/process", "/api/v1/medical/process"} {
		w := doJSON(r, "POST", path, payload)
		if w.Code != http.StatusOK {
			t.Fatalf("[%s] expected 200, got %d: %s", path, w.Code, w.Body.String())
		}
	}
}

// ──────────────────────────────────────────────
// P1: 运维诊断端点测试
// ──────────────────────────────────────────────

func TestOpsDiagnostics(t *testing.T) {
	r, _ := setupRouter(t)
	for _, path := range []string{"/v1/ops/diagnostics", "/ops/diagnostics", "/api/v1/ops/diagnostics"} {
		w := doJSON(r, "GET", path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("[%s] expected 200, got %d: %s", path, w.Code, w.Body.String())
		}
		var diag map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &diag); err != nil {
			t.Fatalf("[%s] unmarshal: %v", path, err)
		}
		if diag["status"] != "ok" {
			t.Fatalf("[%s] expected status ok, got %v", path, diag["status"])
		}
		svc, ok := diag["service"].(map[string]any)
		if !ok || svc["engine"] != "go" {
			t.Fatalf("[%s] expected engine go, got %v", path, svc)
		}
	}
}

// ──────────────────────────────────────────────
// P1: 文件上传脱敏处理测试
// ──────────────────────────────────────────────

func TestProcessFile_CSV(t *testing.T) {
	r, _ := setupRouter(t)
	csvData := "name,phone,id_card_no\n张三,13800138000,110101199001011234\n李四,13900139000,110101199202022345"

	for _, path := range []string{"/v1/privacy/process_file", "/privacy/process_file", "/api/v1/privacy/process_file"} {
		w := doMultipart(r, path, "file", "test.csv", csvData, "mask_dataframe", `{"columns":["phone","id_card_no"]}`)
		if w.Code != http.StatusOK {
			t.Fatalf("[%s] expected 200, got %d: %s", path, w.Code, w.Body.String())
		}
		var res map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("[%s] unmarshal: %v", path, err)
		}
		if res["rows_in"] != float64(2) || res["rows_out"] != float64(2) {
			t.Fatalf("[%s] unexpected rows_in / rows_out: %v", path, res)
		}
	}
}

func TestProcessFile_JSON(t *testing.T) {
	r, _ := setupRouter(t)
	jsonData := `[{"name":"王五","phone":"13700137000"},{"name":"赵六","phone":"13600136000"}]`

	w := doMultipart(r, "/v1/privacy/process_file", "file", "test.json", jsonData, "mask_dataframe", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res["rows_in"] != float64(2) {
		t.Fatalf("expected rows_in 2, got %v", res["rows_in"])
	}
}

func TestProcessFile_KAnonymize(t *testing.T) {
	r, _ := setupRouter(t)
	csvData := "age,zipcode,disease\n25,100010,flu\n26,100010,fever\n45,200020,cough\n46,200020,cold"

	w := doMultipart(r, "/v1/privacy/process_file", "file", "test.csv", csvData, "k_anonymize", `{"qi_cols":["age","zipcode"],"k":2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res["rows_out"] != float64(4) {
		t.Fatalf("expected rows_out 4, got %v", res["rows_out"])
	}
}

// ──────────────────────────────────────────────
// P1: 表级与 DataFrame K-匿名测试
// ──────────────────────────────────────────────

func TestKAnonymizeTable(t *testing.T) {
	r, _ := setupRouter(t)
	payload := map[string]any{
		"records": []map[string]string{
			{"age": "25", "zipcode": "100010", "gender": "M"},
			{"age": "26", "zipcode": "100010", "gender": "F"},
			{"age": "45", "zipcode": "200020", "gender": "M"},
			{"age": "47", "zipcode": "200020", "gender": "F"},
		},
		"qi_cols": []string{"age", "zipcode"},
		"k":       2,
	}

	for _, path := range []string{"/v1/privacy/k_anonymize/table", "/api/v1/kano/table"} {
		w := doJSON(r, "POST", path, payload)
		if w.Code != http.StatusOK {
			t.Fatalf("[%s] expected 200, got %d: %s", path, w.Code, w.Body.String())
		}
		var res map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		if res["k"] != float64(2) {
			t.Fatalf("[%s] expected k 2, got %v", path, res["k"])
		}
	}
}

func TestKAnonymizeDataFrame(t *testing.T) {
	r, _ := setupRouter(t)
	payload := map[string]any{
		"records": []map[string]any{
			{"age": 25, "zipcode": "100010", "salary": 10000},
			{"age": 26, "zipcode": "100010", "salary": 12000},
			{"age": 50, "zipcode": "200020", "salary": 30000},
			{"age": 52, "zipcode": "200020", "salary": 35000},
		},
		"qi_cols": []string{"age", "zipcode"},
		"k":       2,
	}

	for _, path := range []string{"/v1/privacy/k_anonymize/dataframe", "/api/v1/kano/dataframe"} {
		w := doJSON(r, "POST", path, payload)
		if w.Code != http.StatusOK {
			t.Fatalf("[%s] expected 200, got %d: %s", path, w.Code, w.Body.String())
		}
	}
}

// ──────────────────────────────────────────────
// P0: API Key 认证与权限校验测试
// ──────────────────────────────────────────────

func TestAuth_Enabled_Denied_And_Allowed(t *testing.T) {
	os.Setenv("AGENT_AUTH_ENABLED", "true")
	os.Setenv("AGENT_AUTH_INTERNAL_API_KEYS", "secret-admin-key:admin:*;limited-key:user:privacy:mask")
	security.ResetSettings()
	defer func() {
		os.Unsetenv("AGENT_AUTH_ENABLED")
		os.Unsetenv("AGENT_AUTH_INTERNAL_API_KEYS")
		security.ResetSettings()
	}()

	r, _ := setupRouter(t)

	// 1. 未带 Authorization 请求 -> 401 UNAUTHENTICATED
	w1 := doJSON(r, "POST", "/v1/privacy/mask", map[string]string{
		"field": "phone", "value": "13800000000", "type": "phone",
	})
	if w1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w1.Code, w1.Body.String())
	}

	// 2. 带无效 Token -> 401 UNAUTHENTICATED
	req2 := httptest.NewRequest("POST", "/v1/privacy/mask", bytes.NewBufferString(`{"field":"phone","value":"13800000000","type":"phone"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer invalid-token")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w2.Code, w2.Body.String())
	}

	// 3. 带 limited-key 访问仅限隐私掩码的端点 -> 200 OK
	req3 := httptest.NewRequest("POST", "/v1/privacy/mask", bytes.NewBufferString(`{"field":"phone","value":"13800000000","type":"phone"}`))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Authorization", "Bearer limited-key")
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w3.Code, w3.Body.String())
	}

	// 4. 带 limited-key 访问超出 scope 的端点 (/v1/agent/process) -> 403 FORBIDDEN
	req4 := httptest.NewRequest("POST", "/v1/agent/process", bytes.NewBufferString(`{"records":[{"name":"张三"}]}`))
	req4.Header.Set("Content-Type", "application/json")
	req4.Header.Set("Authorization", "Bearer limited-key")
	w4 := httptest.NewRecorder()
	r.ServeHTTP(w4, req4)
	if w4.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w4.Code, w4.Body.String())
	}

	// 5. 带 admin-key 访问 -> 200 OK
	req5 := httptest.NewRequest("POST", "/v1/agent/process", bytes.NewBufferString(`{"records":[{"name":"张三"}]}`))
	req5.Header.Set("Content-Type", "application/json")
	req5.Header.Set("Authorization", "Bearer secret-admin-key")
	w5 := httptest.NewRecorder()
	r.ServeHTTP(w5, req5)
	if w5.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w5.Code, w5.Body.String())
	}

	// 6. 健康探针端点免认证 -> 200 OK
	w6 := doJSON(r, "GET", "/health", nil)
	if w6.Code != http.StatusOK {
		t.Fatalf("expected 200 for health endpoint, got %d", w6.Code)
	}
}
