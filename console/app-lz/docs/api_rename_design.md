# API1/API2 跨服务接口统一命名设计

> **文档版本**：v2.0.0
> **编写时间**：2026-08-27
> **适用范围**：`console/app-lz`（Web + BFF）、`services/service-hub`、`services/datasource-mgr`、`services/audit-log`、`engine` Agent、`pkg/` 共享库
> **设计目标**：统一 API1（医保）与 API2（康养）在服务标识、REST、gRPC、JSON 字段和审计链路中的名称，消除跨服务联调时的语义漂移。
> **关联文档**：[design.md §2.4 API1/API2 数据流与全生命周期接口契约](design.md)、[api.md](api.md)、[frontend_backend_mapping.md](frontend_backend_mapping.md)、[data_lifecycle.md](data_lifecycle.md)、仓库根 `AGENTS.md`

### v2.0.0 相对 v1.0.0 的变更摘要

| 变更 | 说明 |
|---|---|
| 新增 §2 现状差异清单 | 用代码证据（文件:行）固化 15 项已核实的命名/契约漂移，作为整改基线 |
| 修正 §4.1 生命周期 | 采用 **已在 `service-hub` 实现的 6 阶段名**（`ingest→fetch→classify→desensitize→return→audit`），废弃 v1.0 自造的 `resolve/dispatch/agent_process/verify` 七段模型 |
| 修正 canonical 路径原则 | **canonical = 已实现的通用路径**（`/api/datasources/{id}/records`、`/api/audit/logs`、`/v1/medical/process`），不再把未实现的 `/api/v1/...` 设为 canonical，避免为改名而改名 |
| 新增 §5 canonical 注册表与代码落点 | 给出 Go / Python / TypeScript / Protobuf 四层单一事实源的具体文件与代码骨架 |
| 新增 §6 归一化与失败策略、§7 弃用与可观测、§11 测试守卫、§12 文档同步、§14 决策记录 | 让文档可直接实施、可验收 |
| 修正 API3 语义冲突 | 代码里预留位是 `ds_mock3`（政务）/`ds_mock4`，文档里的 `api3_shebao`（社保）是**未登记占位**，两者关系在 §10 明确 |

---

## 0. 术语与基线约定

### 0.1 术语表

| 术语 | 含义 | 出现位置 | 是否可作为持久化 ID |
|---|---|---|---|
| `api_code` | 业务 API 稳定标识，如 `api1_yibao` | 请求体、任务对象、审计记录、前端选项 value | ✅ 是 |
| `datasource_id` | 数据源实体标识，如 `ds_yibao` | REST 路径/查询参数、JSON、proto、审计 | ✅ 是 |
| `source` | 历史字段名（含义等同于 `datasource_id`） | `service-hub` 任务、BFF `DispatchRequest` | ⚠️ 仅兼容期入站 |
| `source_id` | 历史 JSON/proto 字段名 | `datasource-mgr` 响应、`SourceDataQueryRequest` | ⚠️ 仅兼容期出站 |
| `datasource` | 审计域字段名（值必须是 `ds_*`） | `audit-log` 模型与 proto | ✅ 是（字段名保留，值归一） |
| URL slug | 路径片段 `yibao` / `kangyang` | 专用读取端点 | ❌ 否（需归一化） |
| 展示名 | 面向用户的中文名 `医保结算数据接口` | UI、文档 | ❌ 否 |
| 源文件名 | `yibao.csv` / `kangyang.csv` | 数据提供者内部 | ❌ 否 |

**铁律**：一个数据源只有一个 `datasource_id`。`source`、`source_id`、slug、中文名、文件名都只是它的**表现形式**，任何一层把它当作 ID 继续向下游传递都算缺陷。

### 0.2 生命周期基线（以代码实现为准）

`service-hub` 的流水线阶段名（`services/service-hub/internal/handlers/handlers.go:378`、`internal/grpcserver/server.go:328`）：

```text
ingest → fetch → classify → desensitize → return → audit   （终态 stage = "done"）
```

`classify` 与 `desensitize` 是**同一个 `engine` Agent 服务**内的两个连续子阶段，不是两个微服务；当前实现中 `service-hub` 一次调用 `POST /v1/medical/process` 即同时完成两者（`handlers.go:421` 起，`desensitize` 阶段快速通过并保留状态追踪）。

BFF 的预设数据 API 会话（`console/app-lz/bff-go/internal/handlers/handlers.go:392-497`）当前只产出 4 个阶段名 `fetch | classify | desensitize | audit`，与上面的 6 阶段基线**不一致**，需在阶段 4 归一（见 §9.2）。

---

## 1. 命名决策摘要

### 1.1 正式的 canonical 名称

| 业务 API | 中文展示名 | 英文展示名 | canonical `api_code` | canonical `datasource_id` | URL slug | 文件名 | 状态 |
|---|---|---|---|---|---|---|---|
| API1 | 医保结算数据接口 | Medical Insurance Settlement API | `api1_yibao` | `ds_yibao` | `yibao` | `yibao.csv`（19 字段） | active |
| API2 | 康养健康档案接口 | Elderly-Care Health Record API | `api2_kangyang` | `ds_kangyang` | `kangyang` | `kangyang.csv`（27 字段） | active |
| 占位 3 | 预留政务数据源 3 | Reserved Municipal Dataset 3 | — | `ds_mock3` | `mock3` | `mock3.csv` | reserved |
| 占位 4 | 预留企业/金融数据源 4 | Reserved Enterprise Dataset 4 | — | `ds_mock4` | `mock4` | `mock4.csv` | reserved |
| API3（未登记） | 社保数据接口 | Social Insurance API | `api3_shebao` | `ds_shebao` | `shebao` | `shebao.csv` | 仅命名预留，见 §10 |

`api1_yibao` / `api2_kangyang` 是跨服务**业务标识**；`ds_yibao` / `ds_kangyang` 是**数据源实体标识**。两者一一映射且写死在 §5 注册表里，任何服务都不得再为同一数据源另造 ID。

### 1.2 不使用 `v1_kangyang`、`v2_shebao` 作为业务名称

`v1` / `v2` 只表示**接口协议版本**，不表示 API 序号或数据源顺序：

```text
协议版本：/api/v1/... 、/v1/...
业务 API：api1_yibao / api2_kangyang
数据源 ID：ds_yibao / ds_kangyang
```

把 `v1_kangyang` 解释成康养、`v2_shebao` 解释成社保，会将协议版本与业务编号焊死，接口升级（`v1`→`v2`）时必然产生歧义。面向用户展示时统一使用 `API1 · 医保`、`API2 · 康养`，内部只传 canonical code 与 `datasource_id`。

### 1.3 大小写与字符集规则

| 对象 | 规则 | 示例 | 禁止 |
|---|---|---|---|
| URL path 片段、JSON 值、env 值、数据源 ID | 小写 `snake_case` / 小写 slug | `ds_yibao`、`api1_yibao` | `Ds_Yibao`、`yibao.csv`（作 ID）、`医保`（作 ID） |
| `datasource_id` 字面格式 | `^ds_[a-z][a-z0-9_]{1,30}$` | `ds_yibao` | `ds-` 前缀缺失、纯 slug `yibao` |
| `api_code` 字面格式 | `^api[1-9]_[a-z][a-z0-9_]{1,30}$` | `api1_yibao` | `API1`、`api_1` |
| Go 类型/常量/函数、RPC 方法、TS 类型 | `PascalCase` | `DataSourceID`、`GetData` | `get_yibao_data` |
| Python 常量 / 模块 | `UPPER_SNAKE` / `snake_case` | `DS_YIBAO` | `dsYibao` |
| 文档与产品标题 | `API1`、`API2` | `API1 · 医保结算数据接口` | 代码里出现 `API1` 字面值 |

---

## 2. 现状差异清单（Drift Inventory）

> 以下 15 项均为本次逐文件核对结果（非推测）。整改阶段列对应 §9.2 的迁移阶段编号。

