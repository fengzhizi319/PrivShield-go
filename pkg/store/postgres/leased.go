// Package postgres implements Phase B LeasedTaskStore: atomic task ownership via PostgreSQL.
// Package postgres 实现 Phase B LeasedTaskStore：基于 PostgreSQL 的原子任务领取与独占租约管理。
//
// ==============================================================================
// 【核心算法与并发控制】
// 1. 【无阻塞原子竞争领取 (FOR UPDATE SKIP LOCKED)】：
//    ClaimNext 通过单条短事务将 CTE 候选筛选与行级锁定结合，多副本 Hub 节点并发竞争时，
//    自动跳过已被锁定的行，无任何锁等待或死锁风险；
// 2. 【16 字节随机令牌防脑裂覆盖 (Lease Token)】：
//    每个分配的租约携带唯一随机 Hex 令牌（generateToken）。当所有者发生长 GC、假死或网络分区导致租约超期
//    被其他副本接管后，过期所有者后续执行的 CompleteLease / FailLease 均因 token 不匹配返回 false，
//    严防陈旧数据覆盖；
// 3. 【可重试失败与指数退避 (FailLease)】：
//    若 failure.Retryable 为 true 且 retry_count < max_retries，任务重置为 pending 并设置
//    retry_after = NOW() + min(5 * 2^retry_count, 60) 秒；若达到最大重试次数则强制置为 terminal failed；
// 4. 【过期租约自动回收 (RequeueExpiredLeases)】：
//    定期扫描 running 但 lease_expires_at <= NOW() 的任务，重置为 pending 等待重新调度。
// ==============================================================================

package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/fengzhizi319/PrivShield/pkg/store"
)

