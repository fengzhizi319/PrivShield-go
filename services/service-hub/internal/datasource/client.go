// Package datasource provides a client for communicating with the datasource-mgr module.
// Package datasource 提供与模拟数据源服务 (datasource-mgr) 通信的客户端，支持 HTTP REST 与 gRPC (mTLS) 双协议。
//
// 架构设计与高可用特性：
// 1. 双协议支持：提供基于 net/http 的 HTTPS REST 客户端与基于 grpc-go 的高性能 gRPC 客户端；
// 2. 线程安全与延迟连接：gRPC 连接采用 sync.RWMutex 读写锁进行并发安全保护与按需懒加载初始化；
// 3. 生产级 mTLS 支持：当启用 TLS 时，自动加载客户端证书/私钥与受信任 CA，建立端到端 TLS 1.3 加密通道；
// 4. 弹性容灾与自愈：
//   - 三态熔断器（Circuit Breaker: Closed → Open → HalfOpen），连续 5 次失败自动熔断，30s 冷却后半开探测；
//   - 指数退避重试（Exponential Backoff with Jitter），最多 3 次重试，防范瞬时网络抖动与 5xx 故障；
//   - 内存防溢出保护，限制单响应体最大 64 MiB；
//   - 分布式链路追踪透传，自动提取并注入 X-Request-ID。
package datasource

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/fengzhizi319/PrivShield-go/pkg/circuitbreaker"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	dspb "github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/proto"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
)

// Client handles HTTP/REST and gRPC communication with datasource-mgr.
// Client 结构体负责与 datasource-mgr 微服务进行双协议通信，管理 HTTP 传输层与 gRPC 连接生命周期。
type Client struct {
	cfg        *config.Config // 全局运行配置引用
	baseURL    string         // datasource-mgr HTTP REST 基础 URL（首选/兼容）
	baseURLs   []string       // datasource-mgr HTTP REST 多节点集群地址列表（用于负载均衡与故障转移）
	rrIndex    atomic.Uint64  // 轮询调度序号计数器
	grpcAddr   string         // datasource-mgr gRPC 监听网络地址（如 "127.0.0.1:50053"）
	httpClient *http.Client   // 配置了超时与可选 mTLS 的 HTTP 客户端
	logger     *slog.Logger

	// Retry & Circuit Breaker / 重试与熔断配置
	maxRetries     int
	retryBaseDelay time.Duration
	breaker        *circuitbreaker.Breaker            // 兼容性主熔断器引用
	cbMu           sync.Mutex                         // 保护 per-node 熔断器注册表的互斥锁
	breakers       map[string]*circuitbreaker.Breaker // 归一化节点地址 → 该节点独立的熔断器状态
	cbThreshold    int                                // 触发单节点熔断的连续失败阈值（默认 5 次）
	cbCooldown     time.Duration                      // 熔断开启后的冷却等待时间（默认 30s）

	mu         sync.RWMutex                        // 保护 gRPC 连接与客户端实例的读写互斥锁
	grpcConn   *grpc.ClientConn                    // gRPC 底层长连接实例
	grpcClient dspb.DataSourceManagerServiceClient // gRPC 生成桩客户端
}

