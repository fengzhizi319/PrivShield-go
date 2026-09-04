// Package middleware provides authentication and request interception middlewares.
// Package middleware 提供基于 API Key 的安全鉴权与请求拦截中间件。
//
// ==============================================================================
// 【安全设计与架构约束】
// 1. 【RFC 6750 Bearer 规范】：遵循标准 Authorization: Bearer <API_KEY> 格式；
// 2. 【防时序攻击 (Constant-Time Compare)】：
//    使用 crypto/subtle.ConstantTimeCompare 执行密钥校验，杜绝通过网络响应时间反推密钥字符的时序攻击（Timing Attack）；
// 3. 【灵活放行机制】：
//    - apiKey 为空串时：视为开发/本地调试模式，自动放行所有请求；
//    - 健康探针白名单：/health 与 /readyz 绝对豁免鉴权，确保 K8s 与云负载均衡探活畅通；
//    - 作用域边界：/v1/* 业务路径与 /metrics 强制鉴权（P1-6），仅 /health、/readyz 豁免；
// 4. 【统一错误信封】：未携带 Token 或 Key 错误时，统一调用 AbortWithError 输出 HTTP 401 信封。
// 5. 【权责分离 (AuthWithRoles)】：存证核验专区只需读、写入方只需写；只读核验员 Key 的可访问端点
//    由「方法 + 路径」白名单显式列出，越权直接 403（不静默降级为可读）。
// ==============================================================================

package middleware

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
)

// Auth returns an optional API Key authentication middleware.
//
// Auth 返回 API Key 安全鉴权中间件。
//
// 使用方法：
// ```go
// router.Use(middleware.Auth(cfg.APIKey))
// ```
//
// 执行逻辑：
// 1. 若 apiKey 为空：跳过鉴权直接调用 c.Next()（开发模式兼容）；
// 2. 检查请求路径 path：
//   - 若为 "/health" 或 "/readyz"：直接 c.Next() 放行（探活豁免）；
//   - 若不以 "/v1/" 开头且不是 "/metrics"：直接 c.Next() 放行（非核心路径豁免）；
//
// 3. 从 Authorization 请求头解析 Bearer 令牌；若未提供或格式非法，立即响应 401 UNAUTHORIZED；
// 4. 使用 subtle.ConstantTimeCompare 校验传入的 Token 与服务端 apiKey 是否完全一致；
//   - 不一致：立即响应 401 UNAUTHORIZED 错误信封并阻断后续调用；
//   - 一致：调用 c.Next() 继续处理业务。
func Auth(apiKey string) gin.HandlerFunc {
	if apiKey == "" {
		slog.Warn("middleware.Auth: empty API key, all requests will pass through unauthenticated; set API key for production deployments")
	}
	return func(c *gin.Context) {
		// apiKey 为空 → 跳过鉴权（开发模式）
		if apiKey == "" {
			c.Next()
			return
		}

		path := c.Request.URL.Path

		// 健康检查端点豁免
		if path == "/health" || path == "/readyz" {
			c.Next()
			return
		}

		// 非核心路径豁免（/metrics 不在此列 —— P1-6 纳入鉴权）
		if !strings.HasPrefix(path, "/v1/") && path != "/metrics" {
			c.Next()
			return
		}

		// 提取 Bearer token
		token := pkgauth.ExtractBearerToken(c.GetHeader("Authorization"))
		if token == "" {
			AbortWithError(c, http.StatusUnauthorized,
				"UNAUTHORIZED",
				"Unauthorized: missing or invalid bearer token",
				nil,
			)
			return
		}

		// 常量时间比较，防止时序攻击
		if subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
			AbortWithError(c, http.StatusUnauthorized,
				"UNAUTHORIZED",
				"Unauthorized: invalid api key",
				nil,
			)
			return
		}

		c.Next()
	}
}

// AuthWithRoles 在单 Key 鉴权之上叠加「只读核验员」角色（第十二章 P1-6 ②③）。
//
// 动机：数据局核验专区只需要读存证 / 验真，却与写入方共用同一把 AUDIT_LOG_API_KEY，
// 等于「被查者握着查账凭证」。本中间件把可访问端点显式列成「方法 + 路径」白名单，由调用方（服务侧）给出：
//  1. token == apiKey      → 全量放行（运维 / 业务写入身份）；
//  2. token == readerKey 且 (method, path) 命中 readOnly → 放行（只读核验身份）；
//  3. token == readerKey 但未命中白名单                 → 403 FORBIDDEN（显式拒绝，绝不静默降级为可读）；
//  4. 两把 Key 都不匹配                                 → 401 UNAUTHORIZED。
//
// 白名单必须带 method：同一 /v1/audit/logs 上 GET 是查询、POST 是写入，只比路径会把写权限漏给核验员。
// readerKey 为空时完全退化为 Auth(apiKey) 的既有语义（存量部署零影响），是否启用由部署方显式决定。
// apiKey 为空仍是开发态放行（与 Auth 一致；非环回暴露时 P0-1 启动门禁已保证 apiKey 非空）。
func AuthWithRoles(apiKey, readerKey string, readOnly []ReadOnlyEndpoint) gin.HandlerFunc {
	fullAccess := Auth(apiKey)
	if readerKey == "" {
		return fullAccess
	}

	return func(c *gin.Context) {
		if apiKey == "" {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		if path == "/health" || path == "/readyz" {
			c.Next()
			return
		}
		// /metrics 纳入鉴权（P1-6）：非 /v1/ 且非 /metrics 的路径才豁免
		if !strings.HasPrefix(path, "/v1/") && path != "/metrics" {
			c.Next()
			return
		}

		token := pkgauth.ExtractBearerToken(c.GetHeader("Authorization"))
		if token == "" {
			AbortWithError(c, http.StatusUnauthorized,
				"UNAUTHORIZED",
				"Unauthorized: missing or invalid bearer token",
				nil,
			)
			return
		}

		// 完整权限身份优先：常量时间比较，避免任何一路径的时序差异。
		if subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) == 1 {
			c.Next()
			return
		}

		if subtle.ConstantTimeCompare([]byte(token), []byte(readerKey)) == 1 {
			if !IsReadOnlyEndpoint(c.Request.Method, path, readOnly) {
				AbortWithError(c, http.StatusForbidden,
					"FORBIDDEN",
					"Forbidden: reader key is limited to verification endpoints",
					nil,
				)
				return
			}
			c.Next()
			return
		}

		AbortWithError(c, http.StatusUnauthorized,
			"UNAUTHORIZED",
			"Unauthorized: invalid api key",
			nil,
		)
	}
}

// ReadOnlyEndpoint 描述一条允许只读核验员访问的端点。
// Path 为前缀，匹配以 "/" 为边界：/v1/audit/logs 覆盖 /v1/audit/logs/123，
// 但不覆盖 /v1/audit/logs-backup。
type ReadOnlyEndpoint struct {
	Method string
	Path   string
}

// IsReadOnlyEndpoint 判断 (method, path) 是否落在只读核验白名单内。
func IsReadOnlyEndpoint(method, path string, readOnly []ReadOnlyEndpoint) bool {
	cleaned := path
	if cleaned != "/" {
		cleaned = strings.TrimRight(cleaned, "/")
	}
	for _, ep := range readOnly {
		if ep.Path == "" || !strings.EqualFold(ep.Method, method) {
			continue
		}
		if cleaned == ep.Path || strings.HasPrefix(cleaned, ep.Path+"/") {
			return true
		}
	}
	return false
}
