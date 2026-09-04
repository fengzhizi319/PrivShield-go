/**
 * 请求历史面板组件 / Request History Panel Component
 *
 * 以绝对定位覆盖层形式展示当前端点的历史请求记录。
 * Displays current endpoint's historical request records as an absolutely positioned overlay.
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 点击条目可快速回填请求体到编辑器；
 *   2. 支持单条删除（悬浮显示删除按钮）；
 *   3. 支持一键清空全部历史；
 *   4. 空历史时显示友好空态提示。
 *   1. Click entry to quickly restore request body to editor;
 *   2. Supports single entry deletion (delete button shows on hover);
 *   3. Supports one-click clear all history;
 *   4. Shows friendly empty state when no history.
 */

/** 引入历史条目类型 / Import history entry type */
import type { HistoryEntry } from '@/types/api';
/** 引入相对时间格式化工具 / Import relative time formatting utility */
import { formatRelativeTime } from '@/lib/history';
/** 引入图标组件 / Import icon component */
import { Icon } from '@/components/icons';
/** 引入国际化 Hook / Import i18n Hook */
import { useI18n } from '@/i18n';

/**
 * HistoryPanel 组件属性接口 / HistoryPanel Component Props Interface
 */
interface HistoryPanelProps {
  /** 已按当前端点过滤的历史记录 / History entries filtered for current endpoint */
  entries: HistoryEntry[];
  /** 回填请求体回调 / Restore request body callback */
  onRestore: (body: string) => void;
  /** 删除单条记录回调 / Delete single entry callback */
  onDelete: (id: string) => void;
  /** 清空全部历史回调 / Clear all history callback */
  onClear: () => void;
  /** 关闭面板回调 / Close panel callback */
  onClose: () => void;
}

/**
 * 状态码徽章配色 / Status Code Badge Color Scheme
 *
 * 根据 HTTP 状态码返回对应的 Tailwind CSS 类名：
 * Returns corresponding Tailwind CSS class names based on HTTP status code:
 *   - 0（网络错误）→ 灰色 / 0 (network error) → gray
 *   - 2xx（成功）→ 绿色 / 2xx (success) → green
 *   - 其他（4xx/5xx）→ 红色 / Others (4xx/5xx) → red
 *
 * @param status - HTTP 状态码 / HTTP status code
 * @returns Tailwind CSS 类名 / Tailwind CSS class names
 */
function statusBadge(status: number): string {
  if (status === 0) return 'bg-gray-100 text-gray-500';           // 网络错误（灰色）/ Network error (gray)
  if (status >= 200 && status < 300) return 'bg-emerald-50 text-emerald-600'; // 成功（绿色）/ Success (green)
  return 'bg-red-50 text-red-600';                                // 失败（红色）/ Failure (red)
}

/**
 * 请求历史面板主组件 / Request History Panel Main Component
 *
 * 以右侧滑出层展示当前端点的历史请求，
 * 点击条目可快速回填请求体，支持单条删除与一键清空。
 * Displays current endpoint's history as a slide-out layer,
 * click entry to restore body, supports single delete and clear all.
 */
export default function HistoryPanel({ entries, onRestore, onDelete, onClear, onClose }: HistoryPanelProps) {
  const { t } = useI18n(); // 获取翻译函数 / Get translation function
  return (
    /* 覆盖层容器：绝对定位填满父元素，z-10 置于编辑区之上，白色背景 */
    /* Overlay container: absolute fill parent, z-10 above editor, white background */
    <div className="absolute inset-0 z-10 flex flex-col bg-white">
      {/* ====== 面板头部 / Panel Header ====== */}
      <div className="flex items-center justify-between border-b border-gray-100 px-4 py-2.5">
        {/* 标题：时钟图标 + "请求历史 (N)" / Title: clock icon + "Request History (N)" */}
        <span className="inline-flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-gray-500">
          <Icon name="clock" className="h-3.5 w-3.5" />
          {t('history.title', entries.length)}
        </span>
        <div className="flex items-center gap-1">
          {/* 清空按钮：仅在有记录时显示 / Clear button: only shown when entries exist */}
          {entries.length > 0 && (
            <button
              onClick={onClear}
              className="rounded-md px-2 py-1 text-xs text-gray-400 transition-colors hover:bg-red-50 hover:text-red-600"
            >
              {t('history.clear')}
            </button>
          )}
          {/* 关闭按钮：X 图标 / Close button: X icon */}
          <button
            onClick={onClose}
            className="flex h-6 w-6 items-center justify-center rounded-md text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600"
            title={t('history.close')}
            aria-label={t('history.close')}
          >
            <Icon name="x" className="h-3.5 w-3.5" />
          </button>
        </div>
      </div>

      {/* ====== 历史列表（可滚动）/ History List (Scrollable) ====== */}
      <div className="flex-1 overflow-y-auto p-2">
        {/* 空状态：无历史记录时显示友好提示 / Empty state: friendly hint when no history */}
        {entries.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-gray-300">
            <Icon name="clock" className="h-8 w-8" strokeWidth={1.5} />
            <p className="text-xs text-gray-400">{t('history.empty')}</p>
          </div>
        ) : (
          /* 历史条目列表 / History entry list */
          <ul className="space-y-1">
            {entries.map((entry) => (
              /* 单条历史记录，group 类用于悬浮显示删除按钮 / Single history entry, group class for hover-reveal delete button */
              <li key={entry.id} className="group relative">
                {/* 条目主体按钮：点击回填请求体 / Entry body button: click to restore request body */}
                <button
                  onClick={() => onRestore(entry.body)}
                  className="w-full rounded-lg border border-gray-100 bg-gray-50/50 px-3 py-2 pr-9 text-left transition-colors hover:border-indigo-200 hover:bg-indigo-50/40"
                  title={t('history.fill_title')}
                >
                  <div className="flex items-center gap-2">
                    {/* 状态码徽章（0=ERR/2xx=绿/其他=红）/ Status badge (0=ERR/2xx=green/others=red) */}
                    <span className={`rounded px-1.5 py-0.5 text-[10px] font-bold ${statusBadge(entry.status)}`}>
                      {entry.status === 0 ? 'ERR' : entry.status}
                    </span>
                    {/* 相对时间（如 "3分钟前"）/ Relative time (e.g. "3 min ago") */}
                    <span className="text-[11px] text-gray-400">{formatRelativeTime(entry.timestamp)}</span>
                  </div>
                  {/* 请求体预览（单行截断）/ Request body preview (single line truncate) */}
                  <p className="mt-1 truncate font-mono text-[11px] leading-relaxed text-gray-500">
                    {entry.body || t('history.body_empty')}
                  </p>
                </button>
                {/* 删除按钮：悬浮时显示（opacity-0 → group-hover:opacity-100）/ Delete button: shown on hover (opacity-0 → group-hover:opacity-100) */}
                <button
                  onClick={() => onDelete(entry.id)}
                  className="absolute right-2 top-1/2 -translate-y-1/2 rounded-md p-1 text-gray-300 opacity-0 transition-all hover:bg-red-50 hover:text-red-500 group-hover:opacity-100"
                  title={t('history.delete_title')}
                  aria-label={t('history.delete_title')}
                >
                  <Icon name="trash" className="h-3.5 w-3.5" />
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
