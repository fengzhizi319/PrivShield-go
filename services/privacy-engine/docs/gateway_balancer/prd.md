# 代理转发与负载均衡网关产品需求文档 (PRD)

---

## 1. 概述

本文档定义 `PrivShield` 代理转发与自适应负载均衡网关（API Gateway & Load Balancer）的产品需求、核心特性与验收标准。
网关基于 **纯 Go 1.25+ 云原生架构** 实现，作为统一接入入口，负责将南北向 REST 与 gRPC 流量高效、智能地分发至后端多个 `PrivShield Agent` 治理节点，实现微秒级自适应调度、故障自愈与线性水平扩展。

---

## 2. 设计目标

- **双协议透明转发**：无缝代理所有 REST HTTP/1.1 & HTTP/2 路由与 gRPC RPC 方法，对上游客户端完全透明。
- **自适应智能调度**：默认采用 **P2C-EWMA**（两选择随机 + 指数移动加权平均延迟），结合实时在途连接（InFlight）与历史响应延迟智能避让慢节点；同时支持 Nginx 平滑加权轮询（SWRR）、最小连接数等策略。
- **极速低损耗转发**：引入 `byteBufferPool`（32KB `sync.Pool`）与 `rawCodec` 零编解码流式转发，消除内存频繁分配与序列化双重开销，代理额外耗时控制在 **≤ 0.5ms**。
- **故障隔离与三态自愈**：每个后端节点配备独立的三态熔断器（`Closed` → `Open` → `HalfOpen`），毫秒级感知单点故障并实现半开试探自愈。
- **安全与可观测性**：支持东西向零信任 mTLS 回源双向加密，暴露 Prometheus `/metrics` 遥测指标与 `/gateway/backends` 实时拓扑查询。

---

## 3. 用户场景

| 场景 | 说明 |
|---|---|
| **超万级 QPS 批量脱敏** | 网关承接高并发批量脱敏与字段遮盖请求，利用 P2C-EWMA 分发至各 Agent 计算节点。 |
| **高频差分隐私聚合** | 多业务方并发调用 DP Count / DP Sum / DP Mean，网关依据节点实时在途负载均匀路由。 |
| **异构算力节点混合调度** | 集群中存在不同规格节点（如高配 CPU 节点 vs 普通节点），通过 SWRR 平滑加权调度避免请求倾斜。 |
| **节点瞬时崩溃自愈** | 后端 Agent 出现 OOM 或网络波动时，网关熔断器毫秒级熔断隔离，冷却期后自动试探恢复。 |
| **东西向金融级加密回源** | 银行/医疗等高安全等级场景下，网关与后端 Agent 通过 mTLS 双向证书建立加密隧道。 |

---

## 4. 功能需求

### 4.1 协议转发与代理能力

| ID | 需求描述 | 实现方式 |
|---|---|---|
| **GW-REST-1** | 透明代理 `/v1/privacy/*`、`/v1/agent/*`、`/v1/medical/*` 等所有 HTTP 端点 | `gin.NoRoute` + `httputil.ReverseProxy` |
| **GW-REST-2** | 32KB `sync.Pool` 缓冲区复用，避免每次转发分配临时内存 | `httputil.BufferPool` |
| **GW-REST-3** | 共享长连接池与 Keep-Alive 连接复用（最大 2048 空闲连接） | `http.Transport`（`MaxIdleConns: 2048`, `MaxIdleConnsPerHost: 256`） |
| **GW-GRPC-1** | 实现 gRPC 透明流代理，无需维护 proto 反射，支持所有当前与未来 RPC 方法 | `grpc.UnknownServiceHandler` |
| **GW-GRPC-2** | 原始字节透传（零拷贝 / 零编解码开销） | 自定义 `rawCodec`（透传 `[]byte`） |
| **GW-GRPC-3** | 双向流并发代理与 Metadata 上下文传递（Trace ID / Auth 凭证透传） | `clientStream` 与 `serverStream` 双向转发 |

### 4.2 负载均衡策略

| ID | 策略名称 | 标识符 | 算法说明 |
|---|---|---|---|
| **GW-LB-1** | **幂律双选 + EWMA (推荐)** | `p2c` | 随机挑选 2 个健康节点，比较综合得分 $\text{Score} = (\text{InFlight}+1) \times \max(\text{EWMA}, 0.001)$，路由至较小者。 |
| **GW-LB-2** | **平滑加权轮询** | `weighted_rr` | Nginx SWRR 算法，节点动态累计权重并削减总权重，确保高权重节点离散平滑。 |
| **GW-LB-3** | **最小在途连接数** | `least_conn` | 实时追踪节点当前活跃连接数，优先路由至在途数最小的节点。 |
| **GW-LB-4** | **简单轮询** | `round_robin` | 依次循环分发请求至各健康节点。 |
| **GW-LB-5** | **加权随机** | `weighted_random` | 按照节点配置权重比例进行概率抽样。 |

