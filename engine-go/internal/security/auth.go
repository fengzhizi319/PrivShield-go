package security

import (
	"github.com/gin-gonic/gin"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	pkgmiddleware "github.com/fengzhizi319/PrivShield-go/pkg/middleware"
)

// AuthMiddleware 返回 Gin 中间件，执行 API Key 认证。
// 认证未启用时透传并注入匿名身份。
// 若配置了 KeyStore（PRIVACY_AUTH_KEYS_FILE），每请求读取最新 keys 实现热轮转。
func AuthMiddleware() gin.HandlerFunc {
	settings := GetSettings()
	if keyStore != nil {
		return hotReloadAuthMiddleware(settings)
	}
	return pkgauth.AuthMiddleware(&settings.Settings)
}

// hotReloadAuthMiddleware 每请求从 KeyStore 读取最新 keys，实现 API Key 热轮转。
func hotReloadAuthMiddleware(settings *Settings) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		if pkgauth.IsHealthPathOrMethod(path) && settings.HealthNoAuth {
			c.Set(pkgauth.IdentityContextKey, &Identity{ServiceType: "internal", Name: "health-probe", Scopes: []string{"*"}})
			c.Next()
			return
		}

		if !settings.AuthEnabled {
			c.Set(pkgauth.IdentityContextKey, pkgauth.AnonymousIdentity)
			c.Next()
			return
		}

		token := pkgauth.ExtractBearerToken(c.GetHeader("Authorization"))
		if token == "" {
			abortWithAuthError(c, "UNAUTHENTICATED", "Unauthorized: missing credentials")
			return
		}

		ks := GetKeyStore()
		if ks == nil {
			abortWithAuthError(c, "UNAUTHENTICATED", "Unauthorized: key store unavailable")
			return
		}

		currentKeys := ks.Keys()
		lookupSettings := &pkgauth.Settings{
			AuthEnabled:  true,
			InternalKeys: currentKeys,
			ExternalKeys: settings.ExternalKeys,
		}
		identity := pkgauth.AuthenticateAPIKey(lookupSettings, token)
		if identity == nil {
			abortWithAuthError(c, "UNAUTHENTICATED", "Unauthorized: invalid credentials")
			return
		}

		c.Set(pkgauth.IdentityContextKey, identity)
		c.Next()
	}
}

func abortWithAuthError(c *gin.Context, code, msg string) {
	c.AbortWithStatusJSON(401, map[string]any{
		"code":    code,
		"message": msg,
	})
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
			clientIP := pkgmiddleware.RealClientIP(c)
			if clientIP != "" {
				key += ":" + clientIP
			}
		}
		return key
	})
}
