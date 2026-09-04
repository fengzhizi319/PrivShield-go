/**
 * 通用异步动作 Hook / Generic Async Action Hook
 *
 * 统一封装「请求中 / 成功数据 / 错误信息」三态及其切换逻辑，
 * 消除各业务面板中重复的 setLoading / setError / setData 样板代码。
 * Uniformly encapsulates the "loading / data / error" tri-state and its
 * transitions, eliminating repetitive setLoading / setError / setData
 * boilerplate across business panels.
 *
 * 使用方式 / Usage：
 *   const action = useAsyncAction<ClassificationResponse>();
 *   await action.run(() => proxyRequest({...}).then(r => r.data), '评估失败');
 *   // action.loading / action.data / action.error 即可直接渲染
 *
 * 设计要点 / Design Notes：
 *   - run() 每次调用都会先重置三态，避免展示上一轮的陈旧结果；
 *   - 捕获到的异常若为 Error 取其 message，否则回退到 fallbackError；
 *   - reset() 供外部（如切换标准时）主动清空状态。
 *   - run() resets the tri-state on every call to avoid stale results;
 *   - Caught exceptions use Error.message, falling back to fallbackError;
 *   - reset() lets callers (e.g. on standard switch) clear state manually.
 */

/** 引入 React 状态与记忆 Hook / Import React state & memoization Hooks */
import { useCallback, useState } from 'react';
/** 引入统一错误消息提取工具 / Import unified error message extraction utility */
import { getErrorMessage } from '@/utils/error';

/**
 * 异步动作对外暴露的状态与操作。
 * State and operations exposed by an async action.
 */
export interface AsyncAction<T> {
  /** 成功后的数据（未完成或失败时为 null）/ Data on success (null otherwise) */
  data: T | null;
  /** 是否请求中 / Whether a request is in flight */
  loading: boolean;
  /** 错误信息（无错误时为 null）/ Error message (null when no error) */
  error: string | null;
  /**
   * 执行异步动作 / Execute the async action
   *
   * @param fn - 返回目标数据的异步函数 / Async function resolving to target data
   * @param fallbackError - 非 Error 异常时的回退文案 / Fallback text for non-Error throws
   */
  run: (fn: () => Promise<T>, fallbackError?: string) => Promise<void>;
  /** 主动清空三态 / Manually clear the tri-state */
  reset: () => void;
}

/**
 * 创建一组异步动作状态 / Create a set of async action state
 *
 * @typeParam T - 成功数据的类型 / Type of the success data
 * @returns 三态 + run/reset 操作 / Tri-state + run/reset operations
 */
export function useAsyncAction<T>(): AsyncAction<T> {
  /** 成功数据 / Success data */
  const [data, setData] = useState<T | null>(null);
  /** 加载中标记 / Loading flag */
  const [loading, setLoading] = useState(false);
  /** 错误信息 / Error message */
  const [error, setError] = useState<string | null>(null);

  /**
   * 执行异步动作并维护三态 / Run the async action while maintaining tri-state
   *
   * 使用 useCallback 保持引用稳定，避免触发依赖它的副作用重复执行。
   * Uses useCallback to keep a stable reference, avoiding redundant effect re-runs.
   */
  const run = useCallback(async (fn: () => Promise<T>, fallbackError = 'Operation failed') => {
    setLoading(true);   // 进入加载态 / Enter loading state
    setError(null);     // 清除上一轮错误 / Clear previous error
    setData(null);      // 清除上一轮结果 / Clear previous result
    try {
      const result = await fn();
      setData(result);  // 写入成功数据 / Store success data
    } catch (e) {
      // 统一经 getErrorMessage 安全提取（Error.message / 字符串 / 回退）
      // Uniformly extract via getErrorMessage (Error.message / string / fallback)
      setError(getErrorMessage(e, fallbackError));
    } finally {
      setLoading(false); // 结束加载态 / Exit loading state
    }
  }, []);

  /** 主动清空三态 / Manually clear the tri-state */
  const reset = useCallback(() => {
    setData(null);
    setError(null);
    setLoading(false);
  }, []);

  return { data, loading, error, run, reset };
}