### 4.3 故障隔离与自愈（三态熔断器）

| ID | 状态 / 行为 | 说明 |
|---|---|---|
| **GW-CB-1** | **正常态 (`Closed`)** | 正常转发请求；若连续失败次数达到阈值（默认 5 次），状态跃迁为 `Open`。 |
| **GW-CB-2** | **熔断态 (`Open`)** | 立即阻断流量并返回 HTTP 503 / gRPC UNAVAILABLE；进入冷却期（默认 30s）。 |
| **GW-CB-3** | **半开试探态 (`HalfOpen`)** | 冷却期结束后进入半开状态，允许限定试探请求（默认 3 次）；若连续试探成功则复位为 `Closed`，若失败则重新回到 `Open`。 |
| **GW-CB-4** | **无可用节点兜底** | 当所有后端节点均熔断时，返回 HTTP 503 / gRPC UNAVAILABLE，防止雪崩。 |

### 4.4 配置体系

| ID | 需求描述 | 实现方式 |
|---|---|---|
| **GW-CFG-1** | 支持 YAML 文件配置与环境变量无缝覆盖 | `config/gateway.yaml` + `GATEWAY_*` 环境变量 |
| **GW-CFG-2** | 支持配置监听地址与端口（REST 默认 `:8000`，gRPC 默认 `:50000`） | `GATEWAY_HOST`, `GATEWAY_PORT`, `GATEWAY_GRPC_PORT` |
| **GW-CFG-3** | 支持配置后端节点池（逗号分隔地址列表，如 `10.0.1.10:8079,10.0.1.11:8079`） | `GATEWAY_BACKENDS` |
| **GW-CFG-4** | 支持指定调度策略（`p2c` / `weighted_rr` / `least_conn` / `round_robin` / `weighted_random`） | `GATEWAY_STRATEGY` |

### 4.5 东西向零信任 mTLS 回源

| ID | 需求描述 | 说明 |
|---|---|---|
| **GW-TLS-1** | 支持网关到 Agent 之间 mTLS 双向加密回源 | 加载 CA 证书校验后端，并提供网关客户端证书 |
| **GW-TLS-2** | 支持指定最低 TLS 版本（默认 TLS 1.3，兼容 TLS 1.2） | `BuildBackendTLSConfigWithMinVersion` |
| **GW-TLS-3** | 支持非加密模式（用于开发测试环境） | `BuildInsecureBackendTLSConfig` |

### 4.6 监控与可观测性

| ID | 需求描述 | 说明 |
|---|---|---|
| **GW-OBS-1** | 网关自身健康探针 | `GET /health` 返回 `{"status":"ok","component":"gateway"}` |
| **GW-OBS-2** | 后端节点拓扑与状态查询 | `GET /gateway/backends` 返回节点地址、在途连接数、EWMA 延迟与熔断器状态 |
| **GW-OBS-3** | Prometheus 标准指标导出 | `GET /metrics` 导出 `gateway_requests_total`、`gateway_request_duration_seconds` 等指标 |

---

## 5. 非功能需求

| 维度 | 指标 / 要求 |
|---|---|
| **转发延迟** | 网关额外转发耗时 P99 ≤ 0.5ms。 |
| **吞吐能力** | 单网关实例支撑 **≥ 50,000 QPS**（基准压测下零崩溃、零内存泄漏）。 |
| **内存开销** | 常驻内存 ≤ 30MB，`byteBufferPool` 零垃圾回收压力。 |
| **优雅停机** | 捕获 `SIGINT`/`SIGTERM`，HTTP 15s 超时安全收尾，gRPC `GracefulStop` 安全关闭。 |

---

## 6. 验收标准

- [x] REST HTTP/1.1 & HTTP/2 反向代理与 gRPC 透明流代理正常工作。
- [x] 5 种负载均衡策略（P2C-EWMA、SWRR、LeastConn、RR、WeightedRandom）单元测试 100% 通过。
- [x] 三态熔断器（`Closed` → `Open` → `HalfOpen`）状态转移与并发安全测试 100% 通过。
- [x] Prometheus `/metrics` 与 `/gateway/backends` 查询端点测试通过。
- [x] 东西向 mTLS 回源配置加载与 TLS 1.3 证书验证测试通过。
- [x] 全模块编译（`make build`）与全套测试（`make test`）100% 成功。