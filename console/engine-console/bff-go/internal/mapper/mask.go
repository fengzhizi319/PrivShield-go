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
// Health handler —— Health check / 健康检查
// ---------------------------------------------------------------------------

// handleHealth 处理 /v1/privacy/health 路径，调用上游 agent 的 Health RPC。
//
// 执行逻辑：
//  1. 构造空的 HealthRequest（无需参数）
//  2. 调用 client.Health() 获取 agent 状态
//  3. 返回 {"status": "ok", "namespace": "default"} 格式的 map
func (m *Mapper) handleHealth(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	// 调用上游 agent 的 Health RPC，无需请求参数
	resp, err := client.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		return nil, err // gRPC 调用失败，错误向上传递
	}
	// 返回 agent 状态与当前命名空间
	return map[string]string{"status": resp.Status, "namespace": resp.Namespace}, nil
}

// ---------------------------------------------------------------------------
// Masking handlers —— Data masking / 数据脱敏
//
// Common pattern for all masking handlers:
// 所有 masking handler 的共同模式：
//   1. decode(body) parses JSON request body into map[string]any
//      decode(body) 解析 JSON 请求体为 map[string]any
//   2. Use getString/getStringMap helpers to extract fields, construct protobuf request
//      用 getString/getStringMap 等辅助函数提取字段，构造 protobuf 请求
//   3. Call the corresponding gRPC method
//      调用对应的 gRPC 方法
//   4. Convert protobuf response to map and return
//      将 protobuf 响应转换为 map 返回
// ---------------------------------------------------------------------------

// handleMask 处理 /v1/privacy/mask 路径，单字段脱敏。
//
// 前端发送：{"field_name": "email", "value": "alice@example.com", "context": ""}
// 执行逻辑：提取字段名、值、上下文 → 调用 Mask RPC → 返回脱敏结果
func (m *Mapper) handleMask(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	// 解析 JSON 请求体
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 MaskRequest：字段名用于识别 PII 类型，值用于脱敏，上下文可选
	resp, err := client.Mask(ctx, &pb.MaskRequest{
		FieldName: getString(v, "field_name", ""), // 字段名（如 "email"、"phone"）
		Value:     getString(v, "value", ""),      // 待脱敏的值
		Context:   getString(v, "context", ""),    // 可选上下文信息
	})
	if err != nil {
		return nil, err
	}
	// 返回脱敏后的结果（如 "a***@example.com"）
	return map[string]string{"result": resp.Result}, nil
}

// handleMaskRecord 处理 /v1/privacy/mask_record 路径，整条记录脱敏。
//
// 前端发送：{"record": {"name": "Alice", "email": "a@b.com"}, "context": ""}
// 执行逻辑：提取记录 map → 调用 MaskRecord RPC → 返回脱敏后的完整记录
func (m *Mapper) handleMaskRecord(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 MaskRecordRequest：record 为字段名→值的映射
	resp, err := client.MaskRecord(ctx, &pb.MaskRecordRequest{
		Record:  getStringMap(v, "record"),   // 整条记录的字段映射
		Context: getString(v, "context", ""), // 可选上下文
	})
	if err != nil {
		return nil, err
	}
	// 返回脱敏后的记录（字段名→脱敏值的映射）
	return map[string]map[string]string{"result": resp.Result}, nil
}

// handleMaskBatch 处理 /v1/privacy/mask_batch 路径，批量字段脱敏。
//
// 前端发送：{"field_names": ["email","phone"], "values": ["a@b.com","123"], "context": ""}
// 执行逻辑：提取字段名数组和值数组 → 调用 MaskBatch RPC → 返回批量脱敏结果
func (m *Mapper) handleMaskBatch(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 MaskBatchRequest：字段名和值一一对应
	resp, err := client.MaskBatch(ctx, &pb.MaskBatchRequest{
		FieldNames: getStrings(v, "field_names"), // 字段名数组
		Values:     getStrings(v, "values"),      // 值数组（与字段名一一对应）
		Context:    getString(v, "context", ""),  // 可选上下文
	})
	if err != nil {
		return nil, err
	}
	// 返回脱敏结果数组
	return map[string][]string{"results": resp.Results}, nil
}

// handleMaskDataFrame 处理 /v1/privacy/mask_dataframe 路径，DataFrame 级脱敏。
//
// 前端发送：{"data": [{"fields": {"name": "Alice"}}, ...], "columns": ["name"], "context": ""}
// 执行逻辑：提取 RecordEntry 列表 + 列名 → 调用 MaskDataFrame RPC → 返回脱敏后的数据
func (m *Mapper) handleMaskDataFrame(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 MaskDataFrameRequest：data 为多行记录，columns 指定需脱敏的列
	resp, err := client.MaskDataFrame(ctx, &pb.MaskDataFrameRequest{
		Data:    getRecordEntries(v, "data"), // 多行记录（每行包含 fields map）
		Columns: getStrings(v, "columns"),    // 需要脱敏的列名列表
		Context: getString(v, "context", ""), // 可选上下文
	})
	if err != nil {
		return nil, err
	}
	// 返回脱敏后的 RecordEntry 列表
	return map[string][]*pb.RecordEntry{"data": resp.Data}, nil
}

// handleHash 处理 /v1/privacy/hash 路径，HMAC 哈希脱敏。
//
// 前端发送：{"value": "alice@example.com", "salt": "my-salt"}
// 执行逻辑：提取值和盐 → 调用 Hash RPC → 返回哈希结果（不可逆脱敏）
func (m *Mapper) handleHash(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 HashRequest：value 为待哈希值，salt 为 HMAC 盐
	resp, err := client.Hash(ctx, &pb.HashRequest{
		Value: getString(v, "value", ""), // 待哈希的原始值
		Salt:  getString(v, "salt", ""),  // HMAC 盐值（调用方提供）
	})
	if err != nil {
		return nil, err
	}
	// 返回 HMAC 哈希结果（不可逆脱敏）
	return map[string]string{"result": resp.Result}, nil
}
