// Package auth — 用户与权限管理 REST API 控制器。
//
// 提供注册、登录/登出、口令修改、权限授予/调整、账号冻结/注销、动态 API Key 签发与吊销端点。
//
// 授权模型（路由层只要求「已认证」，授权在此按主体判定）：
//
//	读（本人资料/密钥清单）  = 本人(Identity.Subject) | user:read | user:admin
//	写（签发/吊销/改密）     = 本人(Identity.Subject) | user:admin
//	管理（列表/改权/冻结/注销）= user:admin（或 "*" / "admin"）
//
// 全部响应统一使用 pkg/envelope 的 5 字段信封；特权操作输出结构化审计日志（不含口令与明文 Token）。
package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield-go/pkg/envelope"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
)

// ============================================================================
// 路由挂载与可选配置
// ============================================================================

// UserRouteConfig 保存用户管理端点的可选运行策略。
type UserRouteConfig struct {
	// Logger 审计日志输出器；nil 时使用 slog.Default()。
	Logger *slog.Logger
	// SelfRegister 是否允许「非引导期」的公开自注册（默认 false：账号一律由管理员开户）。
	// 引导期（用户库为空）始终允许创建首个 admin 账号，与本开关无关。
	SelfRegister bool
	// SessionTTL 登录会话有效期（默认 DefaultSessionTTL=24h，上限 MaxSessionTTL）。
	SessionTTL time.Duration
	// LoginThrottlePerWindow 登录端点每 IP 每窗口最大尝试次数（<=0 表示禁用该层限速）。
	LoginThrottlePerWindow int
}

// UserRouteOption 为 RegisterUserRoutes 的可选配置项。
type UserRouteOption func(*UserRouteConfig)

// WithUserAuditLogger 指定用户管理审计日志输出器。
func WithUserAuditLogger(logger *slog.Logger) UserRouteOption {
	return func(cfg *UserRouteConfig) { cfg.Logger = logger }
}

// WithSelfRegistration 开启/关闭公开自注册（默认关闭，生产建议保持关闭）。
func WithSelfRegistration(enabled bool) UserRouteOption {
	return func(cfg *UserRouteConfig) { cfg.SelfRegister = enabled }
}

// WithSessionTTL 指定登录会话有效期。
func WithSessionTTL(ttl time.Duration) UserRouteOption {
	return func(cfg *UserRouteConfig) { cfg.SessionTTL = ttl }
}

// WithLoginThrottle 指定登录端点每 IP 每窗口最大尝试次数。
func WithLoginThrottle(maxPerWindow int) UserRouteOption {
	return func(cfg *UserRouteConfig) { cfg.LoginThrottlePerWindow = maxPerWindow }
}

