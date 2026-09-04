// operators_align_test.go — 缺失算子测试 + Python 跨语言对齐验证
package dynclassification

import (
	"sort"
	"testing"
)

// ──────────────────────────────────────────────
// 身份证校验算子测试（与 Python _validate_id_card 对齐）
// ──────────────────────────────────────────────

func TestIdCardChecksum_Valid(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"123", false},
		{"110101199003074518", false},
		{"11010119900307451X", false},
	}
	for _, tt := range tests {
		if got := validateIdCard(tt.value); got != tt.want {
			t.Errorf("validateIdCard(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestIdCardChecksum_ComputeValid(t *testing.T) {
	// 110101199003074178: checksum digit = '8'
	valid := "110101199003074178"
	if !validateIdCard(valid) {
		t.Errorf("validateIdCard(%q) should be valid", valid)
	}
	invalid := "110101199003074177"
	if validateIdCard(invalid) {
		t.Errorf("validateIdCard(%q) should be invalid", invalid)
	}
}

func TestIdCardChecksumOperator_Type(t *testing.T) {
	op := &IdCardChecksumOperator{}
	if op.Type() != OpIdCardChecksum {
		t.Errorf("Type() = %v, want %v", op.Type(), OpIdCardChecksum)
	}
}

// ──────────────────────────────────────────────
// 上海医保卡校验算子测试（与 Python _validate_medical_card 对齐）
// ──────────────────────────────────────────────

func TestMedicalCardChecksum(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"12345678", false},
		{"1234567890", false},
		{"12345678a", false},
		{"123456780", false},
	}
	for _, tt := range tests {
		if got := validateMedicalCard(tt.value); got != tt.want {
			t.Errorf("validateMedicalCard(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestMedicalCardChecksum_ComputeValid(t *testing.T) {
	valid := "123456789"
	if !validateMedicalCard(valid) {
		t.Errorf("validateMedicalCard(%q) should be valid", valid)
	}
	invalid := "123456780"
	if validateMedicalCard(invalid) {
		t.Errorf("validateMedicalCard(%q) should be invalid", invalid)
	}
}

// ──────────────────────────────────────────────
// ICD-10 区间判定算子测试（与 Python _normalize_icd10 / _in_icd10_interval 对齐）
// ──────────────────────────────────────────────

func TestNormalizeIcd10(t *testing.T) {
	tests := []struct {
		input  string
		letter byte
		num    int
		isNil  bool
	}{
		{"A00", 'A', 0, false},
		{"A00.0", 'A', 0, false},
		{"B20", 'B', 20, false},
		{"B20.5", 'B', 20, false},
		{"a50", 'A', 50, false},
		{"123", 0, 0, true},
		{"", 0, 0, true},
		{"AB12", 0, 0, true},
	}
	for _, tt := range tests {
		got := normalizeIcd10(tt.input)
		if tt.isNil {
			if got != nil {
				t.Errorf("normalizeIcd10(%q) = %+v, want nil", tt.input, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("normalizeIcd10(%q) = nil, want (%c, %d)", tt.input, tt.letter, tt.num)
			continue
		}
		if got.letter != tt.letter || got.num != tt.num {
			t.Errorf("normalizeIcd10(%q) = %+v, want (%c, %d)", tt.input, got, tt.letter, tt.num)
		}
	}
}

func TestInIcd10Interval(t *testing.T) {
	code := &icd10Code{'A', 50}
	if !inIcd10Interval(code, "A00", "B99") {
		t.Error("A50 should be in [A00, B99]")
	}
	code2 := &icd10Code{'C', 34}
	if inIcd10Interval(code2, "A00", "B99") {
		t.Error("C34 should NOT be in [A00, B99]")
	}
	code3 := &icd10Code{'A', 0}
	if !inIcd10Interval(code3, "A00", "A00") {
		t.Error("A00 should be in [A00, A00]")
	}
	code4 := &icd10Code{'A', 1}
	if inIcd10Interval(code4, "A00", "A00") {
		t.Error("A01 should NOT be in [A00, A00]")
	}
}

func TestIcd10RangeOperator_Match(t *testing.T) {
	op := &Icd10RangeOperator{start: "A00", end: "B99"}
	if !op.Match("", "A50") {
		t.Error("A50 should match [A00, B99]")
	}
	if op.Match("", "C34") {
		t.Error("C34 should NOT match [A00, B99]")
	}
	if op.Match("", "invalid") {
		t.Error("invalid should NOT match")
	}
}

func TestIcd10RangeOperator_ValidateOnly(t *testing.T) {
	op := &Icd10RangeOperator{}
	if !op.Match("", "A50") {
		t.Error("A50 is valid ICD-10")
	}
	if op.Match("", "123") {
		t.Error("123 is NOT valid ICD-10")
	}
}

// ──────────────────────────────────────────────
// Luhn 校验算子测试（与 Python _validate_luhn 对齐）
// ──────────────────────────────────────────────

func TestValidateLuhn(t *testing.T) {
	tests := []struct {
		value    string
		min, max int
		want     bool
	}{
		{"4532015112830366", 13, 19, true},
		{"4532015112830367", 13, 19, false},
		{"", 13, 19, false},
		{"123", 13, 19, false},
		{"12345678901234567890", 13, 19, false},
		{"453201511283036a", 13, 19, false},
	}
	for _, tt := range tests {
		if got := validateLuhn(tt.value, tt.min, tt.max); got != tt.want {
			t.Errorf("validateLuhn(%q, %d, %d) = %v, want %v", tt.value, tt.min, tt.max, got, tt.want)
		}
	}
}

func TestLuhnChecksumOperator_Match(t *testing.T) {
	op := &LuhnChecksumOperator{minLength: 13, maxLength: 19}
	if !op.Match("", "4532015112830366") {
		t.Error("4532015112830366 should pass Luhn")
	}
	if op.Match("", "4532015112830367") {
		t.Error("4532015112830367 should fail Luhn")
	}
}

// ──────────────────────────────────────────────
// 长度范围匹配算子测试
// ──────────────────────────────────────────────

func TestLengthRangeOperator(t *testing.T) {
	op := &LengthRangeOperator{minLen: 3, maxLen: 10}
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"ab", false},
		{"abc", true},
		{"hello", true},
		{"1234567890", true},
		{"12345678901", false},
	}
	for _, tt := range tests {
		if got := op.Match("", tt.value); got != tt.want {
			t.Errorf("LengthRange.Match(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestLengthRangeOperator_NoUpper(t *testing.T) {
	op := &LengthRangeOperator{minLen: 5, maxLen: -1}
	if !op.Match("", "hello world this is a long string") {
		t.Error("should match long string with no upper bound")
	}
	if op.Match("", "hi") {
		t.Error("should not match short string")
	}
}

// ──────────────────────────────────────────────
// IP 地址匹配算子测试（与 Python ip_address_matcher 对齐）
// ──────────────────────────────────────────────

func TestIsIpAddress(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"192.168.1.1", true},
		{"0.0.0.0", true},
		{"255.255.255.255", true},
		{"256.1.1.1", false},
		{"1.2.3", false},
		{"2001:0db8:85a3:0000:0000:8a2e:0370:7334", true},
		{"not-an-ip", false},
	}
	for _, tt := range tests {
		if got := isIpAddress(tt.value); got != tt.want {
			t.Errorf("isIpAddress(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// MAC 地址匹配算子测试（与 Python mac_address_matcher 对齐）
// ──────────────────────────────────────────────

func TestIsMacAddress(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"AA:BB:CC:DD:EE:FF", true},
		{"aa:bb:cc:dd:ee:ff", true},
		{"AA-BB-CC-DD-EE-FF", true},
		{"AA:BB:CC:DD:EE", false},
		{"AA:BB:CC:DD:EE:GG", false},
		{"not-a-mac", false},
	}
	for _, tt := range tests {
		if got := isMacAddress(tt.value); got != tt.want {
			t.Errorf("isMacAddress(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// 中文姓名匹配算子测试（与 Python chinese_name_matcher 对齐）
// ──────────────────────────────────────────────

func TestIsChineseName(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"张", false},
		{"张三", true},
		{"张三丰", true},
		{"欧阳三丰", true},
		{"张三丰啊", true},
		{"张三丰啊吧", false},
		{"John", false},
		{"张A三", false},
	}
	for _, tt := range tests {
		if got := isChineseName(tt.value); got != tt.want {
			t.Errorf("isChineseName(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// 邮箱匹配算子测试（与 Python email_matcher 对齐）
// ──────────────────────────────────────────────

func TestIsEmail(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"user@example.com", true},
		{"user.name+tag@example.co.uk", true},
		{"user@localhost", false},
		{"@example.com", false},
		{"user@", false},
		{"not-an-email", false},
	}
	for _, tt := range tests {
		if got := isEmail(tt.value); got != tt.want {
			t.Errorf("isEmail(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// 注册表集成测试
// ──────────────────────────────────────────────

func TestOperatorRegistry_NewOperators(t *testing.T) {
	r := NewOperatorRegistry()
	expected := []OperatorType{
		OpIdCardChecksum, OpMedicalCard, OpIcd10Range,
		OpLuhnChecksum, OpLengthRange, OpIpAddress,
		OpMacAddress, OpChineseName, OpEmail,
	}
	registered := r.ListOperators()
	regSet := make(map[OperatorType]bool, len(registered))
	for _, rt := range registered {
		regSet[rt] = true
	}
	for _, et := range expected {
		if !regSet[et] {
			t.Errorf("operator %q not registered", et)
		}
	}
}

func TestOperatorRegistry_CreateNewOperators(t *testing.T) {
	r := NewOperatorRegistry()
	tests := []struct {
		opType OperatorType
		args   []string
	}{
		{OpIdCardChecksum, nil},
		{OpMedicalCard, nil},
		{OpIcd10Range, []string{"A00", "B99"}},
		{OpLuhnChecksum, []string{"13", "19"}},
		{OpLengthRange, []string{"3", "10"}},
		{OpIpAddress, nil},
		{OpMacAddress, nil},
		{OpChineseName, nil},
		{OpEmail, nil},
	}
	for _, tt := range tests {
		op := r.Create(tt.opType, tt.args...)
		if op == nil {
			t.Errorf("Create(%q) returned nil", tt.opType)
			continue
		}
		if op.Type() != tt.opType {
			t.Errorf("Create(%q).Type() = %q, want %q", tt.opType, op.Type(), tt.opType)
		}
	}
}

func TestOperatorRegistry_TotalCount(t *testing.T) {
	r := NewOperatorRegistry()
	ops := r.ListOperators()
	if len(ops) != 15 {
		t.Errorf("expected 15 operators, got %d: %v", len(ops), ops)
	}
}

// ──────────────────────────────────────────────
// Python 跨语言对齐测试
// ──────────────────────────────────────────────

func TestAlignPython_LuhnChecksum(t *testing.T) {
	if !validateLuhn("4532015112830366", 13, 19) {
		t.Error("Go: Luhn(4532015112830366) should be True (Python: True)")
	}
	if validateLuhn("4532015112830367", 13, 19) {
		t.Error("Go: Luhn(4532015112830367) should be False (Python: False)")
	}
}

func TestAlignPython_Icd10Normalize(t *testing.T) {
	c1 := normalizeIcd10("A00")
	if c1 == nil || c1.letter != 'A' || c1.num != 0 {
		t.Errorf("Go: normalizeIcd10(A00) = %+v, Python: (A, 0)", c1)
	}
	c2 := normalizeIcd10("A00.0")
	if c2 == nil || c2.letter != 'A' || c2.num != 0 {
		t.Errorf("Go: normalizeIcd10(A00.0) = %+v, Python: (A, 0)", c2)
	}
	c3 := normalizeIcd10("123")
	if c3 != nil {
		t.Errorf("Go: normalizeIcd10(123) = %+v, Python: None", c3)
	}
}

func TestAlignPython_IpAddress(t *testing.T) {
	if !isIpAddress("192.168.1.1") {
		t.Error("Go: isIpAddress(192.168.1.1) should be True (Python: True)")
	}
	if isIpAddress("256.1.1.1") {
		t.Error("Go: isIpAddress(256.1.1.1) should be False (Python: False)")
	}
}

func TestAlignPython_MacAddress(t *testing.T) {
	if !isMacAddress("AA:BB:CC:DD:EE:FF") {
		t.Error("Go: isMacAddress(AA:BB:CC:DD:EE:FF) should be True (Python: True)")
	}
	if isMacAddress("AA:BB:CC:DD:EE") {
		t.Error("Go: isMacAddress(AA:BB:CC:DD:EE) should be False (Python: False)")
	}
}

func TestAlignPython_ChineseName(t *testing.T) {
	if !isChineseName("张三") {
		t.Error("Go: isChineseName(张三) should be True (Python: True)")
	}
	if isChineseName("John") {
		t.Error("Go: isChineseName(John) should be False (Python: False)")
	}
}

func TestAlignPython_Email(t *testing.T) {
	if !isEmail("user@example.com") {
		t.Error("Go: isEmail(user@example.com) should be True (Python: True)")
	}
	if isEmail("not-an-email") {
		t.Error("Go: isEmail(not-an-email) should be False (Python: False)")
	}
}

// ──────────────────────────────────────────────
// 基准测试
// ──────────────────────────────────────────────

func BenchmarkValidateIdCard(b *testing.B) {
	for i := 0; i < b.N; i++ {
		validateIdCard("110101199003074178")
	}
}

func BenchmarkValidateLuhn(b *testing.B) {
	for i := 0; i < b.N; i++ {
		validateLuhn("4532015112830366", 13, 19)
	}
}

func BenchmarkIsIpAddress(b *testing.B) {
	for i := 0; i < b.N; i++ {
		isIpAddress("192.168.1.1")
	}
}

func BenchmarkIsChineseName(b *testing.B) {
	for i := 0; i < b.N; i++ {
		isChineseName("张三丰")
	}
}

// ──────────────────────────────────────────────
// 辅助测试
// ──────────────────────────────────────────────

func TestSortOperatorTypes(t *testing.T) {
	r := NewOperatorRegistry()
	ops := r.ListOperators()
	sort.Slice(ops, func(i, j int) bool { return ops[i] < ops[j] })
	for i := 1; i < len(ops); i++ {
		if ops[i] == ops[i-1] {
			t.Errorf("duplicate operator type: %q", ops[i])
		}
	}
}
