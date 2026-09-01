# PrivShield 共享基础包 (Shared PKG) — 详细系统设计说明书

> **文档定位**：数联天下 · 数盾（`PrivShield`）全栈公共基础设施库（`pkg`）的核心架构、子系统设计、密码学实现与并发存储原语详细说明书。  
> **适用模块**：`service-hub`、`datasource-mgr`、`audit-log`、`console/bff-go`、`engine-go`、`privacy-go-sdk`。  
> **设计基准**：GB/T 39786-2021 密评三级、GM/T 0004-2012 (SM3)、GM/T 0002-2012 (SM4)、RFC 5869 (HKDF)、RFC 7515/7516、Go 1.25+ 现代微服务架构规范。

---

## 1. 架构定位与设计哲学

在 `PrivShield` 分布式微服务架构中，`pkg` 模块作为所有 Go 子系统共享的底层基座，承载了**「密码安全、持久存储、中间件防护、上游通信、命名治理、安全门禁、全链路可观测」**七大核心能力。

```text
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                              PrivShield 业务与微服务层                                 │
│  service-hub (:8082)   │  datasource-mgr (:8083)  │  audit-log (:8084)  │  bff-go      │
└──────────┬─────────────────────────┬─────────────────────────┬───────────────────┬─────┘
           │                         │                         │                   │
┌──────────▼─────────────────────────▼─────────────────────────▼───────────────────▼─────┐
│                            PrivShield 共享基础设施包 (pkg)                              │
├────────────────┬──────────────────────┬─────────────────────┬─────────────────────────┤
│ 1. 密码学与安全 │ crypto (SM3/SM4-GCM/ │ tlsutil (mTLS / CN  │ auth (Identity / 权限   │
│                │ HKDF-SM3 信封加密 v2) │ 白名单热加载)        │ 映射 / 常量时间认证)     │
│ 2. 存储与微批  │ store (抽象接口 /     │ flusher (单 Worker   │ config (安全门禁        │
│                │ Postgres / SQLite)    │ 串行哈希微批刷盘器)  │ / Fail-Closed 校验)     │
│ 3. 中间件防护  │ middleware (8层防护栈  │ validation (输入     │ naming (SSOT 注册表 /   │
│                │ / IP 令牌桶限流)       │ 安全校验)            │ Observer / 安全分级词表) │
│ 4. 运行时与观测│ agent (多节点熔断重试  │ metrics (防基数爆炸  │ observability (RED      │
│                │ 客户端)               │ Prometheus)          │ / slog / 追踪)          │
└────────────────┴──────────────────────┴─────────────────────┴─────────────────────────┘
```

### 1.1 设计原则

1. **零第三方 CGO 依赖 (Pure Go)**：全部密码学（SM3/SM4）、嵌入式存储（SQLite `modernc.org/sqlite`）均采用纯 Go 编译，杜绝 CGO 交叉编译困难与动态链接库安全漏洞。
2. **规范化密码合规 (SM3/SM4-GCM/HKDF)**：全链路遵循国密商用密码标准，v2 信封加密采用 HKDF-SM3 逐记录 salt 派生，哈希前像采用 UTC 纳秒级规范化格式，快照存储强制执行 SM4-GCM 信封加密。
3. **单 Worker 顺序存证保证**：通过 `BufferedAuditStore` 实现高并发写入入队、单 Worker 串行哈希链绑定与微批合并刷盘，兼顾 3,000+ QPS 吞吐与 100% 连续防篡改哈希链。
4. **两阶段平滑降级与自适应弹性**：数据库优先连接高并发分布式 PostgreSQL（Phase B），若探针超时（3s）或未配置则平滑回退至单机 SQLite WAL，并根据 CPU 核心数自适应调整连接池上下限。
5. **Fail-Closed 安全门禁**：所有服务启动时强制校验安全开关（API Key、TLS、mTLS 白名单、加密密钥、哈希链密钥），缺失即拒绝启动，消除静默降级盲区。

---

## 2. 模块拓扑与目录划分

