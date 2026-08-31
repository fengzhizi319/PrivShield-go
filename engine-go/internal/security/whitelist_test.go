package security

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWhitelistManager_StaticMode(t *testing.T) {
	m := NewWhitelistManager("", []string{"service-a", "service-b"})

	if !m.IsAllowed("service-a") {
		t.Error("expected service-a to be allowed")
	}
	if !m.IsAllowed("service-b") {
		t.Error("expected service-b to be allowed")
	}
	if m.IsAllowed("unknown") {
		t.Error("expected unknown to be denied")
	}

	scopes := m.GetScopes("service-a")
	if len(scopes) != 1 || scopes[0] != "*" {
		t.Errorf("expected [*] scopes, got %v", scopes)
	}
}

func TestWhitelistManager_YAMLMode(t *testing.T) {
	// 创建临时 YAML 文件
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "whitelist.yaml")
	content := `version: "1.0"
entries:
  - cn: "svc-alpha"
    scopes: ["privacy:mask", "privacy:dp"]
    description: "Alpha service"
    enabled: true
  - cn: "svc-beta"
    scopes: ["*"]
    description: "Beta service (full access)"
    enabled: true
  - cn: "svc-disabled"
    scopes: ["privacy:mask"]
    description: "Disabled entry"
    enabled: false
default_scopes: []
`
	if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewWhitelistManager(yamlPath, nil)

	// 已启用的 CN
	if !m.IsAllowed("svc-alpha") {
		t.Error("expected svc-alpha to be allowed")
	}
	scopes := m.GetScopes("svc-alpha")
	if len(scopes) != 2 || scopes[0] != "privacy:mask" || scopes[1] != "privacy:dp" {
		t.Errorf("unexpected scopes for svc-alpha: %v", scopes)
	}

	if !m.IsAllowed("svc-beta") {
		t.Error("expected svc-beta to be allowed")
	}
	betaScopes := m.GetScopes("svc-beta")
	if len(betaScopes) != 1 || betaScopes[0] != "*" {
		t.Errorf("unexpected scopes for svc-beta: %v", betaScopes)
	}

	// 已禁用的 CN
	if m.IsAllowed("svc-disabled") {
		t.Error("expected svc-disabled to be denied (disabled)")
	}

	// 未列出的 CN
	if m.IsAllowed("unknown") {
		t.Error("expected unknown to be denied")
	}

	// 条目计数
	entries := m.AllEntries()
	if len(entries) != 2 {
		t.Errorf("expected 2 active entries, got %d", len(entries))
	}

	// 无错误
	if m.LastError() != "" {
		t.Errorf("unexpected error: %s", m.LastError())
	}
}

func TestWhitelistManager_HotReload(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "whitelist.yaml")

	// 初始内容
	content1 := `version: "1.0"
entries:
  - cn: "svc-a"
    scopes: ["*"]
    enabled: true
`
	if err := os.WriteFile(yamlPath, []byte(content1), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewWhitelistManager(yamlPath, nil)
	if !m.IsAllowed("svc-a") {
		t.Error("expected svc-a allowed initially")
	}
	if m.IsAllowed("svc-b") {
		t.Error("expected svc-b denied initially")
	}

	// 更新文件（确保 mtime 变化）
	content2 := `version: "1.0"
entries:
  - cn: "svc-a"
    scopes: ["*"]
    enabled: true
  - cn: "svc-b"
    scopes: ["privacy:mask"]
    enabled: true
`
	// 等待以确保 mtime 变化
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(yamlPath, []byte(content2), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(yamlPath, time.Now().Add(time.Second), time.Now().Add(time.Second))

	// 触发重载检查
	if !m.IsAllowed("svc-b") {
		t.Error("expected svc-b allowed after hot-reload")
	}
}

func TestWhitelistManager_MissingFile(t *testing.T) {
	m := NewWhitelistManager("/nonexistent/path/whitelist.yaml", nil)

	if m.IsAllowed("any") {
		t.Error("expected all CNs denied when file missing")
	}
	if m.LastError() == "" {
		t.Error("expected error for missing file")
	}
}

func TestWhitelistManager_DefaultScopes(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "whitelist.yaml")
	content := `version: "1.0"
entries: []
default_scopes: ["privacy:mask"]
`
	if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewWhitelistManager(yamlPath, nil)
	ds := m.DefaultScopes()
	if len(ds) != 1 || ds[0] != "privacy:mask" {
		t.Errorf("expected default_scopes [privacy:mask], got %v", ds)
	}
}

func TestWhitelistManager_Singleton(t *testing.T) {
	ResetWhitelistManager()
	ResetSettings()
	defer func() {
		ResetWhitelistManager()
		ResetSettings()
	}()

	os.Unsetenv("PRIVACY_AUTH_MTLS_WHITELIST_FILE")
	os.Unsetenv("PRIVACY_AUTH_MTLS_ALLOWED_CNS")

	m1 := GetWhitelistManager()
	m2 := GetWhitelistManager()
	if m1 != m2 {
		t.Error("expected same singleton instance")
	}
}

func TestWhitelistManager_Reload(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "whitelist.yaml")
	content := `version: "1.0"
entries:
  - cn: "svc-x"
    scopes: ["*"]
    enabled: true
`
	if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewWhitelistManager(yamlPath, nil)
	if !m.IsAllowed("svc-x") {
		t.Error("expected svc-x allowed")
	}

	// 强制重载
	m.Reload()
	if !m.IsAllowed("svc-x") {
		t.Error("expected svc-x still allowed after reload")
	}
}
