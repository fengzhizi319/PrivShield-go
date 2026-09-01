# Go + Gin 技术栈说明 / Go + Gin Technology Stack

## 1. 技术简介 / Introduction

Go（Golang）是 Google 开发的开源编译型语言，以高并发、低延迟和简洁语法著称。
Go (Golang) is an open-source compiled language developed by Google, known for high concurrency, low latency, and concise syntax.

Gin 是 Go 生态中最流行的 HTTP Web 框架，以高性能和中间件机制闻名。
Gin is the most popular HTTP web framework in the Go ecosystem, known for high performance and middleware mechanism.

核心特性 / Core Features：
- **编译型语言（Compiled）**：Go 编译为静态二进制，无运行时依赖，部署极简。
- **goroutine 并发（Goroutine Concurrency）**：轻量级协程，轻松处理数万并发连接。
- **强类型 + 类型推断（Strong Typing + Inference）**：编译期捕获错误，减少运行时异常。
- **Gin 路由性能（Gin Routing Performance）**：基于 Radix Tree 的路由匹配，零分配设计。
- **中间件链（Middleware Chain）**：Gin 的 HandlerFunc 链式调用，支持日志、鉴权、恢复等。
- **标准库丰富（Rich Stdlib）**：net/http、encoding/json、crypto/tls 等开箱即用。

本项目使用版本 / Version Used：`Go 1.21+`、`Gin v1.9+`

## 2. 在本项目中的用法 / Usage in This Project

### 2.1 项目结构 / Project Structure

本项目采用 **Go 1.21+ 多模块工作区（Go Workspace, `go.work`）** 架构，将控制台划分为 4 个独立微服务与 1 个共享公共包：

```
项目根目录/
├── go.work                     # Go 工作区定义 (包含 pkg, console/bff-go, services/*)
│
├── pkg/                        # 共享基础公共库 (github.com/fengzhizi319/PrivShield-go/pkg)
│   ├── store/                  # 存储接口 (TaskStore, DataSourceStore, AuditStore)
│   │   ├── memory/             # 内存安全存储实现 (RWMutex, 切片边界保护)
│   │   └── sqlite/             # SQLite 纯 Go WAL 持久化驱动 (modernc.org/sqlite)
│   ├── middleware/             # 统一 Gin 中间件 (Auth, CORS, RequestID, Recovery, SecurityHeaders)
│   ├── metrics/                # Prometheus 收集器与 Gin 自动化拦截中间件
│   ├── agent/                  # 上游 Agent HTTP 客户端 (带熔断器与 64MB 保护)
│   ├── config/                 # 统一环境变量解析与 slog 结构化日志器
│   └── validation/             # 白名单校验、端口范围与抗碰撞 ID 生成
│
├── services/
│   ├── service-hub/            # 调度中枢微服务 (HTTP :8082 + gRPC :50052)
│   │   ├── cmd/server/main.go  # 服务启动入口 (双协议监听 + 优雅关停)
│   │   ├── internal/handlers/  # 6 阶段流水线编排
│   │   ├── internal/grpcserver/# gRPC 服务实现
│   │   └── proto/servicehub.proto
│   │
│   ├── datasource-mgr/         # 数据源管理微服务 (HTTP :8083)
│   │   ├── cmd/server/main.go  # 服务入口
│   │   └── internal/handlers/  # 数据源 CRUD、连通性探测
│   │
│   └── audit-log/              # 脱敏审计日志微服务 (HTTP :8084)
│       ├── cmd/server/main.go  # 服务入口
│       └── internal/handlers/  # 审计存证、SHA-256 防篡改校验
│
└── console/
    └── bff-go/                 # gRPC 代理网关 (HTTP :8081)
        ├── cmd/server/main.go  # 程序入口 (代理转发 + 静态 SPA 托管)
        ├── internal/agent/client.go # gRPC 客户端 (HTTP/2 多路复用)
        ├── internal/handlers/  # HTTP 路由处理器
        ├── internal/mapper/  # REST path → gRPC method 映射调度表
        └── proto/            # Protobuf 生成代码
```

### 2.2 服务启动 / Server Startup

文件 / File：`console/bff-go/cmd/server/main.go`

```go
// 加载环境变量配置 / Load environment variable config
cfg := config.Load()
// 创建 gRPC 客户端（支持 mTLS）/ Create gRPC client (with mTLS support)
agentClient, err := agent.New(cfg)
// 创建 HTTP 处理器 / Create HTTP handlers
server := handlers.New(agentClient, cfg)
// 注册 Gin 路由 / Register Gin routes
router := gin.Default()
server.RegisterRoutes(router)
// 启动 HTTP 服务 / Start HTTP server
router.Run(cfg.ConsoleAddress())
```

### 2.3 REST→gRPC 路由映射 / REST→gRPC Route Mapping

```go
// mapper.go 中的调度表 / Dispatch table in mapper.go
type Handler func(ctx context.Context, client pb.PrivacyServiceClient, body []byte) (any, error)

type Mapper struct {
    routes map[string]Handler  // "POST /v1/privacy/mask" → handleMask
}

// Dispatch 根据 method+path 查找并执行对应 handler
// Dispatch looks up and executes handler by method+path
func (m *Mapper) Dispatch(ctx context.Context, client pb.PrivacyServiceClient,
    method, path string, body []byte) (any, bool, error)
```

### 2.4 gRPC 客户端（含 mTLS）/ gRPC Client (with mTLS)

```go
// agent/client.go
func buildTransportCredentials(cfg *config.Config) (credentials.TransportCredentials, error) {
    // 加载 CA + 客户端证书 + 私钥 / Load CA + client cert + key
    cert, _ := tls.LoadX509KeyPair(certFile, keyFile)
    pool := x509.NewCertPool()
    pool.AppendCertsFromPEM(caPEM)
    return credentials.NewTLS(&tls.Config{
        Certificates: []tls.Certificate{cert},
        RootCAs:      pool,
        ServerName:   serverName,
    }), nil
}
```

### 2.5 安全中间件 / Security Middleware

```go
// handlers.go 中的 API Key 鉴权 + 滑动窗口限流
// API Key auth + sliding window rate limiting in handlers.go
func (s *Server) authMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        key := c.GetHeader("X-API-Key")
        if !s.validKeys[key] {
            c.AbortWithStatusJSON(401, gin.H{"detail": "invalid api key"})
            return
        }
        c.Next()
    }
}
```

### 2.6 静态文件 SPA 托管 / Static File SPA Hosting

```go
// Go 后端直接托管前端 dist/ 构建产物
// Go backend directly serves frontend dist/ build artifacts
router.Static("/assets", "./web/dist/assets")
router.NoRoute(func(c *gin.Context) {
    c.File("./web/dist/index.html")  // SPA 回退 / SPA fallback
})
```

### 2.7 中间件链详解 / Middleware Chain Details

Gin 的中间件基于 `HandlerFunc` 链式调用，请求按注册顺序依次通过各中间件：
Gin's middleware is based on `HandlerFunc` chain calls, requests pass through each middleware in registration order:

```
请求进入 / Request In
    │
    ▼
[1] corsMiddleware()         ← 跨域处理，OPTIONS 直接返回 204 / CORS, OPTIONS returns 204
    │
    ▼
[2] securityMiddleware()     ← API Key 鉴权 + 滑动窗口限流 / API Key auth + sliding window rate limit
    │
    ▼
[3] Handler (Health/Proxy/...)  ← 业务逻辑处理 / Business logic
    │
    ▼
响应返回 / Response Out
```

#### CORS 中间件 / CORS Middleware

```go
func corsMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Writer.Header().Set("Access-Control-Allow-Origin", "*")       // 允许任意来源 / Allow any origin
        c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
        if c.Request.Method == "OPTIONS" {
            c.AbortWithStatus(http.StatusNoContent)  // 预检请求直接返回 204 / Preflight returns 204
            return
        }
        c.Next()  // 非 OPTIONS 继续下一个中间件 / Non-OPTIONS continues to next middleware
    }
}
```

设计说明：本控制台为本地工具，不依赖 cookie/凭证，故仅设置 `Allow-Origin: *` 而不携带
`Allow-Credentials`，避免“任意来源 + 凭证”组合带来的跨域凭证泄露风险。
Design note: Local tool without cookie/credential dependency, so only `Allow-Origin: *` without
`Allow-Credentials` to avoid cross-origin credential leakage risk.

#### 安全中间件（API Key + 限流）/ Security Middleware (API Key + Rate Limit)

