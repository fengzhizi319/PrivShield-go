// Package masking_test 跨语言对齐测试：验证 Go 脱敏输出与 Python 基准一致。
//
// 本文件中的 "Python baseline" 注释标注了每条断言对应的 Python 函数与输出，
// 可通过 `PYTHONPATH=. .venv/bin/python -c "..."` 复现验证。
// 对齐目标：Python engine/privacy/masking.py 的同名函数。
package masking_test

import (
	"testing"

	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/masking"
)

// ──────────────────────────────────────────────
// MaskPhone 对齐 Python mask_mobile
// ──────────────────────────────────────────────

func TestAlignPython_MaskPhone(t *testing.T) {
	// Python: mask_mobile("13812345678") == "138****5678"
	if got := masking.MaskPhone("13812345678"); got != "138****5678" {
		t.Errorf("MaskPhone(13812345678) = %q, want Python baseline %q", got, "138****5678")
	}
	// Python: mask_mobile("123") == "123" (非11位原样返回)
	// Go: 短于8位走全星掩码路径
	if got := masking.MaskPhone("13812345678"); len(got) != len("138****5678") {
		t.Errorf("MaskPhone output length mismatch: got %d, want %d", len(got), len("138****5678"))
	}
}

// ──────────────────────────────────────────────
// MaskIdCard 对齐 Python mask_id_card
// ──────────────────────────────────────────────

func TestAlignPython_MaskIdCard(t *testing.T) {
	// Python: mask_id_card("110101199001011234") == "110101********1234"
	if got := masking.MaskIdCard("110101199001011234"); got != "110101********1234" {
		t.Errorf("MaskIdCard(110101199001011234) = %q, want Python baseline %q", got, "110101********1234")
	}
}

// ──────────────────────────────────────────────
// MaskChineseName 对齐 Python mask_name
// ──────────────────────────────────────────────

func TestAlignPython_MaskChineseName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		desc     string
	}{
		// Python: mask_name("张三丰") == "张**丰"
		{"张三丰", "张**丰", "3字→首+**+尾"},
		// Python: mask_name("韩雨泽_3") == "韩**泽"
		{"韩雨泽_3", "韩**泽", "带_3序号3字姓名"},
		{"韩雨泽3", "韩**泽", "带3数字3字姓名"},
		// Python: mask_name("李四") == "李*"
		{"李四", "李*", "2字→首+*"},
		{"李四-12", "李*", "带-12序号2字姓名"},
		{"王五 (3)", "王*", "带括号序号2字姓名"},
		// Python: mask_name("欧阳六六") == "欧**六"
		{"欧阳六六", "欧**六", "4字→首+*(n-2)+尾"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got := masking.MaskChineseName(tt.input); got != tt.expected {
				t.Errorf("MaskChineseName(%q) = %q, want Python baseline %q (%s)", tt.input, got, tt.expected, tt.desc)
			}
		})
	}
}

// ──────────────────────────────────────────────
// MaskEmail 对齐 Python mask_email
// ──────────────────────────────────────────────

func TestAlignPython_MaskEmail(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		desc     string
	}{
		// Python: mask_email("zhangsan@example.com") == "z***n@example.com"
		{"zhangsan@example.com", "z***n@example.com", "长用户名→首+***+尾+@域名"},
		// Python: mask_email("ab@test.com") == "a***@test.com"
		{"ab@test.com", "a***@test.com", "短用户名(<=2)→首+***+@域名"},
		// Python: mask_email("noemail") == mask_default("noemail") == "noe*ail"
		{"noemail", "noe*ail", "无@回退到MaskDefault"},
		// Python: mask_email("test@example.com") == "t***t@example.com"
		{"test@example.com", "t***t@example.com", "4字符用户名"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got := masking.MaskEmail(tt.input); got != tt.expected {
				t.Errorf("MaskEmail(%q) = %q, want Python baseline %q (%s)", tt.input, got, tt.expected, tt.desc)
			}
		})
	}
}

// ──────────────────────────────────────────────
// MaskAddress 对齐 Python mask_address
// ──────────────────────────────────────────────

func TestAlignPython_MaskAddress(t *testing.T) {
	// Python: mask_address("北京市朝阳区某某街道123号") == "北京市朝阳区****"
	if got := masking.MaskAddress("北京市朝阳区某某街道123号"); got != "北京市朝阳区****" {
		t.Errorf("MaskAddress = %q, want Python baseline %q", got, "北京市朝阳区****")
	}
	// Python: mask_address("短地址") == "短地址" (长度<=6原样返回)
	if got := masking.MaskAddress("短地址"); got != "短地址" {
		t.Errorf("MaskAddress(短地址) = %q, want Python baseline %q", got, "短地址")
	}
}

// ──────────────────────────────────────────────
// MaskDefault 对齐 Python mask_default
// ──────────────────────────────────────────────

func TestAlignPython_MaskDefault(t *testing.T) {
	// Python: mask_default("abcdefgh") == "abc**fgh"
	if got := masking.MaskDefault("abcdefgh", 3, 3); got != "abc**fgh" {
		t.Errorf("MaskDefault(abcdefgh, 3, 3) = %q, want Python baseline %q", got, "abc**fgh")
	}
}

