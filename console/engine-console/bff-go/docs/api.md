# Go gRPC 代理后端 HTTP API 文档

本后端为前端测试控制台提供统一的 HTTP/JSON 接口，所有 gRPC 操作都通过 `/v1/proxy` 转发。

默认监听地址：`http://127.0.0.1:8081`

---

## 1. GET /health

检查 Go 代理本身以及上游 agent 的健康状态。

### 请求

```bash
curl -s http://127.0.0.1:8081/health | jq
```

### 成功响应（HTTP 200）

```json
{
  "backend": "ok",
  "agent": {
    "status": "ok",
    "namespace": "default"
  },
  "agent_url": "127.0.0.1:50051",
  "latency_ms": 5
}
```

### 字段说明

| 字段 | 类型 | 说明 |
|---|---|---|
| `backend` | string | Go 代理自身状态，固定为 `ok` |
| `agent` | object / string | agent 健康信息；若 agent 不可达则为 `"unreachable"` |
| `agent_url` | string | 当前连接的上游 agent 地址 |
| `latency_ms` | number | 调用 agent health 的耗时（毫秒） |
| `error` | string | agent 不可达时的错误信息 |

---

## 2. GET /v1/samples

返回所有 gRPC 支持的端点示例，供前端加载到测试控制台中。

### 请求

```bash
curl -s http://127.0.0.1:8081/v1/samples | jq
```

### 响应示例

```json
{
  "samples": [
    {
      "method": "POST",
      "path": "/v1/privacy/health",
      "label": "Health",
      "category": "Health",
      "description": "gRPC 健康检查",
      "body": {},
      "backend": "grpc"
    },
    {
      "method": "POST",
      "path": "/v1/privacy/mask",
      "label": "Mask",
      "category": "Masking",
      "description": "单字段脱敏",
      "body": {
        "field_name": "email",
        "value": "alice@example.com"
      },
      "backend": "grpc"
    }
  ]
}
```

### 字段说明

| 字段 | 类型 | 说明 |
|---|---|---|
| `method` | string | 前端使用的 HTTP 方法 |
| `path` | string | 转发到 `/v1/proxy` 时使用的 path |
| `label` | string | 前端展示名称 |
| `category` | string | 分类（Masking、DP、K-Anonymity 等） |
| `description` | string | 说明 |
| `body` | object | 转发时的 JSON 请求体 |
| `backend` | string | `grpc` 表示仅 Go 后端支持 |

---

## 3. POST /v1/proxy

统一代理入口，将 JSON 请求转换为 gRPC 调用。

### 请求体

```json
{
  "method": "POST",
  "path": "/v1/privacy/mask",
  "body": {
    "field_name": "email",
    "value": "alice@example.com"
  }
}
```

### 字段说明

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `method` | string | 是 | 原始 HTTP 方法，当前仅用于前端记录 |
| `path` | string | 是 | 目标 gRPC 路径，如 `/v1/privacy/mask` |
| `body` | object | 否 | 对应 gRPC 请求的 JSON 表示 |

### 成功响应（HTTP 200）

```json
{
  "status": 200,
  "duration_ms": 12,
  "data": {
    "result": "***@example.com"
  }
}
```

### 字段说明

| 字段 | 类型 | 说明 |
|---|---|---|
| `status` | number | HTTP 状态码，固定为 200 |
| `duration_ms` | number | gRPC 调用耗时（毫秒） |
| `data` | any | gRPC 响应转换后的 JSON |

### 错误响应

#### 请求参数错误（HTTP 400）

```json
{
  "detail": "invalid JSON body: ..."
}
```

#### 上游 agent 不可达（HTTP 502）

```json
{
  "detail": "connection refused",
  "status": 502
}
```

---

## 4. 常用 cURL 示例

### 4.1 单字段脱敏

```bash
curl -s -X POST http://127.0.0.1:8081/v1/proxy \
  -H "Content-Type: application/json" \
  -d '{
    "method": "POST",
    "path": "/v1/privacy/mask",
    "body": {
      "field_name": "email",
      "value": "alice@example.com"
    }
  }' | jq
```

### 4.2 差分隐私计数

```bash
curl -s -X POST http://127.0.0.1:8081/v1/proxy \
  -H "Content-Type: application/json" \
  -d '{
    "method": "POST",
    "path": "/v1/privacy/dp/count",
    "body": {
      "values": [1.0, 2.0, 3.0, 4.0, 5.0],
      "epsilon": 0.1,
      "mechanism": "laplace"
    }
  }' | jq
```

### 4.3 K-匿名单条记录

```bash
curl -s -X POST http://127.0.0.1:8081/v1/proxy \
  -H "Content-Type: application/json" \
  -d '{
    "method": "POST",
    "path": "/v1/privacy/k_anonymize/record",
    "body": {
      "record": {"age": "30", "zip": "100000", "gender": "F"},
      "qi_cols": ["age", "zip", "gender"],
      "k": 2
    }
  }' | jq
```

### 4.4 查询混淆

```bash
curl -s -X POST http://127.0.0.1:8081/v1/proxy \
  -H "Content-Type: application/json" \
  -d '{
    "method": "POST",
    "path": "/v1/privacy/qol/obfuscate",
    "body": {
      "query": "糖尿病患者用药推荐",
      "num_dummies": 3,
      "domain": "medical"
    }
  }' | jq
```

### 4.5 字段分类

```bash
curl -s -X POST http://127.0.0.1:8081/v1/proxy \
  -H "Content-Type: application/json" \
  -d '{
    "method": "POST",
    "path": "/v1/privacy/classify/field",
    "body": {
      "field_name": "email",
      "value": "alice@example.com",
      "params_json": "{}"
    }
  }' | jq
```

---

## 5. 环境变量

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `PRIVACY_AGENT_GRPC_HOST` | `127.0.0.1` | 上游 agent gRPC 主机 |
| `PRIVACY_AGENT_GRPC_PORT` | `50051` | 上游 agent gRPC 端口 |
| `PRIVACY_AGENT_API_KEY` | `""` | 可选的认证 API Key |
| `PRIVACY_CONSOLE_HOST` | `127.0.0.1` | 本服务监听地址 |
| `PRIVACY_CONSOLE_PORT` | `8081` | 本服务监听端口 |
| `PRIVACY_CONSOLE_STATIC_DIR` | `../web/dist` | 前端构建产物目录，存在时提供 Console UI；设为空字符串可禁用 |

---

## 6. 状态码约定

| HTTP 状态码 | 含义 |
|---|---|
| 200 | 成功 |
| 400 | 请求体非法或 gRPC 调用参数错误 |
| 502 | 上游 agent 不可达 |
| 404 | 请求了未映射的 path（由 `/v1/proxy` 返回 `unsupported gRPC path`） |
