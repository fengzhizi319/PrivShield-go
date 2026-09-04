// Package dynclassification 提供 ONNX NER 引擎接口与 CPU 降级实现。
//
// 设计目标：
//   - Layer 2 Small-NER：ONNX Runtime 推理（GPU CUDA / CPU OpenMP）
//   - 四级降级：GPU → CPU ONNX → AC 规则引擎 → 安全底线
//   - 动态合批：通过 DynamicBatcher 聚合请求提升 GPU 利用率
//   - 线程绑定：GPU 推理 LockOSThread 避免调度抖动
//
// 当前实现：
//   - NerEngine 接口定义
//   - RuleBasedNerEngine（AC 规则引擎降级实现）
//   - OnnxNerEngine 骨架（CGO 绑定待引入 onnxruntime_go 后实现）
//   - 降级链管理器 FallbackChain
//
// 参考设计文档 §5、§12.5。
package dynclassification

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ──────────────────────────────────────────────
// NER 实体与引擎接口
// ──────────────────────────────────────────────

// NerEntity NER 提取的实体
type NerEntity struct {
	Text       string  `json:"text"`       // 实体文本
	Label      string  `json:"label"`      // 实体标签（如 PER/LOC/ORG）
	Start      int     `json:"start"`      // 起始字符偏移
	End        int     `json:"end"`        // 结束字符偏移
	Confidence float64 `json:"confidence"` // 置信度 [0, 1]
	Source     string  `json:"source"`     // 来源引擎: "onnx_gpu" | "onnx_cpu" | "rule"
}

// NerEngine NER 引擎接口
type NerEngine interface {
	// Extract 从文本中提取命名实体
	Extract(ctx context.Context, text string) ([]NerEntity, error)

	// IsAvailable 检查引擎是否可用
	IsAvailable() bool

	// Name 返回引擎名称
	Name() string
}

// ModelBackedNerEngine 是「真实模型驱动」NER 引擎的可选能力接口（整改项 P1-3）。
//
// 存在理由：RuleBasedNerEngine 是正则降级桩，其 IsAvailable() 恒为 true，
// 因此「NER 能力是否可用」绝不能用 IsAvailable() 表述，否则运维诊断会把正则桩
// 谎报为已交付的模型推理能力。只有真正加载了推理模型（ONNX / CUDA）且当前可执行
// 模型前向的引擎才实现本接口并返回 true。
//
// 该口径对新增引擎是 fail-closed 的：未显式实现本接口的引擎一律按「非模型驱动」
// 上报，因此 rule-based-ner / 任何尚未交付模型的骨架实现都不会被宣称为可用 AI NER。
type ModelBackedNerEngine interface {
	NerEngine

	// ModelBacked 报告该引擎是否由真实推理模型驱动且当前可执行模型推理。
	ModelBacked() bool
}

// NerEngineModelBacked 判定给定 NER 引擎是否具备真实模型推理能力（P1-3 诚实口径）。
func NerEngineModelBacked(engine NerEngine) bool {
	mb, ok := engine.(ModelBackedNerEngine)
	return ok && mb.ModelBacked()
}

// ──────────────────────────────────────────────
// 基于规则的 NER 引擎（CPU 降级实现）
// ──────────────────────────────────────────────

// rulePattern 规则模式
type rulePattern struct {
	pattern *regexp.Regexp
	label   string
}

// RuleBasedNerEngine 基于正则规则的 NER 引擎
// 作为 GPU/CPU ONNX 推理不可用时的降级方案。
type RuleBasedNerEngine struct {
	patterns []rulePattern
}

