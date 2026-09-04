/**
 * useAsyncAction Hook 单元测试。
 * Unit tests for the useAsyncAction Hook.
 *
 * 覆盖：
 *   1. 初始三态（data=null / loading=false / error=null）；
 *   2. 成功路径写入 data 并结束 loading；
 *   3. Error 异常取 message 写入 error；
 *   4. 非 Error 异常回退到 fallbackError；
 *   5. reset 清空三态；
 *   6. run 开始时即清空上一轮结果（避免陈旧数据）。
 */
import { describe, it, expect } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useAsyncAction } from '../useAsyncAction';

describe('useAsyncAction 异步动作 Hook', () => {
  it('初始状态：data 为 null / 非加载中 / 无错误', () => {
    const { result } = renderHook(() => useAsyncAction<number>());
    expect(result.current.data).toBeNull();
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeNull();
  });

  it('成功后写入 data 并结束 loading', async () => {
    const { result } = renderHook(() => useAsyncAction<number>());
    await act(async () => {
      await result.current.run(async () => 42);
    });
    expect(result.current.data).toBe(42);
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeNull();
  });

  it('Error 异常时取 message 写入 error', async () => {
    const { result } = renderHook(() => useAsyncAction<number>());
    await act(async () => {
      await result.current.run(async () => {
        throw new Error('出错了');
      });
    });
    expect(result.current.error).toBe('出错了');
    expect(result.current.data).toBeNull();
    expect(result.current.loading).toBe(false);
  });

  it('字符串异常直接作为错误消息（比泛化回退更具信息量）', async () => {
    const { result } = renderHook(() => useAsyncAction<number>());
    await act(async () => {
      await result.current.run(async () => {
        // 故意抛出字符串以验证 getErrorMessage 原样返回 / throw string to verify verbatim return
        // eslint-disable-next-line no-throw-literal
        throw 'string failure';
      }, '回退文案');
    });
    expect(result.current.error).toBe('string failure');
  });

  it('无可读消息的异常（对象）回退到 fallbackError', async () => {
    const { result } = renderHook(() => useAsyncAction<number>());
    await act(async () => {
      await result.current.run(async () => {
        // 抛出无 message 的对象以验证回退逻辑 / throw object without message to verify fallback
        // eslint-disable-next-line no-throw-literal
        throw { code: 500 };
      }, '回退文案');
    });
    expect(result.current.error).toBe('回退文案');
  });

  it('reset 清空三态', async () => {
    const { result } = renderHook(() => useAsyncAction<number>());
    await act(async () => {
      await result.current.run(async () => 1);
    });
    expect(result.current.data).toBe(1);
    act(() => {
      result.current.reset();
    });
    expect(result.current.data).toBeNull();
    expect(result.current.error).toBeNull();
    expect(result.current.loading).toBe(false);
  });

  it('run 开始时即清空上一轮结果（避免陈旧数据）', async () => {
    const { result } = renderHook(() => useAsyncAction<number>());
    await act(async () => {
      await result.current.run(async () => 1);
    });
    expect(result.current.data).toBe(1);

    // 手动控制第二个 Promise 的完成时机 / manually control the second promise resolution
    let resolveFn: (v: number) => void = () => {};
    const pending = new Promise<number>((resolve) => {
      resolveFn = resolve;
    });
    act(() => {
      void result.current.run(async () => pending);
    });
    // 加载期间旧数据应已被清空 / stale data cleared while loading
    expect(result.current.loading).toBe(true);
    expect(result.current.data).toBeNull();

    await act(async () => {
      resolveFn(2);
      await pending;
    });
    expect(result.current.data).toBe(2);
    expect(result.current.loading).toBe(false);
  });
});
