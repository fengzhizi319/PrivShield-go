package grpcserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/fengzhizi319/PrivShield/pkg/metrics"
	"github.com/fengzhizi319/PrivShield/pkg/store"
	"github.com/fengzhizi319/PrivShield/pkg/store/memory"
	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/agent"
	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/config"
	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/datasource"
	pb "github.com/fengzhizi319/PrivShield/services/service-hub/proto"
)

// testCerts holds paths to test certificate files.
// testCerts 保存测试证书文件路径集合。
type testCerts struct {
	caFile     string // CA 根证书文件路径
	serverCert string // 服务端证书文件路径
	serverKey  string // 服务端私钥文件路径
	clientCert string // 客户端证书文件路径
	clientKey  string // 客户端私钥文件路径
	clientPub  string // 客户端公钥 PEM 文件路径
}

type failingUpdateTaskStore struct {
	*memory.TaskStore
	updateCalls int
}

func (s *failingUpdateTaskStore) Update(task *store.Task) error {
	s.updateCalls++
	return errors.New("simulated task state persistence failure")
}

type leaseWorkerTestStore struct {
	mu        sync.Mutex
	tasks     map[string]*store.Task
	completed chan string
}

func newLeaseWorkerTestStore(task *store.Task) *leaseWorkerTestStore {
	return &leaseWorkerTestStore{
		tasks:     map[string]*store.Task{task.ID: task},
		completed: make(chan string, 1),
	}
}

func (s *leaseWorkerTestStore) Save(task *store.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *task
	s.tasks[task.ID] = &copy
	return nil
}

func (s *leaseWorkerTestStore) Get(id string) (*store.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return nil, errors.New("task not found")
	}
	copy := *task
	return &copy, nil
}

func (s *leaseWorkerTestStore) List(filter store.TaskFilter) ([]store.Task, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tasks []store.Task
	for _, task := range s.tasks {
		if filter.Status == "" || filter.Status == task.Status {
			tasks = append(tasks, *task)
		}
	}
	return tasks, len(tasks), nil
}

func (s *leaseWorkerTestStore) Update(task *store.Task) error { return s.Save(task) }

func (s *leaseWorkerTestStore) Counts() (store.TaskCounts, error) {
	return store.TaskCounts{}, nil
}

func (s *leaseWorkerTestStore) CleanupOld(time.Time) (int64, error) { return 0, nil }

func (s *leaseWorkerTestStore) ClaimNext(owner string, leaseTTL time.Duration) (*store.TaskLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.tasks {
		if task.Status != "pending" {
			continue
		}
		expiresAt := time.Now().Add(leaseTTL)
		task.Status = "running"
		task.LeaseOwner = owner
		task.LeaseToken = "test-token"
		task.LeaseExpiresAt = &expiresAt
		copy := *task
		return &store.TaskLease{Task: &copy, Owner: owner, Token: "test-token", ExpiresAt: expiresAt}, nil
	}
	return nil, nil
}

func (s *leaseWorkerTestStore) RenewLease(_, _, _ string, _ time.Duration) (bool, error) {
	return true, nil
}

func (s *leaseWorkerTestStore) CompleteLease(id, _, _ string, _ store.TaskResult) (bool, error) {
	s.mu.Lock()
	task := s.tasks[id]
	task.Status = "completed"
	s.mu.Unlock()
	s.completed <- id
	return true, nil
}

func (s *leaseWorkerTestStore) FailLease(id, _, _ string, _ store.TaskFailure) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[id].Status = "failed"
	return true, nil
}

func (s *leaseWorkerTestStore) RequeueExpiredLeases(int) (int, error) { return 0, nil }

