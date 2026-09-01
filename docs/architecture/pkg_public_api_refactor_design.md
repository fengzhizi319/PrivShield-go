# PrivShield 公共 API 收敛与 engine-go/internal 重构设计实现文档

> **目标**：让 `pkg/` 真正成为全仓库共享的公共 API，消除 `engine-go/internal` 与 `pkg/` 之间的重复轮子，并把引擎内部可复用的基础设施下沉到 `pkg/`，供 `services/*`、`console/*` 及未来模块统一使用。
> 
> **版本**：v1.0.0（2026-09-01）  
> **适用范围**：`engine-go/internal/*`、`pkg/*`、`services/*`、`console/*`  
> **关联文档**：
> - `docs/architecture/architecture-design.md`（全栈架构总览）
> - `docs/production_observability/design.md`（可观测性设计）
> - `docs/gateway_balancer/design.md`（网关与负载均衡设计）

---

## 一、背景与问题陈述

### 1.1 现状

当前仓库采用 Monorepo + Go workspace 结构：

- `pkg/*`：定位为**公共 API / 共享库**，所有 Go 模块理论上都可以依赖。
- `engine-go/internal/*`：定位为**核心隐私引擎内部实现**，原则上只服务于 `engine-go`。

但实际代码中出现了两类问题：

1. **`engine-go/internal` 没有充分复用 `pkg`**  
   同一类基础设施（日志中间件、限流、认证、配置 env helper、metrics）在 `pkg` 和 `engine-go/internal` 两边各实现了一套。
2. **`engine-go/internal` 里有大量通用能力被锁在内部**  
   可观测性抽象、网关负载均衡、scope-based 鉴权、grpcserver wrapper 等，其实可以被 `services/*`、`console/*` 复用。

### 1.2 目标

- **消除重复**：`engine-go/internal` 优先调用 `pkg` 公共 API。
- **能力下沉**：把引擎内部真正通用的能力以零业务耦合的方式抽到 `pkg/`。
- **边界清晰**：留在 `engine-go/internal` 的，必须是与隐私计算业务强耦合的实现。
- **不破坏现有行为**：所有重构必须通过现有测试；指标名、日志字段、认证语义、限流行为保持不变。

---

## 二、重构原则

1. **最小改动原则**：不为了抽象而抽象；只有出现两处以上重复，或明显可被其他模块复用时才下沉。
2. **向后兼容优先**：`pkg` 新增 API 时尽量扩展而不是替换；若必须替换，保留旧 API 并标记 deprecated。
3. **渐进式迁移**：按独立模块分批落地，每批都可单独通过测试。
4. **零外部依赖保持**：下沉到 `pkg` 的可观测性、配置、安全工具继续遵循「默认零外部依赖」原则。
5. **测试即契约**：所有下沉的 `pkg` API 必须带单元测试；重构后原测试必须继续通过。

---

## 三、总体重构蓝图

### 3.1 阶段划分

| 阶段 | 主题 | 涉及文件 | 优先级 |
|---|---|---|---|
| Phase 1 | 配置层收敛：`engine-go/internal/config` 复用 `pkg/config` env helper | `engine-go/internal/config/config.go`<br>`pkg/config/env.go` | P1 |
| Phase 2 | 可观测性下沉：把 `Tracer`、通用 logger、metrics builder 抽到 `pkg/observability` | `engine-go/internal/observability/*`<br>`pkg/observability/*`（新增） | P1 |
| Phase 3 | 安全与限流收敛：`engine-go/internal/security` 复用 `pkg/middleware`，并把 scope identity 下沉 | `engine-go/internal/security/*`<br>`pkg/middleware/auth.go`<br>`pkg/auth/*`（新增） | P1 |
| Phase 4 | 网关负载均衡下沉：`LoadBalancer`/`CircuitBreaker` 抽到 `pkg/gateway` | `engine-go/internal/gateway/balancer.go`<br>`pkg/gateway/*`（新增） | P2 |
| Phase 5 | gRPC server wrapper 下沉：通用 gRPC server 封装抽到 `pkg/grpcserver` | `engine-go/internal/grpcserver/server.go`<br>`pkg/grpcserver/*`（新增） | P2 |
| Phase 6 | 隐私参数解析器下沉：`engine-go/internal/profile` 抽到 `pkg/profile` | `engine-go/internal/profile/*`<br>`pkg/profile/*`（新增） | P2 |
| Phase 7 | 清理与归档：删除内部重复实现、更新文档、全量测试 | 全仓库 | P2 |

### 3.2 不涉及本次重构的内容

