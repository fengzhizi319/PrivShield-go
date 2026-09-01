// Package gateway provides L7 adaptive load balancing primitives.
//
// 从 engine-go/internal/gateway/balancer.go 下沉，供 engine-go、services 与 console 复用。
// 实现 P2C-EWMA 调度、三态熔断器以及 round-robin / least-conn / weighted 策略。
package gateway

import (
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/circuitbreaker"
)

// ──────────────────────────────────────────────
// 后端节点
// ──────────────────────────────────────────────

// BackendNode 后端节点
type BackendNode struct {
	Address       string
	Weight        int
	currentWeight atomic.Int32            // Nginx SWRR 当前权重（原子操作）
	InFlight      atomic.Int64            // 当前在途请求数（原子操作，与 EWMA 锁分离）
	EWMA          float64                 // 指数移动加权平均延迟
	CB            *circuitbreaker.Breaker // 熔断器
	eWMAMu        sync.Mutex              // 仅保护 EWMA 字段

	// 反向代理实例与节点生命周期绑定：随节点惰性创建、随节点回收即释放。
	// 取代早期「全局 sync.Map 缓存 + 后台 TTL 清理 goroutine」方案——
	// 后端节点集合在启动时静态确定，无动态伸缩抖动，无需额外常驻协程与 TTL 扫描。
	// 构建逻辑见 http_proxy.go 的 BackendNode.ReverseProxy。
	proxyOnce sync.Once
	proxy     *httputil.ReverseProxy
	proxyErr  error
}

// MetricsRecorder is the subset of metrics operations used by the gateway proxy.
// It is typically implemented by engine-go/internal/observability.GatewayMetrics.
type MetricsRecorder interface {
	SetBackendInFlight(nodeID, addr string, count float64)
	SetBackendEWMALatency(nodeID string, latencySec float64)
	SetCircuitBreakerState(nodeID, state string)
	RecordForwarded(nodeID string, status int)
}

// byteBufferPool implements httputil.BufferPool, reusing 32KB read/write buffers.
type byteBufferPool struct {
	pool sync.Pool
}

func newByteBufferPool() *byteBufferPool {
	return &byteBufferPool{
		pool: sync.Pool{
			New: func() any {
				b := make([]byte, 32*1024)
				return &b
			},
		},
	}
}

func (p *byteBufferPool) Get() []byte {
	return *p.pool.Get().(*[]byte)
}

func (p *byteBufferPool) Put(b []byte) {
	if cap(b) >= 32*1024 {
		p.pool.Put(&b)
	}
}

var (
	globalBufferPool = newByteBufferPool()
	sharedTransport  = &http.Transport{
		MaxIdleConns:        2048,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	}
)

// ReverseProxy returns a reverse proxy bound to this backend node.
//
// The proxy is lazily built once and cached for the node's lifetime. All
// proxies share the same underlying transport connection pool.
func (n *BackendNode) ReverseProxy(metrics MetricsRecorder) (*httputil.ReverseProxy, error) {
	n.proxyOnce.Do(func() {
		target, err := url.Parse(fmt.Sprintf("http://%s", n.Address))
		if err != nil {
			n.proxyErr = err
			return
		}

		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.Transport = sharedTransport
		proxy.BufferPool = globalBufferPool
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			n.CB.RecordFailure()
			if metrics != nil {
				metrics.SetCircuitBreakerState(n.Address, n.CB.StateString())
				metrics.RecordForwarded(n.Address, http.StatusBadGateway)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, `{"code":"BAD_GATEWAY","message":"后端 %s 不可达","detail":"%s","trace_id":"","timestamp":"%s"}`, n.Address, err.Error(), time.Now().UTC().Format(time.RFC3339Nano))
		}
		n.proxy = proxy
	})
	return n.proxy, n.proxyErr
}

// ──────────────────────────────────────────────
// 负载均衡器
// ──────────────────────────────────────────────

// LoadBalancer 自适应负载均衡器
type LoadBalancer struct {
	nodes    []*BackendNode
	strategy string       // "p2c" | "round_robin" | "least_conn" | "weighted_rr" | "weighted_random"
	rrIndex  atomic.Int32 // round-robin 原子计数器（无锁化）
}

// NewLoadBalancer 创建负载均衡器
func NewLoadBalancer(addresses []string, strategy string) *LoadBalancer {
	nodes := make([]*BackendNode, len(addresses))
	for i, addr := range addresses {
		nodes[i] = &BackendNode{
			Address: addr,
			Weight:  1,
			CB:      circuitbreaker.NewBreaker(5, 30*time.Second),
		}
	}
	return &LoadBalancer{
		nodes:    nodes,
		strategy: strategy,
	}
}

// NewWeightedLoadBalancer 创建支持权重的负载均衡器。
// weights 与 addresses 一一对应，值越大分配流量越多。
func NewWeightedLoadBalancer(addresses []string, weights []int, strategy string) *LoadBalancer {
	nodes := make([]*BackendNode, len(addresses))
	for i, addr := range addresses {
		w := 1
		if i < len(weights) && weights[i] > 0 {
			w = weights[i]
		}
		nodes[i] = &BackendNode{
			Address: addr,
			Weight:  w,
			CB:      circuitbreaker.NewBreaker(5, 30*time.Second),
		}
	}
	return &LoadBalancer{
		nodes:    nodes,
		strategy: strategy,
	}
}

