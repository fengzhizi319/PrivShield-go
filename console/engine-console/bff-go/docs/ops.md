# Go gRPC 代理后端 — 运维文档

> 面向本地开发与部署运维的操作手册：开发模式与生产模式的区别、配置方式、跨域（CORS）解决方案、启停与排障。
>
> 关联文档：[design.md](design.md)（架构设计）、[api.md](api.md)（接口参考）、[test.md](test.md)（测试策略）。
> 历史说明：`console/bff-py` 已删除，当前统一由 `console/bff-go` 提供 REST/gRPC 上游代理。

---

## 1. 定位与端口总览

本后端（`console/bff-go`）是测试控制台的 **Go BFF 代理网关**，基于 Gin + grpc-go，把前端的 REST 请求**转换为 gRPC 调用**转发给 `PrivShield`，并可选地托管前端构建产物（SPA）。它对前端暴露统一的 JSON 契约，页面顶部 Backend Selector 仅用于在 REST 与 gRPC 两种上游协议间切换。

控制台涉及的进程与默认端口：

| 进程 | 默认地址 | 协议 | 说明 |
|---|---|---|---|
| `PrivShield` | `127.0.0.1:8079` | REST | 隐私能力服务 REST 接口 |
| `PrivShield` | `127.0.0.1:50051` | gRPC | 隐私能力服务（上游，本后端调用） |
| **Go BFF 代理后端** | `127.0.0.1:8081` | HTTP | 本文档主角，转发 REST/gRPC + 托管 UI |
| Vite 开发服务器 | `localhost:5173` | HTTP | 仅前端开发模式使用 |

整体链路：

```mermaid
graph LR
    A[React 前端] -->|HTTP/JSON| B[Go BFF 8081]
    B -->|gRPC 默认| C[PrivShield 50051]
    B -.->|?protocol=rest| E[PrivShield REST 8079]
```

> 历史说明：`console/bff-py` 已删除，当前统一由 `console/bff-go` 提供 REST/gRPC 上游代理。

---

## 2. 开发模式 vs 生产模式

两种模式的核心区别在于 **前端从哪里被加载** 以及 **请求是否跨域**。

如果你想先从“前端 / 后端 / agent / 服务器整体怎么配”的角度理解开发模式与商业化产品模式，请先看 `../../docs/modes.md`；本文这一节聚焦 Go 后端自身该怎么切换。

### 2.1 对比总览

| 维度 | 开发模式 | 生产模式 |
|---|---|---|
| 前端加载来源 | Vite 开发服务器 `localhost:5173`（热更新） | 后端托管的 `web/dist` 静态文件 |
| 前端构建 | 不需要构建，源码直跑 | 需先 `pnpm build` 产出 `web/dist` |
| API 请求走向 | 跨域直连后端绝对地址（5173 → 8081） | 与后端同源（8081 → 8081） |
| 是否触发 CORS | **是**（端口不同，依赖后端 CORS 中间件） | **否**（同源，天然无跨域） |
| 后端运行方式 | `go run ./cmd/server`（改代码手动重启） | 预编译二进制 `./bin/backend-go`，稳定运行 |
| 适用场景 | 前端/后端联调、快速迭代 | 一键体验、演示、部署 |

### 2.2 开发模式

前端与后端分别独立运行，前端由 Vite 提供热更新，请求跨域打到后端。

**启动步骤：**

```bash
# 1. 启动上游 agent（REST 8079 + gRPC 50051）
python -m engine.server

# 2. 启动 Go 代理后端（8081）
cd console/bff-go
go run ./cmd/server

# 3. 启动 Vite 前端开发服务器（5173，热更新）
cd console/web
corepack pnpm install   # 首次
corepack pnpm dev       # 打开 http://localhost:5173
```

此模式下页面来源是 `http://localhost:5173`，而前端 BackendSelector 会把 API 基址设为后端的**绝对地址**（切到 Go 后端即 `http://127.0.0.1:8081`），因此所有 `/v1/*` 请求都是**跨域**的，必须依赖后端的 CORS 中间件（见第 4 节）。

> Go 后端没有热重载，修改代码后需手动重新 `go run`。如需边改边跑，可配合 `air` 等工具。

### 2.3 生产模式

