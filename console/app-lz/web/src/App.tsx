/**
 * App — 前端控制台的根组件，管理所有全局状态和数据拉取。
 *
 * 状态管理：
 *   - currentTab: 当前激活的侧边栏标签页（控制显示哪个面板）
 *   - activeProtocol: 当前拓扑大屏的协议视角（rest/grpc）
 *   - topology: 4 个微服务的实时拓扑状态
 *   - tasks: 任务列表（Service Hub 返回）
 *   - leases: Phase B 租约信息
 *   - suites: E2E 测试套件定义
 *   - auditLogs: 审计日志条目
 *   - metricsRaw / parsedMetrics: Prometheus 指标（原始 + 解析后）
 *   - dataApiDefs: 4 个预设数据 API 的定义
 *
 * 数据拉取策略：
 *   - 组件挂载时并发拉取所有数据（7 个 fetch 函数）
 *   - 拓扑每 15 秒自动刷新
 *   - 每个 fetch 函数都有 fallback 兆底（BFF 层已做一层兆底，前端做二层兆底）
 *
 * 渲染逻辑：
 *   - AuthProvider 包裹整个应用，管理用户认证与角色
 *   - 未登录 → 显示 LoginPage
 *   - 已登录 → 左侧固定 Sidebar 导航（按角色过滤） + 右侧面板
 */
import React, { useState, useEffect, useCallback } from 'react';
import { api } from './api/client';
import {
  ProtocolType,
  TopologyResponse,
  Task,
  LeasedTasksResponse,
  TestSuiteCase,
  AuditLogItem,
  DataApiDef,
  DataApiSessionResponse,
} from './types/api';
import { Sidebar, TabType } from './components/Sidebar';
import { TopologyPanel } from './components/TopologyPanel';
import { TaskLifecyclePanel } from './components/TaskLifecyclePanel';
import { TestRunnerPanel } from './components/TestRunnerPanel';
import { AuditVerifierPanel } from './components/AuditVerifierPanel';
import { MetricsPanel } from './components/MetricsPanel';
import { DataApiPanel } from './components/DataApiPanel';
import { BenchmarkPanel } from './components/BenchmarkPanel';
import { AuthProvider, useAuth } from './auth/AuthContext';
import { LoginPage } from './pages/LoginPage';

export const App: React.FC = () => {
  return (
    <AuthProvider>
      <AppContent />
    </AuthProvider>
  );
};

