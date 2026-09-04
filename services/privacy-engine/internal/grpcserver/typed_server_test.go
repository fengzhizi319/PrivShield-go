package grpcserver

import (
	"context"
	"testing"

	pb "github.com/fengzhizi319/PrivShield-go/engine-go/internal/grpcserver/proto"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/service"
)

func newTypedTestServer(t *testing.T) *TypedServer {
	t.Helper()
	cfg := service.DefaultConfig()
	svc, err := service.NewPrivacyService(cfg)
	if err != nil {
		t.Fatalf("NewPrivacyService: %v", err)
	}
	return NewTypedServer(svc)
}

func TestTyped_PerturbBinaryBatch(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.PerturbBinaryBatch(context.Background(), &pb.PerturbBinaryBatchRequest{
		Values:  []int32{0, 1, 1, 0, 1},
		Epsilon: 1.0,
	})
	if err != nil {
		t.Fatalf("PerturbBinaryBatch: %v", err)
	}
	if len(resp.GetResults()) != 5 {
		t.Errorf("result length = %d, want 5", len(resp.GetResults()))
	}
	for _, v := range resp.GetResults() {
		if v != 0 && v != 1 {
			t.Errorf("value = %d, should be 0 or 1", v)
		}
	}
}

func TestTyped_PerturbCategoricalBatch(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.PerturbCategoricalBatch(context.Background(), &pb.PerturbCategoricalBatchRequest{
		Values:     []string{"A", "B", "C"},
		Categories: []string{"A", "B", "C"},
		Epsilon:    1.0,
	})
	if err != nil {
		t.Fatalf("PerturbCategoricalBatch: %v", err)
	}
	if len(resp.GetResults()) != 3 {
		t.Errorf("result length = %d, want 3", len(resp.GetResults()))
	}
}

func TestTyped_EstimateBinaryFrequency(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.EstimateBinaryFrequency(context.Background(), &pb.EstimateBinaryFrequencyRequest{
		ReportedValues: []int32{1, 1, 0, 1, 0},
		Epsilon:        5.0,
	})
	if err != nil {
		t.Fatalf("EstimateBinaryFrequency: %v", err)
	}
	freq := resp.GetEstimatedFrequency()
	if freq < 0.0 || freq > 1.0 {
		t.Errorf("frequency = %f, should be in [0,1]", freq)
	}
}

func TestTyped_EstimateCategoricalHistogram(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.EstimateCategoricalHistogram(context.Background(), &pb.EstimateCategoricalHistogramRequest{
		ReportedValues: []string{"A", "B", "A", "C"},
		Categories:     []string{"A", "B", "C"},
		Epsilon:        5.0,
	})
	if err != nil {
		t.Fatalf("EstimateCategoricalHistogram: %v", err)
	}
	hist := resp.GetEstimatedHistogram()
	if len(hist) != 3 {
		t.Errorf("histogram categories = %d, want 3", len(hist))
	}
}

func TestTyped_ObfuscateQueryBatch(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.ObfuscateQueryBatch(context.Background(), &pb.ObfuscateQueryBatchRequest{
		Queries:    []string{"SELECT * FROM patients", "SELECT name FROM users"},
		NumDummies: 3,
		Domain:     "medical",
	})
	if err != nil {
		t.Fatalf("ObfuscateQueryBatch: %v", err)
	}
	if len(resp.GetResults()) != 2 {
		t.Errorf("results count = %d, want 2", len(resp.GetResults()))
	}
}

func TestTyped_DPHistogram(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.DPHistogram(context.Background(), &pb.DPHistogramRequest{
		Values:     []string{"A", "B", "A", "C", "A"},
		Categories: []string{"A", "B", "C"},
		Epsilon:    1.0,
	})
	if err != nil {
		t.Fatalf("DPHistogram: %v", err)
	}
	result := resp.GetResult()
	if len(result) != 3 {
		t.Errorf("histogram categories = %d, want 3", len(result))
	}
}

