// align_python_test.go — LDP 跨语言对齐测试（与 Python LocalDPApi 对齐）
package ldp

import (
	"math"
	"testing"
)

// ──────────────────────────────────────────────
// EstimateBinaryFrequency 对齐测试（与 Python estimate_binary_frequency 对齐）
// ──────────────────────────────────────────────

func TestAlignPython_EstimateBinaryFrequency_AllOnes(t *testing.T) {
	// Python: estimate_binary_frequency([1, 1, 1, 1, 1], 5.0) → ~0.97
	// 高 epsilon 下，全 1 上报 → 估计频率接近 1
	result := EstimateBinaryFrequency([]int{1, 1, 1, 1, 1}, 5.0)
	if result < 0.9 || result > 1.0 {
		t.Errorf("EstimateBinaryFrequency(all 1s, eps=5.0) = %f, want ~0.97", result)
	}
}

func TestAlignPython_EstimateBinaryFrequency_AllZeros(t *testing.T) {
	// Python: estimate_binary_frequency([0, 0, 0, 0, 0], 5.0) → ~0.03
	// 高 epsilon 下，全 0 上报 → 估计频率接近 0
	result := EstimateBinaryFrequency([]int{0, 0, 0, 0, 0}, 5.0)
	if result < 0.0 || result > 0.1 {
		t.Errorf("EstimateBinaryFrequency(all 0s, eps=5.0) = %f, want ~0.03", result)
	}
}

func TestAlignPython_EstimateBinaryFrequency_Empty(t *testing.T) {
	// Python: estimate_binary_frequency([], 1.0) → 0.0
	result := EstimateBinaryFrequency([]int{}, 1.0)
	if result != 0.0 {
		t.Errorf("EstimateBinaryFrequency(empty) = %f, want 0.0", result)
	}
}

func TestAlignPython_EstimateBinaryFrequency_ClampedToUnit(t *testing.T) {
	// 极端情况下结果应截断到 [0, 1]
	result := EstimateBinaryFrequency([]int{1, 1, 1}, 0.01)
	if result < 0.0 || result > 1.0 {
		t.Errorf("EstimateBinaryFrequency should be clamped to [0,1], got %f", result)
	}
}

func TestAlignPython_EstimateBinaryFrequency_HalfHalf(t *testing.T) {
	// 50/50 上报，高 epsilon → 估计接近 0.5
	result := EstimateBinaryFrequency([]int{1, 1, 0, 0}, 10.0)
	if math.Abs(result-0.5) > 0.1 {
		t.Errorf("EstimateBinaryFrequency(50/50, eps=10.0) = %f, want ~0.5", result)
	}
}

// ──────────────────────────────────────────────
// EstimateCategoricalHistogram 对齐测试（与 Python estimate_categorical_histogram 对齐）
// ──────────────────────────────────────────────

func TestAlignPython_EstimateCategoricalHistogram_Uniform(t *testing.T) {
	// 均匀上报 → 估计应接近均匀分布
	categories := []string{"A", "B", "C"}
	reported := []string{"A", "B", "C", "A", "B", "C"}
	result := EstimateCategoricalHistogram(reported, categories, 10.0)
	// 高 epsilon 下，均匀上报 → 各类别估计接近 1/3
	for _, c := range categories {
		if math.Abs(result[c]-1.0/3.0) > 0.15 {
			t.Errorf("EstimateCategoricalHistogram[%s] = %f, want ~0.33", c, result[c])
		}
	}
}

func TestAlignPython_EstimateCategoricalHistogram_SumToOne(t *testing.T) {
	// 所有类别频率之和应为 1.0
	categories := []string{"X", "Y", "Z"}
	reported := []string{"X", "X", "X", "Y", "Z"}
	result := EstimateCategoricalHistogram(reported, categories, 3.0)
	total := 0.0
	for _, c := range categories {
		total += result[c]
	}
	if math.Abs(total-1.0) > 0.001 {
		t.Errorf("histogram sum = %f, want 1.0", total)
	}
}

func TestAlignPython_EstimateCategoricalHistogram_Empty(t *testing.T) {
	// 空输入 → 均匀分布
	categories := []string{"A", "B"}
	result := EstimateCategoricalHistogram([]string{}, categories, 1.0)
	for _, c := range categories {
		if math.Abs(result[c]-0.5) > 0.001 {
			t.Errorf("EstimateCategoricalHistogram(empty)[%s] = %f, want 0.5", c, result[c])
		}
	}
}

