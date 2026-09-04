/**
 * 运维诊断面板 / Operations Diagnostics Panel
 *
 * 提供一站式运维排障视图，帮助快速定位问题出在哪一层：
 * Provides a one-stop ops troubleshooting view to quickly locate which layer fails:
 *
 *   1. 链路诊断：前端（浏览器）→ 控制台后端 → Agent，逐段点亮状态灯，
 *      一眼看出是前端、后端还是 Agent 出了问题；
 *      Chain diagnosis: Frontend → Console Backend → Agent, per-segment status lights
 *      reveal whether the frontend, backend or Agent is at fault;
 *   2. 分类引擎状态：NER（Layer-2）/ LLM（Layer-3）当前激活引擎与降级链路，
 *      直观展示"降级到了哪一级"以及每一级不可用的原因；
 *      Engine status: NER (Layer-2) / LLM (Layer-3) active engine and degradation chain,
 *      showing "which level it degraded to" and why each level is unavailable;
 *   3. 依赖与驱动：各 ML 依赖（onnxruntime/torch/transformers/...）是否安装、
 *      版本与安装命令（一键复制）；
 *      Dependencies & drivers: installation status, versions and install commands (copyable);
 *   4. 模型文件：模型是否存在与下载命令；
 *      Model files: existence and download commands;
 *   5. 硬件加速：CUDA / GPU / nvidia-smi 状态。
 *      Hardware acceleration: CUDA / GPU / nvidia-smi status.
 *
 * 数据来源 / Data Sources：
 *   - 链路诊断复用 App 传入的 health（/health）；
 *   - 其余信息经 fetchDiagnostics() 转发到 Agent 的 GET /v1/ops/diagnostics。
 */

/** 引入 React Hooks / Import React Hooks */
import { Fragment, useCallback, useEffect, useState } from 'react';
/** 引入健康检查与诊断类型 / Import health & diagnostics types */
import type { ConsoleHealth, OpsDiagnostics, OpsNerEngine } from '@/types/api';
/** 引入诊断 API / Import diagnostics API */
import { fetchDiagnostics } from '@/api/client';
/** 引入图标组件 / Import icon component */
import { Icon } from '@/components/icons';
/** 引入国际化 Hook / Import i18n Hook */
import { useI18n } from '@/i18n';
/** 引入统一错误消息提取工具 / Import unified error message extraction utility */
import { getErrorMessage } from '@/utils/error';

/**
 * OpsPanel 组件属性接口 / OpsPanel Props Interface
 */
interface OpsPanelProps {
  /** 控制台后端健康状态（用于链路诊断）/ Console backend health (for chain diagnosis) */
  health: ConsoleHealth | null;
}

/** 状态徽章配色：绿色=正常 / 红色=异常 / 灰色=未知 / Status badge colors */
function statusBadge(ok: boolean | null): string {
  if (ok === true) return 'bg-emerald-50 text-emerald-700 border-emerald-200';
  if (ok === false) return 'bg-red-50 text-red-700 border-red-200';
  return 'bg-gray-100 text-gray-500 border-gray-200';
}

/**
 * 可复制命令片段 / Copyable Command Snippet
 *
 * 以等宽字体展示一条 shell 命令，右侧附复制按钮。
 * Displays a shell command in monospace font with a copy button on the right.
 */
function CommandChip({ cmd }: { cmd: string }) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(cmd);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* 忽略剪贴板不可用 / Ignore clipboard unavailable */
    }
  };
  return (
    <span className="inline-flex max-w-full items-center gap-1.5 rounded-md border border-gray-200 bg-gray-50 px-2 py-1">
      <code className="truncate font-mono text-[11px] text-gray-700">{cmd}</code>
      <button
        onClick={copy}
        className="shrink-0 text-gray-400 transition-colors hover:text-indigo-600"
        title={t('ops.copy')}
        aria-label={t('ops.copy')}
      >
        <Icon name={copied ? 'check' : 'copy'} className="h-3 w-3" />
      </button>
    </span>
  );
}

