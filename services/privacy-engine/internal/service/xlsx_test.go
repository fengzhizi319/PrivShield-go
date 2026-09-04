package service

import (
	"archive/zip"
	"bytes"
	"testing"
)

func createTestXLSX(t *testing.T) []byte {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// 1. sharedStrings.xml
	sstXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="4" uniqueCount="4">
	<si><t>name</t></si>
	<si><t>phone</t></si>
	<si><t>张三</t></si>
	<si><t>李四</t></si>
</sst>`
	f1, err := zw.Create("xl/sharedStrings.xml")
	if err != nil {
		t.Fatalf("create sharedStrings: %v", err)
	}
	f1.Write([]byte(sstXML))

	// 2. sheet1.xml
	sheetXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
	<sheetData>
		<row r="1">
			<c r="A1" t="s"><v>0</v></c>
			<c r="B1" t="s"><v>1</v></c>
		</row>
		<row r="2">
			<c r="A2" t="s"><v>2</v></c>
			<c r="B2"><v>13812345678</v></c>
		</row>
		<row r="3">
			<c r="A3" t="s"><v>3</v></c>
			<c r="B3"><v>13987654321</v></c>
		</row>
	</sheetData>
</worksheet>`
	f2, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatalf("create sheet1: %v", err)
	}
	f2.Write([]byte(sheetXML))

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	return buf.Bytes()
}

func TestParseXLSXRecords(t *testing.T) {
	content := createTestXLSX(t)

	records, err := ParseXLSXRecords(content)
	if err != nil {
		t.Fatalf("ParseXLSXRecords failed: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("records len = %d, want 2", len(records))
	}

	if records[0]["name"] != "张三" || records[0]["phone"] != "13812345678" {
		t.Errorf("record 0 = %v, want name:张三, phone:13812345678", records[0])
	}
	if records[1]["name"] != "李四" || records[1]["phone"] != "13987654321" {
		t.Errorf("record 1 = %v, want name:李四, phone:13987654321", records[1])
	}
}

func TestProcessFileXLSX(t *testing.T) {
	content := createTestXLSX(t)
	svc := newTestService(t)

	res, err := svc.ProcessFile(content, "test.xlsx", "mask_dataframe", map[string]interface{}{
		"columns": []interface{}{"phone", "name"},
	})
	if err != nil {
		t.Fatalf("ProcessFile xlsx failed: %v", err)
	}

	if res["rows_in"] != 2 || res["rows_out"] != 2 {
		t.Errorf("rows_in = %v, rows_out = %v, want 2", res["rows_in"], res["rows_out"])
	}

	maskedData, ok := res["result"].([]map[string]string)
	if !ok || len(maskedData) != 2 {
		t.Fatalf("maskedData invalid: %v", res["result"])
	}

	if maskedData[0]["name"] != "张*" {
		t.Errorf("masked name = %q, want '张*'", maskedData[0]["name"])
	}
	if maskedData[0]["phone"] != "138****5678" {
		t.Errorf("masked phone = %q, want '138****5678'", maskedData[0]["phone"])
	}
}
