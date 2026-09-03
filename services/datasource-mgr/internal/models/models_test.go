// Package models_test contains unit tests verifying JSON serialization and deserialization of datasource models.
// Package models_test 包含 datasource-mgr 模块数据结构的 JSON 序列化与反序列化测试套件。
package models

import (
	"encoding/json"
	"testing"
)

// TestMockDataSourceSerialization verifies JSON serialization roundtrip for MockDataSource.
// TestMockDataSourceSerialization 验证 MockDataSource 结构体的 JSON 编解码往返一致性：
// 1. 构造包含 ID、名称、类型、描述、状态、行数及标签的 MockDataSource 对象；
// 2. 将其序列化为 JSON 字节流（json.Marshal）；
// 3. 将字节流反序列化恢复为新的 MockDataSource 实例（json.Unmarshal）；
// 4. 断言关键字段在序列化前后保持完全一致。
func TestMockDataSourceSerialization(t *testing.T) {
	ds := MockDataSource{
		ID:          "ds_yibao",
		Name:        "医保就医与结算模拟数据库",
		Type:        "file",
		Description: "模拟医保数据",
		Status:      "connected",
		RowCount:    50,
		Tags:        []string{"医保", "门诊"},
	}

	// 序列化
	data, err := json.Marshal(ds)
	if err != nil {
		t.Fatalf("failed to marshal MockDataSource: %v", err)
	}

	// 反序列化
	var parsed MockDataSource
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal MockDataSource: %v", err)
	}

	// 断言字段等价性
	if parsed.ID != ds.ID || parsed.RowCount != 50 || len(parsed.Tags) != 2 {
		t.Errorf("unmarshaled MockDataSource mismatch: %+v", parsed)
	}
}

// TestMetadataResponseSerialization verifies JSON serialization roundtrip for MetadataResponse.
// TestMetadataResponseSerialization 验证 Schema 元数据探查响应结构体 MetadataResponse 的 JSON 编解码：
// 1. 构造包含嵌套 TableMetadata 及字段清单 MetadataField 的 MetadataResponse 对象；
// 2. 执行序列化与反序列化；
// 3. 断言嵌套的表名、行数及字段属性数量在编解码后完好无损。
func TestMetadataResponseSerialization(t *testing.T) {
	meta := MetadataResponse{
		DataSourceID: "ds_yibao",
		Tables: []TableMetadata{
			{
				Name:     "yibao_settlement",
				RowCount: 50,
				Fields: []MetadataField{
					{Name: "person_id", Type: "string"},
					{Name: "amount", Type: "float"},
				},
			},
		},
		Via: "datasource-mgr",
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("failed to marshal MetadataResponse: %v", err)
	}

	var parsed MetadataResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal MetadataResponse: %v", err)
	}

	if parsed.DataSourceID != "ds_yibao" || len(parsed.Tables[0].Fields) != 2 {
		t.Errorf("unmarshaled MetadataResponse mismatch: %+v", parsed)
	}
}
