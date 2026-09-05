package security

import (
	"github.com/gin-gonic/gin"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	pkgmiddleware "github.com/fengzhizi319/PrivShield-go/pkg/middleware"
)

// AuthMiddleware 返回 Gin 中间件，执行 API Key 认证 + 按路径 scope 鉴权。
// 认证未启用时透传并注入匿名身份。
//
// 热轮转（AGENT_AUTH_KEYS_FILE）不再走单独副本：活密钥由 Settings.LiveInternalKeys 携带，
// 统一在 pkg/auth.AuthenticateAPIKey 内活读。历史上此处有一份“热重载中间件”副本，只做了
// 认证而**遗漏了 PermissionForRESTPath 的 scope 校验**，导致只要配了密钥文件，任何一把合法
// Key（哪怕只有 health:read）都能调用 budget/reset、dynclassification 写接口、/debug/pprof 等
// 全部端点；同时它把 InternalKeys 整体替换为文件密钥，使环境变量密钥在 REST 面静默失效。
// 现收敛为单一实现，REST 与 gRPC 的认证、吊销、scope 语义从此不会再分叉。
func AuthMiddleware() gin.HandlerFunc {
	return pkgauth.AuthMiddleware(&GetSettings().Settings)
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

	return pkgmiddleware.RateLimitWithEndpoints(rps, burst, settings.RateLimitPerEndpoint, func(c *gin.Context) string {
		path := c.Request.URL.Path
		if pkgauth.IsHealthPathOrMethod(path) && settings.HealthNoRateLimit {
			// 返回空 key，让 pkg/middleware 透传健康端点
			return ""
		}

		identity := GetIdentity(c)
		if identity == nil {
			identity = &Identity{ServiceType: "external", Name: "anonymous"}
		}

		// 匿名与公开身份（public-caller，即未携 Token 访问登录/注册等公开端点）调用者
		// 追加客户端 IP 作为分片因子：既防止单 IP 洪泛，也避免所有公开调用者共用
		// 同一令牌桶造成跨用户拒绝服务（等保三级 G-02 源地址维度限流）。
		// 对 path 做前缀归一化，去除动态 ID 段，防止高基数路径导致桶爆炸
		normalizedPath := pkgmiddleware.NormalizeRateLimitPath(path)
		key := identity.ServiceType + ":" + identity.Name + ":" + normalizedPath
		if identity.Name == "anonymous" || identity.ServiceType == "public" {
			clientIP := pkgmiddleware.RealClientIP(c)
			if clientIP != "" {
				key += ":" + clientIP
			}
		}
		return key
	})
}
