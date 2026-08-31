# 数据服务调度中枢 — 产品需求文档 (PRD)

## 1. 产品概述

**数据服务调度中枢**（Service Hub）是 PrivShield 平台的企业级数据流通中枢微服务，部署于**主机甲（业务网关算力节点 · ECS）**，负责统一接入上游调用（React Web 控制台、Go BFF、外部业务系统），并将数据治理请求编排为 **6 阶段安全流水线**（`ingest` ➔ `fetch` ➔ `classify` ➔ `desensitize` ➔ `return` ➔ `audit`），协同数据源管理（`datasource-mgr`）、隐私计算引擎（`PrivShield Agent`）与不可篡改审计存证（`audit-log`）。

| 属性 | 值 | 说明 |
|---|---|---|
| 模块名称 | `service-hub` | 数据流通调度中枢 |
| HTTP REST 端口 | `8082` | 默认监听地址 `127.0.0.1:8082`（面向 Web 控制台与 BFF） |
| gRPC 端口 | `50052` | 默认监听地址 `127.0.0.1:50052`（支持国密 SM2 / TLS 1.3 mTLS 与 CN 白名单） |
| 开发语言与框架 | Go 1.24+ / Gin / gRPC | 原生协程并发、强类型、高吞吐 |
| 上游依赖 | PrivShield Agent (`:8079` REST) | 3 层动态分类漏斗与脱敏隐私原语 |
| 下游数据源依赖 | datasource-mgr (`:8083` REST / `:50053` gRPC) | 医保 (`ds_yibao` 18字段) / 康养 (`ds_kangyang` 27字段) 等仿真模拟数据源 |
| 下游审计依赖 | audit-log (`:8084` REST / `:50054` gRPC) | 国密 SM3 区块链式防篡改存证与 SM4-GCM 快照 |
| 存储引擎 | PostgreSQL Phase B (多副本原子租约) / SQLite WAL (自愈降级) | 任务持久化、自适应连接池与崩溃恢复 |
| 行业标准 | 四川省健康医疗大数据应用指南 DB51/T 2989—2023 | L1~L5 五级分级、6 类字段矩阵与四柱强剥离 |

---

## 2. 核心业务需求

### 2.1 六阶段安全调度流水线

每个进入调度中枢的数据处理任务按严格顺序经过 6 个状态追踪标签：

```text
① ingest (接入) ──▶ ② fetch (取数) ──▶ ③ classify（分类与脱敏处理） ──▶ ④ desensitize（状态追踪） ──▶ ⑤ return (返回) ──▶ ⑥ audit（状态追踪） ──▶ done
```

| 阶段 | 标识 | 说明 | 协同模块与动作 |
|---|---|---|---|
| ① | `ingest` | 接收请求，参数校验，生成唯一 `task_id`，落库 `pending` 状态 | 快速校验与入队，立即响应 `202 Accepted` |
| ② | `fetch` | 申请并抽取原始数据 | 若请求未显式携带 Payload，自动调用 `datasource-mgr` 采样 |
| ③ | `classify` | 分类与脱敏一体化处理 | 一次调用 Agent `POST /v1/agent/process`（404 时兼容 `POST /v1/medical/process`） |
| ④ | `desensitize` | 状态追踪 | 快速流转 |
| ⑤ | `return` | 状态追踪 | 快速流转 |
| ⑥ | `audit` | 状态追踪 | 记录审计存证并写为 `completed/done` |

### 2.2 敏感度等级到脱敏策略自动映射 (DB51/T 2989—2023)

| 安全等级 | 业务敏感定义 | 自动分发脱敏算子 | 执行优先级 | 策略说明 |
|---|---|---|---|---|
| **L1 (公开)** | 公开数据 / 机构代码 | `none` | low (0) | 无需脱敏，直接放行流通 |
| **L2 (内部)** | 姓名 / 电话 / 身份证号 | `mask` | normal (20) | 字段级动态国密 SM3 掩码与哈希打码 |
| **L3 (敏感)** | 年龄 / 邮编 / 准标识符集合 | `k_anon` | high (50) | K-匿名化区间泛化与微聚合 |
| **L4 (高敏)** | 诊疗金额 / 体征数值 / 主诉病史 | `dp` | critical (80) | 差分隐私加噪与四柱特征剥离 |
| **L5 (极敏)** | 传染病 / HIV / 绝密特种诊断 | `qol` / purge | critical (100) | 查询混淆或整块整句彻底抹平 |

