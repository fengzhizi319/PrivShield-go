# 脱敏审计日志 — 产品需求文档 (PRD)

## 1. 产品概述

**脱敏审计日志**（Audit Log）是 PrivShield 体系的核心合规存证中枢，记录所有脱敏操作的完整审计轨迹，提供 9 要素国密 SM3 连续哈希链（Hash Chain）、快照样本国密 SM4-GCM 信封加密、单点/全链完整性校验、微批聚合刷盘（3k~5k QPS）以及多维合规报告生成能力。

| 属性 | 值 |
|---|---|
| 模块名称 | audit-log |
| 默认端口 | HTTP: 8084 / gRPC: 50054 |
| 开发语言 | Go 1.24+ (Gin + gRPC) |
| 存储底座 | SQLite WAL (单机高吞吐 + Flusher 微批聚合) / PostgreSQL (Phase B 多副本自适应连接池与分区) |
| 上游依赖 | PrivShield Agent REST/gRPC (:8079 / :50051) |
| 密码标准 | 国密 SM2 (mTLS), SM3 (Hash Chain & 摘要), SM4-GCM (快照信封加密) |
| 业务标准 | 四川省健康医疗大数据应用指南 DB51/T 2989—2023 (L1~L5 五级) |

## 2. 核心需求

### 2.1 审计记录生命周期

```
业务操作发生 → 创建审计记录 → 追溯前序 PrevHash → 计算 9 要素国密 SM3 哈希链 → SM4-GCM 快照样本加密 → 内存环形队列微批聚合刷盘 → 链式防篡改验真 → 定期合规报告
```

### 2.2 审计记录与快照关键要素

每条审计记录必须包含满足法律合规溯源要求的核心要素：

| 字段 | 说明 |
|---|---|
| operation | 操作类型（mask/k_anon/dp/qol/classify） |
| datasource / datasource_id | canonical 数据源标识（如 ds_yibao、ds_kangyang） |
| api_code | 关联 API 编码（如 api1_yibao、api2_kangyang） |
| algorithm | 使用的脱敏或隐私算子算法（如 hmac_sm3_mask、k_anonymity_mondrian、laplace_dp） |
| parameters | 算法执行参数字典 |
| input_rows / output_rows | 输入与输出数据行数 |
| duration_ms | 流水线端到端执行耗时（毫秒） |
| user | 操作主体（用户/调用方 CN 身份/租户标识） |
| status | 操作状态（success/failed） |
| security_level | 安全分级级别（L1-L5，遵循 DB51/T 2989—2023） |
| prev_hash | 前序记录国密 SM3 哈希指针（构成区块链式不可篡改哈希链） |
| integrity_hash | 本条存证 9 要素国密 SM3 连续防篡改完整性哈希 |

### 2.3 连续哈希链与快照信封加密

1. **9 要素国密 SM3 防篡改哈希链**：
   $$\text{BlockData} = \text{prevHash} \parallel \text{logID} \parallel \text{timestamp} \parallel \text{algorithm} \parallel \text{inputHash} \parallel \text{outputHash} \parallel \text{user} \parallel \text{securityLevel} \parallel \text{paramsJSON}$$
   $$\text{IntegrityHash} = \text{SM3}(\text{BlockData})$$
2. **快照样本信封加密 (SM4-GCM)**：
   快照中的 `input_sample` 与 `output_sample` 在落盘前经国密 SM4-GCM 应用层信封加密并带有 `enc:v1:` 前缀，防止日志库拖库泄露明文 PII。
3. **全链连续性验真 (`VerifyChain` / `POST /v1/audit/chain/verify`)**：
   支持指定深度对最近 $N$ 条历史记录进行连续性追溯对账，检测任何物理删行、调序或未授权篡改。

### 2.4 微批聚合刷盘与自适应存储底座

- **微批聚合刷盘 (`pkg/store/flusher`)**：基于内存无锁环形队列（容量 10,000），通过 200 条或 20ms 双触发机制将单条并发事务折叠为批量大事务，单机 SQLite 写入达 **3,000 ~ 5,000 QPS**，且优雅停机保证 100% 刷盘零丢数据。
- **自适应连接池与自动分区 (PostgreSQL Phase B)**：根据 CPU 核心数自动调优连接池，自动按月预建时间范围分区索引。
- **自动探针回退降级**：PG 故障或未配置时自动平滑回退 SQLite WAL，保障业务连续性。

