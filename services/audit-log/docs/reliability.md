# audit-log 可靠性能力说明

> 脱敏审计日志与存证服务（audit-log）的崩溃恢复、国密 SM3 完整性校验、微批异步聚合刷盘（Flusher）、自适应连接池调优、探针回退降级与备份能力详解。

---

## 1. 能力总览

| 能力维度 | 支持状态 | 实现方式 |
|---|---|---|
| 崩溃恢复与零丢数据 | ✅ | SQLite WAL + `pkg/store/flusher` 停机同步刷盘，崩溃安全与零丢数据保障 |
| 异步微批聚合刷盘 | ✅ | `flusher.BufferedAuditStore` 内存无锁环形队列 (10,000)，200条/20ms 双触发 |
| 自动连通性探针回退 | ✅ | 启动时 3s 超时探测 PostgreSQL，失败平滑回退至 SQLite WAL 模式 |
| SQLite 完整性校验 | ✅ | `PRAGMA integrity_check` 启动时阻断损坏数据库 |
| 数据库备份 | ✅ | 通过统一备份脚本支持全量/增量备份 |
| 优雅停机 | ✅ | SIGINT/SIGTERM → gRPC GracefulStop → HTTP Shutdown(5s) → Flusher.Close() 同步落盘 |
| 审计数据完整性 | ✅ | 国密 SM3 9 要素连续哈希链 + 快照 SM3 指纹 + SM4-GCM 信封加密 |
| 存储持久化与扩展 | ✅ | SQLite WAL 单机 3k~5k QPS / PostgreSQL Phase B 自适应连接池多副本扩展 |

---

## 2. 内存微批异步聚合刷盘 (`pkg/store/flusher`)

### 2.1 设计痛点与解决思路
传统 SQLite 在面对 > 1,000 QPS 高并发写入时受限于单写者锁（Single Writer Lock）导致排队延迟陡增。系统引入了通用无锁微批缓冲器：

```
高并发写入请求 ──▶ [内存无锁环形队列] ──▶ 后台批量 Worker ──▶ 单事务 SaveLogsBatch()
                      (容量 10,000)          (200条 或 20ms)            (SQLite / PG)
```

1. **双触发刷盘机制**：
   - **定量触发**：缓冲区积累达到 `MaxBatchSize = 200` 条时立即批量入库；
   - **定时触发**：时间窗口达到 `FlushInterval = 20ms` 时自动刷新缓冲区，即使低峰期也能保证毫秒级落盘。
2. **零丢失优雅停机**：
   - 进程收到停机信号后，`BufferedAuditStore.Close()` 会同步关闭后台 Worker，并以阻塞单事务清空剩余所有内存存证，确保零丢失。
3. **背压防溢出降级**：
   - 当极端流量导致环形队列打满时，自动触发 Fail-Safe 同步直写底层存储，绝不丢弃任何审计流水。

---

## 3. SQLite 完整性校验（Integrity Check）

### 3.1 校验时机

服务启动早期（审计存储初始化之前），对 SQLite 数据库文件执行完整性校验：

```
启动 → ValidateIntegrity(dbPath) → 通过 → 继续初始化审计存储
                                   → 失败 → log.Fatalf() 阻止启动
```

### 3.2 校验实现

使用共享库 `pkg/store/sqlite/init.go` 中的 `ValidateIntegrity()` 函数：

```go
// 1. 打开数据库连接
// 2. 执行 PRAGMA integrity_check
// 3. 检查结果是否为 "ok"
// 4. 损坏时返回 "database corruption detected: ..." 错误
```

### 3.3 设计原则

- **Fail-Fast**：数据库损坏时立即终止启动，防止审计数据进一步损坏或丢失；
- **统一实现**：通过 `sqlite.ValidateIntegrity()` 共享函数，与 service-hub 保持一致；
- **内存模式豁免**：`dbPath` 为空时跳过校验。

---

## 4. 审计数据国密完整性保障

### 4.1 国密 SM3 连续防篡改哈希链

所有审计事件以国密 SM3 算法连续计算哈希指针：

```
prevHash + id + task_id + api_code + datasource_id + timestamp + input_hash + output_hash + algorithm → 国密 SM3 → integrity_hash
```

- 每条审计记录附带前序 `prev_hash`，实现区块链式前后咬合；
- 全链核验接口 `POST /api/audit/chain/verify` 毫秒级重算整链，杜绝物理删行或记录插入攻击。

### 4.2 快照样本国密 SM4-GCM 信封加密

快照表中的 `input_sample` 和 `output_sample` 由 `pkg/crypto` 采用国密 SM4-GCM 密文落盘（`enc:v1:<base64>`），防止数据库文件被拖库导致明文隐私外泄。

---

## 5. PostgreSQL Phase B 自适应连接池与自动分区

1. **自适应连接池大小**：
   `postgres.NewAuditStore` 启动时自动根据 `runtime.NumCPU()` 计算最佳连接池连接数：
   $$\text{MaxConns} = \text{clamp}(20, 100, \text{NumCPU} \times 4)$$
   $$\text{MinConns} = \text{clamp}(4, 20, \text{NumCPU})$$
2. **按月动态分区索引预建 (`AutoEnsurePartitions`)**：
   自动预建当前月及未来 3 个月的时间范围分区索引，降低历史大表查询扫描开销。
3. **自动连通性探针回退 (Self-healing Fallback)**：
   若配置的 `AUDIT_LOG_PG_DSN` 连接超时（>3s）或不可达，记录告警日志并自动平滑回退至 SQLite WAL 模式，确保服务高可用。

---

## 6. 数据库备份（Backup）

### 6.1 备份脚本

通过 `scripts/prod/backup-sqlite-databases.sh` 统一备份 audit-log 数据库。

### 6.2 备份模式

| 模式 | 参数 | 说明 |
|---|---|---|
| 全量备份 | `--full`（默认） | 完整备份审计日志数据库 |
| 增量备份 | `--incremental` | 基于国密 SM3 哈希比对，仅备份变化的数据库 |
| 恢复验证 | `--verify` | 解压最新备份并执行 `PRAGMA integrity_check`，确保备份可恢复 |

---

## 7. 优雅停机（Graceful Shutdown）

### 7.1 停机流程

```
SIGINT/SIGTERM → gRPC GracefulStop → HTTP Shutdown(5s) → Flusher.Close() (清空微批缓冲) → 进程安全退出
```

**详细步骤：**

1. **信号捕获**：监听 `SIGINT` 和 `SIGTERM`；
2. **gRPC 优雅停机**：
   - `serviceImpl.Shutdown()`：发送 context 取消信号；
   - `grpcServer.GracefulStop()`：等待在途 RPC 完成；
3. **HTTP 优雅停机**：`httpSrv.Shutdown(ctx)` 等待现有请求完成（可配置硬上限）；
4. **Flusher 同步刷盘**：`auditStore.(*flusher.BufferedAuditStore).Close()` 触发后台 Worker 退出并同步刷空内存环形队列。
