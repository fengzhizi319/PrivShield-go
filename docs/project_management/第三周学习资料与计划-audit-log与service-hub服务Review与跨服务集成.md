# 第三周学习资料与计划：audit-log 与 service-hub 服务 Review + 跨服务集成

> **周目标**：审查上层服务如何正确使用 `pkg` 层基础设施（密码学、存储、刷盘器），验证 audit-log 与 service-hub 的跨服务集成正确性与一致性。
>
> **审查范围**：`services/audit-log/`（~3,000 行）、`services/service-hub/`（~5,300 行）、`console/app-lz/bff-go/`（~1,500 行）、`services/datasource-mgr/`（对接侧）。
>
> **代码总量**：约 50 个 Go 源文件，~10,000 行实现代码 + ~4,500 行测试代码。

---

## 目录

- [第 1 章：前置知识准备](#第-1-章前置知识准备)
  - [P0：微服务架构模式与服务分级](#p0微服务架构模式与服务分级)
  - [P1：6 阶段数据流通调度流水线](#p16-阶段数据流通调度流水线)
  - [P2：租约任务存储与 FOR UPDATE SKIP LOCKED](#p2租约任务存储与-for-update-skip-locked)
  - [P3：先归档后删除的留存红线](#p3先归档后删除的留存红线)
  - [P4：错误分类与结构化重试](#p4错误分类与结构化重试)
  - [P5：BFF 聚合代理模式](#p5bff-聚合代理模式)
- [第 2 章：Day 1-2 audit-log 审计日志服务精读](#第-2-章day-1-2-audit-log-审计日志服务精读)
  - [2.1 审查文件清单](#21-审查文件清单)
  - [2.2 服务启动流程与依赖装配](#22-服务启动流程与依赖装配)
  - [2.3 存储后端初始化与分层降级](#23-存储后端初始化与分层降级)
  - [2.4 REST API 路由与权限模型](#24-rest-api-路由与权限模型)
  - [2.5 gRPC 服务实现与拦截器链](#25-grpc-服务实现与拦截器链)
  - [2.6 归档留存红线 archive.go 深度走读](#26-归档留存红线-archivego-深度走读)
  - [2.7 留存清理协程 auditRetentionLoop](#27-留存清理协程-auditretentionloop)
  - [2.8 Agent 客户端封装](#28-agent-客户端封装)
  - [2.9 配置体系与环境变量](#29-配置体系与环境变量)
- [第 3 章：Day 3-4 service-hub 服务调度中枢精读](#第-3-章day-3-4-service-hub-服务调度中枢精读)
  - [3.1 审查文件清单](#31-审查文件清单)
  - [3.2 服务启动流程与双协议监听](#32-服务启动流程与双协议监听)
  - [3.3 6 阶段流水线调度状态机](#336-阶段流水线调度状态机)
  - [3.4 算子强度单调不减约束（P1-1）](#34-算子强度单调不减约束p1-1)
  - [3.5 存证客户端 audit/client.go 深度走读](#35-存证客户端-auditclientgo-深度走读)
  - [3.6 数据源客户端 datasource/client.go 深度走读](#36-数据源客户端-datasourceclientgo-深度走读)
  - [3.7 错误分类与结构化重试 retry/retry.go](#37-错误分类与结构化重试-retryretrygo)
  - [3.8 崩溃恢复与租约过期回收](#38-崩溃恢复与租约过期回收)
  - [3.9 gRPC TypedServer 统一分发](#39-grpc-typedserver-统一分发)
- [第 4 章：Day 5 跨服务集成与端到端联调](#第-4-章day-5-跨服务集成与端到端联调)
  - [4.1 全栈服务拓扑与启动顺序](#41-全栈服务拓扑与启动顺序)
  - [4.2 核心链路端到端验证](#42-核心链路端到端验证)
  - [4.3 app-lz BFF 聚合代理层走读](#43-app-lz-bff-聚合代理层走读)
  - [4.4 统一错误码信封与错误传播](#44-统一错误码信封与错误传播)
  - [4.5 超时传播与上下文链路](#45-超时传播与上下文链路)
  - [4.6 命名归一化与词表一致性（P1-5）](#46-命名归一化与词表一致性p1-5)
- [第 5 章：audit-log 核心数据模型与接口分析](#第-5-章audit-log-核心数据模型与接口分析)
- [第 6 章：service-hub 核心数据模型与接口分析](#第-6-章service-hub-核心数据模型与接口分析)
- [第 7 章：audit-log 归档段格式与验真算法深度走读](#第-7-章audit-log-归档段格式与验真算法深度走读)
  - [7.1 归档段文件命名与防覆盖策略](#71-归档段文件命名与防覆盖策略)
  - [7.2 NDJSON 行哈希链构造](#72-ndjson-行哈希链构造)
  - [7.3 SM4-GCM 加密落盘与 fsync 持久化](#73-sm4-gcm-加密落盘与-fsync-持久化)
  - [7.4 VerifySegment 独立验真全流程](#74-verifysegment-独立验真全流程)
  - [7.5 路径穿越防护与安全文件名校验](#75-路径穿越防护与安全文件名校验)
- [第 8 章：service-hub 流水线 processTask 完整执行路径走读](#第-8-章service-hub-流水线-processtask-完整执行路径走读)
  - [8.1 ingest → fetch → classify 阶段](#81-ingest--fetch--classify-阶段)
  - [8.2 desensitize 阶段：Agent 调用与指纹回传](#82-desensitize-阶段agent-调用与指纹回传)
  - [8.3 return 阶段：结果返回与脱敏快照](#83-return-阶段结果返回与脱敏快照)
  - [8.4 audit 阶段：存证强绑定与证据链锚定](#84-audit-阶段存证强绑定与证据链锚定)
  - [8.5 信号量并发控制与优雅停机](#85-信号量并发控制与优雅停机)
- [第 9 章：app-lz BFF 层聚合代理深度走读](#第-9-章app-lz-bff-层聚合代理深度走读)
  - [9.1 四上游服务客户端注册](#91-四上游服务客户端注册)
  - [9.2 请求路由与转发映射](#92-请求路由与转发映射)
  - [9.3 认证与会话管理（JWT + TOTP）](#93-认证与会话管理jwt--totp)
  - [9.4 上游健康检查聚合](#94-上游健康检查聚合)
- [第 10 章：代码走读指南](#第-10-章代码走读指南)
- [第 11 章：常见问题与排查指南](#第-11-章常见问题与排查指南)
- [第 12 章：术语表](#第-12-章术语表)
- [第 13 章：Review 检查清单详细版](#第-13-章review-检查清单详细版)
- [第 14 章：周交付物清单](#第-14-章周交付物清单)
- [附录 A：关键环境变量速查表](#附录-a关键环境变量速查表)
- [附录 B：服务间调用关系全景图](#附录-b服务间调用关系全景图)
- [附录 C：REST API 路由清单](#附录-crest-api-路由清单)
- [附录 D：推荐阅读与延伸阅读](#附录-d推荐阅读与延伸阅读)

---

## 第 1 章：前置知识准备

在开始 Review 之前，需要掌握以下微服务架构与分布式系统基础概念。这些知识是理解 audit-log 与 service-hub 服务代码的前提。

### P0：微服务架构模式与服务分级

本项目采用 **Monorepo 多层解耦** 架构，按商业价值与可靠性要求分为两级：

| 服务分级 | 服务 | 标准 | 说明 |
|---|---|---|---|
| 商业应用版 | engine-go、audit-log、service-hub | 生产级 | 密码学正确、可观测性完整、优雅停机、安全加固 |
| 辅助测试版 | datasource-mgr、console/app-lz | 基本可用 | 功能验证为主，不做生产级加固 |

**服务间通信模式**：

```
                    ┌────────────────────────┐
                    │  React Web UI / BFF-Go │
                    └───────────┬────────────┘
                                │ HTTP/JSON
                    ┌───────────▼────────────┐
                    │  service-hub (:8082)    │ ──── 调度中枢
                    │  + gRPC (:50052)        │
                    └──┬──────────┬──────────┘
                       │          │
              HTTP/REST│          │HTTP/REST
                       │          │
            ┌──────────▼──┐  ┌───▼──────────┐
            │ engine-go    │  │ audit-log     │
            │ (:8079)      │  │ (:8084)       │
            │ +gRPC:50051  │  │ +gRPC:50054   │
            └─────────────┘  └───────────────┘
                       │
            ┌──────────▼──────────┐
            │ datasource-mgr      │
            │ (:8083)             │
            │ +gRPC:50053         │
            └─────────────────────┘
```

**关键设计原则**：
- **REST + gRPC 双协议**：每个商业服务同时暴露 REST（Gin，便于调试与前端直连）和 gRPC（低延迟、强类型、mTLS 支持）
- **pkg 层共享基础设施**：密码学（`pkg/crypto`）、存储（`pkg/store`）、中间件（`pkg/middleware`）、认证（`pkg/auth`）统一由 pkg 层提供，服务层只负责业务编排
- **统一错误信封**：所有 REST 错误响应通过 `pkg/middleware.AbortWithError` 输出标准 5 字段信封（status / error / message / path / timestamp）

### P1：6 阶段数据流通调度流水线

service-hub 的核心是 **6 阶段数据流通与安全治理调度流水线**：

```
① ingest ──▶ ② fetch ──▶ ③ classify ──▶ ④ desensitize ──▶ ⑤ return ──▶ ⑥ audit/done
   │             │             │                │                │             │
   ▼             ▼             ▼                ▼                ▼             ▼
 请求接入    申请原数     分类分级         下发脱敏         结果返回      审计存证
```

| 阶段 | 职责 | 调用目标 |
|---|---|---|
| ① ingest | 接收请求、创建任务记录、分配 TaskID | 本地 TaskStore |
| ② fetch | 从 datasource-mgr 获取原始数据 | datasource-mgr REST/gRPC |
| ③ classify | 调用 engine-go 动态分类分级 | engine-go `/v1/dynclassification` |
| ④ desensitize | 调用 engine-go 执行隐私算子 | engine-go `/v1/privacy` |
| ⑤ return | 返回脱敏结果、保存快照 | 本地 TaskStore + AuditStore |
| ⑥ audit | 向 audit-log 提交存证记录 | audit-log REST `/api/audit/logs` |

**关键约束**：
- **P0-6 出域 ↔ 留痕强绑定**：第 ⑥ 阶段必须真实提交存证，提交失败则任务失败，严禁"出域不存证"
- **P1-1 算子强度单调不减**：调用方请求的算子只允许上调保护强度，绝不允许下调（L4 数据即使请求 `none` 也必须走 DP）
- **P1-5 词表一致性**：安全级别标识统一由 `pkg/naming.NormalizeSecurityLevelID` 归一化

### P2：租约任务存储与 FOR UPDATE SKIP LOCKED

service-hub 使用 **租约任务存储（LeasedTaskStore）** 管理任务的并发抢占与崩溃恢复：

```go
// LeasedTaskStore 扩展了基础 TaskStore，增加租约语义
type LeasedTaskStore interface {
    TaskStore
    ClaimNext(ctx context.Context, workerID string, leaseDuration time.Duration) (*Task, error)
    RenewLease(ctx context.Context, taskID, workerID string, duration time.Duration) error
    CompleteLease(ctx context.Context, taskID, workerID string) error
    FailLease(ctx context.Context, taskID, workerID string, errMsg string, errorClass string) error
    RequeueExpiredLeases(ctx context.Context) (int, error)
}
```

**PostgreSQL `FOR UPDATE SKIP LOCKED`**：多副本部署时，多个 worker 并发调用 `ClaimNext`，PostgreSQL 通过行级锁 + `SKIP LOCKED` 保证每个任务只被一个 worker 领取，无需外部分布式锁。

```sql
-- 原子抢占一条 pending 任务（跳过已被其他事务锁定的行）
UPDATE tasks SET status='running', worker_id=$1, lease_expires_at=$2
WHERE id = (
    SELECT id FROM tasks
    WHERE status='pending' AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
    ORDER BY priority DESC, created_at ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
) RETURNING *;
```

**SQLite/内存模式**：无多副本并发能力，通过本地 `StartLocalWorker` 协程轮询 pending 任务实现单点消费。

### P3：先归档后删除的留存红线

audit-log 的存证留存遵循 **P0-8 归档留存红线**：

```
到期存证 ──▶ ① 读取最早到期批次 ──▶ ② 加密归档落盘 ──▶ ③ 磁盘回读验真 ──▶ ④ 按 ID 精确删除
                  │                       │                    │                    │
                  ▼                       ▼                    ▼                    ▼
          FetchOldestForArchive    writeSegment + gzip    VerifySegment      DeleteLogsByIDs
          (分页有界读取)           (SM4-GCM + SM3 行链)   (独立验真)         (归档失败则不删)
```

**Fail-closed 语义**：归档或验真任一失败即中止删除，存证证据不会静默丢失。

**归档段格式**：
- `audit-archive-<cutoff>-<seq>.ndjson.gz.enc` — SM4-GCM(gzip(NDJSON 记录行))
- `audit-archive-<cutoff>-<seq>.manifest.json` — 段元数据与行哈希链尾值

**行哈希链**：`chain[i] = SM3(chain[i-1] || line[i])`，链尾值写入清单。核验时无需访问数据库，仅凭段文件 + 清单 + 密钥即可判定归档证据是否被增删改。

### P4：错误分类与结构化重试

service-hub 的 `internal/retry` 包将流水线错误归一化为 **有界失败分类枚举**，后台重试扫描只读枚举、不读文案：

| 分类 | 含义 | 可重试 | 典型场景 |
|---|---|---|---|
| `timeout` | 上下文/Socket 超时 | ✅ | `context.DeadlineExceeded` |
| `downstream` | 下游不可用 | ✅ | 熔断打开、连接拒绝、网络不可达 |
| `shutdown` | 服务关停/取消 | ✅ | `context.Canceled` |
| `recovered` | 崩溃恢复孤儿任务 | ✅ | 进程重启后的 running 任务 |
| `contract` | 契约级失败 | ❌ | 引擎未返回安全级别 |
| `internal` | 内部故障 | ❌ | panic 恢复、未知错误 |
| `evidence_unavailable` | audit-log 暂时不可用 | ✅ | 5xx / 网络故障 |
| `evidence_rejected` | audit-log 明确拒绝 | ❌ | 4xx 契约不匹配 |
| `evidence_unconfigured` | 未配置存证端点 | ❌ | 重投不可能改变结果 |

**两种偏置（Bias）**：
- `BiasConservative`：调用方自身故障，未知即不重试（避免重试风暴）
- `BiasDownstream`：引擎与数据源出站调用点，未知故障按瞬时处理保持韧性

```go
// Classify 依据错误类型（errors.Is / errors.As）归一化分类，绝不匹配错误文案
func Classify(err error, bias Bias) (class string, retryable bool) {
    switch {
    case errors.Is(err, context.DeadlineExceeded):
        return ClassTimeout, true
    case errors.Is(err, pkgagent.ErrCircuitOpen):
        return ClassDownstream, true
    // ...
    }
}
```

### P5：BFF 聚合代理模式

app-lz BFF（Backend For Frontend）采用 **聚合代理模式**，将四个上游服务的 API 统一暴露给前端：

```
┌─────────────┐
│  React Web   │ ─── HTTP/JSON ───▶ ┌─────────────────────────┐
│  (:5173)     │                    │  app-lz BFF-Go (:8081)   │
└─────────────┘                    │  ├── /api/hub/* → hub    │
                                   │  ├── /api/audit/* → audit│
                                   │  ├── /api/ds/* → ds-mgr  │
                                   │  ├── /api/agent/* → engine│
                                   │  └── /api/auth/* → local │
                                   └─────────────────────────┘
```

**关键设计**：
- **JWT + TOTP 双因素认证**：BFF 层本地管理用户认证（JWT 令牌签发/验证 + TOTP 双因素）
- **上游客户端注册**：`internal/clients/clients.go` 管理四个上游服务的 HTTP 客户端（含超时、mTLS 可选）
- **请求路由映射**：前端请求按路径前缀分发到对应上游服务
- **错误码透传**：上游返回的 5 字段信封原样透传，BFF 层不修改错误结构

---

## 第 2 章：Day 1-2 audit-log 审计日志服务精读

### 2.1 审查文件清单

| 文件 | 行数 | 职责 |
|---|---|---|
| `cmd/server/main.go` | 571 | 程序入口：配置加载 → 存储初始化 → 双协议启动 → 优雅停机 |
| `internal/archive/archive.go` | 519 | 归档留存红线：加密归档段构造 + 独立验真 |
| `internal/handlers/handlers.go` | 763 | REST 路由注册 + 审计日志 CRUD + 统计聚合 |
| `internal/grpcserver/server.go` | 640 | gRPC 服务实现 + TypedServer 统一分发 |
| `internal/config/config.go` | 271 | 环境变量配置加载与校验 |
| `internal/grpcserver/auth.go` | 148 | gRPC 应用层 API Key 鉴权拦截器 |
| `internal/models/models.go` | 90 | 数据模型定义 |
| `internal/agent/client.go` | 25 | Agent 客户端封装（轻量代理） |

### 2.2 服务启动流程与依赖装配

audit-log 的 `main.go` 启动流程按严格顺序装配依赖，每一步失败即 `log.Fatalf` 快速失败：

```
① config.Load() + Validate()                    ← 环境变量解析与一致性校验
    │
② 信封加密密钥版本注册（G-08）                    ← RegisterKeyVersionsFromEnv()
    │                                              多版本密钥轮换支持
③ SM2 签名器/验签器注册（G-10）                   ← NewSM2SignerVerifierFromHex()
    │                                              审计不可否认性
④ 结构化日志初始化                                ← pkgobs.InitLogger()
    │
⑤ API Key 文件热轮转初始化（可选）                ← pkgauth.NewKeyStore()
    │
⑥ 存证哈希链密钥注入                              ← store.SetAuditChainKey()
    │                                              未配置时 Warn（非 Fatal）
⑦ SQLite 完整性校验（SQLite 模式）                ← sqlite.ValidateIntegrity()
    │
⑧ 审计存储后端初始化                              ← initAuditStore()
    │                                              PostgreSQL → SQLite → 内存 分层降级
⑨ Prometheus 指标收集器                           ← metrics.NewCollector("audit-log")
    │
⑩ Agent 客户端 + 留存清理协程                     ← agent.New() + auditRetentionLoop()
    │
⑪ HTTP REST 服务器（Gin）                         ← handlers.New() + RegisterRoutes()
    │                                              含 TrustedProxies / IPAllowlist / CORS
⑫ gRPC 服务器                                     ← pkggrpcserver.New()
    │                                              含 mTLS CN 白名单 + API Key 拦截器
⑬ 信号监听 + 优雅停机                             ← signal.NotifyContext()
```

**关键观察**：

1. **密钥注册先于存储初始化**：信封加密密钥版本注册（步骤 ②）必须在存储初始化之前完成，因为存储层可能需要用密钥解密历史数据
2. **SM2 为可选增强**：SM2 私钥/公钥未配置时跳过注册，不影响哈希链核验（步骤 ③）
3. **存证密钥 Warn 而非 Fatal**：`SetAuditChainKey` 后若密钥为空只输出 Warn，允许开发环境无密钥运行（步骤 ⑥）
4. **StrictStorage 模式**：配置 `AUDIT_LOG_STRICT_STORAGE=true` 时，存储初始化失败直接 Fatal（不降级到内存）

### 2.3 存储后端初始化与分层降级

`initAuditStore` 函数实现了三级存储降级策略：

```go
func initAuditStore(cfg *config.Config, logger *slog.Logger) (store.AuditStore, error) {
    // 1. Try PostgreSQL（3s 探测超时）
    if cfg.PGDSN != "" {
        pgStore, err := postgres.NewAuditStore(ctx, ...)
        if err == nil {
            // Write-only 自检（P1-6）
            if cfg.DBWriteOnly { verifyWriteOnlyPostgres(...) }
        } else if cfg.StrictStorage { return nil, err }
    }

    // 2. Fallback to SQLite（含完整性校验）
    if underlying == nil && cfg.DBPath != "" {
        sqlite.ValidateIntegrity(cfg.DBPath)  // 启动时校验
        db, err := sqlite.Open(cfg.DBPath, ...)
    }

    // 3. Fallback to in-memory（最后兜底）
    if underlying == nil {
        if cfg.StrictStorage { return nil, err }
        underlying = memory.NewAuditStore()
    }

    // 4. Wrap with BufferedAuditStore（微批刷盘器装饰器）
    return flusher.NewBufferedAuditStore(underlying, flusherConfigFrom(cfg), logger), nil
}
```

**Write-Only PostgreSQL 自检**（`verifyWriteOnlyPostgres`）：
- 检查 `audit_logs` 和 `snapshots` 表是否被授予 `UPDATE` / `DELETE` 权限
- 若存在则拒绝启动（链式存证可被事后改写）
- 前置条件：DBA 预先执行 `deploy/sql/audit_writeonly_role.sql`

**刷盘器配置合并**（`flusherConfigFrom`）：
- 以 `flusher.DefaultConfig()` 为基线
- 仅覆盖配置为正的参数（`> 0` 判断），零值保留默认

### 2.4 REST API 路由与权限模型

audit-log 的 REST 路由清单：

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/health` | 无 | 存活探针 |
| GET | `/readyz` | 无 | 就绪探针 |
| GET | `/api/health` | 无 | 标准健康检查 |
| POST | `/api/audit/logs` | `audit:write` | 创建审计日志 |
| GET | `/api/audit/logs` | `audit:read` | 分页查询审计日志 |
| GET | `/api/audit/logs/:id` | `audit:read` | 查询单条日志 |
| GET | `/api/audit/stats` | `audit:read` | 统计聚合 |
| GET | `/api/audit/snapshots` | `audit:read` | 查询脱敏快照 |
| POST | `/api/audit/snapshots/verify` | `audit:read` | 验真快照完整性 |
| GET | `/api/audit/chain/verify` | `audit:read` | 全量哈希链核验 |
| POST | `/api/audit/chain/verify` | `audit:read` | 全量哈希链核验（POST） |
| POST | `/api/audit/report` | `audit:write` | 合规报告生成 |
| GET | `/metrics` | 无 | Prometheus 指标 |

**读写分离权限**：
- `audit:write`：写入存证、导出报表
- `audit:read`：查询、统计、验真、链核验
- 只读核验员 Key 仅可访问 `auditReadOnlyEndpoints` 白名单中的 GET 端点

### 2.5 gRPC 服务实现与拦截器链

audit-log gRPC 服务端的拦截器链按叠加顺序：

```
入站请求
  │
  ▼
① mTLS CN 白名单拦截器（可选）    ← tlsutil.NewWhitelistInterceptor()
  │                                   校验客户端证书 CN 是否在白名单内
  ▼
② API Key 鉴权拦截器（G-17）      ← grpcserver.AuthUnaryInterceptor()
  │                                   静态 Key + ScopeKeys + KeyStore 热轮转合并
  ▼
③ 业务处理                         ← grpcserver.GRPCServer
```

**双层鉴权设计**：mTLS 验证"你是谁"（证书身份），API Key 验证"你能做什么"（Scope 权限），两者叠加形成零信任安全模型。

### 2.6 归档留存红线 archive.go 深度走读

`internal/archive/archive.go`（519 行）实现了存证留存红线的完整逻辑。

**核心流程 `ArchiveAndCleanup`**：

```go
func (a *Archiver) ArchiveAndCleanup(audit store.AuditStore, cutoff time.Time) (*Stats, error) {
    // 1. 类型断言：底层存储必须支持 AuditArchiveReader 接口
    reader, ok := audit.(store.AuditArchiveReader)
    if !ok { return nil, ErrStoreUnsupported }

    // 2. 分页循环（上限 maxArchivePages=100000 防无限循环）
    for page := 0; page < maxArchivePages; page++ {
        // 2a. 读取最早到期批次
        logs, snaps, err := reader.FetchOldestForArchive(cutoff, a.opts.PageSize)
        if len(logs) == 0 { return stats, nil }  // 无更多到期记录

        // 2b. 写归档段（加密 + 行哈希链）
        segment, err := a.writeSegment(logs, snaps, cutoff)

        // 2c. 磁盘回读验真
        if err := VerifySegment(a.opts.ArchiveDir, segment, a.opts.EncryptionKey); err != nil {
            return stats, fmt.Errorf("... deletion refused: %w", err)
        }

        // 2d. 按 ID 精确删除
        deleted, err := reader.DeleteLogsByIDs(ids)
        if deleted == 0 { return stats, ErrNotDeleted }
    }
}
```

**Fail-closed 安全保证**：
- 归档失败 → 不删除（`"deletion refused"`）
- 验真失败 → 不删除（`"deletion refused"`）
- 删除返回 0 → 立即中止（`ErrNotDeleted`，防止重复归档）
- 超过分页上限 → 返回错误

### 2.7 留存清理协程 auditRetentionLoop

```go
func auditRetentionLoop(ctx context.Context, auditStore store.AuditStore, ...) {
    ticker := time.NewTicker(6 * time.Hour)  // 每 6 小时执行一次
    defer ticker.Stop()

    runOnce := func() {
        cutoff := time.Now().AddDate(0, 0, -cfg.RetentionDays)
        archiver, err := archive.New(archive.Options{...}, logger)
        if err != nil {
            logger.Error("archive guard unavailable, deletion refused")
            return  // 归档器不可用 → 不删除
        }
        stats, err := archiver.ArchiveAndCleanup(auditStore, cutoff)
        if err != nil {
            logger.Error("archive-before-delete failed, deletion stopped")
            return  // 任何失败 → 不删除
        }
    }

    runOnce()  // 启动时立即执行一次
    for {
        select {
        case <-ctx.Done(): return  // 优雅停机
        case <-ticker.C: runOnce()
        }
    }
}
```

**设计要点**：
- `RetentionDays=0`（默认）时不启动清理协程，存证证据始终保留
- 归档器在每次运行时重新构造（`archive.New`），确保使用最新的目录与密钥配置
- `ctx` 由 `context.WithCancel` 管理，停机信号到达时自动退出

### 2.8 Agent 客户端封装

audit-log 的 Agent 客户端（`internal/agent/client.go`，25 行）是一个轻量代理封装，将脱敏请求转发到 engine-go。与 service-hub 的 Agent 客户端（242 行）不同，audit-log 的 Agent 客户端主要用于**直接调用引擎执行脱敏并记录结果到审计日志**的场景。

### 2.9 配置体系与环境变量

audit-log 的配置通过环境变量加载（`internal/config/config.go`，271 行），关键配置项：

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `AUDIT_LOG_PORT` | `8084` | HTTP REST 端口 |
| `AUDIT_LOG_GRPC_PORT` | `50054` | gRPC 端口 |
| `AUDIT_LOG_DB_PATH` | `./audit.db` | SQLite 数据库路径 |
| `AUDIT_LOG_PG_DSN` | `""` | PostgreSQL DSN |
| `AUDIT_LOG_HASH_KEY` | `""` | HMAC-SM3 存证密钥 |
| `AUDIT_LOG_ENCRYPTION_KEY` | `""` | SM4-GCM 加密密钥 |
| `AUDIT_LOG_RETENTION_DAYS` | `0` | 存证保留天数（0=永不清理） |
| `AUDIT_LOG_ARCHIVE_DIR` | `./archives` | 归档段存储目录 |
| `AUDIT_LOG_TLS_ENABLED` | `false` | 启用 TLS |
| `AUDIT_LOG_API_KEY` | `""` | 写入 API Key |
| `AUDIT_LOG_READER_API_KEY` | `""` | 只读 API Key |
| `AUDIT_LOG_STRICT_STORAGE` | `false` | 严格存储模式 |
| `AUDIT_LOG_DB_WRITE_ONLY` | `false` | PostgreSQL 写入只角色 |

---

## 第 3 章：Day 3-4 service-hub 服务调度中枢精读

### 3.1 审查文件清单

| 文件 | 行数 | 职责 |
|---|---|---|
| `cmd/server/main.go` | 789 | 程序入口：配置 → 存储 → 双协议 → 优雅停机 |
| `internal/handlers/handlers.go` | 864 | REST 路由 + 6 阶段流水线 processTask |
| `internal/grpcserver/server.go` | 1106 | gRPC TypedServer 统一分发 |
| `internal/audit/client.go` | 757 | audit-log 存证客户端（fail-closed） |
| `internal/datasource/client.go` | 746 | datasource-mgr 双协议客户端 |
| `internal/agent/client.go` | 242 | engine-go Agent 客户端 |
| `internal/config/config.go` | 373 | 环境变量配置加载 |
| `internal/models/models.go` | 179 | 数据模型 + 算子强度映射 |
| `internal/retry/retry.go` | 129 | 错误分类与重试判定 |

### 3.2 服务启动流程与双协议监听

service-hub 的启动流程与 audit-log 类似但增加了更多组件：

```
① config.Load() + Validate()
② API Key Store 初始化（可选）
③ 结构化日志初始化
④ 任务存储初始化（SQLite / 内存 / PostgreSQL）
⑤ Prometheus 指标收集器
⑥ Agent 客户端（engine-go 连接）
⑦ Datasource 客户端（datasource-mgr 连接，含熔断器）
⑧ Server 构造（含 audit 客户端无条件装配）
⑨ 崩溃恢复：RequeueExpiredLeases（回收孤儿任务）
⑩ 本地 Worker 启动（SQLite/内存模式）
⑪ HTTP REST + gRPC 双协议监听
⑫ 信号监听 + 优雅停机
```

**关键差异**：
- **崩溃恢复步骤 ⑨**：启动时立即调用 `RequeueExpiredLeases`，将上次崩溃时遗留的 `running` 任务回退到 `pending`
- **audit 客户端无条件装配**：即使未配置 audit-log URL，audit 客户端也会构造，其 Submit 必然返回 `ErrNotConfigured`，由流水线将任务置为 failed（fail-closed）

### 3.3 6 阶段流水线调度状态机

service-hub 的任务生命周期状态机：

```
                  ┌──────────────────────────────────────────────┐
                  │                                              │
    dispatch ──▶ pending ──▶ running ──▶ completed               │
                  │              │                                │
                  │              ├──▶ failed (non-retryable)      │
                  │              │                                │
                  │              └──▶ pending (retryable, 自动重试) │
                  │                                              │
                  └──▶ rejected (队列满/参数无效)                  │
                  └──────────────────────────────────────────────┘
```

**流水线阶段状态**：

| 阶段 | 进入条件 | 退出条件 | 失败处理 |
|---|---|---|---|
| ingest | 请求到达 | TaskID 分配成功 | 返回 rejected |
| fetch | 任务开始处理 | 原始数据获取成功 | 按 ErrorClass 判定重试/失败 |
| classify | 数据就绪 | 安全级别确定 | 兜底 mask（不跳过） |
| desensitize | 级别确定 | 引擎返回脱敏结果 | 按 ErrorClass 判定重试/失败 |
| return | 脱敏完成 | 快照保存成功 | 任务仍标记 failed |
| audit | 出域完成 | 存证提交成功 | **任务失败**（P0-6 强绑定） |

### 3.4 算子强度单调不减约束（P1-1）

`EffectiveOperation` 函数确保调用方请求的算子只允许上调保护强度：

```go
func EffectiveOperation(requested, derived string) string {
    derivedStrength := OperationStrength(derived)
    if derivedStrength < 0 {
        derived = OperationMask  // 不可识别 → 兜底 mask
        derivedStrength = OperationStrength(derived)
    }
    requestedStrength := OperationStrength(requested)
    if requestedStrength > derivedStrength {
        return strings.TrimSpace(requested)  // 请求更强 → 采用请求
    }
    return derived  // 请求更弱 → 采用定级结果（只允许上调）
}
```

**算子强度序**：`none(0) = classify(0) < mask(1) = qol(1) < k_anon(2) < dp(3)`

**示例**：
- L4 数据（derived=dp, strength=3），请求 `none`（strength=0）→ 生效 `dp`
- L2 数据（derived=mask, strength=1），请求 `k_anon`（strength=2）→ 生效 `k_anon`
- L3 数据（derived=k_anon, strength=2），请求 `mask`（strength=1）→ 生效 `k_anon`

### 3.5 存证客户端 audit/client.go 深度走读

`internal/audit/client.go`（757 行）是 service-hub 与 audit-log 之间的存证桥梁，设计了三条红线：

**红线 1：失败即任务失败**
```go
// Submit 返回错误时，流水线必须将任务置为 failed
// 严禁 logger.Warn 后继续置 done
func (c *Client) Submit(ctx context.Context, req EvidenceRequest) (*EvidenceResponse, error) {
    // ... 构造请求、重试逻辑 ...
    if resp.StatusCode >= 500 {
        return nil, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
    }
    if resp.StatusCode >= 400 {
        return nil, fmt.Errorf("%w: status %d", ErrRejected, resp.StatusCode)
    }
}
```

**红线 2：禁止伪造链头**
- `prev_hash` 由 audit-log 存储层唯一指派
- 客户端请求结构中不含该字段

**红线 3：真实指纹**
- `input_hash` / `output_hash` 取自 engine 返回的 SM3 指纹
- 缺失时由客户端对出域载荷计算 SM3

**多端点支持**：
```go
// 支持配置多个 audit-log 端点，按顺序尝试
// SERVICE_HUB_AUDIT_LOG_URLS="http://audit-1:8084,http://audit-2:8084"
```

**重试策略**：指数退避 + 随机抖动（`defaultRetryBase=500ms`，`defaultMaxRetries=3`）

### 3.6 数据源客户端 datasource/client.go 深度走读

`internal/datasource/client.go`（746 行）提供与 datasource-mgr 的双协议通信：

**HTTP REST 路径**：
- 标准 `net/http` 客户端，10s 超时
- 可选 mTLS（TLS 1.3 + 客户端证书）
- 指数退避重试（最多 3 次）

**gRPC 路径**：
- 延迟连接（`sync.RWMutex` 保护，首次调用时建立）
- Keepalive 参数对齐（30s ping，10s timeout）
- 三态熔断器（Closed → Open → HalfOpen）

**GetRecordByIDCard**：按身份证号查询单条记录（第三周新增功能）

### 3.7 错误分类与结构化重试 retry/retry.go

`internal/retry/retry.go`（129 行）将错误归一化为有界分类枚举：

**Classify 函数决策树**：

```
err == nil?
  └── YES → ClassInternal, false

errors.Is(err, context.DeadlineExceeded)?
  └── YES → ClassTimeout, true

errors.Is(err, context.Canceled)?
  └── YES → ClassShutdown, true  (非下游故障，是关停信号)

errors.Is(err, pkgagent.ErrCircuitOpen / ErrTransport / ErrEndpointUnavailable)?
  └── YES → ClassDownstream, true

errors.Is(err, syscall.ECONNREFUSED / ECONNRESET / EHOSTUNREACH / ...)?
  └── YES → ClassDownstream, true

net.Error && Timeout()?
  └── YES → ClassTimeout, true

bias == BiasDownstream?
  └── YES → ClassDownstream, true  (未知错误按瞬时处理)
  └── NO  → ClassInternal, false   (未知错误按内部故障)
```

### 3.8 崩溃恢复与租约过期回收

service-hub 启动时执行崩溃恢复：

```go
// 回收所有 lease_expires_at < NOW() 的 running 任务
recovered, err := tasks.RequeueExpiredLeases(ctx)
if err != nil {
    logger.Error("failed to requeue expired leases", "error", err.Error())
}
if recovered > 0 {
    logger.Info("recovered orphan tasks from previous crash", "count", recovered)
}
```

**恢复策略**：
- 将 `running` 但租约已过期的任务回退到 `pending`
- 错误分类标记为 `ClassRecovered`（可重试）
- 本地 Worker 协程自动重新领取这些任务

### 3.9 gRPC TypedServer 统一分发

service-hub 的 gRPC 实现采用 **TypedServer 统一分发** 模式：

```go
// 所有 gRPC 方法统一走 RawCodec 字节级转发
// TypedServer 根据方法名分发到对应的 handler 函数
func (s *GRPCServer) handleMethod(method string, rawReq []byte) ([]byte, error) {
    switch method {
    case "/privacy.ServiceHub/Dispatch":
        return s.handleDispatch(rawReq)
    case "/privacy.ServiceHub/GetTask":
        return s.handleGetTask(rawReq)
    // ...
    }
}
```

---

## 第 4 章：Day 5 跨服务集成与端到端联调

### 4.1 全栈服务拓扑与启动顺序

全栈联调的服务启动顺序（考虑依赖关系）：

```
① engine-go (:8079 + :50051)         ← 核心引擎，无外部依赖
② audit-log (:8084 + :50054)         ← 依赖 engine-go（Agent 代理）
③ datasource-mgr (:8083 + :50053)    ← 独立服务
④ service-hub (:8082 + :50052)       ← 依赖 ①②③
⑤ app-lz BFF (:8081)                ← 依赖 ②③④
⑥ console/web (:5173)               ← 依赖 ⑤
```

### 4.2 核心链路端到端验证

端到端核心链路验证步骤：

```
1. service-hub 调度脱敏任务
   POST /api/hub/dispatch
   {"datasource_id": "ds_yibao", "operation": "mask", "payload": {...}}
   
2. service-hub → datasource-mgr 获取原始数据
   GET /api/datasource/yibao/records?id_card=xxx
   
3. service-hub → engine-go 分类分级
   POST /v1/dynclassification
   
4. service-hub → engine-go 执行脱敏
   POST /v1/privacy
   
5. service-hub → audit-log 提交存证
   POST /api/audit/logs
   
6. 验证哈希链完整性
   GET /api/audit/chain/verify
```

### 4.3 app-lz BFF 聚合代理层走读

BFF 层的核心结构：

```go
// Server 聚合四个上游客户端
type Server struct {
    hub      *clients.UpstreamClient  // service-hub
    audit    *clients.UpstreamClient  // audit-log
    ds       *clients.UpstreamClient  // datasource-mgr
    agent    *clients.UpstreamClient  // engine-go
    cfg      *config.Config
    auth     *auth.Handler
    logger   *slog.Logger
}
```

**请求路由映射**：

| BFF 路径前缀 | 上游服务 | 说明 |
|---|---|---|
| `/api/hub/*` | service-hub :8082 | 任务调度 |
| `/api/audit/*` | audit-log :8084 | 审计查询 |
| `/api/datasource/*` | datasource-mgr :8083 | 数据源管理 |
| `/api/agent/*` | engine-go :8079 | 引擎直连 |
| `/api/auth/*` | 本地处理 | JWT/TOTP 认证 |

### 4.4 统一错误码信封与错误传播

所有服务的 REST 错误响应统一使用 5 字段信封：

```json
{
    "status": 400,
    "error": "VALIDATION_ERROR",
    "message": "datasource_id is required",
    "path": "/api/hub/dispatch",
    "timestamp": "2026-09-03T10:30:00Z"
}
```

**错误传播链路**：
- engine-go 返回错误 → service-hub 解析信封 → 按错误分类判定重试 → 最终返回给 BFF
- BFF 原样透传上游错误信封（不修改结构）
- 前端根据 `error` 字段做国际化展示

### 4.5 超时传播与上下文链路

跨服务调用的超时传播：

```
BFF (30s total)
  └── service-hub (processTask, 无硬超时，受限于子调用)
        ├── datasource-mgr (10s HTTP timeout)
        ├── engine-go classify (10s HTTP timeout)
        ├── engine-go privacy (30s HTTP timeout, 含 LLM 调用)
        └── audit-log submit (10s HTTP timeout, 3 retries)
```

**关键约束**：
- 每个出站 HTTP 客户端独立配置超时
- gRPC 调用使用 keepalive 参数防止空闲连接被中间件断开
- audit-log 存证提交的 3 次重试总耗时不超过 `3 * (10s + 500ms * 2^attempt)`

### 4.6 命名归一化与词表一致性（P1-5）

`pkg/naming` 包提供统一的标识符归一化：

```go
// NormalizeSecurityLevelID 将各种词表归一化为 L1~L5
// "L1" / "l1" / "public" / "PUBLIC" → "L1"
// "L3" / "confidential" → "L3"
func NormalizeSecurityLevelID(level string) string { ... }

// NormalizeDatasourceID 将数据源 ID 归一化
// "yibao" / "ds_yibao" / "DS_YIBAO" → "ds_yibao"
func NormalizeDatasourceID(id string) string { ... }
```

**跨服务一致性要求**：
- service-hub 的 `LevelToOperation` 使用 `NormalizeSecurityLevelID`
- audit-log 的存证记录使用归一化后的 `security_level`
- engine-go 的分类结果经 `NormalizeSecurityLevelID` 归一化后回传

---

## 第 5 章：audit-log 核心数据模型与接口分析

### AuditLog 数据模型

```go
type AuditLog struct {
    ID            string    `json:"id"`
    TaskID        string    `json:"task_id,omitempty"`
    APICode       string    `json:"api_code,omitempty"`
    DatasourceID  string    `json:"datasource_id,omitempty"`
    Timestamp     time.Time `json:"timestamp"`
    Operation     string    `json:"operation"`         // "mask" | "classify" | "k_anon" | "dp" | "qol"
    DataSource    string    `json:"datasource"`
    InputHash     string    `json:"input_hash"`        // SM3 指纹
    OutputHash    string    `json:"output_hash"`       // SM3 指纹
    Algorithm     string    `json:"algorithm"`
    Parameters    any       `json:"parameters"`
    InputRows     int       `json:"input_rows"`
    OutputRows    int       `json:"output_rows"`
    DurationMs    int64     `json:"duration_ms"`
    User          string    `json:"user"`
    Status        string    `json:"status"`            // "success" | "failed"
    SecurityLevel string    `json:"security_level"`    // L1~L5
}
```

### AuditStore 接口

```go
type AuditStore interface {
    SaveLog(ctx context.Context, log *AuditLog) error
    GetLog(ctx context.Context, id string) (*AuditLog, error)
    ListLogs(ctx context.Context, query AuditLogQuery) ([]*AuditLog, int, error)
    GetStats(ctx context.Context, period string) (*AuditStats, error)
    VerifyChain(ctx context.Context) (*ChainVerificationResult, error)
    SaveSnapshot(ctx context.Context, snap *SnapshotRecord) error
    Close() error
}
```

### AuditArchiveReader 可选能力接口

```go
type AuditArchiveReader interface {
    AuditStore
    FetchOldestForArchive(cutoff time.Time, pageSize int) ([]AuditLog, []SnapshotRecord, error)
    DeleteLogsByIDs(ids []string) (int64, error)
}
```

---

## 第 6 章：service-hub 核心数据模型与接口分析

### Task 数据模型

```go
type Task struct {
    ID           string     `json:"id"`
    APICode      string     `json:"api_code,omitempty"`
    DatasourceID string     `json:"datasource_id"`
    Status       string     `json:"status"`      // "pending" | "running" | "completed" | "failed"
    Stage        string     `json:"stage"`       // "ingest" ~ "audit" | "done"
    Operation    string     `json:"operation"`   // "none" | "mask" | "k_anon" | "dp"
    CreatedAt    time.Time  `json:"created_at"`
    StartedAt    *time.Time `json:"started_at"`
    CompletedAt  *time.Time `json:"completed_at"`
    DurationMs   int64      `json:"duration_ms"`
    Error        string     `json:"error,omitempty"`
}
```

### LevelToOperation 映射

| 安全级别 | 英文标识 | 映射算子 | 说明 |
|---|---|---|---|
| L1 | public | `none` | 公开数据，无需脱敏 |
| L2 | internal | `mask` | 内部数据，字段级掩码 |
| L3 | confidential | `k_anon` | 敏感数据，K-匿名泛化 |
| L4 | secret | `dp` | 高敏感，差分隐私加噪 |
| L5 | top_secret | `dp` | 极敏感，强 DP + 严格预算 |
| 未知 | — | `mask` | 安全兜底 |

### EffectiveOperation 示例

| 定级推导 (derived) | 调用方请求 (requested) | 生效算子 | 原因 |
|---|---|---|---|
| dp (3) | none (0) | dp | 只允许上调 |
| mask (1) | k_anon (2) | k_anon | 请求更强 |
| k_anon (2) | mask (1) | k_anon | 只允许上调 |
| dp (3) | dp (3) | dp | 强度相等 |
| mask (1) | "" (空) | mask | 空请求采用定级结果 |

---

## 第 7 章：audit-log 归档段格式与验真算法深度走读

### 7.1 归档段文件命名与防覆盖策略

归档段文件名格式：`audit-archive-<cutoff>-<seq>.ndjson.gz.enc`

```go
func (a *Archiver) nextSegmentName(cutoff time.Time) (string, error) {
    prefix := "audit-archive-" + cutoff.UTC().Format("20060102T150405Z") + "-"
    for seq := 0; seq < 1000000; seq++ {
        name := fmt.Sprintf("%s%06d%s", prefix, seq, segmentSuffix)
        // 检查文件是否已存在，绝不覆盖既有归档证据
        switch _, err := os.Stat(path); {
        case errors.Is(err, fs.ErrNotExist):
            return name, nil  // 找到未占用的文件名
        }
    }
}
```

**设计要点**：
- cutoff 时间戳 UTC 格式化为 `20060102T150405Z`，保证排序与可读性
- 6 位序号（`%06d`）支持同一 cutoff 最多 100 万个段
- `os.O_EXCL` 创建标志防止并发覆盖
- 路径穿越防护：`resolveInDir` 拒绝含 `/`、`\\`、`..` 的文件名

### 7.2 NDJSON 行哈希链构造

归档段内每行 NDJSON 记录通过 SM3 哈希链串联：

```go
func writeChainLine(w io.Writer, chain *string, line *archiveLine) error {
    raw, err := json.Marshal(line)
    // chain[i] = SM3(chain[i-1] || line[i])
    *chain = crypto.SumSM3Hex(append([]byte(*chain), raw...))
    raw = append(raw, '\n')
    _, err = w.Write(raw)
    return err
}
```

**链式结构**：
```
chain[0] = SM3("" || line[0])     // 首行：前驱为空串
chain[1] = SM3(chain[0] || line[1])
chain[2] = SM3(chain[1] || line[2])
...
chainTail = chain[n-1]            // 链尾写入 manifest.json
```

**与主日志哈希链的区别**：
- 主日志链：9 要素前映像 + HMAC-SM3，每条记录独立可验
- 归档段行链：SM3(前驱 || 当前行 JSON)，检测增删改与重排序

### 7.3 SM4-GCM 加密落盘与 fsync 持久化

归档段写入流程：

```
NDJSON 行 → bytes.Buffer → gzip 压缩 → SM4-GCM 加密 → O_EXCL 写入 → fsync 文件 + 目录
```

```go
func writeFsync(path string, data []byte) error {
    f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
    f.Write(data)
    f.Sync()        // 文件数据刷盘
    f.Close()
    d, _ := os.Open(filepath.Dir(path))
    d.Sync()        // 目录项刷盘（确保文件条目持久化）
    d.Close()
}
```

**权限控制**：文件 `0o600`（仅属主可读写），目录 `0o700`

### 7.4 VerifySegment 独立验真全流程

`VerifySegment` 无需访问数据库，仅凭段文件 + 清单 + 密钥即可判定归档证据完整性：

```
① 读取 manifest.json → 校验版本与段文件名引用
② 读取 .ndjson.gz.enc → SM4-GCM 解密 → gzip 解压 → 得到 NDJSON 原文
③ 逐行重算 SM3 行哈希链 → 与 manifest.ChainTail 比对
④ 逐行 JSON 反序列化 → 统计 log/snapshot 计数
⑤ 对每条 log 重算 9 要素 integrity_hash → 与记录中的 IntegrityHash 比对
⑥ 校验边界 ID 与时间戳与 manifest 一致
```

**验真失败条件**：
- 行哈希链不匹配 → 证据被修改、截断或重排序
- 记录计数不一致 → 证据被增删
- 边界 ID/时间戳不匹配 → 段文件与清单不对应
- 单条 log 的 integrity_hash 不匹配 → 记录内容被篡改

### 7.5 路径穿越防护与安全文件名校验

```go
func resolveInDir(dir, name string) (string, error) {
    // 拒绝含路径分隔符或特殊字符的文件名
    if strings.ContainsRune(name, os.PathSeparator) || name == "." || name == ".." ||
       strings.Contains(name, "/") || strings.Contains(name, "\\") {
        return "", fmt.Errorf("archive: unsafe file name %q", name)
    }
    // 解析绝对路径后校验目录包含关系
    base, _ := filepath.Abs(dir)
    full, _ := filepath.Abs(filepath.Join(base, name))
    if filepath.Dir(full) != base {
        return "", fmt.Errorf("archive: %q escapes archive dir %s", name, dir)
    }
    return full, nil
}
```

---

## 第 8 章：service-hub 流水线 processTask 完整执行路径走读

### 8.1 ingest → fetch → classify 阶段

**① ingest 阶段**：
- 更新 `task.Status = "running"`、`task.Stage = "ingest"`
- 持久化任务状态到 TaskStore
- 检查优雅停机信号（`s.ctx.Done()`）

**② fetch 阶段**：
- 若 `req.Payload` 为空，自动向 datasource-mgr 拉取数据
- `s.datasource.FetchData(ctx, datasourceID, 10, 0)` — 最多拉取 10 条
- 获取成功后回写 `task.PayloadJSON` 并持久化

**③ classify 阶段**（核心）：
- 一次调用 engine 医疗流水线 `/v1/medical/process`
- 同时完成：3-Layer 分类分级 + L4/L5 高敏文本剥离 + PII 强掩码 + ICD-10 脱敏
- 15 秒超时，幂等键 `hub-<taskID>-classify-<retryCount>`
- 失败时按 `BiasDownstream` 分类错误（可重试/不可重试）

### 8.2 desensitize 阶段：Agent 调用与指纹回传

**④ desensitize 阶段**：
- 已由 ③ classify 合并完成，快速通过
- 保留阶段状态追踪（`task.Stage = "desensitize"`）
- 从 classify 结果中提取：`egressOutput`（脱敏数据）、`egressLevel`（最高敏感级别）、`egressHashIn/Out`（SM3 指纹）

**算子强度校验**（P1-1）：
```go
effectiveOp := models.EffectiveOperation(req.Operation, derivedOp)
// 调用方请求的算子只允许上调保护强度
```

### 8.3 return 阶段：结果返回与脱敏快照

**⑤ return 阶段**：
- 组装脱敏后的数据对象
- 保存脱敏快照（`SnapshotRecord`）到审计存储
- 快照包含：输入/输出样本（截断）、算法参数、完整性哈希

### 8.4 audit 阶段：存证强绑定与证据链锚定

**⑥ audit 阶段**（P0-6 红线）：
- 调用 `s.audit.Submit(ctx, evidence)` 向 audit-log 提交存证
- 存证内容包含：`task_id`、`api_code`、`datasource_id`、`input_hash`、`output_hash`、`security_level`、`operation`
- **提交失败 → 任务失败**：无论何种原因（未配置/网络不可达/4xx/5xx），任务状态置为 `failed`
- 存证成功后，将 `integrity_hash` 回写到任务记录（证据链锚定）

### 8.5 信号量并发控制与优雅停机

**并发控制**：
```go
s.taskSem <- struct{}{}        // 获取信号量（最多 10 个并发任务）
defer func() { <-s.taskSem }() // 释放信号量
```

**Panic 安全恢复**：
```go
defer func() {
    if r := recover(); r != nil {
        task.Status = "failed"
        task.Error = fmt.Sprintf("internal panic: %v", r)
        task.ErrorClass = retry.ClassInternal
        _ = s.persistTask(task, "panic recovery")
    }
}()
```

**优雅停机检查**：每个阶段开始前检查 `s.ctx.Done()`，收到停机信号时任务标记为 `failed`（`ClassShutdown`）。

---

## 第 9 章：app-lz BFF 层聚合代理深度走读

### 9.1 四上游服务客户端注册

BFF 层通过 `ClientPool` 管理与 4 个上游服务的 HTTP 通信：

```go
type ClientPool struct {
    cfg        *config.Config
    httpClient *http.Client  // 共享 HTTP 客户端（连接池复用）
}
```

**连接池配置**：
- 全局超时：10s
- 最大空闲连接：100
- 每主机最大空闲连接：25
- 空闲连接回收：90s
- TLS：`InsecureSkipVerify: true`（兼容自签名证书）

**请求头注入**（`setHeaders`）：
- `X-Request-ID`：链路追踪标识
- `X-Trace-ID`：分布式追踪 ID
- `Authorization: Bearer <APIKey>`：按 serviceID 选择对应的 Key

### 9.2 请求路由与转发映射

BFF 路由分组（`/api/lz/*`）：

| 路由组 | 上游服务 | 说明 |
|---|---|---|
| `/api/lz/topology` | 全部 4 个 | 服务拓扑探测 |
| `/api/lz/pipeline` | service-hub | 6 阶段流水线状态 |
| `/api/lz/tasks/*` | service-hub | 任务管理 |
| `/api/lz/suites/*` | 本地执行 | E2E 测试套件 |
| `/api/lz/audit/*` | audit-log | 审计日志与验真 |
| `/api/lz/metrics*` | 本 BFF | Prometheus 指标 |
| `/api/lz/data-api/*` | 编排调用 | 预设数据 API（5 阶段会话） |

**降级策略**：上游不可达时返回硬编码 fallback 数据（`sourceFallback` 标记），确保前端大屏在开发/演示模式下仍有数据展示。

### 9.3 认证与会话管理（JWT + TOTP）

BFF 层本地管理用户认证：

```go
// JWT 认证中间件
if h.cfg.AuthEnabled && h.cfg.JWTSecret != "" {
    jwtMgr, _ := auth.NewJWTManager(h.cfg.JWTSecret, h.cfg.JWTExpiryHours)
    r.Use(auth.JWTAuthMiddleware(jwtMgr, true))
}
```

**认证流程**：
1. 用户登录 → 验证用户名/密码 → 签发 JWT
2. 后续请求携带 `Authorization: Bearer <JWT>` → 中间件校验
3. TOTP 双因素（可选）：登录时验证 TOTP 码

### 9.4 上游健康检查聚合

`ProbeNode` 方法探测单个上游微服务的健康状态：

```
① REST 探测：GET /api/health → 失败则回退 /health
② gRPC 探测：TCP Dial 检测端口可达性（800ms 超时）
③ 综合判断：根据前端选择的活跃协议设置整体状态
```

**特殊处理**：gRPC TCP 探测失败但 REST 正常时，认为 gRPC 也「ready」（本地 mock 模式兼容）。

---

## 第 10 章：代码走读指南

### 推荐阅读顺序

1. **先读测试文件**：理解预期行为与边界条件
   - `services/audit-log/internal/archive/archive_test.go`
   - `services/service-hub/internal/handlers/handlers_test.go`
   - `services/service-hub/internal/retry/retry_test.go`

2. **再读核心实现**：
   - `services/audit-log/internal/archive/archive.go` — 归档留存红线
   - `services/service-hub/internal/handlers/handlers.go` — 6 阶段流水线
   - `services/service-hub/internal/audit/client.go` — 存证客户端

3. **最后读启动流程**：
   - `services/audit-log/cmd/server/main.go`
   - `services/service-hub/cmd/server/main.go`

### 标记约定

- `// REVIEW:` — 需要讨论的代码段
- `// P0-6` — 出域 ↔ 留痕强绑定相关
- `// P1-1` — 算子强度单调不减约束
- `// G-05` — 三级等保合规相关

---

## 第 11 章：常见问题与排查指南

### 问题 1：service-hub 任务一直 pending 不执行

**排查步骤**：
1. 检查存储模式：PostgreSQL 模式由共享租约 worker 消费，SQLite/内存模式由本地 worker 消费
2. 检查 `StartLocalWorker` 是否被调用
3. 检查任务信号量是否已满（`taskSem` 容量 10）

### 问题 2：audit-log 归档段验真失败

**排查步骤**：
1. 确认密钥与归档时使用的密钥一致
2. 检查段文件是否被修改（`os.Stat` 对比文件大小）
3. 检查 manifest.json 的 `chain_tail` 与实际重算结果

### 问题 3：存证提交失败导致任务 failed

**排查步骤**：
1. 检查 `SERVICE_HUB_AUDIT_LOG_URLS` 是否配置
2. 检查 audit-log 服务是否存活（`GET /health`）
3. 检查 audit-log 返回的状态码（4xx = 契约问题，5xx = 服务端问题）
4. 查看 service-hub 日志中的 `error_class` 字段

### 问题 4：BFF 层返回 fallback 数据

**排查步骤**：
1. 检查响应中的 `source` 字段是否为 `"fallback"`
2. 检查上游服务是否存活（`/api/lz/topology`）
3. 检查 BFF 日志中的上游调用错误

---

## 第 12 章：术语表

| 术语 | 英文 | 含义 |
|---|---|---|
| 6 阶段流水线 | 6-Stage Pipeline | ingest → fetch → classify → desensitize → return → audit |
| 租约任务存储 | Leased Task Store | 基于租约的任务并发抢占与崩溃恢复机制 |
| 归档留存红线 | Archive Retention Red Line | 到期存证必须先归档后删除的强制策略 |
| 错误分类 | Error Classification | 将错误归一化为有界枚举（timeout/downstream/contract 等） |
| 算子强度单调不减 | Operation Strength Monotonic | 调用方请求的算子只允许上调保护强度 |
| 出域留痕绑定 | Egress-Evidence Binding | 每次脱敏数据出域必须关联一条存证记录 |
| BFF 聚合代理 | BFF Aggregation Proxy | 前端专用后端，聚合多个上游服务的 API |
| 命名归一化 | Naming Normalization | 将各种词表统一映射到规范标识符 |
| 写只角色 | Write-Only Role | PostgreSQL 仅授予 INSERT/SELECT 权限的数据库角色 |
| 独立验真 | Independent Verification | 无需访问数据库，仅凭段文件 + 清单 + 密钥验证归档完整性 |

---

## 第 13 章：Review 检查清单详细版

### audit-log 服务检查清单

#### `cmd/server/main.go`

- [ ] 配置加载后立即校验（`cfg.Validate()`）
- [ ] 信封加密密钥版本注册先于存储初始化
- [ ] SM2 签名器未配置时跳过（不影响哈希链）
- [ ] 存证密钥为空时 Warn 而非 Fatal
- [ ] SQLite 启动时完整性校验
- [ ] 存储分层降级：PostgreSQL → SQLite → 内存
- [ ] Write-Only PostgreSQL 自检覆盖 UPDATE/DELETE
- [ ] BufferedAuditStore 装饰器包装
- [ ] 留存清理协程：`RetentionDays=0` 时不启动
- [ ] 优雅停机：gRPC 30s 超时 → 强制停止

#### `internal/archive/archive.go`

- [ ] 目录/密钥缺失时 fail-closed（`ErrMissingDir`/`ErrMissingKey`）
- [ ] 分页循环上限 `maxArchivePages=100000`
- [ ] 归档段写入后立即 `VerifySegment` 验真
- [ ] 验真失败 → 不删除（`"deletion refused"`）
- [ ] 删除返回 0 → `ErrNotDeleted` 中止
- [ ] 行哈希链：`chain[i] = SM3(chain[i-1] || line[i])`
- [ ] `writeFsync`：`O_EXCL` + `fsync` 文件 + 目录
- [ ] `resolveInDir` 路径穿越防护
- [ ] 段文件名防覆盖（序号递增 + `Stat` 检查）

### service-hub 服务检查清单

#### `internal/handlers/handlers.go`

- [ ] `processTask` 信号量限流（`taskSem` 容量 10）
- [ ] Panic 安全恢复：`recover()` → 任务 failed + 持久化
- [ ] 每阶段前检查优雅停机信号（`s.ctx.Done()`）
- [ ] ③ classify 阶段：15s 超时 + 幂等键
- [ ] ⑥ audit 阶段：提交失败 → 任务 failed（P0-6）
- [ ] `EffectiveOperation` 算子强度单调不减
- [ ] `scopeAuthMiddleware` 常量时间 Key 查找
- [ ] 过期 Key 不得使用（`matched.IsExpired()`）
- [ ] `Dispatch` 数据源 ID 归一化（`naming.ResolveInbound`）
- [ ] PostgreSQL 模式由租约 worker 消费，非本地 goroutine

#### `internal/audit/client.go`

- [ ] 三条红线：失败即任务失败 / 禁止伪造链头 / 真实指纹
- [ ] 多端点支持（`SERVICE_HUB_AUDIT_LOG_URLS` 逗号分隔）
- [ ] 重试策略：指数退避 + 随机抖动（500ms base, 3 retries）
- [ ] 响应体大小限制（4 MiB）
- [ ] `ErrNotConfigured` 哨兵错误

#### `internal/retry/retry.go`

- [ ] `Classify` 使用 `errors.Is`/`errors.As`（不匹配文案）
- [ ] `retryableClasses` 表完整（每个分类都有重试判定）
- [ ] `BiasDownstream` 未知错误按瞬时处理
- [ ] `BiasConservative` 未知错误按内部故障

### app-lz BFF 层检查清单

- [ ] `ClientPool` 连接池配置合理（100 idle, 25 per host）
- [ ] `setHeaders` 注入 `X-Request-ID` + `Authorization`
- [ ] 降级数据标记 `sourceFallback`
- [ ] JWT 认证中间件（`authEnabled=false` 时放行）
- [ ] gzip 响应压缩
- [ ] WAF 中间件（G-12）
- [ ] 32 MiB 请求体限制
- [ ] 1000 并发上限

---

## 第 14 章：周交付物清单

### 交付物 1：audit-log 与 service-hub Review 笔记

- [ ] audit-log 启动流程与依赖装配分析
- [ ] 归档留存红线（archive.go）设计原理报告
- [ ] service-hub 6 阶段流水线 processTask 完整执行路径分析
- [ ] 算子强度单调不减约束（P1-1）安全性分析
- [ ] 存证客户端三条红线设计原理
- [ ] 错误分类与结构化重试机制分析
- [ ] 崩溃恢复与租约过期回收机制分析

### 交付物 2：发现的问题清单与改进建议

| 优先级 | 问题 | 位置 | 状态 | 建议 |
|---|---|---|---|---|
| P0 | audit 客户端未配置时任务必然 failed | `handlers.go:99` | 设计决策 | 文档明确说明 fail-closed 语义 |
| P1 | 本地 worker 500ms 轮询间隔可能延迟任务启动 | `handlers.go:131` | 待优化 | 可考虑事件驱动通知 |
| P1 | BFF 降级数据无显式标记时前端难以区分 | `clients/clients.go` | 待评估 | 统一 `source` 字段标记 |
| P2 | 归档段文件名序号上限 100 万 | `archive.go:294` | 设计决策 | 极端场景下可能需要扩展 |
| P2 | processTask 信号量容量硬编码 10 | `handlers.go:106` | 待配置化 | 建议通过环境变量配置 |

### 交付物 3：跨服务集成测试报告

- [ ] 全栈端到端链路验证（hub → engine → audit-log）
- [ ] 存证证据链一致性验证（integrity_hash 跨服务比对）
- [ ] 命名归一化一致性验证（安全级别标识跨服务统一）
- [ ] 错误信封一致性验证（5 字段信封所有服务统一使用）
- [ ] 超时传播链路验证

---

## 附录 A：关键环境变量速查表

### audit-log 环境变量

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `AUDIT_LOG_PORT` | `8084` | HTTP REST 端口 |
| `AUDIT_LOG_GRPC_PORT` | `50054` | gRPC 端口 |
| `AUDIT_LOG_DB_PATH` | `./audit.db` | SQLite 数据库路径 |
| `AUDIT_LOG_PG_DSN` | `""` | PostgreSQL DSN |
| `AUDIT_LOG_HASH_KEY` | `""` | HMAC-SM3 存证密钥 |
| `AUDIT_LOG_ENCRYPTION_KEY` | `""` | SM4-GCM 加密密钥 |
| `AUDIT_LOG_RETENTION_DAYS` | `0` | 存证保留天数（0=永不清理） |
| `AUDIT_LOG_ARCHIVE_DIR` | `./archives` | 归档段存储目录 |
| `AUDIT_LOG_STRICT_STORAGE` | `false` | 严格存储模式（不降级） |
| `AUDIT_LOG_DB_WRITE_ONLY` | `false` | PostgreSQL 写只角色自检 |

### service-hub 环境变量

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `SERVICE_HUB_PORT` | `8082` | HTTP REST 端口 |
| `SERVICE_HUB_GRPC_PORT` | `50052` | gRPC 端口 |
| `AGENT_REST_HOST` | `127.0.0.1` | engine-go REST 地址 |
| `AGENT_REST_PORT` | `8079` | engine-go REST 端口 |
| `SERVICE_HUB_DATASOURCE_URL` | `http://127.0.0.1:8083` | datasource-mgr 地址 |
| `SERVICE_HUB_AUDIT_LOG_URLS` | `""` | audit-log 存证端点（逗号分隔多端点） |
| `SERVICE_HUB_DB_PATH` | `./hub.db` | SQLite 任务存储路径 |
| `SERVICE_HUB_API_KEY` | `""` | 单 API Key（向后兼容） |
| `SERVICE_HUB_API_KEYS` | `""` | Scope-based 多 Key JSON |

---

## 附录 B：服务间调用关系全景图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        全栈服务调用关系全景图                             │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌──────────────────┐                                                   │
│  │  React Web (:5173)│                                                  │
│  └────────┬─────────┘                                                   │
│           │ HTTP/JSON                                                   │
│  ┌────────▼─────────────────────────────────────────────────────────┐  │
│  │  app-lz BFF (:8081)                                              │  │
│  │  ├── JWT + TOTP 认证                                             │  │
│  │  ├── ClientPool（4 上游 HTTP 客户端）                             │  │
│  │  └── 降级 fallback（sourceFallback 标记）                        │  │
│  └──┬──────────┬──────────┬──────────┬──────────────────────────────┘  │
│     │          │          │          │                                  │
│     ▼          ▼          ▼          ▼                                  │
│  ┌──────┐  ┌──────┐  ┌──────┐  ┌──────────┐                          │
│  │ hub  │  │audit │  │ ds   │  │ engine   │                          │
│  │:8082 │  │:8084 │  │:8083 │  │ :8079    │                          │
│  └──┬───┘  └──────┘  └──────┘  └──────────┘                          │
│     │                                                                  │
│     ├──▶ datasource-mgr (:8083) ──── fetch 阶段                       │
│     ├──▶ engine-go (:8079) ─────────── classify + desensitize 阶段     │
│     └──▶ audit-log (:8084) ─────────── audit 阶段（P0-6 强绑定）       │
│                                                                         │
│  pkg 层共享基础设施：                                                    │
│  ├── pkg/crypto (SM3/SM4/HKDF)                                        │
│  ├── pkg/store (AuditStore/TaskStore/flusher)                          │
│  ├── pkg/middleware (auth/ratelimit/WAF/security headers)               │
│  ├── pkg/auth (Identity/KeyStore/Scope)                                │
│  └── pkg/naming (归一化/词表一致性)                                     │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 附录 C：REST API 路由清单

### audit-log REST API

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/health` | 无 | 存活探针 |
| GET | `/readyz` | 无 | 就绪探针 |
| POST | `/api/audit/logs` | `audit:write` | 创建审计日志 |
| GET | `/api/audit/logs` | `audit:read` | 分页查询 |
| GET | `/api/audit/logs/:id` | `audit:read` | 查询单条 |
| GET | `/api/audit/stats` | `audit:read` | 统计聚合 |
| GET | `/api/audit/snapshots` | `audit:read` | 查询快照 |
| POST | `/api/audit/snapshots/verify` | `audit:read` | 验真快照 |
| GET/POST | `/api/audit/chain/verify` | `audit:read` | 哈希链核验 |
| GET | `/metrics` | 无 | Prometheus |

### service-hub REST API

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/health` | 无 | 存活探针 |
| GET | `/readyz` | 无 | 就绪探针 |
| GET | `/api/hub/status` | `hub:read` | 中枢状态 |
| GET | `/api/hub/tasks` | `hub:read` | 任务列表 |
| GET | `/api/hub/tasks/:id` | `hub:read` | 任务详情 |
| POST | `/api/hub/dispatch` | `hub:write` | 分发任务 |
| GET | `/api/hub/pipeline` | `hub:read` | 流水线状态 |
| GET | `/metrics` | 无 | Prometheus |

### app-lz BFF REST API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/health` | BFF 健康检查 |
| GET | `/api/lz/topology` | 服务拓扑探测 |
| GET | `/api/lz/pipeline` | 流水线状态 |
| GET | `/api/lz/tasks` | 任务列表 |
| POST | `/api/lz/tasks/dispatch` | 分发任务 |
| GET | `/api/lz/audit/logs` | 审计日志 |
| POST | `/api/lz/audit/verify` | 归档验真 |
| POST | `/api/auth/login` | 用户登录 |
| POST | `/api/auth/register` | 用户注册 |

---

## 附录 D：推荐阅读与延伸阅读

### 必读

1. **《设计数据密集型应用》(DERTA)** — Martin Kleppmann，分布式系统基础
2. **PostgreSQL `FOR UPDATE SKIP LOCKED` 文档** — 理解租约抢占的 SQL 语义
3. **RFC 7231 HTTP/1.1 Semantics** — 理解 202 Accepted 语义（异步任务接受）
4. **Go `signal.NotifyContext` 文档** — 理解优雅停机的最佳实践

### 选读

5. **《Go 并发编程实战》** — 理解信号量、WaitGroup、Context 取消模式
6. **NDJSON 格式规范** — [ndjson.org](http://ndjson.org/)
7. **SM4-GCM 安全性分析** — NIST SP 800-38D
8. **OpenTelemetry Specification** — 理解分布式追踪的 W3C TraceContext 传播

### 在线资源

9. [PostgreSQL SKIP LOCKED 演示](https://www.2ndquadrant.com/en/blog/what-is-select-skip-locked-and-for-update-skip-locked/) — 可视化理解行级锁跳过
10. [gRPC Keepalive 参数详解](https://github.com/grpc/grpc/blob/master/doc/keepalive.md) — 理解 MaxConnectionAge 与 MaxConnectionAgeGrace 的配合
