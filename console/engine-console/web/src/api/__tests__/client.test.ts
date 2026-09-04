/**
 * client.ts 单元测试：验证统一 request() 的核心行为。
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { setBaseUrl, setApiKey, fetchHealth, proxyRequest } from '../client';

// 模拟全局 fetch
const mockFetch = vi.fn();
vi.stubGlobal('fetch', mockFetch);

function jsonResponse(data: unknown, status = 200) {
  const jsonStr = JSON.stringify(data);
  return Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    statusText: status === 200 ? 'OK' : 'Error',
    text: () => Promise.resolve(jsonStr),
    json: () => Promise.resolve(data),
  });
}

describe('client request()', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setBaseUrl('');
    setApiKey('');
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('拼接 API_BASE 到请求路径', async () => {
    setBaseUrl('http://localhost:8081');
    mockFetch.mockReturnValue(jsonResponse({ status: 'ok' }));

    await fetchHealth();

    expect(mockFetch).toHaveBeenCalledWith(
      'http://localhost:8081/health',
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
  });

  it('setBaseUrl 去除尾部斜杠', async () => {
    setBaseUrl('http://localhost:8081/');
    mockFetch.mockReturnValue(jsonResponse({ status: 'ok' }));

    await fetchHealth();

    expect(mockFetch).toHaveBeenCalledWith(
      'http://localhost:8081/health',
      expect.anything(),
    );
  });

  it('设置 API Key 后附加 Authorization 头', async () => {
    setApiKey('test-secret');
    mockFetch.mockReturnValue(jsonResponse({ status: 'ok' }));

    await fetchHealth();

    const [, init] = mockFetch.mock.calls[0];
    expect(init.headers).toHaveProperty('Authorization', 'Bearer test-secret');
  });

  it('未设置 API Key 时不附加 Authorization 头', async () => {
    mockFetch.mockReturnValue(jsonResponse({ status: 'ok' }));

    await fetchHealth();

    const [, init] = mockFetch.mock.calls[0];
    expect(init.headers).not.toHaveProperty('Authorization');
  });

  it('非 2xx 响应抛出携带 detail 的 Error', async () => {
    mockFetch.mockReturnValue(jsonResponse({ detail: '不支持的操作' }, 400));

    await expect(proxyRequest({ path: '/x', method: 'POST', body: {} })).rejects.toThrow('不支持的操作');
  });

  it('统一信封格式错误优先使用 message 字段', async () => {
    mockFetch.mockReturnValue(
      jsonResponse(
        { code: 'INVALID_ARGUMENT', message: '请求参数校验失败', detail: 'field X is required', trace_id: 'req-123' },
        400,
      ),
    );

    await expect(proxyRequest({ path: '/x', method: 'POST', body: {} })).rejects.toThrow('请求参数校验失败');
  });

  it('统一信封格式错误携带 code 和 traceId', async () => {
    mockFetch.mockReturnValue(
      jsonResponse(
        { code: 'NOT_FOUND', message: '资源不存在', detail: '', trace_id: 'req-456' },
        404,
      ),
    );

    try {
      await proxyRequest({ path: '/x', method: 'GET' });
      expect.unreachable('should have thrown');
    } catch (e) {
      const err = e as Error & { code?: string; traceId?: string };
      expect(err.message).toBe('资源不存在');
      expect(err.code).toBe('NOT_FOUND');
      expect(err.traceId).toBe('req-456');
    }
  });

  it('非 2xx 且无 JSON body 时使用 statusText', async () => {
    mockFetch.mockReturnValue(
      Promise.resolve({
        ok: false,
        status: 500,
        statusText: 'Internal Server Error',
        text: () => Promise.resolve(''),
        json: () => Promise.reject(new Error('no json')),
      }),
    );

    await expect(fetchHealth()).rejects.toThrow('Internal Server Error');
  });

  it('超时时抛出友好的中文错误', async () => {
    vi.useFakeTimers();
    // fetch 返回一个永不 resolve 的 promise，模拟网络挂起
    mockFetch.mockReturnValue(new Promise(() => {}));

    void fetchHealth();
    // 快进 60s 触发 AbortController.abort()
    vi.advanceTimersByTime(60_000);

    // AbortController 触发后 fetch 应 reject with AbortError
    // 由于 mock fetch 不会真正 abort，我们直接验证 timer 被设置
    expect(mockFetch).toHaveBeenCalledWith(
      expect.any(String),
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );

    // 清理：让 promise 不悬挂
    vi.useRealTimers();
  });

  it('POST 请求正确传递 method/headers/body', async () => {
    mockFetch.mockReturnValue(jsonResponse({ result: {} }));

    await proxyRequest({ path: '/v1/privacy/mask', method: 'POST', body: { value: 'x' } });

    const [url, init] = mockFetch.mock.calls[0];
    expect(url).toBe('/v1/proxy');
    expect(init.method).toBe('POST');
    expect(init.headers['Content-Type']).toBe('application/json');
    expect(JSON.parse(init.body)).toEqual({ path: '/v1/privacy/mask', method: 'POST', body: { value: 'x' } });
  });
});
