// Package grpcserver implements the gRPC service interface for service-hub with mTLS support.
// Package grpcserver 实现 service-hub 的 gRPC 高性能远程调用接口层，支持 TLS 1.3 双向认证（mTLS）与公钥指纹固定（Pinned Public Key）。
//
// 核心能力与安全特性：
// 1. 高性能 RPC：基于 google.golang.org/grpc 提供微秒级内部服务通信；
// 2. 企业级 mTLS：支持 RequireAndVerifyClientCert 双向证书认证与动态 CA 根证书挂载；
// 3. 公钥固定（Public Key Pinning）：支持 RSA、ECDSA、Ed25519 客户端公钥证书比对，防御证书伪造攻击；
// 4. 拦截器链：提供 Unary/Stream 统一 Panic 恢复（Recovery）与结构化访问日志（Logging）拦截器；
// 5. 6 阶段流水线驱动：在后台协程中安全驱动 6 阶段流通治理任务，支持并发信号量限流与优雅停机（Graceful Shutdown）。
package grpcserver

import (
	"context"
	"crypto"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	naming "github.com/fengzhizi319/PrivShield-go/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"
	"github.com/fengzhizi319/PrivShield-go/pkg/validation"

	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/audit"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/datasource"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/models"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/retry"
	pb "github.com/fengzhizi319/PrivShield-go/services/service-hub/proto"
)

const moduleVia = "service-hub-grpc"

// GRPCServer implements the ServiceHubService gRPC service.
// GRPCServer 结构体实现 Protobuf 生成的 ServiceHubServiceServer 接口，管理核心客户端与生命周期。
type GRPCServer struct {
	pb.UnimplementedServiceHubServiceServer

	agent      *agent.Client      // 上游 PrivShield Python Agent 客户端
	datasource *datasource.Client // 下游 datasource-mgr 客户端
	audit      *audit.Client      // audit-log 存证客户端（P0-6：出域 ↔ 留痕强绑定）
	cfg        *config.Config     // 模块全局运行配置
	startTime  time.Time          // 服务启动时间戳
	tasks      store.TaskStore    // 任务持久化仓库接口
	logger     *slog.Logger       // 结构化日志记录器
	taskSem    chan struct{}      // 限制后台并发任务协程数的信号量（默认容量 10）
	ctx        context.Context    // 优雅停机广播上下文
	cancel     context.CancelFunc // 触发停机 Context 取消的回调函数
	wg         sync.WaitGroup     // 跟踪记录正在运行的任务协程计数
}

// New creates a new GRPCServer instance.
// New 构造函数初始化 GRPCServer 实例，配置并发信号量与取消上下文。
//
// 存证客户端与 REST 侧同源：一律由 cfg 装配，未配置端点时实例仍保留，
// 提交必然返回 audit.ErrNotConfigured，由流水线 audit 阶段判定任务失败（fail-closed）。
func New(ag *agent.Client, ds *datasource.Client, cfg *config.Config, tasks store.TaskStore, logger *slog.Logger) *GRPCServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &GRPCServer{
		agent:      ag,
		datasource: ds,
		audit:      audit.New(cfg, nil),
		cfg:        cfg,
		startTime:  time.Now(),
		tasks:      tasks,
		logger:     logger,
		taskSem:    make(chan struct{}, 10), // 最大限制 10 个并发异步任务
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Shutdown gracefully stops all in-flight task goroutines.
// Shutdown 优雅停机方法：通知所有在途 gRPC 任务协程安全退出并阻塞等待收敛。
func (s *GRPCServer) Shutdown() {
	s.cancel()
	s.wg.Wait()
}

// StartLeaseWorker starts the PostgreSQL-backed task consumer used by all
// service-hub ingress protocols. It must only be enabled for a backend that
// provides real lease semantics.
func (s *GRPCServer) StartLeaseWorker(owner string, leaseTTL time.Duration) error {
	leasedStore, ok := s.tasks.(store.LeasedTaskStore)
	if !ok {
		return fmt.Errorf("task store does not support leases")
	}
	if leaseTTL <= 0 {
		return fmt.Errorf("lease TTL must be positive")
	}

	s.wg.Add(1)
	go s.leaseWorkerLoop(leasedStore, owner, leaseTTL)
	s.logger.Info("postgresql lease worker started", "owner", owner, "lease_ttl", leaseTTL.String())
	return nil
}

// StartLocalWorker starts a local worker loop for consuming pending/recovered tasks in SQLite/memory mode.
func (s *GRPCServer) StartLocalWorker() error {
	s.wg.Add(1)
	go s.localWorkerLoop()
	s.logger.Info("gRPC local pending task worker started (SQLite/memory mode)")
	return nil
}

func (s *GRPCServer) localWorkerLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.processPendingTasks()
		}
	}
}

func (s *GRPCServer) processPendingTasks() {
	pendingTasks, _, err := s.tasks.List(store.TaskFilter{Status: "pending", Limit: 10})
	if err != nil || len(pendingTasks) == 0 {
		return
	}

	for i := range pendingTasks {
		task := pendingTasks[i]
		if task.RetryAfter != nil && time.Now().Before(*task.RetryAfter) {
			continue
		}

		now := time.Now()
		task.Status = "running"
		task.Stage = "ingest"
		task.StartedAt = &now
		if err := s.persistTask(&task, "local worker claim"); err != nil {
			continue
		}

		s.wg.Add(1)
		go func(t store.Task) {
			defer s.wg.Done()
			reqID := validation.GenerateID("retry")
			s.processTask(&t, t.Operation, t.PayloadJSON, reqID)
		}(task)
	}
}