前端先构建为静态产物，由后端同源托管，浏览器从后端（8081）同时拿到页面与 API，**无跨域**。

在商业化产品模式下，Go 后端通常更偏向二进制交付、mTLS 保护上游 agent 链路、通过反向代理或 Ingress 对外暴露，并关闭开发期的临时参数与调试日志。

**启动步骤（推荐一键脚本）：**

```bash
# 一键启动 agent + Go 后端（自动补依赖、构建前端、预编译 Go 二进制）
./scripts/dev/dev-engine-console.sh

# 访问 http://127.0.0.1:8081 即可打开控制台
# 按 Ctrl+C 停止所有服务；或在另一终端执行 ./scripts/dev/dev-stop.sh
```

**手动方式：**

```bash
# 1. 构建前端产物到 console/web/dist
cd console/web
corepack pnpm install
corepack pnpm build

# 2. 启动 agent
python -m engine.server

# 3. 预编译并启动 Go 后端
cd console/bff-go
go build -o bin/backend-go ./cmd/server
./bin/backend-go
```

后端启动时若检测到 `../web/dist` 存在（由 `PRIVACY_CONSOLE_STATIC_DIR` 控制），会自动挂载 `/assets` 静态资源并注册 SPA 回退路由（非 `/api` 路由一律返回 `index.html`）；若目录不存在则打印日志并退化为「API 模式」，仅提供 `/v1/*`。

### 2.4 双协议切换模式

页面顶部 Backend Selector 仅在 REST 与 gRPC 两种上游协议间切换，二者统一由 Go BFF（`console/bff-go`）承接：

```bash
./scripts/dev/dev-engine-console.sh    # 启动 agent + Go BFF(8081) + Web 前端(5173)
./scripts/dev/dev-stop.sh               # 停止
```

打开 `http://127.0.0.1:8081` 后，通过 `?protocol=rest` 或 `?protocol=grpc` 切换上游协议。

---

## 3. 配置参考

所有配置通过环境变量加载，均有本地开发默认值，零配置即可运行。定义见 [internal/config/config.go](../internal/config/config.go)。

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `PRIVACY_AGENT_GRPC_HOST` | `127.0.0.1` | 上游 agent gRPC 主机 |
| `PRIVACY_AGENT_GRPC_PORT` | `50051` | 上游 agent gRPC 端口 |
| `PRIVACY_AGENT_API_KEY` | （空） | 可选 Bearer Token；agent 开启认证时必填，自动附加到 gRPC 元数据 |
| `PRIVACY_CONSOLE_HOST` | `127.0.0.1` | 本后端 HTTP 监听地址 |
| `PRIVACY_CONSOLE_PORT` | `8081` | 本后端 HTTP 监听端口 |
| `PRIVACY_CONSOLE_STATIC_DIR` | `../web/dist` | 前端构建产物目录；**显式设为空**则禁用 SPA 托管（纯 API 模式） |
| `PRIVACY_AGENT_TLS_ENABLED` | `false` | 是否对上游 gRPC 连接启用 TLS/mTLS |
| `PRIVACY_AGENT_TLS_CERT_FILE` | （空） | 客户端证书文件（PEM），mTLS 双向认证用 |
| `PRIVACY_AGENT_TLS_KEY_FILE` | （空） | 客户端私钥文件（PEM），须与证书同时提供 |
| `PRIVACY_AGENT_TLS_CA_FILE` | （空） | 校验服务端证书的 CA 文件（PEM），TLS 启用时必填 |
| `PRIVACY_AGENT_TLS_SERVER_NAME` | （空） | 服务端证书主机名覆盖值（如连 127.0.0.1 但证书为 localhost） |
| `PRIVACY_AGENT_TLS_INSECURE_SKIP_VERIFY` | `false` | 是否跳过服务端证书校验（**仅测试**，生产严禁） |

> 注意 `PRIVACY_CONSOLE_STATIC_DIR` 使用 `os.LookupEnv` 区分「未设置」与「设为空」：不设置 → 用默认值 `../web/dist`；显式设为空字符串 → 禁用静态托管。

**配置示例：**

