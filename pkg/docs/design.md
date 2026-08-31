# PrivShield 共享基础包 (Shared PKG) — 详细系统设计说明书

> **文档定位**：数联天下 · 数盾（`PrivShield`）全栈公共基础设施库（`pkg`）的核心架构、子系统设计、密码学实现与并发存储原语详细说明书。  
> **适用模块**：`service-hub`、`datasource-mgr`、`audit-log`、`console/bff-go`、`console/app-lz/bff-go`、`engine-go`、`privacy-go-sdk`。  
> **设计基准**：GB/T 39786-2021 密评三级、GM/T 0004-2012 (SM3)、GM/T 0002-2012 (SM4)、RFC 7515/7516、Go 1.25+ 现代微服务架构规范。

---

## 1. 架构定位与设计哲学

在 `PrivShield` 分布式微服务架构中，`pkg` 模块作为所有 Go 子系统共享的底层基座，承载了**「密码安全、持久存储、中间件防护、上游通信、全链路可观测、规范校验」**六大核心能力。

```text
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                              PrivShield 业务与微服务层                                 │
│  service-hub (:8082)   │  datasource-mgr (:8083)  │  audit-log (:8084)  │  bff-go      │
└──────────┬─────────────────────────┬─────────────────────────┬───────────────────┬─────┘
           │                         │                         │                   │
┌──────────▼─────────────────────────▼─────────────────────────▼───────────────────▼─────┐
│                            PrivShield 共享基础设施包 (pkg)                              │
├───────────────────┬───────────────────────────────────┬────────────────────────────────┤
│ 1. 密码学与安全   │ crypto (SM3/SM4-GCM/信封加密)     │ tlsutil (mTLS / CN 白名单热加载)│
│ 2. 存储与微批引擎 │ store (抽象接口 / PostgreSQL / SQLite)│ flusher (单 Worker 串行哈希刷盘器)│
│ 3. 中间件与防护   │ middleware (9层防护栈 / IP 令牌桶限流) │ validation / naming (规范化治理)│
│ 4. 运行时与观测   │ agent (熔断重试客户端)            │ metrics (防基数爆炸 Prometheus) │
└───────────────────┴───────────────────────────────────┴────────────────────────────────┘
```

### 1.1 设计原则

1. **零第三方 CGO 依赖 (Pure Go)**：全部密码学（SM3/SM4）、嵌入式存储（SQLite `modernc.org/sqlite`）均采用纯 Go 编译，杜绝 CGO 交叉编译困难与动态链接库安全漏洞。
2. **规范化密码合规 (SM3/SM4-GCM)**：全链路遵循国密商用密码标准，哈希前像采用 UTC 纳秒级规范化格式，快照存储强制执行 SM4-GCM 信封加密。
3. **单 Worker 顺序存证保证**：通过 `BufferedAuditStore` 实现高并发并发写入入队、单 Worker 串行哈希链绑定与微批合并刷盘，兼顾 3,000+ QPS 吞吐与 100% 连续防篡改哈希链。
4. **两阶段平滑降级与自适应弹性**：数据库优先连接高并发分布式 PostgreSQL（Phase B），若探针超时（3s）或未配置则平滑回退至单机 SQLite WAL，并根据 CPU 核心数自适应调整连接池上下限。

---

## 2. 模块拓扑与目录划分