// extractRequestID extracts x-request-id from gRPC metadata or generates a new one.
func extractRequestID(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("x-request-id"); len(vals) > 0 && vals[0] != "" {
			return vals[0]
		}
	}
	return validation.GenerateID("grpc-req")
}

// persistTask writes a task state transition before the pipeline continues.
func (s *GRPCServer) persistTask(task *store.Task, transition string) error {
	if err := s.tasks.Update(task); err != nil {
		s.logger.Error("failed to persist task state",
			"task_id", task.ID,
			"transition", transition,
			"status", task.Status,
			"stage", task.Stage,
			"error", err.Error())
		return err
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// gRPC Service Methods / gRPC 服务方法
// ─────────────────────────────────────────────────────────────

// Health checks self + upstream agent connectivity.
// Health 实现 gRPC 健康检查接口：检测自身并向 Python Agent 发起健康探测。
func (s *GRPCServer) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	start := time.Now()
	healthCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	agentData, err := s.agent.Health(healthCtx)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return &pb.HealthResponse{
			Backend:   "ok",
			Agent:     "unreachable",
			AgentUrl:  s.cfg.AgentBaseURL(),
			LatencyMs: latency,
			Error:     err.Error(),
			Via:       moduleVia,
		}, nil
	}

	agentStr := fmt.Sprintf("%v", agentData["status"])
	return &pb.HealthResponse{
		Backend:   "ok",
		Agent:     agentStr,
		AgentUrl:  s.cfg.AgentBaseURL(),
		LatencyMs: latency,
		Via:       moduleVia,
	}, nil
}

// HubStatus returns the scheduling hub status overview.
// HubStatus 实现调度中枢状态概览 RPC 方法：返回排队、活跃、成功与失败任务计数。
func (s *GRPCServer) HubStatus(ctx context.Context, req *pb.HubStatusRequest) (*pb.HubStatusResponse, error) {
	counts, err := s.tasks.Counts()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get task counts: %v", err)
	}

	return &pb.HubStatusResponse{
		Status:         "running",
		Uptime:         time.Since(s.startTime).Round(time.Second).String(),
		ActiveTasks:    int32(counts.Running),
		QueuedTasks:    int32(counts.Pending),
		CompletedTotal: int32(counts.Completed),
		FailedTotal:    int32(counts.Failed),
		AgentUrl:       s.cfg.AgentBaseURL(),
	}, nil
}

// Dispatch dispatches a new task to the scheduling pipeline.
// Dispatch 实现显式分发任务 RPC 方法：
// 1. 校验 source 非空、限长 1024 字符，并经 naming 归一化为 canonical datasource_id；
// 2. operation 为可选的调用方「强度请求」，非空时必须属于有效算子词表；
// 3. 持久化任务为 pending 状态；
// 4. 异步拉起 6 阶段流水线协程处理任务并返回 accepted。
//
// 与 REST 分发路径完全同构（P1-1 双路径一致性）：生效算子一律在 ③ classify 阶段
// 由引擎定级结果推导，调用方的 operation 只允许上调保护强度，定级缺失即任务失败。
func (s *GRPCServer) Dispatch(ctx context.Context, req *pb.DispatchRequest) (*pb.DispatchResponse, error) {
	// 字段合法性校验
	rawSource := strings.TrimSpace(req.Source)
	if rawSource == "" {
		return nil, status.Error(codes.InvalidArgument, "source must not be empty")
	}
	if len(rawSource) > 1024 {
		return nil, status.Error(codes.InvalidArgument, "source exceeds maximum length of 1024 characters")
	}

	normID, normErr := naming.ResolveInbound(rawSource)
	if normErr != nil {
		if naming.IsReserved(normErr) {
			return nil, status.Errorf(codes.FailedPrecondition, "reserved source: %v", normErr)
		}
		return nil, status.Errorf(codes.InvalidArgument, "invalid source: %v", normErr)
	}
	normAPICode := naming.APICodeForDataSource(normID)

	operation := strings.TrimSpace(req.Operation)
	if operation != "" {
		if err := validation.AllowedValues("operation", operation, validation.HubOperations); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
	}

	taskID := validation.GenerateID("task")
	now := time.Now()

	initialStatus := "pending"
	stage := "queued"
	var startedAt *time.Time
	if !s.usesLeaseWorker() {
		initialStatus = "running"
		stage = "ingest"
		startedAt = &now
	}

	task := &store.Task{
		ID:           taskID,
		Status:       initialStatus,
		Stage:        stage,
		Source:       normID,
		DatasourceID: normID,
		APICode:      normAPICode,
		Operation:    operation,
		Priority:     int(req.Priority),
		CreatedAt:    now,
		StartedAt:    startedAt,
		PayloadJSON:  req.PayloadJson,
	}

	if err := s.tasks.Save(task); err != nil {
		return nil, status.Errorf(codes.Internal, "save task: %v", err)
	}

	requestID := extractRequestID(ctx)

	if !s.usesLeaseWorker() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.processTask(task, req.Operation, req.PayloadJson, requestID)
		}()
	}

	return &pb.DispatchResponse{
		TaskId: taskID,
		Status: "accepted",
		Via:    moduleVia,
	}, nil
}

