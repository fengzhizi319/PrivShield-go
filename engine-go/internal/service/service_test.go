package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/fengzhizi319/PrivShield/pkg/naming"
)

func newTestService(t *testing.T) *PrivacyService {
	t.Helper()
	svc, err := NewPrivacyService(DefaultConfig())
	if err != nil {
		t.Fatalf("NewPrivacyService: %v", err)
	}
	return svc
}

// ──────────────────────────────────────────────
// SSOT 数据源归一化 — SanitizeMedicalRecord
// ──────────────────────────────────────────────

func TestSanitizeMedicalRecord_CanonicalDSID(t *testing.T) {
	svc := newTestService(t)
	record := map[string]string{"name": "张三", "id_card_no": "110101199003072345"}

	// canonical datasource_id
	result, err := svc.SanitizeMedicalRecord(record, naming.DSYibao)
	if err != nil {
		t.Fatalf("unexpected error for %s: %v", naming.DSYibao, err)
	}
	if result["name"] == "张三" {
		t.Error("name should be masked")
	}
	if result["id_card_no"] == "110101199003072345" {
		t.Error("id_card_no should be masked")
	}
}

func TestSanitizeMedicalRecord_APICode(t *testing.T) {
	svc := newTestService(t)
	record := map[string]string{"name": "李四"}

	// api_code alias → should resolve to DSYibao
	result, err := svc.SanitizeMedicalRecord(record, naming.API1Yibao)
	if err != nil {
		t.Fatalf("unexpected error for %s: %v", naming.API1Yibao, err)
	}
	if result["name"] == "李四" {
		t.Error("name should be masked via api_code resolution")
	}
}

func TestSanitizeMedicalRecord_SlugAlias(t *testing.T) {
	svc := newTestService(t)
	record := map[string]string{"name": "王五"}

	// slug alias "yibao" → should resolve to DSYibao
	result, err := svc.SanitizeMedicalRecord(record, "yibao")
	if err != nil {
		t.Fatalf("unexpected error for slug 'yibao': %v", err)
	}
	if result["name"] == "王五" {
		t.Error("name should be masked via slug resolution")
	}
}

func TestSanitizeMedicalRecord_ChineseAlias(t *testing.T) {
	svc := newTestService(t)
	record := map[string]string{"name": "赵六"}

	// Chinese alias "医保" → should resolve to DSYibao
	result, err := svc.SanitizeMedicalRecord(record, "医保")
	if err != nil {
		t.Fatalf("unexpected error for alias '医保': %v", err)
	}
	if result["name"] == "赵六" {
		t.Error("name should be masked via Chinese alias resolution")
	}
}

func TestSanitizeMedicalRecord_Kangyang_AllForms(t *testing.T) {
	svc := newTestService(t)
	record := map[string]string{"name": "孙七", "phone": "13800138000"}

	aliases := []string{naming.DSKangyang, naming.API2Kangyang, "kangyang", "康养"}
	for _, alias := range aliases {
		result, err := svc.SanitizeMedicalRecord(record, alias)
		if err != nil {
			t.Errorf("unexpected error for %q: %v", alias, err)
			continue
		}
		if result["name"] == "孙七" {
			t.Errorf("name should be masked for alias %q", alias)
		}
	}
}

func TestSanitizeMedicalRecord_UnknownDomain_FailClosed(t *testing.T) {
	svc := newTestService(t)
	record := map[string]string{"name": "张三"}

	_, err := svc.SanitizeMedicalRecord(record, "unknown_source")
	if err == nil {
		t.Fatal("expected error for unknown domain, got nil")
	}
	if !strings.Contains(err.Error(), "INVALID_DATASOURCE_ID") {
		t.Errorf("error should contain INVALID_DATASOURCE_ID, got: %v", err)
	}
}

func TestSanitizeMedicalRecord_ReservedDomain_FailClosed(t *testing.T) {
	svc := newTestService(t)
	record := map[string]string{"name": "张三"}

	// DSMock3 is registered but reserved → should fail
	_, err := svc.SanitizeMedicalRecord(record, naming.DSMock3)
	if err == nil {
		t.Fatal("expected error for reserved domain, got nil")
	}
}

func TestSanitizeMedicalRecord_EmptyDomain_FailClosed(t *testing.T) {
	svc := newTestService(t)
	record := map[string]string{"name": "张三"}

	_, err := svc.SanitizeMedicalRecord(record, "")
	if err == nil {
		t.Fatal("expected error for empty domain, got nil")
	}
}

// ──────────────────────────────────────────────
// SSOT 数据源归一化 — SanitizeMedicalBatch
// ──────────────────────────────────────────────

func TestSanitizeMedicalBatch_CanonicalDSID(t *testing.T) {
	svc := newTestService(t)
	records := []map[string]string{
		{"name": "张三", "phone": "13800138000"},
		{"name": "李四", "phone": "13900139000"},
	}

	results, err := svc.SanitizeMedicalBatch(records, naming.DSYibao)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0]["name"] == "张三" {
		t.Error("first record name should be masked")
	}
}

