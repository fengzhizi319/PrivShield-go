# 代理转发与负载均衡网关 API 与配置参考 (API & Configuration Reference)

> 本文档详细说明 `PrivShield` Go 云原生代理转发与负载均衡网关的 Go Package API、YAML 配置文件规范、环境变量、REST/gRPC 代理端点及 Prometheus 监控指标。

---

## 1. Go Package API

包路径：`github.com/fengzhizi319/PrivShield-go/engine-go/internal/gateway`

### 1.1 `LoadBalancer` 负载均衡器

```go
type LoadBalancer struct {
    nodes    []*BackendNode
    strategy string // "p2c" | "round_robin" | "least_conn" | "weighted_rr" | "weighted_random"
    rrIndex  int
    mu       sync.Mutex
}
```

#### 构造函数

- **`NewLoadBalancer(addresses []string, strategy string) *LoadBalancer`**
  - 创建自适应负载均衡器实例，默认各节点权重为 1。
- **`NewWeightedLoadBalancer(addresses []string, weights []int, strategy string) *LoadBalancer`**
  - 创建支持异构权重的负载均衡器（主要用于 SWRR 与加权随机）。

#### 核心方法

| 方法签名 | 说明 |
|---|---|
| `SelectNode() *BackendNode` | 依据配置策略选择最优后端节点；若全部熔断则返回首个节点供调用方执行熔断处理。 |
| `Nodes() []*BackendNode` | 获取当前所有后端节点切片。 |

---

### 1.2 `BackendNode` 后端节点与状态

```go
type BackendNode struct {
    Address       string         // 后端网络地址 (如 "127.0.0.1:8079")
    Weight        int            // 配置静态权重
    InFlight      int64          // 当前在途活跃并发请求数
    EWMA          float64        // 指数移动加权平均延迟 (秒)
    LastUsed      time.Time      // 最后请求调度时间
    CB            CircuitBreaker // 独立三态熔断器
}
```

#### 状态更新方法

- **`UpdateEWMA(latency time.Duration, alpha float64)`**：更新节点 EWMA 响应延迟，公式为 $\text{EWMA} = \alpha \times \text{latency} + (1-\alpha) \times \text{EWMA}$。
- **`IncrementInFlight()`** / **`DecrementInFlight()`**：并发安全地递增 / 递减在途请求计数。

---

### 1.3 `CircuitBreaker` 三态熔断器

```go
type CBState int

const (
    CBClosed   CBState = iota // 正常态
    CBHalfOpen                // 半开试探态
    CBOpen                    // 熔断态
)
```

- **`NewCircuitBreaker(threshold int, cooldown time.Duration) CircuitBreaker`**：创建熔断器，默认连续失败 5 次熔断，冷却期 30 秒，半开试探 3 次。
- **`Allow() bool`**：检查当前是否允许请求通过（`Closed` 允许，`Open` 且在冷却期内拒绝，`HalfOpen` 且试探次数内允许）。
- **`RecordSuccess()`**：记录成功调用（`HalfOpen` 连续达标转为 `Closed`）。
- **`RecordFailure()`**：记录失败调用（`Closed` 达到阈值或 `HalfOpen` 失败直接转为 `Open`）。
- **`State() CBState`**：获取当前状态枚举。

---

### 1.4 HTTP 反向代理与 gRPC 透明流代理

- **`NewHTTPProxyHandler(lb *LoadBalancer, metrics *observability.GatewayMetrics) gin.HandlerFunc`**
  - 返回 Gin 中间件处理函数，集成 32KB `byteBufferPool` 零分配缓存与 Prometheus 实时指标。
- **`NewHealthCheckHandler(lb *LoadBalancer) gin.HandlerFunc`**
  - 暴露 `GET /gateway/backends` 查询所有节点的在途连接、EWMA 延迟与熔断状态。
- **`NewGrpcProxyListener(lb *LoadBalancer, listenAddr string, metrics *observability.GatewayMetrics) (*grpc.Server, net.Listener, error)`**
  - 创建并启动基于 `grpc.UnknownServiceHandler` 与 `rawCodec` 零拷贝透明流转发的 gRPC 网关服务器。

