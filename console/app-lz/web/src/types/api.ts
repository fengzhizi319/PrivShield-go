/**
 * 前端类型定义文件 — 与 Go BFF 的 models.go 结构体一一对应。
 *
 * 类型分组（按功能模块）：
 *  1. 拓扑 & 服务节点     → ProtocolType, ServiceNode, TopologyResponse
 *  2. 流水线状态          → PipelineStage, PipelineStatusResponse
 *  3. 任务派发           → DispatchRequest/Response, ClassifyDispatchRequest/Response
 *  4. 数据源触发          → TriggerDatasourceRequest/Response
 *  5. 任务 & 租约          → Task, TasksResponse, LeasedTaskSummary, WorkerLeaseInfo, LeasedTasksResponse
 *  6. E2E 测试套件        → TestSuiteAssertion, TestSuiteCase, RunTestSuiteRequest/Response
 *  7. 数据源切片          → Datasource, DatasourceSliceResponse
 *  8. 审计 & Merkle 验真 → AuditLogItem, AuditVerifyResponse
 * 9. 预设数据 API 会话  → DataApiDef, DataApiSessionStage, DataApiSessionResponse
 * 10. 用户认证与 RBAC    → User, LoginRequest, LoginResponse, RegisterRequest
 *
 * 注意：字段命名使用 snake_case，与 Go JSON tag 保持一致。
 */

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 1. 拓扑 & 服务节点
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

/** 协议类型：REST 或 gRPC，用于拓扑大屏的协议视角切换 */
export type ProtocolType = 'rest' | 'grpc';

/**
 * 单个微服务节点的状态信息。
 * 对应 Go BFF models.go 中的 ServiceNode 结构体。
 * BFF 通过并发探测（REST /health + gRPC TCP Dial）填充这些字段。
 */
export interface ServiceNode {
  /** 服务唯一标识，如 'service-hub' / 'engine' / 'datasource-mgr' / 'audit-log' */
  id: string;
  /** 服务显示名称，如 '调度中枢 (Service Hub)' */
  name: string;
  /** HTTP 服务地址，如 'http://127.0.0.1:8082' */
  http_url: string;
  /** gRPC 服务地址，如 '127.0.0.1:50052' */
  grpc_addr: string;
  /** 综合状态（取 REST 和 gRPC 中较差的状态） */
  status: 'ready' | 'unhealthy' | 'unreachable';
  /** 综合 RTT（取 REST 和 gRPC 中较大的值） */
  rtt_ms: number;
  /** REST 探测状态（可选，仅当启用 REST 探测时存在） */
  rest_status?: 'ready' | 'unhealthy' | 'unreachable';
  /** REST 探测 RTT（毫秒） */
  rest_rtt_ms?: number;
  /** gRPC 探测状态（可选，仅当启用 gRPC 探测时存在） */
  grpc_status?: 'ready' | 'unhealthy' | 'unreachable';
  /** gRPC 探测 RTT（毫秒） */
  grpc_rtt_ms?: number;
  /** 当前使用的协议视角 */
  protocol?: ProtocolType;
  /** 服务版本号，如 '1.8.0' */
  version: string;
  /** 额外详情信息（如健康检查返回的额外字段） */
  details?: Record<string, any>;
  /** 探测错误信息（仅当状态非 ready 时存在） */
  error?: string;
}

/**
 * 拓扑响应 — 包含所有 4 个微服务的状态快照。
 * 对应 BFF GET /api/lz/topology 的响应体。
 */
