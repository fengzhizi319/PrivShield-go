package main

import (
	"strings"
	"testing"

	"github.com/fengzhizi319/PrivShield-go/pkg/store/memory"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
)

// TestLeasedStoreRefusesSilentFallbackInStrictMode 验证 P0-4「禁静音降级」在
// service-hub 侧的落地：配置了 SERVICE_HUB_PG_DSN 却探测失败时，
// 默认（strict）必须上抛错误让进程退出；只有显式放宽才允许回退到无租约语义的存储。
func TestLeasedStoreRefusesSilentFallbackInStrictMode(t *testing.T) {
	// 不可解析的 DSN：探测在 ParseConfig 阶段即失败，测试无需等待网络超时。
	brokenDSN := "://not-a-valid-dsn"

	t.Run("strict mode refuses the fallback", func(t *testing.T) {
		cfg := &config.Config{PGDSN: brokenDSN, StrictStorage: true}
		_, err := initLeasedTaskStore(cfg, discardLogger())
		if err == nil {
			t.Fatal("strict storage mode must refuse falling back to a store without lease semantics")
		}
		if !strings.Contains(err.Error(), "strict storage mode") {
			t.Fatalf("error must name the strict-storage gate, got %v", err)
		}
		if !strings.Contains(err.Error(), brokenDSN) && !strings.Contains(err.Error(), "postgres") {
			t.Fatalf("error must wrap the underlying probe failure, got %v", err)
		}
	})

	t.Run("explicit opt-in keeps the legacy fallback", func(t *testing.T) {
		cfg := &config.Config{PGDSN: brokenDSN, StrictStorage: false}
		st, err := initLeasedTaskStore(cfg, discardLogger())
		if err != nil {
			t.Fatalf("non-strict mode must keep falling back, got %v", err)
		}
		if _, ok := st.(*memory.TaskStore); !ok {
			t.Fatalf("expected memory task store fallback, got %T", st)
		}
	})
}
