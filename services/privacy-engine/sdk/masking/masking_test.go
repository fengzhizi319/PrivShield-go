package masking

import (
	"testing"
)

func TestMaskIdCard(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard 18-digit ID card",
			input:    "110101199001011234",
			expected: "110101********1234",
		},
		{
			name:     "ID card ending with X",
			input:    "11010119900101123X",
			expected: "110101********123X",
		},
		{
			name:     "non-standard length",
			input:    "12345678901234567890",
			expected: "1234************7890",
		},
		{
			name:     "short input",
			input:    "12345",
			expected: "*****",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskIdCard(tt.input)
			if result != tt.expected {
				t.Errorf("MaskIdCard(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestMaskPhone(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard mobile number",
			input:    "13812345678",
			expected: "138****5678",
		},
		{
			name:     "with +86 prefix",
			input:    "+8613812345678",
			expected: "+86 138****5678",
		},
		{
			name:     "short input",
			input:    "12345",
			expected: "*****",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskPhone(tt.input)
			if result != tt.expected {
				t.Errorf("MaskPhone(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestMaskBankCard(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard bank card",
			input:    "6222021234567890123",
			expected: "622202*********0123",
		},
		{
			name:     "short input",
			input:    "12345",
			expected: "*****",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskBankCard(tt.input)
			if result != tt.expected {
				t.Errorf("MaskBankCard(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestMaskChineseName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "two-character name",
			input:    "张三",
			expected: "张*",
		},
		{
			name:     "three-character name",
			input:    "张三丰",
			expected: "张**丰", // 与 Python mask_name 对齐：3字→首+**+尾
		},
		{
			name:     "four-character name",
			input:    "欧阳三丰",
			expected: "欧**丰", // 与 Python mask_name 对齐：4字→首+*(n-2)+尾
		},
		{
			name:     "single character",
			input:    "张",
			expected: "*",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskChineseName(tt.input)
			if result != tt.expected {
				t.Errorf("MaskChineseName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestMaskEmail(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard email long local",
			input:    "zhangsan@example.com",
			expected: "z***n@example.com", // 与 Python mask_email 对齐：首+***+尾+@域名
		},
		{
			name:     "test email",
			input:    "test@example.com",
			expected: "t***t@example.com", // 与 Python mask_email 对齐
		},
		{
			name:     "short local part 2 chars",
			input:    "ab@test.com",
			expected: "a***@test.com", // 短用户名(<=2)→首+***+@域名
		},
		{
			name:     "no at sign fallback",
			input:    "noemail",
			expected: "noe*ail", // 无 @ 回退到 MaskDefault
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskEmail(tt.input)
			if result != tt.expected {
				t.Errorf("MaskEmail(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestMaskAddress(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "long address",
			input:    "北京市朝阳区某某街道123号",
			expected: "北京市朝阳区****", // 与 Python mask_address 对齐：前6字符+固定****
		},
		{
			name:     "short address unchanged",
			input:    "短地址",
			expected: "短地址", // 长度<=6原样返回
		},
		{
			name:     "exactly 6 chars unchanged",
			input:    "北京市朝阳区",
			expected: "北京市朝阳区", // 恰好6字符原样返回
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskAddress(tt.input)
			if result != tt.expected {
				t.Errorf("MaskAddress(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestHashHMAC(t *testing.T) {
	// 与 Python hash_value 对齐：HMAC-SHA256(salt, value) → base64 → 前16字符
	result := HashHMAC("hello", "salt")
	if len(result) != 16 {
		t.Errorf("HashHMAC should return 16 chars, got %d: %q", len(result), result)
	}
	// 确定性：相同输入产生相同输出
	if HashHMAC("hello", "salt") != result {
		t.Errorf("HashHMAC should be deterministic")
	}
	// 盐值敏感性：不同盐值产生不同输出
	if HashHMAC("hello", "other") == result {
		t.Errorf("HashHMAC with different salt should produce different result")
	}
	// 精确值校验（G-07 合规修复：HMAC 底层杂凑已从 SHA-256 迁移至 SM3）
	expected := "SKTdBFvNvKAtTphi"
	if result != expected {
		t.Errorf("HashHMAC(hello, salt) = %q, want %q (SM3 baseline)", result, expected)
	}
}
