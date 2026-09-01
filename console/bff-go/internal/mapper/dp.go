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
// DP helpers —— Differential privacy common utilities / 差分隐私公共工具
// ---------------------------------------------------------------------------

// dpRequest 从 JSON body 构造通用的 DPRequest protobuf 消息。
//
// 基础 DP 统计量（count/sum/mean）共用同一个 DPRequest 结构，
// 包含 flat 字段：values（数值数组）、epsilon（隐私预算）、mechanism（噪声机制）等。
// 各字段均从 JSON body 顶层直接读取。
func dpRequest(body json.RawMessage) (*pb.DPRequest, error) {
	// 解析 JSON body 为通用 map
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	return &pb.DPRequest{
		Values:    getFloats(v, "values"),               // 待计算的数值数组
		Epsilon:   getFloat64(v, "epsilon", 1.0),        // 隐私预算 ε，默认 1.0
		Mechanism: getString(v, "mechanism", "laplace"), // 噪声机制，默认 Laplace
		Delta:     getFloat64(v, "delta", 0.0),          // δ 参数（Gaussian 机制需要）
		ClipLower: getFloat64(v, "clip_lower", 0.0),     // 裁剪下界
		ClipUpper: getFloat64(v, "clip_upper", 0.0),     // 裁剪上界
	}, nil
}

// handleDPCount 处理 /v1/privacy/dp/count 路径，差分隐私计数。
// 执行逻辑：dpRequest 构造请求 → 调用 DPCount RPC → 返回加噪计数结果
func (m *Mapper) handleDPCount(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	// 使用公共 dpRequest 构造 DPRequest（count/sum/mean 共用）
	req, err := dpRequest(body)
	if err != nil {
		return nil, err
	}
	// 调用上游 agent 的 DPCount RPC
	resp, err := client.DPCount(ctx, req)
	if err != nil {
		return nil, err
	}
	// 返回加噪后的计数值
	return map[string]float64{"result": resp.Result}, nil
}

// handleDPSum 处理 /v1/privacy/dp/sum 路径，差分隐私求和。
// 执行逻辑：dpRequest 构造请求 → 调用 DPSum RPC → 返回加噪求和结果
func (m *Mapper) handleDPSum(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	req, err := dpRequest(body)
	if err != nil {
		return nil, err
	}
	resp, err := client.DPSum(ctx, req)
	if err != nil {
		return nil, err
	}
	return map[string]float64{"result": resp.Result}, nil
}

// handleDPMean 处理 /v1/privacy/dp/mean 路径，差分隐私均值。
// 执行逻辑：dpRequest 构造请求 → 调用 DPMean RPC → 返回加噪均值结果
func (m *Mapper) handleDPMean(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	req, err := dpRequest(body)
	if err != nil {
		return nil, err
	}
	resp, err := client.DPMean(ctx, req)
	if err != nil {
		return nil, err
	}
	return map[string]float64{"result": resp.Result}, nil
}

// handleDPHistogram 处理 /v1/privacy/dp/histogram 路径，差分隐私直方图。
//
// 与基础 DP 统计量不同，直方图使用独立的 DPHistogramRequest，
// 需要额外的 categories 字段（分类标签列表），且 values 为字符串数组。
func (m *Mapper) handleDPHistogram(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 DPHistogramRequest：values 为字符串值，categories 为分类标签
	resp, err := client.DPHistogram(ctx, &pb.DPHistogramRequest{
		Values:     getStrings(v, "values"),              // 待统计的字符串值数组
		Categories: getStrings(v, "categories"),          // 分类标签列表
		Epsilon:    getFloat64(v, "epsilon", 1.0),        // 隐私预算 ε
		Mechanism:  getString(v, "mechanism", "laplace"), // 噪声机制
		Delta:      getFloat64(v, "delta", 0.0),          // δ 参数
	})
	if err != nil {
		return nil, err
	}
	// 返回加噪后的直方图（分类标签 → 加噪计数）
	return map[string]map[string]float64{"result": resp.Result}, nil
}

