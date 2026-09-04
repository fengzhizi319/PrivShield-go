// Package config 提供 privshield-agent / privshield-gateway 两个引擎入口的启动配置快照，
// 以及 P0-1「零信任默认态」要求的 fail-closed 启动门禁。
//
// 设计要点：
//  1. 监听地址与安全开关集中在此处解析，避免 agent / gateway 各自维护一套环境变量语义；
//  2. Validate() 复用共享库 pkg/config 的 ValidateFailClosed 不变式，不重复实现校验逻辑；
//  3. 任何一条红线命中都由 cmd 入口 log.Fatalf 终止进程，取代原先的「空值即放行 / 静默明文回退」。
//
// Package config supplies the startup configuration snapshot shared by the two engine
// entrypoints (privshield-agent and privshield-gateway) together with the P0-1 zero-trust
// fail-closed gate that must pass before any listener is opened.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
)

// Runtime 是引擎入口的「监听面 + 安全开关」快照。
type Runtime struct {
	// ServiceName 用于门禁错误信息定位（privshield-agent / privshield-gateway）。
	ServiceName string

	// RESTHost / RESTPort：REST（或网关 HTTP 代理）监听地址。
	RESTHost    string
	RESTPort    int
	RESTEnabled bool // 本进程是否开启 REST 监听（agent 默认为 true，可通过 AGENT_REST_ENABLED 调整）

	// GRPCHost / GRPCPort：gRPC（或网关 gRPC 代理）监听地址。
	GRPCHost    string
	GRPCPort    int
	GRPCEnabled bool // 本进程是否开启 gRPC 监听（agent 默认为 true，可通过 AGENT_GRPC_ENABLED 调整）

	// TLS 与 mTLS 服务端配置。
	TLSEnabled        bool
	TLSCertFile       string
	TLSKeyFile        string
	TLSCAFile         string
	MTLSEnabled       bool   // AGENT_AUTH_INTERNAL_MTLS_ENABLED
	MTLSWhitelistFile string // 客户端证书 CN 白名单文件（唯一生效的 gRPC 身份鉴别来源）

	// RequireTLS 由生产编排显式置真（AGENT_REQUIRE_TLS / ENGINE_GATEWAY_REQUIRE_TLS）：
	// TLS 未启用即拒绝启动，防止「声明已加密、实际明文直传」。
	RequireTLS bool

	// AuthEnabled 是 API Key 鉴权开关（AGENT_AUTH_ENABLED）。
	AuthEnabled bool
	// AuthKeyConfigured 表示至少配置了一把可校验的入站 Key。
	AuthKeyConfigured bool
	// SkipTLSForRemote 为真时跳过非环回 TLS 强制校验（网关不终止入站 TLS）。
	SkipTLSForRemote bool
}

// agentYAMLConfig 映射 config/privacy.yaml 中的 agent 基础配置段。
type agentYAMLConfig struct {
	Agent struct {
		RESTEnabled *bool  `yaml:"rest_enabled"`
		RESTHost    string `yaml:"rest_host"`
		RESTPort    int    `yaml:"rest_port"`
		GRPCEnabled *bool  `yaml:"grpc_enabled"`
		GRPCHost    string `yaml:"grpc_host"`
		GRPCPort    int    `yaml:"grpc_port"`
		LogLevel    string `yaml:"log_level"`
		TLSEnabled  *bool  `yaml:"tls_enabled"`
		RequireTLS  *bool  `yaml:"require_tls"`
		AuthEnabled *bool  `yaml:"auth_enabled"`
	} `yaml:"agent"`
}

// loadAgentYAML 从指定路径尝试读取 agent 基础配置段；若文件不存在则返回零值。
func loadAgentYAML(path string) agentYAMLConfig {
	var cfg agentYAMLConfig
	if path == "" {
		return cfg
	}
	// 尝试候选路径，适配从不同工作目录执行 go run
	candidates := []string{path, filepath.Join("..", path), filepath.Join("../..", path)}
	var data []byte
	var err error
	for _, c := range candidates {
		data, err = os.ReadFile(c)
		if err == nil {
			break
		}
	}
	if err != nil {
		return cfg
	}
	_ = yaml.Unmarshal(data, &cfg)
	return cfg
}

