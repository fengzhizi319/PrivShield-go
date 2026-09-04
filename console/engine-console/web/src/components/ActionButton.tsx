/**
 * 通用动作按钮组件 / Generic Action Button Component
 *
 * 统一各面板（文件处理 / 负载均衡 / 批量测试等）的主操作按钮样式与
 * loading 交互：非加载态展示图标 + 文案，加载态展示旋转指示器 + 加载文案，
 * 加载中或显式 disabled 时禁用点击。
 * Unifies the primary action button style and loading interaction across panels
 * (file processing / load balancing / batch test, etc.): shows icon + label when
 * idle, spinner + loading text when loading, and disables clicks while loading
 * or explicitly disabled.
 *
 * 详细逻辑 / Detailed Logic：
 *   1. ``loading`` 为 true 时渲染旋转圆圈并优先展示 ``loadingText``；
 *   2. 实际禁用状态为 ``disabled || loading``，避免加载期间重复触发；
 *   3. ``className`` 追加到基础样式之后，允许调用方微调（如宽度）。
 *   1. When ``loading`` is true, renders a spinner and prefers ``loadingText``;
 *   2. Effective disabled state is ``disabled || loading`` to prevent re-triggering;
 *   3. ``className`` is appended after base styles for caller tweaks (e.g. width).
 */

/** 引入 React 节点类型 / Import React node type */
import type { ReactNode } from 'react';
/** 引入图标组件与图标名类型 / Import icon component and icon name type */
import { Icon, type IconName } from '@/components/icons';

/**
 * ActionButton 组件属性 / ActionButton Component Props
 */
interface ActionButtonProps {
  /** 是否处于加载态（显示旋转指示器并禁用）/ Whether in loading state (shows spinner and disables) */
  loading?: boolean;
  /** 是否禁用（与 loading 取或）/ Whether disabled (OR-ed with loading) */
  disabled?: boolean;
  /** 非加载态展示的图标名；省略则不显示图标 / Icon name shown when idle; omitted hides icon */
  icon?: IconName;
  /** 加载态文案；省略时沿用 children / Loading text; falls back to children when omitted */
  loadingText?: ReactNode;
  /** 点击回调 / Click callback */
  onClick?: () => void;
  /** 按钮类型（默认 button）/ Button type (defaults to button) */
  type?: 'button' | 'submit';
  /** 悬浮提示 / Tooltip */
  title?: string;
  /** 追加的自定义样式类 / Additional custom class names */
  className?: string;
  /** 按钮文案（非加载态）/ Button label (idle state) */
  children: ReactNode;
}

/** 基础样式：靛蓝主按钮 + 禁用降透明度 / Base styles: indigo primary button + disabled opacity */
const BASE_CLASS =
  'inline-flex items-center justify-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white shadow-sm transition-colors hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-60';

/**
 * 通用动作按钮 / Generic Action Button
 *
 * @param props - 见 ActionButtonProps / See ActionButtonProps
 */
export default function ActionButton({
  loading = false,
  disabled = false,
  icon,
  loadingText,
  onClick,
  type = 'button',
  title,
  className,
  children,
}: ActionButtonProps) {
  // 加载中或显式禁用时禁用按钮 / Disable when loading or explicitly disabled
  const isDisabled = disabled || loading;

  return (
    <button
      type={type}
      onClick={onClick}
      disabled={isDisabled}
      title={title}
      className={className ? `${BASE_CLASS} ${className}` : BASE_CLASS}
    >
      {loading ? (
        // 加载态：旋转圆圈 / Loading: spinner circle
        <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
      ) : (
        // 非加载态：可选图标 / Idle: optional icon
        icon && <Icon name={icon} className="h-4 w-4" />
      )}
      {/* 文案：加载态优先 loadingText / Label: prefer loadingText when loading */}
      {loading ? (loadingText ?? children) : children}
    </button>
  );
}
