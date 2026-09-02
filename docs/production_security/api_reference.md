# 生产安全加固 API 参考

> **版本**：v16.0.0  
> **适用范围**：`PrivShield` 核心算力引擎（`engine`）、企业级中台微服务群（`service-hub` / `datasource-mgr` / `audit-log`）、控制台与双 BFF 体系（`bff-go` / `app-lz`）。  
> **定位**：系统参考手册，提供环境变量、Python `engine/security` 模块、Go `pkg/tlsutil` / `pkg/middleware` / `pkg/crypto` 共享库与 REST/gRPC 安全接口定义。

---

## 目录

- [1. 全局环境变量矩阵](#1-全局环境变量矩阵)
  - [1.1 TLS / mTLS 传输加密](#11-tls--mtls-传输加密)
  - [1.2 认证鉴权与 mTLS 白名单](#12-认证鉴权与-mtls-白名单)
  - [1.3 速率限制与分布式后端](#13-速率限制与分布式后端)
  - [1.4 全栈防 DDoS 与快照加密](#14-全栈防-ddos-与快照加密)
  - [1.5 健康检查豁免](#15-健康检查豁免)
- [2. Python 安全 SDK (engine/security)](#2-python-安全-sdk-enginesecurity)
  - [2.1 SecuritySettings](#21-securitysettings)
  - [2.2 TLS 构造器 (security/tls.py)](#22-tls-构造器-securitytlspy)
  - [2.3 身份模型与 Scope 权限映射 (security/identity.py)](#23-身份模型与-scope-权限映射-securityidentitypy)
  - [2.4 认证与鉴权依赖 (security/auth.py)](#24-认证与鉴权依赖-securityauthpy)
  - [2.5 速率限制引擎 (security/ratelimit.py)](#25-速率限制引擎-securityratelimitpy)
  - [2.6 白名单管理器 (security/whitelist.py)](#26-白名单管理器-securitywhitelistpy)
- [3. Go 共享安全库 (pkg/)](#3-go-共享安全库-pkg)
  - [3.1 TLS 与 CN 白名单 (pkg/tlsutil)](#31-tls-与-cn-白名单-pkgtlsutil)
  - [3.2 9 层统一中间件栈 (pkg/middleware)](#32-9-层统一中间件栈-pkgmiddleware)
  - [3.3 SM4-GCM 快照信封加密 (pkg/crypto)](#33-sm4-gcm-快照信封加密-pkgcrypto)
- [4. REST 与 gRPC 协议行为及错误码汇总](#4-rest-与-grpc-协议行为及错误码汇总)

---

## 1. 全局环境变量矩阵

### 1.1 TLS / mTLS 传输加密

| 变量 | 默认值 | 必填 | 说明 |
|---|---|---|---|
| `PRIVACY_TLS_ENABLED` | `false` | 否 | 是否启用 REST/gRPC TLS。 |
| `PRIVACY_TLS_CERT_FILE` / `PRIVACY_TLS_CERT_PATH` | — | TLS 开启时必填 | 服务器证书 PEM 路径。 |
| `PRIVACY_TLS_KEY_FILE` / `PRIVACY_TLS_KEY_PATH` | — | TLS 开启时必填 | 服务器私钥 PEM 路径。 |
| `PRIVACY_TLS_CA_FILE` / `PRIVACY_TLS_CA_PATH` | — | `optional`/`require` 时必填 | CA 证书 PEM 路径，用于校验客户端证书。 |
| `PRIVACY_TLS_CLIENT_AUTH` | `none` | 否 | 客户端认证模式：`none` / `optional` / `require`。 |
| `PRIVACY_TLS_KEY_PASSWORD` | — | 否 | 加密私钥的口令。 |

### 1.2 认证鉴权与 mTLS 白名单

| 变量 | 默认值 | 必填 | 说明 |
|---|---|---|---|
| `PRIVACY_AUTH_ENABLED` | `false` | 否 | 是否启用 API Key 认证鉴权。 |
| `PRIVACY_AUTH_INTERNAL_KEYS_JSON` | `{}` | 否 | 内部服务 API Key 映射，JSON 对象。 |
| `PRIVACY_AUTH_EXTERNAL_KEYS_JSON` | `{}` | 否 | 外部服务 API Key 映射，JSON 对象。 |
| `PRIVACY_AUTH_INTERNAL_MTLS_ENABLED` | `false` | 否 | gRPC 是否将验证通过的 mTLS 客户端视为内部服务（默认关闭，fail-closed）。 |
| `PRIVACY_AUTH_MTLS_WHITELIST_FILE` | — | 否 | mTLS CN 白名单 YAML 配置文件路径。设置后启用 per-CN scope 控制与热重载。 |
| `PRIVACY_AUTH_MTLS_ALLOWED_CNS` | `[]` | 否 | mTLS 客户端证书 CN 静态白名单（JSON 数组或逗号分隔）。当 WHITELIST_FILE 未设置时使用，所有 CN 获得 `["*"]` 全权限。 |

**Scope-based API Key 格式**（`PRIVACY_AUTH_INTERNAL_API_KEYS` / `PRIVACY_AUTH_EXTERNAL_API_KEYS` / `SERVICE_HUB_API_KEYS`）：
```text
token1:name1:scope1,scope2;token2:name2:scope3
```
未指定 scope 时默认 `["*"]`（全权限）。

| 变量 | 默认值 | 必填 | 说明 |
|---|---|---|---|
| `SERVICE_HUB_API_KEY` | — | 否 | service-hub 单 Key 入站鉴权（向后兼容，无 Scope 粒度）。 |
| `SERVICE_HUB_API_KEYS` | — | 否 | service-hub Scope-based API Key 映射（优先于 `SERVICE_HUB_API_KEY`），格式同上。 |

JSON 格式示例：
```json
{
  "sk-internal-1": {
    "name": "service-hub",
    "scopes": ["*"]
  },
  "sk-external-1": {
    "name": "portal",
    "scopes": ["privacy:mask", "classification:read"]
  }
}
```

### 1.3 速率限制与分布式后端

| 变量 | 默认值 | 必填 | 说明 |
|---|---|---|---|
| `PRIVACY_RATE_LIMIT_ENABLED` | `false` | 否 | 是否启用限速。 |
| `PRIVACY_RATE_LIMIT_DEFAULT_RPS` | `10` | 否 | 默认每秒请求数。 |
| `PRIVACY_RATE_LIMIT_DEFAULT_BURST` | `20` | 否 | 默认突发容量。 |
| `PRIVACY_RATE_LIMIT_PER_ENDPOINT_JSON` | `{}` | 否 | 按接口覆盖限速规则。 |
| `PRIVACY_RATE_LIMIT_REDIS_URL` | — | 否 | 多副本时共享计数器，例 `redis://redis:6379/0`。 |

### 1.4 全栈防 DDoS 与快照加密

| 变量 | 默认值 | 必填 | 说明 |
|---|---|---|---|
| `AUDIT_LOG_ENCRYPTION_KEY` | — | 生产必填 | SM4-GCM 快照落盘信封加密密钥。 |
| `MAX_CONCURRENT_REQUESTS` | `1000` | 否 | Go 微服务在途并发信号量拦截上限。 |
| `MAX_BODY_SIZE_MB` | `32` / `64` | 否 | 请求体硬顶限制（微服务 32MB / BFF 64MB）。 |

### 1.5 健康检查豁免

| 变量 | 默认值 | 必填 | 说明 |
|---|---|---|---|
| `PRIVACY_HEALTH_NO_AUTH` | `true` | 否 | `/health` 与 `Health` RPC 是否免认证。 |
| `PRIVACY_HEALTH_NO_RATE_LIMIT` | `true` | 否 | `/health` 与 `Health` RPC 是否免限速。 |

---

## 2. Python 安全 SDK (`engine/security`)

### 2.1 `SecuritySettings`

位置：`engine.security.config.SecuritySettings`，Pydantic v2 集中配置模型。

```python
class SecuritySettings(BaseModel):
    tls_enabled: bool = False
    tls_cert_file: Path | None = None
    tls_key_file: Path | None = None
    tls_ca_file: Path | None = None
    tls_client_auth: Literal["none", "optional", "require"] = "none"
    tls_key_password: str | None = None

    auth_enabled: bool = False
    auth_internal_mtls_enabled: bool = False
    auth_mtls_allowed_cns: list[str] = Field(default_factory=list)
    auth_mtls_whitelist_file: Path | None = None
    internal_keys: dict[str, KeyConfig] = Field(default_factory=dict)
    external_keys: dict[str, KeyConfig] = Field(default_factory=dict)

    rate_limit_enabled: bool = False
    rate_limit_default_rps: float = 10.0
    rate_limit_default_burst: float = 20.0
    rate_limit_per_endpoint: dict[str, RateLimitConfig] = Field(default_factory=dict)
    rate_limit_redis_url: str | None = None

    health_no_auth: bool = True
    health_no_rate_limit: bool = True
```

### 2.2 TLS 构造器 (`security/tls.py`)

- **`uvicorn_ssl_kwargs(settings: SecuritySettings) -> dict[str, Any]`**：为 `uvicorn.run()` 生成 SSL 参数；
- **`grpc_server_credentials(settings: SecuritySettings) -> grpc.ServerCredentials`**：生成 gRPC 服务端 SSL 凭证。

### 2.3 身份模型与 Scope 权限映射 (`security/identity.py`)

```python
@dataclass(frozen=True)
class Identity:
    service_type: Literal["internal", "external"]
    name: str
    scopes: list[str]

    def has_permission(self, permission: str) -> bool: ...
```

#### Engine 权限映射（`PermissionForRESTPath`）

支持 `/v1/*` 与 `/api/v1/*` 双前缀归一化匹配，以及根路径直调别名。

| REST 路径 / gRPC 方法 | 对应权限 Scope |
|---|---|
| `/health`, `/livez`, `/readyz` / `Health` | `health:read` |
| `/v1/privacy/mask*`, `/api/v1/mask*`, `/privacy/process_file` / `Mask`, `MaskRecord` | `privacy:mask` |
| `/v1/privacy/hash`, `/api/v1/hash/hmac` / `Hash` | `privacy:hash` |
| `/v1/privacy/dp/*`, `/v1/privacy/ldp/*`, `/api/v1/dp/*`, `/api/v1/ldp/*` / `DPCount`, `DPSum`, `DPMean` | `privacy:dp` |
| `/v1/privacy/k_anonymize*`, `/api/v1/kano/*` / `KAnonymizeRecord` | `privacy:kano` |
| `/v1/privacy/qol/*`, `/api/v1/qol/*` / `ObfuscateQuery` | `privacy:qol` |
| `/v1/privacy/budget`, `/v1/privacy/budget/reset`, `/api/v1/budget*` | `privacy:budget` |
| `/v1/privacy/profile/recommend` | `privacy:profile` |
| `/v1/privacy/classify/*`, `/api/v1/classify*` | `classification:read` |
| `/v1/dynclassification/classify*`, `/v1/dynclassification/eval_record` | `dynclassification:read` |
| `/v1/dynclassification/profiles/reload`, `/v1/dynclassification/generate_profile` | `dynclassification:write` |
| `/v1/agent/process`, `/api/v1/agent/process`, `/agent/process` | `agent:process` |
| `/v1/medical/*`, `/api/v1/medical/*`, `/medical/process` | `medical:process` |
| `/v1/ops/*`, `/api/v1/ops/*`, `/ops/diagnostics` | `ops:diagnostics` |
| `/debug/pprof*` | `ops:admin` |

#### service-hub 权限映射（`ServiceHubPermissionForPath`）

| REST 路径 | 对应权限 Scope |
|---|---|
| `/api/hub/status`, `/api/hub/tasks`, `/api/hub/tasks/:id`, `/api/hub/pipeline` | `hub:read` |
| `/api/hub/dispatch`, `/api/hub/classify` | `hub:dispatch` |
| `/health`, `/readyz`, `/api/health`, `/metrics` | 无需特定权限（已认证即可） |

### 2.4 认证与鉴权依赖 (`security/auth.py`)

- `get_current_identity(request: Request) -> Identity`：FastAPI 依赖；
- `require_permission(permission: str) -> Depends`：接口级 Scope 校验依赖；
- `AuthInterceptor`：gRPC 拦截器，校验 metadata 与 mTLS auth_context。

### 2.5 速率限制引擎 (`security/ratelimit.py`)

- `Limiter`：基于滑动窗口的限流器；
- `rate_limit_dependency`：FastAPI 限流依赖；
- `RateLimitInterceptor`：gRPC 限流拦截器。

### 2.6 白名单管理器 (`security/whitelist.py`)

- `WhitelistManager`：支持热重载的线程安全 CN 白名单管理器；
- `get_whitelist_manager() -> WhitelistManager`：单例工厂方法。

---

## 3. Go 共享安全库 (`pkg/`)

### 3.1 TLS 与 CN 白名单 (`pkg/tlsutil`)

- `NewServerTLSConfig(certFile, keyFile, caFile, requireClientCert string) (*tls.Config, error)`：构造 TLS 1.3 服务端配置；
- `NewClientTLSConfig(certFile, keyFile, caFile, serverName string) (*tls.Config, error)`：构造客户端双向证书配置；
- `NewWhitelist(configFile string, allowedCNs []string) (*Whitelist, error)`：初始化 CN 白名单（支持 5 秒轮询热重载）；
- `UnaryServerInterceptor(wl *Whitelist)` / `StreamServerInterceptor(wl *Whitelist)`：gRPC 服务端白名单拦截器。

### 3.2 9 层统一中间件栈 (`pkg/middleware`)

1. `TraceMiddleware`：提取并透传 `X-Request-ID` 与 `X-Trace-ID`；
2. `StructuredLogger`：统一结构化 JSON 日志；
3. `Recovery`：Panic 拦截脱敏；
4. `SecurityHeaders`：CSP、HSTS、X-Frame-Options 等安全响应头；
5. `MaxBodySize`：32MB / 64MB 硬顶拦截（413）；
6. `MaxConcurrent`：在途并发容量硬顶（503）；
7. `RateLimit`：IP 令牌桶限流（429 + Retry-After）；
8. `CORS`：跨域白名单；
9. `Auth`：恒定时间 Bearer Token 鉴权（401 / 403）。

### 3.3 SM4-GCM 快照信封加密 (`pkg/crypto`)

- `Encrypt(plaintext []byte, key []byte) (string, error)`：输出 `enc:v1:<Base64(12B Nonce + Ciphertext + 16B Tag)>`；
- `Decrypt(encoded string, key []byte) ([]byte, error)`：透明还原快照密文；
- `IsEncrypted(data string) bool`：判断是否已包含加密前缀。

---

## 4. REST 与 gRPC 协议行为及错误码汇总

| 场景 | REST 响应状态 | gRPC 状态码 | 响应内容示例 |
|---|---|---|---|
| 未携带凭证 / 凭证无效 | `401 Unauthorized` | `UNAUTHENTICATED` | `{"code":"UNAUTHORIZED","message":"missing or invalid credentials"}` |
| 权限不足 / Scope 越权 | `403 Forbidden` | `PERMISSION_DENIED` | `{"code":"FORBIDDEN","message":"insufficient scope"}` |
| 请求速率超限 | `429 Too Many Requests`| `RESOURCE_EXHAUSTED` | `{"code":"RATE_LIMIT_EXCEEDED","message":"too many requests"}` |
| 并发容量超载 | `503 Service Unavailable`| `UNAVAILABLE` | `{"code":"SERVICE_UNAVAILABLE","message":"concurrency limit reached"}` |
| 请求体超限 | `413 Payload Too Large`| `INVALID_ARGUMENT` | `{"code":"PAYLOAD_TOO_LARGE","message":"request body exceeds limit"}` |
| 非法数据源标识 | `400 Bad Request` | `INVALID_ARGUMENT` | `{"code":"INVALID_DATASOURCE_ID","message":"unregistered datasource"}` |
| TLS 握手失败 | TCP 连接切断 | `UNAVAILABLE` | — |