package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/config"
)

func TestClientHealth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": "1.8.0"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("PRIVACY_AGENT_URLS", srv.URL)
	cfg := config.Load()
	c := New(cfg)

	ctx := context.Background()
	health, err := c.Health(ctx)
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	if health["status"] != "ok" {
		t.Errorf("expected health status ok, got %v", health["status"])
	}
}