// SelectNode 选择一个后端节点（无全局锁，各策略独立无锁化）
func (lb *LoadBalancer) SelectNode() *BackendNode {
	switch lb.strategy {
	case "p2c":
		return lb.selectP2C()
	case "round_robin":
		return lb.selectRoundRobin()
	case "least_conn":
		return lb.selectLeastConn()
	case "weighted_rr":
		return lb.selectWeightedRoundRobin()
	case "weighted_random":
		return lb.selectWeightedRandom()
	default:
		return lb.selectP2C()
	}
}

// selectP2C 幂律双选 (Power of Two Choices) + EWMA 延迟
func (lb *LoadBalancer) selectP2C() *BackendNode {
	if len(lb.nodes) == 0 {
		return nil
	}

	// 收集可用节点
	available := make([]*BackendNode, 0, len(lb.nodes))
	for _, n := range lb.nodes {
		if n.CB.Allow() {
			available = append(available, n)
		}
	}
	if len(available) == 0 {
		// 全部熔断，返回第一个节点供调用方执行熔断降级与指标上报
		return lb.nodes[0]
	}
	if len(available) == 1 {
		return available[0]
	}

	// 随机选两个
	i := rand.IntN(len(available))
	j := rand.IntN(len(available))
	for j == i {
		j = rand.IntN(len(available))
	}

	a, b := available[i], available[j]
	// 选择负载较低的（在途请求 * EWMA 延迟）
	scoreA := float64(a.InFlight.Load()+1) * math.Max(a.GetEWMA(), 0.001)
	scoreB := float64(b.InFlight.Load()+1) * math.Max(b.GetEWMA(), 0.001)

	if scoreA <= scoreB {
		return a
	}
	return b
}

// selectRoundRobin 无锁轮询（atomic fetch-and-add）
func (lb *LoadBalancer) selectRoundRobin() *BackendNode {
	available := make([]*BackendNode, 0, len(lb.nodes))
	for _, n := range lb.nodes {
		if n.CB.Allow() {
			available = append(available, n)
		}
	}
	if len(available) == 0 {
		return lb.nodes[0]
	}
	idx := int(lb.rrIndex.Add(1)-1) % len(available)
	return available[idx]
}

// selectLeastConn 最少连接（原子读取 InFlight）
func (lb *LoadBalancer) selectLeastConn() *BackendNode {
	var best *BackendNode
	bestInFlight := int64(math.MaxInt64)
	for _, n := range lb.nodes {
		if !n.CB.Allow() {
			continue
		}
		inFlight := n.InFlight.Load()
		if inFlight < bestInFlight {
			bestInFlight = inFlight
			best = n
		}
	}
	if best == nil {
		return lb.nodes[0]
	}
	return best
}

// selectWeightedRoundRobin Nginx 平滑加权轮询 (SWRR)。
//
// 算法：每轮所有节点 currentWeight += weight；
// 选取 currentWeight 最大的节点；
// 被选中节点 currentWeight -= totalWeight。
// 保证分配比例精确且分布均匀（不会出现连续集中分配到同一节点）。
func (lb *LoadBalancer) selectWeightedRoundRobin() *BackendNode {
	available := make([]*BackendNode, 0, len(lb.nodes))
	for _, n := range lb.nodes {
		if n.CB.Allow() {
			available = append(available, n)
		}
	}
	if len(available) == 0 {
		return lb.nodes[0]
	}

	totalWeight := int32(0)
	var best *BackendNode
	bestCW := int32(-1 << 31) // min int32
	for _, n := range available {
		cw := n.currentWeight.Add(int32(n.Weight))
		totalWeight += int32(n.Weight)
		if cw > bestCW {
			bestCW = cw
			best = n
		}
	}
	best.currentWeight.Add(-totalWeight)
	return best
}

// selectWeightedRandom 加权随机选择。
//
// 每个节点的选中概率与其 Weight 成正比。
func (lb *LoadBalancer) selectWeightedRandom() *BackendNode {
	available := make([]*BackendNode, 0, len(lb.nodes))
	for _, n := range lb.nodes {
		if n.CB.Allow() {
			available = append(available, n)
		}
	}
	if len(available) == 0 {
		return lb.nodes[0]
	}
	if len(available) == 1 {
		return available[0]
	}

	totalWeight := 0
	for _, n := range available {
		totalWeight += n.Weight
	}
	r := rand.IntN(totalWeight)
	cumulative := 0
	for _, n := range available {
		cumulative += n.Weight
		if r < cumulative {
			return n
		}
	}
	return available[len(available)-1]
}

// GetEWMA 安全读取节点 EWMA 延迟（修复 selectP2C/HealthCheck 无锁读数据竞争）
func (n *BackendNode) GetEWMA() float64 {
	n.eWMAMu.Lock()
	defer n.eWMAMu.Unlock()
	return n.EWMA
}

// UpdateEWMA 更新节点 EWMA 延迟（独立 eWMAMu，不与 InFlight 竞争）
func (n *BackendNode) UpdateEWMA(latency time.Duration, alpha float64) {
	n.eWMAMu.Lock()
	defer n.eWMAMu.Unlock()
	n.EWMA = alpha*float64(latency) + (1-alpha)*n.EWMA
}

// IncrementInFlight 增加在途请求数（原子操作）
func (n *BackendNode) IncrementInFlight() {
	n.InFlight.Add(1)
}

// DecrementInFlight 减少在途请求数（原子操作）
func (n *BackendNode) DecrementInFlight() {
	for {
		old := n.InFlight.Load()
		if old <= 0 {
			return
		}
		if n.InFlight.CompareAndSwap(old, old-1) {
			return
		}
	}
}

// Nodes 返回所有节点
func (lb *LoadBalancer) Nodes() []*BackendNode {
	return lb.nodes
}
