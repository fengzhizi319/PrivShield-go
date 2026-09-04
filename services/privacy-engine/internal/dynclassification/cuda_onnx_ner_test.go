package dynclassification

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ──────────────────────────────────────────────
// Mock ONNX Runtime（可注入测试桩）
// ──────────────────────────────────────────────

// mockOnnxRuntime 模拟 ONNX Runtime，返回预设 Logits
type mockOnnxRuntime struct {
	ready      bool
	logitsFunc func(req OnnxInferRequest) []float32
	initErr    error
	inferErr   error
	mu         sync.Mutex
	inferCalls int
}

func newMockOnnxRuntime() *mockOnnxRuntime {
	return &mockOnnxRuntime{ready: false}
}

func (m *mockOnnxRuntime) Initialize(_ string, _ int) error {
	if m.initErr != nil {
		return m.initErr
	}
	m.ready = true
	return nil
}

func (m *mockOnnxRuntime) Infer(req OnnxInferRequest) (*OnnxInferResult, error) {
	m.mu.Lock()
	m.inferCalls++
	m.mu.Unlock()

	if m.inferErr != nil {
		return nil, m.inferErr
	}
	if m.logitsFunc != nil {
		return &OnnxInferResult{Logits: m.logitsFunc(req)}, nil
	}
	// 默认返回全零 Logits（所有标签等概率）
	numClasses := 21 // defaultBIONERLabels 长度
	total := req.BatchSize * req.SeqLen * numClasses
	return &OnnxInferResult{Logits: make([]float32, total)}, nil
}

func (m *mockOnnxRuntime) Close()        { m.ready = false }
func (m *mockOnnxRuntime) IsReady() bool { return m.ready }
func (m *mockOnnxRuntime) DeviceName() string {
	if m.ready {
		return "mock-cuda:0"
	}
	return "mock-stub"
}

// ──────────────────────────────────────────────
// 辅助函数
// ──────────────────────────────────────────────

// makeLogitsForLabel 构造 Logits：在指定 Token 位置设置指定标签的 Logit 最高
func makeLogitsForLabel(seqLen, numClasses int, positions map[int]string, labelMap []string) []float32 {
	labelID := make(map[string]int)
	for i, l := range labelMap {
		labelID[l] = i
	}

	total := seqLen * numClasses
	logits := make([]float32, total)

	// 默认所有位置设为 "O"
	oID := labelID["O"]
	for t := 0; t < seqLen; t++ {
		offset := t * numClasses
		logits[offset+oID] = 10.0 // 高 Logit 确保 argmax 命中
	}

	// 覆盖指定位置
	for pos, label := range positions {
		if pos >= seqLen {
			continue
		}
		offset := pos * numClasses
		// 清零
		for c := 0; c < numClasses; c++ {
			logits[offset+c] = 0
		}
		if id, ok := labelID[label]; ok {
			logits[offset+id] = 10.0
		}
	}

	return logits
}

// ──────────────────────────────────────────────
// 测试：BIO 实体解码
// ──────────────────────────────────────────────

func TestDecodeBIOEntitiesSingle(t *testing.T) {
	labels := defaultBIONERLabels()
	numClasses := len(labels)
	seqLen := 10

	rt := newMockOnnxRuntime()
	cfg := DefaultCudaOnnxNerConfig()
	cfg.LabelList = labels
	cfg.MaxSeqLen = seqLen
	cfg.Runtime = rt
	_ = rt.Initialize("", 0)

	engine := &CudaOnnxNerEngine{
		cfg:      cfg,
		runtime:  rt,
		labelMap: labels,
	}

	// 构造 offsets：前 5 个 Token 对应文本 "张三得了艾滋病" 的每个字符
	text := "张三得了艾滋病"
	offsets := make([]TokenOffset, seqLen)
	// [CLS] 占位
	offsets[0] = TokenOffset{Start: 0, End: 0}
	// 每个字符一个 Token
	byteIdx := 0
	runes := []rune(text)
	for i, r := range runes {
		if i+1 >= seqLen-1 {
			break
		}
		rLen := len(string(r))
		offsets[i+1] = TokenOffset{Start: byteIdx, End: byteIdx + rLen}
		byteIdx += rLen
	}
	// [SEP]
	offsets[len(runes)+1] = TokenOffset{Start: len(text), End: len(text)}

	// 构造 Logits：位置 5 = B-DISEASE, 位置 6 = I-DISEASE
	positions := map[int]string{
		5: "B-DISEASE",
		6: "I-DISEASE",
	}
	logits := makeLogitsForLabel(seqLen, numClasses, positions, labels)

	entities := engine.decodeBIOEntities(text, logits, offsets, seqLen, numClasses)

	if len(entities) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(entities))
	}
	e := entities[0]
	if e.Label != "DISEASE" {
		t.Errorf("expected label DISEASE, got %s", e.Label)
	}
	if e.Source != "onnx_gpu" {
		t.Errorf("expected source onnx_gpu, got %s", e.Source)
	}
	if e.Confidence <= 0 {
		t.Errorf("expected positive confidence, got %f", e.Confidence)
	}
}

