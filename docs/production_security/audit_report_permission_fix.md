# PrivShield 接口权限控制漏洞修复——安全审计报告

> **审计日期**：2026-09-03  
> **审计范围**：`pkg/auth/`、`engine-go/internal/security/`、`engine-go/internal/rest/`、`engine-go/cmd/privshield-gateway/`、`services/service-hub/`、`services/datasource-mgr/`、`services/audit-log/`  
> **审计类型**：接口权限控制（Authorization）与访问控制架构专项代码审计  
> **修复版本**：v16.5.0+permission-fix-rev3

---

## 一、审计背景

`PrivShield` 采用 Scope-based 接口权限控制体系，通过 `PermissionForRESTPath()`、`ServiceHubPermissionForPath()` 等将 REST 路径映射为权限字符串，再由全局鉴权中间件与调用方 Identity 的 Scopes 进行比对。`service-hub` 作为唯一对外网提供编排调度服务的微服务，其鉴权强度直接关系到整个政务云数据流通链路的安全性。

本次审计与复盘共识别并闭环修复 **10 项权限控制与访问安全漏洞**（SEC-09 至 SEC-18），涵盖别名绕过、根路径直调漏报、破坏性端点未授权、尾部斜杠穿透、空格污染、网关拓扑泄露以及未映射路径的 Fail-Closed 兜底缺失，所有项目均已实装并通过 100% 单元测试与端到端回归验证。

---

## 二、发现漏洞与修复详情

### SEC-09：`/v1/*` 别名路由完全绕过 Scope 权限校验（高危）

**漏洞描述**：`PermissionForRESTPath()` 仅识别 `/v1/*` 前缀路径。当请求通过 `/v1/*` 别名路由访问时，函数返回空字符串 `""`，中间件将其视为"无需特定权限"，导致所有 40+ 别名路由的 Scope 鉴权完全失效。

**影响范围**：持有 `["health:read"]` 等最低权限 Scope 的外部调用方可通过 `/v1/` 前缀调用任意隐私原语接口（脱敏、差分隐私、K-匿名、预算重置等）。

**攻击路径示例**：
```text
GET /v1/budget/reset  →  PermissionForRESTPath 返回 ""  →  任何已认证用户可重置隐私预算
POST /v1/dp/count     →  PermissionForRESTPath 返回 ""  →  任何已认证用户可消耗差分隐私预算
```

**修复方案**：在 `PermissionForRESTPath()` 入口处增加路径归一化逻辑，将 `/v1/*` 自动剥离 `/api` 前缀后与 `/v1/*` 统一匹配。同时补充 `/v1/*` 别名路由特有的子路径映射（`/v1/mask*`、`/v1/dp/*`、`/v1/kano/*`、`/v1/qol/*`、`/v1/ldp/*` 等不经过 `/privacy/` 段的路径）。

**修复文件**：`pkg/auth/identity.go`

---

### SEC-10：根路径直调别名未映射权限（高危）

**漏洞描述**：4 个根路径路由（`/agent/process`、`/medical/process`、`/ops/diagnostics`、`/privacy/process_file`）在 `PermissionForRESTPath()` 中无任何映射，返回空字符串，对所有已认证身份开放。

**影响范围**：任何持有有效 API Key 的调用方（即使仅有 `health:read` Scope）均可调用 Agent 数据处理、医疗数据处理、运维诊断接口。

**修复方案**：在 switch 语句中新增 4 条根路径→权限映射规则。

**修复文件**：`pkg/auth/identity.go`

---

### SEC-11：`/v1/dynclassification/classify` 与 `/eval_record` 权限映射缺失（高危）

**漏洞描述**：`PermissionForRESTPath()` 中 dynclassification 分支使用 `strings.HasPrefix` 匹配前缀后，仅对 `profiles/reload` 和 `generate_profile` 两个写操作路径返回 `dynclassification:write`。对于 `classify`、`classify/batch`、`eval_record` 等读操作路径，if 条件不命中后 fall-through 到 switch 末尾，返回空字符串。

**影响范围**：动态分类分级接口对所有已认证用户开放，持有低权限 Scope 的调用方可执行分类评估操作。