| 编号 | 发现 | 证据 | 影响 | canonical 目标 | 阶段 |
|---|---|---|---|---|---|
| D-01 | BFF 请求 `GET /api/v1/datasources`，但 `datasource-mgr` 只注册 `/api/datasources` | `console/app-lz/bff-go/internal/clients/clients.go:324` vs `services/datasource-mgr/internal/handlers/handlers.go:77` | 数据源列表恒 404 → 永久回落 `defaultDatasources()` 硬编码，前端看似正常实则从未读到真实资产 | `GET /api/datasources`（出站改真实路径） | 1 |
| D-02 | BFF 请求 `/api/v1/audit/logs`、`/api/v1/audit/verify`；真实为 `/api/audit/logs`、`/api/audit/snapshots/verify` | `clients.go:491`、`clients.go:554` vs `services/audit-log/internal/handlers/handlers.go:59-64` | 审计大屏与 Merkle 验真恒降级；`VerifyAudit` 甚至合成 `MerkleValid: true` + 固定根哈希 | `GET /api/audit/logs`、`POST /api/audit/snapshots/verify` | 1 |
| D-03 | `InvokeDataApi` 的 `audit` 阶段只调 `GetAuditLogs(1,0)` 探活，却写 `SHA-256 存证已写入 audit-log` 并伪造 `audit_entry_id = "audit-<sessionID>"` | `handlers.go:474-492` | 审计契约造假，`audit_entry_id` 无法反查 | 真实 `POST /api/audit/logs` 返回的 `id`，或显式标记 `skipped` | 3 |
| D-04 | BFF 审计模型与 audit-log 真实模型字段名完全不同（`source`/`data_hash`/`operator`/`encryption`/`result` vs `datasource`/`input_hash`/`user`/`algorithm`/`status`） | `console/app-lz/bff-go/internal/models/models.go:213-223` vs `services/audit-log/internal/models/models.go:6-23` | 即使路径修正，反序列化也只能得到空对象 → 继续走降级 | 以 audit-log 模型为 canonical 重写 BFF 模型 | 1 |
| D-05 | `audit-log` 无 `task_id` 字段，`RecordAudit` 请求也没有 | `services/audit-log/internal/models/models.go`、`proto/auditlog.proto`（`RecordAuditRequest` 字段 1–13） | §13「调用链可追踪到 `task_id`」当前不可实现 | 新增 `task_id`（proto 字段 14）+ `api_code`（字段 15） | 3 |
| D-06 | `audit-log` 对 `datasource` 值不做任何白名单校验（仅校验 `operation`/`status`/`security_level`） | `services/audit-log/internal/handlers/handlers.go:178-192` | 可写入 `yibao`、`医保`、`yibao.csv` 等脏值，存证维度不可聚合 | 白名单：`ds_*` 且必须存在于注册表 | 3 |
| D-07 | 降级兜底里的 `operation: "classify_and_mask"` 不在 `validation.AuditOperations`（`mask/classify/k_anon/dp/qol`）白名单内 | `clients.go:539` vs `pkg/validation/validation.go:72-73` | 兜底数据一旦被回写即 400；操作名也存在漂移 | 统一用 canonical 操作名 | 1 |
| D-08 | `datasource-mgr` JSON 字段三名并存：目录用 `id`、读取响应用 `source_id`、元数据/访问审计用 `datasource_id` | `services/datasource-mgr/internal/models/models.go:11,23,58,66` | 上游消费者必须按端点记忆不同字段名 | 出站新增 `datasource_id` 双写，`source_id` 兼容保留 | 1 |
| D-09 | `service-hub` 取数按 `strings.Contains` 关键字（含中文「医保」「康养」）模糊路由，且只有专用端点 | `services/service-hub/internal/datasource/client.go:167-175` | 新增数据源需改代码；`ds_mock3/4` 未覆盖；中文名参与路由 | `FetchData(ctx, datasourceID, limit, offset)` 精确映射 | 2 |
| D-10 | `service-hub` 建任务只认 `source`，仅做非空与长度校验，值原样入库、原样透传 | `services/service-hub/internal/handlers/handlers.go:53,300-312,321` | `yibao` 与 `ds_yibao` 会同时出现在任务表里，无法聚合 | 入站接受别名 → `NormalizeDataSourceID` → 只存 canonical | 2 |
| D-11 | BFF `GetDatasourceSlice` 用 `if dsID == "ds_kangyang" \|\| "kangyang" → kangyang else → yibao`，任何未知 ID **静默落到医保** | `console/app-lz/bff-go/internal/clients/clients.go:377-386` | 错误数据源被当成正确数据源返回，最危险的漂移 | 显式映射 + 未知 ID 返回 `INVALID_DATASOURCE_ID` | 1 |
| D-12 | 阶段名漂移：hub 6 阶段 vs BFF 会话 4 阶段（缺 `ingest`/`return`）vs v1.0 文档 7 段 | `handlers.go:378` vs `bff-go/internal/handlers/handlers.go:392-497` | 「同一阶段」在不同面板名字不同，指标无法对齐 | 全站统一 6 阶段名（§4.1） | 4 |
| D-13 | 预设 API 契约用数字 `api_id`（`binding:"min=1,max=4"`），无 `api_code`；预留位是 `mock3/mock4` 而非 `shebao` | `bff-go/internal/models/models.go:243-257`、`handlers.go:515-560`、`web/src/api/client.ts:227-230` | 顺序即语义，插新 API 会静默改变已有编号含义；前端与 UI 文案（社保/政务）易混淆 | `api_code` 为主键，`api_id` 降级为展示序号 | 4 |
| D-14 | 前端硬编码兜底定义与 BFF canonical 定义字段集不同（`record_id/patient_name/id_card...` vs `insurance_settlement_id/person_id/...` 18 字段） | `web/src/App.tsx:186-187` vs `handlers.go:521-541` | 同名数据源在两条链路上字段语义漂移，脱敏高亮错位 | 前端兜底必须 import §5.4 注册表，禁止另写字段清单 | 4 |
| D-15 | 服务标识与配置命名漂移：拓扑节点 `id: "engine"`、配置字段 `AgentURL`、文档称 `PrivShield Agent`；文档 env 前缀 `LZ_CONSOLE_*`/`LZ_HUB_API_KEY` 与代码 `APP_LZ_*` 不符，且出站 API Key 在代码中根本不存在 | `clients.go:183`、`bff-go/internal/config/config.go:57-84`、`design.md §5.1-5.2` | 复制文档命令即失效；「四服务」命名不统一 | 服务 ID canonical = 模块目录名；env 统一 `APP_LZ_*` 并回写文档 | 4 |

> 结论：v1.0 文档只解决了「叫什么」，未解决「谁在传错名字」。本版把 canonical 注册表、归一化函数、守卫测试与降级造假整改作为一等公民。

---

## 3. 五类服务的统一命名空间

| 服务 | canonical 服务 ID（= 模块目录名） | 展示名 | API1 角色 | API2 角色 | 端口 |
|---|---|---|---|---|---|
| App-LZ BFF | `app-lz-bff` | 调度之眼 BFF | `api1_yibao` 聚合入口 | `api2_kangyang` 聚合入口 | HTTP `:8085` / gRPC `:50055` |
| 调度中枢 | `service-hub` | 数联数据服务调度中枢 | `datasource_id=ds_yibao` 任务 | `datasource_id=ds_kangyang` 任务 | `:8082` / `:50052` |
| 隐私引擎 | `engine` | 隐私与分类引擎（PrivShield Agent） | 处理医保记录 | 处理康养记录 | `:8079` / `:50051` |
| 数据源 | `datasource-mgr` | 数据源资产管理与敏感特征探查 | 注册 `ds_yibao` | 注册 `ds_kangyang` | `:8083` / `:50053` |
| 审计 | `audit-log` | 脱敏审计日志与不可篡改存证 | `datasource=ds_yibao` | `datasource=ds_kangyang` | `:8084` / `:50054` |

约束：

1. 拓扑节点 `id`、日志 `service` 字段、Prometheus `service` 标签一律取上表 canonical 服务 ID，禁止 `agent` / `privshield-agent` / `engine-agent` 混用；「Agent」只作为展示别名。
2. 分类分级与自适应脱敏同属 `engine` 服务边界。允许存在 `Classify`、`Desensitize` 两个**处理动作**名，禁止出现 `classification-service`、`desensitize-service` 这类**服务**名。
3. 数据源枚举只来自 §5 注册表。未登记的 `ds_*` 必须 fail-closed（§6.3），不得静默回落（D-11）。

---

## 4. 统一数据流接口命名

API1 与 API2 仅在 `datasource_id` 与记录 schema 上不同，接口生命周期共用一条链路：

```text
ingest → fetch → classify → desensitize → return → audit
                                   （engine Agent 内部：classify 与 desensitize 同服务）
```

### 4.1 阶段名与所属服务（canonical）

