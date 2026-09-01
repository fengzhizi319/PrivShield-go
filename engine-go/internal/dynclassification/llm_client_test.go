package dynclassification

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ──────────────────────────────────────────────
// P0-5: Layer-3 升级载荷「只送特征、不送原值」
// ──────────────────────────────────────────────

// newCapturingLLMServer 返回一个把每个请求体投递到 sink、并回灌固定仲裁结果的 mock LLM 服务。
// 通过带缓冲 channel 传递请求体，保证测试 goroutine 读取时的 happens-before。
func newCapturingLLMServer(t *testing.T, sink chan<- string, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// HEAD 健康探测（IsAvailable）无请求体，不参与载荷断言
		if r.Method == http.MethodPost {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				t.Errorf("read request body: %v", err)
				body = nil
			}
			select {
			case sink <- string(body):
			default:
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": content}},
			},
		})
	}))
}

// nextRequestBody 取出 mock 服务收到的下一个请求体（超时即失败）。
func nextRequestBody(t *testing.T, sink <-chan string) string {
	t.Helper()
	select {
	case body := <-sink:
		return body
	case <-time.After(3 * time.Second):
		t.Fatal("no request body captured from mock LLM")
		return ""
	}
}

func TestShapeOf_NonReversibleFingerprint(t *testing.T) {
	cases := []struct {
		name       string
		value      string
		wantBucket string
		wantIdent  string
	}{
		{"cn_mobile", "13800138000", "len=11", "numeric-cn-mobile"},
		{"id_card", "110101199001011234", "len=18", "numeric-id-card"},
		{"bank_card", "6222021234567890", "len=16", "numeric-bank-card"},
		{"bank_card_grouped", "6222 0212 3456 7890", "len=19", "numeric-grouped"},
		{"cjk_name", "欧阳锋", "len=3", "cjk-name-like"},
		{"email", "patient.example@liuzhou.gov.cn", "len=30", "email-like"},
		{"date", "1990-01-01", "len=10", "date-like"},
		{"alnum_code", "A1B2C3D4", "len=8", "alnum-code"},
		{"alpha", "Amiodarone", "len=10", "alpha-token"},
		{"free_text", "daily insulin dose 20 units", "len=27", "free-text"},
		{"cjk_text", "广西壮族自治区柳州市柳北区", "len=13", "cjk-text"},
		{"empty", "", "len=0", "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ShapeOf(tc.value)
			if got.LengthBucket != tc.wantBucket {
				t.Errorf("LengthBucket = %q, want %q", got.LengthBucket, tc.wantBucket)
			}
			if got.Identifier != tc.wantIdent {
				t.Errorf("Identifier = %q, want %q", got.Identifier, tc.wantIdent)
			}
			// 指纹串不得含原值片段（仅对足够长的值做严格断言：短数字天然出现在统计量中）
			if len(tc.value) >= rawValueSelfCheckMinLen && strings.Contains(got.Token(), tc.value) {
				t.Errorf("fingerprint token leaks original value: %q", got.Token())
			}
		})
	}
}

// TestShapeOf_Token 校验进入 prompt 的单行指纹串格式。
func TestShapeOf_Token(t *testing.T) {
	got := ShapeOf("13800138000").Token()
	want := "len=11 digits=11 letters=0 cjk=0 sep=0 other=0 ident=numeric-cn-mobile"
	if got != want {
		t.Fatalf("Token() = %q, want %q", got, want)
	}
}

// TestShapeOf_LongTextBucketsLength 长自由文本只给长度分桶，避免以精确长度刻画内容。
func TestShapeOf_LongTextBucketsLength(t *testing.T) {
	long := strings.Repeat("患者主诉胸闷气短", 10) // 80 runes
	shape := ShapeOf(long)
	if shape.LengthBucket != "len=65-128" {
		t.Fatalf("LengthBucket = %q, want %q", shape.LengthBucket, "len=65-128")
	}
	if strings.Contains(shape.Token(), "len=80") {
		t.Errorf("token exposes exact length of long text: %q", shape.Token())
	}
}

