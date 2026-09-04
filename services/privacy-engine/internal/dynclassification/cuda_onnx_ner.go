// Package dynclassification 提供 CUDA ONNX NER 推理引擎。
//
// 实现设计文档 §6 描述的 Go + CUDA Small-NER 深度学习推理核心：
//   - LockOSThread 专职 GPU Worker Pool：避免 Go Scheduler 在 CGO 调用期间迁移线程
//   - 动态合批 (Dynamic Batching)：通过 Channel + Ticker 聚合请求提升 GPU 利用率
//   - BIO/BIOES 实体解码：从 Logits .argmax 解码为结构化实体
//   - 四级降级：GPU CUDA → CPU ONNX → Rule-based → 安全底线
//
// 当前实现：
//   - 完整 LockOSThread Worker Pool 架构（纯 Go，可编译可测试）
//   - 完整 BIO 实体解码器（纯 Go）
//   - ONNX Runtime CGO 绑定通过 OnnxRuntime 接口抽象
//   - 默认 Stub 实现（返回错误 → 自动降级到 RuleBasedNerEngine）
//   - 引入 github.com/yalue/onnxruntime_go 后替换为真实 CGO 实现
//
// 参考设计文档 §6.1-§6.4、§12.5。
package dynclassification

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ──────────────────────────────────────────────
// NER 推理任务
// ──────────────────────────────────────────────

// NerTask GPU/CPU ONNX 推理任务
type NerTask struct {
	Text       string           // 原始文本
	InputIDs   []int64          // Token ID 序列
	AttnMask   []int64          // Attention Mask
	TypeIDs    []int64          // Token Type IDs
	Offsets    []TokenOffset    // Token → 原始文本字节映射
	ResultChan chan []NerEntity // 推理结果回传通道
}

// ──────────────────────────────────────────────
// ONNX Runtime 抽象接口
// ──────────────────────────────────────────────

// OnnxInferRequest ONNX 推理请求（扁平化批次张量）
type OnnxInferRequest struct {
	BatchSize int     // 批次大小
	SeqLen    int     // 序列长度
	InputIDs  []int64 // 扁平化 input_ids [batch * seq_len]
	AttnMask  []int64 // 扁平化 attention_mask [batch * seq_len]
	TypeIDs   []int64 // 扁平化 token_type_ids [batch * seq_len]
}

// OnnxInferResult ONNX 推理结果
type OnnxInferResult struct {
	Logits []float32 // 扁平化 logits [batch * seq_len * num_classes]
}

// OnnxRuntime ONNX Runtime 推理接口抽象。
// 允许在不引入 CGO 的情况下编译和测试完整架构。
// 生产部署时替换为基于 github.com/yalue/onnxruntime_go 的 CGO 实现。
type OnnxRuntime interface {
	// Initialize 初始化 ONNX Runtime 环境并加载模型
	Initialize(modelPath string, gpuDeviceID int) error

	// Infer 执行批次推理
	Infer(req OnnxInferRequest) (*OnnxInferResult, error)

	// Close 释放模型资源
	Close()

	// IsReady 检查运行时是否就绪
	IsReady() bool

	// DeviceName 返回设备名称（如 "CUDA:0" / "CPU"）
	DeviceName() string
}

// ──────────────────────────────────────────────
// Stub ONNX Runtime（无 CGO 降级实现）
// ──────────────────────────────────────────────

// StubOnnxRuntime ONNX Runtime 桩实现。
// 所有推理请求返回错误，触发上层降级到 RuleBasedNerEngine。
// 用于无 GPU/无 ONNX Runtime 动态库环境下的编译和测试。
type StubOnnxRuntime struct {
	ready bool
}

func NewStubOnnxRuntime() *StubOnnxRuntime {
	return &StubOnnxRuntime{ready: false}
}

func (s *StubOnnxRuntime) Initialize(_ string, _ int) error {
	return fmt.Errorf("stub ONNX runtime: CGO binding not available (install onnxruntime_go + CUDA)")
}

