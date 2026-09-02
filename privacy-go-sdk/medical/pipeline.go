// Package medical 提供医疗数据隐私处理流水线。
//
// 实现医保 18 字段与康养 27 字段的特化脱敏流水线，
// 支持字段级自动识别、分级脱敏策略与双结构结果输出（分级报告 + 脱敏数据集）。
package medical

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/masking"
)

// ──────────────────────────────────────────────
// 字段分类与规格
// ──────────────────────────────────────────────

// FieldCategory 字段敏感类别
type FieldCategory string

const (
	CategoryIdentity  FieldCategory = "identity"  // 身份标识
	CategoryContact   FieldCategory = "contact"   // 联系方式
	CategoryFinancial FieldCategory = "financial" // 财务信息
	CategoryMedical   FieldCategory = "medical"   // 医疗记录
	CategoryLocation  FieldCategory = "location"  // 地理位置
	CategoryOther     FieldCategory = "other"     // 其他
)

// FieldSpec 字段规格 —— 逐字段显式登记项（P0-2 默认拒绝的白名单单元）。
type FieldSpec struct {
	Name     string
	Category FieldCategory
	// Level DB51 五级定级：1=公开数据, 2=内部数据, 3=敏感数据, 4=高敏数据, 5=极致敏数据。
	// （词表口径统一见设计文档 §5.4 差异 5 / 整改项 P1-5。）
	Level int
	// Treatment 处置算子。为空时按 Category 回落到历史分派逻辑（向后兼容自定义规格），
	// 但任何回落分支都不得原样直传，未识别一律走默认拒绝。
	Treatment Treatment
	// ClipLower / ClipUpper 数值处置（TreatmentDPNoise）的截断区间，用于限制噪声越界；
	// 二者相等（零值）表示不做截断。
	ClipLower float64
	ClipUpper float64
	// Band 区间泛化（TreatmentBounding）的区间宽度。
	Band float64
}

// ──────────────────────────────────────────────
// 数据结构定义（双结构输出模型）
// ──────────────────────────────────────────────

// FieldClassification 单字段分类分级结果模型
type FieldClassification struct {
	FieldName          string `json:"field_name"`
	Level              string `json:"level"`
	SecurityTag        string `json:"security_tag"`
	Description        string `json:"description"`
	RuleMatched        string `json:"rule_matched"`
	RawValue           string `json:"raw_value,omitempty"`
	SanitizedValue     string `json:"sanitized_value"`
	SanitizedValueRule string `json:"sanitized_value_rule"`
	SanitizedValueNer  string `json:"sanitized_value_ner"`
}

// RecordClassificationReport 单条记录的分级报告
type RecordClassificationReport struct {
	RecordIndex             int                   `json:"record_index"`
	MaxLevel                string                `json:"max_level"`
	PIIFieldsDetected       []string              `json:"pii_fields_detected"`
	HighSensitivityDetected []string              `json:"high_sensitivity_detected"`
	FieldDetails            []FieldClassification `json:"field_details"`
	RawRecord               map[string]string     `json:"raw_record,omitempty"`
}

// MedicalPipelineResult 医疗流水线最终执行结果（双结构输出）
type MedicalPipelineResult struct {
	ClassificationReport []RecordClassificationReport `json:"classification_report"`
	SanitizedData        []map[string]string          `json:"sanitized_data"`
	RawData              []map[string]string          `json:"raw_data,omitempty"`
	Summary              map[string]interface{}       `json:"summary"`
}

// ──────────────────────────────────────────────
// 医疗隐私流水线 (Pipeline)
// ──────────────────────────────────────────────

// Pipeline 医疗数据隐私处理流水线
type Pipeline struct {
	fieldMap map[string]*FieldSpec
	// specNames 规格矩阵中显式登记的字段名（去重、保持登记顺序），供覆盖率断言与审计使用。
	specNames []string
	mu        sync.RWMutex
	cache     map[string]string
	// unlisted 未登记字段的默认拒绝策略（atomic.Value 热路径无锁读）。
	unlisted atomic.Value
}