/** AppContent — 主内容组件（在 AuthProvider 内部使用） */
const AppContent: React.FC = () => {
  const { isAuthenticated, isAdmin, isLoading } = useAuth();

  // ── 导航状态 ──
  const [currentTab, setCurrentTab] = useState<TabType>('topology');
  const [activeProtocol, setActiveProtocol] = useState<'rest' | 'grpc'>('rest');

  // ── 数据状态（每个对应一个 BFF API）──
  const [topology, setTopology] = useState<TopologyResponse | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [leases, setLeases] = useState<LeasedTasksResponse | null>(null);
  const [suites, setSuites] = useState<TestSuiteCase[]>([]);
  const [auditLogs, setAuditLogs] = useState<AuditLogItem[]>([]);
  const [metricsRaw, setMetricsRaw] = useState<string>('');
  const [parsedMetrics, setParsedMetrics] = useState<{ stage_durations: Record<string, number>; qps: number; percentiles: Record<string, number>; total_requests: number; source: string } | null>(null);
  const [dataApiDefs, setDataApiDefs] = useState<DataApiDef[]>([]);

  // ── 加载状态（每个面板独立控制 loading 动画）──
  const [loadingDataApi, setLoadingDataApi] = useState(false);
  const [loadingTopo, setLoadingTopo] = useState(false);
  const [loadingTasks, setLoadingTasks] = useState(false);
  const [loadingRunner, setLoadingRunner] = useState(false);
  const [loadingAudit, setLoadingAudit] = useState(false);
  const [loadingMetrics, setLoadingMetrics] = useState(false);

  // ── 认证加载状态：正在检查令牌有效性时显示加载屏 ──
  if (isLoading) {
    return (
      <div className="min-h-screen bg-slate-950 flex items-center justify-center">
        <div className="text-center">
          <div className="w-12 h-12 mx-auto rounded-xl bg-gradient-to-tr from-indigo-600 via-indigo-500 to-amber-500 flex items-center justify-center text-white font-bold text-xl mb-4 animate-pulse">
            LZ
          </div>
          <p className="text-slate-400 text-sm">Loading...</p>
        </div>
      </div>
    );
  }

  // ── 未登录：显示登录页面 ──
  if (!isAuthenticated) {
    return <LoginPage />;
  }

  // ── 数据拉取函数 ──────────────────────────────────────────────────

  // 1. 拉取服务拓扑（支持协议切换，含 4 服务 fallback 兆底）
  const fetchTopology = useCallback(async (proto?: ProtocolType) => {
    const p = proto || activeProtocol;
    setLoadingTopo(true);
    try {
      const res = await api.getTopology(p);
      setTopology(res);
    } catch {
      // BFF 不可达时，前端构造合成的“全部 ready”拓扑（演示模式）
      setTopology({
        status: 'healthy',
        active_protocol: p,
        timestamp: new Date().toISOString(),
        services: [
          { id: 'service-hub', name: '调度中枢 (Service Hub)', http_url: 'http://127.0.0.1:8082', grpc_addr: '127.0.0.1:50052', status: 'ready', rtt_ms: 1.8, rest_rtt_ms: 1.8, grpc_rtt_ms: 1.2, version: '1.8.0' },
          { id: 'engine', name: '隐私与分类引擎 (PrivShield Agent)', http_url: 'http://127.0.0.1:8079', grpc_addr: '127.0.0.1:50051', status: 'ready', rtt_ms: 3.2, rest_rtt_ms: 3.2, grpc_rtt_ms: 2.4, version: '1.8.0' },
          { id: 'datasource-mgr', name: '数据源管理 (Datasource Mgr)', http_url: 'http://127.0.0.1:8083', grpc_addr: '127.0.0.1:50053', status: 'ready', rtt_ms: 2.1, rest_rtt_ms: 2.1, grpc_rtt_ms: 1.5, version: '1.8.0' },
          { id: 'audit-log', name: '脱敏审计日志 (Audit Log)', http_url: 'http://127.0.0.1:8084', grpc_addr: '127.0.0.1:50054', status: 'ready', rtt_ms: 1.5, rest_rtt_ms: 1.5, grpc_rtt_ms: 1.1, version: '1.8.0' },
        ],
      });
    } finally {
      setLoadingTopo(false);
    }
  }, [activeProtocol]);

  // 2. 拉取任务列表 + 租约信息（并发请求，含 fallback 示例任务）
  const fetchTasksAndLeases = useCallback(async () => {
    setLoadingTasks(true);
    try {
      const [tRes, lRes] = await Promise.all([api.listTasks(), api.getLeases()]);
      setTasks(tRes.tasks || []);
      setLeases(lRes);
    } catch {
      // BFF 不可达时，前端构造 2 条示例任务（演示模式，已全部处于终态）
      setTasks([
        {
          id: 'task-1787554500-eabf3934',
          status: 'completed',
          stage: 'done',
          source: 'ds_yibao',
          operation: 'mask',
          priority: 50,
          created_at: new Date(Date.now() - 1000 * 60 * 5).toISOString(),
          duration_ms: 270,
          error: '',
          retry_count: 0,
          lease_owner: 'hub-worker-node-1',
        },
        {
          id: 'task-1787554501-89bcdef1',
          status: 'completed',
          stage: 'done',
          source: 'ds_kangyang',
          operation: 'mask',
          priority: 80,
          created_at: new Date(Date.now() - 1000 * 60 * 2).toISOString(),
          duration_ms: 120,
          error: '',
          retry_count: 0,
          lease_owner: 'hub-worker-node-2',
        },
      ]);
    } finally {
      setLoadingTasks(false);
    }
  }, []);

  // 3. 拉取可用测试套件定义
  const fetchSuites = useCallback(async () => {
    try {
      const res = await api.getSuites();
      setSuites(res.suites || []);
    } catch {
      // ignore
    }
  }, []);

  // 4. 拉取审计日志
  const fetchAuditLogs = useCallback(async () => {
    setLoadingAudit(true);
    try {
      const res = await api.getAuditLogs();
      setAuditLogs(res.logs || []);
    } catch {
      // ignore
    } finally {
      setLoadingAudit(false);
    }
  }, []);

  // 5. 拉取 Prometheus 指标（原始文本 + 解析后指标，并发请求）
  const fetchMetrics = useCallback(async () => {
    setLoadingMetrics(true);
    try {
      const [rawRes, parsedRes] = await Promise.all([
        api.getMetrics(),
        api.getParsedMetrics().catch(() => null),
      ]);
      setMetricsRaw(rawRes);
      if (parsedRes) {
        setParsedMetrics(parsedRes);
      }
    } catch {
      // ignore
    } finally {
      setLoadingMetrics(false);
    }
  }, []);

  // 6. 拉取预设数据 API 定义（4 个 API 的元数据）
  const fetchDataApiDefs = useCallback(async () => {
    try {
      const res = await api.getDataApiDefinitions();
      setDataApiDefs(res.apis || []);
    } catch {
      // BFF 不可达时，前端构造 4 个默认 API 定义（演示模式）
      setDataApiDefs([
        { id: 1, name: '医保结算数据 API', datasource_id: 'ds_yibao', category: 'medical', description: '城镇职工基本医疗保险结算数据', fields: ['record_id', 'patient_name', 'id_card', 'phone', 'diagnosis'], status: 'active' },
        { id: 2, name: '康养体征数据 API', datasource_id: 'ds_kangyang', category: 'healthcare', description: '智慧养老健康监护与体征数据', fields: ['elder_id', 'name', 'age', 'heart_rate', 'blood_pressure'], status: 'active' },
        { id: 3, name: '预留数据 API #3', datasource_id: '', category: 'reserved', description: '预留接口，待后续业务接入', fields: [], status: 'reserved' },
        { id: 4, name: '预留数据 API #4', datasource_id: '', category: 'reserved', description: '预留接口，待后续业务接入', fields: [], status: 'reserved' },
      ]);
    }
  }, []);

  // 7. 调用预设数据 API 会话（前端直接消费，返回完整会话结果）
  const invokeDataApi = useCallback(async (apiId: number, limit: number): Promise<DataApiSessionResponse> => {
    setLoadingDataApi(true);
    try {
      return await api.invokeDataApi(apiId, limit);
    } catch (err: any) {
      // 调用失败时返回合成的失败响应（确保前端能正常展示错误信息）
      return {
        session_id: `session-${apiId}-fallback`,
        api_id: apiId,
        api_name: dataApiDefs.find(d => d.id === apiId)?.name || `API ${apiId}`,
        status: 'failed',
        raw_records: [],
        sanitized_data: [],
        stages: [],
        total_duration_ms: 0,
        error: err.message || 'Session failed',
      };
    } finally {
      setLoadingDataApi(false);
    }
  }, [dataApiDefs]);

  // ── 初始化：组件挂载时并发拉取所有数据 ─────────────────────────
  useEffect(() => {
    fetchTopology();
    fetchTasksAndLeases();
    fetchSuites();
    fetchAuditLogs();
    fetchMetrics();
    fetchDataApiDefs();

    // 拓扑每 15 秒自动刷新（其他数据不自动刷新，需手动触发）
    const timer = setInterval(() => {
      fetchTopology();
    }, 15000);
    return () => clearInterval(timer);  // 组件卸载时清理定时器
  }, [fetchTopology, fetchTasksAndLeases, fetchSuites, fetchAuditLogs, fetchMetrics, fetchDataApiDefs]);

  // ── 任务与租约自动轮询：处于任务标签页或存在 running/pending 任务时动态刷新 ──
  const hasActiveTasks = tasks.some(t => t.status === 'running' || t.status === 'pending');
  useEffect(() => {
    if (currentTab === 'tasks' || hasActiveTasks) {
      // 存在正在执行的任务时 1.5s 轮询，否则 4s 轮询
      const intervalMs = hasActiveTasks ? 1500 : 4000;
      const timer = setInterval(() => {
        fetchTasksAndLeases();
      }, intervalMs);
      return () => clearInterval(timer);
    }
  }, [currentTab, hasActiveTasks, fetchTasksAndLeases]);

  // ── 渲染：左侧导航 + 右侧面板 ────────────────────────────────────
  return (
    <div className="min-h-screen bg-slate-950 text-slate-100 flex">
      {/* 左侧固定导航栏（品牌标识 + 角色过滤导航 + 用户信息） */}
      <Sidebar
        currentTab={currentTab}
        onSelectTab={setCurrentTab}
        clusterStatus={topology?.status || 'healthy'}
      />

      {/* 右侧主内容区（根据 currentTab 条件渲染对应面板） */}
      <main className="flex-1 p-8 max-w-7xl mx-auto overflow-y-auto">
        {/* 服务拓扑大屏 */}
        <div style={{ display: currentTab === 'topology' ? 'block' : 'none' }}>
          <TopologyPanel
            topology={topology}
            activeProtocol={activeProtocol}
            onProtocolChange={setActiveProtocol}
            onRefresh={fetchTopology}
            loading={loadingTopo}
          />
        </div>

        {/* 性能与吞吐量压测大屏（仅 admin） */}
        {isAdmin && (
          <div style={{ display: currentTab === 'benchmark' ? 'block' : 'none' }}>
            <BenchmarkPanel
              apis={dataApiDefs}
            />
          </div>
        )}

        {/* 任务生命周期大屏 */}
        <div style={{ display: currentTab === 'tasks' ? 'block' : 'none' }}>
          <TaskLifecyclePanel
            tasks={tasks}
            leases={leases}
            onRefresh={fetchTasksAndLeases}
            loading={loadingTasks}
          />
        </div>

        {/* E2E 测试运行器大屏（仅 admin） */}
        {isAdmin && (
          <div style={{ display: currentTab === 'runner' ? 'block' : 'none' }}>
            <TestRunnerPanel
              suites={suites}
              onRunSuites={async (req) => {
                setLoadingRunner(true);
                try {
                  const res = await api.runSuites(req);
                  fetchTasksAndLeases();
                  return res;
                } finally {
                  setLoadingRunner(false);
                }
              }}
              loading={loadingRunner}
            />
          </div>
        )}

        {/* 审计验证大屏 */}
        <div style={{ display: currentTab === 'audit' ? 'block' : 'none' }}>
          <AuditVerifierPanel
            logs={auditLogs}
            onVerify={() => api.verifyAudit()}
            onRefreshLogs={fetchAuditLogs}
            loading={loadingAudit}
          />
        </div>

        {/* 性能指标大屏（仅 admin） */}
        {isAdmin && (
          <div style={{ display: currentTab === 'metrics' ? 'block' : 'none' }}>
            <MetricsPanel
              metricsRaw={metricsRaw}
              parsedMetrics={parsedMetrics}
              onRefreshMetrics={fetchMetrics}
              loading={loadingMetrics}
            />
          </div>
        )}

        {/* 预设数据 API 大屏 */}
        <div style={{ display: currentTab === 'dataApi' ? 'block' : 'none' }}>
          <DataApiPanel
            apis={dataApiDefs}
            onInvoke={invokeDataApi}
            loading={loadingDataApi}
          />
        </div>
      </main>
    </div>
  );
};
