// hierarchy_test.go — K-匿名层次泛化函数测试 + Python 跨语言对齐
package kano

import (
	"testing"
)

// ──────────────────────────────────────────────
// AgeHierarchy 测试（与 Python age_hierarchy 对齐）
// ──────────────────────────────────────────────

func TestAgeHierarchy(t *testing.T) {
	// Python: age_hierarchy("45", 1) → "[45-50]"
	// Python: age_hierarchy("45", 2) → "[40-50]"
	// Python: age_hierarchy("45", 3) → "[40-60]"
	tests := []struct {
		value string
		level int
		want  string
	}{
		{"45", 0, "45"},
		{"45", 1, "[45-50]"},
		{"45", 2, "[40-50]"},
		{"45", 3, "[40-60]"},
		{"45", 4, "*"},
		{"25", 1, "[25-30]"},
		{"0", 1, "[0-5]"},
		{"invalid", 0, "invalid"},
		{"invalid", 1, "*"},
	}
	for _, tt := range tests {
		if got := AgeHierarchy(tt.value, tt.level); got != tt.want {
			t.Errorf("AgeHierarchy(%q, %d) = %q, want %q", tt.value, tt.level, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// ZipcodeHierarchy 测试（与 Python zipcode_hierarchy 对齐）
// ──────────────────────────────────────────────

func TestZipcodeHierarchy(t *testing.T) {
	// Python: zipcode_hierarchy("100000", 1) → "100***"
	// Python: zipcode_hierarchy("100000", 2) → "10****"
	tests := []struct {
		value string
		level int
		want  string
	}{
		{"100000", 0, "100000"},
		{"100000", 1, "100***"},
		{"100000", 2, "10****"},
		{"100000", 3, "1*****"},
		{"100000", 4, "*"},
		{"10", 1, "*"},
		{"10", 2, "10****"},
	}
	for _, tt := range tests {
		if got := ZipcodeHierarchy(tt.value, tt.level); got != tt.want {
			t.Errorf("ZipcodeHierarchy(%q, %d) = %q, want %q", tt.value, tt.level, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// GenderHierarchy 测试（与 Python gender_hierarchy 对齐）
// ──────────────────────────────────────────────

func TestGenderHierarchy(t *testing.T) {
	// Python: gender_hierarchy("男", 1) → "*"
	tests := []struct {
		value string
		level int
		want  string
	}{
		{"男", 0, "男"},
		{"男", 1, "*"},
		{"女", 0, "女"},
		{"女", 1, "*"},
		{"M", 0, "M"},
		{"M", 1, "*"},
	}
	for _, tt := range tests {
		if got := GenderHierarchy(tt.value, tt.level); got != tt.want {
			t.Errorf("GenderHierarchy(%q, %d) = %q, want %q", tt.value, tt.level, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// SalaryHierarchy 测试（与 Python salary_hierarchy 对齐）
// ──────────────────────────────────────────────

func TestSalaryHierarchy(t *testing.T) {
	// Python: salary_hierarchy("25000", 1) → "[25000K-25005K]"
	// Python: salary_hierarchy("25000", 2) → "[25000K-25010K]"
	tests := []struct {
		value string
		level int
		want  string
	}{
		{"25000", 0, "25000"},
		{"25000", 1, "[25000K-25005K]"},
		{"25000", 2, "[25000K-25010K]"},
		{"25000", 3, "[25000K-25050K]"},
		{"25000", 4, "*"},
		{"invalid", 0, "invalid"},
		{"invalid", 1, "*"},
	}
	for _, tt := range tests {
		if got := SalaryHierarchy(tt.value, tt.level); got != tt.want {
			t.Errorf("SalaryHierarchy(%q, %d) = %q, want %q", tt.value, tt.level, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// EducationHierarchy 测试（与 Python education_hierarchy 对齐）
// ──────────────────────────────────────────────

func TestEducationHierarchy(t *testing.T) {
	// Python: education_hierarchy("本科", 1) → "高等教育"
	tests := []struct {
		value string
		level int
		want  string
	}{
		{"本科", 0, "本科"},
		{"本科", 1, "高等教育"},
		{"硕士", 1, "高等教育"},
		{"博士", 1, "高等教育"},
		{"高中", 1, "基础教育"},
		{"初中", 1, "基础教育"},
		{"本科", 2, "*"},
		{"bachelor", 1, "高等教育"},
		{"master", 1, "高等教育"},
	}
	for _, tt := range tests {
		if got := EducationHierarchy(tt.value, tt.level); got != tt.want {
			t.Errorf("EducationHierarchy(%q, %d) = %q, want %q", tt.value, tt.level, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// ChooseLevel 测试（与 Python choose_level 对齐）
// ──────────────────────────────────────────────

func TestChooseLevel(t *testing.T) {
	// Python: choose_level(5, 4) → 1
	// Python: choose_level(10, 4) → 2
	tests := []struct {
		k, maxLevel int
		want        int
		wantErr     bool
	}{
		{5, 4, 1, false},
		{10, 4, 2, false},
		{2, 4, 1, false},
		{100, 4, 4, false},
		{1, 4, 0, true},
		{5, 0, 0, true},
	}
	for _, tt := range tests {
		got, err := ChooseLevel(tt.k, tt.maxLevel)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ChooseLevel(%d, %d) should return error", tt.k, tt.maxLevel)
			}
			continue
		}
		if err != nil {
			t.Errorf("ChooseLevel(%d, %d) unexpected error: %v", tt.k, tt.maxLevel, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ChooseLevel(%d, %d) = %d, want %d", tt.k, tt.maxLevel, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// AnonymizeRecord 测试
// ──────────────────────────────────────────────

func TestAnonymizeRecord(t *testing.T) {
	record := Record{
		"name":    "张三丰",
		"age":     "45",
		"gender":  "男",
		"zipcode": "100000",
		"salary":  "25000",
	}
	qiCols := []string{"age", "gender", "zipcode"}
	k := 5

	result, err := AnonymizeRecord(record, qiCols, nil, k)
	if err != nil {
		t.Fatalf("AnonymizeRecord() error: %v", err)
	}

	// age=45, k=5 → level=1 → "[45-50]"
	if result["age"] != "[45-50]" {
		t.Errorf("age = %q, want [45-50]", result["age"])
	}
	// gender, k=5 → level=1 → "*"
	if result["gender"] != "*" {
		t.Errorf("gender = %q, want *", result["gender"])
	}
	// zipcode=100000, k=5 → level=1 → "100***"
	if result["zipcode"] != "100***" {
		t.Errorf("zipcode = %q, want 100***", result["zipcode"])
	}
	// name should not be modified
	if result["name"] != "张三丰" {
		t.Errorf("name = %q, want 张三丰", result["name"])
	}
}

func TestAnonymizeRecord_CustomHierarchy(t *testing.T) {
	record := Record{
		"education": "本科",
	}
	qiCols := []string{"education"}
	hierarchies := map[string]HierarchyFunc{
		"education": EducationHierarchy,
	}
	k := 10

	result, err := AnonymizeRecord(record, qiCols, hierarchies, k)
	if err != nil {
		t.Fatalf("AnonymizeRecord() error: %v", err)
	}
	// education=本科, k=10 → level=2 → "*"
	if result["education"] != "*" {
		t.Errorf("education = %q, want *", result["education"])
	}
}

func TestAnonymizeRecord_InvalidK(t *testing.T) {
	record := Record{"age": "45"}
	_, err := AnonymizeRecord(record, []string{"age"}, nil, 1)
	if err == nil {
		t.Error("AnonymizeRecord(k=1) should return error")
	}
}

func TestAnonymizeRecordBatch(t *testing.T) {
	records := []Record{
		{"age": "25", "gender": "男"},
		{"age": "35", "gender": "女"},
	}
	qiCols := []string{"age", "gender"}
	results, err := AnonymizeRecordBatch(records, qiCols, nil, 5)
	if err != nil {
		t.Fatalf("AnonymizeRecordBatch() error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

// ──────────────────────────────────────────────
// Python 跨语言对齐测试
// ──────────────────────────────────────────────

func TestAlignPython_AgeHierarchy(t *testing.T) {
	// Python: age_hierarchy("45", 1) → "[45-50]"
	if got := AgeHierarchy("45", 1); got != "[45-50]" {
		t.Errorf("Go: AgeHierarchy(45, 1) = %q, Python: [45-50]", got)
	}
	// Python: age_hierarchy("45", 2) → "[40-50]"
	if got := AgeHierarchy("45", 2); got != "[40-50]" {
		t.Errorf("Go: AgeHierarchy(45, 2) = %q, Python: [40-50]", got)
	}
}

func TestAlignPython_ZipcodeHierarchy(t *testing.T) {
	// Python: zipcode_hierarchy("100000", 1) → "100***"
	if got := ZipcodeHierarchy("100000", 1); got != "100***" {
		t.Errorf("Go: ZipcodeHierarchy(100000, 1) = %q, Python: 100***", got)
	}
}

func TestAlignPython_GenderHierarchy(t *testing.T) {
	// Python: gender_hierarchy("男", 1) → "*"
	if got := GenderHierarchy("男", 1); got != "*" {
		t.Errorf("Go: GenderHierarchy(男, 1) = %q, Python: *", got)
	}
}

func TestAlignPython_SalaryHierarchy(t *testing.T) {
	// Python: salary_hierarchy("25000", 1) → "[25000K-25005K]"
	if got := SalaryHierarchy("25000", 1); got != "[25000K-25005K]" {
		t.Errorf("Go: SalaryHierarchy(25000, 1) = %q, Python: [25000K-25005K]", got)
	}
}

func TestAlignPython_EducationHierarchy(t *testing.T) {
	// Python: education_hierarchy("本科", 1) → "高等教育"
	if got := EducationHierarchy("本科", 1); got != "高等教育" {
		t.Errorf("Go: EducationHierarchy(本科, 1) = %q, Python: 高等教育", got)
	}
}

func TestAlignPython_ChooseLevel(t *testing.T) {
	// Python: choose_level(5, 4) → 1
	if got, _ := ChooseLevel(5, 4); got != 1 {
		t.Errorf("Go: ChooseLevel(5, 4) = %d, Python: 1", got)
	}
	// Python: choose_level(10, 4) → 2
	if got, _ := ChooseLevel(10, 4); got != 2 {
		t.Errorf("Go: ChooseLevel(10, 4) = %d, Python: 2", got)
	}
}

// ──────────────────────────────────────────────
// 基准测试
// ──────────────────────────────────────────────

func BenchmarkAgeHierarchy(b *testing.B) {
	for i := 0; i < b.N; i++ {
		AgeHierarchy("45", 2)
	}
}

func BenchmarkAnonymizeRecord(b *testing.B) {
	record := Record{"age": "45", "gender": "男", "zipcode": "100000"}
	qiCols := []string{"age", "gender", "zipcode"}
	for i := 0; i < b.N; i++ {
		_, _ = AnonymizeRecord(record, qiCols, nil, 5)
	}
}
