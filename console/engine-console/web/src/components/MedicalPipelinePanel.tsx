/**
 * 医疗敏感数据治理视图 / Medical Privacy Pipeline View
 *
 * 展示从 康养仿真医疗数据集 (kangyang.csv) 到分类分级 (3-Layer L1~L5) 与脱敏清洗 (PII + L4/L5 强抹平)
 * 的全流程治理，提供双结构数据输出（分级报告与合规清洗数据）。
 */

import { useEffect, useState } from 'react';
import type { MedicalPipelineResponse, MedicalRecordReport } from '@/types/api';
import { runMedicalPipeline } from '@/api/client';
import { Icon } from '@/components/icons';
import { getErrorMessage } from '@/utils/error';
import { getFieldDisplayName } from '@/utils/fieldLabels';

interface MedicalPipelinePanelProps {
  agentUrl?: string;
}

export default function MedicalPipelinePanel({ agentUrl }: MedicalPipelinePanelProps) {
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<MedicalPipelineResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [expandedRecord, setExpandedRecord] = useState<number | null>(1);
  const [viewEngineMode, setViewEngineMode] = useState<'both' | 'rule' | 'ner'>('both');

  const handleExecute = async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await runMedicalPipeline();
      setResult(resp);
    } catch (e) {
      setError(getErrorMessage(e));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    handleExecute();
  }, []);

  const levelBadgeCls = (level: string) => {
    switch (level) {
      case 'L5':
        return 'bg-red-100 text-red-700 border-red-200';
      case 'L4':
        return 'bg-orange-100 text-orange-700 border-orange-200';
      case 'L3':
        return 'bg-amber-100 text-amber-700 border-amber-200';
      case 'L2':
        return 'bg-blue-100 text-blue-700 border-blue-200';
      default:
        return 'bg-emerald-100 text-emerald-700 border-emerald-200';
    }
  };

  return (
    <div className="flex h-full flex-col bg-gray-50 p-6 overflow-y-auto">
      {/* 头部标题与控制工具栏 */}
      <div className="flex items-center justify-between border-b border-gray-200 bg-white px-6 py-4 rounded-xl shadow-sm">
        <div>
          <h2 className="flex items-center gap-2.5 text-lg font-bold text-gray-900">
            <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-teal-50 text-teal-600">
              <Icon name="shield" className="h-5 w-5" />
            </span>
            医疗敏感数据分类分级与脱敏全流程治理 (Medical Pipeline)
          </h2>
          <p className="mt-1 text-xs text-gray-500">
            加载康养仿真医疗数据集 <span className="font-mono font-medium text-indigo-600">kangyang.csv</span>，执行 3-Layer 分类分级（识别 L4/L5 特高风险病史）与 PII/L4/L5 强抹平脱敏。
          </p>
        </div>
        <div className="flex items-center gap-3">
          {agentUrl && (
            <span className="hidden items-center gap-1.5 rounded-full bg-gray-100 px-3 py-1 text-xs font-medium text-gray-600 sm:inline-flex">
              <span className="h-2 w-2 rounded-full bg-emerald-500" />
              Agent: {agentUrl}
            </span>
          )}
          <button
            onClick={handleExecute}
            disabled={loading}
            className="inline-flex items-center gap-2 rounded-lg bg-teal-600 px-4 py-2 text-sm font-semibold text-white shadow transition-all hover:bg-teal-700 disabled:opacity-50"
          >
            {loading ? (
              <span className="h-4 w-4 animate-spin rounded-full border-2 border-white border-t-transparent" />
            ) : (
              <Icon name="refresh" className="h-4 w-4" />
            )}
            重新加载并治理 kangyang.csv
          </button>
        </div>
      </div>

      {/* 错误提示 */}
      {error && (
        <div className="mt-4 rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">
          <strong>治理任务执行失败:</strong> {error}
        </div>
      )}

      {/* 统计指标面板 */}
      {result && result.summary && (
        <div className="mt-4 grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-6">
          <div className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
            <p className="text-xs font-medium text-gray-500">总处理记录数</p>
            <p className="mt-1 text-2xl font-extrabold text-gray-900">{result.summary?.total_records ?? 0}</p>
          </div>
          <div className="rounded-xl border border-red-200 bg-red-50/50 p-4 shadow-sm">
            <p className="text-xs font-medium text-red-600">L5 级极高风险记录</p>
            <p className="mt-1 text-2xl font-extrabold text-red-700">{result.summary?.l5_records_count ?? 0}</p>
          </div>
          <div className="rounded-xl border border-orange-200 bg-orange-50/50 p-4 shadow-sm">
            <p className="text-xs font-medium text-orange-600">L4 级高风险记录</p>
            <p className="mt-1 text-2xl font-extrabold text-orange-700">{result.summary?.l4_records_count ?? 0}</p>
          </div>
          <div className="rounded-xl border border-blue-200 bg-blue-50/50 p-4 shadow-sm">
            <p className="text-xs font-medium text-blue-600">单条脱敏 PII 字段</p>
            <p className="mt-1 text-2xl font-extrabold text-blue-700">{result.summary?.sanitized_pii_fields_per_record ?? 0} 列</p>
          </div>
          <div className="rounded-xl border border-emerald-200 bg-emerald-50/50 p-4 shadow-sm col-span-2">
            <p className="text-xs font-medium text-emerald-700">合规保障承诺</p>
            <p className="mt-1 flex items-center gap-1.5 text-sm font-bold text-emerald-800">
              <Icon name="check" className="h-4 w-4 text-emerald-600" />
              100% 抹平 L4/L5 原始高危病史词汇 (耗时 {result.summary?.duration_ms ?? 0} ms)
            </p>
          </div>
        </div>
      )}

      {/* 数据内容区：数据分类分级与脱敏治理报告 */}
      {result && (
        <div className="mt-6 flex flex-1 flex-col rounded-xl border border-gray-200 bg-white shadow-sm overflow-hidden">
          <div className="flex items-center justify-between border-b border-gray-200 bg-gray-50/80 px-4 py-3">
            <div className="flex items-center gap-2 text-sm font-bold text-gray-800">
              <Icon name="activity" className="h-4 w-4 text-teal-600" />
              数据分类分级与脱敏治理报告 ({(result.classification_report ?? []).length} 条)
            </div>
            {/* 引擎切换与对比控制按钮 */}
            <div className="flex items-center gap-2 text-xs">
              <span className="font-semibold text-gray-500">脱敏视图引擎切换:</span>
              <button
                onClick={() => setViewEngineMode('both')}
                className={`rounded-lg px-2.5 py-1 font-bold transition-all border ${
                  viewEngineMode === 'both'
                    ? 'bg-teal-600 text-white border-teal-600 shadow-2xs'
                    : 'bg-white text-gray-600 border-gray-300 hover:bg-gray-50'
                }`}
              >
                📊 双引擎对比模式 (Rule vs Small-NER)
              </button>
              <button
                onClick={() => setViewEngineMode('rule')}
                className={`rounded-lg px-2.5 py-1 font-bold transition-all border ${
                  viewEngineMode === 'rule'
                    ? 'bg-amber-600 text-white border-amber-600 shadow-2xs'
                    : 'bg-white text-gray-600 border-gray-300 hover:bg-gray-50'
                }`}
              >
                ⚡ 规则引擎 (Layer-1 Rule)
              </button>
              <button
                onClick={() => setViewEngineMode('ner')}
                className={`rounded-lg px-2.5 py-1 font-bold transition-all border ${
                  viewEngineMode === 'ner'
                    ? 'bg-indigo-600 text-white border-indigo-600 shadow-2xs'
                    : 'bg-white text-gray-600 border-gray-300 hover:bg-gray-50'
                }`}
              >
                🤖 Small-NER (Layer-2 NER)
              </button>
            </div>
          </div>

          {/* 记录列表与字段治理详情 */}
          <div className="flex-1 overflow-y-auto p-4">
            <div className="flex flex-col gap-3">
              {(result.classification_report ?? []).map((rep: MedicalRecordReport) => {
                const isExpanded = expandedRecord === rep.record_index;
                return (
                  <div
                    key={rep.record_index}
                    className="rounded-xl border border-gray-200 bg-white transition-all hover:border-gray-300 shadow-xs"
                  >
                    <div
                      onClick={() => setExpandedRecord(isExpanded ? null : rep.record_index)}
                      className="flex cursor-pointer items-center justify-between p-4"
                    >
                      <div className="flex items-center gap-3">
                        <span className="text-sm font-bold text-gray-700">记录 #{rep.record_index}</span>
                        <span className={`inline-flex items-center rounded-md border px-2.5 py-0.5 text-xs font-bold ${levelBadgeCls(rep.max_level)}`}>
                          {rep.max_level} 级风险
                        </span>
                        <div className="flex flex-wrap gap-1.5">
                          {rep.high_sensitivity_detected.map((tag, idx) => (
                            <span key={idx} className="rounded bg-red-50 px-2 py-0.5 text-xs font-medium text-red-600 border border-red-100">
                              {tag}
                            </span>
                          ))}
                        </div>
                      </div>
                      <div className="flex items-center gap-2">
                        <span className="text-xs text-gray-400">
                          {rep.field_details.length} 个字段详情
                        </span>
                        <span className="text-gray-400">{isExpanded ? '▲' : '▼'}</span>
                      </div>
                    </div>

                    {isExpanded && (
                      <div className="border-t border-gray-100 bg-gray-50/50 p-4">
                        <div className="overflow-x-auto">
                          <table className="min-w-full divide-y divide-gray-200 text-left text-xs">
                            <thead className="bg-gray-100/70 text-gray-600">
                              <tr>
                                <th className="px-3 py-2 font-bold">字段名</th>
                                <th className="px-3 py-2 font-bold">原始数据 (Original Data)</th>
                                {(viewEngineMode === 'both' || viewEngineMode === 'rule') && (
                                  <th className="px-3 py-2 font-bold text-amber-800 bg-amber-50/70">
                                    ⚡ 规则抹平数据 (Layer-1 Rule)
                                  </th>
                                )}
                                {(viewEngineMode === 'both' || viewEngineMode === 'ner') && (
                                  <th className="px-3 py-2 font-bold text-indigo-800 bg-indigo-50/70">
                                    🤖 Small-NER 实体抹平 (Layer-2 NER)
                                  </th>
                                )}
                                <th className="px-3 py-2 font-bold">等级</th>
                                <th className="px-3 py-2 font-bold">安全标签</th>
                                <th className="px-3 py-2 font-bold">命中的治理规则</th>
                                <th className="px-3 py-2 font-bold">字段描述</th>
                              </tr>
                            </thead>
                            <tbody className="divide-y divide-gray-100 bg-white">
                              {rep.field_details.map((fd, idx) => {
                                const rawVal = fd.raw_value ?? (rep.raw_record ? rep.raw_record[fd.field_name] : '');
                                const ruleVal = fd.sanitized_value_rule ?? fd.sanitized_value ?? '';
                                const nerVal = fd.sanitized_value_ner ?? fd.sanitized_value ?? '';

                                const isRuleDiff = rawVal !== ruleVal && ruleVal !== '';
                                const isNerDiff = rawVal !== nerVal && nerVal !== '';

                                return (
                                  <tr key={idx} className="hover:bg-gray-50/80 transition-colors">
                                    <td className="px-3 py-2 font-mono font-bold text-gray-800 whitespace-nowrap">{getFieldDisplayName(fd.field_name)}</td>
                                    <td className="px-3 py-2 min-w-[180px] max-w-xs break-words whitespace-pre-wrap font-mono text-gray-600 bg-gray-50 rounded px-1.5 py-0.5 border border-gray-200/60">
                                      {rawVal || '-'}
                                    </td>
                                    {(viewEngineMode === 'both' || viewEngineMode === 'rule') && (
                                      <td className="px-3 py-2 min-w-[180px] max-w-xs break-words whitespace-pre-wrap font-mono bg-amber-50/20">
                                        {isRuleDiff ? (
                                          <span className="text-amber-800 font-bold bg-amber-50 px-1.5 py-0.5 rounded border border-amber-200 shadow-2xs">
                                            {ruleVal}
                                          </span>
                                        ) : (
                                          <span className="text-gray-700">{ruleVal || '-'}</span>
                                        )}
                                      </td>
                                    )}
                                    {(viewEngineMode === 'both' || viewEngineMode === 'ner') && (
                                      <td className="px-3 py-2 min-w-[180px] max-w-xs break-words whitespace-pre-wrap font-mono bg-indigo-50/20">
                                        {isNerDiff ? (
                                          <span className="text-indigo-800 font-bold bg-indigo-50 px-1.5 py-0.5 rounded border border-indigo-200 shadow-2xs">
                                            {nerVal}
                                          </span>
                                        ) : (
                                          <span className="text-gray-700">{nerVal || '-'}</span>
                                        )}
                                      </td>
                                    )}
                                    <td className="px-3 py-2 whitespace-nowrap">
                                      <span className={`inline-block rounded px-2 py-0.5 text-[11px] font-bold border ${levelBadgeCls(fd.level)}`}>
                                        {fd.level}
                                      </span>
                                    </td>
                                    <td className="px-3 py-2 font-mono text-gray-600 whitespace-nowrap">{fd.security_tag}</td>
                                    <td className="px-3 py-2 text-indigo-600 font-medium whitespace-nowrap">{fd.rule_matched}</td>
                                    <td className="px-3 py-2 text-gray-500 whitespace-nowrap">{fd.description}</td>
                                  </tr>
                                );
                              })}
                            </tbody>
                          </table>
                        </div>
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
