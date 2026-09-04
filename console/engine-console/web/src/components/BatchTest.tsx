/**
 * 批量测试视图：一键回归验证 / Batch Test View: One-click Regression Verification
 *
 * 选择一个分类（或全部），顺序调用其下所有接口，
 * 汇总展示通过率与逐条结果，单个失败不中断整个批次。
 * Select a category (or all), sequentially invoke all endpoints under it,
 * summarizes pass rate and per-item results, single failure won't abort the batch.
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 下拉框选择测试范围（全部分类 / 单个分类）；
 *   2. 点击“开始测试”后调用 batchRequest API（后端顺序执行）；
 *   3. 结果展示：通过率圆形徽章 + 汇总文字 + 明细表格；
 *   4. 明细表中点击接口可跳转到单个端点测试视图。
 *   1. Dropdown selects test scope (all categories / single category);
 *   2. Click "Start Test" calls batchRequest API (backend executes sequentially);
 *   3. Results display: pass rate circular badge + summary text + detail table;
 *   4. Click endpoint in detail table navigates to single endpoint test view.
 */

/** 引入 React Hooks / Import React Hooks */
import { useMemo, useState } from 'react';
/** 引入类型定义 / Import type definitions */
import type { EndpointSample, BatchResponse } from '@/types/api';
/** 引入批量请求 API / Import batch request API */
import { batchRequest } from '@/api/client';
/** 引入分类排序工具 / Import category ordering utility */
import { orderCategories } from '@/lib/categories';
/** 引入图标组件 / Import icon component */
import { Icon } from '@/components/icons';
/** 引入通用动作按钮 / Import generic action button */
import ActionButton from '@/components/ActionButton';
/** 引入国际化 Hook / Import i18n Hook */
import { useI18n } from '@/i18n';
/** 引入统一错误消息提取工具 / Import unified error message extraction utility */
import { getErrorMessage } from '@/utils/error';

/**
 * BatchTest 组件属性接口 / BatchTest Component Props Interface
 */
interface BatchTestProps {
  /** 全部端点示例数据 / All endpoint sample data */
  samples: EndpointSample[];
  /** 从结果跳转到单个端点测试 / Navigate from result to single endpoint test */
  onSelectSample: (sample: EndpointSample) => void;
}

/** 特殊值：表示“全部分类” / Special value: represents "all categories" */
const ALL = '__all__';

/**
 * 批量测试主组件 / Batch Test Main Component
 *
 * 选择一个分类（或全部），一键顺序调用其下所有接口，
 * 汇总展示成功 / 失败与耗时，便于快速回归验证。
 * Select a category (or all), one-click sequential invocation of all endpoints,
 * summarizes success / failure and duration for quick regression verification.
 */
