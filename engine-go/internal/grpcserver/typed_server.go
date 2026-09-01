// Package grpcserver 提供类型安全的 gRPC PrivacyService 实现。
//
// 使用 protoc-gen-go 生成的桩代码，实现 proto/privacy.proto 定义的
// 34 个 RPC 方法。未实现的方法通过嵌入 UnimplementedPrivacyServiceServer
// 返回 Unimplemented 状态码。
package grpcserver

import (
	"context"
	"encoding/json"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/fengzhizi319/PrivShield-go/engine-go/internal/grpcserver/proto"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/service"
	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/kano"
)

// TypedServer 类型安全的 gRPC 隐私服务端
type TypedServer struct {
	pb.UnimplementedPrivacyServiceServer // 前向兼容
	svc                                  *service.PrivacyService
}

// NewTypedServer 创建类型安全 gRPC 服务端
func NewTypedServer(svc *service.PrivacyService) *TypedServer {
	return &TypedServer{svc: svc}
}

// ──────────────────────────────────────────────
// 核心 RPC 实现
// ──────────────────────────────────────────────

// Health 健康检查
func (s *TypedServer) Health(_ context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{
		Status:    "ok",
		Namespace: "default",
	}, nil
}

// Mask 单字段脱敏
func (s *TypedServer) Mask(_ context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
	maskType := inferMaskType(req.GetFieldName())
	result, err := s.svc.MaskField(maskType, req.GetValue())
	if err != nil {
		return &pb.MaskResponse{Result: "***"}, nil
	}
	return &pb.MaskResponse{Result: result}, nil
}

// MaskRecord 记录级脱敏
func (s *TypedServer) MaskRecord(_ context.Context, req *pb.MaskRecordRequest) (*pb.MaskRecordResponse, error) {
	result := s.svc.MaskRecord(req.GetRecord())
	return &pb.MaskRecordResponse{Result: result}, nil
}