```go
func securityMiddleware(apiKey string, rateLimit int) gin.HandlerFunc {
    var mu sync.Mutex                    // 保护进程内请求计数 map / Protect in-process hit counter
    hits := make(map[string][]time.Time) // 每个 IP 的请求时间戳列表 / Per-IP request timestamps
    return func(c *gin.Context) {
        path := c.Request.URL.Path
        // 仅对 /api/* 生效；/api/health 豁免 / Only for /api/*; /api/health exempted
        if !strings.HasPrefix(path, "/api/") || path == "/api/health" {
            c.Next()
            return
        }
        // API Key 鉴权（配置了才校验）/ API Key auth (only when configured)
        if apiKey != "" {
            if extractBearer(c.GetHeader("Authorization")) != apiKey {
                c.AbortWithStatusJSON(401, gin.H{"detail": "Unauthorized"})
                return
            }
        }
        // 滑动窗口限流：60s 内超过阈值返回 429 / Sliding window: exceed threshold in 60s returns 429
        if rateLimit > 0 {
            ip := c.ClientIP()
            now := time.Now()
            cutoff := now.Add(-60 * time.Second)
            mu.Lock()
            // 就地过滤 60s 窗口外的旧记录 / Filter out records outside 60s window in-place
            window := hits[ip]
            kept := window[:0]
            for _, t := range window {
                if t.After(cutoff) { kept = append(kept, t) }
            }
            if len(kept) >= rateLimit {
                hits[ip] = kept
                mu.Unlock()
                c.AbortWithStatusJSON(429, gin.H{"detail": "Too many requests"})
                return
            }
            hits[ip] = append(kept, now)
            mu.Unlock()
        }
        c.Next()
    }
}
```

### 2.8 错误处理模式 / Error Handling Patterns

本项目采用统一的错误分类与响应策略：
This project uses unified error classification and response strategy:

```go
// 错误分类策略 / Error classification strategy：
//   - 连接类错误（connection refused / dns / timeout / Unavailable）→ 502 Bad Gateway
//   - 参数/业务错误 → 400 Bad Request
//   - 未匹配的 gRPC 路径 → 回退到 REST 透明代理 / Fallback to REST transparent proxy

// isUnavailable 启发式判断上游是否不可达 / Heuristic check if upstream is unreachable
func isUnavailable(err error) bool {
    msg := err.Error()
    return containsAny(msg, []string{"connection refused", "dns", "timeout", "Unavailable"})
}

// writeUpstreamError 公共错误处理入口 / Common error handling entry
func (s *Server) writeUpstreamError(c *gin.Context, err error) {
    status := http.StatusBadRequest
    if isUnavailable(err) {
        status = http.StatusBadGateway  // 上游不可达返回 502 / Upstream unreachable returns 502
    }
    c.JSON(status, gin.H{"detail": err.Error(), "status": status})
}
```

错误响应统一格式 / Unified error response format：
```json
{
  "detail": "具体错误信息 / Specific error message",
  "status": 400
}
```

### 2.9 请求生命周期 / Request Lifecycle

以 `POST /api/proxy` 为例的完整请求处理流程：
Complete request processing flow using `POST /api/proxy` as example:

```
1. 前端发送 HTTP POST /api/proxy {method, path, body}
   Frontend sends HTTP POST /api/proxy {method, path, body}
        │
2. corsMiddleware: 设置 CORS 头，非 OPTIONS 继续
   corsMiddleware: Set CORS headers, non-OPTIONS continues
        │
3. securityMiddleware: 校验 API Key + 限流
   securityMiddleware: Verify API Key + rate limit
        │
4. Proxy handler: ShouldBindJSON 解析请求体
   Proxy handler: ShouldBindJSON parse request body
        │
5. mapper.Dispatch: O(1) 哈希查找 path 对应的 gRPC handler
   mapper.Dispatch: O(1) hash lookup for path's gRPC handler
        │
6. gRPC handler: 解析 body → 构造 protobuf → 调用 RPC → 转换响应
   gRPC handler: Parse body → build protobuf → call RPC → convert response
        │
7. 返回 ProxyResponse {status, duration_ms, data, via, protocol}
   Return ProxyResponse {status, duration_ms, data, via, protocol}
```

### 2.10 关键设计决策 / Key Design Decisions

| 决策 / Decision | 原因 / Reason |
|---|---|
| Go 替代 Python 作为第二后端 / Go as second backend | 展示 gRPC 原生调用路径，对比 REST 代理 / Demonstrate native gRPC path vs REST proxy |
| Gin 而非 net/http / Gin over net/http | 路由分组、中间件链、JSON 绑定更便捷 / Route groups, middleware chain, JSON binding |
| internal/ 包布局 / internal/ package layout | Go 编译器强制包私有性，防止外部误引 / Compiler enforces package privacy |
| Protobuf 桩代码提交到仓库 / Commit generated stubs | 避免 CI 依赖 protoc 安装 / Avoid CI dependency on protoc installation |
| 单一二进制部署 / Single binary deployment | 无运行时依赖，容器镜像极小 / No runtime deps, minimal container image |
| 进程内滑动窗口限流 / In-process sliding window | 单实例部署无需 Redis，简化架构 / Single instance needs no Redis |
| gRPC 失败回退 REST / gRPC fallback to REST | 部分端点未实现 gRPC（如 dynclassification）/ Some endpoints lack gRPC (e.g. dynclassification) |
| 健康检查豁免鉴权 / Health check exempt from auth | 便于 K8s/负载均衡器探测 / Facilitate K8s/load balancer probing |

## 3. Go 并发模型详解 / Go Concurrency Model Details

### 3.1 Goroutine 与请求处理 / Goroutine & Request Handling

Go 的并发模型基于 CSP（Communicating Sequential Processes）：

```text
┌─────────────────────────────────────────────────────────────┐
│  Gin HTTP Server                                             │
│  ┌─────────────────────────────────────────────────────┐ │
│  │  主 goroutine: 监听端口 / Main goroutine: listen port  │ │
│  │  net.Listen(":8081")                                  │ │
│  └──────────────────────────┬──────────────────────────┘ │
│                             │ 每个连接 spawn goroutine       │
│              ┌────────────┼────────────┐              │
│              ▼              ▼              ▼              │
│  ┌────────────┐ ┌────────────┐ ┌────────────┐  │
│  │ goroutine #1 │ │ goroutine #2 │ │ goroutine #N │  │
│  │ 请求 /api/.. │ │ 请求 /api/.. │ │ 请求 /api/.. │  │
│  │ ~2KB 栈内存 │ │ ~2KB 栈内存 │ │ ~2KB 栈内存 │  │
│  └────────────┘ └────────────┘ └────────────┘  │
│  优势：轻松支持数万并发连接 / Easily support 10K+ conns  │
└─────────────────────────────────────────────────────────────┘
```

**Goroutine vs 线程对比 / Goroutine vs Thread**：

| 特性 / Feature | Goroutine | OS 线程 / OS Thread |
|---|---|---|
| 初始栈大小 / Initial stack | ~2KB（动态增长）/ Dynamic | ~1MB（固定）/ Fixed |
| 创建成本 / Creation cost | ~0.3μs | ~10-100μs |
| 调度方式 / Scheduling | Go runtime 协作式 / Cooperative | OS 抢占式 / Preemptive |
| 最大数量 / Max count | 数十万 / Hundreds of thousands | 数千 / Thousands |
| 通信方式 / Communication | channel（类型安全）/ Type-safe | 共享内存 + 锁 / Shared mem + locks |

### 3.2 Context 传播与取消 / Context Propagation & Cancellation

```go
// Gin 中 context.Context 的传播路径 / Context propagation path in Gin
func (s *Server) handleProxy(c *gin.Context) {
    // c.Request.Context() 携带请求级 context
    // c.Request.Context() carries request-level context
    ctx := c.Request.Context()

    // 传递给 gRPC 调用：客户端断开时自动取消
    // Pass to gRPC call: auto-cancel when client disconnects
    result, err := s.mapper.Dispatch(ctx, s.client, method, path, body)
    if err != nil {
        if ctx.Err() == context.Canceled {
            // 客户端主动断开，无需返回错误
            // Client disconnected, no need to return error
            return
        }
        s.writeUpstreamError(c, err)
        return
    }
    c.JSON(200, result)
}

// gRPC 调用中的 context 使用 / Context usage in gRPC calls
func handleMask(ctx context.Context, client pb.PrivacyServiceClient, body []byte) (any, error) {
    // 设置超时：防止上游无响应时 goroutine 泄漏
    // Set timeout: prevent goroutine leak when upstream unresponsive
    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()  // 确保资源释放 / Ensure resource release

    resp, err := client.Mask(ctx, &pb.MaskRequest{...})
    if err != nil {
        return nil, err  // context 超时会返回 DeadlineExceeded
    }
    return resp, nil
}
```

**Context 层次结构 / Context Hierarchy**：

