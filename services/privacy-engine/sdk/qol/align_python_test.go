// Package qol_test 跨语言对齐测试：验证 Go QOL 输出与 Python 基准一致。
//
// 对齐目标：Python engine/privacy/qol.py 的诱饵词库与注入逻辑。
package qol_test

import (
	"sort"
	"testing"

	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/qol"
)

// ──────────────────────────────────────────────
// 诱饵词库对齐验证
// ──────────────────────────────────────────────

func TestAlignPython_MedicalDecoyPool(t *testing.T) {
	// 验证 Go 医疗诱饵词库与 Python MEDICAL_DUMMY 完全一致
	pool := qol.MedicalDecoyPool()
	if len(pool) != 20 {
		t.Errorf("MedicalDecoyPool length = %d, want 20 (same as Python MEDICAL_DUMMY)", len(pool))
	}
	// 验证关键条目与 Python 一致
	expectedMedical := []string{
		"高血压患者的日常饮食建议",
		"流感疫苗接种人群建议",
		"骨质疏松患者补钙与运动方案",
		"甲状腺结节患者饮食禁忌",
	}
	poolSet := make(map[string]bool, len(pool))
	for _, d := range pool {
		poolSet[d] = true
	}
	for _, expected := range expectedMedical {
		if !poolSet[expected] {
			t.Errorf("MedicalDecoyPool missing Python baseline entry: %q", expected)
		}
	}
}

func TestAlignPython_GeneralDecoyPool(t *testing.T) {
	// 验证 Go 通用诱饵词库与 Python GENERIC_DUMMY 完全一致
	pool := qol.GeneralDecoyPool()
	if len(pool) != 20 {
		t.Errorf("GeneralDecoyPool length = %d, want 20 (same as Python GENERIC_DUMMY)", len(pool))
	}
	// 验证关键条目与 Python 一致
	expectedGeneral := []string{
		"天气预报查询",
		"附近医院挂号流程",
		"体检报告解读指南",
		"数字证书在线更新流程",
	}
	poolSet := make(map[string]bool, len(pool))
	for _, d := range pool {
		poolSet[d] = true
	}
	for _, expected := range expectedGeneral {
		if !poolSet[expected] {
			t.Errorf("GeneralDecoyPool missing Python baseline entry: %q", expected)
		}
	}
}

// ──────────────────────────────────────────────
// InjectDecoys 结构验证
// ──────────────────────────────────────────────

func TestAlignPython_InjectDecoysStructure(t *testing.T) {
	// Python: obfuscate_query("SELECT * FROM patients", num_dummies=3, domain="medical")
	// 返回列表长度 = num_dummies + 1 = 4，真实查询在其中某处
	queries, realIdx := qol.InjectDecoys("SELECT * FROM patients", 3, "medical")

	// 总长度验证
	if len(queries) != 4 {
		t.Errorf("InjectDecoys returned %d queries, want 4", len(queries))
	}
	// 真实查询索引范围验证
	if realIdx < 0 || realIdx >= len(queries) {
		t.Errorf("realIdx = %d, out of range [0, %d)", realIdx, len(queries))
	}
	// 真实查询内容验证
	if queries[realIdx] != "SELECT * FROM patients" {
		t.Errorf("queries[%d] = %q, want %q", realIdx, queries[realIdx], "SELECT * FROM patients")
	}
	// 所有诱饵来自医疗词库验证
	medicalPool := qol.MedicalDecoyPool()
	poolSet := make(map[string]bool, len(medicalPool))
	for _, d := range medicalPool {
		poolSet[d] = true
	}
	for i, q := range queries {
		if i == realIdx {
			continue
		}
		if !poolSet[q] {
			t.Errorf("decoy queries[%d] = %q not in medical pool", i, q)
		}
	}
}

// ──────────────────────────────────────────────
// 诱饵词库排序一致性（验证跨语言内容完全匹配）
// ──────────────────────────────────────────────

func TestAlignPython_DecoyPoolSortedMatch(t *testing.T) {
	// Python MEDICAL_DUMMY 排序后与 Go 排序后应完全一致
	pythonMedicalSorted := []string{
		"过敏性鼻炎的日常防治手段",
		"过敏性皮炎日常注意事项",
		"骨质疏松患者补钙与运动方案",
		"骨质疏松防摔倒安全提示",
		"冠心病的早期症状有哪些",
		"带状疱疹的临床表现及治疗",
		"长期失眠的危害及改善建议",
		"流感疫苗接种人群建议",
		"慢性支气管炎的预防措施",
		"甲状腺结节患者饮食禁忌",
		"颈椎病康复训练操指南",
		"偏头痛的诱发因素与缓解方式",
		"儿童常见过敏反应处理",
		"胃溃疡患者吃什么食物好",
		"痛风患者避免食用的食物清单",
		"糖尿病患者运动注意事项",
		"脑梗塞前兆表现及预防建议",
		"哮喘发作时的紧急处理方法",
		"脂肪肝患者运动处方",
		"高血压患者的日常饮食建议",
	}
	goMedical := qol.MedicalDecoyPool()
	goMedicalSorted := make([]string, len(goMedical))
	copy(goMedicalSorted, goMedical)
	sort.Strings(goMedicalSorted)
	sort.Strings(pythonMedicalSorted)

	if len(goMedicalSorted) != len(pythonMedicalSorted) {
		t.Fatalf("pool size mismatch: Go=%d, Python=%d", len(goMedicalSorted), len(pythonMedicalSorted))
	}
	for i := range goMedicalSorted {
		if goMedicalSorted[i] != pythonMedicalSorted[i] {
			t.Errorf("sorted mismatch at [%d]: Go=%q, Python=%q", i, goMedicalSorted[i], pythonMedicalSorted[i])
		}
	}
}
