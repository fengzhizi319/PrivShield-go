// Package config provides centralized configuration for the audit-log module.
// Package config 为审计存证模块提供集中化配置管理。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
)

// minEvidenceRetentionDays 是存证物理删除允许的最短留存天数（三年），
// 对应数安法二十一条与政务信息资源共享留存要求；短于此值一律拒绝启动。
const minEvidenceRetentionDays = 1095

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
	ReaderAPIKey  string   // Read-only verification key (P1-6 只读核验员) / 只读核验员 Key，空=不启用
	CORSOrigins   []string // Allowed CORS origins / 允许的 CORS 来源
	DBPath        string   // SQLite database path (empty = in-memory) / SQLite 数据库路径
	PGDSN         string   // PostgreSQL connection DSN (Phase B / high-concurrency multi-replica)
	EncryptionKey string   // Master key for envelope encryption of sensitive snapshot samples
	ArchiveDir    string   // Archive destination directory for old audit records
	LogFormat     string   // "json" or "text" / 日志格式
	LogLevel      string   // "debug", "info", "warn", "error" / 日志级别

	// RequireTLS 由生产编排显式置真：TLS 未启用即拒绝启动，防止漏配证书仍照常服务。
	RequireTLS bool

	// HashKey 是链式完整性哈希的 HMAC-SM3 密钥（局方托管）。为空时沿用无密钥 SM3 口径（仅存量兼容）。
	HashKey string

	// DBWriteOnly 启用时在启动阶段自检数据库账号是否缺少 UPDATE/DELETE 权限（只写存证账号）。
	DBWriteOnly bool

	// ArchivePageSize 是归档段单批存证日志条数（0 = 默认 500）。
	ArchivePageSize int

	// PostgreSQL 连接池覆盖（0 = 沿用自适应区间）
	PGMaxConn int // AUDIT_LOG_PG_MAX_CONNS
	PGMinConn int // AUDIT_LOG_PG_MIN_CONNS

	// 微批刷盘器参数（0 = 沿用 flusher.DefaultConfig 对应默认值）
	FlushBatchSize        int // AUDIT_LOG_FLUSH_BATCH_SIZE
	FlushIntervalMs       int // AUDIT_LOG_FLUSH_INTERVAL_MS
	FlushQueueSize        int // AUDIT_LOG_FLUSH_QUEUE_SIZE
	FlushEnqueueTimeoutMs int // AUDIT_LOG_FLUSH_ENQUEUE_TIMEOUT_MS
	FlushMaxStaged        int // AUDIT_LOG_FLUSH_MAX_STAGED
	FlushCloseTimeoutMs   int // AUDIT_LOG_FLUSH_CLOSE_TIMEOUT_MS

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
		ReaderAPIKey:  pkgconfig.EnvString("AUDIT_LOG_READER_API_KEY", ""),
		CORSOrigins:   pkgconfig.EnvStringSlice("AUDIT_LOG_CORS_ORIGINS"),
		DBPath:        pkgconfig.EnvString("AUDIT_LOG_DB_PATH", ""),
		PGDSN:         pgDSN,
		EncryptionKey: encKey,
		ArchiveDir:    pkgconfig.EnvString("AUDIT_LOG_ARCHIVE_DIR", "data/archives"),
		LogFormat:     pkgconfig.EnvString("AUDIT_LOG_LOG_FORMAT", "json"),
		LogLevel:      pkgconfig.EnvString("AUDIT_LOG_LOG_LEVEL", "info"),

		// Fail-closed 生产门禁与密钥化存证 / production gate, keyed chain, write-only self-check
		RequireTLS:  pkgconfig.EnvBool("AUDIT_LOG_REQUIRE_TLS", false),
		HashKey:     pkgconfig.EnvString("AUDIT_LOG_HASH_KEY", ""),
		DBWriteOnly: pkgconfig.EnvBool("AUDIT_LOG_DB_WRITE_ONLY", false),
		PGMaxConn:   pkgconfig.EnvInt("AUDIT_LOG_PG_MAX_CONNS", 0),
		PGMinConn:   pkgconfig.EnvInt("AUDIT_LOG_PG_MIN_CONNS", 0),

		// 归档段单批日志条数 / records per archive segment (0 = 500)
		ArchivePageSize: pkgconfig.EnvInt("AUDIT_LOG_ARCHIVE_PAGE_SIZE", 0),

		// 微批刷盘参数 / micro-batch flusher tunables
		FlushBatchSize:        pkgconfig.EnvInt("AUDIT_LOG_FLUSH_BATCH_SIZE", 0),
		FlushIntervalMs:       pkgconfig.EnvInt("AUDIT_LOG_FLUSH_INTERVAL_MS", 0),
		FlushQueueSize:        pkgconfig.EnvInt("AUDIT_LOG_FLUSH_QUEUE_SIZE", 0),
		FlushEnqueueTimeoutMs: pkgconfig.EnvInt("AUDIT_LOG_FLUSH_ENQUEUE_TIMEOUT_MS", 0),
		FlushMaxStaged:        pkgconfig.EnvInt("AUDIT_LOG_FLUSH_MAX_STAGED", 0),
		FlushCloseTimeoutMs:   pkgconfig.EnvInt("AUDIT_LOG_FLUSH_CLOSE_TIMEOUT_MS", 0),

		// Data retention / 数据保留策略：默认 0 = 永不物理删除（等保三级与数安法留存要求）。
		// >0 时清理前必须先完成归档落盘，见 main.go 的归档前置校验与 internal/archive 包。
		RetentionDays: pkgconfig.EnvInt("AUDIT_LOG_RETENTION_DAYS", 0),

		// Graceful shutdown / 优雅关闭超时（默认 5 秒）
		ShutdownTimeout: pkgconfig.EnvInt("AUDIT_LOG_SHUTDOWN_TIMEOUT", 5),

		// Rate limiting / 每客户端 IP 令牌桶限流（默认 100 rps，突发 200）
		RateLimitRPS:   pkgconfig.EnvInt("AUDIT_LOG_RATE_LIMIT_RPS", 100),
		RateLimitBurst: pkgconfig.EnvInt("AUDIT_LOG_RATE_LIMIT_BURST", 200),

		// Strict storage mode / 严格存储模式：默认禁止降级回退，存储不可用即启动失败。
		StrictStorage: pkgconfig.EnvBool("AUDIT_LOG_STRICT_STORAGE", pkgconfig.EnvBool("STRICT_STORAGE", true)),
	}
}

