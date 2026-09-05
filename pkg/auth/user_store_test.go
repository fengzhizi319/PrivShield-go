package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestUserStoreRegistrationAndAuth(t *testing.T) {
	store, err := NewUserStore("")
	if err != nil {
		t.Fatalf("failed to create UserStore: %v", err)
	}

	// 1. 口令过短 (< 8)
	_, err = store.Register("alice", "Ab1!", "Alice", "developer", nil)
	if err != ErrPasswordTooShort {
		t.Fatalf("expected ErrPasswordTooShort, got %v", err)
	}

	// 2. 口令缺少分类 (仅小写+数字，< 3类)
	_, err = store.Register("alice", "abcdef123456", "Alice", "developer", nil)
	if err != ErrPasswordWeak {
		t.Fatalf("expected ErrPasswordWeak, got %v", err)
	}

	// 3. 口令包含用户名（独立哨兵错误，便于客户端给出可操作提示）
	_, err = store.Register("alice", "Alice@2026!Strong", "Alice", "developer", nil)
	if err != ErrPasswordContainsName {
		t.Fatalf("expected ErrPasswordContainsName for containing username, got %v", err)
	}
	// 3b. 口令包含用户名逆序同样被拒（防 "ecilA" 这类反转规避）
	_, err = store.Register("alice", "Ecila@2026!Strong", "Alice", "developer", nil)
	if err != ErrPasswordContainsName {
		t.Fatalf("expected ErrPasswordContainsName for reversed username, got %v", err)
	}

	// 4. 口令命中常见弱密码字典（字符类别达标，但前缀属于黑名单）
	_, err = store.Register("alice", "Password1234#Aa", "Alice", "developer", nil)
	if err != ErrPasswordBlacklisted {
		t.Fatalf("expected ErrPasswordBlacklisted for weak dictionary, got %v", err)
	}

	// 5. 成功注册
	user, err := store.Register("alice", "SecurePass#2026!", "Alice", "developer", nil)
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if user.Username != "alice" || user.Role != "developer" {
		t.Fatalf("unexpected user data: %+v", user)
	}
	if len(user.Scopes) == 0 {
		t.Fatal("expected default scopes for role developer")
	}

	// 6. 重复注册
	_, err = store.Register("alice", "AnotherPass#2026!", "Alice", "developer", nil)
	if err != ErrUserAlreadyExists {
		t.Fatalf("expected ErrUserAlreadyExists, got %v", err)
	}

	// 7. 认证成功
	authed, err := store.Authenticate("alice", "SecurePass#2026!")
	if err != nil {
		t.Fatalf("auth failed: %v", err)
	}
	if authed.Username != "alice" {
		t.Fatalf("expected username alice, got %s", authed.Username)
	}

	// 8. 错误口令重试防爆破锁定
	for i := 0; i < 4; i++ {
		_, err := store.Authenticate("alice", "WrongPass#123")
		if err != ErrInvalidPassword {
			t.Fatalf("expected ErrInvalidPassword, got %v", err)
		}
	}
	// 第 5 次失败
	_, err = store.Authenticate("alice", "WrongPass#123")
	if err != ErrInvalidPassword {
		t.Fatalf("expected ErrInvalidPassword on 5th failure, got %v", err)
	}
	// 第 6 次调用应触发锁定
	_, err = store.Authenticate("alice", "SecurePass#2026!")
	if err != ErrAccountLocked {
		t.Fatalf("expected ErrAccountLocked, got %v", err)
	}
	if !store.IsLocked("alice") {
		t.Fatal("expected IsLocked to be true")
	}
}

