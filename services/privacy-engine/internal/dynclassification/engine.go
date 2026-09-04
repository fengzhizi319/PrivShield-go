// Package dynclassification 提供三层动态分类分级引擎。
//
// Layer 1: Aho-Corasick 自动机 + 字段名正则快速匹配（零 ML 开销）
// Layer 2: Small-NER 实体识别（可选，ONNX Runtime）
// Layer 3: LLM/VLM 仲裁（可选，CUDA 推理）
//
// 本文件实现 Layer 1 规则引擎核心。
package dynclassification

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

// ──────────────────────────────────────────────
// 类型定义
// ──────────────────────────────────────────────

// SecurityLevel 安全等级
type SecurityLevel string

const (
	LevelPublic       SecurityLevel = "public"
	LevelInternal     SecurityLevel = "internal"
	LevelConfidential SecurityLevel = "confidential"
	LevelSecret       SecurityLevel = "secret"
	LevelTopSecret    SecurityLevel = "top_secret"
)

// ClassificationResult 分类结果
type ClassificationResult struct {
	Field string `json:"field"`
	Value string `json:"value,omitempty"`
	// Level 是引擎内部 canonical 词表（public/internal/confidential/secret/top_secret）或
	// 规则文件原始 L 形式，取决于来源；LevelID 始终是可跨服务消费的 L1~L5 标识。
	Level      SecurityLevel `json:"level"`
	LevelID    string        `json:"level_id,omitempty"`
	Category   string        `json:"category"`
	Confidence float64       `json:"confidence"`
	MatchedBy  string        `json:"matched_by"` // "rule:<id>" | "ner" | "llm"
}

// RuleDef 规则定义
type RuleDef struct {
	ID            string        `yaml:"id"`
	Level         SecurityLevel `yaml:"level"`
	Category      string        `yaml:"category"`
	FieldPatterns []string      `yaml:"field_patterns,omitempty"` // 字段名正则
	ValuePatterns []string      `yaml:"value_patterns,omitempty"` // 值内容正则（AC 自动机）
	Description   string        `yaml:"description,omitempty"`
}

// ──────────────────────────────────────────────
// Aho-Corasick 自动机实现
// ──────────────────────────────────────────────

// ACNode AC 自动机节点
type ACNode struct {
	children map[rune]*ACNode
	fail     *ACNode
	output   []string // 匹配到的模式 ID 列表
	isEnd    bool
}

// ACAutomaton Aho-Corasick 自动机
type ACAutomaton struct {
	root     *ACNode
	patterns map[string]*regexp.Regexp // 模式 ID → 正则
	mu       sync.RWMutex
}

// NewACAutomaton 创建 AC 自动机实例
func NewACAutomaton() *ACAutomaton {
	return &ACAutomaton{
		root: &ACNode{
			children: make(map[rune]*ACNode),
		},
		patterns: make(map[string]*regexp.Regexp),
	}
}

// AddPattern 添加匹配模式
func (ac *ACAutomaton) AddPattern(id, pattern string) error {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	// 编译正则
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	ac.patterns[id] = re

	// 插入 Trie（使用字面量字符序列）
	node := ac.root
	for _, ch := range pattern {
		if node.children[ch] == nil {
			node.children[ch] = &ACNode{
				children: make(map[rune]*ACNode),
			}
		}
		node = node.children[ch]
	}
	node.isEnd = true
	node.output = append(node.output, id)
	return nil
}

// Build 构建失败指针（BFS）
func (ac *ACAutomaton) Build() {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	queue := []*ACNode{}
	// 根节点的子节点 fail 指向根
	for _, child := range ac.root.children {
		child.fail = ac.root
		queue = append(queue, child)
	}

	// BFS 构建 fail 指针
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for ch, child := range curr.children {
			queue = append(queue, child)
			// 沿 fail 链查找
			fail := curr.fail
			for fail != nil && fail.children[ch] == nil {
				fail = fail.fail
			}
			if fail == nil {
				child.fail = ac.root
			} else {
				child.fail = fail.children[ch]
				// 合并输出
				child.output = append(child.output, child.fail.output...)
			}
		}
	}
}

// Search 在文本中搜索匹配模式
func (ac *ACAutomaton) Search(text string) []string {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	var matches []string
	node := ac.root
	for _, ch := range text {
		for node != ac.root && node.children[ch] == nil {
			node = node.fail
		}
		if node.children[ch] != nil {
			node = node.children[ch]
		}
		// 检查 output 而非 isEnd：Build() 阶段已将 fail 链上游的输出合并到
		// node.output，仅检查 isEnd 会遗漏通过 fail 指针继承的模式匹配。
		if len(node.output) > 0 {
			matches = append(matches, node.output...)
		}
	}
	return matches
}

// ──────────────────────────────────────────────
// 规则引擎
// ──────────────────────────────────────────────

// ruleSnapshot 规则快照（不可变，供 atomic.Pointer 无锁读替换）
type ruleSnapshot struct {
	rules        []RuleDef
	fieldRegexps []*regexp.Regexp
	ac           *ACAutomaton
}

