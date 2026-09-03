# 调度之眼 (Console App-LZ) — API 接口与数据契约规范

> **文档版本**：v1.2.0  
> **服务端口**：HTTP REST `:8085` / gRPC `:50055`  
> **架构原则**：app-lz BFF 是模拟的外部程序，所有数据请求统一通过 `service-hub` 调度中枢编排，不直接访问 datasource-mgr / engine-go / audit-log。  
> **唯一编排入口**：
> - `service-hub`: `http://127.0.0.1:8082` (gRPC `127.0.0.1:50052`)

---

## 1. 接口概览

| 模块 | HTTP 方法 | 端点路径 | 调用模式 | 上游映射 |
|---|---|---|---|---|
| **健康与拓扑** | `GET` | `/api/health` | 本地 | BFF 自身存活探针 |
| | `GET` | `/api/lz/topology` | **[聚合]** | 并发调用 4 服务 `/health` + TCP gRPC 拨测 |
| | `POST` | `/api/lz/probe/all` | **[聚合]** | 强制全集群主动并发重探测 |
| **任务与租约** | `GET` | `/api/lz/tasks` | **[转发]** | → `service-hub` `GET /api/hub/tasks` |
| | `GET` | `/api/lz/tasks/:id` | **[转发]** | → `service-hub` `GET /api/hub/tasks/:id` |
| | `GET` | `/api/lz/tasks/leases` | **[聚合]** | `service-hub` 存储后端检测 + 租约状态 |
| | `POST` | `/api/lz/tasks/dispatch` | **[转发]** | → `service-hub` `POST /api/hub/dispatch` |
| **自动化测试** | `GET` | `/api/lz/suites` | 本地 | BFF 内置测试用例定义 (TS-01~TS-03) |
| | `POST` | `/api/lz/suites/run` | 本地 | BFF 测试执行引擎（调用上游服务执行断言） |
| **审计验真** | `GET` | `/api/lz/audit/logs` | **[转发]** | → `audit-log` `GET /api/audit/logs` |
| | `POST` | `/api/lz/audit/verify` | **[转发]** | → `audit-log` `POST /api/audit/snapshots/verify` |
| **监控指标** | `GET` | `/api/lz/metrics` | **[转发]** | → `service-hub` `GET /metrics` (Prometheus 原始文本) |
| | `GET` | `/api/lz/metrics/parsed` | **[本地]** | BFF 解析 Prometheus 文本 → 结构化指标 |
| **预设数据 API** | `GET` | `/api/lz/data-api/definitions` | 本地 | BFF 内置 4 个预设数据 API 定义 |
| | `POST` | `/api/lz/data-api/invoke` | **[聚合]** | 通过 service-hub 编排 3 阶段会话：ingest → hub_orchestrate → return |

> **[聚合]** = BFF 并发调用多个上游服务并合并结果；**[转发]** = BFF 透传请求到单一上游，附加认证头与 `X-Request-ID`；**[本地]** = BFF 内部直接处理。

---

## 2. 认证与安全

### 2.1 入站认证

所有 `/api/lz/*` 端点（除 `/api/health` 和 `/metrics`）要求携带 API Key：

```
Authorization: Bearer <LZ_CONSOLE_API_KEY>
```

未携带或 Key 无效时返回：

```json
{ "error": { "code": "UNAUTHORIZED", "message": "missing or invalid API key" }, "via": "app-lz-bff" }
```

### 2.2 出站认证

BFF 向各上游服务转发请求时，自动注入对应的 Bearer Token：

| 上游服务 | 认证头 | 环境变量 |
|---|---|---|
| `service-hub` | `Authorization: Bearer <key>` | `LZ_HUB_API_KEY` |
| `datasource-mgr` | `Authorization: Bearer <key>` | `LZ_DATASOURCE_API_KEY` |
| `audit-log` | `Authorization: Bearer <key>` | `LZ_AUDIT_API_KEY` |
| `engine` Agent | `Authorization: Bearer <key>` | `LZ_AGENT_API_KEY` |

### 2.3 SSE 流认证

SSE 端点通过 URL 查询参数认证：`GET /api/lz/suites/stream/:run_id?token=<run_token>`。`run_token` 在 `POST /api/lz/suites/run` 响应中返回。

