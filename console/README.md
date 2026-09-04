# 数盾控制台与测试生态 (PrivShield Consoles & Testing Ecosystem)

数盾统一测试与运维控制台生态，包含两大独立业务控制台与模拟数据源沙箱，实现对商业化生产服务（`privacy-engine`、`service-hub`、`audit-log`）的端到端严苛测试与可视化观测。

---

## 1. 架构与目录结构

按照平台解耦与测试独立性原则，控制台严格区分职责，**两个控制台的后端保持独立，绝不合并**：

```text
console/
├── engine-console/       # Privacy Engine 专属管理控制台 (专测 services/privacy-engine)
│   ├── bff-go/           # Go gRPC/HTTPS API 代理网关 / BFF (:8081)
│   ├── web/              # React + TypeScript + Vite 前端控制台 (:5173)
│   └── docs/ deploy/ scripts/ Makefile # 自治交付资产
│
├── app-lz/               # 数联调度之眼业务模拟器 (模拟外部调用方，专测 services/service-hub 调度编排)
│   ├── bff-go/           # 业务专有 BFF (:8085，所有数据请求统一走 service-hub)
│   ├── web/              # 业务流水线控制台前端 (:5174)
│   └── docs/ deploy/ scripts/ Makefile # 自治交付资产
│
├── mock-datasource/      # 模拟多源异构数据源服务 (测试沙箱 REST :8083 / gRPC :50053)
│   ├── cmd/server/       # 服务启动入口
│   ├── internal/         # 医保、康养等仿真数据源
│   └── docs/ deploy/ scripts/ Makefile # 自治交付资产
│
├── docs/                 # 控制台相关技术文档
└── README.md             # 控制台生态总览（本文档）
```

---

## 2. 核心架构原则

1. **测试后端隔离**：
   - `engine-console` 专为核心引擎 `privacy-engine` 设计，直连引擎的 REST/gRPC 接口，测试 44 项脱敏原语与三层动态分类分级漏斗。
   - `app-lz` 模拟外部数据申请业务系统，专为调度中枢 `service-hub` 设计，测试全链路数据申请流水线，除 `service-hub` 外**无法直连**任何内部生产服务。
2. **双层交付资产自治**：
   - `engine-console`、`app-lz` 与 `mock-datasource` 均拥有自包含的 `docs/`、`deploy/`、`scripts/` 与 `Makefile`，支持完全独立的单体开发与部署。

---

## 3. 快速启动指南

### 3.1 启动 Engine Console 开发控制台 (专测 privacy-engine)

```bash
# 一键启动 privacy-engine + engine-console BFF + Vite 前端 (http://localhost:5173)
bash ./scripts/dev/dev-engine-console.sh
# 或
bash ./scripts/dev/dev-bff-agent.sh
```

### 3.2 启动 App-LZ 调度之眼控制台 (专测 service-hub 调度全链路)

```bash
# 一键启动 4 大微服务 + app-lz BFF + Vite 前端 (http://localhost:5174)
bash ./scripts/dev/dev-app-lz.sh --force
# 开启 mTLS 模式
bash ./scripts/dev/dev-app-lz.sh --mtls --force
# 开启 TLCP 国密双证书模式
bash ./scripts/dev/dev-app-lz.sh --tlcp --force
```

### 3.3 单模块自治启动

```bash
# 在各模块目录下直接运行 Makefile
cd console/engine-console && make dev
cd console/app-lz && make dev
cd console/mock-datasource && make dev
```
