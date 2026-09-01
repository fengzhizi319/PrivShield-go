package auth

import (
	"crypto/subtle"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield/pkg/middleware"
)

// IdentityContextKey 用于在 gin.Context 中存储认证身份。
const IdentityContextKey = "security_identity"

// extractBearerToken 从 Authorization header 提取 Bearer token。
func extractBearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		return parts[1]
	}
	return ""
}

// ConstantTimeLookup 常量时间查找 token，防止计时攻击。
// 对 key 进行排序以确保确定性迭代顺序（Go map 迭代顺序随机），
// 遍历全部 key 且始终比较所有 key，避免时序侧信道泄漏。
func ConstantTimeLookup(keys map[string]*KeyConfig, token string) *KeyConfig {
	if len(keys) == 0 {
		return nil
	}
	// 排序 key 确保确定性迭代顺序
	sortedKeys := make([]string, 0, len(keys))
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	tokenBytes := []byte(token)
	var matched *KeyConfig
	for _, key := range sortedKeys {
		// subtle.ConstantTimeCompare 确保每次比较耗时恒定
		if subtle.ConstantTimeCompare([]byte(key), tokenBytes) == 1 {
			matched = keys[key]
		}
	}
	return matched
}

// authenticateAPIKey 在内部和外部 key 存储中查找 token。
func authenticateAPIKey(settings *Settings, token string) *Identity {
	if internal := ConstantTimeLookup(settings.InternalKeys, token); internal != nil {
		return &Identity{ServiceType: "internal", Name: internal.Name, Scopes: internal.Scopes}
	}
	if external := ConstantTimeLookup(settings.ExternalKeys, token); external != nil {
		return &Identity{ServiceType: "external", Name: external.Name, Scopes: external.Scopes}
	}
	return nil
}

// AuthMiddleware 返回 Gin 中间件，执行 API Key 认证。
// 认证未启用时透传并注入匿名身份。
func AuthMiddleware(settings *Settings) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// 健康端点豁免
		if IsHealthPathOrMethod(path) && settings.HealthNoAuth {
			c.Set(IdentityContextKey, &Identity{ServiceType: "internal", Name: "health-probe", Scopes: []string{"*"}})
			c.Next()
			return
		}

		if !settings.AuthEnabled {
			c.Set(IdentityContextKey, AnonymousIdentity)
			c.Next()
			return
		}

		token := extractBearerToken(c.GetHeader("Authorization"))
		if token == "" {
			middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized: missing credentials", nil)
			return
		}

		identity := authenticateAPIKey(settings, token)
		if identity == nil {
			middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized: invalid credentials", nil)
			return
		}

		// 接口级权限校验 (PermissionForRESTPath)
		requiredPerm := PermissionForRESTPath(path)
		if requiredPerm != "*" && !identity.HasPermission(requiredPerm) {
			middleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Forbidden: insufficient scope", nil)
			return
		}

		c.Set(IdentityContextKey, identity)
		c.Next()
	}
}

// RequirePermission 返回需要指定权限的 Gin 中间件。
func RequirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity := GetIdentity(c)
		if identity == nil {
			middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "No identity in context", nil)
			return
		}
		if !identity.HasPermission(permission) {
			middleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Forbidden: insufficient scope", nil)
			return
		}
		c.Next()
	}
}

// RequireAnyPermission 返回需要任一指定权限的 Gin 中间件。
func RequireAnyPermission(permissions ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity := GetIdentity(c)
		if identity == nil {
			middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "No identity in context", nil)
			return
		}
		for _, p := range permissions {
			if identity.HasPermission(p) {
				c.Next()
				return
			}
		}
		middleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Forbidden: insufficient scope", nil)
	}
}

// GetIdentity 从 gin.Context 提取认证身份。
func GetIdentity(c *gin.Context) *Identity {
	v, exists := c.Get(IdentityContextKey)
	if !exists {
		return nil
	}
	id, ok := v.(*Identity)
	if !ok {
		return nil
	}
	return id
}
