package gateway

import (
	"github.com/fengzhizi319/PrivShield-go/pkg/circuitbreaker"
	pgateway "github.com/fengzhizi319/PrivShield-go/pkg/gateway"
)

// BackendNode 后端节点
type BackendNode = pgateway.BackendNode

// CBState 熔断器状态
type CBState = circuitbreaker.State

const (
	CBClosed   = circuitbreaker.StateClosed
	CBHalfOpen = circuitbreaker.StateHalfOpen
	CBOpen     = circuitbreaker.StateOpen
)

// CircuitBreaker 三态熔断器
type CircuitBreaker = circuitbreaker.Breaker

// CircuitBreakerOptions 熔断器配置选项
type CircuitBreakerOptions = circuitbreaker.Options

// LoadBalancer 自适应负载均衡器
type LoadBalancer = pgateway.LoadBalancer

// NewCircuitBreaker 创建熔断器
func NewCircuitBreaker(opts CircuitBreakerOptions) *CircuitBreaker {
	return circuitbreaker.New(opts)
}

// NewLoadBalancer 创建负载均衡器，可选支持自定义熔断器选项
func NewLoadBalancer(addresses []string, strategy string, cbOpts ...CircuitBreakerOptions) *LoadBalancer {
	return pgateway.NewLoadBalancer(addresses, strategy, cbOpts...)
}

// NewWeightedLoadBalancer 创建支持权重的负载均衡器，可选支持自定义熔断器选项
func NewWeightedLoadBalancer(addresses []string, weights []int, strategy string, cbOpts ...CircuitBreakerOptions) *LoadBalancer {
	return pgateway.NewWeightedLoadBalancer(addresses, weights, strategy, cbOpts...)
}
