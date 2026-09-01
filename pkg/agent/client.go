// Package agent provides a shared HTTP client to the upstream PrivShield agent REST API.
// Package agent 封装到上游 PrivShield agent REST API 的共享 HTTP 客户端。
//
// ==============================================================================
// 【设计背景与核心价值】
// PrivShield 体系中各业务微服务（service-hub / datasource-mgr / audit-log / bff-go）
// 需要高频调用上游核心隐私治理引擎（engine-go / privshield-agent）的 REST 接口（如脱敏、定级、探活等）。
//
// 为避免各个微服务重复编写 HTTP 请求、重试、熔断与鉴权逻辑，本包提供了统一的高性能 Client：
//  1. 【多节点负载均衡】：支持配置集群节点列表，基于无锁 Round-Robin 算法在客户端实现流量均衡与容灾；
//  2. 【按节点维度的三态断路器】：每个上游节点独立维护 Closed -> Open -> Half-Open 状态机，
//     单节点故障只熔断该节点流量，其余健康节点继续承接请求，杜绝「一台宕机、全集群停摆」；
//  3. 【智能重试与节点故障转移】：对网络波动、读取超时及 5xx 服务端错误执行带随机抖动的指数退避重试
//     （Exponential Backoff with Jitter），并在重试轮次切换到其他健康节点；
//  4. 【4xx 智能防误熔断】：4xx 客户端业务错误直接透传，不计入服务端故障计数，防止恶意或格式错误请求击穿熔断器；
//  5. 【全链路追踪与幂等】：自动从 Context 提取并注入 X-Request-ID、X-Trace-ID 与 X-Idempotency-Key 头；
//  6. 【防 OOM 内存保护】：通过 io.LimitReader 严格限制响应体上限（64 MiB），杜绝异常超大报文导致内存溢出；
//  7. 【高性能连接池】：配置长连接 Keep-Alive 与连接池复用，支撑高并发微秒/毫秒级低延迟请求。
//  8. 【结构化错误分类（P2-7）】：对外暴露哨兵错误 ErrEndpointUnavailable / ErrCircuitOpen /
//     ErrTransport，并在错误产生点用 transportError 定型包装出站 I/O 故障；重试与否由
//     errors.Is / errors.As 判定，不再依赖「对错误文案做子串匹配」（文案变更或本地化
//     即会静默丧失重试能力的隐患已消除）。
// ==============================================================================

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fengzhizi319/PrivShield/pkg/circuitbreaker"
	pkgobs "github.com/fengzhizi319/PrivShield/pkg/observability"
)

// Client wraps HTTP calls to the upstream PrivShield agent REST API with multi-node load balancing.
// Client 封装对上游 PrivShield agent REST API 的 HTTP 调用，支持多节点客户端负载均衡与故障转移。
type Client struct {
	baseURLs   []string      // 上游集群基础地址列表（如 ["http://127.0.0.1:8079", "http://127.0.0.1:8080"]）
	apiKey     string        // 访问上游 agent 所需的 API 鉴权令牌（注入 Authorization: Bearer <key>）
	httpClient *http.Client  // 底层 HTTP 客户端实例（持有独立连接池与全局超时配置）
	logger     *slog.Logger  // 结构化日志输出组件
	rrIndex    atomic.Uint64 // 多节点轮询调度的原子序号计数器

	// Retry configuration / 重试配置
	maxRetries     int           // 可重试错误的最大重试次数（默认 3 次）
	retryBaseDelay time.Duration // 指数退避重试的基础时间间隔（默认 500ms）

	// Per-endpoint circuit breakers / 按上游节点维度独立维护的熔断器组
	// 单节点故障只熔断该节点流量，其余健康节点继续承接请求（故障隔离而非全局雪崩）。
	cbMu          sync.Mutex               // 保护熔断器状态变更的互斥锁
	breakers      map[string]*circuitbreaker.Breaker // 归一化节点地址 → 该节点独立的熔断器状态
	cbOrder       []string                 // 节点配置顺序，保证聚合状态与诊断输出稳定
	cbThreshold   int                      // 触发单节点熔断的连续失败阈值（默认 5 次）
	cbCooldown    time.Duration            // 熔断开启后的冷却等待时间（默认 30s，冷却后转为 Half-Open）
	stateObserver func(node, state string) // 熔断器状态发生流转时的外部回调钩子（用于上报 Prometheus 指标）
}

