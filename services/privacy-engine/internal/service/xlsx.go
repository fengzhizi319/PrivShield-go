package service

import (
	"encoding/xml"
	"io"
	"strconv"
	"strings"

	"github.com/fengzhizi319/PrivShield-go/pkg/fileparse"
)

// maxUncompressedXMLSize 单个 XML 文件最大解压读取限制（256MB），防御解压炸弹拒绝服务攻击
const maxUncompressedXMLSize = 256 * 1024 * 1024

// ParseXLSXRecords 从 .xlsx 字节数据中解析结构化记录列表（委托给 pkg/fileparse）。
func ParseXLSXRecords(content []byte) ([]map[string]string, error) {
	return fileparse.ParseXLSXRecords(content)
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
	col := 0
	for _, r := range cellRef {
		if r >= 'A' && r <= 'Z' {
			col = col*26 + int(r-'A'+1)
		} else if r >= 'a' && r <= 'z' {
			col = col*26 + int(r-'a'+1)
		} else {
			break
		}
	}
	if col > 0 {
		return col - 1
	}
	return 0
}
