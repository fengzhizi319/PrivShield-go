// Package tlsutil — dynamic mTLS CN whitelist with hot-reload support.
// Package tlsutil — 动态 mTLS 客户端 CN（Common Name）白名单与热重载管理组件。
//
// ==============================================================================
// 【核心能力与架构设计】
// 1. 【细粒度 Scope 访问控制 (Per-CN Method Scope)】：
//    从 YAML 配置文件加载客户端证书 CN 及其允许调用的 RPC 方法集合（如 `["/PrivacyService/Process"]`
//    或通配符 `["*"]` / `["/AuditLog/*"]`）；
// 2. 【5 秒无依赖热重载 (Zero-Dependency Hot-Reload)】：
//    后台独立协程每 5 秒通过 os.Stat 轮询配置文件的修改时间（ModTime），
//    在检测到文件被编辑或 ConfigMap 挂载更新时自动触发安全重载，无需重启进程或引入 fsnotify 外部依赖；
// 3. 【读写锁并发保护 (RWMutex Concurrency)】：
//    在鉴权热路径（CheckScope / IsAuthorized）上使用读锁（RLock）保证微秒级高性能并发查询，
//    在配置重载（reload）时使用写锁（Lock）实现原子全量替换，消除读写数据竞争；
// 4. 【双格式向下兼容】：
//    同时支持规范标准格式（`clients: [{cn, allowed_scopes}]`）与早期历史格式（`entries: [{cn, scopes}]`）。
//
// ==============================================================================
// 【白名单配置文件格式范例 (config/mtls-whitelist.yaml)】
//
//	version: "1.0"
//	clients:
//	  - cn: "service-hub.privshield.internal"
//	    allowed_scopes:
//	      - "/PrivacyService/Process"
//	      - "/AuditLog/*"
//	    role: "orchestrator"
//	    description: "数据服务调度中枢核心客户端"
//	    enabled: true
//	  - cn: "bff-go.privshield.internal"
//	    allowed_scopes: ["*"]
//	    role: "gateway"
//	    enabled: true
// ==============================================================================

package tlsutil

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// WhitelistClient represents a single CN entry in the whitelist config.
// WhitelistClient 表示白名单配置文件中的单个客户端 CN 条目。
type WhitelistClient struct {
	// CN 为客户端证书的 Subject Common Name（必填）。
	CN string `yaml:"cn"`

	// AllowedScopes 为该客户端允许调用的 gRPC 全路径方法列表（如 ["/PrivacyService/Process", "/AuditLog/*"]）。
	AllowedScopes []string `yaml:"allowed_scopes"`

	// Role 为客户端的角色标识（可选，如 "orchestrator", "gateway"）。
	Role string `yaml:"role,omitempty"`

	// Description 为条目的可读性描述信息（可选）。
	Description string `yaml:"description,omitempty"`

	// Enabled 表示是否启用该条目（nil 或 true 表示启用，false 表示临时禁用）。
	Enabled *bool `yaml:"enabled,omitempty"`
}

// WhitelistConfig represents the top-level whitelist YAML configuration.
// WhitelistConfig 表示白名单 YAML 配置文件的顶层根结构。
//
// Supports two YAML key formats for backward compatibility:
//   - "clients" (design doc standard): uses "allowed_scopes" field
//   - "entries" (legacy format): uses "scopes" field
type WhitelistConfig struct {
	Version string            `yaml:"version"`
	Clients []WhitelistClient `yaml:"clients"`
	Entries []struct {
		CN          string   `yaml:"cn"`
		Scopes      []string `yaml:"scopes"`
		Description string   `yaml:"description,omitempty"`
		Enabled     *bool    `yaml:"enabled,omitempty"`
	} `yaml:"entries"`
}

// DynamicWhitelist manages a hot-reloadable CN → scopes mapping.
// DynamicWhitelist 管理线程安全、可热重载的客户端 CN 到 AllowedScopes 的映射字典。
type DynamicWhitelist struct {
	mu      sync.RWMutex
	clients map[string][]string // CN → allowed scopes 切片
	path    string              // 配置文件物理路径

	// 轮询与停机状态
	stopCh  chan struct{}
	stopped bool
	stopMu  sync.Mutex
}

// NewDynamicWhitelist creates a whitelist from a YAML file and starts background polling.
//
// NewDynamicWhitelist 从指定路径加载 YAML 白名单文件并拉起后台 5 秒轮询重载协程：
// 1. 立即同步执行首次 reload 解析配置文件；
// 2. 若解析成功，在后台启动 poll 协程持续监听文件 mtime 变更；
// 3. 服务停机时应调用 Close() 优雅释放轮询协程。
func NewDynamicWhitelist(path string) (*DynamicWhitelist, error) {
	cleanPath := filepath.Clean(path)
	dw := &DynamicWhitelist{
		clients: make(map[string][]string),
		path:    cleanPath,
		stopCh:  make(chan struct{}),
	}
	if err := dw.reload(); err != nil {
		return nil, err
	}
	go dw.poll()
	return dw, nil
}

