package dynclassification

import (
	"sort"
	"strings"
	"testing"
)

// ──────────────────────────────────────────────
// 基础功能测试
// ──────────────────────────────────────────────

func TestAhoCorasick_SinglePattern(t *testing.T) {
	ac := NewAhoCorasick()
	ac.AddPattern("艾滋病")
	ac.Build()

	matches := ac.MatchString("该患者确诊为艾滋病晚期")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d: %+v", len(matches), matches)
	}
	if matches[0].Pattern != "艾滋病" {
		t.Errorf("expected pattern '艾滋病', got %q", matches[0].Pattern)
	}
}

func TestAhoCorasick_MultiplePatterns(t *testing.T) {
	ac := NewAhoCorasick()
	ac.AddPattern("艾滋病", "梅毒", "乙肝")
	ac.Build()

	text := "患者同时患有乙肝和梅毒，需要隔离治疗"
	matches := ac.MatchString(text)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d: %+v", len(matches), matches)
	}

	patterns := make([]string, len(matches))
	for i, m := range matches {
		patterns[i] = m.Pattern
	}
	sort.Strings(patterns)
	if patterns[0] != "乙肝" || patterns[1] != "梅毒" {
		t.Errorf("expected [乙肝, 梅毒], got %v", patterns)
	}
}

func TestAhoCorasick_OverlappingPatterns(t *testing.T) {
	ac := NewAhoCorasick()
	ac.AddPattern("AB", "ABC", "BC")
	ac.Build()

	matches := ac.MatchString("ABC")
	if len(matches) < 2 {
		t.Fatalf("expected at least 2 matches for overlapping patterns, got %d: %+v", len(matches), matches)
	}

	found := map[string]bool{}
	for _, m := range matches {
		found[m.Pattern] = true
	}
	// "AB" at pos 0, "BC" at pos 1, "ABC" at pos 0
	if !found["AB"] {
		t.Error("missing match for 'AB'")
	}
	if !found["BC"] {
		t.Error("missing match for 'BC'")
	}
	if !found["ABC"] {
		t.Error("missing match for 'ABC'")
	}
}

func TestAhoCorasick_NoMatch(t *testing.T) {
	ac := NewAhoCorasick()
	ac.AddPattern("艾滋病", "梅毒")
	ac.Build()

	matches := ac.MatchString("普通感冒患者")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}

func TestAhoCorasick_EmptyText(t *testing.T) {
	ac := NewAhoCorasick()
	ac.AddPattern("test")
	ac.Build()

	matches := ac.MatchString("")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for empty text, got %d", len(matches))
	}
}

func TestAhoCorasick_EmptyPattern(t *testing.T) {
	ac := NewAhoCorasick()
	ac.AddPattern("") // should be ignored
	ac.Build()

	if ac.PatternCount() != 0 {
		t.Errorf("empty pattern should be ignored, got count=%d", ac.PatternCount())
	}
}

func TestAhoCorasick_Contains(t *testing.T) {
	ac := NewAhoCorasick()
	ac.AddPattern("高血压", "糖尿病")
	ac.Build()

	if !ac.Contains("患者有高血压病史") {
		t.Error("expected Contains=true for text with '高血压'")
	}
	if ac.Contains("患者体温正常") {
		t.Error("expected Contains=false for text without patterns")
	}
}

func TestAhoCorasick_NotBuilt(t *testing.T) {
	ac := NewAhoCorasick()
	ac.AddPattern("test")
	// 不调用 Build

	matches := ac.MatchString("test")
	if len(matches) != 0 {
		t.Error("MatchString should return empty before Build")
	}
	if ac.Contains("test") {
		t.Error("Contains should return false before Build")
	}
}

// ──────────────────────────────────────────────
// 中文医学词库场景测试
// ──────────────────────────────────────────────

func TestAhoCorasick_MedicalTermBank(t *testing.T) {
	// 模拟高敏医学词库
	l5Terms := []string{
		"艾滋病", "HIV", "梅毒", "乙肝", "丙肝",
		"肺结核", "非典", "新冠", "埃博拉", "鼠疫",
	}
	l4Terms := []string{
		"高血压", "糖尿病", "冠心病", "脑卒中",
		"恶性肿瘤", "白血病", "淋巴瘤",
	}

	ac := NewAhoCorasick()
	ac.AddPattern(append(l5Terms, l4Terms...)...)
	ac.Build()

	tests := []struct {
		text      string
		expectHit bool
		maxLevel  string // "L5" or "L4"
	}{
		{"患者确诊为HIV携带者", true, "L5"},
		{"患者有高血压和糖尿病史", true, "L4"},
		{"普通感冒，无特殊病史", false, ""},
		{"患者家属要求保密", false, ""},
		{"肺结核患者需要隔离", true, "L5"},
	}

	for _, tt := range tests {
		matches := ac.MatchString(tt.text)
		if tt.expectHit && len(matches) == 0 {
			t.Errorf("text=%q: expected match but got none", tt.text)
		}
		if !tt.expectHit && len(matches) > 0 {
			t.Errorf("text=%q: expected no match but got %d", tt.text, len(matches))
		}
	}
}

