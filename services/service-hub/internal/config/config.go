// Package config provides centralized configuration for the service-hub module.
// Package config 为数据服务调度中枢模块（service-hub）提供集中化配置管理。
//
// 该模块负责从环境变量中解析自身 HTTP/gRPC 网络参数、上游 PrivShield Agent 引擎地址、
// datasource-mgr 数据源服务连接、SQLite 任务持久化路径、mTLS 双向证书与公钥固定，
// 并提供安全合理的回退默认值。
package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	pkgagent "github.com/fengzhizi319/PrivShield-go/pkg/agent"
	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
	"github.com/tjfoc/gmsm/gmtls"
)

// ErrAuditEndpointRequired 表示中枢未配置任何 audit-log 存证端点，出域动作将无法留痕（P0-6）。
// 纯环回监听的本机开发形态下启动即拒绝（loud startup rejection），避免「静默不存证」；
// 存在非环回监听时保持启动但每一条出域任务在 audit 阶段失败（fail-closed），由 main 记录 ERROR 告警。
var ErrAuditEndpointRequired = errors.New("audit-log evidence endpoint must be configured: set SERVICE_HUB_AUDIT_LOG_URLS (or SERVICE_HUB_AUDIT_HTTP) to the audit-log service, e.g. http://audit-log:8084")