| 阶段 | 规范名 | 所属服务 | 当前实现 | 备注 |
|---|---|---|---|---|
| 1 | `ingest` | `service-hub` | ✅ | 校验 + 分配 `task_id` + 落库 |
| 2 | `fetch` | `service-hub` → `datasource-mgr` | ✅ | 取原始切片 |
| 3 | `classify` | `engine` | ✅ | 三层漏斗分级 |
| 4 | `desensitize` | `engine` | ✅（与 3 同一次调用完成） | 策略脱敏 |
| 5 | `return` | `service-hub` | ✅ | 结果装配 |
| 6 | `audit` | `audit-log` | ⚠️ 仅状态持久化，未写存证（D-03/D-05） | 需补客户端 |
| — | `resolve` | App-LZ BFF（客户端侧伪阶段） | 仅前端 | 允许，但不得写入任务 `stage` |
| — | `verify` | App-LZ BFF ↔ `audit-log` | 仅前端 | 同上 |

### 4.2 App-LZ BFF 对外接口

| 生命周期 | BFF REST | API1 参数 | API2 参数 | 现状与目标 |
|---|---|---|---|---|
| 查询定义 | `GET /api/lz/data-api/definitions` | `api_code=api1_yibao`（查询参数可选） | `api_code=api2_kangyang` | ✅ 已有；目标：响应体补 `code` 字段（阶段 4） |
| 读取并编排 | `POST /api/lz/data-api/invoke` | `{"api_code":"api1_yibao","limit":5}` | `{"api_code":"api2_kangyang","limit":5}` | 当前只收 `api_id`；目标：`api_code` 优先、`api_id` 兼容（§5.6） |
| 派发任务 | `POST /api/lz/tasks/dispatch` | `datasource_id=ds_yibao` | `datasource_id=ds_kangyang` | 当前只有 `source`；目标：双字段接受、单字段下传 |
| 查询任务 | `GET /api/lz/tasks/{id}`、`GET /api/lz/tasks` | 同一接口 | 同一接口 | ✅ 已有 |
| 租约看板 | `GET /api/lz/tasks/leases` | 同一接口 | 同一接口 | ✅ 已有 |
| 流水线 | `GET /api/lz/pipeline`（**待补**） | 可选 `api_code` 过滤 | 同左 | 当前无该端点；阶段 4 新增，转发 hub `GET /api/hub/pipeline` |
| 审计查询 | `GET /api/lz/audit/logs?datasource_id=...` | `ds_yibao` | `ds_kangyang` | BFF 转换为 audit-log 的 `datasource` 查询参数 |
| 审计验真 | `POST /api/lz/audit/verify` | 同一接口 | 同一接口 | ✅ 已有（需修 D-02 路径） |
| 指标 | `GET /api/lz/metrics`、`/api/lz/metrics/parsed` | 同一接口 | 同一接口 | ✅ 已有 |

> v1.0 列出的 `POST /api/lz/audit/events` **不作为 canonical**：BFF 侧审计写入不是前端职责，且当前不存在。若未来需要「BFF 直接存证」，命名应为 `POST /api/lz/audit/logs`（与上游同名，纯转发），避免再引入 `events` 概念。

推荐请求体（`/api/lz/data-api/invoke`）：

```json
{
  "api_code": "api1_yibao",
  "datasource_id": "ds_yibao",
  "limit": 5
}
```

三个字段的关系：`api_code` 决定 schema 与展示；`datasource_id` 决定取数来源，缺省时由 `api_code` 推出；两者同时给出且不一致时返回 `400 INVALID_REQUEST`（冲突优先级高于猜测）。

### 4.3 datasource-mgr

| 能力 | canonical REST（= 已实现） | API1 示例 | API2 示例 | 备注 |
|---|---|---|---|---|
| 目录 | `GET /api/datasources` | 同接口 | 同接口 | BFF 需修正 `/api/v1/` 前缀（D-01） |
| 详情 | `GET /api/datasources/{id}` | `ds_yibao` | `ds_kangyang` | 路径 `{id}` 接受别名，响应回 canonical |
| schema | `GET /api/datasources/{id}/metadata` | `ds_yibao` | `ds_kangyang` | ✅ 已实现，字段名用 `datasource_id` |
| 记录读取 | `GET /api/datasources/{id}/records?limit=&offset=` | `ds_yibao` | `ds_kangyang` | ✅ 已实现（`limit` 默认 20、上限 500） |
| 记录采样 | `GET /api/datasources/{id}/sample` | `ds_yibao` | `ds_kangyang` | 现为 `records` 别名，保留 |
| 连通测试 | `POST /api/datasources/{id}/test` | `ds_yibao` | `ds_kangyang` | ✅ |
| 专用读取（兼容） | `GET /api/v1/yibao`、`GET /api/v1/kangyang`、`/api/v1/mock3`、`/api/v1/mock4` | `yibao` | `kangyang` | **弃用中**，加弃用头（§7.1） |

canonical 决策说明：

- **不**把 `/api/datasources/...` 整体迁到 `/api/v1/...`。`/api/v1/` 前缀目前只覆盖 4 个专用端点，全量迁移收益低于回归风险。若未来上 `/api/v1` 版本组，必须成组提供别名（`api` 与 `api/v1` 两个 gin group 指向同一 handler），并保证探针路径 `/health`、`/readyz`、`/api/health` 不变。
- v1.0 提出的 `records:sample`、`{id}:test` 冒号动作风格**不采纳**：现有实现已是 `/sample`、`/test`，两个数据源风格已一致。

gRPC 目标（现状：`GetYibaoData` / `GetKangyangData` / `GetMockData3` / `GetMockData4` / `GetDataBySource` / `ListMockSources` / `GetDataSource` / `TestConnection` / `Health`）：

```protobuf
service DataSourceManagerService {
  // canonical：与数据源无关的通用读取
  rpc GetData(DataRequest) returns (DataQueryResponse);
  rpc ListDataSources(ListDataSourcesRequest) returns (ListDataSourcesResponse);
  // 现有方法保留，标注 [deprecated = true]，实现内部转发到 GetData
  rpc GetYibaoData(DataQueryRequest) returns (DataQueryResponse) { option deprecated = true; }
  rpc GetKangyangData(DataQueryRequest) returns (DataQueryResponse) { option deprecated = true; }
  rpc ListMockSources(ListMockSourcesRequest) returns (ListMockSourcesResponse) { option deprecated = true; }
}

message DataRequest {
  string datasource_id = 1;   // 必填，canonical "ds_yibao" | "ds_kangyang" | ...
  int32  limit = 2;
  int32  offset = 3;
}
```

兼容映射（实现内部单点收敛，禁止上层各自拼装）：

```text
GetYibaoData(req)     → GetData(datasource_id=ds_yibao,  limit, offset)
GetKangyangData(req)  → GetData(datasource_id=ds_kangyang, limit, offset)
GetMockData3/4(req)   → GetData(datasource_id=ds_mock3|ds_mock4, ...)
GetDataBySource(req)  → GetData(req.source_id → NormalizeDataSourceID, ...)
ListMockSources       → ListDataSources
```

若暂不改 proto：Go 客户端与文档至少统一为 `FetchData(ctx, datasourceID, limit, offset)`，上层不得再直接依赖 `FetchYibaoData` / `FetchKangyangData`（见 §5.2）。

### 4.4 service-hub

| 能力 | canonical REST（= 已实现） | 说明 |
|---|---|---|
| 提交任务 | `POST /api/hub/dispatch` | 入站接受 `source` / `datasource_id` / `datasource` 三种字段名 |
| 查询任务 | `GET /api/hub/tasks/{id}` | 响应补 `datasource_id` 双写 |
| 任务列表 | `GET /api/hub/tasks?status=&datasource_id=&operation=&limit=&offset=` | 过滤参数接受 canonical 值 |
| 流水线 | `GET /api/hub/pipeline` | 阶段名固定 6 项（§4.1） |
| 调度状态 | `GET /api/hub/status` | `store_type` 决定 BFF 租约看板模式 |
| 指标 | `GET /metrics` | Prometheus 文本 |

v1.0 的 `POST /api/v1/hub/tasks`（= `CreateTask` 语义）**降级为可选别名**，不作 canonical：现有 `/api/hub/*` 已被 3 个 Go 服务、BFF、脚本与测试广泛引用，改名只带来回归。

任务请求（canonical）：

```json
{
  "datasource_id": "ds_kangyang",
  "api_code": "api2_kangyang",
  "operation": "mask",
  "payload": {},
  "priority": 50
}
```

