# 隐私计算核心引擎 (privacy-engine) 安全加固产品需求文档 (PRD)

> **版本**：v1.0.0
> **适用范围**：`services/privacy-engine`（Go module `engine-go`）—— REST (Gin) `:8079` / gRPC `:50051`，网关 `:8000` / `:50000`
> **配套文档**：安全体系架构与设计见 [`./security.md`](./security.md)（本文聚焦「需求条目 + 验收标准」，架构阐述以 security.md 为准）
> **对标标准**：GB/T 22239-2019（等保三级）、GB/T 39786-2021（密评三级）、GM/T 0024-2014（TLCP 国密双证书）、《数据安全法》《个人信息保护法》

---

## 1. 概述

本文档定义 `privacy-engine` 隐私计算核心引擎的安全产品需求与验收标准。引擎处于**内部数据安全计算域（Core Domain）**，不对外部业务系统直接暴露，对外唯一入口是 `service-hub` 调度中枢；引擎仅接受来自 `service-hub` / 受信内部组件（经 mTLS/TLCP + Scope 鉴权）的调用。

安全能力覆盖**网络准入 → 传输证书 → 请求净化（WAF）→ 身份鉴权 → 接口 Scope → 资源限流 → 内核数据防护**的纵深防御链路，全部开关默认关闭或 `fail-closed`，保证向后兼容与本地开发无感。

---

## 2. 设计目标

- **零信任内部调用**：即便在专网内，每一次脱敏/分类/DP 调用都必须携带有效凭证并命中接口级 Scope，绝不因「内网可信」而放行。
- **最小权限 Scope 分域**：内部脱敏权限（`privacy:mask`、`medical:process`、`agent:process`）与运维权限（`ops:diagnostics`、`ops:admin`）严格隔离，外部 Key 绝不持有内部 Scope。
- **权限映射零遗漏**：任何新增注册接口都不会因「加路由忘配权限」而绕过鉴权，形成运行时兜底 + 启动期审计 + CI 门禁的三层防御闭环。
- **国密合规**：脱敏与存证链路满足密评三级对密码算法（SM2/SM3/SM4）、密钥管理与内存清零的要求。
- **默认安全**：所有安全开关通过环境变量显式开启；绑定非回环地址远程暴露时强制要求鉴权 + TLS，否则拒绝启动（fail-closed）。

---

## 3. 用户故事

| 角色 | 故事 |
|---|---|
| **平台运维** | 通过 TLS/TLCP 加密 REST/gRPC 流量，避免隐私原语请求在链路上被窃听或篡改。 |
| **service-hub（内部编排）** | 使用内部 API Key 或 mTLS 证书身份调用 agent，按被授予的 Scope 访问脱敏/分类/DP 原语。 |
| **外部业务/数据门户** | 仅获得被明确授予的最小 Scope（如只读脱敏），不能调用 DP 消耗预算、`medical:process` 或运维端点。 |
| **SRE** | `/health`、`/livez`、`/readyz` 保持匿名可访问，便于 Kubernetes 探针与健康检查，且默认不限速。 |
| **安全团队** | 缺失/无效凭证返回 `401/UNAUTHENTICATED`，越权返回 `403/PERMISSION_DENIED`，超速返回 `429/RESOURCE_EXHAUSTED`；诊断与 pprof 端点按 Scope 分级收敛。 |
| **研发（接口演进）** | 新增路由若漏配权限映射，服务启动即告警、CI `go test` 即刻拦截，避免带漏洞上线。 |

---

## 4. 功能需求矩阵

### 4.1 身份认证与接口级 Scope 鉴权（SEC-AUTH）

| ID | 需求描述 | 实现方式 / 开关 |
|---|---|---|
| **SEC-AUTH-1** | 启用后除健康探针外所有接口必须携带有效凭证，缺失/无效即拒绝 | `AGENT_AUTH_ENABLED=true` |
| **SEC-AUTH-2** | API Key 比对必须使用常量时间，杜绝响应时间差侧信道猜钥 | `subtle.ConstantTimeCompare`（`pkg/auth`） |
| **SEC-AUTH-3** | 支持 internal（可持高权限 Scope）与 external（受限 Scope）两类身份分域 | `AGENT_AUTH_INTERNAL_API_KEYS` / `AGENT_AUTH_EXTERNAL_API_KEYS` |
| **SEC-AUTH-4** | Key 格式为 `token:name:scope1,scope2`，`*` 表示通配 | `pkgauth.LoadAPIKeysFromEnv` |
| **SEC-AUTH-5** | 支持从密钥文件加载多版本 Key 并每请求读取最新集合，支持无缝热轮换与 ISO 8601 到期自动失效（G-14） | `AGENT_AUTH_KEYS_FILE` → KeyStore |
| **SEC-AUTH-6** | 认证通过后按「路径/方法 → 所需权限」做接口级最小权限校验，缺少所需 Scope 直接拒绝并计入 `AuthForbiddenTotal` | REST `PermissionForRESTPath`；gRPC `PermissionForGRPCMethod`（拦截器 `internal/grpcserver/auth.go`） |
| **SEC-AUTH-7** | 鉴权失败返回明确状态码与错误信息，不泄露内部实现细节 | REST `401`/`403`；gRPC `UNAUTHENTICATED`/`PERMISSION_DENIED` |
| **SEC-AUTH-8** | 健康探针默认免鉴权并注入受限身份，可关闭豁免 | `AGENT_HEALTH_NO_AUTH`（默认 `true`） |

