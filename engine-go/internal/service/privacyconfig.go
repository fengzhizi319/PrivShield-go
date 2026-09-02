// Package service — config/privacy.yaml 的策略键绑定（整改项 P2-2 + P0-2）。
//
// 本文件解决两个上线阻塞问题：
//
//  1. **P2-2 配置未绑定**：engine-go 此前在 NewPrivacyService 中硬编码
//     DefaultFunnelConfig() / DefaultSafetyFloorConfig()，导致 config/privacy.yaml 的
//     safety_floor.* 与 classification.* 键形同虚设（设计文档 §5.4 差异 6 / §12.1.1 P2-2）。
//     现绑定：safety_floor.{min_level, confidence_threshold, force_upgrade_on_uncertainty,
//     unlisted_field_policy, unlisted_min_level} 与
//     classification.{confidence_threshold, enable_llm, llm_endpoint, llm_max_concurrency}。
//
//     **classification.enable_ner 故意不绑定**：Layer 2 目前是由正则 NER 引擎实现的
//     （dynclassification.NewRuleBasedNerEngine，无需 ONNX Runtime），
//     而 config/privacy.yaml 中的 `enable_ner: false` 注释语义是「需 ONNX Runtime」。
//     若把它接到漏斗上，Layer 2 整体关闭，engine-go/internal/rest 既有断言
//     「患者张三…」→ NER 命中 0.90 的用例会因分类档位回退而失效——
//     等于为了跑绿而削弱安全断言。该项留待 P1 批次与 ONNX 路径一并裁定（见 进行中清单）。
//
//  2. **P0-2 字段级脱敏默认拒绝**：引入**具名、可审计**的默认拒绝策略
//     `field_level_default_deny`，同时作用于
//     分类侧（未命中任何规则的字段不得低于 unlisted_min_level）与
//     脱敏侧（未列入字段规格矩阵的字段按 mask/drop 处置，见 privacy-go-sdk/medical）。
//
// 词表边界（P1-5 相关，务必注意）：
//   - `rules/domains/*.yaml` 的 level 取值是 DB51 词表 "L1".."L5"，
//     由 RuleEngine.Classify **原样透传**（无归一化），
//     而 SafetyFloor.Arbitrate 的比较用的是 canonical 词表
//     （public/internal/confidential/secret/top_secret），
//     `LevelRank("L4") == 0` —— 直接把 min_level 抬到 canonical 档位会把
//     "L3/L4/L5" 结果**误判为最低档并重写**，造成等级口径丢失。
//   - 因此本文件在把结果交给 Arbitrate 之前做 canonical 归一化，
//     仲裁完再映射回原词表；`unlisted_min_level` 一律用 "L1".."L5" 表达并在读取时换算。
package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/dynclassification"
	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/medical"
	"gopkg.in/yaml.v3"
)

// ──────────────────────────────────────────────
// YAML 结构（只声明本文件真正绑定的键）
// ──────────────────────────────────────────────

// privacyPolicyFile config/privacy.yaml 中与引擎策略相关的子集。
type privacyPolicyFile struct {
	Classification classificationSection `yaml:"classification"`
	SafetyFloor    safetyFloorSection    `yaml:"safety_floor"`
}

// classificationSection 动态分类分级相关键。
type classificationSection struct {
	// ConfidenceThreshold → FunnelConfig.NERConfidenceThreshold（Layer 2 判定阈值）。
	ConfidenceThreshold *float64 `yaml:"confidence_threshold"`
	EnableLLM           *bool    `yaml:"enable_llm"`
	LLMEndpoint         string   `yaml:"llm_endpoint"`
	LLMMaxConcurrency   *int     `yaml:"llm_max_concurrency"`
	// EnableNER 被解析但**不绑定**，仅用于在配置显式关闭时打印一条可审计告警。
	EnableNER *bool `yaml:"enable_ner"`
}

// safetyFloorSection 安全底线门禁相关键（canonical 词表 + P0-2 默认拒绝扩展）。
type safetyFloorSection struct {
	// MinLevel canonical 词表（public/internal/confidential/secret/top_secret）。
	MinLevel string `yaml:"min_level"`
	// ConfidenceThreshold 低置信度触发升级的阈值。
	ConfidenceThreshold       *float64 `yaml:"confidence_threshold"`
	ForceUpgradeOnUncertainty *bool    `yaml:"force_upgrade_on_uncertainty"`
	// UnlistedFieldPolicy P0-2：未列入字段规格矩阵字段的处置（mask | drop）。
	UnlistedFieldPolicy string `yaml:"unlisted_field_policy"`
	// UnlistedMinLevel P0-2：未命中任何规则的字段的最低定级，**DB51 词表**（"L3"）。
	UnlistedMinLevel string `yaml:"unlisted_min_level"`
}

