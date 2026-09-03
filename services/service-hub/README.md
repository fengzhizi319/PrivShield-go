# 数据服务调度中枢 (Service Hub)

数据服务调度中枢是 PrivShield 控制台的核心调度微服务，负责统一管理数据请求的全生命周期：从请求接入、原数取用（联动 `datasource-mgr`）、分类分级（联动 `PrivShield Agent`）、动态脱敏、跨机存证（联动 `audit-log`）到安全回传。

---

## 功能特性

- **唯一外部调度编排中枢**：作为外部模拟应用（如 `app-lz`）接入数据网格的唯一受信任边界中枢，所有外部数据请求（拓扑探活、数据源探查、切片获取、存证写入、审计查询与 Merkle 验真）统一由 `service-hub` 代理编排，外部系统无权直连网格内部下游微服务；
- **跨服务联动编排**：联动 `datasource-mgr`（按身份证号取数与切片采样）、`PrivShield Agent`（一体化分类脱敏）与 `audit-log`（P0-6 出域存证 fail-closed）；
- **全链路流水线可视化**：实时追踪 `ingest` ➔ `fetch` ➔ `classify` ➔ `desensitize` ➔ `return` ➔ `audit` 六大阶段；
- **分类分级智能调度**：根据数据敏感度等级（L1~L5）自动匹配最适脱敏原语（明文/掩码/K-匿名/差分隐私；L4/L5 均映射为 `dp`）；
- **双协议暴露**：同时支持面向 Web 控制台的 HTTP REST (:8082) 与面向高性能内部调用的 gRPC mTLS (:50052)；
- **生产级高可用**：SQLite WAL 持久化、并发信号量防 DoS 击穿、Slowloris 慢连接防御及 Prometheus `/metrics` 监控；
- **崩溃恢复与自动重试**：启动时自动回收孤立任务（running 标记失败、pending 保留队列），周期性后台重试失败任务（指数退避 + RetryCount）；
- **完整性校验与备份**：启动时 `PRAGMA integrity_check` 阻断损坏数据库，统一备份脚本支持全量/增量/验证模式；
- **HTTP/gRPC 双协议 mTLS**：共享 `pkg/tlsutil` 工具库，TLS 1.3 + 公钥固定；
- 📖 **可靠性能力详解**：[docs/reliability.md](docs/reliability.md)

> 📖 **深度学习指南**：完整架构解析、六阶段调度流水线实现与源码导读见 [docs/learning-guide.md](docs/learning-guide.md)。

---

## 快速开始

### 本地原生运行

```bash
# 方式 1: 直接使用 go 运行
cd services/service-hub
go run ./cmd/server

# 方式 2: 使用 Makefile 构建并运行
make build
./bin/server
```

默认监听地址与端口：
- **HTTP REST**: `http://127.0.0.1:8082`
- **gRPC**: `127.0.0.1:50052`
- **上游 Agent 依赖**: `http://127.0.0.1:8079`
- **下游模拟数据源**: `http://127.0.0.1:8083`

---

## 环境变量速查