/**
 * 链路诊断卡片 / Chain Diagnosis Card
 *
 * 渲染"前端 → 控制台后端 → Agent"三段链路，每段一个状态灯，
 * 直观定位问题出在哪一层。
 * Renders the "Frontend → Console Backend → Agent" three-segment chain with a
 * status light per segment to visually locate which layer fails.
 */
function ChainDiagnosis({ health }: { health: ConsoleHealth | null }) {
  const { t } = useI18n();

  // 前端：页面能渲染即为正常 / Frontend: OK as long as the page renders
  const frontendOk: boolean | null = true;
  // 控制台后端：能返回 health 即为正常 / Console backend: OK if health is returned
  const backendOk: boolean | null = health ? health.backend === 'ok' : null;
  // Agent：无 error 字段即为可达 / Agent: reachable when no error field
  const agentOk: boolean | null = health ? !health.error : null;

  const nodes: Array<{ label: string; ok: boolean | null; detail: string }> = [
    {
      label: t('ops.chain.frontend'),
      ok: frontendOk,
      detail: t('ops.chain.frontend_hint'),
    },
    {
      label: t('ops.chain.backend'),
      ok: backendOk,
      detail: health
        ? `${health.via ?? ''}${health.protocol ? ' · ' + health.protocol : ''}`.trim() || t('ops.chain.unknown')
        : t('ops.chain.no_data'),
    },
    {
      label: t('ops.chain.agent'),
      ok: agentOk,
      detail: health
        ? agentOk
          ? `${health.agent_url}${health.latency_ms != null ? ` · ${health.latency_ms.toFixed(1)}ms` : ''}`
          : health.error ?? t('ops.chain.down')
        : t('ops.chain.no_data'),
    },
  ];

  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-stretch">
      {nodes.map((node, i) => (
        <div key={node.label} className="flex flex-1 items-center gap-3">
          {/* 单段链路卡片 / Single segment card */}
          <div
            className={[
              'flex flex-1 items-center gap-3 rounded-xl border bg-white p-3 shadow-sm',
              statusBadge(node.ok),
            ].join(' ')}
          >
            {/* 状态灯 / Status light */}
            <span
              className={[
                'h-2.5 w-2.5 shrink-0 rounded-full',
                node.ok === true ? 'bg-emerald-500' : node.ok === false ? 'bg-red-500' : 'bg-gray-300',
              ].join(' ')}
            />
            <div className="min-w-0">
              <div className="text-sm font-semibold text-gray-800">{node.label}</div>
              <div className="truncate text-[11px] text-gray-500" title={node.detail}>
                {node.detail}
              </div>
            </div>
          </div>
          {/* 段间箭头（最后一段不渲染）/ Inter-segment arrow (not rendered for the last) */}
          {i < nodes.length - 1 && <Icon name="chevron-right" className="h-4 w-4 shrink-0 text-gray-300" />}
        </div>
      ))}
    </div>
  );
}

/**
 * NER 降级链可视化 / NER Degradation Chain Visualization
 *
 * 按尝试顺序渲染各引擎：激活引擎之前的为"已降级跳过"，激活引擎高亮，
 * 之后的为"未到达的备选"。
 * Renders engines in attempt order: engines before the active one are "degraded past",
 * the active engine is highlighted, and later ones are "unreached fallbacks".
 */