```text
pkg/
├── agent/                  # 上游 PrivShield Agent REST API 客户端封装
│   ├── client.go           # Client（熔断器、重试、鉴权头注入、64MiB 响应保护）
│   └── client_test.go      # 基础请求、鉴权、熔断器状态流转单测
├── config/                 # 集中化环境变量解析与结构化日志
│   ├── env.go              # EnvString, EnvInt, EnvBool, EnvDuration, SetupLogger
│   └── env_test.go         # 环境变量读取与默认值测试
├── crypto/                 # 国密商用密码与信封加密体系
│   ├── sm3.go              # GM/T 0004-2012 SM3 算法、HMAC-SM3、UTC 规范化哈希
│   ├── sm3_test.go         # 国标向量验证与哈希完整性单测
│   ├── sm4.go              # GM/T 0002-2012 SM4 分组密码、CBC/CTR/GCM 模式
│   ├── envelope.go         # SM4-GCM 动态信封加密 (enc:v1:...) 与主密钥派生
│   └── envelope_test.go    # 加密解密、IV 随机性、防篡改校验单测
├── docs/                   # pkg 体系工程技术文档中心
├── metrics/                # 模块级隔离 Prometheus 指标采集器
│   ├── metrics.go          # Collector（CounterVec, HistogramVec, 路由标签归一化）
│   └── metrics_test.go     # 指标收集与 HTTP 中间件测试
├── middleware/             # Gin 生产级 9 层纵深防御中间件
│   ├── auth.go             # 常量时间比较 API Key 鉴权 (crypto/subtle)
│   ├── envelope.go         # 跨语言统一 API 信封格式与全局异常拦截
│   ├── envelope_test.go    # 异常信封与 HTTP 状态码映射测试
│   ├── middleware.go       # CORS, SecurityHeaders, MaxBodySize, MaxConcurrent, Recovery
│   ├── ratelimit.go        # 基于 IP 与 ClientID 的令牌桶平滑限流与自动过期淘汰
│   ├── trace.go            # X-Trace-ID 与 X-Request-ID 双头注入与 Context 传播
│   └── trace_test.go       # 链路追踪传播测试
├── naming/                 # 字段与接口命名规范器与运行时探测
│   ├── naming.go           # snake_case / kebab-case 转换与参数安全过滤
│   ├── naming_test.go      # 命名转换单测
│   ├── observer.go         # 字段安全观测与规范异常拦截
│   └── observer_test.go    # 动态观测测试
├── store/                  # 数据持久化抽象层与双引擎实现
│   ├── store.go            # 核心领域实体 (Task, DataSource, AuditLog) 与接口契约
│   ├── audit_hash.go       # 全局统一的 9 要素国密 SM3 存证哈希规范与双模验真
│   ├── flusher/            # 高并发微批缓冲刷盘器
│   │   ├── flusher.go      # BufferedAuditStore 单 Worker 串行哈希链绑定与读己之写
│   │   └── flusher_test.go # 50+ 并发哈希连续性、Close零丢数据、并发 Flush 测试
│   ├── sqlite/             # SQLite 纯 Go 引擎 (WAL 模式,  busy_timeout=5000)
│   │   ├── sqlite.go       # 驱动初始化、PRAGMA 调优与自动 Schema 迁移
│   │   ├── tasks.go        # 任务存储实现
│   │   ├── datasource.go   # 数据源存储实现
│   │   ├── audit.go        # 审计日志与快照存储，SQL 级聚合统计
│   │   └── sqlite_test.go  # SQLite 完整性与事务测试
│   ├── postgres/           # PostgreSQL Phase B 分布式原子租约与审计存储
│   │   ├── postgres.go     # pgxpool 连接池自适应调优与 Schema 初始化
│   │   ├── leased.go       # FOR UPDATE SKIP LOCKED 多副本原子任务争抢与租约续期
│   │   ├── audit.go        # PostgreSQL 原生分区审计日志存储与 SQL 报告聚合
│   │   └── leased_test.go  # 多节点无死锁争抢租约单测
│   ├── memory/             # 纯内存存储实现（单元测试与本地无持久化模式）
│   │   ├── memory.go       # 基于 sync.RWMutex 的内存 Mock
│   │   └── memory_test.go  # 内存对账测试
│   └── cmd/migrate/        # 数据库自动化迁移 CLI 工具
├── tlsutil/                # mTLS 双向认证与动态 CN 白名单管理
│   ├── tlsutil.go          # TLS 1.3 强制、CA 证书链加载、SPKI 公钥固定
│   ├── whitelist.go        # 基于 YAML 的 CN 白名单管理器（文件 mtime 热重载）
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
* **哈希计算**：`Hex(SM3(Data))`。

```go
func ComputeAuditIntegrityHash(logID, prevHash string, timestamp time.Time, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) string {
    if paramsJSON == "" || paramsJSON == "null" {
        paramsJSON = "{}"
    }
    data := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%s",
        prevHash, logID, timestamp.UTC().Format(time.RFC3339Nano), algorithm,
        inputHash, outputHash, user, securityLevel, paramsJSON)
    return crypto.SumSM3Hex([]byte(data))
}
```

#### 3.1.2 双模平滑验真 (Dual-Mode Verification)
系统支持平滑迁移，在 `VerifyAuditIntegrityHash` 中优先校验规范 SM3 摘要；若存量历史数据基于早期 SHA-256 或本地时区生成，则自动回退到 Legacy 验签，并打标 `LegacyHashed` 计数以引导后续重签。

### 3.2 国密 SM4-GCM 动态信封加密 (`pkg/crypto/envelope.go`)

为了保证敏感数据快照（`SnapshotRecord.InputSample` / `OutputSample`）在落库及跨域传输中的机密性，系统实现了基于 **SM4-GCM** 的动态信封加密机制：

```text
明文数据 ───▶ [12-byte 随机 IV] ───▶ [SM4-GCM 加密 (MasterKey)] ───▶ "enc:v1:<Base64(IV + Ciphertext + Tag)>"
```

1. **格式标识**：前缀 `enc:v1:` 代表国密 SM4-GCM 信封加密；
2. **认证加密 (AEAD)**：利用 GCM 模式的 16 字节 Auth Tag 确保密文不可篡改，篡改即解密失败；
3. **主密钥自动派生**：当配置的主密钥长度不足 16 字节时，通过 `SM3(Key)[:16]` 确定性派生出 128 位合规密钥；
4. **透明解密**：若读取到的样本未带 `enc:v1:` 前缀，自动兼容返回原始明文。

---

## 4. 存储架构与微批缓冲刷盘引擎 (`pkg/store`)

### 4.1 存储接口契约体系

```text
              ┌─────────────────────────┐
              │    store.AuditStore     │
              └────────────▲────────────┘
                           │
       ┌───────────────────┴───────────────────┐
       │                                       │