```text
context.Background()                    ← 根 context / Root context
    │
    ▼  http.Server
request.Context()                       ← 请求级：客户端断开时取消 / Request: cancel on disconnect
    │
    ▼  context.WithTimeout(ctx, 30s)
gRPC call context                       ← 调用级：超时自动取消 / Call: auto-cancel on timeout
    │
    ▼  context.WithValue(ctx, key, val)
携带元数据（trace ID 等）/ Carry metadata (trace ID etc.)
```

### 3.3 并发安全模式 / Concurrency Safety Patterns

```go
// 本项目中的并发安全实践 / Concurrency safety practices in this project

// 1. sync.Mutex 保护共享状态 / Protect shared state with sync.Mutex
func securityMiddleware(apiKey string, rateLimit int) gin.HandlerFunc {
    var mu sync.Mutex                    // 保护 hits map / Protect hits map
    hits := make(map[string][]time.Time)
    return func(c *gin.Context) {
        mu.Lock()
        defer mu.Unlock()  // 确保解锁 / Ensure unlock
        // 操作 hits map / Operate on hits map
    }
}

// 2. 负载均衡测试中的并发探测 / Concurrent probing in LB test
func runLBTest(backends []string, numRequests int) []Result {
    results := make([]Result, numRequests)
    var wg sync.WaitGroup
    sem := make(chan struct{}, 10)  // 信号量：限制并发数为 10 / Semaphore: limit concurrency to 10

    for i := 0; i < numRequests; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            sem <- struct{}{}        // 获取信号量 / Acquire semaphore
            defer func() { <-sem }() // 释放信号量 / Release semaphore
            results[idx] = probe(backends[idx%len(backends)])
        }(i)
    }
    wg.Wait()  // 等待所有 goroutine 完成 / Wait for all goroutines
    return results
}
```

## 4. 优雅关闭与信号处理 / Graceful Shutdown & Signal Handling

### 4.1 优雅关闭实现 / Graceful Shutdown Implementation

```go
// cmd/server/main.go 中的优雅关闭模式 / Graceful shutdown pattern in main.go
func main() {
    cfg := config.Load()
    router := setupRouter(cfg)

    srv := &http.Server{
        Addr:    cfg.ConsoleAddress(),
        Handler: router,
    }

    // 在 goroutine 中启动服务 / Start server in goroutine
    go func() {
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("listen: %s\n", err)
        }
    }()

    // 等待中断信号 / Wait for interrupt signal
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit  // 阻塞直到收到信号 / Block until signal received
    log.Println("Shutting down server...")

    // 给活跃请求 5s 时间完成 / Give active requests 5s to complete
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    if err := srv.Shutdown(ctx); err != nil {
        log.Fatal("Server forced to shutdown:", err)
    }
    log.Println("Server exiting")
}
```

### 4.2 关闭流程 / Shutdown Flow

```text
SIGINT/SIGTERM 信号 / Signal received
        │
        ▼
停止接受新连接 / Stop accepting new connections
        │
        ▼
等待活跃请求完成（最多 5s）/ Wait for active requests (max 5s)
        │
        ├── 所有请求完成 / All requests done → 正常退出 / Normal exit
        └── 超时 / Timeout → 强制关闭 / Force shutdown
        │
        ▼
关闭 gRPC 客户端连接 / Close gRPC client connections
        │
        ▼
进程退出 / Process exit
```

## 5. Go 模块与依赖管理 / Go Modules & Dependency Management

### 5.1 go.mod 解析 / go.mod Analysis

```go
// console/bff-go/go.mod
module privacy-console-go          // 模块路径 / Module path

go 1.21                            // 最低 Go 版本 / Minimum Go version

require (
    github.com/gin-gonic/gin v1.9.1           // HTTP 框架 / HTTP framework
    google.golang.org/grpc v1.62.0            // gRPC 客户端 / gRPC client
    google.golang.org/protobuf v1.33.0        // Protobuf 运行时 / Protobuf runtime
)

// 间接依赖（由 go mod tidy 自动管理）/ Indirect deps (managed by go mod tidy)
require (
    github.com/bytedance/sonic v1.9.1 // indirect  // JSON 加速 / JSON acceleration
    github.com/golang/protobuf v1.5.3 // indirect  // 兼容层 / Compat layer
    golang.org/x/net v0.22.0          // indirect  // HTTP/2 支持 / HTTP/2 support
)
```

### 5.2 依赖管理命令 / Dependency Management Commands

```bash
# 添加新依赖 / Add new dependency
go get github.com/gin-gonic/gin@v1.9.1

# 清理未使用依赖 / Clean unused dependencies
go mod tidy

# 查看依赖图 / View dependency graph
go mod graph | head -20

# 验证依赖完整性 / Verify dependency integrity
go mod verify

# 更新特定依赖 / Update specific dependency
go get -u google.golang.org/grpc@latest

# 查看可用更新 / Check available updates
go list -u -m all
```

### 5.3 Go 依赖解析策略 / Go Dependency Resolution Strategy

```text
┌─────────────────────────────────────────────────────────────┐
│  Go 模块解析顺序 / Go module resolution order               │
│                                                             │
│  1. go.mod 中的 require 指令 / require directives in go.mod │
│  2. 最小版本选择 (MVS) / Minimum Version Selection           │
│     - 选择所有依赖要求的最高最小版本                    │
│     - Select highest minimum version required by all deps    │
│  3. go.sum 校验和验证 / go.sum checksum verification       │
│     - 确保依赖未被篡改 / Ensure deps not tampered          │
│  4. 本地缓存 $GOPATH/pkg/mod / Local cache                 │
│  5. 远程下载 proxy.golang.org / Remote download             │
└─────────────────────────────────────────────────────────────┘
```

**MVS vs NPM/Yarn 对比 / MVS vs NPM/Yarn**：

| 特性 / Feature | Go MVS | NPM/Yarn |
|---|---|---|
| 版本选择 / Version selection | 最小满足版本 / Minimum satisfying | 最新兼容版本 / Latest compatible |
| 锁文件 / Lock file | go.sum（校验和）/ Checksums | package-lock.json（完整树）/ Full tree |
| 依赖冲突 / Dependency conflict | 编译错误 / Compile error | 运行时可能出问题 / Runtime issues |
| 可重现性 / Reproducibility | 极高 / Very high | 高 / High |
| 依赖嵌套 / Nested deps | 扁平化 / Flattened | node_modules 嵌套 / Nested |

## 6. 测试模式 / Testing Patterns

### 6.1 Go 测试约定 / Go Testing Conventions

```go
// internal/agent/client_test.go
package agent

import (
    "context"
    "testing"
    "time"
)

// 测试函数命名：Test + 函数名 / Test function naming: Test + function name
func TestNewClientInsecure(t *testing.T) {
    cfg := &config.Config{
        AgentAddr: "localhost:50051",
        TLSEnabled: false,
    }
    client, err := New(cfg)
    if err != nil {
        t.Fatalf("expected no error, got %v", err)
    }
    if client == nil {
        t.Fatal("expected non-nil client")
    }
}

// 表驱动测试 / Table-driven tests
func TestIsUnavailable(t *testing.T) {
    tests := []struct {
        name     string
        errMsg   string
        expected bool
    }{
        {"connection refused", "connection refused", true},
        {"dns error", "no such host", true},
        {"timeout", "context deadline exceeded", true},
        {"business error", "invalid field name", false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := fmt.Errorf(tt.errMsg)
            if got := isUnavailable(err); got != tt.expected {
                t.Errorf("isUnavailable(%q) = %v, want %v", tt.errMsg, got, tt.expected)
            }
        })
    }
}
```

### 6.2 测试运行 / Running Tests

```bash
# 运行所有测试 / Run all tests
cd console/bff-go && go test ./...

# 运行特定包测试 / Run specific package tests
go test ./internal/mapper/ -v

# 运行匹配的测试 / Run matching tests
go test ./... -run TestIsUnavailable -v

# 带覆盖率 / With coverage
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out

# 竞态检测 / Race detection
go test ./... -race
```

## 7. 构建与部署 / Build & Deployment

### 7.1 Makefile 解析 / Makefile Analysis

```makefile
# console/bff-go/Makefile
BINARY := privacy-console-go
VERSION := $(shell git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build run test clean proto

build:                          # 编译二进制 / Compile binary
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/server

run: build                      # 编译并运行 / Build and run
	./bin/$(BINARY)

test:                           # 运行测试 / Run tests
	go test ./... -v -race

clean:                          # 清理构建产物 / Clean build artifacts
	rm -rf bin/

proto:                          # 重新生成 protobuf 代码 / Regenerate protobuf code
	protoc --go_out=. --go-grpc_out=. proto/privacy.proto
```

### 7.2 单二进制部署优势 / Single Binary Deployment Benefits

