// Package qol 提供查询混淆 (Query Obfuscation Layer) 原语。
//
// 实现语义诱饵生成与 Fisher-Yates 随机置乱注入，
// 用于防止外部搜索引擎/大模型通过语义侧信道探测真实查询意图。
// 诱饵词库与 Python engine/privacy/qol.py 对齐，确保跨语言一致性。
package qol

import (
	"math/rand/v2"
)

// ──────────────────────────────────────────────
// 医疗领域诱饵词库（与 Python MEDICAL_DUMMY 对齐）
// ──────────────────────────────────────────────

var medicalDecoys = []string{
	"高血压患者的日常饮食建议",
	"糖尿病患者运动注意事项",
	"冠心病的早期症状有哪些",
	"流感疫苗接种人群建议",
	"儿童常见过敏反应处理",
	"胃溃疡患者吃什么食物好",
	"哮喘发作时的紧急处理方法",
	"慢性支气管炎的预防措施",
	"骨质疏松患者补钙与运动方案",
	"长期失眠的危害及改善建议",
	"脂肪肝患者运动处方",
	"痛风患者避免食用的食物清单",
	"过敏性鼻炎的日常防治手段",
	"颈椎病康复训练操指南",
	"偏头痛的诱发因素与缓解方式",
	"脑梗塞前兆表现及预防建议",
	"骨质疏松防摔倒安全提示",
	"带状疱疹的临床表现及治疗",
	"过敏性皮炎日常注意事项",
	"甲状腺结节患者饮食禁忌",
}

// generalDecoys 通用领域诱饵词库（与 Python GENERIC_DUMMY 对齐）
var generalDecoys = []string{
	"天气预报查询",
	"附近医院挂号流程",
	"健康档案如何查询",
	"医保报销比例说明",
	"体检报告解读指南",
	"公积金提取线上办理步骤",
	"个人所得税申报操作引导",
	"社保卡丢失如何在线补办",
	"市民卡网点营业时间查询",
	"生活垃圾分类最新标准",
	"最近的公共图书馆开放时间",
	"电动自行车上牌申领流程",
	"常用快递运费价格对比",
	"附近免费公共停车场推荐",
	"燃气费线上缴费使用指南",
	"自来水水质检测结果公告",
	"本地博物馆门票预约入口",
	"公交线路首末班车时间查询",
	"居住证积分申请材料清单",
	"数字证书在线更新流程",
}

// ──────────────────────────────────────────────
// 公开 API
// ──────────────────────────────────────────────

// GenerateMedicalDecoy 随机生成一条医疗领域诱饵查询。
func GenerateMedicalDecoy() string {
	return medicalDecoys[rand.IntN(len(medicalDecoys))]
}

// GenerateGeneralDecoy 随机生成一条通用领域诱饵查询。
func GenerateGeneralDecoy() string {
	return generalDecoys[rand.IntN(len(generalDecoys))]
}

// InjectDecoys 将真实查询与 n 条诱饵混合后随机置乱返回。
// 返回切片长度为 n+1，其中一条为真实查询，其余为诱饵。
// 调用方需自行记录真实查询的索引以识别有效响应。
func InjectDecoys(realQuery string, numDecoys int, domain string) ([]string, int) {
	queries := make([]string, 0, numDecoys+1)
	queries = append(queries, realQuery)

	for i := 0; i < numDecoys; i++ {
		var decoy string
		if domain == "medical" {
			decoy = GenerateMedicalDecoy()
		} else {
			decoy = GenerateGeneralDecoy()
		}
		queries = append(queries, decoy)
	}

	// Fisher-Yates 随机置乱
	realIdx := fisherYatesShuffle(queries)
	return queries, realIdx
}

// fisherYatesShuffle 对切片执行 Fisher-Yates 随机置乱，
// 返回原第一个元素（真实查询）的最终索引。
func fisherYatesShuffle(items []string) int {
	realIdx := 0
	n := len(items)
	for i := n - 1; i > 0; i-- {
		j := rand.IntN(i + 1)
		items[i], items[j] = items[j], items[i]
		if realIdx == i {
			realIdx = j
		} else if realIdx == j {
			realIdx = i
		}
	}
	return realIdx
}

// GenerateDecoySet 生成一组纯诱饵查询（不含真实查询）。
func GenerateDecoySet(count int, domain string) []string {
	decoys := make([]string, count)
	for i := 0; i < count; i++ {
		if domain == "medical" {
			decoys[i] = GenerateMedicalDecoy()
		} else {
			decoys[i] = GenerateGeneralDecoy()
		}
	}
	return decoys
}

// MedicalDecoyPool 返回医疗领域诱饵词库（用于跨语言对齐测试）。
func MedicalDecoyPool() []string {
	return medicalDecoys
}

// GeneralDecoyPool 返回通用领域诱饵词库（用于跨语言对齐测试）。
func GeneralDecoyPool() []string {
	return generalDecoys
}
