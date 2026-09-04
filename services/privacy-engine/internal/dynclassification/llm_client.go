// Package dynclassification 提供三层动态分类分级引擎扩展。
//
// llm_client.go — Layer 3 Local LLM / vLLM HTTP 连接池客户端
package dynclassification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

// ──────────────────────────────────────────────
// LLM 客户端配置
// ──────────────────────────────────────────────

// LLMClientConfig LLM 客户端配置
type LLMClientConfig struct {
	// Endpoint LLM 服务地址。
	// P0-5 传输约束：跨信任边界的外送端点必须是 https；明文 http 仅允许环回主机
	// （本地开发与 sidecar 同机 mock LLM）。校验见 ValidateLLMTransport，非放行端点 fail closed。
	Endpoint string
	// ModelName 模型名称
	ModelName string
	// MaxConcurrency 最大并发推理数
	MaxConcurrency int
	// Timeout 单次请求超时
	Timeout time.Duration
	// MaxRetries 最大重试次数
	MaxRetries int
	// APIKey API 密钥（可选）
	APIKey string
}

// envLLMPlaintextOptIn 显式放行「非环回明文 http://」LLM 端点的开关名。
//
// 仅用于受控内网 / 专线内的本地模型服务（如 compose 网络里的 vllm 服务名），
// 取值须为 "true"。它只放宽传输层要求，**不会**恢复字段原值外送：
// 形态指纹（ShapeOf）与出网前的原值包含性自检始终生效（见 ClassifyShape）。
const envLLMPlaintextOptIn = "AGENT_LLM_ALLOW_INSECURE_HTTP_ENDPOINT"

// DefaultLLMClientConfig 默认 LLM 客户端配置。
// 默认端点为**空串**：Layer-3 默认关闭且不存在任何外送路径（P0-5）。
// 启用 Layer-3 时必须显式配置 AGENT_LLM_ENDPOINT 为 https 端点（或受控内网明文端点 + 显式豁免）；
// 空端点在 ValidateLLMTransport 中被拒绝，确保不会静默使用不安全的默认值。
func DefaultLLMClientConfig() LLMClientConfig {
	return LLMClientConfig{
		Endpoint:       "", // P0-5: 强制显式配置，不提供不安全的默认端点
		ModelName:      "qwen3.5",
		MaxConcurrency: 1,
		Timeout:        30 * time.Second,
		MaxRetries:     2,
	}
}

// ──────────────────────────────────────────────
// LLM 请求/响应
// ──────────────────────────────────────────────

// LLMRequest Layer-3 升级仲裁请求。
//
// ⚠️ P0-5「只送特征、不送原值」：本结构**刻意不承载字段原值**。
// 可跨越信任边界外送的只有三类信息：
//  1. 模式元数据（字段名 / 领域 / 参照标准）；
//  2. 值形态指纹 ValueShape（长度分桶 + 字符类别计数 + 结构化标识符形态，不可逆推原值）；
//  3. 前层候选判定 LLMCandidate（规则层 / NER 层的标签与置信度统计量）。
//
// 需要基于原值发起仲裁时请调用 ClassifyShape，由其在客户端内部完成指纹化与出网自检。
type LLMRequest struct {
	Field      string         // 字段名（模式元数据，非原值）
	Shape      ValueShape     // 值形态指纹（不含原值任何子串）
	Domain     string         // 业务领域（可选）
	Standard   string         // 参照标准（可选）
	Candidates []LLMCandidate // 前层候选标签与置信度（可选）
}

// LLMCandidate 前层（规则 / NER）给出的候选判定，仅含标签与统计量，不含原值。
type LLMCandidate struct {
	Source     string  // "rule:<id>" | "ner:<LABEL>"
	Level      string  // 候选安全等级
	Category   string  // 候选数据类别
	Confidence float64 // 候选置信度 [0,1]
}

// LLMResponse LLM 推理响应
type LLMResponse struct {
	Level      string  `json:"level"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning,omitempty"`
}

// ──────────────────────────────────────────────
// 值形态指纹（去标识化升级载荷）
// ──────────────────────────────────────────────

