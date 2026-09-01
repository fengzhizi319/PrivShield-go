# 第一周学习资料与计划：engine-go 网关与安全体系 Review

> **周目标**：完成 engine-go 核心模块的人工 Review，建立对网关流量治理、安全认证体系与可观测性实现的全局认知。
>
> **审查范围**：`engine-go/internal/gateway/`、`engine-go/internal/security/`、`engine-go/internal/observability/`、`pkg/gateway/`、`pkg/auth/`、`pkg/tlsutil/`、`pkg/middleware/`、`pkg/observability/`、`pkg/circuitbreaker/`。
>
> **代码总量**：约 30 个 Go 源文件，~4,500 行实现代码 + ~1,500 行测试代码。

---

## Day 1-2：网关与流量治理 Review

### 1.1 审查文件清单

| 文件路径 | 行数 | 核心职责 |
|---|:---:|---|
| `pkg/gateway/balancer.go` | 359 | P2C-EWMA 负载均衡器 + 5 种调度策略 + BufferPool + BackendNode |
| `pkg/circuitbreaker/circuitbreaker.go` | 141 | 三态熔断器原语（Closed → Open → HalfOpen） |
| `engine-go/internal/gateway/balancer.go` | 42 | 类型别名桥接层（将 pkg 层类型暴露给 engine-go 内部） |
| `engine-go/internal/gateway/http_proxy.go` | 97 | HTTP 反向代理处理器（Gin HandlerFunc） |
| `engine-go/internal/gateway/grpc_proxy.go` | 310 | gRPC 透明流式代理（RawCodec + 双向零拷贝转发） |
| `engine-go/internal/gateway/backend_tls.go` | 65 | 东西向 mTLS 回源 TLS 配置构建 |
| 对应 `_test.go` 文件 | ~400 | 全部网关组件的单元测试 |

### 1.2 核心知识点详解

#### 1.2.1 P2C-EWMA 负载均衡算法

**源码位置**：`pkg/gateway/balancer.go` `selectP2C()` (L186-222)

**算法原理**：
1. **Power of Two Choices (P2C)**：随机选取两个节点，选择负载较轻的那个。相比简单轮询（Round-Robin），P2C 在异构负载场景下能将最大负载降低到 \(O(\log \log n)\)，且无需全局状态同步。
2. **EWMA (Exponentially Weighted Moving Average)**：指数移动加权平均延迟，公式为 \(\text{EWMA}_{new} = \alpha \times \text{latency} + (1 - \alpha) \times \text{EWMA}_{old}\)，其中 \(\alpha\) 为平滑系数（HTTP 代理用 0.3，gRPC 代理用 0.2）。
3. **负载评分**：\(\text{score} = (\text{InFlight} + 1) \times \max(\text{EWMA}, 0.001)\)。InFlight+1 防止零除，EWMA 下限 0.001 防止首次请求即获得极端优势。

**为何选择 P2C 而非简单轮询**：
- 轮询假设所有后端性能一致，实际中各节点 CPU/内存/网络延迟差异显著
- P2C 通过 EWMA 延迟感知自动避开慢节点，实现自适应调度
- 相比最少连接（Least-Conn），P2C 的随机双选避免了全局锁竞争

**5 种调度策略**：
| 策略 | 方法 | 无锁化设计 | 适用场景 |
|---|---|---|---|
| `p2c` (默认) | `selectP2C()` | `rand.IntN` + 原子读 InFlight | 通用生产环境 |
| `round_robin` | `selectRoundRobin()` | `atomic.Int32` fetch-and-add | 后端同构场景 |
| `least_conn` | `selectLeastConn()` | 原子读 InFlight | 长连接场景 |
| `weighted_rr` | `selectWeightedRoundRobin()` | Nginx SWRR 平滑加权，`atomic.Int32` currentWeight | 异构后端按权重分配 |
| `weighted_random` | `selectWeightedRandom()` | 概率与 Weight 成正比 | 灰度/金丝雀发布 |

#### 1.2.2 BackendNode 数据模型

**源码位置**：`pkg/gateway/balancer.go` (L26-42)

