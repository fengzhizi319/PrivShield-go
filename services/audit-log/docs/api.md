# 脱敏审计日志与存证 (audit-log) — API 规范

`audit-log` 采用 **REST (HTTP/HTTPS :8084，支持 mTLS / TLCP 国密) + gRPC (mTLS/Plain :50054)** 双协议架构，为 PrivShield 平台提供全量脱敏合规审计、国密 SM3 区块链式防篡改存证、国密 SM4-GCM 快照加密对账、微批聚合刷盘（3k~5k QPS）与统计合规报告服务。

---

## 1. 通信协议与端口规划

| 协议 | 默认地址 | 认证方式 | 说明 |
|---|---|---|---|
| **HTTP(S) REST** | `http(s)://127.0.0.1:8084` | Bearer Token / API Key / TLS 1.3 mTLS / TLCP 国密双证书 | 供 React 前端与 BFF 交互，支持微批聚合写入与链式验真 |
| **gRPC (mTLS)** | `127.0.0.1:50054` | 国密 SM2 / TLS 1.3 双向 mTLS + CN 白名单 | 供调度流水线与服务集群高性能审计入库与存证 |
| **Prometheus** | `http://127.0.0.1:8084/metrics` | API Key 鉴权 / 内网隔离 | 收集 15+ 核心审计、微批刷盘与存储监控指标 |

---

## 2. gRPC API 规范 (`auditlog.proto`)

`package auditlog;`

### 2.1 服务接口定义 (`AuditLogService`)

```protobuf
service AuditLogService {
  // Health 健康检查（自检 + 上游 Agent 连通性）
  rpc Health(HealthRequest) returns (HealthResponse);

  // RecordAudit 写入单条审计存证日志（支持自动计算国密 SM3 9要素哈希链与 SM4 信封加密快照）
  rpc RecordAudit(RecordAuditRequest) returns (RecordAuditResponse);

  // GetAuditLog 查询单条审计日志
  rpc GetAuditLog(GetAuditLogRequest) returns (AuditLogProto);

  // ListAuditLogs 分页与多维度条件检索审计日志
  rpc ListAuditLogs(ListAuditLogsRequest) returns (ListAuditLogsResponse);

  // GetAuditStats 获取审计与脱敏统计分析指标
  rpc GetAuditStats(GetAuditStatsRequest) returns (AuditStatsResponse);

  // ListSnapshots 查询脱敏快照数据存证（密文展示）
  rpc ListSnapshots(ListSnapshotsRequest) returns (ListSnapshotsResponse);

  // VerifyIntegrity 校验审计快照的国密 SM3 完整性与防篡改存证
  rpc VerifyIntegrity(VerifyIntegrityRequest) returns (VerifyIntegrityResponse);

  // GenerateReport 生成合规审计与治理效能报告
  rpc GenerateReport(GenerateReportRequest) returns (ComplianceReportResponse);
}
```

### 2.2 核心 Proto 消息定义

