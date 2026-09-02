# PrivShield 共享基础包 (Shared PKG) — 详细系统设计说明书

> **文档定位**：数联天下 · 数盾（`PrivShield`）全栈公共基础设施库（`pkg`）的核心架构、子系统设计、密码学实现与并发存储原语详细说明书。  
> **适用模块**：`service-hub`、`datasource-mgr`、`audit-log`、`console/bff-go`、`engine-go`、`privacy-go-sdk`。  
> **设计基准**：GB/T 39786-2021 密评三级、GM/T 0004-2012 (SM3)、GM/T 0002-2012 (SM4)、RFC 5869 (HKDF)、RFC 7515/7516、Go 1.25+ 现代微服务架构规范。

---

## 1. 架构定位与设计哲学

在 `PrivShield` 分布式微服务架构中，`pkg` 模块作为所有 Go 子系统共享的底层基座，承载了**「密码安全、持久存储与归档留存、中间件防护、上游通信与容灾、全链路可观测、规范化治理与安全门禁」**六大核心能力。

```text
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                              PrivShield 业务与微服务层                                    │
│  service-hub (:8082)   │  datasource-mgr (:8083)  │  audit-log (:8084)  │  bff-go        │
└──────────┬──────────────────────────┬──────────────────────────┬────────────────────┬────┘
           │                          │                          │                    │
┌──────────▼──────────────────────────▼──────────────────────────▼────────────────────▼────┐
│                            PrivShield 共享基础设施包 (pkg)                                │
├─────────────────┬──────────────────────────────────┬──────────────────────────────────────┤
│ 1. 密码学与安全  │ crypto (SM3/SM4-GCM/信封加密v1+v2)│ tlsutil (mTLS / CN 白名单热加载)     │
│ 2. 存储与归档    │ store (抽象接口 / PG / SQLite)    │ flusher (微批串行哈希链 + 归档留存)  │
│ 3. 中间件与防护  │ middleware (8层防护栈 / 读写分离)  │ config (安全门禁 ValidateFailClosed) │
│ 4. 运行时与观测  │ agent (多端点独立熔断)            │ metrics / observability / naming     │
└─────────────────┴──────────────────────────────────┴──────────────────────────────────────┘
```

### 1.1 设计原则

1. **零第三方 CGO 依赖 (Pure Go)**：全部密码学（SM3/SM4）、嵌入式存储（SQLite `modernc.org/sqlite`）均采用纯 Go 编译，杜绝 CGO 交叉编译困难与动态链接库安全漏洞。
2. **规范化密码合规 (SM3/SM4-GCM)**：全链路遵循国密商用密码标准，哈希前像采用 UTC 纳秒级规范化格式，快照存储强制执行 SM4-GCM 双版本信封加密（v2 当前写入路径使用 HKDF-SM3 密钥派生）。
3. **单 Worker 顺序存证保证**：通过 `BufferedAuditStore` 实现高并发写入入队、单 Worker 串行哈希链绑定与微批合并刷盘，兼顾 3,000+ QPS 吞吐与 100% 连续防篡改哈希链。
4. **零信任安全门禁 (Zero-Trust Gate)**：非环回地址暴露时，启动阶段即强制校验 API Key、TLS、加密密钥、哈希链密钥四项安全配置缺一不可，Fail-Closed 杜绝配置遗漏导致的裸奔暴露。
5. **两阶段平滑降级与自适应弹性**：数据库优先连接高并发分布式 PostgreSQL（Phase B），若探针超时（3s）或未配置则平滑回退至单机 SQLite WAL，并根据 CPU 核心数自适应调整连接池上下限。

---

## 2. 模块拓扑与目录划分

