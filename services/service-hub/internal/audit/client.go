// Package audit implements the service-hub ➔ audit-log evidence client that binds every
// outbound (desensitized) data flow to a tamper-evident record.
// Package audit 实现「数据服务调度中枢 service-hub ➔ 独立存证节点 audit-log」的存证提交客户端，
// 用于把每一次出域（脱敏数据离开数据源边界）与一条不可篡改存证记录在代码层面强绑定
// （第十二章 P0-6 / Gate G-05：`audit` 阶段真实提交，提交失败按任务失败处理）。
//
// ==============================================================================
// 【设计背景与 fail-closed 语义】
// service-hub 的 6 阶段流水线（ingest ➔ fetch ➔ classify ➔ desensitize ➔ return ➔ audit）
// 中，第 ⑥ 阶段此前只推进任务状态位，仓库内不存在任何 hub ➔ audit-log 调用，
// 因此「每一次出域必然留痕」在代码层面不闭环：可以出域不存证，事后无法对账。
//
// 本客户端确立三条红线：
//  1. 【失败即任务失败】：提交返回任一错误（端点未配置 / 网络不可达 / 4xx/5xx / TLS 材料加载失败）
//     必须由流水线调用方上抛并将任务置为 `failed`，严禁 `logger.Warn` 后继续置 `done`；
//  2. 【禁止伪造链头】：`prev_hash` 由 audit-log 存储层唯一指派（服务端对非空 `prev_hash` 直接 400），
//     本客户端请求结构中根本不含该字段，杜绝调用方分叉或永久破坏哈希链；
//  3. 【真实指纹】：`input_hash` / `output_hash` 取自 engine 单趟处理返回的国密 SM3 指纹，
//     缺失时由本端对出域载荷计算 SM3 指纹（`pkg/crypto.SumSM3Hex`），保证可对账。
//
// 与 `internal/agent`、`internal/datasource` 两个出站客户端保持同一风格：
// 构造函数只吃 `*config.Config`、独立 `http.Client`（含超时与可选 mTLS）、统一 5 字段错误信封解析
// （`pkg/middleware.ErrorEnvelope`）、指数退避 + 随机抖动重试、`X-Request-ID` / `X-Idempotency-Key` 链路头透传。
// ==============================================================================
package audit

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	pkgagent "github.com/fengzhizi319/PrivShield/pkg/agent"
	"github.com/fengzhizi319/PrivShield/pkg/crypto"
	"github.com/fengzhizi319/PrivShield/pkg/metrics"
	"github.com/fengzhizi319/PrivShield/pkg/middleware"
	naming "github.com/fengzhizi319/PrivShield/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield/pkg/observability"
	"github.com/fengzhizi319/PrivShield/pkg/store"
	"github.com/fengzhizi319/PrivShield/pkg/validation"

	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/config"
	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/retry"
)

const (
	// RecordPath 是 audit-log 追加一条存证的 REST 端点，
	// 对应 services/audit-log/internal/handlers/handlers.go 中的 r.POST("/api/audit/logs", s.CreateLog)。
	RecordPath = "/api/audit/logs"

	// EvidenceUser 是本客户端提交存证时登记的调用方身份（中台服务账号，便于与业务侧存证区分）。
	EvidenceUser = "service-hub"

	defaultTimeout      = 10 * time.Second
	defaultRetryBase    = 500 * time.Millisecond
	defaultMaxRetries   = 3
	maxResponseSize     = 4 << 20 // 4 MiB：存证响应仅数个哈希字段，防御异常超大报文导致 OOM
	passthroughOp       = "classify"
	auditStatusSuccess  = "success"
	auditStatusFailed   = "failed"
	evidenceMetricLabel = "audit-log/records"
)

