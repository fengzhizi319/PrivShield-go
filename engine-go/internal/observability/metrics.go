// Package observability — engine-go Prometheus 指标扩展。
//
// 通用 RED 指标已下沉至 pkg/observability.REDMetrics；本包只保留引擎专属业务指标。
package observability

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"

	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
)

// EngineMetrics 持有 engine-go 的 RED 指标 + 隐私计算业务指标。
type EngineMetrics struct {
	*pkgobs.REDMetrics

	// ClassificationTotal 按 engine/level/domain 统计分类命中数。
	ClassificationTotal *prometheus.CounterVec

	// BudgetConsumedTotal 按 namespace/mechanism 统计 DP 预算消耗。
	BudgetConsumedTotal *prometheus.CounterVec

	// NerInferenceSeconds GPU/CPU NER 推理耗时直方图。
	NerInferenceSeconds *prometheus.HistogramVec

	// APIAliasRequestsTotal 统计入站标识命中别名映射的请求数。
	APIAliasRequestsTotal *prometheus.CounterVec

	// DatasourceNormalizeErrorsTotal 统计标识归一化失败与 fail-closed 拒绝数。
	DatasourceNormalizeErrorsTotal *prometheus.CounterVec
}

// NewEngineMetrics 创建并注册 engine-go 指标集合。
// 通用 RED 指标由 pkg/observability.NewREDMetrics 提供，避免与 pkg 侧重复实现。
func NewEngineMetrics() *EngineMetrics {
	red := pkgobs.NewREDMetrics()

	m := &EngineMetrics{
		REDMetrics: red,

		ClassificationTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "privshield_classification_total",
				Help: "Classification funnel hits by engine/level/domain.",
			},
			[]string{"engine", "level", "domain"},
		),

		BudgetConsumedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "privshield_budget_consumed_total",
				Help: "Cumulative differential privacy budget consumed.",
			},
			[]string{"namespace", "mechanism"},
		),

		NerInferenceSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "privshield_ner_inference_seconds",
				Help:    "NER inference latency by device and batch size.",
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
			},
			[]string{"device", "batch_size"},
		),

		APIAliasRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:        "privshield_api_alias_requests_total",
				Help:        "Total requests processed via legacy/alias identifier mapping.",
				ConstLabels: prometheus.Labels{"module": "privshield-agent"},
			},
			[]string{"alias", "canonical", "target"},
		),

		DatasourceNormalizeErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:        "privshield_datasource_normalize_errors_total",
				Help:        "Total identifier normalization errors and fail-closed rejections.",
				ConstLabels: prometheus.Labels{"module": "privshield-agent"},
			},
			[]string{"reason"},
		),
	}

	red.MustRegister(
		m.ClassificationTotal,
		m.BudgetConsumedTotal,
		m.NerInferenceSeconds,
		m.APIAliasRequestsTotal,
		m.DatasourceNormalizeErrorsTotal,
	)

	return m
}

// RecordNamingAlias 上报一次入站别名解析事件（P2-5）。
func (m *EngineMetrics) RecordNamingAlias(alias, canonical, target string) {
	m.APIAliasRequestsTotal.WithLabelValues(alias, canonical, target).Inc()
}

// RecordNamingError 上报一次标识归一化失败 / fail-closed 拒绝（P2-5）。
// reason 必须是 pkg/naming 的有界枚举，避免高基数时序。
func (m *EngineMetrics) RecordNamingError(reason string) {
	m.DatasourceNormalizeErrorsTotal.WithLabelValues(reason).Inc()
}

// RecordClassification 记录一次分类命中。
func (m *EngineMetrics) RecordClassification(engine, level, domain string) {
	m.ClassificationTotal.WithLabelValues(engine, level, domain).Inc()
}

// RecordBudgetConsumed 记录一次 DP 预算消耗。
func (m *EngineMetrics) RecordBudgetConsumed(namespace, mechanism string) {
	m.BudgetConsumedTotal.WithLabelValues(namespace, mechanism).Inc()
}

// RecordNerInference 记录一次 NER 推理耗时。
func (m *EngineMetrics) RecordNerInference(device string, batchSize int, durationSec float64) {
	m.NerInferenceSeconds.WithLabelValues(device, strconv.Itoa(batchSize)).Observe(durationSec)
}

// Handler 返回暴露 /metrics 端点的 Gin handler。
// 覆盖嵌入的 pkg/observability.REDMetrics.Handler()（http.Handler），保持原有签名。
func (m *EngineMetrics) Handler() gin.HandlerFunc {
	return m.REDMetrics.GinHandler()
}

// PrometheusMiddleware 返回自动记录 HTTP 请求指标的 Gin 中间件。
func (m *EngineMetrics) PrometheusMiddleware() gin.HandlerFunc {
	return m.REDMetrics.PrometheusMiddleware()
}

// UnaryServerInterceptor 返回 gRPC unary 拦截器。
func (m *EngineMetrics) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return m.REDMetrics.UnaryServerInterceptor()
}
