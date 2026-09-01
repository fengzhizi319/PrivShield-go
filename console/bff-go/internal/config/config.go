// Package config provides centralized configuration management for the Go gRPC proxy backend.
// Package config 提供 Go gRPC 代理后端的集中化配置管理。
//
// Design principles / 设计原则：
//   - All configuration is read from environment variables, zero config-file dependency
//     所有配置项均通过环境变量读取，零配置文件依赖
//   - Every field has a sensible local-dev default, ready to use out of the box
//     每项配置均有合理的本地开发默认值，开箱即用
//   - Switch target agent address, listen port, auth info via env vars
//     支持通过环境变量快速切换目标 agent 地址、监听端口、认证信息等
//
// Environment variables / 环境变量清单：
//
//	| Variable                        | Default       | Description                       |
//	|---------------------------------|---------------|-----------------------------------|
//	| PRIVACY_AGENT_GRPC_HOST         | 127.0.0.1     | Upstream agent gRPC host           |
//	| PRIVACY_AGENT_GRPC_PORT         | 50051         | Upstream agent gRPC port           |
//	| PRIVACY_AGENT_API_KEY           | (empty)       | Optional Bearer Token auth key     |
//	| PRIVACY_CONSOLE_HOST            | 127.0.0.1     | This proxy's HTTP listen address   |
//	| PRIVACY_CONSOLE_PORT            | 8081          | This proxy's HTTP listen port      |
//	| PRIVACY_CONSOLE_STATIC_DIR      | ../web/dist   | Frontend dist dir, empty=disable   |
//	| PRIVACY_AGENT_TLS_ENABLED       | false         | Enable TLS/mTLS for upstream gRPC  |
//	| PRIVACY_AGENT_TLS_CERT_FILE     | (empty)       | Client cert file (mTLS)            |
//	| PRIVACY_AGENT_TLS_KEY_FILE      | (empty)       | Client key file (mTLS)             |
//	| PRIVACY_AGENT_TLS_CA_FILE       | (empty)       | CA file to verify server cert      |
//	| PRIVACY_AGENT_TLS_SERVER_NAME   | (empty)       | Server cert hostname override      |
//	| PRIVACY_AGENT_TLS_INSECURE_SKIP_VERIFY | false  | Skip server cert verify (test only)|
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	pkgconfig "github.com/fengzhizi319/PrivShield/pkg/config"
)