```text
pkg/
├── agent/                  # 上游 PrivShield Agent REST API 客户端封装
│   ├── client.go           # Client（多节点轮询、按节点独立熔断器、重试、鉴权头注入、64MiB 响应保护）
│   └── client_test.go      # 基础请求、鉴权、熔断器状态流转单测
├── auth/                   # 认证身份与权限映射
│   ├── identity.go         # Identity（ServiceType / Scopes / 权限判定）
│   ├── middleware.go        # AuthMiddleware / RequirePermission / Bearer 提取
│   └── settings.go         # KeyConfig / Settings（内外部 Key 配置）
├── circuitbreaker/         # 三态断路器共享实现
│   └── circuitbreaker.go   # Breaker（Closed → Open → HalfOpen）
├── config/                 # 集中化环境变量解析与 fail-closed 安全门禁
│   ├── env.go              # EnvString, EnvInt, EnvBool, EnvDuration
│   ├── env_test.go         # 环境变量读取与默认值测试
│   └── security.go         # SecurityRequirements / ValidateFailClosed / 5 哨兵错误
├── crypto/                 # 国密商用密码与信封加密体系
│   ├── sm3.go              # GM/T 0004-2012 SM3 算法、HMAC-SM3、UTC 规范化哈希
│   ├── sm3_test.go         # 国标向量验证与哈希完整性单测
│   ├── sm4.go              # GM/T 0002-2012 SM4 分组密码、CBC/CTR/GCM 模式
│   ├── envelope.go         # SM4-GCM 信封加密 (enc:v2: 当前写入 / enc:v1: 存量兼容)
│   └── envelope_test.go    # 加密解密、IV 随机性、防篡改校验单测
├── docs/                   # pkg 体系工程技术文档中心
├── gateway/                # 网关负载均衡共享实现
│   └── balancer.go         # LoadBalancer（P2C-EWMA / Round-Robin / Least-Conn）
├── grpcserver/             # gRPC 服务器构建器
│   └── server.go           # Server（Builder 模式 / 拦截器 / 优雅停机）
├── metrics/                # 模块级隔离 Prometheus 指标采集器
│   ├── metrics.go          # Collector（实现 naming.Observer / CounterVec / HistogramVec）
│   └── metrics_test.go     # 指标收集与 HTTP 中间件测试
├── middleware/             # Gin 生产级 8 层纵深防御中间件
│   ├── auth.go             # Auth / AuthWithRoles（常量时间比较 / 只读核验员角色）
│   ├── envelope.go         # 跨语言统一 API 信封格式与全局异常拦截
│   ├── envelope_test.go    # 异常信封与 HTTP 状态码映射测试
│   ├── middleware.go       # CORS, SecurityHeaders, MaxBodySize, MaxConcurrent, Recovery
│   ├── ratelimit.go        # 基于 IP 与 ClientID 的令牌桶平滑限流与自动过期淘汰
│   ├── trace.go            # X-Trace-ID 与 X-Request-ID 双头注入与 Context 传播
│   └── trace_test.go       # 链路追踪传播测试
├── naming/                 # 跨服务标识 SSOT 注册表与安全分级词表
│   ├── naming.go           # Registry / Entry / Normalize / CheckWritable / AliasConflicts
│   ├── naming_test.go      # 命名转换与归一化单测
│   ├── levels.go           # L1~L5 安全分级词表 / NormalizeSecurityLevelID / MaxSecurityLevelID
│   ├── observer.go         # Observer 接口 / 别名指标上报 / 归一化失败上报
│   └── observer_test.go    # 动态观测测试
├── observability/          # 结构化日志、RED 指标与分布式追踪
│   ├── logger.go           # NewLogger / InitLogger（slog 结构化日志）
│   ├── metrics.go          # REDMetrics（独立 Registry / Gin 中间件 / gRPC 拦截器）
│   ├── request_logger.go   # RequestLogger / RequestLoggerWithModule（结构化请求日志）
│   ├── trace.go            # TraceMiddleware / GenerateRequestID / Context 传播
│   └── tracing.go          # Tracer 接口 / NoOpTracer / OTelTracer
├── profile/                # 隐私参数 Profile 解析
│   └── resolver.go         # Resolver / PrivacyProfile / YAML 动态加载
├── store/                  # 数据持久化抽象层与双引擎实现
│   ├── store.go            # 核心领域实体 / AuditArchiveReader / ChainReason* 常量
│   ├── audit_hash.go       # HMAC-SM3 密钥化存证哈希 / SetAuditChainKey / 多轨验真
│   ├── levels.go           # 审计推荐策略与阈值
│   ├── flusher/            # 高并发微批缓冲刷盘器
│   │   ├── flusher.go      # BufferedAuditStore 单 Worker 串行哈希链绑定与读己之写
│   │   └── flusher_test.go # 50+ 并发哈希连续性、Close零丢数据、并发 Flush 测试
│   ├── sqlite/             # SQLite 纯 Go 引擎 (WAL 模式, busy_timeout=5000)
│   │   ├── init.go         # 驱动初始化、PRAGMA 调优与自动 Schema 迁移
│   │   ├── tasks.go        # 任务存储实现
│   │   ├── datasources.go  # 数据源存储实现
│   │   ├── audit.go        # 审计日志与快照存储，SQL 级聚合统计
│   │   ├── leased.go       # SQLite 租约桩实现（返回 ErrLeaseNotSupported）
│   │   └── sqlite_test.go  # SQLite 完整性与事务测试
│   ├── postgres/           # PostgreSQL Phase B 分布式原子租约与审计存储
│   │   ├── postgres.go     # pgxpool 连接池自适应调优与 Schema 初始化
│   │   ├── schema.go       # 分区表与局部索引迁移
│   │   ├── leased.go       # FOR UPDATE SKIP LOCKED 多副本原子任务争抢与租约续期
│   │   ├── audit.go        # PostgreSQL 原生分区审计日志存储与 SQL 报告聚合
│   │   ├── tasks.go        # PostgreSQL 任务存储实现
│   │   └── leased_test.go  # 多节点无死锁争抢租约单测
│   ├── memory/             # 纯内存存储实现（单元测试与本地无持久化模式）
│   │   ├── memory.go       # 基于 sync.RWMutex 的内存 Mock（实现 AuditArchiveReader）
│   │   └── memory_test.go  # 内存对账测试
│   └── cmd/
│       ├── migrate/        # 数据库自动化迁移 CLI 工具
│       └── repairchain/    # 离线存证重签与链修复工具
├── tlsutil/                # mTLS 双向认证与动态 CN 白名单管理
│   ├── tlsutil.go          # TLS 1.3 强制、CA 证书链加载、SPKI 公钥固定
│   ├── whitelist.go        # 基于 YAML 的 CN 白名单管理器（5s mtime 热重载）
│   ├── grpc_interceptor.go # gRPC Unary/Stream 拦截器，CN 提取与作用域校验
│   └── whitelist_test.go   # 白名单加载与热重载测试
└── validation/             # 输入安全校验与参数清洗
    ├── validation.go       # 白名单枚举、端口范围、抗碰撞唯一 ID 生成、安全分页
    └── validation_test.go  # 校验用例单测
```

---

## 3. 密码学与安全体系设计 (`pkg/crypto` & `pkg/tlsutil`)

