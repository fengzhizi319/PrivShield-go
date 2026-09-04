// Package ldp 提供本地差分隐私 (Local Differential Privacy) 原语。
//
// 实现二值 Randomized Response、多类别 O-RR (Optimized Randomized Response)
// 与无偏频数估计，所有函数均为零状态纯计算。
package ldp

import (
	"math"
	"math/rand/v2"
	"runtime"
	"sync"
)

// ──────────────────────────────────────────────
// 二值 Randomized Response
// ──────────────────────────────────────────────

// RandomizedResponse 对布尔值执行 Randomized Response。
// 以概率 p = e^ε / (1 + e^ε) 返回真实值，以概率 1-p 返回翻转值。
// 满足 ε-本地差分隐私。
func RandomizedResponse(value bool, epsilon float64) bool {
	if epsilon <= 0 {
		return value
	}
	p := math.Exp(epsilon) / (1 + math.Exp(epsilon))
	if rand.Float64() < p {
		return value
	}
	return !value
}

// EstimateTrueCount 从 Randomized Response 结果中估计真实 true 计数。
// 公式：count_true = (n*p - (n - sum)) / (2*p - 1)
func EstimateTrueCount(responses []bool, epsilon float64) int {
	n := len(responses)
	if n == 0 || epsilon <= 0 {
		return 0
	}
	p := math.Exp(epsilon) / (1 + math.Exp(epsilon))
	var sum int
	for _, r := range responses {
		if r {
			sum++
		}
	}
	// 无偏估计
	estimated := (float64(sum) - float64(n)*(1-p)) / (2*p - 1)
	return int(math.Round(estimated))
}

// ──────────────────────────────────────────────
// 多类别 O-RR (Optimized Randomized Response)
// ──────────────────────────────────────────────

// ORRResponse 对离散类别执行 Optimized Randomized Response。
// 以概率 p = e^ε / (e^ε + k - 1) 返回真实值，
// 以概率 1-p 均匀随机返回其他 k-1 个值之一。
// domainSize 为类别总数 k。
func ORRResponse(value int, epsilon float64, domainSize int) int {
	if domainSize <= 1 || epsilon <= 0 {
		return value
	}
	p := math.Exp(epsilon) / (math.Exp(epsilon) + float64(domainSize) - 1)
	if rand.Float64() < p {
		return value
	}
	// 均匀选择其他值
	other := rand.IntN(domainSize - 1)
	if other >= value {
		other++
	}
	return other
}

// EstimateFrequency 从 O-RR 响应中估计各类别频数。
// 返回长度为 domainSize 的切片，第 i 个元素为类别 i 的估计频数。
func EstimateFrequency(responses []int, epsilon float64, domainSize int) []int {
	n := len(responses)
	if n == 0 || domainSize <= 0 {
		return make([]int, domainSize)
	}
	if epsilon <= 0 {
		counts := make([]int, domainSize)
		for _, r := range responses {
			if r >= 0 && r < domainSize {
				counts[r]++
			}
		}
		return counts
	}

	p := math.Exp(epsilon) / (math.Exp(epsilon) + float64(domainSize) - 1)
	q := (1 - p) / float64(domainSize-1)

	// 统计各响应计数
	counts := make([]int, domainSize)
	for _, r := range responses {
		if r >= 0 && r < domainSize {
			counts[r]++
		}
	}

	// 无偏估计：count_i = (n_i - n*q) / (p - q)
	estimated := make([]int, domainSize)
	var estSum int
	for i := 0; i < domainSize; i++ {
		est := (float64(counts[i]) - float64(n)*q) / (p - q)
		val := int(math.Round(math.Max(0, est)))
		estimated[i] = val
		estSum += val
	}

	// 样本总数守恒保形校准
	if estSum > 0 && estSum != n && n > 10 {
		scale := float64(n) / float64(estSum)
		var newSum int
		for i := 0; i < domainSize; i++ {
			estimated[i] = int(math.Round(float64(estimated[i]) * scale))
			newSum += estimated[i]
		}
		diff := n - newSum
		if diff != 0 && domainSize > 0 {
			maxIdx := 0
			for i := 1; i < domainSize; i++ {
				if estimated[i] > estimated[maxIdx] {
					maxIdx = i
				}
			}
			if estimated[maxIdx]+diff >= 0 {
				estimated[maxIdx] += diff
			}
		}
	}

	return estimated
}

