// Package handlers_test contains unit and API tests for the HTTP REST handlers of datasource-mgr.
// Package handlers_test 包含 datasource-mgr 模块 HTTP REST 路由与处理器实现的单元测试套件。
//
// 测试覆盖：
// 1. 存活健康探针（TestHealth）；
// 2. 专用模拟数据源接口 API 1 ~ 4（TestAPI1YibaoData, TestAPI2KangyangData, TestAPI3Mock3Data, TestAPI4Mock4Data）；
// 3. 数据源资产列表查询（TestListDataSources）；
// 4. 单个数据源详情查询与 404 错误处理（TestGetDataSource, TestGetDataSourceNotFound）；
// 5. 数据源记录动态分页采样（TestGetDataSourceRecords）；
// 6. 数据源连通性测试（TestTestConnection）；
// 7. Schema 元数据探查（TestGetMetadata）；
// 8. 访问审计日志（TestGetAccessAudit）；
// 9. 模拟数据重新播种端点（TestSeedDataSources）。
// 10. P0-4 数据完整性严格模式：样本全量可读 + 损坏行拒绝静音降级（TestLoadCSVRecords_*Strict*）。
package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	pkgconfig "github.com/fengzhizi319/PrivShield/pkg/config"
	"github.com/fengzhizi319/PrivShield/pkg/metrics"
	"github.com/fengzhizi319/PrivShield/services/datasource-mgr/internal/config"
	"github.com/fengzhizi319/PrivShield/services/datasource-mgr/internal/models"
)

// newTestRouter constructs an in-memory Gin test engine with all routes registered.
// newTestRouter 构造并初始化用于单元测试的纯内存 Gin 引擎实例：
// 1. 切换 Gin 为 TestMode 模式；
// 2. 构造默认 Config 与 text/debug 格式的 Logger；
// 3. 实例化 Server 并调用 RegisterRoutes 装配全部中间件与业务路由。
func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	cfg := config.Load()
	logger := pkgconfig.SetupLogger("text", "debug")
	server := New(cfg, logger, metrics.NewCollector("datasource-mgr"))

	r := gin.New()
	server.RegisterRoutes(r)
	return r
}

// TestHealth verifies that GET /api/health returns 200 OK and correct service identifiers.
// TestHealth 验证存活健康探针端点：
// 1. 发送 HTTP GET /api/health 请求；
// 2. 断言 HTTP 状态码为 200 OK；
// 3. 验证 JSON 响应体中的 backend 为 "ok" 且 via 标识为 "datasource-mgr"。
func TestHealth(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" || resp["via"] != "datasource-mgr" {
		t.Errorf("unexpected health response: %+v", resp)
	}
}

// TestAPI1YibaoData verifies dedicated API 1 for healthcare/settlement dataset.
// TestAPI1YibaoData 验证专用 API 1（医保就医与结算模拟数据）：
// 1. 请求 GET /api/v1/yibao?limit=5；
// 2. 断言状态码 200 OK；
// 3. 校验返回的 SourceID 为 "ds_yibao"，且 Limit 为 5。
func TestAPI1YibaoData(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("GET", "/api/v1/yibao?limit=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp models.DataQueryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.SourceID != "ds_yibao" || resp.Limit != 5 {
		t.Errorf("unexpected yibao response: %+v", resp)
	}
}

// TestAPI2KangyangData verifies dedicated API 2 for elderly care dataset.
// TestAPI2KangyangData 验证专用 API 2（康养体检与慢病管理模拟数据）：
// 1. 请求 GET /api/v1/kangyang?limit=5；
// 2. 断言状态码 200 OK；
// 3. 校验返回的 SourceID 为 "ds_kangyang"，且 Limit 为 5。
func TestAPI2KangyangData(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("GET", "/api/v1/kangyang?limit=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp models.DataQueryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.SourceID != "ds_kangyang" || resp.Limit != 5 {
		t.Errorf("unexpected kangyang response: %+v", resp)
	}
}

