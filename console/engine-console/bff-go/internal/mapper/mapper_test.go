package mapper

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/agent"
	pb "github.com/fengzhizi319/PrivShield-go/console/bff-go/proto"
)

// fakePrivacyServer 是一个可注入行为的假 gRPC 服务器。
// 通过嵌入 pb.UnimplementedPrivacyServiceServer，只重写测试关心的 RPC。
type fakePrivacyServer struct {
	pb.UnimplementedPrivacyServiceServer

	HealthFunc           func(context.Context, *pb.HealthRequest) (*pb.HealthResponse, error)
	MaskFunc             func(context.Context, *pb.MaskRequest) (*pb.MaskResponse, error)
	DPCountFunc          func(context.Context, *pb.DPRequest) (*pb.DPResponse, error)
	KAnonymizeRecordFunc func(context.Context, *pb.KAnonymizeRequest) (*pb.KAnonymizeResponse, error)
	ObfuscateQueryFunc   func(context.Context, *pb.ObfuscateQueryRequest) (*pb.ObfuscateQueryResponse, error)
	DynEvalFunc          func(context.Context, *pb.DynEvalRequest) (*pb.DynEvalResponse, error)
	DynStandardsFunc     func(context.Context, *pb.DynStandardsRequest) (*pb.DynStandardsResponse, error)
}

func (f *fakePrivacyServer) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	if f.HealthFunc != nil {
		return f.HealthFunc(ctx, req)
	}
	return f.UnimplementedPrivacyServiceServer.Health(ctx, req)
}

func (f *fakePrivacyServer) Mask(ctx context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
	if f.MaskFunc != nil {
		return f.MaskFunc(ctx, req)
	}
	return f.UnimplementedPrivacyServiceServer.Mask(ctx, req)
}

func (f *fakePrivacyServer) DPCount(ctx context.Context, req *pb.DPRequest) (*pb.DPResponse, error) {
	if f.DPCountFunc != nil {
		return f.DPCountFunc(ctx, req)
	}
	return f.UnimplementedPrivacyServiceServer.DPCount(ctx, req)
}

func (f *fakePrivacyServer) KAnonymizeRecord(ctx context.Context, req *pb.KAnonymizeRequest) (*pb.KAnonymizeResponse, error) {
	if f.KAnonymizeRecordFunc != nil {
		return f.KAnonymizeRecordFunc(ctx, req)
	}
	return f.UnimplementedPrivacyServiceServer.KAnonymizeRecord(ctx, req)
}

func (f *fakePrivacyServer) ObfuscateQuery(ctx context.Context, req *pb.ObfuscateQueryRequest) (*pb.ObfuscateQueryResponse, error) {
	if f.ObfuscateQueryFunc != nil {
		return f.ObfuscateQueryFunc(ctx, req)
	}
	return f.UnimplementedPrivacyServiceServer.ObfuscateQuery(ctx, req)
}

func (f *fakePrivacyServer) DynEval(ctx context.Context, req *pb.DynEvalRequest) (*pb.DynEvalResponse, error) {
	if f.DynEvalFunc != nil {
		return f.DynEvalFunc(ctx, req)
	}
	return f.UnimplementedPrivacyServiceServer.DynEval(ctx, req)
}

func (f *fakePrivacyServer) DynStandards(ctx context.Context, req *pb.DynStandardsRequest) (*pb.DynStandardsResponse, error) {
	if f.DynStandardsFunc != nil {
		return f.DynStandardsFunc(ctx, req)
	}
	return f.UnimplementedPrivacyServiceServer.DynStandards(ctx, req)
}

// startBufconnServer 在内存中启动一个 gRPC 服务器并返回对应的客户端连接。
func startBufconnServer(t *testing.T, fs *fakePrivacyServer) (*agent.Client, func()) {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	pb.RegisterPrivacyServiceServer(server, fs)

	errChan := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil {
			errChan <- err
		}
	}()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		server.Stop()
		t.Fatalf("failed to create bufconn client: %v", err)
	}

	cleanup := func() {
		_ = conn.Close()
		server.Stop()
	}
	return agent.NewFromConnection(conn), cleanup
}

func TestDispatchHealth(t *testing.T) {
	fs := &fakePrivacyServer{
		HealthFunc: func(_ context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
			return &pb.HealthResponse{Status: "ok", Namespace: "demo"}, nil
		},
	}
	client, cleanup := startBufconnServer(t, fs)
	defer cleanup()

	mp := New()
	resp, err := mp.Dispatch(context.Background(), client.Raw(), "/v1/privacy/health", nil)
	if err != nil {
		t.Fatalf("Dispatch health failed: %v", err)
	}
	m, ok := resp.(map[string]string)
	if !ok {
		t.Fatalf("unexpected response type: %T", resp)
	}
	if m["status"] != "ok" || m["namespace"] != "demo" {
		t.Fatalf("unexpected health response: %+v", m)
	}
}

func TestDispatchMask(t *testing.T) {
	fs := &fakePrivacyServer{
		MaskFunc: func(_ context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
			if req.FieldName != "email" || req.Value != "alice@example.com" {
				t.Fatalf("unexpected mask request: %+v", req)
			}
			return &pb.MaskResponse{Result: "***@example.com"}, nil
		},
	}
	client, cleanup := startBufconnServer(t, fs)
	defer cleanup()

	body := json.RawMessage(`{"field_name":"email","value":"alice@example.com"}`)
	resp, err := New().Dispatch(context.Background(), client.Raw(), "/v1/privacy/mask", body)
	if err != nil {
		t.Fatalf("Dispatch mask failed: %v", err)
	}
	m, ok := resp.(map[string]string)
	if !ok || m["result"] != "***@example.com" {
		t.Fatalf("unexpected mask response: %+v", resp)
	}
}

