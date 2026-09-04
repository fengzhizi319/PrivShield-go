package mapper

import (
	// context：用于传递 gRPC 调用的上下文
	// context: passes gRPC call context
	"context"
	// encoding/json：用于解析前端 JSON 请求体
	// encoding/json: parses frontend JSON request body
	"encoding/json"

	// pb：由 proto 生成的 gRPC 客户端与消息类型
	// pb: generated gRPC client and message types from proto
	pb "github.com/fengzhizi319/PrivShield-go/console/bff-go/proto"
)

// ---------------------------------------------------------------------------
// LDP handlers —— Local Differential Privacy / 本地差分隐私
//
// Core difference between LDP and DP: noise is added locally on the client side;
// the server can never access users' raw data, only performing statistical estimation
// from perturbed data. Two groups:
// LDP 与 DP 的核心区别：噪声在客户端本地添加，
// 服务端永远无法获取用户的原始数据，只能根据扰动后的数据进行统计估计。
// 分为两组：
//   - perturb: client-side local noise addition / 客户端本地加噪
//   - estimate: server-side estimation from perturbed data / 服务端根据扰动数据估计真实分布
// ---------------------------------------------------------------------------

// handlePerturbBinary 处理 /v1/privacy/ldp/perturb/binary 路径，二进制值本地扰动。
//
// 对每个 0/1 值以一定概率翻转（由 epsilon 控制），
// 使服务端无法确定单个用户的真实值。
func (m *Mapper) handlePerturbBinary(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 PerturbBinaryBatchRequest：values 为 0/1 数组
	resp, err := client.PerturbBinaryBatch(ctx, &pb.PerturbBinaryBatchRequest{
		Values:  getIntSlice(v, "values"),      // 原始 0/1 值数组
		Epsilon: getFloat64(v, "epsilon", 1.0), // 隐私预算 ε（越小噪声越大）
	})
	if err != nil {
		return nil, err
	}
	// 返回扰动后的 0/1 值数组
	return map[string][]int32{"results": resp.Results}, nil
}

// handlePerturbCategorical 处理 /v1/privacy/ldp/perturb/categorical 路径，分类值本地扰动。
//
// 对每个分类值以一定概率替换为随机分类（随机响应机制），
// 使服务端无法确定单个用户的真实分类。
func (m *Mapper) handlePerturbCategorical(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 PerturbCategoricalBatchRequest：values 为原始分类值，categories 为所有可能分类
	resp, err := client.PerturbCategoricalBatch(ctx, &pb.PerturbCategoricalBatchRequest{
		Values:     getStrings(v, "values"),       // 原始分类值数组
		Categories: getStrings(v, "categories"),   // 所有可能的分类标签
		Epsilon:    getFloat64(v, "epsilon", 1.0), // 隐私预算 ε
	})
	if err != nil {
		return nil, err
	}
	// 返回扰动后的分类值数组
	return map[string][]string{"results": resp.Results}, nil
}

// handleEstimateBinary 处理 /v1/privacy/ldp/estimate/binary 路径，二进制频率估计。
//
// 根据扰动后的 0/1 值数组，使用统计方法估计真实的 1 的比例。
func (m *Mapper) handleEstimateBinary(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 EstimateBinaryFrequencyRequest：reported_values 为扰动后的值
	resp, err := client.EstimateBinaryFrequency(ctx, &pb.EstimateBinaryFrequencyRequest{
		ReportedValues: getIntSlice(v, "reported_values"), // 扰动后的 0/1 值
		Epsilon:        getFloat64(v, "epsilon", 1.0),     // 隐私预算 ε（需与扰动时一致）
	})
	if err != nil {
		return nil, err
	}
	// 返回估计的真实频率（0~1 之间的浮点数）
	return map[string]float64{"estimated_frequency": resp.EstimatedFrequency}, nil
}

// handleEstimateCategorical 处理 /v1/privacy/ldp/estimate/categorical 路径，分类直方图估计。
//
// 根据扰动后的分类值数组，使用统计方法估计每个分类的真实比例。
func (m *Mapper) handleEstimateCategorical(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 EstimateCategoricalHistogramRequest：reported_values 为扰动后的值
	resp, err := client.EstimateCategoricalHistogram(ctx, &pb.EstimateCategoricalHistogramRequest{
		ReportedValues: getStrings(v, "reported_values"), // 扰动后的分类值
		Categories:     getStrings(v, "categories"),      // 所有可能的分类标签
		Epsilon:        getFloat64(v, "epsilon", 1.0),    // 隐私预算 ε
	})
	if err != nil {
		return nil, err
	}
	// 返回估计的直方图（分类标签 → 估计比例）
	return map[string]map[string]float64{"estimated_histogram": resp.EstimatedHistogram}, nil
}