// Validate checks that the configuration is consistent and all required files exist.
// Validate 校验配置一致性：TLS 文件存在性、fail-closed 安全不变式与存证留存红线。
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

	if err := pkgconfig.ValidateFailClosed(pkgconfig.SecurityRequirements{
		ServiceName:          "audit-log",
		Hosts:                []string{c.Host, c.GRPCHost},
		APIKey:               c.APIKey,
		TLSEnabled:           c.TLSEnabled,
		RequireTLS:           c.RequireTLS,
		GRPCEnabled:          true,
		MTLSWhitelistFile:    c.MTLSWhitelistFile,
		EncryptionKey:        c.EncryptionKey,
		RequireEncryptionKey: true,
		HashKey:              c.HashKey,
		RequireHashKey:       true,
	}); err != nil {
		return err
	}

	// P1-6 权责分离：只读核验员 Key 与写入 Key 相同等于没做隔离（白名单形同虚设），
	// 反而给运维「核验专区已独立」的错觉，因此直接拒绝启动而不是静默降级。
	if c.ReaderAPIKey != "" && c.ReaderAPIKey == c.APIKey {
		return fmt.Errorf("AUDIT_LOG_READER_API_KEY must differ from AUDIT_LOG_API_KEY (identical keys defeat the P1-6 read-only separation)")
	}

	// P0-8 存证留存红线：0 = 永不删除；开启删除则不得低于三年留存要求，且必须先归档。
	if c.RetentionDays < 0 {
		return fmt.Errorf("AUDIT_LOG_RETENTION_DAYS must be >= 0, got %d", c.RetentionDays)
	}
	if c.RetentionDays > 0 && c.RetentionDays < minEvidenceRetentionDays {
		return fmt.Errorf("AUDIT_LOG_RETENTION_DAYS=%d destroys evidence below the %d-day (3-year) retention floor; use 0 to disable deletion",
			c.RetentionDays, minEvidenceRetentionDays)
	}
	if c.RetentionDays > 0 && strings.TrimSpace(c.ArchiveDir) == "" {
		return fmt.Errorf("AUDIT_LOG_RETENTION_DAYS>0 requires AUDIT_LOG_ARCHIVE_DIR so overdue records are archived before deletion")
	}
	if c.RetentionDays > 0 && strings.TrimSpace(c.EncryptionKey) == "" {
		return fmt.Errorf("AUDIT_LOG_RETENTION_DAYS>0 requires AUDIT_LOG_ENCRYPTION_KEY so archived evidence is written encrypted")
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
