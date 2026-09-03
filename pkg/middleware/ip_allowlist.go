package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strings"

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

	networks := make([]*net.IPNet, 0, len(allowedCIDRs))
	for _, cidr := range allowedCIDRs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		// 支持单 IP（自动补 /32 或 /128）
		if !strings.Contains(cidr, "/") {
			ip := net.ParseIP(cidr)
			if ip == nil {
				slog.Warn("IPAllowlist: invalid IP/CIDR skipped", "entry", cidr)
				continue
			}
			if ip.To4() != nil {
				cidr = cidr + "/32"
			} else {
				cidr = cidr + "/128"
			}
		}
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			slog.Warn("IPAllowlist: invalid CIDR skipped", "entry", cidr, "error", err.Error())
			continue
		}
		networks = append(networks, network)
	}

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

		ip := net.ParseIP(clientIP)
		if ip == nil {
			AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "invalid client IP", nil)
			return
		}

		for _, network := range networks {
			if network.Contains(ip) {
				c.Next()
				return
			}
		}

		AbortWithError(c, http.StatusForbidden, "IP_NOT_ALLOWED", "client IP not in allowed CIDR ranges", nil)
	}
}

// AllowedCIDRsFromEnv 从 PRIVACY_ALLOWED_CIDRS 环境变量解析允许的 CIDR 列表。
func AllowedCIDRsFromEnv() []string {
	return pkgconfig.EnvStringSlice("PRIVACY_ALLOWED_CIDRS")
}