// Fail-closed 哨兵错误：任一命中都意味着「出域未被证明留痕」，调用方必须使任务失败。
var (
	// ErrNotConfigured 表示未配置 audit-log 存证端点（或客户端未构造），无法写入任何留痕。
	ErrNotConfigured = errors.New("audit-log evidence endpoint is not configured")

	// ErrRejected 表示 audit-log 明确拒绝了该存证（4xx，通常为契约/枚举不匹配）。
	ErrRejected = errors.New("audit-log rejected the evidence record")

	// ErrUnavailable 表示 audit-log 在全部重试后仍不可用（网络故障或 5xx）。
	ErrUnavailable = errors.New("audit-log evidence service is unavailable")

	// ErrMissingTask 表示缺少任务上下文，无法建立出域与 task_id 的绑定关系。
	ErrMissingTask = errors.New("outbound evidence requires a task context")
)

// RejectionError carries a non-2xx answer from the audit-log evidence endpoint.
// RejectionError 承载 audit-log 返回的非 2xx 结果，并尽力解析统一错误信封（5 字段）中的错误码。
type RejectionError struct {
	Endpoint     string // 实际请求的存证节点基础地址
	Status       int    // HTTP 状态码
	Code         string // 统一信封机器可读错误码（middleware.ErrorCodeFromStatus 兜底）
	Message      string // 统一信封人类可读摘要
	Op           string // 本次存证提交的隐私算子（便于定位枚举不匹配）
	DatasourceID string // 本次存证的数据源 ID（便于定位非法/未登记数据源）
}

// Error 实现 error 接口。
func (e *RejectionError) Error() string {
	return fmt.Sprintf("audit-log %s returned %d (%s) for datasource=%q operation=%q: %s",
		e.Endpoint, e.Status, e.Code, e.DatasourceID, e.Op, e.Message)
}

// Unwrap 使 errors.Is(err, ErrRejected) 成立，供上层按语义分类处理。
func (e *RejectionError) Unwrap() error {
	if e.Status >= http.StatusInternalServerError {
		return ErrUnavailable
	}
	return ErrRejected
}

// Client submits outbound-flow evidence to the standalone audit-log service.
// Client 是 hub ➔ audit-log 的出站存证客户端：多副本轮询、超时受控、可选 mTLS、失败必返错误。
type Client struct {
	baseURLs       []string           // 存证节点基础地址列表（多副本 failover，轮询选取）
	apiKey         string             // audit-log 入站鉴权 Key（Authorization: Bearer，见 pkg/middleware/auth.go）
	httpClient     *http.Client       // 带超时与可选 mTLS 的 HTTP 客户端
	logger         *slog.Logger       // 结构化日志器
	mc             *metrics.Collector // 可选 Prometheus 观测器（复用上游调用指标）
	maxRetries     int                // 网络错误与 5xx 的最大重试次数
	retryBaseDelay time.Duration      // 指数退避基础时间
	rrIndex        uint64             // 多副本轮询计数
	initErr        error              // 构造期致命错误（如 TLS 材料加载失败），提交时直接上抛
}

