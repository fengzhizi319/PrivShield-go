// Package observability provides shared observability primitives for all PrivShield Go modules.
package observability

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// REDMetrics holds the standard RED (Rate / Errors / Duration) Prometheus metrics.
// It uses an independent Registry so multiple modules can coexist without conflicts.
//
// 与 pkg/metrics.Collector 的边界：REDMetrics 度量传输层通用请求指标（由中间件自动埋点），
// Collector 度量业务领域多维指标（由 Handler 显式上报）。二者互补而非替代。
type REDMetrics struct {
	registry *prometheus.Registry

	// RequestsTotal counts requests by protocol, endpoint and status.
	RequestsTotal *prometheus.CounterVec

	// RequestDuration records request latency by protocol and endpoint.
	RequestDuration *prometheus.HistogramVec
}

// NewREDMetrics creates and registers a RED metric set with the privshield_ prefix.
func NewREDMetrics() *REDMetrics {
	reg := prometheus.NewRegistry()

	m := &REDMetrics{
		registry: reg,
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "privshield_requests_total",
				Help: "Total requests processed.",
			},
			[]string{"protocol", "endpoint", "status"},
		),
		RequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "privshield_request_duration_seconds",
				Help:    "Request latency histogram in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"protocol", "endpoint"},
		),
	}

	reg.MustRegister(m.RequestsTotal, m.RequestDuration)
	return m
}

// RecordRequest 记录一次请求的计数与延迟观测。
// protocol: "http" | "grpc"；endpoint: REST 路径或 gRPC 全限定方法名。
func (m *REDMetrics) RecordRequest(protocol, endpoint string, statusCode int, durationSec float64) {
	statusStr := strconv.Itoa(statusCode)
	m.RequestsTotal.WithLabelValues(protocol, endpoint, statusStr).Inc()
	m.RequestDuration.WithLabelValues(protocol, endpoint).Observe(durationSec)
}

// Registry 返回底层 Prometheus 注册表，供高级场景追加注册自定义指标。
func (m *REDMetrics) Registry() *prometheus.Registry {
	return m.registry
}

// MustRegister 向同一注册表追加注册自定义指标收集器。
func (m *REDMetrics) MustRegister(cs ...prometheus.Collector) {
	m.registry.MustRegister(cs...)
}

// Handler 返回暴露 Prometheus /metrics 文本端点的标准 http.Handler。
func (m *REDMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// GinHandler 返回暴露 Prometheus /metrics 端点的 Gin 处理函数。
func (m *REDMetrics) GinHandler() gin.HandlerFunc {
	h := m.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// PrometheusMiddleware 返回自动记录 HTTP 请求 RED 指标的 Gin 中间件。
// 自动豁免 /metrics 端点自身，避免自抓取导致指标无限自增。
func (m *REDMetrics) PrometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		if path == "/metrics" {
			return
		}

		duration := time.Since(start).Seconds()
		m.RecordRequest("http", path, c.Writer.Status(), duration)
	}
}

// UnaryServerInterceptor 返回自动记录 gRPC 一元调用 RED 指标的拦截器。
func (m *REDMetrics) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		duration := time.Since(start).Seconds()

		statusCode := 0
		if err != nil {
			statusCode = int(status.Code(err))
		}
		m.RecordRequest("grpc", info.FullMethod, statusCode, duration)
		return resp, err
	}
}
