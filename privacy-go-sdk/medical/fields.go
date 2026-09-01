// Package medical — 逐字段脱敏处置矩阵（柳州政务云医保 18 字段 / 康养 27 字段）。
//
// 本文件实装设计文档 §5.4 与《柳州市医疗健康数据分类分级与隐私脱敏算法标准规范》§六
// 要求的**逐字段显式规格矩阵**，对应第十二章整改项 **P0-2（字段级脱敏默认拒绝）**：
//
//  1. 每一个进入示范数据源的字段都必须在本矩阵中显式登记（名称 / DB51 定级 / 处置算子），
//     禁止依赖「字段名子串启发式」猜测处置方式；
//  2. **未列入矩阵的字段一律按默认拒绝处理**（见 [UnlistedFieldPolicy]），永不以公开数据直传；
//  3. 处置算子只复用 SDK 内已有原语：确定性掩码（[masking]）、临床文本实体剥离
//     （[RedactMedicalText] + [StripTextEntities]）、拉普拉斯差分加噪（[dp.AddLaplaceNoise]，
//     ε=1.0 对齐标准规范 §5.5）、区间泛化与 K-匿名年龄分段（[kano.AgeHierarchy]，对齐 §5.4）。
package medical

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/fengzhizi319/PrivShield/privacy-go-sdk/dp"
	"github.com/fengzhizi319/PrivShield/privacy-go-sdk/kano"
	"github.com/fengzhizi319/PrivShield/privacy-go-sdk/masking"
)

// ──────────────────────────────────────────────
// 处置算子
// ──────────────────────────────────────────────

// Treatment 字段级处置算子（规格矩阵中每个字段必须显式声明一个）。
type Treatment string

const (
	// TreatmentKeep 原样保留 —— 仅限 DB51 L1/L2 低敏枚举，且必须在矩阵中显式登记。
	TreatmentKeep Treatment = "keep"
	// TreatmentMaskName 中文姓名确定性掩码（保留姓与末字）。
	TreatmentMaskName Treatment = "mask_name"
	// TreatmentMaskIdCard 身份证号确定性掩码（前 6 后 4）。
	TreatmentMaskIdCard Treatment = "mask_id_card"
	// TreatmentMaskCard 卡号类确定性掩码（保留 BIN 前 6 后 4）。
	TreatmentMaskCard Treatment = "mask_card"
	// TreatmentMaskPhone 联系电话确定性掩码（前 3 后 4）。
	TreatmentMaskPhone Treatment = "mask_phone"
	// TreatmentMaskEmail 电子邮箱确定性掩码（用户名保留首尾字符）。
	TreatmentMaskEmail Treatment = "mask_email"
	// TreatmentMaskAddress 住址泛化至省市级。
	TreatmentMaskAddress Treatment = "mask_address"
	// TreatmentMaskPartial 流水号 / 机构编码：业务前缀保留 + 中段掩码。
	TreatmentMaskPartial Treatment = "mask_partial"
	// TreatmentHashID 人员唯一标识：加盐散列化 + 前缀截断（防枚举反查）。
	TreatmentHashID Treatment = "hash_id"
	// TreatmentClinicalText 长自由文本：高危病种范畴抹平/泛化 + 内嵌实体剥离。
	TreatmentClinicalText Treatment = "clinical_text_redaction"
	// TreatmentDiseaseGeneralize 短诊断文本：范畴抹平/泛化后再做确定性遮蔽。
	TreatmentDiseaseGeneralize Treatment = "disease_generalization"
	// TreatmentEnumGeneralize 高敏枚举值粗粒度泛化（残情等级、离院转归、自理能力）。
	TreatmentEnumGeneralize Treatment = "enum_generalization"
	// TreatmentICD10 ICD-10 编码治理（L5 抹平 / L4 范畴码）。
	TreatmentICD10 Treatment = "icd10_governance"
	// TreatmentDateMonth 日期截断至年月。
	TreatmentDateMonth Treatment = "date_month"
	// TreatmentDateYear 出生日期截断至年份（标准规范 §6.1 行 4）。
	TreatmentDateYear Treatment = "date_year"
	// TreatmentDPNoise 体征数值拉普拉斯差分加噪（ε=1.0，标准规范 §5.5）。
	TreatmentDPNoise Treatment = "dp_noise"
	// TreatmentBounding 数值区间化泛化（费用、评分、住院天数）。
	TreatmentBounding Treatment = "bounding"
	// TreatmentAgeBand 年龄分段 K-匿名泛化（复用 kano.AgeHierarchy）。
	TreatmentAgeBand Treatment = "age_band_kanon"
	// TreatmentDrop 彻底丢弃（置空）。
	TreatmentDrop Treatment = "drop"
	// TreatmentUnlisted 未列入规格矩阵字段的默认拒绝处置（不可直传）。
	TreatmentUnlisted Treatment = "unlisted_default_deny"
)

