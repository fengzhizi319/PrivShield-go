/**
 * 错误消息提取工具 / Error Message Extraction Utility
 *
 * 背景 / Background：
 *   各面板的 catch 分支历史上各自用 ``(e as Error).message`` 提取错误消息，
 *   该写法在抛出的并非 Error 实例（例如字符串 / 对象）时会得到 ``undefined``，
 *   导致界面显示空白错误；``catch (e: any)`` 还会触发 no-explicit-any 告警。
 *   Historically each panel's catch block used ``(e as Error).message`` to extract
 *   the error message, which yields ``undefined`` when the thrown value is not an
 *   Error instance (e.g. a string / object), resulting in blank error text on screen;
 *   ``catch (e: any)`` also triggers the no-explicit-any lint warning.
 *
 * 方案 / Solution：
 *   统一收敛到 ``getErrorMessage``，安全地从 ``unknown`` 异常提取可读消息，
 *   与 ``useAsyncAction`` 内部的提取逻辑保持一致（单一事实来源）。
 *   Consolidate into ``getErrorMessage`` which safely extracts a readable message from
 *   an ``unknown`` exception, staying consistent with the extraction logic inside
 *   ``useAsyncAction`` (single source of truth).
 */

/**
 * 从未知异常中安全提取错误消息 / Safely extract an error message from an unknown exception
 *
 * 提取规则（按优先级）/ Extraction rules (by priority)：
 *   1. ``Error`` 实例且 ``message`` 非空 → 返回 ``message``；
 *      Error instance with non-empty message → return the message;
 *   2. 字符串异常 → 原样返回；
 *      string exception → return as-is;
 *   3. 其余情况（null / undefined / 对象等）→ 返回 ``fallback``。
 *      otherwise (null / undefined / object, etc.) → return the fallback.
 *
 * @param e - catch 捕获的未知异常 / The unknown exception caught
 * @param fallback - 无法提取消息时的回退文案 / Fallback text when no message can be extracted
 * @returns 可读的错误消息 / A human-readable error message
 */
export function getErrorMessage(e: unknown, fallback = '操作失败'): string {
  // Error 实例且 message 非空：返回真实错误消息
  // Error instance with non-empty message: return the real error message
  if (e instanceof Error && e.message) {
    return e.message;
  }
  // 字符串异常：原样返回（兼容 throw 'xxx' 的旧代码）
  // String exception: return as-is (compatible with legacy `throw 'xxx'`)
  if (typeof e === 'string' && e) {
    return e;
  }
  // 其余情况回退 / Otherwise fallback
  return fallback;
}
