/**
 * DataApiPanel — 预设数据 API 全链路会话测试大屏。
 *
 * 功能概述：
 *  1. 展示 4 个预设数据 API 卡片（医保/康养/预留×2），支持点击“申请”触发全链路会话
 *  2. 5 阶段流水线可视化：Ingest → Fetch → Classify & Desensitize → Return → Audit
 *  3. 会话结果展示：状态指示 + 各阶段耗时 + 原始数据 vs 脱敏数据对比
 *  4. 手风琴式数据对比视图（DataAccordionView）：支持逐行展开查看字段级脱敏详情
 *
 * 全链路会话流程：
 *  前端 → BFF → Service Hub 调度 → Datasource Mgr 按身份证号查询单条记录
 *  → Engine 分类脱敏 → Audit Log 存证 → 前端展示结果
 *
 * 状态管理：
 *  - selectedApiId: 当前选中的 API
 *  - idCardNo: 身份证号输入
 *  - session: 会话结果（含原始数据 + 脱敏数据 + 各阶段状态）
 *  - showRaw: 是否显示原始 JSON（vs 手风琴模式）
 *  - expandedRows: 手风琴模式中展开的行
 */
import React, { useState, useMemo } from 'react';
import { DataApiDef, DataApiSessionResponse } from '../types/api';
import { useI18n } from '../i18n';
import {
  IconDatabase,
  IconPlay,
  IconCheckCircle,
  IconXCircle,
  IconRefresh,
  IconShieldCheck,
  IconLock,
  IconActivity,
} from './icons';

/** DataApiPanel 组件的 Props */
interface DataApiPanelProps {
  /** 预设数据 API 定义列表（4 个） */
  apis: DataApiDef[];
  /** 调用数据 API 的回调（触发全链路会话，传入 apiId 和身份证号） */
  onInvoke: (apiId: number, idCardNo: string) => Promise<DataApiSessionResponse>;
  /** 是否正在加载中 */
  loading: boolean;
}

