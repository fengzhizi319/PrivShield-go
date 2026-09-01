# 本地开发与测试运维脚本 (scripts/dev)

本目录包含 **数联天下 · 数盾 (`PrivShield`)** 在本地开发调试、端到端集成测试、Docker 容器联调以及性能基准评估阶段所需的全部自动化脚本。

全栈基于 **Go 1.25+ Cloud-Native** 原生架构，所有命令均支持在 IDE Markdown 视图中**一键点击直接运行**（无论当前终端位于项目根目录还是 `scripts/dev/` 目录）。

---

## 目录索引

- [1. 本地原生开发与控制台启动脚本](#1-本地原生开发与控制台启动脚本)
  - [`dev-bff-agent.sh` / `dev-bff-agent.ps1` (Go Agent + Go BFF + 前端 HMR)](#dev-bff-agentsh--dev-bff-agentps1)
  - [`dev-app-lz.sh` (调度之眼 App-LZ: Go BFF + 前端 HMR)](#dev-app-lzsh)
  - [`go-engine-start.sh` (快速启动 Go Agent)](#go-engine-startsh)
  - [`go-gateway-start.sh` (快速启动 Go Gateway 网关)](#go-gateway-startsh)
  - [`dev-stop.sh` (停止本地开发服务)](#dev-stopsh)
  - [`stop-app-lz.sh` (停止 App-LZ 控制台服务)](#stop-app-lzsh)
- [2. 中台微服务群管理脚本](#2-中台微服务群管理脚本)
  - [`dev-start-new-modules.sh` (启动 3 大中台微服务)](#dev-start-new-modulessh)
  - [`dev-stop-new-modules.sh` (停止 3 大中台微服务)](#dev-stop-new-modulessh)
  - [`e2e-start-all-services.sh` (一键启动 Go Agent + 3 大微服务)](#e2e-start-all-servicessh)
  - [`e2e-stop-all-services.sh` (停止 Go Agent + 3 大微服务)](#e2e-stop-all-servicessh)
  - [`start_all_services.sh` (后台启动全栈服务群)](#start_all_servicessh)
  - [`stop_all_services.sh` (停止全栈服务群)](#stop_all_servicessh)
- [3. Docker 容器化联调脚本](#3-docker-容器化联调脚本)
  - [`docker-start-bff-agent.sh` / `docker-start-bff-agent.ps1` (控制台三件套容器版)](#docker-start-bff-agentsh--docker-start-bff-agentps1)
  - [`docker-start-app-lz.sh` (调度之眼 App-LZ 全栈容器版)](#docker-start-app-lzsh)
  - [`docker-stop-app-lz.sh` (停止 App-LZ 容器集群)](#docker-stop-app-lzsh)
  - [`docker-start-all.sh` / `docker-start-go-all.sh` (启动全栈 Docker 容器)](#docker-start-allsh)
  - [`docker-start-agent.sh` / `docker-start-go-agent.sh` (启动 Go Agent 容器)](#docker-start-agentsh)
  - [`docker-stop-agent.sh` / `docker-stop-go-agent.sh` (停止 Go Agent 容器)](#docker-stop-agentsh)
  - [`docker-start-llm.sh` / `docker-start-llm.ps1` (启动 vLLM 大模型容器)](#docker-start-llmsh--docker-start-llmps1)
  - [`docker-stop-llm.sh` / `docker-stop-llm.ps1` (停止 vLLM 容器)](#docker-stop-llmsh--docker-stop-llmps1)
  - [`docker-stop.sh` (停止全部 Docker 容器)](#docker-stopsh)
  - [`start-postgres.sh` (独立启动 Phase B PostgreSQL)](#start-postgressh)
- [4. 自动化测试、基准压测与环境工具](#4-自动化测试基准压测与环境工具)
  - [`go-engine-test.sh` (Go 全栈模块单元测试与集成测试)](#go-engine-testsh)
  - [`integration-test-go.sh` (Go Engine 19 大 REST 端点全量集成测试)](#integration-test-gosh)
  - [`integration-test-new-modules.sh` (中台三大微服务集成测试)](#integration-test-new-modulessh)
  - [`run_console_e2e_tests.sh` (Console 前后端端到端 E2E 测试)](#run_console_e2e_testssh)
  - [`go-engine-bench.sh` (Go 引擎基准性能压测)](#go-engine-benchsh)
  - [`benchmark_performance.sh` (HTTP 原语基准性能压测)](#benchmark_performancesh)
  - [`benchmark-data-api.sh` (预设数据 API 全链路性能基准压测)](#benchmark-data-apish)
  - [`health_check.sh` (全组件健康状态诊断与探针)](#health_checksh)
  - [`check_metrics_endpoints.sh` (Prometheus 指标端点探针)](#check_metrics_endpointssh)
  - [`lint-source-naming.sh` (源代码命名规约检查)](#lint-source-namingsh)
  - [`start_monitoring.sh` / `stop_monitoring.sh` (启动/停止监控栈)](#start_monitoringsh--stop_monitoringsh)
  - [`verify_console_environment.sh` (开发与编译构建环境巡检)](#verify_console_environmentsh)
  - [`generate_all_test_certs.sh` (一键生成全量 mTLS 测试证书链)](#generate_all_test_certssh)
  - [`clean_privacy_budget_db.sh` (重置清理 SQLite 数据库)](#clean_privacy_budget_dbsh)

---

## 1. 本地原生开发与控制台启动脚本

### `dev-bff-agent.sh` / `dev-bff-agent.ps1`
- **作用说明**: 【推荐主力】一键启动 Go 核心隐私 Agent（REST `:8079`、gRPC `:50051`）、Go 语言 gRPC/HTTPS 代理网关 BFF (`:8081`)，以及基于 Vite 的 React Web 前端开发服务器 (`:5173`，支持毫秒级 HMR 热更新）。同时支持 `--mtls` 参数以 mTLS 双向认证模式启动。
- **参数选项**:
  - `--force`: 端口被占用时自动释放占用进程。
  - `--mtls`: 启用 mTLS 双向认证模式（自动生成/挂载自签名证书）。

标准开发模式（Linux / macOS）：
```bash
bash ./scripts/dev/dev-bff-agent.sh
```

mTLS 安全认证模式（Linux / macOS）：
```bash
bash ./scripts/dev/dev-bff-agent.sh --mtls
```

Windows PowerShell 启动：
```powershell
.\scripts\dev\dev-bff-agent.ps1
```

---

### `dev-app-lz.sh`
- **作用说明**: 【调度之眼 · 全景测试工作台】一键启动专用于 `services/service-hub` 深度测试与观测的 `console/app-lz` 前后端控制台：
  - App-LZ Go BFF 聚合代理后端（REST `:8085`）
  - App-LZ React Web 前端开发服务器（`:5174`，支持毫秒级 HMR 热更新）
  脚本自动打通 4 大核心服务（`service-hub` `:8082`、`datasource-mgr` `:8083`、`audit-log` `:8084`、`privshield-agent` `:8079`），提供 6 阶段流水线动态流转大屏、TS-01~TS-07 一键自动化测试套件、数据源切片探查与 Phase B PostgreSQL 原子租约争抢看板。
- **参数选项**:
  - `--force`: 端口被占用时自动释放占用进程。
  - `--mtls`: 启用 mTLS 双向认证模式。

标准开发模式（BFF `:8085` + Vite `:5174`）：
```bash
bash ./scripts/dev/dev-app-lz.sh --force
```

mTLS 安全模式：
```bash
bash ./scripts/dev/dev-app-lz.sh --mtls --force
```

---

### `go-engine-start.sh`
- **作用说明**: 快速启动 Go 核心隐私计算与动态分类分级引擎 Agent（`privshield-agent`），监听 REST `:8079` 与 gRPC `:50051`。

执行启动命令：
```bash
bash ./scripts/dev/go-engine-start.sh
```

---

### `go-gateway-start.sh`
- **作用说明**: 快速启动 Go 高性能隐私网关反向代理（`privshield-gateway`），监听 REST `:8000` 与 gRPC `:50000`，提供 P2C-EWMA 负载均衡与 BufferPool 零分配代理。

执行启动命令：
```bash
bash ./scripts/dev/go-gateway-start.sh
```

---

### `dev-stop.sh`
- **作用说明**: 一键优雅停止本地由 `dev-bff-agent.sh` 启动的所有进程（Go Agent、Go BFF、Vite 前端），释放相关端口资源。

执行停止命令：
```bash
bash ./scripts/dev/dev-stop.sh
```

---

### `stop-app-lz.sh`
- **作用说明**: 一键优雅停止由 `dev-app-lz.sh` 启动的 App-LZ 控制台进程（Go BFF `:8085` 与 Web 前端 `:5174`），清理 PID 文件并释放端口。

执行停止命令：
```bash
bash ./scripts/dev/stop-app-lz.sh
```

---

## 2. 中台微服务群管理脚本

### `dev-start-new-modules.sh`
- **作用说明**: 启动 PrivShield 的 3 大 Go 语言中台微服务：
  - `service-hub` 数据流通调度中枢 (REST `:8082`，gRPC `:50052`)
  - `datasource-mgr` 数据源资产管理与探查 (REST `:8083`，gRPC `:50053`)
  - `audit-log` 脱敏审计日志与哈希存证 (REST `:8084`，gRPC `:50054`)
  *(注：该脚本要求核心 Agent 已在运行中)*。

执行启动命令：
```bash
bash ./scripts/dev/dev-start-new-modules.sh
```

---

### `dev-stop-new-modules.sh`
- **作用说明**: 停止由 `dev-start-new-modules.sh` 启动的 3 大微服务进程。

执行停止命令：
```bash
bash ./scripts/dev/dev-stop-new-modules.sh
```

---

### `e2e-start-all-services.sh`
- **作用说明**: 【真实全量环境】一键顺序启动 Go Agent + 3 大 Go 中台微服务，构建真实 E2E 运行环境。

执行启动命令：
```bash
bash ./scripts/dev/e2e-start-all-services.sh
```

---

### `e2e-stop-all-services.sh`
- **作用说明**: 停止由 `e2e-start-all-services.sh` 启动的所有服务进程。

执行停止命令：
```bash
bash ./scripts/dev/e2e-stop-all-services.sh
```

---

### `start_all_services.sh`
- **作用说明**: 一键后台启动核心 Go Agent、Go BFF 以及可选的中台微服务群（支持 `--with-services`）。

启动 Agent + BFF + 3 大微服务全量服务群：
```bash
bash ./scripts/dev/start_all_services.sh --with-services
```

---

### `stop_all_services.sh`
- **作用说明**: 停止本地由 `start_all_services.sh` 启动的全量开发服务群，清理 PID 文件并释放所有相关端口。

执行停止命令：
```bash
bash ./scripts/dev/stop_all_services.sh
```

---

## 3. Docker 容器化联调脚本

### `docker-start-bff-agent.sh` / `docker-start-bff-agent.ps1`
- **作用说明**: 【推荐 Docker 开发】通过 Docker Compose 启动控制台三件套核心容器（`privshield` Go 隐私 Agent + `privacy-console-backend-go` Go BFF 网关 + `privacy-console-web` Nginx 前端）。脚本提供**标准非 mTLS**与 **mTLS 双向认证**两个版本，REST 与 gRPC 双协议均获得完整支持。
- **参数选项**:
  - `--mtls`: 以 mTLS 双向认证模式启动（开启 REST HTTPS + gRPC mTLS）。
  - `--no-mtls`: 以标准明文模式启动（默认）。
  - `--no-build`: 跳过构建直接运行已有本地镜像。
  - `--build`: 启动前重新构建本地镜像（默认行为）。
  - `--force`: 端口被占用时自动释放占用进程。

标准非 mTLS 模式启动（HTTP + 明文 gRPC）：
```bash
bash ./scripts/dev/docker-start-bff-agent.sh --force
```

mTLS 双向认证模式启动（HTTPS + mTLS gRPC）：
```bash
bash ./scripts/dev/docker-start-bff-agent.sh --mtls --force
```

Windows PowerShell 标准启动：
```powershell
.\scripts\dev\docker-start-bff-agent.ps1
```

Windows PowerShell mTLS 双向认证启动：
```powershell
.\scripts\dev\docker-start-bff-agent.ps1 -MTLS
```

---

### `docker-start-app-lz.sh`
- **作用说明**: 【调度之眼 · Docker 全栈环境】通过 Docker Compose（`deploy/docker-compose/docker-compose.app-lz.yml`）一键拉起 App-LZ 调度之眼专属容器测试集群：
  - `privshield-app-lz-web`: Nginx 托管的 React 前端控制台大屏（`:5174`）
  - `privshield-app-lz-bff`: Go 语言聚合代理后端（`:8085`，gRPC `:50055`）
  - `privshield-service-hub`: 数据流通调度中枢（`:8082`，gRPC `:50052`）
  - `privshield-datasource-mgr`: 数据源资产探查（`:8083`，gRPC `:50053`）
  - `privshield-audit-log`: 脱敏审计日志存证（`:8084`，gRPC `:50054`）
  - `privshield`: Go 核心隐私与动态分类引擎（`:8079`，gRPC `:50051`）
- **参数选项**:
  - `--build`: 启动前重新构建镜像（默认）。
  - `--no-build`: 使用本地已有镜像快速拉起。
  - `--force`: 自动清理占用端口的非容器进程。

构建并启动 App-LZ 全栈容器测试集群：
```bash
bash ./scripts/dev/docker-start-app-lz.sh --force
```

---

### `docker-stop-app-lz.sh`
- **作用说明**: 一键停止并销毁由 `docker-start-app-lz.sh` 启动的 App-LZ 容器集群及 Docker 网络。

执行停止命令：
```bash
bash ./scripts/dev/docker-stop-app-lz.sh
```

---

### `docker-start-all.sh` / `docker-start-go-all.sh`
- **作用说明**: 通过 Docker Compose 一键启动全栈容器集群（Go Agent + 3 大 Go 中台微服务 + Go BFF + Web 前端）。
- **参数选项**:
  - `--with-llm`: 联动启动本地 vLLM 大语言模型推理容器 (`:8000`)。
  - `--with-postgres`: 启动 Phase B PostgreSQL 多副本 Hub 模式。
  - `--with-monitoring`: 启动 Prometheus + Grafana 监控栈。
  - `--no-build`: 跳过构建直接运行。

标准启动全栈容器：
```bash
bash ./scripts/dev/docker-start-all.sh
```

全量联动启动 (LLM + PostgreSQL + 监控)：
```bash
bash ./scripts/dev/docker-start-all.sh --with-llm --with-postgres --with-monitoring
```

---

### `docker-start-agent.sh` / `docker-start-go-agent.sh`
- **作用说明**: 仅启动 Go 核心 Agent 容器，暴露 REST 端口 `:8079` 与 gRPC 端口 `:50051`。

执行启动命令：
```bash
bash ./scripts/dev/docker-start-agent.sh
```

---

### `docker-stop-agent.sh` / `docker-stop-go-agent.sh`
- **作用说明**: 停止由 `docker-start-agent.sh` 启动的 Go Agent 容器。

执行停止命令：
```bash
bash ./scripts/dev/docker-stop-agent.sh
```

---

### `docker-start-llm.sh` / `docker-start-llm.ps1`
- **作用说明**: 启动专用的 vLLM 本地大模型推理容器 (`:8000`)，需宿主机具备 NVIDIA GPU 与 Container Toolkit。

执行启动命令：
```bash
bash ./scripts/dev/docker-start-llm.sh
```

---

### `docker-stop-llm.sh` / `docker-stop-llm.ps1`
- **作用说明**: 停止由 `docker-start-llm.sh` 启动的 vLLM 容器。

执行停止命令：
```bash
bash ./scripts/dev/docker-stop-llm.sh
```

---

### `docker-stop.sh`
- **作用说明**: 一键停止并清理所有通过 Docker Compose 启动的开发容器及网络（含 llm/monitoring/phase-b 全部 profile）。

执行停止命令：
```bash
bash ./scripts/dev/docker-stop.sh
```

---

### `start-postgres.sh`
- **作用说明**: 独立启动一个 PostgreSQL 16 Docker 容器，供 Phase B LeasedTaskStore 开发调试。支持 `--stop` 停止并移除容器。
- **参数选项**:
  - `--stop`: 停止并移除 PostgreSQL 容器。
- **环境变量**:
  - `PG_PORT`: 宿主机映射端口 (默认: 5432)。
  - `PG_PASSWORD`: 数据库密码 (默认: privshield_dev)。

启动独立 PostgreSQL 容器：
```bash
bash ./scripts/dev/start-postgres.sh
```

停止并移除容器：
```bash
bash ./scripts/dev/start-postgres.sh --stop
```

---

## 4. 自动化测试、基准压测与环境工具

### `go-engine-test.sh`
- **作用说明**: 【核心测试入口】一键按序运行全仓库 Go 模块的单元测试与集成测试（覆盖 `privacy-go-sdk`、`engine-go`、`pkg`、`services`、`console/bff-go`）。

执行全量测试：
```bash
bash ./scripts/dev/go-engine-test.sh
```

---

### `integration-test-go.sh`
- **作用说明**: 【Go 原生引擎集成测试】对 `privshield-agent` 暴露的 19 个 REST 端点（健康检查、掩码脱敏、差分隐私、K-匿名、查询混淆、LDP、医疗流水线、通用 Agent、动态分类及 Prometheus 指标）进行全量自动化端到端测试。

执行集成测试（需 Agent 在 `:8079` 运行）：
```bash
bash ./scripts/dev/integration-test-go.sh
```

---

### `integration-test-new-modules.sh`
- **作用说明**: 对 `service-hub`、`datasource-mgr` 与 `audit-log` 三大微服务执行全流程接口与数据流集成测试。

执行微服务集成测试：
```bash
bash ./scripts/dev/integration-test-new-modules.sh
```

---

### `run_console_e2e_tests.sh`
- **作用说明**: 【全栈 E2E 自动化测试】自动拉起真实的 Go Agent 算力层，按序执行 4 大测试阶段：
  1. `privacy-go-sdk` 隐私原语与 `engine-go` 引擎测试
  2. `console/bff-go` 代理后端与 `pkg` 基础库测试
  3. `services` 微服务群集成测试
  4. `console/web` 前端 TypeScript 与 Vitest 组件自动化测试（79+ 项测试）

执行端到端 E2E 测试：
```bash
bash ./scripts/dev/run_console_e2e_tests.sh
```

---

### `go-engine-bench.sh`
- **作用说明**: 对 `privacy-go-sdk` 与 `engine-go` 中的所有核心隐私计算原语执行高并发基准性能压测（Benchmark），输出 Ops/s、单次耗时 (ns/op) 与内存分配指标。

执行基准性能压测：
```bash
bash ./scripts/dev/go-engine-bench.sh
```

---

### `benchmark_performance.sh`
- **作用说明**: 对运行中的 Go Agent REST API（`/v1/privacy/mask`、`/v1/privacy/dp/laplace`、`/v1/dynclassification/classify` 等）进行基于 HTTP 并发请求的吞吐量 (QPS) 与延迟基准压测。

执行 HTTP 性能压测：
```bash
bash ./scripts/dev/benchmark_performance.sh
```

---

### `benchmark-data-api.sh`
- **作用说明**: 【App-LZ 全链路压测】通过 curl 并发请求直接测量 App-LZ BFF → 微服务群（`datasource-mgr` → `engine` → `audit-log`）全链路延迟与吞吐量，规避浏览器 JS 主线程阻塞对测量精度的影响。输出延迟分位数 (P50/P90/P95/P99)、QPS、服务端 5 阶段耗时拆解（计算 vs 通信）、SLA 判定与延迟分布直方图。
- **参数选项**:
  - `-u, --url <URL>`: BFF 地址 (默认: `http://127.0.0.1:8085`)。
  - `-a, --api-id <ID>`: 数据 API ID，`1`=医保结算 / `2`=康养慢病 (默认: `1`)。
  - `-l, --limit <N>`: 每次请求返回记录数 (默认: `5`)。
  - `-c, --concurrency <N>`: 并发数 (默认: `10`)。
  - `-n, --requests <N>`: 总请求数 (默认: `100`)。
  - `--lean`: 启用 lean 模式（不返回 `raw_records`/`sanitized_data`，载荷缩减 82%）。
  - `--warmup <N>`: 预热请求数 (默认: `5`)。

标准压测（10 并发 × 100 请求）：
```bash
bash ./scripts/dev/benchmark-data-api.sh
```

高并发突发脉冲（50 并发 × 300 请求）：
```bash
bash ./scripts/dev/benchmark-data-api.sh -c 50 -n 300
```

康养场景 + lean 模式：
```bash
bash ./scripts/dev/benchmark-data-api.sh -a 2 --lean
```

自定义 BFF 地址：
```bash
bash ./scripts/dev/benchmark-data-api.sh -u http://192.168.1.100:8085
```

---

### `health_check.sh`
- **作用说明**: 对所有开发环境微服务（Go Agent、Go BFF、三大中台微服务）进行全方位的 Go 编译器就绪状态、源码结构完整性与 HTTP `/health` / `/livez` 健康探针巡检。

执行健康检查巡检：
```bash
bash ./scripts/dev/health_check.sh
```

---

### `check_metrics_endpoints.sh`
- **作用说明**: 检查各服务（Agent `:8079`、BFF `:8081`、Service-Hub `:8082`、Datasource-Mgr `:8083`、Audit-Log `:8084`）的 `/metrics` Prometheus 指标暴露端点连通性。

执行指标端点巡检：
```bash
bash ./scripts/dev/check_metrics_endpoints.sh
```

---

### `lint-source-naming.sh`
- **作用说明**: 依据 Go 规范自动扫描全仓库源文件与测试文件的命名合法性，并验证 Go 测试套件可正常被发现与执行。

执行命名规约检查：
```bash
bash ./scripts/dev/lint-source-naming.sh
```

---

### `start_monitoring.sh` / `stop_monitoring.sh`
- **作用说明**: 启动/停止基于 Docker Compose 的 Prometheus (`:9090`) 与 Grafana (`:3000`) 监控看板。

启动监控大屏：
```bash
bash ./scripts/dev/start_monitoring.sh
```

停止监控大屏：
```bash
bash ./scripts/dev/stop_monitoring.sh
```

---

### `verify_console_environment.sh`
- **作用说明**: 全面巡检开发与构建环境，包含 Go 1.22+ 编译器、`go.work` 多模块工作区、Node.js 18+、pnpm 8+、Web 前端 TypeScript 类型检查（`tsc`）以及全栈 Go 模块构建可行性。

执行环境就绪巡检：
```bash
bash ./scripts/dev/verify_console_environment.sh
```

---

### `generate_all_test_certs.sh`
- **作用说明**: 一键重新生成全套 mTLS 测试证书链（Root CA、Server、Client 证书、私钥与 SPKI 客户端公钥固定文件），自动覆盖 `config/certs`、`console/bff-go/certs` 以及 `services/*/certs`。

执行证书生成命令：
```bash
bash ./scripts/dev/generate_all_test_certs.sh
```

---

### `clean_privacy_budget_db.sh`
- **作用说明**: 重置并清理本地开发阶段生成的 SQLite 数据库（`service-hub.db`、`audit-log.db`、`datasource-mgr.db`）与隐私预算缓存。

执行数据库重置清理：
```bash
bash ./scripts/dev/clean_privacy_budget_db.sh
```
