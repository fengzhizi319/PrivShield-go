package service

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// streamCSVCases 覆盖流式与物化两条路径必须保持语义一致的输入形态。
var streamCSVCases = []struct {
	name    string
	content string
	options map[string]interface{}
}{
	{"plain", "phone,name,remark\n13812345678,张三,a\n13987654321,李四,b\n", nil},
	{"bom", "\ufeffphone,name\n13812345678,张三\n", nil},
	{"column-filter", "phone,name\n13812345678,张三\n", map[string]interface{}{"columns": []interface{}{"phone"}}},
	{"quoted-comma", "phone,note\n13812345678,\"a,b\"\n", nil},
	{"empty-body", "phone,name\n", nil},
	{"empty-file", "", nil},
}

// TestProcessFileStreamCSVMatchesProcessFile 校验 CSV 流式路径与物化路径输出完全等价。
func TestProcessFileStreamCSVMatchesProcessFile(t *testing.T) {
	svc := newTestService(t)

	for _, tc := range streamCSVCases {
		t.Run(tc.name, func(t *testing.T) {
			want, wantErr := svc.ProcessFile([]byte(tc.content), "data.csv", "mask_dataframe", tc.options)
			got, gotErr := svc.ProcessFileStream(strings.NewReader(tc.content), "data.csv", "mask_dataframe", tc.options)

			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("error mismatch: ProcessFile=%v ProcessFileStream=%v", wantErr, gotErr)
			}
			if wantErr != nil {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("result mismatch\n stream = %v\n file   = %v", got, want)
			}
		})
	}
}

// TestProcessFileStreamJSONMatchesProcessFile 校验 JSON 流式路径与物化路径输出完全等价。
func TestProcessFileStreamJSONMatchesProcessFile(t *testing.T) {
	svc := newTestService(t)

	cases := []struct {
		name    string
		content string
		options map[string]interface{}
	}{
		{"plain", `[{"phone":"13812345678","age":30},{"phone":"13987654321","age":41}]`, nil},
		{"column-filter", `[{"phone":"13812345678","name":"张三"}]`, map[string]interface{}{"columns": []string{"phone"}}},
		{"nested-value", `[{"phone":"13812345678","ext":{"k":"v"},"tags":["a","b"]}]`, nil},
		{"empty-array", `[]`, nil},
		{"empty-file", ``, nil},
		{"top-level-object", `{"phone":"13812345678"}`, nil},
		{"non-object-element", `[1,2]`, nil},
		{"trailing-data", `[{"phone":"13812345678"}] {}`, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, wantErr := svc.ProcessFile([]byte(tc.content), "data.json", "mask_dataframe", tc.options)
			got, gotErr := svc.ProcessFileStream(strings.NewReader(tc.content), "data.json", "mask_dataframe", tc.options)

			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("error mismatch: ProcessFile=%v ProcessFileStream=%v", wantErr, gotErr)
			}
			if wantErr != nil {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("result mismatch\n stream = %v\n file   = %v", got, want)
			}
		})
	}
}

// TestProcessFileStreamFallbackMatchesProcessFile 校验 k_anonymize / xlsx 等
// 非流式组合经 ProcessFileStream 回退后与直接调用 ProcessFile 行为一致。
func TestProcessFileStreamFallbackMatchesProcessFile(t *testing.T) {
	svc := newTestService(t)

	content := "age,gender,zip\n30,M,100000\n31,F,100001\n30,M,100002\n31,F,100003\n"
	opts := map[string]interface{}{"qi_cols": []interface{}{"age", "gender"}}

	want, wantErr := svc.ProcessFile([]byte(content), "data.csv", "k_anonymize", opts)
	got, gotErr := svc.ProcessFileStream(strings.NewReader(content), "data.csv", "k_anonymize", opts)
	if (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("fallback error mismatch: ProcessFile=%v ProcessFileStream=%v", wantErr, gotErr)
	}
	if wantErr == nil && !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback result mismatch\n stream = %v\n file   = %v", got, want)
	}

	// 不支持的类型在两条路径上均应拒绝
	if _, err := svc.ProcessFileStream(strings.NewReader(content), "data.txt", "mask_dataframe", nil); err == nil {
		t.Fatal("unsupported file type should be rejected")
	}
}

