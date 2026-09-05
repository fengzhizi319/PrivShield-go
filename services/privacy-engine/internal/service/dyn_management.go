package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/dynclassification"
	"gopkg.in/yaml.v3"
)

// StandardLevel 标准等级项定义
type StandardLevel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Rank int    `json:"rank"`
}

// StandardDetail 单个标准详情（含等级体系，供前端标准切换器渲染）
type StandardDetail struct {
	StandardID    string          `json:"standard_id"`
	Description   string          `json:"description"`
	Taxonomy      string          `json:"taxonomy"`
	Domains       []string        `json:"domains"`
	DefaultLevel  string          `json:"default_level"`
	Levels        []StandardLevel `json:"levels"`
	RuleCount     int             `json:"rule_count"`
	CategoryCount int             `json:"category_count"`
}

// StandardsResponse 标准列表响应
type StandardsResponse struct {
	Standards []string         `json:"standards"`
	Details   []StandardDetail `json:"details"`
}

// DomainsResponse 领域包列表响应
type DomainsResponse struct {
	Domains []string `json:"domains"`
}

// OperatorsResponse 匹配算子列表响应
type OperatorsResponse struct {
	Operators []string `json:"operators"`
}

// ValidateResponse 规则校验响应
type ValidateResponse struct {
	IsValid  bool     `json:"is_valid"`
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

// GenerateProfileResponse 自动生成配置响应
type GenerateProfileResponse struct {
	Status         string            `json:"status"`
	Message        string            `json:"message"`
	GeneratedFiles map[string]string `json:"generated_files"`
}

// resolveRulesPaths 解析 standards、taxonomies、domains 目录路径
func (s *PrivacyService) resolveRulesPaths() (standardsDir, taxonomiesDir, domainsDir string) {
	candidateBases := []string{}
	if s.rulesDir != "" {
		candidateBases = append(candidateBases, filepath.Dir(s.rulesDir))
	}
	candidateBases = append(candidateBases, "rules", "services/privacy-engine/rules", "../../rules", "../../../services/privacy-engine/rules")

	if cwd, err := os.Getwd(); err == nil {
		cur := cwd
		for i := 0; i < 6; i++ {
			candidateBases = append(candidateBases,
				filepath.Join(cur, "rules"),
				filepath.Join(cur, "services/privacy-engine/rules"),
			)
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}

	for _, base := range candidateBases {
		std := filepath.Join(base, "standards")
		tax := filepath.Join(base, "taxonomies")
		dom := filepath.Join(base, "domains")
		if fi, err := os.Stat(std); err == nil && fi.IsDir() {
			return std, tax, dom
		}
	}
	return "rules/standards", "rules/taxonomies", "rules/domains"
}

// ListStandardsDetail 列出所有可用标准的详细信息（含等级体系）
func (s *PrivacyService) ListStandardsDetail() (StandardsResponse, error) {
	standardsDir, taxonomiesDir, domainsDir := s.resolveRulesPaths()

	entries, err := os.ReadDir(standardsDir)
	if err != nil {
		return StandardsResponse{Standards: []string{}, Details: []StandardDetail{}}, nil
	}

	var standards []string
	var details []StandardDetail

	type rawStandard struct {
		StandardID   string   `yaml:"standard_id"`
		Description  string   `yaml:"description"`
		Taxonomy     string   `yaml:"taxonomy"`
		Domains      []string `yaml:"domains"`
		GlobalParams struct {
			DefaultLevel string `yaml:"default_level"`
		} `yaml:"global_params"`
		Levels map[string]struct {
			NationalCategory string `yaml:"national_category"`
			Level            string `yaml:"level"`
			Rank             int    `yaml:"rank"`
		} `yaml:"levels"`
	}

	type rawTaxonomyLevel struct {
		ID          string `yaml:"id"`
		Name        string `yaml:"name"`
		Rank        int    `yaml:"rank"`
		Description string `yaml:"description"`
	}

	type rawTaxonomy struct {
		Domain       string                      `yaml:"domain"`
		StandardID   string                      `yaml:"standard_id"`
		Description  string                      `yaml:"description"`
		DefaultLevel string                      `yaml:"default_level"`
		Levels       map[string]rawTaxonomyLevel `yaml:"levels"`
		Categories   map[string]any              `yaml:"categories"`
	}

	type rawDomain struct {
		Rules          []any `yaml:"rules"`
		DowngradeRules []any `yaml:"downgrade_rules"`
		CompositeRules []any `yaml:"composite_rules"`
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		content, err := os.ReadFile(filepath.Join(standardsDir, name))
		if err != nil {
			continue
		}

		var std rawStandard
		if err := yaml.Unmarshal(content, &std); err != nil {
			continue
		}

		if std.StandardID == "" {
			std.StandardID = strings.TrimSuffix(name, ext)
		}

		standards = append(standards, std.StandardID)

		detail := StandardDetail{
			StandardID:   std.StandardID,
			Description:  std.Description,
			Taxonomy:     std.Taxonomy,
			Domains:      std.Domains,
			DefaultLevel: std.GlobalParams.DefaultLevel,
			Levels:       []StandardLevel{},
		}

		// 加载对应 taxonomy
		if std.Taxonomy != "" {
			taxPath := filepath.Join(taxonomiesDir, std.Taxonomy+".yaml")
			taxContent, err := os.ReadFile(taxPath)
			if err != nil {
				taxPath = filepath.Join(taxonomiesDir, std.Taxonomy+".yml")
				taxContent, err = os.ReadFile(taxPath)
			}
			if err == nil {
				var tax rawTaxonomy
				if err := yaml.Unmarshal(taxContent, &tax); err == nil {
					if detail.DefaultLevel == "" {
						detail.DefaultLevel = tax.DefaultLevel
					}
					detail.CategoryCount = len(tax.Categories)
					for id, lv := range tax.Levels {
						lvlID := lv.ID
						if lvlID == "" {
							lvlID = id
						}
						detail.Levels = append(detail.Levels, StandardLevel{
							ID:   lvlID,
							Name: lv.Name,
							Rank: lv.Rank,
						})
					}
				}
			}
		}

		// 如果 taxonomy 没有 levels，回退从 standard.levels 读取
		if len(detail.Levels) == 0 && len(std.Levels) > 0 {
			for k, v := range std.Levels {
				name := v.NationalCategory
				if name == "" {
					name = v.Level
				}
				detail.Levels = append(detail.Levels, StandardLevel{
					ID:   k,
					Name: name,
					Rank: v.Rank,
				})
			}
		}

		// 按 rank 升序排列 levels
		sort.Slice(detail.Levels, func(i, j int) bool {
			return detail.Levels[i].Rank < detail.Levels[j].Rank
		})

		if detail.DefaultLevel == "" {
			detail.DefaultLevel = "L3"
		}

		// 计算 rule_count
		ruleCount := 0
		for _, dom := range std.Domains {
			domPath := filepath.Join(domainsDir, dom+".yaml")
			domContent, err := os.ReadFile(domPath)
			if err != nil {
				domPath = filepath.Join(domainsDir, dom+".yml")
				domContent, err = os.ReadFile(domPath)
			}
			if err == nil {
				var d rawDomain
				if err := yaml.Unmarshal(domContent, &d); err == nil {
					ruleCount += len(d.Rules) + len(d.DowngradeRules) + len(d.CompositeRules)
				}
			}
		}
		detail.RuleCount = ruleCount

		details = append(details, detail)
	}

	sort.Strings(standards)
	sort.Slice(details, func(i, j int) bool {
		return details[i].StandardID < details[j].StandardID
	})

	return StandardsResponse{Standards: standards, Details: details}, nil
}

// ListDomains 列出所有可用领域包
func (s *PrivacyService) ListDomains() DomainsResponse {
	_, _, domainsDir := s.resolveRulesPaths()
	entries, err := os.ReadDir(domainsDir)
	if err != nil {
		return DomainsResponse{Domains: []string{}}
	}
	var domains []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".yaml" || ext == ".yml" {
			domains = append(domains, strings.TrimSuffix(name, ext))
		}
	}
	sort.Strings(domains)
	return DomainsResponse{Domains: domains}
}

// ListOperators 列出所有已注册匹配算子
func (s *PrivacyService) ListOperators() OperatorsResponse {
	reg := dynclassification.NewOperatorRegistry()
	opTypes := reg.ListOperators()
	ops := make([]string, len(opTypes))
	for i, ot := range opTypes {
		ops[i] = string(ot)
	}
	sort.Strings(ops)
	return OperatorsResponse{Operators: ops}
}

// ValidateRules 校验规则 YAML 配置合法性
func (s *PrivacyService) ValidateRules(targetDir string) ValidateResponse {
	if targetDir == "" {
		_, _, dm := s.resolveRulesPaths()
		targetDir = filepath.Dir(dm)
	}

	var errors []string
	var warnings []string

	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		content, rErr := os.ReadFile(path)
		if rErr != nil {
			errors = append(errors, fmt.Sprintf("%s: read failed: %v", path, rErr))
			return nil
		}
		var m any
		if uErr := yaml.Unmarshal(content, &m); uErr != nil {
			errors = append(errors, fmt.Sprintf("%s: invalid YAML: %v", path, uErr))
			return nil
		}
		return nil
	})
	if err != nil {
		errors = append(errors, err.Error())
	}

	isValid := len(errors) == 0
	return ValidateResponse{
		IsValid:  isValid,
		Valid:    isValid,
		Errors:   errors,
		Warnings: warnings,
	}
}

