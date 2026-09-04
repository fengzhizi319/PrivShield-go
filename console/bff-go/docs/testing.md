# 测试控制台后端（Go BFF）测试文档

## 1. 测试目的与策略

Go BFF（`console/bff-go`）是控制台与 PrivShield Agent 之间的薄代理层，测试重点是：

- HTTP/REST 路由与请求/响应包装是否正确；
- gRPC 到上游 Agent 的转发、错误转换、认证元数据是否按约定工作；
- TLS/mTLS 服务端配置与握手是否可正常建立；
- 批量执行、文件上传、负载均衡探测等复合接口的容错与汇总统计是否准确。

测试分三层：

| 层次 | 工具 | 是否依赖真实 agent | 位置 |
|---|---|---|---|
| 单元测试 | `go test` + `httptest` / `bufconn` | 否（mock gRPC server） | `console/bff-go/internal/**/*_test.go` |
| 集成测试 | `go test` + 真实 HTTP 调用 | 是（需 agent + BFF 运行） | `console/bff-go/tests/integration_test.go` |
| 端到端测试 | `bash ./scripts/dev/run_console_e2e_tests.sh` | 是 | 覆盖 BFF + Services + Web |

**关键设计**：单元测试通过 `bufconn` 内存 gRPC 连接或 mock handler 隔离真实 agent；集成测试通过本地启动 BFF 并调用真实 HTTP 端口验证端到端转发。

## 2. 单元测试用例说明

### 2.1 `/health`

| 用例 | 场景 | 断言要点 |
|---|---|---|
| `TestHealth_OK` | agent 可达 | 返回 200；`status == "ok"`；含 `via` 与 `protocol` |
| `TestHealth_AgentUnreachable` | agent 不可达 | 仍返回 **200**；agent 状态为 `"unreachable"`；含 `error` 字段 |

### 2.2 `/v1/samples`

| 用例 | 场景 | 断言要点 |
|---|---|---|
| `TestSamples` | 获取示例 | 返回 200；`samples` 数量与 `get_samples()` 一致；首条含 `path` |

### 2.3 `/v1/proxy`

| 用例 | 场景 | 断言要点 |
|---|---|---|
| `TestProxy_JSON` | 转发 JSON 请求 | 返回 200；包装为 `status/duration_ms/data`；`data` 为上游返回 |
| `TestProxy_InvalidBody` | 缺少必填字段 `path` | 返回 **400**；响应含 `error` 字段 |
| `TestProxy_UpstreamError` | 上游返回错误 | **透传**状态码与错误信息 |

### 2.4 `/v1/batch`

| 用例 | 场景 | 断言要点 |
|---|---|---|
| `TestBatch_OK` | 批量转发 | 返回 200；`total/passed/failed` 统计正确；单个失败不中断 |

## 3. gRPC Server 与 mTLS 测试

`console/bff-go/internal/grpcserver/server_test.go` 覆盖：

- 基础 gRPC 转发：`Mask` / `Health` 请求能正确透传到上游 Agent；
- TLS/mTLS 握手：使用自签名测试证书验证 `ConsoleTLSEnabled` + `ConsoleTLSClientAuth=require` 配置。

运行方式：

```bash
cd console/bff-go
go test -v ./internal/grpcserver
```

## 4. 集成测试说明

`console/bff-go/tests/integration_test.go` 在真实端口上启动 BFF，并连接 mock 或真实 agent：

- 探测 `/health`、`/v1/samples` 是否能正常返回；
- 验证 `/v1/proxy` 路径转发与响应包装；
- 验证文件上传 `/v1/upload` 的 multipart 处理。

前置条件：

```bash
# 方式 1：使用真实 agent
cd /path/to/PrivShield
python -m engine.server &

# 方式 2：使用 mock agent
python3 scripts/dev/mock_agent_server.py 8079 &
```

运行：

```bash
cd console/bff-go
go test -v ./tests
```

## 5. 端到端测试

```bash
bash ./scripts/dev/run_console_e2e_tests.sh
```

该脚本会：

1. 启动 Mock Agent（8079）；
2. 运行 `go test ./pkg/... ./console/bff-go/...`；
3. 运行 Services 微服务测试；
4. 运行 React Web 前端 Vitest 测试。

## 6. CI 集成

Go BFF 测试不依赖 Python 依赖，可在标准 Go 环境中运行：

```bash
go test ./pkg/... ./services/service-hub/... ./services/datasource-mgr/... ./services/audit-log/... ./console/bff-go/... -race -count=1
```

## 7. 测试覆盖建议

- `/v1/upload` 大文件/超大文件拒绝分支；
- gRPC Server 在 `ConsoleGRPCEnabled=false` 时不启动分支；
- mTLS 公钥固定（`ConsoleTLSPinnedPubKeyFile`）拒绝非法客户端分支；
- 限流中间件（`CONSOLE_RATE_LIMIT`）命中与跳过分支。
