# 脱敏审计日志与存证 (Audit Log)

`services/audit-log` 是 PrivShield 平台的企业级脱敏合规审计与不可篡改存证微服务。模块提供 **REST (HTTP/JSON :8084) + gRPC (mTLS :50054)** 双协议接入，支持全量隐私原语与分类治理事件存证、8 要素增强 SHA-256 防篡改校验、快照对账与多维度合规报告生成。

---

## 核心功能特性

- **双协议接入**：提供标准 REST API（供前端控制台/BFF 使用）与高性能 gRPC 接口（端口 `:50054`，供调度流水线高并发写入）；
- **零信任 mTLS 与公钥固定**：gRPC 通道支持 TLS 1.3 双向证书认证与客户端公钥固定（Public Key Pinning）；
- **8 要素增强 SHA-256 存证**：将日志 ID、高精时间戳、操作类型、算法、输入哈希、输出哈希、操作人、安全等级与配置 JSON 全面纳入哈希指纹；
- **防篡改在线核验**：提供实时快照校验端点，检测任何底层数据库篡改与时序攻击；
- **SQL 级高性能统计与合规报告**：基于 SQLite 聚合引擎提供毫秒级多维统计指标与权威合规治理建议；
- **高可用与生产加固**：Slowloris 防护、32 MiB MaxBodySize 限制、Prometheus `/metrics` 监控与 SQLite WAL 模式持久化；
- **完整性校验**：启动时 `PRAGMA integrity_check` 阻断损坏数据库，统一备份脚本支持全量/增量备份；
- **独立校验脚本**：`scripts/prod/verify-audit.sh`（封装 `scripts/prod/verify_audit.go`）独立验证审计数据完整性，支持 CI/CD 集成；
- 📖 **可靠性能力详解**：[docs/reliability.md](docs/reliability.md)

> 📖 **深度学习指南**：完整架构解析、8 要素 SHA-256 存证原理与源码导读见 [docs/learning-guide.md](docs/learning-guide.md)。

---

## 快速开始

### 本地启动

```bash
cd services/audit-log
bash run.sh
```

默认监听：
- **HTTP REST**：`http://127.0.0.1:8084`
- **gRPC (insecure)**：`127.0.0.1:50054`

### 生产启动（启用 mTLS 与公钥固定）

```bash
AUDIT_LOG_HOST=0.0.0.0 \
AUDIT_LOG_PORT=8084 \
AUDIT_LOG_GRPC_HOST=0.0.0.0 \
AUDIT_LOG_GRPC_PORT=50054 \
AUDIT_LOG_TLS_ENABLED=true \
AUDIT_LOG_TLS_CERT_FILE=/certs/server.crt \
AUDIT_LOG_TLS_KEY_FILE=/certs/server.key \
AUDIT_LOG_TLS_CA_FILE=/certs/ca.crt \
AUDIT_LOG_TLS_CLIENT_AUTH=require \
AUDIT_LOG_TLS_PINNED_PUBKEY_FILE=/certs/client_pub.pem \
./bin/audit-log
```

---

## 运行测试

```bash
# 运行全部单元测试
go test -v ./services/audit-log/...

# 运行全仓 Go 测试
make test-go
```

---

## 容器化与独立 Kubernetes 部署

```bash
# 1. 独立构建 Docker 镜像（构建上下文需在仓库根目录以包含共享 pkg/）
docker build -f services/audit-log/Dockerfile -t audit-log:latest .

# 2. 独立部署到 Kubernetes（使用单服务自包含清单）
kubectl apply -k services/audit-log/deploy/k8s/
```

---

## 详细文档目录

- 📘 [详细设计文档 (docs/design.md)](docs/design.md)
- 🔌 [API 接口规范与 Proto 定义 (docs/api.md)](docs/api.md)
- 🛠️ [运维与部署手册 (docs/ops.md)](docs/ops.md)
- 🧪 [测试规范与全景指南 (docs/testing.md)](docs/testing.md)
- 📋 [产品需求文档 (docs/prd.md)](docs/prd.md)
