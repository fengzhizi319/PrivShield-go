// Package store defines storage interfaces for console Go modules.
// Package store 为控制台各 Go 模块定义存储接口。
//
// 三个接口（TaskStore / DataSourceStore / AuditStore）分别对应
// service-hub / datasource-mgr / audit-log 的核心数据模型。
// 各模块可独立选择内存实现（开发）或 SQLite 实现（生产）。
package store

import (
	"errors"
	"time"
)

// ─────────────────────────────────────────────────────────────
// Task Store (service-hub) / 任务存储
// ─────────────────────────────────────────────────────────────

// Task represents a scheduling task in the pipeline.
type Task struct {
	ID           string     `json:"id"`
	APICode      string     `json:"api_code,omitempty"`      // canonical 业务 API（如 "api1_yibao"）
	DatasourceID string     `json:"datasource_id,omitempty"` // canonical 数据源（如 "ds_yibao"）
	Status       string     `json:"status"`                  // "pending" | "running" | "completed" | "failed"
	Stage        string     `json:"stage"`                   // Current pipeline stage
	Source       string     `json:"source"`                  // Data source name
	Operation    string     `json:"operation"`               // "mask" | "k_anon" | "dp" | "classify" | "none"
	Priority     int        `json:"priority"`                // Higher = sooner
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	DurationMs   int64      `json:"duration_ms"`
	Error        string     `json:"error,omitempty"`
	PayloadJSON  string     `json:"-"`                     // Raw payload (not exposed in JSON)
	RetryCount   int        `json:"retry_count"`           // Number of retry attempts (replaces fragile string matching)
	RetryAfter   *time.Time `json:"retry_after,omitempty"` // Earliest time for next retry (backoff delay)
	TraceID      string     `json:"trace_id,omitempty"`    // Distributed trace ID for end-to-end correlation

	// ── Phase B: Lease fields for multi-replica Hub / 多副本 Hub 租约字段 ──
	LeaseOwner     string     `json:"lease_owner,omitempty"`      // Hub instance that owns the current lease
	LeaseToken     string     `json:"lease_token,omitempty"`      // Unique token for this lease (prevents stale writes)
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"` // When the lease expires (nil = not leased)
	Version        int        `json:"version"`                    // Optimistic concurrency version counter
	MaxRetries     int        `json:"max_retries"`                // Maximum retry attempts allowed (default 3)
}

// TaskFilter specifies filtering criteria for listing tasks.
type TaskFilter struct {
	Status string // Filter by status (empty = all)
	Limit  int    // Max results (0 = unlimited)
	Offset int    // Pagination offset
}

