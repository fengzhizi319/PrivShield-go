# 分类分级与隐私脱敏核心引擎 (privacy-engine) — 安全体系与等保三级/密评合规架构设计

> **版本**：v2.0.0（原理与实现细节深度增强版）
> **组件归属**：`services/privacy-engine`（Go module `engine-go`）
> **协议端口**：REST (Gin) `:8079` / gRPC `:50051`；网关反向代理 HTTP `:8000` / gRPC `:50000`
> **模块定位**：数联天下 · 数盾 (`PrivShield`) 核心隐私计算与动态分类分级引擎（Sidecar / Agent），对外提供 44 项隐私原语与三层分类漏斗
> **配套文档**：产品需求与验收标准见 [`./prd.md`](./prd.md)
> **对标标准**：
> - GB/T 22239-2019《信息安全技术 网络安全等级保护基本要求》（第三级）
> - GB/T 39786-2021《信息安全技术 信息系统密码应用基本要求》（第三级）
> - GM/T 0024-2014 / GB/T 38636-2020《传输层密码协议（TLCP）》（国密双证书）
> - 《中华人民共和国数据安全法》《中华人民共和国个人信息保护法》
>
> **文档约定**：本文所有代码位置均为仓库相对路径；`pkg/*` 为跨服务共享库，`internal/*` 为 `engine-go` 私有实现。环境变量前缀统一为 `AGENT_`（Agent 进程），网关侧为 `GATEWAY_`。

---

## 目录

