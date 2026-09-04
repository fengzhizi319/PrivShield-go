package microservices

// P0-7 转发层纵深防御测试：ClientPool.Proxy 只接受绝对且已规范化的路径。

import (
	"context"
	"net/http"
	"testing"
)

func TestIsCleanProxyPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/v1/datasources", true},
		{"/v1/datasources/ds_yibao/metadata", true},
		{"/health", true},
		{"", false},
		{"api/datasources", false},             // 非绝对路径
		{"/v1/datasources/", false},            // 尾斜杠未规范化
		{"/v1//datasources", false},            // 重复斜杠未规范化
		{"/v1/datasources/../v1/yibao", false}, // 含 ".."
		{"/v1/datasources/%2e%2e/x", false},    // 含编码混淆
		{`/v1/datasources\..\v1\yibao`, false},
	}
	for _, tc := range cases {
		if got := isCleanProxyPath(tc.path); got != tc.want {
			t.Errorf("isCleanProxyPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestClientPoolProxy_RejectsDirtyPath 断言脏路径在发起上游请求前即被拒绝。
func TestClientPoolProxy_RejectsDirtyPath(t *testing.T) {
	pool := &ClientPool{
		httpClient: &http.Client{},
		urls:       map[string]string{"datasource": "http://127.0.0.1:1"},
		apiKeys:    map[string]string{"datasource": ""},
	}
	// 端口 1 上没有服务：若实现退化为真实拨号，用例会以拨网地址失败而非快速拒绝，
	// 因此这里断言错误信息必须是路径非法，而不是上游请求失败。
	_, _, err := pool.Proxy(context.Background(), "datasource", http.MethodGet,
		"/v1/datasources/../v1/yibao", nil, nil, "", "req-1")
	if err == nil {
		t.Fatal("expected dirty proxy path to be rejected, got nil error")
	}
	if err.Error() != "invalid proxy path for datasource" {
		t.Errorf("err = %q, want %q", err.Error(), "invalid proxy path for datasource")
	}
}
