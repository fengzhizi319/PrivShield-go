// GatewayMetrics 网关专属 Prometheus 指标，用于观察反向代理自身及其后端节点的运行状态。
//
// 对齐设计文档 §11.1 网关指标规约：
//   - privshield_gateway_backend_in_flight{node_id,backend_addr}
//   - privshield_gateway_backend_ewma_latency_seconds{node_id}
//   - privshield_gateway_circuit_breaker_state{node_id,state}
//   - privshield_gateway_requests_total{node_id,status}
//
// 主要执行流程：
//  1. NewGatewayMetrics 创建独立的 Prometheus Registry 和四组指标，并完成注册；
//  2. Gateway 启动时通过 PrometheusMiddleware 将中间件挂载到 Gin，统计经过网关的请求；
//  3. 反向代理选择后端节点、完成转发并收到响应后，通过 SetBackendInFlight、
//     SetBackendEWMALatency、SetCircuitBreakerState 和 RecordForwarded 更新精确指标；
//  4. 访问网关的 /metrics 端点时，Handler 将当前 Registry 序列化为 Prometheus 文本格式；
//  5. Prometheus 定期抓取 /metrics，结合 node_id、backend_addr 和 status 标签展示各节点负载、
//     延迟、熔断状态及请求结果。
//
// 指标职责：
//   - Gauge 表示当前状态，可随请求完成、延迟变化或熔断状态切换而增减；
//   - Counter 只增不减，适合统计累计转发请求数；
//   - node_id 用于区分逻辑后端，backend_addr 用于定位实际网络地址。
//
// 与 engine-go/cmd/privshield-agent/server_rest.go 的关系：
//   - Agent 的 server_rest.go 负责业务 REST 服务：创建 Gin 引擎、注册业务路由和安全中间件，
//     按 AGENT_REST_HOST/AGENT_REST_PORT 创建监听器，并在 Start() 中通过 Serve/ServeTLS 接收请求；
//   - Gateway 不复用 Agent 的 HTTP Server，而是在 privshield-gateway/main.go 中独立监听
//     ENGINE_GATEWAY_HOST/ENGINE_GATEWAY_PORT（默认 127.0.0.1:8000），通过 ENGINE_GATEWAY_BACKENDS 配置后端 Agent；
//   - 客户端先连接 Gateway，Gateway 再建立或复用到 Agent 的后端连接；因此本文件记录的是
//     Gateway 这一跳及其后端节点的指标，不是 Agent 内部的请求指标；
//   - Gateway 的 /metrics 使用本文件的独立 Registry，Agent 的 /metrics 使用 EngineMetrics，
//     两者应分别配置 Prometheus 抓取地址。
package observability

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// GatewayMetrics 持有网关专属 Prometheus 指标。
type GatewayMetrics struct {
	registry *prometheus.Registry

	// BackendInFlight 各后端节点实时在途并发数。
	BackendInFlight *prometheus.GaugeVec

	// BackendEWMALatency 节点指数移动加权平均延迟（秒）。
	BackendEWMALatency *prometheus.GaugeVec

	// CircuitBreakerState 节点熔断器状态（0=Closed, 1=HalfOpen, 2=Open）。
	CircuitBreakerState *prometheus.GaugeVec

	// RequestsTotal 按 node_id/status 统计网关转发请求数。
	RequestsTotal *prometheus.CounterVec
}

// NewGatewayMetrics 创建并注册网关指标集合。
// 使用独立 Registry，避免把网关指标隐式混入进程的默认 Registry；
// 调用方应通过 Handler 暴露该 Registry，供网关自己的 /metrics 端点使用。
func NewGatewayMetrics() *GatewayMetrics {
	reg := prometheus.NewRegistry()

	m := &GatewayMetrics{
		registry: reg,

		BackendInFlight: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "privshield_gateway_backend_in_flight",
				Help: "Current in-flight requests per backend node.",
			},
			[]string{"node_id", "backend_addr"},
		),

		BackendEWMALatency: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "privshield_gateway_backend_ewma_latency_seconds",
				Help: "Exponentially weighted moving average latency per backend node.",
			},
			[]string{"node_id"},
		),

		CircuitBreakerState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "privshield_gateway_circuit_breaker_state",
				Help: "Circuit breaker state per node (0=closed, 1=half_open, 2=open).",
			},
			[]string{"node_id", "state"},
		),

		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "privshield_gateway_requests_total",
				Help: "Total requests forwarded by the gateway.",
			},
			[]string{"node_id", "status"},
		),
	}

	reg.MustRegister(
		m.BackendInFlight,
		m.BackendEWMALatency,
		m.CircuitBreakerState,
		m.RequestsTotal,
	)

	return m
}

// SetBackendInFlight 更新后端在途请求数。
func (m *GatewayMetrics) SetBackendInFlight(nodeID, addr string, count float64) {
	m.BackendInFlight.WithLabelValues(nodeID, addr).Set(count)
}

// SetBackendEWMALatency 更新后端 EWMA 延迟。
func (m *GatewayMetrics) SetBackendEWMALatency(nodeID string, latencySec float64) {
	m.BackendEWMALatency.WithLabelValues(nodeID).Set(latencySec)
}

// SetCircuitBreakerState 更新熔断器状态。
// state: "closed"=0, "half_open"=1, "open"=2
func (m *GatewayMetrics) SetCircuitBreakerState(nodeID, state string) {
	var val float64
	switch state {
	case "closed":
		val = 0
	case "half_open":
		val = 1
	case "open":
		val = 2
	}
	m.CircuitBreakerState.WithLabelValues(nodeID, state).Set(val)
}

// RecordForwarded 记录一次转发。
func (m *GatewayMetrics) RecordForwarded(nodeID string, status int) {
	m.RequestsTotal.WithLabelValues(nodeID, strconv.Itoa(status)).Inc()
}

// PrometheusMiddleware 返回网关 HTTP 请求指标中间件。
// 中间件先跳过网关本地端点，再调用 c.Next() 让代理或其他 Handler 处理请求，
// 最后根据响应状态码写入 aggregate 节点的总请求计数；后端节点级指标由代理层补充。
func (m *GatewayMetrics) PrometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 仅记录经过网关的请求（不记录 /health、/metrics 等本地端点）
		path := c.Request.URL.Path
		if path == "/health" || path == "/metrics" || path == "/gateway/backends" {
			c.Next()
			return
		}
		c.Next()
		// 记录到默认节点（实际 node_id 由代理层 RecordForwarded 精确上报）
		m.RequestsTotal.WithLabelValues("aggregate", strconv.Itoa(c.Writer.Status())).Inc()
	}
}

// Handler 返回暴露 /metrics 端点的 Gin handler。
// promhttp.HandlerFor 在请求到达时读取当前 Registry，并将指标写入 HTTP 响应；
// Handler() 本身只在路由注册阶段创建适配器，不会在初始化时执行抓取。
func (m *GatewayMetrics) Handler() gin.HandlerFunc {
	h := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}
