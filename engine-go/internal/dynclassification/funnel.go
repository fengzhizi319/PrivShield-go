// Package dynclassification 提供三层动态分类分级漏斗（Rule Engine → Small-NER → External LLM Arbitration）。
//
// 架构设计（设计文档 §3）：
//  1. Layer 1（规则层）：基于 Aho-Corasick 自动机与字段名正则快速匹配（零 ML 开销，< 50μs）；
//  2. Layer 2（NER 实体层）：Small-NER 实体抽取（识别姓名、证件、电话、住址、疾病诊断、ICD-10 等）；
//  3. Layer 3（LLM 仲裁层）：通过 HTTP 连接池调度外部独立 LLM（vLLM / Ollama），无需内嵌 PyTorch；
//     升级载荷「只送特征、不送原值」（P0-5）：仅字段名 + 值形态指纹 + 前层候选标签，
//     且端点必须为 https 或环回明文，否则 fail closed 回退 Safety Floor；
//  4. Safety Floor（安全底线）：全链路异常/超时时的 Fail-closed 安全托底。
package dynclassification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// FunnelConfig 分类漏斗配置
type FunnelConfig struct {
	RuleConfidenceThreshold float64       // Layer 1 规则置信度阈值 (默认 0.85)
	NERConfidenceThreshold  float64       // Layer 2 NER 置信度阈值 (默认 0.80)
	EnableNER               bool          // 是否开启 Layer 2 NER
	EnableLLM               bool          // 是否开启 Layer 3 LLM 外部仲裁
	LLMTimeout              time.Duration // LLM 仲裁单次超时时间
}

// DefaultFunnelConfig 默认漏斗配置
func DefaultFunnelConfig() FunnelConfig {
	return FunnelConfig{
		RuleConfidenceThreshold: 0.85,
		NERConfidenceThreshold:  0.80,
		EnableNER:               true,
		EnableLLM:               false, // 按需通过配置或环境变量开启
		LLMTimeout:              5 * time.Second,
	}
}

// ClassificationFunnel 三层分类分级漏斗执行器（带高并发 LRU 缓存）
type ClassificationFunnel struct {
	ruleEngine  *RuleEngine
	nerEngine   NerEngine
	llmClient   *LLMClient
	safetyFloor *SafetyFloor
	cfg         FunnelConfig
	cache       *classificationCache
	standards   []StandardDef // P1-3: 已加载的标准映射文件（供诊断上报）
}

// NewClassificationFunnel 创建三层分类分级漏斗实例
func NewClassificationFunnel(
	rules []RuleDef,
	nerEngine NerEngine,
	llmClient *LLMClient,
	cfg FunnelConfig,
) (*ClassificationFunnel, error) {
	engine, err := NewRuleEngine(rules)
	if err != nil {
		return nil, err
	}

	if nerEngine == nil {
		nerEngine = NewRuleBasedNerEngine()
	}

	return &ClassificationFunnel{
		ruleEngine:  engine,
		nerEngine:   nerEngine,
		llmClient:   llmClient,
		safetyFloor: NewSafetyFloor(DefaultSafetyFloorConfig()),
		cfg:         cfg,
		cache:       newClassificationCache(10000),
	}, nil
}

// SetStandards 注入已加载的标准映射文件（P1-3）。
// 标准文件不参与分类决策，仅供诊断上报与合规对照。
func (f *ClassificationFunnel) SetStandards(standards []StandardDef) {
	f.standards = standards
}

// Standards 返回已加载的标准映射文件列表。
func (f *ClassificationFunnel) Standards() []StandardDef {
	return f.standards
}

