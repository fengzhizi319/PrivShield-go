# 测试控制台前端（Web）测试文档

## 1. 测试目的与策略

前端是纯静态 SPA，不含算法逻辑，测试重点是**类型正确性**与**交互行为**。采用「自动化类型检查 + 手工回归清单」相结合的策略：

| 层次 | 手段 | 说明 |
|---|---|---|
| 类型检查 | `tsc --noEmit` | 构建期全量类型校验，拦截契约不一致、props 误用 |
| 构建验证 | `npm run build` | 先 `tsc` 后 `vite build`，任一失败即中断 |
| 静态检查 | `npm run lint` | ESLint（react-hooks / react-refresh 规则） |
| 手工回归 | 回归清单 | 验证 UI 交互（见第 3 节） |
| 端到端 | 浏览器验证 | 连接真实后端，走通核心链路 |

当前未引入单元测试框架（如 Vitest / Testing Library）。由于组件逻辑薄、数据契约由 TypeScript 强约束，类型检查已能覆盖大部分回归风险；后续如需补充，优先为 `lib/`（curl / history / categories）纯函数添加单测。

## 2. 自动化检查

### 2.1 类型检查

```bash
cd console/web
npx tsc --noEmit
```

重点保障：

- `types/api.ts` 与后端 Pydantic 模型字段一致；
- 组件 props 类型正确传递；
- `View` 判别联合的穷尽性（`view.type` 分支不遗漏）。

### 2.2 构建与 Lint

```bash
npm run build        # tsc && vite build
npm run lint         # eslint . --ext ts,tsx --max-warnings 0
```

## 3. 手工回归清单

### 3.1 导航与总览

- [ ] 首屏加载展示全部分类卡片，数量与 `/v1/samples` 一致。
- [ ] 点击分类卡片进入该分类第一个接口。
- [ ] 侧边栏搜索 `mask` 能过滤出 Masking 相关接口。
- [ ] 分组默认折叠，点击分组头可展开/收起。
- [ ] 「接口总览」「批量测试」快捷入口高亮正确。

### 3.2 端点测试

- [ ] 选择一个 POST 接口，请求体预填示例 JSON。
- [ ] 点击「格式化」能校验并美化 JSON；非法 JSON 给出错误提示。
- [ ] 发送请求后右侧展示状态码、耗时与高亮响应。
- [ ] `Cmd/Ctrl+Enter` 可快捷发送。
- [ ] 切换到另一个端点，请求体与响应被重置（无残留）。
- [ ] GET 接口请求体编辑区禁用并提示「无需请求体」。

### 3.3 辅助工具

- [ ] 复制 cURL，粘贴到终端可直接执行（含特殊字符转义正确）。
- [ ] 复制响应 JSON 到剪贴板，按钮短暂显示「已复制」。
- [ ] 下载响应为 `.json` 文件，文件名由路径派生。
- [ ] 发送请求后历史面板新增一条；点击条目回填请求体。
- [ ] 单条删除与一键清空生效；刷新页面后历史仍在（localStorage）。

### 3.4 批量测试

- [ ] 选择某分类，点击「开始测试」，按钮显示进度。
- [ ] 结果汇总展示正确的通过率 / 成功 / 失败数。
- [ ] 失败条目展示错误信息；点击结果行跳转到对应端点。

### 3.5 后端切换与异常

- [ ] 切换协议（REST ↔ gRPC），samples 与状态灯刷新。
- [ ] 默认选中的后端与页面来源一致。
- [ ] 停止后端后刷新页面，展示错误页与「重试」按钮；恢复后端后点击重试可正常加载。

## 4. 端到端验证

连接真实后端走通核心链路（可用浏览器自动化或手工）：

```bash
# 1. 启动 agent + Go BFF + 前端
./scripts/dev/dev-engine-console.sh

# 2. 构建前端（若未构建）
cd console/web && npm run build

# 3. 浏览器访问 http://127.0.0.1:8081
#    - 总览页 → 选择 Masking 某接口 → 发送 → 确认高亮响应
#    - 批量测试 → 选择 Masking → 确认 100% 通过
```

## 5. 已知测试缺口与建议

- 无单元测试框架：建议引入 Vitest，优先覆盖 `lib/curl.ts`（shell 转义）、`lib/history.ts`（截断/删除）、`lib/categories.ts`（排序/兜底）。
- 无组件测试：关键交互（EndpointView 状态重置、BatchTest 汇总）可补 Testing Library 用例。
- 无视觉回归：样式依赖 Tailwind 字面量类名，改动 `categories.ts` 时需人工确认配色。
