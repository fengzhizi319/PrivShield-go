/**
 * 后端 API 调用封装模块 / Backend API Client Module
 *
 * 所有与后端的 HTTP 交互都集中在这里，上层组件不直接使用 fetch：
 * All HTTP interactions with the backend are centralized here; upper-layer components never use fetch directly:
 *   - ``API_BASE`` 为可变基址，由 BackendSelector 切换后端时经 setBaseUrl 更新；
 *   - 默认值为空字符串（同源），生产环境下控制台与后端同域部署。
 *
 * 健壮性约定 / Robustness Conventions：
 *   - 所有请求经统一 ``request()`` 发出，附带 ``AbortController`` 超时（默认 60s）；
 *   - 非 2xx 响应统一抛出携带 ``detail`` 的 Error，由调用方展示；
 *   - 可选 API Key（与后端 ``CONSOLE_API_KEY`` 对应）：设置后为请求附加
 *     ``Authorization: Bearer`` 头，未设置则完全不影响本地开发。
 */

/** 导入所有前后端数据契约类型 / Import all frontend-backend data contract types */
import type { ProxyRequest, ProxyResponse, ConsoleHealth, EndpointSample, BatchRequestItem, BatchResponse, FileOperation, UploadResponse, LbTestRequest, LbTestResponse, OpsDiagnostics, StandardsResponse, ConcurrencyTestRequest, ConcurrencyTestResponse, MedicalPipelineRequest, MedicalPipelineResponse } from '@/types/api';

/** 当前后端基址（空串表示同源，即请求发往当前页面所在的服务器）。 */
/** Current backend base URL (empty string means same-origin, i.e. requests go to the server hosting the page). */
let API_BASE = '';

/** 请求超时时间（毫秒），超过此时间将中断请求并抛出超时错误。 */
/** Request timeout in milliseconds; exceeding this will abort the request and throw a timeout error. */
const REQUEST_TIMEOUT_MS = 180_000;
const MAX_IDEMPOTENT_RETRIES = 2;

/**
 * 可选控制台 API Key。默认从构建期环境变量 ``VITE_CONSOLE_API_KEY`` 读取，
 * 也可经 setApiKey 运行时注入；为空时不附加任何鉴权头。
 * Optional console API Key. Defaults from build-time env ``VITE_CONSOLE_API_KEY``,
 * can also be injected at runtime via setApiKey; when empty, no auth header is attached.
 */
let API_KEY: string = (import.meta.env.VITE_CONSOLE_API_KEY as string | undefined) ?? '';

let PROTOCOL = 'gRPC';

/**
 * 切换后端基址与通信协议（gRPC / REST）。
 * Switch backend base URL and communication protocol (gRPC / REST).
 *
 * @param baseUrl - 新后端地址或带协议后缀（如 http://127.0.0.1:8081?protocol=rest）
 */
export function setBaseUrl(baseUrl: string): void {
  if (baseUrl.includes('protocol=rest')) {
    PROTOCOL = 'REST';
  } else if (baseUrl.includes('protocol=grpc')) {
    PROTOCOL = 'gRPC';
  }
  const cleanUrl = baseUrl.split('?')[0];
  API_BASE = cleanUrl.replace(/\/$/, '');
}

/**
 * 运行时设置控制台 API Key（空串表示关闭鉴权头）。
 * Set console API Key at runtime (empty string disables auth header).
 *
 * @param key - API 密钥，空串则不附加 Authorization 头 / API key, empty string means no Authorization header
 */
export function setApiKey(key: string): void {
  API_KEY = key;
}

/**
 * 在给定请求头基础上附加可选鉴权头与协议标识 / Attach optional auth header and protocol header
 */
function buildHeaders(extra?: Record<string, string>): Record<string, string> {
  const headers: Record<string, string> = { ...extra };
  if (API_KEY) headers['Authorization'] = `Bearer ${API_KEY}`;
  if (PROTOCOL) headers['X-PrivShield-Protocol'] = PROTOCOL;
  return headers;
}

/**
 * 统一请求入口 / Unified Request Entry Point
 */
