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
      setExpandedRows(new Set([0]));
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
            const hubStage = session?.stages.find(s => s.name === 'hub_orchestrate');
            const effectiveStage = stageData || ((step.key === 'fetch' || step.key === 'classify_desensitize' || step.key === 'audit') ? hubStage : undefined);
            const isActive = effectiveStage?.status === 'success';
            const isError = effectiveStage?.status === 'error';
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
                  {effectiveStage && (
                    <div className={`text-[9px] font-mono mt-1 ${
                      isError ? 'text-rose-400' : isActive ? 'text-emerald-400' : 'text-slate-500'
                    }`}>
                      {effectiveStage.duration_ms}ms
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
          {((session.raw_records?.length ?? 0) > 0 || (session.sanitized_data?.length ?? 0) > 0) && (
            <DataAccordionView
              rawRecords={session.raw_records || []}
              sanitizedData={session.sanitized_data || []}
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
 * DataAccordionView — 外部数据申请方合规交付视图（手风琴展开）。
 *
 * 核心设计原则（零信任架构 · 原始数据不出域）：
 *  1. app-lz 作为模拟的外部数据申请方，仅能获取并展示治理脱敏后的合规数据（sanitizedData）；
 *  2. 原始明文数据（rawRecords）在调度中枢 service-hub 与脱敏引擎 engine-go 内部流转，严禁出域；
 *  3. 对每条交付记录提供关键 ID 摘要、脱敏字段数量统计、字段级防护状态展示；
 *  4. 支持一键展开/折叠，以及查看外部申请方实际接收到的完整 JSON 交付报文。
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
  const hasRaw = rawRecords.length > 0;

  /** 合并所有字段名（优先脱敏交付字段） */
  const allFields = useMemo(() => {
    const keys = new Set<string>();
    sanitizedData.forEach((r) => Object.keys(r).forEach((k) => keys.add(k)));
    rawRecords.forEach((r) => Object.keys(r).forEach((k) => keys.add(k)));
    return Array.from(keys);
  }, [rawRecords, sanitizedData]);

  /** 判断字段是否被脱敏处理 */
  const isFieldMasked = (field: string, rawVal: string, sanitizedVal: string): boolean => {
    if (hasRaw) {
      return rawVal !== sanitizedVal;
    }
    // 外部申请方视角：若值中包含掩码符号 '*'，即为已脱敏保护字段
    return sanitizedVal.includes('*');
  };

  /** 获取记录的摘要标签（优先使用关键 ID 或名称字段） */
  const getRecordSummary = (raw: Record<string, any>, sanitized: Record<string, any>): string => {
    const idFields = ['record_id', 'elder_id', 'id', 'patient_name', 'name', 'id_card', 'id_card_no', 'insurance_settlement_id', 'person_id'];
    for (const f of idFields) {
      if (sanitized[f] !== undefined) return `${f}: ${sanitized[f]}`;
      if (raw[f] !== undefined) return `${f}: ${raw[f]}`;
    }
    const firstKey = Object.keys(sanitized)[0] || Object.keys(raw)[0];
    if (firstKey) return `${firstKey}: ${sanitized[firstKey] ?? raw[firstKey]}`;
    return '(empty record)';
  };

  const formatValue = (v: any): string => {
    if (v === null || v === undefined) return '-';
    if (typeof v === 'object') return JSON.stringify(v);
    return String(v);
  };

  /** 统计记录中被脱敏的字段数 */
  const countMasked = (raw: Record<string, any>, sanitized: Record<string, any>): number => {
    let count = 0;
    for (const key of Object.keys(sanitized)) {
      const rawVal = formatValue(raw[key]);
      const sanitizedVal = formatValue(sanitized[key]);
      if (isFieldMasked(key, rawVal, sanitizedVal)) {
        count++;
      }
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

  return (
    <div className="bg-slate-900 border border-slate-800 rounded-2xl p-5 shadow-xl">
      {/* 零信任安全隔离声明横幅 */}
      <div className="mb-4 p-3.5 rounded-xl bg-cyan-950/20 border border-cyan-500/30 flex items-start gap-3">
        <IconShieldCheck className="w-5 h-5 text-cyan-400 shrink-0 mt-0.5" />
        <div className="text-xs">
          <div className="font-semibold text-cyan-300 flex items-center gap-2">
            <span>{t('dataApi.rawIsolated')}</span>
            <span className="text-[10px] font-mono px-2 py-0.5 rounded-full bg-cyan-900/40 text-cyan-300 border border-cyan-700/50">
              零信任数据安全隔离
            </span>
          </div>
          <div className="text-slate-400 text-[11px] mt-1 leading-relaxed">
            {t('dataApi.rawIsolatedDesc')}
          </div>
        </div>
      </div>

      {/* Header */}
      <div className="flex items-center justify-between border-b border-slate-800 pb-3 mb-4">
        <h3 className="text-sm font-bold text-slate-100 flex items-center gap-2">
          <IconLock className="w-4 h-4 text-emerald-400" />
          {t('dataApi.dataDiff')}
          <span className="text-[10px] font-mono px-2 py-0.5 rounded-full bg-emerald-950/40 text-emerald-300 border border-emerald-700/40">
            {sanitizedData.length} {t('dataApi.sanitizedRecords')}
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
            className="text-xs px-2.5 py-1 rounded bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700 transition flex items-center gap-1.5"
          >
            {showRaw ? t('dataApi.hideRaw') : t('dataApi.showRaw')}
          </button>
        </div>
      </div>

      {/* Raw / Sanitized JSON Mode */}
      {showRaw ? (
        <div className="space-y-4">
          <div className="p-3 bg-slate-950 border border-slate-800 rounded-xl text-xs text-slate-400 flex items-center justify-between">
            <div className="flex items-center gap-2">
              <span className="w-2 h-2 rounded-full bg-emerald-400" />
              <span className="font-semibold text-slate-200">外部申请方交付端点接收到的脱敏报文</span>
              <span className="text-[10px] text-slate-500">({sanitizedData.length} 条记录)</span>
            </div>
            <span className="text-[10px] font-mono text-cyan-400/90 bg-cyan-950/40 px-2 py-0.5 rounded border border-cyan-800/40">
              原始明文在 Service-Hub 边界剥离·不出域
            </span>
          </div>
          {hasRaw && (
            <div>
              <div className="text-xs font-semibold text-rose-400 mb-1 flex items-center gap-1">
                <span className="w-2 h-2 rounded-full bg-rose-400" />
                {t('dataApi.rawRecords')} ({rawRecords.length})
              </div>
              <pre className="p-3 bg-slate-950 border border-rose-500/20 rounded-xl text-xs font-mono text-rose-200/90 overflow-x-auto max-h-80">
                {JSON.stringify(rawRecords, null, 2)}
              </pre>
            </div>
          )}
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
        /* Accordion Mode — 外部交付记录手风琴 */
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
                    {maskedCount > 0 ? (
                      <span className="shrink-0 text-[10px] font-semibold px-2 py-0.5 rounded-full bg-amber-500/10 text-amber-400 border border-amber-500/20">
                        {maskedCount} 处字段已脱敏保护
                      </span>
                    ) : (
                      <span className="shrink-0 text-[10px] font-semibold px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
                        安全合规交付
                      </span>
                    )}
                    <span className="shrink-0 text-[10px] font-mono px-2 py-0.5 rounded bg-slate-800/80 text-slate-400 border border-slate-700/60">
                      原始明文不出域
                    </span>
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
                        const isMasked = isFieldMasked(field, rawVal, sanitizedVal);
                        const fieldExists = raw[field] !== undefined || sanitized[field] !== undefined;
                        if (!fieldExists) return null;

                        return (
                          <div
                            key={field}
                            className={`grid grid-cols-[160px_1fr_1fr] gap-3 items-center px-3.5 py-2.5 rounded-lg text-xs font-mono transition-all ${
                              isMasked
                                ? 'bg-amber-500/5 border border-amber-500/20 shadow-sm'
                                : 'bg-slate-900/60 border border-slate-800/60'
                            }`}
                          >
                            {/* 字段名 */}
                            <div className="flex items-center gap-1.5 min-w-0" title={field}>
                              <span className="text-slate-300 font-semibold truncate">{field}</span>
                            </div>

                            {/* 交付脱敏值 (外部申请方可见) */}
                            <div className="min-w-0">
                              <div className="text-[9px] text-slate-500 uppercase mb-0.5 font-sans">
                                交付脱敏值 (外部申请方)
                              </div>
                              <div
                                className={`truncate font-medium ${isMasked ? 'text-amber-300 font-bold' : 'text-slate-200'}`}
                                title={sanitizedVal}
                              >
                                {sanitizedVal}
                              </div>
                            </div>

                            {/* 原始明文状态 (不出域保护 / 对比) */}
                            <div className="min-w-0">
                              <div className="text-[9px] text-slate-500 uppercase mb-0.5 font-sans">
                                {hasRaw ? '原始明文 (内部验证)' : '安全保护状态'}
                              </div>
                              {hasRaw ? (
                                <div className={`truncate ${isMasked ? 'text-rose-300' : 'text-slate-400'}`} title={rawVal}>
                                  {rawVal}
                                </div>
                              ) : (
                                <div className="flex items-center gap-1 text-[11px] text-slate-400">
                                  <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${isMasked ? 'bg-amber-400' : 'bg-emerald-400'}`} />
                                  <span className="text-slate-500">原始明文不出域</span>
                                  {isMasked ? (
                                    <span className="ml-1 text-[9px] font-sans px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-400 border border-amber-500/20">
                                      已掩码脱敏
                                    </span>
                                  ) : (
                                    <span className="ml-1 text-[9px] font-sans px-1.5 py-0.5 rounded bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
                                      合规明文
                                    </span>
                                  )}
                                </div>
                              )}
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