### 2.5 合规报告与统计分析

- 支持按时段生成报告（1h/24h/7d/30d）
- 统计：总操作数、按操作类型分布、各安全等级分布（L1~L5）、成功率、平均耗时
- 合规建议：根据统计指标智能输出改进建议，对标 DB51 医疗标准与 GB/T 39786-2021 密评三级要求。

## 3. 功能需求

### 3.1 API 端点 (REST)

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/health` / `/readyz` | 健康检查与上游探活 |
| GET | `/v1/audit/logs` | 审计日志列表（支持多维度复合过滤与分页） |
| POST | `/v1/audit/logs` | 创建审计记录（自动关联前序哈希并加密样本，异步微批刷盘） |
| GET | `/v1/audit/logs/:id` | 审计记录详情 |
| GET | `/v1/audit/stats` | 审计统计概览（SQL 原生聚合） |
| GET | `/v1/audit/snapshots` | 快照列表（向鉴权调用方透明解密样本） |
| POST | `/v1/audit/snapshots/verify` | 单点验证快照国密 SM3 完整性 |
| POST | `/v1/audit/chain/verify` | 全链路国密 SM3 区块链式防篡改连续哈希链验真 |
| POST | `/v1/audit/report` | 生成合规审计报告 |
| GET | `/metrics` | Prometheus 监控指标收集端点 |

### 3.2 gRPC 服务定义 (`auditlog.proto`)

| RPC 方法 | 作用 |
|---|---|
| `Health` | 探针健康检查 |
| `RecordAudit` | 写入审计记录并原子生成快照与哈希链关联 |
| `GetAuditLog` | 查询单条审计日志 |
| `ListAuditLogs` | 列表查询（支持过滤与分页） |
| `ListSnapshots` | 快照查询（支持透明解密） |
| `VerifyIntegrity` | 单点快照国密 SM3 哈希对账 |
| `VerifyChain` | 全链路国密 SM3 防篡改哈希链对账 |
| `GetAuditStats` | 统计概览 |
| `GenerateReport` | 生成合规审计报告 |

### 3.3 配置项

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `AUDIT_LOG_HOST` | `0.0.0.0` | HTTP 监听地址 |
| `AUDIT_LOG_PORT` | `8084` | HTTP 监听端口 |
| `AUDIT_LOG_GRPC_HOST` | `0.0.0.0` | gRPC 监听地址 |
| `AUDIT_LOG_GRPC_PORT` | `50054` | gRPC 监听端口 |
| `AUDIT_LOG_DB_PATH` | `""` (内存) | SQLite 数据库文件路径 |
| `AUDIT_LOG_PG_DSN` / `PG_DSN` | `""` | Phase B PostgreSQL 存储连接串（多副本模式） |
| `AUDIT_LOG_ENCRYPTION_KEY` | `""` | 样本信封加密国密 SM4-GCM 密钥 |
| `AUDIT_LOG_RETENTION_DAYS` | `90` | 审计日志保留天数 |

## 4. 非功能需求

- **不可篡改**: 审计记录只允许 Append-only 追加，对外不暴露 Update/Delete 接口；结合国密 SM3 连续哈希链实现链式防篡改。
- **隐私保护**: 快照样本实施应用层国密 SM4-GCM 信封加密，杜绝日志库拖库明文泄露。
- **高可用与水平扩展**: 支持 SQLite WAL 单机轻量微批模式与 PostgreSQL Phase B 多副本并发模式，具备自动连通性探针回退。
- **高性能**: 内存微批聚合刷盘下单机 SQLite 写入达 3,000 ~ 5,000 QPS，PostgreSQL 批量写入吞吐 > 10,000 logs/s。
- **合规性**: 全面满足《数据安全法》、《个人信息保护法》(PIPL)、GB/T 39786-2021 密评三级与四川 DB51 医疗标准。
