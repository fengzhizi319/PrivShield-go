# 模拟数据源服务 (Mock Datasource Manager) — 详细设计文档

> 本文档定义 **数联天下 · 数盾 (`PrivShield`)** 模拟数据源模块（`services/datasource-mgr`）的系统架构、固定模拟数据库、API 1~4 接口设计、双协议支持（REST + gRPC）与国密 SM2 / TLS 1.3 双向认证。

---

## 1. 定位与设计原则

### 1.1 业务定位
`datasource-mgr` 专为 **开发、联调、集成测试与合规演练** 设计，作为轻量级、高保真的模拟数据提供者。
- **开发/联调期**：提供严格对齐 **DB51/T 2989—2023** 标准的模拟样本数据（医保 `yibao.csv` 18 字段、康养 `kangyang.csv` 27 字段及预留接口 3/4）；
- **生产运行期**：调度中枢及业务服务可直接对接局方真实数据底座（如专有 VPC 子网内的 MySQL/PostgreSQL/Oracle/DataMesh），无需在生产环境中部署多源异构探查等重型测试中间件。

### 1.2 核心特性
1. **双协议通信与全链路安全**：对外提供 HTTP/HTTPS REST (`:8083`) 与高性能 gRPC (`:50053`)；
2. **国密 SM2 与 TLS 1.3 双向 mTLS**：HTTP/HTTPS 与 gRPC 统一支持国密 SM2 / TLS 1.3 客户端证书校验与 CN 白名单鉴权；
3. **高保真模拟数据库**：内置 18 字段医保就医结算数据与 27 字段康养体征随访档案；
4. **4 个独立模拟接口**：API 1（医保 `ds_yibao`）、API 2（康养 `ds_kangyang`）、API 3（预留扩展 3）、API 4（预留扩展 4）；
5. **无状态高可用设计**：纯无状态服务，天然具备秒级崩溃恢复与多实例横向扩展能力。

---

## 2. 总体架构拓扑

```mermaid
graph TD
    subgraph Clients [开发与联调客户端]
        WebConsole[React 前端控制台<br/>:8000 / :5173]
        GatewayBFF[Go BFF 网关<br/>:8081 / :8085]
        ServiceHub[Service Hub 调度流水线<br/>:8082 / :50052]
    end

    subgraph MockDatasourceMgr ["Mock Datasource Mgr 微服务 (:8083 / :50053)"]
        HTTPRouter["Gin HTTPS/REST 路由层<br/>/api/datasources/* :8083<br/>(SM2 / TLS 1.3 mTLS + CN 白名单)"]
        GRPCRouter["gRPC Server :50053<br/>(SM2 / TLS 1.3 mTLS + CN 白名单)"]
        TLSConfig["统一安全引擎<br/>BuildServerTLSConfig (TLS 1.3 / ClientCA / CN Scope)"]
        MiddlewareStack[9层统一中间件链<br/>Auth / TraceID / Logger / Recovery / CORS / MaxBodySize / RateLimit]

        MockDataProvider[内置高保真模拟数据引擎<br/>mock_data.go]

        subgraph EmbeddedDatasets [内置模拟数据集 (对齐 DB51/T 2989—2023)]
            DS1[(API 1: ds_yibao 医保就医结算 18字段)]
            DS2[(API 2: ds_kangyang 康养健康体征 27字段)]
            DS3[(API 3: ds_mock3 预留政务数据源 3)]
            DS4[(API 4: ds_mock4 预留企业数据源 4)]
        end
    end

    WebConsole -->|HTTP/HTTPS REST| HTTPRouter
    GatewayBFF -->|HTTP/HTTPS REST| HTTPRouter
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

## 3. 接口架构映射 (API 1 ~ 4)

| 接口 | 目标数据源 | REST 规范路径 | gRPC RPC 方法 | 字段数 | 关键数据特征 |
|---|---|---|---|---|---|
| **API 1** | 医保数据源 (`ds_yibao`) | `GET /api/datasources/ds_yibao/records` | `GetYibaoData` | 18 字段 | 包含 `insurance_settlement_id`, `person_id`, `id_card`, `diagnosis_name`, `icd10_code`, `total_amount` |
| **API 2** | 康养数据源 (`ds_kangyang`) | `GET /api/datasources/ds_kangyang/records` | `GetKangyangData` | 27 字段 | 包含 `record_id`, `person_id`, `id_card`, `chief_complaint`, `vital_signs_*` (心率/血压/血糖/血氧) |
| **API 3** | 预留数据源 3 (`ds_mock3`) | `GET /api/datasources/ds_mock3/records` | `GetMockData3` | 动态 | 政务跨部门流通与审批流水模拟 |
| **API 4** | 预留数据源 4 (`ds_mock4`) | `GET /api/datasources/ds_mock4/records` | `GetMockData4` | 动态 | 财务税收与企业统计报表模拟 |
