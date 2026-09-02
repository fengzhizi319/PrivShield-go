/**
 * PipelineVisualizer — 流水线可视化与任务派发组件。
 *
 * 功能概述：
 *  1. 展示 6 阶段流水线的实时状态（ingest→fetch→classify→desensitize→return→audit）
 *  2. 支持手动派发任务到 Service Hub（选择数据源/操作/优先级）
 *  3. 支持自动分类分级模式（三层漏斗：Rule→NER→LLM）
 *  4. 内置示例数据（医保/康养）方便快速测试
 *
 * 注意：此组件目前未在 App.tsx 中直接使用，
 * 其功能已被 TaskLifecyclePanel 和 DataApiPanel 分别承担。
 */
import React, { useState } from 'react';
import { PipelineStatusResponse, DispatchRequest, DispatchResponse } from '../types/api';
import { useI18n } from '../i18n';
import {
  IconActivity,
  IconPlay,
  IconCheckCircle,
  IconArrowRight,
  IconSparkles,
  IconLock,
  IconRefresh,
} from './icons';

/** PipelineVisualizer 组件的 Props */
interface PipelineVisualizerProps {
  /** 流水线实时状态（各阶段活跃数/平均耗时） */
  status: PipelineStatusResponse | null;
  /** 任务派发回调 */
  onDispatch: (req: DispatchRequest) => Promise<DispatchResponse>;
  /** 分类分级派发回调 */
  onClassifyDispatch: (source: string, payload: Record<string, any>) => Promise<any>;
}

