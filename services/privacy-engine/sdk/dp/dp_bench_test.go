package dp

import (
	"testing"
)

// ──────────────────────────────────────────────
// 基准测试：差分隐私原语
// ──────────────────────────────────────────────

func BenchmarkAddLaplaceNoise(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		AddLaplaceNoise(100.0, 1.0, 1.0)
	}
}

func BenchmarkAddGaussianNoise(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		AddGaussianNoise(100.0, 1.0, 1e-5, 1.0)
	}
}

func BenchmarkNoisyCount(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		NoisyCount(1000, 1.0)
	}
}

func BenchmarkNoisySum(b *testing.B) {
	values := make([]float64, 100)
	for i := range values {
		values[i] = float64(i)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		NoisySum(values, 1.0, 1.0)
	}
}

func BenchmarkNoisyMean(b *testing.B) {
	values := make([]float64, 100)
	for i := range values {
		values[i] = float64(i)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		NoisyMean(values, 1.0, 1e-5, 1.0)
	}
}

func BenchmarkNoisyCount_Batch1000(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 1000; j++ {
			NoisyCount(j, 0.1)
		}
	}
}
