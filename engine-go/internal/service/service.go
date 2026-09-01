// Package service 提供 PrivacyService 统一编排层。
//
// 将隐私原语、分类引擎、预算会计、医疗流水线等组件串联为统一服务接口，
// 供 REST 和 gRPC 控制器调用。
package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/dynclassification"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/profile"
	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
	"github.com/fengzhizi319/PrivShield-go/pkg/crypto"
	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/budget"
	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/dp"
	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/kano"
	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/ldp"
	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/masking"
	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/medical"
	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/qol"
)

// ──────────────────────────────────────────────
// PrivacyService 统一编排
// ──────────────────────────────────────────────

// PrivacyService 隐私服务编排器
type PrivacyService struct {
	classifier   atomic.Pointer[dynclassification.RuleEngine]
	funnel       *dynclassification.ClassificationFunnel
	safetyFloor  *dynclassification.SafetyFloor
	budget       *budget.BudgetAccountant
	medicalYibao *medical.Pipeline
	medicalKang  *medical.Pipeline
	resolver     *profile.Resolver
	namespace    string
	rulesDir     string // 领域规则目录
	privacyYAML  string // 隐私策略配置文件

	// ── P2-2 配置绑定 + P0-2 默认拒绝（绑定态快照，供仲裁与诊断读取）──
	// safetyFloorConfig 当前生效的安全底线配置（可能来自 config/privacy.yaml）。
	safetyFloorConfig dynclassification.SafetyFloorConfig
	// unlistedFloor 具名默认拒绝策略（分类侧下限 + 脱敏侧处置）。
	unlistedFloor unlistedFieldFloor
	// policyBound 标记 config/privacy.yaml 是否被成功读取并绑定。
	policyBound bool
	// baseRules 调用方提供的基线规则（热更新时与领域规则目录重新合并，不丢失自定义规则）。
	baseRules []dynclassification.RuleDef

	// LLM 运行态快照（供诊断接口读取）。
	llmEndpoint       string
	llmMaxConcurrency int
	enableLLM         bool

	mu sync.RWMutex
}

// policyLoaded 返回 config/privacy.yaml 是否已成功绑定（诊断与测试用）。
func (s *PrivacyService) policyLoaded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.policyBound
}

// Config 服务配置
type Config struct {
	TotalEpsilon    float64
	TotalDelta      float64
	BudgetWindowSec int64
	Namespace       string
	ProfilePath     string
	RulesDir        string // 领域规则目录（默认 rules/domains）
	StandardsDir    string // 标准映射文件目录（默认 rules/standards，P1-3）
	PrivacyYAML     string // 隐私策略配置文件（默认 config/privacy.yaml）
	LLMEndpoint     string
	EnableLLM       bool
	EnableNER       bool
	Rules           []dynclassification.RuleDef
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	enableLLM := os.Getenv("PRIVACY_LLM_ENABLE") == "true"
	llmEndpoint := os.Getenv("PRIVACY_LLM_ENDPOINT")
	if llmEndpoint == "" {
		llmEndpoint = "http://localhost:8000/v1/chat/completions"
	}

	rulesDir := pkgconfig.EnvString("PRIVACY_RULES_DIR", "rules/domains")
	standardsDir := pkgconfig.EnvString("PRIVACY_STANDARDS_DIR", "rules/standards")
	privacyYAML := pkgconfig.EnvString("PRIVACY_CONFIG_FILE", "config/privacy.yaml")

	return Config{
		TotalEpsilon:    10.0,
		TotalDelta:      0.01,
		BudgetWindowSec: 3600,
		Namespace:       "default",
		ProfilePath:     "",
		RulesDir:        rulesDir,
		StandardsDir:    standardsDir,
		PrivacyYAML:     privacyYAML,
		LLMEndpoint:     llmEndpoint,
		EnableLLM:       enableLLM,
		EnableNER:       true,
		Rules:           defaultRules(),
	}
}

// NewPrivacyService 创建隐私服务实例。
//
// 配置绑定（P2-2）：safety_floor.* 与 classification.* 从 cfg.PrivacyYAML 读取；
// 默认拒绝（P0-2）：safety_floor.unlisted_field_policy / unlisted_min_level 解析为
// 具名策略，同时下发给两条医疗流水线与分类仲裁链路。
// 配置文件缺失或解析失败时回落到**代码级限制性默认值**（fail-closed，不中断启动）。
func NewPrivacyService(cfg Config) (*PrivacyService, error) {
	// ── 1. 读取策略配置（P2-2）──
	policy, policyErr := loadPrivacyPolicy(cfg.PrivacyYAML)

	// ── 2. 规则集：内置规则优先 + 领域规则目录（含字段规格矩阵）──
	rules := mergeDomainRules(cfg.Rules, cfg.RulesDir)
	engine, err := dynclassification.NewRuleEngine(rules)
	if err != nil {
		return nil, fmt.Errorf("init rule engine: %w", err)
	}

	res := profile.NewResolver()
	if cfg.ProfilePath != "" {
		if err := res.LoadFromYAML(cfg.ProfilePath); err != nil {
			slog.Warn("profile load failed, using defaults", "path", cfg.ProfilePath, "error", err)
		}
	}

	ns := cfg.Namespace
	if ns == "" {
		ns = pkgconfig.EnvString("PRIVACY_NAMESPACE", "default")
	}

	// ── 3. 安全底线（含 P0-2 默认拒绝下限）──
	sfCfg := dynclassification.DefaultSafetyFloorConfig()
	floor := defaultUnlistedFloor()
	if policy != nil {
		sfCfg = policy.SafetyFloor.applyToSafetyFloorConfig(sfCfg)
		floor = policy.SafetyFloor.resolveUnlistedFloor()
	}

	// ── 4. LLM 客户端（classification.llm_endpoint / llm_max_concurrency）──
	llmEndpoint := cfg.LLMEndpoint
	if policy != nil && os.Getenv("PRIVACY_LLM_ENDPOINT") == "" {
		if fromFile := strings.TrimSpace(policy.Classification.LLMEndpoint); fromFile != "" {
			llmEndpoint = fromFile
		}
	}
	llmMaxConcurrency := pkgconfig.EnvInt("PRIVACY_LLM_MAX_CONCURRENCY", 4)
	if policy != nil && policy.Classification.LLMMaxConcurrency != nil &&
		*policy.Classification.LLMMaxConcurrency > 0 &&
		*policy.Classification.LLMMaxConcurrency < llmMaxConcurrency {
		// 配置文件只能收紧并发上限（不放大外送面）。
		llmMaxConcurrency = *policy.Classification.LLMMaxConcurrency
	}

	var llmClient *dynclassification.LLMClient
	if cfg.EnableLLM || llmEndpoint != "" {
		llmClient = dynclassification.NewLLMClient(dynclassification.LLMClientConfig{
			Endpoint:       llmEndpoint,
			ModelName:      pkgconfig.EnvString("PRIVACY_LLM_MODEL", "qwen3.5"),
			MaxConcurrency: llmMaxConcurrency,
			Timeout:        30 * time.Second,
			MaxRetries:     2,
			APIKey:         os.Getenv("PRIVACY_LLM_API_KEY"),
		})
	}

	// ── 5. 漏斗配置（classification.confidence_threshold / enable_llm；enable_ner 有意不绑定）──
	funnelCfg := dynclassification.DefaultFunnelConfig()
	funnelCfg.EnableNER = cfg.EnableNER
	if policy != nil {
		funnelCfg = policy.Classification.applyToFunnelConfig(funnelCfg)
		policy.Classification.warnUnboundKeys(cfg.PrivacyYAML)
	}
	// Layer 3 只有在调用方与配置文件双方都允许时才开启（配置可关不可开）。
	funnelCfg.EnableLLM = funnelCfg.EnableLLM && cfg.EnableLLM && llmClient != nil

	funnel, err := dynclassification.NewClassificationFunnel(rules, dynclassification.NewRuleBasedNerEngine(), llmClient, funnelCfg)
	if err != nil {
		return nil, fmt.Errorf("init classification funnel: %w", err)
	}

	// P1-3: 加载标准映射文件并注入漏斗（供诊断上报与合规对照）。
	if cfg.StandardsDir != "" {
		standards, stdErrs := dynclassification.LoadStandardsFromDir(cfg.StandardsDir)
		for _, e := range stdErrs {
			slog.Warn("standards load error", "error", e)
		}
		funnel.SetStandards(standards)
		if len(standards) > 0 {
			slog.Info("standards loaded", "dir", cfg.StandardsDir, "count", len(standards))
		}
	}

	// ── 6. 医疗流水线：下发具名默认拒绝策略（P0-2 白名单反转）──
	medicalYibao := medical.NewYibaoPipeline()
	medicalYibao.SetUnlistedFieldPolicy(floor.Policy)
	medicalKang := medical.NewKangyangPipeline()
	medicalKang.SetUnlistedFieldPolicy(floor.Policy)

	svc := &PrivacyService{
		llmEndpoint:       llmEndpoint,
		llmMaxConcurrency: llmMaxConcurrency,
		enableLLM:         funnelCfg.EnableLLM,
		funnel:            funnel,
		safetyFloor:       dynclassification.NewSafetyFloor(sfCfg),
		safetyFloorConfig: sfCfg,
		unlistedFloor:     floor,
		budget:            budget.NewBudgetAccountant(cfg.TotalEpsilon, cfg.TotalDelta, cfg.BudgetWindowSec),
		medicalYibao:      medicalYibao,
		medicalKang:       medicalKang,
		resolver:          res,
		namespace:         ns,
		rulesDir:          cfg.RulesDir,
		privacyYAML:       cfg.PrivacyYAML,
		baseRules:         cfg.Rules,
	}
	svc.classifier.Store(engine)

	if policyErr != nil {
		slog.Warn("privacy policy not bound, falling back to restrictive code defaults",
			"path", cfg.PrivacyYAML, "error", policyErr,
			"min_level", string(sfCfg.MinLevel),
			"unlisted_field_policy", floor.Name,
			"unlisted_min_level", floor.levelLabel(),
		)
	} else {
		svc.policyBound = true
		slog.Info("privacy policy bound",
			"path", cfg.PrivacyYAML,
			"min_level", string(sfCfg.MinLevel),
			"confidence_threshold", sfCfg.ConfidenceThreshold,
			"ner_confidence_threshold", funnelCfg.NERConfidenceThreshold,
			"llm_enabled", funnelCfg.EnableLLM,
			"rules", len(rules),
			"unlisted_field_policy", floor.Name,
			"unlisted_disposition", string(floor.Policy),
			"unlisted_min_level", floor.levelLabel(),
		)
	}
	return svc, nil
}

