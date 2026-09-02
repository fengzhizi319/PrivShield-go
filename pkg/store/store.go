// Package store defines storage interfaces for console Go modules.
// Package store 为控制台各 Go 微服务定义统一的抽象存储接口与核心数据模型。
//
// ==============================================================================
// 【架构设计定位与接口体系】
// 本包定义了 PrivShield 核心数据流通与治理体系的三大存储接口：
//  1. 【TaskStore / LeasedTaskStore】：
//     - 服务于调度中枢 service-hub；
//     - TaskStore 提供单实例任务 CRUD、状态过滤、统计聚合与过期清理；
//     - LeasedTaskStore 扩展支持 Phase B 多副本并发 Hub 调度，通过 PostgreSQL
//       FOR UPDATE SKIP LOCKED 实现无阻塞原子任务抢占（ClaimNext）、租约续期（RenewLease）、
//       条件完成（CompleteLease）、带退避的失败重试（FailLease）与过期租约回收（RequeueExpiredLeases）；
//  2. 【DataSourceStore】：
//     - 服务于数据源资产管理 datasource-mgr；
//     - 提供数据源注册、配置持久化、敏感特征探查历史与访问审计记录（AccessAuditRecord）；
//  3. 【AuditStore】：
//     - 服务于审计存证服务 audit-log；
//     - 提供基于国密 SM3 的不可篡改链式日志（AuditLog）与 SM4-GCM 信封加密脱敏快照（SnapshotRecord）落盘、
//       高并发微批写入（SaveLogsBatch）、SQL 级统计分析与全量哈希链对账核验（VerifyChain）。
// ==============================================================================

package store

