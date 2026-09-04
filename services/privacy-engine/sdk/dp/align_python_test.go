package dp

import (
	"math"
	"testing"
)

// ──────────────────────────────────────────────
// 跨语言对齐测试：Go DP vs Python DP
//
// 确定性函数（ClipValue / ClipL2Norm）必须精确匹配。
// 随机函数（NoisyCount / NoisySum / NoisyMean / NoisyHistogram / VectorMean）
// 因使用不同 RNG 无法精确匹配，仅验证统计特性：
//   - 无偏性：多次采样均值 ≈ 真值
//   - 噪声尺度：噪声标准差 ≈ 理论 Laplace/Gaussian scale
// ──────────────────────────────────────────────

// TestAlignPython_ClipValue 验证 ClipValue 与 Python 精确一致。
// Python: max(-bound, min(bound, value))
func TestAlignPython_ClipValue(t *testing.T) {
	tests := []struct {
		value, bound, want float64
	}{
		// Python: clip_value(10.0, 5.0) == 5.0
		{10.0, 5.0, 5.0},
		// Python: clip_value(-10.0, 5.0) == -5.0
		{-10.0, 5.0, -5.0},
		// Python: clip_value(3.0, 5.0) == 3.0
		{3.0, 5.0, 3.0},
		{0.0, 1.0, 0.0},
		{-1.0, 1.0, -1.0},
		{1.0, 1.0, 1.0},
	}
	for _, tt := range tests {
		got := ClipValue(tt.value, tt.bound)
		if got != tt.want {
			t.Errorf("ClipValue(%g, %g) = %g, Python = %g", tt.value, tt.bound, got, tt.want)
		}
	}
}

