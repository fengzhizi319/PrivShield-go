package grpcserver

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestNewServer_WithOptions(t *testing.T) {
	s := New("127.0.0.1:0").
		WithOptions(grpc.MaxConcurrentStreams(10)).
		WithUnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			return handler(ctx, req)
		})
	if s == nil {
		t.Fatal("New returned nil")
	}
}

func TestServer_ServeAndStop(t *testing.T) {
	s := New("127.0.0.1:0")

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()

	// Register a health service to verify registration works.
	hs := health.NewServer()
	s.RegisterService(&grpc_health_v1.Health_ServiceDesc, hs)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- s.ServeListener(lis)
	}()

	time.Sleep(50 * time.Millisecond)
	s.Stop()

	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("ServeListener returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeListener did not stop")
	}
}

func TestServer_GracefulStop(t *testing.T) {
	s := New("127.0.0.1:0")
	// GracefulStop should be safe before Serve.
	s.GracefulStop()
}
