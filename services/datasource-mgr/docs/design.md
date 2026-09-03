# 模拟数据源服务 (Mock Datasource Manager) — 详细设计文档

> 本文档定义 **数联天下 · 数盾 (`PrivShield`)** 模拟数据源模块（`services/datasource-mgr`）的系统架构、固定模拟数据库、API 1~4 接口设计、双协议支持（REST + gRPC）与国密 SM2 / TLS 1.3 双向认证。

---

## 1. 定位与设计原则

### 1.1 业务定位
`datasource-mgr` 专为 **开发、联调、集成测试与合规演练** 设计，作为轻量级、高保真的模拟数据提供者。
- **开发/联调期**：提供严格对齐 **DB51/T 2989—2023** 标准的模拟样本数据（医保 `yibao.csv` 19 字段、康养 `kangyang.csv` 27 字段及预留接口 3/4）；
- **生产运行期**：调度中枢及业务服务可直接对接局方真实数据底座（如专有 VPC 子网内的 MySQL/PostgreSQL/Oracle/DataMesh），无需在生产环境中部署多源异构探查等重型测试中间件。

### 1.2 核心特性
1. **双协议通信与全链路安全**：对外提供 HTTP/HTTPS REST (`:8083`) 与高性能 gRPC (`:50053`)；
2. **国密 SM2 与 TLS 1.3 双向 mTLS**：HTTP/HTTPS 与 gRPC 统一支持国密 SM2 / TLS 1.3 客户端证书校验与 CN 白名单鉴权；
3. **高保真模拟数据库**：内置 19 字段医保就医结算数据与 27 字段康养健康档案数据（从 `yibao.csv` / `kangyang.csv` 加载）；
4. **4 个内置模拟数据源**：医保 `ds_yibao`、康养 `ds_kangyang` 及预留扩展 `ds_mock3` / `ds_mock4`，统一经 `record-by-id` 端点按身份证号抽取单条记录；
5. **无状态高可用设计**：纯无状态服务，天然具备秒级崩溃恢复与多实例横向扩展能力。

---

## 2. 总体架构拓扑

```mermaid
graph TD
    subgraph Clients [调用方]
        ServiceHub[Service Hub 调度流水线<br/>:8082 / :50052<br/>【唯一直接调用方】]
    end

    subgraph MockDatasourceMgr ["Mock Datasource Mgr 微服务 (:8083 / :50053)"]
        HTTPRouter["Gin HTTPS/REST 路由层<br/>/api/datasources/* :8083<br/>(SM2 / TLS 1.3 mTLS + CN 白名单)"]
        GRPCRouter["gRPC Server :50053<br/>(SM2 / TLS 1.3 mTLS + CN 白名单)"]
        TLSConfig["统一安全引擎<br/>BuildServerTLSConfig (TLS 1.3 / ClientCA / CN Scope)"]
        MiddlewareStack[统一中间件链<br/>Trace / Logger / Recovery / SecurityHeaders / WAF / MaxBodySize / MaxConcurrent / RateLimit / CORS / ScopeAuth]

        MockDataProvider[内置高保真模拟数据引擎<br/>data_provider.go（CSV 加载 + 动态类型推断）]

        subgraph EmbeddedDatasets [内置模拟数据集 (对齐 DB51/T 2989—2023)]
            DS1[(ds_yibao 医保就医结算 19字段)]
            DS2[(ds_kangyang 康养健康档案 27字段)]
            DS3[(ds_mock3 预留政务数据源 3)]
            DS4[(ds_mock4 预留企业数据源 4)]
        end
    end

    ServiceHub -->|HTTPS REST mTLS :8083| HTTPRouter
    ServiceHub -->|gRPC mTLS :50053| GRPCRouter

    TLSConfig -.->|注入安全配置| HTTPRouter
    TLSConfig -.->|注入安全凭证| GRPCRouter

    HTTPRouter --> MiddlewareStack
    MiddlewareStack --> MockDataProvider
    GRPCRouter --> MockDataProvider

    MockDataProvider --> DS1
    MockDataProvider --> DS2
    MockDataProvider --> DS3
    MockDataProvider --> DS4
```

---

## 3. 接口能力映射与契约分级

> 当前调度流水线统一采用「按身份证号查询单条记录」模式，数据抽取核心入口为 `record-by-id` / `GetRecordByIDCard`。下表为 datasource-mgr 对外暴露的完整能力矩阵（与 [docs/api.md](api.md) §1.4 严格对齐）。
> 真实外部数据局/机构前置机**仅需实现 Class A 核心契约**。

| 分级 | 能力 | REST 规范路径 | gRPC RPC 方法 | 外部数据局要求 | 说明 |
|---|---|---|---|---|---|
| **Class A** | **按身份证号查询单条记录** | `GET /api/datasources/:id/record-by-id?id_card_no=xxx` | `GetRecordByIDCard` | **强制实现 (P0)** | **核心数据抽取入口**，支持 `ds_yibao`（19 字段）/ `ds_kangyang`（27 字段） |
| **Class A** | 健康探活探针 | `GET /health`、`GET /api/health` | `Health` | **强制实现 (P0)** | 服务基础存活探针 |
| **Class A** | 连通性自测试 | `POST /api/datasources/:id/test` | `TestConnection` | **推荐实现 (P1)** | 探测数据源网络与数据库连通性及响应延迟 |
| **Class B** | 数据源资产目录 | `GET /api/datasources` | `ListDataSources` | 可选扩展 (P2) | 列出全部已注册数据源元数据（若支持动态多源） |
| **Class B** | 单数据源详情 | `GET /api/datasources/:id` | `GetDataSource` | 可选扩展 (P2) | 按 ID 查询单个数据源元数据 |
| **Class B** | Schema 元数据探查 | `GET /api/datasources/:id/metadata` | — | 可选扩展 (P2) | 返回表结构与字段类型定义 |
| **Class C** | 容器就绪探针与监控指标 | `GET /readyz`、`GET /metrics` | — | **免对接实现** | K8s 集群就绪探针与 Prometheus 指标（局方专网自决） |
| **Class D** | 模拟访问审计查询 | `GET /api/datasources/:id/audit` | — | **严禁开放 (测试专用)** | 本地单测桩记录，真实审计统一走 `audit-log` |
| **Class D** | 模拟数据播种/重置 | `POST /api/datasources/seed` | — | **严禁开放 (测试专用)** | 本地一键恢复基准状态，生产环境已物理屏蔽 |