兼容期入站归一化顺序（必须显式、优先级固定）：

```text
datasource_id  >  datasource  >  source  >  (缺省: 由 api_code 推导)
        ↓ NormalizeDataSourceID()（pkg/naming，见 §5.2）
   ds_yibao | ds_kangyang | ds_mock3 | ds_mock4
        ↓ 未知/歧义 → 400 INVALID_DATASOURCE_ID（§6.3）
   仅 canonical 值写入 store.Task 与下游调用
```

gRPC 目标（现状：`Dispatch` / `ClassifyAndDispatch` / `GetTask` / `ListTasks` / `PipelineStatus` / `HubStatus` / `Health`）：

- proto 层 `DispatchRequest.source`（字段 1）**保留不改名**，新增 `datasource_id`（字段 5）与 `api_code`（字段 6）；服务端按上述优先级取值。旧字段号严禁复用或改类型（§5.5）。
- `ClassifyAndDispatch` 保留，语义澄清为「`Dispatch` + 强制 `classify` 策略」，不是独立服务；v1.0 的 `CreateTask` / `GetPipelineStatus` 重命名**不采纳**为 canonical，只作为客户端封装层的可选方法名糖。

任务响应必须包含：`task_id`、`api_code`、`datasource_id`、`operation`、`stage`、`status`、`created_at`、`updated_at`、`duration_ms`。

### 4.5 engine Agent

engine 不区分医保/康养：两个 API 走同一批端点与同一组 RPC，差异只在 `records` schema 与请求中的 `api_code` / `datasource_id`。

**engine 现有 REST 全部无 `/api` 前缀**（`/v1/privacy/mask`、`/v1/privacy/mask_record`、`/v1/dynclassification/eval_record`、`/v1/medical/process`、`/v1/pipeline/process_records`），因此 v1.0 的 `POST /api/v1/agent/process` 会引入第六套前缀风格。**本版决策**：

```text
canonical：POST /v1/agent/process      （新增，组合 classify + desensitize）
别名（保留）：POST /v1/medical/process        （hub 与 BFF 主链路，弃用头标记）
别名（保留）：POST /v1/pipeline/process_records
```

请求体：

```json
{
  "api_code": "api1_yibao",
  "datasource_id": "ds_yibao",
  "records": []
}
```

`/v1/agent/process` 在服务内顺序执行 `classify → policy_select → desensitize`，响应至少含 `classification_report`、`sanitized_data`、`summary`，并新增 `summary.api_code`、`summary.datasource_id`、`summary.input_hash`、`summary.output_hash`，供上游写审计（§4.6）。

> engine 已有「双路径别名」先例（`/v1/privacy/mask/batch` 与 `/v1/privacy/mask_batch`，见 `engine/routers/mask.py:50-61`）与 Python 层命名兼容测试（`tests/test_naming_conventions.py`），本方案的别名风格与之保持一致。

gRPC：`privacy.local.PrivacyService` 现有 `DynClassify`、`Mask`、`MaskRecord`、`MaskBatch`、`MaskDataFrame`、`KAnonymizeRecord`、`DP*`、`ObfuscateQuery*` 等，**没有** `ProcessRecords`。目标：

```protobuf
service PrivacyService {
  rpc ProcessRecords (ProcessRecordsRequest) returns (ProcessRecordsResponse);
}
```

过渡期由 `service-hub` / BFF 编排现有 RPC，但必须保持同一 `PrivacyService` 服务名与同一 `datasource_id`。禁止新增 `YibaoService`、`KangyangService`、`ProcessMedical` 之类按业务域复制的 RPC。

### 4.6 audit-log

| 能力 | canonical REST（= 已实现） | 说明 |
|---|---|---|
| 写入存证 | `POST /api/audit/logs` | body 字段 `datasource`，值必须 `ds_*` |
| 查询列表 | `GET /api/audit/logs?datasource=&operation=&status=&security_level=&limit=&offset=` | 查询参数与 body 同名 |
| 查询单条 | `GET /api/audit/logs/{id}` | ✅ |
| 统计 | `GET /api/audit/stats` | ✅ |
| 快照 | `GET /api/audit/snapshots`、`POST /api/audit/snapshots/verify` | Merkle/SHA-256 验真 |
| 报告 | `POST /api/audit/report` | ✅ |

v1.0 的 `/api/v1/audit/events`、`/api/v1/audit/verifications` **不采纳**：`logs` / `snapshots:verify` 已是全站（3 份 App-LZ 文档、集成脚本、E2E 测试）通用词汇，改名只制造新漂移。

写入请求（canonical，含 §5.5 新增字段）：

```json
{
  "datasource": "ds_yibao",
  "api_code": "api1_yibao",
  "task_id": "task-1787554500-eabf3934",
  "operation": "mask",
  "input_hash": "sha256:...",
  "output_hash": "sha256:...",
  "algorithm": "field_mask",
  "status": "success",
  "security_level": "L3"
}
```

gRPC 保留 `AuditLogService` 的 `RecordAudit` / `GetAuditLog` / `ListAuditLogs` / `GetAuditStats` / `ListSnapshots` / `VerifyIntegrity` / `GenerateReport`；仅按需增加资源化别名（`RecordAuditEvent` 等），**不**新增 `RecordYibaoAudit` / `RecordKangyangAudit`。

---

## 5. Canonical 注册表与代码落点

### 5.1 单一事实源（Single Source of Truth）

| 字段 | `api1_yibao` | `api2_kangyang` |
|---|---|---|
| `api_code` | `api1_yibao` | `api2_kangyang` |
| `datasource_id` | `ds_yibao` | `ds_kangyang` |
| 序号（展示用，非 ID） | 1 | 2 |
| 中文名 | 医保结算数据接口 | 康养健康档案接口 |
| 英文名 | Medical Insurance Settlement API | Elderly-Care Health Record API |
| 分类 | `medical` | `healthcare` |
| slug / 文件名 | `yibao` / `yibao.csv` | `kangyang` / `kangyang.csv` |
| 字段数 | 18 | 27 |
| 入站别名 | `yibao`、`yibao.csv`、`医保`、`medical` | `kangyang`、`kangyang.csv`、`康养`、`healthcare` |
| 状态 | active | active |
| 专用端点（弃用中） | `GET /api/v1/yibao`、`GetYibaoData` | `GET /api/v1/kangyang`、`GetKangyangData` |

注册表条目字段：`api_code, datasource_id, seq, display_zh, display_en, category, file_name, field_count, aliases[], status`。

### 5.2 Go：`pkg/naming`（新建，4 个 Go 模块共享）

`go.work` 已包含 `./pkg`，`console/app-lz/bff-go` 也已通过 `replace` 引用 `pkg`，因此新增包即可被全部 Go 侧消费者使用（无需改 `go.work`）。

```go
// pkg/naming/naming.go
package naming

// canonical api_code
const (
    API1Yibao    = "api1_yibao"
    API2Kangyang = "api2_kangyang"
)

// canonical datasource_id
const (
    DSYibao    = "ds_yibao"
    DSKangyang = "ds_kangyang"
    DSMock3    = "ds_mock3" // reserved
    DSMock4    = "ds_mock4" // reserved
)

// Entry 是注册表的一行；所有服务必须从这里派生选项与校验集。
type Entry struct {
    APICode      string
    DataSourceID string
    Seq          int
    DisplayName  map[string]string // "zh-CN" / "en-US"
    Category     string
    FileName     string
    FieldCount   int
    Aliases      []string
    Status       string // "active" | "reserved"
}

// ActiveDataSources 只返回 active 条目，供 UI 选项与写侧校验使用。
func ActiveDataSources() []string

// NormalizeDataSourceID 把任意入站表现（canonical / slug / 文件名 / 中文名 / api_code）
// 归一为 canonical datasource_id；未知或歧义值返回 ErrUnknownDataSource。
func NormalizeDataSourceID(raw string) (string, error)
```

改造点（按 D 编号）：

- `services/service-hub/internal/datasource/client.go`：以 `FetchData(ctx, datasourceID, limit, offset)` 取代 `FetchYibaoData`/`FetchKangyangData`/`FetchDataBySource` 的 `strings.Contains` 分支（D-09）。
- `services/service-hub/internal/handlers/handlers.go` + `grpcserver`：入站先归一化再落库（D-10）。
- `services/datasource-mgr/internal/handlers/data_provider.go`：把 `switch` 分支与 `FindDataSource` 的硬编码 `id == "yibao"` 判断改为查注册表（D-08）。
- `console/app-lz/bff-go/internal/clients/clients.go`：修 3 处路径（D-01/D-02）、`GetDatasourceSlice` 改精确映射并对未知 ID 报错（D-11）。
- `console/app-lz/bff-go/internal/models/models.go`：`DispatchRequest`、`AuditLogItem`、`DataApiDef` 字段与 canonical 对齐（D-04/D-13）。