```text
Go 部署 vs Python 部署 / Go deployment vs Python deployment:

┌─────────────────────────────────────────────────────────────┐
│  Go 部署 / Go deployment                                     │
│  - 单个二进制文件 (~15MB) / Single binary (~15MB)          │
│  - 无运行时依赖 / No runtime dependencies                  │
│  - 容器镜像可从 scratch 构建 / Container from scratch      │
│  - 启动时间 < 10ms / Startup < 10ms                        │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│  Python 部署 / Python deployment                             │
│  - Python 解释器 + 依赖包 (~200MB+) / Interpreter + deps   │
│  - 需要 pip install / Requires pip install                  │
│  - 容器镜像基于 python:3.x-slim / Based on python image   │
│  - 启动时间 ~500ms-2s / Startup ~500ms-2s                  │
└─────────────────────────────────────────────────────────────┘
```

## 8. 中间件机制详解 / Middleware Mechanism Details

### 8.1 Gin 中间件模型 / Gin Middleware Model

Gin 中间件是 `gin.HandlerFunc` 类型的函数，通过 `c.Next()` 控制执行流：
Gin middleware is a `gin.HandlerFunc` function, controlling flow via `c.Next()`:

```go
// 中间件执行顺序 / Middleware execution order
// r.Use(A, B, C) → A前 → B前 → C前 → Handler → C后 → B后 → A后

func TimingMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()       // 前置逻辑 / Pre-processing
        c.Next()                  // 执行后续处理器 / Execute next handlers
        elapsed := time.Since(start)  // 后置逻辑 / Post-processing
        c.Header("X-Elapsed", elapsed.String())
    }
}
```

### 8.2 本项目中间件实现 / Middleware in This Project

```go
// 恢复中间件：捕获 panic 防止服务崩溃 / Recovery: catch panic, prevent crash
r.Use(gin.Recovery())

// 日志中间件：记录请求方法、路径、状态码、耗时
// Logger: record method, path, status, duration
r.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
    return fmt.Sprintf("%s | %3d | %13v | %s | %s\n",
        param.TimeStamp.Format("2006/01/02 - 15:04:05"),
        param.StatusCode,
        param.Latency,
        param.ClientIP,
        param.Path,
    )
}))

// CORS 中间件：允许前端跨域 / CORS: allow frontend cross-origin
r.Use(func(c *gin.Context) {
    c.Header("Access-Control-Allow-Origin", "*")
    c.Header("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
    c.Header("Access-Control-Allow-Headers", "Content-Type,Authorization")
    if c.Request.Method == "OPTIONS" {
        c.AbortWithStatus(204)
        return
    }
    c.Next()
})
```

### 8.3 中间件链控制 / Middleware Chain Control

| 方法 / Method | 作用 / Effect | 场景 / Scenario |
|---|---|---|
| `c.Next()` | 继续执行下一个处理器 / Continue to next | 正常流程 / Normal flow |
| `c.Abort()` | 停止链但不写响应 / Stop chain, no response | 静默拒绝 / Silent reject |
| `c.AbortWithStatus(code)` | 停止并写状态码 / Stop with status | 401/403 拒绝 / Auth rejection |
| `c.AbortWithStatusJSON(...)` | 停止并写 JSON / Stop with JSON | 错误响应 / Error response |

## 9. Context 上下文管理 / Context Management

### 9.1 gin.Context 结构 / gin.Context Structure

```go
// gin.Context 是每个请求的核心对象 / gin.Context is the core per-request object
type Context struct {
    Request   *http.Request   // 原始 HTTP 请求 / Original HTTP request
    Writer    ResponseWriter  // 响应写入器 / Response writer
    Params    Params          // URL 路径参数 / URL path params
    Keys      map[string]any  // 中间件共享数据 / Middleware shared data
    // ... 内部字段 / Internal fields
}
```

### 9.2 请求上下文数据传递 / Request Context Data Passing

```go
// 中间件中设置值 / Set value in middleware
func AuthMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        token := c.GetHeader("Authorization")
        if token == "" {
            c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
            return
        }
        c.Set("user_token", token)  // 存入上下文 / Store in context
        c.Next()
    }
}

// 处理器中读取值 / Read value in handler
func handleProxy(c *gin.Context) {
    token, _ := c.Get("user_token")  // 从上下文读取 / Read from context
    // ... 使用 token / Use token
}
```

### 9.3 Context 与 Go 标准 context 的关系 / Context & Go stdlib context

```go
// gin.Context 实现了 context.Context 接口
// gin.Context implements context.Context interface
var _ context.Context = (*gin.Context)(nil)

// 可以直接传递给需要 context 的函数 / Can pass to functions needing context
func (m *Mapper) Dispatch(c *gin.Context, ...) {
    ctx := c.Request.Context()  // 获取标准 context / Get stdlib context
    result, err := m.client.Call(ctx, req)  // 传递给 gRPC / Pass to gRPC
}

// 超时控制 / Timeout control
ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
defer cancel()
result, err := m.client.Call(ctx, req)
```

## 10. 性能优化与调优 / Performance Optimization & Tuning

### 10.1 Gin 性能设计 / Gin Performance Design

| 设计 / Design | 实现 / Implementation | 效果 / Effect |
|---|---|---|
| 路由基数树 / Radix tree routing | httprouter 变体 / httprouter variant | O(k) 路由查找 / O(k) lookup |
| Context 对象池 / Context pooling | `sync.Pool` 复用 / Reuse via `sync.Pool` | 减少 GC 压力 / Less GC pressure |
| 零分配路由 / Zero-alloc routing | 参数存储在固定数组 / Params in fixed array | 无堆分配 / No heap alloc |
| 中间件内联 / Middleware inline | 函数指针链 / Function pointer chain | 无反射开销 / No reflection |

### 10.2 本项目性能实践 / Performance Practices

```go
// 1. 复用 gRPC 客户端连接（单例）/ Reuse gRPC client connection (singleton)
var agentClient *agent.Client  // 全局单例 / Global singleton

// 2. 路由分发表 O(1) 查找 / Route dispatch table O(1) lookup
m.handlers = map[string]Handler{...}  // 哈希表 / Hash map

// 3. 流式读取请求体（避免大内存分配）/ Stream request body (avoid large alloc)
body, err := io.ReadAll(io.LimitReader(c.Request.Body, 64<<20))  // 64MB 上限 / 64MB cap

// 4. 直接写入响应（避免中间缓冲）/ Direct response write (avoid buffering)
c.Data(status, "application/json", jsonBytes)
```

### 10.3 与其他 Go Web 框架对比 / Comparison with Other Go Frameworks

| 框架 / Framework | 路由性能 / Routing | 中间件 / Middleware | 生态 / Ecosystem | 本项目选择 / Choice |
|---|---|---|---|---|
| Gin | 极快（基数树）/ Very fast | 成熟 / Mature | 丰富 / Rich | ✅ 选用 / Selected |
| Echo | 极快 / Very fast | 成熟 / Mature | 中等 / Medium | 备选 / Alternative |
| Chi | 快（标准库兼容）/ Fast | 标准库风格 / stdlib style | 轻量 / Light | 备选 / Alternative |
| net/http | 基础 / Basic | 无 / None | 标准 / Standard | 过于原始 / Too primitive |

## 11. 错误处理模式 / Error Handling Patterns

### 11.1 Go 错误处理哲学 / Go Error Handling Philosophy

Go 采用显式错误返回而非异常机制，Gin 中的错误处理遵循以下模式：

```go
// 本项目错误处理模式 / This project's error handling pattern
func (h *Handler) handleProxy(c *gin.Context) {
    // 1. 解析请求体 / Parse request body
    var req models.ProxyRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{
            "error":   "invalid_request",
            "message": err.Error(),
        })
        return  // 早返回 / Early return
    }

    // 2. 调用下游服务 / Call downstream service
    result, err := h.agentClient.Call(c.Request.Context(), req)
    if err != nil {
        // 区分错误类型 / Distinguish error types
        if errors.Is(err, context.DeadlineExceeded) {
            c.JSON(http.StatusGatewayTimeout, gin.H{
                "error": "upstream_timeout",
            })
            return
        }
        c.JSON(http.StatusBadGateway, gin.H{
            "error":   "upstream_error",
            "message": err.Error(),
        })
        return
    }

    // 3. 成功响应 / Success response
    c.Data(result.StatusCode, "application/json", result.Body)
}
```

### 11.2 错误分类与 HTTP 状态码映射 / Error Classification & Status Code Mapping

