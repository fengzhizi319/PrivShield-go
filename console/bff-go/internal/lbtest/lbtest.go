// Package lbtest implements load-balancing test strategy dispatch and statistics logic.
// Package lbtest 实现负载均衡测试的策略分发与统计逻辑。
//
// The Go backend's /api/lb_test endpoint dispatches probe requests to multiple agent
// backend addresses configured by the user, using strategies (round_robin / random /
// least_connections), and aggregates per-node hit counts, success/failure counts,
// and latency distribution.
// 控制台 Go 后端的 /api/lb_test 端点把探测请求按策略（round_robin / random /
// least_connections）分发到用户配置的多个 agent 后端地址，并统计各节点的
// 命中数、成功/失败数与延迟分布。
// The *http.Client used for probing is injectable, allowing tests to point at
// httptest-spawned fake backends.
// 探测用的 *http.Client 可注入，便于测试时指向 httptest 起立的假后端。
package lbtest

import (
	// bytes：用于从 JSON body 创建 io.Reader
	// bytes: creates io.Reader from JSON body
	"bytes"
	// context：用于传递请求上下文（超时/取消）
	// context: passes request context (timeout/cancellation)
	"context"
	// encoding/json：用于探测请求体的 JSON 序列化
	// encoding/json: JSON serialization for probe request body
	"encoding/json"
	// fmt：用于格式化错误信息
	// fmt: formats error messages
	"fmt"
	// io：用于丢弃探测响应体（io.Discard）
	// io: discards probe response body (io.Discard)
	"io"
	// math：用于四舍五入延迟统计值
	// math: rounds latency statistics
	"math"
	// math/rand：用于 random 策略的随机数生成
	// math/rand: random number generation for random strategy
	"math/rand"
	// net/http：用于发送 HTTP 探测请求
	// net/http: sends HTTP probe requests
	"net/http"
	// net/url：用于解析和校验探测目标 URL
	// net/url: parses and validates probe target URLs
	"net/url"
	// strings：用于 URL 拼接与 host 比较
	// strings: URL concatenation and host comparison
	"strings"
	// sync：用于并发探测的 WaitGroup 同步
	// sync: WaitGroup synchronization for concurrent probes
	"sync"
	// time：用于计算探测延迟与超时配置
	// time: calculates probe latency and timeout configuration
	"time"

	// models：与前端共享的 JSON 数据结构（LbTestRequest/LbTestResponse）
	// models: shared JSON data structures with frontend (LbTestRequest/LbTestResponse)
	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/models"
)

// Supported dispatch strategy constants.
// 支持的三种分发策略常量。
const (
	// StrategyRoundRobin: sequential rotation, most even distribution
	// 依次轮询，分发最均匀
	StrategyRoundRobin = "round_robin"
	// StrategyRandom: independent random selection per request
	// 独立随机选择
	StrategyRandom = "random"
	// StrategyLeastConnections: always picks the node with fewest hits so far
	// 每次选当前累计命中最少的节点
	StrategyLeastConnections = "least_connections"
)

// ValidateURL validates a load-balancing probe target URL for legitimacy (SSRF protection).
// ValidateURL 校验负载均衡探测目标 URL 的合法性（SSRF 防护）。
//
//   - scheme must be http/https (blocks file:// / gopher:// etc.)
//     scheme 必须为 http/https（拦截 file:// / gopher:// 等）；
//   - when allowedHosts is non-empty, host must match the allowlist
//     allowedHosts 非空时，host 必须命中白名单。
//
// Note: lb_test is designed to probe user-specified addresses (including local 127.0.0.1),
// so private/loopback IPs are not blocked; use allowedHosts allowlist for production hardening.
// 说明：lb_test 的设计目的就是探测用户指定地址（含本地 127.0.0.1），
// 故不屏蔽私有/回环 IP；如需生产收紧，通过 allowedHosts 白名单约束。
func ValidateURL(rawURL string, allowedHosts []string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("探测地址解析失败: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("不支持的探测地址 scheme '%s'，仅允许 http/https", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("探测地址缺少 host: %s", rawURL)
	}
	if len(allowedHosts) > 0 {
		host := strings.ToLower(parsed.Hostname())
		for _, h := range allowedHosts {
			if strings.EqualFold(strings.TrimSpace(h), host) {
				return nil
			}
		}
		return fmt.Errorf("探测地址 host '%s' 不在白名单内", parsed.Hostname())
	}
	return nil
}

