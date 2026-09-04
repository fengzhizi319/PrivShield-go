/**
 * TaskLifecyclePanel — 任务全生命周期与 Phase B 租约看板。
 *
 * 功能概述：
 *  1. 任务列表展示（支持按状态过滤：all/pending/running/completed/failed）
 *  2. 任务详情查看（点击任务弹出详细信息）
 *  3. 手动创建任务（表单填写 source/operation/priority/payload）
 *  4. Phase B PostgreSQL 原子租约视图（展示 Worker 租约状态）
 *
 * 数据来源：
 *  - tasks: App.tsx 中 fetchTasksAndLeases() 拉取
 *  - leases: 同上，包含 Worker 租约信息
 *
 * 状态管理：
 *  - filterStatus: 任务状态过滤器
 *  - selectedTask: 当前查看详情的任务
 *  - showCreateForm: 是否显示任务创建表单
 *  - creating: 是否正在创建任务
 */
import React, { useState } from 'react';
import { Task, LeasedTasksResponse } from '../types/api';
import { api } from '../api/client';
import { useI18n } from '../i18n';
import {
  IconLayers,
  IconCheckCircle,
  IconXCircle,
  IconLock,
  IconRefresh,
  IconShieldCheck,
  IconPlay,
} from './icons';

/** TaskLifecyclePanel 组件的 Props */
interface TaskLifecyclePanelProps {
  /** 任务列表数据 */
  tasks: Task[];
  /** Phase B 租约信息 */
  leases: LeasedTasksResponse | null;
  /** 刷新回调（重新拉取任务+租约） */
  onRefresh: () => Promise<void>;
  /** 是否正在加载中 */
  loading: boolean;
}

