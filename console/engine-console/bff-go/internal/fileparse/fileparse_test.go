package fileparse

import (
	"reflect"
	"testing"
)

func TestParseCSV(t *testing.T) {
	data := []byte("email,phone,name\nalice@example.com,13800138000,Alice\nbob@example.com,13900139000,Bob\n")
	records, schema, err := ParseCSV(data)
	if err != nil {
		t.Fatalf("ParseCSV failed: %v", err)
	}
	if !reflect.DeepEqual(schema, []string{"email", "phone", "name"}) {
		t.Fatalf("unexpected schema: %v", schema)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0]["email"] != "alice@example.com" || records[0]["name"] != "Alice" {
		t.Fatalf("unexpected first record: %v", records[0])
	}
}

func TestParseCSVShortRowPadded(t *testing.T) {
	// 第二行字段数不足，应以空串补齐。
	data := []byte("a,b,c\n1,2\n")
	records, _, err := ParseCSV(data)
	if err != nil {
		t.Fatalf("ParseCSV failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0]["a"] != "1" || records[0]["b"] != "2" || records[0]["c"] != "" {
		t.Fatalf("unexpected padded record: %v", records[0])
	}
}

func TestParseCSVEmpty(t *testing.T) {
	if _, _, err := ParseCSV([]byte("")); err == nil {
		t.Fatalf("expected error for empty CSV")
	}
}

func TestParseJSON(t *testing.T) {
	data := []byte(`[{"email":"alice@example.com","age":30},{"email":"bob@example.com","age":25,"vip":true}]`)
	records, schema, err := ParseJSON(data)
	if err != nil {
		t.Fatalf("ParseJSON failed: %v", err)
	}
	// schema 按字母序排序：age, email, vip
	if !reflect.DeepEqual(schema, []string{"age", "email", "vip"}) {
		t.Fatalf("unexpected schema: %v", schema)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0]["email"] != "alice@example.com" || records[0]["age"] != "30" {
		t.Fatalf("unexpected first record: %v", records[0])
	}
	// 布尔值转字符串
	if records[1]["vip"] != "true" {
		t.Fatalf("expected vip=true, got %q", records[1]["vip"])
	}
}

func TestParseJSONInvalid(t *testing.T) {
	// 非记录数组应报错。
	if _, _, err := ParseJSON([]byte(`{"a":1}`)); err == nil {
		t.Fatalf("expected error for non-array JSON")
	}
}