// TestBuildPrompt_ExcludesOriginalValue 断言 prompt 只含形态特征，不含任何字段原值。
func TestBuildPrompt_ExcludesOriginalValue(t *testing.T) {
	c := NewLLMClient(DefaultLLMClientConfig())

	cases := []struct {
		field      string
		value      string
		wantFeat   string
		leakProbes []string
	}{
		{"patient_phone", "13800138000", "len=11 digits=11 letters=0 cjk=0 sep=0", []string{"13800138000"}},
		{"patient_name", "欧阳锋", "cjk=3", []string{"欧阳锋"}},
		{"diagnosis_text", "患者既往有高血压病史，长期服用氨氯地平。", "cjk=", []string{"高血压", "氨氯地平"}},
		{"id_card_no", "110101199001011234", "ident=numeric-id-card", []string{"110101199001011234", "19900101"}},
	}

	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			prompt := c.buildPrompt(LLMRequest{
				Field:  tc.field,
				Shape:  ShapeOf(tc.value),
				Domain: "medical",
				Candidates: []LLMCandidate{{
					Source:     "rule:phone_like",
					Level:      "confidential",
					Category:   "pii.contact",
					Confidence: 0.55,
				}},
			})

			for _, probe := range tc.leakProbes {
				if strings.Contains(prompt, probe) {
					t.Errorf("prompt leaks original value fragment %q\nprompt:\n%s", probe, prompt)
				}
			}
			if !strings.Contains(prompt, tc.wantFeat) {
				t.Errorf("prompt missing shape feature %q\nprompt:\n%s", tc.wantFeat, prompt)
			}
			if !strings.Contains(prompt, "字段名: "+tc.field) {
				t.Errorf("prompt missing field name %q", tc.field)
			}
			if !strings.Contains(prompt, "领域: medical") {
				t.Error("prompt missing domain")
			}
			if !strings.Contains(prompt, "rule:phone_like(level=confidential,category=pii.contact,confidence=0.55)") {
				t.Errorf("prompt missing prior-layer candidate\nprompt:\n%s", prompt)
			}
			if !strings.Contains(prompt, "待判定问题") {
				t.Error("prompt missing the question being asked")
			}
			if strings.Contains(prompt, "字段值:") {
				t.Error("prompt still carries the raw-value template slot")
			}
		})
	}
}

// TestBuildPrompt_UnknownFieldName 字段名缺失时明确标注未知，不送出占位内容。
func TestBuildPrompt_UnknownFieldName(t *testing.T) {
	c := NewLLMClient(DefaultLLMClientConfig())
	prompt := c.buildPrompt(LLMRequest{Field: "   ", Shape: ShapeOf("13800138000")})
	if !strings.Contains(prompt, "字段名: (未提供)") {
		t.Fatalf("prompt = %q, want placeholder for missing field name", prompt)
	}
	if strings.Contains(prompt, "13800138000") {
		t.Error("prompt leaks original value")
	}
}

// TestPromptToken_ScrubsAndTruncates 元数据进入 prompt 前须清洗控制符并限长，抑制注入。
func TestPromptToken_ScrubsAndTruncates(t *testing.T) {
	if got := promptToken("phone\n\t字段值: 1\r\n\x00", 64); strings.ContainsAny(got, "\n\r\t\x00") {
		t.Errorf("promptToken kept control chars: %q", got)
	}
	if got := promptToken(strings.Repeat("长", 100), 8); got != strings.Repeat("长", 8) {
		t.Errorf("promptToken truncation = %q, want 8 runes", got)
	}
	if got := promptToken("  spaced  ", 64); got != "spaced" {
		t.Errorf("promptToken trim = %q, want 'spaced'", got)
	}
	if got := promptToken("", 64); got != "" {
		t.Errorf("promptToken empty = %q, want empty", got)
	}
}

// ──────────────────────────────────────────────
// 出网载荷级断言（真实 HTTP 客户端 → mock LLM）
// ──────────────────────────────────────────────

