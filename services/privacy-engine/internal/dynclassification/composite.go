// Package dynclassification — 复合规则引擎。
//
// 对齐 Python engine/dynclassification/composite.py：
// 用于识别"单字段不敏感、多字段组合后敏感"的上下文场景。
// 在单条记录的字段级分类完成后执行，根据字段名组合升级敏感度等级。
package dynclassification

import (
	"regexp"
	"strings"
	// SecurityLevel 等类型从 engine.go 同包继承
)

// CompositeRuleDef 复合规则定义。
type CompositeRuleDef struct {
	ID            string        `yaml:"id" json:"id"`
	FieldPatterns []string      `yaml:"field_patterns" json:"field_patterns"`
	MinMatches    int           `yaml:"min_matches" json:"min_matches"`
	TargetLevel   SecurityLevel `yaml:"target_level" json:"target_level"`
	Category      string        `yaml:"category" json:"category"`
	Description   string        `yaml:"description" json:"description"`
}

// CompositeTag 复合规则命中产生的标签。
type CompositeTag struct {
	Level        SecurityLevel `json:"level"`
	Category     string        `json:"category"`
	Confidence   float64       `json:"confidence"`
	SourceEngine string        `json:"source_engine"`
	RuleID       string        `json:"rule_id"`
}

// CompositeRuleEngine 复合规则引擎。
type CompositeRuleEngine struct {
	rules            []CompositeRuleDef
	compiledPatterns map[string][]*regexp.Regexp
}

// NewCompositeRuleEngine 创建复合规则引擎实例。
func NewCompositeRuleEngine(rules []CompositeRuleDef) *CompositeRuleEngine {
	e := &CompositeRuleEngine{
		rules:            rules,
		compiledPatterns: make(map[string][]*regexp.Regexp),
	}
	for _, rule := range rules {
		var compiled []*regexp.Regexp
		for _, pattern := range rule.FieldPatterns {
			// 匹配词边界或下划线边界
			bounded := `(?:\b|_)(?:` + pattern + `)(?:\b|_)`
			if re, err := regexp.Compile(`(?i)` + bounded); err == nil {
				compiled = append(compiled, re)
			}
		}
		e.compiledPatterns[rule.ID] = compiled
	}
	return e
}

// normalize 规范化字段名用于模式匹配。
// 去除空格、下划线和连字符，使 id_card / id-card / idcard 统一匹配。
func normalize(name string) string {
	name = strings.ToLower(name)
	replacer := strings.NewReplacer(" ", "", "_", "", "-", "")
	return replacer.Replace(name)
}

// Evaluate 评估单条记录是否命中复合规则。
//
// 算法：
// 1. 规范化记录中的所有字段名。
// 2. 对每条复合规则，计算有多少个模式匹配了至少一个字段名。
// 3. 若匹配数 >= rule.MinMatches，规则触发并产生标签。
func (e *CompositeRuleEngine) Evaluate(record map[string]string) []CompositeTag {
	var tags []CompositeTag

	// 构建规范化字段名 → 原始字段名映射
	normFields := make(map[string]string, len(record))
	for name := range record {
		normFields[normalize(name)] = name
	}

	for _, rule := range e.rules {
		compiled := e.compiledPatterns[rule.ID]
		matched := 0

		for _, re := range compiled {
			for normName, origName := range normFields {
				if re.MatchString(normName) || re.MatchString(origName) {
					matched++
					break
				}
			}
			if matched >= rule.MinMatches {
				break
			}
		}

		if matched >= rule.MinMatches {
			tags = append(tags, CompositeTag{
				Level:        rule.TargetLevel,
				Category:     rule.Category,
				Confidence:   1.0,
				SourceEngine: "COMPOSITE",
				RuleID:       rule.ID,
			})
		}
	}

	return tags
}

// ApplyToRecordLevel 将复合规则标签应用到记录级等级（只升不降）。
func (e *CompositeRuleEngine) ApplyToRecordLevel(currentLevel SecurityLevel, tags []CompositeTag) SecurityLevel {
	if len(tags) == 0 {
		return currentLevel
	}
	best := currentLevel
	for _, tag := range tags {
		if LevelRank(tag.Level) > LevelRank(best) {
			best = tag.Level
		}
	}
	return best
}

// DefaultCompositeRules 返回默认的复合规则集合。
func DefaultCompositeRules() []CompositeRuleDef {
	return []CompositeRuleDef{
		{
			ID:            "full_pii_identity",
			FieldPatterns: []string{`name`, `id_?card`, `phone`, `mobile`},
			MinMatches:    3,
			TargetLevel:   LevelTopSecret,
			Category:      "pii.composite.identity",
			Description:   "姓名+身份证+手机号组合 → 最高敏感级",
		},
		{
			ID:            "medical_identity",
			FieldPatterns: []string{`name`, `patient`, `diagnosis`, `medical`},
			MinMatches:    2,
			TargetLevel:   LevelSecret,
			Category:      "medical.composite.identity",
			Description:   "姓名+诊断/患者信息组合 → 高敏感级",
		},
		{
			ID:            "financial_identity",
			FieldPatterns: []string{`name`, `bank_?card`, `salary`, `income`},
			MinMatches:    2,
			TargetLevel:   LevelSecret,
			Category:      "pii.composite.financial",
			Description:   "姓名+银行卡/薪资组合 → 高敏感级",
		},
	}
}
