// Package agent encapsulates the gRPC client connection to the PrivShield service.
// Package agent 封装到 PrivShield gRPC 服务的客户端连接。
//
// Responsibilities / 职责：
//   - Establish and manage the gRPC connection to the upstream agent
//     建立并管理到上游 agent 的 gRPC 连接
//   - Provide a type-safe PrivacyServiceClient for handlers to call all RPC methods
//     提供类型安全的 PrivacyServiceClient 供 handler 层调用所有 RPC 方法
//   - Automatically attach optional auth metadata (API Key Bearer Token) on each call
//     在每次调用时自动附加可选的认证元数据（API Key Bearer Token）
//
// Dependency chain / 依赖关系：
//
//	handlers → agent.Client → proto.PrivacyServiceClient → gRPC → PrivShield Agent
//
// All RPC timeouts are controlled via caller-provided context; this package hardcodes none.
// 所有 RPC 超时通过调用方传入的 context 控制，本包不硬编码超时。
package agent

import (
	// context：用于传递认证元数据到 gRPC 调用
	"context"
	// tls：构造 mTLS 客户端的 TLS 配置（证书加载与校验策略）
	"crypto/tls"
	// x509：构造受信任 CA 证书池，用于校验服务端证书链
	"crypto/x509"
	// fmt：用于格式化错误信息
	"fmt"
	// os：用于读取证书/私钥/CA 文件内容
	"os"
	// time：用于 keepalive 心跳间隔与超时配置
	"time"

	// grpc：gRPC 核心库，提供客户端连接与调用能力
	"google.golang.org/grpc"
	// credentials：基于 TLS 配置的传输凭证，用于加密与双向认证

	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"google.golang.org/grpc/credentials"
	// insecure：非安全传输凭证，用于本地开发环境（无 TLS）
	"google.golang.org/grpc/credentials/insecure"
	// keepalive：gRPC 连接保活配置，定期发送心跳检测连接健康状态
	"google.golang.org/grpc/keepalive"
	// metadata：用于在 gRPC 调用中附加自定义元数据（如 authorization header）
	"google.golang.org/grpc/metadata"

	// config：加载代理后端配置（agent 地址、API Key、TLS 等）
	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/config"
	// pb：由 proto/privacy.proto 生成的 gRPC 客户端代码，包含所有 RPC 方法定义
	pb "github.com/fengzhizi319/PrivShield-go/console/bff-go/proto"
)

// Client wraps the gRPC connection and the generated PrivacyService client.
// Client 封装 gRPC 连接与生成的 PrivacyService 客户端。
// All handlers call upstream agent RPC methods through this struct.
// 所有 handler 通过该结构体调用上游 agent 的任意 RPC 方法。
type Client struct {
	// conn：底层 gRPC 连接，程序退出时需调用 Close() 释放
	conn *grpc.ClientConn
	// client：由 proto 生成的类型安全客户端，提供 Mask/DPCount/DynClassify 等所有 RPC 方法
	client pb.PrivacyServiceClient
	// cfg：代理后端配置，主要用于获取 API Key 以附加认证元数据
	cfg *config.Config
}