func TestAlignPython_EstimateCategoricalHistogram_NonNegative(t *testing.T) {
	// 所有估计应非负
	categories := []string{"A", "B", "C", "D"}
	reported := []string{"A", "A", "A", "A", "B"}
	result := EstimateCategoricalHistogram(reported, categories, 1.0)
	for _, c := range categories {
		if result[c] < 0 {
			t.Errorf("EstimateCategoricalHistogram[%s] = %f, should be non-negative", c, result[c])
		}
	}
}

// ──────────────────────────────────────────────
// PerturbBinaryBatch 对齐测试
// ──────────────────────────────────────────────

func TestAlignPython_PerturbBinaryBatch_Length(t *testing.T) {
	// 输出长度应与输入一致
	values := []int{0, 1, 0, 1, 1}
	result := PerturbBinaryBatch(values, 1.0)
	if len(result) != len(values) {
		t.Errorf("PerturbBinaryBatch length = %d, want %d", len(result), len(values))
	}
}

func TestAlignPython_PerturbBinaryBatch_ValuesBinary(t *testing.T) {
	// 输出应全为 0 或 1
	values := []int{0, 1, 0, 1, 1, 0}
	result := PerturbBinaryBatch(values, 1.0)
	for i, v := range result {
		if v != 0 && v != 1 {
			t.Errorf("PerturbBinaryBatch[%d] = %d, should be 0 or 1", i, v)
		}
	}
}

func TestAlignPython_PerturbBinaryBatch_HighEpsilon(t *testing.T) {
	// 高 epsilon → 大部分值保持不变
	values := make([]int, 1000)
	for i := range values {
		values[i] = i % 2
	}
	result := PerturbBinaryBatch(values, 20.0)
	match := 0
	for i := range values {
		if result[i] == values[i] {
			match++
		}
	}
	// 高 epsilon 下，保持率应 > 95%
	if float64(match)/float64(len(values)) < 0.95 {
		t.Errorf("PerturbBinaryBatch high epsilon: match rate = %d/1000, want > 950", match)
	}
}

// ──────────────────────────────────────────────
// PerturbCategoricalBatch 对齐测试
// ──────────────────────────────────────────────

func TestAlignPython_PerturbCategoricalBatch_Length(t *testing.T) {
	values := []string{"A", "B", "C", "A"}
	categories := []string{"A", "B", "C"}
	result := PerturbCategoricalBatch(values, categories, 1.0)
	if len(result) != len(values) {
		t.Errorf("PerturbCategoricalBatch length = %d, want %d", len(result), len(values))
	}
}

func TestAlignPython_PerturbCategoricalBatch_ValidCategories(t *testing.T) {
	values := []string{"A", "B", "C", "A", "B"}
	categories := []string{"A", "B", "C"}
	result := PerturbCategoricalBatch(values, categories, 1.0)
	validSet := map[string]bool{"A": true, "B": true, "C": true}
	for i, v := range result {
		if !validSet[v] {
			t.Errorf("PerturbCategoricalBatch[%d] = %q, should be in categories", i, v)
		}
	}
}

func TestAlignPython_PerturbCategoricalBatch_HighEpsilon(t *testing.T) {
	// 高 epsilon → 大部分值保持不变
	values := []string{"A", "B", "C", "A", "B", "C", "A", "B", "C", "A"}
	categories := []string{"A", "B", "C"}
	result := PerturbCategoricalBatch(values, categories, 20.0)
	match := 0
	for i := range values {
		if result[i] == values[i] {
			match++
		}
	}
	if float64(match)/float64(len(values)) < 0.8 {
		t.Errorf("PerturbCategoricalBatch high epsilon: match rate = %d/%d, want > 80%%", match, len(values))
	}
}

// ──────────────────────────────────────────────
// 基准测试
// ──────────────────────────────────────────────

func BenchmarkEstimateBinaryFrequency(b *testing.B) {
	values := make([]int, 10000)
	for i := range values {
		values[i] = i % 2
	}
	for i := 0; i < b.N; i++ {
		EstimateBinaryFrequency(values, 1.0)
	}
}

func BenchmarkEstimateCategoricalHistogram(b *testing.B) {
	categories := []string{"A", "B", "C", "D", "E"}
	values := make([]string, 10000)
	for i := range values {
		values[i] = categories[i%5]
	}
	for i := 0; i < b.N; i++ {
		EstimateCategoricalHistogram(values, categories, 1.0)
	}
}
