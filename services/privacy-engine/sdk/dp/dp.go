// Package dp 提供纯标量浮点差分隐私原语。
//
// 实现 Laplace / Gaussian 机制、自适应梯度截断与向量加噪，
// 所有函数均为零状态纯计算，适合高并发场景。
package dp

import (
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"strings"
	"sync"
)

// ──────────────────────────────────────────────
// Laplace 机制（ε-DP，适用于 Count / Sum）
// ──────────────────────────────────────────────

// AddLaplaceNoise 为数值添加 Laplace 噪声。
// scale = sensitivity / epsilon，满足 ε-差分隐私。
// epsilon <= 0 时直接返回原值（无隐私保护）。
func AddLaplaceNoise(value, epsilon, sensitivity float64) float64 {
	if epsilon <= 0 || sensitivity <= 0 {
		return value
	}
	scale := sensitivity / epsilon
	u := rand.Float64() - 0.5
	sgn := 1.0
	if u < 0 {
		sgn = -1.0
	}
	noise := -scale * sgn * math.Log(1.0-2.0*math.Abs(u))
	return value + noise
}

// ──────────────────────────────────────────────
// Gaussian 机制（(ε,δ)-DP，适用于 Mean / Sum）
// ──────────────────────────────────────────────

// AddGaussianNoise 为数值添加 Gaussian 噪声。
// sigma = sqrt(2 * ln(1.25/delta)) * sensitivity / epsilon，
// 满足 (ε,δ)-差分隐私。
func AddGaussianNoise(value, epsilon, delta, sensitivity float64) float64 {
	if epsilon <= 0 || delta <= 0 || sensitivity <= 0 {
		return value
	}
	sigma := math.Sqrt(2.0*math.Log(1.25/delta)) * sensitivity / epsilon
	return value + boxMullerNormal()*sigma
}

// boxMullerNormal 使用 Box-Muller 变换生成标准正态分布随机数。
func boxMullerNormal() float64 {
	u1 := rand.Float64()
	u2 := rand.Float64()
	// 避免 log(0)
	for u1 == 0 {
		u1 = rand.Float64()
	}
	return math.Sqrt(-2.0*math.Log(u1)) * math.Cos(2.0*math.Pi*u2)
}

// ──────────────────────────────────────────────
// 自适应梯度截断（Clipping）
// ──────────────────────────────────────────────

// ClipValue 将数值截断至 [-bound, +bound] 区间。
func ClipValue(value, bound float64) float64 {
	if value > bound {
		return bound
	}
	if value < -bound {
		return -bound
	}
	return value
}

// ClipValueRange 将数值截断至 [lower, upper] 区间。
// 用于差分隐私聚合的有界敏感度截断：值域 [lower, upper] 确保
// sum 敏感度 = upper - lower 有界，满足 ε-DP 保证。
func ClipValueRange(value, lower, upper float64) float64 {
	if value > upper {
		return upper
	}
	if value < lower {
		return lower
	}
	return value
}

// ClipL2Norm 将向量截断至 L2 范数不超过 maxNorm。
// 返回截断后的新切片（不修改原切片）。
func ClipL2Norm(vec []float64, maxNorm float64) []float64 {
	if maxNorm <= 0 || len(vec) == 0 {
		return vec
	}
	var sumSq float64
	for _, v := range vec {
		sumSq += v * v
	}
	norm := math.Sqrt(sumSq)
	if norm <= maxNorm {
		result := make([]float64, len(vec))
		copy(result, vec)
		return result
	}
	scale := maxNorm / norm
	result := make([]float64, len(vec))
	for i, v := range vec {
		result[i] = v * scale
	}
	return result
}

// ──────────────────────────────────────────────
// 向量加噪
// ──────────────────────────────────────────────

// AddLaplaceVector 为向量每个分量独立添加 Laplace 噪声。
func AddLaplaceVector(vec []float64, epsilon, sensitivity float64) []float64 {
	result := make([]float64, len(vec))
	for i, v := range vec {
		result[i] = AddLaplaceNoise(v, epsilon, sensitivity)
	}
	return result
}

