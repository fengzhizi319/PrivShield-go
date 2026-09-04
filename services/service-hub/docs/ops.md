# 数据服务调度中枢 (Service Hub) — 运维手册

> 本文档提供 **数联天下 · 数盾 (`PrivShield`)** 数据服务调度中枢模块（`services/service-hub`）的部署、配置、mTLS 证书配置、数据源联动监控与故障排查指南。

---

## 1. 运行与启动

### 1.1 本地开发模式

```bash
# 方式 1: 直接在模块目录下运行
cd services/service-hub
go run ./cmd/server

# 方式 2: 使用根目录自动化脚本启动全部微服务
bash ./scripts/dev/e2e-start-all-services.sh
```

默认同时启动：
- **HTTP REST**：`http://127.0.0.1:8082`
- **gRPC (insecure)**：`127.0.0.1:50052`
- 上游 Agent 默认连接：`http://127.0.0.1:8079`
- 模拟数据源默认连接：`http://127.0.0.1:8083`（gRPC `:50053`）

### 1.2 生产模式（启用 mTLS 与公钥固定）

```bash
# 编译二进制产物
cd services/service-hub
make build

# 启动服务（主机甲 · 业务网关算力节点 · ECS）
SERVICE_HUB_HOST=0.0.0.0 \
SERVICE_HUB_PORT=8082 \
SERVICE_HUB_GRPC_HOST=0.0.0.0 \
SERVICE_HUB_GRPC_PORT=50052 \
SERVICE_HUB_TLS_ENABLED=true \
SERVICE_HUB_TLS_CERT_FILE=/etc/privshield/certs/server.crt \
SERVICE_HUB_TLS_KEY_FILE=/etc/privshield/certs/server.key \
SERVICE_HUB_TLS_CA_FILE=/etc/privshield/certs/ca.crt \
SERVICE_HUB_TLS_CLIENT_AUTH=require \
PRIVACY_AUTH_MTLS_WHITELIST_FILE=/etc/privshield/mtls-whitelist.yaml \
SERVICE_HUB_AUDIT_LOG_URLS=http://audit-log:8084 \
SERVICE_HUB_STRICT_STORAGE=true \
DATASOURCE_MGR_HOST=127.0.0.1 \
DATASOURCE_MGR_PORT=8083 \
DATASOURCE_MGR_GRPC_HOST=127.0.0.1 \
DATASOURCE_MGR_GRPC_PORT=50053 \
PRIVACY_AGENT_REST_HOST=127.0.0.1 \
PRIVACY_REST_PORT=8079 \
SERVICE_HUB_DB_PATH=/var/lib/privshield/service-hub.db \
SERVICE_HUB_RETENTION_DAYS=30 \
./bin/server
```

---

## 2. 环境变量速查表

