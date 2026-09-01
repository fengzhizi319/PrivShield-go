// Package gateway 提供 gRPC 透明流式代理。
//
// 基于 grpc.UnknownServiceHandler + 原始编解码器 (rawCodec) 实现
// 零编解码字节流透传，避免"先反序列化再序列化"的双重开销。
// 配合 P2C-EWMA 负载均衡与三态熔断器，实现 L7 per-RPC 精准调度。
//
// 参考设计文档 §9.4。
package gateway

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/fengzhizi319/PrivShield/engine-go/internal/observability"
	pgateway "github.com/fengzhizi319/PrivShield/pkg/gateway"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ──────────────────────────────────────────────
// 原始编解码器（透传 protobuf 字节）
// ──────────────────────────────────────────────

// rawCodec 实现 grpc.encoding.Codec 接口，
// 直接透传原始字节而不做 marshal/unmarshal。
type rawCodec struct{}

func (rawCodec) Marshal(v interface{}) ([]byte, error) {
	if b, ok := v.(*[]byte); ok {
		return *b, nil
	}
	return nil, fmt.Errorf("rawCodec: unsupported type %T", v)
}

func (rawCodec) Unmarshal(data []byte, v interface{}) error {
	if b, ok := v.(*[]byte); ok {
		*b = data
		return nil
	}
	return fmt.Errorf("rawCodec: unsupported type %T", v)
}

func (rawCodec) Name() string { return "raw" }

func (rawCodec) String() string { return "raw" }

// ──────────────────────────────────────────────
// gRPC 透明流代理服务器
// ──────────────────────────────────────────────

// GrpcProxyServer gRPC 透明流代理
type GrpcProxyServer struct {
	lb          *LoadBalancer
	connPool    map[string]*grpc.ClientConn
	connPoolMu  sync.RWMutex
	maxPoolSize int // 连接池最大连接数（防止后端地址动态变化时内存泄漏）
	ewmaAlpha   float64
	dialTimeout time.Duration
	metrics     *observability.GatewayMetrics // 可为 nil
}

// NewGrpcProxyServer 创建 gRPC 透明流代理
func NewGrpcProxyServer(lb *LoadBalancer, metrics *observability.GatewayMetrics) *GrpcProxyServer {
	return &GrpcProxyServer{
		lb:          lb,
		connPool:    make(map[string]*grpc.ClientConn),
		maxPoolSize: 256,
		ewmaAlpha:   0.2,
		dialTimeout: 5 * time.Second,
		metrics:     metrics,
	}
}

// getOrCreateConn 获取或创建到后端的 gRPC 连接（连接池 + 健康检查）
func (g *GrpcProxyServer) getOrCreateConn(addr string) (*grpc.ClientConn, error) {
	g.connPoolMu.RLock()
	conn, ok := g.connPool[addr]
	g.connPoolMu.RUnlock()
	if ok && isConnReady(conn) {
		return conn, nil
	}

	g.connPoolMu.Lock()
	defer g.connPoolMu.Unlock()

	// 双重检查
	conn, ok = g.connPool[addr]
	if ok && isConnReady(conn) {
		return conn, nil
	}

	// 关闭旧连接（如果有）
	if ok && conn != nil {
		_ = conn.Close()
		delete(g.connPool, addr)
	}

	// 连接池大小限制
	if len(g.connPool) >= g.maxPoolSize {
		return nil, fmt.Errorf("connection pool full (max %d)", g.maxPoolSize)
	}

	ctx, cancel := context.WithTimeout(context.Background(), g.dialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
	)
	if err != nil {
		return nil, fmt.Errorf("dial backend %s: %w", addr, err)
	}
	g.connPool[addr] = conn
	return conn, nil
}

// isConnReady 检查 gRPC 连接是否可复用。
// IDLE / READY / CONNECTING 均视为可用（连接池应复用，而非反复创建）；
// 仅 TRANSIENT_FAILURE / SHUTDOWN 视为不可用。
func isConnReady(conn *grpc.ClientConn) bool {
	s := conn.GetState().String()
	return s == "READY" || s == "IDLE" || s == "CONNECTING"
}