async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const { headers, ...rest } = init;
  const method = (rest.method ?? 'GET').toString().toUpperCase();
  const retryableMethod = ['GET', 'HEAD', 'OPTIONS'].includes(method);
  let lastError: unknown;

  const sep = path.includes('?') ? '&' : '?';
  const urlWithProtocol = PROTOCOL === 'REST' && !path.includes('protocol=')
    ? `${API_BASE}${path}${sep}protocol=rest`
    : `${API_BASE}${path}`;

  for (let attempt = 0; attempt <= (retryableMethod ? MAX_IDEMPOTENT_RETRIES : 0); attempt += 1) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
    try {
      const res = await fetch(urlWithProtocol, {
        ...rest,
        headers: buildHeaders(headers as Record<string, string> | undefined),
        signal: controller.signal,
      });

      // 非 2xx 响应统一抛出携带 detail 的 Error / Non-2xx responses throw Error with detail
      // 兼容统一信封格式 {code, message, detail, trace_id, timestamp} 与旧格式 {detail}
      if (!res.ok) {
        const err = await res.json().catch(() => ({ detail: res.statusText }));
        // 优先使用统一信封的 message 字段，回退到旧 detail 字段
        const detailText = typeof err.detail === 'string' ? err.detail : JSON.stringify(err.detail);
        const errMsg = err.message || detailText || res.statusText;
        const error = new Error(errMsg) as Error & { status?: number; code?: string; traceId?: string };
        error.status = res.status;
        if (err.code) error.code = err.code;
        if (err.trace_id) error.traceId = err.trace_id;
        if (err.trace_id) {
          console.error(`[PrivShield API Error] ${err.code || 'UNKNOWN'} (TraceID: ${err.trace_id}): ${errMsg}`);
        }
        // Dispatch global error event for unified toast/notification handling
        // 派发全局错误事件，供统一 Toast/通知组件监听
        if (err.code) {
          window.dispatchEvent(new CustomEvent('privshield:api-error', {
            detail: { code: err.code, message: errMsg, detail: err.detail, trace_id: err.trace_id },
          }));
        }
        if (![502, 503, 504].includes(res.status) || attempt >= MAX_IDEMPOTENT_RETRIES) throw error;
        lastError = error;
      } else {
        // 先读取原始文本，再尝试 JSON 解析（便于错误时展示原始内容）
        const text = await res.text();
        try {
          return JSON.parse(text) as T;
        } catch {
          throw new Error(`Response JSON parse failed (HTTP ${res.status}): ${text.slice(0, 100)}`);
        }
      }
    } catch (e) {
      if ((e as Error).name === 'AbortError') {
        throw new Error(`Request timed out (${REQUEST_TIMEOUT_MS / 1000}s), please check backend availability`);
      }
      lastError = e;
      const status = (e as Error & { status?: number }).status;
      if (!retryableMethod || attempt >= MAX_IDEMPOTENT_RETRIES || (status !== undefined && ![502, 503, 504].includes(status))) {
        throw e;
      }
    } finally {
      clearTimeout(timer);
    }
    await new Promise((resolve) => setTimeout(resolve, 250 * 2 ** attempt));
  }

  throw lastError instanceof Error ? lastError : new Error('Request failed');
}

/**
 * 获取后端与 agent 的连通性状态 / Fetch backend-to-agent connectivity status
 *
 * 调用后端 /health 端点，返回后端自身状态及其与 agent 的通信结果。
 * Calls backend /health endpoint, returns backend's own status and its communication result with agent.
 *
 * @returns 健康检查结果（包含 backend/agent/agent_url/protocol 等字段）/ Health check result
 */
export async function fetchHealth(): Promise<ConsoleHealth> {
  return request<ConsoleHealth>('/health');
}

/**
 * 获取所有端点示例列表 / Fetch all endpoint sample list
 *
 * 后端 /v1/samples 返回 { samples: [...] } 结构，本函数解包后返回纯数组。
 * Backend /v1/samples returns { samples: [...] } structure; this function unwraps and returns the plain array.
 *
 * @returns 端点示例数组（用于侧边栏与总览页渲染）/ Endpoint samples array (for sidebar & overview rendering)
 */
export async function fetchSamples(): Promise<EndpointSample[]> {
  // 请求后端示例接口 / Request backend samples endpoint
  const data = await request<{ samples: EndpointSample[] }>('/v1/samples');
  // 解包 samples 字段返回纯数组 / Unwrap samples field and return plain array
  return data.samples;
}

/**
 * 通用代理：把单个请求交给后端转发到 agent / Generic proxy: forward a single request to agent via backend
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 将 ProxyRequest 序列化为 JSON；
 *   2. POST 到后端 /v1/proxy；
 *   3. 后端转发到 agent 并包装响应（含 status/duration_ms/data/via/protocol）；
 *   4. 后端返回非 2xx 时抛出 Error（携带 detail），由调用方展示。
 *
 * @param req - 代理请求体（method + path + body）/ Proxy request body (method + path + body)
 * @returns 包装后的代理响应 / Wrapped proxy response
 */
