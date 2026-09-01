# PrivShield 四周学习、Review 与完善计划

> 本计划面向 AI 生成代码的人工 Review、学习与改进阶段。代码整体已由 AI 完成，但尚未经过系统性的人工审查与生产级打磨。
>
> 服务分级：
> - 商业应用版（生产级标准）：engine-go、services/audit-log、services/service-hub
> - 辅助测试版（基本功能可用）：services/datasource-mgr、console/app-lz

---

## 总体目标

通过四周时间，完成以下转变：

- 从"AI 生成可运行"到"人工理解可维护"
- 从"功能跑通"到"生产级健壮"
- 从"单服务可用"到"全链路协同"
- 从"基础安全"到"零信任合规"

---

## 周任务概览（汇报用）

### 第一周：engine-go 网关与安全体系 Review

**周目标**：完成 engine-go 核心模块的人工 Review，建立对网关流量治理、安全认证体系与可观测性实现的全局认知。

**关键任务**

- 审查 P2C-EWMA 负载均衡、熔断器三态转换、BufferPool 零分配代理、gRPC 透明代理的实现正确性
- 审查 mTLS CN 白名单热重载、API Key 常量时间认证、Scope-based 权限模型、32 分片令牌桶限流的安全性
- 审查 Prometheus RED 指标体系、OpenTelemetry 追踪集成、结构化日志的实现完整性

**周交付物**

- engine-go 网关与安全模块 Review 笔记（含 `// REVIEW:` 标注）
- 发现的问题清单与改进建议
- 可观测性改进方案（Span 创建、告警规则）

---

### 第二周：审计日志不可篡改体系与密码学基础 Review

**周目标**：深入理解并验证 `pkg/store` 与 `pkg/crypto` 的密码学实现，确认审计存证体系的安全基石正确无误。

**关键任务**

- 精读 9 要素完整性前映像结构、HMAC-SM3 密钥化存证、多轨兼容核验算法
- 精读 SM4-GCM 信封加密（v2 HKDF 派生、AAD 绑定、fail-closed 策略）
- 精读 BufferedAuditStore 微批刷盘器（单一权威机制、FIFO 保序、优雅停机）
- 审查 SQLite/PostgreSQL/内存三种存储后端实现与分层降级逻辑

**周交付物**

- 密码学实现 Review 报告（哈希链算法 + 信封加密）
- 手动推导文档（哈希计算过程、加解密过程）
- 存储后端 Review 笔记与改进建议

---

### 第三周：audit-log 与 service-hub 服务 Review + 跨服务集成

**周目标**：审查上层服务如何正确使用 pkg 层基础设施，验证跨服务集成的正确性与一致性。

**关键任务**

- 审查 audit-log 服务的归档留存红线、加密归档段管理、API 层安全特性
- 审查 service-hub 的流水线调度状态机、租约任务存储、崩溃恢复、审计证据绑定
- 全栈联调：service-hub -> engine-go -> audit-log 端到端链路验证
- 统一错误码体系，验证 app-lz BFF 聚合代理正确性

**周交付物**

- audit-log 与 service-hub Review 笔记
- 全栈集成测试用例与执行结果
- 跨服务集成问题清单与修复记录

---

### 第四周：可观测性闭环、性能压测与生产就绪

**周目标**：完成全链路可观测性闭环，通过性能压测与混沌演练验证系统韧性，完成生产就绪评审。

**关键任务**

- 补齐全链路追踪覆盖（BFF -> Hub -> Engine -> Audit）
- 梯度加压性能压测（各服务 QPS 拐点、内存/CPU 泄漏检测）
- 混沌工程故障注入演练（节点宕机、网络分区、存储故障、刷盘器饱和）
- 生产就绪评审（PRR）逐项核验

**周交付物**

- Grafana 全链路监控看板
- 性能基线报告
- 混沌演练报告
- 生产就绪评审签署单
- 运维应急手册（Runbook）

---

## 第一周：engine-go 网关与安全体系 Review

> 聚焦 engine-go 核心模块，以学习和理解为主，逐行阅读网关流量治理与安全认证体系。本周节奏偏慢，重在建立对代码库的全局认知。

### Day 1-2：网关与流量治理 Review

**审查范围**

`engine-go/internal/gateway/` 全部文件，以及 `pkg/gateway/balancer.go`。

**学习重点**