// Config holds all runtime configuration for the Go gRPC proxy server.
// Config 保存 Go gRPC 代理服务器运行时的所有配置项。
// Loaded once from env vars via Load(), read-only during runtime.
// 通过 Load() 从环境变量一次性加载，运行期间只读不修改。
type Config struct {
	// AgentGRPCHost：上游 PrivShield gRPC 服务的主机名或 IP 地址。
	// 对应环境变量 PRIVACY_AGENT_GRPC_HOST，默认 "127.0.0.1"。
	AgentGRPCHost string

	// AgentGRPCPort：上游 agent gRPC 服务的监听端口。
	// 对应环境变量 PRIVACY_AGENT_GRPC_PORT，默认 50051。
	// 与 AgentGRPCHost 组合后形成完整的 gRPC 目标地址（如 "127.0.0.1:50051"）。
	AgentGRPCPort int

	// AgentAPIKey：可选的 Bearer Token，用于上游 agent 开启认证时的身份验证。
	// 对应环境变量 PRIVACY_AGENT_API_KEY，默认为空（不认证）。
	// 非空时每次 gRPC 调用会自动附加 "authorization: Bearer <key>" 元数据。
	AgentAPIKey string

	// ConsoleHost：本 Go 代理 HTTP 服务器的绑定地址。
	// 对应环境变量 PRIVACY_CONSOLE_HOST，默认 "127.0.0.1"。
	ConsoleHost string

	// ConsolePort：本 Go 代理 HTTP 服务器的监听端口。
	// 对应环境变量 PRIVACY_CONSOLE_PORT，默认 8081。
	// 与 ConsoleHost 组合后形成完整的 HTTP 监听地址（如 "127.0.0.1:8081"）。
	ConsolePort int

	// StaticDistDir：前端 React 构建产物的目录路径。
	// 对应环境变量 PRIVACY_CONSOLE_STATIC_DIR，默认 "../web/dist"。
	// 当该目录存在时，Go 服务器同时托管 Console UI 静态文件；
	// 设为空字符串则禁用静态托管，仅作为纯 API 代理。
	StaticDistDir string

	// AgentTLSEnabled：是否对上游 agent 的 gRPC 连接启用 TLS/mTLS。
	// 对应环境变量 PRIVACY_AGENT_TLS_ENABLED，默认 false（使用非安全传输）。
	// 启用后必须提供 CA 证书（AgentTLSCAFile）以校验服务端身份。
	AgentTLSEnabled bool

	// AgentTLSCertFile：本代理作为 gRPC 客户端的证书文件路径（PEM）。
	// 对应环境变量 PRIVACY_AGENT_TLS_CERT_FILE，默认空。
	// 与 AgentTLSKeyFile 配对使用，用于向服务端证明客户端身份（mTLS 双向认证）。
	AgentTLSCertFile string

	// AgentTLSKeyFile：本代理作为 gRPC 客户端的私钥文件路径（PEM）。
	// 对应环境变量 PRIVACY_AGENT_TLS_KEY_FILE，默认空。
	// 必须与 AgentTLSCertFile 同时提供，否则无法完成客户端身份认证。
	AgentTLSKeyFile string

	// AgentTLSCAFile：用于校验上游 agent 服务端证书的 CA 证书文件路径（PEM）。
	// 对应环境变量 PRIVACY_AGENT_TLS_CA_FILE，默认空。
	// TLS 启用时必填：客户端用它验证服务端证书是否由受信任 CA 签发。
	AgentTLSCAFile string

	// AgentTLSServerName：TLS 握手时用于校验服务端证书的主机名覆盖值。
	// 对应环境变量 PRIVACY_AGENT_TLS_SERVER_NAME，默认空（使用连接目标地址）。
	// 典型场景：连接 127.0.0.1 但证书 SAN 仅含 localhost 时，设为 "localhost"。
	AgentTLSServerName string

	// AgentTLSInsecureSkipVerify：是否跳过服务端证书校验（仅限测试）。
	// 对应环境变量 PRIVACY_AGENT_TLS_INSECURE_SKIP_VERIFY，默认 false。
	// 设为 true 时不校验服务端证书链与主机名，存在中间人攻击风险，生产环境严禁启用。
	AgentTLSInsecureSkipVerify bool

	// ── 可选安全加固配置（默认关闭 / 宽松，本地开发零配置即可运行）──────────────

	// ConsoleAPIKey：可选的控制台 API Key。
	// 对应环境变量 CONSOLE_API_KEY，默认空（不鉴权）。
	// 非空时 /api/*（除 /api/health）需携带 Authorization: Bearer <key>。
	ConsoleAPIKey string

	// ConsoleRateLimit：每分钟每客户端 IP 的最大请求数。
	// 对应环境变量 CONSOLE_RATE_LIMIT，默认 600；设为 0 关闭限流。
	ConsoleRateLimit int

	// MaxUploadBytes：上传文件大小上限（字节）。
	// 对应环境变量 CONSOLE_MAX_UPLOAD_BYTES，默认 10MB；超限返回 413。
	MaxUploadBytes int64

	// LBAllowedHosts：负载均衡探测目标 host 白名单（逗号分隔）。
	// 对应环境变量 LB_ALLOWED_HOSTS，默认空（不限制，本地探测默认行为）。
	LBAllowedHosts string

	// ── gRPC 重试策略配置（#12）───────────────

	// AgentRetryMaxAttempts：上游 agent gRPC 调用最大重试次数。
	// 对应环境变量 PRIVACY_AGENT_RETRY_MAX_ATTEMPTS，默认 6。
	AgentRetryMaxAttempts int

	// AgentRetryInitialBackoff：重试初始退避时间（秒）。
	// 对应环境变量 PRIVACY_AGENT_RETRY_INITIAL_BACKOFF，默认 1。
	AgentRetryInitialBackoff int

	// AgentRetryMaxBackoff：重试最大退避时间（秒）。
	// 对应环境变量 PRIVACY_AGENT_RETRY_MAX_BACKOFF，默认 8。
	AgentRetryMaxBackoff int

	// ── BFF 入站 HTTP/HTTPS 服务端 TLS 与 mTLS 配置 ─────────────────────

	// ConsoleTLSEnabled：是否为本 BFF HTTP/REST 服务开启 HTTPS (TLS/mTLS)。
	// 对应环境变量 PRIVACY_CONSOLE_TLS_ENABLED，默认 false。
	ConsoleTLSEnabled bool

	// ConsoleRequireTLS：部署方声明本 BFF 必须以加密面暴露（PRIVACY_CONSOLE_REQUIRE_TLS，默认 false）。
	// 置真而 ConsoleTLSEnabled 为假时启动门禁直接拒绝，消除「以为已加密、实际明文直传」。
	ConsoleRequireTLS bool

	// ConsoleTLSCertFile：BFF 服务端 X.509 证书路径（PEM）。
	// 对应环境变量 PRIVACY_CONSOLE_TLS_CERT_FILE。
	ConsoleTLSCertFile string

	// ConsoleTLSKeyFile：BFF 服务端私钥路径（PEM）。
	// 对应环境变量 PRIVACY_CONSOLE_TLS_KEY_FILE。
	ConsoleTLSKeyFile string

	// ConsoleTLSCAFile：用于校验入站客户端证书的 CA 根证书路径（PEM，mTLS 双向认证）。
	// 对应环境变量 PRIVACY_CONSOLE_TLS_CA_FILE。
	ConsoleTLSCAFile string

	// ConsoleTLSClientAuth：客户端认证模式："require" | "verify" | "request" | ""。
	// 对应环境变量 PRIVACY_CONSOLE_TLS_CLIENT_AUTH，默认 ""（单向 TLS）。
	ConsoleTLSClientAuth string

	// ConsoleTLSPinnedPubKeyFile：可选的客户端公钥固定文件路径（PEM）。
	// 对应环境变量 PRIVACY_CONSOLE_TLS_PINNED_PUBKEY_FILE。
	ConsoleTLSPinnedPubKeyFile string

	// ConsoleMTLSWhitelistFile：BFF 入站 gRPC 服务端 mTLS CN 白名单文件路径（YAML）。
	// 对应环境变量 PRIVACY_AUTH_MTLS_WHITELIST_FILE，为空则关闭 gRPC 层 method-scope 鉴权。
	ConsoleMTLSWhitelistFile string

	// ── BFF 入站 gRPC 服务端配置 ─────────────────────────────────────────

	// ConsoleGRPCEnabled：是否同时启动 BFF 自身对外暴露的 gRPC 代理网关服务。
	// 对应环境变量 PRIVACY_CONSOLE_GRPC_ENABLED，默认 false。
	ConsoleGRPCEnabled bool

	// ConsoleGRPCHost：BFF gRPC 服务的绑定主机地址。
	// 对应环境变量 PRIVACY_CONSOLE_GRPC_HOST，默认 "127.0.0.1"。
	ConsoleGRPCHost string

	// ConsoleGRPCPort：BFF gRPC 服务的监听端口。
	// 对应环境变量 PRIVACY_CONSOLE_GRPC_PORT，默认 50055。
	ConsoleGRPCPort int

	// ── 直连 Go 微服务配置（Phase 2：console/bff-go 不再只代理 Python Agent）
	//
	// HubURL：service-hub HTTP REST 基础地址。
	// 对应环境变量 BFF_HUB_URL，默认 "http://127.0.0.1:8082"。
	HubURL string

	// DatasourceURL：datasource-mgr HTTP REST 基础地址。
	// 对应环境变量 BFF_DATASOURCE_URL，默认 "http://127.0.0.1:8083"。
	DatasourceURL string

	// AuditURL：audit-log HTTP REST 基础地址。
	// 对应环境变量 BFF_AUDIT_URL，默认 "http://127.0.0.1:8084"。
	AuditURL string

	// HubAPIKey：访问 service-hub 的 API Key（可选）。
	// 对应环境变量 BFF_HUB_API_KEY。
	HubAPIKey string

	// DatasourceAPIKey：访问 datasource-mgr 的 API Key（可选）。
	// 对应环境变量 BFF_DATASOURCE_API_KEY。
	DatasourceAPIKey string

	// AuditAPIKey：访问 audit-log 的 API Key（可选）。
	// 对应环境变量 BFF_AUDIT_API_KEY。
	AuditAPIKey string
}

