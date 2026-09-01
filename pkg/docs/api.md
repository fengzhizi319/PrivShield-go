# PrivShield 共享基础包 (Shared PKG) — 编程接口与 API 规约手册

> **文档定位**：`pkg` 模块中所有公共包、结构体、接口契约、函数签名、错误码定义及集成调用范例的权威技术手册。
> **面向对象**：`PrivShield` 全栈微服务开发者、SDK 开发者与中台运维研发人员。

---

## 目录

- [一、国密密码学原语与信封加密体系 (`pkg/crypto`)](#一国密密码学原语与信封加密体系-pkgcrypto)
- [二、持久化与微批存储引擎 (`pkg/store` & `pkg/store/flusher`)](#二持久化与微批存储引擎-pkgstore--pkgstoreflusher)
- [三、纵深防御中间件与信封协议 (`pkg/middleware`)](#三纵深防御中间件与信封协议-pkgmiddleware)
- [四、上游 Agent 客户端与熔断器 (`pkg/agent`)](#四上游-agent-客户端与熔断器-pkgagent)
- [五、全链路可观测性与指标采集 (`pkg/metrics`)](#五全链路可观测性与指标采集-pkgmetrics)
- [六、国密 mTLS 与 CN 白名单 (`pkg/tlsutil`)](#六国密-mtls-与-cn-白名单-pkgtlsutil)
- [七、环境配置与安全基线校验 (`pkg/config`)](#七环境配置与安全基线校验-pkgconfig)
- [八、命名治理与输入校验 (`pkg/naming` & `pkg/validation`)](#八命名治理与输入校验-pkgnaming--pkgvalidation)
- [九、调度中枢 `service-hub` 全阶段公共 PKG 调用规范](#九调度中枢-service-hub-全阶段公共-pkg-调用规范)

---

## 一、国密密码学原语与信封加密体系 (`pkg/crypto`)

`pkg/crypto` 提供符合国密标准（GM/T 0004-2012 / GM/T 0002-2012）的密码学原语与动态信封加密实现，包含纯 Go 实现的 SM3 哈希与 SM4 分组密码。

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/crypto"
```

### 1.1 常量定义

```go
// 信封加密版本前缀
const EncryptedPrefix   = "enc:v1:"  // V1：SHA-256 密钥派生
const EncryptedPrefixV2 = "enc:v2:"  // V2：HKDF-SM3 密钥派生
const NonceSize         = 12         // GCM 随机数长度（字节）

// SM3 哈希算法常量
const SM3BlockSize = 64  // SM3 分组长度（字节）
const SM3Size      = 32  // SM3 摘要长度（字节）

// SM4 分组密码常量
const BlockSize = 16  // SM4 分组长度（字节）
const KeySize   = 16  // SM4 密钥长度（字节）
const Rounds    = 32  // SM4 轮数
```

### 1.2 哨兵错误

```go
var ErrEmptyKey        = errors.New("crypto: encryption key is not configured")
var ErrUnencryptedValue = errors.New("crypto: value is not envelope-encrypted (missing enc:v1:/enc:v2: prefix)")
```

### 1.3 SM3 哈希函数

```go
// NewSM3 返回国密 SM3 哈希引擎（实现 hash.Hash 接口）
func NewSM3() hash.Hash

// SumSM3 计算输入数据的 32 字节国密 SM3 原始摘要
func SumSM3(data []byte) [SM3Size]byte

// SumSM3Hex 计算输入数据的 64 字符十六进制国密 SM3 摘要
func SumSM3Hex(data []byte) string

// HMACSM3 计算基于国密 SM3 的 HMAC 消息认证码（32 字节原始值）
func HMACSM3(key, data []byte) [SM3Size]byte

// HMACSM3Hex 计算基于国密 SM3 的 HMAC 消息认证码（64 字符十六进制字符串）
func HMACSM3Hex(key, data []byte) string
```

### 1.4 SM4 分组密码

```go
// NewCipher 使用给定密钥创建 SM4 分组密码器（实现 cipher.Block 接口）
// 密钥长度必须为 16 字节
func NewCipher(key []byte) (cipher.Block, error)
```

### 1.5 信封加密与密钥派生

```go
// DeriveKey 使用 SHA-256 从口令派生 16 字节 SM4 密钥（V1 格式）
func DeriveKey(secret string) []byte

// DeriveKeyHKDF 使用 HKDF-SM3 从口令与盐值派生 16 字节 SM4 密钥（V2 格式）
func DeriveKeyHKDF(secret string, salt []byte) []byte

// EncryptString 使用 SM4-GCM 动态信封加密敏感字符串
// V1 格式返回 "enc:v1:<Base64>"，V2 格式返回 "enc:v2:<Base64>"
func EncryptString(plaintext, secret string) (string, error)

// DecryptString 解密 SM4-GCM 动态信封密文（自动识别 V1/V2 前缀）
func DecryptString(ciphertext, secret string) (string, error)

// IsEncrypted 判断字符串是否为信封加密格式（识别 enc:v1: 和 enc:v2: 前缀）
func IsEncrypted(value string) bool
```

### 1.6 代码调用范例

```go
// 1. SM3 哈希
digest := crypto.SumSM3([]byte("hello"))
hexStr := crypto.SumSM3Hex([]byte("hello"))

// 2. HMAC-SM3 消息认证码
mac := crypto.HMACSM3Hex([]byte("my-key"), []byte("my-data"))

// 3. SM4-GCM 信封加密
masterKey := "my-secret-key-12345"
encrypted, err := crypto.EncryptString("张三 (110101199003072345)", masterKey)
if err != nil {
    log.Fatal(err)
}
// 输出格式: enc:v1:7xX...== 或 enc:v2:...

// 4. 透明解密
plain, err := crypto.DecryptString(encrypted, masterKey)

// 5. 检测是否为密文
if crypto.IsEncrypted(value) {
    decrypted, _ := crypto.DecryptString(value, masterKey)
}
```

---

## 二、持久化与微批存储引擎 (`pkg/store` & `pkg/store/flusher`)

### 2.1 核心数据结构 (`pkg/store/store.go`)

```go
// ── 任务调度实体 ──

// Task 流水线任务实体
type Task struct {
    ID             string     `json:"id"`
    APICode        string     `json:"api_code,omitempty"`
    DatasourceID   string     `json:"datasource_id,omitempty"`
    Status         string     `json:"status"`
    Stage          string     `json:"stage"`
    Source         string     `json:"source"`
    Operation      string     `json:"operation"`
    Priority       int        `json:"priority"`
    CreatedAt      time.Time  `json:"created_at"`
    StartedAt      *time.Time `json:"started_at"`
    CompletedAt    *time.Time `json:"completed_at"`
    DurationMs     int64      `json:"duration_ms"`
    Error          string     `json:"error,omitempty"`
    ErrorClass     string     `json:"error_class,omitempty"`
    PayloadJSON    string     `json:"-"`
    RetryCount     int        `json:"retry_count"`
    RetryAfter     *time.Time `json:"retry_after,omitempty"`
    TraceID        string     `json:"trace_id,omitempty"`
    LeaseOwner     string     `json:"lease_owner,omitempty"`
    LeaseToken     string     `json:"lease_token,omitempty"`
    LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
    Version        int        `json:"version"`
    MaxRetries     int        `json:"max_retries"`
}

// TaskFilter 任务查询过滤条件
type TaskFilter struct {
    Status string
    Limit  int
    Offset int
}

// TaskCounts 各状态任务计数
type TaskCounts struct {
    Pending   int `json:"pending"`
    Running   int `json:"running"`
    Completed int `json:"completed"`
    Failed    int `json:"failed"`
}

// TaskLease 租约领取结果
type TaskLease struct {
    Task      *Task
    Owner     string
    Token     string
    ExpiresAt time.Time
}

// TaskResult 租约完成提交结果
type TaskResult struct {
    OutputJSON string
    Stage      string
}

// TaskFailure 租约失败上报
type TaskFailure struct {
    Error      string
    Retryable  bool
    ErrorClass string
}

// ── 数据源资产管理 ──

// DataSource 数据源资产实体
type DataSource struct {
    ID            string     `json:"id"`
    Name          string     `json:"name"`
    Type          string     `json:"type"`
    Host          string     `json:"host"`
    Port          int        `json:"port"`
    Database      string     `json:"database"`
    SecurityLevel string     `json:"security_level"`
    Status        string     `json:"status"`
    CreatedAt     time.Time  `json:"created_at"`
    LastCheckAt   *time.Time `json:"last_check_at"`
    TagsJSON      string     `json:"-"`
    Tags          []string   `json:"tags"`
}

// AccessAuditRecord 数据源访问审计记录
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

// DataSourceFilter 数据源查询过滤条件
type DataSourceFilter struct {
    Limit  int
    Offset int
}

// ── 审计存证实体 ──

// AuditLog 审计日志实体
type AuditLog struct {
    ID             string    `json:"id"`
    TaskID         string    `json:"task_id,omitempty"`
    APICode        string    `json:"api_code,omitempty"`
    DatasourceID   string    `json:"datasource_id,omitempty"`
    Timestamp      time.Time `json:"timestamp"`
    Operation      string    `json:"operation"`
    DataSource     string    `json:"datasource"`
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
    PrevHash       string    `json:"prev_hash,omitempty"`      // 前序区块哈希
    IntegrityHash  string    `json:"integrity_hash,omitempty"` // 9 要素 SM3 完整性哈希
}

// AuditFilter 审计日志查询过滤条件
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

// SnapshotRecord 快照存证记录
type SnapshotRecord struct {
    ID             string    `json:"id"`
    AuditLogID     string    `json:"audit_log_id"`
    Timestamp      time.Time `json:"timestamp"`
    InputSample    string    `json:"input_sample"`  // 存储时执行 SM4-GCM 信封加密
    OutputSample   string    `json:"output_sample"` // 存储时执行 SM4-GCM 信封加密
    Algorithm      string    `json:"algorithm"`
    ParametersJSON string    `json:"-"`
    Parameters     any       `json:"parameters"`
    IntegrityHash  string    `json:"integrity_hash"`
    PrevHash       string    `json:"prev_hash,omitempty"`
}

// AuditStats 审计统计摘要
type AuditStats struct {
    TotalOperations int            `json:"total_operations"`
    ByOperation     map[string]int `json:"by_operation"`
    ByStatus        map[string]int `json:"by_status"`
    BySecurityLevel map[string]int `json:"by_security_level"`
    AvgDurationMs   float64        `json:"avg_duration_ms"`
}

// AuditReport 审计报告
type AuditReport struct {
    TotalOperations int            `json:"total_operations"`
    SuccessRate     float64        `json:"success_rate"`
    BySecurityLevel map[string]int `json:"by_security_level"`
    TopOperations   []string       `json:"top_operations"`
    Recommendations []string       `json:"recommendations"`
}

// ChainVerificationResult 链式验真结果
type ChainVerificationResult struct {
    Reason        string `json:"reason"`
    TotalVerified int    `json:"total_verified"`
    TotalRecords  int    `json:"total_records"`
    Valid         bool   `json:"valid"`
    BrokenAtID    string `json:"broken_at_id,omitempty"`
    ExpectedHash  string `json:"expected_hash,omitempty"`
    ActualHash    string `json:"actual_hash,omitempty"`
    LegacyHashed  int    `json:"legacy_hashed"`
    Message       string `json:"message"`
}
```

### 2.2 链式验真原因常量

```go
const (
    ChainReasonOK              = "ok"               // 链路完整
    ChainReasonLegacyHashed    = "legacy_hashed"     // 早期历史哈希格式
    ChainReasonTamperedPayload = "tampered_payload"  // 载荷被篡改
    ChainReasonHashMismatch    = "hash_mismatch"     // 哈希不匹配
    ChainReasonBrokenChain     = "broken_chain"      // 链路断裂
    ChainReasonMissingPrev     = "missing_prev"      // 前序记录缺失
    ChainReasonMissingRecords  = "missing_records"   // 记录缺失
)
```

### 2.3 哈希算法标签常量 (`pkg/store/audit_hash.go`)

```go
const (
    AuditHashSM3     = "SM3"           // 国密 SM3 纯哈希
    AuditHashSM3HMAC = "SM3-HMAC:v1"  // 国密 SM3-HMAC 带密钥哈希
    AuditHashSHA256  = "SHA256"        // 旧版 SHA-256（兼容历史数据）
    LegacyHashSuffix = "-LEGACY"       // 旧版哈希后缀标记
)
```

### 2.4 哨兵错误

```go
// SQLite 后端不支持多副本租约时返回
var ErrLeaseNotSupported = errors.New("this store backend does not support multi-replica task leases; use PostgreSQL for Phase B deployment")
```

### 2.5 审计链密钥管理

```go
// SetAuditChainKey 设置审计链 HMAC-SM3 密钥（服务启动时调用）
func SetAuditChainKey(key string)

// AuditChainKey 获取当前审计链密钥
func AuditChainKey() string
```

### 2.6 完整性哈希计算与验真

```go
// ComputeAuditIntegrityHash 使用标准 UTC 纳秒前像格式计算 9 要素防篡改审计哈希
func ComputeAuditIntegrityHash(logID, prevHash string, timestamp time.Time,
    algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) string

// ComputeAuditIntegrityHashAlgo 返回当前使用的哈希算法标签
func ComputeAuditIntegrityHashAlgo() string

// VerifyAuditIntegrityHash 验证存证记录的完整性哈希（自动适配 SM3 / SM3-HMAC / Legacy SHA-256）
// 返回值：(valid: 是否有效, algoLabel: 实际使用的算法标签)
func VerifyAuditIntegrityHash(stored, logID, prevHash string, timestamp time.Time,
    algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) (bool, string)

// IsCanonicalHashLabel 判断给定标签是否为规范哈希算法标识
func IsCanonicalHashLabel(label string) bool

// ComputeSnapshotIntegrityHash 计算快照存证记录的完整性哈希
func ComputeSnapshotIntegrityHash(snapshotID, auditLogID, prevHash string, timestamp time.Time,
    algorithm, inputSample, outputSample, parametersJSON string) string

// VerifySnapshotIntegrityHash 验证快照存证记录的完整性哈希
// 返回值：(valid: 是否有效, algoLabel: 实际使用的算法标签)
func VerifySnapshotIntegrityHash(stored, snapshotID, auditLogID, prevHash string, timestamp time.Time,
    algorithm, inputSample, outputSample, parametersJSON string) (bool, string)
```

### 2.7 存储接口契约

#### `TaskStore` 接口
```go
type TaskStore interface {
    Save(task *Task) error
    Get(id string) (*Task, error)
    List(filter TaskFilter) ([]Task, int, error)
    Update(task *Task) error
    Counts() (TaskCounts, error)
    CleanupOld(before time.Time) (int64, error)
}
```

#### `LeasedTaskStore` 接口 (PostgreSQL Phase B 原子租约)
```go
type LeasedTaskStore interface {
    TaskStore
    // FOR UPDATE SKIP LOCKED 无阻塞原子竞争领取任务
    ClaimNext(owner string, leaseTTL time.Duration) (*TaskLease, error)
    // 续约租约 (CAS 校验 owner 和 token)
    RenewLease(id, owner, token string, leaseTTL time.Duration) (bool, error)
    // 完成任务并释放租约
    CompleteLease(id, owner, token string, result TaskResult) (bool, error)
    // 标记失败并支持指数重试
    FailLease(id, owner, token string, failure TaskFailure) (bool, error)
    // 回收超时孤立租约
    RequeueExpiredLeases(limit int) (int, error)
}
```

#### `AuditStore` 接口
```go
type AuditStore interface {
    SaveLog(log *AuditLog) error
    SaveLogWithSnapshot(log *AuditLog, snapshot *SnapshotRecord) error
    SaveLogsBatch(logs []AuditLog, snapshots []SnapshotRecord) error
    GetLog(id string) (*AuditLog, error)
    GetLatestLog() (*AuditLog, error)
    ListLogs(filter AuditFilter) ([]AuditLog, int, error)
    GetStats() (*AuditStats, error)
    GenerateReport(period string) (*AuditReport, error)
    SaveSnapshot(snap *SnapshotRecord) error
    ListSnapshots(limit, offset int) ([]SnapshotRecord, int, error)
    GetSnapshot(id string) (*SnapshotRecord, error)
    VerifyChain(limit int) (*ChainVerificationResult, error)
    CleanupOld(before time.Time) (int64, error)
}
```

#### `AuditArchiveReader` 接口（归档冷数据读取）
```go
type AuditArchiveReader interface {
    // FetchOldestForArchive 获取指定时间之前的最旧审计日志与快照记录（分页归档用）
    FetchOldestForArchive(before time.Time, limit int) ([]AuditLog, []SnapshotRecord, error)
    // DeleteLogsByIDs 按 ID 批量删除审计日志（归档清理用）
    DeleteLogsByIDs(ids []string) (int64, error)
}
```

#### `DataSourceStore` 接口（数据源资产管理）
```go
type DataSourceStore interface {
    SaveDS(ds *DataSource) error
    GetDS(id string) (*DataSource, error)
    ListDS(filter DataSourceFilter) ([]DataSource, int, error)
    DeleteDS(id string) error
    UpdateDS(ds *DataSource) error
    SaveAudit(rec *AccessAuditRecord) error
    ListAudit(dsID string, limit, offset int) ([]AccessAuditRecord, int, error)
}
```

### 2.8 高并发微批缓冲刷盘器 (`pkg/store/flusher`)

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/store/flusher"
```

#### 哨兵错误
```go
var ErrStoreClosed      = errors.New("audit store is closed")
var ErrBacklogSaturated = errors.New("audit flush backlog saturated, underlying storage unavailable")
```

#### 配置结构
```go
// Config 微批刷盘器配置（8 字段完整定义）
type Config struct {
    BufferSize     int           // 内存队列最大容量
    MaxBatchSize   int           // 单批落盘最大记录数
    FlushInterval  time.Duration // 强制刷盘时间窗口
    EnqueueTimeout time.Duration // 入队超时
    FlushTimeout   time.Duration // 刷盘超时
    CloseTimeout   time.Duration // 优雅关闭超时
    MaxRetries     int           // 最大重试次数
    MaxStaged      int           // 最大暂存记录数
}

// DefaultConfig 返回推荐的默认配置
func DefaultConfig() Config
```

#### 构造与核心方法
```go
// NewBufferedAuditStore 创建缓冲微批包装器
func NewBufferedAuditStore(underlying store.AuditStore, cfg Config, logger *slog.Logger) *BufferedAuditStore

// 实现 store.AuditStore 全部方法 + store.AuditArchiveReader 接口
func (b *BufferedAuditStore) SaveLog(log *store.AuditLog) error
func (b *BufferedAuditStore) SaveLogWithSnapshot(log *store.AuditLog, snapshot *store.SnapshotRecord) error
func (b *BufferedAuditStore) SaveLogsBatch(logs []store.AuditLog, snapshots []store.SnapshotRecord) error
func (b *BufferedAuditStore) GetLog(id string) (*store.AuditLog, error)
func (b *BufferedAuditStore) GetLatestLog() (*store.AuditLog, error)
func (b *BufferedAuditStore) ListLogs(filter store.AuditFilter) ([]store.AuditLog, int, error)
func (b *BufferedAuditStore) GetStats() (*store.AuditStats, error)
func (b *BufferedAuditStore) GenerateReport(period string) (*store.AuditReport, error)
func (b *BufferedAuditStore) SaveSnapshot(snap *store.SnapshotRecord) error
func (b *BufferedAuditStore) ListSnapshots(limit, offset int) ([]store.SnapshotRecord, int, error)
func (b *BufferedAuditStore) GetSnapshot(id string) (*store.SnapshotRecord, error)
func (b *BufferedAuditStore) VerifyChain(limit int) (*store.ChainVerificationResult, error)
func (b *BufferedAuditStore) CleanupOld(before time.Time) (int64, error)
func (b *BufferedAuditStore) FetchOldestForArchive(before time.Time, limit int) ([]store.AuditLog, []store.SnapshotRecord, error)
func (b *BufferedAuditStore) DeleteLogsByIDs(ids []string) (int64, error)

// Flush 立即强制刷盘
func (b *BufferedAuditStore) Flush() error

// Close 优雅关闭并排空队列
func (b *BufferedAuditStore) Close() error

// ── 运行时诊断指标 ──

func (b *BufferedAuditStore) QueueDepth() int        // 当前队列深度
func (b *BufferedAuditStore) FlushedTotal() int64     // 累计成功刷盘记录数
func (b *BufferedAuditStore) FailedTotal() int64      // 累计失败记录数
func (b *BufferedAuditStore) OverflowTotal() int64    // 累计溢出丢弃记录数
func (b *BufferedAuditStore) EvictedTotal() int64     // 累计淘汰记录数
func (b *BufferedAuditStore) RetryPending() int64     // 当前待重试记录数
func (b *BufferedAuditStore) StagedCount() int        // 当前暂存记录数
func (b *BufferedAuditStore) HasFlushError() bool     // 是否存在刷盘错误
func (b *BufferedAuditStore) LastFlushError() string  // 最近一次刷盘错误信息
```

#### 调用范例

```go
cfg := flusher.DefaultConfig()
cfg.BufferSize = 2000
cfg.MaxBatchSize = 200
cfg.FlushInterval = 20 * time.Millisecond

bufferedStore := flusher.NewBufferedAuditStore(underlyingStore, cfg, logger)
defer bufferedStore.Close()

// 保存日志（非阻塞入队 + 读己之写即时可见）
err := bufferedStore.SaveLog(logEntry)

// 运行时诊断
depth := bufferedStore.QueueDepth()
if bufferedStore.HasFlushError() {
    logger.Error("flush error detected", "last_error", bufferedStore.LastFlushError())
}

// 链式验真（全局连续无断点）
res, err := bufferedStore.VerifyChain(1000)
```

---

## 三、纵深防御中间件与信封协议 (`pkg/middleware`)

### 3.1 统一 API 响应与错误信封 (`pkg/middleware/envelope.go`)

`PrivShield` 遵循统一的跨语言 API 信封协议：

#### 成功响应信封结构
```go
type SuccessEnvelope struct {
    Code      string `json:"code"`       // 业务状态码（如 "SUCCESS"）
    Message   string `json:"message"`    // 人类可读描述
    Data      any    `json:"data,omitempty"` // 业务数据载荷
    TraceID   string `json:"trace_id"`   // 全局追踪 ID
    Timestamp string `json:"timestamp"`  // UTC 纳秒时间戳
}
```

#### 错误响应信封结构
```go
type ErrorEnvelope struct {
    Code      string `json:"code"`       // 错误码（如 "INVALID_ARGUMENT"）
    Message   string `json:"message"`    // 人类可读错误描述
    Detail    any    `json:"detail,omitempty"` // 附加错误详情
    TraceID   string `json:"trace_id"`   // 全局追踪 ID
    Timestamp string `json:"timestamp"`  // UTC 纳秒时间戳
}
```

#### 成功响应示例
```json
{
  "code": "SUCCESS",
  "message": "Operation completed successfully",
  "data": { ... },
  "trace_id": "req-1725091200-a1b2c3d4",
  "timestamp": "2025-01-15T08:00:00.000000000Z"
}
```

#### 错误响应示例
```json
{
  "error": {
    "code": "INVALID_ARGUMENT",
    "message": "parameters too large: 1048576 bytes (max 65536 bytes)",
    "detail": null,
    "trace_id": "req-1725091200-a1b2c3d4",
    "timestamp": "2025-01-15T08:00:00.000000000Z"
  }
}
```

### 3.2 信封辅助函数

```go
// AbortWithError 中断请求并输出标准错误信封
func AbortWithError(c *gin.Context, httpStatus int, code string, message string, detail any)

// RespondWithSuccess 输出标准成功信封
func RespondWithSuccess(c *gin.Context, httpStatus int, message string, data any)

// ErrorCodeFromStatus 根据 HTTP 状态码映射标准错误码
// 400→INVALID_ARGUMENT, 401→UNAUTHORIZED, 403→FORBIDDEN, 404→NOT_FOUND,
// 409→CONFLICT, 413→PAYLOAD_TOO_LARGE, 429→RATE_LIMITED,
// 500→INTERNAL_ERROR, 503→UPSTREAM_UNAVAILABLE, 其他→UNKNOWN_ERROR
func ErrorCodeFromStatus(status int) string

// ExtractErrorMessage 从 Gin 上下文中提取错误消息（按优先级查找）
func ExtractErrorMessage(c *gin.Context, fallback string) string
```

### 3.3 认证中间件

```go
// Auth 常量时间校验 Authorization: Bearer <Key>
// 注意：/metrics 端点同样需要认证（不再豁免）
func Auth(apiKey string) gin.HandlerFunc

// ReadOnlyEndpoint 定义只读端点（用于分级认证）
type ReadOnlyEndpoint struct {
    Method string  // HTTP 方法（如 "GET"）
    Path   string  // 路径模式（如 "/api/audit/logs"）
}

// AuthWithRoles 分级角色认证：主密钥拥有完全权限，只读密钥仅可访问指定端点
func AuthWithRoles(apiKey, readerKey string, readOnly []ReadOnlyEndpoint) gin.HandlerFunc
```

### 3.4 中间件栈（8 层纵深防御）

> 注意：日志记录由 `pkg/observability.RequestLogger` 统一提供，不再作为独立中间件层。

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/middleware"

router := gin.New()

// 1. 分布式追踪：提取/生成 X-Request-ID 注入上下文
router.Use(middleware.TraceMiddleware())

// 2. Panic 恢复：拦截 Handler 运行时 Panic，防止服务进程崩溃
router.Use(middleware.Recovery(logger, "service-hub"))

// 3. 安全响应头：注入 CSP、HSTS、X-Content-Type-Options 等
router.Use(middleware.SecurityHeaders())

// 4. 请求体限制：防御超大报文耗尽内存（默认 32 MiB）
router.Use(middleware.MaxBodySize(32 * 1024 * 1024))

// 5. 并发控制：限制在途活跃并发请求数（默认 1000）
router.Use(middleware.MaxConcurrent(1000))

// 6. 令牌桶限流：基于客户端 IP 的 32 分片精确限流
router.Use(middleware.RateLimit(100, 200)) // 100 RPS, 200 Burst

// 7. 跨域安全：跨域来源白名单校验与 OPTIONS 预检放行
router.Use(middleware.CORS([]string{"http://localhost:3000"}))

// 8. 认证校验：常量时间校验 Bearer Token
router.Use(middleware.Auth(cfg.APIKey))
// 或使用分级认证：
// router.Use(middleware.AuthWithRoles(cfg.APIKey, cfg.ReaderKey, readOnlyEndpoints))
```

#### 中间件防护行为一览

| 顺序 | 中间件 | 功能说明 | 违规拦截响应 |
|:---|:---|:---|:---|
| 1 | `TraceMiddleware()` | 提取/生成 `X-Request-ID`，注入响应头 `X-Trace-ID` | 注入追踪头 |
| 2 | `Recovery(logger, module)` | 拦截 Handler Panic，结构化日志记录 | `500 INTERNAL_ERROR` |
| 3 | `SecurityHeaders()` | 注入 CSP、HSTS、X-Frame-Options 等安全头 | — |
| 4 | `MaxBodySize(maxBytes)` | 限制请求体大小，防御报文洪泛 | `413 PAYLOAD_TOO_LARGE` |
| 5 | `MaxConcurrent(limit)` | 控制在途并发数，超载保护 | `503 SERVICE_UNAVAILABLE` |
| 6 | `RateLimit(rps, burst)` | 32 分片令牌桶精确限流 | `429 RATE_LIMITED` |
| 7 | `CORS(origins)` | 跨域来源白名单与预检放行 | 阻断跨域访问 |
| 8 | `Auth(apiKey)` | 常量时间 Bearer Token 校验 | `401 UNAUTHORIZED` |

### 3.5 限流路径规范化与辅助函数

```go
// NormalizeRateLimitPath 将路径中的纯数字段和 UUID 段替换为 ":id"，防止基数膨胀
// 例: "/api/tasks/12345" → "/api/tasks/:id"
// 例: "/api/users/550e8400-e29b-41d4-a716-446655440000" → "/api/users/:id"
func NormalizeRateLimitPath(path string) string

// IsAllDigits 判断字符串是否全部由数字组成
func IsAllDigits(s string) bool

// IsUUIDFormat 判断字符串是否为标准 UUID 格式（8-4-4-4-12）
func IsUUIDFormat(s string) bool
```

### 3.6 高级限流与请求 ID

```go
// RateLimitWithKeyFunc 自定义限流键提取函数的高级限流中间件
// 自动豁免 /health 和 /api/health 健康检查路径
func RateLimitWithKeyFunc(rps int, burst int, keyFunc func(*gin.Context) string) gin.HandlerFunc

// RequestID 生成/提取请求 ID 中间件（独立于 TraceMiddleware）
func RequestID() gin.HandlerFunc
```

---

## 四、上游 Agent 客户端与熔断器 (`pkg/agent`)

`pkg/agent.Client` 用于向 Go 隐私计算核心引擎（`engine-go/cmd/privshield-agent`）下发任务，内置**每端点独立熔断器**、多端点轮询、幂等保护、超时控制与链路追踪透传。

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/agent"
```

### 4.1 配置结构

```go
// Config Agent 客户端配置
type Config struct {
    BaseURL        string        // 主端点 URL（兼容单节点场景）
    BaseURLs       []string      // 多端点 URL 列表（负载均衡场景）
    APIKey         string        // API 认证密钥
    Timeout        time.Duration // 请求超时（默认 30s）
    CBThreshold    int           // 每端点熔断阈值：连续失败次数（默认 5）
    CBCooldown     time.Duration // 熔断冷却期（默认 30s）
    MaxRetries     int           // 最大重试次数（默认 3）
    RetryBaseDelay time.Duration // 重试基础延迟（默认 500ms）
    Logger         *slog.Logger  // 结构化日志器（默认 slog.Default()）
    StateObserver  func(node, state string) // 熔断状态变更回调（可选）
}
```

### 4.2 哨兵错误

```go
var ErrEndpointUnavailable = errors.New("no agent endpoint available")
var ErrCircuitOpen         = errors.New("circuit breaker open (cooldown remaining)")
var ErrTransport           = errors.New("agent transport failure")
```

### 4.3 构造函数

```go
// New 创建 Agent 客户端，为每个端点 URL 初始化独立熔断器
func New(cfg Config) *Client
```

### 4.4 客户端方法

```go
// BaseURL 返回主端点 URL（首个 URL 或空字符串）
func (c *Client) BaseURL() string

// BaseURLs 返回所有配置的端点 URL 列表
func (c *Client) BaseURLs() []string

// PickEndpoint 轮询选取健康端点（Round-Robin），若无健康端点则回退到首个 URL
func (c *Client) PickEndpoint() string

// EndpointStates 返回每个端点的熔断状态快照
// 状态值: "closed" / "open" / "half_open"
func (c *Client) EndpointStates() map[string]string

// CircuitStateString 返回聚合熔断状态描述
func (c *Client) CircuitStateString() string

// Health 健康检查（委托至 Get(ctx, "/health")）
func (c *Client) Health(ctx context.Context) (map[string]any, error)

// Get 发送 GET 请求并返回 JSON 解码结果
func (c *Client) Get(ctx context.Context, path string) (map[string]any, error)

// Post 发送 POST 请求（自动从上下文提取 RequestID）
func (c *Client) Post(ctx context.Context, path string, payload any) (map[string]any, error)

// PostWithRequestID 发送携带显式 RequestID 的 POST 请求
func (c *Client) PostWithRequestID(ctx context.Context, path string, payload any, requestID string) (map[string]any, error)
```

### 4.5 幂等键上下文

```go
// ContextWithIdempotencyKey 向上下文注入幂等保护键
// 客户端自动将其作为 X-Idempotency-Key 请求头发送，防止网络超时导致重复处理
func ContextWithIdempotencyKey(ctx context.Context, key string) context.Context

// IdempotencyKeyFromContext 从上下文提取幂等键（不存在时返回空字符串）
func IdempotencyKeyFromContext(ctx context.Context) string
```

### 4.6 调用范例

```go
client := agent.New(agent.Config{
    BaseURLs:    []string{"http://10.0.1.10:8079", "http://10.0.1.11:8079"},
    APIKey:      "secret-token",
    Timeout:     30 * time.Second,
    CBThreshold: 5,
    CBCooldown:  30 * time.Second,
})

// 健康检查
health, err := client.Health(ctx)

// 发送 POST 请求（自动注入 X-Trace-ID 和 X-Idempotency-Key）
ctx = agent.ContextWithIdempotencyKey(ctx, fmt.Sprintf("hub-%s-%d", taskID, retryCount))
result, err := client.Post(ctx, "/v1/privacy/mask", reqPayload)

// 查看端点熔断状态
for node, state := range client.EndpointStates() {
    fmt.Printf("  %s → %s\n", node, state)
}
```

---

## 五、全链路可观测性与指标采集 (`pkg/metrics`)

### 5.1 Prometheus 指标收集器

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/metrics"

// 初始化模块指标收集器
mc := metrics.NewCollector("audit-log")
```

### 5.2 Collector 完整方法列表

```go
// ── HTTP 与 Agent 调用度量 ──

// RecordHTTP 记录 HTTP 请求计数与延迟
func (c *Collector) RecordHTTP(method, path string, status int, durationSec float64)

// RecordAgentCall 记录上游 Agent 调用计数与延迟
func (c *Collector) RecordAgentCall(endpoint string, status string, durationSec float64)

// HTTPMiddleware 返回自动记录 HTTP 指标的 Gin 中间件（自动跳过 /metrics 自身）
func (c *Collector) HTTPMiddleware() gin.HandlerFunc

// ── 任务调度度量 ──

// RecordOrphanedRecovery 记录孤立任务恢复计数
func (c *Collector) RecordOrphanedRecovery(taskType string)

// RecordTaskRetry 记录任务重试计数
func (c *Collector) RecordTaskRetry(result string)

// SetCircuitBreakerState 设置熔断器状态仪表（0=closed, 1=open, 2=half_open）
func (c *Collector) SetCircuitBreakerState(node string, state string)

// RecordLeaseConflict 记录租约冲突计数
func (c *Collector) RecordLeaseConflict()

// RecordLeaseExpired 记录租约过期计数
func (c *Collector) RecordLeaseExpired(count int)

// RecordClaimLatency 记录任务领取延迟
func (c *Collector) RecordClaimLatency(durationSec float64)

// RecordTaskTransition 记录任务状态机流转
func (c *Collector) RecordTaskTransition(from, to, result string)

// ── 就绪探针与命名治理度量 ──

// SetReady 设置服务就绪状态仪表（1=就绪, 0=未就绪）
func (c *Collector) SetReady(ready bool)

// RecordAPIAlias 记录别名映射使用（实现 naming.Observer 接口）
func (c *Collector) RecordAPIAlias(alias, canonical, target string)

// RecordNormalizeError 记录标识归一化失败（实现 naming.Observer 接口）
func (c *Collector) RecordNormalizeError(reason string)

// RecordDatasourceRequest 记录规范数据源请求度量
// 非标数据源标识自动映射为固定标签 "unknown"，消除时间序列基数膨胀
func (c *Collector) RecordDatasourceRequest(datasourceID, apiCode, status string)

// ── Prometheus 端点 ──

// Handler 返回提供 Prometheus /metrics 抓取的 Gin 处理器
func (c *Collector) Handler() gin.HandlerFunc
```

> **注意**：`Collector` 实现了 `naming.Observer` 接口，可通过 `naming.SetObserver(mc)` 注册为全局命名观测器。

### 5.3 挂载范例

```go
// 挂载 HTTP 监控与 Prometheus 抓取端点
router.Use(mc.HTTPMiddleware())
router.GET("/metrics", mc.Handler())

// 注册为全局命名观测器
naming.SetObserver(mc)

// 业务埋点
mc.RecordHTTP("POST", "/api/audit/logs", 200, 0.015)
mc.RecordAgentCall("http://10.0.1.10:8079", "success", 0.012)
mc.RecordDatasourceRequest("ds_yibao", "api1_yibao", "success")
mc.SetReady(true)
```

---

## 六、国密 mTLS 与 CN 白名单 (`pkg/tlsutil`)

### 6.1 服务端 TLS 配置

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"

// ServerTLSConfig 服务端 TLS 配置结构
type ServerTLSConfig struct {
    Enabled          bool   // 是否启用 TLS
    CertFile         string // 服务端证书文件路径
    KeyFile          string // 服务端私钥文件路径
    CAFile           string // CA 根证书文件路径
    ClientAuth       string // 客户端证书校验模式（"require" / "requireandverify" / "request" 等）
    PinnedPubKeyFile string // SPKI 公钥固定文件路径（可选）
}

// BuildServerTLSConfig 构建强制 TLS 1.3 的服务端证书配置
func BuildServerTLSConfig(cfg *ServerTLSConfig) (*tls.Config, error)
```

### 6.2 公钥工具

```go
// LoadPublicKey 从 PEM 文件加载公钥（支持 PKIX 公钥和 X.509 证书格式）
// 支持 RSA、ECDSA、Ed25519 密钥类型
func LoadPublicKey(path string) (crypto.PublicKey, error)

// PublicKeysEqual 深度比较两个公钥是否相等（支持 RSA/ECDSA/Ed25519）
func PublicKeysEqual(a, b crypto.PublicKey) bool
```

### 6.3 动态 CN 白名单 (`tlsutil.DynamicWhitelist`)

```go
// NewDynamicWhitelist 创建动态 CN 白名单管理器
// 首次同步加载 YAML 文件，后台每 5 秒轮询文件 mtime 实现热重载
func NewDynamicWhitelist(path string) (*DynamicWhitelist, error)

// IsAuthorized 检查客户端 CN 是否在白名单中
func (dw *DynamicWhitelist) IsAuthorized(clientCN string) bool

// CheckScope 检查客户端 CN 对指定 gRPC 方法的访问权限
// 返回值：(是否授权, 该 CN 的全部 scope 列表)
// 匹配规则：全局通配 "*"、精确匹配、前缀通配（如 "/AuditLog/*"）
func (dw *DynamicWhitelist) CheckScope(clientCN string, method string) (bool, []string)

// GetScopes 获取客户端 CN 的全部 scope 列表
func (dw *DynamicWhitelist) GetScopes(clientCN string) ([]string, bool)

// UnaryServerInterceptor 返回 gRPC 一元拦截器（自动从 TLS 对端证书提取 CN）
func (dw *DynamicWhitelist) UnaryServerInterceptor() grpc.UnaryServerInterceptor

// StreamServerInterceptor 返回 gRPC 流拦截器（自动从 TLS 对端证书提取 CN）
func (dw *DynamicWhitelist) StreamServerInterceptor() grpc.StreamServerInterceptor

// Close 停止后台轮询协程（幂等安全）
func (dw *DynamicWhitelist) Close()
```

### 6.4 一体化拦截器构造

```go
// NewWhitelistInterceptor 一步构造 gRPC CN 白名单拦截器对
// path 为空时返回 (nil, nil, nil, nil)
func NewWhitelistInterceptor(path string) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor, *DynamicWhitelist, error)
```

### 6.5 调用范例

```go
// 构造服务端 TLS 配置
tlsCfg, err := tlsutil.BuildServerTLSConfig(&tlsutil.ServerTLSConfig{
    Enabled:    true,
    CertFile:   "/etc/certs/server.crt",
    KeyFile:    "/etc/certs/server.key",
    CAFile:     "/etc/certs/ca.crt",
    ClientAuth: "require",
})

// 一步构造 gRPC CN 白名单拦截器
unaryInt, streamInt, whitelist, err := tlsutil.NewWhitelistInterceptor("/etc/privshield/whitelist.yaml")
if err != nil {
    log.Fatal(err)
}
defer whitelist.Close()

grpcServer := grpc.NewServer(
    grpc.Creds(credentials.NewTLS(tlsCfg)),
    grpc.UnaryInterceptor(unaryInt),
    grpc.StreamInterceptor(streamInt),
)
```

---

## 七、环境配置与安全基线校验 (`pkg/config`)

### 7.1 类型安全环境变量读取

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/config"

// EnvString 读取字符串环境变量（未设置或为空时返回默认值）
func EnvString(name, def string) string

// EnvInt 读取整数环境变量（缺失或解析失败时返回默认值）
func EnvInt(name string, def int) int

// EnvFloat 读取浮点数环境变量（缺失或解析失败时返回默认值）
func EnvFloat(name string, def float64) float64

// EnvBool 读取布尔环境变量
// 识别为 true 的值（不区分大小写）: "true", "1", "yes", "on"
// 其他一切（含空/未设置）返回默认值 def
func EnvBool(name string, def bool) bool

// EnvStringSlice 读取逗号分隔的字符串切片（自动去空白、去空项）
func EnvStringSlice(name string) []string

// EnvStringFirstSet 返回多个环境变量中首个非空值（全部为空时返回 ""）
func EnvStringFirstSet(names ...string) string

// EnvStringOptional 仅在变量完全未设置时使用默认值
// 显式设置为空字符串被视为有效值（区别于 EnvString）
func EnvStringOptional(name, def string) string
```

### 7.2 Fail-Closed 安全基线校验

```go
// SecurityRequirements 安全基线需求描述
type SecurityRequirements struct {
    ServiceName          string   // 服务名称（用于错误消息，如 "audit-log"）
    Hosts                []string // 所有监听地址（HTTP + gRPC）
    APIKey               string   // 入站认证密钥
    TLSEnabled           bool     // 是否启用服务端 TLS
    RequireTLS           bool     // 生产编排强制要求 TLS
    GRPCEnabled          bool     // 是否监听 gRPC 端口
    MTLSWhitelistFile    string   // 客户端证书 CN 白名单文件路径
    EncryptionKey        string   // 信封加密主密钥
    RequireEncryptionKey bool     // 强制要求加密密钥
    HashKey              string   // HMAC-SM3 哈希链密钥
    RequireHashKey       bool     // 强制要求哈希链密钥
}

// ValidateFailClosed 执行 Fail-Closed 启动安全不变量校验
// 校验规则：
// 1. 非回环地址 + 空 APIKey → ErrAPIKeyRequired
// 2. RequireTLS=true 但 TLSEnabled=false → ErrTLSRequired
// 3. TLS+gRPC 启用但无 CN 白名单 → ErrMTLSWhitelistRequired
// 4. RequireEncryptionKey=true + 非回环 + 空密钥 → ErrEncryptionKeyRequired
// 5. RequireHashKey=true + 非回环 + 空密钥 → ErrChainKeyRequired
func ValidateFailClosed(req SecurityRequirements) error

// IsLoopbackHost 判断主机是否仅接受本地连接
// 回环判定: 空串、"localhost"、127.0.0.0/8、::1
// "0.0.0.0"、"::"、"*"、具体 IP 均返回 false（Fail-Closed）
func IsLoopbackHost(host string) bool
```

### 7.3 哨兵错误

```go
var ErrAPIKeyRequired        = errors.New("inbound API key must not be empty when listening on a non-loopback address")
var ErrTLSRequired           = errors.New("TLS is required by configuration but not enabled")
var ErrMTLSWhitelistRequired = errors.New("mTLS CN whitelist file is required when TLS is enabled on the gRPC server")
var ErrEncryptionKeyRequired = errors.New("snapshot encryption key must not be empty when listening on a non-loopback address")
var ErrChainKeyRequired      = errors.New("evidence hash chain key must not be empty when listening on a non-loopback address")
```

### 7.4 结构化日志

> 日志初始化由 `pkg/observability` 包提供，不在 `pkg/config` 中：

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/observability"

// 初始化全局结构化日志器
logger := observability.InitLogger("info")

// 创建独立格式化日志器
logger := observability.NewLogger("json", "debug")
```

### 7.5 调用范例

```go
port := config.EnvInt("PORT", 8084)
dbPath := config.EnvString("DB_PATH", "./data/audit.db")
origins := config.EnvStringSlice("CORS_ORIGINS")
debug := config.EnvBool("DEBUG", false)

// Fail-Closed 安全校验
err := config.ValidateFailClosed(config.SecurityRequirements{
    ServiceName:       "audit-log",
    Hosts:             []string{config.EnvString("HTTP_HOST", "0.0.0.0")},
    APIKey:            config.EnvString("API_KEY", ""),
    TLSEnabled:        config.EnvBool("TLS_ENABLED", false),
    RequireTLS:        config.EnvBool("REQUIRE_TLS", false),
    GRPCEnabled:       true,
    MTLSWhitelistFile: config.EnvString("MTLS_WHITELIST_FILE", ""),
    EncryptionKey:     config.EnvString("ENCRYPTION_KEY", ""),
    RequireEncryptionKey: true,
    HashKey:           config.EnvString("HASH_KEY", ""),
    RequireHashKey:    true,
})
if err != nil {
    log.Fatalf("security baseline check failed: %v", err)
}
```

---

## 八、命名治理与输入校验 (`pkg/naming` & `pkg/validation`)

### 8.1 命名注册表与数据源治理 (`pkg/naming`)

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/naming"
```

#### Observer 观测器接口

```go
// Observer 命名治理观测器接口（Collector 实现此接口以采集 Prometheus 指标）
type Observer interface {
    RecordAPIAlias(alias, canonical, target string)
    RecordNormalizeError(reason string)
}

// SetObserver 注册全局命名观测器
func SetObserver(o Observer)

// CurrentObserver 获取当前全局命名观测器
func CurrentObserver() Observer
```

#### API 编号与数据源标识常量

```go
// API 编号常量
const API1Yibao    = "api1_yibao"
const API2Kangyang = "api2_kangyang"

// 数据源标识常量
const DSYibao    = "ds_yibao"
const DSKangyang = "ds_kangyang"
const DSMock3    = "ds_mock3"
const DSMock4    = "ds_mock4"

// 注册表状态常量
const StatusActive   = "active"
const StatusReserved = "reserved"
```

#### 哨兵错误

```go
var ErrUnknownDataSource  = errors.New("unknown datasource id")
var ErrReservedDataSource = errors.New("reserved datasource")
```

#### Entry 注册表条目

```go
// Entry 数据源注册表条目
type Entry struct {
    APICode      string            // API 编号（如 "api1_yibao"）
    DataSourceID string            // 数据源标识（如 "ds_yibao"）
    Seq          int               // 序号
    DisplayName  map[string]string // 多语言显示名称
    Category     string            // 分类
    FileName     string            // 关联文件名
    FieldCount   int               // 字段数量
    Aliases      []string          // 别名列表（中文名、旧版 slug 等）
    Status       string            // 状态（"active" / "reserved"）
}

// Registry 全局数据源注册表（包含 Yibao、Kangyang、Mock3、Mock4 四个条目）
var Registry = []Entry{ ... }
```

#### 安全等级常量与函数 (`pkg/naming/levels.go`)

```go
// 安全等级 ID 常量
const SecurityLevelL1 = "L1"
const SecurityLevelL2 = "L2"
const SecurityLevelL3 = "L3"
const SecurityLevelL4 = "L4"
const SecurityLevelL5 = "L5"

// NormalizeSecurityLevelID 规范化安全等级 ID（无效输入回退到 "L1"）
func NormalizeSecurityLevelID(level string) string

// SecurityLevelRank 返回安全等级数值排名（L1=1 ... L5=5）
func SecurityLevelRank(level string) int

// MaxSecurityLevelID 返回多个安全等级中的最高等级
func MaxSecurityLevelID(levels ...string) string

// SecurityLevelLabel 返回安全等级完整标签（如 "L3 - 敏感"）
func SecurityLevelLabel(level string) string

// SecurityLevelName 返回安全等级中文名称（如 "敏感"）
func SecurityLevelName(level string) string

// SecurityLevelIDs 返回所有安全等级 ID 列表
func SecurityLevelIDs() []string

// SecurityLevelNames 返回所有安全等级名称列表
func SecurityLevelNames() []string

// SecurityLevelLabels 返回所有安全等级标签映射
func SecurityLevelLabels() map[string]string
```

#### 核心治理函数

```go
// Normalize 将原始输入规范化为注册表条目（支持别名、中文名、旧版 slug 匹配）
func Normalize(raw string) (*Entry, error)

// NormalizeDataSourceID 规范化数据源标识
func NormalizeDataSourceID(raw string) (string, error)

// ResolveInbound 解析入站数据源标识（自动匹配别名映射为 Canonical 标识）
func ResolveInbound(raw string) (string, error)

// CheckWritable 检查数据源标识是否可写（非保留、非未知）
func CheckWritable(datasourceID string) error

// EntryByDataSourceID 按数据源标识查找注册表条目
func EntryByDataSourceID(id string) (Entry, bool)

// EntryByAPICode 按 API 编号查找注册表条目
func EntryByAPICode(code string) (Entry, bool)

// ActiveEntries 返回所有活跃状态条目
func ActiveEntries() []Entry

// Entries 返回所有注册表条目副本
func Entries() []Entry

// AllDataSourceIDs 返回所有已注册数据源标识列表
func AllDataSourceIDs() []string

// ActiveDataSourceIDs 返回所有活跃状态数据源标识列表
func ActiveDataSourceIDs() []string

// APICodeForDataSource 反查数据源对应的 API 编号
func APICodeForDataSource(datasourceID string) string

// DataSourceForAPICode 反查 API 编号对应的数据源标识
func DataSourceForAPICode(apiCode string) (string, bool)

// APICodes 返回所有已注册 API 编号列表
func APICodes() []string

// ValidDataSourceIDFormat 校验数据源标识格式合法性
func ValidDataSourceIDFormat(s string) bool

// ValidAPICodeFormat 校验 API 编号格式合法性
func ValidAPICodeFormat(s string) bool

// IsUnknownDataSource 判断错误是否为未知数据源
func IsUnknownDataSource(err error) bool

// IsReserved 判断错误是否为保留数据源
func IsReserved(err error) bool

// AliasConflicts 检测注册表中存在的别名冲突
func AliasConflicts() []string
```

### 8.2 输入校验 (`pkg/validation`)

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/validation"
```

#### 预定义枚举白名单

```go
// DataSourceTypes 数据源类型白名单
var DataSourceTypes = []string{"database", "api", "file"}

// SensitivityLevels 敏感度等级白名单（来自 naming 包安全等级 ID）
var SensitivityLevels = naming.SecurityLevelIDs() // ["L1", "L2", "L3", "L4", "L5"]

// HubOperations 调度中枢操作类型白名单
var HubOperations = []string{"mask", "k_anon", "dp", "classify", "none"}

// AuditOperations 审计操作类型白名单
var AuditOperations = []string{"mask", "classify", "k_anon", "dp", "qol"}

// AuditStatuses 审计状态白名单
var AuditStatuses = []string{"success", "failed"}

// TaskStatuses 任务状态白名单
var TaskStatuses = []string{"pending", "running", "completed", "failed"}
```

#### 校验函数

```go
// AllowedValues 校验值是否在允许枚举列表中
func AllowedValues(field, value string, allowed []string) error

// PortRange 校验端口号是否在合法范围（1-65535）
func PortRange(port int) error

// NonEmpty 校验字段值非空
func NonEmpty(field, value string) error

// MaxLength 校验字段值长度不超过上限
func MaxLength(field, value string, max int) error

// GenerateID 生成结合纳秒时间戳与密码学安全随机数的全局唯一 ID
// 输出格式: "{prefix}-{纳秒时间戳}-{随机hex}"
func GenerateID(prefix string) string

// ParsePagination 从 Gin 上下文解析分页参数（强制安全边界）
// defaultLimit: 未指定时的默认值; maxLimit: 允许的最大值（防拖库）
func ParsePagination(c *gin.Context, defaultLimit, maxLimit int) (limit, offset int)
```

### 8.3 调用范例

```go
// 1. 数据源标识归一化与保留字拦截
normID, err := naming.ResolveInbound(req.DatasourceID)
if err != nil {
    if naming.IsReserved(err) {
        middleware.AbortWithError(c, http.StatusConflict, "RESERVED_DATASOURCE", err.Error(), nil)
        return
    }
    middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_DATASOURCE_ID", err.Error(), nil)
    return
}
normAPICode := naming.APICodeForDataSource(normID)

// 2. 枚举白名单校验
err = validation.AllowedValues("operation", req.Operation, validation.HubOperations)

// 3. 生成防碰撞唯一 ID
taskID := validation.GenerateID("task")

// 4. 安全分页参数解析
limit, offset := validation.ParsePagination(c, 100, 1000)

// 5. 安全等级查询
rank := naming.SecurityLevelRank("L3")            // → 3
label := naming.SecurityLevelLabel("L3")          // → "L3 - 敏感"
maxLevel := naming.MaxSecurityLevelID("L2", "L4") // → "L4"
```

---

## 九、调度中枢 `service-hub` 全阶段公共 PKG 调用规范

数据服务调度中枢（`services/service-hub`）作为数据流通与安全治理的**核心调度大脑**，在请求生命周期各阶段均深度集成了 `pkg/` 共享基础库。下表展示了全生命周期各阶段与公共包的调用关系矩阵：

### 9.1 全生命周期拓扑与公共 PKG 调用全景图

```text
┌──────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                   service-hub 服务生命周期与 8 阶段流水线                                         │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
                                                       │
  【阶段一：服务引导与配置装配】───────────────────────┼──▶ pkg/config (环境变量解析、Fail-Closed 安全基线校验)
                                                       ├──▶ pkg/observability (结构化 slog 日志初始化)
                                                       ├──▶ pkg/tlsutil (mTLS 双向证书、SPKI 公钥固定、CN 白名单)
                                                       ├──▶ pkg/store (SQLite 完整性探针、PostgreSQL 租约连接池初始化)
                                                       ├──▶ pkg/metrics & pkg/naming (Prometheus 收集器与命名观测器)
                                                       └──▶ pkg/crypto (审计链密钥 SetAuditChainKey)
                                                       │
  【阶段二：崩溃恢复与生命周期治理】───────────────────┼──▶ pkg/store (扫描 running 孤立任务置 fail，pending 保留排队)
                                                       └──▶ pkg/metrics (记录孤立任务恢复与指数退避重试指标)
                                                       │
  【阶段三：网络接入与 8 层纵深防御】──────────────────┼──▶ pkg/middleware (TraceID、Recovery、SecurityHeaders、
                                                       │                    MaxBodySize、MaxConcurrent、RateLimit、
                                                       │                    CORS、Auth/AuthWithRoles)
                                                       │
  【阶段四：入参安全清洗与命名归一】───────────────────┼──▶ pkg/validation (纳秒时序防碰撞 ID 生成、枚举白名单、分页防拖库)
                                                       └──▶ pkg/naming (多源别名映射归一、保留字阻断、标准 API 反查)
                                                       │
  【阶段五：流水线任务调度与租约竞抢】─────────────────┼──▶ pkg/store (PostgreSQL FOR UPDATE SKIP LOCKED 原子租约抢占、
                                                       │               后台心跳续约、超时孤立租约回收、状态机流转)
                                                       │
  【阶段六：原数采样与下游交互】───────────────────────┼──▶ pkg/agent (幂等键注入、每端点熔断器、超时控制)
                                                       ├──▶ pkg/naming (向 datasource-mgr 发起标准标识数据采样)
                                                       └──▶ pkg/store/DataSourceStore (数据源资产查询与访问审计)
                                                       │
  【阶段七：敏感定级与隐私脱敏计算】───────────────────┼──▶ pkg/agent (3 态每端点熔断器、幂等重试键、一体化医疗流水线)
                                                       │
  【阶段八：审计存证上报与下游交互】───────────────────┼──▶ pkg/agent (向 audit-log 发送存证证据，internal/audit/client)
                                                       ├──▶ pkg/metrics (有界标签终态上报、就绪探针状态翻转、Prometheus 导出)
                                                       └──▶ pkg/crypto (SM4-GCM 信封加密存证快照)
                                                       │
  【阶段九：优雅停机与资源排空】───────────────────────┴──▶ Context 广播排空任务信号量、gRPC 30s 超时收敛、连接池安全注销
```

---

### 9.2 阶段一：服务引导与安全基线初始化 (Bootstrap & Security Baseline)

在 `service-hub` 启动入口（`cmd/server/main.go`），公共包负责建立零信任安全基线与环境初始化：

1. **结构化日志初始化 (`pkg/observability`)**：
   - 调用 `observability.InitLogger(cfg.LogLevel)` 构建全局 `slog.Logger`（支持 JSON/Text 格式）。
2. **环境配置与 Fail-Closed 安全校验 (`pkg/config`)**：
   - 使用类型安全的 `config.EnvString`、`config.EnvInt`、`config.EnvBool` 读取配置；
   - 调用 `config.ValidateFailClosed(req)` 执行零信任安全基线校验（非回环暴露必须配置 APIKey、TLS、加密密钥、哈希链密钥）。
3. **审计链密钥装载 (`pkg/crypto` & `pkg/store`)**：
   - 调用 `store.SetAuditChainKey(cfg.HashKey)` 设置 HMAC-SM3 审计链密钥，后续存证哈希自动使用该密钥。
4. **底层存储完整性探针与初始化 (`pkg/store` / `pkg/store/sqlite` / `pkg/store/postgres`)**：
   - **物理探针**：若采用 SQLite，在连接前调用 `sqlite.ValidateIntegrity(cfg.DBPath)` 执行 `PRAGMA integrity_check`，发现文件损坏立即 Fail-Fast；
   - **租约存储装配**：若配置了 `PG_DSN`，调用 `postgres.New` 初始化支持多副本 Hub 的 `LeasedTaskStore`；否则回退为 `sqlite.NewTaskStore` 或 `memory.NewTaskStore`。
5. **mTLS 双向证书与 CN 白名单拦截器 (`pkg/tlsutil`)**：
   - 调用 `tlsutil.BuildServerTLSConfig(&tlsutil.ServerTLSConfig{...})` 构建强制 TLS 1.3 的服务端证书配置，支持客户端证书强校验与公钥证书固定（SPKI Pinning）；
   - gRPC 端点挂载 `tlsutil.NewWhitelistInterceptor(cfg.MTLSWhitelistFile)`，实现基于客户端 CN 白名单的 method-scope 动态热重载鉴权。
6. **监控指标与全局命名观测器 (`pkg/metrics` & `pkg/naming`)**：
   - 初始化 `mc := metrics.NewCollector("service-hub")`，注册 QPS、延迟与状态指标；
   - 调用 `naming.SetObserver(mc)` 注册全局观测器，当流量携带非标别名或脏 ID 时自动向 Prometheus 上报别名使用度量。

```go
// main.go 启动引导核心代码片段
logger := observability.InitLogger(cfg.LogLevel)
if cfg.PGDSN == "" && cfg.DBPath != "" {
    if err := sqlite.ValidateIntegrity(cfg.DBPath); err != nil {
        log.Fatalf("sqlite integrity check failed: %v", err)
    }
}
taskStore, _ := initLeasedTaskStore(cfg, logger)
mc := metrics.NewCollector("service-hub")
naming.SetObserver(mc)
store.SetAuditChainKey(cfg.HashKey)

// Fail-Closed 安全基线校验
err := config.ValidateFailClosed(config.SecurityRequirements{
    ServiceName:       "service-hub",
    Hosts:             []string{config.EnvString("HTTP_HOST", "0.0.0.0")},
    APIKey:            cfg.APIKey,
    TLSEnabled:        cfg.TLSEnabled,
    RequireTLS:        cfg.RequireTLS,
    GRPCEnabled:       true,
    MTLSWhitelistFile: cfg.MTLSWhitelistFile,
    EncryptionKey:     cfg.EncryptionKey,
    RequireEncryptionKey: true,
    HashKey:           cfg.HashKey,
    RequireHashKey:    true,
})
```

---

### 9.3 阶段二：崩溃恢复与历史生命周期治理 (Crash Recovery & Retention)

在服务重启时，调度中枢自动修复意外崩溃留下的孤立脏数据：

1. **孤立任务恢复 (`pkg/store`)**：
   - 调用 `taskStore.List(store.TaskFilter{Status: "running"})` 扫描因服务异常重启或断电卡在 `running` 的任务，安全标记为 `failed` 并写入错误原因；
   - 扫描 `pending` 任务并直接保留在队列中等待重新消费。
2. **指数退避重试与生命周期清理 (`pkg/store` & `pkg/metrics`)**：
   - 自动重试可恢复故障任务，按 `5s * 2^retryCount` 计算 `RetryAfter` 指数退避时间；
   - 启动后台协程定期调用 `taskStore.CleanupOld(cutoff)` 清理超过 `RetentionDays` 保留期的终态历史数据，防止数据库文件无界膨胀；
   - 恢复与重试全过程调用 `mc.RecordOrphanedRecovery` 与 `mc.RecordTaskRetry` 上报指标。

---

### 9.4 阶段三：请求接入与 8 层安全防御中间件 (Ingress & 8-Layer Defense Middlewares)

HTTP REST 控制器（`internal/handlers/handlers.go`）通过挂载 `pkg/middleware` 实现 API 纵深防御：

| 顺序 | 中间件组件 | 功能说明 | 违规拦截响应 |
|:---|:---|:---|:---|
| 1 | `middleware.TraceMiddleware()` | 提取请求头 `X-Request-ID`，不存在时自动生成纳秒级 `trace_id` 注入上下文 | 注入响应头 `X-Trace-ID` |
| 2 | `middleware.Recovery(logger, module)` | 拦截 Handler 运行时 Panic，防止服务进程崩溃 | `500 INTERNAL_ERROR` |
| 3 | `middleware.SecurityHeaders()` | 注入 CSP、HSTS、X-Content-Type-Options、X-Frame-Options 等安全响应头 | — |
| 4 | `middleware.MaxBodySize(32MB)` | 限制 HTTP 请求体最大 32 MiB，防御超大报文耗尽内存 | `413 PAYLOAD_TOO_LARGE` |
| 5 | `middleware.MaxConcurrent(1000)` | 控制在途活跃并发请求数不超过 1000，超载保护 | `503 SERVICE_UNAVAILABLE` |
| 6 | `middleware.RateLimit(rps, burst)` | 基于客户端 IP 的 32 分片令牌桶算法精确限流 | `429 RATE_LIMITED` |
| 7 | `middleware.CORS(origins)` | 跨域来源安全白名单校验与 OPTIONS 预检放行 | 阻断跨域访问 |
| 8 | `middleware.Auth(apiKey)` | 常量时间校验 `Authorization: Bearer <Key>`（/metrics 同样需要认证） | `401 UNAUTHORIZED` |

> **注意**：日志记录由 `pkg/observability.RequestLogger` 统一提供，不再作为独立中间件层。

所有响应与错误统一调用 `middleware.RespondWithSuccess` 或 `middleware.AbortWithError`，输出规范的 5 字段跨语言 API 信封（Code、Message、Detail/Data、TraceID、Timestamp）。

---

### 9.5 阶段四：入参安全清洗与命名治理 (Validation & Naming Governance)

任务进入调度前，必须经过严格的参数清洗与命名规范化：

1. **纳秒级防碰撞 ID 生成 (`pkg/validation`)**：
   - 调用 `validation.GenerateID("task")` 生成结合时间戳与密码学安全随机数的全局唯一任务编号（如 `task-1725091200000-a1b2c3d4`）。
2. **枚举白名单校验与分页安全边界 (`pkg/validation`)**：
   - 调用 `validation.AllowedValues("operation", req.Operation, validation.HubOperations)` 校验脱敏算子合法性；
   - 调用 `validation.ParsePagination(c, 100, 1000)` 解析分页参数，强制限制最大拉取 1000 条，杜绝恶意拖库。
3. **数据源标识归一化与保留字拦截 (`pkg/naming`)**：
   - 接收业务传入的数据源标识（支持中文字符、旧版别名如 `ds_yibao`、历史 API 编号），调用 `naming.ResolveInbound(rawSource)` 自动解析映射为 Canonical 标准标识 `ds_yibao`；
   - 调用 `naming.IsReserved(err)` 检测是否命中系统保留关键字，命中时立即阻断并返回 `409 CONFLICT`；
   - 调用 `naming.APICodeForDataSource(normID)` 反查匹配标准 API 编号（如 `api1_yibao`）。

```go
// 参数清洗与命名归一化调用示例
normID, err := naming.ResolveInbound(req.DatasourceID)
if err != nil {
    if naming.IsReserved(err) {
        middleware.AbortWithError(c, http.StatusConflict, "RESERVED_DATASOURCE", err.Error(), nil)
        return
    }
    middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_DATASOURCE_ID", err.Error(), nil)
    return
}
normAPICode := naming.APICodeForDataSource(normID)
taskID := validation.GenerateID("task")
```

---

### 9.6 阶段五：流水线任务调度与原子租约竞抢 (Task Scheduling & Leased Execution)

支持单机 SQLite 模式与多副本 PostgreSQL 模式的透明切换：

1. **多副本分布式原子租约 (`pkg/store/postgres` - `LeasedTaskStore`)**：
   - **无阻塞原子抢占**：租约 Worker 轮询调用 `tasks.ClaimNext(owner, leaseTTL)`，底层通过 `FOR UPDATE SKIP LOCKED` 抢占 `pending` 任务并生成 16 字节安全租约令牌；
   - **并发心跳续约**：任务执行期间以 `leaseTTL / 2` 周期异步调用 `tasks.RenewLease`，CAS 校验 owner 与 token，防止长耗时任务被误判超时；
   - **原子流转与状态提交**：执行完毕调用 `tasks.CompleteLease` 或 `tasks.FailLease`，支持按错误类型（网络超时、崩溃）决定是否触发指数退避重试；
   - **孤立租约回收**：定期调用 `tasks.RequeueExpiredLeases(100)` 回收崩溃节点超时的未完成租约。
2. **单机并发控制 (`pkg/store`)**：
   - 内存与 SQLite 模式下通过带容量缓冲通道（`taskSem := make(chan struct{}, 10)`）限制最大 10 个流水线协程并发执行，调用 `taskStore.Update` 驱动状态机流转。

---

### 9.7 阶段六 & 七：原数抽取与一体化隐私计算 (Data Fetching & Privacy Pipeline)

流水线流转到 `fetch` 与 `classify` 阶段时与上下游引擎交互：

1. **链路追踪与幂等保护 (`pkg/agent`)**：
   - 向 `datasource-mgr` 请求抽取样本时，调用 `agent.ContextWithIdempotencyKey(ctx, key)` 注入幂等保护键；
   - 客户端内置**每端点独立 3 态熔断器**（连续 5 次错误触发熔断，30s 冷却后 HalfOpen 探测），保护 Core Engine 免于雪崩；
   - `Post` / `PostWithRequestID` 返回 `map[string]any`，自动从上下文提取 RequestID 注入请求头。
2. **一体化医疗隐私流水线与熔断保护 (`pkg/agent`)**：
   - 替代传统的"分类定级 + 脱敏处理"两次网络往返，一次调用完成全流程；
   - 注入幂等保护键 `agent.ContextWithIdempotencyKey(ctx, fmt.Sprintf("hub-%s-%s-%d", task.ID, stage, task.RetryCount))`，防止网络超时导致的重复处理。

---

### 9.8 阶段八：审计存证上报 (Audit Evidence Submission)

1. **存证证据客户端 (`internal/audit/client` → P0-6)**：
   - service-hub 通过 `internal/audit/client` 将脱敏存证证据发送至 audit-log 服务；
   - 使用 `pkg/crypto.EncryptString` 对存证快照输入/输出样本执行 SM4-GCM 信封加密（V1/V2 双版本自动适配）。
2. **标准规范加载器 (P1-3)**：
   - engine-go 启动时加载 `rules/standards/*.yaml` 标准规范文件，为分类定级提供领域规则支撑。
3. **有界指标度量上报 (`pkg/metrics`)**：
   - 任务终态时调用 `mc.RecordDatasourceRequest(datasourceID, apiCode, status)`；
   - 未在系统登记的脏数据源标识自动映射为固定标签 `"unknown"`，彻底消除由于任意输入导致的时间序列基数膨胀（Cardinality Explosion）；
   - 暴露 `/metrics` 标准 Prometheus 端点（需认证访问）。

---

### 9.9 阶段九：优雅停机与资源排空 (Graceful Shutdown)

1. **优雅停机与资源排空 (`pkg/tlsutil` & `pkg/store`)**：
   - 拦截 `SIGINT` / `SIGTERM` 信号，调用 `signal.NotifyContext` 取消全局 Context；
   - 触发 `Server.Shutdown()` 广播通知在途任务协程，等待并发信号量（`taskSem`）全部释放；
   - 启动 30 秒超时控制，先调用 `grpcServer.GracefulStop()` 再调用 `httpSrv.Shutdown()`，有序关闭连接池与监听套接字；
   - flusher `BufferedAuditStore.Close()` 排空内存队列中的未落盘审计记录，确保零数据丢失。
