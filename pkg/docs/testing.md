# PrivShield 共享基础包 (Shared PKG) — 测试规范与质量保障指南

> **文档定位**：`pkg` 模块的单元测试、并发竞态检测（`-race`）、微批刷盘压力测试、哈希链连续性验证与安全门禁的完整测试说明，以及与持续集成（CI）的对接规范。

---

## 目录

- [一、测试设计理念与测试矩阵](#一测试设计理念与测试矩阵)
- [二、核心测试套件说明](#二核心测试套件说明)
  - [2.1 密码学与信封加密单测 (`pkg/crypto`)](#21-密码学与信封加密单测-pkgcrypto)
  - [2.2 微批缓冲与刷盘器全场景测试 (`pkg/store/flusher`)](#22-微批缓冲与刷盘器全场景测试-pkgstoreflusher)
  - [2.3 分布式原子租约与并发争抢测试 (`pkg/store/postgres` & `pkg/store/sqlite`)](#23-分布式原子租约与并发争抢测试-pkgstorepostgres--pkgstoresqlite)
  - [2.4 中间件防御与限流熔断单测 (`pkg/middleware`)](#24-中间件防御与限流熔断单测-pkgmiddleware)
  - [2.5 安全门禁 Fail-Closed 前置校验测试 (`pkg/config`)](#25-安全门禁-fail-closed-前置校验测试-pkgconfig)
  - [2.6 哈希链完整性密钥化验真测试 (`pkg/store`)](#26-哈希链完整性密钥化验真测试-pkgstore)
  - [2.7 命名归一化与安全等级词表测试 (`pkg/naming`)](#27-命名归一化与安全等级词表测试-pkgnaming)
- [三、执行测试与性能基准指令](#三执行测试与性能基准指令)
- [四、CI/CD 自动化集成质量红线](#四cicd-自动化集成质量红线)

---

## 一、测试设计理念与测试矩阵

`pkg` 模块是全系统的基石，任何并发竞态（Data Race）、锁死锁（Deadlock）或哈希链断裂缺陷都将导致全局系统灾难。为此构建了立体测试矩阵：

| 测试层级 | 覆盖目标 | 核心验证手段 |
|---|---|---|
| **算法与密码学单测** | 国密 SM3/SM4-GCM、HMAC-SM3、信封加解密（enc:v2） | 国标测试向量比对、IV 随机性、认证标签防篡改、HKDF-SM3 salt 唯一性 |
| **高并发刷盘测试** | `flusher.BufferedAuditStore`（8 字段 Config） | 多协程并发写入、连续哈希链在线验真、停机排空零丢失、积压饱和拒绝、拥塞超时 |
| **分布式租约并发测试** | `postgres.LeasedTaskStore` | 20+ 虚拟 Hub 实例无死锁争抢、令牌防脏写覆盖；SQLite/Memory 返回 `ErrLeaseNotSupported` |
| **中间件链路测试** | Gin 纵深防御栈 | 异常 Panic 恢复、IP 令牌桶限流、常量时间鉴权、/metrics 鉴权收敛、只读角色端点隔离 |
| **安全门禁前置校验** | `config.ValidateFailClosed` | 非回环绑定必须携带 API Key / TLS / mTLS 白名单 / 加密密钥，任一缺失即 fail-closed |
| **哈希链密钥化验真** | `store.ComputeAuditIntegrityHash` / `VerifyAuditIntegrityHash` | HMAC-SM3 密钥化哈希、SM3-HMAC:v1 → SM3 → SHA256-LEGACY 三级降级验真、算法标签返回 |
| **命名归一化与观测** | `naming.Normalize` / `Observer` | 等级词表 canonical 映射 L1~L5、别名事件上报、fail-closed 未知输入拒绝 |
| **网络容灾与熔断测试** | `agent.Client` | 模拟上游 5xx 故障、熔断器三状态流转、64MiB 响应防护 |

---

## 二、核心测试套件说明

### 2.1 密码学与信封加密单测 (`pkg/crypto`)

* **SM3 国标向量对齐 (`sm3_test.go`)**：
  * `TestSM3_StandardVector1`：输入 `"abc"` 严格断言输出 `66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0`；
  * `TestSM3_StandardVector2`：64 字节重复字符串测试向量对齐 `debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732`；
  * `TestSM3_HashInterface`：增量 `Write` 分片哈希一致性验证。
* **HMAC-SM3 密钥化消息认证码 (`sm3.go` / `envelope_test.go`)**：
  * `crypto.HMACSM3(key, data)` 与 `crypto.HMACSM3Hex(key, data)` 提供基于国密 SM3 的密钥化 MAC；
  * 存证哈希链在有密钥态下使用 `HMAC-SM3` 保证「可验真不可伪造」，详见 [§2.6](#26-哈希链完整性密钥化验真测试-pkgstore)。
* **SM4-GCM 信封加密与 enc:v2 信封测试 (`envelope_test.go`)**：
  * `TestSM4StandardVector`：GB/T 32907-2016 附录 A.1 标准测试向量逐字节对齐；
  * `TestEnvelopeEncryption`：端到端信封加解密验证，包含以下子断言：
    1. 密文包含 `enc:v2:` 前缀（`IsEncrypted` 返回 `true`）；
    2. 正确密钥无损还原明文；
    3. 错误密钥 GCM 认证标签校验失败并报错；
    4. 无前缀字符串返回 `ErrUnencryptedValue`，不再静默当作明文放行；
    5. **空密钥断言**：传入空密钥 `""` 调用 `EncryptString`，断言返回 `ErrEmptyKey` 且不产出任何密文；
  * `TestEnvelopeVersionDowngradeRejected`：剥离或改写版本前缀无法让密文被静默接受（v2 前缀参与 GCM AAD）；
  * `TestEnvelopeLegacyV1Readable`：存量 v1 密文（SHA-256 派生、无 AAD）在新实现下仍可解密，保证历史存证不失效；
  * `TestEnvelopeSaltMakesCiphertextUnique`：**enc:v2 HKDF-SM3 带 salt 信封验证**——同一明文在逐记录随机 salt 下两次加密产出不同密文，且两者都能被同一口令正确还原，消除密文等值泄漏。

### 2.2 微批缓冲与刷盘器全场景测试 (`pkg/store/flusher`)

`flusher.Config` 包含 8 个配置字段：

| 字段 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `BufferSize` | `int` | 10000 | 环形缓冲队列容量 |
| `MaxBatchSize` | `int` | 200 | 单批最大写入条数 |
| `FlushInterval` | `time.Duration` | 20ms | 最长刷盘等待时间窗口 |
| `EnqueueTimeout` | `time.Duration` | 500ms | 队列满时等待可用槽位的超时时间 |
| `FlushTimeout` | `time.Duration` | 5s | 显式 Flush 屏障等待超时 |
| `CloseTimeout` | `time.Duration` | 10s | 优雅停机排空等待超时 |
| `MaxRetries` | `int` | 3 | 单批提交失败重试次数 |
| `MaxStaged` | `int` | 50000 | 内存暂存/重投积压上限（防 OOM） |

* **`TestBufferedFlusher_BatchSizeThreshold`**：验证单批达到 `MaxBatchSize` 阈值时立即触发批量落盘；
* **`TestBufferedFlusher_TickerTrigger`**：验证未达到批大小阈值时，定时器 `FlushInterval` 到期自动触发刷盘；
* **`TestBufferedFlusher_ConcurrentWrite`**（P0-1 核心验证）：
  * 启动 10 个并发 Goroutine 写入 100 条审计记录（共 1000 条）；
  * 写入完成后调用 `VerifyChain(0)`；
  * 断言：`res.Valid == true`，`res.TotalVerified == 1000`，证明单 Worker 串行哈希链绝对连续。
* **`TestBufferedFlusher_GetLog_ReadYourOwnWrites`**（P1-1 核心验证）：
  * 设置刷盘间隔为长超时（10s），保存日志后立即在同一协程中调用 `GetLog`；
  * 断言：在数据尚未触发定时刷盘前，内存暂存层必须立刻返回正确日志对象；底层 store 此时不应有此记录。
* **`TestBufferedFlusher_SnapshotAndResponseHashStrictMatch`**（P0-B 修复验证）：
  * 验证主日志、快照与客户端响应体哈希的绝对一致；
  * `snap.PrevHash` 必须指向父日志的 `IntegrityHash`，且两者不可相同。
* **`TestBufferedFlusher_CloseDrainsBuffer`**：
  * 25 条记录入队后立即执行 `Close()`；
  * 断言：底层 store 精确包含 25 条日志，零数据丢失；
  * `Close` 之后再次写入返回 `ErrStoreClosed`。
* **`TestBufferedFlusher_FlushBarrier`**：
  * `Flush()` 作为同步强一致性屏障，返回 nil 当且仅当队列清空并成功提交；
  * 断言 `QueueDepth() == 0`，`FlushedTotal() == 10`。
* **`TestBufferedFlusher_UnderlyingFailureRetryBacklog`**：
  * 注入底层存储故障，5 条记录转入重试积压区（Retry Backlog）；
  * 恢复后触发 `Flush()`，按原序重投，哈希链完好无损。
* **`TestBufferedFlusher_BoundedBacklogSaturationRejection`**（防 OOM）：
  * 底层存储持续不可用，积压区达到 `MaxStaged`（5 条）后快速拒绝；
  * 第 6 条写入必须返回 `ErrBacklogSaturated`。
* **`TestBufferedFlusher_CongestionTimeoutRejection`**：
  * 队列深度为 2，工作协程被阻塞；
  * 第 4 条写入在 `EnqueueTimeout`（20ms）超时后被拒绝，返回拥塞错误。
* **`TestBufferedFlusher_BoundedStagedMemoryEviction`**：
  * `MaxStaged` 设为 3，写入 5 条后断言暂存不超过 3 条、淘汰不少于 2 条，按 FIFO 淘汰防内存泄漏。

### 2.3 分布式原子租约与并发争抢测试 (`pkg/store/postgres` & `pkg/store/sqlite`)

**PostgreSQL 集成测试**（`leased_test.go`，需 `PRIVSHIELD_PG_TEST_DSN` 环境变量，否则自动 Skip）：

* **`TestClaimNext_NoPendingTasks`**：无 pending 任务时返回 `nil`；
* **`TestClaimNext_ClaimsPendingTask`**：正常抢占最高优先级 pending 任务，状态流转为 `running`；
* **`TestClaimNext_SkipsRunningTasks`**：自动跳过 running 任务，不重复抢占；
* **`TestCompleteLease_Success`**：合法 token 正常完成，状态流转为 `completed`；
* **`TestCompleteLease_WrongToken`**：错误 token 返回 `ok=false`，数据库状态不被篡改；
* **`TestFailLease_Terminal`**：终态失败流转为 `failed`；
* **`TestFailLease_Retryable`**：可重试失败回退为 `pending`，`retry_count` 递增；
* **`TestRenewLease_Success`**：未超期租约正常续期；
* **`TestRequeueExpiredLeases`**：使用极短 TTL（1ms）领取任务，等待超时后批量回收重置为 `pending`。

**SQLite / Memory 租约桩实现测试**（`sqlite_test.go` / `memory_test.go`）：

* **`TestLeasedTaskStore_ClaimNext_ReturnsNotSupported`**：SQLite 下 `ClaimNext` 返回 `ErrLeaseNotSupported`；
* **`TestLeasedTaskStore_RenewLease_ReturnsNotSupported`**：SQLite 下 `RenewLease` 返回 `ErrLeaseNotSupported`；
* **`TestLeasedTaskStore_CompleteLease_ReturnsNotSupported`**：SQLite 下 `CompleteLease` 返回 `ErrLeaseNotSupported`；
* **`TestLeasedTaskStore_FailLease_ReturnsNotSupported`**：SQLite 下 `FailLease` 返回 `ErrLeaseNotSupported`；
* **`TestLeasedTaskStore_RequeueExpiredLeases_ReturnsNotSupported`**：SQLite 下 `RequeueExpiredLeases` 返回 `ErrLeaseNotSupported`；
* **`TestLeasedTaskStore_InterfaceCompliance`**：断言 SQLite `TaskStore` 实现了 `store.LeasedTaskStore` 接口，但所有方法均返回 `ErrLeaseNotSupported`，明确标识单副本模式不支持跨实例租约。

### 2.4 中间件防御与限流熔断单测 (`pkg/middleware`)

**CORS 跨域测试**：
* `TestCORS_AllowAll` / `TestCORS_AllowAllWildcard`：通配符放行 `Access-Control-Allow-Origin: *`；
* `TestCORS_SpecificOrigins`：白名单精确过滤，非白名单不设置 Allow-Origin 头；
* `TestCORS_PreflightOptions`：OPTIONS 返回 204 No Content 并携带 Allow-Methods / Allow-Headers。

**Auth 鉴权测试**：
* `TestAuth_EmptyKey_SkipsAuth`：apiKey 为空时自动跳过鉴权（开发模式兼容）；
* `TestAuth_HealthExempt`：`/health` 与 `/api/health` 路径免鉴权；
* `TestAuth_ValidKey` / `TestAuth_InvalidKey` / `TestAuth_MissingToken`：Bearer Token 校验正确/错误/缺失时分别返回 200/401/401；
* **`TestAuth_MetricsRequiresAuth`**（P1-6 暴露面收敛）：`/metrics` 端点必须携带合法 Bearer Token，无 Token 返回 401，不再作为免鉴权暴露面；
* `TestAuth_NonCorePath_Exempt`：非核心路径（非 `/api/*` 且非 `/metrics`）仍免鉴权。

**只读角色鉴权测试**（`roles_auth_test.go`）：
* **`TestAuthWithRoles_ReaderAllowsVerificationReads`**：只读核验员必须能完成验真与查询端点（GET /api/audit/logs、GET /api/audit/stats、POST /api/audit/snapshots/verify、POST/GET /api/audit/chain/verify）；
* **`TestAuthWithRoles_ReaderDeniedOnWriteEndpoints`**：白名单必须带方法——同路径 POST 写入不能因 GET 白名单被放行；
* **`TestAuthWithRoles_FullKeyKeepsWriteAccess`**：写入身份不受白名单约束；
* **`TestAuthWithRoles_UnknownAndMissingKeys`**：未知 Key 返回 401，缺 Token 返回 401；
* **`TestAuthWithRoles_EmptyReaderKeyDegradesToSingleKey`**：readerKey 为空时与 `Auth(apiKey)` 完全同构，存量部署零影响；
* **`TestAuthWithRoles_HealthExempt`**：探活路径豁免语义与 Auth 保持一致；
* **`TestAuthWithRoles_MetricsRequiresAuth`**：/metrics 纳入鉴权（P1-6），无 Key 返回 401，持 write-key 返回 200；
* **`TestIsReadOnlyEndpoint_PathBoundary`**：前缀匹配必须以 `/` 为边界，防止 `/api/audit/logs` 越到 `/api/audit/logs-backup`；
* **`TestAuthWithRoles_ReaderKeyNeverTreatedAsWriteKey`**：拒绝响应体返回标准 `FORBIDDEN` 信封，不泄露可枚举信息。

**RequestID / 追踪测试**：
* `TestRequestID_Passthrough`：入站 `X-Request-ID` 原样透传；
* `TestRequestID_Generated`：缺失时自动生成 `req-` 前缀随机 ID。

**Recovery / SecurityHeaders / DDoS 纵深防御测试**：
* `TestRecovery_CatchesPanic`：Handler panic 被捕获，输出 500 `INTERNAL_ERROR` 统一信封；
* `TestSecurityHeaders`：6 项企业级安全响应头全部注入；
* `TestMaxBodySize`：超出 413 Payload Too Large 拦截；
* `TestMaxConcurrent`：并发超限返回 503 Service Unavailable；
* `TestRateLimit_AllowsUnderBurstAndRejectsOver`：IP 维度令牌桶超出 RPS/Burst 后的 429 拦截。

**网络准入与受信任代理纯函数单测**（`ip_allowlist_test.go` & `middleware_test.go`）：
* `TestIPAllowlist_AllowsMatchingCIDR` / `TestIPAllowlist_BlocksNonMatching`：CIDR 白名单精准放行与非法源 IP 403 阻断；
* `TestAllowedCIDRsFromEnv`：验证空键返回 `nil`、专属变量精准读取与切片解析；
* `TestTrustedProxiesFromEnv`：验证空键返回 `nil`、专属变量精准读取与切片解析；
* `TestRealClientIP_UntrustedProxyIgnored`：非可信反向代理上送的 `X-Forwarded-For` 严格丢弃，消除伪造攻击。

**国密 TLCP 与多版本密钥轮换单测**（`tlcp_test.go` & `envelope_test.go`）：
* `TestIsTLCPEnabled`：验证空键安全返回 `false`、按参数显式检测特定变量；
* `TestTLCPConfigFromEnv`：验证空前缀传参返回空结构体、传入业务前缀后解析国密双证书配置；
* `TestRegisterKeyVersionsFromEnv`：验证空前缀安全返回 0，传入专属服务前缀后正确挂载多版本 SM4 密钥。

### 2.5 安全门禁 Fail-Closed 前置校验测试 (`pkg/config`)

`config.ValidateFailClosed` 在服务启动前执行零信任安全门禁校验，任何一项不满足即拒绝启动（`security_test.go`）：

* **`TestValidateFailClosed`** 包含以下子测试：
  * **loopback without api key is allowed**：本地回环绑定（`127.0.0.1`）允许无 API Key 启动（开发兼容）；
  * **remote bind without api key fails closed**：非回环绑定（`0.0.0.0`）无 API Key 返回 `ErrAPIKeyRequired`；
  * **require tls without tls fails**：配置 `RequireTLS=true` 但未启用 TLS 返回 `ErrTLSRequired`；
  * **grpc tls without whitelist fails**：启用 TLS + gRPC 但未配置 mTLS 白名单文件返回 `ErrMTLSWhitelistRequired`；配置白名单文件后通过；
  * **missing encryption key on remote bind fails**：远程绑定且 `RequireEncryptionKey=true` 时未配置加密密钥返回 `ErrEncryptionKeyRequired`；配置密钥后通过。

* **`TestIsLoopbackHost`**：验证回环地址判定逻辑——`localhost`、`127.0.0.1`、`127.0.1.5`、`::1` 均为回环；`0.0.0.0`、`::`、`10.0.0.7` 均非回环。

### 2.6 哈希链完整性密钥化验真测试 (`pkg/store`)

`store.ComputeAuditIntegrityHash` / `VerifyAuditIntegrityHash` 提供审计日志的完整性哈希计算与多级降级验真（`audit_hash_test.go`）：

* **`TestAuditChainKeyTrimsWhitespace`**：纯空白密钥被裁剪为空串，恢复为无密钥态（`AuditHashSM3`）；
* **`TestUnkeyedChainUsesPlainSM3`**：无密钥态使用标准 SM3 哈希，验真返回 `(true, "SM3")`；
* **`TestKeyedChainIsHMACSM3WithVersionPrefix`**（HMAC-SM3 密钥化哈希）：
  * 有密钥态使用 `HMAC-SM3`，格式为 `SM3-HMAC:v1|<hex>`；
  * 验真返回 `(true, "SM3-HMAC:v1")`，且该标签被标记为 canonical。
* **`TestKeyedChainRejectsUnkeyedRecomputation`**：知道口径但不知道密钥者重算出的无密钥摘要不能被接受为规范态；
* **`TestKeyedChainRejectsWrongKeyForgery`**：错误密钥计算出的 HMAC-SM3 与正确密钥产出不同，验真返回 `false`；
* **`TestKeyedChainDetectsFieldTampering`**：篡改任意字段（如 `output_hash`）后验真失败；
* **`TestLegacyLocalTimezoneSHA256StillVerifies`**（降级兼容验真）：
  * 验证 `VerifyAuditIntegrityHash` 的三级降级匹配顺序：
    1. **SM3-HMAC:v1**：有密钥态首选 HMAC-SM3 验真；
    2. **SM3**：无密钥态回退到标准 SM3 验真；
    3. **SHA256-LEGACY**：最终回退到迁移前本机时区 + SHA-256 验真，返回标签 `"SHA256-LEGACY"`；
  * 旧版标签永不标记为 canonical。
* **`TestVerifyRejectsEmptyStoredHash`**：空存储哈希返回 `(false, "")`。

### 2.7 命名归一化与安全等级词表测试 (`pkg/naming`)

**安全等级词表测试**（`levels_test.go`）：

* **`TestSecurityLevelsMatchTaxonomyYAML`**（P1-5 词表一致性）：
  * 断言代码内词表与 `rules/taxonomies/default.yaml` 完全一致；
  * 等级标识（`L1`~`L5`）、中文名、排名三者在任何服务侧不得出现第二份副本。
* **`TestSecurityLevelNormalization`**：
  * `NormalizeSecurityLevelID` 覆盖两套词表表达互转：`L1` ↔ `public`、`L2` ↔ `internal`、`L3` ↔ `confidential`、`L4` ↔ `secret`、`L5` ↔ `top_secret`；
  * 大小写/空白容忍（`" l4 "` → `L4`，`"PUBLIC"` → `L1`）；
  * 词表外脏值（`"critical"`、`"L6"`、`""`）必须返回空串，严禁静默兜底。
* **`TestMaxSecurityLevelID`**：混合词表取最高等级（`MaxSecurityLevelID("public", "secret", "L2")` → `L4`）；全不可识别返回空串（fail-closed）。
* **`TestSecurityLevelIDsAndLabelsAreCopies`**：确保返回值为防御性拷贝，调用方无法通过修改返回值篡改全局词表。

**命名归一化与 Observer 测试**（`observer_test.go` / `naming_test.go`）：

* **`TestNormalizeRecordsAliasUse`**：注册 Observer 后，`Normalize("yibao")`、`Normalize("医保")`、`Normalize("api2_kangyang")` 成功归一化，Observer 接收到对应的 alias 事件（包含 alias、canonical、target 三元组）；
* **`TestCanonicalInputEmitsNoAliasEvent`**：canonical 输入不产生 alias 事件；
* **`TestNormalizeFailureReasons`**：空输入 → `ReasonEmpty`，未知输入 → `ReasonUnknown`，reserved 输入 → `ReasonReserved`，Observer 均接收到对应错误事件；
* **`TestCheckWritableReportsDistinctReasons`**：别名 → `ReasonFormatInvalid`，未注册 → `ReasonUnknown`，reserved → `ReasonReserved`；
* **`TestResolveInboundCountsReservedOnce`**：防止同一 reserved 调用被重复计数；
* **`TestObserverIsOptional`**：未注册 Observer 时不 panic，但 fail-closed 语义不受影响。

---

## 三、执行测试与性能基准指令

### 3.1 运行全量单元测试
```bash
cd /home/charles/code/PrivShield-go

# 运行 pkg 全量单元测试
go test -v ./pkg/...

# 运行全项目测试（SDK + Agent + 微服务群 + BFF）
make test
```

### 3.2 运行并发竞态检测 (Data Race Detector)
```bash
# 使用 make 目标运行写入路径竞态门禁（推荐）
make test-race

# 等价于：
CGO_ENABLED=1 go test -race -count=1 -timeout 900s ./pkg/store/... ./services/audit-log/...

# 或手动指定 pkg 子包
CGO_ENABLED=1 go test -race ./pkg/store/flusher/... ./pkg/crypto/... ./pkg/middleware/...
```

> `-race` 依赖 CGO，因此不能使用 `CGO_ENABLED=0`。需在 Linux AMD64 环境下运行。

### 3.3 运行性能基准测试 (Benchmarks)
```bash
# 测试 SM3、SM4-GCM 与微批刷盘吞吐
go test -bench=. -benchmem ./pkg/crypto/... ./pkg/store/flusher/...

# 运行仓内全量性能基线（含环境指纹）
make bench
```

---

## 四、CI/CD 自动化集成质量红线

所有针对 `pkg` 的提交必须通过 GitHub Actions / 本地 CI 门禁检查：

1. **编译检查**：`CGO_ENABLED=0 go build ./pkg/...` 零编译错误；
2. **测试覆盖率**：全包测试覆盖率不低于 **90%**；
3. **哈希链连续性红线**：`TestBufferedFlusher_ConcurrentWrite` 必须 100% PASS，证明多协程并发下哈希链绝对连续；
4. **归档验证测试红线**：`TestBufferedFlusher_CloseDrainsBuffer` 必须通过，验证 `Close()` 排空所有在途记录后底层存储精确包含预期条数，零数据丢失；
5. **安全门禁测试红线**：`TestValidateFailClosed` 必须通过，验证非回环绑定下 API Key / TLS / mTLS 白名单 / 加密密钥四项任一缺失即 fail-closed 拒绝启动；
6. **命名一致性检查**：`make taxonomy-check` 必须通过，确保 `rules/taxonomies/default.yaml` 唯一事实源与 `pkg/naming`、`pkg/validation`、`engine-go` 三处派生口径完全一致（P1-5）；
7. **依赖洁净化**：严禁引入未经安全审计的第三方 CGO 库或冗余外部依赖。
