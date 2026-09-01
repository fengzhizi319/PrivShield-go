package grpcserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/fengzhizi319/PrivShield/console/bff-go/internal/agent"
	"github.com/fengzhizi319/PrivShield/console/bff-go/internal/config"
	pb "github.com/fengzhizi319/PrivShield/console/bff-go/proto"
	pkgobs "github.com/fengzhizi319/PrivShield/pkg/observability"
)

// mockAgentServer implements pb.PrivacyServiceServer for testing
type mockAgentServer struct {
	pb.UnimplementedPrivacyServiceServer
}

func (m *mockAgentServer) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{
		Status:    "ok",
		Namespace: "default",
	}, nil
}

func (m *mockAgentServer) Mask(ctx context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
	return &pb.MaskResponse{
		Result: "138****0000",
	}, nil
}

func setupBufConnServer(t *testing.T) (pb.PrivacyServiceClient, func()) {
	t.Helper()
	buffer := 1024 * 1024
	lis := bufconn.Listen(buffer)

	mockAgent := &mockAgentServer{}
	agentGRPC := grpc.NewServer()
	pb.RegisterPrivacyServiceServer(agentGRPC, mockAgent)

	go func() {
		_ = agentGRPC.Serve(lis)
	}()

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial bufnet: %v", err)
	}

	agentClient := agent.NewFromConnection(conn)
	cfg := &config.Config{}
	logger := pkgobs.NewLogger("text", "debug")
	bffServer := New(agentClient, cfg, logger)

	bffLis := bufconn.Listen(buffer)
	bffGRPC := grpc.NewServer()
	pb.RegisterPrivacyServiceServer(bffGRPC, bffServer)

	go func() {
		_ = bffGRPC.Serve(bffLis)
	}()

	bffConn, err := grpc.NewClient("passthrough://bufnet2",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return bffLis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial bff bufnet: %v", err)
	}

	client := pb.NewPrivacyServiceClient(bffConn)
	cleanup := func() {
		_ = bffConn.Close()
		bffGRPC.Stop()
		_ = conn.Close()
		agentGRPC.Stop()
	}

	return client, cleanup
}

func TestGRPCServer_HealthAndMask(t *testing.T) {
	client, cleanup := setupBufConnServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	hResp, err := client.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		t.Fatalf("Health RPC failed: %v", err)
	}
	if hResp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", hResp.Status)
	}

	mResp, err := client.Mask(ctx, &pb.MaskRequest{
		FieldName: "phone",
		Value:     "13800000000",
	})
	if err != nil {
		t.Fatalf("Mask RPC failed: %v", err)
	}
	if mResp.Result != "138****0000" {
		t.Errorf("expected '138****0000', got %q", mResp.Result)
	}
}

func generateTestCert(t *testing.T, dir string) (certFile, keyFile, caFile string) {
	t.Helper()
	caPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate ca key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"PrivShield Test CA"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caPriv.PublicKey, caPriv)
	if err != nil {
		t.Fatalf("failed to create ca cert: %v", err)
	}
	caFile = filepath.Join(dir, "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if err := os.WriteFile(caFile, caPEM, 0600); err != nil {
		t.Fatalf("write ca file: %v", err)
	}

	srvPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate srv key: %v", err)
	}
	srvTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"PrivShield Server"},
		},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:    []string{"localhost"},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTemplate, caTemplate, &srvPriv.PublicKey, caPriv)
	if err != nil {
		t.Fatalf("failed to create srv cert: %v", err)
	}
	certFile = filepath.Join(dir, "server.pem")
	keyFile = filepath.Join(dir, "server-key.pem")
	srvCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvDER})
	srvKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(srvPriv)})
	_ = os.WriteFile(certFile, srvCertPEM, 0600)
	_ = os.WriteFile(keyFile, srvKeyPEM, 0600)
	return certFile, keyFile, caFile
}

func TestGRPCServer_TLS_mTLS(t *testing.T) {
	tempDir := t.TempDir()
	certFile, keyFile, caFile := generateTestCert(t, tempDir)

	cfg := &config.Config{
		ConsoleTLSEnabled:    true,
		ConsoleTLSCertFile:   certFile,
		ConsoleTLSKeyFile:    keyFile,
		ConsoleTLSCAFile:     caFile,
		ConsoleTLSClientAuth: "require",
	}
	logger := pkgobs.NewLogger("text", "debug")
	bffServer := New(nil, cfg, logger)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()

	go func() {
		_ = bffServer.Start(addr)
	}()
	defer bffServer.Stop()

	// Wait for server to bind
	time.Sleep(100 * time.Millisecond)

	// Test client with mTLS
	clientCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("load client cert: %v", err)
	}
	caPEM, _ := os.ReadFile(caFile)
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      caPool,
		ServerName:   "localhost",
	})

	var conn *grpc.ClientConn
	var hResp *pb.HealthResponse
	for i := 0; i < 30; i++ {
		time.Sleep(50 * time.Millisecond)
		conn, err = grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
		if err != nil {
			continue
		}
		client := pb.NewPrivacyServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		hResp, err = client.Health(ctx, &pb.HealthRequest{})
		cancel()
		if err == nil {
			break
		}
		_ = conn.Close()
	}
	if err != nil {
		t.Fatalf("mTLS gRPC Health call failed: %v", err)
	}
	defer conn.Close()

	if hResp.Status != "degraded" && hResp.Status != "ok" {
		t.Errorf("unexpected status: %s", hResp.Status)
	}
}
