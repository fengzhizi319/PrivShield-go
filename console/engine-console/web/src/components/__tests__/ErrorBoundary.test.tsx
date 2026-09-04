/**
 * ErrorBoundary 单元测试：验证正常渲染与错误降级行为。
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import type { ReactNode } from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import ErrorBoundary from '../ErrorBoundary';
import { I18nProvider } from '@/i18n';

// 模拟 Icon 组件（避免引入完整图标库）
vi.mock('@/components/icons', () => ({
  Icon: ({ name }: { name: string }) => <span data-testid={`icon-${name}`} />,
}));

/** 用 I18nProvider 包裹 ErrorBoundary（类组件经 contextType 读取翻译）。 */
function renderBoundary(children: ReactNode) {
  return render(<I18nProvider>{children}</I18nProvider>);
}

/** 故意在渲染时抛错的组件。 */
function Bomb({ shouldThrow }: { shouldThrow: boolean }) {
  if (shouldThrow) throw new Error('测试爆炸');
  return <div data-testid="safe-content">正常内容</div>;
}

describe('ErrorBoundary', () => {
  afterEach(() => {
    localStorage.removeItem('console-lang');
  });

  it('子组件正常时渲染 children', () => {
    renderBoundary(
      <ErrorBoundary>
        <Bomb shouldThrow={false} />
      </ErrorBoundary>,
    );

    expect(screen.getByTestId('safe-content')).toBeInTheDocument();
  });

  it('子组件抛错时展示降级界面', () => {
    // 抑制 React 默认的 console.error 输出
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});

    renderBoundary(
      <ErrorBoundary>
        <Bomb shouldThrow={true} />
      </ErrorBoundary>,
    );

    expect(screen.getByText('界面渲染出错')).toBeInTheDocument();
    expect(screen.getByText('测试爆炸')).toBeInTheDocument();

    spy.mockRestore();
  });

  it('英文语言下降级文案为英文', () => {
    localStorage.setItem('console-lang', 'en');
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});

    renderBoundary(
      <ErrorBoundary>
        <Bomb shouldThrow={true} />
      </ErrorBoundary>,
    );

    expect(screen.getByText('UI Render Error')).toBeInTheDocument();
    expect(screen.getByText('Retry')).toBeInTheDocument();

    spy.mockRestore();
  });

  it('点击重试按钮后重新渲染子树', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});

    // 使用可控的 shouldThrow 状态
    let throwFlag = true;
    function ControlledBomb() {
      if (throwFlag) throw new Error('临时错误');
      return <div data-testid="recovered">已恢复</div>;
    }

    renderBoundary(
      <ErrorBoundary>
        <ControlledBomb />
      </ErrorBoundary>,
    );

    // 初始应显示错误
    expect(screen.getByText('界面渲染出错')).toBeInTheDocument();

    // 修复错误后点击重试
    throwFlag = false;
    fireEvent.click(screen.getByText('重试'));

    expect(screen.getByTestId('recovered')).toBeInTheDocument();

    spy.mockRestore();
  });
});