// mergeDomainRules 把 rulesDir 下的领域规则（含 P0-2 字段规格矩阵）追加到基线规则之后。
//
// 顺序即优先级：dynclassification.RuleEngine Layer 1 为「首个字段名正则命中即返回」，
// 基线 canonical 规则保持在前的话，既有分类口径不受新增矩阵影响。
func mergeDomainRules(base []dynclassification.RuleDef, rulesDir string) []dynclassification.RuleDef {
	if strings.TrimSpace(rulesDir) == "" {
		return base
	}
	domainRules, err := dynclassification.LoadRulesFromDir(rulesDir)
	if err != nil || len(domainRules) == 0 {
		return base
	}
	merged := make([]dynclassification.RuleDef, 0, len(base)+len(domainRules))
	merged = append(merged, base...)
	merged = append(merged, domainRules...)
	return merged
}

// ──────────────────────────────────────────────
// 掩码 API
// ──────────────────────────────────────────────

// MaskField 对单个字段执行脱敏
func (s *PrivacyService) MaskField(fieldType, value string) (string, error) {
	switch fieldType {
	case "id_card":
		return masking.MaskIdCard(value), nil
	case "phone":
		return masking.MaskPhone(value), nil
	case "bank_card":
		return masking.MaskBankCard(value), nil
	case "name":
		return masking.MaskChineseName(value), nil
	case "email":
		return masking.MaskEmail(value), nil
	case "address":
		return masking.MaskAddress(value), nil
	case "officer_id":
		return masking.MaskOfficerId(value), nil
	case "sm3", "hash_sm3":
		return s.HashSM3(value, ""), nil
	default:
		return "", fmt.Errorf("unknown mask type: %s", fieldType)
	}
}

// MaskRecord 对整条记录执行自动脱敏（基于字段名推断类型）
func (s *PrivacyService) MaskRecord(record map[string]string) map[string]string {
	result := make(map[string]string, len(record))
	for k, v := range record {
		result[k] = s.autoMaskField(k, v)
	}
	return result
}