// Config holds all runtime configuration for the service-hub server.
// Config 结构体保存数据服务调度中枢服务器运行时的所有配置项。
type Config struct {
	// HTTP REST 服务网络监听参数
	Host string // HTTP 监听主机地址（默认 127.0.0.1）
	Port int    // HTTP 监听端口（默认 8082）

	// 上游 PrivShield Agent 核心引擎连接配置
	AgentRESTHost   string // Agent REST 主机地址（默认 127.0.0.1）
	AgentRESTPort   int    // Agent REST 端口（默认 8079）
	AgentAPIKey     string // 访问上游 Agent 接口所需的 API Key 认证密钥
	MaxQueueDepth   int    // 调度引擎最大任务等待队列深度（默认 1000）
	ScheduleTimeout int    // 任务单步调度与执行超时时间（秒，默认 30）

	// 上游 Agent 出站 REST 客户端传输信任配置（PRIVACY_AGENT_TLS_* / PRIVACY_AGENT_TLCP_*）。
	AgentTLSCAFile              string // 校验 agent https 服务端证书的根 CA PEM 路径（为空用系统根 CA）
	AgentTLSInsecureSkipVerify  bool   // 跳过 agent 服务端证书校验（仅开发/演练）
	AgentTLCPCAFile             string // 校验 agent TLCP 服务端 SM2 证书链的根 CA PEM 路径
	AgentTLCPInsecureSkipVerify bool   // TLCP 模式下跳过服务端证书校验（仅开发/演练）

	// datasource-mgr 模拟数据源服务连接配置
	DatasourceRESTHost string // 数据源服务 HTTP REST 主机地址（默认 127.0.0.1）
	DatasourceRESTPort int    // 数据源服务 HTTP REST 端口（默认 8083）
	DatasourceGRPCHost string // 数据源服务 gRPC 主机地址（默认 127.0.0.1）
	DatasourceGRPCPort int    // 数据源服务 gRPC 端口（默认 50053）
	DatasourceAPIKey   string // 访问 datasource-mgr 所需的出站 API Key（可选）

	// ── P0-6: 出域 ↔ 存证代码级绑定（service-hub ➔ audit-log 存证客户端）──
	// 流水线第 ⑥ 阶段（audit）必须真实写入一条存证；提交失败即任务失败，禁止静默完成。
	AuditLogBaseURLs    []string // audit-log REST 基础地址列表（多副本轮询；空即未配置）
	AuditLogAPIKey      string   // audit-log 入站鉴权 Key（以 Authorization: Bearer 提交）
	AuditLogTimeout     int      // 单次存证提交超时秒数（默认 10）
	AuditLogMaxRetries  int      // 网络错误/5xx 的存证重试次数（默认 3；显式 0 = 不重试）
	AuditLogTLSEnabled  bool     // 是否以 TLS 1.3（可选双向证书）访问存证节点
	AuditLogTLSCertFile string   // 存证客户端证书 PEM 路径（为空回退 SERVICE_HUB_TLS_CERT_FILE）
	AuditLogTLSKeyFile  string   // 存证客户端私钥 PEM 路径（为空回退 SERVICE_HUB_TLS_KEY_FILE）
	AuditLogTLSCAFile   string   // 校验 audit-log 服务端证书的根 CA 路径（为空回退 SERVICE_HUB_TLS_CA_FILE）

	// RequireTLS 由部署方显式声明「本服务必须启用 TLS」（生产编排），
	// 而 TLSEnabled 仍为 false 时启动即失败（P0-1 零信任默认态）。
	RequireTLS bool // 环境变量 SERVICE_HUB_REQUIRE_TLS（默认 false）

	// gRPC 远程过程调用服务网络监听参数
	GRPCHost string // gRPC 监听主机地址（默认 127.0.0.1）
	GRPCPort int    // gRPC 监听端口（默认 50052）

	// mTLS 双向传输层安全认证配置
	TLSEnabled    bool   // 是否在 gRPC/HTTPS 服务端启用 TLS/mTLS 强加密
	TLSCertFile   string // 服务端 X.509 证书 PEM 文件路径
	TLSKeyFile    string // 服务端私钥 PEM 文件路径
	TLSCAFile     string // 验证调用方客户端身份的受信任根 CA 证书路径
	TLSClientAuth string // 客户端认证模式："require"（强制双向校验）| "verify" | "request" | ""

	// mTLS CN 白名单配置（gRPC 服务端 method-scope 鉴权）
	MTLSWhitelistFile string // 客户端证书 CN 白名单 YAML 文件路径（为空则关闭 gRPC 层鉴权）

	// 应用层公钥指纹固定（SPKI Pinning，防御 CA 劫持与伪造）
	TLSPinnedPubKeyFile string // 固定的客户端 RSA 公钥 PEM 文件路径

	// 生产安全加固与持久化配置
	APIKey      string                        // 本模块对外暴露接口的入站鉴权 API Key（为空表示免密）
	ScopeKeys   map[string]*pkgauth.KeyConfig // Scope-based API Key 映射（SERVICE_HUB_API_KEYS），优先于 APIKey
	KeysFile    string                        // API Key 文件路径（SERVICE_HUB_API_KEYS_FILE），启用热轮转
	CORSOrigins []string                      // 允许跨域访问的 Origin 来源白名单
	DBPath      string                        // SQLite 任务数据库文件物理路径（为空表示使用进程内内存存储）
	LogFormat   string                        // 日志输出格式："json"（生产推荐）或 "text"（开发可读）
	LogLevel    string                        // 日志输出级别："debug", "info", "warn", "error"

	// StrictStorage 严格存储模式（P0-4 禁静音降级）：为真时，已配置 SERVICE_HUB_PG_DSN
	// 却探测失败的进程**拒绝启动**，而不是静默回退到 SQLite/内存 —— 后者会让多副本 Hub
	// 在无人察觉的情况下失去租约语义（ErrLeaseNotSupported），任务被两副本同时领取。
	// 默认 true，可用 SERVICE_HUB_STRICT_STORAGE=false 显式放宽（仅容开发/演练环境）。
	StrictStorage bool

	// Data retention / 数据保留策略
	RetentionDays int // 终态任务保留天数，超期自动清理（0 = 不清理）

	// Graceful shutdown / 优雅关闭
	ShutdownTimeout int // HTTP 优雅关闭超时秒数（默认 5）

	// Rate limiting / 南北向边缘限流（service-hub 为对外网唯一通道，默认全开）
	// 显式关闭手段：SERVICE_HUB_RATE_LIMIT_ENABLED=false，或将对应 RPS 设为 <=0。
	RateLimitEnabled bool // 边缘限流总开关（SERVICE_HUB_RATE_LIMIT_ENABLED，默认 true）
	RateLimitRPS     int  // 每客户端 IP 令牌桶每秒请求数（默认 100；<=0 时关闭 IP 级限流）
	RateLimitBurst   int  // IP 级令牌桶突发容量（默认 200）

	// 身份级细粒度限流（挂载在鉴权之后）：key = 认证身份 + 归一化路径（未认证回退客户端 IP），
	// 防止单个 API 身份 / 单 IP 对特定端点洪泛。/health、/readyz、/metrics 探针端点豁免。
	RateLimitPerIdentityRPS   int // 每身份每路径每秒请求数（默认 50；<=0 时关闭身份级限流）
	RateLimitPerIdentityBurst int // 身份级令牌桶突发容量（默认 100）

	// ── Phase B: PostgreSQL 多副本 Hub 配置 ──
	PGDSN     string // PostgreSQL 连接字符串（为空时回退 SQLite）
	PGMaxConn int    // PostgreSQL 最大连接池大小（默认 10）
	PGMinConn int    // PostgreSQL 最小连接池大小（默认 2）
	LeaseTTL  int    // 任务租约 TTL 秒数（默认 60）
}