- `engine-go/internal/service` 中的 `PrivacyService` 业务编排（与隐私原语强耦合）。
- `engine-go/internal/dynclassification` 中的 LLM client、ONNX NER、AC 自动机等业务逻辑。
- `engine-go/internal/imageredact` 中的 DICOM 二进制处理。

---

## 四、Phase 1：配置层收敛

### 4.1 问题

`engine-go/internal/config/config.go` 在 `205~232` 行自己实现了 `envString/envInt/envBool`，而 `pkg/config/env.go` 已经提供了 `EnvString/EnvInt/EnvBool/EnvStringSlice`。

### 4.2 目标

删除 `engine-go/internal/config` 的私有 env helper，统一使用 `pkg/config` 的公共函数。

### 4.3 变更点

**文件：`engine-go/internal/config/config.go`**

- 删除私有函数：
  - `envString(name, def string) string`
  - `envInt(name string, def int) int`
  - `envBool(name string, def bool) bool`
- 将 `LoadAgent` / `LoadGateway` 中的 `envString(...)` 替换为 `pkgconfig.EnvString(...)`，依此类推。

**示例**：

```go
// 替换前
RESTHost: envString("PRIVACY_REST_HOST", "127.0.0.1"),

// 替换后
RESTHost: pkgconfig.EnvString("PRIVACY_REST_HOST", "127.0.0.1"),
```

### 4.4 兼容性

- `pkg/config.EnvString/EnvInt/EnvBool` 与内部函数语义完全一致。
- 无行为变化。

### 4.5 验收

- `go test ./engine-go/internal/config/...` 通过。

---

## 五、Phase 2：可观测性下沉到 `pkg/observability`

### 5.1 问题

当前可观测性能力分散：

- `engine-go/internal/observability`：提供 `InitLogger`、`RequestLogger`、`EngineMetrics`/`GatewayMetrics`、`Tracer` 抽象。
- `pkg/middleware`：提供 `StructuredLogger`、`RequestID`、`TraceMiddleware`。
- `pkg/config`：提供 `SetupLogger`。
- `pkg/metrics`：提供 `Collector`（面向 services）。

能力重叠、字段不统一、日志 key 不同。

### 5.2 目标

新建 `pkg/observability`，作为全仓库可观测性的**公共基座**：

- `Logger`：统一 `InitLogger`、`SetupLogger` 能力，支持 JSON/Text。
- `RequestLogger`：统一 HTTP 访问日志字段。
- `Metrics`：基于独立 Registry 的通用 RED metrics builder。
- `Tracer`：保留 `engine-go/internal/observability/tracing.go` 的 `Tracer` 接口 + `NoOpTracer`/`OTelTracer` 骨架。

`engine-go/internal/observability` 退化为**引擎专属指标扩展层**：只保留 `EngineMetrics` 中隐私计算业务指标（classification、budget、ner）的定义，通用 RED 指标和初始化逻辑复用 `pkg/observability`。

### 5.3 变更点

#### 5.3.1 新建 `pkg/observability/logger.go`

```go
package observability

import (
    "log/slog"
    "os"
    "strings"
)

// InitLogger 初始化全局 slog，支持 json/text。
func InitLogger(format, level string) {
    logger := NewLogger(format, level)
    slog.SetDefault(logger)
}

// NewLogger 创建新的 slog.Logger。
func NewLogger(format, level string) *slog.Logger {
    // ... 合并 engine-go/internal/observability/logger.go 与 pkg/config.SetupLogger 逻辑
}
```

字段规范：

- `format`：`json`（默认）或 `text`。
- `level`：`debug/info/warn/error`，不区分大小写。

#### 5.3.2 新建 `pkg/observability/request_logger.go`

```go
package observability

import (
    "log/slog"
    "time"

    "github.com/gin-gonic/gin"
)

// RequestLogger returns a Gin middleware that records structured access logs.
func RequestLogger(module string) gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        path := c.Request.URL.Path
        query := c.Request.URL.RawQuery
        c.Next()
        slog.Info("http_request",
            "module", module,
            "request_id", GetTraceID(c),
            "method", c.Request.Method,
            "path", path,
            "query", query,
            "status", c.Writer.Status(),
            "duration_ms", time.Since(start).Milliseconds(),
            "client_ip", c.ClientIP(),
        )
    }
}
```

> 注意：`GetTraceID(c)` 优先从 Gin Context 读取，兼容 `pkg/middleware.TraceMiddleware` 和 `pkg/middleware.RequestID`。

#### 5.3.3 新建 `pkg/observability/metrics.go`