// Load reads all configuration from environment variables and returns a populated Config.
// Load 从环境变量读取所有配置项，返回填充完毕的 Config 实例。
//
// Execution logic / 执行逻辑：
//  1. Read each env var in sequence; use default if not set
//     依次读取各环境变量，不存在则使用默认值
//  2. Port fields are auto-parsed to int; fallback to default on parse failure
//     端口号类配置自动解析为 int 类型，解析失败时回退到默认值
//  3. StaticDistDir uses getEnvOptional: explicitly setting empty disables static hosting
//     StaticDistDir 使用 getEnvOptional：显式设为空字符串即禁用静态托管
//
// Typical usage / 典型用法：
//
//	cfg := config.Load()  // called once at startup in main
func Load() *Config {
	return &Config{
		// 上游 agent gRPC 主机地址，默认 127.0.0.1（本地开发场景）
		AgentGRPCHost: pkgconfig.EnvString("PRIVACY_AGENT_GRPC_HOST", "127.0.0.1"),
		// 上游 agent gRPC 端口，默认 50051（与 PrivShield 默认 gRPC 端口一致）
		AgentGRPCPort: pkgconfig.EnvInt("PRIVACY_AGENT_GRPC_PORT", 50051),
		// 认证 API Key，默认为空（不启用认证）
		AgentAPIKey: pkgconfig.EnvString("PRIVACY_AGENT_API_KEY", ""),
		// 本代理 HTTP 监听地址，默认 127.0.0.1
		ConsoleHost: pkgconfig.EnvString("PRIVACY_CONSOLE_HOST", "127.0.0.1"),
		// 本代理 HTTP 监听端口，默认 8081
		ConsolePort: pkgconfig.EnvInt("PRIVACY_CONSOLE_PORT", 8081),
		// 前端静态文件目录，使用 getEnvOptional 以支持"设为空即禁用"语义
		StaticDistDir: getEnvOptional("PRIVACY_CONSOLE_STATIC_DIR", "../web/dist"),
		// 是否启用上游 gRPC 连接的 TLS/mTLS，默认关闭（非安全传输）
		AgentTLSEnabled: pkgconfig.EnvBool("PRIVACY_AGENT_TLS_ENABLED", false) || pkgconfig.EnvBool("PRIVACY_AGENT_MTLS_ENABLED", false),
		// 客户端证书文件（mTLS 双向认证），默认空
		AgentTLSCertFile: getEnvWithFallback("PRIVACY_AGENT_TLS_CERT_FILE", "PRIVACY_AGENT_CLIENT_CERT"),
		// 客户端私钥文件（mTLS 双向认证），默认空
		AgentTLSKeyFile: getEnvWithFallback("PRIVACY_AGENT_TLS_KEY_FILE", "PRIVACY_AGENT_CLIENT_KEY"),
		// 校验服务端证书的 CA 文件，TLS 启用时必填
		AgentTLSCAFile: getEnvWithFallback("PRIVACY_AGENT_TLS_CA_FILE", "PRIVACY_AGENT_CA_CERT"),
		// 服务端证书主机名覆盖值，默认空（使用连接目标地址）
		AgentTLSServerName: getEnvWithFallback("PRIVACY_AGENT_TLS_SERVER_NAME", "PRIVACY_AGENT_SERVER_NAME"),
		// 是否跳过服务端证书校验（仅测试用），默认关闭
		AgentTLSInsecureSkipVerify: pkgconfig.EnvBool("PRIVACY_AGENT_TLS_INSECURE_SKIP_VERIFY", false),
		// 可选控制台 API Key，默认空（不鉴权）
		ConsoleAPIKey: pkgconfig.EnvString("CONSOLE_API_KEY", ""),
		// 限流：每分钟每 IP 最大请求数，默认 600（0 关闭）
		ConsoleRateLimit: pkgconfig.EnvInt("CONSOLE_RATE_LIMIT", 600),
		// 上传文件大小上限，默认 10MB
		MaxUploadBytes: int64(pkgconfig.EnvInt("CONSOLE_MAX_UPLOAD_BYTES", 10*1024*1024)),
		// 负载均衡探测 host 白名单，默认空（不限制）
		LBAllowedHosts: pkgconfig.EnvString("LB_ALLOWED_HOSTS", ""),
		// gRPC 重试策略（#12）：最大重试次数、初始/最大退避秒数
		AgentRetryMaxAttempts:    pkgconfig.EnvInt("PRIVACY_AGENT_RETRY_MAX_ATTEMPTS", 6),
		AgentRetryInitialBackoff: pkgconfig.EnvInt("PRIVACY_AGENT_RETRY_INITIAL_BACKOFF", 1),
		AgentRetryMaxBackoff:     pkgconfig.EnvInt("PRIVACY_AGENT_RETRY_MAX_BACKOFF", 8),
		// BFF 入站 HTTPS/TLS 配置
		ConsoleTLSEnabled:          pkgconfig.EnvBool("PRIVACY_CONSOLE_TLS_ENABLED", false),
		ConsoleRequireTLS:          pkgconfig.EnvBool("PRIVACY_CONSOLE_REQUIRE_TLS", false),
		ConsoleTLSCertFile:         pkgconfig.EnvString("PRIVACY_CONSOLE_TLS_CERT_FILE", ""),
		ConsoleTLSKeyFile:          pkgconfig.EnvString("PRIVACY_CONSOLE_TLS_KEY_FILE", ""),
		ConsoleTLSCAFile:           pkgconfig.EnvString("PRIVACY_CONSOLE_TLS_CA_FILE", ""),
		ConsoleTLSClientAuth:       pkgconfig.EnvString("PRIVACY_CONSOLE_TLS_CLIENT_AUTH", ""),
		ConsoleTLSPinnedPubKeyFile: pkgconfig.EnvString("PRIVACY_CONSOLE_TLS_PINNED_PUBKEY_FILE", ""),
		ConsoleMTLSWhitelistFile:   pkgconfig.EnvString("PRIVACY_AUTH_MTLS_WHITELIST_FILE", ""),
		// BFF 入站 gRPC 服务端配置
		ConsoleGRPCEnabled: pkgconfig.EnvBool("PRIVACY_CONSOLE_GRPC_ENABLED", false),
		ConsoleGRPCHost:    pkgconfig.EnvString("PRIVACY_CONSOLE_GRPC_HOST", "127.0.0.1"),
		ConsoleGRPCPort:    pkgconfig.EnvInt("PRIVACY_CONSOLE_GRPC_PORT", 50055),

		// 直连 Go 微服务配置
		HubURL:           pkgconfig.EnvString("BFF_HUB_URL", "http://127.0.0.1:8082"),
		DatasourceURL:    pkgconfig.EnvString("BFF_DATASOURCE_URL", "http://127.0.0.1:8083"),
		AuditURL:         pkgconfig.EnvString("BFF_AUDIT_URL", "http://127.0.0.1:8084"),
		HubAPIKey:        pkgconfig.EnvString("BFF_HUB_API_KEY", ""),
		DatasourceAPIKey: pkgconfig.EnvString("BFF_DATASOURCE_API_KEY", ""),
		AuditAPIKey:      pkgconfig.EnvString("BFF_AUDIT_API_KEY", ""),
	}
}

