// Package config 测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证 Package config（环境变量读取工具函数）的核心功能：
//  1. 【EnvString】：验证字符串环境变量的读取，以及变量缺失时回退到默认值；
//  2. 【EnvInt】：验证整数环境变量的读取、缺失回退默认值、非法值（非数字）回退默认值；
//  3. 【EnvBool】：验证多种布尔表示形式（true/TRUE/1/yes/on/false/0/no/空串）的解析，以及缺失回退；
//  4. 【EnvStringSlice】：验证逗号分隔字符串切片的解析，以及缺失变量返回空切片。
// ==============================================================================

package config

import (
	"os"
	"testing"
)

// ──────────────────────────────────────────────
// 1. 字符串环境变量读取测试
// ──────────────────────────────────────────────

// TestEnvString 验证 EnvString 对字符串环境变量的读取与默认值回退逻辑。
// 执行逻辑：设置 TEST_STR="hello"，断言读取到 "hello"；
// 读取不存在的 TEST_STR_MISSING，断言回退到默认值 "default"。
func TestEnvString(t *testing.T) {
	os.Setenv("TEST_STR", "hello")
	defer os.Unsetenv("TEST_STR")

	if got := EnvString("TEST_STR", "default"); got != "hello" {
		t.Errorf("EnvString(hello) = %q, want hello", got)
	}
	if got := EnvString("TEST_STR_MISSING", "default"); got != "default" {
		t.Errorf("EnvString(missing) = %q, want default", got)
	}
}

// ──────────────────────────────────────────────
// 2. 整数环境变量读取测试
// ──────────────────────────────────────────────

// TestEnvInt 验证 EnvInt 对整数环境变量的读取与多重回退逻辑。
// 执行逻辑：设置 TEST_INT="42" 断言读取到 42；
// 缺失变量回退到默认值 99；设置非法值 "not-a-number" 回退到默认值 7。
func TestEnvInt(t *testing.T) {
	os.Setenv("TEST_INT", "42")
	defer os.Unsetenv("TEST_INT")

	if got := EnvInt("TEST_INT", 0); got != 42 {
		t.Errorf("EnvInt(42) = %d, want 42", got)
	}
	if got := EnvInt("TEST_INT_MISSING", 99); got != 99 {
		t.Errorf("EnvInt(missing) = %d, want 99", got)
	}

	os.Setenv("TEST_INT_BAD", "not-a-number")
	defer os.Unsetenv("TEST_INT_BAD")
	if got := EnvInt("TEST_INT_BAD", 7); got != 7 {
		t.Errorf("EnvInt(bad) = %d, want 7", got)
	}
}

// ──────────────────────────────────────────────
// 3. 布尔环境变量解析测试
// ──────────────────────────────────────────────

// TestEnvBool 验证 EnvBool 对多种布尔表示形式的解析逻辑。
// 执行逻辑：遍历 true/TRUE/1/yes/on（解析为 true）与 false/0/no/空串（解析为 false），
// 断言每种表示均能正确解析；最后验证缺失变量回退到默认值 true。
func TestEnvBool(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{"on", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"", false},
	}

	for _, tt := range tests {
		os.Setenv("TEST_BOOL", tt.value)
		got := EnvBool("TEST_BOOL", false)
		if got != tt.expected {
			t.Errorf("EnvBool(%q) = %v, want %v", tt.value, got, tt.expected)
		}
	}
	os.Unsetenv("TEST_BOOL")

	// Test default
	if got := EnvBool("TEST_BOOL_MISSING", true); got != true {
		t.Errorf("EnvBool(missing, true) = %v, want true", got)
	}
}

// ──────────────────────────────────────────────
// 4. 字符串切片环境变量解析测试
// ──────────────────────────────────────────────

// TestEnvStringSlice 验证 EnvStringSlice 对逗号分隔字符串切片的解析逻辑。
// 执行逻辑：设置 TEST_SLICE="a,b,c"，断言解析为 [a b c]；
// 缺失变量返回空切片（长度为 0）。
func TestEnvStringSlice(t *testing.T) {
	os.Setenv("TEST_SLICE", "a,b,c")
	defer os.Unsetenv("TEST_SLICE")

	got := EnvStringSlice("TEST_SLICE")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("EnvStringSlice(a,b,c) = %v, want [a b c]", got)
	}

	// Empty returns nil
	got2 := EnvStringSlice("TEST_SLICE_MISSING")
	if len(got2) != 0 {
		t.Errorf("EnvStringSlice(missing) = %v, want empty", got2)
	}
}
