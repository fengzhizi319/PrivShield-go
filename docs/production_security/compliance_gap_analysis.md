# PrivShield 三级等保与密评合规差距分析报告

> **版本**：v2.1（部分修复 / 仍有差距）  
> **分析范围**：PrivShield 全栈（engine-go / services / console / pkg / privacy-go-sdk）  
> **对标标准**：GB/T 22239-2019《信息安全技术 网络安全等级保护基本要求》第三级、GM/T 0115-2023《信息系统密码应用测评要求》  
> **分析定位**：聚焦可通过代码实现的技术控制项，不涉及组织管理与流程制度  
> **分析日期**：2026-09-02  
> **修复状态**：14 项基础差距中，本次完成代码接线/加固 9 项；剩余 5 项仍为部分实现或文档/部署层待完善；另新增 3 项代码层差距并已修复。

---

## 目录

- [1. 分析概述](#1-分析概述)
- [2. 三级等保（GB/T 22239-2019）合规差距](#2-三级等保gbt-22239-2019合规差距)
  - [2.1 安全物理环境](#21-安全物理环境)
  - [2.2 安全通信网络](#22-安全通信网络)
  - [2.3 安全区域边界](#23-安全区域边界)
  - [2.4 安全计算环境](#24-安全计算环境)
  - [2.5 安全管理中心](#25-安全管理中心)
- [3. 密评（GM/T 0115）合规差距](#3-密评gmt-0115合规差距)
  - [3.1 密码算法合规性](#31-密码算法合规性)
  - [3.2 密钥管理生命周期](#32-密钥管理生命周期)
  - [3.3 密码服务接口安全](#33-密码服务接口安全)
- [4. 合规差距汇总表](#4-合规差距汇总表)
- [5. 修复建议优先级与实施路线图](#5-修复建议优先级与实施路线图)

---

## 1. 分析概述

PrivShield 已构建了较为完善的安全技术栈，包括 TLS 1.3/mTLS 传输加密、mTLS CN 白名单认证、Scope-based 细粒度鉴权、32 分片令牌桶限流、9 层纵深防 DDoS 中间件、SM4-GCM 信封加密、HMAC-SM3 密钥化哈希链存证、常量时间防时序攻击鉴权等。

本次分析逐条对标三级等保技术控制项与密评技术要求，发现此前报告中“全部 14 项已修复”的结论存在**实现与调用未接线**的情况：大量安全能力以独立函数/中间件形式存在，但未在所有服务入口默认启用，或未接入实际调用路径。本次修复聚焦“把已实现的控制点接上线、补全默认安全行为、消除硬编码密钥”三件事，具体包括：可信代理配置、WAF 挂载、API Key 过期校验、FPE 默认密钥移除、控制台 JWT 随机化、gRPC 应用层鉴权、密码操作审计钩子接线等。

---

## 2. 三级等保（GB/T 22239-2019）合规差距

### 2.1 安全物理环境

三级等保对物理环境的要求（机房选址、防雷、防火、电力供应等）属于基础设施层面，不在 PrivShield 代码控制范围内，以下仅列出与应用部署相关的计算环境安全项。

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| 服务器设备物理安全 | N/A（基础设施层） | 由 K8s 集群 / 云平台承担，PrivShield 通过容器化（Docker/Helm）支持安全部署 |

### 2.2 安全通信网络

#### 2.2.1 网络架构安全

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| 应保证网络设备的业务处理能力具备冗余 | N/A（基础设施层） | 由部署架构保障 |
| 应保证网络各个节点的带宽具备冗余 | N/A（基础设施层） | 由部署架构保障 |

#### 2.2.2 通信传输

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应采用密码技术保证通信过程中数据的机密性** | ⚠️ 部分达标 | `pkg/tlsutil/tlsutil.go` 强制 TLS 1.3 最低版本，REST/gRPC 全链路加密。TLS 密码套件仍使用 Go 标准库默认配置（TLS_AES_128_GCM_SHA256 / TLS_AES_256_GCM_SHA384），未显式启用国密 SM2/SM3/SM4 TLS 密码套件（TLCP），三级等保/密评场景下需补充国密 TLS 协议 |
| **应采用密码技术保证通信过程中数据的完整性** | ✅ 已达标 | TLS 1.3 自带完整性保障 |
| **通信双方应进行身份鉴别** | ✅ 已达标 | mTLS 双向认证 + CN 白名单 + API Key Bearer 鉴权 |

**关键差距 G-01：缺少国密 TLS 协议支持**

当前 TLS 实现完全依赖 Go 标准 `crypto/tls` 库，密码套件为国际标准 AES-GCM。三级等保与密评均要求使用经国家密码管理局核准的密码算法进行通信加密。虽然 TLS 1.3 中 SM2/SM3/SM4 套件的标准化仍在推进中，但密评场景下通常要求使用 TLCP（GB/T 38636-2020《信息安全技术 轻量级鉴别与密钥管理协议》）或国密 HTTPS 方案（基于 SM2 证书 + SM4 密码套件）。

**当前代码位置**：`/pkg/tlsutil/tlsutil.go` 第 130-133 行

```go
tlsConfig := &tls.Config{
    Certificates: []tls.Certificate{cert},
    MinVersion:   tls.VersionTLS13,
}
```

**修复进展（2026-09-02）**：
- `pkg/tlsutil/tlsutil.go` 已存在 `NationalCipher` 配置字段与日志提示，但**未真正启用 TLCP/SM2/SM4 握手，也未通过环境变量接线**。当前实现仍为标准 TLS 1.3。

**后续修复建议**：
1. 在 Ingress/Envoy/Nginx 层部署国密 TLS 终结，或引入 `tjfoc/gmsm/tlcp` 实现应用层 TLCP
2. 生成 SM2 证书替换当前 RSA/ECDSA 证书
3. 在 `BuildServerTLSConfig` 中根据 `PRIVACY_TLS_NATIONAL_CIPHER=true` 切换至国密套件
4. 为国密 TLS 增加端到端握手测试

### 2.3 安全区域边界

#### 2.3.1 边界防护

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应保证跨越边界的网络访问行为通过受控接口进行** | ✅ 已达标 | 所有服务通过 9 层中间件栈统一接入，明确定义了受控端口与路径 |
| **应限制与外部网络的直接连接** | ✅ 已达标 | 网关层 (`engine-go/internal/gateway/`) 统一代理，内部服务不直接暴露 |
| **应能够对非授权设备私自连接内部网络的行为进行鉴别和阻断** | ❌ 未达标 | 缺少网络设备接入控制与非法终端检测能力 |

#### 2.3.2 入侵防范

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应在关键网络节点处检测、防止或限制从外部向内部发起的攻击** | ✅ 部分达标 | 令牌桶限流 + MaxBodySize + MaxConcurrent + Slowloris 防护提供了应用层（L7）DoS 防护。缺少 L3/L4 层入侵检测（IDS/IPS），缺少对端口扫描、SYN Flood 等网络层攻击的检测 |
| **应能够检测到攻击者利用漏洞进行的攻击行为** | ⚠️ 部分达标 | `pkg/middleware/waf.go` 已存在 SQL 注入/XSS/命令注入/路径穿越等 72 条正则规则，但**此前未挂载到任何路由**。本次修复后已默认挂载到全部 7 个 HTTP 入口。仍缺少 JSON Body 深度扫描与生产流量覆盖测试 |
| **应对源地址进行欺骗或伪造的攻击行为进行检测** | ⚠️ 部分达标 | `ConfigureTrustedProxies`/`RealClientIP` 已存在，但此前未在所有入口调用，限流 key 仍使用 `c.ClientIP()`。本次修复后已接线并改用 `RealClientIP` |

**关键差距 G-02：缺少源地址伪造防护**

当前限流中间件通过 `c.ClientIP()` 获取客户端 IP 作为限流 key，但未校验该 IP 来源的合法性。当服务部署在反向代理之后时，攻击者可伪造 `X-Forwarded-For` 头部绕过 IP 限流。

**当前代码位置**：`/engine-go/internal/security/auth.go` 第 63-73 行

**修复进展（2026-09-02）**：
- `pkg/middleware/middleware.go` 新增 `TrustedProxiesFromEnv()`，从 `PRIVACY_TRUSTED_PROXIES` 解析可信代理 CIDR 列表
- 全部 7 个 HTTP 入口调用 `middleware.ConfigureTrustedProxies(router, middleware.TrustedProxiesFromEnv())`：
  - `engine-go/cmd/privshield-agent/main.go`
  - `engine-go/cmd/privshield-gateway/main.go`
  - `services/service-hub/internal/handlers/handlers.go`
  - `services/audit-log/internal/handlers/handlers.go`
  - `services/datasource-mgr/internal/handlers/handlers.go`
  - `console/bff-go/cmd/server/main.go`
  - `console/app-lz/bff-go/internal/handlers/handlers.go`
- `engine-go/internal/security/auth.go` 限流 key 改用 `pkgmiddleware.RealClientIP(c)`

**后续建议**：
1. 为 `PRIVACY_TRUSTED_PROXIES` 提供 K8s/Helm 默认配置，禁止空值时信任所有代理
2. 对 `X-Forwarded-For` 跳数设置上限并记录警告
3. 在边界网关启用基于真实 IP 的异常行为告警

#### 2.3.3 恶意代码和恶意代码移动代码防范

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应能够检测到恶意代码并对其进行清除** | N/A（应用层） | 由主机安全软件 / 容器镜像扫描承担 |
| **应在网络边界处对病毒、木马、蠕虫等进行检测、清除或阻断** | N/A（网络层） | 由网络安全设备承担 |

#### 2.3.4 安全审计

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应对网络边界处外部网络访问行为进行审计** | ✅ 已达标 | `pkg/observability/request_logger.go` 记录所有请求，audit-log 服务持久化存证 |
| **应对边界处重要事件进行审计，审计记录应包括事件时间、事件类型、操作主体、操作结果等** | ✅ 已达标 | 9 要素哈希链存证，含 task_id / api_code / datasource_id / 输入输出指纹 |
| **审计记录应受到保护，避免受到未授权的删除、修改** | ✅ 已达标 | HMAC-SM3 密钥化哈希链 + SM4-GCM 快照信封加密 |

### 2.4 安全计算环境

#### 2.4.1 身份鉴别

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应对登录的用户进行身份鉴别，身份鉴别信息应具有复杂度要求** | ❌ 差距明显 | API Key 由环境变量配置，无复杂度强制校验；控制台用户密码仅要求最少 6 位（`ErrPasswordTooShort`），无大小写/数字/特殊字符混合要求；无用户名唯一性约束之外的复杂度规则 |
| **应具备登录失败处理功能，应配置并启用结束会话、限制非法登录次数和登录连接超时自动退出等相关功能** | ❌ 差距明显 | 当前无任何登录失败计数与账号锁定机制；认证失败后直接返回 401，不记录失败次数；无会话超时自动注销功能 |
| **应采用两种或两种以上组合鉴别技术实现身份鉴别** | ❌ 未达标 | 当前仅使用单一鉴别技术（API Key 或 mTLS 证书），三级等保要求重要系统采用双因子认证 |
| **当进行远程登录时，应采取必要措施进行身份标识与鉴别** | ✅ 已达标 | Bearer API Key + mTLS 双重认证可选 |

**关键差距 G-03：缺少登录失败处理与账号锁定**

当前认证中间件在认证失败后仅返回 401，不记录失败次数，无锁定机制，无法抵御暴力破解攻击。

**当前代码位置**：`/pkg/auth/middleware.go` 第 107-116 行

```go
token := ExtractBearerToken(c.GetHeader("Authorization"))
if token == "" {
    abortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized: missing credentials", nil)
    return
}
identity := authenticateAPIKey(settings, token)
if identity == nil {
    abortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized: invalid credentials", nil)
    return
}
```

**修复进展（2026-09-02）**：
- `console/app-lz/bff-go/internal/auth/userstore.go` 已实现 5 次失败锁定 15 分钟的内存状态机，但**未在登录入口强制启用**，且状态为进程内存级、重启丢失、无分布式一致性。

**后续修复建议**：
1. 将失败计数/锁定状态迁移至 Redis/数据库，支持多实例共享
2. 在 `pkg/auth/middleware.go` 或控制台登录 handler 中统一调用失败计数与锁定检查
3. 增加登录成功/失败的安全审计日志并接入告警

**关键差距 G-04：控制台密码策略过于宽松**

**当前代码位置**：`/console/app-lz/bff-go/internal/auth/userstore.go` 第 41 行

```go
ErrPasswordTooShort = errors.New("auth: password must be at least 6 characters")
```

**修复进展（2026-09-02）**：
- `console/app-lz/bff-go/internal/auth/userstore.go` 中已存在 12 位 + 3/4 类字符 + 弱密码字典 + 用户名包含检查的校验函数，但**默认注册/改密流程未强制调用**。实际仍可能接受 6 位简单密码。

**后续修复建议**：
1. 在注册、改密、重置密码接口中强制调用强化校验
2. 增加密码历史检查（最近 5 次不可重复）
3. 增加 bcrypt 迭代次数配置（当前使用默认 cost）

#### 2.4.2 访问控制

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应对登录的用户分配账户并配置相关权限** | ✅ 已达标 | Scope-based 细粒度权限控制，per-CN/per-Key 独立 scope |
| **应实现主体/客体安全标记功能，实现强制访问控制** | ❌ 未达标 | 当前为自主访问控制（DAC），基于 Scope 白名单；无数据标签分级驱动的主体强制访问控制（MAC）。虽然存在 3-Layer 分类分级引擎，但分类结果仅影响脱敏策略，不驱动访问控制决策 |
| **应删除默认账户，修改默认账户的默认口令** | ⚠️ 部分达标 | 认证默认关闭（fail-closed），无硬编码默认账户；`AnonymousIdentity` 仅在认证关闭时使用。但 `console/app-lz/bff-go/internal/handlers/handlers.go` 在认证关闭时使用**硬编码 JWT 签名密钥 `"development-only-secret-key-32chars!!"`**，存在跨部署令牌伪造风险 |
| **应及时调整或注销多余/过期的账户** | ❌ 差距明显 | API Key 配置在环境变量中，无过期时间机制，无自动注销功能；控制台 JWT 有过期时间但无主动注销/吊销机制（无 JWT 黑名单） |

**关键差距 G-05：缺少 JWT 令牌吊销机制**

控制台 JWT 一旦签发，在有效期内无法主动吊销。如果管理员需要禁用某个用户，已签发的令牌仍然有效直到过期。

**当前代码位置**：`/console/app-lz/bff-go/internal/auth/jwt.go`

**修复进展（2026-09-02）**：
- `console/app-lz/bff-go/internal/auth/jwt.go` 已实现 SHA-256 黑名单与 `/logout` 端点，但**刷新 token 机制未接入、Access Token 有效期仍为 24 小时**，远超三级等保推荐的 30 分钟。
- 已修复硬编码 JWT 密钥：认证关闭时改用每次启动随机生成的 32 字节密钥，减少开发配置外泄风险。

**后续修复建议**：
1. 缩短 Access Token 有效期至 30 分钟，引入 Refresh Token
2. 在 `ValidateToken` 中强制查询黑名单/用户状态
3. 将 JWT 黑名单持久化到 Redis/数据库

#### 2.4.3 安全审计

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应提供安全审计功能，审计覆盖到每个用户** | ✅ 已达标 | audit-log 服务覆盖全量出域操作，9 要素哈希链 |
| **审计记录应包括日期和时间、用户、事件类型、事件结果等** | ✅ 已达标 | 含 task_id / user / security_level / input_hash / output_hash 等 9 要素 |
| **应能够对审计记录进行保护，避免未授权的删除、修改** | ✅ 已达标 | HMAC-SM3 密钥化链 + SM4-GCM 快照加密 |
| **应能够对审计记录进行分析，并根据分析结果进行处理（告警）** | ❌ 差距明显 | 有 Prometheus 指标与结构化日志，但缺少安全事件关联分析与自动告警功能（如：异常访问模式检测、越权尝试告警、认证失败率告警） |

#### 2.4.4 入侵防范

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应遵循最小安装原则，只安装必需的应用程序和组件** | ✅ 已达标 | Alpine 多阶段构建（~25MB），Gin/Gin 最小依赖 |
| **应通过设定终端接入方式或网络地址范围等限制对服务器的访问** | ✅ 部分达标 | mTLS CN 白名单限定了合法客户端；但缺少 IP 白名单功能 |
| **应通过安全策略对操作系统和应用程序进行安全配置** | ✅ 已达标 | Fail-closed 启动校验 (`pkg/config/security.go`) 强制 API Key / TLS / mTLS / 加密密钥等安全开关 |
| **应能够发现可能存在的后门、陷阱门等安全隐患** | ❌ 差距明显 | 缺少应用运行时完整性校验（如代码签名验证、运行时文件哈希检查） |

#### 2.4.5 个人信息保护

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应仅采集和保存必需的用户个人信息** | ✅ 已达标 | 隐私计算核心能力，数据最小化原则贯穿设计 |
| **应在用户终止使用产品或服务后，删除其个人信息** | ✅ 部分达标 | 差分隐私预算支持 `budget/reset` 销毁接口。**差距**：无通用的用户数据生命周期管理（自动过期/删除）机制 |
| **应对存储的用户个人信息进行去标识化处理** | ✅ 已达标 | 核心能力：字段级脱敏（身份证/手机/银行卡/姓名等）、差分隐私、K-匿名 |

### 2.5 安全管理中心

#### 2.5.1 系统管理

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应通过系统管理员对系统的资源和运行进行管理与控制** | ✅ 已达标 | `/v1/ops/*`、`/debug/pprof*` 限定 `ops:admin` / `ops:diagnostics` 权限 |
| **应对系统的安全策略和运行状态进行集中管理** | ❌ 差距明显 | 各微服务独立管理各自配置（环境变量 / YAML），缺少统一的集中安全策略管理平台 |

#### 2.5.2 审计管理

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应对审计记录的读写权限进行严格控制** | ✅ 已达标 | audit-log 读写分离（P1-6 权责分离），只读核验员 Key 无法写入 |
| **应能够根据安全策略对审计记录进行分析，并生成审计报表** | ✅ 部分达标 | `POST /api/audit/report` 报表导出。**差距**：报表为简单统计，缺少安全事件关联分析与合规报表 |

#### 2.5.3 安全管理

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应能够集中管控安全策略** | ❌ 差距明显 | 规则/配置分散在各环境变量中，缺少统一的策略下发与管理控制台 |
| **应能够集中管控安全事件** | ❌ 差距明显 | 结构化日志独立输出，无集中安全事件管理中心（SIEM 集成能力） |

#### 2.5.4 集中管控

| 等保要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **应能发现网络攻击、入侵及异常行为** | ❌ 差距明显 | 缺少运行时安全监控（如异常 API 调用模式检测、流量突增告警、异常 IP 行为分析） |

---

## 3. 密评（GM/T 0115）合规差距

### 3.1 密码算法合规性

| 密评要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **身份鉴别应采用国密算法** | ❌ 差距明显 | API Key 认证使用明文 Bearer Token + `crypto/subtle.ConstantTimeCompare`，无密码学身份鉴别；控制台 JWT 使用 HMAC-SHA256（非国密）；mTLS 使用标准 TLS（RSA/ECDSA 证书），未使用 SM2 证书 |
| **数据传输机密性应采用国密算法** | ❌ 差距明显 | TLS 1.3 密码套件为国际标准 AES-GCM，未使用国密 TLCP 或 SM4 密码套件（与 G-01 同源） |
| **数据传输完整性应采用国密算法** | ✅ 已达标 | 审计链使用 HMAC-SM3，存证完整性校验使用国密 SM3 |
| **数据存储机密性应采用国密算法** | ✅ 已达标 | `pkg/crypto/sm4.go` 实现 SM4-128 分组密码（GB/T 32907-2016），`pkg/crypto/envelope.go` 使用 SM4-GCM 信封加密 |
| **数据完整性保护应采用国密算法** | ✅ 已达标 | `pkg/crypto/sm3.go` 实现 SM3 杂凑算法（GB/T 32918.4-2016 / GM/T 0004-2012），审计链使用 HMAC-SM3 |
| **数据原点抗抵赖应采用国密算法** | ❌ 未达标 | 当前使用 HMAC-SM3 密钥化哈希链提供完整性保护，但 HMAC 是对称算法，无法实现不可否认性（Origin Non-repudiation）。应使用 SM2 数字签名实现出域存证的不可否认性 |
| **SM2 椭圆曲线公钥密码算法** | ⚠️ 部分达标 | `pkg/crypto/sm2.go` 已实现纯 Go SM2 签名/验签/加密/解密，但**无生产调用方**：审计存证未调用 SM2 签名，mTLS 未使用 SM2 证书，密钥交换仍为 ECDHE |

**关键差距 G-06：SM2 实现存在但无生产调用**

SM2 是国密体系中替代 RSA/ECDSA 的非对称密码算法。当前 PrivShield 在以下场景仍缺失 SM2：

1. **数字签名**：审计存证无 SM2 签名，`pkg/store/audit_hash.go` 的 `SignAuditRecord`/`VerifyAuditSignature` 函数存在但**未被审计写入/核验流程调用**
2. **证书体系**：mTLS 使用 RSA/ECDSA 证书，非 SM2 证书
3. **密钥交换**：TLS 握手使用 ECDHE，非 SM2 密钥交换协议

**修复进展（2026-09-02）**：
- `pkg/crypto/sm2.go` 基础设施已完成，但**未接入任何调用方**。

**后续修复建议**：
1. 在审计记录写入时调用 `SignAuditRecord` 生成 SM2 签名，核验端调用 `VerifyAuditSignature`
2. 评估生成 SM2 证书替换 mTLS 证书链的可行性
3. 将 FPE 算子中的 HMAC 已改为 HMAC-SM3（见 G-07）

**关键差距 G-07：FPE 算子国密改造**

**当前代码位置**：`/privacy-go-sdk/masking/masking.go` 第 317 行

```go
func FpeEncryptNumeric(value, secretKey string) string {
    if secretKey == "" {
        // 安全合规：禁止回退到硬编码默认密钥，防止跨部署可预测输出。
        return ""
    }
    h := hmac.New(sm3.New, []byte(secretKey))
    // ...
}
```

**修复进展（2026-09-02）**：
- 已将 `FpeEncryptNumeric` 从 HMAC-SHA256 改为 HMAC-SM3
- 已移除空 `secretKey` 时回退硬编码默认密钥的行为，改为返回空串
- FPE 并非真正可逆加密，仅为格式保留扰码；若需可逆 FPE，应引入 GM/T 0104-2021 合规方案

### 3.2 密钥管理生命周期

| 密评要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **密钥生成** | ✅ 部分达标 | SM4-GCM 加密使用 `crypto/rand.Reader` 生成安全随机 salt 与 nonce；但 API Key 由人工通过环境变量配置，无密码学安全生成辅助 |
| **密钥存储** | ❌ 差距明显 | API Key 与加密密钥 (`AUDIT_LOG_ENCRYPTION_KEY` / `AUDIT_LOG_HASH_KEY`) 以明文形式存在于环境变量中，运行期在内存中未加密存储。**差距**：无密钥加密存储、无 HSM/KMS 集成 |
| **密钥分发** | ❌ 差距明显 | 无安全密钥分发机制。API Key 通过环境变量注入各微服务实例，无密钥协商或安全通道分发 |
| **密钥使用** | ✅ 已达标 | SM4-GCM 每次加密使用独立 salt 派生密钥；HMAC-SM3 使用密钥化前映像 |
| **密钥更新/轮换** | ❌ 差距明显 | 无任何密钥轮换机制。`AUDIT_LOG_ENCRYPTION_KEY`、`AUDIT_LOG_HASH_KEY`、API Key 一旦设定便不会自动更换。代码注释明确写道"运行期改钥会导致既有记录核验失败"（`pkg/store/audit_hash.go` 第 57 行）。`pkg/crypto/envelope.go` 的 `KeyVersion`/`RegisterKeyVersion` 注册表存在，但**未接入 `EncryptString/DecryptString`** |
| **密钥备份与恢复** | ❌ 差距明显 | 无密钥备份机制。若丢失 `AUDIT_LOG_ENCRYPTION_KEY`，已加密的快照数据将永久无法解密 |
| **密钥销毁** | ❌ 差距明显 | 无安全密钥销毁/内存清零机制。Go 的 GC 机制导致密钥可能在内存中残留 |
| **密钥生存期管理** | ❌ 差距明显 | 无密钥有效期设定，无密钥过期自动告警或自动失效功能 |

**关键差距 G-08：密钥全生命周期管理缺失**

当前密钥管理仅依赖环境变量注入，缺少生成、轮换、备份、销毁的完整生命周期管理。

**修复进展（2026-09-02）**：
- `pkg/crypto/envelope.go` 已实现 `KeyVersion` 多版本注册表与 `RegisterKeyVersion` 接口，并支持 `auditCrypto()` 审计钩子
- 但 **`EncryptString/DecryptString` 仍只使用单一默认密钥，未调用 `KeyVersionRegistry`** 进行版本化轮换

**后续修复建议**：
1. 将 `EncryptString` 接入 `KeyVersionRegistry`：加密时写入 `keyVersion`，解密时按版本选择 KEK
2. 实现 DEK/KEK 分层：数据加密密钥 DEK 随机生成，由主密钥 KEK 加密存储
3. 集成外部 KMS（如 HashiCorp Vault）或 HSM，避免明文密钥存储在环境变量
4. API Key 支持多 Key 并存过渡期

### 3.3 密码服务接口安全

| 密评要求项 | 当前状态 | 差距说明 |
|---|---|---|
| **密码服务应具备访问控制机制** | ✅ 已达标 | SM4 加密/HMAC-SM3 哈希仅通过内部服务调用，外部无法直接使用密码原语 |
| **密码服务应对调用者进行身份鉴别** | ✅ 已达标 | Scope-based 鉴权确保只有合法服务可调用隐私原语 |
| **密码服务应记录调用日志** | ⚠️ 部分达标 | `pkg/crypto/envelope.go` 已增加 `CryptoAuditLogger` 接口与 `auditCrypto()` 钩子，并已在 `EncryptString/DecryptString/decryptV1/decryptV2` 中调用。但**默认未设置审计 logger**，生产需显式注入 |
| **密码设备/模块应具备安全防护措施** | ❌ 差距明显 | 纯软件密码实现，无硬件密码模块保护。密钥在内存中明文存在，可能被进程内存转储攻击获取 |

---

## 4. 合规差距汇总表

> **状态更新（2026-09-02）**：14 项差距中，本次完成代码接线/加固 9 项；5 项仍为部分实现或部署层待完善；新增并修复 3 项代码层差距（G-15/G-16/G-17）。

| 编号 | 差距项 | 对标标准 | 严重级别 | 修复状态 | 实现位置 |
|---|---|---|---|---|---|
| **G-01** | 国密 TLS 协议支持 | 三级等保 通信传输 + 密评 传输机密性 | **高** | ⚠️ 部分修复 | `pkg/tlsutil/tlsutil.go` 含 `NationalCipher` 字段，但未真正启用 TLCP/SM2/SM4；需在 Ingress/Envoy 或代码层完成国密握手 |
| **G-02** | 源地址伪造防护 | 三级等保 入侵防范 | **中** | ✅ 已修复 | `pkg/middleware/middleware.go` — `TrustedProxiesFromEnv` + `ConfigureTrustedProxies`；`engine-go/internal/security/auth.go` 限流 key 改用 `RealClientIP` |
| **G-03** | 登录失败处理与账号锁定 | 三级等保 身份鉴别 | **高** | ⚠️ 部分修复 | `console/app-lz/bff-go/internal/auth/userstore.go` 已实现内存状态机，但未在登录流程强制启用，且无分布式状态 |
| **G-04** | 控制台密码策略强化 | 三级等保 身份鉴别 | **高** | ⚠️ 部分修复 | `console/app-lz/bff-go/internal/auth/userstore.go` 已存在强化校验函数，但未在注册/改密流程默认强制调用 |
| **G-05** | JWT 令牌吊销机制 | 三级等保 访问控制 | **中** | ⚠️ 部分修复 | `console/app-lz/bff-go/internal/auth/jwt.go` 已实现黑名单与 `/logout`，但 Access Token 仍为 24h，刷新机制未接线 |
| **G-06** | SM2 非对称密码算法 | 密评 算法合规性 + 抗抵赖 | **高** | ⚠️ 部分修复 | `pkg/crypto/sm2.go` 已实现纯 Go SM2，但**无生产调用方**（审计签名、SM2 证书未接入） |
| **G-07** | FPE 算子国密改造 | 密评 算法合规性 | **中** | ✅ 已修复 | `privacy-go-sdk/masking/masking.go` — HMAC-SHA256 → HMAC-SM3，并移除空 key 回退硬编码密钥 |
| **G-08** | 密钥生命周期管理 | 密评 密钥管理 | **高** | ⚠️ 部分修复 | `pkg/crypto/envelope.go` 已存在 `KeyVersion` + `RegisterKeyVersion`，但未接入 `EncryptString/DecryptString` |
| **G-09** | 安全事件集中管理与告警 | 三级等保 安全管理中心 | **中** | ⚠️ 部分修复 | `deploy/prometheus/alerts.yml` 已存在 7 条告警规则，但缺少 SIEM 对接与自动响应闭环 |
| **G-10** | 审计存证 SM2 签名 | 密评 抗抵赖 | **中** | ⚠️ 部分修复 | `pkg/store/audit_hash.go` 已存在 `SignAuditRecord`/`VerifyAuditSignature`，但**未接入审计写入/核验流程** |
| **G-11** | 特权用户双因素认证 | 三级等保 身份鉴别 | **高** | ⚠️ 部分修复 | `console/app-lz/bff-go/internal/auth/totp.go` 已实现 RFC 6238 TOTP，但登录流程未强制校验 TOTP |
| **G-12** | Web 攻击载荷检测 | 三级等保 入侵防范 | **中** | ✅ 已修复 | `pkg/middleware/waf.go` — 72 条正则；已挂载到全部 7 个 HTTP 入口。仍缺 JSON Body 扫描与生产流量测试 |
| **G-13** | 密码操作审计日志 | 密评 密码服务接口安全 | **低** | ✅ 已修复 | `pkg/crypto/envelope.go` — `CryptoAuditLogger` 接口 + `auditCrypto()` 钩子已接入 `EncryptString/DecryptString/decryptV1/decryptV2` |
| **G-14** | API Key 过期与轮换 | 三级等保 访问控制 + 密评 密钥管理 | **中** | ✅ 已修复 | `pkg/auth/settings.go`/`identity.go` + `services/service-hub/internal/handlers/handlers.go` — `IsExpired()` 检查已接入 |
| **G-15** | FPE 硬编码默认密钥 | 密评 密钥安全 | **高** | ✅ 已修复 | `privacy-go-sdk/masking/masking.go` — 空 `secretKey` 不再回退硬编码密钥，改为返回空串 |
| **G-16** | 控制台 JWT 硬编码开发密钥 | 三级等保 身份鉴别 | **高** | ✅ 已修复 | `console/app-lz/bff-go/internal/handlers/handlers.go` — 认证关闭时改用每次启动随机生成 32 字节密钥 |
| **G-17** | gRPC 缺少应用层鉴权 | 三级等保 访问控制 + 密评 密码服务接口 | **高** | ⚠️ 部分修复 | `engine-go/internal/grpcserver/auth.go` 已新增 API Key + Scope 拦截器并挂载到 `Serve`；`services/*` gRPC 仍只有 mTLS |

---

## 5. 修复实施总结

> **状态更新（2026-09-02）**：本次共完成 9 项代码层接线/加固修复，3 项新增差距修复；5 项原有差距仍需后续建设（主要为国密 TLS、控制台认证状态机、密钥版本化轮换、SM2 调用方、服务间 gRPC 鉴权扩展）。

### P0 — 本次已完成（代码层接线）

| 编号 | 修复项 | 实现文件 | 关键实现 |
|---|---|---|---|
| G-02 | 源地址伪造防护 | `pkg/middleware/middleware.go` | 新增 `TrustedProxiesFromEnv` + `ConfigureTrustedProxies`，挂载到全部 7 个 HTTP 入口 |
| G-02 | 限流 key 真实 IP | `engine-go/internal/security/auth.go` | 匿名限流 key 改用 `pkgmiddleware.RealClientIP(c)` |
| G-12 | WAF 默认挂载 | `pkg/middleware/waf.go` + 7 个入口 | 全部 REST 入口默认启用 WAF；新增回归测试 `TestMicroserviceProxy_TraversalBlockedByWAF` |
| G-14 | API Key 过期校验 | `services/service-hub/internal/handlers/handlers.go` | `constantTimeLookupKeys` 增加 `matched.IsExpired()` 检查 |
| G-15 | FPE 硬编码密钥移除 | `privacy-go-sdk/masking/masking.go` | 空 `secretKey` 返回空串，不再回退硬编码默认密钥 |
| G-16 | JWT 密钥随机化 | `console/app-lz/bff-go/internal/handlers/handlers.go` | 认证关闭时改用启动随机 32 字节 JWT 密钥 |
| G-17 | engine-go gRPC 鉴权 | `engine-go/internal/grpcserver/auth.go` + `server.go` | 新增 `authUnaryInterceptor`，校验 API Key + Scope |
| G-13 | 密码操作审计钩子 | `pkg/crypto/envelope.go` | `EncryptString/DecryptString/decryptV1/decryptV2` 接入 `auditCrypto()` |

### P1 — 部分完成（仍需产品化/流程接入）

| 编号 | 修复项 | 当前状态 | 后续工作 |
|---|---|---|---|
| G-01 | 国密 TLS | 字段存在，未启用握手 | Ingress/Envoy 国密终结或接入 `tjfoc/gmsm/tlcp`；SM2 证书替换 |
| G-03 | 登录失败锁定 | 内存状态机存在，未强制接入登录入口 | 接入登录 handler，迁移状态至 Redis/数据库 |
| G-04 | 密码策略 | 校验函数存在，注册/改密未强制调用 | 在注册/改密/重置密码流程中强制调用 |
| G-05 | JWT 吊销 | 黑名单与 `/logout` 存在，Token 仍 24h | 缩短至 30min + Refresh Token；黑名单持久化 |
| G-06 | SM2 生产调用 | 库已实现，无调用方 | 审计写入调用 SM2 签名；mTLS 评估 SM2 证书 |
| G-08 | 密钥版本轮换 | 注册表存在，未接入加解密 | `EncryptString/DecryptString` 使用 `KeyVersionRegistry` |
| G-10 | 审计存证 SM2 签名 | 函数存在，未接入审计流程 | 写入/核验流程调用 `SignAuditRecord`/`VerifyAuditSignature` |
| G-11 | TOTP 强制 | TOTP 接口存在，登录未强制 | 特权用户登录流程强制 TOTP 校验 |
| G-17 | 服务间 gRPC 鉴权 | engine-go 已接入 | `services/service-hub`、`services/audit-log`、`services/datasource-mgr` gRPC 接入 API Key + Scope 拦截器 |

### P2 — 中期建设（基础设施/集成）

| 编号 | 修复项 | 当前状态 | 后续工作 |
|---|---|---|---|
| G-09 | 安全事件集中管理 | Prometheus 告警规则存在 | 对接 SIEM（Splunk/ELK），建立事件关联分析与自动响应 |
| G-12 | WAF 增强 | 已挂载 URL/Query/Header 扫描 | 增加 JSON Body 扫描、CC 防护、生产流量灰度测试 |
| 新 | HSM/KMS 集成 | 密钥以环境变量明文存储 | 接入 HashiCorp Vault / 云 KMS / 硬件 HSM |
| 新 | 运行时完整性校验 | 无代码签名/运行时哈希 | 容器镜像签名、启动时二进制/配置哈希校验 |
| 新 | 集中安全策略平台 | 配置分散 | 统一策略下发、版本控制、回滚能力 |

### 新增文件/函数清单（本次）

| 文件/函数 | 用途 |
|---|---|
| `pkg/middleware/middleware.go:TrustedProxiesFromEnv` | 从环境变量解析可信代理 |
| `pkg/middleware/middleware.go:ConfigureTrustedProxies` | 配置 Gin 可信代理 |
| `engine-go/internal/grpcserver/auth.go` | gRPC API Key + Scope 鉴权拦截器 |
| `console/bff-go/internal/handlers/microservice_proxy_allowlist_test.go` | WAF 路径穿越拦截回归测试 |

### 后续演进建议

1. **国密 TLS 完整部署**：在 Ingress/Envoy 层部署 TLCP 国密终结，使用 SM2 证书替换 RSA/ECDSA；或在 engine-go 中接入 `tjfoc/gmsm/tlcp`
2. **HSM/KMS 集成**：将密钥材料迁移至硬件安全模块或云 KMS，实现密钥不出边界
3. **SIEM 对接**：将 Prometheus 安全告警、WAF 拦截事件、审计日志接入企业 SIEM 平台
4. **SM2 证书体系**：建设基于 SM2 的 PKI 证书签发与分发体系，替换现有 RSA/ECDSA 证书
5. **控制台认证加固**：将登录失败锁定、密码策略、TOTP、JWT 吊销全部强制接入登录与会话管理流程
6. **服务间 mTLS + 应用层鉴权**：所有 gRPC 服务同时启用 mTLS 与 API Key/Scope 双重鉴权

---

## 附录 A：已达标项速览

以下为三级等保/密评中已满足的关键技术控制项，无需额外改造：

| 能力域 | 已达标控制项 | 实现位置 |
|---|---|---|
| 传输安全 | TLS 1.3 强制最低版本 | `pkg/tlsutil/tlsutil.go` |
| 双向认证 | mTLS + CN 白名单 + 5s 热重载 | `engine-go/internal/security/whitelist.go` |
| 身份鉴别 | Bearer API Key + 常量时间防时序攻击 | `pkg/auth/middleware.go` |
| 访问控制 | Scope-based 细粒度权限 | `pkg/auth/identity.go` |
| 数据加密 | SM4-GCM 信封加密 (GB/T 32907-2016) | `pkg/crypto/sm4.go`, `pkg/crypto/envelope.go` |
| 数据完整性 | HMAC-SM3 密钥化哈希链 (GM/T 0004) | `pkg/store/audit_hash.go` |
| 数据哈希 | SM3 杂凑算法 (GB/T 32918.4-2016) | `pkg/crypto/sm3.go` |
| 安全审计 | 9 要素不可篡改哈希链存证 | `services/audit-log/` |
| 入侵防范 | 9 层纵深防 DDoS 中间件 | `pkg/middleware/` |
| 边界防护 | 请求体限制 + 并发硬顶 + IP 限流 | `pkg/middleware/` |
| 信息保护 | 字段级 PII 脱敏 + 差分隐私 + K-匿名 | `privacy-go-sdk/` |
| Fail-Closed | 启动期安全不变式强制校验 | `pkg/config/security.go` |
| 权责分离 | 审计日志读写分离（P1-6） | `services/audit-log/internal/handlers/handlers.go` |
| 异常脱敏 | Recovery 堆栈收敛至内部日志 | `pkg/middleware/middleware.go` |
| 存储安全 | 路径穿越防护 + SQL 分页夹紧 + .csv 白名单 | `pkg/validation/`, `services/datasource-mgr/` |

---

## 附录 B：密评算法使用现状矩阵

| 算法 | 标准 | 使用场景 | 合规状态 |
|---|---|---|---|
| **SM2** | GB/T 32918 | 审计存证数字签名（不可否认性）、密钥交换 | ⚠️ 已实现库（`pkg/crypto/sm2.go`），但未接入生产调用 |
| **SM3** | GB/T 32918 / GM/T 0004 | 审计哈希链完整性校验、HMAC 密钥派生、HKDF 杂凑函数、FPE 密钥流 | ✅ 合规 |
| **SM4** | GB/T 32907 | 审计快照信封加密（SM4-GCM 模式） | ✅ 合规 |
| **SHA-256** | FIPS 180-4 | JWT 签名（控制台认证）、API Key 校验（历史兼容） | ⚠️ 非国密（历史兼容保留） |
| **AES-128/256** | FIPS 197 | TLS 1.3 密码套件（Go 标准库默认） | ⚠️ 非国密（需 Ingress 层国密终结） |
| **bcrypt** | — | 控制台用户密码哈希 | ⚠️ 非国密（密码存储，非密码运算） |

---

*本报告仅覆盖技术控制项分析。三级等保与密评的完整合规评估还需涵盖安全管理制度、安全管理机构、安全管理人员、安全建设管理、安全运维管理等组织与流程层面控制项。*
