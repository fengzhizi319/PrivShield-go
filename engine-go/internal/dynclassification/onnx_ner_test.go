package dynclassification

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ──────────────────────────────────────────────
// DynamicBatcher 测试
// ──────────────────────────────────────────────

func TestDynamicBatcherBasic(t *testing.T) {
	var mu sync.Mutex
	var batchCount int

	fn := func(ctx context.Context, items []BatchItem) []BatchResult {
		mu.Lock()
		batchCount++
		mu.Unlock()

		results := make([]BatchResult, len(items))
		for i, item := range items {
			results[i] = BatchResult{
				ItemID: item.ID,
				Output: fmt.Sprintf("processed: %s", item.Input),
			}
		}
		return results
	}

	cfg := DynamicBatcherConfig{
		MaxBatchSize:    4,
		MaxWaitTime:     50 * time.Millisecond,
		QueueBufferSize: 100,
	}
	batcher := NewDynamicBatcher(cfg, fn)
	batcher.Start()
	defer batcher.Stop()

	// 提交单个请求
	ctx := context.Background()
	result, err := batcher.Submit(ctx, "req-1", "hello", 2*time.Second)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.Output != "processed: hello" {
		t.Errorf("result = %v, want 'processed: hello'", result.Output)
	}
}

func TestDynamicBatcherBatching(t *testing.T) {
	var maxBatchSize int
	var mu sync.Mutex

	fn := func(ctx context.Context, items []BatchItem) []BatchResult {
		mu.Lock()
		if len(items) > maxBatchSize {
			maxBatchSize = len(items)
		}
		mu.Unlock()

		results := make([]BatchResult, len(items))
		for i, item := range items {
			results[i] = BatchResult{
				ItemID: item.ID,
				Output: fmt.Sprintf("ok-%s", item.ID),
			}
		}
		return results
	}

	cfg := DynamicBatcherConfig{
		MaxBatchSize:    10,
		MaxWaitTime:     100 * time.Millisecond,
		QueueBufferSize: 100,
	}
	batcher := NewDynamicBatcher(cfg, fn)
	batcher.Start()
	defer batcher.Stop()

	// 并发提交多个请求
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			result, err := batcher.Submit(ctx, fmt.Sprintf("req-%d", id), fmt.Sprintf("input-%d", id), 5*time.Second)
			if err != nil {
				t.Errorf("Submit req-%d: %v", id, err)
				return
			}
			expected := fmt.Sprintf("ok-req-%d", id)
			if result.Output != expected {
				t.Errorf("req-%d result = %v, want %s", id, result.Output, expected)
			}
		}(i)
	}
	wg.Wait()

	mu.Lock()
	batchSize := maxBatchSize
	mu.Unlock()
	if batchSize == 0 {
		t.Error("no batches were executed")
	}
	t.Logf("max batch size observed: %d", batchSize)
}

func TestDynamicBatcherStats(t *testing.T) {
	fn := func(ctx context.Context, items []BatchItem) []BatchResult {
		results := make([]BatchResult, len(items))
		for i, item := range items {
			results[i] = BatchResult{ItemID: item.ID, Output: "ok"}
		}
		return results
	}

	cfg := DefaultBatcherConfig()
	cfg.MaxBatchSize = 2
	cfg.MaxWaitTime = 10 * time.Millisecond
	batcher := NewDynamicBatcher(cfg, fn)
	batcher.Start()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, _ = batcher.Submit(ctx, fmt.Sprintf("r%d", i), "test", 2*time.Second)
	}

	batcher.Stop()

	processed, batches, dropped := batcher.Stats()
	if processed != 3 {
		t.Errorf("processed = %d, want 3", processed)
	}
	if batches == 0 {
		t.Error("batches = 0, want > 0")
	}
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
}

// ──────────────────────────────────────────────
// RuleBasedNerEngine 测试
// ──────────────────────────────────────────────

func TestRuleBasedNerExtractIdCard(t *testing.T) {
	engine := NewRuleBasedNerEngine()
	ctx := context.Background()

	text := "患者身份证号 110101199003071234 已登记"
	entities, err := engine.Extract(ctx, text)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	found := false
	for _, e := range entities {
		if e.Label == "ID_CARD" {
			found = true
			if e.Text != "110101199003071234" {
				t.Errorf("ID_CARD text = %q, want %q", e.Text, "110101199003071234")
			}
			if e.Source != "rule" {
				t.Errorf("source = %q, want %q", e.Source, "rule")
			}
		}
	}
	if !found {
		t.Error("ID_CARD entity not found")
	}
}