| 错误类型 / Error Type | HTTP 状态码 / Status | 场景 / Scenario | 处理 / Handling |
|---|---|---|---|
| 参数验证失败 / Validation | 400 | JSON 解析错误 / Parse error | 返回详细错误 / Return detail |
| 认证失败 / Auth failure | 401 | API Key 无效 / Invalid key | 统一拒绝 / Uniform reject |
| 资源不存在 / Not found | 404 | 未知端点 / Unknown endpoint | 简洁提示 / Brief message |
| 上游超时 / Upstream timeout | 504 | Agent 无响应 / Agent no response | 建议重试 / Suggest retry |
| 上游错误 / Upstream error | 502 | Agent 返回错误 / Agent error | 透传状态 / Passthrough status |
| 内部错误 / Internal | 500 | 未预期异常 / Unexpected panic | 隐藏细节 / Hide details |

### 11.3 Panic 恢复机制 / Panic Recovery Mechanism

```go
// Gin 内置 Recovery 中间件 / Gin built-in Recovery middleware
func Recovery() gin.HandlerFunc {
    return func(c *gin.Context) {
        defer func() {
            if err := recover(); err != nil {
                // 记录堆栈 / Log stack trace
                log.Printf("panic recovered: %v\n%s", err, debug.Stack())
                // 返回 500 / Return 500
                c.AbortWithStatusJSON(500, gin.H{
                    "error": "internal_server_error",
                })
            }
        }()
        c.Next()
    }
}

// 本项目使用 / This project uses:
r := gin.New()
r.Use(gin.Recovery())  // 防止单个请求崩溃影响全局 / Prevent single req crash
```

## 12. 请求绑定与验证 / Request Binding & Validation

### 12.1 ShouldBind 系列方法 / ShouldBind Method Family

```go
// Gin 提供多种绑定方式 / Gin provides multiple binding methods

// JSON 绑定 / JSON binding
var req ProxyRequest
err := c.ShouldBindJSON(&req)    // 从 Body 解析 / Parse from Body

// Query 参数绑定 / Query param binding
var query ListQuery
err := c.ShouldBindQuery(&query) // 从 URL 解析 / Parse from URL

// URI 路径参数 / URI path params
var uri EndpointURI
err := c.ShouldBindUri(&uri)     // 从路径解析 / Parse from path

// 表单绑定 / Form binding
var form UploadForm
err := c.ShouldBind(&form)       // 自动检测 Content-Type / Auto detect
```

### 12.2 结构体标签验证 / Struct Tag Validation

```go
// 本项目请求模型示例 / This project's request model example
type ProxyRequest struct {
    Method  string          `json:"method" binding:"required,oneof=GET POST PUT DELETE"`
    Path    string          `json:"path" binding:"required,startswith=/"`
    Body    json.RawMessage `json:"body,omitempty"`
    Timeout int             `json:"timeout" binding:"omitempty,min=100,max=300000"`
    Headers map[string]string `json:"headers,omitempty"`
}

// 验证标签说明 / Validation tag explanation:
// required        → 必填字段 / Required field
// oneof=A B C     → 枚举值 / Enum values
// startswith=/    → 前缀检查 / Prefix check
// min=100,max=N   → 范围约束 / Range constraint
// omitempty       → 空值跳过验证 / Skip if empty
```

### 12.3 绑定错误处理 / Binding Error Handling

```go
// 统一绑定错误响应 / Unified binding error response
func bindJSON[T any](c *gin.Context) (*T, bool) {
    var req T
    if err := c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{
            "error":   "validation_error",
            "message": err.Error(),
            "fields":  parseValidationErrors(err),  // 解析字段错误 / Parse field errors
        })
        return nil, false
    }
    return &req, true
}

// 使用泛型绑定 / Using generic binding
func handleProxy(c *gin.Context) {
    req, ok := bindJSON[ProxyRequest](c)
    if !ok {
        return  // 已写入错误响应 / Error response already written
    }
    // 继续处理 / Continue processing
    ...
}
```

## 13. 日志与监控 / Logging & Monitoring

### 13.1 Gin 日志中间件 / Gin Logging Middleware

```go
// 本项目自定义日志中间件 / This project's custom logging middleware
func RequestLogger() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        path := c.Request.URL.Path
        method := c.Request.Method

        c.Next()  // 执行后续处理 / Execute downstream

        // 请求完成后记录 / Log after request completes
        latency := time.Since(start)
        status := c.Writer.Status()
        clientIP := c.ClientIP()

        log.Printf("[%s] %s %s %d %v %s",
            method, path, clientIP, status, latency,
            c.Errors.ByType(gin.ErrorTypePrivate).String(),
        )
    }
}
```

### 13.2 结构化日志输出 / Structured Logging Output

```go
// 生产环境 JSON 日志 / Production JSON logging
type LogEntry struct {
    Timestamp string `json:"timestamp"`
    Level     string `json:"level"`
    Method    string `json:"method"`
    Path      string `json:"path"`
    Status    int    `json:"status"`
    Latency   string `json:"latency"`
    ClientIP  string `json:"client_ip"`
    RequestID string `json:"request_id"`
}

// 输出示例 / Output example:
// {"timestamp":"2024-01-15T10:30:00Z","level":"info","method":"POST",
//  "path":"/api/proxy","status":200,"latency":"45ms",
//  "client_ip":"10.0.1.5","request_id":"abc-123"}
```

### 13.3 健康检查与指标 / Health Check & Metrics

```go
// 健康检查端点 / Health check endpoint
func healthHandler(c *gin.Context) {
    c.JSON(200, gin.H{
        "status":  "healthy",
        "version": Version,
        "uptime":  time.Since(startTime).String(),
    })
}

// 请求计数与延迟统计 / Request count & latency stats
var (
    requestCount = prometheus.NewCounterVec(
        prometheus.CounterOpts{Name: "http_requests_total"},
        []string{"method", "path", "status"},
    )
    requestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "http_request_duration_seconds",
            Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5},
        },
        []string{"method", "path"},
    )
)
```

### 13.4 监控策略对比 / Monitoring Strategy Comparison

| 策略 / Strategy | 工具 / Tool | 适用场景 / Use Case | 本项目 / This Project |
|---|---|---|---|
| 结构化日志 / Structured log | slog / zap | 请求跟踪 / Request tracing | ✅ 使用 / Used |
| Prometheus 指标 / Metrics | prometheus/client_golang | 告警与仪表盘 / Alert & dashboard | ✅ 可选 / Optional |
| 分布式追踪 / Distributed trace | OpenTelemetry | 跨服务调用链 / Cross-service | 未实现 / Not yet |
| 健康探针 / Health probe | 自定义 / Custom | K8s 存活/就绪 / Liveness/Readiness | ✅ 使用 / Used |

## 14. 路由组与 API 版本化 / Route Groups & API Versioning

### 14.1 Gin 路由组架构 / Gin Route Group Architecture

```text
┌────────────────────────────────────────────────────────────────┐
│  Gin 路由组层次 / Gin Route Group Hierarchy                    │
├────────────────────────────────────────────────────────────────┤
│                                                                │
│  gin.Engine (root)                                             │
│  ├── 全局中间件 / Global middleware                            │
│  │   ├── gin.Recovery()                                        │
│  │   ├── CORS middleware                                       │
│  │   └── RequestID middleware                                  │
│  │                                                             │
│  ├── /api (路由组 / Route group)                               │
│  │   ├── 组中间件 / Group middleware                           │
│  │   │   └── Auth middleware (optional)                        │
│  │   ├── GET  /health                                          │
│  │   ├── POST /mask                                            │
│  │   ├── POST /dp                                              │
│  │   ├── POST /kano                                            │
│  │   ├── POST /qol                                             │
│  │   └── POST /classify                                        │
│  │                                                             │
│  └── / (静态文件 / Static files)                               │
│      └── StaticFS("/", distFS)                                 │
│                                                                │
│  本项目实际结构 / This project's actual structure:              │
│  所有 API 在同一版本（无 /v1, /v2）                            │
│  All APIs in same version (no /v1, /v2)                        │
└────────────────────────────────────────────────────────────────┘
```

### 14.2 本项目路由实现 / This Project's Route Implementation

```go
// internal/handlers/handlers.go - 路由注册
// Route registration
func SetupRouter(agentClient *agent.Client, cfg *config.Config) *gin.Engine {
    // 设置模式 / Set mode
    if cfg.Release {
        gin.SetMode(gin.ReleaseMode)
    }

    r := gin.New()

    // 全局中间件 / Global middleware
    r.Use(gin.Recovery())
    r.Use(corsMiddleware())

    // API 路由组 / API route group
    api := r.Group("/api")
    {
        api.GET("/health", healthHandler(agentClient))
        api.POST("/mask", maskHandler(agentClient))
        api.POST("/dp", dpHandler(agentClient))
        api.POST("/kano", kanoHandler(agentClient))
        api.POST("/qol", qolHandler(agentClient))
        api.POST("/ldp", ldpHandler(agentClient))
        api.POST("/classify", classifyHandler(agentClient))
        api.POST("/profile", profileHandler(agentClient))
        api.POST("/parse-file", parseFileHandler())
        api.POST("/lb-test", lbTestHandler())
    }

    // 静态文件服务（前端 SPA）/ Static file serving (frontend SPA)
    r.StaticFS("/", http.FS(distFS))

    return r
}
```