```text
pkg/
├── agent/                  # 上游 PrivShield Agent REST API 多端点客户端封装
│   ├── client.go           # Client（多端点独立熔断器、原子轮询、重试、鉴权头注入、64MiB 响应保护）
│   └── client_test.go      # 基础请求、鉴权、熔断器状态流转、端点选择单测
├── auth/                   # Scope-based 身份认证与权限映射基础设施
│   ├── identity.go         # Identity 结构、PermissionForRESTPath（/v1/* + /api/v1/* 归一化）、ServiceHubPermissionForPath、ParseAPIKeysEnv
│   ├── middleware.go        # AuthMiddleware（Gin）、RequirePermission、ConstantTimeLookup
│   └── settings.go         # KeyConfig / Settings 认证配置
├── circuitbreaker/         # 三态熔断器内核（Closed → Open → HalfOpen）
│   └── circuitbreaker.go   # Breaker 独立实现，供 agent 多端点复用
├── config/                 # 集中化环境变量解析、结构化日志与安全门禁
│   ├── env.go              # EnvString, EnvInt, EnvBool, EnvDuration
│   └── security.go         # SecurityRequirements + ValidateFailClosed 启动安全门禁
├── crypto/                 # 国密商用密码与双版本信封加密体系
│   ├── sm3.go              # GM/T 0004-2012 SM3 算法、HMAC-SM3、UTC 规范化哈希
│   ├── sm4.go              # GM/T 0002-2012 SM4 分组密码、CBC/CTR/GCM 模式
│   ├── envelope.go         # SM4-GCM 双版本信封加密 (enc:v1: + enc:v2:) 与 HKDF-SM3 密钥派生
│   └── envelope_test.go    # 加密解密、IV 随机性、防篡改校验单测
├── docs/                   # pkg 体系工程技术文档中心
├── gateway/                # 网关层共享基础设施
│   └── balancer.go         # P2C-EWMA 负载均衡器
├── grpcserver/             # gRPC 服务端统一启动与拦截器
│   └── server.go           # gRPC Server 构建与 mTLS 集成
├── metrics/                # 模块级隔离 Prometheus 指标采集器
│   ├── metrics.go          # Collector（15 项指标、路由标签归一化、实现 naming.Observer）
│   └── metrics_test.go     # 指标收集与 HTTP 中间件测试
├── middleware/             # Gin 生产级 8 层纵深防御中间件
│   ├── auth.go             # Auth + AuthWithRoles（读写密钥分离、/metrics 纳入鉴权 P1-6）
│   ├── envelope.go         # 跨语言统一 API 信封格式与全局异常拦截
│   ├── middleware.go        # CORS, RequestID, Recovery, SecurityHeaders
│   ├── ratelimit.go        # MaxBodySize, MaxConcurrent, 32 分片令牌桶限流
│   └── trace.go            # TraceMiddleware 兼容垫片（实现下沉至 pkg/observability）
├── naming/                 # 跨服务业务标识 SSOT 与安全分级词表
│   ├── naming.go           # Registry（4 条目）、别名归一化、别名冲突检测
│   ├── levels.go           # L1~L5 安全分级词表（双表达映射：L3 ↔ confidential）
│   ├── observer.go         # Observer 接口（指标钩子）+ 低基数标签常量
│   └── *_test.go           # 注册表、分级词表与观测器单测
├── observability/          # 全链路可观测性基础设施（从 middleware 下沉）
│   ├── logger.go           # NewLogger / InitLogger（slog JSON/Text 双格式）
│   ├── request_logger.go   # RequestLogger / RequestLoggerWithModule（替代原 StructuredLogger）
│   ├── trace.go            # TraceMiddleware, GenerateRequestID, ContextWithRequestID
│   ├── tracing.go          # Tracer 接口 + OTel 集成 + StartSpan
│   └── metrics.go          # REDMetrics（独立 Registry + privshield_ 前缀）
├── profile/                # 隐私策略 Profile 动态加载器
│   └── resolver.go         # 领域规则与参数解析
├── store/                  # 数据持久化抽象层与双引擎实现
│   ├── store.go            # 核心领域实体、AuditStore 接口、AuditArchiveReader 归档接口
│   ├── audit_hash.go       # 三代哈希链存证（SHA256→SM3→SM3-HMAC:v1）+ 5 候选验真
│   ├── levels.go           # 存储层安全分级辅助
│   ├── flusher/            # 高并发微批缓冲刷盘器 + 归档留存
│   │   ├── flusher.go      # BufferedAuditStore（8 字段 Config、归档、重试、读己之写）
│   │   └── flusher_test.go # 50+ 并发哈希连续性、Close 零丢数据、归档流程测试
│   ├── sqlite/             # SQLite 纯 Go 引擎 (WAL 模式, busy_timeout=5000)
│   │   ├── init.go         # 驱动初始化、PRAGMA 调优与自动 Schema 迁移
│   │   ├── tasks.go        # 任务存储实现
│   │   ├── datasources.go  # 数据源存储实现
│   │   ├── audit.go        # 审计日志与快照存储，SQL 级聚合统计
│   │   └── leased.go       # SQLite 原子租约实现
│   ├── postgres/           # PostgreSQL Phase B 分布式原子租约与审计存储
│   │   ├── postgres.go     # pgxpool 连接池自适应调优与 Schema 初始化
│   │   ├── schema.go       # DDL 定义
│   │   ├── leased.go       # FOR UPDATE SKIP LOCKED 多副本原子任务争抢与租约续期
│   │   ├── audit.go        # PostgreSQL 原生分区审计日志存储与 SQL 报告聚合
│   │   └── tasks.go        # 任务 CRUD
│   ├── memory/             # 纯内存存储实现（单元测试与本地无持久化模式）
│   │   └── memory.go       # 基于 sync.RWMutex 的内存 Mock
│   └── cmd/                # 数据库自动化迁移与修复 CLI 工具
│       ├── migrate/        # Schema 迁移
│       └── repairchain/    # 哈希链修复
├── tlsutil/                # mTLS 双向认证与动态 CN 白名单管理
│   ├── tlsutil.go          # TLS 1.3 强制、CA 证书链加载
│   ├── whitelist.go        # 基于 YAML 的 CN 白名单管理器（文件 mtime 热重载）
│   └── grpc_interceptor.go # gRPC mTLS 客户端 CN 提取拦截器
└── validation/             # 输入安全校验与参数清洗
    ├── validation.go       # 白名单枚举、端口范围、抗碰撞唯一 ID 生成、安全分页
    └── validation_test.go  # 校验用例单测
```

---

## 3. 密码学与安全体系设计 (`pkg/crypto` & `pkg/tlsutil`)

### 3.1 信封加密双版本设计 (`pkg/crypto/envelope.go`)

为保证敏感数据快照（`SnapshotRecord.InputSample` / `OutputSample`）在落库及跨域传输中的机密性，系统实现了基于 **SM4-GCM** 的动态信封加密机制，并演进至双版本并存设计：

```text
                        ┌───────────────────────────────────────────────┐
                        │             EncryptString (写入路径)          │
                        │  始终使用 v2 格式，HKDF-SM3 密钥派生           │
                        └───────────────────┬───────────────────────────┘
                                            │
   ┌────────────────────────────────────────┼────────────────────────────────────────┐
   │                                                                                │
   ▼  v2 格式（当前生产写入路径）                                     ▼  v1 格式（仅解密兼容）
   ┌────────────────────────────────────────────────┐               ┌───────────────────────────────────────┐
   │ enc:v2:<Base64( 16B salt │ 12B nonce │ CT │ Tag )>            │ enc:v1:<Base64( 12B nonce │ CT │ Tag )>│
   │                                                │               │                                       │
   │ 密钥派生: DeriveKeyHKDF(secret, salt)          │               │ 密钥派生: DeriveKey(secret)            │
   │   → RFC 5869 HKDF (Extract-then-Expand)        │               │   → SHA-256(secret)[:16]               │
   │   → HMAC-SM3 底层哈希                           │               │   → 无 AAD                              │
   │ AAD: "enc:v2:" 前缀字节                         │               │                                       │
   └────────────────────────────────────────────────┘               └───────────────────────────────────────┘
                                            │
                        ┌───────────────────▼───────────────────────────┐
                        │             DecryptString (读取路径)          │
                        │  自动检测前缀：enc:v2: → v2 解密              │
                        │                   enc:v1: → v1 解密           │
                        │                   无前缀    → ErrUnencryptedValue│
                        └───────────────────────────────────────────────┘
```