// idempotencyKeyType is the context key for propagating X-Idempotency-Key.
type idempotencyKeyType struct{}

var idempotencyKey idempotencyKeyType

// ContextWithIdempotencyKey returns a copy of ctx carrying the given idempotency key.
// Downstream calls automatically inject this value as X-Idempotency-Key header.
//
// ContextWithIdempotencyKey 将幂等键注入 context。Client 会自动将其注入 X-Idempotency-Key 请求头。
func ContextWithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKey, key)
}

// IdempotencyKeyFromContext extracts idempotency key from context if present.
func IdempotencyKeyFromContext(ctx context.Context) string {
	if ik, ok := ctx.Value(idempotencyKey).(string); ok {
		return ik
	}
	return ""
}

// ─────────────────────────────────────────────────────────────
// Error taxonomy / 错误分类体系（P2-7）
// ─────────────────────────────────────────────────────────────
//
// 本客户端所有可被上层据以决策的错误都以**哨兵错误**或**结构化错误类型**表达，
// 供调用方使用 errors.Is / errors.As 判定，取代「对 err.Error() 做英文子串匹配」
// 这一脆弱口径（错误文案改写或本地化即静默丧失重试与降级能力）。
var (
	// ErrEndpointUnavailable 表示集群中没有任何可承接流量的上游节点地址
	// （未配置节点，或重试时唯一节点被排除）。属于配置类故障，重试不可能改变结果。
	ErrEndpointUnavailable = errors.New("no agent endpoint available")

	// ErrCircuitOpen 表示目标节点的熔断器正处于 Open 冷却期，请求在触网前被快速拒绝。
	// 语义为「该节点暂时不可流」，因此不应在同一请求内继续消耗重试轮次。
	ErrCircuitOpen = errors.New("circuit breaker open (cooldown remaining)")

	// ErrTransport 表示一次真实发生的出站 I/O 故障（建连、TLS、响应体读取等），
	// 由 do() 在错误产生点通过 transportError 注入。网络闪断与超时归入此类。
	ErrTransport = errors.New("agent transport failure")
)

// transportReason 是对传输故障根因的有界分类枚举，仅用于结构化判定与日志归因，
// 刻意不参与 Error() 文案，以保持既有错误字符串口径逐字节不变。
type transportReason int

const (
	reasonUnknown           transportReason = iota // 未能结构化识别的出站故障（DNS/TLS/代理等）
	reasonTimeout                                  // 超时：context 截止、http.Client.Timeout、socket 超时
	reasonConnectionRefused                        // 连接被拒（对端端口未监听）
	reasonConnectionReset                          // 连接被重置
	reasonUnexpectedEOF                            // 响应体读取被截断或对端提前关闭
	reasonConnClosed                               // 连接已关闭（连接池中的陈旧连接）
)