| 环境变量 | 默认值 | 类型 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_HOST` | `127.0.0.1` | string | HTTP REST 服务监听主机（生产设为 `0.0.0.0`） |
| `SERVICE_HUB_PORT` | `8082` | int | HTTP REST 服务监听端口 |
| `SERVICE_HUB_GRPC_HOST` | `127.0.0.1` | string | gRPC 服务监听主机 |
| `SERVICE_HUB_GRPC_PORT` | `50052` | int | gRPC 服务监听端口 |
| `PRIVACY_AGENT_REST_HOST` | `127.0.0.1` | string | 上游 Agent REST 主机 |
| `PRIVACY_REST_PORT` | `8079` | int | 上游 Agent REST 端口 |
| `PRIVACY_AGENT_API_KEY` | `""` | string | 请求上游 Agent 的 API Key 凭证 |
| `PRIVACY_AGENT_URLS` | `""` | string | 多 Agent 负载均衡/故障转移地址（逗号分隔） |
| `SERVICE_HUB_MAX_QUEUE` | `1000` | int | 调度引擎最大排队等待任务数 |
| `SERVICE_HUB_SCHEDULE_TIMEOUT` | `30` | int | 任务单步调度与执行超时（秒） |
| `DATASOURCE_MGR_HOST` | `127.0.0.1` | string | 模拟数据源 HTTP 主机 |
| `DATASOURCE_MGR_PORT` | `8083` | int | 模拟数据源 HTTP 端口 |
| `DATASOURCE_MGR_GRPC_HOST` | `127.0.0.1` | string | 模拟数据源 gRPC 主机 |
| `DATASOURCE_MGR_GRPC_PORT` | `50053` | int | 模拟数据源 gRPC 端口 |
| `SERVICE_HUB_TLS_ENABLED` | `false` | bool | 是否启用 HTTP/gRPC TLS / 国密 SM2 双向认证 |
| `SERVICE_HUB_TLS_CERT_FILE` | `""` | string | 服务端 X.509 证书 PEM 文件路径 |
| `SERVICE_HUB_TLS_KEY_FILE` | `""` | string | 服务端私钥 PEM 文件路径 |
| `SERVICE_HUB_TLS_CA_FILE` | `""` | string | 客户端证书校验根 CA 证书 PEM 路径 |
| `SERVICE_HUB_TLS_CLIENT_AUTH` | `""` | string | 客户端双向认证模式: `require` \| `verify` \| `request` |
| `PRIVACY_AUTH_MTLS_WHITELIST_FILE` | `""` | string | gRPC 客户端证书 CN 白名单 YAML 文件路径（全局共享，支持热重载） |
| `SERVICE_HUB_API_KEY` | `""` | string | 本模块入站 API Key（空表示免密） |
| `SERVICE_HUB_CORS_ORIGINS` | `""` | string | 允许的 CORS 跨域源（逗号分隔） |
| `SERVICE_HUB_DB_PATH` | `""` | string | SQLite 数据库路径（空表示纯内存模式） |
| `SERVICE_HUB_PG_DSN` / `PG_DSN` | `""` | string | Phase B PostgreSQL 连接串（启用多副本原子租约争抢，带 3s 探针自动降级） |
| `SERVICE_HUB_PG_MAX_CONNS` | `10` | int | PostgreSQL 连接池最大连接数（默认按 NumCPU*4 动态调优） |
| `SERVICE_HUB_PG_MIN_CONNS` | `2` | int | PostgreSQL 连接池最小连接数（默认按 NumCPU 动态调优） |
| `SERVICE_HUB_LEASE_TTL` | `60` | int | 任务租约有效时间（秒，超时自动被其他 Worker 认领） |
| `SERVICE_HUB_RETENTION_DAYS` | `30` | int | 终态任务保留天数（0 表示禁用清理） |
| `SERVICE_HUB_SHUTDOWN_TIMEOUT` | `5` | int | 优雅停机等待超时（秒） |
| `SERVICE_HUB_LOG_FORMAT` | `json` | string | 日志格式: `json`（生产推荐） \| `text` |
| `SERVICE_HUB_LOG_LEVEL` | `info` | string | 日志级别: `debug` \| `info` \| `warn` \| `error` |
| `SERVICE_HUB_TLS_PINNED_PUBKEY_FILE` | `""` | string | 客户端固定公钥 PEM 路径（SPKI Pinning，防御 CA 劫持） |
| `SERVICE_HUB_API_KEYS` | `""` | string | Scope-based 多 Key 映射（JSON），每个 Key 携带 `Name` 与 `Scopes`（`hub:read` / `hub:dispatch`） |
| `SERVICE_HUB_API_KEYS_FILE` | `""` | string | Scope-based API Key 文件路径（支持 KeyStore 热轮转） |
| `SERVICE_HUB_REQUIRE_TLS` | `false` | bool | 生产零信任（P0-1）：强制要求启用 TLS，否则拒绝启动 |
| `SERVICE_HUB_STRICT_STORAGE` / `STRICT_STORAGE` | `true` | bool | 严格存储（P0-4）：配置 PG 但连接失败时拒绝启动，不静默降级 |
| `SERVICE_HUB_RATE_LIMIT_RPS` | `100` | int | 每客户端 IP 令牌桶每秒请求数（0 = 不限流） |
| `SERVICE_HUB_RATE_LIMIT_BURST` | `200` | int | 令牌桶突发容量 |
| `SERVICE_HUB_DATASOURCE_API_KEY` | `""` | string | 访问下游 datasource-mgr 的 API Key |
| `PRIVACY_ALLOWED_CIDRS` | `""` | string | 允许访问的客户端 CIDR 白名单（逗号分隔） |
| `SERVICE_HUB_AUDIT_LOG_URLS` | `""` | string | 出域存证 audit-log REST 地址列表（逗号分隔；未配置时回退 `SERVICE_HUB_AUDIT_HTTP` ➔ `http://audit-log:8084`） |
| `SERVICE_HUB_AUDIT_HTTP` | `""` | string | audit-log 存证备用地址（docker-compose 注入的别名） |
| `SERVICE_HUB_AUDIT_LOG_API_KEY` / `AUDIT_LOG_API_KEY` | `""` | string | 访问 audit-log 的 API Key（专用变量优先，回退存证服务入站密钥） |
| `SERVICE_HUB_AUDIT_LOG_TIMEOUT` | `10` | int | 单次存证提交超时（秒） |
| `SERVICE_HUB_AUDIT_LOG_MAX_RETRIES` | `3` | int | 存证网络错误/5xx 重试次数 |
| `SERVICE_HUB_AUDIT_LOG_TLS_ENABLED` | `false` | bool | 存证链路是否启用 TLS/mTLS（P0-6 出域存证 fail-closed） |
| `SERVICE_HUB_AUDIT_LOG_TLS_CERT_FILE` | `""` | string | 存证链路客户端证书 PEM 路径（可回退 `SERVICE_HUB_TLS_CERT_FILE`） |
| `SERVICE_HUB_AUDIT_LOG_TLS_KEY_FILE` | `""` | string | 存证链路客户端私钥 PEM 路径（可回退 `SERVICE_HUB_TLS_KEY_FILE`） |
| `SERVICE_HUB_AUDIT_LOG_TLS_CA_FILE` | `""` | string | 存证链路根 CA 证书 PEM 路径（可回退 `SERVICE_HUB_TLS_CA_FILE`） |

