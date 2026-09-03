# 数据源管理与提供服务 (datasource-mgr) — 接口规范与数据局开发对接指南

> **版本**：v2.4.0 (对齐 DB51/T 2989—2023 与国密/TLS 1.3 标准)  
> **文档定位**：本规范为 **数联天下 · 数盾 (`PrivShield`)** 体系中「数据源提供服务（Data Provider Service）」的标准化通信与数据交互规约。  
> **面向对象**：**数据局开发团队、政务云数据底座研发团队、第三方医疗/政务数据提供方**。  
> **重要说明**：当前仓库中的 `services/datasource-mgr` 为 PrivShield 官方提供的**参考实现与高保真模拟服务**。在真实生产部署环境中，**实际只有数盾调度中枢 (`service-hub`) 与数据源服务程序进行直接通信与交互**。数据局及承建方在正式开发真实数据源服务时，**必须严格遵循本技术规范所定义的 REST / gRPC 接口契约、传输安全基线、字段字典与错误信封**，以确保与调度中枢 (`service-hub`) 的零代码修改无缝对接。

---

## 目录

- [1. 系统架构与对接概览](#1-系统架构与对接概览)
  - [1.1 业务定位与交互拓扑](#11-业务定位与交互拓扑)
  - [1.2 通信协议与端口规划](#12-通信协议与端口规划)
  - [1.3 核心数据源唯一标识 (Canonical IDs)](#13-核心数据源唯一标识-canonical-ids)
- [2. 通信协议与安全基线规约](#2-通信协议与安全基线规约)
  - [2.1 传输层安全 (TLS 1.3 / 国密 SM2 / mTLS)](#21-传输层安全-tls-13--国密-sm2--mtls)
  - [2.2 身份鉴权与访问控制 (API Key / CN Whitelist)](#22-身份鉴权与访问控制-api-key--cn-whitelist)
  - [2.3 分布式全链路追踪规范 (Trace & Request ID)](#23-分布式全链路追踪规范-trace--request-id)
- [3. HTTP RESTful API 详细规范](#3-http-restful-api-详细规范)
  - [3.1 探针与监控端点](#31-探针与监控端点)
    - [3.1.1 存活健康检查 (GET /health & GET /api/health)](#311-存活健康检查-get-health--get-apihealth)
    - [3.1.2 服务就绪探针 (GET /readyz)](#312-服务就绪探针-get-readyz)
    - [3.1.3 Prometheus 监控指标 (GET /metrics)](#313-prometheus-监控指标-get-metrics)
  - [3.2 数据源元数据与状态管理端点](#32-数据源元数据与状态管理端点)
    - [3.2.1 获取数据源资产目录列表 (GET /api/datasources)](#321-获取数据源资产目录列表-get-apidatasources)
    - [3.2.2 获取单个数据源详情 (GET /api/datasources/:id)](#322-获取单个数据源详情-get-apidatasourcesid)
    - [3.2.3 查询数据源 Schema 表结构 (GET /api/datasources/:id/metadata)](#323-查询数据源-schema-表结构-get-apidatasourcesidmetadata)
    - [3.2.4 数据源物理/逻辑连通性测试 (POST /api/datasources/:id/test)](#324-数据源物理逻辑连通性测试-post-apidatasourcesidtest)
    - [3.2.5 数据访问审计日志查询 (GET /api/datasources/:id/audit)](#325-数据访问审计日志查询-get-apidatasourcesidaudit)
    - [3.2.6 数据源初始化与重置 (POST /api/datasources/seed)](#326-数据源初始化与重置-post-apidatasourcesseed)
  - [3.3 核心业务数据抽取端点](#33-核心业务数据抽取端点)
    - [3.3.1 按身份证号查询单条记录 (GET /api/datasources/:id/record-by-id)](#331-按身份证号查询单条记录-get-apidatasourcesidrecord-by-id)
    - [3.3.2 按身份证号查询与完整 HTTP 请求-响应示例（端到端）](#332-按身份证号查询与完整-http-请求-响应示例端到端)
- [4. gRPC API 规范与 Protobuf 定义](#4-grpc-api-规范与-protobuf-定义)
  - [4.1 Protobuf 契约文件 (datasourcemgr.proto)](#41-protobuf-契约文件-datasourcemgrproto)
  - [4.2 gRPC 服务接口与方法规约](#42-grpc-服务接口与方法规约)
  - [4.3 gRPC 消息结构体字段详细定义](#43-grpc-消息结构体字段详细定义)
  - [4.4 gRPC Metadata 上下文传递约定](#44-grpc-metadata-上下文传递约定)
- [5. 核心业务数据集标准与字段字典](#5-核心业务数据集标准与字段字典)
  - [5.1 数据集 1：医保就医与费用结算数据集 (ds_yibao / API 1 · 19 字段)](#51-数据集-1医保就医与费用结算数据集-ds_yibao--api-1--19-字段)
  - [5.2 数据集 2：康养体检与慢病健康档案数据集 (ds_kangyang / API 2 · 27 字段)](#52-数据集-2康养体检与慢病健康档案数据集-ds_kangyang--api-2--27-字段)
  - [5.3 数据集 3 & 4：预留政务与企业扩展数据集 (ds_mock3, ds_mock4)](#53-数据集-3--4预留政务与企业扩展数据集-ds_mock3-ds_mock4)
- [6. 统一错误码与异常处理规范](#6-统一错误码与异常处理规范)
  - [6.1 REST 统一 5 字段错误信封](#61-rest-统一-5-字段错误信封)
  - [6.2 HTTP 状态码与业务错误码对照表](#62-http-状态码与业务错误码对照表)
  - [6.3 gRPC 状态码映射标准](#63-grpc-状态码映射标准)
- [7. 非功能性需求与开发交付验收标准](#7-非功能性需求与开发交付验收标准)
  - [7.1 性能与高并发 SLA 指标](#71-性能与高并发-sla-指标)
  - [7.2 推荐环境变量配置表](#72-推荐环境变量配置表)
  - [7.3 数据局端联调自测命令与验收清单](#73-数据局端联调自测命令与验收清单)

---

## 1. 系统架构与对接概览

### 1.1 业务定位与交互拓扑

在 PrivShield 数据要素流通与隐私治理体系中，数据局端的数据源程序扮演 **数据提供者（Data Provider）** 角色。它连接底层的真实政务大数据平台、医保结算中心、公立医院 HIS 系统或民政康养数据库，并通过标准化 REST / gRPC 接口将原始政务/医疗数据安全输出给 PrivShield 调度中枢。

**生产调用关系说明**：
- **唯一直接调用方**：数盾系统的 **调度中枢 (`service-hub`)** 是数据局数据源服务程序的**唯一直接交互客户端**；
- **职责分工**：`service-hub` 负责执行隐私计算治理流水线调度，它发起对数据源程序的数据抽取（优先通过高性能 gRPC `:50053`，或 HTTPS REST `:8083`）、连通性探测和元数据探查；抽取数据后再调度隐私引擎（`engine-go`）执行脱敏、差分隐私、K-匿名等治理操作；
- **隔离保护**：任何前端界面、运维网关或外部第三方系统均不直接连接数据源服务程序，全部通过 `service-hub` 进行统一认证与安全代理。

```mermaid
flowchart LR
    subgraph DataBureau ["数据局 / 专网数据底座 (局方开发实现)"]
        RawDB[("真实业务数据库<br/>(MySQL/PgSQL/Oracle/DataMesh)")]
        DSProgram["数据源服务程序 (Data Provider)<br/>HTTP :8083 / gRPC :50053<br/>【按照本文档规范开发】"]
        RawDB -->|SQL / 内网抽取| DSProgram
    end

    subgraph PrivShieldCluster ["数盾 PrivShield 集群"]
        ServiceHub["调度中枢 service-hub<br/>(:8082 / :50052)<br/>【唯一直接调用方】"]
        Engine["隐私计算引擎 engine-go<br/>(44个隐私原语 + 3层分类分级)"]
        AuditLog["审计存证 audit-log<br/>(:8084)"]
    end

    ServiceHub -->|"gRPC mTLS :50053 (推荐)"| DSProgram
    ServiceHub -->|HTTPS REST mTLS :8083| DSProgram
    ServiceHub -->|隐私流水线调度| Engine
    ServiceHub -.->|不可篡改存证| AuditLog
```

### 1.2 通信协议与端口规划

数据局服务程序应在专用 VPC 或隔离专网中运行，并暴露以下端口：

| 协议类型 | 默认监听端口 | 适用调用方 | 传输安全与认证机制 | 业务用途 |
|---|---|---|---|---|
| **gRPC (HTTP/2)** | `50053` | 数盾调度中枢 (`service-hub`) 【推荐生产首选】 | TLS 1.3 / 国密 SM2 双向 mTLS + 证书 CN 白名单 | 大批量数据记录高速流式抽取、低延迟连通性自检 |
| **HTTP/HTTPS REST** | `8083` | 数盾调度中枢 (`service-hub` HTTP 模式) / 专网运维调试 | TLS 1.3 / 国密 SM2 双向 mTLS + API Key / Bearer Token | 数据源资产检索、连通性探测、元数据 Schema 探查、按身份证号查询 |
| **Prometheus Metrics** | `8083` (`/metrics`) | 运维监控 Prometheus 集群 | 内网隔离 / 局方网关鉴权 | 暴露 QPS、P99 延迟、在途请求数、错误率指标 |

### 1.3 核心数据源唯一标识 (Canonical IDs)

为了保证跨微服务业务协同的统一性，系统规定了权威的 **规范数据源标识符（Canonical DataSource ID）** 与 **业务 API 代码（API Code）**：

| 数据源序号 | 规范数据源标识 (`datasource_id`) | 业务 API 代码 (`api_code`) | 中文业务名称 | 字段数 | 对应业务实体 / 标准规范 |
|---|---|---|---|---|---|
| **1** | `ds_yibao` | `api1_yibao` | 医保就医与结算数据源 | **19** | 医院门诊住院结算、病案诊断（DB51/T 2989—2023） |
| **2** | `ds_kangyang` | `api2_kangyang` | 康养体检与慢病数据源 | **27** | 康养中心体检随访、体征指标、残疾评定（DB51/T 2989—2023） |
| **3** | `ds_mock3` | `(预留)` | 预留政务数据源 3 | 动态 | 跨部门政务服务审批流水、电子证照流水 |
| **4** | `ds_mock4` | `(预留)` | 预留企业/金融数据源 4 | 动态 | 季度税收财务报表、企业信用统计数据 |

> [!IMPORTANT]
> **规范命名约束**：
> 1. 数据局程序在解析与返回数据时，`datasource_id` 必须严格使用 `ds_yibao`、`ds_kangyang` 等规范小写下划线格式；
> 2. 为兼容旧版调用，数据局服务对于入站别名（如 `yibao`、`yibao.csv`、`医保`）应支持归一化解析映射到 `ds_yibao`，但在响应报文的 `datasource_id` 中必须统一返回规范值。

---

## 2. 通信协议与安全基线规约

### 2.1 传输层安全 (TLS 1.3 / 国密 SM2 / mTLS)

在生产环境中，数据源服务承载政务高敏数据，**必须启用双向传输加密与身份验证（mTLS）**：

1. **最低协议版本**：强制使用 **TLS 1.3**（禁止回退至 TLS 1.0/1.1/1.2，防范协议降级攻击）；
2. **国密算法支持**：生产推荐配置支持国密 **SM2 签名/验签与密钥交换 + SM3 摘要 + SM4-GCM 对称加密**，或国际标准套件 `TLS_AES_256_GCM_SHA384` / `TLS_CHACHA20_POLY1305_SHA256`；
3. **客户端证书验证 (ClientAuth)**：
   - 服务端必须启用 `RequireAndVerifyClientCert` 模式；
   - 必须配置受信任的根 CA 证书 (`TLS_CA_FILE`)，拒绝未签发的未知客户端证书连接；
4. **公钥指纹固定 (Public Key Pinning，可选)**：支持配置固定调用方（`service-hub`）的公钥指纹，即便 CA 被仿冒也无法伪造合法连接。

### 2.2 身份鉴权与访问控制 (API Key / CN Whitelist)

1. **HTTP 请求头鉴权**：
   - 调度中枢在调用 REST 接口时，会在 HTTP 请求头中携带 Bearer 令牌：
     ```http
     Authorization: Bearer <API_KEY>
     ```
   - 若数据源服务配置了 `API_KEY`，服务端必须采用**常量时间比对（Constant-Time Compare）**校验 Token，验证失败返回 HTTP `401 Unauthorized`。
2. **gRPC 证书 Common Name (CN) 白名单**：
   - gRPC 服务端在 TLS 握手完成后，应从对端 X.509 证书提取 Subject Common Name (CN)；
   - 仅放行在配置白名单中的客户端 CN（例如：`service-hub.privshield.internal`），不在白名单中的连接直接中断并上报审计。

### 2.3 分布式全链路追踪规范 (Trace & Request ID)

为了实现从调度中枢 (`service-hub`) 到数据源服务 (Data Provider) 的全链路审计与性能追踪，数据局服务必须遵守以下规范：

1. **请求头注入与透传**：
   - **HTTP REST**：读取客户端请求头中的 `X-Request-ID` 与 `X-Trace-ID`（若不存在则自动生成 UUIDv4），并在所有 HTTP 响应头中原样注入回传：
     ```http
     X-Request-ID: req-20260831-yibao-8f7d9a
     X-Trace-ID: req-20260831-yibao-8f7d9a
     ```
   - **gRPC**：从 incoming metadata 中读取 `x-request-id` 与 `x-trace-id`，并在 outgoing header/trailer 中附带回传。
2. **服务来源标识 (`via`)**：
   - 所有响应载荷中均包含 `via` 字段，默认固定为 `"datasource-mgr"`（或数据局自定服务标识符，如 `"data-bureau-provider"`），便于排查多跳调用路径。

---

## 3. HTTP RESTful API 详细规范

### 3.1 探针与监控端点

#### 3.1.1 存活健康检查 (GET /health & GET /api/health)

- **功能说明**：K8s Liveness 探针或负载均衡器健康检查端点。服务进程启动并能响应 HTTP 即返回 200。
- **请求方法**：`GET`
- **请求路径**：`/health`（规范路径）或 `/api/health`（兼容别名）
- **请求头**：无特殊要求
- **请求参数**：无
- **成功响应**：`HTTP 200 OK`
  ```json
  {
    "status": "ok",
    "mode": "mock_datasource_provider",
    "latency_ms": 0,
    "via": "datasource-mgr"
  }
  ```
- **字段说明**：
- 
  | 字段名 | 类型 | 必填 | 说明 |
  |---|---|---|---|
  | `status` | string | 是 | 服务运行状态，固定为 `"ok"` |
  | `mode` | string | 是 | 运行模式标识（如 `"mock_datasource_provider"` 或 `"production_provider"`） |
  | `latency_ms` | integer | 是 | 内部健康自检耗时（毫秒） |
  | `via` | string | 是 | 响应服务标识符 |

---

#### 3.1.2 服务就绪探针 (GET /readyz)

- **功能说明**：K8s Readiness 探针。确认底层数据库连接池初始化完毕、具备对外处理流量能力。
- **请求方法**：`GET`
- **请求路径**：`/readyz`
- **成功响应**：`HTTP 200 OK`
  ```json
  {
    "status": "ready",
    "mode": "mock_datasource_provider",
    "latency_ms": 0,
    "via": "datasource-mgr"
  }
  ```

---

#### 3.1.3 Prometheus 监控指标 (GET /metrics)

- **功能说明**：暴露 Prometheus 标准文本格式的运行指标，供监控系统周期性采集。
- **请求方法**：`GET`
- **请求路径**：`/metrics`
- **成功响应**：`HTTP 200 OK`，`Content-Type: text/plain; version=0.0.4; charset=utf-8`
- **核心指标规范**：
  - `privshield_datasource_requests_total{datasource_id="ds_yibao",api_code="api1_yibao",status="success"}`：请求总计数
  - `privshield_datasource_request_duration_seconds_bucket{...}`：请求耗时直方图
  - `privshield_in_flight_requests`：当前并发在途请求数

---

### 3.2 数据源元数据与状态管理端点

#### 3.2.1 获取数据源资产目录列表 (GET /api/datasources)

- **功能说明**：查询系统中已注册或已连接的全部数据源资产元数据列表。由 `service-hub` 在初始化资产探查时调用。
- **请求方法**：`GET`
- **请求路径**：`/api/datasources`
- **请求头**：
  - `Authorization: Bearer <API_KEY>`（如启用鉴权）
- **成功响应**：`HTTP 200 OK`
  ```json
  {
    "total": 4,
    "datasources": [
      {
        "id": "ds_yibao",
        "datasource_id": "ds_yibao",
        "api_code": "api1_yibao",
        "name": "医保就医与结算模拟数据库 (yibao.csv)",
        "type": "file",
        "description": "模拟医保局患者就医、诊断与费用结算明细数据",
        "status": "connected",
        "row_count": 50,
        "tags": ["医保", "门诊住院", "结算流水", "敏感数据"]
      },
      {
        "id": "ds_kangyang",
        "datasource_id": "ds_kangyang",
        "api_code": "api2_kangyang",
        "name": "康养体检与慢病模拟数据库 (kangyang.csv)",
        "type": "file",
        "description": "模拟民政/卫健康养中心体检、慢病随访与残疾评估数据",
        "status": "connected",
        "row_count": 50,
        "tags": ["康养", "慢病随访", "体检报告", "健康档案"]
      },
      {
        "id": "ds_mock3",
        "datasource_id": "ds_mock3",
        "name": "预留政务数据源 3 (Reserved Mock Source 3)",
        "type": "mock",
        "description": "预留扩展模拟数据源 3，用于后续政务跨部门联合调试",
        "status": "connected",
        "row_count": 10,
        "tags": ["预留", "政务流通", "扩展接口"]
      },
      {
        "id": "ds_mock4",
        "datasource_id": "ds_mock4",
        "name": "预留企业/金融数据源 4 (Reserved Mock Source 4)",
        "type": "mock",
        "description": "预留扩展模拟数据源 4，用于后续企业端数据合规流转调试",
        "status": "connected",
        "row_count": 10,
        "tags": ["预留", "金融统计", "扩展接口"]
      }
    ],
    "via": "datasource-mgr"
  }
  ```
- **字段定义表**：
- 
  | 字段名 | 归属结构 | 类型 | 必填 | 业务说明 |
  |---|---|---|---|---|
  | `total` | 顶层 | integer | 是 | 注册数据源总数量 |
  | `datasources` | 顶层 | array[object] | 是 | 数据源实体对象列表 |
  | `datasources[].id` | 实体 | string | 是 | 数据源唯一标识（与 `datasource_id` 一致） |
  | `datasources[].datasource_id` | 实体 | string | 是 | canonical 数据源 ID（`ds_yibao` 等） |
  | `datasources[].api_code` | 实体 | string | 否 | 业务 API 代码（如 `api1_yibao`），预留数据源可为空 |
  | `datasources[].name` | 实体 | string | 是 | 数据源中文展示名称 |
  | `datasources[].type` | 实体 | string | 是 | 存储类型（`"database"`, `"file"`, `"api"`, `"mock"`） |
  | `datasources[].description` | 实体 | string | 是 | 数据源业务用途与背景描述 |
  | `datasources[].status` | 实体 | string | 是 | 连接状态（`"connected"`, `"disconnected"`, `"error"`） |
  | `datasources[].row_count` | 实体 | integer | 是 | 预估或精确总数据行数 |
  | `datasources[].tags` | 实体 | array[string] | 是 | 业务与分类分级标签数组 |
  | `via` | 顶层 | string | 是 | 响应节点标识 |

---

#### 3.2.2 获取单个数据源详情 (GET /api/datasources/:id)

- **功能说明**：根据数据源 ID 查询单个数据源的元数据。
- **请求方法**：`GET`
- **请求路径**：`/api/datasources/:id`（例如：`/api/datasources/ds_yibao`）
- **路径参数**：
  - `id` (string, 必填)：数据源唯一标识符，支持 `ds_yibao`、`ds_kangyang`、`ds_mock3`、`ds_mock4`。
- **成功响应**：`HTTP 200 OK`
  ```json
  {
    "id": "ds_yibao",
    "datasource_id": "ds_yibao",
    "api_code": "api1_yibao",
    "name": "医保就医与结算模拟数据库 (yibao.csv)",
    "type": "file",
    "description": "模拟医保局患者就医、诊断与费用结算明细数据",
    "status": "connected",
    "row_count": 50,
    "tags": ["医保", "门诊住院", "结算流水", "敏感数据"]
  }
  ```
- **错误响应**：若不存在返回 `HTTP 404 Not Found`：
  ```json
  {
    "code": "NOT_FOUND",
    "message": "mock datasource not found: ds_unknown",
    "trace_id": "req-20260831-err-001",
    "timestamp": "2026-08-31T05:40:00Z"
  }
  ```

---

#### 3.2.3 查询数据源 Schema 表结构 (GET /api/datasources/:id/metadata)

- **功能说明**：探查并返回目标数据源的表结构模式（Schema）、字段名称、物理/逻辑字段类型。PrivShield 的 3-Layer 分类分级引擎在执行敏感特征自动探查时会通过 `service-hub` 调用此接口。
- **请求方法**：`GET`
- **请求路径**：`/api/datasources/:id/metadata`
- **路径参数**：`id` (string, 必填)
- **成功响应**：`HTTP 200 OK`
  ```json
  {
    "datasource_id": "ds_yibao",
    "tables": [
      {
        "name": "医保就医与结算模拟数据库 (yibao.csv)",
        "row_count": 50,
        "fields": [
          {"name": "insurance_settlement_id", "type": "string"},
          {"name": "person_id", "type": "string"},
          {"name": "gender", "type": "string"},
          {"name": "birth_date", "type": "string"},
          {"name": "admission_date", "type": "string"},
          {"name": "discharge_date", "type": "string"},
          {"name": "length_of_stay", "type": "integer"},
          {"name": "admission_dept", "type": "string"},
          {"name": "discharge_dept", "type": "string"},
          {"name": "hospital_code", "type": "string"},
          {"name": "medical_category", "type": "string"},
          {"name": "discharge_mode", "type": "string"},
          {"name": "settlement_seq_no", "type": "string"},
          {"name": "diagnosis_seq", "type": "integer"},
          {"name": "diagnosis_type", "type": "string"},
          {"name": "icd10_code", "type": "string"},
          {"name": "diagnosis_name", "type": "string"},
          {"name": "admission_condition", "type": "string"}
        ]
      }
    ],
    "via": "datasource-mgr"
  }
  ```
- **字段定义表**：

  | 字段名 | 归属 | 类型 | 说明 |
  |---|---|---|---|
  | `datasource_id` | 顶层 | string | 数据源规范标识 |
  | `tables` | 顶层 | array[object] | 数据表列表 |
  | `tables[].name` | 表对象 | string | 表名或数据集名 |
  | `tables[].row_count` | 表对象 | integer | 表内记录总数 |
  | `tables[].fields` | 表对象 | array[object] | 字段定义清单 |
  | `tables[].fields[].name` | 字段对象 | string | 字段英文物理名（如 `person_id`） |
  | `tables[].fields[].type` | 字段对象 | string | 数据类型（`"string"`, `"integer"`, `"float"`, `"timestamp"`, `"boolean"`） |

---

#### 3.2.4 数据源物理/逻辑连通性测试 (POST /api/datasources/:id/test)

- **功能说明**：由调度中枢 (`service-hub`) 或专网运维工具触发，主动探测目标数据源的网络连通性、账号鉴权状态及响应延迟。
- **请求方法**：`POST`
- **请求路径**：`/api/datasources/:id/test`
- **路径参数**：`id` (string, 必填)
- **请求 Body**：空
- **成功响应**：`HTTP 200 OK`
  ```json
  {
    "datasource_id": "ds_yibao",
    "success": true,
    "latency_ms": 2,
    "via": "datasource-mgr"
  }
  ```
- **失败响应**：若连通失败返回 `HTTP 200 OK` 且 `success: false`，或者抛出 `HTTP 404/500` 错误信封：
  ```json
  {
    "datasource_id": "ds_yibao",
    "success": false,
    "error": "database connection timeout after 3000ms",
    "latency_ms": 3001,
    "via": "datasource-mgr"
  }
  ```

---

#### 3.2.5 数据访问审计日志查询 (GET /api/datasources/:id/audit)

- **功能说明**：查询针对该数据源的历史采样与抽取审计记录。
- **请求方法**：`GET`
- **请求路径**：`/api/datasources/:id/audit`
- **成功响应**：`HTTP 200 OK`
  ```json
  {
    "datasource_id": "ds_yibao",
    "total": 1,
    "records": [
      {
        "id": "audit_mock_1",
        "operation": "query_sample",
        "user": "service-hub",
        "timestamp": "2026-08-31T05:40:00Z",
        "status": "success"
      }
    ],
    "via": "datasource-mgr"
  }
  ```

---

#### 3.2.6 数据源初始化与重置 (POST /api/datasources/seed)

- **功能说明**：重置或预填充模拟数据源，用于测试环境一键恢复基准状态。
- **请求方法**：`POST`
- **请求路径**：`/api/datasources/seed`
- **成功响应**：`HTTP 200 OK`
  ```json
  {
    "message": "mock datasources initialized (yibao, kangyang, mock3, mock4)",
    "via": "datasource-mgr"
  }
  ```

---

### 3.3 核心业务数据抽取端点

#### 3.3.1 按身份证号查询单条记录 (GET /api/datasources/:id/record-by-id)

- **功能说明**：根据身份证号从指定数据源中精确查询单条记录。支持 `ds_yibao` 和 `ds_kangyang` 两个核心数据源，均按 `id_card_no` 字段匹配。
- **请求方法**：`GET`
- **请求路径**：`/api/datasources/:id/record-by-id`
- **Path 参数**：

  | 参数名 | 类型 | 必填 | 说明 |
  |---|---|---|---|
  | `id` | string | 是 | 规范数据源标识符，如 `ds_yibao`、`ds_kangyang` |

- **Query 参数**：

  | 参数名 | 类型 | 必填 | 说明 |
  |---|---|---|---|
  | `id_card_no` | string | 是 | 18 位中国居民身份证号码 |

- **成功响应**：`HTTP 200 OK`（找到记录）
  ```json
  {
    "datasource_id": "ds_yibao",
    "record": {
      "insurance_settlement_id": "YB202511040001",
      "person_id": "PID66453983",
      "gender": "男",
      "birth_date": "1968-09-17",
      "admission_date": "2025-11-04",
      "discharge_date": "2025-11-13",
      "length_of_stay": "9",
      "admission_dept": "急诊科",
      "discharge_dept": "急诊科",
      "hospital_code": "H4201020015",
      "medical_category": "日间手术",
      "discharge_mode": "医嘱转院",
      "settlement_seq_no": "MX202511049975",
      "diagnosis_seq": "1",
      "diagnosis_type": "主要诊断",
      "icd10_code": "A51.000",
      "diagnosis_name": "硬下疳伴TPPA滴度1:64阳性(早期梅毒)",
      "admission_condition": "一般",
      "id_card_no": "510101199001011234"
    },
    "found": true,
    "via": "datasource-mgr"
  }
  ```
- **未找到响应**：`HTTP 200 OK`（未找到匹配记录）
  ```json
  {
    "datasource_id": "ds_yibao",
    "record": null,
    "found": false,
    "via": "datasource-mgr"
  }
  ```
- **错误响应**：`HTTP 400 Bad Request`（缺少或非法 `id_card_no` 参数）
- **响应字段说明**：

  | 字段名 | 类型 | 说明 |
  |---|---|---|
  | `datasource_id` | string | 规范数据源标识符 |
  | `record` | object \| null | 匹配的单条数据记录，未找到时为 null |
  | `found` | boolean | 是否找到匹配记录 |
  | `via` | string | 服务节点标识符 |

---

#### 3.3.2 按身份证号查询与完整 HTTP 请求-响应示例（端到端）

> 本节提供 `ds_yibao`（医保）和 `ds_kangyang`（康养）两个核心数据集的**完整 HTTP 请求与响应报文示例**。
> 当前数盾调度流水线已切换为**按身份证号查询单条记录**模式（参见 §3.3.1），调度中枢 (`service-hub`) 通过 `/api/datasources/:id/record-by-id?id_card_no=xxx` 按身份证号精确抽取单条记录。

##### 示例 1：ds_yibao 按身份证号查询单条记录（推荐）

**curl 命令**：

```bash
curl -s -i \
  -H "Authorization: Bearer sec_privshield_token_2026" \
  -H "Accept: application/json" \
  -H "X-Request-ID: req-20260902-yibao-id-a1b2c3" \
  "http://127.0.0.1:8083/api/datasources/ds_yibao/record-by-id?id_card_no=110101196809171010"
```

**完整 HTTP 请求报文**：

```http
GET /api/datasources/ds_yibao/record-by-id?id_card_no=110101196809171010 HTTP/1.1
Host: 127.0.0.1:8083
Authorization: Bearer sec_privshield_token_2026
Accept: application/json
X-Request-ID: req-20260902-yibao-id-a1b2c3
User-Agent: service-hub/1.0
```

**完整 HTTP 响应报文**：

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
X-Request-ID: req-20260902-yibao-id-a1b2c3
X-Trace-ID: req-20260902-yibao-id-a1b2c3
Date: Tue, 02 Sep 2026 08:30:00 GMT
Content-Length: 1284

{
  "datasource_id": "ds_yibao",
  "record": {
    "insurance_settlement_id": "YB202511040001",
    "person_id": "PID66453983",
    "gender": "男",
    "birth_date": "1968-09-17",
    "admission_date": "2025-11-04",
    "discharge_date": "2025-11-13",
    "length_of_stay": "9",
    "admission_dept": "急诊科",
    "discharge_dept": "急诊科",
    "hospital_code": "H4201020015",
    "medical_category": "日间手术",
    "discharge_mode": "医嘱转院",
    "settlement_seq_no": "MX202511049975",
    "diagnosis_seq": "1",
    "diagnosis_type": "主要诊断",
    "icd10_code": "A51.000",
    "diagnosis_name": "硬下疳伴TPPA滴度1:64阳性(早期梅毒)",
    "admission_condition": "一般",
    "id_card_no": "110101196809171010"
  },
  "found": true,
  "via": "datasource-mgr"
}
```

> **要点说明**：
> - 端点 `/api/datasources/:id/record-by-id` 为**当前推荐规范**，通过 `id_card_no` 查询参数按身份证号精确查询单条记录（参见 §3.3.6）；
> - 响应中 `found` 字段标识是否找到匹配记录，`record` 为匹配的单条完整数据对象（19 字段）或 `null`；
> - 响应固定包含 `datasource_id`（规范数据源标识符）。

---

##### 示例 2：ds_kangyang 按身份证号查询单条记录（推荐）

**curl 命令**：

```bash
curl -s -i \
  -H "Authorization: Bearer sec_privshield_token_2026" \
  -H "Accept: application/json" \
  -H "X-Request-ID: req-20260902-ky-id-g7h8i9" \
  "http://127.0.0.1:8083/api/datasources/ds_kangyang/record-by-id?id_card_no=110105198402151071"
```

**完整 HTTP 请求报文**：

```http
GET /api/datasources/ds_kangyang/record-by-id?id_card_no=110105198402151071 HTTP/1.1
Host: 127.0.0.1:8083
Authorization: Bearer sec_privshield_token_2026
Accept: application/json
X-Request-ID: req-20260902-ky-id-g7h8i9
User-Agent: service-hub/1.0
```

**完整 HTTP 响应报文**：

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
X-Request-ID: req-20260902-ky-id-g7h8i9
X-Trace-ID: req-20260902-ky-id-g7h8i9
Date: Tue, 02 Sep 2026 08:32:00 GMT
Content-Length: 3156

{
  "datasource_id": "ds_kangyang",
  "record": {
    "gender": "男",
    "age": "45",
    "diagnosis_name": "急性心肌梗死",
    "chief_complaint": "反复胸闷胸痛半年，加重2小时",
    "present_illness": "患者2小时前突发胸骨后剧烈压榨样疼痛，向左肩背部放射，伴大汗及濒死感。心电图示V1-V5导联ST段抬高0.3-0.5mV。急诊行冠脉造影提示前降支100%闭塞，予行PCI术及支架植入术。",
    "past_history": "高脂血症病史5年，口服阿托伐他汀20mg qn。高血压病史3年，最高160/100mmHg。",
    "personal_history": "吸烟20年，每日20支(20包年)。饮酒15年。",
    "is_smoking": "是",
    "smoking_duration": "20年",
    "family_history": "父亲因'恶性肿瘤'去世(65岁)，母亲健在。一弟患'重度精神分裂症'、'2型糖尿病'。否认其他家族遗传病史。",
    "allergic_history": "青霉素过敏(皮疹)。",
    "department": "心内科",
    "height": "175",
    "weight": "78",
    "disability_category": "肢体残疾",
    "disability_level": "二级",
    "assess_type_name": "心功能综合评估",
    "assess_result_name": "需辅助工具与护理",
    "assess_score": "65",
    "assess_time": "2025-01-10",
    "progress_note": "今日查房：患者神志清楚，心前区无不适。查体：BP 125/80mmHg，HR 72次/分，律齐。继续予双抗及他汀治疗。",
    "progress_note_time": "2025-01-10 10:30:00",
    "name": "萧志明_1",
    "id_card_no": "110105198402151071",
    "registered_address": "北京市东城区景山前街4号",
    "disability_cert_no": "11010119800512123401",
    "medical_insurance_no": "3301030127183297"
  },
  "found": true,
  "via": "datasource-mgr"
}
```

> **要点说明**：
> - `ds_kangyang` 严格输出 **27 个字段**，与 §5.2 字段字典完全一致；
> - 允许部分字段为空字符串（如 `smoking_duration` 不吸烟时、`disability_cert_no` 无残疾时），但**字段 Key 必须完整保留**，不得省略；
> - 长文本字段（`present_illness`、`past_history`、`family_history`、`progress_note`）保留原始换行与标点，调用方需自行处理展示。

---

##### 示例 3：按身份证号查询未找到记录

**curl 命令**：

```bash
curl -s -i \
  -H "Authorization: Bearer sec_privshield_token_2026" \
  -H "Accept: application/json" \
  -H "X-Request-ID: req-20260902-yibao-nf-x1y2z3" \
  "http://127.0.0.1:8083/api/datasources/ds_yibao/record-by-id?id_card_no=000000000000000000"
```

**完整 HTTP 响应报文**：

```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
X-Request-ID: req-20260902-yibao-nf-x1y2z3
X-Trace-ID: req-20260902-yibao-nf-x1y2z3
Date: Tue, 02 Sep 2026 08:31:00 GMT
Content-Length: 98

{
  "datasource_id": "ds_yibao",
  "record": null,
  "found": false,
  "via": "datasource-mgr"
}
```

> **要点说明**：
> - 身份证号格式合法（18 位）但在数据源中未找到匹配记录时，仍返回 `HTTP 200 OK`；
> - `found` 为 `false`，`record` 为 `null`，调用方应据此判断是否需要降级处理。


---

##### 错误响应示例

**场景：查询不存在的数据源**

```bash
curl -s -i \
  -H "Authorization: Bearer sec_privshield_token_2026" \
  -H "Accept: application/json" \
  -H "X-Request-ID: req-20260902-err-m3n4o5" \
  "http://127.0.0.1:8083/api/datasources/ds_unknown/record-by-id?id_card_no=110101196809171010"
```

```http
HTTP/1.1 404 Not Found
Content-Type: application/json; charset=utf-8
X-Request-ID: req-20260902-err-m3n4o5
X-Trace-ID: req-20260902-err-m3n4o5
Date: Tue, 02 Sep 2026 08:34:00 GMT
Content-Length: 186

{
  "code": "NOT_FOUND",
  "message": "mock datasource not found: ds_unknown",
  "detail": null,
  "trace_id": "req-20260902-err-m3n4o5",
  "timestamp": "2026-09-02T08:34:00.123456789Z"
}
```

**场景：缺少身份证号参数**

```bash
curl -s -i \
  -H "Authorization: Bearer sec_privshield_token_2026" \
  -H "Accept: application/json" \
  "http://127.0.0.1:8083/api/datasources/ds_yibao/record-by-id"
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/json; charset=utf-8
Date: Tue, 02 Sep 2026 08:35:00 GMT
Content-Length: 178

{
  "code": "INVALID_ARGUMENT",
  "message": "query parameter id_card_no is required",
  "detail": null,
  "trace_id": "req-20260902-err-p6q7r8",
  "timestamp": "2026-09-02T08:35:00.654321000Z"
}
```

**场景：身份证号格式非法**

```bash
curl -s -i \
  -H "Authorization: Bearer sec_privshield_token_2026" \
  -H "Accept: application/json" \
  "http://127.0.0.1:8083/api/datasources/ds_yibao/record-by-id?id_card_no=12345"
```

```http
HTTP/1.1 400 Bad Request
Content-Type: application/json; charset=utf-8
Date: Tue, 02 Sep 2026 08:36:00 GMT
Content-Length: 168

{
  "code": "INVALID_ARGUMENT",
  "message": "id_card_no must be 18 characters",
  "detail": null,
  "trace_id": "req-20260902-err-s9t0u1",
  "timestamp": "2026-09-02T08:36:00.123456789Z"
}
```

> **注意**：`record-by-id` 端点对 `id_card_no` 参数进行严格校验：必填且必须为 18 位，不合法时返回 `HTTP 400 Bad Request` 及标准 5 字段错误信封。

---

## 4. gRPC API 规范与 Protobuf 定义

### 4.1 Protobuf 契约文件 (datasourcemgr.proto)

数据局端实现的 gRPC 服务端必须完全兼容以下 Protobuf 语法定义（`proto3`），保持 Package 名与字段 Tag 严格一致：

```protobuf
syntax = "proto3";

package datasourcemgr;

option go_package = "github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/proto";

// DataSourceManagerService 数据源管理与提供服务
// 对外提供高性能数据记录查询、目录清单与连通性探针
service DataSourceManagerService {
  // Health 服务可用性探针
  rpc Health(HealthRequest) returns (HealthResponse);

  // ListDataSources 规范通用 RPC：获取当前已注册数据源目录列表
  rpc ListDataSources(ListMockSourcesRequest) returns (ListMockSourcesResponse);

  // GetDataSource 获取单个数据源详细元数据
  rpc GetDataSource(GetDataSourceRequest) returns (DataSourceProto);

  // TestConnection 连通性与响应延迟探测
  rpc TestConnection(TestConnectionRequest) returns (TestConnectionResponse);

  // GetRecordByIDCard 按身份证号查询单条记录
  rpc GetRecordByIDCard(GetRecordByIDCardRequest) returns (SingleRecordResponse);

}

// ─────────────────────────────────────────────────────────────
// 消息结构定义
// ─────────────────────────────────────────────────────────────

message HealthRequest {}

message HealthResponse {
  string status = 1;       // 服务状态，如 "ok"
  int64  latency_ms = 2;   // 探测耗时（毫秒）
  string via = 3;          // 节点标识，如 "datasource-mgr"
}

message DataRowProto {
  map<string, string> fields = 1; // 动态键值对数据行，Key 为字段英文名，Value 为字符串格式化值
}

message DataSourceProto {
  string id = 1;                      // 数据源唯一 ID
  string name = 2;                    // 数据源中文名称
  string type = 3;                    // 数据源类型 ("database", "file", "mock")
  string description = 4;             // 数据源用途描述
  string status = 5;                  // 状态 ("connected", "disconnected")
  int32  row_count = 6;               // 总数据行数
  repeated string tags = 7;           // 业务与分类标签
}

message ListMockSourcesRequest {}

message ListMockSourcesResponse {
  int32 total = 1;                         // 数据源总数
  repeated DataSourceProto sources = 2;    // 数据源列表
  string via = 3;                          // 服务节点标识
}

message GetDataSourceRequest {
  string id = 1;                      // 数据源 ID (如 "ds_yibao")
}

message TestConnectionRequest {
  string id = 1;                      // 数据源 ID
}

message TestConnectionResponse {
  string datasource_id = 1;           // 测试的数据源 ID
  bool success = 2;                   // 连通性测试结果 (true/false)
  int64 latency_ms = 3;               // 网络与握手耗时（毫秒）
  string via = 4;                     // 服务节点标识
}

message GetRecordByIDCardRequest {
  string source_id = 1;              // 数据源标识，如 "ds_yibao", "ds_kangyang"
  string id_card_no = 2;             // 18 位中国居民身份证号码
}

message SingleRecordResponse {
  string datasource_id = 1;          // 规范数据源标识符
  DataRowProto record = 2;           // 匹配的单条数据记录，未找到时为 nil
  bool found = 3;                    // 是否找到匹配记录
  string via = 4;                    // 服务节点标识
}
```

### 4.2 gRPC 服务接口与方法规约

| RPC 方法名 | 请求消息 | 响应消息 | 建议调用方 | 说明 |
|---|---|---|---|---|
| `Health` | `HealthRequest` | `HealthResponse` | `service-hub` 探活、K8s gRPC 探针 | 检查 gRPC 服务端及底层连接池健康状况 |
| `ListDataSources` | `ListMockSourcesRequest` | `ListMockSourcesResponse` | `service-hub` 资产探查流水线 | **【规范推荐】** 列出所有已就绪数据源资产元数据 |
| `GetDataSource` | `GetDataSourceRequest` | `DataSourceProto` | `service-hub` | 查询指定数据源元数据 |
| `TestConnection` | `TestConnectionRequest` | `TestConnectionResponse` | `service-hub` 连通性检测 | 执行真实数据源连通性测试 |
| `GetRecordByIDCard` | `GetRecordByIDCardRequest` | `SingleRecordResponse` | `service-hub` 调度流水线 | **【规范推荐】** 按身份证号查询单条记录 |

### 4.3 gRPC 消息结构体字段详细定义

以核心的 `SingleRecordResponse` 为例：
- `record` 为 `DataRowProto`，包含一个 `map<string, string> fields`。
- **值序列化规范**：
  - 数值型（如 `length_of_stay: 9`、`age: 45`、`assess_score: 65`）需格式化为对应的字符串（如 `"9"`, `"45"`, `"65"`）放入 map，确保传输的通用性与跨平台一致性。
  - 空值字段映射为空字符串 `""`，不得直接忽略 Key，以保证表结构列名完整。

### 4.4 gRPC Metadata 上下文传递约定

1. **Incoming Metadata**：服务端在处理每一个 RPC 时，应尝试从 incoming metadata 中读取 `x-request-id` 与 `x-trace-id`；
2. **Outgoing Metadata**：在返回响应或抛出 gRPC 错误前，通过 `grpc.SetHeader(ctx, md)` 将追踪 ID 原样回传。

---

## 5. 核心业务数据集标准与字段字典

根据四川省地方标准 **DB51/T 2989—2023（健康医疗大数据应用安全与隐私保护指南）** 及国家卫健委、国家医保局相关数据元标准，以下为数据局端应支持的两个核心高敏数据集的字段字典规范。

### 5.1 数据集 1：医保就医与费用结算数据集 (ds_yibao / API 1 · 19 字段)

- **数据集标识**：`ds_yibao`
- **业务场景**：医疗保险结算、门诊慢特病审核、住院就医行为分析、异地就医合规核查。
- **字段总数**：**19 个**

| 序号 | 字段英文名 (物理名) | 字段中文名 | 数据类型 | 必填 |  取值范围 / 样例数据 | 业务含义与说明 |
|---|---|---|---|---|---|---|
| 1 | `insurance_settlement_id` | 医保结算流水号 | `string` | 是 |  `YB202511040001` | 医保结算单次唯一业务流水号，通常由机构与时间戳组合生成 |
| 2 | `person_id` | 参保人个人编号 | `string` | 是 |  `PID66453983` | 医保系统内部参保人全局唯一编码（脱敏或加密映射） |
| 3 | `gender` | 性别 | `string` | 是 |  `男` / `女` / `其他` | 参保患者生理性别（GB/T 2261.1） |
| 4 | `birth_date` | 出生日期 | `string` | 是 |  `1968-09-17` | 出生年月日，标准格式 `YYYY-MM-DD` |
| 5 | `admission_date` | 入院/就诊日期 | `string` | 是 |  `2025-11-04` | 患者办理门诊就诊或住院手续日期 (`YYYY-MM-DD`) |
| 6 | `discharge_date` | 出院/结算日期 | `string` | 是 |  `2025-11-13` | 门诊离开或出院办结日期 (`YYYY-MM-DD`) |
| 7 | `length_of_stay` | 实际住院天数 | `integer` | 是 |  `9` (天) | 住院总天数，门诊通常为 0 或 1 |
| 8 | `admission_dept` | 入院/就诊科室 | `string` | 是 | `急诊科`, `心血管内科` | 患者办理入院时的初始临床科室名称 |
| 9 | `discharge_dept` | 出院科室 | `string` | 是 | `急诊科`, `血液内科` | 患者最终办理出院的临床科室名称 |
| 10 | `hospital_code` | 定点医药机构编码 | `string` | 是 | `H4201020015` | 国家医保统一定点医疗机构编码（定点医院代码） |
| 11 | `medical_category` | 医疗类别 | `string` | 是 | `普通门诊`, `住院`, `门诊慢特病`, `日间手术` | 本次就医的医保结算类别枚举 |
| 12 | `discharge_mode` | 离院方式 | `string` | 是 | `医嘱离院`, `医嘱转院`, `非医嘱离院`, `死亡` | 患者出院时的转归状态枚举 |
| 13 | `settlement_seq_no` | 结算序列号 | `string` | 是 | `MX202511049975` | 院内 HIS 与医保交易结算详细对账批次序列号 |
| 14 | `diagnosis_seq` | 诊断序号 | `integer` | 是 | `1`, `2`, `3` | 本次就诊记录中的诊断排列序号 |
| 15 | `diagnosis_type` | 诊断类型 | `string` | 是 | `主要诊断`, `次要诊断`, `病理诊断` | 诊断在病案中的级别分类 |
| 16 | `icd10_code` | ICD-10 疾病编码 | `string` | 是 | `A51.000`, `I21.900`, `C34.900` | 国际疾病分类标准编码 ICD-10，属于极度高敏健康生理诊断特征 |
| 17 | `diagnosis_name` | 临床诊断中文名称 | `string` | 是 | `硬下疳伴TPPA滴度1:64阳性(早期梅毒)`, `原发性高血压` | 医生书写的临床疾病确诊中文全称 |
| 18 | `admission_condition` | 入院病情评估 | `string` | 是 | `一般`, `急`, `重`, `危` | 入院时患者病情严重程度评级 |
| 19 | `id_card_no` | 公民身份证号 | `string` | 是 | `510101199001011234` | 18 位中国居民身份证号码（极高敏身份核验要素），用于按身份证号精确查询单条记录 |

---

### 5.2 数据集 2：康养体检与慢病健康档案数据集 (ds_kangyang / API 2 · 27 字段)

- **数据集标识**：`ds_kangyang`
- **业务场景**：民政/卫健康养中心老人健康管理、慢病定期随访、残疾人评定及综合护理能力评估。
- **字段总数**：**27 个**

| 序号 | 字段英文名 (物理名) | 字段中文名 | 数据类型 | 必填 | 取值范围 / 样例数据                                    | 业务含义与说明 |
|---|---|---|---|---|--------------------------------------------------------|---|
| 1 | `gender` | 性别 | `string` | 是 | `男` / `女`                                            | 档案对象性别 |
| 2 | `age` | 年龄 | `integer` | 是 | `45`, `68`                                             | 实足周岁年龄 (0 ~ 120) |
| 3 | `diagnosis_name` | 主要疾病诊断 | `string` | 是 | `急性心肌梗死`, `重度精神分裂症`, `2型糖尿病`          | 康养中心建档慢病或主要健康问题 |
| 4 | `chief_complaint` | 主诉 | `string` | 是 | `反复胸闷胸痛半年，加重2小时`                          | 患者或家属自述的最主要症状与持续时间 |
| 5 | `present_illness` | 现病史 | `string` | 是 | `患者2小时前突发胸骨后剧烈压榨样疼痛...`               | 本次疾病发生、发展、诊疗经过详细描述 |
| 6 | `past_history` | 既往病史 | `string` | 是 | `高脂血症病史5年，高血压病史3年...`                    | 既往患有的慢性疾病、手术外伤史 |
| 7 | `personal_history` | 个人史与生活习惯 | `string` | 是 | `吸烟20年，每日20支。饮酒15年。`                       | 烟酒嗜好、职业暴露史、居住环境史 |
| 8 | `is_smoking` | 是否吸烟 | `string` | 是 | `是` / `否`                                            | 是否有长期吸烟习惯 |
| 9 | `smoking_duration` | 吸烟年限 | `string` | 否 | `20年` (不吸烟可为空)                                  | 吸烟持续时间或包年数 |
| 10 | `family_history` | 家族遗传病史 | `string` | 是 | `父亲因恶性肿瘤去世，一弟患重度精神分裂症`             | 直系亲属遗传性疾病与重大病史 |
| 11 | `allergic_history` | 药物与食物过敏史 | `string` | 是 | `青霉素过敏(皮疹)` / `否认过敏史`                      | 已知的药物、食物或环境过敏原 |
| 12 | `department` | 负责临床/康养科室 | `string` | 是 | `心内科`, `精神科`, `神经内科`                         | 随访或建档归属科室 |
| 13 | `height` | 身高 (cm) | `integer` | 是 | `175`                                                  | 身体测量指标身高（厘米） |
| 14 | `weight` | 体重 (kg) | `integer` | 是 | `78`                                                   | 身体测量指标体重（公斤） |
| 15 | `disability_category` | 残疾类别 | `string` | 是 | `肢体残疾`, `精神残疾`, `视力残疾`, `无残疾`           | 经残联认定的残疾类别（GB/T 13800） |
| 16 | `disability_level` | 残疾等级 | `string` | 是 | `一级`, `二级`, `三级`, `四级`, `无`                   | 残疾严重程度分级（一级最重） |
| 17 | `assess_type_name` | 综合评估类型 | `string` | 是 | `心功能综合评估`, `精神功能综合评估`, `常规健康评估`   | 本次评估量表或体系名称 |
| 18 | `assess_result_name` | 评估结论等级 | `string` | 是 | `需辅助工具与护理`, `完全不能独立生活`, `完全独立生活` | 综合生活自理与健康等级判定 |
| 19 | `assess_score` | 综合评估分值 | `integer` | 是 | `65` (0 ~ 100)                                         | 量表量化总分值（如 Barthel 评分） |
| 20 | `assess_time` | 评估日期 | `string` | 是 | `2025-01-10`                                           | 评估执行日期 (`YYYY-MM-DD`) |
| 21 | `progress_note` | 查房/随访病程记录 | `string` | 是 | `今日查房：患者神志清楚，查体BP 125/80...`             | 医护人员随访与体格检查详细记录 |
| 22 | `progress_note_time` | 病程记录时间戳 | `string` | 是 | `2025-01-10 10:30:00`                                  | 病程记录精确时间 (`YYYY-MM-DD HH:MM:SS`) |
| 23 | `name` | 患者真实姓名 | `string` | 是 | `萧志明_1`, `郭凯_2`                                   | 患者姓名（极高敏个人标识符） |
| 24 | `id_card_no` | 公民身份证号 | `string` | 是 | `110105198402151071`                                   | 18 位中国居民身份证号码（极高敏身份核验要素） |
| 25 | `registered_address` | 户籍居住地址 | `string` | 是 | `北京市东城区景山前街4号`                              | 详细到门牌号的常住/户籍地址 |
| 26 | `disability_cert_no` | 残疾人证件号 | `string` | 是 | `11010119800512123401`                                 | 中国残疾人联合会统一下发残疾人证号 (20位) |
| 27 | `medical_insurance_no`| 个人医保卡号/社保号 | `string` | 是 |  `3301030127183297`                                    | 社会保障卡号或医保个人编号 |

---

### 5.3 数据集 3 & 4：预留政务与企业扩展数据集 (ds_mock3, ds_mock4)

- **`ds_mock3` (政务审批流水)**：
  - 核心字段建议包含：`id` (int), `service_code` (string), `name` (string), `amount` (float), `status` (string), `apply_time` (string)。
- **`ds_mock4` (企业财税经营报表)**：
  - 核心字段建议包含：`id` (int), `dept_code` (string), `report_name` (string), `value` (float), `quarter` (string)。

---

## 6. 统一错误码与异常处理规范

为了实现跨系统与跨语言的错误统一解析，数据局端程序在遇到异常时，**必须返回 PrivShield 跨模块标准的统一错误信封**。

### 6.1 REST 统一 5 字段错误信封

当发生 HTTP 4xx 或 5xx 异常时，响应 Body 必须为以下标准 JSON 格式：

```json
{
  "code": "INVALID_ARGUMENT",
  "message": "请求参数非法：缺少 id_card_no",
  "detail": {
    "field": "limit",
    "received": 1000,
    "max_allowed": 500
  },
  "trace_id": "req-20260831-err-8f7d9a",
  "timestamp": "2026-08-31T05:40:00.123456789Z"
}
```

- **字段说明**：

  | 字段名 | 类型 | 必填 | 说明 |
  |---|---|---|---|
  | `code` | string | 是 | 机器可读的大写下划线错误码枚举 |
  | `message` | string | 是 | 面向开发或运维人员的人类可读中文错误摘要 |
  | `detail` | any | 否 | 结构化补充详情（如校验失败的字段列表或上游原始错误） |
  | `trace_id` | string | 是 | 分布式追踪 ID（与请求头 `X-Request-ID` / `X-Trace-ID` 保持一致） |
  | `timestamp` | string | 是 | RFC3339 格式的 UTC 时间戳（精确到毫秒或纳秒） |

---

### 6.2 HTTP 状态码与业务错误码对照表

| HTTP 状态码 | 业务错误代码 (`code`) | 触发场景说明 |
|---|---|---|
| `400 Bad Request` | `INVALID_ARGUMENT` | 请求参数缺失、格式错误（如 `id_card_no` 非法） |
| `400 Bad Request` | `INVALID_DATASOURCE_ID` | 请求的 `datasource_id` 格式错误或无法归一化识别 |
| `401 Unauthorized` | `UNAUTHORIZED` | 未携带 `Authorization` 头或 API Key / Bearer 校验失败 |
| `403 Forbidden` | `FORBIDDEN` | 客户端证书 CN 不在白名单中或无权访问该数据源 |
| `404 Not Found` | `NOT_FOUND` | 指定的 `datasource_id` 不存在于数据源目录中 |
| `409 Conflict` | `RESERVED_DATASOURCE` | 尝试访问已登记但未实现上线的预留数据源 |
| `413 Payload Too Large`| `PAYLOAD_TOO_LARGE` | 上传请求体超过 32 MiB 单包限制 |
| `429 Too Many Requests`| `RATE_LIMITED` | 客户端请求速率触发每 IP / 全局令牌桶限流阈值 |
| `500 Internal Error` | `INTERNAL_ERROR` | 数据局程序内部 SQL 执行异常、文件 I/O 失败或未知崩溃 |
| `503 Unavailable` | `UPSTREAM_UNAVAILABLE` | 底层政务数据库不可达、连接池耗尽或并发排队超限 |

---

### 6.3 gRPC 状态码映射标准

数据局端在实现 gRPC 接口时，若遇到异常应通过 `status.Errorf(codes.Code, format, ...)` 返回规范的 gRPC Code：

| 错误场景 | 对应的 gRPC Code | 错误信息示例 |
|---|---|---|
| 入参 `source_id` 为空或非法 | `codes.InvalidArgument` | `"source_id is required and must match ^ds_[a-z0-9_]+$"` |
| 请求的数据源不存在 | `codes.NotFound` | `"datasource not found: ds_xyz"` |
| 客户端 mTLS 证书校验失败 | `codes.Unauthenticated` | `"client did not present a valid certificate"` |
| 客户端 CN 无权访问该数据源 | `codes.PermissionDenied` | `"CN 'test.client' is not authorized for ds_yibao"` |
| 底层数据库查询超时 | `codes.DeadlineExceeded` | `"upstream database query timed out after 5000ms"` |
| 数据局服务并发超载 | `codes.ResourceExhausted` | `"datasource server concurrency limit reached (max 1000)"` |
| 底层数据库崩溃或内部错误 | `codes.Internal` | `"failed to read records from database: dial tcp connection refused"` |

---

## 7. 非功能性需求与开发交付验收标准

### 7.1 性能与高并发 SLA 指标

为了保障 PrivShield 隐私计算流水线（DP/LDP/K-Anonymity）的高效运转，数据局端程序应满足以下性能指标：

1. **响应延迟 SLA**：
   - 数据源单次查询（按身份证号）：**P95 延迟 < 20ms，P99 延迟 < 50ms**；
   - 连通性测试与健康检查：**P99 延迟 < 5ms**；
2. **吞吐能力**：单实例支持 **500+ QPS** 并发抽取能力；
3. **并发在途上限**：单服务实例建议支持最大 **1000** 个并发在途请求，超出时通过快速失败返回 `503 Service Unavailable` 保护底层数据库。

### 7.2 推荐环境变量配置表

建议数据局在开发该程序时，支持通过环境变量进行运行时参数配置：

| 环境变量名 | 默认值 | 示例值 | 配置说明 |
|---|---|---|---|
| `DATASOURCE_MGR_HOST` | `0.0.0.0` | `0.0.0.0` | HTTP/REST 监听绑定地址 |
| `DATASOURCE_MGR_PORT` | `8083` | `8083` | HTTP/REST 监听端口 |
| `DATASOURCE_MGR_GRPC_HOST` | `0.0.0.0` | `0.0.0.0` | gRPC 服务监听绑定地址 |
| `DATASOURCE_MGR_GRPC_PORT` | `50053` | `50053` | gRPC 服务监听端口 |
| `DATASOURCE_MGR_TLS_ENABLED` | `false` | `true` | 是否启用 TLS 1.3 / 国密 SM2 双向 mTLS |
| `DATASOURCE_MGR_TLS_CERT_FILE` | `""` | `/etc/certs/server.crt` | 服务端 X.509 证书路径 |
| `DATASOURCE_MGR_TLS_KEY_FILE` | `""` | `/etc/certs/server.key` | 服务端私钥路径 |
| `DATASOURCE_MGR_TLS_CA_FILE` | `""` | `/etc/certs/ca.crt` | 客户端认证根 CA 证书路径 |
| `DATASOURCE_MGR_TLS_CLIENT_AUTH` | `""` | `require` | 客户端证书模式 (`require` / `verify`) |
| `DATASOURCE_MGR_TLS_ALLOWED_CNS` | `""` | `service-hub` | 允许连接的客户端证书 CN 白名单 |
| `DATASOURCE_MGR_API_KEY` | `""` | `sec_privshield_token_2026` | 统一 API Key 鉴权口令（为空则免 Token） |
| `DATASOURCE_MGR_LOG_LEVEL` | `info` | `debug` / `info` | 日志级别 (`debug`, `info`, `warn`, `error`) |
| `DATASOURCE_MGR_LOG_FORMAT` | `json` | `json` | 日志输出格式 (`json` 推荐结构化日志) |

---

### 7.3 数据局端联调自测命令与验收清单

在完成数据局端服务程序的开发后，请按照以下命令进行接口自测与交付验收：

#### 1. HTTP REST 接口自测命令

```bash
# 1. 存活探针检查
curl -s http://127.0.0.1:8083/health | jq .

# 2. 查询数据源资产列表
curl -s http://127.0.0.1:8083/api/datasources | jq .

# 3. 测试医保数据源连通性
curl -s -X POST http://127.0.0.1:8083/api/datasources/ds_yibao/test | jq .

# 4. 探查医保数据源 Schema 结构 (必须包含 19 字段)
curl -s http://127.0.0.1:8083/api/datasources/ds_yibao/metadata | jq .

# 5. 按身份证号查询医保数据源单条记录 (推荐规范)
curl -s "http://127.0.0.1:8083/api/datasources/ds_yibao/record-by-id?id_card_no=110101196809171010" | jq .

# 6. 按身份证号查询康养数据源单条记录 (推荐规范，必须包含 27 字段)
curl -s "http://127.0.0.1:8083/api/datasources/ds_kangyang/record-by-id?id_card_no=110105198402151071" | jq .

# 7. 异常测试：查询未知数据源 (应返回 404 及标准 5 字段信封)
curl -s -i "http://127.0.0.1:8083/api/datasources/ds_unknown/record-by-id?id_card_no=110101196809171010"
```

#### 2. gRPC 接口自测命令 (使用 `grpcurl`)

```bash
# 1. gRPC 服务存活性探测
grpcurl -plaintext 127.0.0.1:50053 datasourcemgr.DataSourceManagerService/Health

# 2. gRPC 按身份证号查询医保数据记录
grpcurl -plaintext -d '{"source_id": "ds_yibao", "id_card_no": "110101196809171010"}' \
  127.0.0.1:50053 datasourcemgr.DataSourceManagerService/GetRecordByIDCard

# 3. gRPC 列出数据源资产
grpcurl -plaintext 127.0.0.1:50053 datasourcemgr.DataSourceManagerService/ListDataSources
```

#### 3. 交付验收清单 (Delivery Checklist)

- [ ] **双协议支持**：HTTP REST (:8083) 与 gRPC (:50053) 均能正常连通与响应；
- [ ] **唯一直接调用方对齐**：仅接收来自 `service-hub`（调度中枢）的请求，支持 CN 为 `service-hub` 的 mTLS 认证；
- [ ] **核心数据集字段对齐**：
  - [ ] 医保数据集 (`ds_yibao`) 严格输出本规范第 5.1 节定义的 **19 个字段**，字段名与含义完全一致；
  - [ ] 康养数据集 (`ds_kangyang`) 严格输出本规范第 5.2 节定义的 **27 个字段**，字段名与含义完全一致；
- [ ] **统一错误信封**：所有 HTTP 4xx/5xx 错误均返回包含 `code`, `message`, `detail`, `trace_id`, `timestamp` 的标准 JSON；
- [ ] **全链路追踪**：成功透传并返回 `X-Request-ID` 与 `X-Trace-ID` 响应头；
- [ ] **安全加固**：生产环境支持 TLS 1.3 / 国密 SM2 双向 mTLS 证书校验。