func TestTyped_DPNoisyHistogram(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.DPNoisyHistogram(context.Background(), &pb.DPNoisyHistogramRequest{
		TrueCounts: map[string]float64{"A": 100, "B": 200, "C": 50},
		Epsilon:    1.0,
	})
	if err != nil {
		t.Fatalf("DPNoisyHistogram: %v", err)
	}
	result := resp.GetResult()
	if len(result) != 3 {
		t.Errorf("histogram categories = %d, want 3", len(result))
	}
}

func TestTyped_DPChunkedCount(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.DPChunkedCount(context.Background(), &pb.DPChunkedCountRequest{
		Chunks: []*pb.DoubleChunk{
			{Values: []float64{1, 2, 3}},
			{Values: []float64{4, 5}},
		},
		Epsilon: 1.0,
	})
	if err != nil {
		t.Fatalf("DPChunkedCount: %v", err)
	}
	if resp.GetResult() < -50 || resp.GetResult() > 60 {
		t.Errorf("chunked count = %f, out of reasonable range", resp.GetResult())
	}
}

func TestTyped_DPChunkedSum(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.DPChunkedSum(context.Background(), &pb.DPChunkedSumRequest{
		Chunks: []*pb.DoubleChunk{
			{Values: []float64{10, 20, 30}},
			{Values: []float64{40, 50}},
		},
		Epsilon:   1.0,
		ClipLower: 0,
		ClipUpper: 100,
	})
	if err != nil {
		t.Fatalf("DPChunkedSum: %v", err)
	}
	// True sum = 150, sensitivity = 100, epsilon = 1.0 → Laplace(0,100) noise
	// Extremely wide range to avoid flaky: ±5000 covers >99.9% cases
	if resp.GetResult() < -5000 || resp.GetResult() > 5200 {
		t.Errorf("chunked sum = %f, out of reasonable range", resp.GetResult())
	}
}

func TestTyped_DPVectorSum(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.DPVectorSum(context.Background(), &pb.DPVectorSumRequest{
		Vectors: []*pb.DoubleChunk{
			{Values: []float64{1.0, 2.0}},
			{Values: []float64{3.0, 4.0}},
		},
		MaxNorm: 10.0,
		Epsilon: 1.0,
	})
	if err != nil {
		t.Fatalf("DPVectorSum: %v", err)
	}
	vec := resp.GetNoisyVector()
	if len(vec) != 2 {
		t.Errorf("vector dim = %d, want 2", len(vec))
	}
}

func TestTyped_DPVectorMean(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.DPVectorMean(context.Background(), &pb.DPVectorMeanRequest{
		Vectors: []*pb.DoubleChunk{
			{Values: []float64{1.0, 2.0}},
			{Values: []float64{3.0, 4.0}},
		},
		MaxNorm: 10.0,
		Epsilon: 1.0,
	})
	if err != nil {
		t.Fatalf("DPVectorMean: %v", err)
	}
	vec := resp.GetMeanVector()
	if len(vec) != 2 {
		t.Errorf("vector dim = %d, want 2", len(vec))
	}
}

func TestTyped_KAnonymizeTable(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.KAnonymizeTable(context.Background(), &pb.KAnonymizeTableRequest{
		Rows: []*pb.RecordEntry{
			{Fields: map[string]string{"phone": "13812345678", "name": "张三"}},
			{Fields: map[string]string{"phone": "13987654321", "name": "李四"}},
		},
		QiCols: []string{"phone", "name"},
		K:      2,
	})
	if err != nil {
		t.Fatalf("KAnonymizeTable: %v", err)
	}
	if len(resp.GetRows()) != 2 {
		t.Errorf("rows count = %d, want 2", len(resp.GetRows()))
	}
}

