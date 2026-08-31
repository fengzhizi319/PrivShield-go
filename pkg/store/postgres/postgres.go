// Package postgres provides a PostgreSQL-backed implementation of the store interfaces.
// Package postgres 提供基于 PostgreSQL 的 store 接口实现。
//
// 此包支持 Phase B 多副本 Hub 部署，提供原子租约（FOR UPDATE SKIP LOCKED）、
// 乐观并发控制和完整的任务生命周期管理。
//
// 依赖 github.com/jackc/pgx/v5 连接池驱动。
package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds PostgreSQL connection parameters.
// Config 持有 PostgreSQL 连接参数。
type Config struct {
	DSN     string // Connection string (e.g. "postgres://user:pass@host:5432/db?sslmode=require")
	MaxConn int32  // Maximum pool connections (default adaptive: NumCPU*4, clamped [10, 100])
	MinConn int32  // Minimum pool connections (default adaptive: NumCPU, clamped [2, 20])
}

// Store implements store.TaskStore and store.LeasedTaskStore backed by PostgreSQL.
// Store 实现基于 PostgreSQL 的 store.TaskStore 和 store.LeasedTaskStore。
type Store struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// effectiveNumCPU reads cgroup limits if available, falling back to runtime.NumCPU.
func effectiveNumCPU() int32 {
	num := int32(runtime.NumCPU())
	// Try cgroup v2
	if data, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 && fields[0] != "max" {
			if quota, err := strconv.ParseFloat(fields[0], 64); err == nil {
				if period, err := strconv.ParseFloat(fields[1], 64); err == nil && period > 0 {
					cores := int32(math.Ceil(quota / period))
					if cores > 0 && cores < num {
						num = cores
					}
				}
			}
		}
	} else if data, err := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us"); err == nil {
		// Try cgroup v1
		if quota, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64); err == nil && quota > 0 {
			if periodData, err := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us"); err == nil {
				if period, err := strconv.ParseFloat(strings.TrimSpace(string(periodData)), 64); err == nil && period > 0 {
					cores := int32(math.Ceil(quota / period))
					if cores > 0 && cores < num {
						num = cores
					}
				}
			}
		}
	}
	if num < 1 {
		num = 1
	}
	return num
}

// New creates a new PostgreSQL store with connection pool.
// New 创建带连接池的 PostgreSQL 存储实例。
//
// ctx 用于初始化连接池；logger 为 nil 时使用 slog.Default()。
func New(ctx context.Context, cfg Config, logger *slog.Logger) (*Store, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("postgres: DSN must not be empty")
	}
	if logger == nil {
		logger = slog.Default()
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse DSN: %w", err)
	}

	numCPU := effectiveNumCPU()
	if cfg.MaxConn > 0 {
		poolCfg.MaxConns = cfg.MaxConn
	} else {
		// Adaptive MaxConns: clamp between [10, 100]
		adaptiveMax := numCPU * 4
		if adaptiveMax < 10 {
			adaptiveMax = 10
		} else if adaptiveMax > 100 {
			adaptiveMax = 100
		}
		poolCfg.MaxConns = adaptiveMax
	}

	if cfg.MinConn > 0 {
		poolCfg.MinConns = cfg.MinConn
	} else {
		// Adaptive MinConns: clamp between [2, 20]
		adaptiveMin := numCPU
		if adaptiveMin < 2 {
			adaptiveMin = 2
		} else if adaptiveMin > 20 {
			adaptiveMin = 20
		}
		poolCfg.MinConns = adaptiveMin
	}

	// Invariant: minConns must not exceed maxConns
	if poolCfg.MinConns > poolCfg.MaxConns {
		poolCfg.MinConns = poolCfg.MaxConns
	}

	// Connection health check interval / 连接健康检查间隔
	poolCfg.HealthCheckPeriod = 30 * time.Second

	// Max connection lifetime to prevent stale connections / 最大连接生命周期
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}

	// Probe connectivity with 3s timeout / 使用 3 秒超时探测连接
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer pingCancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping probe failed (3s): %w", err)
	}

	s := &Store{pool: pool, logger: logger}

	// Initialize schema / 初始化表结构 (允许 30 秒 DDL)
	schemaCtx, schemaCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer schemaCancel()
	if err := s.initSchema(schemaCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: init schema: %w", err)
	}

	logger.Info("postgres: store initialized successfully",
		"max_conns", poolCfg.MaxConns,
		"min_conns", poolCfg.MinConns,
	)

	return s, nil
}

// Close closes the underlying connection pool.
// Close 关闭底层连接池。
func (s *Store) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

// Pool returns the underlying pgxpool.Pool for advanced usage.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

// Ping verifies the database connection is alive.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}
