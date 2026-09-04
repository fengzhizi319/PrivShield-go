package qol

import (
	"testing"
)

func TestGenerateMedicalDecoy(t *testing.T) {
	decoy := GenerateMedicalDecoy()
	if decoy == "" {
		t.Error("GenerateMedicalDecoy should not return empty string")
	}
}

func TestGenerateGeneralDecoy(t *testing.T) {
	decoy := GenerateGeneralDecoy()
	if decoy == "" {
		t.Error("GenerateGeneralDecoy should not return empty string")
	}
}

func TestInjectDecoys(t *testing.T) {
	realQuery := "HIV检测"
	numDecoys := 5

	queries, realIdx := InjectDecoys(realQuery, numDecoys, "medical")

	if len(queries) != numDecoys+1 {
		t.Errorf("expected %d queries, got %d", numDecoys+1, len(queries))
	}

	if realIdx < 0 || realIdx >= len(queries) {
		t.Errorf("realIdx = %d, out of range", realIdx)
	}

	if queries[realIdx] != realQuery {
		t.Errorf("queries[%d] = %q, want %q", realIdx, queries[realIdx], realQuery)
	}
}

func TestInjectDecoysGeneral(t *testing.T) {
	realQuery := "天气预报"
	numDecoys := 3

	queries, realIdx := InjectDecoys(realQuery, numDecoys, "general")

	if len(queries) != numDecoys+1 {
		t.Errorf("expected %d queries, got %d", numDecoys+1, len(queries))
	}

	if queries[realIdx] != realQuery {
		t.Errorf("queries[%d] = %q, want %q", realIdx, queries[realIdx], realQuery)
	}
}

func TestGenerateDecoySet(t *testing.T) {
	decoys := GenerateDecoySet(10, "medical")
	if len(decoys) != 10 {
		t.Errorf("expected 10 decoys, got %d", len(decoys))
	}
	for i, d := range decoys {
		if d == "" {
			t.Errorf("decoy[%d] is empty", i)
		}
	}
}

func TestFisherYatesShuffle(t *testing.T) {
	// 验证 shuffle 保留了所有元素
	items := []string{"a", "b", "c", "d", "e"}
	original := make([]string, len(items))
	copy(original, items)

	realIdx := fisherYatesShuffle(items)

	if realIdx < 0 || realIdx >= len(items) {
		t.Errorf("realIdx = %d, out of range", realIdx)
	}

	// 验证 "a" 在 realIdx 位置
	if items[realIdx] != "a" {
		t.Errorf("items[%d] = %q, want 'a'", realIdx, items[realIdx])
	}

	// 验证长度不变
	if len(items) != len(original) {
		t.Errorf("length changed: %d vs %d", len(items), len(original))
	}
}
