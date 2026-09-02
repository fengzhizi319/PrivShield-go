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
		"HIV-1", "HIV-2", "HIV", "AIDS", "艾滋病", "艾滋",
		// 全角绕过变体
		"ＨＩＶ", "ＡＩＤＳ",
		// 拼音/形近字绕过变体
		"aizibing", "aizi", "H1V", "HlV",
		// CD4 扩展
		"CD4+ T淋巴细胞", "CD4+ T细胞", "CD4+T细胞", "CD4细胞", "CD4+ T", "CD4+T",
		"CD4计数", "CD4/CD8", "CD4",
		// 药物与疗法
		"替诺福韦+拉米夫定+多替拉韦", "替诺福韦+拉米夫定", "替诺福韦", "拉米夫定", "多替拉韦", "依非韦伦", "阿巴卡韦",
		"恩曲他滨", "齐多夫定",
		"HAART抗逆转录治疗", "HAART抗病毒治疗", "HAART方案", "HAART", "抗逆转录治疗", "抗逆转录", "ART抗逆转录",
		"HIV病毒载量", "病毒载量",
	},
	"PSYCHIATRIC_DISORDER": {
		"重度精神分裂症", "精神分裂症", "精神分裂", "双相情感障碍", "言语关联妄想", "关联妄想", "命令性幻听", "保护性约束倾向",
		// 拼音/形近字绕过变体
		"jingshenfenlie", "精神分lie",
		// 扩展症状
		"幻听（命令性言语）", "命令性言语", "被害妄想", "幻听", "幻觉", "偏执",
		"自伤倾向", "冲动砸物", "保护性约束",
		// 药物别名
		"奥氮平片", "奥氮平", "富马酸喹硫平", "富马酸奎硫平", "喹硫平", "奎硫平",
		"阿立哌唑", "利培酮", "氯氮平", "氨磺必利", "舒必利", "奋乃静", "氟哌啶醇", "哈泊度醇",
		"丙戊酸钠", "碳酸锂", "精神卫生中心", "schizophrenia",
	},
	"GENETIC_DEFECT": {
		"遗传性亨廷顿舞蹈病", "亨廷顿舞蹈病", "亨廷顿病", "Huntington Disease", "HTT基因CAG重复序列", "HTT基因",
		"HTT", "CAG重复序列", "CAG重复", "CAG扩增", "四苯嗪",
		"舞蹈样动作", "舞蹈样症状", "四肢舞蹈样动作", "舞蹈病", "Huntington",
	},
}

