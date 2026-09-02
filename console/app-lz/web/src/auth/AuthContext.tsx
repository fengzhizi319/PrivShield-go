/**
 * AuthContext — 用户认证与角色状态管理。
 *
 * 职责：
 *  1. 管理当前登录用户信息（username / role / display_name）
 *  2. 管理 JWT 令牌（localStorage 持久化）
 *  3. 提供 login / register / logout 方法
 *  4. 提供 isAuthenticated / isAdmin 便捷判断
 *
 * 使用方式：
 *  const { user, isAuthenticated, isAdmin, login, logout } = useAuth();
 */
import React, { createContext, useContext, useState, useCallback, useEffect, ReactNode } from 'react';
import { api, getToken, setToken, clearToken } from '../api/client';
import { User, UserRole } from '../types/api';

// ============================================================================
// Context 类型定义
// ============================================================================

interface AuthContextType {
  /** 当前登录用户（未登录为 null） */
  user: User | null;
  /** 是否已登录 */
  isAuthenticated: boolean;
  /** 是否为管理员 */
  isAdmin: boolean;
  /** 是否正在加载用户信息（初始验证中） */
  isLoading: boolean;
  /** 登录方法 */
  login: (username: string, password: string) => Promise<void>;
  /** 注册方法 */
  register: (username: string, password: string, displayName: string, role: UserRole) => Promise<void>;
  /** 登出方法 */
  logout: () => void;
}

// ============================================================================
// Context 实例
// ============================================================================

const AuthContext = createContext<AuthContextType | null>(null);

// ============================================================================
// Provider
// ============================================================================

interface AuthProviderProps {
  children: ReactNode;
  /** 认证是否启用（false 时自动以匿名 admin 身份登录） */
  authEnabled?: boolean;
}

export const AuthProvider: React.FC<AuthProviderProps> = ({ children, authEnabled = false }) => {
  const [user, setUser] = useState<User | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  // 组件挂载时检查 localStorage 中是否有有效令牌
  useEffect(() => {
    const checkAuth = async () => {
      if (!authEnabled) {
        // 认证未启用 → 以匿名 admin 身份自动登录
        setUser({
          username: 'anonymous',
          display_name: 'Anonymous Admin',
          role: 'admin',
        });
        setIsLoading(false);
        return;
      }

      const token = getToken();
      if (token) {
        try {
          const me = await api.getMe();
          setUser(me);
        } catch {
          // 令牌无效或过期 → 清除
          clearToken();
        }
      }
      setIsLoading(false);
    };

    checkAuth();
  }, [authEnabled]);

  const login = useCallback(async (username: string, password: string) => {
    const res = await api.login({ username, password });
    setToken(res.token);
    setUser(res.user);
  }, []);

  const register = useCallback(async (username: string, password: string, displayName: string, role: UserRole) => {
    const res = await api.register({ username, password, display_name: displayName, role });
    setToken(res.token);
    setUser(res.user);
  }, []);

  const logout = useCallback(() => {
    clearToken();
    setUser(null);
  }, []);

  const isAuthenticated = user !== null;
  const isAdmin = user?.role === 'admin';

  return (
    <AuthContext.Provider value={{ user, isAuthenticated, isAdmin, isLoading, login, register, logout }}>
      {children}
    </AuthContext.Provider>
  );
};

// ============================================================================
// Hook
// ============================================================================

/**
 * useAuth — 获取认证上下文。
 * 必须在 AuthProvider 内部使用。
 */
export const useAuth = (): AuthContextType => {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return ctx;
};
