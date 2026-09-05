# 数盾 PrivShield (Data & Privacy Shield) — Agent Guide

> AI coding agent guide for the **数联天下 · 数盾 (`PrivShield`)** project. Read this before modifying code.

`PrivShield` is an enterprise privacy-preserving governance sidecar implementing the **「三层四柱五御六类」数据安全与隐私治理架构** (3-Funnel, 4-Pillar, 5-Protection, 6-Category Architecture), exposing 44 privacy primitives (masking, differential privacy, K-anonymity, query obfuscation) and a 3-layer data classification funnel over REST (Gin) and gRPC in pure **Go 1.25+ Cloud-Native**.

---

## 1. Project Overview

| Capability | Status | Notes |
|---|---|---|
| Masking | ✅ Ready | Field-name-aware masking + SM3/SM4 national crypto + ASCII fast-path |
| Differential Privacy | ✅ Ready | Fused single-pass Laplace/Gaussian count/sum/mean + atomic budget accounting |
| Local Differential Privacy (LDP) | ✅ Ready | Multi-core chunked randomized response / categorical / projected frequency |
| K-anonymity & L-Diversity | ✅ Ready | Mondrian KD-tree + Distinct L-Diversity verification + recursion guard |
| Query Obfuscation | ✅ Ready | Dummy query injection & Fisher-Yates shuffle |
| File Privacy Processing | ✅ Ready | CSV (UTF-8 BOM strip) / Excel (XLSX string interning + Zip bomb limit) / JSON |
| Medical Data Pipeline | ✅ Ready | Native DICOM binary parsing, path traversal guard, and multi-core chunked processing |
| Profile Recommendation | ✅ Ready | Data-driven privacy profile recommendation (YAML dynamic reload) |
| Ops Diagnostics | ✅ Ready | Runtime health (`/readyz`/`/healthz`/`/ops/diagnostics`), pprof, and metric snapshots |
| 3-Layer Classification | ✅ Ready | Rule engine (AC automaton + Regex) → Small-NER (ONNX) → External LLM (Circuit Breaker) |
| Gateway / Load Balancer | ✅ Ready | REST + gRPC reverse proxy + P2C-EWMA + BufferPool zero-allocation proxy cache |
| TLS / Auth / Rate Limit | ✅ Ready | mTLS CN whitelist 5s hot-reload, constant-time auth, 32-shard token bucket |
| Observability | ✅ Ready | Standard library `log/slog` + Prometheus `/metrics` + distributed tracing |
| K8s / Helm Deployment | ✅ Ready | `deploy/helm/` + `deploy/k8s/` + `deploy/docker-compose/` |

## 2. Technology Stack

- **Go 1.25+** (Multi-module workspace `go.work`)
- **Gin** for High-Performance REST
- **gRPC** (`google.golang.org/grpc`) for Low-Latency RPC
- **YAML** (`gopkg.in/yaml.v3`) for Rule & Profile Configuration
- **ONNX Runtime Go** for Small-NER (optional, lazy-loaded)
- **External LLM Client** with 3-state Circuit Breaker (`Closed` -> `Open` -> `HalfOpen`)
- **Alpine Linux / Multi-stage Docker** (~25MB ultra-lightweight image)

## 3. Repository Layout (v3.0 Architecture)

