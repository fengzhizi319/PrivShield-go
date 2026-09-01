package store

import (
	"strings"
	"testing"

	"github.com/fengzhizi319/PrivShield/pkg/naming"
)

// TestBuildAuditRecommendationsUsesTaxonomyLabels 断言报表建议文案里的等级中文名
// 直接取自规则库词表（P1-5），任何后端不得再自带一份「机密/绝密」旧口径。
func TestBuildAuditRecommendationsUsesTaxonomyLabels(t *testing.T) {
	recs := BuildAuditRecommendations(map[string]int{
		naming.SecurityLevelL4: RecommendHighSensitiveCount + 1,
		naming.SecurityLevelL5: RecommendTopSecretCount + 1,
	}, 90.0)

	if len(recs) != 3 {
		t.Fatalf("expected 3 recommendations, got %d: %v", len(recs), recs)
	}
	if want := naming.SecurityLevelLabel(naming.SecurityLevelL4); !strings.Contains(recs[0], want) {
		t.Errorf("L4 recommendation %q must carry taxonomy label %q", recs[0], want)
	}
	if want := naming.SecurityLevelLabel(naming.SecurityLevelL5); !strings.Contains(recs[1], want) {
		t.Errorf("L5 recommendation %q must carry taxonomy label %q", recs[1], want)
	}
	for _, r := range recs {
		for _, obsolete := range []string{"机密数据", "绝密数据"} {
			if strings.Contains(r, obsolete) {
				t.Errorf("recommendation %q uses superseded label %q", r, obsolete)
			}
		}
	}
}

// TestBuildAuditRecommendationsBelowThresholds 验证阈值边界（严格大于）与 healthy 兜底文案。
func TestBuildAuditRecommendationsBelowThresholds(t *testing.T) {
	atThreshold := BuildAuditRecommendations(map[string]int{
		naming.SecurityLevelL4: RecommendHighSensitiveCount,
		naming.SecurityLevelL5: RecommendTopSecretCount,
	}, RecommendMinSuccessRate)
	if len(atThreshold) != 1 || atThreshold[0] != "审计指标正常，无需特别关注" {
		t.Errorf("thresholds must be exclusive, got %v", atThreshold)
	}

	aboveRate := BuildAuditRecommendations(map[string]int{naming.SecurityLevelL1: 5}, 99.9)
	if len(aboveRate) != 1 {
		t.Errorf("expected only the healthy line, got %v", aboveRate)
	}
}