- P2C-EWMA（Power of Two Choices + Exponentially Weighted Moving Average）负载均衡算法的数学原理与实现细节。理解为何选择 P2C 而非简单轮询，EWMA 如何平滑后端负载指标
- 熔断器三态转换逻辑（Closed -> Open -> HalfOpen）的状态机设计，关注原子操作与竞态安全性
- HTTP 反向代理的 BufferPool 零分配机制，理解 `sync.Pool` 的使用模式与潜在泄漏风险
- gRPC 透明代理的 RawCodec 编解码与连接池管理（Max 256），理解双向字节级转发的工作原理
- 后端 TLS 构建（`backend_tls.go`）的证书校验链，理解 mTLS 客户端证书的加载与验证流程

**Review 方法**

- 先阅读测试文件（`http_proxy_test.go`、`grpc_proxy_test.go`），理解预期行为
- 再阅读实现文件，对照测试用例理解边界条件
- 对不理解的地方添加 `// REVIEW:` 注释，集中讨论

**关注问题**

- 连接池 Max 256 在高并发场景下是否足够
- 熔断器冷却时间是否硬编码，是否需要配置化
- BufferPool 在 panic 路径下是否正确回收

### Day 3-4：安全体系 Review（mTLS、认证、权限）

**审查范围**

- `engine-go/internal/security/` 全部文件
- `pkg/auth/identity.go`、`pkg/auth/middleware.go`、`pkg/auth/settings.go`
- `pkg/tlsutil/whitelist.go`、`pkg/tlsutil/grpc_interceptor.go`、`pkg/tlsutil/tlsutil.go`
- `pkg/middleware/ratelimit.go`、`pkg/middleware/auth.go`

**学习重点**

- mTLS CN 白名单热重载机制：`pkg/tlsutil/whitelist.go` 中的 5s 节流、两阶段提交（atomic swap）实现。理解为何需要节流（防止频繁文件变更触发重载风暴）以及两阶段提交如何保证零停机
- API Key 认证：`pkg/auth/middleware.go` 中的常量时间比较（`hmac.Equal` / `subtle.ConstantTimeCompare`），理解为何必须使用常量时间比较（防止时序攻击）
- Scope-based 权限模型：`pkg/auth/identity.go` 中 `PermissionForRESTPath` 与 `PermissionForGRPCMethod` 的映射规则，理解 internal/external 身份分离的设计意图
- 32 分片令牌桶限流：`pkg/middleware/ratelimit.go` 中的分片策略，理解为何分片（减少锁竞争）以及分片 key 的选择（IP + Identity）
- 安全头中间件：CSP、HSTS、X-Content-Type-Options 等 HTTP 安全头的覆盖范围

**关注问题**

- mTLS CN 白名单配置字段存在（`MTLSEnabled`、`MTLSAllowedCNs`），但需确认是否已接入 Gin 主中间件链
- 权限模型为静态 scope 定义，REST 路径到权限的映射是否有遗漏
- API Key 无过期机制，生产环境需要轮转能力
- 限流完全基于内存，多实例部署时无法共享限流状态

### Day 5：可观测性 Review（日志、指标、追踪）

**审查范围**

- `engine-go/internal/observability/` 全部文件
- `pkg/observability/metrics.go`、`pkg/observability/tracing.go`、`pkg/observability/logger.go`、`pkg/observability/request_logger.go`
- `pkg/middleware/trace.go`

**学习重点**

- Prometheus RED 指标体系：`pkg/observability/metrics.go` 中的 `REDMetrics`（Rate、Errors、Duration），理解 RED 方法论的核心思想
- `EngineMetrics` 在 RED 基础上扩展的引擎特定指标（分类计数、NER 推理耗时、别名请求数）
- OpenTelemetry 追踪集成：`pkg/observability/tracing.go` 的 `OTelTracer` 与 `NoOpTracer` 接口设计，理解 W3C TraceContext 的传播机制
- 结构化日志：`pkg/observability/logger.go` 基于 `log/slog` 的 JSON 结构化输出，理解 request_id、trace_id 的注入方式
- Gin 中间件与 gRPC 拦截器的指标上报：`RequestLogger`、Gin metrics middleware、gRPC stats handler

**关注问题**

- 追踪模块当前为薄封装，引擎内部（分类漏斗各阶段）是否创建了独立 Span
- 审计关键事件（分类命中 LLM、熔断器状态变更、mTLS 证书即将过期）是否输出标准化日志
- 缺少 Prometheus 告警规则定义（`deploy/prometheus/rules/`）

