package dynclassification

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStandardsFromDir(t *testing.T) {
	dir := t.TempDir()

	content := `
standard_id: test_standard
description: Test standard mapping
taxonomy: default
domains:
- general-pii
global_params:
  default_level: "L3"
levels:
  CORE:
    national_category: "核心数据"
    level: "L5"
    rank: 5
    note: "test note"
  PUBLIC:
    national_category: "公开数据"
    level: "L1"
    rank: 1
    note: "public data"
extra_rules: []
`
	if err := os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	standards, errs := LoadStandardsFromDir(dir)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(standards) != 1 {
		t.Fatalf("got %d standards, want 1", len(standards))
	}

	sd := standards[0]
	if sd.StandardID != "test_standard" {
		t.Errorf("standard_id = %q, want %q", sd.StandardID, "test_standard")
	}
	if sd.DefaultLevel() != "L3" {
		t.Errorf("default_level = %q, want %q", sd.DefaultLevel(), "L3")
	}
	if len(sd.Levels) != 2 {
		t.Errorf("levels count = %d, want 2", len(sd.Levels))
	}
	if sd.Levels["CORE"].Level != "L5" {
		t.Errorf("CORE level = %q, want %q", sd.Levels["CORE"].Level, "L5")
	}
}

func TestLoadStandardsFromDir_Empty(t *testing.T) {
	dir := t.TempDir()
	standards, errs := LoadStandardsFromDir(dir)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(standards) != 0 {
		t.Errorf("got %d standards, want 0", len(standards))
	}
}

func TestLoadStandardsFromDir_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	// Unmatched bracket causes YAML parse error
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("key: [unmatched"), 0644); err != nil {
		t.Fatal(err)
	}

	_, errs := LoadStandardsFromDir(dir)
	if len(errs) == 0 {
		t.Error("expected parse error for invalid YAML")
	}
}

func TestStandardsSummary(t *testing.T) {
	stds := []StandardDef{
		{StandardID: "gbt43697", Description: "GB/T 43697-2024", Taxonomy: "default"},
	}
	stds[0].GlobalParams.DefaultLevel = "L3"

	summary := StandardsSummary(stds)
	if len(summary) != 1 {
		t.Fatalf("got %d entries, want 1", len(summary))
	}
	if summary[0]["standard_id"] != "gbt43697" {
		t.Errorf("standard_id = %v, want gbt43697", summary[0]["standard_id"])
	}
	if summary[0]["default_level"] != "L3" {
		t.Errorf("default_level = %v, want L3", summary[0]["default_level"])
	}
}