#### 3.1.1 v2 格式核心设计

**密钥派生函数 `DeriveKeyHKDF`**：

```go
func DeriveKeyHKDF(secret string, salt []byte) []byte
```

遵循 RFC 5869 HKDF 规范，使用 SM3 作为底层 HMAC 哈希函数：
1. **Extract 阶段**：`PRK = HMAC-SM3(salt, secret)` — 将不定长输入密钥材料萃取为固定长度伪随机密钥；
2. **Expand 阶段**：迭代 `HMAC-SM3(PRK, prev || info || counter)` 并截断至 `KeySize`（16 字节）；
3. **Info 绑定字符串**：`"PrivShield audit snapshot SM4-GCM v2"` — 将派生密钥绑定到单一用途，防止跨上下文密钥复用。

**每条记录独立盐值**：`EncryptString` 为每次加密操作生成 16 字节随机盐值（`crypto/rand.Read`），确保即使相同明文与主密钥，密文也完全不同，彻底消除模式泄漏风险。

**AAD 绑定**：GCM Seal 时将 `"enc:v2:"` 前缀字节作为附加认证数据（Additional Authenticated Data），防止攻击者将 v2 密文降级伪装为 v1 格式。

#### 3.1.2 哨兵错误

| 哨兵错误 | 触发条件 |
|---|---|
| `ErrEmptyKey` | 主密钥未配置（空字符串），加密/解密均拒绝执行 |
| `ErrUnencryptedValue` | 解密时值不带 `enc:v1:` 或 `enc:v2:` 前缀，拒绝静默返回明文 |

#### 3.1.3 透明兼容策略

- `EncryptString(plaintext, secret)` — **始终写入 v2 格式**，绝不产生新的 v1 密文；
- `DecryptString(ciphertext, secret)` — 自动检测前缀并路由到对应解密路径，v1/v2 均支持；
- `IsEncrypted(value)` — 检测值是否携带 `enc:v1:` 或 `enc:v2:` 前缀；
- 空字符串输入返回 `("", nil)` — 兼容空字段无需加密的场景；
- `secret == ""` 时加密返回 `("", ErrEmptyKey)` — **绝不静默降级为明文存储**。

### 3.2 哈希链三代演进 (`pkg/crypto/sm3.go` & `pkg/store/audit_hash.go`)

存证哈希链经历了三代算法演进，系统在 `VerifyAuditIntegrityHash` 中实现了 5 候选兼容验真，确保历史存量数据在任何迁移阶段均可正确验证。

#### 3.2.1 三代演进时间线

```text
第一代 (Legacy)        第二代 (过渡期)              第三代 (当前生产写入路径)
┌──────────────┐      ┌──────────────────┐       ┌─────────────────────────┐
│   SHA-256     │  →   │   SM3 (无密钥)    │  →    │  SM3-HMAC:v1 (密钥化)    │
│ 本地时区时间戳 │      │ UTC 规范化时间戳   │       │ UTC 规范化时间戳          │
│              │      │                  │       │ atomic.Pointer 管理密钥   │
└──────────────┘      └──────────────────┘       └─────────────────────────┘
```

#### 3.2.2 9 要素前像规范化格式 (Canonical Pre-image)

为杜绝时区差异、浮点表示不一致或字段缺失导致的验签失败，前像字符串强制遵循以下规范：

$$\text{Data} = \text{PrevHash} \mid \text{LogID} \mid \text{Timestamp(UTC Nano)} \mid \text{Algorithm} \mid \text{InputHash} \mid \text{OutputHash} \mid \text{User} \mid \text{SecurityLevel} \mid \text{ParamsJSON}$$

* **时区归一化**：`timestamp.UTC().Format(time.RFC3339Nano)`；
* **参数确定性**：空参数或无效 JSON 归一化为 `"{}"`；
* **密钥化哈希**（第三代）：`HMAC-SM3(key, "SM3-HMAC:v1|" + Data)`；
* **无密钥哈希**（第二代）：`Hex(SM3(Data))`。

#### 3.2.3 密钥管理与算法自动检测

```go
// SetAuditChainKey 通过 atomic.Pointer[string] 原子地设置 HMAC 密钥。
// 空字符串 → 退化为无密钥 SM3 模式（第二代）。
func SetAuditChainKey(key string)

// ComputeAuditIntegrityHashAlgo 返回当前写入路径使用的算法标签。
// 有密钥 → "SM3-HMAC:v1"；无密钥 → "SM3"
func ComputeAuditIntegrityHashAlgo() string
```

`chainKey` 使用 `atomic.Pointer[string]` 实现无锁并发安全读取，进程启动时通过 `SetAuditChainKey(key)` 一次性注入密钥，运行时零竞争开销。

#### 3.2.4 5 候选降级验真 (`VerifyAuditIntegrityHash`)

```go
func VerifyAuditIntegrityHash(stored, logID, prevHash string, timestamp time.Time,
    algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) (bool, string)
```

按优先级构建最多 5 个候选摘要，逐一比较：

| 优先级 | 算法 | 时间区 | 标签 | 条件 |
|:---:|---|---|---|---|
| 1 | HMAC-SM3 (密钥化, UTC) | UTC | `"SM3-HMAC:v1"` | 仅当链密钥已配置 |
| 2 | SM3 (无密钥, UTC) | UTC | `"SM3"` | 始终尝试 |
| 3 | SHA-256 (UTC) | UTC | `"SHA256-LEGACY"` | 始终尝试 |
| 4 | SM3 (无密钥, 本地时区) | Local | `"SM3-LEGACY"` | 仅当时区偏移产生差异 |
| 5 | SHA-256 (本地时区) | Local | `"SHA256-LEGACY"` | 仅当时区偏移产生差异 |

**关键安全保证**：所有候选比较均使用 `hmac.Equal` 常量时间函数，杜绝时序侧信道攻击。返回值为 `(matched bool, label string)`，`label` 标识命中的算法版本，便于上层标记需要重签的遗留记录（`LegacyHashSuffix = "-LEGACY"`）。