### 3.1 国密 SM3 算法与 9 要素防篡改存证链 (`pkg/crypto/sm3.go` & `pkg/store/audit_hash.go`)

存证哈希采用严格的国密 **SM3（GM/T 0004-2012）** 密码杂凑算法，输出 256 位（64 字符十六进制）摘要。

#### 3.1.1 9 要素前像规范化格式 (Canonical Pre-image)

为了杜绝时区差异、浮点表示不一致或字段缺失导致的验签失败，前像字符串强制遵循以下规范：

$$\text{Data} = \text{PrevHash} \mid \text{LogID} \mid \text{Timestamp(UTC Nano)} \mid \text{Algorithm} \mid \text{InputHash} \mid \text{OutputHash} \mid \text{User} \mid \text{SecurityLevel} \mid \text{ParamsJSON}$$

* **时区归一化**：`timestamp.UTC().Format(time.RFC3339Nano)`；
* **参数确定性**：空参数或无效 JSON 归一化为 `"{}"`；
* **双模计算**：配置存证 HMAC 密钥时 → `HMAC-SM3(key, "SM3-HMAC:v1|" + Data)`；未配置时 → `Hex(SM3(Data))`。

```go
func ComputeAuditIntegrityHash(logID, prevHash string, timestamp time.Time,
    algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) string {
    payload := integrityPayload(logID, prevHash, timestamp, algorithm,
        inputHash, outputHash, user, securityLevel, paramsJSON, true)
    if key := AuditChainKey(); key != "" {
        return crypto.HMACSM3Hex([]byte(key), []byte(AuditHashSM3HMAC+"|"+payload))
    }
    sum := crypto.SumSM3([]byte(payload))
    return hex.EncodeToString(sum[:])
}
```

#### 3.1.2 HMAC-SM3 密钥化完整性存证链 (`pkg/store/audit_hash.go`)

为抵御「知晓计算口径即可伪造合法记录」的攻击面，系统支持通过 `SetAuditChainKey(key)` 注入局方托管 HMAC 密钥。配置后新写入存证一律采用 `AuditHashSM3HMAC = "SM3-HMAC:v1"` 算法标识：

$$\text{Hash} = \text{HMAC-SM3}(\text{key},\; \texttt{"SM3-HMAC:v1|"} \parallel \text{Data})$$

* **密钥注入**：进程启动阶段一次性调用 `SetAuditChainKey`，内部以 `atomic.Pointer[string]` 存储，运行期改钥会导致既有记录核验失败；
* **无密钥回退**：传空串退回无密钥 SM3 口径（仅可证明内容未被修改，不能阻止知晓口径者重算，故生产环境必须注入密钥）；
* **当前口径查询**：`ComputeAuditIntegrityHashAlgo()` 返回当前写入使用的算法标签（`"SM3-HMAC:v1"` 或 `"SM3"`），供诊断与重签工具使用。

#### 3.1.3 多轨平滑验真 (Multi-Track Verification)

`VerifyAuditIntegrityHash` 按优先级依次尝试以下候选算法，确保存量历史数据在密钥化升级后依然合法可验：

| 优先级 | 候选算法标签 | 前像格式 | 适用场景 |
|---|---|---|---|
| 1（最高） | `SM3-HMAC:v1` | HMAC-SM3(key, "SM3-HMAC:v1\|" + UTC payload) | 密钥化口径写入的记录 |
| 2 | `SM3` | SM3(UTC payload) | 国密升级后、密钥化之前的无密钥 SM3 记录 |
| 3 | `SHA256-LEGACY` | SHA-256(UTC payload) | 国密升级前的 SHA-256 记录 |
| 4 | `SM3-LEGACY` | SM3(本地时区 payload) | 早期本地时区 SM3 记录 |
| 5 | `SHA256-LEGACY` | SHA-256(本地时区 payload) | 最早期本地时区 SHA-256 记录 |

验真采用 `hmac.Equal` 常量时间比较，杜绝时序侧信道。命中非当前写入口径的候选标记 `LegacyHashed` 计数，指导后续重签。

#### 3.1.4 快照独立完整性哈希

快照记录不再简单继承主日志哈希，而是计算覆盖自身字段（含加密样本）的独立完整性哈希：

$$\text{SnapData} = \text{PrevHash} \mid \text{SnapshotID} \mid \text{AuditLogID} \mid \text{Timestamp} \mid \text{Algorithm} \mid \text{InputSample} \mid \text{OutputSample} \mid \text{ParamsJSON}$$

密钥化口径使用 `"SM3-HMAC:v1-SNAPSHOT|"` 前缀绑定，验真时保留旧版「继承自主日志哈希」的兼容性回退。

### 3.2 国密 SM4-GCM 动态信封加密 (`pkg/crypto/envelope.go`)

为保证敏感数据快照（`SnapshotRecord.InputSample` / `OutputSample`）在落盘及跨域传输中的机密性，系统实现了基于 **SM4-GCM** 的双版本信封加密机制。

#### 3.2.1 当前写入格式：`enc:v2:` (HKDF-SM3 + 逐记录 Salt)

```text
明文数据 ──▶ [16-byte 随机 Salt] ──▶ [HKDF-SM3 派生密钥] ──▶ [12-byte 随机 Nonce]
         ──▶ [SM4-GCM 加密 (AAD = "enc:v2:")] ──▶ "enc:v2:<Base64(Salt + Nonce + Ciphertext + Tag)>"
```

