/**
 * 顶部导航栏组件 / Top Navigation Bar Component
 *
 * 功能：品牌标识 + Agent 健康状态灯 + 语言切换 + 后端切换器。
 * Function: Brand logo + Agent health indicator + Language switch + Backend selector.
 *
 * 详细逻辑 / Detailed Logic：
 *   - 点击品牌区可返回总览页（触发 onHome 回调）；
 *   - HealthPill 实时反映 agent 连通性（绿色=正常 / 红色=不可达）；
 *   - LangSwitch 在中英文之间切换，状态持久化到 localStorage；
 *   - BackendSelector 在 REST / gRPC 两种协议间切换。
 *
 * Clicking the brand area returns to the overview page (triggers onHome callback);
 * HealthPill reflects agent connectivity in real-time (green=OK / red=unreachable).
 */

/** 引入健康检查响应类型 / Import health check response type */
import type { ConsoleHealth } from '@/types/api';
/** 引入后端选项类型 / Import backend option type */
import type { BackendOption } from '@/components/BackendSelector';
/** 引入后端切换器组件 / Import backend selector component */
import BackendSelector from '@/components/BackendSelector';
/** 引入内联 SVG 图标组件 / Import inline SVG icon component */
import { Icon } from '@/components/icons';
/** 引入国际化 Hook / Import i18n Hook */
import { useI18n } from '@/i18n';

/**
 * Header 组件属性接口 / Header Component Props Interface
 *
 * 由 App 组件传入，控制导航栏的全部行为。
 * Passed from App component, controls all navigation bar behaviors.
 */
interface HeaderProps {
  /** 当前选中的后端 / Currently selected backend */
  backend: BackendOption;
  /** 后端切换回调 / Backend change callback */
  onBackendChange: (option: BackendOption) => void;
  /** 健康检查数据（null 表示未加载）/ Health check data (null means not loaded) */
  health: ConsoleHealth | null;
  /** 是否正在加载 / Whether loading */
  loading: boolean;
  /** 点击 logo 返回总览页 / Click logo to return to overview */
  onHome?: () => void;
}

/**
 * 健康状态徽章组件 / Health Status Badge Component
 *
 * 详细逻辑 / Detailed Logic：
 *   - 加载中且无数据：显示灰色脉冲动画 + "检测中"；
 *   - 有数据且无 error：绿色圆点 + "Agent 正常"；
 *   - 有数据且有 error：红色圆点 + "Agent 不可达"；
 *   - 同时展示后端与 agent 的通信协议（REST / gRPC），
 *     切换 REST / gRPC 协议后该标识随之变化，可直观验证切换生效。
 *
 * Also displays the communication protocol (REST / gRPC) between backend and agent;
 * the badge changes after switching REST / gRPC protocol, visually verifying the switch.
 */
function HealthPill({ health, loading }: { health: ConsoleHealth | null; loading: boolean }) {
  const { t } = useI18n(); // 获取翻译函数 / Get translation function
  // 加载中且无历史数据：显示灰色脉冲动画 / Loading with no prior data: show gray pulse animation
  if (loading && !health) {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full bg-gray-100 px-3 py-1 text-xs font-medium text-gray-500">
        {/* animate-pulse 产生呼吸灯效果 / animate-pulse creates breathing light effect */}
        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-gray-400" />
        {t('header.detecting')}
      </span>
    );
  }
  // 无数据时不渲染任何内容 / Render nothing when no data
  if (!health) return null;

  // 判断 agent 是否可达：无 error 字段即为正常 / Determine agent reachability: no error field means OK
  const ok = !health.error;
  return (
    <span
      className={[
        'inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium',
        // 根据状态选择绿色/红色背景 / Choose green/red background based on status
        ok ? 'bg-emerald-50 text-emerald-700' : 'bg-red-50 text-red-700',
      ].join(' ')}
      // 悬停提示显示 agent 地址与协议 / Hover tooltip shows agent URL and protocol
      title={`${health.agent_url}${health.protocol ? ` · ${health.protocol}` : ''}${health.error ? ` (${health.error})` : ''}`}
    >
      {/* 状态圆点：绿色=正常 / 红色=不可达 / Status dot: green=OK / red=unreachable */}
      <span className={['h-1.5 w-1.5 rounded-full', ok ? 'bg-emerald-500' : 'bg-red-500'].join(' ')} />
      {/* 状态文字 / Status text */}
      {ok ? t('header.agent_ok') : t('header.agent_down')}
      {/* 协议徽章（REST / gRPC），切换后端后随之变化 / Protocol badge (REST / gRPC), changes after backend switch */}
      {health.protocol && (
        <span className="ml-0.5 rounded bg-white/60 px-1 py-px text-[10px] font-semibold">
          {health.protocol}
        </span>
      )}
    </span>
  );
}

