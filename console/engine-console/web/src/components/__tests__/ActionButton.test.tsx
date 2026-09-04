/**
 * ActionButton 单元测试：验证通用动作按钮的加载态、禁用态与图标渲染。
 * ActionButton unit tests: verify loading state, disabled state and icon rendering.
 */
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import ActionButton from '../ActionButton';

// 模拟 Icon 组件（避免引入完整图标库）/ Mock Icon component
vi.mock('@/components/icons', () => ({
  Icon: ({ name }: { name: string }) => <span data-testid={`icon-${name}`} />,
}));

describe('ActionButton', () => {
  it('非加载态渲染图标与文案', () => {
    render(<ActionButton icon="send">提交</ActionButton>);
    expect(screen.getByRole('button', { name: '提交' })).toBeInTheDocument();
    expect(screen.getByTestId('icon-send')).toBeInTheDocument();
  });

  it('加载态显示加载文案并禁用', () => {
    render(
      <ActionButton loading icon="send" loadingText="处理中…">
        提交
      </ActionButton>,
    );
    const btn = screen.getByRole('button', { name: '处理中…' });
    expect(btn).toBeDisabled();
    // 加载态不渲染图标 / Icon hidden while loading
    expect(screen.queryByTestId('icon-send')).not.toBeInTheDocument();
  });

  it('加载态未提供 loadingText 时沿用 children', () => {
    render(
      <ActionButton loading>
        提交
      </ActionButton>,
    );
    expect(screen.getByRole('button', { name: '提交' })).toBeDisabled();
  });

  it('显式 disabled 时禁用且不触发点击', () => {
    const onClick = vi.fn();
    render(
      <ActionButton disabled onClick={onClick}>
        提交
      </ActionButton>,
    );
    const btn = screen.getByRole('button', { name: '提交' });
    expect(btn).toBeDisabled();
    fireEvent.click(btn);
    expect(onClick).not.toHaveBeenCalled();
  });

  it('可用态点击触发回调', () => {
    const onClick = vi.fn();
    render(<ActionButton onClick={onClick}>提交</ActionButton>);
    fireEvent.click(screen.getByRole('button', { name: '提交' }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('className 追加到基础样式之后', () => {
    render(
      <ActionButton className="w-full">
        提交
      </ActionButton>,
    );
    const btn = screen.getByRole('button', { name: '提交' });
    expect(btn.className).toContain('w-full');
    expect(btn.className).toContain('bg-indigo-600');
  });
});
