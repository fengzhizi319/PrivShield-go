package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/config"
	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
)

// TestMicroserviceProxy_Routes verifies that the allowlisted routes under
// /v1/hub, /v1/datasource and /v1/audit proxy method, path, query and body
// to the upstream Go microservices and inject X-Request-ID / X-Trace-ID /
// Authorization headers.
func TestMicroserviceProxy_Routes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create three upstream fake microservices.
	var gotHubHeaders, gotDSHeaders, gotAuditHeaders http.Header
	var gotHubPath, gotDSPath, gotAuditPath string

	hubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHubHeaders = r.Header.Clone()
		gotHubPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"service":"hub","id":"task-123"}`))
	}))
	defer hubSrv.Close()

	dsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDSHeaders = r.Header.Clone()
		gotDSPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"service":"datasource","total":1}`))
	}))
	defer dsSrv.Close()

	auditSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuditHeaders = r.Header.Clone()
		gotAuditPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"service":"audit","verified":true}`))
	}))
	defer auditSrv.Close()

	cfg := &config.Config{
		HubURL:           hubSrv.URL,
		DatasourceURL:    dsSrv.URL,
		AuditURL:         auditSrv.URL,
		HubAPIKey:        "hub-key",
		DatasourceAPIKey: "ds-key",
		AuditAPIKey:      "audit-key",
		ConsoleRateLimit: 0, // disable rate limit for tests
	}

	client, err := agent.New(cfg)
	if err != nil {
		// agent.New only needs the agent connection config; if it fails, fall back to nil.
		client = nil
	}
	defer func() {
		if client != nil {
			_ = client.Close()
		}
	}()

	server := New(client, cfg, nil, metrics.NewCollector("test"))
	router := gin.New()
	server.RegisterRoutes(router)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/hub/v1/hub/tasks?status=pending", nil)
	req.Header.Set("X-Request-ID", "req-123")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("hub proxy status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotHubPath != "/v1/hub/tasks" {
		t.Errorf("hub upstream path = %q, want /v1/hub/tasks", gotHubPath)
	}
	if gotHubHeaders.Get("X-Request-ID") != "req-123" {
		t.Errorf("hub X-Request-ID = %q, want req-123", gotHubHeaders.Get("X-Request-ID"))
	}
	if gotHubHeaders.Get("X-Trace-ID") != "req-123" {
		t.Errorf("hub X-Trace-ID = %q, want req-123", gotHubHeaders.Get("X-Trace-ID"))
	}
	if gotHubHeaders.Get("Authorization") != "Bearer hub-key" {
		t.Errorf("hub Authorization = %q, want Bearer hub-key", gotHubHeaders.Get("Authorization"))
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/v1/datasource/v1/datasources", nil)
	req.Header.Set("X-Request-ID", "req-456")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("datasource proxy status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotDSPath != "/v1/datasources" {
		t.Errorf("datasource upstream path = %q, want /v1/datasources", gotDSPath)
	}
	if gotDSHeaders.Get("Authorization") != "Bearer ds-key" {
		t.Errorf("datasource Authorization = %q, want Bearer ds-key", gotDSHeaders.Get("Authorization"))
	}

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/v1/audit/v1/audit/logs", nil)
	req.Header.Set("X-Request-ID", "req-789")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("audit proxy status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotAuditPath != "/v1/audit/logs" {
		t.Errorf("audit upstream path = %q, want /v1/audit/logs", gotAuditPath)
	}
	if gotAuditHeaders.Get("Authorization") != "Bearer audit-key" {
		t.Errorf("audit Authorization = %q, want Bearer audit-key", gotAuditHeaders.Get("Authorization"))
	}
}

// TestMicroserviceProxy_Body verifies POST body and query forwarding.
func TestMicroserviceProxy_Body(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotBody []byte
	var gotQuery string
	hubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"created":true}`))
	}))
	defer hubSrv.Close()

	cfg := &config.Config{
		HubURL:           hubSrv.URL,
		DatasourceURL:    hubSrv.URL,
		AuditURL:         hubSrv.URL,
		ConsoleRateLimit: 0,
	}

	server := New(nil, cfg, nil, metrics.NewCollector("test"))
	router := gin.New()
	server.RegisterRoutes(router)

	body := `{"source":"test","operation":"mask"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/hub/v1/hub/dispatch?priority=1", nil)
	req.Body = io.NopCloser(strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if string(gotBody) != body {
		t.Errorf("upstream body = %q, want %q", string(gotBody), body)
	}
	if gotQuery != "priority=1" {
		t.Errorf("upstream query = %q, want priority=1", gotQuery)
	}
}
