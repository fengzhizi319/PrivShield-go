# 代理转发与负载均衡网关文档索引 (API Gateway & Load Balancer Documentation Index)

本目录包含 `PrivShield` 代理转发与自适应负载均衡网关（API Gateway & Load Balancer）的全套 SDLC 工程规范与技术文档。
网关基于 **纯 Go 1.25+ 云原生架构** 实现，集成 REST（Gin + BufferPool 零拷贝反向代理）与 gRPC（UnknownServiceHandler + rawCodec 零编解码透明流代理），提供 P2C-EWMA 自适应调度、三态熔断保护、平滑加权轮询及 Prometheus 遥测指标。

---

## 📚 文档清单

| 文档 | 说明 | 目标读者 |
|---|---|---|
| [prd.md](./prd.md) | 产品需求文档（PRD）与功能边界 | 产品经理、架构师 |
| [design.md](./design.md) | 技术架构、双协议代理、调度算法原理与实现细节 | 后端开发、架构师 |
| [new_design.md](./new_design.md) | Go 云原生自适应网关演进设计与性能剖析 | 架构师、资深开发 |
| [api_reference.md](./api_reference.md) | Go API 接口、YAML / 环境变量配置、REST & gRPC 代理行为参考 | 接入开发者、SRE |
| [examples.md](./examples.md) | 命令行启动、cURL / grpcurl 调用与 Go / Python 客户端示例 | 接入开发者 |
| [examples/gateway_usage.py](./examples/gateway_usage.py) | 完整的 Python 客户端集成验证脚本 | 接入开发者 |
| [optimizations.md](./optimizations.md) | BufferPool 内存池、连接复用、P2C-EWMA 与性能优化方案 | 后端开发、架构师 |
| [testing.md](./testing.md) | 单元测试、集成测试、基准压测报告与验证方法 | QA、测试开发 |
| [reliability.md](./reliability.md) | 三态熔断器、自适应降级、故障隔离与可靠性说明 | SRE、运维开发 |
| [ops.md](./ops.md) | 生产部署、K8s 协同、Helm 编排、监控告警与故障排查 SOP | SRE、DevOps |

---

## 🚀 快速开始

### 1. 快速编译二进制产物

```bash
cd /path/to/PrivShield

# 编译网关产物至 bin/privshield-gateway
make build
```

### 2. 本地启动网关服务 (REST :8000 + gRPC :50000)

```bash
# 方式 A：使用开发启动脚本（自动配置默认环境变量）
bash ./scripts/dev/go-gateway-start.sh

# 方式 B：通过 go run 启动
export GATEWAY_HOST=0.0.0.0
export GATEWAY_PORT=8000
export GATEWAY_GRPC_PORT=50000
export GATEWAY_BACKENDS="127.0.0.1:8079"
export GATEWAY_STRATEGY="p2c"
go run ./engine-go/cmd/privshield-gateway
```

### 3. 验证网关运行状态

```bash
# 查询网关自身健康状态
curl http://127.0.0.1:8000/health

# 查询后端节点在途连接与 EWMA 状态
curl http://127.0.0.1:8000/gateway/backends

# 采集 Prometheus 监控指标
curl http://127.0.0.1:8000/metrics

# 通过网关调用后端隐私脱敏接口
curl -X POST http://127.0.0.1:8000/v1/privacy/mask \
  -H "Content-Type: application/json" \
  -d '{"field":"name","value":"张三","type":"name"}'
```

### 4. 运行全量网关单元测试与基准压测

```bash
# 运行 gateway 模块测试
CGO_ENABLED=0 go test -v ./engine-go/internal/gateway/...

# 运行基准压测 (P2C-EWMA / SWRR / BufferPool)
CGO_ENABLED=0 go test -bench=. ./engine-go/internal/gateway/...
```