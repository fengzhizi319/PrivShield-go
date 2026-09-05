package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// KeyStore 提供基于文件或集中式 SecretWatcher 的 API Key 热重载能力。
// 后台 goroutine 每 5 秒检查文件 mtime（文件模式），或监听 SecretWatcher 事件通道（集中式模式）。
// 复用 pkg/tlsutil/whitelist.go 的轮询模式。
type KeyStore struct {
	mu          sync.RWMutex
	keys        map[string]*KeyConfig
	path        string
	stopCh      chan struct{}
	stopOnce    sync.Once
	lastModTime time.Time
	version     versionCounter // 每次重载/更新递增，供 Aggregator 判定快照是否需要重建
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

// NewKeyStoreWithWatcher 创建由集中式 SecretWatcher（如 K8s Secret、Vault）事件驱动的 KeyStore。
func NewKeyStoreWithWatcher(ctx context.Context, watcher SecretWatcher, secretName string, initialContent string) (*KeyStore, error) {
	if watcher == nil {
		return nil, errors.New("watcher must not be nil")
	}

	ks := &KeyStore{
		path:   "secret-manager:" + secretName,
		stopCh: make(chan struct{}),
	}

	if strings.TrimSpace(initialContent) != "" {
		if err := ks.ReloadContent(initialContent); err != nil {
			return nil, fmt.Errorf("initial secret load: %w", err)
		}
	} else {
		ks.keys = make(map[string]*KeyConfig)
	}

	events, err := watcher.Watch(ctx, secretName)
	if err != nil {
		return nil, fmt.Errorf("start secret watcher for %q: %w", secretName, err)
	}

	go func() {
		for {
			select {
			case <-ks.stopCh:
				return
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					slog.Warn("KeyStore: secret watcher channel closed", "secret", secretName)
					return
				}
				if ev.Err != nil {
					slog.Error("KeyStore: watcher event error", "secret", secretName, "error", ev.Err)
					continue
				}
				if err := ks.ReloadContent(ev.Content); err != nil {
					slog.Error("KeyStore: failed to reload content from watcher", "secret", secretName, "error", err)
				} else {
					slog.Info("KeyStore: keys reloaded via SecretWatcher", "secret", secretName, "version", ev.Version)
				}
			}
		}
	}()

	return ks, nil
}

// Keys 返回当前 API Key 映射的只读快照。
func (ks *KeyStore) Keys() map[string]*KeyConfig {
	if ks == nil {
		return nil
	}
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	result := make(map[string]*KeyConfig, len(ks.keys))
	for k, v := range ks.keys {
		result[k] = v
	}
	return result
}

// LiveKeys 实现 LiveKeySource：返回当前文件/Secret 密钥快照（与 Keys 等价，语义上供聚合器使用）。
func (ks *KeyStore) LiveKeys() map[string]*KeyConfig { return ks.Keys() }

// Version 实现 LiveKeySource：返回密钥集变更版本号（重载/编程更新时递增）。
func (ks *KeyStore) Version() uint64 {
	if ks == nil {
		return 0
	}
	return ks.version.get()
}

// Close 停止后台轮询 goroutine。
func (ks *KeyStore) Close() {
	if ks == nil {
		return
	}
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
	ks.version.bump()

	slog.Info("KeyStore: loaded keys", "path", ks.path, "count", len(keys))
	return nil
}

// ReloadContent 从内存文本内容重新加载 API Key（支持 SecretWatcher 事件推送与动态注入）。
func (ks *KeyStore) ReloadContent(content string) error {
	keys := ParseAPIKeysEnv(strings.TrimSpace(content))
	ks.mu.Lock()
	ks.keys = keys
	ks.lastModTime = time.Now()
	ks.mu.Unlock()
	ks.version.bump()
	slog.Info("KeyStore: content reloaded", "source", ks.path, "count", len(keys))
	return nil
}

// UpdateKeys 直接原子替换当前 Key 映射。
func (ks *KeyStore) UpdateKeys(newKeys map[string]*KeyConfig) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	copied := make(map[string]*KeyConfig, len(newKeys))
	for k, v := range newKeys {
		copied[k] = v
	}
	ks.keys = copied
	ks.lastModTime = time.Now()
	ks.version.bump()
	slog.Info("KeyStore: keys updated programmatically", "source", ks.path, "count", len(copied))
}