func TestRuleBasedNerExtractPhone(t *testing.T) {
	engine := NewRuleBasedNerEngine()
	ctx := context.Background()

	text := "联系电话 13812345678 已更新"
	entities, err := engine.Extract(ctx, text)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	found := false
	for _, e := range entities {
		if e.Label == "PHONE" && e.Text == "13812345678" {
			found = true
		}
	}
	if !found {
		t.Error("PHONE entity not found")
		t.Logf("entities: %+v", entities)
	}
}

func TestRuleBasedNerExtractEmail(t *testing.T) {
	engine := NewRuleBasedNerEngine()
	ctx := context.Background()

	text := "邮箱 test@example.com 已验证"
	entities, err := engine.Extract(ctx, text)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	found := false
	for _, e := range entities {
		if e.Label == "EMAIL" && e.Text == "test@example.com" {
			found = true
		}
	}
	if !found {
		t.Error("EMAIL entity not found")
	}
}

func TestRuleBasedNerExtractMedical(t *testing.T) {
	engine := NewRuleBasedNerEngine()
	ctx := context.Background()

	text := "诊断结果为艾滋病，需要进一步检查"
	entities, err := engine.Extract(ctx, text)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	found := false
	for _, e := range entities {
		if e.Label == "MEDICAL_CONDITION" && e.Text == "艾滋病" {
			found = true
		}
	}
	if !found {
		t.Error("MEDICAL_CONDITION entity not found")
	}
}

func TestRuleBasedNerEmptyText(t *testing.T) {
	engine := NewRuleBasedNerEngine()
	ctx := context.Background()

	entities, err := engine.Extract(ctx, "")
	if err != nil {
		t.Fatalf("Extract empty: %v", err)
	}
	if len(entities) != 0 {
		t.Errorf("expected 0 entities for empty text, got %d", len(entities))
	}
}

func TestRuleBasedNerIsAvailable(t *testing.T) {
	engine := NewRuleBasedNerEngine()
	if !engine.IsAvailable() {
		t.Error("rule-based NER should always be available")
	}
	if engine.Name() != "rule-based-ner" {
		t.Errorf("name = %q, want %q", engine.Name(), "rule-based-ner")
	}
}

// ──────────────────────────────────────────────
// OnnxNerEngine 降级测试
// ──────────────────────────────────────────────

func TestOnnxNerEngineFallback(t *testing.T) {
	cfg := DefaultOnnxNerConfig()
	engine := NewOnnxNerEngine(cfg)

	// 骨架模式下 should fallback to rules
	ctx := context.Background()
	text := "身份证号 110101199003071234"
	entities, err := engine.Extract(ctx, text)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(entities) == 0 {
		t.Error("expected entities from fallback, got none")
	}

	// 验证降级计数
	_, fallbackCount := engine.Stats()
	if fallbackCount == 0 {
		t.Error("expected fallback count > 0")
	}
}

func TestOnnxNerEngineIsAvailable(t *testing.T) {
	cfg := DefaultOnnxNerConfig()
	engine := NewOnnxNerEngine(cfg)

	// 骨架模式默认不可用
	if engine.IsAvailable() {
		t.Error("skeleton OnnxNerEngine should not be available")
	}
}

// ──────────────────────────────────────────────
// NER 能力诚实口径（P1-3）
// ──────────────────────────────────────────────

// modelBackedStubNer 是「真实模型驱动」NER 引擎的测试替身，
// 用于证明 modelBacked 口径来自引擎真实状态而非硬编码常量。
type modelBackedStubNer struct{ backed bool }

func (s *modelBackedStubNer) Extract(_ context.Context, _ string) ([]NerEntity, error) {
	return nil, nil
}
func (s *modelBackedStubNer) IsAvailable() bool { return true }
func (s *modelBackedStubNer) Name() string      { return "stub-model-ner" }
func (s *modelBackedStubNer) ModelBacked() bool { return s.backed }

func TestNerEngineModelBacked(t *testing.T) {
	if NerEngineModelBacked(NewRuleBasedNerEngine()) {
		t.Error("正则降级桩 rule-based-ner 不得被判定为模型驱动（P1-3）")
	}
	if NerEngineModelBacked(NewOnnxNerEngine(DefaultOnnxNerConfig())) {
		t.Error("ONNX 骨架未加载模型，不得被判定为模型驱动")
	}
	if NerEngineModelBacked(NewCudaOnnxNerEngine(DefaultCudaOnnxNerConfig())) {
		t.Error("默认 StubOnnxRuntime 下 CUDA 引擎不得被判定为模型驱动")
	}
	if NerEngineModelBacked(nil) {
		t.Error("nil 引擎必须返回 false")
	}
	if !NerEngineModelBacked(&modelBackedStubNer{backed: true}) {
		t.Error("真实模型驱动的引擎应返回 true（证明口径非硬编码 false）")
	}
	if NerEngineModelBacked(&modelBackedStubNer{backed: false}) {
		t.Error("模型未就绪的引擎应返回 false")
	}
}