快照验真函数 `VerifySnapshotIntegrityHash` 采用完全对称的 5 候选策略，标签后缀加 `"-SNAPSHOT"`。

---

## 4. 归档留存设计 (`pkg/store` + `pkg/store/flusher`)

### 4.1 AuditArchiveReader 接口 (`pkg/store/store.go`)

为满足数据保留策略（Retention Policy）的合规要求，系统定义了可选能力接口 `AuditArchiveReader`，实现「归档后删除」(Archive-Before-Delete) 的安全留存模式：

```go
type AuditArchiveReader interface {
    // FetchOldestForArchive 按链序（旧→新）返回最早到期的审计日志及关联快照。
    // before: 严格早于此时间戳的记录；limit: 单次分页容量。
    FetchOldestForArchive(before time.Time, limit int) ([]AuditLog, []SnapshotRecord, error)

    // DeleteLogsByIDs 按 ID 精确删除审计日志并级联删除关联快照。
    DeleteLogsByIDs(ids []string) (int64, error)
}
```

### 4.2 归档留存流程

```text
                    ┌──────────────────────────────────────────────────┐
                    │            留存守卫 (Retention Guard)             │
                    └──────────────┬───────────────────────────────────┘
                                   │
                    ┌──────────────▼───────────────────────────────────┐
                    │  ① Flush() — 排空缓冲区，确保无未落盘数据         │
                    │     (fail-closed: 刷盘失败则中止整个归档流程)      │
                    └──────────────┬───────────────────────────────────┘
                                   │
                    ┌──────────────▼───────────────────────────────────┐
                    │  ② FetchOldestForArchive(before, pageSize)       │
                    │     底层存储必须实现 AuditArchiveReader 接口       │
                    └──────────────┬───────────────────────────────────┘
                                   │
                    ┌──────────────▼───────────────────────────────────┐
                    │  ③ 写入 NDJSON 归档段                             │
                    │     SM4-GCM 加密 + gzip 压缩                     │
                    └──────────────┬───────────────────────────────────┘
                                   │
                    ┌──────────────▼───────────────────────────────────┐
                    │  ④ 校验归档完整性                                 │
                    │     (读取验证 + 哈希校验)                         │
                    └──────────────┬───────────────────────────────────┘
                                   │
                    ┌──────────────▼───────────────────────────────────┐
                    │  ⑤ DeleteLogsByIDs(ids)                          │
                    │     同时清理 recentLogs 内存暂存映射               │
                    └──────────────────────────────────────────────────┘
```

### 4.3 关键常量与配置

| 常量 | 值 | 说明 |
|---|---|---|
| `DefaultArchivePageSize` | `500` | 归档分页拉取的默认页大小 |
| `ArchiveIDChunkSize` | `500` | 批量删除时的 ID 分片大小，避免单条 SQL 过大 |

### 4.4 STRICT_STORAGE 容错策略

`STRICT_STORAGE` 是服务级配置（默认 `true`），通过环境变量 `STRICT_STORAGE` 或服务专属前缀变量（如 `AUDIT_LOG_STRICT_STORAGE`、`SERVICE_HUB_STRICT_STORAGE`、`DATASOURCE_MGR_STRICT_STORAGE`）控制：

- **`STRICT_STORAGE=true`（默认）**：归档失败（如底层存储不支持 `AuditArchiveReader`、归档写入失败、校验不通过）**阻断删除操作**，保证数据零丢失；
- **`STRICT_STORAGE=false`**：归档失败仅记录告警日志，允许继续执行删除（仅用于开发/测试环境）。

**设计哲学**：Fail-Closed 默认值确保生产环境绝不会因归档故障而意外删除未备份的审计数据。

---

## 5. Per-Endpoint 熔断器设计 (`pkg/agent`)

### 5.1 多端点架构

`pkg/agent.Client` 为各 Go 微服务与上游 PrivShield Agent 隐私计算引擎之间提供强韧的高并发通信保障。核心突破在于支持**多端点独立熔断**：

```text
                    ┌─────────────────────────────────────┐
                    │         agent.Client                │
                    │                                     │
                    │  baseURLs: [url1, url2, url3]       │
                    │  rrIndex:  atomic.Uint64 (轮询计数器) │
                    │                                     │
                    │  breakers: map[string]*Breaker       │
                    │    ├── "url1" → Closed (正常)        │
                    │    ├── "url2" → Open (熔断中)        │
                    │    └── "url3" → Closed (正常)        │
                    └─────────────────────────────────────┘
```

每个端点维护独立的三态熔断器（`Closed` → `Open` → `HalfOpen`），单个节点故障不影响其他节点的正常服务。

### 5.2 端点选择与熔断流转

```go
// PickEndpoint 原子轮询选择端点，跳过熔断节点。
func (c *Client) PickEndpoint() string

// EndpointStates 返回每个端点的当前熔断器状态快照。
func (c *Client) EndpointStates() map[string]string
```

**`PickEndpoint()` 执行逻辑**：
1. 原子递增 `rrIndex`（`atomic.Uint64.Add(1)`），实现无锁轮询；
2. 依次检查候选端点的熔断器状态：
   - `Closed` → 放行，返回该端点；
   - `Open` → 跳过，尝试下一个；
   - `HalfOpen` → 放行单个探测请求；
3. 所有端点均不可用时返回 `ErrEndpointUnavailable`；
4. 触发熔断的端点返回 `ErrCircuitOpen`。

### 5.3 三态熔断器 (`pkg/circuitbreaker`)

```text
         连续失败 ≥ threshold (默认 5)
    ┌──────────────────────────────────────┐
    │                                      ▼
┌────────┐                           ┌──────────┐
│ Closed │                           │   Open   │
│ (正常)  │                           │ (熔断中)  │
└────┬───┘                           └─────┬────┘
     │                                     │
     │         冷却时间到期 (默认 30s)       │
     │         ┌───────────────────────────┘
     │         ▼
     │    ┌───────────┐
     │    │ Half-Open │
     │    │ (半开探测) │
     │    └─────┬─────┘
     │          │
     │    探测成功 │          探测失败
     │          │               │
     └──────────┘               └──→ 回到 Open
```

