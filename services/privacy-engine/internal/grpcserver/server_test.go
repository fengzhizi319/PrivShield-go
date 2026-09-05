package grpcserver

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/fengzhizi319/PrivShield-go/engine-go/internal/grpcserver/proto"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/service"
)

// newTestServer 创建测试用 gRPC 服务端
func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := service.DefaultConfig()
	svc, err := service.NewPrivacyService(cfg)
	if err != nil {
		t.Fatalf("NewPrivacyService: %v", err)
	}
	return NewServer(svc)
}

func TestHandleHealth(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.Health(context.Background(), &pb.HealthRequest{})
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}
	if resp.Namespace != "default" {
		t.Errorf("namespace = %q, want %q", resp.Namespace, "default")
	}
}

func TestHandleMask(t *testing.T) {
	srv := newTestServer(t)

	tests := []struct {
		name      string
		fieldName string
		value     string
		wantEmpty bool
	}{
		{"id_card", "id_card_no", "110101199003072345", false},
		{"phone", "phone", "13812345678", false},
		{"name", "patient_name", "张三", false},
		{"email", "email", "test@example.com", false},
		{"unknown_field", "foo", "bar", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := srv.Mask(context.Background(), &pb.MaskRequest{
				FieldName: tt.fieldName,
				Value:     tt.value,
			})
			if tt.name == "unknown_field" {
				// 脱敏失败现在返回错误而非原文（P0 安全修复）
				if err == nil {
					t.Fatalf("expected error for unknown field type, got resp=%v", resp)
				}
				return
			}
			if err != nil {
				t.Fatalf("Mask: %v", err)
			}

			result := resp.Result
			if tt.wantEmpty && result != "" {
				t.Errorf("result = %q, want empty", result)
			}
			if !tt.wantEmpty && result == "" {
				t.Errorf("result is empty, want non-empty")
			}
			if tt.name != "unknown_field" && result == tt.value {
				t.Errorf("result = %q, should be masked (original: %q)", result, tt.value)
			}
		})
	}
}

func TestHandleMaskRecord(t *testing.T) {
	srv := newTestServer(t)

	rec := map[string]string{
		"id_card_no":   "110101199003072345",
		"phone":        "13812345678",
		"patient_name": "张三",
		"diagnosis":    "感冒",
	}

	resp, err := srv.MaskRecord(context.Background(), &pb.MaskRecordRequest{
		Record: rec,
	})
	if err != nil {
		t.Fatalf("MaskRecord: %v", err)
	}

	result := resp.Result
	if result["id_card_no"] == "110101199003072345" {
		t.Errorf("id_card_no not masked: %v", result["id_card_no"])
	}
	if result["phone"] == "13812345678" {
		t.Errorf("phone not masked: %v", result["phone"])
	}
	if result["patient_name"] == "张三" {
		t.Errorf("patient_name not masked: %v", result["patient_name"])
	}
}

func TestHandleMaskBatch(t *testing.T) {
	srv := newTestServer(t)

	fieldNames := []string{"phone", "email"}
	values := []string{"13812345678", "test@example.com"}

	resp, err := srv.MaskBatch(context.Background(), &pb.MaskBatchRequest{
		FieldNames: fieldNames,
		Values:     values,
	})
	if err != nil {
		t.Fatalf("MaskBatch: %v", err)
	}

	if len(resp.Results) != 2 {
		t.Fatalf("results len = %d, want 2", len(resp.Results))
	}
	if resp.Results[0] == "13812345678" {
		t.Errorf("phone not masked: %v", resp.Results[0])
	}
}

func TestHandleHash(t *testing.T) {
	srv := newTestServer(t)

	resp, err := srv.Hash(context.Background(), &pb.HashRequest{
		Value: "test-data",
		Salt:  "test-salt",
	})
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if resp.Result == "" {
		t.Errorf("Hash result is empty")
	}
}