// Load reads configuration from environment variables with fallback defaults.
// Load 函数从系统环境变量中读取各项配置，若未设置则自动回退至预设的安全默认值。
// 执行步骤：
// 1. 调用 pkgconfig.Env* 依次解析 HTTP、Agent、Datasource、gRPC、mTLS、DB 与日志参数；
// 2. 构造并返回初始化的 *Config 实例。
func Load() *Config {
	return &Config{
		Host:            pkgconfig.EnvString("SERVICE_HUB_HOST", "127.0.0.1"),
		Port:            pkgconfig.EnvInt("SERVICE_HUB_PORT", 8082),
		AgentRESTHost:   pkgconfig.EnvString("PRIVACY_AGENT_REST_HOST", "127.0.0.1"),
		AgentRESTPort:   pkgconfig.EnvInt("PRIVACY_REST_PORT", 8079),
		AgentAPIKey:     pkgconfig.EnvString("PRIVACY_AGENT_API_KEY", ""),
		MaxQueueDepth:   pkgconfig.EnvInt("SERVICE_HUB_MAX_QUEUE", 1000),
		ScheduleTimeout: pkgconfig.EnvInt("SERVICE_HUB_SCHEDULE_TIMEOUT", 30),

		// 上游 Agent 出站 REST 客户端传输信任配置（PRIVACY_AGENT_* 前缀，与 agent 服务端 AGENT_* 区分）：
		//   PRIVACY_AGENT_URLS 基础地址为 https 时由 PRIVACY_AGENT_TLS_CA_FILE / PRIVACY_AGENT_TLS_INSECURE_SKIP_VERIFY
		//   构建标准 TLS 信任；为 tlcp:// 时由 PRIVACY_AGENT_TLCP_CA_FILE / PRIVACY_AGENT_TLCP_INSECURE_SKIP_VERIFY
		//   构建国密 TLCP 客户端配置。均未配置时保持默认行为（http 明文或系统根 CA 校验）。
		AgentTLSCAFile:              pkgconfig.EnvString("PRIVACY_AGENT_TLS_CA_FILE", ""),
		AgentTLSInsecureSkipVerify:  pkgconfig.EnvBool("PRIVACY_AGENT_TLS_INSECURE_SKIP_VERIFY", false),
		AgentTLCPCAFile:             pkgconfig.EnvString("PRIVACY_AGENT_TLCP_CA_FILE", ""),
		AgentTLCPInsecureSkipVerify: pkgconfig.EnvBool("PRIVACY_AGENT_TLCP_INSECURE_SKIP_VERIFY", false),

		// Datasource Mgr 数据源服务连接参数
		DatasourceRESTHost: pkgconfig.EnvString("DATASOURCE_MGR_HOST", "127.0.0.1"),
		DatasourceRESTPort: pkgconfig.EnvInt("DATASOURCE_MGR_PORT", 8083),
		DatasourceGRPCHost: pkgconfig.EnvString("DATASOURCE_MGR_GRPC_HOST", "127.0.0.1"),
		DatasourceGRPCPort: pkgconfig.EnvInt("DATASOURCE_MGR_GRPC_PORT", 50053),
		DatasourceAPIKey:   pkgconfig.EnvString("SERVICE_HUB_DATASOURCE_API_KEY", ""),

		// ── P0-6: 出域存证客户端（audit-log 服务）──
		// 端点解析顺序：SERVICE_HUB_AUDIT_LOG_URLS（逗号分隔多副本）
		// ➔ SERVICE_HUB_AUDIT_HTTP（docker-compose.app-lz.yml 已注入的别名）
		// ➔ 默认与内置编排一致的全栈服务地址 http://audit-log:8084。
		AuditLogBaseURLs: auditLogURLsFromEnv(),
		// Key 解析顺序：SERVICE_HUB_AUDIT_LOG_API_KEY ➔ AUDIT_LOG_API_KEY（存证服务自身的入站密钥）。
		AuditLogAPIKey:      auditLogAPIKeyFromEnv(),
		AuditLogTimeout:     pkgconfig.EnvInt("SERVICE_HUB_AUDIT_LOG_TIMEOUT", 10),
		AuditLogMaxRetries:  pkgconfig.EnvInt("SERVICE_HUB_AUDIT_LOG_MAX_RETRIES", 3),
		AuditLogTLSEnabled:  pkgconfig.EnvBool("SERVICE_HUB_AUDIT_LOG_TLS_ENABLED", false),
		AuditLogTLSCertFile: pkgconfig.EnvString("SERVICE_HUB_AUDIT_LOG_TLS_CERT_FILE", ""),
		AuditLogTLSKeyFile:  pkgconfig.EnvString("SERVICE_HUB_AUDIT_LOG_TLS_KEY_FILE", ""),
		AuditLogTLSCAFile:   pkgconfig.EnvString("SERVICE_HUB_AUDIT_LOG_TLS_CA_FILE", ""),

		// P0-1 零信任默认态：生产编排显式声明必须 TLS。
		RequireTLS: pkgconfig.EnvBool("SERVICE_HUB_REQUIRE_TLS", false),

		// gRPC 服务监听参数（默认 127.0.0.1:50052）
		GRPCHost: pkgconfig.EnvString("SERVICE_HUB_GRPC_HOST", "127.0.0.1"),
		GRPCPort: pkgconfig.EnvInt("SERVICE_HUB_GRPC_PORT", 50052),

		// mTLS 双向传输层安全认证配置
		TLSEnabled:    pkgconfig.EnvBool("SERVICE_HUB_TLS_ENABLED", false),
		TLSCertFile:   pkgconfig.EnvString("SERVICE_HUB_TLS_CERT_FILE", ""),
		TLSKeyFile:    pkgconfig.EnvString("SERVICE_HUB_TLS_KEY_FILE", ""),
		TLSCAFile:     pkgconfig.EnvString("SERVICE_HUB_TLS_CA_FILE", ""),
		TLSClientAuth: pkgconfig.EnvString("SERVICE_HUB_TLS_CLIENT_AUTH", ""),

		// mTLS CN 白名单配置（全局白名单文件，所有 Go gRPC 服务端共享）
		MTLSWhitelistFile: pkgconfig.EnvString("PRIVACY_AUTH_MTLS_WHITELIST_FILE", ""),

		// 客户端 RSA 公钥固定
		TLSPinnedPubKeyFile: pkgconfig.EnvString("SERVICE_HUB_TLS_PINNED_PUBKEY_FILE", ""),

		// 生产鉴权、跨域与存储参数
		APIKey:      pkgconfig.EnvString("SERVICE_HUB_API_KEY", ""),
		ScopeKeys:   pkgauth.LoadAPIKeysFromEnv("SERVICE_HUB_API_KEYS"),
		KeysFile:    pkgconfig.EnvString("SERVICE_HUB_API_KEYS_FILE", ""),
		CORSOrigins: pkgconfig.EnvStringSlice("SERVICE_HUB_CORS_ORIGINS"),
		DBPath:      pkgconfig.EnvString("SERVICE_HUB_DB_PATH", ""),
		LogFormat:   pkgconfig.EnvString("SERVICE_HUB_LOG_FORMAT", "json"),
		LogLevel:    pkgconfig.EnvString("SERVICE_HUB_LOG_LEVEL", "info"),

		// P0-4 禁静音降级：默认严格，专用变量优先于全局变量。
		StrictStorage: pkgconfig.EnvBool("SERVICE_HUB_STRICT_STORAGE", pkgconfig.EnvBool("STRICT_STORAGE", true)),

		// Data retention / 数据保留策略（默认 30 天）
		RetentionDays: pkgconfig.EnvInt("SERVICE_HUB_RETENTION_DAYS", 30),

		// Graceful shutdown / 优雅关闭超时（默认 5 秒）
		ShutdownTimeout: pkgconfig.EnvInt("SERVICE_HUB_SHUTDOWN_TIMEOUT", 5),

		// Rate limiting / 南北向边缘限流（默认全开；SERVICE_HUB_RATE_LIMIT_ENABLED=false 或 RPS<=0 关闭）
		RateLimitEnabled:          pkgconfig.EnvBool("SERVICE_HUB_RATE_LIMIT_ENABLED", true),
		RateLimitRPS:              pkgconfig.EnvInt("SERVICE_HUB_RATE_LIMIT_RPS", 100),
		RateLimitBurst:            pkgconfig.EnvInt("SERVICE_HUB_RATE_LIMIT_BURST", 200),
		RateLimitPerIdentityRPS:   pkgconfig.EnvInt("SERVICE_HUB_RATE_LIMIT_PER_IDENTITY_RPS", 50),
		RateLimitPerIdentityBurst: pkgconfig.EnvInt("SERVICE_HUB_RATE_LIMIT_PER_IDENTITY_BURST", 100),

		// ── Phase B: PostgreSQL 多副本 Hub 配置 ──
		PGDSN:     pkgconfig.EnvString("SERVICE_HUB_PG_DSN", ""),
		PGMaxConn: pkgconfig.EnvInt("SERVICE_HUB_PG_MAX_CONNS", 10),
		PGMinConn: pkgconfig.EnvInt("SERVICE_HUB_PG_MIN_CONNS", 2),
		LeaseTTL:  pkgconfig.EnvInt("SERVICE_HUB_LEASE_TTL", 60),
	}
}

