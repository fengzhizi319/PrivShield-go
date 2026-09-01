// Package grpcserver_test contains unit and integration tests for the datasource-mgr gRPC server.
// Package grpcserver_test 包含 datasource-mgr 模块 gRPC 服务的单元与集成测试套件。
//
// ==============================================================================
// Test Suite Coverage / 测试套件覆盖范围：
// ==============================================================================
// 1. 服务初始化与生命周期测试 (TestGRPCHealth):
//    - 验证健康检查接口返回 "ok" 状态以及正确的 moduleVia 标识。
// 2. 模拟数据源查询接口全覆盖 (TestGRPCApis):
//    - API 1 ~ 4 专用接口验证 (GetYibaoData, GetKangyangData, GetMockData3, GetMockData4)；
//    - 通用路由查询与资产元数据接口验证 (GetDataBySource, ListMockSources, GetDataSource, TestConnection)。
// 3. 入参校验与错误码防御测试 (TestGRPCValidationErrors):
//    - 校验空参数时的 codes.InvalidArgument 错误拦截；
//    - 校验不存在的数据源 ID 时的 codes.NotFound 错误拦截。
// 4. mTLS 安全凭证与公钥指纹固定测试 (TestBuildServerCredentials):
//    - 动态生成内存临时 CA 根证书与 RSA 2048 密钥对；
//    - 覆盖 TLS 关闭、证书缺失、单向 TLS、双向 mTLS（ClientAuth）以及公钥指纹固定（Key Pinning）场景。
// ==============================================================================

package grpcserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/internal/config"
	pb "github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/proto"
)

// setupTestGRPCServer starts an in-process ephemeral gRPC server for testing.
// setupTestGRPCServer 在本地随机空闲端口（127.0.0.1:0）启动一个临时的 gRPC 服务器并创建测试客户端，
// 返回客户端实例与清理闭包（自动释放连接、端口和后台 goroutine）。
func setupTestGRPCServer(t *testing.T) (pb.DataSourceManagerServiceClient, func()) {
	t.Helper()

	// 1. 初始化测试配置与日志记录器
	cfg := config.Load()
	logger := pkgobs.NewLogger("text", "debug")
	srvImpl := New(cfg, logger)

	// 2. 随机分配本地可用端口
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	// 3. 构建 gRPC 服务并注册 Protobuf 服务定义
	s := grpc.NewServer()
	pb.RegisterDataSourceManagerServiceServer(s, srvImpl)

	go func() {
		_ = s.Serve(lis)
	}()

	// 4. 创建本地明文 gRPC 客户端连接
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	client := pb.NewDataSourceManagerServiceClient(conn)

	// 5. 资源清理闭包
	cleanup := func() {
		_ = conn.Close()
		s.Stop()
		srvImpl.Shutdown()
		_ = lis.Close()
	}

	return client, cleanup
}