// ──────────────────────────────────────────────
// 数值型 LDP（基于分段机制）
// ──────────────────────────────────────────────

// NumericLDP 对 [lower, upper] 区间内的数值添加本地差分隐私噪声。
// 使用简化的分段机制：将值归一化至 [0, 1]，添加 Laplace 噪声后截断回区间。
func NumericLDP(value, lower, upper, epsilon float64) float64 {
	if upper <= lower || epsilon <= 0 {
		return value
	}
	// 归一化至 [0, 1]
	normalized := (value - lower) / (upper - lower)
	// 添加 Laplace 噪声（敏感度 = 1）
	noisy := AddLaplaceSimple(normalized, epsilon)
	// 截断回 [0, 1] 并反归一化
	noisy = math.Max(0, math.Min(1, noisy))
	return lower + noisy*(upper-lower)
}

// AddLaplaceSimple 简化版 Laplace 噪声（敏感度 = 1）。
func AddLaplaceSimple(value, epsilon float64) float64 {
	if epsilon <= 0 {
		return value
	}
	scale := 1.0 / epsilon
	u := rand.Float64() - 0.5
	sgn := 1.0
	if u < 0 {
		sgn = -1.0
	}
	noise := -scale * sgn * math.Log(1.0-2.0*math.Abs(u))
	return value + noise
}

// ──────────────────────────────────────────────
// Python LocalDPApi 对齐函数
// ──────────────────────────────────────────────

// PerturbBinaryBatch 批量对二值数据进行本地 DP 扰动（Warner 模型，支持多核并发无锁分块计算）。
// 与 Python perturb_binary_batch 对齐。
func PerturbBinaryBatch(values []int, epsilon float64) []int {
	n := len(values)
	result := make([]int, n)
	if n == 0 {
		return result
	}
	if epsilon <= 0 {
		copy(result, values)
		return result
	}

	// 概率 p 仅在循环外计算一次
	p := 1.0 / (1.0 + math.Exp(-epsilon))

	if n <= 1024 {
		for i, v := range values {
			if v == 0 || v == 1 {
				if rand.Float64() < p {
					result[i] = v
				} else {
					result[i] = 1 - v
				}
			} else {
				result[i] = v
			}
		}
		return result
	}

	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers > 16 {
		numWorkers = 16
	}
	if numWorkers > n {
		numWorkers = n
	}

	chunkSize := (n + numWorkers - 1) / numWorkers
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
		go func(s, e int) {
			defer wg.Done()
			// per-worker 独立随机源，消除全局 math/rand 锁竞争
			rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			for i := s; i < e; i++ {
				v := values[i]
				if v == 0 || v == 1 {
					if rng.Float64() < p {
						result[i] = v
					} else {
						result[i] = 1 - v
					}
				} else {
					result[i] = v
				}
			}
		}(startIdx, endIdx)
	}
	wg.Wait()
	return result
}

// perturbBinary 对单个二值数据进行 ε-本地差分隐私扰动。
func perturbBinary(value int, epsilon float64) int {
	if epsilon <= 0 {
		return value
	}
	p := 1.0 / (1.0 + math.Exp(-epsilon))
	if rand.Float64() < p {
		return value
	}
	return 1 - value
}

// PerturbCategoricalBatch 批量对类别型数据进行 k-ary Randomized Response 扰动（支持多核并发无锁分块计算）。
// 与 Python perturb_categorical_batch 对齐。
func PerturbCategoricalBatch(values []string, categories []string, epsilon float64) []string {
	n := len(values)
	result := make([]string, n)
	if n == 0 {
		return result
	}
	k := len(categories)
	if k < 2 || epsilon <= 0 {
		copy(result, values)
		return result
	}

	// 概率 p 仅在循环外计算一次
	p := 1.0 / (1.0 + float64(k-1)*math.Exp(-epsilon))

	// 预先为每个类别构建"其他类别列表"，消除循环内动态切片分配
	othersMap := make(map[string][]string, k)
	for _, cat := range categories {
		others := make([]string, 0, k-1)
		for _, other := range categories {
			if other != cat {
				others = append(others, other)
			}
		}
		othersMap[cat] = others
	}

	if n <= 1024 {
		for i, v := range values {
			if rand.Float64() < p {
				result[i] = v
				continue
			}
			others, ok := othersMap[v]
			if !ok || len(others) == 0 {
				result[i] = categories[rand.IntN(k)]
			} else {
				result[i] = others[rand.IntN(len(others))]
			}
		}
		return result
	}

	numWorkers := runtime.GOMAXPROCS(0)
	if numWorkers > 16 {
		numWorkers = 16
	}
	if numWorkers > n {
		numWorkers = n
	}

	chunkSize := (n + numWorkers - 1) / numWorkers
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
		go func(s, e int) {
			defer wg.Done()
			// per-worker 独立随机源，消除全局 math/rand 锁竞争
			rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			for i := s; i < e; i++ {
				v := values[i]
				if rng.Float64() < p {
					result[i] = v
					continue
				}
				others, ok := othersMap[v]
				if !ok || len(others) == 0 {
					result[i] = categories[rng.IntN(k)]
				} else {
					result[i] = others[rng.IntN(len(others))]
				}
			}
		}(startIdx, endIdx)
	}
	wg.Wait()
	return result
}

