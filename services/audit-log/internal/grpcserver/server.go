// Package grpcserver implements the gRPC service for the audit-log module with mTLS support.
// Package grpcserver 实现审计日志与不可篡改存证模块的 gRPC 服务端，支持 mTLS 双向认证与公钥固定。
package grpcserver

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/fengzhizi319/PrivShield-go/pkg/crypto"
	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"
	"github.com/fengzhizi319/PrivShield-go/pkg/validation"
	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/config"
	pb "github.com/fengzhizi319/PrivShield-go/services/audit-log/proto"
)

const moduleVia = "audit-log"

// GRPCServer implements pb.AuditLogServiceServer.
type GRPCServer struct {
	pb.UnimplementedAuditLogServiceServer

	agent     *agent.Client
	cfg       *config.Config
	audit     store.AuditStore
	logger    *slog.Logger
	startTime time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a new GRPCServer instance.
func New(ag *agent.Client, cfg *config.Config, audit store.AuditStore, logger *slog.Logger) *GRPCServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &GRPCServer{
		agent:     ag,
		cfg:       cfg,
		audit:     audit,
		logger:    logger,
		startTime: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Shutdown gracefully stops background tasks.
func (s *GRPCServer) Shutdown() {
	s.cancel()
	s.wg.Wait()
}

// Health checks self and upstream agent connectivity.
func (s *GRPCServer) Health(ctx context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
	start := time.Now()
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	agentData, err := s.agent.Health(timeoutCtx)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		s.logger.Warn("gRPC Health: agent unreachable", "error", err.Error())
		return &pb.HealthResponse{
			Backend:   "ok",
			Agent:     "unreachable",
			AgentUrl:  s.cfg.AgentBaseURL(),
			LatencyMs: latency,
			Error:     err.Error(),
			Via:       moduleVia,
		}, nil
	}

	agentStatus := "ok"
	if st, ok := agentData["status"].(string); ok {
		agentStatus = st
	}

	return &pb.HealthResponse{
		Backend:   "ok",
		Agent:     agentStatus,
		AgentUrl:  s.cfg.AgentBaseURL(),
		LatencyMs: latency,
		Via:       moduleVia,
	}, nil
}

// RecordAudit writes a new audit log entry.
func (s *GRPCServer) RecordAudit(ctx context.Context, req *pb.RecordAuditRequest) (*pb.RecordAuditResponse, error) {
	if strings.TrimSpace(req.Operation) == "" {
		return nil, status.Error(codes.InvalidArgument, "operation is required")
	}

	// The hash chain tail is server-assigned by the audit store. Accepting a caller-supplied
	// prev_hash would let any client fork or permanently break the tamper-evidence chain.
	if req.PrevHash != "" {
		return nil, status.Error(codes.InvalidArgument, "prev_hash is assigned by the audit store and must be omitted")
	}

	rawDS := req.DatasourceId
	if rawDS == "" {
		rawDS = req.ApiCode
	}
	if rawDS == "" {
		rawDS = req.Datasource
	}

	if strings.TrimSpace(rawDS) == "" {
		return nil, status.Errorf(codes.InvalidArgument, "datasource or api_code is required")
	}

	entry, err := naming.Normalize(rawDS)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid datasource %q: %v", rawDS, err)
	}
	if err := naming.CheckWritable(entry.DataSourceID); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	normID := entry.DataSourceID
	normAPICode := req.ApiCode
	if normAPICode == "" {
		normAPICode = entry.APICode
	}

	now := time.Now()
	id := fmt.Sprintf("audit_%d", now.UnixNano())

	secLevel := req.SecurityLevel
	if secLevel == "" {
		secLevel = "L3"
	}
	opStatus := req.Status
	if opStatus == "" {
		opStatus = "success"
	}
	user := req.User
	if user == "" {
		user = "system"
	}

	var params any
	if req.ParametersJson != "" {
		_ = json.Unmarshal([]byte(req.ParametersJson), &params)
	}

	// P0 fix: input_hash and output_hash must be supplied by the caller. Server-side fallback
	// to metadata-only hashes weakens the tamper-evidence binding because it does not cover
	// the actual data content.
	if req.InputHash == "" {
		return nil, status.Error(codes.InvalidArgument, "input_hash is required and must be a cryptographic hash of the actual input data")
	}
	if req.OutputHash == "" {
		return nil, status.Error(codes.InvalidArgument, "output_hash is required and must be a cryptographic hash of the actual output data")
	}
	inputHash := req.InputHash
	outputHash := req.OutputHash

	logEntry := &store.AuditLog{
		ID:             id,
		TaskID:         req.TaskId,
		APICode:        normAPICode,
		DatasourceID:   normID,
		Timestamp:      now,
		Operation:      req.Operation,
		DataSource:     normID,
		InputHash:      inputHash,
		OutputHash:     outputHash,
		Algorithm:      req.Algorithm,
		Parameters:     params,
		ParametersJSON: req.ParametersJson,
		InputRows:      int(req.InputRows),
		OutputRows:     int(req.OutputRows),
		DurationMs:     req.DurationMs,
		User:           user,
		Status:         opStatus,
		ErrorMessage:   req.ErrorMessage,
		SecurityLevel:  secLevel,
	}

	// Envelope encrypt sample fields if key is configured
	encInput, encOutput := req.InputSample, req.OutputSample
	if s.cfg.EncryptionKey != "" {
		var err error
		if encInput, err = crypto.EncryptString(req.InputSample, s.cfg.EncryptionKey); err != nil {
			s.logger.Error("failed to encrypt input snapshot sample", "error", err.Error())
			return nil, status.Errorf(codes.Internal, "failed to encrypt input snapshot sample: %v", err)
		}
		if encOutput, err = crypto.EncryptString(req.OutputSample, s.cfg.EncryptionKey); err != nil {
			s.logger.Error("failed to encrypt output snapshot sample", "error", err.Error())
			return nil, status.Errorf(codes.Internal, "failed to encrypt output snapshot sample: %v", err)
		}
	}

	snapID := validation.GenerateID("snap")
	snapshot := &store.SnapshotRecord{
		ID:             snapID,
		AuditLogID:     id,
		Timestamp:      now,
		InputSample:    encInput,
		OutputSample:   encOutput,
		Algorithm:      req.Algorithm,
		Parameters:     params,
		ParametersJSON: req.ParametersJson,
	}

	// Single Authority: store assigns and syncs PrevHash and IntegrityHash to logEntry and snapshot
	if err := s.audit.SaveLogWithSnapshot(logEntry, snapshot); err != nil {
		return nil, status.Errorf(codes.Internal, "save audit log and snapshot: %v", err)
	}

	s.logger.Info("gRPC recorded audit log with snapshot", "id", id, "op", req.Operation, "status", opStatus, "snap_id", snapID)

	return &pb.RecordAuditResponse{
		Id:            id,
		Success:       true,
		Via:           moduleVia,
		SnapshotId:    snapID,
		IntegrityHash: logEntry.IntegrityHash,
	}, nil
}