// ClassifyAndDispatch performs classification first, then auto-dispatches based on sensitivity.
// ClassifyAndDispatch 动态分类定级并自动分发 RPC 方法：
// 1. 调用 Agent Classify 接口完成敏感等级评估；
// 3. 自适应决策脱敏算子并启动流水线任务。
func (s *GRPCServer) ClassifyAndDispatch(ctx context.Context, req *pb.ClassifyAndDispatchRequest) (*pb.ClassifyAndDispatchResponse, error) {
	if strings.TrimSpace(req.Source) == "" {
		return nil, status.Error(codes.InvalidArgument, "source must not be empty")
	}
	if len(req.Source) > 1024 {
		return nil, status.Error(codes.InvalidArgument, "source exceeds maximum length of 1024 characters")
	}

	normID, normErr := naming.ResolveInbound(req.Source)
	if normErr != nil {
		if naming.IsReserved(normErr) {
			return nil, status.Errorf(codes.FailedPrecondition, "reserved source: %v", normErr)
		}
		return nil, status.Errorf(codes.InvalidArgument, "invalid source: %v", normErr)
	}
	normAPICode := naming.APICodeForDataSource(normID)

	requestID := extractRequestID(ctx)

	payloadJSON := req.PayloadJson

	classifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	classifyCtx = pkgobs.ContextWithRequestID(classifyCtx, requestID)
	classifyCtx = agent.ContextWithIdempotencyKey(classifyCtx, fmt.Sprintf("hub-classify-%s", normID))
	defer cancel()

	classifyResult, err := s.agent.Classify(classifyCtx, agent.ToRecords(payloadJSON))
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "classification failed: %v", err)
	}

	// 定级一律由引擎给出；读不到可识别的 L1~L5 级别即拒绝派发。
	// 历史上这里回退到硬编码 "L2"，等价于「分类失败时自行降低安全等级」（P1-1 消除）。
	level := audit.MaxSensitivityLevel(classifyResult)
	if level == "" {
		return nil, status.Error(codes.FailedPrecondition,
			"classification returned no recognizable security level (L1~L5); dispatch refused")
	}

	operation := models.LevelToOperation(level)
	priority := levelToPriority(level)

	taskID := validation.GenerateID("task")
	now := time.Now()

	task := &store.Task{
		ID:           taskID,
		APICode:      normAPICode,
		DatasourceID: normID,
		Status:       "pending",
		Stage:        "queued",
		Source:       normID,
		Operation:    operation,
		Priority:     priority,
		CreatedAt:    now,
		PayloadJSON:  payloadJSON,
	}

	if err := s.tasks.Save(task); err != nil {
		return nil, status.Errorf(codes.Internal, "save task: %v", err)
	}

	classifyResultJSON, _ := json.Marshal(classifyResult)

	if !s.usesLeaseWorker() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.processTask(task, operation, payloadJSON, requestID)
		}()
	}

	return &pb.ClassifyAndDispatchResponse{
		TaskId:             taskID,
		Level:              level,
		AutoOperation:      operation,
		ClassifyResultJson: string(classifyResultJSON),
		Via:                moduleVia,
	}, nil
}

// GetTask returns the details of a single task by ID.
// GetTask 根据 TaskID 查询单个任务详情，若不存在返回 NotFound 错误码。
func (s *GRPCServer) GetTask(ctx context.Context, req *pb.GetTaskRequest) (*pb.TaskProto, error) {
	taskID := strings.TrimSpace(req.GetTaskId())
	if taskID == "" {
		return nil, status.Error(codes.InvalidArgument, "task id must not be empty")
	}

	task, err := s.tasks.Get(taskID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "task %s not found", taskID)
	}

	return taskToProto(task), nil
}

// ListTasks returns all tasks, optionally filtered by status.
// ListTasks 分页获取任务列表，支持状态过滤白名单校验。
func (s *GRPCServer) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.ListTasksResponse, error) {
	statusFilter := req.GetStatusFilter()
	if statusFilter != "" {
		if err := validation.AllowedValues("status", statusFilter, validation.TaskStatuses); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
	}

	tasks, total, err := s.tasks.List(store.TaskFilter{
		Status: statusFilter,
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list tasks: %v", err)
	}

	protos := make([]*pb.TaskProto, len(tasks))
	for i := range tasks {
		protos[i] = taskToProto(&tasks[i])
	}

	return &pb.ListTasksResponse{
		Total: int32(total),
		Tasks: protos,
		Via:   moduleVia,
	}, nil
}

// PipelineStatus returns the current status of each pipeline stage.
// PipelineStatus 获取流水线 6 个阶段的实时任务活跃度与 Agent 连通性。
func (s *GRPCServer) PipelineStatus(ctx context.Context, req *pb.PipelineStatusRequest) (*pb.PipelineStatusResponse, error) {
	runningTasks, _, err := s.tasks.List(store.TaskFilter{Status: "running", Limit: 1000})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list running tasks: %v", err)
	}

	stageNames := []string{"ingest", "fetch", "classify", "desensitize", "return", "audit"}
	stageCounts := make(map[string]int)
	for _, t := range runningTasks {
		stageCounts[t.Stage]++
	}

	stages := make([]*pb.PipelineStageProto, len(stageNames))
	for i, name := range stageNames {
		st := "idle"
		if stageCounts[name] > 0 {
			st = "processing"
		}
		stages[i] = &pb.PipelineStageProto{
			Name:        name,
			Status:      st,
			ActiveCount: int32(stageCounts[name]),
		}
	}

	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, agentErr := s.agent.Health(healthCtx)

	return &pb.PipelineStatusResponse{
		Stages:  stages,
		AgentOk: agentErr == nil,
	}, nil
}

