package ldp

import (
	"math"
	"testing"
)

func TestRandomizedResponse(t *testing.T) {
	// 高 epsilon 应保留大部分真实值
	trueCount := 0
	n := 1000
	for i := 0; i < n; i++ {
		if RandomizedResponse(true, 5.0) {
			trueCount++
		}
	}
	// epsilon=5.0 时 p ≈ 0.993，true 计数应 > 900
	if trueCount < 900 {
		t.Errorf("RandomizedResponse(true, 5.0) true count = %d, want > 900", trueCount)
	}
}

func TestEstimateTrueCount(t *testing.T) {
	// 生成 1000 个 true 响应
	responses := make([]bool, 1000)
	for i := range responses {
		responses[i] = true
	}

	epsilon := 3.0
	estimated := EstimateTrueCount(responses, epsilon)
	// 估计值应在合理范围内（800~1200）
	if estimated < 800 || estimated > 1200 {
		t.Errorf("EstimateTrueCount = %d, want 800~1200", estimated)
	}
}

func TestORRResponse(t *testing.T) {
	// 高 epsilon 应保留真实值
	correct := 0
	n := 1000
	for i := 0; i < n; i++ {
		if ORRResponse(2, 10.0, 5) == 2 {
			correct++
		}
	}
	if correct < 950 {
		t.Errorf("ORRResponse(2, 10.0, 5) correct count = %d, want > 950", correct)
	}
}

func TestEstimateFrequency(t *testing.T) {
	// 生成类别 0 的响应
	responses := make([]int, 500)
	for i := range responses {
		responses[i] = 0
	}

	epsilon := 3.0
	estimated := EstimateFrequency(responses, epsilon, 5)
	// 类别 0 的估计应接近 500（放宽范围，LDP 噪声较大）
	if estimated[0] < 350 || estimated[0] > 650 {
		t.Errorf("EstimateFrequency[0] = %d, want 350~650", estimated[0])
	}
}

func TestNumericLDP(t *testing.T) {
	value := 50.0
	lower := 0.0
	upper := 100.0
	epsilon := 1.0

	var results []float64
	for i := 0; i < 100; i++ {
		noisy := NumericLDP(value, lower, upper, epsilon)
		results = append(results, noisy)
		// 验证结果在区间内
		if noisy < lower || noisy > upper {
			t.Errorf("NumericLDP = %f, out of range [%f, %f]", noisy, lower, upper)
		}
	}

	// 验证均值接近真实值
	var sum float64
	for _, v := range results {
		sum += v
	}
	mean := sum / float64(len(results))
	if math.Abs(mean-value) > 20.0 {
		t.Errorf("NumericLDP mean = %f, want ~%f", mean, value)
	}
}

func TestEstimateTrueCountEmpty(t *testing.T) {
	estimated := EstimateTrueCount(nil, 1.0)
	if estimated != 0 {
		t.Errorf("EstimateTrueCount(nil) = %d, want 0", estimated)
	}
}

func TestEstimateFrequencyEmpty(t *testing.T) {
	estimated := EstimateFrequency(nil, 1.0, 5)
	if len(estimated) != 5 {
		t.Errorf("EstimateFrequency(nil) length = %d, want 5", len(estimated))
	}
}