// handleDPNoisyCount 处理 /v1/privacy/dp/noisy_count 路径，带噪计数。
//
// 与基础 DPCount 不同，Noisy 变体接收已计算好的 true_count（而非原始 values），
// 仅负责添加噪声，适合调用方已自行计算精确计数的场景。
func (m *Mapper) handleDPNoisyCount(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 DPNoisyCountRequest：true_count 为精确计数，RPC 负责加噪
	resp, err := client.DPNoisyCount(ctx, &pb.DPNoisyCountRequest{
		TrueCount: getFloat64(v, "true_count", 0.0),     // 精确计数值
		Epsilon:   getFloat64(v, "epsilon", 1.0),        // 隐私预算 ε
		Mechanism: getString(v, "mechanism", "laplace"), // 噪声机制
		Delta:     getFloat64(v, "delta", 0.0),          // δ 参数
	})
	if err != nil {
		return nil, err
	}
	return map[string]float64{"result": resp.Result}, nil
}

// handleDPNoisySum 处理 /v1/privacy/dp/noisy_sum 路径，带噪求和。
//
// 接收已计算好的 true_sum，添加 sensitivity/clip 参数控制噪声规模。
func (m *Mapper) handleDPNoisySum(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 DPNoisySumRequest：true_sum + sensitivity + clip 参数
	resp, err := client.DPNoisySum(ctx, &pb.DPNoisySumRequest{
		TrueSum:     getFloat64(v, "true_sum", 0.0),       // 精确求和值
		Epsilon:     getFloat64(v, "epsilon", 1.0),        // 隐私预算 ε
		Mechanism:   getString(v, "mechanism", "laplace"), // 噪声机制
		Delta:       getFloat64(v, "delta", 0.0),          // δ 参数
		Sensitivity: getFloat64(v, "sensitivity", 0.0),    // 敏感度（决定噪声规模）
		ClipLower:   getFloat64(v, "clip_lower", 0.0),     // 裁剪下界
		ClipUpper:   getFloat64(v, "clip_upper", 0.0),     // 裁剪上界
	})
	if err != nil {
		return nil, err
	}
	return map[string]float64{"result": resp.Result}, nil
}

// handleDPNoisyMean 处理 /v1/privacy/dp/noisy_mean 路径，带噪均值。
//
// 接收 true_sum 和 true_count，通过 min_count 防止小样本场景下的隐私泄露。
func (m *Mapper) handleDPNoisyMean(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 DPNoisyMeanRequest：通过 true_sum/true_count 计算均值并加噪
	resp, err := client.DPNoisyMean(ctx, &pb.DPNoisyMeanRequest{
		TrueSum:     getFloat64(v, "true_sum", 0.0),       // 精确求和值
		TrueCount:   getFloat64(v, "true_count", 0.0),     // 精确计数
		Epsilon:     getFloat64(v, "epsilon", 1.0),        // 隐私预算 ε
		Mechanism:   getString(v, "mechanism", "laplace"), // 噪声机制
		Delta:       getFloat64(v, "delta", 0.0),          // δ 参数
		Sensitivity: getFloat64(v, "sensitivity", 0.0),    // 敏感度
		ClipLower:   getFloat64(v, "clip_lower", 0.0),     // 裁剪下界
		ClipUpper:   getFloat64(v, "clip_upper", 0.0),     // 裁剪上界
		MinCount:    getFloat64(v, "min_count", 5.0),      // 最小样本数（防止小样本泄露）
	})
	if err != nil {
		return nil, err
	}
	return map[string]float64{"result": resp.Result}, nil
}