---

## 第二周：审计日志不可篡改体系与密码学基础 Review

> 深入理解 `pkg/store` 与 `pkg/crypto` 的密码学实现，这是整个审计存证体系的安全基石。本周内容密度最高，需要逐行精读。

### Day 1-2：不可篡改哈希链算法精读

**审查范围**

- `pkg/store/audit_hash.go` — 哈希链计算与核验的唯一权威实现
- `pkg/store/store.go` — `AuditLog`、`SnapshotRecord`、`ChainVerificationResult` 数据模型与 `AuditStore` 接口定义
- `pkg/crypto/sm3.go` — 国密 SM3 杂凑算法的纯 Go 实现

**学习重点**

- 9 要素完整性前映像结构：`prev_hash|log_id|timestamp_utc|algorithm|input_hash|output_hash|user|security_level|params_json`。理解为何选择这 9 个字段（覆盖链锚点、身份标识、数据指纹、操作参数），以及为何用 `|` 分隔而非 JSON 序列化（确定性编码，消除字段顺序歧义）
- UTC 纳秒级时间戳归一化：`time.RFC3339Nano` 格式。理解为何必须 UTC 归一化（PostgreSQL TIMESTAMPTZ 丢失写入方时区偏移会导致核验端算出不同前映像，产生伪分叉）
- 密钥化 HMAC-SM3 存证（P1-2）：配置 `AUDIT_LOG_HASH_KEY` 后采用 `HMAC-SM3(key, "SM3-HMAC:v1|" + 9要素前映像)`。理解密钥化 vs 无密钥的区别 — 无密钥 SM3 仅证明"内容未被修改"（知道口径者可重算），密钥化 HMAC-SM3 使未持有密钥者无法伪造或改写记录
- 向下兼容多轨核验：`VerifyAuditIntegrityHash` 依次尝试「密钥化 HMAC-SM3 -> 无密钥 SM3-UTC -> SHA-256-UTC -> SM3-LocalTZ -> SHA-256-LocalTZ」5 种候选。理解为何需要兼容（加密产品认证前写入的历史证据仍需合法可验）
- 快照独立完整性哈希：`ComputeSnapshotIntegrityHash` 将快照自身字段（输入/输出样本密文）绑定到防篡改链，而非简单继承主日志哈希。理解这样设计的原因 — 替换样本应被检测
- 链核验结论枚举（P2-4）：`ChainReasonOK`、`ChainReasonLegacyHashed`、`ChainReasonTamperedPayload`、`ChainReasonBrokenChain`、`ChainReasonMissingPrev`、`ChainReasonMissingRecords`。理解每种断链类型的检测条件与业务含义
- 规范化链序：`(seq ASC, timestamp ASC, id ASC)` 回放规则。理解为何需要规范化（保证同时间戳记录在任何后端与工具上都以同一顺序回放，不产生伪分叉）

**Review 方法**

- 对照 `pkg/store/audit_hash_test.go` 理解测试覆盖的边界条件
- 手动推导一条审计日志的哈希计算过程（从 9 要素前映像到最终 hex 编码）
- 验证 `VerifyAuditIntegrityHash` 的 5 种候选尝试顺序是否合理

### Day 3：SM4-GCM 信封加密与密钥派生精读

**审查范围**

- `pkg/crypto/envelope.go` — SM4-GCM 信封加密/解密实现
- `pkg/crypto/sm4.go` — 国密 SM4 分组密码算法的纯 Go 实现

**学习重点**

