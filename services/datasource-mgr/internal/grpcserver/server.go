// Package grpcserver implements the gRPC service for the mock datasource-mgr module with mTLS support.
// Package grpcserver 实现了模拟数据源模块（datasource-mgr）的 gRPC 服务端，支持高性能 RPC 通信与 mTLS 双向认证。
//
// ==============================================================================
// Design & Capabilities / 设计定位与核心能力：
// ==============================================================================
// 1. 数据服务接口暴露 (Data Query APIs)：
//    - 专用接口：GetYibaoData（医保数据源 API 1）、GetKangyangData（康养数据源 API 2）、
//      GetMockData3（预留数据源 API 3）、GetMockData4（预留数据源 API 4）；
//    - 通用接口：GetDataBySource（根据数据源 ID 动态路由查询）、ListMockSources（数据源资产目录列表）、
//      GetDataSource（单个数据源元数据详情）、TestConnection（数据源连通性探测）；
//    - 运维接口：Health（自检健康探针与服务标识上报）。
// 2. 零信任与双向 TLS 认证 (Zero-Trust mTLS & Key Pinning)：
//    - 支持 TLS 1.3 强加密基线；
//    - 支持基于 CA 根证书的 ClientAuth 强校验（RequireAndVerifyClientCert）；
//    - 支持公钥指纹固定（Pinned Public Key），在应用层防止伪造 CA 或证书替换攻击。
// 3. 并发安全与优雅停机 (Concurrency & Lifecycle)：
//    - 内置 Context 取消传播与 sync.WaitGroup，支持在微服务关闭时平滑等待后台任务执行完毕。
// ==============================================================================

package grpcserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"
	"github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/internal/handlers"
	pb "github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/proto"

	naming "github.com/fengzhizi319/PrivShield-go/pkg/naming"
)

// moduleVia 是在所有 gRPC 响应体中携带的服务节点标识，用于全链路追踪与调试来源识别。
const moduleVia = "datasource-mgr"

// GRPCServer implements pb.DataSourceManagerServiceServer.
// GRPCServer 实现了 Protobuf 定义的 DataSourceManagerServiceServer 接口，封装数据源查询与管理能力。
type GRPCServer struct {
	// pb.UnimplementedDataSourceManagerServiceServer 提供向前兼容的前置桩实现，
	// 避免 proto 接口扩展新增方法时导致编译失败。
	pb.UnimplementedDataSourceManagerServiceServer

	cfg    *config.Config // 运行时全局配置引用
	logger *slog.Logger   // 结构化日志记录器

	ctx    context.Context    // 控制服务端后台生命周期的父 Context
	cancel context.CancelFunc // 终止父 Context 的取消函数
	wg     sync.WaitGroup     // 同步等待组，用于追踪并平滑收敛内部异步协程
}