// handleDPNoisyHistogram 处理 /v1/privacy/dp/noisy_histogram 路径，带噪直方图。
//
// 接收已计算好的 true_counts（分类标签 → 精确计数），为每个桶添加噪声。
func (m *Mapper) handleDPNoisyHistogram(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 从 JSON body 中提取 true_counts map（分类标签 → 精确计数）
	trueCounts := map[string]float64{}
	if mm, ok := v["true_counts"].(map[string]any); ok {
		// 遍历 map，将 float64 值提取到 trueCounts
		for k, val := range mm {
			if n, ok := val.(float64); ok {
				trueCounts[k] = n
			}
		}
	}
	// 构造 DPNoisyHistogramRequest：true_counts 为精确直方图，RPC 负责加噪
	resp, err := client.DPNoisyHistogram(ctx, &pb.DPNoisyHistogramRequest{
		TrueCounts: trueCounts,                           // 分类标签 → 精确计数
		Epsilon:    getFloat64(v, "epsilon", 1.0),        // 隐私预算 ε
		Mechanism:  getString(v, "mechanism", "laplace"), // 噪声机制
		Delta:      getFloat64(v, "delta", 0.0),          // δ 参数
	})
	if err != nil {
		return nil, err
	}
	// 返回加噪后的直方图（分类标签 → 加噪计数）
	return map[string]map[string]float64{"result": resp.Result}, nil
}

// handleDPChunkedCount 处理 /v1/privacy/dp/chunked_count 路径，分块差分隐私计数。
//
// 将大数据集分成多个 chunk，每个 chunk 独立计算 DP 计数后合并，
// 适合处理超出单次查询容量限制的大规模数据。
func (m *Mapper) handleDPChunkedCount(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 DPChunkedCountRequest：chunks 为分块数据，每块包含 values 数组
	resp, err := client.DPChunkedCount(ctx, &pb.DPChunkedCountRequest{
		Chunks:    getDoubleChunks(v, "chunks"),         // 分块浮点数数组
		Epsilon:   getFloat64(v, "epsilon", 1.0),        // 隐私预算 ε（在所有 chunk 间分配）
		Mechanism: getString(v, "mechanism", "laplace"), // 噪声机制
		Delta:     getFloat64(v, "delta", 0.0),          // δ 参数
	})
	if err != nil {
		return nil, err
	}
	return map[string]float64{"result": resp.Result}, nil
}

// handleDPChunkedSum 处理 /v1/privacy/dp/chunked_sum 路径，分块差分隐私求和。
//
// 与 ChunkedCount 类似，但计算的是加噪求和，额外支持 clip_lower/clip_upper 裁剪参数。
func (m *Mapper) handleDPChunkedSum(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 DPChunkedSumRequest：chunks + 裁剪参数
	resp, err := client.DPChunkedSum(ctx, &pb.DPChunkedSumRequest{
		Chunks:    getDoubleChunks(v, "chunks"),         // 分块浮点数数组
		Epsilon:   getFloat64(v, "epsilon", 1.0),        // 隐私预算 ε
		Mechanism: getString(v, "mechanism", "laplace"), // 噪声机制
		Delta:     getFloat64(v, "delta", 0.0),          // δ 参数
		ClipLower: getFloat64(v, "clip_lower", 0.0),     // 裁剪下界
		ClipUpper: getFloat64(v, "clip_upper", 0.0),     // 裁剪上界
	})
	if err != nil {
		return nil, err
	}
	return map[string]float64{"result": resp.Result}, nil
}

// handleDPChunkedMean 处理 /v1/privacy/dp/chunked_mean 路径，分块差分隐私均值。
//
// 与 ChunkedSum 类似，但计算的是加噪均值，额外支持 min_count 防止小样本泄露。
func (m *Mapper) handleDPChunkedMean(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 DPChunkedMeanRequest：chunks + 裁剪 + 最小样本数
	resp, err := client.DPChunkedMean(ctx, &pb.DPChunkedMeanRequest{
		Chunks:    getDoubleChunks(v, "chunks"),         // 分块浮点数数组
		Epsilon:   getFloat64(v, "epsilon", 1.0),        // 隐私预算 ε
		Mechanism: getString(v, "mechanism", "laplace"), // 噪声机制
		Delta:     getFloat64(v, "delta", 0.0),          // δ 参数
		ClipLower: getFloat64(v, "clip_lower", 0.0),     // 裁剪下界
		ClipUpper: getFloat64(v, "clip_upper", 0.0),     // 裁剪上界
		MinCount:  getFloat64(v, "min_count", 5.0),      // 最小样本数
	})
	if err != nil {
		return nil, err
	}
	return map[string]float64{"result": resp.Result}, nil
}