func TestDispatchDPCount(t *testing.T) {
	fs := &fakePrivacyServer{
		DPCountFunc: func(_ context.Context, req *pb.DPRequest) (*pb.DPResponse, error) {
			if len(req.Values) != 5 {
				t.Fatalf("unexpected dp values: %+v", req.Values)
			}
			return &pb.DPResponse{Result: 5.0}, nil
		},
	}
	client, cleanup := startBufconnServer(t, fs)
	defer cleanup()

	body := json.RawMessage(`{"values":[1.0,2.0,3.0,4.0,5.0],"epsilon":0.1,"mechanism":"laplace"}`)
	resp, err := New().Dispatch(context.Background(), client.Raw(), "/v1/privacy/dp/count", body)
	if err != nil {
		t.Fatalf("Dispatch dp/count failed: %v", err)
	}
	m, ok := resp.(map[string]float64)
	if !ok || m["result"] != 5.0 {
		t.Fatalf("unexpected dp count response: %+v", resp)
	}
}

func TestDispatchKAnonymizeRecord(t *testing.T) {
	fs := &fakePrivacyServer{
		KAnonymizeRecordFunc: func(_ context.Context, req *pb.KAnonymizeRequest) (*pb.KAnonymizeResponse, error) {
			if req.Record["age"] != "30" {
				t.Fatalf("unexpected record: %+v", req.Record)
			}
			return &pb.KAnonymizeResponse{Result: map[string]string{"age": "30-40", "zip": "100***"}}, nil
		},
	}
	client, cleanup := startBufconnServer(t, fs)
	defer cleanup()

	body := json.RawMessage(`{"record":{"age":"30","zip":"100000","gender":"F"},"qi_cols":["age","zip","gender"],"k":2}`)
	resp, err := New().Dispatch(context.Background(), client.Raw(), "/v1/privacy/k_anonymize/record", body)
	if err != nil {
		t.Fatalf("Dispatch k_anonymize/record failed: %v", err)
	}
	m, ok := resp.(map[string]map[string]string)
	if !ok || m["result"]["age"] != "30-40" {
		t.Fatalf("unexpected k-anonymize response: %+v", resp)
	}
}

func TestDispatchObfuscateQuery(t *testing.T) {
	fs := &fakePrivacyServer{
		ObfuscateQueryFunc: func(_ context.Context, req *pb.ObfuscateQueryRequest) (*pb.ObfuscateQueryResponse, error) {
			if req.Query != "糖尿病患者用药推荐" {
				t.Fatalf("unexpected query: %s", req.Query)
			}
			return &pb.ObfuscateQueryResponse{Result: []string{"糖尿病患者用药推荐", "高血压患者用药推荐", "高血脂患者用药推荐"}}, nil
		},
	}
	client, cleanup := startBufconnServer(t, fs)
	defer cleanup()

	body := json.RawMessage(`{"query":"糖尿病患者用药推荐","num_dummies":3,"domain":"medical"}`)
	resp, err := New().Dispatch(context.Background(), client.Raw(), "/v1/privacy/qol/obfuscate", body)
	if err != nil {
		t.Fatalf("Dispatch qol/obfuscate failed: %v", err)
	}
	m, ok := resp.(map[string][]string)
	if !ok || len(m["result"]) != 3 {
		t.Fatalf("unexpected obfuscate response: %+v", resp)
	}
}

func TestDispatchDynEval(t *testing.T) {
	fs := &fakePrivacyServer{
		DynEvalFunc: func(_ context.Context, req *pb.DynEvalRequest) (*pb.DynEvalResponse, error) {
			if req.FieldName != "phone" {
				t.Fatalf("unexpected field_name: %s", req.FieldName)
			}
			return &pb.DynEvalResponse{
				Field:      req.FieldName,
				Value:      req.Value,
				Level:      "S3",
				LevelId:    "L3",
				Category:   "PHONE",
				Confidence: 0.95,
				MatchedBy:  "rule_phone",
				ResultJson: `{"field":"phone","level":"S3","category":"PHONE"}`,
			}, nil
		},
	}
	client, cleanup := startBufconnServer(t, fs)
	defer cleanup()

	body := json.RawMessage(`{"field_name":"phone","value":"13800138000","domain":"telecom"}`)
	resp, err := New().Dispatch(context.Background(), client.Raw(), "/v1/dynclassification/eval", body)
	if err != nil {
		t.Fatalf("Dispatch dynclassification/eval failed: %v", err)
	}
	m, ok := resp.(map[string]any)
	if !ok || m["field"] != "phone" {
		t.Fatalf("unexpected dyn eval response: %+v", resp)
	}
}

func TestDispatchDynStandards(t *testing.T) {
	fs := &fakePrivacyServer{
		DynStandardsFunc: func(_ context.Context, _ *pb.DynStandardsRequest) (*pb.DynStandardsResponse, error) {
			return &pb.DynStandardsResponse{
				Standards:   []string{"financial", "telecom"},
				DetailsJson: `{"standards":["financial","telecom"],"count":2}`,
			}, nil
		},
	}
	client, cleanup := startBufconnServer(t, fs)
	defer cleanup()

	resp, err := New().Dispatch(context.Background(), client.Raw(), "/v1/dynclassification/standards", nil)
	if err != nil {
		t.Fatalf("Dispatch dynclassification/standards failed: %v", err)
	}
	m, ok := resp.(map[string]any)
	if !ok || m["count"] != float64(2) {
		t.Fatalf("unexpected dyn standards response: %+v", resp)
	}
}