func TestDecodeBIOEntitiesMultiple(t *testing.T) {
	labels := defaultBIONERLabels()
	numClasses := len(labels)
	seqLen := 10

	rt := newMockOnnxRuntime()
	cfg := DefaultCudaOnnxNerConfig()
	cfg.LabelList = labels
	cfg.MaxSeqLen = seqLen
	cfg.Runtime = rt

	engine := &CudaOnnxNerEngine{
		cfg:      cfg,
		runtime:  rt,
		labelMap: labels,
	}

	// 简单文本 "张三13800138000"
	text := "张三13800138000"
	offsets := make([]TokenOffset, seqLen)
	offsets[0] = TokenOffset{Start: 0, End: 0} // [CLS]
	byteIdx := 0
	for i, r := range []rune(text) {
		if i+1 >= seqLen-1 {
			break
		}
		rLen := len(string(r))
		offsets[i+1] = TokenOffset{Start: byteIdx, End: byteIdx + rLen}
		byteIdx += rLen
	}

	// B-PERSON at pos 1, I-PERSON at pos 2, B-PHONE at pos 3-6
	positions := map[int]string{
		1: "B-PERSON",
		2: "I-PERSON",
		3: "B-PHONE",
		4: "I-PHONE",
		5: "I-PHONE",
		6: "I-PHONE",
	}
	logits := makeLogitsForLabel(seqLen, numClasses, positions, labels)

	entities := engine.decodeBIOEntities(text, logits, offsets, seqLen, numClasses)

	if len(entities) < 2 {
		t.Fatalf("expected >= 2 entities, got %d", len(entities))
	}

	// 检查第一个实体是 PERSON
	if entities[0].Label != "PERSON" {
		t.Errorf("first entity: expected PERSON, got %s", entities[0].Label)
	}
}

func TestDecodeBIOEntitiesNoEntity(t *testing.T) {
	labels := defaultBIONERLabels()
	numClasses := len(labels)
	seqLen := 5

	rt := newMockOnnxRuntime()
	cfg := DefaultCudaOnnxNerConfig()
	cfg.LabelList = labels
	cfg.MaxSeqLen = seqLen
	cfg.Runtime = rt

	engine := &CudaOnnxNerEngine{
		cfg:      cfg,
		runtime:  rt,
		labelMap: labels,
	}

	text := "今天天气好"
	offsets := make([]TokenOffset, seqLen)
	for i := range offsets {
		offsets[i] = TokenOffset{Start: 0, End: 0}
	}

	// 所有位置都是 "O"
	positions := map[int]string{}
	logits := makeLogitsForLabel(seqLen, numClasses, positions, labels)

	entities := engine.decodeBIOEntities(text, logits, offsets, seqLen, numClasses)

	if len(entities) != 0 {
		t.Errorf("expected 0 entities, got %d", len(entities))
	}
}

// ──────────────────────────────────────────────
// 测试：辅助函数
// ──────────────────────────────────────────────

func TestArgmax(t *testing.T) {
	tests := []struct {
		vals []float32
		want int32
	}{
		{[]float32{1, 2, 3}, 2},
		{[]float32{3, 2, 1}, 0},
		{[]float32{0, 0, 10, 0}, 2},
		{[]float32{-1, -5, -2}, 0},
		{[]float32{5}, 0},
	}
	for _, tt := range tests {
		got := argmax(tt.vals)
		if got != tt.want {
			t.Errorf("argmax(%v) = %d, want %d", tt.vals, got, tt.want)
		}
	}
}