1. **HKDF-SM3 密钥派生** (RFC 5869)：`DeriveKeyHKDF(secret, salt)` 以 HMAC-SM3 执行 Extract-then-Expand，info 字段为 `"PrivShield audit snapshot SM4-GCM v2"`，将派生密钥绑定到审计快照加密用途，防止跨用途密钥复用；
2. **逐记录 Salt (16 字节)**：每条记录携带独立随机 salt，使同一口令在不同记录上产出互不相同的加密密钥，抵抗短语令离线暴破；
3. **认证加密 (AEAD)**：GCM 模式 16 字节 Auth Tag 确保密文不可篡改；
4. **前缀参与 AAD**：版本前缀 `"enc:v2:"` 作为附加认证数据，剥离或改写前缀直接导致认证失败，不存在「去前缀即降级为明文」的静默通道；
5. **空密钥拒绝**：`secret` 为空时返回 `ErrEmptyKey`，不再静默降级为明文落盘。

#### 3.2.2 历史存量格式：`enc:v1:` (SHA-256 截断派生)

```text
"enc:v1:<Base64(12-byte Nonce + Ciphertext + Tag)>"  —  密钥 = SHA-256(secret)[:16]
```

v1 仅保留解密能力，不再用于写入。解密时优先匹配 v2，其次回退 v1，无前缀则返回 `ErrUnencryptedValue`。

### 3.3 mTLS 双向认证与动态 CN 白名单 (`pkg/tlsutil`)

* **TLS 1.3 强制**：`BuildServerTLSConfig` 设置 `MinVersion = tls.VersionTLS13`，禁用所有降级密码套件；
* **SPKI 公钥固定**：支持配置 `PinnedPubKeyFile`，防止 CA 被攻破后签发伪造证书；
* **CN 白名单 5s 热重载**：`DynamicWhitelist` 每 5 秒检测配置文件 mtime 变化并原子替换映射，支持 `clients` 与 `entries` 双格式 YAML；
* **gRPC 拦截器**：`UnaryServerInterceptor` / `StreamServerInterceptor` 自动提取客户端 CN 并校验方法级作用域。

---

## 4. 存储架构与微批缓冲刷盘引擎 (`pkg/store`)

### 4.1 存储接口契约体系

```text
              ┌─────────────────────────┐
              │    store.AuditStore     │
              └────────────▲────────────┘
                           │
       ┌───────────────────┼────────────────────────┐
       │                   │                        │
┌──────┴──────┐   ┌────────┴─────────┐   ┌────────┴──────────┐
│ flusher     │   │  底层实际持久化   │   │ AuditArchiveReader│
│ Buffered    │   │ (Postgres/SQLite │   │ (可选：归档读取)   │
│ AuditStore  │   │  /Memory)        │   │                   │
└──────┬──────┘   └────────▲─────────┘   └────────▲──────────┘
       │                    │                       │
       └──── 批量写入/刷新 ──┘    FetchOldest/DeleteByIDs ┘
```

1. **`TaskStore`**：单节点任务流水线状态管理；
2. **`LeasedTaskStore`**：多副本分布式短事务原子租约争抢（`ClaimNext` / `RenewLease` / `CompleteLease` / `FailLease`）；不支持租约的后端（SQLite / Memory）统一返回 `ErrLeaseNotSupported`；
3. **`DataSourceStore`**：多源数据连接配置与访问审计；
4. **`AuditStore`**：防篡改连续存证日志与加密快照；
5. **`AuditArchiveReader`**：可选能力接口，供「先归档后删除」留存红线逐批取档并精确删除。

### 4.2 高并发缓冲刷盘器 (`flusher.BufferedAuditStore`)

传统方案在高并发写入时直接对底层数据库写入单条记录，导致数据库锁争用严重（SQLite 表现为 `database is locked`，PG 表现为连接池耗尽），且多协程并发写入会破坏区块链式哈希链的先后顺序。

`BufferedAuditStore` 的内部架构设计如下：

```text
HTTP / gRPC 请求
 (Goroutine 1..N)
        │
        ▼
 ┌──────────────┐   1. Lock stateMu ──▶ 暂存到 recentLogs[ID] (实现"读己之写")
 │   SaveLog    │   2. Lock closeMu (若已关闭则拒绝)
 └──────┬───────┘   3. 非阻塞入队 queue (若满则等待 EnqueueTimeout)
        │
        ▼
   queue (chan pendingItem, 容量 10000)
        │
        ▼
 ┌────────────────────────────────────────────────────────────────────────┐
 │                      单一后台协程 (flushWorker)                        │
 │                                                                        │
 │  1. FIFO 弹出微批 (最大 200 条或 20ms 定时器触发)                     │
 │  2. 单一权威: 调用方传入的 prevHash 被强制覆盖为 b.lastHash           │
 │  3. 串行依次绑定: item.IntegrityHash = ComputeAuditIntegrityHash(...) │
 │  4. 推进: b.lastHash = item.IntegrityHash                             │
 │  5. 原子大事务批量落盘: underlying.SaveLogsBatch(logs, snaps)          │
 │  6. 失败保留至 retry backlog (最多 MaxRetries=3 次退避重投)           │
 │  7. 清理 recentLogs 中已落盘的条目                                    │
 └────────────────────────────────────────────────────────────────────────┘
```

#### 4.2.1 核心技术突破与保证

* **哈希链 100% 绝对连续**：所有记录的 `PrevHash` 与 `IntegrityHash` 的生成均收敛在单一 Worker 中按出队顺序串行完成，从数学上消除并发交错导致的断链；
* **读己之写 (Read-Your-Own-Writes) 即时可见**：写入请求在入队瞬间即写入内存 `recentLogs` 映射并更新 `latest` 原子指针。调用 `GetLog(id)` 时优先检索内存缓冲，未刷盘数据立刻可读；
* **安全优雅停机 (Zero-Loss Graceful Shutdown)**：`Close()` 首先闭合写锁拦截新请求，随后通过 `stopCh` 通知 Worker 执行 `drainQueue()` 彻底排空队列并执行最后一次批量落盘，保证停机过程零数据丢失；
* **无通道争用并发 Flush**：`Flush()` 通过专用的 `flushReqCh` 向 Worker 发送请求并阻塞等待，Worker 统一执行 `drainQueue()` 并在处理完成后通知调用方，杜绝并发竞争；
* **内存有界防 OOM**：暂存映射受 `MaxStaged` (50000) 约束，超限按入队序淘汰最旧条目；重试积压区同样有界，饱和后快速返回 `ErrBacklogSaturated`。

