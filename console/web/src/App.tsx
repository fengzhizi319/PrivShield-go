/**
 * 应用根组件 / Application Root Component
 *
 * 功能：负责全局状态管理与三栏布局编排。
 * Function: Responsible for global state management and three-column layout orchestration.
 *
 * 布局结构 / Layout Structure：
 *   顶部 Header + 左侧 Sidebar + 右侧主区域。
 *   Top Header + Left Sidebar + Right main area.
 *
 * 主区域通过 ``View`` 判别联合在六种视图间切换：
 * Main area switches between six views via ``View`` discriminated union:
 *   - overview：接口总览（卡片式）/ API overview (card grid)；
 *   - endpoint：单端点测试（请求/响应分栏）/ Single endpoint test (request/response split)；
 *   - batch：批量测试 / Batch test；
 *   - filetest：文件处理 / File processing；
 *   - lbtest：负载均衡测试 / Load balancer test；
 *   - dynclassification：动态分类分级 / Dynamic classification；
 *   - ops：运维诊断 / Ops diagnostics。
 *
 * 数据流 / Data Flow：启动时并行拉取 samples 与 health；切换后端时重新拉取。
 * On startup, fetch samples & health in parallel; re-fetch when backend switches.
 */

/** 引入 React Hooks：副作用 / 状态 / 稳定回调 / Import React Hooks: side effects / state / stable callback */
import { useEffect, useState, useCallback } from 'react';
/** 引入前后端数据契约类型 / Import frontend-backend data contract types */
import type { EndpointSample, ConsoleHealth } from '@/types/api';
/** 引入 API 客户端函数：获取示例 / 健康检查 / 设置基址 / Import API client functions */
import { fetchSamples, fetchHealth, setBaseUrl } from '@/api/client';
/** 引入布局组件：顶部导航栏 / Import layout component: top navigation bar */
import Header from '@/components/Header';
/** 引入布局组件：左侧接口导航树 / Import layout component: left endpoint navigation tree */
import Sidebar from '@/components/Sidebar';
/** 引入视图组件：接口总览页 / Import view component: API overview page */
import Overview from '@/components/Overview';
/** 引入视图组件：单端点测试页 / Import view component: single endpoint test page */
import EndpointView from '@/components/EndpointView';
/** 引入视图组件：批量测试页 / Import view component: batch test page */
import BatchTest from '@/components/BatchTest';
/** 引入视图组件：文件处理页 / Import view component: file processing page */
import FileTest from '@/components/FileTest';
/** 引入视图组件：负载均衡测试页 / Import view component: load balancer test page */
import LbTest from '@/components/LbTest';
/** 引入视图组件：动态分类分级面板 / Import view component: dynamic classification panel */
import DynClassificationPanel from '@/components/DynClassificationPanel';
/** 引入视图组件：运维诊断面板 / Import view component: ops diagnostics panel */
import OpsPanel from '@/components/OpsPanel';
/** 引入视图组件：并发压测面板 / Import view component: concurrency test panel */
import ConcurrencyTestPanel from '@/components/ConcurrencyTestPanel';
/** 引入视图组件：医疗敏感数据治理面板 / Import view component: medical privacy pipeline panel */
import MedicalPipelinePanel from '@/components/MedicalPipelinePanel';
/** 引入视图组件：医保结算数据治理面板 / Import view component: yibao privacy pipeline panel */
import YibaoPipelinePanel from '@/components/YibaoPipelinePanel';
/** 引入错误边界组件：防止单组件崩溃导致整页白屏 / Import error boundary: prevent single component crash from blank page */
import ErrorBoundary from '@/components/ErrorBoundary';
/** 引入后端切换器类型与默认值 / Import backend selector type and default value */
import { DEFAULT_BACKEND, type BackendOption } from '@/components/BackendSelector';
/** 引入内联 SVG 图标组件 / Import inline SVG icon component */
import { Icon } from '@/components/icons';
/** 引入国际化 Hook（提供 t() 翻译函数）/ Import i18n Hook (provides t() translation function) */
import { useI18n } from '@/i18n';
/** 引入统一错误消息提取工具 / Import unified error message extraction utility */
import { getErrorMessage } from '@/utils/error';

/**
 * 主区域视图判别联合类型 / Main area view discriminated union type
 *
 * 通过 type 字段区分当前渲染哪个视图组件，
 * endpoint 视图额外携带 sample 数据（当前选中的端点示例）。
 * Distinguished by type field; endpoint view additionally carries sample data.
 */