// New creates the evidence client from service-hub configuration.
// New 依据 service-hub 运行配置构造存证客户端。
//
// 执行步骤：
//  1. 读取存证端点列表 / API Key / 超时 / 重试次数（配置缺失不在此处报错，交由提交时 fail-closed）；
//  2. 当 SERVICE_HUB_AUDIT_LOG_TLS_ENABLED=true 时构建 TLS 1.3 客户端传输（证书/私钥/CA 缺失或
//     加载失败写入 initErr，绝不静默降级为明文）；
//  3. 返回 *Client（永不返回 nil，便于调用方无条件持有）。
func New(cfg *config.Config, mc *metrics.Collector) *Client {
	c := &Client{
		mc:             mc,
		logger:         slog.Default(),
		retryBaseDelay: defaultRetryBase,
	}
	if cfg == nil {
		c.initErr = ErrNotConfigured
		return c
	}

	c.baseURLs = trimURLs(cfg.AuditLogBaseURLs)
	c.apiKey = cfg.AuditLogAPIKey
	c.maxRetries = cfg.AuditLogMaxRetries
	if c.maxRetries < 0 {
		c.maxRetries = defaultMaxRetries
	}
	timeout := time.Duration(cfg.AuditLogTimeout) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	transport := &http.Transport{
		MaxIdleConns:        50,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	if cfg.AuditLogTLSEnabled {
		tlsConfig, err := clientTLSConfig(cfg)
		if err != nil {
			// fail-closed：证书材料异常时记录构造错误，后续每次提交都返回该错误（任务失败），
			// 而不是悄悄以明文继续出域留痕。
			c.initErr = fmt.Errorf("audit-log mTLS configuration invalid: %w", err)
			c.logger.Error("audit-log evidence client TLS setup failed; evidence submissions will fail closed",
				"error", c.initErr.Error())
		} else {
			transport.TLSClientConfig = tlsConfig
		}
	}
	c.httpClient = &http.Client{Transport: transport, Timeout: timeout}

	return c
}

// Configured reports whether at least one evidence endpoint is available.
// Configured 报告是否至少配置了一个可用存证端点（且 TLS 材料可用）。
func (c *Client) Configured() bool {
	return c != nil && c.initErr == nil && len(c.baseURLs) > 0
}

// Endpoint returns the first configured evidence endpoint (for diagnostics/readiness).
// Endpoint 返回首个已配置存证端点地址（用于就绪探针与诊断透出）。
func (c *Client) Endpoint() string {
	if c == nil || len(c.baseURLs) == 0 {
		return ""
	}
	return c.baseURLs[0]
}

// OutboundFlow describes one data flow leaving a datasource boundary.
// OutboundFlow 描述一次「数据离开数据源边界」的出域事实，是流水线 audit 阶段的输入。
type OutboundFlow struct {
	Task          *store.Task // 流水线任务实体（提供 task_id / api_code / datasource_id / operation / 耗时）
	Protocol      string      // 入口协议标识："rest" | "grpc" | "lease"（写入 parameters 便于对账）
	SecurityLevel string      // 可选 L1~L5 数据分级（由分类分级结果导出；空则不下发）
	Input         any         // 出域前的原始载荷（用于计算 input_hash，除非 Engine 已给出指纹）
	Output        any         // 实际出域的脱敏后载荷（用于计算 output_hash）
	InputHash     string      // 可选：engine 单趟处理产出的输入指纹（优先于本地计算）
	OutputHash    string      // 可选：engine 单趟处理产出的输出指纹（优先于本地计算）
	Algorithm     string      // 可选：采用的算法标识（如 "SM4-GCM" / "field_mask"）
	Error         string      // 非空表示该次出域以失败告终，存证 status 记为 "failed"
}

// Result carries the identifiers audit-log assigns to the accepted evidence.
// Result 承载 audit-log 受理存证后回写的标识，用于任务日志与事后对账（G-10 哈希链验真）。
type Result struct {
	LogID         string `json:"id"`
	SnapshotID    string `json:"snapshot_id"`
	IntegrityHash string `json:"integrity_hash"`
	PrevHash      string `json:"prev_hash"`
}

// RecordOutbound writes one outbound-flow evidence row and returns its identifiers.
// RecordOutbound 提交一条出域存证记录（POST /api/audit/logs）。
//
// 返回错误即代表「该次出域未被证明留痕」，调用方 MUST 使任务失败：
//   - c 为 nil 或未配置端点 → ErrNotConfigured；
//   - 缺少任务上下文 → ErrMissingTask；
//   - 4xx → *RejectionError（可 errors.Is 到 ErrRejected，不重试）；
//   - 5xx / 网络故障且重试耗尽 → *RejectionError 或包装错误（可 errors.Is 到 ErrUnavailable）。
func (c *Client) RecordOutbound(ctx context.Context, flow OutboundFlow) (*Result, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: evidence client was not constructed", ErrNotConfigured)
	}
	if c.initErr != nil {
		return nil, c.initErr
	}
	if len(c.baseURLs) == 0 {
		return nil, fmt.Errorf("%w: set SERVICE_HUB_AUDIT_LOG_URLS (or SERVICE_HUB_AUDIT_HTTP) to the audit-log service", ErrNotConfigured)
	}
	rec, err := buildRecord(flow)
	if err != nil {
		return nil, err
	}

	endpoint := c.pickEndpoint()
	start := time.Now()
	respBody, err := c.postJSON(ctx, endpoint, rec)
	elapsed := time.Since(start)
	if c.mc != nil {
		status := "success"
		if err != nil {
			status = "error"
		}
		c.mc.RecordAgentCall(evidenceMetricLabel, status, elapsed.Seconds())
	}
	if err != nil {
		return nil, err
	}

	res := &Result{}
	_ = decodeJSON(respBody, res)
	c.logger.Info("outbound evidence recorded",
		"task_id", rec.TaskID,
		"datasource_id", rec.DatasourceID,
		"api_code", rec.APICode,
		"operation", rec.Operation,
		"status", rec.Status,
		"audit_log_id", res.LogID,
		"integrity_hash", res.IntegrityHash,
		"endpoint", endpoint,
		"latency_ms", elapsed.Milliseconds(),
	)
	return res, nil
}