// Classify 执行 3 层漏斗分级仲裁（优先查询 LRU 高速缓存）
func (f *ClassificationFunnel) Classify(ctx context.Context, field, value string) (*ClassificationResult, error) {
	// ─── Cache: 查询高并发 LRU 缓存（value 哈希化避免原始 PII 留存在堆内存）───
	cacheKey := classifyCacheKey(field, value)
	if cached, hit := f.cache.get(cacheKey); hit {
		return cached, nil
	}

	// ─── Layer 1: 规则引擎匹配 ───
	res := f.ruleEngine.Classify(field, value)
	if res.Confidence >= f.cfg.RuleConfidenceThreshold && res.MatchedBy != "default" {
		f.cache.put(cacheKey, res)
		return res, nil
	}

	// ─── Layer 2: Small-NER 实体抽取 ───
	// candidates 仅收集前层的标签与统计量（不含实体文本），供 Layer 3 仲裁参考。
	var candidates []LLMCandidate
	if f.cfg.EnableNER && f.nerEngine != nil && f.nerEngine.IsAvailable() && value != "" {
		entities, err := f.nerEngine.Extract(ctx, value)
		if err == nil && len(entities) > 0 {
			bestEntity := selectHighestRiskEntity(entities)
			if bestEntity.Confidence >= f.cfg.NERConfidenceThreshold {
				level, category := mapNERLabelToSecurity(bestEntity.Label)
				nerRes := &ClassificationResult{
					Field:      field,
					Value:      value,
					Level:      level,
					Category:   category,
					Confidence: bestEntity.Confidence,
					MatchedBy:  "ner:" + bestEntity.Label,
				}
				f.cache.put(cacheKey, nerRes)
				return nerRes, nil
			}
			// 未达阈值：只把「标签 + 等级 + 置信度」下传为候选，实体文本本身绝不出域
			level, category := mapNERLabelToSecurity(bestEntity.Label)
			candidates = append(candidates, LLMCandidate{
				Source:     "ner:" + bestEntity.Label,
				Level:      string(level),
				Category:   category,
				Confidence: bestEntity.Confidence,
			})
		}
	}

	// ─── Layer 3: 外部 LLM 仲裁服务 ───
	// P0-5「只送特征、不送原值」：升级载荷由 ClassifyShape 内部指纹化，原值不外送。
	if f.cfg.EnableLLM && f.llmClient != nil {
		if res != nil && res.MatchedBy != "" && res.MatchedBy != "default" {
			candidates = append([]LLMCandidate{{
				Source:     res.MatchedBy,
				Level:      string(res.Level),
				Category:   res.Category,
				Confidence: res.Confidence,
			}}, candidates...)
		}

		llmCtx, cancel := context.WithTimeout(ctx, f.cfg.LLMTimeout)
		defer cancel()

		if f.llmClient.IsAvailable(llmCtx) {
			llmResp, err := f.llmClient.ClassifyShape(llmCtx, field, value, candidates)
			if err == nil && llmResp != nil && llmResp.Confidence >= 0.70 {
				llmRes := &ClassificationResult{
					Field:      field,
					Value:      value,
					Level:      SecurityLevel(llmResp.Level),
					Category:   llmResp.Category,
					Confidence: llmResp.Confidence,
					MatchedBy:  "llm",
				}
				f.cache.put(cacheKey, llmRes)
				return llmRes, nil
			}
		}
	}

	// ─── Safety Floor: 兜底安全等级 ───
	floorRes := f.safetyFloor.Arbitrate(res)
	f.cache.put(cacheKey, floorRes)
	return floorRes, nil
}

// ClearCache 清理分类缓存
func (f *ClassificationFunnel) ClearCache() {
	if f.cache != nil {
		f.cache.clear()
	}
}

// LLMStatus 返回 LLM 客户端健康状态（用于 /readyz/llm 探测）。
// 返回 (configured, available)：
//   - configured: LLM 客户端是否已配置
//   - available: LLM 服务是否可达
func (f *ClassificationFunnel) LLMStatus(ctx context.Context) (configured, available bool) {
	if f.llmClient == nil {
		return false, false
	}
	if !f.cfg.EnableLLM {
		return true, false
	}
	return true, f.llmClient.IsAvailable(ctx)
}

// NerStatus 返回 Layer 2 实际装配的 NER 引擎口径，供 /ops/diagnostics 诚实上报（P1-3）。
// 返回 (backend, modelBacked)：
//   - backend: 引擎实现名（NerEngine.Name()）；未装配时为 "none"
//   - modelBacked: 是否装配了真实模型驱动（ONNX / CUDA）且当前可推理的 NER 引擎；
//     默认装配的正则降级桩 rule-based-ner 恒为 false。
func (f *ClassificationFunnel) NerStatus() (backend string, modelBacked bool) {
	if f.nerEngine == nil {
		return "none", false
	}
	return f.nerEngine.Name(), NerEngineModelBacked(f.nerEngine)
}

