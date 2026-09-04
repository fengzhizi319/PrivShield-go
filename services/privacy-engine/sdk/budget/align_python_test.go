package budget

import (
	"math"
	"testing"
)

// ──────────────────────────────────────────────
// 跨语言对齐测试：Go Budget vs Python Budget
//
// Python BudgetAccountant 通过 default_registry.get_or_create() 创建，
// 使用 namespace 隔离；Go 直接实例化。
// 核心语义对齐：
//   - spend(epsilon, delta) 在预算不足时拒绝（Python 抛异常 / Go 返回 false）
//   - remaining 查询语义一致
//   - reset 重置已用预算
// ──────────────────────────────────────────────

// TestAlignPython_InitialBudget 验证初始预算状态与 Python 一致。
// Python:
//
//	ba = default_registry.get_or_create('test', epsilon_total=10.0, delta_total=1e-5)
//	ba.remaining → (10.0, 1e-5)
func TestAlignPython_InitialBudget(t *testing.T) {
	ba := NewBudgetAccountant(10.0, 1e-5, 0)

	// Python: remaining.epsilon == 10.0
	if got := ba.RemainingEpsilon(); got != 10.0 {
		t.Errorf("RemainingEpsilon() = %g, Python = 10.0", got)
	}
	// Python: remaining.delta == 1e-5
	if got := ba.RemainingDelta(); math.Abs(got-1e-5) > 1e-15 {
		t.Errorf("RemainingDelta() = %g, Python = 1e-5", got)
	}
	// UsedEpsilon starts at 0
	if got := ba.UsedEpsilon(); got != 0 {
		t.Errorf("UsedEpsilon() = %g, want 0", got)
	}
}

// TestAlignPython_SpendAndReject 验证扣减与拒绝语义与 Python 一致。
// Python:
//
//	ba.spend(epsilon=2.5, delta=1e-6)  → OK, remaining=(7.5, 9e-6)
//	ba.spend(epsilon=8.0, delta=1e-6)  → raises PrivacyBudgetExhausted
func TestAlignPython_SpendAndReject(t *testing.T) {
	ba := NewBudgetAccountant(10.0, 1e-5, 0)

	// 第一次扣减：成功
	ok := ba.Consume(2.5, 1e-6)
	if !ok {
		t.Fatal("first Consume(2.5, 1e-6) should succeed")
	}
	// Python: remaining.epsilon == 7.5
	if got := ba.RemainingEpsilon(); got != 7.5 {
		t.Errorf("after spend: RemainingEpsilon() = %g, Python = 7.5", got)
	}
	// Python: remaining.delta == 9e-6
	if got := ba.RemainingDelta(); math.Abs(got-9e-6) > 1e-15 {
		t.Errorf("after spend: RemainingDelta() = %g, Python = 9e-6", got)
	}

	// 第二次扣减：超额，应拒绝
	// Python: ba.spend(8.0, 1e-6) → raises PrivacyBudgetExhausted
	ok = ba.Consume(8.0, 1e-6)
	if ok {
		t.Error("Consume(8.0, 1e-6) should fail (budget exhausted)")
	}
	// 拒绝后预算不变
	if got := ba.RemainingEpsilon(); got != 7.5 {
		t.Errorf("after rejected: RemainingEpsilon() = %g, Python = 7.5", got)
	}
}

// TestAlignPython_Reset 验证重置语义与 Python 一致。
// Python:
//
//	ba.reset() → remaining=(10.0, 1e-5)
func TestAlignPython_Reset(t *testing.T) {
	ba := NewBudgetAccountant(10.0, 1e-5, 0)
	ba.Consume(5.0, 5e-6)

	ba.Reset()
	// Python: remaining.epsilon == 10.0 after reset
	if got := ba.RemainingEpsilon(); got != 10.0 {
		t.Errorf("after Reset: RemainingEpsilon() = %g, Python = 10.0", got)
	}
	// Python: remaining.delta == 1e-5 after reset
	if got := ba.RemainingDelta(); math.Abs(got-1e-5) > 1e-15 {
		t.Errorf("after Reset: RemainingDelta() = %g, Python = 1e-5", got)
	}
}

// TestAlignPython_BudgetExhaustionBoundary 验证边界条件。
// 恰好用完预算时应成功，再多一点应拒绝。
func TestAlignPython_BudgetExhaustionBoundary(t *testing.T) {
	ba := NewBudgetAccountant(10.0, 1e-5, 0)

	// 恰好用完 epsilon
	ok := ba.Consume(10.0, 1e-5)
	if !ok {
		t.Error("Consume(10.0, 1e-5) should succeed (exact budget)")
	}
	if got := ba.RemainingEpsilon(); got != 0 {
		t.Errorf("RemainingEpsilon() = %g, want 0", got)
	}

	// 再多一点应拒绝
	ok = ba.Consume(0.001, 0)
	if ok {
		t.Error("Consume(0.001, 0) should fail after budget exhausted")
	}
}

// TestAlignPython_ZeroDeltaConsume 验证 delta=0 的扣减行为。
func TestAlignPython_ZeroDeltaConsume(t *testing.T) {
	ba := NewBudgetAccountant(10.0, 1e-5, 0)

	ok := ba.Consume(1.0, 0)
	if !ok {
		t.Error("Consume(1.0, 0) should succeed")
	}
	if got := ba.RemainingEpsilon(); got != 9.0 {
		t.Errorf("RemainingEpsilon() = %g, want 9.0", got)
	}
}
