package fileparse

import (
	"bytes"
	"encoding/csv"
	"fmt"
)

// ParseCSV 把 CSV 原始字节解析为记录列表与列名顺序 (schema)。
// 自动剥离 UTF-8 BOM，缺失列自动以空字符串补齐。
func ParseCSV(data []byte) ([]map[string]string, []string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("CSV 解析失败: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("CSV 文件为空")
	}

	schema := rows[0]
	records := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		record := make(map[string]string, len(schema))
		for i, col := range schema {
			if i < len(row) {
				record[col] = row[i]
			} else {
				record[col] = ""
			}
		}
		records = append(records, record)
	}
	return records, schema, nil
}
