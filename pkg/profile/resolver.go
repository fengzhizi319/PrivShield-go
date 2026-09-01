// Package profile — 隐私参数解析与校验模块。
//
// 对齐 Python engine/privacy/profile.py：
// 从 YAML 配置文件加载各隐私原语的默认参数，支持请求级参数覆盖。
// 提供参数校验能力，确保 DP epsilon 为正、K-Anonymity k 不小于 2 等。
package profile

import (
	"fmt"
	"os"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

// PrimitiveParams 隐私原语默认参数集合，以 key-value 形式存储各原语的可调参数。
// 例如 DP 原语可能包含 "epsilon": 1.0, "delta": 0.0, "mechanism": "laplace"。
type PrimitiveParams map[string]interface{}

// PrivacyProfile 隐私参数配置，对应一个完整的 YAML 配置文件。
// 支持全局默认参数与各命名空间（租户）级个性化参数两层覆盖。
type PrivacyProfile struct {
	Name       string                     `yaml:"name"`       // Profile 名称（如 "standard", "medical"）
	Version    string                     `yaml:"version"`    // 配置版本号
	Defaults   map[string]PrimitiveParams `yaml:"defaults"`   // 全局默认参数：primitive → params
	Namespaces map[string]PrimitiveParams `yaml:"namespaces"` // 命名空间级个性化参数：namespace → primitive → params
}

// Resolver 隐私参数解析器，线程安全地管理当前生效的 PrivacyProfile。
// 支持运行时通过 LoadFromYAML 热重载配置，读路径使用 RLock 保证高并发。
type Resolver struct {
	mu      sync.RWMutex    // 读写锁：保护 profile 指针的并发安全
	profile *PrivacyProfile // 当前生效的隐私参数配置
}

// NewResolver 创建参数解析器。
func NewResolver() *Resolver {
	return &Resolver{
		profile: defaultProfile(),
	}
}

// LoadFromYAML 从 YAML 文件加载并原子替换隐私参数配置。
//
// 执行逻辑：
// 1. 读取指定路径的 YAML 文件内容；
// 2. 反序列化为 PrivacyProfile 结构体；
// 3. 获取写锁后原子替换 r.profile 指针，保证读路径无阻塞。
func (r *Resolver) LoadFromYAML(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read profile: %w", err)
	}
	var p PrivacyProfile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("parse profile YAML: %w", err)
	}
	r.mu.Lock()
	r.profile = &p
	r.mu.Unlock()
	return nil
}

// Resolve 解析指定原语的参数，支持请求级覆盖。
// 优先级：请求参数 > 命名空间参数 > 全局默认 > 内置默认。
func (r *Resolver) Resolve(primitive string, namespace string, overrides map[string]interface{}) map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 1. 内置默认
	result := builtinDefaults(primitive)

	// 2. 全局默认覆盖
	if r.profile != nil && r.profile.Defaults != nil {
		if defaults, ok := r.profile.Defaults[primitive]; ok {
			for k, v := range defaults {
				result[k] = v
			}
		}
	}

	// 3. 命名空间参数覆盖
	if namespace != "" && r.profile != nil && r.profile.Namespaces != nil {
		if nsParams, ok := r.profile.Namespaces[namespace]; ok {
			if v, exists := nsParams[primitive]; exists {
				if m, ok := v.(map[string]interface{}); ok {
					for k, val := range m {
						result[k] = val
					}
				}
			}
		}
	}

	// 4. 请求级覆盖
	for k, v := range overrides {
		result[k] = v
	}

	return result
}

// Recommend 返回当前 Profile 下的静态隐私参数推荐值。
// 返回结果包含推荐 Profile 名称、DP 参数（epsilon/delta/mechanism）与 K-Anonymity k 值。
func (r *Resolver) Recommend() map[string]interface{} {
	return map[string]interface{}{
		"recommended_profile": r.profileName(),
		"epsilon":             1.0,
		"delta":               1e-5,
		"k":                   5,
		"mechanism":           "laplace",
		"note":                "Go 引擎参数推荐",
	}
}

// RecommendDataParams 根据输入样本数据特征自动计算并推荐 DP 与 K-Anonymity 最佳隐私参数。
//
// 执行逻辑：
//  1. 【DP 参数推荐】：对数值型样本计算 5%~95% 分位数作为自适应截断区间 [clip_lower, clip_upper]，
//     并根据样本量 n 动态调整 delta = min(1e-5, 1/(10n²))；
//  2. 【K-Anonymity 参数推荐】：按行数 n/10 估算 k 值，限制在 [2, 10] 安全区间内；
//  3. 将推荐结果保存至命名空间级个性化参数，后续 Resolve 调用自动生效。
func (r *Resolver) RecommendDataParams(namespace string, values []float64, rows []map[string]interface{}, qiCols []string) map[string]interface{} {
	recommendations := make(map[string]interface{})

	// 1. 推荐 DP 参数
	if len(values) > 0 {
		n := len(values)
		sorted := make([]float64, n)
		copy(sorted, values)
		sort.Float64s(sorted)

		p5Idx := int(float64(n) * 0.05)
		p95Idx := int(float64(n) * 0.95)
		if p95Idx >= n {
			p95Idx = n - 1
		}
		clipLower := sorted[p5Idx]
		clipUpper := sorted[p95Idx]
		if clipLower == clipUpper {
			clipLower -= 1.0
			clipUpper += 1.0
		}

		delta := 1e-5
		if n > 0 {
			fn := float64(n)
			calcDelta := 1.0 / (10.0 * fn * fn)
			if calcDelta < delta {
				delta = calcDelta
			}
		}

		dpParams := map[string]interface{}{
			"epsilon":    1.0,
			"delta":      delta,
			"mechanism":  "laplace",
			"clip_lower": clipLower,
			"clip_upper": clipUpper,
		}
		recommendations["dp"] = dpParams
		r.SavePersonalizedParams(namespace, "dp", dpParams)
	}

	// 2. 推荐 K-Anonymity 参数
	if len(rows) > 0 {
		n := len(rows)
		k := n / 10
		if k < 2 {
			k = 2
		}
		if k > 10 {
			k = 10
		}
		kanoParams := map[string]interface{}{
			"k":         k,
			"max_depth": 10,
		}
		recommendations["k_anonymity"] = kanoParams
		r.SavePersonalizedParams(namespace, "k_anonymity", kanoParams)
	}

	if len(recommendations) == 0 {
		recommendations["default"] = r.Recommend()
	}

	return recommendations
}

