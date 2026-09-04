// Package kano 提供 K-匿名（K-Anonymity）隐私保护原语。
//
// 包含记录级启发式脱敏与数据集级 Mondrian 多维分区泛化算法。
package kano

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// KAnonymizeResult 表级 K-匿名处理结果（Mondrian 算法）。
type KAnonymizeResult struct {
	Records                 []Record `json:"records"`
	K                       int      `json:"k"`
	QICols                  []string `json:"qi_cols"`
	EquivalenceClassesCount int      `json:"equivalence_classes_count"`
}

// Mondrian 对整张表执行 K-匿名泛化（Mondrian 多维分区算法）。
//
// 步骤：
// 1. 校验输入行数 >= k，校验 qi_cols 非空且存在于记录中。
// 2. 递归选择跨度最大维度进行中位数切分。
// 3. 对叶子等价组实施区间/集合泛化。
func Mondrian(rows []Record, qiCols []string, k int, maxDepth int) (*KAnonymizeResult, error) {
	if len(rows) == 0 {
		return &KAnonymizeResult{Records: nil, K: k, QICols: qiCols, EquivalenceClassesCount: 0}, nil
	}
	if k < 2 {
		return nil, fmt.Errorf("k must be at least 2 for meaningful anonymity, got %d", k)
	}
	if len(rows) < k {
		return nil, fmt.Errorf("input table has %d rows, but k-anonymity requires at least %d", len(rows), k)
	}
	if len(qiCols) == 0 {
		return nil, fmt.Errorf("qi_cols must not be empty")
	}
	// 校验 qi_cols 存在
	for _, col := range qiCols {
		if _, ok := rows[0][col]; !ok {
			return nil, fmt.Errorf("qi_col %q not found in records", col)
		}
	}

	result := mondrianRecurse(rows, qiCols, k, maxDepth)
	// 计算真实等价组数
	eqSet := make(map[string]struct{})
	for _, r := range result {
		key := ""
		for _, c := range qiCols {
			if key != "" {
				key += "|"
			}
			key += r[c]
		}
		eqSet[key] = struct{}{}
	}

	return &KAnonymizeResult{
		Records:                 result,
		K:                       k,
		QICols:                  qiCols,
		EquivalenceClassesCount: len(eqSet),
	}, nil
}

func mondrianRecurse(records []Record, qiCols []string, k, depth int) []Record {
	if len(records) < 2*k || depth <= 0 {
		return generalize(records, qiCols)
	}

	// 选择跨度最大的维度
	dim := chooseDimension(records, qiCols)
	splitIdx := medianSplit(records, dim, k)
	if splitIdx == -1 {
		return generalize(records, qiCols)
	}

	// 按选定维度排序
	sorted := make([]Record, len(records))
	copy(sorted, records)
	sort.SliceStable(sorted, func(i, j int) bool {
		return compareValues(sorted[i][dim], sorted[j][dim]) < 0
	})

	left := mondrianRecurse(sorted[:splitIdx], qiCols, k, depth-1)
	right := mondrianRecurse(sorted[splitIdx:], qiCols, k, depth-1)
	return append(left, right...)
}

func chooseDimension(records []Record, qiCols []string) string {
	bestDim := qiCols[0]
	bestSpan := -1.0
	for _, col := range qiCols {
		s := span(records, col)
		if s > bestSpan {
			bestSpan = s
			bestDim = col
		}
	}
	return bestDim
}

func span(records []Record, col string) float64 {
	var values []string
	for _, r := range records {
		if v, ok := r[col]; ok {
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		return 0
	}
	// 检查是否全为数值
	allNumeric := true
	var nums []float64
	for _, v := range values {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			allNumeric = false
			break
		}
		nums = append(nums, f)
	}
	if allNumeric && len(nums) > 0 {
		minV, maxV := nums[0], nums[0]
		for _, n := range nums[1:] {
			if n < minV {
				minV = n
			}
			if n > maxV {
				maxV = n
			}
		}
		return maxV - minV
	}
	// 分类型：跨度 = 唯一值数 - 1
	unique := make(map[string]struct{})
	for _, v := range values {
		unique[v] = struct{}{}
	}
	return float64(len(unique) - 1)
}

func medianSplit(records []Record, dim string, k int) int {
	if len(records) < 2*k {
		return -1
	}
	sorted := make([]Record, len(records))
	copy(sorted, records)
	sort.SliceStable(sorted, func(i, j int) bool {
		return compareValues(sorted[i][dim], sorted[j][dim]) < 0
	})
	mid := len(sorted) / 2
	splitIdx := max(k, min(mid, len(sorted)-k))
	if splitIdx < k || len(sorted)-splitIdx < k {
		return -1
	}
	return splitIdx
}

func generalize(records []Record, qiCols []string) []Record {
	if len(records) == 0 {
		return nil
	}
	// 对每个 QI 列计算泛化值
	genValues := make(map[string]string)
	for _, col := range qiCols {
		var values []string
		for _, r := range records {
			if v, ok := r[col]; ok {
				values = append(values, v)
			}
		}
		if len(values) == 0 {
			continue
		}
		// 检查是否全为数值
		allNumeric := true
		var nums []float64
		for _, v := range values {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				allNumeric = false
				break
			}
			nums = append(nums, f)
		}
		if allNumeric && len(nums) > 0 {
			minV, maxV := nums[0], nums[0]
			for _, n := range nums[1:] {
				if n < minV {
					minV = n
				}
				if n > maxV {
					maxV = n
				}
			}
			if minV == maxV {
				genValues[col] = formatNum(minV)
			} else {
				genValues[col] = fmt.Sprintf("[%s-%s]", formatNum(minV), formatNum(maxV))
			}
		} else {
			// 分类型：排序后的唯一值集合
			unique := make(map[string]struct{})
			for _, v := range values {
				unique[v] = struct{}{}
			}
			sorted := make([]string, 0, len(unique))
			for v := range unique {
				sorted = append(sorted, v)
			}
			sort.Strings(sorted)
			if len(sorted) == 1 {
				genValues[col] = sorted[0]
			} else {
				genValues[col] = "{" + strings.Join(sorted, ",") + "}"
			}
		}
	}

	// 将泛化值应用到每条记录
	result := make([]Record, len(records))
	for i, r := range records {
		newRec := make(Record, len(r))
		for k, v := range r {
			newRec[k] = v
		}
		for col, genVal := range genValues {
			newRec[col] = genVal
		}
		result[i] = newRec
	}
	return result
}

func formatNum(f float64) string {
	if f == math.Trunc(f) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
