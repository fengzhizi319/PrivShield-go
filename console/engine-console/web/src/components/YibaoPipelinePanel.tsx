/**
 * 医保结算数据治理视图 / Medical Insurance Privacy Pipeline View (yibao.csv)
 *
 * 展示从 yibao.csv (18 字段) 到分类分级 (3-Layer L1~L5) 与脱敏清洗 (PII + L4/L5 强抹平)
 * 的分场景演示全流程治理，提供双结构数据输出（分级报告与合规清洗数据）。
 */

import { useEffect, useState } from 'react';
import type { MedicalFieldClassification, MedicalPipelineResponse, MedicalRecordReport } from '@/types/api';
import { runYibaoPipeline } from '@/api/client';
import { Icon } from '@/components/icons';
import { getErrorMessage } from '@/utils/error';
import { getFieldDisplayName } from '@/utils/fieldLabels';

interface YibaoPipelinePanelProps {
  agentUrl?: string;
}

type ScenarioFilter = 'all' | 'l5' | 'l4' | 'pii';

export default function YibaoPipelinePanel({ agentUrl }: YibaoPipelinePanelProps) {
  const [loading, setLoading] = useState(false);
  const [result, setResult] = useState<MedicalPipelineResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [expandedRecord, setExpandedRecord] = useState<number | null>(1);
  const [activeScenario, setActiveScenario] = useState<ScenarioFilter>('all');
  const [activeTab, setActiveTab] = useState<'report' | 'sanitized'>('report');

  const handleExecute = async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await runYibaoPipeline();
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

  // 根据选中的场景过滤记录
  const filteredRecords = result?.classification_report?.filter((r: MedicalRecordReport) => {
    if (activeScenario === 'l5') return r.max_level === 'L5';
    if (activeScenario === 'l4') return r.max_level === 'L4';
    if (activeScenario === 'pii') return (r.pii_fields_detected?.length ?? 0) > 0;
    return true;
  }) || [];

  return (
    <div className="flex h-full flex-col bg-gray-50 p-6 overflow-y-auto">
      {/* 头部标题与控制工具栏 */}
      <div className="flex items-center justify-between border-b border-gray-200 bg-white px-6 py-4 rounded-xl shadow-sm">
        <div>
          <h2 className="flex items-center gap-2.5 text-lg font-bold text-gray-900">
            <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-cyan-50 text-cyan-600">
              <Icon name="shield" className="h-5 w-5" />
            </span>
            医保结算数据治理与分场景脱敏演示 (Yibao Pipeline)
          </h2>
          <p className="mt-1 text-xs text-gray-500">
            加载 18 字段标准医保结算数据 <span className="font-mono font-medium text-cyan-700">yibao.csv</span>，执行 ICD-10 诊断高敏词擦除、人员 PID/结算流水号格式掩码与分场景演示。
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
            className="inline-flex items-center gap-2 rounded-lg bg-cyan-600 px-4 py-2 text-sm font-semibold text-white shadow transition-all hover:bg-cyan-700 disabled:opacity-50"
          >
            {loading ? (
              <span className="h-4 w-4 animate-spin rounded-full border-2 border-white border-t-transparent" />
            ) : (
              <Icon name="refresh" className="h-4 w-4" />
            )}
            重新加载并治理 yibao.csv (50条)
          </button>
        </div>
      </div>

      {/* 错误提示 */}
      {error && (
        <div className="mt-4 rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">
          <strong>医保治理任务执行失败:</strong> {error}
        </div>
      )}

      {/* 统计指标面板 */}
      {result && result.summary && (
        <div className="mt-4 grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-6">
          <div className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
            <p className="text-xs font-medium text-gray-500">医保结算总记录</p>
            <p className="mt-1 text-2xl font-extrabold text-gray-900">{result.summary?.total_records ?? 0}</p>
          </div>
          <div className="rounded-xl border border-red-200 bg-red-50/50 p-4 shadow-sm">
            <p className="text-xs font-medium text-red-600">L5 极高敏诊断记录</p>
            <p className="mt-1 text-2xl font-extrabold text-red-700">{result.summary?.l5_records_count ?? 0}</p>
          </div>
          <div className="rounded-xl border border-orange-200 bg-orange-50/50 p-4 shadow-sm">
            <p className="text-xs font-medium text-orange-600">L4 高敏诊断记录</p>
            <p className="mt-1 text-2xl font-extrabold text-orange-700">{result.summary?.l4_records_count ?? 0}</p>
          </div>
          <div className="rounded-xl border border-blue-200 bg-blue-50/50 p-4 shadow-sm">
            <p className="text-xs font-medium text-blue-600">医保 PID 掩码字段</p>
            <p className="mt-1 text-2xl font-extrabold text-blue-700">
              {result.summary?.sanitized_pii_fields_per_record ?? 0} 列/条
            </p>
          </div>
          <div className={`rounded-xl border p-4 shadow-sm col-span-2 ${
            result.summary?.guarantee_no_l4_l5_raw_data
              ? 'border-cyan-200 bg-cyan-50/50'
              : 'border-red-300 bg-red-50/50'
          }`}>
            <p className={`text-xs font-medium ${result.summary?.guarantee_no_l4_l5_raw_data ? 'text-cyan-700' : 'text-red-700'}`}>
              安全防护保证 (Guaranteed)
            </p>
            {result.summary?.guarantee_no_l4_l5_raw_data ? (
              <div className="mt-1 flex items-center gap-2">
                <span className="inline-flex items-center gap-1 rounded-md bg-emerald-600 px-2 py-0.5 text-xs font-bold text-white">
                  <Icon name="check" className="h-3.5 w-3.5" /> 100% 合格
                </span>
                <span className="text-xs text-cyan-900 font-medium">无高敏词泄漏</span>
              </div>
            ) : (
              <div className="mt-1 flex items-center gap-2">
                <span className="inline-flex items-center gap-1 rounded-md bg-red-600 px-2 py-0.5 text-xs font-bold text-white">
                  合规校验未通过
                </span>
                <span className="text-xs text-red-900 font-medium">
                  打码失败 {result.summary?.redaction_failures ?? 0} 处 / 门禁整值删除 {result.summary?.fail_safe_triggered_fields ?? 0} 字段
                </span>
              </div>
            )}
          </div>
        </div>
      )}

      {/* 演示场景与视图模式切换器 */}
      {result && (
        <div className="mt-6 flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-4 border-b border-gray-200 pb-3">
          {/* 左侧：分场景演示筛选 */}
          <div className="flex items-center gap-2 overflow-x-auto pb-1 sm:pb-0">
            <span className="text-xs font-bold text-gray-500 whitespace-nowrap">演示场景:</span>
            <button
              onClick={() => setActiveScenario('all')}
              className={`rounded-lg px-3 py-1.5 text-xs font-medium transition-all ${
                activeScenario === 'all'
                  ? 'bg-cyan-600 text-white shadow-sm'
                  : 'bg-white text-gray-600 hover:bg-gray-100 border border-gray-200'
              }`}
            >
              全部医保记录 ({result.classification_report?.length || 0})
            </button>
            <button
              onClick={() => setActiveScenario('l5')}
              className={`rounded-lg px-3 py-1.5 text-xs font-medium transition-all ${
                activeScenario === 'l5'
                  ? 'bg-red-600 text-white shadow-sm'
                  : 'bg-white text-red-600 hover:bg-red-50 border border-red-200'
              }`}
            >
              L5 极高敏诊断 (HIV/性病/重度精神)
            </button>
            <button
              onClick={() => setActiveScenario('l4')}
              className={`rounded-lg px-3 py-1.5 text-xs font-medium transition-all ${
                activeScenario === 'l4'
                  ? 'bg-orange-600 text-white shadow-sm'
                  : 'bg-white text-orange-600 hover:bg-orange-50 border border-orange-200'
              }`}
            >
              L4 重大高敏诊断 (恶性肿瘤/乙肝)
            </button>
            <button
              onClick={() => setActiveScenario('pii')}
              className={`rounded-lg px-3 py-1.5 text-xs font-medium transition-all ${
                activeScenario === 'pii'
                  ? 'bg-blue-600 text-white shadow-sm'
                  : 'bg-white text-blue-600 hover:bg-blue-50 border border-blue-200'
              }`}
            >
              医保流水/PID 掩码
            </button>
          </div>

          {/* 右侧：分栏视图切换 (1. 18 字段分级报告 / 2. 脱敏清洗数据) */}
          <div className="flex items-center gap-1 rounded-lg bg-gray-200 p-1">
            <button
              onClick={() => setActiveTab('report')}
              className={`rounded-md px-3 py-1 text-xs font-semibold transition-all ${
                activeTab === 'report' ? 'bg-white text-gray-900 shadow-sm' : 'text-gray-600 hover:text-gray-900'
              }`}
            >
              1. 18 字段分级报告
            </button>
            <button
              onClick={() => setActiveTab('sanitized')}
              className={`rounded-md px-3 py-1 text-xs font-semibold transition-all ${
                activeTab === 'sanitized' ? 'bg-white text-gray-900 shadow-sm' : 'text-gray-600 hover:text-gray-900'
              }`}
            >
              2. 脱敏清洗数据
            </button>
          </div>
        </div>
      )}

      {/* 主视图内容区域 */}
      {result && (
        <div className="mt-4 flex-1">
          {activeTab === 'report' ? (
            /* 视图 1：18 字段分级报告 */
            <div className="space-y-4">
              {filteredRecords.length === 0 ? (
                <div className="rounded-xl border border-gray-200 bg-white p-8 text-center text-sm text-gray-500">
                  当前演示场景下无匹配的医保记录。
                </div>
              ) : (
                filteredRecords.map((rec: MedicalRecordReport) => {
                  const isExpanded = expandedRecord === rec.record_index;
                  const pidField = rec.field_details.find((f: MedicalFieldClassification) => f.field_name === 'person_id');
                  return (
                    <div key={rec.record_index} className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
                      {/* 记录标头手风琴触控 */}
                      <button
                        onClick={() => setExpandedRecord(isExpanded ? null : rec.record_index)}
                        className="flex w-full items-center justify-between bg-gray-50 px-5 py-3.5 text-left hover:bg-gray-100/80 transition-colors"
                      >
                        <div className="flex items-center gap-3">
                          <span className="flex h-6 w-6 items-center justify-center rounded-full bg-cyan-100 text-xs font-bold text-cyan-800">
                            #{rec.record_index}
                          </span>
                          <span className="text-sm font-bold text-gray-900">
                            医保记录 #{rec.record_index} (PID: {pidField?.sanitized_value || '***'})
                          </span>
                          <span className={`rounded-md border px-2 py-0.5 text-xs font-bold ${levelBadgeCls(rec.max_level)}`}>
                            最高等级: {rec.max_level}
                          </span>
                        </div>
                        <div className="flex items-center gap-2">
                          <span className="text-xs text-gray-400">展开 18 字段明细</span>
                          <Icon name={isExpanded ? 'chevron-down' : 'chevron-right'} className="h-4 w-4 text-gray-400" />
                        </div>
                      </button>

                      {/* 展开的 18 字段分级表格 */}
                      {isExpanded && (
                        <div className="p-5 border-t border-gray-200">
                          <div className="overflow-x-auto">
                            <table className="min-w-full divide-y divide-gray-200 text-xs">
                              <thead className="bg-gray-100 text-gray-600 font-bold">
                                <tr>
                                  <th className="px-3 py-2 text-left">字段 Key</th>
                                  <th className="px-3 py-2 text-left">风险等级</th>
                                  <th className="px-3 py-2 text-left">原始属性值</th>
                                  <th className="px-3 py-2 text-left">脱敏治理输出</th>
                                  <th className="px-3 py-2 text-left">安全 PII/识别规则</th>
                                </tr>
                              </thead>
                              <tbody className="divide-y divide-gray-100 bg-white">
                                {rec.field_details.map((fieldInfo: MedicalFieldClassification) => (
                                  <tr key={fieldInfo.field_name} className="hover:bg-cyan-50/30">
                                    <td className="px-3 py-2.5 font-mono font-medium text-cyan-900">{getFieldDisplayName(fieldInfo.field_name)}</td>
                                    <td className="px-3 py-2.5">
                                      <span className={`rounded border px-1.5 py-0.5 font-bold ${levelBadgeCls(fieldInfo.level)}`}>
                                        {fieldInfo.level}
                                      </span>
                                    </td>
                                    <td className="px-3 py-2.5 font-mono text-gray-600 truncate max-w-xs">{fieldInfo.raw_value}</td>
                                    <td className="px-3 py-2.5 font-mono font-bold text-gray-900 truncate max-w-xs">
                                      {fieldInfo.sanitized_value}
                                    </td>
                                    <td className="px-3 py-2.5 text-gray-500">
                                      {fieldInfo.sanitized_value_rule || fieldInfo.rule_matched ? (
                                        <span className="rounded bg-blue-50 px-1.5 py-0.5 font-mono text-blue-700 border border-blue-200">
                                          {fieldInfo.sanitized_value_rule || fieldInfo.rule_matched}
                                        </span>
                                      ) : (
                                        <span className="text-gray-400">-</span>
                                      )}
                                    </td>
                                  </tr>
                                ))}
                              </tbody>
                            </table>
                          </div>
                        </div>
                      )}
                    </div>
                  );
                })
              )}
            </div>
          ) : (
            /* 视图 2：合规脱敏清洗数据 (`sanitized_data`) */
            <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
              <div className="flex items-center justify-between border-b border-gray-100 pb-3 mb-4">
                <h3 className="text-sm font-bold text-gray-900 flex items-center gap-2">
                  <Icon name="check" className="h-4 w-4 text-emerald-600" />
                  清洗合规数据视图（50 条医保记录已被彻底剥离 L4/L5 风险诊断与 PII）
                </h3>
                <span className="text-xs text-gray-500">可在外发科研、数据共享或大模型调用前安全传输</span>
              </div>
              <div className="overflow-x-auto">
                <table className="min-w-full divide-y divide-gray-200 text-xs">
                  <thead className="bg-gray-100 text-gray-700 font-bold">
                    <tr>
                      <th className="px-3 py-2 text-left">#</th>
                      <th className="px-3 py-2 text-left">{getFieldDisplayName('insurance_settlement_id')}</th>
                      <th className="px-3 py-2 text-left">{getFieldDisplayName('person_id')}</th>
                      <th className="px-3 py-2 text-left">{getFieldDisplayName('gender')}</th>
                      <th className="px-3 py-2 text-left">{getFieldDisplayName('admission_dept')}</th>
                      <th className="px-3 py-2 text-left">{getFieldDisplayName('icd10_code')}</th>
                      <th className="px-3 py-2 text-left">{getFieldDisplayName('diagnosis_name')}</th>
                      <th className="px-3 py-2 text-left">{getFieldDisplayName('admission_condition')}</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100 bg-white">
                    {result.sanitized_data.map((r: Record<string, string>, idx: number) => (
                      <tr key={idx} className="hover:bg-gray-50">
                        <td className="px-3 py-2.5 font-bold text-gray-400">{idx + 1}</td>
                        <td className="px-3 py-2.5 font-mono text-gray-600">{r.insurance_settlement_id}</td>
                        <td className="px-3 py-2.5 font-mono text-blue-700 font-bold">{r.person_id}</td>
                        <td className="px-3 py-2.5 text-gray-700">{r.gender}</td>
                        <td className="px-3 py-2.5 text-gray-700">{r.admission_dept}</td>
                        <td className="px-3 py-2.5 font-mono text-purple-700">{r.icd10_code || <span className="text-gray-400 italic">[已抹平]</span>}</td>
                        <td className="px-3 py-2.5 font-bold text-gray-900 max-w-sm truncate">{r.diagnosis_name || <span className="text-gray-400 italic">[已零痕迹抹平]</span>}</td>
                        <td className="px-3 py-2.5 text-gray-700">{r.admission_condition}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
