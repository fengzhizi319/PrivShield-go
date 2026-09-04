package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/fengzhizi319/PrivShield-go/engine-go/internal/grpcserver/proto"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/observability"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/service"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("AGENT_REST_ENABLED", "")
	t.Setenv("AGENT_GRPC_ENABLED", "")

	cfg := loadConfig()
	if !cfg.RESTEnabled {
		t.Fatal("RESTEnabled must default to true")
	}
	if !cfg.GRPCEnabled {
		t.Fatal("GRPCEnabled must default to true")
	}
	if cfg.RateLimitRPS != 1000 {
		t.Fatalf("expected RateLimitRPS=1000, got %d", cfg.RateLimitRPS)
	}
}

func TestLoadConfigEnvOverrides(t *testing.T) {
	t.Setenv("AGENT_REST_ENABLED", "false")
	t.Setenv("AGENT_GRPC_ENABLED", "true")

	cfg := loadConfig()
	if cfg.RESTEnabled {
		t.Fatal("RESTEnabled should be false")
	}
	if !cfg.GRPCEnabled {
		t.Fatal("GRPCEnabled should be true")
	}
}

func TestRESTServerRunnerLifecycle(t *testing.T) {
	cfg := loadConfig()
	cfg.RESTPort = 0 // 使用系统随机端口
	cfg.RateLimitRPS = 0

	svcCfg := service.DefaultConfig()
	svc, err := service.NewPrivacyService(svcCfg)
	if err != nil {
		t.Fatalf("failed to init PrivacyService: %v", err)
	}
	metrics := observability.NewEngineMetrics()

	runner, err := newRESTServerRunner(cfg, svc, metrics)
	if err != nil {
		t.Fatalf("failed to create RESTServerRunner: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runner.Start()
	}()

	// 实际发起 HTTP 连接测试端点健康状态
	client := &http.Client{Timeout: 2 * time.Second}
	readyURL := fmt.Sprintf("http://%s/readyz", runner.Address())

	var lastErr error
	var readyOK bool
	for i := 0; i < 30; i++ {
		resp, err := client.Get(readyURL)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				readyOK = true
				_ = body
				break
			}
			lastErr = fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !readyOK {
		t.Fatalf("failed to query /readyz endpoint on %s: %v", runner.Address(), lastErr)
	}

	// 测试 Prometheus 指标端点 /metrics
	metricsURL := fmt.Sprintf("http://%s/metrics", runner.Address())
	metricsResp, err := client.Get(metricsURL)
	if err != nil {
		t.Fatalf("failed to query /metrics on %s: %v", runner.Address(), err)
	}
	_ = metricsResp.Body.Close()
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /metrics, got %d", metricsResp.StatusCode)
	}

	// 优雅停机
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runner.Shutdown(ctx); err != nil {
		t.Fatalf("REST server shutdown failed: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			t.Fatalf("unexpected start error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server shutdown")
	}

	// 断言停机后端口已关闭
	_, postShutdownErr := client.Get(readyURL)
	if postShutdownErr == nil {
		t.Fatal("expected connection failure after REST server shutdown, but request succeeded")
	}
}

func TestGRPCServerRunnerLifecycle(t *testing.T) {
	cfg := loadConfig()
	cfg.GRPCPort = 0 // 操作系统随机分配端口
	cfg.TLSEnabled = false

	svcCfg := service.DefaultConfig()
	svc, err := service.NewPrivacyService(svcCfg)
	if err != nil {
		t.Fatalf("failed to init PrivacyService: %v", err)
	}
	metrics := observability.NewEngineMetrics()

	runner, err := newGRPCServerRunner(cfg, svc, metrics)
	if err != nil {
		t.Fatalf("failed to create GRPCServerRunner: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runner.Start()
	}()

	// 实际发起 gRPC 客户端连接与 RPC 调用测试
	conn, err := grpc.NewClient(
		runner.Address(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial gRPC server at %s: %v", runner.Address(), err)
	}
	defer conn.Close()

	grpcClient := pb.NewPrivacyServiceClient(conn)

	// 1. 测试 Health 探针 RPC
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var healthResp *pb.HealthResponse
	for i := 0; i < 30; i++ {
		healthResp, err = grpcClient.Health(ctx, &pb.HealthRequest{})
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("gRPC Health RPC failed on %s: %v", runner.Address(), err)
	}
	if healthResp.GetStatus() != "ok" {
		t.Fatalf("expected Health status 'ok', got %q", healthResp.GetStatus())
	}

	// 2. 测试业务 Mask RPC 实际数据脱敏链路
	maskResp, err := grpcClient.Mask(ctx, &pb.MaskRequest{
		FieldName: "phone",
		Value:     "13800138000",
	})
	if err != nil {
		t.Fatalf("gRPC Mask RPC failed: %v", err)
	}
	if maskResp.GetResult() == "" || maskResp.GetResult() == "13800138000" {
		t.Fatalf("expected masked phone result, got %q", maskResp.GetResult())
	}

	// 优雅停机
	if err := runner.Shutdown(2 * time.Second); err != nil {
		t.Fatalf("gRPC server shutdown failed: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected start error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for gRPC server stop")
	}
}
