# 测试控制台后端（Python）产品需求文档（PRD）— 历史归档

> 历史说明：`console/bff-py` 已删除，当前统一由 `console/bff-go`（端口 8081）提供 REST/gRPC 上游代理。本文档保留为 Python 后端历史 PRD，不再描述当前实现。

## 1. 概述

Privacy 测试控制台后端是 `PrivShield` 的配套**测试与演示工具**。它以浏览器友好的方式，把 agent 的全部 REST 能力暴露为一个可视化控制台：用户无需手写 curl，即可加载示例、编辑请求、发送并查看结果。

Python 后端在其中的定位是「**代理层 + 静态服务器**」：

- 对浏览器：提供控制台 SPA 页面与统一的 `/v1/*` 接口；
- 对 agent：作为 HTTP 客户端转发请求，屏蔽跨域、二进制载荷与错误格式差异。

它**不实现任何隐私算法**，所有计算均由 agent 完成。

## 2. 设计目标

- **开箱即用**：零配置即可本地运行，自动连接默认地址的 agent。
- **全能力覆盖**：示例数据覆盖 agent 的全部 REST 端点（脱敏、DP、K-匿名、分类等）。
- **统一契约**：代理响应统一为 `{status, duration_ms, data}`，降低前端解析成本。
- **透明转发**：错误状态码与描述透传，保证调试信息与直连 agent 一致。
- **二进制支持**：支持 base64 请求载荷与 Arrow IPC 响应的 JSON 化展示。
- **轻量无状态**：不持有数据，可任意重启 / 扩展。

## 3. 用户场景

| 场景 | 角色 | 描述 |
|---|---|---|
| 功能验证 | 开发 / QA | 启动 agent 后，通过控制台逐端点发送示例请求，验证返回是否符合预期 |
| 接口调试 | 接入开发者 | 编辑请求体、查看响应与耗时，复制 cURL 命令到终端复现 |
| 回归测试 | QA | 使用批量测试一键跑完某分类（或全部）接口，查看通过率 |
| 演示 | 产品 / 售前 | 向客户直观展示脱敏、差分隐私等能力的输入输出 |
| 连通性排查 | 运维 | 通过 `/health` 区分「后端故障」与「agent 不可达」 |

## 4. 功能需求

### 4.1 代理转发（CB-PROXY）

| ID | 需求 |
|---|---|
| CB-PROXY-1 | 提供 `POST /v1/proxy`，把 `{method, path, body}` 转发到 agent 并返回统一包装响应 |
| CB-PROXY-2 | 支持 `raw_payload_b64` + `content_type` 转发二进制请求载荷（如 Arrow IPC 输入） |
| CB-PROXY-3 | 记录并返回每次转发耗时 `duration_ms` |
| CB-PROXY-4 | agent 非 2xx 时透传状态码与 `detail`；网络错误返回 502 |
| CB-PROXY-5 | 所有请求体经 Pydantic v2 校验，非法输入返回 422 |

### 4.2 批量测试（CB-BATCH）

| ID | 需求 |
|---|---|
| CB-BATCH-1 | 提供 `POST /v1/batch`，顺序转发一组请求 |
| CB-BATCH-2 | 单个请求失败不中断批次，吸收异常并记入该条结果 |
| CB-BATCH-3 | 汇总返回 `total / passed / failed` 与逐条 `results` |

### 4.3 辅助接口（CB-AUX）

| ID | 需求 |
|---|---|
| CB-AUX-1 | 提供 `GET /health`，返回后端状态、agent 连通性与探测延迟 |
| CB-AUX-2 | agent 不可达时 `/health` 仍返回 200，`agent` 字段为 `"unreachable"` |
| CB-AUX-3 | 提供 `GET /v1/samples`，返回全部端点的示例数据（含分类、描述、默认请求体） |
| CB-AUX-4 | 示例数据标注 `backend`（`rest` / `both`），标识端点在双后端中的可用性 |

### 4.4 静态托管（CB-STATIC）

| ID | 需求 |
|---|---|
| CB-STATIC-1 | 挂载前端构建产物 `dist`，浏览器经本后端直接访问控制台 |
| CB-STATIC-2 | `/assets/*` 返回带哈希构建产物；其余路径回退 `index.html`（SPA 路由） |
| CB-STATIC-3 | `dist` 不存在时应用仍可启动并仅提供 API |

### 4.5 配置（CB-CONFIG）

| ID | 需求 |
|---|---|
| CB-CONFIG-1 | agent 地址、API Key、监听地址/端口、静态目录均可通过环境变量配置 |
| CB-CONFIG-2 | 所有配置项有默认值，本地开发零配置可运行 |
| CB-CONFIG-3 | 支持 `.env` 文件加载 |

## 5. 非功能需求

| 类别 | 需求 |
|---|---|
| 性能 | 复用 HTTP 连接池并在启动时预热；单次转发额外开销 < 5ms（本机） |
| 可用性 | 无状态，可随时重启；agent 故障不影响后端自身存活 |
| 安全 | Pydantic 校验全部输入；API Key 仅置于请求头；不持久化任何请求数据 |
| 可维护性 | 模块按职责拆分；代码含详细中文注释；契约与前端 `types/api.ts` 对应 |
| 兼容性 | Python 3.10+；与 Go 后端保持相同 `/v1/*` 契约 |

## 6. 验收标准

- [ ] `./run.sh` 启动后，`GET /health` 返回 `backend: "ok"`。
- [ ] agent 运行时，`POST /v1/proxy` 转发 `/v1/privacy/mask` 返回统一包装的正确结果。
- [ ] agent 停止时，`/health` 返回 200 且 `agent: "unreachable"`，`/v1/proxy` 返回 502。
- [ ] `POST /v1/batch` 对混合成功/失败的请求返回正确的 `total/passed/failed`。
- [ ] `GET /v1/samples` 返回的示例数量与 `get_samples()` 一致。
- [ ] 构建前端后，浏览器访问 `http://127.0.0.1:8081` 可打开控制台（当前由 Go BFF 托管；历史 Python 后端使用 8080）。
- [ ] `pytest tests -v` 全部通过。
