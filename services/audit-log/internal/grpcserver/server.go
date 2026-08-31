// Package grpcserver implements the gRPC service for the audit-log module with mTLS support.
// Package grpcserver 实现审计日志与不可篡改存证模块的 gRPC 服务端，支持 mTLS 双向认证与公钥固定。
package grpcserver

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/fengzhizi319/PrivShield/pkg/crypto"
	"github.com/fengzhizi319/PrivShield/pkg/naming"
	"github.com/fengzhizi319/PrivShield/pkg/store"
	"github.com/fengzhizi319/PrivShield/pkg/validation"
	"github.com/fengzhizi319/PrivShield/services/audit-log/internal/agent"
	"github.com/fengzhizi319/PrivShield/services/audit-log/internal/config"
	pb "github.com/fengzhizi319/PrivShield/services/audit-log/proto"
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

	inputHash := req.InputHash
	outputHash := req.OutputHash
	if inputHash == "" {
		h := crypto.SumSM3([]byte(fmt.Sprintf("input|%s|%d|%s|%s", normID, req.InputRows, user, req.ParametersJson)))
		inputHash = hex.EncodeToString(h[:])
	}
	if outputHash == "" {
		h := crypto.SumSM3([]byte(fmt.Sprintf("output|%s|%d|%s|%s|%s", normID, req.OutputRows, opStatus, secLevel, req.ParametersJson)))
		outputHash = hex.EncodeToString(h[:])
	}

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
		PrevHash:       req.PrevHash,
	}

	// Envelope encrypt sample fields if key is configured
	encInput := req.InputSample
	encOutput := req.OutputSample
	if s.cfg.EncryptionKey != "" {
		if enc, err := crypto.EncryptString(req.InputSample, s.cfg.EncryptionKey); err == nil {
			encInput = enc
		}
		if enc, err := crypto.EncryptString(req.OutputSample, s.cfg.EncryptionKey); err == nil {
			encOutput = enc
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
		PrevHash:       req.PrevHash,
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
			if dec, err := crypto.DecryptString(inSample, s.cfg.EncryptionKey); err == nil {
				inSample = dec
			}
			if dec, err := crypto.DecryptString(outSample, s.cfg.EncryptionKey); err == nil {
				outSample = dec
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
		prevHash = log.PrevHash
	}

	valid, _ := store.VerifyAuditIntegrityHash(
		snap.IntegrityHash, snap.AuditLogID, prevHash, snap.Timestamp, snap.Algorithm,
		log.InputHash, log.OutputHash, log.User, log.SecurityLevel, snap.ParametersJSON,
	)
	computed := store.ComputeAuditIntegrityHash(
		snap.AuditLogID, prevHash, snap.Timestamp, snap.Algorithm,
		log.InputHash, log.OutputHash, log.User, log.SecurityLevel, snap.ParametersJSON,
	)

	expected := req.ExpectedHash
	if expected == "" {
		expected = snap.IntegrityHash
	}
	if req.ExpectedHash != "" {
		valid = computed == req.ExpectedHash
	}

	msg := "integrity verified: SM3 hash matches non-repudiation proof"
	if !valid {
		msg = "integrity violation: hash mismatch, potential data tampering detected"
	}

	return &pb.VerifyIntegrityResponse{
		SnapshotId:   snap.ID,
		Valid:        valid,
		ComputedHash: computed,
		ExpectedHash: expected,
		Message:      msg,
		Via:          moduleVia,
	}, nil
}

// VerifyChain verifies the unbroken cryptographic hash chain of recent records.
func (s *GRPCServer) VerifyChain(ctx context.Context, req *pb.VerifyChainRequest) (*pb.VerifyChainResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 1000
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
	if !cfg.TLSEnabled {
		return nil, fmt.Errorf("TLS is disabled in configuration")
	}
	if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
		return nil, fmt.Errorf("TLS cert file and key file must be configured")
	}

	cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load server x509 key pair: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	clientAuthMode := strings.ToLower(strings.TrimSpace(cfg.TLSClientAuth))
	if clientAuthMode != "" {
		if cfg.TLSCAFile == "" {
			return nil, fmt.Errorf("TLS CA file must be configured when client auth is enabled")
		}
		caPEM, err := os.ReadFile(cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read TLS CA file: %w", err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", cfg.TLSCAFile)
		}
		tlsConfig.ClientCAs = caPool

		switch clientAuthMode {
		case "require", "requireandverify":
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		case "verify":
			tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
		case "request":
			tlsConfig.ClientAuth = tls.RequestClientCert
		default:
			return nil, fmt.Errorf("unknown TLS client auth mode: %s", cfg.TLSClientAuth)
		}
	}

	if cfg.TLSPinnedPubKeyFile != "" {
		pinnedKey, err := loadPublicKey(cfg.TLSPinnedPubKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load pinned client public key: %w", err)
		}
		tlsConfig.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("mTLS: client did not present a certificate")
			}
			peerCert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("mTLS: failed to parse peer certificate: %w", err)
			}
			if !publicKeysEqual(peerCert.PublicKey, pinnedKey) {
				return fmt.Errorf("mTLS: client public key does not match pinned key")
			}
			return nil
		}
	}

	return credentials.NewTLS(tlsConfig), nil
}

func loadPublicKey(path string) (any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM data found in %s", path)
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		cert, certErr := x509.ParseCertificate(block.Bytes)
		if certErr == nil {
			return cert.PublicKey, nil
		}
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	return pub, nil
}

func publicKeysEqual(a, b any) bool {
	rsaA, okA := a.(*rsa.PublicKey)
	rsaB, okB := b.(*rsa.PublicKey)
	if okA && okB {
		return rsaA.N.Cmp(rsaB.N) == 0 && rsaA.E == rsaB.E
	}
	return false
}

// BuildServerTLSConfig constructs a *tls.Config for the HTTP REST server.
// BuildServerTLSConfig 为 HTTP REST 服务器构建 TLS 配置，与 gRPC 共享同一套证书。
func BuildServerTLSConfig(cfg *config.Config) (*tls.Config, error) {
	if !cfg.TLSEnabled {
		return nil, fmt.Errorf("TLS is disabled in configuration")
	}
	if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
		return nil, fmt.Errorf("TLS cert file and key file must be configured")
	}

	cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load server x509 key pair: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	clientAuthMode := strings.ToLower(strings.TrimSpace(cfg.TLSClientAuth))
	if clientAuthMode != "" {
		if cfg.TLSCAFile == "" {
			return nil, fmt.Errorf("TLS CA file must be configured when client auth is enabled")
		}
		caPEM, err := os.ReadFile(cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read TLS CA file: %w", err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("failed to parse TLS CA certificate from %s", cfg.TLSCAFile)
		}
		tlsConfig.ClientCAs = caPool

		switch clientAuthMode {
		case "require", "requireandverify":
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		case "verify":
			tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
		case "request":
			tlsConfig.ClientAuth = tls.RequestClientCert
		default:
			return nil, fmt.Errorf("unknown TLS client auth mode: %s", cfg.TLSClientAuth)
		}
	}

	return tlsConfig, nil
}
