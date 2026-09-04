package mapper

import (
	// encoding/json：用于 JSON 解析与序列化
	// encoding/json: JSON parsing and serialization
	"encoding/json"
	// fmt：用于格式化错误信息
	// fmt: formats error messages
	"fmt"
	// strings：用于大小写不敏感的参数解析
	// strings: case-insensitive parameter parsing
	"strings"

	// protojson：用于将 protobuf 消息序列化为 JSON（保留原始字段名）
	// protojson: serializes protobuf messages to JSON (preserving original field names)
	"google.golang.org/protobuf/encoding/protojson"
	// proto：提供 proto.Message 接口类型（marshalProto 参数）
	// proto: provides proto.Message interface type (marshalProto parameter)
	"google.golang.org/protobuf/proto"

	// pb：由 proto/privacy.proto 生成的 gRPC 代码（RecordEntry/DoubleChunk/StringChunk 等消息类型）
	// pb: generated gRPC code from proto/privacy.proto (RecordEntry/DoubleChunk/StringChunk message types)
	pb "github.com/fengzhizi319/PrivShield-go/console/bff-go/proto"
)

// ---------------------------------------------------------------------------
// JSON helper functions / JSON 辅助函数
//
// The following functions safely extract various field values from a map[string]any
// produced by json.Unmarshal. Since Go's json.Unmarshal decodes JSON numbers uniformly
// as float64, numeric extraction functions must handle float64/int/int64 simultaneously.
// All functions are "safe extractors": returning default/nil on missing key or type
// mismatch, never panicking.
// 以下函数用于从 json.Unmarshal 后的 map[string]any 中安全地提取各类字段值。
// 由于 Go 的 json.Unmarshal 将 JSON number 统一解码为 float64，
// 因此数值类提取函数需要同时处理 float64/int/int64 三种类型。
// 所有函数均为"安全提取"：字段不存在或类型不匹配时返回默认值/nil，不会 panic。
// ---------------------------------------------------------------------------

// decode 将原始 JSON body 解析为通用的 map[string]any。
//
// 执行逻辑：
//  1. body 为空时返回空 map（不报错），允许无参请求
//  2. 调用 json.Unmarshal 解析为 map[string]any
//  3. 解析失败时返回带上下文的错误信息
//
// 为什么用 map[string]any 而非具体结构体：
//
//	前端发送的 JSON 字段名与 protobuf 字段名一致，但类型可能不完全匹配
//	（如 JSON number → float64），使用通用 map 可灵活处理类型转换
func decode(body json.RawMessage) (map[string]any, error) {
	// body 为空（nil 或 ""）时返回空 map，避免后续 nil 判断
	if len(body) == 0 {
		return map[string]any{}, nil
	}
	var v map[string]any
	// 解析 JSON 到通用 map，json.Number 默认解码为 float64
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	return v, nil
}

// getString 从 map 中安全提取字符串字段，不存在或类型不匹配时返回默认值。
//
// 参数：
//   - m：decode 返回的通用 map
//   - key：要提取的字段名
//   - def：字段不存在或类型不匹配时的默认返回值
func getString(m map[string]any, key, def string) string {
	// 检查 key 是否存在
	if v, ok := m[key]; ok {
		// 类型断言为 string，成功则返回
		if s, ok := v.(string); ok {
			return s
		}
	}
	// 字段不存在或类型不匹配，返回默认值
	return def
}

// getFloat64 从 map 中安全提取数值字段并转为 float64。
//
// 为什么需要处理多种数值类型：
//
//	json.Unmarshal 默认将 JSON number 解码为 float64，
//	但某些场景下 map 中的值可能是 int 或 int64（如手动构造的 map），
//	因此需要同时处理三种情况以确保健壮性。
func getFloat64(m map[string]any, key string, def float64) float64 {
	if v, ok := m[key]; ok {
		// 使用 type switch 处理不同数值类型
		switch n := v.(type) {
		case float64:
			return n // JSON number 的默认类型
		case int:
			return float64(n) // 手动构造的 int
		case int64:
			return float64(n) // 大整数场景
		}
	}
	return def
}

