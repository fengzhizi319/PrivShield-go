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
- [九、调度中枢 `service-hub` 全阶段公共 PKG 调用规范](#九调度中枢-service-hub-全阶段公共-pkg-调用规范)

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

---

## 九、调度中枢 `service-hub` 全阶段公共 PKG 调用规范

数据服务调度中枢（`services/service-hub`）作为数据流通与安全治理的**核心调度大脑**，在请求生命周期各阶段均深度集成了 `pkg/` 共享基础库。下表展示了全生命周期各阶段与公共包的调用关系矩阵：

### 9.1 全生命周期拓扑与公共 PKG 调用全景图

```text
┌──────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                    service-hub 服务生命周期与 6 阶段流水线                                       │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
                                                       │
  【阶段一：服务引导与配置装配】───────────────────────┼──▶ pkg/config (环境变量解析与 slog 结构化日志)
                                                       ├──▶ pkg/tlsutil (mTLS 双向证书、SPKI 公钥固定、CN 白名单)
                                                       ├──▶ pkg/store (SQLite 完整性探针、PostgreSQL 租约连接池初始化)
                                                       └──▶ pkg/metrics & pkg/naming (Prometheus 收集器与命名观测器)
                                                       │
  【阶段二：崩溃恢复与生命周期治理】───────────────────┼──▶ pkg/store (扫描 running 孤立任务置 fail，pending 保留排队)
                                                       └──▶ pkg/metrics (记录孤立任务恢复与指数退避重试指标)
                                                       │
  【阶段三：网络接入与 9 层纵深防御】──────────────────┼──▶ pkg/middleware (TraceID、Logger、Recovery、CORS、Auth、
                                                       │                    SecurityHeaders、MaxBodySize、MaxConcurrent)
                                                       │
  【阶段四：入参安全清洗与命名归一】───────────────────┼──▶ pkg/validation (纳秒时序防碰撞 ID 生成、枚举白名单、分页防拖库)
                                                       └──▶ pkg/naming (多源别名映射归一、保留字阻断、标准 API 反查)
                                                       │
  【阶段五：流水线任务调度与租约竞抢】─────────────────┼──▶ pkg/store (PostgreSQL FOR UPDATE SKIP LOCKED 原子租约抢占、
                                                       │               后台心跳续约、超时孤立租约回收、状态机流转)
                                                       │
  【阶段六：原数采样与下游交互】───────────────────────┼──▶ pkg/agent (RequestID 上下文注入、超时控制)
                                                       └──▶ pkg/naming (向 datasource-mgr 发起标准标识数据采样)
                                                       │
  【阶段七：敏感定级与隐私脱敏计算】───────────────────┼──▶ pkg/agent (3 态熔断器保护、幂等重试键、一体化医疗流水线)
                                                       │
  【阶段八：业务指标度量与存证上报】───────────────────┼──▶ pkg/metrics (有界标签终态上报、就绪探针状态翻转、Prometheus 导出)
                                                       │
  【阶段九：优雅停机与资源排空】───────────────────────┴──▶ Context 广播排空任务信号量、gRPC 30s 超时收敛、连接池安全注销
```

---

### 9.2 阶段一：服务引导与安全基线初始化 (Bootstrap & Security Baseline)

在 `service-hub` 启动入口（`cmd/server/main.go`），公共包负责建立零信任安全基线与环境初始化：

1. **环境配置与结构化日志 (`pkg/config`)**：
   - 调用 `pkgconfig.SetupLogger(cfg.LogFormat, cfg.LogLevel)` 构建全局 `slog.Logger`（支持 JSON/Text 格式）；
   - 使用类型安全的 `pkgconfig.GetEnv` 读取配置，并在 `cfg.Validate()` 中执行一致性校验。
2. **底层存储完整性探针与初始化 (`pkg/store` / `pkg/store/sqlite` / `pkg/store/postgres`)**：
   - **物理探针**：若采用 SQLite，在连接前调用 `sqlite.ValidateIntegrity(cfg.DBPath)` 执行 `PRAGMA integrity_check`，发现文件损坏立即 Fail-Fast；
   - **租约存储装配**：若配置了 `PG_DSN`，调用 `postgres.New` 初始化支持多副本 Hub 的 `LeasedTaskStore`；否则回退为 `sqlite.NewTaskStore` 或 `memory.NewTaskStore`。
3. **mTLS 双向证书与 CN 白名单拦截器 (`pkg/tlsutil`)**：
   - 调用 `tlsutil.BuildServerTLSConfig` 构建强制 TLS 1.3 的服务端证书配置，并支持客户端证书强校验与公钥证书固定（SPKI Pinning）；
   - gRPC 端点挂载 `tlsutil.NewWhitelistInterceptor(cfg.MTLSWhitelistFile)`，实现基于客户端 CN 白名单的 method-scope 动态热重载鉴权。
4. **监控指标与全局命名观测器 (`pkg/metrics` & `pkg/naming`)**：
   - 初始化 `mc := metrics.NewCollector("service-hub")`，注册 QPS、延迟与状态指标；
   - 调用 `naming.SetObserver(mc)` 注册全局观测器，当流量携带非标别名或脏 ID 时自动向 Prometheus 上报别名使用度量。

```go
// main.go 启动引导核心代码片段
logger := pkgconfig.SetupLogger(cfg.LogFormat, cfg.LogLevel)
if cfg.PGDSN == "" && cfg.DBPath != "" {
    if err := sqlite.ValidateIntegrity(cfg.DBPath); err != nil {
        log.Fatalf("sqlite integrity check failed: %v", err)
    }
}
taskStore, _ := initLeasedTaskStore(cfg, logger)
mc := metrics.NewCollector("service-hub")
naming.SetObserver(mc)
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

### 9.4 阶段三：请求接入与 9 层安全防御中间件 (Ingress & 9-Layer Defense Middlewares)

HTTP REST 控制器（`internal/handlers/handlers.go`）通过挂载 `pkg/middleware` 实现 API 纵深防御：

| 顺序 | 中间件组件 | 功能说明 | 违规拦截响应 |
|:---|:---|:---|:---|
| 1 | `middleware.TraceMiddleware()` | 提取请求头 `X-Request-ID`，不存在时自动生成纳秒级 `trace_id` 注入上下文 | 注入响应头 `X-Trace-ID` |
| 2 | `middleware.StructuredLogger(logger)` | 统一输出包含 client_ip、method、path、status、latency_ms 的结构化日志 | — |
| 3 | `middleware.Recovery(logger)` | 拦截 Handler 运行时 Panic，防止服务进程崩溃 | `500 INTERNAL_ERROR` |
| 4 | `middleware.SecurityHeaders()` | 注入 CSP、HSTS、X-Content-Type-Options、X-Frame-Options 等安全响应头 | — |
| 5 | `middleware.MaxBodySize(32MB)` | 限制 HTTP 请求体最大 32 MiB，防御超大报文耗尽内存 | `413 PAYLOAD_TOO_LARGE` |
| 6 | `middleware.MaxConcurrent(1000)` | 控制在途活跃并发请求数不超过 1000，超载保护 | `503 SERVICE_UNAVAILABLE` |
| 7 | `middleware.RateLimit(rps, burst)` | 基于客户端 IP 的令牌桶算法精确限流 | `429 TOO_MANY_REQUESTS` |
| 8 | `middleware.CORS(origins)` | 跨域来源安全白名单校验与 OPTIONS 预检放行 | 阻断跨域访问 |
| 9 | `middleware.Auth(apiKey)` | 常量时间校验 `Authorization: Bearer <Key>` | `401 UNAUTHORIZED` |

所有响应与错误统一调用 `middleware.RespondWithSuccess` 或 `middleware.AbortWithError`，输出规范的 5 字段跨语言 API 信封。

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

1. **链路追踪透传 (`pkg/agent`)**：
   - 向 `datasource-mgr` 请求抽取样本时，调用 `agent.ContextWithRequestID(ctx, requestID)` 透传全局追踪 ID。
2. **一体化医疗隐私流水线与熔断保护 (`pkg/agent`)**：
   - 替代传统的“分类定级 + 脱敏处理”两次网络往返，一次调用 `s.agent.ProcessMedical(ctx, records)`；
   - 注入幂等保护键 `agent.ContextWithIdempotencyKey(ctx, fmt.Sprintf("hub-%s-%s-%d", task.ID, stage, task.RetryCount))`，防止网络超时导致的重复处理；
   - 客户端内置 3 态熔断器（连续 5 次错误触发熔断，30s 冷却后 HalfOpen 探测），保护 Core Engine 免于雪崩。

---

### 9.8 阶段八 & 九：指标度量上报与优雅停机收敛 (Observability & Graceful Shutdown)

1. **有界指标度量上报 (`pkg/metrics`)**：
   - 任务终态时调用 `s.mc.RecordDatasourceRequest(datasourceID, apiCode, status)`；
   - 未在系统登记的脏数据源标识自动映射为固定标签 `"unknown"`，彻底消除由于任意输入导致的时间序列基数膨胀（Cardinality Explosion）；
   - 暴露 `/metrics` 标准 Prometheus 端点。
2. **优雅停机与资源排空 (`pkg/tlsutil` & `pkg/store`)**：
   - 拦截 `SIGINT` / `SIGTERM` 信号，调用 `signal.NotifyContext` 取消全局 Context；
   - 触发 `Server.Shutdown()` 广播通知在途任务协程，等待并发信号量（`taskSem`）全部释放；
   - 启动 30 秒超时控制，先调用 `grpcServer.GracefulStop()` 再调用 `httpSrv.Shutdown()`，有序关闭连接池与监听套接字。

