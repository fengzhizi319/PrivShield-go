// Package auth — 活密钥聚合与 Token 摘要工具。
//
// 背景：认证内核（AuthenticateAPIKey）在**每个请求**上都要拿到「当前生效的密钥全集」。
// 若每次都从 KeyStore（文件/Secret 热轮转）与 UserStore（动态用户密钥/登录会话）各自拷贝一遍
// 再合并，热路径上会产生 2~3 次 map 全量分配与 O(n) 复制；密钥规模上千时认证延迟与 GC 压力
// 会明显放大。本文件提供版本号驱动的缓存聚合器：仅当任一来源发生变更时才重建快照，
// 其余请求复用同一份只读快照（零分配）。
package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
)

// HashToken 返回 Token 的 SHA-256 十六进制摘要。
//
// 用途：动态用户密钥在持久化文件中**只保存摘要**（等保三级 G-14 / 密评：敏感凭证不以明文长期
// 存储），认证时对来访 Token 计算一次摘要后走同一套常量时间比对。Token 本身为 128-bit 密码学
// 随机串，摘要不可逆推，落盘文件泄露不等于凭证泄露。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// LiveKeySource 是活密钥来源抽象，由 KeyStore 与 UserStore 实现。
type LiveKeySource interface {
	// LiveKeys 返回当前有效的「Token 明文 → KeyConfig」快照，实现必须返回深拷贝。
	LiveKeys() map[string]*KeyConfig
	// Version 返回单调递增的变更版本号；未变更时聚合器直接复用缓存快照。
	Version() uint64
}

// HashedLiveKeySource 是「只落盘摘要」的活密钥来源抽象（UserStore 实现）。
type HashedLiveKeySource interface {
	// LiveHashedKeys 返回「HashToken(token) → KeyConfig」快照，实现必须返回深拷贝。
	LiveHashedKeys() map[string]*KeyConfig
	// Version 语义同 LiveKeySource（与明文活密钥共用同一版本号）。
	Version() uint64
}

// Aggregator 以版本号驱动缓存，聚合静态密钥与多个活密钥来源。
//
// 合并优先级：后注册的来源覆盖先注册的来源，静态密钥优先级最低（与历史「动态存储为准」一致）。
// 并发语义：Keys() 返回的 map 为**只读共享快照**，调用方不得写入；快照仅在版本变化时被整体替换。
type Aggregator struct {
	static  map[string]*KeyConfig
	sources []LiveKeySource

	mu       sync.Mutex
	versions []uint64
	cached   map[string]*KeyConfig
}

// NewAggregator 创建聚合器。static 为启动期快照（可为 nil），sources 为活密钥来源（可含 nil，自动忽略）。
func NewAggregator(static map[string]*KeyConfig, sources ...LiveKeySource) *Aggregator {
	kept := make([]LiveKeySource, 0, len(sources))
	for _, s := range sources {
		if s != nil {
			kept = append(kept, s)
		}
	}
	return &Aggregator{
		static:   static,
		sources:  kept,
		versions: make([]uint64, len(kept)),
	}
}

// Keys 返回当前生效的密钥全集快照（**只读共享**）。仅在任一来源版本变化时重建，否则零分配复用。
//
// 并发约定：调用方不得写入返回的 map 或其中的 *KeyConfig（认证内核仅做只读比对）。
// 快照与来源内部状态相互独立：静态密钥在重建时按值深拷贝，活密钥来源按接口约定返回深拷贝，
// 因此任一方的后续变更都不会就地污染另一方的数据。
func (a *Aggregator) Keys() map[string]*KeyConfig {
	if a == nil {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.sources) == 0 {
		if a.cached == nil {
			a.cached = copyKeyMap(a.static)
		}
		return a.cached
	}

	current := make([]uint64, len(a.sources))
	for i, s := range a.sources {
		current[i] = s.Version()
	}
	if a.cached != nil && equalVersions(a.versions, current) {
		return a.cached
	}

	merged := copyKeyMap(a.static)
	for _, s := range a.sources {
		for k, v := range s.LiveKeys() {
			merged[k] = v
		}
	}
	a.cached = merged
	copy(a.versions, current)
	return merged
}

// Size 返回当前快照中的密钥数量（诊断用）。
func (a *Aggregator) Size() int {
	return len(a.Keys())
}

func equalVersions(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// copyKeyMap 深拷贝静态密钥映射，使聚合快照与配置源（cfg.ScopeKeys 等）彻底解耦。
// 仅在重建快照时调用（低频），不影响热路径的零分配复用。
func copyKeyMap(src map[string]*KeyConfig) map[string]*KeyConfig {
	dst := make(map[string]*KeyConfig, len(src))
	for k, v := range src {
		dst[k] = v.Clone()
	}
	return dst
}

// versionCounter 提供来源侧的版本号自增能力（KeyStore / UserStore 内嵌使用）。
type versionCounter struct {
	v atomic.Uint64
}

func (c *versionCounter) bump() { c.v.Add(1) }

func (c *versionCounter) get() uint64 { return c.v.Load() }