---

## 3. 健康检查与运维监控

### 3.1 探活与就绪检查
```bash
# 存活探针 (Liveness: 进程存活即 200)
curl -s http://127.0.0.1:8082/health | jq .

# 就绪探针 (Readiness: 深度检查 Agent + Datasource 连通性)
curl -s http://127.0.0.1:8082/readyz | jq .

# 综合健康状态
curl -s http://127.0.0.1:8082/health | jq .
```
可同时观测：
- 调度中枢自身状态 (`backend: "ok"`)
- 上游 Agent 连通性与命名空间 (`agent: {"status": "ok"}`)
- 下游模拟数据源连通性 (`datasource: "ok"`)

### 3.2 提交调度任务
```bash
curl -s -X POST http://127.0.0.1:8082/v1/hub/dispatch \
  -H "Content-Type: application/json" \
  -d '{"datasource_id": "ds_yibao", "operation": "mask"}' | jq .
```

任务需在提交时显式携带 `payload`（`fetch` 阶段的分页自动抽取接口已移除）；随后在 `classify` 标签通过一次 Agent 一体化调用（`POST /v1/agent/process`，404 回退 `/v1/medical/process`）完成分类与脱敏，并在 `audit` 标签向 audit-log 提交出域存证（P0-6 fail-closed）。若需按身份证号端到端取数+脱敏，请改用 `POST /v1/hub/fetch-and-desensitize`。

### 3.3 查看 Prometheus 监控指标
```bash
curl -s http://127.0.0.1:8082/metrics | head -n 30
```

---

## 4. 外网连接与反向代理架构说明 (是否需要 Nginx 及生产配置指南)

### 4.1 核心结论：外网连接服务场景下的 Nginx 选型定性

