# 模拟数据源服务 (Mock Datasource Manager)

`services/datasource-mgr` 是 PrivShield 平台的轻量级模拟数据源服务。**本项目专为开发、测试与调试阶段提供真实业务数据仿真与跨服务通信验证**；在生产环境中，调度中枢将直接对接真实外部数据源。

---

## 核心功能与特性

- **固定模拟数据库**：内置脱敏场景常用的医保就医结算数据（`yibao.csv`）与康养健康档案数据（`kangyang.csv`）；
- **四级接口契约分级体系**（调度流水线以「按身份证号查询单条记录」为唯一取数模型）：
  - **Class A 核心生产契约 (P0)**：按身份证号查询单条记录（`GET /api/datasources/:id/record-by-id?id_card_no=xxx` / `rpc GetRecordByIDCard`）、数据源连通性自测（`POST /api/datasources/:id/test` / `rpc TestConnection`）、服务存活探针（`GET /health` / `rpc Health`）；
  - **Class B 可选元数据契约 (P2)**：资产目录 / 详情（`GET /api/datasources`、`GET /api/datasources/:id`）、Schema 元数据探查（`GET /api/datasources/:id/metadata`）；
  - **Class C 容器基础设施端点**：K8s 就绪探针（`GET /readyz`）、Prometheus 监控指标（`GET /metrics`）；
  - **Class D 本地 Mock 辅助（生产严禁开放）**：数据播种重置（`POST /api/datasources/seed`）与本地测试桩审计（`GET /api/datasources/:id/audit`），受 `DATASOURCE_MGR_ENABLE_MOCK_HELPERS` 开关控制（生产模式设为 `false` 物理屏蔽）。
- **双协议通信支持**：对外提供 HTTP/HTTPS REST（端口 `:8083`），对内提供高性能 gRPC（端口 `:50053`）；
- **全链路 mTLS 双向认证与公钥固定**：HTTP/HTTPS 与 gRPC 服务均支持 TLS 1.3 客户端证书校验与客户端公钥固定（Public Key Pinning）；
- **测试证书持久入库**：预置全套测试证书链与已固定的公钥文件（`certs/client.pub`），无需每次测试重新生成，保障公钥固定机制可复现；
- **无状态设计**：纯无状态服务，无持久化存储与任务队列，天然具备崩溃恢复能力；
- 📖 **可靠性能力详解**：[docs/reliability.md](docs/reliability.md)

> 📖 **深度学习指南**：完整架构解析、数据集字典说明与源码导读见 [docs/learning-guide.md](docs/learning-guide.md)。

---

## 运行脚本指南

### 1. 开发运行 (Development Run)

无需 mTLS，直接启动轻量开发服务：

```bash
cd services/datasource-mgr
bash scripts/dev-run.sh
# 或者使用 Makefile 快捷命令：
make dev
```

监听：
- **HTTP REST**：`http://127.0.0.1:8083`
- **gRPC (insecure)**：`127.0.0.1:50053`

### 2. 生产加固运行 (Production Run with mTLS)

启用完整的 TLS 1.3 双向证书校验与客户端公钥固定（HTTPS + gRPC 双协议加固）：

```bash
cd services/datasource-mgr
bash scripts/prod-run.sh
# 或者使用 Makefile 快捷命令：
make prod
```

监听：
- **HTTPS REST (mTLS)**：`https://0.0.0.0:8083`（支持客户端证书与公钥固定校验）
- **gRPC (mTLS)**：`0.0.0.0:50053`（校验 `certs/ca.crt`、`certs/server.crt` 与固定公钥 `certs/client.pub`）

### 3. 证书重新生成脚本 (Generate Certs)

如需更新测试证书链：

```bash
cd services/datasource-mgr
bash scripts/gen-certs.sh
# 或 make gen-certs
```

生成文件清单（存放于 `certs/` 并提交至 Git）：
- `ca.crt` / `ca.key`：测试根 CA
- `server.crt` / `server.key`：服务端 X.509 证书（SAN: `localhost`, `127.0.0.1`）
- `client.crt` / `client.key`：客户端 X.509 证书（EKU: `clientAuth`）
- `client.pub`：提取的客户端 RSA 公钥（用于静态公钥固定校验）

---

## 运行测试

```bash
# 运行 datasource-mgr 全部单元测试
go test -v ./services/datasource-mgr/...

# 运行整个 Go 工作区测试
make test-go
```

---

## 容器化与独立 Kubernetes 部署

```bash
# 1. 独立构建 Docker 镜像（构建上下文需在仓库根目录以包含共享 pkg/）
docker build -f services/datasource-mgr/Dockerfile -t datasource-mgr:latest .

# 2. 独立部署到 Kubernetes（使用单服务自包含清单）
kubectl apply -k services/datasource-mgr/deploy/k8s/
```
