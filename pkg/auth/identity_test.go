// Package auth 测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证 Package auth（身份认证与权限映射）的核心功能：
//  1. 【Identity.HasPermission 权限判定】：验证通配符 "*"、精确匹配、多 Scope 列表、空 Scope 等场景下的权限校验逻辑；
//  2. 【PermissionForRESTPath 路径→权限映射】：验证所有 REST 端点（健康探活、隐私原语、Agent 处理、运维诊断、pprof）
//     到权限字符串的映射覆盖完整性，未知路径返回空串（fail-closed）；
//  3. 【PermissionForGRPCMethod 方法→权限映射】：验证 gRPC 全限定方法名到权限字符串的映射，未知方法返回空串；
//  4. 【IsHealthPathOrMethod 探活识别】：验证 REST 探活路径（/health、/livez、/readyz）与 gRPC Health 方法的统一识别。
// ==============================================================================

package auth

import "testing"

// ──────────────────────────────────────────────
// 1. Identity 权限判定测试
// ──────────────────────────────────────────────

// TestIdentity_HasPermission 验证 Identity 结构体的 Scope 权限判定逻辑。
// 执行逻辑：构造不同 Scope 组合的 Identity（通配符、精确匹配、无匹配、空 Scope、多 Scope），
// 断言 HasPermission 对目标权限的判定结果与预期一致。
func TestIdentity_HasPermission(t *testing.T) {
	tests := []struct {
		name       string
		identity   Identity
		permission string
		want       bool
	}{
		{"wildcard", Identity{Scopes: []string{"*"}}, "privacy:mask", true},
		{"exact match", Identity{Scopes: []string{"privacy:mask"}}, "privacy:mask", true},
		{"no match", Identity{Scopes: []string{"privacy:dp"}}, "privacy:mask", false},
		{"empty scopes", Identity{Scopes: []string{}}, "privacy:mask", false},
		{"multi scopes", Identity{Scopes: []string{"privacy:dp", "privacy:mask"}}, "privacy:mask", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.identity.HasPermission(tt.permission); got != tt.want {
				t.Errorf("HasPermission(%q) = %v, want %v", tt.permission, got, tt.want)
			}
		})
	}
}

// ──────────────────────────────────────────────
// 2. REST 路径→权限映射测试
// ──────────────────────────────────────────────