```text
PrivShield/
├── services/                          # 商业化生产微服务群 (Production Services)
│   ├── privacy-engine/                # 核心隐私计算与动态分类分级引擎 (Core Sidecar/Agent)
│   │   ├── cmd/
│   │   │   ├── privshield-agent/      # Agent 主入口 (REST :8079 + gRPC :50051)
│   │   │   └── privshield-gateway/    # 网关与反向代理入口 (:8000 + gRPC :50000)
│   │   ├── internal/                  # 动态分类分级、网关代理、安全认证、画像等
│   │   ├── sdk/                       # 内置隐私计算数学原语库 (Masking, DP, LDP, K-Ano, Medical, Budget)
│   │   ├── rules/                     # 领域敏感特征规则库 (Taxonomies, Domains, Standards)
│   │   ├── docs/                      # 引擎自包含架构与 API 说明文档
│   │   ├── deploy/                    # 引擎专属 Dockerfile / Dockerfile.cuda / k8s / compose
│   │   ├── scripts/                   # 引擎单模块运行、测试与压测脚本
│   │   └── Makefile                   # 引擎单模块构建与测试入口
│   ├── service-hub/                   # 数联数据服务调度中枢 · 唯一编排入口 (流水线调度: :8082)
│   │   ├── docs/ deploy/ scripts/ Makefile # 自包含交付资产
│   │   └── ...
│   └── audit-log/                     # 脱敏审计日志与不可篡改存证服务 (:8084)
│       ├── docs/ deploy/ scripts/ Makefile # 自包含交付资产
│       └── ...
├── console/                           # 测试与管理生态 (Testing & Management Consoles)
│   ├── engine-console/                # Privacy Engine 专属管理控制台 (专测 privacy-engine)
│   │   ├── bff-go/                    # Engine Console BFF (:8081)
│   │   ├── web/                       # Engine Console Web 前端 (React 18 + TS + Vite :5173)
│   │   ├── docs/ deploy/ scripts/ Makefile # 自包含交付资产
│   ├── app-lz/                        # 数联调度之眼业务模拟器 (专测 service-hub 调度编排)
│   │   ├── bff-go/                    # 业务专有 BFF (:8085，所有数据请求统一走 service-hub)
│   │   ├── web/                       # 业务流水线控制台前端 (React 18 + TS + Vite :5174)
│   │   ├── docs/ deploy/ scripts/ Makefile # 自包含交付资产
│   └── mock-datasource/               # 模拟多源异构数据源服务 (:8083)
│       ├── docs/ deploy/ scripts/ Makefile # 自包含交付资产
│       └── ...
├── deploy/                            # 全栈集中部署与编排资产 (Compose / Helm / K8s)
│   ├── docker-compose/                # Docker Compose 全栈集中编排
│   ├── helm/PrivShield/               # 全栈统一 Helm Chart
│   ├── k8s/                           # 原生 K8s 全栈集成清单
│   ├── prometheus/                    # Prometheus 采集与告警规则
│   └── grafana/                       # Grafana 预置仪表盘
├── pkg/                               # Go 共享基础库 (Agent客户端, 中间件, 存储, 国密, 校验)
├── proto/privacy.proto                # gRPC 协议定义
├── go.work                            # 根目录 Go 1.25 工作区
├── config/                            # 全局 Profile & runtime 配置文件
├── data/                              # 样例数据集与测试数据
├── scripts/                           # 全局自动化运维、开发启动与全链路测试脚本
├── Makefile                           # 根目录统一全局构建与测试入口
└── Dockerfile                         # 根目录多阶段构建 Dockerfile
```

## 4. Build & Test Commands

```bash
cd /path/to/PrivShield

# 运行全仓库所有模块测试 (100% 通过)
make test

# 快速编译全局二进制产物至 bin/
make build

# 静态代码检查与格式化
make check

# 构建全套 Docker 镜像
make docker-all
```

## 5. Running Locally

### 编译并启动 Go Privacy Engine (REST :8079 + gRPC :50051)

```bash
go run ./services/privacy-engine/cmd/privshield-agent
```

### 启动 Go Privacy Gateway (REST :8000 + gRPC :50000)

```bash
go run ./services/privacy-engine/cmd/privshield-gateway
```

### 一键启动 Privacy Engine 开发控制台全家桶 (Agent + Engine Console BFF + Vite 前端)

```bash
bash ./scripts/dev/dev-engine-console.sh
```

### 一键启动调度之眼控制台 (App-LZ BFF + Vite 前端 + 4 上游微服务)

```bash
bash ./scripts/dev/dev-app-lz.sh --force          # 明文模式
bash ./scripts/dev/dev-app-lz.sh --mtls --force   # mTLS 模式
bash ./scripts/dev/dev-app-lz.sh --tlcp --force   # TLCP 国密双证书模式（与 --mtls 互斥）
```

## 6. Key Configuration

