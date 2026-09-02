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
	"strconv"
	"strings"

	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
)

// Runtime 是引擎入口的「监听面 + 安全开关」快照。
type Runtime struct {
	// ServiceName 用于门禁错误信息定位（privshield-agent / privshield-gateway）。
	ServiceName string

	// RESTHost / RESTPort：REST（或网关 HTTP 代理）监听地址。
	RESTHost string
	RESTPort int
	// GRPCHost / GRPCPort：gRPC（或网关 gRPC 代理）监听地址。
	GRPCHost    string
	GRPCPort    int
	GRPCEnabled bool // 本进程是否开启 gRPC 监听（agent / gateway 均为 true）

	// TLS 与 mTLS 服务端配置。
	TLSEnabled        bool
	TLSCertFile       string
	TLSKeyFile        string
	TLSCAFile         string
	MTLSEnabled       bool   // PRIVACY_AUTH_INTERNAL_MTLS_ENABLED
	MTLSWhitelistFile string // 客户端证书 CN 白名单文件（唯一生效的 gRPC 身份鉴别来源）

	// RequireTLS 由生产编排显式置真（PRIVACY_REQUIRE_TLS / GATEWAY_REQUIRE_TLS）：
	// TLS 未启用即拒绝启动，防止「声明已加密、实际明文直传」。
	RequireTLS bool

	// AuthEnabled 是 API Key 鉴权开关（PRIVACY_AUTH_ENABLED）。
	AuthEnabled bool
	// AuthKeyConfigured 表示至少配置了一把可校验的入站 Key。
	AuthKeyConfigured bool
	// SkipTLSForRemote 为真时跳过非环回 TLS 强制校验（网关不终止入站 TLS）。
	SkipTLSForRemote bool
}

// LoadAgent 读取 privshield-agent 的运行环境变量。
//
// 监听地址默认收敛到 127.0.0.1：容器编排（deploy/k8s、deploy/helm、docker-compose、
// scripts/prod/docker-start-agent.sh）均显式注入 0.0.0.0，因此收紧默认值只影响手工
// `go run ./engine-go/cmd/privshield-agent` 的本地开发形态（无密钥可启动，且不对外暴露）。
func LoadAgent() *Runtime {
	authEnabled := pkgconfig.EnvBool("PRIVACY_AUTH_ENABLED", false)
	return &Runtime{
		ServiceName:       "privshield-agent",
		RESTHost:          pkgconfig.EnvString("PRIVACY_REST_HOST", "127.0.0.1"),
		RESTPort:          pkgconfig.EnvInt("PRIVACY_REST_PORT", 8079),
		GRPCHost:          pkgconfig.EnvString("PRIVACY_GRPC_HOST", "127.0.0.1"),
		GRPCPort:          pkgconfig.EnvInt("PRIVACY_GRPC_PORT", 50051),
		GRPCEnabled:       true,
		TLSEnabled:        pkgconfig.EnvBool("PRIVACY_TLS_ENABLED", false),
		TLSCertFile:       pkgconfig.EnvString("PRIVACY_TLS_CERT_FILE", ""),
		TLSKeyFile:        pkgconfig.EnvString("PRIVACY_TLS_KEY_FILE", ""),
		TLSCAFile:         pkgconfig.EnvString("PRIVACY_TLS_CA_FILE", ""),
		MTLSEnabled:       pkgconfig.EnvBool("PRIVACY_AUTH_INTERNAL_MTLS_ENABLED", false),
		MTLSWhitelistFile: pkgconfig.EnvString("PRIVACY_AUTH_MTLS_WHITELIST_FILE", ""),
		RequireTLS:        pkgconfig.EnvBool("PRIVACY_REQUIRE_TLS", false),
		AuthEnabled:       authEnabled,
		AuthKeyConfigured: inboundKeyConfigured(),
	}
}

// LoadGateway 读取 privshield-gateway 的运行环境变量。
//
// 网关是 L7 透明代理：自身**不终止 TLS、也不校验入站凭据**（鉴权由被代理的 Agent 端
// `PRIVACY_AUTH_*` 强制），因此非环回监听同样受 fail-closed 门禁约束；若声明
// GATEWAY_REQUIRE_TLS，门禁会直接拒绝启动并要求把 TLS 交由 mTLS 回源 / 入口网关实现。
func LoadGateway() *Runtime {
	return &Runtime{
		ServiceName:       "privshield-gateway",
		RESTHost:          pkgconfig.EnvString("GATEWAY_HOST", "127.0.0.1"),
		RESTPort:          pkgconfig.EnvInt("GATEWAY_PORT", 8000),
		GRPCHost:          pkgconfig.EnvString("GATEWAY_GRPC_HOST", "127.0.0.1"),
		GRPCPort:          pkgconfig.EnvInt("GATEWAY_GRPC_PORT", 50000),
		GRPCEnabled:       true,
		TLSEnabled:        false, // 网关不终止入站 TLS
		MTLSEnabled:       false,
		RequireTLS:        pkgconfig.EnvBool("GATEWAY_REQUIRE_TLS", false),
		AuthEnabled:       pkgconfig.EnvBool("PRIVACY_AUTH_ENABLED", false),
		AuthKeyConfigured: inboundKeyConfigured(),
		SkipTLSForRemote:  true, // 网关不终止入站 TLS，由上游/后端处理
	}
}

// Hosts 返回本进程将要打开的全部监听地址，用于判断是否存在非环回暴露面。
func (r *Runtime) Hosts() []string {
	hosts := []string{r.RESTHost}
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
//  1. TLS 开关已开但证书/私钥缺失或不可读 → 拒绝启动（消除「TLS=true 却回退明文」的静音降级）；
//  2. 复用 pkgconfig.ValidateFailClosed：非环回监听且入站凭据缺失 → ErrAPIKeyRequired；
//     RequireTLS=true 而 TLS 未启用 → ErrTLSRequired；gRPC 启用 TLS 但无 CN 白名单文件 → ErrMTLSWhitelistRequired；
//  3. internal mTLS 开关已开但白名单文件为空或不可读 → ErrMTLSWhitelistRequired
//     （白名单拦截器只在文件存在时才注册，缺失等同未做身份鉴别）。
func (r *Runtime) Validate() error {
	if r.TLSEnabled {
		if r.TLSCertFile == "" {
			return fmt.Errorf("%s: TLS enabled but PRIVACY_TLS_CERT_FILE is not set", r.ServiceName)
		}
		if r.TLSKeyFile == "" {
			return fmt.Errorf("%s: TLS enabled but PRIVACY_TLS_KEY_FILE is not set", r.ServiceName)
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
		AllowedCIDRs:      pkgconfig.EnvStringSlice("PRIVACY_ALLOWED_CIDRS"),
	}); err != nil {
		return err
	}

	// internal mTLS 已声明启用：白名单文件必须真实存在且可读，否则 gRPC 侧身份鉴别完全不生效。
	if r.MTLSEnabled {
		path := strings.TrimSpace(r.MTLSWhitelistFile)
		if path == "" {
			return fmt.Errorf("%s: PRIVACY_AUTH_INTERNAL_MTLS_ENABLED=true but PRIVACY_AUTH_MTLS_WHITELIST_FILE is empty: %w",
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
		"PRIVACY_AUTH_INTERNAL_API_KEYS",
		"PRIVACY_AUTH_EXTERNAL_API_KEYS",
		"PRIVACY_AUTH_STATIC_API_KEYS",
		"PRIVACY_AUTH_API_KEY",
		"PRIVACY_API_KEY",
	} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}
