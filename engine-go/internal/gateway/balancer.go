package gateway

import (
	"time"

	pgateway "github.com/fengzhizi319/PrivShield/pkg/gateway"
)

// BackendNode 后端节点
type BackendNode = pgateway.BackendNode

// CBState 熔断器状态
type CBState = pgateway.CBState

const (
	CBClosed   = pgateway.CBClosed
	CBHalfOpen = pgateway.CBHalfOpen
	CBOpen     = pgateway.CBOpen
)

// CircuitBreaker 三态熔断器
type CircuitBreaker = pgateway.CircuitBreaker

// LoadBalancer 自适应负载均衡器
type LoadBalancer = pgateway.LoadBalancer

// NewCircuitBreaker 创建熔断器
func NewCircuitBreaker(threshold int, cooldown time.Duration) CircuitBreaker {
	return pgateway.NewCircuitBreaker(threshold, cooldown)
}

// NewLoadBalancer 创建负载均衡器
func NewLoadBalancer(addresses []string, strategy string) *LoadBalancer {
	return pgateway.NewLoadBalancer(addresses, strategy)
}

// NewWeightedLoadBalancer 创建支持权重的负载均衡器。
func NewWeightedLoadBalancer(addresses []string, weights []int, strategy string) *LoadBalancer {
	return pgateway.NewWeightedLoadBalancer(addresses, weights, strategy)
}
