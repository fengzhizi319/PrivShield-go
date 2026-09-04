# service-hub 可靠性能力说明

> 数据服务调度中枢（service-hub）的崩溃恢复、自动重试、待处理任务消费、下游弹性熔断、幂等防护、完整性校验与备份能力详解。

---

## 1. 能力总览

| 能力维度 | 支持状态 | 实现方式 |
|---|---|---|
| HTTP TLS/mTLS 双向认证 | ✅ | 与 gRPC 共享证书配置，TLS 1.3 强制最低版本，支持 require/verify/request 客户端认证模式 |
| gRPC TLS/mTLS 双向认证 | ✅ | TLS 1.3 + 多客户端认证模式 + 公钥固定（SPKI Pinning） |
| 崩溃恢复（孤立任务回收） | ✅ | 启动时区分 pending（保留队列）/ running（标记 failed）任务 |
| 失败任务自动重试 | ✅ | 启动时 + 周期性后台重试，结构化 RetryCount 字段，指数退避延迟 |
| SQLite/内存待处理任务消费引擎 | ✅ | 本地 Worker 协程每 500ms 轮询消费 pending 任务（校验 RetryAfter 退避），解决单机模式崩溃恢复与重试任务积压 |
| PostgreSQL 分布式原子租约 | ✅ | 多副本 Hub 基于 `FOR UPDATE SKIP LOCKED` 原子领取（ClaimNext）、租约续期与到期自动回收 |
| Agent 客户端熔断器 | ✅ | 按节点维度独立三态熔断（Closed→Open→HalfOpen），单节点故障仅熔断该节点，连续 5 次失败触发，30s 冷却后半开探测 |
| Agent 客户端指数退避重试 | ✅ | 最多 3 次重试，500ms 基础延迟 + 随机抖动，5xx/网络错误可重试、4xx 不重试 |
| Agent 幂等凭据透传 | ✅ | 自动在请求上下文注入 `X-Idempotency-Key`（`hub-<task_id>-<stage>-<retry_count>`），防御重试导致 DP 预算重复扣减 |
| Datasource 客户端熔断与重试 | ✅ | 三态熔断器 + 3 次指数退避重试 + HTTP/gRPC 双协议 `X-Request-ID` 链路追踪透传 |
| 多层 Panic 恢复 | ✅ | HTTP 中间件 + gRPC 拦截器 + 异步任务协程三层 panic 安全网 |
| HTTP 服务器超时防护 | ✅ | ReadHeaderTimeout(5s)/ReadTimeout(30s)/WriteTimeout(60s)/MaxHeaderBytes(1MiB) |
| gRPC Keepalive 保活 | ✅ | MaxConnectionAge(2h)/Time(30s)/Timeout(10s) + EnforcementPolicy |
| 并发信号量限流 | ✅ | HTTP + gRPC 各 10 并发异步任务信号量 |
| Per-IP 令牌桶限流 | ✅ | 可配置 RPS/Burst，自动清理 10min 不活动 IP 桶，健康端点豁免 |
| 安全响应头 | ✅ | X-Content-Type-Options / X-Frame-Options / HSTS / Referrer-Policy / Permissions-Policy |
| 请求体大小限制 | ✅ | HTTP 中间件 32 MiB + Agent/Datasource 响应体 64 MiB 双重防护 |
| 全链路分布式追踪 | ✅ | TraceMiddleware 中间件注入 X-Request-ID + X-Trace-ID 双头 → Context 全链路传播（异步协程不丢上下文） → 下游客户端透传 |
| Prometheus 可观测性 | ✅ | 7 项指标：HTTP/gRPC QPS+延迟、崩溃恢复、重试、熔断器状态、租约指标 |
| SQLite 完整性校验 | ✅ | `PRAGMA integrity_check` 启动时阻断损坏数据库 |
| 数据库备份 | ✅ | 支持全量/增量备份、`--verify` 恢复验证模式、自动过期清理 |
| 数据保留清理 | ✅ | 每 6h 清理超过 RetentionDays 的终态任务，防止 SQLite 膨胀 |
| 优雅停机 | ✅ | SIGINT/SIGTERM → 停止后台协程 → 异步任务取消 → gRPC(30s 超时) → HTTP 顺序关闭 |
| 配置校验（Fail-Fast） | ✅ | TLS 启用时校验证书文件存在且可读，启动早期快速失败 |
| 存储持久化 | ✅ | SQLite WAL 模式，支持内存回退与 PostgreSQL 多副本租约 |