// MaskBatchContext 批量脱敏（支持多核并发无锁分块计算与 Context 快速中断）
func (s *PrivacyService) MaskBatchContext(ctx context.Context, records []map[string]string) ([]map[string]string, error) {
	n := len(records)
	results := make([]map[string]string, n)
	if n == 0 {
		return results, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if n <= 16 {
		for i, r := range records {
			if i%8 == 0 && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			results[i] = s.MaskRecord(r)
		}
		return results, nil
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
		startIdx := w * chunkSize
		endIdx := startIdx + chunkSize
		if endIdx > n {
			endIdx = n
		}
		if startIdx >= endIdx {
			break
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			for i := start; i < end; i++ {
				if i%64 == 0 && ctx.Err() != nil {
					return
				}
				results[i] = s.MaskRecord(records[i])
			}
		}(startIdx, endIdx)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// MaskBatch 批量脱敏（兼容非 context 调用）
func (s *PrivacyService) MaskBatch(records []map[string]string) []map[string]string {
	res, _ := s.MaskBatchContext(context.Background(), records)
	return res
}

// ──────────────────────────────────────────────
// 差分隐私 API
// ──────────────────────────────────────────────

// NoisyCount 噪声计数
func (s *PrivacyService) NoisyCount(ctx context.Context, count int, epsilon float64) (float64, error) {
	if !s.budget.Consume(epsilon, 0) {
		return 0, fmt.Errorf("privacy budget exhausted")
	}
	return dp.NoisyCount(count, epsilon), nil
}

// NoisySum 噪声求和
func (s *PrivacyService) NoisySum(ctx context.Context, values []float64, epsilon, sensitivity float64) (float64, error) {
	if !s.budget.Consume(epsilon, 0) {
		return 0, fmt.Errorf("privacy budget exhausted")
	}
	return dp.NoisySum(values, epsilon, sensitivity), nil
}

// NoisyMean 噪声均值
func (s *PrivacyService) NoisyMean(ctx context.Context, values []float64, epsilon, delta, clipBound float64) (float64, error) {
	if !s.budget.Consume(epsilon, delta) {
		return 0, fmt.Errorf("privacy budget exhausted")
	}
	return dp.NoisyMean(values, epsilon, delta, clipBound), nil
}

// DPHistogram 差分隐私直方图（带预算消耗）
func (s *PrivacyService) DPHistogram(ctx context.Context, trueCounts map[string]int, epsilon float64) (map[string]float64, error) {
	if !s.budget.Consume(epsilon, 0) {
		return nil, fmt.Errorf("privacy budget exhausted")
	}
	return dp.NoisyHistogram(trueCounts, epsilon), nil
}

// DPVectorSum 差分隐私向量求和
func (s *PrivacyService) DPVectorSum(ctx context.Context, vectors [][]float64, maxNorm, epsilon float64) ([]float64, error) {
	if !s.budget.Consume(epsilon, 0) {
		return nil, fmt.Errorf("privacy budget exhausted")
	}
	return dp.VectorSum(vectors, maxNorm, epsilon), nil
}

// DPVectorMean 差分隐私向量均值
func (s *PrivacyService) DPVectorMean(ctx context.Context, vectors [][]float64, maxNorm, epsilon float64) ([]float64, error) {
	if !s.budget.Consume(epsilon, 0) {
		return nil, fmt.Errorf("privacy budget exhausted")
	}
	return dp.VectorMean(vectors, maxNorm, epsilon), nil
}

// ──────────────────────────────────────────────
// 本地差分隐私 API
// ──────────────────────────────────────────────

// RandomizedResponse 二值随机响应
func (s *PrivacyService) RandomizedResponse(value bool, epsilon float64) bool {
	return ldp.RandomizedResponse(value, epsilon)
}

// ORRResponse 多类别优化随机响应
func (s *PrivacyService) ORRResponse(value int, epsilon float64, domainSize int) int {
	return ldp.ORRResponse(value, epsilon, domainSize)
}

// PerturbBinaryBatch 批量二值扰动（与 Python perturb_binary_batch 对齐）
func (s *PrivacyService) PerturbBinaryBatch(values []int, epsilon float64) []int {
	return ldp.PerturbBinaryBatch(values, epsilon)
}

// PerturbCategoricalBatch 批量类别扰动（与 Python perturb_categorical_batch 对齐）
func (s *PrivacyService) PerturbCategoricalBatch(values []string, categories []string, epsilon float64) []string {
	return ldp.PerturbCategoricalBatch(values, categories, epsilon)
}

// EstimateBinaryFrequency 二值频率无偏估计（与 Python estimate_binary_frequency 对齐）
func (s *PrivacyService) EstimateBinaryFrequency(reportedValues []int, epsilon float64) float64 {
	return ldp.EstimateBinaryFrequency(reportedValues, epsilon)
}

// EstimateCategoricalHistogram 类别直方图无偏估计（与 Python estimate_categorical_histogram 对齐）
func (s *PrivacyService) EstimateCategoricalHistogram(reportedValues []string, categories []string, epsilon float64) map[string]float64 {
	return ldp.EstimateCategoricalHistogram(reportedValues, categories, epsilon)
}

// ──────────────────────────────────────────────
// K-匿名 API
// ──────────────────────────────────────────────

// KAnonymize K-匿名处理
func (s *PrivacyService) KAnonymize(records []kano.Record, qiFields []string, k int) (*kano.AnonymizationResult, error) {
	return kano.Anonymize(records, qiFields, k)
}

// ──────────────────────────────────────────────
// 查询混淆 API
// ──────────────────────────────────────────────

// ObfuscateQuery 查询混淆
func (s *PrivacyService) ObfuscateQuery(query string, numDecoys int, domain string) ([]string, int) {
	return qol.InjectDecoys(query, numDecoys, domain)
}

// ObfuscateQueryBatch 批量查询混淆（多核并发分块加速）
func (s *PrivacyService) ObfuscateQueryBatch(queries []string, numDecoys int, domain string) [][]string {
	n := len(queries)
	results := make([][]string, n)
	if n == 0 {
		return results
	}

	if n <= 32 {
		for i, q := range queries {
			injected, _ := qol.InjectDecoys(q, numDecoys, domain)
			results[i] = injected
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
				injected, _ := qol.InjectDecoys(queries[i], numDecoys, domain)
				results[i] = injected
			}
		}(start, end)
	}
	wg.Wait()
	return results
}

// ──────────────────────────────────────────────
// 动态分类 API
// ──────────────────────────────────────────────

// Classify 动态分类（通过 3 层漏斗：Rule → Small-NER → External LLM Arbitration）。
//
// 漏斗返回后仍必须过服务层安全底线：漏斗内置 SafetyFloor 用的是代码默认值，
// 只有这里才会应用 config/privacy.yaml 绑定后的 min_level / confidence_threshold
// 以及 P0-2 的具名默认拒绝下限（P2-2 修复点）。
func (s *PrivacyService) Classify(field, value string) *dynclassification.ClassificationResult {
	s.mu.RLock()
	funnel := s.funnel
	s.mu.RUnlock()
	if funnel != nil {
		res, err := funnel.Classify(context.Background(), field, value)
		if err == nil && res != nil {
			return s.arbitrate(res)
		}
	}
	if engine := s.classifier.Load(); engine != nil {
		return s.arbitrate(engine.Classify(field, value))
	}
	return s.arbitrate(&dynclassification.ClassificationResult{
		Field: field, Level: dynclassification.LevelPublic, Category: "unknown", Confidence: 0.5, MatchedBy: "default",
	})
}

// ClassifyBatch 批量分类（多核并发分块加速）
func (s *PrivacyService) ClassifyBatch(records []map[string]string) []*dynclassification.ClassificationResult {
	// 展平为 (field, value) 对列表
	type fv struct{ field, value string }
	var flat []fv
	for _, record := range records {
		for field, value := range record {
			flat = append(flat, fv{field, value})
		}
	}
	n := len(flat)
	if n == 0 {
		return nil
	}

	// 小批量走串行快速路径
	if n <= 32 {
		results := make([]*dynclassification.ClassificationResult, n)
		for i, item := range flat {
			results[i] = s.Classify(item.field, item.value)
		}
		return results
	}

	// 大批量多核分块并行
	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers > 16 {
		numWorkers = 16
	}
	if numWorkers > n {
		numWorkers = n
	}
	chunkSize := (n + numWorkers - 1) / numWorkers
	allResults := make([]*dynclassification.ClassificationResult, n)
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
		go func(startIdx, endIdx int) {
			defer wg.Done()
			for i := startIdx; i < endIdx; i++ {
				allResults[i] = s.classifyInternal(flat[i].field, flat[i].value)
			}
		}(start, end)
	}
	wg.Wait()
	return allResults
}

// classifyInternal 内部分类（不含外部锁，供并行调用）
func (s *PrivacyService) classifyInternal(field, value string) *dynclassification.ClassificationResult {
	s.mu.RLock()
	funnel := s.funnel
	s.mu.RUnlock()
	if funnel != nil {
		res, err := funnel.Classify(context.Background(), field, value)
		if err == nil && res != nil {
			return s.arbitrate(res)
		}
	}
	if engine := s.classifier.Load(); engine != nil {
		return s.arbitrate(engine.Classify(field, value))
	}
	return s.arbitrate(&dynclassification.ClassificationResult{
		Field: field, Level: dynclassification.LevelPublic, Category: "unknown", Confidence: 0.5, MatchedBy: "default",
	})
}

// ──────────────────────────────────────────────
// 医疗流水线 API
// ──────────────────────────────────────────────

// SanitizeMedicalRecord 医疗记录脱敏。
// domain 参数支持任意入站表示（canonical id / api_code / 别名），
// 通过 naming.NormalizeDataSourceID 归一化后路由到对应流水线。
// 未知数据源触发 Fail-Closed（设计文档 §3.3）。
func (s *PrivacyService) SanitizeMedicalRecord(record map[string]string, domain string) (map[string]string, error) {
	dsID, err := naming.NormalizeDataSourceID(domain)
	if err != nil {
		return nil, fmt.Errorf("INVALID_DATASOURCE_ID: %w", err)
	}
	switch dsID {
	case naming.DSYibao:
		return s.medicalYibao.SanitizeRecord(record), nil
	case naming.DSKangyang:
		return s.medicalKang.SanitizeRecord(record), nil
	default:
		return nil, fmt.Errorf("unsupported datasource: %s", dsID)
	}
}

// SanitizeMedicalBatch 批量医疗脱敏（SSOT 归一化 + Fail-Closed）。
func (s *PrivacyService) SanitizeMedicalBatch(records []map[string]string, domain string) ([]map[string]string, error) {
	dsID, err := naming.NormalizeDataSourceID(domain)
	if err != nil {
		return nil, fmt.Errorf("INVALID_DATASOURCE_ID: %w", err)
	}
	switch dsID {
	case naming.DSYibao:
		return s.medicalYibao.SanitizeBatch(records), nil
	case naming.DSKangyang:
		return s.medicalKang.SanitizeBatch(records), nil
	default:
		return nil, fmt.Errorf("unsupported datasource: %s", dsID)
	}
}

// ──────────────────────────────────────────────
// 预算查询 API
// ──────────────────────────────────────────────

// BudgetStatus 预算状态
func (s *PrivacyService) BudgetStatus() map[string]float64 {
	return map[string]float64{
		"total_epsilon":     s.budget.TotalEpsilon(),
		"used_epsilon":      s.budget.UsedEpsilon(),
		"remaining_epsilon": s.budget.RemainingEpsilon(),
		"total_delta":       s.budget.TotalDelta(),
		"used_delta":        s.budget.UsedDelta(),
		"remaining_delta":   s.budget.RemainingDelta(),
	}
}

// BudgetReset 重置预算
func (s *PrivacyService) BudgetReset() map[string]float64 {
	s.budget.Reset()
	return s.BudgetStatus()
}

// ──────────────────────────────────────────────
// HMAC 散列 API
// ──────────────────────────────────────────────

// HashHMAC HMAC 加盐散列
func (s *PrivacyService) HashHMAC(value, salt string) string {
	return masking.HashHMAC(value, salt)
}

// HashSM3 生成国密 SM3 确定性哈希脱敏散列，十六进制输出（前 16 位）
func (s *PrivacyService) HashSM3(value, salt string) string {
	if value == "" {
		return ""
	}
	h := crypto.NewSM3()
	if salt != "" {
		h.Write([]byte(salt))
	}
	h.Write([]byte(value))
	digest := hex.EncodeToString(h.Sum(nil))
	if len(digest) > 16 {
		return digest[:16]
	}
	return digest
}

// ──────────────────────────────────────────────
// Agent & Medical 统一处理流水线 API (P0)
// ──────────────────────────────────────────────

// AgentProcessResult 表示 /v1/agent/process 与 /v1/medical/process 的返回结果。
type AgentProcessResult struct {
	ClassificationReport []map[string]interface{} `json:"classification_report"`
	SanitizedData        []map[string]string      `json:"sanitized_data"`
	Summary              map[string]interface{}   `json:"summary"`
	// Level 是本次分类结果中的最高敏感级别，使用规则库 L1~L5 词表（供下游定级→算子映射与存证）。
	// 空串表示分类报告中不存在任何可识别级别——调用方 MUST 按 fail-closed 处理，不得静默定级。
	Level string `json:"level"`
}

// ProcessAgentData 对提交的数据集执行 3-Layer 分类分级与隐私脱敏治理。
func (s *PrivacyService) ProcessAgentData(records []map[string]interface{}, apiCode, datasourceID string) (*AgentProcessResult, error) {
	if len(records) == 0 {
		return &AgentProcessResult{
			ClassificationReport: []map[string]interface{}{},
			SanitizedData:        []map[string]string{},
			Summary: map[string]interface{}{
				"total_records": 0,
				"input_hash":    "",
				"output_hash":   "",
				"api_code":      apiCode,
				"datasource_id": datasourceID,
				"engine":        "go",
				// 无记录即无定级：级别为空，下游不得据此推断任何默认等级。
				"overall_level": "",
			},
		}, nil
	}

	reports := make([][]map[string]interface{}, len(records))
	sanitized := make([]map[string]string, len(records))

	dsID, normErr := naming.NormalizeDataSourceID(datasourceID)
	if dsID == "" && apiCode != "" {
		dsID, normErr = naming.NormalizeDataSourceID(apiCode)
	}
	if dsID == "" && normErr != nil {
		slog.Warn("ProcessAgentData: datasource normalization failed",
			"datasource_id", datasourceID, "api_code", apiCode, "err", normErr)
	}

	// processOne 处理单条记录（分类 + 脱敏）
	processOne := func(idx int) {
		rec := records[idx]
		strRecord := make(map[string]string, len(rec))
		for k, v := range rec {
			strRecord[k] = fmt.Sprintf("%v", v)
		}
		localReport := make([]map[string]interface{}, 0, len(strRecord))
		for k, v := range strRecord {
			cRes := s.classifyInternal(k, v)
			localReport = append(localReport, map[string]interface{}{
				"field":      k,
				"level":      cRes.Level,
				"level_id":   cRes.Level.LevelID(),
				"category":   cRes.Category,
				"confidence": cRes.Confidence,
				"matched_by": cRes.MatchedBy,
			})
		}
		reports[idx] = localReport
		switch dsID {
		case naming.DSYibao:
			sanitized[idx] = s.medicalYibao.SanitizeRecord(strRecord)
		case naming.DSKangyang:
			sanitized[idx] = s.medicalKang.SanitizeRecord(strRecord)
		default:
			sanitized[idx] = s.MaskRecord(strRecord)
		}
	}

	if len(records) <= 32 {
		for i := range records {
			processOne(i)
		}
	} else {
		numWorkers := runtime.GOMAXPROCS(0)
		if numWorkers > 16 {
			numWorkers = 16
		}
		var wg sync.WaitGroup
		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for i := workerID; i < len(records); i += numWorkers {
					processOne(i)
				}
			}(w)
		}
		wg.Wait()
	}

	// 合并分类报告
	report := make([]map[string]interface{}, 0, len(records)*4)
	for _, r := range reports {
		report = append(report, r...)
	}

	// 定级结果显式外发：取报告中最高的 level_id（L1~L5 词表）。
	// 下游若拿不到该级别必须 fail-closed，绝不允许回退到某个默认算子（P1-1）。
	levelIDs := make([]string, 0, len(report))
	for _, entry := range report {
		if id, ok := entry["level_id"].(string); ok {
			levelIDs = append(levelIDs, id)
		}
	}
	overallLevel := naming.MaxSecurityLevelID(levelIDs...)

	// 3. 计算国密 SM3 存证指纹（与全链路存证哈希口径一致，P2-3）
	rawBytes, _ := json.Marshal(records)
	inputHash := crypto.SumSM3Hex(rawBytes)

	sanitizedBytes, _ := json.Marshal(sanitized)
	outputHash := crypto.SumSM3Hex(sanitizedBytes)

	summary := map[string]interface{}{
		"total_records":        len(records),
		"classification_count": len(report),
		"input_hash":           inputHash,
		"output_hash":          outputHash,
		"api_code":             apiCode,
		"datasource_id":        datasourceID,
		"engine":               "go",
		"overall_level":        overallLevel,
	}

	return &AgentProcessResult{
		ClassificationReport: report,
		SanitizedData:        sanitized,
		Summary:              summary,
		Level:                overallLevel,
	}, nil
}