// String 返回故障分类的可读标识（供日志与指标标签使用）。
func (r transportReason) String() string {
	switch r {
	case reasonTimeout:
		return "timeout"
	case reasonConnectionRefused:
		return "connection_refused"
	case reasonConnectionReset:
		return "connection_reset"
	case reasonUnexpectedEOF:
		return "unexpected_eof"
	case reasonConnClosed:
		return "conn_closed"
	case reasonUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// transportError 包装 http.Client.Do 与响应体读取返回的原始错误：
//
//   - Unwrap 透出根因，使 errors.Is(err, context.DeadlineExceeded)、
//     errors.Is(err, syscall.ECONNREFUSED) 等下游判定依然成立；
//   - Is(ErrTransport) 为真，把「这是一次出站 I/O 故障」固化为结构类别；
//   - Error() 完全委托给根因，保证错误字符串与改造前一致。
type transportError struct {
	cause  error           // 底层原始错误（*url.Error / *net.OpError 等）
	reason transportReason // 由 classifyTransport 结构化识别出的根因分类
}

func (e *transportError) Error() string { return e.cause.Error() }

// Unwrap 透出根因错误，维持 errors.Is / errors.As 的判定链。
func (e *transportError) Unwrap() error { return e.cause }

// Is 使本错误可被 errors.Is(err, ErrTransport) 命中。
func (e *transportError) Is(target error) bool { return target == ErrTransport }

// newTransportError 在错误产生点包装出站 I/O 故障，并同步完成根因结构化分类。
func newTransportError(cause error) *transportError {
	return &transportError{cause: cause, reason: classifyTransport(cause)}
}

// classifyTransport 纯粹依据类型（errors.Is / errors.As）识别传输故障根因，
// 不做任何错误文案匹配：一段写着 "connection refused" 的普通描述性错误不会被误判。
func classifyTransport(err error) transportReason {
	switch {
	case err == nil:
		return reasonUnknown
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return reasonTimeout
	case errors.Is(err, net.ErrClosed):
		return reasonConnClosed
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return reasonUnexpectedEOF
	case errors.Is(err, syscall.ECONNREFUSED):
		return reasonConnectionRefused
	case errors.Is(err, syscall.ECONNRESET):
		return reasonConnectionReset
	}
	// http.Client.Timeout、连接池 i/o timeout 等均以实现 net.Error 且 Timeout() == true 表达。
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return reasonTimeout
	}
	return reasonUnknown
}

// isRetryableError 以结构化方式判定一次故障是否值得换节点重试（P2-7：不再匹配错误文案）。
//
// 判定表（逐项对齐改造前的实际重试集合，既不扩大也不缩小）：
//   - 本客户端自身的快速失败判定 ErrCircuitOpen / ErrEndpointUnavailable → 不重试。
//     改造前这两类同样走 fail-fast 分支直接上抛，不产生任何额外出站调用；
//   - 出站 I/O 故障（ErrTransport，含超时 / 连接拒绝 / 连接重置 / EOF / 陈旧连接，
//     以及 DNS、TLS、代理等无法穷举但同样属于瞬时抖动的 reasonUnknown）→ 重试。
//     此处刻意对全部传输故障放行重试，因为改造前 http.Client.Do 返回任何错误都会 continue，
//     若按根因白名单逐个筛选将「缩小」重试集合，导致上游抖动直接打穿到业务侧；
//   - 未在本客户端产生、但根因可被结构化识别为瞬时故障的错误 → 重试
//     （供调用方直接持有底层错误时复用同一判定）；
//   - 其余（4xx 业务错误、JSON 解析失败、响应体超限、payload 序列化失败等
//     确定性、重试不可能改变结果的错误）→ 不重试。
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	// 客户端自身的快速失败判定优先排除：重试只会放大出站调用量。
	if errors.Is(err, ErrEndpointUnavailable) || errors.Is(err, ErrCircuitOpen) {
		return false
	}
	var terr *transportError
	if errors.As(err, &terr) {
		return true
	}
	switch classifyTransport(err) {
	case reasonTimeout, reasonConnectionRefused, reasonConnectionReset, reasonUnexpectedEOF, reasonConnClosed:
		return true
	default:
		return false
	}
}

// Config holds agent client configuration.
// Config 定义 Client 的构造参数配置项。
type Config struct {
	BaseURL        string                   // 上游 agent 单节点基础地址（如 "http://127.0.0.1:8079"）
	BaseURLs       []string                 // 上游 agent 多节点集群地址列表（设置时优先于 BaseURL，开启客户端负载均衡）
	APIKey         string                   // 可选的 Bearer Token 鉴权凭证
	Timeout        time.Duration            // HTTP 请求全局超时时间（默认 30s）
	CBThreshold    int                      // 触发熔断的连续失败次数阈值（默认 5 次）
	CBCooldown     time.Duration            // 熔断开启后的冷却重试等待时间（默认 30s）
	MaxRetries     int                      // 网络故障与 5xx 错误的最大重试次数（默认 3 次，0 表示不重试）
	RetryBaseDelay time.Duration            // 指数退避重试的基础时间（默认 500ms）
	Logger         *slog.Logger             // 结构化日志器（默认使用 slog.Default()）
	StateObserver  func(node, state string) // 熔断器状态变更时的观察者回调函数（入参为 node 与 state 字符串）
}

