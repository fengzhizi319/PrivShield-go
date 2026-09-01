// Package models defines shared data structures for the service-hub module.
// Package models 定义数据服务调度中枢模块的领域模型与共享数据结构。
//
// 涵盖调度中枢健康监控、6 阶段流水线任务生命周期状态、任务分发请求/响应、
// 敏感级别（L1~L5）至脱敏算子（none/mask/k_anon/dp）的映射规则。
package models

import (
	"strings"
	"time"

	"github.com/fengzhizi319/PrivShield/pkg/naming"
)

// HubStatus represents the scheduling hub's current status and queue telemetry.
// HubStatus 结构体表示调度中枢运行时的全局概览状态与指标快照。
type HubStatus struct {
	Status         string `json:"status"`          // 中枢服务状态："running"（正常）| "degraded"（降级）| "stopped"（停止）
	Uptime         string `json:"uptime"`          // 服务可读运行时长（如 "12h34m56s"）
	ActiveTasks    int    `json:"active_tasks"`    // 当前正在执行的任务并发数
	QueuedTasks    int    `json:"queued_tasks"`    // 当前在缓冲队列中排队等待的任务数
	CompletedTotal int    `json:"completed_total"` // 历史累计成功完成的任务总数
	FailedTotal    int    `json:"failed_total"`    // 历史累计失败的任务总数
	AgentURL       string `json:"agent_url"`       // 当前绑定的上游 PrivShield Agent REST 访问基地址
}

// Task represents a scheduling task and its lifecycle execution metadata.
// Task 结构体表示数据处理流水线中的一个具体调度任务。
type Task struct {
	ID           string     `json:"id"`                 // 任务全局唯一标识符（如 "task-1787578265-8d479f51"）
	APICode      string     `json:"api_code,omitempty"` // canonical 业务 API（如 "api1_yibao"）
	DatasourceID string     `json:"datasource_id"`      // canonical 数据源 ID（如 "ds_yibao"）
	Status       string     `json:"status"`             // 任务当前生命周期状态："pending" | "running" | "completed" | "failed"
	Stage        string     `json:"stage"`              // 任务当前流水线执行阶段："ingest" | "fetch" | "classify" | "desensitize" | "return" | "audit" | "done"
	Source       string     `json:"source"`             // 兼容历史字段名，与 DatasourceID 同值双写
	Operation    string     `json:"operation"`          // 执行的隐私保护算子类型："none" | "mask" | "k_anon" | "dp"
	CreatedAt    time.Time  `json:"created_at"`         // 任务接收并创建的时间戳
	StartedAt    *time.Time `json:"started_at"`         // 任务开始分配并执行的时间戳（未开始为 nil）
	CompletedAt  *time.Time `json:"completed_at"`       // 任务最终完成或失败的时间戳（未结束为 nil）
	DurationMs   int64      `json:"duration_ms"`        // 任务从开始到完成的端到端执行耗时（毫秒）
	Error        string     `json:"error,omitempty"`    // 任务执行失败时的详细错误信息（成功时省略）
}

// TaskListResponse is the HTTP REST response for listing and querying tasks.
// TaskListResponse 结构体是查询任务列表接口的响应包装。
type TaskListResponse struct {
	Total int    `json:"total"` // 符合过滤条件或当前存储库中的任务总条数
	Tasks []Task `json:"tasks"` // 任务对象列表切片
	Via   string `json:"via"`   // 响应来源模块标识（固定为 "service-hub"）
}

// DispatchRequest is the request body for dispatching a new privacy task into the pipeline.
// DispatchRequest 结构体表示向调度中枢提交新数据处理任务时的入参。
type DispatchRequest struct {
	APICode      string `json:"api_code"`                     // canonical 业务 API 编码（如 "api1_yibao"）
	DatasourceID string `json:"datasource_id"`                // canonical 数据源标识符（如 "ds_yibao"）
	Source       string `json:"source"`                       // 历史兼容字段（如 "yibao" 或 "ds_yibao"）
	Operation    string `json:"operation" binding:"required"` // 指定的脱敏操作类型（必填，"mask" | "k_anon" | "dp" | "none"）
	Payload      any    `json:"payload"`                      // 待处理的原始数据载荷（可为单条记录 map 或记录列表切片）
	Priority     int    `json:"priority"`                     // 任务执行优先级（数值越大，调度优先级越高，默认 0）
}

// DispatchResponse is the HTTP response returned immediately after dispatching a task.
// DispatchResponse 结构体表示任务接收与异步派发成功的响应。
type DispatchResponse struct {
	TaskID       string `json:"task_id"`                 // 系统为该任务分配的唯一 TaskID
	APICode      string `json:"api_code,omitempty"`      // canonical 业务 API 编码
	DatasourceID string `json:"datasource_id,omitempty"` // canonical 数据源标识符
	Status       string `json:"status"`                  // 派发接收状态："accepted"（已受理）| "queued"（已入队）| "rejected"（拒绝）
	Via          string `json:"via"`                     // 响应来源模块标识（固定为 "service-hub"）
}

