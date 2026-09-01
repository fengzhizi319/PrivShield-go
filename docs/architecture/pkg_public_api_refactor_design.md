# PrivShield 公共 API 收敛与 engine-go/internal 重构设计实现文档

> **目标**：让 `pkg/` 真正成为全仓库共享的公共 API，消除 `engine-go/internal` 与 `pkg/` 之间的重复轮子，并把引擎内部可复用的基础设施下沉到 `pkg/`，供 `services/*`、`console/*` 及未来模块统一使用。
> 
> **版本**：v2.0.0（2026-09-01）  
> **状态**：已完成并落地  
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
   可观测性抽象、网关负载均衡、scope identity 鉴权、gRPC server wrapper 等，其实可以被 `services/*`、`console/*` 复用。

### 1.2 目标

- **消除重复**：`engine-go/internal` 优先调用 `pkg` 公共 API。
- **能力下沉**：把引擎内部真正通用的能力以零业务耦合的方式抽到 `pkg/`。
- **边界清晰**：留在 `engine-go/internal` 的，必须是与隐私计算业务强耦合的实现。
- **不破坏现有行为**：所有重构通过现有测试；指标名、日志字段、认证语义、限流行为保持不变。

---

## 二、重构原则

1. **最小改动原则**：不为了抽象而抽象；只有出现两处以上重复，或明显可被其他模块复用时才下沉。
2. **向后兼容优先**：`pkg` 新增 API 时尽量扩展而不是替换；若必须替换，保留旧 API 并标记 deprecated。
3. **渐进式迁移**：按独立模块分批落地，每批都可单独通过测试。
4. **零外部依赖保持**：下沉到 `pkg` 的可观测性、配置、安全工具继续遵循「默认零外部依赖」原则。
5. **测试即契约**：所有下沉的 `pkg` API 必须带单元测试；重构后原测试必须继续通过。

---

## 三、总体重构蓝图（已落地）

### 3.1 阶段划分

| 阶段 | 主题 | 涉及文件 | 优先级 | 状态 |
|---|---|---|---|---|
| Phase 1 | 配置层收敛：`engine-go/internal/config` 复用 `pkg/config` env helper | `engine-go/internal/config/config.go`<br>`pkg/config/env.go` | P1 | ✅ 已完成 |
| Phase 2 | 可观测性下沉：`Tracer`、通用 logger、metrics builder 抽到 `pkg/observability` | `engine-go/internal/observability/*`<br>`pkg/observability/*`（新增） | P1 | ✅ 已完成 |
| Phase 3 | 安全与限流收敛：`engine-go/internal/security` 复用 `pkg/auth`/`pkg/middleware` | `engine-go/internal/security/*`<br>`pkg/middleware/auth.go`<br>`pkg/auth/*`（新增） | P1 | ✅ 已完成 |
| Phase 4 | 网关负载均衡下沉：`LoadBalancer`/`CircuitBreaker` 抽到 `pkg/gateway` | `engine-go/internal/gateway/balancer.go`<br>`pkg/gateway/*`（新增） | P2 | ✅ 已完成 |
| Phase 5 | gRPC server wrapper 下沉：通用 gRPC server 封装抽到 `pkg/grpcserver` | `engine-go/internal/grpcserver/server.go`<br>`pkg/grpcserver/*`（新增） | P2 | ✅ 已完成 |
| Phase 6 | 隐私参数解析器下沉：`engine-go/internal/profile` 抽到 `pkg/profile` | `engine-go/internal/profile/*`<br>`pkg/profile/*`（新增） | P2 | ✅ 已完成 |
| Phase 7 | console / services 向 `pkg` 公共 API 收敛 | `console/bff-go/*`<br>`console/app-lz/bff-go/*`<br>`services/audit-log/*`<br>`services/datasource-mgr/*` | P1 | ✅ 已完成 |
| Phase 8 | 清理、归档与全量测试 | 全仓库 | P2 | ✅ 已完成 |

### 3.2 不涉及本次重构的内容

- `engine-go/internal/service` 中的 `PrivacyService` 业务编排（与隐私原语强耦合）。
- `engine-go/internal/dynclassification` 中的 LLM client、ONNX NER、AC 自动机等业务逻辑。
- `engine-go/internal/imageredact` 中的 DICOM 二进制处理。

