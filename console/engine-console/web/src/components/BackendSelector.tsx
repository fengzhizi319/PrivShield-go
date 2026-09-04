/**
 * 后端切换器组件 / Backend Selector Component
 *
 * 功能：在 REST 与 gRPC 两种代理协议间切换（统一由 Go BFF 承接，通过请求头区分）。
 * Function: Switch between REST and gRPC proxy protocols (both served by the Go BFF, distinguished via request header).
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 切换时通过 ``setBaseUrl`` 更新全局通信协议标识；
 *   2. 后续所有请求仍发往当前页面同源的 Go BFF；
 *   3. 默认优先选择 gRPC 协议。
 *
 * On switch, updates global API base URL via ``setBaseUrl``;
 * all subsequent requests will go to the newly selected backend.
 */

/** 引入 API 基址设置函数 / Import API base URL setter function */
import { setBaseUrl } from '@/api/client';
/** 引入内联 SVG 图标组件 / Import inline SVG icon component */
import { Icon } from '@/components/icons';

/**
 * 单个后端选项接口 / Single Backend Option Interface
 *
 * label 用于 UI 展示，value 为实际 API 基址。
 * label for UI display, value is the actual API base URL.
 */
export interface BackendOption {
  /** 显示标签（如 "gRPC", "REST"）/ Display label */
  label: string;
  /** API 基址与协议（如 "http://127.0.0.1:8081?protocol=grpc"）/ API base URL */
  value: string;
}

/**
 * 可选协议列表 / Available Protocol List
 *
 * 统一由 Go BFF 代理后端承接，支持 gRPC 与 REST 两种通信链路。
 */
export const DEFAULT_BACKENDS: BackendOption[] = [
  { label: 'gRPC', value: '?protocol=grpc' },
  { label: 'REST', value: '?protocol=rest' },
];

/**
 * 默认协议选择逻辑 / Default Protocol Selection Logic
 */
export const DEFAULT_BACKEND: BackendOption = DEFAULT_BACKENDS[0];

/**
 * BackendSelector 组件属性 / BackendSelector Component Props
 */
interface BackendSelectorProps {
  /** 当前选中的后端（可选，默认 DEFAULT_BACKEND）/ Currently selected backend (optional, defaults to DEFAULT_BACKEND) */
  value?: BackendOption;
  /** 切换回调（可选）/ Change callback (optional) */
  onChange?: (option: BackendOption) => void;
}

/**
 * 后端切换下拉框组件 / Backend Switch Dropdown Component
 *
 * 渲染一个带服务器图标的 select 下拉框，选择后更新全局 API 基址并触发回调。
 * Renders a select dropdown with server icon; on selection updates global API base URL and triggers callback.
 */
export default function BackendSelector({
  value = DEFAULT_BACKEND,
  onChange,
}: BackendSelectorProps) {
  /**
   * 处理下拉框选择变化 / Handle dropdown selection change
   *
   * 逻辑：查找匹配的选项 → 更新全局基址 → 触发父组件回调。
   * Logic: find matching option → update global base URL → trigger parent callback.
   */
  const handleChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    // 根据 select 的 value 查找对应的后端选项 / Find backend option by select's value
    const option = DEFAULT_BACKENDS.find((b) => b.value === e.target.value);
    if (!option) return; // 安全防御：找不到则忽略 / Safety guard: ignore if not found
    setBaseUrl(option.value); // 更新全局 API 基址 / Update global API base URL
    onChange?.(option);       // 触发父组件回调（可选链安全调用）/ Trigger parent callback (optional chaining)
  };

  return (
    /* 外层 label：relative 定位用于放置图标 / Outer label: relative positioning for icon placement */
    <label className="relative inline-flex items-center">
      {/* 左侧服务器图标（pointer-events-none 避免拦截点击）/ Left server icon (pointer-events-none avoids click interception) */}
      <span className="pointer-events-none absolute left-2.5 text-gray-400">
        <Icon name="server" className="h-3.5 w-3.5" />
      </span>
      {/* 下拉框：appearance-none 移除原生箭头，用自定义图标替代 / Select: appearance-none removes native arrow, replaced by custom icon */}
      <select
        value={value.value}
        onChange={handleChange}
        className="appearance-none rounded-lg border border-gray-200 bg-gray-50 py-1.5 pl-8 pr-8 text-xs font-medium text-gray-700 transition-colors hover:border-gray-300 focus:outline-none focus:ring-2 focus:ring-indigo-500"
      >
        {/* 渲染所有可选后端 / Render all available backends */}
        {DEFAULT_BACKENDS.map((b) => (
          <option key={b.value} value={b.value}>
            {b.label}
          </option>
        ))}
      </select>
      {/* 右侧下箭头图标 / Right chevron-down icon */}
      <span className="pointer-events-none absolute right-2.5 text-gray-400">
        <Icon name="chevron-down" className="h-3.5 w-3.5" />
      </span>
    </label>
  );
}