// NewPipeline 创建医疗流水线实例。
//
// 默认策略为 [DefaultUnlistedFieldPolicy]（未列入规格矩阵的字段确定性掩码），
// 即白名单反转：只有在 fields 中显式登记的字段才可能被原样保留。
func NewPipeline(fields []FieldSpec) *Pipeline {
	p := &Pipeline{
		fieldMap:  make(map[string]*FieldSpec, len(fields)),
		specNames: make([]string, 0, len(fields)),
		cache:     make(map[string]string),
	}
	p.unlisted.Store(DefaultUnlistedFieldPolicy)
	seen := make(map[string]bool, len(fields))
	for i := range fields {
		spec := fields[i] // 值拷贝入表，避免与调用方共享底层数组
		key := normalizeFieldName(spec.Name)
		p.fieldMap[key] = &spec
		if !seen[key] {
			seen[key] = true
			p.specNames = append(p.specNames, spec.Name)
		}
		// 同时按别名规范名建索引（如 "姓名"/"patient_name" → name），
		// 显式规格优先于启发式猜测。
		if canon := CanonicalizePIIField(key); canon != key {
			if _, exists := p.fieldMap[canon]; !exists {
				p.fieldMap[canon] = &spec
			}
		}
	}
	return p
}

// normalizeFieldName 规格表键名归一：去空白 + 小写。
func normalizeFieldName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// NewYibaoPipeline 创建医保 18 字段流水线（契约 18 字段 ∪ 历史规格名的完整矩阵）。
func NewYibaoPipeline() *Pipeline {
	return NewPipeline(YibaoFields)
}

// NewKangyangPipeline 创建康养 27 字段流水线（契约 27 字段 ∪ 历史规格名 ∪ 体征/档案扩展槽位）。
func NewKangyangPipeline() *Pipeline {
	specs := make([]FieldSpec, 0, len(KangyangFields)+len(VitalSignFields)+len(GovCareExtraFields))
	specs = append(specs, KangyangFields...)
	specs = append(specs, VitalSignFields...)
	specs = append(specs, GovCareExtraFields...)
	return NewPipeline(specs)
}

// SetUnlistedFieldPolicy 设置未登记字段的默认拒绝策略。
//
// 只接受限制性策略（mask / drop）；无法识别的取值回落到 [DefaultUnlistedFieldPolicy]，
// 任何调用都无法把未登记字段重新放开为明文直传。
func (p *Pipeline) SetUnlistedFieldPolicy(policy UnlistedFieldPolicy) {
	p.unlisted.Store(ParseUnlistedFieldPolicy(string(policy)))
}

// UnlistedFieldPolicy 返回当前生效的未登记字段处置策略。
func (p *Pipeline) UnlistedFieldPolicy() UnlistedFieldPolicy {
	if v, ok := p.unlisted.Load().(UnlistedFieldPolicy); ok && v != "" {
		return v
	}
	return DefaultUnlistedFieldPolicy
}

// ProcessRecords 全流程处理医疗数据集，生成双结构报告与脱敏数据集（支持多核并发分块加速）
func (p *Pipeline) ProcessRecords(records []map[string]string) *MedicalPipelineResult {
	start := time.Now()
	n := len(records)
	sanitizedData := make([]map[string]string, n)
	reports := make([]RecordClassificationReport, n)
	levelCounts := map[string]int{"L1": 0, "L2": 0, "L3": 0, "L4": 0, "L5": 0}

	if n <= 64 {
		// 小批量直接串行处理
		for i, rec := range records {
			sanRec, report := p.ProcessRecord(rec, i+1)
			sanitizedData[i] = sanRec
			reports[i] = *report
			levelCounts[report.MaxLevel]++
		}
	} else {
		// 大批量自动根据 CPU 核心数进行分块并发调度
		numWorkers := runtime.GOMAXPROCS(0)
		if numWorkers > 16 {
			numWorkers = 16
		}
		if numWorkers > n {
			numWorkers = n
		}

		chunkSize := (n + numWorkers - 1) / numWorkers
		var wg sync.WaitGroup
		var mu sync.Mutex

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
			go func(s, e int) {
				defer wg.Done()
				localCounts := make(map[string]int)
				for i := s; i < e; i++ {
					sanRec, report := p.ProcessRecord(records[i], i+1)
					sanitizedData[i] = sanRec
					reports[i] = *report
					localCounts[report.MaxLevel]++
				}
				mu.Lock()
				for k, v := range localCounts {
					levelCounts[k] += v
				}
				mu.Unlock()
			}(startIdx, endIdx)
		}
		wg.Wait()
	}

	elapsed := time.Since(start).Seconds()

	leakedFields := 0
	for _, rec := range sanitizedData {
		for _, v := range rec {
			if ContainsHighRiskText(v) {
				leakedFields++
			}
		}
	}

	return &MedicalPipelineResult{
		ClassificationReport: reports,
		SanitizedData:        sanitizedData,
		RawData:              records,
		Summary: map[string]interface{}{
			"total_records":               n,
			"level_counts":                levelCounts,
			"duration_seconds":            elapsed,
			"status":                      "success",
			"compliance_guaranteed":       leakedFields == 0,
			"leaked_fields_post_sanitize": leakedFields,
			"spec_field_count":            p.FieldCount(),
			"unlisted_field_policy":       string(p.UnlistedFieldPolicy()),
			"unlisted_policy_name":        UnlistedFieldPolicyName,
			"unlisted_min_level":          UnlistedFieldLevel,
			"unlisted_default":            "deny",
		},
	}
}

