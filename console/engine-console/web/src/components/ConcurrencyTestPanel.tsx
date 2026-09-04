/**
 * 并发压测视图 / Concurrency Test View
 *
 * 用户配置目标路径、并发度与总请求数，运行后由后端以 asyncio.Semaphore
 * 控制并发发出请求，统计延迟分布（P50/P95/P99）、吞吐量（QPS）与成功率。
 * User configures target path, concurrency level and total requests;
 * backend uses asyncio.Semaphore to control concurrency, collecting
 * latency distribution (P50/P95/P99), throughput (QPS) and success rate.
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 左侧配置面板：目标路径 + 方法 + 请求体 + 并发数 + 总请求数；
 *   2. 点击"运行测试"调用 concurrencyTest API；
 *   3. 右侧结果面板：汇总卡片（QPS/成功率/总耗时）+ 延迟分布表格。
 */

import { useState } from 'react';
import type { ConcurrencyTestResponse } from '@/types/api';
import { concurrencyTest } from '@/api/client';
import { Icon } from '@/components/icons';
import { useI18n } from '@/i18n';
import { getErrorMessage } from '@/utils/error';

/** 默认请求体（mask 接口）/ Default request body (mask endpoint) */
const DEFAULT_MASK_BODY = {
  field_name: 'phone',
  value: '13812345678',
  context: 'user_profile',
};

/**
 * 预设路径及对应的默认请求体 / Preset paths with matching default bodies
 *
 * 每个 POST 预设必须携带与后端 Pydantic 模型匹配的请求体，
 * 否则压测会全部 422：
 * - mask: MaskRequest { field_name, value, context }
 * - dp_count / dp_sum: DPRequest { values, params }（sum 需 clip 边界）
 * - hash: HashRequest { value, salt }
 */
const PRESET_PATHS = [
  { path: '/v1/privacy/mask', method: 'POST', label: 'Mask PII', body: DEFAULT_MASK_BODY },
  {
    path: '/v1/privacy/dp/count',
    method: 'POST',
    label: 'DP Count',
    body: { values: [1, 2, 3, 4, 5], params: { epsilon: 1.0 } },
  },
  {
    path: '/v1/privacy/dp/sum',
    method: 'POST',
    label: 'DP Sum',
    body: { values: [1, 2, 3, 4, 5], params: { epsilon: 1.0, clip_lower: 0, clip_upper: 10 } },
  },
  {
    path: '/v1/privacy/hash',
    method: 'POST',
    label: 'Hash',
    body: { value: '13812345678', salt: 'test-salt' },
  },
  { path: '/health', method: 'GET', label: 'Health Check', body: null },
];

/**
 * ConcurrencyTestPanel 组件属性接口 / Props Interface
 */
interface ConcurrencyTestPanelProps {
  /** agent REST 地址（仅用于展示）/ Agent REST URL (display only) */
  agentUrl?: string;
}

/**
 * 并发压测主组件 / Concurrency Test Main Component
 */