// ProcessMedicalData 医疗数据流水线处理（兼容别名）。
func (s *PrivacyService) ProcessMedicalData(records []map[string]interface{}) (*AgentProcessResult, error) {
	return s.ProcessAgentData(records, naming.API1Yibao, naming.DSYibao)
}

// ──────────────────────────────────────────────
// 文件上传脱敏处理 API (P1)
// ──────────────────────────────────────────────

// ProcessFile 解析 CSV/JSON 数据文件并执行 DataFrame 脱敏或 K-匿名。
func (s *PrivacyService) ProcessFile(content []byte, filename, operation string, options map[string]interface{}) (map[string]interface{}, error) {
	name := strings.ToLower(filename)
	var records []map[string]string

	switch {
	case strings.HasSuffix(name, ".csv"):
		cleanContent := bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
		r := csv.NewReader(bytes.NewReader(cleanContent))
		rows, err := r.ReadAll()
		if err != nil {
			return nil, fmt.Errorf("CSV parse error: %w", err)
		}
		if len(rows) < 1 {
			return nil, fmt.Errorf("CSV file is empty")
		}
		headers := rows[0]
		records = make([]map[string]string, 0, len(rows)-1)
		for _, row := range rows[1:] {
			rec := make(map[string]string, len(headers))
			for i, h := range headers {
				if i < len(row) {
					rec[h] = row[i]
				} else {
					rec[h] = ""
				}
			}
			records = append(records, rec)
		}
	case strings.HasSuffix(name, ".json"):
		var rawList []map[string]interface{}
		if err := json.Unmarshal(content, &rawList); err != nil {
			return nil, fmt.Errorf("JSON parse error: %w", err)
		}
		records = make([]map[string]string, 0, len(rawList))
		for _, m := range rawList {
			rec := make(map[string]string, len(m))
			for k, v := range m {
				rec[k] = fmt.Sprintf("%v", v)
			}
			records = append(records, rec)
		}
	case strings.HasSuffix(name, ".xlsx") || strings.HasSuffix(name, ".xls"):
		xlsxRecords, err := ParseXLSXRecords(content)
		if err != nil {
			return nil, fmt.Errorf("Excel parse error: %w", err)
		}
		records = xlsxRecords
	default:
		return nil, fmt.Errorf("unsupported file type: %s (supported: .csv, .json, .xlsx, .xls)", filename)
	}

	rowsIn := len(records)
	var result interface{}

	switch operation {
	case "mask_dataframe":
		colsFilter := extractColumnFilter(options)

		masked := make([]map[string]string, len(records))
		for i, rec := range records {
			m := make(map[string]string, len(rec))
			for k, v := range rec {
				if len(colsFilter) == 0 || colsFilter[k] {
					m[k] = masking.MaskValue(k, v)
				} else {
					m[k] = v
				}
			}
			masked[i] = m
		}
		result = masked

	case "k_anonymize":
		var qiCols []string
		if cols, ok := options["qi_cols"].([]interface{}); ok {
			for _, c := range cols {
				qiCols = append(qiCols, fmt.Sprintf("%v", c))
			}
		} else if colsStr, ok := options["qi_cols"].([]string); ok {
			qiCols = colsStr
		}
		if len(qiCols) == 0 {
			return nil, fmt.Errorf("k_anonymize operation requires qi_cols")
		}

		k := 5
		if kVal, ok := options["k"].(float64); ok && kVal >= 2 {
			k = int(kVal)
		} else if kVal, ok := options["k"].(int); ok && kVal >= 2 {
			k = kVal
		}

		kanoRecords := make([]kano.Record, len(records))
		for i, r := range records {
			kanoRecords[i] = kano.Record(r)
		}
		anonRes, err := kano.Anonymize(kanoRecords, qiCols, k)
		if err != nil {
			return nil, fmt.Errorf("k-anonymize error: %w", err)
		}
		result = anonRes.Records

	default:
		return nil, fmt.Errorf("unsupported operation: %s (supported: mask_dataframe, k_anonymize)", operation)
	}

	rowsOut := rowsIn
	if list, ok := result.([]map[string]string); ok {
		rowsOut = len(list)
	} else if list, ok := result.([]kano.Record); ok {
		rowsOut = len(list)
	}

	return map[string]interface{}{
		"operation": operation,
		"rows_in":   rowsIn,
		"rows_out":  rowsOut,
		"result":    result,
	}, nil
}