// LLMEscalationStats 返回 Layer-3 升级外送诊断快照（升级次数、载荷去标识化与传输安全态），
// 供 /ops/diagnostics 类上报直接消费。Layer 3 关闭时计数器天然为 0；
// 未配置客户端时返回零值并标记 PayloadDeidentified=true，表示不存在未去标识化的外送。
func (f *ClassificationFunnel) LLMEscalationStats() LLMClientStats {
	if f.llmClient == nil {
		return LLMClientStats{PayloadDeidentified: true}
	}
	return f.llmClient.Stats()
}

// LLMEnabled 返回 Layer-3 外部仲裁是否**真实启用**：配置开关为真且客户端已装配。
// 诊断上报必须据此口径，禁止写死 available=true 把未启用的能力宣称为已交付（P1-3）。
func (f *ClassificationFunnel) LLMEnabled() bool {
	return f.cfg.EnableLLM && f.llmClient != nil
}

// CacheStats 返回分类缓存命中统计（使用原子计数器，无需遍历分片）
func (f *ClassificationFunnel) CacheStats() (hits, misses, size int) {
	if f.cache == nil {
		return 0, 0, 0
	}
	hits = int(f.cache.totalHits.Load())
	misses = int(f.cache.totalMiss.Load())
	// size 仍需遍历分片（但 hits/misses 已是 O(1)）
	for _, shard := range f.cache.shards {
		shard.mu.RLock()
		size += len(shard.items)
		shard.mu.RUnlock()
	}
	return hits, misses, size
}

// ──────────────────────────────────────────────
// 分片高并发 LRU 缓存实现
// ──────────────────────────────────────────────

const lruNumShards = 16

// lruMaxScanAttempts Second-Chance 淘汰的最大扫描节点数，限制单次驱逐的最坏工作量。
const lruMaxScanAttempts = 8

// lruNode 缓存节点。
// ref 为 Second-Chance（CLOCK）引用标记：读命中只需原子置位，
// 不再触碰双向链表，从而允许读路径持有 RLock 而非排他锁。
type lruNode struct {
	key  string
	val  *ClassificationResult
	prev *lruNode
	next *lruNode
	ref  atomic.Bool
}

type lruShard struct {
	mu       sync.RWMutex
	capacity int
	items    map[string]*lruNode
	head     *lruNode
	tail     *lruNode
}

type classificationCache struct {
	shards    [lruNumShards]*lruShard
	totalHits atomic.Int64 // 全局原子命中计数（避免 CacheStats 遍历所有分片）
	totalMiss atomic.Int64 // 全局原子未命中计数
}

func newClassificationCache(capacity int) *classificationCache {
	if capacity <= 0 {
		capacity = 10000
	}
	shardCap := (capacity + lruNumShards - 1) / lruNumShards
	c := &classificationCache{}
	for i := 0; i < lruNumShards; i++ {
		head := &lruNode{}
		tail := &lruNode{}
		head.next = tail
		tail.prev = head
		c.shards[i] = &lruShard{
			capacity: shardCap,
			items:    make(map[string]*lruNode, shardCap),
			head:     head,
			tail:     tail,
		}
	}
	return c
}

func (c *classificationCache) shardFor(key string) *lruShard {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return c.shards[h%lruNumShards]
}

// get 读路径全程持 RLock（同分片并发读不互斥）。
// 命中仅原子置 ref 标记，链表结构性写批量下放到驱逐路径，近似 LRU。
func (c *classificationCache) get(key string) (*ClassificationResult, bool) {
	shard := c.shardFor(key)
	shard.mu.RLock()
	node, exists := shard.items[key]
	if !exists {
		shard.mu.RUnlock()
		c.totalMiss.Add(1)
		return nil, false
	}
	cp := *node.val
	shard.mu.RUnlock()

	node.ref.Store(true)
	c.totalHits.Add(1)
	return &cp, true
}

func (c *classificationCache) put(key string, val *ClassificationResult) {
	if val == nil {
		return
	}
	shard := c.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	if node, exists := shard.items[key]; exists {
		node.val = val
		c.moveToFront(shard, node)
		return
	}

	if len(shard.items) >= shard.capacity {
		c.removeOldest(shard)
	}

	node := &lruNode{key: key, val: val}
	shard.items[key] = node
	c.addToFront(shard, node)
}