### 14.3 API 版本化策略对比 / API Versioning Strategy Comparison

| 策略 / Strategy | 示例 / Example | 优势 / Pros | 劣势 / Cons |
|---|---|---|---|
| URL 路径 / URL path | `/api/v1/mask` | 明确、易调试 / Clear, easy debug | URL 变化 / URL changes |
| Header | `Accept: application/vnd.api.v2+json` | URL 稳定 / Stable URL | 不可见 / Invisible |
| 查询参数 / Query param | `/api/mask?version=2` | 简单 / Simple | 不规范 / Non-standard |
| 无版本（本项目）/ None (this project) | `/api/mask` | 最简单 / Simplest | 破坏性变更困难 / Hard breaking changes |

### 14.4 本项目路由设计决策 / This Project's Routing Design Decisions

| 决策 / Decision | 原因 / Reason |
|---|---|
| 无 API 版本前缀 / No version prefix | 内部工具，无外部契约 / Internal tool, no external contract |
| 扁平路由结构 / Flat route structure | 端点少（<15），无需嵌套 / Few endpoints, no nesting needed |
| 单一路由组 / Single route group | 所有端点同一权限级别 / All same permission level |
| 静态文件在根路径 / Static at root | SPA 前端服务 / SPA frontend serving |
| POST 为主 / POST dominant | 数据操作需要请求体 / Data ops need request body |

## 15. 文件上传与流处理 / File Upload & Stream Processing

### 15.1 Gin 文件上传处理 / Gin File Upload Handling

```go
// internal/handlers/handlers.go - 文件解析端点
// File parsing endpoint
func parseFileHandler() gin.HandlerFunc {
    return func(c *gin.Context) {
        // 1. 接收上传文件 / Receive uploaded file
        file, header, err := c.Request.FormFile("file")
        if err != nil {
            c.JSON(http.StatusBadRequest, gin.H{
                "error": "No file uploaded / 未上传文件",
            })
            return
        }
        defer file.Close()

        // 2. 文件大小限制 / File size limit
        if header.Size > 10*1024*1024 { // 10MB
            c.JSON(http.StatusRequestEntityTooLarge, gin.H{
                "error": "File too large (max 10MB) / 文件过大",
            })
            return
        }

        // 3. 解析文件内容 / Parse file content
        records, err := fileparse.Parse(file, header.Filename)
        if err != nil {
            c.JSON(http.StatusUnprocessableEntity, gin.H{
                "error": fmt.Sprintf("Parse failed: %v", err),
            })
            return
        }

        // 4. 返回解析结果 / Return parsed result
        c.JSON(http.StatusOK, gin.H{
            "records": records,
            "count":   len(records),
            "filename": header.Filename,
        })
    }
}
```

### 15.2 文件解析器实现 / File Parser Implementation

```go
// internal/fileparse/fileparse.go - 多格式解析
// Multi-format parsing
package fileparse

import (
    "encoding/csv"
    "encoding/json"
    "io"
    "path/filepath"
    "strings"
)

// Parse 根据文件扩展名选择解析器
// Parse selects parser based on file extension
func Parse(r io.Reader, filename string) ([]map[string]string, error) {
    ext := strings.ToLower(filepath.Ext(filename))
    switch ext {
    case ".csv":
        return parseCSV(r)
    case ".json":
        return parseJSON(r)
    default:
        return nil, fmt.Errorf("unsupported format: %s", ext)
    }
}

// parseCSV 解析 CSV 文件为记录列表
// Parse CSV file into record list
func parseCSV(r io.Reader) ([]map[string]string, error) {
    reader := csv.NewReader(r)
    reader.TrimLeadingSpace = true

    // 读取表头 / Read header
    headers, err := reader.Read()
    if err != nil {
        return nil, fmt.Errorf("read header: %w", err)
    }

    // 读取数据行 / Read data rows
    var records []map[string]string
    for {
        row, err := reader.Read()
        if err == io.EOF {
            break
        }
        if err != nil {
            return nil, fmt.Errorf("read row: %w", err)
        }

        record := make(map[string]string, len(headers))
        for i, h := range headers {
            if i < len(row) {
                record[h] = row[i]
            }
        }
        records = append(records, record)
    }
    return records, nil
}
```

### 15.3 本项目文件处理实践 / This Project's File Handling Practice

| 方面 / Aspect | 实现 / Implementation | 说明 / Notes |
|---|---|---|
| 支持格式 / Formats | CSV, JSON | 表格数据为主 / Tabular data primary |
| 大小限制 / Size limit | 10MB | 防止内存溢出 / Prevent OOM |
| 解析方式 / Parse method | 全量加载 / Full load | 文件小，无需流式 / Small files, no streaming |
| 输出格式 / Output format | []map[string]string | 统一结构 / Unified structure |
| 错误处理 / Error handling | 详细错误信息 / Detailed errors | 包含行号和原因 / Include line number & reason |

## 16. gRPC-Gateway 混合架构 / gRPC-Gateway Hybrid Architecture

### 16.1 本项目混合架构 / This Project's Hybrid Architecture

```text
┌────────────────────────────────────────────────────────────────┐
│  Console 混合通信架构 / Console Hybrid Communication Arch       │
├────────────────────────────────────────────────────────────────┤
│                                                                │
│  浏览器 / Browser                                              │
│    │  HTTP/JSON                                                │
│    ▼                                                           │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  方案 A: Python Backend (FastAPI)                     │  │
│  │  ├── REST API 接收 / REST API receive                │  │
│  │  ├── httpx 转发到 Agent REST / Forward to Agent REST  │  │
│  │  └── 返回 JSON / Return JSON                         │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                                │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  方案 B: Go Backend (Gin + gRPC)  ← 本项目主要 / Main │  │
│  │  ├── Gin REST API 接收 / Gin REST API receive        │  │
│  │  ├── gRPC 调用 Agent / gRPC call to Agent            │  │
│  │  ├── Mapper 转换 proto ↔ JSON / Convert proto ↔ JSON │  │
│  │  └── 返回 JSON / Return JSON                         │  │
│  └──────────────────────────────────────────────────────┘  │
│                          │                                     │
│                          │ gRPC (protobuf)                     │
│                          ▼                                     │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  PrivShield Agent (Python)                                │  │
│  │  ├── REST :8079  (FastAPI/Uvicorn)                     │  │
│  │  └── gRPC :50051 (grpc.server)                         │  │
│  └──────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────┘
```

### 16.2 Mapper 层设计 / Mapper Layer Design

```go
// internal/mapper/mapper.go - Proto ↔ JSON 转换层
// Proto ↔ JSON conversion layer
package mapper

import (
    pb "console-backend/proto"
    "console-backend/internal/models"
)

// MaskRequestToProto 将前端 JSON 请求转为 gRPC Proto
// Convert frontend JSON request to gRPC Proto
func MaskRequestToProto(req *models.MaskRequest) *pb.MaskRequest {
    return &pb.MaskRequest{
        Data:     req.Data,
        Strategy: req.Strategy,
        Fields:   req.Fields,
    }
}

// MaskResponseFromProto 将 gRPC Proto 响应转为前端 JSON
// Convert gRPC Proto response to frontend JSON
func MaskResponseFromProto(resp *pb.MaskResponse) *models.MaskResponse {
    return &models.MaskResponse{
        MaskedData: resp.GetMaskedData(),
        Strategy:   resp.GetStrategy(),
    }
}

// 每个隐私原语都有对应的 Mapper:
// Each privacy primitive has corresponding Mapper:
// - mask.go:    MaskRequest/MaskResponse
// - dp.go:      DPQueryRequest/DPQueryResponse
// - kano.go:    KAnonymityRequest/KAnonymityResponse
// - qol.go:     QOLRequest/QOLResponse
// - ldp.go:     LDPRequest/LDPResponse
// - profile.go: ProfileRequest/ProfileResponse
```

### 16.3 混合架构优势 / Hybrid Architecture Advantages

| 优势 / Advantage | 说明 / Description |
|---|---|
| 前端无感知 / Frontend agnostic | 浏览器始终使用 REST/JSON / Browser always uses REST/JSON |
| 后端高性能 / Backend performant | gRPC 二进制传输更快 / gRPC binary transport faster |
| 强类型契约 / Strong type contract | Proto 定义确保一致性 / Proto ensures consistency |
| 可替换性 / Replaceability | Python/Go 后端可互换 / Python/Go backends interchangeable |
| 调试便利 / Debug friendly | 前端侧用 JSON 易调试 / JSON easy to debug on frontend |

### 16.4 本项目架构决策 / This Project's Architecture Decisions

