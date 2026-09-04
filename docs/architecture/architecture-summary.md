# PrivShield 架构设计与工程实践总结 (Architecture & Engineering Summary)

> **版本**：v16.0.0 (Go 1.25+ Cloud-Native)  
> **适用范围**：`PrivShield` 核心算力引擎（`services/privacy-engine`）、企业级中台微服务群（`services/service-hub` / `services/audit-log`）、控制台与数据源生态（`console/engine-console` / `console/app-lz` / `console/mock-datasource`）及全局云原生基础设施。  
> **关联文档**：[unified_design_specifications.md](unified_design_specifications.md)（全栈统一设计规范）、[new_api_design.md](new_api_design.md)（新增数据接口扩展 SOP）、[architecture-design.md](architecture-design.md)（详细架构设计）、[production_optimization_design.md](production_optimization_design.md)（生产级优化设计）。

---

## 目录

- [一、项目定位与系统全景](#一项目定位与系统全景)
- [二、核心设计哲学与标准实践](#二核心设计哲学与标准实践)
  - [2.1 纯 Go 云原生 Monorepo 架构](#21-纯-go-云原生-monorepo-架构)
  - [2.2 双栈同源协议支持 (REST + gRPC)](#22-双栈同源协议支持-rest--grpc)
  - [2.3 纵深安全防御体系与国密合规](#23-纵深安全防御体系与国密合规)
  - [2.4 全链路可观测性三支柱](#24-全链路可观测性三支柱)
  - [2.5 无锁原子隐私预算会计](#25-无锁原子隐私预算会计)
  - [2.6 企业级中台微服务群实践](#26-企业级中台微服务群实践)
- [三、核心高光工程设计](#三核心高光工程设计)
  - [3.1 三层递进式动态分类分级漏斗 (3-Layer Funnel)](#31-三层递进式动态分类分级漏斗-3-layer-funnel)
  - [3.2 客户端去中心化负载均衡与网关 P2C-EWMA 零分配反向代理](#32-客户端去中心化负载均衡与网关-p2c-ewma-零分配反向代理)
  - [3.3 9 层统一中间件栈与纵深防 DDoS 体系](#33-9-层统一中间件栈与纵深防-ddos-体系)
- [四、工程注意事项与避坑指南](#四工程注意事项与避坑指南)
- [五、可复用设计模式清单](#五可复用设计模式清单)

---

## 一、项目定位与系统全景

PrivShield 是一个**企业级数据安全流通与隐私治理 Sidecar / 中台系统**，实现**「三层四柱五御六类」**安全治理体系：
- **算力面 (PrivShield Core & Primitives)**：纯 Go 1.25+（`services/privacy-engine`，内置 `sdk/` 隐私数学原语库）实现的零内存分配、多核分块并发隐私原语（国密 SM3/SM4、脱敏、差分隐私、K-匿名与 L-多样性、查询混淆）与 3 层动态分类分级漏斗（Rule → Small-NER → External LLM），并提供离线 LoRA 专精微调流水线（`services/privacy-engine/model-training/llmlora`）；
- **调度面 (Enterprise Services)**：Go 1.25 微服务集群与测试生态（`services/service-hub`、`services/audit-log`、`console/mock-datasource`）负责多源数据资产管理与模拟、6 阶段流水线任务编排调度、PostgreSQL 原子租约并发与 9 要素密码学防篡改存证；
- **展现面 (Console & BFF)**：双控制台体系（`console/engine-console` 专测引擎、`console/app-lz` 专测调度编排）与 React 18 + TypeScript 现代化测试控制台群。

---

## 二、核心设计哲学与标准实践

### 2.1 纯 Go 云原生 Monorepo 架构

```text
PrivShield/ (Repo Root)
├── services/                          # 商业化生产微服务群 (Production Services)
│   ├── privacy-engine/                # 核心隐私计算与动态分类分级引擎 (Core Sidecar/Agent)
│   │   ├── cmd/
│   │   │   ├── privshield-agent/      # Agent 主入口 (REST :8079 + gRPC :50051)
│   │   │   └── privshield-gateway/    # 网关与反向代理入口 (:8000 + gRPC :50000)
│   │   ├── internal/                  # 动态分类分级、网关代理、安全认证、画像等
│   │   ├── sdk/                       # 内置隐私计算数学原语库 (Masking, DP, LDP, K-Ano, Medical, Budget)
│   │   ├── rules/                     # 领域敏感特征规则库 (Taxonomies, Domains, Standards)
│   │   ├── model-training/llmlora/    # 领域大模型/NER 离线微调与量化流水线 (Python/PEFT)
│   │   ├── docs/ deploy/ scripts/ Makefile # 自包含交付资产
│   ├── service-hub/                   # 数联数据服务调度中枢 · 唯一编排入口 (流水线调度: :8082 / :50052)
│   │   ├── docs/ deploy/ scripts/ Makefile # 自包含交付资产
│   └── audit-log/                     # 脱敏审计日志与不可篡改存证服务 (:8084 / :50054)
│       ├── docs/ deploy/ scripts/ Makefile # 自包含交付资产
├── console/                           # 测试与管理生态 (Testing & Management Consoles)
│   ├── engine-console/                # Privacy Engine 专属管理控制台 (专测 privacy-engine)
│   │   ├── bff-go/                    # Engine Console BFF (:8081 / :50055)
│   │   ├── web/                       # Engine Console Web 前端 (React 18 + TS + Vite :5173)
│   │   ├── docs/ deploy/ scripts/ Makefile # 自包含交付资产
│   ├── app-lz/                        # 数联调度之眼业务模拟器 (专测 service-hub 调度编排)
│   │   ├── bff-go/                    # 业务专有 BFF (:8085，所有数据请求统一走 service-hub)
│   │   ├── web/                       # 业务流水线控制台前端 (React 18 + TS + Vite :5174)
│   │   ├── docs/ deploy/ scripts/ Makefile # 自包含交付资产
│   └── mock-datasource/               # 模拟多源异构数据源服务 (:8083 / :50053)
│       ├── docs/ deploy/ scripts/ Makefile # 自包含交付资产
├── pkg/                               # Go 全局共享基础库 (naming, middleware, store, crypto, tlsutil, metrics, validation, agent)
├── deploy/                            # 全栈集中部署套件 (Helm, K8s, Compose, Prometheus, Grafana)
├── config/                            # 全局运行时配置与 mtls-whitelist.yaml
└── scripts/                           # 全局自动化运维、启动、测试与数据迁移脚本
```

### 2.2 双栈同源协议支持 (REST + gRPC)

```text
REST (Gin, :8079)      ←→  PrivacyService (统一编排业务中枢)  ←→  纯 Go 零依赖数学原语
gRPC (RawCodec, :50051) ←→  PrivacyService (统一编排业务中枢)  ←→  纯 Go 零依赖数学原语
```
- Protobuf 契约定义在 `proto/privacy.proto`；
- REST 与 gRPC 共享同一底层 `PrivacyService`，保证跨协议行为 100% 一致；
- Agent 统一由 `services/privacy-engine/cmd/privshield-agent` 启动，原生同时监听 REST (`:8079`) 与 gRPC (`:50051`)；
- 网关反向代理统一由 `services/privacy-engine/cmd/privshield-gateway` 启动，监听 REST (`:8000`) 与 gRPC (`:50000`)。

### 2.3 纵深安全防御体系与国密合规

| 层次 | 实现机制 | 说明 |
|---|---|---|
| **传输加密** | TLS 1.3 / mTLS | 支持服务端证书与双向客户端证书校验；gRPC 服务端统一注册 `pkg/tlsutil` 的 `NewWhitelistInterceptor()` 拦截器，按 `PRIVACY_AUTH_MTLS_WHITELIST_FILE` 加载 `config/mtls-whitelist.yaml`，5 秒 mtime 轮询热重载 |
| **访问认证** | API Key (Bearer Token) | 内外部 API Key 独立隔离，常量时间比对 (`subtle.ConstantTimeCompare`)，Fail-Closed 零信任拦截 |
| **国密支持** | SM3 哈希 / SM4-GCM 加密 | GB/T 32918.4 密码哈希与 GB/T 32907 对称加密，快照持久化数据带 `enc:v2:` 前缀加密（HKDF 派生） |
| **存证安全** | 9 要素哈希链 | 区块链式链式锚定，秒级在线核验防篡改 |
| **流量限速** | 32 分片高并发令牌桶 | 消除锁竞争，支持 IP/租户级别独立限流，防单点资源耗尽 |
| **并发保护** | 并发信号量 (MaxConcurrent) | 全局在途请求上限拦截（503），保护协程池与连接池 |

### 2.4 全链路可观测性三支柱

| 支柱 | 技术选型 | 说明 |
|---|---|---|
| **Metrics** | Prometheus `/metrics` | 统一导出 40+ 业务与运行时指标（原语耗时、分类命中率、预算消耗、Goroutine 数量等） |
| **Tracing** | OpenTelemetry (OTLP) / TraceID | `X-Request-ID` / `X-Trace-ID` 双头传递，Span 树全链路关联 |
| **Logging** | 结构化 `log/slog` (JSON/Text) | `trace_id` 全链路自动注入，支持敏感字段上下文拦截 |

### 2.5 无锁原子隐私预算会计

- **无锁 CAS 循环**：`BudgetAccountant` 基于 `sync/atomic` 与 `math.Float64bits` 实现单机千万级 QPS 原子记账与原子回滚；
- **滑动时间窗口重置**：配置 `PRIVACY_BUDGET_WINDOW_SECONDS` 实现自动周期重置；
- **不可篡改 HMAC/SM3 审计**：对每笔预算消耗记录进行 HMAC-SHA256 / SM3 签名存证。

### 2.6 企业级中台微服务群实践

各服务使用独立前缀的环境变量控制 HTTP/gRPC/TLS 等运行参数（`SERVICE_HUB_*`、`DATASOURCE_MGR_*`、`AUDIT_LOG_*`、`PRIVACY_CONSOLE_*`），并共享 `PRIVACY_AUTH_MTLS_WHITELIST_FILE`、`PRIVACY_AGENT_*`、`PRIVACY_REST_PORT` 等全局配置。

- **`service-hub` (:8082 / :50052)**：流水线 6 阶段调度编排（`Ingest` ➔ `Fetch` ➔ `Classify` ➔ `Desensitize` ➔ `Return` ➔ `Audit`）与 Worker Pool 异步削峰；
  - **PostgreSQL 租约并发**：`LeasedTaskStore` 基于 `FOR UPDATE SKIP LOCKED` 实现多副本并发抢占与防脑裂；
  - **崩溃恢复与自动重试**：启动时回收孤立任务，周期性后台指数退避自动重试；
  - 📖 [可靠性能力详解](../../services/service-hub/docs/reliability.md)
- **`mock-datasource` (:8083 / :50053)**：多源异构资产纳管与测试模拟、内置医保与康养模拟库（`yibao.csv` & `kangyang.csv`）、动态元数据自动探查与样本切片安全提取；
  - 📖 [可靠性能力详解](../../console/mock-datasource/docs/reliability.md)
- **`audit-log` (:8084 / :50054)**：基于 9 要素特征的不可篡改 SHA-256 / SM3 存证哈希链与 SM4-GCM 快照加密；
  - **在线核验**：`POST /v1/audit/chain/verify` 接口实时定位断裂节点；
  - 📖 [可靠性能力详解](../../services/audit-log/docs/reliability.md)

---

## 三、核心高光工程设计

### 3.1 三层递进式动态分类分级漏斗 (3-Layer Funnel)

```text
Layer 1: YAML 规则引擎 (10~50μs) → Aho-Corasick 自动机 + 正则/词典/组合条件/Safety Floor 过滤 85%+ 明确数据
  ↓ (未命中或低置信)
Layer 2: Small-NER 引擎 (1~5ms)   → ONNX Runtime Go 抽取中文专有实体（跳过纯数字与英文字段）
  ↓ (语义存疑/规则冲突/多模态图像)
Layer 3: External LLM 仲裁 (100~500ms) → HTTP 连接池 + 三态熔断器调度独立 vLLM/Ollama (Closed/Open/HalfOpen)
```

### 3.2 客户端去中心化负载均衡与网关 P2C-EWMA 零分配反向代理

- **Service Hub 出站多节点矩阵 (`pkg/agent/client.go` / `datasource/client.go`)**：
  - 原生支持 `PRIVACY_AGENT_URLS`、`DATASOURCE_MGR_URLS` 与 `SERVICE_HUB_AUDIT_LOG_URLS` 多实例列表；
  - 内置无锁原子 Round-Robin 轮询调度与按节点独立三态熔断（Per-Node Circuit Breaker）；
  - 遇到节点崩溃或 5xx 故障自动触发带抖动的指数退避，并在重试轮次透明故障转移（Failover）至健康节点；
- **P2C-EWMA 服务端负载均衡 (`services/privacy-engine/internal/gateway/balancer.go`)**：Power of Two Choices 算法结合指数加权移动平均（EWMA）延迟与在途请求动态打分，消除羊群效应；
- **BufferPool 零分配反向代理 (`services/privacy-engine/internal/gateway/http_proxy.go`)**：基于 `sync.Pool` 复用 32KB 数据包切片，实现反向代理数据流转发 0 堆内存分配；
- **gRPC 零反序列化流代理 (`services/privacy-engine/internal/gateway/grpc_proxy.go`)**：采用 `rawCodec` 直通模式，避免 Proto 编解码 CPU 损耗。

### 3.3 9 层统一中间件栈与纵深防 DDoS 体系

所有 Go 服务统一装配 9 层中间件栈：
```text
TraceMiddleware ➔ StructuredLogger ➔ Recovery ➔ SecurityHeaders ➔ MaxBodySize ➔ MaxConcurrent ➔ RateLimit ➔ CORS ➔ Auth
```
- **慢速连接防护 (Anti-Slowloris)**：配置 `ReadHeaderTimeout(5s)`、`ReadTimeout(30s)` 与 `MaxHeaderBytes(1MB)`；
- **请求体上限 (Payload Protection)**：`MaxBodySize` 限制 32MB/64MB 硬顶拦截（413）；
- **IP 令牌桶限流 (HTTP Flood)**：`RateLimit` 基于 IP 提供 32 分片高精度令牌桶，超额返回 429；
- **并发容量熔断 (Concurrency Cap)**：`MaxConcurrent` 实施全局在途并发信号量拦截（503），保护协程池。

---

## 四、工程注意事项与避坑指南

1. **探针不设防**：`/healthz` 与 `/readyz` 探针路由严禁挂载认证/限流中间件，防止 K8s 存活检查因无 Token 而导致容器被异常重启；
2. **Context 超时传递**：所有计算密集型批量 API 必须调用 `MaskBatchContext(ctx, ...)`，支持客户端超时快速中断，防止 CPU 空跑；
3. **压缩炸弹与目录穿越防御**：文件解析必须采用 `io.LimitReader` 设定最大展开阈值（如 256MB），医学影像路径必须通过 `isPathAllowed` 白名单校验；
4. **单副本与多副本持久化约束**：SQLite 仅支持单副本 `Recreate` 部署；多副本 Hub 必须基于 PostgreSQL `LeasedTaskStore` 运行，禁止在共享网络卷上挂载 SQLite。

---

## 五、可复用设计模式清单

| 模式名称 | 应用场景 | 本项目代表性实现 |
|---|---|---|
| **Sidecar Pattern** | 云原生高性能服务化 | 独立部署提供 REST (:8079) + gRPC (:50051)，常驻内存 ~25MB |
| **Funnel Pattern** | 递进式智能分级 | Rule (10μs) ➔ NER (1ms) ➔ External LLM (100ms) |
| **Graceful Degradation** | 可选重依赖解耦 | LLM/NER 缺失时回退规则层与人工审核标记，熔断器自动半开恢复 |
| **P2C-EWMA** | 动态负载均衡 | 随机选取两节点对比 EWMA 延迟与在途连接打分分流 |
| **BufferPool Pooling** | 零内存分配网关 | `sync.Pool` 复用 32KB 缓冲，反代转发 0 B/op |
| **Fused Vector DP** | 单趟融合计算 | 单次迭代融合灵敏度计算与噪声注入，无多余中间数组分配 |
| **Chunked Concurrency** | 多核无锁分块并发 | 数据切片按 `GOMAXPROCS` 无锁分块，多核并行吞吐 10x |
| **Leased Task Pattern** | 分布式无锁抢占 | `pkg/store/postgres` `FOR UPDATE SKIP LOCKED` 原子租约 |
| **Envelope Encryption** | 敏感静态数据落盘 | `pkg/crypto` SM4-GCM `enc:v2:...` |
| **Cryptographic Hash Chain** | 存证防篡改审计 | `services/audit-log` 9 要素区块链式哈希链 |
| **Single Source of Truth** | 全栈业务命名一致性 | `pkg/naming` 常量注册表与别名归一化 |