// RegisterUserRoutes 在 Gin 引擎上挂载用户与权限管理相关 REST API 路由。
func RegisterUserRoutes(r *gin.Engine, store *UserStore, opts ...UserRouteOption) {
	if store == nil {
		return
	}
	cfg := &UserRouteConfig{
		Logger:                 slog.Default(),
		SessionTTL:             DefaultSessionTTL,
		LoginThrottlePerWindow: LoginThrottleMaxPerIP,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	api := &userAPI{store: store, cfg: cfg, throttle: newLoginThrottle()}
	store.SetAuditLogger(cfg.Logger)

	g := r.Group("/v1/auth")
	{
		// 1. 公开认证端点（免密）
		g.POST("/login", api.handleLogin)
		g.POST("/users/register", api.handleRegister)

		// 2. 会话与口令（需认证）
		g.POST("/logout", api.handleLogout)
		g.POST("/change-password", api.handleChangePassword)

		// 3. 用户全生命周期管理
		g.GET("/users", api.handleListUsers)
		g.GET("/users/:username", api.handleGetUser)
		g.PUT("/users/:username/permissions", api.handleUpdatePermissions)
		g.PUT("/users/:username/status", api.handleSetStatus)
		g.DELETE("/users/:username", api.handleDeleteUser)

		// 4. 用户动态 API Key 生命周期管理
		g.POST("/users/:username/keys", api.handleIssueKey)
		g.GET("/users/:username/keys", api.handleListKeys)
		g.DELETE("/users/:username/keys/:key_id", api.handleRevokeKey)
	}
}

// userPolicyEnv 声明单个服务的用户管理策略环境变量全集。
type userPolicyEnv struct {
	selfRegister  string // bool：是否开放公开自注册
	sessionTTL    string // duration：登录会话有效期
	loginThrottle string // int：登录端点每 IP 每分钟最大尝试次数
}

// userPolicyEnvTable 是两端用户管理策略变量的**唯一事实源**。
// 显式列出完整变量名而非在运行期拼接前缀，原因有二：
//  1. `scripts/check_orchestration_env_consistency.sh` 以「Go 源码中的字符串字面量」判定变量是否
//     已被消费，动态拼接会使编排清单里的声明被误判为幽灵变量而卡住 CI 门禁；
//  2. 安全白皮书与 `.env.example` 可直接对照本表核对口径，避免文档漂移。
var userPolicyEnvTable = map[string]userPolicyEnv{
	"AGENT_AUTH": {
		selfRegister:  "AGENT_AUTH_USER_SELF_REGISTER",
		sessionTTL:    "AGENT_AUTH_USER_SESSION_TTL",
		loginThrottle: "AGENT_AUTH_USER_LOGIN_THROTTLE_PER_MIN",
	},
	"SERVICE_HUB": {
		selfRegister:  "SERVICE_HUB_USER_SELF_REGISTER",
		sessionTTL:    "SERVICE_HUB_USER_SESSION_TTL",
		loginThrottle: "SERVICE_HUB_USER_LOGIN_THROTTLE_PER_MIN",
	},
}

// userPolicyEnvFor 返回指定前缀的变量名集合；未登记的前缀按 `<prefix>_USER_*` 约定派生，
// 保持对新接入服务的向后兼容。
func userPolicyEnvFor(prefix string) userPolicyEnv {
	if env, ok := userPolicyEnvTable[prefix]; ok {
		return env
	}
	return userPolicyEnv{
		selfRegister:  prefix + "_USER_SELF_REGISTER",
		sessionTTL:    prefix + "_USER_SESSION_TTL",
		loginThrottle: prefix + "_USER_LOGIN_THROTTLE_PER_MIN",
	}
}

// UserRouteOptionsFromEnv 按服务前缀从环境变量构造 RegisterUserRoutes 的可选配置，
// 使 privacy-engine（AGENT_AUTH）与 service-hub（SERVICE_HUB）共用同一策略口径：
//
//	<prefix>_USER_SELF_REGISTER          bool，是否开放公开自注册（默认 false，生产建议关闭）
//	<prefix>_USER_SESSION_TTL            登录会话有效期，支持 "24h"/"15m" 或纯秒数（默认 24h，上限 24h）
//	<prefix>_USER_LOGIN_THROTTLE_PER_MIN 登录端点每 IP 每分钟最大尝试次数（<=0 关闭该层，默认 20）
//
// 未设置或解析失败的变量一律保留默认值（不 panic、不静默放宽）。
func UserRouteOptionsFromEnv(prefix string) []UserRouteOption {
	if strings.TrimSpace(prefix) == "" {
		return nil
	}
	env := userPolicyEnvFor(prefix)
	opts := make([]UserRouteOption, 0, 3)
	if v := strings.TrimSpace(os.Getenv(env.selfRegister)); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			opts = append(opts, WithSelfRegistration(b))
		}
	}
	if v := strings.TrimSpace(os.Getenv(env.sessionTTL)); v != "" {
		if d, err := parseDurationOrSeconds(v); err == nil && d > 0 {
			opts = append(opts, WithSessionTTL(d))
		}
	}
	if v := strings.TrimSpace(os.Getenv(env.loginThrottle)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts = append(opts, WithLoginThrottle(n))
		}
	}
	return opts
}