export async function proxyRequest(req: ProxyRequest): Promise<ProxyResponse> {
  return request<ProxyResponse>('/v1/proxy', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' }, // JSON 请求体 / JSON request body
    body: JSON.stringify(req), // 序列化请求对象 / Serialize request object
  });
}

/**
 * 批量测试：将一组请求提交给后端逐个转发，返回汇总结果。
 * Batch test: submit a group of requests to backend for sequential forwarding, return aggregated results.
 *
 * 单个请求失败不会中断整个批次（后端逐个执行并记录结果）。
 * A single request failure won't abort the entire batch (backend executes sequentially and records results).
 *
 * @param requests - 批量请求项数组（method + path + body）/ Batch request items array
 * @returns 批量测试汇总响应（total/passed/failed/results）/ Batch test summary response
 */
export async function batchRequest(requests: BatchRequestItem[]): Promise<BatchResponse> {
  return request<BatchResponse>('/v1/batch', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' }, // JSON 请求体 / JSON request body
    body: JSON.stringify({ requests }), // 包装为 { requests: [...] } 结构 / Wrap as { requests: [...] } structure
  });
}

/**
 * 数据文件隐私处理：以 multipart 上传文件并指定操作类型。
 * Data file privacy processing: upload file as multipart with specified operation type.
 *
 * 详细逻辑 / Detailed Logic：
 *   1. 构造 FormData，附加 file、operation、params 三个字段；
 *   2. POST 到后端 /v1/upload；
 *   3. 后端转发到 agent 的 process_file 端点，返回包装后的处理结果。
 *
 * 注意：不手动设置 Content-Type，由浏览器自动生成带 boundary 的 multipart 头。
 * Note: Don't manually set Content-Type; browser auto-generates multipart header with boundary.
 *
 * @param file - 要上传的 CSV/JSON 文件 / The CSV/JSON file to upload
 * @param operation - 操作类型（mask_dataframe / k_anonymize）/ Operation type
 * @param params - 操作参数（如 columns/k 等）/ Operation parameters (e.g. columns/k)
 * @returns 上传处理响应 / Upload processing response
 */
export async function uploadFile(
  file: File,
  operation: FileOperation,
  params: Record<string, unknown>,
): Promise<UploadResponse> {
  // 创建 FormData 对象用于 multipart 上传 / Create FormData object for multipart upload
  const form = new FormData();
  // 附加文件字段（后端以 'file' 键读取）/ Append file field (backend reads by 'file' key)
  form.append('file', file);
  // 附加操作类型字段 / Append operation type field
  form.append('operation', operation);
  // 附加参数 JSON 字符串（后端解析为 dict）/ Append params JSON string (backend parses as dict)
  form.append('params', JSON.stringify(params));

  // 发送 POST 请求，不设置 Content-Type（浏览器自动添加 multipart/form-data + boundary）
  // Send POST request without Content-Type (browser auto-adds multipart/form-data + boundary)
  return request<UploadResponse>('/v1/upload', {
    method: 'POST',
    body: form, // FormData 作为请求体 / FormData as request body
  });
}

/**
 * 负载均衡测试：提交多个后端地址与策略，
 * Load balancer test: submit multiple backend addresses and strategy,
 * 后端按策略分发探测请求并返回各节点统计。
 * backend distributes probe requests by strategy and returns per-node statistics.
 *
 * @param req - 负载均衡测试请求（backends + num_requests + strategy）/ LB test request
 * @returns 负载均衡测试响应（含各节点命中数/成功率/延迟）/ LB test response (per-node hits/success rate/latency)
 */
export async function lbTest(req: LbTestRequest): Promise<LbTestResponse> {
  return request<LbTestResponse>('/v1/lb_test', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' }, // JSON 请求体 / JSON request body
    body: JSON.stringify(req), // 序列化请求对象 / Serialize request object
  });
}