### 4.3 先归档后删除留存红线 (`AuditArchiveReader` & 归档段设计)

当配置了 `AUDIT_LOG_RETENTION_DAYS > 0` 时，到期存证记录**必须先完成加密归档落盘并通过磁盘回读验真后**，方可执行物理删除。这一保障通过 `AuditArchiveReader` 接口实现：

```go
type AuditArchiveReader interface {
    // FetchOldestForArchive 返回早于 before 的最多 limit 条到期存证日志（旧→新）及其快照。
    FetchOldestForArchive(before time.Time, limit int) ([]AuditLog, []SnapshotRecord, error)
    // DeleteLogsByIDs 精确删除给定 ID 的存证日志及其级联快照，返回实际删除的日志条数。
    DeleteLogsByIDs(ids []string) (int64, error)
}
```

#### 归档段格式设计

归档段由 `services/audit-log/internal/archive.Archiver` 逐页写入，格式如下：

| 文件 | 内容 |
|---|---|
| `audit-archive-<cutoff>-<seq>.ndjson.gz.enc` | SM4-GCM 加密的 gzip 压缩 NDJSON 段文件，每行一条审计日志或快照 |
| `audit-archive-<cutoff>-<seq>.manifest.json` | 段元数据（版本号 `privshield-audit-archive/v1`、行计数、SM3 行哈希链尾值、创建时间） |

归档流程保证：
1. **逐页归档**：每页 `DefaultArchivePageSize = 500` 条，从最老到期记录开始，无需游标；
2. **行哈希链**：段内每行的 SM3 哈希串联前一行的哈希，保证段内记录不可重排或篡改；
3. **磁盘回读验真**：写入段后立即 `VerifySegment` 回读并验证行哈希链完整性，验真失败则拒绝删除；
4. **精确删除**：`DeleteLogsByIDs` 按该页 ID 集合（`ArchiveIDChunkSize = 500`）精确删除，不出现「删而未档」；
5. **不支持归档的后端拒绝删除**：SQLite / Memory 未实现 `AuditArchiveReader`，调用时返回 `ErrStoreUnsupported`。

### 4.4 验真结论枚举 (ChainReason* 常量)

`ChainVerificationResult.Reason` 取值集合，严格对应存储层核验循环可检测的状态：

| 常量 | 取值 | 语义 | Valid |
|---|---|---|---|
| `ChainReasonOK` | `"ok"` | 全链按当前写入口径逐条验真通过 | `true` |
| `ChainReasonLegacyHashed` | `"legacy_hashed"` | 链连续，但至少一条仅命中迁移前历史候选（待重签，非篡改） | `true` |
| `ChainReasonTamperedPayload` | `"tampered_payload"` | 前序锚点衔接，但自身哈希与任何候选不匹配（原位改写业务字段） | `false` |
| `ChainReasonHashMismatch` | `"hash_mismatch"` | 完整性哈希与期望值不一致，且锚点同时失配 | `false` |
| `ChainReasonBrokenChain` | `"broken_chain"` | 内容验真通过，但 `prev_hash` 与上一条记录不衔接 | `false` |
| `ChainReasonMissingPrev` | `"missing_prev"` | 非链首记录携带空 `prev_hash`（链起点被抹除） | `false` |
| `ChainReasonMissingRecords` | `"missing_records"` | 核验通过记录数小于表内总记录数（链中段存在物理删除） | `false` |

### 4.5 PostgreSQL Phase B 多副本租约调度 (`pkg/store/postgres`)

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
3. **令牌防脑裂 (Token Fencing)**：所有写回操作（`CompleteLease` / `RenewLease` / `FailLease`）均携带 `(id, owner, token)` 三元组条件校验，若租约已被夺走则更新失败并返回 `false`，彻底阻断延迟写覆盖；
4. **不支持租约的后端**：SQLite 与 Memory 实现的租约方法一律返回 `ErrLeaseNotSupported`，调用方据此拒绝多副本调度。

---

## 5. 纵深防御中间件流水线 (`pkg/middleware`)

所有进入 Gin HTTP 服务的请求均依次流经 **8 层**标准化中间件栈（结构化请求日志已迁移至 `pkg/observability.RequestLogger`，不再由 `pkg/middleware` 独立承担）：

```text
HTTP Request
     │
     ▼
[ 1. TraceMiddleware ]       ➔ 提取/生成 X-Trace-ID & X-Request-ID，注入 context
     │
     ▼
[ 2. RequestLogger ]         ➔ pkg/observability.RequestLoggerWithModule("模块名")
     │                         基于 slog 记录请求完成结构化日志（method/path/status/latency_ms）
     ▼
[ 3. Recovery ]              ➔ 捕获 panic 并使用统一 JSON 信封输出 500
     │
     ▼
[ 4. SecurityHeaders ]       ➔ 注入 X-Content-Type-Options, X-Frame-Options, CSP
     │
     ▼
[ 5. MaxBodySize ]           ➔ 限制请求体最大 32 MiB，防止内存拒绝服务 (DoS)
     │
     ▼
[ 6. MaxConcurrent ]         ➔ 限制全局最大并发请求数 (默认 1000)，超载返回 503
     │
     ▼
[ 7. RateLimit / CORS ]      ➔ 客户端 IP 令牌桶限流 (RPS + Burst)
     │                         精确 Origin 白名单匹配，严格禁止通配符携带凭证
     ▼
[ 8. Auth / AuthWithRoles ]  ➔ API Key 常量时间比对 (crypto/subtle.ConstantTimeCompare)
     │                         /metrics 纳入鉴权范围 (P1-6)
     ▼
业务 Handler (Controller)
```

