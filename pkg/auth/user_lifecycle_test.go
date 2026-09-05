package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// newTestStore 构造一个纯内存 UserStore（不落盘），供各生命周期用例复用。
func newTestStore(t *testing.T) *UserStore {
	t.Helper()
	store, err := NewUserStore("")
	if err != nil {
		t.Fatalf("failed to create UserStore: %v", err)
	}
	return store
}

// TestUserStoreLastAdminProtection 验证「最后一个活跃管理员」自锁防护：
// 降权、冻结、注销均不得使管理面永久无主（等保三级 G-07 可管理性）。
func TestUserStoreLastAdminProtection(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Register("root", "Sup3rSecret#2026!", "Root", "admin", nil); err != nil {
		t.Fatalf("register root failed: %v", err)
	}

	// 唯一管理员：降权 / 冻结 / 注销全部被拒
	if err := store.UpdatePermissions("root", "developer", nil); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin on demote, got %v", err)
	}
	if err := store.SetStatus("root", UserStatusDisabled); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin on disable, got %v", err)
	}
	if err := store.DeleteUser("root"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin on delete, got %v", err)
	}
	if err := store.SetStatus("root", "frozen"); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus, got %v", err)
	}

	// 第二个管理员就位后，原管理员可被降权/注销
	if _, err := store.Register("root2", "SecondAdmin#2026!", "Root2", "admin", nil); err != nil {
		t.Fatalf("register root2 failed: %v", err)
	}
	if err := store.UpdatePermissions("root", "auditor", nil); err != nil {
		t.Fatalf("demote with second admin present failed: %v", err)
	}
	if err := store.DeleteUser("root"); err != nil {
		t.Fatalf("delete demoted user failed: %v", err)
	}
	// root2 成为唯一管理员，再次触发保护
	if err := store.DeleteUser("root2"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin for remaining admin, got %v", err)
	}
}

// TestUserStorePasswordHistoryAndSessionRevocation 验证口令历史禁重用与改密后会话强制下线。
func TestUserStorePasswordHistoryAndSessionRevocation(t *testing.T) {
	store := newTestStore(t)
	const oldPass, newPass = "Initial#Pass2026", "Rotated#Pass2026"
	if _, err := store.Register("dave", oldPass, "Dave", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	token, expiresAt, err := store.CreateSession("dave", time.Hour)
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	if expiresAt.Sub(time.Now()) > time.Hour+time.Second {
		t.Fatalf("session expiry %v exceeds requested ttl", expiresAt)
	}
	if _, ok := store.LiveHashedKeys()[HashToken(token)]; !ok {
		t.Fatal("session token must be present in live hashed keys")
	}

	// 改密：旧口令校验 + 新口令生效
	if err := store.ChangePassword("dave", oldPass, newPass); err != nil {
		t.Fatalf("change password failed: %v", err)
	}
	if _, err := store.Authenticate("dave", oldPass); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("expected ErrInvalidPassword for old password, got %v", err)
	}
	// 改密后旧会话必须立即失效（防止已泄露会话继续可用）
	if _, ok := store.LiveHashedKeys()[HashToken(token)]; ok {
		t.Fatal("sessions must be revoked after password change")
	}
	if err := store.RevokeSession(token); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound after revocation, got %v", err)
	}

	// 口令历史：不得回退到最近使用过的口令
	if err := store.ChangePassword("dave", newPass, oldPass); !errors.Is(err, ErrPasswordReused) {
		t.Fatalf("expected ErrPasswordReused, got %v", err)
	}
	if err := store.ChangePassword("dave", newPass, newPass); !errors.Is(err, ErrPasswordSame) {
		t.Fatalf("expected ErrPasswordSame, got %v", err)
	}
	if err := store.ChangePassword("dave", "Wrong#Pass2026", "Another#Pass2026"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("expected ErrInvalidPassword, got %v", err)
	}

	u, err := store.GetUser("dave")
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	if u.PasswordUpdatedAt.IsZero() {
		t.Fatal("PasswordUpdatedAt must be recorded for compliance inspection")
	}
}