// New creates a new agent client from the given config.
//
// New 根据配置构建新的 agent 客户端实例。
// 执行逻辑：
// 1. 对未填写的参数设置企业级生产安全默认值（Timeout: 30s, CBThreshold: 5, CBCooldown: 30s, MaxRetries: 3, RetryBaseDelay: 500ms）；
// 2. 统一合并 BaseURL 与 BaseURLs 为切片集合；
// 3. 构建专属 http.Transport 连接池（MaxIdleConns: 100, MaxIdleConnsPerHost: 20, IdleConnTimeout: 90s, Keep-Alive 开启）；
// 4. 初始化熔断器状态为 circuitbreaker.StateClosed。
func New(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.CBThreshold == 0 {
		cfg.CBThreshold = 5
	}
	if cfg.CBCooldown == 0 {
		cfg.CBCooldown = 30 * time.Second
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.RetryBaseDelay == 0 {
		cfg.RetryBaseDelay = 500 * time.Millisecond
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	urls := make([]string, 0)
	if len(cfg.BaseURLs) > 0 {
		urls = append(urls, cfg.BaseURLs...)
	} else if cfg.BaseURL != "" {
		urls = append(urls, cfg.BaseURL)
	}

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
	}

	breakers := make(map[string]*circuitbreaker.Breaker, len(urls))
	order := make([]string, 0, len(urls))
	for _, u := range urls {
		ep := normalizeEndpoint(u)
		if _, dup := breakers[ep]; dup {
			continue
		}
		breakers[ep] = circuitbreaker.NewBreaker(cfg.CBThreshold, cfg.CBCooldown)
		order = append(order, ep)
	}

	return &Client{
		baseURLs: urls,
		apiKey:   cfg.APIKey,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   cfg.Timeout,
		},
		logger:         cfg.Logger,
		maxRetries:     cfg.MaxRetries,
		retryBaseDelay: cfg.RetryBaseDelay,
		breakers:       breakers,
		cbOrder:        order,
		cbThreshold:    cfg.CBThreshold,
		cbCooldown:     cfg.CBCooldown,
		stateObserver:  cfg.StateObserver,
	}
}

