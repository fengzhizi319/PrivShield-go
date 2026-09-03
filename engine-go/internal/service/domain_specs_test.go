package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/medical"
)

func TestDomainSpecsYAMLLoader(t *testing.T) {
	tempDir := t.TempDir()
	yamlContent := `domain: "medical"
field_specs:
  - name: "patient_age"
    category: "identity"
    level: 2
    treatment: "age_band"
  - name: "consultation_fee"
    category: "financial"
    level: 3
    treatment: "bounding"
    band: 50.0
aliases:
  "cust_patient_name": "name"
`
	yamlPath := filepath.Join(tempDir, "medical_custom.yaml")
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test yaml: %v", err)
	}

	pipe := medical.NewFullMedicalPipeline()

	loadAndRegisterDomainSpecs(tempDir, pipe)
	defer medical.ResetCustomFieldAliases()

	// 1. 验证字段规格是否成功注入
	specAge := pipe.GetFieldSpec("patient_age")
	if specAge == nil {
		t.Fatal("patient_age should be registered in medical pipeline")
	}
	if specAge.Treatment != medical.TreatmentAgeBand {
		t.Errorf("patient_age treatment = %v, want TreatmentAgeBand", specAge.Treatment)
	}

	specFee := pipe.GetFieldSpec("consultation_fee")
	if specFee == nil {
		t.Fatal("consultation_fee should be registered in medical pipeline")
	}
	if specFee.Treatment != medical.TreatmentBounding {
		t.Errorf("consultation_fee treatment = %v, want TreatmentBounding", specFee.Treatment)
	}
	if specFee.Band != 50.0 {
		t.Errorf("consultation_fee band = %f, want 50.0", specFee.Band)
	}

	// 2. 验证别名是否生效
	if canon := medical.CanonicalizePIIField("cust_patient_name"); canon != "name" {
		t.Errorf("expected CanonicalizePIIField(cust_patient_name) = 'name', got %q", canon)
	}

	// 3. 验证通过 PrivacyService 完整端到端脱敏
	cfg := DefaultConfig()
	cfg.RulesDir = tempDir
	svc, err := NewPrivacyService(cfg)
	if err != nil {
		t.Fatalf("NewPrivacyService failed: %v", err)
	}

	record := map[string]string{
		"patient_age":       "88",
		"consultation_fee":  "123.45",
		"cust_patient_name": "张三丰",
	}

	sanitized, err := svc.SanitizeMedicalRecord(record, "yibao")
	if err != nil {
		t.Fatalf("SanitizeMedicalRecord failed: %v", err)
	}

	// patient_age 应当被泛化为区间 [88-90)
	if sanitized["patient_age"] != "[88-90)" {
		t.Errorf("sanitized patient_age = %q, want '[88-90)'", sanitized["patient_age"])
	}
	// consultation_fee 应当被分箱 (50.0 步长，123.45 -> [100.0~150.0])
	if sanitized["consultation_fee"] != "[100.0~150.0]" {
		t.Errorf("sanitized consultation_fee = %q, want '[100.0~150.0]'", sanitized["consultation_fee"])
	}
	// cust_patient_name 别名映射为 name，应当脱敏为 张**丰
	if sanitized["cust_patient_name"] != "张**丰" {
		t.Errorf("sanitized cust_patient_name = %q, want '张**丰'", sanitized["cust_patient_name"])
	}
}
