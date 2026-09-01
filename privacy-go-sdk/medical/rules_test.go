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