// normalizeEndpoint 归一化节点基础地址（去除末尾斜杠与空白）。
func normalizeEndpoint(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

// New creates a new Client instance with optional HTTPS mTLS support and circuit breaker.
// New 构造函数根据传入的配置初始化数据源客户端。
func New(cfg *config.Config) *Client {
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	// 配置 HTTPS 客户端证书双向认证（mTLS）
	if cfg.TLSEnabled && cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS13,
		}
		if cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile); err == nil {
			tlsConfig.Certificates = []tls.Certificate{cert}
		}
		if cfg.TLSCAFile != "" {
			if caPEM, err := os.ReadFile(cfg.TLSCAFile); err == nil {
				caPool := x509.NewCertPool()
				if caPool.AppendCertsFromPEM(caPEM) {
					tlsConfig.RootCAs = caPool
				}
			}
		}
		httpClient.Transport = &http.Transport{
			TLSClientConfig:     tlsConfig,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
		}
	}

	// 收集并归一化全部数据源 REST 节点地址
	rawURLs := cfg.DatasourceBaseURLs()
	var baseURLs []string
	seen := make(map[string]struct{}, len(rawURLs))
	for _, u := range rawURLs {
		norm := normalizeEndpoint(u)
		if norm != "" {
			if _, ok := seen[norm]; !ok {
				seen[norm] = struct{}{}
				baseURLs = append(baseURLs, norm)
			}
		}
	}
	if len(baseURLs) == 0 {
		baseURLs = []string{normalizeEndpoint(cfg.DatasourceBaseURL())}
	}

	const cbThreshold = 5
	const cbCooldown = 30 * time.Second

	breakers := make(map[string]*circuitbreaker.Breaker, len(baseURLs))
	for _, ep := range baseURLs {
		breakers[ep] = circuitbreaker.New(circuitbreaker.Options{
			Threshold:   cbThreshold,
			Cooldown:    cbCooldown,
			HalfOpenMax: 3,
		})
	}

	return &Client{
		cfg:            cfg,
		baseURL:        baseURLs[0],
		baseURLs:       baseURLs,
		grpcAddr:       cfg.DatasourceGRPCAddress(),
		httpClient:     httpClient,
		logger:         slog.Default(),
		maxRetries:     3,
		retryBaseDelay: 500 * time.Millisecond,
		breaker:        breakers[baseURLs[0]],
		breakers:       breakers,
		cbThreshold:    cbThreshold,
		cbCooldown:     cbCooldown,
	}
}

// Close closes any active gRPC connection.
// Close 方法安全关闭当前持有的 gRPC 底层连接，释放网络句柄资源。
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.grpcConn != nil {
		err := c.grpcConn.Close()
		c.grpcConn = nil
		c.grpcClient = nil
		return err
	}
	return nil
}

// BaseURL returns the primary/first configured datasource base URL.
func (c *Client) BaseURL() string {
	if len(c.baseURLs) > 0 {
		return c.baseURLs[0]
	}
	return c.baseURL
}

// BaseURLs returns all configured datasource base URLs.
func (c *Client) BaseURLs() []string {
	return c.baseURLs
}

// breakerFor 返回指定 endpoint 对应的熔断器状态，必要时惰性初始化。
func (c *Client) breakerFor(endpoint string) *circuitbreaker.Breaker {
	endpoint = normalizeEndpoint(endpoint)
	c.cbMu.Lock()
	defer c.cbMu.Unlock()
	if endpoint == "" || endpoint == c.BaseURL() {
		if c.breaker != nil {
			if c.breakers != nil {
				c.breakers[c.BaseURL()] = c.breaker
			}
			return c.breaker
		}
		endpoint = c.BaseURL()
	}
	if c.breakers == nil {
		c.breakers = make(map[string]*circuitbreaker.Breaker)
	}
	b, ok := c.breakers[endpoint]
	if !ok {
		b = circuitbreaker.New(circuitbreaker.Options{
			Threshold:   c.cbThreshold,
			Cooldown:    c.cbCooldown,
			HalfOpenMax: 3,
		})
		c.breakers[endpoint] = b
	}
	return b
}

// allowRequest 判定指定节点的熔断器当前是否允许发起请求。
func (c *Client) allowRequest(endpoint string) error {
	b := c.breakerFor(endpoint)
	if !b.Allow() {
		return fmt.Errorf("datasource circuit breaker open for endpoint %s (cooling down)", endpoint)
	}
	return nil
}

// PickEndpoint returns the next healthy URL using round-robin.
func (c *Client) PickEndpoint() string {
	ep, err := c.pickEndpoint("")
	if err != nil {
		return c.BaseURL()
	}
	return ep
}

