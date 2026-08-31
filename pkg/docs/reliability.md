# PrivShield 共享基础包 (Shared PKG) — 高可用、容灾与数据一致性保障手册

> **文档定位**：深入阐述 `pkg` 模块在微批缓冲、数据存证、分布式租约争抢、多级降级与容灾恢复等场景下的可靠性架构与实现机理。

---

## 目录

- [一、高可用与一致性设计总览](#一高可用与一致性设计总览)
- [二、微批缓冲刷盘与零丢失优雅停机 (`flusher.BufferedAuditStore`)](#二微批缓冲刷盘与零丢失优雅停机-flusherbufferedauditstore)
  - [2.1 单 Worker 串行哈希链一致性模型](#21-单-worker-串行哈希链一致性模型)
  - [2.2 读己之写 (Read-Your-Own-Writes) 内存分级缓存](#22-读己之写-read-your-own-writes-内存分级缓存)
  - [2.3 优雅停机与队列排空时序 (Drain on Close)](#23-优雅停机与队列排空时序-drain-on-close)
- [三、分布式任务调度防脑裂与租约自愈 (`store.LeasedTaskStore`)](#三分布式任务调度防脑裂与租约自愈-storeleasedtaskstore)
  - [3.1 行级短事务与无死锁争抢 (FOR UPDATE SKIP LOCKED)](#31-行级短事务与无死锁争抢-for-update-skip-locked)
  - [3.2 令牌屏障 (Token Fencing) 防延迟写覆盖](#32-令牌屏障-token-fencing-防延迟写覆盖)
  - [3.3 孤立任务自愈与租约超时回收](#33-孤立任务自愈与租约超时回收)
- [四、存储引擎多级平滑降级架构](#四存储引擎多级平滑降级架构)
  - [4.1 PostgreSQL 3 秒探针超时与 SQLite 回退](#41-postgresql-3-秒探针超时与-sqlite-回退)
  - [4.2 队列满溢与背压丢弃保护](#42-队列满溢与背压丢弃保护)
- [五、上游 Agent 熔断与网络韧性](#五上游-agent-熔断与网络韧性)

---

## 一、高可用与一致性设计总览

`PrivShield` 作为一个承载政务云敏感数据流通的安全网关，其共享基础设施层必须保证在**节点突发崩溃、网络瞬断、并发洪峰与存储抖动**等极端场景下的系统韧性与数据完整性：

```text
┌────────────────────────────────────────────────────────────────────────────┐
│                       PrivShield 可靠性纵深防御矩阵                        │
├─────────────────┬──────────────────────────────────────────────────────────┤
│ 存证数据完整性  │ 单 Worker 串行哈希链 + 优雅停机排空 (RPO = 0, 零断链)     │
│ 内存读写一致性  │ 读己之写内存索引 + 刷盘后原子驱逐                        │
│ 分布式任务调度  │ PostgreSQL FOR UPDATE SKIP LOCKED + 令牌屏障 (零脑裂)     │
│ 存储多级容灾    │ PG (3s探针) ➔ SQLite WAL (忙等待重试) ➔ 内存模式         │
│ 上游通信韧性    │ 指数退避重试 (3次) + 断路熔断器 (5错熔断/30s自愈) + 64MiB防爆│
└─────────────────┴──────────────────────────────────────────────────────────┘
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
4. **刷盘后驱逐**：微批落盘成功后，后台 Worker 自动清理 `recentLogs` 中已落库的条目，防止内存泄漏。

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
   │                              记录告警日志并平滑降级
   │                                           │
   ▼                                           ▼
配置了 DB_PATH? ────► 是 ──────────────────────┴───────────────► 启用 SQLite WAL (busy_timeout=5000)
   │
   ▼ (未配置 DB_PATH)
启用 Memory 内存模式 (本地测试/无状态)
```

### 4.2 队列满溢与背压丢弃保护

当底层存储发生严重 I/O 故障导致刷盘速率远低于写入速率时，内存通道 `queue` 可能满溢：
* 系统采用非阻塞 `select` 探测队列容量；
* 若队列满（超过 2000 条），触发同步落盘兜底机制并递增 `droppedTotal` 指标；
* 同步落盘过程仍受锁保护以维护哈希链，确保系统在背压下不崩溃且哈希链不中断。

---

## 五、上游 Agent 熔断与网络韧性

`pkg/agent.Client` 实现了状态机熔断器与响应体防爆保护：

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

1. **快速失败保护**：当 Python Agent 计算节点发生异常或宕机时，`Open` 状态直接返回熔断错误，防止 Go 协程挂起耗尽连接池；
2. **自愈探测**：30 秒冷却后自动放行探测请求，一旦上游服务恢复立刻重置熔断状态；
3. **64 MiB 响应体保护**：采用 `io.LimitReader` 严格限制单次 Agent 响应大小，防御异常返回超大样本导致 OOM。
