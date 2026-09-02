/**
 * MetricsPanel — 实时性能指标与分位数监控大屏。
 *
 * 功能概述：
 *  1. 展示实时 QPS、6 阶段平均耗时瀑布图
 *  2. 展示 P50/P90/P95/P99 延迟分位数（带说明）
 *  3. 支持查看 Prometheus 原始文本指标
 *  4. 内置前端压测工具（可指定并发数和 API 目标）
 *
 * 数据来源：
 *  - metricsRaw: Prometheus 原始文本（BFF GET /metrics）
 *  - parsedMetrics: BFF 解析后的结构化指标（含 histogram 线性插值 P50/P90/P95/P99）
 *
 * 压测工具：
 *  - 使用 Promise.all + AbortController 实现并发压测
 *  - 计算实际 QPS、成功率、P50/P90/P95/P99 延迟
 */
import React, { useState, useRef, useCallback } from 'react';
import { useI18n } from '../i18n';
import { api } from '../api/client';
import {
  IconSparkles,
  IconRefresh,
  IconPlay,
  IconCheckCircle,
} from './icons';

/** BFF 解析后的结构化指标 */
interface ParsedMetrics {
  stage_durations: Record<string, number>;
  qps: number;
  percentiles: Record<string, number>;
  total_requests: number;
  source: string;
}

/** MetricsPanel 组件的 Props */
interface MetricsPanelProps {
  /** Prometheus 原始文本 */
  metricsRaw: string;
  /** 解析后的结构化指标 */
  parsedMetrics: ParsedMetrics | null;
  /** 刷新指标回调 */
  onRefreshMetrics: () => Promise<void>;
  /** 是否正在加载中 */
  loading: boolean;
}

/** 前端压测结果 */
interface StressTestResult {
  totalRequests: number;
  successCount: number;
  failCount: number;
  durationMs: number;
  qps: number;
  p50: number;
  p90: number;
  p95: number;
  p99: number;
}