// LoadAgent 读取 privshield-agent 的运行时配置。
//
// 配置优先级顺序（三级驱动模型）：
//  1. 系统环境变量（最高，支持命令行 export 与容器/K8s 平台注入，AGENT_*）；
//  2. 本地 .env 配置文件（若未通过 DOTENV_DISABLED 禁用，启动时自动从当前或上级目录发现并加载）；
//  3. config/privacy.yaml 声明式配置文件中的 agent 配置段；
//  4. 安全代码兜底默认值（最低，默认绑定 127.0.0.1 环回地址，无密钥不可被外部访问）。
func LoadAgent() *Runtime {
	// 1. 自动探测并加载 .env 文件（开发体验无痛开箱即用，测试中可被 DOTENV_DISABLED 禁用）
	if os.Getenv("DOTENV_DISABLED") != "true" {
		pkgconfig.LoadDotEnvAuto()
	}

	// 2. 尝试从 YAML 文件（默认 AGENT_CONFIG_FILE 或 config/privacy.yaml）读取基础服务配置
	configFile := pkgconfig.EnvString("AGENT_CONFIG_FILE", "config/privacy.yaml")
	yamlCfg := loadAgentYAML(configFile)

	// 3. 确定各配置项的基准值（YAML 优于硬编码默认值）
	defRESTEnabled := true
	if yamlCfg.Agent.RESTEnabled != nil {
		defRESTEnabled = *yamlCfg.Agent.RESTEnabled
	}
	defRESTHost := "127.0.0.1"
	if yamlCfg.Agent.RESTHost != "" {
		defRESTHost = yamlCfg.Agent.RESTHost
	}
	defRESTPort := 8079
	if yamlCfg.Agent.RESTPort > 0 {
		defRESTPort = yamlCfg.Agent.RESTPort
	}

	defGRPCEnabled := true
	if yamlCfg.Agent.GRPCEnabled != nil {
		defGRPCEnabled = *yamlCfg.Agent.GRPCEnabled
	}
	defGRPCHost := "127.0.0.1"
	if yamlCfg.Agent.GRPCHost != "" {
		defGRPCHost = yamlCfg.Agent.GRPCHost
	}
	defGRPCPort := 50051
	if yamlCfg.Agent.GRPCPort > 0 {
		defGRPCPort = yamlCfg.Agent.GRPCPort
	}

	defTLSEnabled := false
	if yamlCfg.Agent.TLSEnabled != nil {
		defTLSEnabled = *yamlCfg.Agent.TLSEnabled
	}
	defRequireTLS := false
	if yamlCfg.Agent.RequireTLS != nil {
		defRequireTLS = *yamlCfg.Agent.RequireTLS
	}
	defAuthEnabled := false
	if yamlCfg.Agent.AuthEnabled != nil {
		defAuthEnabled = *yamlCfg.Agent.AuthEnabled
	}

	// 4. 环境变量覆盖 YAML 与代码默认值
	authEnabled := pkgconfig.EnvBool("AGENT_AUTH_ENABLED", defAuthEnabled)
	return &Runtime{
		ServiceName:       "privshield-agent",
		RESTHost:          pkgconfig.EnvString("AGENT_REST_HOST", defRESTHost),
		RESTPort:          pkgconfig.EnvInt("AGENT_REST_PORT", defRESTPort),
		RESTEnabled:       pkgconfig.EnvBool("AGENT_REST_ENABLED", defRESTEnabled),
		GRPCHost:          pkgconfig.EnvString("AGENT_GRPC_HOST", defGRPCHost),
		GRPCPort:          pkgconfig.EnvInt("AGENT_GRPC_PORT", defGRPCPort),
		GRPCEnabled:       pkgconfig.EnvBool("AGENT_GRPC_ENABLED", defGRPCEnabled),
		TLSEnabled:        pkgconfig.EnvBool("AGENT_TLS_ENABLED", defTLSEnabled),
		TLSCertFile:       pkgconfig.EnvString("AGENT_TLS_CERT_FILE", ""),
		TLSKeyFile:        pkgconfig.EnvString("AGENT_TLS_KEY_FILE", ""),
		TLSCAFile:         pkgconfig.EnvString("AGENT_TLS_CA_FILE", ""),
		MTLSEnabled:       pkgconfig.EnvBool("AGENT_AUTH_INTERNAL_MTLS_ENABLED", false),
		MTLSWhitelistFile: pkgconfig.EnvString("AGENT_AUTH_MTLS_WHITELIST_FILE", ""),
		RequireTLS:        pkgconfig.EnvBool("AGENT_REQUIRE_TLS", defRequireTLS),
		AuthEnabled:       authEnabled,
		AuthKeyConfigured: inboundKeyConfigured(),
	}
}