---

### 1.5 东西向 mTLS 证书配置

- **`BuildBackendTLSConfig(caCertPath, clientCertPath, clientKeyPath string) (*tls.Config, error)`**
  - 构建 mTLS 双向认证配置（默认 TLS 1.3）。
- **`BuildBackendTLSConfigWithMinVersion(caCertPath, clientCertPath, clientKeyPath string, minVersion uint16) (*tls.Config, error)`**
  - 构建 mTLS 配置并指定最低版本（如 `tls.VersionTLS12`）。
- **`BuildInsecureBackendTLSConfig() *tls.Config`**
  - 构建仅加密、跳过客户端证书校验的简单 TLS 配置。

---

## 2. 配置文件参考 (`config/gateway.yaml`)

```yaml
# ── 网关监听 ──
gateway:
  host: "0.0.0.0"              # 监听主机地址
  rest_port: 8000              # REST 反向代理端口
  grpc_port: 50000             # gRPC 透明代理端口

# ── 后端 Agent 节点池 ──
backends:
  - address: "127.0.0.1:8079"  # Agent REST 端口
    weight: 1                  # 调度权重（SWRR / 加权随机模式）
    grpc_address: "127.0.0.1:50051"

# ── 调度策略 ──
strategy: "p2c"                # p2c (推荐) / weighted_rr / least_conn / round_robin / weighted_random

# ── P2C-EWMA 参数 ──
p2c:
  ewma_alpha: 0.2              # EWMA 衰减因子
  ewma_init_latency_us: 1000   # 初始 EWMA 延迟（微秒）

# ── 三态熔断器 ──
circuit_breaker:
  failure_threshold: 5         # 连续失败次数触发熔断
  cooldown_seconds: 30         # 熔断冷却时间（秒）
  half_open_max_probes: 3      # HalfOpen 状态最大试探请求数

# ── 反向代理与连接池 ──
proxy:
  dial_timeout_ms: 1000        # 后端连接超时（毫秒）
  response_header_timeout_ms: 30000
  max_idle_conns: 2048         # 最大空闲连接池容量
  idle_conn_timeout_seconds: 90 # 空闲连接保持时长
```

---

## 3. 环境变量参考

所有 YAML 配置项均支持通过环境变量直接覆盖：

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `GATEWAY_HOST` | `0.0.0.0` | HTTP 网关监听地址 |
| `GATEWAY_PORT` | `8000` | HTTP 网关监听端口 |
| `GATEWAY_GRPC_PORT` | `50000` | gRPC 网关监听端口 |
| `GATEWAY_BACKENDS` | `127.0.0.1:8079` | 后端 Agent 地址列表（以逗号分隔，如 `10.0.1.10:8079,10.0.1.11:8079`） |
| `GATEWAY_STRATEGY` | `p2c` | 负载均衡策略：`p2c` / `weighted_rr` / `least_conn` / `round_robin` / `weighted_random` |
| `PRIVACY_LOG_LEVEL` | `INFO` | 日志级别：`DEBUG` / `INFO` / `WARN` / `ERROR` |

---

## 4. 网关 REST API 端点

### 4.1 网关自身健康检查

- **请求**：`GET /health`
- **响应**：`200 OK`
  ```json
  {
    "status": "ok",
    "component": "gateway"
  }
  ```

### 4.2 后端拓扑与实时指标查询

- **请求**：`GET /gateway/backends`
- **响应**：`200 OK`
  ```json
  {
    "backends": [
      {
        "address": "127.0.0.1:8079",
        "in_flight": 2,
        "ewma_ms": 1.45,
        "cb_state": "closed"
      },
      {
        "address": "127.0.0.1:8080",
        "in_flight": 0,
        "ewma_ms": 0.92,
        "cb_state": "closed"
      }
    ]
  }
  ```

### 4.3 Prometheus 遥测端点

- **请求**：`GET /metrics`
- **响应**：Prometheus 文本格式指标流。

### 4.4 通配反向代理

