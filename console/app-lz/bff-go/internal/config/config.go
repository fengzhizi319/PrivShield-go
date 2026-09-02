// Package config 负责从环境变量加载 App-LZ BFF 的全部运行时配置。
//
// 环境变量命名规则：
//   - 优先读取 APP_LZ_* 前缀变量（推荐）
//   - 兼容无前缀的旧变量名（如 HUB_URL），方便向后兼容
//
// 每个上游微服务需要配置两个地址：
//   - HTTP URL（如 http://127.0.0.1:8082）—— 用于 REST API 调用
//   - GRPC 地址（如 127.0.0.1:50052）—— 用于拓扑探测时尝试 gRPC 连接
package config

import (
	"fmt"
	"os"

	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
)

// Config 保存 App-LZ BFF 的全部运行时配置项。
// 所有字段在 Load() 时从环境变量一次性读取，运行期不可变。
type Config struct {
	// ── HTTP Server 监听配置 ──
	Host string // 监听地址，默认 0.0.0.0
	Port string // 监听端口，默认 8085

	// ── 上游微服务 HTTP URL ──
	HubURL        string // Service Hub 调度中枢 REST 地址（默认 http://127.0.0.1:8082）
	DatasourceURL string // 数据源管理器 REST 地址（默认 http://127.0.0.1:8083）
	AuditURL      string // 审计存证服务 REST 地址（默认 http://127.0.0.1:8084）
	AgentURL      string // 隐私脱敏引擎 REST 地址（默认 http://127.0.0.1:8079）

	// ── 上游微服务 API Key（出站零信任认证）──
	HubAPIKey        string // Service Hub 出站 API Key（可选）
	DatasourceAPIKey string // 数据源管理器出站 API Key（可选）
	AuditAPIKey      string // 审计存证服务出站 API Key（可选）
	AgentAPIKey      string // 隐私脱敏引擎出站 API Key（可选）

	// ── 上游微服务 gRPC 地址（用于拓扑探测）──
	HubGRPC        string // Service Hub gRPC 地址（默认 127.0.0.1:50052）
	DatasourceGRPC string // 数据源管理器 gRPC 地址（默认 127.0.0.1:50053）
	AuditGRPC      string // 审计存证服务 gRPC 地址（默认 127.0.0.1:50054）
	AgentGRPC      string // 隐私脱敏引擎 gRPC 地址（默认 127.0.0.1:50051）

	// ── 静态文件 & 日志 ──
	StaticDir string // 前端 SPA 构建产物目录（默认 ./web/dist）
	LogFormat string // 日志输出格式："json"（生产推荐）或 "text"（开发可读）
	LogLevel  string // 日志级别（默认 info）

	// ── TLS 配置 ──
	TLSEnabled   bool   // 是否启用 TLS（默认 false）
	RequireTLS   bool   // 部署方声明必须以加密面暴露（APP_LZ_REQUIRE_TLS）：TLS 关闭即拒绝启动
	CertFile     string // TLS 证书文件路径
	KeyFile      string // TLS 私钥文件路径
	ClientCAFile string // 客户端 CA 证书路径（用于 mTLS）

	// ── 认证配置 ──
	APIKey string // API Key（用于 BFF 自身的出站认证校验）

	// ── RBAC 用户认证配置 ──
	AuthEnabled    bool   // 是否启用用户认证（JWT），默认 false（开发模式放行）
	JWTSecret      string // JWT 签名密钥（最少 32 字符）
	JWTExpiryHours int    // JWT 令牌有效期（小时，默认 1）
	UserDBPath     string // 用户数据持久化路径（空 = 内存模式）

	// ── 限流配置 ──
	RateLimitRPS   int // 每客户端 IP 每秒允许请求数（默认 100，0 = 不限流）
	RateLimitBurst int // 令牌桶突发容量（默认 200）
}