// GetAuditLog returns a single audit log by ID.
func (s *GRPCServer) GetAuditLog(ctx context.Context, req *pb.GetAuditLogRequest) (*pb.AuditLogProto, error) {
	if strings.TrimSpace(req.Id) == "" {
		return nil, status.Error(codes.InvalidArgument, "audit log id is required")
	}

	rec, err := s.audit.GetLog(req.Id)
	if err != nil || rec == nil {
		return nil, status.Errorf(codes.NotFound, "audit log not found: %s", req.Id)
	}

	return recordToProto(rec), nil
}

// ListAuditLogs returns filtered audit logs.
func (s *GRPCServer) ListAuditLogs(ctx context.Context, req *pb.ListAuditLogsRequest) (*pb.ListAuditLogsResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := int(req.Offset)
	if offset < 0 {
		offset = 0
	}

	rawDS := req.DatasourceId
	if rawDS == "" {
		rawDS = req.Datasource
	}
	normDS := ""
	if rawDS != "" {
		if id, err := naming.NormalizeDataSourceID(rawDS); err == nil {
			normDS = id
		} else {
			normDS = rawDS
		}
	}

	filter := store.AuditFilter{
		TaskID:        req.TaskId,
		APICode:       req.ApiCode,
		DatasourceID:  normDS,
		Operation:     req.Operation,
		DataSource:    normDS,
		User:          req.User,
		Status:        req.Status,
		SecurityLevel: req.SecurityLevel,
		Limit:         limit,
		Offset:        offset,
	}

	logs, total, err := s.audit.ListLogs(filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list audit logs: %v", err)
	}

	protos := make([]*pb.AuditLogProto, 0, len(logs))
	for i := range logs {
		protos = append(protos, recordToProto(&logs[i]))
	}

	return &pb.ListAuditLogsResponse{
		Total:  int32(total),
		Limit:  int32(limit),
		Offset: int32(offset),
		Logs:   protos,
		Via:    moduleVia,
	}, nil
}

