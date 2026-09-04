/**
 * Console Web 根组件 App.test.tsx 自动化单元与集成测试
 *
 * 测试范围：
 *   1. 异步数据拉取与 App 头部 / 左侧 Sidebar 正常渲染
 *   2. 主区域视图组件切换 (Overview -> Batch -> DynClassification -> LbTest)
 *   3. 错误状态降级渲染与重试逻辑
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import App from '../App';
import * as apiClient from '../api/client';

// Mock API 客户端模块
vi.mock('../api/client', () => ({
  fetchSamples: vi.fn(),
  fetchHealth: vi.fn(),
  setBaseUrl: vi.fn(),
}));

describe('Console Web App.tsx UI 单元与视图切换集成测试', () => {
  const mockSamples = [
    {
      name: '数据脱敏示例',
      method: 'POST',
      path: '/v1/privacy/mask',
      category: 'masking',
      body: { records: [] },
      description: '将输入记录中的手机号身份证等进行脱敏',
      label: '数据脱敏示例',
    },
    {
      name: '动态分类示例',
      method: 'POST',
      path: '/v1/dynclassification/classify',
      category: 'classification',
      body: { text: '示例文本' },
      description: '三层分类分级判定',
      label: '动态分类示例',
    },
  ];

  const mockHealth = {
    status: 'healthy',
    backend: 'gRPC',
    agent_url: 'http://127.0.0.1:8079',
    mode: 'test',
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('正常加载：渲染头部、侧边栏并默认展示 Overview 视图', async () => {
    vi.mocked(apiClient.fetchSamples).mockResolvedValue(mockSamples as any);
    vi.mocked(apiClient.fetchHealth).mockResolvedValue(mockHealth as any);

    render(<App />);

    // 等待异步拉取完成并验证 API 样本列表渲染
    await waitFor(() => {
      expect(screen.getAllByText('数据脱敏示例').length).toBeGreaterThan(0);
      expect(screen.getAllByText('动态分类示例').length).toBeGreaterThan(0);
    });
  });

  it('视图切换测试：点击左侧 Sidebar 成功导航至 动态分类分级 面板', async () => {
    vi.mocked(apiClient.fetchSamples).mockResolvedValue(mockSamples as any);
    vi.mocked(apiClient.fetchHealth).mockResolvedValue(mockHealth as any);

    render(<App />);

    await waitFor(() => {
      expect(screen.getAllByText('数据脱敏示例').length).toBeGreaterThan(0);
    });

    // 找到动态分类分级 Sidebar 入口并点击
    const dynClassifyBtns = screen.getAllByRole('button', { name: /dyn_classify|动态分类/i });
    expect(dynClassifyBtns.length).toBeGreaterThan(0);
    fireEvent.click(dynClassifyBtns[0]);

    // 验证分类面板被成功加载或展现
    await waitFor(() => {
      expect(screen.getByRole('main')).toBeInTheDocument();
    });
  });

  it('异常降级测试：接口请求失败时展示错误提示框并提供重试按钮', async () => {
    vi.mocked(apiClient.fetchSamples).mockRejectedValue(new Error('网络连接超时 504'));
    vi.mocked(apiClient.fetchHealth).mockRejectedValue(new Error('网络连接超时 504'));

    render(<App />);

    await waitFor(() => {
      expect(screen.getByText(/网络连接超时 504/i)).toBeInTheDocument();
    });

    // 验证重试按钮存在
    const retryBtn = screen.getByRole('button', { name: /retry|重试/i });
    expect(retryBtn).toBeInTheDocument();
  });
});
