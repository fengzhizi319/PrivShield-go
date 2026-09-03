package auth

import (
	"context"
	"testing"
	"time"
)

func TestChannelSecretWatcher_Lifecycle(t *testing.T) {
	watcher := NewChannelSecretWatcher(4)
	defer watcher.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, err := watcher.Watch(ctx, "test-secret")
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	// 1. 推送首个事件
	err = watcher.Push(SecretEvent{
		SecretName: "test-secret",
		Content:    "token1:svc-a:privacy:mask",
		Version:    "v1",
	})
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	select {
	case ev := <-events:
		if ev.Content != "token1:svc-a:privacy:mask" {
			t.Errorf("got content %q, want token1:svc-a:privacy:mask", ev.Content)
		}
		if ev.Version != "v1" {
			t.Errorf("got version %q, want v1", ev.Version)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for secret event")
	}

	// 2. 关闭后推送报错
	watcher.Close()
	err = watcher.Push(SecretEvent{SecretName: "test-secret", Content: "token2:svc-b:hub:read"})
	if err == nil {
		t.Error("expected error when pushing to closed watcher")
	}
}

func TestKeyStore_WithWatcher(t *testing.T) {
	watcher := NewChannelSecretWatcher(8)
	defer watcher.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial := "tok1:svc-1:privacy:mask;tok2:svc-2:hub:read"
	ks, err := NewKeyStoreWithWatcher(ctx, watcher, "privshield-api-keys", initial)
	if err != nil {
		t.Fatalf("NewKeyStoreWithWatcher: %v", err)
	}
	defer ks.Close()

	keys := ks.Keys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 initial keys, got %d", len(keys))
	}
	if keys["tok1"].Name != "svc-1" {
		t.Errorf("tok1 name = %q, want svc-1", keys["tok1"].Name)
	}

	// 推送热更新事件
	err = watcher.Push(SecretEvent{
		SecretName: "privshield-api-keys",
		Content:    "tok3:svc-3:audit:write",
		Version:    "v2",
	})
	if err != nil {
		t.Fatalf("push secret event: %v", err)
	}

	// 等待 KeyStore 异步更新
	var updatedKeys map[string]*KeyConfig
	for i := 0; i < 20; i++ {
		time.Sleep(20 * time.Millisecond)
		updatedKeys = ks.Keys()
		if len(updatedKeys) == 1 && updatedKeys["tok3"] != nil {
			break
		}
	}

	if len(updatedKeys) != 1 || updatedKeys["tok3"] == nil {
		t.Fatalf("expected updated keys to contain tok3, got %+v", updatedKeys)
	}
	if updatedKeys["tok3"].Scopes[0] != "audit:write" {
		t.Errorf("tok3 scope = %v, want [audit:write]", updatedKeys["tok3"].Scopes)
	}

	// 测试 UpdateKeys 编程式原子更新
	ks.UpdateKeys(map[string]*KeyConfig{
		"manual-tok": {Name: "manual-svc", Scopes: []string{"admin"}},
	})
	manualKeys := ks.Keys()
	if len(manualKeys) != 1 || manualKeys["manual-tok"] == nil {
		t.Fatalf("expected manual-tok key, got %+v", manualKeys)
	}
}