// GetAuditStats calculates aggregated audit metrics.
func (s *GRPCServer) GetAuditStats(ctx context.Context, req *pb.GetAuditStatsRequest) (*pb.AuditStatsResponse, error) {
	period := req.Period
	if period == "" {
		period = "24h"
	}

	filter := store.AuditFilter{Limit: 10000}
	logs, _, err := s.audit.ListLogs(filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get stats: %v", err)
	}

	byOp := make(map[string]int32)
	byStatus := make(map[string]int32)
	byLevel := make(map[string]int32)
	var totalDuration int64

	for _, l := range logs {
		byOp[l.Operation]++
		byStatus[l.Status]++
		if l.SecurityLevel != "" {
			byLevel[l.SecurityLevel]++
		}
		totalDuration += l.DurationMs
	}

	var avgDuration float64
	if len(logs) > 0 {
		avgDuration = float64(totalDuration) / float64(len(logs))
	}

	return &pb.AuditStatsResponse{
		TotalOperations: int32(len(logs)),
		ByOperation:     byOp,
		ByStatus:        byStatus,
		BySecurityLevel: byLevel,
		AvgDurationMs:   avgDuration,
		Period:          period,
		Via:             moduleVia,
	}, nil
}

// ListSnapshots returns desensitization snapshots (decrypting samples if key configured).
func (s *GRPCServer) ListSnapshots(ctx context.Context, req *pb.ListSnapshotsRequest) (*pb.ListSnapshotsResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	offset := int(req.Offset)
	if offset < 0 {
		offset = 0
	}

	snapshots, total, err := s.audit.ListSnapshots(limit, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list snapshots: %v", err)
	}

	protos := make([]*pb.SnapshotProto, 0, len(snapshots))
	for _, snap := range snapshots {
		inSample := snap.InputSample
		outSample := snap.OutputSample
		if s.cfg.EncryptionKey != "" {
			if crypto.IsEncrypted(inSample) {
				if dec, err := crypto.DecryptString(inSample, s.cfg.EncryptionKey); err == nil {
					inSample = dec
				} else {
					s.logger.Warn("snapshot sample decryption failed",
						"snapshot_id", snap.ID, "field", "input_sample", "error", err.Error())
				}
			} else if inSample != "" {
				s.logger.Warn("snapshot sample stored without envelope prefix while encryption is enabled",
					"snapshot_id", snap.ID, "field", "input_sample")
			}
			if crypto.IsEncrypted(outSample) {
				if dec, err := crypto.DecryptString(outSample, s.cfg.EncryptionKey); err == nil {
					outSample = dec
				} else {
					s.logger.Warn("snapshot sample decryption failed",
						"snapshot_id", snap.ID, "field", "output_sample", "error", err.Error())
				}
			} else if outSample != "" {
				s.logger.Warn("snapshot sample stored without envelope prefix while encryption is enabled",
					"snapshot_id", snap.ID, "field", "output_sample")
			}
		}

		paramsJSON, _ := json.Marshal(snap.Parameters)
		protos = append(protos, &pb.SnapshotProto{
			Id:             snap.ID,
			AuditLogId:     snap.AuditLogID,
			Timestamp:      snap.Timestamp.Format(time.RFC3339),
			InputSample:    inSample,
			OutputSample:   outSample,
			Algorithm:      snap.Algorithm,
			ParametersJson: string(paramsJSON),
			IntegrityHash:  snap.IntegrityHash,
			PrevHash:       snap.PrevHash,
		})
	}

	return &pb.ListSnapshotsResponse{
		Total:     int32(total),
		Limit:     int32(limit),
		Offset:    int32(offset),
		Snapshots: protos,
		Via:       moduleVia,
	}, nil
}