```
BackendNode 结构体关键字段：
├── Address: string            // 后端地址 "host:port"
├── Weight: int                // 调度权重（SWRR/加权随机用）
├── currentWeight: atomic.Int32 // Nginx SWRR 当前权重（原子操作）
├── InFlight: atomic.Int64     // 在途请求数（原子操作）
├── EWMA: float64              // 指数移动加权延迟（eWMAMu 保护）
├── CB: *circuitbreaker.Breaker // 节点级熔断器
├── eWMAMu: sync.Mutex         // 仅保护 EWMA 字段（与 InFlight 锁分离）
├── proxyOnce: sync.Once       // 反向代理惰性初始化
└── proxy: *httputil.ReverseProxy // 节点绑定的反向代理实例
```

**设计亮点**：
- `InFlight` 使用 `atomic.Int64`，`EWMA` 使用独立 `eWMAMu` 互斥量 — 二者锁分离，高频 InFlight 原子操作不会阻塞 EWMA 更新
- `proxyOnce` 实现惰性创建：反向代理实例随节点首次使用时构建，随节点回收即释放，取代了早期「全局 sync.Map + 后台 TTL 清理 goroutine」方案
- `DecrementInFlight()` 使用 CAS 循环（CompareAndSwap）而非简单 Add(-1)，防止并发下 InFlight 变为负数

#### 1.2.3 BufferPool 零分配机制

**源码位置**：`pkg/gateway/balancer.go` (L54-87)

```
byteBufferPool 实现 httputil.BufferPool 接口：
├── sync.Pool 管理 32KB 预分配缓冲区
├── Get() 从池中取出 *[]byte 并解引用
└── Put() 仅回收 cap >= 32KB 的缓冲区（防止小缓冲区污染池）
```

**共享传输层**（`sharedTransport`）：
- `MaxIdleConns: 2048` — 全局最大空闲连接数
- `MaxIdleConnsPerHost: 256` — 单后端最大空闲连接
- `IdleConnTimeout: 90s` — 空闲连接超时回收

**关键关注点**：
- `sync.Pool` 在 GC 时会清空所有池对象 — 高 GC 压力下可能频繁重建缓冲区
- `Put()` 的 `cap(b) >= 32*1024` 检查防止被缩容的 slice 污染池
- 所有代理共享同一个 `sharedTransport`，避免每个代理独立维护连接池

#### 1.2.4 三态熔断器

**源码位置**：`pkg/circuitbreaker/circuitbreaker.go` (L39-141)

**状态机转换图**：
```
                    连续失败 >= threshold
    ┌──────────┐  ──────────────────────▶  ┌──────────┐
    │  Closed  │                           │   Open   │
    │ (正常)   │  ◀──────────────────────  │ (熔断)   │
    └──────────┘    冷却期已过              └──────────┘
         ▲                                    │
         │    HalfOpen 探测成功 >= halfOpenMax  │ 冷却期已过
         │  ◀─────────────────────────────────│
         │                                    ▼
         │                              ┌──────────┐
         └──────────────────────────────│ HalfOpen │
            HalfOpen 任何失败 → Open     │ (探测)   │
                                        └──────────┘
```

**关键参数**：
- `threshold`：触发熔断的连续失败次数（默认 5）
- `cooldown`：Open 状态最短持续时间（默认 30s）
- `halfOpenMax`：HalfOpen 状态最大探测请求数（硬编码 3）

**并发安全**：所有方法使用 `sync.Mutex` 保护，`Allow()` 在 Open 状态下同时检查冷却期并自动转换为 HalfOpen。

**Review 关注问题**：
- `halfOpenMax` 硬编码为 3，是否需要配置化？
- 冷却时间 `cooldown` 默认 30s 是否适合所有后端场景？
- `RecordFailure()` 中 `openedAt` 在每次失败时都更新（L118），而非仅在状态转换时更新 — 这是否影响冷却期计算？

#### 1.2.5 gRPC 透明流式代理

**源码位置**：`engine-go/internal/gateway/grpc_proxy.go` (L59-310)

