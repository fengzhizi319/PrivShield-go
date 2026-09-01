# PrivShield 共享基础包 (Shared PKG) — 编程接口与 API 规约手册

> **文档定位**：`pkg` 模块中所有公共包、结构体、接口契约、函数签名、错误码定义及集成调用范例的权威技术手册。
> **面向对象**：`PrivShield` 全栈微服务开发者、SDK 开发者与中台运维研发人员。

---

## 目录

- [一、密码与信封加密体系 (`pkg/crypto`)](#一密码与信封加密体系-pkgcrypto)
- [二、持久化与微批存储引擎 (`pkg/store` & `pkg/store/flusher`)](#二持久化与微批存储引擎-pkgstore--pkgstoreflusher)
- [三、纵深防御中间件与信封协议 (`pkg/middleware`)](#三纵深防御中间件与信封协议-pkgmiddleware)
- [四、上游 Agent 客户端与熔断器 (`pkg/agent`)](#四上游-agent-客户端与熔断器-pkgagent)
- [五、全链路可观测性与指标采集 (`pkg/metrics`)](#五全链路可观测性与指标采集-pkgmetrics)
- [六、国密 mTLS 与 CN 白名单 (`pkg/tlsutil`)](#六国密-mtls-与-cn-白名单-pkgtlsutil)
- [七、环境配置与安全门禁 (`pkg/config`)](#七环境配置与安全门禁-pkgconfig)
- [八、命名治理与安全等级 (`pkg/naming`)](#八命名治理与安全等级-pkgnaming)
- [九、输入校验 (`pkg/validation`)](#九输入校验-pkgvalidation)
- [十、调度中枢 `service-hub` 全阶段公共 PKG 调用规范](#十调度中枢-service-hub-全阶段公共-pkg-调用规范)

---

## 一、密码与信封加密体系 (`pkg/crypto`)

`pkg/crypto` 提供符合国密标准（GM/T 0004-2012 / GM/T 0002-2012）的密码学原语与动态信封加密实现。

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/crypto"
```

### 1.1 常量

| 常量 | 值 | 说明 |
|---|---|---|
| `EncryptedPrefix` | `"enc:v1:"` | V1 信封加密前缀（SM4-GCM，密钥由 `DeriveKey` 直出） |
| `EncryptedPrefixV2` | `"enc:v2:"` | V2 信封加密前缀（HKDF-SM3 密钥派生 + 16 字节随机盐） |
| `NonceSize` | `12` | GCM 模式随机数长度（字节） |
| `SM3BlockSize` | `64` | SM3 哈希分组长度（字节） |
| `SM3Size` | `32` | SM3 摘要输出长度（字节） |
| `BlockSize` | `16` | SM4 分组长度（字节） |
| `KeySize` | `16` | SM4 密钥长度（字节） |
| `Rounds` | `32` | SM4 加密轮数 |

### 1.2 错误

| 错误变量 | 值 | 触发场景 |
|---|---|---|
| `ErrEmptyKey` | `"crypto: encryption key is not configured"` | 未配置加密密钥时调用加密/解密函数 |
| `ErrUnencryptedValue` | `"crypto: value is not envelope-encrypted (missing enc:v1:/enc:v2: prefix)"` | 解密时输入值不携带合法信封前缀 |

### 1.3 密钥派生函数

```go
// DeriveKey 使用 SM3 对 secret 进行单向摘要派生，输出 KeySize (16) 字节子密钥。
// 确定性派生：同一 secret 始终产出相同子密钥。
func DeriveKey(secret string) []byte

// DeriveKeyHKDF 使用 HKDF-SM3 两步提取-扩展模型，结合 16 字节随机盐派生密钥。
// 同一 secret + 不同 salt 产出不同子密钥，适用于 V2 信封格式。
func DeriveKeyHKDF(secret string, salt []byte) []byte
```

### 1.4 信封加密与解密

```go
// EncryptString 使用 SM4-GCM 动态信封加密敏感字符串。
// 返回 "enc:v2:<base64>" 格式，内部使用 HKDF-SM3 密钥派生 + 16 字节随机盐。
// 每次加密生成不同密文（盐不同），但解密结果一致。
func EncryptString(plaintext, secret string) (string, error)

// DecryptString 解密 SM4-GCM 动态信封密文。
// 自动检测 v1/v2 前缀并选择对应解密策略；若输入为明文（无合法前缀）则原样返回。
func DecryptString(ciphertext, secret string) (string, error)

// IsEncrypted 判断字符串是否为信封加密格式（检查 v1 与 v2 两种前缀）。
func IsEncrypted(value string) bool
```

**调用范例**：

```go
// V2 信封加密
masterKey := "my-secret-key-12345"
encrypted, err := crypto.EncryptString("张三 (110101199003072345)", masterKey)
if err != nil {
    log.Fatal(err)
}
// 输出格式: enc:v2:ABCD...==
fmt.Println(crypto.IsEncrypted(encrypted)) // true

// 透明解密（自动识别 v1/v2）
plain, err := crypto.DecryptString(encrypted, masterKey)

// 明文直通：解密未加密值不会报错
plain, err = crypto.DecryptString("普通明文", masterKey) // plain == "普通明文"
```

### 1.5 SM3 国密哈希原语

```go
// NewSM3 返回实现 hash.Hash 接口的 SM3 哈希实例，可增量写入。
func NewSM3() hash.Hash

// SumSM3 计算输入数据的 32 字节国密 SM3 原始摘要。
func SumSM3(data []byte) [SM3Size]byte

// SumSM3Hex 计算输入数据的 64 字符十六进制国密 SM3 摘要。
func SumSM3Hex(data []byte) string

// HMACSM3 使用 SM3 作为底层哈希的 HMAC 认证消息码，返回 32 字节原始值。
func HMACSM3(key, data []byte) [SM3Size]byte

// HMACSM3Hex 使用 SM3 作为底层哈希的 HMAC，返回 64 字符十六进制字符串。
func HMACSM3Hex(key, data []byte) string
```

### 1.6 SM4 分组密码

```go
// NewCipher 使用给定密钥创建 SM4 分组密码实例（实现 cipher.Block 接口）。
// 密钥长度必须为 KeySize (16) 字节，否则返回错误。
func NewCipher(key []byte) (cipher.Block, error)
```

---

## 二、持久化与微批存储引擎 (`pkg/store` & `pkg/store/flusher`)

### 2.1 常量 (`pkg/store`)

#### 链式验真原因码

| 常量 | 值 | 说明 |
|---|---|---|
| `ChainReasonOK` | `"ok"` | 链式验证全部通过 |
| `ChainReasonLegacyHashed` | `"legacy_hashed"` | 记录使用旧版哈希算法 |
| `ChainReasonTamperedPayload` | `"tampered_payload"` | 载荷字段被篡改 |
| `ChainReasonHashMismatch` | `"hash_mismatch"` | 计算哈希与存储哈希不匹配 |
| `ChainReasonBrokenChain` | `"broken_chain"` | 前序哈希链接断裂 |
| `ChainReasonMissingPrev` | `"missing_prev"` | 缺失前序记录 |
| `ChainReasonMissingRecords` | `"missing_records"` | 无可用记录 |

#### 归档与分页

| 常量 | 值 | 说明 |
|---|---|---|
| `DefaultArchivePageSize` | `500` | 归档分页默认页大小 |
| `ArchiveIDChunkSize` | `500` | 归档 ID 分块删除的批次大小 |

#### 哈希算法标识

| 常量 | 值 | 说明 |
|---|---|---|
| `AuditHashSM3` | `"SM3"` | 无密钥的纯 SM3 哈希链 |
| `AuditHashSM3HMAC` | `"SM3-HMAC:v1"` | 带密钥的 HMAC-SM3 哈希链 |
| `AuditHashSHA256` | `"SHA256"` | 旧版 SHA-256 哈希（遗留兼容） |
| `LegacyHashSuffix` | `"-LEGACY"` | 旧版哈希标记后缀 |

#### 推荐策略阈值

| 常量 | 值 | 说明 |
|---|---|---|
| `RecommendHighSensitiveCount` | `100` | 高敏感记录数阈值 |
| `RecommendTopSecretCount` | `50` | 绝密级记录数阈值 |
| `RecommendMinSuccessRate` | `95.0` | 最低成功率百分比阈值 |

### 2.2 错误

```go
var ErrLeaseNotSupported = errors.New("this store backend does not support multi-replica task leases; use PostgreSQL for Phase B deployment")
```

当底层存储不支持多副本原子租约时返回（仅 PostgreSQL 支持 `LeasedTaskStore`）。

### 2.3 核心数据结构

#### Task 任务实体

```go
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
```

#### TaskFilter / TaskCounts / TaskLease / TaskResult / TaskFailure

```go
type TaskFilter struct {
    Status string
    Limit  int
    Offset int
}

type TaskCounts struct {
    Pending   int `json:"pending"`
    Running   int `json:"running"`
    Completed int `json:"completed"`
    Failed    int `json:"failed"`
}

type TaskLease struct {
    Task      *Task
    Owner     string
    Token     string
    ExpiresAt time.Time
}

type TaskResult struct {
    OutputJSON string
    Stage      string
}

type TaskFailure struct {
    Error      string
    Retryable  bool
    ErrorClass string
}
```

#### DataSource 数据源资产

```go
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

type DataSourceFilter struct {
    Limit  int
    Offset int
}

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
```

#### AuditLog 审计日志

```go
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
```

#### SnapshotRecord 快照存证

```go
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
```

#### AuditStats / AuditReport

```go
type AuditStats struct {
    TotalOperations int            `json:"total_operations"`
    ByOperation     map[string]int `json:"by_operation"`
    ByStatus        map[string]int `json:"by_status"`
    BySecurityLevel map[string]int `json:"by_security_level"`
    AvgDurationMs   float64        `json:"avg_duration_ms"`
}

type AuditReport struct {
    TotalOperations int            `json:"total_operations"`
    SuccessRate     float64        `json:"success_rate"`
    BySecurityLevel map[string]int `json:"by_security_level"`
    TopOperations   []string       `json:"top_operations"`
    Recommendations []string       `json:"recommendations"`
}
```

#### ChainVerificationResult 链式验真结果

```go
type ChainVerificationResult struct {
    Reason        string `json:"reason"`           // 验真原因码（ChainReason* 常量之一）
    TotalVerified int    `json:"total_verified"`   // 已验证记录总数
    TotalRecords  int    `json:"total_records"`    // 总记录数
    Valid         bool   `json:"valid"`            // 验证是否全部通过
    BrokenAtID    string `json:"broken_at_id,omitempty"`    // 断裂位置记录 ID
    ExpectedHash  string `json:"expected_hash,omitempty"`   // 期望哈希值
    ActualHash    string `json:"actual_hash,omitempty"`     // 实际哈希值
    LegacyHashed  int    `json:"legacy_hashed"`             // 使用旧版哈希的记录数
    Message       string `json:"message"`                   // 人可读的描述信息
}
```

### 2.4 存储接口契约

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

#### `LeasedTaskStore` 接口（PostgreSQL Phase B 原子租约）

```go
type LeasedTaskStore interface {
    TaskStore
    // FOR UPDATE SKIP LOCKED 无阻塞原子竞争领取任务
    ClaimNext(owner string, leaseTTL time.Duration) (*TaskLease, error)
    // 续约租约（CAS 校验 owner 和 token）
    RenewLease(id, owner, token string, leaseTTL time.Duration) (bool, error)
    // 完成任务并释放租约
    CompleteLease(id, owner, token string, result TaskResult) (bool, error)
    // 标记失败并支持指数重试
    FailLease(id, owner, token string, failure TaskFailure) (bool, error)
    // 回收超时孤立租约
    RequeueExpiredLeases(limit int) (int, error)
}
```

#### `DataSourceStore` 接口

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

#### `AuditArchiveReader` 接口

```go
type AuditArchiveReader interface {
    // 分页获取最老的审计日志与快照用于归档
    FetchOldestForArchive(before time.Time, limit int) ([]AuditLog, []SnapshotRecord, error)
    // 按 ID 批量删除已归档的日志记录
    DeleteLogsByIDs(ids []string) (int64, error)
}
```

### 2.5 审计哈希链函数

```go
// SetAuditChainKey 使用 atomic.Pointer 原子设置 HMAC-SM3 链式哈希密钥。
// 设置后审计链从纯 SM3 升级为 HMAC-SM3（AuditHashSM3HMAC），具备防篡改认证能力。
func SetAuditChainKey(key string)

// AuditChainKey 返回当前配置的链式哈希密钥（空字符串表示使用纯 SM3）。
func AuditChainKey() string

// ComputeAuditIntegrityHash 计算审计日志的 9 要素完整性哈希。
// 若配置了链式密钥则使用 HMAC-SM3，否则使用纯 SM3。
func ComputeAuditIntegrityHash(logID, prevHash string, timestamp time.Time,
    algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) string

// ComputeAuditIntegrityHashAlgo 返回当前生效的算法标签。
// 返回 "SM3"（无密钥）或 "SM3-HMAC:v1"（已设置密钥）。
func ComputeAuditIntegrityHashAlgo() string

// VerifyAuditIntegrityHash 验证审计日志的完整性哈希。
// 返回 (valid: 是否有效, algoLabel: 使用的算法标签)。
func VerifyAuditIntegrityHash(stored, logID, prevHash string, timestamp time.Time,
    algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) (bool, string)

// IsCanonicalHashLabel 判断给定的哈希算法标签是否为当前标准算法。
func IsCanonicalHashLabel(label string) bool

// ComputeSnapshotIntegrityHash 计算快照存证记录的完整性哈希。
func ComputeSnapshotIntegrityHash(snapshotID, auditLogID, prevHash string, timestamp time.Time,
    algorithm, inputSample, outputSample, parametersJSON string) string

// VerifySnapshotIntegrityHash 验证快照存证记录的完整性哈希。
// 返回 (valid: 是否有效, algoLabel: 使用的算法标签)。
func VerifySnapshotIntegrityHash(stored, snapshotID, auditLogID, prevHash string, timestamp time.Time,
    algorithm, inputSample, outputSample, parametersJSON string) (bool, string)

// BuildAuditRecommendations 基于安全等级分布与成功率生成审计建议。
// byLevel 为各等级计数（如 {"L3": 120, "L4": 55}），successRate 为成功率百分比。
func BuildAuditRecommendations(byLevel map[string]int, successRate float64) []string
```

**调用范例**：

```go
// 设置链式密钥（启动时调用一次）
store.SetAuditChainKey("my-hmac-chain-secret-key")

// 写入审计日志前计算完整性哈希
hash := store.ComputeAuditIntegrityHash(
    "log-1001", "prev-hash-999", time.Now().UTC(),
    "SM3-HMAC:v1", "in-hash-abc", "out-hash-def",
    "admin", "L3", `{"strategy":"mask"}`,
)

// 验证已有记录
valid, algo := store.VerifyAuditIntegrityHash(
    storedHash, "log-1001", "prev-hash-999", ts,
    "SM3-HMAC:v1", "in-hash-abc", "out-hash-def",
    "admin", "L3", `{"strategy":"mask"}`,
)
if !valid {
    log.Warn("audit chain tampered", "algo", algo, "logID", "log-1001")
}

// 当前算法标签
fmt.Println(store.ComputeAuditIntegrityHashAlgo()) // "SM3-HMAC:v1"

// 生成审计建议
recs := store.BuildAuditRecommendations(
    map[string]int{"L3": 120, "L4": 55, "L5": 10},
    97.5,
)
```

### 2.6 高并发微批缓冲刷盘器 (`pkg/store/flusher`)

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/store/flusher"
```

#### 错误

| 错误变量 | 值 | 触发场景 |
|---|---|---|
| `ErrStoreClosed` | `"audit store is closed"` | 对已关闭的缓冲存储执行操作 |
| `ErrBacklogSaturated` | `"audit flush backlog saturated, underlying storage unavailable"` | 底层存储不可用导致积压队列饱和 |

#### Config 配置结构体

```go
type Config struct {
    BufferSize     int           // 内存队列最大容量
    MaxBatchSize   int           // 单批落盘最大记录数
    FlushInterval  time.Duration // 强制刷盘时间窗口
    EnqueueTimeout time.Duration // 入队超时（队列满时等待上限）
    FlushTimeout   time.Duration // 单次刷盘操作超时
    CloseTimeout   time.Duration // 优雅关闭排空超时
    MaxRetries     int           // 失败重试最大次数
    MaxStaged      int           // 暂存区最大记录数
}

// DefaultConfig 返回推荐的默认刷盘配置。
func DefaultConfig() Config
```

#### BufferedAuditStore 缓冲审计存储

```go
// NewBufferedAuditStore 创建缓冲微批包装器，包装底层 AuditStore 实现。
func NewBufferedAuditStore(underlying store.AuditStore, cfg Config, logger *slog.Logger) *BufferedAuditStore
```

`BufferedAuditStore` 实现完整的 `AuditStore` + `AuditArchiveReader` 接口，并额外提供以下方法：

```go
// AuditStore 全部方法（SaveLog, SaveLogWithSnapshot, SaveLogsBatch, GetLog,
// GetLatestLog, ListLogs, GetStats, GenerateReport, SaveSnapshot, ListSnapshots,
// GetSnapshot, VerifyChain, CleanupOld）
// + AuditArchiveReader 方法:
func (b *BufferedAuditStore) FetchOldestForArchive(before time.Time, limit int) ([]store.AuditLog, []store.SnapshotRecord, error)
func (b *BufferedAuditStore) DeleteLogsByIDs(ids []string) (int64, error)

// 缓冲控制与诊断
func (b *BufferedAuditStore) Flush() error          // 手动强制刷盘
func (b *BufferedAuditStore) Close() error           // 优雅关闭并排空队列
func (b *BufferedAuditStore) QueueDepth() int        // 当前队列深度
func (b *BufferedAuditStore) FlushedTotal() int64    // 累计成功刷盘记录数
func (b *BufferedAuditStore) FailedTotal() int64     // 累计刷盘失败记录数
func (b *BufferedAuditStore) OverflowTotal() int64   // 队列溢出丢弃记录数
func (b *BufferedAuditStore) EvictedTotal() int64    // 因重试超限被驱逐记录数
func (b *BufferedAuditStore) RetryPending() int64    // 当前重试队列中的记录数
func (b *BufferedAuditStore) StagedCount() int       // 暂存区当前记录数
func (b *BufferedAuditStore) HasFlushError() bool    // 最近一次刷盘是否出错
func (b *BufferedAuditStore) LastFlushError() string // 最近一次刷盘错误信息
```

**调用范例**：

```go
cfg := flusher.Config{
    BufferSize:     2000,
    MaxBatchSize:   200,
    FlushInterval:  20 * time.Millisecond,
    EnqueueTimeout: 5 * time.Second,
    FlushTimeout:   10 * time.Second,
    CloseTimeout:   30 * time.Second,
    MaxRetries:     3,
    MaxStaged:      5000,
}
bufferedStore := flusher.NewBufferedAuditStore(underlyingStore, cfg, logger)
defer bufferedStore.Close()

// 保存日志（非阻塞入队 + 读己之写即时可见）
err := bufferedStore.SaveLog(logEntry)

// 诊断指标
fmt.Printf("queue=%d flushed=%d failed=%d overflow=%d\n",
    bufferedStore.QueueDepth(),
    bufferedStore.FlushedTotal(),
    bufferedStore.FailedTotal(),
    bufferedStore.OverflowTotal(),
)
```

---

## 三、纵深防御中间件与信封协议 (`pkg/middleware`)

### 3.1 统一 API 响应信封

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/middleware"
```

`PrivShield` 遵循统一的跨语言 API 信封协议：

#### 错误响应格式

```json
{
  "error": {
    "code": "INVALID_ARGUMENT",
    "message": "parameters too large: 1048576 bytes (max 65536 bytes)",
    "detail": null,
    "trace_id": "req-1725091200-a1b2c3d4",
    "timestamp": "2025-01-15T08:30:00Z"
  }
}
```

#### 成功响应格式

```json
{
  "code": "SUCCESS",
  "message": "Operation completed successfully",
  "data": { ... },
  "trace_id": "req-1725091200-a1b2c3d4",
  "timestamp": "2025-01-15T08:30:00Z"
}
```

### 3.2 常量

| 常量 | 说明 |
|---|---|
| `TraceIDContextKey` | 上下文键，用于在 `context.Context` 中存取 Trace ID |
| `TraceHeader` | 上游传入的请求头名称（`X-Request-ID`） |
| `TraceIDHeader` | 下游写出的响应头名称（`X-Trace-ID`） |

### 3.3 信封类型

```go
type ErrorEnvelope struct {
    Code      string `json:"code"`
    Message   string `json:"message"`
    Detail    any    `json:"detail,omitempty"`
    TraceID   string `json:"trace_id"`
    Timestamp string `json:"timestamp"`
}

type SuccessEnvelope struct {
    Code      string `json:"code"`
    Message   string `json:"message"`
    Data      any    `json:"data,omitempty"`
    TraceID   string `json:"trace_id"`
    Timestamp string `json:"timestamp"`
}
```

### 3.4 信封函数

```go
// AbortWithError 中断请求并返回标准 5 字段错误信封。
// 所有 REST 错误响应统一通过此函数输出。
func AbortWithError(c *gin.Context, httpStatus int, code string, message string, detail any)

// RespondWithSuccess 返回标准成功信封。
func RespondWithSuccess(c *gin.Context, httpStatus int, message string, data any)

// ErrorCodeFromStatus 根据 HTTP 状态码推断默认错误码字符串。
func ErrorCodeFromStatus(status int) string

// ExtractErrorMessage 从 gin.Context 中提取已设置的错误消息，fallback 为默认值。
func ExtractErrorMessage(c *gin.Context, fallback string) string
```

**调用范例**：

```go
// Controller 内中断请求
middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid schema parameter", nil)

// 返回成功
middleware.RespondWithSuccess(c, http.StatusOK, "Task created", map[string]string{"task_id": "task-001"})
```

### 3.5 安全与限流类型

```go
type ReadOnlyEndpoint struct {
    Method string // HTTP 方法（"GET", "HEAD" 等）
    Path   string // 路由路径
}

type IPRateLimiter struct {
    // 所有字段未导出
}
```

`IPRateLimiter` 方法：

```go
func (l *IPRateLimiter) Close()          // 关闭清理资源
func (l *IPRateLimiter) Allow(key string) bool // 判断给定 key 是否允许通过
```

### 3.6 中间件函数

#### 请求体与并发控制

```go
// MaxBodySize 限制 HTTP 请求体最大字节数，防御超大报文耗尽内存。
// 超限返回 413 PAYLOAD_TOO_LARGE。
func MaxBodySize(maxBytes int64) gin.HandlerFunc

// MaxConcurrent 控制在途活跃并发请求数上限，超载返回 503 SERVICE_UNAVAILABLE。
func MaxConcurrent(limit int) gin.HandlerFunc
```

#### 令牌桶限流

```go
// NewIPRateLimiter 创建基于客户端 IP 的令牌桶限流器实例。
func NewIPRateLimiter(rps, burst int) *IPRateLimiter

// RateLimit 基于客户端 IP 的令牌桶限流中间件，超限返回 429 TOO_MANY_REQUESTS。
func RateLimit(rps, burst int) gin.HandlerFunc

// RateLimitWithKeyFunc 支持自定义限流键函数的限流中间件。
// keyFunc 从 gin.Context 中提取限流键（如 IP、用户 ID、API Key 等）。
func RateLimitWithKeyFunc(rps, burst int, keyFunc func(*gin.Context) string) gin.HandlerFunc

// NormalizeRateLimitPath 对路径进行归一化处理（将 UUID 和纯数字段替换为占位符），
// 防止路径参数导致限流桶基数膨胀。
func NormalizeRateLimitPath(path string) string

// IsAllDigits 判断字符串是否全部由数字组成。
func IsAllDigits(s string) bool

// IsUUIDFormat 判断字符串是否符合 UUID 格式（8-4-4-4-12）。
func IsUUIDFormat(s string) bool
```

#### 安全头与 CORS

```go
// CORS 跨域来源安全白名单校验与 OPTIONS 预检放行中间件。
func CORS(origins []string) gin.HandlerFunc

// SecurityHeaders 注入 CSP、HSTS、X-Content-Type-Options、X-Frame-Options 等安全响应头。
func SecurityHeaders() gin.HandlerFunc

// SecurityHeadersTo 将安全响应头写入指定的 http.ResponseWriter（非中间件场景使用）。
func SecurityHeadersTo(w http.ResponseWriter)
```

#### 追踪与请求 ID

```go
// RequestID 提取请求头 X-Request-ID，不存在时自动生成纳秒级 trace_id 注入上下文。
func RequestID() gin.HandlerFunc

// TraceMiddleware 追踪中间件，提取或生成 TraceID 并注入响应头 X-Trace-ID。
func TraceMiddleware() gin.HandlerFunc

// GetTraceID 从 gin.Context 中提取当前请求的 Trace ID。
func GetTraceID(c *gin.Context) string
```

#### 崩溃恢复

```go
// Recovery 拦截 Handler 运行时 Panic，防止服务进程崩溃。
// module 参数用于结构化日志中标识模块来源。
func Recovery(logger *slog.Logger, module string) gin.HandlerFunc
```

#### 认证与授权

```go
// Auth 常量时间校验 Authorization: Bearer <Key>，失败返回 401 UNAUTHORIZED。
func Auth(apiKey string) gin.HandlerFunc

// AuthWithRoles 支持读写分离的角色认证中间件。
// readerKey 允许访问 readOnly 中定义的只读端点。
func AuthWithRoles(apiKey, readerKey string, readOnly []ReadOnlyEndpoint) gin.HandlerFunc
```

### 3.7 完整中间件挂载范例

```go
router := gin.New()

// 1. 注册纵深防御中间件
router.Use(middleware.TraceMiddleware())
router.Use(middleware.Recovery(logger, "service-hub"))
router.Use(middleware.SecurityHeaders())
router.Use(middleware.MaxBodySize(32 * 1024 * 1024))
router.Use(middleware.MaxConcurrent(1000))
router.Use(middleware.RateLimit(100, 200))
router.Use(middleware.CORS([]string{"http://localhost:3000"}))
router.Use(middleware.Auth(cfg.APIKey))
```

---

## 四、上游 Agent 客户端与熔断器 (`pkg/agent`)

`pkg/agent.Client` 用于向隐私计算核心引擎下发任务，内置 3 态熔断器保护、超时控制、幂等重试键与链路追踪透传。

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/agent"
```

### 4.1 错误

| 错误变量 | 值 | 触发场景 |
|---|---|---|
| `ErrEndpointUnavailable` | `"no agent endpoint available"` | 未配置任何 Agent 端点 |
| `ErrCircuitOpen` | `"circuit breaker open (cooldown remaining)"` | 熔断器处于 Open 状态，冷却期未过 |
| `ErrTransport` | `"agent transport failure"` | 网络传输层错误（超时、连接拒绝等） |

### 4.2 Config 配置结构体

```go
type Config struct {
    BaseURL        string            // 单端点模式 URL
    BaseURLs       []string          // 多端点模式 URL 列表
    APIKey         string            // API 认证密钥
    Timeout        time.Duration     // 请求超时
    CBThreshold    int               // 熔断器触发阈值（连续错误次数）
    CBCooldown     time.Duration     // 熔断冷却时间（Open -> HalfOpen）
    MaxRetries     int               // 最大重试次数
    RetryBaseDelay time.Duration     // 重试基础延迟
    Logger         *slog.Logger      // 结构化日志器
    StateObserver  func(node, state string) // 熔断器状态变更回调
}
```

### 4.3 幂等键上下文

```go
// ContextWithIdempotencyKey 向 context 注入幂等保护键，防止网络超时导致的重复处理。
func ContextWithIdempotencyKey(ctx context.Context, key string) context.Context

// IdempotencyKeyFromContext 从 context 中提取幂等保护键。
func IdempotencyKeyFromContext(ctx context.Context) string
```

### 4.4 客户端构造与查询

```go
// New 创建具备熔断保护的 Agent 客户端。
func New(cfg Config) *Client
```

### 4.5 Client 方法

```go
// BaseURL 返回当前主端点 URL。
func (c *Client) BaseURL() string

// BaseURLs 返回所有配置的端点 URL 列表。
func (c *Client) BaseURLs() []string

// PickEndpoint 从多端点中选择一个可用端点。
func (c *Client) PickEndpoint() string

// Health 执行健康检查请求，返回引擎状态信息。
func (c *Client) Health(ctx context.Context) (map[string]any, error)

// Get 发送 GET 请求并解析 JSON 响应。
func (c *Client) Get(ctx context.Context, path string) (map[string]any, error)

// Post 发送 POST 请求并解析 JSON 响应。
func (c *Client) Post(ctx context.Context, path string, payload any) (map[string]any, error)

// PostWithRequestID 发送携带指定 RequestID 的 POST 请求。
func (c *Client) PostWithRequestID(ctx context.Context, path string, payload any, requestID string) (map[string]any, error)

// CircuitStateString 返回当前熔断器状态字符串（"closed"/"open"/"half-open"）。
func (c *Client) CircuitStateString() string

// EndpointStates 返回所有端点的当前状态映射。
func (c *Client) EndpointStates() map[string]string
```

**调用范例**：

```go
client := agent.New(agent.Config{
    BaseURL:     "http://127.0.0.1:8079",
    APIKey:      "secret-token",
    Timeout:     30 * time.Second,
    CBThreshold: 5,
    CBCooldown:  30 * time.Second,
    Logger:      logger,
})

// 幂等保护
ctx = agent.ContextWithIdempotencyKey(ctx, fmt.Sprintf("hub-%s-%s-%d",
    task.ID, stage, task.RetryCount))

// 健康检查
status, err := client.Health(ctx)

// 发送脱敏请求
resp, err := client.Post(ctx, "/v1/privacy/mask", payload)

// 熔断器状态
fmt.Println(client.CircuitStateString()) // "closed"
```

---

## 五、全链路可观测性与指标采集 (`pkg/metrics`)

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/metrics"
```

### 5.1 Collector 结构体

`Collector` 聚合所有 Prometheus 指标向量，实现 `naming.Observer` 接口以自动上报命名归一化度量。

```go
type Collector struct {
    HTTPRequestsTotal              *prometheus.CounterVec   // HTTP 请求总数 [method, path, status]
    HTTPRequestDuration            *prometheus.HistogramVec // HTTP 请求延迟 [method, path]
    AgentRequestsTotal             *prometheus.CounterVec   // Agent 调用总数 [endpoint, status]
    AgentRequestDuration           *prometheus.HistogramVec // Agent 调用延迟 [endpoint]
    OrphanedTasksRecovered         *prometheus.CounterVec   // 孤立任务恢复数 [task_type]
    TasksRetried                   *prometheus.CounterVec   // 任务重试数 [result]
    CircuitBreakerState            *prometheus.GaugeVec     // 熔断器状态 [node, state]
    TaskLeaseConflicts             prometheus.Counter       // 租约冲突总数
    TaskLeaseExpired               prometheus.Counter       // 租约过期总数
    TaskClaimLatency               prometheus.Histogram     // 租约抢占延迟
    TaskTransitions                *prometheus.CounterVec   // 任务状态流转 [from, to, result]
    ServiceHubReady                prometheus.Gauge         // 服务就绪状态 (0/1)
    APIAliasRequestsTotal          *prometheus.CounterVec   // API 别名请求 [alias, canonical, target]
    DatasourceNormalizeErrorsTotal *prometheus.CounterVec   // 数据源归一化错误 [reason]
    DatasourceRequestsTotal        *prometheus.CounterVec   // 数据源请求总数 [datasource_id, api_code, status]
}
```

### 5.2 构造函数

```go
// NewCollector 创建指定模块的 Prometheus 指标收集器。
// module 参数用于指标标签区分（如 "service-hub", "audit-log"）。
func NewCollector(module string) *Collector
```

### 5.3 Collector 方法

```go
// RecordHTTP 记录 HTTP 请求度量。
func (c *Collector) RecordHTTP(method, path string, status int, durationSec float64)

// RecordAgentCall 记录 Agent 调用度量。
func (c *Collector) RecordAgentCall(endpoint string, status string, durationSec float64)

// HTTPMiddleware 返回自动记录所有 HTTP 请求度量的 Gin 中间件。
func (c *Collector) HTTPMiddleware() gin.HandlerFunc

// RecordOrphanedRecovery 记录孤立任务恢复事件。
func (c *Collector) RecordOrphanedRecovery(taskType string)

// RecordTaskRetry 记录任务重试事件。
func (c *Collector) RecordTaskRetry(result string)

// SetCircuitBreakerState 设置熔断器状态仪表。
func (c *Collector) SetCircuitBreakerState(node string, state string)

// RecordLeaseConflict 递增租约冲突计数器。
func (c *Collector) RecordLeaseConflict()

// RecordLeaseExpired 递增租约过期计数器。
func (c *Collector) RecordLeaseExpired(count int)

// RecordClaimLatency 记录租约抢占延迟。
func (c *Collector) RecordClaimLatency(durationSec float64)

// RecordTaskTransition 记录任务状态流转事件。
func (c *Collector) RecordTaskTransition(from, to, result string)

// SetReady 设置服务就绪状态（true=1, false=0）。
func (c *Collector) SetReady(ready bool)

// RecordAPIAlias 记录 API 别名解析事件（实现 naming.Observer 接口）。
func (c *Collector) RecordAPIAlias(alias, canonical, target string)

// RecordNormalizeError 记录命名归一化错误（实现 naming.Observer 接口）。
func (c *Collector) RecordNormalizeError(reason string)

// RecordDatasourceRequest 记录数据源请求事件。
// 未在系统登记的脏数据源标识自动映射为固定标签 "unknown"，消除基数膨胀。
func (c *Collector) RecordDatasourceRequest(datasourceID, apiCode, status string)

// Handler 返回 Prometheus /metrics HTTP 处理器。
func (c *Collector) Handler() gin.HandlerFunc
```

**调用范例**：

```go
mc := metrics.NewCollector("audit-log")

// 挂载 HTTP 监控与 Prometheus 抓取端点
router.Use(mc.HTTPMiddleware())
router.GET("/metrics", mc.Handler())

// 注册全局命名观测器
naming.SetObserver(mc)

// 业务埋点
mc.RecordHTTP("POST", "/v1/audit/logs", 200, 0.015)
mc.RecordAgentCall("/v1/privacy/mask", "success", 0.025)
mc.RecordOrphanedRecovery("pipeline")
mc.RecordTaskRetry("success")
mc.RecordDatasourceRequest("ds_yibao", "api1_yibao", "success")
mc.SetReady(true)
```

---

## 六、国密 mTLS 与 CN 白名单 (`pkg/tlsutil`)

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"
```

### 6.1 类型定义

```go
// WhitelistClient 白名单中的单个客户端条目。
type WhitelistClient struct {
    CN            string   `yaml:"cn"`               // 客户端证书 CN
    AllowedScopes []string `yaml:"allowed_scopes"`   // 允许的 gRPC method scope
    Role          string   `yaml:"role,omitempty"`   // 角色标识
    Description   string   `yaml:"description,omitempty"` // 描述信息
    Enabled       *bool    `yaml:"enabled,omitempty"`    // 启用状态（nil 视为 true）
}

// WhitelistConfig 白名单配置文件结构。
type WhitelistConfig struct {
    Version string            `yaml:"version"`
    Clients []WhitelistClient `yaml:"clients"`
    Entries []struct {
        CN          string   `yaml:"cn"`
        Scopes      []string `yaml:"scopes"`
        Description string   `yaml:"description,omitempty"`
        Enabled     *bool    `yaml:"enabled,omitempty"`
    } `yaml:"entries"`
}

// DynamicWhitelist 动态热重载的 CN 白名单管理器。
// 内部监控文件修改时间，支持 5 秒级自动热重载。
type DynamicWhitelist struct {
    // 所有字段未导出
}

// ServerTLSConfig TLS 服务端配置参数。
type ServerTLSConfig struct {
    Enabled          bool   // 是否启用 TLS
    CertFile         string // 服务端证书文件路径
    KeyFile          string // 服务端私钥文件路径
    CAFile           string // CA 根证书文件路径
    ClientAuth       string // 客户端证书校验模式
    PinnedPubKeyFile string // SPKI 公钥固定文件路径
}
```

### 6.2 白名单管理器

```go
// NewDynamicWhitelist 创建动态 CN 白名单管理器，自动监控白名单文件变更并热重载。
func NewDynamicWhitelist(path string) (*DynamicWhitelist, error)
```

**DynamicWhitelist 方法**：

```go
// Close 关闭文件监控并释放资源。
func (dw *DynamicWhitelist) Close()

// IsAuthorized 检查指定 CN 的客户端是否被授权接入。
func (dw *DynamicWhitelist) IsAuthorized(clientCN string) bool

// CheckScope 检查客户端是否具有指定 gRPC method 的调用权限。
// 返回 (authorized, grantedScopes)。
func (dw *DynamicWhitelist) CheckScope(clientCN string, method string) (bool, []string)

// GetScopes 获取客户端被授权的全部 scope 列表。
func (dw *DynamicWhitelist) GetScopes(clientCN string) ([]string, bool)

// UnaryServerInterceptor 返回该白名单的 gRPC Unary 拦截器。
func (dw *DynamicWhitelist) UnaryServerInterceptor() grpc.UnaryServerInterceptor

// StreamServerInterceptor 返回该白名单的 gRPC Stream 拦截器。
func (dw *DynamicWhitelist) StreamServerInterceptor() grpc.StreamServerInterceptor
```

### 6.3 gRPC 拦截器快捷构造

```go
// UnaryServerInterceptor 返回独立的 gRPC Unary 服务端拦截器函数。
func UnaryServerInterceptor() grpc.UnaryServerInterceptor

// StreamServerInterceptor 返回独立的 gRPC Stream 服务端拦截器函数。
func StreamServerInterceptor() grpc.StreamServerInterceptor

// NewWhitelistInterceptor 一站式创建 CN 白名单拦截器。
// 返回 Unary 拦截器、Stream 拦截器、DynamicWhitelist 实例及可能的初始化错误。
func NewWhitelistInterceptor(path string) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor, *DynamicWhitelist, error)
```

### 6.4 TLS 配置与公钥工具

```go
// BuildServerTLSConfig 从 ServerTLSConfig 构建 tls.Config。
// 支持 TLS 1.3 强制、客户端证书校验与 SPKI 公钥固定。
func BuildServerTLSConfig(cfg *ServerTLSConfig) (*tls.Config, error)

// LoadPublicKey 从文件加载公钥（支持 PEM / DER 格式）。
func LoadPublicKey(path string) (crypto.PublicKey, error)

// PublicKeysEqual 比较两个公钥是否等价（支持 RSA / ECDSA / Ed25519）。
func PublicKeysEqual(a, b crypto.PublicKey) bool
```

**调用范例**：

```go
// 一站式创建白名单拦截器
unary, stream, whitelist, err := tlsutil.NewWhitelistInterceptor("/etc/privshield/whitelist.yaml")
if err != nil {
    log.Fatal(err)
}
defer whitelist.Close()

grpcServer := grpc.NewServer(
    grpc.UnaryInterceptor(unary),
    grpc.StreamInterceptor(stream),
)

// 构建服务端 TLS
tlsCfg, err := tlsutil.BuildServerTLSConfig(&tlsutil.ServerTLSConfig{
    Enabled:    true,
    CertFile:   "/etc/certs/server.crt",
    KeyFile:    "/etc/certs/server.key",
    CAFile:     "/etc/certs/ca.crt",
    ClientAuth: "require",
})

// 检查客户端授权
if !whitelist.IsAuthorized("service-hub-client") {
    // 拦截无权限客户端
}
scopes, ok := whitelist.GetScopes("service-hub-client")
```

---

## 七、环境配置与安全门禁 (`pkg/config`)

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/config"
```

### 7.1 错误

| 错误变量 | 值 | 触发场景 |
|---|---|---|
| `ErrAPIKeyRequired` | `"inbound API key must not be empty when listening on a non-loopback address"` | 非回环地址监听但未配置 API Key |
| `ErrTLSRequired` | `"TLS is required by configuration but not enabled"` | 配置要求 TLS 但未启用 |
| `ErrMTLSWhitelistRequired` | `"mTLS CN whitelist file is required when TLS is enabled on the gRPC server"` | gRPC TLS 启用但未配置 CN 白名单 |
| `ErrEncryptionKeyRequired` | `"snapshot encryption key must not be empty when listening on a non-loopback address"` | 非回环地址监听但未配置加密密钥 |
| `ErrChainKeyRequired` | `"evidence hash chain key must not be empty when listening on a non-loopback address"` | 非回环地址监听但未配置哈希链密钥 |

### 7.2 SecurityRequirements 安全需求结构体

```go
type SecurityRequirements struct {
    ServiceName          string   // 服务名称（用于错误消息）
    Hosts                []string // 监听地址列表
    APIKey               string   // API 认证密钥
    TLSEnabled           bool     // TLS 是否已启用
    RequireTLS           bool     // 是否强制要求 TLS
    GRPCEnabled          bool     // gRPC 是否已启用
    MTLSWhitelistFile    string   // mTLS CN 白名单文件路径
    EncryptionKey        string   // 快照加密密钥
    RequireEncryptionKey bool     // 是否强制要求加密密钥
    HashKey              string   // 哈希链密钥
    RequireHashKey       bool     // 是否强制要求哈希链密钥
}
```

### 7.3 安全门禁函数

```go
// ValidateFailClosed 执行 fail-closed 安全校验。
// 非回环地址监听时强制要求 API Key、加密密钥、哈希链密钥等安全基线配置。
// 任一必需配置缺失则返回对应错误，服务应拒绝启动。
func ValidateFailClosed(req SecurityRequirements) error

// IsLoopbackHost 判断给定主机名是否为回环地址（127.0.0.1, ::1, localhost 等）。
func IsLoopbackHost(host string) bool
```

### 7.4 环境变量读取函数

```go
// EnvString 读取环境变量，不存在或为空时返回默认值。
func EnvString(name, def string) string

// EnvStringFirstSet 按顺序检查多个环境变量名，返回第一个非空的值。
func EnvStringFirstSet(names ...string) string

// EnvStringOptional 读取环境变量，不存在时使用默认值（允许为空字符串）。
func EnvStringOptional(name, def string) string

// EnvInt 读取环境变量并解析为整数，解析失败时返回默认值。
func EnvInt(name string, def int) int

// EnvFloat 读取环境变量并解析为浮点数，解析失败时返回默认值。
func EnvFloat(name string, def float64) float64

// EnvBool 读取环境变量并解析为布尔值（支持 1/true/yes/on），解析失败时返回默认值。
func EnvBool(name string, def bool) bool

// EnvStringSlice 读取环境变量并按逗号分隔解析为字符串切片。
func EnvStringSlice(name string) []string
```

**调用范例**：

```go
// 安全门禁校验
err := config.ValidateFailClosed(config.SecurityRequirements{
    ServiceName:          "audit-log",
    Hosts:                []string{config.EnvString("HOST", "0.0.0.0")},
    APIKey:               config.EnvString("API_KEY", ""),
    TLSEnabled:           config.EnvBool("TLS_ENABLED", false),
    RequireTLS:           true,
    EncryptionKey:        config.EnvString("ENCRYPTION_KEY", ""),
    RequireEncryptionKey: true,
    HashKey:              config.EnvString("CHAIN_KEY", ""),
    RequireHashKey:       true,
})
if err != nil {
    log.Fatalf("security baseline not met: %v", err)
}

// 类型安全的环境变量读取
port := config.EnvInt("PORT", 8084)
dbPath := config.EnvString("DB_PATH", "./data/audit.db")
debug := config.EnvBool("DEBUG", false)
origins := config.EnvStringSlice("CORS_ORIGINS")
```

---

## 八、命名治理与安全等级 (`pkg/naming`)

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/naming"
```

### 8.1 常量

#### 安全等级

| 常量 | 值 | 说明 |
|---|---|---|
| `SecurityLevelL1` | `"L1"` | 公开级 |
| `SecurityLevelL2` | `"L2"` | 内部级 |
| `SecurityLevelL3` | `"L3"` | 敏感级 |
| `SecurityLevelL4` | `"L4"` | 高敏感级 |
| `SecurityLevelL5` | `"L5"` | 绝密级 |

#### 标准 API 编号与数据源标识

| 常量 | 值 | 说明 |
|---|---|---|
| `API1Yibao` | `"api1_yibao"` | 医保 API 标准编号 |
| `API2Kangyang` | `"api2_kangyang"` | 康养 API 标准编号 |
| `DSYibao` | `"ds_yibao"` | 医保数据源标准标识 |
| `DSKangyang` | `"ds_kangyang"` | 康养数据源标准标识 |
| `DSMock3` | `"ds_mock3"` | Mock 数据源 3 |
| `DSMock4` | `"ds_mock4"` | Mock 数据源 4 |

#### 状态标识

| 常量 | 值 | 说明 |
|---|---|---|
| `StatusActive` | `"active"` | 活跃状态 |
| `StatusReserved` | `"reserved"` | 保留状态 |

#### 归一化目标

| 常量 | 值 | 说明 |
|---|---|---|
| `TargetDataSourceID` | `"datasource_id"` | 归一化目标为数据源 ID |
| `TargetAPICode` | `"api_code"` | 归一化目标为 API 编号 |
| `TargetPath` | `"path"` | 归一化目标为路径 |

#### 归一化失败原因

| 常量 | 值 | 说明 |
|---|---|---|
| `ReasonUnknown` | `"unknown"` | 未知数据源 |
| `ReasonEmpty` | `"empty"` | 空输入 |
| `ReasonReserved` | `"reserved"` | 命中保留字 |
| `ReasonFormatInvalid` | `"format_invalid"` | 格式不合法 |

### 8.2 错误

| 错误变量 | 值 | 触发场景 |
|---|---|---|
| `ErrUnknownDataSource` | `"unknown datasource id"` | 输入无法映射到已知数据源 |
| `ErrReservedDataSource` | `"reserved datasource"` | 输入命中系统保留关键字 |

### 8.3 全局注册表

```go
// Registry 全局数据源注册表，包含所有已知的数据源条目。
var Registry = []Entry{ ... }  // DSYibao, DSKangyang, DSMock3, DSMock4
```

### 8.4 类型定义

#### Entry 数据源条目

```go
type Entry struct {
    APICode      string            // 标准 API 编号
    DataSourceID string            // 标准数据源标识
    Seq          int               // 排序序号
    DisplayName  map[string]string // 多语言显示名称
    Category     string            // 分类
    FileName     string            // 文件名
    FieldCount   int               // 字段数量
    Aliases      []string          // 别名列表
    Status       string            // 状态（active/reserved）
}
```

#### Observer 观测器接口

```go
// Observer 命名归一化观测接口，用于上报别名使用和归一化错误指标。
// metrics.Collector 实现了此接口。
type Observer interface {
    RecordAPIAlias(alias, canonical, target string)
    RecordNormalizeError(reason string)
}
```

### 8.5 安全等级函数

```go
// SecurityLevelIDs 返回所有安全等级 ID 列表 ["L1","L2","L3","L4","L5"]。
func SecurityLevelIDs() []string

// SecurityLevelNames 返回所有安全等级名称列表。
func SecurityLevelNames() []string

// SecurityLevelLabel 返回指定等级的完整标签（如 "L3" -> "敏感级"）。
func SecurityLevelLabel(level string) string

// SecurityLevelName 返回指定等级的名称（如 "L3" -> "Sensitive"）。
func SecurityLevelName(level string) string

// NormalizeSecurityLevelID 将各种输入格式归一化为标准安全等级 ID。
func NormalizeSecurityLevelID(level string) string

// SecurityLevelRank 返回等级的数值排名（L1=1, L2=2, ...），未知等级返回 0。
func SecurityLevelRank(level string) int

// MaxSecurityLevelID 从给定等级列表中返回最高等级 ID。
func MaxSecurityLevelID(levels ...string) string

// SecurityLevelLabels 返回等级 ID 到标签的映射表。
func SecurityLevelLabels() map[string]string
```

### 8.6 观测器管理函数

```go
// SetObserver 注册全局命名观测器（通常为 metrics.Collector）。
func SetObserver(o Observer)

// CurrentObserver 返回当前注册的全局观测器。
func CurrentObserver() Observer
```

### 8.7 命名治理函数

```go
// AliasConflicts 检测注册表中的别名冲突，返回冲突的别名字符串列表。
func AliasConflicts() []string

// EntryByDataSourceID 根据数据源 ID 查找注册表条目。
func EntryByDataSourceID(id string) (Entry, bool)

// EntryByAPICode 根据 API 编号查找注册表条目。
func EntryByAPICode(code string) (Entry, bool)

// Entries 返回注册表中所有条目的副本。
func Entries() []Entry

// ActiveEntries 返回所有活跃状态的条目。
func ActiveEntries() []Entry

// ActiveDataSourceIDs 返回所有活跃数据源 ID 列表。
func ActiveDataSourceIDs() []string

// AllDataSourceIDs 返回所有数据源 ID 列表（含保留状态）。
func AllDataSourceIDs() []string

// NormalizeDataSourceID 将原始数据源标识归一化为标准格式。
// 支持别名映射、中文字符匹配、历史编号反查。
func NormalizeDataSourceID(raw string) (string, error)

// Normalize 将原始标识归一化并返回完整的 Entry 条目。
func Normalize(raw string) (*Entry, error)

// IsUnknownDataSource 判断错误是否为未知数据源错误。
func IsUnknownDataSource(err error) bool

// IsReserved 判断错误是否为保留数据源错误。
func IsReserved(err error) bool

// CheckWritable 检查指定数据源是否可写（非保留且活跃）。
func CheckWritable(datasourceID string) error

// ResolveInbound 解析入站原始标识为标准数据源 ID。
// 完整支持别名、中文、历史格式的归一化。
func ResolveInbound(raw string) (string, error)

// ValidDataSourceIDFormat 判断字符串是否符合标准数据源 ID 格式。
func ValidDataSourceIDFormat(s string) bool

// ValidAPICodeFormat 判断字符串是否符合标准 API 编号格式。
func ValidAPICodeFormat(s string) bool

// APICodeForDataSource 根据数据源 ID 反查对应的 API 编号。
func APICodeForDataSource(datasourceID string) string

// DataSourceForAPICode 根据 API 编号反查对应的标准数据源 ID。
func DataSourceForAPICode(apiCode string) (string, bool)

// APICodes 返回所有已注册的 API 编号列表。
func APICodes() []string
```

**调用范例**：

```go
// 入站标识归一化
normID, err := naming.ResolveInbound(req.DatasourceID)
if err != nil {
    if naming.IsReserved(err) {
        middleware.AbortWithError(c, http.StatusConflict, "RESERVED_DATASOURCE", err.Error(), nil)
        return
    }
    middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_DATASOURCE_ID", err.Error(), nil)
    return
}

// 反查 API 编号
apiCode := naming.APICodeForDataSource(normID)

// 安全等级查询
rank := naming.SecurityLevelRank("L4")    // 4
label := naming.SecurityLevelLabel("L4")  // "高敏感级"
maxLevel := naming.MaxSecurityLevelID("L2", "L4", "L3") // "L4"
```

---

## 九、输入校验 (`pkg/validation`)

```go
import "github.com/fengzhizi319/PrivShield-go/pkg/validation"
```

### 9.1 枚举变量

```go
// DataSourceTypes 允许的数据源类型枚举
var DataSourceTypes = []string{"database", "api", "file"}

// SensitivityLevels 允许的安全等级枚举（来自 naming.SecurityLevelIDs()）
var SensitivityLevels = naming.SecurityLevelIDs()  // ["L1","L2","L3","L4","L5"]

// HubOperations 调度中枢允许的脱敏算子枚举
var HubOperations = []string{"mask", "k_anon", "dp", "classify", "none"}

// AuditOperations 审计日志允许的操作类型枚举
var AuditOperations = []string{"mask", "classify", "k_anon", "dp", "qol"}

// AuditStatuses 审计日志允许的状态枚举
var AuditStatuses = []string{"success", "failed"}

// TaskStatuses 任务允许的状态枚举
var TaskStatuses = []string{"pending", "running", "completed", "failed"}
```

### 9.2 校验函数

```go
// AllowedValues 校验字段值是否在允许的枚举列表中。
// 不在列表中返回包含字段名、值和允许列表的错误信息。
func AllowedValues(field, value string, allowed []string) error

// PortRange 校验端口号是否在合法范围 (1-65535)。
func PortRange(port int) error

// NonEmpty 校验字段值不为空字符串。
func NonEmpty(field, value string) error

// MaxLength 校验字段值长度不超过上限。
func MaxLength(field, value string, max int) error
```

### 9.3 工具函数

```go
// GenerateID 生成结合纳秒时序与密码学安全随机数的防碰撞唯一 ID。
// 格式: "{prefix}-{纳秒时间戳}-{8位十六进制随机数}"
// 示例: "task-1725091200000-a1b2c3d4"
func GenerateID(prefix string) string

// ParsePagination 从 Gin 上下文中解析分页参数（limit, offset）。
// defaultLimit 为默认每页条数，maxLimit 为最大允许条数（防拖库超限保护）。
func ParsePagination(c *gin.Context, defaultLimit, maxLimit int) (limit, offset int)
```

**调用范例**：

```go
// 枚举校验
err := validation.AllowedValues("operation", req.Operation, validation.HubOperations)
if err != nil {
    middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_OPERATION", err.Error(), nil)
    return
}

// 安全分页
limit, offset := validation.ParsePagination(c, 100, 1000)

// 唯一 ID 生成
taskID := validation.GenerateID("task")
```

---

## 十、调度中枢 `service-hub` 全阶段公共 PKG 调用规范

数据服务调度中枢（`services/service-hub`）作为数据流通与安全治理的**核心调度大脑**，在请求生命周期各阶段均深度集成了 `pkg/` 共享基础库。下表展示了 9 阶段全生命周期与各公共包的调用关系矩阵。

### 10.1 全生命周期拓扑与公共 PKG 调用全景图

```text
┌──────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                    service-hub 服务生命周期与 9 阶段流水线                                       │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
                                                       │
  【阶段一：服务引导与安全基线】─────────────────────────┼──▶ pkg/config (环境变量解析与 fail-closed 安全门禁)
                                                       ├──▶ pkg/tlsutil (mTLS 双向证书、SPKI 公钥固定、CN 白名单)
                                                       ├──▶ pkg/store (SQLite 完整性探针、PostgreSQL 租约连接池初始化)
                                                       │     └──▶ store.SetAuditChainKey (审计哈希链密钥装配)
                                                       └──▶ pkg/metrics & pkg/naming (Prometheus 收集器与命名观测器注册)
                                                       │
  【阶段二：崩溃恢复与生命周期治理】─────────────────────┼──▶ pkg/store (扫描 running 孤立任务置 fail，pending 保留排队)
                                                       └──▶ pkg/metrics (RecordOrphanedRecovery, RecordTaskRetry)
                                                       │
  【阶段三：网络接入与纵深防御中间件】───────────────────┼──▶ pkg/middleware (TraceMiddleware, Recovery, SecurityHeaders,
                                                       │    MaxBodySize, MaxConcurrent, RateLimit, CORS, Auth)
                                                       └──▶ pkg/metrics (HTTPMiddleware 自动记录请求度量)
                                                       │
  【阶段四：入参安全清洗与命名归一】─────────────────────┼──▶ pkg/validation (GenerateID 纳秒时序 ID, AllowedValues 枚举校验,
                                                       │    ParsePagination 分页防拖库, PortRange, NonEmpty, MaxLength)
                                                       └──▶ pkg/naming (ResolveInbound 多源别名归一, IsReserved 保留字阻断,
                                                             APICodeForDataSource 标准 API 反查)
                                                       │
  【阶段五：流水线任务调度与租约竞抢】───────────────────┼──▶ pkg/store (LeasedTaskStore: ClaimNext FOR UPDATE SKIP LOCKED
                                                       │    原子抢占, RenewLease 心跳续约, CompleteLease/FailLease 状态提交,
                                                       │    RequeueExpiredLeases 孤立回收)
                                                       └──▶ pkg/metrics (RecordLeaseConflict, RecordLeaseExpired,
                                                             RecordClaimLatency, RecordTaskTransition)
                                                       │
  【阶段六：原数采样与下游交互】─────────────────────────┼──▶ pkg/agent (Post/PostWithRequestID 链路追踪透传,
                                                       │    ContextWithIdempotencyKey 幂等保护)
                                                       └──▶ pkg/naming (标准标识映射向 datasource-mgr 发起数据采样)
                                                       │
  【阶段七：敏感定级与隐私脱敏计算】─────────────────────┼──▶ pkg/agent (3 态熔断器保护: Closed->Open->HalfOpen,
                                                       │    幂等重试键注入, 一体化医疗流水线调用)
                                                       └──▶ pkg/metrics (SetCircuitBreakerState 状态上报)
                                                       │
  【阶段八：业务指标度量与存证上报】─────────────────────┼──▶ pkg/metrics (RecordDatasourceRequest 有界标签终态上报,
                                                       │    SetReady 就绪探针翻转, Handler Prometheus 导出)
                                                       ├──▶ pkg/store (AuditStore 存证: SaveLog + ComputeAuditIntegrityHash,
                                                       │    VerifyChain 链式验真)
                                                       └──▶ pkg/crypto (EncryptString V2 信封加密快照样本)
                                                       │
  【阶段九：优雅停机与资源排空】─────────────────────────┴──▶ Context 广播排空任务信号量
                                                       ├──▶ gRPC GracefulStop 30s 超时收敛
                                                       ├──▶ flusher.BufferedAuditStore.Close 缓冲排空
                                                       ├──▶ tlsutil.DynamicWhitelist.Close 文件监控关闭
                                                       └──▶ 连接池安全注销与监听套接字释放
```

### 10.2 阶段一：服务引导与安全基线初始化 (Bootstrap & Security Baseline)

在 `service-hub` 启动入口（`cmd/server/main.go`），公共包负责建立零信任安全基线与环境初始化：

1. **环境配置与安全门禁 (`pkg/config`)**：
   - 使用类型安全的 `config.EnvString`、`config.EnvInt`、`config.EnvBool` 读取配置；
   - 调用 `config.ValidateFailClosed` 执行 fail-closed 安全基线校验，非回环地址强制要求 API Key、加密密钥、哈希链密钥。
2. **底层存储完整性探针与初始化 (`pkg/store`)**：
   - **物理探针**：若采用 SQLite，在连接前调用 `sqlite.ValidateIntegrity(cfg.DBPath)` 执行 `PRAGMA integrity_check`，发现文件损坏立即 Fail-Fast；
   - **租约存储装配**：若配置了 `PG_DSN`，调用 `postgres.New` 初始化支持多副本 Hub 的 `LeasedTaskStore`；否则回退为 `sqlite.NewTaskStore` 或 `memory.NewTaskStore`；
   - **审计哈希链密钥装配**：调用 `store.SetAuditChainKey(cfg.ChainKey)` 启用 HMAC-SM3 认证哈希链。
3. **mTLS 双向证书与 CN 白名单拦截器 (`pkg/tlsutil`)**：
   - 调用 `tlsutil.BuildServerTLSConfig` 构建强制 TLS 1.3 的服务端证书配置，支持客户端证书强校验与公钥证书固定（SPKI Pinning）；
   - gRPC 端点挂载 `tlsutil.NewWhitelistInterceptor(cfg.MTLSWhitelistFile)`，实现基于客户端 CN 白名单的 method-scope 动态热重载鉴权。
4. **监控指标与全局命名观测器 (`pkg/metrics` & `pkg/naming`)**：
   - 初始化 `mc := metrics.NewCollector("service-hub")`，注册 QPS、延迟与状态指标；
   - 调用 `naming.SetObserver(mc)` 注册全局观测器，当流量携带非标别名或脏 ID 时自动向 Prometheus 上报别名使用度量。

```go
// main.go 启动引导核心代码片段
mc := metrics.NewCollector("service-hub")
naming.SetObserver(mc)

// 安全门禁
err := config.ValidateFailClosed(config.SecurityRequirements{
    ServiceName:          "service-hub",
    Hosts:                []string{config.EnvString("HOST", "0.0.0.0")},
    APIKey:               config.EnvString("API_KEY", ""),
    TLSEnabled:           config.EnvBool("TLS_ENABLED", false),
    RequireTLS:           true,
    GRPCEnabled:          true,
    MTLSWhitelistFile:    config.EnvString("MTLS_WHITELIST_FILE", ""),
    EncryptionKey:        config.EnvString("ENCRYPTION_KEY", ""),
    RequireEncryptionKey: true,
    HashKey:              config.EnvString("CHAIN_KEY", ""),
    RequireHashKey:       true,
})
if err != nil {
    log.Fatalf("security baseline not met: %v", err)
}

// 审计链密钥装配
store.SetAuditChainKey(config.EnvString("CHAIN_KEY", ""))
```

### 10.3 阶段二：崩溃恢复与历史生命周期治理 (Crash Recovery & Retention)

在服务重启时，调度中枢自动修复意外崩溃留下的孤立脏数据：

1. **孤立任务恢复 (`pkg/store`)**：
   - 调用 `taskStore.List(store.TaskFilter{Status: "running"})` 扫描因服务异常重启或断电卡在 `running` 的任务，安全标记为 `failed` 并写入错误原因；
   - 扫描 `pending` 任务并直接保留在队列中等待重新消费。
2. **指数退避重试与生命周期清理 (`pkg/store` & `pkg/metrics`)**：
   - 自动重试可恢复故障任务，按 `5s * 2^retryCount` 计算 `RetryAfter` 指数退避时间；
   - 启动后台协程定期调用 `taskStore.CleanupOld(cutoff)` 清理超过 `RetentionDays` 保留期的终态历史数据；
   - 恢复与重试全过程调用 `mc.RecordOrphanedRecovery` 与 `mc.RecordTaskRetry` 上报指标。

### 10.4 阶段三：请求接入与纵深防御中间件 (Ingress & Defense Middlewares)

HTTP REST 控制器通过挂载 `pkg/middleware` 实现 API 纵深防御：

| 顺序 | 中间件组件 | 功能说明 | 违规拦截响应 |
|:---|:---|:---|:---|
| 1 | `middleware.TraceMiddleware()` | 提取 `X-Request-ID`，不存在时自动生成纳秒级 `trace_id` | 注入响应头 `X-Trace-ID` |
| 2 | `middleware.Recovery(logger, module)` | 拦截 Handler 运行时 Panic，防止服务进程崩溃 | `500 INTERNAL_ERROR` |
| 3 | `middleware.SecurityHeaders()` | 注入 CSP、HSTS、X-Content-Type-Options、X-Frame-Options 等 | — |
| 4 | `middleware.MaxBodySize(32MB)` | 限制 HTTP 请求体最大 32 MiB，防御超大报文耗尽内存 | `413 PAYLOAD_TOO_LARGE` |
| 5 | `middleware.MaxConcurrent(1000)` | 控制在途活跃并发请求数不超过 1000 | `503 SERVICE_UNAVAILABLE` |
| 6 | `middleware.RateLimit(rps, burst)` | 基于客户端 IP 的令牌桶算法精确限流 | `429 TOO_MANY_REQUESTS` |
| 7 | `middleware.CORS(origins)` | 跨域来源安全白名单校验与 OPTIONS 预检放行 | 阻断跨域访问 |
| 8 | `middleware.Auth(apiKey)` | 常量时间校验 `Authorization: Bearer <Key>` | `401 UNAUTHORIZED` |

所有响应与错误统一调用 `middleware.RespondWithSuccess` 或 `middleware.AbortWithError`，输出规范的 5 字段跨语言 API 信封。

### 10.5 阶段四：入参安全清洗与命名治理 (Validation & Naming Governance)

任务进入调度前，必须经过严格的参数清洗与命名规范化：

1. **纳秒级防碰撞 ID 生成 (`pkg/validation`)**：
   - 调用 `validation.GenerateID("task")` 生成结合时间戳与密码学安全随机数的全局唯一任务编号。
2. **枚举白名单校验与分页安全边界 (`pkg/validation`)**：
   - 调用 `validation.AllowedValues("operation", req.Operation, validation.HubOperations)` 校验脱敏算子合法性；
   - 调用 `validation.ParsePagination(c, 100, 1000)` 解析分页参数，强制限制最大拉取 1000 条，杜绝恶意拖库。
3. **数据源标识归一化与保留字拦截 (`pkg/naming`)**：
   - 接收业务传入的数据源标识（支持中文字符、旧版别名如 `ds_yibao`、历史 API 编号），调用 `naming.ResolveInbound(rawSource)` 自动解析映射为 Canonical 标准标识；
   - 调用 `naming.IsReserved(err)` 检测是否命中系统保留关键字，命中时立即阻断并返回 `409 CONFLICT`；
   - 调用 `naming.APICodeForDataSource(normID)` 反查匹配标准 API 编号。

```go
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

### 10.6 阶段五：流水线任务调度与原子租约竞抢 (Task Scheduling & Leased Execution)

支持单机 SQLite 模式与多副本 PostgreSQL 模式的透明切换：

1. **多副本分布式原子租约 (`LeasedTaskStore`)**：
   - **无阻塞原子抢占**：租约 Worker 轮询调用 `tasks.ClaimNext(owner, leaseTTL)`，底层通过 `FOR UPDATE SKIP LOCKED` 抢占 `pending` 任务并生成 16 字节安全租约令牌；
   - **并发心跳续约**：任务执行期间以 `leaseTTL / 2` 周期异步调用 `tasks.RenewLease`，CAS 校验 owner 与 token；
   - **原子流转与状态提交**：执行完毕调用 `tasks.CompleteLease` 或 `tasks.FailLease`，支持按错误类型决定是否触发指数退避重试；
   - **孤立租约回收**：定期调用 `tasks.RequeueExpiredLeases(100)` 回收崩溃节点超时的未完成租约。
2. **单机并发控制 (`pkg/store`)**：
   - 内存与 SQLite 模式下通过带容量缓冲通道限制最大并发数，调用 `taskStore.Update` 驱动状态机流转。

### 10.7 阶段六与七：原数抽取与一体化隐私计算 (Data Fetching & Privacy Pipeline)

流水线流转到 `fetch` 与 `classify` 阶段时与上下游引擎交互：

1. **链路追踪透传与幂等保护 (`pkg/agent`)**：
   - 向 `datasource-mgr` 请求抽取样本时，通过 `agent.ContextWithIdempotencyKey(ctx, key)` 透传全局追踪 ID 与幂等键；
   - 使用 `client.Post(ctx, path, payload)` 或 `client.PostWithRequestID(ctx, path, payload, requestID)` 发送请求。
2. **熔断保护 (`pkg/agent`)**：
   - 客户端内置 3 态熔断器（连续错误达到 `CBThreshold` 次触发熔断，`CBCooldown` 冷却后 HalfOpen 探测）；
   - 通过 `client.CircuitStateString()` 和 `client.EndpointStates()` 监控熔断状态；
   - 熔断状态变更通过 `Config.StateObserver` 回调上报至 `mc.SetCircuitBreakerState`。

### 10.8 阶段八：指标度量、存证上报与加密快照 (Observability & Evidence)

1. **有界指标度量上报 (`pkg/metrics`)**：
   - 任务终态时调用 `mc.RecordDatasourceRequest(datasourceID, apiCode, status)`；
   - 未在系统登记的脏数据源标识自动映射为固定标签 `"unknown"`，彻底消除基数膨胀；
   - 暴露 `/metrics` 标准 Prometheus 端点 `mc.Handler()`。
2. **存证哈希链与信封加密 (`pkg/store` & `pkg/crypto`)**：
   - 审计日志写入前调用 `store.ComputeAuditIntegrityHash` 计算 9 要素完整性哈希；
   - 快照样本存储前调用 `crypto.EncryptString` 执行 V2 信封加密；
   - 支持链式验真 `auditStore.VerifyChain(limit)` 检测篡改。

### 10.9 阶段九：优雅停机与资源排空 (Graceful Shutdown)

1. **信号捕获与 Context 广播**：
   - 拦截 `SIGINT` / `SIGTERM` 信号，调用 `signal.NotifyContext` 取消全局 Context；
   - 触发 `Server.Shutdown()` 广播通知在途任务协程，等待并发信号量全部释放。
2. **有序关闭序列**：
   - 启动 30 秒超时控制，先调用 `grpcServer.GracefulStop()` 再调用 `httpSrv.Shutdown()`；
   - 调用 `bufferedStore.Close()` 排空微批缓冲区中的审计日志；
   - 调用 `whitelist.Close()` 关闭 CN 白名单文件监控；
   - 有序关闭连接池与监听套接字。
