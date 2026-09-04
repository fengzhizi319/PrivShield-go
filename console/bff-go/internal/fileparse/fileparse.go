// Package fileparse parses uploaded CSV/JSON data files into a unified records + schema structure.
// Package fileparse 把上传的 CSV/JSON 数据文件解析为统一的 records + schema 结构。
//
// The Go backend's /v1/upload endpoint receives files from the frontend, then uses this
// package to parse them into []map[string]string (each record, values unified as strings)
// and []string (column name order), for further construction of gRPC RecordEntry messages
// (whose Fields field is map[string]string).
// 控制台 Go 后端的 /v1/upload 端点收到前端上传的文件后，用本包解析为
// []map[string]string（每条记录，值统一为字符串）与 []string（列名顺序），
// 以便进一步构造 gRPC 的 RecordEntry（其 Fields 即 map[string]string）。
// Values are unified to strings to stay consistent with the agent's records API semantics.
// 值统一转字符串是为了与 agent 的 records 接口语义保持一致。
package fileparse

import (
	pkgfileparse "github.com/fengzhizi319/PrivShield-go/pkg/fileparse"
)

// ParseCSV parses CSV bytes into records and schema.
// ParseCSV 把 CSV 字节解析为 records 与 schema。
//
// The first row is treated as the header (schema); remaining rows are mapped
// to records by header column names. Rows with fewer fields than the header
// are padded with empty strings, allowing inconsistent field counts per row.
// 首行视为表头（schema），其余行按表头列名映射为 record；
// 某行字段数不足时以空字符串补齐，允许各行字段数不一致。
//
// Parameters / 参数：
//   - data: raw CSV file content bytes / 原始 CSV 文件内容字节
//
// Returns / 返回：
//   - []map[string]string: parsed records (column→value) / 解析后的记录
//   - []string: schema (ordered column names) / 列名顺序
//   - error: parse failure / 解析失败错误
func ParseCSV(data []byte) ([]map[string]string, []string, error) {
	return pkgfileparse.ParseCSV(data)
}

// ParseJSON parses a JSON record array (list of objects) into records and schema.
// ParseJSON 把 JSON 记录数组（list of objects）解析为 records 与 schema。
//
// Schema collects all keys appearing across all records, sorted alphabetically
// to ensure deterministic output (Go map iteration is unordered).
// schema 取所有记录中出现过的键并按字母序排序，保证结果确定（Go map 遍历无序）；
// Each value is uniformly converted to string (numbers, booleans, null, nested
// objects all have corresponding handling).
// 每个值统一转换为字符串（数字、布尔、null、嵌套对象等均有对应处理）。
//
// Parameters / 参数：
//   - data: raw JSON file content (must be an array of objects)
//     原始 JSON 文件内容（必须为对象数组）
//
// Returns / 返回：
//   - []map[string]string: parsed records / 解析后的记录
//   - []string: sorted schema (all unique keys) / 排序后的列名
//   - error: parse failure / 解析失败错误
func ParseJSON(data []byte) ([]map[string]string, []string, error) {
	return pkgfileparse.ParseJSON(data)
}