func TestTyped_MaskDataFrame(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.MaskDataFrame(context.Background(), &pb.MaskDataFrameRequest{
		Data: []*pb.RecordEntry{
			{Fields: map[string]string{"phone": "13812345678", "name": "张三"}},
		},
		Columns: []string{"phone", "name"},
	})
	if err != nil {
		t.Fatalf("MaskDataFrame: %v", err)
	}
	if len(resp.GetData()) != 1 {
		t.Errorf("data count = %d, want 1", len(resp.GetData()))
	}
	// 手机号应被脱敏
	row := resp.GetData()[0].GetFields()
	if row["phone"] == "13812345678" {
		t.Error("phone should be masked")
	}
}

func TestTyped_RecommendParams(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.RecommendParams(context.Background(), &pb.RecommendRequest{
		Namespace: "test",
	})
	if err != nil {
		t.Fatalf("RecommendParams: %v", err)
	}
	if resp.GetStatus() != "ok" {
		t.Errorf("status = %q, want %q", resp.GetStatus(), "ok")
	}
	if resp.GetNamespace() != "test" {
		t.Errorf("namespace = %q, want %q", resp.GetNamespace(), "test")
	}
	if resp.GetRecommendedParamsJson() == "" {
		t.Error("recommended_params_json should not be empty")
	}
}

func TestTyped_DPAggregate(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.DPAggregate(context.Background(), &pb.DPAggregateRequest{
		Rows: []*pb.RecordEntry{
			{Fields: map[string]string{"a": "1"}},
			{Fields: map[string]string{"a": "2"}},
		},
		SpecsJson: `{"a":"sum"}`,
		Epsilon:   1.0,
		ClipLower: 0,
		ClipUpper: 100.0,
	})
	if err != nil {
		t.Fatalf("DPAggregate: %v", err)
	}
	if resp.GetResultsJson() == "" {
		t.Error("results_json should not be empty")
	}
}

func TestTyped_DPAdaptiveClip(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.DPAdaptiveClip(context.Background(), &pb.DPAdaptiveClipRequest{
		Values:      []float64{1.0, 2.0, 3.0, 100.0},
		Epsilon:     1.0,
		InitialClip: 10.0,
	})
	if err != nil {
		t.Fatalf("DPAdaptiveClip: %v", err)
	}
	if resp.GetClipUpper() <= 0 {
		t.Error("clip_upper should be positive")
	}
}

func TestTyped_KAnonymizeDataFrame(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.KAnonymizeDataFrame(context.Background(), &pb.KAnonymizeDataFrameRequest{
		Data: []*pb.RecordEntry{
			{Fields: map[string]string{"phone": "13812345678", "name": "张三"}},
			{Fields: map[string]string{"phone": "13987654321", "name": "李四"}},
		},
		QiCols: []string{"phone", "name"},
		K:      2,
	})
	if err != nil {
		t.Fatalf("KAnonymizeDataFrame: %v", err)
	}
	if len(resp.GetData()) != 2 {
		t.Errorf("data count = %d, want 2", len(resp.GetData()))
	}
	// phone should be masked
	if resp.GetData()[0].GetFields()["phone"] == "13812345678" {
		t.Error("phone should be masked")
	}
}

func TestTyped_DPGroupBy(t *testing.T) {
	ts := newTypedTestServer(t)
	resp, err := ts.DPGroupBy(context.Background(), &pb.DPGroupByRequest{
		Rows: []*pb.RecordEntry{
			{Fields: map[string]string{"group": "A", "value": "10"}},
			{Fields: map[string]string{"group": "B", "value": "20"}},
			{Fields: map[string]string{"group": "A", "value": "30"}},
		},
		GroupCol:  "group",
		TargetCol: "value",
		Agg:       "count",
		Epsilon:   1.0,
	})
	if err != nil {
		t.Fatalf("DPGroupBy: %v", err)
	}
	if resp.GetResultJson() == "" {
		t.Error("result_json should not be empty")
	}
}