func (s *StubOnnxRuntime) Infer(_ OnnxInferRequest) (*OnnxInferResult, error) {
	return nil, fmt.Errorf("stub ONNX runtime: not initialized")
}

func (s *StubOnnxRuntime) Close()             {}
func (s *StubOnnxRuntime) IsReady() bool      { return s.ready }
func (s *StubOnnxRuntime) DeviceName() string { return "stub" }

// ──────────────────────────────────────────────
// CUDA ONNX NER 引擎配置
// ──────────────────────────────────────────────

// CudaOnnxNerConfig CUDA ONNX NER 引擎配置
type CudaOnnxNerConfig struct {
	ModelPath    string        // ONNX 模型文件路径
	VocabPath    string        // WordPiece 词表路径
	LabelList    []string      // 标签列表 (e.g. ["O", "B-DISEASE", "I-DISEASE", ...])
	GPUDeviceID  int           // CUDA 设备 ID (-1 = CPU)
	NumWorkers   int           // GPU Worker 数量
	MaxSeqLen    int           // 最大序列长度
	MaxBatch     int           // 动态合批最大大小
	BatchWait    time.Duration // 动态合批最大等待
	QueueSize    int           // 推理队列缓冲大小
	InferTimeout time.Duration // 单次推理超时
	Runtime      OnnxRuntime   // ONNX Runtime 实现（可注入测试桩）
}

// DefaultCudaOnnxNerConfig 默认 CUDA ONNX NER 配置
func DefaultCudaOnnxNerConfig() CudaOnnxNerConfig {
	return CudaOnnxNerConfig{
		ModelPath:    ".models/ner/model.onnx",
		VocabPath:    ".models/ner/vocab.txt",
		LabelList:    defaultBIONERLabels(),
		GPUDeviceID:  0,
		NumWorkers:   1,
		MaxSeqLen:    128,
		MaxBatch:     32,
		BatchWait:    3 * time.Millisecond,
		QueueSize:    4096,
		InferTimeout: 50 * time.Millisecond,
		Runtime:      NewStubOnnxRuntime(),
	}
}

// defaultBIONERLabels 默认 BIO NER 标签集
func defaultBIONERLabels() []string {
	return []string{
		"O",
		"B-PERSON", "I-PERSON",
		"B-ID_CARD", "I-ID_CARD",
		"B-PHONE", "I-PHONE",
		"B-EMAIL", "I-EMAIL",
		"B-ADDRESS", "I-ADDRESS",
		"B-ORG", "I-ORG",
		"B-DISEASE", "I-DISEASE",
		"B-MEDICAL", "I-MEDICAL",
	}
}

// ──────────────────────────────────────────────
// CUDA ONNX NER 引擎
// ──────────────────────────────────────────────

// CudaOnnxNerEngine CUDA ONNX NER 推理引擎。
//
// 架构（设计文档 §6.3）：
//
//	Go Goroutine → taskQueue → LockOSThread Worker → ONNX Runtime CGO → CUDA GPU
//	                     ↑ Dynamic Batching (Channel + Ticker)
//	                     ↓ 超时/队列满 → CPU Fallback (RuleBasedNerEngine)
type CudaOnnxNerEngine struct {
	cfg       CudaOnnxNerConfig
	runtime   OnnxRuntime
	tokenizer *Tokenizer
	fallback  *RuleBasedNerEngine
	taskQueue chan *NerTask
	stopChan  chan struct{}
	wg        sync.WaitGroup
	labelMap  []string // ID → Label 映射

	// 统计（atomic）
	inferCount    int64
	fallbackCount int64
	batchCount    int64
	available     int32 // atomic: 0=不可用, 1=可用
}

