// Package config provides centralized configuration for the mock datasource-mgr module.
// Package config 为模拟数据源模块（datasource-mgr）提供集中化运行时配置管理。
//
// 该模块负责从环境变量中读取 HTTP REST、gRPC、mTLS 证书链、安全鉴权及日志等配置项，
// 并提供安全合理的默认回退值，确保服务在本地单机调试与生产容器化部署中均能无缝启动。
package config

import (
	"fmt"
	"os"
	"strconv"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
)

// Config holds runtime configuration for the mock datasource-mgr server.
// Config 结构体封装了 mock datasource-mgr 服务的全部运行时配置项。
type Config struct {
	// HTTP REST 服务网络监听配置
	Host string // HTTP 监听主机地址（例如 "127.0.0.1" 或 "0.0.0.0"）
	Port int    // HTTP 监听端口号（默认 8083）

	// gRPC 远程过程调用服务网络监听配置
	GRPCHost string // gRPC 监听主机地址（默认 "127.0.0.1"）
	GRPCPort int    // gRPC 监听端口号（默认 50053）

	// mTLS 双向传输层安全认证配置
	TLSEnabled          bool   // 是否启用 TLS/mTLS 加密传输（默认 false）
	TLSCertFile         string // 服务端 X.509 证书 PEM 文件路径
	TLSKeyFile          string // 服务端私钥 PEM 文件路径
	TLSCAFile           string // 验证客户端证书的受信任 CA 根证书 PEM 文件路径
	TLSClientAuth       string // 客户端认证模式："require"（强制双向认证）| "verify"（可选验证）| ""（单向 TLS）
	TLSPinnedPubKeyFile string // 固定客户端公钥 PEM 文件路径，用于应用层公钥指纹比对（防 CA 劫持与伪造）

	// mTLS CN 白名单配置（gRPC 服务端 method-scope 鉴权）
	MTLSWhitelistFile string // 客户端证书 CN 白名单 YAML 文件路径（启用 gRPC TLS 时必填，缺失即启动失败）

	// 安全鉴权与跨域配置
	APIKey      string                        // 入站 HTTP/gRPC 请求 API Key 鉴权密钥（非环回监听时为必填项，缺失即启动失败）
	ScopeKeys   map[string]*pkgauth.KeyConfig // Scope-based API Key 映射（DATASOURCE_MGR_API_KEYS），优先于 APIKey
	CORSOrigins []string // 允许的跨域来源列表（CORS 白名单）

	// RequireTLS 由生产编排显式置真：TLS 未启用即拒绝启动，防止漏配证书仍照常服务。
	RequireTLS bool

	// StrictStorage 严格存储模式（P0-4 禁静音降级）：为真时禁止任何「写入失败/数据损坏 → 降级为内存态」的
	// 静默回退，异常一律上抛为启动或请求失败。默认 true，可用 DATASOURCE_MGR_STRICT_STORAGE=false 显式放宽。
	StrictStorage bool

	// 可观测性与日志配置
	LogFormat string // 日志输出格式："json"（生产推荐）或 "text"（本地开发可读）
	LogLevel  string // 日志输出级别："debug", "info", "warn", "error"

	// Graceful shutdown / 优雅关闭
	ShutdownTimeout int // HTTP 优雅关闭超时秒数（默认 5）

	// Rate limiting / 每客户端 IP 令牌桶限流
	RateLimitRPS   int // 每秒允许的请求数（默认 100，0 = 不限流）
	RateLimitBurst int // 令牌桶突发容量（默认 200）
}

