// Package config provides centralized configuration for the audit-log module.
// Package config 为审计存证模块提供集中化配置管理。
package config

import (
	"fmt"
	"os"
	"strconv"

	pkgconfig "github.com/fengzhizi319/PrivShield/pkg/config"
)

// Config holds all runtime configuration for the audit-log server.
// Config 保存审计存证服务器运行时的所有配置项。
type Config struct {
	Host          string // HTTP listen host / HTTP 监听地址
	Port          int    // HTTP listen port / HTTP 监听端口
	AgentRESTHost string // Upstream agent REST host / 上游 agent REST 地址
	AgentRESTPort int    // Upstream agent REST port / 上游 agent REST 端口
	AgentAPIKey   string // Optional auth key for upstream agent / 上游 agent 认证密钥

	// gRPC server configuration / gRPC 服务器配置
	GRPCHost string // gRPC listen host / gRPC 监听地址
	GRPCPort int    // gRPC listen port / gRPC 监听端口

	// mTLS configuration / mTLS 双向认证配置
	TLSEnabled    bool   // Enable TLS/mTLS on gRPC server / 在 gRPC 服务端启用 TLS/mTLS
	TLSCertFile   string // Server certificate PEM / 服务端证书文件路径
	TLSKeyFile    string // Server private key PEM / 服务端私钥文件路径
	TLSCAFile     string // CA cert for client verification / 用于校验客户端证书的 CA 证书路径
	TLSClientAuth string // Client auth mode: "require" | "verify" | "" / 客户端认证模式

	// mTLS CN whitelist configuration / mTLS CN 白名单配置（gRPC 服务端 method-scope 鉴权）
	MTLSWhitelistFile string // Path to client certificate CN whitelist YAML / 客户端证书 CN 白名单 YAML 文件路径

	// Public key pinning / 公钥固定
	TLSPinnedPubKeyFile string // Pinned client public key PEM / 固定的客户端公钥文件路径

	// Production hardening / 生产加固
	APIKey        string   // Inbound API key for this module / 本模块入站 API Key
	CORSOrigins   []string // Allowed CORS origins / 允许的 CORS 来源
	DBPath        string   // SQLite database path (empty = in-memory) / SQLite 数据库路径
	PGDSN         string   // PostgreSQL connection DSN (Phase B / high-concurrency multi-replica)
	EncryptionKey string   // Master key for envelope encryption of sensitive snapshot samples
	ArchiveDir    string   // Archive destination directory for old audit records
	LogFormat     string   // "json" or "text" / 日志格式
	LogLevel      string   // "debug", "info", "warn", "error" / 日志级别

	// Data retention / 数据保留策略
	RetentionDays int // 审计日志保留天数，超期自动清理（0 = 不清理）

	// Graceful shutdown / 优雅关闭
	ShutdownTimeout int // HTTP 优雅关闭超时秒数（默认 5）

	// Rate limiting / 每客户端 IP 令牌桶限流
	RateLimitRPS   int // 每秒允许的请求数（默认 100，0 = 不限流）
	RateLimitBurst int // 令牌桶突发容量（默认 200）

	// Strict storage enforcement / 严格存储模式（禁止降级回退）
	StrictStorage bool // 当配置的持久化存储连接失败时直接报错退出，禁止静默回退
}