// TestGRPCHealth verifies the self-health endpoint response and module identification.
// TestGRPCHealth 验证健康检查接口返回 "ok" 状态以及正确的 moduleVia 标识。
func TestGRPCHealth(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	resp, err := client.Health(context.Background(), &pb.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	if resp.Status != "ok" || resp.Via != "datasource-mgr" {
		t.Errorf("unexpected health response: %+v", resp)
	}
}

// TestGRPCApis tests all functional mock data query endpoints through the gRPC interface.
// TestGRPCApis 测试 gRPC 暴露的所有业务查询接口（医保、康养、预留政务数据、通用数据源动态查询及连通性测试）。
func TestGRPCApis(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	ctx := context.Background()

	// ── API 1: 医保就医与结算模拟数据 (GetYibaoData) ──────────────────────
	yibaoResp, err := client.GetYibaoData(ctx, &pb.DataQueryRequest{Limit: 5, Offset: 0})
	if err != nil {
		t.Fatalf("GetYibaoData failed: %v", err)
	}
	if yibaoResp.SourceId != "ds_yibao" || yibaoResp.Limit != 5 {
		t.Errorf("unexpected yibao response: %+v", yibaoResp)
	}

	// ── API 2: 康养体检与慢病模拟数据 (GetKangyangData) ───────────────────
	kangResp, err := client.GetKangyangData(ctx, &pb.DataQueryRequest{Limit: 5, Offset: 0})
	if err != nil {
		t.Fatalf("GetKangyangData failed: %v", err)
	}
	if kangResp.SourceId != "ds_kangyang" || kangResp.Limit != 5 {
		t.Errorf("unexpected kangyang response: %+v", kangResp)
	}

	// ── API 3: 预留政务模拟数据源 3 (GetMockData3) ─────────────────────────
	m3Resp, err := client.GetMockData3(ctx, &pb.DataQueryRequest{Limit: 5})
	if err != nil {
		t.Fatalf("GetMockData3 failed: %v", err)
	}
	if m3Resp.SourceId != "ds_mock3" || len(m3Resp.Records) == 0 {
		t.Errorf("unexpected mock3 response: %+v", m3Resp)
	}

	// ── API 4: 预留政务模拟数据源 4 (GetMockData4) ─────────────────────────
	m4Resp, err := client.GetMockData4(ctx, &pb.DataQueryRequest{Limit: 5})
	if err != nil {
		t.Fatalf("GetMockData4 failed: %v", err)
	}
	if m4Resp.SourceId != "ds_mock4" || len(m4Resp.Records) == 0 {
		t.Errorf("unexpected mock4 response: %+v", m4Resp)
	}

	// ── 通用数据源按 ID 路由查询 (GetDataBySource) ──────────────────────────
	bySrcResp, err := client.GetDataBySource(ctx, &pb.SourceDataQueryRequest{SourceId: "ds_yibao", Limit: 3})
	if err != nil {
		t.Fatalf("GetDataBySource failed: %v", err)
	}
	if bySrcResp.SourceId != "ds_yibao" {
		t.Errorf("unexpected GetDataBySource response: %+v", bySrcResp)
	}

	// ── 模拟数据源资产目录列表 (ListMockSources) ───────────────────────────
	listResp, err := client.ListMockSources(ctx, &pb.ListMockSourcesRequest{})
	if err != nil {
		t.Fatalf("ListMockSources failed: %v", err)
	}
	if listResp.Total < 2 {
		t.Errorf("expected at least 2 sources, got %d", listResp.Total)
	}

	// ── 单个数据源详情查询 (GetDataSource) ──────────────────────────────────
	dsResp, err := client.GetDataSource(ctx, &pb.GetDataSourceRequest{Id: "ds_yibao"})
	if err != nil {
		t.Fatalf("GetDataSource failed: %v", err)
	}
	if dsResp.Id != "ds_yibao" {
		t.Errorf("unexpected GetDataSource response: %+v", dsResp)
	}

	// ── 数据源连通性测试 (TestConnection) ────────────────────────────────────
	connResp, err := client.TestConnection(ctx, &pb.TestConnectionRequest{Id: "ds_kangyang"})
	if err != nil {
		t.Fatalf("TestConnection failed: %v", err)
	}
	if !connResp.Success || connResp.DatasourceId != "ds_kangyang" {
		t.Errorf("unexpected TestConnection response: %+v", connResp)
	}
}

// TestGRPCValidationErrors verifies error handling and gRPC status codes on invalid inputs.
// TestGRPCValidationErrors 验证在非法请求入参（如空 ID、未注册数据源）时的 gRPC 错误状态码映射。
func TestGRPCValidationErrors(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	ctx := context.Background()

	// 1. GetDataBySource 空 source_id 应返回 InvalidArgument 错误
	_, err := client.GetDataBySource(ctx, &pb.SourceDataQueryRequest{SourceId: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for empty source_id, got: %v", err)
	}

	// 2. GetDataBySource 不存在的数据源 ID 应返回 NotFound 错误
	_, err = client.GetDataBySource(ctx, &pb.SourceDataQueryRequest{SourceId: "unknown_123"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for unknown source_id, got: %v", err)
	}

	// 3. GetDataSource 空 id 应返回 InvalidArgument 错误
	_, err = client.GetDataSource(ctx, &pb.GetDataSourceRequest{Id: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for empty id, got: %v", err)
	}

	// 4. GetDataSource 不存在的 id 应返回 NotFound 错误
	_, err = client.GetDataSource(ctx, &pb.GetDataSourceRequest{Id: "non_existent"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for non-existent id, got: %v", err)
	}

	// 5. TestConnection 空 id 应返回 InvalidArgument 错误
	_, err = client.TestConnection(ctx, &pb.TestConnectionRequest{Id: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for empty test id, got: %v", err)
	}
}

// ==============================================================================
// mTLS Credentials and Key Pinning Helper & Tests / mTLS 凭据与公钥固定测试
// ==============================================================================

// generateTestCertAndKey dynamically creates a self-signed CA, server cert/key pair, and client public key file.
// generateTestCertAndKey 动态生成测试用的 CA 根证书、带 IP SAN (127.0.0.1) 的服务端证书/私钥，以及客户端公钥 PEM 文件。
func generateTestCertAndKey(t *testing.T, tmpDir string) (string, string, string, string) {
	t.Helper()

	// 1. 生成 CA 私钥与自签名根证书
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "PrivShield-DS-CA",
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

	// 2. 生成服务端私钥与由 CA 签发的操作证书
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

	// 3. 导出客户端公钥 PEM 用于指纹固定校验
	pubBytes, err := x509.MarshalPKIXPublicKey(&srvKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal pubkey: %v", err)
	}
	pubKeyFile := filepath.Join(tmpDir, "client_pub.pem")
	_ = os.WriteFile(pubKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}), 0600)

	return caFile, srvCertFile, srvKeyFile, pubKeyFile
}

// TestBuildServerCredentials verifies TLS and mTLS credentials construction with different security configurations.
// TestBuildServerCredentials 验证 BuildServerCredentials 在不同配置组合（未启用 TLS、缺失私钥、有效单向 TLS、双向 mTLS、公钥固定）下的行为。
func TestBuildServerCredentials(t *testing.T) {
	tmpDir := t.TempDir()
	caFile, srvCert, srvKey, pubKey := generateTestCertAndKey(t, tmpDir)

	// 1. 测试用例 1：TLS 未启用时应返回错误
	cfg := &config.Config{TLSEnabled: false}
	if _, err := BuildServerCredentials(cfg); err == nil {
		t.Errorf("expected error when TLS is disabled")
	}

	// 2. 测试用例 2：启用 TLS 但证书/私钥文件路径缺失时应返回错误
	cfg = &config.Config{TLSEnabled: true}
	if _, err := BuildServerCredentials(cfg); err == nil {
		t.Errorf("expected error when cert/key missing")
	}

	// 3. 测试用例 3：合法的单向 TLS 配置
	cfg = &config.Config{
		TLSEnabled:  true,
		TLSCertFile: srvCert,
		TLSKeyFile:  srvKey,
	}
	creds, err := BuildServerCredentials(cfg)
	if err != nil || creds == nil {
		t.Fatalf("failed to build simple TLS credentials: %v", err)
	}

	// 4. 测试用例 4：合法的双向 mTLS 配置（验证 Client CA 证书池与 RequireAndVerifyClientCert 模式）
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

	// 5. 测试用例 5：合法的双向 mTLS + 客户端公钥指纹固定配置
	cfg.TLSPinnedPubKeyFile = pubKey
	creds, err = BuildServerCredentials(cfg)
	if err != nil || creds == nil {
		t.Fatalf("failed to build mTLS credentials with public key pinning: %v", err)
	}
}

// TestBuildServerTLSConfig_HTTPS_MTLS verifies HTTPS REST server operation with mTLS and public key pinning.
// TestBuildServerTLSConfig_HTTPS_MTLS 验证基于 BuildServerTLSConfig 启动的 HTTPS 服务器支持完整的双向证书校验与公钥固定。
func TestBuildServerTLSConfig_HTTPS_MTLS(t *testing.T) {
	tmpDir := t.TempDir()
	caFile, srvCertFile, srvKeyFile, pubKeyFile := generateTestCertAndKey(t, tmpDir)

	// 1. 构建带有双向认证与公钥固定的服务端 TLS 配置
	cfg := &config.Config{
		TLSEnabled:          true,
		TLSCertFile:         srvCertFile,
		TLSKeyFile:          srvKeyFile,
		TLSCAFile:           caFile,
		TLSClientAuth:       "require",
		TLSPinnedPubKeyFile: pubKeyFile,
	}
	tlsConfig, err := BuildServerTLSConfig(cfg)
	if err != nil {
		t.Fatalf("BuildServerTLSConfig failed: %v", err)
	}

	// 2. 启动测试 HTTPS 服务器
	rawListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on tcp: %v", err)
	}
	tlsListener := tls.NewListener(rawListener, tlsConfig)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","mode":"https_mtls"}`))
	})

	srv := &http.Server{
		Handler:   mux,
		TLSConfig: tlsConfig,
	}
	go func() {
		_ = srv.Serve(tlsListener)
	}()
	defer func() {
		_ = srv.Close()
		_ = tlsListener.Close()
	}()

	serverAddr := rawListener.Addr().String()
	targetURL := fmt.Sprintf("https://%s/api/health", serverAddr)

	// 3. 读取 CA 证书池
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatalf("read ca.pem: %v", err)
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	// 4. 读取与服务端生成时公钥一致的证书作为合法的客户端证书
	clientCert, err := tls.LoadX509KeyPair(srvCertFile, srvKeyFile)
	if err != nil {
		t.Fatalf("load client cert pair: %v", err)
	}

	// Case A: 携带合法客户端证书发起 HTTPS 请求 -> 应成功 200 OK
	validClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{clientCert},
				RootCAs:      caPool,
				ServerName:   "127.0.0.1",
			},
		},
	}
	resp, err := validClient.Get(targetURL)
	if err != nil {
		t.Fatalf("valid HTTPS mTLS request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Case B: 未携带客户端证书发起请求 -> 应当握手失败（被 mTLS 阻断）
	noCertClient := &http.Client{
		Timeout: 1 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    caPool,
				ServerName: "127.0.0.1",
			},
		},
	}
	_, noCertErr := noCertClient.Get(targetURL)
	if noCertErr == nil {
		t.Errorf("expected handshake failure when client certificate is missing, but request succeeded")
	}
}
