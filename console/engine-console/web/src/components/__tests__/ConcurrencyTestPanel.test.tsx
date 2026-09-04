/**
 * ConcurrencyTestPanel 单元测试：验证配置面板渲染、预设选择、结果展示。
 * ConcurrencyTestPanel unit tests: verify config panel rendering, preset selection, result display.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import ConcurrencyTestPanel from '../ConcurrencyTestPanel';
import type { ConcurrencyTestResponse } from '@/types/api';

// 模拟 API 客户端 / Mock API client
const mockConcurrencyTest = vi.fn();
vi.mock('@/api/client', () => ({
  concurrencyTest: (req: unknown) => mockConcurrencyTest(req),
}));

// 模拟 Icon 组件 / Mock Icon component
vi.mock('@/components/icons', () => ({
  Icon: ({ name }: { name: string }) => <span data-testid={`icon-${name}`} />,
}));

// 模拟 i18n / Mock i18n
vi.mock('@/i18n', () => ({
  useI18n: () => ({
    t: (key: string) => {
      const map: Record<string, string> = {
        'concurrency.title': '并发压测',
        'concurrency.subtitle': '以指定并发度向 Agent 发送请求',
        'concurrency.presets': '快捷路径',
        'concurrency.target_path': '目标路径',
        'concurrency.request_body': '请求体（JSON）',
        'concurrency.concurrency': '并发数',
        'concurrency.concurrency_hint': '同时发出的最大请求数',
        'concurrency.total_requests': '总请求数',
        'concurrency.run': '运行压测',
        'concurrency.running': '压测中…',
        'concurrency.target_agent': '目标 Agent',
        'concurrency.empty_hint': '配置参数后点击"运行压测"查看结果',
        'concurrency.invalid_json': '请求体 JSON 格式错误，请检查',
        'concurrency.qps': '吞吐量',
        'concurrency.success_rate': '成功率',
        'concurrency.total_time': '总耗时',
        'concurrency.success_failed': '成功/失败',
        'concurrency.latency_distribution': '延迟分布',
        'concurrency.avg': '平均',
        'concurrency.min': '最小',
        'concurrency.max': '最大',
        'concurrency.metric': '指标',
        'concurrency.value_ms': '数值 (ms)',
        'concurrency.meaning': '统计含义与解释',
        'concurrency.desc.p50': '50% 的请求在此耗时内完成（中位数）',
        'concurrency.desc.p90': '90% 的请求在此耗时内完成',
        'concurrency.desc.p95': '95% 的请求在此耗时内完成',
        'concurrency.desc.p99': '99% 的请求在此耗时内完成',
        'concurrency.desc.avg': '所有请求的算术平均耗时',
        'concurrency.desc.min': '单次最快请求耗时',
        'concurrency.desc.max': '单次最慢请求耗时',
        'concurrency.guide.title': '性能指标与分位数 (P50/P90/P95/P99) 名词指南',
        'concurrency.guide.what_is_p': '什么是分位数 (Percentile)？',
        'concurrency.guide.what_is_p_desc': '分位数说明',
        'concurrency.guide.why_not_avg': '为什么压测必须看分位数？',
        'concurrency.guide.why_not_avg_desc': '平均值易被拉偏说明',
        'concurrency.guide.how_to_use': '如何根据分位数进行调优？',
        'concurrency.guide.how_to_use_desc': '调优建议说明',
      };
      return map[key] || key;
    },
  }),
}));

// 模拟 getErrorMessage / Mock getErrorMessage
vi.mock('@/utils/error', () => ({
  getErrorMessage: (e: unknown) => (e instanceof Error ? e.message : 'Unknown error'),
}));

/** 构造标准模拟响应 / Build mock concurrency test response */
const mockResponse: ConcurrencyTestResponse = {
  total: 200,
  success: 195,
  failed: 5,
  duration_ms: 1500,
  qps: 130.0,
  avg_latency_ms: 12.5,
  min_latency_ms: 1.2,
  max_latency_ms: 85.0,
  p50_latency_ms: 8.0,
  p90_latency_ms: 25.0,
  p95_latency_ms: 35.0,
  p99_latency_ms: 70.0,
};