**核心架构**：
```
客户端 ──gRPC Stream──▶ Gateway (UnknownServiceHandler)
                           │
                    ┌──────┴──────┐
                    │ RawCodec    │  ← 零编解码字节透传
                    │ P2C-EWMA    │  ← 选择最优后端
                    │ ConnPool    │  ← 连接池 (max 256)
                    └──────┬──────┘
                           │
                    双向零拷贝流转发
                    ┌──────┴──────┐
                    │ 客户端→后端  │  goroutine 1
                    │ 后端→客户端  │  goroutine 2
                    └─────────────┘
```

**RawCodec 设计**（L33-53）：
- 实现 `grpc.encoding.Codec` 接口，`Marshal`/`Unmarshal` 直接透传 `*[]byte`
- 避免「先反序列化再序列化」的双重开销
- 通过 `grpc.ForceCodec(rawCodec{})` 强制使用

**连接池管理**（L82-122）：
- 双重检查锁（Double-Check Locking）模式
- `maxPoolSize: 256` — 防止后端地址动态变化时内存泄漏
- `isConnReady()` 将 IDLE/READY/CONNECTING 均视为可用（连接池应复用而非反复创建）
- 仅 TRANSIENT_FAILURE/SHUTDOWN 视为不可用并触发重建

**双向流转发**（L193-253）：
- 两个 goroutine 分别处理客户端→后端和后端→客户端方向
- `streamCtx` + `streamCancel` 确保任一方向出错时两个 goroutine 都能退出
- `errChan` 容量为 2，防止 goroutine 泄漏
- 第一个 goroutine 退出后调用 `streamCancel()` 通知另一个退出

**Review 关注问题**：
- 连接池 Max 256 在高并发场景下是否足够？
- gRPC 代理使用 `insecure.NewCredentials()` — 后端连接是否应升级为 mTLS？
- 双向转发中 `RecvMsg(&frame)` 的 `frame` 是 `[]byte` 类型 — 大消息场景下的内存占用

#### 1.2.6 东西向 mTLS 回源 TLS

**源码位置**：`engine-go/internal/gateway/backend_tls.go` (L24-65)

**TLS 构建流程**：
1. 加载内部 CA 证书 → 构建 `x509.CertPool`（验证后端证书）
2. 加载网关客户端证书/密钥 → `tls.X509KeyPair`（后端验证网关身份）
3. 构建 `tls.Config`，强制 TLS 1.3 最低版本

**三种配置模式**：
- `BuildBackendTLSConfig`：标准 mTLS（TLS 1.3）
- `BuildBackendTLSConfigWithMinVersion`：支持降级到 TLS 1.2
- `BuildInsecureBackendTLSConfig`：仅加密不验证（开发/测试用）

### 1.3 Day 1-2 Review 方法

1. **先读测试**：`http_proxy_test.go` → `grpc_proxy_test.go` → `balancer_test.go`，理解预期行为
2. **再读实现**：对照测试用例理解边界条件
3. **画图理解**：画出 P2C 选择流程、熔断器状态机、gRPC 双向流转发的时序图
4. **标记疑问**：对不理解的地方添加 `// REVIEW:` 注释

---

## Day 3-4：安全体系 Review（mTLS、认证、权限）

### 2.1 审查文件清单

| 文件路径 | 行数 | 核心职责 |
|---|:---:|---|
| `engine-go/internal/security/config.go` | 143 | 安全配置加载（环境变量 → Settings 单例） |
| `engine-go/internal/security/auth.go` | 76 | 认证中间件桥接 + 安全头 + 限流中间件 |
| `engine-go/internal/security/identity.go` | 28 | 身份类型别名 + 权限映射函数 |
| `engine-go/internal/security/whitelist.go` | 226 | mTLS CN 白名单管理器（YAML 热重载） |
| `pkg/auth/identity.go` | 127 | Identity 模型 + Scope 权限模型 + REST/gRPC 权限映射 |
| `pkg/auth/middleware.go` | 178 | API Key 认证中间件 + 常量时间查找 |
| `pkg/auth/settings.go` | ~30 | KeyConfig + Settings 数据结构 |
| `pkg/tlsutil/whitelist.go` | 271 | 动态 mTLS CN 白名单（5s 轮询热重载 + Scope 匹配） |
| `pkg/tlsutil/grpc_interceptor.go` | 134 | gRPC mTLS CN 白名单拦截器（一元 + 流式） |
| `pkg/middleware/ratelimit.go` | 314 | 32 分片令牌桶限流 + DDoS 防护 |
| `pkg/middleware/auth.go` | 192 | 基础 API Key 认证 + AuthWithRoles 读写分离 |
| `pkg/middleware/trace.go` | 53 | 分布式追踪上下文传播中间件 |

