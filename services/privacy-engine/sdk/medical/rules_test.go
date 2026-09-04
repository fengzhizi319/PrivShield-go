package medical

import (
	"strings"
	"testing"
)

func TestCanonicalizePIIField(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"身份证号", "id_card_no"},
		{"居民身份证", "id_card_no"},
		{"真实姓名", "name"},
		{"家庭住址", "registered_address"},
		{"id_card_no (身份证号)", "id_card_no"},
		{"主诉", "chief_complaint"},
		{"诊断编码", "icd10_code"},
		{"other_field", "other_field"},
	}

	for _, c := range cases {
		got := CanonicalizePIIField(c.input)
		if got != c.want {
			t.Errorf("CanonicalizePIIField(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestClassifyAndRedactICD10Code(t *testing.T) {
	// L5: HIV (B20-B24)
	level, cat, ok := ClassifyICD10Code("B20.900")
	if !ok || level != "L5" || cat != "MEDICAL_ICD10_HIV" {
		t.Errorf("B20.900 classify = (%q, %q, %v), want (L5, MEDICAL_ICD10_HIV, true)", level, cat, ok)
	}
	if got := RedactICD10Code("B20.900"); got != "" {
		t.Errorf("RedactICD10Code(B20.900) = %q, want empty string", got)
	}

	// L4: Neoplasm (C34.900) — 无痕脱敏：L4 编码也整值抹平
	level, cat, ok = ClassifyICD10Code("C34.900")
	if !ok || level != "L4" || cat != "MEDICAL_ICD10_CANCER" {
		t.Errorf("C34.900 classify = (%q, %q, %v), want (L4, MEDICAL_ICD10_CANCER, true)", level, cat, ok)
	}
	if got := RedactICD10Code("C34.900"); got != "" {
		t.Errorf("RedactICD10Code(C34.900) = %q, want empty string (traceless)", got)
	}

	// L4: STD (A53.900)
	level, cat, ok = ClassifyICD10Code("A53.900")
	if !ok || level != "L4" || cat != "MEDICAL_ICD10_STD" {
		t.Errorf("A53.900 classify = (%q, %q, %v), want (L4, MEDICAL_ICD10_STD, true)", level, cat, ok)
	}

	// Non-sensitive code (I10)
	level, cat, ok = ClassifyICD10Code("I10.x00")
	if ok {
		t.Errorf("I10.x00 should not be high risk, got (%q, %q)", level, cat)
	}
	if got := RedactICD10Code("I10.x00"); got != "I10.x00" {
		t.Errorf("RedactICD10Code(I10.x00) = %q, want I10.x00", got)
	}
}

func TestTruncateDateToMonth(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"1990-05-18", "1990-05"},
		{"2023/12/31", "2023-12"},
		{"2024.1.15", "2024-01"},
		{"invalid-date", "invalid-date"},
		{"", ""},
	}

	for _, c := range cases {
		got := TruncateDateToMonth(c.input)
		if got != c.want {
			t.Errorf("TruncateDateToMonth(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestNormalizeFullwidthAlphanumeric(t *testing.T) {
	input := "患者ＨＩＶ抗体阳性，ＣＤ４细胞计数１２３。"
	want := "患者HIV抗体阳性，CD4细胞计数123。"
	got := NormalizeFullwidthAlphanumeric(input)
	if got != want {
		t.Errorf("NormalizeFullwidthAlphanumeric(%q) = %q, want %q", input, got, want)
	}
}

func TestRedactMedicalText(t *testing.T) {
	// L5: HIV & Psychiatric — 无痕脱敏：敏感词直接擦除，不产生任何标签
	text1 := "患者自述既往有艾滋病病史，长期口服奥氮平片治疗精神分裂症。"
	got1 := RedactMedicalText(text1)
	if strings.Contains(got1, "艾滋病") || strings.Contains(got1, "奥氮平") || strings.Contains(got1, "精神分裂症") {
		t.Errorf("RedactMedicalText did not redact L5 terms: %q", got1)
	}
	// 无痕断言：输出不得包含任何 [L4-...] 或 [L5-...] 提示性标签
	if strings.Contains(got1, "[L5-") || strings.Contains(got1, "[L4-") {
		t.Errorf("RedactMedicalText output contains hinting tags: %q", got1)
	}

	// L4: Malignant neoplasm & STD — 无痕脱敏
	text2 := "初步诊断为肺腺癌晚期，合并梅毒感染。"
	got2 := RedactMedicalText(text2)
	if strings.Contains(got2, "肺腺癌") || strings.Contains(got2, "梅毒") {
		t.Errorf("RedactMedicalText did not redact L4 terms: %q", got2)
	}
	// 无痕断言：输出不得包含任何提示性标签
	if strings.Contains(got2, "[L4-") || strings.Contains(got2, "[L5-") {
		t.Errorf("RedactMedicalText output contains hinting tags: %q", got2)
	}

	// 范畴化泛化验证：「恶性肿瘤家族史」→「相关系统疾病家族史」
	text3 := "有消化道恶性肿瘤家族史。"
	got3 := RedactMedicalText(text3)
	if strings.Contains(got3, "恶性肿瘤") {
		t.Errorf("RedactMedicalText did not generalize: %q", got3)
	}
	if !strings.Contains(got3, "消化道疾病") {
		t.Errorf("RedactMedicalText generalization missing: %q", got3)
	}

	// 句法语境脱敏验证：「因HIV去世」→「因病去世」
	text4 := "因艾滋病导致的并发症去世。"
	got4 := RedactMedicalText(text4)
	if strings.Contains(got4, "艾滋") || strings.Contains(got4, "HIV") {
		t.Errorf("RedactMedicalText did not redact death cause: %q", got4)
	}
	if !strings.Contains(got4, "因病去世") {
		t.Errorf("RedactMedicalText death restructuring missing: %q", got4)
	}

	// 干净文本原样放行验证
	text5 := "高脂血症病史5年，口服阿托伐他汀20mg qn。"
	got5 := RedactMedicalText(text5)
	if got5 != text5 {
		t.Errorf("RedactMedicalText modified clean text: %q, want %q", got5, text5)
	}
}

func TestProcessRecordsFullPipeline(t *testing.T) {
	p := NewYibaoPipeline()
	records := []map[string]string{
		{
			"name":           "张三",
			"id_card_no":     "110101199005181234",
			"phone":          "13812345678",
			"birth_date":     "1990-05-18",
			"diagnosis":      "既往有艾滋病，现诊断为肺腺癌",
			"icd_code":       "B20.900",
			"admission_date": "2023-10-01",
			"total_cost":     "12500.50",
		},
		{
			"name":           "李四",
			"id_card_no":     "110101198503205678",
			"phone":          "13987654321",
			"birth_date":     "1985-03-20",
			"diagnosis":      "高血压二级",
			"icd_code":       "I10.x00",
			"admission_date": "2023-11-15",
			"total_cost":     "850.00",
		},
	}

	result := p.ProcessRecords(records)
	if len(result.SanitizedData) != 2 {
		t.Fatalf("SanitizedData len = %d, want 2", len(result.SanitizedData))
	}
	if len(result.ClassificationReport) != 2 {
		t.Fatalf("ClassificationReport len = %d, want 2", len(result.ClassificationReport))
	}

	// 记录 1 应为 L5
	rep1 := result.ClassificationReport[0]
	if rep1.MaxLevel != "L5" {
		t.Errorf("Record 1 max_level = %q, want 'L5'", rep1.MaxLevel)
	}

	// 检查脱敏后的字段
	san1 := result.SanitizedData[0]
	if san1["name"] != "张*" {
		t.Errorf("sanitized name = %q, want '张*'", san1["name"])
	}
	if san1["id_card_no"] != "110101********1234" {
		t.Errorf("sanitized id_card_no = %q, want '110101********1234'", san1["id_card_no"])
	}
	// 出生日期按标准规范 §6.1 行 4 粗粒度化到**年份**（泛化而非丢弃）。
	// 旧断言 "1990-05" 对应的是整改前的月级截断默认值，现已收紧为年级。
	if san1["birth_date"] != "1990" {
		t.Errorf("sanitized birth_date = %q, want '1990'", san1["birth_date"])
	}
	if san1["icd_code"] != "" {
		t.Errorf("sanitized icd_code for B20.900 = %q, want empty string (purged)", san1["icd_code"])
	}
	if strings.Contains(san1["diagnosis"], "艾滋病") || strings.Contains(san1["diagnosis"], "肺腺癌") {
		t.Errorf("sanitized diagnosis leaked clinical terms: %q", san1["diagnosis"])
	}
}

func TestRedactMedicalText_CoreCases(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"非医嘱离院", "非医嘱离院", "离院"},
		{"肿瘤保留分期", "原发性左肺上叶腺癌(T2N1M0)", "原发性左肺上叶(T2N1M0)"},
		{"HIV_CD4_ART整句擦除", "确诊HIV抗体阳性，CD4+细胞180/μL，行ART抗逆转录治疗", ""},
		{"STD综合句法擦除", "硬下疳伴TPPA滴度1:64阳性(早期梅毒)", ""},
		{"遗传缺陷整句擦除", "基因检测提示亨廷顿舞蹈病(HTT基因CAG重复46次)", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactMedicalText(c.input)
			if got != c.want {
				t.Errorf("RedactMedicalText(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestRedactMedicalText_FourPillars(t *testing.T) {
	// 四柱覆盖：每个病种从病因/体征/诊断/用药四个维度验证
	cases := []struct {
		name  string
		input string
		check func(string) bool // 返回 true 表示失败（包含敏感信息）
	}{
		// HIV/AIDS 四柱
		{"HIV_诊断柱", "确诊艾滋病", func(s string) bool { return strings.Contains(s, "艾滋") }},
		{"HIV_用药柱", "口服替诺福韦+拉米夫定+多替拉韦治疗", func(s string) bool { return strings.Contains(s, "替诺福韦") }},
		{"HIV_检查柱", "CD4+ T细胞计数200/μL", func(s string) bool { return strings.Contains(s, "CD4") }},
		{"HIV_病因柱", "HIV感染史", func(s string) bool { return strings.Contains(s, "HIV") }},

		// 精神病 四柱
		{"精神_诊断柱", "诊断为精神分裂症", func(s string) bool { return strings.Contains(s, "精神分裂") }},
		{"精神_用药柱", "长期服用奥氮平片", func(s string) bool { return strings.Contains(s, "奥氮平") }},
		{"精神_体征柱", "出现命令性幻听", func(s string) bool { return strings.Contains(s, "幻听") }},

		// 性病 四柱
		{"STD_体征柱", "外阴多发菜花状赘生物", func(s string) bool { return strings.Contains(s, "赘生物") }},
		{"STD_检查柱", "醋酸白试验阳性", func(s string) bool { return strings.Contains(s, "醋酸白") }},
		{"STD_用药柱", "行CO2激光灼除术", func(s string) bool { return strings.Contains(s, "激光") }},

		// 恶性肿瘤 四柱
		{"肿瘤_诊断柱", "初步诊断肺腺癌", func(s string) bool { return strings.Contains(s, "肺腺癌") }},
		{"肿瘤_用药柱", "口服奥希替尼靶向治疗", func(s string) bool { return strings.Contains(s, "奥希替尼") }},
		{"肿瘤_检查柱", "EGFR基因检测突变阳性", func(s string) bool { return strings.Contains(s, "EGFR") }},

		// 肝炎 四柱
		{"肝炎_诊断柱", "慢性乙型病毒性肝炎", func(s string) bool { return strings.Contains(s, "乙肝") || strings.Contains(s, "乙型") }},
		{"肝炎_检查柱", "HBsAg阳性", func(s string) bool { return strings.Contains(s, "HBsAg") }},
		{"肝炎_用药柱", "口服恩替卡韦抗病毒", func(s string) bool { return strings.Contains(s, "恩替卡韦") }},

		// 遗传缺陷
		{"遗传_诊断柱", "亨廷顿舞蹈病", func(s string) bool { return strings.Contains(s, "亨廷顿") }},
		{"遗传_检查柱", "HTT基因CAG重复序列扩增", func(s string) bool { return strings.Contains(s, "HTT") || strings.Contains(s, "CAG") }},

		// 器官损害
		{"器官_诊断柱", "慢性阻塞性肺疾病COPD", func(s string) bool { return strings.Contains(s, "COPD") }},
		{"器官_体征柱", "急性心肌梗死", func(s string) bool { return strings.Contains(s, "心肌梗死") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactMedicalText(c.input)
			if c.check(got) {
				t.Errorf("RedactMedicalText(%q) = %q, still contains sensitive terms", c.input, got)
			}
		})
	}
}

func TestRedactMedicalText_EvasionVariants(t *testing.T) {
	cases := []struct {
		name  string
		input string
		check func(string) bool
	}{
		// 全角绕过
		{"全角HIV", "患者ＨＩＶ抗体阳性", func(s string) bool { return strings.Contains(s, "HIV") || strings.Contains(s, "ＨＩＶ") }},
		{"全角AIDS", "ＡＩＤＳ患者", func(s string) bool { return strings.Contains(s, "AIDS") || strings.Contains(s, "ＡＩＤＳ") }},
		// 拼音绕过
		{"拼音feiai", "诊断为feiai晚期", func(s string) bool { return strings.Contains(s, "feiai") }},
		{"拼音aizibing", "既往aizibing病史", func(s string) bool { return strings.Contains(s, "aizibing") }},
		// 形近字绕过
		{"形近字H1V", "H1V感染者", func(s string) bool { return strings.Contains(s, "H1V") }},
		// 中英文混合绕过
		{"混合肺ai", "左肺ai晚期", func(s string) bool { return strings.Contains(s, "肺ai") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactMedicalText(c.input)
			if c.check(got) {
				t.Errorf("RedactMedicalText(%q) = %q, evasion variant not caught", c.input, got)
			}
		})
	}
}

func TestRedactMedicalText_ASCIIBoundary(t *testing.T) {
	// ASCII 词边界保护：中文文本中的英文敏感词应正确识别
	cases := []struct {
		name  string
		input string
		check func(t *testing.T, got string)
	}{
		{
			"独立HIV应擦除",
			"患者HIV抗体阳性",
			func(t *testing.T, got string) {
				if strings.Contains(got, "HIV") {
					t.Errorf("HIV not redacted: %q", got)
				}
			},
		},
		{
			"中文语境COPD应擦除",
			"诊断为COPD",
			func(t *testing.T, got string) {
				if strings.Contains(got, "COPD") {
					t.Errorf("COPD not redacted: %q", got)
				}
			},
		},
		{
			"干净英文文本",
			"high blood pressure",
			func(t *testing.T, got string) {
				// 无敏感词的英文文本应原样保留
				if got != "high blood pressure" {
					t.Errorf("clean English text modified: %q", got)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactMedicalText(c.input)
			c.check(t, got)
		})
	}
}

func TestRedactMedicalText_SyntaxSelfHealing(t *testing.T) {
	cases := []struct {
		name  string
		input string
		check func(t *testing.T, got string)
	}{
		{
			"HAART重构",
			"开展HAART抗病毒治疗",
			func(t *testing.T, got string) {
				if strings.Contains(got, "HAART") || strings.Contains(got, "抗病毒") {
					t.Errorf("HAART not restructured: %q", got)
				}
			},
		},
		{
			"死因修复_因病去世",
			"因艾滋病导致的并发症去世。",
			func(t *testing.T, got string) {
				if strings.Contains(got, "艾滋") {
					t.Errorf("death cause not redacted: %q", got)
				}
				if !strings.Contains(got, "因病去世") {
					t.Errorf("death restructuring missing: %q", got)
				}
			},
		},
		{
			"干净文本不修改",
			"高脂血症病史5年，口服阿托伐他汀20mg qn。",
			func(t *testing.T, got string) {
				if got != "高脂血症病史5年，口服阿托伐他汀20mg qn。" {
					t.Errorf("clean text modified: %q", got)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactMedicalText(c.input)
			c.check(t, got)
		})
	}
}

func TestRedactMedicalText_SafetyGate(t *testing.T) {
	// 后处理安全门：若输出仍含 L4/L5 词 → 整值清空
	input := "艾滋病精神分裂症亨廷顿舞蹈病梅毒乙肝癌症"
	got := RedactMedicalText(input)
	if ContainsHighRiskText(got) {
		t.Errorf("safety gate failed: output still contains high-risk text: %q", got)
	}
}

func TestPipelineComplianceGuarantee(t *testing.T) {
	p := NewYibaoPipeline()
	records := []map[string]string{
		{
			"name":      "张三",
			"diagnosis": "既往有艾滋病，现诊断为肺腺癌",
			"icd_code":  "B20.900",
		},
	}
	result := p.ProcessRecords(records)

	guaranteed, ok := result.Summary["compliance_guaranteed"].(bool)
	if !ok || !guaranteed {
		t.Errorf("compliance_guaranteed = %v, want true", result.Summary["compliance_guaranteed"])
	}

	leaked, _ := result.Summary["leaked_fields_post_sanitize"].(int)
	if leaked != 0 {
		t.Errorf("leaked_fields_post_sanitize = %d, want 0", leaked)
	}
}

func TestIsDiagnosisField(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"diagnosis", true},
		{"diagnosis_name", true},
		{"icd_code", true},
		{"chief_complaint", true},
		{"诊断", true},
		{"主诉", true},
		{"name", false},
		{"department", false},
		{"total_cost", false},
	}
	for _, c := range cases {
		got := isDiagnosisField(c.input)
		if got != c.want {
			t.Errorf("isDiagnosisField(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}
