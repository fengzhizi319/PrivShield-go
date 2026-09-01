package retry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"

	pkgagent "github.com/fengzhizi319/PrivShield-go/pkg/agent"
)

// fakeNetError 是可控的 net.Error 实现，用于验证按类型（而非文案）判定超时。
type fakeNetError struct {
	timeout bool
}

func (fakeNetError) Error() string   { return "i/o error" }
func (e fakeNetError) Timeout() bool { return e.timeout }
func (e fakeNetError) Temporary() bool {
	return true
}

func TestClassifyIsDrivenByErrorIdentity(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		bias      Bias
		wantClass string
		wantRetry bool
	}{
		{
			name:      "context deadline",
			err:       fmt.Errorf("classify stage: %w", context.DeadlineExceeded),
			wantClass: ClassTimeout,
			wantRetry: true,
		},
		{
			name:      "os deadline exceeded",
			err:       os.ErrDeadlineExceeded,
			wantClass: ClassTimeout,
			wantRetry: true,
		},
		{
			name:      "syscall etimedout",
			err:       fmt.Errorf("read: %w", syscall.ETIMEDOUT),
			wantClass: ClassTimeout,
			wantRetry: true,
		},
		{
			name:      "net timeout by type",
			err:       fakeNetError{timeout: true},
			wantClass: ClassTimeout,
			wantRetry: true,
		},
		{
			name:      "net error non timeout",
			err:       fakeNetError{},
			wantClass: ClassDownstream,
			wantRetry: true,
		},
		{
			name:      "agent transport",
			err:       fmt.Errorf("process agent: %w", pkgagent.ErrTransport),
			wantClass: ClassDownstream,
			wantRetry: true,
		},
		{
			name:      "circuit open",
			err:       fmt.Errorf("process agent: %w", pkgagent.ErrCircuitOpen),
			wantClass: ClassDownstream,
			wantRetry: true,
		},
		{
			name:      "no endpoint",
			err:       pkgagent.ErrEndpointUnavailable,
			wantClass: ClassDownstream,
			wantRetry: true,
		},
		{
			name:      "connection refused by errno",
			err:       fmt.Errorf("dial: %w", syscall.ECONNREFUSED),
			wantClass: ClassDownstream,
			wantRetry: true,
		},
		{
			name:      "cancellation is shutdown not downstream",
			err:       context.Canceled,
			wantClass: ClassShutdown,
			wantRetry: true,
		},
		{
			// 关键回归点：仅仅「文案像网络故障」不得换来重试资格。
			name:      "lookalike text stays internal",
			err:       errors.New("validation failed: timeout field must be an integer"),
			wantClass: ClassInternal,
			wantRetry: false,
		},
		{
			name:      "unknown error at downstream site stays transient",
			err:       errors.New("agent returned status 503: draining"),
			bias:      BiasDownstream,
			wantClass: ClassDownstream,
			wantRetry: true,
		},
		{
			name:      "unknown error at caller site is not retried",
			err:       errors.New("agent returned status 503: draining"),
			wantClass: ClassInternal,
			wantRetry: false,
		},
		{
			name:      "nil error",
			err:       nil,
			bias:      BiasDownstream,
			wantClass: ClassInternal,
			wantRetry: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, ok := Classify(tc.err, tc.bias)
			if class != tc.wantClass || ok != tc.wantRetry {
				t.Fatalf("Classify() = (%q, %v), want (%q, %v)", class, ok, tc.wantClass, tc.wantRetry)
			}
		})
	}
}

// TestEveryDeclaredClassIsDeliberatelyRegistered 防止新增分类取值后忘记登记重试口径，
// 造成「分类了但永远不重试」的黑洞：每个取值必须显式出现在判定表里。
func TestEveryDeclaredClassIsDeliberatelyRegistered(t *testing.T) {
	all := []string{
		ClassTimeout, ClassDownstream, ClassShutdown, ClassRecovered,
		ClassContract, ClassInternal,
		ClassEvidence, ClassEvidenceUnavailable, ClassEvidenceRejected,
		ClassEvidenceUnconfigured, ClassEvidenceInvalid,
	}
	seen := make(map[string]bool, len(all))
	for _, class := range all {
		if seen[class] {
			t.Fatalf("duplicate class value %q", class)
		}
		seen[class] = true
		if _, ok := retryableClasses[class]; !ok {
			t.Errorf("class %q is not registered in the retry verdict table", class)
		}
	}
}

// TestClassifyVerdictAgreesWithRegistry 保证 Classify 返回「可重试」时，
// 该分类在判定表中同样判可重试：错误产生点的内存判定与后台扫描的持久化判定不会分叉。
func TestClassifyVerdictAgreesWithRegistry(t *testing.T) {
	for _, err := range []error{
		context.DeadlineExceeded,
		syscall.ECONNREFUSED,
		fmt.Errorf("wrapped: %w", pkgagent.ErrTransport),
		fakeNetError{timeout: true},
		context.Canceled,
		errors.New("opaque"),
	} {
		for _, bias := range []Bias{BiasConservative, BiasDownstream} {
			class, ok := Classify(err, bias)
			if registry := IsRetryableClass(class); ok && !registry {
				t.Errorf("Classify(%v, %d) says retryable but registry says no for class %q", err, bias, class)
			}
		}
	}
}

func TestIsRetryableClassRejectsUnknownAndEmpty(t *testing.T) {
	for _, class := range []string{"", "timeout-ish", "classification", "evidence_rejected", "unknown"} {
		if IsRetryableClass(class) {
			t.Errorf("class %q must not be retryable", class)
		}
	}
}

// TestTimeoutIdentitySurvivesClientWrapping 复现引擎客户端的真实包装链
// （fmt.Errorf %w → *net.OpError → context.DeadlineExceeded），判定必须命中超时。
func TestTimeoutIdentitySurvivesClientWrapping(t *testing.T) {
	opErr := &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
	class, ok := Classify(fmt.Errorf("agent request failed: %w", opErr), BiasConservative)
	if class != ClassTimeout || !ok {
		t.Fatalf("expected (timeout, true) for wrapped net.OpError, got (%q, %v)", class, ok)
	}
}