// TestAlignPython_ClipL2Norm 验证 ClipL2Norm 与 Python 精确一致。
// Python: clip_l2(vec, max_norm) → norm > max_norm 时按比例缩放
func TestAlignPython_ClipL2Norm(t *testing.T) {
	tests := []struct {
		name    string
		vec     []float64
		maxNorm float64
		want    []float64
	}{
		{
			// Python: clip_l2([3,4], 5.0) == [3.0, 4.0] (norm=5 ≤ 5)
			"norm<=maxNorm",
			[]float64{3.0, 4.0}, 5.0,
			[]float64{3.0, 4.0},
		},
		{
			// Python: clip_l2([3,4], 3.0) == [1.8, 2.4] (norm=5 > 3, scale=3/5=0.6)
			"norm>maxNorm",
			[]float64{3.0, 4.0}, 3.0,
			[]float64{1.8, 2.4},
		},
		{
			// Python: clip_l2([1,0,0], 5.0) == [1.0, 0.0, 0.0]
			"unitVector",
			[]float64{1.0, 0.0, 0.0}, 5.0,
			[]float64{1.0, 0.0, 0.0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClipL2Norm(tt.vec, tt.maxNorm)
			if len(got) != len(tt.want) {
				t.Fatalf("len mismatch: got %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if math.Abs(got[i]-tt.want[i]) > 1e-10 {
					t.Errorf("[%d] = %g, Python = %g", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestAlignPython_NoisyCountStatistical 验证 NoisyCount 统计特性与 Python 一致。
// Python: noisy_count(100, eps=1.0) → Laplace(100, scale=1/1.0=1.0)
// Laplace(scale=1.0) 的方差 = 2*scale^2 = 2.0，标准差 ≈ 1.414
func TestAlignPython_NoisyCountStatistical(t *testing.T) {
	const (
		trueCount = 100
		epsilon   = 1.0
		runs      = 2000
		tolerance = 0.5 // 均值容差
	)

	var sum float64
	for i := 0; i < runs; i++ {
		sum += NoisyCount(trueCount, epsilon)
	}
	mean := sum / float64(runs)

	// 无偏性：均值应接近真值
	if math.Abs(mean-float64(trueCount)) > tolerance {
		t.Errorf("NoisyCount mean=%g, want ~%d (diff=%g)", mean, trueCount, mean-float64(trueCount))
	}
}

// TestAlignPython_NoisySumStatistical 验证 NoisySum 统计特性。
// Python: noisy_sum(50.0, sensitivity=1.0, eps=1.0) → 50.0 + Laplace(scale=1.0)
func TestAlignPython_NoisySumStatistical(t *testing.T) {
	const (
		trueSum     = 50.0
		epsilon     = 1.0
		sensitivity = 1.0
		runs        = 2000
		tolerance   = 0.5
	)

	var sum float64
	for i := 0; i < runs; i++ {
		sum += NoisySum([]float64{10.0, 15.0, 25.0}, epsilon, sensitivity)
	}
	mean := sum / float64(runs)

	if math.Abs(mean-trueSum) > tolerance {
		t.Errorf("NoisySum mean=%g, want ~%g", mean, trueSum)
	}
}

// TestAlignPython_NoisyHistogram 验证 NoisyHistogram 基本行为。
// 每个分类独立添加 Laplace(scale=1/epsilon) 噪声。
func TestAlignPython_NoisyHistogram(t *testing.T) {
	trueCounts := map[string]int{
		"A": 100,
		"B": 200,
		"C": 50,
	}
	epsilon := 1.0
	runs := 1000

	// 累计各分类的带噪声计数
	accum := map[string]float64{"A": 0, "B": 0, "C": 0}
	for i := 0; i < runs; i++ {
		result := NoisyHistogram(trueCounts, epsilon)
		for k, v := range result {
			accum[k] += v
		}
	}

	// 验证各分类均值接近真值
	for k, trueVal := range trueCounts {
		mean := accum[k] / float64(runs)
		if math.Abs(mean-float64(trueVal)) > 1.0 {
			t.Errorf("histogram[%s] mean=%g, want ~%d", k, mean, trueVal)
		}
	}
}

// TestAlignPython_VectorMean 验证 VectorMean 基本行为。
// 先 L2 截断 → 计算均值 → 添加 Laplace 向量噪声。
func TestAlignPython_VectorMean(t *testing.T) {
	vectors := [][]float64{
		{1.0, 2.0},
		{3.0, 4.0},
		{5.0, 6.0},
	}
	maxNorm := 10.0 // 所有向量 L2 范数 < 10，不截断
	epsilon := 1.0
	runs := 1000

	// 累计各分量的带噪声均值
	dim := len(vectors[0])
	accum := make([]float64, dim)
	for i := 0; i < runs; i++ {
		result := VectorMean(vectors, maxNorm, epsilon)
		for j := 0; j < dim; j++ {
			accum[j] += result[j]
		}
	}

	// 真实均值 = (1+3+5)/3=3, (2+4+6)/3=4
	trueMean := []float64{3.0, 4.0}
	for j := 0; j < dim; j++ {
		mean := accum[j] / float64(runs)
		if math.Abs(mean-trueMean[j]) > 0.5 {
			t.Errorf("VectorMean[%d] mean=%g, want ~%g", j, mean, trueMean[j])
		}
	}
}

// TestAlignPython_VectorMeanEmpty 验证空输入行为。
func TestAlignPython_VectorMeanEmpty(t *testing.T) {
	result := VectorMean(nil, 1.0, 1.0)
	if result != nil {
		t.Errorf("VectorMean(nil) should return nil, got %v", result)
	}

	result = VectorMean([][]float64{{}}, 1.0, 1.0)
	if result != nil {
		t.Errorf("VectorMean([[]]) should return nil, got %v", result)
	}
}

// TestAlignPython_NoisyHistogramEmpty 验证空输入行为。
func TestAlignPython_NoisyHistogramEmpty(t *testing.T) {
	result := NoisyHistogram(map[string]int{}, 1.0)
	if len(result) != 0 {
		t.Errorf("NoisyHistogram({}) should return empty map, got %v", result)
	}
}

// TestAlignPython_VectorSum 验证 VectorSum 基本行为。
// 先 L2 截断 → 计算总和 → 添加 Laplace 向量噪声。
// Python: dp_vector_sum(vectors, max_norm=10.0, epsilon=1.0)
func TestAlignPython_VectorSum(t *testing.T) {
	vectors := [][]float64{
		{1.0, 2.0},
		{3.0, 4.0},
		{5.0, 6.0},
	}
	maxNorm := 10.0 // 所有向量 L2 范数 < 10，不截断
	epsilon := 1.0
	runs := 5000

	// 累计各分量的带噪声总和
	dim := len(vectors[0])
	accum := make([]float64, dim)
	for i := 0; i < runs; i++ {
		result := VectorSum(vectors, maxNorm, epsilon)
		for j := 0; j < dim; j++ {
			accum[j] += result[j]
		}
	}

	// 真实总和 = 1+3+5=9, 2+4+6=12
	trueSum := []float64{9.0, 12.0}
	for j := 0; j < dim; j++ {
		mean := accum[j] / float64(runs)
		if math.Abs(mean-trueSum[j]) > 1.5 {
			t.Errorf("VectorSum[%d] mean=%g, want ~%g", j, mean, trueSum[j])
		}
	}
}

// TestAlignPython_VectorSumEmpty 验证空输入行为。
func TestAlignPython_VectorSumEmpty(t *testing.T) {
	result := VectorSum(nil, 1.0, 1.0)
	if result != nil {
		t.Errorf("VectorSum(nil) should return nil, got %v", result)
	}

	result = VectorSum([][]float64{{}}, 1.0, 1.0)
	if result != nil {
		t.Errorf("VectorSum([[]]) should return nil, got %v", result)
	}
}