### 2.2 核心知识点详解

#### 2.2.1 mTLS CN 白名单热重载机制

**两套实现并存**（须理解差异）：

| 维度 | `engine-go/internal/security/whitelist.go` | `pkg/tlsutil/whitelist.go` |
|---|---|---|
| **定位** | engine-go Agent 专用（REST 侧） | 共享基础库（gRPC 侧） |
| **重载方式** | 请求驱动被动检查（`checkReload`） | 后台 5s 轮询主动检查（`poll`） |
| **YAML 格式** | `entries: [{cn, scopes}]` | `clients: [{cn, allowed_scopes}]` + 兼容 `entries` |
| **Scope 模型** | 简单 scope 列表 | 支持通配符 `/ServiceHub/*` 前缀匹配 |
| **停机能力** | 无（无后台协程） | `Close()` 优雅停止轮询协程 |

**engine-go 侧 WhitelistManager**（L34-226）：
- **两阶段提交**：先解析到临时 `newCache` map，成功后在 `mu.Lock()` 临界区内原子交换 `m.cache = newCache`
- **请求驱动重载**：`GetEntry()` 每次调用前执行 `checkReload()` → `os.Stat()` 检查 mtime → 若变更则触发 `load()`
- **节流机制**：无显式节流（每次请求都 `os.Stat`），但 `os.Stat` 是系统调用级别操作，开销极低
- **模块级单例**：`GetWhitelistManager()` 使用 `sync.Once` 保证全局唯一实例

**pkg 侧 DynamicWhitelist**（L86-271）：
- **后台轮询**：独立 goroutine 每 5s 通过 `os.Stat` 检测 mtime 变更
- **双格式兼容**：优先解析 `clients` 键（设计标准），回退到 `entries` 键（历史格式）
- **Scope 匹配规则**（`CheckScope` L227-242）：
  1. 全局通配符 `"*"` → 允许所有方法
  2. 精确全名匹配 → `/PrivacyService/Process`
  3. 前缀通配符 → `/AuditLog/*` 匹配所有 `/AuditLog/` 前缀方法
- **优雅停机**：`Close()` 使用 `stopMu` 互斥量保护，支持幂等调用

#### 2.2.2 API Key 常量时间认证

**源码位置**：`pkg/auth/middleware.go` `ConstantTimeLookup()` (L55-75)

**为何必须常量时间比较**：
- 标准 `==` 比较在发现第一个不同字符时即返回，攻击者可通过测量响应时间逐字符猜解密钥
- `subtle.ConstantTimeCompare` 始终比较所有字节，耗时与输入长度成正比而非匹配位置

**实现细节**：
```go
func ConstantTimeLookup(keys map[string]*KeyConfig, token string) *KeyConfig {
    // 1. 排序 key 确保确定性迭代顺序（Go map 迭代顺序随机）
    sortedKeys := make([]string, 0, len(keys))
    for k := range keys { sortedKeys = append(sortedKeys, k) }
    sort.Strings(sortedKeys)

    // 2. 遍历全部 key 且始终比较所有 key（不提前返回）
    tokenBytes := []byte(token)
    var matched *KeyConfig
    for _, key := range sortedKeys {
        if subtle.ConstantTimeCompare([]byte(key), tokenBytes) == 1 {
            matched = keys[key]  // 记录但不 break，继续比较剩余 key
        }
    }
    return matched
}
```

**关键安全特性**：
- 排序 key 消除 map 迭代随机性带来的时序差异
- 不提前 break — 即使已找到匹配也继续比较所有 key，防止时序侧信道泄漏匹配位置
- 对内部和外部 key 存储分别执行常量时间查找（`authenticateAPIKey` L78-86）

#### 2.2.3 Scope-based 权限模型

