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

// 缺省参数常量
const (
	DefaultThreshold     = 5
	DefaultCooldown      = 30 * time.Second
	DefaultHalfOpenMax   = 3
	DefaultBackoffFactor = 1.0
)

// Options 定义熔断器的配置选项。
type Options struct {
	// Threshold 触发熔断的连续失败次数阈值（<=0 时缺省为 DefaultThreshold 5）。
	Threshold int

	// Cooldown 熔断开启后的最短基础冷却时间（<=0 时缺省为 DefaultCooldown 30s）。
	Cooldown time.Duration

	// HalfOpenMax 半开探测状态下允许放行的最大并发探测请求数（<=0 时缺省为 DefaultHalfOpenMax 3）。
	HalfOpenMax int

	// MaxCooldown 启用自适应退避时的冷却期上限。若 <=0 且 BackoffFactor > 1.0，则默认上限为 Cooldown * 10（且不少于 5 分钟）。
	MaxCooldown time.Duration

	// BackoffFactor 连续探测失败重新熔断时的冷却期指数退避系数（例如 2.0 表示每次翻倍）。
	// 若 <= 1.0 则禁用退避，每次熔断冷却期恒定为 Cooldown。缺省为 1.0。
	BackoffFactor float64
}

// Breaker 是一个并发安全的三态熔断器实例。
// 状态转移规则：
//   - Closed → Open：连续失败达到 threshold 时触发，记录 openedAt 并设定初始冷却期；
//   - Open → HalfOpen：自 openedAt 经过当前冷却期（currentCooldown）后自动转入，放行首个探测请求；
//   - HalfOpen → Closed：探测成功次数达到 halfOpenMax 后完全恢复，连续失败计数清零，自适应冷却期重置为基础冷却期；
//   - HalfOpen → Open：任何一次探测失败立即回退至 Open 状态，若启用自适应退避则按系数递增冷却期，并更新 openedAt。
//
// 关键安全修复：
//   - openedAt 仅在发生进入 Open 状态的状态跃迁时更新（Closed→Open 与 HalfOpen→Open）；
//   - 在 StateOpen 期间因残余在途请求失败而调用 RecordFailure 时，绝不更新 openedAt，彻底消除饥饿（starvation）风险；
//   - 在 StateClosed 期间调用 RecordFailure 时，未达到阈值不更新 openedAt，保持语义纯正。
type Breaker struct {
	state           State         // 当前熔断状态（Closed / Open / HalfOpen）
	failures        int           // 连续失败计数（Closed 状态下累计，达到 threshold 触发熔断）
	successes       int           // 探测成功计数（HalfOpen 状态下累计，达到 halfOpenMax 恢复 Closed）
	inFlightProbes  int           // HalfOpen 状态下当前正在执行的在途探测请求数
	threshold       int           // 触发熔断的连续失败次数阈值
	halfOpenMax     int           // HalfOpen 状态下允许放行的最大探测请求数
	openedAt        time.Time     // 最近一次转入 Open 状态的时间戳（仅在状态跃迁至 Open 时更新）
	baseCooldown    time.Duration // 基础冷却时间
	currentCooldown time.Duration // 当前生效的冷却时间（退避时动态增长，恢复 Closed 后重置）
	maxCooldown     time.Duration // 退避冷却时间上限
	backoffFactor   float64       // 探测失败重回 Open 时的冷却退避系数
	mu              sync.Mutex    // 保护所有状态字段的互斥锁
}