// parseDurationOrSeconds 兼容 "24h"/"15m" 与纯秒数（"3600"）两种写法。
func parseDurationOrSeconds(v string) (time.Duration, error) {
	if d, err := time.ParseDuration(v); err == nil {
		return d, nil
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	return 0, errors.New("invalid duration: " + v)
}

// ============================================================================
// 请求与响应载荷模型
// ============================================================================

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type loginResponse struct {
	Token     string      `json:"token"`
	TokenType string      `json:"token_type"`
	ExpiresAt time.Time   `json:"expires_at"`
	User      UserSummary `json:"user"`
	// PasswordExpired 标记口令已超过 MaxPasswordAge（等保三级 G-04 定期更换）。
	// 仅标记不阻断登录（避免唯一管理员被锁死），供前端引导改密与合规巡检取证。
	PasswordExpired bool `json:"password_expired"`
}

type registerRequest struct {
	Username    string   `json:"username" binding:"required"`
	Password    string   `json:"password" binding:"required"`
	DisplayName string   `json:"display_name"`
	Role        string   `json:"role"`
	Scopes      []string `json:"scopes"`
}

type updatePermissionsRequest struct {
	Role   string   `json:"role"`
	Scopes []string `json:"scopes"`
}

type setStatusRequest struct {
	Status UserStatus `json:"status" binding:"required"`
	Reason string     `json:"reason"`
}

type issueKeyRequest struct {
	KeyName    string   `json:"key_name"`
	Scopes     []string `json:"scopes"`
	TTLSeconds int64    `json:"ttl_seconds"` // 0 → 默认 30 天；上限 90 天（等保三级 G-14 密钥有效期）
}

type changePasswordRequest struct {
	Username    string `json:"username" binding:"required"`
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

// ============================================================================
// 权限辅助判定
// ============================================================================

func isCallerAdmin(id *Identity) bool {
	if id == nil {
		return true // 未启用认证（开发/免密模式）时不做应用层限制，与仓库既有约定一致
	}
	return id.HasPermission("*") || id.HasPermission("admin") || id.HasPermission("user:admin")
}

// canViewUserAccount 读权限：本人 | user:read | user:admin。
func canViewUserAccount(id *Identity, targetUsername string) bool {
	if id == nil {
		return true
	}
	if isCallerAdmin(id) || id.HasPermission("user:read") {
		return true
	}
	return id.IsSubject(targetUsername)
}

// canManageUserAccount 写权限：本人 | user:admin。
// 注意：**user:read 不得**授予写能力，否则任何持有只读审计 scope 的账号都能替他人签发/吊销
// 密钥或改密（越权提权）。
func canManageUserAccount(id *Identity, targetUsername string) bool {
	if id == nil {
		return true
	}
	if isCallerAdmin(id) {
		return true
	}
	return id.IsSubject(targetUsername)
}

// callerLabel 返回审计日志中的调用者标识（优先主体，其次密钥名）。
func callerLabel(id *Identity) string {
	if id == nil {
		return "anonymous"
	}
	if id.Subject != "" {
		return id.Subject
	}
	if id.Name != "" {
		return id.Name
	}
	return "anonymous"
}

// ============================================================================
// 响应信封
// ============================================================================

// respondSuccess 以统一 5 字段成功信封（code/message/data/trace_id/timestamp）输出。
func (a *userAPI) respondSuccess(c *gin.Context, message string, data any) {
	traceID := pkgobs.GetTraceID(c)
	c.Header("X-Request-ID", traceID)
	c.Header("X-Trace-ID", traceID)
	c.JSON(http.StatusOK, envelope.SuccessEnvelope{
		Code:      "OK",
		Message:   message,
		Data:      data,
		TraceID:   traceID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (a *userAPI) respondError(c *gin.Context, status int, code, message string, detail any) {
	abortWithError(c, status, code, message, detail)
}

// logger 返回审计日志输出器（未配置时回退 slog.Default，避免直接构造 userAPI 时空指针）。
func (a *userAPI) logger() *slog.Logger {
	if a == nil || a.cfg == nil || a.cfg.Logger == nil {
		return slog.Default()
	}
	return a.cfg.Logger
}

// internalError 输出统一的服务端错误响应：对外**仅暴露泛化文案**（搭配 trace_id 供排障），
// 具体错误细节（文件路径、OS 错误码、内部状态）只写服务端日志。
//
// 动机：直接把 err.Error() 回给客户端会把部署拓扑与文件系统布局泄露给未信任调用者
// （例如 "mkdir /var/lib/privshield: permission denied"），为后续攻击提供情报；
// 等保三级 G-11 要求最小化对外信息暴露。
func (a *userAPI) internalError(c *gin.Context, event, target, publicMessage string, err error) {
	logger := a.logger()
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	logger.Error("auth_internal_error",
		"event", event,
		"actor", callerLabel(GetIdentity(c)),
		"target_user", target,
		"error", reason,
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"client_ip", c.ClientIP(),
		"trace_id", pkgobs.GetTraceID(c),
	)
	a.respondError(c, http.StatusInternalServerError, "INTERNAL_ERROR", publicMessage, nil)
}

// audit 输出结构化审计日志（等保三级 G-07：管理操作可追溯）。
// 严禁记录口令、明文 Token；密钥仅记录脱敏前缀。
func (a *userAPI) audit(c *gin.Context, event, target, result, reason string, extra ...any) {
	logger := a.logger()
	args := make([]any, 0, len(extra)+16)
	args = append(args,
		"event", event,
		"actor", callerLabel(GetIdentity(c)),
		"target_user", target,
		"result", result,
		"reason", reason,
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"client_ip", c.ClientIP(),
		"trace_id", pkgobs.GetTraceID(c),
	)
	args = append(args, extra...)
	logger.Info("auth_audit", args...)
}

// ============================================================================
// 登录端点限速（每 IP 固定窗口）
// ============================================================================

type loginThrottle struct {
	shards [8]throttleShard
}

type throttleShard struct {
	mu      sync.Mutex
	entries map[string]throttleWindow
}

type throttleWindow struct {
	count   int
	resetAt time.Time
}

func newLoginThrottle() *loginThrottle {
	t := &loginThrottle{}
	for i := range t.shards {
		t.shards[i].entries = make(map[string]throttleWindow)
	}
	return t
}

// allow 判定 key 是否仍在窗口配额内；超限时返回 false 与建议等待时长。
func (t *loginThrottle) allow(key string, limit int, window time.Duration) (bool, time.Duration) {
	if limit <= 0 || key == "" {
		return true, 0
	}
	idx := 0
	for i := 0; i < len(key); i++ {
		idx = (idx*31 + int(key[i])) & 0x7fffffff
	}
	shard := &t.shards[idx%len(t.shards)]

	shard.mu.Lock()
	defer shard.mu.Unlock()

	now := time.Now()
	// 惰性清理：条目过多时回收已过期窗口，防止来源 IP 伪造导致的内存膨胀。
	if len(shard.entries) > 4096 {
		for k, w := range shard.entries {
			if now.After(w.resetAt) {
				delete(shard.entries, k)
			}
		}
	}

	w, ok := shard.entries[key]
	if !ok || now.After(w.resetAt) {
		shard.entries[key] = throttleWindow{count: 1, resetAt: now.Add(window)}
		return true, 0
	}
	w.count++
	shard.entries[key] = w
	if w.count > limit {
		retry := time.Until(w.resetAt)
		if retry < 0 {
			retry = 0
		}
		return false, retry
	}
	return true, 0
}

// ============================================================================
// 控制器实现
// ============================================================================

type userAPI struct {
	store    *UserStore
	cfg      *UserRouteConfig
	throttle *loginThrottle
}

// handleLogin 处理账号密码认证登录，签发内存态会话 Token（默认 24h，不落盘）。
func (a *userAPI) handleLogin(c *gin.Context) {
	ip := c.ClientIP()
	// 限速键带端点前缀，使登录与注册共用实现但**各自独立计数**（否则一个洪水端点会
	// 误伤另一个端点的正常调用者）。
	if ok, retryAfter := a.throttle.allow("login:"+ip, a.cfg.LoginThrottlePerWindow, LoginThrottleWindow); !ok {
		seconds := int(retryAfter.Seconds()) + 1
		c.Header("Retry-After", strconv.Itoa(seconds))
		a.respondError(c, http.StatusTooManyRequests, "RATE_LIMITED", "too many login attempts from this client, slow down", nil)
		a.audit(c, "login", "", "denied", "ip_throttled", "client_ip", ip, "retry_after_seconds", seconds)
		return
	}

	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.respondError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid login payload", err.Error())
		return
	}
	username := NormalizeUsername(req.Username)

	user, err := a.store.Authenticate(username, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrAccountLocked):
			remaining := a.store.LockoutRemaining(username)
			seconds := int(remaining.Seconds()) + 1
			c.Header("Retry-After", strconv.Itoa(seconds))
			a.respondError(c, http.StatusTooManyRequests, "ACCOUNT_LOCKED",
				"account is temporarily locked due to too many failed attempts", gin.H{"retry_after_seconds": seconds})
			a.audit(c, "login", username, "denied", "account_locked", "retry_after_seconds", seconds)
		case errors.Is(err, ErrUserDisabled):
			a.respondError(c, http.StatusForbidden, "ACCOUNT_DISABLED", "account is disabled", nil)
			a.audit(c, "login", username, "denied", "account_disabled")
		default:
			// 用户不存在与口令错误统一响应，抑制账号枚举
			a.respondError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid username or password", nil)
			a.audit(c, "login", username, "denied", "invalid_credentials")
		}
		return
	}

	token, expiresAt, err := a.store.CreateSession(user.Username, a.cfg.SessionTTL)
	if err != nil {
		a.internalError(c, "login", user.Username, "failed to create session", err)
		return
	}

	passwordExpired := user.PasswordExpired()
	if passwordExpired {
		// 等保三级 G-04：口令超期必须可发现、可追溯（不阻断登录，由巡检驱动闭环改密）。
		a.logger().Warn("auth_audit: password exceeded maximum age",
			"event", "password_expired",
			"target_user", user.Username,
			"password_updated_at", user.PasswordUpdatedAt.Format(time.RFC3339),
			"max_password_age_days", int(MaxPasswordAge/(24*time.Hour)),
			"client_ip", ip,
			"trace_id", pkgobs.GetTraceID(c),
		)
	}

	a.audit(c, "login", user.Username, "success", "",
		"session_expires_at", expiresAt.Format(time.RFC3339),
		"password_expired", passwordExpired)
	a.respondSuccess(c, "login succeeded", loginResponse{
		Token:           token,
		TokenType:       "Bearer",
		ExpiresAt:       expiresAt,
		User:            user.ToSummary(),
		PasswordExpired: passwordExpired,
	})
}

// handleLogout 注销当前请求所使用的登录会话 Token。
func (a *userAPI) handleLogout(c *gin.Context) {
	token := ExtractBearerToken(c.GetHeader("Authorization"))
	caller := GetIdentity(c)
	if token == "" {
		a.respondError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "missing bearer token", nil)
		return
	}
	if err := a.store.RevokeSession(token); err != nil {
		// 非会话型凭证（长期 API Key）不应经登出端点吊销，返回 404 但不视为服务异常
		a.respondError(c, http.StatusNotFound, "SESSION_NOT_FOUND", "session not found or already revoked", nil)
		a.audit(c, "logout", callerLabel(caller), "denied", "session_not_found")
		return
	}
	a.audit(c, "logout", callerLabel(caller), "success", "", "token_prefix", tokenPrefix(token))
	a.respondSuccess(c, "session revoked", gin.H{"revoked": true})
}

// handleRegister 处理新用户自主注册（引导期/开关开启）或管理员开户。
//
// 引导期（用户库为空）是唯一的**免认证开户窗口**，因此受三重收敛：
//  1. 每 IP 限速：公开注册每次都跑一遍 bcrypt(cost=12)，不限速即未认证 CPU 耗尽放大器；
//  2. 「库为空」判定与写入由 UserStore.RegisterBootstrapAdmin 在同一把锁内原子完成，
//     杜绝并发双引导产生多个自封管理员（TOCTOU）；引导期忽略自定义 scope；
//  3. 匿名调用者（无身份上下文：认证中间件未生效或遗留免密透传）**不得**走管理员
//     开户通道，否则未认证请求可自封 admin 并授予 "*" 等任意 scope。
func (a *userAPI) handleRegister(c *gin.Context) {
	ip := c.ClientIP()
	if ok, retryAfter := a.throttle.allow("register:"+ip, a.cfg.LoginThrottlePerWindow, LoginThrottleWindow); !ok {
		seconds := int(retryAfter.Seconds()) + 1
		c.Header("Retry-After", strconv.Itoa(seconds))
		a.respondError(c, http.StatusTooManyRequests, "RATE_LIMITED", "too many registration attempts from this client, slow down", nil)
		a.audit(c, "register", "", "denied", "ip_throttled", "client_ip", ip, "retry_after_seconds", seconds)
		return
	}

	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.respondError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid register payload", err.Error())
		return
	}

	caller := GetIdentity(c)
	// 管理员开户必须具备**真实身份上下文**：nil 身份意味着认证中间件未生效（或遗留免密透传），
	// 此时授予「任意角色 + 自定义 scope」等于把最高权限白送给未认证调用者。
	isAdmin := caller != nil && isCallerAdmin(caller)
	bootstrap := !isAdmin && a.store.Count() == 0
	username := NormalizeUsername(req.Username)

	switch {
	case isAdmin:
		// 管理员开户：允许指定任意预置角色与自定义 scope
	case bootstrap:
		// 系统引导：用户库为空时允许创建首个管理员（避免管理面永久无主）
		if req.Role != "" && req.Role != "admin" {
			a.respondError(c, http.StatusBadRequest, "INVALID_BOOTSTRAP_ROLE",
				"the first account must be created with role 'admin'", nil)
			a.audit(c, "register", username, "denied", "bootstrap_role_not_admin", "role", req.Role)
			return
		}
		// 引导期忽略自定义 scope：未认证调用者不得借引导通道给自己组装任意权限，
		// 首个管理员一律使用 admin 角色的预置权限。
		if len(req.Scopes) > 0 {
			a.respondError(c, http.StatusBadRequest, "INVALID_BOOTSTRAP_SCOPES",
				"custom scopes are not accepted while bootstrapping the first administrator", nil)
			a.audit(c, "register", username, "denied", "bootstrap_custom_scopes")
			return
		}
		req.Role = "admin"
	case a.cfg.SelfRegister:
		// 公开自注册：强制降权为 developer，禁止自定义 scope 与特权角色
		if IsPrivilegedRole(req.Role) {
			a.respondError(c, http.StatusForbidden, "FORBIDDEN", "only administrators can assign privileged roles", nil)
			a.audit(c, "register", username, "denied", "privileged_role_forbidden", "role", req.Role)
			return
		}
		if len(req.Scopes) > 0 {
			a.respondError(c, http.StatusForbidden, "FORBIDDEN", "custom scopes can only be granted by administrator", nil)
			a.audit(c, "register", username, "denied", "custom_scope_forbidden")
			return
		}
		if req.Role == "" || req.Role == "admin" {
			req.Role = "developer"
		}
	default:
		a.respondError(c, http.StatusForbidden, "SELF_REGISTER_DISABLED", ErrSelfRegisterDisabled.Error(), nil)
		a.audit(c, "register", username, "denied", "self_register_disabled")
		return
	}

	var (
		user *User
		err  error
	)
	if bootstrap {
		// 原子引导：判定与写入同锁，并发竞争失败者得到 409（绝不静默降级为管理员开户）。
		user, err = a.store.RegisterBootstrapAdmin(req.Username, req.Password, req.DisplayName)
		if errors.Is(err, ErrBootstrapClosed) {
			a.respondError(c, http.StatusConflict, "BOOTSTRAP_CLOSED",
				"the first administrator has already been created; ask an administrator to provision this account", nil)
			a.audit(c, "register", username, "denied", "bootstrap_race_closed")
			return
		}
	} else {
		user, err = a.store.Register(req.Username, req.Password, req.DisplayName, req.Role, req.Scopes)
	}
	if err != nil {
		a.respondRegisterError(c, username, err)
		return
	}

	a.audit(c, "register", user.Username, "success", "",
		"role", user.Role, "bootstrap", bootstrap, "self_service", !isAdmin && !bootstrap)
	a.respondSuccess(c, "user registered", user.ToSummary())
}