**源码位置**：`pkg/auth/identity.go` (L9-127)

**Identity 结构**：
```go
type Identity struct {
    ServiceType string   // "internal" (高信任) | "external" (外部客户端)
    Name        string   // 服务/账户名称
    Scopes      []string // 权限列表，["*"] 表示完全访问
}
```

**REST 路径 → 权限映射**（`PermissionForRESTPath` L46-88）：
| 路径前缀 | 权限字符串 |
|---|---|
| `/health`, `/livez`, `/readyz` | `health:read` |
| `/v1/privacy/mask*` | `privacy:mask` |
| `/v1/privacy/dp/*`, `/v1/privacy/ldp/*` | `privacy:dp` |
| `/v1/privacy/k_anonymize*` | `privacy:kano` |
| `/v1/privacy/qol/*` | `privacy:qol` |
| `/v1/privacy/classify/*` | `classification:read` |
| `/v1/dynclassification/profiles/reload` | `dynclassification:write` |
| `/v1/agent*` | `agent:process` |
| `/v1/medical*` | `medical:process` |
| `/v1/ops/*` | `ops:diagnostics` |
| `/debug/pprof*` | `ops:admin` |

**gRPC 方法 → 权限映射**（`PermissionForGRPCMethod` L91-126）：
- 使用静态 map 映射方法短名到权限字符串
- 覆盖所有 44 个隐私原语 + 分类分级 + 健康检查

**internal/external 身份分离的设计意图**：
- `internal` 身份：由内部服务（service-hub、bff-go）持有，通常授予更宽泛的 scope
- `external` 身份：由外部客户端持有，scope 受最小权限约束
- 认证时先查 internal keys，再查 external keys — internal 优先

#### 2.2.4 32 分片令牌桶限流

**源码位置**：`pkg/middleware/ratelimit.go` (L80-224)

**架构设计**：
```
shardedRateLimiter 结构：
├── shards[32] *rateLimitShard     // 32 个独立分片
│   ├── mu: sync.Mutex             // 分片级互斥锁
│   └── buckets: map[string]*bucket // key → 令牌桶
├── stopCh: chan struct{}           // 停机信号
└── 后台 GC 协程 (每 3 分钟清理 >10 分钟未活动的桶)
```

**为何分片**：
- 单锁限流器在高并发下成为瓶颈 — 所有请求竞争同一把锁
- 32 分片将锁竞争降低到 1/32（FNV-1a 哈希分片）
- 分片键选择：`Identity.ServiceType + ":" + Identity.Name + ":" + normalizedPath`
- 匿名调用者追加 `ClientIP` 作为分片因子，防止单 IP 洪泛攻击

**令牌桶算法**（`allow()` L133-157）：
```
1. 计算距上次检查的经过时间 elapsed
2. 补充令牌：tokens += elapsed * rps（令牌按恒定速率补充）
3. 上限截断：tokens = min(tokens, burst)（防止令牌堆积）
4. 判定：tokens >= 1.0 则扣除 1 令牌并放行，否则拒绝
```

**路径归一化**（`NormalizeRateLimitPath` L271-282）：
- 将路径中的动态 ID 段（纯数字、UUID 格式）替换为 `:id` 占位符
- 防止高基数路径（如 `/api/users/12345`）导致桶爆炸

**DDoS 纵深防御三层**：
1. `MaxBodySize`：限制请求体最大 32 MiB（`http.MaxBytesReader`）
2. `MaxConcurrent`：Channel 信号量限制最大 1000 并发
3. `RateLimit`：32 分片令牌桶平滑限流

#### 2.2.5 安全头中间件

**源码位置**：`engine-go/internal/security/auth.go` (L33-39)

```go
func SecurityHeadersMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        pkgmiddleware.SecurityHeadersTo(c.Writer)  // CSP, HSTS, X-Content-Type-Options 等
        c.Writer.Header().Set("X-Frame-Options", "DENY")  // 禁止 iframe 嵌套
        c.Next()
    }
}
```

#### 2.2.6 gRPC mTLS CN 拦截器

**源码位置**：`pkg/tlsutil/grpc_interceptor.go` (L38-134)

