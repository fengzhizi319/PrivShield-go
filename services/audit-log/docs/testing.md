# 脱敏审计日志与存证 (Audit Log) — 测试规范与测试全景

> 本文档详细说明 **数联天下 · 数盾 (`PrivShield`)** 脱敏审计日志模块（`services/audit-log`）的测试架构、用例覆盖与执行方式。

---

## 1. 测试全景与模块覆盖

`audit-log` 实现了全方位的自动化单元测试与集成测试，覆盖率达 **85%+**，全量测试 **100% PASS**：

| 测试包 | 测试文件 | 覆盖内容与核心断言 |
|---|---|---|
| `internal/grpcserver` | `server_test.go` | **全部 9 个 gRPC 方法**（Health/RecordAudit/GetAuditLog/ListAuditLogs/GetAuditStats/ListSnapshots/VerifyIntegrity/VerifyChain/GenerateReport）、原子快照创建与样本信封解密、mTLS 凭证构造、CA 链校验与 CN 白名单校验 |
| `internal/handlers` | `handlers_test.go` | **HTTP REST Handler 层**（Health、创建审计日志、日志检索过滤、统计概览、快照列表、快照样本 SM4-GCM 信封加密/解密、国密 SM3 9 要素完整性校验、全链路连续哈希链验真 `/v1/audit/chain/verify`、合规报告生成、参数超大拦截防 DoS） |
| `pkg/store/flusher` | `flusher_test.go` | **内存微批异步聚合刷盘器**（定量 200 条批量刷盘、定时 20ms 窗口超时刷盘、20 协程高并发无锁入队一致性与优雅停机同步清空） |
| `internal/config` | `config_test.go` | 默认配置、自定义环境变量加载（`PGDSN`、`EncryptionKey`、`ArchiveDir`）、`Address()`、`GRPCAddress()`、`AgentBaseURLs()` 多节点轮询与 mTLS 配置解析 |
| `internal/models` | `models_test.go` | 审计日志、快照存证、合规报告等核心数据结构的 JSON 序列化与反序列化双向无损性验证 |
| `internal/agent` | `client_test.go` | 上游 Agent HTTP 客户端（Health 探活） |
| `pkg/crypto` | `envelope_test.go` | 国密 SM4-GCM 信封加密、解密、防篡改、空密钥降级与明文向后兼容性验证 |
| `pkg/store/postgres` | `postgres_test.go` (Phase B) | PostgreSQL 多副本自适应连接池初始化、按月动态分区索引预建、批量高效入库 `SaveLogsBatch`、前序哈希追溯 `GetLatestLog`、全链验真 `VerifyChain` |

---

## 2. 运行测试命令

```bash
# 1. 运行 audit-log 全部单元测试
go test -v ./services/audit-log/...

# 2. 运行微批刷盘器单元测试
go test -v ./pkg/store/flusher/...

# 3. 运行带覆盖率统计的测试
go test -coverprofile=coverage.out ./services/audit-log/...
go tool cover -func=coverage.out

# 4. 运行根工作区全量 Go 测试
go test ./pkg/... ./services/audit-log/... ./services/service-hub/... ./services/datasource-mgr/...
```

---

## 3. 核心测试用例清单

### 3.1 gRPC 与 mTLS 测试 (`internal/grpcserver/server_test.go`)
- `TestGRPCHealth`：验证 gRPC 探活接口及上游 Agent 状态解析；
- `TestGRPCHealthAgentUnreachable`：验证 Agent 宕机时的容错降级；
- `TestGRPCAuditLogOperations`：全流程存证闭环（RecordAudit 自动快照 -> GetAuditLog -> ListAuditLogs -> GetAuditStats -> GenerateReport -> ListSnapshots -> VerifyIntegrity -> VerifyChain）；
- `TestGRPCValidationErrors`：空操作、空 ID、不存在日志与空快照 ID 的 ArgumentError 拦截；
- `TestBuildServerCredentials`：覆盖 7 类 TLS/mTLS 场景（未启用、缺少证书、单向 TLS、mTLS 强制校验、CN 白名单校验、CA 缺失失败、非法 client auth 模式）。

### 3.2 HTTP REST 测试 (`internal/handlers/handlers_test.go`)
- `TestHealth`：GET `/health` 与 `/readyz` 探活及响应头；
- `TestCreateLog` / `TestGetLog` / `TestListLogsWithFilter`；
- `TestVerifyIntegrity`：验证 9 要素国密 SM3 存证完整性；
- `TestVerifyChainEndpoint`：验证 POST `/v1/audit/chain/verify` 对历史区块链式哈希链的连续性对账；
- `TestEnvelopeEncryptionOfSnapshots`：验证快照落盘时的 `enc:v1:` 国密 SM4-GCM 加密以及读取时的透明解密；
- `TestCreateLogParametersTooLarge`：超大参数攻击拦截（防内存耗尽 DoS）；
- `TestComputeIntegrityHash`：验证国密 SM3 哈希确定性、哈希链连续性与雪崩效应。

### 3.3 微批聚合刷盘测试 (`pkg/store/flusher/flusher_test.go`)
- `TestBufferedAuditStore_BatchFlushByCount`：验证达到单批数量阈值时自动触发批量事务刷盘，以及关闭时的残余数据清空；
- `TestBufferedAuditStore_BatchFlushByTimer`：验证低流量低并发场景下定时时间窗口到达自动刷盘；
- `TestBufferedAuditStore_ConcurrentWrites`：验证 20 个高并发协程同时写入 1,000 条日志与快照时的数据无丢、无乱序与原子落盘。
