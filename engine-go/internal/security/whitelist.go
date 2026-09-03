// Package security — mTLS CN 白名单管理（热重载）。
//
// 对齐 Python engine/security/whitelist.py：
//   - 基于 YAML 配置文件的 CN 白名单管理
//   - 每个 CN 独立 scope 控制（最小权限原则）
//   - 基于文件 mtime 的热重载（请求驱动，被动检查）
//   - 线程安全的两阶段提交
//   - 向后兼容环境变量静态列表
package security

import (
	"sync"
	"time"

	pkgtlsutil "github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"
)

// CNEntry 表示白名单中的单个 CN 条目。
type CNEntry struct {
	CN          string   `yaml:"cn"`
	Scopes      []string `yaml:"scopes"`
	Description string   `yaml:"description"`
	Enabled     bool     `yaml:"enabled"`
}

// WhitelistManager 线程安全的 mTLS CN 白名单管理器，委托给 pkg/tlsutil.DynamicWhitelist。
type WhitelistManager struct {
	configPath    string
	staticCNs     []string
	dw            *pkgtlsutil.DynamicWhitelist
	mu            sync.RWMutex
	lastLoadTime  time.Time
	loadError     string
	defaultScopes []string
}

// NewWhitelistManager 创建白名单管理器（委托给 pkg/tlsutil）。
// configPath 非空时从 YAML 文件加载并启动 5s 自动热重载；否则使用 staticCNs 构建静态列表。
func NewWhitelistManager(configPath string, staticCNs []string) *WhitelistManager {
	m := &WhitelistManager{
		configPath: configPath,
		staticCNs:  staticCNs,
	}
	m.load()
	return m
}

// load 加载或重载白名单配置。
func (m *WhitelistManager) load() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.configPath == "" {
		m.dw = pkgtlsutil.NewStaticWhitelist(m.staticCNs)
		m.lastLoadTime = time.Now()
		m.loadError = ""
		m.defaultScopes = nil
		return
	}

	dw, err := pkgtlsutil.NewDynamicWhitelist(m.configPath)
	if err != nil {
		m.loadError = err.Error()
		return
	}
	if m.dw != nil {
		m.dw.Close()
	}
	m.dw = dw
	m.lastLoadTime = time.Now()
	m.loadError = ""
}

// GetEntry 查找 CN 白名单条目。
func (m *WhitelistManager) GetEntry(cn string) *CNEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.dw == nil {
		return nil
	}
	m.dw.CheckReload()
	scopes, ok := m.dw.GetScopes(cn)
	if !ok {
		return nil
	}
	return &CNEntry{
		CN:      cn,
		Scopes:  scopes,
		Enabled: true,
	}
}

// GetScopes 返回 CN 的 scope 列表。未找到返回 nil。
func (m *WhitelistManager) GetScopes(cn string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.dw == nil {
		return nil
	}
	m.dw.CheckReload()
	scopes, ok := m.dw.GetScopes(cn)
	if !ok {
		return nil
	}
	return scopes
}

// IsAllowed 检查 CN 是否在白名单中。
func (m *WhitelistManager) IsAllowed(cn string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.dw == nil {
		return false
	}
	m.dw.CheckReload()
	return m.dw.IsAuthorized(cn)
}

// DefaultScopes 返回未知 CN 的默认 scope（空 = fail-closed）。
func (m *WhitelistManager) DefaultScopes() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.dw == nil {
		return nil
	}
	m.dw.CheckReload()
	return m.dw.DefaultScopes()
}

// AllEntries 返回所有活跃白名单条目的快照。
func (m *WhitelistManager) AllEntries() []CNEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.dw == nil {
		return nil
	}
	clients := m.dw.AuthorizedClients()
	entries := make([]CNEntry, 0, len(clients))
	for cn, scopes := range clients {
		entries = append(entries, CNEntry{
			CN:      cn,
			Scopes:  scopes,
			Enabled: true,
		})
	}
	return entries
}

// LastLoadTime 返回最近一次成功加载的时间。
func (m *WhitelistManager) LastLoadTime() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastLoadTime
}

// LastError 返回最近一次加载的错误信息。
func (m *WhitelistManager) LastError() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loadError
}

// Reload 强制重载白名单配置。
func (m *WhitelistManager) Reload() {
	m.load()
}

// Close 释放内部资源。
func (m *WhitelistManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dw != nil {
		m.dw.Close()
		m.dw = nil
	}
}

// ──────────────────────────────────────────────
// 模块级单例
// ──────────────────────────────────────────────

var (
	whitelistManager     *WhitelistManager
	whitelistManagerOnce sync.Once
)

// GetWhitelistManager 获取模块级 WhitelistManager 单例。
func GetWhitelistManager() *WhitelistManager {
	whitelistManagerOnce.Do(func() {
		settings := GetSettings()
		whitelistFile := settings.MTLSWhitelistFile
		if whitelistFile != "" {
			whitelistManager = NewWhitelistManager(whitelistFile, nil)
		} else {
			whitelistManager = NewWhitelistManager("", settings.MTLSAllowedCNs)
		}
	})
	return whitelistManager
}

// ResetWhitelistManager 重置单例（仅测试用）。
func ResetWhitelistManager() {
	whitelistManagerOnce = sync.Once{}
	whitelistManager = nil
}