// Validate checks that the configuration is consistent and all required files exist.
// Validate 校验配置一致性并在启动早期快速失败：
//
//  1. TLS 启用时确认证书/私钥文件存在；
//  2. 出域存证（P0-6）：
//     - 启用存证 TLS 时确认客户端证书/私钥/CA 可读；
//     - 未配置任何 audit-log 端点时，若全部监听地址均为环回（本机开发形态）→ 直接拒绝启动，
//     避免「无存证链路却自以为已留痕」；若存在非环回监听 → 允许启动（由 main 记录 ERROR 告警），
//     但流水线 audit 阶段会让每一条出域任务失败（fail-closed），数据不可能无声出域；
//  3. 零信任默认态（P0-1）：统一委托 pkgconfig.ValidateFailClosed ——
//     非环回监听必须配置入站 API Key、RequireTLS 必须真正启用 TLS、
//     gRPC 启用 TLS 时必须提供 mTLS CN 白名单文件。
func (c *Config) Validate() error {
	if c.TLSEnabled {
		if c.TLSCertFile == "" {
			return fmt.Errorf("TLS enabled but SERVICE_HUB_TLS_CERT_FILE is not set")
		}
		if c.TLSKeyFile == "" {
			return fmt.Errorf("TLS enabled but SERVICE_HUB_TLS_KEY_FILE is not set")
		}
		if _, err := os.Stat(c.TLSCertFile); err != nil {
			return fmt.Errorf("TLS cert file not accessible: %s: %w", c.TLSCertFile, err)
		}
		if _, err := os.Stat(c.TLSKeyFile); err != nil {
			return fmt.Errorf("TLS key file not accessible: %s: %w", c.TLSKeyFile, err)
		}
	}

	if c.AuditLogTLSEnabled {
		certFile := firstConfigured(c.AuditLogTLSCertFile, c.TLSCertFile)
		keyFile := firstConfigured(c.AuditLogTLSKeyFile, c.TLSKeyFile)
		caFile := firstConfigured(c.AuditLogTLSCAFile, c.TLSCAFile)
		if certFile == "" || keyFile == "" {
			return fmt.Errorf("audit-log evidence TLS enabled but no client certificate/key pair is configured " +
				"(set SERVICE_HUB_AUDIT_LOG_TLS_CERT_FILE and SERVICE_HUB_AUDIT_LOG_TLS_KEY_FILE, or the SERVICE_HUB_TLS_* fallbacks)")
		}
		for _, path := range []string{certFile, keyFile, caFile} {
			if path == "" {
				continue
			}
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("audit-log evidence TLS file not accessible: %s: %w", path, err)
			}
		}
	}

	if len(c.AuditLogURLs()) == 0 && loopbackOnlyBind(c.Host, c.GRPCHost) {
		return fmt.Errorf("service-hub: %w", ErrAuditEndpointRequired)
	}

	return pkgconfig.ValidateFailClosed(pkgconfig.SecurityRequirements{
		ServiceName:       "service-hub",
		Hosts:             []string{c.Host, c.GRPCHost},
		APIKey:            c.APIKey,
		AuthEnabled:       c.APIKey != "" || len(c.ScopeKeys) > 0,
		TLSEnabled:        c.TLSEnabled,
		RequireTLS:        c.RequireTLS,
		GRPCEnabled:       true, // 中枢进程始终监听 gRPC（默认 :50052）
		MTLSWhitelistFile: c.MTLSWhitelistFile,
		AllowedCIDRs:      pkgconfig.EnvStringSlice("PRIVACY_ALLOWED_CIDRS"),
	})
}