// L4TermsMap 高风险病史词汇映射组
var L4TermsMap = map[string][]string{
	"STD_VENEREAL": {
		"早期隐性梅毒", "隐性梅毒", "早期梅毒", "晚期梅毒", "神经梅毒", "心血管梅毒", "先天梅毒", "胎传梅毒", "梅毒",
		// 变体/拼音/英文
		"霉毒", "meidu", "苍白密螺旋体",
		"TPPA阳性", "TPPA", "RPR阳性", "RPR 1:16", "RPR",
		"syphilis", "gonorrhea", "herpes", "chancroid",
		"淋病", "淋球菌", "尖锐湿疣", "生殖器疱疹", "软下疳",
		"性病", "性传播疾病", "xingbing", "linbing",
		"不洁性接触史", "不洁接触史", "无痛性溃疡", "硬下疳", "非医嘱",
		// 赘生物描述群
		"人乳头瘤病毒高危型",
		"外阴多发赘生物伴瘙痒", "外阴多发赘生物", "会阴部多发菜花状赘生物", "会阴部多发赘生物",
		"肛周多发菜花状赘生物", "肛周多发赘生物", "外阴菜花状赘生物", "外阴赘生物",
		"会阴部赘生物", "肛周赘生物", "多发赘生物", "菜花状赘生物", "鸡冠状赘生物",
		"乳头状赘生物", "生殖器赘生物", "赘生物伴瘙痒", "赘生物", "菜花状", "鸡冠状",
		// 拼音变体
		"caihuazhuang", "jiguanzhuang",
		// 检查
		"醋酸白试验阳性", "醋酸白试验",
		"HPV 6/11低危型阳性", "HPV 6/11低危型", "HPV 6/11", "HPV 16/18", "HPV高危型", "HPV低危型", "HPV",
		// 处置
		"CO2激光灼除术", "CO2激光灼除", "激光灼除术", "二氧化碳激光",
		"咪喹莫特乳膏", "咪喹莫特", "苄星青霉素",
	},
	"MALIGNANT_NEOPLASM": {
		"恶性肿瘤", "浸润性腺癌", "肺腺癌", "胃癌", "肝癌", "乳腺癌", "宫颈癌", "癌症", "腺癌", "导管癌",
		"鳞状细胞癌", "鳞癌", "肉瘤", "消化道恶性肿瘤", "消化道肿瘤", "转移性肿瘤",
		// 拼音/字混合绕过变体
		"feiai", "ganai", "weiai", "肺ai", "肝ai", "胃ai", "乳腺ai", "肠ai", "直肠ai", "结肠ai",
		"食道ai", "食管ai", "胰ai", "胰腺ai", "宫颈ai", "卵巢ai", "前列腺ai", "鼻咽ai",
		"淋巴ai", "骨ai", "脑ai", "皮肤ai", "肾ai", "膀胱ai", "甲状腺ai",
		// 药物/检查
		"奥希替尼", "EGFR基因检测", "EGFR突变", "cancer", "tumor",
		"化疗", "放疗", "靶向治疗", "PD-1抑制剂", "PD-1",
	},
	"HEPATITIS_VIRUS": {
		"慢性乙型病毒性肝炎", "乙型肝炎", "乙肝", "丙型肝炎", "丙肝",
		// 拼音/变体
		"yigan", "乙gan", "binggan", "丙gan",
		// 肝硬化/并发症
		"肝硬化失代偿期", "肝硬化代偿期", "早期肝硬化", "肝硬化", "小肝癌",
		"蜘蛛痣", "肝掌", "肝硬化腹水",
		"门静脉高压", "门脉高压",
		"食管胃底静脉曲张破裂出血", "食管胃底静脉曲张", "静脉曲张破裂出血", "食管静脉曲张",
		"脾大", "脾肿大", "脾功能亢进",
		// HBV/HCV 标记扩展
		"HBV-DNA阳性", "HBV-DNA阴性", "HBV-DNA 5.6×10^6 IU/mL", "HBV-DNA定量", "HBV-DNA", "HBV",
		"HCV-RNA", "HCV",
		// 全角变体
		"ＨＢＶ", "ＨＣＶ",
		// 药物/检查
		"恩替卡韦", "恩贴卡韦", "干扰素",
		"肝穿刺活检", "肝穿刺", "G3S4",
		"HBsAg阳性", "HBsAg", "HBeAg阳性", "HBeAg", "HBcAb阳性", "HBcAb", "HBsAb", "HBeAb",
		"乙肝表面抗原", "乙肝两对半", "hepatitis", "cirrhosis",
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
	category       string
	replacement    string
	regex          *regexp.Regexp
	needsASCIICheck bool // true if any term contains ASCII letters → needs word boundary check
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
		catHasASCII := false
		for i, t := range sorted {
			escaped[i] = regexp.QuoteMeta(t)
			if hasASCIIAlpha(t) {
				catHasASCII = true
			}
		}
		pattern := "(?i)(" + strings.Join(escaped, "|") + ")"
		re := regexp.MustCompile(pattern)
		l5RegexList = append(l5RegexList, compiledTermRegex{
			category:        cat,
			replacement:     repl,
			regex:           re,
			needsASCIICheck: catHasASCII,
		})
	}

	// 构建 L4 正则（无痕脱敏：replacement 直接使用映射值，空串即擦除）
	for cat, terms := range L4TermsMap {
		repl := L4ReplacementMap[cat]
		sorted := make([]string, len(terms))
		copy(sorted, terms)
		sortStringsByLengthDesc(sorted)

		escaped := make([]string, len(sorted))
		catHasASCII := false
		for i, t := range sorted {
			escaped[i] = regexp.QuoteMeta(t)
			if hasASCIIAlpha(t) {
				catHasASCII = true
			}
		}
		pattern := "(?i)(" + strings.Join(escaped, "|") + ")"
		re := regexp.MustCompile(pattern)
		l4RegexList = append(l4RegexList, compiledTermRegex{
			category:        cat,
			replacement:     repl,
			regex:           re,
			needsASCIICheck: catHasASCII,
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

// boundaryReplaceAll 对含 ASCII 词条的正则执行词边界感知替换。
// 匹配位置前后若紧邻 ASCII 字母数字（如 "archive" 中的 "hiv"），跳过该匹配。
func boundaryReplaceAll(re *regexp.Regexp, s string, repl string) string {
	locs := re.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var sb strings.Builder
	lastEnd := 0
	for _, loc := range locs {
		start, end := loc[0], loc[1]
		if !isASCIIBounded(s, start, end) {
			continue
		}
		sb.WriteString(s[lastEnd:start])
		sb.WriteString(repl)
		lastEnd = end
	}
	if lastEnd == 0 {
		return s
	}
	sb.WriteString(s[lastEnd:])
	return sb.String()
}

// boundaryReplaceAllWithGroup 对含捕获组的正则执行词边界感知替换（如 $1 回写）。
func boundaryReplaceAllWithGroup(re *regexp.Regexp, s string, repl string) string {
	locs := re.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var sb strings.Builder
	lastEnd := 0
	for _, loc := range locs {
		start, end := loc[0], loc[1]
		if !isASCIIBounded(s, start, end) {
			continue
		}
		sb.WriteString(s[lastEnd:start])
		sb.WriteString(string(re.ExpandString(nil, repl, s, loc)))
		lastEnd = end
	}
	if lastEnd == 0 {
		return s
	}
	sb.WriteString(s[lastEnd:])
	return sb.String()
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

// hasASCIIAlpha 判断字符串是否包含 ASCII 字母。
func hasASCIIAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return true
		}
	}
	return false
}

// isASCIIBounded 检查匹配位置前后的字符是否为非 ASCII 字母数字（词边界）。
func isASCIIBounded(text string, start, end int) bool {
	if start > 0 {
		c := text[start-1]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			return false
		}
	}
	if end < len(text) {
		c := text[end]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			return false
		}
	}
	return true
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

	// 通用句法模式
	redactDeathPattern         *regexp.Regexp // 死因句法重构
	redactSufferDeathPattern   *regexp.Regexp // 患有[病]去世
	redactDiagnosisPattern     *regexp.Regexp // 独立诊断句法
	redactPairedPattern        *regexp.Regexp // 复合疾病列表首位
	redactPairedSuffixPattern  *regexp.Regexp // 复合疾病列表后续
	redactSingleSufferPattern  *regexp.Regexp // 单疾病场景
	redactHistoryPattern       *regexp.Regexp // 既往史/病史
	redactFeatureTendencyPattern *regexp.Regexp // 特征倾向

	// 分类专属四柱句法模式
	redactCD4Pattern                  *regexp.Regexp // CD4计数+ART治疗
	redactGeneticClausePattern        *regexp.Regexp // 遗传缺陷基因检测
	redactSTDFeatureClausePattern     *regexp.Regexp // 性病综合8分支
	redactHepatitisFeatureClausePattern *regexp.Regexp // 肝炎特征
	redactMedicationFullPattern       *regexp.Regexp // 完整用药句法
	redactHospitalPattern             *regexp.Regexp // 就诊机构
)

