# 安全策略与合规白皮书 (Security Policy)

**数联天下 · 数盾 (`PrivShield`)** 团队高度重视数据隐私计算、动态分类分级与安全治理中台系统的安全性。本文档明确了系统的支持版本、漏洞报告流程与 SLA 响应时限、漏洞受理范畴、内置安全纵深防御体系以及生产安全配置指南。

---

## 1. 支持版本 (Supported Versions)

项目维护团队为以下版本提供持续的安全更新与漏洞修复：

| 版本系列 | 当前状态 | 安全支持 | 说明 |
|---|---|---|---|
| **1.8.x** | 当前稳定版本 (Release) | ✅ 支持 | 持续迭代与全量安全支持（推荐生产使用） |
| **1.x** | 主版本系列 (Major) | ✅ 支持 | 提供关键安全补丁与高危漏洞修复 |
| **< 1.0.0** | 早期预览版本 (Beta) | ❌ 不支持 | 已停止维护，请升级至最新的 1.8.x 稳定版本 |

---

## 2. 漏洞报告与披露流程 (Reporting a Vulnerability)

如果您在 PrivShield 中发现了潜在的安全漏洞，请通过负责任的私密披露渠道向我们报告。**请勿在公开的 GitHub Issue 或 Discussion 中提交未经修复的安全漏洞细节。**

### 2.1 报告渠道

