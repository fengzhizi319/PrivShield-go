// Package agent provides an HTTP client to the upstream PrivShield agent.
// Package agent 封装到上游 PrivShield agent 的 HTTP 客户端。
//
// 本模块的 agent 客户端已精简为 thin wrapper，
// 通用 HTTP 逻辑由 pkg/agent 共享库提供。
package agent

import (
	"fmt"

	pkgagent "github.com/fengzhizi319/PrivShield-go/pkg/agent"
	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/config"
)

// Client wraps the shared agent client with audit-log-specific endpoints.
type Client struct {
	*pkgagent.Client
}

// New creates a new agent client from the given config.
// New 函数根据配置构造 Agent 客户端：
// 由 PRIVACY_AGENT_TLS_* / PRIVACY_AGENT_TLCP_* 显式配置构建出站传输信任
// （CA 文件不可读即报错，fail-fast），全部缺省时保持默认 HTTP 行为。
func New(cfg *config.Config) (*Client, error) {
	tlsCfg, err := cfg.AgentTLSClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build agent TLS client config: %w", err)
	}
	tlcpCfg, err := cfg.AgentTLCPClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build agent TLCP client config: %w", err)
	}
	shared := pkgagent.New(pkgagent.Config{
		BaseURLs:   cfg.AgentBaseURLs(),
		APIKey:     cfg.AgentAPIKey,
		TLSConfig:  tlsCfg,
		TLCPConfig: tlcpCfg,
	})
	return &Client{Client: shared}, nil
}
