// Package dynclassification — 标准映射文件加载（P1-3）。
//
// 标准文件（rules/standards/*.yaml）是「外部标准类别 → 本仓 L1~L5 词表」的纯映射声明，
// 不定义规则算子，不直接参与分类决策。其用途：
//  1. 合规对照：局方/密评机构可核查国标类别与本仓等级的对齐关系；
//  2. 默认档位：global_params.default_level 可作为「未列入字段」的备选兜底等级；
//  3. 诊断上报：/ops/diagnostics 列出已加载标准，证明规则库覆盖度。
package dynclassification

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// StandardLevelMapping 描述一条「外部标准类别 → 本仓等级」的映射条目。
type StandardLevelMapping struct {
	NationalCategory string `yaml:"national_category"`
	Level            string `yaml:"level"`
	Rank             int    `yaml:"rank"`
	Note             string `yaml:"note"`
}

// StandardDef 描述一个标准映射文件的完整内容。
type StandardDef struct {
	StandardID   string   `yaml:"standard_id"`
	Description  string   `yaml:"description"`
	Taxonomy     string   `yaml:"taxonomy"`
	Domains      []string `yaml:"domains"`
	GlobalParams struct {
		DefaultLevel string `yaml:"default_level"`
	} `yaml:"global_params"`
	Levels              map[string]StandardLevelMapping `yaml:"levels"`
	ExtraRules          []RuleDef                       `yaml:"extra_rules"`
	ExtraDowngradeRules []RuleDef                       `yaml:"extra_downgrade_rules"`
}

// LoadStandardsFromDir 读取目录下所有 .yaml/.yml 文件并解析为 StandardDef 列表。
// 解析失败的文件会被跳过并记录错误（不阻断启动）。
func LoadStandardsFromDir(dir string) ([]StandardDef, []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []error{fmt.Errorf("read standards dir %q: %w", dir, err)}
	}

	var standards []StandardDef
	var errs []error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			errs = append(errs, fmt.Errorf("read %q: %w", name, err))
			continue
		}
		var sd StandardDef
		if err := yaml.Unmarshal(data, &sd); err != nil {
			errs = append(errs, fmt.Errorf("parse %q: %w", name, err))
			continue
		}
		if sd.StandardID == "" {
			sd.StandardID = strings.TrimSuffix(name, ext)
		}
		standards = append(standards, sd)
	}

	sort.Slice(standards, func(i, j int) bool {
		return standards[i].StandardID < standards[j].StandardID
	})
	return standards, errs
}

// DefaultLevel 返回标准的默认兜底等级；未定义时返回空串。
func (s *StandardDef) DefaultLevel() string {
	if s == nil {
		return ""
	}
	return s.GlobalParams.DefaultLevel
}

// StandardsSummary 返回已加载标准的诊断摘要（供 /ops/diagnostics 消费）。
func StandardsSummary(standards []StandardDef) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(standards))
	for _, sd := range standards {
		entry := map[string]interface{}{
			"standard_id":   sd.StandardID,
			"description":   sd.Description,
			"taxonomy":      sd.Taxonomy,
			"default_level": sd.DefaultLevel(),
			"level_count":   len(sd.Levels),
			"extra_rules":   len(sd.ExtraRules),
		}
		out = append(out, entry)
	}
	return out
}