// TaskCounts holds aggregated task counts by status.
type TaskCounts struct {
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

// TaskStore defines the persistence interface for scheduling tasks.
type TaskStore interface {
	Save(task *Task) error
	Get(id string) (*Task, error)
	List(filter TaskFilter) ([]Task, int, error) // returns tasks, total count, error
	Update(task *Task) error
	Counts() (TaskCounts, error)
	CleanupOld(before time.Time) (int64, error) // Delete terminal tasks older than cutoff / 清理过期终态任务
}

// ─────────────────────────────────────────────────────────────
// LeasedTaskStore — Phase B: Atomic task ownership for multi-replica Hub
// 原子任务领取与租约接口，支持多副本 Hub 安全并发调度。
// ─────────────────────────────────────────────────────────────

// ErrLeaseNotSupported is returned by backends that cannot provide lease semantics.
// 当存储后端不支持租约语义时返回此错误（如 SQLite / 内存实现）。
var ErrLeaseNotSupported = errors.New("this store backend does not support multi-replica task leases; use PostgreSQL for Phase B deployment")

// TaskLease wraps a claimed task with its ownership metadata.
// TaskLease 封装已领取的任务及其所有权元数据。
type TaskLease struct {
	Task      *Task     // The claimed task / 已领取的任务
	Owner     string    // Hub instance identifier that claimed this task
	Token     string    // Unique token for this lease (prevents stale writes)
	ExpiresAt time.Time // Absolute lease expiry time
}

// TaskResult encapsulates the successful output of a task.
// TaskResult 封装任务成功执行的结果。
type TaskResult struct {
	OutputJSON string // Serialized output payload
	Stage      string // Final pipeline stage reached (typically "done")
}

// TaskFailure encapsulates the reason a task failed.
// TaskFailure 封装任务失败的原因与分类。
type TaskFailure struct {
	Error      string // Human-readable error description
	Retryable  bool   // Whether the failure is transient and worth retrying
	ErrorClass string // Error classification (e.g. "timeout", "downstream", "internal")
}

// LeasedTaskStore extends TaskStore with atomic lease operations for multi-replica Hub.
// 所有条件更新方法返回 (bool, error)：bool 表示条件更新是否实际取得一行（即是否仍持有所有权）。
// 调用方不得忽略 bool 返回值——当返回 false 时，当前副本已失去任务所有权，必须停止处理。
//
// SQLite / 内存实现返回 ErrLeaseNotSupported；PostgreSQL 实现提供完整原子语义。
type LeasedTaskStore interface {
	TaskStore

	// ClaimNext atomically claims the next pending task for the given owner.
	// 使用 FOR UPDATE SKIP LOCKED 实现无阻塞竞争领取；无可用任务时返回 (nil, nil)。
	ClaimNext(owner string, leaseTTL time.Duration) (*TaskLease, error)

	// RenewLease extends the lease for a task, conditional on ownership and non-expiry.
	// 返回 false 表示租约已过期或所有权已丢失。
	RenewLease(id, owner, token string, leaseTTL time.Duration) (bool, error)

	// CompleteLease marks a task as completed, conditional on ownership.
	// 返回 false 表示当前副本已失去所有权。
	CompleteLease(id, owner, token string, result TaskResult) (bool, error)

	// FailLease marks a task as failed, conditional on ownership.
	// 若 failure.Retryable 为 true 且重试次数未耗尽，任务回退为 pending。
	// 返回 false 表示当前副本已失去所有权。
	FailLease(id, owner, token string, failure TaskFailure) (bool, error)

	// RequeueExpiredLeases reclaims tasks whose lease has expired.
	// 返回回收的任务数量。
	RequeueExpiredLeases(limit int) (int, error)
}

// ─────────────────────────────────────────────────────────────
// DataSource Store (datasource-mgr) / 数据源存储
// ─────────────────────────────────────────────────────────────

// DataSource represents a registered data source.
type DataSource struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Type          string     `json:"type"` // "database" | "api" | "file"
	Host          string     `json:"host"`
	Port          int        `json:"port"`
	Database      string     `json:"database"`
	SecurityLevel string     `json:"security_level"` // "high" | "medium" | "low"
	Status        string     `json:"status"`         // "connected" | "disconnected" | "error"
	CreatedAt     time.Time  `json:"created_at"`
	LastCheckAt   *time.Time `json:"last_check_at"`
	TagsJSON      string     `json:"-"`    // JSON-encoded tags
	Tags          []string   `json:"tags"` // Business tags
}

// AccessAuditRecord represents an access audit log entry.
type AccessAuditRecord struct {
	ID             string    `json:"id"`
	DataSourceID   string    `json:"datasource_id"`
	DataSourceName string    `json:"datasource_name"`
	Operation      string    `json:"operation"`
	User           string    `json:"user"`
	Timestamp      time.Time `json:"timestamp"`
	RecordsCount   int       `json:"records_count"`
	Status         string    `json:"status"`
}

// DataSourceFilter specifies filtering/pagination criteria for listing data sources.
// P28 fix: push pagination to SQL level instead of in-memory slicing.
type DataSourceFilter struct {
	Limit  int // Max results (0 = unlimited)
	Offset int // Pagination offset
}

// DataSourceStore defines the persistence interface for data sources.
type DataSourceStore interface {
	SaveDS(ds *DataSource) error
	GetDS(id string) (*DataSource, error)
	ListDS(filter DataSourceFilter) ([]DataSource, int, error) // returns datasources, total count, error
	DeleteDS(id string) error
	UpdateDS(ds *DataSource) error

	SaveAudit(rec *AccessAuditRecord) error
	ListAudit(dsID string, limit, offset int) ([]AccessAuditRecord, int, error) // returns records, total count, error
}

// ─────────────────────────────────────────────────────────────
// Audit Store (audit-log) / 审计日志存储
// ─────────────────────────────────────────────────────────────

// AuditLog represents a single audit log entry.
type AuditLog struct {
	ID             string    `json:"id"`
	TaskID         string    `json:"task_id,omitempty"`       // 所属调度流水线任务 ID
	APICode        string    `json:"api_code,omitempty"`      // canonical 业务 API（如 "api1_yibao"）
	DatasourceID   string    `json:"datasource_id,omitempty"` // canonical 数据源 ID（如 "ds_yibao"）
	Timestamp      time.Time `json:"timestamp"`
	Operation      string    `json:"operation"`
	DataSource     string    `json:"datasource"` // 兼容历史字段，与 DatasourceID 保持一致
	InputHash      string    `json:"input_hash"`
	OutputHash     string    `json:"output_hash"`
	Algorithm      string    `json:"algorithm"`
	ParametersJSON string    `json:"-"`
	Parameters     any       `json:"parameters"`
	InputRows      int       `json:"input_rows"`
	OutputRows     int       `json:"output_rows"`
	DurationMs     int64     `json:"duration_ms"`
	User           string    `json:"user"`
	Status         string    `json:"status"`
	ErrorMessage   string    `json:"error,omitempty"`
	SecurityLevel  string    `json:"security_level"`
	PrevHash       string    `json:"prev_hash,omitempty"`      // 前序存证哈希（形成防篡改哈希链）
	IntegrityHash  string    `json:"integrity_hash,omitempty"` // 本条记录的综合密码学完整性哈希
}

