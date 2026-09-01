// Package dynclassification 提供三层动态分类分级引擎扩展。
//
// safety_floor.go — 安全底线门禁仲裁器
package dynclassification

import (
	"log/slog"
	"runtime"
	"sync"

	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
)

// ──────────────────────────────────────────────
// 安全等级排序
// ──────────────────────────────────────────────

var levelRank = map[SecurityLevel]int{
	LevelPublic:       0,
	LevelInternal:     1,
	LevelConfidential: 2,
	LevelSecret:       3,
	LevelTopSecret:    4,
}

// LevelFromString 从字符串解析安全等级
func LevelFromString(s string) SecurityLevel {
	switch s {
	case "public":
		return LevelPublic
	case "internal":
		return LevelInternal
	case "confidential":
		return LevelConfidential
	case "secret":
		return LevelSecret
	case "top_secret":
		return LevelTopSecret
	default:
		return LevelPublic
	}
}

// LevelID 返回本等级在规则库（rules/taxonomies/default.yaml）中的 L1~L5 标识：
// "confidential" → "L3"，已是 L 形式时幂等返回。词表外取值返回空串，绝不静默兜底为某个等级。
//
// 下游消费者（service-hub 的定级→算子映射、audit-log 的 security_level 枚举）统一使用 L 形式，
// 而引擎内部 canonical 名称只在引擎内流转；跨服务响应必须补齐 level_id，否则历史上当分类结果
// 只回 "confidential" 时，中枢会读不到级别并把算子降级为默认值（P1-1 根因）。
func (l SecurityLevel) LevelID() string {
	return naming.NormalizeSecurityLevelID(string(l))
}

// LevelRank 返回安全等级排名（越高越敏感）
func LevelRank(level SecurityLevel) int {
	if r, ok := levelRank[level]; ok {
		return r
	}
	return 0
}

// MaxLevel 返回两个等级中较高的一个
func MaxLevel(a, b SecurityLevel) SecurityLevel {
	if LevelRank(a) >= LevelRank(b) {
		return a
	}
	return b
}

// ──────────────────────────────────────────────
// 安全底线仲裁器
// ──────────────────────────────────────────────

// SafetyFloorConfig 安全底线配置
type SafetyFloorConfig struct {
	// MinLevel 最低安全等级（任何分类结果不得低于此等级）
	MinLevel SecurityLevel
	// ConfidenceThreshold 置信度阈值（低于此值触发升级）
	ConfidenceThreshold float64
	// ForceUpgradeOnUncertainty 不确定时强制升级
	ForceUpgradeOnUncertainty bool
	// AuditLog 是否记录仲裁日志
	AuditLog bool
}

// DefaultSafetyFloorConfig 默认安全底线配置。
//
// MinLevel 默认为 LevelInternal（P0-2 默认拒绝）：配置文件缺失时，引擎不再把
// 「没有任何底线」当成默认态 —— public 底线等价于「未定级字段可原样出域」。
// 需要 public 底线的部署必须显式写 safety_floor.min_level: "public"。
func DefaultSafetyFloorConfig() SafetyFloorConfig {
	return SafetyFloorConfig{
		MinLevel:                  LevelInternal,
		ConfidenceThreshold:       0.6,
		ForceUpgradeOnUncertainty: true,
		AuditLog:                  true,
	}
}

// SafetyFloor 安全底线仲裁器
type SafetyFloor struct {
	config    SafetyFloorConfig
	mu        sync.RWMutex
	audit     []ArbitrationEvent // 固定容量 ring buffer
	auditIdx  int                // 下一个写入位置
	auditFull bool               // 是否已循环覆盖一轮
}

// ArbitrationEvent 仲裁事件
type ArbitrationEvent struct {
	Field         string        `json:"field"`
	OriginalLevel SecurityLevel `json:"original_level"`
	FinalLevel    SecurityLevel `json:"final_level"`
	Reason        string        `json:"reason"`
	Confidence    float64       `json:"confidence"`
	EngineLayer   string        `json:"engine_layer"`
}

// NewSafetyFloor 创建安全底线仲裁器
func NewSafetyFloor(config SafetyFloorConfig) *SafetyFloor {
	return &SafetyFloor{
		config: config,
		audit:  make([]ArbitrationEvent, 0, 10000),
	}
}