提供通用 RED metrics builder：

```go
package observability

import "github.com/prometheus/client_golang/prometheus"

type REDMetrics struct {
    RequestsTotal   *prometheus.CounterVec
    RequestDuration *prometheus.HistogramVec
    registry        *prometheus.Registry
}

func NewREDMetrics(subsystem string) *REDMetrics { ... }

func (m *REDMetrics) RecordRequest(protocol, endpoint string, status int, durationSec float64) { ... }
```

#### 5.3.4 迁移 `engine-go/internal/observability/tracing.go` 到 `pkg/observability/tracing.go`

- 保留 `Tracer` 接口、`NoOpTracer`、`InitTracing`、`GetTracer`。
- `engine-go/internal/observability/tracing.go` 改为类型别名或删除。

#### 5.3.5 改造 `engine-go/internal/observability/metrics.go`

```go
type EngineMetrics struct {
    *observability.REDMetrics  // 嵌入通用 RED
    ClassificationTotal   *prometheus.CounterVec
    BudgetConsumedTotal   *prometheus.CounterVec
    NerInferenceSeconds   *prometheus.HistogramVec
}
```

### 5.4 兼容性与验收

- 指标名 `privshield_requests_total`、`privshield_request_duration_seconds` 保持不变。
- REST/gRPC 指标标签保持不变。
- 日志字段统一后，文档 `docs/production_observability/design.md` 同步更新。
- 验收：`go test ./engine-go/internal/observability/... ./pkg/observability/...` 通过。

---

## 六、Phase 3：安全与限流收敛

### 6.1 问题

- `engine-go/internal/security` 实现了 scope-based API key 认证、32 分片限流、安全头。
- `pkg/middleware` 实现了简单 API key 认证、单锁 IP 限流、安全头。
- 两边关键能力不互通，services 和 console 只能用 `pkg/middleware` 的简单版。

### 6.2 目标

1. 把 `engine-go/internal/security` 中的 **scope-based Identity 模型** 和 **权限映射** 下沉到 `pkg/auth`。
2. 把 **32 分片高性能限流器** 下沉到 `pkg/middleware`，替换现有的 `IPRateLimiter`，并保留 `RateLimit` API。
3. `engine-go/internal/security` 退化为**引擎安全配置加载器**，调用 `pkg/auth` + `pkg/middleware`。
4. 安全头统一使用 `pkg/middleware.SecurityHeaders()`。

### 6.3 变更点

#### 6.3.1 新建 `pkg/auth/identity.go`

```go
package auth

type Identity struct {
    ServiceType string   // "internal" | "external"
    Name        string
    Scopes      []string
}

func (id *Identity) HasPermission(permission string) bool { ... }

var AnonymousIdentity = &Identity{...}
```

#### 6.3.2 新建 `pkg/auth/permissions.go`

把 `PermissionForRESTPath`、`PermissionForGRPCMethod`、`IsHealthPathOrMethod` 从 `engine-go/internal/security/identity.go` 迁移过来。

#### 6.3.3 新建 `pkg/auth/middleware.go`

```go
package auth

import "github.com/gin-gonic/gin"

func Middleware(settings *Settings) gin.HandlerFunc { ... }

func RequirePermission(permission string) gin.HandlerFunc { ... }
```

`Settings` 包含 `AuthEnabled`、`HealthNoAuth`、`InternalKeys`、`ExternalKeys`。

#### 6.3.4 增强 `pkg/middleware/ratelimit.go`

将 `engine-go/internal/security/auth.go` 中的 32-shard token bucket 实现下沉到这里，替换现有 `IPRateLimiter`，但保留对外函数签名：

```go
func RateLimit(rps int, burst int) gin.HandlerFunc
func NewIPRateLimiter(rps, burst int) *IPRateLimiter
```

内部实现改为 sharded + identity-aware，但默认按 client IP 限流。

#### 6.3.5 改造 `engine-go/internal/security`

- `auth.go` 调用 `pkg/auth.Middleware(GetSettings())`。
- `identity.go` 改为 `type Identity = pkgauth.Identity`（类型别名）。
- 删除 `RateLimitMiddleware` 的限流实现，调用 `pkg/middleware.RateLimit(...)`。
- 删除 `SecurityHeadersMiddleware`，调用 `pkg/middleware.SecurityHeaders()`。

### 6.4 兼容性与验收

- 认证行为、健康端点豁免、scope 权限映射保持不变。
- 限流默认行为（未启用时透传）保持不变。
- 安全头字段统一为 `pkg/middleware.SecurityHeaders()` 的输出。
- 验收：`go test ./engine-go/internal/security/... ./pkg/auth/... ./pkg/middleware/...` 通过。