// TestAPI3Mock3Data verifies dedicated API 3 for reserved municipal dataset 3.
// TestAPI3Mock3Data 验证专用 API 3（预留政务数据源 3）：
// 1. 请求 GET /api/v1/mock3；
// 2. 断言状态码 200 OK；
// 3. 校验 SourceID 为 "ds_mock3" 且非空记录。
func TestAPI3Mock3Data(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("GET", "/api/v1/mock3", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp models.DataQueryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.SourceID != "ds_mock3" || len(resp.Records) == 0 {
		t.Errorf("unexpected mock3 response: %+v", resp)
	}
}

// TestAPI4Mock4Data verifies dedicated API 4 for reserved municipal dataset 4.
// TestAPI4Mock4Data 验证专用 API 4（预留政务数据源 4）：
// 1. 请求 GET /api/v1/mock4；
// 2. 断言状态码 200 OK；
// 3. 校验 SourceID 为 "ds_mock4" 且非空记录。
func TestAPI4Mock4Data(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("GET", "/api/v1/mock4", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp models.DataQueryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.SourceID != "ds_mock4" || len(resp.Records) == 0 {
		t.Errorf("unexpected mock4 response: %+v", resp)
	}
}

// TestListDataSources verifies GET /api/datasources returns the full mock directory.
// TestListDataSources 验证数据源列表查询端点：
// 1. 请求 GET /api/datasources；
// 2. 断言返回状态码 200 OK；
// 3. 校验返回的数据源总数至少为 2 个（包含医保与康养）。
func TestListDataSources(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("GET", "/api/datasources", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["total"].(float64) < 2 {
		t.Errorf("expected at least 2 datasources, got: %+v", resp)
	}
}

// TestGetDataSource verifies GET /api/datasources/:id returns metadata for a registered datasource.
// TestGetDataSource 验证单个数据源元数据查询端点：
// 1. 请求 GET /api/datasources/ds_yibao；
// 2. 断言状态码 200 OK 且返回的 ID 精确匹配 "ds_yibao"。
func TestGetDataSource(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("GET", "/api/datasources/ds_yibao", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var ds models.MockDataSource
	_ = json.Unmarshal(w.Body.Bytes(), &ds)
	if ds.ID != "ds_yibao" {
		t.Errorf("unexpected datasource: %+v", ds)
	}
}

// TestGetDataSourceNotFound verifies GET /api/datasources/:id returns 404 for unknown datasource.
// TestGetDataSourceNotFound 验证查询不存在的数据源时返回 404 Not Found 错误。
func TestGetDataSourceNotFound(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("GET", "/api/datasources/non_existent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestGetDataSourceRecords verifies GET /api/datasources/:id/records returns paginated data rows.
// TestGetDataSourceRecords 验证通用数据源分页采样端点：
// 1. 请求 GET /api/datasources/ds_yibao/records?limit=3；
// 2. 验证响应状态码 200 OK 且 datasource_id 为 "ds_yibao"。
func TestGetDataSourceRecords(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("GET", "/api/datasources/ds_yibao/records?limit=3", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["datasource_id"] != "ds_yibao" {
		t.Errorf("unexpected records response: %+v", resp)
	}
}

// TestTestConnection verifies POST /api/datasources/:id/test returns success and latency.
// TestTestConnection 验证数据源连通性测试端点：
// 1. 发送 POST /api/datasources/ds_kangyang/test 请求；
// 2. 校验返回成功标识 Success=true 且 DataSourceID 匹配。
func TestTestConnection(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("POST", "/api/datasources/ds_kangyang/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp models.ConnectionTestResult
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Success || resp.DataSourceID != "ds_kangyang" {
		t.Errorf("unexpected connection test response: %+v", resp)
	}
}

// TestGetMetadata verifies GET /api/datasources/:id/metadata returns table schemas.
// TestGetMetadata 验证数据源 Schema 元数据探查端点：
// 1. 请求 GET /api/datasources/ds_yibao/metadata；
// 2. 校验返回状态码 200 OK，包含数据表清单且 DataSourceID 为 "ds_yibao"。
func TestGetMetadata(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("GET", "/api/datasources/ds_yibao/metadata", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp models.MetadataResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.DataSourceID != "ds_yibao" || len(resp.Tables) == 0 {
		t.Errorf("unexpected metadata response: %+v", resp)
	}
}

// TestGetAccessAudit verifies GET /api/datasources/:id/audit returns mock audit log records.
// TestGetAccessAudit 验证模拟数据源访问审计日志查询端点。
func TestGetAccessAudit(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("GET", "/api/datasources/ds_yibao/audit", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// TestSeedDataSources verifies POST /api/datasources/seed returns seed initialization message.
// TestSeedDataSources 验证模拟数据源重新初始化/播种端点返回 200 OK。
func TestSeedDataSources(t *testing.T) {
	r := newTestRouter()
	req, _ := http.NewRequest("POST", "/api/datasources/seed", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestLoadCSVRecords_PathTraversal(t *testing.T) {
	// Absolute path attempts should be rejected by the allow-list / basename logic.
	malicious := []string{
		"../../../etc/passwd.csv",
		"..\\..\\..\\etc\\passwd.csv",
		"/etc/passwd.csv",
		"yibao.txt",
		"unknown.csv",
		"yibao.csv/../../etc/passwd.csv",
	}

	for _, name := range malicious {
		if _, _, err := LoadCSVRecords(name, 10, 0); err == nil {
			t.Errorf("expected error for path traversal attempt %q, got nil", name)
		}
	}
}

func TestLoadCSVRecords_AllowedFiles(t *testing.T) {
	// The two official mock datasets must continue to load successfully.
	for _, name := range []string{"yibao.csv", "kangyang.csv"} {
		records, total, err := LoadCSVRecords(name, 5, 0)
		if err != nil {
			t.Fatalf("unexpected error loading %s: %v", name, err)
		}
		if total <= 0 || len(records) == 0 {
			t.Errorf("expected records for %s, got total=%d len=%d", name, total, len(records))
		}
	}
}

// TestLoadCSVRecords_ShippedSamplesSurviveStrictMode 保证 P0-4 的「默认严格」不会误伤生产：
// 官方自带的两份样本数据在严格完整性模式下必须整份读完（记录数与非严格模式一致），
// 否则 DATASOURCE_MGR_STRICT_STORAGE 的默认值就等于把已有查询接口打挂。
// TestLoadCSVRecords_ShippedSamplesSurviveStrictMode pins the fail-closed default: the shipped
// mock datasets must parse completely under strict integrity, otherwise defaulting
// DATASOURCE_MGR_STRICT_STORAGE=true to would break every existing sample query.
func TestLoadCSVRecords_ShippedSamplesSurviveStrictMode(t *testing.T) {
	defer SetStrictDataIntegrity(strictDataIntegrity.Load())

	SetStrictDataIntegrity(false)
	baseline := map[string]int{}
	for _, name := range []string{"yibao.csv", "kangyang.csv"} {
		_, total, err := LoadCSVRecords(name, 1000, 0)
		if err != nil {
			t.Fatalf("lenient load of %s failed: %v", name, err)
		}
		baseline[name] = total
	}

	SetStrictDataIntegrity(true)
	for _, name := range []string{"yibao.csv", "kangyang.csv"} {
		records, total, err := LoadCSVRecords(name, 1000, 0)
		if err != nil {
			t.Fatalf("strict load of %s failed: %v", name, err)
		}
		if total != baseline[name] || len(records) != baseline[name] {
			t.Errorf("strict mode lost rows for %s: total=%d len=%d, want %d", name, total, len(records), baseline[name])
		}
	}
}

// TestLoadCSVRecords_StrictModeAbortsOnCorruptRow 是 P0-4 的核心断言：
// 损坏行在严格模式下必须上抛为错误（旧实现 `continue` 静音丢弃，调用方拿到缺行数据却返回 200）；
// 显式关闭严格模式时才允许降级为静音跳过。
// TestLoadCSVRecords_StrictModeAbortsOnCorruptRow asserts the no-silent-degradation contract: a
// malformed line aborts the whole load under strict mode and is only skipped when strict storage is
// explicitly turned off.
func TestLoadCSVRecords_StrictModeAbortsOnCorruptRow(t *testing.T) {
	defer SetStrictDataIntegrity(strictDataIntegrity.Load())

	// findCSVFile resolves allow-listed names against relative candidate dirs, so a per-test
	// working directory lets us feed a fixture without touching the repository samples.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "samples"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	corrupt := "id,name\n1,\"unclosed\n2,ok\n"
	if err := os.WriteFile(filepath.Join(dir, "samples", "yibao.csv"), []byte(corrupt), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Chdir(dir)

	SetStrictDataIntegrity(true)
	if _, _, err := LoadCSVRecords("yibao.csv", 10, 0); err == nil {
		t.Fatal("strict mode must abort on a corrupt row, got nil error")
	} else if !strings.Contains(err.Error(), "malformed record") {
		t.Errorf("expected malformed-record error, got %v", err)
	}

	SetStrictDataIntegrity(false)
	records, total, err := LoadCSVRecords("yibao.csv", 10, 0)
	if err != nil {
		t.Fatalf("lenient mode should skip the corrupt row, got %v", err)
	}
	if total >= 2 || len(records) >= 2 {
		t.Fatalf("expected the corrupt row to be silently dropped, got total=%d", total)
	}
}