// handleDPChunkedHistogram 处理 /v1/privacy/dp/chunked_histogram 路径，分块差分隐私直方图。
//
// 与 ChunkedCount/Sum 不同，分块直方图使用 StringChunk（分类值）而非 DoubleChunk（浮点数）。
func (m *Mapper) handleDPChunkedHistogram(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 DPChunkedHistogramRequest：chunks 为字符串分块，categories 为分类标签
	resp, err := client.DPChunkedHistogram(ctx, &pb.DPChunkedHistogramRequest{
		Chunks:     getStringChunks(v, "chunks"),         // 分块字符串数组（分类值）
		Categories: getStrings(v, "categories"),          // 分类标签列表
		Epsilon:    getFloat64(v, "epsilon", 1.0),        // 隐私预算 ε
		Mechanism:  getString(v, "mechanism", "laplace"), // 噪声机制
		Delta:      getFloat64(v, "delta", 0.0),          // δ 参数
	})
	if err != nil {
		return nil, err
	}
	// 返回加噪后的直方图（分类标签 → 加噪计数）
	return map[string]map[string]float64{"result": resp.Result}, nil
}

// handleDPAggregate 处理 /v1/privacy/dp/aggregate 路径，多指标差分隐私聚合。
//
// 支持一次请求同时计算多个指标（count/sum/mean 等），
// 通过 specs_json 指定聚合规格，返回 JSON 格式的聚合结果。
func (m *Mapper) handleDPAggregate(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 DPAggregateRequest：rows 为数据行，specs_json 为聚合规格
	resp, err := client.DPAggregate(ctx, &pb.DPAggregateRequest{
		Rows:          getRecordEntries(v, "rows"),          // 数据行（每行包含 fields map）
		SpecsJson:     getString(v, "specs_json", "{}"),     // 聚合规格 JSON（指定哪些列、哪些指标）
		Epsilon:       getFloat64(v, "epsilon", 1.0),        // 隐私预算 ε
		Delta:         getFloat64(v, "delta", 0.0),          // δ 参数
		Mechanism:     getString(v, "mechanism", "laplace"), // 噪声机制
		ReturnDetails: true,                                 // 始终返回详细信息
		ClipLower:     getFloat64(v, "clip_lower", 0.0),     // 值截断下界
		ClipUpper:     getFloat64(v, "clip_upper", 100.0),   // 值截断上界（确保敏感度有界）
	})
	if err != nil {
		return nil, err
	}
	// 尝试将 results_json 解析为结构化对象，失败则保持原始字符串
	var results any
	if err := json.Unmarshal([]byte(resp.ResultsJson), &results); err != nil {
		return map[string]string{"results_json": resp.ResultsJson}, nil
	}
	return map[string]any{"results": results}, nil
}