// ValueShape 字段值的非可逆形态指纹。
//
// 设计约束（对应设计文档 §12.1.1 P0-5 验收口径「仅提交字段名 + 值形态指纹」）：
//   - 只保留长度分桶、字符类别计数与结构化标识符形态；
//   - 不含原值的任何子串或字符位置信息（连「掩码后样例」也不外送，
//     因为保留首尾字符会给链接攻击留下还原线索）；
//   - 因此无法由指纹反推原值，可安全送往外部大模型。
type ValueShape struct {
	LengthBucket string // 长度分桶标记，如 "len=11"（≤32 精确）/ "len=33-64"
	Digits       int    // ASCII 数字字符数
	Latin        int    // ASCII 拉丁字母数
	CJK          int    // 汉字数
	Sep          int    // 空白 + 标点 + 符号类字符数
	Other        int    // 其余字符数（非拉丁文字、组合记号等）
	Identifier   string // 结构化标识符形态标签，如 "numeric-cn-mobile" / "free-text"
}

// Token 渲染为进入 prompt 的单行指纹串，
// 形如 "len=11 digits=11 letters=0 cjk=0 sep=0 other=0 ident=numeric-cn-mobile"。
func (s ValueShape) Token() string {
	ident := s.Identifier
	if ident == "" {
		ident = "unknown"
	}
	return fmt.Sprintf("%s digits=%d letters=%d cjk=%d sep=%d other=%d ident=%s",
		s.bucketOrUnknown(), s.Digits, s.Latin, s.CJK, s.Sep, s.Other, ident)
}

func (s ValueShape) bucketOrUnknown() string {
	if s.LengthBucket == "" {
		return "len=unknown"
	}
	return s.LengthBucket
}

// ShapeOf 计算字段值的形态指纹——这是升级路径上**唯一**允许读取原值的函数，
// 其返回值只含聚合统计量，不携带原文任何片段。
func ShapeOf(value string) ValueShape {
	var digits, latin, cjk, spaces, punct, other, total int
	hasAt := false
	for _, r := range value {
		total++
		switch {
		case r >= '0' && r <= '9':
			digits++
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			latin++
		case unicode.Is(unicode.Han, r):
			cjk++
		case unicode.IsSpace(r):
			spaces++
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			punct++
		default:
			other++
		}
		if r == '@' {
			hasAt = true
		}
	}

	return ValueShape{
		LengthBucket: lengthBucket(total),
		Digits:       digits,
		Latin:        latin,
		CJK:          cjk,
		Sep:          spaces + punct,
		Other:        other,
		Identifier:   identifierShape(total, digits, latin, cjk, punct, spaces, other, hasAt),
	}
}

// lengthBucket 将字符数泛化为长度分桶：短值给出精确长度（形态判定的必要信息），
// 长文本按桶泛化，避免以精确长度刻画自由文本内容。
func lengthBucket(n int) string {
	switch {
	case n <= 32:
		return "len=" + strconv.Itoa(n)
	case n <= 64:
		return "len=33-64"
	case n <= 128:
		return "len=65-128"
	case n <= 256:
		return "len=129-256"
	case n <= 1024:
		return "len=257-1024"
	default:
		return "len>1024"
	}
}

// identifierShape 仅依据「总长度 + 字符类别计数 + 符号存在性」判断值是否呈现结构化标识符形态。
// 严禁使用字符位置或内容前缀（例如「以 13 开头」「末位为 X」），否则指纹会重新泄露原值信息。
func identifierShape(n, digits, latin, cjk, punct, spaces, other int, hasAt bool) string {
	letterish := n - spaces - punct
	switch {
	case n == 0:
		return "empty"
	case hasAt && spaces == 0:
		return "email-like"
	case digits == n:
		// 纯数字：长度即唯一形态特征
		switch n {
		case 11:
			return "numeric-cn-mobile"
		case 15:
			return "numeric-imei"
		case 18:
			return "numeric-id-card"
		case 16, 19:
			return "numeric-bank-card"
		default:
			return "numeric"
		}
	case punct >= 1 && n <= 10 && digits >= 6 && cjk == 0 && spaces == 0:
		return "date-like"
	case digits*2 >= n && punct+spaces > 0 && cjk == 0:
		return "numeric-grouped" // 分隔符分组的数字串：带空格银行卡号、+86 电话等
	case cjk == n && n <= 4:
		return "cjk-name-like"
	case cjk*2 >= n && n >= 20:
		return "cjk-prose"
	case cjk > 0 && n >= 20:
		return "cjk-mixed-text"
	case cjk*2 >= n:
		return "cjk-text"
	case latin == n:
		return "alpha-token"
	case digits+latin == n && n <= 32:
		return "alnum-code"
	case spaces > 0 && letterish >= 4:
		return "free-text"
	case other > 0:
		return "mixed-encoded"
	default:
		return "mixed"
	}
}