// LoadGateway 读取 privshield-gateway 的运行环境变量。
//
// 网关是 L7 透明代理：自身**不终止 TLS、也不校验入站凭据**（鉴权由被代理的 Agent 端
// `AGENT_AUTH_*` 强制），因此非环回监听同样受 fail-closed 门禁约束；若声明
// ENGINE_GATEWAY_REQUIRE_TLS，门禁会直接拒绝启动并要求把 TLS 交由 mTLS 回源 / 入口网关实现。
func LoadGateway() *Runtime {
	return &Runtime{
		ServiceName:       "privshield-gateway",
		RESTHost:          pkgconfig.EnvString("ENGINE_GATEWAY_HOST", "127.0.0.1"),
		RESTPort:          pkgconfig.EnvInt("ENGINE_GATEWAY_PORT", 8000),
		RESTEnabled:       true,
		GRPCHost:          pkgconfig.EnvString("ENGINE_GATEWAY_GRPC_HOST", "127.0.0.1"),
		GRPCPort:          pkgconfig.EnvInt("ENGINE_GATEWAY_GRPC_PORT", 50000),
		GRPCEnabled:       true,
		TLSEnabled:        false, // 网关不终止入站 TLS
		MTLSEnabled:       false,
		RequireTLS:        pkgconfig.EnvBool("ENGINE_GATEWAY_REQUIRE_TLS", false),
		AuthEnabled:       pkgconfig.EnvBool("ENGINE_GATEWAY_AUTH_ENABLED", false),
		AuthKeyConfigured: inboundKeyConfigured(),
		SkipTLSForRemote:  true, // 网关不终止入站 TLS，由上游/后端处理
	}
}

// Hosts 返回本进程将要打开的全部监听地址，用于判断是否存在非环回暴露面。
func (r *Runtime) Hosts() []string {
	var hosts []string
	if r.RESTEnabled {
		hosts = append(hosts, r.RESTHost)
	}
	if r.GRPCEnabled {
		hosts = append(hosts, r.GRPCHost)
	}
	return hosts
}

// RESTAddress 返回完整的 REST/HTTP 监听地址（host:port）。
func (r *Runtime) RESTAddress() string {
	return net.JoinHostPort(r.RESTHost, strconv.Itoa(r.RESTPort))
}

// GRPCAddress 返回完整的 gRPC 监听地址（host:port）。
func (r *Runtime) GRPCAddress() string {
	return net.JoinHostPort(r.GRPCHost, strconv.Itoa(r.GRPCPort))
}