// reload reads and parses the YAML whitelist configuration file.
//
// reload 在写锁保护下读取并解析 YAML 白名单：
// 1. 读取配置文件内容并反序列化为 WhitelistConfig；
// 2. 优先遍历 clients 列表，过滤掉 enabled=false 的条目；
// 3. 若 clients 为空，回退遍历 entries 列表（向下兼容）；
// 4. 获取写锁全量原子更新 dw.clients 映射表并记录日志。
func (dw *DynamicWhitelist) reload() error {
	data, err := os.ReadFile(dw.path)
	if err != nil {
		return err
	}
	var conf WhitelistConfig
	if err := yaml.Unmarshal(data, &conf); err != nil {
		return err
	}

	newClients := make(map[string][]string)

	// Priority 1: "clients" key (design doc standard) / 优先使用 "clients" 键
	for _, c := range conf.Clients {
		if c.Enabled != nil && !*c.Enabled {
			continue
		}
		newClients[c.CN] = c.AllowedScopes
	}

	// Priority 2: "entries" key (legacy format) / 回退到 "entries" 键
	if len(newClients) == 0 {
		for _, e := range conf.Entries {
			if e.Enabled != nil && !*e.Enabled {
				continue
			}
			newClients[e.CN] = e.Scopes
		}
	}

	dw.mu.Lock()
	defer dw.mu.Unlock()
	dw.clients = newClients
	log.Printf("[mTLS Whitelist] Reloaded %d authorized CN entries from %s", len(dw.clients), dw.path)
	return nil
}

// poll watches for file changes by polling modification time.
//
// poll 以后台协程方式运行，每隔 5 秒通过 os.Stat 检测配置文件最后修改时间：
// - 若检测到 ModTime 晚于上次记录的时间戳，触发 reload；
// - 接收到 stopCh 信号时退出循环。
func (dw *DynamicWhitelist) poll() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var lastModTime time.Time
	if info, err := os.Stat(dw.path); err == nil {
		lastModTime = info.ModTime()
	}

	for {
		select {
		case <-ticker.C:
			info, err := os.Stat(dw.path)
			if err != nil {
				continue
			}
			if info.ModTime().After(lastModTime) {
				lastModTime = info.ModTime()
				if err := dw.reload(); err != nil {
					log.Printf("[mTLS Whitelist] Reload error: %v", err)
				}
			}
		case <-dw.stopCh:
			return
		}
	}
}

// Close stops the background file polling goroutine.
//
// Close 安全停止后台轮询协程，支持多次重复调用（幂等保护）。
func (dw *DynamicWhitelist) Close() {
	dw.stopMu.Lock()
	defer dw.stopMu.Unlock()
	if !dw.stopped {
		close(dw.stopCh)
		dw.stopped = true
	}
}

// IsAuthorized checks whether a client CN is present in the whitelist.
//
// IsAuthorized 快速查询客户端证书 CN 是否登记在白名单中（在读锁保护下执行）。
func (dw *DynamicWhitelist) IsAuthorized(clientCN string) bool {
	dw.mu.RLock()
	defer dw.mu.RUnlock()
	_, exists := dw.clients[clientCN]
	return exists
}

// CheckScope checks whether a client CN is authorized for a specific method/scope.
//
// CheckScope 检查客户端 CN 是否被授权访问指定的 gRPC 方法全名：
//
// ==============================================================================
// 【Scope 匹配规则】
// 1. 【全局通配符 ("*")】：若配置包含 `*`，允许访问所有方法；
// 2. 【精确全名匹配 (Exact Match)】：配置字符串与 method 完全一致（如 `/PrivacyService/Process`）；
// 3. 【前缀通配符 (Prefix Wildcard)】：如 `/AuditLog/*` 可匹配 `/AuditLog/RecordAudit`、`/AuditLog/ListLogs` 等；
// 4. 【返回值】：(authorized: 是否通过, scopes: 该 CN 拥有的全部 scope 列表)。
// ==============================================================================
func (dw *DynamicWhitelist) CheckScope(clientCN string, method string) (bool, []string) {
	dw.mu.RLock()
	defer dw.mu.RUnlock()

	scopes, exists := dw.clients[clientCN]
	if !exists {
		return false, nil
	}

	for _, s := range scopes {
		if s == "*" || s == method || matchScopePattern(s, method) {
			return true, scopes
		}
	}
	return false, scopes
}

// GetScopes returns the allowed scopes for a given CN.
//
// GetScopes 返回指定客户端 CN 的所有已授权 Scope 列表副本。
func (dw *DynamicWhitelist) GetScopes(clientCN string) ([]string, bool) {
	dw.mu.RLock()
	defer dw.mu.RUnlock()
	scopes, exists := dw.clients[clientCN]
	return scopes, exists
}

// matchScopePattern performs simple wildcard pattern matching.
//
// matchScopePattern 针对通配符模式执行字符串匹配：
// - `*`：匹配任意方法；
// - `/ServiceHub/*`：匹配所有以 `/ServiceHub/` 为前缀的方法名；
// - 其他：执行精确字符串等值比对。
func matchScopePattern(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	// 对 "/ServiceHub/*" 等模式执行高效前缀匹配
	if len(pattern) > 2 && pattern[len(pattern)-1] == '*' && pattern[len(pattern)-2] == '/' {
		prefix := pattern[:len(pattern)-1] // 提取 "/ServiceHub/"
		return len(value) >= len(prefix) && value[:len(prefix)] == prefix
	}
	return pattern == value
}