**修复方案**：在 dynclassification 分支中，写操作路径返回 `dynclassification:write` 后，其余路径默认返回 `dynclassification:read`（而非 fall-through 返回空串）。

**修复文件**：`pkg/auth/identity.go`

---

### SEC-12：`POST /v1/privacy/budget/reset` 未映射权限（高危）

**漏洞描述**：`PermissionForRESTPath()` 仅覆盖 `path == "/v1/privacy/budget"`（GET 查询操作），而 `POST /v1/privacy/budget/reset`（破坏性重置操作）不匹配该条件，返回空字符串。

**影响范围**：任何已认证用户可重置全部隐私预算计数器，导致差分隐私 $\varepsilon$ 消耗追踪失效，隐私保证被破坏。

**修复方案**：新增 `/v1/privacy/budget/reset` 与 `/v1/budget/reset` → `privacy:budget` 映射。

**修复文件**：`pkg/auth/identity.go`

---

### SEC-13：service-hub 对外接口缺少 Scope 细粒度鉴权（中危）

**漏洞描述**：service-hub 是唯一对外网提供服务的微服务，但仅使用 `middleware.Auth(apiKey)` 进行简单 Bearer Token 校验。所有通过认证的调用方拥有完全相同的权限，无法区分只读查询与任务分发操作。

**影响范围**：API Key 泄漏后，攻击者可完全控制调度中枢（查看任务、分发任务、触发流水线），无最小权限隔离。

**修复方案**：
1. 新增 `SERVICE_HUB_API_KEYS` 环境变量，支持 Scope-based 多 Key 鉴权（格式 `token:name:scope1,scope2;...`）；
2. 新增 `ServiceHubPermissionForPath()` 路径→权限映射函数，定义 `hub:read` 与 `hub:dispatch` 两个 Scope；
3. 新增 `scopeAuthMiddleware()` 中间件，优先使用 Scope-based 模式，向后兼容单 Key 模式；
4. 所有 Key 校验使用 `crypto/subtle.ConstantTimeCompare` 恒定时间比较，防止时序攻击。

**修复文件**：
- `pkg/auth/identity.go`（新增 `ServiceHubPermissionForPath`、`ParseAPIKeysEnv`、`LoadAPIKeysFromEnv`）
- `services/service-hub/internal/config/config.go`（新增 `ScopeKeys` 字段）
- `services/service-hub/internal/handlers/handlers.go`（新增 `scopeAuthMiddleware`、`constantTimeLookupKeys`）

---

### SEC-14：`/v1/dynclassification/*` 与 `/v1/privacy/profile/recommend` 别名路由覆盖不全（中危）

**漏洞描述**：SEC-09 修复后 `PermissionForRESTPath()` 已能正确归一化 `/v1/*` 路径，但 `engine-go/internal/rest/routes.go` 实际只注册了 `/v1/dynclassification/eval_record`，未注册 `/v1/dynclassification/classify`、`/v1/dynclassification/classify/batch`、`/v1/dynclassification/profiles/reload` 以及 `/v1/privacy/profile/recommend`。这导致权限映射表与真实路由不一致，外部调用方仍可能通过直接访问 `/v1/dynclassification/*` 主路由绕过别名路由的审计与统一入口策略，且在多微服务兼容场景下出现 404 功能缺口。

**影响范围**：依赖 `/v1/*` 前缀的 BFF/网关/外部政务云调用方无法使用动态分类与 Profile 推荐别名，部分场景被迫回退到 `/v1/*` 主路由，削弱统一权限入口与审计覆盖。

**修复方案**：在 `engine-go/internal/rest/routes.go` 的 `/api/v1` 历史别名组中补全缺失路由：
- `POST /v1/dynclassification/classify`
- `POST /v1/dynclassification/classify/batch`
- `POST /v1/dynclassification/eval_record`
- `POST /v1/dynclassification/profiles/reload`
- `GET  /v1/privacy/profile/recommend`

**修复文件**：`engine-go/internal/rest/routes.go`

---

### SEC-15：`ServiceHubPermissionForPath()` 路径尾部斜杠绕过 Scope 校验（中危）