### 5.4 三个哨兵错误

| 哨兵错误 | 语义 |
|---|---|
| `ErrEndpointUnavailable` | 所有端点均处于熔断状态，无可用节点 |
| `ErrCircuitOpen` | 目标端点熔断器处于 Open 状态，请求被快速拒绝 |
| `ErrTransport` | 底层网络传输失败（超时、连接拒绝、连接重置等） |

`transportError` 类型实现了 `Is(target error) bool` 方法，支持 `errors.Is(err, ErrTransport)` 语义匹配，同时通过 `Unwrap()` 暴露根因，兼容 `errors.Is(err, context.DeadlineExceeded)` 等上下文错误检测。

### 5.5 指数退避重试与响应保护

- **指数退避**：对网络瞬断或 `502/503/504` 状态码执行最多 3 次指数退避重试（`500ms → 1000ms → 2000ms`，含随机抖动）；
- **端点故障转移**：重试时自动尝试下一个可用端点（`pickEndpoint(exclude)`），避免对故障节点反复重试；
- **响应体防爆**：采用 `io.LimitReader` 限制单次读取上限为 64 MiB，防止异常超大响应引发 OOM 崩溃；
- **重试决策**：`ErrEndpointUnavailable` / `ErrCircuitOpen` → 不重试（快速失败）；`transportError` → 重试；`4xx` → 不重试（客户端错误）。

---

## 6. 安全门禁设计 (`pkg/config/security.go`)

### 6.1 零信任启动校验

`ValidateFailClosed` 在服务启动阶段强制执行安全配置校验，确保非环回地址暴露时不会因配置遗漏导致裸奔：

```go
type SecurityRequirements struct {
    ServiceName          string   // 服务名称（用于日志标识）
    Hosts                []string // 监听地址列表
    APIKey               string   // Bearer 认证密钥
    TLSEnabled           bool     // TLS 是否已启用
    RequireTLS           bool     // 是否强制要求 TLS
    GRPCEnabled          bool     // 是否启用 gRPC
    MTLSWhitelistFile    string   // mTLS CN 白名单文件路径
    EncryptionKey        string   // 快照加密主密钥
    RequireEncryptionKey bool     // 是否强制要求加密密钥
    HashKey              string   // 存证哈希链密钥
    RequireHashKey       bool     // 是否强制要求哈希链密钥
}

func ValidateFailClosed(req SecurityRequirements) error
```

### 6.2 五项校验规则 (按优先级依次评估，首个失败即返回)

| 序号 | 条件 | 哨兵错误 |
|:---:|---|---|
| 1 | 任一非环回 Host + `APIKey` 为空 | `ErrAPIKeyRequired` |
| 2 | `RequireTLS=true` 且 `TLSEnabled=false` | `ErrTLSRequired` |
| 3 | TLS+gRPC 均已启用但 `MTLSWhitelistFile` 为空 | `ErrMTLSWhitelistRequired` |
| 4 | `RequireEncryptionKey=true` + 非环回 + `EncryptionKey` 为空 | `ErrEncryptionKeyRequired` |
| 5 | `RequireHashKey=true` + 非环回 + `HashKey` 为空 | `ErrChainKeyRequired` |

### 6.3 环回地址检测 (`IsLoopbackHost`)

```go
func IsLoopbackHost(host string) bool
```

检测逻辑：
- 空字符串 → `true`（未指定地址，视为本地开发）；
- `"localhost"` → `true`；
- 支持 `"host:port"` 格式（通过 `net.SplitHostPort` 分离）；
- 有效 IP → 委托 `ip.IsLoopback()` 判断（覆盖 `127.0.0.0/8` 和 `::1`）；
- `"0.0.0.0"` / `"::"` / `"*"` → `false`（监听所有接口，视为非环回暴露）；
- 无法解析的主机名 → `false`（Fail-Closed，拒绝假定为安全）。

---

## 7. naming/SSOT 设计 (`pkg/naming`)

### 7.1 唯一事实源 (Single Source of Truth)

`pkg/naming` 是 PrivShield 全栈跨服务业务标识的唯一事实源与唯一收口卡点 (Choke Point)。所有入站流量在服务边界（REST/gRPC/BFF 入口）做别名归一化与合法性校验时，均必须经由此包。

**核心设计约束**：
1. **唯一标识原则**：一个数据源实体有且仅有一个 canonical `datasource_id`，所有外部表现形式（slug、文件名、中文名）仅在系统边界归一化一次；
2. **Fail-Closed 零逃逸原则**：未登记的未知入站值必须直接报错拒绝（HTTP 400），严禁静默回退到任何默认数据源；
3. **代码防腐原则**：全仓库严禁裸数据源字符串字面量，必须引用 `naming.DSYibao` 等导出常量。

### 7.2 Registry 注册表 (4 条目)

```go
var Registry = []Entry{
    // 1. 医保结算数据接口 (active, 19 字段, 9 别名)
    {APICode: "api1_yibao", DataSourceID: "ds_yibao", Status: "active", ...},
    // 2. 康养健康档案接口 (active, 27 字段, 9 别名)
    {APICode: "api2_kangyang", DataSourceID: "ds_kangyang", Status: "active", ...},
    // 3. 预留政务数据源 3 (reserved, 5 别名)
    {DataSourceID: "ds_mock3", Status: "reserved", ...},
    // 4. 预留企业/金融数据源 4 (reserved, 6 别名)
    {DataSourceID: "ds_mock4", Status: "reserved", ...},
}
```

包 `init()` 时一次性构建三张 O(1) 查找索引：`byDataSourceID`、`byAPICode`、`aliasIndex`。

### 7.3 别名冲突检测

```go
func AliasConflicts() []string
```