// AddGaussianVector 为向量每个分量独立添加 Gaussian 噪声。
func AddGaussianVector(vec []float64, epsilon, delta, sensitivity float64) []float64 {
	result := make([]float64, len(vec))
	for i, v := range vec {
		result[i] = AddGaussianNoise(v, epsilon, delta, sensitivity)
	}
	return result
}

// ──────────────────────────────────────────────
// 统计聚合 + 加噪
// ──────────────────────────────────────────────

// NoisyCount 计算计数并添加 Laplace 噪声（敏感度 = 1）。
func NoisyCount(count int, epsilon float64) float64 {
	return AddLaplaceNoise(float64(count), epsilon, 1.0)
}

// NoisySum 计算总和并添加 Laplace 噪声。
func NoisySum(values []float64, epsilon, sensitivity float64) float64 {
	var sum float64
	for _, v := range values {
		sum += v
	}
	return AddLaplaceNoise(sum, epsilon, sensitivity)
}

// NoisyMean 计算均值并添加 Gaussian 噪声。
// 先对每个值截断至 [-clipBound, +clipBound]，再计算均值并加噪。
func NoisyMean(values []float64, epsilon, delta, clipBound float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += ClipValue(v, clipBound)
	}
	mean := sum / float64(len(values))
	// 均值的敏感度 = clipBound / n
	sensitivity := clipBound / float64(len(values))
	return AddGaussianNoise(mean, epsilon, delta, sensitivity)
}

// ──────────────────────────────────────────────
// 直方图与向量均值（对齐 Python DPApi）
// ──────────────────────────────────────────────

// NoisyHistogram 计算带噪声的分类直方图。
// trueCounts 为各分类的真实计数（map[分类]计数），
// epsilon 为隐私预算。返回各分类的带噪声计数。
// 每个分类独立添加 Laplace 噪声（敏感度 = 1）。
func NoisyHistogram(trueCounts map[string]int, epsilon float64) map[string]float64 {
	result := make(map[string]float64, len(trueCounts))
	for k, v := range trueCounts {
		result[k] = NoisyCount(v, epsilon)
	}
	return result
}

// VectorMean 计算向量均值并添加 Laplace 向量噪声。
// 单趟流式计算：在线截断 L2 范数并融合累加，消除中间切片分配。
func VectorMean(vectors [][]float64, maxNorm float64, epsilon float64) []float64 {
	n := len(vectors)
	if n == 0 {
		return nil
	}
	dim := len(vectors[0])
	if dim == 0 {
		return nil
	}

	sum := make([]float64, dim)
	for _, v := range vectors {
		var normSq float64
		vLen := len(v)
		for j := 0; j < dim && j < vLen; j++ {
			normSq += v[j] * v[j]
		}
		norm := math.Sqrt(normSq)
		scale := 1.0
		if maxNorm > 0 && norm > maxNorm {
			scale = maxNorm / norm
		}
		for j := 0; j < dim && j < vLen; j++ {
			sum[j] += v[j] * scale
		}
	}

	num := float64(n)
	mean := make([]float64, dim)
	for j := 0; j < dim; j++ {
		mean[j] = sum[j] / num
	}

	sensitivity := maxNorm / num
	return AddLaplaceVector(mean, epsilon, sensitivity)
}

// VectorSum 对向量集合执行差分隐私求和。
// 单趟流式计算：在线截断 L2 范数并融合累加，消除中间切片分配。
// 与 Python dp_vector_sum 对齐。
func VectorSum(vectors [][]float64, maxNorm float64, epsilon float64) []float64 {
	if len(vectors) == 0 {
		return nil
	}
	dim := len(vectors[0])
	if dim == 0 {
		return nil
	}

	sum := make([]float64, dim)
	for _, v := range vectors {
		var normSq float64
		vLen := len(v)
		for j := 0; j < dim && j < vLen; j++ {
			normSq += v[j] * v[j]
		}
		norm := math.Sqrt(normSq)
		scale := 1.0
		if maxNorm > 0 && norm > maxNorm {
			scale = maxNorm / norm
		}
		for j := 0; j < dim && j < vLen; j++ {
			sum[j] += v[j] * scale
		}
	}

	return AddLaplaceVector(sum, epsilon, maxNorm)
}