// ProcessRecord 处理单条记录
func (p *Pipeline) ProcessRecord(record map[string]string, index int) (map[string]string, *RecordClassificationReport) {
	sanRec := make(map[string]string, len(record))
	var fieldDetails []FieldClassification
	var piiFields []string
	var highSensFields []string
	maxLevel := "L1"

	for k, v := range record {
		fc := p.ClassifyAndSanitizeField(k, v)
		sanRec[k] = fc.SanitizedValue
		fieldDetails = append(fieldDetails, *fc)

		if strings.HasPrefix(fc.SecurityTag, "PII_") || fc.SecurityTag == "IDENTITY" {
			piiFields = append(piiFields, k)
		}
		if fc.Level == "L4" || fc.Level == "L5" {
			highSensFields = append(highSensFields, fmt.Sprintf("%s(%s)", k, fc.Level))
		}

		if compareLevel(fc.Level, maxLevel) > 0 {
			maxLevel = fc.Level
		}
	}

	report := &RecordClassificationReport{
		RecordIndex:             index,
		MaxLevel:                maxLevel,
		PIIFieldsDetected:       piiFields,
		HighSensitivityDetected: highSensFields,
		FieldDetails:            fieldDetails,
		RawRecord:               record,
	}

	return sanRec, report
}

// ClassifyAndSanitizeField 对单个字段执行分类与脱敏。
//
// 定级遵循**就高原则**：取「规格矩阵登记等级」与「值内容实测等级」的较高者，
// 防止字段名与内容错位（如把诊断文本塞进 gender 字段）绕过矩阵；
// 未登记字段按 [UnlistedFieldLevel]（L3 敏感数据）+ 默认拒绝处置，绝不落为公开数据。
func (p *Pipeline) ClassifyAndSanitizeField(fieldName, value string) *FieldClassification {
	res := p.resolveField(fieldName)
	sanVal := p.applyTreatment(res, fieldName, value)

	level := res.level
	ruleID := res.ruleID
	if cl := contentLevel(value); cl > level {
		level = cl
		ruleID = "HIGH_RISK_VALUE_ESCALATION:" + ruleID
	}
	// 出域前的最后一道值层安全网：任何分支的结果都不得残留 L4/L5 词表命中。
	if ContainsHighRiskText(sanVal) {
		sanVal = redactClinicalText(sanVal)
	}

	// 诊断字段特殊治理：若原始值含高敏词且脱敏后仍非空（部分脱敏），整值清空，
	// 防止部分脱敏残留的文本碎片泄露病种归属信息。
	if isDiagnosisField(fieldName) && sanVal != "" && sanVal != value && ContainsHighRiskText(value) {
		sanVal = ""
	}

	levelStr := fmt.Sprintf("L%d", level)
	desc := res.description()

	return &FieldClassification{
		FieldName:          fieldName,
		Level:              levelStr,
		SecurityTag:        res.securityTag(),
		Description:        desc,
		RuleMatched:        ruleID,
		RawValue:           value,
		SanitizedValue:     sanVal,
		SanitizedValueRule: sanVal,
		SanitizedValueNer:  sanVal,
	}
}

// contentLevel 实测值内容的 DB51 等级（0 = 未命中高敏词表）。
func contentLevel(value string) int {
	if value == "" || !ContainsHighRiskText(value) {
		return 0
	}
	if containsL5Term(value) {
		return 5
	}
	return 4
}

// diagnosisField 诊断/临床字段名集合（归一化后匹配）。
var diagnosisFields = map[string]bool{
	"diagnosis":           true,
	"diagnosis_name":      true,
	"icd10_code":          true,
	"icd10":               true,
	"icd_code":            true,
	"chief_complaint":     true,
	"present_illness":     true,
	"past_history":        true,
	"progress_note":       true,
	"admission_condition": true,
	"诊断":                  true,
	"诊断名称":                true,
	"诊断编码":                true,
	"主诉":                  true,
	"现病史":                 true,
	"既往史":                 true,
	"病程记录":                true,
	"入院病情":                true,
}

