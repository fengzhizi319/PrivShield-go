package dp

import (
	"math"
	"testing"
)

func TestAddLaplaceNoise(t *testing.T) {
	value := 100.0
	epsilon := 1.0
	sensitivity := 1.0

	// 多次采样验证噪声存在
	var results []float64
	for i := 0; i < 100; i++ {
		noisy := AddLaplaceNoise(value, epsilon, sensitivity)
		results = append(results, noisy)
	}

	// 验证噪声非零
	var sum float64
	for _, v := range results {
		sum += v
	}
	mean := sum / float64(len(results))
	if math.Abs(mean-value) > 5.0 {
		t.Errorf("Laplace noise mean deviation too large: %f", mean-value)
	}
}

func TestAddGaussianNoise(t *testing.T) {
	value := 100.0
	epsilon := 1.0
	delta := 1e-5
	sensitivity := 1.0

	// 多次采样验证噪声存在
	var results []float64
	for i := 0; i < 100; i++ {
		noisy := AddGaussianNoise(value, epsilon, delta, sensitivity)
		results = append(results, noisy)
	}

	// 验证噪声非零
	var sum float64
	for _, v := range results {
		sum += v
	}
	mean := sum / float64(len(results))
	if math.Abs(mean-value) > 5.0 {
		t.Errorf("Gaussian noise mean deviation too large: %f", mean-value)
	}
}

func TestClipValue(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		bound    float64
		expected float64
	}{
		{"within bound", 5.0, 10.0, 5.0},
		{"exceeds upper", 15.0, 10.0, 10.0},
		{"exceeds lower", -15.0, 10.0, -10.0},
		{"at boundary", 10.0, 10.0, 10.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClipValue(tt.value, tt.bound)
			if result != tt.expected {
				t.Errorf("ClipValue(%f, %f) = %f, want %f", tt.value, tt.bound, result, tt.expected)
			}
		})
	}
}

func TestClipL2Norm(t *testing.T) {
	vec := []float64{3.0, 4.0} // L2 norm = 5.0
	maxNorm := 3.0

	result := ClipL2Norm(vec, maxNorm)
	var sumSq float64
	for _, v := range result {
		sumSq += v * v
	}
	norm := math.Sqrt(sumSq)

	if math.Abs(norm-maxNorm) > 0.01 {
		t.Errorf("ClipL2Norm norm = %f, want %f", norm, maxNorm)
	}
}

func TestNoisyCount(t *testing.T) {
	count := 100
	epsilon := 1.0

	var results []float64
	for i := 0; i < 100; i++ {
		noisy := NoisyCount(count, epsilon)
		results = append(results, noisy)
	}

	// 验证均值接近真实值
	var sum float64
	for _, v := range results {
		sum += v
	}
	mean := sum / float64(len(results))
	if math.Abs(mean-float64(count)) > 5.0 {
		t.Errorf("NoisyCount mean = %f, want ~%d", mean, count)
	}
}

func TestNoisySum(t *testing.T) {
	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	epsilon := 1.0
	sensitivity := 5.0
	expectedSum := 15.0

	// 多次采样取均值，验证无偏性
	const runs = 500
	var sum float64
	for i := 0; i < runs; i++ {
		sum += NoisySum(values, epsilon, sensitivity)
	}
	avg := sum / float64(runs)

	// Laplace(scale=5.0) 的 std dev ≈ 7.07, SE ≈ 7.07/sqrt(500) ≈ 0.316
	if math.Abs(avg-expectedSum) > 2.0 {
		t.Errorf("NoisySum avg = %f, want ~%f", avg, expectedSum)
	}
}

func TestNoisyMean(t *testing.T) {
	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	epsilon := 1.0
	delta := 1e-5
	// clipBound 设为数据上界，使 sensitivity = clipBound/n = 1.0
	// 避免过大 sensitivity 导致噪声方差过高、测试不稳定
	clipBound := 5.0

	// 多次运行取平均，验证噪声均值无偏
	var sum float64
	runs := 500
	for i := 0; i < runs; i++ {
		sum += NoisyMean(values, epsilon, delta, clipBound)
	}
	avgResult := sum / float64(runs)
	expectedMean := 3.0

	// sensitivity=1.0 时 sigma≈2.74, SE≈0.12; 容差 1.5 ≈ 12σ 极安全
	if math.Abs(avgResult-expectedMean) > 1.5 {
		t.Errorf("NoisyMean avg = %f, want ~%f", avgResult, expectedMean)
	}
}

func TestAdaptiveClip(t *testing.T) {
	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0, 10.0}
	lower, upper := AdaptiveClip(values, 1.0, 0.9, 15, 10.0)
	if lower != 0.0 {
		t.Errorf("AdaptiveClip lower = %f, want 0.0", lower)
	}
	if upper <= 0.0 {
		t.Errorf("AdaptiveClip upper = %f, want > 0.0", upper)
	}

	// 空数据测试
	lowerEmpty, upperEmpty := AdaptiveClip(nil, 1.0, 0.9, 10, 5.0)
	if lowerEmpty != 0.0 || upperEmpty != 5.0 {
		t.Errorf("AdaptiveClip empty data = (%f, %f), want (0.0, 5.0)", lowerEmpty, upperEmpty)
	}
}

func TestGroupBy(t *testing.T) {
	rows := []map[string]string{
		{"dept": "Cardiology", "cost": "100.0"},
		{"dept": "Cardiology", "cost": "200.0"},
		{"dept": "Neurology", "cost": "300.0"},
		{"dept": "Neurology", "cost": "400.0"},
	}

	// Count
	resCount, err := GroupBy(rows, "dept", "cost", "count", 1.0, 0, 0, 500, "laplace")
	if err != nil {
		t.Fatalf("GroupBy count failed: %v", err)
	}
	if len(resCount) != 2 {
		t.Errorf("GroupBy count len = %d, want 2", len(resCount))
	}

	// Sum
	resSum, err := GroupBy(rows, "dept", "cost", "sum", 1.0, 0, 0, 500, "laplace")
	if err != nil {
		t.Fatalf("GroupBy sum failed: %v", err)
	}
	if len(resSum) != 2 {
		t.Errorf("GroupBy sum len = %d, want 2", len(resSum))
	}

	// Mean
	resMean, err := GroupBy(rows, "dept", "cost", "mean", 1.0, 1e-5, 0, 500, "gaussian")
	if err != nil {
		t.Fatalf("GroupBy mean failed: %v", err)
	}
	if len(resMean) != 2 {
		t.Errorf("GroupBy mean len = %d, want 2", len(resMean))
	}
}

func TestAggregate(t *testing.T) {
	rows := []map[string]string{
		{"age": "25", "salary": "5000"},
		{"age": "35", "salary": "8000"},
		{"age": "45", "salary": "12000"},
	}
	specs := map[string]string{
		"age":    "mean",
		"salary": "sum",
	}

	res, err := Aggregate(rows, specs, 1.0, 1e-5, 0, 100.0, "laplace")
	if err != nil {
		t.Fatalf("Aggregate failed: %v", err)
	}
	if len(res) != 2 {
		t.Errorf("Aggregate result len = %d, want 2", len(res))
	}
	if _, ok := res["age_mean"]; !ok {
		t.Errorf("Aggregate missing age_mean")
	}
	if _, ok := res["salary_sum"]; !ok {
		t.Errorf("Aggregate missing salary_sum")
	}
}