---

## 3. 统一错误响应格式

### 3.1 错误响应结构

所有错误响应遵循统一格式：

```json
{
  "error": {
    "code": "UPSTREAM_UNAVAILABLE",
    "message": "service-hub is unreachable",
    "details": {
      "service": "service-hub",
      "url": "http://127.0.0.1:8082",
      "timeout_ms": 5000
    }
  },
  "via": "app-lz-bff"
}
```

### 3.2 错误码定义

| 错误码 | HTTP 状态码 | 触发条件 | `details` 字段 |
|---|---|---|---|
| `UNAUTHORIZED` | 401 | API Key 缺失或无效 | — |
| `RATE_LIMITED` | 429 | 请求频率超过限制 | `limit`, `retry_after_seconds` |
| `INVALID_REQUEST` | 400 | 请求参数校验失败 | `field`, `reason` |
| `UPSTREAM_UNAVAILABLE` | 502 | 上游服务连接失败或超时 | `service`, `url`, `timeout_ms` |
| `UPSTREAM_TIMEOUT` | 504 | 上游服务响应超时 | `service`, `url`, `timeout_ms` |
| `UPSTREAM_ERROR` | 502 | 上游返回 5xx 错误 | `service`, `upstream_status`, `upstream_body` |
| `PARTIAL_DEGRADED` | 200 | 聚合查询中部分服务不可达 | `degraded_services[]`, `healthy_services[]` |

### 3.3 部分降级响应示例

拓扑查询中 `audit-log` 不可达时：

```json
{
  "status": "partial_degraded",
  "timestamp": "2026-08-26T10:45:00Z",
  "services": [
    { "id": "service-hub", "status": "ready", "rtt_ms": 1.8 },
    { "id": "datasource-mgr", "status": "ready", "rtt_ms": 2.1 },
    {
      "id": "audit-log",
      "status": "unreachable",
      "rtt_ms": null,
      "error": { "code": "UPSTREAM_UNAVAILABLE", "message": "connection refused" }
    },
    { "id": "engine", "status": "ready", "rtt_ms": 3.2 }
  ],
  "degraded_services": ["audit-log"],
  "healthy_services": ["service-hub", "datasource-mgr", "engine"],
  "via": "app-lz-bff"
}
```

---

## 4. 核心接口详细定义

### 4.1 4 服务实时拓扑与双协议健康探针 (`GET /api/lz/topology`)

- **调用模式**：[聚合] 并发调用 4 个上游服务的 REST `/health` 与 gRPC 端口连通性
- **查询参数**：
  - `protocol` (可选，字符串): 激活协议视角，可选值 `rest`（默认）或 `grpc`
