// Package budget 提供无锁内存原子隐私预算会计。
//
// 基于 atomic.Uint64 浮点位操作实现无锁扣减与查询，
// 支持滑动窗口自动重置。适用于单实例部署；多实例场景
// 应使用 Redis 或 PostgreSQL 后端。
package budget

import (
	"math"
	"sync/atomic"
	"time"
)

// BudgetAccountant 管理 (ε, δ) 隐私预算的扣减与查询。
type BudgetAccountant struct {
	// 使用 atomic 浮点位操作实现无锁并发
	totalEpsilonBits atomic.Uint64 // 总 ε 预算（以 uint64 位表示）
	usedEpsilonBits  atomic.Uint64 // 已消耗 ε
	totalDeltaBits   atomic.Uint64 // 总 δ 预算
	usedDeltaBits    atomic.Uint64 // 已消耗 δ

	// 滑动窗口重置
	windowSeconds int64
	lastResetTime atomic.Int64
}

// NewBudgetAccountant 创建预算会计实例。
// totalEpsilon / totalDelta 为总预算上限。
// windowSeconds 为滑动窗口重置周期（0 表示禁用自动重置）。
func NewBudgetAccountant(totalEpsilon, totalDelta float64, windowSeconds int64) *BudgetAccountant {
	ba := &BudgetAccountant{
		windowSeconds: windowSeconds,
	}
	ba.totalEpsilonBits.Store(math.Float64bits(totalEpsilon))
	ba.totalDeltaBits.Store(math.Float64bits(totalDelta))
	ba.lastResetTime.Store(time.Now().Unix())
	return ba
}

// TotalEpsilon 返回总 ε 预算。
func (ba *BudgetAccountant) TotalEpsilon() float64 {
	return math.Float64frombits(ba.totalEpsilonBits.Load())
}

// UsedEpsilon 返回已消耗 ε。
func (ba *BudgetAccountant) UsedEpsilon() float64 {
	return math.Float64frombits(ba.usedEpsilonBits.Load())
}

// RemainingEpsilon 返回剩余 ε 预算。
func (ba *BudgetAccountant) RemainingEpsilon() float64 {
	total := ba.TotalEpsilon()
	used := ba.UsedEpsilon()
	remaining := total - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

// TotalDelta 返回总 δ 预算。
func (ba *BudgetAccountant) TotalDelta() float64 {
	return math.Float64frombits(ba.totalDeltaBits.Load())
}

// UsedDelta 返回已消耗 δ。
func (ba *BudgetAccountant) UsedDelta() float64 {
	return math.Float64frombits(ba.usedDeltaBits.Load())
}

// RemainingDelta 返回剩余 δ 预算。
func (ba *BudgetAccountant) RemainingDelta() float64 {
	total := ba.TotalDelta()
	used := ba.UsedDelta()
	remaining := total - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Consume 尝试扣减 (ε, δ) 预算。
// 若剩余预算不足则返回 false，不做任何扣减。
// 使用严格无锁 CAS 循环保障并发安全与原子回滚。
func (ba *BudgetAccountant) Consume(epsilon, delta float64) bool {
	ba.maybeReset()

	totalE := ba.TotalEpsilon()
	totalD := ba.TotalDelta()

	// 1. 原子扣减 ε 预算（CAS 循环每次从当前 oldBits 反解真实值）
	for {
		oldEBits := ba.usedEpsilonBits.Load()
		curUsedE := math.Float64frombits(oldEBits)
		newEVal := curUsedE + epsilon
		if newEVal > totalE {
			return false
		}
		if ba.usedEpsilonBits.CompareAndSwap(oldEBits, math.Float64bits(newEVal)) {
			break
		}
	}

	if delta <= 0 {
		return true
	}

	// 2. 原子扣减 δ 预算
	for {
		oldDBits := ba.usedDeltaBits.Load()
		curUsedD := math.Float64frombits(oldDBits)
		newDVal := curUsedD + delta
		if newDVal > totalD {
			// δ 超限，原子回滚刚才已扣减的 ε
			ba.rollbackEpsilon(epsilon)
			return false
		}
		if ba.usedDeltaBits.CompareAndSwap(oldDBits, math.Float64bits(newDVal)) {
			break
		}
	}
	return true
}

func (ba *BudgetAccountant) rollbackEpsilon(epsilon float64) {
	for {
		old := ba.usedEpsilonBits.Load()
		cur := math.Float64frombits(old)
		reverted := cur - epsilon
		if reverted < 0 {
			reverted = 0
		}
		if ba.usedEpsilonBits.CompareAndSwap(old, math.Float64bits(reverted)) {
			break
		}
	}
}

// maybeReset 检查是否需要滑动窗口重置。
func (ba *BudgetAccountant) maybeReset() {
	if ba.windowSeconds <= 0 {
		return
	}
	lastReset := ba.lastResetTime.Load()
	now := time.Now().Unix()
	if now-lastReset >= ba.windowSeconds {
		if ba.lastResetTime.CompareAndSwap(lastReset, now) {
			// 重置已用预算
			ba.usedEpsilonBits.Store(0)
			ba.usedDeltaBits.Store(0)
		}
	}
}

// Reset 手动重置已用预算。
func (ba *BudgetAccountant) Reset() {
	ba.usedEpsilonBits.Store(0)
	ba.usedDeltaBits.Store(0)
	ba.lastResetTime.Store(time.Now().Unix())
}
