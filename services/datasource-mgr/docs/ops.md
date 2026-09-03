# 模拟数据源服务 (Mock Datasource Manager) — 运维手册

> 本文档提供 **数联天下 · 数盾 (`PrivShield`)** 模拟数据源模块（`services/datasource-mgr`）的启动、配置、国密 mTLS 证书部署与接口联调说明。

---

## 1. 运行与启动脚本

### 1.1 开发模式 (Insecure / No-mTLS)

```bash
cd services/datasource-mgr
bash scripts/dev-run.sh
# 或
make dev
```

默认同时启动：
- **HTTP REST**：`http://127.0.0.1:8083`
- **gRPC (insecure)**：`127.0.0.1:50053`

### 1.2 生产加固模式 (国密 SM2 / TLS 1.3 mTLS + CN 白名单)

```bash
cd services/datasource-mgr
bash scripts/prod-run.sh
# 或
make prod
```

自动加载 `services/datasource-mgr/certs/` 目录中的测试证书与客户端证书白名单。

### 1.3 重新生成测试证书链

```bash
cd services/datasource-mgr
bash scripts/gen-certs.sh
# 或
make gen-certs
```

---

## 2. 环境变量速查表

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `DATASOURCE_MGR_HOST` | `127.0.0.1` | HTTP/HTTPS REST 服务监听主机 |
| `DATASOURCE_MGR_PORT` | `8083` | HTTP/HTTPS REST 服务监听端口 |
| `DATASOURCE_MGR_GRPC_HOST` | `127.0.0.1` | gRPC 服务监听主机 |
| `DATASOURCE_MGR_GRPC_PORT` | `50053` | gRPC 服务监听端口 |
| `DATASOURCE_MGR_TLS_ENABLED` | `false` | 是否在 HTTP REST 与 gRPC 服务上启用 TLS 1.3 / 国密 SM2 mTLS |
| `DATASOURCE_MGR_TLS_CERT_FILE` | (空) | 服务端 X.509 证书 PEM 路径 |
| `DATASOURCE_MGR_TLS_KEY_FILE` | (空) | 服务端私钥 PEM 路径 |
| `DATASOURCE_MGR_TLS_CA_FILE` | (空) | 客户端证书校验 CA 证书 PEM 路径 |
| `DATASOURCE_MGR_TLS_CLIENT_AUTH` | (空) | 客户端认证模式: `require` \| `verify` \| `request` |
| `DATASOURCE_MGR_TLS_PINNED_PUBKEY_FILE` | (空) | 客户端公钥指纹固定文件路径 (SPKI Pinning) |
| `PRIVACY_AUTH_MTLS_WHITELIST_FILE` | (空) | gRPC 客户端证书 CN 白名单 YAML 文件（启用 gRPC TLS 时必填） |
| `DATASOURCE_MGR_REQUIRE_TLS` | `false` | 生产门禁：置真时未启用 TLS 即拒绝启动 (fail-closed) |
| `DATASOURCE_MGR_API_KEY` | (空) | 本模块入站 API Key（空表示免密） |
| `DATASOURCE_MGR_API_KEYS` | (空) | Scope-based API Key 映射，优先于单 `API_KEY` |
| `DATASOURCE_MGR_API_KEYS_FILE` | (空) | API Key 文件路径（支持热轮转，K8s Secret 投影） |
| `DATASOURCE_MGR_CORS_ORIGINS` | (空) | 允许的 CORS 跨域源（逗号分隔） |
| `DATASOURCE_MGR_STRICT_STORAGE` | `true` | 严格存储模式 (P0-4)：CSV 损坏行直接报错，不静默丢弃 |
| `DATASOURCE_MGR_RATE_LIMIT_RPS` | `100` | 每客户端 IP 令牌桶限流速率（0 = 不限流） |
| `DATASOURCE_MGR_RATE_LIMIT_BURST` | `200` | 令牌桶突发容量 |
| `DATASOURCE_MGR_SHUTDOWN_TIMEOUT` | `5` | HTTP 优雅关闭超时秒数 |
| `DATASOURCE_MGR_LOG_FORMAT` | `json` | 日志格式: `json` \| `text` |
| `DATASOURCE_MGR_LOG_LEVEL` | `info` | 日志级别: `debug` \| `info` \| `warn` \| `error` |

---

## 3. 接口快速验证与联调

### 3.1 HTTP 综合健康检查（开发模式）
```bash
curl -s http://127.0.0.1:8083/health | jq .
curl -s http://127.0.0.1:8083/readyz | jq .
```

### 3.2 HTTPS 双向认证 (mTLS) 调取示例（生产加固模式）
```bash
# 携带 CA 根证书与客户端证书访问 HTTPS REST API（按身份证号查询单条记录）
curl -s --cacert certs/ca.crt \
  --cert certs/client.crt \
  --key certs/client.key \
  "https://127.0.0.1:8083/api/datasources/ds_yibao/record-by-id?id_card_no=110101196809171010" | jq .
```

### 3.3 按身份证号查询康养数据 (27 字段)
```bash
curl -s "http://127.0.0.1:8083/api/datasources/ds_kangyang/record-by-id?id_card_no=110105198402151071" | jq .
```

---

## 4. 反向代理与网关架构说明 (是否需要 Nginx 及使用场景)

### 4.1 核心结论