// ClaimNext atomically claims the next pending task for the given owner.
//
// ClaimNext 原子抢占下一个待执行任务。
//
// 执行逻辑：
//  1. 生成 16 字节随机十六进制令牌 token；
//  2. 执行单条 SQL 短事务：
//     a. CTE candidate：筛选 status='pending' 且到达重试时间 retry_after 的任务，按 priority 降序、created_at 升序排列，
//     使用 FOR UPDATE SKIP LOCKED 锁定 1 行（自动跳过其他副本正在处理的行）；
//     b. UPDATE tasks：将状态置为 running、stage 置为 running、写入 lease_owner、lease_token、lease_expires_at 并自增 version；
//     c. RETURNING：返回被领取的完整任务行；
//  3. 若无可用 pending 任务，返回 (nil, nil)；
//  4. 返回包装后的 store.TaskLease 结构体。
func (s *Store) ClaimNext(owner string, leaseTTL time.Duration) (*store.TaskLease, error) {
	ctx := context.Background()
	token := generateToken()

	row := s.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			FROM tasks
			WHERE status = 'pending'
			  AND (retry_after IS NULL OR retry_after <= NOW())
			  AND retry_count < max_retries
			ORDER BY priority DESC, created_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE tasks
		SET status = 'running',
		    stage = 'running',
		    started_at = COALESCE(started_at, NOW()),
		    lease_owner = $1,
		    lease_token = $2,
		    lease_expires_at = NOW() + ($3::TEXT || ' seconds')::INTERVAL,
		    version = version + 1
		WHERE id IN (SELECT id FROM candidate)
		RETURNING id, status, stage, source, api_code, datasource_id, operation, priority, created_at, started_at,
			completed_at, duration_ms, error, error_class, retry_count, retry_after, trace_id,
			lease_owner, lease_token, lease_expires_at, version, max_retries
	`, owner, token, fmt.Sprintf("%.0f", leaseTTL.Seconds()))

	task, err := scanTask(row)
	if err != nil {
		// 无行返回表示当前没有可调度的 pending 任务
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: claim next: %w", err)
	}

	return &store.TaskLease{
		Task:      task,
		Owner:     task.LeaseOwner,
		Token:     task.LeaseToken,
		ExpiresAt: *task.LeaseExpiresAt,
	}, nil
}

// RenewLease extends the lease for a task, conditional on ownership and non-expiry.
//
// RenewLease 延长任务租约。
//
// 执行逻辑：
// 仅当 id、status='running'、lease_owner、lease_token 均匹配且 lease_expires_at > NOW()（未过期）时
// 延长过期时间并自增版本号；返回 false 表示已失去所有权。
func (s *Store) RenewLease(id, owner, token string, leaseTTL time.Duration) (bool, error) {
	ctx := context.Background()
	tag, err := s.pool.Exec(ctx, `
		UPDATE tasks
		SET lease_expires_at = NOW() + ($4::TEXT || ' seconds')::INTERVAL,
		    version = version + 1
		WHERE id = $1
		  AND status = 'running'
		  AND lease_owner = $2
		  AND lease_token = $3
		  AND lease_expires_at > NOW()
	`, id, owner, token, fmt.Sprintf("%.0f", leaseTTL.Seconds()))
	if err != nil {
		return false, fmt.Errorf("postgres: renew lease: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// CompleteLease marks a task as completed, conditional on ownership.
//
// CompleteLease 将任务标记为完成终态。
//
// 执行逻辑：
// 仅当当前副本持有合法且未超期的租约时，将任务状态置为 completed、记录 completed_at 与实际执行耗时 duration_ms，
// 并清空租约过期时间；返回 false 表示已失去所有权。
func (s *Store) CompleteLease(id, owner, token string, result store.TaskResult) (bool, error) {
	ctx := context.Background()
	tag, err := s.pool.Exec(ctx, `
		UPDATE tasks
		SET status = 'completed',
		    stage = CASE WHEN $4 != '' THEN $4 ELSE stage END,
		    completed_at = NOW(),
		    duration_ms = EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000,
		    lease_expires_at = NULL,
		    version = version + 1
		WHERE id = $1
		  AND status = 'running'
		  AND lease_owner = $2
		  AND lease_token = $3
		  AND lease_expires_at > NOW()
	`, id, owner, token, result.Stage)
	if err != nil {
		return false, fmt.Errorf("postgres: complete lease: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// FailLease marks a task as failed, conditional on ownership.
//
// FailLease 标记任务执行失败。
//
// 执行逻辑：
//  1. 若 failure.Retryable 为 true：
//     a. 若 retry_count < max_retries：将状态重置为 pending、清空租约所有者、累加 retry_count，
//     并按 min(5 * 2^retry_count, 60) 秒计算指数退避时间 retry_after；
//     b. 若重试次数已达上限（retry_count >= max_retries）：强制转为终态 failed，记录耗时与错误信息；
//  2. 若不可重试（failure.Retryable 为 false）：直接置为终态 failed。
func (s *Store) FailLease(id, owner, token string, failure store.TaskFailure) (bool, error) {
	ctx := context.Background()

	if failure.Retryable {
		// 可重试失败：回退为 pending 并设置退避
		tag, err := s.pool.Exec(ctx, `
			UPDATE tasks
			SET status = 'pending',
			    stage = 'queued',
			    error = $4,
			    error_class = $5,
			    retry_count = retry_count + 1,
			    retry_after = NOW() + (LEAST(5 * POWER(2, retry_count), 60)::TEXT || ' seconds')::INTERVAL,
			    lease_owner = '',
			    lease_token = '',
			    lease_expires_at = NULL,
			    version = version + 1
			WHERE id = $1
			  AND status = 'running'
			  AND lease_owner = $2
			  AND lease_token = $3
			  AND lease_expires_at > NOW()
			  AND retry_count < max_retries
		`, id, owner, token, failure.Error, failure.ErrorClass)
		if err != nil {
			return false, fmt.Errorf("postgres: fail lease (retryable): %w", err)
		}
		if tag.RowsAffected() > 0 {
			return true, nil
		}

		// 重试次数已耗尽，流转为终态 failed
		tag, err = s.pool.Exec(ctx, `
			UPDATE tasks
			SET status = 'failed',
			    error = $4,
			    error_class = $5,
			    completed_at = NOW(),
			    duration_ms = EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000,
			    lease_expires_at = NULL,
			    version = version + 1
			WHERE id = $1
			  AND status = 'running'
			  AND lease_owner = $2
			  AND lease_token = $3
			  AND lease_expires_at > NOW()
			  AND retry_count >= max_retries
		`, id, owner, token, failure.Error, failure.ErrorClass)
		if err != nil {
			return false, fmt.Errorf("postgres: fail lease after retry exhaustion: %w", err)
		}
		return tag.RowsAffected() > 0, nil
	}

	// 不可重试失败：直接标记为终态 failed
	tag, err := s.pool.Exec(ctx, `
		UPDATE tasks
		SET status = 'failed',
		    error = $4,
		    error_class = $5,
		    completed_at = NOW(),
		    duration_ms = EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000,
		    lease_expires_at = NULL,
		    version = version + 1
		WHERE id = $1
		  AND status = 'running'
		  AND lease_owner = $2
		  AND lease_token = $3
		  AND lease_expires_at > NOW()
	`, id, owner, token, failure.Error, failure.ErrorClass)
	if err != nil {
		return false, fmt.Errorf("postgres: fail lease (terminal): %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// RequeueExpiredLeases reclaims tasks whose lease has expired.
//
// RequeueExpiredLeases 批量回收过期租约：将 running 状态且 lease_expires_at <= NOW() 的任务重置为 pending。
func (s *Store) RequeueExpiredLeases(limit int) (int, error) {
	ctx := context.Background()
	if limit <= 0 {
		limit = 100
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE tasks
		SET status = 'pending',
		    stage = 'queued',
		    lease_owner = '',
		    lease_token = '',
		    lease_expires_at = NULL,
		    version = version + 1
		FROM (
			SELECT id FROM tasks
			WHERE status = 'running'
			  AND lease_expires_at IS NOT NULL
			  AND lease_expires_at <= NOW()
			ORDER BY lease_expires_at ASC
			LIMIT $1
		) AS expired
		WHERE tasks.id = expired.id
	`, limit)
	if err != nil {
		return 0, fmt.Errorf("postgres: requeue expired leases: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// generateToken creates a random 16-byte hex token for lease identification.
// generateToken 生成 16 字节安全随机十六进制字符串作为租约令牌。
func generateToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
