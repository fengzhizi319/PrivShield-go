# PrivShield 部署全景指南（deploy/）

> 本目录汇总了 PrivShield 全栈的所有部署资产：Docker Compose 编排、Helm Chart、
> 原生 K8s 清单，以及 Prometheus / Grafana 监控配置。本文是部署入口导航，
> 各部署形态的深度文档见 [`docs/deployment/`](../docs/deployment/)。

---

## 1. 目录结构总览

```text
deploy/
├── README.md                    # ← 本文件：部署全景导航
├── docker-compose/              # Docker Compose 全栈编排（单机/演示/开发/生产）
│   ├── README.md                #   Compose 使用详解（文件矩阵、生产准备、CI 测试）
│   ├── docker-compose.yml       #   通用全栈编排（本地构建，支持 --profile llm / monitoring）
│   ├── docker-compose.prod.yml  #   生产编排（纯镜像、TLS、Redis 限流、安全加固）
│   ├── docker-compose.dev.yml   #   开发联调编排（源码挂载、免鉴权）
│   ├── docker-compose.test.yml  #   CI 集成测试编排（test-runner 自动冒烟）
│   ├── .env.prod.example        #   生产环境变量模板（cp 为 .env 后填写）
│   └── privacy-profile.yaml     #   隐私原语参数 profile（挂载至容器）
├── helm/PrivShield/             # 生产级全栈 Helm Chart（HPA / PDB / NetworkPolicy / Ingress）
│   ├── Chart.yaml
│   ├── values.yaml              #   默认值（开发模式）
│   ├── values-production.yaml   #   生产覆盖值（2 副本 + TLS + Auth + 限流 + HPA）
│   ├── values-ml.yaml           #   ML 镜像覆盖值（torch/transformers/onnxruntime）
│   └── templates/               #   K8s 资源模板
├── k8s/                         # 原生 K8s 全栈集成清单（Kustomize 入口，通过相对路径引用各子服务 deploy/k8s）
│   ├── kustomization.yaml       #   聚合 Agent + services/* + console/* 的全栈主入口
│   ├── namespace.yaml / configmap.yaml / deployment.yaml / service.yaml
│   └── secret.example.yaml      #   TLS + API Key 示例（需自行填值）
├── prometheus/                  # Prometheus 采集配置与告警规则
│   ├── prometheus.yml
│   └── alerts.yml
└── grafana/                     # Grafana 预置仪表盘（JSON 大屏）
    ├── dashboard.json           #   PrivShield 全景总览大屏
    └── service-hub-dashboard.json  #   调度中枢专属大屏
```

> 💡 **架构分层说明**：
> - **单服务原子部署**：各独立子项目（`services/service-hub`、`console/mock-datasource`、`services/audit-log`、`console`）在各自的 `deploy/k8s/` 目录下自包含原子 Deployment/Service/PVC 清单与 `Dockerfile`，支持单服务独立构建与独立发布；
> - **全栈集成编排**：`deploy/` 根目录统一管理多服务全栈拓扑、统一 Compose 联调、统一 Helm 伞图（Umbrella Chart）与全栈 Kustomize 集成入口。

---

## 2. 服务拓扑与端口

全栈共 8 类业务服务 + 可选的 LLM 与监控：

| 服务 | 角色 | 端口 | 技术栈 |
|---|---|---|---|
| **PrivShield (Core Agent)** | 隐私计算引擎（脱敏/DP/K-匿名/分类分级） | REST `8079` / gRPC `50051` | Python (FastAPI + gRPC) |
| **bff-go** (`console/engine-console/bff-go`) | Console Go 代理（REST + gRPC 双协议） | `8081` / `50055` | Go (Gin + gRPC) |
| **console-web** | React 控制台前端（Nginx 托管） | `5173` | React + Nginx |
| **service-hub** (`services/service-hub`) | 数据服务调度中枢 | REST `8082` / gRPC `50052` | Go |
| **datasource-mgr** (`console/mock-datasource`) | 数据源管理 | REST `8083` / gRPC `50053` | Go |
| **audit-log** (`services/audit-log`) | 脱敏审计日志 | REST `8084` / gRPC `50054` | Go |
| **vllm**（可选，`--profile llm`） | Layer-3 LLM 推理（GPU） | `8000` | vLLM |
| **redis**（仅生产编排） | 分布式限流后端 | `6379`（内部） | Redis |
| **phase-b-postgres**（可选，`--profile phase-b`） | Phase B 多副本 Hub 原子租约后端 | `5432` | PostgreSQL |
| **prometheus / grafana**（可选，`--profile monitoring`） | 监控与可视化 | `9090` / `3000` | Prometheus / Grafana |

