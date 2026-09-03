package envelope

import (
	"net/http"
	"testing"
)

func TestErrorCodeFromStatus(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, "INVALID_ARGUMENT"},
		{http.StatusUnauthorized, "UNAUTHORIZED"},
		{http.StatusForbidden, "FORBIDDEN"},
		{http.StatusNotFound, "NOT_FOUND"},
		{http.StatusTooManyRequests, "RESOURCE_EXHAUSTED"},
		{http.StatusInternalServerError, "INTERNAL_ERROR"},
		{http.StatusBadGateway, "BAD_GATEWAY"},
		{http.StatusServiceUnavailable, "UNAVAILABLE"},
		{http.StatusGatewayTimeout, "DEADLINE_EXCEEDED"},
		{418, "UNKNOWN_ERROR"},
	}

	for _, tt := range tests {
		if got := ErrorCodeFromStatus(tt.status); got != tt.want {
			t.Errorf("ErrorCodeFromStatus(%d) = %q, want %q", tt.status, got, tt.want)
		}
	}
}