func TestHandleDP(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()

	// 1. DPCount（ε=2.0，true=20，Laplace scale=0.5，负值概率 < 10^-17）
	respCount, err := srv.DPCount(ctx, &pb.DPRequest{
		Values:  make([]float64, 20),
		Epsilon: 2.0,
	})
	if err != nil {
		t.Fatalf("DPCount: %v", err)
	}
	if respCount.Result <= 0 {
		t.Errorf("DPCount result = %v, want > 0", respCount.Result)
	}

	// 2. DPSum
	respSum, err := srv.DPSum(ctx, &pb.DPRequest{
		Values:  []float64{10, 20, 30},
		Epsilon: 1.0,
	})
	if err != nil {
		t.Fatalf("DPSum: %v", err)
	}
	if respSum.Result <= 0 {
		t.Errorf("DPSum result = %v, want > 0", respSum.Result)
	}

	// 3. DPMean
	respMean, err := srv.DPMean(ctx, &pb.DPRequest{
		Values:    []float64{10, 20, 30},
		Epsilon:   1.0,
		ClipUpper: 100,
	})
	if err != nil {
		t.Fatalf("DPMean: %v", err)
	}
	if respMean.Result <= 0 {
		t.Errorf("DPMean result = %v, want > 0", respMean.Result)
	}

	// 4. DPNoisyCount
	respNoisyCount, err := srv.DPNoisyCount(ctx, &pb.DPNoisyCountRequest{
		TrueCount: 100,
		Epsilon:   1.0,
	})
	if err != nil {
		t.Fatalf("DPNoisyCount: %v", err)
	}
	if respNoisyCount.Result <= 0 {
		t.Errorf("DPNoisyCount result = %v, want > 0", respNoisyCount.Result)
	}

	// 5. DPNoisySum
	respNoisySum, err := srv.DPNoisySum(ctx, &pb.DPNoisySumRequest{
		TrueSum:     500.0,
		Epsilon:     1.0,
		Sensitivity: 1.0,
	})
	if err != nil {
		t.Fatalf("DPNoisySum: %v", err)
	}
	if respNoisySum.Result <= 0 {
		t.Errorf("DPNoisySum result = %v, want > 0", respNoisySum.Result)
	}

	// 6. DPNoisyMean
	respNoisyMean, err := srv.DPNoisyMean(ctx, &pb.DPNoisyMeanRequest{
		TrueSum:   200.0,
		TrueCount: 10.0,
		Epsilon:   1.0,
		ClipUpper: 50.0,
	})
	if err != nil {
		t.Fatalf("DPNoisyMean: %v", err)
	}
	if respNoisyMean.Result <= 0 {
		t.Errorf("DPNoisyMean result = %v, want > 0", respNoisyMean.Result)
	}
}

func TestHandleObfuscateQuery(t *testing.T) {
	srv := newTestServer(t)

	resp, err := srv.ObfuscateQuery(context.Background(), &pb.ObfuscateQueryRequest{
		Query:      "肺癌早期症状",
		NumDummies: 4,
		Domain:     "medical",
	})
	if err != nil {
		t.Fatalf("ObfuscateQuery: %v", err)
	}

	if len(resp.Result) != 5 {
		t.Errorf("queries len = %d, want 5", len(resp.Result))
	}
}

func TestHandleKAnonymizeRecord(t *testing.T) {
	srv := newTestServer(t)

	resp, err := srv.KAnonymizeRecord(context.Background(), &pb.KAnonymizeRequest{
		Record: map[string]string{
			"patient_name": "张三",
			"phone":        "13812345678",
		},
	})
	if err != nil {
		t.Fatalf("KAnonymizeRecord: %v", err)
	}

	if resp.Result["patient_name"] == "张三" {
		t.Errorf("patient_name not anonymized: %v", resp.Result["patient_name"])
	}
}

func TestServerReflection(t *testing.T) {
	srv := newTestServer(t)
	lis := bufconn.Listen(1024 * 1024)
	go func() {
		_ = srv.Serve(lis)
	}()
	defer srv.Stop()

	ctx := context.Background()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	refClient := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := refClient.ServerReflectionInfo(ctx)
	if err != nil {
		t.Fatalf("ServerReflectionInfo: %v", err)
	}
	err = stream.Send(&grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{
			ListServices: "*",
		},
	})
	if err != nil {
		t.Fatalf("send reflection request: %v", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv reflection response: %v", err)
	}
	listResp := resp.GetListServicesResponse()
	if listResp == nil || len(listResp.Service) == 0 {
		t.Fatalf("expected services listed, got: %+v", resp)
	}
	foundPrivacy := false
	for _, s := range listResp.Service {
		if s.Name == "privacy.local.PrivacyService" {
			foundPrivacy = true
			break
		}
	}
	if !foundPrivacy {
		t.Fatalf("privacy.local.PrivacyService not found in reflection list: %+v", listResp.Service)
	}
}