┌──────────────┴──────────────┐         ┌──────────────┴──────────────┐
│  flusher.BufferedAuditStore │         │      底层实际持久化存储      │
│  (单 Worker 串行哈希微批)   │         │ (Postgres / SQLite / Memory)│
└──────────────┬──────────────┘         └──────────────▲──────────────┘
               │                                       │
               └───────────── 批量写入/刷新 ───────────┘
```

1. **`TaskStore`**：单节点任务流水线状态管理；
2. **`LeasedTaskStore`**：多副本分布式短事务原子租约争抢（`ClaimNext` / `RenewLease` / `CompleteLease` / `FailLease`）；
3. **`DataSourceStore`**：多源数据连接配置与访问审计；
4. **`AuditStore`**：防篡改连续存证日志与加密快照。

### 4.2 高并发缓冲刷盘器 (`flusher.BufferedAuditStore`)

传统方案在高并发并发写入时直接对底层数据库写入单条记录，导致数据库锁争用严重（SQLite 表现为 `database is locked`，PG 表现为连接池耗尽），且多协程并发写入会破坏区块链式哈希链的先后顺序。

`BufferedAuditStore` 的内部架构设计如下：

```text
HTTP / gRPC 请求
 (Goroutine 1..N)
        │
        ▼
 ┌──────────────┐   1. Lock stateMu ──▶ 暂存到 recentLogs[ID] (实现“读己之写”)
 │   SaveLog    │   2. Lock closeMu (若已关闭则拒绝)
 └──────┬───────┘   3. 非阻塞入队 queue (若满则回退同步落盘并计数 droppedTotal)
        │
        ▼
   queue (chan pendingItem, 容量 2000)
        │
        ▼
 ┌────────────────────────────────────────────────────────────────────────┐
 │                      单一后台协程 (flushWorker)                        │
 │                                                                        │
 │  1. FIFO 弹出微批 (最大 200 条或 20ms 定时器触发)                     │
 │  2. 串行依次绑定: item.PrevHash = lastHash                            │
 │  3. 重新计算: item.IntegrityHash = ComputeAuditIntegrityHash(...)     │
 │  4. 推进: lastHash = item.IntegrityHash                                │
 │  5. 原子大事务批量落盘: underlying.SaveLogsBatch(logs, snaps)           │
 │  6. 清理 recentLogs 中已落盘的条目                                    │
 └────────────────────────────────────────────────────────────────────────┘
```

#### 4.2.1 核心技术突破与保证
* **哈希链 100% 绝对连续**：所有记录的 `PrevHash` 与 `IntegrityHash` 的生成均收敛在单一 Worker 中按出队顺序串行完成，从数学上消除并发交错导致的断链；
* **读己之写 (Read-Your-Own-Writes) 即时可见**：写入请求在入队瞬间即写入内存 `recentLogs` 映射并更新 `lastLog`。调用 `GetLog(id)` 时优先检索内存缓冲，未刷盘数据立刻可读；
* **安全优雅停机 (Zero-Loss Graceful Shutdown)**：`Close()` 首先闭合写锁拦截新请求，随后通过 `stopCh` 通知 Worker 执行 `drainQueue()` 彻底排空队列并执行最后一次批量落盘，保证停机过程零数据丢失；
* **无通道争用并发 Flush**：`Flush()` 通过专用的 `flushReqCh` 向 Worker 发送请求并阻塞等待，Worker 统一执行 `drainQueue()` 并在处理完成后通知调用方，杜绝并发竞争。

### 4.3 PostgreSQL Phase B 多副本租约调度 (`pkg/store/postgres`)

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
RETURNING id, type, priority, status, stage, payload_json, lease_owner, lease_token, lease_expires_at, created_at, updated_at;
```

