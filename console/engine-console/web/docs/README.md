# 测试控制台前端（Web）文档索引

本目录包含 Privacy 测试控制台 **Web 前端**（`console/web`）的全套 SDLC 文档。

前端是一个 **React 18 + TypeScript + Vite + Tailwind CSS** 单页应用，以三栏布局（顶部导航 + 侧边导航 + 主区域）组织 `PrivShield` 的全部可测试接口，支持示例加载、请求编辑、JSON 高亮响应、cURL 导出、请求历史与批量测试。它通过统一的 `/v1/*` 契约与 Go BFF（REST 入口 / gRPC 上游）通信。

## 文档清单

| 文档 | 说明 | 目标读者 |
|---|---|---|
| [prd.md](./prd.md) | 产品需求文档 | 产品经理、项目经理 |
| [design.md](./design.md) | 前端架构、组件设计与状态管理 | 前端开发 |
| [api_reference.md](./api_reference.md) | 前后端数据契约、类型定义与后端 API 约定 | 前端 / 接入开发者 |
| [ops.md](./ops.md) | 开发、构建与部署 | 前端开发、SRE |
| [testing.md](./testing.md) | 测试策略与验证清单 | QA、测试开发 |

## 快速开始

1. 阅读 [prd.md](./prd.md) 了解控制台前端的产品定位与功能需求。
2. 阅读 [design.md](./design.md) 掌握三栏布局、视图路由与状态管理设计。
3. 联调或修改数据契约时参考 [api_reference.md](./api_reference.md)。
4. 开发、构建与部署参考 [ops.md](./ops.md)。
5. 回归验证时参考 [testing.md](./testing.md)。

## 本地开发

```bash
cd console/web
npm install
npm run dev          # 开发服务器 http://127.0.0.1:5173，自动代理 /api 到 8081
```

## 构建

```bash
cd console/web
npm run build        # 产物输出到 dist/，由后端挂载为静态资源
```
