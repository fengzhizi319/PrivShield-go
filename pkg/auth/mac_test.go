package auth

import (
	"testing"
)

func TestEvaluateMAC(t *testing.T) {
	tests := []struct {
		name      string
		clearance SecurityLevel
		object    SecurityLevel
		wantErr   bool
	}{
		{"equal clearance S2 accesses S2", LevelInternal, LevelInternal, false},
		{"higher clearance S3 accesses S1", LevelConfidential, LevelPublic, false},
		{"admin clearance S4 accesses S4", LevelRestricted, LevelRestricted, false},
		{"insufficient clearance S1 accesses S3", LevelPublic, LevelConfidential, true},
		{"insufficient clearance S2 accesses S4", LevelInternal, LevelRestricted, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := EvaluateMAC(tt.clearance, tt.object)
			if (err != nil) != tt.wantErr {
				t.Errorf("EvaluateMAC() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseSecurityLevel(t *testing.T) {
	if got := ParseSecurityLevel("S4"); got != LevelRestricted {
		t.Errorf("ParseSecurityLevel(S4) = %v, want LevelRestricted", got)
	}
	if got := ParseSecurityLevel("l2"); got != LevelInternal {
		t.Errorf("ParseSecurityLevel(l2) = %v, want LevelInternal", got)
	}
	if got := ParseSecurityLevel("unknown"); got != LevelRestricted {
		t.Errorf("ParseSecurityLevel(unknown) = %v, want LevelRestricted fail-closed default", got)
	}
}