func TestUserStorePermissionsAndLiveKeys(t *testing.T) {
	store, err := NewUserStore("")
	if err != nil {
		t.Fatalf("failed to create UserStore: %v", err)
	}

	user, err := store.Register("bob", "Complex#Pass2026", "Bob", "developer", nil)
	if err != nil {
		t.Fatalf("register bob failed: %v", err)
	}
	if user.Username != "bob" || user.Role != "developer" {
		t.Fatalf("unexpected user data: %+v", user)
	}

	// 签发 API Key
	rec, token, err := store.IssueAPIKey("bob", "test-key", []string{"privacy:mask"}, 1*time.Hour)
	if err != nil {
		t.Fatalf("issue api key failed: %v", err)
	}
	if rec.TokenPrefix == "" || token == "" {
		t.Fatal("expected valid token and prefix")
	}

	// 验证活密钥快照即时生效（以 Token 摘要为索引，明文不落盘）
	liveKeys := store.LiveHashedKeys()
	cfg, ok := liveKeys[HashToken(token)]
	if !ok {
		t.Fatalf("expected live hashed keys to contain token hash of %s", token)
	}
	if len(cfg.Scopes) != 1 || cfg.Scopes[0] != "privacy:mask" {
		t.Fatalf("unexpected scopes in live key: %+v", cfg.Scopes)
	}
	if cfg.Subject != "bob" {
		t.Fatalf("expected live key subject=bob, got %q", cfg.Subject)
	}
	// 活密钥快照不得泄露明文 Token（摘要不可逆推）
	if _, exists := liveKeys[token]; exists {
		t.Fatal("live hashed keys must not be indexed by plaintext token")
	}

	// 越权签发拦截：bob 没有 admin 权限，尝试签发 admin scope 报错
	_, _, err = store.IssueAPIKey("bob", "hack-key", []string{"admin"}, 1*time.Hour)
	if err == nil {
		t.Fatal("expected error when issuing scope exceeding user permission")
	}

	// 调整 bob 权限并联动
	err = store.UpdatePermissions("bob", "data-engineer", []string{"privacy:mask", "privacy:dp"})
	if err != nil {
		t.Fatalf("update permissions failed: %v", err)
	}
	liveKeys = store.LiveHashedKeys()
	cfg = liveKeys[HashToken(token)]
	if len(cfg.Scopes) != 2 {
		t.Fatalf("expected live key scopes updated to 2, got %d", len(cfg.Scopes))
	}

	// 禁用账号
	err = store.SetStatus("bob", UserStatusDisabled)
	if err != nil {
		t.Fatalf("set status disabled failed: %v", err)
	}
	liveKeys = store.LiveHashedKeys()
	if _, exists := liveKeys[HashToken(token)]; exists {
		t.Fatal("disabled user's token should not be in live hashed keys")
	}

	// 重新激活
	err = store.SetStatus("bob", UserStatusActive)
	if err != nil {
		t.Fatalf("set status active failed: %v", err)
	}
	liveKeys = store.LiveHashedKeys()
	if _, exists := liveKeys[HashToken(token)]; !exists {
		t.Fatal("reactivated user's token should be restored in live hashed keys")
	}

	// 吊销 API Key
	err = store.RevokeAPIKey("bob", rec.KeyID)
	if err != nil {
		t.Fatalf("revoke key failed: %v", err)
	}
	liveKeys = store.LiveHashedKeys()
	if _, exists := liveKeys[HashToken(token)]; exists {
		t.Fatal("revoked token should be removed from live hashed keys")
	}
}

func TestUserStorePersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "privshield_auth_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	filePath := filepath.Join(tmpDir, "users.json")
	store1, err := NewUserStore(filePath)
	if err != nil {
		t.Fatalf("failed to create store1: %v", err)
	}

	_, err = store1.Register("charlie", "Secret#Audit2026!", "Charlie", "auditor", nil)
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	rec, token, err := store1.IssueAPIKey("charlie", "audit-token", nil, 24*time.Hour)
	if err != nil {
		t.Fatalf("issue key failed: %v", err)
	}

	// 重新加载实例
	store2, err := NewUserStore(filePath)
	if err != nil {
		t.Fatalf("failed to reload store2: %v", err)
	}

	if store2.Count() != 1 {
		t.Fatalf("expected 1 user in store2, got %d", store2.Count())
	}

	u, err := store2.GetUser("charlie")
	if err != nil || u.Username != "charlie" {
		t.Fatalf("failed to retrieve charlie: %v", err)
	}

	// 验证重新加载后的活密钥依然保有该 Token（仅摘要落盘，重启无损恢复）
	live2 := store2.LiveHashedKeys()
	if _, exists := live2[HashToken(token)]; !exists {
		t.Fatalf("expected token %s to be reloaded into active keys", token)
	}

	// 重启后仍可凭原口令登录（口令哈希必须落盘）
	if _, err := store2.Authenticate("charlie", "Secret#Audit2026!"); err != nil {
		t.Fatalf("authenticate after reload failed: %v", err)
	}

	// 落盘文件不得包含明文 Token
	raw, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read persisted file failed: %v", err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("persisted file must not contain plaintext token")
	}
	if !strings.Contains(string(raw), "password_hash") {
		t.Fatal("persisted file must contain password hash")
	}

	_ = rec
}

func TestUserRoutesIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	store, _ := NewUserStore("")
	settings := &Settings{
		AuthEnabled:            true,
		LiveInternalHashedKeys: store.LiveHashedKeysFunc(),
	}

	// 注册全局 Auth 中间件
	r.Use(AuthMiddleware(settings))
	RegisterUserRoutes(r, store)

	// 1. 初始公开注册管理员 (store.Count() == 0 允许初始管理员引导)
	regAdminBody := map[string]any{
		"username":     "rootadmin",
		"password":     "AdminRoot#2026!",
		"display_name": "Root Admin",
		"role":         "admin",
	}
	data, _ := json.Marshal(regAdminBody)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/auth/users/register", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap admin register failed: %d, body: %s", w.Code, w.Body.String())
	}

	// 2. 管理员登录获取 Token
	loginBody := map[string]string{
		"username": "rootadmin",
		"password": "AdminRoot#2026!",
	}
	data, _ = json.Marshal(loginBody)
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/auth/login", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d, body: %s", w.Code, w.Body.String())
	}

	var loginResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &loginResp)
	adminToken := loginResp.Data.Token
	if adminToken == "" {
		t.Fatal("empty admin token")
	}

	// 3. 管理员查询用户清单
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/v1/auth/users", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list users failed: %d, body: %s", w.Code, w.Body.String())
	}

	// 4. 默认关闭公开自注册：匿名注册应被拒绝 (403 SELF_REGISTER_DISABLED)
	regDevBody := map[string]any{
		"username":     "devuser",
		"password":     "SecretDeveloper#2026!",
		"display_name": "Developer User",
		"role":         "developer",
	}
	data, _ = json.Marshal(regDevBody)
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/auth/users/register", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when self-registration disabled, got %d, body: %s", w.Code, w.Body.String())
	}

	// 4b. 管理员开户：携带管理员 Token 为下属创建 developer 账号 (200)
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/auth/users/register", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin-provisioned devuser register failed: %d, body: %s", w.Code, w.Body.String())
	}

	// 5. 匿名请求注册特权角色（引导期已结束）应被拒绝 403
	regPrivBody := map[string]any{
		"username": "hacker",
		"password": "Hacker#Pass2026!",
		"role":     "admin",
	}
	data, _ = json.Marshal(regPrivBody)
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/auth/users/register", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 forbidden for unauthorized admin registration, got %d", w.Code)
	}

	// 6. 管理员为 devuser 签发 API Key（Scope 必须属于 devuser 自身权限子集）
	issueKeyBody := map[string]any{
		"key_name": "dev-runner",
		"scopes":   []string{"privacy:mask"},
	}
	data, _ = json.Marshal(issueKeyBody)
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/auth/users/devuser/keys", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("issue key failed: %d, body: %s", w.Code, w.Body.String())
	}

	var keyResp struct {
		Data struct {
			KeyID string `json:"key_id"`
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &keyResp)
	devToken := keyResp.Data.Token
	devKeyID := keyResp.Data.KeyID

	// 7. 使用刚刚签发的 devToken 访问受限用户详情 (devuser 查自己成功)
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/v1/auth/users/devuser", nil)
	req.Header.Set("Authorization", "Bearer "+devToken)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get self user profile failed: %d, body: %s", w.Code, w.Body.String())
	}

	// 8. 吊销该 Key
	w = httptest.NewRecorder()
	req = httptest.NewRequest("DELETE", "/v1/auth/users/devuser/keys/"+devKeyID, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke key failed: %d, body: %s", w.Code, w.Body.String())
	}

	// 9. 被吊销的 Key 再次请求应返回 401 UNAUTHENTICATED
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/v1/auth/users/devuser", nil)
	req.Header.Set("Authorization", "Bearer "+devToken)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated after key revocation, got %d", w.Code)
	}
}
