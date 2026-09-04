/**
 * 应用入口文件 / Application Entry Point
 *
 * 功能：挂载 React 根组件到 DOM。
 * Function: Mount the React root component to the DOM.
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 使用 React 18 的 createRoot API 创建渲染根容器；
 *   2. StrictMode 在开发环境下双重调用渲染以帮助发现副作用问题（不影响生产构建）；
 *   3. I18nProvider 提供中英文国际化上下文，包裹整个应用；
 *   4. 引入全局样式 index.css（Tailwind CSS 基础层 + 自定义样式）。
 *
 * Uses React 18's createRoot API; StrictMode double-invokes renders in development
 * to help detect side-effect issues (does not affect production builds).
 */

/** 引入 React 核心库（用于 StrictMode 组件）/ Import React core library (for StrictMode component) */
import React from 'react';
/** 引入 React DOM 客户端渲染 API / Import React DOM client-side rendering API */
import ReactDOM from 'react-dom/client';
/** 引入应用根组件（包含全局状态与布局编排）/ Import the root App component (contains global state and layout) */
import App from './App';
/** 引入国际化 Provider（提供 zh/en 双语切换能力）/ Import i18n Provider (provides zh/en bilingual switching) */
import { I18nProvider } from './i18n';
/** 引入全局样式：Tailwind CSS 三层指令 + 自定义滚动条样式 / Import global styles: Tailwind CSS layers + custom scrollbar */
import './index.css';

/**
 * 创建 React 渲染根容器并挂载组件树 / Create React render root and mount the component tree
 *
 * - document.getElementById('root') 获取 index.html 中的挂载点；
 * - 非空断言 (!) 因为 index.html 中必然存在 #root 元素；
 * - 组件层级：StrictMode > I18nProvider > App。
 */
ReactDOM.createRoot(document.getElementById('root')!).render(
  // StrictMode：开发模式下双重渲染检测副作用 / StrictMode: double-render in dev to detect side effects
  <React.StrictMode>
    {/* I18nProvider：提供 t() 翻译函数与 lang/setLang 状态 / Provides t() translation function and lang/setLang state */}
    <I18nProvider>
      {/* App：应用根组件，包含 Header + Sidebar + 主区域 / Root component with Header + Sidebar + main area */}
      <App />
    </I18nProvider>
  </React.StrictMode>,
);