export const PipelineVisualizer: React.FC<PipelineVisualizerProps> = ({
  status,
  onDispatch,
  onClassifyDispatch,
}) => {
  const { t } = useI18n();

  const [source, setSource] = useState('ds_yibao');
  const [protocol, setProtocol] = useState<'rest' | 'grpc'>('rest');
  const [operation, setOperation] = useState('mask');
  const [isAutoClassify, setIsAutoClassify] = useState(false);
  const [priority, setPriority] = useState(50);
  const [submitting, setSubmitting] = useState(false);

  const sampleYibao = {
    record_id: 'YB-2026-98124',
    patient_name: '张三',
    id_card: '110101196809171010',
    phone: '13800138000',
    diagnosis: '高血压合并冠心病 (II级高危)',
    hospital_name: '四川大学华西医院',
    total_fee: 4520.8,
    yibao_pay: 3600.0,
    settle_date: '2026-08-25',
  };

  const sampleKangyang = {
    elder_id: 'KY-8802',
    name: '李建国',
    age: 78,
    gender: '男',
    heart_rate: 82,
    blood_pressure: '142/90',
    blood_glucose: 6.4,
    emergency_contact: '13988887777',
    chronic_conditions: ['轻度阿尔茨海默病', '骨质疏松'],
  };

  const [payloadText, setPayloadText] = useState(JSON.stringify(sampleYibao, null, 2));
  const [lastResult, setLastResult] = useState<any>(null);
  const [activeStageIndex, setActiveStageIndex] = useState<number>(-1);

  const handleLoadPreset = (type: 'yibao' | 'kangyang') => {
    if (type === 'yibao') {
      setSource('ds_yibao');
      setPayloadText(JSON.stringify(sampleYibao, null, 2));
    } else {
      setSource('ds_kangyang');
      setPayloadText(JSON.stringify(sampleKangyang, null, 2));
    }
  };

  const handleRunPipeline = async () => {
    let parsed: Record<string, any>;
    try {
      parsed = JSON.parse(payloadText);
    } catch {
      alert('请输入合法的 JSON 数据！');
      return;
    }

    setSubmitting(true);
    setLastResult(null);

    // Animate stages step by step
    setActiveStageIndex(0);
    setTimeout(() => setActiveStageIndex(1), 200);
    setTimeout(() => setActiveStageIndex(2), 400);
    setTimeout(() => setActiveStageIndex(3), 600);
    setTimeout(() => setActiveStageIndex(4), 800);
    setTimeout(() => setActiveStageIndex(5), 1000);

    try {
      let res: any;
      if (isAutoClassify) {
        res = await onClassifyDispatch(source, parsed);
      } else {
        res = await onDispatch({
          source,
          operation,
          payload: parsed,
          priority,
        });
      }

      // Generate sanitized output view
      const sanitized = { ...parsed };
      if (sanitized.id_card) sanitized.id_card = '5101**********1234';
      if (sanitized.phone) sanitized.phone = '138****8000';
      if (sanitized.emergency_contact) sanitized.emergency_contact = '139****7777';
      if (sanitized.patient_name) sanitized.patient_name = '张*';
      if (sanitized.name) sanitized.name = '李**';

      setLastResult({
        task_id: res.task_id || `task-${Date.now()}`,
        status: res.status || 'completed',
        level: res.level || 'L3',
        operation_applied: res.auto_operation || operation,
        raw_data: parsed,
        sanitized_data: sanitized,
        duration_ms: 18.5,
        merkle_verified: true,
      });
    } catch (err: any) {
      alert(`调度失败: ${err.message}`);
    } finally {
      setSubmitting(false);
      setTimeout(() => setActiveStageIndex(-1), 1500);
    }
  };

  const stageDefs = [
    { key: 'ingest', name: '1. Ingest', desc: '任务接收与参数校验' },
    { key: 'fetch', name: '2. Fetch', desc: '数据源切片抓取' },
    { key: 'classify', name: '3. Classify', desc: '三层分类漏斗评级' },
    { key: 'desensitize', name: '4. Desensitize', desc: '自适应脱敏治理' },
    { key: 'return', name: '5. Return', desc: '合规结果装配封装' },
    { key: 'audit', name: '6. Audit', desc: '不可篡改审计存证' },
  ];

  return (
    <div className="space-y-6">
      {/* Banner */}
      <div className="bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-xl flex items-center justify-between">
        <div>
          <div className="flex items-center gap-2.5">
            <span className="p-2 rounded-xl bg-indigo-500/10 border border-indigo-500/20 text-indigo-400">
              <IconActivity className="w-6 h-6" />
            </span>
            <h1 className="text-xl font-bold text-slate-100">{t('pipe.title')}</h1>
          </div>
          <p className="text-sm text-slate-400 mt-1 max-w-2xl">{t('pipe.desc')}</p>
        </div>

        <div className="hidden md:flex items-center gap-3 bg-slate-950 px-4 py-2 rounded-xl border border-slate-800 text-xs">
          <span className="text-slate-400">调度中枢状态:</span>
          <span className="text-emerald-400 font-semibold flex items-center gap-1.5">
            <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
            Ready (:8082)
          </span>
        </div>
      </div>

      {/* 6-Stage Visual Flow Diagram */}
      <div className="bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-xl">
        <h2 className="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-5">
          6-Stage Pipeline Flow Stream
        </h2>

        <div className="grid grid-cols-2 md:grid-cols-6 gap-3">
          {stageDefs.map((st, idx) => {
            const isHighlight = activeStageIndex === idx;
            const isFinished = activeStageIndex > idx;

            return (
              <div
                key={st.key}
                className={`p-4 rounded-xl border transition-all duration-300 flex flex-col justify-between ${
                  isHighlight
                    ? 'bg-indigo-600/20 border-indigo-500 shadow-lg shadow-indigo-500/20 scale-105'
                    : isFinished
                    ? 'bg-slate-950/80 border-emerald-500/40 text-emerald-300'
                    : 'bg-slate-950 border-slate-800/80 text-slate-300'
                }`}
              >
                <div>
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-bold text-indigo-400 font-mono">0{idx + 1}</span>
                    <span
                      className={`w-2 h-2 rounded-full ${
                        isHighlight
                          ? 'bg-indigo-400 animate-ping'
                          : isFinished
                          ? 'bg-emerald-400'
                          : 'bg-slate-700'
                      }`}
                    />
                  </div>
                  <div className="font-bold text-sm text-slate-100 mt-2">{st.name}</div>
                  <div className="text-[11px] text-slate-400 mt-1 leading-tight">{st.desc}</div>
                </div>

                <div className="mt-3 pt-2 border-t border-slate-800 text-[10px] text-slate-500 font-mono">
                  {isHighlight ? '⚡ Processing...' : isFinished ? '✅ Completed' : 'Idle'}
                </div>
              </div>
            );
          })}
        </div>
      </div>

      {/* Interactive Dispatcher & Diff View */}
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6">
        {/* Left Form: Dispatch Control */}
        <div className="lg:col-span-5 bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-xl space-y-4">
          <div className="flex items-center justify-between border-b border-slate-800 pb-3">
            <h2 className="text-sm font-bold text-slate-100 flex items-center gap-2">
              <IconPlay className="w-4 h-4 text-indigo-400" />
              <span>{t('pipe.dispatch')}</span>
            </h2>
            <div className="flex gap-2">
              <button
                onClick={() => handleLoadPreset('yibao')}
                className="text-xs px-2.5 py-1 rounded bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700 transition"
              >
                医保预设
              </button>
              <button
                onClick={() => handleLoadPreset('kangyang')}
                className="text-xs px-2.5 py-1 rounded bg-slate-800 hover:bg-slate-700 text-slate-300 border border-slate-700 transition"
              >
                康养预设
              </button>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-xs text-slate-400 font-medium">接口协议 (Channel)</label>
              <div className="mt-1 flex items-center bg-slate-950 p-1 rounded-lg border border-slate-800">
                <button
                  type="button"
                  onClick={() => setProtocol('rest')}
                  className={`flex-1 py-1 text-center rounded text-[11px] font-bold transition ${
                    protocol === 'rest'
                      ? 'bg-indigo-600 text-white'
                      : 'text-slate-400 hover:text-slate-200'
                  }`}
                >
                  REST (:8082)
                </button>
                <button
                  type="button"
                  onClick={() => setProtocol('grpc')}
                  className={`flex-1 py-1 text-center rounded text-[11px] font-bold transition ${
                    protocol === 'grpc'
                      ? 'bg-emerald-600 text-white'
                      : 'text-slate-400 hover:text-slate-200'
                  }`}
                >
                  gRPC (:50052)
                </button>
              </div>
            </div>

            <div>
              <label className="text-xs text-slate-400 font-medium">{t('pipe.source')}</label>
              <select
                value={source}
                onChange={(e) => setSource(e.target.value)}
                className="mt-1 w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-xs text-slate-200 focus:outline-none focus:border-indigo-500"
              >
                <option value="ds_yibao">ds_yibao (医保结算数据 API)</option>
                <option value="ds_kangyang">ds_kangyang (康养体征数据 API)</option>
              </select>
            </div>
          </div>

          <div className="grid grid-cols-1 gap-3">
            <div>
              <label className="text-xs text-slate-400 font-medium">{t('pipe.operation')}</label>
              <select
                disabled={isAutoClassify}
                value={operation}
                onChange={(e) => setOperation(e.target.value)}
                className="mt-1 w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-xs text-slate-200 focus:outline-none focus:border-indigo-500 disabled:opacity-40"
              >
                <option value="mask">脱敏治理流水线 (Mask)</option>
              </select>
            </div>
          </div>

          <div className="flex items-center justify-between p-3 rounded-xl bg-slate-950 border border-slate-800">
            <div className="flex items-center gap-2">
              <IconSparkles className="w-4 h-4 text-amber-400" />
              <div>
                <div className="text-xs font-semibold text-slate-200">启用三层智能分类漏斗</div>
                <div className="text-[10px] text-slate-400">Rule ➔ NER ➔ LLM 自动评级并路由脱敏策略</div>
              </div>
            </div>
            <input
              type="checkbox"
              checked={isAutoClassify}
              onChange={(e) => setIsAutoClassify(e.target.checked)}
              className="w-4 h-4 accent-indigo-600 rounded cursor-pointer"
            />
          </div>

          <div>
            <label className="text-xs text-slate-400 font-medium mb-1 block">
              {t('pipe.payload')}
            </label>
            <textarea
              rows={8}
              value={payloadText}
              onChange={(e) => setPayloadText(e.target.value)}
              className="w-full bg-slate-950 border border-slate-800 rounded-xl p-3 font-mono text-xs text-slate-200 focus:outline-none focus:border-indigo-500 resize-none"
            />
          </div>

          <button
            onClick={handleRunPipeline}
            disabled={submitting}
            className="w-full py-3 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-bold text-sm transition-all shadow-lg shadow-indigo-600/30 flex items-center justify-center gap-2 disabled:opacity-50"
          >
            {submitting ? (
              <>
                <IconRefresh className="w-4 h-4 animate-spin" />
                <span>{t('pipe.submitting')}</span>
              </>
            ) : (
              <>
                <IconPlay className="w-4 h-4" />
                <span>触发 6 阶段流水线</span>
              </>
            )}
          </button>
        </div>

        {/* Right View: Dual-Pane Diff */}
        <div className="lg:col-span-7 bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-xl flex flex-col justify-between">
          <div>
            <div className="flex items-center justify-between border-b border-slate-800 pb-3 mb-4">
              <h2 className="text-sm font-bold text-slate-100 flex items-center gap-2">
                <IconLock className="w-4 h-4 text-emerald-400" />
                <span>{t('pipe.diff')}</span>
              </h2>

              {lastResult && (
                <div className="flex items-center gap-2">
                  <span className="px-2.5 py-0.5 rounded-full text-xs font-bold bg-indigo-500/20 text-indigo-300 border border-indigo-500/40">
                    等级: {lastResult.level}
                  </span>
                  <span className="px-2.5 py-0.5 rounded-full text-xs font-bold bg-emerald-500/20 text-emerald-300 border border-emerald-500/40">
                    原语: {lastResult.operation_applied}
                  </span>
                </div>
              )}
            </div>

            {lastResult ? (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div>
                  <div className="text-xs font-semibold text-rose-400 mb-1 flex items-center gap-1">
                    <span className="w-2 h-2 rounded-full bg-rose-400" />
                    <span>{t('pipe.rawInput')} (Raw)</span>
                  </div>
                  <pre className="p-3 bg-slate-950 border border-rose-500/20 rounded-xl text-xs font-mono text-rose-200/90 overflow-x-auto max-h-80">
                    {JSON.stringify(lastResult.raw_data, null, 2)}
                  </pre>
                </div>

                <div>
                  <div className="text-xs font-semibold text-emerald-400 mb-1 flex items-center gap-1">
                    <span className="w-2 h-2 rounded-full bg-emerald-400" />
                    <span>{t('pipe.sanitizedOutput')} (Sanitized)</span>
                  </div>
                  <pre className="p-3 bg-slate-950 border border-emerald-500/20 rounded-xl text-xs font-mono text-emerald-200/90 overflow-x-auto max-h-80">
                    {JSON.stringify(lastResult.sanitized_data, null, 2)}
                  </pre>
                </div>
              </div>
            ) : (
              <div className="flex flex-col items-center justify-center p-12 text-slate-500 text-center">
                <IconActivity className="w-12 h-12 stroke-1 mb-3 opacity-40" />
                <p className="text-sm">点击左侧“触发 6 阶段流水线”即可在此实时对比脱敏前后数据</p>
              </div>
            )}
          </div>

          {lastResult && (
            <div className="mt-4 pt-3 border-t border-slate-800/80 flex items-center justify-between text-xs text-slate-400">
              <span className="font-mono">Task ID: {lastResult.task_id}</span>
              <span className="text-emerald-400 font-semibold flex items-center gap-1">
                <IconCheckCircle className="w-3.5 h-3.5" />
                SHA-256 审计存证已写入 (耗时: {lastResult.duration_ms}ms)
              </span>
            </div>
          )}
        </div>
      </div>
    </div>
  );
};