// Load 从环境变量加载配置，未设置时使用合理默认值。
//
// 环境变量优先级：APP_LZ_* 前缀 > 无前缀旧变量 > 硬编码默认值。
// 例如 HubURL 的读取顺序：APP_LZ_HUB_URL → HUB_URL → http://127.0.0.1:8082
func Load() *Config {
	// ── Server 监听地址 ──
	host := pkgconfig.EnvString("APP_LZ_HOST", "0.0.0.0")
	port := pkgconfig.EnvString("APP_LZ_PORT", "8085")

	// ── 上游微服务双协议地址（HTTP + gRPC）──
	// 每个服务先用 EnvString 读 APP_LZ_* 前缀变量，若未设置则 fallback 到无前缀旧变量，
	// 最终 fallback 到硬编码默认值（本地开发环境地址）。
	hubURL := pkgconfig.EnvString("APP_LZ_HUB_URL", pkgconfig.EnvString("HUB_URL", "http://127.0.0.1:8082"))
	hubGRPC := pkgconfig.EnvString("APP_LZ_HUB_GRPC", pkgconfig.EnvString("HUB_GRPC", "127.0.0.1:50052"))
	datasourceURL := pkgconfig.EnvString("APP_LZ_DATASOURCE_URL", pkgconfig.EnvString("DATASOURCE_URL", "http://127.0.0.1:8083"))
	datasourceGRPC := pkgconfig.EnvString("APP_LZ_DATASOURCE_GRPC", pkgconfig.EnvString("DATASOURCE_GRPC", "127.0.0.1:50053"))
	auditURL := pkgconfig.EnvString("APP_LZ_AUDIT_URL", pkgconfig.EnvString("AUDIT_URL", "http://127.0.0.1:8084"))
	auditGRPC := pkgconfig.EnvString("APP_LZ_AUDIT_GRPC", pkgconfig.EnvString("AUDIT_GRPC", "127.0.0.1:50054"))
	agentURL := pkgconfig.EnvString("APP_LZ_AGENT_URL", pkgconfig.EnvString("AGENT_URL", "http://127.0.0.1:8079"))
	agentGRPC := pkgconfig.EnvString("APP_LZ_AGENT_GRPC", pkgconfig.EnvString("AGENT_GRPC", "127.0.0.1:50051"))

	// ── 上游微服务出站 API Key（可选，用于服务间认证）──
	hubAPIKey := pkgconfig.EnvString("APP_LZ_HUB_API_KEY", pkgconfig.EnvString("HUB_API_KEY", ""))
	datasourceAPIKey := pkgconfig.EnvString("APP_LZ_DATASOURCE_API_KEY", pkgconfig.EnvString("DATASOURCE_API_KEY", ""))
	auditAPIKey := pkgconfig.EnvString("APP_LZ_AUDIT_API_KEY", pkgconfig.EnvString("AUDIT_API_KEY", ""))
	agentAPIKey := pkgconfig.EnvString("APP_LZ_AGENT_API_KEY", pkgconfig.EnvString("AGENT_API_KEY", ""))

	// ── 静态文件 & 日志 ──
	staticDir := pkgconfig.EnvString("APP_LZ_STATIC_DIR", "./web/dist")
	logFormat := pkgconfig.EnvString("APP_LZ_LOG_FORMAT", "json")
	logLevel := pkgconfig.EnvString("APP_LZ_LOG_LEVEL", "info")

	// ── TLS 配置 ──
	tlsEnabled := pkgconfig.EnvBool("APP_LZ_TLS_ENABLED", false)
	requireTLS := pkgconfig.EnvBool("APP_LZ_REQUIRE_TLS", false)
	certFile := pkgconfig.EnvString("APP_LZ_CERT_FILE", "")
	keyFile := pkgconfig.EnvString("APP_LZ_KEY_FILE", "")
	clientCAFile := pkgconfig.EnvString("APP_LZ_CLIENT_CA_FILE", "")

	// ── 认证 ──
	apiKey := pkgconfig.EnvString("APP_LZ_API_KEY", "")

	// ── RBAC 用户认证 ──
	authEnabled := pkgconfig.EnvBool("APP_LZ_AUTH_ENABLED", false)
	jwtSecret := pkgconfig.EnvString("APP_LZ_JWT_SECRET", "")
	jwtExpiryHours := pkgconfig.EnvInt("APP_LZ_JWT_EXPIRY_HOURS", 1) // 默认 1 小时，符合等保短会话要求
	userDBPath := pkgconfig.EnvString("APP_LZ_USER_DB_PATH", "")

	return &Config{
		Host:             host,
		Port:             port,
		HubURL:           hubURL,
		HubGRPC:          hubGRPC,
		DatasourceURL:    datasourceURL,
		DatasourceGRPC:   datasourceGRPC,
		AuditURL:         auditURL,
		AuditGRPC:        auditGRPC,
		AgentURL:         agentURL,
		AgentGRPC:        agentGRPC,
		HubAPIKey:        hubAPIKey,
		DatasourceAPIKey: datasourceAPIKey,
		AuditAPIKey:      auditAPIKey,
		AgentAPIKey:      agentAPIKey,
		StaticDir:        staticDir,
		LogFormat:        logFormat,
		LogLevel:         logLevel,
		TLSEnabled:       tlsEnabled,
		RequireTLS:       requireTLS,
		CertFile:         certFile,
		KeyFile:          keyFile,
		ClientCAFile:     clientCAFile,
		APIKey:           apiKey,
		AuthEnabled:      authEnabled,
		JWTSecret:        jwtSecret,
		JWTExpiryHours:   jwtExpiryHours,
		UserDBPath:       userDBPath,

		// ── 限流 ──
		RateLimitRPS:   pkgconfig.EnvInt("APP_LZ_RATE_LIMIT_RPS", 100),
		RateLimitBurst: pkgconfig.EnvInt("APP_LZ_RATE_LIMIT_BURST", 200),
	}
}

// Validate 校验配置的一致性，在启动早期（fail-fast）发现致命配置错误。
//
// 当前校验规则：
//   - TLS 启用时，证书文件和私钥文件路径必须非空且在磁盘上可访问
//
// 返回 nil 表示配置合法，可以安全启动。
func (c *Config) Validate() error {
	if c.TLSEnabled {
		// TLS 开启但缺少证书/私钥路径 → 立即失败
		if c.CertFile == "" || c.KeyFile == "" {
			return fmt.Errorf("TLS enabled but APP_LZ_CERT_FILE and/or APP_LZ_KEY_FILE are empty")
		}
		// 确认证书文件在磁盘上存在且当前用户有权限读取
		if _, err := os.Stat(c.CertFile); err != nil {
			return fmt.Errorf("TLS cert file not accessible: %w", err)
		}
		// 确认私钥文件在磁盘上存在且当前用户有权限读取
		if _, err := os.Stat(c.KeyFile); err != nil {
			return fmt.Errorf("TLS key file not accessible: %w", err)
		}
	}

	// P0-1 零信任默认态：Auth 中间件在 Key 为空时整体放行，而本 BFF 默认 0.0.0.0 监听
	// 且聚合了中枢/数据源/存证三类上游，故非环回监听必须配置 APP_LZ_API_KEY。
	return pkgconfig.ValidateFailClosed(pkgconfig.SecurityRequirements{
		ServiceName: "app-lz-bff",
		Hosts:       []string{c.Host},
		APIKey:      c.APIKey,
		TLSEnabled:  c.TLSEnabled,
		RequireTLS:  c.RequireTLS,
		GRPCEnabled: false, // 本 BFF 只有 HTTP 服务端（其 gRPC 端口映射属 P2-1 配置漂移，已删除）
	})
}