export default function ConcurrencyTestPanel({ agentUrl }: ConcurrencyTestPanelProps) {
  const { t } = useI18n();

  /** 目标路径 / Target path */
  const [path, setPath] = useState('/v1/privacy/mask');
  /** HTTP 方法 / HTTP method */
  const [method, setMethod] = useState('POST');
  /** 请求体 JSON 文本 / Request body JSON text */
  const [bodyText, setBodyText] = useState(JSON.stringify(DEFAULT_MASK_BODY, null, 2));
  /** 并发数 / Concurrency level */
  const [concurrency, setConcurrency] = useState(50);
  /** 总请求数 / Total requests */
  const [totalRequests, setTotalRequests] = useState(200);
  /** 加载态 / Loading state */
  const [loading, setLoading] = useState(false);
  /** 测试结果 / Test result */
  const [result, setResult] = useState<ConcurrencyTestResponse | null>(null);
  /** 错误信息 / Error message */
  const [error, setError] = useState<string | null>(null);

  /** 选择预设路径 / Select preset path */
  const selectPreset = (preset: (typeof PRESET_PATHS)[number]) => {
    setPath(preset.path);
    setMethod(preset.method);
    // 始终同步为该预设匹配的默认请求体，避免残留其他接口的 body 导致 422
    setBodyText(preset.method === 'GET' || !preset.body ? '' : JSON.stringify(preset.body, null, 2));
  };

  /** 执行并发压测 / Execute concurrency test */
  const handleRun = async () => {
    setLoading(true);
    setError(null);
    setResult(null);

    let body: Record<string, unknown> | null = null;
    if (method !== 'GET' && bodyText.trim()) {
      try {
        body = JSON.parse(bodyText);
      } catch {
        setError(t('concurrency.invalid_json'));
        setLoading(false);
        return;
      }
    }

    try {
      const resp = await concurrencyTest({
        path,
        method,
        body,
        concurrency,
        total_requests: totalRequests,
      });
      setResult(resp);
    } catch (e) {
      setError(getErrorMessage(e));
    } finally {
      setLoading(false);
    }
  };

  /** 统一输入框样式 / Unified input style */
  const inputCls =
    'rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 text-sm text-gray-700 placeholder-gray-400 transition-colors focus:border-indigo-400 focus:bg-white focus:outline-none focus:ring-2 focus:ring-indigo-100';

  return (
    <div className="flex h-full">
      {/* 左侧：配置面板 / Left: Config Panel */}
      <div className="flex w-[380px] shrink-0 flex-col gap-4 overflow-y-auto border-r border-gray-200 bg-white p-5">
        <div>
          <h2 className="flex items-center gap-2 text-base font-semibold text-gray-800">
            <span className="flex h-6 w-6 items-center justify-center rounded bg-orange-50 text-orange-600">
              <Icon name="activity" className="h-3.5 w-3.5" />
            </span>
            {t('concurrency.title')}
          </h2>
          <p className="mt-1 text-xs text-gray-500">{t('concurrency.subtitle')}</p>
        </div>

        {/* 预设路径快捷选择 / Preset path quick selection */}
        <div>
          <label className="mb-1 block text-xs font-medium text-gray-600">
            {t('concurrency.presets')}
          </label>
          <div className="flex flex-wrap gap-1.5">
            {PRESET_PATHS.map((preset) => (
              <button
                key={preset.path}
                onClick={() => selectPreset(preset)}
                className={`rounded-md border px-2 py-1 text-xs transition-colors ${
                  path === preset.path
                    ? 'border-orange-300 bg-orange-50 font-medium text-orange-700'
                    : 'border-gray-200 text-gray-600 hover:border-gray-300 hover:bg-gray-50'
                }`}
              >
                {preset.label}
              </button>
            ))}
          </div>
        </div>

        {/* 目标路径 / Target path */}
        <div>
          <label className="mb-1 block text-xs font-medium text-gray-600">
            {t('concurrency.target_path')}
          </label>
          <div className="flex gap-2">
            <select
              value={method}
              onChange={(e) => setMethod(e.target.value)}
              className={`${inputCls} w-20 shrink-0`}
            >
              <option value="GET">GET</option>
              <option value="POST">POST</option>
            </select>
            <input
              value={path}
              onChange={(e) => setPath(e.target.value)}
              className={`${inputCls} flex-1`}
              placeholder="/v1/privacy/mask"
            />
          </div>
        </div>

        {/* 请求体（仅 POST）/ Request body (POST only) */}
        {method === 'POST' && (
          <div>
            <label className="mb-1 block text-xs font-medium text-gray-600">
              {t('concurrency.request_body')}
            </label>
            <textarea
              value={bodyText}
              onChange={(e) => setBodyText(e.target.value)}
              rows={5}
              className={`${inputCls} w-full resize-none font-mono text-xs`}
              placeholder='{"field_name": "phone", "value": "13812345678"}'
            />
          </div>
        )}

        {/* 并发数 / Concurrency */}
        <div>
          <label className="mb-1 block text-xs font-medium text-gray-600">
            {t('concurrency.concurrency')}
          </label>
          <input
            type="number"
            min={1}
            max={500}
            value={concurrency}
            onChange={(e) => {
              const v = Number(e.target.value);
              // 钳制到 [1, 500]，避免负数/超限值被直接提交（后端会 422）
              setConcurrency(Number.isNaN(v) ? 1 : Math.min(500, Math.max(1, v)));
            }}
            className={`${inputCls} w-full`}
          />
          <p className="mt-0.5 text-[11px] text-gray-400">{t('concurrency.concurrency_hint')}</p>
        </div>

        {/* 总请求数 / Total requests */}
        <div>
          <label className="mb-1 block text-xs font-medium text-gray-600">
            {t('concurrency.total_requests')}
          </label>
          <input
            type="number"
            min={1}
            max={5000}
            value={totalRequests}
            onChange={(e) => {
              const v = Number(e.target.value);
              // 钳制到 [1, 5000]，避免负数/超限值被直接提交（后端会 422）
              setTotalRequests(Number.isNaN(v) ? 1 : Math.min(5000, Math.max(1, v)));
            }}
            className={`${inputCls} w-full`}
          />
        </div>

        {/* 运行按钮 / Run button */}
        <button
          onClick={handleRun}
          disabled={loading}
          className="inline-flex items-center justify-center gap-2 rounded-lg bg-orange-600 px-4 py-2 text-sm font-medium text-white shadow-sm transition-colors hover:bg-orange-700 disabled:cursor-not-allowed disabled:opacity-60"
        >
          {loading ? (
            <>
              <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/30 border-t-white" />
              {t('concurrency.running')}
            </>
          ) : (
            <>
              <Icon name="play" className="h-4 w-4" />
              {t('concurrency.run')}
            </>
          )}
        </button>

        {/* agent 地址展示 / Agent URL display */}
        {agentUrl && (
          <p className="text-[11px] text-gray-400">
            {t('concurrency.target_agent')}: <code className="text-gray-500">{agentUrl}</code>
          </p>
        )}
      </div>

      {/* 右侧：结果面板 / Right: Result Panel */}
      <div className="flex flex-1 flex-col overflow-y-auto bg-gray-50 p-6">
        {error && (
          <div className="mb-4 rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700">
            {error}
          </div>
        )}

        {!result && !loading && (
          <div className="flex flex-1 flex-col items-center justify-center gap-3 text-gray-400">
            <Icon name="activity" className="h-10 w-10 text-gray-300" />
            <p className="text-sm">{t('concurrency.empty_hint')}</p>
          </div>
        )}

        {loading && (
          <div className="flex flex-1 flex-col items-center justify-center gap-3 text-gray-400">
            <span className="h-8 w-8 animate-spin rounded-full border-2 border-gray-200 border-t-orange-500" />
            <p className="text-sm">{t('concurrency.running')}</p>
          </div>
        )}

        {result && !loading && (() => {
          const maxLatency = Math.max(result.max_latency_ms, 1);
          return (
            <div className="space-y-6">
              {/* 汇总卡片 / Summary cards */}
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
                <SummaryCard label={t('concurrency.qps')} value={result.qps.toFixed(1)} unit="req/s" color="orange" />
                <SummaryCard
                  label={t('concurrency.success_rate')}
                  value={((result.success / result.total) * 100).toFixed(1)}
                  unit="%"
                  color="green"
                />
                <SummaryCard label={t('concurrency.total_time')} value={result.duration_ms.toFixed(0)} unit="ms" color="blue" />
                <SummaryCard
                  label={t('concurrency.success_failed')}
                  value={`${result.success}/${result.failed}`}
                  unit=""
                  color="gray"
                />
              </div>

              {/* 延迟分布 / Latency distribution */}
              <div className="rounded-xl border border-gray-200 bg-white p-5">
                <div className="mb-4 flex items-center justify-between">
                  <h3 className="text-sm font-semibold text-gray-800">
                    {t('concurrency.latency_distribution')}
                  </h3>
                  <span className="text-xs text-gray-400">
                    {t('concurrency.concurrency_hint')}
                  </span>
                </div>
                <div className="space-y-3">
                  <LatencyRow
                    label="P50"
                    desc={t('concurrency.desc.p50')}
                    value={result.p50_latency_ms}
                    max={maxLatency}
                    color="bg-green-500"
                  />
                  {result.p90_latency_ms !== undefined && (
                    <LatencyRow
                      label="P90"
                      desc={t('concurrency.desc.p90')}
                      value={result.p90_latency_ms}
                      max={maxLatency}
                      color="bg-lime-500"
                    />
                  )}
                  <LatencyRow
                    label="P95"
                    desc={t('concurrency.desc.p95')}
                    value={result.p95_latency_ms}
                    max={maxLatency}
                    color="bg-yellow-500"
                  />
                  <LatencyRow
                    label="P99"
                    desc={t('concurrency.desc.p99')}
                    value={result.p99_latency_ms}
                    max={maxLatency}
                    color="bg-red-500"
                  />
                  <LatencyRow
                    label={t('concurrency.avg')}
                    desc={t('concurrency.desc.avg')}
                    value={result.avg_latency_ms}
                    max={maxLatency}
                    color="bg-blue-500"
                  />
                  <LatencyRow
                    label={t('concurrency.min')}
                    desc={t('concurrency.desc.min')}
                    value={result.min_latency_ms}
                    max={maxLatency}
                    color="bg-teal-400"
                  />
                  <LatencyRow
                    label={t('concurrency.max')}
                    desc={t('concurrency.desc.max')}
                    value={result.max_latency_ms}
                    max={maxLatency}
                    color="bg-purple-500"
                  />
                </div>
              </div>

              {/* 延迟统计与含义解释表格 / Latency stats & explanation table */}
              <div className="overflow-hidden rounded-xl border border-gray-200 bg-white">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-gray-100 bg-gray-50">
                      <th className="px-4 py-2.5 text-left text-xs font-medium text-gray-500">{t('concurrency.metric')}</th>
                      <th className="px-4 py-2.5 text-left text-xs font-medium text-gray-500">{t('concurrency.meaning')}</th>
                      <th className="px-4 py-2.5 text-right text-xs font-medium text-gray-500">{t('concurrency.value_ms')}</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-50">
                    <tr>
                      <td className="px-4 py-2.5 font-medium text-gray-800">
                        <span className="inline-flex items-center rounded bg-green-50 px-1.5 py-0.5 text-xs font-semibold text-green-700">P50</span>
                      </td>
                      <td className="px-4 py-2.5 text-xs text-gray-600">{t('concurrency.desc.p50')}</td>
                      <td className="px-4 py-2.5 text-right font-mono text-gray-800">{result.p50_latency_ms.toFixed(2)} ms</td>
                    </tr>
                    {result.p90_latency_ms !== undefined && (
                      <tr>
                        <td className="px-4 py-2.5 font-medium text-gray-800">
                          <span className="inline-flex items-center rounded bg-lime-50 px-1.5 py-0.5 text-xs font-semibold text-lime-700">P90</span>
                        </td>
                        <td className="px-4 py-2.5 text-xs text-gray-600">{t('concurrency.desc.p90')}</td>
                        <td className="px-4 py-2.5 text-right font-mono text-gray-800">{result.p90_latency_ms.toFixed(2)} ms</td>
                      </tr>
                    )}
                    <tr>
                      <td className="px-4 py-2.5 font-medium text-gray-800">
                        <span className="inline-flex items-center rounded bg-yellow-50 px-1.5 py-0.5 text-xs font-semibold text-yellow-700">P95</span>
                      </td>
                      <td className="px-4 py-2.5 text-xs text-gray-600">{t('concurrency.desc.p95')}</td>
                      <td className="px-4 py-2.5 text-right font-mono text-gray-800">{result.p95_latency_ms.toFixed(2)} ms</td>
                    </tr>
                    <tr>
                      <td className="px-4 py-2.5 font-medium text-gray-800">
                        <span className="inline-flex items-center rounded bg-red-50 px-1.5 py-0.5 text-xs font-semibold text-red-700">P99</span>
                      </td>
                      <td className="px-4 py-2.5 text-xs text-gray-600">{t('concurrency.desc.p99')}</td>
                      <td className="px-4 py-2.5 text-right font-mono text-gray-800">{result.p99_latency_ms.toFixed(2)} ms</td>
                    </tr>
                    <tr>
                      <td className="px-4 py-2.5 font-medium text-gray-800">
                        <span className="inline-flex items-center rounded bg-blue-50 px-1.5 py-0.5 text-xs font-semibold text-blue-700">{t('concurrency.avg')}</span>
                      </td>
                      <td className="px-4 py-2.5 text-xs text-gray-600">{t('concurrency.desc.avg')}</td>
                      <td className="px-4 py-2.5 text-right font-mono text-gray-800">{result.avg_latency_ms.toFixed(2)} ms</td>
                    </tr>
                    <tr>
                      <td className="px-4 py-2.5 font-medium text-gray-800">
                        <span className="inline-flex items-center rounded bg-teal-50 px-1.5 py-0.5 text-xs font-semibold text-teal-700">{t('concurrency.min')}</span>
                      </td>
                      <td className="px-4 py-2.5 text-xs text-gray-600">{t('concurrency.desc.min')}</td>
                      <td className="px-4 py-2.5 text-right font-mono text-gray-800">{result.min_latency_ms.toFixed(2)} ms</td>
                    </tr>
                    <tr>
                      <td className="px-4 py-2.5 font-medium text-gray-800">
                        <span className="inline-flex items-center rounded bg-purple-50 px-1.5 py-0.5 text-xs font-semibold text-purple-700">{t('concurrency.max')}</span>
                      </td>
                      <td className="px-4 py-2.5 text-xs text-gray-600">{t('concurrency.desc.max')}</td>
                      <td className="px-4 py-2.5 text-right font-mono text-gray-800">{result.max_latency_ms.toFixed(2)} ms</td>
                    </tr>
                  </tbody>
                </table>
              </div>

              {/* 指标名词详细指南卡片 / Metrics & Percentiles Guide Card */}
              <MetricsGuideCard />
            </div>
          );
        })()}
      </div>
    </div>
  );
}