// genTestCerts generates a complete test certificate chain in a temp directory.
// genTestCerts 在临时目录中动态生成完整的测试证书链（CA 根证书 ➔ 服务端证书 ➔ 客户端证书 ➔ 客户端公钥）。
func genTestCerts(t *testing.T) testCerts {
	t.Helper()
	dir := t.TempDir()

	// 1. 生成 CA 私钥和自签名 CA 证书
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caFile := writePEM(t, dir, "ca.crt", "CERTIFICATE", caDER)
	writePEM(t, dir, "ca.key", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(caKey))

	// 解析 CA 用于签发后续证书
	caCert, _ := x509.ParseCertificate(caDER)

	// 2. 生成由 CA 签发的服务端证书
	serverKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	serverTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	serverDER, _ := x509.CreateCertificate(rand.Reader, serverTmpl, caCert, &serverKey.PublicKey, caKey)
	serverCert := writePEM(t, dir, "server.crt", "CERTIFICATE", serverDER)
	serverKeyFile := writePEM(t, dir, "server.key", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey))

	// 3. 生成由 CA 签发的客户端证书
	clientKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	clientTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, _ := x509.CreateCertificate(rand.Reader, clientTmpl, caCert, &clientKey.PublicKey, caKey)
	clientCert := writePEM(t, dir, "client.crt", "CERTIFICATE", clientDER)
	clientKeyFile := writePEM(t, dir, "client.key", "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(clientKey))

	// 4. 提取客户端公钥并保存为 PEM
	clientPubDER, _ := x509.MarshalPKIXPublicKey(&clientKey.PublicKey)
	clientPub := writePEM(t, dir, "client.pub", "PUBLIC KEY", clientPubDER)

	return testCerts{
		caFile:     caFile,
		serverCert: serverCert,
		serverKey:  serverKeyFile,
		clientCert: clientCert,
		clientKey:  clientKeyFile,
		clientPub:  clientPub,
	}
}

// writePEM writes a DER-encoded block as PEM to a file.
// writePEM 将 DER 编码的字节切片以指定 blockType 写入 PEM 格式文件。
func writePEM(t *testing.T, dir, name, blockType string, der []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		t.Fatalf("encode PEM %s: %v", path, err)
	}
	return path
}

// ─────────────────────────────────────────────────────────────
// Tests / 测试用例
// ─────────────────────────────────────────────────────────────

// TestBuildServerCredentialsTLSDisabled verifies that disabled TLS returns error.
// TestBuildServerCredentialsTLSDisabled 验证当 TLSEnabled=false 时正确返回错误。
func TestBuildServerCredentialsTLSDisabled(t *testing.T) {
	cfg := &config.Config{TLSEnabled: false}
	if _, err := BuildServerCredentials(cfg); err == nil {
		t.Fatal("expected error when TLS is disabled")
	}
}

// TestBuildServerCredentialsMissingCert verifies error when cert is missing.
// TestBuildServerCredentialsMissingCert 验证证书或私钥配置缺失时的错误拦截。
func TestBuildServerCredentialsMissingCert(t *testing.T) {
	cfg := &config.Config{
		TLSEnabled:  true,
		TLSCertFile: "",
		TLSKeyFile:  "/some/key.pem",
	}
	if _, err := BuildServerCredentials(cfg); err == nil {
		t.Fatal("expected error when cert file is missing")
	}
}

// TestBuildServerCredentialsInvalidCertPath verifies error with invalid cert path.
// TestBuildServerCredentialsInvalidCertPath 验证证书路径不存在时的文件读取错误。
func TestBuildServerCredentialsInvalidCertPath(t *testing.T) {
	cfg := &config.Config{
		TLSEnabled:  true,
		TLSCertFile: "/nonexistent/cert.pem",
		TLSKeyFile:  "/nonexistent/key.pem",
	}
	if _, err := BuildServerCredentials(cfg); err == nil {
		t.Fatal("expected error when cert path is invalid")
	}
}