// TestProcessFileStreamEnforcesSizeLimit 校验流式路径同样受字节硬上限保护。
func TestProcessFileStreamEnforcesSizeLimit(t *testing.T) {
	old := maxProcessFileBytes
	maxProcessFileBytes = 48
	defer func() { maxProcessFileBytes = old }()

	svc := newTestService(t)
	csvContent := "phone,remark\n" + strings.Repeat("13812345678,0123456789\n", 20)
	jsonContent := "[" + strings.Repeat(`{"phone":"13812345678"},`, 20) + `{"phone":"13812345678"}]`

	tests := []struct {
		name      string
		content   string
		filename  string
		operation string
	}{
		{"csv-stream", csvContent, "big.csv", "mask_dataframe"},
		{"json-stream", jsonContent, "big.json", "mask_dataframe"},
		{"materialized-fallback", csvContent, "big.csv", "k_anonymize"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.ProcessFileStream(strings.NewReader(tc.content), tc.filename, tc.operation, nil)
			if !errors.Is(err, ErrFileTooLarge) {
				t.Fatalf("err = %v, want ErrFileTooLarge", err)
			}
		})
	}
}

// TestProcessFileStreamReducesAllocation 校验流式路径确实消除了全量物化带来的内存放大。
// 使用裸 PrivacyService（无后台热重载 goroutine）避免 TotalAlloc 受干扰。
// 阈值取绝对量而非比例：-race 下检测器自身会注入数百 MB 的阴影内存，使比例失真。
func TestProcessFileStreamReducesAllocation(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("phone,name,remark\n")
	for i := 0; i < 40000; i++ {
		sb.WriteString("13812345678,张三,13800138000\n")
	}
	content := sb.String()

	svc := &PrivacyService{}

	allocOf := func(fn func()) uint64 {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		fn()
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}

	materialized := allocOf(func() {
		if _, err := svc.ProcessFile([]byte(content), "data.csv", "mask_dataframe", nil); err != nil {
			t.Fatalf("ProcessFile failed: %v", err)
		}
	})
	streamed := allocOf(func() {
		if _, err := svc.ProcessFileStream(strings.NewReader(content), "data.csv", "mask_dataframe", nil); err != nil {
			t.Fatalf("ProcessFileStream failed: %v", err)
		}
	})

	if streamed >= materialized {
		t.Fatalf("streaming path did not reduce allocations: streamed=%d materialized=%d", streamed, materialized)
	}
	// 物化路径额外驻留：原始快照 + 全量 [][]string + 未脱敏 records 副本，至少数倍于文件体积
	const minSaving = 4 << 20
	if materialized-streamed < minSaving {
		t.Fatalf("streaming path saved only %d bytes, want >= %d", materialized-streamed, minSaving)
	}
	t.Logf("TotalAlloc: streamed=%d materialized=%d saving=%d", streamed, materialized, materialized-streamed)
}

// TestForEachChunkedCoversEveryIndexOnce 校验分块并行的区间不重不漏。
func TestForEachChunkedCoversEveryIndexOnce(t *testing.T) {
	const n = streamParallelMinRows * 3
	if runtime.NumCPU() < 2 {
		t.Skip("needs multiple CPUs to exercise the chunked path")
	}

	hits := make([]int64, n)
	var total int64
	forEachChunked(n, func(start, end int) {
		for i := start; i < end; i++ {
			atomic.AddInt64(&hits[i], 1)
			atomic.AddInt64(&total, 1)
		}
	})
	if total != n {
		t.Fatalf("processed %d indices, want %d", total, n)
	}
	for i, h := range hits {
		if h != 1 {
			t.Fatalf("index %d processed %d times, want exactly 1", i, h)
		}
	}
}

// BenchmarkProcessFileStream 对比流式与物化路径的吞吐与内存（-benchmem 观察 B/op）。
func BenchmarkProcessFileStream(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("phone,name,remark\n")
	for i := 0; i < 5000; i++ {
		sb.WriteString("13812345678,张三,0123456789\n")
	}
	content := []byte(sb.String())
	svc := &PrivacyService{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.ProcessFileStream(strings.NewReader(string(content)), "data.csv", "mask_dataframe", nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkProcessFileMaterialized 物化路径基线。
func BenchmarkProcessFileMaterialized(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("phone,name,remark\n")
	for i := 0; i < 5000; i++ {
		sb.WriteString("13812345678,张三,0123456789\n")
	}
	content := []byte(sb.String())
	svc := &PrivacyService{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.ProcessFile(content, "data.csv", "mask_dataframe", nil); err != nil {
			b.Fatal(err)
		}
	}
}