| 决策 / Decision | 原因 / Reason |
|---|---|
| Go 后端为主 / Go backend primary | 性能更好、单二进制部署 / Better perf, single binary |
| Python 后端保留 / Python backend kept | 开发调试方便 / Easy dev debugging |
| Mapper 手动编写 / Manual mapper | 字段少，无需代码生成 / Few fields, no codegen needed |
| 无 gRPC-Gateway 库 / No gRPC-Gateway lib | 项目简单，手动映射更清晰 / Simple project, manual mapping clearer |
| 前端不直接 gRPC / Frontend no direct gRPC | 浏览器 gRPC 需 grpc-web 代理 / Browser gRPC needs grpc-web proxy |

## 17. 依赖注入与项目架构 / Dependency Injection & Project Architecture

### 17.1 手动依赖注入模式 / Manual DI Pattern

Go 社区偏好显式依赖注入而非框架魔法。本项目采用构造函数注入：

```go
// internal/handlers/handlers.go
package handlers

// Handler 持有所有依赖——通过构造函数注入
// Handler holds all dependencies — injected via constructor
type Handler struct {
    agentClient *agent.Client   // gRPC 客户端 / gRPC client
    cfg         *config.Config  // 配置 / configuration
    logger      *slog.Logger    // 结构化日志 / structured logger
}

// NewHandler 创建 Handler 实例
// NewHandler creates a Handler instance
func NewHandler(client *agent.Client, cfg *config.Config, logger *slog.Logger) *Handler {
    return &Handler{
        agentClient: client,
        cfg:         cfg,
        logger:      logger,
    }
}

// RegisterRoutes 注册所有路由
// RegisterRoutes registers all routes
func (h *Handler) RegisterRoutes(r *gin.Engine) {
    api := r.Group("/api/v1")
    api.POST("/mask", h.HandleMask)
    api.POST("/dp/count", h.HandleDPCount)
    api.POST("/kano", h.HandleKAnonymity)
    api.GET("/health", h.HandleHealth)
}
```

### 17.2 Wire 自动生成依赖 / Wire Auto-Generated DI

对于大型项目，可使用 Google Wire 自动生成初始化代码：

```go
// cmd/server/wire.go
//go:build wireinject

package main

import (
    "github.com/google/wire"
    "myapp/internal/agent"
    "myapp/internal/config"
    "myapp/internal/handlers"
)

// ProviderSet 定义依赖提供者
// ProviderSet defines dependency providers
var ProviderSet = wire.NewSet(
    config.Load,
    agent.NewClient,
    handlers.NewHandler,
    NewServer,
)

// InitializeServer 由 Wire 自动生成实现
// InitializeServer implementation is auto-generated by Wire
func InitializeServer() (*Server, error) {
    wire.Build(ProviderSet)
    return nil, nil
}
```

```bash
# 安装 Wire
# Install Wire
go install github.com/google/wire/cmd/wire@latest

# 生成依赖注入代码
# Generate DI code
wire ./cmd/server/
```

### 17.3 分层架构设计 / Layered Architecture Design

```
┌─────────────────────────────────────────────────────┐
│  cmd/server/main.go                                  │
│  • 初始化配置 / Init config                          │
│  • 组装依赖 / Assemble dependencies                  │
│  • 启动服务 / Start server                           │
├─────────────────────────────────────────────────────┤
│  internal/handlers/                                  │
│  • HTTP 请求处理 / HTTP request handling             │
│  • 参数验证 / Parameter validation                   │
│  • 响应格式化 / Response formatting                  │
├─────────────────────────────────────────────────────┤
│  internal/mapper/                                    │
│  • Proto ↔ JSON 转换 / Proto ↔ JSON conversion      │
│  • 字段映射 / Field mapping                          │
│  • 默认值填充 / Default value filling                │
├─────────────────────────────────────────────────────┤
│  internal/agent/                                     │
│  • gRPC 客户端管理 / gRPC client management          │
│  • 连接池 / Connection pool                          │
│  • 重试逻辑 / Retry logic                            │
├─────────────────────────────────────────────────────┤
│  internal/config/                                    │
│  • 环境变量读取 / Env var reading                    │
│  • 配置验证 / Config validation                      │
│  • 默认值 / Default values                           │
└─────────────────────────────────────────────────────┘
```

### 17.4 接口抽象与可测试性 / Interface Abstraction & Testability

```go
// internal/agent/client.go

// PrivacyAgent 定义与后端交互的接口
// PrivacyAgent defines the interface for backend interaction
type PrivacyAgent interface {
    Mask(ctx context.Context, req *MaskRequest) (*MaskResponse, error)
    DPCount(ctx context.Context, req *DPCountRequest) (*DPCountResponse, error)
    HealthCheck(ctx context.Context) error
}

// grpcClient 是生产实现
// grpcClient is the production implementation
type grpcClient struct {
    conn *grpc.ClientConn
    stub pb.PrivacyServiceClient
}

// mockClient 用于测试
// mockClient is used for testing
type mockClient struct {
    maskFn func(ctx context.Context, req *MaskRequest) (*MaskResponse, error)
}

func (m *mockClient) Mask(ctx context.Context, req *MaskRequest) (*MaskResponse, error) {
    return m.maskFn(ctx, req)
}
```

### 17.5 项目布局最佳实践 / Project Layout Best Practices

| 原则 / Principle | 实践 / Practice | 原因 / Reason |
|---|---|---|
| internal/ 封装 / internal/ encapsulation | 所有业务代码在 internal/ | 防止外部导入 / Prevent external import |
| 接口在使用方定义 / Interface at consumer | handlers 定义所需接口 | 解耦 / Decoupling |
| 配置集中管理 / Centralized config | config 包统一加载 | 避免散落 / Avoid scattering |
| 无全局状态 / No global state | 通过参数传递依赖 | 可测试 / Testable |
| 错误向上传播 / Errors bubble up | 不在底层处理 HTTP 响应 | 关注点分离 / Separation |

## 18. 安全加固与认证授权 / Security Hardening & Authentication

### 18.1 CORS 安全配置 / CORS Security Configuration

```go
// 安全的 CORS 配置（生产环境）
// Secure CORS configuration (production)
func secureCORSMiddleware(allowedOrigins []string) gin.HandlerFunc {
    return func(c *gin.Context) {
        origin := c.GetHeader("Origin")
        
        // 严格白名单检查
        // Strict whitelist check
        allowed := false
        for _, o := range allowedOrigins {
            if o == origin {
                allowed = true
                break
            }
        }
        
        if allowed {
            c.Header("Access-Control-Allow-Origin", origin)
            c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
            c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
            c.Header("Access-Control-Max-Age", "86400")
            // 生产环境启用凭证
            // Enable credentials in production
            c.Header("Access-Control-Allow-Credentials", "true")
        }
        
        if c.Request.Method == "OPTIONS" {
            c.AbortWithStatus(http.StatusNoContent)
            return
        }
        c.Next()
    }
}
```

### 18.2 API Key 认证中间件 / API Key Auth Middleware

```go
// APIKeyAuth 验证 API 密钥
// APIKeyAuth validates API keys
func APIKeyAuth(validKeys map[string]bool) gin.HandlerFunc {
    return func(c *gin.Context) {
        // 从多个位置提取 key
        // Extract key from multiple locations
        key := c.GetHeader("X-API-Key")
        if key == "" {
            key = c.Query("api_key")
        }
        
        if key == "" {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
                "error": "missing API key",
            })
            return
        }
        
        // 常量时间比较防止时序攻击
        // Constant-time comparison to prevent timing attacks
        if !constantTimeContains(validKeys, key) {
            c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
                "error": "invalid API key",
            })
            return
        }
        
        c.Set("api_key", key)
        c.Next()
    }
}

// constantTimeContains 使用 crypto/subtle 防止时序攻击
// constantTimeContains uses crypto/subtle to prevent timing attacks
func constantTimeContains(keys map[string]bool, candidate string) bool {
    for k := range keys {
        if subtle.ConstantTimeCompare([]byte(k), []byte(candidate)) == 1 {
            return true
        }
    }
    return false
}
```

### 18.3 速率限制 / Rate Limiting

```go
// RateLimiter 基于令牌桶的速率限制
// RateLimiter token bucket based rate limiting
type RateLimiter struct {
    limiters sync.Map  // map[string]*rate.Limiter
    rate     rate.Limit
    burst    int
}

func NewRateLimiter(rps float64, burst int) *RateLimiter {
    return &RateLimiter{
        rate:  rate.Limit(rps),
        burst: burst,
    }
}

func (rl *RateLimiter) Middleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        // 按 IP 限制
        // Limit by IP
        ip := c.ClientIP()
        
        limiter, _ := rl.limiters.LoadOrStore(ip, rate.NewLimiter(rl.rate, rl.burst))
        l := limiter.(*rate.Limiter)
        
        if !l.Allow() {
            c.Header("Retry-After", "1")
            c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
                "error": "rate limit exceeded",
            })
            return
        }
        
        c.Next()
    }
}
```