### 5.1 AuthWithRoles 权责分离鉴权

`AuthWithRoles(apiKey, readerKey, readOnly []ReadOnlyEndpoint)` 在单 Key 鉴权之上叠加只读核验员角色：

| 身份判定 | 行为 |
|---|---|
| `token == apiKey` | 全量放行（运维 / 业务写入身份） |
| `token == readerKey` 且 `(method, path)` 命中 `readOnly` 白名单 | 放行（只读核验身份） |
| `token == readerKey` 但未命中白名单 | 403 FORBIDDEN（显式拒绝，不静默降级为可读） |
| 两把 Key 都不匹配 | 401 UNAUTHORIZED |

`ReadOnlyEndpoint` 结构体包含 `Method` 与 `Path` 字段，必须携带方法——同一 `/api/audit/logs` 上 GET 是查询、POST 是写入，只比路径会把写权限漏给核验员。`readerKey` 为空时完全退化为 `Auth(apiKey)` 的既有语义。

### 5.2 /metrics 鉴权

`/metrics` 端点纳入鉴权范围（P1-6），与 `/api/*` 路径同等级别要求。仅 `/health`、`/readyz`、`/api/health` 豁免鉴权。

---

## 6. 全链路可观测性与指标采集 (`pkg/metrics` & `pkg/observability`)

### 6.1 指标设计与防基数爆炸 (Cardinality Control)

Prometheus 抓取高频接口时，若直接将 URL 路径中的动态参数（如 UUID、任务 ID）作为 label，会导致 Prometheus 内存膨胀并崩溃。

`pkg/metrics.Collector` 采取了严格的**路由模板归一化策略**：
1. **标准路由模板**：中间件自动抓取匹配到的 Gin 路由模式（例如 `/api/audit/logs/:id` 而不是实际的 `/api/audit/logs/log-12345`）；
2. **未匹配拦截**：未命中路由的扫描探测统一归一为 `NOT_FOUND`；
3. **核心监控指标**：
   * `http_requests_total{module, method, path, status}`：请求总量计数器；
   * `http_request_duration_seconds{module, method, path}`：延迟分布直方图（默认 Buckets：`0.005s ~ 10s`）；
   * `http_requests_in_flight{module}`：实时并发处理请求量；
   * `agent_client_requests_total{endpoint, status}`：上游 Agent 调用统计；
   * `audit_flusher_flushed_total` / `audit_flusher_dropped_total`：存储微批刷盘运行健康指标；
   * `privshield_api_alias_requests_total{alias, canonical, target}`：命名别名解析统计；
   * `privshield_datasource_normalize_errors_total{reason}`：归一化失败统计（低基数 reason 标签）。

### 6.2 RED 指标体系 (`pkg/observability.REDMetrics`)

独立的 Prometheus Registry 实例，提供协议无关的 RED 方法：

| 指标名称 | 类型 | 标签 |
|---|---|---|
| `privshield_requests_total` | CounterVec | `protocol`, `endpoint`, `status` |
| `privshield_request_duration_seconds` | HistogramVec | `protocol`, `endpoint` |

支持 Gin 中间件（`PrometheusMiddleware`）、gRPC 拦截器（`UnaryServerInterceptor`）与标准 HTTP Handler（`Handler()`），自动跳过 `/metrics` 自引用记录。

---

## 7. 上游 Agent 通信与容灾 (`pkg/agent`)

`pkg/agent.Client` 为各 Go 微服务与底层隐私治理核心引擎之间提供强韧的高并发通信保障。

### 7.1 按端点维度独立熔断器

与传统单熔断器设计不同，`Client` 为每个上游节点维护独立的三态断路器（`map[string]*circuitbreaker.Breaker`）：

```text
Client
  ├── breakers["http://node-a:8079"] → Breaker (Closed)
  ├── breakers["http://node-b:8079"] → Breaker (Open, 冷却中)
  └── breakers["http://node-c:8079"] → Breaker (HalfOpen, 探测中)
```

* **故障隔离**：单节点故障只熔断该节点流量，其余健康节点继续承接请求，杜绝「一台宕机、全集群停摆」；
* **状态流转**：Closed → 连续失败达 `CBThreshold`(5) 次 → Open → `CBCooldown`(30s) 冷却 → HalfOpen → 探测成功 → Closed。

### 7.2 智能轮询端点选取 (`PickEndpoint`)

```go
func (c *Client) PickEndpoint() string
```

基于原子 Round-Robin 轮询调度，沿环形顺序找到第一个「自身熔断器未开启」的节点返回。全部节点均处于 Open 冷却期时返回 `ErrCircuitOpen`。重试时通过 `pickEndpoint(exclude)` 避开刚失败的节点，实现自动故障转移。

### 7.3 指数退避重试与端点切换

```go
// Config 关键参数
type Config struct {
    BaseURLs       []string                // 多节点集群地址列表
    CBThreshold    int                     // 触发熔断的连续失败阈值（默认 5）
    CBCooldown     time.Duration           // 熔断冷却时间（默认 30s）
    MaxRetries     int                     // 最大重试次数（默认 3）
    RetryBaseDelay time.Duration           // 指数退避基础时间（默认 500ms）
    StateObserver  func(node, state string) // 熔断器状态变更观察者回调
    // ...
}
```

