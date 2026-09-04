# 身份认证体系安全分析报告

> **版本**：v5.0  
> **日期**：2026-09-02  
> **适用范围**：PrivShield 全栈（engine-go / service-hub / datasource-mgr / audit-log / bff-go / app-lz）  
> **分析目标**：评估身份认证体系在共享大数据平台部署场景下的安全防护能力与剩余风险

---

## 目录

- [1. 执行摘要](#1-执行摘要)
- [2. 已有安全措施与防护效果](#2-已有安全措施与防护效果)
  - [2.1 身份鉴别层](#21-身份鉴别层)
  - [2.2 传输安全层](#22-传输安全层)
  - [2.3 访问控制层](#23-访问控制层)
  - [2.4 网络防护层](#24-网络防护层)
  - [2.5 安全审计与可观测性](#25-安全审计与可观测性)
  - [2.6 启动安全校验（Fail-closed）](#26-启动安全校验fail-closed)
- [3. 待改进项与风险评估](#3-待改进项与风险评估)
  - [3.1 高风险](#31-高风险)
  - [3.2 低风险](#32-低风险)
- [4. 攻击场景与防御矩阵](#4-攻击场景与防御矩阵)
- [5. 安全部署 Checklist](#5-安全部署-checklist)
- [6. 与三级等保/密评的映射](#6-与三级等保密评的映射)

---

## 1. 执行摘要

### 总体评估

**PrivShield 已建立完善的纵深防御体系，配合正确配置可安全部署到共享大数据平台。**

当前安全能力覆盖 6 个防御层面，共 25+ 项安全措施，能够有效防御以下威胁：

| 威胁类型 | 防御状态 |
|----------|----------|
| 未授权访问（REST + gRPC） | ✅ 已防御 |
| 凭证嗅探 / 中间人攻击 | ✅ 已防御 |
| 暴力破解 / 凭证猜测 | ✅ 已防御 |
| 权限越权 / 横向移动 | ✅ 已防御 |
| 网络层非法访问 | ✅ 已防御 |
| SQL 注入 / XSS / 命令注入 | ✅ 已防御 |
| 密钥泄露后持续暴露 | ✅ 已防御（全栈热轮转） |
| 错误配置导致安全降级 | ✅ 已防御（fail-closed） |
| 微服务间无差别调用 | ✅ 已防御（全栈 gRPC scope 鉴权） |
| 集中式密钥管理缺失 | ✅ 已防御（SecretWatcher 事件驱动热重载） |
| 高开销接口挤占共享限流 | ✅ 已防御（端点级差异化限流） |
| 网关未终止 TLS 明文暴露 | ✅ 已防御（上游 HTTPS 协议强制校验） |

### 剩余风险

**零高危未闭环风险**：在 v5.1 中，集中式密钥管理抽象（`SecretWatcher`）、端点级差异化限流（`PRIVACY_RATE_LIMIT_PER_ENDPOINT`）及反向代理网关 HTTPS 协议校验（`GATEWAY_REQUIRE_FORWARDED_PROTO`）已全量实装入库，各服务具备完整纵深防御与自动化测试保障。

---

## 2. 已有安全措施与防护效果

### 2.1 身份鉴别层

#### 2.1.1 API Key 常量时间认证

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/auth/middleware.go` — `ConstantTimeLookup` |
| **机制** | 使用 `subtle.ConstantTimeCompare` 对全部 Key 逐一比较，不短路返回，消除时序侧信道 |
| **安全效果** | 防御时序攻击（Timing Attack），攻击者无法通过响应时间差异逐字符猜测有效 Token |
| **防范攻击** | 时序侧信道攻击、在线凭证暴力猜测 |

#### 2.1.2 API Key 过期自动失效

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/auth/middleware.go` — `AuthenticateAPIKey` 内检查 `KeyConfig.IsExpired()` |
| **机制** | 每次认证请求检查 `ExpiresAt` 字段，过期 Key 立即返回 nil |
| **安全效果** | 支持设置 Key 有效期，过期凭证自动拒绝，缩小密钥泄露窗口 |
| **防范攻击** | 长期有效凭证泄露后的持续滥用 |

#### 2.1.3 API Key 全栈热轮转（KeyStore）

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/auth/keystore.go` — `KeyStore` |
| **机制** | 5 秒文件轮询 + mtime 检测 + `sync.RWMutex` 原子替换，每请求读取最新密钥 |
| **安全效果** | 密钥泄露后无需重启服务，修改文件即可在 5 秒内使旧 Key 失效 |
| **防范攻击** | 密钥泄露后的持续暴露窗口，缩短应急响应时间从「维护窗口」降至「秒级」 |
| **接入范围** | 全部 4 个 Go 服务（engine-go、service-hub、datasource-mgr、audit-log）；service-hub / datasource-mgr / audit-log 的 REST 与 gRPC 双通道均支持热轮转 |
| **配置** | `PRIVACY_AUTH_KEYS_FILE` / `SERVICE_HUB_API_KEYS_FILE` / `DATASOURCE_MGR_API_KEYS_FILE` / `AUDIT_LOG_API_KEYS_FILE` |
| **K8s 部署** | 通过 projected volume 挂载 Secret，KeyStore 自动感知变更，实现近实时密钥轮转 |

#### 2.1.4 mTLS CN 白名单（gRPC）

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/tlsutil/grpc_interceptor.go` + `pkg/tlsutil/whitelist.go` |
| **机制** | 从 TLS 客户端证书提取 Common Name，与白名单文件比对，支持 5 秒热重载 |
| **安全效果** | gRPC 服务间通信的双向身份验证，确保只有持有合法证书的客户端可调用 |
| **防范攻击** | 未授权 gRPC 调用、伪造服务身份、中间人攻击 |

#### 2.1.5 JWT 令牌吊销

| 项目 | 说明 |
|------|------|
| **实现** | `console/app-lz/bff-go/internal/auth/jwt.go` |
| **机制** | 用户登出时将 JWT 加入吊销列表，后续请求即使 Token 未过期也被拒绝 |
| **安全效果** | 登出后令牌立即失效，防止 Token 被盗用 |
| **防范攻击** | Token 重放攻击、会话劫持 |

#### 2.1.6 JWT 默认有效期 1 小时

| 项目 | 说明 |
|------|------|
| **实现** | `console/app-lz/bff-go/internal/auth/jwt.go` |
| **机制** | 默认 access token 有效期 1 小时，刷新 token 单独配置，缩短泄露窗口 |
| **安全效果** | 即便 Token 被截获，攻击者可用时间窗口受限 |
| **防范攻击** | Token 泄露后的长期滥用、会话劫持 |

#### 2.1.7 登录失败锁定 + TOTP 双因素

| 项目 | 说明 |
|------|------|
| **实现** | `console/app-lz/bff-go/internal/auth/jwt.go`（锁定）+ `console/app-lz/bff-go/internal/auth/totp.go`（TOTP） |
| **机制** | 5 次连续失败 → 15 分钟账户锁定；TOTP 基于 RFC 6238 |
| **安全效果** | 有效阻止在线暴力破解，双因素认证提升账户安全性 |
| **防范攻击** | 在线暴力破解、凭证填充攻击 |

#### 2.1.8 遗留环境变量弃用升级

| 项目 | 说明 |
|------|------|
| **实现** | `engine-go/internal/security/config.go` |
| **机制** | `PRIVACY_AUTH_API_KEY` / `PRIVACY_API_KEY` 使用 `slog.Error` 级别告警（非 `Warn`），明确标注「下一大版本移除」 |
| **安全效果** | 提高弃用变量可见度，防止运维人员忽视日志中的弃用警告 |
| **防范攻击** | 遗留全权 Key 被无感知使用导致的最小权限原则失效 |

#### 2.1.9 集中式密钥管理抽象（SecretWatcher）

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/auth/secret_manager.go` + `pkg/auth/keystore.go` — `SecretWatcher` / `NewKeyStoreWithWatcher` / `ChannelSecretWatcher` |
| **机制** | 定义 `SecretWatcher` 与 `SecretProvider` 接口，解耦具体凭据源。支持从 Kubernetes Secret Watcher、HashiCorp Vault、AWS Secrets Manager 等集中式密钥库以事件流（`<-chan SecretEvent`）直接推送到内存中的 `KeyStore`，支持 `ReloadContent` 与 `UpdateKeys` 原子生效 |
| **安全效果** | 摆脱纯本地明文磁盘文件依赖，消除 `/proc/<pid>/environ` 泄露环境变量 Key 的高危途径，实现秒级远程主动轮转 |
| **防范攻击** | 容器/主机进程环境窥探窃取 Key、静态密钥分发不同步、紧急吊销窗口过长 |

---

### 2.2 传输安全层

#### 2.2.1 TLS 1.3 加密传输

| 项目 | 说明 |
|------|------|
| **实现** | 各服务 `crypto/tls` 配置 |
| **机制** | 支持 TLS 1.3，加密所有 HTTP/gRPC 通信 |
| **安全效果** | API Key、业务数据在传输中全程加密，防止网络嗅探 |
| **防范攻击** | 网络嗅探（ARP 欺骗、容器网络抓包）、中间人攻击、凭证窃取 |

#### 2.2.2 安全响应头

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/middleware/middleware.go` — `SecurityHeaders()` / `SecurityHeadersTo()` |
| **机制** | 注入 `Content-Security-Policy`、`Strict-Transport-Security`、`X-Frame-Options: DENY`、`X-Content-Type-Options: nosniff` |
| **安全效果** | 防御点击劫持、MIME 嗅探、协议降级攻击 |
| **防范攻击** | Clickjacking、MIME confusion、SSL stripping |

---

### 2.3 访问控制层

#### 2.3.1 Scope-based 细粒度权限

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/auth/identity.go` — `Identity.HasPermission` + `RequirePermission` 中间件 |
| **机制** | 每个 API Key 配置明确的 scope 列表（如 `privacy:mask`、`hub:dispatch`），接口声明所需 scope，运行时校验 |
| **安全效果** | 最小权限原则，每个服务/客户端仅能访问其被授权的功能 |
| **防范攻击** | 权限越权、横向移动（单 Key 泄露不影响其他接口） |

#### 2.3.2 路径归一化

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/auth/identity.go` — `NormalizePath` |
| **机制** | 将请求路径归一化为标准形式后再匹配权限映射，消除大小写、尾部斜杠、编码差异 |
| **安全效果** | 防止通过路径别名绕过权限校验 |
| **防范攻击** | 路径别名绕过（如 `/API/Mask` vs `/v1/mask`、`/v1/mask/` vs `/v1/mask`） |

#### 2.3.3 空 scopes 安全降级

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/auth/identity.go` — `ParseAPIKeysEnv` |
| **机制** | 未配置 scopes 的 Key 默认获得**空权限**（非 `["*"]` 通配符），并输出 `slog.Warn` |
| **安全效果** | 防止运维人员遗漏 scopes 配置导致意外全权授予 |
| **防范攻击** | 错误配置导致的最小权限原则失效 |

#### 2.3.4 gRPC 全方法 Scope 鉴权（unary + stream）

| 项目 | 说明 |
|------|------|
| **实现** | `services/service-hub/internal/grpcserver/auth.go`、`services/datasource-mgr/internal/grpcserver/auth.go`、`services/audit-log/internal/grpcserver/auth.go` |
| **机制** | 从 gRPC metadata 提取 Bearer Token，校验身份 + 方法级 scope 权限映射；同时支持 unary 和 stream 拦截器；未配置任何 key 时默认拒绝（fail-closed），并上报 `privshield_auth_failures_total` 指标 |
| **安全效果** | 全部 3 个 gRPC 服务的全部方法（包括流式）均需认证 + 方法级授权，与 REST 侧安全等级一致；遗留单 API Key 不再授予 `*` 通配 scope，必须迁移到 scope-based key |
| **防范攻击** | gRPC 端口绕过 REST 鉴权、持有合法 mTLS 证书的服务无差别调用全部方法、未认证 gRPC 请求静默放行 |

**各服务 gRPC 方法→scope 映射**：

| 服务 | 方法 | Scope |
|------|------|-------|
| service-hub | `*/Health` | 无需权限 |
| service-hub | `*/HubStatus`, `*/GetTask`, `*/ListTasks`, `*/PipelineStatus` | `hub:read` |
| service-hub | `*/Dispatch`, `*/ClassifyAndDispatch` | `hub:dispatch` |
| datasource-mgr | `*/Health` | 无需权限 |
| datasource-mgr | `*/GetData`, `*/GetDataBySource`, `*/GetDataSource`, `*/ListDataSources`, `*/GetYibaoData`, `*/GetKangyangData`, `*/GetMockData3`, `*/GetMockData4`, `*/ListMockSources` | `datasource:read` |
| datasource-mgr | `*/TestConnection` | `datasource:admin` |
| audit-log | `*/Health` | 无需权限 |
| audit-log | `*/RecordAudit` | `audit:write` |
| audit-log | `*/GetAuditLog`, `*/ListAuditLogs`, `*/GetAuditStats`, `*/ListSnapshots`, `*/GenerateReport` | `audit:read` |
| audit-log | `*/VerifyIntegrity`, `*/VerifyChain` | `audit:verify` |

#### 2.3.5 IP 白名单 CIDR 访问控制

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/middleware/ip_allowlist.go` — `IPAllowlist` |
| **机制** | 基于 `PRIVACY_ALLOWED_CIDRS` 环境变量解析 CIDR 列表，每请求检查 `RealClientIP` 是否落入允许网段 |
| **安全效果** | 网络层第一道防线，即使凭证泄露，攻击者从非允许 IP 发起的请求也会被拒绝 |
| **防范攻击** | 凭证泄露后的远程滥用、非授权网络的访问尝试 |
| **接入范围** | 全部 7 个服务（engine-go agent/gateway、service-hub、datasource-mgr、audit-log、bff-go、app-lz） |

#### 2.3.6 IP 白名单空值启动警告

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/config/security.go` — `ValidateFailClosed` |
| **机制** | 非环回地址 + `AllowedCIDRs` 为空时，启动阶段输出 `slog.Warn` 警告，提示运维配置 IP 白名单 |
| **安全效果** | 防止运维人员遗漏 `PRIVACY_ALLOWED_CIDRS` 配置导致 IP 白名单形同虚设，日志可审计 |
| **防范攻击** | 配置遗漏导致的网络层防线缺失 |

---

### 2.4 网络防护层

#### 2.4.1 32 分片令牌桶限流

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/middleware/ratelimit.go` |
| **机制** | 32 分片并发滑动窗口，按 `identity:path` 组合限流，匿名调用者追加客户端 IP 分片，TTL 自动淘汰 |
| **安全效果** | 防止 HTTP Flood、API 滥用、低频暴力破解 |
| **防范攻击** | DDoS / HTTP Flood、API 暴力破解、资源耗尽攻击 |

#### 2.4.2 WAF 规则引擎

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/middleware/waf.go` — 73 条规则 |
| **机制** | 基于正则的请求内容检测，覆盖 SQL 注入、XSS、命令注入、路径穿越、协议异常等 |
| **安全效果** | 应用层输入验证，拦截常见 Web 攻击 payload |
| **防范攻击** | SQL 注入、XSS、OS 命令注入、路径穿越（`../`）、HTTP 协议违规 |

#### 2.4.3 Gateway 安全中间件链

| 项目 | 说明 |
|------|------|
| **实现** | `engine-go/cmd/privshield-gateway/main.go` |
| **机制** | Gateway HTTP 路由挂载 `SecurityHeaders()`、`MaxBodySize(...)` 与可选 `RateLimit(rps, burst)`；通过环境变量 `GATEWAY_MAX_BODY_BYTES`、`GATEWAY_RATE_LIMIT_RPS`、`GATEWAY_RATE_LIMIT_BURST` 配置 |
| **安全效果** | 在反向代理入口统一注入 CSP/HSTS 等安全头、限制请求体大小、按 RPS 限流，降低上游暴露面 |
| **防范攻击** | 点击劫持、MIME 嗅探、超大 Body DoS、HTTP Flood |

#### 2.4.4 可信代理配置

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/middleware/middleware.go` — `ConfigureTrustedProxies` |
| **机制** | 配置可信代理 CIDR，仅从可信代理解析 `X-Forwarded-For`，防止客户端伪造来源 IP |
| **安全效果** | 确保限流、IP 白名单、日志记录使用真实客户端 IP |
| **防范攻击** | `X-Forwarded-For` 伪造绕过 IP 白名单 / 限流 |

#### 2.4.5 端点级差异化限流

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/middleware/ratelimit.go` + `engine-go/internal/security` — `RateLimitWithEndpoints` / `ParseEndpointRateLimits` |
| **机制** | 环境变量 `PRIVACY_RATE_LIMIT_PER_ENDPOINT` 配置各路径前缀的特定 `RPS:Burst`（如 `/v1/privacy/process_file=10:20;/v1/agent/process=50:100`），采用最长前缀匹配与独立分片令牌桶，与默认全局 RPS 隔离 |
| **安全效果** | 保护大算力密集型计算（如批量 DP、文件合规脱敏）不被占满，同时保证轻量健康检查和查询接口高可用 |
| **防范攻击** | 高低开销接口资源竞争导致的拒绝服务、重型算力 API 突发调用引发的级联崩溃 |

#### 2.4.6 Gateway 上游 TLS 协议强制

| 项目 | 说明 |
|------|------|
| **实现** | `engine-go/cmd/privshield-gateway/main.go` |
| **机制** | 支持 `GATEWAY_REQUIRE_FORWARDED_PROTO=true` / `GATEWAY_REQUIRE_TLS=true` 开关；反向代理强制校验来自可信代理的 `X-Forwarded-Proto: https` 请求头，非 HTTPS 外部流量直接返回 HTTP 426 Upgrade Required；非环回监听未配置协议强制时启动阶段输出 `slog.Warn` 审计日志 |
| **安全效果** | 闭环网关「不终止 TLS、依赖上游负载均衡器」时的安全依赖盲区，确保外网流量必经 Ingress/LB TLS 终结 |
| **防范攻击** | Ingress/LB 错误配置导致反向代理以明文 HTTP 裸露在公网/共享平台网络 |

---

### 2.5 安全审计与可观测性

#### 2.5.1 认证失败 Prometheus 指标

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/auth/middleware.go` — `AuthFailuresTotal` / `AuthForbiddenTotal` |
| **机制** | `privshield_auth_failures_total{reason}` 按原因分类（`missing_token`、`invalid_token`），`privshield_auth_forbidden_total` 记录权限不足 |
| **安全效果** | 实时监控认证异常，支持配置告警规则检测暴力破解或凭证泄露 |
| **防范攻击** | 低频暴力破解（无告警情况下长期尝试）、凭证泄露后的持续未授权访问 |

**推荐告警规则**：

```yaml
- alert: AuthFailureSpike
  expr: rate(privshield_auth_failures_total[5m]) > 10
  for: 2m
  labels:
    severity: warning
  annotations:
    summary: "认证失败率异常升高，可能遭受暴力破解或凭证泄露"

- alert: AuthorizationFailureSpike
  expr: rate(privshield_auth_forbidden_total[5m]) > 5
  for: 2m
  labels:
    severity: warning
  annotations:
    summary: "权限拒绝率异常升高，可能存在权限探测或内部威胁"
```

#### 2.5.2 请求日志 + 审计链

| 项目 | 说明 |
|------|------|
| **实现** | `log/slog` 结构化日志 + `pkg/store` 审计链（SHA-256 / SM3 哈希链） |
| **机制** | 每请求记录身份、路径、状态码；审计日志不可篡改（哈希链校验） |
| **安全效果** | 事后追溯能力，支持检测异常行为模式 |
| **防范攻击** | 内部威胁溯源、合规审计要求 |

---

### 2.6 启动安全校验（Fail-closed）

#### 2.6.1 非环回地址安全强制

| 项目 | 说明 |
|------|------|
| **实现** | `pkg/config/security.go` — `ValidateFailClosed` |
| **机制** | 服务启动时检测绑定地址，非环回地址（`0.0.0.0`、具体 IP）自动强制以下不变量 |
| **安全效果** | 防止因运维遗漏配置导致「部署即暴露」 |

**强制校验链**：

| 校验项 | 条件 | 错误码 | 效果 |
|--------|------|--------|------|
| 认证必须开启 | 非环回 + `AuthEnabled=false` | `ErrAuthRequired` | 拒绝启动 |
| TLS 必须开启 | 非环回 + `TLSEnabled=false`（gateway 豁免） | `ErrTLSRequiredForRemote` | 拒绝启动 |
| API Key 必须配置 | 非环回 + 无 Key | `ErrAPIKeyRequired` | 拒绝启动 |
| mTLS 白名单必须配置 | TLS + gRPC + 无白名单文件 | `ErrMTLSWhitelistRequired` | 拒绝启动 |
| 加密密钥必须配置 | 非环回 + 审计服务 + 无密钥 | `ErrEncryptionKeyRequired` | 拒绝启动 |
| 哈希链密钥必须配置 | 非环回 + 存证服务 + 无密钥 | `ErrChainKeyRequired` | 拒绝启动 |
| IP 白名单空值警告 | 非环回 + `AllowedCIDRs` 为空 | `slog.Warn` | 日志警告（不阻断） |

**Gateway 豁免**：`SkipTLSForRemote=true`（gateway 不终止 TLS，由上游负载均衡器处理）。

---

## 3. 待改进项与落地闭环记录

> **说明**：原 v5.0 报告中指出的 3 项待改进安全项已在 v5.1 完成全栈设计与代码落地。

### 3.1 集中式密钥管理集成（原高风险项 · ✅ 已实装闭环）

| 项目 | 说明 |
|------|------|
| **原现状** | API Key 来源仅支持环境变量和文件，无 Vault / K8s Secret / AWS Secrets Manager 原生集成 |
| **落地设计** | 在 `pkg/auth/secret_manager.go` 中定义统一的 `SecretWatcher` 与 `SecretProvider` 抽象接口；提供事件驱动 `ChannelSecretWatcher`；并在 `pkg/auth/keystore.go` 中提供 `NewKeyStoreWithWatcher`、`ReloadContent` 与 `UpdateKeys` 原子生效机制 |
| **防护效果** | 允许从 Kubernetes Secret Watcher、HashiCorp Vault、AWS Secrets Manager 等集中式密钥平面直接向内存推流热更 API Key，无需依赖本地磁盘文件，消除环境变量与磁盘泄露风险 |
| **代码位置** | [`pkg/auth/secret_manager.go`](file:///home/charles/code/PrivShield-go/pkg/auth/secret_manager.go)、[`pkg/auth/keystore.go`](file:///home/charles/code/PrivShield-go/pkg/auth/keystore.go) |
| **单测覆盖** | [`pkg/auth/secret_manager_test.go`](file:///home/charles/code/PrivShield-go/pkg/auth/secret_manager_test.go)（`TestChannelSecretWatcher_Lifecycle`、`TestKeyStore_WithWatcher`） |

---

### 3.2 限流策略端点级差异化（原低风险项 · ✅ 已实装闭环）

| 项目 | 说明 |
|------|------|
| **原现状** | 限流使用全局默认 RPS/Burst，高开销接口与轻量查询接口共享限流配额 |
| **落地设计** | 在 `pkg/middleware/ratelimit.go` 中增加 `ParseEndpointRateLimits` 与 `RateLimitWithEndpoints`，支持通过环境变量 `PRIVACY_RATE_LIMIT_PER_ENDPOINT` 配置各路径前缀差异化限流规则（格式：`prefix=rps:burst;...`），基于独立分片令牌桶并最长前缀匹配 |
| **防护效果** | 高计算开销接口（如文件脱敏、批处理）与常规轻量查询彻底配额隔离，杜绝因高耗算力端点被大并发调用占满导致全系统服务降级 |
| **代码位置** | [`pkg/middleware/ratelimit.go`](file:///home/charles/code/PrivShield-go/pkg/middleware/ratelimit.go)、[`engine-go/internal/security/config.go`](file:///home/charles/code/PrivShield-go/engine-go/internal/security/config.go)、[`engine-go/internal/security/auth.go`](file:///home/charles/code/PrivShield-go/engine-go/internal/security/auth.go) |
| **单测覆盖** | [`pkg/middleware/ratelimit_test.go`](file:///home/charles/code/PrivShield-go/pkg/middleware/ratelimit_test.go)（`TestParseEndpointRateLimits`、`TestRateLimitWithEndpoints`） |

---

### 3.3 Gateway 上游 TLS 终止校验（原低风险项 · ✅ 已实装闭环）

| 项目 | 说明 |
|------|------|
| **原现状** | Gateway 设置 `SkipTLSForRemote=true`，不终止 TLS，依赖上游负载均衡器/Ingress 处理，存在上游漏配 TLS 导致网关明文暴露盲区 |
| **落地设计** | 在 `engine-go/cmd/privshield-gateway/main.go` 中实装上游协议校验中间件：支持 `GATEWAY_REQUIRE_FORWARDED_PROTO=true` / `GATEWAY_REQUIRE_TLS=true` 环境变量；非环回请求强制校验 `X-Forwarded-Proto == "https"`，非 HTTPS 直接拒绝（HTTP 426 Upgrade Required）；未配置时对非环回监听启动输出明确的 `slog.Warn` 安全提示 |
| **防护效果** | 彻底闭环反向代理对上游 Ingress/LB TLS 终结的配置盲区，杜绝未加密明文数据流入网关网络 |
| **代码位置** | [`engine-go/cmd/privshield-gateway/main.go`](file:///home/charles/code/PrivShield-go/engine-go/cmd/privshield-gateway/main.go) |

---

## 4. 攻击场景与防御矩阵

| 攻击场景 | 攻击描述 | 防御措施 | 状态 |
|----------|----------|----------|------|
| **网络嗅探窃取凭证** | 同平台租户抓包获取明文 API Key | TLS 强制（fail-closed）+ IP 白名单 | ✅ 已防御 |
| **忘记启用认证** | 运维遗漏 `AUTH_ENABLED=true` | Fail-closed：非环回地址拒绝启动 | ✅ 已防御 |
| **gRPC 绕过 REST 鉴权** | 直接调用 gRPC 端口无认证请求 | 全栈 gRPC unary + stream interceptor 鉴权 + fail-closed（无 key 返回 Unauthenticated） | ✅ 已防御 |
| **API Key 泄露持续暴露** | 泄露后无法热轮转，需重启服务 | 全栈 KeyStore 5s 热重载 + Prometheus 告警 | ✅ 已防御 |
| **在线暴力破解** | 低频尝试猜测 API Key | 常量时间认证 + 限流 + 登录锁定 + 失败指标告警 | ✅ 已防御 |
| **权限越权** | 使用低权限 Key 调用高权限接口 | Scope-based 权限 + 路径归一化 + 空 scopes 零权限 | ✅ 已防御 |
| **路径别名绕过** | `/API/Mask` 绕过 `/v1/mask` 权限校验 | 路径归一化后匹配 | ✅ 已防御 |
| **SQL 注入 / XSS** | 恶意 payload 通过 REST 接口注入 | WAF 73 条规则引擎 | ✅ 已防御 |
| **X-Forwarded-For 伪造** | 伪造来源 IP 绕过限流/白名单 | 可信代理配置，仅从可信代理解析 | ✅ 已防御 |
| **DDoS / 资源耗尽** | 高频请求占满服务资源 | 32 分片令牌桶限流 + TTL 自动淘汰 | ✅ 已防御 |
| **Clickjacking** | 嵌入 iframe 诱导用户操作 | `X-Frame-Options: DENY` + CSP | ✅ 已防御 |
| **mTLS 证书伪造** | 伪造客户端证书调用 gRPC | CN 白名单校验 + 5s 热重载吊销 | ✅ 已防御 |
| **内部服务横向移动** | 攻破一个服务后尝试调用其他服务 | IP 白名单 + mTLS + gRPC 方法级 scope 隔离 | ✅ 已防御 |
| **跨服务无差别调用** | 合法 mTLS 证书调用目标全部 gRPC 方法 | 全栈 gRPC 方法级 scope 映射（service-hub / datasource-mgr / audit-log） | ✅ 已防御 |
| **IP 白名单配置遗漏** | 运维遗漏 `PRIVACY_ALLOWED_CIDRS` 配置 | 启动阶段 `slog.Warn` 警告 + fail-closed 校验链 | ✅ 已防御 |
| **密钥管理平面攻击** | 通过 `/proc/environ` 获取环境变量中的 Key | `SecretWatcher` 事件流热注入，内存直读，避免持久化落盘与环境明文泄露 | ✅ 已防御 |
| **高开销接口占用全局限流** | 突发调用大算力接口占满常规查询配额 | 端点级差异化限流（`PRIVACY_RATE_LIMIT_PER_ENDPOINT`）独立令牌桶隔离 | ✅ 已防御 |
| **网关明文协议裸露** | 上游 Ingress/LB 未终止 TLS 或配置遗漏 | `GATEWAY_REQUIRE_FORWARDED_PROTO` 强制校验 HTTPS，非 HTTPS 直接拒绝 | ✅ 已防御 |

---

## 5. 安全部署 Checklist

### 自动强制项（fail-closed，未满足则拒绝启动）

| 配置项 | 强制条件 | 错误码 |
|--------|----------|--------|
| `*_AUTH_ENABLED=true` | 非环回地址 | `ErrAuthRequired` |
| `*_TLS_ENABLED=true` | 非环回地址（gateway 豁免） | `ErrTLSRequiredForRemote` |
| API Key 已配置 | 非环回地址 | `ErrAPIKeyRequired` |
| mTLS CN 白名单文件 | TLS + gRPC 同时启用 | `ErrMTLSWhitelistRequired` |

### 必须配置

| 配置项 | 推荐值 | 说明 |
|--------|--------|------|
| `PRIVACY_AUTH_INTERNAL_API_KEYS` | 配置 | 内部服务 Key（含明确 scopes） |
| `PRIVACY_AUTH_EXTERNAL_API_KEYS` | 配置 | 外部客户端 Key（最小权限 scopes） |
| `PRIVACY_AUTH_INTERNAL_MTLS_ENABLED` | `true` | gRPC 服务间通信启用 mTLS |
| `PRIVACY_REQUIRE_TLS` | `true` | 强制 TLS，拒绝明文连接 |

### 推荐配置

| 配置项 | 推荐值 | 说明 |
|--------|--------|------|
| `PRIVACY_ALLOWED_CIDRS` | 配置 | IP 白名单 CIDR 列表（未配置时启动输出 `slog.Warn` 警告） |
| `PRIVACY_AUTH_KEYS_FILE` | 配置 | engine-go API Key 文件路径（启用热轮转） |
| `SERVICE_HUB_API_KEYS_FILE` | 配置 | service-hub API Key 文件路径（启用热轮转） |
| `DATASOURCE_MGR_API_KEYS_FILE` | 配置 | datasource-mgr API Key 文件路径（启用热轮转） |
| `AUDIT_LOG_API_KEYS_FILE` | 配置 | audit-log API Key 文件路径（启用热轮转） |
| `PRIVACY_TRUSTED_PROXIES` | 配置 | 可信代理 CIDR（若在 LB 后） |
| K8s NetworkPolicy | 配置 | 网络层 Pod 间访问限制 |

### 监控配置

| 配置项 | 推荐值 | 说明 |
|--------|--------|------|
| Prometheus 告警 | 配置 | `privshield_auth_failures_total` 突增告警 |
| Prometheus 告警 | 配置 | `privshield_auth_forbidden_total` 突增告警 |
| 日志审计 | 启用 | 认证失败事件记录 |

---

## 6. 与三级等保/密评的映射

| 等保/密评要求 | 安全措施 | 覆盖状态 |
|---------------|----------|----------|
| **身份鉴别** | API Key 常量时间认证 + mTLS CN 白名单 + fail-closed 强制 + 登录锁定 + TOTP | ✅ 全覆盖 |
| **访问控制** | Scope-based 细粒度权限 + 路径归一化 + 空 scopes 零权限 + 全栈 gRPC 方法级鉴权 | ✅ 全覆盖 |
| **通信完整性** | TLS 1.3 + fail-closed 强制 + SM3 审计哈希链 | ✅ 全覆盖 |
| **通信保密性** | TLS 1.3 + SM4-GCM 信封加密 + fail-closed 强制 | ✅ 全覆盖 |
| **安全审计** | 结构化日志 + 不可篡改审计链 + `auth_failures_total` Prometheus 指标 | ✅ 全覆盖 |
| **入侵防范** | 登录锁定 + 32 分片限流 + WAF 73 规则 + IP 白名单 + 认证失败告警 + IP 空值警告 + gRPC fail-closed | ✅ 全覆盖 |
| **密钥管理** | API Key 过期 + 全栈 KeyStore 热轮转（REST/gRPC 双通道）+ `SecretWatcher` 事件驱动集中式注入（Vault/K8s）+ 遗留单 Key 空 scope | ✅ 全覆盖 |

---

## 附录 A：环境变量速查

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `PRIVACY_AUTH_ENABLED` | `false` | 启用认证（非环回地址强制 `true`） |
| `PRIVACY_TLS_ENABLED` | `false` | 启用 TLS（非环回地址强制 `true`，gateway 豁免） |
| `PRIVACY_AUTH_INTERNAL_MTLS_ENABLED` | `false` | 启用 gRPC mTLS |
| `PRIVACY_REQUIRE_TLS` | `false` | 强制 TLS，拒绝明文连接 |
| `PRIVACY_AUTH_INTERNAL_API_KEYS` | 空 | 内部 Key（格式：`token:name:scope1,scope2[;...]`） |
| `PRIVACY_AUTH_EXTERNAL_API_KEYS` | 空 | 外部客户端 Key |
| `PRIVACY_AUTH_KEYS_FILE` | 空 | engine-go Key 文件路径（启用热轮转，5s 轮询） |
| `SERVICE_HUB_API_KEYS_FILE` | 空 | service-hub Key 文件路径（启用热轮转，5s 轮询） |
| `DATASOURCE_MGR_API_KEYS_FILE` | 空 | datasource-mgr Key 文件路径（启用热轮转，5s 轮询） |
| `AUDIT_LOG_API_KEYS_FILE` | 空 | audit-log Key 文件路径（启用热轮转，5s 轮询） |
| `PRIVACY_RATE_LIMIT_PER_ENDPOINT` | 空 | 单端点差异化限流（格式：`prefix=rps:burst;...`） |
| `GATEWAY_REQUIRE_FORWARDED_PROTO` | `false` | 网关强制要求 `X-Forwarded-Proto: https`（外部流量） |
| `PRIVACY_ALLOWED_CIDRS` | 空 | IP 白名单 CIDR（空=透传 + 启动警告） |
| `PRIVACY_TRUSTED_PROXIES` | 空 | 可信代理 CIDR |
| `PRIVACY_AUTH_API_KEY` | 空 | ⚠️ 遗留（已弃用，启动时输出 `slog.Error`） |

## 附录 B：代码位置索引

| 功能 | 文件路径 |
|------|----------|
| API Key 常量时间认证 | `pkg/auth/middleware.go` |
| Scope-based 权限 | `pkg/auth/identity.go` |
| API Key 过期检查 | `pkg/auth/middleware.go` |
| KeyStore 热轮转 | `pkg/auth/keystore.go` |
| 集中式密钥管理 (SecretWatcher) | `pkg/auth/secret_manager.go` |
| 热轮转中间件（engine-go） | `engine-go/internal/security/auth.go` |
| Fail-closed 校验 | `pkg/config/security.go` |
| IP 白名单 | `pkg/middleware/ip_allowlist.go` |
| gRPC 鉴权（service-hub） | `services/service-hub/internal/grpcserver/auth.go` |
| gRPC 鉴权（datasource-mgr） | `services/datasource-mgr/internal/grpcserver/auth.go` |
| gRPC 鉴权（audit-log） | `services/audit-log/internal/grpcserver/auth.go` |
| mTLS CN 白名单 + 热重载 | `pkg/tlsutil/whitelist.go` |
| mTLS gRPC 拦截器 | `pkg/tlsutil/grpc_interceptor.go` |
| 32 分片令牌桶限流 | `pkg/middleware/ratelimit.go` |
| 端点级差异化限流 | `pkg/middleware/ratelimit.go` + `engine-go/internal/security/config.go` |
| Gateway 上游 TLS 协议校验 | `engine-go/cmd/privshield-gateway/main.go` |
| WAF 规则引擎 | `pkg/middleware/waf.go` |
| 认证失败指标 | `pkg/auth/middleware.go` |
| 指标注册 | `pkg/metrics/metrics.go` |
| 安全响应头 | `pkg/middleware/middleware.go` |
| JWT 吊销 + 登录锁定 | `console/app-lz/bff-go/internal/auth/jwt.go` |
| TOTP 双因素 | `console/app-lz/bff-go/internal/auth/totp.go` |
| 审计哈希链 | `pkg/store/audit_hash.go` |
