# PrivShield 共享基础包 (Shared PKG) — 开发者实战与架构进阶指南

> **文档定位**：面向全栈开发者的快速上手、源码走读、实战拓展 SOP 与防坑指南（Anti-Patterns）。

---

## 目录

- [一、新手上手与架构全貌](#一新手上手与架构全貌)
- [二、核心业务链路源码走读](#二核心业务链路源码走读)
  - [2.1 数据存证写入链路：从 HTTP 请求到微批落盘](#21-数据存证写入链路从-http-请求到微批落盘)
  - [2.2 分布式任务调度链路：原子争抢与令牌续期](#22-分布式任务调度链路原子争抢与令牌续期)
  - [2.3 敏感数据快照链路：SM4-GCM 信封加密 (enc:v2 / enc:v1)](#23-敏感数据快照链路sm4-gcm-信封加密-encv2--encv1)
  - [2.4 存证哈希链密钥化：HMAC-SM3 注入与多轨向下兼容核验](#24-存证哈希链密钥化hmac-sm3-注入与多轨向下兼容核验)
  - [2.5 存证留存红线：先归档后删除的不可绕过链路](#25-存证留存红线先归档后删除的不可绕过链路)
- [三、核心场景扩展实战 SOP](#三核心场景扩展实战-sop)
  - [SOP-1：如何新增一个生产级 Gin 中间件](#sop-1如何新增一个生产级-gin-中间件)
  - [SOP-2：如何实现一个新的存储后端 (如 MySQL / TiDB)](#sop-2如何实现一个新的存储后端-如-mysql--tidb)
  - [SOP-3：如何安全添加 Prometheus 监控指标](#sop-3如何安全添加-prometheus-监控指标)
  - [SOP-4：如何新增一个安全门禁检查 (ValidateFailClosed 模式)](#sop-4如何新增一个安全门禁检查-validatefailclosed-模式)
  - [SOP-5：如何新增一个命名注册表条目 (naming.Registry)](#sop-5如何新增一个命名注册表条目-namingregistry)
- [四、典型反模式与避坑指南 (Anti-Patterns)](#四典型反模式与避坑指南-anti-patterns)

---

## 一、新手上手与架构全貌

`pkg` 模块是 `PrivShield` 各微服务的公共基础。在本地开发时，推荐通过 Go Work 模式进行无缝调试：

```bash
cd /home/charles/code/PrivShield-go

# 查看工作区包含的 8 大模块
cat go.work

# 运行 pkg 单测
go test -v ./pkg/...
```

---

## 二、核心业务链路源码走读

### 2.1 数据存证写入链路：从 HTTP 请求到微批落盘

```text
1. audit-log 接收 HTTP POST /api/audit/logs
        │
2. 进入 pkg/middleware.TraceMiddleware 注入 X-Trace-ID
        │
3. 进入 Controller: handlers.CreateLog
        │
4. 调用 pkg/crypto.EncryptString 对原始输入/输出样本执行 SM4-GCM 信封加密（输出 enc:v2: 格式）
        │
5. 调用 store.SaveLogWithSnapshot(log, snap) ──► pkg/store/flusher.BufferedAuditStore
        │
   ┌────┴────────────────────────────────────────┐
   │ 内存暂存 (recentLogs) ➔ 保证读己之写即时可见  │
   │ 非阻塞推入 queue (chan pendingItem)        │
   └────┬────────────────────────────────────────┘
        │
6. 后台单一协程 flushWorker:
   a. FIFO 弹出微批 (最大 200 条或 20ms 定时器触发)
   b. 串行绑定: item.PrevHash = lastHash
   c. 规范重算: item.IntegrityHash = store.ComputeAuditIntegrityHash(...)
   d. 推进状态: lastHash = item.IntegrityHash
   e. 批量落盘: underlying.SaveLogsBatch(logs, snaps) ──► SQLite WAL / PostgreSQL
```

### 2.2 分布式任务调度链路：原子争抢与令牌续期

```text
1. service-hub 多个实例并发启动调度 Worker
        │
2. 循环调用 postgres.LeasedTaskStore.ClaimNext(workerID, 30s)
        │
3. 执行 SQL: SELECT ... FOR UPDATE SKIP LOCKED LIMIT 1
        │
4. 成功领取任务并生成 lease_token (如 "tok-abc-123")
        │
5. 执行实际隐私计算（调用上游 Agent 隐私引擎）
        │ (若执行耗时较长，定期调用 RenewLease 续期)
        │
6. 执行完成调用 CompleteLease(taskID, workerID, "tok-abc-123", result)
   SQL 校验: WHERE id=$1 AND lease_owner=$2 AND lease_token=$3 AND expires_at>=NOW()
        │
7. 返回 true 表示提交成功；若返回 false 说明租约已被接管，当前节点放弃结果。
```

### 2.3 敏感数据快照链路：SM4-GCM 信封加密 (enc:v2 / enc:v1)

> 源码：`pkg/crypto/envelope.go`

本模块实现基于国密 SM4-GCM (GB/T 32907-2016) 的信封加密，当前写入格式为 `enc:v2:`，旧格式 `enc:v1:` 仅保留解密能力，不再用于写入。

**enc:v2:（当前写入格式，HKDF-SM3 派生）**

```text
明文数据: "张三, 110101199003072345"
        │
crypto.EncryptString(plaintext, masterKey)
        │
1. secret 为空 → 返回 ErrEmptyKey（不再静默降级为明文，直接拒绝落盘）
2. 生成 16 字节密码学随机 salt: [0xa1, 0xb2, ...]
3. HKDF-Extract/Expand (RFC 5869, 杂凑函数 SM3):
   - Extract: PRK = HMAC-SM3(salt, masterKey)
   - Expand:  DERIVED_KEY = HKDF-Expand(PRK, info="PrivShield audit snapshot SM4-GCM v2", 16)
   （info 将派生密钥绑定到「审计快照加密」用途，防止跨用途密钥复用）
4. 以 DERIVED_KEY 创建 SM4 Cipher，生成 12 字节随机 Nonce
5. 以版本前缀 "enc:v2:" 作为 AAD 执行 SM4-GCM Seal（前缀参与认证，剥离/改写前缀直接导致认证失败）
6. 拼接: salt(16B) + Nonce(12B) + SM4 密文 + AuthTag(16B)
7. Base64 编码并拼接前缀: "enc:v2:" + Base64(...)
        │
写入存储: "enc:v2:rKPh...（salt+nonce+ciphertext+tag 的 Base64）"
```

**enc:v1:（历史存量格式，SHA-256 派生，仅可读）**

```text
写入格式（已废弃）: "enc:v1:" + Base64( Nonce(12B) + SM4 密文 + Tag(16B) )
密钥派生: SHA-256(secret)[:16]（无 salt，弱派生口径）
```

**读取时透明兼容解密**

```text
crypto.DecryptString(ciphertext, masterKey)
        │
  ┌─────┼────────────────────────────────┐
  │     │                                │
enc:v2: enc:v1:                       无前缀
  │     │                                │
HKDF 派生  SHA-256 派生              返回 ErrUnencryptedValue
+ AAD 认证   (存量兼容)              （禁止当作明文返回）
```

**关键设计要点：**
- 空密钥一律返回 `ErrEmptyKey`，不再原样返回明文，消除静默明文落盘风险；
- 无前缀值返回 `ErrUnencryptedValue`，防止攻击者剥离前缀降级为明文；
- v2 前缀参与 GCM AAD，改写或剥离 `enc:v2:` 前缀会直接导致认证失败。

### 2.4 存证哈希链密钥化：HMAC-SM3 注入与多轨向下兼容核验

> 源码：`pkg/store/audit_hash.go`

存证哈希链是防篡改的核心机制。本模块提供进程级 HMAC 密钥注入，使新写入记录具备密钥化完整性保护，同时保留对多种历史格式的向下兼容核验能力。

**启动时注入密钥**

```go
// 在进程启动阶段调用一次（如 main.go），运行期改钥会导致既有记录核验失败。
store.SetAuditChainKey(os.Getenv("AUDIT_LOG_HASH_KEY"))
```

`SetAuditChainKey` 将密钥存入 `atomic.Pointer[string]`，保证并发读安全。传空串则退回无密钥 SM3 口径。

**写入时：ComputeAuditIntegrityHash**

```text
ComputeAuditIntegrityHash(logID, prevHash, timestamp, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON)
        │
1. 构建 UTC 归一化的 9 要素前映像 payload:
   "prevHash|logID|timestamp(UTC RFC3339Nano)|algorithm|inputHash|outputHash|user|securityLevel|paramsJSON"
        │
2. 判断是否配置了 HMAC 密钥 (AuditChainKey())：
   ├── 有密钥 → 返回 HMAC-SM3(key, "SM3-HMAC:v1|" + payload)，编码为 64 位小写十六进制
   └── 无密钥 → 返回 SM3(payload)，编码为 64 位小写十六进制
```

**核验时：VerifyAuditIntegrityHash（多轨向下兼容）**

```text
VerifyAuditIntegrityHash(stored, logID, prevHash, timestamp, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON)
        │
返回: (bool, string) ── bool: 是否通过校验; string: 成功匹配的算法标签
        │
按优先级依次尝试以下候选（最多 5 条路径）：
  1. SM3-HMAC:v1          （密钥化 HMAC-SM3，UTC 前映像，当前写入口径）
  2. SM3                  （无密钥 SM3，UTC 前映像）
  3. SHA256-LEGACY        （旧版 SHA-256，UTC 前映像）
  4. SM3-LEGACY           （无密钥 SM3，本地时区前映像，历史兼容）
  5. SHA256-LEGACY        （旧版 SHA-256，本地时区前映像，历史兼容）
        │
任一候选以常量时间比较 (hmac.Equal) 命中存储值 → 返回 (true, 该候选标签)
全部未命中 → 返回 (false, "")，说明数据已被篡改或损坏
```

**标签规范态判定：**

`IsCanonicalHashLabel(label)` 判断核验命中的标签是否与「当前写入口径」一致。注入密钥后，无密钥 SM3 记录不再视为规范态，而被计入「待重签」，供重签工具升级为密钥化口径。

### 2.5 存证留存红线：先归档后删除的不可绕过链路

> 源码：`services/audit-log/internal/archive/archive.go`

数安法与等保三级要求存证记录在物理删除前必须先完成归档留存。本模块实现「先归档、再验真、后删除」的 fail-closed 链路，任一环节失败即拒绝删除。

**配置约束**

| 环境变量 | 默认值 | 约束 |
|---|---|---|
| `AUDIT_LOG_RETENTION_DAYS` | `0` | 0 = 永不物理删除；>0 时不得低于 `1095`（三年） |

当 `AUDIT_LOG_RETENTION_DAYS > 0` 时，`Config.Validate()` 强制要求同时配置 `AUDIT_LOG_ARCHIVE_DIR` 和 `AUDIT_LOG_ENCRYPTION_KEY`，否则拒绝启动。

**归档执行流程：ArchiveAndCleanup**

```text
Archiver.ArchiveAndCleanup(auditStore, cutoff)
        │
1. 断言 auditStore 实现 store.AuditArchiveReader 接口（否则返回 ErrStoreUnsupported）
        │
2. 分页循环（最多 100000 页，每页默认 500 条）：
   │
   a. reader.FetchOldestForArchive(cutoff, pageSize)
      → 按规范链序 (seq ASC, timestamp ASC, id ASC) 取最早到期记录
      → BufferedAuditStore 先 Flush 到持久化屏障再下沉（fail-closed）
   │
   b. writeSegment(logs, snaps, cutoff)
      → 逐行写入 NDJSON，同时构建 SM3 行哈希链：chain[i] = SM3(chain[i-1] || line[i])
      → gzip 压缩后以 SM4-GCM (enc:v2:) 加密密封
      → 段文件名: audit-archive-<cutoff-UTC>-<seq>.ndjson.gz.enc
      → 同时写入清单文件: audit-archive-<cutoff-UTC>-<seq>.manifest.json
        （含链尾值、条数、时间边界等元数据）
   │
   c. VerifySegment(dir, segment, key)  ← 回读验真（fail-closed 关键步骤）
      → 解密 + 解压 + 重放行哈希链 + 比对链尾值
      → 重算每条日志的 9 要素完整性哈希
      → 校验条数与时间边界
      → 任一项不匹配即返回错误，删除被拒绝
   │
   d. reader.DeleteLogsByIDs(ids)
      → 仅在该段归档并验真成功后才执行精确删除
      → 若删除数为 0 返回 ErrNotDeleted，中止后续分页
   │
3. 返回 Stats（归档条数、删除条数、段文件列表）
```

**Fail-Closed 保障：**
- 归档目录或加密密钥缺失 → `archive.New()` 直接返回错误，不构造归档器；
- 归档段写入失败 → 返回错误，删除被拒绝（`"archive segment failed, deletion refused"`）；
- 归档段回读验真失败 → 返回错误，删除被拒绝（`"archive segment verification failed, deletion refused"`）；
- 存储层不支持 `AuditArchiveReader` → 返回 `ErrStoreUnsupported`，删除被拒绝；
- 删除未生效 → 返回 `ErrNotDeleted`，中止后续分页，避免重复归档。

**归档段独立验真：**

任何持有段文件、清单文件与密钥的第三方，无需访问数据库即可独立判定归档证据是否被增删改：

```bash
# 独立验真流程（伪代码）
manifest = ReadManifest(dir, segment)       # 读取清单
sealed   = ReadFile(dir, segment)           # 读取加密段
gz       = DecryptString(sealed, key)       # SM4-GCM 解密
raw      = gunzip(gz)                       # gzip 解压
chain    = ""
for line in raw.Lines():
    chain = SM3Hex(chain + line)            # 重放行哈希链
assert chain == manifest.ChainTail           # 比对链尾
for log in raw.Logs():
    VerifyAuditIntegrityHash(...)            # 逐条验真 9 要素哈希
```

---

## 三、核心场景扩展实战 SOP

### SOP-1：如何新增一个生产级 Gin 中间件

1. 在 `pkg/middleware/` 下新建源文件，例如 `my_guard.go`；
2. 编写返回 `gin.HandlerFunc` 的构造函数，并利用 `middleware.AbortWithError` 进行异常中断：
   ```go
   package middleware

   import (
       "net/http"
       "github.com/gin-gonic/gin"
   )

   func MyGuard(requiredHeader string) gin.HandlerFunc {
       return func(c *gin.Context) {
           val := c.GetHeader(requiredHeader)
           if val == "" {
               AbortWithError(c, http.StatusBadRequest, "HEADER_MISSING", "Required header is missing", nil)
               return
           }
           c.Next()
       }
   }
   ```
3. **架构铁律（机制与策略解耦）**：`pkg/` 中台基础包严禁直接硬编码或隐式赋值具体服务的环境变量字符串（如写死 `"SERVICE_NAME_XXX"`），也不维护多级次级变量兼容。若需要从环境读取辅助配置，必须设计为参数驱动，由入参接收 `envKey string` 或配置前缀，未配置或传入空键时安全返回空/`nil`，业务层在服务入口显式传入其专属变量名；
4. 在 `pkg/middleware/middleware_test.go` 中编写对应测试用例（覆盖空键、专属键与格式解析等场景）。

### SOP-2：如何实现一个新的存储后端 (如 MySQL / TiDB)

1. 在 `pkg/store/` 下新建目录 `mysql/`；
2. 实现 `store.AuditStore` 接口中定义的所有方法；
3. 若需支持存证留存归档，还需实现 `store.AuditArchiveReader` 接口：
   ```go
   type AuditArchiveReader interface {
       FetchOldestForArchive(before time.Time, limit int) ([]AuditLog, []SnapshotRecord, error)
       DeleteLogsByIDs(ids []string) (int64, error)
   }
   ```
   `FetchOldestForArchive` 必须按规范链序 `(seq ASC, timestamp ASC, id ASC)` 正序返回最早到期记录，使归档顺序与 `VerifyChain` 的回放序一致；
4. 编写 `SaveLogsBatch` 原子批量写入逻辑；
5. 利用 `pkg/store/audit_hash.go` 中的标准规范生成存证哈希：
   ```go
   if log.IntegrityHash == "" {
       log.IntegrityHash = store.ComputeAuditIntegrityHash(
           log.ID, log.PrevHash, log.Timestamp, log.Algorithm,
           log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON,
       )
   }
   ```
6. 接入 `flusher.NewBufferedAuditStore(mysqlStore, cfg, logger)` 即可直接获得微批与哈希链连续性支持。

### SOP-3：如何安全添加 Prometheus 监控指标

1. 在 `pkg/metrics/metrics.go` 的 `Collector` 结构体中添加指标字段；
2. 在 `NewCollector` 中构造指标，注入 `module` 常量标签，并注册到模块专属的 `Registry`：
   ```go
   c.myCounter = prometheus.NewCounterVec(
       prometheus.CounterOpts{
           Name:        "privshield_custom_events_total",
           Help:        "Total number of custom events",
           ConstLabels: prometheus.Labels{"module": module},
       },
       []string{"event_type", "status"}, // 标签值必须为低基数枚举！
   )
   c.registry.MustRegister(c.myCounter)
   ```
3. 严禁将用户 ID、动态 URL、随机 Task ID 作为 Prometheus Label；
4. `Collector` 已实现 `naming.Observer` 接口，服务只需调用 `naming.SetObserver(mc)` 即可自动获得别名解析与归一化失败指标统计。

### SOP-4：如何新增一个安全门禁检查 (ValidateFailClosed 模式)

当你的服务需要在启动时强制某些安全配置不可缺失（如 API Key、TLS、加密密钥），使用 `pkg/config.ValidateFailClosed` 统一门禁：

1. 在 `pkg/config/security.go` 中确认 `SecurityRequirements` 已覆盖你的检查需求；如需新的检查维度，新增对应的 `Err*` sentinel 与字段：
   ```go
   var ErrMyNewRequirement = errors.New("my security check failed")

   type SecurityRequirements struct {
       // ... 已有字段
       MyNewKey     string
       RequireMyNew bool
   }
   ```
2. 在 `ValidateFailClosed` 函数体中添加判定规则：
   ```go
   if req.RequireMyNew && req.MyNewKey == "" && remoteExposed {
       return fmt.Errorf("%s: %w (set MY_SERVICE_KEY)", name, ErrMyNewRequirement)
   }
   ```
3. 在服务的 `Config.Validate()` 中调用门禁：
   ```go
   if err := pkgconfig.ValidateFailClosed(pkgconfig.SecurityRequirements{
       ServiceName:  "my-service",
       Hosts:        []string{c.Host},
       APIKey:       c.APIKey,
       RequireMyNew: true,
       MyNewKey:     c.MyKey,
   }); err != nil {
       return err
   }
   ```

**核心原则：** 非环回监听 (0.0.0.0 / 网卡 IP) 时安全开关缺失 → 启动失败；纯 127.0.0.1 本地开发形态允许无密钥启动。

### SOP-5：如何新增一个命名注册表条目 (naming.Registry)

`pkg/naming` 是 PrivShield 跨服务业务标识的唯一事实源 (SSOT)。新增数据源条目需严格遵循以下流程：

1. 在 `pkg/naming/naming.go` 中定义 canonical 常量：
   ```go
   // 数据源实体唯一标识（必须符合 ^ds_[a-z][a-z0-9_]{1,30}$）
   const DSNewSource = "ds_new_source"

   // 绑定的业务 API 契约编码（必须符合 ^api[1-9]_[a-z][a-z0-9_]{1,30}$）
   const APINewSource = "api3_new_source"
   ```
2. 在 `Registry` 切片中添加 `Entry`：
   ```go
   {
       APICode:      APINewSource,
       DataSourceID: DSNewSource,
       Seq:          5, // 展示排序序号
       DisplayName:  map[string]string{"zh-CN": "新数据源接口", "en-US": "New Source API"},
       Category:     "medical", // "medical" | "healthcare" | "reserved"
       FileName:     "new_source.csv",
       FieldCount:   20,
       Aliases:      []string{"new_source", "new_source.csv", "新数据"},
       Status:       StatusActive, // StatusActive | StatusReserved
   }
   ```
3. **别名冲突自检**：`init()` 会自动检测跨条目别名冲突，CI 单测中应断言 `AliasConflicts()` 返回空切片；
4. 全仓库业务代码中严禁出现裸数据源字符串字面量（如 `"new_source"`），必须统一引用 `naming.DSNewSource` 常量或通过 `naming.Normalize(raw)` 动态查询；
5. 预留条目应设置 `Status: StatusReserved`，写侧调度命中预留位时 `CheckWritable` 返回 `ErrReservedDataSource`（HTTP 409）。

---

## 四、典型反模式与避坑指南 (Anti-Patterns)

### ❌ 反模式 1：绕过 `BufferedAuditStore` 直接执行并发 SQL 插入
* **后果**：多协程直接写库会并发争抢前序哈希，导致生成的防篡改哈希链分叉或断裂，`VerifyChain` 报错失败。
* **正解**：所有写日志操作必须通过 `BufferedAuditStore.SaveLog`，由单 Worker 协程统一出队绑定 `PrevHash`。

### ❌ 反模式 2：使用本地时区格式化时间戳生成存证哈希
* **后果**：当服务运行在 CST（+0800）而核验节点运行在 UTC 时，时间字符串格式不一致导致哈希验真失败。
* **正解**：存证前像中的时间戳必须无条件调用 `timestamp.UTC().Format(time.RFC3339Nano)`。

### ❌ 反模式 3：忽略 `LeasedTaskStore` 条件写回的 `bool` 返回值
* **后果**：当节点租约因网络延迟超时被其他节点接管后，若忽略 `CompleteLease` 返回的 `false`，会导致两个节点同时以为自己执行成功，造成下游业务脏数据。
* **正解**：必须检查返回值：`if ok, err := store.CompleteLease(...); !ok || err != nil { /* 放弃本次执行 */ }`。

### ❌ 反模式 4：在 Prometheus 指标中使用高基数动态参数
* **后果**：将 `/api/audit/logs/log-12345` 作为 path 标签，导致 Prometheus 产生百万级时间序列，最终 OOM 崩溃。
* **正解**：使用 `pkg/metrics.Collector` 的中间件，自动抓取 Gin 路由模板 `/api/audit/logs/:id` 作为统一标签。

### ❌ 反模式 5：跳过归档直接删除存证记录（违反留存红线）
* **后果**：存证记录被物理删除后无法恢复，违反数安法留存要求与 P0-8 存证留存红线，构成不可逆的数据损毁事件。
* **正解**：必须通过 `archive.Archiver.ArchiveAndCleanup` 执行完整的「归档 → 回读验真 → 删除」链路。该函数在归档段写入失败或验真失败时自动拒绝删除（fail-closed）。`AUDIT_LOG_RETENTION_DAYS > 0` 时启动期 `Config.Validate()` 还会强制校验归档目录与加密密钥已配置。

### ❌ 反模式 6：未使用 `ValidateFailClosed` 而在代码中手写安全开关检查
* **后果**：手写检查容易遗漏「非环回监听但密钥为空」的场景，导致服务在无 API Key、无加密密钥的状态下对外暴露，形成可观测性盲区。
* **正解**：所有安全门禁统一通过 `config.ValidateFailClosed(SecurityRequirements{...})` 执行，它在启动阶段一次性校验 API Key / TLS / mTLS 白名单 / 加密密钥 / 哈希链密钥，任何一项缺失且存在非环回监听面时直接终止进程。

### ❌ 反模式 7：新写入使用 `enc:v1:` 格式
* **后果**：v1 格式使用 `SHA-256(secret)[:16]` 弱派生（无 salt、无 info 绑定），同一口令在所有记录上产出相同密钥，无法抵抗离线暴破与跨用途密钥复用攻击。
* **正解**：新写入一律调用 `crypto.EncryptString`，它自动输出 `enc:v2:` 格式（HKDF-SM3 派生，16 字节随机 salt + info 绑定 + 前缀参与 AAD）。`enc:v1:` 仅保留解密能力，用于读取历史存量数据。空密钥时 `EncryptString` 返回 `ErrEmptyKey`，不再静默降级为明文。