---

## 四、Phase 1：配置层收敛

### 4.1 问题

`engine-go/internal/config/config.go` 原先自己实现了 `envString/envInt/envBool`，而 `pkg/config/env.go` 已经提供了 `EnvString/EnvInt/EnvBool/EnvStringSlice`。

### 4.2 目标

删除 `engine-go/internal/config` 的私有 env helper，统一使用 `pkg/config` 的公共函数。

### 4.3 实际变更

**文件：`engine-go/internal/config/config.go`**

- 删除私有函数 `envString/envInt/envBool`。
- `LoadAgent` / `LoadGateway` 全面使用 `pkgconfig.EnvString/EnvInt/EnvBool`。
- 新增复用 `pkgconfig.ValidateFailClosed` 进行 fail-closed 启动门禁校验。

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
- `Tracer`：保留 `Tracer` 接口 + `NoOpTracer`/`OTelTracer` 骨架。

`engine-go/internal/observability` 退化为**引擎专属指标扩展层**：只保留 `EngineMetrics` 中隐私计算业务指标（classification、budget、ner）的定义，通用 RED 指标和初始化逻辑复用 `pkg/observability`。

### 5.3 实际变更

#### 5.3.1 新建 `pkg/observability/logger.go`

```go
package observability

import (
    "log/slog"
    "os"
    "strings"
)

func NewLogger(format, level string) *slog.Logger { ... }
func InitLogger(format, level string) { slog.SetDefault(NewLogger(format, level)) }
```

字段规范：

- `format`：`json`（默认）或 `text`。
- `level`：`debug/info/warn/error`（不区分大小写）。

#### 5.3.2 新建 `pkg/observability/request_logger.go`

```go
package observability

import (
    "log/slog"
    "time"

    "github.com/gin-gonic/gin"
    pkgmiddleware "github.com/fengzhizi319/PrivShield/pkg/middleware"
)

func RequestLogger() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        path := c.Request.URL.Path
        query := c.Request.URL.RawQuery

        c.Next()

        slog.Info("HTTP request",
            "method", c.Request.Method,
            "path", path,
            "query", query,
            "status", c.Writer.Status(),
            "duration", time.Since(start),
            "client_ip", c.ClientIP(),
            "request_id", pkgmiddleware.GetTraceID(c),
        )
    }
}
```

> 字段对齐历史实现：`msg="HTTP request"`，key 为 `method/path/query/status/duration/client_ip/request_id`。

#### 5.3.3 新建 `pkg/observability/metrics.go`

```go
type REDMetrics struct {
    registry *prometheus.Registry
    RequestsTotal   *prometheus.CounterVec
    RequestDuration *prometheus.HistogramVec
}

func NewREDMetrics() *REDMetrics
func (m *REDMetrics) RecordRequest(protocol, endpoint string, status int, durationSec float64)
func (m *REDMetrics) Registry() *prometheus.Registry
func (m *REDMetrics) MustRegister(cs ...prometheus.Collector)
func (m *REDMetrics) Handler() http.Handler
func (m *REDMetrics) GinHandler() gin.HandlerFunc
func (m *REDMetrics) PrometheusMiddleware() gin.HandlerFunc
func (m *REDMetrics) UnaryServerInterceptor() grpc.UnaryServerInterceptor
```

指标名保持：

- `privshield_requests_total{protocol, endpoint, status}`
- `privshield_request_duration_seconds{protocol, endpoint}`

#### 5.3.4 迁移 `engine-go/internal/observability/tracing.go` 到 `pkg/observability/tracing.go`

- 保留 `Tracer` 接口、`NoOpTracer`、`OTelTracer`、`InitTracing`、`GetTracer`、`StartSpan`、`TracingEnabled`。
- `engine-go/internal/observability/tracing.go` 改为类型别名委托。

#### 5.3.5 改造 `engine-go/internal/observability/metrics.go`

```go
type EngineMetrics struct {
    *observability.REDMetrics
    ClassificationTotal            *prometheus.CounterVec
    BudgetConsumedTotal              *prometheus.CounterVec
    NerInferenceSeconds              *prometheus.HistogramVec
    APIAliasRequestsTotal            *prometheus.CounterVec
    DatasourceNormalizeErrorsTotal   *prometheus.CounterVec
}
```