> 💡 **核心结论**：
>
> 1. **仅内网微服务间调用（VPC / K8s ClusterIP / Docker Compose 内部网络）**：**无需使用 Nginx**。
>    - `service-hub` 基于 Go 语言原生高性能网络栈（`net/http` 与 `grpc-go`）构建，内建协程并发调度、TLS 1.3/mTLS 双向认证及 SPKI 公钥固定能力，内网点对点通信性能最佳；
> 2. **需要对外网（Public Internet / 跨机构网络 / 外部第三方数据消费者）提供连接服务**：**强烈推荐且在生产规范下必须前置 Nginx / API Gateway / Ingress Controller**！
>    - 将内部微服务端口直接暴露给公网存在巨大的安全隐患（DDoS 洪峰击穿、慢速连接耗尽、缺少 WAF 拦截、证书管理分散等）。前置 Nginx 作为 DMZ 隔离层与统一流量入口，是企业级数据流通平台的标准架构实践。

---

### 4.2 内网直连 vs 外网暴露场景对比分析

| 对比维度 | 内网微服务集群内调用（VPC / K8s） | 对外网（公网 / 跨机构）提供连接服务 |
|---|---|---|
| **网络边界与可信度** | 位于受保护的受信任内网环境，调用方可控 | 面向不可信公网，面临恶意嗅探、爬虫与扫描 |
| **前置 Nginx 必要性** | ❌ **无需 Nginx**（点对点直接通信，减少网络跳数） | ✅ **必须/强烈推荐 Nginx**（前置安全隔离与流量收敛） |
| **SSL/TLS 证书管理** | 采用内部自建 CA 签发 mTLS 双向证书，服务内建校验 | 采用公网权威 CA（如 Let's Encrypt / 商业证书），由 Nginx 统一管理与自动续签 |
| **DDoS / 突发流量防御** | 内部并发可控，依赖 Go 进程内信号量（`taskSem`）限流 | 极易遭遇恶意 CC 攻击与突发洪峰，需 Nginx 在网络最外层进行 IP 级令牌桶限流 |
| **域名与端口暴露** | 各微服务使用独立内部端口（8082, 8083, 8084 等） | 统一收敛至标准 443 端口，通过 URL 路径（`/v1/hub/`）进行安全路由分发 |
| **报文过滤与 WAF** | 内部 trusted 载荷，直接解析 | 需前置 WAF 拦截 SQL 注入、XSS、畸形 JSON 与超大恶意 Payload |
| **审计真实 IP 溯源** | 内部服务 IP 即可满足链路追踪 | 必须依靠 Nginx 精准提取 CDN / 代理层后面的公网真实客户端 IP 并注入头信息 |

---

### 4.3 为什么对外网提供服务必须引入 Nginx？（7 大核心价值）

```text
                               【公网不可信网络】
                                      │
               ┌──────────────────────┴──────────────────────┐
               ▼                                             ▼
     [外部数据消费者 (REST)]                         [外部调度系统 (gRPC)]
               │                                             │
               └──────────────────────┬──────────────────────┘
                                      │ HTTPS (443) / HTTP/2 (TLS)
                                      ▼
             ┌─────────────────────────────────────────────────┐
             │            前置 Nginx 反向代理 / WAF 网关         │
             │  • 公网 SSL 证书卸载 (TLS Offloading)           │
             │  • IP 级令牌桶限流 (limit_req / limit_conn)     │
             │  • 统一域名收敛与 URL 路由分流                  │
             │  • 真实客户端 IP 提取 (X-Real-IP / X-Forwarded) │
             │  • 敏感端点（如 /metrics）内网隔离访问           │
             └────────────────────────┬────────────────────────┘
                                      │
               ┌──────────────────────┴──────────────────────┐
               │ 内网 HTTP (8082)                            │ 内网 gRPC (50052)
               ▼                                             ▼
     ┌───────────────────┐                         ┌───────────────────┐
     │ service-hub 实例 1│                         │ service-hub 实例 2│
     │   (调度流水线)    │                         │   (调度流水线)    │
     └─────────┬─────────┘                         └─────────┬─────────┘
               │                                             │
               └──────────────────────┬──────────────────────┘
                                      │
                 ┌────────────────────┼────────────────────┐
                 ▼                    ▼                    ▼
        [PrivShield Agent]   [datasource-mgr]        [audit-log]
             (:8079)              (:8083)              (:8084)
```

