// Package grpcserver provides a gRPC server implementation for the Go BFF gateway.
// Package grpcserver 提供 Go BFF 网关的 gRPC 服务端实现，对外暴露与 Agent 同构的 gRPC 契约。
package grpcserver

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/config"
	pb "github.com/fengzhizi319/PrivShield-go/console/bff-go/proto"
	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"
)

// Server implements pb.PrivacyServiceServer as a high-performance gRPC gateway proxy.
type Server struct {
	pb.UnimplementedPrivacyServiceServer
	client    *agent.Client
	cfg       *config.Config
	logger    *slog.Logger
	grpcS     *grpc.Server
	whitelist *tlsutil.DynamicWhitelist
}

// New creates a new gRPC Server instance for the BFF gateway.
func New(client *agent.Client, cfg *config.Config, logger *slog.Logger) *Server {
	return &Server{
		client: client,
		cfg:    cfg,
		logger: logger,
	}
}

// Start launches the gRPC server on the configured host and port.
func (s *Server) Start(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	var opts []grpc.ServerOption
	opts = append(opts,
		grpc.MaxRecvMsgSize(64<<20),
		grpc.MaxSendMsgSize(64<<20),
	)

	// mTLS CN whitelist authorization for inbound gRPC connections.
	if s.cfg.ConsoleMTLSWhitelistFile != "" {
		unaryInterceptor, streamInterceptor, dw, err := tlsutil.NewWhitelistInterceptor(s.cfg.ConsoleMTLSWhitelistFile)
		if err != nil {
			return fmt.Errorf("failed to load mTLS whitelist: %w", err)
		}
		s.whitelist = dw
		opts = append(opts,
			grpc.UnaryInterceptor(unaryInterceptor),
			grpc.StreamInterceptor(streamInterceptor),
		)
		s.logger.Info("bff grpc server configured with mTLS CN whitelist",
			"path", s.cfg.ConsoleMTLSWhitelistFile,
		)
	}

	// TLS / mTLS support
	if s.cfg.ConsoleTLSEnabled {
		tlsConfig, err := tlsutil.BuildServerTLSConfig(&tlsutil.ServerTLSConfig{
			Enabled:          s.cfg.ConsoleTLSEnabled,
			CertFile:         s.cfg.ConsoleTLSCertFile,
			KeyFile:          s.cfg.ConsoleTLSKeyFile,
			CAFile:           s.cfg.ConsoleTLSCAFile,
			ClientAuth:       s.cfg.ConsoleTLSClientAuth,
			PinnedPubKeyFile: s.cfg.ConsoleTLSPinnedPubKeyFile,
		})
		if err != nil {
			return fmt.Errorf("failed to build gRPC TLS config: %w", err)
		}
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConfig)))
		s.logger.Info("bff grpc server TLS enabled", "mTLS", s.cfg.ConsoleTLSClientAuth != "")
	}

	grpcServer := grpc.NewServer(opts...)
	pb.RegisterPrivacyServiceServer(grpcServer, s)

	// Register standard health check service
	healthServer := health.NewServer()
	healthServer.SetServingStatus("privacy.local.PrivacyService", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	s.grpcS = grpcServer
	s.logger.Info("bff grpc server listening", "addr", addr)
	return grpcServer.Serve(lis)
}

// Stop gracefully stops the gRPC server.
func (s *Server) Stop() {
	if s.grpcS != nil {
		s.grpcS.GracefulStop()
	}
	if s.whitelist != nil {
		s.whitelist.Close()
	}
}

// outgoingCtx attaches trace and auth metadata to the upstream gRPC context.
func (s *Server) outgoingCtx(ctx context.Context) context.Context {
	return s.client.WithAuth(s.client.WithTraceFromContext(ctx))
}

// --- gRPC Method Proxies / Forwarding Implementation ---

func (s *Server) Mask(ctx context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().Mask(s.outgoingCtx(ctx), req)
}

func (s *Server) MaskRecord(ctx context.Context, req *pb.MaskRecordRequest) (*pb.MaskRecordResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().MaskRecord(s.outgoingCtx(ctx), req)
}

func (s *Server) MaskBatch(ctx context.Context, req *pb.MaskBatchRequest) (*pb.MaskBatchResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().MaskBatch(s.outgoingCtx(ctx), req)
}

func (s *Server) MaskDataFrame(ctx context.Context, req *pb.MaskDataFrameRequest) (*pb.MaskDataFrameResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().MaskDataFrame(s.outgoingCtx(ctx), req)
}

func (s *Server) Hash(ctx context.Context, req *pb.HashRequest) (*pb.HashResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().Hash(s.outgoingCtx(ctx), req)
}

func (s *Server) DPCount(ctx context.Context, req *pb.DPRequest) (*pb.DPResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().DPCount(s.outgoingCtx(ctx), req)
}

func (s *Server) DPSum(ctx context.Context, req *pb.DPRequest) (*pb.DPResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().DPSum(s.outgoingCtx(ctx), req)
}

func (s *Server) DPMean(ctx context.Context, req *pb.DPRequest) (*pb.DPResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().DPMean(s.outgoingCtx(ctx), req)
}

func (s *Server) DPHistogram(ctx context.Context, req *pb.DPHistogramRequest) (*pb.DPHistogramResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().DPHistogram(s.outgoingCtx(ctx), req)
}

func (s *Server) KAnonymizeRecord(ctx context.Context, req *pb.KAnonymizeRequest) (*pb.KAnonymizeResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().KAnonymizeRecord(s.outgoingCtx(ctx), req)
}

func (s *Server) KAnonymizeTable(ctx context.Context, req *pb.KAnonymizeTableRequest) (*pb.KAnonymizeTableResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().KAnonymizeTable(s.outgoingCtx(ctx), req)
}

func (s *Server) KAnonymizeDataFrame(ctx context.Context, req *pb.KAnonymizeDataFrameRequest) (*pb.KAnonymizeDataFrameResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().KAnonymizeDataFrame(s.outgoingCtx(ctx), req)
}

func (s *Server) ObfuscateQuery(ctx context.Context, req *pb.ObfuscateQueryRequest) (*pb.ObfuscateQueryResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().ObfuscateQuery(s.outgoingCtx(ctx), req)
}

func (s *Server) ObfuscateQueryBatch(ctx context.Context, req *pb.ObfuscateQueryBatchRequest) (*pb.ObfuscateQueryBatchResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().ObfuscateQueryBatch(s.outgoingCtx(ctx), req)
}

func (s *Server) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	if s.client == nil {
		return &pb.HealthResponse{
			Status:    "degraded",
			Namespace: "default",
		}, nil
	}
	return s.client.Health(ctx)
}

func (s *Server) RecommendParams(ctx context.Context, req *pb.RecommendRequest) (*pb.RecommendResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().RecommendParams(s.outgoingCtx(ctx), req)
}

func (s *Server) DPVectorSum(ctx context.Context, req *pb.DPVectorSumRequest) (*pb.DPVectorSumResponse, error) {
	if s.client == nil {
		return nil, status.Error(codes.Unavailable, "agent client not initialized")
	}
	return s.client.Raw().DPVectorSum(s.outgoingCtx(ctx), req)
}