新增业务指标：

- `privshield_classification_total{engine, level, domain}`
- `privshield_budget_consumed_total{namespace, mechanism}`
- `privshield_ner_inference_seconds{device, batch_size}`
- `privshield_api_alias_requests_total{alias, canonical, target}`
- `privshield_datasource_normalize_errors_total{reason}`

#### 5.3.6 改造 `engine-go/internal/observability/logger.go`

```go
func InitLogger(level string) { pkgobs.InitLogger("json", level) }
func RequestLogger() gin.HandlerFunc { return pkgobs.RequestLogger() }
```

### 5.4 兼容性与验收

- 指标名、标签保持不变。
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
4. 安全头统一使用 `pkg/middleware.SecurityHeadersTo`。

### 6.3 实际变更

#### 6.3.1 新建 `pkg/auth/identity.go`

```go
type Identity struct {
    ServiceType string   // "internal" | "external"
    Name        string
    Scopes      []string
}

func (id *Identity) HasPermission(permission string) bool
var AnonymousIdentity = &Identity{...}

func IsHealthPathOrMethod(pathOrMethod string) bool
func PermissionForRESTPath(path string) string
func PermissionForGRPCMethod(method string) string
```

#### 6.3.2 新建 `pkg/auth/settings.go`

```go
type KeyConfig struct {
    Name   string
    Scopes []string
}

type Settings struct {
    AuthEnabled  bool
    TLSEnabled   bool
    HealthNoAuth bool
    InternalKeys map[string]*KeyConfig
    ExternalKeys map[string]*KeyConfig
}
```

#### 6.3.3 新建 `pkg/auth/middleware.go`

```go
func ConstantTimeLookup(keys map[string]*KeyConfig, token string) *KeyConfig
func authenticateAPIKey(settings *Settings, token string) *Identity
func AuthMiddleware(settings *Settings) gin.HandlerFunc
func RequirePermission(permission string) gin.HandlerFunc
func RequireAnyPermission(permissions ...string) gin.HandlerFunc
func GetIdentity(c *gin.Context) *Identity
```

#### 6.3.4 保留 `pkg/middleware/auth.go` 简单版

- `Auth(apiKey string)`：单 Key 全量放行，用于 services 早期场景。
- `AuthWithRoles(apiKey, readerKey string, readOnly []ReadOnlyEndpoint)`：audit-log 只读核验员角色。

#### 6.3.5 增强 `pkg/middleware/ratelimit.go`

- 内部实现改为 32 分片令牌桶（`numRateLimitShards = 32`）。
- 保留对外签名：
  - `RateLimit(rps int, burst int) gin.HandlerFunc`
  - `NewIPRateLimiter(rps, burst int) *IPRateLimiter`
  - 新增 `RateLimitWithKeyFunc(rps, burst int, keyFunc func(*gin.Context) string) gin.HandlerFunc`
- 新增 `NormalizeRateLimitPath(path string) string`：把纯数字/UUID 动态段替换为 `:id`，防止高基数路径导致桶爆炸。

#### 6.3.6 改造 `engine-go/internal/security`

- `auth.go`：
  - `AuthMiddleware()` 委托 `pkgauth.AuthMiddleware(&settings.Settings)`。
  - `RequirePermission/RequireAnyPermission` 委托 `pkgauth`。
  - `RateLimitMiddleware()` 委托 `pkgmiddleware.RateLimitWithKeyFunc`，key 为 `serviceType:name:normalizedPath`，匿名追加 `clientIP`。
- `identity.go`：
  - `type Identity = pkgauth.Identity`
  - `var AnonymousIdentity = pkgauth.AnonymousIdentity`
  - 其他函数全部委托。
- `config.go`：加载 key 并构造 `pkgauth.Settings`。
- `SecurityHeadersMiddleware()`：调用 `pkgmiddleware.SecurityHeadersTo(c.Writer)` 并追加 `X-Frame-Options: DENY`。

### 6.4 兼容性与验收