### 5.3 Python：`engine/naming.py`

engine 不需要知道医保/康养，但需要能校验透传字段，避免日志/审计里出现脏 ID：

```python
# engine/naming.py
DS_YIBAO = "ds_yibao"
DS_KANGYANG = "ds_kangyang"
API1_YIBAO = "api1_yibao"
API2_KANGYANG = "api2_kangyang"

ALIAS_TO_DATASOURCE: dict[str, str] = {
    "yibao": DS_YIBAO, "yibao.csv": DS_YIBAO, "医保": DS_YIBAO, API1_YIBAO: DS_YIBAO,
    "kangyang": DS_KANGYANG, "kangyang.csv": DS_KANGYANG, "康养": DS_KANGYANG, API2_KANGYANG: DS_KANGYANG,
}

def normalize_datasource_id(raw: str | None) -> str | None: ...
```

`engine/routers/agent.py`（新增 `/v1/agent/process`）的 Pydantic 请求模型使用 `datasource_id: str`（经 `normalize_datasource_id` 归一，允许 `None`，因为 engine 对数据源无强依赖）。

### 5.4 TypeScript：`console/app-lz/web/src/types/naming.ts`

```ts
export const DATA_API = {
  api1_yibao: {
    apiCode: 'api1_yibao', datasourceId: 'ds_yibao', seq: 1,
    nameZh: '医保结算数据接口', nameEn: 'Medical Insurance Settlement API',
    category: 'medical', fieldCount: 18, status: 'active',
  },
  api2_kangyang: { /* ... */ },
} as const satisfies Record<string, DataApiDef>;

export type ApiCode = keyof typeof DATA_API;
export type DataSourceID = (typeof DATA_API)[ApiCode]['datasourceId'];
export function datasourceIdOf(apiCode: ApiCode): DataSourceID;
```

约束：`App.tsx` 的兜底 definitions、`PipelineVisualizer.tsx` 的 `source` 初值与下拉项、`TaskLifecyclePanel.tsx` 的下拉项与说明文案（`:190,228-230`）必须全部来自 `naming.ts`，不得再出现裸字符串字面量（D-14/D-15）。`ds_custom` 若保留「自定义源」演示能力，必须显式标注为**非注册演示值**并在派发前拦截。

### 5.5 Protobuf 迁移规则（硬约束）

1. 字段号只增不改不复用；删除的字段必须 `reserved <number>; reserved "<name>";`。
2. 改名通过**新增字段 + 双读**完成：`source` → `datasource_id`、`source_id` → `datasource_id`；旧字段号保留并 `// DEPRECATED: use datasource_id`。
3. 方法改名用「新增 canonical 方法 + 旧方法 `option deprecated = true` + 服务端转发」，不直接重命名（会导致客户端 gRPC 全量 404）。
4. 每次 proto 变更后同步重新生成 stub（`console/app-lz/bff-go/proto/` 四份，命令见 design.md §2.3；建议补 `make proto-gen`）。
5. 新增 `audit-log` 字段建议：`RecordAuditRequest.task_id = 14`、`api_code = 15`，`AuditLogProto` 同步。

### 5.6 JSON / 查询参数字段映射总表

| 边界 | 入站可接受 | 内部规范 | 出站返回 |
|---|---|---|---|
| BFF ← 前端 | `api_code`、`api_id`、`datasource_id`、`source` | `api_code` + `datasource_id` | 两个都返回 |
| BFF → hub | `datasource_id`、`source` | `datasource_id` | `datasource_id`（`source` 兼容双写 1 个发布周期） |
| hub → datasource-mgr | `datasource_id`（query/path） | `datasource_id` | `datasource_id`（`source_id` 兼容双写） |
| hub/BFF → audit-log | body/query `datasource` | `datasource`（值 `ds_*`） | `datasource` + 冗余 `datasource_id` |
| engine | `api_code`、`datasource_id`（可选透传） | 同左 | `summary.api_code`、`summary.datasource_id` |

原则：**字段名可以按域保留历史形态（审计域用 `datasource`），但值只能是 canonical `ds_*`。**

---

## 6. 归一化与失败策略

### 6.1 算法（各语言实现必须语义一致）

```text
1. trim + lowercase（中文别名不做 lowercase，仅精确匹配）
2. 命中 canonical 表 → 返回 canonical
3. 命中 alias 表（slug / 文件名 / 中文名 / api_code）→ 返回映射的 canonical
4. 未命中：
   - 写侧（建任务、写审计、派发取数）→ 拒绝，400 / InvalidArgument
   - 读侧过滤（列表查询参数）→ 视为过滤条件不匹配，返回空集合并记录 warn（不 500）
5. 归一化结果必须以日志字段 canonical_datasource_id 输出，便于 §7 指标统计
```

### 6.2 必须消除的「猜测式」实现

| 反模式 | 位置 | 替代 |
|---|---|---|
| `strings.Contains(s, "yibao") \|\| Contains(s, "医保")` | `service-hub/internal/datasource/client.go:172-175` | `naming.NormalizeDataSourceID` |
| `if dsID == "ds_kangyang" \|\| "kangyang" { kangyang } else { yibao }` | `bff-go/internal/clients/clients.go:381-386` | 注册表查表 + 未知即报错 |
| `if id == "yibao" && ds.ID == "ds_yibao"` 枚举式兼容 | `datasource-mgr/internal/handlers/data_provider.go:79,283-292` | 注册表别名索引 |

### 6.3 失败响应（与 BFF 统一错误体一致）

```json
{
  "error": {
    "code": "INVALID_DATASOURCE_ID",
    "message": "unknown datasource id \"shebao\"",
    "details": { "field": "datasource_id", "received": "shebao", "allowed": ["ds_yibao", "ds_kangyang"] }
  },
  "via": "app-lz-bff"
}
```

| 新增错误码 | HTTP | 触发 |
|---|---|---|
| `INVALID_DATASOURCE_ID` | 400 | 归一化未命中（写侧） |
| `AMBIGUOUS_SOURCE` | 400 | 同一别名映射到多个 canonical（当前不存在，用于防御注册表污染） |
| `API_DATASOURCE_MISMATCH` | 400 | `api_code` 与 `datasource_id` 不自洽 |
| `RESERVED_DATASOURCE` | 409 | 使用 `status=reserved` 的条目（`ds_mock3` / `ds_shebao`） |

---

## 7. 弃用标记与可观测

### 7.1 兼容端点的响应头（由共享中间件统一注入）

```http
Deprecation: true
Sunset: Mon, 01 Feb 2027 00:00:00 GMT
Link: </api/datasources/ds_yibao/records>; rel="successor-version"
X-PrivShield-Canonical-Path: /api/datasources/ds_yibao/records
X-PrivShield-Canonical-Source: ds_yibao
```

过渡期旧端点保持 `200`，不返回 `410`；下线需满足 §7.3 门槛。

### 7.2 指标与日志

| 指标 | 类型 | 标签 | 用途 |
|---|---|---|---|
| `privshield_api_alias_requests_total` | Counter | `service,alias,canonical` | 别名流量趋势，下线依据 |
| `privshield_datasource_normalize_errors_total` | Counter | `service,reason` | 脏 ID 发现率 |
| `privshield_datasource_requests_total` | Counter | `service,datasource_id,endpoint` | 真实 API1/API2 用量分布 |

日志字段：`datasource_id`（canonical）、`raw_source`（原始值）、`alias_used`（是否走了别名）。指标复用 `pkg/metrics` Collector，避免各服务重复造轮子。

### 7.3 别名下线门槛

```text
连续 2 个发布周期 privshield_api_alias_requests_total{alias=X} 增量为 0
且 3 份 App-LZ 文档、集成脚本、E2E 用例中无 X 引用
→ 才可将 X 从兼容表移除（先在注册表标记 sunset，再删除）
```

---

## 8. 跨服务 canonical 命名矩阵

