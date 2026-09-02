/**
 * Sidebar — 左侧固定导航栏组件。
 *
 * 布局结构（从上到下）：
 *  1. 品牌标识区：LZ Logo + 应用标题 + 版本号 + 集群状态指示灯
 *  2. 导航菜单区：6 个标签页按钮（拓扑/数据API/任务/测试/审计/指标）
 *  3. 底部区：中英文语言切换开关 + 版权信息
 *
 * 交互逻辑：
 *  - 点击导航按钮触发 onSelectTab，由 App.tsx 中的 currentTab 状态控制右侧面板切换
 *  - 当前激活的标签页高亮显示（indigo 主题色）
 *  - 集群状态灯：healthy=绿色脉冲，其他=琥珀色
 */
import React from 'react';
import { useI18n } from '../i18n';
import { useAuth } from '../auth/AuthContext';
import {
  IconServer,
  IconShieldCheck,
  IconPlay,
  IconLayers,
  IconSparkles,
  IconGlobe,
  IconGauge,
} from './icons';

/** 7 个标签页类型，对应 7 个面板组件 */
export type TabType = 'topology' | 'benchmark' | 'dataApi' | 'tasks' | 'runner' | 'audit' | 'metrics';

/** Sidebar 组件的 Props */
interface SidebarProps {
  /** 当前激活的标签页 */
  currentTab: TabType;
  /** 标签页切换回调 */
  onSelectTab: (tab: TabType) => void;
  /** 集群整体状态（来自拓扑探测结果） */
  clusterStatus: string;
}
export const Sidebar: React.FC<SidebarProps> = ({
  currentTab,
  onSelectTab,
  clusterStatus,
}) => {
  const { lang, setLang, t } = useI18n();
  const { user, isAdmin, logout } = useAuth();

  /** 导航菜单项定义（7 个标签页，每个包含 ID、翻译 key、图标、是否仅 admin） */
  const allNavItems: { id: TabType; labelKey: string; icon: React.ReactNode; adminOnly?: boolean }[] = [
    { id: 'topology', labelKey: 'nav.topology', icon: <IconServer className="w-5 h-5" /> },
    { id: 'benchmark', labelKey: 'nav.benchmark', icon: <IconGauge className="w-5 h-5" />, adminOnly: true },
    { id: 'dataApi', labelKey: 'nav.dataApi', icon: <IconGlobe className="w-5 h-5" /> },
    { id: 'tasks', labelKey: 'nav.tasks', icon: <IconLayers className="w-5 h-5" /> },
    { id: 'runner', labelKey: 'nav.runner', icon: <IconPlay className="w-5 h-5" />, adminOnly: true },
    { id: 'audit', labelKey: 'nav.audit', icon: <IconShieldCheck className="w-5 h-5" /> },
    { id: 'metrics', labelKey: 'nav.metrics', icon: <IconSparkles className="w-5 h-5" />, adminOnly: true },
  ];

  // 按角色过滤导航项：admin 看全部，user 只看非 adminOnly 的
  const navItems = allNavItems.filter(item => !item.adminOnly || isAdmin);

  return (
    <aside className="w-72 bg-slate-950 border-r border-slate-800 flex flex-col justify-between shrink-0 h-screen sticky top-0">
      <div>
        {/* Branding Header */}
        <div className="p-5 border-b border-slate-800">
          <div className="flex items-center space-x-3">
            <div className="w-10 h-10 rounded-xl bg-gradient-to-tr from-indigo-600 via-indigo-500 to-amber-500 flex items-center justify-center shadow-lg shadow-indigo-500/20 text-white font-bold text-lg">
              LZ
            </div>
            <div>
              <div className="font-bold text-slate-100 text-base tracking-wide flex items-center gap-1.5">
                {t('app.title')}
                <span className="text-[10px] bg-indigo-500/20 text-indigo-300 font-mono px-1.5 py-0.5 rounded border border-indigo-500/30">v1.8.0</span>
              </div>
              <p className="text-xs text-slate-400 truncate max-w-[170px] mt-0.5" title={t('app.subtitle')}>
                {t('app.subtitle')}
              </p>
            </div>
          </div>

          {/* Cluster Status Quick Pill */}
          <div className="mt-4 flex items-center justify-between px-3 py-2 rounded-lg bg-slate-900 border border-slate-800/80">
            <span className="text-xs text-slate-400 font-medium">四服务状态</span>
            <div className="flex items-center gap-1.5">
              <span className={`w-2 h-2 rounded-full animate-pulse ${clusterStatus === 'healthy' ? 'bg-emerald-400' : 'bg-amber-400'}`} />
              <span className={`text-xs font-semibold ${clusterStatus === 'healthy' ? 'text-emerald-400' : 'text-amber-400'}`}>
                {clusterStatus === 'healthy' ? 'All Ready' : 'Degraded'}
              </span>
            </div>
          </div>
        </div>

        {/* Navigation Menu */}
        <nav className="p-3 space-y-1">
          {navItems.map((item) => {
            const active = currentTab === item.id;
            return (
              <button
                key={item.id}
                onClick={() => onSelectTab(item.id)}
                className={`w-full flex items-center space-x-3 px-3.5 py-2.5 rounded-xl text-sm font-medium transition-all duration-150 ${
                  active
                    ? 'bg-indigo-600/15 text-indigo-400 border border-indigo-500/30 shadow-sm'
                    : 'text-slate-400 hover:text-slate-200 hover:bg-slate-900/60'
                }`}
              >
                <span className={active ? 'text-indigo-400' : 'text-slate-500'}>
                  {item.icon}
                </span>
                <span>{t(item.labelKey)}</span>
              </button>
            );
          })}
        </nav>
      </div>

      {/* Footer & Language Toggle */}
      <div className="p-4 border-t border-slate-800/80 space-y-3">
        {/* User Info & Logout */}
        {user && (
          <div className="flex items-center justify-between bg-slate-900/60 px-3 py-2 rounded-lg border border-slate-800/60">
            <div className="flex items-center gap-2 min-w-0">
              <div className={`w-7 h-7 rounded-lg flex items-center justify-center text-xs font-bold ${
                isAdmin ? 'bg-amber-500/20 text-amber-400' : 'bg-indigo-500/20 text-indigo-400'
              }`}>
                {user.username.charAt(0).toUpperCase()}
              </div>
              <div className="min-w-0">
                <div className="text-xs font-medium text-slate-200 truncate">{user.display_name || user.username}</div>
                <div className={`text-[10px] font-medium ${isAdmin ? 'text-amber-400' : 'text-indigo-400'}`}>
                  {isAdmin ? t('auth.roleAdmin') : t('auth.roleUser')}
                </div>
              </div>
            </div>
            <button
              onClick={logout}
              className="text-xs text-slate-500 hover:text-red-400 transition-colors px-2 py-1 rounded hover:bg-red-900/20"
              title={t('auth.logout')}
            >
              {t('auth.logout')}
            </button>
          </div>
        )}
        <div className="flex items-center justify-between bg-slate-900 p-1 rounded-lg border border-slate-800 text-xs">
          <button
            onClick={() => setLang('zh-CN')}
            className={`flex-1 py-1 text-center rounded-md font-medium transition-colors ${
              lang === 'zh-CN' ? 'bg-indigo-600 text-white shadow' : 'text-slate-400 hover:text-slate-200'
            }`}
          >
            中文
          </button>
          <button
            onClick={() => setLang('en-US')}
            className={`flex-1 py-1 text-center rounded-md font-medium transition-colors ${
              lang === 'en-US' ? 'bg-indigo-600 text-white shadow' : 'text-slate-400 hover:text-slate-200'
            }`}
          >
            EN
          </button>
        </div>
        <div className="text-[11px] text-slate-500 text-center font-mono">
          PrivShield Service Hub Hub &copy; 2026
        </div>
      </div>
    </aside>
  );
};