// SavePersonalizedParams 将指定原语的个性化参数保存至命名空间级配置。
//
// 执行逻辑：
// 1. 校验 namespace 与 primitive 非空；
// 2. 获取写锁，惰性初始化 profile 与 Namespaces 映射；
// 3. 将 params 合并写入对应命名空间的原语参数中（增量覆盖，不删除已有键）。
func (r *Resolver) SavePersonalizedParams(namespace, primitive string, params map[string]interface{}) {
	if namespace == "" || primitive == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.profile == nil {
		r.profile = defaultProfile()
	}
	if r.profile.Namespaces == nil {
		r.profile.Namespaces = make(map[string]PrimitiveParams)
	}
	if _, ok := r.profile.Namespaces[namespace]; !ok {
		r.profile.Namespaces[namespace] = make(PrimitiveParams)
	}

	m, ok := r.profile.Namespaces[namespace][primitive].(map[string]interface{})
	if !ok {
		m = make(map[string]interface{})
	}
	for k, v := range params {
		m[k] = v
	}
	r.profile.Namespaces[namespace][primitive] = m
}

// profileName 返回当前 Profile 名称，未配置时回退为 "standard"。
func (r *Resolver) profileName() string {
	if r.profile != nil && r.profile.Name != "" {
		return r.profile.Name
	}
	return "standard"
}

// Validate 校验隐私原语参数的合法性与安全性。
//
// 校验规则：
//   - dp：epsilon 必须为正数（> 0），delta 必须非负（>= 0）；
//   - k_anonymity：k 必须 >= 2（k=1 等价于无保护）。
//
// 支持 int 与 float64 两种 k 值类型（YAML 反序列化可能产生任一类型）。
func Validate(primitive string, params map[string]interface{}) error {
	switch primitive {
	case "dp":
		if eps, ok := params["epsilon"].(float64); ok && eps <= 0 {
			return fmt.Errorf("dp epsilon must be positive, got %f", eps)
		}
		if delta, ok := params["delta"].(float64); ok && delta < 0 {
			return fmt.Errorf("dp delta must be non-negative, got %f", delta)
		}
	case "k_anonymity":
		if k, ok := params["k"].(int); ok && k < 2 {
			return fmt.Errorf("k_anonymity k must be >= 2, got %d", k)
		}
		if kf, ok := params["k"].(float64); ok && kf < 2 {
			return fmt.Errorf("k_anonymity k must be >= 2, got %f", kf)
		}
	}
	return nil
}

// ──────────────────────────────────────────────
// 内置默认参数
// ──────────────────────────────────────────────

// defaultProfile 构造内置默认隐私参数配置（"standard" Profile v1.0）。
// 当 YAML 配置文件不存在或解析失败时作为兜底默认值。
func defaultProfile() *PrivacyProfile {
	return &PrivacyProfile{
		Name:    "standard",
		Version: "1.0",
		Defaults: map[string]PrimitiveParams{
			"dp":           {"epsilon": 1.0, "delta": 0.0, "mechanism": "laplace"},
			"k_anonymity":  {"k": 5, "l": 2, "t": 0.2, "max_depth": 10},
			"sanitization": {"engine": "mask"},
			"qol": {
				"num_dummies": 3,
			},
			"classification": {
				"confidence_threshold": 0.75,
			},
		},
	}
}

// builtinDefaults 返回指定原语的内置默认参数副本（深拷贝，调用方修改不影响全局）。
// 未找到对应原语时返回空 map。
func builtinDefaults(primitive string) map[string]interface{} {
	defaults := map[string]map[string]interface{}{
		"dp":             {"epsilon": 1.0, "delta": 0.0, "mechanism": "laplace"},
		"k_anonymity":    {"k": 5, "l": 2, "t": 0.2, "max_depth": 10},
		"sanitization":   {"engine": "mask"},
		"qol":            {"num_dummies": 3},
		"classification": {"confidence_threshold": 0.75},
	}
	if d, ok := defaults[primitive]; ok {
		result := make(map[string]interface{}, len(d))
		for k, v := range d {
			result[k] = v
		}
		return result
	}
	return make(map[string]interface{})
}