// FetchAndDesensitize synchronously fetches a record by ID card from datasource-mgr,
// runs engine classification + desensitization, and returns the result.
// FetchAndDesensitize gRPC 同步端到端接口：按身份证号从 datasource-mgr 拉取单条记录，
// 调用 engine 完成 3-Layer 分类分级 + PII 脱敏，同步返回脱敏结果与分类报告。
func (s *GRPCServer) FetchAndDesensitize(ctx context.Context, req *pb.FetchAndDesensitizeRequest) (*pb.FetchAndDesensitizeResponse, error) {
	datasourceID := strings.TrimSpace(req.DatasourceId)
	if datasourceID == "" {
		return nil, status.Error(codes.InvalidArgument, "datasource_id is required")
	}
	idCardNo := strings.TrimSpace(req.IdCardNo)
	if len(idCardNo) != 18 {
		return nil, status.Error(codes.InvalidArgument, "id_card_no must be exactly 18 characters")
	}

	normID, err := naming.ResolveInbound(datasourceID)
	if err != nil {
		if naming.IsReserved(err) {
			return nil, status.Errorf(codes.FailedPrecondition, "reserved datasource: %v", err)
		}
		return nil, status.Errorf(codes.InvalidArgument, "invalid datasource: %v", err)
	}

	requestID := extractRequestID(ctx)
	rpcCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rpcCtx = pkgobs.ContextWithRequestID(rpcCtx, requestID)

	// ① 从 datasource-mgr 按身份证号拉取单条记录
	if s.datasource == nil {
		return nil, status.Error(codes.Unavailable, "datasource client not configured")
	}

	fetchResult, err := s.datasource.FetchRecordByIDCard(rpcCtx, normID, idCardNo)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "failed to fetch record: %v", err)
	}

	found, _ := fetchResult["found"].(bool)
	if !found {
		return &pb.FetchAndDesensitizeResponse{
			DatasourceId: normID,
			IdCardNo:     idCardNo,
			Found:        false,
			Via:          moduleVia,
		}, nil
	}

	record, ok := fetchResult["record"].(map[string]any)
	if !ok || len(record) == 0 {
		return &pb.FetchAndDesensitizeResponse{
			DatasourceId: normID,
			IdCardNo:     idCardNo,
			Found:        false,
			Via:          moduleVia,
		}, nil
	}

	// ② 调用 engine 完成分类分级 + 脱敏
	records := agent.ToRecords(record)
	if len(records) == 0 {
		return nil, status.Error(codes.Internal, "failed to convert record for engine processing")
	}

	idempotencyKey := fmt.Sprintf("hub-fad-%s-%s", normID, idCardNo)
	rpcCtx = agent.ContextWithIdempotencyKey(rpcCtx, idempotencyKey)

	result, err := s.agent.ProcessAgent(rpcCtx, records, normID)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "engine processing failed: %v", err)
	}

	level := result.Level
	if level == "" {
		level = audit.MaxSensitivityLevel(result.ClassificationReport)
	}

	sanitizedJSON, _ := json.Marshal(result.SanitizedData)
	classifyJSON, _ := json.Marshal(result.ClassificationReport)
	summaryJSON, _ := json.Marshal(result.Summary)

	return &pb.FetchAndDesensitizeResponse{
		DatasourceId:          normID,
		IdCardNo:              idCardNo,
		Found:                 true,
		Level:                 level,
		SanitizedDataJson:     string(sanitizedJSON),
		ClassificationReportJson: string(classifyJSON),
		SummaryJson:           string(summaryJSON),
		Via:                   moduleVia,
	}, nil
}

// ─────────────────────────────────────────────────────────────
// Internal helpers / 内部辅助方法
// ─────────────────────────────────────────────────────────────