/** 指标名词解释与调优指南卡片 / Metrics & Percentiles Guide Card */
function MetricsGuideCard() {
  const { t } = useI18n();
  const [open, setOpen] = useState(true);

  return (
    <div className="rounded-xl border border-indigo-100 bg-indigo-50/40 p-4 transition-all">
      <button
        onClick={() => setOpen(!open)}
        className="flex w-full items-center justify-between text-left text-xs font-semibold text-indigo-900"
      >
        <span className="flex items-center gap-1.5">
          <span className="text-indigo-600">💡</span>
          {t('concurrency.guide.title')}
        </span>
        <span className="text-indigo-500 hover:text-indigo-700">
          {open ? '▲ 收起' : '▼ 展开查看'}
        </span>
      </button>

      {open && (
        <div className="mt-3 space-y-3 text-xs leading-relaxed text-indigo-950/80">
          <div className="rounded-lg bg-white/80 p-3 shadow-sm border border-indigo-100/60">
            <h4 className="font-semibold text-indigo-900 mb-1">
              📌 {t('concurrency.guide.what_is_p')}
            </h4>
            <p className="text-gray-600">{t('concurrency.guide.what_is_p_desc')}</p>
          </div>

          <div className="rounded-lg bg-white/80 p-3 shadow-sm border border-indigo-100/60">
            <h4 className="font-semibold text-indigo-900 mb-1">
              ⚖️ {t('concurrency.guide.why_not_avg')}
            </h4>
            <p className="text-gray-600">{t('concurrency.guide.why_not_avg_desc')}</p>
          </div>

          <div className="rounded-lg bg-white/80 p-3 shadow-sm border border-indigo-100/60">
            <h4 className="font-semibold text-indigo-900 mb-1">
              🚀 {t('concurrency.guide.how_to_use')}
            </h4>
            <p className="whitespace-pre-line text-gray-600">{t('concurrency.guide.how_to_use_desc')}</p>
          </div>
        </div>
      )}
    </div>
  );
}