---

## 2. 崩溃恢复（Crash Recovery）

### 2.1 问题场景

当服务突然崩溃（`kill -9`、OOM Kill、断电）时，优雅停机代码不会执行，导致：
- **running 状态任务**：正在执行的任务卡在 "running" 状态，永远不会完成；
- **pending 状态任务**：已接收但未执行的任务停留在 "pending" 队列。

### 2.2 恢复机制

服务启动时，`recoverOrphanedTasks()` 函数自动执行以下操作：

```
启动 → 初始化 SQLite/PG → 扫描 running 任务 → 标记为 failed（记录日志 + Prometheus 指标）
                       → 扫描 pending 任务 → 保留在队列中 → 由 Worker 协程重新领取调度
```

**处理流程：**

1. **扫描 running 任务**：调用 `taskStore.List(TaskFilter{Status: "running"})` 获取所有运行中任务（上限 10000 条）；
2. **标记为 failed**：设置 `Status = "failed"`，`Error = "server crashed or restarted (recovered on startup)"`，记录 `CompletedAt` 和 `DurationMs`；
3. **扫描 pending 任务**：获取所有 pending 任务，**直接保留在队列中**（它们尚未执行，无需标记失败）；
4. **日志输出**：恢复任务数量 > 0 时输出 WARN 级别日志，包含 running/pending 分类计数；
5. **Prometheus 指标**：通过 `orphaned_tasks_recovered_total{type="running|pending"}` 记录恢复数量。

### 2.3 核心代码

```go
// services/service-hub/cmd/server/main.go → recoverOrphanedTasks()

// 1. 扫描所有 "running" 状态的任务 → 标记为 failed（可能已部分执行）
runningTasks, _, _ := taskStore.List(store.TaskFilter{Status: "running", Limit: 10000})
for i := range runningTasks {
    runningTasks[i].Status = "failed"
    runningTasks[i].Error = "server crashed or restarted (recovered on startup)"
    now := time.Now()
    runningTasks[i].CompletedAt = &now
    runningTasks[i].DurationMs = now.Sub(runningTasks[i].CreatedAt).Milliseconds()
    _ = taskStore.Update(&runningTasks[i])
    mc.RecordOrphanedRecovery("running")  // Prometheus 指标
}

// 2. 扫描所有 "pending" 状态的任务 → 直接保留在队列中（尚未执行，无需标记失败）
pendingTasks, _, _ := taskStore.List(store.TaskFilter{Status: "pending", Limit: 10000})
for range pendingTasks {
    mc.RecordOrphanedRecovery("pending")
}
```

---

## 3. 失败任务自动重试（Automatic Task Retry）

### 3.1 重试策略

服务启动时 + **周期性后台循环**（每 60 秒），`retryFailedTasks()` 自动扫描因临时错误而失败的任务并重新排队：

重试扫描通过 `taskStore.List(store.TaskFilter{Status: "failed", Limit: 100})` 读取当前批次的失败任务。在 SQLite 模式下，这一步等价于按 `created_at DESC` 查询 `tasks` 表中 `status = 'failed'` 的记录；读取结果会还原为 `store.Task`，其中包括 `Error`、`RetryCount` 和 `RetryAfter`。函数先以小写化后的 `Error` 与可重试关键字匹配，再检查 `RetryCount < 3`，并确认 `RetryAfter` 为空或已到达。错误不可重试、重试次数耗尽，或退避时间尚未到达时，函数不会改写该任务记录，任务继续保持原有的 `failed` 状态。