// TestUserStorePasswordLengthUpperBound 验证 bcrypt 72 字节输入上限被显式拒绝，
// 防止「前 72 字节相同的不同口令等价」这一静默截断漏洞。
func TestUserStorePasswordLengthUpperBound(t *testing.T) {
	store := newTestStore(t)
	long := strings.Repeat("Aa1#", 20) // 80 字节
	if len(long) <= MaxPasswordLength {
		t.Fatalf("test fixture must exceed %d bytes", MaxPasswordLength)
	}
	if _, err := store.Register("erin", long, "Erin", "developer", nil); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("expected ErrPasswordTooLong, got %v", err)
	}
}

// TestUserStoreSessionTTLCapAndQuota 验证会话有效期上限与并发会话配额（超出淘汰最早会话）。
func TestUserStoreSessionTTLCapAndQuota(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Register("frank", "Session#Pass2026", "Frank", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	// 请求 30 天会话必须被收敛到 MaxSessionTTL
	_, expiresAt, err := store.CreateSession("frank", 30*24*time.Hour)
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	if remaining := time.Until(expiresAt); remaining > MaxSessionTTL+time.Minute {
		t.Fatalf("session ttl %v exceeds cap %v", remaining, MaxSessionTTL)
	}

	// 并发会话配额：第 MaxSessionsPerUser+1 个会话触发最早会话淘汰
	tokens := make([]string, 0, MaxSessionsPerUser+1)
	for i := 0; i <= MaxSessionsPerUser; i++ {
		tok, _, err := store.CreateSession("frank", time.Hour)
		if err != nil {
			t.Fatalf("create session #%d failed: %v", i, err)
		}
		tokens = append(tokens, tok)
	}
	if _, keys, sessions := store.Stats(); sessions > MaxSessionsPerUser {
		t.Fatalf("expected at most %d sessions, got %d (keys=%d)", MaxSessionsPerUser, sessions, keys)
	}
	if _, ok := store.LiveHashedKeys()[HashToken(tokens[0])]; ok {
		t.Fatal("oldest session should have been evicted")
	}
	if n := store.RevokeUserSessions("frank"); n == 0 {
		t.Fatal("RevokeUserSessions should report revoked session count")
	}
	if _, keys, sessions := store.Stats(); sessions != 0 {
		t.Fatalf("expected all sessions revoked, got sessions=%d keys=%d", sessions, keys)
	}
}

// TestUserStoreAPIKeyTTLAndQuota 验证密钥有效期归一化（0→30 天、超限拒绝）与单用户密钥配额。
func TestUserStoreAPIKeyTTLAndQuota(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Register("grace", "KeyQuota#Pass2026", "Grace", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	if _, _, err := store.IssueAPIKey("grace", "k", nil, -time.Second); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("expected ErrInvalidTTL for negative ttl, got %v", err)
	}
	if _, _, err := store.IssueAPIKey("grace", "k", nil, MaxAPIKeyTTL+time.Hour); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("expected ErrInvalidTTL for ttl over cap, got %v", err)
	}
	if _, _, err := store.IssueAPIKey("grace", "bad name!", nil, time.Hour); !errors.Is(err, ErrInvalidKeyName) {
		t.Fatalf("expected ErrInvalidKeyName, got %v", err)
	}

	// ttl=0 必须归一化为默认有效期，而不是「永不过期」（等保三级 G-14）
	rec, _, err := store.IssueAPIKey("grace", "default-ttl", nil, 0)
	if err != nil {
		t.Fatalf("issue key with ttl=0 failed: %v", err)
	}
	if rec.ExpiresAt == nil {
		t.Fatal("api key must always carry an expiry (no immortal credentials)")
	}
	if d := time.Until(*rec.ExpiresAt); d > DefaultAPIKeyTTL+time.Minute || d < DefaultAPIKeyTTL-time.Hour {
		t.Fatalf("unexpected default expiry in %v", d)
	}

	for i := 1; i < MaxAPIKeysPerUser; i++ {
		if _, _, err := store.IssueAPIKey("grace", "", nil, time.Hour); err != nil {
			t.Fatalf("issue key #%d failed: %v", i, err)
		}
	}
	if _, _, err := store.IssueAPIKey("grace", "", nil, time.Hour); !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("expected ErrTooManyKeys beyond quota, got %v", err)
	}

	keys, err := store.ListAPIKeys("grace")
	if err != nil {
		t.Fatalf("list keys failed: %v", err)
	}
	if len(keys) != MaxAPIKeysPerUser {
		t.Fatalf("expected %d keys, got %d", MaxAPIKeysPerUser, len(keys))
	}
	for _, k := range keys {
		// 明文 Token 永不出现在查询响应；仅保留摘要与脱敏前缀
		if k.TokenHash == "" || k.LegacyToken != "" || !strings.HasSuffix(k.TokenPrefix, "***") {
			t.Fatalf("key listing leaked or lost credential material: %+v", k)
		}
	}
}

