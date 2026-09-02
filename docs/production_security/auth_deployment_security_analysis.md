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

### 剩余风险

仅剩 2 项待改进项（1 高风险 + 1 低风险），集中在**集中式密钥管理集成**和**限流端点差异化**方面，详见 [§3 待改进项与风险评估](#3-待改进项与风险评估)。

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
| **防范攻击** | 路径别名绕过（如 `/API/Mask` vs `/api/mask`、`/api/mask/` vs `/api/mask`） |

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

## 3. 待改进项与风险评估

### 3.1 高风险

#### 3.1.1 无集中式密钥管理集成

| 项目 | 说明 |
|------|------|
| **现状** | API Key 来源仅支持环境变量和文件，无 Vault / K8s Secret / AWS Secrets Manager 原生集成 |
| **风险** | 环境变量可通过 `/proc/<pid>/environ` 泄露；文件需手动分发到各节点 |
| **影响范围** | 全栈 |
| **缓解措施** | K8s 环境可通过 projected volume 挂载 Secret 并结合 `KeyStore` 文件轮询实现近实时同步；非 K8s 环境可使用外部配置管理工具同步密钥文件 |
| **改进建议** | 实现 Secret Manager Watcher 接口，监听密钥变更事件并自动刷新 `KeyStore` |

---

### 3.2 低风险

#### 3.2.1 限流策略无端点级差异化

| 项目 | 说明 |
|------|------|
| **现状** | 限流使用全局默认 RPS/Burst（`PRIVACY_RATE_LIMIT_DEFAULT_RPS`），无端点级差异化配置 |
| **风险** | 高开销接口（如文件处理、批量 DP 计算）与轻量查询接口共享限流配额，可能被低开销高频请求占满 |
| **影响范围** | 全栈 |
| **改进建议** | 支持 `PRIVACY_RATE_LIMIT_PER_ENDPOINT` 配置（`Settings` 已预留 `RateLimitPerEndpoint` 字段），按路径前缀设置差异化限流 |

#### 3.2.2 Gateway TLS 终止依赖上游

| 项目 | 说明 |
|------|------|
| **现状** | Gateway 设置 `SkipTLSForRemote=true`，不终止 TLS，依赖上游负载均衡器/Ingress 处理 |
| **风险** | 若上游未正确配置 TLS 终止，Gateway 将以明文 HTTP 暴露 |
| **影响范围** | engine-go gateway |
| **改进建议** | 在部署文档中明确要求 Ingress/LB 必须配置 TLS 终止；或在 Gateway 增加启动时探测上游 TLS 状态的健康检查 |

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
| **路径别名绕过** | `/API/Mask` 绕过 `/api/mask` 权限校验 | 路径归一化后匹配 | ✅ 已防御 |
| **SQL 注入 / XSS** | 恶意 payload 通过 REST 接口注入 | WAF 73 条规则引擎 | ✅ 已防御 |
| **X-Forwarded-For 伪造** | 伪造来源 IP 绕过限流/白名单 | 可信代理配置，仅从可信代理解析 | ✅ 已防御 |
| **DDoS / 资源耗尽** | 高频请求占满服务资源 | 32 分片令牌桶限流 + TTL 自动淘汰 | ✅ 已防御 |
| **Clickjacking** | 嵌入 iframe 诱导用户操作 | `X-Frame-Options: DENY` + CSP | ✅ 已防御 |
| **mTLS 证书伪造** | 伪造客户端证书调用 gRPC | CN 白名单校验 + 5s 热重载吊销 | ✅ 已防御 |
| **内部服务横向移动** | 攻破一个服务后尝试调用其他服务 | IP 白名单 + mTLS + gRPC 方法级 scope 隔离 | ✅ 已防御 |
| **跨服务无差别调用** | 合法 mTLS 证书调用目标全部 gRPC 方法 | 全栈 gRPC 方法级 scope 映射（service-hub / datasource-mgr / audit-log） | ✅ 已防御 |
| **IP 白名单配置遗漏** | 运维遗漏 `PRIVACY_ALLOWED_CIDRS` 配置 | 启动阶段 `slog.Warn` 警告 + fail-closed 校验链 | ✅ 已防御 |
| **密钥管理平面攻击** | 通过 `/proc/environ` 获取环境变量中的 Key | 待改进：需集成 Secret Manager | ⚠️ 部分防御 |

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
| **密钥管理** | API Key 过期 + 全栈 KeyStore 热轮转（REST/gRPC 双通道）+ 遗留单 Key 空 scope（不再默认全权） | ✅ 全覆盖（Secret Manager 原生集成列为未来增强） |

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
| 热轮转中间件（engine-go） | `engine-go/internal/security/auth.go` |
| Fail-closed 校验 | `pkg/config/security.go` |
| IP 白名单 | `pkg/middleware/ip_allowlist.go` |
| gRPC 鉴权（service-hub） | `services/service-hub/internal/grpcserver/auth.go` |
| gRPC 鉴权（datasource-mgr） | `services/datasource-mgr/internal/grpcserver/auth.go` |
| gRPC 鉴权（audit-log） | `services/audit-log/internal/grpcserver/auth.go` |
| mTLS CN 白名单 + 热重载 | `pkg/tlsutil/whitelist.go` |
| mTLS gRPC 拦截器 | `pkg/tlsutil/grpc_interceptor.go` |
| 32 分片限流 | `pkg/middleware/ratelimit.go` |
| WAF 规则引擎 | `pkg/middleware/waf.go` |
| 认证失败指标 | `pkg/auth/middleware.go` |
| 指标注册 | `pkg/metrics/metrics.go` |
| 安全响应头 | `pkg/middleware/middleware.go` |
| JWT 吊销 + 登录锁定 | `console/app-lz/bff-go/internal/auth/jwt.go` |
| TOTP 双因素 | `console/app-lz/bff-go/internal/auth/totp.go` |
| 审计哈希链 | `pkg/store/audit_hash.go` |
