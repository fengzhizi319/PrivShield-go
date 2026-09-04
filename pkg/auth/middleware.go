package auth

import (
	"crypto/subtle"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/fengzhizi319/PrivShield-go/pkg/envelope"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
)

// AuthFailuresTotal 认证失败计数器，按原因分类。
var AuthFailuresTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "privshield_auth_failures_total",
		Help: "Total number of authentication failures by reason",
	},
	[]string{"reason"},
)

// AuthForbiddenTotal 授权失败计数器（认证通过但权限不足）。
var AuthForbiddenTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "privshield_auth_forbidden_total",
		Help: "Total number of authorization failures (insufficient scope)",
	},
)

// IdentityContextKey 用于在 gin.Context 中存储认证身份。
const IdentityContextKey = "security_identity"

// abortWithError 中断请求并以统一错误信封格式输出 JSON 错误响应。
// 统一复用 pkg/envelope.ErrorEnvelope 标准 5 字段信封模型。
func abortWithError(c *gin.Context, httpStatus int, code string, message string, detail any) {
	traceID := pkgobs.GetTraceID(c)
	c.Header("X-Request-ID", traceID)
	c.Header("X-Trace-ID", traceID)
	c.AbortWithStatusJSON(httpStatus, envelope.ErrorEnvelope{
		Code:      code,
		Message:   message,
		Detail:    detail,
		TraceID:   traceID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// ExtractBearerToken 从 Authorization header 提取 Bearer token。
func ExtractBearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		return parts[1]
	}
	return ""
}

// ConstantTimeLookup 常量时间查找 token，防止计时攻击。
// 对 key 进行排序以确保确定性迭代顺序（Go map 迭代顺序随机），
// 遍历全部 key 且始终比较所有 key，避免时序侧信道泄漏。
// 栈上切片优化：8 个 key 以内使用栈数组，消除高并发堆分配与 GC 压力。
// 三级等保 G-14：跳过已过期的密钥。
func ConstantTimeLookup(keys map[string]*KeyConfig, token string) *KeyConfig {
	if len(keys) == 0 {
		return nil
	}
	tokenBytes := []byte(token)

	// 单 Key 快速常量路径：无需排序，直接恒定时间比对，零内存分配
	if len(keys) == 1 {
		for k, v := range keys {
			if subtle.ConstantTimeCompare([]byte(k), tokenBytes) == 1 {
				if v != nil && !v.IsExpired() {
					return v
				}
			}
		}
		return nil
	}

	// 栈缓冲区小切片：8 个 key 以内栈上完成，避免每次 HTTP 请求在堆上分配切片
	var stackBuf [8]string
	var sortedKeys []string
	if len(keys) <= len(stackBuf) {
		sortedKeys = stackBuf[:0]
	} else {
		sortedKeys = make([]string, 0, len(keys))
	}
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	var matched *KeyConfig
	for _, key := range sortedKeys {
		// subtle.ConstantTimeCompare 确保每次比较耗时恒定
		if subtle.ConstantTimeCompare([]byte(key), tokenBytes) == 1 {
			matched = keys[key]
		}
	}
	if matched != nil && matched.IsExpired() {
		return nil
	}
	return matched
}

// AuthenticateAPIKey 在内部和外部 key 存储中查找 token。
// 过期 key（IsExpired() == true）视为无效，返回 nil。
//
// 热轮转语义：若 Settings.LiveInternalKeys 已提供，先查活密钥（KeyStore 当前快照），
// 再回退静态内部密钥。REST 与 gRPC 拦截器都调本函数，因此密钥轮换/吊销在两条路径上
// 行为完全一致，不需各服务自己复制“热重载中间件”（历史上副本会遗漏 scope 鉴权）。
func AuthenticateAPIKey(settings *Settings, token string) *Identity {
	if settings == nil {
		return nil
	}
	if settings.LiveInternalKeys != nil {
		if live := ConstantTimeLookup(settings.LiveInternalKeys(), token); live != nil {
			return &Identity{ServiceType: "internal", Name: live.Name, Scopes: live.Scopes}
		}
	}
	if internal := ConstantTimeLookup(settings.InternalKeys, token); internal != nil {
		if internal.IsExpired() {
			return nil
		}
		return &Identity{ServiceType: "internal", Name: internal.Name, Scopes: internal.Scopes}
	}
	if external := ConstantTimeLookup(settings.ExternalKeys, token); external != nil {
		if external.IsExpired() {
			return nil
		}
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

		token := ExtractBearerToken(c.GetHeader("Authorization"))
		if token == "" {
			AuthFailuresTotal.WithLabelValues("missing_token").Inc()
			abortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized: missing credentials", nil)
			return
		}

		identity := AuthenticateAPIKey(settings, token)
		if identity == nil {
			AuthFailuresTotal.WithLabelValues("invalid_token").Inc()
			abortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized: invalid credentials", nil)
			return
		}

		// 接口级权限校验 (PermissionForRESTPath)
		// 映射函数对未显式登记的路径 fail-closed 返回最高权限 "admin"（而非空串），
		// 因此新增路由若遗忘配 scope 会被默认锁死，由启动期审计与 CI 门禁提醒补显式映射。
		requiredPerm := PermissionForRESTPath(path)
		if requiredPerm != "" && !identity.HasPermission(requiredPerm) {
			AuthForbiddenTotal.Inc()
			abortWithError(c, http.StatusForbidden, "FORBIDDEN", "Forbidden: insufficient scope", nil)
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
			abortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "No identity in context", nil)
			return
		}
		if !identity.HasPermission(permission) {
			abortWithError(c, http.StatusForbidden, "FORBIDDEN", "Forbidden: insufficient scope", nil)
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
			abortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "No identity in context", nil)
			return
		}
		for _, p := range permissions {
			if identity.HasPermission(p) {
				c.Next()
				return
			}
		}
		abortWithError(c, http.StatusForbidden, "FORBIDDEN", "Forbidden: insufficient scope", nil)
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
