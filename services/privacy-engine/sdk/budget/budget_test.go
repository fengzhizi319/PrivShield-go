package budget

import (
	"testing"
)

func TestNewBudgetAccountant(t *testing.T) {
	ba := NewBudgetAccountant(10.0, 1e-5, 0)

	if ba.TotalEpsilon() != 10.0 {
		t.Errorf("TotalEpsilon() = %f, want 10.0", ba.TotalEpsilon())
	}

	if ba.TotalDelta() != 1e-5 {
		t.Errorf("TotalDelta() = %e, want 1e-5", ba.TotalDelta())
	}

	if ba.UsedEpsilon() != 0 {
		t.Errorf("UsedEpsilon() = %f, want 0", ba.UsedEpsilon())
	}

	if ba.RemainingEpsilon() != 10.0 {
		t.Errorf("RemainingEpsilon() = %f, want 10.0", ba.RemainingEpsilon())
	}
}

func TestConsume(t *testing.T) {
	ba := NewBudgetAccountant(10.0, 1e-5, 0)

	// 成功扣减
	ok := ba.Consume(1.0, 1e-6)
	if !ok {
		t.Error("Consume should succeed")
	}

	if ba.UsedEpsilon() != 1.0 {
		t.Errorf("UsedEpsilon() = %f, want 1.0", ba.UsedEpsilon())
	}

	if ba.RemainingEpsilon() != 9.0 {
		t.Errorf("RemainingEpsilon() = %f, want 9.0", ba.RemainingEpsilon())
	}

	// 超额扣减
	ok = ba.Consume(10.0, 0)
	if ok {
		t.Error("Consume should fail when budget exhausted")
	}
}

func TestConsumeDelta(t *testing.T) {
	ba := NewBudgetAccountant(10.0, 1e-5, 0)

	ok := ba.Consume(0, 5e-6)
	if !ok {
		t.Error("Consume delta should succeed")
	}

	if ba.RemainingDelta() != 5e-6 {
		t.Errorf("RemainingDelta() = %e, want 5e-6", ba.RemainingDelta())
	}

	// 超额扣减
	ok = ba.Consume(0, 1e-5)
	if ok {
		t.Error("Consume delta should fail when budget exhausted")
	}
}

func TestReset(t *testing.T) {
	ba := NewBudgetAccountant(10.0, 1e-5, 0)

	ba.Consume(5.0, 5e-6)
	if ba.UsedEpsilon() != 5.0 {
		t.Errorf("UsedEpsilon() = %f, want 5.0", ba.UsedEpsilon())
	}

	ba.Reset()
	if ba.UsedEpsilon() != 0 {
		t.Errorf("UsedEpsilon() after Reset() = %f, want 0", ba.UsedEpsilon())
	}

	if ba.RemainingEpsilon() != 10.0 {
		t.Errorf("RemainingEpsilon() after Reset() = %f, want 10.0", ba.RemainingEpsilon())
	}
}

func TestConcurrentConse(t *testing.T) {
	ba := NewBudgetAccountant(100.0, 1e-4, 0)

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				ba.Consume(1.0, 1e-6)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// 验证总消耗不超过预算
	if ba.UsedEpsilon() > 100.0 {
		t.Errorf("UsedEpsilon() = %f, should not exceed 100.0", ba.UsedEpsilon())
	}
}