1. **公网 SSL/TLS 证书生命周期管理与卸载 (TLS Offloading)**：
   - 公网必须使用受信商业 CA 或 Let's Encrypt 证书。Nginx 支持 ACME 协议自动申请、续签与热重载；
   - Nginx 在边缘卸载 HTTPS/TLS 握手开销，解密后以内网 HTTP 或轻量 mTLS 转发至后端 Go 进程，极大降低调度中枢的 CPU 密码学算力负担。
2. **公网抗 DDoS / CC 攻击与多维令牌桶限流 (Rate Limiting & Throttling)**：
   - `service-hub` 触发的数据脱敏与差分隐私流水线属于计算密集型任务，若公网未加限制，几十个恶意并发即可打满服务器 CPU；
   - 利用 Nginx `limit_req_zone`（漏桶/令牌桶）限制单 IP 每秒请求数（如 `10r/s`，突发 `burst=20`），配合 `limit_conn_zone` 限制单 IP 最大并发连接数（如 10），在流量进入 Go 进程前即被阻断。
3. **统一公网域名收敛与 URL 路由分发 (API Gateway / Ingress)**：
   - 避免将内部微服务杂乱端口（8082, 8083, 8084, 5173）直接向公网开放导致的端口扫描与攻击面扩大；
   - 统一使用 `https://api.privshield.com` 泛域名收敛：
     - `/v1/hub/` ──▶ `service-hub:8082`（调度中枢）
     - `/v1/datasources/` ──▶ `datasource-mgr:8083`（数据源管理）
     - `/v1/audit/` ──▶ `audit-log:8084`（不可篡改审计存证）
     - `/` ──▶ `console-web:5173`（前端控制台）
4. **Web 应用防火墙（WAF）与深度报文检测 (Payload Inspection)**：
   - 限制请求体最大尺寸（`client_max_body_size 10M`），防止超大 Payload 耗尽内存导致 OOM；
   - 挂载 ModSecurity / OpenResty / Coraza WAF 模块，过滤 SQL 注入、跨站脚本、目录穿越等恶意利用。
5. **真实客户端 IP 穿透与不可篡改审计追踪 (Real-IP Propagation)**：
   - 外网流量通常途经 CDN（Cloudflare / 阿里云 CDN）或四层负载均衡器（L4 SLB）；
   - Nginx 通过 `set_real_ip_from` 与 `X-Real-IP` / `X-Forwarded-For` 准确还原客户端真实公网 IP 并注入 HTTP/gRPC 请求头，确保 `service-hub` 在写入 `audit-log` 存证时记录合规真实的调用者身份。
6. **双协议统一支持（HTTP REST + gRPC over HTTP/2）**：
   - Nginx 原生支持 HTTP/2 与 `grpc_pass` 指令，能够同时对外代理 HTTP REST 接口与 gRPC 远程过程调用接口。
7. **生产集群负载均衡与故障平滑隔离 (Load Balancing & Health Checks)**：
   - 外网流量增大时，前置 Nginx 可将流量按权重或最少连接（`least_conn`）分发到多个 `service-hub` 实例，并配合 `max_fails` 和 `fail_timeout` 自动剔除异常节点。

---

### 4.4 Nginx 生产级完整配置方案

以下为经过生产环境加固验证的 Nginx 配置模板（同时支持 HTTP REST 与 gRPC HTTP/2 反向代理、令牌桶限流、真实 IP 透传与内部端点隔离）：

#### 配置文件路径：`/etc/nginx/conf.d/service_hub.conf`