// NewRuleBasedNerEngine 创建规则 NER 引擎
func NewRuleBasedNerEngine() *RuleBasedNerEngine {
	patterns := []rulePattern{
		// 中国身份证号 (18位)
		{regexp.MustCompile(`\b(\d{6}(19|20)\d{2}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])\d{3}[\dXx])\b`), "ID_CARD"},
		// 中国手机号
		{regexp.MustCompile(`\b(1[3-9]\d{9})\b`), "PHONE"},
		// 邮箱
		{regexp.MustCompile(`\b([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})\b`), "EMAIL"},
		// 银行卡号 (16-19位)
		{regexp.MustCompile(`\b(\d{4}[\s-]?\d{4}[\s-]?\d{4}[\s-]?\d{4,7})\b`), "BANK_CARD"},
		// 中文姓名 (2-4个汉字)
		{regexp.MustCompile(`([\x{4e00}-\x{9fff}]{2,4})`), "PERSON"},
		// 地址关键词
		{regexp.MustCompile(`((?:省|市|区|县|镇|乡|村|路|街|号|弄|栋|幢|单元|室).{2,20})`), "ADDRESS"},
		// 医疗术语
		{regexp.MustCompile(`(艾滋病|HIV|梅毒|乙肝|结核|癌症|肿瘤|糖尿病|高血压|冠心病|白血病)`), "MEDICAL_CONDITION"},
		// 军官证
		{regexp.MustCompile(`\b(军字第\s?\d{4,8}号)\b`), "MILITARY_ID"},
		// 护照号
		{regexp.MustCompile(`\b([A-Z]\d{8})\b`), "PASSPORT"},
	}
	return &RuleBasedNerEngine{patterns: patterns}
}

// Extract 使用正则规则提取实体
func (e *RuleBasedNerEngine) Extract(_ context.Context, text string) ([]NerEntity, error) {
	if text == "" {
		return nil, nil
	}

	var entities []NerEntity
	seen := make(map[string]bool) // 去重

	for _, rp := range e.patterns {
		matches := rp.pattern.FindAllStringIndex(text, -1)
		for _, loc := range matches {
			entityText := text[loc[0]:loc[1]]
			key := fmt.Sprintf("%s:%d:%d", rp.label, loc[0], loc[1])
			if seen[key] {
				continue
			}
			seen[key] = true

			entities = append(entities, NerEntity{
				Text:       entityText,
				Label:      rp.label,
				Start:      loc[0],
				End:        loc[1],
				Confidence: 0.85, // 规则匹配固定置信度
				Source:     "rule",
			})
		}
	}
	return entities, nil
}

// IsAvailable 规则引擎始终可用。
//
// ⚠️ 这只是「降级桩可服务」，不是「NER 模型能力可用」：本引擎刻意不实现
// ModelBackedNerEngine，因此 NerEngineModelBacked 对它恒返回 false（P1-3）。
func (e *RuleBasedNerEngine) IsAvailable() bool { return true }

// Name 返回引擎名称
func (e *RuleBasedNerEngine) Name() string { return "rule-based-ner" }

// ──────────────────────────────────────────────
// ONNX NER 引擎骨架（待 CGO 绑定）
// ──────────────────────────────────────────────

// OnnxDevice ONNX 推理设备类型
type OnnxDevice int

const (
	OnnxDeviceCPU OnnxDevice = iota
	OnnxDeviceCUDA
)

// OnnxNerConfig ONNX NER 引擎配置
type OnnxNerConfig struct {
	ModelPath    string        // 模型文件路径 (.onnx)
	VocabPath    string        // 词表文件路径 (vocab.txt)
	Device       OnnxDevice    // 推理设备
	MaxSeqLen    int           // 最大序列长度
	QueueSize    int           // 推理队列大小
	Timeout      time.Duration // 推理超时
	BatchMaxSize int           // 动态合批最大大小
	BatchMaxWait time.Duration // 动态合批最大等待
}