- 认证行为、健康端点豁免、scope 权限映射保持不变。
- 限流默认行为（未启用时透传）保持不变。
- 安全头字段统一为 `pkg/middleware.SecurityHeadersTo` 的输出。
- 验收：`go test ./engine-go/internal/security/... ./pkg/auth/... ./pkg/middleware/...` 通过。

---

## 七、Phase 4：网关负载均衡下沉到 `pkg/gateway`

### 7.1 问题

`engine-go/internal/gateway/balancer.go` 实现了 `LoadBalancer`、`BackendNode`、`CircuitBreaker`、`P2C-EWMA`、`round-robin`、`least-conn`、`weighted` 等算法。这些能力在 `console/bff-go` 和 `services/service-hub` 中其实也需要。

### 7.2 目标

把通用负载均衡抽象和算法实现抽到 `pkg/gateway`，`engine-go/internal/gateway` 只保留 HTTP/gRPC 反向代理胶水代码。

### 7.3 实际变更

#### 7.3.1 新建 `pkg/gateway/balancer.go`

```go
type BackendNode struct {
    Address       string
    Weight        int
    currentWeight atomic.Int32
    InFlight      atomic.Int64
    EWMA          float64
    LastUsed      time.Time
    CB            CircuitBreaker
    eWMAMu        sync.Mutex
    proxyOnce     sync.Once
    proxy         *httputil.ReverseProxy
    proxyErr      error
}

type CircuitBreaker struct { ... }
type CBState int

type LoadBalancer struct {
    nodes    []*BackendNode
    strategy string
    rrIndex  atomic.Int32
}

func NewLoadBalancer(addresses []string, strategy string) *LoadBalancer
func NewWeightedLoadBalancer(addresses []string, weights []int, strategy string) *LoadBalancer
func (lb *LoadBalancer) SelectNode() *BackendNode
func (n *BackendNode) ReverseProxy(metrics MetricsRecorder) (*httputil.ReverseProxy, error)
func (n *BackendNode) GetEWMA() float64
func (n *BackendNode) UpdateEWMA(latency time.Duration, alpha float64)
func (n *BackendNode) IncrementInFlight()
func (n *BackendNode) DecrementInFlight()
```

下沉算法：

- `p2c`：Power of Two Choices + EWMA 延迟
- `round_robin`：无锁原子轮询
- `least_conn`：最少在途连接
- `weighted_rr`：Nginx 平滑加权轮询（SWRR）
- `weighted_random`：加权随机

熔断器：

- 三态：`Closed / HalfOpen / Open`
- 默认阈值：`threshold=5`，`cooldown=30s`，`halfOpenMax=3`

#### 7.3.2 改造 `engine-go/internal/gateway/balancer.go`

```go
package gateway

import pgateway "github.com/fengzhizi319/PrivShield/pkg/gateway"

type LoadBalancer = pgateway.LoadBalancer
type BackendNode = pgateway.BackendNode
type CircuitBreaker = pgateway.CircuitBreaker
type CBState = pgateway.CBState

const CBClosed = pgateway.CBClosed
const CBHalfOpen = pgateway.CBHalfOpen
const CBOpen = pgateway.CBOpen
```

> 文件大小从原来的 ~400 行精简到 ~50 行，仅保留类型别名与常量。

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

### 8.3 实际变更

#### 8.3.1 新建 `pkg/grpcserver/server.go`

```go
package grpcserver

import (
    "net"
    "google.golang.org/grpc"
)

type Server struct {
    *grpc.Server
    address string
    opts    []grpc.ServerOption
}

func New(address string, opts ...grpc.ServerOption) *Server
func (s *Server) WithOptions(opts ...grpc.ServerOption) *Server
func (s *Server) WithUnaryInterceptor(interceptors ...grpc.UnaryServerInterceptor) *Server
func (s *Server) WithStreamInterceptor(interceptors ...grpc.StreamServerInterceptor) *Server
func (s *Server) RegisterService(desc *grpc.ServiceDesc, impl any)
func (s *Server) Serve() error
func (s *Server) ServeListener(lis net.Listener) error
func (s *Server) GracefulStop()
func (s *Server) Stop()
```

#### 8.3.2 改造 `engine-go/internal/grpcserver/server.go`