**鉴权流程**：
1. `extractClientCN(ctx)` → 从 `peer.Peer.AuthInfo` 提取 `credentials.TLSInfo`
2. 深入 `VerifiedChains[0][0].Subject.CommonName` 获取经过 CA 验证的 CN
3. `authorizeClient(cn, fullMethod)` → 白名单存在性检查 + 方法级 Scope 匹配
4. 同时提供 `UnaryServerInterceptor` 和 `StreamServerInterceptor` — 一元与流式全覆盖

**快速装配工厂**：
```go
unaryInt, streamInt, whitelist, err := tlsutil.NewWhitelistInterceptor(path)
// path 为空 → 返回全 nil（禁用 CN 白名单鉴权）
// path 非空 → 自动加载 YAML + 启动热重载 + 返回拦截器
```

### 2.3 Day 3-4 Review 方法

1. **对照配置加载**：从 `config.go` 的 `loadSettings()` 开始，追踪每个环境变量的加载路径
2. **追踪认证链路**：HTTP 请求 → `AuthMiddleware` → `ExtractBearerToken` → `ConstantTimeLookup` → `authenticateAPIKey` → `PermissionForRESTPath` → `HasPermission`
3. **对比两套白名单**：engine-go `WhitelistManager` vs pkg `DynamicWhitelist`，理解设计差异与适用场景
4. **安全审计清单**：
   - [ ] mTLS CN 白名单是否已接入 Gin 主中间件链
   - [ ] REST 路径到权限的映射是否有遗漏
   - [ ] API Key 无过期机制，生产环境需要轮转能力
   - [ ] 限流完全基于内存，多实例部署时无法共享限流状态

---

## Day 5：可观测性 Review（日志、指标、追踪）

### 3.1 审查文件清单

| 文件路径 | 行数 | 核心职责 |
|---|:---:|---|
| `pkg/observability/metrics.go` | 125 | REDMetrics（Rate/Errors/Duration）Prometheus 指标 |
| `pkg/observability/tracing.go` | 94 | Tracer 抽象接口 + NoOpTracer + OTelTracer 占位 |
| `pkg/observability/logger.go` | 47 | 结构化 slog 日志初始化（JSON/Text） |
| `pkg/observability/request_logger.go` | 56 | HTTP 请求日志 Gin 中间件 |
| `pkg/observability/trace.go` | 126 | TraceID 生成 + 追踪上下文传播中间件 |
| `engine-go/internal/observability/metrics.go` | 140 | EngineMetrics（RED + 业务指标扩展） |
| `engine-go/internal/observability/gateway_metrics.go` | 137 | GatewayMetrics（InFlight/EWMA/熔断器/转发计数） |
| `pkg/middleware/trace.go` | 53 | 追踪中间件兼容别名（下沉至 pkg/observability） |

### 3.2 核心知识点详解

#### 3.2.1 Prometheus RED 指标体系

**源码位置**：`pkg/observability/metrics.go` (L22-125)

**RED 方法论**：
- **Rate**（请求率）：`privshield_requests_total` — 按 protocol/endpoint/status 分组的计数器
- **Errors**（错误率）：从 `RequestsTotal` 的 status 标签中提取 5xx 状态码计算
- **Duration**（延迟）：`privshield_request_duration_seconds` — 按 protocol/endpoint 分组的直方图

**设计要点**：
- 使用独立 `prometheus.Registry`（非全局默认注册表）— 多模块共存不冲突
- 提供 `GinHandler()` 和 `PrometheusMiddleware()` — 直接嵌入 Gin 路由
- 同时提供 HTTP 中间件和 gRPC `UnaryServerInterceptor` — 双协议自动埋点

#### 3.2.2 EngineMetrics 业务指标扩展

**源码位置**：`engine-go/internal/observability/metrics.go` (L17-140)

```
EngineMetrics 继承 REDMetrics + 5 个业务指标：
├── ClassificationTotal      // 分类命中计数 (engine/level/domain)
├── BudgetConsumedTotal      // DP 预算消耗 (namespace/mechanism)
├── NerInferenceSeconds      // NER 推理耗时直方图 (device/batch_size)
├── APIAliasRequestsTotal    // 别名映射请求计数 (alias/canonical/target)
└── DatasourceNormalizeErrorsTotal // 标识归一化错误计数 (reason)
```

