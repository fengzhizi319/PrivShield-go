# PrivShield 共享基础包 (Shared PKG) — 高可用、容灾与数据一致性保障手册

> **文档定位**：深入阐述 `pkg` 模块在微批缓冲、数据存证、分布式租约争抢、多级降级、归档留存与密码学链路完整性等场景下的可靠性架构与实现机理。

---

## 目录

- [一、高可用与一致性设计总览](#一高可用与一致性设计总览)
- [二、微批缓冲刷盘与零丢失优雅停机 (`flusher.BufferedAuditStore`)](#二微批缓冲刷盘与零丢失优雅停机-flusherbufferedauditstore)
  - [2.1 单 Worker 串行哈希链一致性模型](#21-单-worker-串行哈希链一致性模型)
  - [2.2 读己之写 (Read-Your-Own-Writes) 内存分级缓存](#22-读己之写-read-your-own-writes-内存分级缓存)
  - [2.3 优雅停机与队列排空时序 (Drain on Close)](#23-优雅停机与队列排空时序-drain-on-close)
  - [2.4 归档读取能力扩展 (AuditArchiveReader)](#24-归档读取能力扩展-auditarchivereader)
  - [2.5 运行时可观测性与错误可见性](#25-运行时可观测性与错误可见性)
- [三、分布式任务调度防脑裂与租约自愈 (`store.LeasedTaskStore`)](#三分布式任务调度防脑裂与租约自愈-storeleasedtaskstore)
  - [3.1 行级短事务与无死锁争抢 (FOR UPDATE SKIP LOCKED)](#31-行级短事务与无死锁争抢-for-update-skip-locked)
  - [3.2 令牌屏障 (Token Fencing) 防延迟写覆盖](#32-令牌屏障-token-fencing-防延迟写覆盖)
  - [3.3 孤立任务自愈与租约超时回收](#33-孤立任务自愈与租约超时回收)
  - [3.4 后端能力边界：SQLite / Memory 不支持租约](#34-后端能力边界sqlite--memory-不支持租约)
- [四、存储引擎多级平滑降级架构](#四存储引擎多级平滑降级架构)
  - [4.1 PostgreSQL 3 秒探针超时与 SQLite 回退](#41-postgresql-3-秒探针超时与-sqlite-回退)
  - [4.2 严格存储模式 (STRICT_STORAGE) 与 SQLite 完整性校验](#42-严格存储模式-strict_storage-与-sqlite-完整性校验)
  - [4.3 队列满溢与背压丢弃保护](#43-队列满溢与背压丢弃保护)
  - [4.4 PostgreSQL 连接池环境变量覆盖](#44-postgresql-连接池环境变量覆盖)
  - [4.5 先归档后删除：存证留存红线 (Archive-Before-Delete)](#45-先归档后删除存证留存红线-archive-before-delete)
- [五、上游 Agent 按节点熔断与网络韧性](#五上游-agent-按节点熔断与网络韧性)
  - [5.1 按端点维度独立熔断器 (Per-Endpoint Circuit Breaker)](#51-按端点维度独立熔断器-per-endpoint-circuit-breaker)
  - [5.2 配置参数](#52-配置参数)
  - [5.3 轮询端点选取与重试故障转移](#53-轮询端点选取与重试故障转移)
  - [5.4 诊断与状态观测](#54-诊断与状态观测)
  - [5.5 哨兵错误分类](#55-哨兵错误分类)
  - [5.6 64 MiB 响应体防爆保护](#56-64-mib-响应体防爆保护)
- [六、哈希链密钥完整性与密钥轮换](#六哈希链密钥完整性与密钥轮换)
  - [6.1 密钥化 HMAC-SM3 存证哈希 (P1-2)](#61-密钥化-hmac-sm3-存证哈希-p1-2)
  - [6.2 运行时密钥注入 (SetAuditChainKey)](#62-运行时密钥注入-setauditchainkey)
  - [6.3 历史候选核验顺序](#63-历史候选核验顺序)
  - [6.4 密钥轮换与存量重签](#64-密钥轮换与存量重签)
- [七、归档段可靠性与独立验真](#七归档段可靠性与独立验真)
  - [7.1 归档段文件格式](#71-归档段文件格式)
  - [7.2 清单文件 (Manifest)](#72-清单文件-manifest)
  - [7.3 Fail-Closed：归档失败即拒绝删除](#73-fail-closed归档失败即拒绝删除)
  - [7.4 归档段回读验真 (VerifySegment)](#74-归档段回读验真-verifysegment)

---

## 一、高可用与一致性设计总览

`PrivShield` 作为一个承载政务云敏感数据流通的安全网关，其共享基础设施层必须保证在**节点突发崩溃、网络瞬断、并发洪峰与存储抖动**等极端场景下的系统韧性与数据完整性：

```text
┌────────────────────────────────────────────────────────────────────────────────────┐
│                           PrivShield 可靠性纵深防御矩阵                              │
├─────────────────┬──────────────────────────────────────────────────────────────────┤
│ 存证数据完整性  │ 单 Worker 串行哈希链 + 优雅停机排空 (RPO = 0, 零断链)              │
│ 密钥化防篡改    │ HMAC-SM3(key, "SM3-HMAC:v1|payload") + 多轨向下兼容核验            │
│ 内存读写一致性  │ 读己之写内存索引 + 刷盘后原子驱逐                                   │
│ 分布式任务调度  │ PostgreSQL FOR UPDATE SKIP LOCKED + 令牌屏障 (零脑裂)              │
│ 存储多级容灾    │ PG (3s探针) ➔ SQLite WAL (忙等待重试) ➔ 严格模式禁止静默降级        │
│ 存证留存红线    │ 先归档 (SM4-GCM+gzip+SM3行链) 后删除，归档失败即拒绝物理删除          │
│ 归档独立验真    │ 段文件 + 清单 + 密钥离线验真，不依赖数据库                           │
│ 上游通信韧性    │ 按节点维度独立熔断 + 指数退避重试 + 节点故障转移 + 64MiB 防爆        │
└─────────────────┴──────────────────────────────────────────────────────────────────┘
```

---

## 二、微批缓冲刷盘与零丢失优雅停机 (`flusher.BufferedAuditStore`)

### 2.1 单 Worker 串行哈希链一致性模型

在传统多协程直接写入数据库的架构中，并发协程获取前序哈希并写入时存在竞态条件，导致生成的哈希链在时序上出现断裂或分叉。

`BufferedAuditStore` 通过**将并发数据收集与哈希链顺序生成解耦**解决了该难题：

```text
[HTTP Handler 并发请求]
   │
   ├─► SaveLog (Log A) ─┐
   ├─► SaveLog (Log B) ─┼──► [ FIFO 内存通道 queue ]
   └─► SaveLog (Log C) ─┘          │
                                   ▼
                       [ 单一后台 flushWorker ]
                                   │
                     1. 依次出队: Log A ➔ Log B ➔ Log C
                     2. 串行绑定: Log A.PrevHash = lastHash
                                  lastHash = Hash(Log A)
                                  Log B.PrevHash = lastHash
                                  lastHash = Hash(Log B)
                     3. 批量底层落盘: underlying.SaveLogsBatch([A, B, C])
```

* **数学一致性保证**：由于哈希链的推进完全在单协程内线性执行，无论上层并发有多高，下层落盘的数据必然构成一条严格单调递增、首尾相连的防篡改哈希链。

### 2.2 读己之写 (Read-Your-Own-Writes) 内存分级缓存

当客户端在写入一条审计日志后立刻调用 `GetLog(id)` 查询时，若该日志仍在微批缓冲队列中尚未落盘，可能出现短暂的 `404 Not Found`。

为了达成单客户端线性一致性，`BufferedAuditStore` 引入了**暂存内存索引**：

1. **写入暂存**：`SaveLog` 时在写锁保护下将日志对象深拷贝至 `recentLogs[log.ID]` 并更新 `lastLog`；
2. **优先读内存**：`GetLog(id)` 与 `GetLatestLog()` 优先检索 `recentLogs`，命中直接返回；
3. **未命中查库**：未命中时穿透查询底层持久化存储；
4. **刷盘后驱逐**：微批落盘成功后，后台 Worker 自动清理 `recentLogs` 中已落库的条目，防止内存泄漏；
5. **有界淘汰**：暂存映射受 `MaxStaged` 约束（默认 50000），超限后按入队顺序淘汰最旧条目并递增 `evictedTotal` 计数。

### 2.3 优雅停机与队列排空时序 (Drain on Close)

在容器缩容、发布更新或主机维护收到 `SIGTERM` 信号时，必须确保缓冲区内所有未刷盘数据 100% 写入数据库：

```text
os.Signal (SIGTERM)
       │
       ▼
main.go: auditStore.Close()
       │
       ▼
 1. 获取 closeMu 写锁 ───► 将 isClosed 置为 true（后续 SaveLog 直接拒绝）
       │
       ▼
 2. 发送 stopCh 信号 ────► 唤醒 flushWorker
       │
       ▼
 3. 执行 drainQueue() ───► 循环读取 queue 中剩余的所有数据，合并为最终批次
       │
       ▼
 4. 执行底层 SaveLogsBatch ──► 确保全部落盘到 SQLite/PostgreSQL
       │
       ▼
 5. Worker 安全退出并关闭 underlying.Close()
```

若排空超时（默认 `CloseTimeout = 10s`），`Close()` 将返回错误并报告搁浅条数（`stranded = queueDepth + retryPending`），便于运维评估数据丢失风险。

### 2.4 归档读取能力扩展 (AuditArchiveReader)

`BufferedAuditStore` 实现了 `store.AuditArchiveReader` 接口，将「先归档后删除」的存证留存能力无缝穿透到缓冲层：

- **`FetchOldestForArchive(before, limit)`**：先执行 `Flush()` 同步屏障排空所有缓冲，再下沉到底层存储读取到期记录。若刷盘未完成或底层不具备归档读取能力，直接返回错误（fail-closed），避免「按不完整数据集归档后删除」。
- **`DeleteLogsByIDs(ids)`**：同步清除暂存区 `recentLogs` 中对应 ID 的记录后，下沉到底层存储精确删除。

### 2.5 运行时可观测性与错误可见性

`BufferedAuditStore` 提供以下监控方法，供 `/ops/diagnostics` 与 Prometheus 指标采集：

| 方法 | 说明 |
|---|---|
| `OverflowTotal()` | 因队列满溢或积压饱和而被拒绝的写入总条数 |
| `EvictedTotal()` | 因暂存映射有界淘汰而驱逐的记录总条数 |
| `StagedCount()` | 当前暂存在内存读己之写缓存中的记录条数 |
| `QueueDepth()` | 当前 FIFO 通道中待刷盘的记录条数 |
| `RetryPending()` | 底层提交失败后保留在工作线程重试积压区中的记录条数 |
| `FlushedTotal()` | 已成功刷盘到底层存储的累计记录条数 |
| `FailedTotal()` | 刷盘提交失败（含所有重试耗尽）的累计记录条数 |
| `HasFlushError()` | 当前是否存在未恢复的刷盘错误 |
| `LastFlushError()` | 最近一次刷盘错误的文本描述 |

**`ErrBacklogSaturated`**：当底层存储长期不可用导致重试积压区（`retryPending`）达到 `MaxStaged` 上限时，新写入将快速失败并返回此哨兵错误，防止无界内存增长引发 OOM。

---

## 三、分布式任务调度防脑裂与租约自愈 (`store.LeasedTaskStore`)

### 3.1 行级短事务与无死锁争抢 (FOR UPDATE SKIP LOCKED)

在多副本 `service-hub` 场景下，多个调度实例并发争抢待执行任务。系统采用 PostgreSQL `FOR UPDATE SKIP LOCKED` 特性：
* **无阻塞竞争**：事务仅锁定获取到的单行任务并跳过已被其他副本锁定的行，高并发下无锁排队等待；
* **极短事务周期**：仅在 `ClaimNext` 时锁定一行并更新为 `running`，随后立即提交释放行锁，任务实际执行过程（可能耗时数十秒）不在数据库长事务中。

### 3.2 令牌屏障 (Token Fencing) 防延迟写覆盖

```text
Hub 节点 1 领取任务 (Token: T1) ───[发生 GC 暂停/网络分区 35s]───► 尝试写回 CompleteLease(T1) ❌ 拒绝
                                                                             ▲
Hub 节点 2 接管任务 (Token: T2) ───[租约过期接管成功]───► 执行完成并提交 CompleteLease(T2) ✔ 成功
```

* 每次成功领取任务均生成全局唯一的随机 `lease_token`；
* 状态更新 SQL 包含硬性条件：`WHERE id = $1 AND lease_owner = $2 AND lease_token = $3 AND lease_expires_at >= NOW()`；
* 若节点因网络分区或假死导致租约被抢占，其携带的旧 Token 无法更新数据库，防止脑裂状态下的数据破坏。

### 3.3 孤立任务自愈与租约超时回收

1. **后台周期性回收**：`service-hub` 后台启动协程每 30 秒执行 `RequeueExpiredLeases(100)`，扫描 `status = 'running' AND lease_expires_at < NOW()` 的超时任务，自动重置为 `pending` 状态供其他健康节点抢占；
2. **进程崩溃启动恢复**：在服务启动时自动执行 `recoverOrphanedTasks`：
   * `pending` 状态任务直接保留在队列中继续等待调度；
   * `running` 状态任务标记为 `failed`（可能已部分执行，避免脏状态扩散）。

### 3.4 后端能力边界：SQLite / Memory 不支持租约

租约语义依赖 PostgreSQL `FOR UPDATE SKIP LOCKED` 的行级锁特性。SQLite（单写者模型）与内存存储无法提供等价的多副本并发抢占能力，因此：

- `store.ErrLeaseNotSupported`：当调用方对 SQLite 或内存后端执行 `ClaimNext` / `RenewLease` / `CompleteLease` / `FailLease` / `RequeueExpiredLeases` 等租约操作时，返回此哨兵错误；
- 多副本 Phase B 部署**必须**使用 PostgreSQL 后端；单实例开发环境可使用 SQLite/内存但不具备租约能力。

---

## 四、存储引擎多级平滑降级架构

### 4.1 PostgreSQL 3 秒探针超时与 SQLite 回退

在服务启动阶段，系统执行非阻塞探测：
```text
启动初始化
   │
   ├─► 配置了 PG_DSN? ───► 是 ───► 执行 3s 短超时 Ping 探测 ───► 成功 ───► 启用 PostgreSQL Phase B
   │                                           │ (超时/失败)
   │                                           ▼
   │                              StrictStorage? ──► 是 ──► 启动失败退出（禁止静默降级）
   │                                           │ (否)
   │                                           ▼
   │                              记录告警日志并平滑降级
   │                                           │
   ▼                                           ▼
配置了 DB_PATH? ────► 是 ──────────────────────┴───────────────► SQLite 完整性校验
   │                                                               │
   ▼ (未配置 DB_PATH)                                    校验通过 ──► 启用 SQLite WAL
StrictStorage? ──► 是 ──► 启动失败退出                               │
                 │ (否)                                    校验失败 ──► StrictStorage? ──► 是 ──► FATAL 退出
                 ▼                                                                   │ (否)
          启用 Memory 内存模式 (本地测试/无状态)                              记录错误但继续启动
```

### 4.2 严格存储模式 (STRICT_STORAGE) 与 SQLite 完整性校验

**`AUDIT_LOG_STRICT_STORAGE` 默认为 `true`**（非 `false`），即默认禁止静默降级回退：

- 当配置的 PostgreSQL 连接探测失败时，**直接报错退出**而非悄悄回退到 SQLite/内存；
- 当 SQLite 文件打开或初始化失败时，**直接报错退出**而非回退到内存模式；
- 当未配置任何持久化存储时，**直接报错退出**。

**SQLite `PRAGMA integrity_check` 失败为致命错误 (FATAL)**：

- 服务启动时调用 `sqlite.ValidateIntegrity(dbPath)` 执行完整性校验；
- 若校验返回非 `ok`（数据库损坏），在严格模式下**直接 `log.Fatalf` 终止进程**，不会自动重建或覆盖损坏的数据库文件；
- 非严格模式下（已废弃）：记录错误日志后仍继续启动，SQLite 可能在损坏数据上运行并产生不一致的哈希链。

### 4.3 队列满溢与背压丢弃保护

当底层存储发生严重 I/O 故障导致刷盘速率远低于写入速率时，内存通道 `queue` 可能满溢：
* 系统采用有界等待 `EnqueueTimeout`（默认 500ms）探测队列容量，维护严格 FIFO 保序；
* 若在超时时间内仍无法入队，触发写入拒绝并递增 `overflowTotal` 指标；
* 底层提交失败时，整批保留在工作线程暂存区（retry backlog）并在下一轮**按原序优先重投**，绝不丢弃已确认（acked）的记录；
* 当重试积压区达到 `MaxStaged`（默认 50000）上限时，`ErrBacklogSaturated` 快速拒绝新写入。

### 4.4 PostgreSQL 连接池环境变量覆盖

PostgreSQL 连接池默认采用 cgroup 感知的自适应计算（`effectiveNumCPU * 4` 为 MaxConns，范围 `[10, 100]`；`effectiveNumCPU` 为 MinConns，范围 `[2, 20]`）。生产环境可通过以下环境变量显式覆盖：

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `AUDIT_LOG_PG_MAX_CONNS` | `0`（自适应） | 连接池最大连接数覆盖（0 = 使用自适应值） |
| `AUDIT_LOG_PG_MIN_CONNS` | `0`（自适应） | 连接池最小常驻连接数覆盖（0 = 使用自适应值） |

连接池固定配置：30 秒健康检查周期（`HealthCheckPeriod`）、30 分钟最大连接生命周期（`MaxConnLifetime`）、5 分钟空闲回收（`MaxConnIdleTime`）。

### 4.5 先归档后删除：存证留存红线 (Archive-Before-Delete)

当启用数据留存清理（`AUDIT_LOG_RETENTION_DAYS > 0`）时，系统**必须**先完成归档再执行物理删除，这是数安法与等保三级的强制要求：

```text
定时清理协程 (每 6 小时)
       │
       ▼
 1. FetchOldestForArchive(cutoff, pageSize) ──► 取出到期存证（旧→新）
       │
       ▼
 2. 写入归档段 ──► SM4-GCM(gzip(NDJSON)) + SM3 行哈希链
       │
       ▼
 3. VerifySegment ──► 回读段文件，校验行哈希链、条数、边界、完整性哈希
       │
       ├─► 归档或验真失败 ──► 立即中止，拒绝删除（fail-closed）
       │
       ▼
 4. DeleteLogsByIDs(ids) ──► 按该段归档的 ID 精确物理删除
       │
       ▼
 5. 循环下一段，直到无更多到期记录
```

**关键约束**：
- 归档失败 = 删除拒绝（fail-closed），存证证据不会静默丢失；
- 归档加密密钥（`AUDIT_LOG_ENCRYPTION_KEY`）为必填项，缺失则归档器拒绝启动；
- 留存天数不得低于 1095 天（三年），否则配置校验直接拒绝启动；
- 默认 `RetentionDays = 0` 表示永不物理删除，存证始终保留在库内。

---

## 五、上游 Agent 按节点熔断与网络韧性

### 5.1 按端点维度独立熔断器 (Per-Endpoint Circuit Breaker)

`pkg/agent.Client` 实现了**按上游节点维度独立维护的三态熔断器**，而非单一客户端级熔断器。单节点故障只熔断该节点流量，其余健康节点继续承接请求，杜绝「一台宕机、全集群停摆」：

```text
               ┌──────────────────────────────────────┐
               │              [ Closed ]              │
               │   正常放行全部请求，连续失败计数     │
               └──────┬────────────────────────▲──────┘
                      │ (连续失败 5 次)        │ (探测成功 1 次)
                      ▼                        │
               ┌──────────────┐         ┌──────┴───────┐
               │    [ Open ]  │ 30s 冷却│ [ Half-Open ]│
               │ 快速拒绝请求 ├────────►│ 放行探测请求 │
               └──────────────┘         └──────────────┘
```

每个上游节点独立维护一套 Closed → Open → Half-Open 状态机，互不影响。

### 5.2 配置参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `BaseURLs` | `[]string{}` | 上游 Agent 集群地址列表（如 `["http://10.0.0.1:8079", "http://10.0.0.2:8079"]`） |
| `CBThreshold` | `5` | 触发单节点熔断的连续失败次数阈值 |
| `CBCooldown` | `30s` | 熔断开启后的冷却等待时间，冷却后转为 Half-Open 探测 |
| `MaxRetries` | `3` | 可重试错误的最大重试次数（0 = 不重试） |
| `RetryBaseDelay` | `500ms` | 指数退避重试的基础时间间隔 |
| `StateObserver` | `nil` | 熔断器状态流转时的外部回调钩子（入参为 `node` 与 `state` 字符串），用于上报 Prometheus 指标 |

### 5.3 轮询端点选取与重试故障转移

**`PickEndpoint()`** 基于无锁原子 Round-Robin 轮询调度选取健康节点：

1. 原子递增轮询序号，沿环形顺序找到第一个「自身熔断器未开启」的节点返回；
2. 全部节点均处于 Open 冷却期时返回熔断错误（fail-fast）；
3. 重试时通过 `pickEndpoint(exclude)` 避开刚失败的节点，优先故障转移到其他健康节点。

**重试与故障转移时序**：
```text
请求 ──► pickEndpoint("") ──► 节点 A
              │
              ▼ 节点 A 超时/5xx ──► recordFailure(A)
              │
              ▼ retryEndpoint(A) ──► pickEndpoint(exclude=A) ──► 节点 B
              │
              ▼ 指数退避等待: baseDelay * 2^(attempt-1) + jitter
              │
              ▼ 节点 B 成功 ──► recordSuccess(B) ──► 返回结果
```

### 5.4 诊断与状态观测

**`EndpointStates()`** 返回各上游节点独立的熔断状态快照（`map[string]string`），供 `/ops/diagnostics` 与告警系统定位「究竟是哪台节点被熔断」：

```json
{
  "http://10.0.0.1:8079": "closed",
  "http://10.0.0.2:8079": "open",
  "http://10.0.0.3:8079": "half_open"
}
```

**`StateObserver` 回调**：在熔断器状态发生流转时（Closed → Open / Open → Half-Open / Half-Open → Closed 等），调用注册的观察者回调函数，入参为归一化节点地址与状态字符串（`"closed"` / `"open"` / `"half_open"`），常用于上报 Prometheus 熔断指标。

### 5.5 哨兵错误分类

所有可被上层据以决策的错误都以哨兵错误或结构化错误类型表达，供调用方使用 `errors.Is` / `errors.As` 判定：

| 哨兵错误 | 语义 | 重试策略 |
|---|---|---|
| `ErrEndpointUnavailable` | 集群中没有任何可承接流量的节点（配置类故障） | 不重试 |
| `ErrCircuitOpen` | 目标节点熔断器处于 Open 冷却期，请求被快速拒绝 | 不重试（同请求内） |
| `ErrTransport` | 真实出站 I/O 故障（建连/TLS/超时/EOF/连接重置等） | 可重试 + 换节点 |

传输故障通过 `transportError` 结构化包装，`Unwrap()` 透出根因（如 `context.DeadlineExceeded`、`syscall.ECONNREFUSED`），`Is(ErrTransport)` 恒为真。4xx 客户端错误直接透传，不计入熔断失败计数。

### 5.6 64 MiB 响应体防爆保护

采用 `io.LimitReader` 严格限制单次 Agent 响应最大读取 64 MiB（`64 << 20`），超出直接报错并计入失败，防御异常返回超大样本数据导致 OOM。

---

## 六、哈希链密钥完整性与密钥轮换

### 6.1 密钥化 HMAC-SM3 存证哈希 (P1-2)

当前写入口径依据密钥配置状态分为两种模式：

- **密钥化模式（生产必须）**：配置 `AUDIT_LOG_HASH_KEY`（局方托管密钥）后，新写入存证采用 `HMAC-SM3(key, "SM3-HMAC:v1|" + 9要素前映像)`，算法标签为 `AuditHashSM3HMAC = "SM3-HMAC:v1"`。未持有密钥者无法伪造或改写记录；
- **无密钥模式（仅兼容存量）**：未配置密钥时退回无密钥 SM3 摘要（`AuditHashSM3 = "SM3"`），仅可证明「内容未被修改」，不能阻止知道口径者重算。

9 要素前映像结构：`prev_hash|log_id|timestamp_utc|algorithm|input_hash|output_hash|user|security_level|params_json`

时间戳统一归一化为 UTC RFC3339Nano 格式，彻底杜绝 PostgreSQL TIMESTAMPTZ 类型丢失写入方本地时区偏移导致的伪分叉。

### 6.2 运行时密钥注入 (SetAuditChainKey)

`store.SetAuditChainKey(key)` 通过原子指针操作注入进程级 HMAC 密钥：

- 在进程启动阶段由 `main.go` 调用一次，传入 `cfg.HashKey`；
- 传空串退回无密钥 SM3 口径；
- 运行期改钥会导致既有记录核验失败，因此**只在启动时注入一次**；
- 密钥通过 `atomic.Pointer[string]` 存储，读取无锁。

### 6.3 历史候选核验顺序

`VerifyAuditIntegrityHash` 依次尝试以下候选前映像，确保各历史阶段的存证均可合法验真：

| 优先级 | 候选算法 | 条件 | 说明 |
|---|---|---|---|
| 1 | `SM3-HMAC:v1` (HMAC-SM3) | 配置了密钥时 | 当前写入口径，密钥化防篡改 |
| 2 | `SM3` (无密钥 SM3-UTC) | 始终尝试 | 密钥化前的规范口径 |
| 3 | `SHA256-LEGACY` | 始终尝试 | 升级国密前的 SHA-256-UTC 旧版 |
| 4 | `SM3-LEGACY` | LocalTZ ≠ UTC 时 | SM3 使用本地时区的历史变体 |
| 5 | `SHA256-LEGACY` | LocalTZ ≠ UTC 时 | SHA-256 使用本地时区的历史变体 |

匹配时使用 `hmac.Equal` 常量时间比较，防止时序侧信道攻击。命中非当前写入口径的候选时，核验结果标记为 `legacy_hashed`（已验真但待重签）。

### 6.4 密钥轮换与存量重签

密钥轮换策略：
- **旧密钥写入的行**：在核验时仍可通过 HMAC-SM3 候选命中验真（因为核验使用的是当前注入的密钥）；
- **新密钥生效后的新行**：使用新密钥计算 HMAC-SM3；
- **存量升级**：`repairchain` 工具遍历全部记录，将仅命中无密钥 SM3 的存量记录重签为 HMAC-SM3 口径。`IsCanonicalHashLabel(label)` 判定某条记录的算法标签是否与当前写入口径一致，不一致即计入「待重签」工作量。

核验结论通过 `ChainVerificationResult.Reason` 机器可读枚举表达：
- `ok`：全链按当前口径验真通过（唯一完全可信取值）；
- `legacy_hashed`：链连续且内容验真，但至少一条记录仅命中历史候选（待重签，非篡改）；
- `tampered_payload` / `hash_mismatch` / `broken_chain` / `missing_prev` / `missing_records`：均为无效状态。

---

## 七、归档段可靠性与独立验真

### 7.1 归档段文件格式

每次归档运行产出一组「段文件 + 清单文件」：

```text
audit-archive-<cutoff>-<seq>.ndjson.gz.enc   SM4-GCM(gzip(NDJSON 记录行))
audit-archive-<cutoff>-<seq>.manifest.json   段元数据与行哈希链尾值
```

**段文件内部结构**：
1. 每条审计日志与关联快照按 NDJSON 格式逐行写入（日志行 kind="log"，快照行 kind="snapshot"）；
2. 每行 JSON 序列化后计算 SM3 行哈希链：`chain[i] = SM3(chain[i-1] || line[i])`；
3. 全部 NDJSON 行经 gzip 压缩；
4. 压缩后数据经 SM4-GCM 加密（密钥为 `AUDIT_LOG_ENCRYPTION_KEY`），算法标识为 `SM4-GCM/HKDF-SM3(enc:v2)`；
5. 密文写入 `.ndjson.gz.enc` 段文件。

**安全约束**：
- 段文件名绝不覆盖既有归档证据（通过 `O_EXCL` 保证）；
- 文件权限 `0o600`，仅属主可读写；
- 写入完成后执行 `fsync` 确保落盘；
- 归档目录路径穿越防护（`resolveInDir` 拒绝任何越出目录的路径片段）。

### 7.2 清单文件 (Manifest)

清单文件 `.manifest.json` 记录归档段的完整元数据，是独立验真的必要凭据：

```json
{
  "version": "privshield-audit-archive/v1",
  "chain_algo": "SM3-LINE-CHAIN:v1",
  "encryption": "SM4-GCM/HKDF-SM3(enc:v2)",
  "segment_file": "audit-archive-20250101T000000Z-000001.ndjson.gz.enc",
  "created_at": "2025-01-01T00:00:00Z",
  "cutoff": "2024-01-01T00:00:00Z",
  "log_count": 500,
  "snapshot_count": 120,
  "first_log_id": "...",
  "last_log_id": "...",
  "first_timestamp": "...",
  "last_timestamp": "...",
  "chain_tail": "sm3-hex-of-final-chain-link"
}
```

清单包含：格式版本、行链算法标识、加密口径标识、段文件名、创建时间、截止时间、日志/快照条数、首末日志 ID 与时间戳、行哈希链尾值。

### 7.3 Fail-Closed：归档失败即拒绝删除

归档留存流程的任何环节失败都**立即中止**，不再继续后续段落的处理：

| 失败环节 | 行为 |
|---|---|
| 未配置归档加密密钥 | 归档器拒绝创建（`ErrMissingKey`） |
| 未配置归档目录 | 归档器拒绝创建（`ErrMissingDir`） |
| 底层存储不具备归档读取能力 | 返回 `ErrStoreUnsupported`，拒绝删除 |
| 归档段写入失败 | 返回错误，删除拒绝（`archive segment failed, deletion refused`） |
| 段文件回读验真失败 | 返回错误，删除拒绝（`archive segment verification failed, deletion refused`） |
| 删除操作本身失败 | 返回错误，中止后续段落（避免重复归档） |
| 归档成功但删除条数为 0 | 返回 `ErrNotDeleted`，中止后续段落 |

### 7.4 归档段回读验真 (VerifySegment)

`VerifySegment(dir, segment, key)` 仅凭段文件、清单与密钥即可独立验真归档证据的完整性，**不需要访问数据库**：

1. **读取清单**：解析 `.manifest.json`，校验版本标识（`privshield-audit-archive/v1`）；
2. **解密解压**：读取段文件 → SM4-GCM 解密 → gzip 解压 → 恢复 NDJSON 行流；
3. **重算行哈希链**：逐行读取 NDJSON，按 `chain = SM3(chain || line)` 重算行哈希链；
4. **校验链尾**：重算链尾与清单中 `chain_tail` 比对，不一致说明归档证据被增删改或重排序；
5. **校验条数**：实际日志/快照条数与清单记录比对；
6. **校验边界**：首末日志 ID 与时间戳与清单记录比对；
7. **逐条完整性核验**：对每条日志按 9 要素重算自身 `integrity_hash`，与存证链式口径一致。

任一步骤失败即返回错误，表明归档证据已被篡改、截断或重排序。

---

## 八、中台基础包高内聚与参数驱动可靠性保障

### 8.1 机制与策略完全解耦

作为供所有微服务与外部业务系统复用的中台基础库，`pkg/` 严禁侵入特定业务的环境变量名称（例如不得在 `pkg/middleware` 中写死 `"PRIVACY_ALLOWED_CIDRS"` 或在 `pkg/tlsutil` 中写死 `"PRIVACY_TLCP_*"`）。

硬编码特定变量会导致严重的可靠性缺陷：
- **命名污染与配置踩踏**：网关需要配置对外公网 CIDR 白名单，内部 Agent 需要配置内网 VPC CIDR 白名单，若共用硬编码变量则无法实现差异化安全策略；
- **隐式回退导致配置漂移**：当开发者误以为生效的是服务专属配置时，中台包的隐式兜底会导致排障极度困难。

### 8.2 单一职责与参数显式驱动 (Direct Parameter-Driven)

中台包统一通过入参显式接收环境变量名（如 `envKey string` 或 `prefix string`），不进行隐式次级回退。调用方在业务入口显式指定其唯一变量名：

```text
调用方显式传入专属变量:
"GATEWAY_ALLOWED_CIDRS"
        │
        ├─► 命中且非空 ────► 采用网关专属策略
        │
        └─► 未配置/空 ─────► 安全返回 nil (零侵入放行)
```

### 8.3 零配置默认安全 (Secure by Default)

在任何入参为空或环境变量未配置的情况下，中台基础包均确保系统处于确定性的安全默认状态：
- `AllowedCIDRsFromEnv()`：无传参或全空时返回 `nil`，交由外部路由逻辑显式判断；
- `TrustedProxiesFromEnv()`：无传参或全空时返回 `nil`，Gin 默认不信任任何代理，彻底阻断 `X-Forwarded-For` 伪造；
- `IsTLCPEnabled()`：无传参或全空时返回 `false`，绝不静默尝试加载不存在的国密证书；
- `RegisterKeyVersionsFromEnv()`：未传前缀时直接返回 `0`，杜绝使用未知版本密钥加密。

