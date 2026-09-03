// Package fileparse provides unified file format parsers for CSV, JSON, and XLSX.
package fileparse

import (
	"fmt"
	"strings"
)

// DetectAndParse 根据文件名后缀自适应解析 CSV、JSON、XLSX 文件。
func DetectAndParse(filename string, data []byte) ([]map[string]string, []string, error) {
	name := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(name, ".csv"):
		return ParseCSV(data)
	case strings.HasSuffix(name, ".json"):
		return ParseJSON(data)
	case strings.HasSuffix(name, ".xlsx") || strings.HasSuffix(name, ".xls"):
		return ParseXLSX(data)
	default:
		return nil, nil, fmt.Errorf("unsupported file type: %s (supported: .csv, .json, .xlsx, .xls)", filename)
	}
}

// Parse 等同于 DetectAndParse。
func Parse(filename string, data []byte) ([]map[string]string, []string, error) {
	return DetectAndParse(filename, data)
}
