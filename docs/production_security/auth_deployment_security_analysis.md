# 身份认证体系部署安全分析报告

> **版本**：v1.0  
> **日期**：2026-09-02  
> **适用范围**：PrivShield 全栈（engine-go / service-hub / datasource-mgr / audit-log / bff-go / app-lz）  
> **分析目标**：评估现有身份认证体系在共享大数据平台部署场景下的安全性——能否保证仅授权方调用接口

---

## 目录

- [1. 执行摘要](#1-执行摘要)
- [2. 现有安全能力清单](#2-现有安全能力清单)
- [3. 关键安全缺口分析](#3-关键安全缺口分析)
  - [3.1 Critical：部署即暴露](#31-critical部署即暴露)
    - [3.1.1 认证默认关闭](#311-认证默认关闭)
    - [3.1.2 TLS 默认关闭](#312-tls-默认关闭)
    - [3.1.3 service-hub gRPC 无鉴权（死代码）](#313-service-hub-grpc-无鉴权死代码)
  - [3.2 High：纵深防御缺失](#32-high纵深防御缺失)
    - [3.2.1 无 IP 白名单 / CIDR 访问控制](#321-无-ip-白名单--cidr-访问控制)
    - [3.2.2 API Key 无热轮转能力](#322-api-key-无热轮转能力)
    - [3.2.3 无认证失败指标与告警](#323-无认证失败指标与告警)
  - [3.3 Medium：配置陷阱](#33-medium配置陷阱)
    - [3.3.1 空 scopes 默认通配符](#331-空-scopes-默认通配符)
    - [3.3.2 遗留单 Key 环境变量全权](#332-遗留单-key-环境变量全权)
    - [3.3.3 mTLS 默认关闭](#333-mtls-默认关闭)
    - [3.3.4 middleware.Auth("") 静默放行](#334-middlewareauth-静默放行)
- [4. 攻击场景分析](#4-攻击场景分析)
  - [4.1 同平台租户嗅探明文 API Key](#41-同平台租户嗅探明文-api-key)
  - [4.2 忘记配置 AUTH_ENABLED 导致全接口开放](#42-忘记配置-auth_enabled-导致全接口开放)
  - [4.3 service-hub gRPC 绕过 REST 鉴权](#43-service-hub-grpc-绕过-rest-鉴权)
  - [4.4 API Key 泄露后无法热轮转](#44-api-key-泄露后无法热轮转)
- [5. 安全部署 Checklist](#5-安全部署-checklist)
- [6. 修复建议与优先级](#6-修复建议与优先级)
- [7. 与三级等保/密评的映射](#7-与三级等保密评的映射)

---

## 1. 执行摘要

### 结论

**当前默认配置下，无法保证安全部署到共享大数据平台。**

经过对全项目认证鉴权代码的深度审计，发现：

| 严重性 | 数量 | 说明 |
|--------|------|------|
| **Critical** | 3 | 默认配置下部署即暴露，攻击者可直接调用接口 |
| **High** | 3 | 纵深防御缺失，单点突破后无后备防线 |
| **Medium** | 4 | 配置陷阱，错误配置可导致安全降级 |

### 核心风险

1. **认证默认关闭**：`PRIVACY_AUTH_ENABLED=false`（默认值）→ 所有请求以 `AnonymousIdentity{Scopes:["*"]}` 放行，等同于无认证
2. **TLS 默认关闭**：API Key 以明文 HTTP 传输，同平台任何租户可嗅探
3. **service-hub gRPC 无鉴权**：auth interceptor 是死代码（引用未定义变量），从未注册到拦截器链

### 安全部署前提

若要在共享平台安全部署，**必须**同时满足：
- `PRIVACY_AUTH_ENABLED=true`
- `PRIVACY_TLS_ENABLED=true`
- `PRIVACY_REQUIRE_TLS=true`
- 配置 K8s NetworkPolicy 限制网络访问
- 使用 Secret Manager 管理 API Key

---

## 2. 现有安全能力清单

项目已实现的安全能力（需正确配置后生效）：

| 能力 | 实现位置 | 防御目标 | 状态 |
|------|----------|----------|------|
| **API Key 常量时间认证** | `pkg/auth/middleware.go:55-78` | 防时序攻击逐字符猜测 Token | ✅ 已实现 |
| **Scope-based 细粒度权限** | `pkg/auth/identity.go:26-33` | 最小权限原则，按接口级授权 | ✅ 已实现 |
| **路径归一化** | `pkg/auth/identity.go:50-61` | 防别名路由绕过权限校验 | ✅ 已实现 |
| **mTLS CN 白名单（gRPC）** | `pkg/tlsutil/grpc_interceptor.go:38-134` | 客户端证书身份校验 | ✅ 已实现 |
| **mTLS CN 白名单 5s 热重载** | `pkg/tlsutil/whitelist.go:166-192` | 证书吊销后及时生效 | ✅ 已实现 |
| **32 分片令牌桶限流** | `pkg/middleware/ratelimit.go:80-224` | 防 HTTP Flood / 暴力破解 | ✅ 已实现 |
| **WAF 72 条规则** | `pkg/middleware/waf.go` | SQL 注入/XSS/命令注入/路径穿越 | ✅ 已实现 |
| **Fail-closed 启动校验** | `pkg/config/security.go:ValidateFailClosed` | 非回环地址+无 Key 时拒绝启动 | ✅ 已实现 |
| **可信代理配置 (G-02)** | `pkg/middleware/middleware.go:162-164` | 防 X-Forwarded-For 伪造 | ✅ 已实现 |
| **JWT 令牌吊销 (G-05)** | `console/bff-go/internal/auth/jwt.go` | 登出后令牌立即失效 | ✅ 已实现 |
| **API Key 过期 (G-14)** | `pkg/auth/identity.go:KeyConfig.IsExpired()` | 过期凭证自动失效 | ✅ 已实现 |
| **登录失败锁定 (G-03)** | `console/bff-go/internal/auth/jwt.go` | 5 次失败 → 15 分钟锁定 | ✅ 已实现 |
| **TOTP 双因素 (G-11)** | `console/bff-go/internal/auth/totp.go` | RFC 6238 实现 | ✅ 已实现 |
| **安全头中间件** | `pkg/middleware/security_headers.go` | CSP/HSTS/X-Frame-Options | ✅ 已实现 |

---

## 3. 关键安全缺口分析

### 3.1 Critical：部署即暴露

#### 3.1.1 认证默认关闭

**代码位置**：
- `engine-go/internal/config/config.go:61` — 默认值定义
- `pkg/auth/middleware.go:105-108` — 匿名身份注入
- `engine-go/internal/grpcserver/auth.go:23` — gRPC 鉴权跳过

**问题描述**：

```go
// engine-go/internal/config/config.go
// PRIVACY_AUTH_ENABLED 默认为 false

// pkg/auth/middleware.go:105-108
if !settings.AuthEnabled {
    c.Set(IdentityContextKey, AnonymousIdentity) // Scopes: ["*"]
    c.Next()
    return
}

// engine-go/internal/grpcserver/auth.go:23
if !settings.AuthEnabled || pkgauth.IsHealthPathOrMethod(info.FullMethod) {
    return handler(ctx, req) // 直接放行
}
```

**攻击场景**：

部署到大数据平台时，若运维人员忘记设置 `PRIVACY_AUTH_ENABLED=true`（或 Helm Chart 遗漏此配置），所有 REST 和 gRPC 接口对网络可达的任何主机完全开放。

**影响范围**：engine-go REST + gRPC（全部 44 个隐私原语接口）

**修复建议**：

```go
// 方案 A：将默认值改为 true（破坏性变更，需更新文档）
authEnabled := EnvBool("PRIVACY_AUTH_ENABLED", true)

// 方案 B：增强 fail-closed 校验
// 当绑定非回环地址时，强制要求 AUTH_ENABLED=true
if !isLoopback(host) && !authEnabled {
    log.Fatal("PRIVACY_AUTH_ENABLED must be true when binding to non-loopback address")
}
```

---

#### 3.1.2 TLS 默认关闭

**代码位置**：
- `engine-go/internal/config/config.go:69-73` — 默认值定义

**问题描述**：

```go
// engine-go/internal/config/config.go:69-73
tlsEnabled := EnvBool("PRIVACY_TLS_ENABLED", false)      // 默认关闭
mtlsEnabled := EnvBool("PRIVACY_AUTH_INTERNAL_MTLS_ENABLED", false) // 默认关闭
requireTLS := EnvBool("PRIVACY_REQUIRE_TLS", false)      // 默认关闭
```

**攻击场景**：

大数据平台通常有多个租户共享网络基础设施。当 TLS 关闭时：
1. API Key 以 HTTP 明文传输
2. 同平台任何租户可通过网络嗅探（ARP 欺骗、交换机镜像、容器网络抓包）获取 API Key
3. 获取 API Key 后，攻击者可伪装成合法服务调用所有接口

**影响范围**：全栈所有服务

**修复建议**：

```go
// 强制 TLS 默认开启
tlsEnabled := EnvBool("PRIVACY_TLS_ENABLED", true)
requireTLS := EnvBool("PRIVACY_REQUIRE_TLS", true)

// 或在 fail-closed 校验中增加：
if !isLoopback(host) && !tlsEnabled {
    log.Fatal("PRIVACY_TLS_ENABLED must be true when binding to non-loopback address")
}
```

---

#### 3.1.3 service-hub gRPC 无鉴权（死代码）

**代码位置**：
- `services/service-hub/internal/grpcserver/auth.go:17-57` — 死代码
- `services/service-hub/internal/grpcserver/server.go:1060-1074` — 拦截器链未包含 auth

**问题描述**：

```go
// services/service-hub/internal/grpcserver/auth.go:24
// 问题 1：引用未定义变量 cfg
if cfg == nil || cfg.APIKey == "" {
    return handler(ctx, req)
}

// 问题 2：pkgauth.Settings 没有 APIKey/ScopeKeys 字段
settings := &pkgauth.Settings{
    AuthEnabled: true,
    APIKey:      cfg.APIKey,      // ❌ 编译错误：Settings 无此字段
    ScopeKeys:   cfg.ScopeKeys,   // ❌ 编译错误：Settings 无此字段
}

// services/service-hub/internal/grpcserver/server.go:1060-1064
// 问题 3：auth interceptor 从未注册
unaryChain := grpc.ChainUnaryInterceptor(
    UnaryRecoveryInterceptor(logger),
    UnaryLoggingInterceptor(logger),
    // ❌ 缺少 authUnaryInterceptor
)
```

**`pkgauth.Settings` 实际字段**：

```go
// pkg/auth/settings.go:22-28
type Settings struct {
    AuthEnabled  bool
    TLSEnabled   bool
    HealthNoAuth bool
    InternalKeys map[string]*KeyConfig // token -> config
    ExternalKeys map[string]*KeyConfig // token -> config
}
// 注意：没有 APIKey 和 ScopeKeys 字段
```

**攻击场景**：

即使 engine-go REST 侧正确配置了鉴权，攻击者仍可通过直接调用 service-hub gRPC 端口（默认 :50052）绕过所有鉴权，执行：
- `Dispatch` — 触发隐私处理流水线
- `ClassifyAndDispatch` — 分类+分发
- `ListTasks` / `GetTask` — 获取任务详情
- `GetPipelineTelemetry` — 获取流水线遥测

**影响范围**：service-hub gRPC 全部方法

**修复建议**：

```go
// 1. 修复 auth.go，使用正确的 Settings 字段
func authUnaryInterceptor(cfg *config.Config) grpc.UnaryInterceptor {
    return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
        if pkgauth.IsHealthPathOrMethod(info.FullMethod) {
            return handler(ctx, req)
        }
        
        settings := &pkgauth.Settings{
            AuthEnabled:  true,
            InternalKeys: cfg.ScopeKeys, // 使用正确的字段
        }
        // ... 鉴权逻辑
    }
}

// 2. 在 BuildServerOptions 中注册 interceptor
func BuildServerOptions(logger *slog.Logger, cfg *config.Config, creds credentials.TransportCredentials) []grpc.ServerOption {
    unaryChain := grpc.ChainUnaryInterceptor(
        UnaryRecoveryInterceptor(logger),
        UnaryLoggingInterceptor(logger),
        authUnaryInterceptor(cfg), // ✅ 注册鉴权拦截器
    )
    // ...
}
```

---

### 3.2 High：纵深防御缺失

#### 3.2.1 无 IP 白名单 / CIDR 访问控制

**问题描述**：

应用层无任何 IP 访问控制机制。搜索全项目代码，未发现 `CIDR`、`IPAllowlist`、`AllowedCIDR`、`NetworkPolicy` 等实现。

现有的 IP 相关机制仅用于：
- `ConfigureTrustedProxies` — 可信代理配置（用于解析 X-Forwarded-For，非访问控制）
- `RealClientIP` — 限流 key 构造和日志记录

**攻击场景**：

大数据平台中，任何能到达服务监听端口的 Pod/主机都可以尝试认证。即使 API Key 泄露，攻击者也可从任意位置发起请求。

**修复建议**：

```go
// 新增 IP 白名单中间件
func IPAllowlist(allowedCIDRs []string) gin.HandlerFunc {
    var networks []*net.IPNet
    for _, cidr := range allowedCIDRs {
        _, network, _ := net.ParseCIDR(cidr)
        networks = append(networks, network)
    }
    return func(c *gin.Context) {
        ip := net.ParseIP(RealClientIP(c))
        for _, network := range networks {
            if network.Contains(ip) {
                c.Next()
                return
            }
        }
        c.AbortWithStatusJSON(403, gin.H{"error": "IP not allowed"})
    }
}

// 环境变量配置
// PRIVACY_ALLOWED_CIDRS=10.0.0.0/8,172.16.0.0/12
```

**替代方案**：使用 K8s NetworkPolicy 在网络层限制 Pod 间访问。

---

#### 3.2.2 API Key 无热轮转能力

**代码位置**：`engine-go/internal/security/config.go:42-47`

**问题描述**：

```go
// engine-go/internal/security/config.go:42-47
var settingsOnce sync.Once
var cachedSettings *Settings

func GetSettings() *Settings {
    settingsOnce.Do(func() {
        cachedSettings = loadSettings()
    })
    return cachedSettings
}
```

API Key 在进程启动时从环境变量加载一次，之后永不刷新。若 API Key 泄露：
1. 必须重启服务才能生效新 Key
2. 重启期间攻击者可持续使用泄露的 Key
3. 在多实例部署时，需要滚动重启所有实例

**对比**：mTLS CN 白名单支持 5 秒热重载（`pkg/tlsutil/whitelist.go:166-192`），但 API Key 无此能力。

**修复建议**：

```go
// 方案 A：文件轮询热重载（与 mTLS 白名单一致）
func watchAPIKeys(path string, interval time.Duration) {
    ticker := time.NewTicker(interval)
    var lastModTime time.Time
    for range ticker.C {
        stat, _ := os.Stat(path)
        if stat.ModTime().After(lastModTime) {
            reloadKeys(path)
            lastModTime = stat.ModTime()
        }
    }
}

// 方案 B：集成 Secret Manager（Vault / K8s Secrets）
// 监听 Secret 变更事件，自动刷新 Key 缓存
```

---

#### 3.2.3 无认证失败指标与告警

**代码位置**：`pkg/metrics/metrics.go`（无认证相关指标）

**问题描述**：

认证失败仅通过 HTTP 401/403 状态码在访问日志中间接体现。无专用 Prometheus 计数器：
- 无 `auth_failures_total{service,reason}` 指标
- 无暴力破解检测（连续失败告警）
- 无异常认证模式检测

**攻击场景**：

攻击者可进行低频暴力破解（低于限流阈值），在无告警的情况下逐步尝试 API Key。

**修复建议**：

```go
// pkg/metrics/metrics.go
var (
    AuthFailuresTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "privshield_auth_failures_total",
            Help: "Total number of authentication failures",
        },
        []string{"service", "reason"}, // reason: invalid_token, expired_token, missing_token
    )
)

// pkg/auth/middleware.go
if identity == nil {
    metrics.AuthFailuresTotal.WithLabelValues("engine-go", "invalid_token").Inc()
    abortWithError(c, http.StatusUnauthorized, ...)
}
```

---

### 3.3 Medium：配置陷阱

#### 3.3.1 空 scopes 默认通配符

**代码位置**：`pkg/auth/identity.go:232`

**问题描述**：

```go
// pkg/auth/identity.go:232
// ParseAPIKeysEnv 解析时，若未指定 scopes，默认授予 ["*"]
if len(scopes) == 0 {
    scopes = []string{"*"} // 全权通配符
}
```

**攻击场景**：

运维人员配置 API Key 时遗漏 scopes 字段：
```bash
# 预期：只授予 privacy:mask 权限
PRIVACY_AUTH_INTERNAL_API_KEYS="sk-hub:service-hub"  # 缺少 scopes

# 实际：sk-hub 获得 ["*"] 全权，可调用所有接口
```

**修复建议**：

```go
// 移除默认通配符，要求显式声明 scopes
if len(scopes) == 0 {
    return nil, fmt.Errorf("key %q has no scopes; explicit scope declaration required", name)
}
```

---

#### 3.3.2 遗留单 Key 环境变量全权

**代码位置**：`engine-go/internal/security/config.go:62`

**问题描述**：

```go
// engine-go/internal/security/config.go:62
// 遗留的 PRIVACY_API_KEY / PRIVACY_AUTH_API_KEY 自动获得 ["*"] 全权
if key := os.Getenv("PRIVACY_AUTH_API_KEY"); key != "" {
    settings.InternalKeys[key] = &KeyConfig{Name: "default-internal", Scopes: []string{"*"}}
}
```

**攻击场景**：

使用遗留环境变量配置的服务，单个 API Key 拥有所有权限。若该 Key 泄露，攻击者可调用全部接口。

**修复建议**：

在文档中明确标注遗留环境变量已弃用，并在新版本中移除。

---

#### 3.3.3 mTLS 默认关闭

**代码位置**：`engine-go/internal/config/config.go:73`

**问题描述**：

```go
mtlsEnabled := EnvBool("PRIVACY_AUTH_INTERNAL_MTLS_ENABLED", false)
```

mTLS CN 白名单是 gRPC 侧唯一的身份鉴别机制。默认关闭意味着 gRPC 接口在部署时若无额外配置，无客户端证书校验。

**修复建议**：

对于内部服务间通信，默认启用 mTLS 或在文档中明确要求。

---

#### 3.3.4 middleware.Auth("") 静默放行

**代码位置**：`pkg/middleware/auth.go:52-54`

**问题描述**：

```go
// pkg/middleware/auth.go:52-54
func Auth(apiKey string) gin.HandlerFunc {
    if apiKey == "" {
        return func(c *gin.Context) { c.Next() } // 空 key 放行所有请求
    }
    // ...
}
```

**攻击场景**：

service-hub 的 `scopeAuthMiddleware` 回退到 `middleware.Auth(s.cfg.APIKey)`。若 `SERVICE_HUB_API_KEY` 为空且未配置 `SERVICE_HUB_API_KEYS`，所有请求放行。

**缓解**：`ValidateFailClosed` 在非回环地址+无 Key 时拒绝启动，但仅检查单 Key 模式。

---

## 4. 攻击场景分析

### 4.1 同平台租户嗅探明文 API Key

**前提条件**：
- TLS 未启用（默认配置）
- 大数据平台多租户共享网络

**攻击步骤**：

```
1. 攻击者在同平台启动抓包工具（tcpdump / Wireshark）
2. 过滤 HTTP Authorization 头
3. 等待合法服务调用接口
4. 从抓包中提取 Bearer Token
5. 使用提取的 Token 调用任意接口
```

**影响**：完全绕过身份认证，获取所有数据和处理能力

**防御**：启用 TLS（`PRIVACY_TLS_ENABLED=true`）

---

### 4.2 忘记配置 AUTH_ENABLED 导致全接口开放

**前提条件**：
- 运维人员部署时未设置 `PRIVACY_AUTH_ENABLED`
- 服务绑定非回环地址（生产配置）

**攻击步骤**：

```
1. 服务启动，AUTH_ENABLED=false（默认）
2. 所有请求注入 AnonymousIdentity{Scopes:["*"]}
3. 攻击者发现无需认证即可调用接口
4. 执行任意隐私原语操作
```

**影响**：44 个隐私原语接口 + 动态分类分级接口完全开放

**防御**：
- 启用 fail-closed 校验（已实现，但需确认覆盖所有服务）
- 部署清单中强制设置 `PRIVACY_AUTH_ENABLED=true`

---

### 4.3 service-hub gRPC 绕过 REST 鉴权

**前提条件**：
- service-hub REST 配置了鉴权
- service-hub gRPC 端口（:50052）网络可达

**攻击步骤**：

```
1. 攻击者扫描发现 service-hub gRPC 端口
2. 直接调用 gRPC 方法（无需认证）
3. 调用 Dispatch 触发隐私处理流水线
4. 获取任务列表和敏感数据
```

**影响**：完全绕过 REST 侧的身份认证和权限校验

**防御**：修复 service-hub gRPC auth interceptor 死代码

---

### 4.4 API Key 泄露后无法热轮转

**前提条件**：
- API Key 通过某种方式泄露（日志暴露、环境变量泄露等）
- 服务运行中

**攻击步骤**：

```
1. 攻击者获取泄露的 API Key
2. 持续使用泄露的 Key 调用接口
3. 防御方发现泄露，但无法热轮转
4. 必须安排维护窗口重启服务
5. 重启期间攻击者持续访问
```

**影响**：泄露窗口期内持续暴露

**防御**：实现 API Key 热重载机制

---

## 5. 安全部署 Checklist

部署到共享大数据平台前，**必须**完成以下配置：

### 必须项（Critical）

| 配置项 | 推荐值 | 说明 |
|--------|--------|------|
| `PRIVACY_AUTH_ENABLED` | `true` | 启用 API Key 认证 |
| `PRIVACY_TLS_ENABLED` | `true` | 启用 TLS 加密传输 |
| `PRIVACY_REQUIRE_TLS` | `true` | 强制 TLS，拒绝明文连接 |
| `PRIVACY_AUTH_INTERNAL_API_KEYS` | 配置 | 至少配置一个内部服务 Key（含明确 scopes） |
| `PRIVACY_AUTH_EXTERNAL_API_KEYS` | 配置 | 外部客户端 Key（最小权限 scopes） |
| `PRIVACY_AUTH_INTERNAL_MTLS_ENABLED` | `true` | gRPC 服务间通信启用 mTLS |

### 推荐项（High）

| 配置项 | 推荐值 | 说明 |
|--------|--------|------|
| `PRIVACY_TRUSTED_PROXIES` | 配置 | 若部署在负载均衡器后，配置可信代理 CIDR |
| K8s NetworkPolicy | 配置 | 限制可访问服务端点的 Pod |
| API Key 存储 | Secret Manager | 使用 Vault / K8s Secret，避免明文环境变量 |

### 监控项（Medium）

| 配置项 | 推荐值 | 说明 |
|--------|--------|------|
| Prometheus 告警 | 配置 | 监控 401/403 状态码突增 |
| 日志审计 | 启用 | 记录所有认证失败事件 |

---

## 6. 修复建议与优先级

| 优先级 | 修复项 | 工作量 | 影响范围 |
|--------|--------|--------|----------|
| **P0** | 修复 service-hub gRPC auth interceptor 死代码 | 0.5d | service-hub gRPC |
| **P1** | 默认值安全化：AUTH_ENABLED / TLS 默认 true | 0.5d | 全栈 |
| **P1** | 增强 fail-closed：非回环地址强制 AUTH+TLS | 0.5d | 全栈 |
| **P2** | 新增 IP 白名单中间件 | 1d | 全栈 |
| **P2** | API Key 热重载机制 | 1d | engine-go |
| **P2** | 认证失败 Prometheus 指标 | 0.5d | 全栈 |
| **P3** | 移除 scopes 默认通配符 | 0.5d | 全栈 |
| **P3** | 弃用遗留单 Key 环境变量 | 0.5d | engine-go |

---

## 7. 与三级等保/密评的映射

| 等保/密评要求 | 现有实现 | 缺口 |
|---------------|----------|------|
| **身份鉴别**（通信双方身份验证） | API Key + mTLS CN 白名单 | 默认关闭，需手动启用 |
| **访问控制**（最小权限原则） | Scope-based 细粒度权限 | 空 scopes 默认全权 |
| **通信完整性**（传输加密） | TLS 1.3 | 默认关闭 |
| **通信保密性**（数据加密） | TLS 1.3 + SM4-GCM 信封加密 | TLS 默认关闭 |
| **安全审计**（操作日志） | 请求日志 + 审计链 | 无专用认证失败指标 |
| **入侵防范**（暴力破解防护） | 登录失败锁定 (G-03) + 限流 | 无 API Key 暴力破解检测 |
| **密钥管理**（密钥轮转） | API Key 过期 (G-14) | 无热轮转，需重启服务 |

---

## 附录 A：环境变量速查

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `PRIVACY_AUTH_ENABLED` | `false` | 启用 API Key 认证 |
| `PRIVACY_TLS_ENABLED` | `false` | 启用 TLS |
| `PRIVACY_AUTH_INTERNAL_MTLS_ENABLED` | `false` | 启用 gRPC mTLS |
| `PRIVACY_REQUIRE_TLS` | `false` | 强制 TLS |
| `PRIVACY_AUTH_INTERNAL_API_KEYS` | 空 | 内部服务 API Key |
| `PRIVACY_AUTH_EXTERNAL_API_KEYS` | 空 | 外部客户端 API Key |
| `PRIVACY_AUTH_API_KEY` | 空 | 遗留单 Key（已弃用） |
| `PRIVACY_TRUSTED_PROXIES` | 空 | 可信代理 CIDR 列表 |

## 附录 B：代码位置索引

| 功能 | 文件路径 | 行号 |
|------|----------|------|
| AnonymousIdentity 定义 | `pkg/auth/identity.go` | L23 |
| 匿名身份注入 | `pkg/auth/middleware.go` | L105-108 |
| API Key 解析 | `pkg/auth/identity.go` | L177-257 |
| 默认 scopes 通配符 | `pkg/auth/identity.go` | L232 |
| 配置默认值 | `engine-go/internal/config/config.go` | L61-73 |
| settings sync.Once | `engine-go/internal/security/config.go` | L42-47 |
| service-hub gRPC auth 死代码 | `services/service-hub/internal/grpcserver/auth.go` | L17-57 |
| service-hub gRPC 拦截器链 | `services/service-hub/internal/grpcserver/server.go` | L1060-1074 |
| middleware.Auth 空 key 放行 | `pkg/middleware/auth.go` | L52-54 |
| mTLS CN 白名单热重载 | `pkg/tlsutil/whitelist.go` | L166-192 |
