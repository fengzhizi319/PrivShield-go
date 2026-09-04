/**
 * getErrorMessage 错误消息提取工具单元测试。
 * Unit tests for the getErrorMessage error-extraction utility.
 *
 * 覆盖：
 *   1. Error 实例取 message；
 *   2. message 为空的 Error 回退；
 *   3. 字符串异常原样返回；
 *   4. null / undefined / 对象等回退到默认或自定义 fallback。
 */
import { describe, it, expect } from 'vitest';
import { getErrorMessage } from '../error';

describe('getErrorMessage', () => {
  it('Error 实例返回其 message', () => {
    expect(getErrorMessage(new Error('boom'))).toBe('boom');
  });

  it('message 为空的 Error 回退到默认文案', () => {
    expect(getErrorMessage(new Error(''))).toBe('操作失败');
  });

  it('message 为空的 Error 回退到自定义 fallback', () => {
    expect(getErrorMessage(new Error(''), '自定义失败')).toBe('自定义失败');
  });

  it('字符串异常原样返回', () => {
    expect(getErrorMessage('string failure')).toBe('string failure');
  });

  it('空字符串异常回退（避免空白错误）', () => {
    expect(getErrorMessage('', '回退')).toBe('回退');
  });

  it('null / undefined 回退到默认文案', () => {
    expect(getErrorMessage(null)).toBe('操作失败');
    expect(getErrorMessage(undefined)).toBe('操作失败');
  });

  it('普通对象异常回退到自定义 fallback', () => {
    expect(getErrorMessage({ code: 500 }, '请求异常')).toBe('请求异常');
  });

  it('数字异常回退', () => {
    expect(getErrorMessage(42, '数值错误')).toBe('数值错误');
  });
});
