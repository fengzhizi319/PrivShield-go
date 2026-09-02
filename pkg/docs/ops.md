# PrivShield 共享基础包 (Shared PKG) — 生产运维与加固手册

> **文档定位**：`pkg` 基础包及底层存储引擎、密码信封、mTLS 证书体系、归档留存与中间件网关的生产部署、性能调优、监控告警与故障排查（Runbook）运维指南。

---

## 目录

- [一、完整环境变量配置表](#一完整环境变量配置表)
  - [1.1 共享环境变量助手函数](#11-共享环境变量助手函数)
  - [1.2 audit-log 服务环境变量](#12-audit-log-服务环境变量)
  - [1.3 service-hub 服务环境变量](#13-service-hub-服务环境变量)
  - [1.4 datasource-mgr 服务环境变量](#14-datasource-mgr-服务环境变量)
  - [1.5 跨服务共享变量](#15-跨服务共享变量)
  - [1.6 可观测性与迁移工具变量](#16-可观测性与迁移工具变量)
- [二、存储引擎运维](#二存储引擎运维)
  - [2.1 SQLite WAL 生产模式与完整性校验](#21-sqlite-wal-生产模式与完整性校验)
  - [2.2 PostgreSQL Phase B 分布式集群与连接池调优](#22-postgresql-phase-b-分布式集群与连接池调优)
  - [2.3 数据库自动化迁移工具](#23-数据库自动化迁移工具)
  - [2.4 归档运维](#24-归档运维)
- [三、密码运维](#三密码运维)
  - [3.1 信封加密体系](#31-信封加密体系)
  - [3.2 enc:v2: 密钥管理（HKDF-SM3 派生）](#32-encv2-密钥管理hkdf-sm3-派生)
  - [3.3 enc:v1: 遗留迁移](#33-encv1-遗留迁移)
  - [3.4 HMAC-SM3 链密钥注入](#34-hmac-sm3-链密钥注入)
  - [3.5 密钥轮转流程](#35-密钥轮转流程)
- [四、全栈监控指标](#四全栈监控指标)
  - [4.1 业务领域指标（pkg/metrics.Collector）](#41-业务领域指标pkgmetricscollector)
  - [4.2 传输层 RED 指标（pkg/observability.REDMetrics）](#42-传输层-red-指标pkgobservabilityredmetrics)
  - [4.3 推荐 Prometheus 告警规则](#43-推荐-prometheus-告警规则)
- [五、安全门禁运维](#五安全门禁运维)
  - [5.1 ValidateFailClosed 启动期安全不变式](#51-validatefailclosed-启动期安全不变式)
  - [5.2 五大哨兵错误](#52-五大哨兵错误)
  - [5.3 IsLoopbackHost 行为](#53-isloopbackhost-行为)
  - [5.4 零信任默认态](#54-零信任默认态)
  - [5.5 ValidateFailClosed 故障排查](#55-validatefailclosed-故障排查)
- [六、mTLS 证书与 CN 白名单运维](#六mtls-证书与-cn-白名单运维)
- [七、命名 SSOT 运维](#七命名-ssot-运维)
  - [7.1 注册表管理](#71-注册表管理)
  - [7.2 Observer 指标解读](#72-observer-指标解读)
  - [7.3 安全等级词表（L1-L5）](#73-安全等级词表l1-l5)
- [八、核心故障排查手册 (Runbook)](#八核心故障排查手册-runbook)

---

## 一、完整环境变量配置表

### 1.1 共享环境变量助手函数

所有环境变量统一通过 `pkg/config/env.go` 提供的助手函数读取，**严禁在各服务中直接调用 `os.Getenv`**。

| 函数签名 | 用途 | 异常处理 |
|---|---|---|
| `EnvString(name, def string) string` | 读取字符串环境变量，未设置或为空时返回默认值 | 空串视为未设置 |
| `EnvStringFirstSet(names ...string) string` | 依次读取多个环境变量，返回第一个非空值 | 全为空时返回空串 |
| `EnvStringOptional(name, def string) string` | 区分「未设置」与「显式设为空」，仅完全未设置时使用默认值 | 通过 `os.LookupEnv` 实现 |
| `EnvInt(name string, def int) int` | 以整数形式读取，缺失或无效时返回默认值 | `strconv.Atoi` 失败则兜底 |
| `EnvFloat(name string, def float64) float64` | 以浮点数形式读取，缺失或无效时返回默认值 | `strconv.ParseFloat` 失败则兜底 |
| `EnvBool(name string, def bool) bool` | 以布尔值读取；识别 `"true"`, `"1"`, `"yes"`, `"on"`（不区分大小写） | 其他值一律视为 `false` |
| `EnvStringSlice(name string) []string` | 以逗号分隔读取为字符串切片，自动去除空白 | 空串返回 `nil` |

### 1.2 audit-log 服务环境变量

配置源：`services/audit-log/internal/config/config.go`

#### 网络与监听

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `AUDIT_LOG_HOST` | string | `"127.0.0.1"` | HTTP 服务监听地址 |
| `AUDIT_LOG_PORT` | int | `8084` | HTTP 服务监听端口 |
| `AUDIT_LOG_GRPC_HOST` | string | `"127.0.0.1"` | gRPC 服务监听地址 |
| `AUDIT_LOG_GRPC_PORT` | int | `50054` | gRPC 服务监听端口 |
| `AUDIT_LOG_CORS_ORIGINS` | []string | `nil` | 逗号分隔的允许 CORS 来源列表 |
| `AUDIT_LOG_SHUTDOWN_TIMEOUT` | int | `5` | HTTP 优雅停机超时（秒） |

#### 日志

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `AUDIT_LOG_LOG_FORMAT` | string | `"json"` | 日志输出格式：`"json"`（对接 ELK/Loki）或 `"text"` |
| `AUDIT_LOG_LOG_LEVEL` | string | `"info"` | 日志级别：`"debug"` / `"info"` / `"warn"` / `"error"` |

#### 存储引擎

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `AUDIT_LOG_DB_PATH` | string | `""` | SQLite 数据库文件路径（空串使用内存模式） |
| `AUDIT_LOG_PG_DSN` | string | `""` | PostgreSQL 连接串（主优先）；空时回退至 `PG_DSN` |
| `PG_DSN` | string | `""` | PostgreSQL 连接串回退值（当 `AUDIT_LOG_PG_DSN` 为空时使用） |
| `AUDIT_LOG_PG_MAX_CONNS` | int | `0` | PostgreSQL 最大连接池大小（0 = 自适应计算） |
| `AUDIT_LOG_PG_MIN_CONNS` | int | `0` | PostgreSQL 最小常驻连接数（0 = 自适应计算） |
| `AUDIT_LOG_STRICT_STORAGE` | bool | 继承 `STRICT_STORAGE` | 严格存储模式：存储连接失败时启动失败（不静默回退） |
| `STRICT_STORAGE` | bool | `true` | 全局严格存储模式回退默认值 |

#### 微批刷盘器

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `AUDIT_LOG_FLUSH_BATCH_SIZE` | int | `0` | 微批刷盘单批最大写入条数（0 = 使用刷盘器默认值 200） |
| `AUDIT_LOG_FLUSH_INTERVAL_MS` | int | `0` | 刷盘时间窗口（毫秒，0 = 默认 20ms） |
| `AUDIT_LOG_FLUSH_QUEUE_SIZE` | int | `0` | 环形缓冲队列容量（0 = 默认 10000） |
| `AUDIT_LOG_FLUSH_ENQUEUE_TIMEOUT_MS` | int | `0` | 队列满时等待槽位的超时时间（毫秒，0 = 默认 500ms） |
| `AUDIT_LOG_FLUSH_MAX_STAGED` | int | `0` | 内存暂存/重投积压上限（0 = 默认 50000，防存储故障期 OOM） |
| `AUDIT_LOG_FLUSH_CLOSE_TIMEOUT_MS` | int | `0` | 优雅停机排空等待超时（毫秒，0 = 默认 10s） |

#### 留存与归档

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `AUDIT_LOG_RETENTION_DAYS` | int | `0` | 审计日志保留天数；0 = 永不删除；>0 时触发自动清理（**最小值 1095 / 3 年**，且须同时配置 `AUDIT_LOG_ARCHIVE_DIR` 和 `AUDIT_LOG_ENCRYPTION_KEY`） |
| `AUDIT_LOG_ARCHIVE_DIR` | string | `"data/archives"` | 归档段文件存放目录路径 |
| `AUDIT_LOG_ARCHIVE_PAGE_SIZE` | int | `0` | 每段归档读取条数（0 = 默认 500） |
| `AUDIT_LOG_ENCRYPTION_KEY` | string | `""` | 归档快照信封加密主密钥（主优先）；空时回退至 `PRIVACY_AUDIT_KEY` |
| `PRIVACY_AUDIT_KEY` | string | `""` | 加密密钥回退值 |

#### 密码与完整性

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `AUDIT_LOG_HASH_KEY` | string | `""` | HMAC-SM3 存证哈希链密钥（局方托管）；空串退回无密钥 SM3（仅用于本地开发兼容，**生产必须配置**） |
| `AUDIT_LOG_DB_WRITE_ONLY` | bool | `false` | 启用后自检数据库账户是否缺少 UPDATE/DELETE 权限（写入专用存证账户验证） |

#### 鉴权

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `AUDIT_LOG_API_KEY` | string | `""` | 入站 API Key 鉴权密钥（service-hub 外联时亦作为回退密钥） |
| `AUDIT_LOG_READER_API_KEY` | string | `""` | 只读验证密钥（P1-6 职责分离）；若同时设置，**必须与 `AUDIT_LOG_API_KEY` 不同** |
| `PRIVACY_AGENT_API_KEY` | string | `""` | 上游隐私计算 Agent REST API 鉴权密钥 |

#### TLS / mTLS

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `AUDIT_LOG_TLS_ENABLED` | bool | `false` | 启用 gRPC TLS/mTLS |
| `AUDIT_LOG_TLS_CERT_FILE` | string | `""` | 服务端 TLS 证书 PEM 文件路径 |
| `AUDIT_LOG_TLS_KEY_FILE` | string | `""` | 服务端 TLS 私钥 PEM 文件路径 |
| `AUDIT_LOG_TLS_CA_FILE` | string | `""` | 客户端认证 CA 证书路径 |
| `AUDIT_LOG_TLS_CLIENT_AUTH` | string | `""` | 客户端认证模式：`"require"` / `"verify"` / `""` |
| `AUDIT_LOG_TLS_PINNED_PUBKEY_FILE` | string | `""` | 客户端固定公钥 PEM 文件路径（SPKI 固定） |
| `AUDIT_LOG_REQUIRE_TLS` | bool | `false` | Fail-closed 生产门禁：为 `true` 时 TLS 未启用则启动失败 |
| `PRIVACY_AUTH_MTLS_WHITELIST_FILE` | string | `""` | mTLS CN 白名单 YAML 文件路径（三服务共享） |

#### 限流

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `AUDIT_LOG_RATE_LIMIT_RPS` | int | `100` | 每客户端 IP 令牌桶每秒补充速率（0 = 不限流） |
| `AUDIT_LOG_RATE_LIMIT_BURST` | int | `200` | 每客户端 IP 令牌桶突发最大容量 |

#### 上游 Agent 连接

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `PRIVACY_AGENT_REST_HOST` | string | `"127.0.0.1"` | 上游 Agent REST 主机地址 |
| `PRIVACY_REST_PORT` | int | `8079` | 上游 Agent REST 端口 |
| `PRIVACY_AGENT_URLS` | []string | `nil` | 逗号分隔的上游 Agent REST URL 列表（多节点负载均衡/故障转移）；空时回退为单节点 `AgentBaseURL()` |

### 1.3 service-hub 服务环境变量

配置源：`services/service-hub/internal/config/config.go`

#### 网络与监听

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_HOST` | string | `"127.0.0.1"` | HTTP 监听地址 |
| `SERVICE_HUB_PORT` | int | `8082` | HTTP 监听端口 |
| `SERVICE_HUB_GRPC_HOST` | string | `"127.0.0.1"` | gRPC 监听地址 |
| `SERVICE_HUB_GRPC_PORT` | int | `50052` | gRPC 监听端口 |
| `SERVICE_HUB_CORS_ORIGINS` | []string | `nil` | 逗号分隔的允许 CORS 来源列表 |
| `SERVICE_HUB_SHUTDOWN_TIMEOUT` | int | `5` | HTTP 优雅停机超时（秒） |

#### 日志

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_LOG_FORMAT` | string | `"json"` | 日志输出格式 |
| `SERVICE_HUB_LOG_LEVEL` | string | `"info"` | 日志级别 |

#### 调度引擎

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_MAX_QUEUE` | int | `1000` | 调度引擎最大任务等待队列深度 |
| `SERVICE_HUB_SCHEDULE_TIMEOUT` | int | `30` | 单步任务调度/执行超时（秒） |
| `SERVICE_HUB_RETENTION_DAYS` | int | `30` | 终态任务保留天数（0 = 永不清理） |
| `SERVICE_HUB_LEASE_TTL` | int | `60` | 任务租约 TTL（秒） |

#### 存储

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_DB_PATH` | string | `""` | SQLite 任务数据库路径（空 = 内存模式） |
| `SERVICE_HUB_PG_DSN` | string | `""` | PostgreSQL 连接串（多副本 Hub 部署）；空 = 回退 SQLite |
| `SERVICE_HUB_PG_MAX_CONNS` | int | `10` | PostgreSQL 最大连接池大小 |
| `SERVICE_HUB_PG_MIN_CONNS` | int | `2` | PostgreSQL 最小常驻连接数 |
| `SERVICE_HUB_STRICT_STORAGE` | bool | 继承 `STRICT_STORAGE` | 严格存储模式 |

#### 上游数据源管理

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `DATASOURCE_MGR_HOST` | string | `"127.0.0.1"` | 数据源管理器 REST 主机 |
| `DATASOURCE_MGR_PORT` | int | `8083` | 数据源管理器 REST 端口 |
| `DATASOURCE_MGR_GRPC_HOST` | string | `"127.0.0.1"` | 数据源管理器 gRPC 主机 |
| `DATASOURCE_MGR_GRPC_PORT` | int | `50053` | 数据源管理器 gRPC 端口 |
| `SERVICE_HUB_DATASOURCE_API_KEY` | string | `""` | 外联 datasource-mgr 的 API Key |

#### 审计日志外联

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_AUDIT_LOG_URLS` | []string | `nil` | 逗号分隔的 audit-log REST URL 列表（多副本轮询） |
| `SERVICE_HUB_AUDIT_HTTP` | string | `""` | 回退单 audit-log URL（`SERVICE_HUB_AUDIT_LOG_URLS` 为空时使用）；均空则硬编码 `"http://audit-log:8084"` |
| `SERVICE_HUB_AUDIT_LOG_API_KEY` | string | `""` | 外联 audit-log 的 API Key（主优先）；空时回退至 `AUDIT_LOG_API_KEY` |
| `SERVICE_HUB_AUDIT_LOG_TIMEOUT` | int | `10` | 单次存证提交超时（秒） |
| `SERVICE_HUB_AUDIT_LOG_MAX_RETRIES` | int | `3` | 存证提交网络错误/5xx 重试次数（0 = 不重试） |
| `SERVICE_HUB_AUDIT_LOG_TLS_ENABLED` | bool | `false` | 外联 audit-log 是否使用 TLS 1.3 |
| `SERVICE_HUB_AUDIT_LOG_TLS_CERT_FILE` | string | `""` | 客户端证书 PEM（回退至 `SERVICE_HUB_TLS_CERT_FILE`） |
| `SERVICE_HUB_AUDIT_LOG_TLS_KEY_FILE` | string | `""` | 客户端私钥 PEM（回退至 `SERVICE_HUB_TLS_KEY_FILE`） |
| `SERVICE_HUB_AUDIT_LOG_TLS_CA_FILE` | string | `""` | 根 CA 证书（回退至 `SERVICE_HUB_TLS_CA_FILE`） |

#### 鉴权

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_API_KEY` | string | `""` | 入站 API Key |

#### TLS / mTLS

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_TLS_ENABLED` | bool | `false` | 启用 TLS/mTLS |
| `SERVICE_HUB_TLS_CERT_FILE` | string | `""` | 服务端 TLS 证书 PEM 路径 |
| `SERVICE_HUB_TLS_KEY_FILE` | string | `""` | 服务端 TLS 私钥 PEM 路径 |
| `SERVICE_HUB_TLS_CA_FILE` | string | `""` | CA 证书路径 |
| `SERVICE_HUB_TLS_CLIENT_AUTH` | string | `""` | 客户端认证模式 |
| `SERVICE_HUB_TLS_PINNED_PUBKEY_FILE` | string | `""` | 客户端固定公钥 PEM（SPKI） |
| `SERVICE_HUB_REQUIRE_TLS` | bool | `false` | Fail-closed：为 `true` 时 TLS 未启用则启动失败 |
| `PRIVACY_AUTH_MTLS_WHITELIST_FILE` | string | `""` | CN 白名单 YAML 文件路径 |

#### 限流

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_RATE_LIMIT_RPS` | int | `100` | 令牌桶速率（0 = 不限流） |
| `SERVICE_HUB_RATE_LIMIT_BURST` | int | `200` | 令牌桶突发容量 |

#### 上游 Agent 连接

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `PRIVACY_AGENT_REST_HOST` | string | `"127.0.0.1"` | 上游 Agent REST 主机 |
| `PRIVACY_REST_PORT` | int | `8079` | 上游 Agent REST 端口 |
| `PRIVACY_AGENT_API_KEY` | string | `""` | 上游 Agent API Key |
| `PRIVACY_AGENT_URLS` | []string | `nil` | 多 Agent URL 列表 |

### 1.4 datasource-mgr 服务环境变量

配置源：`services/datasource-mgr/internal/config/config.go`

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `DATASOURCE_MGR_HOST` | string | `"127.0.0.1"` | HTTP 监听地址 |
| `DATASOURCE_MGR_PORT` | int | `8083` | HTTP 监听端口 |
| `DATASOURCE_MGR_GRPC_HOST` | string | `"127.0.0.1"` | gRPC 监听地址 |
| `DATASOURCE_MGR_GRPC_PORT` | int | `50053` | gRPC 监听端口 |
| `DATASOURCE_MGR_LOG_FORMAT` | string | `"json"` | 日志格式 |
| `DATASOURCE_MGR_LOG_LEVEL` | string | `"info"` | 日志级别 |
| `DATASOURCE_MGR_SHUTDOWN_TIMEOUT` | int | `5` | 优雅停机超时（秒） |
| `DATASOURCE_MGR_API_KEY` | string | `""` | 入站 API Key |
| `DATASOURCE_MGR_CORS_ORIGINS` | []string | `nil` | CORS 允许来源 |
| `DATASOURCE_MGR_REQUIRE_TLS` | bool | `false` | Fail-closed TLS 门禁 |
| `DATASOURCE_MGR_STRICT_STORAGE` | bool | 继承 `STRICT_STORAGE` | 严格存储模式 |
| `DATASOURCE_MGR_TLS_ENABLED` | bool | `false` | 启用 TLS |
| `DATASOURCE_MGR_TLS_CERT_FILE` | string | `""` | 服务端证书路径 |
| `DATASOURCE_MGR_TLS_KEY_FILE` | string | `""` | 服务端私钥路径 |
| `DATASOURCE_MGR_TLS_CA_FILE` | string | `""` | CA 证书路径 |
| `DATASOURCE_MGR_TLS_CLIENT_AUTH` | string | `""` | 客户端认证模式 |
| `DATASOURCE_MGR_TLS_PINNED_PUBKEY_FILE` | string | `""` | 客户端固定公钥 PEM |
| `PRIVACY_AUTH_MTLS_WHITELIST_FILE` | string | `""` | CN 白名单文件 |
| `DATASOURCE_MGR_RATE_LIMIT_RPS` | int | `100` | 令牌桶速率 |
| `DATASOURCE_MGR_RATE_LIMIT_BURST` | int | `200` | 令牌桶突发容量 |

### 1.5 跨服务共享变量

以下变量被多个服务读取，需确保全局一致配置：

| 变量 | 使用方 | 用途 |
|---|---|---|
| `PRIVACY_AGENT_REST_HOST` | audit-log, service-hub | 上游 Agent REST 主机 |
| `PRIVACY_REST_PORT` | audit-log, service-hub | 上游 Agent REST 端口 |
| `PRIVACY_AGENT_API_KEY` | audit-log, service-hub | 上游 Agent 鉴权密钥 |
| `PRIVACY_AGENT_URLS` | audit-log, service-hub | 多 Agent 负载均衡 URL 列表 |
| `PRIVACY_AUTH_MTLS_WHITELIST_FILE` | audit-log, service-hub, datasource-mgr | mTLS CN 白名单 YAML 路径 |
| `STRICT_STORAGE` | audit-log, service-hub, datasource-mgr | 全局严格存储模式回退默认值 |
| `AUDIT_LOG_API_KEY` | audit-log (入站), service-hub (出站回退) | 审计日志 API 密钥 |
| `PG_DSN` | audit-log (回退) | PostgreSQL DSN 回退 |
| `PRIVACY_AUDIT_KEY` | audit-log (回退) | 加密密钥回退 |
| `DATASOURCE_MGR_HOST/PORT/GRPC_HOST/GRPC_PORT` | datasource-mgr (自身), service-hub (出站) | 数据源管理器连接参数 |

### 1.6 可观测性与迁移工具变量

| 环境变量 | 使用场景 | 说明 |
|---|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `pkg/observability/tracing.go` | 分布式追踪 OTLP 端点（设置后启用追踪） |
| `PRIVACY_SERVICE_NAME` | `pkg/observability/tracing.go` | 追踪服务名称（默认 `"PrivShield"`） |
| `PRIVSHIELD_PG_TEST_DSN` | 集成测试 | PostgreSQL 集成测试 DSN |
| `PRIVSHIELD_MIGRATE_PG_DSN` | `pkg/store/cmd/migrate` | 迁移目标 PostgreSQL DSN |
| `PRIVSHIELD_MIGRATE_SNAPSHOT_VERIFY` | `pkg/store/cmd/migrate` | 快照验证模式 |
| `AUDIT_LOG_DB_PATH` | `pkg/store/cmd/repairchain` | 修复链工具的 SQLite 路径 |

---

## 二、存储引擎运维

### 2.1 SQLite WAL 生产模式与完整性校验

当部署为单节点轻量网关时，系统采用嵌入式 SQLite 作为存储引擎。

#### 关键 PRAGMA 参数

1. **WAL 模式 (`PRAGMA journal_mode=WAL`)**：写入追加到 `-wal` 文件，读操作直接读取主库与 WAL 快照，实现读写互不阻塞。
2. **锁重试超时 (`PRAGMA busy_timeout=5000`)**：遇到瞬时写锁冲突时自动在 5,000ms 内指数重试，消除 `database is locked` 偶发异常。实际 DSN 中配置为 `busy_timeout(10000)`。
3. **安全同步级别 (`PRAGMA synchronous=NORMAL`)**：相比 `FULL` 提升 10 倍以上写入性能，在 WAL 模式下保证掉电不损坏。
4. **外键约束 (`PRAGMA foreign_keys=ON`)**：级联保障快照表与审计日志表的数据完整性。

#### 连接池约束（P24 fix）

SQLite 同一时间仅支持一个写入者，连接池参数严格限制：

| 参数 | 值 | 说明 |
|---|---|---|
| `MaxOpenConns` | 4 | 最大打开连接数 |
| `MaxIdleConns` | 2 | 最大空闲连接数 |
| `MaxConnLifetime` | 5m | 连接最大生命周期 |

#### 启动完整性校验 — `sqlite.ValidateIntegrity`

```go
// pkg/store/sqlite/init.go
func ValidateIntegrity(dbPath string) error
```

通过执行 `PRAGMA integrity_check` 在启动早期探测突发断电导致的数据库文件损坏：

- `dbPath` 为空 → 返回 `nil`（内存模式无需校验）
- 结果为 `"ok"` → 通过
- 其他 → 返回 `error`，服务应拒绝启动

**运维操作**：

```bash
# 手动检查 SQLite 完整性
sqlite3 /var/lib/privshield/data.db "PRAGMA integrity_check;"

# 检查 WAL 文件状态
ls -la /var/lib/privshield/data.db*
```

> **目录与权限建议**：确保 `DB_PATH` 所在目录具有可写权限，且同目录下有足够磁盘空间用于生成 `-wal` 和 `-shm` 共享内存文件。

### 2.2 PostgreSQL Phase B 分布式集群与连接池调优

面向政务云多副本高并发部署场景，配置 DSN 即可无缝切换至 PostgreSQL 存储集群。

#### 容器感知的自适应连接池

`pkg/store/postgres/postgres.go` 中的 `effectiveNumCPU()` 函数自动探测容器真实 CPU 配额（支持 cgroup v1 和 v2），计算公式：

```
MaxConns = clamp(effectiveNumCPU * 4, 10, 100)
MinConns = clamp(effectiveNumCPU, 2, 20)
```

其中 `clamp(value, min, max)` 将值限制在 `[min, max]` 范围内。不变式约束：`MinConns <= MaxConns`。

手动覆盖时通过 `*_PG_MAX_CONNS` / `*_PG_MIN_CONNS` 环境变量指定。

#### 连接生命周期管理

| 参数 | 值 | 说明 |
|---|---|---|
| `HealthCheckPeriod` | 30s | 连接健康检查探测周期 |
| `MaxConnLifetime` | 30m | 连接最长生命周期 |
| `MaxConnIdleTime` | 5m | 空闲连接最大存活时间 |

#### 初始化探测

连接池创建后执行 3 秒超时 Ping 探活，失败则立即返回错误；随后在 30 秒超时内执行 Schema 初始化。

#### 无锁原子租约抢占

PostgreSQL 后端通过 `FOR UPDATE SKIP LOCKED` 实现多 Hub 节点并发竞争式任务领取（`ClaimNext`），无需外部分布式锁（如 Redis/ZooKeeper）。

### 2.3 数据库自动化迁移工具

#### SQLite → PostgreSQL 迁移 (`pkg/store/cmd/migrate`)

```bash
# 查看帮助
go run pkg/store/cmd/migrate/main.go -help

# 对 SQLite 数据库执行迁移
go run pkg/store/cmd/migrate/main.go -driver sqlite -dsn /var/lib/privshield/data.db

# 对 PostgreSQL 数据库执行迁移
go run pkg/store/cmd/migrate/main.go -driver postgres -dsn "postgres://user:pwd@127.0.0.1:5432/privshield?sslmode=disable"
```

特性：幂等插入、哈希链序保持、快照 SM4-GCM 解密验证、迁移后链式验真。

#### 哈希链修复工具 (`pkg/store/cmd/repairchain`)

```bash
# 只读验链
go run pkg/store/cmd/repairchain/main.go -mode verify -db /var/lib/privshield/audit.db

# 修复断裂链接
go run pkg/store/cmd/repairchain/main.go -mode repair -db /var/lib/privshield/audit.db

# 重签为 HMAC-SM3:v1（注入 AUDIT_LOG_HASH_KEY 后使用）
go run pkg/store/cmd/repairchain/main.go -mode resign -db /var/lib/privshield/audit.db
```

三种模式：
- `verify`：扫描链，将记录分类为 canonical / legacy / re-anchor / tampered
- `repair`：重新锚定断裂的 `prev_hash` 链接并级联重算 `integrity_hash`
- `resign`：将存量记录升级为 HMAC-SM3 密钥化口径

### 2.4 归档运维

#### 归档段文件格式

归档系统在 `AUDIT_LOG_ARCHIVE_DIR` 目录下产出两种文件：

| 文件名模式 | 内容 |
|---|---|
| `audit-archive-<cutoff>-<seq>.ndjson.gz.enc` | SM4-GCM(gzip(NDJSON 记录行)) |
| `audit-archive-<cutoff>-<seq>.manifest.json` | 段元数据清单 |

**文件名规则**：
- `<cutoff>`：截止时间 UTC 格式 `20060102T150405Z`
- `<seq>`：6 位序号 `%06d`（从 000000 起递增，绝不覆盖既有归档）

**段文件内部结构**：
1. 每行一条 NDJSON 记录（`{"kind":"log",...}` 或 `{"kind":"snapshot",...}`）
2. 全部行经 gzip 压缩
3. 压缩结果经 SM4-GCM 信封加密（`enc:v2:` 格式），密钥来自 `AUDIT_LOG_ENCRYPTION_KEY`

**行哈希链**：`chain[i] = SM3(chain[i-1] || line[i])`，链尾值写入清单。

#### 清单（Manifest）结构

```json
{
  "version": "privshield-audit-archive/v1",
  "chain_algo": "SM3-LINE-CHAIN:v1",
  "encryption": "SM4-GCM/HKDF-SM3(enc:v2)",
  "segment_file": "audit-archive-20260801T000000Z-000000.ndjson.gz.enc",
  "created_at": "2026-08-01T00:00:00Z",
  "cutoff": "2026-08-01T00:00:00Z",
  "log_count": 500,
  "snapshot_count": 500,
  "first_log_id": "log-xxx",
  "last_log_id": "log-yyy",
  "first_timestamp": "2025-01-01T00:00:00Z",
  "last_timestamp": "2026-07-31T23:59:59Z",
  "chain_tail": "a1b2c3..."
}
```

#### 归档验证流程

`VerifySegment` 函数可独立验真任何归档段（无需访问数据库）：

1. 读取并解析清单文件
2. 使用密钥解密段文件（SM4-GCM）
3. gzip 解压得到 NDJSON 原始行
4. 逐行重算行哈希链 `SM3(chain[i-1] || line[i])`，与清单 `chain_tail` 比对
5. 校验日志条数、快照条数、边界 ID 与边界时间戳
6. 对每条日志重算 9 要素 `integrity_hash`，与记录自身存储值比对

#### 归档-before-删除工作流

`Archiver.ArchiveAndCleanup` 执行严格的先归档后删除红线：

1. 从底层存储按链序（旧→新）取出一页到期记录（默认 500 条）
2. 写入归档段文件 + 清单文件（fsync 确保持久化）
3. **立即回读验真**（`VerifySegment`），验真失败则拒绝删除
4. 按该页 ID 精确删除日志及级联快照
5. 删除失败则中止（不会继续归档下一页，防止「档而未删」或「删而未档」）
6. 循环直到无更多到期记录

#### 归档故障恢复

| 故障场景 | 处理方式 |
|---|---|
| 段文件解密失败 | 检查 `AUDIT_LOG_ENCRYPTION_KEY` 是否与写入时一致 |
| 行哈希链不匹配 | 段文件被篡改、截断或行序被打乱；需从备份恢复 |
| 归档成功但删除失败 | 安全中止，不重复归档；手动排查存储连接后重试 |
| 目录权限不足 | 确保归档目录权限 `0o700`，进程用户可写 |
| 磁盘空间不足 | `writeFsync` 会在写入后执行 `fsync`，空间不足时立即报错 |

---

## 三、密码运维

### 3.1 信封加密体系

`pkg/crypto/envelope.go` 实现基于国密 SM4-GCM (GB/T 32907-2016) 的信封加密。

**核心参数**：

| 参数 | 值 | 说明 |
|---|---|---|
| SM4 密钥长度 | 128 位 (16 字节) | GB/T 32907-2016 标准 |
| GCM Nonce | 12 字节 | 标准 AEAD 随机数 |
| GCM Auth Tag | 16 字节 | 认证标签，防篡改 |
| AAD | 版本前缀 | `"enc:v2:"` 参与认证，防前缀剥离降级 |

**落盘格式规范**：

| 版本 | 格式 | 密钥派生 | 状态 |
|---|---|---|---|
| v2 (当前) | `enc:v2:<Base64(16B salt + 12B nonce + SM4密文 + 16B tag)>` | HKDF-SM3(salt, secret) | **当前写入格式** |
| v1 (遗留) | `enc:v1:<Base64(12B nonce + SM4密文 + 16B tag)>` | SHA-256(secret)[:16] | **仅可读，不再写入** |

### 3.2 enc:v2: 密钥管理（HKDF-SM3 派生）

`DeriveKeyHKDF` 使用 RFC 5869 HKDF（Extract-then-Expand），以 SM3 为底层杂凑函数：

1. **Extract 阶段**：`PRK = HMAC-SM3(salt, secret)`
   - 每条记录独立生成 16 字节密码学安全随机 salt（`crypto/rand.Reader`）
   - 同一口令在不同记录上产出完全不同的派生密钥
2. **Expand 阶段**：`Key = HKDF-Expand(PRK, info, 16)`
   - `info = "PrivShield audit snapshot SM4-GCM v2"` 将派生密钥绑定到「审计快照加密」用途
3. **加密阶段**：`SM4-GCM-Seal(key, nonce, plaintext, aad="enc:v2:")`
   - 版本前缀作为 AAD 参与认证，剥离/改写前缀会导致认证失败

**密钥生成**：

```bash
# 生成 256 位（32 字符）高强度随机主密钥
openssl rand -hex 16

# 或通过 K8s Secret 注入
kubectl create secret generic privshield-audit \
  --from-literal=AUDIT_LOG_ENCRYPTION_KEY=$(openssl rand -hex 16)
```

**安全保证**：
- 密钥为空时写入路径直接返回 `ErrEmptyKey`，**绝不静默降级为明文**
- 无前缀密文返回 `ErrUnencryptedValue`，防止剥离前缀降级攻击

### 3.3 enc:v1: 遗留迁移

v1 格式使用 `SHA-256(secret)[:16]` 弱派生，无逐记录 salt，存在离线暴破风险。

**迁移流程**：

1. 确认 `AUDIT_LOG_ENCRYPTION_KEY` 已设置为新密钥
2. 运行迁移工具解密旧数据并以新格式重新加密：

```bash
# 快照验证模式迁移
go run pkg/store/cmd/migrate/main.go \
  -driver sqlite \
  -dsn /var/lib/privshield/data.db \
  -snapshot-verify full
```

3. 迁移工具自动检测 `enc:v1:` 前缀并使用旧派生路径解密，再以 v2 格式重新加密

### 3.4 HMAC-SM3 链密钥注入

`pkg/store/audit_hash.go` 实现存证哈希链的密钥化管理。

**9 要素前映像结构**：
```
prev_hash|log_id|timestamp_utc|algorithm|input_hash|output_hash|user|security_level|params_json
```

**写入路径**：
- 配置 `AUDIT_LOG_HASH_KEY` → `integrity_hash = HMAC-SM3(key, "SM3-HMAC:v1|" + payload)`
- 未配置 → `integrity_hash = SM3(payload)`（仅用于本地开发）

**注入方法**：
```bash
# 服务启动前设置环境变量
export AUDIT_LOG_HASH_KEY="<局方托管的32字节以上随机密钥>"
```

代码中通过 `store.SetAuditChainKey(key)` 在进程启动时注入一次，运行期改钥会导致既有记录核验失败。

**核验兼容**：`VerifyAuditIntegrityHash` 依次尝试 5 种候选算法：
1. HMAC-SM3 (密钥化，当前规范)
2. SM3-UTC (无密钥)
3. SHA-256-UTC (SHA-256 遗留)
4. SM3-LocalTZ (本地时区遗留)
5. SHA-256-LocalTZ (本地时区 SHA-256 遗留)

**存量升级**：注入密钥后，使用 `repairchain` 工具的 `resign` 模式将存量无密钥记录升级为 HMAC-SM3 口径。

### 3.5 密钥轮转流程

```
步骤 1: 生成新密钥
        openssl rand -hex 16 > new_key.txt

步骤 2: 编写离线迁移脚本
        读取旧数据 → DecryptString(ciphertext, old_key) → EncryptString(plaintext, new_key) → 写回

步骤 3: 更新环境变量
        AUDIT_LOG_ENCRYPTION_KEY=<new_key>

步骤 4: 重启服务（新写入使用新密钥）

步骤 5: 验证
        调用快照查询接口确认旧数据可正常解密
```

**注意**：轮转加密密钥不影响存证哈希链。哈希链密钥（`AUDIT_LOG_HASH_KEY`）一旦设定不可更改（运行期改钥导致既有记录核验失败），如需更换必须配合 `repairchain -mode resign` 全量重签。

---

## 四、全栈监控指标

### 4.1 业务领域指标（pkg/metrics.Collector）

每个微服务模块在启动时调用 `metrics.NewCollector(module)` 创建带独立 `prometheus.Registry` 的指标收集器。所有指标均携带 `module` 常量标签。

#### HTTP 吞吐指标

| 指标名称 | 类型 | 标签 | 含义与阈值建议 |
|---|---|---|---|
| `http_requests_total` | Counter | `method`, `path`, `status` | HTTP 请求总量（关注 5xx 占比 > 1%） |
| `http_request_duration_seconds` | Histogram | `method`, `path` | 接口响应延迟（P99 应 < 100ms） |

#### 上游 Agent 调用指标

| 指标名称 | 类型 | 标签 | 含义与阈值建议 |
|---|---|---|---|
| `agent_requests_total` | Counter | `endpoint`, `status` | 上游隐私计算 Agent 请求总数 |
| `agent_request_duration_seconds` | Histogram | `endpoint` | Agent 调用延迟直方图 |

#### 可靠性与故障自愈指标

| 指标名称 | 类型 | 标签 | 含义与阈值建议 |
|---|---|---|---|
| `orphaned_tasks_recovered_total` | Counter | `type` (`"running"` / `"pending"`) | 崩溃重启后自动回收的孤儿任务数 |
| `tasks_retried_total` | Counter | `result` (`"queued"` / `"exhausted"`) | 进入自动重试队列的任务数 |
| `circuit_breaker_state` | Gauge | `node` | 上游节点熔断器状态（0=Closed 正常, 1=Open 熔断, 2=HalfOpen 半开探测） |

#### 租约与并发指标

| 指标名称 | 类型 | 标签 | 含义与阈值建议 |
|---|---|---|---|
| `task_lease_conflicts_total` | Counter | `module` | 租约所有权抢占冲突次数（正常应为 0，持续增长需排查多 Hub 时钟偏差） |
| `task_lease_expired_total` | Counter | `module` | 租约超期回收事件总数 |
| `task_claim_latency_seconds` | Histogram | `module` | PostgreSQL `ClaimNext` 抢占延迟（P99 应 < 50ms） |
| `task_transitions_total` | Counter | `from`, `to`, `result` | 任务状态机流转次数 |

#### 就绪探针指标

| 指标名称 | 类型 | 标签 | 含义 |
|---|---|---|---|
| `service_hub_ready` | Gauge | `module` | 调度中枢就绪状态（1=可接收流量, 0=未就绪） |

#### 命名路由指标

| 指标名称 | 类型 | 标签 | 含义与阈值建议 |
|---|---|---|---|
| `privshield_api_alias_requests_total` | Counter | `alias`, `canonical`, `target` | 使用非 Canonical 别名的请求数（`target`: `datasource_id` / `api_code` / `path`） |
| `privshield_datasource_normalize_errors_total` | Counter | `reason` | 标识归一化失败次数（`reason`: `unknown` / `empty` / `reserved` / `format_invalid`） |
| `privshield_datasource_requests_total` | Counter | `datasource_id`, `api_code`, `status` | 按规范数据源实体处理的请求总数（`status`: `success` / `error` / `fallback`） |

### 4.2 传输层 RED 指标（pkg/observability.REDMetrics）

`pkg/observability` 提供传输层通用 RED（Rate / Errors / Duration）指标，由 HTTP/gRPC 中间件自动埋点。使用独立的 `prometheus.Registry`，指标名带 `privshield_` 前缀。

| 指标名称 | 类型 | 标签 | 含义 |
|---|---|---|---|
| `privshield_requests_total` | Counter | `protocol`, `endpoint`, `status` | 总请求数（`protocol`: `http` / `grpc`） |
| `privshield_request_duration_seconds` | Histogram | `protocol`, `endpoint` | 请求延迟直方图 |

**与 Collector 的边界**：REDMetrics 度量「请求经过了多少」（传输层自动埋点），Collector 度量「业务做了什么」（Handler 显式上报）。二者互补而非替代。

### 4.3 推荐 Prometheus 告警规则

```yaml
groups:
  - name: privshield_pkg_alerts
    rules:
      - alert: PrivShieldHigh5xxErrorRate
        expr: sum(rate(http_requests_total{status=~"5.."}[2m])) / sum(rate(http_requests_total[2m])) * 100 > 2
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "PrivShield 接口 5xx 错误率超过 2%"
          description: "模块 {{ $labels.module }} 在过去 2 分钟内 5xx 错误率达到 {{ $value }}%"

      - alert: PrivShieldAgentCircuitBreakerOpen
        expr: circuit_breaker_state == 1
        for: 30s
        labels:
          severity: critical
        annotations:
          summary: "上游隐私计算 Agent 熔断器已触发"
          description: "目标 {{ $labels.target }} 连续请求失败超过阈值，已进入熔断阻断状态。"

      - alert: PrivShieldTaskLeaseConflicts
        expr: increase(task_lease_conflicts_total[5m]) > 5
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "任务租约冲突频繁"
          description: "模块 {{ $labels.module }} 在过去 5 分钟内发生 {{ $value }} 次租约冲突，请排查多 Hub 时钟同步。"

      - alert: PrivShieldNormalizeErrorsSpike
        expr: increase(privshield_datasource_normalize_errors_total{reason="unknown"}[5m]) > 10
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "数据源标识归一化失败激增"
          description: "模块 {{ $labels.module }} 出现大量未知数据源标识请求，可能存在非法调用或客户端配置错误。"

      - alert: PrivShieldServiceHubNotReady
        expr: service_hub_ready == 0
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "调度中枢未就绪"
          description: "模块 {{ $labels.module }} 持续 2 分钟未就绪，请检查存储连接与依赖服务状态。"
```

---

## 五、安全门禁运维

### 5.1 ValidateFailClosed 启动期安全不变式

`pkg/config/security.go` 中的 `ValidateFailClosed` 函数在每个服务启动时强制执行安全不变式校验，取代原先「配置缺失 → 静默降级为无鉴权/明文传输」的危险行为。

**校验规则**：

1. **API Key 强制**：任一监听地址非环回 → 必须配置入站 API Key
2. **TLS 强制**：`RequireTLS=true` 而 `TLSEnabled=false` → 拒绝启动
3. **mTLS 白名单强制**：启用 gRPC TLS 但未提供 CN 白名单文件 → 拒绝启动
4. **加密密钥强制**：`RequireEncryptionKey=true` 且密钥为空、存在非环回监听 → 拒绝启动
5. **哈希链密钥强制**：`RequireHashKey=true` 且链密钥为空、存在非环回监听 → 拒绝启动

### 5.2 五大哨兵错误

| 哨兵错误 | 触发条件 | 修复方法 |
|---|---|---|
| `ErrAPIKeyRequired` | 非环回监听但未配置 API Key | 设置对应的 `*_API_KEY` 环境变量，或绑定到 `127.0.0.1` |
| `ErrTLSRequired` | 部署方声明 `RequireTLS=true` 但 TLS 未启用 | 设置 `*_TLS_ENABLED=true` 并配置证书 |
| `ErrMTLSWhitelistRequired` | gRPC TLS 启用但未提供 CN 白名单文件 | 设置 `PRIVACY_AUTH_MTLS_WHITELIST_FILE` 指向白名单 YAML |
| `ErrEncryptionKeyRequired` | 非环回监听且存证加密密钥为空 | 设置 `AUDIT_LOG_ENCRYPTION_KEY` |
| `ErrChainKeyRequired` | 非环回监听且存证哈希链密钥为空 | 设置 `AUDIT_LOG_HASH_KEY`（局方托管密钥） |

### 5.3 IsLoopbackHost 行为

`IsLoopbackHost(host string) bool` 判断监听地址是否仅接受本机连接：

| 输入 | 结果 | 说明 |
|---|---|---|
| `""` (空串) | `true` | 空串视为本地 |
| `"localhost"` | `true` | 本地回环 |
| `"127.0.0.1"`, `"127.x.x.x"` | `true` | 127.0.0.0/8 段全部视为本地 |
| `"::1"` | `true` | IPv6 环回 |
| `"host:port"` 形式 | 取主机部分判断 | 通过 `net.SplitHostPort` 解析 |
| `"0.0.0.0"` | `false` | 全接口监听，对外暴露 |
| `"::"` | `false` | IPv6 全接口，对外暴露 |
| 具体网卡 IP | `false` | 视为对外暴露 |
| 无法解析的主机名 | `false` | **Fail-closed**：不可解析的按对外暴露处理 |

### 5.4 零信任默认态

系统默认采用零信任安全姿态：

- **本地开发**：绑定 `127.0.0.1` 时允许无密钥/无鉴权启动（仅限开发便利）
- **生产部署**：绑定 `0.0.0.0` 或具体网卡 IP 时，API Key、TLS、加密密钥、哈希链密钥缺一不可
- **Fail-closed 原则**：所有安全开关缺失即启动失败，绝不静默降级

### 5.5 ValidateFailClosed 故障排查

**症状**：服务启动立即退出，日志中出现哨兵错误。

**排查步骤**：

1. **识别错误类型**：查看启动日志中的具体错误名
2. **检查监听地址**：确认 `*_HOST` 变量是否意外设为 `0.0.0.0`
3. **检查密钥配置**：确认所有 `*_API_KEY`、`*_ENCRYPTION_KEY`、`*_HASH_KEY` 已通过 K8s Secret 或环境变量正确注入
4. **检查 TLS 配置**：确认 `*_TLS_ENABLED` 与 `*_REQUIRE_TLS` 状态一致
5. **本地开发快速绕过**：将 `*_HOST` 设为 `127.0.0.1` 即可跳过所有安全门禁

---

## 六、mTLS 证书与 CN 白名单运维

### 白名单配置文件格式

```yaml
# config/mtls-whitelist.yaml
version: "1.0"
clients:
  - cn: "service-hub.privshield.internal"
    allowed_scopes:
      - "/PrivacyService/Process"
      - "/AuditLog/*"
    role: "orchestrator"
    description: "数据服务调度中枢核心客户端"
    enabled: true
  - cn: "bff-go.privshield.internal"
    allowed_scopes: ["*"]
    role: "gateway"
    enabled: true
```

支持双格式：
- **标准格式**：`clients` 键 + `allowed_scopes` 字段
- **遗留格式**：`entries` 键 + `scopes` 字段（向下兼容）

### Scope 匹配规则

1. **全局通配符** `"*"`：允许访问所有方法
2. **精确全名匹配**：如 `/PrivacyService/Process`
3. **前缀通配符**：如 `/AuditLog/*` 匹配所有 `/AuditLog/` 前缀方法

### 5 秒热重载机制

`DynamicWhitelist` 后台协程每 5 秒通过 `os.Stat` 轮询配置文件 `mtime`：

- 检测到文件变更 → 自动触发 `reload()` 解析
- 获取写锁全量原子替换 `clients` 映射
- 热路径（`CheckScope` / `IsAuthorized`）使用读锁，微秒级并发性能

**修改白名单后无需重启服务**，秒级生效。

### gRPC 拦截器集成

`pkg/tlsutil` 提供 `UnaryInterceptor` 和 `StreamInterceptor`，从 mTLS 对端证书中提取 CN，调用 `DynamicWhitelist.CheckScope` 进行鉴权。

---

## 七、命名 SSOT 运维

### 7.1 注册表管理

`pkg/naming` 是 PrivShield 跨服务业务标识的**唯一事实源 (Single Source of Truth, SSOT)**。

#### 核心设计约束

1. **唯一标识原则**：一个数据源实体有且仅有一个 canonical `datasource_id`
2. **Fail-Closed 零逃逸原则**：未知入站值必须报错，严禁静默回退
3. **代码防腐原则**：业务代码中严禁出现裸数据源字符串字面量

#### 当前注册表

| 序号 | API Code | Datasource ID | 状态 | 领域 | 字段数 | 别名示例 |
|---|---|---|---|---|---|---|
| 1 | `api1_yibao` | `ds_yibao` | active | medical | 19 | yibao, 医保, 医保结算 |
| 2 | `api2_kangyang` | `ds_kangyang` | active | healthcare | 27 | kangyang, 康养, 康养体检 |
| 3 | (未绑定) | `ds_mock3` | reserved | reserved | - | mock3, 政务 |
| 4 | (未绑定) | `ds_mock4` | reserved | reserved | - | mock4, 企业, 金融 |

#### 标识格式正则

- `datasource_id`：`^ds_[a-z][a-z0-9_]{1,30}$`
- `api_code`：`^api[1-9]_[a-z][a-z0-9_]{1,30}$`

#### 别名归一化优先级

1. Canonical ID 精确匹配（如 `"ds_yibao"`）→ 直接返回
2. API Code 匹配（如 `"api1_yibao"`）→ 触发 `RecordAPIAlias(target="api_code")`
3. 别名池不区分大小写匹配（如 `"YIBAO.CSV"`）→ 触发 `RecordAPIAlias(target="datasource_id")`
4. 别名池精确匹配（支持中文 `"医保"`）→ 触发 `RecordAPIAlias`
5. 全部未命中 → Fail-Closed 报错 `ErrUnknownDataSource`

#### 注册表管理操作

**新增数据源**：
1. 在 `pkg/naming/naming.go` 的 `Registry` 切片中添加 `Entry`
2. 定义新的 `const` 常量（`DS*` 和 `API*`）
3. 添加别名列表
4. 运行 CI 测试确认无别名冲突（`AliasConflicts()` 必须返回空）
5. 数据库 Schema 迁移会自动从 Registry 回填 `api_code`

**CI 断言**：
- `AliasConflicts()` 长度必须为 0
- `TestSecurityLevelsMatchTaxonomyYAML` 断言词表与 `rules/taxonomies/default.yaml` 同步

### 7.2 Observer 指标解读

`pkg/naming/observer.go` 定义了 `Observer` 接口作为可观测性扩展点。`pkg/metrics.Collector` 编译期实现此接口：

```go
var _ naming.Observer = (*metrics.Collector)(nil)
```

**注册方式**：

```go
mc := metrics.NewCollector("service-hub")
naming.SetObserver(mc)
```

#### 指标解读

| 指标 | 含义 | 正常值 | 告警阈值 |
|---|---|---|---|
| `privshield_api_alias_requests_total{target="api_code"}` | 业务方通过 api_code 调用（非 canonical） | 正常流量 | 持续增长表示客户端未迁移到 canonical ID |
| `privshield_api_alias_requests_total{target="datasource_id"}` | 通过别名（slug/文件名/中文名）调用 | 少量正常 | 大量增长表示外部调用方未使用规范 ID |
| `privshield_datasource_normalize_errors_total{reason="unknown"}` | 未知数据源标识 | 应为 0 | >0 需排查（可能非法调用） |
| `privshield_datasource_normalize_errors_total{reason="empty"}` | 空标识请求 | 应为 0 | >0 需排查客户端 |
| `privshield_datasource_normalize_errors_total{reason="reserved"}` | 命中预留位写操作 | 正常 | 大量表示业务方误用预留位 |
| `privshield_datasource_normalize_errors_total{reason="format_invalid"}` | 格式不合法 | 应为 0 | >0 需排查（调用方漏了边界归一化） |

**标签值常量**：
- Target：`"datasource_id"` / `"api_code"` / `"path"`
- Reason：`"unknown"` / `"empty"` / `"reserved"` / `"format_invalid"`

### 7.3 安全等级词表（L1-L5）

`pkg/naming/levels.go` 是数据安全分级的唯一事实源，桥接两套命名体系：

| L1-L5 标识 | Engine 内部名 | 中文名称 | 敏感度排名 |
|---|---|---|---|
| `L1` | `public` | 公开数据 | 1 |
| `L2` | `internal` | 内部数据 | 2 |
| `L3` | `confidential` | 敏感数据 | 3 |
| `L4` | `secret` | 高敏感数据 | 4 |
| `L5` | `top_secret` | 极敏感数据 | 5 |

**核心 API**：

| 函数 | 用途 |
|---|---|
| `SecurityLevelIDs()` | 返回 `["L1", "L2", "L3", "L4", "L5"]` |
| `SecurityLevelNames()` | 返回 `["public", "internal", "confidential", "secret", "top_secret"]` |
| `SecurityLevelLabel(level)` | 任意输入 → 中文名称（`"L4"` → `"高敏感数据"`） |
| `SecurityLevelName(level)` | 任意输入 → Engine 内部名（`"L3"` → `"confidential"`） |
| `NormalizeSecurityLevelID(level)` | 任意输入 → L1-L5 标识 |
| `SecurityLevelRank(level)` | 敏感度排名（1-5，未知返回 0） |
| `MaxSecurityLevelID(levels...)` | 返回多个等级中敏感度最高的 |

**约束**：
- `pkg/validation` 包显式委托给 `naming.SecurityLevelIDs()`，不得自建副本
- `pkg/store/levels.go` 使用 `naming.SecurityLevelL4/L5` 常量进行审计推荐
- 必须与 `rules/taxonomies/default.yaml` 的 `levels.*.id` 严格同步（CI 断言）

---

## 八、核心故障排查手册 (Runbook)

### 8.1 存证哈希链验真失败

**现象**：`POST /api/audit/chain/verify` 返回 `valid: false`，`broken_at_id` 指向特定记录。

**排查流程**：

1. 检查 `broken_at_id` 记录的 `timestamp` 是否与前序记录颠倒（时钟跳变）
2. 检查数据库是否存在手动 `UPDATE` 或 `DELETE` 审计日志的操作
3. 检查是否有未经 `BufferedAuditStore` 的直接并发 SQL 插入绕过了单 Worker 哈希绑定
4. 确认时间戳格式是否统一为 UTC RFC3339Nano
5. 如注入了 `AUDIT_LOG_HASH_KEY`，确认密钥在启动期间未被更改

### 8.2 SQLite 锁等待超时

**现象**：高并发写入时日志中出现 `database is locked`。

**排查流程**：

1. 确认是否已装配 `flusher.BufferedAuditStore`（未装配时单条落盘易锁库）
2. 确认 SQLite 连接池最大打开数（`MaxOpenConns` 不应超过 4）
3. 检查磁盘 I/O 状态（`iostat -x 1`），考虑将数据目录挂载至 NVMe SSD
4. 业务 QPS 持续超过 1,000 时，配置 `PG_DSN` 迁移至 PostgreSQL

### 8.3 敏感样本解密失败

**现象**：快照查询接口返回密文或解密错误。

**排查流程**：

1. 检查 `AUDIT_LOG_ENCRYPTION_KEY` 是否与数据写入时的密钥一致
2. 检查密文字符串前缀：
   - `enc:v2:` → 需要 HKDF-SM3 派生路径
   - `enc:v1:` → 需要 SHA-256 派生路径（旧数据）
   - 无前缀 → 返回 `ErrUnencryptedValue`，数据可能被篡改
3. 检查 Base64 字符串是否在传输中被截断或转义损坏
4. 检查数据库字符集是否为 UTF-8

### 8.4 微批刷盘器异常

**现象**：`HasFlushError()` 返回 `true`，或 `RetryPending()` 持续增长。

**排查流程**：

1. 检查 `LastFlushError()` 获取最近错误描述
2. 检查底层存储连接是否正常
3. 查看 `QueueDepth()` 判断队列积压程度
4. 如 `RetryPending() >= MaxStaged`（默认 50000），将触发 `ErrBacklogSaturated` 快速拒绝新写入
5. 刷盘器内置指数退避重试（25ms, 50ms, 75ms），最多 `MaxRetries` 次（默认 3）

### 8.5 归档段验证失败

**现象**：`VerifySegment` 返回错误。

**排查流程**：

| 错误信息 | 原因 | 处理 |
|---|---|---|
| `line hash chain mismatch` | 段文件被篡改、截断或行序被打乱 | 从备份恢复原始段文件 |
| `record count mismatch` | 段文件内容被增删 | 从备份恢复 |
| `boundary log ids mismatch` | 边界记录 ID 不一致 | 检查段文件完整性 |
| `boundary timestamps mismatch` | 边界时间戳不一致 | 检查段文件完整性 |
| `integrity hash does not match` | 日志行 9 要素被篡改 | 检查段文件与密钥 |
| `decrypt segment` 失败 | 密钥不匹配 | 确认 `AUDIT_LOG_ENCRYPTION_KEY` |