// NewCudaOnnxNerEngine 创建 CUDA ONNX NER 引擎。
//
// 初始化流程：
//  1. 初始化 ONNX Runtime 环境
//  2. 加载模型并创建推理 Session
//  3. 启动 LockOSThread 专职 GPU Worker Pool
//  4. 若初始化失败，引擎自动标记为不可用（Extract 时降级到规则引擎）
func NewCudaOnnxNerEngine(cfg CudaOnnxNerConfig) *CudaOnnxNerEngine {
	if cfg.NumWorkers <= 0 {
		cfg.NumWorkers = 1
	}
	if cfg.MaxBatch <= 0 {
		cfg.MaxBatch = 32
	}
	if cfg.MaxSeqLen <= 0 {
		cfg.MaxSeqLen = 128
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 4096
	}

	rt := cfg.Runtime
	if rt == nil {
		rt = NewStubOnnxRuntime()
	}

	engine := &CudaOnnxNerEngine{
		cfg:       cfg,
		runtime:   rt,
		tokenizer: NewSimpleTokenizer(cfg.MaxSeqLen),
		fallback:  NewRuleBasedNerEngine(),
		taskQueue: make(chan *NerTask, cfg.QueueSize),
		stopChan:  make(chan struct{}),
		labelMap:  cfg.LabelList,
	}

	// 尝试初始化 ONNX Runtime
	if err := rt.Initialize(cfg.ModelPath, cfg.GPUDeviceID); err != nil {
		slog.Warn("CudaOnnxNerEngine: ONNX Runtime init failed, will use rule fallback",
			"model", cfg.ModelPath,
			"device", cfg.GPUDeviceID,
			"error", err.Error(),
		)
	} else {
		atomic.StoreInt32(&engine.available, 1)
		slog.Info("CudaOnnxNerEngine initialized",
			"model", cfg.ModelPath,
			"device", rt.DeviceName(),
			"workers", cfg.NumWorkers,
			"labels", len(cfg.LabelList),
		)
	}

	return engine
}

// Start 启动 GPU Worker Pool
func (e *CudaOnnxNerEngine) Start() {
	for i := 0; i < e.cfg.NumWorkers; i++ {
		e.wg.Add(1)
		go e.workerLoop(i)
	}
	slog.Info("CudaOnnxNerEngine workers started", "count", e.cfg.NumWorkers)
}

// Stop 优雅停止引擎
func (e *CudaOnnxNerEngine) Stop() {
	close(e.stopChan)
	e.wg.Wait()
	if e.runtime != nil {
		e.runtime.Close()
	}
	slog.Info("CudaOnnxNerEngine stopped",
		"infer", atomic.LoadInt64(&e.inferCount),
		"fallback", atomic.LoadInt64(&e.fallbackCount),
		"batches", atomic.LoadInt64(&e.batchCount),
	)
}

// Extract 从文本中提取命名实体（实现 NerEngine 接口）
//
// 流程：
//  1. 检查引擎是否可用（ONNX Runtime 已初始化）
//  2. 不可用 → 直接降级到规则引擎
//  3. 可用 → 通过 RunInferenceSafe 提交推理任务
//  4. 推理超时/队列满 → 降级到规则引擎
func (e *CudaOnnxNerEngine) Extract(ctx context.Context, text string) ([]NerEntity, error) {
	if text == "" {
		return nil, nil
	}

	if atomic.LoadInt32(&e.available) == 0 {
		atomic.AddInt64(&e.fallbackCount, 1)
		return e.fallback.Extract(ctx, text)
	}

	return e.RunInferenceSafe(ctx, text)
}

// IsAvailable 检查引擎是否可用
func (e *CudaOnnxNerEngine) IsAvailable() bool {
	return atomic.LoadInt32(&e.available) != 0
}

// ModelBacked 实现 ModelBackedNerEngine：仅当初始化成功且注入的 ONNX Runtime
// 真正就绪时才报告模型推理能力可用。交付构建默认注入 StubOnnxRuntime
// （CGO 绑定缺失），因此本方法恒为 false（P1-3）。
func (e *CudaOnnxNerEngine) ModelBacked() bool {
	if atomic.LoadInt32(&e.available) == 0 {
		return false
	}
	return e.runtime != nil && e.runtime.IsReady()
}