// ValidTreatment 判定处置算子名是否为引擎已实装的合法算子。
func ValidTreatment(t Treatment) bool {
	switch t {
	case TreatmentKeep, TreatmentMaskName, TreatmentMaskIdCard, TreatmentMaskCard,
		TreatmentMaskPhone, TreatmentMaskEmail, TreatmentMaskAddress, TreatmentMaskPartial,
		TreatmentHashID,
		TreatmentClinicalText, TreatmentDiseaseGeneralize, TreatmentEnumGeneralize,
		TreatmentICD10, TreatmentDateMonth, TreatmentDateYear, TreatmentDPNoise,
		TreatmentBounding, TreatmentAgeBand, TreatmentDrop:
		return true
	}
	return false
}

// ──────────────────────────────────────────────
// 默认拒绝策略（P0-2 核心）
// ──────────────────────────────────────────────

// UnlistedFieldPolicy 未列入规格矩阵字段的默认处置策略。
//
// 语义为**白名单反转（默认拒绝）**：只有在本矩阵中显式登记并声明了处置算子的字段
// 才可能被直传；未登记的字段永远不得按「公开数据」输出原值。
type UnlistedFieldPolicy string

const (
	// UnlistedFieldMask 确定性掩码（保留首字符，其余置 *）——引擎可施加的最严格可用处置之一。
	UnlistedFieldMask UnlistedFieldPolicy = "mask"
	// UnlistedFieldDrop 直接丢弃（置空）。
	UnlistedFieldDrop UnlistedFieldPolicy = "drop"
)

const (
	// DefaultUnlistedFieldPolicy 包级默认策略。任何配置缺失、解析失败或取值非法时
	// 都必须回落到该值，禁止回落到「原样直传」。
	DefaultUnlistedFieldPolicy = UnlistedFieldMask
	// UnlistedFieldLevel 未登记字段的最低定级：DB51 L3（敏感/重要数据）。
	UnlistedFieldLevel = 3
	// UnlistedFieldPolicyName 本策略的审计名称（在诊断接口与流水线 Summary 中回显）。
	UnlistedFieldPolicyName = "field_level_default_deny"
)

// ParseUnlistedFieldPolicy 解析策略配置字符串。
//
// 只接受 "mask" / "drop" 两种**限制性**取值；任何其他输入（含 "keep"/"plain"/"allow"/空串）
// 一律回落到 [DefaultUnlistedFieldPolicy]，即配置层面无法把未知字段重新放开为明文直传。
func ParseUnlistedFieldPolicy(s string) UnlistedFieldPolicy {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "drop", "purge", "suppress":
		return UnlistedFieldDrop
	case "mask", "deny", "deny_public_mask", "default_deny", "":
		return DefaultUnlistedFieldPolicy
	default:
		return DefaultUnlistedFieldPolicy
	}
}

// SanitizeUnlistedField 对未列入规格矩阵的字段值施加默认拒绝处置。
//
// 先做高危病种范畴抹平与内嵌实体剥离，再施加确定性遮蔽 / 丢弃，
// 保证任何分支下原值都不会出现在出域结果中。
func SanitizeUnlistedField(policy UnlistedFieldPolicy, value string) string {
	if value == "" {
		return ""
	}
	if policy == UnlistedFieldDrop {
		return ""
	}
	scrubbed := RedactMedicalText(StripTextEntities(value))
	if isStrippedToSingleTag(scrubbed) {
		// 整值本身就是一个直接标识符（剥离后只剩一个占位标签）：
		// 改为对原值做确定性掩码，输出形态可读，遮蔽强度不变。
		scrubbed = value
	}
	return maskRunes(scrubbed, 1, 0)
}

// isStrippedToSingleTag 判定文本是否被实体剥离压缩成单个占位标签。
func isStrippedToSingleTag(s string) bool {
	if len(s) < 3 || !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return false
	}
	inner := s[1 : len(s)-1]
	return strings.Contains(inner, "已剥离") && !strings.Contains(inner, "][")
}

// ──────────────────────────────────────────────
// 文本实体剥离（规则 / 正则级，复用既有词表引擎）
// ──────────────────────────────────────────────