// Load reads configuration from environment variables with fallback defaults.
// Load 函数从系统环境变量中加载配置信息，执行逻辑如下：
// 1. 调用通用配置工具库 pkgconfig.Env* 依次读取各配置项；
// 2. 若对应环境变量未设置或为空，则自动回退至预设的安全默认值（HTTP :8083, gRPC :50053）；
// 3. 将解析后的字段组装为 *Config 实例并返回给调用方。
func Load() *Config {
	return &Config{
		// HTTP 监听地址解析（默认 127.0.0.1:8083）
		Host: pkgconfig.EnvString("DATASOURCE_MGR_HOST", "127.0.0.1"),
		Port: pkgconfig.EnvInt("DATASOURCE_MGR_PORT", 8083),

		// gRPC 监听地址解析（默认 127.0.0.1:50053）
		GRPCHost: pkgconfig.EnvString("DATASOURCE_MGR_GRPC_HOST", "127.0.0.1"),
		GRPCPort: pkgconfig.EnvInt("DATASOURCE_MGR_GRPC_PORT", 50053),

		// TLS 与 mTLS 证书配置解析
		TLSEnabled:          pkgconfig.EnvBool("DATASOURCE_MGR_TLS_ENABLED", false),
		TLSCertFile:         pkgconfig.EnvString("DATASOURCE_MGR_TLS_CERT_FILE", ""),
		TLSKeyFile:          pkgconfig.EnvString("DATASOURCE_MGR_TLS_KEY_FILE", ""),
		TLSCAFile:           pkgconfig.EnvString("DATASOURCE_MGR_TLS_CA_FILE", ""),
		TLSClientAuth:       pkgconfig.EnvString("DATASOURCE_MGR_TLS_CLIENT_AUTH", ""),
		TLSPinnedPubKeyFile: pkgconfig.EnvString("DATASOURCE_MGR_TLS_PINNED_PUBKEY_FILE", ""),

		// mTLS CN 白名单配置（全局白名单文件，所有 Go gRPC 服务端共享）
		MTLSWhitelistFile: pkgconfig.EnvString("PRIVACY_AUTH_MTLS_WHITELIST_FILE", ""),

		// API 鉴权与跨域策略解析
		APIKey:      pkgconfig.EnvString("DATASOURCE_MGR_API_KEY", ""),
		ScopeKeys:   pkgauth.LoadAPIKeysFromEnv("DATASOURCE_MGR_API_KEYS"),
		CORSOrigins: pkgconfig.EnvStringSlice("DATASOURCE_MGR_CORS_ORIGINS"),

		// Fail-closed 生产门禁 / zero-trust production gate
		RequireTLS: pkgconfig.EnvBool("DATASOURCE_MGR_REQUIRE_TLS", false),

		// Strict storage mode / 严格存储模式：默认禁止降级回退，存储或数据异常即启动/请求失败。
		StrictStorage: pkgconfig.EnvBool("DATASOURCE_MGR_STRICT_STORAGE", pkgconfig.EnvBool("STRICT_STORAGE", true)),

		// 结构化日志参数解析（默认 json 格式，info 级别）
		LogFormat: pkgconfig.EnvString("DATASOURCE_MGR_LOG_FORMAT", "json"),
		LogLevel:  pkgconfig.EnvString("DATASOURCE_MGR_LOG_LEVEL", "info"),

		// Graceful shutdown / 优雅关闭超时（默认 5 秒）
		ShutdownTimeout: pkgconfig.EnvInt("DATASOURCE_MGR_SHUTDOWN_TIMEOUT", 5),

		// Rate limiting / 每客户端 IP 令牌桶限流（默认 100 rps，突发 200）
		RateLimitRPS:   pkgconfig.EnvInt("DATASOURCE_MGR_RATE_LIMIT_RPS", 100),
		RateLimitBurst: pkgconfig.EnvInt("DATASOURCE_MGR_RATE_LIMIT_BURST", 200),
	}
}

// Validate checks that the configuration is consistent and all required files exist.
// Validate 校验配置一致性：TLS 文件存在性与 fail-closed 零信任安全不变式（P0-1）。
func (c *Config) Validate() error {
	if c.TLSEnabled {
		if c.TLSCertFile == "" {
			return fmt.Errorf("TLS enabled but DATASOURCE_MGR_TLS_CERT_FILE is not set")
		}
		if c.TLSKeyFile == "" {
			return fmt.Errorf("TLS enabled but DATASOURCE_MGR_TLS_KEY_FILE is not set")
		}
		if _, err := os.Stat(c.TLSCertFile); err != nil {
			return fmt.Errorf("TLS cert file not accessible: %s: %w", c.TLSCertFile, err)
		}
		if _, err := os.Stat(c.TLSKeyFile); err != nil {
			return fmt.Errorf("TLS key file not accessible: %s: %w", c.TLSKeyFile, err)
		}
	}

	// P0-1 零信任默认态：非环回监听必须有入站 API Key；声明需要 TLS 则必须启用；
	// 启用 gRPC TLS 时必须注入 CN 白名单文件（否则白名单拦截器根本不注册，形同未做身份鉴别）。
	// 本进程始终同时监听 REST 与 gRPC，故 GRPCEnabled 恒为 true。
	return pkgconfig.ValidateFailClosed(pkgconfig.SecurityRequirements{
		ServiceName:       "datasource-mgr",
		Hosts:             []string{c.Host, c.GRPCHost},
		APIKey:            c.APIKey,
		AuthEnabled:       c.APIKey != "",
		TLSEnabled:        c.TLSEnabled,
		RequireTLS:        c.RequireTLS,
		GRPCEnabled:       true,
		MTLSWhitelistFile: c.MTLSWhitelistFile,
	})
}

// Address returns the full HTTP listen address formatted as "host:port".
// Address 方法将 Host 和 Port 拼接为标准网络监听地址字符串（如 "127.0.0.1:8083" 或 "0.0.0.0:8083"），
// 供 net/http.Server 绑定监听端口使用。
func (c *Config) Address() string {
	return c.Host + ":" + strconv.Itoa(c.Port)
}

// GRPCAddress returns the full gRPC listen address formatted as "host:port".
// GRPCAddress 方法将 GRPCHost 和 GRPCPort 拼接为标准 gRPC 网络监听地址字符串（如 "127.0.0.1:50053" 或 "0.0.0.0:50053"），
// 供 net.Listen("tcp", ...) 创建 gRPC 监听套接字使用。
func (c *Config) GRPCAddress() string {
	return c.GRPCHost + ":" + strconv.Itoa(c.GRPCPort)
}