func initSyntacticPatterns() {
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

	q := `['""'']?`

	// ── 通用句法模式 ──

	redactDeathPattern = regexp.MustCompile(
		`(?i)(?:不幸)?\s*(?:因|由于|死于|殁于|身亡于|病逝于|离世于|因为|由)\s*` + q +
			`(?:` + allTermsOr + `)` + q +
			`\s*(?:导致的并发症|引起的并发症|破裂出血导致|破裂出血引起|破裂出血|出血导致|并发症导致|并发症|抢救无效|导致|引起)?\s*` +
			`(?:去世|死于|离世|殁于|身亡于|病逝于|不幸身亡|宣告不治|逝世)?`,
	)

	redactSufferDeathPattern = regexp.MustCompile(
		`(?i)(?:患有?|确诊(?:为)?|诊断(?:为)?|患)\s*` + q + `(?:` + allTermsOr + `)` + q +
			`\s*(?:去世|死于|离世|殁于|身亡于|病逝于|不幸身亡|宣告不治|逝世)`,
	)

	redactDiagnosisPattern = regexp.MustCompile(
		`(?i)(?:患者\s*\d+\s*(?:年|月|天)?前|既往)?\s*` +
			`(?:诊断为|确诊为|确诊|检查出|查出|发现|提示为|考虑为)\s*` +
			q + `(?:` + allTermsOr + `)` + q + `\s*(?:，|,|。|；|;)?`,
	)

	redactPairedPattern = regexp.MustCompile(
		`(?i)((?:因|患有?|确诊(?:为)?|诊断(?:为)?|患|有|合并|伴有?)\s*)` +
			q + `(?:` + allTermsOr + `)` + q + `\s*[、,，及与和]\s*`,
	)

	redactPairedSuffixPattern = regexp.MustCompile(
		`(?i)[、,，及与和]\s*` + q + `(?:` + allTermsOr + `)` + q,
	)

	redactSingleSufferPattern = regexp.MustCompile(
		`(?i)(?:患有?|确诊(?:为)?|诊断(?:为)?|患)\s*` +
			q + `(?:` + allTermsOr + `)` + q + `\s*(?:病史|史)?`,
	)

	redactHistoryPattern = regexp.MustCompile(
		`(?i)(?:既往|慢性)?\s*` + q + `(?:` + allTermsOr + `)` + q + `\s*(?:病史|史)?`,
	)

	redactFeatureTendencyPattern = regexp.MustCompile(
		`(?i)(?:(?:及|与|和|伴|伴有)\s*` + q + `(?:` + allTermsOr + `)` + q + `\s*(?:倾向|表现|体征|症状)?|` +
			q + `(?:` + allTermsOr + `)` + q + `\s*(?:倾向|表现|体征|症状))`,
	)

	// ── 分类专属四柱句法模式 ──

	// CD4 计数句法擦除（诊断柱+用药柱）
	redactCD4Pattern = regexp.MustCompile(
		`(?i)CD4\+?\s{0,2}T?\s{0,1}(?:细胞|淋巴细胞)?\s{0,1}(?:计数)?\s{0,1}(?:为|约)?\s{0,1}\d+\s*(?:个|cells)?\s*(?:/μL|/µL|/ul|/mm3|/L)?\s*[，,。；;]?`,
	)

	// 遗传缺陷基因检测综合擦除（诊断柱+检查柱）
	redactGeneticClausePattern = regexp.MustCompile(
		`(?i)(?:(?:基因检测提示|基因检测示|基因检测结果示?|基因检测)?\s*` + q +
			`(?:遗传性亨廷顿舞蹈病|亨廷顿病|亨廷顿舞蹈病|HTT基因|CAG重复序列|CAG重复|CAG扩增|舞蹈病)` + q +
			`\s*(?:\([^)]*\)|（[^）]*）)?\s*[，,。；;]?|` +
			`(?:四肢)?舞蹈样动作\s*(?:与|和|及)?\s*)`,
	)

	// 性病综合句法擦除（8分支，覆盖四柱：病因+体征+诊断+用药）
	redactSTDFeatureClausePattern = regexp.MustCompile(
		`(?i)(?:` +
			// 分支1: 梅毒等性病病史句法
			`(?:患者)?(?:\d+[年月天]+前)?(?:既往有|曾有|自述有|有)?(?:早期|晚期|隐性|神经|心血管|胎传|先天)*梅毒(?:\s{0,2}[\(（][^)）]*[\)）])?(?:病史|史)?\s*[，,。；;]?|` +
			`(?:患者)?(?:\d+[年月天]+前)?(?:既往有|曾有|自述有|有)?(?:淋病|尖锐湿疣|生殖器疱疹|软下疳|性病)(?:\s{0,2}[\(（][^)）]*[\)）])?(?:病史|史)?\s*[，,。；;]?|` +
			// 分支2: 检查出/确诊为 梅毒/TPPA/RPR
			`(?:检查出|确诊为|诊断为)?['"""]?(?:梅毒|TPPA阳性|RPR阳性|淋病|尖锐湿疣)['"""]?\s*[，,。；;]?|` +
			// 分支3: 血清学 TPPA/RPR 滴度
			`(?:血清学检查示|血清学检查|血清学)?\s*(?:TPPA阳性|TPPA滴度\s*\d+:\d+\s*阳性|TPPA|RPR阳性|RPR滴度\s*\d+:\d+\s*阳性|RPR\s*1:\d+|RPR|\d+:\d+)\s*[，,。；;]?|` +
			// 分支4: 不洁接触史（病因柱）
			`(?:追问病史[，,]?)?(?:1年前有|既往有|曾有)?(?:不洁性接触史|不洁接触史)\s*[，,。；;]?|` +
			// 分支5: 无痛性溃疡/硬下疳（体征柱）
			`(?:半年前|1年前)?(?:外阴)?(?:曾出现|出现)?(?:无痛性溃疡|硬下疳)(?:[\(（]硬下疳[\)）])?(?:伴[^\s，,。；;]*)?(?:自愈)?\s*[，,。；;]?|` +
			// 分支6: 菜花状/鸡冠状/乳头状赘生物（体征柱）
			`(?:患者)?(?:\d+[年月天]+前)?(?:发现|出现)?(?:外阴及会阴部|外阴|会阴部|肛周)?(?:及(?:外阴|会阴部|肛周))?(?:多发)?(?:(?:菜花状|鸡冠状|乳头状)?赘生物|菜花状|鸡冠状)(?:[，,]逐渐增多)?(?:[，,]伴(?:局部)?(?:轻度)?(?:瘙痒|异物感|接触性出血))*\s*[，,。；;]?|` +
			// 分支7: 醋酸白试验/HPV基因型（检查柱）
			`(?:醋酸白试验(?:阳性)?|HPV\s*(?:6/11|16/18)?(?:低危型|高危型)?(?:阳性)?|(?:病理)?活检提示(?:尖锐湿疣)?)\s*[，,。；;]?|` +
			// 分支8: CO2激光/咪喹莫特等处置（用药柱）
			`(?:行|给予|实施)?['"""]?(?:CO2激光灼除术|CO2激光灼除治疗|CO2激光灼除|CO2激光治疗|激光灼除术|二氧化碳激光|咪喹莫特乳膏(?:外用|局部涂抹)?|咪喹莫特)['"""]?(?:及|与|和)?['"""]?(?:CO2激光灼除术|CO2激光灼除治疗|CO2激光灼除|CO2激光治疗|激光灼除术|二氧化碳激光|咪喹莫特乳膏(?:外用|局部涂抹)?|咪喹莫特)?['"""]?(?:外用|局部涂抹|治疗)?\s*[，,。；;]?` +
			`)`,
	)

	// 肝炎特征综合擦除（诊断柱+检查柱+用药柱）
	redactHepatitisFeatureClausePattern = regexp.MustCompile(
		`(?i)(?:` +
			`\(?HBV-DNA\s*[\d.×^E+\-⁰¹²³⁴⁵⁶⁷⁸⁹]+\s*(?:IU/mL|copies/ml)?\)?\s*[，,。；;]?|` +
			`HBV-DNA(?:\s*阳性|\s*阴性|定量)?(?:降至|低于|为)?\s*(?:检测下限|阴性|阳性|\d+)?\s*[，,。；;]?|` +
			`(?:HBsAg|HBeAg|HBcAb|HBsAb|HBeAb)(?:阳性|阴性)?\s*[，,。；;]?|` +
			`(?:行)?肝(?:脏)?穿刺(?:活检)?(?:提示|示)?\s*[A-Z0-9]+\s*[，,。；;]?|` +
			`(?:目前|近期|现)?\s*HBV-DNA降至检测下限[。；;]?` +
			`)`,
	)

	// 完整服药/用药句法擦除（用药柱）
	medDose := `(?:\s*\d+(?:\.\d+)?\s*(?:mg|g|ml|u|ug|片|粒|支|%))?`
	medFreq := `(?:\s*(?:qd|bid|tid|qid|qn|qw|im|iv|po))?`
	medPrefix := `(?:建议)?\s*(?:尽早)?\s*(?:目前)?\s*(?:长期|定期|口服|服用|给予|使用|行|实施|接受|予|给予口服|开具|遵医嘱|启动|开始)`
	medSuffix := `(?:控制舞蹈样症状|控制症状|抗逆转录治疗|抗逆转录|抗病毒治疗|抗病毒|对症治疗|治疗|对症处理|口服|方案)`
	medDoseFreqNonEmpty := `(?:(?:\s*\d+(?:\.\d+)?\s*(?:mg|g|ml|u|ug|片|粒|支|%))(?:\s*(?:qd|bid|tid|qid|qn|qw|im|iv|po))?|\s*(?:qd|bid|tid|qid|qn|qw|im|iv|po))`

	redactMedicationFullPattern = regexp.MustCompile(
		`(?i)(?:` + medPrefix + `\s*(?:病理提示|提示|行)?\s*` + q + `(?:` + allTermsOr + `)` + q + medDose + medFreq +
			`\s*(?:口服|服用)?\s*(?:及|与|和|合并|\+)?\s*(?:` + q + `(?:` + allTermsOr + `)` + q + medDose + medFreq + `)*\s*(?:` + medSuffix + `)?|` +
			q + `(?:` + allTermsOr + `)` + q + medDose + medFreq + `\s*(?:口服|服用)?\s*(?:及|与|和|合并|\+)?\s*(?:` +
			q + `(?:` + allTermsOr + `)` + q + medDose + medFreq + `)*\s*` + medSuffix + `|` +
			q + `(?:` + allTermsOr + `)` + q + medDoseFreqNonEmpty + `)`,
	)

	// 就诊机构句法擦除
	redactHospitalPattern = regexp.MustCompile(
		`(?i)(?:曾?就诊于|就诊于|收治于|转诊至|住院于|门诊于)\s*` + q +
			`(?:` + allTermsOr + `|[\w\x{4e00}-\x{9fa5}]{2,15}(?:医院|中心|诊所|专科|卫生院|卫生所|外院))` + q +
			`\s*(?:，|,|。|；|;)?`,
	)
}