// processTask simulates the scheduling pipeline stages.
// processTask 内部异步流水线执行器：
// 顺序流转 ingest ➔ fetch ➔ classify ➔ desensitize ➔ return ➔ audit 6 个阶段。
// 其中 ⑥ audit 阶段真实向 audit-log 提交含 task_id / api_code / datasource_id /
// 输入输出指纹的出域存证（P0-6）；提交失败一律按任务 failed 处理，绝不静默推进至 done。
func (s *GRPCServer) processTask(task *store.Task, operation, payloadJSON string, requestID string) {
	if requestID == "" {
		requestID = validation.GenerateID("grpc-task")
	}

	s.taskSem <- struct{}{}
	defer func() { <-s.taskSem }()

	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("processTask panic recovered",
				"task_id", task.ID, "panic", fmt.Sprintf("%v", r))
			task.Status = "failed"
			task.Error = fmt.Sprintf("internal panic: %v", r)
			task.ErrorClass = retry.ClassInternal
			now := time.Now()
			task.CompletedAt = &now
			task.DurationMs = now.Sub(task.CreatedAt).Milliseconds()
			_ = s.persistTask(task, "panic recovery")
		}
	}()

	stages := []string{"ingest", "fetch", "classify", "desensitize", "return", "audit"}

	// 出域事实（供 ⑥ 存证使用）：③ 阶段引擎返回的脱敏结果与其中最高敏感级别。
	var (
		egressOutput  any
		egressLevel   string
		egressHashIn  string
		egressHashOut string
	)

	for _, stage := range stages {
		task.Stage = stage
		task.Status = "running"
		now := time.Now()
		task.StartedAt = &now
		if err := s.persistTask(task, "stage started"); err != nil {
			return
		}

		select {
		case <-time.After(100 * time.Millisecond):
		case <-s.ctx.Done():
			task.Status = "failed"
			task.Error = "server shutting down"
			task.ErrorClass = retry.ClassShutdown
			now := time.Now()
			task.CompletedAt = &now
			task.DurationMs = now.Sub(task.CreatedAt).Milliseconds()
			_ = s.persistTask(task, "shutdown failure")
			return
		}

		// Stage 2: fetch → 数据源拉取阶段保留（分页抽取接口已移除，需由调用方在提交任务时携带载荷）

		// Stage 3: classify → 分类+脱敏一体化，一次调用 engine 医疗流水线
		//
		// P1-1 权限收敛：与 REST 路径同构 —— 是否脱敏与采用哪个算子由引擎定级决定，
		// 调用方传入的 operation 只能上调保护强度，不能下调；定级缺失即任务失败。
		if stage == "classify" {
			ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
			ctx = pkgobs.ContextWithRequestID(ctx, requestID)
			idempotencyKey := fmt.Sprintf("hub-%s-%s-%d", task.ID, stage, task.RetryCount)
			ctx = agent.ContextWithIdempotencyKey(ctx, idempotencyKey)
			records := agent.ToRecords(payloadJSON)
			if len(records) > 0 {
				result, err := s.agent.ProcessAgent(ctx, records, task.DatasourceID)
				cancel()
				if err != nil {
					task.Status = "failed"
					task.Error = fmt.Sprintf("medical pipeline failed at stage %s: %v", stage, err)
					task.ErrorClass, _ = retry.Classify(err, retry.BiasDownstream)
					now := time.Now()
					task.CompletedAt = &now
					task.DurationMs = now.Sub(task.CreatedAt).Milliseconds()
					_ = s.persistTask(task, "medical pipeline failure")
					return
				}
				if result == nil {
					result = &agent.MedicalProcessResult{}
				}

				level := result.Level
				if level == "" {
					level = audit.MaxSensitivityLevel(result.ClassificationReport)
				}
				if level == "" {
					task.Status = "failed"
					task.Error = fmt.Sprintf("classification failed at stage %s: engine returned no security level", stage)
					task.ErrorClass = retry.ClassContract
					now := time.Now()
					task.CompletedAt = &now
					task.DurationMs = now.Sub(task.CreatedAt).Milliseconds()
					_ = s.persistTask(task, "classification level unavailable")
					return
				}

				derived := models.LevelToOperation(level)
				applied := models.EffectiveOperation(operation, derived)
				if applied != operation {
					s.logger.Warn("caller-requested operation overridden by classification result (P1-1 fail-closed)",
						"task_id", task.ID,
						"requested_operation", operation,
						"security_level", level,
						"applied_operation", applied)
				}
				operation = applied
				task.Operation = applied
				egressLevel = level

				// 记录真实出域事实：脱敏后载荷与引擎侧输入/输出指纹。
				if len(result.SanitizedData) > 0 {
					egressOutput = result.SanitizedData
				}
				egressHashIn, egressHashOut = audit.EngineFingerprints(result.Summary)
			} else {
				cancel()
			}
		}

		// Stage 4: desensitize → 已由 ③ 医疗流水线合并完成，快速通过

		// Stage 6: audit → 出域与不可篡改留痕强绑定（P0-6 / G-05）。
		// 提交失败必然使任务终态 failed：不存在「已出域但无存证仍 done」的路径。
		if stage == "audit" {
			if evErr := s.submitEvidence(s.ctx, task, "grpc", payloadJSON, egressOutput, egressLevel, egressHashIn, egressHashOut); evErr != nil {
				task.Status = "failed"
				task.Error = fmt.Sprintf("audit evidence submission failed at stage %s: %v", stage, evErr)
				task.ErrorClass, _ = audit.FailureClass(evErr)
				now := time.Now()
				task.CompletedAt = &now
				task.DurationMs = now.Sub(task.CreatedAt).Milliseconds()
				_ = s.persistTask(task, "audit evidence failure")
				return
			}
		}
	}

	task.Status = "completed"
	task.Stage = "done"
	now := time.Now()
	task.CompletedAt = &now
	task.DurationMs = now.Sub(task.CreatedAt).Milliseconds()
	_ = s.persistTask(task, "task completed")
}