// AuditFilter specifies filtering criteria for listing audit logs.
type AuditFilter struct {
	TaskID        string
	APICode       string
	DatasourceID  string
	Operation     string
	DataSource    string
	User          string
	Status        string
	SecurityLevel string
	Limit         int
	Offset        int
}

// SnapshotRecord represents a desensitization snapshot for evidence.
type SnapshotRecord struct {
	ID             string    `json:"id"`
	AuditLogID     string    `json:"audit_log_id"`
	Timestamp      time.Time `json:"timestamp"`
	InputSample    string    `json:"input_sample"`
	OutputSample   string    `json:"output_sample"`
	Algorithm      string    `json:"algorithm"`
	ParametersJSON string    `json:"-"`
	Parameters     any       `json:"parameters"`
	IntegrityHash  string    `json:"integrity_hash"`
	PrevHash       string    `json:"prev_hash,omitempty"` // 关联的前序哈希
}

// AuditStats holds aggregated audit statistics.
// P31 fix: SQL-level aggregation instead of loading 10k records into memory.
type AuditStats struct {
	TotalOperations int            `json:"total_operations"`
	ByOperation     map[string]int `json:"by_operation"`
	ByStatus        map[string]int `json:"by_status"`
	BySecurityLevel map[string]int `json:"by_security_level"`
	AvgDurationMs   float64        `json:"avg_duration_ms"`
}

// AuditReport holds compliance audit report data.
// P33 fix: SQL-level filtering and aggregation instead of loading 10k records.
type AuditReport struct {
	TotalOperations int            `json:"total_operations"`
	SuccessRate     float64        `json:"success_rate"`
	BySecurityLevel map[string]int `json:"by_security_level"`
	TopOperations   []string       `json:"top_operations"`
	Recommendations []string       `json:"recommendations"`
}

// ChainVerificationResult represents the result of cryptographic hash chain verification.
type ChainVerificationResult struct {
	TotalVerified int    `json:"total_verified"`
	Valid         bool   `json:"valid"`
	BrokenAtID    string `json:"broken_at_id,omitempty"`
	ExpectedHash  string `json:"expected_hash,omitempty"`
	ActualHash    string `json:"actual_hash,omitempty"`
	// LegacyHashed counts records that only authenticated under a pre-migration convention
	// (SHA-256, or a local-zone timestamp) and still need re-signing under canonical SM3.
	LegacyHashed int    `json:"legacy_hashed"`
	Message      string `json:"message"`
}

// AuditStore defines the persistence interface for audit logs and snapshots.
type AuditStore interface {
	SaveLog(log *AuditLog) error
	SaveLogWithSnapshot(log *AuditLog, snapshot *SnapshotRecord) error
	SaveLogsBatch(logs []AuditLog, snapshots []SnapshotRecord) error // 高并发批量刷盘支持
	GetLog(id string) (*AuditLog, error)
	// GetLatestLog returns the newest tail log in the chain (including staged in-memory records if buffered).
	// 获取防篡改哈希链当前链尾的最新日志（若启用了微批缓冲，包含内存中待落盘记录），用于构建连续防篡改哈希链。
	GetLatestLog() (*AuditLog, error)
	ListLogs(filter AuditFilter) ([]AuditLog, int, error)
	GetStats() (*AuditStats, error)                     // P31: SQL-level aggregation
	GenerateReport(period string) (*AuditReport, error) // P33: SQL-level filtering + aggregation

	SaveSnapshot(snap *SnapshotRecord) error
	ListSnapshots(limit, offset int) ([]SnapshotRecord, int, error) // P35: return total count for pagination
	GetSnapshot(id string) (*SnapshotRecord, error)
	VerifyChain(limit int) (*ChainVerificationResult, error) // 全局/区间防篡改哈希链对账核验
	CleanupOld(before time.Time) (int64, error)              // Delete audit logs older than cutoff / 清理过期审计日志
}