export const MetricsPanel: React.FC<MetricsPanelProps> = ({
  metricsRaw,
  parsedMetrics,
  onRefreshMetrics,
  loading,
}) => {
  const { t } = useI18n();
  const [showRaw, setShowRaw] = useState(false);
  const [stressConcurrency, setStressConcurrency] = useState(5);
  const [stressApiId, setStressApiId] = useState(1);
  const [stressResult, setStressResult] = useState<StressTestResult | null>(null);
  const [stressRunning, setStressRunning] = useState(false);
  const abortRef = useRef(false);

  const metricsSource = parsedMetrics?.source ?? 'fallback';

  const handleStressTest = useCallback(async () => {
    abortRef.current = false;
    setStressRunning(true);
    setStressResult(null);

    const latencies: number[] = [];
    let successCount = 0;
    let failCount = 0;
    const totalRequests = stressConcurrency;
    const startAll = performance.now();

    const promises = Array.from({ length: totalRequests }, async (_, i) => {
      if (abortRef.current) return;
      const t0 = performance.now();
      try {
        await api.invokeDataApi(stressApiId, '110101196809171010');
        latencies.push(performance.now() - t0);
        successCount++;
      } catch {
        latencies.push(performance.now() - t0);
        failCount++;
      }
    });

    await Promise.all(promises);
    const totalDuration = performance.now() - startAll;

    latencies.sort((a, b) => a - b);
    const n = latencies.length;
    const p50 = n > 0 ? latencies[Math.floor(n * 0.50)] : 0;
    const p90 = n > 0 ? latencies[Math.floor(n * 0.90)] : 0;
    const p95 = n > 0 ? latencies[Math.floor(n * 0.95)] : 0;
    const p99 = n > 0 ? latencies[Math.floor(n * 0.99)] : 0;
    const qps = totalDuration > 0 ? (n / totalDuration) * 1000 : 0;

    setStressResult({
      totalRequests: n,
      successCount,
      failCount,
      durationMs: Math.round(totalDuration),
      qps: Math.round(qps * 10) / 10,
      p50: Math.round(p50 * 10) / 10,
      p90: Math.round(p90 * 10) / 10,
      p95: Math.round(p95 * 10) / 10,
      p99: Math.round(p99 * 10) / 10,
    });
    setStressRunning(false);
  }, [stressConcurrency, stressApiId]);

  return (
    <div className="space-y-6">
      {/* Banner */}
      <div className="bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-xl flex flex-col md:flex-row items-start md:items-center justify-between gap-4">
        <div>
          <div className="flex items-center gap-2.5">
            <span className="p-2 rounded-xl bg-purple-500/10 border border-purple-500/20 text-purple-400">
              <IconSparkles className="w-6 h-6" />
            </span>
            <h1 className="text-xl font-bold text-slate-100">{t('metrics.title')}</h1>
          </div>
          <p className="text-sm text-slate-400 mt-1 max-w-2xl">预设数据 API 性能指标与压力测试</p>
        </div>

        <div className="flex items-center gap-3">
          <span className={`text-[10px] font-mono px-2 py-1 rounded-full border ${
            metricsSource === 'prometheus'
              ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/30'
              : 'bg-amber-500/10 text-amber-400 border-amber-500/30'
          }`}>
            {metricsSource === 'prometheus' ? '● LIVE Prometheus' : '○ Fallback Defaults'}
          </span>

          <button
            onClick={() => setShowRaw(!showRaw)}
            className="px-3.5 py-2.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs font-semibold border border-slate-700 transition"
          >
            {showRaw ? '隐藏 Prometheus' : '查看 Prometheus'}
          </button>

          <button
            onClick={onRefreshMetrics}
            disabled={loading}
            className="flex items-center gap-2 px-4 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-medium text-xs shadow-lg shadow-indigo-600/30 transition disabled:opacity-50"
          >
            <IconRefresh className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
            <span>刷新</span>
          </button>
        </div>
      </div>

      {/* Stress Test Section */}
      <div className="bg-gradient-to-br from-purple-950/30 via-slate-900 to-slate-900 border border-purple-500/30 rounded-2xl p-6 shadow-xl">
        <div className="flex items-center justify-between border-b border-purple-500/20 pb-3 mb-4">
          <div className="flex items-center gap-2">
            <IconPlay className="w-5 h-5 text-purple-400" />
            <h2 className="text-sm font-bold text-slate-100">预设数据 API 压力测试</h2>
            <span className="text-[10px] font-mono px-2 py-0.5 rounded bg-purple-500/20 text-purple-300 border border-purple-500/30">
              并发发送多个 InvokeDataApi 请求
            </span>
          </div>
        </div>

        <div className="flex flex-wrap items-end gap-4 mb-4">
          <div>
            <label className="text-xs text-slate-400 font-medium mb-1 block">目标 API</label>
            <select
              value={stressApiId}
              onChange={(e) => setStressApiId(Number(e.target.value))}
              className="bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-xs text-slate-200 focus:outline-none focus:border-purple-500"
            >
              <option value={1}>API 1 — 医保结算</option>
              <option value={2}>API 2 — 康养体征</option>
            </select>
          </div>
          <div>
            <label className="text-xs text-slate-400 font-medium mb-1 block">并发数</label>
            <input
              type="number"
              value={stressConcurrency}
              onChange={(e) => setStressConcurrency(Number(e.target.value))}
              min={1}
              max={50}
              className="w-20 bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-xs text-slate-200 font-mono focus:outline-none focus:border-purple-500"
            />
          </div>
          <button
            onClick={handleStressTest}
            disabled={stressRunning}
            className="flex items-center gap-2 px-5 py-2.5 rounded-xl bg-purple-600 hover:bg-purple-500 text-white font-bold text-sm transition-all shadow-lg shadow-purple-600/30 disabled:opacity-50"
          >
            {stressRunning ? (
              <><IconRefresh className="w-4 h-4 animate-spin" /><span>压测中...</span></>
            ) : (
              <><IconPlay className="w-4 h-4" /><span>开始压测</span></>
            )}
          </button>
        </div>

        {/* Stress Test Results */}
        {stressResult && (
          <div className="space-y-4">
            {/* Summary Cards */}
            <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
              <div className="bg-slate-950 border border-slate-800 rounded-xl p-4">
                <div className="text-[10px] text-slate-500 uppercase mb-1">吞吐量 QPS</div>
                <div className="text-2xl font-bold font-mono text-cyan-400">{stressResult.qps}</div>
                <div className="text-[10px] text-slate-500 mt-1">
                  {stressResult.successCount}/{stressResult.totalRequests} 成功
                </div>
              </div>
              <div className="bg-slate-950 border border-slate-800 rounded-xl p-4">
                <div className="text-[10px] text-slate-500 uppercase mb-1">总耗时</div>
                <div className="text-2xl font-bold font-mono text-emerald-400">{stressResult.durationMs}</div>
                <div className="text-[10px] text-slate-500 mt-1">ms (wall clock)</div>
              </div>
              <div className="bg-slate-950 border border-slate-800 rounded-xl p-4">
                <div className="text-[10px] text-slate-500 uppercase mb-1">成功 / 失败</div>
                <div className="text-2xl font-bold font-mono">
                  <span className="text-emerald-400">{stressResult.successCount}</span>
                  <span className="text-slate-600"> / </span>
                  <span className="text-rose-400">{stressResult.failCount}</span>
                </div>
                <div className="text-[10px] text-slate-500 mt-1">共 {stressResult.totalRequests} 请求</div>
              </div>
              <div className="bg-slate-950 border border-slate-800 rounded-xl p-4">
                <div className="text-[10px] text-slate-500 uppercase mb-1">并发数</div>
                <div className="text-2xl font-bold font-mono text-purple-400">{stressConcurrency}</div>
                <div className="text-[10px] text-slate-500 mt-1">同时发出</div>
              </div>
            </div>

            {/* Latency Percentiles */}
            <div>
              <div className="flex items-center gap-2 mb-3">
                <h3 className="text-xs font-semibold text-slate-300">延迟分位数分布</h3>
                <span className="text-[10px] text-slate-500">| P50=中位延迟 P90=90%请求低于此值 P95=95%请求低于此值 P99=尾部延迟</span>
              </div>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                {[
                  { label: 'P50', value: stressResult.p50, color: 'text-emerald-400', desc: '中位延迟 — 50% 请求低于此值，反映典型用户体验' },
                  { label: 'P90', value: stressResult.p90, color: 'text-indigo-400', desc: '90% 分位 — 仅 10% 请求慢于此值，反映大多数用户体感' },
                  { label: 'P95', value: stressResult.p95, color: 'text-amber-400', desc: '95% 分位 — SLA 常用指标，衡量服务承诺达标线' },
                  { label: 'P99', value: stressResult.p99, color: 'text-rose-400', desc: '尾部延迟 — 仅 1% 请求超此值，揭示长尾异常与系统瓶颈' },
                ].map((p) => (
                  <div key={p.label} className="bg-slate-950 border border-slate-800 rounded-xl p-4">
                    <div className="flex items-center justify-between mb-2">
                      <span className="font-mono font-bold text-xs px-2 py-0.5 rounded bg-slate-900 border border-slate-800 text-slate-300">
                        {p.label}
                      </span>
                    </div>
                    <div className={`text-2xl font-bold font-mono ${p.color}`}>
                      {p.value} <span className="text-sm text-slate-500">ms</span>
                    </div>
                    <div className="text-[10px] text-slate-500 mt-1.5 leading-relaxed">{p.desc}</div>
                  </div>
                ))}
              </div>
            </div>

            {/* Latency Bar */}
            <div className="bg-slate-950 border border-slate-800 rounded-xl p-4">
              <div className="text-xs font-semibold text-slate-400 mb-3">延迟分布 (相对比例)</div>
              <div className="space-y-2">
                {[
                  { label: 'P50', value: stressResult.p50, color: 'bg-emerald-500', max: stressResult.p99 || 1 },
                  { label: 'P90', value: stressResult.p90, color: 'bg-indigo-500', max: stressResult.p99 || 1 },
                  { label: 'P95', value: stressResult.p95, color: 'bg-amber-500', max: stressResult.p99 || 1 },
                  { label: 'P99', value: stressResult.p99, color: 'bg-rose-500', max: stressResult.p99 || 1 },
                ].map((p) => (
                  <div key={p.label} className="flex items-center gap-3">
                    <span className="w-8 text-xs font-mono text-slate-400">{p.label}</span>
                    <div className="flex-1 h-3 bg-slate-900 rounded-full overflow-hidden border border-slate-800">
                      <div
                        className={`h-full rounded-full ${p.color} transition-all duration-500`}
                        style={{ width: `${Math.max((p.value / p.max) * 100, 2)}%` }}
                      />
                    </div>
                    <span className="w-20 text-right text-xs font-mono text-slate-400">{p.value} ms</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}
      </div>

      {/* Prometheus Parsed Metrics (from service-hub /metrics) */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div className="bg-slate-900 border border-slate-800 rounded-2xl p-5 shadow-xl">
          <div className="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-2">{t('metrics.qps')}</div>
          <div className="text-3xl font-bold font-mono text-cyan-400">{(parsedMetrics?.qps ?? 0).toFixed(1)}</div>
          <div className="text-[11px] text-slate-500 mt-1">总请求数: {(parsedMetrics?.total_requests ?? 0).toFixed(0)}</div>
        </div>
        <div className="bg-slate-900 border border-slate-800 rounded-2xl p-5 shadow-xl">
          <div className="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-2">延迟分位数 (Hub Prometheus)</div>
          <div className="flex items-center gap-4 mt-2">
            <span className="text-sm font-mono text-emerald-400">P50: {(parsedMetrics?.percentiles?.p50 ?? 0).toFixed(1)}ms</span>
            <span className="text-sm font-mono text-indigo-400">P90: {(parsedMetrics?.percentiles?.p90 ?? 0).toFixed(1)}ms</span>
            <span className="text-sm font-mono text-amber-400">P95: {(parsedMetrics?.percentiles?.p95 ?? 0).toFixed(1)}ms</span>
            <span className="text-sm font-mono text-rose-400">P99: {(parsedMetrics?.percentiles?.p99 ?? 0).toFixed(1)}ms</span>
          </div>
          <div className="text-[11px] text-slate-500 mt-2">来源: service-hub /metrics (Prometheus)</div>
        </div>
      </div>

      {/* Prometheus Raw Exporter View */}
      {showRaw && (
        <div className="bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-xl space-y-3">
          <h2 className="text-sm font-bold text-slate-100">Prometheus Metrics Stream (/metrics)</h2>
          <pre className="p-4 bg-slate-950 border border-slate-800 rounded-xl text-xs font-mono text-emerald-400/90 max-h-64 overflow-y-auto">
            {metricsRaw || '# service_hub_status 1\n# service_hub_qps 0'}
          </pre>
        </div>
      )}
    </div>
  );
};