- **超时策略**：单个服务探测超时 1.5 秒，整体响应超时 5 秒
- **返回顺序**：严格保证固定四节点顺序：`1. service-hub ➔ 2. engine ➔ 3. datasource-mgr ➔ 4. audit-log`
- **降级行为**：单个服务不可达时返回 200 + `status: degraded`，不阻塞整体响应
- **响应格式**：`application/json`
- **响应示例**：
```json
{
  "status": "healthy",
  "active_protocol": "rest",
  "timestamp": "2026-08-26T10:45:00Z",
  "services": [
    {
      "id": "service-hub",
      "name": "调度中枢 (Service Hub)",
      "http_url": "http://127.0.0.1:8082",
      "grpc_addr": "127.0.0.1:50052",
      "status": "ready",
      "rtt_ms": 1.8,
      "rest_status": "ready",
      "rest_rtt_ms": 1.8,
      "grpc_status": "ready",
      "grpc_rtt_ms": 1.2,
      "protocol": "rest",
      "version": "1.8.0",
      "details": {
        "store_type": "postgres_leased",
        "active_tasks": 2,
        "completed_total": 128,
        "failed_total": 3,
        "uptime": "12h34m56s"
      }
    },
    {
      "id": "engine",
      "name": "隐私与分类引擎 (PrivShield Agent)",
      "http_url": "http://127.0.0.1:8079",
      "grpc_addr": "127.0.0.1:50051",
      "status": "ready",
      "rtt_ms": 3.2,
      "rest_status": "ready",
      "rest_rtt_ms": 3.2,
      "grpc_status": "ready",
      "grpc_rtt_ms": 2.4,
      "protocol": "rest",
      "version": "1.8.0",
      "details": {
        "funnel_layers": ["rule", "ner", "llm"],
        "primitives": ["mask", "dp", "ldp", "kano", "qol"]
      }
    },
    {
      "id": "datasource-mgr",
      "name": "数据源管理 (Datasource Mgr)",
      "http_url": "http://127.0.0.1:8083",
      "grpc_addr": "127.0.0.1:50053",
      "status": "ready",
      "rtt_ms": 2.1,
      "rest_status": "ready",
      "rest_rtt_ms": 2.1,
      "grpc_status": "ready",
      "grpc_rtt_ms": 1.5,
      "protocol": "rest",
      "version": "1.8.0",
      "details": {
        "datasources_count": 2,
        "total_records": 1800
      }
    },
    {
      "id": "audit-log",
      "name": "脱敏审计日志 (Audit Log)",
      "http_url": "http://127.0.0.1:8084",
      "grpc_addr": "127.0.0.1:50054",
      "status": "ready",
      "rtt_ms": 1.5,
      "rest_status": "ready",
      "rest_rtt_ms": 1.5,
      "grpc_status": "ready",
      "grpc_rtt_ms": 1.1,
      "protocol": "rest",
      "version": "1.8.0",
      "details": {
        "merkle_valid": true,
        "total_audit_logs": 256
      }
    }
  ]
}
```

---

### 4.2 任务分发 (`POST /api/lz/tasks/dispatch`)

- **调用模式**：[转发] → `service-hub` `POST /api/hub/dispatch`
- **请求体**：
```json
{
  "source": "ds_yibao",
  "operation": "mask",
  "payload": {
    "name": "张三",
    "id_card": "110101199001011234",
    "phone": "13800138000"
  },
  "priority": 50
}
```
- **响应**（HTTP 202）：
```json
{
  "task_id": "task-1787554500-eabf3934",
  "status": "accepted",
  "via": "service-hub"
}
```

---

### 4.3 任务列表查询 (`GET /api/lz/tasks`)

- **调用模式**：[转发] → `service-hub` `GET /api/hub/tasks`
- **查询参数**：

| 参数 | 类型 | 默认值 | 上限 | 说明 |
|---|---|---|---|---|
| `status` | string | — | `pending` / `running` / `completed` / `failed` | 按状态过滤 |
| `operation` | string | — | `mask` / `k_anon` / `dp` / `none` | 按操作类型过滤 |
| `limit` | int | 100 | 1000 | 分页大小 |
| `offset` | int | 0 | — | 分页偏移 |

- **响应示例**：
```json
{
  "total": 128,
  "tasks": [
    {
      "id": "task-1787554500-eabf3934",
      "status": "completed",
      "stage": "done",
      "source": "ds_yibao",
      "operation": "mask",
      "created_at": "2026-08-26T10:30:00Z",
      "started_at": "2026-08-26T10:30:00Z",
      "completed_at": "2026-08-26T10:30:01Z",
      "duration_ms": 35,
      "error": ""
    }
  ],
  "via": "service-hub"
}
```

---

### 4.4 Phase B 租约看板 (`GET /api/lz/tasks/leases`)

- **调用模式**：[聚合] 查询 `service-hub` 存储后端类型，PostgreSQL 模式下返回租约详情
- **存储后端自适应**：
  - **PostgreSQL 模式**：返回完整租约信息（Worker 分布、租约倒计时、孤儿回收）
  - **SQLite / Memory 模式**：返回 `store_backend` 标识 + 提示信息，`workers` 为空数组

- **PostgreSQL 模式响应示例**：
```json
{
  "store_backend": "postgres_leased",
  "total_leased_tasks": 3,
  "workers": [
    {
      "worker_id": "hub-worker-replica-1",
      "claimed_tasks_count": 2,
      "tasks": [
        {
          "task_id": "task-1787554500-eabf3934",
          "stage": "desensitize",
          "lease_expires_in_seconds": 28.5,
          "priority": 50
        }
      ]
    },
    {
      "worker_id": "hub-worker-replica-2",
      "claimed_tasks_count": 1,
      "tasks": [
        {
          "task_id": "task-1787554501-89bcdef1",
          "stage": "classify",
          "lease_expires_in_seconds": 29.1,
          "priority": 80
        }
      ]
    }
  ],
  "orphan_recovery": {
    "enabled": true,
    "scan_interval_seconds": 5,
    "recovered_total": 0
  },
  "via": "app-lz-bff"
}
```

