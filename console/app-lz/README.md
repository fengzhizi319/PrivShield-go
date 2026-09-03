# 调度之眼 (Console App-LZ) — 调度中枢全景测试与治理控制台

> **数联天下 · 数盾 (`PrivShield`)** 数据服务调度中枢 (`services/service-hub`) 全景测试、观测与微服务治理前端控制台。

---

## 1. 项目简介

`console/app-lz`（调度之眼）模拟外部业务系统应用程序，其 BFF 后端 (`console/app-lz/bff-go`) 运行于受保护服务网格边界之外。

> **核心架构原则（唯一调度编排中枢）**：  
> `app-lz BFF` 作为模拟的外部业务程序，**除了访问 `service-hub` (:8082)，并没有直接访问其他服务（`datasource-mgr` / `engine-go` / `audit-log`）的权限**。  
> 所有 `app-lz` 发起的业务请求（集群拓扑、数据源目录、数据采样切片、任务派发、全链路脱敏、审计查询与 Merkle 验真）**全部由 `service-hub` 统一调度与编排**。`service-hub` 为唯一的编排调度入口。

- **`services/service-hub`** (:8082 / :50052)：数据流水线调度中枢 · 唯一外部编排入口
- **`services/datasource-mgr`** (:8083 / :50053)：模拟数据源管理（仅由 `service-hub` 内部调用）
- **`services/audit-log`** (:8084 / :50054)：不可篡改 SHA-256 / Merkle 树审计存证（仅由 `service-hub` 内部调用）
- **`engine` Agent** (:8079 / :50051)：动态分类分级与隐私计算引擎（仅由 `service-hub` 内部调用）

前端设计风格与 `console/web` 保持高度一致（React 18 + TypeScript + Vite + Tailwind CSS + Lucide Icons），提供 **7 大核心工作台**。

---

## 2. 7 大核心工作台

1. **集群拓扑与健康矩阵 (Topology & Mesh Health)**：4 服务实时 RTT、探针与连通性自检。
2. **6 阶段流水线动态大屏 (6-Stage Pipeline Visualizer)**：`Ingest` ➔ `Fetch` ➔ `Classify` ➔ `Desensitize` ➔ `Return` ➔ `Audit` 流转动效与数据脱敏前后对比。
3. **任务生命周期与租约看板 (Task Lifecycle & Lease Inspector)**：任务检索、阶段耗时时间线，以及 Phase B PostgreSQL 原子租约 (`FOR UPDATE SKIP LOCKED`) 争抢监控。
4. **一键全场景自动化测试执行器 (One-Click E2E Test Suite Runner)**：内置 3 大自动化测试套件（TS-01~TS-03），图形化执行与实时断言报告输出。
5. **数据源资产探查器 (Datasource Explorer)**：医保/康养数据源在线浏览与切片采样。
6. **不可篡改审计验真 (Audit Log & Merkle Verifier)**：存证流水查看与 Merkle 树防篡改在线验真。
7. **性能监控与耗时直方图 (Metrics & Performance Analyzer)**：实时 QPS、6 阶段耗时瀑布图与 P50/P90/P95/P99 延迟分位数分析。

---

## 3. 架构与规范文档

- [系统架构与全景设计文档 (`docs/design.md`)](docs/design.md)
- [API 接口与数据契约规范 (`docs/api.md`)](docs/api.md)
- [测试数据来源与生命周期 (`docs/data_lifecycle.md`)](docs/data_lifecycle.md)
- [前端功能 ↔ BFF 接口 ↔ 上游服务映射 (`docs/frontend_backend_mapping.md`)](docs/frontend_backend_mapping.md)

---

## 4. 快速开始

### 开发模式（BFF + Vite HMR）

```bash
# 1. 先启动 4 个上游微服务（参考 scripts/dev/e2e-start-all-services.sh）
bash ./scripts/dev/e2e-start-all-services.sh

# 2. 启动 App-LZ 控制台（BFF :8085 + Vite :5174）
bash ./scripts/dev/dev-app-lz.sh

# 3. 访问控制台
# 前端: http://localhost:5174
# BFF:  http://localhost:8085
```

### 生产模式（BFF + 静态托管）

```bash
# 构建前端
cd console/app-lz/web && pnpm install && pnpm build

# 启动生产模式
cd ../../.. && bash ./scripts/prod/prod-app-lz.sh
```

### Docker 全栈启动

```bash
# 启动完整栈（Agent + BFF + Web + 3 Go 服务）
bash ./scripts/dev/docker-start-all.sh

# 或仅启动 App-LZ 控制台
bash ./scripts/dev/docker-start-app-lz.sh
```

### 验证

```bash
# 检查拓扑探针
curl -s http://localhost:8085/api/lz/topology | jq .status

# 执行全量 E2E 测试套件
curl -s -X POST http://localhost:8085/api/lz/suites/run \
  -H "Content-Type: application/json" \
  -d '{"suite_ids": []}' | jq .
```

---

## 5. 端口一览

| 服务 | 端口 | 说明 |
|---|---|---|
| App-LZ 前端 | `:5174` | Vite 开发服务器（生产模式由 BFF 静态托管） |
| App-LZ BFF | `:8085` | Go Gin 聚合代理 |
| engine Agent | `:8079` / `:50051` | 隐私与分类引擎 |
| service-hub | `:8082` / `:50052` | 调度中枢 |
| datasource-mgr | `:8083` / `:50053` | 数据源管理 |
| audit-log | `:8084` / `:50054` | 审计存证 |
