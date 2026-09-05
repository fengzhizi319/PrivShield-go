# 数据服务调度中枢 (Service Hub) — 安全体系与等保三级/密评合规架构设计

> **版本**：v2.0.0  
> **模块定位**：数联天下 · 数盾 (`PrivShield`) 数据服务调度中枢（唯一编排入口）  
> **对标标准**：  
> - GB/T 22239-2019《信息安全技术 网络安全等级保护基本要求》（第三级）  
> - GB/T 39786-2021《信息安全技术 信息系统密码应用基本要求》（第三级）  
> - GM/T 0115-2023《信息系统密码应用测评要求》  
> - 《中华人民共和国数据安全法》《中华人民共和国个人信息保护法》  
> - DB51/T 2989—2023《四川省健康医疗大数据应用指南》  

---

## 1. 调度中枢安全定位与零信任边界架构

### 1.1 唯一编排入口（P0-2 Single Orchestration Gateway）

`Service-Hub` 是整个 PrivShield 数据流通基础设施中**对外部业务系统开放的唯一调度入口**。在企业与政务云架构中，外部模拟程序或业务系统（如 `app-lz`）**严禁跨过调度中枢直接访问底层服务**（`privacy-engine`、`mock-datasource`、`audit-log`）。

```
                                 外部网络 / 业务区 (DMZ)
                                           │
                        [外部数据申请方: app-lz BFF / 第三方系统]
                                           │ (仅暴露 :8082 REST / :50052 gRPC)
                                           ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                        数据服务调度中枢 (Service-Hub :8082 / :50052)                    │
│                                                                                        │
│   ┌───────────────────────┐  ┌───────────────────────────┐  ┌────────────────────────┐ │
│   │ 细粒度 Scope 鉴权      │  │ 数据源租户授权矩阵 (ABAC) │  │ 原始明文绝对不出域隔离 │ │
│   └───────────────────────┘  └───────────────────────────┘  └────────────────────────┘ │
└──────────────┬───────────────────────────┬───────────────────────────┬─────────────────┘
               │ ① mTLS / TLCP 国密专网     │ ② 私有网络                │ ③ 异步批量落盘
               ▼                           ▼                           ▼
┌─────────────────────────────┐ ┌─────────────────────┐ ┌────────────────────────────────┐
│ 隐私计算与分类脱敏引擎        │ │ 数据源资产管理中枢   │ │ 独立不可篡改存证中枢           │
│ Engine-Go (:8079 / :50051)  │ │ Datasource-Mgr      │ │ Audit-Log (:8084)            │
│ (三层漏斗 + 44 隐私原语)     │ │ (:8083 / :50053)    │ │ (9 要素 SM3 哈希链存证)        │
└─────────────────────────────┘ └─────────────────────┘ └────────────────────────────────┘
                                内部数据安全计算域 (Core Domain)
```

### 1.2 核心安全铁律：「原始数据切片不出域」

在政务云与医疗大数据流通场景下，本中枢确立了**「资产属地留存、可用不可见、回传严脱敏、过程全存证」**的安全原则：
1. **原始数据仅限内部流转**：`Service-Hub` 从 `mock-datasource` 抽取到的原始数据（`raw_record`），仅在内部内存中单向流向 `privacy-engine` 进行分类分级与深度脱敏；
2. **严禁向外暴露原始明文**：`Service-Hub` 面向外部调用方（如 `POST /v1/hub/fetch-and-desensitize`）的响应报文、以及异步任务结果中，**彻底剥离 `raw_record` 字段**，外部调用方物理上不可能通过 API 获取任何原始敏感切片；
3. **出域存证责任绑定（P0-6 Fail-Closed）**：任何脱敏数据出域前，必须先向独立存证节点 `audit-log` 真实提交入站与出站的哈希指纹（SM3/SHA-256）；一旦存证节点不可达或提交失败，请求立即被阻断（HTTP 502 Bad Gateway），绝不放行。

---

## 2. 纵深防御模型（Defense-in-Depth 4-Tier Security）

为了确保外部申请方（如 `app-lz`）**只能申请已授权的数据源（如 `ds_yibao`、`ds_kangyang`）的脱敏后数据**，而绝对无法触碰底层发送原始数据的接口，系统构建了 4 层安全防线：

```
       [外部请求传入]
             │
             ▼
   [Tier 1: 网络域物理/逻辑隔离] ──── 外部网络根本无法路由至 Engine-Go (:8079) / Datasource-Mgr (:8083)
             │ (通过防火墙/安全组/ClusterIP 阻断)
             ▼
   [Tier 2: 传输层双向证书鉴别] ──── mTLS / TLCP 国密双证书握手，CN 白名单 5 秒热重载
             │ (无内部合法 CA 证书直接在握手期终止)
             ▼
   [Tier 3: API 身份与 Scope 鉴权] ── Constant-Time 密钥比对，接口级 Scope 强制校验 (hub:dispatch)
             │ (缺少特定权限返回 403 FORBIDDEN)
             ▼
   [Tier 4: 数据源租户授权矩阵] ──── 检查调用者 Scopes 是否包含该数据源 (如 hub:dispatch:ds_yibao)
             │ (未授权数据源拦截为 403 UNAUTHORIZED_DATASOURCE)
             ▼
   [Tier 5: 响应出域彻底剥离原值] ── 结构体仅输出 sanitized_data，原始明文随栈析构
```

### 2.1 第一道防线：网络拓扑与端口外露收敛（无法触达）

- **网络分区**：`Service-Hub` 部署在网络边界（或反向代理网关后），对外仅开放统一调度端口（HTTP `:8082` / gRPC `:50052`）；
- **内部服务禁止映射**：`privacy-engine`（`:8079` / `:50051`）、`mock-datasource`（`:8083` / `:50053`）、`audit-log`（`:8084`）仅绑定在私有内网或容器网络，在 K8s 中使用 `ClusterIP` 且通过 `NetworkPolicy` 限制仅接受 `Service-Hub` Pod 访问；
- **攻击面收敛**：外部即使发起端口扫描，核心计算与数据节点在网络层均处于不可达（Unreachable）状态。

### 2.2 第二道防线：传输层双向证书认证与 CN 白名单（无法连接）

- **标准 TLS 1.3 / 国密 TLCP 双通道**：
  - 支持 RFC 8446 标准 TLS 1.3 mTLS 双向认证；
  - 支持 GM/T 0024-2014 国密 TLCP 双证书双向认证（签名证书 + 加密证书双通道）；
