# 测试控制台后端（Go BFF）API 参考

本文档描述控制台 Go BFF（`console/bff-go`）对外提供的全部 REST 接口、请求/响应数据模型与环境变量配置。

- **默认基址**：`http://127.0.0.1:8081`
- **可选 gRPC 网关**：`127.0.0.1:50055`（由 `PRIVACY_CONSOLE_GRPC_ENABLED=true` 启用）
- **数据格式**：除静态资源与文件上传外，全部为 `application/json`
- **统一错误结构**：`{"error": "..."}`（HTTP 非 2xx）

## 1. 接口总览

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/health` | 检查后端自身与下游 agent 的连通性 |
| GET | `/v1/samples` | 返回所有端点的示例数据（按功能分类） |
| POST | `/v1/proxy` | 通用代理：把请求通过 gRPC 转发到 agent |
| POST | `/v1/batch` | 批量代理：顺序转发一组请求并汇总结果 |
| POST | `/v1/upload` | 文件上传 + 隐私处理（masking / K-anonymity / classification） |
| POST | `/v1/lb_test` | 网关负载均衡策略测试 |
| GET | `/assets/*` | 静态资源（前端构建产物） |
| GET | `/{full_path}` | SPA 回退：未命中路径返回 `index.html` |

---

## 2. GET /health

检查后端自身与下游 agent 的连通性。

**响应字段**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `status` | string | 后端自身状态，如 `"ok"` |
| `backend` | string | 后端身份，恒为 `"ok"` |
| `agent` | object \| string | agent `/health` 返回内容；不可达时为 `"unreachable"` |
| `agent_url` | string | 配置的 agent REST 地址 |
| `via` | string | 后端实现标识，如 `"go-grpc"` |
| `protocol` | string | 当前通信协议，`"REST"` 或 `"gRPC"` |
| `latency_ms` | float | 探测 agent 的往返耗时（仅可达时） |
| `error` | string | 错误描述（仅不可达时） |

**注意**：agent 不可达时仍返回 **HTTP 200**（而非 5xx），以便前端读取 `agent == "unreachable"` 并展示友好提示。

**示例**：

```bash
curl http://127.0.0.1:8081/health
```

```json
{
  "status": "ok",
  "backend": "ok",
  "agent": { "status": "ok" },
  "agent_url": "http://127.0.0.1:8079",
  "via": "go-grpc",
  "protocol": "gRPC",
  "latency_ms": 3.21
}
```

---

## 3. GET /v1/samples

返回所有可测试端点的示例数据。前端启动时调用该接口渲染侧边栏与总览。

**响应结构**：`{"samples": [EndpointSample, ...]}`

**`EndpointSample` 字段**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `method` | string | HTTP 方法（GET / POST） |
| `path` | string | 端点路径（如 `/v1/privacy/mask`） |
| `label` | string | UI 中显示的简短名称 |
| `category` | string | 功能分类（Masking / DP / ...） |
| `description` | string | 中文功能描述 |
| `body` | object \| null | 默认 JSON 请求体 |
| `contentType` | string \| null | 二进制载荷的 Content-Type |
| `rawPayloadB64` | string \| null | 二进制载荷的 base64 编码 |
| `backend` | string | 可用性标识：`"rest"` / `"grpc"` / `"both"` |

---

## 4. POST /v1/proxy

通用代理：把一个请求通过 **gRPC** 转发到 `PrivShield`。

**请求体（`ProxyRequest`）**：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `method` | string | 是 | 目标 HTTP 方法 |
| `path` | string | 是 | 目标路径（如 `/v1/privacy/mask`） |
| `body` | object \| null | 否 | JSON 请求体（与 `raw_payload_b64` 二选一） |
| `raw_payload_b64` | string \| null | 否 | 二进制载荷的 base64 编码 |
| `content_type` | string \| null | 否 | 二进制载荷的 Content-Type |

**响应体（`ProxyResponse`）**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `status` | int | 转发后的逻辑状态码（成功为 200） |
| `duration_ms` | float | 本次转发耗时（毫秒） |
| `data` | any | agent 返回的原始数据 |

**示例**：

```bash
curl -X POST http://127.0.0.1:8081/v1/proxy \
  -H 'Content-Type: application/json' \
  -H 'X-PrivShield-Protocol: gRPC' \
  -d '{
    "method": "POST",
    "path": "/v1/privacy/mask",
    "body": { "value": "13800138000", "field": "phone" }
  }'
```

```json
{
  "status": 200,
  "duration_ms": 2.35,
  "data": { "result": "138****8000" }
}
```

**错误**：

| 状态码 | 场景 |
|---|---|
| 400 | `raw_payload_b64` 解码失败 |
| 422 | 请求体校验失败 |
| 502 | agent 不可达 / 超时 |
| 其他 | 透传 agent 返回的状态码 |

---

## 5. POST /v1/batch

批量代理：顺序转发一组请求并汇总成功 / 失败统计。单个请求失败不中断整个批次。

**请求体（`BatchRequest`）**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `requests` | `BatchRequestItem[]` | 子请求列表（顺序执行） |

**`BatchRequestItem` 字段**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `method` | string | HTTP 方法（默认 POST） |
| `path` | string | 目标路径 |
| `body` | object \| null | JSON 请求体（批量不支持二进制载荷） |

**响应体（`BatchResponse`）**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `total` | int | 子请求总数 |
| `passed` | int | 状态码在 2xx 区间的数量 |
| `failed` | int | 其余数量（`total == passed + failed`） |
| `results` | `BatchResultItem[]` | 逐条结果 |

**`BatchResultItem` 字段**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `method` | string | HTTP 方法 |
| `path` | string | 目标路径 |
| `status` | int | 该请求的状态码 |
| `duration_ms` | float | 该请求耗时（毫秒） |
| `data` | any \| null | 成功时的 agent 返回数据 |
| `error` | string \| null | 失败时的错误描述 |

**示例**：

```bash
curl -X POST http://127.0.0.1:8081/v1/batch \
  -H 'Content-Type: application/json' \
  -d '{
    "requests": [
      { "method": "GET",  "path": "/health" },
      { "method": "POST", "path": "/v1/privacy/mask", "body": { "value": "a@b.com", "field": "email" } }
    ]
  }'
```

---

## 6. POST /v1/upload

文件上传隐私处理：支持 CSV / Excel / JSON / 图片 / DICOM 等格式，自动识别字段并完成 masking / K-anonymity / classification。

**请求**：`multipart/form-data`

| 字段 | 类型 | 说明 |
|---|---|---|
| `file` | File | 待处理文件 |
| `operations` | string (JSON) | 处理操作列表，如 `[{"type": "mask"}]` |

**响应**：处理后的文件下载或 JSON 结果。

---

## 7. 静态资源与 SPA 回退

| 路径 | 行为 |
|---|---|
| `/assets/*` | 返回 `web/dist/assets/` 下带哈希的 JS/CSS（强缓存友好） |
| `/{full_path}` | 未命中路径一律返回 `index.html`（前端路由接管）；`index.html` 不存在时返回 404 |

静态目录由 `PRIVACY_CONSOLE_STATIC_DIR` 指定，默认 `../web/dist`（相对 BFF 工作目录）。目录不存在时应用仍可提供 API。

---

## 8. 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `PRIVACY_AGENT_GRPC_HOST` | `127.0.0.1` | 下游 agent 的 gRPC 主机 |
| `PRIVACY_AGENT_GRPC_PORT` | `50051` | 下游 agent 的 gRPC 端口 |
| `PRIVACY_AGENT_API_KEY` | — | 可选认证 API Key（agent 开启 auth 时必填） |
| `PRIVACY_CONSOLE_HOST` | `127.0.0.1` | 控制台 BFF HTTP 监听地址 |
| `PRIVACY_CONSOLE_PORT` | `8081` | 控制台 BFF HTTP 监听端口 |
| `PRIVACY_CONSOLE_STATIC_DIR` | `../web/dist` | 前端构建产物目录 |
| `CONSOLE_API_KEY` | — | BFF 自身可选 API Key |
| `CONSOLE_RATE_LIMIT` | `600` | 每分钟每 IP 最大请求数（0 关闭） |
| `PRIVACY_CONSOLE_TLS_ENABLED` | `false` | 是否启用 HTTPS/mTLS |
| `PRIVACY_CONSOLE_TLS_CERT_FILE` | — | 服务端证书 |
| `PRIVACY_CONSOLE_TLS_KEY_FILE` | — | 服务端私钥 |
| `PRIVACY_CONSOLE_TLS_CA_FILE` | — | mTLS 客户端 CA |
| `PRIVACY_CONSOLE_TLS_CLIENT_AUTH` | — | 客户端认证模式：`require` / `verify` / `request` |
| `PRIVACY_CONSOLE_GRPC_ENABLED` | `false` | 是否启动 BFF 自身 gRPC 网关 |
| `PRIVACY_CONSOLE_GRPC_PORT` | `50055` | BFF gRPC 网关端口 |

配置通过 `console/bff-go/internal/config` 从环境变量加载，所有项均有默认值，本地开发零配置即可运行。