// TestPermissionForRESTPath 验证 REST 路径到权限字符串的映射覆盖所有端点类型。
// 执行逻辑：遍历健康探活（/health、/livez、/readyz）、隐私原语（mask/dp/kano/qol/budget）、
// Agent 处理、运维诊断、pprof 管理端及未知路径，断言每条路径映射到正确的权限字符串，
// 未知路径返回空串（fail-closed 安全语义）。
func TestPermissionForRESTPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		// 健康探活
		{"/health", "health:read"},
		{"/livez", "health:read"},
		{"/readyz", "health:read"},
		// /v1/* 主路由
		{"/v1/privacy/mask", "privacy:mask"},
		{"/v1/privacy/mask/record", "privacy:mask"},
		{"/v1/privacy/dp/count", "privacy:dp"},
		{"/v1/privacy/ldp/randomized_response", "privacy:dp"},
		{"/v1/privacy/k_anonymize", "privacy:kano"},
		{"/v1/privacy/qol/obfuscate", "privacy:qol"},
		{"/v1/privacy/budget", "privacy:budget"},
		{"/v1/privacy/budget/reset", "privacy:budget"},
		{"/v1/privacy/hash", "privacy:hash"},
		{"/v1/privacy/profile/recommend", "privacy:profile"},
		{"/v1/privacy/process_file", "privacy:mask"},
		{"/v1/privacy/classify/field", "classification:read"},
		{"/v1/agent/process", "agent:process"},
		{"/v1/medical/process", "medical:process"},
		{"/v1/ops/diagnostics", "ops:diagnostics"},
		{"/v1/dynclassification/classify", "dynclassification:read"},
		{"/v1/dynclassification/classify/batch", "dynclassification:read"},
		{"/v1/dynclassification/eval_record", "dynclassification:read"},
		{"/v1/dynclassification/profiles/reload", "dynclassification:write"},
		// 根路径直调别名
		{"/agent/process", "agent:process"},
		{"/medical/process", "medical:process"},
		{"/ops/diagnostics", "ops:diagnostics"},
		{"/privacy/process_file", "privacy:mask"},
		// /v1/* 规范路径补充覆盖
		{"/v1/privacy/k_anonymize/table", "privacy:kano"},
		{"/v1/privacy/k_anonymize/dataframe", "privacy:kano"},
		{"/v1/medical/sanitize", "medical:process"},
		{"/v1/medical/sanitize/batch", "medical:process"},
		// pprof
		{"/debug/pprof", "ops:admin"},
		{"/debug/pprof/heap", "ops:admin"},
		// 未知路径（fail-closed：默认归入 admin 权限）
		{"/unknown", "admin"},
		{"/api/v2/something", "admin"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := PermissionForRESTPath(tt.path); got != tt.want {
				t.Errorf("PermissionForRESTPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// ──────────────────────────────────────────────
// 3. gRPC 方法→权限映射测试
// ──────────────────────────────────────────────

// TestPermissionForGRPCMethod 验证 gRPC 全限定方法名到权限字符串的映射。
// 执行逻辑：覆盖 Mask、DPCount、Health 等已知方法及 Unknown 未知方法，
// 断言已知方法映射到正确权限，未知方法返回 admin（fail-closed）。
func TestPermissionForGRPCMethod(t *testing.T) {
	tests := []struct {
		method string
		want   string
	}{
		{"privacy.local.PrivacyService/Mask", "privacy:mask"},
		{"privacy.local.PrivacyService/DPCount", "privacy:dp"},
		{"privacy.local.PrivacyService/Health", "health:read"},
		{"privacy.local.PrivacyService/Unknown", "admin"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			if got := PermissionForGRPCMethod(tt.method); got != tt.want {
				t.Errorf("PermissionForGRPCMethod(%q) = %q, want %q", tt.method, got, tt.want)
			}
		})
	}
}

// ──────────────────────────────────────────────
// 4. 健康探活路径/方法识别测试
// ──────────────────────────────────────────────

// TestIsHealthPathOrMethod 验证健康探活路径与 gRPC Health 方法的统一识别。
// 执行逻辑：覆盖 /health、/livez、/readyz、/readyz/llm 等 REST 探活路径，
// 以及 gRPC Health 方法名，断言返回 true；业务路径（/v1/privacy/mask）返回 false。
func TestIsHealthPathOrMethod(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/health", true},
		{"/livez", true},
		{"/readyz", true},
		{"/readyz/llm", true},
		{"privacy.local.PrivacyService/Health", true},
		{"/v1/privacy/mask", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsHealthPathOrMethod(tt.path); got != tt.want {
				t.Errorf("IsHealthPathOrMethod(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// ──────────────────────────────────────────────
// 5. service-hub 路由→权限映射测试
// ──────────────────────────────────────────────

func TestServiceHubPermissionForPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/health", ""},
		{"/readyz", ""},
		{"/api/health", ""},
		{"/metrics", ""},
		{"/api/hub/status", "hub:read"},
		{"/api/hub/tasks", "hub:read"},
		{"/api/hub/tasks/abc-123", "hub:read"},
		{"/api/hub/pipeline", "hub:read"},
		{"/api/hub/dispatch", "hub:dispatch"},
		{"/api/hub/dispatch/", "hub:dispatch"}, // 尾部斜杠不应绕过 Scope 校验
		{"/api/hub/classify", "hub:dispatch"},
		{"/api/hub/classify/", "hub:dispatch"}, // 尾部斜杠不应绕过 Scope 校验
		{"/api/hub/tasks/", "hub:read"},        // 尾部斜杠归一化
		{"/api/hub/topology", "hub:read"},
		{"/api/hub/audit/logs", "hub:read"},
		{"/api/hub/audit/verify", "hub:dispatch"},
		{"/api/hub/datasources", "hub:read"},
		{"/api/hub/fetch-and-desensitize", "hub:dispatch"},
		{"/unknown", "admin"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := ServiceHubPermissionForPath(tt.path); got != tt.want {
				t.Errorf("ServiceHubPermissionForPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// ──────────────────────────────────────────────
// 6. API Key 解析测试
// ──────────────────────────────────────────────

func TestParseAPIKeysEnv(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantLen int
		check   func(t *testing.T, keys map[string]*KeyConfig)
	}{
		{
			name:    "empty",
			raw:     "",
			wantLen: 0,
		},
		{
			name:    "single key with scopes",
			raw:     "tok1:svc-a:privacy:mask,privacy:dp",
			wantLen: 1,
			check: func(t *testing.T, keys map[string]*KeyConfig) {
				k := keys["tok1"]
				if k == nil {
					t.Fatal("expected tok1")
				}
				if k.Name != "svc-a" {
					t.Errorf("name = %q, want svc-a", k.Name)
				}
				if len(k.Scopes) != 2 || k.Scopes[0] != "privacy:mask" || k.Scopes[1] != "privacy:dp" {
					t.Errorf("scopes = %v, want [privacy:mask, privacy:dp]", k.Scopes)
				}
			},
		},
		{
			name:    "multiple keys",
			raw:     "tok1:a:*;tok2:b:health:read",
			wantLen: 2,
			check: func(t *testing.T, keys map[string]*KeyConfig) {
				if keys["tok1"].Scopes[0] != "*" {
					t.Errorf("tok1 scopes = %v, want [*]", keys["tok1"].Scopes)
				}
				if keys["tok2"].Name != "b" {
					t.Errorf("tok2 name = %q, want b", keys["tok2"].Name)
				}
			},
		},
		{
			name:    "no scopes defaults to empty",
			raw:     "tok1:svc-a",
			wantLen: 1,
			check: func(t *testing.T, keys map[string]*KeyConfig) {
				if len(keys["tok1"].Scopes) != 0 {
					t.Errorf("scopes = %v, want [] (empty, no permissions)", keys["tok1"].Scopes)
				}
			},
		},
		{
			name:    "trims spaces around token name and scopes",
			raw:     " tok1 : svc-a : privacy:mask , health:read ; tok2 : svc-b : hub:read ",
			wantLen: 2,
			check: func(t *testing.T, keys map[string]*KeyConfig) {
				k1 := keys["tok1"]
				if k1 == nil || k1.Name != "svc-a" || len(k1.Scopes) != 2 || k1.Scopes[0] != "privacy:mask" || k1.Scopes[1] != "health:read" {
					t.Errorf("tok1 config = %+v, want svc-a with [privacy:mask health:read]", k1)
				}
				k2 := keys["tok2"]
				if k2 == nil || k2.Name != "svc-b" || len(k2.Scopes) != 1 || k2.Scopes[0] != "hub:read" {
					t.Errorf("tok2 config = %+v, want svc-b with [hub:read]", k2)
				}
			},
		},
		{
			name:    "drops empty token or empty name entries",
			raw:     ":svc-a:health:read;tok1::privacy:mask;tok2:svc-b:hub:read",
			wantLen: 1,
			check: func(t *testing.T, keys map[string]*KeyConfig) {
				k := keys["tok2"]
				if k == nil || k.Name != "svc-b" || k.Scopes[0] != "hub:read" {
					t.Errorf("tok2 config = %+v, want svc-b with [hub:read]", k)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := ParseAPIKeysEnv(tt.raw)
			if tt.wantLen == 0 {
				if keys != nil {
					t.Errorf("expected nil, got %v", keys)
				}
				return
			}
			if len(keys) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(keys), tt.wantLen)
			}
			if tt.check != nil {
				tt.check(t, keys)
			}
		})
	}
}
