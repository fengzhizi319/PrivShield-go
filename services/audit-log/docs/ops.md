# 脱敏审计日志与存证 (Audit Log) — 运维手册

> 本文档提供 **数联天下 · 数盾 (`PrivShield`)** 脱敏审计日志模块（`services/audit-log`）的部署、配置、国密 mTLS 证书配置、微批刷盘调优、监控与故障排查指南。

---

## 1. 运行与启动

### 1.1 开发模式

```bash
cd services/audit-log
bash run.sh
```

默认同时启动：
- **HTTP REST**：`127.0.0.1:8084`
- **gRPC (insecure)**：`127.0.0.1:50054`

### 1.2 生产模式（启用国密 mTLS 与 CN 白名单）

```bash
# 编译二进制产物
cd services/audit-log
make build

# 启动服务（主机乙 · 独立安全审计节点 · ECS）
AUDIT_LOG_HOST=0.0.0.0 \
AUDIT_LOG_PORT=8084 \
AUDIT_LOG_GRPC_HOST=0.0.0.0 \
AUDIT_LOG_GRPC_PORT=50054 \
AUDIT_LOG_TLS_ENABLED=true \
AUDIT_LOG_TLS_CERT_FILE=/etc/privshield/certs/server.crt \
AUDIT_LOG_TLS_KEY_FILE=/etc/privshield/certs/server.key \
AUDIT_LOG_TLS_CA_FILE=/etc/privshield/certs/ca.crt \
AUDIT_LOG_TLS_CLIENT_AUTH=require \
AUDIT_LOG_ENCRYPTION_KEY=0123456789abcdef0123456789abcdef \
AUDIT_LOG_DB_PATH=/data/audit/audit.db \
./bin/audit-log
```

### 1.3 Docker / 容器部署

```bash
docker build -t privshield-audit-log -f services/audit-log/Dockerfile .
docker run -d \
  --name audit-log \
  -p 8084:8084 \
  -p 50054:50054 \
  -v /data/audit:/app/data \
  -v /etc/privshield/certs:/certs:ro \
  -e AUDIT_LOG_HOST=0.0.0.0 \
  -e AUDIT_LOG_PORT=8084 \
  -e AUDIT_LOG_GRPC_HOST=0.0.0.0 \
  -e AUDIT_LOG_GRPC_PORT=50054 \
  -e AUDIT_LOG_TLS_ENABLED=true \
  -e AUDIT_LOG_TLS_CERT_FILE=/certs/server.crt \
  -e AUDIT_LOG_TLS_KEY_FILE=/certs/server.key \
  -e AUDIT_LOG_TLS_CA_FILE=/certs/ca.crt \
  -e AUDIT_LOG_TLS_CLIENT_AUTH=require \
  -e AUDIT_LOG_DB_PATH=/app/data/audit-log.db \
  -e AUDIT_LOG_ENCRYPTION_KEY=0123456789abcdef0123456789abcdef \
  -e PRIVACY_AGENT_REST_HOST=privshield-agent \
  -e PRIVACY_REST_PORT=8079 \
  privshield-audit-log
```

---