var (
	entIDCardRe    = regexp.MustCompile(`\d{6}(\d{8}|\d{12})[\dXx]`)
	entPhoneRe     = regexp.MustCompile(`1[3-9]\d{9}`)
	entEmailRe     = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	entBankCardRe  = regexp.MustCompile(`\d{16,19}`)
	entDateRe      = regexp.MustCompile(`\d{4}[-/.]\d{1,2}[-/.]\d{1,2}`)
	entHouseNoRe   = regexp.MustCompile(`[0-9一二三四五六七八九十]+(?:号楼|栋|单元|楼|层|室|床|号)`)
	entBirthDateRe = regexp.MustCompile(`(?:出生|生日|出生日期)\s*[:：]?\s*\d{4}[年\-/.]\d{1,2}[月\-/.]?\d{0,2}日?`)
)

// StripTextEntities 从自由文本中剥离内嵌的直接标识符（证件号 / 手机号 / 邮箱 /
// 银行卡号 / 精确日期 / 门牌床号），以占位标签替换，保留句法可读性。
//
// 顺序敏感：先剥离长结构（身份证 → 银行卡 → 手机号），避免短模式切割长模式。
func StripTextEntities(text string) string {
	if text == "" {
		return ""
	}
	s := entBirthDateRe.ReplaceAllString(text, "[日期已剥离]")
	s = entIDCardRe.ReplaceAllString(s, "[证件号已剥离]")
	s = entBankCardRe.ReplaceAllString(s, "[卡号已剥离]")
	s = entPhoneRe.ReplaceAllString(s, "[电话已剥离]")
	s = entEmailRe.ReplaceAllString(s, "[邮箱已剥离]")
	s = entDateRe.ReplaceAllString(s, "[日期已剥离]")
	s = entHouseNoRe.ReplaceAllString(s, "[门牌已剥离]")
	return s
}

// ──────────────────────────────────────────────
// 日期 / 年龄 / 住址泛化原语
// ──────────────────────────────────────────────

var (
	dateYearRe = regexp.MustCompile(`^\s*(\d{4})`)
	digitsRe   = regexp.MustCompile(`\d+`)
)

// TruncateDateToYear 把日期粗粒度化到年份（标准规范 §6.1 行 4 出生日期口径）。
//
// 与 [TruncateDateToMonth] 同为**泛化而非丢弃**：保留年份可用性，剥离月日重识别风险。
// 无法识别 4 位年份的输入按默认拒绝处置，绝不整值直传。
func TruncateDateToYear(dateStr string) string {
	if dateStr == "" {
		return ""
	}
	if m := dateYearRe.FindStringSubmatch(dateStr); m != nil {
		return m[1]
	}
	return SanitizeUnlistedField(DefaultUnlistedFieldPolicy, dateStr)
}

// ageBandKanon 按标准规范 §5.4 实施年龄分段 K-匿名泛化：
//
//	age < 60  → 3 岁区间（age − age mod 3）
//	age ≥ 60  → 2 岁区间（age − age mod 2）
//
// 兼容 "62岁" / " 62 " 等带单位写法；无法抽取数字时回落到 [kano.AgeHierarchy]
// level 1（5 岁区间），仍为泛化值。区间宽度取 §5.4 而不用 AgeHierarchy 的
// 5/10/20 固定步长，是为了与政务云出域口径逐字对齐。
func ageBandKanon(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	nums := digitsRe.FindString(raw)
	if nums == "" {
		return kano.AgeHierarchy(raw, 1)
	}
	age, err := strconv.Atoi(nums)
	if err != nil || age < 0 {
		return "*"
	}
	step := 3
	if age >= 60 {
		step = 2
	}
	start := age - age%step
	return "[" + strconv.Itoa(start) + "-" + strconv.Itoa(start+step) + ")"
}

// maskAddressGeneralized 住址泛化（保留到区县一级的确定性截断）。
//
// 依赖说明：masking.MaskAddress 当前对 ≤6 字符的短地址**原样返回**（整改项 P0-3，
// 由他人修复，本包不得改 masking.go）。在其修复前，此处对未产生任何遮蔽的结果
// 追加一次 rune 安全截断，保证默认拒绝语义不被短地址绕过。
func maskAddressGeneralized(value string) string {
	masked := masking.MaskAddress(value)
	if masked == value {
		masked = maskRunes(value, 2, 0)
	}
	return masked
}

// noPlaintext 掩码原语的安全网：若底层函数因异常输入返回原值，退化为确定性首字符掩码。
func noPlaintext(masked, original string) string {
	if masked == "" || masked == original {
		return maskRunes(original, 1, 0)
	}
	return masked
}

// ──────────────────────────────────────────────
// 数值处置（差分加噪 / 区间泛化 / 年龄分段）
// ──────────────────────────────────────────────