/** 汇总卡片 / Summary Card */
function SummaryCard({ label, value, unit, color }: { label: string; value: string; unit: string; color: string }) {
  const colorMap: Record<string, string> = {
    orange: 'border-orange-200 bg-orange-50 text-orange-700',
    green: 'border-green-200 bg-green-50 text-green-700',
    blue: 'border-blue-200 bg-blue-50 text-blue-700',
    gray: 'border-gray-200 bg-gray-50 text-gray-700',
  };
  return (
    <div className={`rounded-xl border p-4 ${colorMap[color] || colorMap.gray}`}>
      <p className="text-xs font-medium opacity-70">{label}</p>
      <p className="mt-1 text-2xl font-bold">
        {value}
        {unit && <span className="ml-1 text-xs font-normal opacity-60">{unit}</span>}
      </p>
    </div>
  );
}

/** 延迟条形图行 / Latency bar row */
function LatencyRow({
  label,
  desc,
  value,
  max,
  color,
}: {
  label: string;
  desc?: string;
  value: number;
  max: number;
  color: string;
}) {
  const pct = max > 0 ? Math.max(2, (value / max) * 100) : 2;
  return (
    <div className="flex items-center gap-3">
      <div className="w-12 shrink-0">
        <span className="text-xs font-semibold text-gray-700" title={desc}>{label}</span>
      </div>
      <div className="flex-1" title={desc ? `${label}: ${desc}` : undefined}>
        <div className="h-5 w-full overflow-hidden rounded-full bg-gray-100">
          <div
            className={`h-full rounded-full ${color} transition-all duration-500`}
            style={{ width: `${pct}%` }}
          />
        </div>
      </div>
      <span className="w-20 text-right font-mono text-xs text-gray-700">{value.toFixed(2)} ms</span>
    </div>
  );
}
