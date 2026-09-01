// Package postgres provides a PostgreSQL-backed implementation of the store interfaces.
// Package postgres 提供基于 PostgreSQL 的 store 接口实现。
//
// ==============================================================================
// 【架构设计定位与高并发保障】
// 1. 【Phase B 多副本部署核心支撑】：
//    PostgreSQL 后端为 PrivShield Phase B 多副本 Hub 部署架构提供核心支撑；
// 2. 【无锁原子租约抢占 (FOR UPDATE SKIP LOCKED)】：
//    支持多 Hub 节点并发竞争式领取待调度任务（ClaimNext），无需任何外部分布式锁（如 Redis/ZooKeeper）；
// 3. 【容器感知的自适应连接池 (pgxpool)】：
//    支持 cgroup v1 与 cgroup v2 CPU 配额感知（effectiveNumCPU），自适应计算连接池上下限，
//    并在初始化阶段执行 3 秒轻量级 Ping 探活探测；
// 4. 【连接生命周期管理】：
//    配置 30 秒健康检查周期（HealthCheckPeriod）、30 分钟最大连接生命周期（MaxConnLifetime）
//    与 5 分钟空闲回收（MaxConnIdleTime），有效防范云原生环境网络断连与陈旧连接悬挂。
// ==============================================================================

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
// Config 持有 PostgreSQL 数据库连接与连接池配置参数。
type Config struct {
	DSN     string // PostgreSQL 连接字符串（如 "postgres://user:pass@host:5432/db?sslmode=require"）
	MaxConn int32  // 最大连接池容量（默认自适应计算：effectiveNumCPU * 4，限制在 [10, 100] 范围内）
	MinConn int32  // 最小常驻连接数（默认自适应计算：effectiveNumCPU，限制在 [2, 20] 范围内）
}

// Store implements store.TaskStore and store.LeasedTaskStore backed by PostgreSQL.
// Store 实现基于 PostgreSQL 的 store.TaskStore 和 store.LeasedTaskStore 接口。
type Store struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// effectiveNumCPU reads cgroup limits if available, falling back to runtime.NumCPU.
//
// effectiveNumCPU 探测当前进程在容器中的真实 CPU 核心配额：
// 1. 优先尝试读取 Linux cgroup v2 配额文件（/sys/fs/cgroup/cpu.max）；
// 2. 其次尝试读取 Linux cgroup v1 配额文件（/sys/fs/cgroup/cpu/cpu.cfs_quota_us）；
// 3. 若未限制或读取失败，降级回退使用 runtime.NumCPU()。
func effectiveNumCPU() int32 {
	num := int32(runtime.NumCPU())
	// 尝试 cgroup v2
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
		// 尝试 cgroup v1
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
//
// New 构建带连接池的 PostgreSQL 任务与租约存储实例。
//
// 执行逻辑：
// 1. 校验 DSN 非空，解析 pgxpool.Config；
// 2. 结合容器算力自适应配置 MaxConns 与 MinConns；
// 3. 配置连接探活健康检查与空闲回收超时；
// 4. 创建 pgxpool.Pool，并执行 3 秒超时 Ping 探活探测；
// 5. 调用 initSchema 在 30 秒超时时间内初始化 tasks 表结构与索引；
// 6. 返回初始化就绪的 Store 指针。
func NewStore(ctx context.Context, cfg Config, logger *slog.Logger) (*Store, error) {
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
		// 自适应 MaxConns：限制在 [10, 100] 之间
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
		// 自适应 MinConns：限制在 [2, 20] 之间
		adaptiveMin := numCPU
		if adaptiveMin < 2 {
			adaptiveMin = 2
		} else if adaptiveMin > 20 {
			adaptiveMin = 20
		}
		poolCfg.MinConns = adaptiveMin
	}

	// 不变式约束：MinConns 不得超过 MaxConns
	if poolCfg.MinConns > poolCfg.MaxConns {
		poolCfg.MinConns = poolCfg.MaxConns
	}

	// 连接健康检查周期
	poolCfg.HealthCheckPeriod = 30 * time.Second

	// 最大生命周期与空闲回收
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}

	// 使用 3 秒超时探测数据库连通性
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer pingCancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping probe failed (3s): %w", err)
	}

	s := &Store{pool: pool, logger: logger}

	// 初始化表结构 (允许 30 秒 DDL 超时)
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
// Close 关闭底层连接池，释放所有物理连接。
func (s *Store) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

// Pool returns the underlying pgxpool.Pool for advanced usage.
// Pool 返回底层的 pgxpool.Pool 实例，供高级测试或原生 SQL 事务使用。
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

// Ping verifies the database connection is alive.
// Ping 执行数据库连接探活检测。
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}
