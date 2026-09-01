// Package tlsutil provides unit tests for DynamicWhitelist.
// Package tlsutil 为动态 mTLS 客户端 CN 白名单管理器提供全量单元与边界测试套件。
//
// ==============================================================================
// 【测试模块与验证目标】
// 1. 【Load & Parse】：测试标准 clients 节点与 enabled=false 过滤；
// 2. 【CheckScope】：测试通配符 `*`、精确匹配、前缀通配符 `/AuditLog/*` 及越权拦截；
// 3. 【HotReload】：测试文件重写后的热重载与旧条目作废；
// 4. 【GetScopes】：测试查询存在的 CN 与不存在的 CN；
// 5. 【Error Handling】：测试不存在文件与畸形 YAML 的异常处理；
// 6. 【Legacy Format】：测试历史 entries 字段格式的向下兼容支持；
// 7. 【matchScopePattern】：测试不同通配符匹配规则的覆盖与边界断言。
// ==============================================================================

package tlsutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testWhitelistYAML = `version: "1.0"
clients:
  - cn: "bff-go.privshield.internal"
    allowed_scopes: ["*"]
    role: "gateway"
    description: "BFF gateway"
    enabled: true

  - cn: "service-hub.privshield.internal"
    allowed_scopes: ["/PrivacyService/Process", "/AuditLog/*"]
    role: "orchestrator"
    description: "Service hub"
    enabled: true

  - cn: "disabled-service"
    allowed_scopes: ["*"]
    description: "Disabled entry"
    enabled: false
`

// createTempWhitelist 在临时目录中生成测试用的临时 YAML 白名单文件。
func createTempWhitelist(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Clean(filepath.Join(dir, "mtls-whitelist.yaml"))
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp whitelist: %v", err)
	}
	return path
}

// ─────────────────────────────────────────────────────────────
// 1. 白名单基础加载与状态测试
// ─────────────────────────────────────────────────────────────

func TestNewDynamicWhitelist_Load(t *testing.T) {
	path := createTempWhitelist(t, testWhitelistYAML)
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		t.Fatalf("NewDynamicWhitelist failed: %v", err)
	}
	defer dw.Close()

	// bff-go 应当加载成功
	if !dw.IsAuthorized("bff-go.privshield.internal") {
		t.Error("expected bff-go to be authorized")
	}

	// service-hub 应当加载成功
	if !dw.IsAuthorized("service-hub.privshield.internal") {
		t.Error("expected service-hub to be authorized")
	}

	// disabled-service 被显式禁用，不应授权
	if dw.IsAuthorized("disabled-service") {
		t.Error("expected disabled-service to NOT be authorized")
	}

	// 未知 CN 必须鉴权失败
	if dw.IsAuthorized("unknown-client") {
		t.Error("expected unknown-client to NOT be authorized")
	}
}

// ─────────────────────────────────────────────────────────────
// 2. Scope 方法权限校验测试
// ─────────────────────────────────────────────────────────────

func TestDynamicWhitelist_CheckScope(t *testing.T) {
	path := createTempWhitelist(t, testWhitelistYAML)
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		t.Fatalf("NewDynamicWhitelist failed: %v", err)
	}
	defer dw.Close()

	// bff-go 拥有全局通配符 scope: 允许调用任意方法
	ok, scopes := dw.CheckScope("bff-go.privshield.internal", "/AnyMethod/Anything")
	if !ok {
		t.Error("expected bff-go to be authorized for any method")
	}
	if len(scopes) != 1 || scopes[0] != "*" {
		t.Errorf("expected scopes [*], got %v", scopes)
	}

	// service-hub 拥有特定方法权限: /PrivacyService/Process 应当放行
	ok, _ = dw.CheckScope("service-hub.privshield.internal", "/PrivacyService/Process")
	if !ok {
		t.Error("expected service-hub to be authorized for /PrivacyService/Process")
	}

	// service-hub 拥有前缀通配符 /AuditLog/*: /AuditLog/RecordAudit 应当放行
	ok, _ = dw.CheckScope("service-hub.privshield.internal", "/AuditLog/RecordAudit")
	if !ok {
		t.Error("expected service-hub to be authorized for /AuditLog/RecordAudit via wildcard")
	}

	// service-hub 未被授予 /DatasourceMgr/FetchSlice: 必须被阻断
	ok, _ = dw.CheckScope("service-hub.privshield.internal", "/DatasourceMgr/FetchSlice")
	if ok {
		t.Error("expected service-hub to NOT be authorized for /DatasourceMgr/FetchSlice")
	}

	// 未登记的未知 CN: 必须鉴权失败
	ok, _ = dw.CheckScope("unknown", "/AnyMethod")
	if ok {
		t.Error("expected unknown CN to fail scope check")
	}
}

