// Package circuitbreaker provides a shared three-state circuit breaker primitive.
// Package circuitbreaker 提供共享的三态熔断器原语（Closed → Open → HalfOpen），
// 供 pkg/gateway（反向代理负载均衡）与 pkg/agent（上游 HTTP 客户端）统一使用，
// 消除原先两处独立实现的重复代码与语义漂移风险。
package circuitbreaker

import (
	"sync"
	"time"
)

// State 枚举熔断器的三种标准生命周期状态。
type State int

const (
	// StateClosed 正常运行状态：所有请求正常下发。
	StateClosed State = iota
	// StateOpen 熔断开启状态：请求在客户端被快速拒绝。
	StateOpen
	// StateHalfOpen 半开探测状态：允许放行有限探测请求试探上游健康状态。
	StateHalfOpen
)

// String 返回熔断器状态的可读字符串描述。
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Breaker 是一个并发安全的三态熔断器实例。
// 状态转移规则：
//   - Closed → Open：连续失败达到 threshold 时触发；
//   - Open → HalfOpen：冷却期（cooldown）过后自动转入；
//   - HalfOpen → Closed：探测成功次数达到 halfOpenMax 后恢复；
//   - HalfOpen → Open：任何一次探测失败立即回退。
type Breaker struct {
	state          State         // 当前熔断状态（Closed / Open / HalfOpen）
	failures       int           // 连续失败计数（Closed 状态下累计，达到 threshold 触发熔断）
	successes      int           // 探测成功计数（HalfOpen 状态下累计，达到 halfOpenMax 恢复 Closed）
	inFlightProbes int           // HalfOpen 状态下当前正在执行的在途探测请求数
	threshold      int           // 触发熔断的连续失败次数阈值
	halfOpenMax    int           // HalfOpen 状态下允许放行的最大探测请求数
	openedAt       time.Time     // 熔断开启时间戳（用于计算冷却期是否已过）
	cooldown       time.Duration // Open 状态最短持续时间（冷却期）
	mu             sync.Mutex    // 保护所有状态字段的互斥锁
}

// NewBreaker 创建指定失败阈值与冷却时间的熔断器。
// threshold 为触发熔断的连续失败次数；cooldown 为 Open 状态最短持续时间。
func NewBreaker(threshold int, cooldown time.Duration) *Breaker {
	if threshold <= 0 {
		threshold = 5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &Breaker{
		state:       StateClosed,
		threshold:   threshold,
		halfOpenMax: 3,
		cooldown:    cooldown,
	}
}

// Allow 判定当前是否允许请求通过。
//
// 状态转移逻辑：
//   - StateClosed：始终放行；
//   - StateOpen：冷却期已过则转为 StateHalfOpen 并放行首个探测请求，否则拒绝；
//   - StateHalfOpen：严格限制在途并发探测数与成功数之和不超过 halfOpenMax，防并发击穿。
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(b.openedAt) >= b.cooldown {
			b.state = StateHalfOpen
			b.successes = 0
			b.inFlightProbes = 1
			return true
		}
		return false
	case StateHalfOpen:
		if b.successes+b.inFlightProbes < b.halfOpenMax {
			b.inFlightProbes++
			return true
		}
		return false
	}
	return true
}

// RecordSuccess 记录一次成功调用。
// HalfOpen 状态下累计成功次数达到 halfOpenMax 后恢复 Closed。
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateHalfOpen:
		if b.inFlightProbes > 0 {
			b.inFlightProbes--
		}
		b.successes++
		if b.successes >= b.halfOpenMax {
			b.state = StateClosed
			b.failures = 0
			b.inFlightProbes = 0
		}
	case StateClosed:
		b.failures = 0
	}
}

// RecordFailure 记录一次失败调用。
// Closed 状态下连续失败达到 threshold 时转入 Open；HalfOpen 状态下任何失败立即回退 Open。
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures++
	b.openedAt = time.Now()

	switch b.state {
	case StateClosed:
		if b.failures >= b.threshold {
			b.state = StateOpen
		}
	case StateHalfOpen:
		b.state = StateOpen
		b.inFlightProbes = 0
	}
}

// State 返回当前熔断器状态（读锁安全）。
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// StateString 返回当前状态的可读字符串。
func (b *Breaker) StateString() string {
	return b.State().String()
}