// DefaultDP epsilon —— 对齐标准规范 §5.5「隐私预算默认配置 ε=1.0」。
//
// 注意：本 SDK 为零状态数学原语库，此处**不做预算扣减**；
// 预算会计统一在 engine-go 服务层与 privacy-go-sdk/budget 完成。
const (
	DefaultDPNoiseEpsilon = 1.0
	DefaultDPSensitivity  = 1.0
)

// applyDPNoise 对数值施加拉普拉斯差分噪声并截断到给定合理区间。
// 非数值输入返回 ok=false，由调用方回落到文本处置。
func applyDPNoise(value string, epsilon, sensitivity, lower, upper float64) (string, bool) {
	trimmed := strings.TrimSpace(value)
	f, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return "", false
	}
	if epsilon <= 0 {
		epsilon = DefaultDPNoiseEpsilon
	}
	if sensitivity <= 0 {
		sensitivity = DefaultDPSensitivity
	}
	if lower < upper {
		f = dp.ClipValue(f-upper, upper-lower) + upper
	}
	noised := dp.AddLaplaceNoise(f, epsilon, sensitivity)
	if lower < upper {
		if noised < lower {
			noised = lower
		}
		if noised > upper {
			noised = upper
		}
	}
	return formatNumber(noised, trimmed), true
}

// applyBounding 区间化泛化：把数值归并到宽度为 band 的区间 [lo, hi)。
// 非数值输入返回 ok=false。
func applyBounding(value string, band float64) (string, bool) {
	trimmed := strings.TrimSpace(value)
	f, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return "", false
	}
	if band <= 0 {
		band = 1
	}
	lo := math_floor(f/band) * band
	return "[" + formatNumber(lo, trimmed) + "~" + formatNumber(lo+band, trimmed) + "]", true
}

// math_floor 小工具：避免为一次调用引入 math 包的额外依赖面。
func math_floor(x float64) float64 {
	i := int64(x)
	if float64(i) > x {
		i--
	}
	return float64(i)
}

// formatNumber 按输入形态回写数值：整数输入输出整数，小数输入保留一位小数。
func formatNumber(f float64, original string) string {
	if !strings.Contains(original, ".") {
		return strconv.FormatInt(int64(math_floor(f+0.5)), 10)
	}
	return strconv.FormatFloat(f, 'f', 1, 64)
}

// ──────────────────────────────────────────────
// 确定性掩码工具（rune 安全）
// ──────────────────────────────────────────────

// maskRunes 保留前 keepPrefix 与后 keepSuffix 个字符（rune 语义，中文安全），
// 中间以 * 填充；长度不足时整体置 *，绝不返回原值。
func maskRunes(value string, keepPrefix, keepSuffix int) string {
	if value == "" {
		return ""
	}
	if keepPrefix < 0 {
		keepPrefix = 0
	}
	if keepSuffix < 0 {
		keepSuffix = 0
	}
	runes := []rune(value)
	n := len(runes)
	if n <= keepPrefix+keepSuffix {
		return strings.Repeat("*", n)
	}
	var sb strings.Builder
	sb.Grow(len(value) + 4)
	if keepPrefix > 0 {
		sb.WriteString(string(runes[:keepPrefix]))
	}
	sb.WriteString(strings.Repeat("*", n-keepPrefix-keepSuffix))
	if keepSuffix > 0 {
		sb.WriteString(string(runes[n-keepSuffix:]))
	}
	return sb.String()
}

// maskSerialPartial 流水号 / 编码类处置：保留业务前缀与末位校验段，中段掩码。
func maskSerialPartial(value string) string {
	return maskRunes(value, 4, 2)
}

// hashWithPrefix 人员标识散列化：保留业务前缀 1 字符 + 加盐散列（确定性、不可逆）。
func hashWithPrefix(salt, value string) string {
	digest := masking.HashHMAC(value, hashIDSalt+salt)
	prefix := ""
	if runes := []rune(value); len(runes) > 0 {
		prefix = string(runes[:1])
	}
	if len(digest) > 10 {
		digest = digest[:10]
	}
	return prefix + digest + "***"
}

// hashIDSalt 人员标识散列命名空间盐值（固定，保证跨批次确定性一致）。
const hashIDSalt = "privshield-medical-id-salt"

// ──────────────────────────────────────────────
// 高敏枚举粗粒度泛化
// ──────────────────────────────────────────────