// Name 返回引擎名称
func (e *CudaOnnxNerEngine) Name() string {
	if e.cfg.GPUDeviceID >= 0 {
		return "cuda-onnx-ner-gpu"
	}
	return "cuda-onnx-ner-cpu"
}

// Stats 返回引擎统计
func (e *CudaOnnxNerEngine) Stats() (infer, fallback, batches int64) {
	return atomic.LoadInt64(&e.inferCount),
		atomic.LoadInt64(&e.fallbackCount),
		atomic.LoadInt64(&e.batchCount)
}

// ──────────────────────────────────────────────
// LockOSThread Worker Pool（设计文档 §6.3）
// ──────────────────────────────────────────────

// workerLoop 专职 GPU Worker 循环。
//
// 关键设计（设计文档 §6.3）：
//   - runtime.LockOSThread()：将 Goroutine 锁定到专有 OS 线程，
//     避免 Go Scheduler 在 CGO 调用期间迁移线程导致 CUDA Context 切换。
//   - 动态合批：收集最多 MaxBatch 个任务，或等待超过 BatchWait 后执行。
//   - 优雅停机：监听 stopChan，退出前 drain 队列。
func (e *CudaOnnxNerEngine) workerLoop(workerID int) {
	defer e.wg.Done()

	// 锁定到专有 OS 线程（设计文档 §6.3 核心约束）
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	batch := make([]*NerTask, 0, e.cfg.MaxBatch)
	ticker := time.NewTicker(e.cfg.BatchWait)
	defer ticker.Stop()

	slog.Debug("GPU worker started", "worker_id", workerID)

	for {
		select {
		case <-e.stopChan:
			// 优雅停机：drain 剩余任务
			e.drainWorkerBatch(batch)
			slog.Debug("GPU worker stopped", "worker_id", workerID)
			return

		case task := <-e.taskQueue:
			batch = append(batch, task)
			// 尝试非阻塞收集更多任务
			e.collectTasksNonBlocking(&batch)
			if len(batch) >= e.cfg.MaxBatch {
				e.runBatchInference(batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				e.runBatchInference(batch)
				batch = batch[:0]
			}
		}
	}
}

// collectTasksNonBlocking 非阻塞收集队列中的额外任务
func (e *CudaOnnxNerEngine) collectTasksNonBlocking(batch *[]*NerTask) {
	for len(*batch) < e.cfg.MaxBatch {
		select {
		case task := <-e.taskQueue:
			*batch = append(*batch, task)
		default:
			return
		}
	}
}

// drainWorkerBatch 停机时处理剩余任务
func (e *CudaOnnxNerEngine) drainWorkerBatch(batch []*NerTask) {
	// 处理当前批次
	if len(batch) > 0 {
		e.runBatchInference(batch)
	}
	// 排空队列
	for {
		select {
		case task := <-e.taskQueue:
			e.runBatchInference([]*NerTask{task})
		default:
			return
		}
	}
}

// ──────────────────────────────────────────────
// 安全推理入口（设计文档 §12.5）
// ──────────────────────────────────────────────

// RunInferenceSafe 安全推理入口（带超时降级）。
//
// 流程：
//  1. Tokenizer 编码文本为 InputIDs/AttentionMask/TypeIDs/Offsets
//  2. 构造 NerTask 并投递到 taskQueue
//  3. 等待 ResultChan 返回结果
//  4. 超时 → 降级到 CPU 规则引擎
//  5. 队列满 → 降级到 CPU 规则引擎
func (e *CudaOnnxNerEngine) RunInferenceSafe(ctx context.Context, text string) ([]NerEntity, error) {
	task := &NerTask{
		Text:       text,
		ResultChan: make(chan []NerEntity, 1),
	}

	// Tokenizer 编码
	inputIDs, attnMask, typeIDs, offsets := e.tokenizer.EncodeWithOffsets(text)
	task.InputIDs = inputIDs
	task.AttnMask = attnMask
	task.TypeIDs = typeIDs
	task.Offsets = offsets

	// 非阻塞投递到推理队列
	select {
	case e.taskQueue <- task:
		// 入队成功，等待结果
	case <-ctx.Done():
		atomic.AddInt64(&e.fallbackCount, 1)
		return e.cpuFallback(ctx, text)
	default:
		// 队列满载，平滑降级
		atomic.AddInt64(&e.fallbackCount, 1)
		return e.cpuFallback(ctx, text)
	}

	// 等待推理结果（带超时）
	select {
	case entities := <-task.ResultChan:
		atomic.AddInt64(&e.inferCount, 1)
		return entities, nil
	case <-time.After(e.cfg.InferTimeout):
		atomic.AddInt64(&e.fallbackCount, 1)
		return e.cpuFallback(ctx, text)
	case <-ctx.Done():
		atomic.AddInt64(&e.fallbackCount, 1)
		return e.cpuFallback(ctx, text)
	}
}

// cpuFallback CPU 降级推理（使用规则引擎）
func (e *CudaOnnxNerEngine) cpuFallback(ctx context.Context, text string) ([]NerEntity, error) {
	return e.fallback.Extract(ctx, text)
}

// ──────────────────────────────────────────────
// 批次推理（设计文档 §6.3 runBatchInference）
// ──────────────────────────────────────────────

// runBatchInference 执行一批 ONNX 推理。
//
// 流程：
//  1. 扁平化合批张量 (batch * seq_len)
//  2. 调用 ONNX Runtime 推理
//  3. 对每个任务解码 BIO 实体并回传结果
func (e *CudaOnnxNerEngine) runBatchInference(tasks []*NerTask) {
	atomic.AddInt64(&e.batchCount, 1)
	bSize := len(tasks)
	seqLen := e.cfg.MaxSeqLen
	numClasses := len(e.labelMap)

	// 扁平化合批张量
	flatInputIDs := make([]int64, bSize*seqLen)
	flatAttnMask := make([]int64, bSize*seqLen)
	flatTypeIDs := make([]int64, bSize*seqLen)

	for i, task := range tasks {
		offset := i * seqLen
		copy(flatInputIDs[offset:], padOrTrim(task.InputIDs, seqLen))
		copy(flatAttnMask[offset:], padOrTrim(task.AttnMask, seqLen))
		copy(flatTypeIDs[offset:], padOrTrim(task.TypeIDs, seqLen))
	}

	// 调用 ONNX Runtime 推理
	req := OnnxInferRequest{
		BatchSize: bSize,
		SeqLen:    seqLen,
		InputIDs:  flatInputIDs,
		AttnMask:  flatAttnMask,
		TypeIDs:   flatTypeIDs,
	}

	result, err := e.runtime.Infer(req)
	if err != nil {
		// 推理失败，所有任务降级
		for _, task := range tasks {
			entities, _ := e.fallback.Extract(context.Background(), task.Text)
			task.ResultChan <- entities
		}
		return
	}

	// 解码每个任务的 BIO 实体
	logitsPerSample := seqLen * numClasses
	for i, task := range tasks {
		offset := i * logitsPerSample
		end := offset + logitsPerSample
		if end > len(result.Logits) {
			task.ResultChan <- nil
			continue
		}
		taskLogits := result.Logits[offset:end]
		entities := e.decodeBIOEntities(task.Text, taskLogits, task.Offsets, seqLen, numClasses)
		task.ResultChan <- entities
	}
}

// ──────────────────────────────────────────────
// BIO 实体解码（设计文档 §6.4）
// ──────────────────────────────────────────────

// decodeBIOEntities 从 Logits 解码 BIO 标注实体。
//
// 流程：
//  1. 对每个 Token 位置取 argmax 得到标签 ID
//  2. 通过 labelMap 映射为 BIO 标签
//  3. B-XXX 开始新实体，I-XXX 延续当前实体
//  4. 使用 TokenOffset 对齐原始文本字节
func (e *CudaOnnxNerEngine) decodeBIOEntities(
	text string,
	logits []float32,
	offsets []TokenOffset,
	seqLen int,
	numClasses int,
) []NerEntity {
	var entities []NerEntity
	var curEntity *NerEntity
	var curStart, curEnd int

	for tokenIdx := 0; tokenIdx < seqLen && tokenIdx < len(offsets); tokenIdx++ {
		logitOffset := tokenIdx * numClasses
		if logitOffset+numClasses > len(logits) {
			break
		}
		tokenLogits := logits[logitOffset : logitOffset+numClasses]

		// Argmax 找到最大 Logit 对应的 ClassID
		classID := argmax(tokenLogits)
		if int(classID) >= len(e.labelMap) {
			continue
		}
		label := e.labelMap[classID]

		// 获取 Token 的原始文本偏移
		off := offsets[tokenIdx]
		if off.Start < 0 || off.Start >= len(text) {
			// PAD 或超出范围
			if curEntity != nil {
				entities = append(entities, *curEntity)
				curEntity = nil
			}
			continue
		}

		if strings.HasPrefix(label, "B-") {
			// 开始新实体：先保存旧实体
			if curEntity != nil {
				curEntity.Text = text[curStart:curEnd]
				entities = append(entities, *curEntity)
			}
			tag := strings.TrimPrefix(label, "B-")
			confidence := softmaxScore(tokenLogits, classID)
			curEntity = &NerEntity{
				Label:      tag,
				Start:      off.Start,
				End:        off.End,
				Confidence: confidence,
				Source:     "onnx_gpu",
			}
			curStart = off.Start
			curEnd = off.End
		} else if strings.HasPrefix(label, "I-") && curEntity != nil {
			// 延续当前实体
			tag := strings.TrimPrefix(label, "I-")
			if curEntity.Label == tag {
				curEnd = off.End
			} else {
				// 标签不匹配，结束旧实体
				curEntity.Text = text[curStart:curEnd]
				entities = append(entities, *curEntity)
				curEntity = nil
			}
		} else {
			// "O" 或其他：结束当前实体
			if curEntity != nil {
				curEntity.Text = text[curStart:curEnd]
				entities = append(entities, *curEntity)
				curEntity = nil
			}
		}
	}

	// 收尾
	if curEntity != nil {
		curEntity.Text = text[curStart:curEnd]
		entities = append(entities, *curEntity)
	}

	return entities
}

// ──────────────────────────────────────────────
// 纯 Go 辅助函数
// ──────────────────────────────────────────────

// argmax 返回切片中最大值的索引
func argmax(vals []float32) int32 {
	if len(vals) == 0 {
		return 0
	}
	maxIdx := 0
	maxVal := vals[0]
	for i := 1; i < len(vals); i++ {
		if vals[i] > maxVal {
			maxVal = vals[i]
			maxIdx = i
		}
	}
	return int32(maxIdx)
}

// softmaxScore 计算指定类别的 softmax 概率值
func softmaxScore(logits []float32, classID int32) float64 {
	if len(logits) == 0 {
		return 0
	}
	// 数值稳定 softmax: 减去最大值避免 exp 溢出
	maxVal := float64(logits[0])
	for _, v := range logits[1:] {
		if float64(v) > maxVal {
			maxVal = float64(v)
		}
	}
	var sumExp float64
	var targetExp float64
	for i, v := range logits {
		exp := math.Exp(float64(v) - maxVal)
		sumExp += exp
		if int32(i) == classID {
			targetExp = exp
		}
	}
	if sumExp == 0 {
		return 0
	}
	return targetExp / sumExp
}

// padOrTrim 将切片填充或截断到指定长度
func padOrTrim(src []int64, length int) []int64 {
	if len(src) >= length {
		return src[:length]
	}
	result := make([]int64, length)
	copy(result, src)
	return result
}