- SM4-GCM（GB/T 32907-2016）工作模式：128 位分组长度、128 位密钥长度、GCM 认证加密（AEAD）。理解 GCM 模式如何同时提供机密性与完整性保护
- v2 信封格式：`enc:v2:<Base64(16字节 salt + 12字节 Nonce + SM4 密文 + 16字节 Tag)>`。理解每个字段的作用 — salt 用于 HKDF 密钥派生（逐记录独立密钥）、Nonce 为 GCM 标准 96-bit 随机数、Tag 为 128-bit 认证标签
- HKDF-SM3 密钥派生（RFC 5869）：`DeriveKeyHKDF` 从口令与逐记录随机 salt 派生 16 字节 SM4 密钥。理解 Extract-then-Expand 两阶段设计，以及为何需要逐记录 salt（使同一口令在不同记录上产出互不相同的加密密钥，抵抗离线暴破）
- 版本前缀参与 AAD：`gcm.Seal(nonce, nonce, plaintext, []byte(EncryptedPrefixV2))`。理解为何前缀参与 AAD — 剥离/改写 `enc:vN:` 前缀会直接导致认证失败，消除"去前缀即降级为明文"的静默通道
- v1 历史兼容：`enc:v1:` 格式使用 `SHA-256(secret)[:16]` 弱派生，仅保留解密能力。理解为何不再写入（短口令直接哈希截断属于弱派生，易被暴破）
- Fail-closed 安全策略：空密钥时 `EncryptString` 返回 `ErrEmptyKey`（不再静默降级为明文）；无前缀密文返回 `ErrUnencryptedValue`（防止剥离前缀降级）

**Review 方法**

- 对照 `pkg/crypto/envelope_test.go` 理解加解密的往返测试与边界条件
- 手动推导一条密文的生成过程（salt 生成 -> HKDF 派生密钥 -> GCM Seal -> Base64 编码）
- 验证 v1/v2 格式的兼容性（用 v1 格式加密的数据能否被正确解密）

### Day 4：微批刷盘器（BufferedAuditStore）精读

**审查范围**

- `pkg/store/flusher/flusher.go` — 内存缓冲微批异步刷盘器

**学习重点**

- 单一权威机制（Single Authority）：链尾 `prev_hash` 与 `integrity_hash` 只能由刷盘器在服务端单点裁定，入队即在锁内确定并同步写回调用方指针。理解为何需要单一权威（消除日志行、快照行与 HTTP/gRPC 响应体的哈希分叉）
- 严格 FIFO 保序入队：链推进与入队成功在临界区内原子完成，队列拥塞时按 `EnqueueTimeout`（默认 500ms）有界等待。理解为何必须保序（乱序落盘会导致断链）
- 持久性优先于吞吐：底层写入失败时整批保留在工作线程暂存区（retry backlog），下一轮按原序优先重投，绝不丢弃已确认记录。理解退避重试策略（25ms * 2^attempt）
- 生命周期无竞态停机：`closed` 状态受 `stateMu` 互斥量保护，停机先置位再关信号。`Close` 之后的入队必被拒绝。排空超时则如实报告搁浅条数，且不关闭底层存储（避免被抛弃的工作线程写入已关闭句柄）
- Flush 强一致性屏障：`Flush` 返回 nil 当且仅当队列与工作线程暂存区均已清空并成功提交。理解为何 `ListLogs`/`GetStats`/`VerifyChain` 等读路径需要先 `Flush`（防止数据未落盘时给出虚假结论）
- 内存有界防 OOM：读己之写暂存映射（`recentLogs`）受 `MaxStaged`（默认 50000）约束并按入队序淘汰最旧条目。重试暂存区同样有界，超限后快速拒绝新写入
- 配置参数理解：`BufferSize`（10000）、`MaxBatchSize`（200）、`FlushInterval`（20ms）、`EnqueueTimeout`（500ms）、`FlushTimeout`（5s）、`CloseTimeout`（10s）、`MaxRetries`（3）、`MaxStaged`（50000）

**Review 方法**

- 对照 `pkg/store/flusher/flusher_test.go` 理解测试覆盖的场景（正常刷盘、存储故障重试、优雅停机、积压饱和拒绝）
- 画出 `flushWorker` 的事件循环流程图（ticker 触发、队列接收、Flush 屏障、停止信号）
- 验证 `SaveLogWithSnapshot` 的锁持有范围（`stateMu` 保护 `closed` + `lastHash`，`stageMu` 保护 `recentLogs`）

### Day 5：存储后端实现 Review

**审查范围**

- `pkg/store/sqlite/audit.go`、`pkg/store/sqlite/init.go` — SQLite 审计存储实现
- `pkg/store/postgres/audit.go`、`pkg/store/postgres/schema.go` — PostgreSQL 审计存储实现
- `pkg/store/memory/memory.go` — 内存审计存储实现（测试用）
- `pkg/store/levels.go` — 分层存储降级逻辑
- `pkg/store/cmd/repairchain/main.go` — 哈希链重签工具

**学习重点**

