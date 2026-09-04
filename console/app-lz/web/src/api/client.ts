/**
 * API 客户端层 — 封装与 Go BFF 后端的所有 HTTP 通信。
 *
 * 设计原则：
 *  1. 统一通过 fetchJSON<T> 泛型函数处理请求/响应，自动设置 Content-Type 和错误提取
 *  2. 所有方法返回 Promise，由调用方（App.tsx 中的 fetch 函数）决定如何处理错误
 *  3. BASE_URL 使用相对路径 '/v1/lz'，由 Vite dev proxy 或 Nginx 反代到 Go BFF :8081
 *
 * API 分组（对应 BFF handlers.go 中的路由组）：
 *  1. 拓扑 & 网格健康   → GET /topology, POST /probe/all
 *  2. 任务 & 租约       → GET/POST /tasks/*, GET /tasks/leases
 *  3. 测试套件运行器    → GET /suites, POST /suites/run
 *  4. 审计 & Merkle 验真 → GET /audit/logs, POST /audit/verify
 *  5. Prometheus 指标   → GET /metrics, GET /metrics/parsed
 *  6. 预设数据 API      → GET /data-api/definitions, POST /data-api/invoke
 */
import {
  TopologyResponse,
  DispatchRequest,
  DispatchResponse,
  TasksResponse,
  Task,
  LeasedTasksResponse,
  TestSuiteCase,
  RunTestSuiteRequest,
  RunTestSuiteResponse,
  AuditLogItem,
  AuditVerifyResponse,
  DataApiDef,
  DataApiSessionResponse,
  LoginRequest,
  RegisterRequest,
  AuthResponse,
  User,
} from '../types/api';

/** BFF 统一 API 前缀（Vite proxy / Nginx 将此路径反代到 Go BFF :8081） */
const BASE_URL = '/v1/lz';

/** localStorage 中存储 JWT 令牌的 key */
const TOKEN_KEY = 'privshield_token';

/**
 * 获取存储的 JWT 令牌。
 */
export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

/**
 * 存储 JWT 令牌。
 */
export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

/**
 * 清除 JWT 令牌。
 */
export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY);
}

/**
 * 通用 JSON 请求函数 — 所有 API 调用的底层入口。
 *
 * 执行流程：
 *  1. 合并 Content-Type: application/json 到请求头（可通过 options.headers 覆盖）
 *  2. 发起 fetch 请求
 *  3. 检查 HTTP 状态码：非 2xx 时尝试解析响应体中的 error/detail 字段作为错误信息
 *  4. 2xx 时将响应体解析为泛型类型 T 并返回
 *
 * @param url     请求地址（相对路径，拼接在 BASE_URL 之后）
 * @param options 原生 RequestInit（method / body / headers 等）
 * @returns Promise<T> 解析后的 JSON 数据
 * @throws Error 当 HTTP 状态非 2xx 时抛出，消息优先取响应体中的 error/detail 字段
 */
async function fetchJSON<T>(url: string, options?: RequestInit): Promise<T> {
  // 自动附加 JWT 令牌（如果存在）
  const token = getToken();
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options?.headers as Record<string, string> || {}),
  };
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const res = await fetch(url, {
    ...options,
    headers,
  });
  if (!res.ok) {
    // 尝试从响应体提取人类可读的错误信息
    // 兼容统一信封格式 {code, message, detail, trace_id, timestamp} 与旧格式 {error/detail}
    let errMsg = `HTTP Error ${res.status}`;
    try {
      const errBody = await res.json();
      if (errBody.code) {
        // 统一信封格式：优先使用 message，附带 trace_id 便于排查
        errMsg = errBody.message || errBody.detail || errBody.code;
        if (errBody.trace_id) {
          console.error(`[PrivShield API Error] ${errBody.code} (TraceID: ${errBody.trace_id}): ${errMsg}`);
        }
        // Dispatch global error event for unified toast/notification handling
        // 派发全局错误事件，供统一 Toast/通知组件监听
        window.dispatchEvent(new CustomEvent('privshield:api-error', {
          detail: { code: errBody.code, message: errMsg, detail: errBody.detail, trace_id: errBody.trace_id },
        }));
      } else if (errBody.error || errBody.detail) {
        errMsg = errBody.error || errBody.detail;
      }
    } catch {
      // 响应体非 JSON 格式，使用默认 HTTP Error 消息
    }
    throw new Error(errMsg);
  }
  return res.json() as Promise<T>;
}

