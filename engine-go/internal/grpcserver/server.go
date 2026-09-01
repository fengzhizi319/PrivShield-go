// Package grpcserver 提供 gRPC 服务端实现。
//
// 采用标准 protobuf 生成桩代码 (engine-go/internal/grpcserver/proto)，实现类型安全的
// RegisterPrivacyServiceServer 接口。所有 44 个 RPC 方法通过
// 标准 gRPC 协议与微服务及控制台 BFF 进行低延迟通信。
package grpcserver

import (
	"context"
	"log/slog"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/status"

	pb "github.com/fengzhizi319/PrivShield-go/engine-go/internal/grpcserver/proto"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/observability"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/service"
	pkggrpcserver "github.com/fengzhizi319/PrivShield-go/pkg/grpcserver"
)

// Server gRPC 隐私服务服务端
type Server struct {
	pb.UnimplementedPrivacyServiceServer
	svc     *service.PrivacyService
	metrics *observability.EngineMetrics
	*pkggrpcserver.Server
}

// NewServer 创建 gRPC 服务端实例
// 可选传入 grpc.ServerOption（如 Keepalive、TLS 凭证、mTLS 白名单拦截器）
func NewServer(svc *service.PrivacyService, opts ...grpc.ServerOption) *Server {
	return &Server{
		svc:    svc,
		Server: pkggrpcserver.New("", opts...),
	}
}

// WithMetrics 注入 engine-go 指标收集器，用于记录 gRPC 统一 metrics。
func (s *Server) WithMetrics(m *observability.EngineMetrics) *Server {
	s.metrics = m
	return s
}

// Serve 启动 gRPC 服务（阻塞）
func (s *Server) Serve(lis net.Listener) error {
	builtinOpts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(64 * 1024 * 1024), // 64MB 接收上限，防止 OOM
		grpc.MaxSendMsgSize(64 * 1024 * 1024), // 64MB 发送上限
		grpc.MaxConcurrentStreams(250),        // 并发流限制
	}
	s.WithOptions(builtinOpts...)
	if s.metrics != nil {
		s.WithUnaryInterceptor(s.metrics.UnaryServerInterceptor())
	}
	pb.RegisterPrivacyServiceServer(s.Server, s)
	slog.Info("gRPC server starting (Protobuf + Type-Safe)", "addr", lis.Addr())
	return s.ServeListener(lis)
}

// ──────────────────────────────────────────────
// 核心 RPC 处理器实现
// ──────────────────────────────────────────────

func (s *Server) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{
		Status:    "ok",
		Namespace: "default",
	}, nil
}

func (s *Server) Mask(ctx context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
	maskType := inferMaskType(req.FieldName)
	result, err := s.svc.MaskField(maskType, req.Value)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "mask failed: %v", err)
	}
	return &pb.MaskResponse{Result: result}, nil
}

func (s *Server) MaskRecord(ctx context.Context, req *pb.MaskRecordRequest) (*pb.MaskRecordResponse, error) {
	result := s.svc.MaskRecord(req.Record)
	return &pb.MaskRecordResponse{Result: result}, nil
}

func (s *Server) MaskBatch(ctx context.Context, req *pb.MaskBatchRequest) (*pb.MaskBatchResponse, error) {
	results := make([]string, len(req.Values))
	for i, v := range req.Values {
		fieldType := "default"
		if i < len(req.FieldNames) {
			fieldType = inferMaskType(req.FieldNames[i])
		}
		r, err := s.svc.MaskField(fieldType, v)
		if err != nil {
			results[i] = "***"
		} else {
			results[i] = r
		}
	}
	return &pb.MaskBatchResponse{Results: results}, nil
}

func (s *Server) Hash(ctx context.Context, req *pb.HashRequest) (*pb.HashResponse, error) {
	val := s.svc.HashSM3(req.Value, req.Salt)
	return &pb.HashResponse{Result: val}, nil
}

func (s *Server) DPCount(ctx context.Context, req *pb.DPRequest) (*pb.DPResponse, error) {
	count := len(req.Values)
	noisy, err := s.svc.NoisyCount(ctx, count, req.Epsilon)
	if err != nil {
		return nil, status.Errorf(codes.ResourceExhausted, "dp count: %v", err)
	}
	return &pb.DPResponse{Result: noisy}, nil
}