// RecordOutboundEvidence is the pipeline-facing helper invoked by the audit stage.
// RecordOutboundEvidence 是流水线 audit 阶段的唯一入口：即使客户端未构造（nil）也会返回错误，
// 使调用方的 fail-closed 分支必然执行，绝不会静默把任务标成 done。
func RecordOutboundEvidence(ctx context.Context, c *Client, flow OutboundFlow) (*Result, error) {
	return c.RecordOutbound(ctx, flow)
}

// FailureClass maps an evidence submission error onto a bounded retryability verdict
// shared by all three pipeline execution sites (REST, gRPC, lease worker).
// FailureClass 将存证提交错误归一化为「错误分类 + 是否可重试」，
// 供 REST / gRPC / 租约工作器三条流水线共用同一判定，避免各处自行猜测。
// 分类取值取自 internal/retry 的有界枚举，与后台重试判定表同源维护：
//   - evidence_rejected / evidence_unconfigured / evidence_invalid：重试不可能改变结果；
//   - evidence_unavailable：audit-log 暂时不可用（5xx/网络），整任务重投有意义；
//   - evidence：未预期的内部错误（保守按可重试处理）。
func FailureClass(err error) (errorClass string, retryable bool) {
	switch {
	case errors.Is(err, ErrRejected):
		return retry.ClassEvidenceRejected, false
	case errors.Is(err, ErrNotConfigured):
		return retry.ClassEvidenceUnconfigured, false
	case errors.Is(err, ErrMissingTask):
		return retry.ClassEvidenceInvalid, false
	case errors.Is(err, ErrUnavailable):
		return retry.ClassEvidenceUnavailable, true
	default:
		return retry.ClassEvidence, true
	}
}

// pickEndpoint 以原子轮询方式在多个存证副本间均衡选取基础地址。
func (c *Client) pickEndpoint() string {
	if len(c.baseURLs) == 1 {
		return c.baseURLs[0]
	}
	idx := atomic.AddUint64(&c.rrIndex, 1)
	return c.baseURLs[(idx-1)%uint64(len(c.baseURLs))]
}

// postJSON 发送存证请求，带指数退避 + 随机抖动重试与响应体上限保护。
//
// 重试判定与 internal/datasource 客户端一致：网络错误与 5xx 可重试；4xx 为契约/参数错误，
// 立即返回（重试只会重复产生被拒记录），并且 4xx 不被吞掉——必然上抛至任务失败分支。
func (c *Client) postJSON(ctx context.Context, endpoint string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal evidence record: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := c.retryBaseDelay * time.Duration(1<<(attempt-1))
			if delay > 0 {
				sleepDur := delay + time.Duration(rand.Int64N(int64(delay/2)+1))
				c.logger.Warn("retrying audit-log evidence submission",
					"attempt", attempt+1, "max_attempts", c.maxRetries+1, "backoff", sleepDur.String())
				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("evidence retry cancelled: %w", ctx.Err())
				case <-time.After(sleepDur):
				}
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+RecordPath, bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("create evidence request: %w", err)
		}
		c.setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%w: audit-log %s unreachable: %v", ErrUnavailable, endpoint, err)
			continue
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read audit-log response: %w", readErr)
			continue
		}
		if len(body) > maxResponseSize {
			return nil, fmt.Errorf("audit-log response too large: exceeds %d bytes", maxResponseSize)
		}

		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			return body, nil
		}

		rej := &RejectionError{
			Endpoint:     endpoint,
			Status:       resp.StatusCode,
			Code:         middleware.ErrorCodeFromStatus(resp.StatusCode),
			Message:      truncate(extractMessage(body), 512),
			Op:           payloadOperation(payload),
			DatasourceID: payloadDatasource(payload),
		}
		if resp.StatusCode < http.StatusInternalServerError {
			// 4xx：契约级拒绝，重试无意义，直接上抛使任务失败并可观测。
			// 同时包装哨兵，使任务 error 文本自带「被存证节点拒绝」语义（告警可读）。
			return nil, fmt.Errorf("%w: %w", ErrRejected, rej)
		}
		lastErr = rej
	}

	return nil, fmt.Errorf("%w after %d attempts: %w", ErrUnavailable, c.maxRetries+1, lastErr)
}