// ──────────────────────────────────────────────
// LLM 连接池客户端
// ──────────────────────────────────────────────

// CircuitState 熔断器状态
type CircuitState int32

const (
	CircuitClosed   CircuitState = 0 // 闭合（正常通行）
	CircuitOpen     CircuitState = 1 // 打开（熔断阻断）
	CircuitHalfOpen CircuitState = 2 // 半开（试探自愈）
)

// LLMClient LLM 连接池客户端（内置三态熔断器与并发控制）
type LLMClient struct {
	config      LLMClientConfig
	client      *http.Client
	sem         chan struct{} // 并发信号量
	cbState     CircuitState
	failures    int
	lastFailure time.Time
	cooldown    time.Duration
	cbMu        sync.RWMutex

	// transportErr 端点传输安全性判定（构造期确定，此后只读）。
	// 非 nil 表示该端点被拒绝（明文 http 跨网段外送），所有外呼一律 fail closed。
	transportErr error

	// Layer-3 升级外送诊断计数（供 /ops/diagnostics 类上报直接读取）
	escalations  atomic.Int64 // 已构建并进入外送流程的仲裁请求数
	deidentified atomic.Int64 // 其中以「去标识化形态指纹」外送的数量

	// IsAvailable TTL 缓存，防止高并发下探测风暴
	availCache     atomic.Bool
	availCacheTime atomic.Int64 // Unix nano
	availCacheTTL  time.Duration
	availProbeMu   sync.Mutex // 串行化缓存刷新，防止并发探测风暴

	// Half-Open 状态在途试探请求数，防止刚恢复的 LLM 被瞬时并发流量二次打崩
	halfOpenInflight atomic.Int32
}

// maxHalfOpenProbes Half-Open 状态下允许并发通过的试探请求上限，
// 与 gateway.CircuitBreaker 的 halfOpenMax 保持一致的保护语义。
const maxHalfOpenProbes = 3

// NewLLMClient 创建 LLM 客户端。
// 构造期即完成端点传输安全判定：被判定为不安全的端点不会被静默使用，
// 后续所有 Classify / IsAvailable 调用一律 fail closed。
func NewLLMClient(config LLMClientConfig) *LLMClient {
	allowPlaintext := isPlaintextOptInEnabled()
	c := &LLMClient{
		config: config,
		client: &http.Client{
			Timeout: config.Timeout,
		},
		transportErr:  ValidateLLMTransport(config.Endpoint, allowPlaintext),
		sem:           make(chan struct{}, config.MaxConcurrency),
		cbState:       CircuitClosed,
		cooldown:      15 * time.Second,
		availCacheTTL: 5 * time.Second,
	}
	if c.transportErr != nil {
		slog.Error("Layer-3 LLM endpoint refused: plaintext egress blocked, escalation falls back to Safety Floor",
			"error", c.transportErr, "opt_in_env", envLLMPlaintextOptIn)
	}
	return c
}

// isPlaintextOptInEnabled 读取显式放行开关（仅放宽传输层，不恢复原值外送）。
func isPlaintextOptInEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(envLLMPlaintextOptIn)), "true")
}

// TransportError 返回端点传输安全判定结果；nil 表示端点允许外送。
func (c *LLMClient) TransportError() error { return c.transportErr }

// ──────────────────────────────────────────────
// 传输安全：拒绝明文外送（P0-5）
// ──────────────────────────────────────────────