// VerifyIntegrity verifies SHA-256 hash of a snapshot.
func (s *GRPCServer) VerifyIntegrity(ctx context.Context, req *pb.VerifyIntegrityRequest) (*pb.VerifyIntegrityResponse, error) {
	if strings.TrimSpace(req.SnapshotId) == "" {
		return nil, status.Error(codes.InvalidArgument, "snapshot_id is required")
	}

	snap, err := s.audit.GetSnapshot(req.SnapshotId)
	if err != nil || snap == nil {
		return nil, status.Errorf(codes.NotFound, "snapshot not found: %s", req.SnapshotId)
	}

	log, err := s.audit.GetLog(snap.AuditLogID)
	if err != nil || log == nil {
		return nil, status.Errorf(codes.NotFound, "associated audit log not found: %s", snap.AuditLogID)
	}

	prevHash := snap.PrevHash
	if prevHash == "" {
		// Backward compatibility: snapshots written before the P0 fix may have an empty prev_hash.
		prevHash = log.PrevHash
	}

	// P0 fix: verify the snapshot using its own integrity hash that covers input/output samples.
	var valid bool
	var hashLabel string
	valid, hashLabel = store.VerifySnapshotIntegrityHash(
		snap.IntegrityHash, snap.ID, snap.AuditLogID, prevHash, snap.Timestamp, snap.Algorithm,
		snap.InputSample, snap.OutputSample, snap.ParametersJSON,
	)
	// Legacy fallback: snapshots written before this fix copied the parent audit log's integrity hash.
	if !valid {
		valid, hashLabel = store.VerifyAuditIntegrityHash(
			snap.IntegrityHash, snap.AuditLogID, prevHash, snap.Timestamp, snap.Algorithm,
			log.InputHash, log.OutputHash, log.User, log.SecurityLevel, snap.ParametersJSON,
		)
	}
	computed := store.ComputeSnapshotIntegrityHash(
		snap.ID, snap.AuditLogID, prevHash, snap.Timestamp, snap.Algorithm,
		snap.InputSample, snap.OutputSample, snap.ParametersJSON,
	)

	expected := req.ExpectedHash
	if expected == "" {
		expected = snap.IntegrityHash
	}
	if req.ExpectedHash != "" {
		valid = computed == req.ExpectedHash
		// When the caller supplies an explicit expected hash, the matched algorithm label is unknown.
	}

	reason := store.ChainReasonOK
	msg := "integrity verified: SM3 hash matches non-repudiation proof"

	// G-10: verify the SM2 digital signature over the snapshot integrity hash.
	if valid && snap.SM2Signature != "" {
		sigOk, _ := store.VerifyAuditSignature(snap.IntegrityHash, snap.SM2Signature)
		if !sigOk {
			valid = false
			reason = store.ChainReasonInvalidSM2Signature
			msg = "integrity violation: SM2 signature invalid, non-repudiation proof forged or key mismatch"
		}
	}

	if !valid && reason == store.ChainReasonOK {
		msg = "integrity violation: hash mismatch, potential data tampering detected"
		reason = store.ChainReasonHashMismatch
	}

	hashLabelStr := hashLabel
	if hashLabelStr == "" {
		hashLabelStr = snap.Algorithm
	}

	return &pb.VerifyIntegrityResponse{
		SnapshotId:   snap.ID,
		Valid:        valid,
		ComputedHash: computed,
		ExpectedHash: expected,
		Message:      msg,
		Via:          moduleVia,
		Reason:       reason,
		HashLabel:    hashLabelStr,
	}, nil
}

// VerifyChain verifies the unbroken cryptographic hash chain of records.
// P1 fix: when limit is omitted or zero, the entire chain is verified by default.
func (s *GRPCServer) VerifyChain(ctx context.Context, req *pb.VerifyChainRequest) (*pb.VerifyChainResponse, error) {
	limit := int(req.Limit)
	if limit < 0 {
		limit = 0
	}

	res, err := s.audit.VerifyChain(limit)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "verify chain: %v", err)
	}

	return &pb.VerifyChainResponse{
		TotalVerified: int32(res.TotalVerified),
		Valid:         res.Valid,
		BrokenAtId:    res.BrokenAtID,
		ExpectedHash:  res.ExpectedHash,
		ActualHash:    res.ActualHash,
		Message:       res.Message,
		Via:           moduleVia,
		Reason:        res.Reason,
		LegacyHashed:  int32(res.LegacyHashed),
		TotalRecords:  int32(res.TotalRecords),
	}, nil
}

