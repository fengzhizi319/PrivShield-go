package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/clients"
	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/config"
	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/models"
)

func TestGetAvailableSuites(t *testing.T) {
	runner := NewTestRunner(nil)
	suites := runner.GetAvailableSuites()
	if len(suites) < 4 {
		t.Fatalf("expected at least 4 test suites, got %d", len(suites))
	}
	expectedIDs := map[string]bool{"TS-01": true, "TS-02": true, "TS-03": true, "TS-04": true}
	for _, s := range suites {
		if !expectedIDs[s.ID] {
			t.Errorf("unexpected suite ID: %s", s.ID)
		}
	}
}

func TestRunSuites_WithMockAuditLog(t *testing.T) {
	// Mock upstream audit-log server
	auditSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/audit/logs":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"id":             "audit-12345",
					"snapshot_id":    "snap-12345",
					"integrity_hash": "hash-12345",
					"via":            "audit-log",
				})
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total": 1,
				"logs": []map[string]any{
					{
						"id":            "audit-12345",
						"timestamp":     time.Now().Format(time.RFC3339),
						"datasource_id": "ds_yibao",
						"datasource":    "ds_yibao",
						"operation":     "mask",
						"input_hash":    "in-hash",
						"output_hash":   "out-hash",
						"status":        "success",
					},
				},
				"via": "audit-log",
			})
		case "/api/audit/snapshots":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total": 1,
				"snapshots": []map[string]any{
					{
						"id":             "snap-12345",
						"audit_log_id":   "audit-12345",
						"timestamp":      time.Now().Format(time.RFC3339),
						"integrity_hash": "hash-12345",
					},
				},
				"via": "audit-log",
			})
		case "/api/audit/snapshots/verify":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"snapshot_id": "snap-12345",
				"valid":       true,
				"expected":    "hash-12345",
				"actual":      "hash-12345",
				"via":         "audit-log",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer auditSrv.Close()

	cfg := &config.Config{
		AuditURL: auditSrv.URL,
	}
	pool := clients.NewClientPool(cfg)
	runner := NewTestRunner(pool)

	resp := runner.RunSuites(context.Background(), models.RunTestSuiteRequest{
		SuiteIDs: []string{"TS-01"},
	})

	if resp.Status != "completed" {
		t.Fatalf("expected run status completed, got %s", resp.Status)
	}
	if resp.PassedCases != 1 || resp.FailedCases != 0 {
		t.Fatalf("expected 1 passed case and 0 failed cases, got %d passed, %d failed", resp.PassedCases, resp.FailedCases)
	}
	if resp.Results[0].ID != "TS-01" || resp.Results[0].Status != "passed" {
		t.Errorf("expected TS-01 passed, got %+v", resp.Results[0])
	}
}
