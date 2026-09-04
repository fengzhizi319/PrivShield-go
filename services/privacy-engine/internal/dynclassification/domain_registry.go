// Package dynclassification — 领域策略与脱敏回调注册表。
//
// 对齐 Python engine/dynclassification/domain_registry.py：
// 实现通用分类引擎与领域规则（medical/finance/hr 等）之间的解耦。
// 遵循依赖倒置原则 (DIP) 与策略模式 (Strategy Pattern)。
package dynclassification

import (
	"fmt"
	"strings"
	"sync"
)

// TextSanitizerCallback 领域文本脱敏回调函数签名。
// 参数：(fieldName, text, finalLevel, mode) → sanitized text
type TextSanitizerCallback func(fieldName, text, finalLevel, mode string) string

// DomainStrategyRegistry 领域策略与回调注册表（线程安全）。
type DomainStrategyRegistry struct {
	mu         sync.RWMutex
	sanitizers map[string]TextSanitizerCallback
}

// NewDomainStrategyRegistry 创建空的领域注册表。
func NewDomainStrategyRegistry() *DomainStrategyRegistry {
	return &DomainStrategyRegistry{
		sanitizers: make(map[string]TextSanitizerCallback),
	}
}

// RegisterSanitizer 注册指定领域的文本脱敏回调函数。
func (r *DomainStrategyRegistry) RegisterSanitizer(domain string, sanitizer TextSanitizerCallback) error {
	if domain == "" || sanitizer == nil {
		return fmt.Errorf("domain name must be non-empty and sanitizer must be non-nil")
	}
	key := strings.ToLower(strings.TrimSpace(domain))
	r.mu.Lock()
	r.sanitizers[key] = sanitizer
	r.mu.Unlock()
	return nil
}

// UnregisterSanitizer 解绑指定领域的文本脱敏回调。
func (r *DomainStrategyRegistry) UnregisterSanitizer(domain string) bool {
	key := strings.ToLower(strings.TrimSpace(domain))
	r.mu.Lock()
	_, exists := r.sanitizers[key]
	delete(r.sanitizers, key)
	r.mu.Unlock()
	return exists
}

// GetSanitizer 获取指定领域的文本脱敏回调函数。未注册返回 nil。
func (r *DomainStrategyRegistry) GetSanitizer(domain string) TextSanitizerCallback {
	key := strings.ToLower(strings.TrimSpace(domain))
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sanitizers[key]
}

// HasSanitizer 检查指定领域是否已注册回调。
func (r *DomainStrategyRegistry) HasSanitizer(domain string) bool {
	return r.GetSanitizer(domain) != nil
}

// RegisteredDomains 返回所有已注册的领域名称列表。
func (r *DomainStrategyRegistry) RegisteredDomains() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	domains := make([]string, 0, len(r.sanitizers))
	for d := range r.sanitizers {
		domains = append(domains, d)
	}
	return domains
}

// ──────────────────────────────────────────────
// 全局单例
// ──────────────────────────────────────────────

var (
	globalRegistry     *DomainStrategyRegistry
	globalRegistryOnce sync.Once
)

// GetGlobalRegistry 获取全局领域注册表单例。
func GetGlobalRegistry() *DomainStrategyRegistry {
	globalRegistryOnce.Do(func() {
		globalRegistry = NewDomainStrategyRegistry()
	})
	return globalRegistry
}

// ResetGlobalRegistry 重置全局单例（仅测试用）。
func ResetGlobalRegistry() {
	globalRegistryOnce = sync.Once{}
	globalRegistry = nil
}