1. **GitHub 私密安全通告 (推荐)**：
   - 访问仓库漏洞报告入口：**[Security → Advisories → Report a vulnerability](https://github.com/fengzhizi319/PrivShield-go/security/advisories/new)**；
   - 按照模板填写私密漏洞报告表单。
2. **邮件与私密通告**：
   - 将漏洞详细复现步骤与影响分析通过 GitHub Security Advisories 私密渠道发送给仓库维护团队。

### 2.2 漏洞报告包含内容

为了协助安全团队快速复现、定位与定级，请在报告中包含以下信息：
- **受影响组件**：Python 核心算力引擎、Go 企业级中台微服务（`service-hub`、`datasource-mgr`、`audit-log`）、Go BFF 网关或 Web 前端；
- **漏洞类型与描述**：攻击向量分析、利用条件及潜在的安全影响；
- **复现步骤 (PoC)**：最小化复现步骤、curl 命令、数据 Payload 或验证脚本；
- **运行环境**：操作系统、Python/Go 运行时版本、相关配置参数、部署模式（原生 / Docker / K8s）；
- **修复建议**（如有）。

### 2.3 响应时效与 SLA 承诺 (Response Timeline)

- **初次确认接收 (Acknowledgment)**：**48 小时内**；
- **定级与影响评估 (Initial Assessment)**：**7 天内**；
- **补丁修复与公告发布 (Fix & Advisory)**：
  - **严重级别 (Critical)**（如：未授权远程代码执行 RCE、完全鉴权绕过、隐私预算被任意篡改或耗尽）：**7 天内**；
  - **高危级别 (High)**（如：敏感明文数据泄露、差分隐私 Epsilon 噪声失效、审计哈希被篡改）：**14 天内**；
  - **中/低危级别 (Medium / Low)**（如：针对特定接口的资源耗尽 DoS、权限范围越权）：**30 天内**。

---

## 3. 漏洞受理范畴 (Vulnerability Scope)

### 3.1 受理范畴 (In Scope)

- **身份认证与权限控制**：
  - API Key 鉴权机制绕过或伪造；
  - mTLS 客户端证书校验绕过或 CN 身份伪造；
  - 内部服务与外部租户之间的 RBAC Scope 越权提升；
  - 字符串比较中的时序攻击（Timing Attack）与侧信道漏洞。
- **隐私保护原语与数据治理**：
  - 差分隐私（DP/LDP）预算记账竞态条件、绕过或下溢攻击；
  - 差分隐私噪声机制、灵敏度截断或极值保护失效；
  - K-匿名（Mondrian）准标识符泛化区间泄露；
  - 动态脱敏算法绕过或可逆还原漏洞；
  - 查询混淆（QOL）语义分布泄露。
- **审计存证与密码学防篡改**：
  - 8 要素 SHA-256 审计存证哈希碰撞、伪造或动态核验绕过；
  - SQLite 存证数据只增不改（Append-Only）约束被突破或记录被非法修改/删除。
- **微服务编排与流转流水线**：
  - Service Hub 6 阶段流水线安全调度绕过；
  - Datasource Manager CSV 数据源沙箱逃逸、路径穿越或任意文件包含（LFI）；
  - 医疗与单据图片打码模块的文件目录遍历或软链接（Symlink）逃逸。
- **底层架构与执行安全**：
  - 不安全的反序列化（YAML `SafeLoader` 绕过、`pickle` 注入、未经验证的 PyTorch 权重加载）；
  - 接口参数导致的命令注入或代码注入；
  - 应用层拒绝服务（Slowloris 绕过、大包请求 OOM 攻击、无限制的协程/线程耗尽）。

### 3.2 不受理范畴 (Out of Scope)

- 无法对其他管理用户产生危害的 Self-XSS；
- 缺乏实际可利用 PoC 的信息性 HTTP 安全响应头缺失；
- 需要宿主机物理接触或已获得 root/管理员完全提权的前置攻击；
- 虽存在于第三方依赖中但在 PrivShield 链路中完全不可达的代码漏洞；
- 针对项目维护人员的社会工程学攻击或钓鱼攻击。

---

## 4. 内置安全纵深防御体系 (Built-in Defenses)

PrivShield 实现了覆盖 Python 算力引擎与 Go 中台微服务群的纵深防御体系：

### 4.1 传输层安全 (TLS 1.3 与零信任 mTLS)
- **双协议传输加密**：外部 REST (`:8079`) 与高性能 gRPC (`:50051`) 强制支持 TLS 1.3。
- **微服务互通 mTLS 与公钥固定**：中台微服务（`service-hub`、`datasource-mgr`、`audit-log`、`console/bff-go`）基于共享库 [`pkg/tlsutil`](pkg/tlsutil) 实现双向证书认证（mTLS），并内置客户端 SPKI 公钥固定（Public Key Pinning）。
- **CN 证书白名单与动态热重载**：gRPC mTLS 支持客户端证书 CN 白名单校验（`PRIVACY_AUTH_MTLS_WHITELIST_FILE`），支持细粒度 per-CN Scope 控制与零停机热重载。

### 4.2 身份鉴权与防时序攻击
- **常量时间比较**：所有 API Key、Bearer Token 与 HMAC 签名的比对均采用常量时间算法（Python `hmac.compare_digest`、Go `crypto/subtle.ConstantTimeCompare`），彻底消除时序侧信道攻击风险。
- **双层身份隔离**：严格区分内部服务身份（`scopes: ["*"]`）与外部租户身份（最小权限原则，如 `privacy:mask`、`classification:read`）。

### 4.3 全栈多层次防 DDoS 纵深防御
- **慢速连接防护 (Anti-Slowloris)**：服务端强制配置 `ReadHeaderTimeout: 5s`、`ReadTimeout: 30s` 与 `MaxHeaderBytes: 1MB`，拦截挂起攻击。
- **大包攻击切断 (MaxBodySize)**：全微服务配置 32MB/64MB 请求体上限，超限使用 `http.MaxBytesReader` 快速返回 `413 Payload Too Large`。
- **IP 令牌桶与滑动窗口限流**：内置并发安全的 `IPRateLimiter`（自动后台 GC 清理 10 分钟闲置 IP 桶），结合滑动窗口算法实施接口级精细化限速。
- **并发容量熔断 (MaxConcurrent)**：基于信号量硬顶并发请求数，超载快速熔断并返回 `503 Service Unavailable`，保护协程池免于雪崩。

### 4.4 数据源沙箱与安全反序列化
- **路径穿越与 LFI 阻断**：
  - 图像脱敏强制校验 `PRIVACY_IMAGE_ALLOWED_DIRS` 白名单，执行 `Path.resolve()` 规范化并严禁软链接（Symlink）逃逸。
  - 数据源服务强制校验 `.csv` 后缀白名单、使用 `filepath.Base` 剥离路径，硬性限制单次最多加载 50,000 行。
- **安全反序列化规范**：
  - 所有 YAML 配置必须使用 `yaml.safe_load()`；
  - 严禁生产环境使用 `pickle.loads()`；
  - PyTorch 权重加载强制设置 `weights_only=True`。

### 4.5 8 要素不可篡改审计存证
- **密码学哈希签名链**：全链路脱敏操作均记录 8 维度特征并计算 SHA-256 不可篡改签名：
  $$\text{IntegrityHash} = \text{SHA256}(\text{logID} \parallel \text{timestamp} \parallel \text{algorithm} \parallel \text{inputHash} \parallel \text{outputHash} \parallel \text{user} \parallel \text{securityLevel} \parallel \text{paramsJSON})$$
- **只增不改 (Append-Only) 约束**：存储层代码级禁止 UPDATE / DELETE 操作；服务启动自检 `PRAGMA integrity_check` 阻断损坏数据库。

---

## 5. 生产安全配置指南 (Production Configuration)

在生产环境中部署 PrivShield 时，建议显式配置以下安全环境变量：

### 5.1 Python 核心引擎安全配置

```bash
# --- 传输层 TLS / HTTPS / gRPCs ---
PRIVACY_TLS_ENABLED=true
PRIVACY_TLS_CERT_FILE=/etc/privshield/certs/server.crt
PRIVACY_TLS_KEY_FILE=/etc/privshield/certs/server.key
PRIVACY_TLS_CA_FILE=/etc/privshield/certs/ca.crt
PRIVACY_TLS_CLIENT_AUTH=require

# --- 身份鉴权与零信任 mTLS ---
PRIVACY_AUTH_ENABLED=true
PRIVACY_AUTH_INTERNAL_MTLS_ENABLED=true
PRIVACY_AUTH_MTLS_WHITELIST_FILE=/etc/privshield/config/mtls-whitelist.yaml
PRIVACY_AUTH_INTERNAL_KEYS_JSON='{
  "sk-internal-svc": {"name": "service-hub", "scopes": ["*"]}
}'
PRIVACY_AUTH_EXTERNAL_KEYS_JSON='{
  "sk-external-app": {"name": "client-app", "scopes": ["privacy:mask", "classification:read"]}
}'

# --- 速率限制 ---
PRIVACY_RATE_LIMIT_ENABLED=true
PRIVACY_RATE_LIMIT_DEFAULT_RPS=100
PRIVACY_RATE_LIMIT_DEFAULT_BURST=200
PRIVACY_RATE_LIMIT_PER_ENDPOINT_JSON='{
  "/v1/privacy/dp/count": {"rps": 10, "burst": 20},
  "/v1/privacy/mask_record": {"rps": 50, "burst": 100}
}'

# --- 图片与数据源沙箱目录白名单 ---
PRIVACY_IMAGE_ALLOWED_DIRS=/data/medical_images:/tmp/privshield

# --- 隐私预算持久化数据库 ---
PRIVACY_BUDGET_DB=/data/budget.db
```

### 5.2 Go 中台微服务安全配置

```bash
# --- Service Hub 数据调度中枢 (:8082, gRPC :50052) ---
SERVICE_HUB_TLS_ENABLED=true
SERVICE_HUB_TLS_CERT_FILE=/etc/privshield/certs/service-hub.crt
SERVICE_HUB_TLS_KEY_FILE=/etc/privshield/certs/service-hub.key
SERVICE_HUB_TLS_CA_FILE=/etc/privshield/certs/ca.crt
SERVICE_HUB_API_KEY=sk-internal-servicehub-secret

# --- Datasource Manager 模拟数据源 (:8083, gRPC :50053) ---
DATASOURCE_MGR_TLS_ENABLED=true
DATASOURCE_MGR_TLS_CERT_FILE=/etc/privshield/certs/datasource-mgr.crt
DATASOURCE_MGR_TLS_KEY_FILE=/etc/privshield/certs/datasource-mgr.key
DATASOURCE_MGR_TLS_CA_FILE=/etc/privshield/certs/ca.crt
DATASOURCE_MGR_API_KEY=sk-internal-datasourcemgr-secret

# --- Audit Log 脱敏审计与存证 (:8084, gRPC :50054) ---
AUDIT_LOG_TLS_ENABLED=true
AUDIT_LOG_TLS_CERT_FILE=/etc/privshield/certs/audit-log.crt
AUDIT_LOG_TLS_KEY_FILE=/etc/privshield/certs/audit-log.key
AUDIT_LOG_TLS_CA_FILE=/etc/privshield/certs/ca.crt
AUDIT_LOG_API_KEY=sk-internal-auditlog-secret
```

---

## 6. 安全设计与运维文档导航

如需深入了解架构设计、威胁模型、证书运维或安全规范，请参阅：

- 📘 [生产安全规范与设计 (Production Security Design)](docs/production_security/design.md)
- 📋 [技术栈常见漏洞与编码安全规范 (Security Requirements)](docs/production_security/security_requirements.md)
- 🛠️ [生产安全运维与证书配置手册 (Production Security Ops)](docs/production_security/ops.md)
- 🔒 [Service Hub 调度中枢可靠性与安全 (Service Hub Reliability)](services/service-hub/docs/reliability.md)
- 🛡️ [Datasource Manager 数据源安全与可靠性 (Datasource Reliability)](services/datasource-mgr/docs/reliability.md)
- 📜 [Audit Log 脱敏审计存证可靠性与哈希完整性 (Audit Log Reliability)](services/audit-log/docs/reliability.md)
- 🔍 [2026 全项目安全/正确性审计与整改报告 (Audit Report)](docs/audit_reports/2026_full_project_audit_report.md)

