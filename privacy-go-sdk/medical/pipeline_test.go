package medical

import (
	"strings"
	"testing"
)

// 整改前规格矩阵恰好等于契约字段集，故断言 FieldCount()==18/27。
// P0-2 之后矩阵口径扩展为「契约字段 ∪ 历史规格名 ∪ 标准规范 §6.2 扩展槽位」，
// 计数断言改为**契约字段逐个登记 + 登记项必须带合法处置算子**（覆盖率口径更严）。
func TestNewYibaoPipeline(t *testing.T) {
	p := NewYibaoPipeline()
	assertContractRegistered(t, p, YibaoContractFields, len(YibaoFields))
}

func TestNewKangyangPipeline(t *testing.T) {
	p := NewKangyangPipeline()
	assertContractRegistered(t, p, KangyangContractFields, len(KangyangFields)+len(VitalSignFields)+len(GovCareExtraFields))
}

// assertContractRegistered 断言契约字段全部在规格矩阵中显式登记且处置算子合法。
func assertContractRegistered(t *testing.T, p *Pipeline, contract []string, wantRegistered int) {
	t.Helper()
	if got := p.FieldCount(); got != wantRegistered {
		t.Errorf("registered spec fields = %d, want %d", got, wantRegistered)
	}
	for _, name := range contract {
		spec := p.GetFieldSpec(name)
		if spec == nil {
			t.Errorf("contract field %q is NOT registered in the field matrix (P0-2 default-deny violation)", name)
			continue
		}
		if !ValidTreatment(spec.Treatment) {
			t.Errorf("field %q treatment %q is not an implemented operator", name, spec.Treatment)
		}
	}
}

func TestSanitizeIdCard(t *testing.T) {
	p := NewYibaoPipeline()
	result := p.SanitizeField("id_card_no", "110101199001011234")
	expected := "110101********1234"
	if result != expected {
		t.Errorf("SanitizeField(id_card_no) = %q, want %q", result, expected)
	}
}

func TestSanitizePhone(t *testing.T) {
	p := NewYibaoPipeline()
	result := p.SanitizeField("phone", "13812345678")
	expected := "138****5678"
	if result != expected {
		t.Errorf("SanitizeField(phone) = %q, want %q", result, expected)
	}
}

func TestSanitizeName(t *testing.T) {
	p := NewYibaoPipeline()
	result := p.SanitizeField("name", "张三丰")
	expected := "张**丰" // 与 Python mask_name 对齐：3字→首+**+尾
	if result != expected {
		t.Errorf("SanitizeField(name) = %q, want %q", result, expected)
	}
}

func TestSanitizeAddress(t *testing.T) {
	p := NewYibaoPipeline()
	result := p.SanitizeField("address", "北京市朝阳区建国路88号")
	if len(result) == 0 {
		t.Error("SanitizeField(address) should not be empty")
	}
	// 前 6 个 rune 应保留（MaskAddress 保留前 6 个字符）
	runes := []rune(result)
	if string(runes[:6]) != "北京市朝阳区" {
		t.Errorf("SanitizeField(address) prefix = %q, want '北京市朝阳区'", string(runes[:6]))
	}
}

func TestSanitizeRecord(t *testing.T) {
	p := NewYibaoPipeline()
	record := map[string]string{
		"name":       "张三",
		"id_card_no": "110101199001011234",
		"phone":      "13812345678",
		"gender":     "男",
		"age":        "35",
	}

	result := p.SanitizeRecord(record)

	if result["name"] != "张*" {
		t.Errorf("name = %q, want '张*'", result["name"])
	}
	if result["id_card_no"] != "110101********1234" {
		t.Errorf("id_card_no = %q, want '110101********1234'", result["id_card_no"])
	}
	if result["phone"] != "138****5678" {
		t.Errorf("phone = %q, want '138****5678'", result["phone"])
	}
	if result["gender"] != "男" {
		t.Errorf("gender should be preserved, got %q", result["gender"])
	}
	// 整改前 age 走「低敏感保留」分支原样直传；P0-2 起按标准规范 §5.4
	// 施加年龄分段 K-匿名泛化（<60 岁 3 岁区间），旧断言编码的是宽松默认值，已收紧。
	if result["age"] != "[33-36)" {
		t.Errorf("age = %q, want K-anon band '[33-36)'", result["age"])
	}
}