// normalizeEndpoint 归一化节点基础地址作为熔断器键（去除末尾斜杠与空白）。
func normalizeEndpoint(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

// BaseURL returns the first configured upstream agent base URL.
// BaseURL 返回配置的第一个上游 agent 基础地址。
func (c *Client) BaseURL() string {
	if len(c.baseURLs) == 0 {
		return ""
	}
	return c.baseURLs[0]
}

// BaseURLs returns all configured agent base URLs.
// BaseURLs 返回配置的所有上游 agent 基础地址列表。
func (c *Client) BaseURLs() []string {
	return c.baseURLs
}

// PickEndpoint returns the next healthy URL in the cluster using round-robin.
//
// PickEndpoint 基于 Round-Robin 轮询调度算法获取下一个可承接流量的上游节点地址。
// 执行逻辑：
// 1. 无地址时返回空串；单节点时直接返回该节点（熔断判定仍由 allowRequest 负责）；
// 2. 多节点时原子递增轮询序号，沿环形顺序找到第一个「自身熔断器未开启」的节点返回；
// 3. 全部节点均处于 Open 冷却期时返回熔断错误，由调用方快速失败。
func (c *Client) PickEndpoint() string {
	if len(c.baseURLs) == 0 {
		return ""
	}
	endpoint, err := c.pickEndpoint("")
	if err != nil {
		// 兼容旧签名：无可用节点时退回首个配置地址，由 allowRequest 给出精确错误。
		return c.baseURLs[0]
	}
	return endpoint
}

// pickEndpoint 轮询选取一个「允许发起请求」的节点，exclude 用于重试时避开刚失败的节点。
func (c *Client) pickEndpoint(exclude string) (string, error) {
	if len(c.cbOrder) == 0 {
		return "", ErrEndpointUnavailable
	}
	if len(c.cbOrder) == 1 {
		if c.cbOrder[0] == exclude {
			return "", ErrEndpointUnavailable
		}
		if err := c.allowRequest(c.cbOrder[0]); err != nil {
			return "", err
		}
		return c.cbOrder[0], nil
	}

	start := c.rrIndex.Add(1) - 1
	var lastErr error = ErrCircuitOpen
	for k := uint64(0); k < uint64(len(c.cbOrder)); k++ {
		ep := c.cbOrder[(start+k)%uint64(len(c.cbOrder))]
		if ep == exclude {
			continue
		}
		if err := c.allowRequest(ep); err != nil {
			lastErr = err
			continue
		}
		return ep, nil
	}
	return "", lastErr
}

// breakerFor 返回指定 endpoint 对应的熔断器状态，必要时惰性初始化。
func (c *Client) breakerFor(endpoint string) *circuitbreaker.Breaker {
	endpoint = normalizeEndpoint(endpoint)
	c.cbMu.Lock()
	defer c.cbMu.Unlock()
	b, ok := c.breakers[endpoint]
	if !ok {
		b = circuitbreaker.NewBreaker(c.cbThreshold, c.cbCooldown)
		c.breakers[endpoint] = b
	}
	return b
}

// allowRequest 判定指定节点的熔断器当前是否允许发起请求。
//
// 状态转移逻辑：
//  1. circuitbreaker.StateClosed：允许请求通过；
//  2. circuitbreaker.StateOpen：自该节点 cbOpenedAt 起已超过冷却时间则转入 circuitbreaker.StateHalfOpen 并放行探测请求，
//     否则立即返回 ErrCircuitOpen 哨兵，仅拦截发往该节点的流量；
//  3. circuitbreaker.StateHalfOpen：放行探测请求。
func (c *Client) allowRequest(endpoint string) error {
	b := c.breakerFor(endpoint)
	prev := b.State()
	if !b.Allow() {
		return ErrCircuitOpen
	}
	if prev == circuitbreaker.StateOpen && b.State() == circuitbreaker.StateHalfOpen {
		c.logger.Info("circuit breaker half-open, probing recovery", "endpoint", endpoint)
		c.reportCircuitState(endpoint, circuitbreaker.StateHalfOpen)
	}
	return nil
}

// Health checks the upstream agent health.
//
// Health 检查上游 agent 的健康状态，等价于调用 GET /health 端点。
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	return c.Get(ctx, "/health")
}

// Get performs a GET request to the agent and returns parsed JSON.
//
// Get 对上游 agent 发起 HTTP GET 请求并反序列化响应为 map[string]any。
//
// 使用方法：
// 常用于调用上游探活、元数据获取等只读端点。
//
// 执行逻辑：
// 1. 通过 pickEndpoint() 轮询选取一个「自身熔断器允许请求」的候选节点，全部节点熔断时快速失败；
// 2. 构建带 ctx 的 http.Request，注入鉴权头、X-Request-ID、X-Trace-ID 与 X-Idempotency-Key；
// 3. 调用 do(req, endpoint) 执行具有重试、故障转移与熔断统计的 HTTP 管道。
func (c *Client) Get(ctx context.Context, path string) (map[string]any, error) {
	endpoint, err := c.pickEndpoint("")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)
	if rid := pkgobs.RequestIDFromContext(ctx); rid != "" {
		req.Header.Set("X-Request-ID", rid)
	}
	return c.do(req, endpoint)
}

// Post performs a POST request to the agent and returns parsed JSON.
//
// Post 对上游 agent 发起 HTTP POST 请求并反序列化响应 JSON。
// 自动从 context 提取 X-Request-ID 注入下游链路。
func (c *Client) Post(ctx context.Context, path string, payload any) (map[string]any, error) {
	// Extract request ID from context for automatic distributed tracing correlation.
	// 从 context 提取请求 ID，实现自动分布式追踪关联。
	rid := pkgobs.RequestIDFromContext(ctx)
	return c.PostWithRequestID(ctx, path, payload, rid)
}

