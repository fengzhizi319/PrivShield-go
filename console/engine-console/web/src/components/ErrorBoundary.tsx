/**
 * 错误边界组件 / Error Boundary Component
 *
 * 功能：捕获子组件树渲染期抛出的异常，避免单组件崩溃导致整页白屏。
 * Function: Catches exceptions thrown during child component tree rendering,
 * preventing a single component crash from causing a full-page blank screen.
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 采用 React 类组件（错误边界目前仅支持 getDerivedStateFromError /
 *      componentDidCatch 生命周期，函数组件无法实现）；
 *   2. getDerivedStateFromError 捕获渲染期异常，记录到 state；
 *   3. componentDidCatch 记录错误到控制台（便于调试）；
 *   4. 捕获到错误后展示友好的降级界面，并提供“重试”按钮重置状态、重新渲染子树。
 *
 * Uses React class component (error boundaries currently only support
 * getDerivedStateFromError / componentDidCatch lifecycles; function components cannot implement this).
 * After catching an error, shows a friendly fallback UI with a "Retry" button to reset state and re-render subtree.
 */

/** 引入 React 类组件基类与类型 / Import React class component base and types */
import { Component, type ErrorInfo, type ReactNode } from 'react';
/** 引入内联 SVG 图标组件 / Import inline SVG icon component */
import { Icon } from '@/components/icons';
/** 引入 i18n Context（类组件无法使用 useI18n Hook，改经 contextType 接入）/ Import i18n Context (class components cannot use useI18n Hook, connect via contextType) */
import { I18nContext, type I18nContextValue } from '@/i18n';

/**
 * ErrorBoundary 组件属性 / ErrorBoundary Component Props
 */
interface ErrorBoundaryProps {
  /** 被保护的子组件树 / The protected child component tree */
  children: ReactNode;
}

/**
 * ErrorBoundary 组件状态 / ErrorBoundary Component State
 */
interface ErrorBoundaryState {
  /** 捕获到的错误；为 null 表示子树渲染正常 / Captured error; null means child tree renders normally */
  error: Error | null;
}

/**
 * 错误边界类组件 / Error Boundary Class Component
 *
 * 包裹在主区域外层，当任何视图组件渲染崩溃时展示降级界面而非白屏。
 * Wraps the main area; when any view component crashes during render, shows fallback UI instead of blank page.
 */
export default class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  /**
   * 接入 i18n Context，使类组件能通过 ``this.context`` 获取翻译函数。
   * Connect to the i18n Context so the class component can access the translation
   * function via ``this.context``.
   */
  static contextType = I18nContext;
  declare context: I18nContextValue;

  /** 初始状态：无错误 / Initial state: no error */
  state: ErrorBoundaryState = { error: null };

  /**
   * 渲染期捕获子树异常 / Catch child tree exceptions during rendering
   *
   * React 在子组件 render 抛出异常时调用此静态方法，
   * 返回的新 state 会触发重新渲染（切换到降级界面）。
   * React calls this static method when a child's render throws;
   * the returned new state triggers re-render (switches to fallback UI).
   *
   * @param error - 捕获到的错误对象 / The captured error object
   * @returns 新的 state（包含错误）/ New state (containing the error)
   */
  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error }; // 将错误记录到 state / Record error to state
  }

  /**
   * 错误上报钩子 / Error reporting hook
   *
   * 在错误捕获后调用（仅记录到控制台，便于调试）。
   * 生产环境可对接 Sentry 等错误监控服务。
   * Called after error is captured (logs to console for debugging).
   * In production, can integrate with Sentry or similar error monitoring services.
   *
   * @param error - 错误对象 / Error object
   * @param info - 包含 componentStack 的信息 / Info containing componentStack
   */
  componentDidCatch(error: Error, info: ErrorInfo): void {
    // 记录错误与组件堆栈到控制台 / Log error and component stack to console
    console.error('ErrorBoundary 捕获到渲染异常:', error, info.componentStack);
  }

  /**
   * 重置错误状态，重新尝试渲染子树 / Reset error state, retry rendering child tree
   *
   * 点击“重试”按钮时调用，将 error 置为 null 后
   * React 会重新渲染 children（若问题已解决则恢复正常）。
   * Called when "Retry" button is clicked; sets error to null,
   * React will re-render children (recovers if issue is resolved).
   */
  private handleReset = (): void => {
    this.setState({ error: null }); // 清除错误状态 / Clear error state
  };

  /**
   * 渲染逻辑 / Render Logic
   *
   * - 有错误：展示降级界面（警告图标 + 错误消息 + 重试按钮）；
   * - 无错误：正常渲染子组件树。
   * - Has error: show fallback UI (warning icon + error message + retry button);
   * - No error: render child component tree normally.
   */
  render(): ReactNode {
    // 从 i18n Context 取翻译函数 / Get translation function from i18n Context
    const { t } = this.context;
    // 判断是否捕获到错误 / Check if an error was captured
    if (this.state.error) {
      return (
        /* 降级界面容器：居中布局 / Fallback UI container: centered layout */
        <div className="flex h-full flex-col items-center justify-center gap-4 px-6">
          {/* 红色警告图标 / Red warning icon */}
          <span className="flex h-12 w-12 items-center justify-center rounded-full bg-red-50 text-red-500">
            <Icon name="alert" className="h-6 w-6" />
          </span>
          {/* 错误信息展示区 / Error message display area */}
          <div className="text-center">
            <p className="text-sm font-medium text-gray-800">{t('error.title')}</p>
            {/* 显示具体错误消息（截断过长内容）/ Show specific error message (truncate long content) */}
            <p className="mt-1 max-w-md break-words text-xs text-gray-500">
              {this.state.error.message || t('error.unknown')}
            </p>
          </div>
          {/* 重试按钮：重置状态后重新渲染子树 / Retry button: reset state and re-render child tree */}
          <button
            onClick={this.handleReset}
            className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white shadow-sm transition-colors hover:bg-indigo-700"
          >
            <Icon name="refresh" className="h-4 w-4" />
            {t('error.retry')}
          </button>
        </div>
      );
    }
    // 无错误：正常渲染子组件树 / No error: render child component tree normally
    return this.props.children;
  }
}