// New creates a Client by establishing a gRPC connection to the upstream agent.
// New 根据配置创建 Client，建立到上游 agent 的 gRPC 连接。
//
// Execution flow / 执行流程：
//  1. Get agent gRPC target address (host:port) from config
//     从配置中获取 agent gRPC 目标地址（host:port）
//  2. Build transport credentials: insecure by default, mTLS when TLS enabled
//     根据配置构造传输凭证：默认非安全（insecure），启用 TLS 后为 mTLS
//  3. Set max receive message size to 64 MiB for large-table classification
//     设置最大接收消息大小为 64 MiB，支持大表分类等场景
//  4. Generate PrivacyServiceClient instance from the connection
//     基于连接生成 PrivacyServiceClient 实例
//
// Transport credentials are determined by buildTransportCredentials:
// 传输凭证由 buildTransportCredentials 根据配置决定：
//   - PRIVACY_AGENT_TLS_ENABLED=false (default): insecure, for local dev
//   - PRIVACY_AGENT_TLS_ENABLED=true: TLS/mTLS, verify server cert and present client cert
func New(cfg *config.Config) (*Client, error) {
	// 获取上游 agent 的 gRPC 监听地址，格式如 "127.0.0.1:50051"
	target := cfg.AgentAddress()

	// 根据配置构造传输凭证（非安全或 mTLS）
	creds, err := buildTransportCredentials(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build transport credentials for %s: %w", target, err)
	}

	// 创建 gRPC 客户端连接。
	// grpc.NewClient 采用懒连接模式，不会立即建立 TCP 连接，
	// 而是在首次 RPC 调用时才真正连接（延迟连接策略）。
	//
	// 重试策略参数从配置读取（#12），支持通过环境变量动态调整：
	// - PRIVACY_AGENT_RETRY_MAX_ATTEMPTS: 最大重试次数（默认 6）
	// - PRIVACY_AGENT_RETRY_INITIAL_BACKOFF: 初始退避秒数（默认 1）
	// - PRIVACY_AGENT_RETRY_MAX_BACKOFF: 最大退避秒数（默认 8）
	conn, err := grpc.NewClient(
		target,
		// 使用构造好的传输凭证：非安全或 TLS/mTLS
		grpc.WithTransportCredentials(creds),
		// 配置自动重试 + 等待就绪策略：覆盖 agent 崩溃/重启的完整故障窗口。
		// 重试参数从配置读取，支持运行时通过环境变量调整。
		grpc.WithDefaultServiceConfig(fmt.Sprintf(`{
  "methodConfig": [{
    "name": [{"service": "privacy.local.PrivacyService"}],
    "waitForReady": true,
    "retryPolicy": {
      "MaxAttempts": %d,
      "InitialBackoff": "%ds",
      "MaxBackoff": "%ds",
      "BackoffMultiplier": 2.0,
      "RetryableStatusCodes": ["UNAVAILABLE"]
    }
  }]
}`, cfg.AgentRetryMaxAttempts, cfg.AgentRetryInitialBackoff, cfg.AgentRetryMaxBackoff)),
		// 设置单次 RPC 调用最大接收与发送消息大小为 64 MiB（64 * 2^20 字节）。
		// 默认值为 4 MiB，base64 编码的图片或大表分类场景可能超出默认限制，
		// 导致服务端重置 HTTP/2 连接（表现为 connection reset by peer）。
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(64<<20),
			grpc.MaxCallSendMsgSize(64<<20),
		),
		// 配置连接保活策略：定期发送 HTTP/2 PING 帧检测连接健康状态。
		// 当 Python agent 因 VLM 推理 OOM 等原因崩溃后，keepalive 能在
		// 数秒内检测到连接断开并触发重连，避免后续请求持续失败。
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second, // 每 30 秒发送一次心跳（需 ≥ 服务端 min_ping_interval）
			Timeout:             10 * time.Second, // 心跳超时 10 秒后判定连接断开
			PermitWithoutStream: true,             // 无活跃 RPC 时也发送心跳
		}),
	)
	if err != nil {
		// 连接创建失败时返回包装后的错误，包含目标地址便于排查
		return nil, fmt.Errorf("failed to dial agent gRPC %s: %w", target, err)
	}

	// 立即主动发起子通道连接与 TCP 握手，消除首个请求冷启动 2 秒级别的连接等待开销
	conn.Connect()

	// 组装 Client 结构体并返回
	return &Client{
		conn:   conn,                             // 保存 gRPC 连接引用，供 Close() 使用
		client: pb.NewPrivacyServiceClient(conn), // 基于连接生成类型安全的 RPC 客户端
		cfg:    cfg,                              // 保存配置引用，供 WithAuth() 读取 API Key
	}, nil
}

