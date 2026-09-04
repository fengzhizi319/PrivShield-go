# 前端与后端学习指南 / Frontend & Backend Learning Guide

> 面向新入门开发者的完整学习路径，从零理解本项目的全栈架构。
> A complete learning path for new developers, understanding the full-stack architecture from scratch.

---

## 目录 / Table of Contents

1. [项目全景：你在学什么](#1-项目全景你在学什么)
2. [学习路径总览](#2-学习路径总览)
3. [第一阶段：Web 基础](#3-第一阶段web-基础)
4. [第二阶段：前端核心](#4-第二阶段前端核心)
5. [第三阶段：后端核心](#5-第三阶段后端核心)
6. [第四阶段：Agent 与隐私原语](#6-第四阶段agent-与隐私原语)
7. [第五阶段：工程化与部署](#7-第五阶段工程化与部署)
8. [本项目代码导读](#8-本项目代码导读)
9. [推荐资源](#9-推荐资源)
10. [常见问题 FAQ](#10-常见问题-faq)

---

## 1. 项目全景：你在学什么

本项目 `PrivShield` 是一个**数据安全与隐私治理 Sidecar 及控制台微服务集群**，整体架构分为三大层级：

```
┌─────────────────────────────────────────────────────────────────┐
│                        浏览器（前端）                             │
│   React + TypeScript + Vite + Tailwind CSS                      │
│   位置：console/web/ (:5173)                                    │
└────────────────────────────┬────────────────────────────────────┘
                             │ HTTP (JSON)
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                     控制台微服务与代理网关集群                      │
│                                                                  │
│   • 统一代理网关：                                                │
│     - Go BFF：REST 入口 + gRPC 上游 (console/bff-go/ :8081)        │
│                                                                  │
│   • 专项治理微服务 (Go/Gin + SQLite 持久化)：                     │
│     - 调度中枢：services/service-hub/ (:8082 / :50052 gRPC)       │
│     - 数据源管理：services/datasource-mgr/ (:8083)                 │
│     - 脱敏审计日志：services/audit-log/ (:8084)                    │
│                                                                  │
│   • 共享基础库：仓库根 `pkg/` (存储/中间件/指标/客户端)      │
└────────────────────────────┬────────────────────────────────────┘
                             │ REST 或 gRPC
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                  PrivShield（核心隐私计算引擎）                   │
│   Python FastAPI + gRPC + 隐私算法                                │
│   位置：engine/ (:8079 REST / :50051 gRPC)                  │
│                                                                  │
│   能力：脱敏 / 差分隐私 / K-匿名 / 查询混淆 / 动态三层分类分级       │
└─────────────────────────────────────────────────────────────────┘
```

**你需要掌握的技术栈：**

| 层级 | 技术 | 作用 |
|------|------|------|
| 前端 | React 18 + TypeScript | 构建交互式 UI 与治理流水线面板 |
| 前端 | Vite | 开发服务器 + 生产构建工具 |
| 前端 | Tailwind CSS | 原子化 CSS 样式 |
| 前端 | Vitest + Testing Library | 前端单元测试 |
| 代理网关 | Go + Gin + Protobuf | 高性能 REST/gRPC 代理网关与静态托管 |
| 调度中枢 | Go + Gin + gRPC (mTLS) | 6 阶段流水线编排调度 |
| 数据源管理 | Go + Gin + SQLite | 多源异构数据源纳管与元数据分类 |
| 脱敏审计日志 | Go + Gin + SHA-256 | 8 要素防篡改存证与合规报告 |
| 共享基础库 | Go Package (pkg/) | SQLite/Memory 存储、中间件、指标 |
| 核心 Agent | Python + FastAPI + gRPC | 隐私原语与三层分类漏斗执行引擎 |

---

## 2. 学习路径总览

```
第一阶段（1-2 周）     第二阶段（2-3 周）      第三阶段（2-3 周）
┌──────────────┐    ┌──────────────┐     ┌──────────────┐
│  Web 基础     │───▶│  前端核心     │────▶│  后端核心     │
│  HTML/CSS/JS │    │  React + TS  │     │  Go / Python │
└──────────────┘    └──────────────┘     └──────────────┘
                                                │
                    ┌──────────────┐            │
                    │  工程化部署   │◀───────────┤
                    │  Docker/K8s  │            ▼
                    └──────────────┘     ┌──────────────┐
                                         │  Agent 层    │
                                         │  隐私算法    │
                                         └──────────────┘
```

**建议节奏：**
- 每天 2-3 小时，约 8-12 周可完成全部阶段
- 每个阶段结束后，尝试在本项目中找到对应代码并阅读理解
- 不要跳过第一阶段，即使你觉得"已经会了"

---

## 3. 第一阶段：Web 基础

### 3.1 HTML — 网页的骨架

**学什么：**
- 标签语义：`<div>`, `<span>`, `<button>`, `<input>`, `<table>`, `<nav>`, `<aside>`
- 表单元素：`<form>`, `<select>`, `<textarea>`, `<label>`
- 文档结构：`<html>`, `<head>`, `<body>`, `<script>`

**在本项目中的体现：**
打开 `console/web/index.html`，这是整个前端应用的 HTML 入口：
```html
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Privacy Console</title>
  </head>
  <body>
    <div id="root"></div>           <!-- React 挂载点 -->
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

**练习：** 用纯 HTML 写一个包含输入框和按钮的表单。

### 3.2 CSS — 网页的皮肤

**学什么：**
- 盒模型：margin / border / padding / content
- 布局：Flexbox（重点！）和 Grid
- 选择器：类选择器 `.foo`、后代选择器 `.a .b`
- 响应式：`@media` 查询

**在本项目中的体现：**
本项目使用 Tailwind CSS（原子化 CSS），不写传统 `.css` 文件，而是在 HTML 类名中直接写样式：
```tsx
// 传统 CSS 写法：
// .card { display: flex; padding: 16px; border-radius: 8px; }

// Tailwind 写法（本项目风格）：
<div className="flex p-4 rounded-lg border border-gray-200 shadow-sm">
```

**关键 Tailwind 类名速查：**

| 类名 | 含义 |
|------|------|
| `flex` | display: flex |
| `items-center` | align-items: center |
| `justify-between` | justify-content: space-between |
| `gap-2` | gap: 0.5rem |
| `p-4` | padding: 1rem |
| `rounded-lg` | border-radius: 0.5rem |
| `text-sm` | font-size: 0.875rem |
| `text-gray-500` | color: #6b7280 |
| `bg-white` | background: white |
| `border-b` | border-bottom |
| `w-full` | width: 100% |
| `h-full` | height: 100% |
| `overflow-y-auto` | overflow-y: auto |
| `truncate` | 文本溢出截断 |

**练习：** 用 Flexbox 实现一个水平导航栏（logo 在左，链接在右）。

### 3.3 JavaScript — 网页的大脑

**学什么：**
- 变量：`let`, `const`（不用 `var`）
- 函数：箭头函数 `const fn = (x) => x * 2`
- 数组方法：`.map()`, `.filter()`, `.reduce()`, `.find()`
- 对象解构：`const { name, age } = person`
- 异步：`Promise`, `async/await`
- 模块：`import` / `export`
- JSON：`JSON.parse()`, `JSON.stringify()`

**在本项目中的体现：**
```typescript
// console/web/src/api/client.ts 中的异步请求
async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, { ...rest, signal: controller.signal });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ detail: res.statusText }));
    throw new Error(typeof err.detail === 'string' ? err.detail : JSON.stringify(err));
  }
  const text = await res.text();
  return JSON.parse(text) as T;
}
```

**练习：** 写一个 `async` 函数，用 `fetch` 请求 `https://jsonplaceholder.typicode.com/posts/1` 并打印标题。

### 3.4 TypeScript — 带类型的 JavaScript

**学什么：**
- 基本类型：`string`, `number`, `boolean`, `null`, `undefined`
- 接口：`interface User { name: string; age: number }`
- 泛型：`function first<T>(arr: T[]): T | undefined`
- 联合类型：`string | null`
- 类型导入：`import type { Foo } from './types'`

**在本项目中的体现：**
```typescript
// console/web/src/types/api.ts — 前后端数据契约
export interface ProxyResponse {
  status: number;
  duration_ms: number;
  data: any;
  via?: string;       // ? 表示可选字段
  protocol?: string;
}
```

**为什么用 TypeScript？**
- 编译期发现拼写错误（如 `respnse.status`）
- IDE 自动补全更准确
- 重构时能一次性找到所有受影响的地方
- 前后端共享类型定义，减少"字段名写错"的 bug

**练习：** 给上一阶段的 fetch 函数加上类型注解。

---

## 4. 第二阶段：前端核心

### 4.1 React 基础概念

**核心心智模型：**
```
UI = f(state)
```
界面是状态的函数。状态变了，界面自动更新。你不需要手动操作 DOM。

**学什么（按顺序）：**

1. **组件 = 函数**
```tsx
function Hello({ name }: { name: string }) {
  return <h1>Hello, {name}!</h1>;
}
```

2. **useState — 组件记忆**
```tsx
const [count, setCount] = useState(0);
// count 是当前值，setCount 是更新函数
// 调用 setCount(1) 后，组件重新渲染，count 变为 1
```

3. **useEffect — 副作用**
```tsx
useEffect(() => {
  // 组件挂载后执行（如发请求）
  fetchData();
  return () => {
    // 组件卸载时清理（如取消订阅）
  };
}, []); // 空数组 = 只执行一次
```

4. **props — 父传子**
```tsx
// 父组件
<Card title="hello" />

// 子组件
function Card({ title }: { title: string }) {
  return <div>{title}</div>;
}
```

5. **条件渲染 & 列表渲染**
```tsx
{loading ? <Spinner /> : <Data data={result} />}

{items.map((item) => <li key={item.id}>{item.name}</li>)}
```

**在本项目中阅读：**
- 从 `console/web/src/App.tsx` 开始，理解整体布局
- 然后看 `Sidebar.tsx`，理解 props 传递和状态管理
- 再看 `EndpointView.tsx`，理解表单交互和异步请求

### 4.2 React 进阶 Hooks

| Hook | 用途 | 本项目示例 |
|------|------|-----------|
| `useState` | 局部状态 | 所有组件 |
| `useEffect` | 副作用（请求/订阅） | `EndpointView` 键盘监听 |
| `useMemo` | 缓存计算结果 | `Sidebar` 分组过滤 |
| `useCallback` | 缓存函数引用 | `useAsyncAction` hook |
| `useRef` | 持久化引用（不触发渲染） | `EndpointView` sendRef |
| `useContext` | 跨层级共享数据 | `useI18n` 国际化 |

**自定义 Hook 模式（本项目重点）：**
```typescript
// console/web/src/hooks/useAsyncAction.ts
// 把"加载中/成功/失败"三态封装为可复用的 Hook
export function useAsyncAction<T>(): AsyncAction<T> {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = useCallback(async (fn: () => Promise<T>, fallbackError = 'Operation failed') => {
    setLoading(true);
    setError(null);
    setData(null);
    try {
      setData(await fn());
    } catch (e) {
      setError(getErrorMessage(e, fallbackError));
    } finally {
      setLoading(false);
    }
  }, []);

  return { data, loading, error, run, reset };
}
```

### 4.3 Vite — 构建工具

**它解决什么问题？**
- 开发时：秒级热更新（改代码 → 浏览器立即刷新）
- 生产时：打包、压缩、tree-shaking、代码分割

**关键命令：**
```bash
cd console/web
corepack pnpm install    # 安装依赖
corepack pnpm dev        # 启动开发服务器（http://localhost:5173）
corepack pnpm build      # 生产构建 → dist/
corepack pnpm preview    # 预览生产构建
```

**配置文件 `vite.config.ts` 要点：**
```typescript
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': '/src' },  // @ 代表 src 目录
  },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8081',  // 开发时代理到 Go BFF
    },
  },
});
```

### 4.4 Tailwind CSS — 原子化样式

**核心理念：** 不写 `.css` 文件，用预定义的工具类组合出样式。

**学习策略：**
1. 先记住 20 个最常用的类（见 3.2 节表格）
2. 遇到不认识的类，去 https://tailwindcss.com/docs 搜索
3. 理解响应式前缀：`sm:`, `md:`, `lg:` 对应不同断点
4. 理解状态前缀：`hover:`, `focus:`, `disabled:`

**本项目配置：** `console/web/tailwind.config.js`

### 4.5 测试 — Vitest + Testing Library

**为什么写测试？**
- 改代码后快速验证没有破坏已有功能
- 测试即文档：读测试就能理解组件行为

**运行测试：**
```bash
cd console/web
./node_modules/.bin/vitest run
```

**测试示例解读：**
```tsx
// console/web/src/components/__tests__/ActionButton.test.tsx
import { render, screen, fireEvent } from '@testing-library/react';
import ActionButton from '../ActionButton';

test('显示加载文本', () => {
  render(<ActionButton loading loadingText="提交中...">提交</ActionButton>);
  expect(screen.getByText('提交中...')).toBeInTheDocument();
});
```

---

## 5. 第三阶段：后端核心

### 5.1 Agent 后端（FastAPI）

**学什么（按顺序）：**

1. **Python 基础语法**（如果你还不会）
   - 变量、函数、类、列表推导式
   - `async/await` 异步编程
   - 类型注解：`def foo(name: str) -> int:`

2. **FastAPI 核心概念**
```python
from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI()

class Item(BaseModel):
    name: str
    price: float

@app.post("/items/")
async def create_item(item: Item):
    return {"message": f"Created {item.name}"}
```

3. **Pydantic 数据校验**
```python
# 请求体自动校验：类型不对 → 422 错误
class ProxyRequest(BaseModel):
    method: str = Field(..., examples=["POST"])
    path: str = Field(..., examples=["/v1/privacy/mask"])
    body: dict[str, Any] | None = Field(default=None)
```

4. **httpx 异步 HTTP 客户端**
```python
async with httpx.AsyncClient(timeout=60) as client:
    resp = await client.post(agent_url + path, json=body)
```

**在本项目中阅读：**
- `console/bff-go/cmd/server/main.go` — 程序入口
- `console/bff-go/internal/handlers/handlers.go` — HTTP 路由
- `console/bff-go/internal/config/config.go` — 环境变量配置
- `console/bff-go/internal/agent/client.go` — gRPC 客户端

**启动方式：**
```bash
cd console/bff-go
go run ./cmd/server   # 默认监听 :8081
```

### 5.2 Go 后端（Gin + gRPC）

**学什么（按顺序）：**

1. **Go 基础语法**
   - 变量声明：`var x int` 或 `x := 10`
   - 函数：`func add(a, b int) int { return a + b }`
   - 结构体：`type Server struct { port int }`
   - 接口：`type Handler interface { ServeHTTP(w, r) }`
   - 错误处理：`if err != nil { return err }`
   - goroutine：`go func() { ... }()`

2. **Gin 框架**
```go
r := gin.Default()
r.GET("/api/health", func(c *gin.Context) {
    c.JSON(200, gin.H{"status": "ok"})
})
r.Run(":8081")
```

3. **gRPC + Protobuf**
```protobuf
// proto/privacy.proto
service PrivacyService {
  rpc Mask(MaskRequest) returns (MaskResponse);
  rpc DPCount(DPCountRequest) returns (DPCountResponse);
}
```

```go
// 调用 gRPC
resp, err := client.Mask(ctx, &pb.MaskRequest{
    Data:   jsonData,
    Fields: fields,
})
```

**在本项目中阅读：**
- `console/bff-go/cmd/server/main.go` — 程序入口
- `console/bff-go/internal/handlers/handlers.go` — HTTP 路由
- `console/bff-go/internal/mapper/mapper.go` — REST→gRPC 映射
- `console/bff-go/internal/agent/client.go` — gRPC 客户端

**启动方式：**
```bash
cd console/bff-go
go run ./cmd/server   # 默认监听 :8081
```

### 5.3 Go BFF 特点

| 维度 | Go BFF |
|------|--------|
| 前端入口 | REST (HTTP/JSON) |
| 与 agent 通信 | gRPC (Protobuf) |
| 性能 | 高（编译型 + goroutine） |
| 部署 | 单个静态二进制文件 |
| 适用场景 | 高并发、生产部署 |

前端通过 `BackendSelector` 在 REST 与 gRPC 两种上游协议间切换，统一由 Go BFF 承接。

---

## 6. 第四阶段：Agent 与隐私原语

### 6.1 Agent 是什么

`PrivShield` 是真正执行隐私算法的引擎。前端和后端都只是"代理"和"展示"。

**它提供的能力：**

| 能力 | 说明 | 对应代码 |
|------|------|---------|
| 脱敏 (Masking) | 手机号→138****8000 | `privacy/masking.py` |
| 差分隐私 (DP) | 给统计结果加噪声 | `privacy/dp.py` |
| K-匿名 | 让记录不可区分 | `privacy/kano.py` |
| 查询混淆 (QOL) | 注入虚假查询 | `privacy/qol.py` |
| 动态分类分级 | 三层漏斗：规则→NER→LLM | `dynclassification/` |

### 6.2 三层分类漏斗

```
输入字段 "patient_brca1_gene"
        │
        ▼
┌─────────────────┐
│  Layer 1: 规则   │  ← 字段名/值模式匹配（毫秒级）
│  命中 → 输出结果  │
│  未命中 ↓        │
└─────────────────┘
        │
        ▼
┌─────────────────┐
│  Layer 2: NER   │  ← 小型命名实体识别模型（十毫秒级）
│  命中 → 输出结果  │
│  未命中 ↓        │
└─────────────────┘
        │
        ▼
┌─────────────────┐
│  Layer 3: LLM   │  ← 本地大模型推理（百毫秒~秒级）
│  最终兜底        │
└─────────────────┘
```

### 6.3 启动 Agent

```bash
# 安装
pip install -e .

# 启动 REST + gRPC
python -m engine.server
# REST: http://127.0.0.1:8079
# gRPC: 127.0.0.1:50051
```

---

## 7. 第五阶段：工程化与部署

### 7.1 本地联调（最常用）

```bash
# 终端 1：启动 agent
python -m engine.server

# 终端 2：启动 Go 后端
cd console/bff-go && go run ./cmd/server

# 终端 3：启动前端开发服务器
cd console/web && corepack pnpm dev
```

然后浏览器打开 `http://localhost:5173`。

### 7.2 一键启动脚本

```bash
# 从仓库根目录执行
./scripts/dev/dev-engine-console.sh          # 启动 Agent + Go BFF + Vite 前端 (HMR)
./scripts/dev/dev-engine-console.sh --mtls   # 以 mTLS 双向认证模式启动
./scripts/dev/dev-stop.sh                   # 停止所有开发服务
```

### 7.3 Docker 部署

```bash
# 构建 agent 镜像
docker build --target core -t privshield:1.8.0 .

# 运行
docker run -p 8079:8079 -p 50051:50051 privshield:1.8.0
```

### 7.4 环境变量速查

| 变量 | 默认值 | 作用 |
|------|--------|------|
| `PRIVACY_REST_PORT` | 8079 | Agent REST 端口 |
| `PRIVACY_GRPC_PORT` | 50051 | Agent gRPC 端口 |
| `PRIVACY_AGENT_URL` | http://127.0.0.1:8079 | Agent REST 监听地址 |
| `PRIVACY_AGENT_GRPC_HOST` | 127.0.0.1 | Go 后端连 agent 的 gRPC 地址 |
| `AGENT_TLS_ENABLED` | false | 是否启用 TLS |
| `AGENT_AUTH_ENABLED` | false | 是否启用 API Key 认证 |

---

## 8. 本项目代码导读

### 8.1 前端代码地图

```
console/web/src/
├── main.tsx              ← 入口：挂载 React 到 DOM
├── App.tsx               ← 根组件：全局状态 + 视图路由
├── i18n/index.tsx        ← 国际化字典（中/英）
├── types/api.ts          ← 前后端数据契约（TypeScript 接口）
├── api/client.ts         ← 所有 HTTP 请求封装
├── hooks/
│   └── useAsyncAction.ts ← 异步三态 Hook（loading/data/error）
├── lib/
│   ├── categories.ts     ← 接口分类元数据
│   ├── curl.ts           ← cURL 命令生成
│   ├── history.ts        ← localStorage 历史记录
│   └── levelColor.ts     ← 等级着色工具
├── utils/
│   ├── error.ts          ← 统一错误消息提取
│   ├── fileParse.ts      ← 客户端文件解析
│   └── sampleFile.ts     ← 示例文件生成
└── components/
    ├── Sidebar.tsx        ← 侧边栏导航
    ├── EndpointView.tsx   ← 单接口测试
    ├── ResponsePanel.tsx  ← 响应展示
    ├── DynClassificationPanel.tsx ← 动态分类分级
    ├── OpsPanel.tsx       ← 运维诊断
    ├── FileTest.tsx       ← 文件上传处理
    ├── BatchTest.tsx      ← 批量测试
    ├── LbTest.tsx         ← 负载均衡测试
    └── ...
```

**建议阅读顺序：**
1. `main.tsx` → `App.tsx`（理解整体结构）
2. `types/api.ts`（理解数据长什么样）
3. `api/client.ts`（理解请求怎么发出去）
4. `Sidebar.tsx`（理解 props 和状态）
5. `EndpointView.tsx`（理解完整的交互流程）
6. `hooks/useAsyncAction.ts`（理解自定义 Hook）
7. `DynClassificationPanel.tsx`（理解复杂组件）

### 8.2 Go 代理网关与微服务代码地图

#### (1) 共享基础库 (`pkg/`)
```
pkg/（仓库根共享基础库）
├── store/              ← 存储接口 (TaskStore, DataSourceStore, AuditStore)
│   ├── memory/         ← 内存安全存储实现
│   └── sqlite/         ← SQLite 纯 Go WAL 持久化引擎
├── middleware/         ← 共享中间件 (Auth, CORS, RequestID, Recovery, SecurityHeaders)
├── metrics/            ← Prometheus 模块级指标收集器与 HTTP 拦截器
├── agent/              ← 上游 Agent HTTP 客户端 (带熔断器与 64MB 限制)
├── config/             ← 环境变量统一解析与结构化日志
└── validation/         ← 白名单校验、端口范围与抗碰撞 ID 生成
```

#### (2) 调度中枢 (`services/service-hub/`)
```
services/service-hub/
├── cmd/server/main.go       ← 服务入口 (HTTP :8082 + gRPC :50052 双协议)
├── internal/
│   ├── agent/               ← Agent 上游通信 (REST / gRPC)
│   ├── config/              ← 环境变量与 TLS 配置
│   ├── handlers/            ← REST 路由 (Dispatch, Classify, Tasks, Pipeline)
│   └── grpcserver/          ← gRPC 服务实现 (带 mTLS 与公钥固定)
└── proto/                   ← gRPC Protobuf 定义文件
```

#### (3) 数据源管理 (`services/datasource-mgr/`)
```
services/datasource-mgr/
├── cmd/server/main.go       ← 服务入口 (:8083)
├── internal/
│   ├── agent/               ← Agent 探活与分类委托客户端
│   ├── config/              ← 环境变量配置
│   └── handlers/            ← 数据源 CRUD、连通性测试、元数据分类与访问审计
```

#### (4) 脱敏审计日志 (`services/audit-log/`)
```
services/audit-log/
├── cmd/server/main.go       ← 服务入口 (:8084)
├── internal/
│   ├── agent/               ← 上游 Agent 通信客户端
│   ├── config/              ← 环境变量配置
│   └── handlers/            ← 审计日志、8 要素 SHA-256 存证快照与合规报告
```

#### (5) Go gRPC 代理网关 (`console/bff-go/`)
```
console/bff-go/
├── cmd/server/main.go       ← 入口：配置 → 客户端 → 路由 → 静态托管 → 启动 (:8081)
├── internal/
│   ├── config/config.go     ← 环境变量读取
│   ├── agent/client.go      ← gRPC 客户端 (HTTP/2 连接池 + mTLS)
│   ├── handlers/handlers.go ← HTTP 处理器 (Health/Proxy/Batch/Upload/LbTest)
│   ├── mapper/              ← REST path → gRPC method 映射调度表
│   ├── models/models.go     ← JSON 请求/响应结构体
│   ├── samples/samples.go   ← 内置示例数据
│   ├── fileparse/           ← CSV/JSON 文件解析
│   └── lbtest/              ← 负载均衡测试逻辑
└── proto/                   ← Protobuf 生成的 Go 代码
```

### 8.3 Go BFF 代理网关代码地图

```
console/bff-go/
├── cmd/server/
│   └── main.go          ← 程序入口 (:8081 HTTP / :50055 gRPC)
├── internal/
│   ├── config/          ← 环境变量配置
│   ├── handlers/        ← HTTP 路由（REST 入口）
│   ├── grpcserver/      ← gRPC 网关服务端
│   ├── agent/           ← 上游 Agent gRPC 客户端
│   ├── mapper/          ← REST→gRPC 映射
│   ├── samples/         ← 示例数据
│   └── ...
├── proto/               ← Protobuf 生成代码
├── tests/               ← 集成测试
├── docs/                ← BFF 文档
└── Dockerfile
```

---

## 9. 推荐资源

### 前端

| 资源 | 说明 | 链接 |
|------|------|------|
| MDN Web Docs | HTML/CSS/JS 权威参考 | https://developer.mozilla.org |
| React 官方教程 | 新版交互式教程 | https://react.dev/learn |
| TypeScript Handbook | 官方手册 | https://www.typescriptlang.org/docs/handbook/ |
| Tailwind CSS Docs | 官方文档（ searchable） | https://tailwindcss.com/docs |
| Vite 官方文档 | 构建工具 | https://vitejs.dev/guide/ |

### 后端

| 资源 | 说明 | 链接 |
|------|------|------|
| Go Tour | 官方交互式入门 | https://go.dev/tour/ |
| Go by Example | 代码示例集 | https://gobyexample.com |
| Gin 文档 | Web 框架 | https://gin-gonic.com/docs/ |
| FastAPI 教程 | 官方中文教程 | https://fastapi.tiangolo.com/zh/tutorial/ |
| Pydantic V2 文档 | 数据校验 | https://docs.pydantic.dev/latest/ |
| gRPC Go 教程 | 官方教程 | https://grpc.io/docs/languages/go/ |

### 通用

| 资源 | 说明 |
|------|------|
| 项目 `docs/learning/tech-*.md` | 每个技术的详细说明文档 |
| 本项目 `console/docs/modes.md` | 开发模式 vs 生产模式 |
| 本项目 `AGENTS.md` | 项目整体架构与约定 |

---

## 10. 常见问题 FAQ

### Q: 前端改了代码但看不到效果？

确认 Vite 开发服务器正在运行（`corepack pnpm dev`），它支持热更新。如果不行，硬刷新浏览器（Ctrl+Shift+R）。

### Q: 前端请求报 CORS 错误？

开发时前端在 `:5173`，Go BFF 在 `:8081`，端口不同触发跨域。解决方案：
- 方案 A：使用 `vite.config.ts` 中的 `server.proxy` 配置
- 方案 B：Go BFF 已配置 CORS 中间件，确认后端正在运行

### Q: 前端如何切换协议？

页面顶部 `BackendSelector` 可在 REST 与 gRPC 两种上游协议间切换。两者均由 Go BFF 承接，切换通过请求头 `X-PrivShield-Protocol` 标记。

### Q: 为什么只有 Go BFF？

项目已统一收敛到单个 Go BFF，对外提供 REST 入口，内部通过 gRPC 与 Agent 通信。这样可减少维护成本并提升生产性能。

### Q: 怎么跑测试？

```bash
# 前端测试
cd console/web && ./node_modules/.bin/vitest run

# Go BFF 测试
cd console/bff-go && go test ./...

# Agent 测试
PYTHONPATH=. pytest tests -q
```

### Q: 看不懂代码里的中英双语注释？

本项目约定所有注释和文档使用"中文 / English"双语格式，方便不同语言的开发者阅读。你只需要看其中一种语言即可。

### Q: 学习过程中遇到不懂的 API 怎么办？

1. 先在 `docs/learning/tech-*.md` 中搜索（本项目已有详细技术文档）
2. 再去官方文档搜索
3. 最后用搜索引擎搜索具体错误信息

---

## 附录：学习检查清单

完成以下所有项目后，你就具备了独立维护本项目前后端的能力：

- [ ] 能手写一个 React 函数组件，包含 useState 和事件处理
- [ ] 能解释 `useEffect` 的依赖数组作用
- [ ] 能用 TypeScript 定义一个 interface 并用于函数参数
- [ ] 能读懂 Tailwind 类名并写出基本布局
- [ ] 能启动本项目前端开发服务器并看到页面
- [ ] 能解释前端请求是如何到达 agent 的（完整链路）
- [ ] 能写一个简单的 FastAPI 端点
- [ ] 能写一个简单的 Gin 端点
- [ ] 能解释 gRPC 和 REST 的区别
- [ ] 能运行本项目的前端测试并全部通过
- [ ] 能修改一个现有组件并通过测试
- [ ] 能用 Docker 构建并运行 agent 镜像