// GenerateReport produces a compliance audit report.
func (s *GRPCServer) GenerateReport(ctx context.Context, req *pb.GenerateReportRequest) (*pb.ComplianceReportResponse, error) {
	period := req.Period
	if period == "" {
		period = "30d"
	}

	filter := store.AuditFilter{Limit: 10000}
	logs, total, err := s.audit.ListLogs(filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "generate report: %v", err)
	}

	successCount := 0
	byLevel := make(map[string]int32)
	opCounts := make(map[string]int)

	for _, l := range logs {
		if l.Status == "success" {
			successCount++
		}
		if l.SecurityLevel != "" {
			byLevel[l.SecurityLevel]++
		}
		opCounts[l.Operation]++
	}

	var successRate float64
	if total > 0 {
		successRate = float64(successCount) / float64(total) * 100
	}

	topOps := make([]string, 0, len(opCounts))
	for op := range opCounts {
		topOps = append(topOps, op)
	}

	recommendations := []string{
		"对 L4/L5 敏感数据强化自动化差异隐私 (DP) 与 K-匿名防护",
		"定期进行 SHA-256 审计存证一致性巡检与防篡改对账",
		"建立针对高频查询数据源的差分隐私预算动态分配策略",
	}

	now := time.Now()
	reportID := fmt.Sprintf("report_%d", now.UnixNano())

	return &pb.ComplianceReportResponse{
		Id:              reportID,
		GeneratedAt:     now.Format(time.RFC3339),
		Period:          period,
		TotalOperations: int32(total),
		SuccessRate:     successRate,
		BySecurityLevel: byLevel,
		TopOperations:   topOps,
		Recommendations: recommendations,
		Via:             moduleVia,
	}, nil
}

func recordToProto(rec *store.AuditLog) *pb.AuditLogProto {
	paramsJSON, _ := json.Marshal(rec.Parameters)
	return &pb.AuditLogProto{
		Id:             rec.ID,
		Timestamp:      rec.Timestamp.Format(time.RFC3339),
		Operation:      rec.Operation,
		Datasource:     rec.DataSource,
		InputHash:      rec.InputHash,
		OutputHash:     rec.OutputHash,
		Algorithm:      rec.Algorithm,
		ParametersJson: string(paramsJSON),
		InputRows:      int32(rec.InputRows),
		OutputRows:     int32(rec.OutputRows),
		DurationMs:     rec.DurationMs,
		User:           rec.User,
		Status:         rec.Status,
		ErrorMessage:   rec.ErrorMessage,
		SecurityLevel:  rec.SecurityLevel,
		TaskId:         rec.TaskID,
		ApiCode:        rec.APICode,
		DatasourceId:   rec.DatasourceID,
		PrevHash:       rec.PrevHash,
		IntegrityHash:  rec.IntegrityHash,
	}
}

// ─────────────────────────────────────────────────────────────
// mTLS Credentials Builder / mTLS 凭证构造与公钥固定
// ─────────────────────────────────────────────────────────────

// BuildServerCredentials constructs gRPC transport credentials supporting mTLS and public key pinning.
func BuildServerCredentials(cfg *config.Config) (credentials.TransportCredentials, error) {
	tlsConfig, err := BuildServerTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(tlsConfig), nil
}

// BuildServerTLSConfig constructs a *tls.Config for the HTTP REST server.
// BuildServerTLSConfig 为 HTTP REST 服务器构建 TLS 配置，与 gRPC 共享同一套证书。
func BuildServerTLSConfig(cfg *config.Config) (*tls.Config, error) {
	return tlsutil.BuildServerTLSConfig(&tlsutil.ServerTLSConfig{
		Enabled:          cfg.TLSEnabled,
		CertFile:         cfg.TLSCertFile,
		KeyFile:          cfg.TLSKeyFile,
		CAFile:           cfg.TLSCAFile,
		ClientAuth:       cfg.TLSClientAuth,
		PinnedPubKeyFile: cfg.TLSPinnedPubKeyFile,
	})
}