- SQLite 实现中的事务管理（`BEGIN IMMEDIATE` 防写锁升级）、WAL 模式配置、并发读优化
- PostgreSQL 实现中的写入只角色自检、连接池配置、`FOR UPDATE SKIP LOCKED` 租约抢占
- 分层降级逻辑（PostgreSQL -> SQLite -> 内存）的触发条件与数据一致性保障
- `repairchain` 重签工具的工作原理：读取存量记录 -> 识别非规范哈希标签 -> 用当前写入口径重新计算并覆盖

**Review 方法**

- 对照各后端的测试文件理解行为预期
- 验证 `VerifyChain` 在不同后端上的链序一致性（`(seq ASC, timestamp ASC, id ASC)` 是否被严格遵守）

---

## 第三周：audit-log 与 service-hub 服务 Review + 跨服务集成

> 在第二周深入理解 pkg 层密码学与存储基础后，本周审查上层服务如何正确使用这些基础设施，并验证跨服务集成。

### Day 1-2：audit-log 审计日志服务 Review

**审查范围**

`services/audit-log/` 全部源码，包括 `cmd/server/`、`internal/agent/`、`internal/archive/`、`internal/config/`、`internal/grpcserver/`、`internal/handlers/`、`internal/models/`。

**学习重点**

- 服务启动流程：配置加载 -> 存储后端初始化 -> BufferedAuditStore 包装 -> HTTP/gRPC 服务启动 -> 优雅停机钩子
- Agent 客户端封装：如何调用 engine-go 执行脱敏，并将结果写入审计日志
- 归档留存红线（P0-8）：`internal/archive/` 中的"先归档后删除"机制。理解 `FetchOldestForArchive` -> 归档段加密落盘 -> `DeleteLogsByIDs` 的三步流程，以及 fail-closed 语义（归档失败则不删除）
- 加密归档段的密钥管理：归档加密密钥的来源（环境变量注入），与审计链 HMAC 密钥的关系
- API 层：REST 与 gRPC 双协议暴露，审计日志查询的分页与过滤能力
- 安全特性：mTLS CN 白名单拦截器、API Key 认证（writer/reader 分离）、32 分片限流、Slowloris 防护

**关注问题**

- 归档加密密钥是否已对接环境变量注入
- 审计日志查询 API 是否支持 cursor-based 分页与时间范围过滤
- 写入-only Postgres 角色的自检是否覆盖所有写入路径

### Day 3-4：service-hub 服务调度中枢 Review

**审查范围**

`services/service-hub/` 全部源码，包括 `internal/agent/`、`internal/audit/`、`internal/config/`、`internal/datasource/`、`internal/grpcserver/`、`internal/handlers/`、`internal/models/`、`internal/retry/`。

**学习重点**

- 流水线调度状态机：任务创建（pending）-> 领取（running，带租约）-> 完成（completed）/ 失败（failed，可重试则回退 pending）的完整生命周期
- 租约任务存储（`LeasedTaskStore`）：`ClaimNext`（FOR UPDATE SKIP LOCKED 原子抢占）、`RenewLease`（条件续期）、`CompleteLease`（条件完成）、`FailLease`（条件失败 + 自动重试判定）、`RequeueExpiredLeases`（过期租约回收）
- 崩溃恢复：进程重启后如何检测并恢复"进行中"但已失去租约的任务
- 后台定期重试：`internal/retry/` 的重试策略，理解指数退避算法与错误分类（retryable vs non-retryable，基于 `ErrorClass` 字段判定）
- 审计日志证据绑定（P0-6）：任务执行完成后如何将审计日志的 `integrity_hash` 绑定到任务记录，形成可追溯的证据链
- 数据保留清理：`CleanupOld` 的安全边界（仅清理终态且超过保留期的任务）

**关注问题**

- 租约抢占在多副本场景下的竞态安全性（PostgreSQL `FOR UPDATE SKIP LOCKED` 是否正确使用）
- 崩溃恢复是否覆盖所有中间状态
- 审计日志证据绑定是否在事务中完成（防止任务完成但证据未绑定）

### Day 5：跨服务集成与端到端联调

**任务内容**

- 启动全栈服务（engine-go + audit-log + service-hub + datasource-mgr + app-lz），验证端到端流程
- 测试核心链路：service-hub 调度脱敏任务 -> engine-go 执行 -> audit-log 记录审计日志 -> 哈希链完整性验证
- 验证 audit-log 证据链签名在跨服务传递过程中的完整性（service-hub 记录的 `integrity_hash` 与 audit-log 中的是否一致）
- 检查 app-lz BFF 层对四个上游服务的聚合代理（超时传递、错误码映射、响应合并）