// ─────────────────────────────────────────────────────────────
// 3. 动态热重载测试
// ─────────────────────────────────────────────────────────────

func TestDynamicWhitelist_HotReload(t *testing.T) {
	path := createTempWhitelist(t, testWhitelistYAML)
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		t.Fatalf("NewDynamicWhitelist failed: %v", err)
	}
	defer dw.Close()

	// 初始状态下 bff-go 存在
	if !dw.IsAuthorized("bff-go.privshield.internal") {
		t.Fatal("expected bff-go to be authorized initially")
	}

	// 覆写新配置内容（移除 bff-go，新增 new-client）
	newYAML := `version: "1.0"
clients:
  - cn: "new-client"
    allowed_scopes: ["*"]
    enabled: true
`
	time.Sleep(50 * time.Millisecond) // 确保文件系统 mtime 发生变化
	if err := os.WriteFile(path, []byte(newYAML), 0644); err != nil {
		t.Fatalf("failed to rewrite whitelist: %v", err)
	}

	// 触发重载
	if err := dw.reload(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	// 旧 CN 应当被清理并作废
	if dw.IsAuthorized("bff-go.privshield.internal") {
		t.Error("expected bff-go to NOT be authorized after reload")
	}

	// 新 CN 应当立即生效
	if !dw.IsAuthorized("new-client") {
		t.Error("expected new-client to be authorized after reload")
	}
}

// ─────────────────────────────────────────────────────────────
// 4. 查询 Scope 列表与异常文件测试
// ─────────────────────────────────────────────────────────────

func TestDynamicWhitelist_GetScopes(t *testing.T) {
	path := createTempWhitelist(t, testWhitelistYAML)
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		t.Fatalf("NewDynamicWhitelist failed: %v", err)
	}
	defer dw.Close()

	scopes, ok := dw.GetScopes("service-hub.privshield.internal")
	if !ok {
		t.Fatal("expected service-hub to exist")
	}
	if len(scopes) != 2 {
		t.Errorf("expected 2 scopes, got %d: %v", len(scopes), scopes)
	}

	_, ok = dw.GetScopes("nonexistent")
	if ok {
		t.Error("expected nonexistent CN to return false")
	}
}

func TestDynamicWhitelist_InvalidFile(t *testing.T) {
	_, err := NewDynamicWhitelist("/nonexistent/path/whitelist.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestDynamicWhitelist_InvalidYAML(t *testing.T) {
	path := createTempWhitelist(t, "invalid: yaml: [broken")
	_, err := NewDynamicWhitelist(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// ─────────────────────────────────────────────────────────────
// 5. 历史格式与通配符规则测试
// ─────────────────────────────────────────────────────────────

func TestDynamicWhitelist_LegacyEntriesFormat(t *testing.T) {
	legacyYAML := `version: "1.0"
entries:
  - cn: "legacy-client"
    scopes: ["*"]
    description: "Legacy format entry"
    enabled: true
  - cn: "legacy-disabled"
    scopes: ["*"]
    enabled: false
`
	path := createTempWhitelist(t, legacyYAML)
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		t.Fatalf("NewDynamicWhitelist failed: %v", err)
	}
	defer dw.Close()

	if !dw.IsAuthorized("legacy-client") {
		t.Error("expected legacy-client to be authorized")
	}
	if dw.IsAuthorized("legacy-disabled") {
		t.Error("expected legacy-disabled to NOT be authorized")
	}
}

func TestMatchScopePattern(t *testing.T) {
	tests := []struct {
		pattern string
		value   string
		want    bool
	}{
		{"*", "/Any/Method", true},
		{"/ServiceHub/*", "/ServiceHub/DispatchTask", true},
		{"/ServiceHub/*", "/ServiceHub/", true},
		{"/ServiceHub/*", "/AuditLog/Record", false},
		{"/PrivacyService/Process", "/PrivacyService/Process", true},
		{"/PrivacyService/Process", "/PrivacyService/Other", false},
	}

	for _, tt := range tests {
		got := matchScopePattern(tt.pattern, tt.value)
		if got != tt.want {
			t.Errorf("matchScopePattern(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.want)
		}
	}
}
