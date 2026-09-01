// Package observability 提供可观测性基础设施。
//
// 通用实现已下沉至 pkg/observability；本包保留向后兼容的入口与引擎专属处理器。
package observability

import (
	"github.com/gin-gonic/gin"

	pkgobs "github.com/fengzhizi319/PrivShield/pkg/observability"
)

// InitLogger 初始化结构化日志。
// 委托给 pkg/observability.InitLogger，保持原有单参数签名（默认 JSON 格式）。
func InitLogger(level string) {
	pkgobs.InitLogger("json", level)
}

// RequestLogger 记录 HTTP 请求日志。
// 委托给 pkg/observability.RequestLogger，统一全仓库访问日志字段。
func RequestLogger() gin.HandlerFunc {
	return pkgobs.RequestLogger()
}
