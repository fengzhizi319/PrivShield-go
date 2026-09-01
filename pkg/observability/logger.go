// Package observability provides shared observability primitives for all PrivShield Go modules.
//
// 包含结构化日志初始化、HTTP 请求日志、Prometheus RED 指标与抽象 Tracer。
// 设计目标：默认零外部依赖、独立 Registry、可嵌入业务指标扩展。
package observability

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger creates a structured slog.Logger with the given format and level.
//
// format: "json" (default) or "text"
// level:  "debug" | "info" (default) | "warn" | "error" (case-insensitive)
func NewLogger(format, level string) *slog.Logger {
	var logLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn", "warning":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: logLevel}

	var handler slog.Handler
	if strings.ToLower(format) == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

// InitLogger initializes the global default logger.
// It is a convenience wrapper around NewLogger + slog.SetDefault.
func InitLogger(format, level string) {
	slog.SetDefault(NewLogger(format, level))
}