// isDiagnosisField 判定字段名是否为诊断/临床字段。
func isDiagnosisField(fieldName string) bool {
	key := normalizeFieldName(fieldName)
	if diagnosisFields[key] {
		return true
	}
	canon := CanonicalizePIIField(key)
	return diagnosisFields[canon]
}

// containsL5Term 快速检测文本是否包含 L5 极高敏词汇。
func containsL5Term(value string) bool {
	if value == "" {
		return false
	}
	norm := NormalizeFullwidthAlphanumeric(value)
	for _, terms := range L5TermsMap {
		for _, term := range terms {
			if strings.Contains(norm, term) {
				return true
			}
		}
	}
	return false
}

// ──────────────────────────────────────────────
// 字段解析：规格矩阵优先，未登记默认拒绝
// ──────────────────────────────────────────────

// fieldResolution 单字段解析结果（P0-2 白名单判定的产物）。
type fieldResolution struct {
	spec      *FieldSpec
	treatment Treatment
	level     int
	category  FieldCategory
	ruleID    string
	unlisted  bool
}

// specName 规格登记名（无规格时为待解析字段名，用作散列盐值）。
func (res fieldResolution) specName() string {
	if res.spec != nil {
		return res.spec.Name
	}
	return ""
}

func (res fieldResolution) securityTag() string {
	if res.unlisted {
		return "UNLISTED_DEFAULT_DENY"
	}
	switch res.treatment {
	case TreatmentClinicalText, TreatmentDiseaseGeneralize:
		return "CLINICAL_TEXT"
	case TreatmentICD10:
		return "ICD10_DIAGNOSIS"
	case TreatmentDateMonth, TreatmentDateYear:
		return "DATE_QI"
	case TreatmentDPNoise, TreatmentBounding, TreatmentAgeBand:
		return "NUMERIC_QI"
	case TreatmentHashID, TreatmentMaskIdCard, TreatmentMaskCard, TreatmentMaskPartial:
		return "PSEUDONYM_ID"
	case TreatmentMaskName, TreatmentMaskPhone, TreatmentMaskEmail, TreatmentMaskAddress:
		return "PII_DIRECT"
	case TreatmentEnumGeneralize:
		return "SENSITIVE_ENUM"
	case TreatmentDrop:
		return "DROPPED"
	default:
		return strings.ToUpper(string(res.category))
	}
}

func (res fieldResolution) description() string {
	if res.unlisted {
		return "未列入字段规格矩阵，按默认拒绝策略处置（" + UnlistedFieldPolicyName + "）"
	}
	if res.spec != nil {
		return string(res.spec.Category) + "/" + string(res.spec.Treatment)
	}
	return string(res.category) + "/" + string(res.treatment)
}

// lookupSpec 按归一化字段名查显式规格矩阵。
func (p *Pipeline) lookupSpec(fieldName string) (*FieldSpec, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	spec, ok := p.fieldMap[normalizeFieldName(fieldName)]
	return spec, ok
}

// resolveField 解析字段的处置来源。优先级：
//
//  1. 规格矩阵显式登记（[YibaoFields] / [KangyangFields] / 扩展名单，含别名索引）；
//  2. rules.go 中的**名单级**登记（ICD-10 编码字段 / 日期准标识符字段 / PII 字段），
//     同样是白名单单元，不是字段名子串猜测；
//  3. 二者皆未命中 → 默认拒绝（mask 或 drop），定级至少 L3。
//
// 整改前存在的「字段名包含 id/phone/name 即猜测处置方式、否则原样直传」启发式分层
// 已整体删除：启发式既可能漏（陌生命名直传明文）也可能错（width/valid_flag 命中 id），
// 不具备可审计性，正是 P0-2 的成因。
func (p *Pipeline) resolveField(fieldName string) fieldResolution {
	key := normalizeFieldName(fieldName)
	if spec, ok := p.lookupSpec(key); ok {
		return specResolution(spec, "SPEC_RULE")
	}
	canon := CanonicalizePIIField(key)
	if canon != key {
		if spec, ok := p.lookupSpec(canon); ok {
			return specResolution(spec, "SPEC_RULE_ALIAS")
		}
	}
	if ICD10FieldNames[canon] || ICD10FieldNames[key] {
		return fieldResolution{
			treatment: TreatmentICD10, level: 4, category: CategoryMedical,
			ruleID: "ICD10_STANDARD",
		}
	}
	if DateGeneralizationFields[canon] || DateGeneralizationFields[key] {
		return fieldResolution{
			treatment: TreatmentDateMonth, level: 2, category: CategoryMedical,
			ruleID: "DATE_GENERALIZATION",
		}
	}
	if rule, ok := PIIFieldRules[canon]; ok {
		treatment, level := piiRegistryTreatment(canon)
		return fieldResolution{
			treatment: treatment, level: level, category: CategoryIdentity,
			ruleID: rule,
		}
	}
	return fieldResolution{
		treatment: TreatmentUnlisted, level: UnlistedFieldLevel, category: CategoryOther,
		ruleID: UnlistedFieldPolicyName, unlisted: true,
	}
}

