package validation

import (
	"strings"
	"testing"

	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
)

func TestAllowedValues(t *testing.T) {
	allowed := []string{"mask", "k_anon", "dp"}

	if err := AllowedValues("op", "mask", allowed); err != nil {
		t.Errorf("mask should be allowed: %v", err)
	}
	if err := AllowedValues("op", "invalid", allowed); err == nil {
		t.Error("invalid should be rejected")
	}
	if err := AllowedValues("op", "", allowed); err == nil {
		t.Error("empty should be rejected")
	}
}

func TestPortRange(t *testing.T) {
	if err := PortRange(80); err != nil {
		t.Errorf("port 80 should be valid: %v", err)
	}
	if err := PortRange(1); err != nil {
		t.Errorf("port 1 should be valid: %v", err)
	}
	if err := PortRange(65535); err != nil {
		t.Errorf("port 65535 should be valid: %v", err)
	}
	if err := PortRange(0); err == nil {
		t.Error("port 0 should be invalid")
	}
	if err := PortRange(-1); err == nil {
		t.Error("port -1 should be invalid")
	}
	if err := PortRange(70000); err == nil {
		t.Error("port 70000 should be invalid")
	}
}

func TestNonEmpty(t *testing.T) {
	if err := NonEmpty("field", "value"); err != nil {
		t.Errorf("non-empty should pass: %v", err)
	}
	if err := NonEmpty("field", ""); err == nil {
		t.Error("empty should fail")
	}
}

func TestMaxLength(t *testing.T) {
	if err := MaxLength("f", "abc", 5); err != nil {
		t.Errorf("short string should pass: %v", err)
	}
	if err := MaxLength("f", "abc", 3); err != nil {
		t.Errorf("exact length should pass: %v", err)
	}
	if err := MaxLength("f", "abcdef", 3); err == nil {
		t.Error("too long should fail")
	}
}

func TestWhitelists(t *testing.T) {
	// Verify whitelists are non-empty
	if len(HubOperations) == 0 {
		t.Error("HubOperations should not be empty")
	}
	if len(AuditOperations) == 0 {
		t.Error("AuditOperations should not be empty")
	}
	if len(AuditStatuses) == 0 {
		t.Error("AuditStatuses should not be empty")
	}
	if len(DataSourceTypes) == 0 {
		t.Error("DataSourceTypes should not be empty")
	}
	if len(SensitivityLevels) == 0 {
		t.Error("SensitivityLevels should not be empty")
	}
	// P1-5：等级词表只有 pkg/naming 一个 Go 侧实现，validation 只能是其别名。
	if got, want := strings.Join(SensitivityLevels, ","), strings.Join(naming.SecurityLevelIDs(), ","); got != want {
		t.Errorf("SensitivityLevels = %q, want the pkg/naming vocabulary %q", got, want)
	}
}