---

## 七、Phase 4：网关负载均衡下沉到 `pkg/gateway`

### 7.1 问题

`engine-go/internal/gateway/balancer.go` 实现了 `LoadBalancer`、`BackendNode`、`CircuitBreaker`、`P2C-EWMA`、`round-robin`、`least-conn`、`weighted` 等算法。这些能力在 `console/bff-go` 和 `services/service-hub` 中其实也需要。

### 7.2 目标

把通用负载均衡抽象和算法实现抽到 `pkg/gateway`，`engine-go/internal/gateway` 只保留 HTTP/gRPC 反向代理胶水代码。

### 7.3 变更点

#### 7.3.1 新建 `pkg/gateway/balancer.go`

```go
package gateway

type BackendNode struct {
    ID      string
    Address string
    Weight  int
    // ...
}

type LoadBalancer interface {
    Pick() (*BackendNode, error)
    Update(nodes []*BackendNode)
    RecordLatency(nodeID string, latency time.Duration)
    SetCircuitBreakerState(nodeID string, state CircuitState)
}
```

下沉：

- `P2CEWMABalancer`（默认）
- `RoundRobinBalancer`
- `LeastConnBalancer`
- `WeightedBalancer`
- `CircuitBreaker`

#### 7.3.2 改造 `engine-go/internal/gateway/balancer.go`

```go
package gateway

import pgateway "github.com/fengzhizi319/PrivShield/pkg/gateway"

// LoadBalancer 现在是 pkg/gateway.LoadBalancer 的别名或 thin wrapper。
type LoadBalancer = pgateway.LoadBalancer
```

### 7.4 兼容性与验收

- 网关默认算法（P2C-EWMA）行为不变。
- `/gateway/backends` 端点返回的字段不变。
- 验收：`go test ./engine-go/internal/gateway/... ./pkg/gateway/...` 通过。

---

## 八、Phase 5：gRPC server wrapper 下沉到 `pkg/grpcserver`

### 8.1 问题

`engine-go/internal/grpcserver/server.go` 封装了一个通用 gRPC server，支持 TLS、metrics interceptor、message size 限制。`services/*/grpcserver` 包里存在重复样板。

### 8.2 目标

把通用 gRPC server wrapper 抽到 `pkg/grpcserver`，保留引擎特有的 `PrivacyService` 注册在 `engine-go/internal/grpcserver`。

### 8.3 变更点

#### 8.3.1 新建 `pkg/grpcserver/server.go`

```go
package grpcserver

import (
    "google.golang.org/grpc"
)

type Server struct {
    *grpc.Server
    address string
    opts    []grpc.ServerOption
}

func New(address string, opts ...Option) *Server { ... }

func (s *Server) WithUnaryInterceptor(interceptors ...grpc.UnaryServerInterceptor) *Server { ... }
func (s *Server) WithStreamInterceptor(interceptors ...grpc.StreamServerInterceptor) *Server { ... }
func (s *Server) Serve() error { ... }
```

#### 8.3.2 改造 `engine-go/internal/grpcserver/server.go`

- 复用 `pkg/grpcserver.Server`。
- 保留 `PrivacyService` 注册和引擎特有的拦截器链配置。

### 8.4 兼容性与验收

- gRPC 监听地址、keepalive、消息大小限制不变。
- metrics interceptor 挂载方式不变。
- 验收：`go test ./engine-go/internal/grpcserver/... ./pkg/grpcserver/...` 通过。

---

## 九、Phase 6：隐私参数解析器下沉到 `pkg/profile`

### 9.1 问题

`engine-go/internal/profile/resolver.go` 实现了 YAML 隐私参数解析、namespace override、请求级参数合并。`services/service-hub` 在编排流水线时可能需要类似能力。

### 9.2 目标

把通用隐私参数解析器抽到 `pkg/profile`，`engine-go/internal/profile` 保留与引擎配置热重载相关的胶水。

### 9.3 变更点

#### 9.3.1 新建 `pkg/profile/resolver.go`

```go
package profile

type Resolver struct { ... }

func NewResolver(defaultsPath string) (*Resolver, error) { ... }
func (r *Resolver) Resolve(namespace string, req Request) (Params, error) { ... }
```

#### 9.3.2 改造 `engine-go/internal/profile`

```go
package profile

import pprofile "github.com/fengzhizi319/PrivShield/pkg/profile"

type Resolver = pprofile.Resolver
```