func (a *userAPI) respondRegisterError(c *gin.Context, username string, err error) {
	switch {
	case errors.Is(err, ErrUserAlreadyExists):
		a.respondError(c, http.StatusConflict, "USER_EXISTS", "username already exists", nil)
		a.audit(c, "register", username, "denied", "user_exists")
	case errors.Is(err, ErrPasswordTooShort), errors.Is(err, ErrPasswordTooLong),
		errors.Is(err, ErrPasswordWeak), errors.Is(err, ErrPasswordContainsName),
		errors.Is(err, ErrPasswordBlacklisted):
		a.respondError(c, http.StatusBadRequest, "WEAK_PASSWORD", err.Error(), nil)
		a.audit(c, "register", username, "denied", "weak_password")
	case errors.Is(err, ErrInvalidUsername):
		a.respondError(c, http.StatusBadRequest, "INVALID_USERNAME", err.Error(), nil)
	case errors.Is(err, ErrInvalidRole):
		a.respondError(c, http.StatusBadRequest, "INVALID_ROLE", err.Error(), gin.H{"known_roles": KnownRoles})
	case errors.Is(err, ErrInvalidDisplayName), errors.Is(err, ErrInvalidScope), errors.Is(err, ErrTooManyScopes):
		a.respondError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
	case errors.Is(err, ErrForbiddenScope):
		a.respondError(c, http.StatusForbidden, "FORBIDDEN_SCOPE", err.Error(), nil)
		a.audit(c, "register", username, "denied", "forbidden_scope")
	default:
		a.internalError(c, "register", username, "failed to register user", err)
	}
}