// ValidateBackends validates all backend node URLs; returns error on first invalid one.
// ValidateBackends 逐个校验后端节点 URL，任一非法即返回错误。
func ValidateBackends(backends []models.LbBackend, allowedHosts []string) error {
	for _, b := range backends {
		if err := ValidateURL(b.URL, allowedHosts); err != nil {
			return err
		}
	}
	return nil
}

// PickBackends generates a backend index sequence for n probe requests according to strategy.
// PickBackends 按策略生成 n 个探测请求对应的后端下标序列。
//
//   - round_robin: sequential rotation, most even distribution
//     依次轮询，分发最均匀；
//   - random: independent random selection
//     独立随机选择；
//   - least_connections: picks the node with the fewest active probes
//     每次选择当前活动探测数最少的节点。
func PickBackends(strategy string, n, numBackends int) ([]int, error) {
	if numBackends <= 0 {
		return nil, nil
	}
	switch strategy {
	case StrategyRoundRobin:
		seq := make([]int, n)
		for i := range seq {
			seq[i] = i % numBackends
		}
		return seq, nil
	case StrategyRandom:
		seq := make([]int, n)
		for i := range seq {
			seq[i] = rand.Intn(numBackends)
		}
		return seq, nil
	case StrategyLeastConnections:
		counts := make([]int, numBackends)
		seq := make([]int, 0, n)
		for i := 0; i < n; i++ {
			idx := 0
			for j := 1; j < numBackends; j++ {
				if counts[j] < counts[idx] {
					idx = j
				}
			}
			counts[idx]++
			seq = append(seq, idx)
		}
		return seq, nil
	default:
		return nil, fmt.Errorf("不支持的策略 '%s'，可选: round_robin/random/least_connections", strategy)
	}
}