```protobuf
message AuditLogProto {
  string id = 1;              // 唯一日志 ID（如 "audit_1787552256274976692"）
  string timestamp = 2;       // 操作发生时间 (RFC3339)
  string operation = 3;       // "mask" | "classify" | "k_anon" | "dp" | "qol"
  string datasource = 4;      // 数据源标识
  string input_hash = 5;      // 输入数据国密 SM3 摘要哈希
  string output_hash = 6;     // 输出数据国密 SM3 摘要哈希
  string algorithm = 7;       // 所用脱敏或隐私算法
  string parameters_json = 8; // 算法参数（JSON 字符串）
  int32  input_rows = 9;      // 输入数据行数
  int32  output_rows = 10;    // 输出数据行数
  int64  duration_ms = 11;    // 耗时（毫秒）
  string user = 12;           // 操作人/调用服务
  string status = 13;         // "success" | "failed"
  string error_message = 14;  // 错误信息（失败时）
  string security_level = 15; // 敏感等级 "L1" - "L5"（遵循 DB51/T 2989—2023）
  string task_id = 16;        // 关联调度任务 ID
  string api_code = 17;       // 规范 API 编码（如 "api1_yibao"）
  string datasource_id = 18;  // 规范数据源 ID（如 "ds_yibao"）
  string prev_hash = 19;      // 前序区块国密 SM3 哈希（区块链式连续哈希链）
  string integrity_hash = 20; // 9 要素国密 SM3 综合防篡改完整性哈希
}

message VerifyIntegrityRequest {
  string snapshot_id = 1;     // 快照 ID
  string expected_hash = 2;   // 期望哈希（空表示与存证哈希比对）
}

message VerifyIntegrityResponse {
  string snapshot_id = 1;
  bool   valid = 2;           // 是否防篡改校验通过
  string computed_hash = 3;   // 本地重计算国密 SM3 哈希
  string expected_hash = 4;   // 存证哈希
  string message = 5;         // 校验结论说明
  string via = 6;
}
```

---

## 3. HTTP REST API 规范

### 3.1 审计日志与存证检索

#### `GET /v1/audit/logs`
- **说明**：支持多维度过滤检索脱敏审计流水，底层经由 SQL 级分页下推。
- **参数**：
  - `task_id`：任务 ID 过滤
  - `api_code`：API 编码过滤（如 `api1_yibao`、`api2_kangyang`）
  - `datasource_id` / `datasource`：数据源标识（如 `ds_yibao`、`ds_kangyang`）
  - `operation`：操作类型 (`mask` / `classify` / `k_anon` / `dp` / `qol`)
  - `user`：操作人员/系统
  - `status`：状态 (`success` / `failed`)
  - `security_level`：敏感等级 (`L1`~`L5`)
  - `limit` (默认 100, 最大 1000), `offset` (默认 0)
- **响应示例**：
```json
{
  "total": 150,
  "limit": 100,
  "offset": 0,
  "logs": [
    {
      "id": "audit_1787552256274976692",
      "task_id": "task_20260831_001",
      "api_code": "api1_yibao",
      "datasource_id": "ds_yibao",
      "timestamp": "2026-08-31T10:00:00Z",
      "operation": "mask",
      "datasource": "ds_yibao",
      "input_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
      "output_hash": "ca978112ca1bbdcafac231b39a23dc4da786081998d6365faf57629009733549",
      "algorithm": "hmac_sm3_mask",
      "parameters": {"fields": ["person_id", "diagnosis_name"]},
      "input_rows": 50,
      "output_rows": 50,
      "duration_ms": 12,
      "user": "service_hub",
      "status": "success",
      "security_level": "L4",
      "prev_hash": "0000000000000000000000000000000000000000000000000000000000000000",
      "integrity_hash": "8f4a1c5e9b2d3f7a1e5c8b2d3f7a1e5c8b2d3f7a1e5c8b2d3f7a1e5c8b2d3f7a"
    }
  ],
  "via": "audit-log"
}
```

#### `POST /v1/audit/logs`
- **说明**：写入审计流水。自动由 `pkg/store/flusher` 异步批量聚合刷盘，单机支持 **3,000 ~ 5,000 QPS**。
- **请求体**：
```json
{
  "task_id": "task_20260831_002",
  "api_code": "api2_kangyang",
  "datasource_id": "ds_kangyang",
  "operation": "k_anon",
  "datasource": "ds_kangyang",
  "algorithm": "k_anonymity_mondrian",
  "parameters": {"k": 5, "qi_cols": ["age", "gender", "heart_rate"]},
  "input_sample": "{\"age\": 65, \"heart_rate\": 78}",
  "output_sample": "{\"age\": \"[60-70)\", \"heart_rate\": \"[75-80)\"}",
  "input_rows": 50,
  "output_rows": 50,
  "duration_ms": 25,
  "user": "service_hub",
  "status": "success",
  "security_level": "L4"
}
```