// buildTransportCredentials constructs gRPC transport credentials based on config.
// buildTransportCredentials 根据配置构造 gRPC 传输凭证。
//
// Two modes / 两种模式：
//   - TLS disabled (default): returns insecure.NewCredentials(), no encryption/cert verification
//     TLS 未启用（默认）：返回非安全凭证，不加密、不校验证书
//   - TLS enabled: builds *tls.Config and returns credentials.NewTLS(...)
//     TLS 启用：构造 *tls.Config 并返回 credentials.NewTLS(...)
//     1. Load CA cert to build trusted root pool for server cert chain verification
//     加载 CA 证书构造受信任根证书池，用于校验服务端证书链
//     2. If client cert/key configured, load for mTLS mutual authentication
//     若配置了客户端证书/私钥，加载作为 mTLS 双向认证的客户端凭证
//     3. Optionally override ServerName and InsecureSkipVerify (test only)
//     可选覆盖 ServerName（证书主机名校验）与 InsecureSkipVerify（仅测试）
func buildTransportCredentials(cfg *config.Config) (credentials.TransportCredentials, error) {
	// 未启用 TLS 时直接返回非安全凭证，保持本地开发零配置可用
	if !cfg.AgentTLSEnabled {
		return insecure.NewCredentials(), nil
	}

	// 启用 TLS 时 CA 证书为必填：客户端必须能校验服务端身份
	if cfg.AgentTLSCAFile == "" {
		return nil, fmt.Errorf("PRIVACY_AGENT_TLS_CA_FILE is required when TLS is enabled")
	}

	// 读取 CA 证书 PEM 内容
	caPEM, err := os.ReadFile(cfg.AgentTLSCAFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file %s: %w", cfg.AgentTLSCAFile, err)
	}

	// 构造受信任根证书池并加入自定义 CA
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate from %s", cfg.AgentTLSCAFile)
	}

	// 组装客户端 TLS 配置
	tlsConfig := &tls.Config{
		// RootCAs：用于校验服务端证书是否由受信任 CA 签发
		RootCAs: certPool,
		// MinVersion：强制最低 TLS 1.2，避免降级到不安全的老版本协议
		MinVersion: tls.VersionTLS12,
	}

	// 可选：覆盖服务端证书校验时使用的主机名。
	// 场景：连接目标为 127.0.0.1 但证书 SAN 仅含 localhost。
	if cfg.AgentTLSServerName != "" {
		tlsConfig.ServerName = cfg.AgentTLSServerName
	}

	// 可选：跳过服务端证书校验（仅限测试环境，生产严禁）
	if cfg.AgentTLSInsecureSkipVerify {
		tlsConfig.InsecureSkipVerify = true
	}

	// 若配置了客户端证书与私钥，加载用于 mTLS 双向认证。
	// 两者必须同时提供，否则服务端要求客户端证书时握手将失败。
	if cfg.AgentTLSCertFile != "" && cfg.AgentTLSKeyFile != "" {
		clientCert, err := tls.LoadX509KeyPair(cfg.AgentTLSCertFile, cfg.AgentTLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client key pair (%s, %s): %w",
				cfg.AgentTLSCertFile, cfg.AgentTLSKeyFile, err)
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	} else if cfg.AgentTLSCertFile != "" || cfg.AgentTLSKeyFile != "" {
		// 只提供了证书或私钥之一属于配置错误，提前报错避免运行时握手失败难以排查
		return nil, fmt.Errorf("PRIVACY_AGENT_TLS_CERT_FILE and PRIVACY_AGENT_TLS_KEY_FILE must be provided together")
	}

	// 基于 TLS 配置构造 gRPC 传输凭证
	return credentials.NewTLS(tlsConfig), nil
}

// NewFromConnection creates a Client from an existing gRPC connection.
// NewFromConnection 基于已存在的 gRPC 连接创建 Client。
//
// Primarily for unit testing:
// 该构造器主要用于单元测试场景：
//   - Tests can pass a bufconn in-memory connection, no real TCP needed
//     测试可传入 grpc/test/bufconn 提供的内存连接，无需启动真实 TCP 服务
//   - Avoids test dependency on external agent process
//     避免测试依赖外部 agent 进程，实现完全隔离的单元测试
//
// Production code should use New() instead.
// 生产代码应使用 New() 而非本方法。
func NewFromConnection(conn *grpc.ClientConn) *Client {
	return &Client{
		conn:   conn,                             // 使用外部传入的已有连接（如 bufconn）
		client: pb.NewPrivacyServiceClient(conn), // 基于该连接生成 RPC 客户端
		cfg:    &config.Config{},                 // 使用空配置：测试场景下不需要认证
	}
}

// Close closes the underlying gRPC connection, releasing TCP and HTTP/2 stream resources.
// Close 关闭底层 gRPC 连接，释放 TCP 连接与 HTTP/2 流资源。
// Should be called via defer in main to prevent connection leaks on exit.
// 应在 main 函数中通过 defer 调用，确保程序退出时不泄漏连接。
func (c *Client) Close() error {
	return c.conn.Close()
}