**改进任务**

- 补充全栈集成测试（`tests/e2e/`），覆盖核心业务流程
- 统一各服务的错误码体系（`pkg/middleware/envelope.go` 的 5 字段信封是否被所有服务一致使用）
- app-lz BFF 层补充上游服务健康检查聚合（任一上游不可用时返回降级响应而非 502）

---

## 第四周：可观测性闭环、性能压测与生产就绪

> 前三周完成了代码 Review 与改进，本周聚焦全链路可观测性闭环、性能验证与生产准入评审。

### Day 1：全链路可观测性闭环

**审查范围**

从客户端请求到最终响应的全链路追踪覆盖度。

**审查重点**

- app-lz BFF 层是否生成或透传 W3C `traceparent`
- service-hub 调度任务时是否将 trace context 通过 gRPC metadata 传递至 engine-go
- engine-go 内部各阶段（分类漏斗 Rule -> NER -> LLM）是否创建独立 Span
- audit-log 写入审计条目时是否关联 `trace_id`（`AuditLog` 结构体中已有 `TaskID` 字段，需确认是否关联 trace）
- 所有 Span 是否正确上报至 OpenTelemetry Collector

**改进任务**

- app-lz BFF 层补充 trace context 生成与透传逻辑
- engine-go 内部为分类漏斗三阶段创建子 Span，形成可观测的调用链火焰图
- 在 Grafana 中构建统一看板：展示请求链路（BFF -> Hub -> Engine -> Audit）的完整拓扑与耗时分布
- 补充 Prometheus 告警规则（`deploy/prometheus/rules/`）：P99 延迟 > 5s、错误率 > 1%、熔断器 Open、mTLS 证书 7 天内过期、审计日志刷盘器积压饱和

### Day 2-3：性能基线与极限压测

**压测计划**

使用 `hey` / `wrk` / `Locust` 对各服务进行梯度加压。

压测场景覆盖：

- engine-go 单接口脱敏（纯 CPU 计算型，关注 P95/P99 延迟）
- engine-go 分类漏斗（含 LLM 调用，IO 密集型，关注熔断器触发频率）
- service-hub 任务调度（高并发任务创建与查询，关注租约抢占吞吐）
- audit-log 审计写入（高吞吐写入场景，关注 BufferedAuditStore 的刷盘延迟与积压深度）
- app-lz BFF 聚合代理（多上游并发调用，关注超时传播与响应合并延迟）

**关注指标**

- QPS 吞吐拐点（P99 延迟开始急剧上升的临界点）
- 内存/CPU 使用率曲线（是否存在泄漏）
- 连接池利用率（是否出现连接等待）
- BufferedAuditStore 的 `QueueDepth`、`RetryPending`、`OverflowTotal` 指标
- 熔断器触发频率与恢复时间

**改进任务**

- 根据压测结果调优连接池大小、超时时间、批量处理参数
- 识别并修复性能瓶颈（如锁竞争、GC 压力、网络序列化）
- 输出性能基线报告（`docs/benchmarks/performance_baseline.md`）

### Day 4：混沌工程故障注入演练

**演练场景**

- 节点宕机：随机 `kill -9` engine-go 实例，验证 service-hub 的任务重试与 audit-log 的 BufferedAuditStore 重试暂存区正确工作
- 网络分区：模拟 app-lz 与上游服务网络中断，验证 BFF 层的降级响应
- 存储故障：模拟 PostgreSQL 不可用，验证 audit-log 降级到 SQLite 的正确性，以及分层存储降级逻辑是否触发
- 慢依赖：模拟 engine-go 响应延迟 10s，验证 service-hub 的超时控制与熔断器行为
- 刷盘器饱和：模拟审计写入速率持续超过刷盘速率，验证 `ErrBacklogSaturated` 快速拒绝是否生效

**改进任务**

- 根据演练结果修复发现的容灾缺陷
- 补充自动化混沌测试脚本（`scripts/chaos/`）
- 输出混沌演练报告（`docs/chaos/chaos_engineering_report.md`）

### Day 5：生产就绪评审（PRR）与收尾

**评审清单**

逐项核验以下生产准入标准：

