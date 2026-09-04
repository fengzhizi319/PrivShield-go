# 模拟数据源服务脚本与独立运维指南 (`scripts/`)

> 本目录包含了 **模拟数据源服务 (`datasource-mgr`)** 的所有本地开发、mTLS 加固运行、单服务容器化部署、Kubernetes 独立发布、证书生成与健康检查脚本。

---

## 1. 脚本清单与功能速览

| 脚本文件 | 类型 | 主要功能 | 适用场景 |
|---|---|---|---|
| [`dev-run.sh`](dev-run.sh) | 本地运行 | 启动轻量开发模式（明文 HTTP :8083 与 insecure gRPC :50053，免 mTLS） | 本地快速编码与功能联调 |
| [`prod-run.sh`](prod-run.sh) | 本地运行 | 启动生产加固模式（启用 TLS 1.3 双向认证与客户端公钥固定） | 安全机制验证与生产本地演练 |
| [`deploy.sh`](deploy.sh) | Docker | 独立编译 Docker 镜像并启动单机容器 | 单服务 Docker 容器化运行 / CI 测试 |
| [`stop-docker.sh`](stop-docker.sh) | Docker | 停止并清理 `datasource-mgr` 独立容器 | 容器清理 / 重新部署 |
| [`deploy-k8s.sh`](deploy-k8s.sh) | Kubernetes | 使用 `deploy/k8s/` 目录下的自包含清单独立部署到 K8s | 单服务独立发布 / K8s 集群部署 |
| [`stop-k8s.sh`](stop-k8s.sh) | Kubernetes | 卸载与清理 `datasource-mgr` 在 K8s 中的所有独立资源 | 集群资源清理 / 卸载下线 |
| [`gen-certs.sh`](gen-certs.sh) | 安全/证书 | 生成测试根 CA、服务端证书、客户端证书，并自动导出客户端公钥 PEM | mTLS 证书生成与公钥固定链准备 |
| [`health-check.sh`](health-check.sh) | 运维探针 | 探测 `/health` 与 `/v1/yibao` 端点连通性 | 服务探活与数据集可读性检查 |

---

## 2. 脚本使用详解

### 2.1 本地原生开发与生产加固运行

```bash
# 1. 开发运行（免证书）
bash ./scripts/dev-run.sh

# 2. 生产加固运行（启用 mTLS 与公钥固定）
bash ./scripts/prod-run.sh
```

### 2.2 独立 Docker 容器部署

```bash
# 构建并启动独立容器
bash ./scripts/deploy.sh

# 停止并清理容器
bash ./scripts/stop-docker.sh
```

### 2.3 独立 Kubernetes 部署

```bash
# 1. 独立部署到指定命名空间：
bash ./scripts/deploy-k8s.sh -n privshield

# 2. 演练模式（Dry-Run）：
bash ./scripts/deploy-k8s.sh --dry-run

# 3. 卸载集群资源：
bash ./scripts/stop-k8s.sh -n privshield
```

### 2.4 证书更新与健康检查

```bash
# 生成/更新测试证书链及固定公钥
bash ./scripts/gen-certs.sh

# 执行健康检查
bash ./scripts/health-check.sh
```

---

## 3. 环境变量速查

| 环境变量 | 默认值 | 作用脚本 | 说明 |
|---|---|---|---|
| `DATASOURCE_MGR_HOST` | `127.0.0.1` | `dev-run.sh`, `prod-run.sh` | HTTP 服务监听地址 |
| `DATASOURCE_MGR_PORT` | `8083` | 全部部署与检查脚本 | HTTP REST 服务端口 |
| `DATASOURCE_MGR_GRPC_PORT` | `50053` | 全部部署脚本 | gRPC 服务端口 |
| `DATASOURCE_MGR_TLS_ENABLED` | `false` / `true` | `prod-run.sh` | 是否开启 TLS/mTLS |
| `DATASOURCE_MGR_TLS_PINNED_PUBKEY_FILE` | `certs/client.pub` | `prod-run.sh` | 客户端固定公钥 PEM 路径 |
| `K8S_NAMESPACE` | `privshield` | `deploy-k8s.sh`, `stop-k8s.sh` | Kubernetes 目标命名空间 |
