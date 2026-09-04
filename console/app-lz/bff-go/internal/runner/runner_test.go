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
	// Mock upstream server：同时模拟 service-hub（唯一编排中枢）和 audit-log（审计存证）
	// app-lz BFF 只访问 service-hub，service-hub 内部编排 datasource-mgr / engine-go / audit-log
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/hub/fetch-and-desensitize":
			if r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"datasource_id": "ds_yibao",
					"id_card_no":    "510101199001011234",
					"found":         true,
					"level":         "L4",
					"sanitized_data": map[string]any{
						"name":    "李*",
						"id_card": "5101***********234",
					},
					"classification_report": map[string]any{"max_sensitivity": "L4"},
					"summary":               map[string]any{"total_fields": 2, "sanitized_fields": 2},
					"audit_task_id":         "fad-ds_yibao-510101199001011234-mock123",
					"via":                   "service-hub",
				})
				return
			}
		case "/v1/hub/audit/logs", "/v1/audit/logs":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"id":             "audit-12345",
					"snapshot_id":    "snap-12345",
					"integrity_hash": "hash-12345",
					"via":            "service-hub",
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
				"via": "service-hub",
			})
		case "/v1/hub/audit/verify":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"snapshot_id":   "snap-12345",
				"merkle_valid":  true,
				"expected_hash": "hash-12345",
				"root_hash":     "hash-12345",
				"total_entries": 1,
				"source":        "service-hub",
				"via":           "service-hub",
			})
		case "/v1/audit/snapshots":
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
		case "/v1/audit/snapshots/verify":
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
	defer mockSrv.Close()

	cfg := &config.Config{
		HubURL:   mockSrv.URL,
		AuditURL: mockSrv.URL,
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