// redactMaxTextLength 超长文本降级阈值（对齐 Python _REDACT_MAX_TEXT_LENGTH）。
const redactMaxTextLength = 50000

// redactTermsOnly 超长文本降级路径：仅词库级擦除（无句法重构），防 ReDoS。
func redactTermsOnly(s string) string {
	for _, cr := range l5RegexList {
		if cr.needsASCIICheck {
			s = boundaryReplaceAll(cr.regex, s, cr.replacement)
		} else {
			s = cr.regex.ReplaceAllString(s, cr.replacement)
		}
	}
	for _, cr := range l4RegexList {
		if cr.needsASCIICheck {
			s = boundaryReplaceAll(cr.regex, s, cr.replacement)
		} else {
			s = cr.regex.ReplaceAllString(s, cr.replacement)
		}
	}
	return s
}

// RedactMedicalText 对医疗临床文本进行高精度 L4/L5 级无痕脱敏。
//
// 执行管线（完整对齐 Python redact_medical_text 20 步编排）：
//  1. 全角归一化
//  2. 长文本降级（>50K → 仅词库擦除）
//  3. Fast-path 预过滤
//  4. ReDoS 全局防护
//  5. 死因句法重构（含年龄K匿名）
//  6. CD4计数擦除
//  7. 遗传缺陷句法擦除
//  8. 性病综合句法擦除
//  9. 肝炎特征句法擦除
//  10. 范畴化降级泛化
//  11. 完整用药句法擦除
//  12. 就诊机构句法擦除
//  13. 独立诊断句法擦除
//  14. 特征倾向擦除
//  15. 复合疾病列表
//  16. 单疾病场景
//  17. 既往史/病史
//  18. 裸词替换
//  19. 语法自愈
//  20. 后处理安全门
func RedactMedicalText(text string) string {
	if text == "" {
		return text
	}
	compiledOnce.Do(initCompiledPatterns)

	s := NormalizeFullwidthAlphanumeric(text)

	// Step 2: 超长文本降级
	if len(s) > redactMaxTextLength {
		return redactTermsOnly(s)
	}

	// Step 3: Fast-path
	if !allTermsPattern.MatchString(s) {
		return s
	}

	// Step 4: ReDoS 全局防护
	s = cleanupHorizSpacesRe.ReplaceAllString(s, " ")

	// Step 5: 死因句法重构
	s = redactDeathPattern.ReplaceAllString(s, "因病去世")
	s = redactSufferDeathPattern.ReplaceAllString(s, "因病去世")

	// Step 6: CD4计数擦除
	s = redactCD4Pattern.ReplaceAllString(s, "")

	// Step 7: 遗传缺陷句法擦除
	s = redactGeneticClausePattern.ReplaceAllString(s, "")

	// Step 8: 性病综合句法擦除
	s = redactSTDFeatureClausePattern.ReplaceAllString(s, "")

	// Step 9: 肝炎特征句法擦除
	s = redactHepatitisFeatureClausePattern.ReplaceAllString(s, "")

	// Step 10: 范畴化降级泛化
	s = applyCategoryGeneralizations(s)

	// Step 11: 完整用药句法擦除
	s = redactMedicationFullPattern.ReplaceAllString(s, "")

	// Step 12: 就诊机构句法擦除
	s = redactHospitalPattern.ReplaceAllString(s, "")

	// Step 13: 独立诊断句法擦除
	s = redactDiagnosisPattern.ReplaceAllString(s, "")

	// Step 14: 特征倾向擦除
	s = redactFeatureTendencyPattern.ReplaceAllString(s, "")

	// Step 15: 复合疾病列表
	s = redactPairedPattern.ReplaceAllString(s, "$1")
	s = redactPairedSuffixPattern.ReplaceAllString(s, "")

	// Step 16: 单疾病场景
	s = redactSingleSufferPattern.ReplaceAllString(s, "患病")

	// Step 17: 既往史/病史
	s = redactHistoryPattern.ReplaceAllString(s, "")

	// Step 18: 裸词替换
	for _, cr := range l5RegexList {
		if cr.needsASCIICheck {
			s = boundaryReplaceAll(cr.regex, s, cr.replacement)
		} else {
			s = cr.regex.ReplaceAllString(s, cr.replacement)
		}
	}
	for _, cr := range l4RegexList {
		if cr.needsASCIICheck {
			s = boundaryReplaceAll(cr.regex, s, cr.replacement)
		} else {
			s = cr.regex.ReplaceAllString(s, cr.replacement)
		}
	}

	// Step 19: 语法自愈
	s = cleanOrphanSyntax(s)

	// Step 20: 后处理安全门 — 若输出仍含 L4/L5 词 → 整值清空
	if ContainsHighRiskText(s) {
		return ""
	}

	return s
}