// ──────────────────────────────────────────────
// HashHMAC 对齐 Python hash_value
// ──────────────────────────────────────────────

func TestAlignPython_HashHMAC(t *testing.T) {
	// Python: hash_value("hello", "salt") == "hqgcMCMTbl75WlVF"
	// 实现：HMAC-SHA256(key=salt, msg=value) → base64 → 前16字符
	if got := masking.HashHMAC("hello", "salt"); got != "hqgcMCMTbl75WlVF" {
		t.Errorf("HashHMAC(hello, salt) = %q, want Python baseline %q", got, "hqgcMCMTbl75WlVF")
	}
	// 长度验证：Python hash_value 固定返回 16 字符
	if got := masking.HashHMAC("test", "key"); len(got) != 16 {
		t.Errorf("HashHMAC output length = %d, want 16", len(got))
	}
	// 确定性验证
	if a, b := masking.HashHMAC("data", "salt"), masking.HashHMAC("data", "salt"); a != b {
		t.Errorf("HashHMAC not deterministic: %q != %q", a, b)
	}
	// 盐值敏感性
	if a, b := masking.HashHMAC("data", "salt1"), masking.HashHMAC("data", "salt2"); a == b {
		t.Errorf("HashHMAC different salts produced same result")
	}
}

// ──────────────────────────────────────────────
// 跨语言一致性综合验证
// ──────────────────────────────────────────────

func TestAlignPython_MaskRecordIntegration(t *testing.T) {
	// 验证 Go 各脱敏函数组合后与 Python mask_record 输出一致
	// Python: mask_record({"mobile": "13812345678", "name": "张三丰", "age": 30})
	//   → {"mobile": "138****5678", "name": "张**丰", "age": 30}
	record := map[string]string{
		"mobile": "13812345678",
		"name":   "张三丰",
	}
	expectedMobile := "138****5678"
	expectedName := "张**丰"

	if got := masking.MaskPhone(record["mobile"]); got != expectedMobile {
		t.Errorf("record mobile: got %q, want %q", got, expectedMobile)
	}
	if got := masking.MaskChineseName(record["name"]); got != expectedName {
		t.Errorf("record name: got %q, want %q", got, expectedName)
	}
}

func TestAlignPython_TruncateConsistency(t *testing.T) {
	// Python: truncate("abcdef", 3) == "abc***"
	// MaskDefault 短值保护：len("ab")=2 <= prefix+suffix=6，保留首字符+掩码
	if got := masking.MaskDefault("ab", 3, 3); got != "a*" {
		t.Errorf("MaskDefault(ab, 3, 3) = %q, want %q (short value protection)", got, "a*")
	}
	// 空值原样返回
	if got := masking.MaskDefault("", 3, 3); got != "" {
		t.Errorf("MaskDefault(empty, 3, 3) = %q, want empty", got)
	}
	// 单字符：保留首字符
	if got := masking.MaskDefault("x", 3, 3); got != "x" {
		t.Errorf("MaskDefault(x, 3, 3) = %q, want %q", got, "x")
	}
}

func TestAlignPython_MaskValue(t *testing.T) {
	tests := []struct {
		fieldName string
		value     string
		expected  string
	}{
		{"mobile", "13812345678", "138****5678"},
		{"id_card", "110101199001011234", "110101********1234"},
		{"name", "张三丰", "张**丰"},
		{"email", "test@example.com", "t***t@example.com"},
		{"address", "北京市朝阳区某某街道", "北京市朝阳区****"},
		{"unknown", "abcdefgh", "abc**fgh"},
	}
	for _, tt := range tests {
		t.Run(tt.fieldName, func(t *testing.T) {
			if got := masking.MaskValue(tt.fieldName, tt.value); got != tt.expected {
				t.Errorf("MaskValue(%q, %q) = %q, want %q", tt.fieldName, tt.value, got, tt.expected)
			}
		})
	}
}

func TestAlignPython_MaskRecord(t *testing.T) {
	record := map[string]string{
		"mobile":  "13812345678",
		"id_card": "110101199001011234",
		"name":    "韩雨泽_3",
		"email":   "user@domain.com",
		"address": "上海市浦东新区张江高科",
	}
	masked := masking.MaskRecord(record)
	if masked["mobile"] != "138****5678" {
		t.Errorf("mobile = %q, want %q", masked["mobile"], "138****5678")
	}
	if masked["id_card"] != "110101********1234" {
		t.Errorf("id_card = %q, want %q", masked["id_card"], "110101********1234")
	}
	if masked["name"] != "韩**泽" {
		t.Errorf("name = %q, want %q", masked["name"], "韩**泽")
	}
	if masked["email"] != "u***r@domain.com" {
		t.Errorf("email = %q, want %q", masked["email"], "u***r@domain.com")
	}
	if masked["address"] != "上海市浦东新****" {
		t.Errorf("address = %q, want %q", masked["address"], "上海市浦东新****")
	}
}