export default function BatchTest({ samples, onSelectSample }: BatchTestProps) {
  const { t } = useI18n(); // 获取翻译函数 / Get translation function
  /** 当前选择的分类（ALL 表示全部）/ Currently selected category (ALL means all) */
  const [category, setCategory] = useState<string>(ALL);
  /** 测试运行中标记 / Test running flag */
  const [running, setRunning] = useState(false);
  /** 批量响应结果 / Batch response result */
  const [result, setResult] = useState<BatchResponse | null>(null);
  /** 错误信息 / Error message */
  const [error, setError] = useState<string | null>(null);

  // 提取并排序所有分类名（useMemo 缓存）/ Extract and sort all category names (useMemo cached)
  const categories = useMemo(
    () => orderCategories([...new Set(samples.map((s) => s.category))]),
    [samples],
  );

  /**
   * path+method → sample 的映射 / path+method → sample mapping
   *
   * 用于结果展示时查找 label 与跳转。
   * Used for looking up label and navigation in result display.
   */
  const sampleMap = useMemo(() => {
    const map = new Map<string, EndpointSample>(); // 初始化映射 / Initialize mapping
    for (const s of samples) map.set(`${s.method} ${s.path}`, s); // 以 "METHOD /path" 为键 / Key by "METHOD /path"
    return map;
  }, [samples]);

  // 根据选择的分类过滤目标端点 / Filter target endpoints by selected category
  const targets = useMemo(
    () => (category === ALL ? samples : samples.filter((s) => s.category === category)),
    [samples, category],
  );

  /**
   * 执行批量测试 / Execute Batch Test
   *
   * 将目标端点映射为 {method, path, body} 数组，
   * 调用 batchRequest API 由后端顺序执行，单个失败不中断。
   * Maps target endpoints to {method, path, body} array,
   * calls batchRequest API for backend sequential execution, single failure won't abort.
   */
  const handleRun = async () => {
    setRunning(true);  // 开启加载态 / Enable loading state
    setError(null);    // 清空错误 / Clear error
    setResult(null);   // 清空上次结果 / Clear previous result
    try {
      // 构建批量请求体 / Build batch request body
      const res = await batchRequest(
        targets.map((s) => ({
          method: s.method,
          path: s.path,
          body: s.body ?? null,
          raw_payload_b64: s.rawPayloadB64 ?? null,
          content_type: s.contentType ?? null,
        })),
      );
      setResult(res);  // 设置结果 / Set result
    } catch (e) {
      setError(getErrorMessage(e)); // 设置错误 / Set error
    } finally {
      setRunning(false); // 取消加载态 / Disable loading state
    }
  };

  // 计算通过率百分比 / Calculate pass rate percentage
  const passRate = result && result.total > 0 ? Math.round((result.passed / result.total) * 100) : 0;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-4xl px-8 py-10">
        {/* 标题区 */}
        <div className="mb-6">
          <h1 className="flex items-center gap-2 text-2xl font-bold text-gray-900">
            <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600">
              <Icon name="play" className="h-4 w-4" />
            </span>
            {t('batch.title')}
          </h1>
          <p className="mt-1.5 text-sm text-gray-500">
            {t('batch.subtitle')}
          </p>
        </div>

        {/* 控制区 */}
        <div className="flex flex-wrap items-center gap-3 rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
          <label className="flex items-center gap-2 text-sm text-gray-600">
            {t('batch.scope')}
            <select
              value={category}
              onChange={(e) => setCategory(e.target.value)}
              className="rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 text-sm text-gray-700 transition-colors focus:border-indigo-400 focus:outline-none focus:ring-2 focus:ring-indigo-100"
            >
              <option value={ALL}>{t('batch.all_categories', samples.length)}</option>
              {categories.map((c) => (
                <option key={c} value={c}>
                  {c}（{samples.filter((s) => s.category === c).length}）
                </option>
              ))}
            </select>
          </label>
          <ActionButton
            onClick={handleRun}
            loading={running}
            disabled={targets.length === 0}
            icon="play"
            loadingText={t('batch.running')}
          >
            {t('batch.start', targets.length)}
          </ActionButton>
        </div>

        {/* 错误提示 */}
        {error && (
          <div className="mt-4 flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-600">
            <Icon name="alert" className="h-4 w-4 shrink-0" />
            {error}
          </div>
        )}

        {/* 结果汇总 */}
        {result && (
          <div className="mt-6">
            <div className="flex items-center gap-4 rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
              <div className="flex items-center gap-2">
                <span
                  className={[
                    'flex h-10 w-10 items-center justify-center rounded-full text-sm font-bold',
                    result.failed === 0 ? 'bg-emerald-50 text-emerald-600' : 'bg-amber-50 text-amber-600',
                  ].join(' ')}
                >
                  {passRate}%
                </span>
                <div className="text-sm">
                  <div className="font-semibold text-gray-800">
                    {result.failed === 0 ? t('batch.all_passed') : t('batch.n_failed', result.failed)}
                  </div>
                  <div className="text-xs text-gray-400">
                    {t('batch.summary', result.total, result.passed, result.failed)}
                  </div>
                </div>
              </div>
            </div>

            {/* 结果明细表 */}
            <div className="mt-4 overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-gray-100 bg-gray-50/60 text-left text-xs uppercase tracking-wide text-gray-400">
                    <th className="px-4 py-2.5 font-semibold">{t('batch.col_status')}</th>
                    <th className="px-4 py-2.5 font-semibold">{t('batch.col_endpoint')}</th>
                    <th className="px-4 py-2.5 text-right font-semibold">{t('batch.col_duration')}</th>
                    <th className="px-4 py-2.5 font-semibold">{t('batch.col_info')}</th>
                  </tr>
                </thead>
                <tbody>
                  {result.results.map((r, i) => {
                    const sample = sampleMap.get(`${r.method} ${r.path}`);
                    const ok = r.status >= 200 && r.status < 300;
                    return (
                      <tr key={`${r.method}-${r.path}-${i}`} className="border-b border-gray-50 last:border-0">
                        <td className="px-4 py-2.5">
                          <span
                            className={[
                              'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-semibold',
                              ok ? 'bg-emerald-50 text-emerald-700' : 'bg-red-50 text-red-700',
                            ].join(' ')}
                          >
                            <span className={`h-1.5 w-1.5 rounded-full ${ok ? 'bg-emerald-500' : 'bg-red-500'}`} />
                            {r.status}
                          </span>
                        </td>
                        <td className="px-4 py-2.5">
                          <button
                            onClick={() => sample && onSelectSample(sample)}
                            className="group text-left"
                            title={t('batch.goto_endpoint')}
                          >
                            <div className="flex items-center gap-2">
                              <span
                                className={`w-10 shrink-0 rounded px-1 py-0.5 text-center text-[10px] font-bold ${
                                  r.method === 'GET' ? 'bg-emerald-50 text-emerald-600' : 'bg-sky-50 text-sky-600'
                                }`}
                              >
                                {r.method}
                              </span>
                              <span className="font-mono text-xs text-gray-700 group-hover:text-indigo-600">
                                {r.path}
                              </span>
                            </div>
                            {sample && <div className="mt-0.5 pl-12 text-xs text-gray-400">{sample.label}</div>}
                          </button>
                        </td>
                        <td className="px-4 py-2.5 text-right font-mono text-xs text-gray-500">
                          {r.duration_ms} ms
                        </td>
                        <td className="max-w-[240px] truncate px-4 py-2.5 text-xs text-gray-400" title={r.error ?? ''}>
                          {ok ? '—' : r.error}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {/* 空状态 */}
        {!result && !error && !running && (
          <div className="mt-10 flex flex-col items-center gap-3 text-gray-300">
            <Icon name="zap" className="h-12 w-12" strokeWidth={1.5} />
            <p className="text-sm text-gray-400">{t('batch.empty_hint')}</p>
          </div>
        )}
      </div>
    </div>
  );
}