```bash
# 指向远程 agent 的 gRPC 端口并启用认证
PRIVACY_AGENT_GRPC_HOST=10.0.0.5 \
PRIVACY_AGENT_GRPC_PORT=50051 \
PRIVACY_AGENT_API_KEY=your-key \
./bin/backend-go
```

```bash
# 禁用 UI 托管，仅作为纯 gRPC→REST 代理
PRIVACY_CONSOLE_STATIC_DIR= ./bin/backend-go
```

---

## 4. 跨域（CORS）解决方案

### 4.1 为什么会出现跨域

浏览器同源策略要求「协议 + 域名 + 端口」三者一致，否则为跨域。本控制台在以下三种场景触发跨域：

1. **开发模式**：页面在 `localhost:5173`（Vite），API 在 `127.0.0.1:8081`，端口不同；
2. **分离部署**：UI 与后端部署在不同域名/端口（如 nginx 单独托管 dist）。

### 4.2 方案一：同源部署（生产推荐）

生产模式下前端 `dist` 由后端直接托管，页面与 API 同源（都是 `127.0.0.1:8081`），**从根本上不产生跨域**，无需任何 CORS 配置。这是最简单、最安全的方式。

### 4.3 方案二：后端 CORS 中间件（开发默认）

后端内置宽松 CORS 中间件（[internal/handlers/handlers.go](../internal/handlers/handlers.go) 的 `corsMiddleware`），允许任意来源跨域，专为本地开发设计：

```go
func corsMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
        c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
        // OPTIONS 预检请求直接返回 204，不进入后续 handler
        if c.Request.Method == "OPTIONS" {
            c.AbortWithStatus(http.StatusNoContent)
            return
        }
        c.Next()
    }
}
```

浏览器发起的 `OPTIONS` 预检请求由该中间件拦截并返回 `204`，前端无需任何额外处理。

> 安全提示：`Access-Control-Allow-Origin: *` 仅适用于本地可信环境。若将后端暴露到不可信网络，应收紧为具体来源，或改用方案一/方案四。

### 4.4 方案三：Vite 开发代理（同源回退）

`vite.config.ts` 配置了 `/api` 代理（默认指向 Go BFF 8081）。在 Vite 开发模式下请求会被透明转发到 Go 后端，不触发 CORS：

```ts
// console/web/vite.config.ts
server: {
  port: 5173,
  proxy: {
    '/api': { target: 'http://127.0.0.1:8081', changeOrigin: true },
  },
}
```

当 API 基址为空（同源）时，请求 `http://localhost:5173/v1/*` 会被 Vite 透明转发到后端，不触发 CORS。此方案适合只想对接单一后端的纯前端开发。

### 4.5 方案四：反向代理（分离部署）

若 UI 与后端必须分域名部署，可用 nginx 等反向代理把 `/api` 转发到后端，对浏览器保持同源：

```nginx
server {
    listen 80;
    server_name console.example.com;

    # 静态托管前端产物
    location / {
        root /var/www/privacy-console/dist;
        try_files $uri $uri/ /index.html;   # SPA 路由回退
    }

    # API 反向代理到 Go 后端，保持同源，规避 CORS
    location /v1/ {
        proxy_pass http://127.0.0.1:8081/v1/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

### 4.6 方案选型小结

| 场景 | 推荐方案 | 是否跨域 |
|---|---|---|
| 生产一键部署 | 方案一 同源部署 | 否 |
| 本地前后端联调 | 方案二 后端 CORS 中间件 | 是（已自动处理） |
| 纯前端开发（单后端） | 方案三 Vite 代理 | 否 |
| UI 与后端分域名部署 | 方案四 反向代理 | 否 |

---

## 5. mTLS 双向认证（gRPC 传输安全）

默认情况下 Go 代理到上游 agent 的 gRPC 连接使用非安全传输（`insecure`），仅适合本地开发。生产环境应启用 mTLS（双向 TLS）：客户端校验服务端证书，服务端也校验客户端证书，实现传输加密 + 双向身份认证。

### 5.1 一键体验（自签名证书）

仓库提供了证书生成脚本与 mTLS 一键启动脚本，方便本地快速验证：

```bash
# 1. 生成一套自签名测试证书（CA + 服务端 + 客户端）到 console/bff-go/certs/
./scripts/dev/gen-certs.sh