// submitEvidence performs the single outbound-flow evidence write of stage ⑥.
// submitEvidence 执行 ⑥ 审计存证阶段唯一的出域留痕提交（POST /api/audit/logs）。
//
// 返回的 error 一旦非空即代表「这次出域没有被证明留痕」，调用方 MUST 让任务失败：
// gRPC 直连与本地工作器路径写 task.Status=failed 并落盘，租约路径返回 *store.TaskFailure。
// parent 只用于停机传播；每次提交独立受 SERVICE_HUB_AUDIT_LOG_TIMEOUT 约束。
func (s *GRPCServer) submitEvidence(parent context.Context, task *store.Task, protocol, payloadJSON string, output any, level, inHash, outHash string) error {
	timeout := s.cfg.AuditLogTimeoutDuration()
	evCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	evCtx = pkgobs.ContextWithRequestID(evCtx, task.ID)
	evCtx = agent.ContextWithIdempotencyKey(evCtx, fmt.Sprintf("hub-%s-audit-%d", task.ID, task.RetryCount))

	_, err := audit.RecordOutboundEvidence(evCtx, s.audit, audit.OutboundFlow{
		Task:          task,
		Protocol:      protocol,
		SecurityLevel: level,
		Input:         payloadJSON,
		Output:        output,
		InputHash:     inHash,
		OutputHash:    outHash,
	})
	if err != nil {
		s.logger.Error("outbound evidence submission failed; task marked failed (P0-6 fail-closed)",
			"task_id", task.ID,
			"datasource_id", task.DatasourceID,
			"api_code", task.APICode,
			"operation", task.Operation,
			"protocol", protocol,
			"error", err.Error())
	}
	return err
}

func (s *GRPCServer) usesLeaseWorker() bool {
	return s.cfg.PGDSN != ""
}

func (s *GRPCServer) leaseWorkerLoop(tasks store.LeasedTaskStore, owner string, leaseTTL time.Duration) {
	defer s.wg.Done()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		if reclaimed, err := tasks.RequeueExpiredLeases(100); err != nil {
			s.logger.Error("failed to requeue expired leases", "error", err.Error())
		} else if reclaimed > 0 {
			s.logger.Warn("requeued expired task leases", "count", reclaimed)
		}

		lease, err := tasks.ClaimNext(owner, leaseTTL)
		if err != nil {
			s.logger.Error("failed to claim pending task", "error", err.Error())
		} else if lease != nil {
			s.runLeasedTask(tasks, lease, leaseTTL)
		}

		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *GRPCServer) runLeasedTask(tasks store.LeasedTaskStore, lease *store.TaskLease, leaseTTL time.Duration) {
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	renewalDone := make(chan struct{})
	go func() {
		defer close(renewalDone)
		interval := leaseTTL / 2
		if interval < time.Second {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				valid, err := tasks.RenewLease(lease.Task.ID, lease.Owner, lease.Token, leaseTTL)
				if err != nil || !valid {
					s.logger.Warn("task lease lost while executing",
						"task_id", lease.Task.ID, "error", err)
					cancel()
					return
				}
			}
		}
	}()

	failure := s.executeLeasedTask(ctx, lease.Task)
	cancel()
	<-renewalDone

	if failure == nil {
		completed, err := tasks.CompleteLease(lease.Task.ID, lease.Owner, lease.Token, store.TaskResult{Stage: "done"})
		if err != nil || !completed {
			s.logger.Warn("could not complete leased task", "task_id", lease.Task.ID, "error", err)
		}
		return
	}

	failed, err := tasks.FailLease(lease.Task.ID, lease.Owner, lease.Token, *failure)
	if err != nil || !failed {
		s.logger.Warn("could not fail leased task", "task_id", lease.Task.ID, "error", err)
	}
}

