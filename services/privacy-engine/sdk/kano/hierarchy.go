// Package kano 提供 K-匿名层次泛化函数。
//
// hierarchy.go — 准标识符 (QI) 层次泛化函数，与 Python engine/privacy/kano.py 完全对齐。
package kano

import (
	"fmt"
	"strconv"
	"strings"
)

// ──────────────────────────────────────────────
// 层次泛化函数类型
// ──────────────────────────────────────────────

// HierarchyFunc 层次泛化函数签名。
// 输入：原始值、泛化层级；输出：泛化后的值。
type HierarchyFunc func(value string, level int) string

// ──────────────────────────────────────────────
// 内置层次泛化函数（与 Python kano.py 完全对齐）
// ──────────────────────────────────────────────

// AgeHierarchy 年龄泛化层次函数。
//
// 根据 level 将具体年龄泛化为区间：
//   - level 0: 原始值
//   - level 1: 5 岁区间，如 "[25-30]"
//   - level 2: 10 岁区间
//   - level 3: 20 岁区间
//   - level >= 4: "*"
func AgeHierarchy(value string, level int) string {
	age, err := strconv.Atoi(value)
	if err != nil {
		if level >= 1 {
			return "*"
		}
		return value
	}
	switch level {
	case 0:
		return value
	case 1:
		start := (age / 5) * 5
		return fmt.Sprintf("[%d-%d]", start, start+5)
	case 2:
		start := (age / 10) * 10
		return fmt.Sprintf("[%d-%d]", start, start+10)
	case 3:
		start := (age / 20) * 20
		return fmt.Sprintf("[%d-%d]", start, start+20)
	default:
		return "*"
	}
}

// ZipcodeHierarchy 邮编泛化层次函数。
//
// 根据 level 逐步隐藏邮编后几位：
//   - level 0: 原始值
//   - level 1: 保留前 3 位
//   - level 2: 保留前 2 位
//   - level 3: 保留前 1 位
//   - level >= 4 或长度不足: "*"
func ZipcodeHierarchy(value string, level int) string {
	if level == 0 {
		return value
	}
	runes := []rune(value)
	if level == 1 && len(runes) >= 3 {
		return string(runes[:3]) + "***"
	}
	if level == 2 && len(runes) >= 2 {
		return string(runes[:2]) + "****"
	}
	if level == 3 && len(runes) >= 1 {
		return string(runes[:1]) + "*****"
	}
	return "*"
}

// GenderHierarchy 性别泛化层次函数。
//
//   - level 0: 原始值
//   - level >= 1: "*" (完全抑制)
func GenderHierarchy(value string, level int) string {
	if level >= 1 {
		return "*"
	}
	return value
}

// SalaryHierarchy 薪资泛化层次函数。
//
// 根据 level 将具体薪资泛化为区间：
//   - level 0: 原始值
//   - level 1: 5K 区间，如 "[25K-30K]"
//   - level 2: 10K 区间
//   - level 3: 50K 区间
//   - level >= 4: "*"
func SalaryHierarchy(value string, level int) string {
	salary, err := strconv.Atoi(value)
	if err != nil {
		if level >= 1 {
			return "*"
		}
		return value
	}
	switch level {
	case 0:
		return value
	case 1:
		start := (salary / 5) * 5
		return fmt.Sprintf("[%dK-%dK]", start, start+5)
	case 2:
		start := (salary / 10) * 10
		return fmt.Sprintf("[%dK-%dK]", start, start+10)
	case 3:
		start := (salary / 50) * 50
		return fmt.Sprintf("[%dK-%dK]", start, start+50)
	default:
		return "*"
	}
}

// EducationHierarchy 学历泛化层次函数。
//
// 根据 level 将具体学历泛化为更宽泛的类别：
//   - level 0: 原始值
//   - level 1: 合并为 "高等教育" / "基础教育"
//   - level >= 2: "*"
func EducationHierarchy(value string, level int) string {
	if level == 0 {
		return value
	}
	higherEdu := map[string]bool{
		"本科": true, "硕士": true, "博士": true, "博士后": true,
		"MBA": true, "EMBA": true,
		"bachelor": true, "master": true, "phd": true, "doctorate": true,
	}
	if level == 1 {
		lower := strings.ToLower(value)
		if higherEdu[value] || higherEdu[lower] {
			return "高等教育"
		}
		return "基础教育"
	}
	return "*"
}

// ──────────────────────────────────────────────
// 内置层次函数注册表
// ──────────────────────────────────────────────

// BuiltinHierarchies 内置准标识符泛化层次函数映射表。
var BuiltinHierarchies = map[string]HierarchyFunc{
	"age":       AgeHierarchy,
	"zipcode":   ZipcodeHierarchy,
	"gender":    GenderHierarchy,
	"salary":    SalaryHierarchy,
	"education": EducationHierarchy,
}

// ──────────────────────────────────────────────
// 层次选择与记录泛化
// ──────────────────────────────────────────────

// ChooseLevel 根据 k 值选择泛化层级。
//
// 采用启发式策略：level 与 k/5 成正比，但不超过 maxLevel 且至少为 1。
func ChooseLevel(k, maxLevel int) (int, error) {
	if k < 2 {
		return 0, fmt.Errorf("k must be at least 2, got %d", k)
	}
	if maxLevel < 1 {
		return 0, fmt.Errorf("maxLevel must be at least 1, got %d", maxLevel)
	}
	level := k / 5
	if level < 1 {
		level = 1
	}
	if level > maxLevel {
		level = maxLevel
	}
	return level, nil
}

// AnonymizeRecord 对单条记录按 K-匿名要求进行泛化。
//
// qiCols 为准标识符字段列表，hierarchies 为字段名到层次函数的映射，
// k 为匿名化参数。返回泛化后的记录。
func AnonymizeRecord(record Record, qiCols []string, hierarchies map[string]HierarchyFunc, k int) (Record, error) {
	if k < 2 {
		return nil, fmt.Errorf("k must be at least 2, got %d", k)
	}
	if len(qiCols) == 0 {
		return record, nil
	}

	// 拷贝记录
	result := make(Record, len(record))
	for k, v := range record {
		result[k] = v
	}

	// 对每个准标识符字段应用层次泛化
	for _, col := range qiCols {
		value, ok := result[col]
		if !ok {
			continue
		}
		hierFunc, ok := hierarchies[col]
		if !ok {
			// 尝试内置层次函数
			hierFunc, ok = BuiltinHierarchies[col]
			if !ok {
				continue
			}
		}
		// 根据 k 值选择泛化层级
		maxLevel := 4 // 默认最大层级
		level, err := ChooseLevel(k, maxLevel)
		if err != nil {
			return nil, err
		}
		result[col] = hierFunc(value, level)
	}
	return result, nil
}

// AnonymizeRecordBatch 对批量记录按 K-匿名要求进行泛化。
func AnonymizeRecordBatch(records []Record, qiCols []string, hierarchies map[string]HierarchyFunc, k int) ([]Record, error) {
	results := make([]Record, 0, len(records))
	for _, record := range records {
		result, err := AnonymizeRecord(record, qiCols, hierarchies, k)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}