// ValidateLLMTransport 校验 Layer-3 端点的传输安全性，失败即 fail closed：
//
//   - https / wss：任意主机放行（证书与链路可信度交给标准 http.Client 与 TLS 配置）；
//   - http：仅环回主机放行。环回放行是显式决定，因为本地开发以及 sidecar 单机部署形态下
//     Layer-3 指向同机 mock / vLLM 服务（见 DefaultLLMClientConfig 与 scripts/models/start_vllm_server.sh）；
//   - 其他 scheme、空主机、无法解析的地址：一律拒绝。
//
// allowPlaintext 为 true 时（envLLMPlaintextOptIn 显式置 "true"）额外放行非环回明文 http，
// 供受控内网 / 专线内的模型服务使用；它只放宽传输层，绝不放宽载荷去标识化约束。
func ValidateLLMTransport(endpoint string, allowPlaintext bool) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return fmt.Errorf("llm endpoint is empty")
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("llm endpoint %q invalid: %w", endpoint, err)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("llm endpoint %q has no host", endpoint)
	}

	switch strings.ToLower(u.Scheme) {
	case "https", "wss":
		return nil
	case "http":
		if isLoopbackHost(host) {
			return nil // 环回：明文不出机器，视为本地开发/同机 sidecar 形态
		}
		if allowPlaintext {
			return nil // 运维显式放行的受控内网明文端点
		}
		return fmt.Errorf(
			"llm endpoint %q uses plaintext http on non-loopback host %q: refused fail-closed "+
				"(use an https:// endpoint, or set %s=true for a controlled private network)",
			endpoint, host, envLLMPlaintextOptIn)
	default:
		return fmt.Errorf("llm endpoint %q scheme %q unsupported (https required, http allowed for loopback only)",
			endpoint, u.Scheme)
	}
}

// isLoopbackHost 判断主机名是否为环回地址。
// 刻意不做 DNS 解析：解析会引入外呼与解析结果可被篡改的风险，
// 因此只承认字面环回地址（127.0.0.0/8、::1）与 localhost 别名。
func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// checkCircuit 检查熔断器状态，返回是否允许通行。
// Half-Open 状态下仅允许最多 maxHalfOpenProbes 个并发试探请求通过，
// 并通过 releaseProbe（幂等）在试探结束时释放配额；超额请求直接拒绝走 Safety Floor 降级。
func (c *LLMClient) checkCircuit() (allowed bool, releaseProbe func()) {
	c.cbMu.RLock()
	state := c.cbState
	lastFail := c.lastFailure
	cooldown := c.cooldown
	c.cbMu.RUnlock()

	if state == CircuitClosed {
		return true, nil
	}

	if state == CircuitOpen {
		if time.Since(lastFail) > cooldown {
			c.cbMu.Lock()
			if c.cbState == CircuitOpen && time.Since(c.lastFailure) > c.cooldown {
				c.cbState = CircuitHalfOpen
				// 进入 Half-Open 重置配额，本请求自身占位成为第一个试探请求
				c.halfOpenInflight.Store(1)
				c.cbMu.Unlock()
				var once sync.Once
				return true, func() { once.Do(func() { c.halfOpenInflight.Add(-1) }) }
			}
			c.cbMu.Unlock()
		}
		return false, nil
	}

	// Half-Open: 限制并发试探配额，超额请求拒绝避免二次雪崩
	if c.halfOpenInflight.Add(1) > maxHalfOpenProbes {
		c.halfOpenInflight.Add(-1)
		return false, nil
	}
	var once sync.Once
	return true, func() { once.Do(func() { c.halfOpenInflight.Add(-1) }) }
}

// recordSuccess 记录一次成功调用，自愈重置熔断器
func (c *LLMClient) recordSuccess() {
	c.cbMu.Lock()
	defer c.cbMu.Unlock()
	c.failures = 0
	c.cbState = CircuitClosed
}

// recordFailure 记录一次失败调用，连续超阈值触发熔断
func (c *LLMClient) recordFailure() {
	c.cbMu.Lock()
	defer c.cbMu.Unlock()
	c.failures++
	c.lastFailure = time.Now()
	if c.failures >= 3 {
		c.cbState = CircuitOpen
	}
}

