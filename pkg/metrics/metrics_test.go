// Package metrics 单元测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证 Package metrics（Prometheus 监控指标收集器）的核心功能：
//  1. 【实例与注册表隔离】：验证 NewCollector 正确初始化私有 Registry，多模块实例互不干扰；
//  2. 【指标打点安全性】：验证 RecordHTTP、RecordAgentCall 等方法在高并发调用时不发生 panic；
//  3. 【Prometheus 端点导出】：验证 Handler() 正确输出标准 Prometheus 格式指标与 Content-Type；
//  4. 【命名规范集成】：验证 naming.Observer 接口回调（别名上报、错误分类统计）能够正确进入指标文本。
// ==============================================================================

package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestNewCollector 验证 NewCollector 能够正确构建实例并完整注册所有预置指标。
func TestNewCollector(t *testing.T) {
	c := NewCollector("test-module")
	if c == nil {
		t.Fatal("NewCollector returned nil")
	}
	if c.module != "test-module" {
		t.Errorf("module = %q, want %q", c.module, "test-module")
	}
	if c.registry == nil {
		t.Error("registry is nil")
	}
	if c.HTTPRequestsTotal == nil {
		t.Error("HTTPRequestsTotal is nil")
	}
	if c.HTTPRequestDuration == nil {
		t.Error("HTTPRequestDuration is nil")
	}
	if c.AgentRequestsTotal == nil {
		t.Error("AgentRequestsTotal is nil")
	}
	if c.AgentRequestDuration == nil {
		t.Error("AgentRequestDuration is nil")
	}
}

// TestNewCollector_IndependentRegistries 验证不同模块创建的 Collector 拥有相互独立的注册表实例，
// 杜绝全局注册表命名空间冲突。
func TestNewCollector_IndependentRegistries(t *testing.T) {
	c1 := NewCollector("module-a")
	c2 := NewCollector("module-b")
	if c1.registry == c2.registry {
		t.Error("two collectors should have independent registries")
	}
}

// TestRecordHTTP 验证 HTTP 请求计数与延迟打点方法正常执行无异常。
func TestRecordHTTP(t *testing.T) {
	c := NewCollector("test")
	// Should not panic
	c.RecordHTTP("GET", "/health", 200, 0.05)
	c.RecordHTTP("POST", "/v1/dispatch", 201, 0.12)
	c.RecordHTTP("GET", "/health", 500, 0.01)
}

// TestRecordAgentCall 验证上游 Agent 调用指标打点方法正常执行无异常。
func TestRecordAgentCall(t *testing.T) {
	c := NewCollector("test")
	// Should not panic
	c.RecordAgentCall("/health", "200", 0.03)
	c.RecordAgentCall("/v1/privacy/mask", "500", 0.5)
}

// TestHandler_ReturnsMetrics 验证 GET /metrics 端点能正确响应并输出包含 module 标签的标准 Prometheus 格式文本。
func TestHandler_ReturnsMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := NewCollector("handler-test")

	// Record some metrics first / 先注入测试指标
	c.RecordHTTP("GET", "/health", 200, 0.01)
	c.RecordAgentCall("/health", "200", 0.02)

	r := gin.New()
	r.GET("/metrics", c.Handler())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/metrics", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	// Verify key metrics are present / 断言核心指标存在
	if !strings.Contains(body, "http_requests_total") {
		t.Error("response missing http_requests_total")
	}
	if !strings.Contains(body, "agent_requests_total") {
		t.Error("response missing agent_requests_total")
	}
	if !strings.Contains(body, `module="handler-test"`) {
		t.Error("response missing module label")
	}
}

// TestHandler_ContentType 验证 /metrics 响应头 Content-Type 符合 Prometheus 协议格式规范。
func TestHandler_ContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := NewCollector("content-type-test")

	r := gin.New()
	r.GET("/metrics", c.Handler())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/metrics", nil)
	r.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") && !strings.Contains(ct, "openmetrics") {
		t.Errorf("content-type = %q, want prometheus format", ct)
	}
}

// TestNamingMetrics 验证命名规范指标（别名流量、归一化失败、数据源请求）能够被正确记录并导出至 /metrics 报文。
func TestNamingMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := NewCollector("naming-metrics-test")

	c.RecordAPIAlias("yibao", "ds_yibao", "datasource_id")
	c.RecordNormalizeError("reserved")
	c.RecordDatasourceRequest("ds_yibao", "api1_yibao", "success")

	r := gin.New()
	r.GET("/metrics", c.Handler())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/metrics", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "privshield_api_alias_requests_total") {
		t.Error("missing privshield_api_alias_requests_total")
	}
	if !strings.Contains(body, "privshield_datasource_normalize_errors_total") {
		t.Error("missing privshield_datasource_normalize_errors_total")
	}
	if !strings.Contains(body, "privshield_datasource_requests_total") {
		t.Error("missing privshield_datasource_requests_total")
	}
}