- [1. 引擎安全定位与信任边界](#1-引擎安全定位与信任边界)
- [2. 纵深防御中间件链：有序装配原理](#2-纵深防御中间件链有序装配原理)
- [3. 身份认证与接口级 Scope 权限控制](#3-身份认证与接口级-scope-权限控制)
  - [3.9 引擎管理面架构与登录凭据体系](#39-引擎管理面架构与登录凭据体系-management-plane--credentials)
    - [3.9.1 是否有管理面？（双层管理面架构）](#391-是否有管理面双层管理面架构)
    - [3.9.2 登录密钥与凭据体系是什么？](#392-登录密钥与凭据体系是什么)
    - [3.9.3 普通用户与租户全生命周期管理系统设计](#393-普通用户与租户全生命周期管理系统设计-user--tenant-lifecycle-management)
- [4. 传输层安全：TLS 1.3 / 国密 TLCP / mTLS CN 白名单](#4-传输层安全tls-13--国密-tlcp--mtls-cn-白名单)
- [5. 隐私计算内核安全：国密算法与数据防护](#5-隐私计算内核安全国密算法与数据防护)
- [6. Web 攻击净化 WAF 与 DoS 纵深防御](#6-web-攻击净化-waf-与-dos-纵深防御)
- [7. 可观测性与诊断端点权限分级](#7-可观测性与诊断端点权限分级)
- [8. 生产威胁模型（STRIDE）与防御矩阵](#8-生产威胁模型stride与防御矩阵)
- [9. 生产安全部署 Checklist](#9-生产安全部署-checklist)
- [附录 A：环境变量总览](#附录-a环境变量总览)
- [附录 B：Scope 权限字典与路径映射](#附录-bscope-权限字典与路径映射)
- [附录 C：合规控制点映射（等保三级 / 密评）](#附录-c合规控制点映射等保三级--密评)
- [附录 D：关键安全代码文件索引](#附录-d关键安全代码文件索引)

---

## 1. 引擎安全定位与信任边界

### 1.1 为什么引擎不对外暴露

`privacy-engine` 是隐私计算的**核心执行引擎**，在整体架构中处于**内部数据安全计算域（Core Domain）**。它持有全部 44 项隐私原语、三层分类漏斗规则库与国密算法实现，一旦直接暴露于公网，攻击面将从「有限的编排接口」膨胀为「完整的密码计算能力面」——攻击者可尝试穷举参数触发 DP 预算耗尽、构造畸形 DICOM/XLSX 触发解析漏洞、或高频调用 NER/LLM 层耗尽 GPU/CPU。因此架构上强制：

- **对外唯一编排入口是 `service-hub` 调度中枢**（`:8082`）；
- `privacy-engine` **网络层禁止公网映射**，仅接受来自 `service-hub` / 受信内部组件的调用；
- 即便在专网内，每一次调用仍必须通过 **mTLS/TLCP 双向证书 + Scope 鉴权**，实现「网络隔离」与「身份最小权限」的**双重控制**（Defense in Depth：任一控制失效不导致整体沦陷）。

```
        [外部业务系统 app-lz / 第三方]
                    │  (仅可达 service-hub :8082)
                    ▼
        ┌───────────────────────────┐
        │  service-hub 调度中枢      │   ← 对外唯一入口（详见 service-hub/docs/security.md）
        └────────────┬──────────────┘
                     │ ① mTLS / TLCP 国密专网 + Scope Key
                     ▼
   ┌─────────────────────────────────────────────────────────┐
   │      privacy-engine  隐私计算核心引擎 (:8079 / :50051)    │  ← 内部计算域，网络层禁止公网映射
   │                                                           │
   │  ┌──────────────┐ ┌──────────────┐ ┌───────────────────┐ │
   │  │ 传输/边界防护 │ │ 身份与Scope  │ │ 隐私原语计算内核  │ │
   │  │ WAF/限流/隔离 │ │ 鉴权/白名单  │ │ 国密/DP/K-匿名    │ │
   │  └──────────────┘ └──────────────┘ └───────────────────┘ │
   └─────────────────────────────────────────────────────────┘
```

### 1.2 信任边界与威胁主体

| 信任域 | 组件 | 引擎假设 | 跨越边界的控制 |
|---|---|---|---|
| **外部不可信域** | 第三方系统、公网扫描器、越权业务 | 完全敌对 | 网络层不可达（K8s NetworkPolicy）+ 无内部 CA 证书 |
| **DMZ 编排域** | `service-hub`、BFF 控制台 | 半可信（可能被攻陷） | 仅授予其**完成任务所需最小 Scope**，不持 `ops:admin` |
| **内部计算域** | `privacy-engine` 各原语 | 引擎自身可信 | 明文随栈析构、不落盘、密钥用后清零 |

威胁主体因此分为两类：**外部攻击者**（试图突破边界直调引擎）与**被攻陷的内部组件**（持合法网络位置但已失陷，试图越权）。引擎的设计对二者一视同仁——都必须在**身份层（证书/Key）**与**授权层（Scope）**同时通过，网络可达性本身绝不构成授权。

### 1.3 核心安全原则

1. **计算无状态、原语纯函数**：所有脱敏/DP/K-匿名原语为**零状态纯函数**（`Privacy原语与服务状态分离`），敏感中间值随调用栈析构；引擎不落盘任何原始明文，从根源消除「静态数据泄露」。这也是并发安全的前提——无共享可变状态，故可安全地无锁分块并行。
2. **最小权限 Scope 隔离**：内部脱敏权限（`privacy:mask`、`medical:process`、`agent:process`）与运维权限（`ops:diagnostics`、`ops:admin`）严格分域，外部 Key 绝不持有内部 Scope；权限判定用**精确匹配 + 通配 `*`**，不存在前缀模糊授权带来的越权扩散。
3. **默认安全 / fail-closed**：从网络准入 → 传输证书 → 请求净化（WAF）→ 身份鉴权 → 接口 Scope → 资源限流逐层收敛，**任一层失败即拒绝**；未显式映射的路径、未知 CN、缺失的启动开关一律走「拒绝」分支，绝不因配置遗漏而静默放行。

---

## 2. 纵深防御中间件链：有序装配原理

### 2.1 为什么中间件顺序是安全属性

Gin 的 `router.Use()` 将 `gin.HandlerFunc` 按**调用顺序**压入全局链，请求到达时依次 `c.Next()` 下钻、响应时逆序回溯。**顺序本身就是安全属性**——若把 WAF 放在 Auth 之后，则未认证的攻击载荷可先消耗鉴权计算；若把身份级限流放在 Auth 之前，则无法取得身份做「按身份分桶」。因此引擎把链路拆成**两段装配**再合并为一条有序链：

- **基础设施段**（`cmd/privshield-agent/server_rest.go::newRESTServerRunner`）：受信任代理、IP 准入、Recovery、Trace、全局限流、访问日志、Prometheus；
- **安全防护段**（`internal/rest/routes.go::RegisterRoutes`，在任何业务路由注册前 `Use`）：SecurityHeaders → MaxBodySize → WAF → Auth → RateLimit(身份级)。

合并后的完整链路：

```
请求
  → IPAllowlist(AGENT_ALLOWED_CIDRS)   # 最外层网络准入，早于一切解析
  → gin.Recovery()                      # Panic 兜底，防协程崩溃宕机
  → TraceMiddleware()                   # 注入/透传 TraceID、RequestID
  → RateLimit(全局粗粒度, RateLimitRPS) # 入口总量削峰
  → RequestLogger(slog)                 # 结构化访问日志
  → PrometheusMiddleware(RED)           # 请求量/时延/错误率
  → SecurityHeaders                     # 注入安全响应头（响应阶段生效）
  → MaxBodySize(64MB)                   # 包裹 Body 为 MaxBytesReader
  → WAF(nil)                            # 攻击载荷净化
  → Auth(Scope 鉴权)                     # 常量时间 Key + 接口级 Scope
  → RateLimit(身份级令牌桶)              # 按身份+归一化路径分片
  → 业务 Handler                         # 脱敏/DP/K-匿名/分类计算
  → 响应
```

> **关键点**：`gin.New()` 创建空引擎（不内置 Logger/Recovery），确保链路顺序完全受控，避免默认中间件抢占顺序；`RegisterRoutes` 内的 `Use` 与 `main` 段的 `Use` 因 Gin 全局链特性而自动衔接为一条链，业务路由注册发生在两段之间但**不影响链的装配**。

### 2.2 受信任代理与真实客户端 IP

`middleware.ConfigureTrustedProxies(router, TrustedProxiesFromEnv("AGENT_TRUSTED_PROXIES"))` 对应三级等保 G-02：**仅当对端 TCP IP 属于可信 CIDR 时才信任 `X-Forwarded-For` / `X-Real-IP` 头**。否则攻击者可伪造 `X-Forwarded-For` 绕过 IP 准入或限流分片。`RealClientIP(c)` 优先取受 `TrustedProxies` 约束的 `c.ClientIP()`，回退剥端口/方括号的 `RemoteAddr`（兼容 IPv4/IPv6），其结果安全用于**限流 key** 与 **审计日志**。

### 2.3 统一错误信封

所有安全拦截（401/403/413/429/503）均通过 `middleware.AbortWithError`（`pkg/middleware/envelope.go`）输出**标准 5 字段信封**，并注入 `X-Request-ID` / `X-Trace-ID` 响应头：

```json
{
  "code": "FORBIDDEN",
  "message": "Forbidden: insufficient scope",
  "detail": null,
  "trace_id": "b2c1...",
  "timestamp": "2026-09-04T10:15:30.123456789Z"
}
```

**原理**：错误响应结构统一（前端/SDK 可稳定解析）、且不泄露内部实现细节（堆栈、SQL、路径等仅进内部 slog），对应「鉴权失败返回明确状态码但不泄露细节」的合规要求。

### 2.4 各层防护目标与代码位置

| 层级 | 中间件 | 防护目标 | 代码位置 |
|---|---|---|---|
| **网络准入** | `IPAllowlist` | 仅放行 `AGENT_ALLOWED_CIDRS` 内客户端 IP，未命中 403 | `pkg/middleware/ip_allowlist.go` |
| **崩溃隔离** | `gin.Recovery` | Panic 捕获，防协程泄漏宕机 | `server_rest.go` |
| **可追溯** | `TraceMiddleware` | 注入 TraceID/RequestID 贯穿全链路 | `pkg/middleware/trace.go` |
| **总量削峰** | `RateLimit`(全局) | 入口 IP 令牌桶，防突发洪泛 DoS | `pkg/middleware/ratelimit.go` |
| **传输加固** | `SecurityHeaders` | HSTS/CSP/nosniff/DENY 等 7 项响应头 | `internal/security/auth.go` → `pkg/middleware` |
| **报文约束** | `MaxBodySize` | 请求体 ≤ 64MB，超限读取失败 | `pkg/middleware/ratelimit.go` |
| **攻击净化** | `WAF` | 5 类规则命中即 403 并审计溯源 | `pkg/middleware/waf.go` |
| **身份鉴权** | `Auth` | 常量时间 API Key + 接口级 Scope | `internal/security/auth.go`、`pkg/auth` |
| **资源限流** | `RateLimit`(身份级) | 身份+归一化路径分片令牌桶 | `internal/security/auth.go` |

### 2.5 安全响应头清单（`SecurityHeadersTo`）

`SecurityHeadersMiddleware` 复用 `pkg/middleware.SecurityHeadersTo(w)` 并**额外覆写** `X-Frame-Options: DENY`（引擎为纯 API，无任何可嵌入页面，故比默认的 `SAMEORIGIN` 更严格）：

| 响应头 | 值 | 防护 |
|---|---|---|
| `Content-Security-Policy` | `default-src 'self'; object-src 'none'; frame-ancestors 'none'; ...` | XSS / 数据注入 |
| `X-Content-Type-Options` | `nosniff` | 禁止 MIME 嗅探 |
| `X-Frame-Options` | `DENY`（引擎覆写） | 点击劫持 |
| `X-XSS-Protection` | `1; mode=block` | 旧浏览器 XSS 过滤 |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` | 强制 HTTPS（HSTS） |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | 跨域路径隐私 |
| `Permissions-Policy` | `camera=(), microphone=(), geolocation=()` | 禁用敏感硬件权限 |

### 2.6 gRPC 服务端的纵深装配（与 REST 对称）

gRPC 不走 Gin 中间件链，而是在 [`internal/grpcserver/server.go`](../../../../services/privacy-engine/internal/grpcserver/server.go) 通过 `grpc.ServerOption` + 拦截器链实现等价的分层防护：

```go
builtinOpts := []grpc.ServerOption{
    grpc.MaxRecvMsgSize(64 * 1024 * 1024), // 64MB 接收上限，防 OOM
    grpc.MaxSendMsgSize(64 * 1024 * 1024), // 64MB 发送上限
    grpc.MaxConcurrentStreams(250),        // 并发流限制，防资源耗尽
}
s.WithOptions(builtinOpts...)

// 防护链由 grpcGuardInterceptors 组装，统一走 ChainUnaryInterceptor（不与已废弃的
// grpc.UnaryInterceptor 混用，否则与调用方传入的白名单拦截器之间次序不可预期）。
guardUnary, guardStream := grpcGuardInterceptors(authUnaryInterceptor)
s.WithUnaryInterceptor(guardUnary...)  // IP 准入 → 鉴权 → 身份级限流
s.WithStreamInterceptor(guardStream...) // 流式同样受 IP 准入与限流约束
if s.metrics != nil {
    s.WithUnaryInterceptor(s.metrics.UnaryServerInterceptor()) // RED 指标埋点
}
```

装配代码在 [`internal/grpcserver/guard.go`](../../../../services/privacy-engine/internal/grpcserver/guard.go)：`grpcGuardInterceptors` 按「**IP 准入 → 鉴权 → 身份级限流**」的顺序返回拦截器切片，未启用的防护自动跳过（返回 `nil` 即不挂载）。次序本身是安全属性：IP 准入置于鉴权之前，避免对非法来源做无谓的常量时间密钥比对（否则 gRPC 端口会成为免费的身份验证 oracle）；限流置于鉴权之后，才能取到身份做分片键。

**与 REST 的对应关系**：

| 安全属性 | REST 侧 | gRPC 侧 |
|---|---|---|
| 网络层准入 | `IPAllowlist(AGENT_ALLOWED_CIDRS)` | `Unary/StreamIPAllowlist`（同一份配置，`grpc_guard.go`） |
| 传输证书双向认证 | TLS/TLCP mTLS | 同（credentials 层，§4） |
| 应用层身份+Scope | `AuthMiddleware` | `authUnaryInterceptor`（§3.7） |
| 证书→方法白名单 | 主要为准入 | `Whitelist.UnaryServerInterceptor`（§4.5） |
| 身份级限流 | `RateLimit`(身份+归一化路径) | `Unary/StreamKeyedRateLimit`（身份+RPC 短方法名） |
| 报文体积约束 | `MaxBodySize(64MB)` | `MaxRecvMsgSize`/`MaxSendMsgSize(64MB)` |
| 并发资源限制 | `MaxConcurrent` | `MaxConcurrentStreams(250)` |
| 可观测埋点 | `PrometheusMiddleware` | `metrics.UnaryServerInterceptor` |

> **双路径对称性是第一性要求**：Agent 的 REST `:8079` 与 gRPC `:50051` 是同一进程、同一份数据、同一套密钥上的**两个等价入口**。任何只写在一侧的防护（历史上的 IP 准入、限流、吊销活读、`AGENT_HEALTH_NO_AUTH` 判定）都等价于「该防护根本不存在」，因为攻击者只会走没有防护的那个端口。限流分片键使用 `shortMethodName` 收敛为 RPC 短方法名，方法集合由 proto 固定，天然有界，不会像高基数 HTTP 路径那样造成桶爆炸。

**拦截器链执行语义**：链式一元拦截器按注册顺序由外到内包裹 handler，请求时逐层下钻。`DynamicWhitelist.UnaryServerInterceptor`（§4.5，由 `cmd/privshield-agent/server_grpc.go` 传入）处理证书 CN/SAN → 方法 scope，`UnaryIPAllowlist` 处理来源网段准入，`authUnaryInterceptor`（§3.7）处理 API Key Scope，`UnaryKeyedRateLimit` 处理配额；各层从不同维度（证书身份 vs 网络来源 vs 凭据身份）共同守卫同一 RPC，任一拒绝即 `codes.Unauthenticated`/`codes.PermissionDenied`/`codes.ResourceExhausted`。`MaxRecvMsgSize` 与 REST `MaxBodySize` 同为 64MB，与 §5.5 的文件载荷上限形成一致的体积约束。

### 2.6.1 网关 gRPC 透明代理：`UnknownServiceHandler` 不受拦截器链保护

`privshield-gateway` 的 gRPC 代理 `:50000` 用 `grpc.UnknownServiceHandler` 做任意方法的字节透传（`internal/gateway/grpc_proxy.go`）。这里有一个极易被忽略的 gRPC 实现事实：**未知方法的透传路径不经过 `ChainUnaryInterceptor`/`ChainStreamInterceptor`**（服务端 `serveStream` 找不到注册的 handler 时直接调用 `serveUnknownStream`，拦截器 `wrap` 只作用于已注册 handler）。因此「给 server 挂拦截器」对透明代理**完全无效**，防护必须写在被证明可达的位置：

| 防护 | 落点 | 说明 |
|---|---|---|
| 入站 CIDR 准入 | `TransparentStreamDirector` **函数内** | 与 HTTP 漏斗共用 `ENGINE_GATEWAY_ALLOWED_CIDRS`；取不到 peer 或不在白名单即 `PermissionDenied`（fail-closed） |
| 报文体积 | `MaxRecvMsgSize`/`MaxSendMsgSize(64MB)` | 未知方法仍受 server 级消息上限约束 |
| 连接资源 | `MaxConcurrentStreams(250)` + Keepalive 强制策略 | grpc-go 默认**不限制**单连接并发流，必须显式设定 |
| 熔断/负载均衡 | director 内 `node.CB.Allow()` | 三态熔断 + P2C-EWMA 选路 |

> 与之对照：网关 HTTP 侧 `:8000` 的漏斗（`IPAllowlist` → WAF → `MaxBodySize` → Auth → RateLimit）本已完整，若只加固 HTTP 而漏掉 gRPC `:50000`，则代理端口成为绕过全部七层防护的直连通道——这同样是双路径对称性问题。

---

## 3. 身份认证与接口级 Scope 权限控制

### 3.1 身份模型与 Scope 语义

认证核心数据结构定义在 [`pkg/auth/identity.go`](../../../../pkg/auth/identity.go)（已从 `engine-go/internal/security` 下沉到 `pkg/auth`，供 services / console / engine 统一复用）：

```go
type Identity struct {
    ServiceType string   // "internal"（高信任内部服务） | "external"（外部/低信任）
    Name        string   // 服务/账户名，用于日志与限流 key
    Scopes      []string // 已授予权限列表；["*"] 表示完全访问
}
func (id *Identity) HasPermission(permission string) bool { // 精确匹配或 "*"
    for _, s := range id.Scopes {
        if s == "*" || s == permission { return true }
    }
    return false
}
```

**原理**：`HasPermission` 采用**白名单式精确匹配**，通配 `*` 是唯一放宽手段；不存在 `privacy:mask*` 之类的前缀匹配，因而「授予 `privacy:mask`」不会意外扩权到 `privacy:mask:admin`。认证未启用时注入 `AnonymousIdentity`（`Scopes:["*"]`），保证本地开发零配置可用——但这只在**绑定回环地址**时被 `ValidateFailClosed` 允许（见 §4.1）。

### 3.2 API Key 配置格式与解析

Key 支持从环境变量或密钥文件配置，统一格式（[`ParseAPIKeysEnv`](../../../../pkg/auth/identity.go)）：

```
token:name:scope1,scope2[:2025-12-31T23:59:59Z][;token2:name2:scopes...]
```

解析规则（三级等保 G-14）：

1. 以 `;` 分割多条目，`SplitN(entry, ":", 3)` 先拆 `token / name / rest`；
2. `rest` 再判尾：`findExpirySeparator` 从末尾回溯，尝试把冒号后的片段按 **RFC3339** 解析——成功则识别为 `expires_at`，失败则整段视为 scopes（**兼容 scope 内含冒号的歧义**）；
3. 对 `token/name/scope` 全量 `TrimSpace`；**丢弃空 token/name 条目**；
4. scope 为空的 Key 会打 `WARN` 并**默认零权限**（`scopes=[]`），即「配了 Key 但没给权限」不会误放行。

`KeyConfig`（[`pkg/auth/settings.go`](../../../../pkg/auth/settings.go)）：

```go
type KeyConfig struct {
    Name      string
    Scopes    []string
    ExpiresAt *time.Time // nil 表示永不过期
}
func (k *KeyConfig) IsExpired() bool { return k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt) }
```

日志脱敏：`tokenPrefix(token)` 仅输出前 4 字符 + `***`，杜绝完整密钥进日志。

### 3.3 常量时间查找与侧信道防护

鉴权最关键的实现是 [`ConstantTimeLookup`](../../../../pkg/auth/middleware.go)。朴素写法 `if keys[token] != nil` 存在**两条时序泄露**：① map 命中/未命中耗时不同；② 多 Key 时短路比较耗时随匹配位置变化。攻击者可统计响应时间差逐步猜解合法 token。引擎的对策：

```go
func ConstantTimeLookup(keys map[string]*KeyConfig, token string) *KeyConfig {
    // ① 单 Key 快速路径：无需排序，直接恒定时间比对，零内存分配
    if len(keys) == 1 { for k, v := range keys {
        if subtle.ConstantTimeCompare([]byte(k), []byte(token)) == 1 { if !v.IsExpired() { return v } }
    }; return nil }

    // ② 栈上缓冲：≤8 个 key 用 [8]string 栈数组，避免每次 HTTP 请求堆分配 + GC 压力
    var stackBuf [8]string
    sortedKeys := stackBuf[:0] /* or make(...) when > 8 */
    for k := range keys { sortedKeys = append(sortedKeys, k) }
    sort.Strings(sortedKeys) // ③ 确定性迭代序：Go map 迭代顺序随机，必须排序才能恒定

    var matched *KeyConfig
    for _, key := range sortedKeys {
        // ④ 遍历全部 key 且每次都比较（不因命中而 break），耗时与命中位置无关
        if subtle.ConstantTimeCompare([]byte(key), tokenBytes) == 1 { matched = keys[key] }
    }
    if matched != nil && matched.IsExpired() { return nil } // ⑤ G-14 跳过过期
    return matched
}
```

**四条要点**：
- `subtle.ConstantTimeCompare` 逐字节异或累加、耗时仅取决于长度，杜绝 `==` 的短路时序；
- **遍历全部 Key 不提前 break**——若命中即返回，攻击者可从「越早命中越快」推断前缀；
- **排序保证确定性**：Go map 随机迭代序会让「比较次数与顺序」抖动，破坏恒定性；
- **栈上小切片**优化：热路径（每请求都走）在 ≤8 Key 时零堆分配。

`AuthenticateAPIKey` 依次查 `LiveInternalKeys()`（活密钥，若已接上 KeyStore）→ `InternalKeys`（环境变量静态快照）→ `ExternalKeys`，命中即构造对应 `ServiceType` 的 `Identity`，过期 Key 视为无效返回 `nil`；`settings == nil` 时直接返回 `nil`（fail-closed）。

### 3.4 认证中间件：全引擎只有一份实现

[`pkg/auth.AuthMiddleware`](../../../../pkg/auth/middleware.go) 是唯一实现：健康探针豁免 → 未启用鉴权透传 → 提取 `Authorization: Bearer` → 缺失 401 / 无效 401 / **Scope 不足 403**。`internal/security/auth.go::AuthMiddleware` 只是一个取单例配置的薄封装，**不再因配了密钥文件而切换到另一份副本**：

```go
func AuthMiddleware() gin.HandlerFunc {
    return pkgauth.AuthMiddleware(&GetSettings().Settings)
}
```

热轮转能力并未因此丢失——它已从「中间件层副本」下沉为「认证内核的数据源」（`Settings.LiveInternalKeys`，§3.5），于是 REST 与 gRPC 的认证、吊销、Scope 语义不可能再分叉。

> **反例（已修的缺陷类型）**：此处曾有一份 `hotReloadAuthMiddleware` 副本，它只做了认证而**遗漏了 `PermissionForRESTPath` 的 Scope 校验**，并整段将 `InternalKeys` 替换为文件密钥。后果：只要配了 `AGENT_AUTH_KEYS_FILE`，任何一把合法 Key（哪怕只有 `health:read`）都能访问 `/v1/privacy/budget`、dynclassification 写接口、`/debug/pprof` 等全部端点；同时环境变量 Key 在 REST 面静默 401（只在 gRPC 面可用）。这类「为了加一个能力而复制一份中间件」的分叉，是双路径越权问题的典型成因；现由 `internal/security/auth_keystore_test.go` 在 CI 层面锁死。

### 3.5 KeyStore 热轮转与到期失效

[`pkg/auth/keystore.go`](../../../../pkg/auth/keystore.go) 提供两种驱动：

- **文件模式** `NewKeyStore(path)`：后台 goroutine 每 **5 秒** `os.Stat` 比对 mtime，晚于上次记录则 `reload()`（读文件 → `ParseAPIKeysEnv` → 写锁原子替换 `ks.keys`）；
- **集中式 SecretWatcher** `NewKeyStoreWithWatcher`：监听 K8s Secret / Vault 事件通道，收到变更即 `ReloadContent`；
- 另有 `UpdateKeys` 供程序化原子替换。

**原理**：写路径持 `sync.RWMutex` 写锁全量替换 map，读路径（`Keys()`）持读锁并返回**深拷贝快照**——鉴权热路径读到的永远是某一时刻的一致视图，轮换过程零请求中断（多版本 Key 并存即"无缝轮换"）。配合 `ExpiresAt`，旧 Key 到期后即使仍在文件中也自动失效（G-14）。

**接入方式的关键约定**：KeyStore 必须以 `Settings.LiveInternalKeys = ks.Keys` 的形式挂到配置上，而**不得**在启动时把 `ks.Keys()` 并入 `InternalKeys`：

```go
keyStore = ks
liveKeys = ks.Keys   // 活读；并入 InternalKeys 则造成撤销绕过
```

因为 `InternalKeys` 是**启动期快照**：一旦把文件密钥合并进去，从文件删除（吊销）的 Key 仍会命中旧快照而永久有效。而 gRPC 拦截器历史上只读 `GetSettings()` 快照，等于吊销在 gRPC 面从不生效。现在两个端口都经 `AuthenticateAPIKey` 活读，做到「改文件 → 5s 内两个端口同时失效」。另为兼容既有部署，环境变量 Key 与文件 Key 取**并集**（`LiveInternalKeys` 未命中则回退 `InternalKeys`）。

### 3.6 接口级 Scope 强制校验（REST）

认证通过后，`AuthMiddleware` 用 [`PermissionForRESTPath`](../../../../pkg/auth/identity.go) 做**路径→所需权限**映射并校验：

```go
requiredPerm := PermissionForRESTPath(path)
if requiredPerm != "" && !identity.HasPermission(requiredPerm) {
    AuthForbiddenTotal.Inc()
    abortWithError(c, 403, "FORBIDDEN", "Forbidden: insufficient scope", nil)
    return
}
```

映射逻辑（节选，完整见[附录 B](#附录-bscope-权限字典与路径映射)）：

| 路径前缀/精确 | 所需 Scope | 说明 |
|---|---|---|
| `/health` `/livez` `/readyz` `/readyz/llm` | `health:read` | 探针（默认豁免鉴权） |
| `/v1/privacy/mask*` `/v1/privacy/process_file` | `privacy:mask` | 字段/记录/文件脱敏 |
| `/v1/privacy/hash` | `privacy:hash` | HMAC 散列 |
| `/v1/privacy/dp/*` `/v1/privacy/ldp/*` | `privacy:dp` | 差分隐私（消耗预算） |
| `/v1/privacy/k_anonymize*` | `privacy:kano` | K-匿名 |
| `/v1/privacy/qol/*` | `privacy:qol` | 查询混淆 |
| `/v1/privacy/budget*` | `privacy:budget` | 预算查询/重置 |
| `/v1/dynclassification/profiles/reload` | `dynclassification:write` | 规则热重载（写） |
| `/v1/dynclassification*`（其余） | `dynclassification:read` | 动态分类（读） |
| `/v1/agent*` | `agent:process` | 通用流水线 |
| `/v1/medical*` | `medical:process` | 医疗流水线 |
| `/v1/ops/*` `/ops/diagnostics` | `ops:diagnostics` | 运维诊断 |
| `/debug/pprof*` | `ops:admin` | 性能分析 |
| **`default`** | **`admin`** | **fail-closed 兜底** |

尾部斜杠先归一化（`/v1/privacy/mask/` → `/v1/privacy/mask`），防带斜杠路径绕过 Scope 校验。

### 3.7 gRPC 鉴权拦截器

gRPC 侧 [`internal/grpcserver/auth.go::authUnaryInterceptor`](../../../../services/privacy-engine/internal/grpcserver/auth.go) 与 REST 共用同一套 `pkgauth` 权限字典与同一入口认证内核：

```go
if pkgauth.IsHealthPathOrMethod(info.FullMethod) && settings.HealthNoAuth {
    return handler(ContextWithIdentity(ctx, nil), req)   // 豁免也必须走配置，不能无条件
}
if !settings.AuthEnabled {
    return handler(ContextWithIdentity(ctx, pkgauth.AnonymousIdentity), req)
}
md, _ := metadata.FromIncomingContext(ctx)
token := extractGRPCToken(md)                            // 统一解析，兼容 "Bearer <t>" 与裸 token
identity := pkgauth.AuthenticateAPIKey(&settings.Settings, token)  // 含活密钥，与 REST 同源
if identity == nil { return nil, status.Error(codes.Unauthenticated, "invalid credentials") }
requiredPerm := pkgauth.PermissionForGRPCMethod(info.FullMethod)
if requiredPerm != "" && !identity.HasPermission(requiredPerm) { return nil, status.Error(codes.PermissionDenied, "insufficient scope") }
return handler(ContextWithIdentity(ctx, identity), req)   // 身份注入 ctx，供限流/审计使用
```

**原理**：三级等保要求「通信双方身份鉴别」——REST 侧启用 API Key 时，gRPC 侧不能仅依赖 mTLS 传输层而缺失**应用层**鉴权，故 `PermissionForGRPCMethod` 对 `Mask→privacy:mask`、`DPCount→privacy:dp` 等 RPC 逐一映射，未映射方法同样 fail-closed 归 `admin`。

**与 REST 的四点行为对齐**（本次加固）：① 认证走 `AuthenticateAPIKey`，因此 `AGENT_AUTH_KEYS_FILE` 的热轮转/吊销对 gRPC 面同样即时生效（历史上 gRPC 只吃启动快照，吊销在 gRPC 面从不生效）；② 健康探针豁免受 `AGENT_HEALTH_NO_AUTH` 约束（历史上 gRPC 无条件豁免 `/Health`）；③ 认证通过后把身份注入 ctx（`ContextWithIdentity`），供 §2.6 的身份级限流分片与后续审计使用；④ token 解析收敛到 `extractGRPCToken` 单一函数，兼容裸 token 以免打断外部客户端，但两个端口共用一个解析入点，不会再出现「一边接受裸 token、另一边拒绝」的语义漂移。回归由 `grpc_route_audit_test.go` 锁定。

### 3.8 权限映射完整性三层防御（防「加路由忘配权限」）

Scope 鉴权采用**集中式 `path → permission` 映射**，与路由注册（`internal/rest/routes.go::RegisterRoutes`）**物理分离**。隐患：接口数量增长时，新增路由极易遗漏在映射函数登记——朴素实现下漏配路径要么被放行（若默认空权限）造成越权，要么被 `admin` 锁死造成可用性故障。引擎构建**三层防御闭环**：

| 层次 | 手段 | 触发时机 | 代码位置 |
|---|---|---|---|
| **① 运行时兜底** | fail-closed：未显式映射路径/方法默认归最高 `admin`，绝不因漏配而放行 | 每次请求 | `PermissionForRESTPath`/`PermissionForGRPCMethod` 的 `default` 分支 |
| **② 启动期审计** | 启动遍历全部路由，凡落入兜底 `admin`（且不在基础设施白名单）即打 `WARN` 列出 `method+path` | 进程启动 | `pkgauth.LogRoutePermissionAudit`（`RegisterRoutes` 末尾） |
| **③ CI 门禁（REST）** | 单测断言「全部路由均有显式映射」，一旦新增路由漏配即刻 `go test` 失败 | 每次 PR / `make test` | `TestAllRoutesHaveExplicitPermission`（`internal/rest/route_audit_test.go`） |
| **③ CI 门禁（gRPC）** | 遍历 proto 生成的 `PrivacyService_ServiceDesc` 全部 Unary + Stream 方法，逐个断言不得落入兜底 `admin` | 每次 PR / `make test` | `TestAllGRPCMethodsHaveExplicitPermission`（`internal/grpcserver/grpc_route_audit_test.go`） |

三层关系是**纵深冗余**而非重复：①保证运行时绝不漏放（可用性可能受损但安全）；②把 ①的「静默锁死」显性化为可观测告警；③在合并前就阻断，把问题从生产左移到 CI。

> **为何 gRPC 侧需要独立的第 ③ 层**：② 的启动期审计以 `r.Routes()`（Gin 路由表）为数据源，天然看不到 gRPC。而 gRPC 方法集不向运行时暴露（由 proto 静态决定），因此它的完整性适合用 CI 遍历 `ServiceDesc` 来保证——比启动告警更硬（直接失败而非日志）。此前正是缺这一层，`DPVectorMean` 在无人察觉的情况下静默落入 `admin` 兜底：对持有 `privacy:dp` 的合法调用方永久 403，直到有人报障才会发现。新增 RPC 忘配 Scope 从今天起是 CI 红，不是生产故障。

通用审计器 [`pkg/auth/route_audit.go`](../../../../pkg/auth/route_audit.go) 下沉共享库，三服务共用，各自传入权限函数与兜底哨兵值：

```go
func AuditRoutePermissions(routes []gin.RouteInfo, permFunc func(method, path string) string,
    fallbackPerms map[string]bool, allowFallback map[string]bool) []RoutePermissionIssue {
    var issues []RoutePermissionIssue
    for _, rt := range routes {
        if allowFallback[rt.Path] { continue }                              // 基础设施路由显式豁免
        if perm := permFunc(rt.Method, rt.Path); fallbackPerms[perm] {      // 命中兜底即问题
            issues = append(issues, RoutePermissionIssue{rt.Method, rt.Path, perm})
        }
    }
    sort.Slice(...) // 按 path、method 稳定排序，日志/测试输出确定
    return issues
}
```

privacy-engine 在 `RegisterRoutes` 末尾这样接入（`fallbackPerms={"admin"}`，`allowFallback=nil`——业务路由与 `/metrics` 均已显式映射，故不误报）：

```go
pkgauth.LogRoutePermissionAudit(nil, "privacy-engine", r.Routes(),
    func(method, path string) string { return pkgauth.PermissionForRESTPath(path) },
    map[string]bool{"admin": true}, nil)
```

> **新增接口规范动作**：在 `routes.go` 注册路由后，必须同步在 `PermissionForRESTPath` 补充对应 `case`；否则启动审计会告警、CI 门禁会拦截。对确属「有意仅 `admin` 可见」的基础设施路由（如 pprof），通过 `allowFallback` 白名单显式豁免，避免误报。

### 3.9 引擎管理面架构与登录凭据体系 (Management Plane & Credentials)

#### 3.9.1 是否有管理面？（双层管理面架构）

`privacy-engine` 拥有**图形化管理控制台生态**与**引擎内部原生管控 API** 构成的双层管理面：

```
┌────────────────────────────────────────────────────────────────────────┐
│                   管理控制台生态 (Engine Console)                      │
│                                                                        │
│   [Web 前端 :5173] ─── (HTTP JSON) ───► [BFF 代理后端 :8081]           │
│   • 44 隐私原语演练场                   • 协议分发 (REST / gRPC)        │
│   • 动态规则/标准体系管理               • 凭据代发 & 批量测试聚合       │
│   • 批量测试矩阵 (39 样例)                                             │
└──────────────────────────────────┬─────────────────────────────────────┘
                                   │ 代理访问 (REST :8079 / gRPC :50051)
                                   ▼
┌────────────────────────────────────────────────────────────────────────┐
│                 引擎原生运维与管控端点 (Internal Control & Ops APIs)    │
│                                                                        │
│  ┌──────────────────────┐ ┌──────────────────────┐ ┌─────────────────┐ │
│  │ 动态分类规则与策略管控│ │ 隐私预算管理与重置   │ │ 系统诊断与性能剖析  │ │
│  │ /v1/dynclass.../eval │ │ /v1/privacy/budget   │ │ /ops/diagnostics│ │
│  │ .../profiles/reload  │ │ .../budget/reset     │ │ /openapi.json   │ │
│  │ .../generate_profile │ │                      │ │ /debug/pprof/*  │ │
│  └──────────────────────┘ └──────────────────────┘ └─────────────────┘ │
└────────────────────────────────────────────────────────────────────────┘
```

1. **图形化测试与管理控制台 (`console/engine-console`)**：
   - **前端界面 (`console/engine-console/web`，React 18 + TS + Vite，默认端口 `:5173`)**：
     - **Privacy Playground**：44 项隐私原语（掩码脱敏、拉普拉斯/高斯差分隐私、本地 DP 随机响应与直方图估计、Mondrian K-匿名、Fisher-Yates 查询混淆）在线测试与参数调优；
     - **Dynamic Classification & Management**：动态分类分级规则与标准管理面板，支持行业标准（GB/T 35273、JR/T 0197 等）、分类领域（telecom、finance、medical 等）切换、算子查看与隐私策略配置文件（`privacy.yaml`）一键导出；
     - **Batch Test Matrix**：39 组全场景样例矩阵一键回归，支持实时切换 REST/gRPC 通信协议与详细耗时分析；
     - **System Diagnostics Panel**：引擎运行健康度、三层分类漏斗状态（L1 规则/L2 NER/L3 LLM 熔断器）与内存/CPU 指标展示。
   - **代理后端 BFF (`console/engine-console/bff-go`，默认端口 `:8081`)**：
     负责与上游 `privacy-engine` 进行 REST/gRPC 双协议代理与请求转换，解耦前端与底层引擎网络拓扑。
2. **核心内置管控与运维端点 (Internal Control & Ops APIs)**：
   - **OpenAPI 3.0.3 规范文档导出**：`GET /openapi.json`、`GET /docs/openapi.json`（需 `ops:diagnostics` 权限，供 API 工具自省）；
   - **系统运行时诊断**：`GET /ops/diagnostics`、`GET /v1/ops/diagnostics`（需 `ops:diagnostics` 权限，输出引擎版本、内存、Goroutine、活跃模块）；
   - **动态分类分级与规则管理**：
     - 查询标准与领域：`GET /v1/dynclassification/standards`、`/domains`、`/operators`（需 `dynclassification:read` 权限）；
     - 规则校验：`POST /v1/dynclassification/validate`（需 `dynclassification:read` 权限）；
     - 策略配置动态热重载：`POST /v1/dynclassification/profiles/reload`（需 `dynclassification:write` 或 `admin` 权限）；
     - 策略配置自动生成：`POST /v1/dynclassification/generate_profile`（需 `dynclassification:write` 或 `admin` 权限）；
   - **差分隐私预算生命周期管理**：
     - 预算消耗查询：`GET /v1/privacy/budget`（需 `privacy:budget` 权限）；
     - 预算强制重置：`POST /v1/privacy/budget/reset`（需 `privacy:budget` 或 `admin` 权限）；
   - **底层性能剖析 (pprof)**：`/debug/pprof/*`（生产默认关闭，需环境变量 `AGENT_PPROF_ENABLED=true` 且调用方具备最高 `ops:admin` 权限）。

#### 3.9.2 登录密钥与凭据体系是什么？

##### (1) 为什么没有传统的「账号/密码登录页面」？
`privacy-engine` 定位于**高安全内部计算域 Sidecar / 隐私中台引擎**，对标等保三级与微服务治理规范：
- 摒弃了单体架构中脆弱的弱口令、Session 会话状态与 Cookie 机制（防范撞库攻击、暴力破解与 CSRF 漏洞）；
- 采用云原生标准的 **API Key (Bearer Token) 细粒度 Scope 授权 + 传输层 mTLS CN 证书双因子准入**。

##### (2) 凭证配置环境变量与格式
- **认证开关**：`AGENT_AUTH_ENABLED=true`（开发环境默认 false 免密；生产/预发环境必须设为 true）；
- **密钥配置环境变量**：`AGENT_AUTH_API_KEYS`（或 `PRIVACY_AUTH_API_KEYS`）；
- **动态密钥文件**：`AGENT_AUTH_API_KEYS_FILE`（指向 YAML/文本密钥挂载卷，支持动态热轮换）；
- **令牌语法格式**：
  ```
  token:identity_name:scope1,scope2[:expires_at]
  ```
- **请求携带规范**：
  - REST 请求头：`Authorization: Bearer <token>` 或 `X-API-Key: <token>`
  - gRPC 元数据：`metadata.Pairs("authorization", "Bearer <token>")` 或 `metadata.Pairs("x-api-key", "<token>")`

##### (3) 典型预置密钥角色矩阵

| 角色类型 | 配置示例 (`AGENT_AUTH_API_KEYS`) | Scope 权限范围 | 适用主体与操作场景 |
|---|---|---|---|
| **超级管理员 (Admin Key)** | `admin-token-super-secret:engine-admin:admin` 或 `...:admin:*` | `admin` 或 `*` | 控制台管理员、规则配置热重载、预算重置、pprof 深度调试与所有管控 API |
| **调度中枢凭证 (Service-Hub Key)** | `service-hub-token:service-hub:agent:process,privacy:mask,privacy:dp,privacy:kano,medical:process,dynclassification:read` | 计算与脱敏相关 Scopes | `service-hub` 调度中枢唯一持有，仅能触发脱敏/DP/分类计算，无权访问运维管理/重载端点 |
| **运维监控探针凭证 (Monitor Key)** | `ops-probe-token:monitor:ops:diagnostics,health:read` | `ops:diagnostics`, `health:read` | Prometheus 监控采集、K8s 就绪探针、运维巡检调用 `/ops/diagnostics` 与 `/metrics` |

##### (4) 传输层身份鉴别（mTLS 双向证书与 CN 白名单）
除了应用层 API Key 外，传输层提供第二道身份鉴别防线：
- `AGENT_AUTH_INTERNAL_MTLS_ENABLED=true` 启用 gRPC mTLS 强制认证；
- `AGENT_AUTH_MTLS_WHITELIST_FILE` 配置允许连接的证书 Common Name（CN）白名单（如 `CN=service-hub`, `CN=privshield-client`, `CN=privshield-ops`），支持 5 秒动态热重载，非受信证书即使持有 Token 也在握手层直接阻断。

#### 3.9.3 普通用户与租户全生命周期管理系统设计 (User & Tenant Lifecycle Management)

##### (1) 现状评估与重新设计动因
在传统配置驱动模式下，API Key 的添加或权限变更往往依赖重启服务或重新加载配置文件，缺乏针对终端用户与业务租户的自主注册、权限申请、权限动态授予/收回以及账号注销等完整生命周期治理机制：
1. **用户注册与身份建档缺失**：缺乏标准化的普通用户注册入口，用户密码存储不满足等保三级关于复杂度与加盐哈希的强制要求；
2. **权限生命周期缺乏动态响应**：无法在服务运行期对用户的 Scope 权限进行热授予、热收回或降权，导致权限回收必须中断服务；
3. **缺乏状态机与防爆破锁死**：缺少账户状态（活跃/冻结/注销）管理与登录失败连续重试锁死机制；
4. **Token 与用户脱节**：静态 Token 难以追踪具体自然人或机构责任主体，审计溯源链不完整。

因此，系统全面设计并引入了统一的**普通用户与租户全生命周期管理系统**（基于 `pkg/auth` 通用用户引擎）：

```
┌────────────────────────────────────────────────────────────────────────┐
│             普通用户/租户生命周期与动态密钥状态机 (User Lifecycle)       │
│                                                                        │
│            [注册请求 POST /v1/auth/users/register]                     │
│                │                                                       │
│                ▼                                                       │
│        ┌───────────────┐        管理员审批 / 动态赋权                   │
│        │  待审批/初始态 │ ─────────────────────────────┐               │
│        └───────┬───────┘                              │               │
│                │ 默认激活 (或审批通过)                 ▼               │
│                ▼                              ┌───────────────┐        │
│        ┌───────────────┐     安全风控/冻结     │  正常激活态   │        │
│        │   Active 正常 ├─────────────────────►│ (持有 APIKey) │        │
│        └───────▲───────┘                      └───────┬───────┘        │
│                │                                      │                │
│                │ 解冻                                 │ 违规/调岗      │
│        ┌───────┴───────┐                              ▼                │
│        │Disabled 已冻结│◄───────────────────── 权限收回/密钥即刻失效   │
│        └───────┬───────┘                                               │
│                │ 账号注销 (DELETE)                                     │
│                ▼                                                       │
│        ┌───────────────┐                                               │
│        │ Deleted 已注销│ (所有关联 Token 物理吊销，LiveInternalKeys 实时清退) │
│        └───────────────┘                                               │
└────────────────────────────────────────────────────────────────────────┘
```

##### (2) 角色权限矩阵与模型设计 (RBAC + ABAC)

系统预置 8 类标准角色并支持细粒度自定义 Scope。**唯一事实源**为 [`pkg/auth/user.go::DefaultScopesForRole`](../../../../pkg/auth/user.go)（`KnownRoles` 为合法角色白名单，非法角色开户直接 `400 INVALID_ROLE`），本表必须与代码一致：

| 角色标识 (`role`) | 角色名称 | 预置权限集合 (`scopes`) | 适用主体与管理职责 |
|---|---|---|---|
| `admin` | 超级管理员 | `*`, `user:admin`, `hub:admin`, `ops:admin`, `privacy:budget` | 系统运维、规则热重载、用户权限全生命周期审批、预算强制重置、pprof 调试 |
| `operator` | 调度运营员 | `hub:dispatch`, `hub:read`, `hub:admin`, `user:read` | 日常调度与任务干预、查看用户清单 |
| `data-engineer` | 数据工程师 | `privacy:mask`, `privacy:dp`, `privacy:kano`, `medical:process`, `file:process` | 数据脱敏、差分隐私计算、K-匿名化与医疗数据流转，无权修改规则与用户 |
| `compliance-officer` | 合规审计专员 | `dynclassification:read`, `dynclassification:write`, `privacy:budget`, `ops:diagnostics`, `user:read` | 领域分类分级标准与规则定义、隐私预算消耗监控与合规审计 |
| `auditor` | 安全审计员 | `audit:read`, `ops:diagnostics`, `health:read`, `user:read` | 独立审计方：系统诊断、审计链路与用户列表查看 |
| `developer` | 算法业务开发者 | `privacy:mask`, `medical:process`, `hub:dispatch`, `hub:read`, `health:read` | 外部业务接入账号（公开自注册的默认角色），**不含 `user:read`** |
| `user` | 普通用户 | `privacy:mask`, `health:read` | 最小权限个体账号 |
| `guest` | 只读访客 | `health:read` | 仅允许访问 `/healthz`、`/readyz` 等健康探针 |

**关键授权语义（务必与代码一致）**：

1. **`user:read` 是只读 scope**：仅可查询用户清单/详情与密钥概要，**不得**据此为他人签发或吊销密钥、改密、改权、冻结（否则任何持只读审计 scope 的账号都能越权提权）。判定函数：`canViewUserAccount`（本人 | `user:read` | `user:admin`）与 `canManageUserAccount`（本人 | `user:admin`）。
2. **ABAC 主体绑定**：动态签发的 API Key 与登录会话在 `KeyConfig.Subject` 上绑定自然人/机构账号，认证后注入 `Identity.Subject`，使“本人自助”判定与审计溯源可落实到责任主体。
3. **特权角色不可自注册**：`admin`/`operator`/`compliance-officer`/`auditor` 属特权角色（`IsPrivilegedRole`），公开自注册通道一律强制降权为 `developer`，且禁止携带自定义 scope。
4. **越权签发拦截**：普通用户为自己签发 Key 时，申请的 scope 必须是自身已持权限的子集（`ErrForbiddenScope` → `403 FORBIDDEN_SCOPE`）。
5. **最后管理员保护**：降权、冻结、注销若会使系统失去最后一个活跃管理员，一律拒绝（`ErrLastAdmin` → `409 LAST_ADMIN`），防止管理面永久无主。

##### (3) 用户管理核心 API 规范

全部端点挂载在 `/v1/auth` 命名空间（由 [`pkg/auth/user_handlers.go::RegisterUserRoutes`](../../../../pkg/auth/user_handlers.go) 统一注册），成功响应为标准 5 字段信封（`code`/`message`/`data`/`trace_id`/`timestamp`），错误响应为 `code`/`message`/`detail`(可选)/`trace_id`/`timestamp`：

| 方法与路径 | 权限与访问控制 | 核心行为与联动 |
|---|---|---|
| `POST /v1/auth/login` | 公开免密 | 校验等保口令与防爆破锁定；成功下发会话 Bearer Token（默认 24h，内存态不落盘）；用户不存在与口令错误统一 `401`，抑制账号枚举 |
| `POST /v1/auth/users/register` | 公开（**仅引导期首个 admin**，或显式开启自注册）/ `user:admin` | 用户库为空时允许创建首个管理员（角色必须为 `admin`，否则 `400 INVALID_BOOTSTRAP_ROLE`）；默认关闭公开自注册（`403 SELF_REGISTER_DISABLED`） |
| `POST /v1/auth/logout` | 已认证 | 吊销当前会话 Token，同一 Token 后续请求立即 `401`；长期 API Key 不适用（`404 SESSION_NOT_FOUND`） |
| `POST /v1/auth/change-password` | 本人或 `user:admin` | 校验旧口令 + 新口令等保复杂度 + 口令历史禁重用（最近 3 个）；成功后**强制吊销该用户全部会话** |
| `GET /v1/auth/users` | `user:read` 或 `user:admin` | 输出脱敏摘要（抹除口令哈希与 Token 材料） |
| `GET /v1/auth/users/:username` | 本人或 `user:read` / `user:admin` | 用户档案 + 名下绑定的 API Key 概要 |
| `PUT /v1/auth/users/:username/permissions` | `user:admin` | 更新角色与自定义 scope；名下所有活密钥权限**毫秒级联动刷新**，无须重启 |
| `PUT /v1/auth/users/:username/status` | `user:admin` | `disabled` 时名下全部 Key 与会话立即失效（拦截器 `401`）；`active` 解冻后自动恢复 |
| `DELETE /v1/auth/users/:username` | `user:admin` | 删除账号，所有活跃 Token 从活密钥池注销 |
| `POST /v1/auth/users/:username/keys` | 本人或 `user:admin` | 请求体 `{"key_name":"etl-runner","scopes":["privacy:mask"],"ttl_seconds":2592000}`；生成 `psk_<32hex>` 随机 Token，**明文仅本次响应下发一次**，服务端只存 SHA-256 摘要 |
| `GET /v1/auth/users/:username/keys` | 本人或 `user:read` / `user:admin` | 仅输出 `key_id`/`name`/`token_prefix`/`scopes`/`expires_at`，绝不回显明文 |
| `DELETE /v1/auth/users/:username/keys/:key_id` | 本人或 `user:admin` | 立即从活密钥表剔除，下一次请求常量时间比对直接失效返回 `401` |

> **路由层与 Handler 层的权限分工**：`PermissionForRESTPath` 将 `/v1/auth/login` 与 `/v1/auth/users/register` 显式映射为 `auth:public`（认证中间件对公开路径跳过 scope 强制）；其余 `/v1/auth/*` 路径显式映射为空 scope（仅需已认证），具体授权在 Handler 内按主体（ABAC）强校验。这既避免了新端点 fail-closed 落入 `admin` 兜底（由 `TestAllRoutesHaveExplicitPermission` 门禁守护），也避免了“公开注册端点被要求管理员权限”的引导期死锁。

##### (4) 口令与会话安全控制常数（等保三级 G-03 / G-04 / G-14）

以下常数定义于 [`pkg/auth/user.go`](../../../../pkg/auth/user.go)，属编译期不可绕过的硬约束：

| 控制项 | 取值 | 对应控制点 | 说明 |
|---|---|---|---|
| 口令存储 | `bcrypt cost=12` 加盐杂凑 | G-04 | 明文严禁落盘或进日志；bcrypt 计算在写锁外执行，避免阻塞并发认证 |
| 口令长度 | `8 ~ 72` 字节 | G-04 | 上限 72 是 bcrypt 硬限制：超出部分被**静默截断**会使两个不同口令等价，故显式拒绝 |
| 字符类别 | 大写/小写/数字/特殊字符 **至少 3 类** | G-04 | `ErrPasswordWeak` |
| 禁止包含用户名 | 含**逆序**同样拒绝 | G-04 | `ErrPasswordContainsName`（独立哨兵错误，便于客户端提示） |
| 弱口令字典 | 18 项常见弱口令前缀/全等拦截 | G-04 | `ErrPasswordBlacklisted` |
| 口令历史 | 最近 **3** 个不得重用；新旧不得相同 | G-04 | `ErrPasswordReused` / `ErrPasswordSame` |
| 连续失败锁定 | **5** 次 → 锁定 **15** 分钟 | G-03 | 登录成功自动清零；锁定期返回 `429 ACCOUNT_LOCKED` + `Retry-After` |
| 登录限速 | 每 IP 每分钟 **20** 次（8 分片固定窗口） | G-03 / 抗 DoS | 超限 `429 RATE_LIMITED` + `Retry-After`；缓解口令喷洒与“故意锁死管理员” |
| 会话有效期 | 默认 = 上限 **24h** | G-14 | 会话为**内存态**，重启即失效（不持久化凭证） |
| 并发会话配额 | 每用户 **8** 个 | G-14 | 超出淘汰最早会话 |
| API Key 有效期 | 默认 **30 天**，上限 **90 天** | G-14 | `ttl_seconds=0` 归一化为 30 天而非“永不过期”；负值/超限 `400 INVALID_TTL` |
| API Key 配额 | 每用户 **32** 个活跃 Key | 抗 DoS | 认证为 O(n) 常量时间比对，须防止密钥表无界膨胀 |
| 特权操作审计 | 全量结构化 `auth_audit` 日志 | G-07 | 记录 `actor`/`target_user`/`result`/`reason`/`client_ip`/`trace_id`；严禁记录口令与明文 Token |

##### (5) 运行时零重启动态同步（双通道活密钥）

`pkg/auth/middleware.go` 在处理每一次 HTTP/gRPC 请求时，会取得“当前生效的密钥全集”并执行常量时间比对。为避免热路径重复拷贝，采用**双通道 + 版本驱动缓存**：

| 通道 | 索引方式 | 来源 | 落盘内容 |
|---|---|---|---|
| `Settings.LiveInternalKeys` | **明文 Token** | 静态 `AGENT_AUTH_API_KEYS` + `KeyStore`（`AGENT_AUTH_API_KEYS_FILE` / K8s Secret 热轮转） | 明文（由部署方控制文件权限） |
| `Settings.LiveInternalHashedKeys` | **`HashToken`（SHA-256 hex）** | `UserStore` 动态用户 API Key 与登录会话 | **仅摘要 + bcrypt 口令哈希**，明文 Token 永不落盘 |

- **版本驱动聚合器**（[`pkg/auth/live_keys.go::Aggregator`](../../../../pkg/auth/live_keys.go)）：合并后的快照仅在任一来源版本号变化时重建，其余请求零分配复用同一份**只读共享快照**；重建时对配置源做深拷贝，快照与来源内部状态无指针别名。
- **毫秒级联动**：权限调整、状态冻结/解冻、Token 签发/吊销、改密与注销都会 `bump` 版本号，活密钥表即刻原子更新，实现「配置不动、服务不启、权限秒级生效」。
- **持久化保障**：`UserStore` 采用临时文件写入 + `os.Rename` 原子替换，目录 `0700`、文件 `0600`；重启后账号、口令哈希与**有效 API Key 摘要**无损恢复（会话 Token 因内存态而失效，需重新登录）。

##### (6) 用户体系环境变量

| 变量 | 默认值 | 用途 |
|---|---|---|
| `AGENT_AUTH_USER_STORE_FILE`（兼容 `PRIVACY_USER_STORE_FILE`） | 空（纯内存） | 用户与动态密钥持久化文件路径（如 `data/users.json`）；只写入摘要与口令哈希 |
| `AGENT_AUTH_USER_SELF_REGISTER` | `false` | 是否开放公开自注册（生产建议保持关闭，账号一律由管理员开户） |
| `AGENT_AUTH_USER_SESSION_TTL` | `24h` | 登录会话有效期，支持 `24h`/`15m` 或纯秒数；超过 24h 自动收敛 |
| `AGENT_AUTH_USER_LOGIN_THROTTLE_PER_MIN` | `20` | 登录端点每 IP 每分钟最大尝试次数，`<=0` 关闭该层限速 |

> 变量名以 [`pkg/auth/user_handlers.go::userPolicyEnvTable`](../../../../pkg/auth/user_handlers.go) 为唯一事实源（service-hub 侧前缀为 `SERVICE_HUB_`），编排清单与本表须保持一致，否则会被 `scripts/check_orchestration_env_consistency.sh` 门禁判定为幽灵变量。

---

## 4. 传输层安全：TLS 1.3 / 国密 TLCP / mTLS CN 白名单

引擎的传输层安全解决两个问题：**信道机密性与完整性**（防止中间人窃听/篡改脱敏请求与结果）以及**通信双方身份真实性**（防止假冒客户端或假冒引擎）。在政务云/密评场景下还额外要求**使用国密算法套件**。本章自底向上分四层：启动期不变式 → 标准 TLS/mTLS → 国密 TLCP → 应用层 CN/SAN 白名单鉴权。

### 4.1 启动期 fail-closed 安全不变式（`ValidateFailClosed`）

**原理**：最危险的安全缺陷不是「代码有 bug」，而是「配置漏了却照常启动」——例如生产编排忘记挂证书，服务却以明文方式成功启动对外提供脱敏，运维毫无感知。为杜绝这类**静默降级**，[`pkg/config/security.go::ValidateFailClosed`](../../../../pkg/config/security.go) 在进程 `Validate()` 阶段强制一组**安全不变式**，任一命中即上抛错误、`main()` 终止进程（fail-fast），把「配置错误」从运行期故障提前到启动期崩溃。

核心判据是**监听面是否可被远端触达**：

```go
remoteExposed := false
for _, h := range req.Hosts {
    if !IsLoopbackHost(h) { remoteExposed = true; break } // 0.0.0.0 / :: / 网卡 IP / 无法解析均视为对外
}
```

[`IsLoopbackHost`](../../../../pkg/config/security.go) 把空串、`localhost`、`127.0.0.0/8`、`::1` 判为本地；`0.0.0.0`、`::`、`*`、具体网卡 IP 以及**无法解析的主机名**一律判为对外暴露（**连解析失败都按暴露处理**，是典型的 fail-closed 取向）。据此派生以下不变式（对应 7 个哨兵错误）：

| 不变式 | 条件 | 哨兵错误 | 防御目标 |
|---|---|---|---|
| 远端可达须有 Key | `remoteExposed && APIKey==""` | `ErrAPIKeyRequired` | 防空 API Key 裸奔 |
| 远端可达须启鉴权 | `remoteExposed && !AuthEnabled` | `ErrAuthRequired` | 防鉴权开关漏开 |
| 远端可达须启 TLS | `!SkipTLSForRemote && remoteExposed && !TLSEnabled` | `ErrTLSRequiredForRemote` | 防明文传输 |
| 声明要求 TLS 却未启用 | `RequireTLS && !TLSEnabled` | `ErrTLSRequired` | 防生产漏配证书仍服务 |
| gRPC TLS 须配 CN 白名单 | `TLSEnabled && GRPCEnabled && MTLSWhitelistFile==""` | `ErrMTLSWhitelistRequired` | 防「以为双向认证、实则拦截器未注册」 |
| 存证须加密密钥 | `RequireEncryptionKey && EncryptionKey=="" && remoteExposed` | `ErrEncryptionKeyRequired` | 防快照明文落盘（audit-log） |
| 存证须链密钥 | `RequireHashKey && HashKey=="" && remoteExposed` | `ErrChainKeyRequired` | 防无密钥哈希可被重算伪造 |

> **`SkipTLSForRemote` 的用途**：当 TLS 在反向代理 / Envoy / Ingress 层终结、引擎只在内网明文回环段被代理访问时，由部署方显式置真跳过第 3 条 TLS 强制；其余不变式仍生效。这是一个「有意识的例外」而非「遗漏」，需运维显式声明。

对 privacy-engine 而言，`SecurityRequirements` 通常传入 REST/gRPC 两个监听地址、`AuthEnabled=AGENT_AUTH_ENABLED`、`TLSEnabled=AGENT_TLS_ENABLED`、`GRPCEnabled`、`MTLSWhitelistFile=AGENT_AUTH_MTLS_WHITELIST_FILE`。注意 `RequireEncryptionKey`/`RequireHashKey` 主要服务于 audit-log 的存证落盘，引擎自身不落盘明文故一般为 false。此外当 `remoteExposed && len(AllowedCIDRs)==0` 时打 `slog.Warn`（非致命），提示未设 IP 准入白名单。

### 4.2 标准 TLS 1.3 与 mTLS 双向认证

当 `AGENT_TLS_ENABLED=true` 且未开启国密时，REST/gRPC 走 Go 标准库 `crypto/tls`，最低协议版本锁定 **TLS 1.3**（禁用 1.0/1.1/1.2 的弱套件与前向不安全曲线）。mTLS（mutual TLS）要求**服务端与客户端互换并校验证书**：服务端下发证书 + 客户端证书经 CA 校验，握手期双向验签。这满足三级等保「通信实体双向身份鉴别」要求。

**证书体系**见 `config/certs/`（CA / server / client 三件套）。生产部署应由内部 CA 签发、通过 K8s Secret 挂载，禁止自签裸证书直接对外。证书文件路径经 `AGENT_TLS_*`（或中台侧 `PRIVACY_AUTH_*`）环境变量注入。

> **mTLS 与 API Key 的关系**：二者是**互补而非替代**。mTLS 在传输层证明「进程身份」（哪个 Pod/服务），API Key + Scope 在应用层证明「调用授权」（允许调哪些方法）。`ErrMTLSWhitelistRequired` 正是强制：既然开了 TLS 双向，就必须配套 CN 白名单把「证书可信」落到「方法可授权」，否则任何通过 CA 校验的客户端都能调用全部 RPC。

### 4.3 国密 TLCP 双证书（GB/T 38636-2020）

**为什么需要 TLCP**：密评（GB/T 39786-2021）三级要求传输层使用**国产密码算法**。Go 标准库 `crypto/tls` 不原生支持 SM2/SM3/SM4 套件，因此引擎基于 `github.com/tjfoc/gmsm/gmtls` 提供应用层 TLCP（传输层密码协议，又称「国密双证书 HTTPS」）替代方案。

**TLCP 与普通 TLS 的核心差异是「双证书」**——普通 TLS 一张证书既签名又加密密钥交换；TLCP 拆成**签名证书链**与**加密证书链**两套 SM2 证书，分别用于身份签名认证与会话密钥加解密，符合 GM/T 0024 的密钥分离原则（签名密钥长期、加密密钥可由 KMC 托管分发）。

构造逻辑在 [`pkg/tlsutil/tlcp.go::BuildTLCPConfig`](../../../../pkg/tlsutil/tlcp.go)：

```go
func BuildTLCPConfig(cfg *TLCPConfig) (*gmtls.Config, error) {
    if !cfg.Enabled { return nil, nil }                         // 未启用 → 返回 nil，调用方回退标准 TLS
    if cfg.SignCertFile == "" || cfg.SignKeyFile == "" {
        return nil, fmt.Errorf("TLCP: sign cert file and sign key file are required") // 签名证书必填 → fail-fast
    }
    signCert, signKey, _ := loadSM2CertFromPEM(...)             // 解析 SM2 签名证书/私钥
    certs := []gmtls.Certificate{{Certificate: [][]byte{signCert.Raw}, PrivateKey: signKey, Leaf: signCert}}
    if cfg.EncCertFile != "" && cfg.EncKeyFile != "" {          // 加密证书可选（双向/双证书场景）
        encCert, encKey, _ := loadSM2CertFromPEM(...)
        certs = append(certs, gmtls.Certificate{...})
    }
    config := &gmtls.Config{
        Certificates: certs,
        GMSupport:    &gmtls.GMSupport{WorkMode: gmtls.ModeGMSSLOnly}, // 仅国密套件，禁止回退到标准 TLS 协商
    }
    // 双向：ClientAuth ∈ {require/requireandverify→RequireAndVerifyClientCert, verify→VerifyClientCertIfGiven, request→RequestClientCert}
}
```

**关键设计点**：
- `ModeGMSSLOnly` 强制只协商国密套件，杜绝降级到国际算法（防算法降级攻击，对应密评「不得使用弱算法」）。
- 签名证书缺失即返回 error（不静默回退），加密证书可选——单向 TLCP 至少签名证书，双向再需客户端 SM2 证书。
- `loadSM2CertFromPEM` 用 `tjfoctx509.ParseCertificate` + `ReadPrivateKeyFromPem` 解析 SM2 专用 ASN.1 结构（与标准 X.509 不同）。
- `ClientCAFile` 在启用双向时必填，构造 `tjfoctx509.CertPool` 校验客户端证书。

环境变量由 [`TLCPConfigFromEnv(prefix)`](../../../../pkg/tlsutil/tlcp.go) 读取（机制与策略分离，基础包不硬编码前缀，由调用方传入 `AGENT_`）：

| 变量（前缀 `AGENT_`） | 含义 |
|---|---|
| `TLS_NATIONAL_CIPHER` | 是否启用 TLCP（`IsTLCPEnabled` 检查此开关） |
| `TLCP_SIGN_CERT_FILE` / `TLCP_SIGN_KEY_FILE` | 服务端 SM2 签名证书/私钥 |
| `TLCP_ENC_CERT_FILE` / `TLCP_ENC_KEY_FILE` | 服务端 SM2 加密证书/私钥 |
| `TLCP_CLIENT_CA_FILE` | 客户端根 CA（双向必填） |
| `TLCP_CLIENT_AUTH` | 客户端认证模式 require/requireandverify/verify/request |

`--tlcp` 启动模式与 `--mtls` 互斥：二者分别驱动标准 TLS 与国密 TLCP 两条证书路径。监听用 `NewTLCPListener` 直接替换 `net.Listen + tls`。

### 4.4 动态 mTLS CN/SAN 白名单（5 秒热重载）

即便 mTLS/TLCP 已完成传输层双向认证，仍需在**应用层**把「哪个证书身份能调用哪些 RPC」细粒度收紧。这就是 [`pkg/tlsutil/whitelist.go::DynamicWhitelist`](../../../../pkg/tlsutil/whitelist.go)——一个 CN/SAN → 允许 scope 的映射，支持热重载。

**配置文件 `config/mtls-whitelist.yaml`**（双格式向下兼容）：

```yaml
version: "1.0"
clients:                              # 规范格式（design doc 标准），用 allowed_scopes
  - cn: "service-hub.privshield.internal"
    allowed_scopes: ["/PrivacyService/Process", "/AuditLog/*"]
    role: "orchestrator"
    description: "数据服务调度中枢核心客户端"
    enabled: true
  - cn: "bff-go.privshield.internal"
    allowed_scopes: ["*"]             # 全局通配
    enabled: true
# entries: [{cn, scopes}]            # 早期历史格式，clients 为空时回退
```

**热重载原理**（无第三方依赖，不用 fsnotify）：`NewDynamicWhitelist(path)` 先同步 `reload()` 一次，再拉起后台 `poll()` 协程——每 **5 秒** `os.Stat` 比对 `ModTime`，晚于上次记录即触发 `reload()`。`reload()` 在**写锁**下解析 YAML、构建全新 `map[string][]string` 并**原子整体替换** `dw.clients`；读路径（`IsAuthorized`/`CheckScope`/`GetScopes`）持**读锁**微秒级并发查询。K8s ConfigMap 挂载更新文件 mtime 即自动生效，无需重启进程。

```go
func (dw *DynamicWhitelist) CheckScope(clientCN, method string) (bool, []string) {
    dw.mu.RLock(); defer dw.mu.RUnlock()
    scopes, exists := dw.clients[clientCN]
    if !exists { return false, nil }                    // 未知 CN → fail-closed 拒绝
    for _, s := range scopes {
        if s == "*" || s == method || matchScopePattern(s, method) { return true, scopes }
    }
    return false, scopes
}
```

**三类 scope 匹配**（`matchScopePattern`）：① 全局通配 `*`；② 精确全名 `/PrivacyService/Process`；③ 前缀通配 `/AuditLog/*`（仅当模式以 `/*` 结尾时提取前缀 `/AuditLog/` 做前缀比较，高效且不易误配）。

**`enabled` 语义**：`*bool`——nil 或 true 视为启用，false 视为临时禁用（`reload` 时 `continue` 跳过），实现「保留配置但暂时下线某客户端」的运维开关。

privacy-engine 侧 [`internal/security/whitelist.go`](../../../../services/privacy-engine/internal/security/whitelist.go) 的 `WhitelistManager` 是薄封装，**委托** `pkgtlsutil.DynamicWhitelist`，对外暴露 `GetEntry/GetScopes/IsAllowed/DefaultScopes/AllEntries`，并以模块级单例 `GetWhitelistManager` 供 REST/gRPC 与诊断端点共享读取。引擎白名单变量为 `AGENT_AUTH_MTLS_WHITELIST_FILE`（中台微服务群仍用 `PRIVACY_AUTH_MTLS_WHITELIST_FILE`），快速放行 CN 可用 `AGENT_AUTH_MTLS_ALLOWED_CNS`（`NewStaticWhitelist` 构造内存白名单，每 CN 绑定 `*`）。

### 4.5 gRPC 白名单鉴权拦截器（多身份提取）

[`pkg/tlsutil/grpc_interceptor.go`](../../../../pkg/tlsutil/grpc_interceptor.go) 把白名单接入 gRPC 拦截器链，实现「证书身份 → 方法权限」双重校验。

**身份提取 `extractClientIdentities`**（不止 CN）——从 `peer.Peer.AuthInfo` 取 `credentials.TLSInfo`，深入**已验证链** `VerifiedChains[0][0]`（必须是经 CA 验证通过的链，而非客户端自报的 `PeerCertificates`，防伪造），再提取三类身份：

```go
cert := tlsInfo.State.VerifiedChains[0][0]      // 只信任经验证的链，未通过 CA 校验的证书进不来
var identities []string
for _, u := range cert.URIs    { identities = append(identities, u.String()) } // ① SAN URI（如 SPIFFE ID）
for _, dns := range cert.DNSNames { identities = append(identities, dns) }     // ② SAN DNSName
if cert.Subject.CommonName != "" { identities = append(identities, cert.Subject.CommonName) } // ③ CN
if len(identities) == 0 { return nil, status.Error(codes.Unauthenticated, "no CN or SAN identity") }
```

**为什么提取多身份**：现代云原生 PKI（如 cert-manager / Istio）常把机器身份写在 **SAN**（URI 形式的 SPIFFE ID 或 DNSName）而非 CN；只认 CN 会误拒合法客户端。`authorizeClientIdentities` 遍历身份的**任意一个**命中白名单即视为身份可信，再做 `FullMethod` scope 匹配（同 `CheckScope` 三类规则），任一环节失败返回 `codes.PermissionDenied` 并 `slog.Warn` 记录。

**一元与流式全覆盖**：`UnaryServerInterceptor()` 与 `StreamServerInterceptor()` 走同一 `extractClientIdentities + authorizeClientIdentities` 逻辑，保证所有 RPC 类型（含流式）一致鉴权。工厂 `NewWhitelistInterceptor(path)` 一次完成「加载 YAML + 启动热重载 + 返回拦截器三元组」；`path==""` 返回全 nil（禁用白名单），与 §4.1 的 `ErrMTLSWhitelistRequired` 协同——生产开 gRPC TLS 时 path 必须非空，否则启动即失败。

> **与 REST 侧 mTLS 的差异**：REST 侧 CN 白名单主要用于准入与身份标注，接口授权仍走 §3 的 API Key Scope；gRPC 侧则把证书身份直接映射到 RPC 方法 scope，两套机制共享 `pkg/auth` 权限字典与 `pkg/tlsutil` 白名单，实现「证书即身份、身份绑权限」的零信任闭环。

---

## 5. 隐私计算内核安全：国密算法与数据防护

引擎的「计算内核」直接处理敏感明文（姓名、身份证、HIV 报告、DICOM 影像），其安全目标是：**算法合规可信、密钥生命周期可控、中间明文不残留、输入不可被构造为攻击载体**。本章分国密算法、信封加密、内存清零、DP 预算、文件/影像输入防护五部分。

### 5.1 国密算法内核（SM3 / SM4 / SM2）

三级等保与密评要求优先使用国产密码算法。引擎在 [`pkg/crypto`](../../../../pkg/crypto) 自主实现 SM3/SM4，SM2 依赖 `tjfoc/gmsm`：

| 算法 | 标准 | 类型 | 关键参数 | 用途 |
|---|---|---|---|---|
| **SM3** | GB/T 32907 / GM/T 0004 | 密码杂凑 | 分组 64 字节、摘要 32 字节、初始向量 `sm3IV` | HMAC-SM3 散列/完整性/预算派生 |
| **SM4** | GB/T 32907-2016 | 分组密码 | 分组 16 字节、密钥 16 字节、32 轮、`sbox`/`fk`/`ck` | SM4-GCM 信封加密 |
| **SM2** | GM/T 0003 | 椭圆曲线公钥 | 256-bit 曲线 | TLCP 双证书签名/密钥交换 |

**为什么自实现而非直接调标准库**：Go 标准库无 SM3/SM4；且隐私原语需要「字段名感知 + ASCII 快速路径」的深度定制（如对纯 ASCII 身份证号的字符级掩码走查表快速路径，仅对含多字节汉字的值回落到完整 UTF-8 解码），故 [`pkg/crypto/sm3.go`](../../../../pkg/crypto/sm3.go)、[`sm4.go`](../../../../pkg/crypto/sm4.go) 以内联常量表（`sbox`/`fk`/`ck`）实现纯函数计算，零外部依赖、可审计、可恒定时间测试。`HMACSM3` 提供 GB/T 32918.4 标准的带密钥杂凑，用于不可逆敏感字段散列与存证链。

> **原语纯函数原则**：掩码、散列、DP 噪声均为**零状态纯函数**——输入相同则输出确定（DP 除外，其随机性来自显式随机源），不读写全局可变状态。这既是并发安全（无锁分块多核并行）的前提，也是「明文不残留」的基础：中间值随调用栈局部变量析构，不进堆上长期对象。

### 5.2 信封加密与密钥生命周期（v1/v2/v3）

针对需要密文落盘的敏感数据（如 audit-log 快照），[`pkg/crypto/envelope.go`](../../../../pkg/crypto/envelope.go) 提供基于 **SM4-GCM（AEAD 认证加密）** 的信封加密，格式历经三版演进，体现密钥生命周期的安全加固：

```
v1（历史，仅可读）: enc:v1:<Base64( 12B Nonce || 密文 || 16B Tag )>          密钥 = SHA-256(secret)[:16]
v2（当前写入）    : enc:v2:<Base64( 16B salt || 12B Nonce || 密文 || 16B Tag )>  密钥 = HKDF-SM3(secret, salt)
v3（多版本轮换）  : enc:v3:<version>:<Base64( ... )>                       密钥按 version 索引，AAD 绑定版本
```

**逐版安全动机**：
- **v1→v2（防弱派生 + 防降级）**：v1 用 `SHA-256(secret)[:16]` 直接截断派生密钥，是「短口令直接哈希截断」式弱派生。v2 改用 **RFC 5869 HKDF（Extract-then-Expand）+ SM3 杂凑**，每条记录携带**独立 16 字节随机 salt**，使得同一明文每次加密得到不同密文（语义安全），并把派生密钥**绑定到特定用途**（`hkdfInfo="PrivShield audit snapshot SM4-GCM v2"`，防跨用途密钥复用）。
- **前缀参与 AAD（关键）**：v2/v3 把 `enc:vN:` 版本前缀作为 GCM 的 **AAD（附加认证数据）** 参与认证。因此任何**剥离或改写前缀**的篡改都会导致 GCM 16 字节认证标签校验失败——**不存在「去前缀即静默降级为明文」的通道**。配合 `ErrUnencryptedValue`（读到不带任何 `enc:vN:` 前缀的值即报错，不当明文返回），彻底封死降级攻击。
- **v3（G-08 密钥轮换）**：引入密钥版本标识，`keyRegistry` 管理多版本 `KeyVersion{Version,Key,Active,CreatedAt}`——`Active` 密钥用于写入，旧版本仅用于解密历史密文，实现轮换过渡期新旧并存。`RegisterKeyVersion`/`RegisterKeyVersionsFromEnv`（前缀如 `AUDIT_CRYPTO_`）从环境变量批量注入多版本密钥。
- **`ErrEmptyKey`（fail-closed）**：写入路径未配置密钥时**直接报错拒绝落盘**，绝不静默写明文——与 §4.1 的启动期不变式互为呼应（运行期第二道防线）。

### 5.3 内存清零（防死存储消除）

派生密钥、明文缓冲用毕必须清零，否则可能残留在内存被 core dump / 换页 / 后续分配读出。[`pkg/crypto/zeroize.go::Zeroize`](../../../../pkg/crypto/zeroize.go)：

```go
func Zeroize(b []byte) {
    for i := range b { b[i] = 0 }
    runtime.KeepAlive(b) // 防止编译器把「写入后立即释放」的清零循环当作死存储优化消除
}
```

**原理**：Go 编译器/VM 可能将「赋值后不再读取」的循环判定为死代码消除，导致清零被优化掉。`runtime.KeepAlive` 强制保留对 `b` 的引用至该点，保证清零真实执行（GM/T 0115 密钥销毁要求）。

### 5.4 差分隐私预算原子记账

DP 原语的隐私保护强度由预算 \(\varepsilon, \delta\) 界定——**预算一旦耗尽，继续查询会使累计隐私损失突破保证**。因此预算记账必须是**并发准确、不可超支**的。[`services/privacy-engine/sdk/budget/budget.go::BudgetAccountant`](../../../../services/privacy-engine/sdk/budget/budget.go) 用 **`atomic.Uint64` 存 Float64 位 + 无锁 CAS 循环**实现：

```go
func (ba *BudgetAccountant) Consume(epsilon, delta float64) bool {
    ba.maybeReset()                                // 滑动窗口到期自动重置
    totalE, totalD := ba.TotalEpsilon(), ba.TotalDelta()
    // 1. 无锁 CAS 扣 ε：每次从当前 oldBits 反解真实值，超总预算则直接 false
    for {
        old := ba.usedEpsilonBits.Load(); cur := math.Float64frombits(old)
        if cur+epsilon > totalE { return false }
        if ba.usedEpsilonBits.CompareAndSwap(old, math.Float64bits(cur+epsilon)) { break }
    }
    if delta <= 0 { return true }
    // 2. 无锁 CAS 扣 δ；若 δ 超限，回滚刚扣的 ε（保持 (ε,δ) 记账原子性）
    for {
        old := ba.usedDeltaBits.Load(); cur := math.Float64frombits(old)
        if cur+delta > totalD { ba.rollbackEpsilon(epsilon); return false } // δ 溢出回滚 ε
        if ba.usedDeltaBits.CompareAndSwap(old, math.Float64bits(cur+delta)) { break }
    }
    return true
}
```

**设计要点**：
- **无锁而非互斥锁**：`math.Float64bits` 把 float64 塞进 uint64 用原子位操作，高并发查询下避免锁争用（对应「高频批量计算无锁分块多核并行」规范）。回滚保证 ε、δ 两个维度的**联合原子性**——不存在「扣了 ε 但 δ 超限导致 ε 白扣」的记账泄漏。
- **滑动窗口 `maybeReset`**：`windowSeconds` 到期把已用量归零（周期性预算，`lastResetTime` 用 `atomic.Int64`）。
- **REST 层拒付**：`NoisyCount/NoisySum/...` handler 中 `Consume` 返回 false → **HTTP 429 `BUDGET_EXHAUSTED`**，从入口阻断超支查询（`privacy:dp` scope 前置）。
- **敏感度有界（clip）**：DP 数学保证要求单记录贡献有界，故求和/均值前用 `clipValues` 把值截断到 \([lo,hi]\)，噪声尺度按截断后敏感度计算——防止异常大值放大敏感度导致实际隐私损失超预算。
- **命名空间隔离**：`PRIVACY_NAMESPACE`（默认 `default`）实现多租户预算隔离，避免跨租户预算互耗（DoS 面收敛）。单实例用内存会计；多实例应切 Redis/PostgreSQL 后端。

### 5.5 文件与影像输入防护

文件/影像脱敏是最易受**构造性攻击**的入口（路径穿越、解压炸弹、超大载荷、畸形格式）。引擎逐层设防：

| 防护 | 机制 | 代码位置 |
|---|---|---|
| **路径穿越守卫** | `filepath.Abs` + `EvalSymlinks` 解析真实路径，`filepath.Rel` 判是否以 `..` 逃出；**解析后与原始路径双重检查**，防 symlink 逃逸 | `internal/imageredact/redaction.go::isPathAllowed` |
| **目录白名单** | `AGENT_IMAGE_ALLOWED_DIRS` 限定可读根（默认 cwd 的 `data/uploads/samples/medical_images` + 系统临时目录），越界拒绝 | `allowedImageDirs` |
| **载荷上限** | REST `MaxBodySize(64MB)`；`processFile` `ParseMultipartForm(50MB)`；`agentProcess/medicalProcess` 记录数 >500 → `PAYLOAD_TOO_LARGE` | `internal/rest/routes.go` |
| **UTF-8 BOM 剥离** | CSV 解析前 `bytes.TrimPrefix(data, "\xef\xbb\xbf")`，防 BOM 干扰首列列名匹配 | `pkg/fileparse/csv.go` |
| **Zip 炸弹限制** | XLSX（zip）解压 XML 上限 `maxUncompressedXMLSize=256MB`，用 `io.LimitReader` 截断，防极小文件膨胀耗尽内存 | `pkg/fileparse/xlsx.go` |
| **影像防护** | 超过 `maxDimension=2048` 自动下采样；输出文件名 SHA-256 匿名化；磁盘防满自动清理 >200 旧文件；无法处理格式 fail-closed 返回 `[IMAGE-REDACTION-FAILED]` 占位符 | `internal/imageredact/redaction.go` |

**默认遮挡区**（`DefaultBoxes`）：头部 16% + 底部 18%（覆盖姓名/诊断/签名等高频身份区），比例坐标 \([0,1]\) 与分辨率无关。`IsImageInput` 对输入按扩展名 + `data:image/` Data URI 前缀识别，路径长度 ≥512 直接判非图像（防超长路径缓冲区探测）。

> **fail-closed 一致性**：从 §4.1 启动不变式、§5.2 `ErrEmptyKey`、到本节「无法处理格式返回占位符而非原文」，全链路贯彻「宁可拒绝/降级为无用占位符，也不泄露/放行」。这是贯穿全文的核心安全不变式。

### 5.6 三层分类漏斗的安全底线（默认拒绝 P0-2）

动态分类漏斗（规则引擎 → Small-NER → 外部 LLM 仲裁）的输出直接决定「一个字段是否被当作敏感而脱敏」——若漏斗因配置错误/LLM 不可用而**保守地判为「不敏感」**，就会导致敏感数据**漏脱敏**直接外流。这是隐私引擎最危险的失效模式。引擎以**安全底线（Safety Floor）+ 默认拒绝（default-deny）**堆叠在漏斗之上（[`internal/service/service.go`](../../../../services/privacy-engine/internal/service/service.go)）：

- **底线高于漏斗**：漏斗返回后**仍必须过服务层安全底线**——漏斗内置 SafetyFloor 只用代码默认值，只有 `Classify` 服务层才应用 `config/privacy.yaml` 绑定后的 `min_level` / `confidence_threshold` 以及 P0-2 具名默认拒绝下限（即「未列名字段的处理策略」）。
- **限制性默认（代码级 fail-closed）**：`DefaultSafetyFloorConfig().MinLevel == LevelInternal`——**配置缺失、解析失败、甚至 `min_level` 拼写错误（如 `confidntial`）都绝不静默降到 `public`**，而是保留限制性默认值（不中断启动）。单测 `TestSafetyFloorMinLevelRejectsUnknownVocabulary` / `TestApplyToSafetyFloorConfigKeepsFloorOnBadValue` 固化此约束：非法词汇必被拒绝而非降级。
- **未列名默认拒绝**：`unlisted_field_policy` / `unlisted_min_level` 解析为具名策略，同时下发给两条医疗流水线与分类仲裁链路——**未在规则中命中的字段默认按敏感处理**，而非当作安全放行（deny-by-default）。
- **规则引擎原子热替换**：`classifier atomic.Pointer[dynclassification.RuleEngine]` 用原子指针整体替换，重载过程无锁读、无中间态，避免半更新导致的分类不准。
- **NER 降级链**：诊断快照 `degradation_chain` 如实暴露 `cuda_onnx`/`onnx`（骨架不可用）→ `rule-based-ner`（当前实际装配的正则实体桩），**不伪称模型能力**，属可审计的降级透明。

> **`/readyz/llm` 与诊断健康**：LLM 层熔断不可用时 `/readyz/llm` 返回 503 `{"status":"degraded","llm":"unavailable"}`；`Diagnostics` 的 `ComponentHealth` 将 `budget_store`（耗尽→down、不足 10%→degraded）、`rules_loaded`（无规则→degraded）、`safety_floor`（未初始化→degraded）作为独立分量上报，运维可一眼看出隐私保护能力是否已退化。注意：LLM 不可用只会让分类**回落到前两层 + 安全底线**，而非放弃脱敏——底线不受 LLM 可用性影响。

---

## 6. Web 攻击净化 WAF 与 DoS 纵深防御

即便引擎不直接对外，作为纵深防御（§1.3）仍需假设「被攻陷的内部组件会转发敌意载荷」。本章两类防护：**载荷内容净化**（WAF，防注入类攻击）与**资源耗尽阻断**（限流/体积/并发，防 DoS）。

### 6.1 WAF 五类规则与预编译（G-12）

[`pkg/middleware/waf.go`](../../../../pkg/middleware/waf.go) 基于**预编译正则引擎**对 HTTP 请求多维度扫描，覆盖五大类常见 Web 攻击向量（三级等保 G-12 入侵防范）：

| 类别 `category` | 模式数 | 典型检测 | ASCII 预检字符集 |
|---|---|---|---|
| `SQL_INJECTION` | 21 | `UNION SELECT`、`OR 1=1`、`DROP TABLE`、`SLEEP()`、`LOAD_FILE()`、`information_schema` | ` '"=;()#/*\t\r\n` |
| `XSS` | 18 | `<script>`、`javascript:`、`onerror=`、`<svg onload=>`、`document.cookie`、`eval()` | `<>:(.=` |
| `COMMAND_INJECTION` | 12 | `\| cat`、`; whoami`、`&& curl`、反引号、`$()`、`system()` | `\|;&\`$()` |
| `PATH_TRAVERSAL` | 14 | `../`、`..\\`、`/etc/passwd` | `.%\\` |
| `EXPLOIT` | 7 | **Log4Shell CVE-2021-44228**（`${jndi:ldap://}`）等高危 RCE | `${(#` |

**预编译原理（G-12）**：`init()` 阶段一次性 `regexp.MustCompile` 编译全部模式为包级不可变 `wafRules`，运行时**零编译、零分配**；启动阶段编译失败即 panic（fail-fast，把正则配置错误暴露在启动）。

### 6.2 ASCII 快速路径预检（性能与安全的平衡）

朴素 WAF 对每个请求跑 72 个正则，成为性能瓶颈。[`canMatchRule`](../../../../pkg/middleware/waf.go) 用**类别特征字符预检**解决：`strings.ContainsAny(content, 特征字符集)` 若内容不含该类别可能触发正则的**任一**特殊字符（如 SQL 注入必含空格/引号/等号/分号，EXPLOIT 必含 `$`/`{`），直接**跳过整类数十个正则**。效果：99% 的合规流量（如纯中文病历字段）在微秒内放行，仅对「含可疑字符」的内容才跑完整正则。

> **原理**：这是「布隆式」快速否定——预检字符集是每类正则必需字符的并集下近似，不命中则必不匹配（无误删），命中才进入精确正则（误报可控）。把安全检测从 O(正则数) 降为绝大多数请求的 O(1) 字符扫描。

### 6.3 多维度扫描与请求体重建

[`WAF(logger)`](../../../../pkg/middleware/waf.go) 扫描四个维度：① URL 路径；② 原始查询串 `RawQuery`；③ 关键请求头（`User-Agent`/`Referer`/`Cookie`/`X-Forwarded-For`/`Content-Type`，攻击者常经头部注入）；④ 请求体（仅 `x-www-form-urlencoded`/`multipart/form-data`/`application/json`）。

**请求体安全读取**：受 `maxBodyScanSize=1MiB` 限制（`io.LimitReader`，防超大 payload 内存压力）；扫描后通过 `io.NopCloser(io.MultiReader(bytes.NewReader(bodyBytes), c.Request.Body))` **重建 Body**，保证后续 Handler 可正常消费（否则读过的 Body 为空）。命中则 `AbortWithError(403, "WAF_BLOCKED", ..., {category})` 并 `slog.Warn` 记录完整上下文（request_id、category、client_ip、path、target、截断至 `maxPayloadLogLen=512` 的 payload、matched_pattern）。

> **载荷截断双重含义**：`maxBodyScanSize`（1MiB）防扫描时内存耗尽；`maxPayloadLogLen`（512）防日志膨胀与敏感内容大段进日志（后者只记摘要，不记完整明文）。

### 6.4 32 分片令牌桶限流（防 Hash-Flooding）

[`pkg/middleware/ratelimit.go`](../../../../pkg/middleware/ratelimit.go) 采用 **32 分片令牌桶**而非单一全局锁，是高并发下的核心防 DoS 手段：

```go
const (
    numRateLimitShards = 32
    maxBucketsPerShard = 10000 // 全实例最多 320,000 桶，防 Hash-Flooding DoS 内存耗尽
)
func (l *shardedRateLimiter) shard(key string) *rateLimitShard {
    var h uint32 = 2166136261                    // FNV-1a 初始偏移
    for i := 0; i < len(key); i++ { h ^= uint32(key[i]); h *= 16777619 } // FNV 质数
    return l.shards[h%numRateLimitShards]
}
```

**设计要点**：
- **分片降锁争用**：以 FNV-1a 哈希将 key 映射到 32 个独立 `rateLimitShard`，每片自带互斥锁，不同分片并发无争用；避免单全局锁成为吞吐瓶颈。
- **`maxBucketsPerShard` 防 Hash-Flooding**：攻击者若伪造海量不同 key（如随机 IP）可撑爆桶 map 致 OOM。分片桶数达上限时，先回收 `lastCheck` 超过 2 分钟的闲置桶，仍满则淘汰旧桶，保证内存有界。
- **后台 GC**：独立协程周期扫描（3min 检测 / 10min TTL）回收长期闲置桶，防内存持续增长泄漏。
- **429 + `Retry-After` + `X-RateLimit-Limit`**：超限返回标准限流信封。

**两层限流协作**（§2.1）：基础设施段做**入口全局粗粒度**削峰（`AGENT_RATE_LIMIT_DEFAULT_RPS=100`/`BURST=200`）；`internal/security/auth.go::RateLimitMiddleware` 做**身份级细粒度**，key=`serviceType:name:normalizedPath`（已认证身份）+ 匿名追加 IP，配合 `NormalizeRateLimitPath` 把纯数字/UUID 路径段归一为 `:id`（防高基数路径撞爆桶）。`AGENT_HEALTH_NO_RATE_LIMIT=true` 使探针不占限流额。

### 6.5 其他 DoS 缓解

| 手段 | 机制 | 代码位置 |
|---|---|---|
| **MaxBodySize** | `http.MaxBytesReader` 包裹 Body，超限读取报错（默认 32MiB，引擎 REST 配 64MB） | `pkg/middleware/ratelimit.go::MaxBodySize` |
| **MaxConcurrent** | `chan` 信号量限并发，满 → **503 `UPSTREAM_UNAVAILABLE`**（默认 1000） | `pkg/middleware/ratelimit.go::MaxConcurrent` |
| **IPAllowlist** | CIDR 白名单准入（单 IP 自动补 /32 或 /128），未命中 403，空透传 | `pkg/middleware/ip_allowlist.go` |
| **LLM 三态熔断** | 外部 LLM 层熔断器 Closed→Open→HalfOpen，故障时降级不雪崩 | 动态分类漏斗第三层 |

> **DoS 纵深**：`IPAllowlist`（网络准入）→ `MaxConcurrent`（并发上限）→ 全局 `RateLimit`（入口削峰）→ `MaxBodySize`（单请求体积）→ WAF（载荷净化）→ 身份级 `RateLimit`（按身份限流），逐层收敛，任一环节都能独立阻断一类资源耗尽攻击。

---

## 7. 可观测性与诊断端点权限分级

可观测性本身也是**攻击面**：运行时健康、指标、pprof 堆栈会泄露内部拓扑、并发度、内存布局。引擎因此把观测端点按敏感度分级授权，**默认生产关高危端点**。

### 7.1 端点分级与 Scope 映射

| 端点 | 用途 | 所需 Scope | 默认状态 |
|---|---|---|---|
| `/health` `/livez` `/readyz` `/readyz/llm` | 存活/就绪探针 | `health:read`（默认**豁免鉴权**） | 开启 |
| `/metrics` | Prometheus 指标拉取 | `ops:diagnostics`（显式登记，不落 `admin` 兜底） | 开启（`server_rest.go` 注册，在 `RegisterRoutes` 之外） |
| `/ops/diagnostics` `/v1/ops/diagnostics` | 运行时健康快照 | `ops:diagnostics` | 开启 |
| `/debug/pprof/*` | 性能剖析（heap/goroutine/profile） | `ops:admin` | **默认关闭** |

**探针豁免原理**：K8s kubelet 探针无法携带凭证，故 `AGENT_HEALTH_NO_AUTH=true`（默认）使健康端点跳过鉴权；`IsHealthPathOrMethod` 仅将**精确探针路径**列为豁免，不以路径前缀模糊判定（防 `/health/../admin` 类绕过）。该开关现在 **REST 与 gRPC 同口径**（历史上 gRPC 无条件豁免 `/Health`，把它当作一个无需凭证的免费探测面）。`AGENT_HEALTH_NO_RATE_LIMIT=true` 使探针不占限流额（否则高频探针会误伤业务额度），同样在两端口均生效。

> **`/metrics` 为何要显式登记**：它在 `RegisterRoutes` **之外**注册，不进路由审计与 REST 门禁的数据源；而 `PermissionForRESTPath` 对未登记路径 fail-closed 返回 `admin`——两者叠加的结果是：要么 Prometheus 采集器必须持有 **admin 密钥**（违反最小权限，采集器被入侵即全库可读），要么采集失效。因此显式映射为 `ops:diagnostics`，与 `/v1/ops/*` 同级。这是一次**有意放宽**（从 admin 降为运维诊断域），需与监控采集器密钥的 Scope 发放策略一并评审。

### 7.2 pprof 默认关闭与显式开关

[`registerPprof`](../../../../services/privacy-engine/internal/rest/routes.go) 仅在 `AGENT_PPROF_ENABLED=true` 时注册 `/debug/pprof/*`（映射到 `net/http/pprof` 的 Index/ cmdline/profile/symbol/heap/goroutine/allocs 等）。pprof 能 dump 堆内存（可能含瞬时明文/密钥）与全量 goroutine 栈，是高危信息泄露面，故**生产默认关闭**；即使开启，路由仍受 `PermissionForRESTPath` 映射的 `ops:admin` 保护——**双重开关**（环境变量 + Scope）降低误开风险。

### 7.3 诊断端点的两级读写区分

[`diagnosticsHandler`](../../../../services/privacy-engine/internal/rest/routes.go) 展示了一个细粒度授权典范：**同一端点的读与写分属不同权限**：

```go
func diagnosticsHandler(svc *service.PrivacyService) gin.HandlerFunc {
    return func(c *gin.Context) {
        refresh := c.Query("refresh") == "true"
        if refresh {                                    // 带 refresh=true 会触发重新采集，副作用更重
            identity := security.GetIdentity(c)
            if identity != nil && !identity.HasPermission("ops:admin") {
                middleware.AbortWithError(c, 403, "FORBIDDEN", "Forbidden: refresh requires ops:admin scope", "")
                return
            }
        }
        diag := svc.Diagnostics(refresh)                // 只读快照仅需 ops:diagnostics；主动刷需 ops:admin
        c.JSON(200, diag)
    }
}
```

**原理**：`/ops/diagnostics` 本身只需 `ops:diagnostics`（读缓存快照）；但 `?refresh=true` 会**主动触发一次完整健康重采**（可能逐下游服务探测、耗资源），构成低频 SSRF/资源耗尽面，故额外要求 `ops:admin`。这体现「同一资源的不同副作用等级 → 不同权限」的最小权限思想。

### 7.4 安全事件指标（认证可观测）

[`pkg/auth/middleware.go`](../../../../pkg/auth/middleware.go) 暴露两个 Prometheus 计数器，供告警发现凭据猜测/越权探测：

| 指标 | 类型 | 标签 | 含义 |
|---|---|---|---|
| `privshield_auth_failures_total` | CounterVec | `reason`（`missing_token`/`invalid_token`） | 认证失败数，按原因分类——异常增高提示凭据枚举攻击 |
| `privshield_auth_forbidden_total` | Counter | — | 已认证但 Scope 不足（403），增高提示内部组件尝试越权调用 |

配合 TraceMiddleware 注入的 `X-Request-ID`/`X-Trace-ID`，安全事件可从指标→日志→链路三级下钻溯源。WAF 命中、mTLS 未知 CN 拒绝、预算耗尽 429 均走结构化 `slog.Warn`，统一可被 SIEM 采集。

---

## 8. 生产威胁模型（STRIDE）与防御矩阵

STRIDE 提供系统化的威胁枚举框架。下表的每一行是一种威胁类别，**右列均指向本文前面章节已实现的具体防御**（非空泛原则），构成可验证的威胁→控制映射。

| 威胁 | 针对引擎的具体场景 | 主要防御控制 | 章节 |
|---|---|---|---|
| **S**poofing 假冒 | 伪造客户端证书/API Key 自称 `service-hub` | mTLS/TLCP 双向证书（仅信 `VerifiedChains`）+ 常量时间 Key 比对 + CN/SAN 白名单 | §3.3 §4.2 §4.5 |
| **T**ampering 篡改 | 中间人改包；剥 `enc:vN:` 前缀降级密文；注入 SQLi/命令 | TLS 1.3 完整性 + GCM 认证标签（前缀参与 AAD）+ WAF 5 类净化 | §4.2 §5.2 §6.1 |
| **R**epudiation 否认 | 越权/攻击行为无法溯源 | TraceMiddleware 贯穿 TraceID + 安全事件 `slog.Warn` + Prometheus 计数器 + `privshield_auth_*` | §2.3 §7.4 |
| **I**nformation Disclosure 泄露 | 日志/错误回显明文与堆栈；pprof dump 内存；密钥残留 | 统一 5 字段信封（细节仅内部日志）+ token 前 4 字符脱敏 + pprof 默认关 + `Zeroize` 清零 + 明文不落盘 | §2.3 §3.2 §5.3 §7.2 |
| **D**enial of Service 服务拒绝 | 洪泛请求；撑爆限流桶；解压炸弹；DP 预算耗尽；超大影像 | 32 分片令牌桶 + `maxBucketsPerShard` + MaxBodySize/MaxConcurrent + Zip 256MB 上限 + 下采样 + DP 预算 CAS 不可超支 | §5.4 §5.5 §6.4 §6.5 |
| **E**levation of Privilege 越权 | 外部 Key 持内部 Scope；新增路由漏配权限被默认放行；`refresh` 未鉴权 | fail-closed 默认 `admin` + Scope 精确匹配无模糊 + 三层防御闭环 + 诊断读写分级 | §1.3 §3.1 §3.6 §3.8 §7.3 |

> **模型要点**：引擎对**外部攻击者**与**被攻陷的内部组件**一视同仁（§1.2）——网络可达本身不构成授权，身份层（证书/Key）与授权层（Scope）必须同时通过。多个 STRIDE 威胁由同一控制交叉覆盖（如 mTLS 同时防御 Spoofing 与 Tampering），体现纵深防御的冗余性。

### 8.1 正向请求全链路时序（合法脱敏调用）

以一个持 `privacy:mask` 的内部 Key 调用 `POST /v1/privacy/mask` 为例，串起前文各层：

```
1. TCP 接入            → IPAllowlist 校验来源 IP ∈ AGENT_ALLOWED_CIDRS（否则 403）
2. mTLS/TLCP 握手      → 服务端证书 + 客户端证书经 CA 双向校验（§4.2/§4.3）
3. Recovery            → panic 兜底（若下游崩溃不宕进程）
4. TraceMiddleware     → 注入/透传 X-Request-ID / X-Trace-ID（§2.3）
5. 全局 RateLimit       → 入口 IP 令牌桶削峰（§6.4）
6. SecurityHeaders     → 预备 7 项安全响应头（§2.5）
7. MaxBodySize(64MB)   → 包裹 Body 为 MaxBytesReader（§6.5）
8. WAF                 → 扫描 path/query/headers/body，无命中放行（§6.1–§6.3）
9. Auth                → Bearer 提取 → ConstantTimeLookup 恒时比对 → Identity
                        → PermissionForRESTPath("/v1/privacy/mask")=="privacy:mask" → HasPermission 通过（§3.3/§3.6）
10. 身份级 RateLimit    → key=internal:<name>:privacy:mask 分片令牌桶（§6.4）
11. 业务 Handler        → SM4/掩码原语纯函数计算，明文随栈析构（§5.1）
12. 响应               → 统一信封 + 安全头回写；失败走 AbortWithError
```

关键点：任何一步失败都在**进入下一步之前**短路返回，敏感计算（第 11 步）是链路的**最后**一环——即“验证足、才计算”，避免未授权者消耗密码计算资源。

### 8.2 攻击场景的逐层阻断（示例）

| 攻击 | 在哪一层被阻断 | 响应 |
|---|---|---|
| 公网直连引擎 | 第 1 层网络不可达（K8s NetworkPolicy）或 IPAllowlist | TCP 拒/RST 或 403 |
| 无 CA 签发证书伪造身份 | mTLS 握手期 `VerifiedChains` 为空 | 握手失败（§4.5） |
| 合法证书但调未授权 RPC | `authorizeClientIdentities` scope 不覆盖 | `codes.PermissionDenied` |
| 外部 Key 调 `/v1/privacy/dp/count` | Auth 第 9 步 `HasPermission("privacy:dp")` 失败 | 403（计 `auth_forbidden_total`） |
| body 携 `${jndi:ldap://}` | WAF 第 8 层 EXPLOIT 规则命中 | 403 `WAF_BLOCKED` + `slog.Warn` |
| 海量随机 IP 撞限流桶 | `maxBucketsPerShard` 淘汰 + 后台 GC | 内存有界（§6.4） |
| 剥 `enc:v1:` 前缀伪造密文 | GCM 认证失败（前缀参与 AAD）/ `ErrUnencryptedValue` | 报错不当明文返回（§5.2） |
| 持续 DP 查询至预算耗尽 | `Consume` CAS 返回 false | 429 `BUDGET_EXHAUSTED`（§5.4） |

### 8.3 新增接口安全自检流程

1. 在 `routes.go` 注册路由；
2. 在 `PermissionForRESTPath`（或 `PermissionForGRPCMethod`）补对应 `case`（§3.8）；
3. 本地跑 `make test`，`TestAllRoutesHaveExplicitPermission` 会拦截漏配；
4. 启动观察 `LogRoutePermissionAudit` 是否告警；
5. 若新接口有副作用/高成本（如触发重采），按 §7.3 在 handler 内做 `ops:admin` 等细粒度二次校验。

---

## 9. 生产安全部署 Checklist

以下项均为**部署前必逐条核对**的硬指标（✅=引擎已实现，需运维正确配置才能生效）：

**启动不变式（§4.1）**
- [ ] REST/gRPC 监听非回环地址时，`AGENT_AUTH_ENABLED=true`、`AGENT_TLS_ENABLED=true`（否则 `ValidateFailClosed` 启动失败）
- [ ] 启用 gRPC TLS 时 `AGENT_AUTH_MTLS_WHITELIST_FILE` 已提供（否则 `ErrMTLSWhitelistRequired`）
- [ ] 网络层通过 K8s NetworkPolicy 确保引擎仅 `service-hub`/受信组件可达，无公网映射

**身份与权限（§3）**
- [ ] 内部/外部 Key 分域注入（`AGENT_AUTH_INTERNAL_API_KEYS` / `AGENT_AUTH_EXTERNAL_API_KEYS`），外部 Key 绝不持 `privacy:mask`/`ops:*` 等内部 Scope
- [ ] 生产 Key 通过 `AGENT_AUTH_KEYS_FILE` 或 SecretWatcher 挂载，支持热轮换；每 Key 设 `ExpiresAt`（G-14）
- [ ] CI 已接入 `TestAllRoutesHaveExplicitPermission`，新增路由必同步 `PermissionForRESTPath` 映射（§3.8）

**传输与国密（§4）**
- [ ] 证书由内部 CA 签发、K8s Secret 挂载，禁自签裸证书对外；HSTS 已启用（§2.5）
- [ ] 密评场景：`AGENT_TLS_NATIONAL_CIPHER=true` 并配齐 `AGENT_TLCP_SIGN_*`/`ENC_*` 双证书（与 `--mtls` 互斥）
- [ ] mTLS 白名单每 CN 绑定最小 `allowed_scopes`，无多余的 `*`

**内核与输入防护（§5）**
- [ ] `AGENT_IMAGE_ALLOWED_DIRS` 显式限定可读目录，不依赖默认 cwd
- [ ] 信封加密密钥（如需）通过环境变量注入，多版本密钥按 G-08 轮换
- [ ] `PRIVACY_NAMESPACE` 为各租户正确隔离，DP 总预算按业务量合理设定

**WAF / DoS（§6）**
- [ ] `AGENT_RATE_LIMIT_ENABLED=true`，RPS/Burst 按容量测试调优
- [ ] `AGENT_ALLOWED_CIDRS` 配置内部调用方网段（空则 `ValidateFailClosed` 告警）
- [ ] `AGENT_TRUSTED_PROXIES` 仅含真实代理 CIDR，防 XFF 伪造（G-02）

**可观测（§7）**
- [ ] `AGENT_PPROF_ENABLED=false`（生产）；如需开启必限 `ops:admin`
- [ ] `privshield_auth_failures_total`/`forbidden_total` 已配告警规则

---

## 附录 A：环境变量总览

引擎环境变量前缀统一为 `AGENT_`（Agent 进程），网关侧为 `ENGINE_GATEWAY_`；中台微服务群（service-hub/audit-log）的 mTLS 白名单仍用 `PRIVACY_AUTH_MTLS_WHITELIST_FILE`。

### A.1 身份认证

| 变量 | 默认 | 含义 |
|---|---|---|
| `AGENT_AUTH_ENABLED` | `false` | 是否启用 API Key 鉴权 |
| `AGENT_AUTH_INTERNAL_API_KEYS` | — | 内部高信任 Key（`token:name:scopes:RFC3339`，`;` 分隔） |
| `AGENT_AUTH_EXTERNAL_API_KEYS` | — | 外部低信任 Key |
| `AGENT_AUTH_API_KEY` | — | 单 Key（default-internal，Scope `*`） |
| `AGENT_AUTH_STATIC_API_KEYS` | — | 静态 Key（不热轮） |
| `AGENT_AUTH_KEYS_FILE` | — | Key 文件（启用 KeyStore 5s mtime 热轮转）；文件不存在/不可读则启动 fail-fast，不静默降级 |
| `AGENT_AUTH_USER_STORE_FILE`（兼容 `PRIVACY_USER_STORE_FILE`） | 空（纯内存） | 用户与动态密钥持久化文件（目录 `0700`/文件 `0600`，只存 SHA-256 摘要与 bcrypt 口令哈希）；不可读时回退内存态 |
| `AGENT_AUTH_USER_SELF_REGISTER` | `false` | 公开自注册开关（生产保持关闭；引导期首个 `admin` 不受此限） |
| `AGENT_AUTH_USER_SESSION_TTL` | `24h` | 登录会话有效期（`24h`/`15m`/纯秒数，超上限自动收敛） |
| `AGENT_AUTH_USER_LOGIN_THROTTLE_PER_MIN` | `20` | 登录端点每 IP 每分钟最大尝试次数（`<=0` 关闭该层限速） |
| `AGENT_HEALTH_NO_AUTH` | `true` | 健康探针豁免鉴权（REST `:8079` 与 gRPC `:50051` 同口径） |

### A.2 传输安全

| 变量 | 默认 | 含义 |
|---|---|---|
| `AGENT_TLS_ENABLED` | `false` | 启用 REST/gRPC TLS |
| `AGENT_TLS_NATIONAL_CIPHER` | `false` | 启用 TLCP 国密双证书（与 mTLS 互斥） |
| `AGENT_TLCP_SIGN_CERT_FILE` / `_SIGN_KEY_FILE` | — | SM2 签名证书/私钥 |
| `AGENT_TLCP_ENC_CERT_FILE` / `_ENC_KEY_FILE` | — | SM2 加密证书/私钥 |
| `AGENT_TLCP_CLIENT_CA_FILE` / `AGENT_TLCP_CLIENT_AUTH` | — | 客户端 CA / 认证模式 |
| `AGENT_AUTH_INTERNAL_MTLS_ENABLED` | `false` | 启用 gRPC mTLS 客户端证书认证 |
| `AGENT_AUTH_MTLS_WHITELIST_FILE` | — | CN/SAN 白名单 YAML（5s 热重载） |
| `AGENT_AUTH_MTLS_ALLOWED_CNS` | — | 快速放行 CN（绑 `*`） |

### A.3 WAF / 限流 / 准入

| 变量 | 默认 | 含义 |
|---|---|---|
| `AGENT_RATE_LIMIT_ENABLED` | `false` | 启用 32 分片令牌桶（REST 与 gRPC 均生效） |
| `AGENT_RATE_LIMIT_DEFAULT_RPS` | `100` | 默认每秒请求数 |
| `AGENT_RATE_LIMIT_DEFAULT_BURST` | `200` | 默认突发容量 |
| `AGENT_RATE_LIMIT_PER_ENDPOINT` | — | 按端点定制限额 |
| `AGENT_RATE_LIMIT_REDIS_URL` | — | 多实例共享限流后端 |
| `AGENT_HEALTH_NO_RATE_LIMIT` | `true` | 探针不占限流额（REST 与 gRPC） |
| `AGENT_ALLOWED_CIDRS` | — | IP 准入白名单（空透传）；**同时约束 REST `:8079` 与 gRPC `:50051`** |
| `AGENT_TRUSTED_PROXIES` | — | 受信任代理 CIDR（G-02） |
| `ENGINE_GATEWAY_ALLOWED_CIDRS` | — | 网关入站准入；**同时约束 HTTP `:8000` 与 gRPC 代理 `:50000`** |

### A.4 内核与观测

| 变量 | 默认 | 含义 |
|---|---|---|
| `PRIVACY_NAMESPACE` | `default` | 隐私预算租户隔离命名空间 |
| `AGENT_IMAGE_ALLOWED_DIRS` | cwd + 临时目录 | 影像可读目录白名单 |
| `AGENT_PPROF_ENABLED` | `false` | pprof 端点（生产默认关） |
| `AGENT_REST_PORT` / `AGENT_GRPC_PORT` | `8079` / `50051` | REST / gRPC 端口 |

---

## 附录 B：Scope 权限字典与路径映射

`PermissionForRESTPath` / `PermissionForGRPCMethod` 定义的完整 Scope 字典（`default` 分支一律 fail-closed 归 `admin`）：

| Scope | 信任级 | 涵盖 REST 路径 | 涵盖 gRPC 方法 | 说明 |
|---|---|---|---|---|
| `health:read` | 公开 | `/health` `/livez` `/readyz` `/readyz/llm` | `Health` | 探针，默认豁免鉴权 |
| `privacy:mask` | 内部 | `/v1/privacy/mask*` `/v1/privacy/process_file` | `Mask`/`MaskRecord`/`MaskBatch`/`MaskDataFrame` | 字段/记录/文件脱敏 |
| `privacy:hash` | 内部 | `/v1/privacy/hash` | `Hash` | HMAC-SM3 散列 |
| `privacy:dp` | 内部 | `/v1/privacy/dp/*` `/v1/privacy/ldp/*` | `DPCount`/`DPSum`/`PerturbBinaryBatch`/... | 差分隐私（消预算） |
| `privacy:kano` | 内部 | `/v1/privacy/k_anonymize*` | `KAnonymizeRecord`/`KAnonymizeTable`/`KAnonymizeDataFrame` | K-匿名/L-多样性 |
| `privacy:qol` | 内部 | `/v1/privacy/qol/*` | `ObfuscateQuery`/`ObfuscateQueryBatch` | 查询混淆 |
| `privacy:budget` | 内部 | `/v1/privacy/budget*` | — | 预算查询/重置 |
| `privacy:profile` | 内部 | `/v1/privacy/profile/recommend` | `RecommendParams` | 画像参数推荐 |
| `classification:read` | 内部 | `/v1/privacy/classify/*` | `ClassifyField`/`ClassifyRecord`/`ClassifyTable` | 三层分类（静态） |
| `dynclassification:read` | 内部 | `/v1/dynclassification*`（除 reload） | `DynClassify` | 动态分类读 |
| `dynclassification:write` | 内部 | `/v1/dynclassification/profiles/reload` | — | 规则热重载（写） |
| `agent:process` | 内部 | `/v1/agent*` `/agent*` | — | 通用流水线 |
| `medical:process` | 内部 | `/v1/medical*` `/medical*` | — | 医疗影像/报告流水线 |
| `ops:diagnostics` | 运维 | `/v1/ops/*` `/ops/diagnostics` | — | 只读诊断快照 |
| `ops:admin` | 最高运维 | `/debug/pprof*` | — | pprof / refresh 主动重采 |
| `auth:public` | 公开 | `/v1/auth/login` `/v1/auth/users/register` | — | 认证中间件跳过 scope 强制（引导期匿名开户与登录）；其余 `/v1/auth/*` 路由层映射为**空 scope**（仅需已认证），授权在 Handler 内按主体 ABAC 判定 |
| `user:read` | 审计/管理 | `/v1/auth/users*`（Handler 内校验；路由层为空 scope） | — | 用户与权限**只读**审计（无写权限） |
| `user:admin` | 用户管理 | `/v1/auth/users*`（Handler 内校验；路由层为空 scope） | — | 用户与权限全生命周期管理（角色/状态/密钥写操作） |
| `admin` | 最高 | **未显式映射路径（default 分支）** + 未映射方法 | 未映射方法 | fail-closed 兜底 |
| `*` | 完全 | 任意 | 任意 | 仅本地开发/全权限 Key |

> **与 `HasPermission` 的关系**：上表是「接口需要什么 Scope」；`Identity.HasPermission`（§3.1）是「身份持有什么 Scope」。二者在 `AuthMiddleware` 交汇：`requiredPerm = PermissionForRESTPath(path)` 后 `identity.HasPermission(requiredPerm)` 判定。尾部斜杠先归一化，防 `/v1/privacy/mask/` 绕过。

---

## 附录 C：合规控制点映射（等保三级 / 密评）

本文将控制点编号（G-xx）散落在各节；此表集中映射「合规要求 ↔ 实现机制 ↔ 章节」，便于密评/等保测评时逐项对账。

| 控制点 | 合规要求（摘要） | 引擎实现机制 | 章节 |
|---|---|---|---|
| **G-02** | 网络边界防护/来源真实性 | `ConfigureTrustedProxies` 仅受信代理才信 XFF；`RealClientIP` | §2.2 |
| **G-03** | 身份鉴别防爆破 | 单账号连续 **5** 次失败锁定 **15** 分钟（`429 ACCOUNT_LOCKED` + `Retry-After`）+ 登录端点每 IP 每分钟 **20** 次限速（8 分片固定窗口） | §3.9.3 (4) |
| **G-04** | 口令复杂度与存储 | 长度 `8~72`、字符类别 ≥3、禁含用户名（含逆序）、18 项弱口令字典拦截；`bcrypt cost=12` 加盐杂凑（写锁外计算）；口令历史最近 **3** 个禁重用 | §3.9.3 (4) |
| **G-07** | 特权操作可追溯 | 用户体系全量结构化 `auth_audit` 日志（`actor`/`target_user`/`result`/`reason`/`client_ip`/`trace_id`），严禁记录口令与明文 Token | §3.9.3 (4) |
| **G-08** | 密钥生命周期/轮换 | 信封 v3 多版本密钥 `keyRegistry`（Active 写入、旧版解密）+ KeyStore 热轮换 | §3.5 §5.2 |
| **G-12** | 入侵防范/恶意代码防范 | WAF 5 类 72 正则 init 预编译 + ASCII 快速预检 | §6.1 §6.2 |
| **G-13** | 安全审计/密码操作可追溯 | `CryptoAuditLogger` 记录每次 sm4/sm3 操作（不记明文） | §5.2 |
| **G-14** | 身份鉴别时效性 | Key 支持 RFC3339 `ExpiresAt`，过期自动失效（`ConstantTimeLookup` 跳过） | §3.2 §3.3 |
| 访问控制 | 最小权限/授权 | Scope 精确匹配 + fail-closed 默认 `admin` + 三层防御 | §3.1 §3.6 §3.8 |
| 身份鉴别 | 双向实体鉴别 | mTLS/TLCP 双向证书 + gRPC SAN/CN 多身份白名单 | §4.2 §4.3 §4.5 |
| 数据保密性 | 传输/存储加密（国密） | TLS 1.3 / TLCP 国密套件 + SM4-GCM 信封 + HKDF-SM3 | §4 §5.1 §5.2 |
| 密码应用合规 | 使用国产密码算法（密评） | SM2/SM3/SM4 自主实现 + `ModeGMSSLOnly` 禁降级 | §4.3 §5.1 |
| 个人信息保护 | 去标识/匿名化/隐私保护 | 44 项隐私原语 + DP 预算不可超支 + 安全底线默认拒绝 | §5.4 §5.6 |
| 可信验证/抗篡改 | 防静默降级 | `ValidateFailClosed` 7 不变式 + `ErrEmptyKey` + fail-closed 兜底 | §4.1 §5.2 |

> **标准依据**：GB/T 22239-2019（等保三级）、GB/T 39786-2021（密评三级）、GM/T 0024 / GB/T 38636-2020（TLCP）、GB/T 32907/32918（SM4/SM3）、GM/T 0003（SM2）。控制点编号引自仓库 `docs/production_security` 与代码注释中的 G-xx 标记。

---

## 附录 D：关键安全代码文件索引

下表为本文所引安全机制的**真实代码位置**（仓库相对路径），便于审计与二次开发定位：

| 文件 | 职责 | 本文章节 |
|---|---|---|
| [`pkg/auth/identity.go`](../../../../pkg/auth/identity.go) | 身份模型、`HasPermission`、`PermissionForRESTPath`/`GRPCMethod`、`ParseAPIKeysEnv` | §3.1 §3.2 §3.6 |
| [`pkg/auth/middleware.go`](../../../../pkg/auth/middleware.go) | `ConstantTimeLookup`、`AuthenticateAPIKey`、`AuthMiddleware`、错误信封、安全指标 | §3.3 §3.4 §7.4 |
| [`pkg/auth/keystore.go`](../../../../pkg/auth/keystore.go) | KeyStore 热轮转（5s mtime / SecretWatcher） | §3.5 |
| [`pkg/auth/settings.go`](../../../../pkg/auth/settings.go) | `KeyConfig`、`IsExpired`、`Settings` | §3.2 |
| [`pkg/auth/user.go`](../../../../pkg/auth/user.go) | 用户模型与状态机、等保口令复杂度校验（`ValidatePasswordStrength`）、角色→Scope 矩阵（`KnownRoles`）、安全常数 | §3.9.3 (2)(4) |
| [`pkg/auth/user_store.go`](../../../../pkg/auth/user_store.go) | 用户全生命周期存储、动态 API Key 签发/吊销、会话管理、`LiveHashedKeys` 只读快照、原子持久化 | §3.9.3 (3)(5) |
| [`pkg/auth/user_handlers.go`](../../../../pkg/auth/user_handlers.go) | `/v1/auth/*` REST 控制器、`userPolicyEnvTable` 环境变量事实源、Handler 内 ABAC 授权 | §3.9.3 (3)(6) |
| [`pkg/auth/live_keys.go`](../../../../pkg/auth/live_keys.go) | `HashToken`、版本驱动 `Aggregator` 缓存、深拷贝隔离的活密钥聚合 | §3.9.3 (5) |
| [`pkg/auth/route_audit.go`](../../../../pkg/auth/route_audit.go) | `AuditRoutePermissions`/`LogRoutePermissionAudit` 启动期审计 | §3.8 |
| [`services/privacy-engine/internal/security/auth.go`](../../../../services/privacy-engine/internal/security/auth.go) | 引擎侧 Auth/SecurityHeaders/RateLimit 中间件 | §2 §3.4 §6.4 |
| [`services/privacy-engine/internal/security/config.go`](../../../../services/privacy-engine/internal/security/config.go) | `Settings` 装配与 `AGENT_*` 环境变量读取 | 附录 A |
| [`services/privacy-engine/internal/security/whitelist.go`](../../../../services/privacy-engine/internal/security/whitelist.go) | `WhitelistManager`（委托 `pkg/tlsutil`） | §4.4 |
| [`services/privacy-engine/internal/grpcserver/auth.go`](../../../../services/privacy-engine/internal/grpcserver/auth.go) | gRPC 鉴权拦截器（API Key + Scope） | §3.7 |
| [`pkg/config/security.go`](../../../../pkg/config/security.go) | `ValidateFailClosed` 启动不变式、`IsLoopbackHost` | §4.1 |
| [`pkg/tlsutil/tlcp.go`](../../../../pkg/tlsutil/tlcp.go) | TLCP 国密双证书 `BuildTLCPConfig` | §4.3 |
| [`pkg/tlsutil/whitelist.go`](../../../../pkg/tlsutil/whitelist.go) | `DynamicWhitelist` 5s 热重载、`matchScopePattern` | §4.4 |
| [`pkg/tlsutil/grpc_interceptor.go`](../../../../pkg/tlsutil/grpc_interceptor.go) | `extractClientIdentities`（SAN/CN）、`authorizeClientIdentities` | §4.5 |
| [`pkg/crypto/envelope.go`](../../../../pkg/crypto/envelope.go) | SM4-GCM 信封 v1/v2/v3、HKDF-SM3、`CryptoAuditLogger` | §5.2 |
| [`pkg/crypto/sm3.go`](../../../../pkg/crypto/sm3.go) / [`sm4.go`](../../../../pkg/crypto/sm4.go) | 国密杂凑/分组密码纯实现 | §5.1 |
| [`pkg/crypto/zeroize.go`](../../../../pkg/crypto/zeroize.go) | `Zeroize` 内存清零 | §5.3 |
| [`services/privacy-engine/sdk/budget/budget.go`](../../../../services/privacy-engine/sdk/budget/budget.go) | `BudgetAccountant` 无锁 CAS 预算记账 | §5.4 |
| [`services/privacy-engine/internal/service/service.go`](../../../../services/privacy-engine/internal/service/service.go) | 安全底线 SafetyFloor / 默认拒绝 / 诊断健康 | §5.6 §7.1 |
| [`pkg/middleware/waf.go`](../../../../pkg/middleware/waf.go) | WAF 5 类规则、ASCII 预检、请求体重建 | §6.1–§6.3 |
| [`pkg/middleware/ratelimit.go`](../../../../pkg/middleware/ratelimit.go) | 32 分片令牌桶、`MaxBodySize`/`MaxConcurrent` | §6.4 §6.5 |
| [`pkg/middleware/ip_allowlist.go`](../../../../pkg/middleware/ip_allowlist.go) | `IPAllowlist` CIDR 准入 | §6.5 |
| [`pkg/fileparse/csv.go`](../../../../pkg/fileparse/csv.go) / [`xlsx.go`](../../../../pkg/fileparse/xlsx.go) | BOM 剥离 / Zip 炸弹限制 | §5.5 |
| [`services/privacy-engine/internal/imageredact/redaction.go`](../../../../services/privacy-engine/internal/imageredact/redaction.go) | 路径穿越守卫、目录白名单、影像防护 | §5.5 |
| [`services/privacy-engine/internal/rest/routes.go`](../../../../services/privacy-engine/internal/rest/routes.go) | 中间件链装配、路由注册、启动审计、诊断/pprof handler | §2.1 §3.8 §7.2 §7.3 |

---

> **文档总结**：`privacy-engine` 的安全不是单点加固，而是以 **fail-closed** 为统一哲学、以**纵深防御**为结构原则的多层闭环：网络准入→传输证书→启动不变式→请求净化→身份鉴权→接口 Scope→资源限流→预算约束→安全底线→观测审计，逐层收敛且任一层失效不导致整体沦陷。配套产品需求与验收标准见 [`./prd.md`](./prd.md)。
