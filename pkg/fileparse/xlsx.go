package fileparse

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxUncompressedXMLSize 单个 XML 文件最大解压读取限制（256MB），防御解压炸弹拒绝服务攻击
const maxUncompressedXMLSize = 256 * 1024 * 1024

// ParseXLSXRecords 从 .xlsx 字节数据中解析结构化记录列表（纯 Go 实现，无 CGO 依赖）。
func ParseXLSXRecords(content []byte) ([]map[string]string, error) {
	records, _, err := ParseXLSX(content)
	return records, err
}

// ParseXLSX 从 .xlsx 字节数据中解析结构化记录列表与表头 schema。
func ParseXLSX(content []byte) ([]map[string]string, []string, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, nil, fmt.Errorf("open xlsx zip: %w", err)
	}

	var sharedStrings []string
	var sheetFile *zip.File

	for _, f := range zr.File {
		if f.Name == "xl/sharedStrings.xml" {
			rc, err := f.Open()
			if err == nil {
				sharedStrings, _ = parseSharedStrings(io.LimitReader(rc, maxUncompressedXMLSize))
				rc.Close()
			}
		} else if f.Name == "xl/worksheets/sheet1.xml" || (sheetFile == nil && strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml")) {
			sheetFile = f
		}
	}

	if sheetFile == nil {
		return nil, nil, fmt.Errorf("sheet1.xml not found in xlsx archive")
	}

	rc, err := sheetFile.Open()
	if err != nil {
		return nil, nil, fmt.Errorf("open sheet xml: %w", err)
	}
	defer rc.Close()

	rows, err := parseSheetData(io.LimitReader(rc, maxUncompressedXMLSize), sharedStrings)
	if err != nil {
		return nil, nil, fmt.Errorf("parse sheet xml: %w", err)
	}

	if len(rows) < 1 {
		return nil, nil, fmt.Errorf("xlsx sheet is empty")
	}

	headers := rows[0]
	schema := make([]string, 0, len(headers))
	for _, h := range headers {
		if h != "" {
			schema = append(schema, h)
		}
	}

	records := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		rec := make(map[string]string, len(headers))
		hasAny := false
		for i, h := range headers {
			if h == "" {
				continue
			}
			if i < len(row) {
				rec[h] = row[i]
				if row[i] != "" {
					hasAny = true
				}
			} else {
				rec[h] = ""
			}
		}
		if hasAny {
			records = append(records, rec)
		}
	}

	return records, schema, nil
}

type stringInterner struct {
	pool map[string]string
}

func newStringInterner() *stringInterner {
	return &stringInterner{pool: make(map[string]string, 1024)}
}

func (si *stringInterner) Intern(s string) string {
	if s == "" {
		return ""
	}
	if len(s) > 128 {
		return s
	}
	if existing, ok := si.pool[s]; ok {
		return existing
	}
	si.pool[s] = s
	return s
}

func parseSharedStrings(r io.Reader) ([]string, error) {
	var sst struct {
		XMLName xml.Name `xml:"sst"`
		SI      []struct {
			T string `xml:"t"`
			R []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}

	if err := xml.NewDecoder(r).Decode(&sst); err != nil {
		return nil, err
	}

	interner := newStringInterner()
	result := make([]string, len(sst.SI))
	for i, si := range sst.SI {
		if si.T != "" {
			result[i] = interner.Intern(si.T)
		} else if len(si.R) > 0 {
			var sb strings.Builder
			for _, r := range si.R {
				sb.WriteString(r.T)
			}
			result[i] = interner.Intern(sb.String())
		}
	}
	return result, nil
}

func parseSheetData(r io.Reader, sharedStrings []string) ([][]string, error) {
	var ws struct {
		XMLName   xml.Name `xml:"worksheet"`
		SheetData struct {
			Row []struct {
				R int `xml:"r,attr"`
				C []struct {
					R  string `xml:"r,attr"`
					T  string `xml:"t,attr"`
					V  string `xml:"v"`
					IS struct {
						T string `xml:"t"`
					} `xml:"is"`
				} `xml:"c"`
			} `xml:"row"`
		} `xml:"sheetData"`
	}

	if err := xml.NewDecoder(r).Decode(&ws); err != nil {
		return nil, err
	}

	interner := newStringInterner()
	rows := make([][]string, 0, len(ws.SheetData.Row))

	for _, row := range ws.SheetData.Row {
		rowVals := make([]string, 0, len(row.C))
		for _, c := range row.C {
			colIdx := colNameToIndex(c.R)
			for len(rowVals) <= colIdx {
				rowVals = append(rowVals, "")
			}

			val := c.V
			if c.T == "s" {
				idx, err := strconv.Atoi(c.V)
				if err == nil && idx >= 0 && idx < len(sharedStrings) {
					val = sharedStrings[idx]
				}
			} else if c.T == "inlineStr" {
				val = c.IS.T
			}
			rowVals[colIdx] = interner.Intern(val)
		}
		rows = append(rows, rowVals)
	}
	return rows, nil
}

func colNameToIndex(cellRef string) int {
	colLetters := ""
	for _, ch := range cellRef {
		if ch >= 'A' && ch <= 'Z' {
			colLetters += string(ch)
		} else {
			break
		}
	}
	if colLetters == "" {
		return 0
	}
	idx := 0
	for _, ch := range colLetters {
		idx = idx*26 + int(ch-'A'+1)
	}
	return idx - 1
}
