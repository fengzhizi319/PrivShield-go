package grpcserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/memory"
	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/config"
	pb "github.com/fengzhizi319/PrivShield-go/services/audit-log/proto"
)

func setupTestGRPCServer(t *testing.T, agentMux http.Handler) (pb.AuditLogServiceClient, store.AuditStore, func()) {
	t.Helper()

	var agentURL string
	if agentMux != nil {
		agentSrv := httptest.NewServer(agentMux)
		agentURL = agentSrv.URL
	} else {
		agentURL = "http://127.0.0.1:59999"
	}

	t.Setenv("PRIVACY_AGENT_URLS", agentURL)
	cfg := config.Load()
	logger := pkgobs.NewLogger("text", "debug")
	auditStore := memory.NewAuditStore()
	agentClient := agent.New(cfg)

	srvImpl := New(agentClient, cfg, auditStore, logger)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterAuditLogServiceServer(s, srvImpl)

	go func() {
		_ = s.Serve(lis)
	}()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := pb.NewAuditLogServiceClient(conn)

	cleanup := func() {
		_ = conn.Close()
		s.Stop()
		srvImpl.Shutdown()
		_ = lis.Close()
	}

	return client, auditStore, cleanup
}

func TestGRPCHealth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})

	client, _, cleanup := setupTestGRPCServer(t, mux)
	defer cleanup()

	resp, err := client.Health(context.Background(), &pb.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	if resp.Backend != "ok" || resp.Agent != "ok" {
		t.Errorf("unexpected health response: %+v", resp)
	}
}

func TestGRPCHealthAgentUnreachable(t *testing.T) {
	client, _, cleanup := setupTestGRPCServer(t, nil)
	defer cleanup()

	resp, err := client.Health(context.Background(), &pb.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	if resp.Backend != "ok" || resp.Agent != "unreachable" {
		t.Errorf("unexpected health response when agent unreachable: %+v", resp)
	}
}

func TestGRPCAuditLogOperations(t *testing.T) {
	client, _, cleanup := setupTestGRPCServer(t, nil)
	defer cleanup()

	ctx := context.Background()

	// 1. RecordAudit
	recResp, err := client.RecordAudit(ctx, &pb.RecordAuditRequest{
		Operation:      "mask",
		Datasource:     "ds_yibao",
		InputHash:      "hash_in_123",
		OutputHash:     "hash_out_456",
		Algorithm:      "field_mask",
		ParametersJson: `{"pattern":"id_card"}`,
		InputRows:      100,
		OutputRows:     100,
		DurationMs:     15,
		User:           "sec_officer",
		Status:         "success",
		SecurityLevel:  "L4",
	})
	if err != nil {
		t.Fatalf("RecordAudit failed: %v", err)
	}
	if !recResp.Success || recResp.Id == "" {
		t.Errorf("unexpected record audit response: %+v", recResp)
	}

	logID := recResp.Id

	// 2. GetAuditLog
	getResp, err := client.GetAuditLog(ctx, &pb.GetAuditLogRequest{Id: logID})
	if err != nil {
		t.Fatalf("GetAuditLog failed: %v", err)
	}
	if getResp.Id != logID || getResp.Operation != "mask" || getResp.SecurityLevel != "L4" {
		t.Errorf("unexpected get audit log: %+v", getResp)
	}

	// 3. ListAuditLogs
	listResp, err := client.ListAuditLogs(ctx, &pb.ListAuditLogsRequest{
		Operation: "mask",
		Limit:     10,
		Offset:    0,
	})
	if err != nil {
		t.Fatalf("ListAuditLogs failed: %v", err)
	}
	if listResp.Total != 1 || len(listResp.Logs) != 1 {
		t.Errorf("unexpected list audit logs: %+v", listResp)
	}

	// 4. GetAuditStats
	statsResp, err := client.GetAuditStats(ctx, &pb.GetAuditStatsRequest{Period: "24h"})
	if err != nil {
		t.Fatalf("GetAuditStats failed: %v", err)
	}
	if statsResp.TotalOperations != 1 || statsResp.ByOperation["mask"] != 1 {
		t.Errorf("unexpected audit stats: %+v", statsResp)
	}

	// 5. GenerateReport
	repResp, err := client.GenerateReport(ctx, &pb.GenerateReportRequest{Period: "30d"})
	if err != nil {
		t.Fatalf("GenerateReport failed: %v", err)
	}
	if repResp.TotalOperations != 1 || repResp.SuccessRate != 100.0 || len(repResp.Recommendations) == 0 {
		t.Errorf("unexpected compliance report: %+v", repResp)
	}

	// 6. ListSnapshots (auto-created by RecordAudit)
	snapListResp, err := client.ListSnapshots(ctx, &pb.ListSnapshotsRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListSnapshots failed: %v", err)
	}
	if snapListResp.Total < 1 || len(snapListResp.Snapshots) < 1 {
		t.Errorf("unexpected snapshots list: %+v", snapListResp)
	}

	// 7. VerifyIntegrity on the auto-created snapshot
	snapID := recResp.SnapshotId
	if snapID == "" && len(snapListResp.Snapshots) > 0 {
		snapID = snapListResp.Snapshots[0].Id
	}
	verifyResp, err := client.VerifyIntegrity(ctx, &pb.VerifyIntegrityRequest{
		SnapshotId: snapID,
	})
	if err != nil {
		t.Fatalf("VerifyIntegrity failed: %v", err)
	}
	if verifyResp.ComputedHash == "" || !verifyResp.Valid {
		t.Errorf("expected valid computed hash in verify response: %+v", verifyResp)
	}

	// 8. VerifyChain
	chainResp, err := client.VerifyChain(ctx, &pb.VerifyChainRequest{Limit: 10})
	if err != nil {
		t.Fatalf("VerifyChain failed: %v", err)
	}
	if !chainResp.Valid || chainResp.TotalVerified < 1 {
		t.Errorf("expected valid hash chain, got: %+v", chainResp)
	}
}

func TestGRPCValidationErrors(t *testing.T) {
	client, _, cleanup := setupTestGRPCServer(t, nil)
	defer cleanup()

	ctx := context.Background()

	// 1. RecordAudit with empty operation
	_, err := client.RecordAudit(ctx, &pb.RecordAuditRequest{Operation: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for empty operation, got: %v", err)
	}

	// 2. GetAuditLog with empty ID
	_, err = client.GetAuditLog(ctx, &pb.GetAuditLogRequest{Id: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for empty log ID, got: %v", err)
	}

	// 3. GetAuditLog not found
	_, err = client.GetAuditLog(ctx, &pb.GetAuditLogRequest{Id: "non_existent_log"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for non-existent log, got: %v", err)
	}

	// 4. VerifyIntegrity with empty snapshot ID
	_, err = client.VerifyIntegrity(ctx, &pb.VerifyIntegrityRequest{SnapshotId: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for empty snapshot ID, got: %v", err)
	}

	// 5. RecordAudit must reject a caller-chosen chain predecessor
	_, err = client.RecordAudit(ctx, &pb.RecordAuditRequest{
		Operation:  "mask",
		Status:     "success",
		Datasource: "ds_yibao",
		PrevHash:   "cafe0000_client_forged",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for caller-supplied prev_hash, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// mTLS Credentials and Key Pinning Tests
// ─────────────────────────────────────────────────────────────

func generateTestCertAndKey(t *testing.T, tmpDir string) (string, string, string, string) {
	t.Helper()

	// 1. CA
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "PrivShield-Audit-CA",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caBytes, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create CA cert: %v", err)
	}

	caFile := filepath.Join(tmpDir, "ca.pem")
	_ = os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caBytes}), 0600)

	// 2. Server Cert
	srvKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate srv key: %v", err)
	}
	srvTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "127.0.0.1",
		},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	srvBytes, err := x509.CreateCertificate(rand.Reader, srvTemplate, caTemplate, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create server cert: %v", err)
	}

	srvCertFile := filepath.Join(tmpDir, "server.crt")
	srvKeyFile := filepath.Join(tmpDir, "server.key")
	_ = os.WriteFile(srvCertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvBytes}), 0600)
	_ = os.WriteFile(srvKeyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(srvKey)}), 0600)

	// 3. Client PubKey for Pinning
	pubBytes, err := x509.MarshalPKIXPublicKey(&srvKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal pubkey: %v", err)
	}
	pubKeyFile := filepath.Join(tmpDir, "client_pub.pem")
	_ = os.WriteFile(pubKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}), 0600)

	return caFile, srvCertFile, srvKeyFile, pubKeyFile
}

