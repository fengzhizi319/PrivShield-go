package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestParseEndpointRateLimits(t *testing.T) {
	tests := []struct {
		input string
		want  map[string]*EndpointRateLimit
	}{
		{
			input: "",
			want:  nil,
		},
		{
			input: "/v1/privacy/process_file=10:20;/v1/agent/process=50:100",
			want: map[string]*EndpointRateLimit{
				"/v1/privacy/process_file": {RPS: 10, Burst: 20},
				"/v1/agent/process":        {RPS: 50, Burst: 100},
			},
		},
		{
			input: "/v1/heavy=5", // burst defaults to rps*2
			want: map[string]*EndpointRateLimit{
				"/v1/heavy": {RPS: 5, Burst: 10},
			},
		},
	}

	for _, tt := range tests {
		got := ParseEndpointRateLimits(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("ParseEndpointRateLimits(%q) got len %d, want %d", tt.input, len(got), len(tt.want))
			continue
		}
		for k, v := range tt.want {
			g, ok := got[k]
			if !ok {
				t.Errorf("key %q missing in got", k)
				continue
			}
			if g.RPS != v.RPS || g.Burst != v.Burst {
				t.Errorf("for key %q got %+v, want %+v", k, g, v)
			}
		}
	}
}

func TestRateLimitWithEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	perEndpoint := map[string]*EndpointRateLimit{
		"/heavy": {RPS: 1, Burst: 1}, // 只有 1 个 token
	}

	// 默认 100 RPS / 100 Burst
	r.Use(RateLimitWithEndpoints(100, 100, perEndpoint, func(c *gin.Context) string {
		return "test-client"
	}))

	r.GET("/heavy", func(c *gin.Context) {
		c.String(http.StatusOK, "heavy ok")
	})
	r.GET("/light", func(c *gin.Context) {
		c.String(http.StatusOK, "light ok")
	})

	// 1. 请求 /heavy 第一次：200
	req1, _ := http.NewRequest("GET", "/heavy", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first /heavy got %d, want 200", w1.Code)
	}

	// 2. 紧接着请求 /heavy 第二次：429（因为 burst=1）
	req2, _ := http.NewRequest("GET", "/heavy", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second /heavy got %d, want 429", w2.Code)
	}

	// 3. 同时请求 /light：200（走默认 100 RPS，不被 /heavy 阻塞）
	req3, _ := http.NewRequest("GET", "/light", nil)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("/light got %d, want 200", w3.Code)
	}
}

func TestShardedRateLimiter_CapacityCap(t *testing.T) {
	limiter := newShardedRateLimiter()
	defer limiter.stop()

	// 往同一个 shard 写入多个不同的 key
	shard := limiter.shards[0]
	// 临时设置较小的容量测试驱逐
	shard.mu.Lock()
	for i := 0; i < 50; i++ {
		shard.buckets[string(rune('a'+i))] = &rateLimitBucket{
			tokens:    10,
			lastCheck: time.Now(),
		}
	}
	shard.mu.Unlock()

	// allow 操作正常进行且不 panic
	ok := limiter.allow("test-key-1", 10, 20)
	if !ok {
		t.Error("expected allow to succeed")
	}
}

func TestKeyedRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// 按身份+路径维度限流：1 RPS / 1 Burst，豁免 /health 与 /metrics
	r.Use(KeyedRateLimit(1, 1, func(c *gin.Context) string {
		return "user:alice:" + c.Request.URL.Path
	}, "/health", "/metrics"))

	r.GET("/v1/hub/tasks", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/metrics", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	// 1. 同一 key 第一次请求：200
	req1, _ := http.NewRequest("GET", "/v1/hub/tasks", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first /v1/hub/tasks got %d, want 200", w1.Code)
	}

	// 2. 同一 key 第二次请求：429 + Retry-After 头
	req2, _ := http.NewRequest("GET", "/v1/hub/tasks", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second /v1/hub/tasks got %d, want 429", w2.Code)
	}
	if w2.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on 429")
	}

	// 3. 豁免端点不受限流影响（连续请求均 200）
	for i := 0; i < 3; i++ {
		reqH, _ := http.NewRequest("GET", "/health", nil)
		wH := httptest.NewRecorder()
		r.ServeHTTP(wH, reqH)
		if wH.Code != http.StatusOK {
			t.Fatalf("/health request %d got %d, want 200 (exempt)", i, wH.Code)
		}
		reqM, _ := http.NewRequest("GET", "/metrics", nil)
		wM := httptest.NewRecorder()
		r.ServeHTTP(wM, reqM)
		if wM.Code != http.StatusOK {
			t.Fatalf("/metrics request %d got %d, want 200 (exempt)", i, wM.Code)
		}
	}
}

func TestKeyedRateLimit_FallbackKeyOnEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// keyFunc 返回空串时回退客户端 IP 作为限流 key
	r.Use(KeyedRateLimit(1, 1, func(c *gin.Context) string { return "" }))

	r.GET("/v1/hub/status", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	for i, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		req, _ := http.NewRequest("GET", "/v1/hub/status", nil)
		req.RemoteAddr = "10.1.2.3:5678"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("request %d got %d, want %d", i, w.Code, want)
		}
	}
}
