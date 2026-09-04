// Package naming 测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证安全等级词表与 rules/taxonomies/default.yaml 权威源的一致性：
//  1. 【词表与 YAML 权威源同步】：断言代码内安全等级（L1~L5）的标识、中文名、排名与
//     rules/taxonomies/default.yaml 完全一致，任何服务侧不得出现第二份副本（P1-5）；
//  2. 【安全等级归一化】：覆盖 L1~L5 与 public…top_secret 两套词表的互转、
//     大小写/空白容忍，以及词表外脏值返回空串（严禁静默兆底为某个等级）；
//  3. 【MaxSecurityLevelID 混合词表取最高】：验证混合词表输入取最高等级，
//     以及全部不可识别时返回空串的 fail-closed 语义；
//  4. 【返回值拷贝安全性】：确保 SecurityLevelIDs/SecurityLevelLabels 返回的是副本，
//     调用方无法通过修改返回值篡改全局词表。
// ==============================================================================

package naming

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// taxonomyYAML mirrors the levels block of rules/taxonomies/default.yaml.
type taxonomyYAML struct {
	Levels map[string]struct {
		ID   string `yaml:"id"`
		Name string `yaml:"name"`
		Rank int    `yaml:"rank"`
	} `yaml:"levels"`
	DefaultLevel string `yaml:"default_level"`
}

// loadTaxonomy reads the authoritative level vocabulary file from the repository root.
func loadTaxonomy(t *testing.T) taxonomyYAML {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "rules", "taxonomies", "default.yaml"),
		filepath.Join("..", "..", "services", "privacy-engine", "rules", "taxonomies", "default.yaml"),
		filepath.Join("..", "services", "privacy-engine", "rules", "taxonomies", "default.yaml"),
		filepath.Join("services", "privacy-engine", "rules", "taxonomies", "default.yaml"),
		filepath.Join("rules", "taxonomies", "default.yaml"),
	}
	var foundPath string
	var raw []byte
	var err error
	for _, p := range candidates {
		raw, err = os.ReadFile(p)
		if err == nil {
			foundPath = p
			break
		}
	}
	if err != nil {
		t.Fatalf("read default.yaml from candidates %v: %v", candidates, err)
	}
	var tax taxonomyYAML
	if err := yaml.Unmarshal(raw, &tax); err != nil {
		t.Fatalf("parse %s: %v", foundPath, err)
	}
	return tax
}

// TestSecurityLevelsMatchTaxonomyYAML 断言代码内词表与 rules/taxonomies/default.yaml 完全一致
// （P1-5：等级标识、中文名、排名三者在任何服务侧不得出现第二份副本）。
func TestSecurityLevelsMatchTaxonomyYAML(t *testing.T) {
	tax := loadTaxonomy(t)
	if len(tax.Levels) != len(securityLevels) {
		t.Fatalf("taxonomy has %d levels, code has %d", len(tax.Levels), len(securityLevels))
	}
	for _, spec := range securityLevels {
		entry, ok := tax.Levels[spec.id]
		if !ok {
			t.Fatalf("level %s (%s) missing from taxonomy", spec.id, spec.label)
		}
		if entry.ID != spec.id {
			t.Errorf("taxonomy id %q != code id %q", entry.ID, spec.id)
		}
		if entry.Name != spec.label {
			t.Errorf("level %s label drift: taxonomy=%q code=%q", spec.id, entry.Name, spec.label)
		}
		if entry.Rank != spec.rank {
			t.Errorf("level %s rank drift: taxonomy=%d code=%d", spec.id, entry.Rank, spec.rank)
		}
	}
}

// TestSecurityLevelNormalization 覆盖两套词表表达（L1~L5 与 public…top_secret）的互转、
// 大小写/空白容忍，以及词表外脏值必须返回空串（严禁静默兜底为某个等级）。
func TestSecurityLevelNormalization(t *testing.T) {
	tests := []struct {
		in        string
		wantID    string
		wantName  string
		wantRank  int
		wantLabel string
	}{
		{in: "L3", wantID: "L3", wantName: "confidential", wantRank: 3, wantLabel: "敏感数据"},
		{in: " l4 ", wantID: "L4", wantName: "secret", wantRank: 4, wantLabel: "高敏感数据"},
		{in: "top_secret", wantID: "L5", wantName: "top_secret", wantRank: 5, wantLabel: "极敏感数据"},
		{in: "PUBLIC", wantID: "L1", wantName: "public", wantRank: 1, wantLabel: "公开数据"},
		{in: "Internal", wantID: "L2", wantName: "internal", wantRank: 2, wantLabel: "内部数据"},
		{in: "critical", wantID: "", wantName: "", wantRank: 0, wantLabel: ""},
		{in: "L6", wantID: "", wantName: "", wantRank: 0, wantLabel: ""},
		{in: "", wantID: "", wantName: "", wantRank: 0, wantLabel: ""},
	}
	for _, tc := range tests {
		if got := NormalizeSecurityLevelID(tc.in); got != tc.wantID {
			t.Errorf("NormalizeSecurityLevelID(%q) = %q, want %q", tc.in, got, tc.wantID)
		}
		if got := SecurityLevelName(tc.in); got != tc.wantName {
			t.Errorf("SecurityLevelName(%q) = %q, want %q", tc.in, got, tc.wantName)
		}
		if got := SecurityLevelRank(tc.in); got != tc.wantRank {
			t.Errorf("SecurityLevelRank(%q) = %d, want %d", tc.in, got, tc.wantRank)
		}
		if got := SecurityLevelLabel(tc.in); got != tc.wantLabel {
			t.Errorf("SecurityLevelLabel(%q) = %q, want %q", tc.in, got, tc.wantLabel)
		}
	}
}

// TestMaxSecurityLevelID 验证混合词表取最高等级与「全部不可识别返回空串」的 fail-closed 语义。
func TestMaxSecurityLevelID(t *testing.T) {
	if got := MaxSecurityLevelID("public", "secret", "L2"); got != "L4" {
		t.Errorf("MaxSecurityLevelID mixed vocab = %q, want L4", got)
	}
	if got := MaxSecurityLevelID("L1", "public"); got != "L1" {
		t.Errorf("MaxSecurityLevelID lowest = %q, want L1", got)
	}
	if got := MaxSecurityLevelID("critical", ""); got != "" {
		t.Errorf("MaxSecurityLevelID unknown-only = %q, want empty", got)
	}
	if got := MaxSecurityLevelID(); got != "" {
		t.Errorf("MaxSecurityLevelID empty = %q, want empty", got)
	}
}

// TestSecurityLevelIDsAndLabelsAreCopies 确保调用方无法通过修改返回值篡改全局词表。
func TestSecurityLevelIDsAndLabelsAreCopies(t *testing.T) {
	ids := SecurityLevelIDs()
	ids[0] = "tampered"
	if SecurityLevelIDs()[0] != SecurityLevelL1 {
		t.Error("SecurityLevelIDs leaked its backing slice")
	}

	labels := SecurityLevelLabels()
	labels["L5"] = "tampered"
	if SecurityLevelLabels()["L5"] != "极敏感数据" {
		t.Error("SecurityLevelLabels leaked its map")
	}
}