当任务满足重试条件时，服务不会在 `retryFailedTasks()` 内直接同步重新执行六阶段流水线，而是将该任务恢复为待调度状态：`Status` 从 `failed` 改为 `pending`，`Stage` 改为 `queued`；`StartedAt` 和 `CompletedAt` 设为 `nil`，`DurationMs` 重置为 `0`；`Error` 改写为 `retrying (attempt N/3)`，以便查询接口和运维人员识别其当前处于第几次重试；`RetryCount` 加 `1`。同时根据重试前的计数计算下一次可重新扫描时间：`RetryAfter = now + 5s * 2^旧RetryCount`，因此前三次重试后的退避窗口依次为 5 秒、10 秒和 20 秒。任务在后续调度路径中被领取时，才会从 `pending` 写为 `running` 并重新执行。

每次状态改写都通过 `taskStore.Update(&task)` 落库；只有这个调用成功，代码才增加 `tasks_retried_total{result="queued"}` 指标并记录“queued for retry”日志。调用失败时仅记录错误日志，不计入已入队指标，因此内存对象中的临时修改不会被误报为持久化成功。SQLite `TaskStore.Update` 使用参数化的单条 `UPDATE tasks SET ... WHERE id = ?` 语句，按任务 ID 原子写入 `status`、`stage`、`started_at`、`completed_at`、`duration_ms`、`error`、`retry_count` 和 `retry_after`。空的开始或结束时间会以 SQL `NULL` 写入；非空时间以 RFC3339Nano 格式保存。

**可重试的错误类型：**

| 错误模式 | 匹配关键字 | 说明 |
|---|---|---|
| 网络超时 | `timeout` | 下游服务响应超时 |
| 连接拒绝 | `connection refused` | 下游服务未启动或端口未监听 |
| 临时故障 | `temporary failure` | DNS 解析失败等临时错误 |
| 网络不可达 | `network unreachable` | 路由不可达 |
| 上下文超时 | `context deadline exceeded` | gRPC 上下文超时 |
| 崩溃恢复任务 | `server crashed or restarted` | 崩溃恢复标记的任务 |

### 3.2 重试限制

- **最大重试次数**：3 次（通过结构化 `RetryCount` 字段精确跟踪，替代脆弱的字符串匹配）；
- **指数退避延迟**：`5s × 2^RetryCount`（即 5s → 10s → 20s），通过 `RetryAfter` 字段控制最早重试时间；
- **超限处理**：超过最大重试次数的任务保持 `failed` 状态，输出 WARN 日志，Prometheus 指标 `tasks_retried_total{result="exhausted"}` 递增；
- **状态重置**：重试时重置 `Status = "pending"`、`Stage = "queued"`，清空 `StartedAt`/`CompletedAt`/`DurationMs`；
- **周期性后台重试**：`periodicRetryLoop()` 协程每 60 秒扫描一次，解决“运行时失败的任务必须等到下次重启”的问题；
- **停机清理**：优雅停机时通过 `retryCancel()` 取消后台重试协程。

---

## 4. 待处理任务消费引擎（Dual-Mode Task Workers）

针对不同存储后端的架构差异，service-hub 实现了双模态待处理任务消费机制，彻底解决崩溃恢复和重试任务在队列中积压无法拉起的问题：

```
                    ┌───────────────────────────────┐
                    │       service-hub 启动         │
                    └──────────────┬────────────────┘
                                   │
                    ┌──────────────┴──────────────┐
                    │ PGDSN 是否非空？             │
                    └───┬─────────────────────┬───┘
                        │ 是                  │ 否
                        ▼                     ▼
        ┌───────────────────────────┐ ┌───────────────────────────┐
        │  PostgreSQL Leased Worker │ │ SQLite / Memory Local     │
        │  - ClaimNext (SKIP LOCKED)│ │  - StartLocalWorker       │
        │  - 租约续期 (RenewLease)    │ │  - 500ms 轮询扫描 pending │
        │  - 到期回收 (RequeueLeases) │ │  - RetryAfter 退避判定    │
        │  - 多副本分布式无冲突       │ │  - 原子标记 running 拉起  │
        └───────────────────────────┘ └───────────────────────────┘
```

