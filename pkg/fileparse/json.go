package fileparse

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ParseJSON 把 JSON 记录数组解析为记录列表与字母序排列的 schema。
func ParseJSON(data []byte) ([]map[string]string, []string, error) {
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("JSON 解析失败（需为记录数组）: %w", err)
	}

	seen := make(map[string]bool)
	for _, obj := range raw {
		for k := range obj {
			seen[k] = true
		}
	}

	schema := make([]string, 0, len(seen))
	for k := range seen {
		schema = append(schema, k)
	}
	sort.Strings(schema)

	records := make([]map[string]string, 0, len(raw))
	for _, obj := range raw {
		record := make(map[string]string, len(obj))
		for k, v := range obj {
			record[k] = toString(v)
		}
		records = append(records, record)
	}
	return records, schema, nil
}

func toString(val any) string {
	if val == nil {
		return ""
	}
	if s, ok := val.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", val)
}
