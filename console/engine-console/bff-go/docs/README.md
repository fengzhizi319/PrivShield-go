# 测试控制台 Go BFF 网关 (bff-go) 文档索引

本目录包含 Privacy 测试控制台 **Go gRPC 代理网关 (BFF)**（`console/bff-go`）的全套 SDLC 文档。

该网关是一个基于 **Gin + gRPC-Go** 的高性能 BFF 代理层：对外暴露与前端契约一致的 REST JSON 接口，对内通过 gRPC (HTTP/2) 多路复用连接与 `PrivShield` 核心服务高速通信，并可选直接挂载前端 SPA 静态资源。

## 文档清单

| 文档 | 说明 | 目标读者 |
|---|---|---|
| [prd.md](./prd.md) | 产品需求文档 | 产品经理、项目经理 |
| [design.md](./design.md) | 技术架构、模块设计与协议转换细节 | 后端开发、SRE |
| [api.md](./api.md) | REST 接口、请求/响应模型、环境变量参考 | 接入开发者、SRE |
| [ops.md](./ops.md) | 部署、配置、mTLS 证书与故障排查 | SRE、运维 |
| [test.md](./test.md) | 测试策略与集成测试用例说明 | QA、测试开发 |

## 快速开始

```bash
# 1. 启动 PrivShield（REST: 8079 / gRPC: 50051）
python -m engine.server

# 2. 启动 Go BFF（默认监听 127.0.0.1:8081）
cd console/bff-go
go run ./cmd/server

# 3. 浏览器访问
open http://127.0.0.1:8081
```

也可以直接使用一键开发脚本：

```bash
bash ./scripts/dev/dev-bff-agent.sh
```