// enumGeneralizationMap 高敏枚举值的层级泛化表（对齐 kano.EducationHierarchy 的
// 「合并为上位类别」口径）。未命中表值的输入按确定性掩码处置，绝不直传。
var enumGeneralizationMap = map[string]map[string]string{
	// 离院方式：转归信息中「死亡」为最高敏枚举，其余合并为离院/转院大类。
	"discharge_mode": {
		"医嘱离院":  "医嘱离院",
		"医嘱转院":  "医嘱转院",
		"非医嘱离院": "离院",
		"死亡":    "其他",
		"医疗转让":  "其他",
	},
	// 残疾类别：精神/智力/多重类残疾为 L4/L5 强剥离，感官类合并为大类。
	"disability_category": {
		"精神残疾": "[L4-PSYCHIATRIC_DISORDER]",
		"智力残疾": "[L4-DISABILITY_REDACTED]",
		"多重残疾": "[L4-DISABILITY_REDACTED]",
		"肢体残疾": "肢体或感官残疾",
		"视力残疾": "肢体或感官残疾",
		"听力残疾": "肢体或感官残疾",
		"言语残疾": "肢体或感官残疾",
		"无残疾":  "无残疾",
	},
	// 残疾等级：一/二级（重度）与三/四级（中轻度）二分泛化。
	"disability_level": {
		"一级":  "重度",
		"二级":  "重度",
		"三级":  "中轻度",
		"四级":  "中轻度",
		"无":   "无",
		"未评定": "未评定",
	},
	// 评估结论：自理能力三段泛化。
	"assess_result_name": {
		"完全独立生活":   "自理",
		"基本自理":     "自理",
		"需辅助工具与护理": "部分自理",
		"需辅助工具与介护": "部分自理",
		"半自理":      "部分自理",
		"完全不能独立生活": "非自理",
		"完全不能自理":   "非自理",
	},
}

// generalizeEnum 按字段名查泛化层级表；未命中返回 ok=false。
func generalizeEnum(name, value string) (string, bool) {
	m, ok := enumGeneralizationMap[name]
	if !ok {
		return "", false
	}
	key := strings.TrimSpace(value)
	if generalized, ok := m[key]; ok {
		return generalized, true
	}
	return "", false
}

// ──────────────────────────────────────────────
// 文本类处置
// ──────────────────────────────────────────────

// redactClinicalText 长自由文本处置：内嵌直接标识符剥离 → L4/L5 病种范畴抹平与泛化。
func redactClinicalText(value string) string {
	return RedactMedicalText(StripTextEntities(value))
}

// generalizeDisease 短诊断文本处置：范畴抹平/泛化后，若未命中任何敏感病种词表，
// 施加确定性遮蔽（不再误用中文姓名掩码，见设计文档 §5.4 差异项）。
func generalizeDisease(value string) string {
	scrubbed := redactClinicalText(value)
	if scrubbed != value && strings.Contains(scrubbed, "[") {
		return scrubbed
	}
	return maskRunes(scrubbed, 1, 1)
}

// ──────────────────────────────────────────────
// 示范数据源契约字段名（CI 覆盖率断言的权威名单）
// ──────────────────────────────────────────────

// YibaoContractFields 医保示范数据源（ds_yibao / api1_yibao）契约字段名，
// 逐字取自 services/datasource-mgr/docs/api.md §5.1。
var YibaoContractFields = []string{
	"insurance_settlement_id", "person_id", "gender", "birth_date",
	"admission_date", "discharge_date", "length_of_stay", "admission_dept",
	"discharge_dept", "hospital_code", "medical_category", "discharge_mode",
	"settlement_seq_no", "diagnosis_seq", "diagnosis_type", "icd10_code",
	"diagnosis_name", "admission_condition",
}

// KangyangContractFields 康养示范数据源（ds_kangyang / api2_kangyang）契约字段名，
// 逐字取自 services/datasource-mgr/docs/api.md §5.2，与 data/kangyang.csv 表头一一对应。
var KangyangContractFields = []string{
	"gender", "age", "diagnosis_name", "chief_complaint", "present_illness",
	"past_history", "personal_history", "is_smoking", "smoking_duration",
	"family_history", "allergic_history", "department", "height", "weight",
	"disability_category", "disability_level", "assess_type_name",
	"assess_result_name", "assess_score", "assess_time", "progress_note",
	"progress_note_time", "name", "id_card_no", "registered_address",
	"disability_cert_no", "medical_insurance_no",
}

// ──────────────────────────────────────────────
// 医保 18 字段规格矩阵（ds_yibao / api1_yibao）
// ──────────────────────────────────────────────