| 生命周期 | App-LZ BFF | service-hub | datasource-mgr | engine Agent | audit-log |
|---|---|---|---|---|---|
| 业务标识 | `api1_yibao` / `api2_kangyang` | 同值（`api_code`） | 同值（可选透传） | 同值（`api_code`） | 同值（`api_code`，需 §5.5 新增字段） |
| 数据源 ID | `datasource_id` | `datasource_id`（`source` 仅入站兼容） | `datasource_id`（`source_id` 出站兼容） | `datasource_id` | `datasource`（值必须 `ds_*`） |
| 读取 | `POST /api/lz/data-api/invoke`、`GET /api/lz/datasources/{id}/records`（待补） | `FetchData(datasource_id)` | `GET /api/datasources/{id}/records` / `GetData` | — | — |
| 调度 | `POST /api/lz/tasks/dispatch` | `POST /api/hub/dispatch` / `Dispatch` | — | — | — |
| Agent 处理 | 会话内聚合 | `POST /v1/medical/process` → 迁至 `/v1/agent/process` | — | `/v1/agent/process` / `ProcessRecords` | — |
| 返回 | `GET /api/lz/tasks/{id}` | `GET /api/hub/tasks/{id}` / `GetTask` | — | 返回 `sanitized_data` + `summary` | — |
| 审计写入 | BFF 编排或 hub 客户端（**当前均缺失**，D-03） | `RecordAudit` client（待补） | — | 提供 `input_hash` / `output_hash` | `POST /api/audit/logs` / `RecordAudit` |
| 验真 | `POST /api/lz/audit/verify` | — | — | — | `POST /api/audit/snapshots/verify` / `VerifyIntegrity` |

### 8.1 API1 canonical 调用链

```text
App-LZ BFF   POST /api/lz/tasks/dispatch   { api_code: api1_yibao, datasource_id: ds_yibao }
   ↓
service-hub  Dispatch(datasource_id=ds_yibao) → task_id, stage=ingest
   ↓ stage=fetch
datasource   GetData(datasource_id=ds_yibao, limit, offset)   [REST: /api/datasources/ds_yibao/records]
   ↓ stage=classify|desensitize
engine       ProcessRecords(api_code=api1_yibao, datasource_id=ds_yibao, records)
   ↓ stage=return
service-hub  GetTask(task_id) → sanitized_data
   ↓ stage=audit
audit-log    RecordAudit(datasource=ds_yibao, api_code=api1_yibao, task_id, input_hash, output_hash)
   ↓
App-LZ BFF   POST /api/lz/audit/verify → VerifyIntegrity → merkle_valid + root_hash
```

### 8.2 API2 canonical 调用链

与 §8.1 完全同构，仅替换 `api2_kangyang` / `ds_kangyang`；不得出现第二套调度、Agent 或审计接口。

---

## 9. 兼容策略与迁移顺序

### 9.1 别名与兼容映射总表

| 旧名称/接口 | canonical | 处理策略 | 证据 |
|---|---|---|---|
| `yibao` / `kangyang`（slug） | `api1_yibao` / `api2_kangyang`，数据源 `ds_yibao` / `ds_kangyang` | 入站归一化，URL slug 保留 | `datasource-mgr/.../handlers.go:71-72` |
| `yibao.csv` / `kangyang.csv` | `ds_yibao` / `ds_kangyang` | 仅别名表接受，禁止作 ID | `data_provider.go:283,286` |
| `医保` / `康养`（中文） | 同上 | 仅别名表接受，不参与路由 | `datasource/client.go:172-175` |
| `source`（任务字段） | `datasource_id` | 入站兼容，内部只留 canonical | `service-hub/.../handlers.go:53`、BFF `models.go:67` |
| `source_id`（响应/proto 字段） | `datasource_id` | 出站双写 1 个发布周期 | `datasource-mgr/models.go:23`、`SourceDataQueryRequest` |
| `datasource`（审计字段） | 字段名保留，值必须 `ds_*` | 加白名单校验 | `audit-log/handlers.go:159` |
| `api_id: 1\|2` | `api_code` | `api_code` 优先，`api_id` 兼容映射到注册表 `seq` | `bff-go/models.go:255` |
| `GET /api/v1/yibao` / `/api/v1/kangyang` | `GET /api/datasources/{id}/records` | 保留 `200` + 弃用头 | `bff-go/clients.go:386` 仍在用 |
| `GET /api/v1/mock3` / `mock4` | `GET /api/datasources/ds_mock3\|ds_mock4/records` | 同上，状态 reserved | `handlers.go:73-74` |
| `GetYibaoData` / `GetKangyangData` / `GetMockData3` / `GetMockData4` | `GetData(datasource_id=...)` | proto 保留 + `deprecated` + 转发 | `datasourcemgr.proto:14-23` |
| `ListMockSources` | `ListDataSources` | 新增同名 RPC，旧为别名 | `datasourcemgr.proto:29` |
| `Dispatch` / `ClassifyAndDispatch` | 保留（`CreateTask` 不采纳） | 语义文档化，新增 `datasource_id`/`api_code` 字段 | `servicehub.proto:17,20` |
| `POST /v1/medical/process` | `POST /v1/agent/process` | 旧路由保留 + 弃用头 | `engine/routers/medical.py:12,44` |
| `POST /v1/pipeline/process_records` | `POST /v1/agent/process` | 别名保留（CSV/表格语义不同，可延后） | `engine/pipeline/router.py:23,37` |
| `POST /api/v1/audit/verify`（BFF 侧调用） | `POST /api/audit/snapshots/verify` | **纯缺陷修复**，立即改正 | `bff-go/clients.go:554` |

### 9.2 迁移阶段（每阶段可独立发布、独立回滚）

| 阶段 | 目标 | 交付物 | 前置 | 回滚方式 |
|---|---|---|---|---|
| **0：常量与路径缺陷整改** | 先修 D-01/D-02/D-04/D-07/D-11（属功能缺陷，不属改名）；新增 `pkg/naming` 常量与注册表；BFF/前端/hub 全部改引注册表 | `pkg/naming` + 4 处路径修正 + 单测 | 无 | 单点回滚 BFF 客户端文件 |
| **1：datasource-mgr 统一入口** | 新增 `GetData` / `ListDataSources`；响应双写 `datasource_id`；`data_provider` 改查表；旧 RPC 转发 | proto + handler + 契约测试 | 阶段 0 | 撤新增 RPC，双写字段无害 |
| **2：service-hub 归一化** | `NormalizeDataSourceID` 入站；`FetchData` 取代关键字路由；`Task` 双写 `datasource_id`+`source`；新增 `api_code` 字段 | 客户端与 handler + 迁移脚本（回填历史任务 `source`→`ds_*`） | 阶段 1 | 关闭归一化开关（`HUB_DATASOURCE_NORMALIZE=off`）退回旧行为 |
| **3：审计字段与真实存证** | audit-log 加 `task_id`/`api_code` + `datasource` 白名单；hub 补 `Audit` 阶段 `RecordAudit` 客户端；BFF 会话 `audit` 阶段真实写入或标 `skipped`（禁止再返回 `success`） | proto 字段 14/15 + store 迁移 + 客户端 | 阶段 2 | 白名单校验降级为 warn（feature flag） |
| **4：App-LZ 与前端切换** | `api_code` 契约、`GET /api/lz/pipeline`、6 阶段名统一、移除前端硬编码兜底、`ds_custom` 拦截；服务 ID 与 env 前缀文档回写（D-12~D-15） | BFF + web + 文档 | 阶段 3 | 前端保留 `api_id` 兼容字段，可分模块回退 |
| **5：观察期与下线** | 按 §7.3 门槛移除别名；proto 旧方法从「deprecated」进入删除候选 | 变更说明 + CHANGELOG | 阶段 4 稳定 2 个发布周期 | 恢复别名表条目 |

**顺序原则**：先修「用错名字导致的静默降级」（阶段 0），再统一命名（1-2），再补语义闭环（3），最后改对外契约（4）。反过来做会让改名掩盖真实缺陷。

### 9.3 兼容期不变式（必须始终成立）

1. 旧路径/旧 RPC/旧字段在兼容窗口内行为与新 canonical **逐字节一致**（除新增字段外）。
2. 归一化只发生在服务边界一次；内部各层只见 canonical。
3. 任何 `ds_*` 值必须存在于注册表；未知值不得静默映射到默认数据源（D-11 类缺陷的根因）。
4. 降级/兜底数据不得被标记为 `success`，必须携带 `source: "fallback"` 之类的显式来源标记（现有 `metrics/parsed` 已有此约定，审计与会话阶段应对齐）。