// New 使用指定配置选项创建三态熔断器实例。
func New(opts Options) *Breaker {
	threshold := opts.Threshold
	if threshold <= 0 {
		threshold = DefaultThreshold
	}

	cooldown := opts.Cooldown
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}

	halfOpenMax := opts.HalfOpenMax
	if halfOpenMax <= 0 {
		halfOpenMax = DefaultHalfOpenMax
	}

	backoffFactor := opts.BackoffFactor
	if backoffFactor < 1.0 {
		backoffFactor = DefaultBackoffFactor
	}

	maxCooldown := opts.MaxCooldown
	if backoffFactor > 1.0 && maxCooldown <= 0 {
		maxCooldown = cooldown * 10
		if maxCooldown < 5*time.Minute {
			maxCooldown = 5 * time.Minute
		}
	}

	return &Breaker{
		state:           StateClosed,
		threshold:       threshold,
		halfOpenMax:     halfOpenMax,
		baseCooldown:    cooldown,
		currentCooldown: cooldown,
		maxCooldown:     maxCooldown,
		backoffFactor:   backoffFactor,
	}
}

// NewBreaker 是 New 的同义构造别名，使用指定配置选项创建熔断器。
func NewBreaker(opts Options) *Breaker {
	return New(opts)
}

// Allow 判定当前是否允许请求通过。
//
// 状态转移逻辑：
//   - StateClosed：始终放行；
//   - StateOpen：自 openedAt 经历当前冷却期后转为 StateHalfOpen 并放行首个探测请求，否则拒绝；
//   - StateHalfOpen：严格限制在途并发探测数与成功数之和不超过 halfOpenMax，防并发击穿。
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(b.openedAt) >= b.currentCooldown {
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
// HalfOpen 状态下累计成功次数达到 halfOpenMax 后恢复 Closed，并重置自适应冷却期。
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
			b.currentCooldown = b.baseCooldown
		}
	case StateClosed:
		b.failures = 0
	}
}

// RecordFailure 记录一次失败调用。
//
// 状态转移与冷却期安全：
//   - Closed 状态下连续失败达到 threshold 时转入 Open，记录 openedAt 并使用基础冷却期；
//   - HalfOpen 状态下任何失败立即回退 Open，重新记录 openedAt，若启用退避则成倍增加下一次冷却期；
//   - Open 状态下已处于熔断，因残余在途请求到达的失败绝不更新 openedAt，杜绝饥饿（starvation）。
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.failures++
		if b.failures >= b.threshold {
			b.state = StateOpen
			b.openedAt = time.Now()
			b.currentCooldown = b.baseCooldown
		}
	case StateHalfOpen:
		b.state = StateOpen
		b.openedAt = time.Now()
		b.inFlightProbes = 0
		if b.backoffFactor > 1.0 {
			nextCooldown := time.Duration(float64(b.currentCooldown) * b.backoffFactor)
			if b.maxCooldown > 0 && nextCooldown > b.maxCooldown {
				nextCooldown = b.maxCooldown
			}
			b.currentCooldown = nextCooldown
		}
	case StateOpen:
		// 已经处于 Open 状态时，属于在途请求残余失败或并发上报，
		// 严禁重置 openedAt，防止因连续残余失败导致冷却期被无限顺延。
	}
}

// State 返回当前熔断器状态。
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// StateString 返回当前状态的可读字符串。
func (b *Breaker) StateString() string {
	return b.State().String()
}

// Cooldown 返回当前生效的冷却等待时长。
func (b *Breaker) Cooldown() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentCooldown
}

// BaseCooldown 返回配置的基础冷却时间。
func (b *Breaker) BaseCooldown() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.baseCooldown
}

// HalfOpenMax 返回半开状态允许的最大探测数。
func (b *Breaker) HalfOpenMax() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.halfOpenMax
}

// Threshold 返回连续失败熔断阈值。
func (b *Breaker) Threshold() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.threshold
}

// OpenedAt 返回最近一次进入 StateOpen 的时间戳。
func (b *Breaker) OpenedAt() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.openedAt
}

// Failures 返回当前连续失败计数。
func (b *Breaker) Failures() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures
}

// Reset 将熔断器重置为初始 StateClosed 状态。
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.state = StateClosed
	b.failures = 0
	b.successes = 0
	b.inFlightProbes = 0
	b.currentCooldown = b.baseCooldown
	b.openedAt = time.Time{}
}
