package security

// ==============================================================================
// 【H-1′ 回归门禁：KeyStore（AGENT_AUTH_KEYS_FILE）模式下的 REST 授权对称性】
//
// 历史上 internal/security.AuthMiddleware() 在 keyStore != nil 时会切换到一份复制出来的
// “热重载中间件”。该副本只做认证、**遗漏了 PermissionForRESTPath 的 scope 校验**，于是：
//   - 只要配了密钥文件，任何一把合法 Key（哪怕只有 health:read）都能访问
//     /v1/privacy/budget、dynclassification 写接口、/debug/pprof 等全部端点；
//   - 它还把 InternalKeys 整体替换为文件密钥，使环境变量 Key 在 REST 面静默 401。
//
// 现在活密钥统一由 pkg/auth.Settings.LiveInternalKeys 携带、在认证内核内活读，
// 中间件只有一份实现。本文件把这三条语义固定为断言，防止有人再复制一份“优化过的”副本。
// ==============================================================================

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
)

// writeKeysFile 写入密钥文件并返回路径；内容格式与 ParseAPIKeysEnv 一致。
// 随后调用 KeyStore.ReloadContent 即等价于后台轮询器检测到 mtime 变化后的一次 reload。
func writeKeysFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api-keys.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write keys file: %v", err)
	}
	return path
}

// reloadKeyStore 模拟 5s 轮询触发的热重载：读文件 + 整体替换密钥集，
// 与 KeyStore.poll() -> reload() 的行为等价（直接 sleep 会让单测变慢且不稳定）。
func reloadKeyStore(t *testing.T, path string) {
	t.Helper()
	ks := GetKeyStore()
	if ks == nil {
		t.Fatal("expected a KeyStore to be initialized by AGENT_AUTH_KEYS_FILE")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read keys file: %v", err)
	}
	if err := ks.ReloadContent(string(data)); err != nil {
		t.Fatalf("reload keys: %v", err)
	}
}

// newGuardedRouter 挂载与生产一致的 AuthMiddleware，并注册若干真实路径的空实现处理器。
func newGuardedRouter() *gin.Engine {
	r := gin.New()
	r.Use(AuthMiddleware())
	handler := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	for _, path := range []string{"/v1/privacy/mask", "/v1/privacy/budget", "/debug/pprof/heap"} {
		r.GET(path, handler)
	}
	return r
}

func doRequest(r *gin.Engine, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestAuthMiddleware_KeyStoreModeStillEnforcesScopes 是 H-1′ 的核心断言：
// 配置密钥文件后，scope 鉴权必须与纯环境变量模式一样生效。
func TestAuthMiddleware_KeyStoreModeStillEnforcesScopes(t *testing.T) {
	ResetSettings()
	t.Setenv("AGENT_AUTH_ENABLED", "true")
	t.Setenv("AGENT_AUTH_KEYS_FILE", writeKeysFile(t, "file-mask-key-123456:mask-svc:privacy:mask"))
	ResetSettings()
	t.Cleanup(ResetSettings)

	r := newGuardedRouter()

	if rec := doRequest(r, "/v1/privacy/mask", "file-mask-key-123456"); rec.Code != http.StatusOK {
		t.Fatalf("privacy:mask key must reach /v1/privacy/mask, got %d: %s", rec.Code, rec.Body.String())
	}
	// budget 需要 privacy:budget、pprof 需要 ops:admin，二者都必须被拒。
	for _, path := range []string{"/v1/privacy/budget", "/debug/pprof/heap"} {
		if rec := doRequest(r, path, "file-mask-key-123456"); rec.Code != http.StatusForbidden {
			t.Errorf("file key with only privacy:mask must get 403 on %s, got %d: %s",
				path, rec.Code, rec.Body.String())
		}
	}
	if rec := doRequest(r, "/v1/privacy/mask", "bogus-key-1234567890"); rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown token must be 401, got %d", rec.Code)
	}
}

