package dynclassification

import (
	"testing"
)

// ──────────────────────────────────────────────
// 基准测试：规则引擎与 AC 自动机
// ──────────────────────────────────────────────

func benchmarkRules() []RuleDef {
	return []RuleDef{
		{ID: "id_card", Level: LevelSecret, Category: "pii.identity",
			FieldPatterns: []string{`(?i)(id_?card|身份证|identity)`}},
		{ID: "phone", Level: LevelConfidential, Category: "pii.contact",
			FieldPatterns: []string{`(?i)(phone|mobile|手机|电话)`}},
		{ID: "email", Level: LevelConfidential, Category: "pii.contact",
			FieldPatterns: []string{`(?i)(email|邮箱|邮件)`}},
		{ID: "bank_card", Level: LevelSecret, Category: "pii.financial",
			FieldPatterns: []string{`(?i)(bank_?card|银行卡|信用卡)`}},
		{ID: "name", Level: LevelConfidential, Category: "pii.identity",
			FieldPatterns: []string{`(?i)(^name$|patient_name|姓名)`}},
		{ID: "address", Level: LevelConfidential, Category: "pii.location",
			FieldPatterns: []string{`(?i)(address|地址|住址)`}},
		{ID: "medical_record", Level: LevelSecret, Category: "medical.record",
			FieldPatterns: []string{`(?i)(medical_record|病历|诊断)`}},
		{ID: "social_security", Level: LevelTopSecret, Category: "pii.financial",
			FieldPatterns: []string{`(?i)(social_security|社保|医保号)`}},
	}
}

func BenchmarkClassify_FieldMatch(b *testing.B) {
	engine, err := NewRuleEngine(benchmarkRules())
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		engine.Classify("id_card_no", "110101199003072345")
	}
}

func BenchmarkClassify_NoMatch(b *testing.B) {
	engine, err := NewRuleEngine(benchmarkRules())
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		engine.Classify("diagnosis", "2型糖尿病")
	}
}

func BenchmarkClassifyBatch_10Records(b *testing.B) {
	engine, err := NewRuleEngine(benchmarkRules())
	if err != nil {
		b.Fatal(err)
	}

	records := make([]map[string]string, 10)
	for i := range records {
		records[i] = map[string]string{
			"id_card_no":   "110101199003072345",
			"phone":        "13812345678",
			"patient_name": "张三",
			"diagnosis":    "2型糖尿病",
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		engine.ClassifyBatch(records)
	}
}

func BenchmarkACAutomaton_Search(b *testing.B) {
	ac := NewACAutomaton()
	ac.AddPattern("id_card", "身份证")
	ac.AddPattern("phone", "手机号")
	ac.AddPattern("hiv", "HIV")
	ac.AddPattern("aids", "艾滋病")
	ac.Build()

	text := "患者身份证号为110101199003072345，手机号13812345678，HIV抗体阳性，艾滋病确诊"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ac.Search(text)
	}
}
