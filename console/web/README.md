# Privacy Test Console - Frontend

React 18 + TypeScript + Vite + Tailwind CSS 实现的前端测试控制台。

以三栏布局（顶部导航 + 侧边导航 + 主区域）组织 `PrivShield` 的全部可测试接口，支持示例加载、请求编辑、JSON 高亮响应、cURL 导出、请求历史与批量测试。

前端模式与部署差异说明见 [`../docs/modes.md`](../docs/modes.md)，Vite 的原理、结构与项目用法说明见 [`../docs/vite.md`](../docs/vite.md)。

## 开发

```bash
cd console/web
npm install
# 启动开发服务器（自动代理 /api 到 127.0.0.1:8081）
npm run dev
```

开发服务器默认在 `http://127.0.0.1:5173` 启动，需先启动 Go 后端（`cd ../bff-go && go run ./cmd/server`）。

## 前端模式切换说明

前端在不同模式下的核心差异，不是“页面长什么样”，而是 **API 基址从哪里来、请求是否跨域、静态资源由谁托管**。

### 1. 开发模式

开发模式适合本地联调和快速改 UI：

- 前端由 Vite 开发服务器提供，默认地址是 `http://127.0.0.1:5173`
- 后端运行在 `http://127.0.0.1:8081`（Go BFF，同时提供 REST 入口与 gRPC 上游协议）
- 前端会通过绝对地址访问后端，因此经常会触发跨域
- 此时优先依赖后端 CORS 中间件，或使用 Vite 的 `/api` 代理做同源回退

推荐启动方式：

```bash
cd console/web
npm install
npm run dev
```

如果你同时在调试后端，通常再配合：

```bash
cd ../bff-go
go run ./cmd/server
```

### 2. 商业化产品模式

商业化产品模式更接近正式交付或客户环境部署：

- 先执行 `npm run build`，生成 `dist/`
- 由 Go 后端、Nginx 或网关托管静态资源
- 前端页面与 API 尽量同源，减少跨域和浏览器兼容问题
- 浏览器不直接连接开发服务器，也不依赖热更新

推荐部署思路：

1. 构建前端静态文件
2. 由后端或反向代理托管 `dist/`
3. 前端请求地址与页面同源
4. 生产环境只暴露必要端口，并在上游启用 TLS / Auth / 限流

```bash
cd console/web
npm run build
```

### 3. 页面里“切换后端”意味着什么

前端顶部的 Backend Selector 主要切换的是 **API 的上游后端**，不是切换前端本身：

- `REST`：前端请求通过 Go BFF 的 REST 入口转发到 `PrivShield`
- `gRPC`：前端请求仍发往 Go BFF，由 BFF 通过 gRPC 转发到 `PrivShield`

在开发模式下，切换协议通常通过请求头标记区分；在商业化产品模式下，建议收敛到同一个对外入口，避免浏览器端感知多个后端地址。

### 4. 什么时候用哪种模式

| 场景 | 推荐模式 | 说明 |
|---|---|---|
| 改 UI、改交互、看样式 | 开发模式 | 热更新最快，适合频繁改动 |
| 联调后端接口 | 开发模式 | 便于同时看到前后端日志 |
| 对外演示 / 试用 | 商业化产品模式 | 前端静态化、稳定、少暴露入口 |
| 企业内网交付 | 商业化产品模式 | 更适合和 TLS、认证、反向代理一起部署 |

如果你想进一步了解“前端、后端、agent、服务器”在两种模式下的整体区别，可以再看 `../docs/modes.md`。 

## 构建

```bash
npm run build        # 等价于 tsc && vite build
```

产物输出到 `dist/`，由后端挂载为静态资源：

- Go BFF：`console/bff-go/cmd/server`（`PRIVACY_CONSOLE_STATIC_DIR`，默认 `../web/dist`）

## 主要文件

- `src/main.tsx` - 应用入口
- `src/App.tsx` - 根组件：全局状态 + 三栏布局 + 视图路由
- `src/v1/client.ts` - 后端 API 调用封装（唯一 fetch 出口）
- `src/types/api.ts` - 前后端数据契约（TS 类型定义）
- `src/lib/categories.ts` - 分类元数据（顺序 / 图标 / 配色）
- `src/lib/curl.ts` - cURL 命令生成
- `src/lib/history.ts` - 请求历史（localStorage 持久化）
- `src/components/` - 视图与组件
  - `Header.tsx` - 顶栏（品牌 + 健康状态灯 + 后端切换）
  - `BackendSelector.tsx` - 后端切换下拉框
  - `Sidebar.tsx` - 侧边导航树（搜索 / 分组 / 折叠）
  - `Overview.tsx` - 总览页（分类卡片网格）
  - `EndpointView.tsx` - 端点测试页（请求 / 响应分栏）
  - `BatchTest.tsx` - 批量测试页
  - `ResponsePanel.tsx` - 响应查看器（JSON 高亮 / 复制 / 下载）
  - `HistoryPanel.tsx` - 请求历史面板
  - `icons.tsx` - 内联 SVG 图标库

## 技术栈

- React 18
- TypeScript 5
- Vite 5
- Tailwind CSS 3

## 代码质量

```bash
npx tsc --noEmit     # 类型检查
npm run lint         # ESLint
```
