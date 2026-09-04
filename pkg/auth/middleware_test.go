// Package auth 测试套件 —— 认证内核与密钥热轮转（LiveInternalKeys）语义。
//
// ==============================================================================
// 【为什么单独有本文件】
// Settings.LiveInternalKeys 是「REST 与 gRPC 双路径共享同一份活密钥」的落地点。
// 各服务历史上倾向复制一份「热重载中间件」，而副本往往只做认证不做鉴权、或只在一条
// 路径上生效，形成「文件里删掉的密钥在另一个端口仍然可用」的撤销绕过。把语义固定在内核
// 层并加以断言，可确保任何调用方（Gin 中间件 / gRPC 拦截器 / 未来的新协议）行为一致。
// ==============================================================================

package auth

import (
	"testing"
	"time"
)

// keyMap 恒等辅助函数：仅让测试里的密钥表构造意图更明确（键即 token）。
func keyMap(entries map[string]*KeyConfig) map[string]*KeyConfig { return entries }

func TestAuthenticateAPIKey_StaticOnly(t *testing.T) {
	s := &Settings{AuthEnabled: true, InternalKeys: keyMap(map[string]*KeyConfig{
		"static-token": {Name: "static-svc", Scopes: []string{"privacy:mask"}},
	})}

	id := AuthenticateAPIKey(s, "static-token")
	if id == nil || id.Name != "static-svc" || id.ServiceType != "internal" {
		t.Fatalf("static internal key must authenticate, got %+v", id)
	}
	if AuthenticateAPIKey(s, "wrong-token") != nil {
		t.Error("unknown token must not authenticate")
	}
	if AuthenticateAPIKey(nil, "static-token") != nil {
		t.Error("nil settings must not authenticate (fail-closed)")
	}
}

// TestAuthenticateAPIKey_LiveKeysAreRevokedImmediately 锁死撤销语义：
// 密钥从活存储（KeyStore）中删除后，必须立即无法认证——即使它仍出现在启动期快照里
// 也不允许命中。这是调用方「文件型密钥不得并入 InternalKeys」约定的对偶断言。
func TestAuthenticateAPIKey_LiveKeysAreRevokedImmediately(t *testing.T) {
	live := map[string]*KeyConfig{
		"file-token": {Name: "file-svc", Scopes: []string{"privacy:mask"}},
	}
	s := &Settings{
		AuthEnabled:      true,
		InternalKeys:     keyMap(map[string]*KeyConfig{}), // 约定：静态快照只放环境变量密钥
		LiveInternalKeys: func() map[string]*KeyConfig { return live },
	}

	if id := AuthenticateAPIKey(s, "file-token"); id == nil || id.Name != "file-svc" {
		t.Fatalf("live key must authenticate, got %+v", id)
	}

	// 模拟运维从密钥文件中删除该 key（KeyStore 轮询后快照变小）。
	delete(live, "file-token")
	if id := AuthenticateAPIKey(s, "file-token"); id != nil {
		t.Fatalf("revoked live key must stop authenticating at once, got %+v", id)
	}
}

// TestAuthenticateAPIKey_LiveKeysDoNotShadowStaticKeys 验证并集语义：
// 环境变量密钥不得因为存在热重载文件而被覆盖失效（历史上 REST 热重载分支整体替换
// InternalKeys，导致 env key 只在 gRPC 面可用、REST 面 401）。
func TestAuthenticateAPIKey_LiveKeysDoNotShadowStaticKeys(t *testing.T) {
	s := &Settings{
		AuthEnabled:  true,
		InternalKeys: keyMap(map[string]*KeyConfig{"env-token": {Name: "env-svc", Scopes: []string{"privacy:dp"}}}),
		LiveInternalKeys: func() map[string]*KeyConfig {
			return keyMap(map[string]*KeyConfig{"file-token": {Name: "file-svc", Scopes: []string{"privacy:mask"}}})
		},
	}

	if id := AuthenticateAPIKey(s, "env-token"); id == nil || id.Name != "env-svc" {
		t.Errorf("env key must keep working alongside a key store, got %+v", id)
	}
	if id := AuthenticateAPIKey(s, "file-token"); id == nil || id.Name != "file-svc" {
		t.Errorf("file key must work too, got %+v", id)
	}
}

// TestAuthenticateAPIKey_LiveKeysHonorExpiry 验证 G-14 过期语义在活密钥上同样生效。
func TestAuthenticateAPIKey_LiveKeysHonorExpiry(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	s := &Settings{
		AuthEnabled: true,
		LiveInternalKeys: func() map[string]*KeyConfig {
			return keyMap(map[string]*KeyConfig{"exp-token": {Name: "exp-svc", Scopes: []string{"*"}, ExpiresAt: &past}})
		},
	}
	if id := AuthenticateAPIKey(s, "exp-token"); id != nil {
		t.Errorf("expired live key must be rejected (G-14), got %+v", id)
	}
}

// TestAuthenticateAPIKey_ExternalKeysStillWork 保证新增活密钥分支没有破坏外部密钥路径。
func TestAuthenticateAPIKey_ExternalKeysStillWork(t *testing.T) {
	s := &Settings{
		AuthEnabled:      true,
		ExternalKeys:     keyMap(map[string]*KeyConfig{"ext-token": {Name: "biz-app", Scopes: []string{"privacy:mask"}}}),
		LiveInternalKeys: func() map[string]*KeyConfig { return keyMap(map[string]*KeyConfig{}) },
	}
	id := AuthenticateAPIKey(s, "ext-token")
	if id == nil || id.ServiceType != "external" || id.Name != "biz-app" {
		t.Fatalf("external key must authenticate as external, got %+v", id)
	}
}