// perturbCategorical 对单个类别型数据进行 k-ary Randomized Response 扰动。
func perturbCategorical(value string, categories []string, epsilon float64) string {
	k := len(categories)
	if k < 2 || epsilon <= 0 {
		return value
	}
	// p = e^ε / (k-1 + e^ε) = 1 / (1 + (k-1)*e^(-ε))
	p := 1.0 / (1.0 + float64(k-1)*math.Exp(-epsilon))
	if rand.Float64() < p {
		return value
	}
	// 均匀选择其他 k-1 个类别之一
	others := make([]string, 0, k-1)
	for _, c := range categories {
		if c != value {
			others = append(others, c)
		}
	}
	if len(others) == 0 {
		return value
	}
	return others[rand.IntN(len(others))]
}

// EstimateBinaryFrequency 根据扰动后的二值样本估计真实比例为 1 的频率。
// 与 Python estimate_binary_frequency 对齐。
// 公式：hat_f = (f_reported - (1-p)) / (2p-1)，截断到 [0, 1]。
func EstimateBinaryFrequency(reportedValues []int, epsilon float64) float64 {
	n := len(reportedValues)
	if n == 0 || epsilon <= 0 {
		return 0.0
	}
	// p = 1 / (1 + e^(-ε))
	p := 1.0 / (1.0 + math.Exp(-epsilon))
	// 统计上报样本中 1 的比例
	count := 0
	for _, v := range reportedValues {
		if v == 1 {
			count++
		}
	}
	fReported := float64(count) / float64(n)
	// 无偏纠偏公式
	est := (fReported - (1.0 - p)) / (2.0*p - 1.0)
	// 截断到 [0, 1]
	if est < 0.0 {
		return 0.0
	}
	if est > 1.0 {
		return 1.0
	}
	return est
}

// EstimateCategoricalHistogram 根据扰动后的类别样本估计各类别的真实频率分布。
// 与 Python estimate_categorical_histogram 对齐。
// 返回 map[类别]估计频率，所有频率之和为 1.0。
func EstimateCategoricalHistogram(reportedValues []string, categories []string, epsilon float64) map[string]float64 {
	n := len(reportedValues)
	k := len(categories)
	if k < 2 || n == 0 || epsilon <= 0 {
		// 回退为均匀分布
		result := make(map[string]float64, k)
		for _, c := range categories {
			result[c] = 1.0 / float64(k)
		}
		return result
	}
	// p = 报出真实类别的概率
	p := 1.0 / (1.0 + float64(k-1)*math.Exp(-epsilon))
	// q = 报出任一指定错误类别的概率
	q := (1.0 - p) / float64(k-1)
	denominator := p - q

	// 统计各类别出现次数
	counts := make(map[string]int, k)
	for _, c := range categories {
		counts[c] = 0
	}
	for _, v := range reportedValues {
		if _, ok := counts[v]; ok {
			counts[v]++
		}
	}

	// 无偏估计 + 非负截断
	estimates := make(map[string]float64, k)
	total := 0.0
	for _, c := range categories {
		fReported := float64(counts[c]) / float64(n)
		est := (fReported - q) / denominator
		if est < 0.0 {
			est = 0.0
		}
		estimates[c] = est
		total += est
	}

	// 归一化使总和为 1.0
	if total > 0 {
		for c, v := range estimates {
			estimates[c] = v / total
		}
	} else {
		// 截断后总和为 0，回退为均匀分布
		for _, c := range categories {
			estimates[c] = 1.0 / float64(k)
		}
	}
	return estimates
}