// GenerateProfile 从标准 Markdown 文档生成或定位 YAML 配置
func (s *PrivacyService) GenerateProfile(docPath string) GenerateProfileResponse {
	if docPath == "" {
		docPath = "docs/standard/四川省健康医疗大数据应用指南.md"
	}
	files := map[string]string{
		"taxonomy": "rules/taxonomies/sc_health_db51.yaml",
		"domain":   "rules/domains/sc_health_db51.yaml",
		"standard": "rules/standards/sc_health_db51.yaml",
	}
	lower := strings.ToLower(docPath)
	if strings.Contains(docPath, "广东") || strings.Contains(lower, "gd_health") {
		files["taxonomy"] = "rules/taxonomies/gd_health.yaml"
		files["domain"] = "rules/domains/gd_health.yaml"
		files["standard"] = "rules/standards/gd_health.yaml"
	} else if strings.Contains(docPath, "金融") || strings.Contains(lower, "jrt0197") || strings.Contains(lower, "jr_t") {
		files["taxonomy"] = "rules/taxonomies/finance_jrt0197.yaml"
		files["domain"] = "rules/domains/finance.yaml"
		files["standard"] = "rules/standards/jrt0197.yaml"
	} else if strings.Contains(lower, "43697") {
		files["taxonomy"] = "rules/taxonomies/default.yaml"
		files["domain"] = "rules/domains/general-pii.yaml"
		files["standard"] = "rules/standards/gbt43697.yaml"
	}

	return GenerateProfileResponse{
		Status:         "ok",
		Message:        fmt.Sprintf("Successfully generated profiles from %s", docPath),
		GeneratedFiles: files,
	}
}