### 2.3 多副本租约与持久化高可用

- **PostgreSQL Phase B 原子租约**：基于 `FOR UPDATE SKIP LOCKED` 短事务实现多副本无阻塞争抢任务租约（`ClaimNext`）与令牌续期（`RenewLease`），彻底杜绝分布式死锁与脑裂。
- **自适应连接池调优**：根据 `runtime.NumCPU()` 动态调优连接池大小。
- **自动探针降级**：配置 `SERVICE_HUB_PG_DSN` 时以 3s 超时探测连接；失败自动平滑回退至 SQLite WAL 模式。

---

## 3. 接口需求

### 3.1 HTTP REST 接口清单

| 方法 | 路径 | 鉴权要求 | 说明 |
|---|---|---|---|
| GET | `/health` | 免密 | 存活探针（Liveness Probe，进程存活即返回 200） |
| GET | `/readyz` | 免密 | 就绪探针（Readiness Probe，检查 Agent+Datasource 依赖，失败返回 503） |
| GET | `/api/health` | 免密 | 存活探针兼容别名，返回自身状态与模块标识 |
| GET | `/api/hub/status` | 可选 API Key | 调度中枢运行状态（Uptime、排队数、活跃任务数、成功/失败总量） |
| GET | `/api/hub/tasks` | 可选 API Key | 分页查询任务列表（支持 `?status=` 过滤与 `limit`/`offset` 参数） |
| GET | `/api/hub/tasks/:id` | 可选 API Key | 查询单个任务详情（包含流水线阶段、耗时与错误信息） |
| POST | `/api/hub/dispatch` | 可选 API Key | 手动提交指定算子的隐私调度任务（返回 202 Accepted） |
| GET | `/api/hub/pipeline` | 可选 API Key | 获取 6 阶段流水线活跃状态与 Agent 连通性 |
| GET | `/metrics` | 免密 | Prometheus 格式指标导出端点 |

### 3.2 gRPC 服务接口清单 (`servicehub.ServiceHubService`)

| RPC 方法 | 入参 Request | 出参 Response | 说明 |
|---|---|---|---|
| `Health` | `HealthRequest` | `HealthResponse` | gRPC 探针：自检 + 上游 Agent 连通性 |
| `HubStatus` | `HubStatusRequest` | `HubStatusResponse` | 调度中枢状态与队列深度 |
| `Dispatch` | `DispatchRequest` | `DispatchResponse` | 高性能提交任务到流水线 |
| `ClassifyAndDispatch` | `ClassifyAndDispatchRequest` | `ClassifyAndDispatchResponse` | 分类分级并自动分发策略 |
| `GetTask` | `GetTaskRequest` | `TaskProto` | 任务详情查询 |
| `ListTasks` | `ListTasksRequest` | `ListTasksResponse` | 任务列表查询（支持状态过滤） |
| `PipelineStatus` | `PipelineStatusRequest` | `PipelineStatusResponse` | 流水线阶段活跃监控 |

---

## 4. 运行配置与环境变量需求

| 环境变量 | 默认值 | 类型 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_HOST` | `127.0.0.1` | string | HTTP REST 监听主机地址（生产通常设为 `0.0.0.0`） |
| `SERVICE_HUB_PORT` | `8082` | int | HTTP REST 服务端口 |
| `SERVICE_HUB_GRPC_HOST` | `127.0.0.1` | string | gRPC 服务监听主机地址 |
| `SERVICE_HUB_GRPC_PORT` | `50052` | int | gRPC 服务端口 |
| `SERVICE_HUB_PG_DSN` | `""` | string | PostgreSQL 连接串（启用多副本租约模式，带 3s 探针自动降级） |
| `SERVICE_HUB_DB_PATH` | `""` | string | SQLite 数据库文件路径（空表示纯内存模式） |
| `SERVICE_HUB_TLS_ENABLED` | `false` | bool | 是否开启 gRPC TLS 1.3 / 国密 SM2 mTLS |
| `SERVICE_HUB_TLS_ALLOWED_CNS` | `""` | string | 允许调用的客户端证书 CN 白名单（逗号分隔） |
| `PRIVACY_AGENT_REST_HOST` | `127.0.0.1` | string | 上游 PrivShield Agent REST 主机地址 |
| `PRIVACY_REST_PORT` | `8079` | int | 上游 PrivShield Agent REST 端口 |
| `SERVICE_HUB_RETENTION_DAYS` | `90` | int | 终态任务数据保留清理天数 |