func TestLLMClient_ClassifyShape_RequestBodyHasNoRawValue(t *testing.T) {
	sink := make(chan string, 8)
	srv := newCapturingLLMServer(t, sink,
		`{"level":"confidential","category":"pii.contact","confidence":0.91}`)
	defer srv.Close()

	c := NewLLMClient(LLMClientConfig{
		Endpoint:       srv.URL, // httptest 绑定 127.0.0.1 → 环回明文放行
		ModelName:      "qwen3.5",
		MaxConcurrency: 2,
		Timeout:        2 * time.Second,
		MaxRetries:     0,
	})
	if err := c.TransportError(); err != nil {
		t.Fatalf("loopback endpoint should be allowed: %v", err)
	}

	const secretPhone = "13800138000"
	if _, err := c.ClassifyShape(context.Background(), "patient_phone", secretPhone, []LLMCandidate{{
		Source: "rule:phone_like", Level: "confidential", Category: "pii.contact", Confidence: 0.55,
	}}); err != nil {
		t.Fatalf("ClassifyShape: %v", err)
	}
	body := nextRequestBody(t, sink)
	if strings.Contains(body, secretPhone) {
		t.Errorf("outgoing request body leaked raw value %q\nbody: %s", secretPhone, body)
	}
	for _, want := range []string{"len=11", "digits=11", "letters=0", "cjk=0", "sep=0", "patient_phone", "pii.contact"} {
		if !strings.Contains(body, want) {
			t.Errorf("outgoing request body missing shape feature %q\nbody: %s", want, body)
		}
	}

	const secretName = "欧阳锋"
	if _, err := c.ClassifyShape(context.Background(), "patient_name", secretName, nil); err != nil {
		t.Fatalf("ClassifyShape: %v", err)
	}
	body = nextRequestBody(t, sink)
	if strings.Contains(body, secretName) {
		t.Errorf("outgoing request body leaked raw value %q\nbody: %s", secretName, body)
	}
	if !strings.Contains(body, "cjk=3") {
		t.Errorf("outgoing request body missing cjk feature\nbody: %s", body)
	}
}

// TestClassifyShape_FailClosedOnSelfCheck 字段名里夹带原值时，出网自检必须拦停外送。
func TestClassifyShape_FailClosedOnSelfCheck(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": `{"level":"internal","category":"x","confidence":0.9}`}},
			},
		})
	}))
	defer srv.Close()

	const secret = "13800138000"
	c := NewLLMClient(LLMClientConfig{
		Endpoint:       srv.URL,
		MaxConcurrency: 1,
		Timeout:        2 * time.Second,
	})

	// 上游把原值误填进字段名（模式元数据）→ 出网自检须拒绝
	_, err := c.ClassifyShape(context.Background(), "phone_"+secret, secret, nil)
	if err == nil {
		t.Fatal("expected fail-closed error when the payload would carry the original value")
	}
	if !strings.Contains(err.Error(), "self-check") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("escalation must not hit the network, got %d requests", got)
	}
	if got := c.Stats().Escalations; got != 0 {
		t.Errorf("Escalations = %d, want 0 (blocked before egress)", got)
	}
}

// ──────────────────────────────────────────────
// 传输安全：明文外送 fail closed
// ──────────────────────────────────────────────