// auditLogURLsFromEnv resolves the evidence endpoint list from the environment.
// auditLogURLsFromEnv 按 SERVICE_HUB_AUDIT_LOG_URLS ➔ SERVICE_HUB_AUDIT_HTTP ➔ 内置编排默认值解析。
func auditLogURLsFromEnv() []string {
	if urls := pkgconfig.EnvStringSlice("SERVICE_HUB_AUDIT_LOG_URLS"); len(urls) > 0 {
		return urls
	}
	if single := pkgconfig.EnvString("SERVICE_HUB_AUDIT_HTTP", ""); single != "" {
		return []string{single}
	}
	return []string{"http://audit-log:8084"}
}

// auditLogAPIKeyFromEnv resolves the outbound evidence API key.
// auditLogAPIKeyFromEnv 优先读取中枢专用变量，回退到 audit-log 服务自身的入站密钥变量，
// 使单机/同命名空间部署只需注入一次 AUDIT_LOG_API_KEY 即可打通存证链路。
func auditLogAPIKeyFromEnv() string {
	if key := pkgconfig.EnvString("SERVICE_HUB_AUDIT_LOG_API_KEY", ""); key != "" {
		return key
	}
	return pkgconfig.EnvString("AUDIT_LOG_API_KEY", "")
}

// AuditLogURLs returns the configured audit-log evidence endpoints (no hidden defaults).
// AuditLogURLs 返回已配置的存证端点列表（自动剔除空白项）；未配置时返回 nil，
// 由调用方按 fail-closed 处理。剔除规则与 audit 客户端构造期的 trimURLs 保持一致，
// 避免「Validate 认为已配置、客户端认为未配置」的双重口径。
func (c *Config) AuditLogURLs() []string {
	if c == nil {
		return nil
	}
	urls := make([]string, 0, len(c.AuditLogBaseURLs))
	for _, u := range c.AuditLogBaseURLs {
		if strings.TrimSpace(u) != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return nil
	}
	return urls
}