func (s *GRPCServer) executeLeasedTask(ctx context.Context, task *store.Task) (failure *store.TaskFailure) {
	defer func() {
		if recovered := recover(); recovered != nil {
			failure = &store.TaskFailure{Error: fmt.Sprintf("internal panic: %v", recovered), ErrorClass: retry.ClassInternal}
		}
	}()

	payloadJSON := task.PayloadJSON

	// 出域事实（供 ⑥ 存证使用）：③ 阶段引擎返回的脱敏结果与其中最高敏感级别。
	var (
		egressOutput  any
		egressLevel   string
		egressHashIn  string
		egressHashOut string
	)

	for _, stage := range []string{"ingest", "fetch", "classify", "desensitize", "return", "audit"} {
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return &store.TaskFailure{Error: "lease worker shutting down", Retryable: true, ErrorClass: retry.ClassShutdown}
		}

		// Stage 2: fetch → 数据源拉取阶段保留（分页抽取接口已移除，需由调用方在提交任务时携带载荷）

		if stage == "classify" {
			records := agent.ToRecords(payloadJSON)
			if len(records) == 0 {
				continue
			}
			processCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			processCtx = pkgobs.ContextWithRequestID(processCtx, task.ID)
			idempotencyKey := fmt.Sprintf("hub-%s-%s-%d", task.ID, stage, task.RetryCount)
			processCtx = agent.ContextWithIdempotencyKey(processCtx, idempotencyKey)
			result, err := s.agent.ProcessAgent(processCtx, records, task.DatasourceID)
			cancel()
			if err != nil {
				class, canRetry := retry.Classify(err, retry.BiasDownstream)
				return &store.TaskFailure{Error: fmt.Sprintf("medical pipeline failed: %v", err), Retryable: canRetry, ErrorClass: class}
			}
			if result == nil {
				result = &agent.MedicalProcessResult{}
			}

			// P1-1：租约工作器同样只认引擎定级，调用方算子仅可上调不可下调。
			level := result.Level
			if level == "" {
				level = audit.MaxSensitivityLevel(result.ClassificationReport)
			}
			if level == "" {
				return &store.TaskFailure{
					Error:      "classification returned no security level; refusing to egress unclassified data",
					Retryable:  false,
					ErrorClass: retry.ClassContract,
				}
			}
			applied := models.EffectiveOperation(task.Operation, models.LevelToOperation(level))
			if applied != task.Operation {
				s.logger.Warn("leased task operation overridden by classification result (P1-1 fail-closed)",
					"task_id", task.ID,
					"requested_operation", task.Operation,
					"security_level", level,
					"applied_operation", applied)
			}
			task.Operation = applied
			egressLevel = level

			if len(result.SanitizedData) > 0 {
				egressOutput = result.SanitizedData
			}
			egressHashIn, egressHashOut = audit.EngineFingerprints(result.Summary)
		}

		// Stage 6: audit → 出域与不可篡改留痕强绑定（P0-6 / G-05）。
		// 存证未被受理时绝不返回 nil failure：调用方因此不会执行 CompleteLease。
		// 契约级拒绝与「未配置存证端点」重试无意义（Retryable=false），
		// 仅 audit-log 暂时不可用（5xx/网络）才整任务重投。
		if stage == "audit" {
			if evErr := s.submitEvidence(ctx, task, "lease", payloadJSON, egressOutput, egressLevel, egressHashIn, egressHashOut); evErr != nil {
				errorClass, retryable := audit.FailureClass(evErr)
				return &store.TaskFailure{
					Error:      fmt.Sprintf("audit evidence submission failed: %v", evErr),
					Retryable:  retryable,
					ErrorClass: errorClass,
				}
			}
		}
	}
	return nil
}

// taskToProto converts a store.Task domain model to its Protobuf representation.
// taskToProto 将领域实体 Task 转换为 gRPC Protobuf 传输对象 TaskProto。
func taskToProto(t *store.Task) *pb.TaskProto {
	proto := &pb.TaskProto{
		Id:         t.ID,
		Status:     t.Status,
		Stage:      t.Stage,
		Source:     t.Source,
		Operation:  t.Operation,
		CreatedAt:  t.CreatedAt.Format(time.RFC3339Nano),
		Error:      t.Error,
		DurationMs: t.DurationMs,
	}
	if t.StartedAt != nil {
		proto.StartedAt = t.StartedAt.Format(time.RFC3339Nano)
	}
	if t.CompletedAt != nil {
		proto.CompletedAt = t.CompletedAt.Format(time.RFC3339Nano)
	}
	return proto
}

// levelToPriority maps a sensitivity level to a scheduling priority score.
// 入参容忍两套词表（L1~L5 标识与引擎 canonical 名称），归一化统一由 pkg/naming 完成；
// 无法识别的级别返回 0，由调用方按 fail-closed 处理，不再冒充 L2 的中性优先级。
func levelToPriority(level string) int {
	switch naming.NormalizeSecurityLevelID(level) {
	case naming.SecurityLevelL1:
		return 10
	case naming.SecurityLevelL2:
		return 40
	case naming.SecurityLevelL3:
		return 60
	case naming.SecurityLevelL4:
		return 80
	case naming.SecurityLevelL5:
		return 100
	default:
		return 0
	}
}

// ─────────────────────────────────────────────────────────────
// mTLS Credentials Builder / mTLS 凭证构造
// ─────────────────────────────────────────────────────────────

// BuildServerCredentials constructs gRPC transport credentials supporting TLS 1.3, mTLS client auth, and public key pinning.
// BuildServerCredentials 委托共享 tlsutil.BuildServerTLSConfig 构造 gRPC TLS 传输凭证，
// 支持 TLS 1.3 强制最低版本、mTLS 双向认证与公钥固定。
func BuildServerCredentials(cfg *config.Config) (credentials.TransportCredentials, error) {
	tlsCfg := &tlsutil.ServerTLSConfig{
		Enabled:          cfg.TLSEnabled,
		CertFile:         cfg.TLSCertFile,
		KeyFile:          cfg.TLSKeyFile,
		CAFile:           cfg.TLSCAFile,
		ClientAuth:       cfg.TLSClientAuth,
		PinnedPubKeyFile: cfg.TLSPinnedPubKeyFile,
	}
	tlsConfig, err := tlsutil.BuildServerTLSConfig(tlsCfg)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(tlsConfig), nil
}