// getInt32 从 map 中安全提取数值字段并转为 int32。
//
// 用于 protobuf 中 int32 类型的字段（如 k、max_depth、num_dummies 等）。
// 注意：float64 → int32 会截断小数部分。
func getInt32(m map[string]any, key string, def int32) int32 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int32(n) // JSON number → int32（截断小数）
		case int:
			return int32(n)
		case int64:
			return int32(n)
		}
	}
	return def
}

// getStrings 从 map 中安全提取字符串数组字段。
//
// 典型用途：提取 field_names、values、categories 等字符串列表。
// 执行逻辑：遍历 []any 数组，仅保留可断言为 string 的元素。
func getStrings(m map[string]any, key string) []string {
	if v, ok := m[key]; ok {
		// JSON 数组解码为 []any
		if arr, ok := v.([]any); ok {
			// 预分配容量以避免多次扩容
			out := make([]string, 0, len(arr))
			for _, item := range arr {
				// 仅保留字符串类型的元素，跳过非字符串
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

// getFloats 从 map 中安全提取 float64 数组字段。
//
// 典型用途：提取 values（差分隐私的数值列表）等浮点数数组。
// 支持 JSON number（float64）和 int/int64 混合数组。
func getFloats(m map[string]any, key string) []float64 {
	if v, ok := m[key]; ok {
		if arr, ok := v.([]any); ok {
			out := make([]float64, 0, len(arr))
			for _, item := range arr {
				// 逐个元素按类型转换为 float64
				switch n := item.(type) {
				case float64:
					out = append(out, n)
				case int:
					out = append(out, float64(n))
				case int64:
					out = append(out, float64(n))
				}
			}
			return out
		}
	}
	return nil
}

// getIntSlice 从 map 中安全提取 int32 数组字段。
//
// 典型用途：提取 LDP 二进制扰动中的 values（0/1 整数数组）。
func getIntSlice(m map[string]any, key string) []int32 {
	if v, ok := m[key]; ok {
		if arr, ok := v.([]any); ok {
			out := make([]int32, 0, len(arr))
			for _, item := range arr {
				// 逐个元素按类型转换为 int32
				switch n := item.(type) {
				case float64:
					out = append(out, int32(n))
				case int:
					out = append(out, int32(n))
				case int64:
					out = append(out, int32(n))
				}
			}
			return out
		}
	}
	return nil
}

// getStringMap 从 map 中安全提取 map[string]string 字段。
//
// 典型用途：提取 record（脱敏用的字段名→值映射）。
// JSON object 解码为 map[string]any，需逐个转换值为 string。
func getStringMap(m map[string]any, key string) map[string]string {
	if v, ok := m[key]; ok {
		// JSON object 解码为 map[string]any
		if mm, ok := v.(map[string]any); ok {
			// 预分配容量
			out := make(map[string]string, len(mm))
			for k, val := range mm {
				// 仅保留值为字符串的键值对
				if s, ok := val.(string); ok {
					out[k] = s
				}
			}
			return out
		}
	}
	return nil
}

// getRecordEntries 从 map 中提取 RecordEntry 列表（protobuf 消息数组）。
//
// 前端发送的 JSON 格式：
//
//	{ "data": [{"fields": {"name": "Alice", "email": "a@b.com"}}, ...] }
//
// 执行逻辑：
//  1. 提取 key 对应的 []any 数组
//  2. 遍历每个元素，找到 "fields" 子对象
//  3. 将 "fields" 内的 string 值提取为 map[string]string
//  4. 构造 pb.RecordEntry{Fields: map[string]string}
//
// 典型用途：mask_dataframe、k_anonymize/table、dyn_classify 等表格类接口
func getRecordEntries(m map[string]any, key string) []*pb.RecordEntry {
	if v, ok := m[key]; ok {
		if arr, ok := v.([]any); ok {
			out := make([]*pb.RecordEntry, 0, len(arr))
			for _, item := range arr {
				// 每个数组元素应为一个 map（对应一条记录）
				if mm, ok := item.(map[string]any); ok {
					fields := make(map[string]string)
					for k, val := range mm {
						// 只处理 "fields" 键，其值为字段名→字段值的映射
						if k == "fields" {
							if fmap, ok := val.(map[string]any); ok {
								for fk, fv := range fmap {
									if fs, ok := fv.(string); ok {
										fields[fk] = fs
									}
								}
							}
						}
					}
					// 构造 protobuf RecordEntry 消息
					out = append(out, &pb.RecordEntry{Fields: fields})
				}
			}
			return out
		}
	}
	return nil
}

// getRecordEntry 从 map 中提取单个 RecordEntry（protobuf 消息）。
//
// 与 getRecordEntries 类似，但仅提取单个记录而非数组。
// 典型用途：dyn_classify/record 等单记录分类接口
func getRecordEntry(m map[string]any, key string) *pb.RecordEntry {
	if v, ok := m[key]; ok {
		if mm, ok := v.(map[string]any); ok {
			fields := make(map[string]string)
			for k, val := range mm {
				// 只处理 "fields" 键
				if k == "fields" {
					if fmap, ok := val.(map[string]any); ok {
						for fk, fv := range fmap {
							if fs, ok := fv.(string); ok {
								fields[fk] = fs
							}
						}
					}
				}
			}
			return &pb.RecordEntry{Fields: fields}
		}
	}
	return nil
}

// getBool 从 map 中安全提取布尔字段，不存在或类型不匹配时返回默认值。
//
// 典型用途：提取 mask_input（复核导出时是否脱敏输入）等布尔开关。
func getBool(m map[string]any, key string, def bool) bool {
	if v, ok := m[key]; ok {
		// 类型断言为 bool
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// getStringMapFromMap 从嵌套 map 中提取 map[string]string。
//
// 功能与 getStringMap 完全一致，保留为历史兼容。
func getStringMapFromMap(m map[string]any, key string) map[string]string {
	if v, ok := m[key]; ok {
		if mm, ok := v.(map[string]any); ok {
			out := make(map[string]string, len(mm))
			for k, val := range mm {
				if s, ok := val.(string); ok {
					out[k] = s
				}
			}
			return out
		}
	}
	return nil
}

// getDoubleChunks 从 map 中提取 DoubleChunk 列表（分块浮点数数据）。
//
// 前端发送的 JSON 格式：
//
//	{ "chunks": [{"values": [1.0, 2.0, 3.0]}, {"values": [4.0, 5.0]}] }
//
// 每个 chunk 包含一个 values 浮点数组，用于分块 DP 计算（chunked_count/sum/mean）。
func getDoubleChunks(m map[string]any, key string) []*pb.DoubleChunk {
	if v, ok := m[key]; ok {
		if arr, ok := v.([]any); ok {
			out := make([]*pb.DoubleChunk, 0, len(arr))
			for _, item := range arr {
				// 每个元素应为 {"values": [...]} 格式的 map
				if mm, ok := item.(map[string]any); ok {
					// 调用 getFloats 提取 "values" 数组
					out = append(out, &pb.DoubleChunk{Values: getFloats(mm, "values")})
				}
			}
			return out
		}
	}
	return nil
}

// getStringChunks 从 map 中提取 StringChunk 列表（分块字符串数据）。
//
// 前端发送的 JSON 格式：
//
//	{ "chunks": [{"values": ["a", "b"]}, {"values": ["c"]}] }
//
// 用于分块直方图计算（chunked_histogram），每个 chunk 包含分类值数组。
func getStringChunks(m map[string]any, key string) []*pb.StringChunk {
	if v, ok := m[key]; ok {
		if arr, ok := v.([]any); ok {
			out := make([]*pb.StringChunk, 0, len(arr))
			for _, item := range arr {
				if mm, ok := item.(map[string]any); ok {
					// 调用 getStrings 提取 "values" 字符串数组
					out = append(out, &pb.StringChunk{Values: getStrings(mm, "values")})
				}
			}
			return out
		}
	}
	return nil
}

// getVectorEntries 从 map 中提取 DoubleChunk 列表（向量数据）。
//
// 前端发送的 JSON 格式：
//
//	{ "vectors": [{"values": [1.0, 2.0]}, {"values": [3.0, 4.0]}] }
//
// 功能与 getDoubleChunks 相同，但语义上用于向量类 RPC（如 DPVectorSum）。
// 每个 DoubleChunk 代表一个向量（多维浮点数组）。
func getVectorEntries(m map[string]any, key string) []*pb.DoubleChunk {
	if v, ok := m[key]; ok {
		if arr, ok := v.([]any); ok {
			out := make([]*pb.DoubleChunk, 0, len(arr))
			for _, item := range arr {
				if mm, ok := item.(map[string]any); ok {
					out = append(out, &pb.DoubleChunk{Values: getFloats(mm, "values")})
				}
			}
			return out
		}
	}
	return nil
}

// marshalProto 将 protobuf 消息转换为 JSON 可序列化的 Go 值。
//
// 执行逻辑：
//  1. 使用 protojson.Marshal 将 protobuf 消息序列化为 JSON 字节流
//     - UseProtoNames: true 保持字段名为 protobuf 原始名称（如 "field_name" 而非 "fieldName"）
//  2. 使用 json.Unmarshal 将 JSON 字节流解析为 Go 的 any 类型
//  3. 返回 any 类型的值，可直接被 gin.JSON 序列化返回前端
//
// 为什么需要两步转换：
//
//	protojson 输出的是 []byte，而 gin.JSON 需要 any 类型，
//	中间经过 JSON 解析为 Go 原生类型（map/slice/string/number/bool）
func marshalProto(msg proto.Message) (any, error) {
	// 第一步：protobuf → JSON 字节流（使用原始字段名）
	b, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(msg)
	if err != nil {
		return nil, err
	}
	// 第二步：JSON 字节流 → Go 原生类型
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// extractJSONField 解析 map 中指定字段的 JSON 字符串，将其替换为结构化对象。
//
// 某些 RPC 的响应中包含 "result_json" 字段，其值为 JSON 编码的字符串，
// 如 `{"result_json": "{\"label\": \"email\", \"confidence\": 0.95}"}`。
// 本函数将该字符串解析为结构化对象，使前端收到的是嵌套 JSON 而非转义字符串。
//
// 执行逻辑：
//  1. 将 v 断言为 map[string]any
//  2. 提取指定 field 的字符串值
//  3. 尝试 json.Unmarshal 解析为 any
//  4. 成功则替换原字段值，失败则保持原样
func extractJSONField(v any, field string) any {
	// 尝试将 v 断言为 map，非 map 类型直接返回
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	// 提取目标字段的字符串值
	raw, ok := m[field].(string)
	// 字段不存在、非字符串或为空时直接返回
	if !ok || raw == "" {
		return v
	}
	// 尝试解析 JSON 字符串为结构化对象
	var parsed any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		// 解析失败时保持原字符串不变
		return v
	}
	// 用解析后的结构化对象替换原 JSON 字符串
	m[field] = parsed
	return m
}

// ---------------------------------------------------------------------------
// Utility functions / 工具函数
// ---------------------------------------------------------------------------

// lower 将字符串转为小写并去除首尾空白。
//
// 用于大小写不敏感的参数解析（如 mechanism、format 等枚举值）。
func lower(s string) string {
	// TrimSpace 去除首尾空白，ToLower 转为小写
	return strings.ToLower(strings.TrimSpace(s))
}

// 确保 protojson 包被引用，避免未使用导入的编译错误。
// 即使所有 handler 均不使用 protojson.MarshalOptions{}，
// 该变量声明也能保证 import 不会报错。
var _ = protojson.MarshalOptions{}