export interface TopologyResponse {
  /** 集群整体状态：'healthy' / 'degraded' / 'unhealthy' */
  status: string;
  /** 当前探测使用的协议 */
  active_protocol?: ProtocolType;
  /** 拓扑快照时间戳（ISO 8601 格式） */
  timestamp: string;
  /** 4 个微服务节点的详细信息（固定顺序：Hub→Engine→Datasource→Audit） */
  services: ServiceNode[];
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 2. 流水线状态
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

/**
 * 流水线单个阶段的运行时状态。
 * 6 个阶段：ingest → fetch → classify → desensitize → return → audit
 */
export interface PipelineStage {
  /** 阶段标识符 */
  name: 'ingest' | 'fetch' | 'classify' | 'desensitize' | 'return' | 'audit';
  /** 阶段显示名称（中文） */
  title: string;
  /** 阶段当前状态 */
  status: 'idle' | 'processing' | 'error';
  /** 当前正在处理的活跃任务数 */
  active_count: number;
  /** 该阶段的平均处理耗时（毫秒） */
  avg_duration_ms: number;
}

/** 流水线整体状态响应（包含各阶段信息 + 各服务连接状态）。 */
export interface PipelineStatusResponse {
  stages: PipelineStage[];
  agent_connected: boolean;
  datasource_connected: boolean;
  audit_connected: boolean;
  qps: number;
  recent_tasks_count: number;
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 3. 任务派发
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

/** 任务派发请求（对应 BFF POST /api/lz/tasks/dispatch）。 */
export interface DispatchRequest {
  /** 数据来源标识，如 'ds_yibao' / 'ds_kangyang' */
  source: string;
  /** 操作类型，如 'mask' / 'classify_and_mask' / 'dp_count' */
  operation: string;
  /** 任务负载数据（任意 JSON 对象） */
  payload: Record<string, any>;
  /** 优先级（0-100，数值越大优先级越高） */
  priority: number;
}

/** 任务派发响应（包含新创建的 task_id）。 */
export interface DispatchResponse {
  task_id: string;
  status: string;
  /** 任务派发路径，如 'hub' / 'fallback' */
  via?: string;
  error?: string;
}

/** 分类分级派发请求（触发三层漏斗分类）。 */
export interface ClassifyDispatchRequest {
  source: string;
  payload: Record<string, any>;
  priority: number;
}

/** 分类分级派发响应（包含分类级别和操作建议）。 */
export interface ClassifyDispatchResponse {
  task_id: string;
  /** 分类级别，如 'L1' / 'L2' / 'L3' / 'L4' */
  level: string;
  /** 自动执行的操作，如 'mask' / 'dp' / 'review' */
  auto_operation: string;
  /** 分类结果详情（各层输出） */
  classify_result: Record<string, any>;
  via?: string;
  error?: string;
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 4. 数据源触发
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

/** 触发数据源拉取请求（从指定数据源获取数据并执行操作）。 */
export interface TriggerDatasourceRequest {
  /** 数据源 ID，如 'ds_yibao' */
  datasource_id: string;
  /** 拉取记录数上限 */
  limit: number;
  /** 操作类型 */
  operation: string;
}

/** 触发数据源拉取响应。 */
export interface TriggerDatasourceResponse {
  task_id: string;
  datasource_id: string;
  /** 实际拉取的记录数 */
  records_count: number;
  operation: string;
  status: string;
  via?: string;
  error?: string;
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 5. 任务 & 租约（Phase B PostgreSQL 原子租约）
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

/**
 * 单个任务的完整信息。
 * 任务生命周期：pending → running → completed/failed
 * Service Hub 通过 Worker 池执行任务，每个任务有租约机制防止崩溃后任务丢失。
 */
export interface Task {
  /** 任务唯一 ID，格式如 'task-1787554500-eabf3934' */
  id: string;
  /** 任务状态 */
  status: 'pending' | 'running' | 'completed' | 'failed';
  /** 当前执行阶段，如 'fetch' / 'classify' / 'desensitize' / 'audit' */
  stage: string;
  /** 数据来源标识 */
  source: string;
  /** 操作类型 */
  operation: string;
  /** 优先级（0-100） */
  priority: number;
  /** 任务创建时间（ISO 8601） */
  created_at: string;
  /** 任务开始执行时间 */
  started_at?: string;
  /** 任务完成时间 */
  completed_at?: string;
  /** 任务执行耗时（毫秒） */
  duration_ms: number;
  /** 错误信息（仅当 status='failed' 时存在） */
  error?: string;
  /** 任务负载的 JSON 字符串 */
  payload_json?: string;
  /** 任务结果的 JSON 字符串 */
  result_json?: string;
  /** 重试次数（失败后自动重试） */
  retry_count: number;
  /** 持有该任务租约的 Worker ID */
  lease_owner?: string;
  /** 租约过期时间（超过此时间未完成任务将被回收） */
  lease_expires_at?: string;
  /** 任务执行路径 */
  via?: string;
}

/** 任务列表响应（分页）。 */
export interface TasksResponse {
  total: number;
  tasks: Task[];
  via?: string;
}

/** 单个租约任务的摘要信息（用于租约大屏展示）。 */
export interface LeasedTaskSummary {
  task_id: string;
  stage: string;
  priority: number;
  /** 租约剩余有效时间（秒），到期后任务将被回收 */
  lease_expires_in_seconds: number;
}

/** 单个 Worker 的租约信息（包含该 Worker 持有的所有任务）。 */
export interface WorkerLeaseInfo {
  worker_id: string;
  /** 该 Worker 当前持有的任务数 */
  claimed_tasks_count: number;
  /** 持有的任务摘要列表 */
  tasks: LeasedTaskSummary[];
}

/**
 * 租约总览响应。
 * 对应 BFF GET /api/lz/tasks/leases（内部转发到 Service Hub）。
 */
export interface LeasedTasksResponse {
  /** 租约存储后端：'postgres'（Phase B）或 'memory'/'sqlite'（开发模式） */
  store_backend: string;
  /** 当前所有活跃租约的任务总数 */
  total_leased_tasks: number;
  /** 各 Worker 的租约信息列表 */
  workers: WorkerLeaseInfo[];
  /** 孤儿任务恢复信息（崩溃 Worker 的任务被回收的统计） */
  orphan_recovery: Record<string, any>;
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 6. E2E 测试套件（对应 BFF runner.go 的 TS-01/02/03）
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

/** 测试套件中的单个断言（期望值 vs 实际值）。 */
export interface TestSuiteAssertion {
  /** 断言名称，如 'Merkle root hash matches' */
  name: string;
  /** 期望值描述 */
  expected: string;
  /** 实际值描述 */
  actual: string;
  /** 是否通过 */
  passed: boolean;
}

/**
 * 单个测试用例的完整信息。
 * 对应 runner.go 中的 TestSuiteCase 结构体。
 */
export interface TestSuiteCase {
  /** 用例 ID，如 'TS-01' / 'TS-02' / 'TS-03' */
  id: string;
  /** 用例标题 */
  title: string;
  /** 用例描述 */
  description: string;
  /** 用例分类，如 'security' / 'performance' / 'reliability' */
  category: string;
  /** 用例执行状态 */
  status: 'pending' | 'running' | 'passed' | 'failed' | 'skipped';
  /** 执行耗时（毫秒） */
  duration_ms: number;
  /** 错误信息（仅当 status='failed' 时存在） */
  error?: string;
  /** 断言结果列表 */
  assertions: TestSuiteAssertion[];
  /** 执行日志行列表 */
  logs: string[];
}

/** 测试套件执行请求（可指定要执行的套件、并发数、压测请求数）。 */
export interface RunTestSuiteRequest {
  /** 要执行的套件 ID 列表（空或不传则执行全部） */
  suite_ids?: string[];
  /** TS-02 压测并发数，默认 50 */
  concurrency?: number;
  /** TS-02 压测总请求数，默认 200 */
  benchmark_requests?: number;
}

/** 测试套件执行响应（包含所有用例的执行结果汇总）。 */
export interface RunTestSuiteResponse {
  /** 本次执行的唯一 ID */
  run_id: string;
  status: string;
  total_cases: number;
  passed_cases: number;
  failed_cases: number;
  started_at: string;
  completed_at?: string;
  /** 各用例的详细执行结果 */
  results: TestSuiteCase[];
  /** 汇总统计（如 TS-02 的 P50/P99 延迟） */
  summary?: Record<string, any>;
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 7. 数据源切片
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

/** 数据源基本信息（用于数据源浏览器展示）。 */
export interface Datasource {
  id: string;
  name: string;
  /** 数据源分类，如 'medical' / 'healthcare' */
  category: string;
  /** 数据源中的总记录数 */
  records_count: number;
  /** 字段名列表 */
  fields?: string[];
}

/** 数据源切片响应（分页获取数据源中的记录）。 */
export interface DatasourceSliceResponse {
  datasource_id: string;
  /** 本次返回的记录数 */
  count: number;
  /** 数据源总记录数 */
  total: number;
  /** 记录数据列表 */
  records: Record<string, any>[];
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 8. 审计 & Merkle 验真
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

/**
 * 单条审计日志条目。
 * 对应 Audit Log 服务写入的不可篡改日志（SHA-256 哈希链 + Merkle 树）。
 */
export interface AuditLogItem {
  id: string;
  /** 日志时间戳（ISO 8601） */
  timestamp: string;
  /** 关联的任务 ID */
  task_id?: string;
  /** 规范 API 编码，如 'api1_yibao' */
  api_code?: string;
  /** 规范数据源 ID，如 'ds_yibao' */
  datasource_id?: string;
  /** 数据来源 */
  source: string;
  /** 执行的操作 */
  operation: string;
  /** 数据内容的 SHA-256 哈希（用于完整性验证） */
  data_hash: string;
  /** 操作者标识 */
  operator: string;
  /** 加密方式，如 'SM4-GCM' */
  encryption: string;
  /** 操作结果，如 'success' / 'failed' */
  result: string;
}

/**
 * Merkle 树完整性验证响应。
 * 对应 BFF POST /api/lz/audit/verify（内部转发到 Audit Log 服务）。
 * merkle_valid=true 表示所有日志条目的哈希链未被篡改。
 */
export interface AuditVerifyResponse {
  /** Merkle 树验证是否通过（true = 未被篡改） */
  merkle_valid: boolean;
  /** Merkle 树根哈希值（十六进制字符串） */
  root_hash: string;
  /** 参与验证的日志总条目数 */
  total_entries: number;
  /** 验证时间戳 */
  timestamp: string;
  /** 数字签名（可选，用于防抵赖） */
  signature?: string;
  /** 验证错误信息（仅当验证失败时存在） */
  error?: string;
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 9. 预设数据 API 会话（4 阶段：fetch → classify → audit → return）
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

/**
 * 预设数据 API 的定义。
 * 当前有 4 个：医保结算 API / 康养体征 API / 预留×2。
 * 定义由 BFF 硬编码（presetDataApiDefinitions），也可从配置中加载。
 */
export interface DataApiDef {
  /** API 唯一 ID（1-4） */
  id: number;
  /** 规范 API 编码，如 'api1_yibao' */
  api_code?: string;
  /** 序号（1-4） */
  seq?: number;
  /** API 显示名称 */
  name: string;
  /** 关联的数据源 ID，如 'ds_yibao' */
  datasource_id: string;
  /** 数据文件名，如 'yibao.csv' */
  file_name?: string;
  /** 字段数 */
  field_count?: number;
  /** API 分类，如 'medical' / 'healthcare' / 'reserved' */
  category: string;
  /** API 描述 */
  description: string;
  /** API 返回的字段名列表 */
  fields: string[];
  /** API 状态：'active' = 可用，'reserved' = 预留待接入 */
  status: 'active' | 'reserved';
}

/**
 * 数据 API 会话中单个阶段的执行结果。
 * 5 个阶段：ingest（接入校验）→ fetch（数据抽取）→ classify_desensitize（评级与脱敏）→ return（装配返回）→ audit（不可篡改存证）
 */
export interface DataApiSessionStage {
  /** 阶段标识符 */
  name: string;
  /** 阶段显示名称（中文） */
  title: string;
  /** 阶段执行状态 */
  status: 'success' | 'error' | 'skipped';
  /** 阶段执行耗时（毫秒） */
  duration_ms: number;
  /** 本地计算耗时（毫秒，含 JSON 编解码） */
  compute_ms?: number;
  /** 上游 HTTP 通信耗时（毫秒，含网络往返 + 序列化） */
  network_ms?: number;
  /** 阶段详情（如分类结果摘要、掩码字段数等） */
  detail?: string;
}

/**
 * 数据 API 会话的完整响应。
 * 包含原始数据、脱敏后数据、各阶段执行结果。
 * 对应 BFF handlers.go 中 InvokeDataApi 的返回值。
 */
export interface DataApiSessionResponse {
  /** 会话唯一 ID */
  session_id: string;
  /** 调用的 API ID */
  api_id: number;
  /** 调用的 API 名称 */
  api_name: string;
  /** 会话整体状态 */
  status: 'completed' | 'partial' | 'failed' | 'skipped';
  /** 原始记录（脱敏前） */
  raw_records: Record<string, any>[];
  /** 脱敏后的记录 */
  sanitized_data: Record<string, any>[];
  /** 各阶段执行结果列表 */
  stages: DataApiSessionStage[];
  /** 审计日志条目 ID（写入 Audit Log 后返回） */
  audit_entry_id?: string;
  /** 会话总耗时（毫秒） */
  total_duration_ms: number;
  /** 错误信息（仅当 status='failed' 时存在） */
  error?: string;
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// 10. 用户认证与 RBAC
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

/** 用户角色类型 */
export type UserRole = 'user' | 'admin';

/**
 * 系统用户信息。
 * 对应 BFF auth/handlers.go 中的 UserInfo 结构体。
 */
export interface User {
  /** 用户名（唯一标识） */
  username: string;
  /** 显示名称 */
  display_name: string;
  /** 角色: 'user' | 'admin' */
  role: UserRole;
  /** 注册时间（ISO 8601） */
  created_at?: string;
}

/** 用户登录请求。 */
export interface LoginRequest {
  username: string;
  password: string;
}

/** 用户注册请求。 */
export interface RegisterRequest {
  username: string;
  password: string;
  display_name?: string;
  role: UserRole;
}

/** 认证响应（登录/注册共用）。 */
export interface AuthResponse {
  /** JWT 令牌 */
  token: string;
  /** 用户信息 */
  user: User;
}