// YibaoFields 医保结算数据集逐字段规格矩阵。
//
// 覆盖口径为「示范数据源契约 18 字段 ∪ 历史 SDK 规格名」的并集：
//   - 契约字段名取自 services/datasource-mgr/docs/api.md §5.1 与 docs/architecture
//     设计文档 §5.4 表 1（权威等级列）；
//   - 历史规格名（name / id_card_no / total_cost / diagnosis 等）保留以兼容既有调用方，
//     整改前二者字段名不匹配是 P0-2 的直接成因（设计文档 §5.4 矩阵级差异 1）。
var YibaoFields = []FieldSpec{
	// ── 契约 18 字段（ds_yibao）──
	{Name: "insurance_settlement_id", Category: CategoryFinancial, Level: 3, Treatment: TreatmentMaskPartial},
	{Name: "person_id", Category: CategoryIdentity, Level: 4, Treatment: TreatmentHashID},
	{Name: "gender", Category: CategoryIdentity, Level: 1, Treatment: TreatmentKeep},
	// 出生日期按 §6.1 行 4 权威等级 L2，处置为年份保留（月日泛化）。
	{Name: "birth_date", Category: CategoryIdentity, Level: 2, Treatment: TreatmentDateYear},
	{Name: "admission_date", Category: CategoryMedical, Level: 2, Treatment: TreatmentDateMonth},
	{Name: "discharge_date", Category: CategoryMedical, Level: 2, Treatment: TreatmentDateMonth},
	{Name: "length_of_stay", Category: CategoryMedical, Level: 2, Treatment: TreatmentBounding, Band: 3},
	{Name: "admission_dept", Category: CategoryMedical, Level: 2, Treatment: TreatmentKeep},
	{Name: "discharge_dept", Category: CategoryMedical, Level: 2, Treatment: TreatmentKeep},
	{Name: "hospital_code", Category: CategoryLocation, Level: 2, Treatment: TreatmentMaskPartial},
	{Name: "medical_category", Category: CategoryFinancial, Level: 2, Treatment: TreatmentKeep},
	{Name: "discharge_mode", Category: CategoryMedical, Level: 3, Treatment: TreatmentEnumGeneralize},
	{Name: "settlement_seq_no", Category: CategoryFinancial, Level: 3, Treatment: TreatmentMaskPartial},
	{Name: "diagnosis_seq", Category: CategoryMedical, Level: 2, Treatment: TreatmentKeep},
	{Name: "diagnosis_type", Category: CategoryMedical, Level: 2, Treatment: TreatmentKeep},
	{Name: "icd10_code", Category: CategoryMedical, Level: 4, Treatment: TreatmentICD10},
	{Name: "diagnosis_name", Category: CategoryMedical, Level: 4, Treatment: TreatmentDiseaseGeneralize},
	{Name: "admission_condition", Category: CategoryMedical, Level: 2, Treatment: TreatmentKeep},
	// ── 历史规格名（保留向后兼容，均已显式登记处置算子）──
	{Name: "name", Category: CategoryIdentity, Level: 4, Treatment: TreatmentMaskName},
	{Name: "id_card_no", Category: CategoryIdentity, Level: 5, Treatment: TreatmentMaskIdCard},
	{Name: "age", Category: CategoryIdentity, Level: 2, Treatment: TreatmentAgeBand},
	{Name: "date_of_birth", Category: CategoryIdentity, Level: 2, Treatment: TreatmentDateYear},
	{Name: "phone", Category: CategoryContact, Level: 4, Treatment: TreatmentMaskPhone},
	{Name: "address", Category: CategoryLocation, Level: 4, Treatment: TreatmentMaskAddress},
	{Name: "medical_record_no", Category: CategoryMedical, Level: 4, Treatment: TreatmentMaskPartial},
	{Name: "social_security_no", Category: CategoryFinancial, Level: 5, Treatment: TreatmentMaskIdCard},
	{Name: "insurance_type", Category: CategoryFinancial, Level: 2, Treatment: TreatmentKeep},
	{Name: "diagnosis", Category: CategoryMedical, Level: 4, Treatment: TreatmentDiseaseGeneralize},
	{Name: "icd_code", Category: CategoryMedical, Level: 3, Treatment: TreatmentICD10},
	{Name: "total_cost", Category: CategoryFinancial, Level: 3, Treatment: TreatmentBounding, Band: 500},
	{Name: "reimbursement", Category: CategoryFinancial, Level: 3, Treatment: TreatmentBounding, Band: 500},
	{Name: "chief_complaint", Category: CategoryMedical, Level: 4, Treatment: TreatmentClinicalText},
	{Name: "doctor_name", Category: CategoryIdentity, Level: 3, Treatment: TreatmentMaskName},
}

// ──────────────────────────────────────────────
// 康养 27 字段规格矩阵（ds_kangyang / api2_kangyang）
// ──────────────────────────────────────────────