// Arbitrate 对分类结果执行安全底线仲裁
func (sf *SafetyFloor) Arbitrate(result *ClassificationResult) *ClassificationResult {
	if result == nil {
		return nil
	}

	// 加锁读取配置，防止与 UpdateConfig 并发竞争
	sf.mu.RLock()
	cfg := sf.config
	sf.mu.RUnlock()

	original := result.Level
	reason := ""

	// 规则 1：不低于最低安全等级
	if LevelRank(result.Level) < LevelRank(cfg.MinLevel) {
		result.Level = cfg.MinLevel
		reason = "below_minimum_level"
	}

	// 规则 2：低置信度触发升级
	if result.Confidence < cfg.ConfidenceThreshold {
		if cfg.ForceUpgradeOnUncertainty {
			nextLevel := sf.nextLevel(result.Level)
			if LevelRank(nextLevel) > LevelRank(result.Level) {
				result.Level = nextLevel
				if reason != "" {
					reason += "+low_confidence"
				} else {
					reason = "low_confidence"
				}
			}
		}
	}

	// 记录仲裁事件
	if reason != "" && cfg.AuditLog {
		event := ArbitrationEvent{
			Field:         result.Field,
			OriginalLevel: original,
			FinalLevel:    result.Level,
			Reason:        reason,
			Confidence:    result.Confidence,
			EngineLayer:   result.MatchedBy,
		}
		sf.recordEvent(event)
		slog.Debug("Safety floor arbitration",
			"field", result.Field,
			"original", original,
			"final", result.Level,
			"reason", reason,
		)
	}

	return result
}

// ArbitrateBatch 批量仲裁（超大批量多核并发）。
// 单条仲裁为纯内存比较 + ring buffer 写入（极轻量），阈值 128 以下
// goroutine 创建与调度开销高于串行执行收益，小批量直接单趟串行。
func (sf *SafetyFloor) ArbitrateBatch(results []*ClassificationResult) []*ClassificationResult {
	n := len(results)
	if n <= 128 {
		for i, r := range results {
			results[i] = sf.Arbitrate(r)
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
	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		start := w * chunkSize
		end := start + chunkSize
		if end > n {
			end = n
		}
		if start >= end {
			break
		}
		wg.Add(1)
		go func(s, e int) {
			defer wg.Done()
			for i := s; i < e; i++ {
				results[i] = sf.Arbitrate(results[i])
			}
		}(start, end)
	}
	wg.Wait()
	return results
}

// nextLevel 返回下一个更高的安全等级
func (sf *SafetyFloor) nextLevel(level SecurityLevel) SecurityLevel {
	switch level {
	case LevelPublic:
		return LevelInternal
	case LevelInternal:
		return LevelConfidential
	case LevelConfidential:
		return LevelSecret
	case LevelSecret:
		return LevelTopSecret
	default:
		return level
	}
}

// recordEvent 记录仲裁事件（ring buffer 固定容量循环覆盖，零分配）
func (sf *SafetyFloor) recordEvent(event ArbitrationEvent) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	cap := cap(sf.audit)
	if len(sf.audit) < cap {
		sf.audit = sf.audit[:len(sf.audit)+1]
		sf.audit[sf.auditIdx] = event
	} else {
		sf.audit[sf.auditIdx] = event
		sf.auditFull = true
	}
	sf.auditIdx = (sf.auditIdx + 1) % cap
}

// AuditEvents 返回仲裁审计事件（按时间顺序）
func (sf *SafetyFloor) AuditEvents() []ArbitrationEvent {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	n := len(sf.audit)
	if n == 0 {
		return nil
	}
	if !sf.auditFull {
		// 尚未循环，直接返回拷贝
		result := make([]ArbitrationEvent, n)
		copy(result, sf.audit)
		return result
	}
	// 已循环，从 auditIdx 开始按时间顺序拼接
	result := make([]ArbitrationEvent, 0, n)
	result = append(result, sf.audit[sf.auditIdx:]...)
	result = append(result, sf.audit[:sf.auditIdx]...)
	return result
}

// UpdateConfig 更新安全底线配置
func (sf *SafetyFloor) UpdateConfig(config SafetyFloorConfig) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	sf.config = config
}
