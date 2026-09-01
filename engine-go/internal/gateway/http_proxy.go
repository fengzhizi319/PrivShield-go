// Package gateway 提供 HTTP 反向代理。
//
// 集成 GatewayMetrics 实时上报 InFlight/EWMA/熔断器状态到 Prometheus。
// 错误响应使用 pkg/middleware 统一信封格式。
package gateway

import (
	"fmt"
	"net/http"
	"time"

	"github.com/fengzhizi319/PrivShield/engine-go/internal/observability"
	"github.com/fengzhizi319/PrivShield/pkg/middleware"
	"github.com/gin-gonic/gin"
)

// NewHTTPProxyHandler 创建 HTTP 反向代理处理器。
// metrics 可为 nil，为 nil 时不上报 Prometheus 指标。
func NewHTTPProxyHandler(lb *LoadBalancer, metrics *observability.GatewayMetrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		node := lb.SelectNode()
		if node == nil {
			middleware.AbortWithError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "无可用后端节点", "all backends exhausted")
			return
		}

		// 检查熔断器
		if !node.CB.Allow() {
			if metrics != nil {
				metrics.SetCircuitBreakerState(node.Address, node.CB.StateString())
			}
			middleware.AbortWithError(c, http.StatusServiceUnavailable, "CIRCUIT_OPEN", fmt.Sprintf("后端 %s 熔断器开启", node.Address), "circuit breaker is open")
			return
		}

		node.IncrementInFlight()
		defer node.DecrementInFlight()

		// 上报 InFlight 指标
		if metrics != nil {
			metrics.SetBackendInFlight(node.Address, node.Address, float64(node.InFlight.Load()))
		}

		// 获取节点内聚的反向代理（内置 BufferPool 与共享长连接池）
		proxy, err := node.ReverseProxy(metrics)
		if err != nil {
			node.CB.RecordFailure()
			if metrics != nil {
				metrics.SetCircuitBreakerState(node.Address, node.CB.StateString())
			}
			middleware.AbortWithError(c, http.StatusInternalServerError, "PROXY_ERROR", "后端代理创建失败", err.Error())
			return
		}

		// 记录延迟
		start := time.Now()
		proxy.ServeHTTP(c.Writer, c.Request)
		latency := time.Since(start)

		// 更新 EWMA（alpha=0.3）
		node.UpdateEWMA(latency, 0.3)

		// 根据响应状态更新熔断器
		if c.Writer.Status() < 500 {
			node.CB.RecordSuccess()
		} else {
			node.CB.RecordFailure()
		}

		// 上报 Prometheus 指标
		if metrics != nil {
			metrics.SetBackendEWMALatency(node.Address, float64(latency.Seconds()))
			metrics.SetCircuitBreakerState(node.Address, node.CB.StateString())
			metrics.SetBackendInFlight(node.Address, node.Address, float64(node.InFlight.Load()))
			metrics.RecordForwarded(node.Address, c.Writer.Status())
		}
	}
}

// NewHealthCheckHandler 创建健康检查代理
func NewHealthCheckHandler(lb *LoadBalancer) gin.HandlerFunc {
	return func(c *gin.Context) {
		nodes := lb.Nodes()
		results := make([]gin.H, 0, len(nodes))
		for _, n := range nodes {
			state := n.CB.StateString()
			results = append(results, gin.H{
				"address":   n.Address,
				"in_flight": n.InFlight.Load(),
				"ewma_ms":   n.GetEWMA() / 1e6,
				"cb_state":  state,
			})
		}
		c.JSON(http.StatusOK, gin.H{"backends": results})
	}
}