// TestBuildServerCredentialsMTLS verifies successful mTLS credential building.
// TestBuildServerCredentialsMTLS 验证成功构建包含 Client CAs 的双向认证 TLS 凭证。
func TestBuildServerCredentialsMTLS(t *testing.T) {
	certs := genTestCerts(t)
	cfg := &config.Config{
		TLSEnabled:    true,
		TLSCertFile:   certs.serverCert,
		TLSKeyFile:    certs.serverKey,
		TLSCAFile:     certs.caFile,
		TLSClientAuth: "require",
	}

	creds, err := BuildServerCredentials(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
	if creds.Info().SecurityProtocol != "tls" {
		t.Errorf("security protocol = %q, want tls", creds.Info().SecurityProtocol)
	}
}

// TestBuildServerCredentialsMTLSWithPinnedKey verifies mTLS with public key pinning.
// TestBuildServerCredentialsMTLSWithPinnedKey 验证带公钥固定校验器的 mTLS 凭证构建。
func TestBuildServerCredentialsMTLSWithPinnedKey(t *testing.T) {
	certs := genTestCerts(t)
	cfg := &config.Config{
		TLSEnabled:          true,
		TLSCertFile:         certs.serverCert,
		TLSKeyFile:          certs.serverKey,
		TLSCAFile:           certs.caFile,
		TLSClientAuth:       "require",
		TLSPinnedPubKeyFile: certs.clientPub,
	}

	creds, err := BuildServerCredentials(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
}

// TestBuildServerCredentialsMTLSMissingCA verifies error when CA is missing for mTLS.
// TestBuildServerCredentialsMTLSMissingCA 验证启用客户端认证但缺失 CA 文件时的校验阻断。
func TestBuildServerCredentialsMTLSMissingCA(t *testing.T) {
	certs := genTestCerts(t)
	cfg := &config.Config{
		TLSEnabled:    true,
		TLSCertFile:   certs.serverCert,
		TLSKeyFile:    certs.serverKey,
		TLSCAFile:     "", // 缺失 CA 文件
		TLSClientAuth: "require",
	}

	if _, err := BuildServerCredentials(cfg); err == nil {
		t.Fatal("expected error when CA file is missing for mTLS")
	}
}

// TestBuildServerCredentialsInvalidClientAuthMode verifies error with unknown auth mode.
// TestBuildServerCredentialsInvalidClientAuthMode 验证使用非法客户端认证模式字符串时报错。
func TestBuildServerCredentialsInvalidClientAuthMode(t *testing.T) {
	certs := genTestCerts(t)
	cfg := &config.Config{
		TLSEnabled:    true,
		TLSCertFile:   certs.serverCert,
		TLSKeyFile:    certs.serverKey,
		TLSCAFile:     certs.caFile,
		TLSClientAuth: "unknown-mode",
	}

	if _, err := BuildServerCredentials(cfg); err == nil {
		t.Fatal("expected error with unknown client auth mode")
	}
}

// TestLoadPublicKey verifies public key loading from PEM file.
// TestLoadPublicKey 验证从 PEM 公钥文件中正确解析加载 RSA 公钥。
func TestLoadPublicKey(t *testing.T) {
	certs := genTestCerts(t)

	key, err := loadPublicKey(certs.clientPub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key == nil {
		t.Fatal("expected non-nil public key")
	}
}

// TestLoadPublicKeyInvalidPath verifies error with invalid path.
// TestLoadPublicKeyInvalidPath 验证加载不存在的公钥文件时返回错误。
func TestLoadPublicKeyInvalidPath(t *testing.T) {
	if _, err := loadPublicKey("/nonexistent/key.pub"); err == nil {
		t.Fatal("expected error with invalid path")
	}
}

// TestLoadPublicKeyInvalidPEM verifies error with invalid PEM content.
// TestLoadPublicKeyInvalidPEM 验证加载非 PEM 格式内容时返回错误。
func TestLoadPublicKeyInvalidPEM(t *testing.T) {
	dir := t.TempDir()
	badFile := filepath.Join(dir, "bad.pub")
	if err := os.WriteFile(badFile, []byte("not a PEM file"), 0o644); err != nil {
		t.Fatalf("write bad file: %v", err)
	}

	if _, err := loadPublicKey(badFile); err == nil {
		t.Fatal("expected error with invalid PEM content")
	}
}

// TestPublicKeysEqual verifies public key comparison.
// TestPublicKeysEqual 验证同源公钥与不同公钥的比对逻辑。
func TestPublicKeysEqual(t *testing.T) {
	certs := genTestCerts(t)

	key1, _ := loadPublicKey(certs.clientPub)
	key2, _ := loadPublicKey(certs.clientPub)

	if !publicKeysEqual(key1, key2) {
		t.Error("same public keys should be equal")
	}

	// 生成不同的密钥
	dir := t.TempDir()
	diffKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	diffPubDER, _ := x509.MarshalPKIXPublicKey(&diffKey.PublicKey)
	diffPub := writePEM(t, dir, "diff.pub", "PUBLIC KEY", diffPubDER)
	key3, _ := loadPublicKey(diffPub)

	if publicKeysEqual(key1, key3) {
		t.Error("different public keys should not be equal")
	}
}

// TestPublicKeysEqualDifferentTypes verifies comparison of different key types.
// TestPublicKeysEqualDifferentTypes 验证不同类型（RSA vs 非 Key 结构）比对安全返回 false。
func TestPublicKeysEqualDifferentTypes(t *testing.T) {
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	if publicKeysEqual(&rsaKey.PublicKey, "not a key") {
		t.Error("RSA key should not equal string")
	}
}

// ─────────────────────────────────────────────────────────────
// gRPC Server Method Tests / gRPC 服务方法单元测试
// ─────────────────────────────────────────────────────────────

// newMockAuditLogServer starts a minimal audit-log stub that accepts POST /api/audit/logs
// and returns a 201 Created response with a valid-looking evidence record.
// newMockAuditLogServer 启动一个接受存证写入的最小 audit-log 占位服务，用于测试流水线 audit 阶段。
func newMockAuditLogServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/audit/logs" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             "audit-" + fmt.Sprintf("%d", time.Now().UnixNano()),
			"snapshot_id":    "snap-" + fmt.Sprintf("%d", time.Now().UnixNano()),
			"integrity_hash": "deadbeef" + strings.Repeat("0", 56),
			"prev_hash":      "cafebabe" + strings.Repeat("0", 56),
			"via":            "audit-log-mock",
		})
	}))
}