| 环境变量 | 默认值 | 类型 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_HOST` | `127.0.0.1` | string | HTTP REST 服务监听主机 |
| `SERVICE_HUB_PORT` | `8082` | int | HTTP REST 服务监听端口 |
| `SERVICE_HUB_GRPC_HOST` | `127.0.0.1` | string | gRPC 服务监听主机 |
| `SERVICE_HUB_GRPC_PORT` | `50052` | int | gRPC 服务监听端口 |
| `PRIVACY_AGENT_REST_HOST` | `127.0.0.1` | string | 上游 PrivShield Agent REST 主机 |
| `PRIVACY_REST_PORT` | `8079` | int | 上游 PrivShield Agent REST 端口 |
| `PRIVACY_AGENT_API_KEY` | `""` | string | 请求上游 Agent 的 API Key |
| `PRIVACY_AGENT_URLS` | `""` | string | 多 Agent 负载均衡/故障转移地址（逗号分隔） |
| `SERVICE_HUB_MAX_QUEUE` | `1000` | int | 调度引擎最大排队深度 |
| `SERVICE_HUB_SCHEDULE_TIMEOUT` | `30` | int | 任务单步调度与执行超时（秒） |
| `DATASOURCE_MGR_HOST` | `127.0.0.1` | string | 模拟数据源 HTTP 主机 |
| `DATASOURCE_MGR_PORT` | `8083` | int | 模拟数据源 HTTP 端口 |
| `DATASOURCE_MGR_GRPC_HOST` | `127.0.0.1` | string | 模拟数据源 gRPC 主机 |
| `DATASOURCE_MGR_GRPC_PORT` | `50053` | int | 模拟数据源 gRPC 端口 |
| `SERVICE_HUB_TLS_ENABLED` | `false` | bool | 是否启用 HTTP/gRPC TLS 强加密 |
| `SERVICE_HUB_TLS_CERT_FILE` | `""` | string | 服务端证书 X.509 路径 |
| `SERVICE_HUB_TLS_KEY_FILE` | `""` | string | 服务端私钥路径 |
| `SERVICE_HUB_TLS_CA_FILE` | `""` | string | 验证客户端身份的 CA 证书路径 |
| `SERVICE_HUB_TLS_CLIENT_AUTH` | `""` | string | 客户端双向认证模式 (`require`/`verify`/`request`) |
| `SERVICE_HUB_TLS_PINNED_PUBKEY_FILE` | `""` | string | 客户端固定公钥 PEM 路径 (SPKI Pinning) |
| `SERVICE_HUB_API_KEY` | `""` | string | 本模块入站 API Key 鉴权（空表示免密） |
| `SERVICE_HUB_CORS_ORIGINS` | `""` | string | 允许跨域的 Origin 白名单（逗号分隔） |
| `SERVICE_HUB_DB_PATH` | `""` | string | SQLite 数据库路径（空表示纯内存模式） |
| `SERVICE_HUB_RETENTION_DAYS` | `30` | int | 终态任务保留天数（0 表示不清理） |
| `SERVICE_HUB_SHUTDOWN_TIMEOUT` | `5` | int | 优雅停机超时（秒） |
| `SERVICE_HUB_LOG_FORMAT` | `json` | string | 结构化日志格式 (`json`/`text`) |
| `SERVICE_HUB_LOG_LEVEL` | `info` | string | 日志级别 (`debug`/`info`/`warn`/`error`) |

> 完整环境变量（含 P0-6 出域存证 `SERVICE_HUB_AUDIT_LOG_*`、P0-4 `SERVICE_HUB_STRICT_STORAGE`、Scope 鉴权 `SERVICE_HUB_API_KEYS`、gRPC CN 白名单 `PRIVACY_AUTH_MTLS_WHITELIST_FILE`、限流与 CIDR 白名单等）见 [docs/ops.md](docs/ops.md) §2 与 [docs/api.md](docs/api.md) §7.2。

---

## 路由与 API 清单

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| GET | `/health` | 免密 | 存活探针（Liveness Probe，进程存活即返回 200） |
| GET | `/readyz` | 免密 | 就绪探针（Readiness Probe，检查 Agent+Datasource 连通性，失败返回 503） |
| GET | `/api/health` | 免密 | 综合健康检查（兼容别名，返回自身与上下游延迟） |
| GET | `/api/hub/status` | 可选 | 调度中枢运行状态概览 |
| GET | `/api/hub/tasks` | 可选 | 任务列表（支持分页与 `?status=` 过滤） |
| GET | `/api/hub/tasks/:id` | 可选 | 获取单个任务详情 |
| POST | `/api/hub/dispatch` | 可选 | 手动分发隐私处理任务到流水线 |
| GET | `/api/hub/pipeline` | 可选 | 流水线 6 阶段实时活跃状态 |
| POST | `/api/hub/classify` | 可选 | 智能分类分级 + 自动策略脱敏分发 |
| POST | `/api/hub/fetch-and-desensitize` | 可选 | 按身份证号端到端查询+脱敏（同步，需 `hub:dispatch` scope） |
| GET | `/api/hub/topology` | 可选 | **外部编排**：网格拓扑与微服务健康状态全景探针 |
| GET | `/api/hub/datasources` | 可选 | **外部编排**：数据源资产目录查询代理 |
| GET | `/api/hub/audit/logs` | 可选 | **外部编排**：不可篡改审计日志查询代理 |
| POST | `/api/hub/audit/logs` | 可选 | **外部编排**：审计存证日志写入代理 |
| POST | `/api/hub/audit/verify` | 可选 | **外部编排**：Merkle Tree 完整性验真代理 |
| GET | `/metrics` | 免密 | Prometheus 监控指标采集端点 |

---

## 构建与测试

```bash
# 运行单元测试
go test -v ./services/service-hub/...

# 运行真实跨服务 E2E 全链路流水线测试（需启动 Agent :8079）
PRIVSHIELD_E2E=1 go test -v -run TestRealE2E ./services/service-hub/internal/handlers/

# 编译 Linux 静态二进制
CGO_ENABLED=0 go build -ldflags="-w -s" -o bin/server ./cmd/server
```

---

## 容器化与独立 Kubernetes 部署

```bash
# 1. 独立构建 Docker 镜像（构建上下文需在仓库根目录以包含共享 pkg/）
docker build -f services/service-hub/Dockerfile -t service-hub:latest .

# 2. 独立部署到 Kubernetes（使用单服务自包含清单）
kubectl apply -k services/service-hub/deploy/k8s/

# 3. 部署 Phase B PostgreSQL 多副本后端（可选）
kubectl apply -k services/service-hub/deploy/k8s/postgres/
```