/**
 * 获取 Agent 运维诊断信息 / Fetch Agent ops diagnostics
 *
 * 通过通用代理转发 GET /v1/ops/diagnostics 到 agent，
 * 返回 NER/LLM 降级链路、依赖安装情况、模型文件与硬件加速状态。
 * Forwards GET /v1/ops/diagnostics to agent via generic proxy, returns
 * NER/LLM degradation chain, dependency installation, model files and hardware acceleration status.
 *
 * 该端点为 REST 专用：Go 后端 gRPC 无对应方法时会自动回退到 REST 代理
 *（proxyRest），因此 REST 与 gRPC 两种协议均可使用。
 * This endpoint is REST-only: Python backend forwards directly; Go backend
 * automatically falls back to REST proxy when gRPC has no matching method.
 *
 * @param refresh - 为 true 时附加 ?refresh=true，令 agent 失效引擎探测缓存并
 *   重新探测（用于服务运行期间补装依赖/模型后刷新结论）；重新探测会加载模型，
 *   耗时较长，仅用于用户显式点击“刷新诊断”。
 *   When true, appends ?refresh=true so the agent invalidates its engine probe
 *   cache and re-probes (used after installing deps/models while the service is running).
 * @returns 运维诊断信息（含引擎状态/依赖/模型/硬件）/ Ops diagnostics (engines/deps/models/hardware)
 */
export async function fetchDiagnostics(refresh = false): Promise<OpsDiagnostics> {
  // 经通用代理转发 GET 请求到 agent 的诊断端点 / Forward GET request to agent diagnostics endpoint via generic proxy
  // 查询串随 path 一并传递，Go BFF 原样转发到 agent
  // Query string travels with path; both Python/Go proxy backends forward it as-is
  const path = refresh ? '/v1/ops/diagnostics?refresh=true' : '/v1/ops/diagnostics';
  const resp = await proxyRequest({ method: 'GET', path });
  // proxyRequest 返回包装结构，真实诊断数据在 data 字段 / Wrapped response; actual diagnostics in data field
  return resp.data as OpsDiagnostics;
}

/**
 * 获取所有可用的动态分类分级标准 / Fetch all available dynamic classification standards
 *
 * 通过通用代理转发 GET /v1/dynclassification/standards 到 agent，
 * 返回标准 ID 列表与详情（含等级体系），供动态分类面板的
 * “当前标准”展示与标准切换器渲染。
 * Forwards GET /v1/dynclassification/standards to agent via generic proxy;
 * returns standard IDs and details (incl. level systems) for the standard switcher.
 *
 * @returns 标准列表响应（standards + details）/ Standards response (standards + details)
 */
export async function fetchStandards(): Promise<StandardsResponse> {
  // 经通用代理转发 GET 请求到 agent 的标准列表端点 / Forward GET request to agent standards endpoint via generic proxy
  const resp = await proxyRequest({ method: 'GET', path: '/v1/dynclassification/standards' });
  // proxyRequest 返回包装结构，真实数据在 data 字段 / Wrapped response; actual data in data field
  return resp.data as StandardsResponse;
}

/**
 * 并发压测：以指定并发度向 agent 发送请求并统计延迟分布与吞吐量。
 * Concurrency test: send requests to agent at specified concurrency and collect latency/throughput statistics.
 *
 * @param req - 并发压测请求（路径/并发数/总请求数）/ Concurrency test request
 * @returns 压测结果汇总（QPS/延迟分布/成功率）/ Test result summary
 */
export async function concurrencyTest(req: ConcurrencyTestRequest): Promise<ConcurrencyTestResponse> {
  return request<ConcurrencyTestResponse>('/v1/concurrency_test', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
}

/**
 * 医疗敏感数据全流程治理：对康养医疗记录或 kangyang.csv 执行分类分级与 L4/L5 抹平脱敏。
 * Medical privacy pipeline: classifies & desensitizes medical records or kangyang.csv.
 */
export async function runMedicalPipeline(req: MedicalPipelineRequest = {}): Promise<MedicalPipelineResponse> {
  const raw = await request<any>('/v1/medical_pipeline', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  // 若代理后端包装在 raw.data 中，透明取内层 data 字段；否则取 raw 本身
  if (raw && typeof raw === 'object' && 'data' in raw && raw.data && typeof raw.data === 'object' && 'summary' in raw.data) {
    return raw.data as MedicalPipelineResponse;
  }
  return raw as MedicalPipelineResponse;
}

/**
 * 医保结算数据全流程治理：对 yibao.csv 18 字段执行分类分级与 L4/L5 抹平脱敏。
 * Yibao pipeline: classifies & desensitizes 18-field medical insurance records or yibao.csv.
 */
export async function runYibaoPipeline(req: MedicalPipelineRequest = {}): Promise<MedicalPipelineResponse> {
  const raw = await request<any>('/v1/yibao_pipeline', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ...req, dataset: 'yibao' }),
  });
  if (raw && typeof raw === 'object' && 'data' in raw && raw.data && typeof raw.data === 'object' && 'summary' in raw.data) {
    return raw.data as MedicalPipelineResponse;
  }
  return raw as MedicalPipelineResponse;
}