调用链路：`console-web → bff-go → PrivShield(REST/gRPC)`；
`service-hub / datasource-mgr / audit-log → PrivShield(REST)`；
`PrivShield → vllm`（Layer-3 分类）。

---

## 3. 部署方式选型

| 场景 | 推荐方式 | 入口 |
|---|---|---|
| 本地开发 / 演示 / 单机 | Docker Compose（通用编排） | §4.1 |
| 生产单机（无 K8s） | Docker Compose 生产编排 | §4.2 |
| 生产多副本 / 弹性伸缩 / 合规 | Helm Chart | §5 |
| 最小化学习 / 无 Helm 环境 | 原生 K8s 清单 | §6 |
| CI 集成冒烟测试 | Compose 测试编排 | `deploy/docker-compose/README.md` §4 |

---

## 4. Docker Compose 部署

> 详细用法（文件矩阵、证书准备、日志查看）见
> [`deploy/docker-compose/README.md`](docker-compose/README.md)。

### 4.1 通用全栈（本地构建）

```bash
# 前置：构建 Agent 镜像（core 或 ml）
make docker-core          # 或 make docker-ml

cd deploy/docker-compose
docker compose up -d                              # 基础全栈
docker compose --profile llm up -d                # + vLLM GPU 推理
docker compose --profile monitoring up -d         # + Prometheus/Grafana
docker compose --profile phase-b up -d            # + Phase B PostgreSQL (多副本 Hub)
```

### 4.2 生产编排

```bash
cd deploy/docker-compose

# 1. 准备环境变量（强密码 / API Key / Grafana 密码必填）
cp .env.prod.example .env && vim .env

# 2. 准备 TLS 证书
mkdir -p certs && cp /path/to/tls.crt certs/tls.crt && cp /path/to/tls.key certs/tls.key

# 3. 启动（纯镜像、不挂载源码、restart: always）
docker compose -f docker-compose.prod.yml up -d

# 4. 查看状态
docker compose -f docker-compose.prod.yml ps
docker compose -f docker-compose.prod.yml logs -f PrivShield
```

生产编排关键特性：全链路 TLS + API Key 鉴权、Redis 分布式限流、
`security_opt: no-new-privileges`、资源 limits/reservations、
JSON 结构化日志（json-file 滚动）、命名卷持久化（预算库/审计日志/各服务 DB）。

或使用一键脚本：

```bash
bash ./scripts/prod/deploy-docker-compose.sh [--with-llm] [--with-monitoring] [--with-postgres] [--agent-only]
bash ./scripts/prod/stop-docker-compose.sh
```

---

## 5. Helm 部署（生产级）

```bash
# 开发模式
helm install privshield ./deploy/helm/PrivShield

# 生产模式（需自管 TLS / API Key Secret）
kubectl create secret tls privshield-tls --cert=tls.crt --key=tls.key -n PrivShield
kubectl create secret generic privshield-apikeys --from-file=api-keys.json -n PrivShield

helm install privshield ./deploy/helm/PrivShield \
  -f ./deploy/helm/PrivShield/values-production.yaml \
  --set security.tls.existingSecret=privshield-tls \
  --set security.auth.apiKeysSecret=privshield-apikeys

# ML 镜像
helm install privshield ./deploy/helm/PrivShield -f ./deploy/helm/PrivShield/values-ml.yaml
```

生产模式额外启用：2 副本、HPA（2~10）、PDB、NetworkPolicy、ServiceMonitor（需 Prometheus Operator）。