function NerChain({ engines, active }: { engines: OpsNerEngine[]; active: string }) {
  const { t } = useI18n();
  const activeIdx = engines.findIndex((e) => e.engine === active);

  return (
    <div className="flex flex-wrap items-center gap-2">
      {engines.map((eng, i) => {
        const isActive = eng.engine === active && eng.available;
        // 激活引擎之前 = 已降级跳过；之后 = 未到达备选 / Before active = degraded past; after = unreached fallback
        const state = isActive ? 'active' : activeIdx === -1 ? 'failed' : i < activeIdx ? 'skipped' : 'fallback';
        const styles =
          state === 'active'
            ? 'border-emerald-300 bg-emerald-50'
            : state === 'skipped' || state === 'failed'
              ? 'border-red-200 bg-red-50/50'
              : 'border-gray-200 bg-gray-50';
        return (
          <div key={eng.engine} className="flex items-center gap-2">
            <div className={`rounded-lg border px-3 py-2 ${styles}`} title={eng.note}>
              <div className="flex items-center gap-1.5">
                <span
                  className={[
                    'h-1.5 w-1.5 rounded-full',
                    state === 'active' ? 'bg-emerald-500' : state === 'fallback' ? 'bg-gray-300' : 'bg-red-400',
                  ].join(' ')}
                />
                <span className="text-xs font-bold uppercase tracking-wide text-gray-700">{eng.engine}</span>
              </div>
              <div className="mt-0.5 max-w-[180px] text-[10px] leading-tight text-gray-500">
                {state === 'active' && t('ops.engines.active')}
                {state === 'skipped' && (eng.reason ?? eng.probe_error ?? t('ops.engines.degraded'))}
                {state === 'failed' && (eng.reason ?? eng.probe_error ?? t('ops.engines.unavailable'))}
                {state === 'fallback' && t('ops.engines.fallback')}
              </div>
            </div>
            {i < engines.length - 1 && <Icon name="chevron-right" className="h-3.5 w-3.5 shrink-0 text-gray-300" />}
          </div>
        );
      })}
    </div>
  );
}

/**
 * 运维诊断主组件 / Operations Diagnostics Main Component
 */
