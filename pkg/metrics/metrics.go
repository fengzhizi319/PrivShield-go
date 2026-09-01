// Package metrics provides shared Prometheus metrics for console Go modules.
// Package metrics 为控制台各 Go 模块提供共享的 Prometheus 指标定义与 /metrics 端点。
//
// ==============================================================================
// 【与 pkg/observability.REDMetrics 的边界划分】
//   - pkg/metrics.Collector：面向业务领域的多维指标集合（Agent 调用、任务调度、命名路由、
//     熔断器状态、数据源探查等），每个微服务模块持有一个独立 Registry 的 Collector 实例。
//   - pkg/observability.REDMetrics：面向传输层的通用 RED（Rate / Errors / Duration）指标，
//     用于 gRPC 与 HTTP 中间件的自动埋点，按 protocol + endpoint 维度统计。
//
// 二者互补而非替代：Collector 度量「业务做了什么」，REDMetrics 度量「请求经过了多少」。
// ==============================================================================
//
// ==============================================================================
// 【架构设计背景与核心价值】
// 1. 【模块隔离设计】：每个微服务模块在启动时调用 NewCollector(module) 创建带独立
//    prometheus.Registry 的指标收集器，彻底避免全局注册表并发注册冲突（单测/多实例环境安全）；
// 2. 【无缝契合命名规范】：Collector 天然实现 naming.Observer 接口，服务只需调用
//    naming.SetObserver(mc)，即可自动获得跨服务别名解析流量与归一化失败指标统计；
// 3. 【全景度量覆盖】：
//    - 基础 HTTP 吞吐、状态码与延迟直方图（http_requests_total / http_request_duration_seconds）；
//    - 上游 Agent 隐私引擎调用指标（agent_requests_total / agent_request_duration_seconds）；
//    - 可靠性与故障自愈指标（orphaned_tasks_recovered_total / tasks_retried_total / circuit_breaker_state）；
//    - PostgreSQL 原子租约与并发冲突指标（task_lease_conflicts_total / task_claim_latency_seconds / service_hub_ready）；
//    - 数据源与 API 命名路由指标（privshield_api_alias_requests_total / privshield_datasource_normalize_errors_total）。
// ==============================================================================

package metrics