// KangyangFields 康养体征数据集逐字段规格矩阵。
//
// 覆盖口径同样为「示范数据源契约 27 字段 ∪ 历史 SDK 规格名」并集，
// 契约字段名取自 services/datasource-mgr/docs/api.md §5.2 与设计文档 §5.4 表 2，
// 并与仓库样例数据集 data/kangyang.csv 表头逐一对齐（由 coverage 测试断言）。
var KangyangFields = []FieldSpec{
	// ── 契约 27 字段（ds_kangyang，对齐 data/kangyang.csv 表头）──
	{Name: "gender", Category: CategoryIdentity, Level: 1, Treatment: TreatmentKeep},
	{Name: "age", Category: CategoryIdentity, Level: 2, Treatment: TreatmentAgeBand},
	{Name: "diagnosis_name", Category: CategoryMedical, Level: 4, Treatment: TreatmentDiseaseGeneralize},
	{Name: "chief_complaint", Category: CategoryMedical, Level: 4, Treatment: TreatmentClinicalText},
	{Name: "present_illness", Category: CategoryMedical, Level: 4, Treatment: TreatmentClinicalText},
	{Name: "past_history", Category: CategoryMedical, Level: 4, Treatment: TreatmentClinicalText},
	{Name: "personal_history", Category: CategoryMedical, Level: 3, Treatment: TreatmentClinicalText},
	{Name: "is_smoking", Category: CategoryMedical, Level: 2, Treatment: TreatmentKeep},
	{Name: "smoking_duration", Category: CategoryMedical, Level: 2, Treatment: TreatmentKeep},
	{Name: "family_history", Category: CategoryMedical, Level: 4, Treatment: TreatmentClinicalText},
	{Name: "allergic_history", Category: CategoryMedical, Level: 3, Treatment: TreatmentClinicalText},
	{Name: "department", Category: CategoryMedical, Level: 2, Treatment: TreatmentKeep},
	{Name: "height", Category: CategoryMedical, Level: 2, Treatment: TreatmentDPNoise, ClipLower: 50, ClipUpper: 250},
	{Name: "weight", Category: CategoryMedical, Level: 2, Treatment: TreatmentDPNoise, ClipLower: 2, ClipUpper: 300},
	{Name: "disability_category", Category: CategoryMedical, Level: 4, Treatment: TreatmentEnumGeneralize},
	{Name: "disability_level", Category: CategoryMedical, Level: 4, Treatment: TreatmentEnumGeneralize},
	{Name: "assess_type_name", Category: CategoryMedical, Level: 2, Treatment: TreatmentKeep},
	{Name: "assess_result_name", Category: CategoryMedical, Level: 3, Treatment: TreatmentEnumGeneralize},
	{Name: "assess_score", Category: CategoryMedical, Level: 3, Treatment: TreatmentBounding, Band: 5},
	{Name: "assess_time", Category: CategoryMedical, Level: 2, Treatment: TreatmentDateMonth},
	{Name: "progress_note", Category: CategoryMedical, Level: 4, Treatment: TreatmentClinicalText},
	{Name: "progress_note_time", Category: CategoryMedical, Level: 2, Treatment: TreatmentDateMonth},
	{Name: "name", Category: CategoryIdentity, Level: 4, Treatment: TreatmentMaskName},
	{Name: "id_card_no", Category: CategoryIdentity, Level: 5, Treatment: TreatmentMaskIdCard},
	{Name: "registered_address", Category: CategoryLocation, Level: 4, Treatment: TreatmentMaskAddress},
	{Name: "disability_cert_no", Category: CategoryIdentity, Level: 4, Treatment: TreatmentMaskIdCard},
	{Name: "medical_insurance_no", Category: CategoryFinancial, Level: 4, Treatment: TreatmentMaskCard},
	// ── 历史规格名（保留向后兼容，均已显式登记处置算子）──
	{Name: "date_of_birth", Category: CategoryIdentity, Level: 2, Treatment: TreatmentDateYear},
	{Name: "phone", Category: CategoryContact, Level: 4, Treatment: TreatmentMaskPhone},
	{Name: "emergency_contact", Category: CategoryContact, Level: 4, Treatment: TreatmentMaskName},
	{Name: "emergency_phone", Category: CategoryContact, Level: 4, Treatment: TreatmentMaskPhone},
	{Name: "address", Category: CategoryLocation, Level: 4, Treatment: TreatmentMaskAddress},
	{Name: "health_record_no", Category: CategoryMedical, Level: 4, Treatment: TreatmentMaskPartial},
	{Name: "blood_type", Category: CategoryMedical, Level: 2, Treatment: TreatmentKeep},
	{Name: "allergies", Category: CategoryMedical, Level: 3, Treatment: TreatmentClinicalText},
	{Name: "chronic_diseases", Category: CategoryMedical, Level: 4, Treatment: TreatmentDiseaseGeneralize},
	{Name: "medication_history", Category: CategoryMedical, Level: 4, Treatment: TreatmentClinicalText},
	{Name: "vital_signs", Category: CategoryMedical, Level: 3, Treatment: TreatmentDPNoise, ClipLower: 0, ClipUpper: 500},
	{Name: "assessment_score", Category: CategoryMedical, Level: 3, Treatment: TreatmentBounding, Band: 5},
	{Name: "care_level", Category: CategoryMedical, Level: 2, Treatment: TreatmentKeep},
	{Name: "admission_date", Category: CategoryMedical, Level: 2, Treatment: TreatmentDateMonth},
	{Name: "bed_no", Category: CategoryLocation, Level: 3, Treatment: TreatmentMaskPartial},
	{Name: "room_no", Category: CategoryLocation, Level: 3, Treatment: TreatmentMaskPartial},
	{Name: "nurse_name", Category: CategoryIdentity, Level: 3, Treatment: TreatmentMaskName},
	{Name: "doctor_name", Category: CategoryIdentity, Level: 3, Treatment: TreatmentMaskName},
	{Name: "family_contact", Category: CategoryContact, Level: 4, Treatment: TreatmentMaskName},
	{Name: "payment_method", Category: CategoryFinancial, Level: 3, Treatment: TreatmentKeep},
	{Name: "monthly_fee", Category: CategoryFinancial, Level: 3, Treatment: TreatmentBounding, Band: 500},
	{Name: "dietary_restrictions", Category: CategoryMedical, Level: 3, Treatment: TreatmentClinicalText},
	{Name: "special_notes", Category: CategoryMedical, Level: 4, Treatment: TreatmentClinicalText},
}