### 9.4 兼容性与验收

- 隐私参数推荐行为不变。
- 配置热重载路径不变。
- 验收：`go test ./engine-go/internal/profile/... ./pkg/profile/...` 通过。

---

## 十、Phase 7：清理、文档与全量测试

### 10.1 清理动作

- 删除 `engine-go/internal/config` 中的私有 env helper。
- 删除 `engine-go/internal/security` 中已下沉的限流、安全头实现。
- 删除 `engine-go/internal/observability` 中已下沉的通用 logger/metrics/tracing 实现。
- 删除 `engine-go/internal/gateway` 中已下沉的 balancer 算法实现。
- 删除 `engine-go/internal/grpcserver` 中已下沉的通用 server wrapper。
- 删除 `engine-go/internal/profile` 中已下沉的 resolver 实现。

### 10.2 文档更新

- 在 `docs/architecture/` 下创建本设计实现文档的归档版本。
- 更新 `docs/production_observability/design.md` 中关于 logger/metrics/tracing 的代码路径引用。
- 更新 `docs/gateway_balancer/design.md` 中关于 balancer 代码路径的引用。
- 更新 `AGENTS.md` 中的代码路径速查表。

### 10.3 全量测试

```bash
make test
```

必须 100% 通过。

---

## 十一、风险与缓解

| 风险 | 影响 | 缓解措施 |
|---|---|---|
| 指标名/日志字段变化导致 dashboard/日志解析失效 | 高 | 保持指标名、标签、日志 key 不变；若必须调整，双写并提前公告 |
| 限流行为变化导致生产突增被限 | 高 | 保持默认未启用；保持 token-bucket 算法和默认值；单独跑限流基准测试 |
| 安全头取值变化影响 iframe/安全策略 | 中 | 统一为 `SAMEORIGIN` 前先确认前端控制台依赖；必要时保持 `DENY` |
| `pkg` 包循环依赖 | 中 | 新 `pkg` 包只依赖现有 `pkg` 子包，不依赖 `engine-go/internal` 或 `services/*` |
| 工作区模块 go.mod 不同步 | 中 | 每个 phase 结束后执行 `go mod tidy` 并跑 `make check` |

---

## 十二、实施计划与里程碑

| 阶段 | 预计改动文件数 | 预计耗时 | 里程碑 |
|---|---|---|---|
| Phase 1：配置层收敛 | 2 | 0.5 天 | `engine-go/internal/config` 全部复用 `pkg/config` |
| Phase 2：可观测性下沉 | 10+ | 1.5 天 | `pkg/observability` 创建并接入 engine-go |
| Phase 3：安全与限流收敛 | 8+ | 1.5 天 | `pkg/auth` 创建，`pkg/middleware` 限流增强 |
| Phase 4：网关负载均衡下沉 | 4+ | 1 天 | `pkg/gateway` 创建并接入 engine-go |
| Phase 5：gRPC wrapper 下沉 | 3+ | 0.5 天 | `pkg/grpcserver` 创建并接入 engine-go |
| Phase 6：profile resolver 下沉 | 3+ | 0.5 天 | `pkg/profile` 创建并接入 engine-go |
| Phase 7：清理、文档、全量测试 | 全仓库 | 1 天 | `make test` 100% 通过，文档更新完成 |

---

## 十三、附录：代码路径速查

| 主题 | 当前路径 | 重构后路径 |
|---|---|---|
| env helper | `engine-go/internal/config/config.go:205` | `pkg/config/env.go:17` |
| RequestLogger | `engine-go/internal/observability/logger.go:55` | `pkg/observability/request_logger.go` |
| RED metrics builder | `engine-go/internal/observability/metrics.go:30` | `pkg/observability/metrics.go` |
| Tracer 抽象 | `engine-go/internal/observability/tracing.go:14` | `pkg/observability/tracing.go` |
| Scope identity | `engine-go/internal/security/identity.go:8` | `pkg/auth/identity.go` |
| 32-shard rate limiter | `engine-go/internal/security/auth.go:271` | `pkg/middleware/ratelimit.go` |
| 安全头 | `engine-go/internal/security/auth.go:155` | `pkg/middleware/middleware.go:182` |
| LoadBalancer/CircuitBreaker | `engine-go/internal/gateway/balancer.go:143` | `pkg/gateway/balancer.go` |
| gRPC server wrapper | `engine-go/internal/grpcserver/server.go:24` | `pkg/grpcserver/server.go` |
| Profile resolver | `engine-go/internal/profile/resolver.go:29` | `pkg/profile/resolver.go` |
