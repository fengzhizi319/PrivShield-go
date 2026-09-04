/**
 * 接口总览页组件 / API Overview Page Component
 *
 * 功能：控制台的首页，以分类卡片网格展示全部功能模块。
 * Function: Console homepage, displays all functional modules as categorized card grid.
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 接收 samples 数组，按 category 分组（useMemo 缓存）；
 *   2. 对分类名排序（orderCategories 保证固定顺序）；
 *   3. 每个分类渲染一张卡片：渐变色条 + 图标 + 名称 + 接口数 + 前 3 个接口预览；
 *   4. 点击卡片进入该分类的第一个接口测试页。
 *
 * Compared to a flat long list, card grid provides clearer navigation entry points.
 */

/** 引入 React 记忆化 Hook / Import React memoization Hook */
import { useMemo } from 'react';
/** 引入端点示例类型 / Import endpoint sample type */
import type { EndpointSample } from '@/types/api';
/** 引入分类元数据与排序工具 / Import category metadata and ordering utility */
import { categoryMeta, orderCategories } from '@/lib/categories';
/** 引入内联 SVG 图标组件 / Import inline SVG icon component */
import { Icon } from '@/components/icons';
/** 引入国际化 Hook / Import i18n Hook */
import { useI18n } from '@/i18n';

/**
 * Overview 组件属性接口 / Overview Component Props Interface
 */
interface OverviewProps {
  /** 全部端点示例数据 / All endpoint sample data */
  samples: EndpointSample[];
  /** 点击卡片时的回调（进入该端点测试）/ Callback when card clicked (enter endpoint test) */
  onSelect: (sample: EndpointSample) => void;
}

/**
 * 概览页主组件 / Overview Page Main Component
 *
 * 以分类卡片网格展示全部功能模块，点击卡片进入该分类的第一个接口。
 * 相比平铺长列表，提供更清晰的导航入口。
 * Displays all modules as categorized card grid; clicking a card enters the first endpoint of that category.
 */
export default function Overview({ samples, onSelect }: OverviewProps) {
  const { t } = useI18n(); // 获取翻译函数 / Get translation function

  /**
   * 按 category 分组 samples / Group samples by category
   *
   * 使用 useMemo 缓存分组结果，仅当 samples 引用变化时重新计算。
   * Cache grouped result with useMemo; recompute only when samples reference changes.
   */
  const grouped = useMemo(() => {
    // 创建 Map：分类名 → 该分类下的端点数组 / Create Map: category name → endpoints array
    const map = new Map<string, EndpointSample[]>();
    for (const s of samples) {
      // 获取或初始化该分类的数组 / Get or initialize the array for this category
      const list = map.get(s.category) || [];
      list.push(s);              // 将当前端点加入分组 / Add current endpoint to group
      map.set(s.category, list); // 更新 Map / Update Map
    }
    return map;
  }, [samples]);

  // 对分类名排序（保证 Masking → DP → K-Anonymity → ... 的固定顺序）
  // Sort category names (ensures fixed order: Masking → DP → K-Anonymity → ...)
  const categories = orderCategories([...grouped.keys()]);

  return (
    /* 外层容器：全高可滚动 / Outer container: full-height scrollable */
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-5xl px-8 py-10">
        {/* 标题区：页面标题 + 统计副标题 / Title area: page title + statistics subtitle */}
        <div className="mb-8">
          <h1 className="text-2xl font-bold text-gray-900">{t('overview.title')}</h1>
          <p className="mt-1.5 text-sm text-gray-500">
            {/* 显示接口总数与分类数 / Show total endpoints and category count */}
            {t('overview.subtitle', samples.length, categories.length)}
          </p>
        </div>

        {/* 分类卡片网格：响应式 1/2/3 列 / Category card grid: responsive 1/2/3 columns */}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {categories.map((category) => {
            // 获取分类的图标/颜色/描述元数据 / Get category icon/color/description metadata
            const meta = categoryMeta(category);
            // 获取该分类下的全部端点 / Get all endpoints under this category
            const list = grouped.get(category)!;
            // 取第一个端点作为卡片点击目标 / Take first endpoint as card click target
            const first = list[0];
            return (
              /* 卡片按钮：悬停时上移 + 边框变色 + 阴影加深 / Card button: hover lifts + border color change + deeper shadow */
              <button
                key={category}
                onClick={() => onSelect(first)}
                className="group flex flex-col overflow-hidden rounded-xl border border-gray-200 bg-white text-left shadow-sm transition-all hover:-translate-y-0.5 hover:border-indigo-200 hover:shadow-md"
              >
                {/* 顶部渐变色条：每个分类独特的颜色标识 / Top gradient bar: unique color identity per category */}
                <div className={`h-1 bg-gradient-to-r ${meta.accent}`} />
                <div className="flex flex-1 flex-col p-4">
                  {/* 图标 + 分类名 + 接口数徽章 / Icon + category name + endpoint count badge */}
                  <div className="flex items-center gap-3">
                    <span
                      className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-lg ${meta.chip}`}
                    >
                      <Icon name={meta.icon} className="h-5 w-5" />
                    </span>
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        {/* 分类名称（截断过长文本）/ Category name (truncate long text) */}
                        <h2 className="truncate text-sm font-semibold text-gray-900">{category}</h2>
                        {/* 接口数量徽章 / Endpoint count badge */}
                        <span className="shrink-0 rounded-full bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium text-gray-500">
                          {list.length}
                        </span>
                      </div>
                      {/* 分类描述（截断）/ Category description (truncated) */}
                      <p className="mt-0.5 truncate text-xs text-gray-400">{meta.desc}</p>
                    </div>
                  </div>

                  {/* 前 3 个接口预览列表 / Preview list of first 3 endpoints */}
                  <ul className="mt-3 space-y-1 border-t border-gray-100 pt-3">
                    {list.slice(0, 3).map((s) => (
                      <li
                        key={`${s.method}-${s.path}`}
                        className="flex items-center gap-2 text-xs text-gray-500"
                      >
                        {/* HTTP 方法徽章：GET=绿色 / POST=蓝色 / HTTP method badge: GET=green / POST=blue */}
                        <span
                          className={`w-9 shrink-0 rounded px-0.5 py-px text-center text-[9px] font-bold ${
                            s.method === 'GET'
                              ? 'bg-emerald-50 text-emerald-600'
                              : 'bg-sky-50 text-sky-600'
                          }`}
                        >
                          {s.method}
                        </span>
                        {/* 接口简短名称 / Endpoint short label */}
                        <span className="truncate">{s.label}</span>
                      </li>
                    ))}
                    {/* 超过 3 个时显示 "+N 个更多" / Show "+N more" when exceeding 3 */}
                    {list.length > 3 && (
                      <li className="text-[11px] text-gray-400">{t('overview.more', list.length - 3)}</li>
                    )}
                  </ul>

                  {/* 悬停时显示的"进入测试"提示 / "Enter test" hint shown on hover */}
                  <div className="mt-auto flex items-center gap-1 pt-3 text-xs font-medium text-indigo-600 opacity-0 transition-opacity group-hover:opacity-100">
                    {t('overview.enter_test')}
                    <Icon name="chevron-right" className="h-3.5 w-3.5" />
                  </div>
                </div>
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}