重试策略：
1. **退避计算**：`delay = RetryBaseDelay * 2^(attempt-1) + random jitter`；
2. **端点切换**：每轮重试优先 `pickEndpoint(current)` 故障转移到其他健康节点，单节点集群或无替代节点时原地重试；
3. **4xx 防误熔断**：4xx 客户端业务错误直接透传，不计入节点熔断失败计数，防止恶意或格式错误请求击穿熔断器；
4. **结构化错误分类**：`ErrEndpointUnavailable` / `ErrCircuitOpen` / `ErrTransport`，重试判定由 `errors.Is` / `errors.As` 结构化完成，不依赖错误文案子串匹配；
5. **状态观察者**：`StateObserver` 回调在熔断器状态发生流转时触发，用于上报 Prometheus 指标（`SetCircuitBreakerState`）或告警。

### 7.4 诊断接口 (`EndpointStates`)

```go
func (c *Client) EndpointStates() map[string]string
```

返回各上游节点独立的熔断状态快照（键为归一化节点地址），供 `/ops/diagnostics` 与告警定位「究竟是哪台节点被熔断」，而非只看聚合态。

### 7.5 响应体防爆 (Body Limit)

采用 `io.LimitReader` 限制单次读取上限为 64 MiB，防止异常超大响应引发 OOM 崩溃。

---

## 8. 命名治理与 SSOT 注册表设计 (`pkg/naming`)

### 8.1 Registry 唯一事实源 (SSOT)

`naming.Registry` 是全系统权威的数据源静态注册表，管理 canonical `datasource_id` / `api_code` 与入站别名的映射关系：

```go
var Registry = []Entry{
    {APICode: "api1_yibao", DataSourceID: "ds_yibao",     Status: "active",   ...},
    {APICode: "api2_kangyang", DataSourceID: "ds_kangyang", Status: "active",   ...},
    {DataSourceID: "ds_mock3", Status: "reserved", ...},
    {DataSourceID: "ds_mock4", Status: "reserved", ...},
}
```

包 `init()` 时单次构建三组 O(1) 索引：`byDataSourceID`、`byAPICode`、`aliasIndex`，并执行别名冲突防御性检测。

### 8.2 四级优先级归一化 (`Normalize`)

| 优先级 | 匹配策略 | 触发指标 |
|---|---|---|
| 1 | Canonical ID 精确匹配 (`ds_yibao`) | 不触发别名指标 |
| 2 | API Code 契约匹配 (`api1_yibao`) | `RecordAPIAlias(..., TargetAPICode)` |
| 3 | 别名池不区分大小写匹配 (`YIBAO.CSV`) | `RecordAPIAlias(..., TargetDataSourceID)` |
| 4 | 别名池精确匹配（含中文别名 `医保`） | `RecordAPIAlias(..., TargetDataSourceID)` |
| Fail-Closed | 全部未命中 → 报错 `ErrUnknownDataSource` | `RecordNormalizeError(ReasonUnknown)` |

### 8.3 Observer 可观测性接口

```go
type Observer interface {
    RecordAPIAlias(alias, canonical, target string)
    RecordNormalizeError(reason string)
}
```

`*metrics.Collector` 编译期断言实现 `naming.Observer`（`var _ naming.Observer = (*Collector)(nil)`）。服务启动时 `naming.SetObserver(mc)` 注册一次，后续所有归一化调用自动触发 Prometheus 指标上报。未注册时所有上报逻辑为空操作，零性能损耗。

### 8.4 别名冲突检测 (`AliasConflicts`)

`init()` 阶段检测不同数据源之间是否意外声明了同名别名，冲突记录存入 `aliasConflicts` 切片。架构保障：正常情况下切片长度必须恒等于 0，CI 单测严格断言，防止注册表被污染导致歧义路由。

### 8.5 安全分级词表 (L1~L5)

`pkg/naming/levels.go` 是数据安全分级的跨服务唯一事实源，统一管理两套等级表达的映射：

| 等级标识 | Engine Canonical | 中文名称 | 敏感排名 |
|---|---|---|---|
| `L1` | `public` | 公开数据 | 1 |
| `L2` | `internal` | 内部数据 | 2 |
| `L3` | `confidential` | 敏感数据 | 3 |
| `L4` | `secret` | 高敏感数据 | 4 |
| `L5` | `top_secret` | 极敏感数据 | 5 |

`NormalizeSecurityLevelID(level)` 接受任意拼写（`"L3"` / `"l3"` / `"confidential"` / `" L3 "`）并返回规范 L1~L5 标识，未知等级返回空串（不得静默兜底）。`MaxSecurityLevelID(levels...)` 返回入参中敏感度最高的等级。

---

## 9. 安全门禁设计 (`pkg/config`)

### 9.1 Fail-Closed 启动校验 (`ValidateFailClosed`)

所有 PrivShield 服务在启动时统一调用 `ValidateFailClosed(SecurityRequirements)` 执行安全不变式校验，缺失即拒绝启动：

```go
type SecurityRequirements struct {
    ServiceName          string   // 服务标识（如 "audit-log"）
    Hosts                []string // 监听地址列表
    APIKey               string   // 入站鉴权密钥
    TLSEnabled           bool     // TLS 是否已启用
    RequireTLS           bool     // 部署方是否强制 TLS
    GRPCEnabled          bool     // 是否监听 gRPC 端口
    MTLSWhitelistFile    string   // CN 白名单文件路径
    EncryptionKey        string   // 信封加密主密钥
    RequireEncryptionKey bool     // 是否强制加密密钥
    HashKey              string   // 存证哈希链 HMAC 密钥
    RequireHashKey       bool     // 是否强制哈希链密钥
}
```