### 4.1 SQLite / Memory 模式：本地任务消费引擎 (`StartLocalWorker`)

在未配置 PostgreSQL 的单机或轻量部署环境下：
1. **轮询机制**：`StartLocalWorker()` 启动后台协程，每 500ms 扫描一次 `pending` 状态任务（单次最多 10 条）；
2. **退避校验**：检查任务的 `RetryAfter` 字段，尚未到达退避窗口的任务跳过，到达或无退避时间的任务予以处理；
3. **状态抢占**：在启动异步协程前，先通过 `persistTask` 将任务状态原子更新为 `running`（`stage = "ingest"`），防止下一个轮询周期重复拾取；
4. **并发控制与追踪**：在 `s.wg` 与 `s.taskSem`（容量 10）保护下拉起 `processTask(task, req, requestID)` 协程，并将重试请求 ID（`retry-...`）注入链路。

```go
// internal/handlers/handlers.go → localWorkerLoop()
func (s *Server) localWorkerLoop() {
    defer s.wg.Done()
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-s.ctx.Done():
            return
        case <-ticker.C:
            s.processPendingTasks()
        }
    }
}
```

### 4.2 PostgreSQL 模式：分布式原子租约引擎 (`StartLeaseWorker`)

在配置了 `SERVICE_HUB_PG_DSN` 的多副本高可用环境下：
1. **原子竞争**：`tasks.ClaimNext(owner, leaseTTL)` 利用 `FOR UPDATE SKIP LOCKED` 查询并锁定 1 条 `pending` 任务，自动分配 `lease_token`；
2. **租约续期**：后台续租协程以 `leaseTTL / 2` 周期调用 `RenewLease`，若续租失败（如被判定过期回收）则立即中断流水线执行；
3. **过期回收**：循环调用 `RequeueExpiredLeases(100)` 将因节点宕机失联的过期租约重置为 `pending`，供健康副本接管。

---

## 5. 下游客户端弹性与幂等防护

service-hub 在调用下游 **PrivShield Agent**（Privacy Engine 隐私引擎）和 **mock-datasource**（数据源管理中台）时，内置了完整的防御性弹性与幂等保障：

### 5.1 差分隐私预算与脱敏幂等凭据 (`X-Idempotency-Key`)

在差分隐私（DP）和脱敏算子计算中，若网络闪断导致 service-hub 发起重试，若无幂等凭据可能导致上游重复扣减隐私预算（Privacy Budget Exhaustion）。

service-hub 为每个流水线阶段生成唯一的幂等凭据：
$$\text{IdempotencyKey} = \text{"hub-"} + \text{TaskID} + \text{"-"} + \text{Stage} + \text{"-"} + \text{RetryCount}$$

- **注入方式**：通过 `agent.ContextWithIdempotencyKey(ctx, key)` 将凭据注入 Context；
- **HTTP/gRPC 透传**：`agent.Client` 在执行请求时自动提取并设置 HTTP 请求头 `X-Idempotency-Key`；
- **防护效果**：保证同一次重试尝试在 Agent 端被精准识别，避免预算被重复消耗。

### 5.2 按节点维度独立三态熔断器（Per-Node Circuit Breaker）

Agent 客户端为每个上游节点独立维护一套三态熔断器状态机，单节点故障仅熔断该节点流量，其余健康节点继续承接请求：
- **Closed（关闭）**：正常状态，记录该节点连续失败次数；连续失败达到阈值（5 次）立即转为 Open；
- **Open（开启）**：快速失败，发往该节点的后续请求直接返回 `ErrCircuitOpen`，不产生网络 I/O；
- **Half-Open（半开）**：冷却时间（30 秒）结束后转入半开状态，放行单次探测请求。若探测成功则恢复 Closed；若失败则重新开启 30 秒。