// setupTestGRPCServer initializes a test GRPCServer with optional mock agent.
// setupTestGRPCServer 初始化测试用 GRPCServer 实例与 Mock Upstream Agent。
func setupTestGRPCServer(t *testing.T, agentHandler http.HandlerFunc) (*GRPCServer, *httptest.Server, store.TaskStore) {
	t.Helper()
	var mockServer *httptest.Server
	if agentHandler != nil {
		mockServer = httptest.NewServer(agentHandler)
	} else {
		mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/health":
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
			case "/v1/dynclassification/eval_record":
				_ = json.NewEncoder(w).Encode(map[string]any{"level": "L3", "tags": []string{"PII"}})
			case "/v1/privacy/mask", "/v1/privacy/mask_record":
				_ = json.NewEncoder(w).Encode(map[string]any{"result": "masked"})
			default:
				http.NotFound(w, r)
			}
		}))
	}

	auditMock := newMockAuditLogServer(t)
	t.Setenv("PRIVACY_AGENT_URLS", mockServer.URL)
	t.Setenv("SERVICE_HUB_AUDIT_LOG_URLS", auditMock.URL)
	cfg := config.Load()
	mc := metrics.NewCollector("service-hub-grpc-test")
	ag := agent.New(cfg, mc)
	ds := datasource.New(cfg)
	taskStore := memory.NewTaskStore()
	logger := slog.Default()

	srv := New(ag, ds, cfg, taskStore, logger)
	return srv, mockServer, taskStore
}

// TestGRPCServer_Health tests the gRPC Health RPC under reachable and unreachable agent scenarios.
// TestGRPCServer_Health 测试 gRPC 服务健康检查在 Agent 可达与不可达时的返回。
func TestGRPCServer_Health(t *testing.T) {
	t.Run("Reachable", func(t *testing.T) {
		srv, mockServer, _ := setupTestGRPCServer(t, nil)
		defer mockServer.Close()
		defer srv.Shutdown()

		resp, err := srv.Health(context.Background(), &pb.HealthRequest{})
		if err != nil {
			t.Fatalf("Health failed: %v", err)
		}
		if resp.Backend != "ok" || resp.Agent != "ok" {
			t.Errorf("Health unexpected response: %+v", resp)
		}
	})

	t.Run("Unreachable", func(t *testing.T) {
		srv, mockServer, _ := setupTestGRPCServer(t, nil)
		mockServer.Close() // 提前关闭上游模拟服务
		defer srv.Shutdown()

		resp, err := srv.Health(context.Background(), &pb.HealthRequest{})
		if err != nil {
			t.Fatalf("Health returned gRPC error: %v", err)
		}
		if resp.Agent != "unreachable" || resp.Error == "" {
			t.Errorf("Health expected unreachable status, got: %+v", resp)
		}
	})
}

