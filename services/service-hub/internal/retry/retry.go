// Package retry normalises pipeline errors into a bounded, persisted retry verdict.
// Package retry 把流水线各执行点拿到的 error 归一化为「有界分类 + 是否可重试」，
// 并把分类结果持久化到 tasks.error_class 列。
//
// ==============================================================================
// 【为什么要结构化（P2-7）】
// 改造前，后台重试扫描只能对 `task.Error` 这段**自由文案**做子串匹配
// （"timeout" / "connection refused" / ...）来判断是否重试，存在两类缺陷：
//  1. 文案改写或本地化 → 静默丧失重试能力，且没有任何报错提示；
//  2. 业务错误文案恰好含 "timeout" 字样 → 无意义重试，放大下游故障。
//
// 本包把判定挪回**错误产生点**：那里持有真正的 error，可依据 sentinel
// （pkg/agent 的 ErrTransport / ErrCircuitOpen / ErrEndpointUnavailable、
// context.DeadlineExceeded、net.Error、syscall 连接错误）用 errors.Is / errors.As
// 做类型判定；判定结果以有界枚举落库，后台扫描只读枚举，不再读文案。
//
// 【两类偏置】
//   - BiasConservative：调用方自身故障（契约/内部），未知即不重试，避免重试风暴；
//   - BiasDownstream：引擎与数据源出站调用点，未知故障按瞬时处理保持韧性
//     （下游返回 5xx 时常为无 sentinel 的普通包装错误）。
//
// ==============================================================================
package retry

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"

	pkgagent "github.com/fengzhizi319/PrivShield-go/pkg/agent"
)

// Bounded class vocabulary / 有界失败分类枚举。取值同时用于指标聚合与排障检索，
// 新增取值必须显式登记到 retryableClasses，避免出现「分类了却无人判定重试」的黑洞。
const (
	ClassTimeout    = "timeout"    // 超时：上下文截止、客户端超时、socket 超时
	ClassDownstream = "downstream" // 下游不可用：传输故障、熔断打开、无可用节点
	ClassShutdown   = "shutdown"   // 服务关停或调用方取消打断，任务并未真正失败
	ClassRecovered  = "recovered"  // 启动时回收的孤儿 running 任务（进程崩溃/重启遗留）
	ClassContract   = "contract"   // 契约级失败（如引擎未返回安全级别），重投不会改变结果
	ClassInternal   = "internal"   // 未结构化归类的内部故障（panic 恢复等），保守不重试

	// 存证阶段的取值由 internal/audit.FailureClass 产出，在此集中登记重试判定口径。
	ClassEvidence             = "evidence"              // 未预期的存证提交故障
	ClassEvidenceUnavailable  = "evidence_unavailable"  // audit-log 暂时不可用（5xx/网络）
	ClassEvidenceRejected     = "evidence_rejected"     // 存证侧契约拒绝
	ClassEvidenceUnconfigured = "evidence_unconfigured" // 未配置存证端点，重投不可能改变结果
	ClassEvidenceInvalid      = "evidence_invalid"      // 存证请求本身不合法
)

// retryableClasses 是唯一的重试判定表：后台重试扫描仅依据本表决定是否重投。
// 每个分类取值都必须显式登记判定（true/false），否则新增分类会静默落进「永不重试」。
var retryableClasses = map[string]bool{
	ClassTimeout:              true,
	ClassDownstream:           true,
	ClassShutdown:             true,
	ClassRecovered:            true,
	ClassContract:             false,
	ClassInternal:             false,
	ClassEvidence:             true,
	ClassEvidenceUnavailable:  true,
	ClassEvidenceRejected:     false,
	ClassEvidenceUnconfigured: false,
	ClassEvidenceInvalid:      false,
}

// Bias selects how an unrecognisable error is treated at a given failure site.
// Bias 决定失败点遇到「无法按类型识别的错误」时的默认口径。
type Bias int

const (
	// BiasConservative treats unknown errors as non-retryable (caller-side faults).
	BiasConservative Bias = iota
	// BiasDownstream treats unknown errors as transient (outbound calls to engine / datasource).
	BiasDownstream
)

// IsRetryableClass reports whether a persisted failure class permits another attempt.
// Empty and unknown classes are never retryable, so a legacy row cannot loop forever.
// IsRetryableClass 判定已落库的失败分类是否允许再次投递；空值与未知值一律不重试。
func IsRetryableClass(class string) bool {
	return retryableClasses[class]
}

// Classify maps an in-hand error to a bounded class and a retryability verdict,
// purely through error identity (errors.Is / errors.As) — never message matching.
// Classify 依据错误类型把 error 归一化为有界分类与可重试判定，绝不匹配错误文案：
// 一段写着 "connection refused" 的普通描述串不会被误判为可重试。
func Classify(err error, bias Bias) (class string, retryable bool) {
	if err == nil {
		return ClassInternal, false
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, os.ErrDeadlineExceeded),
		errors.Is(err, syscall.ETIMEDOUT):
		return ClassTimeout, true
	case errors.Is(err, context.Canceled):
		// 取消来自本进程关停或调用方主动放弃，不是下游瞬时故障。
		return ClassShutdown, true
	case errors.Is(err, pkgagent.ErrTransport),
		errors.Is(err, pkgagent.ErrCircuitOpen),
		errors.Is(err, pkgagent.ErrEndpointUnavailable):
		return ClassDownstream, true
	case errors.Is(err, syscall.ECONNREFUSED),
		errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, syscall.EHOSTUNREACH),
		errors.Is(err, syscall.ENETUNREACH),
		errors.Is(err, syscall.EPIPE):
		return ClassDownstream, true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return ClassTimeout, true
		}
		return ClassDownstream, true
	}

	if bias == BiasDownstream {
		return ClassDownstream, true
	}
	return ClassInternal, false
}
