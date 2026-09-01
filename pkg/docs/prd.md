# PrivShield 共享基础包 (Shared PKG) — 产品与技术需求规格说明书 (PRD)

> **文档定位**：定义 `pkg` 共享基础设施包的架构定位、业务目标、功能需求、非功能需求、密评合规要求与交付验收指标。

---

## 目录

- [一、需求背景与业务定位](#一需求背景与业务定位)
- [二、系统架构与功能性需求 (Functional Requirements)](#二系统架构与功能性需求-functional-requirements)
  - [2.1 国密商用密码与防篡改存证需求 (FR-1 ~ FR-2)](#21-国密商用密码与防篡改存证需求-fr-1--fr-2)
  - [2.2 高并发微批缓冲与归档保留调度需求 (FR-3 ~ FR-4)](#22-高并发微批缓冲与归档保留调度需求-fr-3--fr-4)
  - [2.3 纵深防御与通信韧性需求 (FR-5 ~ FR-7)](#23-纵深防御与通信韧性需求-fr-5--fr-7)
  - [2.4 零侵入动态 mTLS 鉴权需求 (FR-8)](#24-零侵入动态-mtls-鉴权需求-fr-8)
  - [2.5 数据源命名 SSOT 与分类分级标准需求 (FR-9)](#25-数据源命名-ssot-与分类分级标准需求-fr-9)
  - [2.6 安全门控零信任默认姿态需求 (FR-10)](#26-安全门控零信任默认姿态需求-fr-10)
  - [2.7 哈希链 HMAC-SM3 密钥化存证需求 (FR-11)](#27-哈希链-hmac-sm3-密钥化存证需求-fr-11)
  - [2.8 分类分级标准体系加载需求 (FR-12)](#28-分类分级标准体系加载需求-fr-12)
- [三、非功能性需求 (Non-Functional Requirements)](#三非功能性需求-non-functional-requirements)
  - [3.1 性能与吞吐指标](#31-性能与吞吐指标)
  - [3.2 高可用与数据一致性指标](#32-高可用与数据一致性指标)
  - [3.3 密评三级合规与安全性指标](#33-密评三级合规与安全性指标)
  - [3.4 纯 Go 跨平台构建与可维护性指标](#34-纯-go-跨平台构建与可维护性指标)
- [四、验收标准与质量基线](#四验收标准与质量基线)

---

## 一、需求背景与业务定位

在政务云大数据流通与医疗健康隐私计算场景下，系统面临高并发数据接入、跨域不可篡改审计、多节点无锁任务调度以及等保/密评严格合规的多重挑战。

`pkg` 模块定位为 `PrivShield` 全栈工程的**公共基础设施与安全内核基座**，消除重复造轮子，为上层微服务（`service-hub`、`datasource-mgr`、`audit-log`、`bff-go`）提供统一、经过高强度验证的基础组件。

---

## 二、系统架构与功能性需求 (Functional Requirements)

### 2.1 国密商用密码与防篡改存证需求

* **FR-1：标准国密 SM3 完整性存证**
  * 必须遵循 **GM/T 0004-2012** 规范，提供纯 Go 优化的 SM3 摘要计算；
  * 必须支持将 9 项核心要素（前序哈希、日志ID、UTC纳秒时间戳、算法、输入指纹、输出指纹、操作人、密级、参数）组合为标准规范前像；
  * 必须提供双模平滑核验机制，在升级至 SM3 的同时兼容历史存量数据的离线验真。
* **FR-2：国密 SM4-GCM 动态信封加密（enc:v2 HKDF-SM3）**
  * 存证落盘与跨域传输的敏感样本数据（如原始脱敏前后样例）必须强制采用 **SM4-GCM** 认证加密，当前写入格式为 **`enc:v2:`**；
  * **enc:v2 密钥派生**：每条记录生成 16 字节密码学安全随机 salt，通过 **HKDF-SM3**（RFC 5869：Extract = HMAC-SM3(salt, secret) → PRK；Expand = 迭代 HMAC-SM3(prk, prev || info || counter)）派生 16 字节 SM4 密钥。HKDF info 字段为 `"PrivShield audit snapshot SM4-GCM v2"`，实现版本域分离；
  * **enc:v2 线路格式**：`enc:v2:<Base64( 16-byte salt || 12-byte nonce || SM4 密文 || 16-byte GCM Auth Tag )>`。版本前缀 `enc:v2:` 作为 GCM AAD 参与认证，杜绝版本降级攻击（剥离或替换 `enc:vN:` 前缀将导致认证失败）；
  * **enc:v1 遗留兼容**：`enc:v1:<Base64( 12-byte nonce || SM4 密文 || 16-byte GCM Auth Tag )>`，密钥派生为 `SHA-256(secret)[:16]`，仅支持解密读取，不再用于新写入；
  * **空密钥哨兵**：当加密密钥未配置（空字符串）时，`EncryptString` 与 `decryptV1`/`decryptV2` 必须返回 `ErrEmptyKey` 哨兵错误（`"crypto: encryption key is not configured"`），防止使用全零密钥意外加密；
  * **加密判定函数**：`IsEncrypted(value string) bool` 检测值是否携带 `enc:v1:` 或 `enc:v2:` 前缀，替代旧版 `IsEnvelopeEncrypted`。

### 2.2 高并发微批缓冲与归档保留调度需求

* **FR-3：高并发微批缓冲与归档保留存证管理 (`BufferedAuditStore`)**
  * 必须在高并发多协程写入场景下，将单条落盘折叠为定量（最大 200 条）或定时（20ms）的批量写入；
  * **哈希链连续性约束**：所有微批内记录的 `PrevHash` 绑定与摘要重算必须在单一 Worker 协程中串行依次执行，杜绝多协程交错导致哈希链断裂；
  * **读己之写保证**：写入队列的数据在未落盘前必须立即在内存中可查（`GetLog` / `GetLatestLog`）；
  * **优雅停机零丢失**：服务收到关闭信号时，必须拦截新请求并同步排空队列缓冲区，确保零记录丢失；
  * **归档保留策略 (Archive-Before-Delete)**：
    * 通过环境变量 `AUDIT_LOG_RETENTION_DAYS` 控制审计日志保留天数，**默认值为 0（永不删除）**；
    * 当配置值 `> 0` 时，**最低保留 1095 天（3 年）**，低于此阈值的配置将被拒绝并报错：`"destroys evidence below the 1095-day (3-year) retention floor; use 0 to disable deletion"`；
    * 启用保留删除时，必须同时配置 `AUDIT_LOG_ARCHIVE_DIR`（归档目录）与 `AUDIT_LOG_ENCRYPTION_KEY`（加密密钥），否则校验失败；
    * 删除流程必须遵循 **先归档再删除 (Archive-Before-Delete)** 模式：通过 `AuditArchiveReader` 接口的 `FetchOldestForArchive(before, limit)` 分页获取过期记录（含关联快照），归档至外部存储后，再通过 `DeleteLogsByIDs(ids)` 按 ID 精确级联删除。归档页大小默认 500 条（`DefaultArchivePageSize`），ID 分片删除粒度为 500（`ArchiveIDChunkSize`），确保归档与删除严格一致。
* **FR-4：PostgreSQL Phase B 分布式原子租约调度**
  * 针对多副本部署，必须基于 PostgreSQL `FOR UPDATE SKIP LOCKED` 短事务实现无死锁的任务原子竞争领取；
  * 写回任务状态时必须携带 `(id, owner, token)` 租约令牌校验，防止网络分区与节点延迟引发的脏写覆盖；
  * 提供租约自动过期与健康节点平滑接管机制。

### 2.3 纵深防御与通信韧性需求

* **FR-5：Gin 8 层标准化安全中间件栈**
  * 统一提供 8 层安全中间件，按注册顺序依次为：
    1. **TraceMiddleware** — 全链路追踪（`X-Trace-ID` / `X-Request-ID` 注入与传播）；
    2. **Recovery** — Panic 恢复，内部记录完整堆栈，外部返回安全 500 信封；
    3. **SecurityHeaders** — 安全响应头（CSP / HSTS 1年 / X-Frame-Options / X-Content-Type-Options / Referrer-Policy / Permissions-Policy）；
    4. **MaxBodySize** — 请求体防爆（32MB / 64MB 可配置）；
    5. **MaxConcurrent** — 全局并发控制（默认 1000 连接，超限返回 503）；
    6. **RateLimit** — 按客户端 IP 令牌桶限流（可配置 RPS / Burst，条件启用）；
    7. **CORS** — 白名单跨域（精确匹配或 `*` 开发模式）；
    8. **Auth / AuthWithRoles** — 常量时间比对鉴权（`subtle.ConstantTimeCompare`），支持角色分级（Reader/Writer 分离）。
  * **`/metrics` 端点必须纳入鉴权**（P1-6 安全要求），健康探针路径（`/health`、`/readyz`、`/api/health`）豁免，非 `/api/` 前缀的其余路径豁免认证。
* **FR-6：上游 Agent 弹性通信客户端（多节点集群 + 逐端点熔断）**
  * **多节点集群配置**：通过 `Config.BaseURLs []string` 配置多个 Agent 端点地址，`PickEndpoint()` 以原子轮询（atomic round-robin）方式选取下一个健康节点，`BaseURL` 单节点字段仅作向后兼容回退；
  * **逐端点独立熔断器**：每个端点维护独立的 `circuitbreaker.Breaker` 实例，故障阈值默认连续 5 次触发熔断，冷却期默认 30s 后半开自愈探测。`allowRequest(endpoint)` 检查：Closed 放行、Open 检查冷却到期转 HalfOpen、HalfOpen 放行探测请求；
  * **指数退避重试与故障转移**：默认 3 次重试，退避间隔 `baseDelay * 2^(attempt-1) + random(0, delay/2)`。重试时优先故障转移至不同健康节点（`retryEndpoint(exclude)`），单节点时回退至同节点重试；
  * **三哨兵错误体系**：
    - `ErrEndpointUnavailable`（`"no agent endpoint available"`）— 无可用端点（配置为空或全部被排除）；
    - `ErrCircuitOpen`（`"circuit breaker open (cooldown remaining)"`）— 目标节点熔断器处于 Open 冷却期；
    - `ErrTransport`（`"agent transport failure"`）— 出站 I/O 失败，通过 `transportError` 包装根因（超时 / 连接拒绝 / 连接重置 / 意外 EOF / 连接关闭），支持 `errors.Is` 语义匹配；
  * **诊断接口**：`EndpointStates() map[string]string` 返回每个节点的熔断器状态快照，供运维面板实时展示；
  * **响应体防爆**：单次响应体通过 `io.LimitReader` 限制 64 MiB，防止内存溢出。
* **FR-7：Prometheus 指标防基数爆炸治理**
  * 采集接口 QPS、延迟分位数（Histogram）、处理中并发与存储刷盘状态；
  * 强制采用路由模板（如 `/api/audit/logs/:id`）归一化打标，未知路径统一收敛为 `NOT_FOUND`。

### 2.4 零侵入动态 mTLS 鉴权需求

* **FR-8：国密 TLS 1.3 双向认证与 CN 白名单热加载**
  * 提供服务端/客户端 mTLS 证书配置加载器与公钥固定（SPKI）；
  * 提供基于 YAML 配置的 CN 白名单权限校验，支持文件修改毫秒级热生效，无需重启进程。

### 2.5 数据源命名 SSOT 与分类分级标准需求

* **FR-9：数据源命名唯一真相源 (Registry SSOT) 与安全分级体系**
  * **Registry 唯一真相源**：`naming.Registry` 以 `[]Entry` 切片作为所有数据源标识的 SSOT，每个 Entry 包含 `DataSourceID`（规范标识）、`APICode`（API 路由码）、`Status`（active/reserved）、`Fields`（字段清单）及 `Aliases`（别名集合）。`init()` 阶段构建三组 O(1) 查找索引：`byDataSourceID`、`byAPICode`、`aliasIndex`；
  * **别名冲突检测**：初始化时自动检测别名冲突（同一别名映射到多个不同数据源），冲突将触发 panic 阻断启动，防止静默歧义；
  * **4 级优先级规范化**：`Normalize(raw)` 按以下顺序解析：规范 DataSourceID → API Code → 大小写不敏感别名 → 精确别名 → 失败关闭错误。保留状态数据源返回 `ErrReservedDataSource`，未知标识返回 `ErrUnknownDataSource`；
  * **Observer 接口**：`naming.Observer` 接口提供 `RecordAPIAlias(alias, canonical, target)` 与 `RecordNormalizeError(reason)` 两个观测点，用于外部接入可观测性管道（Prometheus / 结构化日志）。全局 Observer 通过 `SetObserver(o)` / `CurrentObserver()` 管理，`sync.RWMutex` 保护并发安全；
  * **安全分级分类体系 (L1-L5)**：定义 5 级安全等级：
    | ID | 规范名 | 中文标签 | 等级 |
    |---|---|---|---|
    | L1 | public | 公开数据 | 1 |
    | L2 | internal | 内部数据 | 2 |
    | L3 | confidential | 敏感数据 | 3 |
    | L4 | secret | 高敏感数据 | 4 |
    | L5 | top_secret | 极敏感数据 | 5 |
  * **`NormalizeSecurityLevelID(level string) string`**：接受任意大小写拼写（L1-L5 或规范名称），返回 L1~L5 标识符。未知输入返回空字符串 `""`（失败关闭，永不静默默认）。`SecurityLevelRank` 返回 1-5 排名，`MaxSecurityLevelID` 返回输入集合中的最高等级。

### 2.6 安全门控零信任默认姿态需求

* **FR-10：安全门控验证 (ValidateFailClosed)**
  * **SecurityRequirements 结构体**：定义服务启动安全前置条件，包含 `ServiceName`、`Hosts`（监听地址）、`APIKey`、`TLSEnabled`、`RequireTLS`、`GRPCEnabled`、`MTLSWhitelistFile`、`EncryptionKey`、`RequireEncryptionKey`、`HashKey`、`RequireHashKey`；
  * **零信任默认姿态**：所有安全要求默认开启，非回环地址暴露时必须满足全部安全条件，否则进程拒绝启动（Fail-Closed）；
  * **回环地址检测**：`IsLoopbackHost()` 对空字符串、`"localhost"`、`127.0.0.0/8`、`::1` 返回 true；对 `"0.0.0.0"`、`"::"`、`"*"`、具体网卡 IP 返回 false（失败关闭）；
  * **5 个哨兵错误**：
    - `ErrAPIKeyRequired` — 非回环地址暴露但未配置 API Key；
    - `ErrTLSRequired` — 要求 TLS 但未启用；
    - `ErrMTLSWhitelistRequired` — gRPC TLS 已启用但未配置 CN 白名单文件；
    - `ErrEncryptionKeyRequired` — 非回环暴露且需要加密但未配置快照加密密钥；
    - `ErrChainKeyRequired` — 非回环暴露且需要哈希链但未配置 HMAC 密钥。
  * **验证流程**：`ValidateFailClosed(req)` 扫描所有 `Hosts`，检测是否存在非回环暴露，逐条校验上述条件，遇到首个违反条件即返回对应哨兵错误。

### 2.7 哈希链 HMAC-SM3 密钥化存证需求

* **FR-11：哈希链 HMAC-SM3 密钥化完整性**
  * **算法标签常量**：`AuditHashSM3HMAC = "SM3-HMAC:v1"` 为当前生产写入规范标签，`AuditHashSM3 = "SM3"` 为无密钥回退模式，`AuditHashSHA256 = "SHA256"` 为 SM3 迁移前遗留标签；
  * **链密钥注入**：`SetAuditChainKey(key string)` 在进程启动时通过 `atomic.Pointer[string]` 注入 HMAC 密钥，空字符串回退至无密钥 SM3 模式。`AuditChainKey()` 返回当前密钥；
  * **HMAC 计算规范**：当配置链密钥时，`ComputeAuditIntegrityHash` 计算 `HMAC-SM3(key, "SM3-HMAC:v1|" + preimage)`，其中 preimage 为 9 字段管道分隔标准前像（`prevHash|logID|timestamp(UTC)|algorithm|inputHash|outputHash|user|securityLevel|paramsJSON`），输出 64 字符十六进制摘要；
  * **遗留兼容验证**：`VerifyAuditIntegrityHash` 按优先级依次尝试 5 种候选算法：(1) 密钥化 HMAC-SM3-UTC、(2) 无密钥 SM3-UTC、(3) SHA-256-UTC 遗留、(4) SM3-LocalTZ 遗留、(5) SHA-256-LocalTZ 遗留，使用 `hmac.Equal` 常量时间比对防止侧信道攻击；
  * **快照完整性**：`ComputeSnapshotIntegrityHash` / `VerifySnapshotIntegrityHash` 采用相同模式，密钥前缀为 `"SM3-HMAC:v1-SNAPSHOT|"`，绑定快照自身的输入/输出样例字段。

### 2.8 分类分级标准体系加载需求

* **FR-12：分类分级标准体系动态加载**
  * **StandardDef 结构体**：定义单个分类分级标准体系，包含 `StandardID`（标准唯一标识）、`Description`（描述）、`Taxonomy`（分类法）、`Domains`（适用领域）、`GlobalParams.DefaultLevel`（全局默认等级）、`Levels`（字段级映射表 `map[string]StandardLevelMapping`）、`ExtraRules`（附加规则）与 `ExtraDowngradeRules`（降格规则）；
  * **目录批量加载**：`LoadStandardsFromDir(dir string) ([]StandardDef, []error)` 读取指定目录下所有 `.yaml` / `.yml` 文件，逐一解析为 `StandardDef`。单文件解析失败不阻断其他文件加载，错误收集返回。结果按 `StandardID` 排序；
  * **最高默认等级回退**：`highestStandardDefaultLevel()` 扫描所有已加载标准的 `global_params.default_level`，选取最高等级作为分类漏斗的全局回退等级，确保未显式覆盖的字段仍受控于保守安全策略。

---

## 三、非功能性需求 (Non-Functional Requirements)

### 3.1 性能与吞吐指标

| 场景 / 指标 | 目标要求 | 达成方案 |
|---|---|---|
| 纯内存操作延迟 | $\le 10\ \mu\text{s}$ | 零锁/轻量读写锁与局部内存缓存 |
| 单机 SQLite 写入吞吐 | $\ge 3,000\ \text{QPS}$ | `BufferedAuditStore` 200 条定量微批 + WAL 模式 |
| PostgreSQL 原子租约领取延迟 | $\le 5\ \text{ms}$ (P99) | `FOR UPDATE SKIP LOCKED` 行级短事务 |
| 存证哈希在线验真速度 | $\ge 10,000\ \text{条/秒}$ | 纯 Go 向量化 SM3 高速计算 |

### 3.2 高可用与数据一致性指标

1. **服务可用性**：$\ge 99.99\%$，支持 PostgreSQL 3 秒探针超时自动回退 SQLite WAL；
2. **存证数据一致性**：RPO = 0（优雅停机零丢数据），存证哈希链 100% 连续无单点断裂；
3. **任务调度一致性**：多副本并发调度 0 重复消费、0 脏写覆盖、0 脑裂。

### 3.3 密评三级合规与安全性指标

1. 符合 **GB/T 39786-2021《信息安全技术 信息系统密码应用基本要求》** 密评三级；
2. 存证与认证全面采用国密 **SM2、SM3、SM4-GCM**；
3. 存证完整性算法标签统一采用 **`SM3-HMAC:v1`**（密钥化 HMAC-SM3），信封加密采用 **`enc:v2:` HKDF-SM3** 密钥派生；
4. 防暴力破解、防重放攻击、常量时间比较防侧信道攻击；
5. 严格存储默认姿态 (`StrictStorage` default true)：非回环暴露时加密密钥与哈希链密钥为必填项，`ValidateFailClosed` 门控阻断不合规启动。

### 3.4 纯 Go 跨平台构建与可维护性指标

1. 全局配置 `CGO_ENABLED=0`，支持无缝交叉编译至 Linux AMD64 / ARM64 容器；
2. 代码遵循 Go 1.25+ Multi-module 规范，单元测试覆盖率 $\ge 90\%$。

---

## 四、验收标准与质量基线

1. **编译与依赖基线**：全仓库通过 `go build` 与 `make build`，零废弃包依赖；
2. **测试质量基线**：`make test`（含并发压测、哈希链连续性验证、优雅停机排空测试、归档保留验证、安全门控验证、逐端点熔断测试）100% 通过；
3. **静态检查基线**：通过 `golangci-lint` 与 `gitleaks` 零代码缺陷与密钥泄露风险。
