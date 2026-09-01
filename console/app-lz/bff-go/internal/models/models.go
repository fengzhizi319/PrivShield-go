// Package models 定义 App-LZ BFF 层所有数据结构的统一集合。
//
// 这些结构体在 BFF 的三个核心层之间传递：
//   - clients 层：从上游微服务获取原始数据，反序列化为这些模型
//   - handlers 层：将模型序列化为 JSON 返回给前端
//   - runner 层：E2E 测试执行时构造和校验这些模型
//
// 模型分组：
//   - 拓扑探测：ServiceNode, TopologyResponse
//   - 任务调度：DispatchRequest/Response, Task, TasksResponse
//   - Phase B 租约：LeasedTaskSummary, WorkerLeaseInfo, LeasedTasksResponse
//   - E2E 测试：TestSuiteAssertion, TestSuiteCase, RunTestSuiteRequest/Response
//   - 数据源：Datasource, DatasourceSliceResponse
//   - 审计：AuditLogItem, AuditVerifyResponse
//   - 预设数据 API：DataApiDef, DataApiInvokeRequest, DataApiSessionStage, DataApiSessionResponse
package models

import (
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// 拓扑探测模型 —— 用于前端「服务拓扑大屏」展示 4 个上游微服务的实时状态
// ---------------------------------------------------------------------------

// ServiceNode 描述单个微服务的健康状态和连接信息。
// 每个节点同时探测 REST 和 gRPC 两种协议，前端可切换查看。
type ServiceNode struct {
	// 服务标识符，如 "service-hub", "engine", "datasource-mgr", "audit-log"
	ID       string `json:"id"`
	Name     string `json:"name"`      // 服务显示名称
	HTTPURL  string `json:"http_url"`  // REST 探测地址
	GRPCAddr string `json:"grpc_addr"` // gRPC 探测地址

	// 综合状态（取当前活跃协议的结果）
	Status   string  `json:"status"`   // "ready" | "unhealthy" | "unreachable"
	RTTMs    float64 `json:"rtt_ms"`   // 当前协议的往返延迟（毫秒）
	Protocol string  `json:"protocol"` // 当前活跃协议："rest" | "grpc"

	// REST 协议独立探测结果
	RESTStatus string  `json:"rest_status"` // REST 健康状态
	RESTRTTMs  float64 `json:"rest_rtt_ms"` // REST 往返延迟

	// gRPC 协议独立探测结果
	GRPCStatus string  `json:"grpc_status"` // gRPC 健康状态
	GRPCRTTMs  float64 `json:"grpc_rtt_ms"` // gRPC 往返延迟

	Version string         `json:"version"`           // 服务版本号
	Details map[string]any `json:"details,omitempty"` // 额外元数据（如 upstream_count）
	Error   string         `json:"error,omitempty"`   // 错误信息（仅异常时填充）
}

// TopologyResponse 是前端拓扑大屏的完整响应。
// 包含 4 个微服务节点的实时状态快照。
type TopologyResponse struct {
	Status         string        `json:"status"`          // 整体状态："healthy" | "degraded"
	ActiveProtocol string        `json:"active_protocol"` // 当前查看的协议视角
	Timestamp      string        `json:"timestamp"`       // 探测时间戳
	Services       []ServiceNode `json:"services"`        // 固定 4 个服务节点（Hub→Engine→Datasource→Audit）
}

// ---------------------------------------------------------------------------
// 任务调度模型 —— 用于前端「任务生命周期大屏」
// ---------------------------------------------------------------------------

// DispatchRequest 是手动派发任务的请求体。
// 前端通过此结构向 Service Hub 提交新的数据处理任务。
//
// 数据源字段兼容策略（api_rename_design.md §5.6）：
//   - datasource_id：canonical 数据源标识（推荐）
//   - api_code：业务 API 标识，缺省 datasource_id 时由其推导出数据源
//   - source：历史字段名，仅作入站兼容，内部一律使用 canonical 值
type DispatchRequest struct {
	DatasourceID string         `json:"datasource_id"`
	APICode      string         `json:"api_code"`
	Source       string         `json:"source"`    // DEPRECATED: 请改用 datasource_id
	Operation    string         `json:"operation"` // 隐私操作类型："mask" | "dp" | "k_anon" | "qol" | "none"
	Payload      map[string]any `json:"payload" binding:"required"`
	Priority     int            `json:"priority"` // 优先级（数值越小越优先）
}

// RawDatasourceValue returns the first non-empty inbound datasource representation,
// honouring the fixed precedence datasource_id > source.
// RawDatasourceValue 按 datasource_id > source 固定优先级返回入站原始值。
func (r DispatchRequest) RawDatasourceValue() string {
	if strings.TrimSpace(r.DatasourceID) != "" {
		return r.DatasourceID
	}
	return r.Source
}

// DispatchResponse 是任务派发后的响应。
type DispatchResponse struct {
	TaskID       string `json:"task_id"`
	Status       string `json:"status"`
	DatasourceID string `json:"datasource_id,omitempty"` // 归一化后的 canonical 数据源
	APICode      string `json:"api_code,omitempty"`      // 归一化后的 canonical 业务 API
	Via          string `json:"via,omitempty"`           // 派发路径（如 "service-hub"）
	Error        string `json:"error,omitempty"`         // 错误信息
}

// Task 表示 Service Hub 中的一个完整生命周期任务。
// 包含从创建到完成的所有元数据，以及 Phase B 的租约信息。
type Task struct {
	ID           string     `json:"id"`                     // 任务唯一标识
	Status       string     `json:"status"`                 // 状态机："pending" → "running" → "completed"/"failed"
	Stage        string     `json:"stage"`                  // 当前处理阶段（6 阶段：ingest/fetch/classify/desensitize/return/audit）
	DatasourceID string     `json:"datasource_id"`          // canonical 数据源 ID（兼容期与 source 双写）
	APICode      string     `json:"api_code,omitempty"`     // canonical 业务 API 标识
	Source       string     `json:"source"`                 // 数据来源（DEPRECATED：与 datasource_id 同值）
	Operation    string     `json:"operation"`              // 隐私操作类型
	Priority     int        `json:"priority"`               // 优先级
	CreatedAt    time.Time  `json:"created_at"`             // 任务创建时间
	StartedAt    *time.Time `json:"started_at,omitempty"`   // 开始执行时间（nullable）
	CompletedAt  *time.Time `json:"completed_at,omitempty"` // 完成时间（nullable）
	DurationMs   int64      `json:"duration_ms"`            // 总耗时（毫秒）
	Error        string     `json:"error,omitempty"`        // 错误信息（仅 failed 时）
	PayloadJSON  string     `json:"payload_json,omitempty"` // 原始负载的 JSON 字符串
	ResultJSON   string     `json:"result_json,omitempty"`  // 执行结果的 JSON 字符串
	RetryCount   int        `json:"retry_count"`            // 已重试次数

	// Phase B PostgreSQL 租约字段
	LeaseOwner     string     `json:"lease_owner,omitempty"`      // 租约持有者 Worker ID
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"` // 租约过期时间

	Via string `json:"via,omitempty"` // 数据来源标识（"live" / "fallback"）
}

// TasksResponse 是任务列表查询的响应，支持分页。
type TasksResponse struct {
	Total int    `json:"total"`         // 任务总数
	Tasks []Task `json:"tasks"`         // 当前页的任务列表
	Via   string `json:"via,omitempty"` // 数据来源标识
}

// ---------------------------------------------------------------------------
// Phase B 租约模型 —— 用于前端「租约检查器」展示 PostgreSQL 原子租约状态
// ---------------------------------------------------------------------------

// LeasedTaskSummary 保存单个任务的租约信息，用于 UI 展示。
type LeasedTaskSummary struct {
	TaskID                string  `json:"task_id"`                  // 任务 ID
	Stage                 string  `json:"stage"`                    // 当前处理阶段
	Priority              int     `json:"priority"`                 // 优先级
	LeaseExpiresInSeconds float64 `json:"lease_expires_in_seconds"` // 租约剩余秒数（负数表示已过期）
}

// WorkerLeaseInfo 描述单个 Worker 当前持有的所有租约。
type WorkerLeaseInfo struct {
	WorkerID          string              `json:"worker_id"`           // Worker 唯一标识
	ClaimedTasksCount int                 `json:"claimed_tasks_count"` // 该 Worker 持有的任务数
	Tasks             []LeasedTaskSummary `json:"tasks"`               // 具体任务列表
}

// LeasedTasksResponse 是 Phase B PostgreSQL 租约检查的完整响应。
// 包含存储后端类型、所有租约任务、Worker 分组和孤儿任务恢复信息。
type LeasedTasksResponse struct {
	StoreBackend     string            `json:"store_backend"`      // 存储后端："postgresql" | "sqlite" | "memory"
	TotalLeasedTasks int               `json:"total_leased_tasks"` // 当前活跃租约总数
	Workers          []WorkerLeaseInfo `json:"workers"`            // 按 Worker 分组的租约信息
	OrphanRecovery   map[string]any    `json:"orphan_recovery"`    // 孤儿任务恢复状态（过期租约的自动回收）
}

// ---------------------------------------------------------------------------
// E2E 测试套件模型 —— 用于前端「测试运行器大屏」
// ---------------------------------------------------------------------------

// TestSuiteAssertion 表示测试用例中的单个断言结果。
type TestSuiteAssertion struct {
	Name     string `json:"name"`     // 断言名称（如 "审计链完整"）
	Expected string `json:"expected"` // 期望值
	Actual   string `json:"actual"`   // 实际值
	Passed   bool   `json:"passed"`   // 是否通过
}

// TestSuiteCase 表示一个完整的 E2E 测试场景。
// 当前实现 3 个套件：TS-01（审计验真）、TS-02（高并发压测）、TS-03（原子租约争抢）。
type TestSuiteCase struct {
	ID          string               `json:"id"`              // 套件编号："TS-01" | "TS-02" | "TS-03"
	Title       string               `json:"title"`           // 套件标题
	Description string               `json:"description"`     // 详细描述
	Category    string               `json:"category"`        // 分类（如 "audit", "performance", "lease"）
	Status      string               `json:"status"`          // 执行状态："pending" | "running" | "passed" | "failed" | "skipped"
	DurationMs  float64              `json:"duration_ms"`     // 执行耗时（毫秒）
	Error       string               `json:"error,omitempty"` // 错误信息
	Assertions  []TestSuiteAssertion `json:"assertions"`      // 断言结果列表
	Logs        []string             `json:"logs"`            // 执行日志行
}

// RunTestSuiteRequest 是执行测试套件的请求体。
type RunTestSuiteRequest struct {
	SuiteIDs          []string `json:"suite_ids"`          // 要执行的套件 ID 列表
	Concurrency       int      `json:"concurrency"`        // 并发数（TS-02 压测用）
	BenchmarkRequests int      `json:"benchmark_requests"` // 压测请求数（TS-02 用）
}

// RunTestSuiteResponse 是测试执行完成后的响应。
type RunTestSuiteResponse struct {
	RunID       string          `json:"run_id"`                 // 本次运行的唯一标识
	Status      string          `json:"status"`                 // 整体状态："running" | "completed" | "failed"
	TotalCases  int             `json:"total_cases"`            // 用例总数
	PassedCases int             `json:"passed_cases"`           // 通过的用例数
	FailedCases int             `json:"failed_cases"`           // 失败的用例数
	StartedAt   string          `json:"started_at"`             // 开始时间
	CompletedAt string          `json:"completed_at,omitempty"` // 完成时间
	Results     []TestSuiteCase `json:"results"`                // 每个套件的详细结果
	Summary     map[string]any  `json:"summary,omitempty"`      // 额外汇总信息（如压测百分位数）
}

// ---------------------------------------------------------------------------
// 数据源模型 —— 用于前端「数据源浏览器」（旧版组件）
// ---------------------------------------------------------------------------

// Datasource 表示 datasource-mgr 中注册的一个模拟数据源。
// datasource_id 为 canonical 字段，id 为历史字段（兼容期双写）。
type Datasource struct {
	ID           string   `json:"id"`
	DatasourceID string   `json:"datasource_id"` // canonical（与 id 同值）
	Name         string   `json:"name"`          // 显示名称
	APICode      string   `json:"api_code,omitempty"`
	Category     string   `json:"category"`
	Status       string   `json:"status,omitempty"` // "active" | "reserved"
	RecordsCount int      `json:"records_count"`
	Fields       []string `json:"fields,omitempty"`
}

// DatasourceSliceResponse 包含从数据源采样的行数据。
// source 字段用于区分「真实上游」与「本地兜底」（api_rename_design.md §9.3 不变式 4）。
type DatasourceSliceResponse struct {
	DatasourceID string           `json:"datasource_id"` // canonical 数据源 ID
	Count        int              `json:"count"`
	Total        int              `json:"total"`
	Records      []map[string]any `json:"records"`
	Source       string           `json:"source"` // "datasource-mgr" | "fallback"
	Detail       string           `json:"detail,omitempty"`
}

// DatasourcesResponse 是数据源目录查询的响应。
// source 区分真实上游与本地兜底（D-01 整改）。
type DatasourcesResponse struct {
	Datasources []Datasource `json:"datasources"`
	Total       int          `json:"total"`
	Source      string       `json:"source"` // "datasource-mgr" | "fallback"
	Detail      string       `json:"detail,omitempty"`
	Via         string       `json:"via"`
}

// AuditLogsResponse 是审计日志查询的响应。
// source 区分真实上游与本地兜底（D-02 整改）。
type AuditLogsResponse struct {
	Logs   []AuditLogItem `json:"logs"`
	Total  int            `json:"total"`
	Source string         `json:"source"` // "audit-log" | "fallback"
	Detail string         `json:"detail,omitempty"`
	Via    string         `json:"via"`
}

// ---------------------------------------------------------------------------
// 审计模型 —— 用于前端「审计验证大屏」
// ---------------------------------------------------------------------------

// AuditLogItem 表示 audit-log 服务中的一条审计记录。
//
// canonical 字段名与 services/audit-log/internal/models.AuditLog 逐字段对齐（D-04），
// 下方标 DEPRECATED 的字段是从 canonical 推导出的旧前端字段别名，
// 仅用于兼容现有 App-LZ 大屏，由 clients 层在返前统一填充。
type AuditLogItem struct {
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	TaskID        string `json:"task_id,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	APICode       string `json:"api_code,omitempty"`
	Datasource    string `json:"datasource"` // 审计域字段名，值必须 ds_*
	DatasourceID  string `json:"datasource_id"`
	Operation     string `json:"operation"` // mask | classify | k_anon | dp | qol
	InputHash     string `json:"input_hash"`
	OutputHash    string `json:"output_hash"`
	Algorithm     string `json:"algorithm,omitempty"`
	User          string `json:"user,omitempty"`
	Status        string `json:"status"` // success | failed
	SecurityLevel string `json:"security_level,omitempty"`
	InputRows     int    `json:"input_rows,omitempty"`
	OutputRows    int    `json:"output_rows,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`

	// DEPRECATED legacy aliases —— 与旧前端字段保持兼容，由 NormalizeAliases() 填充
	Source     string `json:"source"`
	DataHash   string `json:"data_hash"`
	Operator   string `json:"operator"`
	Encryption string `json:"encryption"`
	Result     string `json:"result"`
}

// NormalizeAliases copies canonical values into the deprecated alias fields so
// the existing App-LZ audit panel keeps rendering while consumers migrate.
// NormalizeAliases 将 canonical 值同步到旧别名字段，保证大屏不回归。
func (a *AuditLogItem) NormalizeAliases() {
	if a.DatasourceID == "" {
		a.DatasourceID = a.Datasource
	}
	if a.Datasource == "" {
		a.Datasource = a.DatasourceID
	}
	a.Source = a.Datasource
	a.DataHash = a.OutputHash
	if a.DataHash == "" {
		a.DataHash = a.InputHash
	}
	a.Operator = a.User
	if a.Operator == "" {
		a.Operator = "service-hub-pipeline"
	}
	a.Encryption = "SHA-256"
	if a.Algorithm != "" {
		a.Encryption = "SHA-256/" + a.Algorithm
	}
	a.Result = a.Status
}

// AuditRecordRequest is the outbound payload used by the BFF to write a real
// evidence entry into audit-log (POST /api/audit/logs).
// AuditRecordRequest 是 BFF 向 audit-log 写入真实存证的请求体。
type AuditRecordRequest struct {
	Datasource    string `json:"datasource"`
	APICode       string `json:"api_code,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	Operation     string `json:"operation"`
	InputHash     string `json:"input_hash,omitempty"`
	OutputHash    string `json:"output_hash,omitempty"`
	InputSample   string `json:"input_sample,omitempty"`
	OutputSample  string `json:"output_sample,omitempty"`
	Algorithm     string `json:"algorithm,omitempty"`
	Parameters    any    `json:"parameters,omitempty"`
	User          string `json:"user,omitempty"`
	Status        string `json:"status"`
	SecurityLevel string `json:"security_level,omitempty"`
	InputRows     int    `json:"input_rows,omitempty"`
	OutputRows    int    `json:"output_rows,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
}

// AuditVerifyResponse 表示 Merkle 树验证的输出结果。
// 用于前端展示审计日志的不可篡改性验证。
type AuditVerifyResponse struct {
	MerkleValid  bool   `json:"merkle_valid"` // 完整性校验是否通过（必须来自真实上游）
	RootHash     string `json:"root_hash"`    // 快照完整性哈希
	TotalEntries int    `json:"total_entries"`
	SnapshotID   string `json:"snapshot_id,omitempty"`   // 被验真的快照 ID
	ExpectedHash string `json:"expected_hash,omitempty"` // 重算哈希
	Source       string `json:"source"`                  // "audit-log" | "fallback"
	Timestamp    string `json:"timestamp"`
	Signature    string `json:"signature,omitempty"`
	Error        string `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// 预设数据 API 会话模型 —— 用于前端「预设数据 API 大屏」
// 描述 4 个预设 API 的定义、调用请求和完整的 4 阶段会话生命周期
// ---------------------------------------------------------------------------

// DataApiDef 描述一个预设数据 API 的定义。
// api_code 为 canonical 业务标识，id/seq 仅作为展示序号（D-13）。
type DataApiDef struct {
	APICode      string   `json:"api_code"`      // canonical："api1_yibao" | "api2_kangyang"
	DatasourceID string   `json:"datasource_id"` // canonical 数据源 ID（预留位为空）
	Seq          int      `json:"seq"`           // 展示序号
	ID           int      `json:"id"`            // DEPRECATED: 等于 seq，仅作顺序展示，不参与语义
	Name         string   `json:"name"`
	NameEn       string   `json:"name_en,omitempty"`
	Slug         string   `json:"slug,omitempty"`
	FileName     string   `json:"file_name,omitempty"`
	Category     string   `json:"category"`
	Description  string   `json:"description"`
	Fields       []string `json:"fields"`
	Status       string   `json:"status"` // "active"（已启用）| "reserved"（预留）
}

// DataApiInvokeRequest 是调用预设数据 API 的请求体。
// 优先级：api_code > api_id；datasource_id 缺省时由 api_code 推导，
// 两者同时给出且不自洽时返回 400 API_DATASOURCE_MISMATCH。
type DataApiInvokeRequest struct {
	APICode      string `json:"api_code"`
	DatasourceID string `json:"datasource_id"`
	ApiID        int    `json:"api_id"` // DEPRECATED: 请改用 api_code
	Limit        int    `json:"limit"`
	Lean         bool   `json:"lean"` // 轻量模式：跳过 raw_records/sanitized_data 序列化（压测/监控专用）
}

// DataApiSessionStage 记录完整会话生命周期中的一个步骤。
// 阶段名（5 阶段，classify 与 desensitize 已合并为 classify_desensitize）：
// ingest → fetch → classify_desensitize → return → audit。
type DataApiSessionStage struct {
	Name       string `json:"name"` // 阶段名（上述 5 项之一）
	Title      string `json:"title"`
	Status     string `json:"status"`           // "success" | "error" | "skipped"
	Source     string `json:"source,omitempty"` // "engine" | "local-fallback" | "audit-log" ...
	DurationMs int64  `json:"duration_ms"`
	ComputeMs  int64  `json:"compute_ms,omitempty"`  // 本地计算耗时（含 JSON 编解码）
	NetworkMs  int64  `json:"network_ms,omitempty"`  // 上游 HTTP 通信耗时（网络往返 + 序列化）
	Detail     string `json:"detail,omitempty"`
}

// DataApiSessionResponse 是预设数据 API 调用的完整会话结果。
// 包含原始数据、脱敏后数据、每个阶段的执行状态和总耗时。
type DataApiSessionResponse struct {
	SessionID     string                `json:"session_id"`
	APICode       string                `json:"api_code"`      // canonical 业务 API
	DatasourceID  string                `json:"datasource_id"` // canonical 数据源
	ApiID         int                   `json:"api_id"`        // DEPRECATED: 等于 seq
	ApiName       string                `json:"api_name"`
	Status        string                `json:"status"`                   // 整体状态："completed" | "partial" | "failed"
	RawRecords    []map[string]any      `json:"raw_records,omitempty"`    // 从数据源获取的原始记录
	SanitizedData []map[string]any      `json:"sanitized_data,omitempty"` // 脱敏后的记录
	Stages        []DataApiSessionStage `json:"stages"`                   // 各阶段执行详情
	AuditEntryID  string                `json:"audit_entry_id,omitempty"` // 写入审计日志的条目 ID
	TotalDuration int64                 `json:"total_duration_ms"`        // 会话总耗时（毫秒）
	Error         string                `json:"error,omitempty"`          // 错误信息
}