func TestSanitizeMedicalBatch_UnknownDomain_FailClosed(t *testing.T) {
	svc := newTestService(t)
	records := []map[string]string{{"name": "张三"}}

	_, err := svc.SanitizeMedicalBatch(records, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown domain, got nil")
	}
	if !strings.Contains(err.Error(), "INVALID_DATASOURCE_ID") {
		t.Errorf("error should contain INVALID_DATASOURCE_ID, got: %v", err)
	}
}

// ──────────────────────────────────────────────
// P0: ClassifyBatch 并行化测试
// ──────────────────────────────────────────────

func TestClassifyBatch_Parallel_Correctness(t *testing.T) {
	svc := newTestService(t)

	// 100 条记录，每条 3 个字段 → 展平后 300 个 (field, value) 对
	records := make([]map[string]string, 100)
	for i := 0; i < 100; i++ {
		records[i] = map[string]string{
			"id_card_no": "110101199003072345",
			"phone":      "13812345678",
			"name":       "张三",
		}
	}

	results := svc.ClassifyBatch(records)
	// ClassifyBatch 展平所有字段，100 × 3 = 300
	if len(results) != 300 {
		t.Fatalf("expected 300 results (100 records × 3 fields), got %d", len(results))
	}
	for i, r := range results {
		if r == nil {
			t.Errorf("result[%d] is nil", i)
		}
	}
}

func TestClassifyBatch_Parallel_ConcurrentSafety(t *testing.T) {
	svc := newTestService(t)

	// 并发执行多次 ClassifyBatch，验证无 data race
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			records := make([]map[string]string, 20)
			for i := range records {
				records[i] = map[string]string{"phone": "13800138000"}
			}
			results := svc.ClassifyBatch(records)
			if len(results) != 20 {
				t.Errorf("expected 20 results, got %d", len(results))
			}
		}()
	}
	wg.Wait()
}

// ──────────────────────────────────────────────
// P0: DP 预算检查测试
// ──────────────────────────────────────────────

func TestNoisyCount_BudgetExhaustion(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TotalEpsilon = 1.0 // 极低预算
	svc, err := NewPrivacyService(cfg)
	if err != nil {
		t.Fatalf("NewPrivacyService: %v", err)
	}
	ctx := context.Background()

	// 第一次消耗 0.6
	_, err = svc.NoisyCount(ctx, 100, 0.6)
	if err != nil {
		t.Fatalf("first NoisyCount should succeed: %v", err)
	}

	// 第二次消耗 0.6，累计 1.2 > 1.0，应被拒绝
	_, err = svc.NoisyCount(ctx, 100, 0.6)
	if err == nil {
		t.Fatal("expected budget exhaustion error, got nil")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Errorf("error should mention budget, got: %v", err)
	}
}

func TestDPVectorSum_BudgetExhaustion(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TotalEpsilon = 0.5
	svc, err := NewPrivacyService(cfg)
	if err != nil {
		t.Fatalf("NewPrivacyService: %v", err)
	}
	ctx := context.Background()

	vectors := [][]float64{{1.0, 2.0}, {3.0, 4.0}}
	_, err = svc.DPVectorSum(ctx, vectors, 1.0, 0.6)
	if err == nil {
		t.Fatal("expected budget exhaustion, got nil")
	}
}

// ──────────────────────────────────────────────
// P0: ObfuscateQueryBatch 并行化测试
// ──────────────────────────────────────────────

func TestObfuscateQueryBatch_Parallel_LargeBatch(t *testing.T) {
	svc := newTestService(t)

	// 200 条查询（超过 32 阈值，触发并行路径）
	queries := make([]string, 200)
	for i := range queries {
		queries[i] = "肺癌早期症状"
	}

	results := svc.ObfuscateQueryBatch(queries, 3, "medical")
	if len(results) != 200 {
		t.Fatalf("expected 200 results, got %d", len(results))
	}
	for i, r := range results {
		// 原始查询 + 3 个混淆 = 4 条
		if len(r) != 4 {
			t.Errorf("result[%d] has %d queries, want 4", i, len(r))
		}
	}
}

func TestObfuscateQueryBatch_Parallel_ConcurrentSafety(t *testing.T) {
	svc := newTestService(t)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			queries := make([]string, 50)
			for i := range queries {
				queries[i] = "糖尿病治疗"
			}
			results := svc.ObfuscateQueryBatch(queries, 2, "medical")
			if len(results) != 50 {
				t.Errorf("expected 50 results, got %d", len(results))
			}
		}()
	}
	wg.Wait()
}

// ──────────────────────────────────────────────
// P2: Config 去硬编码路径测试
// ──────────────────────────────────────────────

func TestDefaultConfig_HasConfigurablePaths(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.RulesDir == "" {
		t.Error("RulesDir should not be empty")
	}
	if cfg.PrivacyYAML == "" {
		t.Error("PrivacyYAML should not be empty")
	}
	if cfg.RulesDir != "rules/domains" {
		t.Errorf("RulesDir = %q, want %q", cfg.RulesDir, "rules/domains")
	}
	if cfg.PrivacyYAML != "config/privacy.yaml" {
		t.Errorf("PrivacyYAML = %q, want %q", cfg.PrivacyYAML, "config/privacy.yaml")
	}
}

