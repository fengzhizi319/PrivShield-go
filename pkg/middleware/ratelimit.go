// Package middleware provides DDoS protection and rate limiting middlewares.
// Package middleware 提供分布式拒绝服务（DDoS）纵深防御与多级限流中间件。
//
// ===============================================================================
// 【DDoS 纵深防御架构】
// 1. 【Payload 保护 (MaxBodySize)】：
//    基于 http.MaxBytesReader 限制请求体最大字节数（默认 32 MiB），杜绝超大 Payload 引发内存耗尽（OOM）；
// 2. 【并发过载保护 (MaxConcurrent)】：
//    基于带缓冲 Channel 信号量限制服务端瞬时在途并发处理请求数（默认 1000 并发），超限快速返回 503 并提示重试；
// 3. 【L7 HTTP Flood 防护 (RateLimit)】：
//    基于 32 分片令牌桶算法（Token Bucket），平滑限流突发请求（超限返回 429 与 Retry-After 头），
//    内置后台定时 GC 协程自动回收长期闲置的限流桶，防止内存持续增长泄漏。
// ===============================================================================

package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// MaxBodySize returns a middleware that limits the maximum allowed request body size.
//
// MaxBodySize 返回限制请求体最大字节数的 Gin 中间件，防御大包拒绝服务攻击。
//
// 执行逻辑：
// 1. 若 maxBytes <= 0 则兜底为默认 32 MiB；
// 2. 使用 http.MaxBytesReader 包装请求体 Body；
// 3. 后续 Handler 读取 Body 超出限制时将产生错误，返回 413 Payload Too Large。
func MaxBodySize(maxBytes int64) gin.HandlerFunc {
	if maxBytes <= 0 {
		maxBytes = 32 << 20 // 默认 32 MiB
	}
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// MaxConcurrent returns a middleware that caps the total in-flight requests on the server.
//
// MaxConcurrent 返回限制服务器最大在途并发处理请求数的中间件，防止瞬时峰值流量耗尽系统资源。
//
// 执行逻辑：
// 1. 初始化容量为 limit 的空结构体 channel 信号量；
// 2. 使用 non-blocking select 尝试向 sem 投递令牌：
//   - 成功获取令牌：通过 defer 释放令牌，并放行 c.Next()；
//   - 队列已满（default 分支）：立即调用 AbortWithError 输出 503 UPSTREAM_UNAVAILABLE 错误信封。
func MaxConcurrent(limit int) gin.HandlerFunc {
	if limit <= 0 {
		limit = 1000 // 默认最多 1000 并发
	}
	sem := make(chan struct{}, limit)

	return func(c *gin.Context) {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
			c.Next()
		default:
			AbortWithError(c, http.StatusServiceUnavailable,
				"UPSTREAM_UNAVAILABLE",
				"Server is overloaded: concurrent request limit reached, please retry later",
				nil,
			)
		}
	}
}

// ──────────────────────────────────────────────
// 32-shard token bucket rate limiter
// ──────────────────────────────────────────────

const numRateLimitShards = 32

type rateLimitBucket struct {
	tokens    float64
	lastCheck time.Time
}

type rateLimitShard struct {
	mu      sync.Mutex
	buckets map[string]*rateLimitBucket
}

// shardedRateLimiter is a high-concurrency token-bucket rate limiter.
type shardedRateLimiter struct {
	shards [numRateLimitShards]*rateLimitShard
	stopCh chan struct{}
}

func newShardedRateLimiter() *shardedRateLimiter {
	limiter := &shardedRateLimiter{
		stopCh: make(chan struct{}),
	}
	for i := 0; i < numRateLimitShards; i++ {
		limiter.shards[i] = &rateLimitShard{
			buckets: make(map[string]*rateLimitBucket),
		}
	}
	// 后台协程定期清理超过 10 分钟未活动的 Bucket，杜绝内存膨胀
	go func() {
		ticker := time.NewTicker(3 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				limiter.cleanup(10 * time.Minute)
			case <-limiter.stopCh:
				return
			}
		}
	}()
	return limiter
}

func (l *shardedRateLimiter) shardFor(key string) *rateLimitShard {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return l.shards[h%numRateLimitShards]
}