---

## 10. API3（shebao）预留与占位规则

现状：代码里 API3/API4 是 `ds_mock3`（政务）与 `ds_mock4`（企业/金融）两个**空壳占位**，`datasource-mgr` 已有端点与 RPC，BFF 预设定义里 `ID: 3/4` 为 `reserved` 且 `DatasourceID` 为空。文档中的 `api3_shebao`（社保）**尚未在任何代码中登记**。

决策：

1. `ds_mock3` / `ds_mock4` 是**占位条目**，`status=reserved`，不得进入前端可用列表与派发白名单（现有 `InvokeDataApi` 已按 `status != active` 拦截，保持）。
2. `api3_shebao` / `ds_shebao` 只是**命名预留**，在实现完成前不写入注册表、不出现在任何 `/definitions` 响应、不出现在 E2E 断言里。
3. 若确定社保为第三个真实数据源，则 `ds_mock3` 通过一次显式改名（新增 `ds_shebao` + `ds_mock3` 别名转发 + 弃用头）承接，而不是直接改字符串——保证历史审计记录可解释。
4. 正式启用必须同时完成：数据文件/Provider → datasource-mgr 注册（REST + gRPC + metadata）→ hub 归一化与客户端 → engine schema/策略验证 → audit-log 白名单 → App-LZ 定义、UI、i18n、E2E。

---

## 11. 测试与守卫

| 层级 | 测试 | 位置建议 | 断言要点 |
|---|---|---|---|
| Go 单测 | 注册表自洽性 | `pkg/naming/naming_test.go` | 别名映射唯一；`ActiveDataSources()` 只含 active；正则格式合法 |
| Go 单测 | 归一化 | 同上 | `yibao`/`yibao.csv`/`医保`/`api1_yibao` → `ds_yibao`；`shebao` → error |
| Go 契约 | canonical 与别名返回一致 | `services/datasource-mgr/internal/handlers` | `/api/v1/yibao` 与 `/api/datasources/ds_yibao/records` 同 `limit/offset` 下 `records` 全等，且响应含弃用头 |
| Go 契约 | 派发别名等价 | `services/service-hub/internal/handlers` | `source=yibao` 与 `datasource_id=ds_yibao` 建出的任务 `datasource_id` 相同 |
| Go 单测 | BFF 不静默回落 | `console/app-lz/bff-go/internal/clients` | 未知 `dsID` 返回 `INVALID_DATASOURCE_ID`；上游 404 时不得返回硬编码医保数据 |
| Python | 常量与路由别名奇偶 | 扩展 `tests/test_naming_conventions.py` | `/v1/agent/process` 与 `/v1/medical/process` 响应结构一致 |
| TS 单测 | 注册表快照 + 无裸字面量 | `web/src/types/__tests__/naming.test.ts` | 选项列表 = `ActiveDataSources()`；`App.tsx` 兜底 = 注册表 |
| E2E | **TS-04「API1/API2 命名一致性与全链路可追踪」** | `console/app-lz/bff-go/internal/runner`（新增用例，design.md §3.4 测试矩阵扩展） | 见下 |
| CI lint | 禁止裸数据源字面量 | `scripts/dev/lint-source-naming.sh`（新增）+ `.pre-commit-config.yaml` | 注册表文件之外不得出现 `"ds_yibao"`/`"ds_kangyang"`/`"yibao"`/`"kangyang"` 字面量 |

TS-04 断言清单：

```text
1. dispatch(api_code=api1_yibao) 与 dispatch(datasource_id=ds_yibao) 与 dispatch(source=yibao)
   → 三个任务落库 datasource_id 全等，且 == ds_yibao
2. GetTask 响应含 api_code / datasource_id / task_id / stage / status
3. 任务完成后 audit-log 可按 task_id 查到记录（依赖阶段 3）
4. 该记录的 datasource 值 ∈ 注册表；input_hash/output_hash 非空
5. VerifyIntegrity 返回 merkle_valid=true 且来自真实上游（不得是合成值）
6. 别名调用与 canonical 调用的响应 diff 仅允许出现在新增字段与弃用头
```

---

## 12. 文档同步清单

改名是跨文档动作，以下每处都需在同一 PR 或紧邻 PR 中更新，否则文档立即失真（D-15 就是这么产生的）：

| 文档 | 需同步内容 |
|---|---|
| `console/app-lz/docs/design.md` | §2.4 三张接口表（当前写的是「实现边界说明」）、§3.2 六阶段名、§5.1-5.2 env 变量真实前缀 `APP_LZ_*`、§8 目录新增 `pkg/naming` |
| `console/app-lz/docs/api.md` | §4.2 派发请求字段、§4.7 预设数据 API 请求体（`api_code`）、错误码表补 §6.3 四个新码 |
| `console/app-lz/docs/frontend_backend_mapping.md` | §8.1 定义表补 `api_code` 列、附录 A 的 BFF→上游 URL 速查表（含修正后的 3 条路径） |
| `console/app-lz/docs/data_lifecycle.md` | §7 限制表新增 G-7（BFF 上游路径 404 导致恒降级）、G-8（审计阶段未真实写入） |
| `AGENTS.md` | §13 关键文档表加入本文件；Configuration 表补 `APP_LZ_*` 前缀说明 |
| 集成脚本 | `scripts/dev/integration-test-new-modules.sh`、`e2e-start-all-services.sh` 中的路径与 `source` 值 |
| 部署说明 | `deploy/` 下若引用旧路径需同步 |

---

## 13. 验收标准

**命名一致性**

- [ ] API1 在五类服务中统一为 `api1_yibao` / `ds_yibao`；API2 为 `api2_kangyang` / `ds_kangyang`。
- [ ] `source`、`source_id`、`datasource` 三者的边界与映射在 §5.6 表中有唯一解释，代码与之一致。
- [ ] 分类分级与自适应脱敏仍属同一 `engine` 服务；仓库内检索不到 `classification-service` / `desensitize-service`。
- [ ] 除注册表文件外，全仓库无裸数据源字面量（CI lint 通过）。

**契约正确性（阻断项，先于改名验收）**

- [ ] D-01/D-02 三处 BFF 路径修正，真实 `datasource-mgr` / `audit-log` 在线时**不再触发降级兜底**（用响应 `via` / `source: fallback` 标记验证）。
- [ ] `GetDatasourceSlice` 对未知 `datasource_id` 返回错误而非默认医保数据（D-11）。
- [ ] 会话 `audit` 阶段要么真实写入并返回可反查的 `audit_entry_id`，要么显式 `skipped`；不再出现「未写入却报已存证」（D-03）。
- [ ] BFF `AuditLogItem` 字段与 `audit-log` 模型一致（D-04）。

**闭环能力**

- [ ] `audit-log` 记录含 `task_id` 与 `api_code`，可按 `task_id` 检索（D-05）。
- [ ] `datasource` 值受白名单约束（D-06）。
- [ ] API1/API2 的 canonical 链路可从 BFF 追到 `task_id` → `sanitized_data` → 审计记录 → `VerifyIntegrity` 结果（TS-04 通过）。

**兼容与可观测**

- [ ] 所有旧路径/旧 RPC 保留 `200`，带 `Deprecation` / `Sunset` / `Link` 头。
- [ ] `privshield_api_alias_requests_total` 在 Grafana 有面板，别名流量可量化。
- [ ] 每个迁移阶段有对应回滚开关或回滚说明。

**产品与文档**

- [ ] UI 展示 `API1 · 医保结算数据接口` / `API2 · 康养健康档案接口`，中英文均来自注册表。
- [ ] `api3_shebao` 未实现前不出现在任何可用列表、枚举与测试断言中。
- [ ] §12 清单中的 6 处文档已同步。

---

## 14. 决策记录与未决问题

### 14.1 决策记录（ADR 摘要）