// ──────────────────────────────────────────────
// DP 自适应截断、分组聚合与多指标聚合
// ──────────────────────────────────────────────

// AdaptiveClip 差分隐私自适应二分搜索估计 [0.0, clipUpper] 上下界（支持多核并发加速）。
// 通过 DP 分位数估计自适应确定安全截断范围。
func AdaptiveClip(values []float64, epsilon float64, targetQuantile float64, numIterations int, initialClip float64) (float64, float64) {
	if numIterations <= 0 {
		numIterations = 15
	}
	if targetQuantile <= 0 || targetQuantile >= 1 {
		targetQuantile = 0.95
	}
	if initialClip <= 0 {
		initialClip = 10.0
	}
	n := len(values)
	if n == 0 {
		return 0.0, initialClip
	}

	epsPerIter := epsilon / float64(numIterations)
	curClip := initialClip
	totalCount := float64(n)

	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers > 16 {
		numWorkers = 16
	}
	if numWorkers > n {
		numWorkers = n
	}
	chunkSize := (n + numWorkers - 1) / numWorkers

	for i := 0; i < numIterations; i++ {
		belowCount := 0
		if n <= 10000 {
			for _, v := range values {
				if v <= curClip {
					belowCount++
				}
			}
		} else {
			localCounts := make([]int, numWorkers)
			var wg sync.WaitGroup
			for w := 0; w < numWorkers; w++ {
				startIdx := w * chunkSize
				endIdx := startIdx + chunkSize
				if endIdx > n {
					endIdx = n
				}
				if startIdx >= endIdx {
					break
				}
				wg.Add(1)
				go func(workerID, start, end int) {
					defer wg.Done()
					cnt := 0
					for j := start; j < end; j++ {
						if values[j] <= curClip {
							cnt++
						}
					}
					localCounts[workerID] = cnt
				}(w, startIdx, endIdx)
			}
			wg.Wait()
			for _, cnt := range localCounts {
				belowCount += cnt
			}
		}

		noisyBelow := NoisyCount(belowCount, epsPerIter)
		frac := noisyBelow / totalCount
		if frac < targetQuantile {
			curClip *= 1.5
		} else {
			curClip *= 0.85
		}
	}

	return 0.0, curClip
}

// GroupBy 对表格记录按 groupCol 分组，并在 targetCol 上执行带差分隐私的分组聚合计算。
// 支持 count、sum、mean 聚合算子，机制支持 laplace 与 gaussian。
func GroupBy(rows []map[string]string, groupCol, targetCol, agg string, epsilon, delta, clipLower, clipUpper float64, mechanism string) (map[string]float64, error) {
	if len(rows) == 0 {
		return map[string]float64{}, nil
	}
	if agg == "" {
		agg = "count"
	}
	if clipUpper <= clipLower {
		clipUpper = clipLower + 1.0
	}

	// 1. 按 groupCol 分组
	groups := make(map[string][]float64)
	for _, row := range rows {
		gVal := row[groupCol]
		if gVal == "" {
			gVal = "unknown"
		}
		var val float64
		if tStr, ok := row[targetCol]; ok && tStr != "" {
			for i, r := range tStr {
				if (r >= '0' && r <= '9') || r == '.' || r == '-' {
					continue
				}
				tStr = tStr[:i]
				break
			}
			var parsed float64
			n, _ := fmt.Sscanf(tStr, "%f", &parsed)
			if n > 0 {
				val = parsed
			}
		}
		groups[gVal] = append(groups[gVal], val)
	}

	// 2. 对每个分组独立计算并加噪
	// 按 DP 基本组合定理将 (ε,δ) 均匀分配至各分组：
	// k 个分组各消耗 ε/k，总隐私成本 = k × (ε/k) = ε。
	numGroups := float64(len(groups))
	epsPerGroup := epsilon / numGroups
	deltaPerGroup := delta / numGroups
	sensitivity := clipUpper - clipLower
	if sensitivity <= 0 {
		sensitivity = 1.0
	}

	result := make(map[string]float64, len(groups))
	for gVal, vals := range groups {
		switch strings.ToLower(agg) {
		case "count":
			result[gVal] = NoisyCount(len(vals), epsPerGroup)
		case "sum":
			var clippedVals []float64
			for _, v := range vals {
				clippedVals = append(clippedVals, ClipValueRange(v, clipLower, clipUpper))
			}
			var sum float64
			for _, v := range clippedVals {
				sum += v
			}
			if strings.ToLower(mechanism) == "gaussian" && deltaPerGroup > 0 {
				result[gVal] = AddGaussianNoise(sum, epsPerGroup, deltaPerGroup, sensitivity)
			} else {
				result[gVal] = AddLaplaceNoise(sum, epsPerGroup, sensitivity)
			}
		case "mean":
			// 先截断至 [clipLower, clipUpper] 确保敏感度有界
			var clippedVals []float64
			for _, v := range vals {
				clippedVals = append(clippedVals, ClipValueRange(v, clipLower, clipUpper))
			}
			// NoisyMean 内部以 ClipValue(v, bound) 对称截断，
			// bound 取 clipLower/clipUpper 绝对值的较大者以覆盖完整值域
			bound := math.Max(math.Abs(clipLower), math.Abs(clipUpper))
			if bound <= 0 {
				bound = 1.0
			}
			result[gVal] = NoisyMean(clippedVals, epsPerGroup, deltaPerGroup, bound)
		default:
			result[gVal] = NoisyCount(len(vals), epsPerGroup)
		}
	}

	return result, nil
}