// handleListUsers 获取全部用户清单（user:read | user:admin）。
func (a *userAPI) handleListUsers(c *gin.Context) {
	caller := GetIdentity(c)
	if caller != nil && !caller.HasPermission("user:read") && !isCallerAdmin(caller) {
		a.respondError(c, http.StatusForbidden, "FORBIDDEN", "permission denied: requires user:read or user:admin", nil)
		a.audit(c, "list_users", "", "denied", "insufficient_scope")
		return
	}
	users := a.store.ListUsers()
	a.audit(c, "list_users", "", "success", "", "count", len(users))
	a.respondSuccess(c, "users listed", users)
}

// handleGetUser 获取指定用户详细档案（本人 | user:read | user:admin）。
func (a *userAPI) handleGetUser(c *gin.Context) {
	username := c.Param("username")
	caller := GetIdentity(c)

	if !canViewUserAccount(caller, username) {
		a.respondError(c, http.StatusForbidden, "FORBIDDEN", "permission denied: cannot access user profile", nil)
		a.audit(c, "get_user", username, "denied", "insufficient_scope")
		return
	}

	user, err := a.store.GetUser(username)
	if err != nil {
		a.respondError(c, http.StatusNotFound, "USER_NOT_FOUND", "user not found", gin.H{"username": NormalizeUsername(username)})
		return
	}
	a.respondSuccess(c, "user profile", user.SanitizedCopy())
}