describe('ConcurrencyTestPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('初始渲染：显示标题与空状态提示', () => {
    render(<ConcurrencyTestPanel />);
    expect(screen.getByText('并发压测')).toBeInTheDocument();
    expect(screen.getByText('配置参数后点击"运行压测"查看结果')).toBeInTheDocument();
  });

  it('渲染预设路径按钮', () => {
    render(<ConcurrencyTestPanel />);
    expect(screen.getByText('Mask PII')).toBeInTheDocument();
    expect(screen.getByText('DP Count')).toBeInTheDocument();
    expect(screen.getByText('DP Sum')).toBeInTheDocument();
    expect(screen.getByText('Hash')).toBeInTheDocument();
    expect(screen.getByText('Health Check')).toBeInTheDocument();
  });

  it('点击预设按钮切换目标路径', () => {
    render(<ConcurrencyTestPanel />);
    // 点击 DP Count 预设
    fireEvent.click(screen.getByText('DP Count'));
    // 路径输入框应变为 /v1/privacy/dp/count
    const pathInput = screen.getByPlaceholderText('/v1/privacy/mask');
    expect(pathInput).toHaveValue('/v1/privacy/dp/count');
  });

  it('点击 Health Check 预设切换为 GET 方法并清空请求体', () => {
    render(<ConcurrencyTestPanel />);
    fireEvent.click(screen.getByText('Health Check'));
    const pathInput = screen.getByPlaceholderText('/v1/privacy/mask');
    expect(pathInput).toHaveValue('/health');
    // GET 方法下请求体 textarea 不应渲染
    expect(screen.queryByPlaceholderText('{"field_name": "phone", "value": "13812345678"}')).not.toBeInTheDocument();
  });

  it('运行压测：成功时显示结果', async () => {
    mockConcurrencyTest.mockResolvedValue(mockResponse);
    render(<ConcurrencyTestPanel />);

    // 点击运行按钮
    fireEvent.click(screen.getByText('运行压测'));

    await waitFor(() => {
      expect(screen.getByText('130.0')).toBeInTheDocument(); // QPS
    });

    // 验证延迟统计（条形图 + 表格各出现一次，用 getAllByText）
    expect(screen.getAllByText('8.00 ms').length).toBeGreaterThanOrEqual(1); // P50
    expect(screen.getAllByText('25.00 ms').length).toBeGreaterThanOrEqual(1); // P90
    expect(screen.getAllByText('35.00 ms').length).toBeGreaterThanOrEqual(1); // P95
    expect(screen.getAllByText('70.00 ms').length).toBeGreaterThanOrEqual(1); // P99
    expect(screen.getByText('性能指标与分位数 (P50/P90/P95/P99) 名词指南')).toBeInTheDocument();
  });

  it('运行压测：失败时显示错误信息', async () => {
    mockConcurrencyTest.mockRejectedValue(new Error('Network error'));
    render(<ConcurrencyTestPanel />);

    fireEvent.click(screen.getByText('运行压测'));

    await waitFor(() => {
      expect(screen.getByText('Network error')).toBeInTheDocument();
    });
  });

  it('无效 JSON 请求体时显示错误提示', () => {
    render(<ConcurrencyTestPanel />);
    // 修改请求体为无效 JSON
    const textarea = screen.getByPlaceholderText('{"field_name": "phone", "value": "13812345678"}');
    fireEvent.change(textarea, { target: { value: '{invalid json}' } });

    // 点击运行
    fireEvent.click(screen.getByText('运行压测'));

    expect(screen.getByText('请求体 JSON 格式错误，请检查')).toBeInTheDocument();
    // API 不应被调用
    expect(mockConcurrencyTest).not.toHaveBeenCalled();
  });

  it('渲染 SummaryCard 组件：QPS 值显示', async () => {
    mockConcurrencyTest.mockResolvedValue(mockResponse);
    render(<ConcurrencyTestPanel />);

    fireEvent.click(screen.getByText('运行压测'));

    await waitFor(() => {
      expect(screen.getByText('吞吐量')).toBeInTheDocument();
      expect(screen.getByText('130.0')).toBeInTheDocument();
    });
  });
});