在 `init()` 构建别名索引时，若不同数据源条目意外声明了相同别名，冲突项被记录为 `"别名"→数据源A|数据源B` 格式。**架构保障**：正常情况下切片长度必须恒等于 0，CI 单测严格断言此函数，防止注册表被污染。

### 7.4 Observer 可观测性接口

```go
type Observer interface {
    RecordAPIAlias(alias, canonical, target string)    // 别名解析上报
    RecordNormalizeError(reason string)                 // 归一化失败上报
}
```

`*metrics.Collector` 编译期断言实现此接口（`var _ naming.Observer = (*Collector)(nil)`），服务启动时通过 `naming.SetObserver(mc)` 一次性注册，业务代码无需关心指标埋点。

**标签基数控制**：`target` 仅取 `"datasource_id"` / `"api_code"` / `"path"` 三个枚举值；`reason` 仅取 `"unknown"` / `"empty"` / `"reserved"` / `"format_invalid"` 四个枚举值。入站原始脏数据绝不进入标签，仅输出到日志。

### 7.5 安全分级词表 (`pkg/naming/levels.go`)

`NormalizeSecurityLevelID` 解决了仓库内两套等级表达的历史分裂问题：

| L1~L5 标识 | Engine Canonical 名称 | 中文名称 | 敏感度排名 |
|---|---|---|:---:|
| `L1` | `public` | 公开数据 | 1 |
| `L2` | `internal` | 内部数据 | 2 |
| `L3` | `confidential` | 敏感数据 | 3 |
| `L4` | `secret` | 高敏感数据 | 4 |
| `L5` | `top_secret` | 极敏感数据 | 5 |

```go
// NormalizeSecurityLevelID 将任意合法拼写归一为 L1~L5 标识。
// "confidential" / "l3" / " L3 " → "L3"；未知输入 → ""（零静默兜底）。
func NormalizeSecurityLevelID(level string) string
```

匹配逻辑：`strings.ToLower(strings.TrimSpace(input))` 后依次比对 `id` 和 `name`，大小写不敏感。

---

## 8. 分类分级标准加载

### 8.1 LoadStandardsFromDir (`engine-go/internal/dynclassification/standards.go`)

```go
func LoadStandardsFromDir(dir string) ([]StandardDef, []error)
```

从指定目录读取所有 `.yaml`/`.yml` 分类分级标准文件：
1. 逐文件反序列化为 `StandardDef` 结构体；
2. 文件名作为 `StandardID`（若文件内未显式指定）；
3. 按 `StandardID` 排序后返回标准切片与逐文件错误切片；
4. **容错设计**：解析失败的文件仅记录错误并跳过，不阻断其他标准文件的加载与服务启动。

### 8.2 highestStandardDefaultLevel 兜底机制

```go
func (f *ClassificationFunnel) highestStandardDefaultLevel() string
```

在分类漏斗（三层架构：AC 规则引擎 → Small-NER → LLM）中作为 P1-3 安全底线的兜底逻辑：
1. 遍历所有已加载标准，提取每个 `GlobalParams.DefaultLevel`；
2. 通过 `LevelFromString` / `LevelRank` 转换敏感度排名；
3. 返回排名最高的默认等级字符串；
4. 当漏斗结果 `MatchedBy == "default"` 且标准默认等级高于底线结果时，采用标准默认等级（`confidence=0.50`，`MatchedBy="standard:" + level`）。

---

## 9. 纵深防御中间件流水线 (`pkg/middleware`)

所有进入 Gin HTTP 服务的请求均依次流经 **8 层**标准化中间件栈（原 9 层中的 `StructuredLogger` 已下沉至 `pkg/observability.RequestLoggerWithModule`，作为独立中间件按需挂载）：

```text
HTTP Request
     │
     ▼
[ 1. TraceMiddleware ]       ➔ 提取/生成 X-Trace-ID & X-Request-ID，注入 context
     │                         （实现下沉至 pkg/observability，trace.go 为兼容垫片）
     ▼
[ 2. Recovery ]              ➔ 捕获 panic 并使用统一 JSON 信封输出 500
     │
     ▼
[ 3. SecurityHeaders ]       ➔ 注入 6 项安全响应头 (nosniff, HSTS, X-Frame-Options 等)
     │
     ▼
[ 4. MaxBodySize ]           ➔ 限制请求体最大 32 MiB，防止内存拒绝服务 (DoS)
     │
     ▼
[ 5. MaxConcurrent ]         ➔ 限制全局最大并发请求数 (默认 1000)，超载返回 503
     │
     ▼
[ 6. RateLimit ]             ➔ 客户端 IP 32 分片令牌桶限流 (RPS + Burst)，10 分钟过期自动淘汰
     │
     ▼
[ 7. CORS ]                  ➔ 精确 Origin 白名单匹配，严格禁止通配符携带凭证
     │
     ▼
[ 8. Auth / AuthWithRoles ]  ➔ API Key 常量时间比对 (crypto/subtle.ConstantTimeCompare)
     │                         /metrics 纳入鉴权范围（P1-6 安全加固）
     ▼
业务 Handler (Controller)
```

### 9.1 AuthWithRoles 读写密钥分离

```go
func AuthWithRoles(apiKey, readerKey string, readOnly []ReadOnlyEndpoint) gin.HandlerFunc

type ReadOnlyEndpoint struct {
    Method string  // HTTP 方法（GET / POST 等）
    Path   string  // 路径前缀（以 "/" 为边界匹配）
}
```

**设计动机**：数据局核验专区只需要读存证/验真，却与写入方共用同一把 API Key，等于「被查者握着查账凭证」。本中间件实现权责分离：

| Token 匹配 | 行为 |
|---|---|
| `token == apiKey` | 全量放行（运维/业务写入身份） |
| `token == readerKey` + 命中白名单 | 放行（只读核验身份） |
| `token == readerKey` + 未命中白名单 | **403 FORBIDDEN**（显式拒绝，绝不静默降级为可读） |
| 两把 Key 都不匹配 | 401 UNAUTHORIZED |

