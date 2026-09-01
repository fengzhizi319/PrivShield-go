package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/dynclassification"
)

// TestSafetyFloorMinLevelRejectsUnknownVocabulary pins the P0-2 fail-closed rule:
// a mistyped min_level must never downgrade the floor to public.
// TestSafetyFloorMinLevelRejectsUnknownVocabulary 固化 P0-2 fail-closed 约束：
// min_level 拼写错误绝不能把底线静默降到 public（整改前 LevelFromString 的兜底行为）。
func TestSafetyFloorMinLevelRejectsUnknownVocabulary(t *testing.T) {
	cases := []struct {
		raw     string
		wantOK  bool
		wantLvl dynclassification.SecurityLevel
	}{
		{"internal", true, dynclassification.LevelInternal},
		{"  confidential ", true, dynclassification.LevelConfidential},
		{"L4", true, dynclassification.LevelSecret}, // DB51 词表同样接受
		{"top_secret", true, dynclassification.LevelTopSecret},
		{"confidntial", false, ""}, // 拼写错误：词表外，保留 restrictive 默认底线
		{"L0", false, ""},
		{"critical", false, ""},
		{"", false, ""},
	}
	for _, tc := range cases {
		t.Run("min_level="+tc.raw, func(t *testing.T) {
			sec := safetyFloorSection{MinLevel: tc.raw}
			got, ok := sec.minLevelSecurityLevel()
			if ok != tc.wantOK {
				t.Fatalf("accepted = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.wantLvl {
				t.Errorf("level = %q, want %q", got, tc.wantLvl)
			}
		})
	}
}

// TestApplyToSafetyFloorConfigKeepsFloorOnBadValue proves the caller really keeps the
// restrictive default instead of the historical public fallback.
// TestApplyToSafetyFloorConfigKeepsFloorOnBadValue 证明调用方确实保留了
// restrictive 默认底线，而不是回到整改前的 public。
func TestApplyToSafetyFloorConfigKeepsFloorOnBadValue(t *testing.T) {
	base := dynclassification.DefaultSafetyFloorConfig()
	if base.MinLevel != dynclassification.LevelInternal {
		t.Fatalf("default floor = %q, want internal (P0-2 default-deny)", base.MinLevel)
	}

	got := safetyFloorSection{MinLevel: "confidntial"}.applyToSafetyFloorConfig(base)
	if got.MinLevel != dynclassification.LevelInternal {
		t.Errorf("floor after invalid value = %q, want the unchanged default %q", got.MinLevel, base.MinLevel)
	}

	got = safetyFloorSection{MinLevel: "L5"}.applyToSafetyFloorConfig(base)
	if got.MinLevel != dynclassification.LevelTopSecret {
		t.Errorf("floor after valid L5 = %q, want top_secret", got.MinLevel)
	}
}

// TestPrivacyPolicyFileBindsSafetyFloorAndClassification covers P2-2: the shipped
// config/privacy.yaml keys must actually reach the funnel and safety floor configs.
// TestPrivacyPolicyFileBindsSafetyFloorAndClassification 覆盖 P2-2：
// config/privacy.yaml 的键必须真正生效，而不是被代码里的默认值覆盖。
func TestPrivacyPolicyFileBindsSafetyFloorAndClassification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "privacy.yaml")
	body := "" +
		"safety_floor:\n" +
		"  min_level: \"secret\"\n" +
		"  confidence_threshold: 0.8\n" +
		"  force_upgrade_on_uncertainty: false\n" +
		"  unlisted_min_level: \"L4\"\n" +
		"classification:\n" +
		"  confidence_threshold: 0.55\n" +
		"  enable_llm: true\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	policy, err := loadPrivacyPolicy(path)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}

	sf := policy.SafetyFloor.applyToSafetyFloorConfig(dynclassification.DefaultSafetyFloorConfig())
	if sf.MinLevel != dynclassification.LevelSecret {
		t.Errorf("min_level = %q, want secret", sf.MinLevel)
	}
	if sf.ConfidenceThreshold != 0.8 {
		t.Errorf("confidence_threshold = %v, want 0.8", sf.ConfidenceThreshold)
	}
	if sf.ForceUpgradeOnUncertainty {
		t.Error("force_upgrade_on_uncertainty = true, want false from config")
	}
	// P0-2：未命中规则的字段下限可配，但取值必须落在 L1~L5。
	if floor := policy.SafetyFloor.resolveUnlistedFloor(); floor.MinRank != 4 {
		t.Errorf("unlisted floor rank = %d, want 4 (unlisted_min_level: L4)", floor.MinRank)
	}

	funnel := policy.Classification.applyToFunnelConfig(dynclassification.DefaultFunnelConfig())
	if !funnel.EnableLLM {
		t.Error("classification.enable_llm did not reach the funnel config")
	}
	if funnel.NERConfidenceThreshold != 0.55 {
		t.Errorf("NERConfidenceThreshold = %v, want 0.55", funnel.NERConfidenceThreshold)
	}
}