// PostWithRequestID performs a POST request, injecting X-Request-ID for tracing.
//
// PostWithRequestID 是带显式 requestID 的 POST 请求实现，Post 方法内部直接代理至本方法。
//
// 执行逻辑：
// 1. 通过 pickEndpoint() 选取一个允许请求的节点（全节点熔断时快速失败）；
// 2. 若 payload != nil，序列化为 JSON 并包装为 bytes.NewReader（支持重试时重新读取）；
// 3. 设置 Content-Type: application/json 及追踪头；
// 4. 交付 do(req, endpoint) 执行网络调用、故障转移与重试处理。
func (c *Client) PostWithRequestID(ctx context.Context, path string, payload any, requestID string) (map[string]any, error) {
	endpoint, err := c.pickEndpoint("")
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+path, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	return c.do(req, endpoint)
}

// setHeaders 集中为 HTTP 请求注入通用请求头。
func (c *Client) setHeaders(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if req.Header.Get("X-Request-ID") == "" {
		if rid := pkgobs.RequestIDFromContext(req.Context()); rid != "" {
			req.Header.Set("X-Request-ID", rid)
		}
	}
	// Inject X-Trace-ID for dual-header trace propagation (aligned with Go TraceMiddleware).
	// 注入 X-Trace-ID 实现双头追踪传播（与 Go TraceMiddleware 对齐）。
	if req.Header.Get("X-Trace-ID") == "" {
		if rid := pkgobs.RequestIDFromContext(req.Context()); rid != "" {
			req.Header.Set("X-Trace-ID", rid)
		}
	}
	if req.Header.Get("X-Idempotency-Key") == "" {
		if ik := IdempotencyKeyFromContext(req.Context()); ik != "" {
			req.Header.Set("X-Idempotency-Key", ik)
		}
	}
}

