# PrivShield 共享基础包 (Shared PKG) — 生产运维与加固手册

> **文档定位**：`pkg` 基础包及底层存储引擎、密码信封、mTLS 证书体系、归档存证与中间件网关的生产部署、性能调优、监控告警与故障排查（Runbook）运维指南。

---

## 目录

- [一、全局运维配置参数表](#一全局运维配置参数表)
  - [1.1 隐私引擎 (privshield-agent)](#11-隐私引擎-privshield-agent)
  - [1.2 隐私网关 (privshield-gateway)](#12-隐私网关-privshield-gateway)
  - [1.3 数据服务调度中枢 (service-hub)](#13-数据服务调度中枢-service-hub)
  - [1.4 数据源资产管理 (datasource-mgr)](#14-数据源资产管理-datasource-mgr)
  - [1.5 审计存证日志 (audit-log)](#15-审计存证日志-audit-log)
  - [1.6 统一运维控制台 BFF (console/bff-go)](#16-统一运维控制台-bff-consolebff-go)
  - [1.7 跨服务共享环境变量](#17-跨服务共享环境变量)
- [二、存储引擎运维与性能调优](#二存储引擎运维与性能调优)
  - [2.1 SQLite WAL 生产模式配置](#21-sqlite-wal-生产模式配置)
  - [2.2 PostgreSQL Phase B 分布式集群与连接池调优](#22-postgresql-phase-b-分布式集群与连接池调优)
  - [2.3 数据库自动化迁移工具 (`store/cmd/migrate`)](#23-数据库自动化迁移工具-storecmdmigrate)
  - [2.4 归档存证段运维](#24-归档存证段运维)
  - [2.5 严格存储模式 (Strict Storage)](#25-严格存储模式-strict-storage)
- [三、商用密码与 mTLS 证书安全运维](#三商用密码与-mtls-证书安全运维)
  - [3.1 国密 SM4 信封加密与密钥管理](#31-国密-sm4-信封加密与密钥管理)
  - [3.2 HMAC-SM3 哈希链密钥注入与轮转](#32-hmac-sm3-哈希链密钥注入与轮转)
  - [3.3 mTLS 客户端证书与 CN 白名单热加载](#33-mtls-客户端证书与-cn-白名单热加载)
- [四、全栈监控大盘与 Prometheus 告警规则](#四全栈监控大盘与-prometheus-告警规则)
  - [4.1 业务指标清单 (`pkg/metrics.Collector`)](#41-业务指标清单-packagemetricscollector)
  - [4.2 传输层 RED 指标 (`pkg/observability.REDMetrics`)](#42-传输层-red-指标-packageobservabilityredmetrics)
  - [4.3 刷盘器运行时指标 (`pkg/store/flusher`)](#43-刷盘器运行时指标-packagestoreflusher)
  - [4.4 命名路由观测指标 (`pkg/naming.Observer`)](#44-命名路由观测指标-packagenamingobserver)
  - [4.5 推荐 Prometheus 告警规则](#45-推荐-prometheus-告警规则)
- [五、安全门控运维 (Security Gate Operations)](#五安全门控运维-security-gate-operations)
  - [5.1 ValidateFailClosed 五条铁律](#51-validatefailclosed-五条铁律)
  - [5.2 常见启动拒绝场景排查](#52-常见启动拒绝场景排查)
- [六、归档存证运维 (Archive Operations)](#六归档存证运维-archive-operations)
  - [6.1 归档段格式规范](#61-归档段格式规范)
  - [6.2 归档段完整性校验](#62-归档段完整性校验)
  - [6.3 从归档恢复数据](#63-从归档恢复数据)
  - [6.4 保留策略配置](#64-保留策略配置)
- [七、核心故障排查手册 (Runbook)](#七核心故障排查手册-runbook)

---

## 一、全局运维配置参数表

### 1.1 隐私引擎 (privshield-agent)

> 核心隐私计算引擎，暴露 REST (`:8079`) + gRPC (`:50051`)。

#### 监听与生命周期

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `PRIVACY_REST_HOST` | string | `127.0.0.1` | `0.0.0.0` | REST HTTP 监听地址 |
| `PRIVACY_REST_PORT` | int | `8079` | `8079` | REST HTTP 监听端口 |
| `PRIVACY_GRPC_HOST` | string | `127.0.0.1` | `0.0.0.0` | gRPC 监听地址 |
| `PRIVACY_GRPC_PORT` | int | `50051` | `50051` | gRPC 监听端口 |
| `PRIVACY_LOG_LEVEL` | string | `INFO` | `info` | 日志级别：`DEBUG` / `INFO` / `WARN` / `ERROR` |
| `PRIVACY_PPROF_ENABLED` | bool | `false` | `false` (生产关闭) | 启用 `/debug/pprof` 性能分析端点（需 `ops:admin` 权限） |
| `PRIVACY_SHUTDOWN_DRAIN_SECONDS` | int | `5` | `10 ~ 30` | HTTP 优雅停机流量排空等待秒数 |
| `PRIVACY_GRPC_GRACEFUL_STOP_SECONDS` | int | `15` | `15 ~ 30` | gRPC 优雅停机超时后强制关闭秒数 |

#### TLS / mTLS / 认证

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `PRIVACY_TLS_ENABLED` | bool | `false` | `true` | 启用 TLS（HTTPS + gRPC TLS） |
| `PRIVACY_TLS_CERT_FILE` | string | `""` | `/etc/privshield/certs/server.crt` | 服务端证书 PEM 路径 |
| `PRIVACY_TLS_KEY_FILE` | string | `""` | `/etc/privshield/certs/server.key` | 服务端私钥 PEM 路径 |
| `PRIVACY_TLS_CA_FILE` | string | `""` | `/etc/privshield/certs/ca.crt` | 客户端认证 CA 证书路径 |
| `PRIVACY_REQUIRE_TLS` | bool | `false` | `true` | 安全门控：为 `true` 时 TLS 未启用则拒绝启动 |
| `PRIVACY_AUTH_ENABLED` | bool | `false` | `true` | 启用 API Key 认证 |
| `PRIVACY_AUTH_API_KEY` | string | `""` | KMS 注入 | 单内部 API Key（通配权限），别名 `PRIVACY_API_KEY` |
| `PRIVACY_AUTH_INTERNAL_API_KEYS` | string | `""` | 格式 `key:name:scope;...` | 多内部 API Key，按名称与作用域精细控制 |
| `PRIVACY_AUTH_EXTERNAL_API_KEYS` | string | `""` | KMS 注入 | 外部 API Key |
| `PRIVACY_AUTH_STATIC_API_KEYS` | string | `""` | KMS 注入 | 静态外部 API Key（合并入外部 Key 集） |
| `PRIVACY_AUTH_INTERNAL_MTLS_ENABLED` | bool | `false` | `true` | 启用 gRPC mTLS 客户端证书认证 |
| `PRIVACY_AUTH_MTLS_WHITELIST_FILE` | string | `""` | `/etc/privshield/whitelist.yaml` | CN 白名单 YAML 路径（支持秒级热重载） |
| `PRIVACY_AUTH_MTLS_ALLOWED_CNS` | string | `""` | 逗号分隔 CN 列表 | 补充允许 CN 列表 |
| `PRIVACY_HEALTH_NO_AUTH` | bool | `true` | `true` | 健康检查端点豁免认证 |

#### 限流

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `PRIVACY_RATE_LIMIT_ENABLED` | bool | `false` | `true` | 启用全局限流 |
| `PRIVACY_RATE_LIMIT_RPS` | int | `1000` | `2000 ~ 5000` | 全局每秒请求补充速率 |
| `PRIVACY_RATE_LIMIT_BURST` | int | `2000` | `4000 ~ 10000` | 全局令牌桶突发容量 |
| `PRIVACY_RATE_LIMIT_DEFAULT_RPS` | int | `100` | `200 ~ 500` | 安全配置级默认每 IP RPS |
| `PRIVACY_RATE_LIMIT_DEFAULT_BURST` | int | `200` | `400 ~ 1000` | 安全配置级默认每 IP 突发 |
| `PRIVACY_RATE_LIMIT_REDIS_URL` | string | `""` | Redis 集群地址 | 分布式限流 Redis URL（空=本地限流） |
| `PRIVACY_HEALTH_NO_RATE_LIMIT` | bool | `true` | `true` | 健康检查端点豁免限流 |

#### 引擎运行参数

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `PRIVACY_NAMESPACE` | string | `default` | 按租户设置 | 隐私预算租户隔离命名空间 |
| `PRIVACY_SERVICE_NAME` | string | `PrivShield` | — | 服务名（用于追踪与诊断标识） |
| `PRIVACY_RULES_DIR` | string | `rules/domains` | — | 领域分类分级规则目录 |
| `PRIVACY_STANDARDS_DIR` | string | `rules/standards` | — | 标准映射目录 |
| `PRIVACY_CONFIG_FILE` | string | `config/privacy.yaml` | — | 隐私策略 YAML 配置文件路径 |
| `PRIVACY_RULES_RELOAD_CHECK_SECONDS` | int | `5` | `5 ~ 30` | 规则文件 mtime 热重载检测节流间隔秒数（0=禁用节流） |
| `PRIVACY_IMAGE_ALLOWED_DIRS` | string | `""` | 指定白名单目录 | 医学影像允许读取的目录白名单（逗号分隔，默认含 `data/`、`uploads/`、`samples/`、`$TMPDIR`） |
| `PRIVACY_DICOM_MAX_FILE_SIZE` | int64 | `268435456` | `268435456` (256MB) | 单个 DICOM 文件最大允许字节数 |

#### LLM 第三层分类

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `PRIVACY_LLM_ENABLE` | string | `false` | `"true"` (需启用时) | 启用第三层 LLM 动态分类（必须为 `"true"`） |
| `PRIVACY_LLM_ENDPOINT` | string | `http://localhost:8000/v1/chat/completions` | 内网 LLM 地址 | LLM 服务端点 URL |
| `PRIVACY_LLM_API_KEY` | string | `""` | KMS 注入 | LLM 服务 Bearer 认证密钥 |
| `PRIVACY_LLM_MODEL` | string | `qwen3.5` | 按实际模型设置 | LLM 模型名称 |
| `PRIVACY_LLM_MAX_CONCURRENCY` | int | `4` | `4 ~ 16` | LLM 推理最大并发请求数 |
| `PRIVACY_LLM_ALLOW_INSECURE_HTTP_ENDPOINT` | bool | `false` | `false` | 允许非回环地址明文 HTTP LLM 端点（生产禁用） |

### 1.2 隐私网关 (privshield-gateway)

> 反向代理网关，暴露 REST (`:8000`) + gRPC (`:50000`)，P2C-EWMA 负载均衡。

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `GATEWAY_HOST` | string | `127.0.0.1` | `0.0.0.0` | 网关 HTTP 监听地址 |
| `GATEWAY_PORT` | int | `8000` | `8000` | 网关 HTTP 监听端口 |
| `GATEWAY_GRPC_HOST` | string | `127.0.0.1` | `0.0.0.0` | 网关 gRPC 监听地址 |
| `GATEWAY_GRPC_PORT` | int | `50000` | `50000` | 网关 gRPC 监听端口 |
| `GATEWAY_REQUIRE_TLS` | bool | `false` | `true` | 安全门控：要求 TLS 未启用则拒绝启动 |
| `GATEWAY_BACKENDS` | string | `127.0.0.1:8079` | 多后端逗号分隔 | 后端 Agent 地址列表 |
| `GATEWAY_STRATEGY` | string | `p2c` | `p2c` | 负载均衡策略：`p2c` / `round_robin` / `least_conn` |
| `GATEWAY_METRICS_API_KEY` | string | `""` | KMS 注入 | `/metrics` 端点 Bearer Key（空=开放） |

### 1.3 数据服务调度中枢 (service-hub)

> 流水线任务调度中枢，REST (`:8082`) + gRPC (`:50052`)。

#### 监听与生命周期

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `SERVICE_HUB_HOST` | string | `127.0.0.1` | `0.0.0.0` | HTTP 监听地址 |
| `SERVICE_HUB_PORT` | int | `8082` | `8082` | HTTP 监听端口 |
| `SERVICE_HUB_GRPC_HOST` | string | `127.0.0.1` | `0.0.0.0` | gRPC 监听地址 |
| `SERVICE_HUB_GRPC_PORT` | int | `50052` | `50052` | gRPC 监听端口 |
| `SERVICE_HUB_LOG_FORMAT` | string | `json` | `json` | 日志格式：`json` / `text` |
| `SERVICE_HUB_LOG_LEVEL` | string | `info` | `info` | 日志级别 |
| `SERVICE_HUB_SHUTDOWN_TIMEOUT` | int | `5` | `10 ~ 30` | HTTP 优雅停机超时秒数 |

#### TLS / 认证 / CORS

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `SERVICE_HUB_TLS_ENABLED` | bool | `false` | `true` | 启用 TLS |
| `SERVICE_HUB_TLS_CERT_FILE` | string | `""` | 证书路径 | 服务端证书 PEM |
| `SERVICE_HUB_TLS_KEY_FILE` | string | `""` | 私钥路径 | 服务端私钥 PEM |
| `SERVICE_HUB_TLS_CA_FILE` | string | `""` | CA 路径 | 客户端 CA 证书 |
| `SERVICE_HUB_TLS_CLIENT_AUTH` | string | `""` | `require` | 客户端认证模式 |
| `SERVICE_HUB_TLS_PINNED_PUBKEY_FILE` | string | `""` | SPKI PEM 路径 | SPKI 公钥钉选 PEM 文件 |
| `SERVICE_HUB_REQUIRE_TLS` | bool | `false` | `true` | 安全门控：TLS 未启用则拒绝启动 |
| `SERVICE_HUB_API_KEY` | string | `""` | KMS 注入 | 入站 API Key |
| `SERVICE_HUB_CORS_ORIGINS` | string | `""` | 指定前端域名 | CORS 允许来源（逗号分隔） |

#### 任务调度与存储

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `SERVICE_HUB_MAX_QUEUE` | int | `1000` | `2000 ~ 5000` | 最大任务队列深度 |
| `SERVICE_HUB_SCHEDULE_TIMEOUT` | int | `30` | `30 ~ 60` | 任务调度超时秒数 |
| `SERVICE_HUB_DB_PATH` | string | `""` | `/var/lib/privshield/hub.db` | SQLite 任务库路径（空=内存模式） |
| `SERVICE_HUB_PG_DSN` | string | `""` | PostgreSQL 连接串 | Phase B 多副本 PostgreSQL DSN |
| `SERVICE_HUB_PG_MAX_CONNS` | int | `10` | `N_cpu * 4` (max 64) | PostgreSQL 连接池最大并发 |
| `SERVICE_HUB_PG_MIN_CONNS` | int | `2` | `max(2, N_cpu)` | PostgreSQL 连接池最小保活 |
| `SERVICE_HUB_LEASE_TTL` | int | `60` | `60 ~ 120` | 任务租约 TTL 秒数 |
| `SERVICE_HUB_RETENTION_DAYS` | int | `30` | `30 ~ 90` | 数据保留天数 |
| `SERVICE_HUB_STRICT_STORAGE` | bool | `true` | `true` | 严格存储模式（存储故障即退出，不静默回退内存） |

#### 上游连接

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `PRIVACY_AGENT_REST_HOST` | string | `127.0.0.1` | Agent 地址 | 上游 Agent REST 主机 |
| `PRIVACY_REST_PORT` | int | `8079` | `8079` | 上游 Agent REST 端口 |
| `PRIVACY_AGENT_API_KEY` | string | `""` | KMS 注入 | 上游 Agent API Key |
| `PRIVACY_AGENT_URLS` | string | `""` | 多 Agent 逗号分隔 | 多 Agent URL 列表（覆盖单主机配置） |
| `DATASOURCE_MGR_HOST` | string | `127.0.0.1` | 数据源管理器地址 | 数据源管理器 HTTP 主机 |
| `DATASOURCE_MGR_PORT` | int | `8083` | `8083` | 数据源管理器 HTTP 端口 |
| `DATASOURCE_MGR_GRPC_HOST` | string | `127.0.0.1` | 数据源管理器 gRPC 主机 | 数据源管理器 gRPC 主机 |
| `DATASOURCE_MGR_GRPC_PORT` | int | `50053` | `50053` | 数据源管理器 gRPC 端口 |
| `SERVICE_HUB_DATASOURCE_API_KEY` | string | `""` | KMS 注入 | 调用数据源管理器的 API Key |
| `SERVICE_HUB_AUDIT_LOG_URLS` | string | `""` | 审计日志 URL 列表 | 审计日志 REST URL（逗号分隔） |
| `SERVICE_HUB_AUDIT_LOG_API_KEY` | string | `""` | KMS 注入 | 审计日志 API Key（回退 `AUDIT_LOG_API_KEY`） |
| `SERVICE_HUB_AUDIT_LOG_TIMEOUT` | int | `10` | `10 ~ 30` | 审计日志提交超时秒数 |
| `SERVICE_HUB_AUDIT_LOG_MAX_RETRIES` | int | `3` | `3 ~ 5` | 审计日志提交重试次数 |

#### 审计日志客户端 TLS

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_AUDIT_LOG_TLS_ENABLED` | bool | `false` | 启用审计日志客户端 TLS |
| `SERVICE_HUB_AUDIT_LOG_TLS_CERT_FILE` | string | `""` | 客户端证书（回退 `SERVICE_HUB_TLS_CERT_FILE`） |
| `SERVICE_HUB_AUDIT_LOG_TLS_KEY_FILE` | string | `""` | 客户端私钥（回退 `SERVICE_HUB_TLS_KEY_FILE`） |
| `SERVICE_HUB_AUDIT_LOG_TLS_CA_FILE` | string | `""` | 客户端 CA（回退 `SERVICE_HUB_TLS_CA_FILE`） |

#### 限流

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `SERVICE_HUB_RATE_LIMIT_RPS` | int | `100` | `200 ~ 500` | 每 IP 令牌桶 RPS |
| `SERVICE_HUB_RATE_LIMIT_BURST` | int | `200` | `400 ~ 1000` | 令牌桶突发容量 |

### 1.4 数据源资产管理 (datasource-mgr)

> 数据源资产管理与敏感特征自动探查，REST (`:8083`) + gRPC (`:50053`)。

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `DATASOURCE_MGR_HOST` | string | `127.0.0.1` | `0.0.0.0` | HTTP 监听地址 |
| `DATASOURCE_MGR_PORT` | int | `8083` | `8083` | HTTP 监听端口 |
| `DATASOURCE_MGR_GRPC_HOST` | string | `127.0.0.1` | `0.0.0.0` | gRPC 监听地址 |
| `DATASOURCE_MGR_GRPC_PORT` | int | `50053` | `50053` | gRPC 监听端口 |
| `DATASOURCE_MGR_TLS_ENABLED` | bool | `false` | `true` | 启用 TLS |
| `DATASOURCE_MGR_TLS_CERT_FILE` | string | `""` | 证书路径 | 服务端证书 PEM |
| `DATASOURCE_MGR_TLS_KEY_FILE` | string | `""` | 私钥路径 | 服务端私钥 PEM |
| `DATASOURCE_MGR_TLS_CA_FILE` | string | `""` | CA 路径 | 客户端 CA 证书 |
| `DATASOURCE_MGR_TLS_CLIENT_AUTH` | string | `""` | `require` | 客户端认证模式 |
| `DATASOURCE_MGR_TLS_PINNED_PUBKEY_FILE` | string | `""` | SPKI PEM 路径 | SPKI 公钥钉选文件 |
| `DATASOURCE_MGR_REQUIRE_TLS` | bool | `false` | `true` | 安全门控：TLS 未启用则拒绝启动 |
| `DATASOURCE_MGR_API_KEY` | string | `""` | KMS 注入 | 入站 API Key |
| `DATASOURCE_MGR_CORS_ORIGINS` | string | `""` | 指定前端域名 | CORS 允许来源 |
| `DATASOURCE_MGR_LOG_FORMAT` | string | `json` | `json` | 日志格式 |
| `DATASOURCE_MGR_LOG_LEVEL` | string | `info` | `info` | 日志级别 |
| `DATASOURCE_MGR_SHUTDOWN_TIMEOUT` | int | `5` | `10 ~ 30` | HTTP 优雅停机超时秒数 |
| `DATASOURCE_MGR_STRICT_STORAGE` | bool | `true` | `true` | 严格存储模式 |
| `DATASOURCE_MGR_RATE_LIMIT_RPS` | int | `100` | `200 ~ 500` | 每 IP 令牌桶 RPS |
| `DATASOURCE_MGR_RATE_LIMIT_BURST` | int | `200` | `400 ~ 1000` | 令牌桶突发容量 |

### 1.5 审计存证日志 (audit-log)

> 不可篡改审计存证服务，REST (`:8084`) + gRPC (`:50054`)。

#### 监听与生命周期

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `AUDIT_LOG_HOST` | string | `127.0.0.1` | `0.0.0.0` | HTTP 监听地址 |
| `AUDIT_LOG_PORT` | int | `8084` | `8084` | HTTP 监听端口 |
| `AUDIT_LOG_GRPC_HOST` | string | `127.0.0.1` | `0.0.0.0` | gRPC 监听地址 |
| `AUDIT_LOG_GRPC_PORT` | int | `50054` | `50054` | gRPC 监听端口 |
| `AUDIT_LOG_LOG_FORMAT` | string | `json` | `json` | 日志格式：`json` / `text` |
| `AUDIT_LOG_LOG_LEVEL` | string | `info` | `info` | 日志级别 |
| `AUDIT_LOG_SHUTDOWN_TIMEOUT` | int | `5` | `10 ~ 30` | HTTP 优雅停机超时秒数 |
| `AUDIT_LOG_CORS_ORIGINS` | string | `""` | 指定前端域名 | CORS 允许来源（逗号分隔） |

#### TLS / mTLS / 认证

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `AUDIT_LOG_TLS_ENABLED` | bool | `false` | `true` | 启用 TLS |
| `AUDIT_LOG_TLS_CERT_FILE` | string | `""` | 证书路径 | 服务端证书 PEM |
| `AUDIT_LOG_TLS_KEY_FILE` | string | `""` | 私钥路径 | 服务端私钥 PEM |
| `AUDIT_LOG_TLS_CA_FILE` | string | `""` | CA 路径 | 客户端 CA 证书 |
| `AUDIT_LOG_TLS_CLIENT_AUTH` | string | `""` | `require` | 客户端认证模式 |
| `AUDIT_LOG_TLS_PINNED_PUBKEY_FILE` | string | `""` | SPKI PEM 路径 | SPKI 公钥钉选文件 |
| `AUDIT_LOG_REQUIRE_TLS` | bool | `false` | `true` | 安全门控：TLS 未启用则拒绝启动 |
| `AUDIT_LOG_API_KEY` | string | `""` | KMS 注入 | 入站写入 API Key（非回环绑定时必填） |
| `AUDIT_LOG_READER_API_KEY` | string | `""` | KMS 注入 | 只读验证 API Key（P1-6 职责分离，必须与写入 Key 不同） |

#### 数据库与存储

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `AUDIT_LOG_DB_PATH` | string | `""` | `/var/lib/privshield/audit.db` | SQLite 数据库路径（空=内存模式） |
| `AUDIT_LOG_PG_DSN` | string | `""` (回退 `PG_DSN`) | PostgreSQL 连接串 | Phase B 多副本 PostgreSQL DSN |
| `AUDIT_LOG_PG_MAX_CONNS` | int | `0` (自动) | `N_cpu * 4` (max 64) | PostgreSQL 连接池最大并发（0=自动计算） |
| `AUDIT_LOG_PG_MIN_CONNS` | int | `0` (自动) | `max(2, N_cpu)` | PostgreSQL 连接池最小保活（0=自动计算） |
| `AUDIT_LOG_DB_WRITE_ONLY` | bool | `false` | `true` (生产) | 写-only 数据库账户模式：启动自检确认无 UPDATE/DELETE 权限（P1-6 存证不可删） |
| `AUDIT_LOG_STRICT_STORAGE` | bool | `true` (回退 `STRICT_STORAGE`) | `true` | 严格存储模式：存储连接失败时立即退出，不静默回退内存 |

#### 加密与哈希链

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `AUDIT_LOG_ENCRYPTION_KEY` | string | `""` (回退 `PRIVACY_AUDIT_KEY`) | 32 位高强度随机串 | SM4-GCM 信封加密主密钥（快照样本 + 归档段加密，非回环绑定时必填，启用保留删除时必填） |
| `AUDIT_LOG_HASH_KEY` | string | `""` | 密码学安全随机串 | HMAC-SM3 哈希链完整性密钥（由密码管理平台注入，空=无密钥 SM3 仅用于向后兼容） |

#### 微批刷盘器调优

| 环境变量 | 类型 | 默认值 | 内部有效默认 | 生产建议 | 说明 |
|---|---|---|---|---|---|
| `AUDIT_LOG_FLUSH_BATCH_SIZE` | int | `0` | `200` | `200 ~ 500` | 单批最大写入条数（0=使用内部默认） |
| `AUDIT_LOG_FLUSH_INTERVAL_MS` | int | `0` | `20` (ms) | `20 ~ 50` | 最长刷盘等待时间窗口（毫秒） |
| `AUDIT_LOG_FLUSH_QUEUE_SIZE` | int | `0` | `10000` | `10000 ~ 50000` | 环形缓冲队列容量 |
| `AUDIT_LOG_FLUSH_ENQUEUE_TIMEOUT_MS` | int | `0` | `500` (ms) | `500 ~ 2000` | 队列满时等待可用槽位超时（毫秒，防越序与阻塞） |
| `AUDIT_LOG_FLUSH_MAX_STAGED` | int | `0` | `50000` | `50000 ~ 100000` | 内存读己之写暂存映射上限（防存储故障期 OOM） |
| `AUDIT_LOG_FLUSH_CLOSE_TIMEOUT_MS` | int | `0` | `10000` (ms) | `10000 ~ 30000` | 优雅停机排空等待超时（毫秒） |

#### 归档与保留策略

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `AUDIT_LOG_ARCHIVE_DIR` | string | `data/archives` | `/var/lib/privshield/archives` | 归档存证段写入目录（启用保留删除时必填） |
| `AUDIT_LOG_ARCHIVE_PAGE_SIZE` | int | `0` (=500) | `500 ~ 2000` | 每归档批次拉取记录数（0=使用全局默认 500） |
| `AUDIT_LOG_RETENTION_DAYS` | int | `0` | `0` 或 `>=1095` | 保留天数：`0`=永不物理删除（默认，符合数据安全法三年存证要求）；`>0` 时必须 `>=1095`（3 年），且必须配置归档目录与加密密钥。清理任务每 6 小时执行一次 |

#### 上游 Agent 连接

| 环境变量 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `PRIVACY_AGENT_REST_HOST` | string | `127.0.0.1` | 上游 Agent REST 主机 |
| `PRIVACY_REST_PORT` | int | `8079` | 上游 Agent REST 端口 |
| `PRIVACY_AGENT_API_KEY` | string | `""` | 上游 Agent API Key |
| `PRIVACY_AGENT_URLS` | string | `""` | 多 Agent URL 列表（逗号分隔，覆盖单主机） |

#### 限流

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `AUDIT_LOG_RATE_LIMIT_RPS` | int | `100` | `200 ~ 500` | 每 IP 令牌桶 RPS |
| `AUDIT_LOG_RATE_LIMIT_BURST` | int | `200` | `400 ~ 1000` | 令牌桶突发容量 |

### 1.6 统一运维控制台 BFF (console/bff-go)

> Go gRPC/HTTPS 代理网关，REST (`:8081`) + 可选 gRPC (`:50055`)。

| 环境变量 | 类型 | 默认值 | 生产建议 | 说明 |
|---|---|---|---|---|
| `PRIVACY_CONSOLE_HOST` | string | `127.0.0.1` | `0.0.0.0` | BFF HTTP 监听地址 |
| `PRIVACY_CONSOLE_PORT` | int | `8081` | `8081` | BFF HTTP 监听端口 |
| `PRIVACY_CONSOLE_STATIC_DIR` | string | `../web/dist` | — | 前端 SPA 静态资源目录（空=禁用） |
| `CONSOLE_API_KEY` | string | `""` | KMS 注入 | BFF 入站 API Key |
| `CONSOLE_RATE_LIMIT` | int | `600` | `600 ~ 1200` | 每 IP 每分钟速率限制 |
| `CONSOLE_MAX_UPLOAD_BYTES` | int64 | `10485760` | `10485760` (10MB) | 最大上传字节数 |
| `PRIVACY_CONSOLE_TLS_ENABLED` | bool | `false` | `true` | 启用 BFF 入站 TLS |
| `PRIVACY_CONSOLE_REQUIRE_TLS` | bool | `false` | `true` | 安全门控 |
| `PRIVACY_CONSOLE_TLS_CERT_FILE` | string | `""` | 证书路径 | BFF 服务端证书 |
| `PRIVACY_CONSOLE_TLS_KEY_FILE` | string | `""` | 私钥路径 | BFF 服务端私钥 |
| `PRIVACY_CONSOLE_TLS_CA_FILE` | string | `""` | CA 路径 | 客户端 CA 证书 |
| `PRIVACY_CONSOLE_TLS_CLIENT_AUTH` | string | `""` | `require` | 客户端认证模式 |
| `PRIVACY_CONSOLE_TLS_PINNED_PUBKEY_FILE` | string | `""` | SPKI PEM 路径 | 公钥钉选文件 |
| `PRIVACY_CONSOLE_GRPC_ENABLED` | bool | `false` | `true` | 启用 BFF gRPC 代理 |
| `PRIVACY_CONSOLE_GRPC_HOST` | string | `127.0.0.1` | `0.0.0.0` | BFF gRPC 监听地址 |
| `PRIVACY_CONSOLE_GRPC_PORT` | int | `50055` | `50055` | BFF gRPC 监听端口 |
| `PRIVACY_AGENT_GRPC_HOST` | string | `127.0.0.1` | Agent gRPC 地址 | 上游 Agent gRPC 主机 |
| `PRIVACY_AGENT_GRPC_PORT` | int | `50051` | `50051` | 上游 Agent gRPC 端口 |
| `PRIVACY_AGENT_TLS_ENABLED` | bool | `false` | `true` | 启用上游 Agent TLS |
| `PRIVACY_AGENT_TLS_CERT_FILE` | string | `""` | 客户端证书路径 | 客户端证书 PEM |
| `PRIVACY_AGENT_TLS_KEY_FILE` | string | `""` | 客户端私钥路径 | 客户端私钥 PEM |
| `PRIVACY_AGENT_TLS_CA_FILE` | string | `""` | CA 路径 | CA 证书 PEM |
| `PRIVACY_AGENT_TLS_SERVER_NAME` | string | `""` | 服务端 CN | TLS ServerName 覆盖 |
| `PRIVACY_AGENT_TLS_INSECURE_SKIP_VERIFY` | bool | `false` | `false` | 跳过服务端证书验证（仅测试） |
| `PRIVACY_AGENT_RETRY_MAX_ATTEMPTS` | int | `6` | `6 ~ 10` | gRPC 重试最大尝试次数 |
| `PRIVACY_AGENT_RETRY_INITIAL_BACKOFF` | int | `1` | `1` | 初始重试退避秒数 |
| `PRIVACY_AGENT_RETRY_MAX_BACKOFF` | int | `8` | `8` | 最大重试退避秒数 |
| `PRIVACY_GRPC_CALL_TIMEOUT` | duration | `60s` | `60s ~ 120s` | gRPC 调用超时 |
| `BFF_HUB_URL` | string | `http://127.0.0.1:8082` | 内网 Hub 地址 | 调度中枢 HTTP URL |
| `BFF_DATASOURCE_URL` | string | `http://127.0.0.1:8083` | 内网数据源地址 | 数据源管理器 HTTP URL |
| `BFF_AUDIT_URL` | string | `http://127.0.0.1:8084` | 内网审计地址 | 审计日志 HTTP URL |
| `BFF_HUB_API_KEY` | string | `""` | KMS 注入 | 调用调度中枢 API Key |
| `BFF_DATASOURCE_API_KEY` | string | `""` | KMS 注入 | 调用数据源管理器 API Key |
| `BFF_AUDIT_API_KEY` | string | `""` | KMS 注入 | 调用审计日志 API Key |
| `LB_ALLOWED_HOSTS` | string | `""` | 探针域名列表 | 负载均衡探针主机白名单 |

### 1.7 跨服务共享环境变量

以下环境变量被多个服务共同消费，需在部署编排中统一配置：

| 环境变量 | 消费服务 | 说明 |
|---|---|---|
| `PRIVACY_AUTH_MTLS_WHITELIST_FILE` | agent, service-hub, datasource-mgr, audit-log, console/bff-go | mTLS CN 白名单 YAML 路径 |
| `PRIVACY_REST_PORT` | agent, service-hub, audit-log, console/bff-go | Agent REST 端口（其他服务作为上游引用） |
| `PRIVACY_AGENT_REST_HOST` | service-hub, audit-log, console/bff-go | Agent REST 主机（其他服务作为上游引用） |
| `PRIVACY_AGENT_API_KEY` | service-hub, audit-log, console/bff-go | Agent API Key（其他服务作为上游认证） |
| `STRICT_STORAGE` | 四个微服务（回退默认） | 全局严格存储模式开关（各服务 `*_STRICT_STORAGE` 优先） |
| `PG_DSN` | audit-log（回退） | 遗留 PostgreSQL DSN（`AUDIT_LOG_PG_DSN` 为空时回退） |
| `PRIVACY_AUDIT_KEY` | audit-log（回退） | 遗留加密密钥（`AUDIT_LOG_ENCRYPTION_KEY` 为空时回退） |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | 全服务（`pkg/observability`） | OpenTelemetry OTLP 导出端点 |
| `PRIVACY_SERVICE_NAME` | 全服务（`pkg/observability`） | 追踪服务名标识 |

---

## 二、存储引擎运维与性能调优

### 2.1 SQLite WAL 生产模式配置

当部署为单节点轻量网关时，系统采用嵌入式 SQLite 作为存储。

#### 关键 PRAGMA 参数与机制：
1. **WAL 模式 (`PRAGMA journal_mode=WAL`)**：
   * 写入追加到 `-wal` 文件，读操作直接读取主库与 WAL 快照，实现读写互不阻塞；
2. **锁重试超时 (`PRAGMA busy_timeout=5000`)**：
   * 遇到瞬间写锁冲突时，自动在内核层进行最长 5,000ms 的指数重试，消除 `database is locked` 偶发异常；
3. **安全同步级别 (`PRAGMA synchronous=NORMAL`)**：
   * 相比 `FULL` 提升 10 倍以上写入性能，且在现代文件系统与 WAL 机制下仍保证掉电不损坏；
4. **外键约束 (`PRAGMA foreign_keys=ON`)**：
   * 级联保障快照表与审计日志表的数据完整性。

> [!TIP]
> **目录与权限建议**：确保 `DB_PATH` 所在目录具有可写权限，且同目录下有足够的磁盘空间用于生成 `-wal` 和 `-shm` 共享内存文件。

### 2.2 PostgreSQL Phase B 分布式集群与连接池调优

面向政务云多副本高并发部署场景，配置 `PG_DSN` 即可无缝切换至 PostgreSQL 存储集群。

#### 2.2.1 自适应连接池计算公式 (`pkg/store/postgres/postgres.go`)
系统根据宿主机/容器的可用 CPU 核心数自动调整连接池大小：
$$\text{MaxConns} = \min\left(64, \max\left(10, N_{cpu} \times 4\right)\right)$$
$$\text{MinConns} = \min\left(\text{MaxConns}, \max\left(2, N_{cpu}\right)\right)$$

* 连接最大空闲时间：`30m`；
* 连接最长生命周期：`1h`；
* 健康检查探测周期：`1m`。

> [!NOTE]
> 审计日志服务 (`audit-log`) 可通过 `AUDIT_LOG_PG_MAX_CONNS` / `AUDIT_LOG_PG_MIN_CONNS` 显式覆盖自动计算值（设为 0 则使用上述自适应公式）。调度中枢 (`service-hub`) 同理使用 `SERVICE_HUB_PG_MAX_CONNS` / `SERVICE_HUB_PG_MIN_CONNS`。

#### 2.2.2 审计日志原生分区管理
PostgreSQL 审计日志表 `audit_logs` 采用按月范围分区（Range Partitioning）：
```sql
CREATE TABLE IF NOT EXISTS audit_logs (
    id VARCHAR(64) NOT NULL,
    task_id VARCHAR(64),
    timestamp TIMESTAMPTZ NOT NULL,
    operation VARCHAR(32) NOT NULL,
    ...
    PRIMARY KEY (id, timestamp)
) PARTITION BY RANGE (timestamp);

-- 月度分区示例
CREATE TABLE IF NOT EXISTS audit_logs_y2026m08 PARTITION OF audit_logs
    FOR VALUES FROM ('2026-08-01 00:00:00+00') TO ('2026-09-01 00:00:00+00');
```

### 2.3 数据库自动化迁移工具 (`store/cmd/migrate`)

`pkg/store/cmd/migrate` 提供了独立且幂等的数据库 Schema 升级 CLI 工具：

```bash
# 查看帮助
go run pkg/store/cmd/migrate/main.go -help

# 对 SQLite 数据库执行迁移
go run pkg/store/cmd/migrate/main.go -driver sqlite -dsn /var/lib/privshield/data.db

# 对 PostgreSQL 数据库执行迁移
go run pkg/store/cmd/migrate/main.go -driver postgres -dsn "postgres://user:pwd@127.0.0.1:5432/privshield?sslmode=disable"
```

相关环境变量：
| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `PRIVSHIELD_MIGRATE_PG_DSN` | `""` | 迁移工具目标 PostgreSQL DSN |
| `PRIVSHIELD_MIGRATE_SNAPSHOT_VERIFY` | `skip` | 迁移快照验证模式 |

### 2.4 归档存证段运维

归档存证段是审计日志保留策略的物理载体，采用加密压缩 + 行哈希链的防篡改格式。

#### 归档段文件命名规范

```
audit-archive-<cutoff>-<seq>.ndjson.gz.enc
```

| 字段 | 格式 | 说明 |
|---|---|---|
| `<cutoff>` | `20060102T150405Z` | 归档截止时间（UTC） |
| `<seq>` | `%06d` (零填充 6 位) | 同截止时间自增序号，防覆盖（最大 999999） |

#### 清单文件 (Manifest)

每个归档段对应一个 JSON 清单文件：

```
audit-archive-<cutoff>-<seq>.manifest.json
```

清单结构：

| 字段 | 类型 | 说明 |
|---|---|---|
| `version` | string | 固定 `"privshield-audit-archive/v1"` |
| `chain_algo` | string | 哈希链算法标识 `"SM3-LINE-CHAIN:v1"` |
| `encryption` | string | 加密方式标识 `"SM4-GCM/HKDF-SM3(enc:v2)"` |
| `segment_file` | string | 对应的段文件名 |
| `created_at` | timestamp | 归档创建时间 |
| `cutoff` | timestamp | 归档截止时间 |
| `log_count` | int64 | 归档审计日志条数 |
| `snapshot_count` | int64 | 归档快照条数 |
| `first_log_id` | string | 首条日志 ID |
| `last_log_id` | string | 末条日志 ID |
| `first_timestamp` | timestamp | 首条日志时间戳 |
| `last_timestamp` | timestamp | 末条日志时间戳 |
| `chain_tail` | string | 行哈希链尾值（SM3 累积哈希） |

#### 段内容管线

1. 审计日志与关联快照序列化为 NDJSON 行（`kind: "log"` 或 `kind: "snapshot"`）；
2. 每行携带 `parameters_json` 字段（因 `store.AuditLog.ParametersJSON` 标记了 `json:"-"`）；
3. 行哈希链推进：`chain[i] = SM3(chain[i-1] || line[i])`；
4. Gzip 压缩；
5. SM4-GCM 信封加密（`enc:v2` 格式，HKDF-SM3 每段派生独立密钥）；
6. 原子写入 (`O_CREATE|O_EXCL|O_WRONLY` + `fsync`)；
7. **写后立即回读验证**：写入后对刚落盘的段执行 `VerifySegment` 全量校验，不通过则中止后续删除。

> [!WARNING]
> **路径穿越防护**：归档器通过 `resolveInDir` 拒绝含 `/`、`\`、`..` 的文件名，并验证解析后路径仍在归档目录内。

### 2.5 严格存储模式 (Strict Storage)

所有微服务默认启用严格存储模式 (`STRICT_STORAGE=true`)。

**行为规则**：
- 数据库连接失败时，服务**立即退出** (`log.Fatalf`)，不会静默回退到内存模式；
- 防止在生产环境中因数据库不可达而丢失数据或在不可靠存储上继续服务；
- 各服务可通过 `*_STRICT_STORAGE` 独立覆盖（如 `AUDIT_LOG_STRICT_STORAGE`、`SERVICE_HUB_STRICT_STORAGE` 等）；
- 全局 `STRICT_STORAGE` 作为各服务未设置时的级联回退默认值。

**排查**：若服务启动时因存储连接失败而立即退出，检查：
1. 数据库文件路径是否可写（SQLite）或 DSN 是否正确（PostgreSQL）；
2. 如确需在开发环境使用内存模式，显式设置 `*_STRICT_STORAGE=false`。

---

## 三、商用密码与 mTLS 证书安全运维

### 3.1 国密 SM4 信封加密与密钥管理

快照存证表中的 `input_sample` 与 `output_sample` 在落盘前自动通过 `AUDIT_LOG_ENCRYPTION_KEY` 进行 SM4-GCM 认证加密。归档段同样使用此密钥加密。

#### enc:v2 信封格式（当前写入格式）

```
enc:v2:<Base64( 16-byte salt + 12-byte nonce + SM4-GCM ciphertext + 16-byte auth tag )>
```

**加密流程**：
1. 拒绝空密钥（返回 `ErrEmptyKey`，无静默明文回退）；
2. 生成 16 字节随机 salt + 12 字节随机 nonce（`crypto/rand.Reader`）；
3. 通过 HKDF-Extract/Expand（HMAC-SM3）派生每记录独立的 16 字节 SM4 密钥：
   - Extract: `HMAC-SM3(salt, secret)`
   - Expand: 迭代 HMAC-SM3，info = `"PrivShield audit snapshot SM4-GCM v2"`
4. 使用 SM4-GCM 密封，AAD = `"enc:v2:"`（版本前缀绑定，防降级攻击）；
5. 输出 `enc:v2:<base64(salt || nonce || ciphertext || tag)>`。

**解密流程**：
- 按前缀自动分派：`enc:v2:` -> HKDF 密钥派生 + GCM 开启；`enc:v1:` -> SHA-256 密钥派生 + GCM 开启；
- 无 recognized 前缀的值返回 `ErrUnencryptedValue`（防止前缀剥离降级攻击）。

#### enc:v1 遗留格式（仅读取兼容）

```
enc:v1:<Base64( 12-byte nonce + SM4-GCM ciphertext + 16-byte auth tag )>
```

- v1 密钥派生：`SHA-256(secret)[:16]`（弱，仅保留向后兼容读取）。

#### 密钥生成

```bash
# 生成 256 位（32 字符）高强度随机主密钥
openssl rand -hex 16
```

#### 密钥注入

严禁将密钥明文写入配置文件或 Git 仓库，必须通过以下途径注入：
1. 政务云 KMS 托管分发；
2. Kubernetes Secret 挂载；
3. 专属环境变量 `AUDIT_LOG_ENCRYPTION_KEY`（回退 `PRIVACY_AUDIT_KEY`）。

#### 密钥轮转策略

**信封加密轮转**：
1. 旧数据保留 `enc:v2:...` 头部标记；
2. 更换 `AUDIT_LOG_ENCRYPTION_KEY` 后，新写入数据自动使用新密钥；
3. 历史数据仍可通过旧密钥解密（需保留旧密钥的离线归档备份）；
4. 批量重加密：通过离线脚本读取旧数据、用旧密钥解密、再用新密钥重新加密存盘。

**哈希链密钥轮转**：参见 [3.2 HMAC-SM3 哈希链密钥注入与轮转](#32-hmac-sm3-哈希链密钥注入与轮转)。

### 3.2 HMAC-SM3 哈希链密钥注入与轮转

审计日志的不可篡改完整性由 HMAC-SM3 哈希链保障。链密钥在进程启动时通过原子操作注入：

```go
store.SetAuditChainKey(cfg.HashKey)   // 原子写入 atomic.Pointer[string]
```

#### 哈希计算规则

**审计日志链推进**（9 元素前像）：
```
prev_hash | log_id | timestamp_utc | algorithm | input_hash | output_hash | user | security_level | params_json
```

- 时间戳归一化：`timestamp.UTC().Format(time.RFC3339Nano)`；
- 配置了链密钥：`HMAC-SM3(key, "SM3-HMAC:v1|" + payload)` -> 标签 `SM3-HMAC:v1`；
- 未配置链密钥：`SM3(payload)` -> 标签 `SM3`（仅向后兼容，生产必须配置密钥）。

**快照完整性哈希**（8 元素前像）：
```
snapshotID | auditLogID | prevHash | timestamp | algorithm | inputSample | outputSample | parametersJSON
```

- 配置了链密钥：`HMAC-SM3(key, "SM3-HMAC:v1-SNAPSHOT|" + payload)`；
- 未配置链密钥：`SM3(payload)`。

#### 多轨验证机制

`VerifyAuditIntegrityHash` 按以下优先级依次尝试匹配：
1. HMAC-SM3 (密钥模式，如已配置)
2. SM3-UTC
3. SHA-256-UTC
4. SM3-LocalTZ
5. SHA-256-LocalTZ

返回 `(bool, label)` 标识是否通过及匹配算法。

#### 链密钥轮转 (`pkg/store/cmd/repairchain`)

`repairchain` CLI 工具提供三种模式：

| 模式 | 说明 |
|---|---|
| `verify` (默认) | 只读扫描，分类每条记录为 `canonical` / `legacy` / `re-anchor` / `tampered`，拒绝修复篡改记录 |
| `repair` | 从首个断链点重锚 `prev_hash`，级联重算 `integrity_hash`，单事务完成，自动 `.bak` 备份 |
| `resign` | 注入新 `AUDIT_LOG_HASH_KEY`，将所有遗留记录从 SM3/SHA-256 升级到 `SM3-HMAC:v1`，从头重写审计日志与快照表，单事务完成 |

**安全不变量**：
- 篡改记录**永不**被重新签名（保留取证证据）；
- 写操作模式执行前自动创建 `.bak` 备份；
- 时间戳必须 RFC3339Nano 字节级往返一致；
- 写后验证确认所有记录状态为 `canonical`。

```bash
# 验证链完整性（只读）
AUDIT_LOG_HASH_KEY="your-key" go run pkg/store/cmd/repairchain/main.go -mode verify

# 升级到 HMAC 密钥模式
AUDIT_LOG_HASH_KEY="new-key" AUDIT_LOG_DB_PATH=/var/lib/privshield/audit.db \
  go run pkg/store/cmd/repairchain/main.go -mode resign
```

### 3.3 mTLS 客户端证书与 CN 白名单热加载

gRPC 跨域接入采用 mTLS 双向认证，通过 CN 白名单文件精细控制访问权限：

#### 白名单配置文件格式 (`whitelist.yaml`)：
```yaml
version: "1.0"
default_scopes: [] # fail-closed: 未知客户端默认拒绝
entries:
  - cn: "service-hub-prod-01"
    scopes: ["*"]
    enabled: true
    description: "调度中枢生产主节点"

  - cn: "audit-collector-node"
    scopes: ["audit:write", "audit:verify"]
    enabled: true
    description: "审计存证同步节点"
```

* **热加载机制**：`WhitelistManager` 在每次请求时通过文件 `mtime` 轮询检查。修改 `whitelist.yaml` 后，**无需重启服务**，秒级生效。

#### TLS 强制策略

所有服务的 TLS 配置强制 `MinVersion: tls.VersionTLS13`（禁止 TLS 1.2 及以下）。

支持公钥钉选 (SPKI Pinning)：通过 `VerifyPeerCertificate` 钩子深度比较 RSA (N, E)、ECDSA (X, Y, Curve) 或 Ed25519 (32-byte key) 公钥，确保证书链之外的额外身份绑定。

---

## 四、全栈监控大盘与 Prometheus 告警规则

### 4.1 业务指标清单 (`pkg/metrics.Collector`)

每个微服务模块持有独立 `prometheus.Registry` 的 `Collector` 实例，所有指标携带 `module` 常量标签。

#### HTTP 吞吐指标

| Prometheus 指标名 | 类型 | 标签 | Collector 方法 | 含义与阈值建议 |
|---|---|---|---|---|
| `http_requests_total` | Counter | `module`, `method`, `path`, `status` | `RecordHTTP(method, path, status, duration)` / `HTTPMiddleware()` | HTTP 请求总量（关注 5xx 占比 > 1%） |
| `http_request_duration_seconds` | Histogram | `module`, `method`, `path` | 同上 | 接口响应延迟（P99 应 < 100ms） |

#### 上游 Agent 调用指标

| Prometheus 指标名 | 类型 | 标签 | Collector 方法 | 含义 |
|---|---|---|---|---|
| `agent_requests_total` | Counter | `module`, `endpoint`, `status` | `RecordAgentCall(endpoint, status, duration)` | 上游 Agent 隐私引擎调用总量 |
| `agent_request_duration_seconds` | Histogram | `module`, `endpoint` | 同上 | 上游 Agent 调用延迟直方图 |

#### 可靠性与容灾指标

| Prometheus 指标名 | 类型 | 标签 | Collector 方法 | 含义 |
|---|---|---|---|---|
| `orphaned_tasks_recovered_total` | Counter | `module`, `type` (running/pending) | `RecordOrphanedRecovery(taskType)` | 崩溃重启后自动回收的孤儿任务数 |
| `tasks_retried_total` | Counter | `module`, `result` (queued/exhausted) | `RecordTaskRetry(result)` | 进入自动重试队列的任务数 |
| `circuit_breaker_state` | Gauge | `module`, `node` | `SetCircuitBreakerState(node, state)` | 上游节点熔断器状态 (0=closed, 1=open, 2=half_open) |

#### Phase B 租约与调度指标

| Prometheus 指标名 | 类型 | 标签 | Collector 方法 | 含义 |
|---|---|---|---|---|
| `task_lease_conflicts_total` | Counter | `module` | `RecordLeaseConflict()` | 租约所有权抢占冲突次数 |
| `task_lease_expired_total` | Counter | `module` | `RecordLeaseExpired(count)` | 租约超期回收事件总数 |
| `task_claim_latency_seconds` | Histogram | `module` | `RecordClaimLatency(durationSec)` | `FOR UPDATE SKIP LOCKED` 任务抢占延迟 |
| `task_transitions_total` | Counter | `module`, `from`, `to`, `result` | `RecordTaskTransition(from, to, result)` | 任务状态机流转次数 |
| `service_hub_ready` | Gauge | `module` | `SetReady(ready)` | 调度中枢就绪探针 (1=ready, 0=not) |

#### 命名路由与数据源指标

| Prometheus 指标名 | 类型 | 标签 | Collector 方法 | 含义 |
|---|---|---|---|---|
| `privshield_api_alias_requests_total` | Counter | `module`, `alias`, `canonical`, `target` | `RecordAPIAlias(alias, canonical, target)` | 使用非规范别名发起的请求数（target: `datasource_id` / `api_code` / `path`） |
| `privshield_datasource_normalize_errors_total` | Counter | `module`, `reason` | `RecordNormalizeError(reason)` | 标识归一化失败次数（reason: `unknown` / `empty` / `reserved` / `format_invalid`） |
| `privshield_datasource_requests_total` | Counter | `module`, `datasource_id`, `api_code`, `status` | `RecordDatasourceRequest(datasourceID, apiCode, status)` | 按规范数据源实体处理的请求总数 |

### 4.2 传输层 RED 指标 (`pkg/observability.REDMetrics`)

传输层通用 RED（Rate / Errors / Duration）指标，由 gRPC/HTTP 中间件自动埋点。

| Prometheus 指标名 | 类型 | 标签 | 含义 |
|---|---|---|---|
| `privshield_requests_total` | Counter | `protocol` (http/grpc), `endpoint`, `status` | 全协议请求总量 |
| `privshield_request_duration_seconds` | Histogram | `protocol`, `endpoint` | 全协议请求延迟直方图 |

自动埋点中间件：
- HTTP: `REDMetrics.PrometheusMiddleware()` (Gin 中间件)
- gRPC: `REDMetrics.UnaryServerInterceptor()` (Unary Server Interceptor)
- `/metrics` 端点: `REDMetrics.GinHandler()`

### 4.3 刷盘器运行时指标 (`pkg/store/flusher`)

刷盘器通过原子计数器暴露运行时状态（非 Prometheus 注册，通过诊断接口查询）：

| 方法 | 返回类型 | 含义 |
|---|---|---|
| `FlushedTotal()` | int64 | 成功刷盘写入的总条数 |
| `FailedTotal()` | int64 | 提交尝试耗尽所有重试后失败的总条数（正常应为 0） |
| `OverflowTotal()` | int64 | 队列持续满溢被拒绝的总条数（正常应为 0） |
| `EvictedTotal()` | int64 | 有界读缓存淘汰的暂存记录数 |
| `RetryPending()` | int64 | 工作线程未提交重试积压中的记录数 |
| `StagedCount()` | int | 当前内存暂存映射中的记录数 |
| `QueueDepth()` | int | 环形缓冲队列当前等待写入的记录数 |
| `HasFlushError()` | bool | 当前是否存在未恢复的刷盘错误 |
| `LastFlushError()` | string | 最近一次刷盘错误描述 |

### 4.4 命名路由观测指标 (`pkg/naming.Observer`)

`naming.Observer` 接口定义了命名规范层面的观测契约：

```go
type Observer interface {
    RecordAPIAlias(alias, canonical, target string)
    RecordNormalizeError(reason string)
}
```

`Collector` 编译期实现此接口 (`var _ naming.Observer = (*Collector)(nil)`)。服务通过 `naming.SetObserver(mc)` 注册后，`Normalize()` / `CheckWritable()` 等调用自动上报指标。未注册时为 no-op。

**标签取值规范**：
- `target`: `"datasource_id"` / `"api_code"` / `"path"`
- `reason`: `"unknown"` / `"empty"` / `"reserved"` / `"format_invalid"`

### 4.5 推荐 Prometheus 告警规则

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

      - alert: PrivShieldAuditFlusherOverflow
        expr: increase(audit_flusher_overflow_total[5m]) > 0
        for: 0m
        labels:
          severity: warning
        annotations:
          summary: "审计微批缓冲队列发生满溢拒绝"
          description: "模块 {{ $labels.module }} 在过去 5 分钟内发生了 {{ $value }} 条存证满溢拒绝，请检查存储写入延迟或调大 FLUSH_QUEUE_SIZE。"

      - alert: PrivShieldAuditFlusherFailed
        expr: increase(audit_flusher_failed_total[5m]) > 0
        for: 0m
        labels:
          severity: critical
        annotations:
          summary: "审计微批刷盘写入失败"
          description: "模块 {{ $labels.module }} 存在 {{ $value }} 条刷盘失败记录，底层存储可能不可用。"

      - alert: PrivShieldAgentCircuitBreakerOpen
        expr: circuit_breaker_state == 1
        for: 30s
        labels:
          severity: critical
        annotations:
          summary: "上游隐私计算 Agent 熔断器已触发"
          description: "目标 {{ $labels.node }} 连续请求失败超过阈值，已进入熔断阻断状态。"

      - alert: PrivShieldNormalizeErrorSpike
        expr: rate(privshield_datasource_normalize_errors_total[5m]) > 10
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "数据源标识归一化失败率异常升高"
          description: "模块 {{ $labels.module }} 归一化失败速率达到 {{ $value }}/s，请检查上游入参质量。"

      - alert: PrivShieldServiceHubNotReady
        expr: service_hub_ready == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "调度中枢就绪探针降级"
          description: "模块 {{ $labels.module }} 已持续 1 分钟处于未就绪状态。"
```

---

## 五、安全门控运维 (Security Gate Operations)

### 5.1 ValidateFailClosed 五条铁律

`pkg/config/security.go` 中的 `ValidateFailClosed(req SecurityRequirements)` 在服务启动时执行五项安全校验，**任一不通过则拒绝启动** (`log.Fatalf`)：

| # | 规则 | 触发条件 | 错误码 | 含义 |
|---|---|---|---|---|
| 1 | API Key 必填 | 监听地址含非回环地址（`0.0.0.0`、NIC IP）且 `APIKey == ""` | `ErrAPIKeyRequired` | 远程暴露必须配置认证密钥 |
| 2 | TLS 必填 | `RequireTLS == true` 但 `TLSEnabled == false` | `ErrTLSRequired` | 生产安全策略要求传输加密 |
| 3 | mTLS 白名单必填 | `TLSEnabled && GRPCEnabled` 但 `MTLSWhitelistFile == ""` | `ErrMTLSWhitelistRequired` | gRPC TLS 启用时必须配置 CN 白名单 |
| 4 | 加密密钥必填 | `RequireEncryptionKey == true` 且 `EncryptionKey == ""` 且非回环暴露 | `ErrEncryptionKeyRequired` | 远程暴露的审计服务必须配置快照加密 |
| 5 | 哈希链密钥必填 | `RequireHashKey == true` 且 `HashKey == ""` 且非回环暴露 | `ErrChainKeyRequired` | 远程暴露的审计服务必须配置链完整性密钥 |

**回环豁免**：仅绑定 `127.0.0.1` / `localhost` / `::1` / 空地址时，规则 1/4/5 允许无密钥启动（本地开发友好）。

### 5.2 常见启动拒绝场景排查

#### 场景 A：`ErrAPIKeyRequired` — API Key 为空

**现象**：服务启动日志出现 `API key is required for non-loopback bind`。

**排查**：
1. 检查监听地址是否配置为 `0.0.0.0` 或 NIC IP；
2. 若为本地开发，改为 `127.0.0.1` 或配置 `*_API_KEY`；
3. 若为生产部署，通过 K8s Secret 或 KMS 注入 API Key 环境变量。

#### 场景 B：`ErrTLSRequired` — TLS 要求但未启用

**现象**：服务启动日志出现 `TLS is required but not enabled`。

**排查**：
1. 确认 `*_REQUIRE_TLS=true` 已设置；
2. 确认 `*_TLS_ENABLED` 也为 `true`；
3. 检查证书文件路径是否正确且可读；
4. 确认 `tlsutil.BuildServerTLSConfig` 未返回错误（强制 TLS 1.3+）。

#### 场景 C：`ErrMTLSWhitelistRequired` — gRPC TLS 启用但无白名单

**现象**：服务启动日志出现 `mTLS whitelist file is required when gRPC TLS is enabled`。

**排查**：
1. 确认 `PRIVACY_AUTH_MTLS_WHITELIST_FILE` 已设置且指向有效的 YAML 文件；
2. 确认 YAML 文件格式正确（至少包含 `version` 和 `entries` 字段）；
3. 确认文件权限允许服务进程读取。

#### 场景 D：`ErrEncryptionKeyRequired` / `ErrChainKeyRequired`

**现象**：审计日志服务启动拒绝，日志提示加密密钥或链密钥未配置。

**排查**：
1. 非回环绑定时必须配置 `AUDIT_LOG_ENCRYPTION_KEY` 和 `AUDIT_LOG_HASH_KEY`；
2. 检查环境变量是否通过 K8s Secret 正确注入（注意 base64 编码/解码）；
3. 检查遗留回退变量名（`PRIVACY_AUDIT_KEY`）是否仍在使用。

#### 场景 E：`AUDIT_LOG_READER_API_KEY` 与 `AUDIT_LOG_API_KEY` 相同

**现象**：审计日志服务启动失败，提示读写 API Key 不能相同。

**排查**：
1. P1-6 职责分离要求：读取验证角色与写入角色必须使用不同密钥；
2. 为 `AUDIT_LOG_READER_API_KEY` 配置独立的只读验证密钥。

---

## 六、归档存证运维 (Archive Operations)

### 6.1 归档段格式规范

归档段将过期的审计日志与快照打包为不可篡改的加密文件，作为数据保留策略的物理存证。

**完整管线**：

```
审计日志 + 快照
    -> NDJSON 序列化 (kind: "log" | "snapshot", 显式携带 parameters_json)
    -> 行哈希链推进 (chain[i] = SM3(chain[i-1] || line[i]))
    -> Gzip 压缩
    -> SM4-GCM 信封加密 (enc:v2, HKDF-SM3 派生独立密钥)
    -> 原子写入 (O_CREATE|O_EXCL|O_WRONLY + fsync)
    -> 写后回读全量验证
```

**文件名格式**：
```
audit-archive-20260815T000000Z-000001.ndjson.gz.enc   # 数据段
audit-archive-20260815T000000Z-000001.manifest.json    # 清单文件
```

**防篡改机制**：
- 行哈希链：即使持有密钥的攻击者删除段中某一行，链尾校验也会失败；
- SM4-GCM 认证加密：无密钥者无法读取或伪造段内容；
- 写后验证：归档写入后立即回读验证，不通过则中止后续删除操作。

### 6.2 归档段完整性校验

使用 `VerifySegment` 对归档段执行全量完整性验证：

```bash
# 通过 repairchain 工具的 verify 模式验证链完整性
AUDIT_LOG_HASH_KEY="your-chain-key" \
AUDIT_LOG_ENCRYPTION_KEY="your-enc-key" \
  go run pkg/store/cmd/repairchain/main.go -mode verify
```

**校验流程**（`archive.VerifySegment`）：
1. 读取并解析清单文件，验证 `version == "privshield-audit-archive/v1"`；
2. 交叉验证 `manifest.SegmentFile == segment`（清单与段文件对应关系）；
3. 读取加密段文件，使用提供的密钥解密 (`crypto.DecryptString`)，Gzip 解压；
4. 逐行扫描 NDJSON 并重新计算行哈希链；
5. 对每条 `log` 类型行，调用 `store.VerifyAuditIntegrityHash` 校验 9 元素完整性哈希；
6. 将重算的链尾与 `manifest.ChainTail` 比对 -- 不匹配 = "evidence modified, truncated or reordered"；
7. 比对日志/快照计数与边界 ID / 时间戳是否与清单一致。

**校验结果判读**：
- 全部通过：段完整且未被篡改；
- 链尾不匹配：段内容被修改、截断或重排；
- 计数不匹配：段内容不完整；
- 解密失败：密钥不匹配或段文件损坏。

### 6.3 从归档恢复数据

归档段设计用于独立验证与取证存证保留。恢复流程需手动执行：

1. **解密**：使用 `crypto.DecryptString(sealed, key)` 解密 `.ndjson.gz.enc` 段文件；
2. **解压**：对解密后的字节流执行 Gzip 解压；
3. **解析**：逐行解析 NDJSON（每行为 JSON 对象，包含 `kind`、`log`/`snapshot`、`parameters_json` 字段）；
4. **重导入**：将解析出的记录通过 `store.AuditStore.SaveLogs` 重新写入目标存储。

> [!IMPORTANT]
> 恢复操作不自动执行链重签。恢复后的记录需要通过 `repairchain` 工具的 `repair` 模式重建链完整性。

### 6.4 保留策略配置

保留策略通过 `AUDIT_LOG_RETENTION_DAYS` 控制：

| 配置值 | 行为 | 约束 |
|---|---|---|
| `0` (默认) | **永不物理删除**审计日志 | 符合《数据安全法》三年存证要求 |
| `>= 1095` | 每 6 小时执行归档清理 | 必须同时配置 `AUDIT_LOG_ARCHIVE_DIR` 和 `AUDIT_LOG_ENCRYPTION_KEY` |
| `1 ~ 1094` | **拒绝启动** | 低于 3 年法定最低保留期限 |

**归档清理流程**（每 6 小时执行）：
1. 计算截止时间 `cutoff = now - RetentionDays`；
2. 创建 `Archiver` 并调用 `ArchiveAndCleanup`；
3. 循环拉取最早过期记录（分页），每页写入一个归档段；
4. 每段写入后立即执行 `VerifySegment` 全量回读验证；
5. **验证失败则立即中止**，不执行任何删除（fail-closed：存证永不静默丢失）；
6. 验证通过后按精确 ID 删除已归档记录；
7. 删除返回 0 条则中止（防止误删）。

---

## 七、核心故障排查手册 (Runbook)

### 7.1 存证哈希链验真失败 (`POST /api/audit/chain/verify` 返回 `valid: false`)

* **现象**：调用核验接口返回 `broken_at_id: "log-xxx"`, `expected_hash != actual_hash`。
* **排查流程**：
  1. 检查 `broken_at_id` 记录的 `timestamp` 是否与前序记录颠倒（时钟跳变）；
  2. 检查数据库是否存在手动 `UPDATE` 或 `DELETE` 审计日志的操作；
  3. 检查是否有未经 `BufferedAuditStore` 的直接并发 SQL 插入绕过了单 Worker 哈希绑定；
  4. 检查是否存在时区配置未归一（已全面采用 `timestamp.UTC().Format(time.RFC3339Nano)` 标准格式）；
  5. 检查 `AUDIT_LOG_HASH_KEY` 是否在写入与验证之间发生过变更（换密钥后旧记录需 `resign`）。
* **修复工具**：
  ```bash
  # 只读诊断
  AUDIT_LOG_HASH_KEY="key" AUDIT_LOG_DB_PATH="path" \
    go run pkg/store/cmd/repairchain/main.go -mode verify
  # 重锚断链
  AUDIT_LOG_HASH_KEY="key" AUDIT_LOG_DB_PATH="path" \
    go run pkg/store/cmd/repairchain/main.go -mode repair
  ```

### 7.2 SQLite 锁等待超时 (`database is locked`)

* **现象**：高并发写入时日志中出现 `database is locked`。
* **排查流程**：
  1. 确认是否已装配 `flusher.BufferedAuditStore`（未装配时单条落盘易锁库）；
  2. 确认 SQLite 连接池最大打开数是否合理（SQLite 写入为单进程排他锁，`MaxOpenConns` 不宜超过 4）；
  3. 确认磁盘 I/O 状态（`iostat -x 1`），若磁盘队列过高，考虑将数据目录挂载至高速 NVMe SSD；
  4. 业务 QPS 持续超过 1,000 时，配置 `PG_DSN` 平滑迁移至 PostgreSQL Phase B。

### 7.3 敏感样本解密失败 (`failed to decrypt sample`)

* **现象**：调用快照查询接口返回密文或解密错误。
* **排查流程**：
  1. 检查当前实例的 `AUDIT_LOG_ENCRYPTION_KEY` 环境变量是否与数据写入时的密钥一致；
  2. 检查密文字符串前缀：`enc:v2:` 为当前格式，`enc:v1:` 为遗留格式；
  3. 检查 Base64 字符串是否在传输中被截断或转义损坏；
  4. 检查数据库字符集是否为 UTF-8；
  5. 如存在密钥轮转历史，确认旧密钥的离线备份是否完好。

### 7.4 归档写入失败

* **现象**：归档清理任务日志出现 `archive write failed`，审计日志未被删除。
* **排查流程**：
  1. 检查 `AUDIT_LOG_ARCHIVE_DIR` 目录是否存在且可写；
  2. 检查磁盘空间是否充足（归档段含压缩+加密，但仍需足够空间）；
  3. 检查 `AUDIT_LOG_ENCRYPTION_KEY` 是否配置（保留删除模式下必填）；
  4. 检查是否有同名文件冲突（序号自动递增至 999999）；
  5. **注意**：归档失败不会导致数据丢失 -- 删除操作被中止，审计日志保留在原库。
* **恢复**：修复磁盘空间或目录权限后，下一个 6 小时清理周期将自动重试。

### 7.5 哈希链密钥不匹配 (`chain key mismatch`)

* **现象**：`VerifySegment` 或 `VerifyAuditIntegrityHash` 返回验证失败，日志提示密钥不匹配。
* **排查流程**：
  1. 确认当前 `AUDIT_LOG_HASH_KEY` 是否与记录写入时的密钥一致；
  2. 检查记录算法标签：`SM3-HMAC:v1` 为密钥模式，`SM3` 为无密钥模式；
  3. 如刚执行过密钥轮转，确认是否需要 `resign` 模式升级遗留记录；
  4. 多轨验证机制会自动尝试多种算法匹配，若所有模式均失败则为真正的篡改或密钥丢失。
* **修复**：
  ```bash
  # 使用新密钥重签所有遗留记录
  AUDIT_LOG_HASH_KEY="new-key" AUDIT_LOG_DB_PATH="path" \
    go run pkg/store/cmd/repairchain/main.go -mode resign
  ```

### 7.6 严格存储模式拒绝启动 (`strict storage: connection failed`)

* **现象**：服务启动后立即退出，日志显示存储连接失败且严格存储模式阻止回退。
* **排查流程**：
  1. SQLite 模式：检查 `*_DB_PATH` 目录权限与磁盘空间；
  2. PostgreSQL 模式：检查 `*_PG_DSN` 连接串、网络连通性与数据库账户权限；
  3. 若启用了 `AUDIT_LOG_DB_WRITE_ONLY=true`，确认数据库账户确实无 UPDATE/DELETE 权限（这是预期行为，自检通过才是正确状态）；
  4. 开发环境可临时设置 `*_STRICT_STORAGE=false` 允许回退内存模式。

### 7.7 归档段完整性校验失败

* **现象**：`VerifySegment` 返回错误：`evidence modified, truncated or reordered`。
* **排查流程**：
  1. 确认段文件是否被外部进程修改、截断或重命名；
  2. 确认解密使用的 `AUDIT_LOG_ENCRYPTION_KEY` 是否与归档时的密钥一致；
  3. 检查清单文件 `.manifest.json` 是否与段文件匹配（`segment_file` 字段）；
  4. 如确认段文件物理损坏，从备份恢复原始 `.ndjson.gz.enc` 文件。
* **注意**：归档段一旦校验失败，**不得**删除对应的数据库记录（如已删除则需从备份恢复）。