// handleUpdatePermissions 调整用户角色与 Scope 权限集合（user:admin）。
func (a *userAPI) handleUpdatePermissions(c *gin.Context) {
	username := c.Param("username")
	caller := GetIdentity(c)

	if !isCallerAdmin(caller) {
		a.respondError(c, http.StatusForbidden, "FORBIDDEN", "permission denied: requires user:admin", nil)
		a.audit(c, "update_permissions", username, "denied", "insufficient_scope")
		return
	}

	var req updatePermissionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.respondError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid payload", err.Error())
		return
	}

	if err := a.store.UpdatePermissions(username, req.Role, req.Scopes); err != nil {
		a.respondMutationError(c, "update_permissions", username, err)
		return
	}

	user, err := a.store.GetUser(username)
	if err != nil {
		a.internalError(c, "update_permissions", NormalizeUsername(username), "permissions updated but profile reload failed", err)
		return
	}
	a.audit(c, "update_permissions", user.Username, "success", "", "role", user.Role, "scopes", user.Scopes)
	a.respondSuccess(c, "permissions updated", user.ToSummary())
}

// handleSetStatus 更改用户账号状态（激活或冻结，user:admin）。
func (a *userAPI) handleSetStatus(c *gin.Context) {
	username := c.Param("username")
	caller := GetIdentity(c)

	if !isCallerAdmin(caller) {
		a.respondError(c, http.StatusForbidden, "FORBIDDEN", "permission denied: requires user:admin", nil)
		a.audit(c, "set_status", username, "denied", "insufficient_scope")
		return
	}

	var req setStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.respondError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid payload", err.Error())
		return
	}
	if req.Status != UserStatusActive && req.Status != UserStatusDisabled {
		a.respondError(c, http.StatusBadRequest, "INVALID_STATUS", ErrInvalidStatus.Error(), nil)
		return
	}
	if err := ValidateDisplayName(req.Reason); err != nil {
		a.respondError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid reason", err.Error())
		return
	}

	if err := a.store.SetStatus(username, req.Status); err != nil {
		a.respondMutationError(c, "set_status", username, err)
		return
	}

	user, err := a.store.GetUser(username)
	if err != nil {
		a.internalError(c, "set_status", NormalizeUsername(username), "status updated but profile reload failed", err)
		return
	}
	a.audit(c, "set_status", user.Username, "success", "", "status", string(user.Status), "reason", req.Reason)
	a.respondSuccess(c, "status updated", user.ToSummary())
}

