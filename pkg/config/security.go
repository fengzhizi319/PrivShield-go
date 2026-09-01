// Package config provides shared environment helpers and the fail-closed security
// invariants enforced by every PrivShield service at startup.
// Package config 提供共享的环境变量读取助手，以及各服务启动时必须通过的 fail-closed 安全不变式校验。
package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// Fail-closed 启动期安全不变式错误。任一命中即由服务的 Validate() 上抛、main() 终止进程，
// 避免「配置缺失 → 静默降级为无鉴权/明文传输」的可观测性盲区。
var (
	// ErrAPIKeyRequired 表示监听地址可被远端访问但未配置入站 API Key（原实现为空即放行）。
	ErrAPIKeyRequired = errors.New("inbound API key must not be empty when listening on a non-loopback address")

	// ErrTLSRequired 表示部署方声明必须启用 TLS，但 TLS 开关处于关闭状态。
	ErrTLSRequired = errors.New("TLS is required by configuration but not enabled")

	// ErrMTLSWhitelistRequired 表示已启用 gRPC TLS 但未注入客户端证书 CN 白名单文件；
	// 此时白名单拦截器不会被注册，任何通过 CA 校验的客户端都可调用全部方法。
	ErrMTLSWhitelistRequired = errors.New("mTLS CN whitelist file is required when TLS is enabled on the gRPC server")

	// ErrEncryptionKeyRequired 表示存证快照加密密钥缺失，样本将以明文落盘。
	ErrEncryptionKeyRequired = errors.New("snapshot encryption key must not be empty when listening on a non-loopback address")

	// ErrChainKeyRequired 表示存证哈希链密钥缺失：无密钥 SM3 前映像口径是公开的，
	// 任何知悉者都能重算并伪造「合法」记录，因此对外暴露的存证服务必须注入 HMAC 密钥。
	ErrChainKeyRequired = errors.New("evidence hash chain key must not be empty when listening on a non-loopback address")
)

// SecurityRequirements 描述一次 fail-closed 校验所需的监听与开关状态。
type SecurityRequirements struct {
	// ServiceName 用于错误信息定位，例如 "audit-log"。
	ServiceName string
	// Hosts 是该进程将要监听的全部地址（HTTP 与 gRPC），用于判断是否存在非环回暴露面。
	Hosts []string
	// APIKey 是入站鉴权密钥（各服务前缀变量）。
	APIKey string
	// TLSEnabled 表示服务端 TLS 是否已启用。
	TLSEnabled bool
	// RequireTLS 由部署方显式置真（生产编排），此时 TLSEnabled 必须为真。
	RequireTLS bool
	// GRPCEnabled 表示本进程是否监听 gRPC 端口。
	GRPCEnabled bool
	// MTLSWhitelistFile 是客户端证书 CN 白名单文件路径（空即拦截器不注册）。
	MTLSWhitelistFile string
	// EncryptionKey 是信封加密主密钥（仅 RequireEncryptionKey 为真时校验）。
	EncryptionKey string
	// RequireEncryptionKey 用于 audit-log 等需要密文落盘的服务。
	RequireEncryptionKey bool
	// HashKey 是存证哈希链的 HMAC-SM3 密钥（局方托管）。
	HashKey string
	// RequireHashKey 用于写入链式存证的服务（audit-log）。
	RequireHashKey bool
}

// ValidateFailClosed 强制「安全开关缺失即启动失败」，取代原先的空值静默放行。
//
// 判定规则：
//  1. 任一监听地址非环回（0.0.0.0 / 具体网卡 IP / 通配）→ 必须配置入站 API Key；
//     纯 127.0.0.1 本地开发形态允许无密钥启动；
//  2. RequireTLS 为真而 TLSEnabled 为假 → 拒绝启动（防止生产编排漏配证书却照常服务）；
//  3. 启用 gRPC TLS 但未提供 CN 白名单文件 → 拒绝启动（防止「以为已做双向认证、实则未注册拦截器」）；
//  4. RequireEncryptionKey 为真且密钥为空、且存在非环回监听 → 拒绝启动（防止快照明文落盘）；
//  5. RequireHashKey 为真且链密钥为空、且存在非环回监听 → 拒绝启动（防止可伪造的无密钥存证哈希）。
func ValidateFailClosed(req SecurityRequirements) error {
	name := req.ServiceName
	if name == "" {
		name = "service"
	}
	remoteExposed := false
	for _, h := range req.Hosts {
		if !IsLoopbackHost(h) {
			remoteExposed = true
			break
		}
	}

	if req.APIKey == "" {
		if remoteExposed {
			return fmt.Errorf("%s: %w (set the service *_API_KEY variable; bind to 127.0.0.1 for local development)", name, ErrAPIKeyRequired)
		}
	}

	if req.RequireTLS && !req.TLSEnabled {
		return fmt.Errorf("%s: %w", name, ErrTLSRequired)
	}

	if req.TLSEnabled && req.GRPCEnabled && strings.TrimSpace(req.MTLSWhitelistFile) == "" {
		return fmt.Errorf("%s: %w", name, ErrMTLSWhitelistRequired)
	}

	if req.RequireEncryptionKey && req.EncryptionKey == "" && remoteExposed {
		return fmt.Errorf("%s: %w (set AUDIT_LOG_ENCRYPTION_KEY; unencrypted samples invalidate snapshot evidence)", name, ErrEncryptionKeyRequired)
	}

	if req.RequireHashKey && strings.TrimSpace(req.HashKey) == "" && remoteExposed {
		return fmt.Errorf("%s: %w (set AUDIT_LOG_HASH_KEY to the 局方托管 secret; un-keyed SM3 hashes can be re-computed and forged)", name, ErrChainKeyRequired)
	}

	return nil
}

// IsLoopbackHost reports whether a listen host only accepts local connections.
//
// IsLoopbackHost 判断监听地址是否仅接受本机连接：空串、"localhost"、以及 127.0.0.0/8
// 与 ::1 环回地址视为本地；"0.0.0.0"、"::" 与具体网卡地址视为对外暴露。
func IsLoopbackHost(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		return true
	}
	if h == "localhost" {
		return true
	}
	// 允许 "host:port" 形式的入参，取主机部分判断。
	if splitHost, _, err := net.SplitHostPort(h); err == nil {
		h = splitHost
	}
	if h == "" || h == "*" {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	// 无法解析为主机名时按对外暴露处理（fail-closed）。
	return false
}
