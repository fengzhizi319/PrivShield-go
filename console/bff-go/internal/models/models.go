// Package models defines the shared JSON data structures between frontend and Go BFF.
// Package models 定义前端与 Go BFF 之间共享的 JSON 数据结构。
//
// Design principles / 设计原则：
//   - All struct JSON tags are aligned with the upstream agent REST/JSON contract
//     所有结构体的 JSON 标签与上游 agent 的 REST/JSON 契约保持一致
//   - Frontend communicates with the single Go BFF using the same JSON contract for both REST and gRPC upstream protocols
//     前端通过同一套 JSON 契约与统一的 Go BFF 交互，无论上游走 REST 还是 gRPC
//   - Structs are only for JSON serialization/deserialization, containing no business logic
//     结构体仅用于 JSON 序列化/反序列化，不包含业务逻辑
//
// Struct list / 结构体清单：
//   - EndpointSample: endpoint sample data (sidebar navigation display)
//     端点示例数据（侧边导航展示）
//   - ProxyRequest / ProxyResponse: single request proxy forwarding
//     单请求代理转发
//   - ConsoleHealth: health check response
//     健康检查响应
//   - SamplesResponse: sample list wrapper
//     示例列表包装
//   - BatchRequest / BatchResultItem / BatchResponse: batch testing
//     批量测试
//   - UploadData: file upload processing result
//     文件上传处理结果
//   - LbBackend / LbTestRequest / LbDistItem / LbTestResponse: load-balancing test
//     负载均衡测试
package models

import (
	// encoding/json：提供 json.RawMessage 类型，用于延迟解析原始 JSON 请求体
	"encoding/json"
)

// EndpointSample 描述一个可测试的端点及其示例 payload。
//
// 前端在启动时通过 /v1/samples 获取全部端点示例，
// 展示在侧边导航中，用户点击后即可填充请求编辑器。
//
// backend 字段用于前端过滤当前上游协议支持的端点：
//   - "rest"：仅 REST 上游协议支持
//   - "grpc"：仅 gRPC 上游协议支持
//   - "both"：两者均支持
type EndpointSample struct {
	// Method：HTTP 方法（如 "POST"），用于请求编辑器预填充
	Method string `json:"method"`
	// Path：端点路径（如 "/v1/privacy/mask"），用于 mapper 路由匹配
	Path string `json:"path"`
	// Label：端点显示名称（如 "Mask Field"），用于侧边导航展示
	Label string `json:"label"`
	// Category：端点分类（如 "masking"、"dp"），用于侧边导航分组
	Category string `json:"category"`
	// Description：端点功能描述，用于端点信息栏展示
	Description string `json:"description"`
	// Body：示例请求体的原始 JSON，使用 json.RawMessage 延迟解析以保持原始格式
	Body json.RawMessage `json:"body,omitempty"`
	// ContentType：请求体内容类型（如 "application/json"），用于文件上传场景
	ContentType string `json:"contentType,omitempty"`
	// RawPayloadB64：Base64 编码的原始 payload，用于二进制数据等非 JSON 场景
	RawPayloadB64 string `json:"rawPayloadB64,omitempty"`
	// Backend：后端支持类型（"rest"/"grpc"/"both"），用于前端过滤
	Backend string `json:"backend"`
}

// ProxyRequest 是前端发送到 POST /v1/proxy 的 JSON 请求体。
//
// 前端将所有 gRPC 支持的操作统一通过 /v1/proxy 转发，
// 由 mapper 根据 Path 字段查找对应的 gRPC 方法并调用。
type ProxyRequest struct {
	// Method：原始 HTTP 方法（如 "POST"），实际由 mapper 根据 Path 决定 gRPC 语义
	Method string `json:"method"`
	// Path：目标端点路径（如 "/v1/privacy/mask"），mapper 据此分发到对应 gRPC 方法
	Path string `json:"path"`
	// Body：请求体的原始 JSON，使用 json.RawMessage 延迟解析，由 mapper 内部处理
	Body json.RawMessage `json:"body,omitempty"`
	// RawPayloadB64：Base64 编码的原始 payload，用于二进制数据等非 JSON 场景
	RawPayloadB64 string `json:"raw_payload_b64,omitempty"`
	// ContentType：请求体内容类型，用于文件上传等特殊场景
	ContentType string `json:"content_type,omitempty"`
}