import (
	"strconv"
	"time"

	"github.com/fengzhizi319/PrivShield/pkg/naming"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Collector satisfies naming.Observer, so services can register it with
// naming.SetObserver(mc) and get alias / error counters for free (§7.2).
//
// 编译期类型断言：确保 Collector 结构体 100% 实现了 naming.Observer 接口。
var _ naming.Observer = (*Collector)(nil)

// Collector holds module-scoped Prometheus metrics.
// Collector 持有模块级别的 Prometheus 指标定义与专属注册表。
type Collector struct {
	module   string               // 当前所属模块名称（作为各指标的固定常量标签 ConstLabels）
	registry *prometheus.Registry // 模块专属的 Prometheus 注册表，隔离全局状态

	// HTTPRequestsTotal 统计当前微服务处理的 HTTP 请求总数（按 method/path/status 维度）。
	HTTPRequestsTotal *prometheus.CounterVec

	// HTTPRequestDuration 记录当前微服务的 HTTP 请求响应延迟直方图（按 method/path 维度，秒级）。
	HTTPRequestDuration *prometheus.HistogramVec

	// AgentRequestsTotal 统计调用上游 Agent 隐私计算引擎的请求总数（按 endpoint/status 维度）。
	AgentRequestsTotal *prometheus.CounterVec

	// AgentRequestDuration 记录调用上游 Agent 隐私计算引擎的延迟直方图（秒级）。
	AgentRequestDuration *prometheus.HistogramVec

	// OrphanedTasksRecovered 统计系统崩溃重启后自动回收的孤儿任务数（按 type: "running" | "pending" 维度）。
	OrphanedTasksRecovered *prometheus.CounterVec

	// TasksRetried 统计进入自动重试队列的任务数（按 result: "queued" | "exhausted" 维度）。
	TasksRetried *prometheus.CounterVec

	// CircuitBreakerState 记录每个上游节点的断路器实时状态（0=closed 正常, 1=open 熔断, 2=half_open 半开）。
	CircuitBreakerState *prometheus.GaugeVec

	// ── Phase B: Lease metrics / 租约指标 ──

	// TaskLeaseConflicts 统计租约所有权抢占冲突次数（由于失去所有权或并发版本过期写入）。
	TaskLeaseConflicts prometheus.Counter

	// TaskLeaseExpired 统计由于租约超期而触发的回收事件总数。
	TaskLeaseExpired prometheus.Counter

	// TaskClaimLatency 记录基于 PostgreSQL FOR UPDATE SKIP LOCKED 抢占任务（ClaimNext）的延迟直方图。
	TaskClaimLatency prometheus.Histogram

	// TaskTransitions 按 from/to/result 维度统计任务状态机流转次数。
	TaskTransitions *prometheus.CounterVec

	// ServiceHubReady 指示当前调度中枢是否处于健康就绪状态（1=ready 可接收流量, 0=not ready）。
	ServiceHubReady prometheus.Gauge

	// ── Canonical Naming & Routing metrics / 命名规范与路由指标 ──

	// APIAliasRequestsTotal 统计使用非 Canonical 别名（如旧 slug、文件名、中文名、api_code）发起的请求数。
	APIAliasRequestsTotal *prometheus.CounterVec

	// DatasourceNormalizeErrorsTotal 统计数据源标识归一化失败或写侧校验被拒绝的次数（按 reason 维度）。
	DatasourceNormalizeErrorsTotal *prometheus.CounterVec

	// DatasourceRequestsTotal 统计按规范数据源实体与 API 编码处理的请求总数（按 datasource_id/api_code/status 维度）。
	DatasourceRequestsTotal *prometheus.CounterVec
}

// NewCollector creates and registers a new metrics collector for the given module.
// Each collector uses its own prometheus.Registry to avoid global registration conflicts.
//
// NewCollector 为指定模块创建并注册全新的指标收集器。
//
// 使用方法：
// 通常在服务的 main.go 初始化阶段单例调用：
// ```go
// mc := metrics.NewCollector("service-hub")
// router.GET("/metrics", mc.Handler())
// router.Use(mc.HTTPMiddleware())
// naming.SetObserver(mc)
// ```
//
// 执行逻辑：
// 1. 创建独立的 prometheus.NewRegistry()；
// 2. 构造所有 Counter、Gauge 与 HistogramVec 指标，注入 module 常量标签；
// 3. 调用 reg.MustRegister(...) 将所有指标注册进私有注册表。
func NewCollector(module string) *Collector {
	reg := prometheus.NewRegistry()

	c := &Collector{
		module:   module,
		registry: reg,
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:        "http_requests_total",
				Help:        "Total HTTP requests processed.",
				ConstLabels: prometheus.Labels{"module": module},
			},
			[]string{"method", "path", "status"},
		),
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:        "http_request_duration_seconds",
				Help:        "HTTP request latency in seconds.",
				ConstLabels: prometheus.Labels{"module": module},
				Buckets:     prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),
		AgentRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:        "agent_requests_total",
				Help:        "Total upstream agent requests.",
				ConstLabels: prometheus.Labels{"module": module},
			},
			[]string{"endpoint", "status"},
		),
		AgentRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:        "agent_request_duration_seconds",
				Help:        "Upstream agent request latency in seconds.",
				ConstLabels: prometheus.Labels{"module": module},
				Buckets:     prometheus.DefBuckets,
			},
			[]string{"endpoint"},
		),
	}

	// Reliability metrics (only registered for modules that need them)
	// 可靠性与容灾指标
	c.OrphanedTasksRecovered = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:        "orphaned_tasks_recovered_total",
			Help:        "Total orphaned tasks recovered after crash/restart.",
			ConstLabels: prometheus.Labels{"module": module},
		},
		[]string{"type"}, // "running" | "pending"
	)
	c.TasksRetried = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:        "tasks_retried_total",
			Help:        "Total tasks queued for automatic retry.",
			ConstLabels: prometheus.Labels{"module": module},
		},
		[]string{"result"}, // "queued" | "exhausted"
	)
	c.CircuitBreakerState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "circuit_breaker_state",
			Help:        "Circuit breaker state per node (0=closed, 1=open, 2=half_open).",
			ConstLabels: prometheus.Labels{"module": module},
		},
		[]string{"node"},
	)

	// ── Phase B: Lease metrics / 租约指标 ──
	c.TaskLeaseConflicts = prometheus.NewCounter(prometheus.CounterOpts{
		Name:        "task_lease_conflicts_total",
		Help:        "Total lease ownership conflicts (lost ownership or stale writes).",
		ConstLabels: prometheus.Labels{"module": module},
	})
	c.TaskLeaseExpired = prometheus.NewCounter(prometheus.CounterOpts{
		Name:        "task_lease_expired_total",
		Help:        "Total lease expiry recovery events.",
		ConstLabels: prometheus.Labels{"module": module},
	})
	c.TaskClaimLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:        "task_claim_latency_seconds",
		Help:        "Task claim (ClaimNext) latency in seconds.",
		ConstLabels: prometheus.Labels{"module": module},
		Buckets:     prometheus.DefBuckets,
	})
	c.TaskTransitions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:        "task_transitions_total",
			Help:        "Total task state transitions by from/to/result.",
			ConstLabels: prometheus.Labels{"module": module},
		},
		[]string{"from", "to", "result"},
	)
	c.ServiceHubReady = prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "service_hub_ready",
		Help:        "Whether the service-hub is ready to serve traffic (1=ready, 0=not ready).",
		ConstLabels: prometheus.Labels{"module": module},
	})

	// ── Canonical Naming & Routing metrics / 命名规范与路由指标 ──
	c.APIAliasRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:        "privshield_api_alias_requests_total",
			Help:        "Total requests processed via legacy/alias identifier mapping.",
			ConstLabels: prometheus.Labels{"module": module},
		},
		[]string{"alias", "canonical", "target"},
	)
	c.DatasourceNormalizeErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:        "privshield_datasource_normalize_errors_total",
			Help:        "Total identifier normalization errors and fail-closed rejections.",
			ConstLabels: prometheus.Labels{"module": module},
		},
		// 标签必须低基数：原始入站脏值不进标签（防止时序爆炸），仅通过日志输出
		[]string{"reason"},
	)
	c.DatasourceRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:        "privshield_datasource_requests_total",
			Help:        "Total requests processed per canonical datasource.",
			ConstLabels: prometheus.Labels{"module": module},
		},
		[]string{"datasource_id", "api_code", "status"},
	)

	reg.MustRegister(
		c.HTTPRequestsTotal,
		c.HTTPRequestDuration,
		c.AgentRequestsTotal,
		c.AgentRequestDuration,
		c.OrphanedTasksRecovered,
		c.TasksRetried,
		c.CircuitBreakerState,
		c.TaskLeaseConflicts,
		c.TaskLeaseExpired,
		c.TaskClaimLatency,
		c.TaskTransitions,
		c.ServiceHubReady,
		c.APIAliasRequestsTotal,
		c.DatasourceNormalizeErrorsTotal,
		c.DatasourceRequestsTotal,
	)

	return c
}