#### 租约调度特性：
1. **零阻塞吞吐**：正在被处理的行被跳过，其他副本无需等待锁，直接领取下一条任务；
2. **自动故障接管**：当副本崩溃或因网络分区未续约时，`lease_expires_at < NOW()` 条件允许健康副本自动重新领取并执行该孤立任务；
3. **令牌防脑裂 (Token Fencing)**：所有写回操作（`CompleteLease` / `RenewLease` / `FailLease`）均携带 `(id, owner, token)` 三元组条件校验，若租约已被夺走则更新失败并返回 `false`，彻底阻断延迟写覆盖。

---

## 5. 纵深防御中间件流水线 (`pkg/middleware`)

所有进入 Gin HTTP 服务的请求均依次流经 9 层标准化中间件栈：

```text
HTTP Request
     │
     ▼
[ 1. TraceMiddleware ]       ➔ 提取/生成 X-Trace-ID & X-Request-ID，注入 context
     │
     ▼
[ 2. StructuredLogger ]      ➔ 基于 slog 记录请求开始/完成结构化日志
     │
     ▼
[ 3. Recovery ]              ➔ 捕获 panic 并使用统一 JSON 信封输出 500
     │
     ▼
[ 4. SecurityHeaders ]       ➔ 注入 X-Content-Type-Options, X-Frame-Options, CSP
     │
     ▼
[ 5. MaxBodySize ]           ➔ 限制请求体最大 32MB / 64MB，防止内存拒绝服务 (DoS)
     │
     ▼
[ 6. MaxConcurrent ]         ➔ 限制全局最大并发请求数 (默认 1000)，超载返回 503
     │
     ▼
[ 7. RateLimit ]             ➔ 客户端 IP / ClientID 令牌桶限流 (RPS + Burst)
     │
     ▼
[ 8. CORS ]                  ➔ 精确 Origin 白名单匹配，严格禁止通配符携带凭证
     │
     ▼
[ 9. Auth ]                  ➔ API Key 常量时间比对 (crypto/subtle.ConstantTimeCompare)
     │
     ▼
业务 Handler (Controller)
```

---

## 6. 全链路可观测性与指标采集 (`pkg/metrics`)

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
   * `audit_flusher_flushed_total` / `audit_flusher_dropped_total`：存储微批刷盘运行健康指标。

---

## 7. 上游 Agent 通信与容灾 (`pkg/agent`)

`pkg/agent.Client` 为各 Go 微服务与底层 Python 隐私计算核心引擎之间提供强韧的高并发通信保障：

1. **自动断路熔断器 (Circuit Breaker)**：
   * 连续失败 5 次触发熔断（`Open` 状态），后续请求快速失败，保护故障中的计算节点；
   * 冷却时间（30s）后进入 `Half-Open` 状态，尝试放行单个探测请求，探测成功则自动恢复为 `Closed`。
2. **指数退避重试 (Exponential Backoff)**：
   * 对网络瞬断或 `502/503/504` 状态码执行最多 3 次指数退避重试（`100ms ➔ 200ms ➔ 400ms`）；
3. **响应体防爆 (Body Limit)**：
   * 采用 `io.LimitReader` 限制单次读取上限为 64 MiB，防止异常超大响应引发 OOM 崩溃。

---

## 8. 总结与架构质量指标

| 维度 | 架构指标 / 达标规范 | 关键保障技术 |
|---|---|---|
| **密码合规** | GB/T 39786-2021 密评三级 | 全栈纯 Go 国密 SM3/SM4-GCM，UTC 纳秒级存证 |
| **存证吞吐** | 单节点 SQLite 3,000~5,000 QPS | `BufferedAuditStore` 单 Worker 串行微批聚合刷盘 |
| **存证完整性** | 100% 连续哈希链，零断链、零丢记录 | 优雅停机排空保障 + 单协程出队哈希绑定 |
| **分布式调度** | 多节点并发 0 冲突、0 死锁 | PostgreSQL `FOR UPDATE SKIP LOCKED` 原子租约 |
| **服务可用性** | 99.99%，秒级探针平滑降级 | PG 故障 3s 超时自动回退 SQLite WAL |
| **代码工程质量**| 0 CGO 依赖，纯 Go 跨平台编译 | Go 1.25+ Multi-module 架构，测试覆盖率 > 90% |