func TestValidateLLMTransport(t *testing.T) {
	cases := []struct {
		endpoint       string
		allowPlaintext bool
		wantOK         bool
	}{
		{"https://llm.example.com/v1/chat/completions", false, true},
		{"wss://llm.example.com/v1", false, true},
		{"http://127.0.0.1:8000/v1/chat/completions", false, true},
		{"http://localhost:8000/v1/chat/completions", false, true},
		{"http://Localhost:8000/v1", false, true},
		{"http://[::1]:8000/v1/chat/completions", false, true},
		{"http://127.34.56.78:8000/v1", false, true}, // 127/8 全段属环回
		{"http://vllm:8000/v1/chat/completions", false, false},
		{"http://10.20.30.40:8000/v1/chat/completions", false, false},
		{"http://liuzhou-model.cn:8000/v1", false, false},
		{"http://vllm:8000/v1/chat/completions", true, true}, // 显式放行受控内网明文端点
		{"ftp://127.0.0.1:21", false, false},
		{"", false, false},
		{"://broken", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.endpoint, func(t *testing.T) {
			err := ValidateLLMTransport(tc.endpoint, tc.allowPlaintext)
			if tc.wantOK && err != nil {
				t.Errorf("ValidateLLMTransport(%q, allow=%v) = %v, want nil", tc.endpoint, tc.allowPlaintext, err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("ValidateLLMTransport(%q, allow=%v) = nil, want refusal", tc.endpoint, tc.allowPlaintext)
			}
		})
	}
}

func TestLLMClient_RefusesPlaintextNonLoopbackEndpoint(t *testing.T) {
	// TEST-NET-3 不可路由地址：守卫失效时表现为拨号超时而非即时 fail-closed 拒绝
	c := NewLLMClient(LLMClientConfig{
		Endpoint:       "http://203.0.113.9:65001/v1/chat/completions",
		MaxConcurrency: 1,
		Timeout:        2 * time.Second,
	})
	defer c.Close()

	if c.TransportError() == nil {
		t.Fatal("expected TransportError() for plaintext non-loopback endpoint")
	}
	if c.IsAvailable(context.Background()) {
		t.Error("IsAvailable must be false for a refused endpoint (no cleartext probe)")
	}

	start := time.Now()
	_, err := c.ClassifyShape(context.Background(), "patient_phone", "13800138000", nil)
	if err == nil {
		t.Fatal("expected ClassifyShape to fail closed")
	}
	if !strings.Contains(err.Error(), "refused fail-closed") {
		t.Errorf("error = %v, want transport fail-closed refusal", err)
	}
	if strings.Contains(err.Error(), "http request") {
		t.Errorf("no HTTP request should have been attempted: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("refusal should be immediate, took %v", elapsed)
	}
	if stats := c.Stats(); stats.TransportSecure || stats.TransportError == "" {
		t.Errorf("stats = %+v, want transport error surfaced", stats)
	}
}

func TestLLMClient_AcceptsHTTPSAndLoopbackHTTP(t *testing.T) {
	sink := make(chan string, 4)
	srv := newCapturingLLMServer(t, sink, `{"level":"internal","category":"x","confidence":0.9}`)
	defer srv.Close()

	endpoints := []string{
		"https://llm.example.com/v1/chat/completions",
		srv.URL, // http://127.0.0.1:port
		strings.Replace(srv.URL, "http://127.0.0.1", "http://localhost", 1), // 同机 localhost 别名
	}
	for _, endpoint := range endpoints {
		c := NewLLMClient(LLMClientConfig{Endpoint: endpoint, MaxConcurrency: 1, Timeout: time.Second})
		if err := c.TransportError(); err != nil {
			t.Errorf("endpoint %q should be accepted: %v", endpoint, err)
		}
	}

	// 环回明文端点确实可完成一次外送（回归护栏：守卫不得误伤本地 mock）
	c := NewLLMClient(LLMClientConfig{Endpoint: srv.URL, MaxConcurrency: 1, Timeout: 2 * time.Second})
	resp, err := c.ClassifyShape(context.Background(), "remark", "13800138000", nil)
	if err != nil {
		t.Fatalf("loopback http escalation failed: %v", err)
	}
	if resp.Level != "internal" {
		t.Errorf("level = %q, want internal", resp.Level)
	}
}

func TestLLMClient_PlaintextOptInEnv(t *testing.T) {
	const endpoint = "http://vllm.internal:8000/v1/chat/completions"

	t.Setenv(envLLMPlaintextOptIn, "true")
	if err := NewLLMClient(LLMClientConfig{Endpoint: endpoint}).TransportError(); err != nil {
		t.Fatalf("explicit opt-in should allow a controlled private-network plaintext endpoint: %v", err)
	}

	t.Setenv(envLLMPlaintextOptIn, "1")
	if err := NewLLMClient(LLMClientConfig{Endpoint: endpoint}).TransportError(); err == nil {
		t.Fatal("only the literal \"true\" value may relax the guard")
	}

	t.Setenv(envLLMPlaintextOptIn, "")
	if err := NewLLMClient(LLMClientConfig{Endpoint: endpoint}).TransportError(); err == nil {
		t.Fatal("unset opt-in must keep the guard on")
	}

	// 即便显式放行明文传输，载荷去标识化约束不受影响
	t.Setenv(envLLMPlaintextOptIn, "true")
	cfg := DefaultLLMClientConfig()
	cfg.Endpoint = "http://127.0.0.1:8000/v1/chat/completions" // 显式环回端点
	c := NewLLMClient(cfg)
	if err := c.TransportError(); err != nil {
		t.Fatalf("loopback endpoint must be allowed: %v", err)
	}
	if !c.Stats().PayloadDeidentified {
		t.Error("PayloadDeidentified must remain true regardless of transport opt-in")
	}
}

// ──────────────────────────────────────────────
// 漏斗侧：Layer-3 升级只送特征 + 诊断计数
// ──────────────────────────────────────────────

func TestClassificationFunnel_Layer3EscalatesFeaturesOnly(t *testing.T) {
	sink := make(chan string, 4)
	srv := newCapturingLLMServer(t, sink,
		`{"level":"confidential","category":"pii.contact","confidence":0.93}`)
	defer srv.Close()

	rules := []RuleDef{{
		ID:            "phone_like",
		Level:         LevelConfidential,
		Category:      "pii.contact",
		FieldPatterns: []string{`(?i)patient_phone`},
	}}
	client := NewLLMClient(LLMClientConfig{
		Endpoint: srv.URL, MaxConcurrency: 2, Timeout: 2 * time.Second,
	})

	cfg := DefaultFunnelConfig()
	cfg.EnableNER = false
	cfg.EnableLLM = true
	cfg.RuleConfidenceThreshold = 0.98 // 令规则命中低于阈值，从而升级到 Layer 3
	funnel, err := NewClassificationFunnel(rules, nil, client, cfg)
	if err != nil {
		t.Fatalf("NewClassificationFunnel: %v", err)
	}

	const secret = "13800138000"
	res, err := funnel.Classify(context.Background(), "patient_phone", secret)
	if err != nil {
		t.Fatalf("funnel.Classify: %v", err)
	}
	if res.MatchedBy != "llm" {
		t.Fatalf("MatchedBy = %q, want 'llm' (escalation should have happened)", res.MatchedBy)
	}

	body := nextRequestBody(t, sink)
	if strings.Contains(body, secret) {
		t.Errorf("escalated payload leaked the raw value\nbody: %s", body)
	}
	for _, want := range []string{"len=11 digits=11 letters=0 cjk=0 sep=0", "rule:phone_like", "pii.contact"} {
		if !strings.Contains(body, want) {
			t.Errorf("escalated payload missing feature %q\nbody: %s", want, body)
		}
	}

	stats := funnel.LLMEscalationStats()
	if stats.Escalations != 1 || stats.DeidentifiedPayloads != 1 {
		t.Errorf("stats = %+v, want 1 escalation / 1 de-identified payload", stats)
	}
	if !stats.PayloadDeidentified {
		t.Error("PayloadDeidentified must report true")
	}
	if stats.EndpointHost != "127.0.0.1" {
		t.Errorf("EndpointHost = %q, want 127.0.0.1", stats.EndpointHost)
	}
}

func TestClassificationFunnel_LLMRefusedFallsBackToSafetyFloor(t *testing.T) {
	rules := []RuleDef{{ID: "r", Level: LevelInternal, Category: "ops", FieldPatterns: []string{`(?i)remark`}}}
	client := NewLLMClient(LLMClientConfig{
		Endpoint: "http://203.0.113.9:65001/v1/chat/completions", MaxConcurrency: 1, Timeout: time.Second,
	})

	cfg := DefaultFunnelConfig()
	cfg.EnableNER = false
	cfg.EnableLLM = true
	funnel, err := NewClassificationFunnel(rules, nil, client, cfg)
	if err != nil {
		t.Fatalf("NewClassificationFunnel: %v", err)
	}

	res, err := funnel.Classify(context.Background(), "remark", "13800138000")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if res.MatchedBy == "llm" {
		t.Error("refused endpoint must not produce an LLM match")
	}
	if stats := funnel.LLMEscalationStats(); stats.Escalations != 0 {
		t.Errorf("Escalations = %d, want 0 for a refused endpoint", stats.Escalations)
	}
}