> **默认开发、Docker Compose 与 K8s 内部集群环境下：不需要使用 Nginx。**
>
> `datasource-mgr` 是基于 Go 语言原生高性能网络栈（`net/http` + `Gin` 与 `grpc-go`）构建的独立微服务，其自身已内建高并发事件驱动模型、精细化连接超时控制（防 Slowloris）、TLS 1.3 / 国密 SM2 双向身份认证及应用层鉴权能力。

### 4.2 为什么默认场景无需 Nginx？

| 维度 | `datasource-mgr` 内建能力 | 为什么可省略 Nginx |
|---|---|---|
| **并发与连接模型** | Go 原生 Goroutine 协程调度 + HTTP/2 流多路复用 | 无需传统动态语言（如 Python WSGI / PHP-FPM）前置的进程管理与连接缓冲池，单实例即可支撑上万并发连接。 |
| **抗 Slowloris / DoS** | 显式配置了网络超时：<br>• `ReadHeaderTimeout: 5s`<br>• `ReadTimeout: 30s`<br>• `WriteTimeout: 60s`<br>• `IdleTimeout: 120s`<br>• `MaxHeaderBytes: 1MB` | 天然免疫慢速连接攻击与连接泄漏，无需依赖 Nginx 进行请求头缓冲保护。 |
| **传输安全与零信任** | 内建 **TLS 1.3 / 国密 SM2** 强加密基线、**mTLS 双向证书校验** 与 **CN 白名单鉴权** | 内部微服务间（如 `service-hub` ⇋ `datasource-mgr`）可直接实现端到端加密与防篡改身份校验。 |
| **安全响应与中间件** | Gin 引擎已内置挂载：<br>`TraceMiddleware`、`StructuredLogger`、`Recovery`、`SecurityHeaders`、`CORS`、`Auth (API Key)` | 安全响应头注入、跨域策略控制、访问日志及崩溃恢复已在进程内闭环。 |
| **服务网络拓扑** | 定位于内部数据源/模拟服务，主要由 `service-hub`（调度中枢）或 `bff-go`（网关）在内网 VPC 子网调用 | 在专有 VPC 子网或 K8s ClusterIP 网络下，微服务通过 DNS 直接点对点通信，额外增加 Nginx 反而会增加网络跳数与延迟。 |

---

### 4.3 什么时候“需要”或“推荐”引入 Nginx？

在以下特定的企业级生产或网络架构演进场景下，建议在 `datasource-mgr` 上游架设 Nginx 反向代理或 API Gateway：

#### 场景 1：无容器编排环境（云虚拟机 ECS 多节点）下的“多实例负载均衡”
- **适用情况**：在云虚拟机 (ECS) 多节点或混合云环境下部署了多个 `datasource-mgr` 实例，但未使用 Kubernetes Service 进行内部负载均衡。
- **Nginx 作用**：通过 `upstream` 负载均衡器实现 HTTP REST 轮询/权重分发，并利用 `grpc_pass` 代理 gRPC 连接。

#### 场景 2：统一公网域名收敛与外部 SSL 证书卸载 (API Gateway / Ingress)
- **适用情况**：需要将整个 PrivShield 体系（`console-web`、`service-hub`、`datasource-mgr`、`audit-log`）统一对外暴露在同一个公网域名与标准端口（如 `https://api.privshield.com`）。
- **Nginx 作用**：Nginx 统一管理公网泛域名 SSL 证书（自动续签/TLS 卸载），并按 URL 路径路由：
  - `/` ──▶ 静态前端 UI (`console-web:5173`)
  - `/api/datasources/` ──▶ `datasource-mgr:8083`
  - `/api/hub/` ──▶ `service-hub:8082`

#### 场景 3：直接面向不可信公网时的 IP 级令牌桶限流与 WAF 防护
- **适用情况**：数据源管理服务需要直接暴露给公网第三方调用。
- **Nginx 作用**：
  - 利用 Nginx `limit_req_zone` / `limit_conn_zone` 实施基于源 IP 的高频请求限流；
  - 挂载 ModSecurity 或 OpenResty 过滤恶意爬虫探测、SQL 注入及异常 Payload。

---

### 4.4 Nginx 生产反向代理配置示例模板

```nginx
# 1. HTTP REST 负载均衡上游定义
upstream datasource_mgr_http {
    server 127.0.0.1:8083 max_fails=3 fail_timeout=10s;
    keepalive 32;
}

# 2. gRPC 负载均衡上游定义
upstream datasource_mgr_grpc {
    server 127.0.0.1:50053 max_fails=3 fail_timeout=10s;
    keepalive 32;
}

server {
    listen 443 ssl http2;
    server_name api.privshield.com;

    # 外部 SSL 证书配置
    ssl_certificate     /etc/nginx/certs/fullchain.pem;
    ssl_certificate_key /etc/nginx/certs/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;

    # ── A. HTTP REST API 反向代理 ──────────────────────────────────────
    location /api/datasources/ {
        proxy_pass http://datasource_mgr_http;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # 超时设置
        proxy_connect_timeout 5s;
        proxy_read_timeout    60s;
        proxy_send_timeout    60s;
    }

    # ── B. gRPC (HTTP/2) 远程过程调用反向代理 ──────────────────────────
    location /datasourcemgr.DataSourceManagerService/ {
        grpc_pass grpc://datasource_mgr_grpc;
        grpc_set_header Host $host;
        grpc_set_header X-Real-IP $remote_addr;
        grpc_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

        # gRPC 超时设置
        grpc_connect_timeout 5s;
        grpc_read_timeout    60s;
        grpc_send_timeout    60s;
    }
}
```