- **服务端公钥固定（SPKI Pinning）**：`SERVICE_HUB_TLS_PINNED_PUBKEY_FILE` 支持固定对端公钥指纹，彻底防御伪造 CA 或中间人攻击；
- **动态 CN 白名单（5 秒热重载）**：
  - `PRIVACY_AUTH_MTLS_WHITELIST_FILE` 维护允许访问的证书主体列表（如 `CN=service-hub`, `CN=privshield-ops`）；
  - 底层 `privacy-engine` 严格验证调用者 CN，外部程序因没有内部专管 CA 签发的专用证书，在握手阶段即被断开连接。

### 2.3 第三道防线：基于 Scope 的 API 接口权限控制（无法越权）

系统在 [`pkg/auth`](file:///home/charles/code/PrivShield-go/pkg/auth) 中实现了常量时间防时序攻击的 API Key 认证中间件：
- **时序侧信道防护**：使用 `subtle.ConstantTimeCompare` 进行密钥全量比对，防止利用响应时间差猜测 Token；
- **最小权限 Scope 划分**：
  - 内部脱敏执行权限：`agent:process`、`medical:process`、`privacy:mask`（仅内部中枢持有）；
  - 底层数据读取权限：`datasource:read`、`datasource:admin`（仅内部中枢持有）；
  - 外部流通调度权限：`hub:dispatch`、`hub:read`（签发给外部申请方）；
- **越权阻断**：外部申请方持有的 Key 绝不包含 `agent:process` 等内部权限。即便通过内网渗透尝试直调引擎接口，也会被引擎侧的 `AuthMiddleware` 拦截并返回 `403 FORBIDDEN: insufficient scope`。

#### 2.3.1 权限映射完整性保障（防「加路由忘配权限」）

Scope 鉴权采用「集中式 `path → permission` 映射」（本服务的 `pkgauth.ServiceHubPermissionForPath`），与路由注册（`RegisterRoutes`）**物理分离**。当接口数量增长时，最大的隐患是：新增路由遗漏在映射函数中登记。为此系统构建了**三层防御闭环**，保证任何新增注册接口都不会无遗漏地漏配权限：

| 层次 | 手段 | 触发时机 | 代码位置 |
|---|---|---|---|
| **运行时兜底** | fail-closed：未显式映射的路径默认归入最高 `admin` 权限，绝不因漏配而放行 | 每次请求 | `ServiceHubPermissionForPath` 的 `default` 分支 |
| **启动期审计** | 服务启动遍历全部路由，凡落入兜底 `admin`（且不在基础设施白名单）即打 `WARN` 列出 `method+path` | 进程启动 | `pkgauth.LogRoutePermissionAudit`（在 `RegisterRoutes` 末尾调用） |
| **CI 门禁** | 单测断言「全部路由均有显式映射」，一旦新增路由漏配即刻 `go test` 失败 | 每次 PR / `make test` | `TestAllRoutesHaveExplicitPermission`（`internal/handlers/route_audit_test.go`） |

> 通用审计器 `pkgauth.AuditRoutePermissions` / `LogRoutePermissionAudit` 下沉在 [`pkg/auth/route_audit.go`](file:///home/charles/code/PrivShield-go/pkg/auth/route_audit.go)，privacy-engine、service-hub、audit-log 三服务共用同一套机制，各自传入本服务的权限函数与兜底哨兵值（`admin` / `audit:admin`）。对确属「有意仅 `admin` 可见」的基础设施路由（如指标抓取），通过 `allowFallback` 白名单显式豁免，避免噪声的同时保留对新增遗漏路由的拦截力。

> **用户管理面（`/v1/auth/*`）的映射约定**：`/v1/auth/login` 与 `/v1/auth/users/register` 显式映射为公开认证路径（`IsAuthPublicPath`），未携 Token 时注入 `auth:public` 身份放行；其余 `/v1/auth/*` 路径显式映射为**空 scope**（仅需已认证），具体授权在 Handler 内按主体（ABAC：本人 / `user:read` / `user:admin`）强校验。两类映射都属于「已显式登记」，因此既不会落入 `admin` 兜底导致登录死锁（公开端点被要求管理员权限），也不会被 CI 门禁判为漏配。

### 2.4 第四道防线：数据源租户授权矩阵（ABAC / 租户数据源隔离）

在 `Service-Hub` 的 [`Dispatch`](file:///home/charles/code/PrivShield-go/services/service-hub/internal/handlers/handlers.go) 与 [`FetchAndDesensitize`](file:///home/charles/code/PrivShield-go/services/service-hub/internal/handlers/handlers.go) 入口处，实现了细粒度的数据源授权检查（ABAC）：
- **授权模型**：
  - 通配权限：超级管理员（`admin` 或 `*`）可调度所有已激活数据源；
  - 细粒度限定：外部调用者的 Scopes 中可绑定特定数据源白名单，例如：
    `token-app-lz:app-lz:hub:dispatch,hub:dispatch:ds_yibao,hub:dispatch:ds_kangyang`；
- **越权探测阻断**：
  - 当申请方请求 `ds_yibao` 或 `ds_kangyang` 时，中枢比对通过；
  - 当申请方企图请求未授权数据源（如社保 `ds_shebao`、税务等）时，中枢直接返回 `403 FORBIDDEN`，错误码 `UNAUTHORIZED_DATASOURCE`，立即终止调度，不向下游发起任何查询。

---

## 3. 管理面架构与身份认证凭证体系 (Management Plane & Authentication)

### 3.1 调度中枢双层管理面架构

`Service-Hub` 作为数据流通中枢，构建了「外部业务控制台 + 核心内置管控 API」的双层管理面：

```
┌────────────────────────────────────────────────────────────────────────┐
│                        业务管理面 (Console Layer)                      │
│                                                                        │
│   [前端控制台 app-lz Web :5174] ─── (HTTP JSON) ───► [业务专有 BFF :8085] │
└───────────────────────────────────────────────────────────────┬────────┘
                                                                │ 唯一流通通道
                                                                │ (带鉴权 Token / mTLS)
                                                                ▼
┌────────────────────────────────────────────────────────────────────────┐
│                   调度中枢核心管控面 (Service-Hub Control APIs)        │
│                                                                        │
│  ┌──────────────────────┐ ┌──────────────────────┐ ┌─────────────────┐ │
│  │ 调度编排与任务生命周期│ │ 存证链式全局验真     │ │ 运行健康与运维诊断  │ │
│  │ /v1/hub/dispatch     │ │ /v1/hub/audit/verify │ │ /ops/diagnostics│ │
│  │ /v1/hub/jobs/:id     │ │                      │ │ /health, /readyz│ │
│  │ /v1/hub/jobs/:id/canc│ │                      │ │ /metrics        │ │
│  └──────────────────────┘ └──────────────────────┘ └─────────────────┘ │
└────────────────────────────────────────────────────────────────────────┘
```

1. **业务可视化控制台 (`console/app-lz` · 数联调度之眼)**：
   - **前端界面 (`console/app-lz/web`，React 18 + TS + Vite，默认端口 `:5174`)**：
     提供流式作业监控、流水线拓扑图（Pipeline Visualizer & Topology Panel）、数据源授权矩阵状态查看与任务进度跟踪。
   - **业务 BFF (`console/app-lz/bff-go`，默认端口 `:8085`)**：
     业务系统与调度中枢之间的安全适配层。遵循**中枢唯一编排铁律**，BFF 绝不直连底层数据源或脱敏引擎，全部请求统一委托给 `Service-Hub`。
2. **核心内置管控与运维端点 (Control & Ops APIs)**：
   - **调度执行**：`POST /v1/hub/fetch-and-desensitize`、`POST /v1/hub/dispatch`（需 `hub:dispatch` 权限）
   - **作业状态监控**：`GET /v1/hub/jobs`、`GET /v1/hub/jobs/:id`（需 `hub:read` 或 `hub:dispatch` 权限）
   - **作业中止与干预**：`POST /v1/hub/jobs/:id/cancel`（需要管理员 `hub:admin` 或 `admin` 权限）
   - **存证全局验真**：`GET /v1/hub/audit/verify`（需要 `hub:admin` 或 `ops:diagnostics` 权限）
   - **系统运维诊断**：`GET /ops/diagnostics`、`GET /v1/ops/diagnostics`（需要 `ops:diagnostics` 权限）
   - **探针与指标**：`GET /health`、`GET /readyz`、`GET /livez`（需 `health:read` 权限）、`GET /metrics`（需 `ops:diagnostics` 权限）

### 3.2 登录密钥与凭证体系（API Key & Tokens）

#### 3.2.1 认证机制（云原生零信任模式）
系统舍弃了传统单体 Web 的弱口令表单与 Session/Cookie 机制（防御会话劫持与 CSRF），全面采用**常量时间 API Key（Bearer Token）细粒度权限模型 + 传输层 mTLS 双向认证**。

#### 3.2.2 凭据配置环境变量与语法
- **多角色/多租户配置（推荐生产使用）**：
  - 环境变量：`SERVICE_HUB_API_KEYS`
  - 语法格式：`token:identity_name:scope1,scope2[:expires_at]`（多条以分号 `;` 分隔）
  - 支持文件动态挂载：`SERVICE_HUB_API_KEYS_FILE`（支持热轮换）
- **单密钥模式（开发/简易环境）**：
  - 环境变量：`SERVICE_HUB_API_KEY`（等价于拥有 `*` 全局权限的默认 Token）
- **入站 HTTP 请求头携带规范**：
  ```http
  Authorization: Bearer <token>
  # 或
  X-API-Key: <token>
  ```
- **入站 gRPC 元数据携带规范**：
  ```text
  authorization: Bearer <token>  或  x-api-key: <token>
  ```

#### 3.2.3 典型预置凭据角色矩阵

| 角色类型 | 典型配置示例 (SERVICE_HUB_API_KEYS) | 持有 Scope 权限 | 适用主体 / 操作场景 |
|---|---|---|---|
| **超级/中枢管理员** | `hub-admin-secret:hub-admin:admin` 或 `...:hub:admin,ops:diagnostics` | `admin` 或 `hub:admin` | 控制台系统运维、取消异常运行中的任务、执行存证链路验真、访问诊断端点 |
| **业务租户 (app-lz)** | `app-lz-token:app-lz:hub:dispatch,hub:read,hub:dispatch:ds_yibao,hub:dispatch:ds_kangyang` | `hub:dispatch`, `hub:read` + ABAC 数据源白名单 | 业务控制台 BFF 调度授权数据源数据，尝试访问 `ds_shebao` 等未授权源直接报 403 |
| **只读监控探针** | `ops-probe-key:monitor:ops:diagnostics,health:read` | `ops:diagnostics`, `health:read` | Prometheus 采集器、K8s 探针、运维巡检系统调用 `/ops/diagnostics` 与 `/metrics` |

#### 3.2.4 调度中枢出站访问下游组件凭据

`Service-Hub` 作为中枢，向下游发起调用时代表自身服务身份，需配置以下出站凭据：
- `PRIVACY_AGENT_AUTH_KEY`：访问核心脱敏引擎 `privacy-engine`（`:8079` / `:50051`）的 Token（须具备 `agent:process`、`privacy:mask` 等计算权限）；
- `SERVICE_HUB_AUDIT_LOG_AUTH_KEY`：访问不可篡改存证服务 `audit-log`（`:8084`）的 Token（须具备 `audit:write` 存证写入权限）；
- `SERVICE_HUB_DATASOURCE_AUTH_KEY`：访问数据源资产管理服务 `mock-datasource`（`:8083`）的 Token（须具备 `datasource:read` 权限）。

### 3.3 普通用户与租户全生命周期管理系统设计 (User & Tenant Lifecycle Management)

#### 3.3.1 现状评估与重新设计动因
此前系统中鉴权主要依赖 `SERVICE_HUB_API_KEYS` 环境变量或静态文件注入，存在以下管理面缺陷：
1. **普通用户无法动态注册与自助申请**：外部合作机构、业务部门或个人开发者无法通过管理界面/API 完成身份注册与合规建档；
2. **权限授予与调整缺乏动态闭环**：权限变更依赖修改环境变量并重启进程，无法做到运行时秒级授权、降权或实时熔断；
3. **缺少账号生命周期状态管理**：无法对可疑账号实施临时冻结（Freeze）、解除冻结或彻底注销，无法满足等保三级关于“账户注销与权限回收”的强制控制点。
4. **Token 与责任主体脱节**：静态 Token 无法回溯到具体自然人或机构，审计溯源链不完整。

落地后的三条**安全边界**（与默认值）：公开自注册默认**关闭**（`SERVICE_HUB_USER_SELF_REGISTER=false`），仅在用户库为空时允许匿名创建**首个** `admin`（引导期）；其余账号一律由管理员开户；降权/冻结/注销受**最后管理员保护**约束；登录同时受**每 IP 限速**与**单账号锁定**双层防爆破控制。

为此，系统重构并建立了企业级**普通用户全生命周期与动态权限管理面**：

```
┌────────────────────────────────────────────────────────────────────────┐
│               普通用户与租户全生命周期状态机 (User Lifecycle)           │
│                                                                        │
│            [注册请求]                                                  │
│                │                                                       │
│                ▼                                                       │
│        ┌───────────────┐        管理员审批 / 动态授权                   │
│        │  待审批/初始态 │ ─────────────────────────────┐               │
│        └───────┬───────┘                              │               │
│                │ 默认启用                             ▼               │
│                ▼                              ┌───────────────┐        │
│        ┌───────────────┐     安全风控/冻结     │  正常激活态   │        │
│        │   Active 正常 ├─────────────────────►│ (持有 APIKey) │        │
│        └───────▲───────┘                      └───────┬───────┘        │
│                │                                      │                │
│                │ 解冻                                 │ 违规/离职      │
│        ┌───────┴───────┐                              ▼                │
│        │Disabled 已冻结│◄───────────────────── 权限回收/密钥失效       │
│        └───────┬───────┘                                               │
│                │ 账号注销 (Delete)                                     │
│                ▼                                                       │
│        ┌───────────────┐                                               │
│        │ Deleted 已注销│ (所有关联 Token 物理吊销，历史审计存证归档永久锁定)│
│        └───────────────┘                                               │
└────────────────────────────────────────────────────────────────────────┘
```

#### 3.3.2 角色权限矩阵与模型设计 (RBAC + ABAC)

系统结合三级等保“三权分立”原则预置 8 类角色，并支持细粒度自定义 Scope 与数据源级授权。
**唯一事实源**为 [`pkg/auth/user.go::DefaultScopesForRole`](../../../pkg/auth/user.go)，本表与代码必须一致（`KnownRoles` 为合法角色白名单，非法角色开户直接 `400 INVALID_ROLE`）：

| 角色代码 (`role`) | 角色名称 | 预置权限集合 (`scopes`) | 适用主体与管理职责 |
|---|---|---|---|
| `admin` | 超级管理员 | `*`, `user:admin`, `hub:admin`, `ops:admin`, `privacy:budget` | 平台配置、用户权限审批、全局任务干预、存证链强制验真、预算重置 |
| `operator` | 调度运营员 | `hub:dispatch`, `hub:read`, `hub:admin`, `user:read` | 日常调度流水线运维、任务重试/取消、查看用户清单 |
| `data-engineer` | 数据工程师 | `privacy:mask`, `privacy:dp`, `privacy:kano`, `medical:process`, `file:process` | 脱敏/差分隐私/K-匿名计算与医疗、文件流水线，无权管理用户与规则 |
| `compliance-officer` | 合规审计专员 | `dynclassification:read`, `dynclassification:write`, `privacy:budget`, `ops:diagnostics`, `user:read` | 分类分级标准与策略定义、预算消耗监控、合规审计 |
| `auditor` | 安全审计员 | `audit:read`, `ops:diagnostics`, `health:read`, `user:read` | 独立安全员：调取存证链、验真出域指纹、安全基线审查 |
| `developer` | 业务开发者 | `privacy:mask`, `medical:process`, `hub:dispatch`, `hub:read`, `health:read` | 外部业务部门/合作机构账号（公开自注册的默认角色），**不含 `user:read`** |
| `user` | 普通用户 | `privacy:mask`, `health:read` | 最小权限个体账号，仅可触发基础脱敏算子 |
| `guest` | 只读访客 | `health:read` | 仅可访问健康探针 |

**关键授权语义（务必与代码一致）**：

1. **`user:read` 是只读 scope**：仅可查询用户清单/详情与密钥概要，**不得**据此为他人签发或吊销密钥、改密、改权、冻结（否则任何持只读审计 scope 的账号都能越权提权）。判定函数见 `canViewUserAccount`（本人 | `user:read` | `user:admin`）与 `canManageUserAccount`（本人 | `user:admin`）。
2. **ABAC 主体绑定**：动态签发的 API Key 与登录会话在 `KeyConfig.Subject` 上绑定自然人/机构账号，认证后注入 `Identity.Subject`，使“本人自助”判定与数据源级 ABAC（`hub:dispatch:<datasource_id>`）可追溯到责任主体。
3. **特权角色不可自注册**：`admin`/`operator`/`compliance-officer`/`auditor` 属特权角色（`IsPrivilegedRole`），公开自注册通道一律强制降权为 `developer`，且禁止携带自定义 scope。
4. **越权签发拦截**：普通用户为自己签发 Key 时，申请的 scope 必须是自身已持权限的子集（`ErrForbiddenScope` → `403 FORBIDDEN_SCOPE`）。
5. **最后管理员保护**：降权、冻结、注销若会使系统失去最后一个活跃管理员，一律拒绝（`ErrLastAdmin` → `409 LAST_ADMIN`），防止管理面永久无主。

#### 3.3.3 用户管理核心 API 规范

全部端点挂载在 `/v1/auth` 命名空间（由 `pkg/auth/user_handlers.go::RegisterUserRoutes` 统一注册），成功响应为标准 5 字段信封（`code`/`message`/`data`/`trace_id`/`timestamp`），错误响应为 `code`/`message`/`detail`(可选)/`trace_id`/`timestamp`：

| 方法与路径 | 权限与访问控制 | 核心行为与联动 |
|---|---|---|
| `POST /v1/auth/login` | 公开免密 | 校验等保口令与防爆破锁定；成功下发会话 Bearer Token（默认 24h，内存态不落盘）；用户不存在、口令错误、**账号已冻结但口令错误**统一 `401` 且信封逐字段一致，抑制账号枚举；冻结状态只在口令校验通过后才披露（`403 ACCOUNT_DISABLED`）；口令超过 `MaxPasswordAge` 时响应 `data.password_expired=true` 并打 WARN 审计（提示改密，不阻断登录） |
| `POST /v1/auth/users/register` | 公开（**仅引导期首个 admin**，或显式开启自注册）/ `user:admin` | 用户库为空时允许创建首个管理员（角色必须为 `admin`，否则 `400 INVALID_BOOTSTRAP_ROLE`）；**不接受调用方自定义 scope**，否则 `400 INVALID_BOOTSTRAP_SCOPES`；引导判定与建号在同一把写锁内完成（并发竞争者恰有一个 200，其余 `409 BOOTSTRAP_CLOSED`）；无身份上下文（认证中间件未生效）的调用者不得进入管理员开户通道，一律 `403`；默认关闭公开自注册（`403 SELF_REGISTER_DISABLED`）；开启时强制降权 `developer` 并禁止特权角色与自定义 scope；**每 IP 独立限速**（键前缀 `register:`，与登录端点分别计数），超限 `429` + `Retry-After` 且不建号 |
| `POST /v1/auth/logout` | 已认证 | 吊销当前会话 Token，同一 Token 后续请求立即 `401`；长期 API Key 不适用（`404 SESSION_NOT_FOUND`） |
| `POST /v1/auth/change-password` | 本人或 `user:admin` | 校验旧口令 + 新口令等保复杂度 + 口令历史禁重用（最近 3 个）；成功后**强制吊销该用户全部会话**；bcrypt 校验与派生均在锁外执行，故写入前在**同一把写锁内复核口令哈希未被并发修改**，陈旧写入者得 `409 PASSWORD_CHANGED_CONCURRENTLY`（防止丢失更新覆盖已提交口令） |
| `GET /v1/auth/users` | `user:read` 或 `user:admin` | 输出脱敏摘要（抹除口令哈希与 Token 材料） |
| `GET /v1/auth/users/:username` | 本人或 `user:read` / `user:admin` | 用户档案 + 名下 API Key 概要 |
| `PUT /v1/auth/users/:username/permissions` | `user:admin` | 更新角色与自定义 scope；与注册路径**共用同一角色/scope 一致性口径**（`validateRoleScopeConsistency`），非特权角色不得被授予管理类 scope（`403 FORBIDDEN_SCOPE`）且无副作用；名下所有活密钥权限**毫秒级联动刷新** |
| `PUT /v1/auth/users/:username/status` | `user:admin` | `disabled` 时名下全部 Key 与会话立即失效（拦截器 `401`）；`active` 解冻后自动恢复 |
| `DELETE /v1/auth/users/:username` | `user:admin` | 删除账号，所有活跃 Token 从活密钥池注销 |
| `POST /v1/auth/users/:username/keys` | 本人或 `user:admin` | 生成 `psk_<32hex>` 随机 Token，**明文仅本次响应下发一次**，服务端只存 SHA-256 摘要；即刻载入活密钥表 |
| `GET /v1/auth/users/:username/keys` | 本人或 `user:read` / `user:admin` | 仅输出 `key_id`/`name`/`token_prefix`/`scopes`/`expires_at`，绝不回显明文 |
| `DELETE /v1/auth/users/:username/keys/:key_id` | 本人或 `user:admin` | 立即从活密钥表剔除，后续请求直接 `401` |

> **路由层与 Handler 层的权限分工**：`/v1/auth/login` 与 `/v1/auth/users/register` 在 `ServiceHubPermissionForPath` 中显式映射为公开认证路径；其余 `/v1/auth/*` 路径映射为空 scope（“仅需已认证”），具体授权在 Handler 内按主体（ABAC）强校验。这样既不会让新端点 fail-closed 落入 `admin` 兜底（由 `TestAllRoutesHaveExplicitPermission` 门禁守护），也不会因“只读 scope”而意外获得写能力。

#### 3.3.4 口令与会话安全控制常数（等保三级 G-03 / G-04 / G-14）

以下常数定义于 [`pkg/auth/user.go`](../../../pkg/auth/user.go)，属**编译期不可绕过**的硬约束：

| 控制项 | 取值 | 等保/密评对应 | 说明 |
|---|---|---|---|
| 口令存储 | `bcrypt cost=12` 加盐杂凑 | G-04 | 明文严禁落盘或进日志；bcrypt 计算（校验与派生）均在写锁外执行，避免阻塞并发认证，并因此引入锁内哈希复核（见下表“口令历史”） |
| 口令长度 | `8 ~ 72` 字节 | G-04 | 上限 72 是 bcrypt 硬限制：超出部分被**静默截断**会使两个不同口令等价，故显式拒绝（`ErrPasswordTooLong`） |
| 字符类别 | 大写/小写/数字/特殊字符 **至少 3 类** | G-04 | 不足返回 `ErrPasswordWeak` |
| 禁止包含用户名 | 含**逆序**同样拒绝 | G-04 | 返回独立哨兵错误 `ErrPasswordContainsName`，便于客户端给出可操作提示 |
| 弱口令字典 | 18 项常见弱口令前缀/全等拦截 | G-04 | `ErrPasswordBlacklisted` |
| 口令历史 | 最近 **3** 个不得重用；新旧不得相同 | G-04 | `ErrPasswordReused` / `ErrPasswordSame`；写入前在写锁内复核哈希快照，并发陈旧写入者得 `ErrPasswordChangedConcurrently` → `409` |
| 口令有效期 | **90** 天（`MaxPasswordAge`） | G-04 | 超期登录仍放行但响应 `data.password_expired=true` 并打 WARN 审计（避免到期日全员锁定造成可用性事故）；`PasswordUpdatedAt` 缺失视为未过期 |
| 连续失败锁定 | **5** 次失败 → 锁定 **15** 分钟 | G-03 | 登录成功自动清零；锁定期内即使口令正确也返回 `429 ACCOUNT_LOCKED` 并携带 `Retry-After` |
| 登录限速 | 每 IP 每分钟 **20** 次（8 分片固定窗口，键前缀 `login:`） | G-03 / 抗 DoS | 超限 `429 RATE_LIMITED` + `Retry-After`；缓解口令喷洒与“故意锁死管理员” |
| 注册限速 | 复用同一限速器，键前缀 `register:` **独立计数** | G-03 / 抗 DoS | 公开注册每次都要跑一遍 bcrypt(cost=12)，不限速即**未认证 CPU 耗尽放大器**；两端点互不牵连（注册配额打满不得连带阻断登录） |
| 会话有效期 | 默认 = 上限 **24h** | G-14 | 请求更长 TTL 自动收敛到上限；会话为**内存态**，重启即失效（不持久化凭证） |
| 并发会话配额 | 每用户 **8** 个 | G-14 | 超出淘汰最早会话 |
| API Key 有效期 | 默认 **30 天**，上限 **90 天** | G-14 | `ttl_seconds=0` 归一化为 30 天而非“永不过期”；负值/超限 `400 INVALID_TTL` |
| API Key 配额 | 每用户 **32** 个活跃 Key | 抗 DoS | 认证为 O(n) 常量时间比对，须防止密钥表无界膨胀 |
| 凭证库文件上限 | **32 MiB**（`maxUserStoreFileSize`） | 抗 DoS | 加载前 `os.Stat` 体检，超限直接拒启（防止超大/被注入文件在启动期耗尽内存） |
| 凭证库权限位 | 目录 `0700` / 文件 `0600` | G-04 / G-14 | 加载时检查组/其他位，被放开则 WARN 并立即收敛为 `0600`（best-effort，不阻断启动） |
| 过期密钥清理 | 加载期就地剔除并回写 | G-14 | `TokenHash` 为空或 `IsExpired()` 的 API Key 不再进入活密钥索引，且不残留于磁盘 |
| 特权操作审计 | 全量结构化 `auth_audit` 日志 | G-07 | 记录 `actor`/`target_user`/`result`/`reason`/`client_ip`/`trace_id`；严禁记录口令与明文 Token |
| 500 响应口径 | 统一 `internalError`：对外只回 `INTERNAL_ERROR` + 泛化文案 | G-11 | 内部错误细节（文件路径、OS 错误码、SQL/目录结构）**只落服务端日志**（事件名 `auth_internal_error`），不随响应体外泄 |

#### 3.3.5 运行时零重启动态生效（双通道活密钥）

认证内核在每个请求上都要取得“当前生效的密钥全集”。为避免热路径重复拷贝，`pkg/auth` 采用**双通道 + 版本驱动缓存**：

| 通道 | 索引方式 | 来源 | 落盘内容 |
|---|---|---|---|
| `Settings.LiveInternalKeys` | **明文 Token** | 静态 `SERVICE_HUB_API_KEYS` + `KeyStore`（`SERVICE_HUB_API_KEYS_FILE` / K8s Secret 热轮转） | 明文（由部署方控制文件权限） |
| `Settings.LiveInternalHashedKeys` | **`HashToken`（SHA-256 hex）** | `UserStore` 动态用户 API Key 与登录会话 | **仅摘要 + bcrypt 口令哈希**，明文 Token 永不落盘 |

- **版本驱动聚合器**（`pkg/auth/live_keys.go::Aggregator`）：静态密钥与热轮转密钥合并后的快照仅在任一来源版本号变化时重建，其余请求零分配复用同一份**只读共享快照**；重建时对配置源做深拷贝，快照与来源内部状态无指针别名。
- **毫秒级联动**：权限调整、冻结/解冻、Key 签发/吊销、改密、注销都会 `bump` 版本号，下一次认证即读到新状态，无需重启进程。
- **REST 与 gRPC 凭证视图对称**：`main.go` 在构造 `handlers.Server` 后调用 `grpcserver.SetLiveAuthProviders(server.LiveAuthKeys(), server.LiveHashedAuthKeys())`，确保登录会话与动态密钥在两条协议路径上同时生效（否则会出现“REST 可登录、gRPC 一律 Unauthenticated”的双路径不对称）。
- **鉴权模式运行期动态判定**：存在静态/热轮转 Scope Key **或用户体系已开户**（`userStore.Count() > 0`）→ Scope 细粒度鉴权（纯动态开户部署在引导期创建首个 admin 后自动从开发免密透传收敛为强制鉴权）；其余情形 → 遗留单 Key 模式，保持向后兼容。
  > **安全要点（不得回退）**：判定条件**不包含**「`SERVICE_HUB_API_KEY` 是否非空」。若把「遗留单 Key 非空」当作退回遗留模式的条件，则已开户的用户体系（动态密钥、登录会话、失败锁定、冻结/注销、ABAC 数据源授权）会被**永久整体旁路**——任何仅持有遗留单 Key 的调用者都能绕过用户级最小权限访问全部管理面。正确做法是让两者**共存**：走 Scope 鉴权，同时在 `scopeHandler` 内以 `subtle.ConstantTimeCompare` 接受遗留单 Key 并映射为 `legacy-api-key` + `"*"` 的**内部身份**（语义与遗留模式一致），避免历史部署在首次开户瞬间全量 `401`；迁移完成后运维应清空 `SERVICE_HUB_API_KEY`。
- **持久化保障**：`UserStore` 采用临时文件写入 + `os.Rename` 原子替换，并在 rename 后对**父目录 fsync**（否则掉电可能只留下目录项而未落盘 inode，重启后凭证库回退到旧版本），目录 `0700`、文件 `0600`；服务重启后账号、口令哈希与**有效 API Key 摘要**自动无损恢复（会话 Token 因内存态而失效，需重新登录）。

#### 3.3.6 用户体系环境变量

| 变量 | 默认值 | 用途 |
|---|---|---|
| `SERVICE_HUB_USER_STORE_FILE` | 空（纯内存） | 用户与动态密钥持久化文件路径（如 `data/users.json`）；只写入摘要与口令哈希 |
| `SERVICE_HUB_USER_SELF_REGISTER` | `false` | 是否开放公开自注册（生产建议保持关闭，账号一律由管理员开户） |
| `SERVICE_HUB_USER_SESSION_TTL` | `24h` | 登录会话有效期，支持 `24h`/`15m` 或纯秒数；超过 24h 自动收敛 |
| `SERVICE_HUB_USER_LOGIN_THROTTLE_PER_MIN` | `20` | 登录端点每 IP 每分钟最大尝试次数，`<=0` 关闭该层限速 |

> 变量名以 [`pkg/auth/user_handlers.go::userPolicyEnvTable`](../../../pkg/auth/user_handlers.go) 为唯一事实源（privacy-engine 侧前缀为 `AGENT_AUTH_`），编排清单与本表须保持一致，否则会被 `scripts/check_orchestration_env_consistency.sh` 门禁判定为幽灵变量。

#### 3.3.7 引导期与反枚举加固（安全审计回合）

下表汇总一轮专项安全审计发现并收敛的风险，逐项已由 [`pkg/auth/security_hardening_test.go`](../../../pkg/auth/security_hardening_test.go) 与 [`handlers_test.go`](../internal/handlers/handlers_test.go) 回归锁定：

| 风险 | 攻击路径 | 收敛措施 |
|---|---|---|
| 引导期 TOCTOU | 用户库尚为空时并发打 `/v1/auth/users/register`，多个请求同时读到“空库”并各自建号 | 引导判定与建号合并到 `RegisterBootstrapAdmin` 的**同一把写锁**内，恰有一个胜出，其余 `ErrBootstrapClosed` → `409 BOOTSTRAP_CLOSED` |
| 匿名自封管理员 | 认证中间件未生效（`GetIdentity` 为 nil）时走管理员开户通道，自带 `role=admin` + `scopes:["*"]` | 无身份上下文一律不得进入管理员通道（`403`）；引导期**拒绝自定义 scope**（`400 INVALID_BOOTSTRAP_SCOPES`），权限只能来自 `admin` 角色预置 |
| 账号状态枚举 | 冻结账号 + 任意错误口令即返回 `ACCOUNT_DISABLED`，无需掌握口令即可测绘“存在且被冻结”的账号 | 冻结判定下沉到口令校验**之后**；未掌握口令者得到的 `401` 与“账号不存在”**信封逐字段一致** |
| 注册端点 CPU 放大 | 未认证调用者洪泛公开注册，每请求强制服务端跑一次 bcrypt(cost=12) | 注册端点接入每 IP 限速（键前缀 `register:` 与登录分开计数），超限 `429` + `Retry-After` 且不建号 |
| 角色/scope 矩阵被破坏 | 经改权路径给 `guest`/`developer` 挂上 `*`、`admin`、`user:admin` 等管理类 scope | `UpdatePermissions` 与注册路径共用 `validateRoleScopeConsistency`，非法组合 `403 FORBIDDEN_SCOPE` 且**无任何副作用** |
| 改密丢失更新 | 两个并发改密请求均在锁外校验同一份旧哈希，后提交者默默覆盖前者 | 写入前在写锁内复核哈希快照，陈旧写入者 `409 PASSWORD_CHANGED_CONCURRENTLY` |
| 凭证库脏数据/暂存风险 | 无体积上限、权限位被放开、过期密钥长期驻留、掉电后回退旧版本 | 32 MiB 上限拒启 + 加载期收敛 `0600` + 过期密钥就地清理回写 + 父目录 fsync |
| 密钥热轮转崩溃 | `ChannelSecretWatcher.Close` 关闭事件通道，与 `Push` 并发时 send on closed channel **直接 panic 拖垮进程** | `Close` 只关 `stopCh`、不关事件通道；幂等（重复调用返回 nil），`Push` 关闭后返回 error；channel 交由 GC 回收 |
| 遗留单 Key 旁路用户体系 | `SERVICE_HUB_API_KEY` 非空时被当作退回遗留模式的条件，开户后整个用户体系被永久旁路 | 模式判定只看“Scope Key 存在 **或** 用户库非空”；遗留单 Key 在 Scope 通道内以常量时间比对被接受并映射为 `legacy-api-key` 内部身份 |
| 公开身份共用令牌桶 | 身份级限流键只含 `serviceType:name:path`，所有匿名/公开调用者共用同一桶（单 IP 洪泛可拖垮全部公开端点） | 匿名与 `public` 身份追加分片因子 `RealClientIP`（等保三级 G-02 源地址维度限流），引擎侧 `internal/security/auth.go` 同口径 |

> **引导期定义**：仅指 `UserStore.Count() == 0`（用户库完全为空）的瞬间。一旦首个账号落库，窗口即永久关闭，后续开户只能由持 `user:admin` 的管理员经认证通道完成，或在显式开启 `SERVICE_HUB_USER_SELF_REGISTER=true` 时降权为 `developer` 自注册。

---

## 4. 等保三级（GB/T 22239-2019）合规性满足性分析

对标 GB/T 22239-2019《信息安全技术 网络安全等级保护基本要求》第三级技术控制项：

| 控制类 | 等保三级要求项 | Service-Hub 落地实现与代码点 | 满足度 |
|---|---|---|---|
| **安全通信网络** | **通信传输完整性** | HTTP/gRPC 全面支持 TLS 1.3 及国密 TLCP 通道；出域流量计算 9 要素 SM3 哈希链；采用消息鉴别码 HMAC 校验报文完整性。代码见 `pkg/tlsutil/tlcp.go`、`pkg/store/audit_hash.go` | **完全满足** |
| | **通信传输机密性** | 支持 SM4-CBC/GCM 国密双证书会话加密与 TLS 1.3 AES-GCM，密码套件强制选用高强度加密算法，禁止弱密码套件。 | **完全满足** |
| | **通信双方双向身份鉴别** | 生产模式强制 `SERVICE_HUB_TLS_CLIENT_AUTH=require`，配合客户端 CA 校验与 mTLS CN 白名单（`PRIVACY_AUTH_MTLS_WHITELIST_FILE`）双向核验身份。 | **完全满足** |
| **安全区域边界** | **边界访问控制** | 调度中枢作为单一暴露点（P0-2），默认拒绝未登记路由（Fail-closed 兜底返回 404/403）；内部组件通过独立端口与私网逻辑隔离。 | **完全满足** |
| | **访问控制粒度** | 基于 `pkg/auth` 实现用户/服务身份识别，粒度达到接口级（Scope）与数据源级（ABAC 细粒度数据源白名单）。 | **完全满足** |
| | **抗拒绝服务与速率限制** | 边缘限流默认全开：IP 级令牌桶（默认 100 RPS，突发 200）+ 鉴权后身份级细粒度限流（默认 50 RPS / 100 Burst，key = 身份 + 归一化路径，匿名与 `public` 身份追加客户端 IP 作为分片因子，32 分片并发安全；`/health`、`/readyz`、`/metrics` 探针豁免）；并发调度信号量控制（`taskSem` 最大并发 10 个重量级任务）。 | **完全满足** |
| **安全计算环境** | **身份鉴别** | API Key 采用常量时间查找（`ConstantTimeLookupKeys`）防时序攻击；支持 KeyStore 动态多版本轮换（G-14 支持失效时间自动失效）。 | **完全满足** |
| | **自主/强制访问控制** | 严格执行最小权限原则；外部数据申请方仅限调度脱敏数据，无底层数据源探查或直接调用脱敏算子的权限。 | **完全满足** |
| | **安全审计覆盖面** | 调度全链路日志结构化输出（`log/slog` 带 `trace_id` 与 `task_id`）；任务出域与完成必须生成链式审计存证并记录入站/出站指纹。 | **完全满足** |
| | **审计记录保护（防篡改）** | 存证数据由独立审计节点 `audit-log` 负责维护；出域指纹采用 SHA-256 / SM3 密码杂凑算法；支持定期链式验真（`/v1/hub/audit/verify`）。 | **完全满足** |
| | **输入合法性验证** | 严格校验身份证号格式（18 位且防越界）、数据源标识（`naming.ResolveInbound` 白名单机制）、任务参数合法性过滤。 | **完全满足** |
| **安全管理中心** | **三权分立与权责分离** | 架构上明确拆分为“业务调用方”（app-lz）、“调度控制中枢”（service-hub）、“数据源资产方”（mock-datasource）与“独立审计方”（audit-log），职责边界完全物理隔离。 | **完全满足** |

---

## 5. 商用密码应用安全性评估（密评 GB/T 39786-2021 第三级）满足性分析

对标 GB/T 39786-2021《信息系统密码应用基本要求》第三级：

```
┌────────────────────────────────────────────────────────────────────────┐
│             GB/T 39786-2021 第三级商用密码技术落地架构                 │
├──────────────────┬─────────────────────────────────────────────────────┤
│ 物理和环境安全   │ 依赖政务云机房与符合密码标准的密码机/专用安全装置   │
├──────────────────┼─────────────────────────────────────────────────────┤
│ 网络和通信安全   │ GM/T 0024 TLCP 国密双证书协议 (SM2 双向认证 + SM4) │
├──────────────────┼─────────────────────────────────────────────────────┤
│ 设备和计算安全   │ SM2 身份鉴别 + API Key 常量时间防侧信道比对         │
├──────────────────┼─────────────────────────────────────────────────────┤
│ 应用和数据安全   │ 出域指纹 SM3 杂凑 + 9 要素防篡改哈希链 + 字段脱敏   │
├──────────────────┼─────────────────────────────────────────────────────┤
│ 密钥管理体系     │ 派生密钥 HKDF-SM3 + 密钥生命周期轮换 + 内存清零     │
└──────────────────┴─────────────────────────────────────────────────────┘
```

### 5.1 密码算法合规性

| 国密算法 | 算法标准 | 适用场景与模块实现 |
|---|---|---|
| **SM2** (非对称密码) | GM/T 0003-2012 / GB/T 32918 | TLCP 双向身份认证（签名证书验签、加密证书密钥交换协商）；数字签名与存证验真 |
| **SM3** (密码杂凑) | GM/T 0004-2012 / GB/T 32907 | 出域数据输入/输出数字指纹、9 要素不可篡改哈希链、HMAC 消息鉴别码 |
| **SM4** (分组对称密码) | GM/T 0002-2012 / GB/T 32907 | TLCP 会话数据传输加密（SM4-CBC/GCM）；敏感字段国密可逆/不可逆掩码 |

### 5.2 网络与通信安全（TLCP 国密通道）
- **国密传输模式**：服务支持通过配置 `SERVICE_HUB_TLS_ENABLED=true` 和 `AGENT_TLS_NATIONAL_CIPHER=true`，启用纯国密 GM/T 0024 TLCP 传输协议；
- **双证书体系**：服务端配置签名证书/私钥（用于身份鉴别）与加密证书/私钥（用于密钥协商协商）；
- **出站信任配置**：在中枢调用底层 `privacy-engine` 时，配置 `PRIVACY_AGENT_URLS="tlcp://..."`，通过 `PRIVACY_AGENT_TLCP_CA_FILE` 验证引擎的 SM2 证书链。

### 5.3 应用与数据安全（出域防篡改与存证链）
- **9 要素不可篡改链**：调度中枢完成任务或同步调用后，采集包括任务 ID、数据源、API Code、操作类型、状态、输入哈希（SM3）、输出哈希（SM3）、前序哈希等 9 大核心要素；
- **无密钥/带密钥哈希链**：支持密钥化 HMAC-SM3，由权威存证服务完成哈希追加，防止内部特权人员篡改中间状态；
- **全链路溯源验真**：提供 `/v1/hub/audit/verify` 接口，按时间序列回放哈希链，一旦发现任何一条记录的输入指纹或输出指纹被篡改，即刻告警定位。

---

## 6. 生产威胁模型（STRIDE）与防御矩阵

| 威胁类型 (STRIDE) | 潜在攻击场景 | Service-Hub 系统级防御对策 |
|---|---|---|
| **S (Spoofing) 身份仿冒** | 伪造外部申请方凭据非法获取脱敏数据 | 强制 API Key 认证 + mTLS 双向证书绑定，非法 Token 常量时间比对失败直接阻断。 |
| **T (Tampering) 数据篡改** | 传输过程中篡改查询身份证号或篡改脱敏结果 | 传输层全链路 TLS 1.3 / TLCP 密文传输；存证层记录输入/输出 SM3 哈希链，事后验真防篡改。 |
| **R (Repudiation) 抵赖** | 申请方调用接口获取数据后否认发生过调用 | P0-6 强约束：出域必须同步向独立存证节点写入留痕，包含操作时间、租户身份与出域数据指纹，无法抵赖。 |
| **I (Information Disclosure) 信息泄露** | 外部申请方试图获取原始明文数据库切片 | **原始数据不出域原则**：中枢响应结构彻底剔除 `raw_record` 字段，原始明文在内部脱敏后立即析构，不提供外泄通道。 |
| **D (Denial of Service) 拒绝服务** | 恶意高频并发打垮调度中枢与底层脱敏引擎 | 32 分片并发安全令牌桶限流（边缘默认全开：IP 级 100 RPS / 200 Burst + 身份级 50 RPS / 100 Burst，匿名/公开身份按客户端 IP 再分片、探针端点豁免）；公开注册/登录端点每 IP 限速（bcrypt CPU 放大防护）；`taskSem` 并发信号量限流（最多 10 并发重任务）；超时熔断保护。 |
| **E (Elevation of Privilege) 权限越权** | 外部申请方尝试调用未授权的敏感数据源 (如跨源查社保/税务) | **Tier 4 数据源租户授权检查（ABAC）**：比对 Token 的 Scopes 是否含有指定数据源授权，越权立即抛出 403 阻断。 |

---

## 7. 生产安全部署 Checklist

在正式投产或进行等保/密评测评前，运维团队必须核对以下配置项：

- [ ] **网络层隔离**：已确认 `privacy-engine:8079` 和 `mock-datasource:8083` 未对外部网络做公网映射，且防火墙仅放行 `8082`；
- [ ] **生产零信任门禁**：配置 `SERVICE_HUB_REQUIRE_TLS=true`，确保未配 TLS 时服务拒绝启动；
- [ ] **强鉴权开启**：`SERVICE_HUB_API_KEY` 或 `SERVICE_HUB_API_KEYS` 非空，禁止免密生产运行；若已启用用户体系（`SERVICE_HUB_USER_STORE_FILE` 非空且已开户），应在迁移完成后**清空 `SERVICE_HUB_API_KEY`**，避免遗留单 Key 长期以 `"*"` 全权限共存；
- [ ] **用户体系安全基线**：`SERVICE_HUB_USER_SELF_REGISTER` 保持 `false`（或经安全审批后显式开启）；首个 `admin` 已由运维当面创建并立即改密；`SERVICE_HUB_USER_STORE_FILE` 指向持久化卷（目录 `0700`/文件 `0600`，只存摘要与口令哈希）；
- [ ] **凭证视图对称**：已确认 REST 与 gRPC 共享同一活密钥视图（`grpcserver.SetLiveAuthProviders` 已接线），登录会话与动态密钥在两条协议路径上同时生效；
- [ ] **外部申请方最小权限**：外部申请方（如 `app-lz`）签发的 API Key 仅配置特定的 `hub:dispatch:<ds_name>` 权限，未授予 `*` 或内部 Scope；
- [ ] **国密证书齐备**：TLCP 模式下，签名证书 `server-sign.crt`、加密证书 `server-enc.crt` 及 CA 证书真实有效且无过期；
- [ ] **存证服务强联动**：配置 `SERVICE_HUB_AUDIT_LOG_URLS` 并设置 `SERVICE_HUB_STRICT_STORAGE=true`，确保出域存证链路完整可用；
- [ ] **日志合规留存**：日志采用 JSON 格式输出，配置归档策略留存不少于 180 天（等保三级基线要求）。