// New creates a new GRPCServer instance with an initialized cancellation context.
// New 创建并初始化 GRPCServer 实例，建立支持生命周期管理的 Context 上下文。
func New(cfg *config.Config, logger *slog.Logger) *GRPCServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &GRPCServer{
		cfg:    cfg,
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Shutdown gracefully stops server tasks and waits for ongoing background goroutines to finish.
// Shutdown 触发优雅停机流程：发送 Context 取消通知，并阻塞等待所有后台协程完全退出。
func (s *GRPCServer) Shutdown() {
	s.cancel()
	s.wg.Wait()
}

// Health returns self health status and latency metadata.
// Health 实现服务健康检查接口，上游（如 service-hub 或 k8s gRPC 探针）可通过此接口探测可用性。
func (s *GRPCServer) Health(ctx context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{
		Status:    "ok",
		LatencyMs: 0,
		Via:       moduleVia,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Dedicated Mock Data Endpoints / 专用模拟数据源查询接口 (API 1 ~ 4)
// ─────────────────────────────────────────────────────────────────────────────

// GetYibaoData implements API 1: queries mock healthcare/insurance records from yibao.csv.
// GetYibaoData 实现专用 API 1：查询医保就医与结算模拟数据集（包含姓名、身份证号、病案号、诊断等高敏感字段）。
func (s *GRPCServer) GetYibaoData(ctx context.Context, req *pb.DataQueryRequest) (*pb.DataQueryResponse, error) {
	// 参数安全归一化处理：限制分页单页上限与下限
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	offset := int(req.Offset)
	if offset < 0 {
		offset = 0
	}

	// 调用内部数据加载层读取记录
	rows, total, err := handlers.GetYibaoRecords(limit, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get yibao records: %v", err)
	}

	return toDataQueryResponse(naming.DSYibao, "医保就医与结算模拟数据库 (yibao.csv)", total, limit, offset, rows), nil
}

// GetKangyangData implements API 2: queries mock elderly care/physical exam records from kangyang.csv.
// GetKangyangData 实现专用 API 2：查询康养体检与慢病管理模拟数据集（包含体检指标、慢病类别、用药记录等）。
func (s *GRPCServer) GetKangyangData(ctx context.Context, req *pb.DataQueryRequest) (*pb.DataQueryResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	offset := int(req.Offset)
	if offset < 0 {
		offset = 0
	}

	rows, total, err := handlers.GetKangyangRecords(limit, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get kangyang records: %v", err)
	}

	return toDataQueryResponse(naming.DSKangyang, "康养体检与慢病模拟数据库 (kangyang.csv)", total, limit, offset, rows), nil
}

// GetMockData3 implements API 3: queries reserved municipal dataset 3.
// GetMockData3 实现预留政务数据源 3 的模拟数据查询。
func (s *GRPCServer) GetMockData3(ctx context.Context, req *pb.DataQueryRequest) (*pb.DataQueryResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	offset := int(req.Offset)
	if offset < 0 {
		offset = 0
	}

	rows, total, err := handlers.GetMock3Records(limit, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get mock3 records: %v", err)
	}

	return toDataQueryResponse(naming.DSMock3, "预留政务数据源 3", total, limit, offset, rows), nil
}

// GetMockData4 implements API 4: queries reserved municipal dataset 4.
// GetMockData4 实现预留政务数据源 4 的模拟数据查询。
func (s *GRPCServer) GetMockData4(ctx context.Context, req *pb.DataQueryRequest) (*pb.DataQueryResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	offset := int(req.Offset)
	if offset < 0 {
		offset = 0
	}

	rows, total, err := handlers.GetMock4Records(limit, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get mock4 records: %v", err)
	}

	return toDataQueryResponse(naming.DSMock4, "预留政务数据源 4", total, limit, offset, rows), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Generic Datasource Query & Management / 通用数据源动态查询与元数据管理
// ─────────────────────────────────────────────────────────────────────────────

// GetData implements canonical RPC for fetching dataset slice.
// GetData 实现规范 RPC：查询指定数据源的数据切片。
func (s *GRPCServer) GetData(ctx context.Context, req *pb.SourceDataQueryRequest) (*pb.DataQueryResponse, error) {
	return s.GetDataBySource(ctx, req)
}

// ListDataSources implements canonical RPC for listing registered data sources.
// ListDataSources 实现规范 RPC：列出所有已注册的数据源资产目录。
func (s *GRPCServer) ListDataSources(ctx context.Context, req *pb.ListMockSourcesRequest) (*pb.ListMockSourcesResponse, error) {
	return s.ListMockSources(ctx, req)
}

// GetDataBySource dynamically routes query requests by source_id.
// GetDataBySource 根据入参的 source_id 动态分发并路由查询对应的数据集。
func (s *GRPCServer) GetDataBySource(ctx context.Context, req *pb.SourceDataQueryRequest) (*pb.DataQueryResponse, error) {
	if strings.TrimSpace(req.SourceId) == "" {
		return nil, status.Error(codes.InvalidArgument, "source_id is required")
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	offset := int(req.Offset)
	if offset < 0 {
		offset = 0
	}

	rows, total, name, err := handlers.GetDataBySource(req.SourceId, limit, offset)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "%v", err)
	}

	return toDataQueryResponse(req.SourceId, name, total, limit, offset, rows), nil
}

// ListMockSources returns the full directory of available mock data sources.
// ListMockSources 返回当前注册在系统中的全部模拟数据源元数据列表（含名称、类型、状态、总行数、敏感标签等）。
func (s *GRPCServer) ListMockSources(ctx context.Context, _ *pb.ListMockSourcesRequest) (*pb.ListMockSourcesResponse, error) {
	list := handlers.ListMockDataSources()
	protos := make([]*pb.DataSourceProto, 0, len(list))
	for _, d := range list {
		protos = append(protos, &pb.DataSourceProto{
			Id:          d.ID,
			Name:        d.Name,
			Type:        d.Type,
			Description: d.Description,
			Status:      d.Status,
			RowCount:    int32(d.RowCount),
			Tags:        d.Tags,
		})
	}
	return &pb.ListMockSourcesResponse{
		Total:   int32(len(protos)),
		Sources: protos,
		Via:     moduleVia,
	}, nil
}

// GetDataSource returns metadata for a single data source by its ID.
// GetDataSource 根据数据源唯一 ID 获取其详细元数据，未找到时返回 codes.NotFound 错误。
func (s *GRPCServer) GetDataSource(ctx context.Context, req *pb.GetDataSourceRequest) (*pb.DataSourceProto, error) {
	if strings.TrimSpace(req.Id) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	ds, err := handlers.GetMockDataSource(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "%v", err)
	}
	return &pb.DataSourceProto{
		Id:          ds.ID,
		Name:        ds.Name,
		Type:        ds.Type,
		Description: ds.Description,
		Status:      ds.Status,
		RowCount:    int32(ds.RowCount),
		Tags:        ds.Tags,
	}, nil
}

// TestConnection verifies connectivity to the specified data source.
// TestConnection 测试指定数据源的物理/逻辑连通性，并返回响应延迟毫秒数。
func (s *GRPCServer) TestConnection(ctx context.Context, req *pb.TestConnectionRequest) (*pb.TestConnectionResponse, error) {
	if strings.TrimSpace(req.Id) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	_, err := handlers.GetMockDataSource(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "%v", err)
	}
	return &pb.TestConnectionResponse{
		DatasourceId: req.Id,
		Success:      true,
		LatencyMs:    1,
		Via:          moduleVia,
	}, nil
}

// toDataQueryResponse transforms raw map rows into standard protobuf DataQueryResponse.
// toDataQueryResponse 将底层的动态键值对数据行（[]map[string]any）转换为强类型的 Protobuf DataQueryResponse 响应对象。
func toDataQueryResponse(id, name string, total, limit, offset int, rows []map[string]any) *pb.DataQueryResponse {
	recordsProto := make([]*pb.DataRowProto, 0, len(rows))
	for _, row := range rows {
		fieldMap := make(map[string]string, len(row))
		for k, v := range row {
			fieldMap[k] = fmt.Sprintf("%v", v)
		}
		recordsProto = append(recordsProto, &pb.DataRowProto{Fields: fieldMap})
	}
	return &pb.DataQueryResponse{
		SourceId:   id,
		SourceName: name,
		Total:      int32(total),
		Limit:      int32(limit),
		Offset:     int32(offset),
		Records:    recordsProto,
		Via:        moduleVia,
	}
}

// ==============================================================================
// mTLS Credentials Builder & Public Key Pinning / mTLS 凭证构造与公钥指纹固定
// ==============================================================================

// BuildServerTLSConfig constructs a *tls.Config supporting mTLS and public key pinning for both HTTP/HTTPS and gRPC.
// BuildServerTLSConfig 根据运行配置构造支持 mTLS 双向身份验证和公钥指纹绑定的标准 tls.Config，可同时服务于 HTTPS REST 和 gRPC。
//
// 安全保障机制：
//  1. 强制 TLS 1.3 最低版本基线 (MinVersion: tls.VersionTLS13)，阻断已知的旧版 TLS 协议降级漏洞；
//  2. 客户端证书验证 (ClientAuth)：支持 RequireAndVerifyClientCert 模式，强制调用方提供合法的客户端证书；
//  3. 公钥指纹固定 (Public Key Pinning)：通过 VerifyPeerCertificate 回调，精确比对客户端公钥 (RSA Modulus + Exponent)，
//     即便第三方 CA 发生密钥泄露或签发了伪造证书，只要公钥不匹配即被拒绝连接（零信任防御）。
//
// BuildServerTLSConfig constructs a *tls.Config supporting mTLS and public key pinning for both HTTP/HTTPS and gRPC.
// BuildServerTLSConfig 根据运行配置构造支持 mTLS 双向身份验证和公钥指纹绑定的标准 tls.Config，可同时服务于 HTTPS REST 和 gRPC。
func BuildServerTLSConfig(cfg *config.Config) (*tls.Config, error) {
	return tlsutil.BuildServerTLSConfig(&tlsutil.ServerTLSConfig{
		Enabled:          cfg.TLSEnabled,
		CertFile:         cfg.TLSCertFile,
		KeyFile:          cfg.TLSKeyFile,
		CAFile:           cfg.TLSCAFile,
		ClientAuth:       cfg.TLSClientAuth,
		PinnedPubKeyFile: cfg.TLSPinnedPubKeyFile,
	})
}

// BuildServerCredentials constructs gRPC transport credentials supporting mTLS and public key pinning.
// BuildServerCredentials 根据运行配置构造支持 mTLS 双向身份验证和公钥指纹绑定的 gRPC 传输层安全凭证。
func BuildServerCredentials(cfg *config.Config) (credentials.TransportCredentials, error) {
	tlsConfig, err := BuildServerTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(tlsConfig), nil
}
