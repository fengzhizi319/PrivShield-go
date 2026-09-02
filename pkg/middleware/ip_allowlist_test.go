package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIPAllowlist_EmptyPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(IPAllowlist(nil))
	r.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "1.2.3.4:12345"
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("empty allowlist: got %d, want 200", w.Code)
	}
}

func TestIPAllowlist_MatchedCIDR(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(IPAllowlist([]string{"10.0.0.0/8", "192.168.1.0/24"}))
	r.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	tests := []struct {
		name     string
		remoteIP string
		wantCode int
	}{
		{"10.x matches 10.0.0.0/8", "10.0.0.1:1234", http.StatusOK},
		{"192.168.1.x matches", "192.168.1.100:1234", http.StatusOK},
		{"172.16.x not in allowlist", "172.16.0.1:1234", http.StatusForbidden},
		{"8.8.8.8 not in allowlist", "8.8.8.8:1234", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = tt.remoteIP
			r.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("got %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

func TestIPAllowlist_SingleIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(IPAllowlist([]string{"10.0.0.1"}))
	r.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("single IP match: got %d, want 200", w.Code)
	}

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.RemoteAddr = "10.0.0.2:1234"
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusForbidden {
		t.Errorf("single IP mismatch: got %d, want 403", w2.Code)
	}
}

func TestIPAllowlist_InvalidCIDRSkipped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(IPAllowlist([]string{"not-a-cidr", "10.0.0.0/8"}))
	r.GET("/test", func(c *gin.Context) { c.String(200, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("valid CIDR should still work after invalid entry: got %d, want 200", w.Code)
	}
}