// TestGRPCServer_HubStatus tests the gRPC HubStatus RPC method.
// TestGRPCServer_HubStatus 测试 HubStatus RPC 返回的运行态指标与队列计数。
func TestGRPCServer_HubStatus(t *testing.T) {
	srv, mockServer, taskStore := setupTestGRPCServer(t, nil)
	defer mockServer.Close()
	defer srv.Shutdown()

	now := time.Now()
	_ = taskStore.Save(&store.Task{ID: "t-1", Status: "running", Stage: "classify", CreatedAt: now})
	_ = taskStore.Save(&store.Task{ID: "t-2", Status: "pending", Stage: "queued", CreatedAt: now})
	_ = taskStore.Save(&store.Task{ID: "t-3", Status: "completed", Stage: "done", CreatedAt: now})

	resp, err := srv.HubStatus(context.Background(), &pb.HubStatusRequest{})
	if err != nil {
		t.Fatalf("HubStatus failed: %v", err)
	}
	if resp.Status != "running" || resp.ActiveTasks != 1 || resp.QueuedTasks != 1 || resp.CompletedTotal != 1 {
		t.Errorf("HubStatus unexpected counts: %+v", resp)
	}
}

// TestGRPCServer_Dispatch tests the gRPC Dispatch RPC method.
// TestGRPCServer_Dispatch 测试 Dispatch RPC 方法的各类入参边界校验与正常任务创建。
func TestGRPCServer_Dispatch(t *testing.T) {
	srv, mockServer, _ := setupTestGRPCServer(t, nil)
	defer mockServer.Close()
	defer srv.Shutdown()

	ctx := context.Background()

	t.Run("Validation_EmptySource", func(t *testing.T) {
		_, err := srv.Dispatch(ctx, &pb.DispatchRequest{Operation: "mask"})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument, got: %v", err)
		}
	})

	// "test.csv" 不在 canonical 注册表内 → 归一化失败即拒绝（P1-1 source 归一化）。
	t.Run("Validation_UnknownSource", func(t *testing.T) {
		_, err := srv.Dispatch(ctx, &pb.DispatchRequest{Source: "test.csv"})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument, got: %v", err)
		}
	})

	t.Run("Validation_InvalidOperation", func(t *testing.T) {
		_, err := srv.Dispatch(ctx, &pb.DispatchRequest{Source: "yibao.csv", Operation: "not_an_operator"})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument for unknown operation, got: %v", err)
		}
	})

	// P1-1：operation 降级为可选「强度提示」，生效算子由定级推导，因此缺省必须受理。
	t.Run("Validation_OperationIsOptional", func(t *testing.T) {
		resp, err := srv.Dispatch(ctx, &pb.DispatchRequest{
			Source:      "yibao.csv",
			PayloadJson: `{"name":"张三"}`,
		})
		if err != nil {
			t.Fatalf("dispatch without operation must be accepted: %v", err)
		}
		if resp.TaskId == "" {
			t.Fatalf("expected non-empty task_id, got %+v", resp)
		}
	})

	t.Run("Validation_OversizedSource", func(t *testing.T) {
		_, err := srv.Dispatch(ctx, &pb.DispatchRequest{
			Source:    strings.Repeat("a", 1025),
			Operation: "mask",
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument for oversized source, got: %v", err)
		}
	})

	t.Run("Success", func(t *testing.T) {
		resp, err := srv.Dispatch(ctx, &pb.DispatchRequest{
			Source:      "yibao.csv",
			Operation:   "mask",
			PayloadJson: `{"name":"张三"}`,
			Priority:    1,
		})
		if err != nil {
			t.Fatalf("Dispatch failed: %v", err)
		}
		if resp.TaskId == "" || resp.Status != "accepted" {
			t.Errorf("unexpected dispatch response: %+v", resp)
		}

		// 短暂等待并校验任务已持久化
		time.Sleep(50 * time.Millisecond)
		task, err := srv.GetTask(ctx, &pb.GetTaskRequest{TaskId: resp.TaskId})
		if err != nil {
			t.Fatalf("GetTask failed: %v", err)
		}
		if task.Id != resp.TaskId {
			t.Errorf("task id mismatch: %+v", task)
		}
		// 入站 source 会经 naming 归一化为 canonical datasource_id。
		if task.Source != "ds_yibao" {
			t.Errorf("expected normalized source ds_yibao, got %q", task.Source)
		}
	})
}