- 复用 `pkg/grpcserver.Server` 作为底层包装器。
- 保留 `PrivacyService` 注册和引擎特有的拦截器链配置（metrics、TLS、mTLS CN 白名单）。

#### 8.3.3 services 与 console 复用 `pkg/grpcserver`

- `services/audit-log/cmd/server/main.go`：`grpcServer = pkggrpcserver.New(cfg.GRPCAddress(), grpcServerOpts...)`
- `services/datasource-mgr/cmd/server/main.go`：`grpcServer = pkggrpcserver.New(cfg.GRPCAddress(), grpcServerOpts...)`

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

### 9.3 实际变更

#### 9.3.1 新建 `pkg/profile/resolver.go`

```go
type PrimitiveParams map[string]interface{}

type PrivacyProfile struct {
    Name       string
    Version    string
    Defaults   map[string]PrimitiveParams
    Namespaces map[string]PrimitiveParams
}

type Resolver struct { ... }

func NewResolver() *Resolver
func (r *Resolver) LoadFromYAML(path string) error
func (r *Resolver) Resolve(primitive string, namespace string, overrides map[string]interface{}) map[string]interface{}
func (r *Resolver) Recommend() map[string]interface{}
func (r *Resolver) RecommendDataParams(namespace string, values []float64, rows []map[string]interface{}, qiCols []string) map[string]interface{}
func (r *Resolver) SavePersonalizedParams(namespace, primitive string, params map[string]interface{})
func Validate(primitive string, params map[string]interface{}) error
```

内置默认参数覆盖：

- `dp`：`epsilon=1.0, delta=0.0, mechanism=laplace`
- `k_anonymity`：`k=5, l=2, t=0.2, max_depth=10`
- `sanitization`：`engine=mask`
- `qol`：`num_dummies=3`
- `classification`：`confidence_threshold=0.75`

#### 9.3.2 改造 `engine-go/internal/profile/resolver.go`

```go
package profile

import pprofile "github.com/fengzhizi319/PrivShield/pkg/profile"

type PrimitiveParams = pprofile.PrimitiveParams
type PrivacyProfile = pprofile.PrivacyProfile
type Resolver = pprofile.Resolver

func NewResolver() *Resolver { return pprofile.NewResolver() }
func Validate(primitive string, params map[string]interface{}) error { return pprofile.Validate(primitive, params) }
```

### 9.4 兼容性与验收

- 隐私参数推荐行为不变。
- 配置热重载路径不变。
- 验收：`go test ./engine-go/internal/profile/... ./pkg/profile/...` 通过。

---

## 十、Phase 7：console / services 向 `pkg` 公共 API 收敛

### 10.1 目标

把 `console/bff-go`、`console/app-lz/bff-go`、`services/audit-log`、`services/datasource-mgr` 中重复实现的基础设施能力统一收敛到 `pkg` 公共 API。

### 10.2 实际变更

#### 10.2.1 配置 env helper 收敛

| 模块 | 文件 | 变更 |
|---|---|---|
| `console/bff-go` | `internal/config/config.go` | 删除私有 `getEnv/getEnvInt/getEnvBool`，改用 `pkgconfig.EnvString/EnvInt/EnvBool` |
| `console/app-lz/bff-go` | `internal/config/config.go` | 同上 |
| `services/audit-log` | `internal/config/config.go` | 同上 |
| `services/datasource-mgr` | `internal/config/config.go` | 同上 |

示例（`console/app-lz/bff-go/internal/config/config.go`）：

```go
host := pkgconfig.EnvString("APP_LZ_HOST", "0.0.0.0")
port := pkgconfig.EnvString("APP_LZ_PORT", "8085")
tlsEnabled, _ := strconv.ParseBool(pkgconfig.EnvString("APP_LZ_TLS_ENABLED", "false"))
```

#### 10.2.2 Logger 初始化收敛

四个模块的 `cmd/server/main.go` 统一改为：

```go
pkgobs.InitLogger(cfg.LogFormat, cfg.LogLevel)
logger := slog.Default()
```

涉及文件：

- `console/bff-go/cmd/server/main.go`
- `console/app-lz/bff-go/cmd/server/main.go`
- `services/audit-log/cmd/server/main.go`
- `services/datasource-mgr/cmd/server/main.go`

#### 10.2.3 gRPC server 收敛

