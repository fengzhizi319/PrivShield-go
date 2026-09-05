package service

import (
	"testing"
)

func TestDynManagement(t *testing.T) {
	cfg := DefaultConfig()
	svc, err := NewPrivacyService(cfg)
	if err != nil {
		t.Fatalf("NewPrivacyService failed: %v", err)
	}

	// 1. ListStandardsDetail
	stds, err := svc.ListStandardsDetail()
	if err != nil {
		t.Fatalf("ListStandardsDetail failed: %v", err)
	}
	t.Logf("Found %d standards", len(stds.Standards))

	// 2. ListDomains
	doms := svc.ListDomains()
	t.Logf("Found %d domains", len(doms.Domains))

	// 3. ListOperators
	ops := svc.ListOperators()
	if len(ops.Operators) == 0 {
		t.Fatalf("expected non-empty operators list")
	}
	t.Logf("Found %d operators", len(ops.Operators))

	// 4. ValidateRules
	val := svc.ValidateRules("")
	t.Logf("ValidateRules: valid=%v, errors=%v", val.IsValid, val.Errors)

	// 5. GenerateProfile
	gen := svc.GenerateProfile("docs/standard/四川省健康医疗大数据应用指南.md")
	if gen.Status != "ok" || len(gen.GeneratedFiles) == 0 {
		t.Fatalf("expected valid GenerateProfile response, got %+v", gen)
	}
}