// TestUserStoreDisabledUserCannotIssueKeys 验证冻结账号无法签发新密钥、无法登录。
func TestUserStoreDisabledUserCannotIssueKeys(t *testing.T) {
	store := newTestStore(t)
	const pass = "Freeze#Pass2026"
	if _, err := store.Register("heidi", pass, "Heidi", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if err := store.SetStatus("heidi", UserStatusDisabled); err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	if _, _, err := store.IssueAPIKey("heidi", "k", nil, time.Hour); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("expected ErrUserDisabled, got %v", err)
	}
	if _, err := store.Authenticate("heidi", pass); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("expected ErrUserDisabled on login, got %v", err)
	}
	if _, _, err := store.CreateSession("heidi", time.Hour); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("expected ErrUserDisabled on session creation, got %v", err)
	}
}

// TestUserStoreScopeSubsetEnforcement 验证越权签发拦截：申请的 scope 必须是自身权限子集。
func TestUserStoreScopeSubsetEnforcement(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Register("ivan", "Subset#Pass2026", "Ivan", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if _, _, err := store.IssueAPIKey("ivan", "ok", []string{"privacy:mask"}, time.Hour); err != nil {
		t.Fatalf("expected subset scope to be issued, got %v", err)
	}
	if _, _, err := store.IssueAPIKey("ivan", "bad", []string{"user:admin"}, time.Hour); !errors.Is(err, ErrForbiddenScope) {
		t.Fatalf("expected ErrForbiddenScope, got %v", err)
	}
	// 通配符持有者（admin）可为自己签发任意 scope
	if _, err := store.Register("judy", "Wildcard#Pass2026", "Judy", "admin", nil); err != nil {
		t.Fatalf("register admin failed: %v", err)
	}
	if _, _, err := store.IssueAPIKey("judy", "ops", []string{"ops:admin"}, time.Hour); err != nil {
		t.Fatalf("admin wildcard key issue failed: %v", err)
	}
	// 非特权角色即使由管理员显式指定也不得持有管理类 scope
	if _, err := store.Register("ken", "NoManage#Pass2026", "Ken", "developer", []string{"user:admin"}); !errors.Is(err, ErrForbiddenScope) {
		t.Fatalf("expected ErrForbiddenScope on register, got %v", err)
	}
	if _, err := store.Register("leo", "BadRole#Pass2026", "Leo", "superuser", nil); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole, got %v", err)
	}
}

// newAuthTestEngine 构造带认证中间件与用户管理路由的测试引擎。
func newAuthTestEngine(t *testing.T, store *UserStore, opts ...UserRouteOption) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware(&Settings{
		AuthEnabled:            true,
		HealthNoAuth:           true,
		LiveInternalHashedKeys: store.LiveHashedKeysFunc(),
	}))
	RegisterUserRoutes(r, store, opts...)
	return r
}