// RecordHTTP records an HTTP request metric.
//
// RecordHTTP 手动记录一次 HTTP 请求的计数与耗时（秒）。
func (c *Collector) RecordHTTP(method, path string, status int, durationSec float64) {
	statusStr := strconv.Itoa(status)
	c.HTTPRequestsTotal.WithLabelValues(method, path, statusStr).Inc()
	c.HTTPRequestDuration.WithLabelValues(method, path).Observe(durationSec)
}

// RecordAgentCall records an upstream agent call metric.
//
// RecordAgentCall 记录一次调用上游 Agent 隐私计算引擎的计数与耗时（秒）。
func (c *Collector) RecordAgentCall(endpoint string, status string, durationSec float64) {
	c.AgentRequestsTotal.WithLabelValues(endpoint, status).Inc()
	c.AgentRequestDuration.WithLabelValues(endpoint).Observe(durationSec)
}

// HTTPMiddleware returns a Gin middleware that automatically records HTTP request
// metrics (count and latency histogram) using this collector.
//
// HTTPMiddleware 返回自动统计 HTTP 请求指标的 Gin 中间件。
//
// 执行逻辑：
// 1. 记录开始时间 start；
// 2. 执行后续 handler（ctx.Next()）；
// 3. 拦截判断当前路径：若为 "/metrics" 端点自身则跳过记录，避免自抓取导致指标无限自增与递归；
// 4. 计算耗时并调用 RecordHTTP 记录。
func (c *Collector) HTTPMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		start := time.Now()
		path := ctx.FullPath()
		if path == "" {
			path = ctx.Request.URL.Path
		}

		ctx.Next()

		// Skip metric recording for the /metrics endpoint itself to avoid recursion
		// 豁免 /metrics 自身，防递归与指标污染
		if path == "/metrics" {
			return
		}

		duration := time.Since(start).Seconds()
		c.RecordHTTP(ctx.Request.Method, path, ctx.Writer.Status(), duration)
	}
}

