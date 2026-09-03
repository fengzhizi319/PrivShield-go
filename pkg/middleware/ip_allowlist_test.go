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
	r.Use(IPAllowlist([]string{"10.0.0.0/8", "192.168.1.0/24", "::1/128", "2001:db8::/32"}))
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
		{"IPv6 loopback matches ::1/128", "[::1]:54321", http.StatusOK},
		{"IPv6 subnet matches 2001:db8::/32", "[2001:db8::cafe]:9999", http.StatusOK},
		{"IPv6 outside not in allowlist", "[2001:dead::1]:8888", http.StatusForbidden},
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

func TestAllowedCIDRsFromEnv(t *testing.T) {
	// 1. 空键名时返回 nil（pkg 自身不硬编码任何具体变量）
	if res := AllowedCIDRsFromEnv(""); res != nil {
		t.Errorf("expected nil for empty envKey, got %v", res)
	}

	// 2. 传入不存在或未配置的键时返回 nil
	t.Setenv("NON_EXISTENT_CIDRS", "")
	if res := AllowedCIDRsFromEnv("NON_EXISTENT_CIDRS"); res != nil {
		t.Errorf("expected nil for empty env, got %v", res)
	}

	// 3. 传入配置的专属变量时成功解析
	t.Setenv("GATEWAY_ALLOWED_CIDRS", "172.16.0.0/16")
	resGw := AllowedCIDRsFromEnv("GATEWAY_ALLOWED_CIDRS")
	if len(resGw) != 1 || resGw[0] != "172.16.0.0/16" {
		t.Errorf("service-specific mismatch, got %v", resGw)
	}

	// 4. 解析多网段逗号分隔
	t.Setenv("AGENT_ALLOWED_CIDRS", "10.0.0.0/8, 192.168.1.0/24")
	resAgent := AllowedCIDRsFromEnv("AGENT_ALLOWED_CIDRS")
	if len(resAgent) != 2 || resAgent[0] != "10.0.0.0/8" || resAgent[1] != "192.168.1.0/24" {
		t.Errorf("parsed slices mismatch, got %v", resAgent)
	}
}