## 2. 环境变量速查表

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `AUDIT_LOG_HOST` | `127.0.0.1` | HTTP REST 服务监听主机 |
| `AUDIT_LOG_PORT` | `8084` | HTTP REST 服务监听端口 |
| `AUDIT_LOG_GRPC_HOST` | `127.0.0.1` | gRPC 服务监听主机 |
| `AUDIT_LOG_GRPC_PORT` | `50054` | gRPC 服务监听端口 |
| `AUDIT_LOG_TLS_ENABLED` | `false` | 是否在 gRPC 服务上启用 TLS/mTLS |
| `AUDIT_LOG_TLS_CERT_FILE` | (空) | gRPC 服务端 X.509 证书 PEM 路径 |
| `AUDIT_LOG_TLS_KEY_FILE` | (空) | gRPC 服务端私钥 PEM 路径 |
| `AUDIT_LOG_TLS_CA_FILE` | (空) | 客户端证书校验 CA 证书 PEM 路径 |
| `AUDIT_LOG_TLS_CLIENT_AUTH` | (空) | 客户端认证模式: `require` \| `verify` \| `request` |
| `AUDIT_LOG_TLS_ALLOWED_CNS` | (空) | 允许调用的客户端证书 CN 白名单（逗号分隔） |
| `PRIVACY_AGENT_REST_HOST` | `127.0.0.1` | 上游 Agent REST 主机 |
| `PRIVACY_REST_PORT` | `8079` | 上游 Agent REST 端口 |
| `PRIVACY_AGENT_API_KEY` | (空) | 上游 Agent 认证密钥 |
| `AUDIT_LOG_API_KEY` | (空) | 本模块入站 API Key（空表示免密） |
| `AUDIT_LOG_CORS_ORIGINS` | (空) | 允许的 CORS 跨域源（逗号分隔） |
| `AUDIT_LOG_DB_PATH` | (空) | SQLite 数据库路径（空表示纯内存模式） |
| `AUDIT_LOG_PG_DSN` / `PG_DSN` | (空) | PostgreSQL 存证库 DSN（Phase B 架构，启用多副本水平扩展；带 3s 探针自动降级） |
| `AUDIT_LOG_ENCRYPTION_KEY` | (空) | 快照敏感样本国密 SM4-GCM 信封加密主密钥（16/24/32 字节 Hex） |
| `AUDIT_LOG_RETENTION_DAYS` | `90` | 审计日志本地保留天数（0 表示禁用自动清理） |
| `AUDIT_LOG_LOG_FORMAT` | `json` | 日志格式: `json` \| `text` |
| `AUDIT_LOG_LOG_LEVEL` | `info` | 日志级别: `debug` \| `info` \| `warn` \| `error` |

---

## 3. 健康检查与验证

### 3.1 HTTP 健康检查
```bash
curl -s http://127.0.0.1:8084/health | jq .
curl -s http://127.0.0.1:8084/readyz | jq .
```

### 3.2 国密 SM3 哈希链与快照验真
```bash
# 1. 验证最近存证的国密 SM3 连续哈希链 (Hash Chain)
curl -s -X POST http://127.0.0.1:8084/api/audit/chain/verify \
  -H "Content-Type: application/json" \
  -d '{"limit": 500}' | jq .

# 2. 验证指定快照的国密 SM3 完整性
curl -s -X POST http://127.0.0.1:8084/api/audit/snapshots/verify \
  -H "Content-Type: application/json" \
  -d '{"snapshot_id": "snap-xxx"}' | jq .
```

### 3.3 gRPC 健康检查与探活
使用 `grpcurl` 工具：
```bash
# 明文连接模式
grpcurl -plaintext 127.0.0.1:50054 auditlog.AuditLogService/Health

# mTLS 认证模式
grpcurl -cacert /certs/ca.crt -cert /certs/client.crt -key /certs/client.key \
  127.0.0.1:50054 auditlog.AuditLogService/Health
```

### 3.4 Prometheus 监控指标抓取
```bash
curl -s http://127.0.0.1:8084/metrics
```

---

## 4. 故障排查手册

| 故障现象 | 潜在原因 | 排查与修复方案 |
|---|---|---|
| **Agent unreachable** | Agent 进程未就绪或端口错误 | 检查 `curl http://127.0.0.1:8079/health`；确认 `PRIVACY_AGENT_REST_HOST` 配置 |
| **gRPC Handshake Failed** | 客户端证书不匹配或 CA 未信任 | 检查 `AUDIT_LOG_TLS_CA_FILE` 是否包含签名 CA；验证证书有效期 |
| **client certificate CN not allowed** | 客户端 CN 不在白名单中 | 检查客户端证书 Subject CN 是否在 `AUDIT_LOG_TLS_ALLOWED_CNS` 中 |
| **PostgreSQL probe timeout** | PG 连接超时或网络不可达 | 系统已自动平滑降级至 SQLite WAL 模式；检查 PG 服务状态与防火墙 |
| **Integrity Violation** | 审计数据遭受篡改或底层存储损坏 | 调用 `/api/audit/snapshots/verify` 定位异常 snapshot_id，排查记录篡改 |
| **Hash Chain Broken** | 存在物理删行、调序或未授权中间修改 | 调用 `/api/audit/chain/verify` 获取 `broken_at_id`，定位断链根源 |
| **Decryption Failed** | 信封加密密钥与原写入密钥不一致 | 检查 `AUDIT_LOG_ENCRYPTION_KEY` 配置；未配置密钥时系统降级为明文读取 |