func (s *Server) DPSum(ctx context.Context, req *pb.DPRequest) (*pb.DPResponse, error) {
	sensitivity := req.ClipUpper - req.ClipLower
	if sensitivity <= 0 {
		sensitivity = 1.0
	}
	noisy, err := s.svc.NoisySum(ctx, req.Values, req.Epsilon, sensitivity)
	if err != nil {
		return nil, status.Errorf(codes.ResourceExhausted, "dp sum: %v", err)
	}
	return &pb.DPResponse{Result: noisy}, nil
}

func (s *Server) DPMean(ctx context.Context, req *pb.DPRequest) (*pb.DPResponse, error) {
	noisy, err := s.svc.NoisyMean(ctx, req.Values, req.Epsilon, req.Delta, req.ClipUpper)
	if err != nil {
		return nil, status.Errorf(codes.ResourceExhausted, "dp mean: %v", err)
	}
	return &pb.DPResponse{Result: noisy}, nil
}

func (s *Server) DPNoisyCount(ctx context.Context, req *pb.DPNoisyCountRequest) (*pb.DPResponse, error) {
	noisy, err := s.svc.NoisyCount(ctx, int(req.TrueCount), req.Epsilon)
	if err != nil {
		return nil, status.Errorf(codes.ResourceExhausted, "dp count: %v", err)
	}
	return &pb.DPResponse{Result: noisy}, nil
}

func (s *Server) DPNoisySum(ctx context.Context, req *pb.DPNoisySumRequest) (*pb.DPResponse, error) {
	sensitivity := req.Sensitivity
	if sensitivity <= 0 {
		sensitivity = 1.0
	}
	noisy, err := s.svc.NoisySum(ctx, []float64{req.TrueSum}, req.Epsilon, sensitivity)
	if err != nil {
		return nil, status.Errorf(codes.ResourceExhausted, "dp sum: %v", err)
	}
	return &pb.DPResponse{Result: noisy}, nil
}

func (s *Server) DPNoisyMean(ctx context.Context, req *pb.DPNoisyMeanRequest) (*pb.DPResponse, error) {
	mean := req.TrueSum
	if req.TrueCount > 0 {
		mean = req.TrueSum / req.TrueCount
	}
	noisy, err := s.svc.NoisyMean(ctx, []float64{mean}, req.Epsilon, req.Delta, req.ClipUpper)
	if err != nil {
		return nil, status.Errorf(codes.ResourceExhausted, "dp mean: %v", err)
	}
	return &pb.DPResponse{Result: noisy}, nil
}

func (s *Server) ObfuscateQuery(ctx context.Context, req *pb.ObfuscateQueryRequest) (*pb.ObfuscateQueryResponse, error) {
	dummyCount := int(req.NumDummies)
	if dummyCount <= 0 {
		dummyCount = 4
	}
	queries, _ := s.svc.ObfuscateQuery(req.Query, dummyCount, req.Domain)
	return &pb.ObfuscateQueryResponse{
		Result: queries,
	}, nil
}

func (s *Server) KAnonymizeRecord(ctx context.Context, req *pb.KAnonymizeRequest) (*pb.KAnonymizeResponse, error) {
	res := s.svc.MaskRecord(req.Record)
	return &pb.KAnonymizeResponse{Result: res}, nil
}

// ──────────────────────────────────────────────
// 辅助函数
// ──────────────────────────────────────────────

// inferMaskType 根据字段名推断脱敏类型
func inferMaskType(fieldName string) string {
	lower := strings.ToLower(fieldName)
	switch {
	case containsAny(lower, "id_card", "idcard", "cert_no", "identity", "身份证"):
		return "id_card"
	case containsAny(lower, "phone", "mobile", "tel", "手机", "电话"):
		return "phone"
	case containsAny(lower, "bank", "credit_card", "银行卡"):
		return "bank_card"
	case containsAny(lower, "email", "mail", "邮箱"):
		return "email"
	case containsAny(lower, "address", "addr", "地址"):
		return "address"
	case containsAny(lower, "name", "姓名"):
		return "name"
	case containsAny(lower, "officer", "军官"):
		return "officer_id"
	default:
		return "default"
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