func specResolution(spec *FieldSpec, ruleID string) fieldResolution {
	treatment := spec.Treatment
	if treatment == "" {
		// 自定义规格未声明算子：按类别回落到该类别的**限制性**默认算子，
		// 且绝不回落到直传；无法归类的按默认拒绝处理。
		treatment = defaultTreatmentForCategory(spec.Category)
	}
	return fieldResolution{
		spec: spec, treatment: treatment, level: spec.Level,
		category: spec.Category, ruleID: ruleID,
	}
}

// defaultTreatmentForCategory 类别级兜底算子（仅在自定义规格缺 Treatment 时使用）。
func defaultTreatmentForCategory(cat FieldCategory) Treatment {
	switch cat {
	case CategoryIdentity, CategoryContact:
		return TreatmentMaskName
	case CategoryFinancial:
		return TreatmentMaskPartial
	case CategoryMedical:
		return TreatmentClinicalText
	case CategoryLocation:
		return TreatmentMaskAddress
	default:
		return TreatmentUnlisted
	}
}

// piiRegistryTreatment rules.go PII 名单字段的算子与等级。
func piiRegistryTreatment(canon string) (Treatment, int) {
	switch canon {
	case "id_card_no", "social_security_no":
		return TreatmentMaskIdCard, 5
	case "registered_address", "address":
		return TreatmentMaskAddress, 4
	case "disability_cert_no":
		return TreatmentMaskIdCard, 4
	case "medical_insurance_no":
		return TreatmentMaskCard, 4
	case "person_id":
		return TreatmentHashID, 4
	case "hospital_code":
		return TreatmentMaskPartial, 2
	default: // name 及名单内其余身份字段
		return TreatmentMaskName, 4
	}
}

// ──────────────────────────────────────────────
// 处置算子分派
// ──────────────────────────────────────────────

// applyTreatment 执行解析出的处置算子。所有分支都必须满足：
// 出域值 ≠ 原值，除非该字段在矩阵中被**显式登记为 keep** 且值内容不含任何敏感命中。
func (p *Pipeline) applyTreatment(res fieldResolution, fieldName, value string) string {
	if value == "" {
		return ""
	}
	spec := res.spec

	switch res.treatment {
	case TreatmentKeep:
		return keepSafetyNet(value)
	case TreatmentMaskName:
		return masking.MaskChineseName(value)
	case TreatmentMaskIdCard:
		return noPlaintext(masking.MaskIdCard(value), value)
	case TreatmentMaskCard:
		return noPlaintext(masking.MaskBankCard(value), value)
	case TreatmentMaskPhone:
		return noPlaintext(masking.MaskPhone(value), value)
	case TreatmentMaskEmail:
		return noPlaintext(masking.MaskEmail(value), value)
	case TreatmentMaskAddress:
		return maskAddressGeneralized(value)
	case TreatmentMaskPartial:
		return maskSerialPartial(value)
	case TreatmentHashID:
		salt := res.specName()
		if salt == "" {
			salt = fieldName
		}
		return hashWithPrefix(normalizeFieldName(salt), value)
	case TreatmentClinicalText:
		return redactClinicalText(value)
	case TreatmentDiseaseGeneralize:
		return generalizeDisease(value)
	case TreatmentEnumGeneralize:
		name := normalizeFieldName(res.specName())
		if name == "" {
			name = normalizeFieldName(fieldName)
		}
		if generalized, ok := generalizeEnum(name, value); ok {
			return generalized
		}
		// 泛化层级表未覆盖该取值：按默认拒绝处置，不猜测。
		return SanitizeUnlistedField(p.UnlistedFieldPolicy(), value)
	case TreatmentICD10:
		return RedactICD10Code(value)
	case TreatmentDateMonth:
		return TruncateDateToMonth(value)
	case TreatmentDateYear:
		return TruncateDateToYear(value)
	case TreatmentDPNoise:
		lower, upper := 0.0, 0.0
		if spec != nil {
			lower, upper = spec.ClipLower, spec.ClipUpper
		}
		if out, ok := applyDPNoise(value, DefaultDPNoiseEpsilon, DefaultDPSensitivity, lower, upper); ok {
			return out
		}
		return redactClinicalText(value) // 非数值输入不得直传
	case TreatmentBounding:
		band := 0.0
		if spec != nil {
			band = spec.Band
		}
		if out, ok := applyBounding(value, band); ok {
			return out
		}
		return redactClinicalText(value)
	case TreatmentAgeBand:
		return ageBandKanon(value)
	case TreatmentDrop:
		return ""
	case TreatmentUnlisted:
		return SanitizeUnlistedField(p.UnlistedFieldPolicy(), value)
	default:
		// 矩阵中写了引擎未实装的算子名：按最严格可用处置，并因此暴露配置缺陷。
		return SanitizeUnlistedField(DefaultUnlistedFieldPolicy, value)
	}
}

