# 数据服务调度中枢 (Service Hub) — 测试规范与用例全集

> 对应模块源码：[services/service-hub](..)  
> 模块定位：政务云数据流通调度中枢，负责串联模拟数据源、任务流转、敏感度分类分级、动态脱敏处理、存证上链回传 6 阶段流水线。

---

## 1. 测试体系概览

| 测试套件 / 层次 | 测试文件 | 覆盖范围与关键断言 | 包覆盖率 |
|---|---|---|:---:|
| **Agent 客户端** | `internal/agent/client_test.go` | 上游健康检查、`ProcessAgent`/`ProcessMedical` 一体化处理、`Classify` 动态分类端点、熔断器与指数退避重试 | **16.0%** |
| **出域存证客户端 (P0-6)** | `internal/audit/client_test.go` | audit-log 出域存证提交（`POST /api/audit/logs`）、SM3 输入/输出指纹、fail-closed 与网络重试 | **82.1%** |
| **数据源客户端** | `internal/datasource/client_test.go` | `FetchRecordByIDCard` 按身份证号取数、`FetchRecords` 分页、`ListDataSources`/`GetDataSource`/`TestConnection`、探活、三态熔断器、HTTP/gRPC 双协议 | **48.6%** |
| **共享领域模型** | `internal/models/models_test.go` | `LevelToOperation` 等级→算子映射（L4/L5 均为 `dp`）、算子强度只升不降语义、核心模型 JSON 序列化 | **35.3%** |
| **配置加载器** | `internal/config/config_test.go` | 默认配置、多节点 `AgentBaseURLs` 轮询、audit-log 存证端点解析与回退链、mTLS/公钥固定、P0-4 严格存储 fail-closed 门禁 | **83.9%** |
| **重试判定与退避** | `internal/retry/retry_test.go` | 可重试错误关键字匹配、`RetryCount < 3` 门禁、指数退避 `RetryAfter` 计算 | **100.0%** |
| **HTTP REST 处理器** | `internal/handlers/handlers_test.go`、`httptls_test.go`、`evidence_stub_test.go`、`pipeline_evidence_test.go` | Health/Readyz、HubStatus、Pipeline、GetTask、ListTasks 分页与状态过滤、Dispatch 边界拦截、单 Key 与 Scope 鉴权、HTTP TLS 1.3/mTLS/SPKI Pinning、流水线出域存证、优雅停机与本地 Worker | **74.9%** |
| **gRPC 服务端与 mTLS** | `internal/grpcserver/server_test.go`、`evidence_stub_test.go`、`operation_derivation_test.go` | 8 个 RPC（Health/HubStatus/Dispatch/ClassifyAndDispatch/GetTask/ListTasks/PipelineStatus/FetchAndDesensitize）、方法级 Scope 鉴权、mTLS 证书链与公钥比对、算子推导（定级缺失即失败）、出域存证、租约 Worker、异常恢复与停机中断 | **63.9%** |
| **启动生命周期与严格存储** | `cmd/server/retry_test.go`、`strict_storage_test.go` | 崩溃恢复 `recoverOrphanedTasks`、周期性重试 `retryFailedTasks`、P0-4 严格存储启动门禁 | **17.1%** |
| **真实跨服务 E2E 流水线** | `internal/handlers/real_e2e_test.go` | 真实 Agent + Service Hub + Datasource Mgr + Audit Log 跨服务 6 阶段完整流水线调度验证 | 条件触发 (`PRIVSHIELD_E2E=1`) |
| **流水线算子集成测试** | `tests/pipeline_operation_test.go` | 跨模块集成校验 6 阶段流水线的算子强度推导与端到端编排（独立 `tests` 包，无非测试语句） | 集成包 |

> 覆盖率由 `go test -cover ./...` 实测（各包按其自身测试二进制统计，不含跨包联动覆盖）；`proto` 为 Protobuf 生成代码，不计入。

---

## 2. 快速运行测试

```bash
# 1. 运行 service-hub 内部全部单元测试与覆盖率统计
go test -v -cover ./services/service-hub/...

# 2. 仅运行 gRPC 服务端与 mTLS 证书校验测试
go test -v ./services/service-hub/internal/grpcserver/

# 3. 仅运行 HTTP REST 接口与数据源联动流水线测试
go test -v ./services/service-hub/internal/handlers/

# 4. 运行全栈真实 E2E 调度测试（需先启动真实 PrivShield Agent :8079）
PRIVSHIELD_E2E=1 go test -v -run TestRealE2E ./services/service-hub/internal/handlers/
```

---

## 3. 详细测试用例清单

### 3.1 HTTP REST 接口测试 (`internal/handlers/handlers_test.go`)

| 测试函数 | 对应接口 / 场景 | 验证内容与防护重点 |
|---|---|---|
| `TestHealth` | `GET /health` / `GET /api/health` | 自身存活探测与模块标识返回 |
| `TestReadyzAgentUnreachable` | `GET /readyz` | 深度依赖探活：上游 Agent 不可达时返回 503 Not Ready（含 Datasource 连通性） |
| `TestHubStatus` | `GET /api/hub/status` | 调度中枢运行状态、活跃/排队/完成/失败任务计数汇总 |
| `TestGetTask_SuccessAndNotFound` | `GET /api/hub/tasks/:id` | 正常查询任务详情与不存在 ID 返回 404 Not Found |
| `TestListTasksEmpty` | `GET /api/hub/tasks` | 无任务时返回 `total=0` 且任务列表为空切片 |
| `TestListTasksWithFilter` | `GET /api/hub/tasks?status=...` | 按 `pending` / `running` / `completed` / `failed` 状态精准过滤 |
| `TestListTasks_InvalidStatusFilter` | `GET /api/hub/tasks?status=invalid` | 非法状态参数返回 400 Bad Request 校验错误 |
| `TestDispatchInvalidBody` | `POST /api/hub/dispatch` | 缺失必需字段（`source` 或 `operation`）时返回 400 Bad Request |
| `TestDispatch_OversizedSource` | `POST /api/hub/dispatch` | `source` 字段超出 1024 字符防超大字符串攻击，返回 400 Bad Request |
| `TestDispatchAccepted` | `POST /api/hub/dispatch` | 合法请求立即返回 202 Accepted + 任务 ID，后台异步调度流水线 |
| `TestProcessTask_StopsWhenStatePersistenceFails` | `processTask` | 首次 `running/ingest` 状态更新失败时立即停止，不推进后续阶段 |
| `TestAuthMiddleware_Protection` | `pkg/middleware.Auth` | 验证未携带 Token 返回 401 Unauthorized、合法 Bearer 放行及 `/health` 免认证 |
| `TestScopeAuthMiddleware_AccessControl` | `Server.scopeAuthMiddleware` | Scope-based 鉴权：`hub:read` / `hub:dispatch` 细粒度权限校验、缺失/非法 Token 拒绝、健康端点豁免 |
| `TestServer_ShutdownGraceful` | `Server.Shutdown` | 验证停机信号触发 Context 取消并安全等待在途任务 Goroutine 完成 |