#### `GET /v1/audit/logs/:id`
- **说明**：查询指定 ID 的单条审计日志。

---

### 3.2 不可篡改快照与国密 SM3 链式对账

#### `GET /v1/audit/snapshots`
- **说明**：获取脱敏前后样本快照与国密 SM3 存证指纹（快照文本由国密 SM4-GCM 信封加密保护 `enc:v1:...`）。
- **参数**：`limit` (默认 20), `offset` (默认 0)

#### `POST /v1/audit/snapshots/verify`
- **说明**：对指定的快照重新计算国密 SM3 哈希并与存证哈希比对，验证数据样本是否遭受篡改。
- **请求体**：`{"snapshot_id": "snap-1"}`
- **响应**：
```json
{
  "snapshot_id": "snap-1",
  "valid": true,
  "computed_hash": "8f4a1c5e9b...",
  "expected_hash": "8f4a1c5e9b...",
  "message": "integrity verified: SM3 matches non-repudiation proof",
  "via": "audit-log"
}
```

#### `POST /v1/audit/chain/verify`
- **说明**：全链路国密 SM3 区块链式连续哈希链（Hash Chain）核验，毫秒级检测是否有任何历史记录被物理删除、篡改、注入或乱序。
- **请求体**：`{"limit": 1000}`
- **响应**：
```json
{
  "total_verified": 150,
  "valid": true,
  "broken_at_id": "",
  "expected_hash": "",
  "actual_hash": "",
  "message": "hash chain verified successfully (150 records checked)",
  "via": "audit-log"
}
```

---

### 3.3 统计分析与合规报告

#### `GET /v1/audit/stats`
- **说明**：SQL 级聚合脱敏与治理指标，包含各操作频次、成功率分布、等级构成比及平均处理延迟。
- **参数**：`period` (`1h` | `24h` | `7d` | `30d`，默认 `24h`)

#### `POST /v1/audit/report`
- **说明**：生成权威合规评估报告，提供基于 DB51/T 2989—2023 与 GB/T 39786-2021 的治理合规评分与建议。

---

## 4. 接口权限（Scope）映射与完整性保障

本服务对每个端点执行**基于 Scope 的接口级鉴权**（另有 P1-6 只读核验员 Key 权责分离）。`AuditLogPermissionForPath`（REST）与 `AuditLogPermissionForGRPCMethod`（gRPC）将方法+路径映射为所需权限：

| Scope | 适用端点 |
|---|---|
| `audit:write` | `POST /v1/audit/logs`、`POST /v1/audit/report` |
| `audit:read` | `GET /v1/audit/logs[/:id]`、`GET /v1/audit/stats`、`GET /v1/audit/snapshots` |
| `audit:verify` | `POST /v1/audit/snapshots/verify`、`/v1/audit/chain/verify` |

由于「路由注册」（`internal/handlers/handlers.go::RegisterRoutes`）与「权限映射」（`AuditLogPermissionForPath`）分离维护，为避免新增路由遗漏配权限，采用三层防御：

| 层次 | 机制 |
|---|---|
| **运行时兜底** | 未显式映射的路径 fail-closed 归入最高 `audit:admin` 权限 |
| **启动期审计** | `RegisterRoutes` 末尾调用 `pkgauth.LogRoutePermissionAudit`，遇落入兜底 `audit:admin` 的路由打 `WARN` |
| **CI 门禁** | `internal/handlers/route_audit_test.go::TestAllRoutesHaveExplicitPermission` 断言全部路由均有显式映射 |

> 作为不可篡改存证服务，权责分离要求极高，任何新增端点必须显式声明 read/write/verify 权限。通用审计器见 [`pkg/auth/route_audit.go`](file:///home/charles/code/PrivShield-go/pkg/auth/route_audit.go)。