// keepSafetyNet 「原样保留」算子的值层安全网。
//
// keep 仅允许矩阵中显式登记的 L1/L2 枚举/编码字段使用；但脏数据与字段错位仍可能让
// 这类字段夹带高敏病种词或直接标识符，因此保留分支同样执行实体剥离与病种范畴抹平，
// 仅在内容确无命中时才直传。
func keepSafetyNet(value string) string {
	if ContainsHighRiskText(value) {
		return redactClinicalText(value)
	}
	return StripTextEntities(value)
}

// SanitizeRecord 对整条记录执行脱敏
func (p *Pipeline) SanitizeRecord(record map[string]string) map[string]string {
	result := make(map[string]string, len(record))
	for k, v := range record {
		result[k] = p.SanitizeField(k, v)
	}
	return result
}

// SanitizeBatch 批量脱敏
func (p *Pipeline) SanitizeBatch(records []map[string]string) []map[string]string {
	results := make([]map[string]string, len(records))
	for i, r := range records {
		results[i] = p.SanitizeRecord(r)
	}
	return results
}

// SanitizeField 对单个字段执行脱敏（规格矩阵优先，未登记字段默认拒绝）。
func (p *Pipeline) SanitizeField(fieldName, value string) string {
	if value == "" {
		return ""
	}
	return p.applyTreatment(p.resolveField(fieldName), fieldName, value)
}

// maskClinicalText 对临床文本脱敏：保留首尾字符（历史算子，保留供自定义流水线复用）
func maskClinicalText(text string) string {
	runes := []rune(text)
	n := len(runes)
	if n <= 2 {
		return strings.Repeat("*", n)
	}
	kept := 2
	maskLen := n - kept*2
	if maskLen <= 0 {
		return strings.Repeat("*", n)
	}
	sb := &strings.Builder{}
	sb.WriteString(string(runes[:kept]))
	sb.WriteString(strings.Repeat("*", maskLen))
	sb.WriteString(string(runes[n-kept:]))
	return sb.String()
}

// GetFieldSpec 获取字段规格（名称归一 + 别名规范，未登记返回 nil）。
func (p *Pipeline) GetFieldSpec(fieldName string) *FieldSpec {
	if spec, ok := p.lookupSpec(fieldName); ok {
		return spec
	}
	return nil
}

// SpecFieldNames 返回规格矩阵中显式登记的字段名（保持登记顺序，不含别名索引键）。
func (p *Pipeline) SpecFieldNames() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, len(p.specNames))
	copy(out, p.specNames)
	return out
}

// FieldCount 返回规格矩阵中**去重后的登记字段名**数量（不含别名索引键）。
//
// 注意：整改前该值等于契约字段数（18 / 27）；P0-2 之后矩阵口径为
// 「契约字段 ∪ 历史规格名 ∪ 标准规范扩展槽位」，故字段数上升。
// 契约覆盖率断言请使用 [YibaoContractFields] / [KangyangContractFields]。
func (p *Pipeline) FieldCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.specNames)
}

func compareLevel(a, b string) int {
	rank := map[string]int{"L1": 1, "L2": 2, "L3": 3, "L4": 4, "L5": 5}
	return rank[a] - rank[b]
}