// Raw returns the generated gRPC client for handlers to call any RPC method.
// Raw 返回生成的 gRPC 客户端实例，供 handler 层调用任意 RPC 方法。
//
// Handlers obtain the client via this method, then call directly:
// handler 通过该方法获取 client 后，可直接调用：
//   - client.Mask(ctx, &pb.MaskRequest{...})
//   - client.DPCount(ctx, &pb.DPRequest{...})
//   - client.DynClassify(ctx, &pb.DynClassificationRequest{...})
//   - etc. all RPC methods defined in proto
func (c *Client) Raw() pb.PrivacyServiceClient {
	return c.client
}

// WithTrace returns a context with distributed trace ID metadata attached.
// WithTrace 返回附带分布式追踪 ID 元数据的 context。
//
// Appends both "x-request-id" and "x-trace-id" to gRPC outgoing metadata,
// ensuring the upstream Python engine receives the trace context for
// end-to-end distributed tracing across the HTTP → gRPC boundary.
// 将 "x-request-id" 与 "x-trace-id" 双头追加到 gRPC outgoing metadata，
// 确保上游 Python 引擎收到追踪上下文，实现 HTTP → gRPC 跨协议全链路追踪。
//
// Usage / 用法：
//
//	ctx := client.WithTrace(ctx, traceID)
//	ctx = client.WithAuth(ctx)
//	resp, err := client.Raw().SomeRPC(ctx, req)
func (c *Client) WithTrace(ctx context.Context, traceID string) context.Context {
	if traceID == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "x-request-id", traceID, "x-trace-id", traceID)
}

// WithTraceFromContext attaches trace metadata from the incoming context.
// It is a convenience wrapper around WithTrace that reads the request ID
// placed by pkg/middleware.TraceMiddleware or pkg/agent.ContextWithRequestID.
func (c *Client) WithTraceFromContext(ctx context.Context) context.Context {
	return c.WithTrace(ctx, pkgobs.RequestIDFromContext(ctx))
}

// WithAuth returns a context with authentication metadata attached.
// WithAuth 返回附带认证元数据的 context。
//
// When PRIVACY_AGENT_API_KEY is non-empty, appends "authorization: Bearer <key>"
// to the gRPC outgoing metadata for upstream agent authentication.
// 当配置中 PRIVACY_AGENT_API_KEY 非空时，在 gRPC 调用的 outgoing metadata 中
// 附加 "authorization: Bearer <key>" 头，用于上游 agent 的身份认证。
//
// Logic / 执行逻辑：
//   - API Key empty → return original context unchanged
//     API Key 为空 → 直接返回原始 context，不附加任何元数据
//   - API Key non-empty → write "Bearer <key>" to metadata, return new context
//     API Key 非空 → 将 "Bearer <key>" 写入 metadata，返回新 context
//
// All RPC calls should uniformly call this method before invoking:
// 所有 RPC 调用前应统一调用该方法处理 context：
//
//	ctx := client.WithAuth(ctx)
//	resp, err := client.Raw().SomeRPC(ctx, req)
func (c *Client) WithAuth(ctx context.Context) context.Context {
	// 未配置 API Key 时直接透传 context，不添加认证头
	if c.cfg.AgentAPIKey == "" {
		return ctx
	}
	// 将 "authorization: Bearer <key>" 追加到 gRPC outgoing metadata。
	// metadata.AppendToOutgoingContext 会创建新 context 而不修改原 context，
	// 符合 Go context 不可变的设计原则。
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.cfg.AgentAPIKey)
}

// Health calls the upstream agent's Health RPC to check service availability.
// Health 调用上游 agent 的 Health RPC，检查 agent 服务是否可用。
//
// Returns HealthResponse containing:
// 返回 HealthResponse 包含：
//   - Status: service status string (e.g. "ok")
//   - Namespace: budget namespace name
//
// Used by /health endpoint; frontend uses it to verify backend connectivity.
// 该方法用于 /health 接口，前端通过它判断后端连接是否正常。
func (c *Client) Health(ctx context.Context) (*pb.HealthResponse, error) {
	// 先通过 WithAuth 附加认证元数据，再发起空请求的 Health RPC 调用
	return c.client.Health(c.WithAuth(ctx), &pb.HealthRequest{})
}