```nginx
# ==============================================================================
# PrivShield - Service Hub (数据服务调度中枢) 生产级反向代理配置
# ==============================================================================

# ── 1. 客户端 IP 限流与连接限制区域定义 ─────────────────────────────────────────
# 针对 HTTP REST API：基于源 IP 限制请求速率为 20r/s，分配 10MB 内存空间
limit_req_zone $binary_remote_addr zone=hub_rest_limit:10m rate=20r/s;

# 针对 gRPC API：基于源 IP 限制请求速率为 50r/s
limit_req_zone $binary_remote_addr zone=hub_grpc_limit:10m rate=50r/s;

# 基于源 IP 限制最大并发连接数为 20
limit_conn_zone $binary_remote_addr zone=hub_conn_limit:10m;

# ── 2. 后端服务集群上游 (Upstream) 定义 ─────────────────────────────────────────
# HTTP REST 负载均衡池（支持长连接保持）
upstream service_hub_http {
    least_conn; # 最少连接负载算法
    server 127.0.0.1:8082 max_fails=3 fail_timeout=10s weight=1;
    # 若存在多台部署实例，可在此追加节点：
    # server 192.168.1.102:8082 max_fails=3 fail_timeout=10s weight=1;
    
    keepalive 64; # 保持 64 个空闲长连接，避免频繁 TCP 握手
}

# gRPC 负载均衡池
upstream service_hub_grpc {
    least_conn;
    server 127.0.0.1:50052 max_fails=3 fail_timeout=10s weight=1;
    # server 192.168.1.102:50052 max_fails=3 fail_timeout=10s weight=1;
    
    keepalive 64;
}

# ── 3. HTTP 强制跳转 HTTPS (80 ➔ 443) ─────────────────────────────────────────
server {
    listen 80;
    listen [::]:80;
    server_name api.privshield.example.com;

    # Let's Encrypt 证书自动续签验证路径
    location /.well-known/acme-challenge/ {
        root /var/www/certbot;
    }

    # 其余所有流量一律 301 强制跳转至 HTTPS
    location / {
        return 301 https://$host$request_uri;
    }
}

# ── 4. HTTPS 主服务 (443 ssl http2) ───────────────────────────────────────────
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name api.privshield.example.com;

    # ── SSL/TLS 生产安全加固配置 ──────────────────────────────────────────────
    ssl_certificate         /etc/letsencrypt/live/api.privshield.example.com/fullchain.pem;
    ssl_certificate_key     /etc/letsencrypt/live/api.privshield.example.com/privkey.pem;
    ssl_session_timeout     1d;
    ssl_session_cache       shared:SSL:10m;
    ssl_session_tickets     off;

    # 仅允许强加密协议与套件
    ssl_protocols           TLSv1.2 TLSv1.3;
    ssl_ciphers             ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers off;

    # ── 安全响应头注入 (OWASP 推荐基线) ───────────────────────────────────────
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "DENY" always;
    add_header X-XSS-Protection "1; mode=block" always;
    add_header Referrer-Policy "no-referrer-when-downgrade" always;

    # ── 基础传输限制与缓冲区配置 ──────────────────────────────────────────────
    client_max_body_size    20M;    # 限制单次任务最大载荷为 20MB（防止大包攻击）
    client_body_buffer_size 512k;
    limit_conn hub_conn_limit 20;   # 限制单 IP 最大 20 个并发连接

    # ── 真实客户端 IP 还原（若前置有 CDN 或云厂商 SLB，需配置信任网段）────────
    # set_real_ip_from 100.64.0.0/10;  # 例如云厂商内网 SLB 网段
    # set_real_ip_from 173.245.48.0/20; # Cloudflare IP 段示例
    # real_ip_header   X-Forwarded-For;
    # real_ip_recursive on;

    # ──────────────────────────────────────────────────────────────────────────
    # A. 调度中枢 HTTP REST API 反向代理 (/v1/hub/)
    # ──────────────────────────────────────────────────────────────────────────
    location /v1/hub/ {
        # 应用速率限制：允许 20r/s，突发队列 buffer 为 30，不延迟处理
        limit_req zone=hub_rest_limit burst=30 nodelay;

        proxy_pass http://service_hub_http;
        proxy_http_version 1.1;
        proxy_set_header Connection ""; # 启用 upstream keepalive

        # 传递真实客户端网络元数据
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Request-ID      $request_id; # 注入链路追踪唯一 ID

        # 超时设置（流水线涉及多阶段处理，适当放宽读取超时）
        proxy_connect_timeout 5s;
        proxy_send_timeout    60s;
        proxy_read_timeout    60s;

        # 缓冲与重试
        proxy_buffering       on;
        proxy_buffer_size     16k;
        proxy_buffers         8 32k;
        proxy_next_upstream   error timeout invalid_header http_502 http_503;
    }

    # ──────────────────────────────────────────────────────────────────────────
    # B. 健康检查探针 (/health)
    # ──────────────────────────────────────────────────────────────────────────
    location = /health {
        proxy_pass http://service_hub_http/health;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_connect_timeout 3s;
        proxy_read_timeout    5s;
        access_log off; # 关闭探针日志，避免污染访问日志
    }

    # ──────────────────────────────────────────────────────────────────────────
    # C. Prometheus 指标端点安全隔离 (/metrics)
    # ──────────────────────────────────────────────────────────────────────────
    location = /metrics {
        # 严格限制：仅允许内网监控采集机 IP 访问，公网直接 403 阻断
        allow 10.0.0.0/8;
        allow 172.16.0.0/12;
        allow 192.168.0.0/16;
        allow 127.0.0.1;
        deny all;

        proxy_pass http://service_hub_http/metrics;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
    }

    # ──────────────────────────────────────────────────────────────────────────
    # D. gRPC (HTTP/2) 远程过程调用反向代理 (/servicehub.ServiceHubService/)
    # ──────────────────────────────────────────────────────────────────────────
    location /servicehub.ServiceHubService/ {
        # 应用 gRPC 专用速率限制
        limit_req zone=hub_grpc_limit burst=50 nodelay;

        grpc_pass grpc://service_hub_grpc;
        
        # 传递 gRPC 元数据
        grpc_set_header Host              $host;
        grpc_set_header X-Real-IP         $remote_addr;
        grpc_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        grpc_set_header X-Forwarded-Proto $scheme;

        # gRPC 超时设置
        grpc_connect_timeout 5s;
        grpc_send_timeout    60s;
        grpc_read_timeout    60s;

        # 错误拦截
        grpc_next_upstream error timeout invalid_header http_502 http_503;
    }

    # 其余未匹配路径默认 404
    location / {
        return 404 '{"code":404,"error":"endpoint not found","module":"service-hub-gateway"}';
        default_type application/json;
    }
}
```