| 编号 | 结论 | 理由 | 被否决的替代方案 |
|---|---|---|---|
| ADR-1 | canonical = **已实现的通用路径**，`/api/v1/...`、`/api/v1/hub/tasks`、`/api/v1/audit/events` 均不采纳 | 为改名而改名会把回归成本转嫁给 3 份文档 + 脚本 + 测试；真正的问题是实现侧路径本来就错（D-01/D-02） | v1.0 提出的全量 `/api/v1` 资源化路径体系 |
| ADR-2 | engine canonical 定为 `POST /v1/agent/process`（无 `/api` 前缀） | engine 全部现有路由均无 `/api` 前缀，保持服务内部一致性 | `POST /api/v1/agent/process` |
| ADR-3 | 生命周期采用已实现的 6 阶段名，`resolve`/`verify` 降级为 BFF 伪阶段 | 阶段名是跨服务可观测维度，必须与 `store.Task.Stage` 一致 | v1.0 的 7 段模型 |
| ADR-4 | 单一事实源落在 `pkg/naming`（Go）+ `engine/naming.py`（Py）+ `web/src/types/naming.ts`（TS），三层用 TS-04/lint 保证一致而非共享文件 | Go/Python/TypeScript 无法共享常量文件，只能靠契约测试对齐；集中式 YAML 会引入运行时依赖与启动期失败风险 | 用 `rules/*.yaml` 或新增配置中心下发注册表 |
| ADR-5 | 未知 `datasource_id` 在写侧 fail-closed | 静默回落是本项目最危险的命名缺陷（D-11） | 宽松匹配（现状） |

### 14.2 未决问题

| 编号 | 问题 | 需要谁决定 | 建议 |
|---|---|---|---|
| Q-1 | `api_code` 是否对前端隐藏（只暴露展示名） | 产品 | 建议保留在 API 响应中但不展示，便于排障 |
| Q-2 | `ds_custom`（前端自定义源演示值）是否纳入注册表 | 控制台 owner | 建议纳入但 `status=demo`，派发前拦截 |
| Q-3 | 历史任务表中 `source=yibao` 是否需要回填为 `ds_yibao` | 数据治理 | 建议一次性迁移脚本 + 保留原值于 `raw_source` 列 |
| Q-4 | 审计是否需要新增 `session_id`（BFF 预设 API 会话）以支持无任务存证 | 审计 owner | 建议与 `task_id` 同批新增（字段 16） |
| Q-5 | 是否统一 `/health`、`/readyz`、`/api/health` 三探针命名规范到全部 Go 服务与 BFF | 平台 | 现状已趋同，仅需文档固化 |

---

## 附录 A：现状接口与标识全量清单（核对基线）

### A.1 datasource-mgr

```text
REST  GET  /health, /readyz, /api/health
REST  GET  /api/v1/yibao | /api/v1/kangyang | /api/v1/mock3 | /api/v1/mock4     (handlers.go:71-74)
REST  GET  /api/datasources | /api/datasources/:id | :id/records | :id/sample   (handlers.go:77-80)
REST  POST /api/datasources/:id/test  |  GET :id/metadata  |  GET :id/audit     (handlers.go:81-83)
REST  POST /api/datasources/seed                                          (handlers.go:84)
gRPC  Health / GetYibaoData / GetKangyangData / GetMockData3 / GetMockData4
      GetDataBySource / ListMockSources / GetDataSource / TestConnection   (datasourcemgr.proto:11-35)
IDs   ds_yibao / ds_kangyang / ds_mock3 / ds_mock4                        (data_provider.go:28-55)
分页  limit 默认 20 上限 500，offset 默认 0                                 (handlers.go:138, parsePagination:92)
```

### A.2 service-hub

```text
REST  GET /api/hub/status | /api/hub/tasks | /api/hub/tasks/:id | /api/hub/pipeline | /metrics
REST  POST /api/hub/dispatch                                              (handlers.go:137-148)
gRPC  Health / HubStatus / Dispatch / ClassifyAndDispatch / GetTask / ListTasks / PipelineStatus
字段  DispatchRequest{source, operation, payload_json, priority}
阶段  ingest → fetch → classify → desensitize → return → audit，终态 done
操作  validation.HubOperations = mask | k_anon | dp | classify | none
Agent 调用  POST /v1/dynclassification/eval_record、/v1/privacy/mask、/v1/privacy/mask_record、
            /v1/medical/process                                            (internal/agent/client.go:57-107)
审计  无 RecordAudit 客户端（§2 D-03/D-05）
```

### A.3 audit-log

```text
REST  GET /api/audit/logs | /api/audit/logs/:id | /api/audit/stats | /api/audit/snapshots
REST  POST /api/audit/snapshots/verify | /api/audit/report                 (handlers.go:59-65)
gRPC  Health / RecordAudit / GetAuditLog / ListAuditLogs / GetAuditStats /
      ListSnapshots / VerifyIntegrity / GenerateReport                      (auditlog.proto:11-32)
校验  operation ∈ AuditOperations、status ∈ AuditStatuses、security_level ∈ L1-L5；datasource 无校验
字段  AuditLog{datasource, operation, input_hash, output_hash, algorithm, user, status,
             security_level, ...}（无 task_id / api_code）
```

### A.4 engine（Agent）

```text
REST  POST /v1/medical/process                     (routers/medical.py:12,44)  — hub/BFF 主链路
REST  POST /v1/pipeline/process_records, /process_csv (engine/pipeline/router.py:23-59)
REST  POST /v1/dynclassification/eval, /eval_record, /eval_table, ... (routers/dynclassification.py)
REST  POST /v1/privacy/mask, /mask_record, /mask/batch|/mask_batch, /mask/dataframe|/mask_dataframe,
      /v1/privacy/hash                                              (routers/mask.py)
gRPC  privacy.local.PrivacyService：Mask/MaskRecord/MaskBatch/MaskDataFrame/DynClassify/
      KAnonymize*/DP*/Obfuscate*/Health/RecommendParams …（无 ProcessRecords）
上限  单请求 500 条记录 / 单记录 100 字段 / 单字段值 100_000 字符 (medical.py:17-19)
```

### A.5 App-LZ（BFF + Web）

```text
BFF   GET /api/lz/data-api/definitions → presetDataApiDefinitions()   (handlers.go:515-560)
BFF   POST /api/lz/data-api/invoke {api_id:1-4, limit}                (models.go:253-257)
BFF   POST /api/lz/tasks/dispatch {source, operation, payload, priority} (models.go:66-70)
BFF   GET /api/lz/audit/logs, POST /api/lz/audit/verify                (handlers.go:93-94)
BFF→  datasource  GET {DS}/api/v1/datasources        ❌ 上游不存在（D-01）
BFF→  datasource  GET {DS}/api/v1/{yibao|kangyang}   ✅ 存在，但 else 分支默认 yibao（D-11）
BFF→  audit-log   GET {AUD}/api/v1/audit/logs        ❌ 应为 /api/audit/logs（D-02）
BFF→  audit-log   POST {AUD}/api/v1/audit/verify     ❌ 应为 /api/audit/snapshots/verify（D-02）
BFF→  engine      POST {AGENT}/v1/medical/process    ✅；降级 applyMasking() 本地掩码
BFF   会话阶段名  fetch | classify | desensitize | audit              （缺 ingest/return，D-12）
Web   client.ts invokeDataApi(apiId: number)          → { api_id, limit }
Web   App.tsx 兜底 definitions 字段集与 BFF 不一致     (App.tsx:186-187，D-14)
Web   PipelineVisualizer / TaskLifecyclePanel 裸 `ds_*` 与 `ds_custom` 字面量（D-15）
Env   代码 APP_LZ_*（config.go:57-84）vs 文档 LZ_CONSOLE_*/LZ_HUB_API_KEY；出站 API Key 未实现
```

---

## 附录 B：别名 ↔ canonical 速查

| 输入值 | 归一化结果 | 说明 |
|---|---|---|
| `ds_yibao` | `ds_yibao` | canonical |
| `yibao` / `yibao.csv` / `医保` / `medical` / `api1_yibao` | `ds_yibao` | 别名（入站可用，禁止下传） |
| `ds_kangyang` | `ds_kangyang` | canonical |
| `kangyang` / `kangyang.csv` / `康养` / `healthcare` / `api2_kangyang` | `ds_kangyang` | 别名 |
| `ds_mock3` / `mock3` | `ds_mock3` | `status=reserved`，写侧 409 |
| `ds_mock4` / `mock4` | `ds_mock4` | 同上 |
| `shebao` / `ds_shebao` / `api3_shebao` | `ErrUnknownDataSource` | 未登记（§10） |
| `ds_custom` / 空串 / 其他 | `ErrUnknownDataSource` | 写侧 400，读侧过滤为空 |

---

> **一句话总结**：先让名字「只对一次」（阶段 0 修路径与静默回落），再把名字「统一」（注册表 + 归一化），最后把「闭环」补真（审计 `task_id` + 验真）。只改文档里的名字，等于什么都没改。
