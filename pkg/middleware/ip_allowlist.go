package middleware

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
)

// IPAllowlist 返回基于 CIDR 白名单的 IP 访问控制中间件。
// 未配置（空列表）时透传所有请求（向后兼容）。
// 命中白名单的请求放行，未命中的返回 403 Forbidden。
func IPAllowlist(allowedCIDRs []string) gin.HandlerFunc {
	if len(allowedCIDRs) == 0 {
		return func(c *gin.Context) { c.Next() }
	}

	// 网段编译与 gRPC 侧共用 ParseAllowedNetworks，保证两个端口的宽松度与日志口径完全一致
	networks := ParseAllowedNetworks(allowedCIDRs)

	if len(networks) == 0 {
		slog.Warn("IPAllowlist: no valid CIDRs configured, all requests will pass through")
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		clientIP := RealClientIP(c)
		if clientIP == "" {
			AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "unable to determine client IP", nil)
			return
		}

		if IPAllowed(networks, clientIP) {
			c.Next()
			return
		}

		AbortWithError(c, http.StatusForbidden, "IP_NOT_ALLOWED", "client IP not in allowed CIDR ranges", nil)
	}
}

// AllowedCIDRsFromEnv 从调用方指定的环境变量名中解析允许的 CIDR 列表。
//
// 机制与策略完全分离：pkg 基础包不硬编码任何具体业务环境变量，不维护次级兼容兜底。
// 调用方必须显式传入专属环境变量名（例如 "ENGINE_GATEWAY_ALLOWED_CIDRS"、"AGENT_ALLOWED_CIDRS"）。
// 若 envKey 为空或未配置，则返回 nil（表示不启用白名单拦截，全量放行）。
func AllowedCIDRsFromEnv(envKey string) []string {
	if envKey == "" {
		return nil
	}
	return pkgconfig.EnvStringSlice(envKey)
}