// ProxyResponse 是 /v1/proxy 返回的统一 JSON 包装响应。
//
// 所有 gRPC 调用的结果都统一包装为该格式返回前端。
type ProxyResponse struct {
	// Status：HTTP 状态码（如 200），用于前端判断请求是否成功
	Status int `json:"status"`
	// DurationMs：gRPC 调用耗时（毫秒），用于前端展示性能指标
	DurationMs int64 `json:"duration_ms"`
	// Data：gRPC 响应转换后的业务数据，类型为 any 以支持不同操作的不同返回结构
	Data any `json:"data"`
	// Via：后端标识（如 "go-grpc"），前端据此展示当前请求经由哪个后端处理
	Via string `json:"via"`
	// Protocol：协议标识（如 "gRPC"），前端据此展示与 agent 的通信协议
	Protocol string `json:"protocol"`
}

// ConsoleHealth 是 GET /health 返回的健康检查响应。
//
// 前端通过该接口判断后端连接状态，并展示状态灯：
//   - Backend="ok" 表示控制台后端自身正常
//   - Agent="ok" 或 "unreachable" 表示上游 agent 的连通性
//   - LatencyMs 展示 Health RPC 调用耗时
type ConsoleHealth struct {
	// Backend：控制台后端自身状态，始终为 "ok"（如果能返回响应说明后端正常）
	Backend string `json:"backend"`
	// Agent：上游 agent 状态，成功时为 map{"status":"ok","namespace":"..."}，失败时为 "unreachable"
	Agent any `json:"agent"`
	// AgentURL：上游 agent 的 gRPC 地址，便于调试确认连接目标
	AgentURL string `json:"agent_url"`
	// LatencyMs：Health RPC 调用耗时（毫秒），使用指针类型以支持 omitempty（失败时可能为 0）
	LatencyMs *int64 `json:"latency_ms,omitempty"`
	// Error：连接失败时的错误信息，成功时为空（omitempty）
	Error string `json:"error,omitempty"`
	// Via：后端标识（如 "go-grpc"）
	Via string `json:"via"`
	// Protocol：协议标识（如 "gRPC"）
	Protocol string `json:"protocol"`
}

// SamplesResponse 包装端点示例列表，作为 GET /v1/samples 的响应。
type SamplesResponse struct {
	// Samples：所有可测试端点的示例数据列表，前端据此渲染侧边导航
	Samples []EndpointSample `json:"samples"`
}

// BatchRequest 是前端发送到 POST /v1/batch 的 JSON 请求体。
//
// 前端“一键批量测试”提交一组请求，后端逐个转发并汇总结果。
// 单个请求失败不会中断整个批次。
type BatchRequest struct {
	// Requests：待转发的请求列表，每个元素为一个完整的 ProxyRequest
	Requests []ProxyRequest `json:"requests"`
}

// BatchResultItem 是批量测试中单个请求的执行结果。
//
// 每个请求独立记录成功/失败状态、耗时与结果数据，
// 前端据此展示逐条结果与通过率统计。
type BatchResultItem struct {
	// Method：HTTP 方法（如 "POST"），用于结果展示
	Method string `json:"method"`
	// Path：请求路径（如 "/v1/privacy/mask"），用于结果展示与跳转
	Path string `json:"path"`
	// Status：HTTP 状态码，200 表示成功，400/502 表示失败
	Status int `json:"status"`
	// DurationMs：该请求的 gRPC 调用耗时（毫秒）
	DurationMs int64 `json:"duration_ms"`
	// Data：成功时的响应数据，失败时为空（omitempty）
	Data any `json:"data,omitempty"`
	// Error：失败时的错误信息，成功时为空（omitempty）
	Error string `json:"error,omitempty"`
}

// BatchResponse 是 POST /v1/batch 返回的批量测试汇总结果。
//
// 前端据此展示通过率（Passed/Total）与逐条结果详情。
type BatchResponse struct {
	// Total：总请求数（等于 len(Results)）
	Total int `json:"total"`
	// Passed：成功请求数（Status=200）
	Passed int `json:"passed"`
	// Failed：失败请求数（等于 Total - Passed）
	Failed int `json:"failed"`
	// Results：逐条请求结果详情，包含成功/失败状态与耗时
	Results []BatchResultItem `json:"results"`
	// Via：后端标识（如 "go-grpc"）
	Via string `json:"via"`
	// Protocol：协议标识（如 "gRPC"）
	Protocol string `json:"protocol"`
}

// UploadData 是 /v1/upload 包装在 ProxyResponse.Data 中的文件处理结果。
//
// 与 Agent REST 响应格式保持一致：
//   - operation 为操作类型（mask_dataframe / k_anonymize / classify_table）
//   - rows_in / rows_out 为输入/输出记录数
//   - result 为具体处理结果（脱敏/K-匿名为记录数组，分类为结果对象）
type UploadData struct {
	// Operation：操作类型，如 "mask_dataframe"、"k_anonymize"、"classify_table"
	Operation string `json:"operation"`
	// RowsIn：输入记录数（上传文件解析后的行数）
	RowsIn int `json:"rows_in"`
	// RowsOut：输出记录数（处理后的行数，分类操作时等于 RowsIn）
	RowsOut int `json:"rows_out"`
	// Result：具体处理结果，类型取决于 Operation：
	//   - mask_dataframe / k_anonymize：[]map[string]string（记录数组）
	//   - classify_table：分类结果对象（包含 field_results、final_level 等）
	Result any `json:"result"`
}