func TestBuildServerCredentials(t *testing.T) {
	tmpDir := t.TempDir()
	caFile, srvCert, srvKey, pubKey := generateTestCertAndKey(t, tmpDir)

	// 1. TLS disabled
	cfg := &config.Config{TLSEnabled: false}
	if _, err := BuildServerCredentials(cfg); err == nil {
		t.Errorf("expected error when TLS is disabled")
	}

	// 2. Missing cert/key
	cfg = &config.Config{TLSEnabled: true}
	if _, err := BuildServerCredentials(cfg); err == nil {
		t.Errorf("expected error when cert/key missing")
	}

	// 3. Valid TLS without client auth
	cfg = &config.Config{
		TLSEnabled:  true,
		TLSCertFile: srvCert,
		TLSKeyFile:  srvKey,
	}
	creds, err := BuildServerCredentials(cfg)
	if err != nil || creds == nil {
		t.Fatalf("failed to build simple TLS credentials: %v", err)
	}

	// 4. Valid mTLS with client cert verification
	cfg = &config.Config{
		TLSEnabled:    true,
		TLSCertFile:   srvCert,
		TLSKeyFile:    srvKey,
		TLSCAFile:     caFile,
		TLSClientAuth: "require",
	}
	creds, err = BuildServerCredentials(cfg)
	if err != nil || creds == nil {
		t.Fatalf("failed to build mTLS credentials: %v", err)
	}

	// 5. Valid mTLS with public key pinning
	cfg.TLSPinnedPubKeyFile = pubKey
	creds, err = BuildServerCredentials(cfg)
	if err != nil || creds == nil {
		t.Fatalf("failed to build mTLS credentials with public key pinning: %v", err)
	}

	// 6. Missing CA when client auth enabled
	cfg = &config.Config{
		TLSEnabled:    true,
		TLSCertFile:   srvCert,
		TLSKeyFile:    srvKey,
		TLSClientAuth: "require",
	}
	if _, err := BuildServerCredentials(cfg); err == nil {
		t.Errorf("expected error when CA file missing with client auth enabled")
	}

	// 7. Invalid client auth mode
	cfg = &config.Config{
		TLSEnabled:    true,
		TLSCertFile:   srvCert,
		TLSKeyFile:    srvKey,
		TLSCAFile:     caFile,
		TLSClientAuth: "invalid_mode",
	}
	if _, err := BuildServerCredentials(cfg); err == nil {
		t.Errorf("expected error for invalid client auth mode")
	}
}