`services/audit-log` 与 `services/datasource-mgr` 的 `cmd/server/main.go` 中 gRPC server 构建统一改为：

```go
import pkggrpcserver "github.com/fengzhizi319/PrivShield/pkg/grpcserver"

grpcServer := pkggrpcserver.New(cfg.GRPCAddress(), grpcServerOpts...)
grpcServer.RegisterService(&pb.AuditLogService_ServiceDesc, serviceImpl)
go grpcServer.ServeListener(grpcLis)
```

> `console/bff-go/internal/grpcserver/server.go` 因与 BFF 代理逻辑高度耦合，未强制迁移到 `pkg/grpcserver`，保持现状。

#### 10.2.4 认证中间件收敛

`services/audit-log` 的 scope/reader 角色认证已确认使用 `pkg/middleware.AuthWithRoles`：

```go
router.Use(middleware.AuthWithRoles(cfg.APIKey, cfg.ReaderAPIKey, readOnlyEndpoints))
```

### 10.3 未强制迁移项

- `pkg/observability.RequestLogger` 未强制替换各程序的 REST 日志中间件，因为现有日志字段差异可能导致日志解析器失效。
- `console/bff-go` 的上游 HTTP/gRPC 客户端未强制改用 `pkg/gateway`，当前仍使用自定义 client pool。

---

## 十一、Phase 8：清理、文档与全量测试

### 11.1 清理动作

- 删除 `engine-go/internal/config` 中的私有 env helper。
- 删除 `engine-go/internal/security` 中已下沉的限流、安全头实现。
- 删除 `engine-go/internal/observability` 中已下沉的通用 logger/metrics/tracing 实现。
- 删除 `engine-go/internal/gateway` 中已下沉的 balancer 算法实现。
- `pkg/config.SetupLogger` 保留未删除，以维持向后兼容。

### 11.2 文档更新

- 本文档：`docs/architecture/pkg_public_api_refactor_design.md`（从设计稿更新为落地报告）。
- `docs/production_observability/design.md`：同步 logger/metrics/tracing 代码路径。
- `docs/gateway_balancer/design.md`：同步 balancer 代码路径。
- `AGENTS.md`：代码路径速查表待后续统一刷新。

### 11.3 全量测试

```bash
make test
make check
```

结果：全部 100% 通过。

---

## 十二、测试与验证结果

### 12.1 单元测试

```bash
CGO_ENABLED=0 go test ./pkg/... ./services/service-hub/... ./services/datasource-mgr/... \
  ./services/audit-log/... ./console/bff-go/... ./console/app-lz/bff-go/... \
  ./privacy-go-sdk/... ./engine-go/...
```

结果：全部 `ok`，无失败。

### 12.2 静态检查

```bash
make check
```

结果：

- 等级词表一致性检查通过。
- 编排变量一致性检查通过（510 个编排变量声明均能在 Go 代码/插值/白名单/豁免标记中找到消费点）。

### 12.3 编译验证

- 修复了迁移过程中暴露的预存在编译错误：`engine-go/internal/service/service.go:1296` 调用未定义的 `s.llmDiagnostics()`。
- 在 `engine-go/internal/service/privacyconfig.go` 中补上了 `llmDiagnostics()` 方法，并在 `PrivacyService` 结构体中增加了 `llmEndpoint`、`llmMaxConcurrency`、`enableLLM` 字段。

---

## 十三、仍然保留在 `engine-go/internal` 的内容

| 模块 | 保留原因 |
|---|---|
| `engine-go/internal/service` | `PrivacyService` 业务编排与隐私原语强耦合。 |
| `engine-go/internal/dynclassification` | LLM client、ONNX NER、AC 自动机、分类分级业务逻辑。 |
| `engine-go/internal/imageredact` | DICOM 二进制处理与医疗影像业务逻辑。 |
| `engine-go/internal/grpcserver/typed_server.go` | 引擎特有的 PrivacyService 注册与 typed gRPC 分发。 |
| `engine-go/internal/observability/gateway_metrics.go` | 网关代理的专属指标扩展。 |
| `engine-go/internal/gateway/http_proxy.go` / `grpc_proxy.go` | HTTP/gRPC 反向代理胶水代码。 |

---