# 2. 一键以 mTLS 模式启动 agent + Go 代理（证书缺失时会自动生成）
./scripts/dev/dev-engine-console.sh --mtls

# 3. 停止
./scripts/dev/dev-stop.sh
```

`gen-certs.sh` 基于 openssl 生成：

- `ca.crt` / `ca.key`：受信任根 CA
- `server.crt` / `server.key`：服务端证书（Python agent，SAN 含 localhost/127.0.0.1）
- `client.crt` / `client.key`：客户端证书（Go 代理，EKU=clientAuth）

> 生成的证书仅用于测试，已加入 `.gitignore`，不会提交到仓库。

### 5.2 手动配置

**agent 端（gRPC 服务端）** 启用 mTLS 并要求客户端证书：

```bash
PRIVACY_TLS_ENABLED=true \
PRIVACY_TLS_CERT_FILE=/path/server.crt \
PRIVACY_TLS_KEY_FILE=/path/server.key \
PRIVACY_TLS_CA_FILE=/path/ca.crt \
PRIVACY_TLS_CLIENT_AUTH=require \
python -m engine.server
```

**Go 代理端（gRPC 客户端）** 出示客户端证书并校验服务端：

```bash
PRIVACY_AGENT_TLS_ENABLED=true \
PRIVACY_AGENT_TLS_CERT_FILE=/path/client.crt \
PRIVACY_AGENT_TLS_KEY_FILE=/path/client.key \
PRIVACY_AGENT_TLS_CA_FILE=/path/ca.crt \
PRIVACY_AGENT_TLS_SERVER_NAME=localhost \
./bin/backend-go
```

### 5.3 握手流程与校验点

```mermaid
sequenceDiagram
    participant Go as Go 代理 (客户端)
    participant Agent as agent (服务端)
    Go->>Agent: TLS ClientHello
    Agent-->>Go: 服务端证书 (server.crt)
    Go->>Go: 用 ca.crt 校验服务端证书链 + 主机名
    Agent->>Go: 请求客户端证书 (require_client_auth)
    Go-->>Agent: 客户端证书 (client.crt)
    Agent->>Agent: 用 ca.crt 校验客户端证书
    Note over Go,Agent: 双向认证通过，建立加密通道
```

客户端凭证构造逻辑见 [internal/agent/client.go](../internal/agent/client.go) 的 `buildTransportCredentials`：

- 未启用 TLS → `insecure.NewCredentials()`（非安全，本地开发）
- 启用 TLS → 加载 CA 构造根证书池，强制最低 TLS 1.2；若配置了客户端证书/私钥则加载用于双向认证

### 5.4 常见问题

| 现象 | 原因 | 处理 |
|---|---|---|
| `PRIVACY_AGENT_TLS_CA_FILE is required` | 启用了 TLS 但未提供 CA | 设置 `PRIVACY_AGENT_TLS_CA_FILE` |
| `x509: certificate signed by unknown authority` | 客户端 CA 与服务端证书不匹配 | 确认两端使用同一 CA 签发 |
| `certificate is valid for localhost, not 127.0.0.1` | 证书 SAN 与连接地址不符 | 设置 `PRIVACY_AGENT_TLS_SERVER_NAME=localhost`，或在证书 SAN 加入对应 IP |
| `error reading server preface: EOF` | 服务端要求客户端证书但客户端未提供 | 配置 `PRIVACY_AGENT_TLS_CERT_FILE` / `_KEY_FILE` |
| `tls: failed to find any PEM data` | 证书/私钥路径错误或格式非 PEM | 核对文件路径与 PEM 格式 |

---

## 6. 启动与停止

### 6.1 一键脚本

| 脚本 | 作用 |
|---|---|
| `./scripts/dev/dev-engine-console.sh` | 启动 agent + Go BFF + Vite 前端（自动补依赖、构建前端、预编译二进制），Ctrl+C 停止 |
| `./scripts/dev/dev-engine-console.sh --mtls` | 以 **mTLS 双向认证模式** 启动 agent + Go BFF + Vite 前端 |
| `./scripts/dev/dev-stop.sh` | 安全停止本地所有开发进程（含 Agent、BFF、Vite） |
| `./scripts/prod/prod-bff-agent.sh` | 生产模式启动 agent + Go BFF（托管静态前端 dist 页面） |
| `./scripts/prod/prod-stop.sh` | 停止生产进程 |

启动脚本每次运行都会自动补齐依赖并构建前端（Go 二进制每次均重新编译）；如需强制重建前端，删除 `console/web/dist` 后重新运行 `./scripts/dev/dev-engine-console.sh` 即可。

### 6.2 手动启停

```bash
# 开发（改代码后手动重启）
cd console/bff-go && go run ./cmd/server