// ──────────────────────────────────────────────
// P0: 热重载并发安全（atomic.Pointer + RLock）
// ──────────────────────────────────────────────

func TestClassify_ConcurrentWithReload(t *testing.T) {
	svc := newTestService(t)
	var wg sync.WaitGroup
	// 32 个 goroutine 并发分类
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				res := svc.Classify("phone", "13812345678")
				if res == nil {
					t.Error("Classify returned nil")
					return
				}
			}
		}()
	}
	// 2 个 goroutine 并发重载
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = svc.ReloadDynamicProfiles()
			}
		}()
	}
	wg.Wait()
}

// ──────────────────────────────────────────────
// P0: atomic.Pointer 初始化验证
// ──────────────────────────────────────────────

func TestNewPrivacyService_ClassifierInitialized(t *testing.T) {
	svc := newTestService(t)
	// classifier 应该在构造后立即可用
	res := svc.Classify("phone", "13812345678")
	if res == nil {
		t.Fatal("Classify returned nil after initialization")
	}
	if res.Level == "" {
		t.Fatal("Classify returned empty level")
	}
}

// ──────────────────────────────────────────────
// P2-3：主链路存证指纹必须是国密 SM3
// ──────────────────────────────────────────────

func TestProcessAgentData_FingerprintIsSM3(t *testing.T) {
	svc := newTestService(t)
	records := []map[string]interface{}{{"phone": "13800138000"}}

	// 已知答案测试的前提：入站载荷字节序列稳定（encoding/json 对 map 键排序）。
	rawBytes, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("marshal records: %v", err)
	}
	if got := string(rawBytes); got != `[{"phone":"13800138000"}]` {
		t.Fatalf("fingerprint input drifted: %s", got)
	}

	res, err := svc.ProcessAgentData(records, naming.API1Yibao, naming.DSYibao)
	if err != nil {
		t.Fatalf("ProcessAgentData: %v", err)
	}

	// 期望值由 `openssl dgst -sm3` 独立算出，不用本仓库 SM3 实现自证。
	const wantInputSM3 = "63c6de8dcddf2b4820996557038af539d0e2be7fa990882f54257a157ca89ae0"
	const wantOutputSM3 = "e62847036194e5d2663aecce05453084b78d0c7e07cca66dec5b47bc07d91683"
	// 同一载荷的 SHA-256，用于证明算法确实换掉了（P2-3 回归护栏）。
	const legacyInputSHA256 = "9a4517471a51654bbef369299af1f6949fb36fc6784f2143d58a398b24500237"

	inputHash, _ := res.Summary["input_hash"].(string)
	if inputHash != wantInputSM3 {
		t.Errorf("input_hash = %q, want SM3 %q", inputHash, wantInputSM3)
	}
	if inputHash == legacyInputSHA256 {
		t.Error("input_hash is still the SHA-256 digest")
	}

	outputHash, _ := res.Summary["output_hash"].(string)
	if outputHash != wantOutputSM3 {
		t.Errorf("output_hash = %q, want SM3 %q", outputHash, wantOutputSM3)
	}
	// 下游存证列宽/前缀比对依赖 64 位小写十六进制口径不变。
	if len(outputHash) != 64 || strings.ToLower(outputHash) != outputHash {
		t.Errorf("output_hash = %q, want 64-char lowercase hex", outputHash)
	}
}

// ──────────────────────────────────────────────
// P2-6：兼容别名不得携带裸字面量标识，等级取自词表常量
// ──────────────────────────────────────────────

func TestProcessMedicalData_UsesNamingSSOT(t *testing.T) {
	svc := newTestService(t)

	res, err := svc.ProcessMedicalData([]map[string]interface{}{{"phone": "13800138000"}})
	if err != nil {
		t.Fatalf("ProcessMedicalData: %v", err)
	}
	if got := res.Summary["api_code"]; got != naming.API1Yibao {
		t.Errorf("api_code = %v, want naming.API1Yibao %q", got, naming.API1Yibao)
	}
	if got := res.Summary["datasource_id"]; got != naming.DSYibao {
		t.Errorf("datasource_id = %v, want naming.DSYibao %q", got, naming.DSYibao)
	}
	// 常量契约字面量在此钉死：改引用常量的同时不得改变运行期字符串。
	if naming.API1Yibao != "api1_yibao" || naming.DSYibao != "ds_yibao" {
		t.Fatalf("naming contract values changed: %q / %q", naming.API1Yibao, naming.DSYibao)
	}

	// 定级结果必须是 rules/taxonomies/default.yaml 的 L1~L5 标识（由 naming 常量给出）。
	if res.Level != naming.SecurityLevelL3 {
		t.Errorf("level = %q, want taxonomy constant %q", res.Level, naming.SecurityLevelL3)
	}
	if got := res.Summary["overall_level"]; got != naming.SecurityLevelL3 {
		t.Errorf("summary.overall_level = %v, want %q", got, naming.SecurityLevelL3)
	}
}