| Variable | Default | Purpose |
|---|---|---|
| `AGENT_REST_HOST` | `0.0.0.0` | REST host |
| `AGENT_REST_PORT` | `8079` | REST port |
| `AGENT_GRPC_HOST` | `0.0.0.0` | gRPC host |
| `AGENT_GRPC_PORT` | `50051` | gRPC port |
| `PRIVACY_NAMESPACE` | `default` | 隐私预算租户隔离命名空间 |
| `AGENT_TLS_ENABLED` | `false` | 启用 REST/gRPC TLS |
| `AGENT_TLS_NATIONAL_CIPHER` | `false` | REST 启用 TLCP 国密双证书模式（GM/T 0024，配合 `AGENT_TLCP_SIGN_CERT_FILE` 等） |
| `AGENT_AUTH_ENABLED` | `false` | 启用 API Key 认证 |
| `AGENT_AUTH_INTERNAL_MTLS_ENABLED` | `false` | 启用 gRPC mTLS 客户端证书认证 |
| `AGENT_AUTH_MTLS_WHITELIST_FILE` | — | CN 白名单配置文件路径 (支持 5s 热重载；中台微服务群仍用 `PRIVACY_AUTH_MTLS_WHITELIST_FILE`) |
| `AGENT_AUTH_USER_STORE_FILE` | 空（纯内存） | 用户与动态密钥持久化文件（兼容 `PRIVACY_USER_STORE_FILE`；目录 `0700`/文件 `0600`，只存摘要与口令哈希） |
| `AGENT_AUTH_USER_SELF_REGISTER` | `false` | 公开自注册开关（生产保持关闭；引导期首个 `admin` 不受此限） |
| `AGENT_AUTH_USER_SESSION_TTL` | `24h` | 登录会话有效期（`24h`/`15m`/纯秒数，超上限自动收敛） |
| `AGENT_AUTH_USER_LOGIN_THROTTLE_PER_MIN` | `20` | 登录端点每 IP 每分钟最大尝试次数（`<=0` 关闭该层限速） |
| `AGENT_RATE_LIMIT_ENABLED` | `false` | 启用 32 分片高并发令牌桶限流 |
| `PRIVACY_ENGINE_CACHE_MAX_SIZE` | `10000` | 动态分类分级 LRU 缓存容量（16 分片） |
| `PRIVACY_IMAGE_ALLOWED_DIRS` | cwd + 系统临时目录 | 医学影像处理允许读取的文件目录白名单 |
| `PRIVACY_RULES_RELOAD_CHECK_SECONDS` | `5` | 规则热重载 mtime 检测节流间隔秒数（0 = 禁用节流） |
| `PRIVACY_PPROF_ENABLED` | `false` | 启用 pprof 性能分析端点（生产默认关闭，需 `ops:admin` 权限） |
| `PRIVACY_RULES_DIR` | `services/privacy-engine/rules/domains` | 领域分类分级规则目录 |
| `PRIVACY_CONFIG_FILE` | `config/privacy.yaml` | 隐私策略配置文件路径 |

### 用户与密钥全生命周期管理（`pkg/auth` 共享内核，两端同构）

privacy-engine 与 service-hub 共用 `pkg/auth` 用户引擎，环境变量前缀分别为 `AGENT_AUTH_` 与
`SERVICE_HUB_`（变量名以 [`pkg/auth/user_handlers.go::userPolicyEnvTable`](pkg/auth/user_handlers.go)
为唯一事实源）。用户体系提供：等保三级口令复杂度（`bcrypt cost=12`、长度 8~72、字符类别 ≥3、
禁含用户名、弱口令字典）、连续 5 次失败锁定 15 分钟、登录每 IP 限速、RBAC+ABAC 8 角色矩阵、
动态 API Key 签发/吊销（`psk_<32hex>`，落盘仅存 SHA-256 摘要）、会话 Token（内存态 24h）、
以及零重启毫秒级生效的双通道活密钥（`LiveInternalKeys` 明文 + `LiveInternalHashedKeys` 摘要）。
`/v1/auth/login` 与 `/v1/auth/users/register` 为公开认证路径，其余 `/v1/auth/*` 路由层仅要求已认证、
授权在 Handler 内按主体（本人 / `user:read` / `user:admin`）强校验。

### 微服务出站 Agent 传输信任（service-hub / audit-log 的 `pkg/agent` 客户端真实读取）

`PRIVACY_AGENT_URLS` 指向 `https://` 时由 `PRIVACY_AGENT_TLS_CA_FILE` /
`PRIVACY_AGENT_TLS_INSECURE_SKIP_VERIFY` 构建标准 TLS 信任；指向 `tlcp://` 时由
`PRIVACY_AGENT_TLCP_CA_FILE` / `PRIVACY_AGENT_TLCP_INSECURE_SKIP_VERIFY` 构建国密 TLCP 信任
（CA 文件不可读在客户端构造期即报错，fail-fast）。全部缺省保持默认 HTTP 行为。

## 7. Go Coding Conventions

- 遵循标准 **Go Code Review Comments** 与 Effective Go。
- 密码学与安全比较一律使用常量时间校验 (`subtle.ConstantTimeCompare` / `hmac.Equal`)。
- 隐私原语与数学计算必须是**零状态、纯函数计算**；状态维护统一在 `service` 与 `budget`。
- 高频批量计算统一采用**无锁分块多核并行模型** (`Chunked Concurrency`)。
- 所有 REST 错误响应统一通过 `pkg/middleware.AbortWithError` 输出标准 5 字段信封。

## 8. 架构原则

- **service-hub 唯一编排入口**：`app-lz BFF` 是模拟的外部业务程序，所有数据请求统一通过 `service-hub` 调度中枢编排，不直接访问 `mock-datasource` / `privacy-engine` / `audit-log`。
- **双层资产自治**：每个微服务/控制台拥有完全独立的 `docs/`、`deploy/`、`scripts/` 与 `Makefile`，可独立测试、构建和部署；根目录资产负责全栈集中编排。
