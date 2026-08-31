# 数据服务调度中枢 (Service Hub) — API 规范

`service-hub` 是 PrivShield 平台的流水线调度中枢，负责串联 **模拟数据源 (datasource-mgr)**、**隐私与分类引擎 (PrivShield Agent)** 与 **审计存证 (audit-log)**，提供 **REST (HTTP/JSON :8082) + gRPC (mTLS :50052)** 双协议接入能力，支持多副本 PostgreSQL 租约并发争抢与 SQLite WAL 自愈降级。

---

## 1. 通信协议与端口规划

| 协议 | 默认地址 | 认证方式 | 主要用途 |
|---|---|---|---|
| **HTTP REST** | `http://127.0.0.1:8082` | `Authorization: Bearer <API_KEY>`（可选） | 供 React 前端与 BFF 交互，提供完整 Web 控制台功能 |
| **gRPC (mTLS)** | `127.0.0.1:50052` | 国密 SM2 / TLS 1.3 双向 mTLS + CN 白名单 | 供高性能微服务互通与跨系统高吞吐任务编排 |
| **Prometheus** | `http://127.0.0.1:8082/metrics` | 内网隔离 / 鉴权 | 导出 20+ 流水线各阶段状态、延迟与熔断器监控指标 |

---

## 2. HTTP REST 接口规范

### 2.1 基础探针与服务概览

#### `GET /health` / `GET /api/health`
- **说明**：Liveness 存活探针。返回 200 表示 service-hub 进程正常存活；同时包含模块标识。
- **响应状态码**：`200 OK`
- **响应示例**：
```json
{
  "status": "ok",
  "via": "service-hub"
}
```

#### `GET /readyz`
- **说明**：Readiness 就绪探针。深度探测上游 `PrivShield Agent` 与下游 `datasource-mgr` 的网络连通性。上游 Agent 不可用时返回 `503 Service Unavailable`，以便 K8s Ingress / Service Mesh 识别并暂停流量导入。
- **响应状态码**：`200 OK`（就绪）或 `503 Service Unavailable`（未就绪）
- **就绪响应示例**：
```json
{
  "status": "ready",
  "backend": "ok",
  "agent": {
    "status": "ok",
    "namespace": "default"
  },
  "agent_url": "http://127.0.0.1:8079",
  "datasource": "ok",
  "datasource_url": "http://127.0.0.1:8083",
  "latency_ms": 3,
  "via": "service-hub"
}
```

#### `GET /api/hub/status`
- **说明**：查询调度中枢当前运行概况与任务队列状态。
- **响应状态码**：`200 OK`

---

### 2.2 任务生命周期与查询

#### `GET /api/hub/tasks`
- **说明**：获取任务列表，支持按状态过滤与分页。
- **查询参数**：
  - `status` (可选)：按状态过滤，支持 `pending` \| `running` \| `completed` \| `failed`
  - `limit` (可选，默认 100，最大 1000)：单页数量
  - `offset` (可选，默认 0)：分页偏移量
- **响应状态码**：`200 OK`

#### `GET /api/hub/tasks/:id`
- **说明**：根据任务唯一 ID 获取任务详细状态与执行结果。
- **响应状态码**：`200 OK`（存在）或 `404 Not Found`（不存在）

---

### 2.3 任务分发与流水线调度

#### `POST /api/hub/dispatch`
- **说明**：手动分发任务到调度流水线。
- **请求体**：
```json
{
  "source": "ds_yibao",
  "operation": "mask",
  "payload": {
    "name": "李明",
    "id_card": "510101198503151234",
    "phone": "13800138000"
  },
  "priority": 50
}
```
- **响应状态码**：`202 Accepted`
- **响应示例**：
```json
{
  "task_id": "task-1787554500-eabf3934",
  "api_code": "API1",
  "datasource_id": "ds_yibao",
  "status": "accepted",
  "via": "service-hub"
}
```

#### `GET /api/hub/pipeline`
- **说明**：返回 6 个状态追踪标签（`ingest` ➔ `fetch` ➔ `classify` ➔ `desensitize` ➔ `return` ➔ `audit`）的实时活跃任务统计与 Agent 运行状态。

---

### 2.4 可观测性与监控导出

#### `GET /metrics`
- **说明**：导出 Prometheus 格式的实时性能监控指标。
- **关键指标包含**：
  - `http_requests_total{method, path, status}`：HTTP 请求计数器
  - `http_request_duration_seconds{method, path}`：请求耗时直方图
  - `task_transitions_total{from_status, to_status, result}`：任务状态转换计数
  - `task_claim_latency_seconds`：多副本任务认领延迟
  - `orphaned_tasks_recovered_total`：崩溃孤立任务回收计数
  - `tasks_retried_total{result}`：失败任务重试计数
  - `circuit_breaker_state`：Agent 客户端熔断器状态

---

## 3. gRPC 服务规范 (`proto/servicehub.proto`)

`ServiceHubService` 运行在端口 `:50052`（支持国密 SM2 / TLS 1.3 mTLS 与 CN 白名单）。

### 3.1 接口列表

```protobuf
service ServiceHubService {
  // Health 健康检查（自检 + 上游 Agent 连通性）
  rpc Health(HealthRequest) returns (HealthResponse);

  // HubStatus 调度中枢状态概览
  rpc HubStatus(HubStatusRequest) returns (HubStatusResponse);

  // Dispatch 分发指定操作任务到流水线
  rpc Dispatch(DispatchRequest) returns (DispatchResponse);

  // ClassifyAndDispatch 先评估敏感度，再按等级自动选择操作并分发任务
  rpc ClassifyAndDispatch(ClassifyAndDispatchRequest) returns (ClassifyAndDispatchResponse);

  // GetTask 查询单个任务状态
  rpc GetTask(GetTaskRequest) returns (TaskProto);

  // ListTasks 列出全部任务（可选按状态过滤）
  rpc ListTasks(ListTasksRequest) returns (ListTasksResponse);

  // PipelineStatus 流水线各阶段状态
  rpc PipelineStatus(PipelineStatusRequest) returns (PipelineStatusResponse);
}
```

### 3.2 核心 Proto 消息定义

```protobuf
message DispatchRequest {
  string source = 1;           // 数据源标识 (如 ds_yibao, ds_kangyang)
  string operation = 2;        // 操作类型: mask / k_anon / dp / classify / none
  string payload_json = 3;     // 任务数据（JSON 序列化）
  int32  priority = 4;         // 优先级（越高越优先）
}

message DispatchResponse {
  string task_id = 1;          // 分配的任务 ID
  string status = 2;           // "accepted" | "queued" | "rejected"
  string via = 3;              // 模块标识 "service-hub"
}

message TaskProto {
  string id = 1;               // 唯一任务 ID
  string status = 2;           // pending | running | completed | failed
  string stage = 3;            // 当前流水线阶段
  string source = 4;           // 数据源名称
  string operation = 5;        // 操作类型
  string created_at = 6;       // 创建时间（RFC3339）
  string started_at = 7;       // 开始时间（RFC3339，可为空）
  string completed_at = 8;     // 完成时间（RFC3339，可为空）
  int64  duration_ms = 9;      // 耗时（毫秒）
  string error = 10;           // 失败时的错误信息
}
```
