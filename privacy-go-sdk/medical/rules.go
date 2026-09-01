// Package medical 提供医疗数据分类分级规则与 L4/L5 级脱敏引擎。
//
// 核心架构对齐 Python engine/medical_pipeline/rules.py：
//  1. 动态字典：PII 别名字典与 L4/L5 重大高敏词库（HIV、精神障碍、遗传缺陷、性病、恶性肿瘤、肝炎、器官损害等）；
//  2. ICD-10 高危诊断编码分级与脱敏治理；
//  3. 日期准标识符泛化（截断为年月）；
//  4. 语法自愈与断句残渣清理。
package medical

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// PII 身份隐私字段及其默认脱敏规则定义
var PIIFieldRules = map[string]string{
	"name":                 "CHINESE_NAME",
	"id_card_no":           "ID_CARD",
	"registered_address":   "ADDRESS",
	"disability_cert_no":   "DISABILITY_CERT",
	"medical_insurance_no": "INSURANCE_NO",
	"person_id":            "PERSON_ID",
	"hospital_code":        "HOSPITAL_CODE",
}

// PII 字段别名映射（中文/英文/缩写 -> 规范字段名）
var PIIFieldAliases = map[string]string{
	"姓名":               "name",
	"真实姓名":             "name",
	"用户姓名":             "name",
	"patient_name":     "name",
	"user_name":        "name",
	"real_name":        "name",
	"身份证":              "id_card_no",
	"身份证号":             "id_card_no",
	"居民身份证":            "id_card_no",
	"公民身份号码":           "id_card_no",
	"id_card":          "id_card_no",
	"idcard":           "id_card_no",
	"id_card_num":      "id_card_no",
	"id_number":        "id_card_no",
	"id_no":            "id_card_no",
	"identity_card":    "id_card_no",
	"identity_no":      "id_card_no",
	"sfz":              "id_card_no",
	"sfz_no":           "id_card_no",
	"地址":               "registered_address",
	"注册地址":             "registered_address",
	"登记地址":             "registered_address",
	"户籍地址":             "registered_address",
	"居住地址":             "registered_address",
	"居民住址":             "registered_address",
	"家庭住址":             "registered_address",
	"联系地址":             "registered_address",
	"address":          "registered_address",
	"home_address":     "registered_address",
	"contact_address":  "registered_address",
	"user_address":     "registered_address",
	"resident_address": "registered_address",
	"location":         "registered_address",
	"残疾证号":             "disability_cert_no",
	"残疾人证号":            "disability_cert_no",
	"disability_cert":  "disability_cert_no",
	"disability_card":  "disability_cert_no",
	"医保卡号":             "medical_insurance_no",
	"医保号":              "medical_insurance_no",
	"医疗保险号":            "medical_insurance_no",
	"insurance_no":     "medical_insurance_no",
	"med_insurance_no": "medical_insurance_no",
	"医保结算流水号":          "medical_insurance_no",
	"人员唯一标识":           "person_id",
	"person_id":        "person_id",
	"pid":              "person_id",
	"定点医疗机构编码":         "hospital_code",
	"hospital_code":    "hospital_code",
	// 临床与诊断字段别名映射
	"主诉":           "chief_complaint",
	"现病史":          "present_illness",
	"既往史":          "past_history",
	"个人史":          "personal_history",
	"家族史":          "family_history",
	"过敏史":          "allergic_history",
	"诊断名称":         "diagnosis_name",
	"病程记录":         "progress_note",
	"诊断编码":         "icd10_code",
	"诊断编码(icd-10)": "icd10_code",
	"诊断编码（icd-10）": "icd10_code",
	"icd-10":       "icd10_code",
	"icd10":        "icd10_code",
	"入院病情":         "admission_condition",
}

// CanonicalizePIIField 将字段名转换为规范字段名。
func CanonicalizePIIField(fieldName string) string {
	if fieldName == "" {
		return fieldName
	}
	cleaned := strings.TrimSpace(fieldName)
	if canonical, ok := PIIFieldAliases[cleaned]; ok {
		return canonical
	}
	if canonical, ok := PIIFieldAliases[strings.ToLower(cleaned)]; ok {
		return canonical
	}

	// 若包含括号（如 "id_card_no (身份证号)"），提取子串匹配
	if strings.ContainsAny(cleaned, "()（）") {
		parts := strings.FieldsFunc(cleaned, func(r rune) bool {
			return r == '(' || r == ')' || r == '（' || r == '）'
		})
		for _, part := range parts {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			if canonical, ok := PIIFieldAliases[p]; ok {
				return canonical
			}
			if canonical, ok := PIIFieldAliases[strings.ToLower(p)]; ok {
				return canonical
			}
		}
	}

	return cleaned
}