// ──────────────────────────────────────────────
// 流式文件处理（恒定内存）
// ──────────────────────────────────────────────

// maxProcessFileBytes 单次文件处理的字节硬上限，与 REST 层 50MB multipart 上限对齐。
// 声明为变量仅为便于测试下调阈值以校验硬上限行为；生产路径不会修改。
var maxProcessFileBytes int64 = 50 << 20

const (
	// streamBatchSize 流式脱敏的恒定内存窗口：每批最多缓存的行数。
	streamBatchSize = 2048
	// streamParallelMinRows 批次行数达到该阈值才启用多核分块，低于则单趟串行。
	streamParallelMinRows = 512
	// streamMaxWorkers 流式脱敏并发上限。
	streamMaxWorkers = 16
)

// ErrFileTooLarge 流式读取超出字节硬上限，供 REST 层映射为 413。
var ErrFileTooLarge = fmt.Errorf("file exceeds %d bytes limit", maxProcessFileBytes)

// ProcessFileStream 以流式（恒定内存）方式解析数据文件并执行脱敏。
//
// 相比 ProcessFile 的「全量物化 → 全量解析 → 全量副本」三阶内存放大（峰值可达文件
// 体积的 4~6 倍），CSV / JSON 的 mask_dataframe 组合走逐行解码、逐行脱敏的单趟流水线，
// 峰值内存仅 O(批次窗口 + 结果集)。需要全局视野的算法（k_anonymize 的 Mondrian 划分）
// 与 XLSX 仍回退到 ProcessFile 既有语义。两条路径的输出结构与字段完全一致。
func (s *PrivacyService) ProcessFileStream(r io.Reader, filename, operation string, options map[string]interface{}) (map[string]interface{}, error) {
	name := strings.ToLower(filename)
	colsFilter := extractColumnFilter(options)

	switch {
	case strings.HasSuffix(name, ".csv") && operation == "mask_dataframe":
		return streamMaskCSV(r, colsFilter)
	case strings.HasSuffix(name, ".json") && operation == "mask_dataframe":
		return streamMaskJSON(r, colsFilter)
	default:
		content, err := io.ReadAll(io.LimitReader(r, maxProcessFileBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read file error: %w", err)
		}
		if int64(len(content)) > maxProcessFileBytes {
			return nil, ErrFileTooLarge
		}
		return s.ProcessFile(content, filename, operation, options)
	}
}

// extractColumnFilter 解析 options["columns"] 为列名集合过滤器。
// JSON 反序列化得到 []interface{}，Go 侧调用方可直接传 []string。
func extractColumnFilter(options map[string]interface{}) map[string]bool {
	colsFilter := make(map[string]bool)
	switch cols := options["columns"].(type) {
	case []interface{}:
		for _, c := range cols {
			colsFilter[fmt.Sprintf("%v", c)] = true
		}
	case []string:
		for _, c := range cols {
			colsFilter[c] = true
		}
	}
	return colsFilter
}

// cappedReader 为流式读取施加字节硬上限，防止超大文件绕过物化路径的容量校验。
type cappedReader struct {
	r    io.Reader
	n    int64
	over bool
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.over {
		return 0, ErrFileTooLarge
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > maxProcessFileBytes {
		c.over = true
	}
	return n, err
}

func streamResult(rows int, result interface{}) map[string]interface{} {
	return map[string]interface{}{
		"operation": "mask_dataframe",
		"rows_in":   rows,
		"rows_out":  rows,
		"result":    result,
	}
}

// streamMaskCSV 单趟流式读取 CSV 并按列脱敏，恒定内存窗口分批多核计算。
func streamMaskCSV(r io.Reader, colsFilter map[string]bool) (map[string]interface{}, error) {
	rd := &cappedReader{r: r}
	br := bufio.NewReader(rd)
	// 剥离 UTF-8 BOM，与 ProcessFile 物化路径语义一致
	if head, err := br.Peek(3); err == nil && string(head) == "\xef\xbb\xbf" {
		_, _ = br.Discard(3)
	}

	cr := csv.NewReader(br)
	headers, err := cr.Read()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("CSV file is empty")
		}
		if rd.over {
			return nil, ErrFileTooLarge
		}
		return nil, fmt.Errorf("CSV parse error: %w", err)
	}

	masked := make([]map[string]string, 0, 512)
	batch := make([][]string, 0, streamBatchSize)
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			if rd.over {
				return nil, ErrFileTooLarge
			}
			return nil, fmt.Errorf("CSV parse error: %w", err)
		}
		batch = append(batch, row)
		if len(batch) == streamBatchSize {
			masked = append(masked, maskCSVBatch(batch, headers, colsFilter)...)
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		masked = append(masked, maskCSVBatch(batch, headers, colsFilter)...)
	}
	if rd.over {
		return nil, ErrFileTooLarge
	}
	return streamResult(len(masked), masked), nil
}

