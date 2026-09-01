// Package sqlite implements Phase B LeasedTaskStore stub for SQLite backend.
// Package sqlite 提供 SQLite 后端的 LeasedTaskStore 租约桩实现。
//
// ==============================================================================
// 【设计考量与 Fail-Closed 原则】
// SQLite 单文件数据库不支持多写者跨实例并发，亦缺乏 FOR UPDATE SKIP LOCKED
// 等行级排他锁语义。
//
// 因此所有租约操作均返回 ErrLeaseNotSupported：
// 1. 防止运维团队在生产环境中错误地将多副本 Hub 连接到同一个 SQLite 文件；
// 2. 在配置错误发生时第一时间通过错误码显式暴露，遵循 Fail-Closed 安全原则；
// 3. 编译期通过 `var _ store.LeasedTaskStore = (*TaskStore)(nil)` 确保类型接口完整性。
// ==============================================================================

package sqlite

import (
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
)

// ClaimNext is not supported on SQLite; returns ErrLeaseNotSupported.
// ClaimNext 在 SQLite 上不支持原子跨实例抢占，返回 ErrLeaseNotSupported。
func (s *TaskStore) ClaimNext(owner string, leaseTTL time.Duration) (*store.TaskLease, error) {
	return nil, store.ErrLeaseNotSupported
}

// RenewLease is not supported on SQLite; returns ErrLeaseNotSupported.
// RenewLease 在 SQLite 上不支持租约续期，返回 ErrLeaseNotSupported。
func (s *TaskStore) RenewLease(id, owner, token string, leaseTTL time.Duration) (bool, error) {
	return false, store.ErrLeaseNotSupported
}

// CompleteLease is not supported on SQLite; returns ErrLeaseNotSupported.
// CompleteLease 在 SQLite 上不支持租约完成标记，返回 ErrLeaseNotSupported。
func (s *TaskStore) CompleteLease(id, owner, token string, result store.TaskResult) (bool, error) {
	return false, store.ErrLeaseNotSupported
}

// FailLease is not supported on SQLite; returns ErrLeaseNotSupported.
// FailLease 在 SQLite 上不支持租约失败标记，返回 ErrLeaseNotSupported。
func (s *TaskStore) FailLease(id, owner, token string, failure store.TaskFailure) (bool, error) {
	return false, store.ErrLeaseNotSupported
}

// RequeueExpiredLeases is not supported on SQLite; returns ErrLeaseNotSupported.
// RequeueExpiredLeases 在 SQLite 上不支持过期租约回收，返回 ErrLeaseNotSupported。
func (s *TaskStore) RequeueExpiredLeases(limit int) (int, error) {
	return 0, store.ErrLeaseNotSupported
}

// 编译期接口断言：SQLite TaskStore 在类型层面满足 LeasedTaskStore 接口，运行时显式抛出不支持错误。
var _ store.LeasedTaskStore = (*TaskStore)(nil)