// TestGRPCServer_ClassifyAndDispatch tests the gRPC ClassifyAndDispatch RPC method.
// TestGRPCServer_ClassifyAndDispatch 测试分类定级并自动分发 RPC 方法。
func TestGRPCServer_ClassifyAndDispatch(t *testing.T) {
	srv, mockServer, _ := setupTestGRPCServer(t, nil)
	defer mockServer.Close()
	defer srv.Shutdown()

	ctx := context.Background()

	t.Run("Validation_EmptySource", func(t *testing.T) {
		_, err := srv.ClassifyAndDispatch(ctx, &pb.ClassifyAndDispatchRequest{})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument, got: %v", err)
		}
	})

	t.Run("Validation_OversizedSource", func(t *testing.T) {
		_, err := srv.ClassifyAndDispatch(ctx, &pb.ClassifyAndDispatchRequest{
			Source: strings.Repeat("a", 1025),
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument, got: %v", err)
		}
	})

	t.Run("Success", func(t *testing.T) {
		resp, err := srv.ClassifyAndDispatch(ctx, &pb.ClassifyAndDispatchRequest{
			Source:      "medical.csv",
			PayloadJson: `{"diagnosis":"C78.0"}`,
		})
		if err != nil {
			t.Fatalf("ClassifyAndDispatch failed: %v", err)
		}
		if resp.TaskId == "" || resp.Level != "L3" || resp.AutoOperation != "k_anon" {
			t.Errorf("unexpected ClassifyAndDispatch response: %+v", resp)
		}
	})
}

// TestGRPCServer_GetAndListTasks tests GetTask and ListTasks RPC methods.
// TestGRPCServer_GetAndListTasks 测试 GetTask 与 ListTasks RPC 方法。
func TestGRPCServer_GetAndListTasks(t *testing.T) {
	srv, mockServer, taskStore := setupTestGRPCServer(t, nil)
	defer mockServer.Close()
	defer srv.Shutdown()

	ctx := context.Background()

	// 初始化种子任务
	now := time.Now()
	_ = taskStore.Save(&store.Task{ID: "t-10", Status: "running", Stage: "fetch", Source: "src1", CreatedAt: now})
	_ = taskStore.Save(&store.Task{ID: "t-20", Status: "completed", Stage: "done", Source: "src2", CreatedAt: now.Add(time.Second)})

	t.Run("GetTask_NotFound", func(t *testing.T) {
		_, err := srv.GetTask(ctx, &pb.GetTaskRequest{TaskId: "nonexistent"})
		if status.Code(err) != codes.NotFound {
			t.Errorf("expected NotFound, got: %v", err)
		}
	})

	t.Run("GetTask_EmptyID", func(t *testing.T) {
		_, err := srv.GetTask(ctx, &pb.GetTaskRequest{TaskId: ""})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument, got: %v", err)
		}
	})

	t.Run("GetTask_Success", func(t *testing.T) {
		task, err := srv.GetTask(ctx, &pb.GetTaskRequest{TaskId: "t-10"})
		if err != nil {
			t.Fatalf("GetTask failed: %v", err)
		}
		if task.Id != "t-10" || task.Status != "running" {
			t.Errorf("unexpected task: %+v", task)
		}
	})

	t.Run("ListTasks_All", func(t *testing.T) {
		resp, err := srv.ListTasks(ctx, &pb.ListTasksRequest{})
		if err != nil {
			t.Fatalf("ListTasks failed: %v", err)
		}
		if resp.Total != 2 || len(resp.Tasks) != 2 {
			t.Errorf("unexpected list response: %+v", resp)
		}
	})

	t.Run("ListTasks_FilterStatus", func(t *testing.T) {
		resp, err := srv.ListTasks(ctx, &pb.ListTasksRequest{StatusFilter: "completed"})
		if err != nil {
			t.Fatalf("ListTasks filter failed: %v", err)
		}
		if resp.Total != 1 || len(resp.Tasks) != 1 || resp.Tasks[0].Id != "t-20" {
			t.Errorf("unexpected filtered list response: %+v", resp)
		}
	})

	t.Run("ListTasks_InvalidFilter", func(t *testing.T) {
		_, err := srv.ListTasks(ctx, &pb.ListTasksRequest{StatusFilter: "invalid_status"})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument for invalid filter, got: %v", err)
		}
	})
}