// TestAuthMiddleware_KeyStoreModeKeepsEnvKeysWorking 验证并集语义：
// 旧副本用文件密钥整体替换 InternalKeys，会让环境变量 Key 在 REST 面静默 401（只在 gRPC 面可用）。
func TestAuthMiddleware_KeyStoreModeKeepsEnvKeysWorking(t *testing.T) {
	ResetSettings()
	t.Setenv("AGENT_AUTH_ENABLED", "true")
	t.Setenv("AGENT_AUTH_INTERNAL_API_KEYS", "env-mask-key-1234:env-svc:privacy:mask")
	t.Setenv("AGENT_AUTH_KEYS_FILE", writeKeysFile(t, "file-mask-key-5678:file-svc:privacy:mask"))
	ResetSettings()
	t.Cleanup(ResetSettings)

	settings := GetSettings()
	if settings.LiveInternalKeys == nil {
		t.Fatal("LiveInternalKeys must be wired when AGENT_AUTH_KEYS_FILE is set")
	}
	// 语义约定：文件型密钥只能经 LiveInternalKeys 活读，不得并入启动期静态快照，
	// 否则密钥从文件删除后仍会命中旧快照（撤销绕过）。
	if _, dup := settings.InternalKeys["file-mask-key-5678"]; dup {
		t.Error("InternalKeys must hold only static (env) keys; file keys must live solely in LiveInternalKeys")
	}
	if _, ok := settings.LiveInternalKeys()["file-mask-key-5678"]; !ok {
		t.Error("file key must be visible through LiveInternalKeys")
	}

	r := newGuardedRouter()
	if rec := doRequest(r, "/v1/privacy/mask", "env-mask-key-1234"); rec.Code != http.StatusOK {
		t.Errorf("env key must still authenticate on REST alongside a key store, got %d: %s",
			rec.Code, rec.Body.String())
	}
	if rec := doRequest(r, "/v1/privacy/mask", "file-mask-key-5678"); rec.Code != http.StatusOK {
		t.Errorf("file key must authenticate too, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAuthMiddleware_RevokedFileKeyRejectedEverywhere 锁死撤销语义：
// 从密钥文件删除的 Key，必须立即在共享认证内核上失效（REST 与 gRPC 同源，一处生效即两处生效）。
func TestAuthMiddleware_RevokedFileKeyRejectedEverywhere(t *testing.T) {
	// 多把 Key 以 ; 分隔（ParseAPIKeysEnv 的条目分隔符）。
	path := writeKeysFile(t, "to-revoke-key-1234:victim:privacy:mask;keep-key-1234567:survivor:privacy:mask")
	ResetSettings()
	t.Setenv("AGENT_AUTH_ENABLED", "true")
	t.Setenv("AGENT_AUTH_KEYS_FILE", path)
	ResetSettings()
	t.Cleanup(ResetSettings)

	r := newGuardedRouter()
	if rec := doRequest(r, "/v1/privacy/mask", "to-revoke-key-1234"); rec.Code != http.StatusOK {
		t.Fatalf("key present in the file must authenticate, got %d: %s", rec.Code, rec.Body.String())
	}

	if err := os.WriteFile(path, []byte("keep-key-1234567:survivor:privacy:mask"), 0o600); err != nil {
		t.Fatalf("rewrite keys file: %v", err)
	}
	reloadKeyStore(t, path)

	if rec := doRequest(r, "/v1/privacy/mask", "to-revoke-key-1234"); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked file key must stop authenticating at once, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(r, "/v1/privacy/mask", "keep-key-1234567"); rec.Code != http.StatusOK {
		t.Errorf("surviving key must keep working after reload, got %d: %s", rec.Code, rec.Body.String())
	}
	// gRPC 拦截器调用的是同一个内核函数，此断言即代表两条路径同时收敛。
	if id := pkgauth.AuthenticateAPIKey(&GetSettings().Settings, "to-revoke-key-1234"); id != nil {
		t.Errorf("revoked key must not authenticate on the shared kernel, got %+v", id)
	}
}

// TestAuthMiddleware_EmptyLiveKeysAreNotBypass 防止误用：活密钥为空集时不得成为放行通道，
// 同时确认文件密钥确实只经 LiveInternalKeys 进入认证流程。
func TestAuthMiddleware_EmptyLiveKeysAreNotBypass(t *testing.T) {
	ResetSettings()
	t.Setenv("AGENT_AUTH_ENABLED", "true")
	t.Setenv("AGENT_AUTH_INTERNAL_API_KEYS", "")
	t.Setenv("AGENT_AUTH_KEYS_FILE", writeKeysFile(t, "only-file-key-12345:f1:privacy:mask"))
	ResetSettings()
	t.Cleanup(ResetSettings)

	settings := &GetSettings().Settings
	if id := pkgauth.AuthenticateAPIKey(settings, ""); id != nil {
		t.Errorf("empty token must never authenticate, got %+v", id)
	}
	if id := pkgauth.AuthenticateAPIKey(settings, "only-file-key-12345"); id == nil {
		t.Error("file key must authenticate via LiveInternalKeys")
	}
}