// defaultRulesReloadCheckInterval Classify 热路径 mtime 检测的最小间隔（节流 os.Stat）
const defaultRulesReloadCheckInterval = 5 * time.Second

// RuleEngine 分类规则引擎
type RuleEngine struct {
	snapshot atomic.Pointer[ruleSnapshot] // 原子快照（Classify 无锁读）
	cache    *engineCache                 // 有界分片缓存（替代无界 sync.Map）

	// 热重载支持（mtime 检测模式，与 WhitelistManager 一致）
	rulesPath   string
	lastModTime time.Time
	reloadMu    sync.Mutex

	// mtime 检测节流：将每请求一次 os.Stat 降为每 checkInterval 一次（冷路径文件 IO 不进热路径）
	lastCheckNano atomic.Int64  // 上次检测时间戳（unixnano）
	checkInterval time.Duration // 检测最小间隔；0 表示不节流
}

// NewRuleEngine 创建规则引擎实例
func NewRuleEngine(rules []RuleDef) (*RuleEngine, error) {
	fieldRegexps := make([]*regexp.Regexp, len(rules))
	ac := NewACAutomaton()

	// 编译字段名正则
	for i, rule := range rules {
		if len(rule.FieldPatterns) > 0 {
			// 合并多个模式为单个正则
			combined := strings.Join(rule.FieldPatterns, "|")
			re, err := regexp.Compile(combined)
			if err != nil {
				return nil, err
			}
			fieldRegexps[i] = re
		}

		// 添加值模式到 AC 自动机
		for _, pattern := range rule.ValuePatterns {
			if err := ac.AddPattern(rule.ID, pattern); err != nil {
				return nil, err
			}
		}
	}

	// 构建 AC 自动机
	ac.Build()

	engine := &RuleEngine{checkInterval: defaultRulesReloadCheckInterval}
	if v := os.Getenv("AGENT_RULES_RELOAD_CHECK_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			engine.checkInterval = time.Duration(n) * time.Second
		}
	}

	// 初始化原子快照
	engine.snapshot.Store(&ruleSnapshot{
		rules:        rules,
		fieldRegexps: fieldRegexps,
		ac:           ac,
	})

	// 初始化有界分片缓存
	engine.cache = newEngineCache(10000)
	return engine, nil
}

// Classify 对字段执行分类
func (e *RuleEngine) Classify(field, value string) *ClassificationResult {
	// 被动检查规则文件热重载
	e.checkRulesReload()

	// 检查分片缓存
	cacheKey := field + ":" + value
	if cached, ok := e.cache.get(cacheKey); ok {
		return cached
	}

	// 原子加载规则快照（无锁读，避免与 reload 写端数据竞争）
	snap := e.snapshot.Load()

	// Layer 1: 字段名正则匹配
	for i, re := range snap.fieldRegexps {
		if re != nil && re.MatchString(field) {
			result := &ClassificationResult{
				Field:      field,
				Level:      snap.rules[i].Level,
				Category:   snap.rules[i].Category,
				Confidence: 0.95,
				MatchedBy:  "rule:" + snap.rules[i].ID,
			}
			e.cache.put(cacheKey, result)
			return result
		}
	}

	// Layer 1: AC 自动机值匹配
	matches := snap.ac.Search(value)
	if len(matches) > 0 {
		// 找到第一个匹配的规则
		for _, rule := range snap.rules {
			for _, matchID := range matches {
				if rule.ID == matchID {
					result := &ClassificationResult{
						Field:      field,
						Level:      rule.Level,
						Category:   rule.Category,
						Confidence: 0.90,
						MatchedBy:  "rule:" + rule.ID,
					}
					e.cache.put(cacheKey, result)
					return result
				}
			}
		}
	}

	// 默认分类
	result := &ClassificationResult{
		Field:      field,
		Level:      LevelPublic,
		Category:   "unknown",
		Confidence: 0.50,
		MatchedBy:  "default",
	}
	e.cache.put(cacheKey, result)
	return result
}

// RuleCount 返回已加载的规则数量
func (e *RuleEngine) RuleCount() int {
	if snap := e.snapshot.Load(); snap != nil {
		return len(snap.rules)
	}
	return 0
}

// WatchRules 启用规则文件 mtime 热重载。
// 每次 Classify 调用时被动检查文件 mtime，变更时自动重新编译规则。
// 与 WhitelistManager 的 mtime 检测模式一致，无外部依赖。
func (e *RuleEngine) WatchRules(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	e.rulesPath = path
	e.lastModTime = info.ModTime()
	return nil
}