// MaskBatch 批量脱敏
func (s *TypedServer) MaskBatch(_ context.Context, req *pb.MaskBatchRequest) (*pb.MaskBatchResponse, error) {
	// 如果有 field_names + values，逐字段脱敏
	if len(req.GetFieldNames()) > 0 && len(req.GetValues()) > 0 {
		results := make([]string, len(req.GetValues()))
		for i, v := range req.GetValues() {
			fieldType := "default"
			if i < len(req.GetFieldNames()) {
				fieldType = inferMaskType(req.GetFieldNames()[i])
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
	return nil, status.Error(codes.InvalidArgument, "provide field_names+values or use MaskRecord")
}

// Hash HMAC 加盐散列
func (s *TypedServer) Hash(_ context.Context, req *pb.HashRequest) (*pb.HashResponse, error) {
	return &pb.HashResponse{Result: s.svc.HashHMAC(req.GetValue(), req.GetSalt())}, nil
}

// DPNoisyCount 差分隐私噪声计数
func (s *TypedServer) DPNoisyCount(ctx context.Context, req *pb.DPNoisyCountRequest) (*pb.DPResponse, error) {
	result, err := s.svc.NoisyCount(ctx, int(req.GetTrueCount()), req.GetEpsilon())
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	return &pb.DPResponse{Result: result}, nil
}

// DPNoisySum 差分隐私噪声求和
func (s *TypedServer) DPNoisySum(ctx context.Context, req *pb.DPNoisySumRequest) (*pb.DPResponse, error) {
	result, err := s.svc.NoisySum(ctx, []float64{req.GetTrueSum()}, req.GetEpsilon(), req.GetSensitivity())
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	return &pb.DPResponse{Result: result}, nil
}

// DPNoisyMean 差分隐私噪声均值
func (s *TypedServer) DPNoisyMean(ctx context.Context, req *pb.DPNoisyMeanRequest) (*pb.DPResponse, error) {
	result, err := s.svc.NoisyMean(ctx, []float64{req.GetTrueSum()}, req.GetEpsilon(), req.GetDelta(), req.GetSensitivity())
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	return &pb.DPResponse{Result: result}, nil
}

// DPCount 差分隐私计数（计算 values 长度后加噪）
func (s *TypedServer) DPCount(ctx context.Context, req *pb.DPRequest) (*pb.DPResponse, error) {
	if len(req.GetValues()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "values required")
	}
	result, err := s.svc.NoisyCount(ctx, len(req.GetValues()), req.GetEpsilon())
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	return &pb.DPResponse{Result: result}, nil
}

func (s *TypedServer) DPSum(ctx context.Context, req *pb.DPRequest) (*pb.DPResponse, error) {
	sensitivity := 1.0
	if req.GetClipUpper() > 0 {
		sensitivity = req.GetClipUpper() - req.GetClipLower()
	}
	// 截断值至 [clipLower, clipUpper] 确保实际敏感度与声明一致
	clipped := clipToRange(req.GetValues(), req.GetClipLower(), req.GetClipUpper())
	result, err := s.svc.NoisySum(ctx, clipped, req.GetEpsilon(), sensitivity)
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	return &pb.DPResponse{Result: result}, nil
}

func (s *TypedServer) DPMean(ctx context.Context, req *pb.DPRequest) (*pb.DPResponse, error) {
	sensitivity := 1.0
	if req.GetClipUpper() > 0 {
		sensitivity = req.GetClipUpper()
	}
	result, err := s.svc.NoisyMean(ctx, req.GetValues(), req.GetEpsilon(), req.GetDelta(), sensitivity)
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	return &pb.DPResponse{Result: result}, nil
}

// KAnonymizeRecord K-匿名单记录（实际执行掩码脱敏，K-匿名语义由表级 KAnonymizeTable 实现）
func (s *TypedServer) KAnonymizeRecord(_ context.Context, req *pb.KAnonymizeRequest) (*pb.KAnonymizeResponse, error) {
	result := s.svc.MaskRecord(req.GetRecord())
	return &pb.KAnonymizeResponse{Result: result}, nil
}

// ObfuscateQuery 查询混淆
func (s *TypedServer) ObfuscateQuery(_ context.Context, req *pb.ObfuscateQueryRequest) (*pb.ObfuscateQueryResponse, error) {
	queries, _ := s.svc.ObfuscateQuery(req.GetQuery(), int(req.GetNumDummies()), req.GetDomain())
	return &pb.ObfuscateQueryResponse{Result: queries}, nil
}

// DynClassify 动态分类分级
func (s *TypedServer) DynClassify(_ context.Context, req *pb.DynClassificationRequest) (*pb.DynClassificationResponse, error) {
	result := s.svc.Classify(req.GetFieldName(), req.GetFieldValue())

	tag := &pb.DynSecurityTagProto{
		Level:        string(result.Level),
		Category:     result.Category,
		RuleId:       result.MatchedBy,
		SourceEngine: "rule",
		Domain:       req.GetDomain(),
		StandardId:   req.GetStandard(),
		MatchTarget:  "field_name",
	}

	return &pb.DynClassificationResponse{
		Tags:           []*pb.DynSecurityTagProto{tag},
		MaxLevel:       string(result.Level),
		AuditTimestamp: time.Now().UTC().Format(time.RFC3339),
		EngineLayer:    result.MatchedBy,
	}, nil
}

// ──────────────────────────────────────────────
// LDP 批量扰动 / 频率估计 RPC
// ──────────────────────────────────────────────

// PerturbBinaryBatch 批量二值 LDP 扰动
func (s *TypedServer) PerturbBinaryBatch(_ context.Context, req *pb.PerturbBinaryBatchRequest) (*pb.PerturbBinaryBatchResponse, error) {
	values := make([]int, len(req.GetValues()))
	for i, v := range req.GetValues() {
		values[i] = int(v)
	}
	results := s.svc.PerturbBinaryBatch(values, req.GetEpsilon())
	out := make([]int32, len(results))
	for i, v := range results {
		out[i] = int32(v)
	}
	return &pb.PerturbBinaryBatchResponse{Results: out}, nil
}

// PerturbCategoricalBatch 批量类别 LDP 扰动
func (s *TypedServer) PerturbCategoricalBatch(_ context.Context, req *pb.PerturbCategoricalBatchRequest) (*pb.PerturbCategoricalBatchResponse, error) {
	results := s.svc.PerturbCategoricalBatch(req.GetValues(), req.GetCategories(), req.GetEpsilon())
	return &pb.PerturbCategoricalBatchResponse{Results: results}, nil
}

// EstimateBinaryFrequency 二值频率无偏估计
func (s *TypedServer) EstimateBinaryFrequency(_ context.Context, req *pb.EstimateBinaryFrequencyRequest) (*pb.EstimateBinaryFrequencyResponse, error) {
	values := make([]int, len(req.GetReportedValues()))
	for i, v := range req.GetReportedValues() {
		values[i] = int(v)
	}
	freq := s.svc.EstimateBinaryFrequency(values, req.GetEpsilon())
	return &pb.EstimateBinaryFrequencyResponse{EstimatedFrequency: freq}, nil
}

// EstimateCategoricalHistogram 类别直方图无偏估计
func (s *TypedServer) EstimateCategoricalHistogram(_ context.Context, req *pb.EstimateCategoricalHistogramRequest) (*pb.EstimateCategoricalHistogramResponse, error) {
	hist := s.svc.EstimateCategoricalHistogram(req.GetReportedValues(), req.GetCategories(), req.GetEpsilon())
	return &pb.EstimateCategoricalHistogramResponse{EstimatedHistogram: hist}, nil
}

// ──────────────────────────────────────────────
// QOL 批量混淆 RPC
// ──────────────────────────────────────────────

// ObfuscateQueryBatch 批量查询混淆
func (s *TypedServer) ObfuscateQueryBatch(_ context.Context, req *pb.ObfuscateQueryBatchRequest) (*pb.ObfuscateQueryBatchResponse, error) {
	allResults := s.svc.ObfuscateQueryBatch(req.GetQueries(), int(req.GetNumDummies()), req.GetDomain())
	responses := make([]*pb.ObfuscateQueryResponse, len(allResults))
	for i, queries := range allResults {
		responses[i] = &pb.ObfuscateQueryResponse{Result: queries}
	}
	return &pb.ObfuscateQueryBatchResponse{Results: responses}, nil
}

// ──────────────────────────────────────────────
// DP 直方图 / 分块 / 向量 RPC
// ──────────────────────────────────────────────

// DPHistogram 差分隐私直方图
func (s *TypedServer) DPHistogram(ctx context.Context, req *pb.DPHistogramRequest) (*pb.DPHistogramResponse, error) {
	trueCounts := make(map[string]int)
	for _, cat := range req.GetCategories() {
		trueCounts[cat] = 0
	}
	for _, v := range req.GetValues() {
		if _, ok := trueCounts[v]; ok {
			trueCounts[v]++
		}
	}
	result, err := s.svc.DPHistogram(ctx, trueCounts, req.GetEpsilon())
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	return &pb.DPHistogramResponse{Result: result}, nil
}

// DPNoisyHistogram 已知真实计数的差分隐私直方图
func (s *TypedServer) DPNoisyHistogram(ctx context.Context, req *pb.DPNoisyHistogramRequest) (*pb.DPHistogramResponse, error) {
	trueCounts := make(map[string]int)
	for k, v := range req.GetTrueCounts() {
		trueCounts[k] = int(v)
	}
	result, err := s.svc.DPHistogram(ctx, trueCounts, req.GetEpsilon())
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	return &pb.DPHistogramResponse{Result: result}, nil
}

// DPChunkedCount 分块差分隐私计数
func (s *TypedServer) DPChunkedCount(ctx context.Context, req *pb.DPChunkedCountRequest) (*pb.DPResponse, error) {
	chunks := req.GetChunks()
	if len(chunks) == 0 {
		return nil, status.Error(codes.InvalidArgument, "chunks required")
	}
	// 每个分块计数后求和，添加 Laplace 噪声
	total := 0.0
	for _, chunk := range chunks {
		total += float64(len(chunk.GetValues()))
	}
	result, err := s.svc.NoisyCount(ctx, int(total), req.GetEpsilon())
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	return &pb.DPResponse{Result: result}, nil
}

// DPChunkedSum 分块差分隐私求和
func (s *TypedServer) DPChunkedSum(ctx context.Context, req *pb.DPChunkedSumRequest) (*pb.DPResponse, error) {
	chunks := req.GetChunks()
	if len(chunks) == 0 {
		return nil, status.Error(codes.InvalidArgument, "chunks required")
	}
	allValues := make([]float64, 0)
	for _, chunk := range chunks {
		allValues = append(allValues, chunk.GetValues()...)
	}
	sensitivity := req.GetClipUpper() - req.GetClipLower()
	if sensitivity <= 0 {
		sensitivity = 1.0
	}
	// 截断值至 [clipLower, clipUpper] 确保实际敏感度与声明一致
	clipped := clipToRange(allValues, req.GetClipLower(), req.GetClipUpper())
	result, err := s.svc.NoisySum(ctx, clipped, req.GetEpsilon(), sensitivity)
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	return &pb.DPResponse{Result: result}, nil
}

// DPChunkedMean 分块差分隐私均值
func (s *TypedServer) DPChunkedMean(ctx context.Context, req *pb.DPChunkedMeanRequest) (*pb.DPResponse, error) {
	chunks := req.GetChunks()
	if len(chunks) == 0 {
		return nil, status.Error(codes.InvalidArgument, "chunks required")
	}
	allValues := make([]float64, 0)
	for _, chunk := range chunks {
		allValues = append(allValues, chunk.GetValues()...)
	}
	clipBound := req.GetClipUpper()
	if clipBound <= 0 {
		clipBound = 1.0
	}
	result, err := s.svc.NoisyMean(ctx, allValues, req.GetEpsilon(), req.GetDelta(), clipBound)
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	return &pb.DPResponse{Result: result}, nil
}

// DPChunkedHistogram 分块差分隐私直方图
func (s *TypedServer) DPChunkedHistogram(ctx context.Context, req *pb.DPChunkedHistogramRequest) (*pb.DPHistogramResponse, error) {
	// 合并所有分块计数
	trueCounts := make(map[string]int)
	for _, cat := range req.GetCategories() {
		trueCounts[cat] = 0
	}
	for _, chunk := range req.GetChunks() {
		for _, v := range chunk.GetValues() {
			if _, ok := trueCounts[v]; ok {
				trueCounts[v]++
			}
		}
	}
	result, err := s.svc.DPHistogram(ctx, trueCounts, req.GetEpsilon())
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	return &pb.DPHistogramResponse{Result: result}, nil
}

// DPVectorSum 差分隐私向量求和（通过 service 层走预算检查）
func (s *TypedServer) DPVectorSum(ctx context.Context, req *pb.DPVectorSumRequest) (*pb.DPVectorSumResponse, error) {
	chunkVecs := req.GetVectors()
	if len(chunkVecs) == 0 {
		return nil, status.Error(codes.InvalidArgument, "vectors required")
	}
	vectors := make([][]float64, len(chunkVecs))
	for i, c := range chunkVecs {
		vectors[i] = c.GetValues()
	}
	result, err := s.svc.DPVectorSum(ctx, vectors, req.GetMaxNorm(), req.GetEpsilon())
	if err != nil {
		return nil, status.Errorf(codes.ResourceExhausted, "dp vector sum: %v", err)
	}
	return &pb.DPVectorSumResponse{NoisyVector: result}, nil
}

// DPVectorMean 差分隐私向量均值（通过 service 层走预算检查）
func (s *TypedServer) DPVectorMean(ctx context.Context, req *pb.DPVectorMeanRequest) (*pb.DPVectorMeanResponse, error) {
	chunkVecs := req.GetVectors()
	if len(chunkVecs) == 0 {
		return nil, status.Error(codes.InvalidArgument, "vectors required")
	}
	vectors := make([][]float64, len(chunkVecs))
	for i, c := range chunkVecs {
		vectors[i] = c.GetValues()
	}
	result, err := s.svc.DPVectorMean(ctx, vectors, req.GetMaxNorm(), req.GetEpsilon())
	if err != nil {
		return nil, status.Errorf(codes.ResourceExhausted, "dp vector mean: %v", err)
	}
	return &pb.DPVectorMeanResponse{MeanVector: result}, nil
}

// ──────────────────────────────────────────────
// DP 高级 RPC（Aggregate / AdaptiveClip / GroupBy）
// ──────────────────────────────────────────────

// DPAggregate 差分隐私聚合
func (s *TypedServer) DPAggregate(_ context.Context, req *pb.DPAggregateRequest) (*pb.DPAggregateResponse, error) {
	rows := req.GetRows()
	rowsMap := make([]map[string]string, len(rows))
	for i, r := range rows {
		rowsMap[i] = r.GetFields()
	}

	specs := make(map[string]string)
	if req.GetSpecsJson() != "" {
		_ = json.Unmarshal([]byte(req.GetSpecsJson()), &specs)
	}

	result, err := s.svc.DPAggregate(rowsMap, specs, req.GetEpsilon(), req.GetDelta(), req.GetClipLower(), req.GetClipUpper(), req.GetMechanism())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	jsonBytes, _ := json.Marshal(result)
	return &pb.DPAggregateResponse{ResultsJson: string(jsonBytes)}, nil
}

// DPAdaptiveClip 自适应截断
func (s *TypedServer) DPAdaptiveClip(_ context.Context, req *pb.DPAdaptiveClipRequest) (*pb.DPAdaptiveClipResponse, error) {
	values := req.GetValues()
	if len(values) == 0 {
		return nil, status.Error(codes.InvalidArgument, "values required")
	}

	clipLower, clipUpper := s.svc.DPAdaptiveClip(
		values,
		req.GetEpsilon(),
		req.GetTargetQuantile(),
		int(req.GetNumIterations()),
		req.GetInitialClip(),
	)
	return &pb.DPAdaptiveClipResponse{ClipLower: clipLower, ClipUpper: clipUpper}, nil
}

// DPGroupBy 差分隐私分组聚合
func (s *TypedServer) DPGroupBy(_ context.Context, req *pb.DPGroupByRequest) (*pb.DPGroupByResponse, error) {
	rows := req.GetRows()
	rowsMap := make([]map[string]string, len(rows))
	for i, r := range rows {
		rowsMap[i] = r.GetFields()
	}

	result, err := s.svc.DPGroupBy(
		rowsMap,
		req.GetGroupCol(),
		req.GetTargetCol(),
		req.GetAgg(),
		req.GetEpsilon(),
		req.GetDelta(),
		req.GetClipLower(),
		req.GetClipUpper(),
		req.GetMechanism(),
	)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	jsonBytes, _ := json.Marshal(result)
	return &pb.DPGroupByResponse{ResultJson: string(jsonBytes)}, nil
}

// ──────────────────────────────────────────────
// K-匿名 / DataFrame RPC
// ──────────────────────────────────────────────

// KAnonymizeTable K-匿名表级处理
func (s *TypedServer) KAnonymizeTable(_ context.Context, req *pb.KAnonymizeTableRequest) (*pb.KAnonymizeTableResponse, error) {
	rows := req.GetRows()
	records := make([]kano.Record, len(rows))
	for i, r := range rows {
		records[i] = kano.Record(r.GetFields())
	}

	anonRes, err := s.svc.KAnonymizeTable(records, req.GetQiCols(), int(req.GetK()))
	if err != nil {
		// 回退到逐条脱敏
		results := make([]*pb.RecordEntry, len(records))
		for i, rec := range records {
			masked := s.svc.MaskRecord(map[string]string(rec))
			results[i] = &pb.RecordEntry{Fields: masked}
		}
		return &pb.KAnonymizeTableResponse{Rows: results}, nil
	}

	results := make([]*pb.RecordEntry, len(anonRes.Records))
	for i, rec := range anonRes.Records {
		results[i] = &pb.RecordEntry{Fields: map[string]string(rec)}
	}
	return &pb.KAnonymizeTableResponse{Rows: results}, nil
}

// MaskDataFrame DataFrame 脱敏
func (s *TypedServer) MaskDataFrame(_ context.Context, req *pb.MaskDataFrameRequest) (*pb.MaskDataFrameResponse, error) {
	data := req.GetData()
	results := make([]*pb.RecordEntry, len(data))
	for i, row := range data {
		masked := s.svc.MaskRecord(row.GetFields())
		results[i] = &pb.RecordEntry{Fields: masked}
	}
	return &pb.MaskDataFrameResponse{Data: results}, nil
}

// KAnonymizeDataFrame K-匿名 DataFrame 处理
func (s *TypedServer) KAnonymizeDataFrame(_ context.Context, req *pb.KAnonymizeDataFrameRequest) (*pb.KAnonymizeDataFrameResponse, error) {
	data := req.GetData()
	results := make([]*pb.RecordEntry, len(data))
	for i, r := range data {
		masked := s.svc.MaskRecord(r.GetFields())
		results[i] = &pb.RecordEntry{Fields: masked}
	}
	return &pb.KAnonymizeDataFrameResponse{Data: results}, nil
}

// ──────────────────────────────────────────────
// Profile 推荐 RPC
// ──────────────────────────────────────────────

// RecommendParams 隐私参数推荐
func (s *TypedServer) RecommendParams(_ context.Context, req *pb.RecommendRequest) (*pb.RecommendResponse, error) {
	var rowsMap []map[string]interface{}
	for _, r := range req.GetRows() {
		m := make(map[string]interface{}, len(r.GetFields()))
		for k, v := range r.GetFields() {
			m[k] = v
		}
		rowsMap = append(rowsMap, m)
	}

	recommended := s.svc.RecommendParams(req.GetNamespace(), req.GetValues(), rowsMap, req.GetQiCols())
	jsonBytes, _ := json.Marshal(recommended)
	return &pb.RecommendResponse{
		Status:                "ok",
		Namespace:             req.GetNamespace(),
		RecommendedParamsJson: string(jsonBytes),
	}, nil
}
