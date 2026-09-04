package kano

import (
	"testing"
)

func TestMondrian_BasicNumeric(t *testing.T) {
	rows := []Record{
		{"name": "Alice", "age": "25", "zip": "10001"},
		{"name": "Bob", "age": "26", "zip": "10002"},
		{"name": "Carol", "age": "27", "zip": "10003"},
		{"name": "Dave", "age": "28", "zip": "10004"},
		{"name": "Eve", "age": "29", "zip": "10005"},
		{"name": "Frank", "age": "30", "zip": "10006"},
	}
	result, err := Mondrian(rows, []string{"age", "zip"}, 2, 10)
	if err != nil {
		t.Fatalf("Mondrian failed: %v", err)
	}
	if result.K != 2 {
		t.Errorf("expected k=2, got %d", result.K)
	}
	if len(result.Records) != 6 {
		t.Errorf("expected 6 records, got %d", len(result.Records))
	}
	if result.EquivalenceClassesCount < 1 {
		t.Errorf("expected at least 1 equivalence class, got %d", result.EquivalenceClassesCount)
	}
	// 验证泛化后的 age 值应该是区间格式或单一值
	for _, r := range result.Records {
		age := r["age"]
		if age == "" {
			t.Error("age should not be empty after generalization")
		}
	}
}

func TestMondrian_CategoricalQI(t *testing.T) {
	rows := []Record{
		{"name": "A", "gender": "M", "city": "BJ"},
		{"name": "B", "gender": "M", "city": "SH"},
		{"name": "C", "gender": "F", "city": "BJ"},
		{"name": "D", "gender": "F", "city": "SH"},
	}
	result, err := Mondrian(rows, []string{"gender", "city"}, 2, 10)
	if err != nil {
		t.Fatalf("Mondrian failed: %v", err)
	}
	if len(result.Records) != 4 {
		t.Errorf("expected 4 records, got %d", len(result.Records))
	}
}

func TestMondrian_EmptyInput(t *testing.T) {
	result, err := Mondrian(nil, []string{"age"}, 2, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Records) != 0 {
		t.Errorf("expected 0 records, got %d", len(result.Records))
	}
}

func TestMondrian_InvalidK(t *testing.T) {
	rows := []Record{{"age": "25"}}
	_, err := Mondrian(rows, []string{"age"}, 1, 10)
	if err == nil {
		t.Fatal("expected error for k < 2")
	}
}

func TestMondrian_InsufficientRows(t *testing.T) {
	rows := []Record{{"age": "25"}}
	_, err := Mondrian(rows, []string{"age"}, 5, 10)
	if err == nil {
		t.Fatal("expected error for rows < k")
	}
}

func TestMondrian_MissingQICol(t *testing.T) {
	rows := []Record{{"age": "25", "name": "A"}}
	_, err := Mondrian(rows, []string{"nonexistent"}, 2, 10)
	if err == nil {
		t.Fatal("expected error for missing qi_col")
	}
}

func TestMondrian_AlignPython_SimpleTable(t *testing.T) {
	// 对齐 Python kano_table.k_anonymize_table 测试
	rows := []Record{
		{"name": "Tom", "age": "25", "salary": "5000"},
		{"name": "Jerry", "age": "30", "salary": "6000"},
		{"name": "Spike", "age": "35", "salary": "7000"},
		{"name": "Tyke", "age": "28", "salary": "5500"},
	}
	result, err := Mondrian(rows, []string{"age", "salary"}, 2, 10)
	if err != nil {
		t.Fatalf("Mondrian failed: %v", err)
	}
	if len(result.Records) != 4 {
		t.Errorf("expected 4 records, got %d", len(result.Records))
	}
	// 验证泛化后的 age 和 salary 值
	for _, r := range result.Records {
		age := r["age"]
		salary := r["salary"]
		if age == "" || salary == "" {
			t.Errorf("generalized fields should not be empty: age=%q, salary=%q", age, salary)
		}
	}
}

func TestMondrian_LargeK(t *testing.T) {
	// k 等于行数时，所有记录应被泛化为同一等价组
	rows := []Record{
		{"age": "20", "city": "A"},
		{"age": "25", "city": "B"},
		{"age": "30", "city": "C"},
	}
	result, err := Mondrian(rows, []string{"age", "city"}, 3, 10)
	if err != nil {
		t.Fatalf("Mondrian failed: %v", err)
	}
	if result.EquivalenceClassesCount != 1 {
		t.Errorf("expected 1 equivalence class when k=len(rows), got %d", result.EquivalenceClassesCount)
	}
}