## 十四、未来可进一步收敛的项

以下项在本次重构中未强制迁移，后续若出现第二处复用可按相同模式下沉：

1. **`pkg/observability.RequestLogger` 全仓库统一**：当前 `services/*` 和 `console/*` 仍有自定义 StructuredLogger，统一字段后可能造成日志解析器变更。
2. **console BFF 上游客户端改用 `pkg/gateway`**：`console/bff-go/internal/microservices/client.go` 使用自定义连接池，未来可评估是否复用 `pkg/gateway` 的负载均衡能力。
3. **`pkg/grpcserver` 流式拦截器增强**：当前仅提供 unary 拦截器，流式服务增长后可补充 stream interceptor builder。
4. **`pkg/metrics.Collector` 与 `pkg/observability.REDMetrics` 语义统一**：两者目前独立存在，Collector 面向 services，REDMetrics 面向 engine-go，未来可考虑统一接口。

---

## 十五、风险与缓解

| 风险 | 影响 | 缓解措施 |
|---|---|---|
| 指标名/日志字段变化导致 dashboard/日志解析失效 | 高 | 保持指标名、标签、日志 key 不变；若必须调整，双写并提前公告 |
| 限流行为变化导致生产突增被限 | 高 | 保持默认未启用；保持 token-bucket 算法和默认值；单独跑限流基准测试 |
| 安全头取值变化影响 iframe/安全策略 | 中 | 统一为 `pkg/middleware.SecurityHeadersTo` 输出，引擎侧追加 `X-Frame-Options: DENY` 前先确认前端控制台依赖 |
| `pkg` 包循环依赖 | 中 | 新 `pkg` 包只依赖现有 `pkg` 子包，不依赖 `engine-go/internal` 或 `services/*` |
| 工作区模块 go.mod 不同步 | 中 | 每个 phase 结束后执行 `go mod tidy` 并跑 `make check` |

---

## 十六、附录：代码路径速查

| 主题 | 重构前路径 | 当前路径 |
|---|---|---|
| env helper | `engine-go/internal/config/config.go:205`（私有函数） | `pkg/config/env.go:17` |
| Logger 初始化 | `engine-go/internal/observability/logger.go:14` | `pkg/observability/logger.go:17` |
| RequestLogger | `engine-go/internal/observability/logger.go:55` | `pkg/observability/request_logger.go:18` |
| RED metrics builder | `engine-go/internal/observability/metrics.go:30` | `pkg/observability/metrics.go:30` |
| Engine 业务指标 | `engine-go/internal/observability/metrics.go`（原混合） | `engine-go/internal/observability/metrics.go:17`（嵌入 RED） |
| Tracer 抽象 | `engine-go/internal/observability/tracing.go:14` | `pkg/observability/tracing.go:11` |
| Scope identity | `engine-go/internal/security/identity.go:8` | `pkg/auth/identity.go:7` |
| Auth middleware | `engine-go/internal/security/auth.go:48` | `pkg/auth/middleware.go:64` |
| 32-shard rate limiter | `engine-go/internal/security/auth.go:271` | `pkg/middleware/ratelimit.go:81` |
| 安全头 | `engine-go/internal/security/auth.go:155` | `pkg/middleware/middleware.go`（`SecurityHeadersTo`） |
| LoadBalancer/CircuitBreaker | `engine-go/internal/gateway/balancer.go:143` | `pkg/gateway/balancer.go:235` |
| gRPC server wrapper | `engine-go/internal/grpcserver/server.go:24` | `pkg/grpcserver/server.go:14` |
| Profile resolver | `engine-go/internal/profile/resolver.go:29` | `pkg/profile/resolver.go:29` |
| console/services logger 入口 | 各模块 `cmd/server/main.go` 私有 `setupLogger` | 统一为 `pkg/observability.InitLogger` |
| services gRPC server | 各模块自建 `grpc.NewServer` | 统一为 `pkg/grpcserver.New` |

---

## 十七、修订历史

| 版本 | 日期 | 说明 |
|---|---|---|
| v1.0.0 | 2026-09-01 | 初始设计稿 |
| v2.0.0 | 2026-09-01 | 更新为落地报告，补充 console/services 收敛、测试验证结果与未来项 |
