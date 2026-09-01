// Package grpcserver provides a thin, reusable gRPC server wrapper.
//
// 从 engine-go/internal/grpcserver/server.go 下沉，供 services 与 console 复用通用 gRPC
// 生命周期管理（监听、拦截器链、服务注册、优雅停机）。
package grpcserver

import (
	"net"

	"google.golang.org/grpc"
)

// Server wraps a grpc.Server and its configuration.
type Server struct {
	*grpc.Server
	address string
	opts    []grpc.ServerOption
}

// New creates a new Server with the given listen address and options.
func New(address string, opts ...grpc.ServerOption) *Server {
	return &Server{
		address: address,
		opts:    opts,
	}
}

// WithOptions appends additional grpc.ServerOption to the server configuration.
func (s *Server) WithOptions(opts ...grpc.ServerOption) *Server {
	s.opts = append(s.opts, opts...)
	return s
}

// WithUnaryInterceptor appends unary interceptors to the server configuration.
func (s *Server) WithUnaryInterceptor(interceptors ...grpc.UnaryServerInterceptor) *Server {
	s.opts = append(s.opts, grpc.ChainUnaryInterceptor(interceptors...))
	return s
}

// WithStreamInterceptor appends stream interceptors to the server configuration.
func (s *Server) WithStreamInterceptor(interceptors ...grpc.StreamServerInterceptor) *Server {
	s.opts = append(s.opts, grpc.ChainStreamInterceptor(interceptors...))
	return s
}

// RegisterService registers a service and its implementation.
// It lazily builds the underlying grpc.Server if necessary.
func (s *Server) RegisterService(desc *grpc.ServiceDesc, impl any) {
	s.ensureBuilt()
	s.Server.RegisterService(desc, impl)
}

// Serve listens on the configured address and serves gRPC requests.
// It lazily builds the underlying grpc.Server if necessary.
func (s *Server) Serve() error {
	s.ensureBuilt()
	lis, err := net.Listen("tcp", s.address)
	if err != nil {
		return err
	}
	return s.Server.Serve(lis)
}

// ServeListener serves gRPC requests on the provided listener.
// It lazily builds the underlying grpc.Server if necessary.
func (s *Server) ServeListener(lis net.Listener) error {
	s.ensureBuilt()
	return s.Server.Serve(lis)
}

// GracefulStop stops the underlying grpc.Server gracefully.
func (s *Server) GracefulStop() {
	if s.Server != nil {
		s.Server.GracefulStop()
	}
}

// Stop forcibly stops the underlying grpc.Server.
func (s *Server) Stop() {
	if s.Server != nil {
		s.Server.Stop()
	}
}

func (s *Server) ensureBuilt() {
	if s.Server == nil {
		s.Server = grpc.NewServer(s.opts...)
	}
}