// handleDeleteUser 注销并删除用户（user:admin）。
func (a *userAPI) handleDeleteUser(c *gin.Context) {
	username := c.Param("username")
	caller := GetIdentity(c)

	if !isCallerAdmin(caller) {
		a.respondError(c, http.StatusForbidden, "FORBIDDEN", "permission denied: requires user:admin", nil)
		a.audit(c, "delete_user", username, "denied", "insufficient_scope")
		return
	}

	if err := a.store.DeleteUser(username); err != nil {
		a.respondMutationError(c, "delete_user", username, err)
		return
	}

	a.audit(c, "delete_user", NormalizeUsername(username), "success", "")
	a.respondSuccess(c, "user deleted", gin.H{"deleted": NormalizeUsername(username)})
}

// handleIssueKey 为用户签发新 API Key（本人 | user:admin）。
func (a *userAPI) handleIssueKey(c *gin.Context) {
	username := c.Param("username")
	caller := GetIdentity(c)

	if !canManageUserAccount(caller, username) {
		a.respondError(c, http.StatusForbidden, "FORBIDDEN", "permission denied: cannot issue key for this user", nil)
		a.audit(c, "issue_key", username, "denied", "insufficient_scope")
		return
	}

	var req issueKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.respondError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid payload", err.Error())
		return
	}

	var ttl time.Duration
	if req.TTLSeconds != 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}

	record, token, err := a.store.IssueAPIKey(username, req.KeyName, req.Scopes, ttl)
	if err != nil {
		a.respondMutationError(c, "issue_key", username, err)
		return
	}

	a.audit(c, "issue_key", NormalizeUsername(username), "success", "",
		"key_id", record.KeyID, "key_name", record.Name, "token_prefix", record.TokenPrefix,
		"scopes", record.Scopes, "expires_at", record.ExpiresAt)
	a.respondSuccess(c, "api key issued", gin.H{
		"key_id":       record.KeyID,
		"name":         record.Name,
		"token":        token, // 明文仅在签发当次下发一次，服务端只保存 SHA-256 摘要
		"token_prefix": record.TokenPrefix,
		"scopes":       record.Scopes,
		"created_at":   record.CreatedAt,
		"expires_at":   record.ExpiresAt,
	})
}

// handleListKeys 查询用户拥有的全部 API Key（本人 | user:read | user:admin）。
func (a *userAPI) handleListKeys(c *gin.Context) {
	username := c.Param("username")
	caller := GetIdentity(c)

	if !canViewUserAccount(caller, username) {
		a.respondError(c, http.StatusForbidden, "FORBIDDEN", "permission denied: cannot list keys", nil)
		a.audit(c, "list_keys", username, "denied", "insufficient_scope")
		return
	}

	keys, err := a.store.ListAPIKeys(username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			a.respondError(c, http.StatusNotFound, "USER_NOT_FOUND", "user not found", nil)
			return
		}
		a.internalError(c, "list_keys", NormalizeUsername(username), "failed to list api keys", err)
		return
	}
	a.respondSuccess(c, "api keys listed", keys)
}

// handleRevokeKey 吊销指定 API Key（本人 | user:admin）。
func (a *userAPI) handleRevokeKey(c *gin.Context) {
	username := c.Param("username")
	keyID := c.Param("key_id")
	caller := GetIdentity(c)

	if !canManageUserAccount(caller, username) {
		a.respondError(c, http.StatusForbidden, "FORBIDDEN", "permission denied: cannot revoke key for this user", nil)
		a.audit(c, "revoke_key", username, "denied", "insufficient_scope", "key_id", keyID)
		return
	}

	if err := a.store.RevokeAPIKey(username, keyID); err != nil {
		a.respondMutationError(c, "revoke_key", username, err)
		return
	}

	a.audit(c, "revoke_key", NormalizeUsername(username), "success", "", "key_id", keyID)
	a.respondSuccess(c, "api key revoked", gin.H{"revoked": keyID})
}

