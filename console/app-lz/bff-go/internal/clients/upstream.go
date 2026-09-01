// Package clients 的上游契约层：统一错误码（api_rename_design.md §6.3）、
// 数据源标识归一化入口，以及「降级必须显式标记」的辅助类型。
//
// 本文件的存在理由：BFF 过去的降级兜底会把「上游 404 / 不可达」伪装成正常数据
// （D-01/D-02/D-11），这里把所有边界收敛成两类结果：
//  1. 契约违规 → *UpstreamError（400/409，写侧 fail-closed）；
//  2. 上游不可用 → 返回值中携带 Source="fallback" + Detail（读侧可演示，但必须可辨识）。
package clients

import (
	"fmt"
	"net/http"

	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/catalog"
	naming "github.com/fengzhizi319/PrivShield-go/pkg/naming"
)

// §6.3 统一错误码。
const (
	CodeInvalidDatasourceID   = "INVALID_DATASOURCE_ID"
	CodeAmbiguousSource       = "AMBIGUOUS_SOURCE"
	CodeAPIDatasourceMismatch = "API_DATASOURCE_MISMATCH"
	CodeInvalidAPICode        = "INVALID_API_CODE"
	CodeReservedDatasource    = "RESERVED_DATASOURCE"
	CodeInvalidRequest        = "INVALID_REQUEST"
	CodeUpstreamUnavailable   = "UPSTREAM_UNAVAILABLE"
)

// UpstreamError 表示一次可呈现给调用方的契约/上游错误。
type UpstreamError struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Field    string   `json:"field,omitempty"`
	Received string   `json:"received,omitempty"`
	Allowed  []string `json:"allowed,omitempty"`
	Status   int      `json:"-"`
	Err      error    `json:"-"`
}

func (e *UpstreamError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap keeps the underlying cause inspectable via errors.Is/errors.As.
func (e *UpstreamError) Unwrap() error { return e.Err }

// StatusCode 返回该错误应映射的 HTTP 状态码。
func (e *UpstreamError) StatusCode() int {
	if e.Status != 0 {
		return e.Status
	}
	switch e.Code {
	case CodeReservedDatasource:
		return http.StatusConflict
	case CodeUpstreamUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadRequest
	}
}

// Body 渲染 §6.3 的统一错误体：{"error":{code,message,details},"via":...}。
func (e *UpstreamError) Body(via string) map[string]any {
	details := map[string]any{}
	if e.Field != "" {
		details["field"] = e.Field
	}
	if e.Received != "" {
		details["received"] = e.Received
	}
	if len(e.Allowed) > 0 {
		details["allowed"] = e.Allowed
	}
	errObj := map[string]any{"code": e.Code, "message": e.Message}
	if len(details) > 0 {
		errObj["details"] = details
	}
	return map[string]any{"error": errObj, "via": via}
}

// invalidDatasourceError 把 naming 的归一化失败转换为 INVALID_DATASOURCE_ID。
func invalidDatasourceError(received string, cause error) *UpstreamError {
	return &UpstreamError{
		Code:     CodeInvalidDatasourceID,
		Message:  fmt.Sprintf("unknown datasource id %q", received),
		Field:    "datasource_id",
		Received: received,
		Allowed:  naming.ActiveDataSourceIDs(),
		Status:   http.StatusBadRequest,
		Err:      cause,
	}
}

// reservedDatasourceError 把预留位（ds_mock3 / ds_mock4）转换为 409。
func reservedDatasourceError(received, datasourceID string) *UpstreamError {
	return &UpstreamError{
		Code:     CodeReservedDatasource,
		Message:  fmt.Sprintf("datasource %s is registered but not implemented yet", datasourceID),
		Field:    "datasource_id",
		Received: received,
		Allowed:  naming.ActiveDataSourceIDs(),
		Status:   http.StatusConflict,
	}
}

// ResolveDatasourceID normalizes any accepted inbound representation and rejects
// unknown (400) and reserved (409) values instead of silently defaulting to the
// medical datasource (defect D-11).
//
// ResolveDatasourceID 归一化入站数据源标识，并对未知/预留值 fail-closed。
func ResolveDatasourceID(raw string) (string, error) {
	entry, err := naming.Normalize(raw)
	if err != nil {
		return "", invalidDatasourceError(raw, err)
	}
	if entry.Status != naming.StatusActive {
		return "", reservedDatasourceError(raw, entry.DataSourceID)
	}
	return entry.DataSourceID, nil
}

// ValidationErrorFor converts a catalog.ValidationError into an *UpstreamError so
// handlers keep a single error rendering path.
// ValidationErrorFor 把 catalog 的校验错误转换为统一错误体。
func ValidationErrorFor(err error) *UpstreamError {
	if ve, ok := err.(*catalog.ValidationError); ok {
		status := http.StatusBadRequest
		if ve.Code == CodeReservedDatasource {
			status = http.StatusConflict
		}
		return &UpstreamError{
			Code:     ve.Code,
			Message:  ve.Message,
			Field:    ve.Field,
			Received: ve.Received,
			Allowed:  naming.ActiveDataSourceIDs(),
			Status:   status,
			Err:      err,
		}
	}
	if ue, ok := err.(*UpstreamError); ok {
		return ue
	}
	return &UpstreamError{
		Code:    CodeInvalidRequest,
		Message: err.Error(),
		Status:  http.StatusBadRequest,
		Err:     err,
	}
}

// upstreamUnavailableError 表示真实上游返回了非 2xx（读侧降级时使用）。
func upstreamUnavailableError(service, path string, status int) *UpstreamError {
	return &UpstreamError{
		Code:    CodeUpstreamUnavailable,
		Message: fmt.Sprintf("%s returned HTTP %d for %s", service, status, path),
		Status:  http.StatusServiceUnavailable,
	}
}
