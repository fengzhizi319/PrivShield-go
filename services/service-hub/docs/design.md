# 数据服务调度中枢 (Service Hub) — 详细设计文档

> 本文档定义 **数联天下 · 数盾 (`PrivShield`)** 数据服务调度中枢模块（`services/service-hub`）的系统架构、六阶段流水线调度、模拟数据源联动对接、多副本 PostgreSQL 租约并发、数据持久化与高可用设计。

---

## 1. 背景与业务定位

在政务云数据安全架构中，**数联数据服务调度中枢 (Service Hub)** 部署于**主机甲（业务网关算力节点 · ECS）**，是数据流通链路的核心枢纽与调度中枢，负责：

1. **统一接入与协商**：统一接收来自各调用方的数据申请请求与协商凭证；
2. **数据源跨服务联动**：对接 `services/datasource-mgr`，按需调取医保（`ds_yibao` 19字段）、康养（`ds_kangyang` 27字段）及预留数据源进行高保真仿真调度；
3. **六阶段流水线编排**：按「请求接入 → 申请原数 → 分类与脱敏一体化处理 → 返回结果 → 完成」编排任务；状态机保留 `ingest`、`fetch`、`classify`、`desensitize`、`return`、`audit` 六个追踪标签；
4. **分类分级智能联动 (DB51/T 2989—2023)**：接入 Layer-1~3 分类分级漏斗，根据动态评估得出的数据敏感度（L1~L5）自动决策并下发最适隐私原语（明文/字段脱敏/K-匿名/差分隐私/查询混淆）；
5. **双协议服务暴露**：同时提供面向 Web 前端与管控端的 HTTP REST API (:8082)，以及面向高性能微服务互通的双向 mTLS / CN 白名单 gRPC 服务 (:50052)；
6. **多副本租约与生产级持久化**：
   * 支持 **PostgreSQL Phase B 存储底座**：基于 `FOR UPDATE SKIP LOCKED` 短事务实现多副本无阻塞争抢任务租约（`ClaimNext`）与令牌续期（`RenewLease`），彻底杜绝分布式死锁与脑裂重复消费；
   * 支持 **自适应连接池调优**：根据 `runtime.NumCPU()` 动态调优连接池大小；
   * 支持 **自动连通性探针回退 (Self-healing Fallback)**：配置 PG_DSN 探针超时（>3s）自动回退至 SQLite WAL 模式；
7. **崩溃恢复与自动重试**：启动时自动回收孤立任务（running 标记失败、pending 保留队列），周期性后台重试失败任务（指数退避 + RetryCount）；
8. **安全审计存证对接**：异步向主机乙（独立安全审计节点 · ECS）的 `audit-log` 提交国密 SM3 区块链式防篡改存证与 SM4-GCM 快照。

---

## 2. 总体架构拓扑

