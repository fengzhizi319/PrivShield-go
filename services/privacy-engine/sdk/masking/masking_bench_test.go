package masking

import (
	"testing"
)

// ──────────────────────────────────────────────
// 基准测试：字段掩码
// ──────────────────────────────────────────────

func BenchmarkMaskIdCard(b *testing.B) {
	id := "110101199003072345"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		MaskIdCard(id)
	}
}

func BenchmarkMaskPhone(b *testing.B) {
	phone := "13812345678"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		MaskPhone(phone)
	}
}

func BenchmarkMaskBankCard(b *testing.B) {
	card := "6222021234567890123"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		MaskBankCard(card)
	}
}

func BenchmarkMaskChineseName(b *testing.B) {
	name := "张三丰"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		MaskChineseName(name)
	}
}

func BenchmarkMaskEmail(b *testing.B) {
	email := "zhangsan@example.com"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		MaskEmail(email)
	}
}

func BenchmarkMaskAddress(b *testing.B) {
	addr := "北京市朝阳区建国路88号SOHO现代城A座1201室"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		MaskAddress(addr)
	}
}

func BenchmarkHashHMAC(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		HashHMAC("110101199003072345", "test-salt-value")
	}
}

// ──────────────────────────────────────────────
// 批量脱敏基准测试
// ──────────────────────────────────────────────

func BenchmarkMaskRecord10Fields(b *testing.B) {
	record := map[string]string{
		"id_card_no":   "110101199003072345",
		"phone":        "13812345678",
		"name":         "张三丰",
		"email":        "zhangsan@example.com",
		"address":      "北京市朝阳区建国路88号",
		"bank_card":    "6222021234567890123",
		"diagnosis":    "2型糖尿病",
		"doctor":       "李医生",
		"department":   "内分泌科",
		"admission_no": "ADM20260101001",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, v := range record {
			MaskIdCard(v)
		}
	}
}