// ConsoleGRPCAddress returns the formatted host:port string for the BFF's gRPC server.
func (c *Config) ConsoleGRPCAddress() string {
	return fmt.Sprintf("%s:%d", c.ConsoleGRPCHost, c.ConsoleGRPCPort)
}

// Validate checks that the configuration is consistent and all required files exist.
// Validate 校验配置一致性：当 TLS 启用时确认 CA 证书文件存在，
// 客户端证书/私钥成对提供，重试参数合理。
// 在启动早期快速失败并给出清晰错误信息，避免运行时才暴露配置问题。
func (c *Config) Validate() error {
	if c.AgentTLSEnabled {
		if c.AgentTLSCAFile == "" {
			return fmt.Errorf("TLS enabled but PRIVACY_AGENT_TLS_CA_FILE is not set")
		}
		if _, err := os.Stat(c.AgentTLSCAFile); err != nil {
			return fmt.Errorf("TLS CA file not accessible: %s: %w", c.AgentTLSCAFile, err)
		}
		// Client cert and key must be provided together (mTLS pair).
		if (c.AgentTLSCertFile != "") != (c.AgentTLSKeyFile != "") {
			return fmt.Errorf("mTLS client cert and key must be provided together: set both PRIVACY_AGENT_TLS_CERT_FILE and PRIVACY_AGENT_TLS_KEY_FILE")
		}
		if c.AgentTLSCertFile != "" {
			if _, err := os.Stat(c.AgentTLSCertFile); err != nil {
				return fmt.Errorf("TLS client cert file not accessible: %s: %w", c.AgentTLSCertFile, err)
			}
			if _, err := os.Stat(c.AgentTLSKeyFile); err != nil {
				return fmt.Errorf("TLS client key file not accessible: %s: %w", c.AgentTLSKeyFile, err)
			}
		}
	}

	// BFF Inbound TLS validation
	if c.ConsoleTLSEnabled {
		if c.ConsoleTLSCertFile == "" || c.ConsoleTLSKeyFile == "" {
			return fmt.Errorf("PRIVACY_CONSOLE_TLS_CERT_FILE and PRIVACY_CONSOLE_TLS_KEY_FILE must be set when console TLS is enabled")
		}
		if _, err := os.Stat(c.ConsoleTLSCertFile); err != nil {
			return fmt.Errorf("Console TLS cert file not accessible: %s: %w", c.ConsoleTLSCertFile, err)
		}
		if _, err := os.Stat(c.ConsoleTLSKeyFile); err != nil {
			return fmt.Errorf("Console TLS key file not accessible: %s: %w", c.ConsoleTLSKeyFile, err)
		}
		if strings.TrimSpace(c.ConsoleTLSClientAuth) != "" {
			if c.ConsoleTLSCAFile == "" {
				return fmt.Errorf("PRIVACY_CONSOLE_TLS_CA_FILE must be configured when client auth is enabled")
			}
			if _, err := os.Stat(c.ConsoleTLSCAFile); err != nil {
				return fmt.Errorf("Console TLS CA file not accessible: %s: %w", c.ConsoleTLSCAFile, err)
			}
		}
		if c.ConsoleTLSPinnedPubKeyFile != "" {
			if _, err := os.Stat(c.ConsoleTLSPinnedPubKeyFile); err != nil {
				return fmt.Errorf("Console TLS pinned pubkey file not accessible: %s: %w", c.ConsoleTLSPinnedPubKeyFile, err)
			}
		}
	}

	if c.AgentRetryMaxAttempts < 1 {
		return fmt.Errorf("PRIVACY_AGENT_RETRY_MAX_ATTEMPTS must be >= 1, got %d", c.AgentRetryMaxAttempts)
	}
	if c.AgentRetryInitialBackoff < 0 {
		return fmt.Errorf("PRIVACY_AGENT_RETRY_INITIAL_BACKOFF must be >= 0, got %d", c.AgentRetryInitialBackoff)
	}
	if c.AgentRetryMaxBackoff < 0 {
		return fmt.Errorf("PRIVACY_AGENT_RETRY_MAX_BACKOFF must be >= 0, got %d", c.AgentRetryMaxBackoff)
	}

	// P0-1 零信任默认态：鉴权中间件在 Key 为空时整体放行（pkg/middleware/auth.go），
	// 而本 BFF 代理面可达原始样本记录（P0-7），故非环回监听必须配置 CONSOLE_API_KEY。
	hosts := []string{c.ConsoleHost}
	if c.ConsoleGRPCEnabled {
		hosts = append(hosts, c.ConsoleGRPCHost)
	}
	return pkgconfig.ValidateFailClosed(pkgconfig.SecurityRequirements{
		ServiceName:       "console-bff",
		Hosts:             hosts,
		APIKey:            c.ConsoleAPIKey,
		TLSEnabled:        c.ConsoleTLSEnabled,
		RequireTLS:        c.ConsoleRequireTLS,
		GRPCEnabled:       c.ConsoleGRPCEnabled,
		MTLSWhitelistFile: c.ConsoleMTLSWhitelistFile,
	})
}

