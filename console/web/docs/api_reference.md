# 测试控制台前端（Web）API 参考

本文档描述前端（`console/web`）的**数据契约**（TypeScript 类型定义）与其所依赖的后端 `/v1/*` 接口约定。

- 契约定义于 `console/web/src/types/api.ts`，与后端 Pydantic 模型一一对应；
- 前端通过 `console/web/src/v1/client.ts` 调用后端，基址由 `setBaseUrl()` 控制（默认同源）；
- 后端接口的完整说明见 [console/bff-go/docs/api_reference.md](../../bff-go/docs/api_reference.md)。

## 1. 后端接口约定（前端视角）

| 方法 | 路径 | 前端调用方 | 用途 |
|---|---|---|---|
| GET | `/health` | `fetchHealth()` | 顶栏状态灯、cURL 基址推断 |
| GET | `/v1/samples` | `fetchSamples()` | 渲染侧边栏 / 总览 / 批量测试 |
| POST | `/v1/proxy` | `proxyRequest()` | 单端点测试发送请求 |
| POST | `/v1/batch` | `batchRequest()` | 批量测试 |

后端返回非 2xx 时，`client.ts` 抛出携带 `detail` 的 `Error`，由组件捕获并展示。

## 2. 数据类型定义

### 2.1 EndpointSample（端点示例）

来自 `GET /v1/samples`（`{ samples: [...] }`）。**camelCase** 命名。

| 字段 | 类型 | 说明 |
|---|---|---|
| `method` | string | HTTP 方法 |
| `path` | string | 端点路径 |
| `label` | string | UI 展示的简短名称 |
| `category` | string | 功能分类（侧边栏分组依据） |
| `description` | string | 中文功能描述 |
| `body` | object \| null | 默认 JSON 请求体 |
| `contentType` | string \| null | 二进制载荷的 Content-Type |
| `rawPayloadB64` | string \| null | 二进制载荷的 base64 编码 |
| `backend` | `"rest"` \| `"grpc"` \| `"both"` | 可用性标识 |

### 2.2 ProxyRequest / ProxyResponse（通用代理）

发往 `POST /v1/proxy`。**snake_case** 命名（与后端一致）。

**ProxyRequest**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `method` | string | 目标 HTTP 方法 |
| `path` | string | 目标路径 |
| `body` | object \| null | JSON 请求体 |
| `raw_payload_b64` | string \| null | 二进制载荷 base64 |
| `content_type` | string \| null | 二进制载荷 Content-Type |

**ProxyResponse**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `status` | number | 逻辑状态码（成功 200） |
| `duration_ms` | number | 转发耗时（毫秒） |
| `data` | any | agent 返回的原始数据 |

### 2.3 ConsoleHealth（健康检查）

来自 `GET /health`。

| 字段 | 类型 | 说明 |
|---|---|---|
| `backend` | string | 后端自身状态（`"ok"`） |
| `agent` | string \| object | agent 健康信息；不可达时为 `"unreachable"` |
| `agent_url` | string | agent REST 地址（用于 cURL 基址推断） |
| `latency_ms` | number? | 探测延迟（仅可达时） |
| `error` | string? | 错误描述（仅不可达时） |

### 2.4 批量测试类型

**BatchRequestItem**（请求项）：`method` / `path` / `body?`。

**BatchResultItem**（结果项）：`method` / `path` / `status` / `duration_ms` / `data?` / `error?`。

**BatchResponse**（汇总）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `total` | number | 子请求总数 |
| `passed` | number | 2xx 数量 |
| `failed` | number | 非 2xx 数量 |
| `results` | `BatchResultItem[]` | 逐条结果 |

### 2.5 HistoryEntry（请求历史）

仅存于浏览器 `localStorage`（键 `privacy-console.history`），不与后端交互。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 唯一标识（时间戳 + 随机串） |
| `method` | string | HTTP 方法 |
| `path` | string | 端点路径 |
| `body` | string | 请求体 JSON 文本 |
| `status` | number | 响应状态码（0 表示网络错误） |
| `timestamp` | number | 记录时间（毫秒时间戳） |

## 3. 命名约定

契约中存在两套命名风格，**均为有意设计**，与后端保持一致：

| 场景 | 命名风格 | 示例 |
|---|---|---|
| 示例数据（`/v1/samples`） | camelCase | `contentType` / `rawPayloadB64` |
| 代理转发（`/v1/proxy` / `/v1/batch`） | snake_case | `raw_payload_b64` / `duration_ms` |

修改任何接口字段时，必须**同步更新** `types/api.ts` 与后端 Pydantic 模型，否则类型检查或运行时会出错。

## 4. 前端内部 API

### 4.1 `api/client.ts`

| 函数 | 说明 |
|---|---|
| `setBaseUrl(baseUrl)` | 切换后端基址（去尾部斜杠）；空串表示同源 |
| `fetchHealth()` | `GET /health` |
| `fetchSamples()` | `GET /v1/samples`，返回 `samples` 数组 |
| `proxyRequest(req)` | `POST /v1/proxy`，非 2xx 抛 Error |
| `batchRequest(requests)` | `POST /v1/batch`，非 2xx 抛 Error |

### 4.2 `lib/curl.ts`

| 函数 | 说明 |
|---|---|
| `buildCurl({method, path, body, baseUrl})` | 生成可执行的 cURL 命令（shell 单引号转义） |
| `deriveAgentBaseUrl(agentUrl?)` | 从健康信息推断 agent REST 基址；gRPC 地址回退默认值 |

### 4.3 `lib/history.ts`

| 函数 | 说明 |
|---|---|
| `loadHistory()` | 读取历史（损坏时返回空数组） |
| `addHistory(entry)` | 新增并置顶，截断到 50 条 |
| `removeHistory(id)` | 按 id 删除 |
| `clearHistory()` | 清空 |
| `formatRelativeTime(ts)` | 相对时间展示（刚刚 / N 分钟前 / ...） |

### 4.4 `lib/categories.ts`

| 符号 | 说明 |
|---|---|
| `CATEGORY_ORDER` | 分类展示顺序 |
| `CATEGORY_META` | 分类元数据（图标 / 配色 / 描述） |
| `FALLBACK_META` | 未知分类的兜底配置 |
| `categoryMeta(name)` | 获取分类元数据（带兜底） |
| `orderCategories(present)` | 按预定义顺序排列，未知分类追加在后 |