// Classify 使用 LLM 对已去标识化的升级请求执行分类（带熔断保护与重试）。
// LLMRequest 结构上不承载字段原值（P0-5），调用方需自行以 ShapeOf 完成指纹化。
func (c *LLMClient) Classify(ctx context.Context, req LLMRequest) (*LLMResponse, error) {
	return c.classify(ctx, req, "")
}

// ClassifyShape Layer-3 升级仲裁入口——「只送特征、不送原值」的唯一推荐路径（P0-5）。
//
// 传入的原值 value 只在客户端内部用于两件事：
//  1. 计算非可逆形态指纹 ShapeOf(value)；
//  2. 出网前的包含性自检（原始值不得出现在请求体任何位置）。
//
// 自检不通过时 fail closed：直接拒绝外送并返回错误，由调用方降级到 Safety Floor。
func (c *LLMClient) ClassifyShape(ctx context.Context, field, value string, candidates []LLMCandidate) (*LLMResponse, error) {
	return c.classify(ctx, LLMRequest{
		Field:      field,
		Shape:      ShapeOf(value),
		Candidates: candidates,
	}, value)
}

// classify 外送共用的内部实现；rawValue 仅用于出网自检，绝不拼入 prompt。
func (c *LLMClient) classify(ctx context.Context, req LLMRequest, rawValue string) (*LLMResponse, error) {
	// 传输安全优先于一切：明文跨网段端点直接拒绝（P0-5）
	if c.transportErr != nil {
		return nil, fmt.Errorf("LLM escalation refused: %w", c.transportErr)
	}

	// 熔断器快速拦截（Half-Open 下限制并发试探配额）
	allowed, releaseProbe := c.checkCircuit()
	if !allowed {
		return nil, fmt.Errorf("LLM circuit breaker is OPEN (cooldown active), request rejected")
	}
	if releaseProbe != nil {
		defer releaseProbe()
	}

	// 获取并发槽位
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// 构建 prompt（仅含模式元数据 + 形态指纹 + 前层候选）
	prompt := c.buildPrompt(req)

	// 出网自检：原值一旦出现在载荷中即中止外送
	if err := assertNoRawValue(prompt, rawValue); err != nil {
		return nil, err
	}
	c.escalations.Add(1)
	c.deidentified.Add(1)

	// 调用 LLM
	var lastErr error
	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		// 检查 context 是否已取消，避免无效重试
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		resp, err := c.callLLM(ctx, prompt)
		if err == nil {
			c.recordSuccess()
			return resp, nil
		}
		lastErr = err
		if attempt < c.config.MaxRetries {
			select {
			case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	c.recordFailure()
	return nil, fmt.Errorf("LLM classification failed after %d retries: %w", c.config.MaxRetries+1, lastErr)
}

// rawValueSelfCheckMinLen 原值自检的最小字节数。
// 极短值（如 "1"、"2024"）会以数字形式天然出现在形态统计串中，
// 逐字包含判定必然误报，因此只对足够长的值执行严格断言。
const rawValueSelfCheckMinLen = 8

// assertNoRawValue 出网前的最后防线：断言原始值未出现在将要送出的载荷里。
func assertNoRawValue(prompt, value string) error {
	if len(value) < rawValueSelfCheckMinLen {
		return nil
	}
	if strings.Contains(prompt, value) {
		return fmt.Errorf("LLM escalation blocked: prompt self-check detected original value in outgoing payload")
	}
	return nil
}

// maxPromptTokenLen 进入 prompt 的短文本（字段名 / 领域 / 标准 / 候选标签）最大 rune 长度，
// 用于约束元数据规模并抑制 prompt 注入。
const maxPromptTokenLen = 64

// maxPromptCandidates 写入 prompt 的前层候选标签数量上限。
const maxPromptCandidates = 5

// buildPrompt 构建分类 prompt。
//
// P0-5 约束：prompt 只包含①字段名等模式元数据、②值形态指纹（长度分桶 + 字符类别计数 +
// 结构化标识符形态）、③前层候选标签与置信度、④待判定的等级/类别问题；
// 不含字段原值的任何片段，也不含「掩码后样例」，外部模型因此零接触原数。
func (c *LLMClient) buildPrompt(req LLMRequest) string {
	var sb strings.Builder
	sb.WriteString("你是一个数据安全分类分级专家。以下仅提供字段的模式元数据与去标识化形态特征，")
	sb.WriteString("不含字段原值；请据此判断该字段的数据类别与安全等级。\n\n")

	name := promptToken(req.Field, maxPromptTokenLen)
	if name == "" {
		name = "(未提供)"
	}
	sb.WriteString("字段名: " + name + "\n")

	if domain := promptToken(req.Domain, maxPromptTokenLen); domain != "" {
		sb.WriteString("领域: " + domain + "\n")
	}
	if standard := promptToken(req.Standard, maxPromptTokenLen); standard != "" {
		sb.WriteString("参照标准: " + standard + "\n")
	}

	sb.WriteString("值形态指纹(已去标识化，无原值): " + req.Shape.Token() + "\n")

	if candidates := formatCandidates(req.Candidates); candidates != "" {
		sb.WriteString("前层候选判定: " + candidates + "\n")
	}

	sb.WriteString(`
待判定问题: 该字段应归入哪一个 level 与 category？

请返回 JSON 格式:
{"level": "public|internal|confidential|secret|top_secret", "category": "类别", "confidence": 0.0-1.0}

只返回 JSON，不要其他内容。`)
	return sb.String()
}

// promptToken 清洗将进入 prompt 的短字符串：换行/制表折叠为空格、丢弃控制字符、
// 去首尾空白并按 rune 截断，避免元数据污染 prompt 结构。
func promptToken(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	needsScrub := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsControl(r) {
			needsScrub = true
			break
		}
	}
	if needsScrub {
		var b strings.Builder
		b.Grow(len(s))
		for _, r := range s {
			switch {
			case r == '\n' || r == '\r' || r == '\t':
				b.WriteRune(' ')
			case unicode.IsControl(r):
				// 丢弃控制字符
			default:
				b.WriteRune(r)
			}
		}
		s = strings.TrimSpace(b.String())
	}

	if max > 0 && utf8.RuneCountInString(s) > max {
		runes := []rune(s)
		s = string(runes[:max])
	}
	return s
}

// formatCandidates 渲染前层候选判定为单行统计串（不含任何原值信息）。
func formatCandidates(cands []LLMCandidate) string {
	if len(cands) == 0 {
		return ""
	}
	parts := make([]string, 0, maxPromptCandidates)
	for _, cd := range cands {
		if len(parts) >= maxPromptCandidates {
			break
		}
		src := promptToken(cd.Source, maxPromptTokenLen)
		lvl := promptToken(cd.Level, maxPromptTokenLen)
		cat := promptToken(cd.Category, maxPromptTokenLen)
		if src == "" && lvl == "" && cat == "" {
			continue
		}
		var b strings.Builder
		b.WriteString(src)
		b.WriteString("(level=")
		if lvl == "" {
			b.WriteString("unknown")
		} else {
			b.WriteString(lvl)
		}
		b.WriteString(",category=")
		if cat == "" {
			b.WriteString("unknown")
		} else {
			b.WriteString(cat)
		}
		b.WriteString(",confidence=")
		b.WriteString(strconv.FormatFloat(cd.Confidence, 'f', 2, 64))
		b.WriteString(")")
		parts = append(parts, b.String())
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " | ")
}

// callLLM 调用 LLM API
func (c *LLMClient) callLLM(ctx context.Context, prompt string) (*LLMResponse, error) {
	body := map[string]interface{}{
		"model": c.config.ModelName,
		"messages": []map[string]string{
			{"role": "system", "content": "你是一个数据安全分类专家，只返回 JSON。"},
			{"role": "user", "content": prompt},
		},
		"temperature": 0.1,
		"max_tokens":  256,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.config.Endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 限制错误响应体最大 1MB
		return nil, fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("empty LLM response")
	}

	// 解析 LLM 返回的 JSON
	content := result.Choices[0].Message.Content
	var llmResp LLMResponse
	if err := json.Unmarshal([]byte(content), &llmResp); err != nil {
		return nil, fmt.Errorf("parse LLM JSON: %w (content: %s)", err, content)
	}

	return &llmResp, nil
}

// IsAvailable 检查 LLM 服务是否可用（带 TTL 缓存 + singleflight 串行化探测）。
// 缓存有效期 5 秒，过期后仅一个 goroutine 执行实际探测，其余等待复用结果。
// 端点被传输安全策略拒绝时直接返回 false（连探测都不外呼，fail closed）。
func (c *LLMClient) IsAvailable(ctx context.Context) bool {
	if c.transportErr != nil {
		return false
	}

	// 快速路径：缓存有效时直接返回
	cachedTime := time.Unix(0, c.availCacheTime.Load())
	if time.Since(cachedTime) < c.availCacheTTL {
		return c.availCache.Load()
	}

	// 慢路径：串行化探测，只有第一个 goroutine 执行 HTTP 请求
	c.availProbeMu.Lock()
	defer c.availProbeMu.Unlock()

	// 双重检查：可能在等锁期间已被其他 goroutine 刷新
	cachedTime = time.Unix(0, c.availCacheTime.Load())
	if time.Since(cachedTime) < c.availCacheTTL {
		return c.availCache.Load()
	}

	// 实际探测（使用 HEAD 请求避免对 POST 端点产生副作用）
	req, err := http.NewRequestWithContext(ctx, "HEAD", c.config.Endpoint, nil)
	if err != nil {
		c.availCache.Store(false)
		c.availCacheTime.Store(time.Now().UnixNano())
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.availCache.Store(false)
		c.availCacheTime.Store(time.Now().UnixNano())
		return false
	}
	resp.Body.Close()
	available := resp.StatusCode < 500
	c.availCache.Store(available)
	c.availCacheTime.Store(time.Now().UnixNano())
	return available
}

// LLMClientStats Layer-3 升级外送诊断快照（供 /ops/diagnostics 类上报直接读取）。
type LLMClientStats struct {
	// Escalations 已进入外送流程的 Layer-3 仲裁请求数
	Escalations int64 `json:"escalations"`
	// DeidentifiedPayloads 以「去标识化形态指纹」外送的载荷数。
	// 与 Escalations 恒等：升级载荷结构上不承载字段原值（P0-5）。
	DeidentifiedPayloads int64 `json:"deidentified_payloads"`
	// PayloadDeidentified 恒为 true，标记 Layer-3 出口仅送特征、不送原值
	PayloadDeidentified bool `json:"payload_deidentified"`
	// TransportSecure 端点是否走加密传输（https/wss）。环回明文 http 属于策略放行但并非加密链路，故为 false。
	TransportSecure bool `json:"transport_secure"`
	// EndpointHost 端点主机（仅主机名，用于运维定位，不含路径与查询串）
	EndpointHost string `json:"endpoint_host,omitempty"`
	// TransportError 端点被拒绝时的原因；nil 表示允许外送
	TransportError string `json:"transport_error,omitempty"`
}

// Stats 返回 Layer-3 升级外送诊断快照。
func (c *LLMClient) Stats() LLMClientStats {
	scheme, host := splitEndpointSchemeAndHost(c.config.Endpoint)
	st := LLMClientStats{
		Escalations:          c.escalations.Load(),
		DeidentifiedPayloads: c.deidentified.Load(),
		PayloadDeidentified:  true,
		TransportSecure:      c.transportErr == nil && (scheme == "https" || scheme == "wss"),
		EndpointHost:         host,
	}
	if c.transportErr != nil {
		st.TransportError = c.transportErr.Error()
	}
	return st
}

// splitEndpointSchemeAndHost 解析端点的 scheme（小写）与主机名；解析失败时返回空串。
func splitEndpointSchemeAndHost(endpoint string) (scheme, host string) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", ""
	}
	return strings.ToLower(u.Scheme), u.Hostname()
}

// Close 关闭客户端
func (c *LLMClient) Close() {
	c.client.CloseIdleConnections()
}