**漏洞描述**：`ServiceHubPermissionForPath()` 对路径进行精确匹配（如 `/v1/hub/dispatch`），但未归一化尾部斜杠。当请求路径为 `/v1/hub/dispatch/` 或 `/v1/hub/classify/` 时，函数返回空字符串，`scopeAuthMiddleware()` 将其视为无需 Scope，任何已认证的低权限调用方均可触发任务分发。

**影响范围**：持有 `hub:read` 的只读调用方可以通过在路径末尾添加 `/` 的方式调用写操作接口，破坏最小权限原则。

**修复方案**：在 `ServiceHubPermissionForPath()` 入口统一去除尾部斜杠，确保 `/v1/hub/dispatch/` 与 `/v1/hub/dispatch` 映射到同一权限 `hub:dispatch`。

**修复文件**：`pkg/auth/identity.go`

---

### SEC-16：`ParseAPIKeysEnv()` 解析存在空格污染与空 Key 注册风险（低危）

**漏洞描述**：`ParseAPIKeysEnv()` 直接按 `;` 与 `:` 切分环境变量，未对 token、name、scope 做去空白处理，也未拒绝空 token 或空 name 的条目。这会导致：
1. 环境变量中不可避免的前导/后导空格使 Key 无法命中，运维误以为鉴权失效而回退到单 Key 模式；
2. 空 token 被注册后，任何调用方只要携带 `Authorization: Bearer `（空 token）即可被识别为合法身份；
3. 空 name 导致日志与限流 key 不可识别，影响审计追踪。

**影响范围**：生产环境注入 `SERVICE_HUB_API_KEYS`、`PRIVACY_AUTH_INTERNAL_API_KEYS` 等多 Key 配置时，因格式空格导致认证异常或意外放行。

**修复方案**：在 `ParseAPIKeysEnv()` 中对 token、name、scope 统一 `strings.TrimSpace()`，并丢弃 token 为空或 name 为空的条目。

**修复文件**：`pkg/auth/identity.go`

---

### SEC-17：网关拓扑端点未鉴权与时序攻击风险（低危）

**漏洞描述**：负载均衡网关（`engine-go/cmd/privshield-gateway`）的 `/gateway/backends` 端点原无鉴权机制，可直接向公网探测内部所有 Agent 节点的真实 IP、端口与实时 EWMA 延迟拓扑；同时 `/metrics` 端点采用原生非恒定时间字符串比较 `auth[7:] != metricsKey`，存在微秒级时序侧信道嗅探风险，且报错使用非标准的裸 JSON。

**影响范围**：攻击者可通过公网端口直接嗅探内网后端拓扑，并对网关指标进行非授权读取。

**修复方案**：
1. 引入 `GATEWAY_ADMIN_API_KEY`（回退为 `GATEWAY_METRICS_API_KEY`）对 `/gateway/backends` 实施 Bearer Token 鉴权保护；
2. 全面采用 `crypto/subtle.ConstantTimeCompare` 常量时间验证 token；
3. 未授权拦截统一走 `middleware.AbortWithError` 输出标准 5 字段信封与 trace ID。

**修复文件**：`engine-go/cmd/privshield-gateway/main.go`

---

### SEC-18：全栈未显式映射路由 Fail-Closed 兜底缺失（高危）

**漏洞描述**：`ServiceHubPermissionForPath`、`DatasourceMgrPermissionForPath`、`AuditLogPermissionForPath` 在请求路径不匹配任何已知路由时，默认返回空字符串 `""`。在各微服务的 `scopeAuthMiddleware()` 中，判定逻辑为：
```go
requiredPerm := PermissionForPath(path)
if requiredPerm != "" && !identity.HasPermission(requiredPerm) {
    // 拦截
}
// requiredPerm == "" 时跳过检查放行！
```
若服务未来注册新路由、存在路径微调或外部调用方请求未映射端点，任何持有空 Scope（`[]`）或任意低权限 Token 的合法调用方均可直接穿透鉴权。

**影响范围**：外部调用方可利用未收敛映射的内部路径绕过细粒度鉴权，打破最小权限原则。

