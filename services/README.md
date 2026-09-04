# 数盾生产微服务群 (PrivShield Production Services)

企业级数据流通与安全治理核心微服务群，基于 Go 1.25+ 云原生架构构建，提供高吞吐、低延迟的核心隐私计算、数据动态分类分级、调度流水线中枢与不可篡改存证能力。

---

## 1. 商业化生产微服务拓扑与职责

```text
services/
├── privacy-engine/     # 核心隐私计算与动态分类分级引擎 (Core Agent / Sidecar)
│   │                   # REST :8079 / gRPC :50051 / Gateway REST :8000 / gRPC :50000
│   ├── cmd/            # privshield-agent 与 privshield-gateway 主入口
│   ├── internal/       # 3-Layer 分类分级漏斗、网关反向代理、安全认证等
│   ├── sdk/            # 纯 Go 零依赖数学原语库 (Masking, DP, LDP, K-Ano, Medical, Budget)
│   ├── rules/          # 敏感特征分类分级标准与规则库 (Taxonomies, Domains, Standards)
│   ├── docs/           # 引擎自包含架构与运维设计文档
│   ├── deploy/         # 独立 Dockerfile / Dockerfile.cuda / k8s / compose 编排
│   ├── scripts/        # 模块自治启动、运行、测试与压测脚本
│   └── Makefile        # 模块独立构建与测试 Makefile
│
├── service-hub/        # 数据服务调度中枢 · 唯一编排入口 (Pipeline Orchestrator REST :8082 / gRPC :50052)
│   ├── cmd/server/     # 服务启动入口
│   ├── internal/       # 调度流水线引擎、上游 Agent 客户端、HTTP Handler
│   ├── proto/          # 调度中枢 gRPC 定义
│   ├── docs/           # 模块设计、PRD、接口、安全规范与运维文档
│   ├── deploy/         # 独立 Dockerfile / k8s / compose 编排
│   ├── scripts/        # 模块自治启动、测试与备份脚本
│   └── Makefile        # 模块独立构建与测试 Makefile
│
└── audit-log/          # 脱敏审计与不可篡改存证微服务 (Audit Log REST :8084 / gRPC :50054)
    ├── cmd/server/     # 服务启动入口
    ├── internal/       # SHA-256 / SM3 存证引擎、合规报告生成、查询
    ├── docs/           # 模块设计、PRD、接口与测试文档
    ├── deploy/         # 独立 Dockerfile / k8s / compose 编排
    ├── scripts/        # 模块自治启动与测试脚本
    └── Makefile        # 模块独立构建与测试 Makefile
```

> **注**：模拟数据源服务已统筹归入测试管理生态 `console/mock-datasource`（REST :8083 / gRPC :50053），供开发演练与沙箱测试使用。

---

## 2. 生产微服务详细介绍

### 2.1 核心隐私计算与动态分类分级引擎 (`privacy-engine` REST :8079 / gRPC :50051)
* **核心职责**：提供 44 项隐私保护数学原语与三层动态分类分级漏斗（规则引擎 ➔ ONNX NER ➔ LLM 熔断仲裁）；
* **高性能网关**：内置 P2C-EWMA 负载均衡网关 (`privshield-gateway` REST :8000 / gRPC :50000) 与 BufferPool 零分配反向代理；
* **高安全性**：mTLS CN 白名单 5 秒热重载、常量时间鉴权、32 分片高并发令牌桶限流。

### 2.2 数据服务调度中枢 (`service-hub` REST :8082 / gRPC :50052)
* **核心职责**：实现 6 阶段自动化调度流水线（`Ingest` ➔ `Fetch` ➔ `Classify` ➔ `Desensitize` ➔ `Return` ➔ `Audit`）；
* **与 Agent 联动**：自动请求 PrivShield Agent 进行字段安全级别判定并匹配执行脱敏算子；
* **高可用保障**：内置熔断器、重试队列与背压保护机制；
* **崩溃恢复与自动重试**：启动时自动回收孤立任务（running 标记失败、pending 保留队列），周期性后台重试失败任务（指数退避 + RetryCount）；
* **完整性校验与备份**：启动时 `PRAGMA integrity_check` 阻断损坏数据库，统一备份脚本支持全量/增量/验证模式；
* **HTTP/gRPC 双协议 mTLS**：共享 `pkg/tlsutil` 工具库，TLS 1.3 + 公钥固定；
* 📖 学习与设计文档：[学习指南](service-hub/docs/learning-guide.md) · [设计文档](service-hub/docs/design.md) · [安全设计与等保合规](service-hub/docs/security.md) · [运维说明](service-hub/docs/ops.md)

### 2.3 脱敏审计日志 (`audit-log` REST :8084 / gRPC :50054)
* **核心职责**：记录全链路所有脱敏与隐私计算操作；
* **不可篡改存证**：采用 SHA-256 包含 8 维度字段（`logID`, `timestamp`, `algorithm`, `inputHash`, `outputHash`, `user`, `securityLevel`, `params`）进行链式哈希计算；
* **合规报告**：支持按时间跨度、部门、数据源生成数据安全审计与合规评估报告；
* **完整性校验**：启动时 `PRAGMA integrity_check` + HMAC-SHA256 签名审计日志 + 独立校验脚本；
* 📖 学习与设计文档：[学习指南](audit-log/docs/learning-guide.md) · [设计文档](audit-log/docs/design.md) · [可靠性能力](audit-log/docs/reliability.md)

---

## 3. 运行与集成

各微服务依赖根目录共享库 [pkg/](../pkg/)，在根目录的 [go.work](../go.work) 协同下运行，同时每个微服务均自包含独立的 `Makefile`、`deploy/` 与 `scripts/`，支持完全脱离主目录的独立单体测试与构建。
