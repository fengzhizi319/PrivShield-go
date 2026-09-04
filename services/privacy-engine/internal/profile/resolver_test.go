package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolver_Defaults(t *testing.T) {
	r := NewResolver()

	params := r.Resolve("dp", "", nil)
	if params["epsilon"] != 1.0 {
		t.Errorf("expected epsilon 1.0, got %v", params["epsilon"])
	}
	if params["mechanism"] != "laplace" {
		t.Errorf("expected mechanism laplace, got %v", params["mechanism"])
	}
}

func TestResolver_Override(t *testing.T) {
	r := NewResolver()

	params := r.Resolve("dp", "", map[string]interface{}{
		"epsilon": 2.0,
	})
	if params["epsilon"] != 2.0 {
		t.Errorf("expected epsilon 2.0 after override, got %v", params["epsilon"])
	}
}

func TestResolver_UnknownPrimitive(t *testing.T) {
	r := NewResolver()

	params := r.Resolve("unknown", "", nil)
	if len(params) != 0 {
		t.Errorf("expected empty params for unknown primitive, got %v", params)
	}
}

func TestResolver_LoadFromYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "profile.yaml")
	content := `name: "custom"
version: "2.0"
defaults:
  dp:
    epsilon: 0.5
    mechanism: "gaussian"
  k_anonymity:
    k: 10
`
	if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewResolver()
	if err := r.LoadFromYAML(yamlPath); err != nil {
		t.Fatal(err)
	}

	params := r.Resolve("dp", "", nil)
	if params["epsilon"] != 0.5 {
		t.Errorf("expected epsilon 0.5 from YAML, got %v", params["epsilon"])
	}
	if params["mechanism"] != "gaussian" {
		t.Errorf("expected mechanism gaussian, got %v", params["mechanism"])
	}
}

func TestResolver_Recommend(t *testing.T) {
	r := NewResolver()
	rec := r.Recommend()
	if rec["recommended_profile"] != "standard" {
		t.Errorf("expected standard profile, got %v", rec["recommended_profile"])
	}
	if rec["epsilon"] != 1.0 {
		t.Errorf("expected epsilon 1.0, got %v", rec["epsilon"])
	}
}

func TestValidate_DP(t *testing.T) {
	if err := Validate("dp", map[string]interface{}{"epsilon": 1.0}); err != nil {
		t.Errorf("expected valid, got error: %v", err)
	}
	if err := Validate("dp", map[string]interface{}{"epsilon": -1.0}); err == nil {
		t.Error("expected error for negative epsilon")
	}
}

func TestValidate_KAnonymity(t *testing.T) {
	if err := Validate("k_anonymity", map[string]interface{}{"k": 5}); err != nil {
		t.Errorf("expected valid, got error: %v", err)
	}
	if err := Validate("k_anonymity", map[string]interface{}{"k": 1}); err == nil {
		t.Error("expected error for k < 2")
	}
}

func TestValidate_Unknown(t *testing.T) {
	if err := Validate("unknown", nil); err != nil {
		t.Errorf("expected no error for unknown primitive, got: %v", err)
	}
}