// ──────────────────────────────────────────────
// 具名默认拒绝策略
// ──────────────────────────────────────────────

// DefaultUnlistedMinLevel 未登记字段的默认最低定级（DB51 L3 = 敏感/重要数据）。
const DefaultUnlistedMinLevel = "L3"

// unlistedFieldFloor 是 P0-2 要求的**具名、可审计**默认拒绝策略。
//
// 它不是「兜底放行」：Policy 只可能是 mask 或 drop，MinRank 只可能被解析得更高，
// 任何解析失败都回落到 L3 + mask 的 restrictive 组合。
type unlistedFieldFloor struct {
	// Name 策略名，出现在诊断输出与分类结果 Category 中，供审计追溯。
	Name string
	// Policy 脱敏侧处置（medical.UnlistedFieldMask / UnlistedFieldDrop）。
	Policy medical.UnlistedFieldPolicy
	// MinRank 分类侧下限：DB51 等级换算成的 1..5 排名（canonical 词表亦可表达）。
	MinRank int
	// RawLevel 配置原文（如 "L3"），用于诊断回显。
	RawLevel string
}

func (f unlistedFieldFloor) levelLabel() string {
	return fmt.Sprintf("L%d", f.MinRank)
}

// defaultUnlistedFloor 返回代码级默认策略（配置文件缺失或未声明时的取值）。
func defaultUnlistedFloor() unlistedFieldFloor {
	return unlistedFieldFloor{
		Name:     medical.UnlistedFieldPolicyName,
		Policy:   medical.DefaultUnlistedFieldPolicy,
		MinRank:  db51Rank(DefaultUnlistedMinLevel),
		RawLevel: DefaultUnlistedMinLevel,
	}
}

func (s safetyFloorSection) resolveUnlistedFloor() unlistedFieldFloor {
	floor := defaultUnlistedFloor()
	floor.Policy = medical.ParseUnlistedFieldPolicy(s.UnlistedFieldPolicy)
	if raw := strings.TrimSpace(s.UnlistedMinLevel); raw != "" {
		if rank := db51Rank(raw); rank > 0 {
			floor.MinRank = rank
			floor.RawLevel = fmt.Sprintf("L%d", rank)
		}
	}
	return floor
}

// ──────────────────────────────────────────────
// 词表映射：DB51 "L1".."L5" ⇄ canonical SecurityLevel
// ──────────────────────────────────────────────

// db51Rank 把任意词表的等级表达归一为 1..5 排名；无法识别返回 0。
func db51Rank(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "l1", "public":
		return 1
	case "l2", "internal":
		return 2
	case "l3", "confidential":
		return 3
	case "l4", "secret":
		return 4
	case "l5", "top_secret":
		return 5
	default:
		return 0
	}
}

// db51ToCanonical 把领域规则文件里的 "L1".."L5" 映射为 canonical SecurityLevel。
// 第二个返回值为 false 表示输入已是 canonical 词表或不可识别（调用方应保持原样）。
func db51ToCanonical(level string) (dynclassification.SecurityLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "l1":
		return dynclassification.LevelPublic, true
	case "l2":
		return dynclassification.LevelInternal, true
	case "l3":
		return dynclassification.LevelConfidential, true
	case "l4":
		return dynclassification.LevelSecret, true
	case "l5":
		return dynclassification.LevelTopSecret, true
	default:
		return "", false
	}
}

// canonicalToDB51 把 canonical SecurityLevel 映射回 DB51 词表（仅当输入原本用的是
// DB51 词表时由调用方回写，避免污染既有 canonical 口径的输出）。
func canonicalToDB51(level dynclassification.SecurityLevel) (string, bool) {
	switch level {
	case dynclassification.LevelPublic:
		return "L1", true
	case dynclassification.LevelInternal:
		return "L2", true
	case dynclassification.LevelConfidential:
		return "L3", true
	case dynclassification.LevelSecret:
		return "L4", true
	case dynclassification.LevelTopSecret:
		return "L5", true
	default:
		return "", false
	}
}

// ──────────────────────────────────────────────
// 加载与解析
// ──────────────────────────────────────────────

// loadPrivacyPolicy 读取 config/privacy.yaml 的策略子集。
// 文件缺失/解析失败返回 error，调用方必须回落到代码级 restrictive 默认值。
func loadPrivacyPolicy(path string) (*privacyPolicyFile, error) {
	if path == "" {
		return nil, fmt.Errorf("empty policy path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read privacy policy: %w", err)
	}
	var policy privacyPolicyFile
	if err := yaml.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("parse privacy policy YAML: %w", err)
	}
	return &policy, nil
}