func TestSanitizeBatch(t *testing.T) {
	p := NewYibaoPipeline()
	records := []map[string]string{
		{"name": "张三", "phone": "13812345678"},
		{"name": "李四", "phone": "13987654321"},
	}

	results := p.SanitizeBatch(records)
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestSanitizeEmpty(t *testing.T) {
	p := NewYibaoPipeline()
	result := p.SanitizeField("name", "")
	if result != "" {
		t.Errorf("SanitizeField(name, '') = %q, want ''", result)
	}
}

// TestSanitizeUnknownFieldDefaultDeny 未登记字段的默认拒绝断言（P0-2 核心）。
//
// 整改前：字段名含 "phone" 子串即命中启发式，按 MaskPhone 保留前 3 后 4；
// 整改后：字段名猜测层已整体删除，未列入矩阵的字段一律走具名默认拒绝策略
// （field_level_default_deny），遮蔽强度只增不减——保留字符数从 7 降到 1。
func TestSanitizeUnknownFieldDefaultDeny(t *testing.T) {
	const raw = "13812345678"
	p := NewYibaoPipeline()

	if p.GetFieldSpec("contact_phone") != nil {
		t.Fatal("contact_phone must NOT be registered for this test to be meaningful")
	}

	got := p.SanitizeField("contact_phone", raw)
	if got == raw {
		t.Fatalf("unlisted field emitted in the clear: %q", got)
	}
	want := "1**********"
	if got != want {
		t.Errorf("SanitizeField(contact_phone) = %q, want %q (default-deny mask)", got, want)
	}

	p.SetUnlistedFieldPolicy(UnlistedFieldDrop)
	if got := p.SanitizeField("contact_phone", raw); got != "" {
		t.Errorf("drop policy: SanitizeField(contact_phone) = %q, want empty", got)
	}

	// 无法识别的策略取值必须回落到限制性默认值，不得被重新放开为直传。
	p.SetUnlistedFieldPolicy(UnlistedFieldPolicy("keep"))
	if got := p.SanitizeField("contact_phone", raw); got == raw || got == "" {
		t.Errorf("invalid policy must fall back to mask, got %q", got)
	}
	if p.UnlistedFieldPolicy() != DefaultUnlistedFieldPolicy {
		t.Errorf("policy fallback = %q, want %q", p.UnlistedFieldPolicy(), DefaultUnlistedFieldPolicy)
	}
}

// TestUnlistedFieldClassifiedAsL3 未登记字段的定级不得低于 L3（敏感数据），
// 且报告标签必须可审计地标记为默认拒绝。
func TestUnlistedFieldClassifiedAsL3(t *testing.T) {
	p := NewKangyangPipeline()

	fc := p.ClassifyAndSanitizeField("mystery_column", "ABC123")
	if fc.Level != "L3" {
		t.Errorf("unlisted field level = %q, want L3 (默认拒绝最低敏档)", fc.Level)
	}
	if fc.SecurityTag != "UNLISTED_DEFAULT_DENY" {
		t.Errorf("security tag = %q, want UNLISTED_DEFAULT_DENY", fc.SecurityTag)
	}
	if fc.RuleMatched != UnlistedFieldPolicyName {
		t.Errorf("rule matched = %q, want %q", fc.RuleMatched, UnlistedFieldPolicyName)
	}
	if fc.SanitizedValue == "ABC123" {
		t.Errorf("unlisted field emitted in the clear: %q", fc.SanitizedValue)
	}

	// 就高原则：未登记字段的内容命中 L4/L5 词表时，等级只升不降。
	const risky = "2025-03-14 患者艾滋病史"
	esc := p.ClassifyAndSanitizeField("mystery_column", risky)
	if compareLevel(esc.Level, "L3") < 0 {
		t.Errorf("escalated level = %q, want >= L3", esc.Level)
	}
	if strings.Contains(esc.SanitizedValue, "艾滋病") || esc.SanitizedValue == risky {
		t.Errorf("unlisted field leaked raw or high-risk content: %q", esc.SanitizedValue)
	}
	if !strings.Contains(esc.RuleMatched, "HIGH_RISK_VALUE_ESCALATION") {
		t.Errorf("rule matched = %q, want value-level escalation marker", esc.RuleMatched)
	}
}

// TestKeepIsExplicitOnly 只有矩阵中显式登记为 keep 的字段才可能直传。
func TestKeepIsExplicitOnly(t *testing.T) {
	p := NewYibaoPipeline()
	if got := p.SanitizeField("gender", "男"); got != "男" {
		t.Errorf("registered keep field gender = %q, want 男", got)
	}
	// 同名脏数据（夹带高敏病种词）仍会被值层安全网抹平。
	if got := p.SanitizeField("gender", "男,艾滋病随访"); strings.Contains(got, "艾滋病") {
		t.Errorf("keep safety net failed: %q", got)
	}
}

func TestMaskClinicalText(t *testing.T) {
	text := "患者因持续性头痛伴恶心三天入院"
	result := maskClinicalText(text)
	runes := []rune(result)
	// 首 2 后 2 应保留
	if string(runes[:2]) != "患者" {
		t.Errorf("prefix = %q, want '患者'", string(runes[:2]))
	}
	if string(runes[len(runes)-2:]) != "入院" {
		t.Errorf("suffix = %q, want '入院'", string(runes[len(runes)-2:]))
	}
}

func TestGetFieldSpec(t *testing.T) {
	p := NewYibaoPipeline()
	spec := p.GetFieldSpec("id_card_no")
	if spec == nil {
		t.Fatal("GetFieldSpec(id_card_no) should not be nil")
	}
	if spec.Level != 5 {
		t.Errorf("id_card_no level = %d, want 5", spec.Level)
	}
	if spec.Category != CategoryIdentity {
		t.Errorf("id_card_no category = %q, want 'identity'", spec.Category)
	}
}