// DefaultOnnxNerConfig 默认 ONNX NER 配置
func DefaultOnnxNerConfig() OnnxNerConfig {
	return OnnxNerConfig{
		ModelPath:    ".models/ner/model.onnx",
		VocabPath:    ".models/ner/vocab.txt",
		Device:       OnnxDeviceCPU,
		MaxSeqLen:    128,
		QueueSize:    512,
		Timeout:      50 * time.Millisecond,
		BatchMaxSize: 32,
		BatchMaxWait: 10 * time.Millisecond,
	}
}

// OnnxNerEngine ONNX NER 推理引擎
//
// 当前为骨架实现：推理方法返回降级结果。
// 完整实现需要引入 CGO 绑定（github.com/yalue/onnxruntime_go）
// 并加载 ONNX Runtime 动态库。
//
// 降级策略：
//   - GPU CUDA 不可用 → 自动切换 CPU ONNX
//   - CPU ONNX 队列拥堵 → 降级为 RuleBasedNerEngine
//   - 推理超时 → 降级为 RuleBasedNerEngine
type OnnxNerEngine struct {
	cfg       OnnxNerConfig
	tokenizer *Tokenizer
	fallback  *RuleBasedNerEngine
	batcher   *DynamicBatcher
	available bool
	mu        sync.RWMutex

	// 统计
	inferCount    int64
	fallbackCount int64
}

// NewOnnxNerEngine 创建 ONNX NER 引擎
func NewOnnxNerEngine(cfg OnnxNerConfig) *OnnxNerEngine {
	tokenizer := NewSimpleTokenizer(cfg.MaxSeqLen)
	fallback := NewRuleBasedNerEngine()

	engine := &OnnxNerEngine{
		cfg:       cfg,
		tokenizer: tokenizer,
		fallback:  fallback,
		available: false, // 骨架模式默认不可用
	}

	// 创建动态合批器
	batchCfg := DynamicBatcherConfig{
		MaxBatchSize:    cfg.BatchMaxSize,
		MaxWaitTime:     cfg.BatchMaxWait,
		QueueBufferSize: cfg.QueueSize,
	}
	engine.batcher = NewDynamicBatcher(batchCfg, engine.inferBatch)

	slog.Info("OnnxNerEngine created (skeleton mode, will fallback to rules)",
		"model", cfg.ModelPath,
		"device", cfg.Device,
		"max_seq_len", cfg.MaxSeqLen,
	)
	return engine
}

// Extract 提取命名实体（带降级）
func (e *OnnxNerEngine) Extract(ctx context.Context, text string) ([]NerEntity, error) {
	if text == "" {
		return nil, nil
	}

	// 检查 ONNX 引擎是否可用
	e.mu.RLock()
	avail := e.available
	e.mu.RUnlock()

	if !avail {
		// 直接降级到规则引擎
		e.mu.Lock()
		e.fallbackCount++
		e.mu.Unlock()
		return e.fallback.Extract(ctx, text)
	}

	// 通过动态合批器提交推理请求
	result, err := e.batcher.Submit(ctx, "", text, e.cfg.Timeout+100*time.Millisecond)
	if err != nil {
		// 超时或队列满，降级
		e.mu.Lock()
		e.fallbackCount++
		e.mu.Unlock()
		return e.fallback.Extract(ctx, text)
	}

	if entities, ok := result.Output.([]NerEntity); ok {
		return entities, nil
	}

	// 结果异常，降级
	return e.fallback.Extract(ctx, text)
}

// inferBatch 批次推理函数（供 DynamicBatcher 调用）
//
// 骨架实现：直接调用规则引擎。
// 完整实现需要：
// 1. Tokenizer 编码为 InputIDs/AttentionMask
// 2. 构造 ONNX Runtime Session 输入张量
// 3. 执行推理 (session.Run)
// 4. 解码 CRF/Softmax 输出为实体列表
func (e *OnnxNerEngine) inferBatch(ctx context.Context, items []BatchItem) []BatchResult {
	results := make([]BatchResult, len(items))
	for i, item := range items {
		// 骨架：使用规则引擎替代 ONNX 推理
		entities, err := e.fallback.Extract(ctx, item.Input)
		results[i] = BatchResult{
			ItemID: item.ID,
			Output: entities,
			Err:    err,
		}
	}

	e.mu.Lock()
	e.inferCount += int64(len(items))
	e.mu.Unlock()

	return results
}