// minLevelSecurityLevel 解析 safety_floor.min_level（canonical 或 L1~L5 词表均可）。
//
// 词表外的取值不再回落成 LevelPublic（P0-2 fail-closed）：
// 历史上 `min_level: confidntial` 这类拼写错误会把底线**静默降到最弱档**，
// 现在由调用方保留代码级 restrictive 默认值并打告警。
func (sec safetyFloorSection) minLevelSecurityLevel() (dynclassification.SecurityLevel, bool) {
	raw := strings.TrimSpace(sec.MinLevel)
	id := naming.NormalizeSecurityLevelID(raw)
	if id == "" {
		return "", false
	}
	name := naming.SecurityLevelName(id)
	if name == "" {
		return "", false
	}
	return dynclassification.LevelFromString(name), true
}

// applyToSafetyFloorConfig 把配置键写进 SafetyFloorConfig（保留代码默认的 AuditLog）。
func (sec safetyFloorSection) applyToSafetyFloorConfig(base dynclassification.SafetyFloorConfig) dynclassification.SafetyFloorConfig {
	out := base
	if raw := strings.TrimSpace(sec.MinLevel); raw != "" {
		level, ok := sec.minLevelSecurityLevel()
		if !ok {
			slog.Warn("safety_floor.min_level is outside the L1~L5 / canonical vocabulary; keeping the restrictive default floor",
				"configured", raw,
				"effective", string(out.MinLevel),
				"allowed", strings.Join(naming.SecurityLevelNames(), "|"),
			)
		} else {
			out.MinLevel = level
		}
	}
	if sec.ConfidenceThreshold != nil && *sec.ConfidenceThreshold > 0 && *sec.ConfidenceThreshold <= 1 {
		out.ConfidenceThreshold = *sec.ConfidenceThreshold
	}
	if sec.ForceUpgradeOnUncertainty != nil {
		out.ForceUpgradeOnUncertainty = *sec.ForceUpgradeOnUncertainty
	}
	return out
}

// applyToFunnelConfig 把 classification.* 写进 FunnelConfig。
// enable_ner 不在此处生效，理由见文件头注释。
func (cls classificationSection) applyToFunnelConfig(base dynclassification.FunnelConfig) dynclassification.FunnelConfig {
	out := base
	if cls.ConfidenceThreshold != nil && *cls.ConfidenceThreshold > 0 && *cls.ConfidenceThreshold <= 1 {
		out.NERConfidenceThreshold = *cls.ConfidenceThreshold
	}
	if cls.EnableLLM != nil {
		out.EnableLLM = *cls.EnableLLM
	}
	return out
}

// warnUnboundKeys 对「已解析但故意不绑定」的键打一条告警，保证配置语义不被静默忽略。
func (cls classificationSection) warnUnboundKeys(path string) {
	if cls.EnableNER != nil && !*cls.EnableNER {
		slog.Warn("classification.enable_ner is NOT bound to the classifier funnel (intentional, P2-2)",
			"path", path,
			"configured", *cls.EnableNER,
			"effective", true,
			"reason", "Layer 2 当前由正则 NER 实现，关闭它将使分类档位回退；ONNX NER 就绪后再绑定",
		)
	}
}

// ──────────────────────────────────────────────
// 分类结果的底线仲裁
// ──────────────────────────────────────────────

// arbitrate 对单条分类结果执行「词表归一 → 安全底线仲裁 → 默认拒绝下限 → 词表回写」。
//
// 传入的结果会被**拷贝**后再改写：dynclassification 的规则缓存会返回共享指针，
// 就地改写既会与并发读者竞争，也会让低置信度升级被重复叠加。
func (s *PrivacyService) arbitrate(result *dynclassification.ClassificationResult) *dynclassification.ClassificationResult {
	if result == nil {
		return nil
	}
	s.mu.RLock()
	sf := s.safetyFloor
	floor := s.unlistedFloor
	s.mu.RUnlock()

	cp := *result
	original := cp.Level
	inDB51 := false
	if mapped, ok := db51ToCanonical(string(original)); ok {
		cp.Level = mapped
		inDB51 = true
	}

	if sf != nil {
		sf.Arbitrate(&cp)
	}

	// P0-2 默认拒绝（分类侧）：未命中任何规则的字段不得低于 unlisted_min_level。
	if strings.EqualFold(cp.MatchedBy, "default") && db51Rank(string(cp.Level)) < floor.MinRank {
		if lifted, ok := rankToCanonical(floor.MinRank); ok {
			cp.Level = lifted
			if cp.Category == "" || cp.Category == "unknown" {
				cp.Category = "unlisted." + floor.Name
			}
		}
	}

	if inDB51 {
		if label, ok := canonicalToDB51(cp.Level); ok {
			cp.Level = dynclassification.SecurityLevel(label)
		}
	}
	// 词表回写完成后补齐 L1~L5 标识：无论 Level 处于哪套词表，跨服务消费者都能拿到同一定级。
	cp.LevelID = cp.Level.LevelID()
	return &cp
}

