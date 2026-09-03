package middleware

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupWAFTestServer() *gin.Engine {
	r := gin.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r.Use(WAF(logger))
	r.POST("/test", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.String(http.StatusOK, "ok:"+string(body))
	})
	r.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	return r
}

func TestWAF_NormalRequestsPass(t *testing.T) {
	r := setupWAFTestServer()

	// Normal GET
	req := httptest.NewRequest(http.MethodGet, "/test?page=1&size=20", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Normal JSON POST
	jsonBody := bytes.NewBufferString(`{"name":"test_user","age":30,"email":"user@example.com"}`)
	req = httptest.NewRequest(http.MethodPost, "/test", jsonBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for normal JSON, got %d", w.Code)
	}
}

func TestWAF_BlocksSQLInjectionInJSON(t *testing.T) {
	r := setupWAFTestServer()

	// SQLi inside JSON body
	jsonBody := bytes.NewBufferString(`{"query":"admin' OR 1=1 --"}`)
	req := httptest.NewRequest(http.MethodPost, "/test", jsonBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for SQLi in JSON body, got %d", w.Code)
	}
}

func TestWAF_BlocksXSSInJSON(t *testing.T) {
	r := setupWAFTestServer()

	// XSS inside JSON body
	jsonBody := bytes.NewBufferString(`{"comment":"<script>alert(1)</script>"}`)
	req := httptest.NewRequest(http.MethodPost, "/test", jsonBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for XSS in JSON body, got %d", w.Code)
	}
}

func TestWAF_BlocksCommandInjectionInJSON(t *testing.T) {
	r := setupWAFTestServer()

	jsonBody := bytes.NewBufferString(`{"cmd":"test; cat /etc/passwd"}`)
	req := httptest.NewRequest(http.MethodPost, "/test", jsonBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for Command Injection in JSON, got %d", w.Code)
	}
}

func TestWAF_BlocksPathTraversalInQuery(t *testing.T) {
	r := setupWAFTestServer()

	req := httptest.NewRequest(http.MethodGet, "/test?file=../../../../etc/passwd", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for Path Traversal in Query, got %d", w.Code)
	}
}