func doJSON(t *testing.T, r *gin.Engine, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body failed: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// envelopeShape 断言响应符合 pkg/envelope 的标准信封字段口径。
// 成功：code/message/data/trace_id/timestamp；错误：code/message/trace_id/timestamp（detail 可选）。
func envelopeShape(t *testing.T, body string, success bool) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("response is not valid JSON: %v (%s)", err, body)
	}
	fields := []string{"code", "message", "trace_id", "timestamp"}
	if success {
		fields = append(fields, "data")
	}
	for _, field := range fields {
		if _, ok := env[field]; !ok {
			t.Fatalf("response envelope missing field %q: %s", field, body)
		}
	}
	return env
}

func dataOf(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("envelope data is not an object: %v", env["data"])
	}
	return data
}

// TestUserRoutesEnvelopeAndSelfService 验证成功信封 5 字段、本人自助读写边界，
// 以及 user:read **不得**携带写能力（越权提权回归防护）。
func TestUserRoutesEnvelopeAndSelfService(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Register("admin1", "AdminOne#2026!", "Admin One", "admin", nil); err != nil {
		t.Fatalf("register admin failed: %v", err)
	}
	if _, err := store.Register("dev1", "DevOne#2026!x", "Dev One", "developer", nil); err != nil {
		t.Fatalf("register dev failed: %v", err)
	}
	r := newAuthTestEngine(t, store)

	adminToken, _, err := store.CreateSession("admin1", time.Hour)
	if err != nil {
		t.Fatalf("admin session failed: %v", err)
	}
	devToken, _, err := store.CreateSession("dev1", time.Hour)
	if err != nil {
		t.Fatalf("dev session failed: %v", err)
	}

	// 本人读取自己 → 200 且为标准信封，且不得泄露口令哈希
	w := doJSON(t, r, http.MethodGet, "/v1/auth/users/dev1", devToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("self profile read failed: %d %s", w.Code, w.Body.String())
	}
	env := envelopeShape(t, w.Body.String(), true)
	if strings.Contains(w.Body.String(), "password_hash") || strings.Contains(w.Body.String(), "$2a$") {
		t.Fatalf("response leaked password material: %s", w.Body.String())
	}
	if dataOf(t, env)["username"] != "dev1" {
		t.Fatalf("unexpected payload: %v", env["data"])
	}

	// 本人登出 → 200；同一 Token 再次使用 → 401
	w = doJSON(t, r, http.MethodPost, "/v1/auth/logout", devToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("logout failed: %d %s", w.Code, w.Body.String())
	}
	envelopeShape(t, w.Body.String(), true)
	w = doJSON(t, r, http.MethodGet, "/v1/auth/users/dev1", devToken, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d %s", w.Code, w.Body.String())
	}

	// developer（不含 user:read）枚举全量用户清单 → 403
	devToken2, _, err := store.CreateSession("dev1", time.Hour)
	if err != nil {
		t.Fatalf("dev session failed: %v", err)
	}
	w = doJSON(t, r, http.MethodGet, "/v1/auth/users", devToken2, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-privileged user listing, got %d %s", w.Code, w.Body.String())
	}
	// 管理员可枚举
	w = doJSON(t, r, http.MethodGet, "/v1/auth/users", adminToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin listing failed: %d %s", w.Code, w.Body.String())
	}

	// developer 试图为他人签发密钥 → 403（本人自助边界）
	w = doJSON(t, r, http.MethodPost, "/v1/auth/users/admin1/keys", devToken2, gin.H{"key_name": "hack"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when issuing key for others, got %d %s", w.Code, w.Body.String())
	}
	// developer 试图冻结管理员 → 403
	w = doJSON(t, r, http.MethodPut, "/v1/auth/users/admin1/status", devToken2, gin.H{"status": "disabled"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when disabling others, got %d %s", w.Code, w.Body.String())
	}
}

// TestUserRoutesReadOnlyScopeCannotManage 验证仅持 user:read 的审计类账号只能读、不能写。
func TestUserRoutesReadOnlyScopeCannotManage(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Register("admin2", "AdminTwo#2026!", "Admin Two", "admin", nil); err != nil {
		t.Fatalf("register admin failed: %v", err)
	}
	if _, err := store.Register("auditor2", "AuditorTwo#2026!", "Auditor", "auditor", nil); err != nil {
		t.Fatalf("register auditor failed: %v", err)
	}
	r := newAuthTestEngine(t, store)

	auditorToken, _, err := store.CreateSession("auditor2", time.Hour)
	if err != nil {
		t.Fatalf("auditor session failed: %v", err)
	}

	// 读他人资料：user:read 允许
	w := doJSON(t, r, http.MethodGet, "/v1/auth/users/admin2", auditorToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("auditor read failed: %d %s", w.Code, w.Body.String())
	}
	// 写他人密钥：user:read 不得授予写能力
	w = doJSON(t, r, http.MethodPost, "/v1/auth/users/admin2/keys", auditorToken, gin.H{"key_name": "escalate"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for read-only scope issuing keys, got %d %s", w.Code, w.Body.String())
	}
	// 改他人权限：403
	w = doJSON(t, r, http.MethodPut, "/v1/auth/users/admin2/permissions", auditorToken, gin.H{"role": "guest"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for read-only scope updating permissions, got %d %s", w.Code, w.Body.String())
	}
}

// TestUserRoutesSelfRegistrationPolicy 验证自注册开关：
// 关闭时匿名开户被拒；开启时强制降权为 developer 且禁止特权角色与自定义 scope。
func TestUserRoutesSelfRegistrationPolicy(t *testing.T) {
	store := newTestStore(t)
	// 引导期：首个账号必须是 admin
	w := doJSON(t, newAuthTestEngine(t, store), http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "firstdev", "password": "F1rstAdmin#2026!", "role": "developer",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-admin bootstrap role, got %d %s", w.Code, w.Body.String())
	}

	r := newAuthTestEngine(t, store, WithSelfRegistration(true))
	w = doJSON(t, r, http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "bootstrap", "password": "F1rstAdmin#2026!",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap admin registration failed: %d %s", w.Code, w.Body.String())
	}

	// 开启自注册：匿名 developer 开户成功
	w = doJSON(t, r, http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "selfdev", "password": "Sel4Reg#2026!x", "display_name": "Self Dev",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("self registration failed: %d %s", w.Code, w.Body.String())
	}
	u, err := store.GetUser("selfdev")
	if err != nil || u.Role != "developer" {
		t.Fatalf("expected self-registered role=developer, got %v (err=%v)", u, err)
	}
	// 请求特权角色被拒
	w = doJSON(t, r, http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "selfauditor", "password": "Pr1vileged#2026!", "role": "auditor",
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for privileged self-registration, got %d %s", w.Code, w.Body.String())
	}
	// 自带自定义 scope 被拒
	w = doJSON(t, r, http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "selfscope", "password": "Cust0mScope#2026!", "scopes": []string{"user:admin"},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for custom scopes on self-registration, got %d %s", w.Code, w.Body.String())
	}
	// 弱口令被拒且返回 400
	w = doJSON(t, r, http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "weakling", "password": "12345678",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for weak password, got %d %s", w.Code, w.Body.String())
	}
	envelopeShape(t, w.Body.String(), false)
}

// TestUserRoutesLoginThrottleAndLockout 验证登录限速（429 + Retry-After）与防爆破锁定联动。
func TestUserRoutesLoginThrottleAndLockout(t *testing.T) {
	store := newTestStore(t)
	const pass = "Throttle#Pass2026"
	if _, err := store.Register("mallory", pass, "Mallory", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	r := newAuthTestEngine(t, store, WithLoginThrottle(3))

	// 前 3 次错误口令：401（抑制账号枚举，不区分用户不存在与口令错误）
	for i := 0; i < 3; i++ {
		w := doJSON(t, r, http.MethodPost, "/v1/auth/login", "", gin.H{"username": "mallory", "password": "Wrong#Pass1"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt #%d expected 401, got %d %s", i+1, w.Code, w.Body.String())
		}
	}
	// 第 4 次：触发每 IP 限速 → 429 且带 Retry-After
	w := doJSON(t, r, http.MethodPost, "/v1/auth/login", "", gin.H{"username": "mallory", "password": pass})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 when login throttled, got %d %s", w.Code, w.Body.String())
	}
	if retry := w.Header().Get("Retry-After"); retry == "" {
		t.Fatal("429 response must carry Retry-After header")
	}
	envelopeShape(t, w.Body.String(), false)
}

// TestUserRoutesAccountLockoutReturnsRetryAfter 验证连续失败触发锁定后返回 429 ACCOUNT_LOCKED。
func TestUserRoutesAccountLockoutReturnsRetryAfter(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Register("oscar", "Lockout#Pass2026", "Oscar", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	r := newAuthTestEngine(t, store)

	for i := 0; i < MaxFailedLoginAttempts; i++ {
		w := doJSON(t, r, http.MethodPost, "/v1/auth/login", "", gin.H{"username": "oscar", "password": "Wrong#Pass1"})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt #%d expected 401, got %d", i+1, w.Code)
		}
	}
	w := doJSON(t, r, http.MethodPost, "/v1/auth/login", "", gin.H{"username": "oscar", "password": "Lockout#Pass2026"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 ACCOUNT_LOCKED, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "ACCOUNT_LOCKED") {
		t.Fatalf("expected ACCOUNT_LOCKED code, got %s", w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("locked response must carry Retry-After header")
	}
	if remaining := store.LockoutRemaining("oscar"); remaining <= 0 || remaining > AccountLockoutDuration {
		t.Fatalf("unexpected lockout remaining %v", remaining)
	}
}

// TestUserRoutesAdminProvisioningAndKeyLifecycle 验证管理员开户 → 签发密钥 → 冻结 → 吊销的完整闭环。
func TestUserRoutesAdminProvisioningAndKeyLifecycle(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Register("admin3", "AdminThree#2026!", "Admin Three", "admin", nil); err != nil {
		t.Fatalf("register admin failed: %v", err)
	}
	r := newAuthTestEngine(t, store)
	adminToken, _, err := store.CreateSession("admin3", time.Hour)
	if err != nil {
		t.Fatalf("admin session failed: %v", err)
	}

	// 管理员开户 developer
	w := doJSON(t, r, http.MethodPost, "/v1/auth/users/register", adminToken, gin.H{
		"username": "worker1", "password": "WorkerOne#2026!", "role": "developer",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("admin provisioning failed: %d %s", w.Code, w.Body.String())
	}

	// 签发密钥（明文仅本次响应下发一次）
	w = doJSON(t, r, http.MethodPost, "/v1/auth/users/worker1/keys", adminToken, gin.H{
		"key_name": "runner", "scopes": []string{"privacy:mask"}, "ttl_seconds": 3600,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("issue key failed: %d %s", w.Code, w.Body.String())
	}
	env := envelopeShape(t, w.Body.String(), true)
	data := dataOf(t, env)
	keyToken, _ := data["token"].(string)
	keyID, _ := data["key_id"].(string)
	if keyToken == "" || keyID == "" {
		t.Fatalf("issue key response missing token/key_id: %v", data)
	}

	// 密钥清单不得回显明文
	w = doJSON(t, r, http.MethodGet, "/v1/auth/users/worker1/keys", adminToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list keys failed: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), keyToken) {
		t.Fatalf("key listing must not echo plaintext token: %s", w.Body.String())
	}

	// 冻结账号后，其密钥立即失效（401）
	w = doJSON(t, r, http.MethodPut, "/v1/auth/users/worker1/status", adminToken, gin.H{
		"status": "disabled", "reason": "suspicious activity",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("disable failed: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(t, r, http.MethodGet, "/v1/auth/users/worker1", keyToken, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for disabled user's key, got %d %s", w.Code, w.Body.String())
	}

	// 解冻后密钥自动恢复
	w = doJSON(t, r, http.MethodPut, "/v1/auth/users/worker1/status", adminToken, gin.H{"status": "active"})
	if w.Code != http.StatusOK {
		t.Fatalf("enable failed: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(t, r, http.MethodGet, "/v1/auth/users/worker1", keyToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected key restored after re-enable, got %d %s", w.Code, w.Body.String())
	}

	// 权限调整毫秒级联动到已签发密钥
	w = doJSON(t, r, http.MethodPut, "/v1/auth/users/worker1/permissions", adminToken, gin.H{
		"role": "data-engineer",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update permissions failed: %d %s", w.Code, w.Body.String())
	}
	cfg, ok := store.LiveHashedKeys()[HashToken(keyToken)]
	if !ok {
		t.Fatal("key disappeared from live hashed keys after permission update")
	}
	found := false
	for _, sc := range cfg.Scopes {
		if sc == "privacy:dp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected data-engineer scopes propagated to live key, got %v", cfg.Scopes)
	}

	// 吊销密钥后立即 401
	w = doJSON(t, r, http.MethodDelete, "/v1/auth/users/worker1/keys/"+keyID, adminToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke key failed: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(t, r, http.MethodGet, "/v1/auth/users/worker1", keyToken, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after revocation, got %d %s", w.Code, w.Body.String())
	}

	// 注销账号
	w = doJSON(t, r, http.MethodDelete, "/v1/auth/users/worker1", adminToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete user failed: %d %s", w.Code, w.Body.String())
	}
	if store.Exists("worker1") {
		t.Fatal("user should be removed")
	}
}

// TestUserRouteOptionsFromEnv 验证两端共用的策略变量口径（变量名表与默认值回退）。
func TestUserRouteOptionsFromEnv(t *testing.T) {
	// 未登记前缀：按 <prefix>_USER_* 约定派生
	if got := userPolicyEnvFor("CUSTOM_SVC"); got.selfRegister != "CUSTOM_SVC_USER_SELF_REGISTER" {
		t.Fatalf("unexpected derived env name %q", got.selfRegister)
	}
	// 已登记前缀：必须与编排清单/安全白皮书中的完整变量名一致
	if got := userPolicyEnvFor("AGENT_AUTH"); got.loginThrottle != "AGENT_AUTH_USER_LOGIN_THROTTLE_PER_MIN" {
		t.Fatalf("unexpected engine env name %q", got.loginThrottle)
	}
	if got := userPolicyEnvFor("SERVICE_HUB"); got.sessionTTL != "SERVICE_HUB_USER_SESSION_TTL" {
		t.Fatalf("unexpected hub env name %q", got.sessionTTL)
	}

	if opts := UserRouteOptionsFromEnv("  "); opts != nil {
		t.Fatalf("blank prefix must yield no options, got %d", len(opts))
	}
	if opts := UserRouteOptionsFromEnv("AGENT_AUTH"); len(opts) != 0 {
		t.Fatalf("unset env must keep defaults, got %d options", len(opts))
	}

	t.Setenv("AGENT_AUTH_USER_SELF_REGISTER", "true")
	t.Setenv("AGENT_AUTH_USER_SESSION_TTL", "15m")
	t.Setenv("AGENT_AUTH_USER_LOGIN_THROTTLE_PER_MIN", "7")
	if opts := UserRouteOptionsFromEnv("AGENT_AUTH"); len(opts) != 3 {
		t.Fatalf("expected 3 options from env, got %d", len(opts))
	}

	// 非法值必须静默保留默认（不 panic、不静默放宽）
	t.Setenv("AGENT_AUTH_USER_SELF_REGISTER", "not-a-bool")
	t.Setenv("AGENT_AUTH_USER_SESSION_TTL", "forever")
	t.Setenv("AGENT_AUTH_USER_LOGIN_THROTTLE_PER_MIN", "many")
	if opts := UserRouteOptionsFromEnv("AGENT_AUTH"); len(opts) != 0 {
		t.Fatalf("invalid env values must be ignored, got %d options", len(opts))
	}
}

// TestAuthPublicPathMappings 验证用户管理面路径映射不会 fail-closed 落入 admin 兜底。
func TestAuthPublicPathMappings(t *testing.T) {
	for _, p := range []string{"/v1/auth/login", "/v1/auth/users/register"} {
		if !IsAuthPublicPath(p) {
			t.Fatalf("%s must be treated as public auth path", p)
		}
		if got := PermissionForRESTPath(p); got != "auth:public" {
			t.Fatalf("PermissionForRESTPath(%s) = %q, want auth:public", p, got)
		}
		if got := ServiceHubPermissionForPath(p); got != "" {
			t.Fatalf("ServiceHubPermissionForPath(%s) = %q, want empty", p, got)
		}
	}
	// 需认证但路由层不强制 scope（授权在 Handler 内按主体判定）；不得落入 "admin" 兜底
	for _, p := range []string{
		"/v1/auth/logout", "/v1/auth/change-password", "/v1/auth/users",
		"/v1/auth/users/dev1", "/v1/auth/users/dev1/keys", "/v1/auth/users/dev1/keys/k_1",
		"/v1/auth/users/dev1/permissions", "/v1/auth/users/dev1/status",
	} {
		if IsAuthPublicPath(p) {
			t.Fatalf("%s must not be public", p)
		}
		if got := PermissionForRESTPath(p); got != "" {
			t.Fatalf("PermissionForRESTPath(%s) = %q, want empty (handler-level ABAC)", p, got)
		}
		if got := ServiceHubPermissionForPath(p); got != "" {
			t.Fatalf("ServiceHubPermissionForPath(%s) = %q, want empty (handler-level ABAC)", p, got)
		}
	}
}

// TestLiveKeyAggregatorCachingAndIsolation 验证聚合器静态密钥合并与活密钥快照的
// copy-on-write 隔离（调用方修改快照不得回写存储内部状态，防数据竞争）。
func TestLiveKeyAggregatorCachingAndIsolation(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Register("pat", "Aggregator#2026!", "Pat", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	agg := NewAggregator(map[string]*KeyConfig{
		"static-token": {Name: "static", Scopes: []string{"health:read"}},
	})
	snap := agg.Keys()
	if len(snap) != 1 || snap["static-token"] == nil {
		t.Fatalf("static key missing from aggregate: %v", snap)
	}
	if agg.Size() != 1 {
		t.Fatalf("unexpected aggregate size %d", agg.Size())
	}
	// 无源聚合器的快照必须稳定复用（版本驱动缓存的退化分支）
	if len(agg.Keys()) != len(snap) {
		t.Fatal("aggregate snapshot unstable across calls")
	}

	// UserStore 活密钥快照：版本未变时复用同一份缓存；变更后必须反映最新状态
	snap1 := store.LiveHashedKeys()
	snap2 := store.LiveHashedKeys()
	if len(snap1) != len(snap2) {
		t.Fatal("snapshot size changed without version bump")
	}
	_, tok, err := store.IssueAPIKey("pat", "agg", nil, time.Hour)
	if err != nil {
		t.Fatalf("issue key failed: %v", err)
	}
	snap3 := store.LiveHashedKeys()
	if _, ok := snap3[HashToken(tok)]; !ok {
		t.Fatal("newly issued key missing from snapshot")
	}
	if _, ok := snap1[HashToken(tok)]; ok {
		t.Fatal("older snapshot must not be mutated in place (copy-on-write violated)")
	}

	// 快照为只读共享对象：即使调用方违规写入，也绝不能污染存储内部状态（深拷贝隔离）。
	for _, cfg := range snap3 {
		cfg.Scopes = append(cfg.Scopes, "injected")
	}
	if _, _, err := store.IssueAPIKey("pat", "agg2", nil, time.Hour); err != nil {
		t.Fatalf("issue second key failed: %v", err)
	}
	for _, cfg := range store.LiveHashedKeys() {
		for _, sc := range cfg.Scopes {
			if sc == "injected" {
				t.Fatal("caller mutation leaked into store state (deep-copy isolation violated)")
			}
		}
	}
}