// maskCSVBatch 将一批 CSV 行映射为列名→脱敏值的多核并发结果（按索引写回，无锁无竞争）。
func maskCSVBatch(rows [][]string, headers []string, colsFilter map[string]bool) []map[string]string {
	out := make([]map[string]string, len(rows))
	forEachChunked(len(rows), func(start, end int) {
		for i := start; i < end; i++ {
			row := rows[i]
			rec := make(map[string]string, len(headers))
			for j, h := range headers {
				v := ""
				if j < len(row) {
					v = row[j]
				}
				if len(colsFilter) == 0 || colsFilter[h] {
					v = masking.MaskValue(h, v)
				}
				rec[h] = v
			}
			out[i] = rec
		}
	})
	return out
}

// streamMaskJSON 以 json.Decoder 令牌流逐对象解码并脱敏，避免整档物化。
func streamMaskJSON(r io.Reader, colsFilter map[string]bool) (map[string]interface{}, error) {
	rd := &cappedReader{r: r}
	dec := json.NewDecoder(bufio.NewReader(rd))

	tok, err := dec.Token()
	if err != nil {
		if rd.over {
			return nil, ErrFileTooLarge
		}
		if err == io.EOF {
			return nil, fmt.Errorf("JSON parse error: unexpected end of JSON input")
		}
		return nil, fmt.Errorf("JSON parse error: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("JSON parse error: expected top-level array of objects")
	}

	masked := make([]map[string]string, 0, 512)
	batch := make([]map[string]interface{}, 0, streamBatchSize)
	for dec.More() {
		var raw map[string]interface{}
		if err := dec.Decode(&raw); err != nil {
			if rd.over {
				return nil, ErrFileTooLarge
			}
			return nil, fmt.Errorf("JSON parse error: %w", err)
		}
		batch = append(batch, raw)
		if len(batch) == streamBatchSize {
			masked = append(masked, maskJSONBatch(batch, colsFilter)...)
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		masked = append(masked, maskJSONBatch(batch, colsFilter)...)
	}
	// 消费闭合 ']' 并确认无尾部脏数据（与 json.Unmarshal 整档校验语义对齐）
	if _, err := dec.Token(); err != nil {
		if rd.over {
			return nil, ErrFileTooLarge
		}
		return nil, fmt.Errorf("JSON parse error: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		// 输入被硬上限截断时，长度错误优先于解析/尾部错误
		if rd.over {
			return nil, ErrFileTooLarge
		}
		return nil, fmt.Errorf("JSON parse error: unexpected trailing data")
	}
	if rd.over {
		return nil, ErrFileTooLarge
	}
	return streamResult(len(masked), masked), nil
}

// maskJSONBatch 一批 JSON 对象的多核并发字段脱敏。
func maskJSONBatch(rows []map[string]interface{}, colsFilter map[string]bool) []map[string]string {
	out := make([]map[string]string, len(rows))
	forEachChunked(len(rows), func(start, end int) {
		for i := start; i < end; i++ {
			raw := rows[i]
			rec := make(map[string]string, len(raw))
			for k, v := range raw {
				val := fmt.Sprintf("%v", v)
				if len(colsFilter) == 0 || colsFilter[k] {
					val = masking.MaskValue(k, val)
				}
				rec[k] = val
			}
			out[i] = rec
		}
	})
	return out
}

// forEachChunked 将 [0,n) 划分为至多 streamMaxWorkers 段连续区间并发执行 fn，
// 各段按索引写回互不重叠（无锁）；n 低于阈值时单趟串行，避免 goroutine 调度开销倒挂。
func forEachChunked(n int, fn func(start, end int)) {
	workers := 1
	if n >= streamParallelMinRows {
		workers = runtime.NumCPU()
		if workers > streamMaxWorkers {
			workers = streamMaxWorkers
		}
	}
	if workers <= 1 {
		fn(0, n)
		return
	}
	chunk := (n + workers - 1) / workers
	var wg sync.WaitGroup
	for start := 0; start < n; start += chunk {
		end := start + chunk
		if end > n {
			end = n
		}
		wg.Add(1)
		go func(s, e int) {
			defer wg.Done()
			fn(s, e)
		}(start, end)
	}
	wg.Wait()
}

// ──────────────────────────────────────────────
// 运维诊断 API (P1)
// ──────────────────────────────────────────────

// Diagnostics 返回 Go 原生引擎的运维诊断与降级链路状态。
//
// P1-3：NER 能力口径必须来自实际装配的引擎，不得宣称未交付的模型能力。
func (s *PrivacyService) Diagnostics(refresh bool) map[string]interface{} {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// 真实代码状态：Layer 2 当前装配的是哪个 NerEngine、是否为模型驱动。
	// 默认装配 NewRuleBasedNerEngine()（正则桩），故交付构建中 ner_available=false。
	nerBackend, nerAvailable := "none", false
	if s.funnel != nil {
		nerBackend, nerAvailable = s.funnel.NerStatus()
	}

	return map[string]interface{}{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		// P1-3：AI/模型级分类能力是否真实可用的显式口径。
		"ner_available": nerAvailable,
		"ner_backend":   nerBackend,
		// P0-2 / P2-2：安全底线与字段级默认拒绝策略的生效快照（可审计）。
		"safety_floor": s.safetyFloorDiagnostics(),
		"service": map[string]interface{}{
			"name":       pkgconfig.EnvString("PRIVACY_SERVICE_NAME", "PrivShield"),
			"engine":     "go",
			"namespace":  pkgconfig.EnvString("PRIVACY_NAMESPACE", "default"),
			"version":    "1.0.0",
			"go_version": runtime.Version(),
			"rest_port":  pkgconfig.EnvInt("PRIVACY_REST_PORT", 8079),
			"grpc_port":  pkgconfig.EnvInt("PRIVACY_GRPC_PORT", 50051),
		},
		"engines": map[string]interface{}{
			"ner": map[string]interface{}{
				"active_engine": nerBackend,
				"available":     nerAvailable,
				"determined_by": "funnel.NerStatus",
				"note":          "ONNX/CUDA NER 模型未交付（无 CGO 绑定），Layer 2 实际运行正则降级桩 rule-based-ner",
				"degradation_chain": []map[string]interface{}{
					{"engine": "cuda_onnx", "available": false, "reason": "CUDA driver / GPU 未挂载，且 ONNX Runtime CGO 绑定未引入", "note": "Go+CUDA 异步批推理引擎（骨架）"},
					{"engine": "onnx", "available": false, "reason": "ONNX 模型未交付，骨架实现从不加载模型", "note": "纯 Go / ONNX 推理引擎（骨架）"},
					{"engine": "rule-based-ner", "available": true, "reason": nil, "note": "正则实体桩：当前实际装配的 Layer 2 实现，不具备模型语义泛化能力"},
				},
			},
			"llm": s.llmDiagnostics(),
		},
		"standards": s.standardsDiagnostics(),
		"dependencies": []map[string]interface{}{
			{"name": "onnxruntime_go", "installed": false, "purpose": "NER ONNX/CUDA 推理引擎", "install": "go get github.com/yalue/onnxruntime_go"},
			{"name": "gin", "installed": true, "purpose": "高性能 REST API 框架", "install": "go get github.com/gin-gonic/gin"},
			{"name": "grpc", "installed": true, "purpose": "高性能 RPC 框架", "install": "go get google.golang.org/grpc"},
			{"name": "prometheus", "installed": true, "purpose": "生产级指标监控", "install": "go get github.com/prometheus/client_golang"},
		},
		"models": []map[string]interface{}{
			{"name": "NER ONNX 模型（CMeEE）", "path": ".models/raner_cmeee.onnx", "exists": fileExists(".models/raner_cmeee.onnx")},
			{"name": "NER 词表 vocab.txt", "path": ".models/vocab.txt", "exists": fileExists(".models/vocab.txt")},
		},
		"hardware": map[string]interface{}{
			"platform":         runtime.GOOS,
			"machine":          runtime.GOARCH,
			"num_cpu":          runtime.NumCPU(),
			"num_goroutines":   runtime.NumGoroutine(),
			"memory_alloc_mb":  float64(memStats.Alloc) / 1024 / 1024,
			"memory_sys_mb":    float64(memStats.Sys) / 1024 / 1024,
			"cuda_available":   false,
			"nvidia_smi_found": false,
		},
	}
}

// ──────────────────────────────────────────────
// LLM 健康状态探测
// ──────────────────────────────────────────────

// LLMStatus 返回 LLM 客户端配置与可用性状态。
func (s *PrivacyService) LLMStatus(ctx context.Context) (configured, available bool) {
	s.mu.RLock()
	funnel := s.funnel
	s.mu.RUnlock()
	if funnel == nil {
		return false, false
	}
	return funnel.LLMStatus(ctx)
}

// ──────────────────────────────────────────────
// Deep Health Check (P22)
// ──────────────────────────────────────────────

// ComponentHealth 组件健康状态
type ComponentHealth struct {
	Status  string `json:"status"`            // "ok" | "degraded" | "down"
	Message string `json:"message,omitempty"` // 可选描述
}

// DeepHealthCheck 返回细粒度组件级健康快照。
func (s *PrivacyService) DeepHealthCheck() map[string]interface{} {
	components := make(map[string]ComponentHealth)
	overallStatus := "ok"

	// 1. budget_store — 隐私预算存储
	remaining := s.budget.RemainingEpsilon()
	total := s.budget.TotalEpsilon()
	if remaining <= 0 {
		components["budget_store"] = ComponentHealth{Status: "down", Message: "privacy budget exhausted"}
		overallStatus = "degraded"
	} else if remaining < total*0.1 {
		components["budget_store"] = ComponentHealth{Status: "degraded", Message: fmt.Sprintf("budget low: %.4f/%.4f epsilon remaining", remaining, total)}
	} else {
		components["budget_store"] = ComponentHealth{Status: "ok"}
	}

	// 2. rules_loaded — 规则引擎
	if engine := s.classifier.Load(); engine != nil && engine.RuleCount() > 0 {
		components["rules_loaded"] = ComponentHealth{
			Status:  "ok",
			Message: fmt.Sprintf("%d rules active", engine.RuleCount()),
		}
	} else {
		components["rules_loaded"] = ComponentHealth{Status: "degraded", Message: "no classification rules loaded"}
	}

	// 3. classification_cache — 分类缓存
	if s.funnel != nil {
		hits, misses, size := s.funnel.CacheStats()
		hitRate := 0.0
		if hits+misses > 0 {
			hitRate = float64(hits) / float64(hits+misses) * 100
		}
		components["classification_cache"] = ComponentHealth{
			Status:  "ok",
			Message: fmt.Sprintf("size=%d, hit_rate=%.1f%%", size, hitRate),
		}
	} else {
		components["classification_cache"] = ComponentHealth{Status: "ok", Message: "no funnel"}
	}

	// 4. llm_cluster — LLM 集群就绪状态
	components["llm_cluster"] = ComponentHealth{Status: "ok", Message: "not_configured"}

	// 5. ner_engine — NER 引擎状态（P1-3：按实际装配引擎上报，正则桩不等同于模型能力）
	if s.funnel != nil {
		backend, modelBacked := s.funnel.NerStatus()
		if modelBacked {
			components["ner_engine"] = ComponentHealth{Status: "ok", Message: backend}
		} else {
			components["ner_engine"] = ComponentHealth{
				Status:  "ok",
				Message: backend + " (regex stand-in, ONNX model not delivered)",
			}
		}
	} else {
		components["ner_engine"] = ComponentHealth{Status: "ok", Message: "not_wired"}
	}

	// 6. safety_floor — 安全底线
	if s.safetyFloor != nil {
		components["safety_floor"] = ComponentHealth{Status: "ok"}
	} else {
		components["safety_floor"] = ComponentHealth{Status: "degraded", Message: "safety floor not initialized"}
		overallStatus = "degraded"
	}

	return map[string]interface{}{
		"status":     overallStatus,
		"components": components,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	}
}

// ──────────────────────────────────────────────
// K-匿名表级与 DataFrame API (P1)
// ──────────────────────────────────────────────

// KAnonymizeTable 表级 K-匿名（Mondrian 算法）。
func (s *PrivacyService) KAnonymizeTable(rows []kano.Record, qiCols []string, k int) (*kano.AnonymizationResult, error) {
	return kano.Anonymize(rows, qiCols, k)
}

// KAnonymizeRecord 单条记录 K-匿名层次泛化。
func (s *PrivacyService) KAnonymizeRecord(record kano.Record, qiCols []string, k int) (kano.Record, error) {
	return kano.AnonymizeRecord(record, qiCols, nil, k)
}

// KAnonymizeDataFrame 结构化 DataFrame K-匿名。
func (s *PrivacyService) KAnonymizeDataFrame(records []map[string]interface{}, qiCols []string, k int) ([]map[string]interface{}, error) {
	kanoRows := make([]kano.Record, len(records))
	for i, r := range records {
		kr := make(kano.Record, len(r))
		for k, v := range r {
			kr[k] = fmt.Sprintf("%v", v)
		}
		kanoRows[i] = kr
	}

	result, err := kano.Anonymize(kanoRows, qiCols, k)
	if err != nil {
		return nil, err
	}

	out := make([]map[string]interface{}, len(result.Records))
	for i, r := range result.Records {
		m := make(map[string]interface{}, len(r))
		for k, v := range r {
			m[k] = v
		}
		out[i] = m
	}
	return out, nil
}

// ──────────────────────────────────────────────
// 内部辅助
// ──────────────────────────────────────────────

func (s *PrivacyService) autoMaskField(fieldName, value string) string {
	masked := masking.MaskValue(fieldName, value)
	if masked != value || value == "" {
		return masked
	}
	// P0-2 默认拒绝：通用掩码路径未能遮蔽任何字符时（如空值或掩码返回原值），
	// 未列入字段规格矩阵的字段不得明文出域。
	s.mu.RLock()
	policy := s.unlistedFloor.Policy
	s.mu.RUnlock()
	return medical.SanitizeUnlistedField(policy, value)
}

// defaultRules 默认分类规则
func defaultRules() []dynclassification.RuleDef {
	return []dynclassification.RuleDef{
		{
			ID:            "id_card",
			Level:         dynclassification.LevelSecret,
			Category:      "pii.identity",
			FieldPatterns: []string{`(?i)(id_?card|身份证|identity|cert_no)`},
			Description:   "中国居民身份证",
		},
		{
			ID:            "phone",
			Level:         dynclassification.LevelConfidential,
			Category:      "pii.contact",
			FieldPatterns: []string{`(?i)(phone|mobile|手机|电话|tel)`},
			Description:   "手机号码",
		},
		{
			ID:            "email",
			Level:         dynclassification.LevelConfidential,
			Category:      "pii.contact",
			FieldPatterns: []string{`(?i)(email|邮箱|邮件|mail)`},
			Description:   "电子邮箱",
		},
		{
			ID:            "bank_card",
			Level:         dynclassification.LevelSecret,
			Category:      "pii.financial",
			FieldPatterns: []string{`(?i)(bank_?card|银行卡|信用卡|credit_card)`},
			Description:   "银行卡号",
		},
		{
			ID:            "name",
			Level:         dynclassification.LevelConfidential,
			Category:      "pii.identity",
			FieldPatterns: []string{`(?i)(^name$|patient_name|user_name|姓名)`},
			Description:   "个人姓名",
		},
		{
			ID:            "address",
			Level:         dynclassification.LevelConfidential,
			Category:      "pii.location",
			FieldPatterns: []string{`(?i)(address|地址|住址|home_address)`},
			Description:   "个人地址",
		},
		{
			ID:            "medical_record",
			Level:         dynclassification.LevelSecret,
			Category:      "medical.record",
			FieldPatterns: []string{`(?i)(medical_record|病历|诊断|diagnosis)`},
			Description:   "医疗记录",
		},
		{
			ID:            "social_security",
			Level:         dynclassification.LevelTopSecret,
			Category:      "pii.financial",
			FieldPatterns: []string{`(?i)(social_security|社保|医保号)`},
			Description:   "社保号码",
		},
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// ──────────────────────────────────────────────
// 动态 Profile 与推荐 API
// ──────────────────────────────────────────────

// RecommendParams 根据输入样本数据推荐 DP 和 K-Anonymity 参数并持久化。
func (s *PrivacyService) RecommendParams(namespace string, values []float64, rows []map[string]interface{}, qiCols []string) map[string]interface{} {
	if namespace == "" {
		namespace = s.namespace
	}
	if s.resolver != nil {
		return s.resolver.RecommendDataParams(namespace, values, rows, qiCols)
	}
	return map[string]interface{}{
		"recommended_profile": "standard",
		"epsilon":             1.0,
		"delta":               1e-5,
		"k":                   5,
	}
}

// ReloadDynamicProfiles 重新加载动态分类规则与隐私策略配置。
// 路径从 Config 中读取（支持环境变量 PRIVACY_CONFIG_FILE / PRIVACY_RULES_DIR），不再硬编码。
func (s *PrivacyService) ReloadDynamicProfiles() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolver != nil {
		_ = s.resolver.LoadFromYAML(s.privacyYAML)
	}
	// 规则热更新：基线规则 + 领域规则目录（含字段规格矩阵）整体重排，
	// 避免旧实现「只用目录规则」把调用方传入的自定义规则丢掉。
	if newEngine, err := dynclassification.NewRuleEngine(mergeDomainRules(s.baseRules, s.rulesDir)); err == nil {
		s.classifier.Store(newEngine)
	}
	// P2-2 + P0-2：安全底线与默认拒绝策略随配置热更新。
	s.rebindPolicyLocked()
	if s.funnel != nil {
		s.funnel.ClearCache()
	}
	return nil
}

// rebindPolicyLocked 重新读取 config/privacy.yaml 并把 safety_floor.* /
// unlisted_field_policy 应用到仲裁器与医疗流水线。**调用方必须持有 s.mu 写锁**。
//
// 注意：漏斗 Layer 2/3 的阈值（classification.confidence_threshold / enable_llm）
// 在 dynclassification 中无导出setter，只能在新建服务实例时生效；
// 服务层底线仲裁对每条结果即时生效，因此 min_level 与默认拒绝下限是热更新可用的。
func (s *PrivacyService) rebindPolicyLocked() {
	policy, err := loadPrivacyPolicy(s.privacyYAML)
	if err != nil {
		slog.Warn("privacy policy reload failed, keeping previous binding", "path", s.privacyYAML, "error", err)
		return
	}
	sfCfg := policy.SafetyFloor.applyToSafetyFloorConfig(s.safetyFloorConfig)
	floor := policy.SafetyFloor.resolveUnlistedFloor()
	s.safetyFloorConfig = sfCfg
	s.unlistedFloor = floor
	s.policyBound = true
	if s.safetyFloor != nil {
		s.safetyFloor.UpdateConfig(sfCfg)
	}
	if s.medicalYibao != nil {
		s.medicalYibao.SetUnlistedFieldPolicy(floor.Policy)
	}
	if s.medicalKang != nil {
		s.medicalKang.SetUnlistedFieldPolicy(floor.Policy)
	}
	slog.Info("privacy policy rebound",
		"path", s.privacyYAML,
		"min_level", string(sfCfg.MinLevel),
		"unlisted_field_policy", floor.Name,
		"unlisted_disposition", string(floor.Policy),
		"unlisted_min_level", floor.levelLabel(),
	)
}

// ──────────────────────────────────────────────
// 高级差分隐私 API
// ──────────────────────────────────────────────

// DPAdaptiveClip 执行自适应分位数截断估计。
func (s *PrivacyService) DPAdaptiveClip(values []float64, epsilon, targetQuantile float64, numIterations int, initialClip float64) (float64, float64) {
	if !s.budget.Consume(epsilon, 0) {
		return 0, 0
	}
	return dp.AdaptiveClip(values, epsilon, targetQuantile, numIterations, initialClip)
}

// DPGroupBy 执行带差分隐私的分组聚合统计。
func (s *PrivacyService) DPGroupBy(rows []map[string]string, groupCol, targetCol, agg string, epsilon, delta, clipLower, clipUpper float64, mechanism string) (map[string]float64, error) {
	if !s.budget.Consume(epsilon, delta) {
		return nil, fmt.Errorf("privacy budget exhausted")
	}
	return dp.GroupBy(rows, groupCol, targetCol, agg, epsilon, delta, clipLower, clipUpper, mechanism)
}

// DPAggregate 执行多指标差分隐私聚合计算。
func (s *PrivacyService) DPAggregate(rows []map[string]string, specs map[string]string, epsilon, delta, clipLower, clipUpper float64, mechanism string) (map[string]float64, error) {
	if !s.budget.Consume(epsilon, delta) {
		return nil, fmt.Errorf("privacy budget exhausted")
	}
	return dp.Aggregate(rows, specs, epsilon, delta, clipLower, clipUpper, mechanism)
}