// ──────────────────────────────────────────────
// ICD-10 高危诊断编码治理
//
// 分级口径的唯一事实源是 rules/domains/medical.yaml 的 RULE_MED_ICD10.intervals
// （整改项 P1-4：SDK 与规则库双源分歧收敛）。下方逐区间的 level / category 与该表
// 一一对应，由 medical/icd10_yaml_consistency_test.go 在全编码空间断言差异数为 0，
// 不得只在本文件单方面改动等级或范畴。
// category 取值是 rules/taxonomies/default.yaml 的分类体系标识（非本包 FieldCategory）。
// ──────────────────────────────────────────────

// ICD10FieldNames 诊断编码字段名集合
var ICD10FieldNames = map[string]bool{
	"icd10_code":     true,
	"icd10":          true,
	"icd_code":       true,
	"icd":            true,
	"diagnosis_code": true,
	"诊断编码":           true,
}

var icd10Regex = regexp.MustCompile(`^\s*([A-Za-z])(\d{2})(?:\.[xX\d]\d*)?\s*$`)

// ClassifyICD10Code 判定 ICD-10 诊断编码的风险等级与范畴。
// 返回 (level, category, isHit)；未列入高危区间的合法编码 isHit=false，
// 由调用方按 YAML 的 default_level（L3 通用编码）处置。
func ClassifyICD10Code(code string) (string, string, bool) {
	match := icd10Regex.FindStringSubmatch(code)
	if match == nil {
		return "", "", false
	}
	letter := strings.ToUpper(match[1])
	number, _ := strconv.Atoi(match[2])

	// L5 极高敏（抹平级）：HIV(B20-B24)、精神分裂症(F20-F29)、亨廷顿舞蹈病(G10)
	if letter == "B" && number >= 20 && number <= 24 {
		return "L5", "MEDICAL_ICD10_HIV", true
	}
	if letter == "F" && number >= 20 && number <= 29 {
		return "L5", "MEDICAL_ICD10_PSYCHIATRIC", true
	}
	if letter == "G" && number == 10 {
		return "L5", "MEDICAL_ICD10_GENERAL", true
	}

	// L4 高敏：性病(A50-A64)、肿瘤(C00-C97/D00-D48)、肝炎(B15-B19)、心梗(I21-I22)、肾衰/尿毒症(N18-N19)、慢阻肺(J44)
	if letter == "A" && number >= 50 && number <= 64 {
		return "L4", "MEDICAL_ICD10_STD", true
	}
	if (letter == "C" && number >= 0 && number <= 97) || (letter == "D" && number >= 0 && number <= 48) {
		return "L4", "MEDICAL_ICD10_CANCER", true
	}
	if letter == "B" && number >= 15 && number <= 19 {
		return "L4", "MEDICAL_ICD10_GENERAL", true
	}
	if letter == "I" && (number == 21 || number == 22) {
		return "L4", "MEDICAL_ICD10_GENERAL", true
	}
	if letter == "N" && (number == 18 || number == 19) {
		return "L4", "MEDICAL_ICD10_GENERAL", true
	}
	if letter == "J" && number == 44 {
		return "L4", "MEDICAL_ICD10_GENERAL", true
	}

	return "", "", false
}

// RedactICD10Code ICD-10 编码脱敏：L5/L4 均整值抹平（无痕），非高危原样返回。
//
// 无痕脱敏原则：L4 编码（如 A50-A64 性病、C00-C97 肿瘤、B15-B19 肝炎）
// 的范畴码标签同样会暴露敏感病种归属，因此一并整值擦除。
func RedactICD10Code(code string) string {
	level, _, ok := ClassifyICD10Code(code)
	if !ok {
		return code
	}
	if level == "L5" || level == "L4" {
		return ""
	}
	return code
}

// ──────────────────────────────────────────────
// 日期准标识符泛化
// ──────────────────────────────────────────────

// DateGeneralizationFields 需截断至年月的日期字段
var DateGeneralizationFields = map[string]bool{
	"birth_date":     true,
	"date_of_birth":  true,
	"admission_date": true,
	"discharge_date": true,
	"出生日期":           true,
	"入院日期":           true,
	"出院日期":           true,
}

var datePrefixRegex = regexp.MustCompile(`^(\d{4})[-/.](\d{1,2})[-/.]\d{1,2}`)