func TestSoftmaxScore(t *testing.T) {
	// 当只有一个非零值时，softmax 应接近 1.0
	logits := []float32{0, 0, 100, 0}
	score := softmaxScore(logits, 2)
	if score < 0.99 {
		t.Errorf("softmaxScore with dominant logit: expected ~1.0, got %f", score)
	}

	// 均匀分布时，每个类别的概率应接近 1/n
	uniform := []float32{1, 1, 1, 1}
	score = softmaxScore(uniform, 0)
	expected := 0.25
	if score < expected-0.01 || score > expected+0.01 {
		t.Errorf("softmaxScore uniform: expected ~%f, got %f", expected, score)
	}
}

func TestPadOrTrim(t *testing.T) {
	// 填充
	src := []int64{1, 2, 3}
	got := padOrTrim(src, 5)
	if len(got) != 5 {
		t.Errorf("padOrTrim pad: expected len 5, got %d", len(got))
	}
	if got[0] != 1 || got[2] != 3 || got[4] != 0 {
		t.Errorf("padOrTrim pad: unexpected content %v", got)
	}

	// 截断
	got = padOrTrim(src, 2)
	if len(got) != 2 {
		t.Errorf("padOrTrim trim: expected len 2, got %d", len(got))
	}
	if got[0] != 1 || got[1] != 2 {
		t.Errorf("padOrTrim trim: unexpected content %v", got)
	}
}

// ──────────────────────────────────────────────
// 测试：引擎生命周期与降级
// ──────────────────────────────────────────────

func TestCudaOnnxNerEngineStubFallback(t *testing.T) {
	// 使用 StubOnnxRuntime（初始化失败）→ 引擎不可用 → Extract 降级到规则引擎
	cfg := DefaultCudaOnnxNerConfig()
	cfg.Runtime = NewStubOnnxRuntime()

	engine := NewCudaOnnxNerEngine(cfg)

	if engine.IsAvailable() {
		t.Error("expected engine to be unavailable with stub runtime")
	}

	// Extract 应降级到规则引擎并返回结果
	entities, err := engine.Extract(context.Background(), "手机号 13800138000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entities) == 0 {
		t.Error("expected rule-based fallback to find entities")
	}
	for _, e := range entities {
		if e.Source != "rule" {
			t.Errorf("expected source 'rule' for fallback, got '%s'", e.Source)
		}
	}
}

func TestCudaOnnxNerEngineInitSuccess(t *testing.T) {
	rt := newMockOnnxRuntime()
	cfg := DefaultCudaOnnxNerConfig()
	cfg.Runtime = rt

	engine := NewCudaOnnxNerEngine(cfg)

	if !engine.IsAvailable() {
		t.Error("expected engine to be available after successful init")
	}
	if engine.Name() != "cuda-onnx-ner-gpu" {
		t.Errorf("expected name 'cuda-onnx-ner-gpu', got '%s'", engine.Name())
	}
}

func TestCudaOnnxNerEngineInitFailure(t *testing.T) {
	rt := newMockOnnxRuntime()
	rt.initErr = fmt.Errorf("CUDA not available")
	cfg := DefaultCudaOnnxNerConfig()
	cfg.Runtime = rt

	engine := NewCudaOnnxNerEngine(cfg)

	if engine.IsAvailable() {
		t.Error("expected engine to be unavailable after init failure")
	}
}

// ──────────────────────────────────────────────
// 测试：Worker Pool LockOSThread + 动态合批
// ──────────────────────────────────────────────

func TestCudaOnnxNerEngineWorkerPool(t *testing.T) {
	labels := defaultBIONERLabels()
	numClasses := len(labels)
	seqLen := 10

	rt := newMockOnnxRuntime()
	// 构造 mock logits：所有位置返回 "O"
	rt.logitsFunc = func(req OnnxInferRequest) []float32 {
		total := req.BatchSize * req.SeqLen * numClasses
		logits := make([]float32, total)
		oID := 0 // "O" is first label
		for i := range logits {
			if i%numClasses == oID {
				logits[i] = 10.0
			}
		}
		return logits
	}

	cfg := DefaultCudaOnnxNerConfig()
	cfg.Runtime = rt
	cfg.LabelList = labels
	cfg.MaxSeqLen = seqLen
	cfg.NumWorkers = 2
	cfg.MaxBatch = 4
	cfg.BatchWait = 5 * time.Millisecond
	cfg.InferTimeout = 500 * time.Millisecond

	engine := NewCudaOnnxNerEngine(cfg)
	engine.Start()
	defer engine.Stop()

	// 并发提交多个推理请求
	const numRequests = 10
	results := make(chan []NerEntity, numRequests)

	for i := 0; i < numRequests; i++ {
		go func(idx int) {
			text := fmt.Sprintf("测试文本 %d", idx)
			entities, err := engine.Extract(context.Background(), text)
			if err != nil {
				t.Errorf("request %d failed: %v", idx, err)
			}
			results <- entities
		}(i)
	}

	// 收集结果
	received := 0
	timeout := time.After(2 * time.Second)
	for received < numRequests {
		select {
		case <-results:
			received++
		case <-timeout:
			t.Fatalf("timeout: received %d/%d results", received, numRequests)
		}
	}

	// 验证推理确实被调用
	rt.mu.Lock()
	calls := rt.inferCalls
	rt.mu.Unlock()
	if calls == 0 {
		t.Error("expected at least one ONNX inference call")
	}

	infer, fallback, batches := engine.Stats()
	if infer == 0 && fallback == 0 {
		t.Error("expected non-zero stats")
	}
	t.Logf("Stats: infer=%d, fallback=%d, batches=%d, inferCalls=%d", infer, fallback, batches, calls)
}

