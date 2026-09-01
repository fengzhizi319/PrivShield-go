// 审计报告建议生成器，供全部存储后端共享。
//
// ==============================================================================
// 【为什么需要单一事实源】
// 「审计报告建议」原先在 memory / sqlite / postgres 中各写了一份，同一等级出现
// 「机密数据 / 高敏感数据」「绝密数据 / 极敏感数据」两种口径，导致对外报表与规则库互相矛盾。
//
// 本文件只保留建议文案与阈值；等级词表本身（L1~L5 标识、中文名、排名）的唯一事实源是
// pkg/naming/levels.go，并由其单元测试断言与 `rules/taxonomies/default.yaml` 完全一致。
// ==============================================================================

package store

import (
	"fmt"

	"github.com/fengzhizi319/PrivShield/pkg/naming"
)

// 审计报告建议的触发阈值：低于以下门槛不提相应建议。
const (
	// RecommendHighSensitiveCount 是「高敏感数据操作偏多」建议的条数阈值。
	RecommendHighSensitiveCount = 100
	// RecommendTopSecretCount 是「极敏感数据操作偏多」建议的条数阈值。
	RecommendTopSecretCount = 50
	// RecommendMinSuccessRate 是可接受的审计成功率下限（百分比）。
	RecommendMinSuccessRate = 95.0
)

// BuildAuditRecommendations 依据等级分布与成功率生成审计报告建议。
//
// 三个存储后端（memory / sqlite / postgres）统一调用本函数，保证同一份统计数据
// 在任何后端都得到逐字一致的建议文案与阈值口径。
func BuildAuditRecommendations(byLevel map[string]int, successRate float64) []string {
	recs := make([]string, 0, 4)
	if n := byLevel[naming.SecurityLevelL4]; n > RecommendHighSensitiveCount {
		recs = append(recs, fmt.Sprintf("L4 %s操作频繁，建议审查差分隐私预算消耗", naming.SecurityLevelLabel(naming.SecurityLevelL4)))
	}
	if n := byLevel[naming.SecurityLevelL5]; n > RecommendTopSecretCount {
		recs = append(recs, fmt.Sprintf("L5 %s操作较多，建议加强访问控制审计", naming.SecurityLevelLabel(naming.SecurityLevelL5)))
	}
	if successRate < RecommendMinSuccessRate {
		recs = append(recs, fmt.Sprintf("成功率 %.1f%% 低于 %.0f%%，建议排查失败原因", successRate, RecommendMinSuccessRate))
	}
	if len(recs) == 0 {
		recs = append(recs, "审计指标正常，无需特别关注")
	}
	return recs
}