**指标命名规范**：统一 `privshield_` 前缀，标签使用有界枚举值（防止高基数爆炸）

#### 3.2.3 GatewayMetrics 网关指标

**源码位置**：`engine-go/internal/observability/gateway_metrics.go` (L19-137)

对齐设计文档 §11.1 网关指标规约：
| 指标名 | 类型 | 标签 | 含义 |
|---|---|---|---|
| `privshield_gateway_backend_in_flight` | Gauge | node_id, backend_addr | 实时在途请求数 |
| `privshield_gateway_backend_ewma_latency_seconds` | Gauge | node_id | EWMA 延迟 |
| `privshield_gateway_circuit_breaker_state` | Gauge | node_id, state | 熔断器状态 (0/1/2) |
| `privshield_gateway_requests_total` | Counter | node_id, status | 转发请求总数 |

#### 3.2.4 OpenTelemetry 追踪集成

**源码位置**：`pkg/observability/tracing.go` (L12-94)

**接口设计**：
```go
type Tracer interface {
    StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func())
}
```

**三种实现**：
| 实现 | 场景 | 行为 |
|---|---|---|
| `NoOpTracer` | 默认/未配置 OTel | 空操作，零开销 |
| `OTelTracer` | 配置了 `OTEL_EXPORTER_OTLP_ENDPOINT` | 占位实现（TODO: 接入 OTel SDK） |
| 全局单例 | `InitTracing()` 初始化 | `atomic.Pointer[Tracer]` + `sync.Once` |

**当前状态**：`OTelTracer.StartSpan()` 实际退化为 no-op（L32-35），需要引入 `go.opentelemetry.io/otel` SDK 实现真实 Span 创建。

#### 3.2.5 结构化日志

**源码位置**：`pkg/observability/logger.go` (L17-47) + `request_logger.go` (L16-56)

**日志初始化**：
- 基于 Go 1.21+ 标准库 `log/slog` — 零外部依赖
- 支持 JSON（默认，生产推荐）和 Text（开发调试）两种格式
- 支持 debug/info/warn/error 四级日志级别

**HTTP 请求日志字段**（`RequestLoggerWithModule`）：
```
msg: "request completed"
├── request_id: TraceID
├── method: HTTP 方法
├── path: 请求路径 + Query String
├── status: HTTP 状态码
├── latency_ms: 延迟毫秒数
├── client_ip: 客户端 IP
└── module: 模块标签（可选）
```

#### 3.2.6 分布式追踪上下文传播

**源码位置**：`pkg/observability/trace.go` (L50-126)

**TraceID 生成**（`GenerateRequestID`）：
- 格式：`req-<YYYYMMDDHHMMSS>-<纳秒>-<8位十六进制随机数>`
- 随机数来源：`crypto/rand.Read`（4 字节加密级安全随机）
- 保证纳秒级时间精度 + 加密随机后缀，碰撞概率极低

**TraceMiddleware 执行流程**：
1. 优先复用上游 `RequestID()` 中间件已生成的 `request_id`
2. 其次读取入站 `X-Request-ID` 请求头
3. 若均为空，自动生成唯一 TraceID
4. 同时写入 `X-Request-ID` + `X-Trace-ID` 双响应头
5. 注入 `request.Context()` — 下游 HTTP/gRPC 客户端自动透传

**GetTraceID 4 级防空兜底**：
1. `TraceIDContextKey`（追踪专属键）
2. `"request_id"` 键（向后兼容）
3. 入站 `X-Request-ID` 请求头
4. 即时生成新 ID

### 3.3 Day 5 Review 方法

1. **指标暴露验证**：启动 engine-go，`curl localhost:8079/metrics`，确认所有指标名称和标签正确
2. **追踪链路验证**：配置 `OTEL_EXPORTER_OTLP_ENDPOINT`，发送请求后检查 Jaeger 是否看到 Span
3. **日志格式验证**：确认 JSON 格式日志包含所有必需字段（request_id, method, path, status, latency_ms）

### 3.4 Day 5 关注问题

