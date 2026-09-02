/**
 * LoginPage — 用户登录与注册合一页面。
 *
 * 功能：
 *  - Tab 切换：登录 / 注册
 *  - 登录：用户名 + 密码
 *  - 注册：用户名 + 密码 + 显示名称 + 角色选择
 *  - 错误提示与加载状态
 *  - 深色主题，与整体控制台风格一致
 */
import React, { useState } from 'react';
import { useAuth } from '../auth/AuthContext';
import { useI18n } from '../i18n';
import { UserRole } from '../types/api';

export const LoginPage: React.FC = () => {
  const { login, register } = useAuth();
  const { t } = useI18n();

  const [mode, setMode] = useState<'login' | 'register'>('login');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  // 表单字段
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [role, setRole] = useState<UserRole>('user');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);

    try {
      if (mode === 'login') {
        await login(username, password);
      } else {
        await register(username, password, displayName, role);
      }
    } catch (err: any) {
      setError(err.message || t('auth.errorGeneric'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen bg-slate-950 flex items-center justify-center p-4">
      <div className="w-full max-w-md">
        {/* Logo & Title */}
        <div className="text-center mb-8">
          <div className="w-16 h-16 mx-auto rounded-2xl bg-gradient-to-tr from-indigo-600 via-indigo-500 to-amber-500 flex items-center justify-center shadow-lg shadow-indigo-500/20 text-white font-bold text-2xl mb-4">
            LZ
          </div>
          <h1 className="text-2xl font-bold text-slate-100">{t('auth.title')}</h1>
          <p className="text-sm text-slate-400 mt-1">{t('auth.subtitle')}</p>
        </div>

        {/* Tab Switcher */}
        <div className="flex bg-slate-900 rounded-xl p-1 mb-6 border border-slate-800">
          <button
            onClick={() => { setMode('login'); setError(''); }}
            className={`flex-1 py-2.5 rounded-lg text-sm font-medium transition-all ${
              mode === 'login'
                ? 'bg-indigo-600 text-white shadow'
                : 'text-slate-400 hover:text-slate-200'
            }`}
          >
            {t('auth.loginTab')}
          </button>
          <button
            onClick={() => { setMode('register'); setError(''); }}
            className={`flex-1 py-2.5 rounded-lg text-sm font-medium transition-all ${
              mode === 'register'
                ? 'bg-indigo-600 text-white shadow'
                : 'text-slate-400 hover:text-slate-200'
            }`}
          >
            {t('auth.registerTab')}
          </button>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} className="space-y-4">
          {/* Username */}
          <div>
            <label className="block text-sm font-medium text-slate-300 mb-1.5">{t('auth.username')}</label>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              required
              minLength={3}
              maxLength={32}
              className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-xl text-slate-100 placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent transition-all"
              placeholder={t('auth.usernamePlaceholder')}
            />
          </div>

          {/* Password */}
          <div>
            <label className="block text-sm font-medium text-slate-300 mb-1.5">{t('auth.password')}</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              minLength={6}
              className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-xl text-slate-100 placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent transition-all"
              placeholder={t('auth.passwordPlaceholder')}
            />
          </div>

          {/* Register-only fields */}
          {mode === 'register' && (
            <>
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1.5">{t('auth.displayName')}</label>
                <input
                  type="text"
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-xl text-slate-100 placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent transition-all"
                  placeholder={t('auth.displayNamePlaceholder')}
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1.5">{t('auth.role')}</label>
                <div className="flex gap-3">
                  <label className={`flex-1 flex items-center justify-center gap-2 px-4 py-2.5 rounded-xl border cursor-pointer transition-all ${
                    role === 'user'
                      ? 'bg-indigo-600/15 border-indigo-500/50 text-indigo-400'
                      : 'bg-slate-900 border-slate-700 text-slate-400 hover:border-slate-600'
                  }`}>
                    <input
                      type="radio"
                      name="role"
                      value="user"
                      checked={role === 'user'}
                      onChange={() => setRole('user')}
                      className="sr-only"
                    />
                    <span className="text-sm font-medium">{t('auth.roleUser')}</span>
                  </label>
                  <label className={`flex-1 flex items-center justify-center gap-2 px-4 py-2.5 rounded-xl border cursor-pointer transition-all ${
                    role === 'admin'
                      ? 'bg-amber-600/15 border-amber-500/50 text-amber-400'
                      : 'bg-slate-900 border-slate-700 text-slate-400 hover:border-slate-600'
                  }`}>
                    <input
                      type="radio"
                      name="role"
                      value="admin"
                      checked={role === 'admin'}
                      onChange={() => setRole('admin')}
                      className="sr-only"
                    />
                    <span className="text-sm font-medium">{t('auth.roleAdmin')}</span>
                  </label>
                </div>
              </div>
            </>
          )}

          {/* Error Message */}
          {error && (
            <div className="p-3 bg-red-900/20 border border-red-800/50 rounded-xl text-red-400 text-sm">
              {error}
            </div>
          )}

          {/* Submit Button */}
          <button
            type="submit"
            disabled={loading}
            className="w-full py-3 bg-indigo-600 hover:bg-indigo-500 disabled:bg-indigo-800 disabled:cursor-not-allowed text-white font-medium rounded-xl transition-all shadow-lg shadow-indigo-500/20"
          >
            {loading
              ? t('auth.loading')
              : mode === 'login'
                ? t('auth.loginBtn')
                : t('auth.registerBtn')
            }
          </button>
        </form>

        {/* Footer */}
        <p className="text-center text-xs text-slate-500 mt-6">
          PrivShield &copy; 2026 &middot; {t('auth.footer')}
        </p>
      </div>
    </div>
  );
};