- 所有商业服务（engine-go、audit-log、service-hub）代码已被人工完整 Review
- 不可篡改哈希链算法经人工验证，9 要素前映像、HMAC-SM3 密钥化、多轨兼容核验均正确
- SM4-GCM 信封加密实现经人工验证，v2 格式 HKDF 派生、AAD 绑定、fail-closed 策略均正确
- BufferedAuditStore 的单一权威机制、FIFO 保序、优雅停机经测试验证
- 全链路追踪覆盖核心业务流程，可在 Jaeger 中查看完整调用链
- 监控看板与告警规则已部署并验证
- 混沌演练通过，系统具备自动容灾自愈能力
- 安全审计日志完整，满足等保三级追溯要求
- 部署脚本（Helm/K8s）经过验证，支持一键回滚
- 运维手册（Runbook）已编写，包含常见故障处理流程

**交付物**

- 性能基线报告
- 混沌演练报告
- 生产就绪评审签署单
- 运维应急手册
- 全栈部署验证报告

---

## 辅助测试服务维护策略

> services/datasource-mgr 和 console/app-lz 定位为辅助测试，不做生产级加固，仅确保基本功能可用。

### datasource-mgr 维护策略

- 保持当前实现不变，仅修复阻断性 Bug
- CSV 数据加载增加基本的文件大小限制（防止测试时误加载超大文件）
- 确保与 app-lz BFF 的对接正常
- 不补充额外的生产级特性（如持久化存储、分布式限流）

### console/app-lz 维护策略

- 保持当前 HTTP-only BFF 架构，不引入 gRPC Server
- 确保四个上游服务的聚合代理逻辑正确
- 补充上游服务健康检查聚合
- 不引入额外的认证/权限层（依赖上游服务的安全控制）
- 前端控制台（console/web）仅做基本功能验证，不做 UI/UX 优化

---

## 每日工作节奏建议

**上午（9:00 - 12:00）**

- 代码 Review：逐行阅读当天计划的模块，理解实现逻辑
- 记录疑问与改进点：使用 `// REVIEW:` 注释标记需要讨论的代码段
- 对照测试文件理解预期行为与边界条件

**下午（14:00 - 18:00）**

- 改进实现：根据 Review 发现的问题进行代码修改
- 补充测试：为 Review 过程中发现的边界条件补充单元测试
- 集成验证：验证修改后的代码与上下游服务的集成

**晚间（可选）**

- 学习相关技术：国密标准（SM3/SM4）、分布式系统设计、OpenTelemetry 规范
- 代码重构：对 Review 过程中发现的代码异味进行小范围重构

---

## 关键风险与应对

**风险 1：密码学实现存在隐藏缺陷**

- 应对：第二周对 SM3 哈希链与 SM4-GCM 信封加密进行逐行人工审查
- 应对：手动推导计算过程，对照国标文档验证实现正确性
- 应对：补充边界条件测试（空密钥、空明文、超长输入、前缀篡改）

**风险 2：微批刷盘器在极端场景下丢数据**

- 应对：第四周混沌演练中模拟刷盘器饱和场景
- 应对：验证优雅停机时所有缓冲数据已落盘
- 应对：确认 `ErrBacklogSaturated` 快速拒绝不会导致调用方静默丢失

**风险 3：跨服务集成出现兼容性问题**

- 应对：第三周 Day 5 全栈联调，尽早暴露集成问题
- 应对：统一错误码体系（`pkg/middleware/envelope.go` 的 5 字段信封）与日志格式

**风险 4：性能瓶颈难以定位**

- 应对：从第一天起就启用 pprof 与追踪，建立性能基线
- 应对：使用火焰图与调用链分析定位瓶颈，避免盲目优化

---

## 成功标准

四周结束后，系统应达到以下状态：

- 所有商业服务代码已被人工完整 Review，核心模块逐行理解
- 不可篡改哈希链算法与 SM4-GCM 信封加密经人工验证正确
- BufferedAuditStore 的单一权威机制与优雅停机经测试验证
- 全链路可观测性闭环，可在 5 分钟内定位任何性能或功能问题
- 通过性能压测与混沌演练，系统具备自动容灾自愈能力
- 安全审计日志完整，满足等保三级追溯要求
- 运维手册完整，On-Call 人员可独立处理常见故障
- 辅助测试服务功能正常，不影响开发调试效率