// ──────────────────────────────────────────────
// 语法自愈清理正则（完整对齐 Python _clean_orphan_syntax 40+ 模式）
// ──────────────────────────────────────────────

var (
	multiPunctPattern    = regexp.MustCompile(`([，,、；;。])\s*[，,、；;。]+`)
	leadingPunctPattern  = regexp.MustCompile(`^[，,、；;。\s]+`)
	trailingCommaPattern = regexp.MustCompile(`[，,、；;\s]+$`)

	cleanupOrphanVerbRe     = regexp.MustCompile(`(?:^|[，,。；])\s*(?:因|由于|患有?|确诊|患|有|行|进行|接受|服用|合并|伴有|予|控制)\s*([。；;，,])`)
	cleanupOrphanPrepRe     = regexp.MustCompile(`(?:目前行|目前|阳性|阴性|显示阳性|提示阳性|抗病毒治疗|抗病毒|抗逆转录治疗|抗逆转录|同时因|由于|同时|曾?就诊于|诊断为|确诊为|检查出|查出|提示为|及倾向|及控制症状|控制症状|控制|基因检测提示|基因检测示|基因检测|长期|定期|口服|服用|血清学|血清学检查示?|予|给予|及|与|和)\s*([。；;，,])`)
	cleanupLeadingConjRe    = regexp.MustCompile(`^[与和及且并]+\s*`)
	cleanupLeadingPatientRe = regexp.MustCompile(`^患者[。；;，,]\s*`)
	cleanupEmptyQuotesRe    = regexp.MustCompile(`['""'']['""'']`)
	cleanupEmptyClauseRe    = regexp.MustCompile(`([，,、])\s*([。;；])`)
	cleanupEmptyParenRe     = regexp.MustCompile(`[\(（]\s*[\)）]`)
	cleanupHorizSpacesRe    = regexp.MustCompile(`[ \t]{2,}`)

	cleanupFamilyVerbHealRe *regexp.Regexp
	cleanupOrphanSubjectRe  *regexp.Regexp

	// ── 新增清理正则（对齐 Python _clean_orphan_syntax 扩展子句）──

	cleanupDevelopAndRe         = regexp.MustCompile(`发展为\s*与`)
	cleanupPatientTimePrefixRe  = regexp.MustCompile(`(?:患者\s*\d+\s*(?:年|月|天|周)?前)\s*([，,])`)
	cleanupVerbPunctRe          = regexp.MustCompile(`((?:因|由于|患有?|确诊|患|有|行|进行|接受|服用|合并|伴有))\s*[、,，]`)
	cleanupNoObjVerbRe          = regexp.MustCompile(`(?:急诊行|急诊就诊|就诊|行|实施|接受|予|给予)\s*(?:提示|检查提示|显示|示)?\s*(?:及|与|和)?\s*([。；;，,])`)
	cleanupNoObjHintRe          = regexp.MustCompile(`(?:基因检测提示|基因检测示|基因检测|检查提示|检查示|提示|显示|示|予|控制)\s*([。；;，,])`)
	cleanupEmptyOpParenRe       = regexp.MustCompile(`[\(（][\s\+\-\*\/]*[\)）]`)
	cleanupHAARTLongRe          = regexp.MustCompile(`开展\s*(?:HAART\s*)?抗病毒治疗`)
	cleanupHAARTShortRe         = regexp.MustCompile(`(?:HAART\s*)?抗病毒治疗`)
	cleanupHAARTWordRe          = regexp.MustCompile(`(?i)\bHAART\b`)
	cleanupHIVParenRe           = regexp.MustCompile(`[\(（]\s*(?:HIV\s*)?(?:[\x{4e00}-\x{9fa5}]{0,6}(?:期|型|阶段|试验)|期|型)?\s*[\)）]`)
	cleanupNameLabelRe          = regexp.MustCompile(`(姓名[：:])\s*([\x{4e00}-\x{9fa5}])[\x{4e00}-\x{9fa5}]{1,2}`)
	cleanupPatientLabelRe       = regexp.MustCompile(`(患者[：:])\s*([\x{4e00}-\x{9fa5}])[\x{4e00}-\x{9fa5}]{1,2}`)
	cleanupColonCommaRe         = regexp.MustCompile(`([：:])\s*[，,、]`)
	cleanupColonPeriodRe        = regexp.MustCompile(`([：:])\s*[。；;]`)
	cleanupCommaPeriodRe        = regexp.MustCompile(`([，,])\s*([。；;])`)
	cleanupAppearPunctRe        = regexp.MustCompile(`(?:出现|发展为|表现为)\s*([。；;，,])`)
	cleanupRepeatQuotesRe       = regexp.MustCompile(`['\""'""'']{2,}`)
	cleanupTimePrefixPunctRe    = regexp.MustCompile(`(?:1年前有|半年前|1年前|既往有|曾有|自述有|外阴|曾出现|出现|自愈)\s*([。；;，,])`)
	cleanupTimePrefixRe         = regexp.MustCompile(`(?:1年前有|半年前|1年前|既往有|曾有|自述有|外阴|曾出现|出现|自愈)`)
	cleanupHistoryStartPunctRe  = regexp.MustCompile(`(?:追问病史|诊断为|确诊为|建议尽早启动|尽早启动|启动|开展|进一步检查|进一步|发现)\s*([。；;，,])`)
	cleanupHistoryStartRe       = regexp.MustCompile(`(?:追问病史|诊断为|确诊为|建议尽早启动|尽早启动|启动|开展|进一步检查|进一步)`)
	cleanupSeekCarePunctRe      = regexp.MustCompile(`(?:曾?就诊于|就诊于|收治于|转诊至|住院于)\s*([。；;，,])`)
	cleanupSeekCareRe           = regexp.MustCompile(`(?:曾?就诊于|就诊于|收治于|转诊至|住院于)`)
	cleanupSymptomItchRe        = regexp.MustCompile(`(?:伴|与|和)?\s*(?:局部)?(?:轻度)?(?:瘙痒|异物感|接触性出血)\s*([。；;，,])?`)
	cleanupDoctorOrderRe        = regexp.MustCompile(`(?:医嘱[：:])\s*(?:立即|及时|定期)?\s*([。；;])`)
	cleanupDieParenRe           = regexp.MustCompile(`(死于|殁于)\s*[\(（]([^）\)]+)[\)）]`)
	cleanupBecauseDieRe         = regexp.MustCompile(`(?:因|死于|因于)\s*(去世|死于|离世|逝世)`)
	cleanupFamilyDieRe          *regexp.Regexp // init 时编译
	cleanupDiagPrefixCommaRe    = regexp.MustCompile(`((?:因|患有?|确诊(?:为)?|诊断(?:为)?|患|有|合并|伴有?))\s*[、,，]\s*`)
	cleanupImageExtRe           = regexp.MustCompile(`(?i)(\b[\w/\\.-]*?)(?:syphilis|hiv|aids|cancer|tumor|hepatitis)([\w/\\.-]*\.(?:png|jpg|jpeg|dcm|webp|gif)\b)`)
	cleanupLeadingPatientChestRe = regexp.MustCompile(`^患者([^，,。；;]{0,10}详见)`)

	// 最终无主语子句检测（≤30字符安全阈值，防 ReDoS）
	cleanupSubjectlessClauseRe = regexp.MustCompile(
		`^(?:患者)?\s*(?:\d+\s*(?:年|月|天|周|小时|周期|疗程|次)?\s*(?:前)?)?\s*` +
			`(?:无明显诱因|体检|目前|近期|现|发现|检查出|查出|提示|示|进一步检查|进一步|` +
			`出现|曾出现|既往|反复发作|发作|持续|存在|明显|自述|口服|服用|给予|使用|予|` +
			`遵医嘱|服|长期|定期|术后|抗病毒治疗|抗病毒|抗逆转录治疗|抗逆转录|检测不到|` +
			`低于检测下限|者|为者)*\s*\d*\s*(?:年|月|天|周|小时|周期|疗程|次)?\s*(?:余)?\s*` +
			`(?:年|月|天|周)?\s*[。；;，,]*$`,
	)
)