// RecordOrphanedRecovery records a recovered orphaned task metric.
//
// RecordOrphanedRecovery 记录一次孤立任务恢复事件。
func (c *Collector) RecordOrphanedRecovery(taskType string) {
	c.OrphanedTasksRecovered.WithLabelValues(taskType).Inc()
}

// RecordTaskRetry records a task retry attempt metric.
//
// RecordTaskRetry 记录一次任务重试排队或重试耗尽事件。
func (c *Collector) RecordTaskRetry(result string) {
	c.TasksRetried.WithLabelValues(result).Inc()
}

// SetCircuitBreakerState updates the circuit breaker state gauge for a node.
//
// SetCircuitBreakerState 更新指定节点的断路器状态值（0=closed, 1=open, 2=half_open）。
func (c *Collector) SetCircuitBreakerState(node string, state string) {
	var val float64
	switch state {
	case "closed":
		val = 0
	case "open":
		val = 1
	case "half_open":
		val = 2
	}
	c.CircuitBreakerState.WithLabelValues(node).Set(val)
}

// ── Phase B: Lease metric helpers / 租约指标辅助方法 ──

// RecordLeaseConflict increments the lease conflict counter.
//
// RecordLeaseConflict 递增租约所有权冲突计数器。
func (c *Collector) RecordLeaseConflict() {
	c.TaskLeaseConflicts.Inc()
}

// RecordLeaseExpired increments the lease expiry recovery counter.
//
// RecordLeaseExpired 递增租约到期回收事件计数器。
func (c *Collector) RecordLeaseExpired(count int) {
	c.TaskLeaseExpired.Add(float64(count))
}

// RecordClaimLatency observes a task claim latency.
//
// RecordClaimLatency 记录一次任务抢占（ClaimNext）的耗时（秒）。
func (c *Collector) RecordClaimLatency(durationSec float64) {
	c.TaskClaimLatency.Observe(durationSec)
}

// RecordTaskTransition records a task state transition.
//
// RecordTaskTransition 记录一次任务状态机流转事件（from, to, result）。
func (c *Collector) RecordTaskTransition(from, to, result string) {
	c.TaskTransitions.WithLabelValues(from, to, result).Inc()
}

// SetReady sets the service-hub readiness gauge.
//
// SetReady 设置当前微服务的就绪探针指标（1 为就绪，0 为未就绪）。
func (c *Collector) SetReady(ready bool) {
	if ready {
		c.ServiceHubReady.Set(1)
	} else {
		c.ServiceHubReady.Set(0)
	}
}

// ── Canonical Naming metric helpers / 命名规范指标辅助方法 ──

// RecordAPIAlias records an alias mapping usage event.
// target: "api_code" | "datasource_id" | "path"
//
// RecordAPIAlias 实现了 naming.Observer 接口，记录别名解析流量事件。
func (c *Collector) RecordAPIAlias(alias, canonical, target string) {
	c.APIAliasRequestsTotal.WithLabelValues(alias, canonical, target).Inc()
}

// RecordNormalizeError records an identifier normalization failure.
// reason: "unknown" | "reserved" | "empty" | "format_invalid"
//
// RecordNormalizeError 实现了 naming.Observer 接口，记录标识归一化失败与写侧校验拒绝事件。
func (c *Collector) RecordNormalizeError(reason string) {
	c.DatasourceNormalizeErrorsTotal.WithLabelValues(reason).Inc()
}

// RecordDatasourceRequest records a request by canonical datasource ID and status.
// status: "success" | "error" | "fallback"
//
// RecordDatasourceRequest 记录按规范数据源实体与 API 编码处理的数据流水线请求。
func (c *Collector) RecordDatasourceRequest(datasourceID, apiCode, status string) {
	c.DatasourceRequestsTotal.WithLabelValues(datasourceID, apiCode, status).Inc()
}

// Handler returns a Gin handler that serves Prometheus /metrics endpoint
// using this collector's custom registry.
//
// Handler 返回暴露 Prometheus /metrics 文本端点的 Gin 处理函数。
// 严格使用当前 Collector 私有注册表中的指标，避免拉取未初始化的全局指标。
func (c *Collector) Handler() gin.HandlerFunc {
	h := promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{})
	return func(ctx *gin.Context) {
		h.ServeHTTP(ctx.Writer, ctx.Request)
	}
}
