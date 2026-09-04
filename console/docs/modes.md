# Console 运行模式说明：开发模式 vs 商业化产品模式

本文用于说明 `console` 目录下前端、后端、agent 与服务器配置在两种典型场景中的区别：

- **开发模式**：面向本地联调、功能迭代、问题排查。
- **商业化产品模式**：面向正式交付、对外演示、企业内网部署或客户环境部署。

> 这里的“商业化产品模式”可以理解为更严格的生产部署模式：强调稳定性、隔离性、认证、加密、可观测性和可运维性。

---

## 1. 总体差异

| 维度 | 开发模式 | 商业化产品模式 |
|---|---|---|
| 前端 | Vite 开发服务器热更新，源码直跑 | 先构建 `dist/`，由静态服务器或后端托管 |
| 后端 | `--reload`、高频修改、日志更详细 | 关闭热重载，稳定运行，多 worker / 多实例 |
| agent | 本机 `127.0.0.1` 或开发集群 | 仅在内网/专有网络暴露，通常启用 TLS / Auth |
| 端口暴露 | 直接暴露本地端口，便于联调 | 仅暴露必要入口，优先通过反向代理或 Ingress |
| 跨域 | 常见，需要 CORS 或 Vite proxy | 尽量同源部署，减少浏览器跨域问题 |
| 配置方式 | `.env`、脚本参数、默认本地值 | 环境变量 + Secret + ConfigMap / 证书挂载 |
| 监控 | 基础健康检查即可 | 日志、指标、链路追踪、审计更完整 |

---

## 2. 前端差异

### 2.1 开发模式

前端通常直接使用 `console/web` 的 Vite 开发服务器：

```bash
cd console/web
corepack pnpm install
corepack pnpm dev
```

特点：

- 热更新快，适合 UI 调整和接口联调；
- 前端访问地址通常是 `http://localhost:5173`；
- 前端会通过绝对地址访问后端，因此常常会触发 CORS；
- 更适合单人开发、快速验证接口变化。

### 2.2 商业化产品模式

前端应先构建静态资源，再由稳定入口提供：

```bash
cd console/web
corepack pnpm install
corepack pnpm build
```

推荐做法：

- 将 `dist/` 交给后端、Nginx、网关或对象存储托管；
- 前端请求地址尽量与页面同源；
- 不把开发代理当作生产方案；
- 对外发布时仅提供必要的页面和 API，不暴露 Vite dev server。

---

## 3. 后端微服务差异

`console` 目录下包含完整的控制台微服务与代理网关集群：

| 微服务 | 技术栈 | 默认端口 | 职责 |
|---|---|---|---|
| `console/bff-go` | Go + Gin + gRPC | `8081` (HTTP) / `50055` (gRPC) | 统一代理网关，REST 入口 + gRPC 上游，支持独立托管 Web SPA |
| `services/service-hub` | Go + Gin + gRPC | `8082` (HTTP) / `50052` (gRPC) | 6 阶段数据治理调度中枢（接入/取数/分类/脱敏/返回/存证） |
| `services/datasource-mgr`| Go + Gin | `8083` | 多源异构数据源接入、连通性探测、元数据智能分类打标与访问审计 |
| `services/audit-log` | Go + Gin | `8084` | 治理事件存证、8 要素 SHA-256 防篡改快照与 SQL 级合规报告 |
| `pkg`（仓库根） | Go Package | — | 共享基础包（SQLite/Memory 存储引擎、安全中间件链、Prometheus 指标、熔断客户端） |

### 3.1 代理网关 (Go BFF)

#### 开发模式
- **Go 后端**：`cd console/bff-go && go run ./cmd/server`（快速编译启动，直连 Agent gRPC）。

#### 商业化产品模式
- **Go 后端**：预编译单一静态二进制文件，开启 REST/HTTPS/gRPC/mTLS，直接提供内置 Web 静态资源托管。

### 3.2 专项治理微服务 (service-hub, datasource-mgr, audit-log)

#### 开发模式
- 默认采用内存存储模式（`PRIVACY_HUB_DB=""` 等），轻量快速，无需预先创建数据库文件；
- 一键启动本地脚本：`bash ./scripts/dev/dev-start-new-modules.sh`。

#### 商业化产品模式
- 启用 SQLite WAL 持久化（如 `PRIVACY_HUB_DB="/var/data/service_hub.db"`），读写并发不阻塞，掉电不丢数据；
- 开启微服务间 API Key 鉴权与安全响应头；
- `service-hub` 开启 gRPC mTLS 与公钥固定（`PRIVACY_TLS_PINNED_PUBKEY_FILE`），达到金融级零信任传输安全。

---

## 4. agent 差异

`PrivShield` 是隐私原语的真正执行者，前后端都只是代理与呈现层。

### 开发模式

```bash
python -m engine.server
```

推荐配置：

- 监听 `127.0.0.1`；
- 允许本地 REST / gRPC 联调；
- 日志级别偏详细，便于定位问题；
- 依赖可选 ML 能力时，缺失则自动降级。

### 商业化产品模式

推荐配置：

- 仅在私有网络中暴露；
- 根据部署需要开启 REST、gRPC、TLS、认证和限流；
- 证书、API Key、预算数据库等通过 Secret / 持久化卷管理；
- 若有多个实例，注意预算、审计和状态的一致性；
- 不建议把 agent 直接暴露给浏览器或公网。

---

## 5. 推荐部署组合

### 5.1 开发模式组合

最常见的本地联调组合是：

- 前端：`console/web` + Vite dev server
- 后端：`console/bff-go`
- agent：本机 `engine.server`

适用场景：

- 调试 API 参数；
- 调整 UI；
- 验证后端代理逻辑；
- 快速排查端点兼容性。

### 5.2 商业化产品模式组合

常见的交付组合是：

- 前端：构建后的 `dist/`，由后端、Nginx 或网关托管；
- 后端：稳定进程或容器服务，关闭热重载；
- agent：仅内网可达，优先启用 TLS / Auth / Rate Limit；
- 入口：统一通过域名、反向代理或 Ingress 暴露。

如果是对外演示或企业内网部署，推荐做到：

1. 前端和 API 同源；
2. 只暴露一个业务入口；
3. agent 不直接暴露给终端浏览器；
4. 证书与密钥走 Secret 管理；
5. 打开可观测性，至少保留健康检查与请求日志。

---

## 6. 配置关注点速查

### 前端

- `console/web/vite.config.ts`：开发代理仅适用于开发；
- `console/web/dist/`：生产静态产物；
- `BackendSelector`：生产环境建议只暴露可用后端，避免用户切换到未启用的链路。

### Go 后端

- `PRIVACY_CONSOLE_STATIC_DIR`：控制静态托管与 API 模式；
- `PRIVACY_AGENT_GRPC_HOST/PORT`：上游 gRPC 地址；
- `PRIVACY_AGENT_TLS_*`：商业化产品模式建议开启。

### agent

- `AGENT_TLS_ENABLED` / `AGENT_AUTH_ENABLED` / `AGENT_RATE_LIMIT_ENABLED`：生产建议按需开启；
- `PRIVACY_PROFILE`：业务参数配置；
- `PRIVACY_BUDGET_DB`：多实例时建议使用持久化后端。

---

## 7. 与现有文档的关系

- Go 后端的详细运维说明：`console/bff-go/docs/ops.md`
- 本文档：从“整条链路”角度说明前端、后端、agent 的模式差异

如果你只想看某一个组件的启动、CORS 或 mTLS 细节，优先阅读对应的 `ops.md`；如果你想先理解整个 console 在不同模式下该怎么组合，优先阅读本文。