// Load reads configuration from environment variables.
// Load 从环境变量读取所有配置项。
func Load() *Config {
	pgDSN := pkgconfig.EnvString("AUDIT_LOG_PG_DSN", "")
	if pgDSN == "" {
		pgDSN = pkgconfig.EnvString("PG_DSN", "")
	}

	encKey := pkgconfig.EnvString("AUDIT_LOG_ENCRYPTION_KEY", "")
	if encKey == "" {
		encKey = pkgconfig.EnvString("PRIVACY_AUDIT_KEY", "")
	}

	return &Config{
		Host:          pkgconfig.EnvString("AUDIT_LOG_HOST", "127.0.0.1"),
		Port:          pkgconfig.EnvInt("AUDIT_LOG_PORT", 8084),
		AgentRESTHost: pkgconfig.EnvString("PRIVACY_AGENT_REST_HOST", "127.0.0.1"),
		AgentRESTPort: pkgconfig.EnvInt("PRIVACY_REST_PORT", 8079),
		AgentAPIKey:   pkgconfig.EnvString("PRIVACY_AGENT_API_KEY", ""),

		// gRPC / gRPC 配置
		GRPCHost: pkgconfig.EnvString("AUDIT_LOG_GRPC_HOST", "127.0.0.1"),
		GRPCPort: pkgconfig.EnvInt("AUDIT_LOG_GRPC_PORT", 50054),

		// mTLS / 双向认证配置
		TLSEnabled:    pkgconfig.EnvBool("AUDIT_LOG_TLS_ENABLED", false),
		TLSCertFile:   pkgconfig.EnvString("AUDIT_LOG_TLS_CERT_FILE", ""),
		TLSKeyFile:    pkgconfig.EnvString("AUDIT_LOG_TLS_KEY_FILE", ""),
		TLSCAFile:     pkgconfig.EnvString("AUDIT_LOG_TLS_CA_FILE", ""),
		TLSClientAuth: pkgconfig.EnvString("AUDIT_LOG_TLS_CLIENT_AUTH", ""),

		// mTLS CN whitelist / 全局白名单文件
		MTLSWhitelistFile: pkgconfig.EnvString("PRIVACY_AUTH_MTLS_WHITELIST_FILE", ""),

		// Public key pinning / 公钥固定
		TLSPinnedPubKeyFile: pkgconfig.EnvString("AUDIT_LOG_TLS_PINNED_PUBKEY_FILE", ""),

		// Production hardening / 生产加固
		APIKey:        pkgconfig.EnvString("AUDIT_LOG_API_KEY", ""),
		CORSOrigins:   pkgconfig.EnvStringSlice("AUDIT_LOG_CORS_ORIGINS"),
		DBPath:        pkgconfig.EnvString("AUDIT_LOG_DB_PATH", ""),
		PGDSN:         pgDSN,
		EncryptionKey: encKey,
		ArchiveDir:    pkgconfig.EnvString("AUDIT_LOG_ARCHIVE_DIR", "data/archives"),
		LogFormat:     pkgconfig.EnvString("AUDIT_LOG_LOG_FORMAT", "json"),
		LogLevel:      pkgconfig.EnvString("AUDIT_LOG_LOG_LEVEL", "info"),

		// Data retention / 数据保留策略（默认 90 天，审计日志保留期较长）
		RetentionDays: pkgconfig.EnvInt("AUDIT_LOG_RETENTION_DAYS", 90),

		// Graceful shutdown / 优雅关闭超时（默认 5 秒）
		ShutdownTimeout: pkgconfig.EnvInt("AUDIT_LOG_SHUTDOWN_TIMEOUT", 5),

		// Rate limiting / 每客户端 IP 令牌桶限流（默认 100 rps，突发 200）
		RateLimitRPS:   pkgconfig.EnvInt("AUDIT_LOG_RATE_LIMIT_RPS", 100),
		RateLimitBurst: pkgconfig.EnvInt("AUDIT_LOG_RATE_LIMIT_BURST", 200),

		// Strict storage mode / 严格存储模式（禁止降级回退）
		StrictStorage: pkgconfig.EnvBool("AUDIT_LOG_STRICT_STORAGE", pkgconfig.EnvBool("STRICT_STORAGE", false)),
	}
}

// Validate checks that the configuration is consistent and all required files exist.
// Validate 校验配置一致性：当 TLS 启用时确认证书/私钥文件存在。
func (c *Config) Validate() error {
	if c.TLSEnabled {
		if c.TLSCertFile == "" {
			return fmt.Errorf("TLS enabled but AUDIT_LOG_TLS_CERT_FILE is not set")
		}
		if c.TLSKeyFile == "" {
			return fmt.Errorf("TLS enabled but AUDIT_LOG_TLS_KEY_FILE is not set")
		}
		if _, err := os.Stat(c.TLSCertFile); err != nil {
			return fmt.Errorf("TLS cert file not accessible: %s: %w", c.TLSCertFile, err)
		}
		if _, err := os.Stat(c.TLSKeyFile); err != nil {
			return fmt.Errorf("TLS key file not accessible: %s: %w", c.TLSKeyFile, err)
		}
	}
	return nil
}

// Address returns the full HTTP listen address.
// Address 返回完整的 HTTP 监听地址。
func (c *Config) Address() string {
	return c.Host + ":" + strconv.Itoa(c.Port)
}

// AgentBaseURL returns the upstream agent REST base URL.
// AgentBaseURL 返回上游 agent REST 基础地址。
func (c *Config) AgentBaseURL() string {
	return "http://" + c.AgentRESTHost + ":" + strconv.Itoa(c.AgentRESTPort)
}

// AgentBaseURLs returns all configured upstream agent REST base URLs.
func (c *Config) AgentBaseURLs() []string {
	envURLs := pkgconfig.EnvStringSlice("PRIVACY_AGENT_URLS")
	if len(envURLs) > 0 {
		return envURLs
	}
	return []string{c.AgentBaseURL()}
}

// GRPCAddress returns the full gRPC listen address.
// GRPCAddress 返回完整的 gRPC 监听地址。
func (c *Config) GRPCAddress() string {
	return c.GRPCHost + ":" + strconv.Itoa(c.GRPCPort)
}
