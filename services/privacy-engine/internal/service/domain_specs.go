// Package service — 领域规则目录 (rules/domains/*.yaml) 字段脱敏规格与别名动态装配。
// 支持通过 YAML 声明式手动配置新字段、重命名字段别名与脱敏算子，并无缝支持热重载。
package service

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/medical"
)

// YAMLFieldSpec 对应 rules/domains/*.yaml 中定义的字段规格。
type YAMLFieldSpec struct {
	Name        string  `yaml:"name"`
	Category    string  `yaml:"category"`
	Level       int     `yaml:"level"`
	Treatment   string  `yaml:"treatment"`
	Band        float64 `yaml:"band,omitempty"`
	ClipLower   float64 `yaml:"clip_lower,omitempty"`
	ClipUpper   float64 `yaml:"clip_upper,omitempty"`
	DataSource  string  `yaml:"datasource,omitempty"` // 可选: yibao / kangyang，空表示通用
	Description string  `yaml:"description,omitempty"`
}

// domainConfigFile 映射 rules/domains/*.yaml 中与字段脱敏矩阵及别名相关的配置段。
type domainConfigFile struct {
	FieldSpecs []YAMLFieldSpec   `yaml:"field_specs"`
	Aliases    map[string]string `yaml:"aliases"`
}

// loadAndRegisterDomainSpecs 遍历 rulesDir 下的所有 YAML 文件，
// 提取 field_specs 与 aliases 并注册到统一医疗脱敏流水线与全局别名表中。
func loadAndRegisterDomainSpecs(rulesDir string, pipeline *medical.Pipeline) {
	if strings.TrimSpace(rulesDir) == "" {
		return
	}
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		filePath := filepath.Join(rulesDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var cfg domainConfigFile
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			continue
		}

		// 1. 注册字段别名映射
		if len(cfg.Aliases) > 0 {
			medical.RegisterFieldAliases(cfg.Aliases)
			slog.Info("registered domain field aliases from yaml", "file", entry.Name(), "count", len(cfg.Aliases))
		}

		// 2. 转换并注册字段脱敏规格
		if len(cfg.FieldSpecs) > 0 {
			var specs []medical.FieldSpec
			for _, f := range cfg.FieldSpecs {
				if f.Name == "" {
					continue
				}
				spec := medical.FieldSpec{
					Name:      f.Name,
					Category:  medical.FieldCategory(f.Category),
					Level:     f.Level,
					Treatment: normalizeTreatment(f.Treatment),
					Band:      f.Band,
					ClipLower: f.ClipLower,
					ClipUpper: f.ClipUpper,
				}
				specs = append(specs, spec)
			}
			if pipeline != nil && len(specs) > 0 {
				pipeline.RegisterFields(specs...)
			}
			slog.Info("registered domain field specs from yaml", "file", entry.Name(), "specs_count", len(cfg.FieldSpecs))
		}
	}
}

// normalizeTreatment 将 YAML 中书写的脱敏算子别名归一化为 SDK 标准 Treatment 常量。
func normalizeTreatment(t string) medical.Treatment {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "age_band", "age_band_kanon", "age":
		return medical.TreatmentAgeBand
	case "keep", "allow", "plain":
		return medical.TreatmentKeep
	case "mask_name", "name", "chinese_name":
		return medical.TreatmentMaskName
	case "mask_id_card", "id_card", "id_card_no", "idcard":
		return medical.TreatmentMaskIdCard
	case "mask_card", "card", "bank_card":
		return medical.TreatmentMaskCard
	case "mask_phone", "phone", "mobile":
		return medical.TreatmentMaskPhone
	case "mask_email", "email", "mail":
		return medical.TreatmentMaskEmail
	case "mask_address", "address":
		return medical.TreatmentMaskAddress
	case "mask_partial", "partial":
		return medical.TreatmentMaskPartial
	case "hash_id", "hash":
		return medical.TreatmentHashID
	case "clinical_text", "clinical_text_redaction":
		return medical.TreatmentClinicalText
	case "disease_generalize", "disease_generalization":
		return medical.TreatmentDiseaseGeneralize
	case "enum_generalize", "enum_generalization":
		return medical.TreatmentEnumGeneralize
	case "icd10", "icd10_governance":
		return medical.TreatmentICD10
	case "date_month":
		return medical.TreatmentDateMonth
	case "date_year":
		return medical.TreatmentDateYear
	case "dp_noise", "dp", "noise":
		return medical.TreatmentDPNoise
	case "bounding", "band":
		return medical.TreatmentBounding
	case "drop":
		return medical.TreatmentDrop
	default:
		return medical.Treatment(t)
	}
}
