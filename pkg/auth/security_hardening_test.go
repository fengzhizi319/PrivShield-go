package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// 本文件是「用户体系安全加固」的回归防线，逐条锁定审计发现的问题不再复发：
//
//	F-1  引导期 TOCTOU 与匿名自封管理员
//	F-2  冻结账号状态在口令校验前被泄露（账号枚举侧信道）
//	F-3  500 响应回显内部错误细节（文件路径 / OS 错误码）
//	F-4  公开注册端点无限速（未认证 bcrypt CPU 耗尽放大器）
//	F-5  改权路径缺失角色/scope 一致性校验（可造出「guest 持 *」）
//	F-6  改密丢失更新（锁外校验 + 锁内写入）
//	F-7  凭证库文件缺体积上限 / 权限位收敛 / 过期密钥清理
//	F-10 口令有效期缺失（等保三级 G-04 定期更换）

// TestUserStoreBootstrapAdminIsAtomic 验证引导窗口的原子性：并发抢占只能产出一个管理员，
// 竞争失败者得到 ErrBootstrapClosed，且窗口一旦关闭便不再接受新的引导注册（F-1）。
func TestUserStoreBootstrapAdminIsAtomic(t *testing.T) {
	store := newTestStore(t)

	const contenders = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, err := store.RegisterBootstrapAdmin(fmt.Sprintf("booter%d", idx), "Bootstrap#Pass2026", "")
			errs[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrBootstrapClosed):
			// 预期：竞争失败者必须被明确拒绝
		default:
			t.Fatalf("unexpected bootstrap error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected exactly one bootstrap administrator, got %d (TOCTOU regression)", succeeded)
	}
	if store.Count() != 1 {
		t.Fatalf("expected exactly one persisted account, got %d", store.Count())
	}
	for _, summary := range store.ListUsers() {
		if summary.Role != "admin" {
			t.Fatalf("bootstrap account must be admin, got %q", summary.Role)
		}
		if len(summary.Scopes) == 0 {
			t.Fatal("bootstrap admin must carry the preset admin scopes")
		}
	}

	// 窗口关闭后再次引导必须被拒（不得退化为普通注册或覆盖既有管理员）
	if _, err := store.RegisterBootstrapAdmin("latecomer", "Bootstrap#Pass2026", ""); !errors.Is(err, ErrBootstrapClosed) {
		t.Fatalf("expected ErrBootstrapClosed after the window closed, got %v", err)
	}
	if store.Count() != 1 {
		t.Fatalf("closed bootstrap window must not create accounts, got %d", store.Count())
	}
}

// TestUserRoutesConcurrentBootstrapYieldsSingleAdmin 验证 HTTP 面并发引导：
// 只允许一个 200，其余落在 403/409，绝不产出第二个自封管理员（F-1）。
func TestUserRoutesConcurrentBootstrapYieldsSingleAdmin(t *testing.T) {
	store := newTestStore(t)
	r := newAuthTestEngine(t, store)

	const contenders = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]int, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			w := doJSON(t, r, http.MethodPost, "/v1/auth/users/register", "", gin.H{
				"username": fmt.Sprintf("raceadmin%d", idx),
				"password": "RaceAdmin#Pass2026",
			})
			codes[idx] = w.Code
		}(i)
	}
	close(start)
	wg.Wait()

	created := 0
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			created++
		case http.StatusConflict, http.StatusForbidden, http.StatusTooManyRequests:
			// 409 BOOTSTRAP_CLOSED（锁内竞争失败）/ 403 SELF_REGISTER_DISABLED（读到非空库）
			// / 429（同 IP 限速）均为合法拒绝路径
		default:
			t.Fatalf("unexpected bootstrap response code %d", code)
		}
	}
	if created != 1 {
		t.Fatalf("expected exactly one bootstrap admin over HTTP, got %d (codes=%v)", created, codes)
	}
	if store.Count() != 1 {
		t.Fatalf("expected exactly one persisted account, got %d", store.Count())
	}
}