type View =
  | { type: 'overview' }                          // 接口总览 / API overview
  | { type: 'endpoint'; sample: EndpointSample }  // 单端点测试 / Single endpoint test
  | { type: 'batch' }                             // 批量测试 / Batch test
  | { type: 'filetest' }                          // 文件处理 / File processing
  | { type: 'lbtest' }                            // 负载均衡 / Load balancer
  | { type: 'concurrency' }                      // 并发压测 / Concurrency test
  | { type: 'dynclassification' }                // 动态分类分级 / Dynamic classification
  | { type: 'medical' }                          // 医疗敏感数据治理 / Medical privacy pipeline
  | { type: 'yibao' }                            // 医保结算数据治理 / Yibao privacy pipeline
  | { type: 'ops' };                             // 运维诊断 / Ops diagnostics


/**
 * 应用根组件函数 / Application root component function
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 维护 samples / view / health / loading / error / backend 六个核心状态；
 *   2. load() 并行拉取示例与健康检查，失败时记录错误并重置视图；
 *   3. backend 变化时更新 API 基址并触发重新加载；
 *   4. 渲染三栏布局，主区域根据 view.type 切换对应视图组件。
 */
export default function App() {
  // 获取国际化翻译函数 / Get i18n translation function
  const { t } = useI18n();
  /** 全部端点示例（来自 /v1/samples）/ All endpoint samples (from /v1/samples) */
  const [samples, setSamples] = useState<EndpointSample[]>([]);
  /** 当前主区域视图状态 / Current main area view state */
  const [view, setView] = useState<View>({ type: 'overview' });
  /** 后端健康状态（用于 Header 状态灯与 cURL 基址推断）/ Backend health status (for Header indicator & cURL base URL inference) */
  const [health, setHealth] = useState<ConsoleHealth | null>(null);
  /** 加载中标志 / Loading flag */
  const [loading, setLoading] = useState(true);
  /** 错误信息（null 表示无错误）/ Error message (null means no error) */
  const [error, setError] = useState<string | null>(null);
  /** 当前选中的通信链路协议（gRPC / REST）/ Currently selected protocol (gRPC / REST) */
  const [backend, setBackend] = useState<BackendOption>(DEFAULT_BACKEND);

  /**
   * 并行拉取示例与健康检查 / Fetch samples and health check in parallel
   */
  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [samplesData, healthData] = await Promise.all([fetchSamples(), fetchHealth()]);
      setSamples(samplesData);
      setHealth(healthData);
      setView({ type: 'overview' });
    } catch (e) {
      setError(getErrorMessage(e));
      setHealth(null);
      setSamples([]);
      setView({ type: 'overview' });
    } finally {
      setLoading(false);
    }
  }, []);

  /**
   * 后端切换副作用 / Backend switch side effect
   *
   * 当 backend 状态变化时：更新全局 API 基址并重新拉取数据。
   * When backend state changes: update global API base URL and re-fetch data.
   */
  useEffect(() => {
    setBaseUrl(backend.value); // 更新 API 客户端的基址 / Update API client's base URL
    load();                    // 触发数据重新加载 / Trigger data reload
  }, [backend, load]);

  /**
   * 定期心跳健康探测 / Periodic health polling
   *
   * 每 5 秒静默检测一次上游连通性，在服务就绪或协议恢复时自动刷新状态灯。
   */
  useEffect(() => {
    let cancelled = false;
    const interval = setInterval(async () => {
      if (document.hidden) return;
      try {
        const data = await fetchHealth();
        if (!cancelled) {
          setHealth(data);
        }
      } catch {
        // 忽略轮询失败，保留已有状态并在下次轮询重试
      }
    }, 5000);

    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [backend]);

  /** 当前选中的端点示例（仅 endpoint 视图非空）/ Currently selected endpoint sample (non-null only in endpoint view) */
  const selected = view.type === 'endpoint' ? view.sample : null;
  /** 导航到总览页 / Navigate to overview page */
  const goOverview = () => setView({ type: 'overview' });
  /** 打开指定端点的测试视图 / Open test view for specified endpoint */
  const openEndpoint = (sample: EndpointSample) => setView({ type: 'endpoint', sample });

  return (
    /* 最外层容器：全屏弹性布局，纵向排列（Header + 内容区） */
    /* Outermost container: full-screen flex layout, vertical arrangement (Header + content area) */
    <div className="flex h-screen flex-col bg-gray-50">
      {/* 顶部导航栏：品牌 + 健康状态灯 + 语言切换 + 后端切换器 */}
      {/* Top navigation bar: brand + health indicator + language switch + backend selector */}
      <Header
        backend={backend}
        onBackendChange={setBackend}
        health={health}
        loading={loading}
        onHome={goOverview}
      />

      {/* 内容区：横向弹性布局（Sidebar + 主区域），溢出隐藏 */}
      {/* Content area: horizontal flex layout (Sidebar + main area), overflow hidden */}
      <div className="flex flex-1 overflow-hidden">
        {/* 加载中状态：居中旋转动画 + 提示文字 / Loading state: centered spinner + hint text */}
        {loading ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-3 text-gray-400">
            {/* CSS 旋转动画模拟加载指示器 / CSS spin animation simulating loading indicator */}
            <span className="h-8 w-8 animate-spin rounded-full border-2 border-gray-200 border-t-indigo-500" />
            <p className="text-sm">{t('app.loading')}</p>
          </div>
        ) : error ? (
          /* 错误状态：图标 + 错误信息 + 重试按钮 / Error state: icon + error message + retry button */
          <div className="flex flex-1 flex-col items-center justify-center gap-4 px-6">
            {/* 红色警告图标 / Red warning icon */}
            <span className="flex h-12 w-12 items-center justify-center rounded-full bg-red-50 text-red-500">
              <Icon name="alert" className="h-6 w-6" />
            </span>
            <div className="text-center">
              {/* 连接失败提示（包含后端名称）/ Connection failed hint (includes backend name) */}
              <p className="text-sm font-medium text-gray-800">{t('app.connect_failed', backend.label)}</p>
              {/* 具体错误消息 / Specific error message */}
              <p className="mt-1 max-w-md break-words text-xs text-gray-500">{error}</p>
            </div>
            {/* 重试按钮：重新触发 load() / Retry button: re-trigger load() */}
            <button
              onClick={load}
              className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white shadow-sm transition-colors hover:bg-indigo-700"
            >
              <Icon name="refresh" className="h-4 w-4" />
              {t('app.retry')}
            </button>
          </div>
        ) : (
          /* 正常状态：Sidebar + 主区域 / Normal state: Sidebar + main area */
          <>
            {/* 左侧导航栏：接口分组树 + 快捷入口 / Left navigation: endpoint group tree + quick entries */}
            <Sidebar
              samples={samples}
              selected={selected}
              onSelect={openEndpoint}
              onHome={goOverview}
              onBatch={() => setView({ type: 'batch' })}
              batchActive={view.type === 'batch'}
              onFileTest={() => setView({ type: 'filetest' })}
              fileTestActive={view.type === 'filetest'}
              onLbTest={() => setView({ type: 'lbtest' })}
              lbTestActive={view.type === 'lbtest'}
              onConcurrency={() => setView({ type: 'concurrency' })}
              concurrencyActive={view.type === 'concurrency'}
              onDynClassify={() => setView({ type: 'dynclassification' })}
              dynClassifyActive={view.type === 'dynclassification'}
              onOps={() => setView({ type: 'ops' })}
              opsActive={view.type === 'ops'}
              onMedicalPipeline={() => setView({ type: 'medical' })}
              medicalActive={view.type === 'medical'}
              onYibaoPipeline={() => setView({ type: 'yibao' })}
              yibaoActive={view.type === 'yibao'}
            />
            {/* 主区域容器：占据剩余空间，溢出隐藏 / Main area container: fills remaining space, overflow hidden */}
            <main className="flex-1 overflow-hidden">
              {/* ErrorBoundary 包裹主区域：单个视图组件崩溃时展示降级界面，避免整页白屏 */}
              {/* ErrorBoundary wraps main area: shows fallback UI when a view component crashes, avoiding blank page */}
              <ErrorBoundary>
                {/* 根据 view.type 渲染对应视图组件 / Render corresponding view component based on view.type */}
                {/* EndpointView 用 key 强制在切换端点时重建组件，避免上一个端点的状态残留 */}
                {/* EndpointView uses key to force remount on endpoint switch, preventing stale state */}
                {view.type === 'endpoint' ? (
                  <EndpointView
                    key={`${view.sample.method}-${view.sample.path}`}
                    sample={view.sample}
                    onBack={goOverview}
                    agentUrl={health?.agent_url}
                  />
                ) : view.type === 'batch' ? (
                  <BatchTest samples={samples} onSelectSample={openEndpoint} />
                ) : view.type === 'filetest' ? (
                  <FileTest />
                ) : view.type === 'lbtest' ? (
                  <LbTest agentUrl={health?.agent_url} />
                ) : view.type === 'concurrency' ? (
                  <ConcurrencyTestPanel agentUrl={health?.agent_url} />
                ) : view.type === 'dynclassification' ? (
                  <DynClassificationPanel />
                ) : view.type === 'medical' ? (
                  <MedicalPipelinePanel agentUrl={health?.agent_url} />
                ) : view.type === 'yibao' ? (
                  <YibaoPipelinePanel agentUrl={health?.agent_url} />
                ) : view.type === 'ops' ? (
                  <OpsPanel health={health} />
                ) : (
                  <Overview samples={samples} onSelect={openEndpoint} />
                )}
              </ErrorBoundary>
            </main>

          </>
        )}
      </div>
    </div>
  );
}
