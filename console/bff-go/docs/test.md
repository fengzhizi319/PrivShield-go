# Go gRPC 代理后端测试文档

## 1. 运行全部测试

```bash
cd console/bff-go

go test ./...
```

该命令会执行：

- `internal/mapper/mapper_test.go`：REST 到 gRPC 映射的单元测试。
- `internal/handlers/handlers_test.go`：HTTP handler 的单元测试。
- `tests/integration_test.go`：连接真实 agent 的集成测试（若 agent 未启动会自动跳过）。

## 2. 运行指定包的测试

### mapper 单元测试

```bash
go test ./internal/mapper -v
```

覆盖的 RPC：

- `Health`
- `Mask`
- `DPCount`
- `KAnonymizeRecord`
- `ObfuscateQuery`
- `ClassifyField`

每个测试都会：

1. 在内存中启动一个 `bufconn` gRPC 服务器；
2. 注入对应的伪造 RPC 实现；
3. 使用 `agent.NewFromConnection` 创建客户端；
4. 调用 `mapper.Dispatch` 并断言响应结构。

### handlers 单元测试

```bash
go test ./internal/handlers -v
```

覆盖的 HTTP 端点：

- `GET /health`：断言返回 `backend: ok` 与伪造的 agent 状态。
- `GET /v1/samples`：断言返回非空 samples 列表。
- `POST /v1/proxy`：以 `/v1/privacy/mask` 为例，断言代理返回伪造的脱敏结果。

这些测试同样使用 `bufconn` 启动伪造的 gRPC 服务器，并通过 `httptest` 模拟 HTTP 调用。

## 3. 集成测试

### 前置条件

集成测试需要真实启动 `PrivShield`：

```bash
cd /path/to/PrivShield
python -m engine.server
```

默认会监听：

- REST：`http://127.0.0.1:8079`
- gRPC：`127.0.0.1:50051`

Go 代理默认会连接 `127.0.0.1:50051`。

### 运行集成测试

```bash
cd console/bff-go
go test ./tests -v
```

如果 `127.0.0.1:50051` 不可达，测试会自动跳过并输出：

```text
=== RUN   TestIntegration_HealthAndProxy
    integration_test.go:xx: 跳过集成测试：agent 127.0.0.1:50051 未可达：...
--- SKIP: TestIntegration_HealthAndProxy (0.00s)
```

### 集成测试覆盖内容

1. 连接真实 agent 并调用 `Health` RPC；
2. 启动 Go HTTP 服务器（随机端口）；
3. 通过 `http.Get` 访问 `/health`；
4. 通过 `http.Post` 访问 `/v1/proxy`，转发 `/v1/privacy/mask`；
5. 通过 `http.Get` 访问 `/v1/samples`。

## 4. 测试关键依赖

- `google.golang.org/grpc/test/bufconn`：内存 gRPC 服务器/连接。
- `net/http/httptest`：HTTP 测试服务器与请求构造。
- `agent.NewFromConnection`：从已有 `*grpc.ClientConn` 构造客户端，便于注入测试连接。

## 5. 如何重新生成 protobuf 代码

如果修改了 `proto/privacy.proto`，需要重新生成 Go 代码：

```bash
cd console/bff-go

protoc \
  -I ../../proto \
  --go_out=./proto \
  --go_opt=paths=source_relative \
  --go-grpc_out=./proto \
  --go-grpc_opt=paths=source_relative \
  ../../proto/privacy.proto
```

> 注意：proto 定义文件位于仓库根目录 `proto/privacy.proto`，请根据仓库结构调整 `-I` 参数。

或者使用 `go generate`（如果已配置）：

```bash
go generate ./...
```

生成后请再次运行：

```bash
go build ./cmd/server
go test ./...
```

确保生成代码与现有代码兼容。

## 6. 构建检查

```bash
cd console/bff-go

go build ./cmd/server
```

如果修改了代码或测试，建议在提交前同时执行：

```bash
go vet ./...
go test ./...
```

## 8. 启动与停止

启动 Go gRPC 代理后端：

```bash
cd console/bff-go
go run ./cmd/server
```

或使用仓库根目录的一键脚本（同时启动 agent 与 Go 代理）：

```bash
cd /path/to/PrivShield
./scripts/dev/dev-bff-agent.sh
./scripts/dev/dev-stop.sh    # 在另一个终端停止
```

## 9. 调试技巧

- 在 mapper 测试中某个 RPC 失败，检查对应 handler 的 JSON 字段名是否与 proto 请求一致。
- handler 测试失败时，可通过 `t.Logf` 输出响应体。
- 集成测试跳过时，确认 Python agent 是否已启动，并检查 `PRIVACY_AGENT_GRPC_HOST` / `PRIVACY_AGENT_GRPC_PORT` 环境变量。
