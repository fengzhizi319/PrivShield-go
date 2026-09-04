# 分类分级与隐私脱敏核心引擎 (privacy-engine) — 配置字典

> **组件归属**：`services/privacy-engine`

---

## 1. privshield-agent 核心环境变量

| 变量名 | 默认值 | 必填 | 作用说明 |
|---|---|:---:|---|
| `AGENT_REST_HOST` | `0.0.0.0` | 否 | REST 监听 IP 地址 |
| `AGENT_REST_PORT` | `8079` | 否 | REST HTTP 监听端口 |
| `AGENT_GRPC_HOST` | `0.0.0.0` | 否 | gRPC 监听 IP 地址 |
| `AGENT_GRPC_PORT` | `50051` | 否 | gRPC 监听端口 |
| `AGENT_RULES_DIR` | `services/privacy-engine/rules/domains` | 否 | 领域分类规则目录路径 |
| `AGENT_STANDARDS_DIR` | `services/privacy-engine/rules/standards` | 否 | 分类分级映射标准目录路径 |
| `AGENT_CONFIG_FILE` | `config/privacy.yaml` | 否 | 隐私安全策略配置文件 |
| `AGENT_AUTH_ENABLED` | `false` | 否 | 是否启用 API Key 认证（生产环境必置 `true`） |
| `AGENT_AUTH_API_KEY` | — | 条件 | 管理员 API Key（开启认证时必填，空值阻断启动） |
| `AGENT_RATE_LIMIT_ENABLED` | `false` | 否 | 是否启用 32 分片并发令牌桶限流 |
| `AGENT_TLS_ENABLED` | `false` | 否 | 是否开启 REST 与 gRPC TLS 加密传输 |
| `AGENT_TLS_NATIONAL_CIPHER` | `false` | 否 | 是否启用 TLCP 国密双证书模式 (GM/T 0024) |
| `AGENT_PPROF_ENABLED` | `false` | 否 | 是否开启 pprof 性能分析端点（生产建议关闭） |

---

## 2. privshield-gateway 网关环境变量

| 变量名 | 默认值 | 必填 | 作用说明 |
|---|---|:---:|---|
| `ENGINE_GATEWAY_HTTP_PORT` | `8000` | 否 | 网关反向代理 HTTP 监听端口 |
| `ENGINE_GATEWAY_GRPC_PORT` | `50000` | 否 | 网关反向代理 gRPC 监听端口 |
| `ENGINE_GATEWAY_HTTP_BACKENDS`| `http://127.0.0.1:8079` | 否 | 上游 Agent HTTP 地址列表（逗号分隔） |
| `ENGINE_GATEWAY_GRPC_BACKENDS`| `127.0.0.1:50051` | 否 | 上游 Agent gRPC 地址列表（逗号分隔） |
| `ENGINE_GATEWAY_METRICS_API_KEY` | — | 否 | 网关 `/metrics` 访问保护 Bearer Token |