const familyMembers = `父亲|母亲|祖父|祖母|外公|外婆|爷爷|奶奶|伯父|叔叔|舅舅|姑姑|姨妈|` +
	`大伯|大舅|大姨|二姨|小姨|一弟|二弟|三弟|长子|次子|长女|次女|长兄|次兄|大哥|二哥|大姐|二姐|` +
	`弟弟|妹妹|哥哥|姐姐|爱人|配偶|丈夫|妻子|儿子|女儿|家属|家族成员`

func initCleanupPatterns() {
	cleanupFamilyVerbHealRe = regexp.MustCompile(
		`(` + familyMembers + `)\s*(?:患有?|确诊(?:为)?|诊断(?:为)?|患|有)\s*([。；;，,])`)
	cleanupOrphanSubjectRe = regexp.MustCompile(
		`(?:^|[，,。；])\s*(?:` + familyMembers + `)\s*([。；;])`)
	cleanupFamilyDieRe = regexp.MustCompile(
		`(` + familyMembers + `)\s*(?:殁于|死于|身亡于|病逝于|离世于|由)\s*([。；;，,])`)
}

// cleanOrphanSyntax 清理因敏感词无痕擦除留下的断句残渣与悬垂标点。
// 完整对齐 Python _clean_orphan_syntax 的 40+ 清理子句。
func cleanOrphanSyntax(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// 0. 折叠连续水平空白（ReDoS 防护）
	s = cleanupHorizSpacesRe.ReplaceAllString(s, " ")

	// 0.1 空括号 + 就诊机构/肝炎特征二次清理
	s = cleanupEmptyParenRe.ReplaceAllString(s, "")
	s = redactHospitalPattern.ReplaceAllString(s, "")
	s = redactHepatitisFeatureClausePattern.ReplaceAllString(s, "")

	// 0.2 空运算符括号 + HAART 重构
	s = cleanupEmptyOpParenRe.ReplaceAllString(s, "")
	s = cleanupHAARTLongRe.ReplaceAllString(s, "开展常规对症治疗")
	s = cleanupHAARTShortRe.ReplaceAllString(s, "常规对症治疗")
	s = cleanupHAARTWordRe.ReplaceAllString(s, "")

	// 0.3 HIV 括号清理
	s = cleanupHIVParenRe.ReplaceAllString(s, "")

	// 0.4 姓名/患者标签遮蔽
	s = cleanupNameLabelRe.ReplaceAllString(s, "${1}${2}*")
	s = cleanupPatientLabelRe.ReplaceAllString(s, "${1}${2}*")

	// 0.5 冒号/标点碰撞修复
	s = cleanupColonCommaRe.ReplaceAllString(s, "$1")
	s = cleanupColonPeriodRe.ReplaceAllString(s, "。")
	s = cleanupCommaPeriodRe.ReplaceAllString(s, "$2")

	// 1. 消除连续重复标点
	s = multiPunctPattern.ReplaceAllString(s, "$1")

	// 2. 消除句首多余标点
	s = leadingPunctPattern.ReplaceAllString(s, "")

	// 3. 消除句尾悬空逗号/分号
	s = trailingCommaPattern.ReplaceAllString(s, "")

	// 4. 无宾语动词/提示动词
	s = cleanupNoObjVerbRe.ReplaceAllString(s, "$1")
	s = cleanupNoObjHintRe.ReplaceAllString(s, "$1")

	// 5. 孤立介词/连词碎片
	s = cleanupOrphanPrepRe.ReplaceAllString(s, "$1")
	s = cleanupAppearPunctRe.ReplaceAllString(s, "$1")
	s = cleanupDevelopAndRe.ReplaceAllString(s, "发展为")

	// 6. 孤立无宾语动词
	s = cleanupOrphanVerbRe.ReplaceAllString(s, "$1")
	s = cleanupVerbPunctRe.ReplaceAllString(s, "")

	// 7. 标点/引号/空子句自愈
	s = cleanupRepeatQuotesRe.ReplaceAllString(s, "")
	s = multiPunctPattern.ReplaceAllString(s, "$1")
	s = cleanupEmptyClauseRe.ReplaceAllString(s, "$2")
	s = cleanupEmptyParenRe.ReplaceAllString(s, "")

	// 8. 时间前缀/病史开头/就诊/症状清理
	s = cleanupPatientTimePrefixRe.ReplaceAllString(s, "")
	s = cleanupTimePrefixPunctRe.ReplaceAllString(s, "$1")
	s = cleanupTimePrefixRe.ReplaceAllString(s, "")
	s = cleanupHistoryStartPunctRe.ReplaceAllString(s, "$1")
	s = cleanupHistoryStartRe.ReplaceAllString(s, "")
	s = cleanupSeekCarePunctRe.ReplaceAllString(s, "$1")
	s = cleanupSeekCareRe.ReplaceAllString(s, "")
	s = cleanupSymptomItchRe.ReplaceAllString(s, "$1")

	// 9. 医嘱残余自愈
	s = cleanupDoctorOrderRe.ReplaceAllString(s, "医嘱：遵医嘱常规治疗与健康管理。")

	// 10. 亲属主语 + 空动词重构
	if cleanupFamilyVerbHealRe != nil {
		s = cleanupFamilyVerbHealRe.ReplaceAllString(s, "$1患病$2")
	}
	if cleanupOrphanSubjectRe != nil {
		s = cleanupOrphanSubjectRe.ReplaceAllString(s, "$1")
	}

	// 11. 死因修复
	s = cleanupDieParenRe.ReplaceAllString(s, "$1$2")
	s = cleanupBecauseDieRe.ReplaceAllString(s, "因病$1")
	if cleanupFamilyDieRe != nil {
		s = cleanupFamilyDieRe.ReplaceAllString(s, "$1因病去世$2")
	}
	s = cleanupDiagPrefixCommaRe.ReplaceAllString(s, "$1")

	// 12. 图片路径清洗
	s = cleanupImageExtRe.ReplaceAllString(s, "${1}sanitized_case_image${2}")

	// 13. 前导连词/患者/悬空主语
	s = cleanupLeadingConjRe.ReplaceAllString(s, "")
	s = cleanupLeadingPatientRe.ReplaceAllString(s, "")
	s = cleanupLeadingPatientChestRe.ReplaceAllString(s, "$1")

	// 14. 最终无主语子句检测（≤30字符安全阈值）
	tail := strings.TrimSpace(s)
	if len([]rune(tail)) <= 30 && cleanupSubjectlessClauseRe.MatchString(tail) {
		return ""
	}

	// 15. 最终清理
	s = cleanupEmptyClauseRe.ReplaceAllString(s, "$2")
	s = leadingPunctPattern.ReplaceAllString(s, "")
	s = multiPunctPattern.ReplaceAllString(s, "$1")
	s = strings.TrimSpace(s)
	s = leadingPunctPattern.ReplaceAllString(s, "")
	s = trailingCommaPattern.ReplaceAllString(s, "")

	// 16. 若只剩标点或空白，返回空
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