// TestUserRoutesBootstrapRejectsCustomScopes 验证引导期不接受调用方自定义 scope：
// 未认证调用者不得借「创建首个管理员」通道给自己组装 "*" 等任意权限（F-1）。
func TestUserRoutesBootstrapRejectsCustomScopes(t *testing.T) {
	store := newTestStore(t)
	r := newAuthTestEngine(t, store, WithSelfRegistration(true))

	w := doJSON(t, r, http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "scopey", "password": "Bootstrap#Pass2026", "scopes": []string{"*"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for custom scopes during bootstrap, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "INVALID_BOOTSTRAP_SCOPES") {
		t.Fatalf("expected INVALID_BOOTSTRAP_SCOPES code, got %s", w.Body.String())
	}
	if store.Count() != 0 {
		t.Fatal("a rejected bootstrap must not create any account")
	}

	// 收敛后正常引导成功，且权限来自 admin 角色预置而非请求体
	w = doJSON(t, r, http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "rootadmin", "password": "Bootstrap#Pass2026",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap registration failed: %d %s", w.Code, w.Body.String())
	}
	if _, err := store.Authenticate("rootadmin", "Bootstrap#Pass2026"); err != nil {
		t.Fatalf("bootstrap admin must be able to log in, got %v", err)
	}
}

// TestUserRoutesAnonymousCallerCannotUseAdminChannel 验证「无身份上下文」的调用者
// 不得走管理员开户通道（F-1）：nil 身份意味着认证中间件未生效或遗留免密透传，
// 此时授予任意角色 + 自定义 scope 等于把最高权限白送给未认证请求。
func TestUserRoutesAnonymousCallerCannotUseAdminChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newTestStore(t)
	if _, err := store.Register("incumbent", "Str0ng#Pass2026!", "Incumbent", "admin", nil); err != nil {
		t.Fatalf("register incumbent admin failed: %v", err)
	}

	// 刻意不挂载 AuthMiddleware：GetIdentity(c) 恒为 nil
	r := gin.New()
	RegisterUserRoutes(r, store)

	w := doJSON(t, r, http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "intrdr", "password": "Intruder#Pass2026", "role": "admin", "scopes": []string{"*"},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("anonymous caller must not reach the admin provisioning channel, got %d %s", w.Code, w.Body.String())
	}
	if store.Exists("intrdr") {
		t.Fatal("anonymous self-promotion must not create an account")
	}

	// 即便处于引导期，匿名调用者也不得自带 scope（只能拿到 admin 角色预置权限）
	empty := newTestStore(t)
	r2 := gin.New()
	RegisterUserRoutes(r2, empty)
	w = doJSON(t, r2, http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "pioneer", "password": "Bootstrap#Pass2026", "role": "admin", "scopes": []string{"user:admin"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for anonymous bootstrap with custom scopes, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "INVALID_BOOTSTRAP_SCOPES") {
		t.Fatalf("expected INVALID_BOOTSTRAP_SCOPES code, got %s", w.Body.String())
	}
	if empty.Count() != 0 {
		t.Fatal("rejected anonymous bootstrap must not create an account")
	}
}

// TestUserStoreDisabledStatusNotLeakedBeforePasswordCheck 验证冻结状态只在口令校验
// 通过后才披露：未掌握口令者无法据响应差异枚举「存在且被冻结」的账号（F-2）。
func TestUserStoreDisabledStatusNotLeakedBeforePasswordCheck(t *testing.T) {
	store := newTestStore(t)
	const pass = "Frozen#Pass2026"
	if _, err := store.Register("frost", pass, "Frost", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if err := store.SetStatus("frost", UserStatusDisabled); err != nil {
		t.Fatalf("disable failed: %v", err)
	}

	// 冻结账号 + 错误口令 与 不存在账号 + 错误口令 必须在 HTTP 面同口径
	if _, err := store.Authenticate("frost", "WrongGuess#Pass2026"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("expected ErrInvalidPassword for disabled account with wrong password, got %v", err)
	}
	// 只有掌握正确口令者才被告知账号已冻结
	if _, err := store.Authenticate("frost", pass); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("expected ErrUserDisabled with the correct password, got %v", err)
	}

	r := newAuthTestEngine(t, store)
	w := doJSON(t, r, http.MethodPost, "/v1/auth/login", "", gin.H{"username": "frost", "password": "WrongGuess#Pass2026"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for disabled account with wrong password, got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "ACCOUNT_DISABLED") || strings.Contains(w.Body.String(), "disabled") {
		t.Fatalf("401 response leaked the account status: %s", w.Body.String())
	}
	// 与「账号不存在」的响应必须逐字段一致（仅 trace_id/timestamp 例外），否则可据差异枚举账号
	ghost := doJSON(t, r, http.MethodPost, "/v1/auth/login", "", gin.H{"username": "nosuchuser", "password": "WrongGuess#Pass2026"})
	if ghost.Code != w.Code {
		t.Fatalf("status code differs between unknown (%d) and disabled (%d) accounts", ghost.Code, w.Code)
	}
	disabledEnv := envelopeShape(t, w.Body.String(), false)
	ghostEnv := envelopeShape(t, ghost.Body.String(), false)
	for _, field := range []string{"code", "message", "detail"} {
		if fmt.Sprint(disabledEnv[field]) != fmt.Sprint(ghostEnv[field]) {
			t.Fatalf("envelope field %q differs between unknown and disabled accounts: %v vs %v",
				field, ghostEnv[field], disabledEnv[field])
		}
	}
	w = doJSON(t, r, http.MethodPost, "/v1/auth/login", "", gin.H{"username": "frost", "password": pass})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 ACCOUNT_DISABLED with the correct password, got %d %s", w.Code, w.Body.String())
	}
}

// TestUserRoutesInternalErrorOmitsDetails 验证 500 响应只暴露泛化文案，
// 内部细节（文件路径、OS 错误码）只落服务端日志（F-3，等保三级 G-11 最小暴露）。
func TestUserRoutesInternalErrorOmitsDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logBuf bytes.Buffer
	api := &userAPI{
		store:    newTestStore(t),
		cfg:      &UserRouteConfig{Logger: slog.New(slog.NewTextHandler(&logBuf, nil))},
		throttle: newLoginThrottle(),
	}

	r := gin.New()
	r.POST("/boom", func(c *gin.Context) {
		api.internalError(c, "unit_test", "someone", "failed to register user",
			fmt.Errorf("mkdir /var/lib/privshield/secrets: permission denied"))
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/boom", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	env := envelopeShape(t, w.Body.String(), false)
	if env["code"] != "INTERNAL_ERROR" {
		t.Fatalf("expected INTERNAL_ERROR code, got %v", env["code"])
	}
	for _, leak := range []string{"/var/lib/privshield", "permission denied", "mkdir", "secrets"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("500 response leaked internal detail %q: %s", leak, w.Body.String())
		}
	}
	// 细节不得丢失：必须写入服务端日志供运维排障
	logged := logBuf.String()
	if !strings.Contains(logged, "/var/lib/privshield") || !strings.Contains(logged, "auth_internal_error") {
		t.Fatalf("internal detail must be logged server-side, got %q", logged)
	}
}

// TestUserRoutesRegisterEndpointThrottled 验证公开注册端点具备独立的每 IP 限速（F-4）：
// 每次注册都要跑一遍 bcrypt(cost=12)，不限速即未认证 CPU 耗尽放大器；
// 且与登录端点各自独立计数，互不牵连。
func TestUserRoutesRegisterEndpointThrottled(t *testing.T) {
	store := newTestStore(t)
	r := newAuthTestEngine(t, store, WithSelfRegistration(true), WithLoginThrottle(2))

	w := doJSON(t, r, http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "regone", "password": "First#Admin2026!",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap registration failed: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(t, r, http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "regtwo", "password": "Second#User2026!",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("self registration failed: %d %s", w.Code, w.Body.String())
	}

	w = doJSON(t, r, http.MethodPost, "/v1/auth/users/register", "", gin.H{
		"username": "regthree", "password": "Third#User2026!",
	})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 when the register endpoint is throttled, got %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("429 response must carry the Retry-After header")
	}
	envelopeShape(t, w.Body.String(), false)
	if store.Exists("regthree") {
		t.Fatal("a throttled registration must not create an account")
	}

	// 注册配额被打满不得连带阻断登录（限速键按端点隔离）
	w = doJSON(t, r, http.MethodPost, "/v1/auth/login", "", gin.H{
		"username": "regone", "password": "First#Admin2026!",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("login must keep its own throttle budget, got %d %s", w.Code, w.Body.String())
	}
}

// TestUserStoreUpdatePermissionsRoleScopeConsistency 验证改权路径与注册路径共用同一
// 角色/scope 一致性口径：非特权角色不得被授予管理类 scope（F-5）。
func TestUserStoreUpdatePermissionsRoleScopeConsistency(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Register("clerk", "Str0ng#Pass2026!", "Clerk", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	for _, tc := range []struct {
		role   string
		scopes []string
	}{
		{"developer", []string{"*"}},
		{"guest", []string{"admin"}},
		{"developer", []string{"user:admin"}},
		{"data-engineer", []string{"hub:admin"}},
		{"user", []string{"ops:admin"}},
		{"guest", []string{"*", "privacy:mask"}},
	} {
		if err := store.UpdatePermissions("clerk", tc.role, tc.scopes); !errors.Is(err, ErrForbiddenScope) {
			t.Fatalf("role=%s scopes=%v: expected ErrForbiddenScope, got %v", tc.role, tc.scopes, err)
		}
	}

	// 被拒的改权不得留下任何副作用
	u, err := store.GetUser("clerk")
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	if u.Role != "developer" {
		t.Fatalf("rejected permission update must not mutate the role, got %q", u.Role)
	}
	for _, sc := range u.Scopes {
		if managementScopes[sc] {
			t.Fatalf("rejected permission update leaked management scope %q", sc)
		}
	}

	// 合法改权仍然放行
	if err := store.UpdatePermissions("clerk", "auditor", nil); err != nil {
		t.Fatalf("legitimate role change failed: %v", err)
	}
	if err := validateRoleScopeConsistency("admin", []string{"*"}); err != nil {
		t.Fatalf("privileged role must accept management scopes, got %v", err)
	}
	if err := validateRoleScopeConsistency("guest", []string{"privacy:mask"}); err != nil {
		t.Fatalf("business scope must stay available to non-privileged roles, got %v", err)
	}
	if err := validateRoleScopeConsistency("guest", nil); err != nil {
		t.Fatalf("nil scopes must be accepted, got %v", err)
	}
}

// TestUserRoutesUpdatePermissionsRejectsManagementScope 验证 HTTP 面同口径返回 403。
func TestUserRoutesUpdatePermissionsRejectsManagementScope(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Register("bossy", "Str0ng#Pass2026!", "Bossy", "admin", nil); err != nil {
		t.Fatalf("register admin failed: %v", err)
	}
	if _, err := store.Register("peon", "Str0ng#Pass2026!", "Peon", "developer", nil); err != nil {
		t.Fatalf("register developer failed: %v", err)
	}
	r := newAuthTestEngine(t, store)
	adminToken, _, err := store.CreateSession("bossy", time.Hour)
	if err != nil {
		t.Fatalf("admin session failed: %v", err)
	}

	w := doJSON(t, r, http.MethodPut, "/v1/auth/users/peon/permissions", adminToken, gin.H{
		"role": "guest", "scopes": []string{"*"},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when granting a management scope to a non-privileged role, got %d %s", w.Code, w.Body.String())
	}
	u, err := store.GetUser("peon")
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	if u.Role != "developer" {
		t.Fatalf("rejected update must not mutate the role, got %q", u.Role)
	}
}

// TestUserStoreChangePasswordRejectsLostUpdate 验证改密的「丢失更新」防护（F-6）：
// 口令校验在锁外执行，写入前必须在同一把写锁内复核哈希快照，
// 陈旧写入者得到 ErrPasswordChangedConcurrently 而不得覆盖已提交的口令。
//
// 交错时序：先行者在 t=0 读取哈希快照并进入锁外 bcrypt（校验+派生合计约数百毫秒），
// 后行者在 t=50ms 读到同一快照、但必然在先行者提交之后才拿到写锁，
// 因而其校验结论已失效。两个边界（50ms 远小于 bcrypt 耗时、又远大于协程启动拖延）
// 均有数量级余量，交错在快慢机器上都是确定的。
func TestUserStoreChangePasswordRejectsLostUpdate(t *testing.T) {
	store := newTestStore(t)
	const oldPass = "Original#Pass2026"
	if _, err := store.Register("racer", oldPass, "Racer", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	winnerErr := make(chan error, 1)
	go func() {
		winnerErr <- store.ChangePassword("racer", oldPass, "Winner#Pass2026!")
	}()
	time.Sleep(50 * time.Millisecond)

	staleErr := store.ChangePassword("racer", oldPass, "Concurrent#Pass2026")
	if werr := <-winnerErr; werr != nil {
		t.Fatalf("the first committed change must succeed, got %v", werr)
	}
	if !errors.Is(staleErr, ErrPasswordChangedConcurrently) {
		t.Fatalf("expected ErrPasswordChangedConcurrently for the stale writer, got %v", staleErr)
	}
	// 丢失更新必须被阻断：最终口令只能是先行者写入的那一个
	if _, err := store.Authenticate("racer", "Concurrent#Pass2026"); err == nil {
		t.Fatal("the stale writer must not overwrite the committed password")
	}
	if _, err := store.Authenticate("racer", "Winner#Pass2026!"); err != nil {
		t.Fatalf("the committed password must remain valid, got %v", err)
	}
}

// TestUserRoutesChangePasswordConflictStatus 验证 HTTP 面把并发改密映射为
// 409 PASSWORD_CHANGED_CONCURRENTLY（而非静默覆写或 500）。
func TestUserRoutesChangePasswordConflictStatus(t *testing.T) {
	store := newTestStore(t)
	const oldPass = "Original#Pass2026"
	if _, err := store.Register("changer", oldPass, "Changer", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	r := newAuthTestEngine(t, store)
	// 会话 Token 在先行者提交前完成鉴权（改密会吊销全部会话）
	token, _, err := store.CreateSession("changer", time.Hour)
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	winnerErr := make(chan error, 1)
	go func() {
		winnerErr <- store.ChangePassword("changer", oldPass, "Winner#Pass2026!")
	}()
	time.Sleep(50 * time.Millisecond)

	w := doJSON(t, r, http.MethodPost, "/v1/auth/change-password", token, gin.H{
		"username": "changer", "old_password": oldPass, "new_password": "Concurrent#Pass2026",
	})
	if werr := <-winnerErr; werr != nil {
		t.Fatalf("the first committed change must succeed, got %v", werr)
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for the stale writer, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "PASSWORD_CHANGED_CONCURRENTLY") {
		t.Fatalf("expected PASSWORD_CHANGED_CONCURRENTLY code, got %s", w.Body.String())
	}
	if _, err := store.Authenticate("changer", "Winner#Pass2026!"); err != nil {
		t.Fatalf("the committed password must remain valid, got %v", err)
	}
}

// TestUserPasswordExpirySignal 验证口令有效期标记（F-10，等保三级 G-04）：
// 超期只标记不阻断登录（避免唯一管理员被锁死），并在登录响应与审计日志中可发现。
func TestUserPasswordExpirySignal(t *testing.T) {
	if (&User{PasswordUpdatedAt: time.Now()}).PasswordExpired() {
		t.Fatal("a freshly set password must not be flagged expired")
	}
	stale := &User{PasswordUpdatedAt: time.Now().Add(-MaxPasswordAge - time.Hour)}
	if !stale.PasswordExpired() {
		t.Fatalf("a password older than %v must be flagged expired", MaxPasswordAge)
	}
	if (&User{}).PasswordExpired() {
		t.Fatal("a zero timestamp (legacy account) must not be flagged expired")
	}
	var nilUser *User
	if nilUser.PasswordExpired() {
		t.Fatal("PasswordExpired must be nil-receiver safe")
	}

	store := newTestStore(t)
	const pass = "Str0ng#Pass2026!"
	if _, err := store.Register("aged", pass, "Aged", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	// 直接把口令更新时间回拨到超期，模拟长期未改密账号
	store.mu.Lock()
	store.users["aged"].PasswordUpdatedAt = time.Now().Add(-MaxPasswordAge - 24*time.Hour)
	store.mu.Unlock()

	r := newAuthTestEngine(t, store)
	w := doJSON(t, r, http.MethodPost, "/v1/auth/login", "", gin.H{"username": "aged", "password": pass})
	if w.Code != http.StatusOK {
		t.Fatalf("an expired password must not block login, got %d %s", w.Code, w.Body.String())
	}
	env := envelopeShape(t, w.Body.String(), true)
	if expired, _ := dataOf(t, env)["password_expired"].(bool); !expired {
		t.Fatalf("login response must flag password_expired=true, got %v", env["data"])
	}

	// 未超期账号不得误报
	if _, err := store.Register("juvenile", pass, "Juvenile", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	w = doJSON(t, r, http.MethodPost, "/v1/auth/login", "", gin.H{"username": "juvenile", "password": pass})
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	env = envelopeShape(t, w.Body.String(), true)
	if expired, _ := dataOf(t, env)["password_expired"].(bool); expired {
		t.Fatalf("a fresh password must not be flagged expired, got %v", env["data"])
	}
}

// TestUserStoreFileHardening 验证凭证库文件的加载期加固（F-7）：
// 权限位收敛为 0600、过期密钥就地清理并回写、超出体积上限直接拒绝加载。
func TestUserStoreFileHardening(t *testing.T) {
	tmpDir := t.TempDir()

	// 1) 宽松权限位（组/其他可读）在加载期被告警并收敛为 0600
	filePath := filepath.Join(tmpDir, "users.json")
	seed, err := NewUserStore(filePath)
	if err != nil {
		t.Fatalf("create seed store failed: %v", err)
	}
	if _, err := seed.Register("hardened", "Str0ng#Pass2026!", "Hardened", "developer", nil); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if err := os.Chmod(filePath, 0o644); err != nil {
		t.Fatalf("chmod failed: %v", err)
	}
	reloaded, err := NewUserStore(filePath)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if !reloaded.Exists("hardened") {
		t.Fatal("credential store failed to reload its accounts")
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("expected the credential store tightened to 0600, got %v", mode)
	}

	// 2) 过期密钥在加载期即被清理，且清理结果回写磁盘（不得长期驻留凭证库）
	stalePath := filepath.Join(tmpDir, "stale.json")
	expiredAt := time.Now().Add(-time.Hour)
	payload := diskData{Version: diskFormatVersion, Users: map[string]*diskUser{
		"stale": {
			Username: "stale", DisplayName: "Stale", Role: "developer",
			Scopes: []string{"privacy:mask"}, Status: UserStatusActive,
			PasswordHash:      "$2a$12$placeholderplaceholderplaceholderplaceholderplaceholder",
			PasswordUpdatedAt: time.Now(),
			APIKeys: map[string]*APIKeyRecord{
				"k_expired": {
					KeyID: "k_expired", Name: "old", TokenHash: HashToken("psk_expired"),
					TokenPrefix: "psk_e***", Scopes: []string{"privacy:mask"},
					CreatedAt: expiredAt, ExpiresAt: &expiredAt,
				},
				"k_valid": {
					KeyID: "k_valid", Name: "new", TokenHash: HashToken("psk_valid"),
					TokenPrefix: "psk_v***", Scopes: []string{"privacy:mask"}, CreatedAt: time.Now(),
				},
			},
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		},
	}}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture failed: %v", err)
	}
	if err := os.WriteFile(stalePath, raw, 0o600); err != nil {
		t.Fatalf("write fixture failed: %v", err)
	}
	purged, err := NewUserStore(stalePath)
	if err != nil {
		t.Fatalf("load stale store failed: %v", err)
	}
	user, err := purged.GetUser("stale")
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	if _, ok := user.APIKeys["k_expired"]; ok {
		t.Fatal("an expired key must be purged on load")
	}
	if _, ok := user.APIKeys["k_valid"]; !ok {
		t.Fatal("a valid key must survive the load-time purge")
	}
	live := purged.LiveHashedKeys()
	if _, ok := live[HashToken("psk_expired")]; ok {
		t.Fatal("an expired key must not be revived as a live credential")
	}
	if _, ok := live[HashToken("psk_valid")]; !ok {
		t.Fatal("a valid key must stay live after the purge")
	}
	persisted, err := os.ReadFile(stalePath)
	if err != nil {
		t.Fatalf("read back failed: %v", err)
	}
	if strings.Contains(string(persisted), "k_expired") {
		t.Fatal("the purge must be persisted so expired material leaves the credential store")
	}

	// 3) 超出体积上限的凭证库必须被拒绝加载（防止启动期被超大文件耗尽内存）
	bigPath := filepath.Join(tmpDir, "big.json")
	big, err := os.Create(bigPath)
	if err != nil {
		t.Fatalf("create oversized file failed: %v", err)
	}
	if err := big.Truncate(maxUserStoreFileSize + 1); err != nil {
		_ = big.Close()
		t.Fatalf("truncate failed: %v", err)
	}
	if err := big.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if _, err := NewUserStore(bigPath); err == nil {
		t.Fatal("an oversized credential store must be rejected instead of loaded into memory")
	}
}

// TestChannelSecretWatcherCloseIsPanicFree 验证 Close 只关闭停止信号而不关闭发送侧
// channel：与并发 Push 竞争时不得触发 "send on closed channel"（F-9）。
// 该 panic 发生在子协程，会直接拖垮整个进程（密钥热轮转路径上的崩溃 = 可用性事故），
// 因此本用例的断言就是「测试二进制不崩溃」。
func TestChannelSecretWatcherCloseIsPanicFree(t *testing.T) {
	w := NewChannelSecretWatcher(4)
	events, err := w.Watch(context.Background(), "privshield-api-keys")
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}

	var wg sync.WaitGroup
	// 消费者持续排空事件通道，避免 Push 因缓冲区满而阻塞在超时分支上
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-w.stopCh:
				return
			case <-events:
			}
		}
	}()
	// 生产者与 Close 并发写入
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 32; j++ {
				_ = w.Push(SecretEvent{SecretName: "privshield-api-keys", Content: fmt.Sprintf("k%d=v%d", idx, j)})
			}
		}(i)
	}
	time.Sleep(20 * time.Millisecond)
	if err := w.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	// Close 必须幂等（不得二次 close(stopCh) panic），且关闭后 Push 返回 error 而非 panic
	if err := w.Close(); err != nil {
		t.Fatalf("second close must be idempotent, got %v", err)
	}
	if err := w.Push(SecretEvent{SecretName: "privshield-api-keys", Content: "late"}); err == nil {
		t.Fatal("Push after Close must report an error")
	}
	wg.Wait()
}