// Validate 执行启动期 fail-closed 安全不变式校验，任一红线命中即返回错误（入口 log.Fatalf）。
//
// 校验顺序与红线：
//  0. 至少启用 REST 或 gRPC 之一，禁止两协议全部关闭导致进程空转；
//  1. TLS 开关已开但证书/私钥缺失或不可读 → 拒绝启动（消除「TLS=true 却回退明文」的静音降级）；
//  2. 复用 pkgconfig.ValidateFailClosed：非环回监听且入站凭据缺失 → ErrAPIKeyRequired；
//     RequireTLS=true 而 TLS 未启用 → ErrTLSRequired；gRPC 启用 TLS 但无 CN 白名单文件 → ErrMTLSWhitelistRequired；
//  3. internal mTLS 开关已开但白名单文件为空或不可读 → ErrMTLSWhitelistRequired
//     （白名单拦截器只在文件存在时才注册，缺失等同未做身份鉴别）。
func (r *Runtime) Validate() error {
	// 启动门禁不变式 0：必须至少启用 REST 或 gRPC 之一，禁止两协议全部关闭导致空转僵死。
	if !r.RESTEnabled && !r.GRPCEnabled {
		return fmt.Errorf("%s: at least one of REST or gRPC must be enabled (both AGENT_REST_ENABLED and AGENT_GRPC_ENABLED are false)", r.ServiceName)
	}
	if r.TLSEnabled {
		if r.TLSCertFile == "" {
			return fmt.Errorf("%s: TLS enabled but AGENT_TLS_CERT_FILE is not set", r.ServiceName)
		}
		if r.TLSKeyFile == "" {
			return fmt.Errorf("%s: TLS enabled but AGENT_TLS_KEY_FILE is not set", r.ServiceName)
		}
		if _, err := os.Stat(r.TLSCertFile); err != nil {
			return fmt.Errorf("%s: TLS cert file not accessible: %s: %w", r.ServiceName, r.TLSCertFile, err)
		}
		if _, err := os.Stat(r.TLSKeyFile); err != nil {
			return fmt.Errorf("%s: TLS key file not accessible: %s: %w", r.ServiceName, r.TLSKeyFile, err)
		}
	}

	if err := pkgconfig.ValidateFailClosed(pkgconfig.SecurityRequirements{
		ServiceName:      r.ServiceName,
		Hosts:            r.Hosts(),
		APIKey:           r.inboundCredential(),
		AuthEnabled:      r.AuthEnabled,
		TLSEnabled:       r.TLSEnabled,
		RequireTLS:       r.RequireTLS,
		SkipTLSForRemote: r.SkipTLSForRemote,
		GRPCEnabled:      r.GRPCEnabled,
		// 引擎始终监听 gRPC，故 TLS 开启时白名单文件为必填项。
		MTLSWhitelistFile: r.MTLSWhitelistFile,
		AllowedCIDRs: func() []string {
			if r.ServiceName == "privshield-gateway" {
				return pkgconfig.EnvStringSlice("ENGINE_GATEWAY_ALLOWED_CIDRS")
			}
			return pkgconfig.EnvStringSlice("AGENT_ALLOWED_CIDRS")
		}(),
	}); err != nil {
		return err
	}

	// internal mTLS 已声明启用：白名单文件必须真实存在且可读，否则 gRPC 侧身份鉴别完全不生效。
	if r.MTLSEnabled {
		path := strings.TrimSpace(r.MTLSWhitelistFile)
		if path == "" {
			return fmt.Errorf("%s: AGENT_AUTH_INTERNAL_MTLS_ENABLED=true but AGENT_AUTH_MTLS_WHITELIST_FILE is empty: %w",
				r.ServiceName, pkgconfig.ErrMTLSWhitelistRequired)
		}
		if info, err := os.Stat(path); err != nil {
			return fmt.Errorf("%s: mTLS CN whitelist file not accessible: %s: %w", r.ServiceName, path, err)
		} else if info.IsDir() {
			return fmt.Errorf("%s: mTLS CN whitelist file is a directory: %s", r.ServiceName, path)
		}
	}

	return nil
}

// inboundCredential 返回交给共享门禁的入站凭据标记（非密钥明文，避免密钥进入错误日志）。
// 仅当鉴权开关打开且至少配置了一把 Key 时视为「鉴权实际生效」，否则返回空串触发远端无密钥红线。
func (r *Runtime) inboundCredential() string {
	if r.AuthEnabled && r.AuthKeyConfigured {
		return "configured"
	}
	return ""
}

// AuthEffectivelyEnabled 报告 API Key 鉴权是否真正生效（开关为真且至少存在一把 Key）。
func (r *Runtime) AuthEffectivelyEnabled() bool {
	return r.AuthEnabled && r.AuthKeyConfigured
}

// inboundKeyConfigured 检查与 internal/security.loadSettings 同源的 Key 环境变量是否至少配置了一项。
func inboundKeyConfigured() bool {
	for _, key := range []string{
		"AGENT_AUTH_INTERNAL_API_KEYS",
		"AGENT_AUTH_API_KEY",
		"AGENT_AUTH_EXTERNAL_API_KEYS",
		"AGENT_AUTH_STATIC_API_KEYS",
	} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}