### 5.3 指数退避重试与抖动（Exponential Backoff & Jitter）

- **重试上限**：最多重试 3 次（共 4 次尝试）；
- **延迟计算**：$\text{delay} = \text{baseDelay} \times 2^{\text{attempt}-1} + \text{jitter}$（基础延迟 500ms，附加 0~250ms 随机抖动）；
- **错误区分**：仅对网络错误、连接超时、5xx 服务端错误重试；对 4xx（如 400 Bad Request、404 Not Found）立即返回不重试。

### 5.4 响应体安全防护

- **64 MiB 保护上限**：`io.LimitReader(resp.Body, 64<<20)` 截断超大响应体，防止下游恶意或异常超大 JSON 导致 service-hub 发生 OOM 崩溃。
- **重试循环及时释放**：重试循环内读取完毕后立即显式 `resp.Body.Close()`（而非 `defer`），避免多轮重试累积占用响应体与底层 TCP 连接。

---

## 6. 全链路分布式追踪（Distributed Tracing）

service-hub 支持端到端请求标识传递，保证异步 6 阶段流水线在后台执行时仍能完整保留上下文链路信息：

```
[外部请求] ──(X-Request-ID / X-Trace-ID)──> [HTTP / gRPC 入口]
                                      │
                                      ▼ 提取或生成 RequestID
                          [异步流水线 processTask(..., reqID)]
                                      │
              ┌───────────────────────┴───────────────────────┐
              ▼                                               ▼
[datasource.Client.Health/GetDataSource]            [agent.Client.ProcessAgent]
   (注入 X-Request-ID Header/Metadata)             (注入 X-Request-ID & X-Idempotency-Key)
```

1. **入口提取**：
   - HTTP：Gin 中间件提取 `c.GetString("RequestID")` 或 `X-Request-ID` 请求头；
   - gRPC：拦截器从 `metadata.FromIncomingContext(ctx)` 提取 `x-request-id`；
2. **异步协程透传**：`processTask(task, req, requestID)` 将标识显式传递给异步处理流水线；
3. **下游调用传播**：通过 `agent.ContextWithRequestID(ctx, requestID)` 将追踪 ID 注入 Context，并在发起 HTTP/gRPC 请求时作为标准头透传。

---

## 7. 六阶段任务状态持久化保证

任务状态以 `pkg/store.TaskStore` 作为唯一写入接口。SQLite 模式下，调度请求只有在初始 `Save()` 已成功执行 `INSERT OR REPLACE INTO tasks (...)` 后才返回 HTTP `202 Accepted` 或相应的 gRPC 接收结果。

### 7.1 六阶段流水线的写入顺序

HTTP `Dispatch` 与 gRPC `Dispatch` 都遵循相同的写入模型：请求入口先调用一次 `TaskStore.Save()` 创建任务；异步 `processTask()` 再按 `ingest → fetch → classify → desensitize → return → audit` 顺序执行。每一阶段的业务动作开始前，都会先调用 `persistTask()`；该函数封装 `TaskStore.Update()`，只有更新成功才会继续执行该阶段的等待、数据源调用或 Agent 调用。