func (l *shardedRateLimiter) allow(key string, rps, burst float64) bool {
	shard := l.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	now := time.Now()
	b, ok := shard.buckets[key]
	if !ok {
		b = &rateLimitBucket{tokens: burst, lastCheck: now}
		shard.buckets[key] = b
	}

	elapsed := now.Sub(b.lastCheck).Seconds()
	b.tokens += elapsed * rps
	if b.tokens > burst {
		b.tokens = burst
	}
	b.lastCheck = now

	if b.tokens < 1.0 {
		return false
	}
	b.tokens -= 1.0
	return true
}

func (l *shardedRateLimiter) cleanup(ttl time.Duration) {
	now := time.Now()
	for i := 0; i < numRateLimitShards; i++ {
		shard := l.shards[i]
		shard.mu.Lock()
		for k, b := range shard.buckets {
			if now.Sub(b.lastCheck) > ttl {
				delete(shard.buckets, k)
			}
		}
		shard.mu.Unlock()
	}
}

func (l *shardedRateLimiter) stop() {
	select {
	case <-l.stopCh:
	default:
		close(l.stopCh)
	}
}

// IPRateLimiter is an in-memory per-key token-bucket rate limiter with automatic stale key cleanup.
//
// IPRateLimiter 是基于内存的按 key 分片令牌桶限流器，具备后台自动垃圾回收能力。
// 历史名称为 "IPRateLimiter"，现内部实现复用 32 分片限流器，但 API 保持兼容。
type IPRateLimiter struct {
	rps     float64
	burst   float64
	limiter *shardedRateLimiter
}

// NewIPRateLimiter creates a new rate limiter with background garbage collection.
//
// NewIPRateLimiter 构建新的限流器，并启动后台协程每 5 分钟扫描清理闲置超过 10 分钟的 key 桶。
func NewIPRateLimiter(rps int, burst int) *IPRateLimiter {
	if rps <= 0 {
		rps = 100
	}
	if burst <= 0 {
		burst = rps * 2
	}

	return &IPRateLimiter{
		rps:     float64(rps),
		burst:   float64(burst),
		limiter: newShardedRateLimiter(),
	}
}

// Close stops the background cleanup goroutine.
//
// Close 优雅停止后台垃圾回收定时器协程。
func (l *IPRateLimiter) Close() {
	if l.limiter != nil {
		l.limiter.stop()
	}
}

// Allow checks whether a request from the given key is permitted under the rate limit.
func (l *IPRateLimiter) Allow(key string) bool {
	if l.limiter == nil {
		return true
	}
	return l.limiter.allow(key, l.rps, l.burst)
}

// RateLimit returns a Gin middleware that enforces per-key token bucket rate limiting.
// The default key is the client IP.
//
// RateLimit 返回基于客户端 IP 令牌桶限流的 Gin 中间件。
func RateLimit(rps int, burst int) gin.HandlerFunc {
	return RateLimitWithKeyFunc(rps, burst, defaultRateLimitKey)
}

// RateLimitWithKeyFunc returns a Gin middleware that enforces token bucket rate limiting
// using a caller-provided key function. If keyFunc returns an empty string, the client IP is used.
func RateLimitWithKeyFunc(rps int, burst int, keyFunc func(*gin.Context) string) gin.HandlerFunc {
	limiter := NewIPRateLimiter(rps, burst)

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/health" || path == "/api/health" {
			c.Next()
			return
		}

		key := keyFunc(c)
		if key == "" {
			key = c.ClientIP()
		}
		if !limiter.Allow(key) {
			c.Writer.Header().Set("Retry-After", "1")
			c.Writer.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", rps))
			AbortWithError(c, http.StatusTooManyRequests,
				"RATE_LIMITED",
				"Too Many Requests: rate limit exceeded, please retry later",
				nil,
			)
			return
		}

		c.Next()
	}
}

func defaultRateLimitKey(c *gin.Context) string {
	return c.ClientIP()
}

// NormalizeRateLimitPath 将路径中的动态 ID 段替换为 :id 占位符，防止高基数路径导致限流桶爆炸。
// 识别两类动态段：纯数字（如 123）和 UUID 格式（如 550e8400-e29b-...）。
func NormalizeRateLimitPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		if IsAllDigits(part) || IsUUIDFormat(part) {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

// IsAllDigits reports whether s consists solely of ASCII digits.
func IsAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// IsUUIDFormat reports whether s matches the 8-4-4-4-12 UUID pattern.
func IsUUIDFormat(s string) bool {
	// UUID: 8-4-4-4-12 hex digits
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}