// TestGRPCServer_PipelineStatus tests the gRPC PipelineStatus RPC method.
// TestGRPCServer_PipelineStatus 测试 PipelineStatus RPC 返回各阶段活跃度和 Agent 状态。
func TestGRPCServer_PipelineStatus(t *testing.T) {
	srv, mockServer, taskStore := setupTestGRPCServer(t, nil)
	defer mockServer.Close()
	defer srv.Shutdown()

	now := time.Now()
	_ = taskStore.Save(&store.Task{ID: "t-1", Status: "running", Stage: "ingest", CreatedAt: now})
	_ = taskStore.Save(&store.Task{ID: "t-2", Status: "running", Stage: "classify", CreatedAt: now})

	resp, err := srv.PipelineStatus(context.Background(), &pb.PipelineStatusRequest{})
	if err != nil {
		t.Fatalf("PipelineStatus failed: %v", err)
	}
	if !resp.AgentOk || len(resp.Stages) != 6 {
		t.Errorf("unexpected PipelineStatus response: %+v", resp)
	}

	for _, stage := range resp.Stages {
		if stage.Name == "ingest" && (stage.Status != "processing" || stage.ActiveCount != 1) {
			t.Errorf("expected ingest processing with 1 active, got: %+v", stage)
		}
	}
}

func TestGRPCServer_ProcessTask_StopsWhenStatePersistenceFails(t *testing.T) {
	srv, mockServer, _ := setupTestGRPCServer(t, nil)
	defer mockServer.Close()
	defer srv.Shutdown()

	failingStore := &failingUpdateTaskStore{TaskStore: memory.NewTaskStore()}
	srv.tasks = failingStore
	task := &store.Task{
		ID:        "task-persist-failure",
		Status:    "pending",
		Stage:     "queued",
		Source:    "test-source",
		Operation: "none",
		CreatedAt: time.Now(),
	}
	if err := failingStore.Save(task); err != nil {
		t.Fatalf("save task: %v", err)
	}

	srv.processTask(task, task.Operation, "{}", "test-req")

	if failingStore.updateCalls != 1 {
		t.Fatalf("expected one failed stage-state write before stopping, got %d", failingStore.updateCalls)
	}
}

