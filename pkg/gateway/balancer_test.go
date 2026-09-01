package gateway

import (
	"math"
	"sync"
	"testing"
	"time"
)

// ──────────────────────────────────────────────
// 熔断器测试
// ──────────────────────────────────────────────

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)

	// 初始状态 Closed，允许通过
	for i := 0; i < 3; i++ {
		if !cb.Allow() {
			t.Fatal("should allow in Closed state")
		}
	}

	// 连续失败 3 次 → 触发熔断
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}
	if cb.State() != CBOpen {
		t.Fatalf("state = %d, want Open (2)", cb.State())
	}

	// 熔断后不允许通过
	if cb.Allow() {
		t.Fatal("should not allow in Open state")
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// 触发熔断
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CBOpen {
		t.Fatal("should be Open")
	}

	// 等待冷却期
	time.Sleep(60 * time.Millisecond)

	// 冷却后自动进入 HalfOpen
	if !cb.Allow() {
		t.Fatal("should allow probe in HalfOpen state")
	}
	if cb.State() != CBHalfOpen {
		t.Fatalf("state = %d, want HalfOpen (1)", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToClosed(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// 触发熔断
	cb.RecordFailure()
	cb.RecordFailure()

	// 等待冷却
	time.Sleep(60 * time.Millisecond)

	// HalfOpen 探测成功 3 次 → 恢复 Closed
	for i := 0; i < 3; i++ {
		cb.Allow()
		cb.RecordSuccess()
	}
	if cb.State() != CBClosed {
		t.Fatalf("state = %d, want Closed (0)", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToOpen(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// 触发熔断
	cb.RecordFailure()
	cb.RecordFailure()

	// 等待冷却
	time.Sleep(60 * time.Millisecond)

	// HalfOpen 探测失败 → 重新熔断
	cb.Allow()
	cb.RecordFailure()
	if cb.State() != CBOpen {
		t.Fatalf("state = %d, want Open (2) after HalfOpen failure", cb.State())
	}
}

// ──────────────────────────────────────────────
// P2C 策略测试
// ──────────────────────────────────────────────

func TestSelectNode_P2C_ReturnsAvailableNode(t *testing.T) {
	lb := NewLoadBalancer([]string{"a:8080", "b:8080", "c:8080"}, "p2c")
	for i := 0; i < 100; i++ {
		node := lb.SelectNode()
		if node == nil {
			t.Fatal("P2C should return a node")
		}
	}
}

func TestSelectNode_P2C_PrefersLowerLoad(t *testing.T) {
	lb := NewLoadBalancer([]string{"a:8080", "b:8080"}, "p2c")
	// 给 a 增加在途请求
	lb.nodes[0].IncrementInFlight()
	lb.nodes[0].IncrementInFlight()
	lb.nodes[0].IncrementInFlight()

	// P2C 应选择负载较低的 b
	selectedB := 0
	for i := 0; i < 100; i++ {
		node := lb.SelectNode()
		if node.Address == "b:8080" {
			selectedB++
		}
	}
	if selectedB < 70 {
		t.Errorf("P2C should prefer lower-load node, selected b %d/100 times", selectedB)
	}
}

// ──────────────────────────────────────────────
// RoundRobin 策略测试
// ──────────────────────────────────────────────

func TestSelectNode_RoundRobin_CyclesEvenly(t *testing.T) {
	addrs := []string{"a:8080", "b:8080", "c:8080"}
	lb := NewLoadBalancer(addrs, "round_robin")

	counts := map[string]int{}
	for i := 0; i < 300; i++ {
		node := lb.SelectNode()
		counts[node.Address]++
	}

	// 每个节点应被选中 100 次
	for _, addr := range addrs {
		if counts[addr] != 100 {
			t.Errorf("round_robin: %s selected %d times, want 100", addr, counts[addr])
		}
	}
}

// ──────────────────────────────────────────────
// LeastConn 策略测试
// ──────────────────────────────────────────────

func TestSelectNode_LeastConn_SelectsLowest(t *testing.T) {
	lb := NewLoadBalancer([]string{"a:8080", "b:8080", "c:8080"}, "least_conn")
	lb.nodes[0].IncrementInFlight()
	lb.nodes[0].IncrementInFlight()
	lb.nodes[1].IncrementInFlight()
	// c 是 0，应该被选中

	node := lb.SelectNode()
	if node.Address != "c:8080" {
		t.Errorf("least_conn should select node with fewest connections, got %s", node.Address)
	}
}

// ──────────────────────────────────────────────
// WeightedRoundRobin (Nginx SWRR) 策略测试
// ──────────────────────────────────────────────

func TestSelectNode_WeightedRR_ExactDistribution(t *testing.T) {
	addrs := []string{"a:8080", "b:8080", "c:8080"}
	weights := []int{5, 3, 2}
	lb := NewWeightedLoadBalancer(addrs, weights, "weighted_rr")

	counts := map[string]int{}
	total := 100
	for i := 0; i < total; i++ {
		node := lb.SelectNode()
		counts[node.Address]++
	}

	// SWRR 保证精确比例: 5:3:2 → 50:30:20
	expected := map[string]int{"a:8080": 50, "b:8080": 30, "c:8080": 20}
	for addr, want := range expected {
		if counts[addr] != want {
			t.Errorf("weighted_rr: %s selected %d times, want %d", addr, counts[addr], want)
		}
	}
}

func TestSelectNode_WeightedRR_SmoothDistribution(t *testing.T) {
	addrs := []string{"a:8080", "b:8080"}
	weights := []int{5, 5}
	lb := NewWeightedLoadBalancer(addrs, weights, "weighted_rr")

	// 等权重 SWRR 应完美交替：a,b,a,b,...
	sequence := make([]string, 10)
	for i := range sequence {
		sequence[i] = lb.SelectNode().Address
	}

	// 检查完美交替（不应连续 2 次选中同一节点）
	for i := 0; i < len(sequence)-1; i++ {
		if sequence[i] == sequence[i+1] {
			t.Errorf("equal-weight SWRR should alternate, got consecutive %s at index %d: %v", sequence[i], i, sequence)
		}
	}
}

func TestSelectNode_WeightedRR_EqualWeights(t *testing.T) {
	addrs := []string{"a:8080", "b:8080", "c:8080"}
	weights := []int{1, 1, 1}
	lb := NewWeightedLoadBalancer(addrs, weights, "weighted_rr")

	counts := map[string]int{}
	for i := 0; i < 300; i++ {
		node := lb.SelectNode()
		counts[node.Address]++
	}

	// 等权重 → 均匀分配
	for _, addr := range addrs {
		if counts[addr] != 100 {
			t.Errorf("equal weight: %s selected %d times, want 100", addr, counts[addr])
		}
	}
}

// ──────────────────────────────────────────────
// WeightedRandom 策略测试
// ──────────────────────────────────────────────

func TestSelectNode_WeightedRandom_ApproximateDistribution(t *testing.T) {
	addrs := []string{"a:8080", "b:8080", "c:8080"}
	weights := []int{7, 2, 1}
	lb := NewWeightedLoadBalancer(addrs, weights, "weighted_random")

	counts := map[string]int{}
	total := 10000
	for i := 0; i < total; i++ {
		node := lb.SelectNode()
		counts[node.Address]++
	}

	// 加权随机：期望比例 70%:20%:10%，允许 ±3% 偏差
	ratios := map[string]float64{
		"a:8080": float64(counts["a:8080"]) / float64(total),
		"b:8080": float64(counts["b:8080"]) / float64(total),
		"c:8080": float64(counts["c:8080"]) / float64(total),
	}
	expected := map[string]float64{"a:8080": 0.70, "b:8080": 0.20, "c:8080": 0.10}
	for addr, want := range expected {
		if math.Abs(ratios[addr]-want) > 0.03 {
			t.Errorf("weighted_random: %s ratio = %.3f, want ~%.2f", addr, ratios[addr], want)
		}
	}
}

func TestSelectNode_WeightedRandom_SingleNode(t *testing.T) {
	lb := NewWeightedLoadBalancer([]string{"a:8080"}, []int{5}, "weighted_random")
	for i := 0; i < 10; i++ {
		node := lb.SelectNode()
		if node.Address != "a:8080" {
			t.Fatal("single node should always be selected")
		}
	}
}

// ──────────────────────────────────────────────
// 熔断器 + 策略集成测试
// ──────────────────────────────────────────────

func TestSelectNode_CircuitBreaker_SkipsOpenNode(t *testing.T) {
	addrs := []string{"a:8080", "b:8080"}
	weights := []int{5, 5}
	lb := NewWeightedLoadBalancer(addrs, weights, "weighted_rr")

	// 熔断节点 a
	forceOpenCB(lb.nodes[0])

	// 所有请求应路由到 b
	for i := 0; i < 10; i++ {
		node := lb.SelectNode()
		if node.Address != "b:8080" {
			t.Errorf("should skip open-circuit node, got %s", node.Address)
		}
	}
}

func TestSelectNode_AllCircuitBreakersOpen(t *testing.T) {
	lb := NewLoadBalancer([]string{"a:8080", "b:8080"}, "round_robin")
	forceOpenCB(lb.nodes[0])
	forceOpenCB(lb.nodes[1])

	// 全部熔断时仍应返回一个节点（避免雪崩）
	node := lb.SelectNode()
	if node == nil {
		t.Fatal("should return a node even when all CBs are open")
	}
}

func TestSelectNode_P2C_WithEWMA(t *testing.T) {
	lb := NewLoadBalancer([]string{"a:8080", "b:8080"}, "p2c")

	// 节点 a 延迟高
	lb.nodes[0].UpdateEWMA(100*time.Millisecond, 1.0)
	// 节点 b 延迟低
	lb.nodes[1].UpdateEWMA(5*time.Millisecond, 1.0)

	selectedB := 0
	for i := 0; i < 100; i++ {
		node := lb.SelectNode()
		if node.Address == "b:8080" {
			selectedB++
		}
	}
	if selectedB < 70 {
		t.Errorf("P2C with EWMA should prefer low-latency node, selected b %d/100", selectedB)
	}
}

// ──────────────────────────────────────────────
// 默认策略测试
// ──────────────────────────────────────────────

func TestSelectNode_UnknownStrategy_DefaultsToP2C(t *testing.T) {
	lb := NewLoadBalancer([]string{"a:8080", "b:8080"}, "unknown_strategy")
	for i := 0; i < 10; i++ {
		node := lb.SelectNode()
		if node == nil {
			t.Fatal("unknown strategy should default to P2C")
		}
	}
}

// ──────────────────────────────────────────────
// NewWeightedLoadBalancer 构造函数测试
// ──────────────────────────────────────────────

func TestNewWeightedLoadBalancer_DefaultWeight(t *testing.T) {
	// weights 长度不足时，缺失的应默认为 1
	lb := NewWeightedLoadBalancer([]string{"a:8080", "b:8080", "c:8080"}, []int{5}, "weighted_rr")
	if lb.nodes[0].Weight != 5 {
		t.Errorf("node[0].Weight = %d, want 5", lb.nodes[0].Weight)
	}
	if lb.nodes[1].Weight != 1 {
		t.Errorf("node[1].Weight = %d, want 1 (default)", lb.nodes[1].Weight)
	}
	if lb.nodes[2].Weight != 1 {
		t.Errorf("node[2].Weight = %d, want 1 (default)", lb.nodes[2].Weight)
	}
}

func TestNewWeightedLoadBalancer_ZeroWeight_DefaultsToOne(t *testing.T) {
	lb := NewWeightedLoadBalancer([]string{"a:8080"}, []int{0}, "weighted_rr")
	if lb.nodes[0].Weight != 1 {
		t.Errorf("zero weight should default to 1, got %d", lb.nodes[0].Weight)
	}
}

// ──────────────────────────────────────────────
// InFlight 计数测试
// ──────────────────────────────────────────────

func TestInFlight_IncrementDecrement(t *testing.T) {
	lb := NewLoadBalancer([]string{"a:8080"}, "p2c")
	node := lb.nodes[0]

	node.IncrementInFlight()
	node.IncrementInFlight()
	if node.InFlight.Load() != 2 {
		t.Fatalf("InFlight = %d, want 2", node.InFlight.Load())
	}

	node.DecrementInFlight()
	if node.InFlight.Load() != 1 {
		t.Fatalf("InFlight = %d, want 1", node.InFlight.Load())
	}

	// 不应减到负数
	node.DecrementInFlight()
	node.DecrementInFlight()
	if node.InFlight.Load() != 0 {
		t.Fatalf("InFlight = %d, want 0 (floor)", node.InFlight.Load())
	}
}

// ──────────────────────────────────────────────
// 辅助函数
// ──────────────────────────────────────────────

// forceOpenCB 强制打开节点熔断器
func forceOpenCB(node *BackendNode) {
	node.CB.mu.Lock()
	defer node.CB.mu.Unlock()
	node.CB.state = CBOpen
	node.CB.lastFailure = time.Now()
	node.CB.cooldown = 1 * time.Hour // 很长冷却期确保保持 Open
}

// ──────────────────────────────────────────────
// P3: LB 原子化 InFlight 并发安全测试
// ──────────────────────────────────────────────

func TestInFlight_ConcurrentSafety(t *testing.T) {
	lb := NewLoadBalancer([]string{"a:8080"}, "p2c")
	node := lb.nodes[0]

	var wg sync.WaitGroup
	// 100 个 goroutine 并发增减
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			node.IncrementInFlight()
			node.IncrementInFlight()
			node.DecrementInFlight()
		}()
	}
	wg.Wait()

	// 每个 goroutine 净增 1，最终应为 100
	if got := node.InFlight.Load(); got != 100 {
		t.Errorf("InFlight = %d, want 100", got)
	}
}

func TestInFlight_NeverNegative(t *testing.T) {
	node := &BackendNode{Address: "test"}

	var wg sync.WaitGroup
	// 1000 个 goroutine 并发减少（从 0 开始）
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			node.DecrementInFlight()
		}()
	}
	wg.Wait()

	if got := node.InFlight.Load(); got != 0 {
		t.Errorf("InFlight = %d, should never go negative, want 0", got)
	}
}

// ──────────────────────────────────────────────
// P3: RoundRobin 无锁并发测试
// ──────────────────────────────────────────────

func TestRoundRobin_ConcurrentDistribution(t *testing.T) {
	addrs := []string{"a:8080", "b:8080", "c:8080"}
	lb := NewLoadBalancer(addrs, "round_robin")

	counts := make(map[string]int)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// 30 个 goroutine 各选 10 次 = 300 次
	for g := 0; g < 30; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				node := lb.SelectNode()
				mu.Lock()
				counts[node.Address]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	total := 0
	for _, c := range counts {
		total += c
	}
	if total != 300 {
		t.Errorf("total selections = %d, want 300", total)
	}
}

// ──────────────────────────────────────────────
// P1-9: SelectNode 无全局锁并发测试（weighted_rr 策略）
// ──────────────────────────────────────────────

func TestSelectNode_WeightedRR_ConcurrentNoLock(t *testing.T) {
	lb := NewWeightedLoadBalancer(
		[]string{"a:1", "b:2", "c:3"},
		[]int{1, 2, 3},
		"weighted_rr",
	)
	var wg sync.WaitGroup
	counts := make([]int, 3)
	var mu sync.Mutex
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				node := lb.SelectNode()
				if node == nil {
					t.Error("SelectNode returned nil")
					return
				}
				mu.Lock()
				switch node.Address {
				case "a:1":
					counts[0]++
				case "b:2":
					counts[1]++
				case "c:3":
					counts[2]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	total := 0
	for _, c := range counts {
		total += c
	}
	if total != 3000 {
		t.Errorf("total selections = %d, want 3000", total)
	}
	// 权重 1:2:3 应大致按比例分配（允许 20% 偏差）
	if counts[0] > counts[2] {
		t.Errorf("weight ordering violated: a(%d) > c(%d)", counts[0], counts[2])
	}
}
