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
- [七、环境配置与结构化日志 (`pkg/config`)](#七环境配置与结构化日志-pkgconfig)
- [八、输入校验与命名治理 (`pkg/validation` & `pkg/naming`)](#八输入校验与命名治理-pkgvalidation--pkgnaming)

---

## 一、密码与信封加密体系 (`pkg/crypto`)

`pkg/crypto` 提供符合国密标准（GM/T 0004-2012 / GM/T 0002-2012）的密码学原语与动态信封加密实现。

```go
import "github.com/fengzhizi319/PrivShield/pkg/crypto"
```

### 1.1 函数列表

```go
// SumSM3 计算输入数据的 32 字节国密 SM3 原始摘要
func SumSM3(data []byte) [32]byte

// SumSM3Hex 计算输入数据的 64 字符十六进制国密 SM3 摘要
func SumSM3Hex(data []byte) string

// ComputeIntegrityHash 使用标准 UTC 纳秒前像格式计算防篡改哈希
func ComputeIntegrityHash(logID, prevHash string, timestamp time.Time, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) string

// VerifyIntegrityHash 验证存证记录的完整性哈希（优先验证 SM3，自动兼容 Legacy SHA-256）
// 返回值：(valid: 是否有效, isLegacy: 是否为早期历史哈希格式)
func VerifyIntegrityHash(actualHash, logID, prevHash string, timestamp time.Time, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) (bool, bool)

// EncryptSample 使用 SM4-GCM 动态信封加密敏感样本（返回 "enc:v1:<Base64>" 格式）
func EncryptSample(plaintext string, masterKey string) (string, error)

// DecryptSample 解密 SM4-GCM 动态信封密文（若为明文则原样返回）
func DecryptSample(ciphertext string, masterKey string) (string, error)

// IsEnvelopeEncrypted 判断字符串是否为信封加密格式
func IsEnvelopeEncrypted(data string) bool
```

### 1.2 代码调用范例

```go
// 1. 生成防篡改哈希
hash := crypto.ComputeIntegrityHash(
    "log-1001", "prev-hash-999", time.Now().UTC(),
    "SM3", "in-hash-abc", "out-hash-def", "admin", "L3", "{\"strategy\":\"mask\"}",
)

// 2. 存证快照信封加密
masterKey := "my-secret-key-12345"
encryptedSample, err := crypto.EncryptSample("张三 (110101199003072345)", masterKey)
if err != nil {
    log.Fatal(err)
}
// 输出格式: enc:v1:7xX...==

// 3. 透明解密
plain, err := crypto.DecryptSample(encryptedSample, masterKey)
```

---

## 二、持久化与微批存储引擎 (`pkg/store` & `pkg/store/flusher`)

### 2.1 核心数据结构 (`pkg/store/store.go`)

```go
// 审计日志实体
type AuditLog struct {
    ID             string    `json:"id"`
    TaskID         string    `json:"task_id"`
    APICode        string    `json:"api_code"`
    DatasourceID   string    `json:"datasource_id"`
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
    IntegrityHash  string    `json:"integrity_hash,omitempty"` // 9要素 SM3 完整性哈希
}

// 快照存证记录
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

// 链式验真结果
type ChainVerificationResult struct {
    TotalVerified int    `json:"total_verified"`
    Valid         bool   `json:"valid"`
    BrokenAtID    string `json:"broken_at_id,omitempty"`
    ExpectedHash  string `json:"expected_hash,omitempty"`
    ActualHash    string `json:"actual_hash,omitempty"`
    LegacyHashed  int    `json:"legacy_hashed"`
    Message       string `json:"message"`
}
```

### 2.2 存储接口契约

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

### 2.3 高并发微批缓冲刷盘器 (`pkg/store/flusher`)

```go
import "github.com/fengzhizi319/PrivShield/pkg/store/flusher"

// 创建缓冲微批包装器
cfg := flusher.Config{
    BufferSize:    2000,             // 内存队列最大容量
    MaxBatchSize:  200,              // 单批落盘最大记录数
    FlushInterval: 20 * time.Millisecond, // 强制刷盘时间窗口
}
bufferedStore := flusher.NewBufferedAuditStore(underlyingStore, cfg, logger)
defer bufferedStore.Close() // 优雅停机保证排空

// 保存日志（非阻塞入队 + 读己之写即时可见）
err := bufferedStore.SaveLog(logEntry)

// 链式验真（全局连续无断点）
res, err := bufferedStore.VerifyChain(1000)
```

---

## 三、纵深防御中间件与信封协议 (`pkg/middleware`)

### 3.1 统一 API 响应与错误信封 (`pkg/middleware/envelope.go`)

`PrivShield` 遵循统一的跨语言 API 信封协议：

#### 成功响应格式：
```json
{
  "code": "SUCCESS",
  "message": "Operation completed successfully",
  "data": { ... },
  "trace_id": "req-1725091200-a1b2c3d4",
  "via": "audit-log"
}
```

#### 错误响应格式：
```json
{
  "error": {
    "code": "INVALID_ARGUMENT",
    "message": "parameters too large: 1048576 bytes (max 65536 bytes)",
    "details": null,
    "trace_id": "req-1725091200-a1b2c3d4",
    "via": "audit-log"
  }
}
```

### 3.2 中间件挂载与辅助函数

```go
import "github.com/fengzhizi319/PrivShield/pkg/middleware"

router := gin.New()

// 1. 注册基础 9 层中间件
router.Use(middleware.TraceMiddleware("service-hub"))
router.Use(middleware.StructuredLogger(logger))
router.Use(middleware.Recovery(logger))
router.Use(middleware.SecurityHeaders())
router.Use(middleware.MaxBodySize(32 * 1024 * 1024))
router.Use(middleware.MaxConcurrent(1000))
router.Use(middleware.RateLimit(100, 200)) // 100 RPS, 200 Burst
router.Use(middleware.CORS([]string{"http://localhost:3000"}))
router.Use(middleware.Auth(cfg.APIKey))

// 2. Controller 内直接中断并返回错误信封
middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "Invalid schema parameter", nil)

// 3. 返回标准成功信封
middleware.RespondWithSuccess(c, http.StatusOK, resultData)
```

---

## 四、上游 Agent 客户端与熔断器 (`pkg/agent`)

`pkg/agent.Client` 用于向 Python 隐私计算核心引擎下发任务，内置熔断保护、超时与链路追踪透传。

```go
import "github.com/fengzhizi319/PrivShield/pkg/agent"

client := agent.New(&config.Config{
    AgentRESTHost: "127.0.0.1",
    AgentRESTPort: 8079,
    AgentAPIKey:   "secret-token",
})

// 健康状态检查 (带 5 次熔断与 30s 冷却恢复)
healthy, err := client.Health(ctx)

// 发送 POST 请求 (自动注入 X-Trace-ID 并限制 64MiB 响应)
var resp AgentProcessResponse
err := client.Post(ctx, "/v1/privacy/mask", reqPayload, &resp)
```

---

## 五、全链路可观测性与指标采集 (`pkg/metrics`)

### 5.1 Prometheus 指标收集器使用

```go
import "github.com/fengzhizi319/PrivShield/pkg/metrics"

// 初始化模块指标收集器
mc := metrics.NewCollector("audit-log")

// 挂载 HTTP 监控与 Prometheus 抓取端点
router.Use(mc.HTTPMiddleware())
router.GET("/metrics", mc.Handler())

// 业务埋点：记录失败调用与耗时
mc.IncError("DATABASE_LOCK_TIMEOUT")
mc.RecordAgentRequest("/v1/privacy/mask", "success", 15*time.Millisecond)
```

---

## 六、国密 mTLS 与 CN 白名单 (`pkg/tlsutil`)

### 6.1 服务端与客户端 mTLS 证书构造

```go
import "github.com/fengzhizi319/PrivShield/pkg/tlsutil"

// 构造服务端 TLS 1.3 双向认证配置
tlsCfg, err := tlsutil.LoadServerTLSConfig(
    "/etc/certs/server.crt",
    "/etc/certs/server.key",
    "/etc/certs/ca.crt",
    "require", // 客户端证书强校验
)

// 构造具备公钥固定 (SPKI) 的客户端凭证
clientTlsCfg, err := tlsutil.LoadClientTLSConfig(
    "/etc/certs/client.crt",
    "/etc/certs/client.key",
    "/etc/certs/ca.crt",
)
```

### 6.2 动态 CN 白名单管理器 (`tlsutil.WhitelistManager`)

```go
wm := tlsutil.NewWhitelistManager("/etc/privshield/whitelist.yaml", nil)

// 检查是否允许某客户端接入 (支持文件修改热重载)
if !wm.IsAllowed("service-hub-client") {
    // 拦截无权限客户端
}

// 检查客户端权限 scope
scopes := wm.GetScopes("service-hub-client")
```

---

## 七、环境配置与结构化日志 (`pkg/config`)

```go
import "github.com/fengzhizi319/PrivShield/pkg/config"

// 类型安全的环境变量读取
port := config.GetEnvInt("PORT", 8084)
dbPath := config.GetEnv("DB_PATH", "./data/audit.db")
rateLimit := config.GetEnvDuration("RATE_LIMIT_INTERVAL", 10*time.Second)
origins := config.GetEnvSlice("CORS_ORIGINS", []string{"*"})

// 初始化标准化结构化日志 (JSON / Text 格式)
logger := config.SetupLogger("info", "json")
logger.Info("service starting", "port", port, "db_path", dbPath)
```

---

## 八、输入校验与命名治理 (`pkg/validation` & `pkg/naming`)

```go
import (
    "github.com/fengzhizi319/PrivShield/pkg/naming"
    "github.com/fengzhizi319/PrivShield/pkg/validation"
)

// 1. 字段名称安全清洗与规范化
normCol := naming.NormalizeFieldName("User ID #1") // 输出: "user_id_1"
normAPI := naming.NormalizeAPICode("api.v1.mask")   // 输出: "api_v1_mask"

// 2. 生成具备纳秒时序与密码学随机数的防碰撞唯一 ID
taskID := validation.GenerateSecureID("task") // 输出: "task-1725091200000-a1b2c3d4"

// 3. 安全分页参数解析与防拖库超限保护
limit, offset := validation.ParsePagination(c.Query("limit"), c.Query("offset"), 100)
```