常用命令：

```bash
make helm-lint            # helm lint 静态检查
make helm-template        # 模板渲染验证
bash ./scripts/prod/deploy-helm.sh      # 一键部署
bash ./scripts/prod/uninstall-helm.sh   # 卸载
```

---

## 6. 原生 K8s 部署（最小清单）

```bash
kubectl apply -k ./deploy/k8s/
kubectl get pods -n PrivShield -w
kubectl port-forward -n PrivShield svc/PrivShield 8079:8079 50051:50051
curl http://localhost:8079/health

# 启用 Phase B PostgreSQL（多副本 Hub 模式）
bash ./scripts/prod/deploy-k8s.sh --with-postgres

# 停止
bash ./scripts/prod/stop-k8s.sh
```

> TLS 证书与 API Key 请复制 `deploy/k8s/secret.example.yaml` 并填入真实值后再启用。

---

## 7. 监控与可观测性

- **指标**：Agent 内置 `/metrics`（`privacy_*` 前缀），采集配置见
  `deploy/prometheus/prometheus.yml`，告警规则见 `deploy/prometheus/alerts.yml`。
- **大屏**：`deploy/grafana/` 下预置两个 Grafana 仪表盘：
  - `dashboard.json` — PrivShield 全景总览（引擎吞吐/预算消耗/分类漏斗分布）
  - `service-hub-dashboard.json` — 调度中枢专属大屏（租约状态/微服务健康/Go 协程池）
  
  启用 `--profile monitoring` 时经 provisioning 自动加载；也可在 Grafana 中手动 Import。
- **健康检查**：`bash ./scripts/prod/prod_health_check.sh`（REST `/health`、`/readyz`、gRPC 探活）。
- **日志**：Go 引擎与 Go 微服务固定输出 JSON 结构化日志，便于 ELK / Loki 收集（Go 微服务用 `*_LOG_LEVEL` 调级别，`*_LOG_FORMAT` 仅 Python 引擎读取）。
- 深度文档：[`docs/production_observability/`](../docs/production_observability/)。

---

## 8. 运维入口速查

| 目标 | 命令 |
|---|---|
| 生产 Compose 部署 | `bash ./scripts/prod/deploy-docker-compose.sh` |
| 停止生产 Compose | `bash ./scripts/prod/stop-docker-compose.sh` |
| Helm 部署 / 卸载 | `bash ./scripts/prod/deploy-helm.sh` / `uninstall-helm.sh` |
| K8s 部署 / 停止 | `bash ./scripts/prod/deploy-k8s.sh [--with-postgres]` / `stop-k8s.sh [--with-postgres]` |
| 生产健康检查 | `bash ./scripts/prod/prod_health_check.sh` |
| 备份隐私预算库 | `bash ./scripts/prod/backup_privacy_budget.sh` |
| 构建 core / ml 镜像 | `make docker-core` / `make docker-ml` |
| Helm lint / template | `make helm-lint` / `make helm-template` |

---

## 9. 延伸文档

| 文档 | 内容 |
|---|---|
| [`docs/deployment/README.md`](../docs/deployment/README.md) | 部署文档总入口 |
| [`docs/deployment/design.md`](../docs/deployment/design.md) | 部署架构与 Compose vs K8s 选型决策 |
| [`docs/deployment/from_code_to_k8s.md`](../docs/deployment/from_code_to_k8s.md) | 从代码到 K8s 的完整教程（清单拆解、Helm、监控） |
| [`docs/deployment/ops.md`](../docs/deployment/ops.md) | 安装、升级与故障排查手册 |
| [`docs/deployment/testing.md`](../docs/deployment/testing.md) | 部署测试清单（helm lint / dry-run / kind） |
| [`deploy/docker-compose/README.md`](docker-compose/README.md) | Compose 文件矩阵与使用详解 |
| [`docs/production_security/ops.md`](../docs/production_security/ops.md) | TLS / 证书 / 鉴权运维速查 |
| [`docs/production_observability/ops.md`](../docs/production_observability/ops.md) | 监控配置与 Grafana 示例 |