// ──────────────────────────────────────────────
// 标准规范 §6.2 体征类扩展字段（拉普拉斯加噪 / 区间化）
// ──────────────────────────────────────────────

// VitalSignFields 标准规范 §6.2 与 §5.5 点名的体征数值字段处置。
// 这些字段名不出现在当前两份示范契约中，但一旦数据源扩展即必须落入矩阵而非启发式猜测。
var VitalSignFields = []FieldSpec{
	{Name: "systolic_bp", Category: CategoryMedical, Level: 2, Treatment: TreatmentBounding, Band: 5},
	{Name: "diastolic_bp", Category: CategoryMedical, Level: 2, Treatment: TreatmentBounding, Band: 5},
	{Name: "fasting_blood_glucose", Category: CategoryMedical, Level: 2, Treatment: TreatmentBounding, Band: 0.1},
	{Name: "temperature", Category: CategoryMedical, Level: 2, Treatment: TreatmentDPNoise, ClipLower: 30, ClipUpper: 45},
	{Name: "heart_rate", Category: CategoryMedical, Level: 2, Treatment: TreatmentBounding, Band: 5},
	{Name: "psychological_score", Category: CategoryMedical, Level: 2, Treatment: TreatmentBounding, Band: 5},
	// sleep_duration 在 §6.2 标 L1，但按 P1-5 词表口径数值型体征不得定级为「公开」，此处按 L2 登记。
	{Name: "sleep_duration", Category: CategoryMedical, Level: 2, Treatment: TreatmentBounding, Band: 1},
}

// GovCareExtraFields 标准规范 §6.2 康养档案的标识与联系字段槽位（与示范契约并存的别名/扩展位）。
//
// 注：§6.2 中的生活方式字段（rehabilitation_plan / dietary_preference / exercise_frequency /
// smoking_status / drinking_status / medication_compliance）**不在此登记**：两份源文档对
// 同类字段的 DB51 定级互相矛盾（P1-5），在局方裁定等级前宁可落空、由默认拒绝策略处置，
// 也不得以自拟的 L1「原样保留」把字段打开。
var GovCareExtraFields = []FieldSpec{
	{Name: "record_id", Category: CategoryMedical, Level: 2, Treatment: TreatmentMaskPartial},
	{Name: "phone_number", Category: CategoryContact, Level: 3, Treatment: TreatmentMaskPhone},
	{Name: "residential_address", Category: CategoryLocation, Level: 3, Treatment: TreatmentMaskAddress},
	{Name: "emergency_contact_phone", Category: CategoryContact, Level: 3, Treatment: TreatmentMaskPhone},
	{Name: "assessment_date", Category: CategoryMedical, Level: 2, Treatment: TreatmentDateMonth},
	{Name: "evaluator_code", Category: CategoryIdentity, Level: 2, Treatment: TreatmentMaskPartial},
	{Name: "email", Category: CategoryContact, Level: 3, Treatment: TreatmentMaskEmail},
}