// pickEndpoint 轮询选取一个允许请求的节点，exclude 用于重试时避开刚失败的节点。
func (c *Client) pickEndpoint(exclude string) (string, error) {
	if len(c.baseURLs) == 0 {
		return "", fmt.Errorf("no datasource endpoint configured")
	}
	if len(c.baseURLs) == 1 {
		if c.baseURLs[0] == exclude {
			return "", fmt.Errorf("no alternative datasource endpoint available")
		}
		if err := c.allowRequest(c.baseURLs[0]); err != nil {
			return "", err
		}
		return c.baseURLs[0], nil
	}

	start := c.rrIndex.Add(1) - 1
	var lastErr error = fmt.Errorf("all datasource endpoints circuit breakers open")
	for k := uint64(0); k < uint64(len(c.baseURLs)); k++ {
		ep := c.baseURLs[(start+k)%uint64(len(c.baseURLs))]
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

// retryEndpoint 在重试轮次解析目标节点：优先故障转移到其他允许请求的节点。
func (c *Client) retryEndpoint(current string) (string, error) {
	if len(c.baseURLs) > 1 {
		if ep, err := c.pickEndpoint(current); err == nil {
			return ep, nil
		}
	}
	return current, c.allowRequest(current)
}

// CircuitStateString returns the aggregate circuit breaker status as a string.
func (c *Client) CircuitStateString() string {
	c.cbMu.Lock()
	defer c.cbMu.Unlock()
	if c.breaker != nil && c.breakers != nil && len(c.baseURLs) <= 1 {
		c.breakers[c.BaseURL()] = c.breaker
	}
	if len(c.breakers) == 0 {
		if c.breaker != nil {
			return c.breaker.StateString()
		}
		return "closed"
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
		return "open"
	case anyHalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

func (c *Client) checkCircuit() error {
	c.cbMu.Lock()
	defer c.cbMu.Unlock()
	if len(c.breakers) == 0 {
		if c.breaker != nil && !c.breaker.Allow() {
			return fmt.Errorf("datasource circuit breaker open (cooling down)")
		}
		return nil
	}
	for _, b := range c.breakers {
		if b.Allow() {
			return nil
		}
	}
	return fmt.Errorf("datasource circuit breaker open (cooling down)")
}

func (c *Client) recordSuccess(endpoint ...string) {
	ep := ""
	if len(endpoint) > 0 {
		ep = endpoint[0]
	}
	b := c.breakerFor(ep)
	prev := b.State()
	b.RecordSuccess()
	if prev == circuitbreaker.StateHalfOpen && b.State() == circuitbreaker.StateClosed {
		c.logger.Info("datasource client circuit breaker closed (recovered)", "endpoint", ep)
	}
	if c.breaker != nil && c.breaker != b {
		c.breaker.RecordSuccess()
	}
}

func (c *Client) recordFailure(endpoint ...string) {
	ep := ""
	if len(endpoint) > 0 {
		ep = endpoint[0]
	}
	b := c.breakerFor(ep)
	prev := b.State()
	b.RecordFailure()
	if prev == circuitbreaker.StateClosed && b.State() == circuitbreaker.StateOpen {
		c.logger.Warn("datasource client circuit breaker opened", "endpoint", ep, "breaker", b.StateString())
	} else if prev == circuitbreaker.StateHalfOpen {
		c.logger.Warn("datasource client circuit breaker re-opened (probe failed)", "endpoint", ep)
	}
	if c.breaker != nil && c.breaker != b {
		c.breaker.RecordFailure()
	}
}

// doHTTP executes an HTTP request with circuit breaker, retries, multi-node failover, and body limit.
// Injects trace headers (X-Request-ID / X-Trace-ID) and outbound API Key for
// zero-trust service-to-service authentication.
func (c *Client) doHTTP(req *http.Request) ([]byte, error) {
	initialEndpoint := normalizeEndpoint(req.URL.Scheme + "://" + req.URL.Host)
	if initialEndpoint == "://" || initialEndpoint == "" {
		initialEndpoint = c.PickEndpoint()
	}
	endpoint := initialEndpoint
	requestTarget := req.URL.RequestURI()

	if rid := pkgobs.RequestIDFromContext(req.Context()); rid != "" {
		if req.Header.Get("X-Request-ID") == "" {
			req.Header.Set("X-Request-ID", rid)
		}
		if req.Header.Get("X-Trace-ID") == "" {
			req.Header.Set("X-Trace-ID", rid)
		}
	}

	// Inject outbound API Key when calling datasource-mgr in production.
	if c.cfg.DatasourceAPIKey != "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.DatasourceAPIKey)
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			next, err := c.retryEndpoint(endpoint)
			if err != nil {
				// No available endpoint to failover
				if lastErr != nil {
					return nil, fmt.Errorf("datasource request failed after %d attempts: %w (failover blocked: %v)", attempt, lastErr, err)
				}
				return nil, err
			}
			if next != endpoint {
				c.logger.Info("failing over to another datasource endpoint",
					"method", req.Method, "path", requestTarget,
					"from", endpoint, "to", next, "attempt", attempt+1)
				endpoint = next
				u, perr := url.Parse(endpoint + requestTarget)
				if perr != nil {
					return nil, fmt.Errorf("retry: rebuild request url: %w", perr)
				}
				req.URL = u
			}

			delay := c.retryBaseDelay * time.Duration(1<<(attempt-1))
			jitter := time.Duration(rand.Int64N(int64(delay / 2)))
			sleepDur := delay + jitter

			c.logger.Info("retrying datasource HTTP request",
				"path", req.URL.Path,
				"endpoint", endpoint,
				"attempt", attempt+1,
				"backoff", sleepDur.String(),
			)

			select {
			case <-req.Context().Done():
				return nil, fmt.Errorf("datasource request cancelled: %w", req.Context().Err())
			case <-time.After(sleepDur):
			}

			if req.GetBody != nil {
				newBody, err := req.GetBody()
				if err != nil {
					return nil, fmt.Errorf("recreate request body: %w", err)
				}
				req.Body = newBody
			}
		} else {
			// Initial attempt: ensure endpoint circuit is checked
			if err := c.allowRequest(endpoint); err != nil {
				// If initial endpoint circuit is open, try pick another
				alt, perr := c.pickEndpoint("")
				if perr == nil && alt != endpoint {
					endpoint = alt
					u, parseErr := url.Parse(endpoint + requestTarget)
					if parseErr == nil {
						req.URL = u
					}
				} else {
					return nil, err
				}
			}
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.recordFailure(endpoint)
			lastErr = fmt.Errorf("datasource HTTP do: %w", err)
			continue
		}

		const maxBodySize = 64 << 20 // 64 MiB
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
		resp.Body.Close()
		if err != nil {
			c.recordFailure(endpoint)
			lastErr = fmt.Errorf("read datasource response: %w", err)
			continue
		}
		if int64(len(body)) > maxBodySize {
			c.recordFailure(endpoint)
			return nil, fmt.Errorf("datasource response too large: exceeds %d bytes", maxBodySize)
		}

		if resp.StatusCode >= 500 {
			c.recordFailure(endpoint)
			lastErr = fmt.Errorf("datasource server error %d: %s", resp.StatusCode, string(body))
			continue
		} else if resp.StatusCode >= 400 {
			// 4xx client errors don't trigger breaker
			return nil, fmt.Errorf("datasource request failed with status %d: %s", resp.StatusCode, string(body))
		}

		c.recordSuccess(endpoint)
		return body, nil
	}

	return nil, fmt.Errorf("datasource request failed after %d attempts: %w", c.maxRetries+1, lastErr)
}

// ─────────────────────────────────────────────────────────────
// HTTP REST Methods / HTTP REST 方法
// ─────────────────────────────────────────────────────────────

// Health checks datasource-mgr connectivity via HTTP REST.
// Health 通过 HTTP GET /health 探测 datasource-mgr 的健康状态。
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.PickEndpoint()+"/health", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	body, err := c.doHTTP(req)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// ListDataSources fetches the list of mock datasources via HTTP REST.
func (c *Client) ListDataSources(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.PickEndpoint()+"/v1/datasources", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	body, err := c.doHTTP(req)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// GetDataSource fetches a single datasource by ID via HTTP REST.
func (c *Client) GetDataSource(ctx context.Context, id string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.PickEndpoint()+"/v1/datasources/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	body, err := c.doHTTP(req)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// TestConnection tests datasource connectivity via HTTP REST.
func (c *Client) TestConnection(ctx context.Context, id string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.PickEndpoint()+"/v1/datasources/"+url.PathEscape(id)+"/test", bytes.NewReader(nil))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	body, err := c.doHTTP(req)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// ─────────────────────────────────────────────────────────────
// gRPC Methods / gRPC 通信方法
// ─────────────────────────────────────────────────────────────

func (c *Client) getGRPCClient(ctx context.Context) (dspb.DataSourceManagerServiceClient, error) {
	c.mu.RLock()
	if c.grpcClient != nil {
		client := c.grpcClient
		c.mu.RUnlock()
		return client, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.grpcClient != nil {
		return c.grpcClient, nil
	}

	var dialOpt grpc.DialOption
	if c.cfg != nil && c.cfg.TLSEnabled {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
		if c.cfg.TLSCAFile != "" {
			caPEM, err := os.ReadFile(c.cfg.TLSCAFile)
			if err != nil {
				return nil, fmt.Errorf("read ca file: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caPEM) {
				return nil, fmt.Errorf("append ca cert failed")
			}
			tlsConfig.RootCAs = pool
		}
		if c.cfg.TLSCertFile != "" && c.cfg.TLSKeyFile != "" {
			cert, err := tls.LoadX509KeyPair(c.cfg.TLSCertFile, c.cfg.TLSKeyFile)
			if err != nil {
				return nil, fmt.Errorf("load keypair: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}
		dialOpt = grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))
	} else {
		dialOpt = grpc.WithTransportCredentials(insecure.NewCredentials())
	}

	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()

	conn, err := grpc.DialContext(dialCtx, c.grpcAddr, dialOpt,
		grpc.WithBlock(),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("dial datasource-mgr gRPC at %s: %w", c.grpcAddr, err)
	}

	c.grpcConn = conn
	c.grpcClient = dspb.NewDataSourceManagerServiceClient(conn)
	return c.grpcClient, nil
}

// wrapGRPCContext injects X-Request-ID, X-Trace-ID and outbound API Key to gRPC metadata if present.
func (c *Client) wrapGRPCContext(ctx context.Context) context.Context {
	if rid := pkgobs.RequestIDFromContext(ctx); rid != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-request-id", rid, "x-trace-id", rid)
	}
	if c.cfg != nil && c.cfg.DatasourceAPIKey != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.cfg.DatasourceAPIKey)
	}
	return ctx
}

// isRetryableGRPCCode checks whether a gRPC error is transient and retryable.
func isRetryableGRPCCode(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return true // network/connection error
	}
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}

// HealthGRPC checks datasource-mgr connectivity via gRPC.
func (c *Client) HealthGRPC(ctx context.Context) (*dspb.HealthResponse, error) {
	if err := c.checkCircuit(); err != nil {
		return nil, err
	}
	client, err := c.getGRPCClient(ctx)
	if err != nil {
		c.recordFailure()
		return nil, err
	}
	outCtx := c.wrapGRPCContext(ctx)
	resp, err := client.Health(outCtx, &dspb.HealthRequest{})
	if err != nil {
		if isRetryableGRPCCode(err) {
			c.recordFailure()
		}
		return nil, err
	}
	c.recordSuccess()
	return resp, nil
}

// ListDataSourcesGRPC lists data sources via gRPC.
func (c *Client) ListDataSourcesGRPC(ctx context.Context) (*dspb.ListMockSourcesResponse, error) {
	if err := c.checkCircuit(); err != nil {
		return nil, err
	}
	client, err := c.getGRPCClient(ctx)
	if err != nil {
		c.recordFailure()
		return nil, err
	}
	outCtx := c.wrapGRPCContext(ctx)
	resp, err := client.ListDataSources(outCtx, &dspb.ListMockSourcesRequest{})
	if err != nil {
		if isRetryableGRPCCode(err) {
			c.recordFailure()
		}
		return nil, err
	}
	c.recordSuccess()
	return resp, nil
}

// GetDataSourceGRPC gets datasource details via gRPC.
func (c *Client) GetDataSourceGRPC(ctx context.Context, id string) (*dspb.DataSourceProto, error) {
	if err := c.checkCircuit(); err != nil {
		return nil, err
	}
	client, err := c.getGRPCClient(ctx)
	if err != nil {
		c.recordFailure()
		return nil, err
	}
	outCtx := c.wrapGRPCContext(ctx)
	resp, err := client.GetDataSource(outCtx, &dspb.GetDataSourceRequest{Id: id})
	if err != nil {
		if isRetryableGRPCCode(err) {
			c.recordFailure()
		}
		return nil, err
	}
	c.recordSuccess()
	return resp, nil
}

// FetchRecordByIDCard fetches a single record from datasource-mgr by ID card number via HTTP REST.
// FetchRecordByIDCard 通过 HTTP REST 按身份证号从指定数据源精确查询单条记录。
// 调用 datasource-mgr GET /v1/datasources/:id/record-by-id?id_card_no=xxx 端点。
func (c *Client) FetchRecordByIDCard(ctx context.Context, datasourceID, idCardNo string) (map[string]any, error) {
	path := fmt.Sprintf("/v1/datasources/%s/record-by-id?id_card_no=%s",
		url.PathEscape(datasourceID), url.QueryEscape(idCardNo))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.PickEndpoint()+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	body, err := c.doHTTP(req)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// FetchRecordByIDCardGRPC fetches a single record by ID card number via gRPC.
// FetchRecordByIDCardGRPC 通过 gRPC 按身份证号从指定数据源精确查询单条记录。
func (c *Client) FetchRecordByIDCardGRPC(ctx context.Context, datasourceID, idCardNo string) (*dspb.SingleRecordResponse, error) {
	if err := c.checkCircuit(); err != nil {
		return nil, err
	}
	client, err := c.getGRPCClient(ctx)
	if err != nil {
		c.recordFailure()
		return nil, err
	}
	outCtx := c.wrapGRPCContext(ctx)
	resp, err := client.GetRecordByIDCard(outCtx, &dspb.GetRecordByIDCardRequest{
		SourceId: datasourceID,
		IdCardNo: idCardNo,
	})
	if err != nil {
		if isRetryableGRPCCode(err) {
			c.recordFailure()
		}
		return nil, err
	}
	c.recordSuccess()
	return resp, nil
}

// TestConnectionGRPC tests connection via gRPC.
func (c *Client) TestConnectionGRPC(ctx context.Context, id string) (*dspb.TestConnectionResponse, error) {
	if err := c.checkCircuit(); err != nil {
		return nil, err
	}
	client, err := c.getGRPCClient(ctx)
	if err != nil {
		c.recordFailure()
		return nil, err
	}
	outCtx := c.wrapGRPCContext(ctx)
	resp, err := client.TestConnection(outCtx, &dspb.TestConnectionRequest{Id: id})
	if err != nil {
		if isRetryableGRPCCode(err) {
			c.recordFailure()
		}
		return nil, err
	}
	c.recordSuccess()
	return resp, nil
}
