package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")

	content := `
# 这是一个注释行
AGENT_REST_PORT=9090
AGENT_REST_HOST="192.168.1.100"
AGENT_LOG_LEVEL='DEBUG'
AGENT_RATE_LIMIT_RPS=5000 # 行内注释
EXISTING_VAR=file_value
`
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp .env: %v", err)
	}

	// 预置系统环境变量，验证高优先级不被文件覆盖
	t.Setenv("EXISTING_VAR", "system_value")
	t.Setenv("AGENT_REST_PORT", "")
	t.Setenv("AGENT_REST_HOST", "")
	t.Setenv("AGENT_LOG_LEVEL", "")
	t.Setenv("AGENT_RATE_LIMIT_RPS", "")

	// 清理已设置
	os.Unsetenv("AGENT_REST_PORT")
	os.Unsetenv("AGENT_REST_HOST")
	os.Unsetenv("AGENT_LOG_LEVEL")
	os.Unsetenv("AGENT_RATE_LIMIT_RPS")

	count, err := LoadDotEnv(envPath)
	if err != nil {
		t.Fatalf("LoadDotEnv returned error: %v", err)
	}
	if count < 4 {
		t.Errorf("expected at least 4 variables loaded, got %d", count)
	}

	if got := os.Getenv("AGENT_REST_PORT"); got != "9090" {
		t.Errorf("AGENT_REST_PORT = %s, want 9090", got)
	}
	if got := os.Getenv("AGENT_REST_HOST"); got != "192.168.1.100" {
		t.Errorf("AGENT_REST_HOST = %s, want 192.168.1.100", got)
	}
	if got := os.Getenv("AGENT_LOG_LEVEL"); got != "DEBUG" {
		t.Errorf("AGENT_LOG_LEVEL = %s, want DEBUG", got)
	}
	if got := os.Getenv("AGENT_RATE_LIMIT_RPS"); got != "5000" {
		t.Errorf("AGENT_RATE_LIMIT_RPS = %s, want 5000", got)
	}
	// 验证系统预置变量未被覆盖
	if got := os.Getenv("EXISTING_VAR"); got != "system_value" {
		t.Errorf("EXISTING_VAR = %s, want system_value", got)
	}
}