export const DataApiPanel: React.FC<DataApiPanelProps> = ({
  apis,
  onInvoke,
  loading,
}) => {
  const { t } = useI18n();
  const [selectedApiId, setSelectedApiId] = useState<number | null>(null);
  const [idCardNo, setIdCardNo] = useState('');
  const [session, setSession] = useState<DataApiSessionResponse | null>(null);
  const [invoking, setInvoking] = useState(false);
  const [showRaw, setShowRaw] = useState(false);
  const [expandedRows, setExpandedRows] = useState<Set<number>>(new Set());

  /** 调用指定 API 的全链路会话 */
  const handleInvoke = async (apiId: number) => {
    const trimmedId = idCardNo.trim();
    if (trimmedId.length !== 18) {
      alert('请输入 18 位身份证号');
      return;
    }
    setInvoking(true);
    setSession(null);
    try {
      const res = await onInvoke(apiId, trimmedId);
      setSession(res);
    } catch (err: any) {
      alert(`API 调用失败: ${err.message}`);
    } finally {
      setInvoking(false);
    }
  };

  /** 根据阶段状态返回对应图标（success=勾号, error=叉号, 其他=-） */
  const getStageIcon = (status: string) => {
    switch (status) {
      case 'success':
        return <IconCheckCircle className="w-4 h-4 text-emerald-400" />;
      case 'error':
        return <IconXCircle className="w-4 h-4 text-rose-400" />;
      default:
        return <span className="w-4 h-4 flex items-center justify-center text-xs text-slate-500">-</span>;
    }
  };

  return (
    <div className="space-y-6">
      {/* Banner */}
      <div className="bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-xl flex flex-col md:flex-row items-start md:items-center justify-between gap-4">
        <div>
          <div className="flex items-center gap-2.5">
            <span className="p-2 rounded-xl bg-cyan-500/10 border border-cyan-500/20 text-cyan-400">
              <IconDatabase className="w-6 h-6" />
            </span>
            <h1 className="text-xl font-bold text-slate-100">{t('dataApi.title')}</h1>
          </div>
          <p className="text-sm text-slate-400 mt-1 max-w-2xl">{t('dataApi.desc')}</p>
        </div>

        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2 text-xs">
            <span className="text-slate-400">身份证号:</span>
            <div className="relative">
              <input
                type="text"
                list="id-card-presets"
                value={idCardNo}
                onChange={(e) => setIdCardNo(e.target.value.replace(/[^0-9Xx]/g, '').slice(0, 18))}
                placeholder="点击选择或输入身份证号"
                className="bg-slate-950 border border-slate-800 rounded px-2 py-1 text-slate-200 font-mono w-56 focus:outline-none focus:border-cyan-500 placeholder:text-slate-600"
                autoComplete="off"
              />
              <datalist id="id-card-presets">
                <option value="110101196809171010">医保 — 硬下疳(早期梅毒)</option>
                <option value="51010119940527103X">医保 — HIV抗体阳性</option>
                <option value="31010119431103106X">医保 — 原发性肺癌</option>
                <option value="440101195402131086">医保 — 亨廷顿舞蹈病</option>
                <option value="330101199201231091">医保 — 社区获得性肺炎</option>
                <option value="420101196909261120">医保 — 急性前壁心肌梗死</option>
                <option value="500101195603151178">医保 — 急性阑尾炎</option>
                <option value="110105198402151071">康养 — 急性心肌梗死(萧志明)</option>
                <option value="110105198303151148">康养 — 重度精神分裂症(郭凯)</option>
                <option value="110105198204151214">康养 — 梅毒(韩雨泽)</option>
                <option value="110105198105151286">康养 — HIV感染(刘斌)</option>
                <option value="110105198006151352">康养 — 遗传性亨廷顿舞蹈病(张丽华)</option>
                <option value="000000000000000000">⚠ 不存在(反向测试)</option>
              </datalist>
            </div>
            {idCardNo.length === 18 && (
              <span className="text-emerald-400 text-[10px]">✓</span>
            )}
          </div>
        </div>
      </div>

      {/* Session Flow Diagram — 5-Stage Pipeline */}
      <div className="bg-slate-900 border border-slate-800 rounded-2xl p-5 shadow-xl">
        <h2 className="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-4">
          {t('dataApi.flowTitle')} — 5 阶段流水线
        </h2>
        <div className="grid grid-cols-2 md:grid-cols-5 gap-2">
          {[
            { key: 'ingest', label: '1. Ingest\n任务接收校验', icon: '📥', color: 'indigo' },
            { key: 'fetch', label: '2. Fetch\n数据源切片抽取', icon: '📊', color: 'amber' },
            { key: 'classify_desensitize', label: '3. Classify & Desensitize\n漏斗评级与脱敏治理', icon: '🔒', color: 'emerald' },
            { key: 'return', label: '4. Return\n合规结果装配', icon: '📦', color: 'cyan' },
            { key: 'audit', label: '5. Audit\n不可篡改存证', icon: '📝', color: 'purple' },
          ].map((step, idx) => {
            const stageData = session?.stages.find(s =>
              s.name === step.key ||
              (step.key === 'classify_desensitize' && (s.name === 'classify_desensitize' || s.name === 'classify' || s.name === 'desensitize' || s.name === 'process'))
            );
            const isActive = stageData?.status === 'success';
            const isError = stageData?.status === 'error';
            return (
              <div key={step.key} className="flex items-center gap-1.5">
                <div className={`flex-1 p-3 rounded-xl border text-center transition-all ${
                  isError
                    ? 'bg-rose-950/20 border-rose-500/40'
                    : isActive
                    ? 'bg-emerald-950/20 border-emerald-500/30'
                    : 'bg-slate-950 border-slate-800'
                }`}>
                  <div className="text-lg mb-1">{step.icon}</div>
                  <div className="text-[10px] font-semibold text-slate-300 whitespace-pre-line leading-tight">
                    {step.label}
                  </div>
                  {stageData && (
                    <div className={`text-[9px] font-mono mt-1 ${
                      isError ? 'text-rose-400' : isActive ? 'text-emerald-400' : 'text-slate-500'
                    }`}>
                      {stageData.duration_ms}ms
                    </div>
                  )}
                </div>
                {idx < 4 && (
                  <span className="text-slate-600 text-xs font-bold shrink-0">›</span>
                )}
              </div>
            );
          })}
        </div>
      </div>

      {/* API Cards — active APIs + merged reserved slot */}
      {(() => {
        const activeApis = apis.filter(a => a.status === 'active');
        const reservedApis = apis.filter(a => a.status === 'reserved');
        const displayApis = [...activeApis];
        if (reservedApis.length > 0) {
          displayApis.push({
            ...reservedApis[0],
            name: `预留数据 API #${reservedApis.length} 合井位`,
            description: `${reservedApis.length} 个预留接口已合并，待后续业务接入新的数据源。实现层已统一为单一调用入口。`,
            id: reservedApis[0].id,
          });
        }
        return (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {displayApis.map((api) => {
          const isActive = api.status === 'active';
          const isSelected = selectedApiId === api.id;
          const isInvoking = invoking && isSelected;

          return (
            <div
              key={api.id}
              onClick={() => isActive && setSelectedApiId(api.id)}
              className={`p-5 rounded-2xl border transition-all duration-200 ${
                !isActive
                  ? 'bg-slate-900/50 border-slate-800/60 opacity-60 cursor-not-allowed'
                  : isSelected
                  ? 'bg-cyan-950/20 border-cyan-500/60 ring-2 ring-cyan-500/20 cursor-pointer'
                  : 'bg-slate-900 border-slate-800 hover:border-slate-700 cursor-pointer'
              }`}
            >
              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-xs font-extrabold px-2 py-0.5 rounded bg-cyan-500/20 text-cyan-300 border border-cyan-500/40">
                    API {api.id}
                  </span>
                  <span className={`text-[10px] px-2 py-0.5 rounded-full font-semibold ${
                    isActive
                      ? 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/20'
                      : 'bg-slate-800 text-slate-500 border border-slate-700'
                  }`}>
                    {isActive ? 'Active' : 'Reserved'}
                  </span>
                </div>
                <span className="text-xs text-slate-400 font-mono">{api.datasource_id || '-'}</span>
              </div>

              <h3 className="text-base font-bold text-slate-100">{api.name}</h3>
              <p className="text-xs text-slate-400 mt-1 leading-relaxed">{api.description}</p>

              {api.fields.length > 0 && (
                <div className="mt-3 flex flex-wrap gap-1">
                  {api.fields.map((f) => (
                    <span key={f} className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-slate-950 text-slate-400 border border-slate-800">
                      {f}
                    </span>
                  ))}
                </div>
              )}

              {isActive && (
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    handleInvoke(api.id);
                  }}
                  disabled={isInvoking}
                  className="mt-4 w-full flex items-center justify-center gap-2 py-2.5 rounded-xl bg-cyan-600 hover:bg-cyan-500 text-white font-bold text-xs transition-all shadow-lg shadow-cyan-600/20 disabled:opacity-50"
                >
                  {isInvoking ? (
                    <>
                      <IconRefresh className="w-3.5 h-3.5 animate-spin" />
                      <span>{t('dataApi.invoking')}</span>
                    </>
                  ) : (
                    <>
                      <IconPlay className="w-3.5 h-3.5" />
                      <span>{t('dataApi.invokeBtn')}</span>
                    </>
                  )}
                </button>
              )}

              {!isActive && (
                <div className="mt-4 py-2 rounded-xl bg-slate-950/60 border border-slate-800 text-center text-xs text-slate-500">
                  {t('dataApi.reserved')}
                </div>
              )}
            </div>
          );
            })}
          </div>
        );
      })()}

      {/* Session Result */}
      {session && (
        <div className="space-y-4">
          {/* Session Header */}
          <div className={`border rounded-2xl p-5 shadow-xl ${
            session.status === 'completed'
              ? 'bg-gradient-to-r from-emerald-950/30 via-slate-900 to-slate-900 border-emerald-500/40'
              : session.status === 'partial'
              ? 'bg-gradient-to-r from-amber-950/30 via-slate-900 to-slate-900 border-amber-500/40'
              : 'bg-gradient-to-r from-rose-950/30 via-slate-900 to-slate-900 border-rose-500/40'
          }`}>
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-3">
                {session.status === 'completed' ? (
                  <IconCheckCircle className="w-6 h-6 text-emerald-400" />
                ) : session.status === 'partial' ? (
                  <IconActivity className="w-6 h-6 text-amber-400" />
                ) : (
                  <IconXCircle className="w-6 h-6 text-rose-400" />
                )}
                <div>
                  <h2 className="text-sm font-bold text-slate-100">
                    {t('dataApi.sessionResult')} — {session.api_name}
                  </h2>
                  <p className="text-xs text-slate-400 font-mono mt-0.5">
                    Session: {session.session_id} · {session.total_duration_ms}ms
                  </p>
                </div>
              </div>
              <span className={`px-3 py-1 rounded-full text-xs font-bold ${
                session.status === 'completed'
                  ? 'bg-emerald-500/20 text-emerald-300 border border-emerald-500/30'
                  : session.status === 'partial'
                  ? 'bg-amber-500/20 text-amber-300 border border-amber-500/30'
                  : 'bg-rose-500/20 text-rose-300 border border-rose-500/30'
              }`}>
                {session.status.toUpperCase()}
              </span>
            </div>

            {/* Stage Timeline */}
            <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-5 gap-2">
              {session.stages.map((stage) => (
                <div key={stage.name} className="p-3 rounded-xl bg-slate-950/80 border border-slate-800">
                  <div className="flex items-center gap-2 mb-1.5">
                    {getStageIcon(stage.status)}
                    <span className="text-xs font-bold text-slate-200">{stage.title}</span>
                  </div>
                  <div className="text-[10px] text-slate-400 font-mono">
                    {stage.duration_ms}ms
                  </div>
                  {stage.detail && (
                    <div className="text-[10px] text-slate-500 mt-1 leading-relaxed">
                      {stage.detail}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>

          {/* Data Diff View — Accordion Style */}
          {(session.raw_records?.length ?? 0) > 0 && (
            <DataAccordionView
              rawRecords={session.raw_records}
              sanitizedData={session.sanitized_data}
              showRaw={showRaw}
              onToggleRaw={() => setShowRaw(!showRaw)}
              expandedRows={expandedRows}
              setExpandedRows={setExpandedRows}
              t={t}
            />
          )}

          {/* Audit Entry */}
          {session.audit_entry_id && (
            <div className="bg-slate-900 border border-slate-800 rounded-2xl p-4 shadow-xl flex items-center gap-3 text-xs">
              <IconShieldCheck className="w-5 h-5 text-emerald-400 shrink-0" />
              <div>
                <span className="text-slate-300 font-semibold">{t('dataApi.auditWritten')}:</span>
                <span className="text-emerald-400 font-mono ml-2">{session.audit_entry_id}</span>
              </div>
            </div>
          )}

          {session.error && (
            <div className="bg-rose-950/30 border border-rose-500/30 rounded-2xl p-4 text-xs text-rose-300">
              {session.error}
            </div>
          )}
        </div>
      )}
    </div>
  );
};

/**
 * DataAccordionView — 手风琴式数据对比视图。
 *
 * 功能：
 *  1. 逐行展示脱敏前后的数据对比（每条记录可展开/收起）
 *  2. 展开后按字段对比原始值 vs 脱敏值，被脱敏的字段高亮显示
 *  3. 支持“全部展开/全部收起”快捷按钮
 *  4. 支持切换“原始 JSON”模式（直接显示完整 JSON 文本）
 *  5. 自动统计每条记录中被脱敏的字段数
 */
interface DataAccordionViewProps {
  rawRecords: Record<string, any>[];
  sanitizedData: Record<string, any>[];
  showRaw: boolean;
  onToggleRaw: () => void;
  expandedRows: Set<number>;
  setExpandedRows: React.Dispatch<React.SetStateAction<Set<number>>>;
  t: (key: string) => string;
}

const DataAccordionView: React.FC<DataAccordionViewProps> = ({
  rawRecords,
  sanitizedData,
  showRaw,
  onToggleRaw,
  expandedRows,
  setExpandedRows,
  t,
}) => {
  /** 合并原始数据和脱敏数据中的所有字段名（去重） */
  const allFields = useMemo(() => {
    const keys = new Set<string>();
    rawRecords.forEach((r) => Object.keys(r).forEach((k) => keys.add(k)));
    sanitizedData.forEach((r) => Object.keys(r).forEach((k) => keys.add(k)));
    return Array.from(keys);
  }, [rawRecords, sanitizedData]);

  /** 获取记录的摘要标签（优先使用 ID 字段，否则取第一个字段） */
  const getRecordSummary = (raw: Record<string, any>, sanitized: Record<string, any>): string => {
    // Try common ID fields first
    const idFields = ['record_id', 'elder_id', 'id', 'patient_name', 'name'];
    for (const f of idFields) {
      if (raw[f] !== undefined) return `${f}: ${raw[f]}`;
    }
    // Fallback: first field value
    const firstKey = Object.keys(raw)[0];
    if (firstKey) return `${firstKey}: ${raw[firstKey]}`;
    return '(empty record)';
  };

  /** 统计记录中被脱敏的字段数（原始值 !== 脱敏值） */
  const countMasked = (raw: Record<string, any>, sanitized: Record<string, any>): number => {
    let count = 0;
    for (const key of Object.keys(raw)) {
      if (String(raw[key]) !== String(sanitized[key] ?? raw[key])) count++;
    }
    return count;
  };

  const toggleRow = (idx: number) => {
    setExpandedRows((prev) => {
      const next = new Set(prev);
      if (next.has(idx)) {
        next.delete(idx);
      } else {
        next.add(idx);
      }
      return next;
    });
  };

  const expandAll = () => {
    setExpandedRows(new Set(sanitizedData.map((_, i) => i)));
  };

  const collapseAll = () => {
    setExpandedRows(new Set());
  };

  const formatValue = (v: any): string => {
    if (v === null || v === undefined) return '-';
    if (typeof v === 'object') return JSON.stringify(v);
    return String(v);
  };

  return (
    <div className="bg-slate-900 border border-slate-800 rounded-2xl p-5 shadow-xl">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-slate-800 pb-3 mb-4">
        <h3 className="text-sm font-bold text-slate-100 flex items-center gap-2">
          <IconLock className="w-4 h-4 text-emerald-400" />
          {t('dataApi.dataDiff')}
          <span className="text-[10px] font-mono px-2 py-0.5 rounded-full bg-slate-800 text-slate-400 border border-slate-700">
            {sanitizedData.length} {t('dataApi.rawRecords').replace(/\(.*\)/, '').trim()}
          </span>
        </h3>
        <div className="flex items-center gap-2">
          <button
            onClick={expandAll}
            className="text-[10px] px-2 py-1 rounded bg-slate-800 hover:bg-slate-700 text-slate-400 border border-slate-700 transition"
          >
            全部展开
          </button>
          <button
            onClick={collapseAll}
            className="text-[10px] px-2 py-1 rounded bg-slate-800 hover:bg-slate-700 text-slate-400 border border-slate-700 transition"
          >
            全部收起
          </button>
          <button
            onClick={onToggleRaw}
            className="text-xs px-2.5 py-1 rounded bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700 transition"
          >
            {showRaw ? t('dataApi.hideRaw') : t('dataApi.showRaw')}
          </button>
        </div>
      </div>

      {/* Raw JSON mode */}
      {showRaw ? (
        <div className="grid grid-cols-1 gap-4">
          <div>
            <div className="text-xs font-semibold text-rose-400 mb-1 flex items-center gap-1">
              <span className="w-2 h-2 rounded-full bg-rose-400" />
              {t('dataApi.rawRecords')} ({rawRecords.length})
            </div>
            <pre className="p-3 bg-slate-950 border border-rose-500/20 rounded-xl text-xs font-mono text-rose-200/90 overflow-x-auto max-h-80">
              {JSON.stringify(rawRecords, null, 2)}
            </pre>
          </div>
          <div>
            <div className="text-xs font-semibold text-emerald-400 mb-1 flex items-center gap-1">
              <span className="w-2 h-2 rounded-full bg-emerald-400" />
              {t('dataApi.sanitizedRecords')} ({sanitizedData.length})
            </div>
            <pre className="p-3 bg-slate-950 border border-emerald-500/20 rounded-xl text-xs font-mono text-emerald-200/90 overflow-x-auto max-h-80">
              {JSON.stringify(sanitizedData, null, 2)}
            </pre>
          </div>
        </div>
      ) : (
        /* Accordion mode — one card per record */
        <div className="space-y-2">
          {sanitizedData.map((sanitized, i) => {
            const raw = rawRecords[i] || {};
            const isExpanded = expandedRows.has(i);
            const maskedCount = countMasked(raw, sanitized);
            const summary = getRecordSummary(raw, sanitized);

            return (
              <div
                key={i}
                className={`rounded-xl border transition-all duration-200 ${
                  isExpanded
                    ? 'bg-slate-950 border-cyan-500/40 ring-1 ring-cyan-500/10'
                    : 'bg-slate-950/60 border-slate-800 hover:border-slate-700'
                }`}
              >
                {/* Collapsed row header — clickable */}
                <button
                  onClick={() => toggleRow(i)}
                  className="w-full flex items-center justify-between px-4 py-3 text-left transition hover:bg-slate-800/30 rounded-xl"
                >
                  <div className="flex items-center gap-3 min-w-0">
                    <span className="shrink-0 w-7 h-7 flex items-center justify-center rounded-lg bg-cyan-500/10 border border-cyan-500/30 text-cyan-300 text-xs font-bold font-mono">
                      {i + 1}
                    </span>
                    <span className="text-sm font-mono text-slate-200 truncate">
                      {summary}
                    </span>
                    {maskedCount > 0 && (
                      <span className="shrink-0 text-[10px] font-semibold px-2 py-0.5 rounded-full bg-amber-500/10 text-amber-400 border border-amber-500/20">
                        {maskedCount} 字段已脱敏
                      </span>
                    )}
                    {maskedCount === 0 && (
                      <span className="shrink-0 text-[10px] font-semibold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
                        无敏感字段
                      </span>
                    )}
                  </div>
                  <svg
                    className={`w-4 h-4 text-slate-500 shrink-0 transition-transform duration-200 ${isExpanded ? 'rotate-180' : ''}`}
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                    strokeWidth={2}
                  >
                    <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
                  </svg>
                </button>

                {/* Expanded field details */}
                {isExpanded && (
                  <div className="px-4 pb-4 border-t border-slate-800/80">
                    <div className="grid grid-cols-1 gap-1.5 mt-3">
                      {allFields.map((field) => {
                        const rawVal = formatValue(raw[field]);
                        const sanitizedVal = formatValue(sanitized[field]);
                        const isMasked = rawVal !== sanitizedVal;
                        const fieldExists = raw[field] !== undefined || sanitized[field] !== undefined;
                        if (!fieldExists) return null;

                        return (
                          <div
                            key={field}
                            className={`grid grid-cols-[140px_1fr_1fr] gap-3 items-center px-3 py-2 rounded-lg text-xs font-mono ${
                              isMasked
                                ? 'bg-amber-500/5 border border-amber-500/10'
                                : 'bg-slate-900/60 border border-slate-800/60'
                            }`}
                          >
                            <span className="text-slate-400 font-semibold truncate" title={field}>
                              {field}
                            </span>
                            <div className="min-w-0">
                              <div className="text-[9px] text-slate-600 uppercase mb-0.5">原始值</div>
                              <div className={`truncate ${isMasked ? 'text-rose-300' : 'text-slate-300'}`} title={rawVal}>
                                {rawVal}
                              </div>
                            </div>
                            <div className="min-w-0">
                              <div className="text-[9px] text-slate-600 uppercase mb-0.5">脱敏值</div>
                              <div className={`truncate ${isMasked ? 'text-amber-300 font-semibold' : 'text-slate-300'}`} title={sanitizedVal}>
                                {sanitizedVal}
                              </div>
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};
