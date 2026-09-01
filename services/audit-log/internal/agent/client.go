// Package agent provides an HTTP client to the upstream PrivShield agent.
// Package agent 封装到上游 PrivShield agent 的 HTTP 客户端。
//
// 本模块的 agent 客户端已精简为 thin wrapper，
// 通用 HTTP 逻辑由 pkg/agent 共享库提供。
package agent

import (
	pkgagent "github.com/fengzhizi319/PrivShield-go/pkg/agent"
	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/config"
)

// Client wraps the shared agent client with audit-log-specific endpoints.
type Client struct {
	*pkgagent.Client
}

// New creates a new agent client from the given config.
func New(cfg *config.Config) *Client {
	shared := pkgagent.New(pkgagent.Config{
		BaseURLs: cfg.AgentBaseURLs(),
		APIKey:   cfg.AgentAPIKey,
	})
	return &Client{Client: shared}
}