# 生产（预编译二进制）
cd console/bff-go
go build -o bin/backend-go ./cmd/server
./bin/backend-go

# 停止：Ctrl+C，或 kill 对应进程
```

### 6.3 优雅关闭

后端监听 `SIGINT`（Ctrl+C）与 `SIGTERM`（kill / 容器停止），收到信号后：

1. 停止接收新连接；
2. 等待所有活跃请求处理完毕，**最长等待 5 秒**；
3. 关闭 gRPC 客户端连接，释放 TCP 与 HTTP/2 资源。

实现见 [cmd/server/main.go](../cmd/server/main.go)。该机制保证容器编排（K8s/Docker）停止服务时不丢请求。

### 6.4 PID 管理

一键脚本会把进程 PID 写入根目录 `.pids/`（如 `agent-go.pid`、`console-go.pid`），`dev-stop.sh` 据此精确停止，避免误杀其他进程。

---

## 7. 健康检查与验证

```bash
# 后端自身 + 上游 agent gRPC 连通性
curl http://127.0.0.1:8081/health

# agent REST 直连健康检查
curl http://127.0.0.1:8079/health
```

`/health` 返回 `backend`（本后端状态）、`agent`（上游 gRPC 连通性）、`via`/`protocol`（后端身份标识，Go 后端为 `go-grpc`/`gRPC`）等字段，详见 [api.md](api.md)。

**Go 测试：**

```bash
cd console/bff-go

# 单元测试（无需 agent）
go test -short ./...

# 全部测试（集成测试需 agent 运行在 127.0.0.1:50051，否则自动跳过）
go test ./...
```

---

## 8. 常见问题排查

| 现象 | 可能原因 | 处理 |
|---|---|---|
| 前端报 CORS 错误 | 后端未启动 / CORS 中间件被移除 | 确认 8081 在监听；检查 `handlers.go` 的 `corsMiddleware` |
| 启动即退出 `failed to create agent client` | agent gRPC 未启动或地址不符 | 启动 agent；核对 `PRIVACY_AGENT_GRPC_HOST/PORT` |
| gRPC 调用返回认证错误 | agent 开启了认证 | 设置 `PRIVACY_AGENT_API_KEY` |
| 访问 `/` 返回 404 | `web/dist` 未构建 | `cd console/web && corepack pnpm build` 后重启 |
| 静态托管未生效 | `PRIVACY_CONSOLE_STATIC_DIR` 被设为空 | 去掉该变量或指向正确的 dist 目录 |
| 端口被占用 `http server failed` | 8081 已被其他进程使用 | 改 `PRIVACY_CONSOLE_PORT`，或 `./scripts/dev/dev-stop.sh` 清理残留 |
| 部分端点 404 / 不支持 | 该端点在 gRPC 侧未实现，或路径拼写错误 | 切换到 REST 上游协议（`?protocol=rest`）后重试；若仍不支持，则该端点未在 gRPC 侧暴露 |
| 改后端代码不生效 | Go 无热重载 | 重新 `go run` 或重新编译二进制 |

---

## 9. 生产加固建议

- 将 CORS 的 `Access-Control-Allow-Origin: *` 收紧为实际来源白名单；
- 启用 gRPC mTLS（见第 5 节）：使用正式 CA 签发的证书，设置 `PRIVACY_TLS_CLIENT_AUTH=require`，切勿在生产使用 `PRIVACY_AGENT_TLS_INSECURE_SKIP_VERIFY`；
- 通过 `PRIVACY_AGENT_API_KEY` 启用对 agent 的认证；
- 如需对外提供 HTTPS，在前置反向代理（nginx/网关）终结证书；
- 利用内置的优雅关闭机制，配合 K8s `preStop` / `terminationGracePeriodSeconds` 平滑下线。
