package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newRoleTestRouter 复用 newTestServer 的内存依赖，只替换鉴权身份配置。
func newRoleTestRouter(t *testing.T, apiKey, readerKey string) *gin.Engine {
	t.Helper()
	s := newTestServer()
	s.cfg.APIKey = apiKey
	s.cfg.ReaderAPIKey = readerKey
	r := gin.New()
	s.RegisterRoutes(r)
	return r
}

func statusWithKey(r *gin.Engine, method, path, key string) int {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	r.ServeHTTP(w, req)
	return w.Code
}

// TestReaderRoleEndToEnd 核验专区持只读 Key 时必须能验真/查存证，但对写存证与报表导出只能拿到 403。
// 断言用「状态码类别」而非业务结果，避免与 handler 内部逻辑耦合。
func TestReaderRoleEndToEnd(t *testing.T) {
	r := newRoleTestRouter(t, "write-key", "reader-key")

	readable := []struct{ method, path string }{
		{http.MethodGet, "/v1/audit/logs"},
		{http.MethodGet, "/v1/audit/stats"},
		{http.MethodGet, "/v1/audit/snapshots"},
		{http.MethodPost, "/v1/audit/snapshots/verify"},
		{http.MethodGet, "/v1/audit/chain/verify"},
		{http.MethodPost, "/v1/audit/chain/verify"},
	}
	for _, tc := range readable {
		if code := statusWithKey(r, tc.method, tc.path, "reader-key"); code == http.StatusUnauthorized || code == http.StatusForbidden {
			t.Errorf("reader %s %s: status = %d, want access granted (not 401/403)", tc.method, tc.path, code)
		}
	}

	denied := []struct{ method, path string }{
		{http.MethodPost, "/v1/audit/logs"},   // 写存证：核验员不得写入
		{http.MethodPost, "/v1/audit/report"}, // 报表导出：运维身份专属
	}
	for _, tc := range denied {
		if code := statusWithKey(r, tc.method, tc.path, "reader-key"); code != http.StatusForbidden {
			t.Errorf("reader %s %s: status = %d, want 403", tc.method, tc.path, code)
		}
	}

	// 写入身份不受白名单约束（否则权责分离会反过来卡死业务链路）。
	if code := statusWithKey(r, http.MethodPost, "/v1/audit/report", "write-key"); code == http.StatusUnauthorized || code == http.StatusForbidden {
		t.Errorf("writer POST /v1/audit/report: status = %d, want access granted (not 401/403)", code)
	}
}

// TestReaderRoleDisabledKeepsSingleKeySemantics 未配置只读 Key 时（存量部署），行为与单 Key 鉴权一致。
func TestReaderRoleDisabledKeepsSingleKeySemantics(t *testing.T) {
	r := newRoleTestRouter(t, "write-key", "")

	if code := statusWithKey(r, http.MethodGet, "/v1/audit/chain/verify", "reader-key"); code != http.StatusUnauthorized {
		t.Errorf("foreign key with role disabled: status = %d, want 401", code)
	}
	if code := statusWithKey(r, http.MethodPost, "/v1/audit/logs", "write-key"); code == http.StatusUnauthorized || code == http.StatusForbidden {
		t.Errorf("write key with role disabled: status = %d, want access granted (not 401/403)", code)
	}
}

// TestReadOnlyWhitelistCoversEveryVerificationRoute 白名单与路由表必须同步：
// 新增验真/查询路由却漏进白名单，等于核验专区功能残缺（这里做静态防漏）。
func TestReadOnlyWhitelistCoversEveryVerificationRoute(t *testing.T) {
	for _, ep := range auditReadOnlyEndpoints {
		if ep.Method == "" || ep.Path == "" {
			t.Fatalf("whitelist entry %+v must set both Method and Path", ep)
		}
	}
	// 写端点必须显式不在表内。
	for _, ep := range auditReadOnlyEndpoints {
		if ep.Path == "/v1/audit/logs" && ep.Method == http.MethodPost {
			t.Fatal("POST /v1/audit/logs (evidence write) must never be readable by the reader role")
		}
	}
}