// do 是底层核心请求调度器，承载重试退避、节点故障转移、大包防护、状态统计与错误分流。
//
// 核心执行逻辑：
// 1. 开启重试循环（attempt 从 0 到 maxRetries）：
//   - attempt > 0 时：先由 retryEndpoint() 解析本轮目标节点——优先切换到其他允许请求的节点，
//     单节点集群或无替代节点时原地重试，集群整体熔断则立即中止循环并上抛熔断错误；
//   - 计算指数退避时延 delay = retryBaseDelay * 2^(attempt-1)，叠加随机抖动 jitter；
//   - 监听 ctx.Done()，支持外部主动取消超时；
//   - 通过 req.GetBody 重建请求流（处理 Body 已被首轮消费的问题）；
//
// 2. 发起底层 http.Do(req)：
//   - 若发生底层网络错误（如连接被拒、DNS解析失败、网络闪断）：以 transportError 定型包装
//     （errors.Is(err, ErrTransport) 成立，且可用 errors.Is 透出 context.DeadlineExceeded、
//     syscall.ECONNREFUSED 等根因），计入该节点失败并交由 isRetryableError 结构化判定是否重试；
//
// 3. 响应保护与读取：
//   - 使用 io.LimitReader 限制最大读取 64 MiB，超过直接报错并计入失败，防止 OOM；
//   - 若读取数据流过程发生 I/O 错误，同样包装为 transportError，计入失败并结构化判定重试；
//
// 4. 状态码分流判定：
//   - 状态码 >= 500（服务端故障）：计入该节点熔断失败计数，打印警告日志，换节点进入下一轮重试；
//   - 状态码 400~499（客户端业务/参数错误）：直接返回错误，【绝不重试】且【不计入熔断失败计数】；
//   - 状态码 200~299（成功）：清零该节点失败计数并按需恢复其半开熔断器，反序列化并返回 JSON 数据；
//
// 5. 若所有重试机会耗尽，返回包装了最后一次失败原因的聚合错误。
func (c *Client) do(req *http.Request, endpoint string) (map[string]any, error) {
	endpoint = normalizeEndpoint(endpoint)
	requestTarget := req.URL.RequestURI()
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			next, err := c.retryEndpoint(endpoint)
			if err != nil {
				// 无可承接节点：立即停止重试，把熔断错误原样上抛（fail-fast）。
				if lastErr != nil {
					return nil, fmt.Errorf("agent request failed after %d attempts: %w (failover blocked: %v)", attempt, lastErr, err)
				}
				return nil, err
			}
			if next != endpoint {
				c.logger.Info("failing over to another agent endpoint",
					"method", req.Method, "path", requestTarget,
					"from", endpoint, "to", next, "attempt", attempt+1)
				endpoint = next
				u, perr := url.Parse(endpoint + requestTarget)
				if perr != nil {
					return nil, fmt.Errorf("retry: rebuild request url: %w", perr)
				}
				req.URL = u
			}

			// Exponential backoff with jitter / 指数退避 + 随机抖动
			delay := c.retryBaseDelay * time.Duration(1<<(attempt-1))
			jitter := time.Duration(rand.Int64N(int64(delay / 2)))
			sleepDur := delay + jitter

			c.logger.Info("retrying agent request",
				"method", req.Method,
				"path", requestTarget,
				"endpoint", endpoint,
				"attempt", attempt+1,
				"max_retries", c.maxRetries+1,
				"backoff", sleepDur.String(),
			)

			select {
			case <-req.Context().Done():
				return nil, fmt.Errorf("retry cancelled: %w", req.Context().Err())
			case <-time.After(sleepDur):
			}

			// Re-create request body for retry (body was consumed on first attempt)
			if req.GetBody != nil {
				newBody, err := req.GetBody()
				if err != nil {
					return nil, fmt.Errorf("retry: recreate body: %w", err)
				}
				req.Body = newBody
			}
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// 在错误产生点定型为传输故障：errors.Is(err, ErrTransport) 成立，
			// 且根因（超时 / 连接拒绝 / EOF 等）仍可被 errors.Is 逐层透出。
			terr := newTransportError(err)
			c.recordFailure(endpoint)
			lastErr = fmt.Errorf("agent request failed: %w", terr)
			c.logger.Warn("agent request failed",
				"method", req.Method,
				"path", requestTarget,
				"endpoint", endpoint,
				"attempt", attempt+1,
				"reason", terr.reason.String(),
				"error", err.Error(),
			)
			// 重试策略统一由 isRetryableError 单点裁决（对传输故障恒为放行，
			// 与改造前「Do 报错即换节点重试」的集合完全一致）。
			if !isRetryableError(lastErr) {
				return nil, lastErr
			}
			continue // Retry on network errors / 网络错误可重试
		}
		defer resp.Body.Close()

		// P23 fix: limit response body to 64 MiB to prevent OOM from misbehaving upstream
		// 限制响应体最大 64 MiB，防止上游异常返回超大响应导致 OOM
		const maxBodySize = 64 << 20
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
		if err != nil {
			terr := newTransportError(err)
			c.recordFailure(endpoint)
			lastErr = fmt.Errorf("read agent response: %w", terr)
			if !isRetryableError(lastErr) {
				return nil, lastErr
			}
			continue // Retry on read errors / 读取错误可重试
		}
		if int64(len(body)) > maxBodySize {
			c.recordFailure(endpoint)
			return nil, fmt.Errorf("agent response too large: exceeds %d bytes", maxBodySize)
		}

		if resp.StatusCode >= 500 {
			c.recordFailure(endpoint)
			lastErr = fmt.Errorf("agent returned server error %d: %s", resp.StatusCode, string(body))
			c.logger.Warn("agent returned server error status",
				"method", req.Method,
				"path", requestTarget,
				"endpoint", endpoint,
				"status", resp.StatusCode,
				"attempt", attempt+1,
			)
			continue // Retry on 5xx（换节点重试）/ 服务端错误可重试
		} else if resp.StatusCode >= 400 {
			// 4xx 是客户端请求参数/业务错误，不计入服务端节点熔断失败计数，防止恶意/非法参数击穿熔断器
			c.logger.Debug("agent returned client error status",
				"method", req.Method,
				"path", requestTarget,
				"endpoint", endpoint,
				"status", resp.StatusCode,
			)
			return nil, fmt.Errorf("agent returned status %d: %s", resp.StatusCode, string(body))
		}

		c.recordSuccess(endpoint)

		var result map[string]any
		if len(body) > 0 {
			if err := json.Unmarshal(body, &result); err != nil {
				return nil, fmt.Errorf("parse agent response: %w", err)
			}
		}
		return result, nil
	}

	// All retries exhausted / 所有重试已耗尽
	return nil, fmt.Errorf("agent request failed after %d attempts: %w", c.maxRetries+1, lastErr)
}