// getEnvWithFallback 尝试依次读取给定的环境变量列表，返回第一个非空值；全为空时返回空字符串。
func getEnvWithFallback(names ...string) string {
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

// getEnvOptional reads an env var, distinguishing "unset" from "explicitly set to empty".
// getEnvOptional 读取环境变量，区分"未设置"与"显式设为空字符串"。
//
// Key difference from pkgconfig.EnvString / 与 pkgconfig.EnvString 的核心区别：
//   - EnvString: empty string equals unset, falls back to default
//     EnvString：空字符串等同于未设置，回退到默认值
//   - getEnvOptional: empty string is a valid value; only uses default when completely unset
//     getEnvOptional：空字符串是合法值，仅在变量完全未设置时才使用默认值
//
// This enables "set empty to disable" semantics, e.g.:
// 这样支持"设为空即禁用"的语义，例如：
//
//	PRIVACY_CONSOLE_STATIC_DIR=  → disable static file hosting / 禁用静态文件托管
//	var not set                  → use default "../web/dist"   / 使用默认值 "../web/dist"
func getEnvOptional(name, defaultValue string) string {
	// os.LookupEnv 返回 (value, exists)，可区分"未设置"与"设为空"
	if v, ok := os.LookupEnv(name); ok {
		return v // 环境变量存在（即使是空字符串也返回）
	}
	return defaultValue // 环境变量完全未设置，使用默认值
}

// AgentAddress returns the full gRPC target address for the upstream agent.
// AgentAddress 拼接并返回上游 agent 的完整 gRPC 目标地址。
//
// Format: "host:port", e.g. "127.0.0.1:50051".
// Used as the target parameter for grpc.NewClient().
// 用于 grpc.NewClient() 的 target 参数。
func (c *Config) AgentAddress() string {
	// 将主机名与端口号通过冒号拼接，strconv.Itoa 将 int 端口转为字符串
	return c.AgentGRPCHost + ":" + strconv.Itoa(c.AgentGRPCPort)
}

// ConsoleAddress returns the full HTTP listen address for this Go proxy.
// ConsoleAddress 拼接并返回本 Go 代理的完整 HTTP 监听地址。
//
// Format: "host:port", e.g. "127.0.0.1:8081".
// Used as the http.Server.Addr parameter.
// 用于 http.Server.Addr 参数。
func (c *Config) ConsoleAddress() string {
	// 将主机名与端口号通过冒号拼接，strconv.Itoa 将 int 端口转为字符串
	return c.ConsoleHost + ":" + strconv.Itoa(c.ConsolePort)
}