| 时点或阶段 | 调用与状态迁移 | 本次写入的字段 | 后续行为 |
|---|---|---|---|
| 接收调度请求 | `Save()`；新建 `pending/queued` | `id`、`status`、`stage`、`source`、`api_code`、`datasource_id`、`operation`、`priority`、`created_at`、`payload_json`、初始重试字段 | `Save()` 失败则 HTTP 返回 `500` 或 gRPC 返回 `Internal`，不会启动异步任务。 |
| `ingest` | `Update()`；`pending/queued → running/ingest` | `status=running`、`stage=ingest`、`started_at=当前时间` | 更新成功后才进行该阶段处理；失败则整个协程立即退出。 |
| `fetch` | 首先 `Update()`；`running/ingest → running/fetch` | `status=running`、`stage=fetch`、新的 `started_at` | 数据源拉取阶段保留，分页抽取接口已移除，需由调用方在提交任务时携带载荷。 |
| `classify` | `Update()`；`running/fetch → running/classify` | `status=running`、`stage=classify`、新的 `started_at` | 对隐私操作调用 Agent 一体化处理接口 `ProcessAgent`（`POST /v1/agent/process`，404 回退 `/v1/medical/process`），透传 `X-Idempotency-Key`。Agent 调用失败时转入失败终态写入。 |
| `desensitize` | `Update()`；`running/classify → running/desensitize` | `status=running`、`stage=desensitize`、新的 `started_at` | 该阶段保留状态追踪；实际脱敏已在 `classify` 调用的医疗流水线中完成。 |
| `return` | `Update()`；`running/desensitize → running/return` | `status=running`、`stage=return`、新的 `started_at` | 预留结果返回阶段。 |
| `audit` | `Update()`；`running/return → running/audit` | `status=running`、`stage=audit`、新的 `started_at` | 审计阶段调用 `submitEvidence` 向 audit-log 提交出域存证（`POST /v1/audit/logs`，P0-6 fail-closed），成功后立即执行成功终态写入；提交失败则转入失败终态。 |
| 正常完成 | `Update()`；`running/audit → completed/done` | `status=completed`、`stage=done`、`completed_at=当前时间`、`duration_ms=当前时间-created_at` | 成功终态写入。 |
| 失败或取消 | `Update()`；当前 `running/<stage> → failed/<stage>` | `status=failed`、保留当前 `stage`、`error`、`completed_at`、`duration_ms` | 写入失败记录，可被后续重试协程拾取。 |

---

## 8. SQLite 完整性校验与备份

### 8.1 启动完整性校验（Integrity Check）

服务启动早期（存储初始化之前），对 SQLite 数据库文件执行完整性校验：

```
启动 → ValidateIntegrity(dbPath) → 通过 → 继续初始化
                                   → 失败 → log.Fatalf() 阻止启动
```

- **Fail-Fast**：使用 `pkg/store/sqlite/init.go` 执行 `PRAGMA integrity_check`，损坏时立即终止启动，防止损坏扩散。

### 8.2 数据库备份（Backup）

通过 `scripts/prod/backup-sqlite-databases.sh` 统一备份：
- **在线备份**：使用 `sqlite3 .backup` 命令，不锁库、不影响在线服务；
- **全量与增量**：支持 `--full` 全量备份与 `--incremental` 国密 SM3 哈希增量备份；
- **恢复验证**：`--verify` 解压最新备份并执行 `PRAGMA integrity_check` 校验；
- **生命周期**：自动清理超过 `RETENTION_DAYS`（默认 7 天）的旧备份文件。

---

## 9. 优雅停机（Graceful Shutdown）

```
SIGINT/SIGTERM → 停止后台协程(retry+retention) → 异步任务广播取消 → gRPC GracefulStop(30s超时) → HTTP Shutdown(可配置超时) → 进程安全退出
```

1. **信号捕获**：监听 `SIGINT` 与 `SIGTERM`；
2. **后台协程停止**：取消周期性重试、保留清理与本地/租约 Worker 协程；
3. **异步任务取消**：`serviceImpl.Shutdown()` + `server.Shutdown()` 发送 context 取消信号，通过 `sync.WaitGroup` 等待在途任务平滑退出；
4. **双协议优雅停机**：gRPC 带 30 秒超时 `GracefulStop()`，HTTP 带可配置 `SERVICE_HUB_SHUTDOWN_TIMEOUT`（默认 5s）`Shutdown()`。

---

## 10. 存储后端选型与配置