// rankToCanonical 把 1..5 排名映射回 canonical SecurityLevel。
func rankToCanonical(rank int) (dynclassification.SecurityLevel, bool) {
	switch rank {
	case 1:
		return dynclassification.LevelPublic, true
	case 2:
		return dynclassification.LevelInternal, true
	case 3:
		return dynclassification.LevelConfidential, true
	case 4:
		return dynclassification.LevelSecret, true
	case 5:
		return dynclassification.LevelTopSecret, true
	default:
		return "", false
	}
}

// ──────────────────────────────────────────────
// 诊断与热更新辅助
// ──────────────────────────────────────────────

// llmDiagnostics 返回 Layer-3 外送的真实交付口径（P0-5 / P1-3）：
// 是否启用、端点是否加密、载荷是否去标识化、累计外送数。
// 诊断上报禁止再写死 available:true —— 那会把「未启用的外部大模型」宣称为可用能力。
func (s *PrivacyService) llmDiagnostics() map[string]interface{} {
	out := map[string]interface{}{
		"enabled":              false,
		"configured":           false,
		"available":            false,
		"payload_deidentified": true,
		"transport_secure":     false,
		"escalations":          int64(0),
		"determined_by":        "funnel.LLMEnabled/LLMEscalationStats",
		"note":                 "Layer-3 外部大模型仲裁默认关闭（config/privacy.yaml classification.enable_llm=false）；未启用时不存在任何外送路径",
	}
	s.mu.RLock()
	funnel := s.funnel
	s.mu.RUnlock()
	if funnel == nil {
		return out
	}
	stats := funnel.LLMEscalationStats()
	configured, available := funnel.LLMStatus(context.Background())
	out["enabled"] = funnel.LLMEnabled()
	out["configured"] = configured
	out["available"] = available
	out["endpoint_host"] = stats.EndpointHost
	out["transport_secure"] = stats.TransportSecure
	out["payload_deidentified"] = stats.PayloadDeidentified
	out["escalations"] = stats.Escalations
	out["deidentified_payloads"] = stats.DeidentifiedPayloads
	if stats.TransportError != "" {
		out["transport_error"] = stats.TransportError
	}
	return out
}

// safetyFloorDiagnostics 返回安全底线与默认拒绝策略的生效快照（P0-2 / P2-2 可审计性）。
func (s *PrivacyService) safetyFloorDiagnostics() map[string]interface{} {
	s.mu.RLock()
	sfCfg := s.safetyFloorConfig
	floor := s.unlistedFloor
	path := s.privacyYAML
	s.mu.RUnlock()

	bound := s.policyLoaded()

	return map[string]interface{}{
		"config_path":                  path,
		"config_bound":                 bound,
		"min_level":                    string(sfCfg.MinLevel),
		"confidence_threshold":         sfCfg.ConfidenceThreshold,
		"force_upgrade_on_uncertainty": sfCfg.ForceUpgradeOnUncertainty,
		"unlisted_field_policy": map[string]interface{}{
			"name":          floor.Name,
			"disposition":   string(floor.Policy),
			"min_level":     floor.levelLabel(),
			"raw_min_level": floor.RawLevel,
			"default":       "deny",
			"spec_field_count": map[string]int{
				naming.DSYibao:    s.medicalYibao.FieldCount(),
				naming.DSKangyang: s.medicalKang.FieldCount(),
			},
		},
	}
}

// standardsDiagnostics 返回已加载的标准映射文件摘要（P1-3 合规对照与规则库覆盖度证明）。
func (s *PrivacyService) standardsDiagnostics() map[string]interface{} {
	s.mu.RLock()
	funnel := s.funnel
	s.mu.RUnlock()

	out := map[string]interface{}{
		"loaded":        0,
		"determined_by": "funnel.Standards",
		"note":          "标准映射文件为纯声明（不定义规则算子），供合规对照与诊断上报",
	}
	if funnel == nil {
		return out
	}
	stds := funnel.Standards()
	out["loaded"] = len(stds)
	out["standards"] = dynclassification.StandardsSummary(stds)
	return out
}