func TestClassificationFunnelNerStatus(t *testing.T) {
	// 生产装配口径（service.go 传入 NewRuleBasedNerEngine()）：正则桩 → 非模型驱动。
	funnel, err := NewClassificationFunnel(nil, NewRuleBasedNerEngine(), nil, DefaultFunnelConfig())
	if err != nil {
		t.Fatalf("NewClassificationFunnel: %v", err)
	}
	backend, modelBacked := funnel.NerStatus()
	if backend != "rule-based-ner" {
		t.Errorf("backend = %q, want %q", backend, "rule-based-ner")
	}
	if modelBacked {
		t.Error("默认构建中 ner modelBacked 必须为 false（ONNX 模型未交付）")
	}

	// nil 引擎时漏斗回落到正则桩，口径同样为 false。
	def, err := NewClassificationFunnel(nil, nil, nil, DefaultFunnelConfig())
	if err != nil {
		t.Fatalf("NewClassificationFunnel: %v", err)
	}
	if b, mb := def.NerStatus(); b != "rule-based-ner" || mb {
		t.Errorf("默认回落口径 = (%q, %v), want (\"rule-based-ner\", false)", b, mb)
	}

	// 真实模型引擎在位时应如实上报 true。
	wired, err := NewClassificationFunnel(nil, &modelBackedStubNer{backed: true}, nil, DefaultFunnelConfig())
	if err != nil {
		t.Fatalf("NewClassificationFunnel: %v", err)
	}
	if b, mb := wired.NerStatus(); b != "stub-model-ner" || !mb {
		t.Errorf("模型驱动口径 = (%q, %v), want (\"stub-model-ner\", true)", b, mb)
	}

	// 未装配引擎（直接构造，绕过 New 的回落）时报告 none/false。
	empty := &ClassificationFunnel{}
	if b, mb := empty.NerStatus(); b != "none" || mb {
		t.Errorf("未装配口径 = (%q, %v), want (\"none\", false)", b, mb)
	}
}

// ──────────────────────────────────────────────
// FallbackChain 测试
// ──────────────────────────────────────────────

func TestFallbackChain(t *testing.T) {
	// ONNX 不可用，应该降级到规则引擎
	onnx := NewOnnxNerEngine(DefaultOnnxNerConfig())
	rule := NewRuleBasedNerEngine()

	chain := NewFallbackChain(onnx, rule)
	ctx := context.Background()

	text := "邮箱 test@example.com 身份证 110101199003071234"
	entities, err := chain.Extract(ctx, text)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(entities) == 0 {
		t.Error("expected entities from fallback chain")
	}
}

// ──────────────────────────────────────────────
// RedactEntities 测试
// ──────────────────────────────────────────────

func TestRedactEntities(t *testing.T) {
	text := "身份证号 110101199003071234 已登记"
	entities := []NerEntity{
		{Text: "110101199003071234", Label: "ID_CARD", Start: 5, End: 23},
	}

	redacted := RedactEntities(text, entities, "*")
	expected := "身份证号 ****************** 已登记"
	if redacted != expected {
		t.Errorf("redacted = %q, want %q", redacted, expected)
	}
}

func TestRedactEntitiesEmpty(t *testing.T) {
	text := "无实体文本"
	redacted := RedactEntities(text, nil, "*")
	if redacted != text {
		t.Errorf("redacted = %q, want %q", redacted, text)
	}
}

// ──────────────────────────────────────────────
// NerLabelToSecurityTag 测试
// ──────────────────────────────────────────────

func TestNerLabelToSecurityTag(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{"ID_CARD", "PII_IDENTITY"},
		{"PHONE", "PII_CONTACT"},
		{"EMAIL", "PII_CONTACT"},
		{"BANK_CARD", "PII_FINANCIAL"},
		{"PERSON", "PII_IDENTITY"},
		{"ADDRESS", "PII_LOCATION"},
		{"MEDICAL_CONDITION", "PHI_HEALTH"},
		{"UNKNOWN", "PII_OTHER"},
	}

	for _, tt := range tests {
		got := NerLabelToSecurityTag(tt.label)
		if got != tt.want {
			t.Errorf("NerLabelToSecurityTag(%q) = %q, want %q", tt.label, got, tt.want)
		}
	}
}
