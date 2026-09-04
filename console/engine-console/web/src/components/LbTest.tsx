/**
 * 负载均衡测试视图 / Load Balancer Test View
 *
 * 用户配置多个 agent 后端地址（name + url，默认用 health.agent_url 预填一行），
 * 设置请求数与分发策略，运行后由后端按策略分发探测请求，
 * 以表格 + 简易条形可视化展示各节点的命中数、成功率与平均延迟。
 * User configures multiple agent backend addresses (name + url, pre-filled with health.agent_url),
 * sets request count and distribution strategy, backend distributes probe requests by strategy,
 * displays hits, success rate and avg latency per node via table + simple bar visualization.
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 左侧配置面板：后端节点列表（可增删）+ 请求数 + 策略选择；
 *   2. 点击“运行测试”调用 lbTest API；
 *   3. 右侧结果面板：汇总卡片（总请求/成功/失败/总耗时）+ 分发结果表格；
 *   4. 表格中每行显示节点名、命中分布条形图、命中数、成功率、平均/最小/最大延迟。
 *   1. Left config panel: backend node list (add/remove) + request count + strategy select;
 *   2. Click "Run Test" calls lbTest API;
 *   3. Right result panel: summary cards (total/success/failed/duration) + distribution table;
 *   4. Each table row shows node name, hit distribution bar, hits, success rate, avg/min/max latency.
 */

/** 引入 React Hooks / Import React Hooks */
import { useEffect, useState } from 'react';
/** 引入类型定义 / Import type definitions */
import type { LbBackend, LbStrategy, LbTestResponse } from '@/types/api';
/** 引入负载均衡测试 API / Import load balancer test API */
import { lbTest } from '@/api/client';
/** 引入图标组件 / Import icon component */
import { Icon } from '@/components/icons';
/** 引入通用动作按钮 / Import generic action button */
import ActionButton from '@/components/ActionButton';
/** 引入国际化 Hook / Import i18n Hook */
import { useI18n } from '@/i18n';
/** 引入统一错误消息提取工具 / Import unified error message extraction utility */
import { getErrorMessage } from '@/utils/error';

/**
 * 策略选项列表（渲染时通过 i18n 解析标签）/ Strategy option list (labels resolved via i18n at render time)
 */
const STRATEGY_KEYS: { value: LbStrategy; i18nKey: string }[] = [
  { value: 'round_robin', i18nKey: 'lb.strategy_round_robin' },       // 轮询 / Round Robin
  { value: 'random', i18nKey: 'lb.strategy_random' },                 // 随机 / Random
  { value: 'least_connections', i18nKey: 'lb.strategy_least_conn' },  // 最少连接 / Least Connections
];

/**
 * LbTest 组件属性接口 / LbTest Component Props Interface
 */
interface LbTestProps {
  /** agent REST 地址，用于预填第一个后端节点 / Agent REST URL, used to pre-fill first backend node */
  agentUrl?: string;
}

/**
 * 负载均衡测试主组件 / Load Balancer Test Main Component
 *
 * 左侧配置后端节点、请求数、策略；右侧展示测试结果。
 * Left side configures backend nodes, request count, strategy; right side displays test results.
 */