// loadPublicKey loads a public key from PEM file (delegates to tlsutil.LoadPublicKey).
// loadPublicKey 委托共享 tlsutil.LoadPublicKey 从 PEM 文件中解析公钥。
func loadPublicKey(path string) (crypto.PublicKey, error) {
	return tlsutil.LoadPublicKey(path)
}

// publicKeysEqual checks if two public keys are identical (delegates to tlsutil.PublicKeysEqual).
// publicKeysEqual 委托共享 tlsutil.PublicKeysEqual 比对两个公钥。
func publicKeysEqual(a, b crypto.PublicKey) bool {
	return tlsutil.PublicKeysEqual(a, b)
}

// ─────────────────────────────────────────────────────────────
// Interceptors / 拦截器
// ─────────────────────────────────────────────────────────────

// UnaryLoggingInterceptor logs method name, status code, latency, and client peer address.
// UnaryLoggingInterceptor 记录一元 RPC 调用的方法名、耗时、状态码与客户端来源 IP。
func UnaryLoggingInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		var clientPeer string
		if p, ok := peer.FromContext(ctx); ok {
			clientPeer = p.Addr.String()
		}

		resp, err := handler(ctx, req)
		latency := time.Since(start)

		grpcCode := codes.OK
		if err != nil {
			if s, ok := status.FromError(err); ok {
				grpcCode = s.Code()
			} else {
				grpcCode = codes.Unknown
			}
		}

		logger.Info("gRPC request completed",
			"method", info.FullMethod,
			"code", grpcCode.String(),
			"latency_ms", latency.Milliseconds(),
			"peer", clientPeer,
			"module", "service-hub",
		)
		return resp, err
	}
}

// UnaryRecoveryInterceptor catches panics in unary RPCs and returns an Internal gRPC status error.
// UnaryRecoveryInterceptor 拦截一元 RPC Handler 的 Panic 并安全转换为 Internal gRPC 错误。
func UnaryRecoveryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("gRPC handler panic recovered",
					"method", info.FullMethod,
					"panic", fmt.Sprintf("%v", r),
					"module", "service-hub",
				)
				err = status.Errorf(codes.Internal, "internal server error: %v", r)
			}
		}()
		return handler(ctx, req)
	}
}

// StreamLoggingInterceptor logs streaming RPC method completions.
// StreamLoggingInterceptor 记录流式 RPC 调用的执行耗时与完成状态。
func StreamLoggingInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		latency := time.Since(start)
		logger.Info("gRPC stream completed",
			"method", info.FullMethod,
			"latency_ms", latency.Milliseconds(),
			"error", err,
			"module", "service-hub",
		)
		return err
	}
}

// StreamRecoveryInterceptor catches panics in stream RPCs and logs the incident.
// StreamRecoveryInterceptor 拦截流式 RPC 中的 Panic 异常。
func StreamRecoveryInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("gRPC stream handler panic recovered",
					"method", info.FullMethod,
					"panic", fmt.Sprintf("%v", r),
					"module", "service-hub",
				)
				err = status.Errorf(codes.Internal, "internal server error: %v", r)
			}
		}()
		return handler(srv, ss)
	}
}

// BuildServerOptions creates server options configuring interceptor chains and transport credentials.
// BuildServerOptions 组装 gRPC 服务端选项链（Recovery -> Auth -> Logging 拦截器与可选 TLS 凭证）。
func BuildServerOptions(logger *slog.Logger, creds credentials.TransportCredentials) []grpc.ServerOption {
	unaryChain := grpc.ChainUnaryInterceptor(
		UnaryRecoveryInterceptor(logger),
		AuthUnaryInterceptor(),
		UnaryLoggingInterceptor(logger),
	)
	streamChain := grpc.ChainStreamInterceptor(
		AuthStreamInterceptor(),
		StreamRecoveryInterceptor(logger),
		StreamLoggingInterceptor(logger),
	)

	opts := []grpc.ServerOption{unaryChain, streamChain}
	if creds != nil {
		opts = append(opts, grpc.Creds(creds))
	}
	return opts
}

// StartGRPCServer initializes and registers the ServiceHubService gRPC server instance.
// StartGRPCServer 快速装配并返回配置了 TLS 与拦截器的 grpc.Server 与 GRPCServer 实例。
func StartGRPCServer(ag *agent.Client, ds *datasource.Client, cfg *config.Config, tasks store.TaskStore, logger *slog.Logger) (*grpc.Server, *GRPCServer, error) {
	var creds credentials.TransportCredentials
	if cfg.TLSEnabled {
		var err error
		creds, err = BuildServerCredentials(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("build TLS credentials: %w", err)
		}
	}

	opts := BuildServerOptions(logger, creds)
	grpcServer := grpc.NewServer(opts...)
	serviceImpl := New(ag, ds, cfg, tasks, logger)
	pb.RegisterServiceHubServiceServer(grpcServer, serviceImpl)

	return grpcServer, serviceImpl, nil
}

// AllowedOperations returns a sorted list of supported hub operations.
// AllowedOperations 返回排序后的有效操作枚举列表。
func AllowedOperations() []string {
	ops := make([]string, len(validation.HubOperations))
	copy(ops, validation.HubOperations)
	sort.Strings(ops)
	return ops
}