// Run executes load-balancing probes and aggregates per-node hit and latency statistics.
// Run 执行负载均衡探测并统计各节点命中与延迟。
//
// client is injectable (pass a client pointing at httptest server in tests);
// nil uses a default client with 10s timeout.
// client 可注入（测试时传入指向 httptest 服务器的客户端），为 nil 时使用
// 带 10s 超时的默认客户端。
// NumRequests/Strategy default to 10/round_robin when unset.
// NumRequests/Strategy 缺省时分别取 10/round_robin。
func Run(ctx context.Context, req models.LbTestRequest, client *http.Client) (models.LbTestResponse, error) {
	if len(req.Backends) == 0 {
		return models.LbTestResponse{}, fmt.Errorf("backends 不能为空")
	}
	if req.NumRequests <= 0 {
		req.NumRequests = 10
	}
	// 上限固定为 1000，与 REST 接口约定保持一致，
	// 防止探测端点被用作请求放大攻击。
	if req.NumRequests > 1000 {
		req.NumRequests = 1000
	}
	if req.Strategy == "" {
		req.Strategy = StrategyRoundRobin
	}
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			// 不跟随重定向：避免重定向被用于绕过限制 / 放大 SSRF。
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	var seq []int
	if req.Strategy != StrategyLeastConnections {
		var err error
		seq, err = PickBackends(req.Strategy, req.NumRequests, len(req.Backends))
		if err != nil {
			return models.LbTestResponse{}, err
		}
	}
	probePath := req.ProbePath
	if probePath == "" {
		probePath = "/health"
	}

	type probeResult struct {
		idx     int
		latency float64
		ok      bool
	}

	overallStart := time.Now()
	results := make([]probeResult, len(seq))
	var wg sync.WaitGroup
	active := make([]int, len(req.Backends))
	var pickMu sync.Mutex
	launch := func(i, idx int) {
		wg.Add(1)
		go func(i, idx int) {
			defer wg.Done()
			backend := req.Backends[idx]
			url := strings.TrimRight(backend.URL, "/") + probePath
			start := time.Now()
			ok := probe(ctx, client, url, req.ProbeBody)
			results[i] = probeResult{
				idx:     idx,
				latency: float64(time.Since(start).Microseconds()) / 1000.0,
				ok:      ok,
			}
		}(i, idx)
	}
	if req.Strategy == StrategyLeastConnections {
		results = make([]probeResult, req.NumRequests)
		for i := 0; i < req.NumRequests; i++ {
			pickMu.Lock()
			idx := 0
			for j := 1; j < len(active); j++ {
				if active[j] < active[idx] {
					idx = j
				}
			}
			active[idx]++
			pickMu.Unlock()
			wg.Add(1)
			go func(i, idx int) {
				defer wg.Done()
				backend := req.Backends[idx]
				url := strings.TrimRight(backend.URL, "/") + probePath
				start := time.Now()
				ok := probe(ctx, client, url, req.ProbeBody)
				results[i] = probeResult{idx: idx, latency: float64(time.Since(start).Microseconds()) / 1000.0, ok: ok}
				pickMu.Lock()
				active[idx]--
				pickMu.Unlock()
			}(i, idx)
		}
	} else {
		for i, idx := range seq {
			launch(i, idx)
		}
	}
	wg.Wait()
	totalMs := float64(time.Since(overallStart).Microseconds()) / 1000.0

	// 按节点聚合延迟与成功/失败计数。
	latencies := make([][]float64, len(req.Backends))
	success := make([]int, len(req.Backends))
	failed := make([]int, len(req.Backends))
	for _, r := range results {
		latencies[r.idx] = append(latencies[r.idx], r.latency)
		if r.ok {
			success[r.idx]++
		} else {
			failed[r.idx]++
		}
	}

	distribution := make([]models.LbDistItem, 0, len(req.Backends))
	totalSuccess, totalFailed := 0, 0
	for i, backend := range req.Backends {
		lats := latencies[i]
		count := len(lats)
		totalSuccess += success[i]
		totalFailed += failed[i]
		item := models.LbDistItem{
			Name:    backend.Name,
			URL:     backend.URL,
			Count:   count,
			Success: success[i],
			Failed:  failed[i],
		}
		if count > 0 {
			sum, minL, maxL := 0.0, lats[0], lats[0]
			for _, l := range lats {
				sum += l
				if l < minL {
					minL = l
				}
				if l > maxL {
					maxL = l
				}
			}
			item.AvgLatencyMs = round2(sum / float64(count))
			item.MinLatencyMs = round2(minL)
			item.MaxLatencyMs = round2(maxL)
		}
		distribution = append(distribution, item)
	}

	return models.LbTestResponse{
		Strategy:     req.Strategy,
		Total:        req.NumRequests,
		Success:      totalSuccess,
		Failed:       totalFailed,
		DurationMs:   round2(totalMs),
		Distribution: distribution,
	}, nil
}

// probe sends a single probe request to the given url, returns success (status < 400).
// probe 向指定 url 发送一次探测请求，返回是否成功（状态码 < 400）。
//
// Uses POST with JSON body when body is non-empty, otherwise GET.
// body 非空时用 POST 发送该 JSON 体，否则用 GET。
func probe(ctx context.Context, client *http.Client, url string, body json.RawMessage) bool {
	var req *http.Request
	var err error
	if len(body) > 0 {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode < 400
}

// round2 rounds a float64 to 2 decimal places.
// round2 把浮点数四舍五入到小数点后两位。
func round2(f float64) float64 {
	return math.Round(f*100) / 100
}
