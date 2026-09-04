// middleware.go 实现 JWT 认证中间件与角色权限校验。
//
// 认证流程：
//  1. 从 Authorization: Bearer <token> 头提取 JWT
//  2. 校验签名与过期时间
//  3. 将用户信息注入 Gin Context（X-User-Name / X-User-Role）
//  4. 角色中间件根据角色决定是否放行

package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Context key 常量 — 用于在 Gin Context 中传递用户信息。
const (
	CtxKeyUserName = "X-User-Name"
	CtxKeyUserRole = "X-User-Role"
	CtxKeyClaims   = "X-User-Claims"
)

// JWTAuthMiddleware 返回 JWT 认证中间件。
//
// 执行逻辑：
//  1. authEnabled=false → 放行（开发模式兼容）
//  2. 健康检查端点 /v1/auth/* 公开端点 → 放行
//  3. 提取 Bearer token → 校验 → 注入用户信息到 Context
//  4. 校验失败 → 401 UNAUTHORIZED
func JWTAuthMiddleware(jwtMgr *JWTManager, authEnabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 认证未启用 → 放行（开发模式）
		if !authEnabled {
			c.Next()
			return
		}

		path := c.Request.URL.Path

		// 健康探针豁免
		if path == "/health" || path == "/readyz" {
			c.Next()
			return
		}

		// 公开认证端点豁免
		if strings.HasPrefix(path, "/v1/auth/") {
			c.Next()
			return
		}

		// 提取 Bearer token
		token := extractBearerToken(c.GetHeader("Authorization"))
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": "Unauthorized: missing or invalid bearer token",
				"via":     "app-lz-bff",
			})
			return
		}

		// 校验 JWT
		claims, err := jwtMgr.ValidateToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    "UNAUTHORIZED",
				"message": "Unauthorized: " + err.Error(),
				"via":     "app-lz-bff",
			})
			return
		}

		// 注入用户信息到 Context（下游 handler 和角色中间件使用）
		c.Set(CtxKeyUserName, claims.Subject)
		c.Set(CtxKeyUserRole, claims.Role)
		c.Set(CtxKeyClaims, claims)

		c.Next()
	}
}

// RequireRole 返回角色校验中间件。
// 仅允许指定角色的用户访问受保护端点。
//
// 使用方法：
//
//	adminRoutes := r.Group("/v1/lz")
//	adminRoutes.Use(RequireRole("admin"))
//	adminRoutes.GET("/metrics", handler)
func RequireRole(allowedRoles ...string) gin.HandlerFunc {
	roleSet := make(map[string]bool, len(allowedRoles))
	for _, r := range allowedRoles {
		roleSet[r] = true
	}

	return func(c *gin.Context) {
		// 从 Context 提取角色（由 JWTAuthMiddleware 注入）
		role, exists := c.Get(CtxKeyUserRole)
		if !exists {
			// 认证未启用时没有角色信息 → 放行
			c.Next()
			return
		}

		roleStr, ok := role.(string)
		if !ok || !roleSet[roleStr] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    "FORBIDDEN",
				"message": "Forbidden: insufficient permissions for this endpoint",
				"via":     "app-lz-bff",
			})
			return
		}

		c.Next()
	}
}

// GetUserName 从 Gin Context 获取当前用户名。
func GetUserName(c *gin.Context) string {
	if v, exists := c.Get(CtxKeyUserName); exists {
		if name, ok := v.(string); ok {
			return name
		}
	}
	return ""
}

// GetUserRole 从 Gin Context 获取当前用户角色。
func GetUserRole(c *gin.Context) string {
	if v, exists := c.Get(CtxKeyUserRole); exists {
		if role, ok := v.(string); ok {
			return role
		}
	}
	return ""
}

// GetClaims 从 Gin Context 获取完整 JWT Claims。
func GetClaims(c *gin.Context) *Claims {
	if v, exists := c.Get(CtxKeyClaims); exists {
		if claims, ok := v.(*Claims); ok {
			return claims
		}
	}
	return nil
}

// extractBearerToken 从 Authorization 头提取 Bearer token。
func extractBearerToken(authHeader string) string {
	if authHeader == "" {
		return ""
	}
	// 格式: "Bearer <token>"
	const prefix = "Bearer "
	if len(authHeader) > len(prefix) && strings.EqualFold(authHeader[:len(prefix)], prefix) {
		return strings.TrimSpace(authHeader[len(prefix):])
	}
	return ""
}
