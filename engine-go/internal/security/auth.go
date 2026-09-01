package security

import (
	"strings"

	"github.com/gin-gonic/gin"

	pkgauth "github.com/fengzhizi319/PrivShield/pkg/auth"
	pkgmiddleware "github.com/fengzhizi319/PrivShield/pkg/middleware"
)

// extractBearerToken 从 Authorization header 提取 Bearer token。
func extractBearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		return parts[1]
	}
	return ""
}

// AuthMiddleware 返回 Gin 中间件，执行 API Key 认证。
// 认证未启用时透传并注入匿名身份。
func AuthMiddleware() gin.HandlerFunc {
	settings := GetSettings()
	return pkgauth.AuthMiddleware(&settings.Settings)
}

// RequirePermission 返回需要指定权限的 Gin 中间件。
func RequirePermission(permission string) gin.HandlerFunc {
	return pkgauth.RequirePermission(permission)
}

// RequireAnyPermission 返回需要任一指定权限的 Gin 中间件。
func RequireAnyPermission(permissions ...string) gin.HandlerFunc {
	return pkgauth.RequireAnyPermission(permissions...)
}

// GetIdentity 从 gin.Context 提取认证身份。
func GetIdentity(c *gin.Context) *Identity {
	return pkgauth.GetIdentity(c)
}

// SecurityHeadersMiddleware 注入安全响应头。
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		pkgmiddleware.SecurityHeadersTo(c.Writer)
		c.Writer.Header().Set("X-Frame-Options", "DENY")
		c.Next()
	}
}

// RateLimitMiddleware 返回分片并发滑动窗口限流中间件（带 TTL 自动淘汰与内存安全）。
func RateLimitMiddleware() gin.HandlerFunc {
	settings := GetSettings()
	if !settings.RateLimitEnabled {
		return func(c *gin.Context) { c.Next() }
	}

	rps := int(settings.RateLimitDefaultRPS)
	burst := settings.RateLimitDefaultBurst

	return pkgmiddleware.RateLimitWithKeyFunc(rps, burst, func(c *gin.Context) string {
		path := c.Request.URL.Path
		if pkgauth.IsHealthPathOrMethod(path) && settings.HealthNoRateLimit {
			// 返回空 key，让 pkg/middleware 透传健康端点
			return ""
		}

		identity := GetIdentity(c)
		if identity == nil {
			identity = &Identity{ServiceType: "external", Name: "anonymous"}
		}

		// 匿名调用者追加客户端 IP 作为分片因子，防止单 IP 洪泛攻击
		// 对 path 做前缀归一化，去除动态 ID 段，防止高基数路径导致桶爆炸
		normalizedPath := pkgmiddleware.NormalizeRateLimitPath(path)
		key := identity.ServiceType + ":" + identity.Name + ":" + normalizedPath
		if identity.Name == "anonymous" {
			clientIP := c.ClientIP()
			if clientIP != "" {
				key += ":" + clientIP
			}
		}
		return key
	})
}

// StopRateLimiter 停止限流器后台清理 goroutine。
// 在新的 pkg/middleware 实现中，每个限流器拥有独立的清理 goroutine；
// 调用 RateLimitMiddleware 不会启动全局 goroutine，因此本函数保留为空操作以兼容旧调用方。
func StopRateLimiter() {}
