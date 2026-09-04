package dynclassification

import (
	"testing"
)

func TestDomainStrategyRegistry_RegisterAndGet(t *testing.T) {
	r := NewDomainStrategyRegistry()

	cb := func(fieldName, text, finalLevel, mode string) string {
		return "[MEDICAL-REDACTED]"
	}

	if err := r.RegisterSanitizer("medical", cb); err != nil {
		t.Fatal(err)
	}

	got := r.GetSanitizer("medical")
	if got == nil {
		t.Fatal("expected sanitizer callback for 'medical'")
	}
	result := got("diagnosis", "高血压", "secret", "redact")
	if result != "[MEDICAL-REDACTED]" {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestDomainStrategyRegistry_CaseInsensitive(t *testing.T) {
	r := NewDomainStrategyRegistry()

	cb := func(fieldName, text, finalLevel, mode string) string { return "ok" }
	_ = r.RegisterSanitizer("Medical", cb)

	if !r.HasSanitizer("medical") {
		t.Error("expected case-insensitive lookup to find 'medical'")
	}
	if !r.HasSanitizer("MEDICAL") {
		t.Error("expected uppercase lookup to find sanitizer")
	}
}

func TestDomainStrategyRegistry_Unregister(t *testing.T) {
	r := NewDomainStrategyRegistry()
	cb := func(fieldName, text, finalLevel, mode string) string { return "ok" }
	_ = r.RegisterSanitizer("finance", cb)

	if !r.UnregisterSanitizer("finance") {
		t.Error("expected unregister to return true")
	}
	if r.HasSanitizer("finance") {
		t.Error("expected sanitizer to be removed")
	}
	if r.UnregisterSanitizer("finance") {
		t.Error("expected second unregister to return false")
	}
}

func TestDomainStrategyRegistry_RegisteredDomains(t *testing.T) {
	r := NewDomainStrategyRegistry()
	cb := func(fieldName, text, finalLevel, mode string) string { return "ok" }
	_ = r.RegisterSanitizer("medical", cb)
	_ = r.RegisterSanitizer("finance", cb)

	domains := r.RegisteredDomains()
	if len(domains) != 2 {
		t.Errorf("expected 2 domains, got %d", len(domains))
	}
}

func TestDomainStrategyRegistry_InvalidRegister(t *testing.T) {
	r := NewDomainStrategyRegistry()

	if err := r.RegisterSanitizer("", nil); err == nil {
		t.Error("expected error for empty domain")
	}
	if err := r.RegisterSanitizer("test", nil); err == nil {
		t.Error("expected error for nil sanitizer")
	}
}

func TestDomainStrategyRegistry_GlobalSingleton(t *testing.T) {
	ResetGlobalRegistry()
	defer ResetGlobalRegistry()

	r1 := GetGlobalRegistry()
	r2 := GetGlobalRegistry()
	if r1 != r2 {
		t.Error("expected same singleton instance")
	}
}