// IsAvailable 检查引擎是否可用
func (e *OnnxNerEngine) IsAvailable() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.available
}

// ModelBacked 实现 ModelBackedNerEngine：报告 ONNX 模型是否真正已加载并可推理。
// 当前骨架实现从不加载模型，故交付构建中恒为 false（P1-3）。
func (e *OnnxNerEngine) ModelBacked() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.available
}

// Name 返回引擎名称
func (e *OnnxNerEngine) Name() string {
	switch e.cfg.Device {
	case OnnxDeviceCUDA:
		return "onnx-ner-gpu"
	default:
		return "onnx-ner-cpu"
	}
}

// Start 启动引擎（合批器）
func (e *OnnxNerEngine) Start() {
	e.batcher.Start()
}

// Stop 停止引擎
func (e *OnnxNerEngine) Stop() {
	e.batcher.Stop()
}

// Stats 返回引擎统计
func (e *OnnxNerEngine) Stats() (inferCount, fallbackCount int64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.inferCount, e.fallbackCount
}

// ──────────────────────────────────────────────
// 降级链管理器
// ──────────────────────────────────────────────

// FallbackChain NER 降级链管理器
//
// 按优先级尝试多个 NER 引擎：
// 1. GPU ONNX (最快，精度最高)
// 2. CPU ONNX (较快，精度相同)
// 3. Rule-based (最快降级，精度较低)
type FallbackChain struct {
	engines []NerEngine
}

// NewFallbackChain 创建降级链
func NewFallbackChain(engines ...NerEngine) *FallbackChain {
	return &FallbackChain{engines: engines}
}

// Extract 按优先级尝试提取实体
func (c *FallbackChain) Extract(ctx context.Context, text string) ([]NerEntity, error) {
	for _, engine := range c.engines {
		if !engine.IsAvailable() {
			continue
		}
		entities, err := engine.Extract(ctx, text)
		if err != nil {
			slog.Warn("NER engine failed, trying next",
				"engine", engine.Name(),
				"error", err.Error(),
			)
			continue
		}
		if len(entities) > 0 {
			return entities, nil
		}
	}
	// 所有引擎都失败或无实体
	return nil, nil
}

// RedactEntities 使用 NER 结果对文本进行实体抹除
func RedactEntities(text string, entities []NerEntity, replacement string) string {
	if len(entities) == 0 || replacement == "" {
		return text
	}

	// 按起始位置降序排列，从后往前替换避免偏移错位
	sorted := make([]NerEntity, len(entities))
	copy(sorted, entities)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Start > sorted[i].Start {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	result := []rune(text)
	for _, e := range sorted {
		start := e.Start
		end := e.End
		if start < 0 || end > len(result) || start >= end {
			continue
		}
		for k := start; k < end; k++ {
			result[k] = []rune(replacement)[0]
		}
	}
	return string(result)
}

// ──────────────────────────────────────────────
// 标签映射
// ──────────────────────────────────────────────

// NerLabelToSecurityTag 将 NER 标签映射为安全分级标签
func NerLabelToSecurityTag(label string) string {
	switch strings.ToUpper(label) {
	case "ID_CARD", "PASSPORT", "MILITARY_ID":
		return "PII_IDENTITY"
	case "PHONE", "EMAIL":
		return "PII_CONTACT"
	case "BANK_CARD":
		return "PII_FINANCIAL"
	case "PERSON":
		return "PII_IDENTITY"
	case "ADDRESS":
		return "PII_LOCATION"
	case "MEDICAL_CONDITION":
		return "PHI_HEALTH"
	default:
		return "PII_OTHER"
	}
}
