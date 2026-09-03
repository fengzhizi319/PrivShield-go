// Package handlers_test contains unit and API tests for the HTTP REST handlers of datasource-mgr.
// Package handlers_test 包含 datasource-mgr 模块 HTTP REST 路由与处理器实现的单元测试套件。
//
// 测试覆盖：
// 1. 存活健康探针（TestHealth）；
// 2. 数据源资产列表查询（TestListDataSources）；
// 3. 单个数据源详情查询与 404 错误处理（TestGetDataSource, TestGetDataSourceNotFound）；
// 4. 数据源连通性测试（TestTestConnection）；
// 5. Schema 元数据探查（TestGetMetadata）；
// 6. 访问审计日志（TestGetAccessAudit）；
// 7. 模拟数据重新播种端点（TestSeedDataSources）。
// 8. 按身份证号查询单条记录（TestGetRecordByIDCard）。
// 9. P0-4 数据完整性严格模式：样本全量可读 + 损坏行拒绝静音降级（TestLoadCSVRecords_*Strict*）。
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

	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/internal/models"
)

// newTestRouter constructs an in-memory Gin test engine with all routes registered.
// newTestRouter 构造并初始化用于单元测试的纯内存 Gin 引擎实例：
// 1. 切换 Gin 为 TestMode 模式；
// 2. 构造默认 Config 与 text/debug 格式的 Logger；
// 3. 实例化 Server 并调用 RegisterRoutes 装配全部中间件与业务路由。
func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	cfg := config.Load()
	logger := pkgobs.NewLogger("text", "debug")
	server := New(cfg, nil, logger, metrics.NewCollector("datasource-mgr"))

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

// TestGetRecordByIDCard verifies GET /api/datasources/:id/record-by-id returns a single record.
// TestGetRecordByIDCard 验证按身份证号查询单条记录端点：
// 1. 请求 GET /api/datasources/ds_yibao/record-by-id?id_card_no=110101196809171010；
// 2. 验证响应状态码 200 OK，found=true，且记录包含匹配的 id_card_no；
// 3. 查询不存在的身份证号时 found=false；
// 4. 缺少 id_card_no 参数时返回 400。
func TestGetRecordByIDCard(t *testing.T) {
	r := newTestRouter()

	// 1. 正常查询：使用 yibao.csv 第一行的身份证号
	req, _ := http.NewRequest("GET", "/api/datasources/ds_yibao/record-by-id?id_card_no=110101196809171010", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["found"] != true {
		t.Errorf("expected found=true, got %+v", resp)
	}
	if resp["datasource_id"] != "ds_yibao" {
		t.Errorf("expected datasource_id=ds_yibao, got %+v", resp)
	}
	record, ok := resp["record"].(map[string]any)
	if !ok {
		t.Fatalf("expected record to be a map, got %T", resp["record"])
	}
	// id_card_no 是纯数字字符串，CSV 加载器会推断为 int64，
	// JSON 反序列化到 map[string]any 时变为 float64，因此用 RawMessage 精确验证。
	var rawResp map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &rawResp)
	var rawRecord map[string]json.RawMessage
	_ = json.Unmarshal(rawResp["record"], &rawRecord)
	idCardRaw := strings.Trim(string(rawRecord["id_card_no"]), `"`)
	if idCardRaw != "110101196809171010" {
		t.Errorf("expected record with id_card_no=110101196809171010, got %s (raw record: %+v)", idCardRaw, record)
	}

	// 2. 查询不存在的身份证号
	req2, _ := http.NewRequest("GET", "/api/datasources/ds_yibao/record-by-id?id_card_no=000000000000000000", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 for not-found, got %d", w2.Code)
	}
	var resp2 map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2["found"] != false {
		t.Errorf("expected found=false for non-existent id, got %+v", resp2)
	}

	// 3. 缺少 id_card_no 参数时应返回 400
	req3, _ := http.NewRequest("GET", "/api/datasources/ds_yibao/record-by-id", nil)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)

	if w3.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing id_card_no, got %d", w3.Code)
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
		if _, _, err := LoadCSVRecords(name); err == nil {
			t.Errorf("expected error for path traversal attempt %q, got nil", name)
		}
	}
}

func TestLoadCSVRecords_AllowedFiles(t *testing.T) {
	// The two official mock datasets must continue to load successfully.
	for _, name := range []string{"yibao.csv", "kangyang.csv"} {
		records, total, err := LoadCSVRecords(name)
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
		_, total, err := LoadCSVRecords(name)
		if err != nil {
			t.Fatalf("lenient load of %s failed: %v", name, err)
		}
		baseline[name] = total
	}

	SetStrictDataIntegrity(true)
	for _, name := range []string{"yibao.csv", "kangyang.csv"} {
		records, total, err := LoadCSVRecords(name)
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
	if _, _, err := LoadCSVRecords("yibao.csv"); err == nil {
		t.Fatal("strict mode must abort on a corrupt row, got nil error")
	} else if !strings.Contains(err.Error(), "malformed record") {
		t.Errorf("expected malformed-record error, got %v", err)
	}

	SetStrictDataIntegrity(false)
	records, total, err := LoadCSVRecords("yibao.csv")
	if err != nil {
		t.Fatalf("lenient mode should skip the corrupt row, got %v", err)
	}
	if total >= 2 || len(records) >= 2 {
		t.Fatalf("expected the corrupt row to be silently dropped, got total=%d", total)
	}
}