// PipelineStage represents monitoring metrics for one stage in the 6-stage scheduling pipeline.
// PipelineStage 结构体表示 6 阶段流水线中某一阶段的实时监控遥测数据。
type PipelineStage struct {
	Name         string `json:"name"`           // 阶段名称："ingest" | "fetch" | "classify" | "desensitize" | "return" | "audit"
	Status       string `json:"status"`         // 阶段当前状态："idle"（空闲）| "processing"（处理中）| "error"（异常）
	ActiveCount  int    `json:"active_count"`   // 当前阶段正在并行处理的任务计数
	AvgLatencyMs int64  `json:"avg_latency_ms"` // 该阶段的历史平均处理延迟（毫秒）
	Throughput   int    `json:"throughput"`     // 该阶段当前的吞吐率（每分钟处理任务数 Tasks Per Minute）
}

// PipelineStatus represents the aggregate status and health of the entire pipeline.
// PipelineStatus 结构体表示整个 6 阶段调度流水线的聚合健康与性能状态。
type PipelineStatus struct {
	Stages   []PipelineStage `json:"stages"`    // 流水线包含的 6 个阶段详细状态列表
	TotalRPS float64         `json:"total_rps"` // 当前流水线聚合每秒请求数 (Requests Per Second)
	AgentOK  bool            `json:"agent_ok"`  // 上游 PrivShield Agent 隐私引擎是否存活且连通正常
}

// ProxyResponse is the unified envelope for responses proxied through service-hub.
// ProxyResponse 结构体是 service-hub 代理外部请求时的统一响应封套。
type ProxyResponse struct {
	Status     int    `json:"status"`      // HTTP 状态码（如 200, 400, 500）
	DurationMs int64  `json:"duration_ms"` // 代理调用的端到端网络往返耗时（毫秒）
	Data       any    `json:"data"`        // 上游服务返回的实际业务数据体
	Via        string `json:"via"`         // 代理中间件标识（固定为 "service-hub"）
}

// LevelToOperation maps a data security classification level (L1~L5) to the corresponding privacy operation.
// LevelToOperation 根据「三层四柱五御六类」数据安全分级标准，将数据的敏感级别映射为对应的隐私增强保护算子。
//
// 入参容忍两套词表：规则库/存证使用的 L1~L5 标识，以及 engine 三层漏斗内部的 canonical
// 名称（public/internal/confidential/secret/top_secret）；归一化统一由 pkg/naming 完成，
// 本函数不再各自维护副本（P1-5 词表一致性）。
//
// - L1 (公开数据):   "none"   (无需脱敏，明文流转)
// - L2 (内部数据):   "mask"   (敏感字段级掩码脱敏，如手机号/身份证掩码)
// - L3 (敏感数据):   "k_anon" (K-匿名泛化抑制，防御重标识风险)
// - L4 (高敏感数据): "dp"     (差分隐私机制，注入拉普拉斯/高斯受控噪声)
// - L5 (极敏感数据): "dp"     (强差分隐私 + 严格隐私预算控制)
// - 词表外/空值:     "mask"   (兜底掩码；调用方应在拿到空级别时直接失败，而非依赖本兜底)
func LevelToOperation(level string) string {
	switch naming.NormalizeSecurityLevelID(level) {
	case naming.SecurityLevelL1:
		return OperationNone // L1 公开数据：无需脱敏
	case naming.SecurityLevelL2:
		return OperationMask // L2 内部数据：字段级掩码脱敏
	case naming.SecurityLevelL3:
		return OperationKAnon // L3 敏感数据：K-匿名泛化
	case naming.SecurityLevelL4, naming.SecurityLevelL5:
		return OperationDP // L4/L5 高敏感与极敏感数据：差分隐私加噪
	default:
		return OperationMask // 未知级别：安全兜底为字段级掩码
	}
}

// 隐私算子标识（与 pkg/validation.HubOperations 保持一致）。
const (
	OperationNone  = "none"
	OperationMask  = "mask"
	OperationKAnon = "k_anon"
	OperationDP    = "dp"
	OperationQOL   = "qol"
	OperationClass = "classify"
)

// operationStrength 是算子的保护强度序：数值越大脱敏力度越强。
// classify 只做定级不做变换，因此与 none 同级；qol 为查询置乱，按掩码同级处理。
// 词表外算子不在表中，OperationStrength 对其返回 -1。
var operationStrength = map[string]int{
	OperationNone:  0,
	OperationClass: 0,
	OperationMask:  1,
	OperationQOL:   1,
	OperationKAnon: 2,
	OperationDP:    3,
}

// OperationStrength 返回算子的保护强度；词表外算子返回 -1。
func OperationStrength(operation string) int {
	if s, ok := operationStrength[strings.TrimSpace(operation)]; ok {
		return s
	}
	return -1
}

// EffectiveOperation resolves the operator a task actually applies.
// EffectiveOperation 计算任务最终执行的隐私算子（P1-1 核心约束）：
//
//	以「定级结果推导出的算子」为下限，调用方请求的算子只允许把保护强度**上调**，
//	绝不允许下调。这样即便上游传入 operation=none，L4 数据依然会走差分隐私加噪，
//	彻底消除「调用方自选弱算子绕过脱敏」的越权路径。
//
// 请求算子不在词表内（含空串）时直接采用定级推导结果。
func EffectiveOperation(requested, derived string) string {
	derivedStrength := OperationStrength(derived)
	if derivedStrength < 0 {
		// 定级推导结果不可识别：保留兜底 mask，宁可多脱敏也不能漏。
		derived = OperationMask
		derivedStrength = OperationStrength(derived)
	}
	requestedStrength := OperationStrength(requested)
	if requestedStrength > derivedStrength {
		return strings.TrimSpace(requested)
	}
	return derived
}
