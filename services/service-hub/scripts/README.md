# Service Hub 脚本与独立运维指南 (`scripts/`)

> 本目录包含了 **数联数据服务调度中枢 (`service-hub`)** 的所有独立构建、单服务部署、证书生成、健康巡检与全链路测试脚本。

---

## 1. 脚本清单与功能速览

| 脚本文件 | 类型 | 主要功能 | 适用场景 |
|---|---|---|---|
| [`deploy.sh`](deploy.sh) | Docker | 独立编译 Docker 镜像并启动单机容器（支持 SQLite 数据卷挂载与健康探测） | 单服务 Docker 容器化运行 / 本地测试 |
| [`stop-docker.sh`](stop-docker.sh) | Docker | 停止并清理 `service-hub` 独立容器 | 容器清理 / 重新部署 |
| [`deploy-k8s.sh`](deploy-k8s.sh) | Kubernetes | 使用 `deploy/k8s/` 目录下的自包含清单独立部署到 K8s（支持 `--with-postgres`） | 单服务独立发布 / K8s 集群部署 |
| [`stop-k8s.sh`](stop-k8s.sh) | Kubernetes | 卸载与清理 `service-hub` 在 K8s 中的所有独立资源与 PostgreSQL 资源 | 集群资源清理 / 卸载下线 |
| [`gen-certs.sh`](gen-certs.sh) | 安全/证书 | 生成 TLS 1.3 服务端证书、客户端证书及 CA 根证书，用于 gRPC/REST 双向认证 | mTLS 安全联调与生产证书准备 |
| [`health-check.sh`](health-check.sh) | 运维探针 | 对 `/health`、`/v1/hub/status` 与 `/v1/hub/pipeline` 进行自动化探测 | 服务健康检查 / 巡检监控 |
| [`simulate-pipeline.sh`](simulate-pipeline.sh) | 业务仿真 | 模拟完整的 6 阶段流水线请求调度（`ingest ➔ fetch ➔ classify ➔ desensitize ➔ return ➔ audit`） | E2E 业务联调 / 演示汇报 |
| [`test-scripts.sh`](test-scripts.sh) | 测试套件 | 全自动化测试所有运维脚本（静态检查、Shell 语法扫描、参数解析、证书生成与离线容错） | 脚本质量保障 / CI 回归测试 |

---

## 2. 脚本使用详解

### 2.1 独立 Docker 容器部署

```bash
# 启动独立容器（自动构建镜像并挂载 SQLite 持久化卷）
bash ./scripts/deploy.sh

# 自定义端口与依赖 Agent 地址
SERVICE_HUB_PORT=8082 \
SERVICE_HUB_GRPC_PORT=50052 \
PRIVACY_AGENT_REST_HOST=127.0.0.1 \
PRIVACY_REST_PORT=8079 \
bash ./scripts/deploy.sh

# 停止容器
bash ./scripts/stop-docker.sh
```

### 2.2 独立 Kubernetes 部署

```bash
# 1. 阶段 A（单副本 SQLite 持久化）：
bash ./scripts/deploy-k8s.sh -n privshield

# 2. 阶段 B（多副本 PostgreSQL 租约架构）：
bash ./scripts/deploy-k8s.sh -n privshield --with-postgres

# 3. 演练模式（Dry-Run，仅验证清单合法性）：
bash ./scripts/deploy-k8s.sh --dry-run

# 4. 卸载集群资源：
bash ./scripts/stop-k8s.sh -n privshield [--with-postgres]
```

### 2.3 证书生成与安全加固

```bash
# 生成全套 mTLS 证书（保存至 certs/ 目录）
bash ./scripts/gen-certs.sh
```

### 2.4 健康检查与流水线模拟

```bash
# 运行健康检查
bash ./scripts/health-check.sh

# 发起全链路流水线模拟任务
bash ./scripts/simulate-pipeline.sh
```

---

## 3. 环境变量与配置对照

| 环境变量 | 默认值 | 作用脚本 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_IMAGE` | `privshield-service-hub:1.8.0` | `deploy.sh` | Docker 镜像名称 |
| `SERVICE_HUB_CONTAINER` | `privshield-service-hub` | `deploy.sh`, `stop-docker.sh` | Docker 容器名称 |
| `SERVICE_HUB_PORT` | `8082` | `deploy.sh`, `health-check.sh` | HTTP REST 服务端口 |
| `SERVICE_HUB_GRPC_PORT` | `50052` | `deploy.sh` | gRPC 内部调用端口 |
| `SERVICE_HUB_DATA_DIR` | `privshield-service-hub-data` | `deploy.sh` | SQLite 数据卷名/路径 |
| `K8S_NAMESPACE` | `privshield` | `deploy-k8s.sh`, `stop-k8s.sh` | Kubernetes 目标命名空间 |
