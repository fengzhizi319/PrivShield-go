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
	baseURL    string         // datasource-mgr HTTP REST 基础 URL（如 "http://127.0.0.1:8083"）
	grpcAddr   string         // datasource-mgr gRPC 监听网络地址（如 "127.0.0.1:50053"）
	httpClient *http.Client   // 配置了超时与可选 mTLS 的 HTTP 客户端
	logger     *slog.Logger

	// Retry & Circuit Breaker / 重试与熔断配置
	maxRetries     int
	retryBaseDelay time.Duration
	breaker        *circuitbreaker.Breaker

	mu         sync.RWMutex                        // 保护 gRPC 连接与客户端实例的读写互斥锁
	grpcConn   *grpc.ClientConn                    // gRPC 底层长连接实例
	grpcClient dspb.DataSourceManagerServiceClient // gRPC 生成桩客户端
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

	return &Client{
		cfg:            cfg,
		baseURL:        strings.TrimRight(cfg.DatasourceBaseURL(), "/"),
		grpcAddr:       cfg.DatasourceGRPCAddress(),
		httpClient:     httpClient,
		logger:         slog.Default(),
		maxRetries:     3,
		retryBaseDelay: 500 * time.Millisecond,
		breaker:        circuitbreaker.NewBreaker(5, 30*time.Second),
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

// CircuitStateString returns the current circuit breaker status as a string.
func (c *Client) CircuitStateString() string {
	return c.breaker.StateString()
}

func (c *Client) checkCircuit() error {
	if c.breaker.Allow() {
		return nil
	}
	return fmt.Errorf("datasource circuit breaker open (cooling down)")
}

func (c *Client) recordSuccess() {
	prev := c.breaker.State()
	c.breaker.RecordSuccess()
	if prev == circuitbreaker.StateHalfOpen && c.breaker.State() == circuitbreaker.StateClosed {
		c.logger.Info("datasource client circuit breaker closed (recovered)")
	}
}

func (c *Client) recordFailure() {
	prev := c.breaker.State()
	c.breaker.RecordFailure()
	if prev == circuitbreaker.StateClosed && c.breaker.State() == circuitbreaker.StateOpen {
		c.logger.Warn("datasource client circuit breaker opened", "breaker", c.breaker.StateString())
	} else if prev == circuitbreaker.StateHalfOpen {
		c.logger.Warn("datasource client circuit breaker re-opened (probe failed)")
	}
}

// doHTTP executes an HTTP request with circuit breaker, retries, and body limit.
// Injects trace headers (X-Request-ID / X-Trace-ID) and outbound API Key for
// zero-trust service-to-service authentication.
func (c *Client) doHTTP(req *http.Request) ([]byte, error) {
	if err := c.checkCircuit(); err != nil {
		return nil, err
	}

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
			delay := c.retryBaseDelay * time.Duration(1<<(attempt-1))
			jitter := time.Duration(rand.Int64N(int64(delay / 2)))
			sleepDur := delay + jitter

			c.logger.Info("retrying datasource HTTP request",
				"path", req.URL.Path,
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
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.recordFailure()
			lastErr = fmt.Errorf("datasource HTTP do: %w", err)
			continue
		}

		const maxBodySize = 64 << 20 // 64 MiB
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
		resp.Body.Close()
		if err != nil {
			c.recordFailure()
			lastErr = fmt.Errorf("read datasource response: %w", err)
			continue
		}
		if int64(len(body)) > maxBodySize {
			c.recordFailure()
			return nil, fmt.Errorf("datasource response too large: exceeds %d bytes", maxBodySize)
		}

		if resp.StatusCode >= 500 {
			c.recordFailure()
			lastErr = fmt.Errorf("datasource server error %d: %s", resp.StatusCode, string(body))
			continue
		} else if resp.StatusCode >= 400 {
			// 4xx client errors don't trigger breaker
			return nil, fmt.Errorf("datasource request failed with status %d: %s", resp.StatusCode, string(body))
		}

		c.recordSuccess()
		return body, nil
	}

	return nil, fmt.Errorf("datasource request failed after %d attempts: %w", c.maxRetries+1, lastErr)
}

// ─────────────────────────────────────────────────────────────
// HTTP REST Methods / HTTP REST 方法
// ─────────────────────────────────────────────────────────────

// Health checks datasource-mgr connectivity via HTTP REST.
// Health 通过 HTTP GET /api/health 探测 datasource-mgr 的健康状态。
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/health", nil)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/datasources", nil)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/datasources/"+url.PathEscape(id), nil)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/datasources/"+url.PathEscape(id)+"/test", bytes.NewReader(nil))
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
// 调用 datasource-mgr GET /api/datasources/:id/record-by-id?id_card_no=xxx 端点。
func (c *Client) FetchRecordByIDCard(ctx context.Context, datasourceID, idCardNo string) (map[string]any, error) {
	path := fmt.Sprintf("/api/datasources/%s/record-by-id?id_card_no=%s",
		url.PathEscape(datasourceID), url.QueryEscape(idCardNo))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
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