// Aggregate 对记录集按指定字段和算子列表执行多指标差分隐私聚合计算。
// specs 为 map[字段名]聚合算子 (count/sum/mean)。
// clipLower/clipUpper 为 sum/mean 聚合的值截断区间，确保敏感度有界；
// mechanism 支持 "laplace"（默认）或 "gaussian"。
func Aggregate(rows []map[string]string, specs map[string]string, epsilon, delta, clipLower, clipUpper float64, mechanism string) (map[string]float64, error) {
	if len(rows) == 0 || len(specs) == 0 {
		return map[string]float64{}, nil
	}

	numSpecs := float64(len(specs))
	epsPerSpec := epsilon / numSpecs
	deltaPerSpec := delta / numSpecs

	// 值截断区间兜底：clipUpper <= clipLower 时退化为对称截断 [0, 1]
	if clipUpper <= clipLower {
		clipUpper = clipLower + 1.0
	}
	sensitivity := clipUpper - clipLower
	if sensitivity <= 0 {
		sensitivity = 1.0
	}

	result := make(map[string]float64, len(specs))
	for col, agg := range specs {
		var vals []float64
		for _, row := range rows {
			if vStr, ok := row[col]; ok && vStr != "" {
				var val float64
				n, _ := fmt.Sscanf(vStr, "%f", &val)
				if n > 0 {
					vals = append(vals, val)
				}
			}
		}

		key := col + "_" + strings.ToLower(agg)
		switch strings.ToLower(agg) {
		case "count":
			result[key] = NoisyCount(len(vals), epsPerSpec)
		case "sum":
			// 先截断至 [clipLower, clipUpper] 再求和，确保敏感度 = clipUpper - clipLower 有界
			var clippedSum float64
			for _, v := range vals {
				clippedSum += ClipValueRange(v, clipLower, clipUpper)
			}
			if strings.ToLower(mechanism) == "gaussian" && deltaPerSpec > 0 {
				result[key] = AddGaussianNoise(clippedSum, epsPerSpec, deltaPerSpec, sensitivity)
			} else {
				result[key] = AddLaplaceNoise(clippedSum, epsPerSpec, sensitivity)
			}
		case "mean":
			// NoisyMean 内部执行 ClipValue(v, clipBound) 并以 clipBound/n 为敏感度
			result[key] = NoisyMean(vals, epsPerSpec, deltaPerSpec, clipUpper)
		default:
			result[key] = NoisyCount(len(vals), epsPerSpec)
		}
	}

	return result, nil
}