- **SQLite / Memory 模式响应示例**：
```json
{
  "store_backend": "sqlite",
  "total_leased_tasks": 0,
  "workers": [],
  "orphan_recovery": {
    "enabled": false,
    "scan_interval_seconds": 0,
    "recovered_total": 0
  },
  "notice": "Lease-based scheduling requires PostgreSQL backend. Current backend is SQLite.",
  "via": "app-lz-bff"
}
```

---

### 4.5 自动化测试套件执行 (`POST /api/lz/suites/run`)

- **调用模式**：本地执行（BFF 测试引擎调用上游服务执行断言）
- **请求体**：
```json
{
  "suite_ids": ["TS-01", "TS-02", "TS-03"],
  "concurrency": 20,
  "benchmark_requests": 100
}
```

| 字段 | 类型 | 必填 | 默认值 | 说明 |
|---|---|---|---|---|
| `suite_ids` | string[] | 否 | 全部 TS-01~TS-03 | 指定执行的用例 ID 列表 |
| `concurrency` | int | 否 | 10 | TS-02 压测并发数 |
| `benchmark_requests` | int | 否 | 100 | TS-02 压测总请求数 |

- **响应示例**：
```json
{
  "run_id": "run-20260826-104500-a1b2",
  "status": "running",
  "total_cases": 3,
  "run_token": "rt-xxxx-yyyy-zzzz",
  "stream_url": "/api/lz/suites/stream/run-20260826-104500-a1b2?token=rt-xxxx-yyyy-zzzz",
  "via": "app-lz-bff"
}
```

---

### 4.6 审计存证与监控指标

- `GET /api/lz/audit/logs`：直通转发至 `audit-log` `GET /api/audit/logs`，支持 `limit`/`offset` 分页。
- `POST /api/lz/audit/verify`：直通转发至 `audit-log` `POST /api/audit/snapshots/verify`。
- `GET /api/lz/metrics`：直通转发至 `service-hub` `GET /metrics`，返回 Prometheus 原始文本格式。
- `GET /api/lz/metrics/parsed`：BFF 本地解析 Prometheus 文本，返回结构化指标：

```json
{
  "stage_durations": { "ingest": 1.2, "fetch": 4.8, "classify": 12.5, "desensitize": 6.8, "return": 0.8, "audit": 3.4 },
  "qps": 45.2,
  "percentiles": { "p50": 12.5, "p90": 28.3, "p95": 35.1, "p99": 48.7 },
  "total_requests": 12345,
  "source": "prometheus"
}
```

> 当上游不可达时，`metrics/parsed` 返回 `source: "fallback"` 及默认值。

---

### 4.7 预设数据 API

- `GET /api/lz/data-api/definitions`：返回 BFF 内置的 4 个预设数据 API 定义。
- `POST /api/lz/data-api/invoke`：通过 service-hub 调度中枢编排完整的 3 阶段会话（ingest → hub_orchestrate → return）。

**请求体**：
```json
{ "api_id": 1, "limit": 5 }
```

**会话链路**：
1. **Ingest** — BFF 接入并校验 API 标识（api_code / datasource_id），委托 service-hub 编排全链路
2. **Hub Orchestrate** — service-hub 内部编排完整 4 步：② 拉取原始数据 → ③ 分类分级+脱敏 → ④ 审计存证 → ⑤ 返回结果（详见 service-hub api.md §3.5）
3. **Return** — 组装会话结果（脱敏数据 + 各阶段耗时 + 审计条目 ID）

> app-lz BFF 不直接访问 datasource-mgr / engine-go / audit-log，所有数据操作由 service-hub 统一编排。

> 详细响应结构参见 [frontend_backend_mapping.md](frontend_backend_mapping.md) §8.2。