---

### 4.5 场景化部署与编排实操

#### 4.5.1 裸机 / Linux 虚拟机部署配置
1. 安装 Nginx（要求 Nginx 1.18+ 以完整支持 `grpc_pass` 与 `http2`）：
   ```bash
   sudo apt-get install -y nginx  # Debian/Ubuntu
   # 或
   sudo yum install -y nginx      # CentOS/RHEL
   ```
2. 将上述配置文件保存到 `/etc/nginx/conf.d/service_hub.conf`；
3. 测试并热加载配置：
   ```bash
   sudo nginx -t && sudo systemctl reload nginx
   ```

---

#### 4.5.2 Docker Compose 容器化联动部署
在 `deploy/docker-compose/docker-compose.yml` 中编排前置 Nginx：

```yaml
version: '3.8'

services:
  # 1. 边缘 Nginx 反向代理网关
  nginx-gateway:
    image: nginx:1.25-alpine
    container_name: privshield-nginx-gateway
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./nginx.conf:/etc/nginx/conf.d/default.conf:ro
      - ./certs:/etc/nginx/certs:ro
    depends_on:
      - service-hub
    networks:
      - privshield-net

  # 2. 数据服务调度中枢（仅暴露在 Docker 内部网络，不映射外部端口）
  service-hub:
    image: privshield-service-hub:1.8.0
    container_name: privshield-service-hub
    restart: unless-stopped
    expose:
      - "8082"   # HTTP REST 内部端口
      - "50052"  # gRPC 内部端口
    environment:
      - SERVICE_HUB_HOST=0.0.0.0
      - SERVICE_HUB_PORT=8082
      - SERVICE_HUB_GRPC_HOST=0.0.0.0
      - SERVICE_HUB_GRPC_PORT=50052
      - PRIVACY_AGENT_REST_HOST=privshield-agent
      - PRIVACY_REST_PORT=8079
      - DATASOURCE_MGR_HOST=datasource-mgr
      - DATASOURCE_MGR_PORT=8083
    networks:
      - privshield-net

networks:
  privshield-net:
    driver: bridge
```