### 4.2 权限映射完整性三层防御（SEC-PERM）

| ID | 层次 | 需求描述 | 实现方式 |
|---|---|---|---|
| **SEC-PERM-1** | 运行时兜底 | 未显式映射的路径/方法默认归入最高 `admin` 权限，绝不因漏配而放行（fail-closed） | `PermissionForRESTPath`/`PermissionForGRPCMethod` 的 `default` 分支 |
| **SEC-PERM-2** | 启动期审计 | 进程启动遍历全部路由，凡落入兜底 `admin`（且不在基础设施白名单）即打 `WARN` 列出 `method+path` | `pkgauth.LogRoutePermissionAudit`（`RegisterRoutes` 末尾调用） |
| **SEC-PERM-3** | CI 门禁 | 单测断言「全部路由均有显式映射」，一旦新增路由漏配即刻 `go test` 失败 | `TestAllRoutesHaveExplicitPermission`（`internal/rest/route_audit_test.go`） |
| **SEC-PERM-4** | 通用复用 | 审计器下沉共享库，privacy-engine / service-hub / audit-log 三服务共用，各自传入权限函数与兜底哨兵（`admin`/`audit:admin`） | `pkg/auth/route_audit.go::AuditRoutePermissions` |
| **SEC-PERM-5** | 有意豁免 | 对确属「仅 `admin` 可见」的基础设施路由（如 pprof）通过白名单显式豁免，避免误报 | `allowFallback map[string]bool` 参数 |

### 4.3 传输层安全 TLS/TLCP + mTLS CN 白名单（SEC-TLS）

| ID | 需求描述 | 实现方式 / 开关 |
|---|---|---|
| **SEC-TLS-1** | 启用后 REST `:8079` 与 gRPC `:50051` 同时受 TLS 保护，禁用弱密码套件 | `AGENT_TLS_ENABLED=true`（RFC 8446，TLS 1.3） |
| **SEC-TLS-2** | 支持国密 TLCP 双证书（签名 + 加密通道，SM2 认证 + SM4 传输） | `AGENT_TLS_NATIONAL_CIPHER=true`（GM/T 0024） |
| **SEC-TLS-3** | mTLS 双向身份鉴别：传输层 CA 信任链校验 + 应用层 CN 白名单匹配 | `AGENT_AUTH_INTERNAL_MTLS_ENABLED=true` + `AGENT_AUTH_MTLS_WHITELIST_FILE` |
| **SEC-TLS-4** | 每个允许 CN 绑定独立可调用方法域（细粒度授权），基于文件 mtime 5s 节流热重载 | `config/mtls-whitelist.yaml` + `pkg/tlsutil.DynamicWhitelist` |
| **SEC-TLS-5** | 未知 CN fail-closed：不在白名单的证书主体默认空 Scope 并拒绝 | `internal/security/whitelist.go` |
| **SEC-TLS-6** | 绑定非回环地址远程暴露时，强制要求配置 API Key + 启用 Auth + TLS，否则拒绝启动 | `pkg/config.ValidateSecurityRequirements` |

### 4.4 纵深防御中间件链（SEC-MW）