// LbBackend 是负载均衡测试中的单个目标后端节点。
//
// 前端在负载均衡测试页面配置多个后端节点，
// 后端按策略向这些节点分发探测请求并统计结果。
type LbBackend struct {
	// Name：节点名称（如 "node-1"），用于结果展示
	Name string `json:"name"`
	// URL：节点 REST 地址（如 "http://127.0.0.1:8079"），探测请求的目标
	URL string `json:"url"`
}

// LbTestRequest 是 POST /v1/lb_test 的请求体。
//
// 控制台后端按 Strategy 策略把 NumRequests 个探测请求分发到 Backends 中的各节点：
//   - ProbePath：探测路径，默认 /health
//   - ProbeBody：提供时以 POST 发送该 JSON 体，否则用 GET
//
// 支持的策略：round_robin（轮询）、random（随机）、least_connections（最少连接）
type LbTestRequest struct {
	// Backends：目标后端节点列表，每个节点包含名称与 URL
	Backends []LbBackend `json:"backends"`
	// NumRequests：探测请求总数，按策略分发到各节点
	NumRequests int `json:"num_requests"`
	// Strategy：负载均衡策略名称（如 "round_robin"、"random"、"least_connections"）
	Strategy string `json:"strategy"`
	// ProbePath：探测路径（如 "/health"），默认 /health
	ProbePath string `json:"probe_path"`
	// ProbeBody：探测请求体（可选），提供时以 POST 发送，否则用 GET
	ProbeBody json.RawMessage `json:"probe_body,omitempty"`
}

// LbDistItem 是负载均衡测试中单个节点的统计结果。
//
// 记录该节点被命中的次数、成功/失败数以及延迟分布（毫秒），
// 前端据此展示各节点的性能对比图表。
type LbDistItem struct {
	// Name：节点名称，与 LbBackend.Name 对应
	Name string `json:"name"`
	// URL：节点地址，与 LbBackend.URL 对应
	URL string `json:"url"`
	// Count：该节点被命中的总次数
	Count int `json:"count"`
	// Success：成功请求数（HTTP 2xx）
	Success int `json:"success"`
	// Failed：失败请求数（非 2xx 或连接错误）
	Failed int `json:"failed"`
	// AvgLatencyMs：平均延迟（毫秒）
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	// MinLatencyMs：最小延迟（毫秒）
	MinLatencyMs float64 `json:"min_latency_ms"`
	// MaxLatencyMs：最大延迟（毫秒）
	MaxLatencyMs float64 `json:"max_latency_ms"`
}

// LbTestResponse 是 POST /v1/lb_test 返回的负载均衡测试汇总结果。
//
// Distribution 按 Backends 顺序给出各节点统计，
// 恒有 Total == Success + Failed。
type LbTestResponse struct {
	// Strategy：使用的负载均衡策略名称
	Strategy string `json:"strategy"`
	// Total：探测请求总数
	Total int `json:"total"`
	// Success：成功请求总数（所有节点成功数之和）
	Success int `json:"success"`
	// Failed：失败请求总数（所有节点失败数之和）
	Failed int `json:"failed"`
	// DurationMs：测试总耗时（毫秒）
	DurationMs float64 `json:"duration_ms"`
	// Distribution：各节点的统计结果列表，按 Backends 顺序排列
	Distribution []LbDistItem `json:"distribution"`
}

// ConcurrencyTestRequest 是 POST /v1/concurrency_test 的请求体。
type ConcurrencyTestRequest struct {
	Path          string          `json:"path"`
	Method        string          `json:"method"`
	Body          json.RawMessage `json:"body,omitempty"`
	Concurrency   int             `json:"concurrency"`
	TotalRequests int             `json:"total_requests"`
}

// ConcurrencyTestResponse 是 POST /v1/concurrency_test 的响应。
type ConcurrencyTestResponse struct {
	Total        int     `json:"total"`
	Success      int     `json:"success"`
	Failed       int     `json:"failed"`
	DurationMs   float64 `json:"duration_ms"`
	Qps          float64 `json:"qps"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	MinLatencyMs float64 `json:"min_latency_ms"`
	MaxLatencyMs float64 `json:"max_latency_ms"`
	P50LatencyMs float64 `json:"p50_latency_ms"`
	P90LatencyMs float64 `json:"p90_latency_ms"`
	P95LatencyMs float64 `json:"p95_latency_ms"`
	P99LatencyMs float64 `json:"p99_latency_ms"`
}