// handleChangePassword 修改用户口令（本人 | user:admin）。
// 成功后该用户全部登录会话被强制下线（口令可能已泄露），API Key 保留由管理员显式吊销。
func (a *userAPI) handleChangePassword(c *gin.Context) {
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		a.respondError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid payload", err.Error())
		return
	}

	caller := GetIdentity(c)
	username := NormalizeUsername(req.Username)
	if !canManageUserAccount(caller, username) {
		a.respondError(c, http.StatusForbidden, "FORBIDDEN", "permission denied: cannot change password for this user", nil)
		a.audit(c, "change_password", username, "denied", "insufficient_scope")
		return
	}

	if err := a.store.ChangePassword(username, req.OldPassword, req.NewPassword); err != nil {
		switch {
		case errors.Is(err, ErrUserNotFound):
			a.respondError(c, http.StatusNotFound, "USER_NOT_FOUND", "user not found", nil)
		case errors.Is(err, ErrInvalidPassword):
			a.respondError(c, http.StatusUnauthorized, "INVALID_PASSWORD", "old password does not match", nil)
			a.audit(c, "change_password", username, "denied", "invalid_old_password")
		case errors.Is(err, ErrPasswordSame), errors.Is(err, ErrPasswordTooShort),
			errors.Is(err, ErrPasswordTooLong), errors.Is(err, ErrPasswordWeak),
			errors.Is(err, ErrPasswordContainsName), errors.Is(err, ErrPasswordBlacklisted),
			errors.Is(err, ErrPasswordReused):
			a.respondError(c, http.StatusBadRequest, "INVALID_PASSWORD", err.Error(), nil)
		case errors.Is(err, ErrPasswordChangedConcurrently):
			// 锁外 bcrypt 校验窗口内口令已被并发修改：本次校验结论失效，要求重试（避免丢失更新）。
			a.respondError(c, http.StatusConflict, "PASSWORD_CHANGED_CONCURRENTLY", err.Error(), nil)
			a.audit(c, "change_password", username, "denied", "concurrent_modification")
		default:
			a.internalError(c, "change_password", username, "failed to change password", err)
		}
		return
	}

	a.audit(c, "change_password", username, "success", "", "sessions_revoked", true)
	a.respondSuccess(c, "password changed; all sessions revoked", gin.H{
		"message":          "password changed successfully",
		"sessions_revoked": true,
	})
}

// respondMutationError 统一转换写操作错误码。
func (a *userAPI) respondMutationError(c *gin.Context, event, username string, err error) {
	switch {
	case errors.Is(err, ErrUserNotFound):
		a.respondError(c, http.StatusNotFound, "USER_NOT_FOUND", "user not found", nil)
	case errors.Is(err, ErrKeyNotFound):
		a.respondError(c, http.StatusNotFound, "KEY_NOT_FOUND", "api key not found", nil)
	case errors.Is(err, ErrUserDisabled):
		a.respondError(c, http.StatusForbidden, "ACCOUNT_DISABLED", err.Error(), nil)
	case errors.Is(err, ErrForbiddenScope):
		a.respondError(c, http.StatusForbidden, "FORBIDDEN_SCOPE", err.Error(), nil)
		a.audit(c, event, username, "denied", "forbidden_scope")
	case errors.Is(err, ErrLastAdmin):
		a.respondError(c, http.StatusConflict, "LAST_ADMIN", err.Error(), nil)
		a.audit(c, event, username, "denied", "last_admin_protection")
	case errors.Is(err, ErrTooManyKeys):
		a.respondError(c, http.StatusConflict, "KEY_QUOTA_EXCEEDED", err.Error(), gin.H{"max_keys_per_user": MaxAPIKeysPerUser})
	case errors.Is(err, ErrInvalidTTL):
		a.respondError(c, http.StatusBadRequest, "INVALID_TTL", err.Error(),
			gin.H{"default_ttl_seconds": int64(DefaultAPIKeyTTL / time.Second), "max_ttl_seconds": int64(MaxAPIKeyTTL / time.Second)})
	case errors.Is(err, ErrInvalidRole):
		a.respondError(c, http.StatusBadRequest, "INVALID_ROLE", err.Error(), gin.H{"known_roles": KnownRoles})
	case errors.Is(err, ErrInvalidStatus):
		a.respondError(c, http.StatusBadRequest, "INVALID_STATUS", err.Error(), nil)
	case errors.Is(err, ErrInvalidKeyName), errors.Is(err, ErrInvalidScope), errors.Is(err, ErrTooManyScopes):
		a.respondError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
	default:
		a.internalError(c, event, NormalizeUsername(username), "request could not be completed", err)
	}
}