**白名单必须带 Method**：同一 `/api/audit/logs` 上 GET 是查询、POST 是写入，只比路径会把写权限漏给核验员。

`readerKey` 为空时完全退化为 `Auth(apiKey)` 的既有语义，存量部署零影响。

### 9.2 /metrics 鉴权加固 (P1-6)

`/metrics` 端点暴露 Prometheus 指标数据，可能泄漏内部接口路径与系统拓扑信息。自 P1-6 安全加固起，`Auth` 与 `AuthWithRoles` 均将 `/metrics` 纳入强制鉴权范围：

```go
// 非 /api/ 且非 /metrics 的路径才豁免鉴权
if !strings.HasPrefix(path, "/api/") && path != "/metrics" {
    c.Next()
    return
}
```

仅 `/health`、`/readyz`、`/api/health` 三个探活端点保持豁免。

---

## 10. flusher 微批设计 (`pkg/store/flusher`)

### 10.1 8 字段 Config 结构体

```go
type Config struct {
    BufferSize     int           // 内部队列容量（默认 10000）
    MaxBatchSize   int           // 单次刷盘最大批条数（默认 200）
    FlushInterval  time.Duration // 定时刷盘间隔（默认 20ms）
    EnqueueTimeout time.Duration // 入队超时（默认 500ms）
    FlushTimeout   time.Duration // 强制刷盘超时（默认 5s）
    CloseTimeout   time.Duration // 优雅停机排空超时（默认 10s）
    MaxRetries     int           // 失败后最大重试次数（默认 3）
    MaxStaged      int           // 最大暂存积压量（默认 50000）
}
```

### 10.2 内部架构

```text
HTTP / gRPC 请求
 (Goroutine 1..N)
        │
        ▼
 ┌──────────────┐   1. Lock stateMu ──▶ 暂存到 recentLogs[ID] (读己之写)
 │   SaveLog    │   2. Lock closeMu (若已关闭则拒绝)
 └──────┬───────┘   3. 检查 RetryPending() < MaxStaged (否则返回 ErrBacklogSaturated)
        │           4. 非阻塞入队 queue (若满则回退同步落盘并计数 droppedTotal)
        ▼
   queue (chan pendingItem, 容量 BufferSize)
        │
        ▼
 ┌─────────────────────────────────────────────────────────────────────────┐
 │                      单一后台协程 (flushWorker)                          │
 │                                                                         │
 │  1. FIFO 弹出微批 (最大 MaxBatchSize 条或 FlushInterval 定时器触发)       │
 │  2. 串行依次绑定: item.PrevHash = lastHash                              │
 │  3. 重新计算: item.IntegrityHash = ComputeAuditIntegrityHash(...)       │
 │  4. 推进: lastHash = item.IntegrityHash                                 │
 │  5. 原子大事务批量落盘: underlying.SaveLogsBatch(logs, snaps)            │
 │  6. 清理 recentLogs 中已落盘的条目                                      │
 │  7. 失败时指数退避重试 (最多 MaxRetries 次)                               │
 │  8. 重试耗尽后转入 staged backlog，等待后续恢复                           │
 └─────────────────────────────────────────────────────────────────────────┘
```

### 10.3 重试与暂存恢复

**重试逻辑**：底层 `SaveLogsBatch` 失败时，Worker 执行最多 `MaxRetries` 次指数退避重试（`25*(1<<attempt)` ms），每次重试前将积压 backlog 与新批次按 FIFO 顺序合并，保证写入时序不变。

**暂存 (Staged) 恢复**：重试耗尽后，整批数据（积压 + 新批次）转入 `backlogLogs` / `backlogSnaps` 暂存区，`retryPending` 计数器更新，`failedTotal` 指标递增。后续每次成功刷盘时检查暂存区是否有积压数据，若有则优先合并入下一批次重试，直到全部持久化成功后输出 `"audit flush backlog recovered and fully persisted"` 恢复日志。

### 10.4 有界积压保护 (`ErrBacklogSaturated`)

```go
var ErrBacklogSaturated = errors.New("audit flush backlog saturated, underlying storage unavailable")
```

当 `RetryPending() >= MaxStaged` 时，`SaveLogWithSnapshot` 立即返回 `ErrBacklogSaturated`，拒绝继续入队。此设计防止底层存储长时间不可用时内存无限膨胀，将背压 (Backpressure) 信号传递到上层调用方。

### 10.5 核心技术保证

* **哈希链 100% 绝对连续**：所有记录的 `PrevHash` 与 `IntegrityHash` 均在单一 Worker 中按出队顺序串行完成，从数学上消除并发交错导致的断链；
* **读己之写 (Read-Your-Own-Writes) 即时可见**：写入请求在入队瞬间即写入内存 `recentLogs` 映射。调用 `GetLog(id)` 时优先检索内存缓冲，未刷盘数据立刻可读；
* **安全优雅停机 (Zero-Loss Graceful Shutdown)**：`Close()` 首先闭合写锁拦截新请求，随后通过 `stopCh` 通知 Worker 执行 `drainQueue()` 彻底排空队列并执行最后一次批量落盘，保证停机过程零数据丢失；
* **无通道争用并发 Flush**：`Flush()` 通过专用的 `flushReqCh` 向 Worker 发送请求并阻塞等待，Worker 统一执行 `drainQueue()` 并在处理完成后通知调用方。

---

## 11. 全链路可观测性与指标采集

### 11.1 指标设计与防基数爆炸 (`pkg/metrics`)

Prometheus 抓取高频接口时，若直接将 URL 路径中的动态参数（如 UUID、任务 ID）作为 label，会导致 Prometheus 内存膨胀并崩溃。

`pkg/metrics.Collector` 采取了严格的**路由模板归一化策略**与**独立 Registry 隔离**：

1. **标准路由模板**：中间件自动抓取匹配到的 Gin 路由模式（例如 `/api/audit/logs/:id` 而不是实际的 `/api/audit/logs/log-12345`）；
2. **未匹配拦截**：未命中路由的扫描探测统一归一为 `NOT_FOUND`；
3. **独立 Registry**：每个 `Collector` 使用独立的 `prometheus.NewRegistry()`（非全局默认），防止跨模块注册冲突；
4. **低基数标签约束**：`naming.Observer` 接口标签严格控制为枚举值，入站原始脏数据不进入标签。

