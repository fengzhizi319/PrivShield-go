# PrivShield 共享基础包 (Shared PKG) — 开发者实战与架构进阶指南

> **文档定位**：面向全栈开发者的快速上手、源码走读、实战拓展 SOP 与防坑指南（Anti-Patterns）。

---

## 目录

- [一、新手上手与架构全貌](#一新手上手与架构全貌)
- [二、核心业务链路源码走读](#二核心业务链路源码走读)
  - [2.1 数据存证写入链路：从 HTTP 请求到微批落盘](#21-数据存证写入链路从-http-请求到微批落盘)
  - [2.2 分布式任务调度链路：原子争抢与令牌续期](#22-分布式任务调度链路原子争抢与令牌续期)
  - [2.3 敏感数据快照链路：SM4-GCM 动态信封加密](#23-敏感数据快照链路sm4-gcm-动态信封加密)
- [三、核心场景扩展实战 SOP](#三核心场景扩展实战-sop)
  - [SOP-1：如何新增一个生产级 Gin 中间件](#sop-1如何新增一个生产级-gin-中间件)
  - [SOP-2：如何实现一个新的存储后端 (如 MySQL / TiDB)](#sop-2如何实现一个新的存储后端-如-mysql--tidb)
  - [SOP-3：如何安全添加 Prometheus 监控指标](#sop-3如何安全添加-prometheus-监控指标)
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
4. 调用 pkg/crypto.EncryptSample 对原始输入/输出样本执行 SM4-GCM 信封加密
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
   e. 批量落盘: underlying.SaveLogsBatch(logs, snaps) ──► SQLite WAL / PG
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
5. 执行实际隐私计算（调用 Python Agent）
        │ (若执行耗时较长，定期调用 RenewLease 续期)
        │
6. 执行完成调用 CompleteLease(taskID, workerID, "tok-abc-123", result)
   SQL 校验: WHERE id=$1 AND lease_owner=$2 AND lease_token=$3 AND expires_at>=NOW()
        │
7. 返回 true 表示提交成功；若返回 false 说明租约已被接管，当前节点放弃结果。
```

### 2.3 敏感数据快照链路：SM4-GCM 动态信封加密

```text
明文数据: "张三, 110101199003072345"
        │
crypto.EncryptSample(plaintext, masterKey)
        │
1. 生成 12 字节密码学随机 IV: [0x1a, 0x2b, ...]
2. 执行 SM4-GCM 加密，生成密文与 16 字节 Auth Tag
3. 拼接: IV(12B) + Ciphertext + Tag(16B)
4. Base64 编码并拼接前缀: "enc:v1:" + Base64(...)
        │
写入存储: "enc:v1:Gk4v..."
        │
读取时: crypto.DecryptSample(str, masterKey) 自动判定前缀并透明解密
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
3. 在 `pkg/middleware/middleware_test.go` 中编写对应测试用例。

### SOP-2：如何实现一个新的存储后端 (如 MySQL / TiDB)

1. 在 `pkg/store/` 下新建目录 `mysql/`；
2. 实现 `store.AuditStore` 或 `store.TaskStore` 接口中定义的所有方法；
3. 编写 `SaveLogsBatch` 原子批量写入逻辑；
4. 利用 `pkg/store/audit_hash.go` 中的标准规范生成存证哈希：
   ```go
   if log.IntegrityHash == "" {
       log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
   }
   ```
5. 接入 `flusher.NewBufferedAuditStore(mysqlStore, cfg, logger)` 即可直接获得微批与哈希链连续性支持。

### SOP-3：如何安全添加 Prometheus 监控指标

1. 在 `pkg/metrics/metrics.go` 的 `Collector` 结构体中添加指标字段；
2. 在 `NewCollector` 中注册指标到该模块专属的 `Registry`：
   ```go
   c.myCounter = prometheus.NewCounterVec(
       prometheus.CounterOpts{
           Namespace: "privshield",
           Subsystem: module,
           Name:      "custom_events_total",
           Help:      "Total number of custom events",
       },
       []string{"event_type", "status"}, // 标签值必须为低基数枚举！
   )
   c.registry.MustRegister(c.myCounter)
   ```
3. 严禁将用户 ID、动态 URL、随机 Task ID 作为 Prometheus Label。

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