### 9.2 五条哨兵错误

| 哨兵错误 | 触发条件 | 风险 |
|---|---|---|
| `ErrAPIKeyRequired` | 非环回监听但未配置 API Key | 任何人均可无鉴权访问服务 |
| `ErrTLSRequired` | RequireTLS 为真但 TLS 未启用 | 生产流量明文传输 |
| `ErrMTLSWhitelistRequired` | gRPC TLS 启用但无 CN 白名单 | 任何通过 CA 的客户端可调用全部方法 |
| `ErrEncryptionKeyRequired` | 非环回监听但加密密钥为空 | 快照以明文落盘 |
| `ErrChainKeyRequired` | 非环回监听但哈希链密钥为空 | 无密钥 SM3 可被任何人重算伪造 |

### 9.3 环回地址检测 (`IsLoopbackHost`)

空串、`"localhost"`、`127.0.0.0/8` 与 `::1` 视为本地环回，允许无密钥开发态启动。`"0.0.0.0"`、`"::"` 与具体网卡地址视为对外暴露，触发全部安全校验。无法解析的主机名按对外暴露处理（fail-closed）。

---

## 10. 分类分级标准体系加载器 (`engine-go/internal/dynclassification`)

### 10.1 StandardDef 结构

分类标准定义文件 (`rules/standards/*.yaml`) 由 `StandardDef` 结构表达：

```go
type StandardDef struct {
    StandardID   string   `yaml:"standard_id"`   // 标准唯一标识
    Description  string   `yaml:"description"`    // 标准描述
    Taxonomy     string   `yaml:"taxonomy"`       // 关联的分类体系
    Domains      []string `yaml:"domains"`        // 适用领域
    GlobalParams  struct {
        DefaultLevel string `yaml:"default_level"` // 全局默认安全等级
    } `yaml:"global_params"`
    Levels              map[string]StandardLevelMapping `yaml:"levels"`
    ExtraRules          []RuleDef                       `yaml:"extra_rules"`
    ExtraDowngradeRules []RuleDef                       `yaml:"extra_downgrade_rules"`
}
```

### 10.2 LoadStandardsFromDir

```go
func LoadStandardsFromDir(dir string) ([]StandardDef, []error)
```

从指定目录读取所有 `.yaml` / `.yml` 文件，逐个反序列化为 `StandardDef`。解析失败收集为错误切片但不阻塞启动。`StandardID` 为空时回退取文件名（去扩展名）。结果按 `StandardID` 字母序排列。

### 10.3 分类漏斗中的 highestStandardDefaultLevel 兜底

在三层分类漏斗（AC 规则引擎 → Small-NER ONNX → 外部 LLM 熔断器）的最终仲裁阶段：

1. 当所有层均未产生匹配（`MatchedBy == "default"`）且至少加载了一个标准时；
2. 调用 `highestStandardDefaultLevel()` 遍历所有已加载标准的 `GlobalParams.DefaultLevel`，取敏感排名最高者；
3. 若该等级的排名高于安全地板（safety floor）的当前结果，则**提升**分类等级，设 `MatchedBy` 为 `"standard:<level>"`，`Confidence` 为固定 `0.50`。

这一设计确保即使引擎各层均未命中规则，系统仍然依据已加载的合规标准给出有意义的安全等级兜底，而非直接退化为最低等级。

---

## 11. 总结与架构质量指标

| 维度 | 架构指标 / 达标规范 | 关键保障技术 |
|---|---|---|
| **密码合规** | GB/T 39786-2021 密评三级 | 全栈纯 Go 国密 SM3/SM4-GCM，HKDF-SM3 v2 信封加密，UTC 纳秒级存证 |
| **密钥化完整性** | HMAC-SM3 密钥化存证链 | `SetAuditChainKey` 注入局方密钥，5 候选多轨平滑验真，`repairchain` 重签工具自动升级 |
| **存证吞吐** | 单节点 SQLite 3,000~5,000 QPS | `BufferedAuditStore` 单 Worker 串行微批聚合刷盘（10000 队列 / 200 批 / 20ms） |
| **存证完整性** | 100% 连续哈希链，零断链、零丢记录 | 优雅停机排空保障 + 单协程出队哈希绑定 + 重试积压按原序重投 |
| **留存红线** | 先归档后删除，零「删而未档」 | SM4-GCM+gzip NDJSON 加密段 + SM3 行哈希链 + 磁盘回读验真 + `ErrStoreUnsupported` 兜底 |
| **分布式调度** | 多节点并发 0 冲突、0 死锁 | PostgreSQL `FOR UPDATE SKIP LOCKED` 原子租约 + 令牌防脑裂 + `ErrLeaseNotSupported` 降级 |
| **上游容灾** | 99.99%，秒级探针平滑降级 | 按节点独立熔断 + Round-Robin 轮询 + 指数退避故障转移 + `EndpointStates` 诊断 |
| **安全门禁** | 配置缺失即启动失败 | 5 哨兵 Fail-Closed 校验 + 环回地址智能豁免 |
| **命名治理** | 全仓库零裸字符串字面量 | SSOT Registry + 四级归一化 + 别名冲突检测 + Observer 可观测 + L1~L5 安全分级词表 |
| **分类兜底** | 全量合规标准覆盖 | `LoadStandardsFromDir` + `highestStandardDefaultLevel` 漏斗安全网 |
| **代码工程质量** | 0 CGO 依赖，纯 Go 跨平台编译 | Go 1.25+ Multi-module 架构，测试覆盖率 > 90% |