### 11.2 15 项核心监控指标

| # | 指标名称 | 类型 | 标签 |
|---|---|---|---|
| 1 | `http_requests_total` | CounterVec | `method`, `path`, `status` |
| 2 | `http_request_duration_seconds` | HistogramVec | `method`, `path` |
| 3 | `agent_requests_total` | CounterVec | `endpoint`, `status` |
| 4 | `agent_request_duration_seconds` | HistogramVec | `endpoint` |
| 5 | `orphaned_tasks_recovered_total` | CounterVec | `type` |
| 6 | `tasks_retried_total` | CounterVec | `result` |
| 7 | `circuit_breaker_state` | GaugeVec | `node` |
| 8 | `task_lease_conflicts_total` | Counter | — |
| 9 | `task_lease_expired_total` | Counter | — |
| 10 | `task_claim_latency_seconds` | Histogram | — |
| 11 | `task_transitions_total` | CounterVec | `from`, `to`, `result` |
| 12 | `service_hub_ready` | Gauge | — |
| 13 | `privshield_api_alias_requests_total` | CounterVec | `alias`, `canonical`, `target` |
| 14 | `privshield_datasource_normalize_errors_total` | CounterVec | `reason` |
| 15 | `privshield_datasource_requests_total` | CounterVec | `datasource_id`, `api_code`, `status` |

所有指标均携带 `module` ConstLabel，标识所属微服务模块。

### 11.3 可观测性基础设施下沉 (`pkg/observability`)

原 `pkg/middleware` 中的 `StructuredLogger` 已下沉至 `pkg/observability` 包，实现关注点分离：

| 功能 | 函数 | 说明 |
|---|---|---|
| 结构化日志 | `NewLogger(format, level)` | slog JSON/Text 双格式，可配日志级别 |
| 请求访问日志 | `RequestLoggerWithModule(module)` | 替代原 `StructuredLogger`，记录请求耗时、状态码、客户端 IP |
| 追踪上下文 | `TraceMiddleware()` | X-Trace-ID / X-Request-ID 双头注入与 Context 传播 |
| 分布式追踪 | `StartSpan(ctx, name, attrs)` | Tracer 接口 + OTel 集成 |
| RED 指标 | `NewREDMetrics()` | 独立 Registry + `privshield_` 前缀，HTTP/gRPC 双协议中间件 |

`pkg/middleware/trace.go` 保留了 `TraceMiddleware()` 和 `GetTraceID()` 的兼容垫片，现有调用方无需修改导入路径。

---

## 12. PostgreSQL Phase B 多副本租约调度 (`pkg/store/postgres`)

在多副本部署场景下，多个 `service-hub` 实例并发消费任务队列。系统通过 PostgreSQL 的 `FOR UPDATE SKIP LOCKED` 行级短事务实现无死锁的竞争领取机制：

```sql
UPDATE tasks
SET status = 'running',
    lease_owner = $1,
    lease_token = $2,
    lease_expires_at = NOW() + $3::interval,
    stage = 'claimed',
    updated_at = NOW()
WHERE id = (
    SELECT id FROM tasks
    WHERE status = 'pending'
       OR (status = 'running' AND lease_expires_at < NOW())
    ORDER BY priority DESC, created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, type, priority, status, stage, payload_json,
          lease_owner, lease_token, lease_expires_at, created_at, updated_at;
```

#### 租约调度特性：
1. **零阻塞吞吐**：正在被处理的行被跳过，其他副本无需等待锁，直接领取下一条任务；
2. **自动故障接管**：当副本崩溃或因网络分区未续约时，`lease_expires_at < NOW()` 条件允许健康副本自动重新领取并执行该孤立任务；
3. **令牌防脑裂 (Token Fencing)**：所有写回操作（`CompleteLease` / `RenewLease` / `FailLease`）均携带 `(id, owner, token)` 三元组条件校验，若租约已被夺走则更新失败并返回 `false`，彻底阻断延迟写覆盖。

---

## 13. 总结与架构质量指标

| 维度 | 架构指标 / 达标规范 | 关键保障技术 |
|---|---|---|
| **密码合规** | GB/T 39786-2021 密评三级 | 全栈纯 Go 国密 SM3/SM4-GCM，HKDF-SM3 密钥派生，双版本信封加密 |
| **存证吞吐** | 单节点 SQLite 3,000~5,000 QPS | `BufferedAuditStore` 单 Worker 串行微批聚合刷盘 |
| **存证完整性** | 100% 连续哈希链，零断链、零丢记录 | 优雅停机排空保障 + 单协程出队哈希绑定 + 5 候选降级验真 |
| **归档安全** | 归档失败阻断删除 (STRICT_STORAGE=true) | Archive-Before-Delete 模式 + AuditArchiveReader 接口 |
| **零信任门禁** | 非环回暴露强制全量安全配置 | `ValidateFailClosed` 五项校验 + `IsLoopbackHost` 环回检测 |
| **服务可用性** | 99.99%，秒级探针平滑降级 | Per-Endpoint 独立熔断 + PG 故障 3s 超时自动回退 SQLite WAL |
| **分布式调度** | 多节点并发 0 冲突、0 死锁 | PostgreSQL `FOR UPDATE SKIP LOCKED` 原子租约 + 令牌防脑裂 |
| **标识治理** | SSOT 零逃逸，别名冲突零容忍 | `naming.Registry` 4 条目 + `init()` 冲突检测 + `NormalizeSecurityLevelID` 双表达映射 |
| **指标安全** | 防基数爆炸，独立 Registry 隔离 | 路由模板归一化 + 低基数枚举标签 + 脏数据禁入标签 |
| **代码工程质量** | Go 1.25+, ≥90% 覆盖率, 零分配热路径 | 纯 Go 跨平台编译，Multi-module workspace，atomic 无锁设计 |