// TransparentStreamDirector 实现 grpc.StreamHandler，
// 作为 UnknownServiceHandler 回调处理所有未注册的 gRPC 方法。
//
// 流程：
// 1. 从 ServerStream 提取完整方法名
// 2. P2C-EWMA 选择最优后端节点
// 3. 建立到后端的双向流
// 4. 启动双向并发零拷贝字节流转发
// 5. 更新 EWMA 延迟指标与熔断器状态
func (g *GrpcProxyServer) TransparentStreamDirector(srv interface{}, serverStream grpc.ServerStream) error {
	fullMethod, ok := grpc.MethodFromServerStream(serverStream)
	if !ok {
		return status.Errorf(codes.Internal, "failed to get method name")
	}

	// 1. 选择后端节点
	node := g.lb.SelectNode()
	if node == nil {
		return status.Errorf(codes.Unavailable, "no backend agent available")
	}

	if !node.CB.Allow() {
		return status.Errorf(codes.Unavailable, "backend %s circuit breaker open", node.Address)
	}

	// 2. 获取后端连接
	conn, err := g.getOrCreateConn(node.Address)
	if err != nil {
		node.CB.RecordFailure()
		return status.Errorf(codes.Unavailable, "backend connect error: %v", err)
	}

	// 3. 在途计数 + EWMA 追踪
	node.IncrementInFlight()
	start := time.Now()
	defer func() {
		node.DecrementInFlight()
		latency := time.Since(start)
		node.UpdateEWMA(latency, g.ewmaAlpha)
		// 上报 Prometheus 指标
		if g.metrics != nil {
			g.metrics.SetBackendInFlight(node.Address, node.Address, float64(node.InFlight.Load()))
			g.metrics.SetBackendEWMALatency(node.Address, latency.Seconds())
		}
	}()

	// 4. 建立到后端的客户端流
	ctx := serverStream.Context()
	// 传递 metadata（trace ID 等）
	md, _ := metadata.FromIncomingContext(ctx)
	outCtx := metadata.NewOutgoingContext(ctx, md.Copy())

	clientStream, err := conn.NewStream(outCtx, &grpc.StreamDesc{
		ServerStreams: true,
		ClientStreams: true,
	}, fullMethod)
	if err != nil {
		node.CB.RecordFailure()
		return status.Errorf(codes.Unavailable, "backend stream error: %v", err)
	}

	// 5. 双向零拷贝流转发
	errChan := make(chan error, 2)
	// 使用 cancel 确保两个方向都能被中断
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	// 客户端 → 后端
	go func() {
		for {
			select {
			case <-streamCtx.Done():
				errChan <- nil
				return
			default:
			}
			var frame []byte
			if err := serverStream.RecvMsg(&frame); err != nil {
				if err == io.EOF {
					_ = clientStream.CloseSend()
					errChan <- nil
					return
				}
				errChan <- err
				return
			}
			if err := clientStream.SendMsg(&frame); err != nil {
				errChan <- err
				return
			}
		}
	}()

	// 后端 → 客户端
	go func() {
		for {
			select {
			case <-streamCtx.Done():
				errChan <- nil
				return
			default:
			}
			var frame []byte
			if err := clientStream.RecvMsg(&frame); err != nil {
				if err == io.EOF {
					errChan <- nil
					return
				}
				errChan <- err
				return
			}
			if err := serverStream.SendMsg(&frame); err != nil {
				errChan <- err
				return
			}
		}
	}()

	// 等待两个方向都结束（或第一个错误触发 cancel 后两个都退出）
	err = <-errChan
	streamCancel() // 通知另一个 goroutine 退出
	<-errChan      // 等待第二个 goroutine 退出，防止泄漏
	if err == nil {
		node.CB.RecordSuccess()
	} else {
		slog.Warn("grpc proxy stream error",
			"method", fullMethod,
			"backend", node.Address,
			"error", err.Error(),
		)
		node.CB.RecordFailure()
	}

	// 上报熔断器状态 + 转发计数
	if g.metrics != nil {
		g.metrics.SetCircuitBreakerState(node.Address, pgateway.CBStateString(node.CB.State()))
		if err == nil {
			g.metrics.RecordForwarded(node.Address, 200)
		} else {
			g.metrics.RecordForwarded(node.Address, 502)
		}
	}

	return err
}

// Close 关闭所有后端连接
func (g *GrpcProxyServer) Close() error {
	g.connPoolMu.Lock()
	defer g.connPoolMu.Unlock()

	for addr, conn := range g.connPool {
		if err := conn.Close(); err != nil {
			slog.Warn("close backend connection error", "addr", addr, "err", err)
		}
		delete(g.connPool, addr)
	}
	return nil
}

// NewGrpcProxyListener 创建并启动 gRPC 透明流代理服务器
// 返回 grpc.Server 实例用于优雅停机。
// metrics 可为 nil，为 nil 时不上报 Prometheus 指标。
func NewGrpcProxyListener(lb *LoadBalancer, listenAddr string, metrics *observability.GatewayMetrics) (*grpc.Server, net.Listener, error) {
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen %s: %w", listenAddr, err)
	}

	proxy := NewGrpcProxyServer(lb, metrics)

	grpcServer := grpc.NewServer(
		grpc.UnknownServiceHandler(proxy.TransparentStreamDirector),
		grpc.CustomCodec(rawCodec{}),
	)

	return grpcServer, lis, nil
}
