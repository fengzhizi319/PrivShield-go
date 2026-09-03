package fileparse

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestParseCSV(t *testing.T) {
	csvContent := []byte("\xef\xbb\xbfname,age,city\nAlice,30,Beijing\nBob,25\n")
	records, schema, err := ParseCSV(csvContent)
	if err != nil {
		t.Fatalf("ParseCSV failed: %v", err)
	}
	if len(schema) != 3 {
		t.Errorf("expected 3 schema cols, got %v", schema)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[1]["city"] != "" {
		t.Errorf("expected padded empty city, got %q", records[1]["city"])
	}
}

func TestParseJSON(t *testing.T) {
	jsonContent := []byte(`[{"name":"Alice","age":30,"active":true},{"name":"Bob","age":25,"extra":{"key":"val"}}]`)
	records, schema, err := ParseJSON(jsonContent)
	if err != nil {
		t.Fatalf("ParseJSON failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0]["active"] != "true" {
		t.Errorf("expected active=true, got %q", records[0]["active"])
	}
	if len(schema) < 3 {
		t.Errorf("expected schema with keys, got %v", schema)
	}
}

func TestParseXLSX(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	sst := `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="4" uniqueCount="4">
		<si><t>Name</t></si>
		<si><t>Age</t></si>
		<si><t>Alice</t></si>
		<si><t>Bob</t></si>
	</sst>`
	w, _ := zw.Create("xl/sharedStrings.xml")
	_, _ = w.Write([]byte(sst))

	sheet := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
		<sheetData>
			<row r="1">
				<c r="A1" t="s"><v>0</v></c>
				<c r="B1" t="s"><v>1</v></c>
			</row>
			<row r="2">
				<c r="A2" t="s"><v>2</v></c>
				<c r="B2"><v>30</v></c>
			</row>
			<row r="3">
				<c r="A3" t="s"><v>3</v></c>
				<c r="B3"><v>25</v></c>
			</row>
		</sheetData>
	</worksheet>`
	w, _ = zw.Create("xl/worksheets/sheet1.xml")
	_, _ = w.Write([]byte(sheet))
	_ = zw.Close()

	records, schema, err := ParseXLSX(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseXLSX failed: %v", err)
	}
	if len(schema) != 2 || schema[0] != "Name" || schema[1] != "Age" {
		t.Errorf("unexpected schema: %v", schema)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0]["Name"] != "Alice" || records[0]["Age"] != "30" {
		t.Errorf("unexpected record 0: %v", records[0])
	}
}

func TestDetectAndParse(t *testing.T) {
	_, _, err := DetectAndParse("data.unsupported", []byte("123"))
	if err == nil {
		t.Fatal("expected error on unsupported file type")
	}
}