// AuditLogTimeoutDuration exposes the per-submission evidence timeout.
// AuditLogTimeoutDuration 返回单次存证提交的超时时间（非正值回退 10s）。
func (c *Config) AuditLogTimeoutDuration() time.Duration {
	seconds := 10
	if c != nil && c.AuditLogTimeout > 0 {
		seconds = c.AuditLogTimeout
	}
	return time.Duration(seconds) * time.Second
}

// loopbackOnlyBind reports whether every configured listen host is loopback-only.
// loopbackOnlyBind 判断给定的全部监听地址是否均为环回（仅本机可访问）。
func loopbackOnlyBind(hosts ...string) bool {
	for _, h := range hosts {
		if !pkgconfig.IsLoopbackHost(h) {
			return false
		}
	}
	return true
}

// firstConfigured returns the first non-empty trimmed value.
// firstConfigured 返回第一个非空（去空白）配置值。
func firstConfigured(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Address returns the full HTTP listen address formatted as "host:port".
// Address 返回完整的 HTTP 服务网络监听地址（如 "127.0.0.1:8082" 或 "0.0.0.0:8082"）。
func (c *Config) Address() string {
	return c.Host + ":" + strconv.Itoa(c.Port)
}

// AgentBaseURL returns the upstream agent REST base URL.
// AgentBaseURL 返回默认单实例上游 Agent 引擎的 HTTP REST 基础 URL（如 "http://127.0.0.1:8079"）。
func (c *Config) AgentBaseURL() string {
	return "http://" + c.AgentRESTHost + ":" + strconv.Itoa(c.AgentRESTPort)
}

// AgentBaseURLs returns all configured upstream agent REST base URLs for load balancing/failover.
// AgentBaseURLs 返回所有已配置的上游 Agent 引擎 REST URL 列表：
// 优先读取 PRIVACY_AGENT_URLS 环境变量（逗号分隔的多个 Agent 地址），未配置时回退为单个 AgentBaseURL()。
func (c *Config) AgentBaseURLs() []string {
	envURLs := pkgconfig.EnvStringSlice("PRIVACY_AGENT_URLS")
	if len(envURLs) > 0 {
		return envURLs
	}
	return []string{c.AgentBaseURL()}
}

// AgentTLSClientConfig 由显式配置构建上游 agent 的标准 TLS 客户端信任配置。
// 未配置 CA 且未开启 skip-verify 时返回 (nil, nil)，保持默认行为。
func (c *Config) AgentTLSClientConfig() (*tls.Config, error) {
	return pkgagent.NewTLSConfig(c.AgentTLSCAFile, c.AgentTLSInsecureSkipVerify)
}

// AgentTLCPClientConfig 由显式配置构建上游 agent 的国密 TLCP 客户端配置。
// 未配置 CA 且未开启 skip-verify 时返回 (nil, nil)（未启用 TLCP 传输）。
func (c *Config) AgentTLCPClientConfig() (*gmtls.Config, error) {
	return pkgagent.NewTLCPConfig(c.AgentTLCPCAFile, c.AgentTLCPInsecureSkipVerify)
}

// DatasourceBaseURL returns the datasource manager HTTP base URL.
// DatasourceBaseURL 返回模拟数据源服务的 HTTP REST 基础 URL（如 "http://127.0.0.1:8083"）。
func (c *Config) DatasourceBaseURL() string {
	return "http://" + c.DatasourceRESTHost + ":" + strconv.Itoa(c.DatasourceRESTPort)
}

// DatasourceGRPCAddress returns the datasource manager gRPC address.
// DatasourceGRPCAddress 返回模拟数据源服务的 gRPC 监听网络地址（如 "127.0.0.1:50053"）。
func (c *Config) DatasourceGRPCAddress() string {
	return c.DatasourceGRPCHost + ":" + strconv.Itoa(c.DatasourceGRPCPort)
}

// GRPCAddress returns the full gRPC listen address formatted as "host:port".
// GRPCAddress 返回 service-hub 自身 gRPC 服务的网络监听地址（如 "127.0.0.1:50052"）。
func (c *Config) GRPCAddress() string {
	return c.GRPCHost + ":" + strconv.Itoa(c.GRPCPort)
}