| ID | 需求描述 | 实现方式 / 位置 |
|---|---|---|
| **SEC-MW-1** | 网络准入：仅放行受信 CIDR 内客户端 IP | `IPAllowlist`（`AGENT_ALLOWED_CIDRS`，`pkg/middleware`） |
| **SEC-MW-2** | 安全响应头加固：HSTS / `X-Content-Type-Options=nosniff` / `X-Frame-Options=DENY` / CSP | `SecurityHeaders`（`internal/security/auth.go`） |
| **SEC-MW-3** | 报文约束：请求体 ≤ 64MB，超限返回 `413`，防超大载荷内存耗尽 DoS | `MaxBodySize`（`internal/rest/routes.go`） |
| **SEC-MW-4** | 攻击净化：对 URL、查询串、关键请求头与 JSON 请求体扫描 SQLi / XSS / 命令注入 / 路径穿越，命中即 `403` 并审计溯源（G-12） | `WAF`（`pkg/middleware/waf.go`） |
| **SEC-MW-5** | 身份级速率限制：32 分片令牌桶（默认 100 RPS / 200 Burst），限流 Key = `身份类型:身份名:归一化路径`，匿名追加客户端 IP 分片因子 | `RateLimitMiddleware`（`AGENT_RATE_LIMIT_*`） |
| **SEC-MW-6** | 对动态 ID 段路径做前缀归一化，防高基数路径导致桶爆炸 | `NormalizeRateLimitPath` |
| **SEC-MW-7** | 健康探针默认豁免限速 | `AGENT_HEALTH_NO_RATE_LIMIT`（默认 `true`） |
| **SEC-MW-8** | 受信任代理配置：仅对端 IP 属可信 CIDR 时才信任 `X-Forwarded-For` | `ConfigureTrustedProxies`（`AGENT_TRUSTED_PROXIES`） |

### 4.5 国密算法与隐私内核数据防护（SEC-CRYPTO）

| ID | 需求描述 | 实现方式 |
|---|---|---|
| **SEC-CRYPTO-1** | 脱敏/存证链路采用国密：SM3 不可逆掩码与 HMAC、SM4 可逆掩码与信封加密、SM2 TLCP 认证与密钥协商（GB/T 39786 三级） | `pkg/crypto` |
| **SEC-CRYPTO-2** | 信封加密：数据加密密钥（DEK）随机生成、主密钥（KEK）包裹，支持密钥分层与轮换 | `pkg/crypto/envelope.go` |
| **SEC-CRYPTO-3** | 敏感密钥 material 用后立即覆写清零，降低内存残留泄露风险 | `pkg/crypto/zeroize.go` |
| **SEC-CRYPTO-4** | 所有脱敏/DP/K-匿名原语为零状态纯函数，敏感中间值随栈析构，引擎不落盘任何原始明文 | 计算内核约定 |

### 4.6 差分隐私预算防护（SEC-DP）

| ID | 需求描述 | 实现方式 |
|---|---|---|
| **SEC-DP-1** | `/v1/privacy/dp/*`、`/v1/privacy/ldp/*` 在命名空间内对 ε 预算做原子累加记账 | `PRIVACY_NAMESPACE` + 预算账本 |
| **SEC-DP-2** | 预算耗尽即返回 `429 BUDGET_EXHAUSTED`，防同一租户无界消耗导致 DP 保证失效 | 原子预算比较 |
| **SEC-DP-3** | 聚合原语（`Aggregate`/`GroupBy`）显式值截断（clip）保障敏感度有界 | clip 参数 |

### 4.7 文件与医学影像输入防护（SEC-FILE）

| ID | 需求描述 | 实现方式 |
|---|---|---|
| **SEC-FILE-1** | 读取文件前 `filepath.Abs` + `EvalSymlinks` 解析真实路径，仅允许落在目录白名单内，符号链接逃逸被拒绝 | `internal/imageredact`（`AGENT_IMAGE_ALLOWED_DIRS`） |
| **SEC-FILE-2** | CSV/文件解析统一剥离 `\ufeff` UTF-8 BOM，防 BOM 污染字段名 | `pkg/fileparse` |
| **SEC-FILE-3** | XLSX（zip）解析限制条目/解压规模，防压缩炸弹 | Zip bomb 限制 |
| **SEC-FILE-4** | 文件上传 `process_file` 单文件 50MB，超限拒绝 | 载荷上限 |

### 4.8 可观测性与诊断端点权限（SEC-OBS）

| ID | 需求描述 | 实现方式 |
|---|---|---|
| **SEC-OBS-1** | 运行时端点按敏感度分级授权：探针 `health:read`（豁免）、`/ops/diagnostics` 需 `ops:diagnostics`、`?refresh=true` 需 `ops:admin`、`/metrics` fail-closed 归 `admin`、`/debug/pprof/*` 需 `ops:admin` | `PermissionForRESTPath` |
| **SEC-OBS-2** | 全链路 `log/slog` 结构化日志带 `trace_id`；认证失败/越权分别计入 `AuthFailuresTotal`/`AuthForbiddenTotal` | `pkg/observability` |
| **SEC-OBS-3** | 熔断器保护外部 LLM 兜底层（三层漏斗第 3 层），LLM 不可达时降级不外呼，避免级联故障 | 三态熔断器 |
| **SEC-OBS-4** | pprof 生产默认关闭，开启需运维管理员权限 | `AGENT_PPROF_ENABLED`（默认 `false`） |

