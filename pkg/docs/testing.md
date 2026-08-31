# PrivShield 共享基础包 (Shared PKG) — 测试规范与质量保障指南

> **文档定位**：`pkg` 模块的单元测试、并发竞态检测（`-race`）、微批刷盘压力测试、哈希链连续性验证与持续集成（CI）规范。

---

## 目录

- [一、测试设计理念与测试矩阵](#一测试设计理念与测试矩阵)
- [二、核心测试套件说明](#二核心测试套件说明)
  - [2.1 密码学与信封加密单测 (`pkg/crypto`)](#21-密码学与信封加密单测-pkgcrypto)
  - [2.2 微批缓冲与哈希链连续性并发测试 (`pkg/store/flusher`)](#22-微批缓冲与哈希链连续性并发测试-pkgstoreflusher)
  - [2.3 分布式原子租约与并发争抢测试 (`pkg/store/postgres`)](#23-分布式原子租约与并发争抢测试-pkgstorepostgres)
  - [2.4 中间件防御与限流熔断单测 (`pkg/middleware` & `pkg/agent`)](#24-中间件防御与限流熔断单测-pkgmiddleware--pkgagent)
- [三、执行测试与性能基准指令](#三执行测试与性能基准指令)
- [四、CI/CD 自动化集成质量红线](#四cicd-自动化集成质量红线)

---

## 一、测试设计理念与测试矩阵

`pkg` 模块是全系统的基石，任何并发竞态（Data Race）、锁死锁（Deadlock）或哈希链断裂缺陷都将导致全局系统灾难。为此构建了立体测试矩阵：

| 测试层级 | 覆盖目标 | 核心验证手段 |
|---|---|---|
| **算法与密码学单测** | 国密 SM3/SM4-GCM、信封加解密 | 国标测试向量比对、IV 随机性、认证标签防篡改 |
| **高并发并发测试** | `flusher.BufferedAuditStore` | 多协程并发写入、连续哈希链在线验真、停机排空零丢失 |
| **分布式租约并发测试** | `postgres.LeasedTaskStore` | 20+ 虚拟 Hub 实例无死锁争抢、令牌防脏写覆盖 |
| **中间件链路测试** | Gin 9 层纵深防御栈 | 异常 Panic 恢复、IP 令牌桶限流、常量时间鉴权 |
| **网络容灾与熔断测试** | `agent.Client` | 模拟上游 5xx 故障、熔断器三状态流转、64MiB 响应防护 |

---

## 二、核心测试套件说明

### 2.1 密码学与信封加密单测 (`pkg/crypto`)

* **SM3 国标向量对齐 (`sm3_test.go`)**：
  * 输入 `"abc"` 严格断言输出 `66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0`；
  * 测试 1MB 大块数据分片哈希一致性；
  * 测试 UTC 纳秒级前像规范化计算与双模验真。
* **SM4-GCM 认证加密与信封测试 (`envelope_test.go`)**：
  * 测试随机 IV（12 字节）在 10,000 次加密中零碰撞；
  * 测试篡改密文字符串时，解密立即报错并拒绝输出损坏明文；
  * 测试短密钥主密钥派生（SM3 KDF）稳定性。

### 2.2 微批缓冲与哈希链连续性并发测试 (`pkg/store/flusher`)

* **`TestBufferedAuditStore_HashChainIntegrity` (P0-1 核心验证)**：
  * 启动 10 个并发 Goroutine 写入 100 条审计记录；
  * 写入完成后调用 `VerifyChain(1000)`；
  * 断言：`res.Valid == true`，`res.TotalVerified == 100`，`res.BrokenAtID == ""`，证明单 Worker 串行哈希链绝对连续。
* **`TestBufferedAuditStore_ReadYourOwnWrites` (P1-1 核心验证)**：
  * 设置刷盘间隔为长超时（500ms），保存日志后立即在同一协程中调用 `GetLog` 与 `GetLatestLog`；
  * 断言：在数据尚未触发定时刷盘前，内存暂存层必须立刻返回正确日志对象。
* **`TestBufferedAuditStore_ConcurrentFlush` (P0-3 核心验证)**：
  * 10 个写入协程并发写入 300 条记录，同时 5 个协程并发频繁调用 `Flush()`；
  * 断言：零 Channel Panic、零锁死锁，最终落盘记录精确等于 300 条。
* **`TestBufferedAuditStore_ConcurrentWrites` (P0-2 核心验证)**：
  * 20 个协程各写 50 条快照与日志，同时触发 `Close()`；
  * 断言：优雅停机排空后底层存储精确包含 1,000 条日志与 1,000 条快照，零数据丢失。

### 2.3 分布式原子租约与并发争抢测试 (`pkg/store/postgres`)

* **`TestLeasedTaskStore_ConcurrentClaim` (`leased_test.go`)**：
  * 模拟 10 个并发 Hub 实例争抢 50 个待调度任务；
  * 断言：每个任务仅被唯一个 Hub 实例成功领取，0 重复消费；
* **`TestLeasedTaskStore_TokenFencing` (`leased_test.go`)**：
  * 模拟节点 1 租约超时后，节点 2 接管并完成任务；
  * 节点 1 尝试使用过期 Token 提交完成，断言操作返回 `false` 且数据库状态未被篡改。

### 2.4 中间件防御与限流熔断单测 (`pkg/middleware` & `pkg/agent`)

* **`TestRateLimiter_AnonymousIPDimension`**：测试 IP 维度令牌桶超出 RPS/Burst 后的 429 拦截；
* **`TestAuthMiddleware_ConstantTime`**：测试非法 API Key 在常量时间比对下的防护；
* **`TestCircuitBreaker_StateTransitions`**：测试 `Closed ➔ 5次失败 ➔ Open ➔ 30s冷却 ➔ HalfOpen ➔ 成功 ➔ Closed` 完整生命周期。

---

## 三、执行测试与性能基准指令

### 3.1 运行全量单元测试
```bash
cd /home/charles/code/PrivShield-go

# 运行 pkg 全量单元测试
go test -v ./pkg/...

# 运行全项目集成测试
make test
```

### 3.2 运行并发竞态检测 (Data Race Detector)
```bash
# 强制开启竞态检测（需开启 CGO 或在 Linux AMD64 下运行）
CGO_ENABLED=1 go test -race ./pkg/store/flusher/... ./pkg/crypto/... ./pkg/middleware/...
```

### 3.3 运行性能基准测试 (Benchmarks)
```bash
# 测试 SM3、SM4-GCM 与微批刷盘吞吐
go test -bench=. -benchmem ./pkg/crypto/... ./pkg/store/flusher/...
```

---

## 四、CI/CD 自动化集成质量红线

所有针对 `pkg` 的提交必须通过 GitHub Actions / 本地 CI 门禁检查：

1. **编译检查**：`CGO_ENABLED=0 go build ./pkg/...` 零编译错误；
2. **测试覆盖率**：全包测试覆盖率不低于 **90%**；
3. **哈希链连续性红线**：`TestBufferedAuditStore_HashChainIntegrity` 必须 100% PASS；
4. **依赖洁净化**：严禁引入未经安全审计的第三方 CGO 库或冗余外部依赖。