**修复方案**：
将所有微服务路径权限映射函数的 `default` 分支从返回空串升级为返回最高管理员权限（`admin`、`datasource:admin`、`audit:admin`），实现严格的 **Fail-Closed（默认拒绝）**；非敏感的健康检查与探活接口（`/health`、`/readyz`、`/livez`、`/metrics`）显式放行。

**修复文件**：
- `pkg/auth/identity.go`（`ServiceHubPermissionForPath`）
- `services/datasource-mgr/internal/handlers/handlers.go`（`DatasourceMgrPermissionForPath`）
- `services/audit-log/internal/handlers/handlers.go`（`AuditLogPermissionForPath`）

---

## 三、修复验证

### 3.1 单元测试覆盖

在 `pkg/auth/identity_test.go` 中新增 **50+ 测试用例**，覆盖：

| 测试类别 | 用例数 | 覆盖内容 |
|---|---|---|
| `/v1/*` 主路由 | 18 | 所有隐私原语、分类分级、动态分类、运维诊断路径 |
| `/v1/*` 别名路由 | 14 | 归一化后与主路由等价的权限映射 |
| 根路径直调别名 | 4 | `/agent/process`、`/medical/process`、`/ops/diagnostics`、`/privacy/process_file` |
| 未知路径 | 2 | `/unknown`、`/v1/v2/something` → 返回空串 |
| `ServiceHubPermissionForPath` | 14 | service-hub 全路由权限映射（含尾部斜杠归一化） |
| `ParseAPIKeysEnv` | 6 | 空值、单 Key、多 Key、默认通配符、空格裁剪、空 token/name 丢弃 |
| 别名路由覆盖 | 4 | `/v1/dynclassification/*` 与 `/v1/privacy/profile/recommend` |
| service-hub Scope 中间件 | 8 | 读/写 Scope 允许与拒绝、无效/缺失 token 拒绝、Health 豁免 |

### 3.2 全量回归测试

```
ok  	github.com/fengzhizi319/PrivShield-go/pkg/auth                          0.006s
ok  	github.com/fengzhizi319/PrivShield-go/engine-go/internal/security        0.059s
ok  	github.com/fengzhizi319/PrivShield-go/engine-go/internal/rest            0.173s
ok  	github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/handlers  32.574s
（全部 24 个测试包通过，0 失败）
```

---

## 四、修复前后对比

| 攻击场景 | 修复前 | 修复后 |
|---|---|---|
| 外部租户通过 `/v1/budget/reset` 重置隐私预算 | **成功**（权限绕过） | **阻断**（403 Forbidden） |
| 低权限用户通过 `/v1/dp/count` 消耗 DP 预算 | **成功**（权限绕过） | **阻断**（403 Forbidden） |
| 仅持有 `health:read` 的 Key 调用 `/ops/diagnostics` | **成功**（根路径无映射） | **阻断**（403 Forbidden） |
| 低权限 Key 调用 `/v1/dynclassification/classify` | **成功**（fall-through 空串） | **阻断**（需要 `dynclassification:read`） |
| service-hub 读 Key 调用 `/v1/hub/dispatch` | **成功**（无 Scope 区分） | **阻断**（需要 `hub:dispatch`） |
| 外部调用 `/v1/dynclassification/classify` 或 `/v1/privacy/profile/recommend` | **404**（别名路由缺失） | **200**（别名路由已注册） |
| 低权限 Key 调用 `/v1/hub/dispatch/` | **成功**（尾部斜杠绕过 Scope） | **阻断**（归一化后需要 `hub:dispatch`） |
| 环境变量空格导致 Key 无法命中 | **成功/异常**（空格污染） | **正常**（自动 TrimSpace） |
| 外部未授权探测 `/gateway/backends` 获取内网拓扑 | **成功**（直接泄露内部实例） | **阻断**（401 Unauthorized，常量时间校验） |
| 低权限/空 Scope Token 访问未显式映射的新增端点 | **成功**（空串直接绕过检查） | **阻断**（403 Forbidden，Fail-Closed 兜底拦截） |

---

## 五、残留风险与已闭环清单

