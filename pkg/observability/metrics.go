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

// RecordRequest increments the request counter and observes latency.
func (m *REDMetrics) RecordRequest(protocol, endpoint string, statusCode int, durationSec float64) {
	statusStr := strconv.Itoa(statusCode)
	m.RequestsTotal.WithLabelValues(protocol, endpoint, statusStr).Inc()
	m.RequestDuration.WithLabelValues(protocol, endpoint).Observe(durationSec)
}

// Registry returns the underlying Prometheus registry for advanced registration.
func (m *REDMetrics) Registry() *prometheus.Registry {
	return m.registry
}

// MustRegister registers additional collectors into the same registry.
func (m *REDMetrics) MustRegister(cs ...prometheus.Collector) {
	m.registry.MustRegister(cs...)
}

// Handler returns an http.Handler that exposes the metrics on /metrics.
func (m *REDMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// GinHandler returns a Gin handler that exposes the metrics on /metrics.
func (m *REDMetrics) GinHandler() gin.HandlerFunc {
	h := m.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// PrometheusMiddleware returns a Gin middleware that records RED metrics for HTTP requests.
// It skips the /metrics endpoint to avoid self-reference.
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

// UnaryServerInterceptor returns a gRPC unary interceptor that records RED metrics.
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
