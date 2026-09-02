package auth

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// KeyStore 提供基于文件的 API Key 热重载能力。
// 后台 goroutine 每 5 秒检查文件 mtime，变更时自动重新加载。
// 复用 pkg/tlsutil/whitelist.go 的轮询模式。
type KeyStore struct {
	mu          sync.RWMutex
	keys        map[string]*KeyConfig
	path        string
	stopCh      chan struct{}
	stopOnce    sync.Once
	lastModTime time.Time
}

// NewKeyStore 创建并启动一个 KeyStore，从指定文件加载 API Key。
// 文件格式与 ParseAPIKeysEnv 相同：token:name:scope1,scope2[;token2:name2:scope3]
// 支持可选的 RFC3339 过期时间：token:name:scopes:2025-12-31T23:59:59Z
func NewKeyStore(path string) (*KeyStore, error) {
	ks := &KeyStore{
		path:   path,
		stopCh: make(chan struct{}),
	}
	if err := ks.reload(); err != nil {
		return nil, fmt.Errorf("initial key load: %w", err)
	}
	go ks.poll()
	return ks, nil
}

// Keys 返回当前 API Key 映射的只读快照。
func (ks *KeyStore) Keys() map[string]*KeyConfig {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	result := make(map[string]*KeyConfig, len(ks.keys))
	for k, v := range ks.keys {
		result[k] = v
	}
	return result
}

// Close 停止后台轮询 goroutine。
func (ks *KeyStore) Close() {
	ks.stopOnce.Do(func() {
		close(ks.stopCh)
	})
}

func (ks *KeyStore) poll() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ks.stopCh:
			return
		case <-ticker.C:
			info, err := os.Stat(ks.path)
			if err != nil {
				slog.Warn("KeyStore: stat failed", "path", ks.path, "error", err.Error())
				continue
			}
			ks.mu.RLock()
			changed := info.ModTime().After(ks.lastModTime)
			ks.mu.RUnlock()
			if changed {
				if err := ks.reload(); err != nil {
					slog.Error("KeyStore: reload failed", "path", ks.path, "error", err.Error())
				} else {
					slog.Info("KeyStore: keys reloaded", "path", ks.path)
				}
			}
		}
	}
}

func (ks *KeyStore) reload() error {
	data, err := os.ReadFile(ks.path)
	if err != nil {
		return fmt.Errorf("read keys file: %w", err)
	}

	info, err := os.Stat(ks.path)
	if err != nil {
		return fmt.Errorf("stat keys file: %w", err)
	}

	content := strings.TrimSpace(string(data))
	keys := ParseAPIKeysEnv(content)

	ks.mu.Lock()
	ks.keys = keys
	ks.lastModTime = info.ModTime()
	ks.mu.Unlock()

	slog.Info("KeyStore: loaded keys", "path", ks.path, "count", len(keys))
	return nil
}