| 决策维度 | 选择 SQLite | 选择 PostgreSQL |
|---|---|---|
| 部署规模 | 单实例或单 Pod，任务并发受内部信号量控制 | 两个及以上 Hub 副本，需要共享同一任务队列 |
| 任务领取 | 本地 Worker 轮询消费，无需跨实例竞争 | 使用 `ClaimNext` 与 `FOR UPDATE SKIP LOCKED` 原子领取 |
| 写入特征 | 低到中等并发写入，WAL 模式 | 高并发状态更新、租约续期、横向扩容 |
| 连接池调优 | 固定连接池 (MaxOpen: 4, MaxIdle: 2) | **自适应连接池**：基于 `runtime.NumCPU()` 动态配置 MaxConns/MinConns |
| 容灾降级 | 本地文件与备份 | **自动探针降级**：3s 超时探测失败平滑回退 SQLite WAL |

### SQLite 配置参数

| PRAGMA / 参数 | 值 | 说明 |
|---|---|---|
| `journal_mode` | `WAL` | Write-Ahead Logging，读写不互斥，崩溃安全 |
| `synchronous` | `NORMAL` | WAL 模式下的安全同步级别 |
| `busy_timeout` | `5000` | 遇锁等待 5 秒，避免短暂锁竞争报错 |
| `MaxOpenConns` | `4` | 限制并发连接数，防止过度锁竞争 |
| `MaxIdleConns` | `2` | 保持适量空闲连接 |

---

## 11. TLS/mTLS 双向认证与国密支持

| 特性 | 实现 |
|---|---|
| 最低协议版本 | TLS 1.3 / 国密 SM2 最低基线锁定 |
| HTTP/gRPC 双向认证 | 与 `pkg/tlsutil` 工具库打通，支持 `require`/`verify`/`request` 模式 |
| 身份准入 | gRPC 方法级 Scope 鉴权（`hub:read` / `hub:dispatch`）+ 动态 CN 白名单（`PRIVACY_AUTH_MTLS_WHITELIST_FILE`）隔离各域访问权限 |

service-hub 的 HTTP REST 和 gRPC 双协议均支持 TLS 1.3 及 mTLS 双向认证，配置完全对称：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `SERVICE_HUB_TLS_ENABLED` | `false` | 是否启用 TLS/mTLS |
| `SERVICE_HUB_TLS_CERT_FILE` | `""` | 服务端 X.509 证书 PEM 路径 |
| `SERVICE_HUB_TLS_KEY_FILE` | `""` | 服务端私钥 PEM 路径 |
| `SERVICE_HUB_TLS_CA_FILE` | `""` | 客户端认证根 CA 证书路径（mTLS 必需） |
| `SERVICE_HUB_TLS_CLIENT_AUTH` | `""` | 客户端认证模式：`require`（强制双向）/ `verify`（可选）/ `request`（请求） |
| `SERVICE_HUB_TLS_PINNED_PUBKEY_FILE` | `""` | 固定的客户端公钥 PEM 路径（SPKI Pinning 防御 CA 劫持） |

---

## 12. Phase B: PostgreSQL 原子租约与演进清单

### 12.1 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `SERVICE_HUB_PG_DSN` | 空 | PostgreSQL 连接字符串（为空时回退 SQLite） |
| `SERVICE_HUB_PG_MAX_CONNS` | `10` | 连接池最大连接数 |
| `SERVICE_HUB_PG_MIN_CONNS` | `2` | 连接池最小连接数 |
| `SERVICE_HUB_LEASE_TTL` | `60` | 任务租约 TTL（秒） |

### 12.2 可靠性演进完成清单

- [x] **下游 Agent 接口幂等键集成**：通过 Context 自动注入 `X-Idempotency-Key`（`hub-<task_id>-<stage>-<retry_count>`）
- [x] **下游 Datasource 客户端弹性加固**：三态熔断器 + 指数退避重试 + 64 MiB 响应体防护
- [x] **SQLite 待处理与重试任务消费引擎**：`StartLocalWorker` 500ms 轮询拾取与 `RetryAfter` 退避校验
- [x] **全链路分布式追踪传播**：异步 6 阶段流水线 goroutine 保持 `X-Request-ID` / `X-Trace-ID` 上下文（`TraceMiddleware` 双头注入）
- [ ] 多副本压测与领取吞吐基准（CI 自动化集成）
- [ ] PostgreSQL 主从切换故障演练