func TestGRPCServer_LeaseWorkerClaimsAndCompletesPendingTask(t *testing.T) {
	auditMock := newMockAuditLogServer(t)
	taskStore := newLeaseWorkerTestStore(&store.Task{
		ID:        "leased-task",
		Status:    "pending",
		Stage:     "queued",
		Source:    "test-source",
		Operation: "none",
		CreatedAt: time.Now(),
	})
	server := New(nil, nil, &config.Config{
		PGDSN:            "postgres://test",
		LeaseTTL:         1,
		AuditLogBaseURLs: []string{auditMock.URL},
		AuditLogTimeout:  1,
	}, taskStore, slog.Default())
	defer server.Shutdown()

	if err := server.StartLeaseWorker("hub-test", time.Second); err != nil {
		t.Fatalf("start lease worker: %v", err)
	}

	select {
	case taskID := <-taskStore.completed:
		if taskID != "leased-task" {
			t.Fatalf("expected leased-task completion, got %q", taskID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lease worker did not complete the pending task")
	}
}

// TestGRPCServer_ProcessTask_FailureBranches tests error branches during asynchronous pipeline processing.
// TestGRPCServer_ProcessTask_FailureBranches 测试异步流水线各异常分支（分类失败、脱敏失败、优雅停机取消）。
func TestGRPCServer_ProcessTask_FailureBranches(t *testing.T) {
	t.Run("ClassifyFails", func(t *testing.T) {
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/medical/process" {
				http.Error(w, "internal model error", http.StatusInternalServerError)
				return
			}
			http.NotFound(w, r)
		}))
		defer mockServer.Close()

		t.Setenv("PRIVACY_AGENT_URLS", mockServer.URL)
		cfg := config.Load()
		ag := agent.New(cfg, metrics.NewCollector("service-hub-grpc-test"))
		ds := datasource.New(cfg)
		taskStore := memory.NewTaskStore()
		srv := New(ag, ds, cfg, taskStore, slog.Default())
		defer srv.Shutdown()

		task := &store.Task{
			ID:        "task-fail-1",
			Status:    "pending",
			Source:    "test.csv",
			Operation: "classify",
			CreatedAt: time.Now(),
		}
		_ = taskStore.Save(task)

		// 同步调用 processTask 验证失败状态更新
		srv.processTask(task, "classify", `{"record":"test"}`, "test-req")

		updated, err := taskStore.Get("task-fail-1")
		if err != nil {
			t.Fatalf("Get task failed: %v", err)
		}
		if updated.Status != "failed" || !strings.Contains(updated.Error, "medical pipeline failed") {
			t.Errorf("expected failed status with medical pipeline error, got: %+v", updated)
		}
	})

	t.Run("MaskFails", func(t *testing.T) {
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/medical/process" {
				http.Error(w, "masking engine down", http.StatusInternalServerError)
				return
			}
			http.NotFound(w, r)
		}))
		defer mockServer.Close()

		t.Setenv("PRIVACY_AGENT_URLS", mockServer.URL)
		cfg := config.Load()
		ag := agent.New(cfg, metrics.NewCollector("service-hub-grpc-test"))
		ds := datasource.New(cfg)
		taskStore := memory.NewTaskStore()
		srv := New(ag, ds, cfg, taskStore, slog.Default())
		defer srv.Shutdown()

		task := &store.Task{
			ID:        "task-fail-2",
			Status:    "pending",
			Source:    "test.csv",
			Operation: "mask",
			CreatedAt: time.Now(),
		}
		_ = taskStore.Save(task)

		srv.processTask(task, "mask", `{"name":"test"}`, "test-req")

		updated, err := taskStore.Get("task-fail-2")
		if err != nil {
			t.Fatalf("Get task failed: %v", err)
		}
		if updated.Status != "failed" || !strings.Contains(updated.Error, "medical pipeline failed") {
			t.Errorf("expected failed status with medical pipeline error, got: %+v", updated)
		}
	})

	t.Run("CancellationOnShutdown", func(t *testing.T) {
		srv, mockServer, taskStore := setupTestGRPCServer(t, nil)
		defer mockServer.Close()

		task := &store.Task{
			ID:        "task-cancel",
			Status:    "pending",
			Source:    "test.csv",
			Operation: "mask",
			CreatedAt: time.Now(),
		}
		_ = taskStore.Save(task)

		srv.wg.Add(1)
		go func() {
			defer srv.wg.Done()
			srv.processTask(task, "mask", `{}`, "test-req")
		}()

		time.Sleep(20 * time.Millisecond)
		srv.Shutdown()

		updated, err := taskStore.Get("task-cancel")
		if err != nil {
			t.Fatalf("Get task failed: %v", err)
		}
		if updated.Status != "failed" || !strings.Contains(updated.Error, "server shutting down") {
			t.Errorf("expected failed status with shutting down error, got: %+v", updated)
		}
	})
}

func TestGRPCServer_LocalPendingWorker(t *testing.T) {
	auditMock := newMockAuditLogServer(t)
	taskStore := memory.NewTaskStore()
	server := New(nil, nil, &config.Config{
		AuditLogBaseURLs: []string{auditMock.URL},
		AuditLogTimeout:  1,
	}, taskStore, slog.Default())
	defer server.Shutdown()

	task := &store.Task{
		ID:        "grpc-recovered-task",
		Status:    "pending",
		Stage:     "queued",
		Source:    "test-source",
		Operation: "none",
		// 空载荷避免在 agent 为 nil 的测试路径中进入 engine 分类 stage。
		CreatedAt: time.Now(),
	}
	if err := taskStore.Save(task); err != nil {
		t.Fatalf("save task: %v", err)
	}

	if err := server.StartLocalWorker(); err != nil {
		t.Fatalf("start local worker: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		tCheck, err := taskStore.Get("grpc-recovered-task")
		if err == nil && tCheck.Status == "completed" {
			completed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !completed {
		tCheck, _ := taskStore.Get("grpc-recovered-task")
		t.Fatalf("expected task to be completed by grpc local worker, got state: %+v", tCheck)
	}
}
