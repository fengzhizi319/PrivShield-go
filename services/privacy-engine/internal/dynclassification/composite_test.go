package dynclassification

import (
	"testing"
)

func TestCompositeRuleEngine_Evaluate_FullPII(t *testing.T) {
	engine := NewCompositeRuleEngine(DefaultCompositeRules())

	// 姓名 + 身份证 + 手机 → 触发 full_pii_identity (min_matches=3)
	record := map[string]string{
		"name":    "张三",
		"id_card": "110101199001011234",
		"phone":   "13800138000",
		"city":    "北京",
	}

	tags := engine.Evaluate(record)
	found := false
	for _, tag := range tags {
		if tag.RuleID == "full_pii_identity" {
			found = true
			if tag.Level != LevelTopSecret {
				t.Errorf("expected top_secret, got %s", tag.Level)
			}
			if tag.SourceEngine != "COMPOSITE" {
				t.Errorf("expected COMPOSITE, got %s", tag.SourceEngine)
			}
			if tag.Confidence != 1.0 {
				t.Errorf("expected confidence 1.0, got %f", tag.Confidence)
			}
		}
	}
	if !found {
		t.Error("expected full_pii_identity rule to fire")
	}
}

func TestCompositeRuleEngine_Evaluate_MedicalIdentity(t *testing.T) {
	engine := NewCompositeRuleEngine(DefaultCompositeRules())

	// 姓名 + 诊断 → 触发 medical_identity (min_matches=2)
	record := map[string]string{
		"patient_name": "李四",
		"diagnosis":    "高血压",
	}

	tags := engine.Evaluate(record)
	found := false
	for _, tag := range tags {
		if tag.RuleID == "medical_identity" {
			found = true
			if tag.Level != LevelSecret {
				t.Errorf("expected secret, got %s", tag.Level)
			}
		}
	}
	if !found {
		t.Error("expected medical_identity rule to fire")
	}
}

func TestCompositeRuleEngine_Evaluate_NoMatch(t *testing.T) {
	engine := NewCompositeRuleEngine(DefaultCompositeRules())

	// 单个不敏感字段 → 不触发任何规则
	record := map[string]string{
		"city":   "北京",
		"gender": "M",
	}

	tags := engine.Evaluate(record)
	if len(tags) != 0 {
		t.Errorf("expected 0 tags, got %d: %+v", len(tags), tags)
	}
}

func TestCompositeRuleEngine_Evaluate_FieldNormalization(t *testing.T) {
	engine := NewCompositeRuleEngine(DefaultCompositeRules())

	// id-card（连字符）和 id_card（下划线）都应匹配
	record := map[string]string{
		"name":    "王五",
		"id-card": "110101199001011234",
		"mobile":  "13800138000",
	}

	tags := engine.Evaluate(record)
	found := false
	for _, tag := range tags {
		if tag.RuleID == "full_pii_identity" {
			found = true
		}
	}
	if !found {
		t.Error("expected full_pii_identity to fire with hyphenated field name")
	}
}

func TestCompositeRuleEngine_ApplyToRecordLevel(t *testing.T) {
	engine := NewCompositeRuleEngine(DefaultCompositeRules())

	// 当前等级 confidential，复合标签升级到 top_secret
	tags := []CompositeTag{
		{Level: LevelTopSecret, SourceEngine: "COMPOSITE"},
	}
	result := engine.ApplyToRecordLevel(LevelConfidential, tags)
	if result != LevelTopSecret {
		t.Errorf("expected top_secret, got %s", result)
	}
}

func TestCompositeRuleEngine_ApplyToRecordLevel_NoTags(t *testing.T) {
	engine := NewCompositeRuleEngine(DefaultCompositeRules())

	result := engine.ApplyToRecordLevel(LevelConfidential, nil)
	if result != LevelConfidential {
		t.Errorf("expected confidential (unchanged), got %s", result)
	}
}

func TestCompositeRuleEngine_EmptyRules(t *testing.T) {
	engine := NewCompositeRuleEngine(nil)
	record := map[string]string{"name": "张三", "phone": "138"}
	tags := engine.Evaluate(record)
	if len(tags) != 0 {
		t.Errorf("expected 0 tags with empty rules, got %d", len(tags))
	}
}

func TestCompositeRuleEngine_FinancialIdentity(t *testing.T) {
	engine := NewCompositeRuleEngine(DefaultCompositeRules())

	record := map[string]string{
		"name":      "赵六",
		"bank_card": "6222021234567890",
	}

	tags := engine.Evaluate(record)
	found := false
	for _, tag := range tags {
		if tag.RuleID == "financial_identity" {
			found = true
			if tag.Level != LevelSecret {
				t.Errorf("expected secret, got %s", tag.Level)
			}
		}
	}
	if !found {
		t.Error("expected financial_identity rule to fire")
	}
}