export default function OpsPanel({ health }: OpsPanelProps) {
  const { t } = useI18n();
  /** 诊断数据 / Diagnostics data */
  const [diag, setDiag] = useState<OpsDiagnostics | null>(null);
  /** 加载中标记 / Loading flag */
  const [loading, setLoading] = useState(true);
  /** 错误信息 / Error message */
  const [error, setError] = useState<string | null>(null);

  /**
   * 拉取诊断数据 / Fetch diagnostics data
   *
   * @param refresh - 是否令 agent 失效探测缓存并重新探测各引擎。
   *   首次加载使用缓存结果（快）；用户点击“刷新诊断”时传 true，
   *   覆盖服务运行期间补装依赖（如 mlx）后结论陈旧的场景。
   *   Whether to invalidate agent probe cache and re-probe engines.
   *   Initial load uses cached results (fast); the explicit "Refresh" click
   *   passes true to cover deps installed while the service was running.
   */
  const load = useCallback(async (refresh = false) => {
    setLoading(true);
    setError(null);
    try {
      setDiag(await fetchDiagnostics(refresh));
    } catch (e) {
      setError(getErrorMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  // 首次挂载时加载 / Load on first mount
  useEffect(() => {
    load();
  }, [load]);

  const ner = diag?.engines.ner;
  const llm = diag?.engines.llm;

  return (
    /* 面板容器：全屏滚动 / Panel container: full-screen scroll */
    <div className="h-full overflow-y-auto bg-gray-50 p-6">
      <div className="mx-auto max-w-5xl space-y-6">
        {/* ====== 标题栏：标题 + 刷新按钮 / Header: title + refresh button ====== */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="flex items-center gap-2 text-xl font-bold text-gray-900">
              <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-600 text-white">
                <Icon name="activity" className="h-4 w-4" />
              </span>
              {t('ops.title')}
            </h1>
            <p className="mt-1 text-sm text-gray-500">{t('ops.subtitle')}</p>
          </div>
          <button
            onClick={() => load(true)}
            disabled={loading}
            title={t('ops.refresh_hint')}
            className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white shadow-sm transition-colors hover:bg-indigo-700 disabled:opacity-50"
          >
            <Icon name="refresh" className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
            {loading ? t('ops.refreshing') : t('ops.refresh')}
          </button>
        </div>

        {/* ====== 链路诊断 / Chain Diagnosis ====== */}
        <section className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
          <h2 className="mb-3 text-sm font-semibold text-gray-700">{t('ops.chain.title')}</h2>
          <ChainDiagnosis health={health} />
        </section>

        {/* 加载中 / Loading */}
        {loading && !diag && (
          <div className="flex flex-col items-center justify-center gap-3 py-16 text-gray-400">
            <span className="h-8 w-8 animate-spin rounded-full border-2 border-gray-200 border-t-indigo-500" />
            <p className="text-sm">{t('ops.loading')}</p>
          </div>
        )}

        {/* 错误 / Error */}
        {error && !diag && (
          <div className="flex flex-col items-center justify-center gap-4 rounded-xl border border-red-200 bg-red-50 py-12">
            <Icon name="alert" className="h-8 w-8 text-red-500" />
            <p className="max-w-md px-6 text-center text-sm text-red-700">{error}</p>
            <button
              onClick={() => load()}
              className="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700"
            >
              <Icon name="refresh" className="h-4 w-4" />
              {t('app.retry')}
            </button>
          </div>
        )}

        {/* ====== 诊断数据主体 / Diagnostics Body ====== */}
        {diag && (
          <>
            {/* ====== 分类引擎状态 / Classification Engine Status ====== */}
            <section className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
              <h2 className="mb-4 text-sm font-semibold text-gray-700">{t('ops.engines.title')}</h2>
              <div className="space-y-5">
                {/* --- NER（Layer-2）--- */}
                {ner && (
                  <div>
                    <div className="mb-2 flex flex-wrap items-center gap-2">
                      <span className="text-sm font-semibold text-gray-800">{t('ops.engines.ner')}</span>
                      <span
                        className={[
                          'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium',
                          statusBadge(ner.available),
                        ].join(' ')}
                      >
                        {ner.available
                          ? `${t('ops.engines.active')}: ${ner.active_engine}`
                          : t('ops.engines.unavailable')}
                      </span>
                      {/* 判定来源 / Determination source */}
                      <span className="inline-flex items-center rounded-full border border-blue-200 bg-blue-50 px-2 py-0.5 text-[10px] font-medium text-blue-600">
                        {ner.determined_by === 'probe' ? t('ops.engines.auto_probe') : t('ops.engines.runtime')}
                      </span>
                      {/* 运行时引擎（若已初始化）/ Runtime engine (if initialized) */}
                      <span className="text-[11px] text-gray-400">
                        {ner.runtime_engine != null
                          ? `${t('ops.engines.runtime')}: ${ner.runtime_engine}`
                          : t('ops.engines.not_initialized')}
                      </span>
                    </div>
                    <NerChain engines={ner.degradation_chain} active={ner.active_engine} />
                    {/* 动态探测详情（可展开）/ Probe details (expandable) */}
                    {ner.probe && ner.probe.details.length > 0 && (
                      <details className="mt-2">
                        <summary className="cursor-pointer text-[11px] text-gray-400 hover:text-gray-600">
                          {t('ops.engines.probe_detail')}
                        </summary>
                        <div className="mt-1 space-y-1 rounded-lg bg-gray-50 p-2">
                          {ner.probe.details.map((d) => (
                            <div key={d.engine} className="flex items-start gap-2 text-[11px]">
                              <span className={d.ok ? 'text-emerald-500' : 'text-red-400'}>
                                {d.ok ? '✓' : '✗'}
                              </span>
                              <span className="font-mono font-medium text-gray-700">{d.engine}</span>
                              {d.error && <span className="text-gray-500">{d.error}</span>}
                            </div>
                          ))}
                        </div>
                      </details>
                    )}
                  </div>
                )}

                {/* --- LLM（Layer-3）--- */}
                {llm && (
                  <div className="border-t border-gray-100 pt-4">
                    <div className="mb-2 flex flex-wrap items-center gap-2">
                      <span className="text-sm font-semibold text-gray-800">{t('ops.engines.llm')}</span>
                      <span
                        className={[
                          'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium',
                          statusBadge(llm.available),
                        ].join(' ')}
                      >
                        {llm.available ? t('ops.engines.llm_ok') : t('ops.engines.unavailable')}
                      </span>
                      {/* 判定来源 / Determination source */}
                      <span className="inline-flex items-center rounded-full border border-blue-200 bg-blue-50 px-2 py-0.5 text-[10px] font-medium text-blue-600">
                        {llm.determined_by === 'probe' ? t('ops.engines.auto_probe') : t('ops.engines.runtime')}
                      </span>
                      <span className="text-[11px] text-gray-400">
                        {llm.runtime_available != null
                          ? `${t('ops.engines.runtime')}: ${llm.runtime_available ? 'ok' : 'down'}`
                          : t('ops.engines.not_initialized')}
                      </span>
                    </div>
                    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-gray-500">
                      <span>
                        {t('ops.engines.backend')}: <code className="font-mono">{llm.backend}</code>
                      </span>
                      <span>
                        {t('ops.engines.model')}: <code className="font-mono">{llm.model}</code>{' '}
                        {llm.model_exists ? '✓' : '✗'}
                      </span>
                      {llm.reason && <span className="text-red-500">{llm.reason}</span>}
                    </div>
                  </div>
                )}
              </div>
            </section>

            {/* ====== 依赖与驱动 / Dependencies & Drivers ====== */}
            <section className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
              <h2 className="mb-3 text-sm font-semibold text-gray-700">{t('ops.deps.title')}</h2>
              <div className="overflow-x-auto">
                <table className="w-full min-w-[640px] text-left text-xs">
                  <thead>
                    <tr className="border-b border-gray-200 text-[11px] uppercase tracking-wide text-gray-400">
                      <th className="py-2 pr-3 font-medium">{t('ops.deps.col_name')}</th>
                      <th className="py-2 pr-3 font-medium">{t('ops.deps.col_status')}</th>
                      <th className="py-2 pr-3 font-medium">{t('ops.deps.col_version')}</th>
                      <th className="py-2 pr-3 font-medium">{t('ops.deps.col_purpose')}</th>
                      <th className="py-2 font-medium">{t('ops.deps.col_install')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {diag.dependencies.map((dep) => (
                      <tr key={dep.name} className="border-b border-gray-50 align-top">
                        <td className="py-2.5 pr-3 font-mono font-semibold text-gray-800">{dep.name}</td>
                        <td className="py-2.5 pr-3">
                          <span
                            className={[
                              'inline-flex items-center rounded-full border px-2 py-0.5 text-[10px] font-medium',
                              statusBadge(dep.installed),
                            ].join(' ')}
                          >
                            {dep.installed ? t('ops.deps.installed') : t('ops.deps.missing')}
                          </span>
                        </td>
                        <td className="py-2.5 pr-3 font-mono text-gray-500">{dep.version ?? '—'}</td>
                        <td className="py-2.5 pr-3 text-gray-500">{dep.purpose}</td>
                        <td className="py-2.5">
                          {!dep.installed && <CommandChip cmd={dep.install} />}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>

            {/* ====== 模型文件 / Model Files ====== */}
            <section className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
              <h2 className="mb-3 text-sm font-semibold text-gray-700">{t('ops.models.title')}</h2>
              <div className="overflow-x-auto">
                <table className="w-full min-w-[560px] text-left text-xs">
                  <thead>
                    <tr className="border-b border-gray-200 text-[11px] uppercase tracking-wide text-gray-400">
                      <th className="py-2 pr-3 font-medium">{t('ops.models.col_name')}</th>
                      <th className="py-2 pr-3 font-medium">{t('ops.models.col_path')}</th>
                      <th className="py-2 pr-3 font-medium">{t('ops.models.col_status')}</th>
                      <th className="py-2 font-medium">{t('ops.models.col_download')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {diag.models.map((m) => (
                      <Fragment key={m.path}>
                        <tr className="border-b border-gray-50 align-top">
                          <td className="py-2.5 pr-3 font-medium text-gray-800">{m.name}</td>
                          <td className="py-2.5 pr-3 font-mono text-gray-500">{m.path}</td>
                          <td className="py-2.5 pr-3">
                            <span
                              className={[
                                'inline-flex items-center rounded-full border px-2 py-0.5 text-[10px] font-medium',
                                statusBadge(m.exists),
                              ].join(' ')}
                            >
                              {m.exists ? t('ops.models.exists') : t('ops.models.missing')}
                            </span>
                          </td>
                          <td className="py-2.5">{!m.exists && <CommandChip cmd={m.download} />}</td>
                        </tr>
                        {/* 附加诊断备注（如孤儿 ONNX 权重说明）/ Additional note (e.g. orphaned ONNX weights) */}
                        {m.note && (
                          <tr className="border-b border-gray-50">
                            <td colSpan={4} className="pb-2.5 pt-0">
                              <div className="flex items-start gap-1.5 rounded-md bg-amber-50 px-2.5 py-1.5 text-[11px] leading-snug text-amber-700">
                                <Icon name="alert" className="mt-0.5 h-3 w-3 shrink-0" />
                                <span>{m.note}</span>
                              </div>
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>

            {/* ====== 硬件加速 / Hardware Acceleration ====== */}
            <section className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
              <h2 className="mb-3 text-sm font-semibold text-gray-700">{t('ops.hardware.title')}</h2>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                {/* CUDA 状态 / CUDA status */}
                <div className={`rounded-lg border p-3 ${statusBadge(diag.hardware.cuda_available)}`}>
                  <div className="text-[11px] font-medium uppercase tracking-wide opacity-70">
                    {t('ops.hardware.cuda')}
                  </div>
                  <div className="mt-1 text-sm font-semibold">
                    {diag.hardware.cuda_available === true
                      ? t('ops.hardware.on')
                      : diag.hardware.cuda_available === false
                        ? t('ops.hardware.off')
                        : t('ops.hardware.unknown')}
                  </div>
                  <div className="mt-1 text-[11px] leading-snug opacity-80">{diag.hardware.cuda_detail}</div>
                </div>
                {/* nvidia-smi */}
                <div className={`rounded-lg border p-3 ${statusBadge(diag.hardware.nvidia_smi_found)}`}>
                  <div className="text-[11px] font-medium uppercase tracking-wide opacity-70">
                    {t('ops.hardware.nvidia_smi')}
                  </div>
                  <div className="mt-1 text-sm font-semibold">
                    {diag.hardware.nvidia_smi_found ? t('ops.hardware.found') : t('ops.hardware.not_found')}
                  </div>
                </div>
                {/* 平台 / Platform */}
                <div className="rounded-lg border border-gray-200 bg-gray-50 p-3">
                  <div className="text-[11px] font-medium uppercase tracking-wide text-gray-400">
                    {t('ops.hardware.platform')}
                  </div>
                  <div className="mt-1 truncate font-mono text-xs text-gray-700" title={diag.hardware.platform}>
                    {diag.hardware.machine}
                  </div>
                  <div className="mt-1 text-[11px] text-gray-500">
                    Python {diag.service.python_version} · {diag.service.name}
                  </div>
                </div>
              </div>
            </section>

            {/* 诊断时间戳 / Diagnostics timestamp */}
            <p className="pb-4 text-center text-[11px] text-gray-400">
              {t('ops.updated_at')} {new Date(diag.timestamp).toLocaleString()}
            </p>
          </>
        )}
      </div>
    </div>
  );
}