export default function LbTest({ agentUrl }: LbTestProps) {
  const { t } = useI18n(); // 获取翻译函数 / Get translation function
  /** 后端节点列表（初始预填一个）/ Backend node list (pre-filled with one) */
  const [backends, setBackends] = useState<LbBackend[]>([
    { name: 'agent-1', url: agentUrl || 'http://127.0.0.1:8079' },
  ]);
  /** 探测请求数 / Number of probe requests */
  const [numRequests, setNumRequests] = useState(20);
  /** 分发策略 / Distribution strategy */
  const [strategy, setStrategy] = useState<LbStrategy>('round_robin');

  /** 加载中标记 / Loading flag */
  const [loading, setLoading] = useState(false);
  /** 测试结果 / Test result */
  const [result, setResult] = useState<LbTestResponse | null>(null);
  /** 错误信息 / Error message */
  const [error, setError] = useState<string | null>(null);

  /**
   * agentUrl 变化时同步更新第一行默认值 / Sync update first row default when agentUrl changes
   *
   * 仅当只有一行且仍为默认值时才更新，避免覆盖用户已编辑的内容。
   * Only updates when there's one row and it's still the default, avoiding overwriting user edits.
   */
  useEffect(() => {
    if (agentUrl) {
      setBackends((prev) => {
        // 仅一行且为默认 URL 时同步 / Only sync when one row with default URL
        if (prev.length === 1 && (prev[0].url === 'http://127.0.0.1:8079' || prev[0].url === '')) {
          return [{ ...prev[0], url: agentUrl }];
        }
        return prev; // 否则不变 / Otherwise unchanged
      });
    }
  }, [agentUrl]);

  /** 更新指定索引后端节点的字段 / Update fields of backend node at specified index */
  const updateBackend = (idx: number, patch: Partial<LbBackend>) => {
    setBackends((prev) => prev.map((b, i) => (i === idx ? { ...b, ...patch } : b)));
  };
  /** 添加新后端节点 / Add new backend node */
  const addBackend = () => {
    setBackends((prev) => [...prev, { name: `agent-${prev.length + 1}`, url: agentUrl || 'http://127.0.0.1:8079' }]);
  };
  /** 删除指定索引的后端节点（至少保留一个）/ Remove backend node at index (keep at least one) */
  const removeBackend = (idx: number) => {
    setBackends((prev) => (prev.length > 1 ? prev.filter((_, i) => i !== idx) : prev));
  };

  /**
   * 执行负载均衡测试 / Execute Load Balancer Test
   *
   * 校验至少有一个有效后端地址，然后调用 lbTest API。
   * Validates at least one valid backend address, then calls lbTest API.
   */
  const handleRun = async () => {
    const valid = backends.filter((b) => b.url.trim()); // 过滤空 URL / Filter empty URLs
    if (valid.length === 0) {
      setError(t('lb.at_least_one')); // 提示至少一个地址 / Prompt at least one address
      return;
    }
    setLoading(true);  // 开启加载态 / Enable loading
    setError(null);    // 清空错误 / Clear error
    setResult(null);   // 清空结果 / Clear result
    try {
      const resp = await lbTest({
        backends: valid,        // 有效后端列表 / Valid backend list
        num_requests: numRequests, // 请求数 / Request count
        strategy,               // 分发策略 / Distribution strategy
      });
      setResult(resp); // 设置结果 / Set result
    } catch (e) {
      setError(getErrorMessage(e)); // 设置错误 / Set error
    } finally {
      setLoading(false); // 取消加载态 / Disable loading
    }
  };

  /** 统一输入框样式类 / Unified input field style class */
  const inputCls =
    'rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 text-sm text-gray-700 placeholder-gray-400 transition-colors focus:border-indigo-400 focus:bg-white focus:outline-none focus:ring-2 focus:ring-indigo-100';

  // 条形可视化的最大命中数基准（用于计算百分比宽度）/ Max hit count baseline for bar visualization (for percentage width calculation)
  const maxCount = result ? Math.max(1, ...result.distribution.map((d) => d.count)) : 1;

  return (
    <div className="flex h-full">
      {/* 左侧：配置 */}
      <div className="flex w-[380px] shrink-0 flex-col gap-4 overflow-y-auto border-r border-gray-200 bg-white p-5">
        <div>
          <h2 className="flex items-center gap-2 text-base font-semibold text-gray-800">
            <span className="flex h-6 w-6 items-center justify-center rounded bg-indigo-50 text-indigo-600">
              <Icon name="scale" className="h-3.5 w-3.5" />
            </span>
            {t('lb.title')}
          </h2>
          <p className="mt-1 text-xs text-gray-500">{t('lb.subtitle')}</p>
        </div>

        {/* 后端列表 */}
        <div>
          <div className="mb-1 flex items-center justify-between">
            <label className="text-xs font-medium text-gray-600">{t('lb.backends')}</label>
            <button
              onClick={addBackend}
              className="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-xs text-indigo-600 transition-colors hover:bg-indigo-50"
            >
              <Icon name="copy" className="h-3 w-3" />
              {t('lb.add_node')}
            </button>
          </div>
          <div className="space-y-2">
            {backends.map((b, idx) => (
              <div key={idx} className="flex items-center gap-2">
                <input
                  value={b.name}
                  onChange={(e) => updateBackend(idx, { name: e.target.value })}
                  className={`${inputCls} w-24 shrink-0`}
                  placeholder={t('lb.name_placeholder')}
                />
                <input
                  value={b.url}
                  onChange={(e) => updateBackend(idx, { url: e.target.value })}
                  className={`${inputCls} flex-1`}
                  placeholder="http://127.0.0.1:8079"
                />
                <button
                  onClick={() => removeBackend(idx)}
                  disabled={backends.length <= 1}
                  className="shrink-0 rounded-md p-1.5 text-gray-400 transition-colors hover:bg-red-50 hover:text-red-500 disabled:cursor-not-allowed disabled:opacity-40"
                  title={t('lb.delete_node')}
                  aria-label={t('lb.delete_node')}
                >
                  <Icon name="trash" className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}
          </div>
        </div>

        {/* 请求数 */}
        <div>
          <label className="mb-1 block text-xs font-medium text-gray-600">{t('lb.num_requests')}</label>
          <input
            type="number"
            min={1}
            max={1000}
            value={numRequests}
            onChange={(e) => setNumRequests(Math.min(1000, Math.max(1, Number(e.target.value) || 1)))}
            className={`${inputCls} w-full`}
          />
        </div>

        {/* 策略 */}
        <div>
          <label className="mb-1 block text-xs font-medium text-gray-600">{t('lb.strategy')}</label>
          <select
            value={strategy}
            onChange={(e) => setStrategy(e.target.value as LbStrategy)}
            className={`${inputCls} w-full`}
          >
            {STRATEGY_KEYS.map((s) => (
              <option key={s.value} value={s.value}>
                {t(s.i18nKey)}
              </option>
            ))}
          </select>
        </div>

        <ActionButton
          onClick={handleRun}
          loading={loading}
          icon="play"
          loadingText={t('lb.running')}
        >
          {t('lb.run')}
        </ActionButton>
      </div>

      {/* 右侧：结果 */}
      <div className="flex-1 overflow-y-auto p-5">
        {error && (
          <div className="mb-4 flex items-center gap-2 rounded-lg border border-red-100 bg-red-50 px-4 py-3 text-sm text-red-600">
            <Icon name="alert" className="h-4 w-4 shrink-0" />
            {error}
          </div>
        )}

        {!result && !error && (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-gray-300">
            <Icon name="scale" className="h-10 w-10" strokeWidth={1.5} />
            <p className="text-sm text-gray-400">{t('lb.empty_hint')}</p>
          </div>
        )}

        {result && (
          <div className="space-y-5">
            {/* 汇总卡片 */}
            <div className="grid grid-cols-4 gap-3">
              <SummaryCard label={t('lb.total_requests')} value={result.total} tone="text-gray-800" />
              <SummaryCard label={t('lb.success')} value={result.success} tone="text-emerald-600" />
              <SummaryCard label={t('lb.failed')} value={result.failed} tone="text-red-500" />
              <SummaryCard label={t('lb.total_duration')} value={`${result.duration_ms.toFixed(1)} ms`} tone="text-indigo-600" />
            </div>

            {/* 分发结果表 */}
            <div className="overflow-hidden rounded-xl border border-gray-200 bg-white">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-gray-100 bg-gray-50 text-left text-xs text-gray-500">
                    <th className="px-4 py-2 font-medium">{t('lb.col_node')}</th>
                    <th className="px-4 py-2 font-medium">{t('lb.col_distribution')}</th>
                    <th className="px-4 py-2 text-right font-medium">{t('lb.col_hits')}</th>
                    <th className="px-4 py-2 text-right font-medium">{t('lb.col_success_rate')}</th>
                    <th className="px-4 py-2 text-right font-medium">{t('lb.col_avg_latency')}</th>
                    <th className="px-4 py-2 text-right font-medium">{t('lb.col_min_max_latency')}</th>
                  </tr>
                </thead>
                <tbody>
                  {result.distribution.map((d, i) => {
                    const rate = d.count > 0 ? (d.success / d.count) * 100 : 0;
                    return (
                      <tr key={i} className="border-b border-gray-50 last:border-0 hover:bg-indigo-50/30">
                        <td className="px-4 py-2.5">
                          <div className="font-medium text-gray-800">{d.name}</div>
                          <div className="text-xs text-gray-400">{d.url}</div>
                        </td>
                        <td className="px-4 py-2.5">
                          <div className="h-2.5 w-full overflow-hidden rounded-full bg-gray-100">
                            <div
                              className="h-full rounded-full bg-indigo-500"
                              style={{ width: `${(d.count / maxCount) * 100}%` }}
                            />
                          </div>
                        </td>
                        <td className="px-4 py-2.5 text-right font-medium text-gray-700">{d.count}</td>
                        <td className="px-4 py-2.5 text-right">
                          <span className={rate === 100 ? 'text-emerald-600' : rate > 0 ? 'text-amber-600' : 'text-red-500'}>
                            {rate.toFixed(0)}%
                          </span>
                        </td>
                        <td className="px-4 py-2.5 text-right text-gray-700">{d.avg_latency_ms.toFixed(2)} ms</td>
                        <td className="px-4 py-2.5 text-right text-xs text-gray-500">
                          {d.min_latency_ms.toFixed(2)} / {d.max_latency_ms.toFixed(2)} ms
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

/**
 * 汇总小卡片组件 / Summary Card Component
 *
 * 展示单个统计指标（标签 + 数值），用于结果面板顶部的四宫格。
 * Displays a single metric (label + value), used in the 4-grid at result panel top.
 *
 * @param label - 指标标签 / Metric label
 * @param value - 指标值 / Metric value
 * @param tone - 数值颜色类 / Value color class
 */
function SummaryCard({ label, value, tone }: { label: string; value: number | string; tone: string }) {
  return (
    <div className="rounded-xl border border-gray-200 bg-white px-4 py-3">
      <div className="text-xs text-gray-400">{label}</div>
      <div className={`mt-1 text-xl font-semibold ${tone}`}>{value}</div>
    </div>
  );
}