// handleDPVectorSum 处理 /v1/privacy/dp/vector_sum 路径，差分隐私向量求和。
//
// 对多个向量进行加噪求和，适合机器学习场景下的梯度聚合。
// 通过 max_norm 限制每个向量的 L2 范数（裁剪），控制敏感度。
func (m *Mapper) handleDPVectorSum(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 DPVectorSumRequest：vectors 为多个向量，max_norm 为裁剪阈值
	resp, err := client.DPVectorSum(ctx, &pb.DPVectorSumRequest{
		Vectors:       getVectorEntries(v, "vectors"),        // 多个向量（每个为 DoubleChunk）
		MaxNorm:       getFloat64(v, "max_norm", 1.0),        // L2 范数裁剪阈值
		Epsilon:       getFloat64(v, "epsilon", 1.0),         // 隐私预算 ε
		Delta:         getFloat64(v, "delta", 0.0),           // δ 参数
		Mechanism:     getString(v, "mechanism", "gaussian"), // 默认使用 Gaussian 机制
		ReturnDetails: true,                                  // 返回详细信息
	})
	if err != nil {
		return nil, err
	}
	// 将 ResultDetails（protobuf）转换为 JSON 可序列化格式
	resultDetails, _ := marshalProto(resp.ResultDetails)
	return map[string]any{
		"noisy_vector":   resp.NoisyVector, // 加噪后的向量
		"result_details": resultDetails,    // 详细信息（包含噪声规模等）
	}, nil
}

// handleDPAdaptiveClip 处理 /v1/privacy/dp/adaptive_clip 路径，自适应裁剪。
//
// 通过迭代算法自动确定合适的裁剪上下界，
// 无需调用方手动指定 clip_lower/clip_upper。
// 基于目标分位数（target_quantile）和迭代次数（num_iterations）进行自适应调整。
func (m *Mapper) handleDPAdaptiveClip(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 DPAdaptiveClipRequest：values + 自适应参数
	resp, err := client.DPAdaptiveClip(ctx, &pb.DPAdaptiveClipRequest{
		Values:         getFloats(v, "values"),                 // 原始数值数组
		Epsilon:        getFloat64(v, "epsilon", 1.0),          // 隐私预算 ε
		TargetQuantile: getFloat64(v, "target_quantile", 0.95), // 目标分位数（默认 95%）
		NumIterations:  getInt32(v, "num_iterations", 15),      // 迭代次数（默认 15）
		InitialClip:    getFloat64(v, "initial_clip", 10.0),    // 初始裁剪值
	})
	if err != nil {
		return nil, err
	}
	// 返回自适应算法确定的裁剪上下界
	return map[string]float64{
		"clip_lower": resp.ClipLower, // 自适应确定的裁剪下界
		"clip_upper": resp.ClipUpper, // 自适应确定的裁剪上界
	}, nil
}

// handleDPGroupBy 处理 /v1/privacy/dp/groupby 路径，分组差分隐私聚合。
//
// 按指定列分组数据，对每组独立计算差分隐私聚合（count/sum/mean）。
// 结果以 JSON 字符串返回，本函数自动解析为结构化对象。
func (m *Mapper) handleDPGroupBy(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 DPGroupByRequest：rows 为数据，group_col/target_col/agg 指定分组聚合逻辑
	resp, err := client.DPGroupBy(ctx, &pb.DPGroupByRequest{
		Rows:      getRecordEntries(v, "rows"),          // 数据行
		GroupCol:  getString(v, "group_col", ""),        // 分组列名
		TargetCol: getString(v, "target_col", ""),       // 目标列名（聚合对象）
		Agg:       getString(v, "agg", ""),              // 聚合类型（"count"/"sum"/"mean"）
		Epsilon:   getFloat64(v, "epsilon", 1.0),        // 隐私预算 ε
		Delta:     getFloat64(v, "delta", 0.0),          // δ 参数
		Mechanism: getString(v, "mechanism", "laplace"), // 噪声机制
		ClipLower: getFloat64(v, "clip_lower", 0.0),     // 裁剪下界
		ClipUpper: getFloat64(v, "clip_upper", 0.0),     // 裁剪上界
	})
	if err != nil {
		return nil, err
	}
	// 尝试将 result_json 解析为结构化对象，失败则保持原始字符串
	var result any
	if err := json.Unmarshal([]byte(resp.ResultJson), &result); err != nil {
		return map[string]string{"result_json": resp.ResultJson}, nil
	}
	return map[string]any{"result": result}, nil
}