// TruncateDateToMonth 将 YYYY-MM-DD / YYYY/MM/DD 等完整日期截断为 YYYY-MM。
func TruncateDateToMonth(dateStr string) string {
	if dateStr == "" {
		return dateStr
	}
	if match := datePrefixRegex.FindStringSubmatch(dateStr); match != nil {
		month := match[2]
		if len(month) == 1 {
			month = "0" + month
		}
		return match[1] + "-" + month
	}
	return dateStr
}

// NormalizeFullwidthAlphanumeric 将全角英文字母和数字转换为半角。
func NormalizeFullwidthAlphanumeric(text string) string {
	if text == "" {
		return text
	}
	var sb strings.Builder
	for _, r := range text {
		if r >= 0xFF10 && r <= 0xFF19 { // ０-９
			sb.WriteRune(r - 0xFEE0)
		} else if r >= 0xFF21 && r <= 0xFF3A { // Ａ-Ｚ
			sb.WriteRune(r - 0xFEE0)
		} else if r >= 0xFF41 && r <= 0xFF5A { // ａ-ｚ
			sb.WriteRune(r - 0xFEE0)
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// ──────────────────────────────────────────────
// L4 / L5 医疗高危词库与脱敏引擎
// ──────────────────────────────────────────────

// L5TermsMap 极高风险病史词汇映射组
var L5TermsMap = map[string][]string{
	"HIV_AIDS": {
		"获得性免疫缺陷综合征", "获得性免疫缺陷", "人免疫缺陷病毒", "HIV感染", "HIV抗体阳性", "HIV抗体", "血清HIV-1",
		"HIV-1", "HIV-2", "HIV", "AIDS", "艾滋病", "艾滋", "CD4+ T淋巴细胞", "CD4+ T细胞", "CD4+T细胞", "CD4细胞",
		"CD4计数", "CD4/CD8", "替诺福韦+拉米夫定+多替拉韦", "替诺福韦", "拉米夫定", "多替拉韦", "依非韦伦", "阿巴卡韦",
		"恩曲他滨", "齐多夫定", "HAART抗逆转录治疗", "HAART抗病毒治疗", "HAART方案", "HAART", "抗逆转录治疗", "HIV病毒载量", "病毒载量",
	},
	"PSYCHIATRIC_DISORDER": {
		"重度精神分裂症", "精神分裂症", "精神分裂", "双相情感障碍", "言语关联妄想", "关联妄想", "命令性幻听", "保护性约束倾向",
		"被害妄想", "幻听", "幻觉", "自伤倾向", "冲动砸物", "保护性约束", "奥氮平片", "奥氮平", "富马酸喹硫平", "喹硫平",
		"阿立哌唑", "利培酮", "氯氮平", "氨磺必利", "舒必利", "奋乃静", "氟哌啶醇", "丙戊酸钠", "碳酸锂", "精神卫生中心", "schizophrenia",
	},
	"GENETIC_DEFECT": {
		"遗传性亨廷顿舞蹈病", "亨廷顿舞蹈病", "亨廷顿病", "Huntington Disease", "HTT基因CAG重复序列", "HTT基因",
		"CAG重复序列", "CAG扩增", "四苯嗪", "舞蹈样动作", "舞蹈病", "Huntington",
	},
}

// L4TermsMap 高风险病史词汇映射组
var L4TermsMap = map[string][]string{
	"STD_VENEREAL": {
		"早期隐性梅毒", "隐性梅毒", "早期梅毒", "晚期梅毒", "神经梅毒", "心血管梅毒", "先天梅毒", "梅毒", "苍白密螺旋体",
		"TPPA阳性", "TPPA", "RPR阳性", "RPR 1:16", "RPR", "syphilis", "gonorrhea", "淋病", "淋球菌", "尖锐湿疣",
		"生殖器疱疹", "软下疳", "性病", "性传播疾病", "不洁性接触史", "硬下疳", "人乳头瘤病毒高危型", "外阴菜花状赘生物",
		"菜花状赘生物", "鸡冠状赘生物", "醋酸白试验阳性", "HPV 6/11", "HPV 16/18", "HPV高危型", "HPV低危型", "HPV",
		"CO2激光灼除术", "咪喹莫特乳膏", "苄星青霉素",
	},
	"MALIGNANT_NEOPLASM": {
		"恶性肿瘤", "浸润性腺癌", "肺腺癌", "胃癌", "肝癌", "乳腺癌", "宫颈癌", "癌症", "腺癌", "导管癌", "鳞状细胞癌", "鳞癌",
		"肉瘤", "消化道恶性肿瘤", "转移性肿瘤", "奥希替尼", "EGFR基因检测", "EGFR突变", "cancer", "tumor", "化疗", "放疗", "靶向治疗", "PD-1抑制剂", "PD-1",
	},
	"HEPATITIS_VIRUS": {
		"慢性乙型病毒性肝炎", "乙型肝炎", "乙肝", "丙型肝炎", "丙肝", "肝硬化失代偿期", "早期肝硬化", "肝硬化", "小肝癌",
		"蜘蛛痣", "肝掌", "肝硬化腹水", "门静脉高压", "食管胃底静脉曲张破裂出血", "食管胃底静脉曲张", "脾大", "脾功能亢进",
		"HBV-DNA阳性", "HBV-DNA", "HBV", "HCV-RNA", "HCV", "恩替卡韦", "干扰素", "HBsAg阳性", "HBsAg", "HBeAg阳性",
		"HBeAg", "HBcAb阳性", "乙肝表面抗原", "乙肝两对半", "hepatitis", "cirrhosis",
	},
	"SEVERE_ORGAN_DAMAGE": {
		"慢性阻塞性肺疾病", "COPD", "急性心肌梗死", "心肌梗死", "心肌梗塞", "冠状动脉重度狭窄", "尿毒症", "肾功能衰竭",
	},
}

// 替换标签映射（无痕脱敏：替换为空串，不产生任何提示性标签）
//
// 设计原则：敏感病种词汇直接擦除，不得以 [L5-xxx] / [L4-xxx] 等标签形式残留，
// 防止「此地无银三百两」——接收方从替换标签反推存在某类高敏疾病。
// 范畴化降级泛化由 categoryGeneralizationRules 独立处理（如「恶性肿瘤」→「相关系统疾病」）。
var L5ReplacementMap = map[string]string{
	"HIV_AIDS":             "",
	"PSYCHIATRIC_DISORDER": "",
	"GENETIC_DEFECT":       "",
}

var L4ReplacementMap = map[string]string{
	"STD_VENEREAL":        "",
	"MALIGNANT_NEOPLASM":  "",
	"HEPATITIS_VIRUS":     "",
	"SEVERE_ORGAN_DAMAGE": "",
}

var (
	compiledOnce    sync.Once
	l5RegexList     []compiledTermRegex
	l4RegexList     []compiledTermRegex
	allTermsPattern *regexp.Regexp
)

type compiledTermRegex struct {
	category    string
	replacement string
	regex       *regexp.Regexp
}

func initCompiledPatterns() {
	// 构建 L5 正则（无痕脱敏：replacement 直接使用映射值，空串即擦除）
	for cat, terms := range L5TermsMap {
		repl := L5ReplacementMap[cat]
		// 按词长倒序排序，避免短词前缀吞掉长词
		sorted := make([]string, len(terms))
		copy(sorted, terms)
		sortStringsByLengthDesc(sorted)

		escaped := make([]string, len(sorted))
		for i, t := range sorted {
			escaped[i] = regexp.QuoteMeta(t)
		}
		pattern := "(?i)(" + strings.Join(escaped, "|") + ")"
		re := regexp.MustCompile(pattern)
		l5RegexList = append(l5RegexList, compiledTermRegex{
			category:    cat,
			replacement: repl,
			regex:       re,
		})
	}

	// 构建 L4 正则（无痕脱敏：replacement 直接使用映射值，空串即擦除）
	for cat, terms := range L4TermsMap {
		repl := L4ReplacementMap[cat]
		sorted := make([]string, len(terms))
		copy(sorted, terms)
		sortStringsByLengthDesc(sorted)

		escaped := make([]string, len(sorted))
		for i, t := range sorted {
			escaped[i] = regexp.QuoteMeta(t)
		}
		pattern := "(?i)(" + strings.Join(escaped, "|") + ")"
		re := regexp.MustCompile(pattern)
		l4RegexList = append(l4RegexList, compiledTermRegex{
			category:    cat,
			replacement: repl,
			regex:       re,
		})
	}

	// 快速探测正则
	var allTerms []string
	for _, terms := range L5TermsMap {
		allTerms = append(allTerms, terms...)
	}
	for _, terms := range L4TermsMap {
		allTerms = append(allTerms, terms...)
	}
	sortStringsByLengthDesc(allTerms)
	escapedAll := make([]string, len(allTerms))
	for i, t := range allTerms {
		escapedAll[i] = regexp.QuoteMeta(t)
	}
	allTermsPattern = regexp.MustCompile("(?i)(" + strings.Join(escapedAll, "|") + ")")

	// 初始化句法语境脱敏正则、范畴化泛化规则与辅助清理正则
	initSyntacticPatterns()
	initCategoryGeneralizationRules()
	initCleanupPatterns()
}

func sortStringsByLengthDesc(arr []string) {
	for i := 0; i < len(arr); i++ {
		for j := i + 1; j < len(arr); j++ {
			if len(arr[j]) > len(arr[i]) {
				arr[i], arr[j] = arr[j], arr[i]
			}
		}
	}
}

// ContainsHighRiskText 快速判断文本是否包含 L4/L5 高风险敏感词。
func ContainsHighRiskText(text string) bool {
	if text == "" {
		return false
	}
	compiledOnce.Do(initCompiledPatterns)
	norm := NormalizeFullwidthAlphanumeric(text)
	return allTermsPattern.MatchString(norm)
}

// ──────────────────────────────────────────────
// 范畴化降级泛化（对齐 Python _CATEGORY_GENERALIZATION_RULES）
//
// 仅对适宜泛化的病种（肿瘤、肝炎、遗传缺陷、器官衰竭）自动降级为 L1/L2 通用系统/器官疾病表述；
// 性病(STD)、艾滋病(HIV)、重度精神障碍属于禁止泛化范畴，100% 直接抹平切除（Purge Only）。
// ──────────────────────────────────────────────

// categoryGeneralizationRule 范畴化降级泛化规则。
type categoryGeneralizationRule struct {
	category    string
	pattern     *regexp.Regexp
	replacement string
}

// categoryGeneralizationRules 范畴化降级泛化规则列表。
// 器官/系统前缀为必选匹配，且各系统专属规则先于裸"肿瘤"兜底规则，
// 防止 "呼吸系统肿瘤" 被首条规则误泛化为 "消化道疾病"。
var categoryGeneralizationRules []categoryGeneralizationRule

func initCategoryGeneralizationRules() {
	rules := []struct {
		cat, patStr, repl string
	}{
		// 1. 恶性肿瘤范畴 → 通用系统/器官疾病
		// 注：Go RE2 不支持零宽前瞻 (?=...)，改用捕获组 (suffix) + $2 回写。
		{"MALIGNANT_NEOPLASM", `(?i)(消化道(?:恶性)?肿瘤)(聚集倾向|家族史|史|风险)`, "消化道疾病$2"},
		{"MALIGNANT_NEOPLASM", `(?i)((?:呼吸道|呼吸系统)(?:恶性)?肿瘤)(聚集倾向|家族史|史|风险)`, "呼吸系统疾病$2"},
		{"MALIGNANT_NEOPLASM", `(?i)(生殖系统(?:恶性)?肿瘤)(聚集倾向|家族史|史|风险)`, "生殖系统疾病$2"},
		{"MALIGNANT_NEOPLASM", `(?i)(神经系统(?:恶性)?肿瘤)(聚集倾向|家族史|史|风险)`, "神经系统疾病$2"},
		{"MALIGNANT_NEOPLASM", `(?i)((?:恶性)?肿瘤)(聚集倾向|家族史|史|风险)`, "相关系统疾病$2"},
		// 2. 病毒性肝炎范畴 → 通用肝脏疾病
		{"HEPATITIS_VIRUS", `(?i)(慢性乙型病毒性肝炎|乙型肝炎|乙肝|丙型肝炎|丙肝|肝硬化代偿期|早期肝硬化|肝硬化)(家族史|史|聚集倾向)`, "肝脏疾病$2"},
		// 3. 重大遗传缺陷范畴 → 遗传性神经系统疾病
		{"GENETIC_DEFECT", `(?i)(遗传性亨廷顿舞蹈病|亨廷顿病|舞蹈病|罕见遗传病)(家族史|史|聚集倾向)`, "遗传性神经系统疾病$2"},
		// 4. 严重器官衰竭范畴 → 系统重大疾病
		{"SEVERE_ORGAN_DAMAGE", `(?i)(急性心肌梗死|冠状动脉重度狭窄)(家族史|史|聚集倾向)`, "心血管系统疾病$2"},
		{"SEVERE_ORGAN_DAMAGE", `(?i)(慢性阻塞性肺疾病|COPD)(家族史|史|聚集倾向)`, "慢性呼吸系统疾病$2"},
		{"SEVERE_ORGAN_DAMAGE", `(?i)(尿毒症|肾功能衰竭)(家族史|史|聚集倾向)`, "肾脏系统疾病$2"},
	}
	categoryGeneralizationRules = make([]categoryGeneralizationRule, len(rules))
	for i, r := range rules {
		categoryGeneralizationRules[i] = categoryGeneralizationRule{
			category:    r.cat,
			pattern:     regexp.MustCompile(r.patStr),
			replacement: r.repl,
		}
	}
}

// applyCategoryGeneralizations 对文本应用范畴化降级泛化。
func applyCategoryGeneralizations(s string) string {
	for _, rule := range categoryGeneralizationRules {
		s = rule.pattern.ReplaceAllString(s, rule.replacement)
	}
	return s
}

// ──────────────────────────────────────────────
// 句法语境脱敏正则（对齐 Python 四柱强剥离句法正则组）
//
// 在裸词替换之前运行，匹配敏感词在特定语法上下文中的出现并整体擦除或泛化，
// 避免残留「确诊」「患有」「病史」等无宾语动词碎片。
// ──────────────────────────────────────────────

var (
	// allTermsOr 所有 L4/L5 敏感词的交替正则片段（供句法模式内嵌引用）。
	allTermsOr string

	// 死因句法重构：因[敏感病]去世/死于/离世 → 因病去世
	redactDeathPattern *regexp.Regexp
	// 独立诊断句法擦除：诊断为/确诊为/查出 [敏感病]
	redactDiagnosisPattern *regexp.Regexp
	// 复合疾病列表：患/患有/确诊 [敏感病A]、[病B] → 仅擦除敏感项
	redactPairedPattern *regexp.Regexp
	// 复合疾病列表后缀：、[敏感病] → 擦除
	redactPairedSuffixPattern *regexp.Regexp
	// 亲属/单疾病场景重构：患/确诊 [敏感病] → 患病
	redactSingleSufferPattern *regexp.Regexp
	// 既往史/病史擦除：[敏感病] 病史/史 → 擦除
	redactHistoryPattern *regexp.Regexp
)

func initSyntacticPatterns() {
	// 构建所有敏感词的交替片段
	var allTerms []string
	for _, terms := range L5TermsMap {
		allTerms = append(allTerms, terms...)
	}
	for _, terms := range L4TermsMap {
		allTerms = append(allTerms, terms...)
	}
	sortStringsByLengthDesc(allTerms)
	escaped := make([]string, len(allTerms))
	for i, t := range allTerms {
		escaped[i] = regexp.QuoteMeta(t)
	}
	allTermsOr = strings.Join(escaped, "|")

	q := `['""'']?` // 可选引号

	// 死因句法：(因|由于|死于)[引号][敏感词][引号](导致的并发症|...)?(去世|死于|离世|...)
	redactDeathPattern = regexp.MustCompile(
		`(?i)(?:不幸)?\s*(?:因|由于|死于|殁于|身亡于|病逝于|离世于|因为|由)\s*` + q +
			`(?:` + allTermsOr + `)` + q +
			`\s*(?:导致的并发症|引起的并发症|破裂出血导致|破裂出血引起|破裂出血|出血导致|并发症导致|并发症|抢救无效|导致|引起)?\s*` +
			`(?:去世|死于|离世|殁于|身亡于|病逝于|不幸身亡|宣告不治|逝世)`,
	)

	// 独立诊断句法：(既往)?(诊断为|确诊为|检查出|查出|发现)[引号][敏感词][引号]
	redactDiagnosisPattern = regexp.MustCompile(
		`(?i)(?:患者\s*\d+\s*(?:年|月|天)?前|既往)?\s*` +
			`(?:诊断为|确诊为|确诊|检查出|查出|发现|提示为|考虑为)\s*` +
			q + `(?:` + allTermsOr + `)` + q,
	)

	// 复合疾病列表（首位）：(患有|确诊|患|有|合并|伴有)[引号][敏感词][引号]、
	redactPairedPattern = regexp.MustCompile(
		`(?i)((?:因|患有?|确诊(?:为)?|诊断(?:为)?|患|有|合并|伴有?)\s*)` +
			q + `(?:` + allTermsOr + `)` + q + `\s*[、,，及与和]\s*`,
	)

	// 复合疾病列表（非首位）：、[敏感词]
	redactPairedSuffixPattern = regexp.MustCompile(
		`(?i)[、,，及与和]\s*` + q + `(?:` + allTermsOr + `)` + q,
	)

	// 单疾病场景：(患有|确诊|患)[引号][敏感词][引号](病史|史)?
	redactSingleSufferPattern = regexp.MustCompile(
		`(?i)(?:患有?|确诊(?:为)?|诊断(?:为)?|患)\s*` +
			q + `(?:` + allTermsOr + `)` + q + `\s*(?:病史|史)?`,
	)

	// 既往史/病史：(既往|慢性)?[引号][敏感词][引号](病史|史)?
	redactHistoryPattern = regexp.MustCompile(
		`(?i)(?:既往|慢性)?\s*` + q + `(?:` + allTermsOr + `)` + q + `\s*(?:病史|史)?`,
	)
}

// RedactMedicalText 对医疗临床文本进行高精度 L4/L5 级无痕脱敏。
//
// 执行管线（对齐 Python redact_medical_text 多步编排）：
//  1. 全角归一化 + Fast-Path 原样放行干净文本
//  2. 死因句法重构（优先级最高，避免被后续泛化改变上下文）
//  3. 范畴化降级泛化：eligible 病种降级为通用系统/器官疾病（如「恶性肿瘤」→「相关系统疾病」）
//  4. 句法语境脱敏：诊断擦除、列表擦除、病史擦除（泛化后的非敏感词不再被过度匹配）
//  5. 裸词替换：残余敏感词直接擦除（空串，不产生任何提示性标签）
//  6. 语法自愈：清理擦除后残留的孤立介词、连词、悬空动词与多余标点
func RedactMedicalText(text string) string {
	if text == "" {
		return text
	}
	compiledOnce.Do(initCompiledPatterns)

	s := NormalizeFullwidthAlphanumeric(text)

	// Fast-path：不含任何敏感词的文本原样返回（<1ms，零误伤干净文本）
	if !allTermsPattern.MatchString(s) {
		return s
	}

	// Step 1: 死因句法重构（优先级最高，如「因HIV去世」→「因病去世」）
	s = redactDeathPattern.ReplaceAllString(s, "因病去世")

	// Step 2: 范畴化降级泛化（在句法擦除之前运行，防止「恶性肿瘤家族史」被病史模式过度擦除）
	s = applyCategoryGeneralizations(s)

	// Step 3: 句法语境脱敏（泛化后的非敏感词不再被过度匹配）
	s = redactDiagnosisPattern.ReplaceAllString(s, "")
	s = redactPairedPattern.ReplaceAllString(s, "$1")
	s = redactPairedSuffixPattern.ReplaceAllString(s, "")
	s = redactSingleSufferPattern.ReplaceAllString(s, "患病")
	s = redactHistoryPattern.ReplaceAllString(s, "")

	// Step 4: 裸词替换 — 残余敏感词直接擦除（无痕，不产生任何标签）
	for _, cr := range l5RegexList {
		s = cr.regex.ReplaceAllString(s, cr.replacement)
	}
	for _, cr := range l4RegexList {
		s = cr.regex.ReplaceAllString(s, cr.replacement)
	}

	// Step 4: 语法自愈 — 清理擦除后残留的孤立介词、连词、悬空动词与多余标点
	s = cleanOrphanSyntax(s)
	return s
}

var (
	multiPunctPattern    = regexp.MustCompile(`([，,、；;。])\s*[，,、；;。]+`)
	leadingPunctPattern  = regexp.MustCompile(`^[，,、；;。\s]+`)
	trailingCommaPattern = regexp.MustCompile(`[，,、；;\s]+$`)

	// 无痕脱敏辅助清理正则（对齐 Python _clean_orphan_syntax 关键子句）
	cleanupOrphanVerbRe     = regexp.MustCompile(`(?:^|[，,。；])\s*(?:因|由于|患有?|确诊|患|有|行|进行|接受|服用|合并|伴有|予|控制)\s*([。；;，,])`)
	cleanupOrphanPrepRe     = regexp.MustCompile(`(?:目前|近期|现|抗病毒治疗|抗病毒|抗逆转录治疗|抗逆转录|同时|曾?就诊于|诊断为|确诊为|检查出|查出|提示|示|长期|定期|口服|服用|予|给予|及|与|和)\s*([。；;，,])`)
	cleanupLeadingConjRe    = regexp.MustCompile(`^[与和及且并]+\s*`)
	cleanupLeadingPatientRe = regexp.MustCompile(`^患者[。；;，,]\s*`)
	cleanupEmptyQuotesRe    = regexp.MustCompile(`['""'']['""'']`)
	cleanupEmptyClauseRe    = regexp.MustCompile(`([，,、])\s*([。;；])`)
	cleanupEmptyParenRe     = regexp.MustCompile(`[\(（]\s*[\)）]`)
	// cleanupRepeatPunctRe 使用 multiPunctPattern 代替（Go RE2 不支持反向引用 \1）
	cleanupHorizSpacesRe    = regexp.MustCompile(`[ \t]{2,}`)
	cleanupFamilyVerbHealRe *regexp.Regexp // init 时编译（依赖 familyMembers）
	cleanupOrphanSubjectRe  *regexp.Regexp // init 时编译（依赖 familyMembers）
)

const familyMembers = `父亲|母亲|祖父|祖母|外公|外婆|爷爷|奶奶|伯父|叔叔|舅舅|姑姑|姨妈|` +
	`大伯|大舅|大姨|二姨|小姨|一弟|二弟|三弟|长子|次子|长女|次女|长兄|次兄|大哥|二哥|大姐|二姐|` +
	`弟弟|妹妹|哥哥|姐姐|爱人|配偶|丈夫|妻子|儿子|女儿|家属|家族成员`

func initCleanupPatterns() {
	cleanupFamilyVerbHealRe = regexp.MustCompile(
		`(` + familyMembers + `)\s*(?:患有?|确诊(?:为)?|诊断(?:为)?|患|有)\s*([。；;，,])`)
	cleanupOrphanSubjectRe = regexp.MustCompile(
		`(?:^|[，,。；])\s*(?:` + familyMembers + `)\s*([。；;])`)
}

// cleanOrphanSyntax 清理因敏感词无痕擦除留下的断句残渣与悬垂标点。
//
// 对齐 Python _clean_orphan_syntax 的关键子句：
//   - 孤立无宾语动词（"确诊。"、"患有。"）
//   - 孤立介词/连词碎片（"及。"、"由于，"）
//   - 亲属主语 + 空动词（"母亲。"）→ 重构为 "母亲患病。"
//   - 句首孤立连词、空括号、空引号、重复标点
func cleanOrphanSyntax(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// 0. 折叠连续水平空白（ReDoS 防护，对齐 Python）
	s = cleanupHorizSpacesRe.ReplaceAllString(s, " ")

	// 1. 消除连续重复标点
	s = multiPunctPattern.ReplaceAllString(s, "$1")

	// 2. 消除句首多余标点
	s = leadingPunctPattern.ReplaceAllString(s, "")

	// 3. 消除句尾悬空逗号/分号
	s = trailingCommaPattern.ReplaceAllString(s, "")

	// 4. 清理孤立无宾语动词（"确诊。" → "。"，"患有，" → "，"）
	s = cleanupOrphanVerbRe.ReplaceAllString(s, "$1")

	// 5. 清理孤立介词/连词碎片（"由于，" → "，"，"同时。" → "。"）
	s = cleanupOrphanPrepRe.ReplaceAllString(s, "$1")

	// 6. 亲属主语 + 空动词重构（"母亲。" → "母亲患病。"）
	if cleanupFamilyVerbHealRe != nil {
		s = cleanupFamilyVerbHealRe.ReplaceAllString(s, "$1患病$2")
	}

	// 7. 清理孤立亲属主语（"父亲。" → 删除孤立行）
	if cleanupOrphanSubjectRe != nil {
		s = cleanupOrphanSubjectRe.ReplaceAllString(s, "$1")
	}

	// 8. 清理句首孤立连词
	s = cleanupLeadingConjRe.ReplaceAllString(s, "")

	// 9. 清理句首孤立 "患者。"
	s = cleanupLeadingPatientRe.ReplaceAllString(s, "")

	// 10. 清理空引号、空括号
	s = cleanupEmptyQuotesRe.ReplaceAllString(s, "")
	s = cleanupEmptyParenRe.ReplaceAllString(s, "")

	// 11. 清理空子句（"，。" → "。"）
	s = cleanupEmptyClauseRe.ReplaceAllString(s, "$2")

	// 12. 再次折叠重复标点（复用 multiPunctPattern）
	s = multiPunctPattern.ReplaceAllString(s, "$1")

	// 13. 最终清理首尾
	s = strings.TrimSpace(s)
	s = leadingPunctPattern.ReplaceAllString(s, "")
	s = trailingCommaPattern.ReplaceAllString(s, "")

	// 14. 若清理后只剩标点或空白，返回空
	isOnlyPunct := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			isOnlyPunct = false
			break
		}
	}
	if isOnlyPunct {
		return ""
	}

	return s
}