import (
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────
// 1. Task Store (service-hub) / 任务流水线调度存储
// ─────────────────────────────────────────────────────────────

// Task represents a scheduling task in the pipeline.
// Task 表示数据流通与脱敏调度流水线中的单个任务实体。
type Task struct {
	ID           string     `json:"id"`                      // 任务唯一标识（UUID 或业务前缀 ID）
	APICode      string     `json:"api_code,omitempty"`      // Canonical 业务 API 编码（如 "api1_yibao", "api2_kangyang"）
	DatasourceID string     `json:"datasource_id,omitempty"` // Canonical 数据源规范 ID（如 "ds_yibao", "ds_kangyang"）
	Status       string     `json:"status"`                  // 任务当前状态："pending" | "running" | "completed" | "failed"
	Stage        string     `json:"stage"`                   // 当前流水线所处阶段（如 "queued", "ingest", "privacy", "done"）
	Source       string     `json:"source"`                  // 原始入站数据源标识（兼容旧版字段）
	Operation    string     `json:"operation"`               // 隐私计算操作原语："mask" | "k_anon" | "dp" | "classify" | "none"
	Priority     int        `json:"priority"`                // 调度优先级（数值越大，越优先被 ClaimNext 调度）
	CreatedAt    time.Time  `json:"created_at"`              // 任务创建时间
	StartedAt    *time.Time `json:"started_at"`              // 任务开始执行时间戳（可空）
	CompletedAt  *time.Time `json:"completed_at"`            // 任务终态完成时间戳（可空）
	DurationMs   int64      `json:"duration_ms"`             // 任务实际执行耗时（毫秒）
	Error        string     `json:"error,omitempty"`         // 失败时的错误信息详情
	ErrorClass   string     `json:"error_class,omitempty"`   // 失败分类枚举（P2-7）：由失败点依 sentinel error 一次性判定并落库，重试扫描只读该字段，不再猜测 Error 文本
	PayloadJSON  string     `json:"-"`                       // 任务原始输入载荷（不在 JSON 序列化中向外暴露）
	RetryCount   int        `json:"retry_count"`             // 当前已发生的重试尝试次数
	RetryAfter   *time.Time `json:"retry_after,omitempty"`   // 下一次允许重试的最早时间戳（指数退避延迟点）
	TraceID      string     `json:"trace_id,omitempty"`      // 全链路分布式追踪 ID，用于端到端关联日志

	// ── Phase B: Lease fields for multi-replica Hub / 多副本 Hub 租约字段 ──
	LeaseOwner     string     `json:"lease_owner,omitempty"`      // 当前持有该任务租约的所有者实例标识（如 "hub-node-1"）
	LeaseToken     string     `json:"lease_token,omitempty"`      // 当前租约的唯一随机令牌（防止过期所有者产生陈旧覆盖写入）
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"` // 租约绝对过期时间（为 nil 表示未被锁定）
	Version        int        `json:"version"`                    // 乐观并发控制版本计数器
	MaxRetries     int        `json:"max_retries"`                // 允许的最大重试次数（默认 3 次）
}

// TaskFilter specifies filtering criteria for listing tasks.
// TaskFilter 定义任务分页与条件查询过滤参数。
type TaskFilter struct {
	Status string // 状态过滤条件（为空表示查询全部状态）
	Limit  int    // 单页最大返回条数（0 表示使用默认或不限制）
	Offset int    // 分页偏移量
}

// TaskCounts holds aggregated task counts by status.
// TaskCounts 包含按状态分类聚合的任务总数。
type TaskCounts struct {
	Pending   int `json:"pending"`   // 待调度排队中的任务数
	Running   int `json:"running"`   // 正在执行中的任务数
	Completed int `json:"completed"` // 成功完成的任务数
	Failed    int `json:"failed"`    // 执行失败终态的任务数
}

// TaskStore defines the persistence interface for scheduling tasks.
// TaskStore 定义任务生命周期管理的基础持久化存储接口。
type TaskStore interface {
	// Save 插入新任务或全量更新任务。
	Save(task *Task) error

	// Get 根据任务 ID 获取任务详情，不存在时返回错误。
	Get(id string) (*Task, error)

	// List 根据过滤条件分页查询任务列表，返回当前页切片与总记录数。
	List(filter TaskFilter) ([]Task, int, error)

	// Update 更新现有任务的可变业务字段与状态。
	Update(task *Task) error

	// Counts 聚合统计各状态的任务数量。
	Counts() (TaskCounts, error)

	// CleanupOld 物理删除早于指定时间戳的终态（completed/failed）任务，防止存储无限膨胀。
	CleanupOld(before time.Time) (int64, error)
}

// ─────────────────────────────────────────────────────────────
// 2. LeasedTaskStore — Phase B: Atomic task ownership for multi-replica Hub
// 原子任务领取与租约接口，支持多副本 Hub 安全并发调度。
// ─────────────────────────────────────────────────────────────

// ErrLeaseNotSupported is returned by backends that cannot provide lease semantics.
// 当存储后端不支持租约语义时返回此错误（如 SQLite 或内存实现）。
var ErrLeaseNotSupported = errors.New("this store backend does not support multi-replica task leases; use PostgreSQL for Phase B deployment")

// TaskLease wraps a claimed task with its ownership metadata.
// TaskLease 封装已成功领取的任务实例及其独占所有权元数据。
type TaskLease struct {
	Task      *Task     // 已成功领取并锁定的任务对象
	Owner     string    // 成功抢占该任务的 Hub 实例标识
	Token     string    // 本次租约分配的独占随机令牌
	ExpiresAt time.Time // 本次租约的绝对到期时间戳
}

// TaskResult encapsulates the successful output of a task.
// TaskResult 封装任务成功执行完毕时的输出结果。
type TaskResult struct {
	OutputJSON string // 任务处理结果的序列化 JSON 字符串
	Stage      string // 最终流转到达的流水线阶段（通常为 "done"）
}

// TaskFailure encapsulates the reason a task failed.
// TaskFailure 封装任务失败的分类、错误信息与是否可重试判定。
type TaskFailure struct {
	Error      string // 人类可读的失败原因描述
	Retryable  bool   // 是否为瞬时可重试故障（若为 true 且重试次数未超限，任务将回退到 pending）
	ErrorClass string // 错误类型分类枚举（如 "timeout", "downstream", "internal"）
}

// LeasedTaskStore extends TaskStore with atomic lease operations for multi-replica Hub.
//
// LeasedTaskStore 扩展 TaskStore，为 Phase B 多副本 Hub 提供原子并发抢占与租约管理能力。
//
// 【重要调用契约】：
// 所有条件更新方法均返回 (bool, error)：
// - bool 返回值代表当前操作是否实际命中了合法持有的租约行（即当前实例是否仍持有有效所有权）；
// - 当返回 false 时，说明当前副本已失去该任务的租约（可能发生租约过期被其他副本接管），调用方必须立即停止后续流程。
type LeasedTaskStore interface {
	TaskStore

	// ClaimNext 基于 FOR UPDATE SKIP LOCKED 机制原子领取下一个优先级最高且待调度的任务；无可用任务时返回 (nil, nil)。
	ClaimNext(owner string, leaseTTL time.Duration) (*TaskLease, error)

	// RenewLease 延长任务的租约有效期，执行条件必须是当前 owner 且 token 匹配且租约尚未过期；返回 false 表示已失去所有权。
	RenewLease(id, owner, token string, leaseTTL time.Duration) (bool, error)

	// CompleteLease 将任务标记为 completed 终态，执行条件必须是当前 owner 持有有效租约；返回 false 表示已失去所有权。
	CompleteLease(id, owner, token string, result TaskResult) (bool, error)

	// FailLease 将任务标记为失败；若 failure.Retryable 为 true 且重试次数未超限，任务自动回退为 pending 并设置退避；返回 false 表示已失去所有权。
	FailLease(id, owner, token string, failure TaskFailure) (bool, error)

	// RequeueExpiredLeases 扫描并批量回收由于节点宕机等原因导致租约超期的 running 任务，重置为 pending 等待重新调度；返回回收的任务数量。
	RequeueExpiredLeases(limit int) (int, error)
}

// ─────────────────────────────────────────────────────────────
// 3. DataSource Store (datasource-mgr) / 数据源资产存储
// ─────────────────────────────────────────────────────────────

// DataSource represents a registered data source.
// DataSource 表示在系统中注册的数据源资产实体。
type DataSource struct {
	ID            string     `json:"id"`             // 数据源唯一标识
	Name          string     `json:"name"`           // 数据源展示名称
	Type          string     `json:"type"`           // 数据源类型："database" | "api" | "file"
	Host          string     `json:"host"`           // 连接主机地址
	Port          int        `json:"port"`           // 连接端口号
	Database      string     `json:"database"`       // 目标数据库名或路径
	SecurityLevel string     `json:"security_level"` // 敏感安全等级："high" | "medium" | "low"
	Status        string     `json:"status"`         // 连通性状态："connected" | "disconnected" | "error"
	CreatedAt     time.Time  `json:"created_at"`     // 注册创建时间
	LastCheckAt   *time.Time `json:"last_check_at"`  // 最近一次探活检测时间戳
	TagsJSON      string     `json:"-"`              // 标签的 JSON 序列化底层存储字段
	Tags          []string   `json:"tags"`           // 业务分类标签列表
}

// AccessAuditRecord represents an access audit log entry.
// AccessAuditRecord 记录外部对数据源资产的访问审计流水。
type AccessAuditRecord struct {
	ID             string    `json:"id"`              // 审计记录唯一 ID
	DataSourceID   string    `json:"datasource_id"`   // 被访问的数据源 ID
	DataSourceName string    `json:"datasource_name"` // 被访问的数据源名称
	Operation      string    `json:"operation"`       // 访问操作类型（如 "query", "export"）
	User           string    `json:"user"`            // 发起操作的用户标识
	Timestamp      time.Time `json:"timestamp"`       // 操作发生时间戳
	RecordsCount   int       `json:"records_count"`   // 涉及读取/导出的记录条数
	Status         string    `json:"status"`          // 操作执行状态（"success" | "denied" | "error"）
}

// DataSourceFilter specifies filtering/pagination criteria for listing data sources.
// DataSourceFilter 定义数据源资产列表查询的分页参数。
type DataSourceFilter struct {
	Limit  int // 单页最大返回条数（0 表示不分页）
	Offset int // 分页偏移量
}

// DataSourceStore defines the persistence interface for data sources.
// DataSourceStore 定义数据源资产管理与访问审计的持久化存储接口。
type DataSourceStore interface {
	// SaveDS 新增或全量保存数据源。
	SaveDS(ds *DataSource) error

	// GetDS 获取指定 ID 的数据源详情。
	GetDS(id string) (*DataSource, error)

	// ListDS 分页查询数据源列表，返回当前页列表与总记录数。
	ListDS(filter DataSourceFilter) ([]DataSource, int, error)

	// DeleteDS 删除指定 ID 的数据源，不存在时报错。
	DeleteDS(id string) error

	// UpdateDS 更新数据源配置与探活状态。
	UpdateDS(ds *DataSource) error

	// SaveAudit 保存一条数据源访问审计记录。
	SaveAudit(rec *AccessAuditRecord) error

	// ListAudit 分页查询指定数据源的历史访问审计记录。
	ListAudit(dsID string, limit, offset int) ([]AccessAuditRecord, int, error)
}

// ─────────────────────────────────────────────────────────────
// 4. Audit Store (audit-log) / 不可篡改审计日志存储
// ─────────────────────────────────────────────────────────────

// AuditLog represents a single audit log entry.
// AuditLog 表示一条不可篡改的隐私脱敏治理审计日志实体。
type AuditLog struct {
	ID             string    `json:"id"`                       // 日志唯一流水号
	TaskID         string    `json:"task_id,omitempty"`        // 所属调度流水线任务 ID
	APICode        string    `json:"api_code,omitempty"`       // Canonical 业务 API 编码（如 "api1_yibao"）
	DatasourceID   string    `json:"datasource_id,omitempty"`  // Canonical 数据源 ID（如 "ds_yibao"）
	Timestamp      time.Time `json:"timestamp"`                // 日志生成时间戳
	Operation      string    `json:"operation"`                // 执行的隐私脱敏操作类型（如 "mask", "dp", "kano"）
	DataSource     string    `json:"datasource"`               // 兼容历史字段（与 DatasourceID 保持一致）
	InputHash      string    `json:"input_hash"`               // 脱敏前输入数据的密码学散列值
	OutputHash     string    `json:"output_hash"`              // 脱敏后输出数据的密码学散列值
	Algorithm      string    `json:"algorithm"`                // 采用的密码或脱敏算法（如 "SM4-GCM", "Laplace-DP"）
	ParametersJSON string    `json:"-"`                        // 脱敏参数的底层 JSON 存储字段
	Parameters     any       `json:"parameters"`               // 解析后的脱敏参数对象
	InputRows      int       `json:"input_rows"`               // 输入数据记录行数
	OutputRows     int       `json:"output_rows"`              // 输出数据记录行数
	DurationMs     int64     `json:"duration_ms"`              // 脱敏计算实际耗时（毫秒）
	User           string    `json:"user"`                     // 发起调用的用户或系统租户
	Status         string    `json:"status"`                   // 脱敏处理状态（"success" | "failed"）
	ErrorMessage   string    `json:"error,omitempty"`          // 失败时的错误信息
	SecurityLevel  string    `json:"security_level"`           // 数据安全分级（"L1" ~ "L5"）
	PrevHash       string    `json:"prev_hash,omitempty"`      // 前序存证哈希（串联形成不可篡改哈希链）
	IntegrityHash  string    `json:"integrity_hash,omitempty"` // 本条记录在服务端裁定的国密 SM3 综合完整性哈希
	SM2Signature   string    `json:"sm2_signature,omitempty"`  // SM2 数字签名值（G-10 审计不可否认性），hex 编码
}

// AuditFilter specifies filtering criteria for listing audit logs.
// AuditFilter 定义审计日志多维度组合条件查询参数。
type AuditFilter struct {
	TaskID        string // 按任务 ID 过滤
	APICode       string // 按 API 编码过滤
	DatasourceID  string // 按数据源 ID 过滤
	Operation     string // 按脱敏操作过滤
	DataSource    string // 兼容旧版数据源过滤字段
	User          string // 按操作用户过滤
	Status        string // 按执行状态过滤
	SecurityLevel string // 按安全等级过滤
	Limit         int    // 单页限制条数
	Offset        int    // 分页偏移量
}

// SnapshotRecord represents a desensitization snapshot for evidence.
// SnapshotRecord 记录脱敏前后数据样例快照（出域留痕凭证，样本字段经过信封加密）。
type SnapshotRecord struct {
	ID             string    `json:"id"`                      // 快照记录唯一 ID
	AuditLogID     string    `json:"audit_log_id"`            // 关联的主审计日志 ID（外键级联）
	Timestamp      time.Time `json:"timestamp"`               // 快照创建时间戳
	InputSample    string    `json:"input_sample"`            // 脱敏前输入样本（信封加密密文或脱敏文本）
	OutputSample   string    `json:"output_sample"`           // 脱敏后输出样本（信封加密密文或脱敏文本）
	Algorithm      string    `json:"algorithm"`               // 使用的算法标识
	ParametersJSON string    `json:"-"`                       // 参数 JSON 字符串
	Parameters     any       `json:"parameters"`              // 解析后的参数对象
	IntegrityHash  string    `json:"integrity_hash"`          // 继承自主日志的完整性哈希
	PrevHash       string    `json:"prev_hash,omitempty"`     // 继承自主日志的前序哈希
	SM2Signature   string    `json:"sm2_signature,omitempty"` // 快照自身完整性哈希的 SM2 签名（G-10）
}

// AuditStats holds aggregated audit statistics.
// AuditStats 保存审计日志的多维度统计聚合数据（SQL 层直接聚合）。
type AuditStats struct {
	TotalOperations int            `json:"total_operations"`  // 审计操作总次数
	ByOperation     map[string]int `json:"by_operation"`      // 按脱敏操作分类统计
	ByStatus        map[string]int `json:"by_status"`         // 按执行状态分类统计
	BySecurityLevel map[string]int `json:"by_security_level"` // 按安全等级分类统计
	AvgDurationMs   float64        `json:"avg_duration_ms"`   // 平均脱敏处理耗时（毫秒）
}

// AuditReport holds compliance audit report data.
// AuditReport 保存合规审计评估报告与智能化整改建议。
type AuditReport struct {
	TotalOperations int            `json:"total_operations"`  // 周期内操作总量
	SuccessRate     float64        `json:"success_rate"`      // 脱敏成功率百分比（0~100）
	BySecurityLevel map[string]int `json:"by_security_level"` // 安全等级分布统计
	TopOperations   []string       `json:"top_operations"`    // 发生频次前 5 的核心操作
	Recommendations []string       `json:"recommendations"`   // 自动化合规治理与预算优化建议清单
}

// 验真结论机器可读枚举（P2-4）：`ChainVerificationResult.Reason` 与单条快照验真响应的 `reason`
// 字段取值集合。历史实现只能靠英文 `message` 字符串区分断链类型，局方看板无法自动判定；
// 下列取值严格对应存储层核验循环「实际可检测」的状态，且不改变 fail-closed 语义——
// 除 `ok` 与 `legacy_hashed` 之外的所有取值均伴随 `valid=false`。
const (
	// ChainReasonOK 表示全链按「当前写入口径」（配置密钥时为 HMAC-SM3）逐条验真通过且锚点连续。
	// ChainReasonOK 是唯一的「完全可信」取值。
	ChainReasonOK = "ok"

	// ChainReasonLegacyHashed 表示链连续且每条记录内容均验真通过，但至少一条记录仅命中
	// 「迁移前历史候选」（无密钥 SM3 / SHA-256 / 本机时区变体），即该存证**写入于密钥化口径之前**，
	// 属于「已验真但待重签」状态，**不是篡改**；配合 `legacy_hashed` 计数即为重签工具的工作量。
	ChainReasonLegacyHashed = "legacy_hashed"

	// ChainReasonTamperedPayload 表示记录的前序锚点与上游 integrity_hash 仍然衔接，
	// 但其自身完整性哈希与任何候选前映像都不匹配——即**链上原位改写了业务字段**
	// （input_hash / output_hash / parameters / user / security_level 之一）。判为无效。
	ChainReasonTamperedPayload = "tampered_payload"

	// ChainReasonHashMismatch 表示记录的完整性哈希与重算期望值不一致，且锚点同时失配
	// （无法归因为「仅内容被改」或「仅锚点被改」的一般性哈希分叉）。判为无效。
	ChainReasonHashMismatch = "hash_mismatch"

	// ChainReasonBrokenChain 表示记录内容验真通过，但其 `prev_hash` 与上一条记录的
	// `integrity_hash` 不衔接——链被重排、上游被替换或记录被删除后重锚。判为无效。
	ChainReasonBrokenChain = "broken_chain"

	// ChainReasonMissingPrev 表示非链首记录携带空的 `prev_hash`（缺失锚点，链起点被抹除）。判为无效。
	ChainReasonMissingPrev = "missing_prev"

	// ChainReasonMissingRecords 表示全量遍历核验通过的记录数小于表内总记录数，
	// 即链中段存在被物理删除的存证。判为无效。
	ChainReasonMissingRecords = "missing_records"

	// ChainReasonInvalidSM2Signature 表示完整性哈希与链式锚点均通过，但记录的 SM2
	// 数字签名无效或无法验证。判为无效（G-10 审计不可否认性）。
	ChainReasonInvalidSM2Signature = "invalid_sm2_signature"
)

// ChainVerificationResult represents the result of cryptographic hash chain verification.
// ChainVerificationResult 封装对账核验防篡改哈希链完整性时的检测报告。
type ChainVerificationResult struct {
	Reason        string `json:"reason"`                  // 机器可读核验结论枚举（P2-4），取值见上方 ChainReason* 常量
	TotalVerified int    `json:"total_verified"`          // 本次核验通过的连续记录总数
	TotalRecords  int    `json:"total_records"`           // 数据库中符合本次核验范围的总记录数（用于检测物理删除）
	Valid         bool   `json:"valid"`                   // 整条哈希链是否完全有效且未被篡改
	BrokenAtID    string `json:"broken_at_id,omitempty"`  // 若断链，记录发生哈希分叉或内容被修改的首个日志 ID
	ExpectedHash  string `json:"expected_hash,omitempty"` // 算法重算得出的期望前序/完整性哈希
	ActualHash    string `json:"actual_hash,omitempty"`   // 数据库中实际读取到的已篡改哈希值
	LegacyHashed  int    `json:"legacy_hashed"`           // 符合旧版规范（SHA-256 或 本地时区）的历史存证记录数
	Message       string `json:"message"`                 // 人类可读核验结论摘要
}

// AuditStore defines the persistence interface for audit logs and snapshots.
// AuditStore 定义审计存证日志、数据快照、统计分析与哈希链核验的持久化存储接口。
type AuditStore interface {
	// SaveLog 持久化单条审计日志。
	SaveLog(log *AuditLog) error

	// SaveLogWithSnapshot 在同一事务中原子保存审计日志及其关联的数据快照。
	SaveLogWithSnapshot(log *AuditLog, snapshot *SnapshotRecord) error

	// SaveLogsBatch 批量原子写入多条审计日志与快照（专为高并发微批刷盘 Flusher 优化）。
	SaveLogsBatch(logs []AuditLog, snapshots []SnapshotRecord) error

	// GetLog 获取指定 ID 的审计日志。
	GetLog(id string) (*AuditLog, error)

	// GetLatestLog 获取当前链尾最新的日志记录（若启用了内存缓冲，包含暂存区最新记录），用于连续哈希链计算。
	GetLatestLog() (*AuditLog, error)

	// ListLogs 根据多条件组合过滤分页查询审计日志列表。
	ListLogs(filter AuditFilter) ([]AuditLog, int, error)

	// GetStats 在数据库引擎层执行聚合统计，获取审计全景指标。
	GetStats() (*AuditStats, error)

	// GenerateReport 生成指定时间周期（如 "24h", "7d", "30d"）的合规审计报告。
	GenerateReport(period string) (*AuditReport, error)

	// SaveSnapshot 单独持久化一条脱敏快照。
	SaveSnapshot(snap *SnapshotRecord) error

	// ListSnapshots 分页查询脱敏快照列表。
	ListSnapshots(limit, offset int) ([]SnapshotRecord, int, error)

	// GetSnapshot 获取指定 ID 的快照详情。
	GetSnapshot(id string) (*SnapshotRecord, error)

	// VerifyChain 从链首到链尾逐条对账核验哈希链完整性与防篡改认证。
	//
	// 【规范化链序 (P2-4)】：所有实现（SQLite / PostgreSQL / 内存）与离线重签工具
	// `repairchain` 一律按 `(seq ASC, timestamp ASC, id ASC)` 回放——
	// `seq` 是单一权威写入者锻造 `prev_hash` 时使用的单调锚点序（内存实现对应切片入队序），
	// 决定链的真正顺序；`(timestamp, id)` 为确定性兜底尾序，保证**同时间戳记录在任何后端
	// 与工具上都以同一顺序回放**，不产生「一处判为断链、另一处判为正常」的伪分叉。
	// 结论必须写入 `ChainVerificationResult.Reason`（取值见 ChainReason* 枚举）。
	VerifyChain(limit int) (*ChainVerificationResult, error)

	// CleanupOld 物理清理早于截止时间的过期审计日志与级联快照。
	CleanupOld(before time.Time) (int64, error)
}

const (
	// DefaultArchivePageSize 是到期存证单批读取的默认条数。
	DefaultArchivePageSize = 500
	// ArchiveIDChunkSize 是按 ID 批量读写时单批绑定的 ID 数上限（远离 SQLite 宿主参数上限）。
	ArchiveIDChunkSize = 500
)

// AuditArchiveReader is an optional capability used by the archive-before-delete retention guard.
// AuditArchiveReader 是可选能力接口：供「先归档后删除」的存证留存红线（P0-8）逐批取档并精确删除。
//
// 两个方法配对使用：调用方每取出一页即完整落盘归档段，再按该页 ID 精确删除，
// 因此每次取页都从「最老到期记录」开始，无需游标，也不会出现「删而未档」。
//
// 实现约束：
//  1. 时间比较语义必须与 CleanupOld 完全一致（严格早于 before），否则会出现「删而未档」；
//  2. FetchOldestForArchive 必须按存证链序（旧→新）返回，并带齐这批日志关联的全部快照；
//  3. DeleteLogsByIDs 必须与 CleanupOld 同样级联删除关联快照。
type AuditArchiveReader interface {
	// FetchOldestForArchive 返回早于 before 的最多 limit 条到期存证日志（旧→新）及其快照。
	FetchOldestForArchive(before time.Time, limit int) ([]AuditLog, []SnapshotRecord, error)

	// DeleteLogsByIDs 精确删除给定 ID 的存证日志及其级联快照，返回实际删除的日志条数。
	DeleteLogsByIDs(ids []string) (int64, error)
}