/**
 * 前端 API 客户端对象 — 聚合所有 BFF 接口调用方法。
 *
 * 使用方式：在 App.tsx 中通过 import { api } 引入，
 * 各 fetch 函数在 useCallback 中调用对应方法。
 */
export const api = {
  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  // 1. 拓扑 & 网格健康（对应 BFF handlers.go GetTopology）
  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  /**
   * 获取 4 个微服务的拓扑状态（REST 或 gRPC 视角）。
   * @param protocol 协议类型，默认 'rest'
   */
  async getTopology(protocol: 'rest' | 'grpc' = 'rest'): Promise<TopologyResponse> {
    return fetchJSON<TopologyResponse>(`${BASE_URL}/topology?protocol=${protocol}`);
  },

  /**
   * 主动触发全量拓扑探测（BFF 并发探测 4 服务后返回结果）。
   * @param protocol 协议类型，默认 'rest'
   */
  async probeAll(protocol: 'rest' | 'grpc' = 'rest'): Promise<TopologyResponse> {
    return fetchJSON<TopologyResponse>(`${BASE_URL}/probe/all?protocol=${protocol}`, { method: 'POST' });
  },

  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  // 2. 任务 & 租约（对应 BFF handlers.go 任务相关路由）
  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  /**
   * 分页查询任务列表（支持按状态过滤）。
   * @param status 状态过滤（空字符串 = 不过滤）
   * @param limit  每页条数，默认 50
   * @param offset 偏移量，默认 0
   */
  async listTasks(status = '', limit = 50, offset = 0): Promise<TasksResponse> {
    const params = new URLSearchParams();
    if (status) params.set('status', status);
    params.set('limit', String(limit));
    params.set('offset', String(offset));
    return fetchJSON<TasksResponse>(`${BASE_URL}/tasks?${params.toString()}`);
  },

  /** 根据 ID 获取单个任务详情。 */
  async getTask(id: string): Promise<Task> {
    return fetchJSON<Task>(`${BASE_URL}/tasks/${id}`);
  },

  /**
   * 获取 Phase B 租约信息（哪些 Worker 持有哪些任务的租约）。
   * BFF 内部基于 Service Hub 的 GET /v1/hub/tasks?status=running 推导租约信息。
   */
  async getLeases(): Promise<LeasedTasksResponse> {
    return fetchJSON<LeasedTasksResponse>(`${BASE_URL}/tasks/leases`);
  },

  /**
   * 向 Service Hub 派发新任务。
   * @param req 包含 source / operation / payload / priority
   */
  async dispatchTask(req: DispatchRequest): Promise<DispatchResponse> {
    return fetchJSON<DispatchResponse>(`${BASE_URL}/tasks/dispatch`, {
      method: 'POST',
      body: JSON.stringify(req),
    });
  },

  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  // 3. 测试套件运行器（对应 BFF runner.go 的 TS-01/02/03）
  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  /** 获取可用的 E2E 测试套件定义列表。 */
  async getSuites(): Promise<{ suites: TestSuiteCase[] }> {
    return fetchJSON<{ suites: TestSuiteCase[] }>(`${BASE_URL}/suites`);
  },

  /**
   * 执行指定的测试套件（可指定 suite_ids / concurrency / benchmark_requests）。
   * BFF 内部调用 runner.go 中的 RunSuites 方法。
   */
  async runSuites(req: RunTestSuiteRequest): Promise<RunTestSuiteResponse> {
    return fetchJSON<RunTestSuiteResponse>(`${BASE_URL}/suites/run`, {
      method: 'POST',
      body: JSON.stringify(req),
    });
  },

  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  // 4. 审计 & Merkle 验真（对应 BFF handlers.go 审计路由）
  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  /**
   * 分页获取审计日志条目。
   * @param limit  每页条数，默认 50
   * @param offset 偏移量，默认 0
   */
  async getAuditLogs(limit = 50, offset = 0): Promise<{ logs: AuditLogItem[] }> {
    return fetchJSON<{ logs: AuditLogItem[] }>(`${BASE_URL}/audit/logs?limit=${limit}&offset=${offset}`);
  },

  /**
   * 触发 Merkle 树完整性验证。
   * BFF 内部转发到 Audit Log 服务的 POST /v1/audit/snapshots/verify。
   * 返回 merkle_valid=true 表示所有日志条目的哈希链未被篡改。
   */
  async verifyAudit(): Promise<AuditVerifyResponse> {
    return fetchJSON<AuditVerifyResponse>(`${BASE_URL}/audit/verify`, { method: 'POST' });
  },

  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  // 5. Prometheus 指标（对应 BFF handlers.go GetMetrics/GetParsedMetrics）
  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  /**
   * 获取 Prometheus 原始文本格式的指标数据。
   * 注意：此接口不使用 fetchJSON（因为返回的是纯文本而非 JSON）。
   */
  async getMetrics(): Promise<string> {
    const res = await fetch(`${BASE_URL}/metrics`);
    return res.text();
  },

  /**
   * 获取 BFF 解析后的结构化指标（阶段耗时 / QPS / 百分位 / 总请求数）。
   * BFF 内部从 Prometheus 文本中提取并计算 P50/P90/P95/P99。
   */
  async getParsedMetrics(): Promise<{
    stage_durations: Record<string, number>;
    qps: number;
    percentiles: Record<string, number>;
    total_requests: number;
    source: string;
  }> {
    return fetchJSON(`${BASE_URL}/metrics/parsed`);
  },

  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  // 6. 预设数据 API（4 阶段会话：fetch → classify → audit → return）
  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  /** 获取预设数据 API 的定义列表（当前 4 个：医保/康养/预留×2）。 */
  async getDataApiDefinitions(): Promise<{ apis: DataApiDef[] }> {
    return fetchJSON<{ apis: DataApiDef[] }>(`${BASE_URL}/data-api/definitions`);
  },

  /**
   * 调用指定的预设数据 API，执行完整的 5 阶段会话。
   * @param apiId 数据 API 的 ID（1-4）
   * @param idCardNo 公民身份证号（18 位），按身份证号查询单条记录
   * @param lean 轻量模式（可选）
   * @returns 完整会话结果（含原始数据、脱敏后数据、各阶段耗时）
   */
  async invokeDataApi(apiId: number, idCardNo: string, lean = false): Promise<DataApiSessionResponse> {
    return fetchJSON<DataApiSessionResponse>(`${BASE_URL}/data-api/invoke`, {
      method: 'POST',
      body: JSON.stringify({ api_id: apiId, id_card_no: idCardNo, lean }),
    });
  },

  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  // 7. 用户认证（注册 / 登录 / 当前用户）
  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  /**
   * 用户注册。
   * @param req 用户名、密码、显示名称、角色
   */
  async register(req: RegisterRequest): Promise<AuthResponse> {
    return fetchJSON<AuthResponse>('/v1/auth/register', {
      method: 'POST',
      body: JSON.stringify(req),
    });
  },

  /**
   * 用户登录。
   * @param req 用户名、密码
   */
  async login(req: LoginRequest): Promise<AuthResponse> {
    return fetchJSON<AuthResponse>('/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify(req),
    });
  },

  /**
   * 获取当前登录用户信息。
   */
  async getMe(): Promise<User> {
    return fetchJSON<User>('/v1/auth/me');
  },
};