/**
 * 语言切换按钮 / Language Toggle Button
 *
 * 在中文 (zh) 与英文 (en) 之间切换，状态由 I18nProvider 管理并持久化到 localStorage。
 * Switches between Chinese (zh) and English (en); state managed by I18nProvider and persisted to localStorage.
 */
function LangSwitch() {
  // 获取当前语言与设置函数 / Get current language and setter function
  const { lang, setLang } = useI18n();
  return (
    <button
      // 点击切换：zh→en / en→zh / Click to toggle: zh→en / en→zh
      onClick={() => setLang(lang === 'zh' ? 'en' : 'zh')}
      className="inline-flex items-center gap-1 rounded-lg border border-gray-200 bg-gray-50 px-2.5 py-1.5 text-xs font-medium text-gray-600 transition-colors hover:border-gray-300 hover:bg-gray-100"
      // 悬停提示显示目标语言 / Hover tooltip shows target language
      title={lang === 'zh' ? 'Switch to English' : '切换为中文'}
    >
      {/* 地球图标表示语言 / Globe icon represents language */}
      <Icon name="globe" className="h-3.5 w-3.5" />
      {/* 显示目标语言缩写 / Show target language abbreviation */}
      {lang === 'zh' ? 'EN' : '中'}
    </button>
  );
}

/**
 * 顶部导航栏主组件 / Top Navigation Bar Main Component
 *
 * 布局：左侧品牌区（可点击返回总览）+ 右侧工具区（状态灯 + 语言 + 后端切换）。
 * Layout: Left brand area (clickable to overview) + Right tools area (status + language + backend switch).
 */
export default function Header({ backend, onBackendChange, health, loading, onHome }: HeaderProps) {
  const { t } = useI18n(); // 获取翻译函数 / Get translation function
  return (
    /* 导航栏容器：固定高度 56px，底部边框分隔 / Nav bar container: fixed height 56px, bottom border separator */
    <header className="flex h-14 shrink-0 items-center justify-between border-b border-gray-200 bg-white px-4">
      {/* 左侧品牌区：点击返回总览页 / Left brand area: click to return to overview */}
      <button
        onClick={onHome}
        className="group flex items-center gap-3 rounded-lg px-1 py-1 text-left transition-colors hover:bg-gray-50"
        title={t('header.back_home')}
      >
        {/* 盾牌图标 Logo：indigo 背景 + 白色图标 / Shield icon Logo: indigo background + white icon */}
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-600 text-white shadow-sm transition-colors group-hover:bg-indigo-700">
          <Icon name="shield" className="h-5 w-5" />
        </div>
        {/* 品牌文字：主标题 + 副标题 / Brand text: main title + subtitle */}
        <div className="leading-tight">
          <div className="text-sm font-semibold text-gray-900">{t('header.brand_title')}</div>
          <div className="text-[11px] text-gray-400">{t('header.brand_subtitle')}</div>
        </div>
      </button>

      {/* 右侧工具区：健康状态 + 语言切换 + 后端选择器 / Right tools area: health status + language switch + backend selector */}
      <div className="flex items-center gap-3">
        {/* Agent 健康状态徽章 / Agent health status badge */}
        <HealthPill health={health} loading={loading} />
        {/* 中英文切换按钮 / Chinese-English toggle button */}
        <LangSwitch />
        {/* gRPC / REST 协议切换下拉框 / gRPC / REST protocol switch dropdown */}
        <BackendSelector value={backend} onChange={onBackendChange} />
      </div>
    </header>
  );
}
