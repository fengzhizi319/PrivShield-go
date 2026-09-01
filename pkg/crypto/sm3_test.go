// Package crypto 测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证国密 SM3 哈希算法的实现正确性：
//  1. 【标准测试向量 1】：使用 GB/T 32907-2016 官方标准附录测试向量 1（输入 "abc"），
//     验证 SumSM3 一次性计算的摘要与标准期望值完全一致；
//  2. 【标准测试向量 2】：使用标准测试向量 2（输入 64 字节重复串 "abcdabcd..."），
//     验证多分组输入的摘要正确性；
//  3. 【hash.Hash 接口兼容性】：验证 NewSM3 返回的 hash.Hash 接口支持分块 Write，
//     分块写入 "a" + "bc" 的结果与一次性写入 "abc" 的摘要完全一致。
// ==============================================================================

package crypto

import (
	"encoding/hex"
	"testing"
)

// ──────────────────────────────────────────────
// 1. 标准测试向量验证
// ──────────────────────────────────────────────

// TestSM3_StandardVector1 使用 GB/T 32907-2016 标准测试向量 1 验证 SM3 实现。
// 执行逻辑：对输入 "abc" 计算 SumSM3 摘要，与标准期望值
// 66c7f0f4...8f4ba8e0 进行十六进制字符串比较。
func TestSM3_StandardVector1(t *testing.T) {
	// Standard Test Vector 1: "abc"
	// Expected: 66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0
	input := []byte("abc")
	expected := "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"

	digest := SumSM3(input)
	got := hex.EncodeToString(digest[:])

	if got != expected {
		t.Errorf("SumSM3(%q) = %s, want %s", input, got, expected)
	}
}

// TestSM3_StandardVector2 使用 GB/T 32907-2016 标准测试向量 2 验证 SM3 实现。
// 执行逻辑：对 64 字节重复输入 "abcdabcd..." 计算摘要，
// 与标准期望值 debe9ff9...9c0c5732 比较，验证多分组场景的正确性。
func TestSM3_StandardVector2(t *testing.T) {
	// Standard Test Vector 2: 64-byte repeated string
	// Expected: debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732
	input := []byte("abcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd")
	expected := "debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732"

	digest := SumSM3(input)
	got := hex.EncodeToString(digest[:])

	if got != expected {
		t.Errorf("SumSM3(vector2) = %s, want %s", got, expected)
	}
}

// ──────────────────────────────────────────────
// 2. hash.Hash 接口兼容性验证
// ──────────────────────────────────────────────

// TestSM3_HashInterface 验证 NewSM3 返回的 hash.Hash 接口分块写入的正确性。
// 执行逻辑：先 Write("a")，再 Write("bc")，然后调用 Sum(nil) 获取摘要，
// 断言分块写入的结果与一次性写入 "abc" 的标准摘要完全一致。
func TestSM3_HashInterface(t *testing.T) {
	h := NewSM3()
	h.Write([]byte("a"))
	h.Write([]byte("bc"))
	got := hex.EncodeToString(h.Sum(nil))
	expected := "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"

	if got != expected {
		t.Errorf("NewSM3().Write() = %s, want %s", got, expected)
	}
}