### 18.4 安全响应头 / Security Response Headers

```go
// SecurityHeaders 添加安全响应头
// SecurityHeaders adds security response headers
func SecurityHeaders() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Header("X-Content-Type-Options", "nosniff")
        c.Header("X-Frame-Options", "DENY")
        c.Header("X-XSS-Protection", "1; mode=block")
        c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
        c.Header("Content-Security-Policy", "default-src 'self'")
        c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
        c.Next()
    }
}
```

### 18.5 输入验证与清洗 / Input Validation & Sanitization

```go
// 请求体大小限制
// Request body size limit
r.Use(func(c *gin.Context) {
    c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20) // 10MB
    c.Next()
})

// 自定义验证器
// Custom validator
func registerCustomValidators() {
    if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
        // 字段名模式验证（隐私字段）
        // Field name pattern validation (privacy fields)
        v.RegisterValidation("privacy_field", func(fl validator.FieldLevel) bool {
            field := fl.Field().String()
            pattern := `^[a-zA-Z_][a-zA-Z0-9_.]{0,127}$`
            matched, _ := regexp.MatchString(pattern, field)
            return matched
        })
    }
}
```

### 18.6 安全检查清单 / Security Checklist

| 检查项 / Check Item | 状态 / Status | 实现方式 / Implementation |
|---|---|---|
| TLS 强制 / TLS enforced | ✅ | mTLS 双向认证 / Mutual TLS |
| 请求体限制 / Body limit | ✅ | MaxBytesReader 10MB |
| CORS 白名单 / CORS whitelist | ✅ | 严格 Origin 匹配 / Strict Origin match |
| 速率限制 / Rate limit | ✅ | 令牌桶 per-IP / Token bucket per-IP |
| 安全头 / Security headers | ✅ | 6 个标准头 / 6 standard headers |
| 输入验证 / Input validation | ✅ | binding + custom validator |
| 日志脱敏 / Log sanitization | ✅ | 不记录请求体 / No body in logs |
| 依赖扫描 / Dependency scan | ✅ | govulncheck CI |

## 19. 分布式追踪与可观测性 / Distributed Tracing & Observability

### 19.1 OpenTelemetry 集成 / OpenTelemetry Integration

```go
// internal/tracing/tracing.go
package tracing

import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

// InitTracer 初始化 OpenTelemetry Tracer
// InitTracer initializes OpenTelemetry Tracer
func InitTracer(serviceName, otlpEndpoint string) (*sdktrace.TracerProvider, error) {
    exporter, err := otlptracegrpc.New(ctx,
        otlptracegrpc.WithEndpoint(otlpEndpoint),
        otlptracegrpc.WithInsecure(), // 开发环境；生产用 TLS
    )
    if err != nil {
        return nil, err
    }
    
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceName(serviceName),
            semconv.ServiceVersion("1.0.0"),
        )),
        sdktrace.WithSampler(sdktrace.TraceIDRatioBased(0.1)), // 10% 采样
    )
    
    otel.SetTracerProvider(tp)
    return tp, nil
}
```

### 19.2 Gin 追踪中间件 / Gin Tracing Middleware

```go
// TracingMiddleware 为每个请求创建 Span
// TracingMiddleware creates a Span for each request
func TracingMiddleware() gin.HandlerFunc {
    tracer := otel.Tracer("gin-server")
    
    return func(c *gin.Context) {
        // 从请求头提取上游 trace context
        // Extract upstream trace context from headers
        ctx := otel.GetTextMapPropagator().Extract(
            c.Request.Context(),
            propagation.HeaderCarrier(c.Request.Header),
        )
        
        // 创建服务端 Span
        // Create server-side Span
        ctx, span := tracer.Start(ctx,
            fmt.Sprintf("%s %s", c.Request.Method, c.FullPath()),
            trace.WithSpanKind(trace.SpanKindServer),
            trace.WithAttributes(
                semconv.HTTPMethod(c.Request.Method),
                semconv.HTTPURL(c.Request.URL.String()),
                semconv.HTTPClientIP(c.ClientIP()),
            ),
        )
        defer span.End()
        
        // 将 ctx 注入 gin.Context
        // Inject ctx into gin.Context
        c.Request = c.Request.WithContext(ctx)
        
        c.Next()
        
        // 记录响应状态
        // Record response status
        span.SetAttributes(semconv.HTTPStatusCode(c.Writer.Status()))
        if c.Writer.Status() >= 500 {
            span.SetStatus(codes.Error, "server error")
        }
    }
}
```

### 19.3 gRPC 调用追踪 / gRPC Call Tracing

```go
// 在 gRPC 客户端调用中传播 trace context
// Propagate trace context in gRPC client calls
func (h *Handler) HandleMask(c *gin.Context) {
    ctx := c.Request.Context()
    
    tracer := otel.Tracer("handler")
    ctx, span := tracer.Start(ctx, "HandleMask",
        trace.WithAttributes(
            attribute.Int("field_count", len(req.Fields)),
        ),
    )
    defer span.End()
    
    // gRPC 调用自动传播 trace（通过 interceptor）
    // gRPC call auto-propagates trace (via interceptor)
    resp, err := h.agentClient.Mask(ctx, req)
    if err != nil {
        span.RecordError(err)
        span.SetStatus(codes.Error, err.Error())
        c.JSON(502, gin.H{"error": err.Error()})
        return
    }
    
    span.SetAttributes(attribute.Int("masked_count", len(resp.MaskedFields)))
    c.JSON(200, resp)
}
```

### 19.4 Prometheus 指标暴露 / Prometheus Metrics Exposure

```go
// internal/metrics/metrics.go
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    // HTTP 请求计数器
    // HTTP request counter
    HTTPRequests = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "http_requests_total",
            Help: "Total HTTP requests",
        },
        []string{"method", "path", "status"},
    )
    
    // 请求延迟直方图
    // Request latency histogram
    HTTPDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "http_request_duration_seconds",
            Help:    "HTTP request latency",
            Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
        },
        []string{"method", "path"},
    )
    
    // gRPC 调用计数器
    // gRPC call counter
    GRPCCalls = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "grpc_client_calls_total",
            Help: "Total gRPC client calls",
        },
        []string{"method", "status"},
    )
)

// MetricsMiddleware 记录 HTTP 指标
// MetricsMiddleware records HTTP metrics
func MetricsMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        
        c.Next()
        
        duration := time.Since(start).Seconds()
        path := c.FullPath()
        if path == "" {
            path = "unknown"
        }
        
        HTTPRequests.WithLabelValues(
            c.Request.Method, path, strconv.Itoa(c.Writer.Status()),
        ).Inc()
        HTTPDuration.WithLabelValues(c.Request.Method, path).Observe(duration)
    }
}
```

### 19.5 结构化日志与关联 / Structured Logging & Correlation

```go
// 日志中注入 trace ID 实现日志-追踪关联
// Inject trace ID into logs for log-trace correlation
func LoggingMiddleware(logger *slog.Logger) gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        
        // 提取 trace ID
        // Extract trace ID
        spanCtx := trace.SpanContextFromContext(c.Request.Context())
        traceID := spanCtx.TraceID().String()
        
        // 生成 request ID
        // Generate request ID
        requestID := uuid.New().String()[:8]
        c.Header("X-Request-ID", requestID)
        
        c.Next()
        
        // 结构化日志输出
        // Structured log output
        logger.Info("request completed",
            slog.String("method", c.Request.Method),
            slog.String("path", c.Request.URL.Path),
            slog.Int("status", c.Writer.Status()),
            slog.Duration("duration", time.Since(start)),
            slog.String("trace_id", traceID),
            slog.String("request_id", requestID),
            slog.String("client_ip", c.ClientIP()),
        )
    }
}
```

### 19.6 可观测性三支柱总结 / Observability Three Pillars Summary

| 支柱 / Pillar | 工具 / Tool | 用途 / Purpose | 本项目实现 / Project Impl |
|---|---|---|---|
| 日志 / Logs | slog (JSON) | 事件记录 / Event recording | LoggingMiddleware |
| 指标 / Metrics | Prometheus | 聚合监控 / Aggregate monitoring | MetricsMiddleware + /metrics |
| 追踪 / Traces | OpenTelemetry | 请求链路 / Request path | TracingMiddleware + gRPC interceptor |
| 健康检查 / Health | 自定义 / Custom | 存活/就绪 / Liveness/Readiness | /health + /readyz |
| 告警 / Alerts | Alertmanager | 异常通知 / Anomaly notification | P99 > 500ms 规则 / Rule |