- **路径**：`/*`（匹配除 `/health`、`/gateway/backends`、`/metrics` 外的所有 HTTP 请求）
- **行为规范**：
  1. 调用 `LoadBalancer.SelectNode()` 依据当前策略挑选最优节点；
  2. 校验节点熔断器状态，若处于 `Open` 状态则通过 `pkg/middleware.AbortWithError` 立即返回 `503 Service Unavailable`；
  3. 原子递增在途连接计数 `node.IncrementInFlight()`；
  4. 从目标 `BackendNode` 获取（首次访问惰性构建并固化）其绑定的 `*httputil.ReverseProxy` 实例；
  5. 代理层通过 `byteBufferPool` 取出 32KB 缓冲区，复用 `sharedTransport`（`MaxIdleConns: 2048`, `MaxIdleConnsPerHost: 256`）长连接向后端发起请求；
  6. 自动执行 RFC 7230 逐段传输头（Hop-by-Hop Headers）剥离，并透传 `X-Forwarded-For`、`X-Forwarded-Proto`、`X-Request-ID` 与 `X-Trace-ID`；
  7. 响应完成时触发 `defer` 回调：原子递减 `node.DecrementInFlight()`，基于请求耗时更新节点 EWMA；根据响应状态码（<500 成功，≥500 失败）更新熔断器；
  8. 实时将 InFlight、EWMA 延迟与请求计数上报 Prometheus（`GatewayMetrics`）。

---

## 5. gRPC 透明流代理行为

- **监听端口**：默认 `:50000`
- **工作机制**：
  1. **泛化方法拦截**：基于 `grpc.UnknownServiceHandler` 统一拦截所有未在网关本地注册的 RPC 方法（如 `/privacy.PrivacyService/*`）；
  2. **零编解码开销 (`rawCodec`)**：通过自定义 `rawCodec` 直接透传原始 Protobuf 二进制帧（`[]byte`），无需在网关层反序列化为 Struct 或重新序列化，消除 CPU 与 GC 损耗；
  3. **双向全双工流管道**：内部启动双 Goroutine 实现客户端与后端的全双工数据帧转发，支持 Unary、Client-Streaming、Server-Streaming、Bidirectional-Streaming 全模式；
  4. **全双工元数据透传**：自动提取 `metadata.IncomingContext` 中的 Metadata（包含分布式追踪 Trace ID、认证凭证等）并注入回源请求；后端响应的 Initial Metadata 与 Trailers 尾部元数据完整回传客户端；
  5. **后端连接池管理**：维护 `connPool`（容量限制 256），智能复用 `READY` / `IDLE` / `CONNECTING` 状态连接，仅在 `TRANSIENT_FAILURE` 或 `SHUTDOWN` 时安全重建；
  6. **RPC 级调度与熔断**：每个独立的 RPC 流均通过 `lb.SelectNode()` 动态选路，流生命周期结束时原子更新 EWMA 与 InFlight。

---

## 6. 异常与标准错误响应

当网关发生调度异常或后端不可达时，统一遵循 `pkg/middleware` 标准信封返回：

| 异常场景 | HTTP 状态码 | Code 标识 | 说明 |
|---|---|---|---|
| **全部节点熔断** | `503 Service Unavailable` | `SERVICE_UNAVAILABLE` | 无可用后端节点 |
| **选定节点熔断** | `503 Service Unavailable` | `CIRCUIT_OPEN` | 当前节点处于熔断期 |
| **后端连接失败** | `502 Bad Gateway` | `BAD_GATEWAY` | 后端服务不可达或连接被拒绝 |
| **代理创建异常** | `500 Internal Server Error` | `PROXY_ERROR` | 网关内部代理初始化失败 |

**错误响应格式示例**：
```json
{
  "code": "BAD_GATEWAY",
  "message": "后端 127.0.0.1:8079 不可达",
  "detail": "dial tcp 127.0.0.1:8079: connect: connection refused",
  "trace_id": "gw-req-20260829221800-abc12345",
  "timestamp": "2026-08-29T14:18:00.123456Z"
}
```