// setHeaders 注入 Bearer 鉴权头与分布式链路头。
// 鉴权头格式与 pkg/middleware.Auth 的 extractBearer 严格匹配（Authorization: Bearer <key>）。
func (c *Client) setHeaders(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	if rid := pkgobs.RequestIDFromContext(req.Context()); rid != "" {
		req.Header.Set("X-Request-ID", rid)
		req.Header.Set("X-Trace-ID", rid)
	}
	if ik := pkgagent.IdempotencyKeyFromContext(req.Context()); ik != "" {
		req.Header.Set("X-Idempotency-Key", ik)
	}
}

// record 是 audit-log POST /api/audit/logs 的请求载荷。
// 字段名与 services/audit-log/internal/handlers/handlers.go 中 CreateLog 的匿名请求结构逐一对应；
// 注意：刻意不包含 prev_hash —— 链头只能由服务端存储层指派（否则 400 INVALID_ARGUMENT）。
type record struct {
	TaskID        string `json:"task_id"`
	APICode       string `json:"api_code,omitempty"`
	DatasourceID  string `json:"datasource_id,omitempty"`
	DataSource    string `json:"datasource"`
	Operation     string `json:"operation"`
	SecurityLevel string `json:"security_level,omitempty"`
	InputHash     string `json:"input_hash,omitempty"`
	OutputHash    string `json:"output_hash,omitempty"`
	Algorithm     string `json:"algorithm,omitempty"`
	Parameters    any    `json:"parameters,omitempty"`
	InputRows     int    `json:"input_rows,omitempty"`
	OutputRows    int    `json:"output_rows,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
	User          string `json:"user"`
	Status        string `json:"status"`
	Timestamp     string `json:"timestamp"`
	Error         string `json:"error,omitempty"`
}

// buildRecord normalizes an OutboundFlow into a wire record accepted by audit-log.
// buildRecord 将出域事实归一化为 audit-log 可受理的存证记录：
//
//  1. task_id / datasource_id 为绑定主键，缺失即拒绝（fail-closed，不写伪存证）；
//  2. datasource_id 优先取 canonical 值，回退顺序 datasource_id ➔ source ➔ api_code；
//     api_code 缺失时由注册表反查补全，与 audit-log 侧 naming.Normalize 结果保持一致；
//  3. operation 归一到 audit-log 白名单 validation.AuditOperations；中枢特有的
//     "none"（无变换直传）在审计枚举中无对应值，按「已定级直传」记为 "classify"，
//     真实算子保留在 parameters.hub_operation，避免掩盖事实；
//  4. status 归一到 validation.AuditStatuses（success | failed）；
//  5. 指纹缺失时本地以国密 SM3 计算，保证出域输入/输出可事后比对。
func buildRecord(flow OutboundFlow) (*record, error) {
	task := flow.Task
	if task == nil {
		return nil, ErrMissingTask
	}

	dsID := firstNonEmpty(task.DatasourceID, task.Source)
	if dsID == "" {
		dsID = task.APICode
	}
	if strings.TrimSpace(dsID) == "" {
		return nil, fmt.Errorf("%w: task %s has no datasource_id", ErrMissingTask, task.ID)
	}
	if canon, err := naming.NormalizeDataSourceID(dsID); err == nil && canon != "" {
		dsID = canon
	}
	apiCode := task.APICode
	if apiCode == "" {
		apiCode = naming.APICodeForDataSource(dsID)
	}

	status := auditStatusSuccess
	if strings.TrimSpace(flow.Error) != "" {
		status = auditStatusFailed
	}
	if !allowed(status, validation.AuditStatuses) {
		status = auditStatusFailed
	}

	op := canonicalOperation(task.Operation)
	level := strings.ToUpper(strings.TrimSpace(flow.SecurityLevel))
	if level != "" && !allowed(level, validation.SensitivityLevels) {
		level = ""
	}

	inHash := strings.TrimSpace(flow.InputHash)
	if inHash == "" {
		inHash = Fingerprint(flow.Input)
	}
	outHash := strings.TrimSpace(flow.OutputHash)
	if outHash == "" {
		outHash = Fingerprint(outPayload(flow))
	}

	params := map[string]any{
		"service":       EvidenceUser,
		"stage":         "audit",
		"protocol":      firstNonEmpty(flow.Protocol, "rest"),
		"hub_operation": task.Operation,
		"trace_id":      task.TraceID,
		"retry_count":   task.RetryCount,
	}

	return &record{
		TaskID:        task.ID,
		APICode:       apiCode,
		DatasourceID:  dsID,
		DataSource:    dsID,
		Operation:     op,
		SecurityLevel: level,
		InputHash:     inHash,
		OutputHash:    outHash,
		Algorithm:     flow.Algorithm,
		Parameters:    params,
		InputRows:     RowCount(flow.Input),
		OutputRows:    RowCount(outPayload(flow)),
		DurationMs:    taskDuration(task),
		User:          EvidenceUser,
		Status:        status,
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		Error:         truncate(flow.Error, 2048),
	}, nil
}

// outPayload returns the payload that actually left the boundary.
// outPayload 返回真正出域的载荷：无脱敏结果时（operation=none 直传）等同于输入。
func outPayload(flow OutboundFlow) any {
	if flow.Output != nil {
		return flow.Output
	}
	return flow.Input
}

// canonicalOperation maps a hub operation onto the audit-log operation whitelist.
func canonicalOperation(op string) string {
	op = strings.ToLower(strings.TrimSpace(op))
	if allowed(op, validation.AuditOperations) {
		return op
	}
	return passthroughOp
}

func allowed(v string, whitelist []string) bool {
	if v == "" {
		return false
	}
	for _, item := range whitelist {
		if item == v {
			return true
		}
	}
	return false
}

// EngineFingerprints extracts the SM3 input/output fingerprints computed by the
// engine inside the /v1/agent/process "summary" block.
// EngineFingerprints 从引擎 /v1/agent/process 返回的 summary 中提取输入/输出指纹（国密 SM3，P2-3）：
// REST 与 gRPC 两条流水线共用同一提取逻辑，存证直接沿用引擎侧指纹以便事后对账；
// 字段缺失或非字符串时返回空串，由 buildRecord 以同一 SM3 算法兜底计算本地指纹。
func EngineFingerprints(summary map[string]any) (inputHash, outputHash string) {
	if summary == nil {
		return "", ""
	}
	in, _ := summary["input_hash"].(string)
	out, _ := summary["output_hash"].(string)
	return strings.TrimSpace(in), strings.TrimSpace(out)
}

// Fingerprint returns the SM3 hex digest identifying a payload (stable across retries).
// Fingerprint 计算载荷的国密 SM3 十六进制指纹，作为 input_hash / output_hash 的本地兜底。
func Fingerprint(payload any) string {
	if payload == nil {
		return ""
	}
	switch v := payload.(type) {
	case string:
		if strings.TrimSpace(v) == "" || v == "null" || v == "{}" {
			return ""
		}
		return crypto.SumSM3Hex([]byte(v))
	case []byte:
		if len(v) == 0 {
			return ""
		}
		return crypto.SumSM3Hex(v)
	default:
		b, err := json.Marshal(payload)
		if err != nil {
			return ""
		}
		return crypto.SumSM3Hex(b)
	}
}

// RowCount counts records in a generic payload (0 when the shape is unknown).
// RowCount 统计通用载荷中的记录行数，用于存证的 input_rows / output_rows。
func RowCount(payload any) int {
	switch v := payload.(type) {
	case nil:
		return 0
	case []map[string]any:
		return len(v)
	case []map[string]string:
		return len(v)
	case []any:
		return len(v)
	case map[string]any:
		if len(v) == 0 {
			return 0
		}
		return 1
	case string:
		if strings.TrimSpace(v) == "" {
			return 0
		}
		var parsed any
		if err := json.Unmarshal([]byte(v), &parsed); err != nil {
			return 0
		}
		return RowCount(parsed)
	default:
		return 0
	}
}

// MaxSensitivityLevel walks a generic classification report and returns the highest L1~L5 level.
// MaxSensitivityLevel 递归遍历分类分级报告（JSON 泛型结构），返回其中最高的敏感级别；
// 未发现任何合法级别时返回空串（调用方 MUST 按 fail-closed 处理，不得替换为默认等级）。
//
// 识别 level / level_id / overall_level 三种键，并同时接受两套词表：存证与调度契约使用的
// L1~L5 标识、engine 三层漏斗的 canonical 名称（public…top_secret）。排名口径唯一来源于
// pkg/naming，中枢不再自建副本（P1-1 / P1-5）。
func MaxSensitivityLevel(report any) string {
	best := 0
	var walk func(node any)
	walk = func(node any) {
		switch v := node.(type) {
		case []any:
			for _, item := range v {
				walk(item)
			}
		case []map[string]any:
			for _, item := range v {
				walk(item)
			}
		case map[string]any:
			for key, item := range v {
				if key == "level" || key == "level_id" || key == "overall_level" {
					if lvl, ok := item.(string); ok {
						if n := naming.SecurityLevelRank(lvl); n > best {
							best = n
						}
						continue
					}
				}
				walk(item)
			}
		}
	}
	walk(report)
	if best == 0 {
		return ""
	}
	return fmt.Sprintf("L%d", best)
}

// taskDuration returns the end-to-end pipeline duration carried by the evidence row.
func taskDuration(task *store.Task) int64 {
	if task == nil {
		return 0
	}
	if task.DurationMs > 0 {
		return task.DurationMs
	}
	if task.StartedAt != nil {
		return time.Since(*task.StartedAt).Milliseconds()
	}
	return time.Since(task.CreatedAt).Milliseconds()
}

// clientTLSConfig 构建访问 audit-log 的 TLS 1.3 客户端配置（支持双向证书认证）。
// 证书材料优先取存证专用配置，未设置时复用中枢自身的客户端证书身份。
func clientTLSConfig(cfg *config.Config) (*tls.Config, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}

	certFile := firstNonEmpty(cfg.AuditLogTLSCertFile, cfg.TLSCertFile)
	keyFile := firstNonEmpty(cfg.AuditLogTLSKeyFile, cfg.TLSKeyFile)
	caFile := firstNonEmpty(cfg.AuditLogTLSCAFile, cfg.TLSCAFile)

	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load client keypair: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read ca file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("append ca certificate failed")
		}
		tlsConfig.RootCAs = pool
	}
	return tlsConfig, nil
}

func decodeJSON(raw []byte, target any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, target)
}

func extractMessage(body []byte) string {
	var env middleware.ErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil {
		if env.Message != "" {
			return env.Message
		}
	}
	var loose struct {
		Error   string `json:"error"`
		Detail  string `json:"detail"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &loose); err == nil {
		return firstNonEmpty(loose.Message, loose.Detail, loose.Error)
	}
	return string(body)
}

func payloadOperation(payload any) string {
	if r, ok := payload.(*record); ok {
		return r.Operation
	}
	return ""
}

func payloadDatasource(payload any) string {
	if r, ok := payload.(*record); ok {
		return r.DatasourceID
	}
	return ""
}

func trimURLs(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		if u = strings.TrimRight(strings.TrimSpace(u), "/"); u != "" {
			out = append(out, u)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