func TestAhoCorasick_PositionAccuracy(t *testing.T) {
	ac := NewAhoCorasick()
	ac.AddPattern("乙肝")
	ac.Build()

	text := "患者有乙肝病史"
	matches := ac.MatchString(text)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}

	// "乙肝" 在 "患者有" 之后，字节偏移 = 3*3 = 9 (UTF-8 中文每字 3 字节)
	m := matches[0]
	if m.Pos != 9 {
		t.Errorf("expected Pos=9, got %d", m.Pos)
	}
	if m.Len != 6 {
		t.Errorf("expected Len=6, got %d", m.Len)
	}

	// 验证提取的子串
	extracted := text[m.Pos : m.Pos+m.Len]
	if extracted != "乙肝" {
		t.Errorf("extracted text mismatch: got %q", extracted)
	}
}

// ──────────────────────────────────────────────
// AcAutomatonOperator 集成测试
// ──────────────────────────────────────────────

func TestAcAutomatonOperator_Match(t *testing.T) {
	termsMap := map[string]string{
		"艾滋病": "L5",
		"梅毒":  "L5",
		"高血压": "L4",
		"糖尿病": "L4",
		"感冒":  "L2",
	}

	op := NewAcAutomatonOperator(termsMap)

	tests := []struct {
		text       string
		wantMatch  bool
		wantLevel  string
		wantMinHit int // 最少匹配数
	}{
		{"患者确诊艾滋病", true, "L5", 1},
		{"患者有高血压和糖尿病", true, "L4", 2},
		{"普通感冒", true, "L2", 1},
		{"体温正常", false, "L1", 0},
	}

	for _, tt := range tests {
		matched, level, terms := op.MatchDetail(tt.text)
		if matched != tt.wantMatch {
			t.Errorf("text=%q: matched=%v, want %v", tt.text, matched, tt.wantMatch)
		}
		if level != tt.wantLevel {
			t.Errorf("text=%q: level=%q, want %q", tt.text, level, tt.wantLevel)
		}
		if len(terms) < tt.wantMinHit {
			t.Errorf("text=%q: got %d terms, want at least %d", tt.text, len(terms), tt.wantMinHit)
		}
	}
}

func TestAcAutomatonOperator_CaseInsensitive(t *testing.T) {
	termsMap := map[string]string{
		"HIV":  "L5",
		"AIDS": "L5",
	}

	op := NewAcAutomatonOperator(termsMap)

	matched, level, terms := op.MatchDetail("Patient tested positive for hiv")
	if !matched {
		t.Error("expected case-insensitive match for 'hiv'")
	}
	if level != "L5" {
		t.Errorf("expected L5, got %q", level)
	}
	if len(terms) == 0 {
		t.Error("expected at least 1 matched term")
	}
}

func TestAcAutomatonOperator_Type(t *testing.T) {
	op := NewAcAutomatonOperator(map[string]string{"test": "L1"})
	if op.Type() != OpACAutomaton {
		t.Errorf("expected type %q, got %q", OpACAutomaton, op.Type())
	}
}

// ──────────────────────────────────────────────
// 算子注册表集成测试
// ──────────────────────────────────────────────

func TestOperatorRegistry_AcAutomaton(t *testing.T) {
	registry := NewOperatorRegistry()

	// 注册 AC 自动机工厂
	registry.Register(OpACAutomaton, func(args ...string) Operator {
		if len(args) < 2 || len(args)%2 != 0 {
			return nil
		}
		termsMap := make(map[string]string)
		for i := 0; i < len(args); i += 2 {
			termsMap[args[i]] = args[i+1]
		}
		return NewAcAutomatonOperator(termsMap)
	})

	// 创建算子
	op := registry.Create(OpACAutomaton, "艾滋病", "L5", "高血压", "L4")
	if op == nil {
		t.Fatal("expected non-nil operator")
	}
	if op.Type() != OpACAutomaton {
		t.Errorf("expected type %q, got %q", OpACAutomaton, op.Type())
	}

	// 验证匹配
	if !op.Match("diagnosis", "患者有艾滋病史") {
		t.Error("expected match for '艾滋病'")
	}
	if op.Match("diagnosis", "普通感冒") {
		t.Error("expected no match for '普通感冒'")
	}
}

// ──────────────────────────────────────────────
// 性能基准
// ──────────────────────────────────────────────

func BenchmarkAhoCorasick_Match100Terms(b *testing.B) {
	// 构建 100 个模式串
	terms := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		terms = append(terms, "医学词条"+strings.Repeat("X", i%10))
	}

	ac := NewAhoCorasick()
	ac.AddPattern(terms...)
	ac.Build()

	text := strings.Repeat("这是一段普通的中文文本，不包含任何敏感信息。", 100)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ac.MatchString(text)
	}
}

func BenchmarkAhoCorasick_Contains100Terms(b *testing.B) {
	terms := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		terms = append(terms, "医学词条"+strings.Repeat("X", i%10))
	}

	ac := NewAhoCorasick()
	ac.AddPattern(terms...)
	ac.Build()

	text := strings.Repeat("这是一段普通的中文文本，不包含任何敏感信息。", 100)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ac.Contains(text)
	}
}