func TestCudaOnnxNerEngineInferTimeout(t *testing.T) {
	rt := newMockOnnxRuntime()
	// 模拟推理延迟
	rt.logitsFunc = func(req OnnxInferRequest) []float32 {
		time.Sleep(200 * time.Millisecond) // 超过 InferTimeout
		total := req.BatchSize * req.SeqLen * 21
		return make([]float32, total)
	}

	cfg := DefaultCudaOnnxNerConfig()
	cfg.Runtime = rt
	cfg.InferTimeout = 50 * time.Millisecond // 很短的超时
	cfg.NumWorkers = 1
	cfg.MaxBatch = 1
	cfg.BatchWait = 1 * time.Millisecond

	engine := NewCudaOnnxNerEngine(cfg)
	engine.Start()
	defer engine.Stop()

	// 推理应超时并降级到规则引擎
	entities, err := engine.Extract(context.Background(), "手机号 13800138000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 降级到规则引擎应该找到实体
	if len(entities) == 0 {
		t.Error("expected fallback entities on timeout")
	}
}

func TestCudaOnnxNerEngineInferError(t *testing.T) {
	rt := newMockOnnxRuntime()
	rt.inferErr = fmt.Errorf("CUDA OOM")

	cfg := DefaultCudaOnnxNerConfig()
	cfg.Runtime = rt
	cfg.NumWorkers = 1
	cfg.MaxBatch = 1
	cfg.BatchWait = 1 * time.Millisecond
	cfg.InferTimeout = 500 * time.Millisecond

	engine := NewCudaOnnxNerEngine(cfg)
	engine.Start()
	defer engine.Stop()

	// 推理错误应降级到规则引擎
	entities, err := engine.Extract(context.Background(), "身份证 110101199003072345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entities) == 0 {
		t.Error("expected fallback entities on inference error")
	}
}

// ──────────────────────────────────────────────
// 测试：CPU 设备名称
// ──────────────────────────────────────────────

func TestCudaOnnxNerEngineCPUName(t *testing.T) {
	cfg := DefaultCudaOnnxNerConfig()
	cfg.GPUDeviceID = -1 // CPU 模式

	engine := NewCudaOnnxNerEngine(cfg)
	if engine.Name() != "cuda-onnx-ner-cpu" {
		t.Errorf("expected 'cuda-onnx-ner-cpu', got '%s'", engine.Name())
	}
}

// ──────────────────────────────────────────────
// 测试：默认标签集
// ──────────────────────────────────────────────

func TestDefaultBIONERLabels(t *testing.T) {
	labels := defaultBIONERLabels()
	if len(labels) == 0 {
		t.Fatal("expected non-empty label list")
	}
	if labels[0] != "O" {
		t.Errorf("first label should be 'O', got '%s'", labels[0])
	}

	// 检查 BIO 配对：每个 B-XXX 应有对应 I-XXX
	bLabels := make(map[string]bool)
	iLabels := make(map[string]bool)
	for _, l := range labels {
		if len(l) > 2 && l[:2] == "B-" {
			bLabels[l[2:]] = true
		}
		if len(l) > 2 && l[:2] == "I-" {
			iLabels[l[2:]] = true
		}
	}
	for tag := range bLabels {
		if !iLabels[tag] {
			t.Errorf("B-%s has no matching I-%s", tag, tag)
		}
	}
}
