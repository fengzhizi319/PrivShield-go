// Package naming 测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证 naming 可观测入口（Observer 模式）的事件上报与防重复计数：
//  1. 【别名使用记录】：验证 Normalize 对别名输入（yibao、中文别名、api2_kangyang）
//     通过 Observer 上报 alias 事件，包含正确的别名、规范名和目标类型；
//  2. 【规范名输入无事件】：验证直接输入规范名（DSYibao）不产生 alias 事件；
//  3. 【失败原因上报】：验证空输入、未知输入、保留字输入分别上报 ReasonEmpty、
//     ReasonUnknown、ReasonReserved 错误事件；
//  4. 【CheckWritable 规范化检查】：验证 CheckWritable 对别名/未注册/保留字的拒绝，
//     以及上报 ReasonFormatInvalid 等不同的错误原因；
//  5. 【防重复计数】：验证 ResolveInbound 对保留字输入仅上报一次错误事件，
//     防止 Normalize 和 checkWritableEntry 双重计数；
//  6. 【Observer 可选性】：验证未注册 Observer 时所有功能正常工作、不 panic。
// ==============================================================================

package naming

import (
	"sync"
	"testing"
)

// fakeObserver records every event emitted through the naming choke point.
// fakeObserver 记录经由 naming 统一入口上报的事件。
type fakeObserver struct {
	mu     sync.Mutex
	alias  [][3]string // {alias, canonical, target}
	errors []string    // reason
}

func (f *fakeObserver) RecordAPIAlias(alias, canonical, target string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alias = append(f.alias, [3]string{alias, canonical, target})
}

func (f *fakeObserver) RecordNormalizeError(reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors = append(f.errors, reason)
}

func (f *fakeObserver) aliasCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.alias)
}

func (f *fakeObserver) errorCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.errors)
}

// useObserver installs obs for the duration of the test.
// useObserver 在本次测试期间安装 obs，结束后恢复。
func useObserver(t *testing.T, obs Observer) {
	t.Helper()
	prev := CurrentObserver()
	SetObserver(obs)
	t.Cleanup(func() { SetObserver(prev) })
}

func TestNormalizeRecordsAliasUse(t *testing.T) {
	obs := &fakeObserver{}
	useObserver(t, obs)

	if _, err := Normalize("yibao"); err != nil {
		t.Fatalf("Normalize(yibao) failed: %v", err)
	}
	if _, err := Normalize("医保"); err != nil {
		t.Fatalf("Normalize(中文别名) failed: %v", err)
	}
	if _, err := Normalize("api2_kangyang"); err != nil {
		t.Fatalf("Normalize(api2_kangyang) failed: %v", err)
	}

	want := [][3]string{
		{"yibao", DSYibao, TargetDataSourceID},
		{"医保", DSYibao, TargetDataSourceID},
		{"api2_kangyang", DSKangyang, TargetAPICode},
	}
	if len(obs.alias) != len(want) {
		t.Fatalf("alias events = %d, want %d (%v)", len(obs.alias), len(want), obs.alias)
	}
	for i, w := range want {
		if obs.alias[i] != w {
			t.Errorf("alias event %d = %v, want %v", i, obs.alias[i], w)
		}
	}
}

func TestCanonicalInputEmitsNoAliasEvent(t *testing.T) {
	obs := &fakeObserver{}
	useObserver(t, obs)

	if _, err := Normalize(DSYibao); err != nil {
		t.Fatalf("Normalize(canonical) failed: %v", err)
	}
	if got := obs.aliasCount(); got != 0 {
		t.Errorf("canonical input produced %d alias events, want 0", got)
	}
	if got := obs.errorCount(); got != 0 {
		t.Errorf("canonical input produced %d error events, want 0", got)
	}
}

func TestNormalizeFailureReasons(t *testing.T) {
	obs := &fakeObserver{}
	useObserver(t, obs)

	if _, err := Normalize("   "); err == nil {
		t.Fatal("empty input must fail")
	}
	if _, err := Normalize("shebao"); err == nil {
		t.Fatal("unknown input must fail closed")
	}
	if _, err := ResolveInbound(DSMock3); err == nil {
		t.Fatal("reserved datasource must fail on write side")
	} else if !IsReserved(err) {
		t.Fatalf("ResolveInbound(%s) error = %v, want reserved", DSMock3, err)
	}

	want := []string{ReasonEmpty, ReasonUnknown, ReasonReserved}
	if len(obs.errors) != len(want) {
		t.Fatalf("error events = %v, want %v", obs.errors, want)
	}
	for i, w := range want {
		if obs.errors[i] != w {
			t.Errorf("error event %d = %q, want %q", i, obs.errors[i], w)
		}
	}
}

func TestCheckWritableReportsDistinctReasons(t *testing.T) {
	obs := &fakeObserver{}
	useObserver(t, obs)

	if err := CheckWritable(DSYibao); err != nil {
		t.Fatalf("CheckWritable(active) failed: %v", err)
	}
	if err := CheckWritable("yibao"); err == nil {
		t.Fatal("alias literal must not pass the canonical-only checker")
	}
	if err := CheckWritable("ds_shebao"); err == nil {
		t.Fatal("well-formed but unregistered id must fail")
	}
	if err := CheckWritable(DSMock4); err == nil {
		t.Fatal("reserved id must fail")
	}

	want := []string{ReasonFormatInvalid, ReasonUnknown, ReasonReserved}
	if len(obs.errors) != len(want) {
		t.Fatalf("error events = %v, want %v", obs.errors, want)
	}
	for i, w := range want {
		if obs.errors[i] != w {
			t.Errorf("error event %d = %q, want %q", i, obs.errors[i], w)
		}
	}
}

// TestResolveInboundCountsReservedOnce guards against double counting now that
// both Normalize and checkWritableEntry emit events.
// TestResolveInboundCountsReservedOnce 防止同一调用被重复计数。
func TestResolveInboundCountsReservedOnce(t *testing.T) {
	obs := &fakeObserver{}
	useObserver(t, obs)

	if _, err := ResolveInbound("mock3"); err == nil || !IsReserved(err) {
		t.Fatalf("ResolveInbound(mock3) = %v, want reserved error", err)
	}
	if got := obs.errorCount(); got != 1 {
		t.Errorf("reserved input produced %d error events, want exactly 1", got)
	}

	if _, err := ResolveInbound(DSYibao); err != nil {
		t.Fatalf("ResolveInbound(active canonical) failed: %v", err)
	}
	if got := obs.errorCount(); got != 1 {
		t.Errorf("successful resolve added error events (now %d, want 1)", got)
	}
	// "mock3" 是别名，应当且仅应当上报一次 alias 使用；canonical 入站不产生事件。
	if got := obs.aliasCount(); got != 1 {
		t.Errorf("alias events = %d, want 1 (only the 'mock3' alias)", got)
	}
}

func TestObserverIsOptional(t *testing.T) {
	SetObserver(nil)
	// Must not panic when no observer is registered.
	if _, err := Normalize("kangyang"); err != nil {
		t.Fatalf("Normalize without observer failed: %v", err)
	}
	if _, err := Normalize("nope"); err == nil {
		t.Fatal("unknown input must still fail closed without an observer")
	}
	if err := CheckWritable(DSMock3); err == nil {
		t.Fatal("CheckWritable must still fail closed without an observer")
	}
}