---

#### 4.5.3 Kubernetes Ingress-Nginx 部署配置
在 K8s 集群中，使用 Ingress-Nginx Controller 对外暴露服务：

```yaml
# service-hub-ingress.yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: service-hub-ingress
  namespace: privshield
  annotations:
    kubernetes.io/ingress.class: "nginx"
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
    nginx.ingress.kubernetes.io/proxy-body-size: "20m"
    nginx.ingress.kubernetes.io/proxy-connect-timeout: "5"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "60"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "60"
    # 限流注解：限制单 IP 每分钟最多 1200 个请求
    nginx.ingress.kubernetes.io/limit-rps: "20"
    nginx.ingress.kubernetes.io/limit-connections: "20"
spec:
  tls:
    - hosts:
        - api.privshield.example.com
      secretName: privshield-api-tls
  rules:
    - host: api.privshield.example.com
      http:
        paths:
          - path: /v1/hub
            pathType: Prefix
            backend:
              service:
                name: service-hub
                port:
                  number: 8082
---
# 若需通过 Ingress 暴露 gRPC 服务：
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: service-hub-grpc-ingress
  namespace: privshield
  annotations:
    kubernetes.io/ingress.class: "nginx"
    nginx.ingress.kubernetes.io/backend-protocol: "GRPC"
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  tls:
    - hosts:
        - grpc.privshield.example.com
      secretName: privshield-api-tls
  rules:
    - host: grpc.privshield.example.com
      http:
        paths:
          - path: /servicehub.ServiceHubService
            pathType: Prefix
            backend:
              service:
                name: service-hub
                port:
                  number: 50052
```

---

### 4.6 生产调优与常见问题排查 (FAQ)

#### Q1: 客户端调用返回 `502 Bad Gateway`？
- **排查步骤**：
  1. 检查后端 `service-hub` 进程是否存活：`curl -I http://127.0.0.1:8082/health`；
  2. 检查 Nginx 错误日志：`sudo tail -n 50 /var/log/nginx/error.log`；
  3. 若日志出现 `connect() failed (111: Connection refused)`，确认 `upstream` 中配置的端口是否与 `SERVICE_HUB_PORT` 一致；
  4. 若在 SELinux 环境下（CentOS/RHEL），执行 `setsebool -P httpd_can_network_connect 1` 允许 Nginx 进行网络转发。

#### Q2: gRPC 远程调用失败，报错 `UNAVAILABLE` 或 `HTTP 400 Bad Request`？
- **排查步骤**：
  1. 确认 Nginx 是否启用了 `http2` 协议（`listen 443 ssl http2;`），gRPC 强制依赖 HTTP/2 传输帧；
  2. 确认 gRPC 代理指令使用的是 `grpc_pass` 而非 `proxy_pass`；
  3. 检查客户端连接的 host 是否与证书的 SAN 匹配，且客户端启用了 TLS。

#### Q3: 批量下发大体积脱敏任务时报 `413 Request Entity Too Large`？
- **解决方案**：
  - 在 Nginx `server` 或 `location /v1/hub/` 块中调大 `client_max_body_size`（如 `client_max_body_size 50M;`）。

#### Q4: audit-log 审计记录中的调用方 IP 变成了 `127.0.0.1` 或 Nginx 容器 IP？
- **解决方案**：
  - 确认 Nginx 配置中包含了 `proxy_set_header X-Real-IP $remote_addr;` 与 `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`；
  - 若前置还有阿里云 SLB、Cloudflare CDN 等，需配置 `set_real_ip_from <SLB_IP_RANGE>;` 与 `real_ip_header X-Forwarded-For;`。

#### Q5: 触发复杂差分隐私或大规模 K-匿名任务时报 `504 Gateway Timeout`？
- **解决方案**：
  - 调大 Nginx 的超时阈值：`proxy_read_timeout 120s;` 与 `grpc_read_timeout 120s;`；
  - 同时调大 `service-hub` 的调度超时配置 `SERVICE_HUB_SCHEDULE_TIMEOUT=120`。

