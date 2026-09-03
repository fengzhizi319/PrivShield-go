// Package config provides shared environment variable helpers for console Go modules.
// Package config 为控制台各 Go 模块提供共享的环境变量读取工具函数。
//
// 三个模块（service-hub / datasource-mgr / audit-log）原先各自维护完全相同的
// getEnv / getEnvInt / getEnvBool 实现，现统一抽取至本包。
package config

import (
	"os"
	"strconv"
	"strings"
)

// EnvString reads an environment variable, returning def if unset or empty.
// EnvString 读取环境变量，未设置或为空时返回默认值。
func EnvString(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// EnvStringFirstSet returns the first non-empty value among the given environment variables.
// Returns empty string if all of them are unset or empty.
// EnvStringFirstSet 依次读取给定的环境变量，返回第一个非空值；全为空时返回空字符串。
func EnvStringFirstSet(names ...string) string {
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

// EnvStringOptional reads an environment variable, distinguishing "unset" from "explicitly set to empty".
// It only falls back to def when the variable is completely unset; an empty string is treated as a valid value.
// EnvStringOptional 读取环境变量，区分"未设置"与"显式设为空字符串"；
// 仅在变量完全未设置时才使用默认值，空字符串被视为合法值。
func EnvStringOptional(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

// EnvInt reads an environment variable as int, returning def on missing or invalid.
// EnvInt 以整数形式读取环境变量，缺失或无效时返回默认值。
func EnvInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

// EnvInt64 reads an environment variable as int64, returning def on missing or invalid.
// EnvInt64 以 64 位整数形式读取环境变量，缺失或无效时返回默认值。
func EnvInt64(name string, def int64) int64 {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	i, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return i
}

// EnvFloat reads an environment variable as float64, returning def on missing or invalid.
// EnvFloat 以浮点数形式读取环境变量，缺失或无效时返回默认值。
func EnvFloat(name string, def float64) float64 {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// EnvBool reads an environment variable as bool.
// Recognized true values: "true", "1", "yes", "on" (case-insensitive).
// EnvBool 以布尔值读取环境变量。
// 识别为 true 的值: "true"、"1"、"yes"、"on"（不区分大小写）。
func EnvBool(name string, def bool) bool {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// EnvStringFallback reads primary env var; if unset or empty, falls back to fallback env var; if still unset, returns def.
// EnvStringFallback 优先读取主环境变量；若未设置或为空则回退读取次环境变量；仍未设置则返回默认值。
func EnvStringFallback(primary, fallback, def string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	if v := os.Getenv(fallback); v != "" {
		return v
	}
	return def
}

// EnvIntFallback reads primary env var as int; if unset or invalid, falls back to fallback env var; if still unset, returns def.
// EnvIntFallback 优先读取主环境变量为整数；若未设置或无效则回退读取次环境变量；仍未设置则返回默认值。
func EnvIntFallback(primary, fallback string, def int) int {
	if v := os.Getenv(primary); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	if v := os.Getenv(fallback); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

// EnvBoolFallback reads primary env var as bool; if unset or empty, falls back to fallback env var; if still unset, returns def.
// EnvBoolFallback 优先读取主环境变量为布尔值；若未设置或为空则回退读取次环境变量；仍未设置则返回默认值。
func EnvBoolFallback(primary, fallback string, def bool) bool {
	if v := os.Getenv(primary); v != "" {
		return parseBool(v)
	}
	if v := os.Getenv(fallback); v != "" {
		return parseBool(v)
	}
	return def
}

// EnvFloatFallback reads primary env var as float64; if unset or invalid, falls back to fallback env var; if still unset, returns def.
func EnvFloatFallback(primary, fallback string, def float64) float64 {
	if v := os.Getenv(primary); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	if v := os.Getenv(fallback); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// EnvStringSlice reads a comma-separated environment variable as []string.
// Returns nil if the variable is empty.
// EnvStringSlice 以逗号分隔读取环境变量为字符串切片。
// 环境变量为空时返回 nil。
func EnvStringSlice(name string) []string {
	v := os.Getenv(name)
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