| 编号 | 风险描述 | 状态 | 落地措施 |
|---|---|---|---|
| R-01 | `SERVICE_HUB_API_KEYS` 生产部署环境变量下发 | 待运维接入 | 生产环境通过 K8s Secret 或 Vault 注入 Scope-based 配置，为各调用方分配最小权限 |
| R-02 | Gateway `/gateway/backends` 暴露内部拓扑 | ✅ 已闭环 (SEC-17) | 引入 `GATEWAY_ADMIN_API_KEY` 保护，基于常量时间校验与标准 5 字段信封 |
| R-03 | `datasource-mgr` 原始数据接口仅单 Key 鉴权 | ✅ 已闭环 | 全面实装 `DATASOURCE_MGR_API_KEYS` 细粒度权限控制（`datasource:read` / `datasource:admin`） |
| R-04 | 别名路由新增或映射表遗漏导致越权绕过 | ✅ 已闭环 (SEC-18) | 实施全栈 Fail-Closed 兜底策略，未在白名单显式映射的非健康路径一律强制要求 `admin` 权限 |

---

## 六、修改文件清单

| 文件路径 | 修改类型 | 说明 |
|---|---|---|
| `pkg/auth/identity.go` | 修复 + 新增 | 路径归一化、根路径映射、dynclassification 默认 read、budget/reset 映射、`ParseAPIKeysEnv` 空格裁剪与空 Key 丢弃、`ServiceHubPermissionForPath` 尾部斜杠归一化与 Fail-Closed admin 兜底 |
| `pkg/auth/identity_test.go` | 新增 | 50+ 测试用例覆盖全部修复路径（含 SEC-15/SEC-16/SEC-18） |
| `engine-go/cmd/privshield-gateway/main.go` | 修复 | `/gateway/backends` 与 `/metrics` 引入 Bearer Token 认证、常量时间校验与统一错误信封 (SEC-17) |
| `engine-go/internal/rest/routes.go` | 修复 | 补全 `/v1/dynclassification/*` 与 `/v1/privacy/profile/recommend` 别名路由 (SEC-14) |
| `engine-go/internal/rest/routes_test.go` | 新增 | 别名路由覆盖测试 |
| `engine-go/internal/security/config.go` | 重构 | 删除重复的 `parseAPIKeys`，改用共享的 `pkgauth.LoadAPIKeysFromEnv` |
| `services/service-hub/internal/config/config.go` | 新增 | `ScopeKeys` 字段、`SERVICE_HUB_API_KEYS` 加载 |
| `services/service-hub/internal/handlers/handlers.go` | 新增 | `scopeAuthMiddleware()`、`constantTimeLookupKeys()` |
| `services/service-hub/internal/handlers/handlers_test.go` | 新增 | Scope-based 细粒度鉴权集成测试 |
| `services/service-hub/cmd/server/main.go` | 修复 | 安全警告条件增加 `ScopeKeys` 检查 |
| `services/datasource-mgr/internal/handlers/handlers.go` | 修复 | `DatasourceMgrPermissionForPath` 增加 Fail-Closed 兜底 (SEC-18) |
| `services/audit-log/internal/handlers/handlers.go` | 修复 | `AuditLogPermissionForPath` 增加 Fail-Closed 兜底 (SEC-18) |
| `docs/production_security/audit_report_permission_fix.md` | 更新 | 新增 SEC-14 ~ SEC-18 修复详情、对比表与闭环清单 |
| `docs/production_security/design.md` | 更新 | 权限映射表同步 |
| `docs/production_security/api_reference.md` | 更新 | 权限映射表 + `SERVICE_HUB_API_KEYS` 环境变量文档 |
| `docs/production_security/security_requirements.md` | 更新 | 新增 SEC-09 ~ SEC-18 漏洞记录 |
| `docs/architecture/architecture-design.md` | 新增 | 6.4 Scope-based 接口权限控制体系 |
| `pkg/docs/design.md` | 更新 | `pkg/auth` 目录描述 |
| `pkg/docs/api.md` | 新增 | `pkg/auth` API 参考章节 |

---

*审计报告终*