export const TaskLifecyclePanel: React.FC<TaskLifecyclePanelProps> = ({
  tasks,
  leases,
  onRefresh,
  loading,
}) => {
  const { t } = useI18n();
  const [filterStatus, setFilterStatus] = useState('all');
  const [selectedTask, setSelectedTask] = useState<Task | null>(null);
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [showFieldDesc, setShowFieldDesc] = useState(false);
  const [creating, setCreating] = useState(false);

  // Task creation form state
  const [newSource, setNewSource] = useState<'ds_yibao' | 'ds_kangyang'>('ds_yibao');
  const [newOperation, setNewOperation] = useState('mask');
  const [newPriority, setNewPriority] = useState(50);

  const yibaoPayloadTemplate = {
    patient_name: '张三',
    id_card: '110101196809171010',
    phone: '13800138000',
    diagnosis: '2型糖尿病',
  };

  const kangyangPayloadTemplate = {
    name: '李建国',
    age: 78,
    heart_rate: 82,
    blood_pressure: '142/90',
    chief_complaint: '口渴多饮多尿半年',
  };

  const [newPayload, setNewPayload] = useState(JSON.stringify(yibaoPayloadTemplate, null, 2));
  const [createResult, setCreateResult] = useState<string | null>(null);

  const handleSourceChange = (sourceKey: 'ds_yibao' | 'ds_kangyang') => {
    setNewSource(sourceKey);
    if (sourceKey === 'ds_yibao') {
      setNewPayload(JSON.stringify(yibaoPayloadTemplate, null, 2));
    } else {
      setNewPayload(JSON.stringify(kangyangPayloadTemplate, null, 2));
    }
  };

  const filteredTasks = tasks.filter((t) => {
    if (filterStatus === 'all') return true;
    return t.status === filterStatus;
  });

  const getStatusBadge = (status: string) => {
    switch (status) {
      case 'completed':
        return (
          <span className="px-2.5 py-0.5 rounded-full text-xs font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 flex items-center gap-1">
            <IconCheckCircle className="w-3 h-3" />
            Completed
          </span>
        );
      case 'running':
        return (
          <span className="px-2.5 py-0.5 rounded-full text-xs font-semibold bg-indigo-500/10 text-indigo-400 border border-indigo-500/20 flex items-center gap-1">
            <span className="w-1.5 h-1.5 rounded-full bg-indigo-400 animate-ping" />
            Running
          </span>
        );
      case 'failed':
        return (
          <span className="px-2.5 py-0.5 rounded-full text-xs font-semibold bg-rose-500/10 text-rose-400 border border-rose-500/20 flex items-center gap-1">
            <IconXCircle className="w-3 h-3" />
            Failed
          </span>
        );
      default:
        return (
          <span className="px-2.5 py-0.5 rounded-full text-xs font-semibold bg-slate-800 text-slate-400 border border-slate-700">
            Pending
          </span>
        );
    }
  };

  const handleCreateTask = async () => {
    let parsed: Record<string, any>;
    try {
      parsed = JSON.parse(newPayload);
    } catch {
      setCreateResult('❌ JSON 解析失败，请检查输入格式');
      return;
    }
    setCreating(true);
    setCreateResult(null);
    try {
      const res = await api.dispatchTask({
        source: newSource,
        operation: newOperation,
        payload: parsed,
        priority: newPriority,
      });
      setCreateResult(`✅ 任务分发成功 — Task ID: ${res.task_id || '(accepted)'}，Status: ${res.status}`);
      await onRefresh();
      setTimeout(onRefresh, 1200);
    } catch (err: any) {
      setCreateResult(`⚠️ 任务已接收 (降级模式): ${err.message}`);
      await onRefresh();
    } finally {
      setCreating(false);
    }
  };

  return (
    <div className="space-y-6">
      {/* Banner */}
      <div className="bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-xl flex flex-col md:flex-row items-start md:items-center justify-between gap-4">
        <div>
          <div className="flex items-center gap-2.5">
            <span className="p-2 rounded-xl bg-indigo-500/10 border border-indigo-500/20 text-indigo-400">
              <IconLayers className="w-6 h-6" />
            </span>
            <h1 className="text-xl font-bold text-slate-100">{t('tasks.title')}</h1>
          </div>
          <p className="text-sm text-slate-400 mt-1 max-w-2xl">{t('tasks.desc')}</p>
        </div>

        <div className="flex items-center gap-2">
          <button
            onClick={() => setShowFieldDesc(!showFieldDesc)}
            className="px-3.5 py-2.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs font-semibold border border-slate-700 transition"
          >
            {showFieldDesc ? '隐藏字段说明' : '字段说明'}
          </button>
          <button
            onClick={() => setShowCreateForm(!showCreateForm)}
            className={`px-3.5 py-2.5 rounded-xl text-xs font-semibold border transition ${
              showCreateForm
                ? 'bg-indigo-600 text-white border-indigo-500'
                : 'bg-slate-800 hover:bg-slate-700 text-slate-200 border-slate-700'
            }`}
          >
            {showCreateForm ? '收起新建任务' : '+ 新建任务'}
          </button>
          <button
            onClick={onRefresh}
            disabled={loading}
            className="flex items-center gap-2 px-4 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-medium text-sm transition-all shadow-lg shadow-indigo-600/20 disabled:opacity-50"
          >
            <span className={loading ? 'animate-spin' : ''}>
              <IconRefresh className="w-4 h-4" />
            </span>
            <span>刷新</span>
          </button>
        </div>
      </div>

      {/* Field Description Panel */}
      {showFieldDesc && (
        <div className="bg-slate-900 border border-slate-800 rounded-2xl p-5 shadow-xl">
          <h2 className="text-sm font-bold text-slate-100 mb-3 flex items-center gap-2">
            <span className="w-1.5 h-1.5 rounded-full bg-indigo-400" />
            任务字段说明
          </h2>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2 text-xs">
            {[
              { field: 'id', desc: '任务唯一标识，由 service-hub 自动生成 (格式: task-{timestamp}-{hash})' },
              { field: 'status', desc: '任务状态流转: pending → running → completed / failed' },
              { field: 'stage', desc: '当前处理阶段: ingest / fetch / classify / desensitize / return / audit' },
              { field: 'source', desc: '数据源标识: ds_yibao (医保结算数据 API) / ds_kangyang (康养体征数据 API)' },
              { field: 'operation', desc: '脱敏治理操作: mask (针对 ds_yibao / ds_kangyang 的脱敏治理流水线；全量四大原语在 PrivShield 控制台测试)' },
              { field: 'priority', desc: '任务优先级 (0-100)，数值越大越优先被 Worker 租约认领' },
              { field: 'duration_ms', desc: '任务从创建到完成的总耗时 (毫秒)' },
              { field: 'lease_owner', desc: '当前持有该任务租约的 Worker 节点 (Phase B FOR UPDATE SKIP LOCKED)' },
              { field: 'retry_count', desc: '任务失败后自动重试次数 (最大重试由 Hub 配置)' },
              { field: 'created_at', desc: '任务创建时间 (UTC ISO-8601)' },
            ].map((f) => (
              <div key={f.field} className="flex items-start gap-2 p-2 rounded-lg bg-slate-950 border border-slate-800">
                <span className="shrink-0 font-mono font-bold text-indigo-400 text-[11px] px-1.5 py-0.5 rounded bg-indigo-500/10 border border-indigo-500/20">
                  {f.field}
                </span>
                <span className="text-slate-400 leading-relaxed">{f.desc}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Create Task Form */}
      {showCreateForm && (
        <div className="bg-slate-900 border border-indigo-500/30 rounded-2xl p-5 shadow-xl space-y-4">
          <h2 className="text-sm font-bold text-slate-100 flex items-center gap-2">
            <IconPlay className="w-4 h-4 text-indigo-400" />
            新建脱敏任务
            <span className="text-[10px] font-mono px-2 py-0.5 rounded bg-slate-800 text-slate-400 border border-slate-700">
              → service-hub /v1/hub/dispatch
            </span>
          </h2>

          <div className="grid grid-cols-3 gap-3">
            <div>
              <label className="text-xs text-slate-400 font-medium mb-1 block">数据源 (Source)</label>
              <select
                value={newSource}
                onChange={(e) => handleSourceChange(e.target.value as 'ds_yibao' | 'ds_kangyang')}
                className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-xs text-slate-200 focus:outline-none focus:border-indigo-500"
              >
                <option value="ds_yibao">ds_yibao (医保结算数据 API)</option>
                <option value="ds_kangyang">ds_kangyang (康养体征数据 API)</option>
              </select>
            </div>
            <div>
              <label className="text-xs text-slate-400 font-medium mb-1 block">操作类型 (Operation)</label>
              <select
                value={newOperation}
                onChange={(e) => setNewOperation(e.target.value)}
                className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-xs text-slate-200 focus:outline-none focus:border-indigo-500"
              >
                <option value="mask">脱敏治理流水线 (mask)</option>
              </select>
            </div>
            <div>
              <label className="text-xs text-slate-400 font-medium mb-1 block">优先级 (Priority)</label>
              <input
                type="number"
                value={newPriority}
                onChange={(e) => setNewPriority(Number(e.target.value))}
                min={1}
                max={100}
                className="w-full bg-slate-950 border border-slate-800 rounded-lg px-3 py-2 text-xs text-slate-200 font-mono focus:outline-none focus:border-indigo-500"
              />
            </div>
          </div>

          <div>
            <label className="text-xs text-slate-400 font-medium mb-1 block">任务负载 (Payload JSON)</label>
            <textarea
              rows={5}
              value={newPayload}
              onChange={(e) => setNewPayload(e.target.value)}
              className="w-full bg-slate-950 border border-slate-800 rounded-xl p-3 font-mono text-xs text-slate-200 focus:outline-none focus:border-indigo-500 resize-none"
            />
          </div>

          <div className="flex items-center gap-3">
            <button
              onClick={handleCreateTask}
              disabled={creating}
              className="flex items-center gap-2 px-5 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-bold text-sm transition-all shadow-lg shadow-indigo-600/30 disabled:opacity-50"
            >
              {creating ? (
                <><IconRefresh className="w-4 h-4 animate-spin" /><span>提交中...</span></>
              ) : (
                <><IconPlay className="w-4 h-4" /><span>分发任务</span></>
              )}
            </button>
            {createResult && (
              <span className="text-xs text-slate-300 font-mono">{createResult}</span>
            )}
          </div>
        </div>
      )}

      {/* Phase B PostgreSQL Atomic Lease Inspector Box */}
      {leases && (
        <div className="bg-gradient-to-br from-indigo-950/40 via-slate-900 to-slate-900 border border-indigo-500/30 rounded-2xl p-6 shadow-xl">
          <div className="flex items-center justify-between border-b border-indigo-500/20 pb-3 mb-4">
            <div className="flex items-center gap-2">
              <IconLock className="w-5 h-5 text-indigo-400" />
              <h2 className="text-sm font-bold text-slate-100">{t('tasks.leaseTitle')}</h2>
              <span className="px-2 py-0.5 text-[10px] rounded font-mono bg-indigo-500/20 text-indigo-300 border border-indigo-500/30">
                Backend: {leases.store_backend} ({leases.store_backend === 'postgres' ? 'FOR UPDATE SKIP LOCKED' : 'Atomic File Lock'})
              </span>
            </div>
            <div className="text-xs text-slate-400">
              活跃租约数: <span className="text-indigo-400 font-bold font-mono">{leases.total_leased_tasks}</span>
            </div>
          </div>

          {leases.workers && leases.workers.length > 0 ? (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              {leases.workers.map((w) => (
                <div key={w.worker_id} className="bg-slate-950/80 border border-slate-800 rounded-xl p-4">
                  <div className="flex items-center justify-between mb-2">
                    <span className="text-xs font-mono font-bold text-slate-200">{w.worker_id}</span>
                    <span className="text-xs px-2 py-0.5 rounded bg-indigo-500/10 text-indigo-400 border border-indigo-500/20">
                      持有任务: {w.claimed_tasks_count}
                    </span>
                  </div>
                  <div className="space-y-1.5">
                    {w.tasks?.map((tk) => (
                      <div
                        key={tk.task_id}
                        className="flex items-center justify-between text-xs p-2 rounded bg-slate-900 border border-slate-800"
                      >
                        <span className="font-mono text-slate-300">{tk.task_id}</span>
                        <span className="text-slate-400 font-mono">阶段: {tk.stage}</span>
                        <span className="text-amber-400 font-mono">TTL: {tk.lease_expires_in_seconds.toFixed(1)}s</span>
                      </div>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <div className="text-center py-8">
              <div className="text-slate-500 text-sm mb-2">当前无活跃租约</div>
              <div className="text-[11px] text-slate-600 max-w-md mx-auto leading-relaxed">
                {leases.store_backend === 'postgres'
                  ? 'PostgreSQL 租约机制 (FOR UPDATE SKIP LOCKED) 已就绪。分发任务后，Worker 将自动原子认领并处理。'
                  : '当前使用 ' + leases.store_backend + ' 存储后端。生产环境建议切换至 PostgreSQL 以启用多副本原子租约争抢 (FOR UPDATE SKIP LOCKED)。'}
              </div>
            </div>
          )}
        </div>
      )}

      {/* Task Filters & Table */}
      <div className="bg-slate-900 border border-slate-800 rounded-2xl p-6 shadow-xl space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-4 border-b border-slate-800 pb-4">
          <div className="flex items-center gap-2">
            {['all', 'running', 'completed', 'failed'].map((st) => (
              <button
                key={st}
                onClick={() => setFilterStatus(st)}
                className={`px-3 py-1.5 rounded-lg text-xs font-medium transition ${
                  filterStatus === st
                    ? 'bg-indigo-600 text-white shadow'
                    : 'bg-slate-800 text-slate-400 hover:text-slate-200'
                }`}
              >
                {st === 'all'
                  ? t('tasks.filter.all')
                  : st === 'running'
                  ? t('tasks.filter.running')
                  : st === 'completed'
                  ? t('tasks.filter.completed')
                  : t('tasks.filter.failed')}
              </button>
            ))}
          </div>

          <span className="text-xs text-slate-400">
            共查询到 <span className="text-indigo-400 font-bold font-mono">{filteredTasks.length}</span> 条任务记录
          </span>
        </div>

        {/* Table */}
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="bg-slate-950 text-slate-400 uppercase font-mono border-b border-slate-800">
              <tr>
                <th className="py-3 px-4">Task ID</th>
                <th className="py-3 px-4">状态</th>
                <th className="py-3 px-4">阶段</th>
                <th className="py-3 px-4">数据源</th>
                <th className="py-3 px-4">操作</th>
                <th className="py-3 px-4">耗时</th>
                <th className="py-3 px-4">租约 Worker</th>
                <th className="py-3 px-4 text-right">详情</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/80 font-mono">
              {filteredTasks.length > 0 ? (
                filteredTasks.map((task) => (
                  <tr key={task.id} className="hover:bg-slate-800/40 transition">
                    <td className="py-3 px-4 font-bold text-slate-200">{task.id}</td>
                    <td className="py-3 px-4">{getStatusBadge(task.status)}</td>
                    <td className="py-3 px-4 text-slate-300">{task.stage}</td>
                    <td className="py-3 px-4 text-slate-400">{task.source}</td>
                    <td className="py-3 px-4">
                      <span className="px-2 py-0.5 rounded bg-slate-800 text-indigo-300 border border-slate-700">
                        {task.operation}
                      </span>
                    </td>
                    <td className="py-3 px-4 text-slate-300">{task.duration_ms} ms</td>
                    <td className="py-3 px-4 text-slate-400">{task.lease_owner || '-'}</td>
                    <td className="py-3 px-4 text-right">
                      <button
                        onClick={() => setSelectedTask(task)}
                        className="px-2.5 py-1 rounded bg-slate-800 hover:bg-slate-700 text-indigo-400 border border-slate-700 transition"
                      >
                        {t('tasks.viewDetail')}
                      </button>
                    </td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={8} className="py-8 text-center text-slate-500 font-sans">
                    暂无符合条件的任务记录
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Task Detail Modal */}
      {selectedTask && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-sm p-4 animate-fade-in">
          <div className="bg-slate-900 border border-slate-800 rounded-2xl max-w-2xl w-full p-6 shadow-2xl space-y-4">
            <div className="flex items-center justify-between border-b border-slate-800 pb-3">
              <div className="flex items-center gap-2">
                <IconShieldCheck className="w-5 h-5 text-indigo-400" />
                <h3 className="text-base font-bold text-slate-100">任务详情 — {selectedTask.id}</h3>
              </div>
              <button
                onClick={() => setSelectedTask(null)}
                className="text-xs text-slate-400 hover:text-slate-200 px-2 py-1 rounded bg-slate-800 border border-slate-700"
              >
                关闭
              </button>
            </div>

            <div className="grid grid-cols-2 gap-3 text-xs">
              <div className="p-2.5 rounded-lg bg-slate-950 border border-slate-800">
                <span className="text-slate-400 block">状态 / 阶段:</span>
                <span className="text-slate-200 font-bold font-mono">{selectedTask.status} / {selectedTask.stage}</span>
              </div>
              <div className="p-2.5 rounded-lg bg-slate-950 border border-slate-800">
                <span className="text-slate-400 block">数据源 / 操作:</span>
                <span className="text-slate-200 font-bold font-mono">{selectedTask.source} / {selectedTask.operation}</span>
              </div>
              <div className="p-2.5 rounded-lg bg-slate-950 border border-slate-800">
                <span className="text-slate-400 block">耗时 / 重试:</span>
                <span className="text-slate-200 font-bold font-mono">{selectedTask.duration_ms} ms / {selectedTask.retry_count} 次</span>
              </div>
              <div className="p-2.5 rounded-lg bg-slate-950 border border-slate-800">
                <span className="text-slate-400 block">租约持有节点:</span>
                <span className="text-indigo-400 font-bold font-mono">{selectedTask.lease_owner || 'None'}</span>
              </div>
            </div>

            <div>
              <span className="text-xs font-semibold text-slate-400 mb-1 block">任务输入与处理结果:</span>
              <pre className="p-3 bg-slate-950 border border-slate-800 rounded-xl text-xs font-mono text-slate-300 max-h-48 overflow-y-auto">
                {JSON.stringify(selectedTask, null, 2)}
              </pre>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
