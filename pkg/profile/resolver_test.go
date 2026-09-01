// Package profile 测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证隐私 Profile 参数解析器（Resolver）与校验器（Validate）的核心功能：
//  1. 【Resolver 默认参数】：验证 DP 原语的默认参数（epsilon=1.0, mechanism=laplace）；
//  2. 【Resolver 参数覆盖】：验证调用方传入参数能覆盖默认值；
//  3. 【Resolver 未知原语】：验证未知原语返回空参数表（fail-closed 安全语义）；
//  4. 【Resolver YAML 加载】：验证从 YAML 文件加载自定义 Profile 并正确覆盖默认参数；
//  5. 【Resolver 推荐逻辑】：验证 Profile 推荐返回 standard 标准配置；
//  6. 【Validate DP 参数校验】：验证 epsilon 必须 > 0，负值被拒绝；
//  7. 【Validate K-匿名参数校验】：验证 k 必须 >= 2，k=1 被拒绝；
//  8. 【Validate 未知原语】：验证未知原语校验不报错（宽容策略）。
// ==============================================================================

package profile

import (
	"os"
	"path/filepath"
	"testing"
)

// ──────────────────────────────────────────────
// 1. Resolver 参数解析测试
// ──────────────────────────────────────────────

// TestResolver_Defaults 验证 DP 原语的默认参数解析。
// 执行逻辑：创建 Resolver 后调用 Resolve("dp", "", nil)，
// 断言默认 epsilon=1.0、mechanism=laplace。
func TestResolver_Defaults(t *testing.T) {
	r := NewResolver()

	params := r.Resolve("dp", "", nil)
	if params["epsilon"] != 1.0 {
		t.Errorf("expected epsilon 1.0, got %v", params["epsilon"])
	}
	if params["mechanism"] != "laplace" {
		t.Errorf("expected mechanism laplace, got %v", params["mechanism"])
	}
}

// TestResolver_Override 验证调用方传入参数能覆盖默认值。
// 执行逻辑：创建 Resolver 后传入 epsilon=2.0 覆盖参数，
// 断言 Resolve 返回的 epsilon 为 2.0 而非默认的 1.0。
func TestResolver_Override(t *testing.T) {
	r := NewResolver()

	params := r.Resolve("dp", "", map[string]interface{}{
		"epsilon": 2.0,
	})
	if params["epsilon"] != 2.0 {
		t.Errorf("expected epsilon 2.0 after override, got %v", params["epsilon"])
	}
}

// TestResolver_UnknownPrimitive 验证未知原语返回空参数表（fail-closed）。
// 执行逻辑：调用 Resolve("unknown", "", nil)，断言返回空 map，不 panic 也不泄露默认参数。
func TestResolver_UnknownPrimitive(t *testing.T) {
	r := NewResolver()

	params := r.Resolve("unknown", "", nil)
	if len(params) != 0 {
		t.Errorf("expected empty params for unknown primitive, got %v", params)
	}
}

// TestResolver_LoadFromYAML 验证从 YAML 文件加载自定义 Profile。
// 执行逻辑：在临时目录写入包含 dp（epsilon=0.5, gaussian）和 k_anonymity（k=10）的 YAML，
// 调用 LoadFromYAML 加载后断言 Resolve("dp") 返回 YAML 中定义的参数。
func TestResolver_LoadFromYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "profile.yaml")
	content := `name: "custom"
version: "2.0"
defaults:
  dp:
    epsilon: 0.5
    mechanism: "gaussian"
  k_anonymity:
    k: 10
`
	if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewResolver()
	if err := r.LoadFromYAML(yamlPath); err != nil {
		t.Fatal(err)
	}

	params := r.Resolve("dp", "", nil)
	if params["epsilon"] != 0.5 {
		t.Errorf("expected epsilon 0.5 from YAML, got %v", params["epsilon"])
	}
	if params["mechanism"] != "gaussian" {
		t.Errorf("expected mechanism gaussian, got %v", params["mechanism"])
	}
}

// TestResolver_Recommend 验证 Profile 推荐逻辑。
// 执行逻辑：调用 Recommend()，断言返回的推荐配置包含 recommended_profile=standard 和 epsilon=1.0。
func TestResolver_Recommend(t *testing.T) {
	r := NewResolver()
	rec := r.Recommend()
	if rec["recommended_profile"] != "standard" {
		t.Errorf("expected standard profile, got %v", rec["recommended_profile"])
	}
	if rec["epsilon"] != 1.0 {
		t.Errorf("expected epsilon 1.0, got %v", rec["epsilon"])
	}
}

// ──────────────────────────────────────────────
// 2. 参数校验器测试
// ──────────────────────────────────────────────

// TestValidate_DP 验证差分隐私参数校验规则。
// 执行逻辑：epsilon=1.0 通过校验；epsilon=-1.0 被拒绝（epsilon 必须 > 0）。
func TestValidate_DP(t *testing.T) {
	if err := Validate("dp", map[string]interface{}{"epsilon": 1.0}); err != nil {
		t.Errorf("expected valid, got error: %v", err)
	}
	if err := Validate("dp", map[string]interface{}{"epsilon": -1.0}); err == nil {
		t.Error("expected error for negative epsilon")
	}
}

// TestValidate_KAnonymity 验证 K-匿名参数校验规则。
// 执行逻辑：k=5 通过校验；k=1 被拒绝（k 必须 >= 2，k=1 无匿名效果）。
func TestValidate_KAnonymity(t *testing.T) {
	if err := Validate("k_anonymity", map[string]interface{}{"k": 5}); err != nil {
		t.Errorf("expected valid, got error: %v", err)
	}
	if err := Validate("k_anonymity", map[string]interface{}{"k": 1}); err == nil {
		t.Error("expected error for k < 2")
	}
}

// TestValidate_Unknown 验证未知原语的校验策略。
// 执行逻辑：调用 Validate("unknown", nil)，断言不报错（对未知原语采用宽容策略）。
func TestValidate_Unknown(t *testing.T) {
	if err := Validate("unknown", nil); err != nil {
		t.Errorf("expected no error for unknown primitive, got: %v", err)
	}
}
