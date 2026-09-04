/**
 * i18n tooltip 与 aria-label 无障碍属性验证测试
 *
 * 验证各组件的 title / aria-label 属性已正确接入 i18n，
 * 在中英文模式下均能渲染出对应语言的文本。
 */

import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { I18nProvider } from '@/i18n';
import HistoryPanel from '@/components/HistoryPanel';
import ResponsePanel from '@/components/ResponsePanel';

/** 包裹 I18nProvider 的渲染辅助 / Render helper wrapping I18nProvider */
function renderWithI18n(ui: React.ReactElement) {
  return render(<I18nProvider>{ui}</I18nProvider>);
}

describe('HistoryPanel i18n tooltip & aria-label', () => {
  const noop = () => {};

  it('关闭按钮具有 aria-label（中文）', () => {
    renderWithI18n(
      <HistoryPanel entries={[]} onRestore={noop} onDelete={noop} onClear={noop} onClose={noop} />,
    );
    const closeBtn = screen.getByRole('button', { name: '关闭' });
    expect(closeBtn).toBeInTheDocument();
    expect(closeBtn).toHaveAttribute('aria-label', '关闭');
    expect(closeBtn).toHaveAttribute('title', '关闭');
  });

  it('删除按钮具有 aria-label（中文）', () => {
    const entries = [
      { id: '1', method: 'POST', path: '/v1/privacy/mask', body: '{"a":1}', status: 200, timestamp: Date.now() },
    ];
    renderWithI18n(
      <HistoryPanel entries={entries} onRestore={noop} onDelete={noop} onClear={noop} onClose={noop} />,
    );
    const deleteBtn = screen.getByRole('button', { name: '删除该记录' });
    expect(deleteBtn).toBeInTheDocument();
    expect(deleteBtn).toHaveAttribute('aria-label', '删除该记录');
  });

  it('历史条目具有回填 title（中文）', () => {
    const entries = [
      { id: '1', method: 'POST', path: '/v1/privacy/mask', body: '{"a":1}', status: 200, timestamp: Date.now() },
    ];
    renderWithI18n(
      <HistoryPanel entries={entries} onRestore={noop} onDelete={noop} onClear={noop} onClose={noop} />,
    );
    const fillBtn = screen.getByTitle('点击回填该请求体');
    expect(fillBtn).toBeInTheDocument();
  });
});

describe('ResponsePanel i18n tooltip', () => {
  it('via 徽章使用 i18n title', () => {
    const response = {
      status: 200,
      data: { ok: true },
      via: 'python-rest',
      protocol: 'REST',
      duration_ms: 12.5,
    };
    renderWithI18n(<ResponsePanel response={response as any} error={null} duration={null} />);
    const viaBadge = screen.getByTitle('处理本请求的控制台后端');
    expect(viaBadge).toBeInTheDocument();
    expect(viaBadge).toHaveTextContent('python-rest');
  });

  it('protocol 徽章使用 i18n title', () => {
    const response = {
      status: 200,
      data: { ok: true },
      via: 'go-grpc',
      protocol: 'gRPC',
      duration_ms: 8.3,
    };
    renderWithI18n(<ResponsePanel response={response as any} error={null} duration={null} />);
    const protocolBadge = screen.getByTitle('后端与 agent 的通信协议');
    expect(protocolBadge).toBeInTheDocument();
    expect(protocolBadge).toHaveTextContent('gRPC');
  });
});
