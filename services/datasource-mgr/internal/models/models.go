// Package models defines data structures for the mock datasource-mgr module.
// Package models 定义模拟数据源模块（datasource-mgr）的核心数据模型与响应结构体。
//
// 该模块统一规定了模拟数据源元数据定义、通用数据分页查询结果、元数据模式（Schema）描述以及
// 连通性测试报告等数据载体，服务于 HTTP REST 控制器与 gRPC 服务实现层。
package models

// MockDataSource represents a registered mock data source for dev/testing.
// MockDataSource 结构体描述了一个注册在系统中的模拟数据源实体元数据。
type MockDataSource struct {
	ID           string   `json:"id"`            // 数据源唯一标识符（如 "ds_yibao", "ds_kangyang", "ds_mock3", "ds_mock4"）
	DatasourceID string   `json:"datasource_id"` // canonical 数据源 ID（与 id 同值双写）
	APICode      string   `json:"api_code,omitempty"`
	Name         string   `json:"name"`        // 数据源中文展示名称（如 "医保就医与结算模拟数据库 (yibao.csv)"）
	Type         string   `json:"type"`        // 数据源底层存储类型（"file" 表示本地 CSV 文件，"mock" 表示内存硬编码数据）
	Description  string   `json:"description"` // 数据源详细业务描述与用途说明
	Status       string   `json:"status"`      // 数据源当前连接状态（如 "connected"）
	RowCount     int      `json:"row_count"`   // 数据源包含的模拟数据总行数
	Tags         []string `json:"tags"`        // 业务与敏感分类标签列表（如 ["医保", "结算流水", "敏感数据"]）
}

// DataSourceListResponse is the response for listing mock datasources.
// DataSourceListResponse 结构体用于承载数据源资产目录列表查询的统一响应。
type DataSourceListResponse struct {
	Total       int              `json:"total"`       // 系统中已注册的数据源总数
	DataSources []MockDataSource `json:"datasources"` // 数据源元数据列表
	Via         string           `json:"via"`         // 服务标识
}

// MetadataField describes a single column's metadata.
// MetadataField 结构体描述单个字段/列的元数据定义。
type MetadataField struct {
	Name string `json:"name"` // 字段名（例如 "person_id", "settlement_amount", "chronic_disease"）
	Type string `json:"type"` // 字段数据类型（例如 "string", "float", "integer", "timestamp"）
}

// TableMetadata describes the schema of a mock data table.
// TableMetadata 结构体描述模拟数据表的 Schema 结构与统计信息。
type TableMetadata struct {
	Name     string          `json:"name"`      // 表名称或数据源名称
	RowCount int             `json:"row_count"` // 表内总行数
	Fields   []MetadataField `json:"fields"`    // 表字段元数据列表
}

// MetadataResponse is the response for metadata query.
// MetadataResponse 结构体封装了特定数据源 Schema 元数据探查的查询响应。
type MetadataResponse struct {
	DataSourceID string          `json:"datasource_id"` // 数据源 ID
	Tables       []TableMetadata `json:"tables"`        // 包含的数据表元数据定义列表
	Via          string          `json:"via"`           // 服务标识
}

// ConnectionTestResult is the response for connection test.
// ConnectionTestResult 结构体承载数据源连通性测试的结果报告。
type ConnectionTestResult struct {
	DataSourceID string `json:"datasource_id"` // 被测试的数据源 ID
	Success      bool   `json:"success"`       // 连通性测试是否成功（true/false）
	LatencyMs    int64  `json:"latency_ms"`    // 探测耗时（毫秒）
	Via          string `json:"via"`           // 服务标识
}

// SingleRecordResponse is the response for querying a single record by ID card number.
// SingleRecordResponse 结构体封装按身份证号查询单条记录的响应。
type SingleRecordResponse struct {
	DatasourceID string         `json:"datasource_id"` // canonical 数据源标识符
	Record       map[string]any `json:"record"`        // 查询到的单条数据记录
	Found        bool           `json:"found"`         // 是否找到匹配记录
	Via          string         `json:"via"`           // 服务标识
}