- 追踪模块当前为薄封装，`OTelTracer.StartSpan()` 实际是 no-op — 引擎内部（分类漏斗各阶段）未创建独立 Span
- 审计关键事件（分类命中 LLM、熔断器状态变更、mTLS 证书即将过期）是否输出标准化日志
- 缺少 Prometheus 告警规则定义（`deploy/prometheus/rules/`）— 需要补充 P99 延迟、错误率、熔断器状态等告警

---

## 周交付物清单

### 交付物 1：engine-go 网关与安全模块 Review 笔记

- [ ] P2C-EWMA 负载均衡算法理解笔记（含数学推导）
- [ ] 熔断器状态机转换图（标注所有转换条件）
- [ ] gRPC 透明代理双向流转发时序图
- [ ] BufferPool 零分配机制分析报告
- [ ] mTLS CN 白名单热重载机制对比分析（engine-go vs pkg 两套实现）
- [ ] API Key 常量时间认证安全性分析
- [ ] 32 分片令牌桶限流性能分析

### 交付物 2：发现的问题清单与改进建议

| 优先级 | 问题 | 位置 | 建议 |
|---|---|---|---|
| P1 | `OTelTracer.StartSpan()` 为 no-op | `pkg/observability/tracing.go:32` | 引入 OTel SDK 实现真实 Span 创建 |
| P1 | 缺少 Prometheus 告警规则 | `deploy/prometheus/` | 补充 P99/错误率/熔断器/证书过期告警 |
| P2 | gRPC 代理使用 `insecure` 凭证 | `grpc_proxy.go:114` | 升级为 mTLS 回源 |
| P2 | 熔断器 `halfOpenMax` 硬编码 | `circuitbreaker.go:62` | 配置化 |
| P2 | 两套 mTLS 白名单实现语义漂移 | `security/whitelist.go` vs `tlsutil/whitelist.go` | 收敛为统一实现 |
| P3 | API Key 无过期/轮转机制 | `pkg/auth/middleware.go` | 生产环境需要 Key 轮转能力 |
| P3 | 限流状态纯内存，多实例不共享 | `pkg/middleware/ratelimit.go` | 评估 Redis 共享限流需求 |

### 交付物 3：可观测性改进方案

- [ ] 分类漏斗三阶段创建子 Span（Rule → NER → LLM）
- [ ] 审计关键事件标准化日志格式
- [ ] Prometheus 告警规则补充（`deploy/prometheus/rules/`）
- [ ] Grafana 网关监控看板（InFlight/EWMA/熔断器状态）

---

## 附录：关键环境变量速查表

| 环境变量 | 默认值 | 影响模块 | 说明 |
|---|---|---|---|
| `PRIVACY_AUTH_ENABLED` | `false` | auth | 启用 API Key 认证 |
| `PRIVACY_TLS_ENABLED` | `false` | auth | 启用 TLS |
| `PRIVACY_AUTH_INTERNAL_MTLS_ENABLED` | `false` | security | 启用 gRPC mTLS |
| `PRIVACY_AUTH_MTLS_WHITELIST_FILE` | — | security | CN 白名单 YAML 路径 |
| `PRIVACY_AUTH_MTLS_ALLOWED_CNS` | — | security | 静态 CN 白名单（逗号分隔） |
| `PRIVACY_AUTH_INTERNAL_API_KEYS` | — | security | 内部 API Key（格式 `key:name:scope1,scope2`） |
| `PRIVACY_AUTH_EXTERNAL_API_KEYS` | — | security | 外部 API Key |
| `PRIVACY_RATE_LIMIT_ENABLED` | `false` | security | 启用 32 分片限流 |
| `PRIVACY_RATE_LIMIT_DEFAULT_RPS` | `100` | security | 默认每秒请求数 |
| `PRIVACY_RATE_LIMIT_DEFAULT_BURST` | `200` | security | 默认突发容量 |
| `PRIVACY_PPROF_ENABLED` | `false` | security | 启用 pprof 端点 |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | observability | OTel Collector 端点 |
| `PRIVACY_SERVICE_NAME` | `PrivShield` | observability | 服务名称（追踪标签） |