// checkRulesReload 被动检查规则文件是否变更（请求驱动，无 goroutine）。
// 热路径节流：每 checkInterval 最多执行一次 os.Stat，避免每请求 syscall。
func (e *RuleEngine) checkRulesReload() {
	if e.rulesPath == "" {
		return
	}
	if e.checkInterval > 0 {
		now := time.Now().UnixNano()
		last := e.lastCheckNano.Load()
		if now-last < int64(e.checkInterval) {
			return
		}
		if !e.lastCheckNano.CompareAndSwap(last, now) {
			return // 已有其他 goroutine 在执行本轮检测
		}
	}
	info, err := os.Stat(e.rulesPath)
	if err != nil {
		return
	}
	if !info.ModTime().After(e.lastModTime) {
		return
	}

	e.reloadMu.Lock()
	defer e.reloadMu.Unlock()

	// 双重检查（避免并发重复加载）
	info2, err := os.Stat(e.rulesPath)
	if err != nil || !info2.ModTime().After(e.lastModTime) {
		return
	}

	// 从文件重新加载规则（修复：之前使用旧规则重建，实际不会更新规则内容）
	newRules, err := LoadRulesFromDir(filepath.Dir(e.rulesPath))
	if err != nil || len(newRules) == 0 {
		return // 加载失败保持旧规则
	}

	newEngine, err := NewRuleEngine(newRules)
	if err != nil {
		return // 编译失败保持旧规则
	}

	// 原子替换快照（reloadMu 保护写端，Classify 通过 atomic.Pointer 无锁读）
	newSnap := newEngine.snapshot.Load()
	e.snapshot.Store(newSnap)
	e.lastModTime = info2.ModTime()
	// 重建缓存（规则变更旧缓存失效）
	e.cache = newEngineCache(10000)
}

// ClassifyBatch 批量分类（多核并发分块加速）
func (e *RuleEngine) ClassifyBatch(records []map[string]string) []*ClassificationResult {
	n := len(records)
	if n == 0 {
		return nil
	}

	if n <= 32 {
		var results []*ClassificationResult
		for _, record := range records {
			for field, value := range record {
				results = append(results, e.Classify(field, value))
			}
		}
		return results
	}

	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers > 16 {
		numWorkers = 16
	}
	if numWorkers > n {
		numWorkers = n
	}

	chunkSize := (n + numWorkers - 1) / numWorkers
	workerResults := make([][]*ClassificationResult, numWorkers)
	var wg sync.WaitGroup

	for w := 0; w < numWorkers; w++ {
		startIdx := w * chunkSize
		endIdx := startIdx + chunkSize
		if endIdx > n {
			endIdx = n
		}
		if startIdx >= endIdx {
			break
		}

		wg.Add(1)
		go func(workerID, start, end int) {
			defer wg.Done()
			var local []*ClassificationResult
			for i := start; i < end; i++ {
				for field, value := range records[i] {
					local = append(local, e.Classify(field, value))
				}
			}
			workerResults[workerID] = local
		}(w, startIdx, endIdx)
	}
	wg.Wait()

	totalCount := 0
	for _, res := range workerResults {
		totalCount += len(res)
	}
	allResults := make([]*ClassificationResult, 0, totalCount)
	for _, res := range workerResults {
		allResults = append(allResults, res...)
	}
	return allResults
}

// LoadRulesFromDir 从指定目录遍历加载所有 YAML/YML 领域规则文件
func LoadRulesFromDir(dir string) ([]RuleDef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var allRules []RuleDef
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var fileContent struct {
			Rules []RuleDef `yaml:"rules"`
		}
		if err := yaml.Unmarshal(data, &fileContent); err == nil && len(fileContent.Rules) > 0 {
			allRules = append(allRules, fileContent.Rules...)
		}
	}
	return allRules, nil
}

// ──────────────────────────────────────────────
// 有界分片缓存（替代无界 sync.Map，防止内存无限增长）
// ──────────────────────────────────────────────

const engineCacheNumShards = 16

type engineCacheShard struct {
	mu       sync.Mutex
	items    map[string]*ClassificationResult
	capacity int
}

type engineCache struct {
	shards [engineCacheNumShards]*engineCacheShard
}

func newEngineCache(totalCapacity int) *engineCache {
	if totalCapacity <= 0 {
		totalCapacity = 10000
	}
	shardCap := (totalCapacity + engineCacheNumShards - 1) / engineCacheNumShards
	c := &engineCache{}
	for i := 0; i < engineCacheNumShards; i++ {
		c.shards[i] = &engineCacheShard{
			items:    make(map[string]*ClassificationResult, shardCap),
			capacity: shardCap,
		}
	}
	return c
}

func (c *engineCache) shardFor(key string) *engineCacheShard {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return c.shards[h%engineCacheNumShards]
}

func (c *engineCache) get(key string) (*ClassificationResult, bool) {
	shard := c.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	r, ok := shard.items[key]
	return r, ok
}

func (c *engineCache) put(key string, val *ClassificationResult) {
	shard := c.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	// 分片满时优先淘汰低价值条目（default/低置信度），保留热规则命中结果
	if len(shard.items) >= shard.capacity {
		target := shard.capacity / 2
		count := 0
		// Phase 1: 先淘汰 MatchedBy=="default" 或 Confidence < 0.6 的低价值条目
		for k, v := range shard.items {
			if v != nil && (v.MatchedBy == "default" || v.Confidence < 0.6) {
				delete(shard.items, k)
				count++
				if count >= target {
					break
				}
			}
		}
		// Phase 2: 若仍不够，回退随机淘汰补足
		if count < target {
			for k := range shard.items {
				delete(shard.items, k)
				count++
				if count >= target {
					break
				}
			}
		}
	}
	shard.items[key] = val
}
