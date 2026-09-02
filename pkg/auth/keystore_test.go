package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKeyStore_LoadAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.txt")

	if err := os.WriteFile(path, []byte("tok1:svc-a:privacy:mask;tok2:svc-b:hub:read"), 0600); err != nil {
		t.Fatal(err)
	}

	ks, err := NewKeyStore(path)
	if err != nil {
		t.Fatalf("NewKeyStore: %v", err)
	}
	defer ks.Close()

	keys := ks.Keys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys["tok1"].Name != "svc-a" {
		t.Errorf("tok1 name = %q, want svc-a", keys["tok1"].Name)
	}
	if keys["tok2"].Scopes[0] != "hub:read" {
		t.Errorf("tok2 scope = %v, want [hub:read]", keys["tok2"].Scopes)
	}

	// Simulate file change
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("tok3:svc-c:privacy:dp"), 0600); err != nil {
		t.Fatal(err)
	}

	// Force reload by calling reload directly (poll would take 5s)
	if err := ks.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	keys = ks.Keys()
	if len(keys) != 1 {
		t.Fatalf("after reload: expected 1 key, got %d", len(keys))
	}
	if keys["tok3"].Name != "svc-c" {
		t.Errorf("tok3 name = %q, want svc-c", keys["tok3"].Name)
	}
	if _, ok := keys["tok1"]; ok {
		t.Error("tok1 should not exist after reload")
	}
}

func TestKeyStore_MissingFile(t *testing.T) {
	_, err := NewKeyStore("/nonexistent/path/keys.txt")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