```mermaid
graph TD
    subgraph Clients [客户端层]
        Web[React 控制台 UI<br/>console/web :8000 / :5173]
        AppLZ[调度之眼 App-LZ<br/>console/app-lz/web :5174]
        Gateway[API Gateway / Go BFF<br/>console/bff-go :8081 / :8085]
        ExtRPC[外部调度客户端<br/>gRPC mTLS :50052]
    end

    subgraph ServiceHub [Service Hub 调度中枢 :8082 / :50052 (主机甲 · ECS)]
        HTTPHandler[HTTP REST 路由层<br/>/api/hub/* :8082]
        GRPCHandler[gRPC 服务层<br/>ServiceHubServiceServer :50052]
        MiddlewareStack[9层中间件链<br/>Auth / CORS / Logger / TraceID / Recovery / RateLimit]
        MetricsCol[Prometheus Collector<br/>/metrics]
        
        Orchestrator[调度编排引擎<br/>Pipeline Orchestrator]
        Semaphore[并发信号量<br/>max: 10 active tasks]
        TaskStore[(LeasedTaskStore 引擎<br/>PostgreSQL Phase B / SQLite WAL)]
        DatasourceClient[数据源客户端<br/>internal/datasource]
    end

    subgraph MockDatasource [模拟数据源 :8083 / :50053 (主机甲 · ECS)]
        DSMgr[datasource-mgr<br/>ds_yibao / ds_kangyang / mock3 / mock4]
    end

    subgraph UpstreamAgent [PrivShield 核心 Agent :8079 (主机甲 · ECS)]
        DynClassify[动态分类分级引擎<br/>Rule → NER → LLM]
        MaskEngine[脱敏/隐私原语引擎<br/>Masking / K-Anon / DP / QOL]
    end

    subgraph AuditLogService [审计存证 :8084 / :50054 (主机乙 · ECS)]
        AuditLog[audit-log<br/>国密 SM3 存证与 SM4-GCM 快照]
    end

    Web -->|HTTP/JSON| HTTPHandler
    AppLZ -->|HTTP/JSON| HTTPHandler
    Gateway -->|HTTP/JSON| HTTPHandler
    ExtRPC -->|gRPC/mTLS| GRPCHandler

    HTTPHandler --> MiddlewareStack
    GRPCHandler --> Orchestrator
    MiddlewareStack --> Orchestrator

    Orchestrator --> Semaphore
    Semaphore --> TaskStore
    Orchestrator --> DatasourceClient
    DatasourceClient -->|HTTP REST / gRPC| DSMgr
    Orchestrator -->|HTTP REST| UpstreamAgent
    Orchestrator -->|HTTP REST / gRPC| AuditLog
    HTTPHandler --> MetricsCol
```

---

## 3. 六阶段调度流水线与数据源联动

调度中枢将每一个数据治理请求抽象为 6 个有序阶段：

```text
① ingest (接入) ──▶ ② fetch (取数) ──▶ ③ classify（分类与脱敏一体化处理） ──▶ ④ desensitize（状态追踪） ──▶ ⑤ return (返回) ──▶ ⑥ audit（状态追踪） ──▶ done
```

| 阶段 | 标识 | 执行动作 | 协同模块与机制 |
|---|---|---|---|
| **1. 接入** | `ingest` | 任务已由 HTTP `Dispatch` 或 gRPC `Dispatch` 创建为 `pending/queued`；流水线写入 `running/ingest` | 参数不合法时请求入口立即拒绝 |
| **2. 取数** | `fetch` | 若请求未显式携带 Payload，自动调用 `datasource-mgr` 根据数据源标识（如 `ds_yibao`）抓取高保真样本 | `internal/datasource/client.go` |
| **3. 分类与脱敏** | `classify` | 一次调用 engine `POST /v1/agent/process`（404 时兼容 `POST /v1/medical/process`），一体化完成分类分级与脱敏处理 | `internal/agent.Client.ProcessAgent()` |
| **4. 状态追踪** | `desensitize` | 不执行独立脱敏动作；已在 `classify` 的一体化调用中完成 | 状态机快速流转 |
| **5. 返回** | `return` | 当前为状态追踪阶段，不写入额外结果对象 | 状态机快速流转 |
| **6. 完成前追踪** | `audit` | 触发审计状态流转，随后写为 `completed/done` | 状态机快速流转 |

---

## 4. 敏感度等级与脱敏策略自动映射 (DB51/T 2989—2023)

```mermaid
graph LR
    Input[原始数据] --> Funnel[Agent 三层分类漏斗]
    Funnel -->|L1 公开| OpNone[无脱敏直接流通 (none)]
    Funnel -->|L2 内部| OpMask[字段级国密 SM3/正则打码 (mask)]
    Funnel -->|L3 敏感| OpKAnon[K-匿名化泛化 (k_anon)]
    Funnel -->|L4 高敏| OpDP[差分隐私加噪与四柱剥离 (dp)]
    Funnel -->|L5 极敏| OpQOL[查询混淆与整块抹平 (qol)]
```