---

## 5. 非功能需求

| 维度 | 要求 |
|---|---|
| **向后兼容** | 所有安全开关默认关闭或 fail-closed；现有本地启动命令与测试集无需修改即可通过。 |
| **性能** | 认证 + Scope 校验 + 限流处理耗时 < 1ms/P99（内存模式）；WAF 规则匹配走预编译正则。 |
| **默认安全** | 未知路径/未知 CN/漏配权限一律归最高权限并拒绝，绝不默认放行。 |
| **可配置** | 全部行为通过 `AGENT_*` 环境变量配置，无需改动代码即可适配不同环境。 |
| **可观测** | 认证失败、越权、超速、413/503 拦截、权限审计告警均打印结构化日志并计入指标。 |
| **可测试** | 提供自签名证书/测试夹具；TLS/mTLS/Auth/RateLimit/WAF/权限审计均有单元与集成测试。 |

---

## 6. 验收标准与测试矩阵

- [ ] **认证**：`AGENT_AUTH_ENABLED=true` 时无/错凭证 REST 返回 `401`、gRPC 返回 `UNAUTHENTICATED`（`internal/security/auth_test.go`）。
- [ ] **Scope 越权**：外部 Key 调用 `medical:process` 等内部权限返回 `403`，`AuthForbiddenTotal` 计数增加。
- [ ] **KeyStore 热轮换**：修改 `AGENT_AUTH_KEYS_FILE` 后新 Key 即时生效、过期 Key 自动失效（G-14）。
- [ ] **权限三层防御**：
  - 运行时兜底——未映射路径归入 `admin`（`default` 分支单测）；
  - 启动期审计——`LogRoutePermissionAudit` 对落入兜底的路由输出 `WARN`；
  - **CI 门禁——`TestAllRoutesHaveExplicitPermission` 断言全部路由显式映射通过**（新增路由漏配即刻失败）。
- [ ] **TLS/TLCP**：开启 `AGENT_TLS_ENABLED` 后 REST/gRPC 仅接受加密连接；`AGENT_TLS_NATIONAL_CIPHER` 下 TLCP 双证书握手成功。
- [ ] **mTLS 白名单**：命中白名单 CN 获授权方法域，未知 CN fail-closed 拒绝；配置文件热重载生效。
- [ ] **远程暴露门禁**：绑定非回环地址且缺 Auth/TLS 时进程拒绝启动。
- [ ] **中间件链**：IP 准入外网拒绝、`MaxBodySize` 超限返回 `413`、WAF 命中 SQLi/XSS/注入/穿越返回 `403`、身份级限速超限返回 `429`。
- [ ] **国密内核**：SM3/SM4 掩码与信封加解密往返正确、密钥内存清零、DP 预算耗尽返回 `429 BUDGET_EXHAUSTED`。
- [ ] **文件/影像防护**：目录白名单外与符号链接逃逸读取被拒、BOM 剥离、Zip 炸弹限制、50MB 上限拦截。
- [ ] **诊断端点**：`/ops/diagnostics`、`/metrics`、`/debug/pprof/*` 按 Scope 分级拦截。
- [ ] **回归**：默认配置下全模块编译（`make build`）与全套测试（`make test`）100% 通过。

---

## 7. 合规对标映射（等保三级 / 密评三级）

| 合规控制点 | 对应标准 | 引擎需求条目 |
|---|---|---|
| 身份鉴别 | GB/T 22239-2019 8.1.2.1 | SEC-AUTH-1/2/3/6、SEC-TLS-3/4 |
| 访问控制（最小权限） | GB/T 22239-2019 8.1.2.2 | SEC-AUTH-6、SEC-PERM-1、SEC-OBS-1 |
| 安全审计 | GB/T 22239-2019 8.1.2.3 | SEC-OBS-2、SEC-MW-4（WAF 溯源） |
| 通信保密性/完整性 | GB/T 22239-2019 8.1.2.5 | SEC-TLS-1/2 |
| 入侵防范 | GB/T 22239-2019 8.1.2.6 | SEC-MW-1/3/4/5 |
| 密码应用合规（算法/密钥） | GB/T 39786-2021 三级 | SEC-CRYPTO-1/2/3、SEC-TLS-2 |
| 个人信息保护（DP 保证） | 《个保法》《数安法》 | SEC-DP-1/2/3、SEC-CRYPTO-4 |