// CircuitStateString returns the aggregate circuit state across all upstream endpoints.
//
// CircuitStateString 返回全量上游节点熔断器的聚合状态，语义为「客户端整体是否还能承接流量」：
// 仅当所有节点均处于 Open 时才返回 "open"；无 closed 节点但存在半开探测节点时返回 "half-open"；
// 其余情况返回 "closed"（单节点故障已被隔离，不影响整体可用性判定）。
func (c *Client) CircuitStateString() string {
	return c.state().String()
}

// state 返回聚合熔断状态（读锁安全）。
func (c *Client) state() circuitbreaker.State {
	c.cbMu.Lock()
	defer c.cbMu.Unlock()
	if len(c.breakers) == 0 {
		return circuitbreaker.StateClosed
	}
	allOpen, anyHalfOpen := true, false
	for _, b := range c.breakers {
		switch b.State() {
		case circuitbreaker.StateOpen:
		case circuitbreaker.StateHalfOpen:
			allOpen = false
			anyHalfOpen = true
		default:
			allOpen = false
		}
	}
	switch {
	case allOpen:
		return circuitbreaker.StateOpen
	case anyHalfOpen:
		return circuitbreaker.StateHalfOpen
	default:
		return circuitbreaker.StateClosed
	}
}

// EndpointStates returns the circuit state of every known upstream endpoint.
//
// EndpointStates 返回各上游节点独立的熔断状态快照（键为归一化节点地址），
// 供 /ops/diagnostics 与告警定位「究竟是哪台节点被熔断」，而非只看聚合态。
func (c *Client) EndpointStates() map[string]string {
	c.cbMu.Lock()
	defer c.cbMu.Unlock()

	states := make(map[string]string, len(c.breakers))
	for ep, b := range c.breakers {
		states[ep] = b.State().String()
	}
	return states
}

// ─────────────────────────────────────────────────────────────
// Circuit Breaker / 熔断器状态机
// ─────────────────────────────────────────────────────────────

// retryEndpoint 在重试轮次解析目标节点：优先故障转移到其他允许请求的节点，
// 无替代节点（含单节点集群）时仅在自身熔断器仍放行的前提下原地重试。
func (c *Client) retryEndpoint(current string) (string, error) {
	if len(c.cbOrder) > 1 {
		if ep, err := c.pickEndpoint(current); err == nil {
			return ep, nil
		}
	}
	return current, c.allowRequest(current)
}

// reportCircuitState 向外部注册的观察者回调上报指定节点的熔断器状态。
func (c *Client) reportCircuitState(endpoint string, state circuitbreaker.State) {
	if c.stateObserver == nil {
		return
	}
	node := endpoint
	if node == "" {
		node = c.BaseURL()
		if node == "" {
			node = "agent"
		}
	}
	var stateStr string
	switch state {
	case circuitbreaker.StateClosed:
		stateStr = "closed"
	case circuitbreaker.StateOpen:
		stateStr = "open"
	case circuitbreaker.StateHalfOpen:
		stateStr = "half_open"
	default:
		stateStr = "unknown"
	}
	c.stateObserver(node, stateStr)
}

// recordSuccess 记录指定节点的一次成功调用。
func (c *Client) recordSuccess(endpoint string) {
	b := c.breakerFor(endpoint)
	prev := b.State()
	b.RecordSuccess()
	cur := b.State()
	if prev == circuitbreaker.StateHalfOpen && cur == circuitbreaker.StateClosed {
		c.logger.Info("circuit breaker closed (recovery successful)", "endpoint", endpoint)
	}
	c.reportCircuitState(endpoint, cur)
}

// recordFailure 记录指定节点的一次失败调用。
func (c *Client) recordFailure(endpoint string) {
	b := c.breakerFor(endpoint)
	prev := b.State()
	b.RecordFailure()
	cur := b.State()
	if prev == circuitbreaker.StateClosed && cur == circuitbreaker.StateOpen {
		c.logger.Warn("circuit breaker opened",
			"endpoint", endpoint,
		)
	} else if prev == circuitbreaker.StateHalfOpen && cur == circuitbreaker.StateOpen {
		c.logger.Warn("circuit breaker re-opened (probe failed)", "endpoint", endpoint)
	}
	c.reportCircuitState(endpoint, cur)
}