func (c *classificationCache) addToFront(s *lruShard, node *lruNode) {
	node.next = s.head.next
	node.prev = s.head
	s.head.next.prev = node
	s.head.next = node
}

func (c *classificationCache) moveToFront(s *lruShard, node *lruNode) {
	c.removeNode(s, node)
	c.addToFront(s, node)
}

func (c *classificationCache) removeNode(s *lruShard, node *lruNode) {
	node.prev.next = node.next
	node.next.prev = node.prev
}

// removeOldest 以 Second-Chance（CLOCK）近似淘汰：
// 从尾部扫描，凡读路径置位的节点延迟提升到队首（偿还下放的结构性写），
// 至多扫描 lruMaxScanAttempts 个节点，扫描耗尽则直接淘汰尾部，保持 O(1) 摊销。
func (c *classificationCache) removeOldest(s *lruShard) {
	for i := 0; i < lruMaxScanAttempts; i++ {
		last := s.tail.prev
		if last == s.head {
			return
		}
		if !last.ref.Swap(false) {
			c.removeNode(s, last)
			delete(s.items, last.key)
			return
		}
		c.moveToFront(s, last)
	}
	if last := s.tail.prev; last != s.head {
		last.ref.Store(false)
		c.removeNode(s, last)
		delete(s.items, last.key)
	}
}

func (c *classificationCache) clear() {
	for _, shard := range c.shards {
		shard.mu.Lock()
		shard.items = make(map[string]*lruNode, shard.capacity)
		head := &lruNode{}
		tail := &lruNode{}
		head.next = tail
		tail.prev = head
		shard.head = head
		shard.tail = tail
		shard.mu.Unlock()
	}
}

// classifyCacheKey 生成安全缓存 key：字段名明文 + value SHA-256 截断 128bit。
// 避免原始高基数 PII 留存在缓存 key 中导致内存膨胀与数据驻留。
func classifyCacheKey(field, value string) string {
	h := sha256.Sum256([]byte(value))
	return field + "\x00" + hex.EncodeToString(h[:16])
}

// selectHighestRiskEntity 选出风险最高且置信度最高的实体
func selectHighestRiskEntity(entities []NerEntity) NerEntity {
	var best NerEntity
	bestRank := -1

	for _, e := range entities {
		rank := getRiskRank(e.Label)
		if rank > bestRank || (rank == bestRank && e.Confidence > best.Confidence) {
			best = e
			bestRank = rank
		}
	}
	return best
}

func getRiskRank(label string) int {
	switch strings.ToUpper(label) {
	case "ID_CARD", "BANK_CARD", "PASSPORT", "MILITARY_ID":
		return 5 // TopSecret
	case "DISEASE", "MEDICAL_CONDITION", "ICD10_CODE", "HIV", "PSYCHIATRIC":
		return 4 // Secret
	case "PHONE", "EMAIL", "ADDRESS", "PERSON":
		return 3 // Confidential
	case "ORG", "ORGANIZATION":
		return 2 // Internal
	default:
		return 1 // Public
	}
}

func mapNERLabelToSecurity(label string) (SecurityLevel, string) {
	switch strings.ToUpper(label) {
	case "ID_CARD":
		return LevelTopSecret, "pii.identity"
	case "BANK_CARD":
		return LevelTopSecret, "pii.financial"
	case "PASSPORT", "MILITARY_ID":
		return LevelTopSecret, "pii.identity"
	case "DISEASE", "MEDICAL_CONDITION", "ICD10_CODE", "HIV", "PSYCHIATRIC":
		return LevelSecret, "medical.condition"
	case "PHONE":
		return LevelConfidential, "pii.contact"
	case "EMAIL":
		return LevelConfidential, "pii.contact"
	case "ADDRESS":
		return LevelConfidential, "pii.location"
	case "PERSON":
		return LevelConfidential, "pii.identity"
	case "ORG", "ORGANIZATION":
		return LevelInternal, "entity.organization"
	default:
		return LevelPublic, "unknown"
	}
}
