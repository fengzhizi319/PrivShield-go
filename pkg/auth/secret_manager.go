package auth

import (
	"context"
	"errors"
	"io"
	"time"
)

// SecretEvent 表示外部集中式密钥存储（Vault / K8s Secret / 云 KMS）的变更事件。
type SecretEvent struct {
	SecretName string    // 密钥标识或路径，例如 "privshield-api-keys"
	Content    string    // 密钥文本内容（格式同 ParseAPIKeysEnv）
	Version    string    // 版本标识（例如 K8s resourceVersion 或 Vault secret version）
	Err        error     // 监听或读取错误（如有）
	Timestamp  time.Time // 事件发生时间
}

// SecretWatcher 抽象外部集中式密钥管理系统的变更监听接口。
// 支持监听来自 Kubernetes Secret Watcher、HashiCorp Vault、AWS Secrets Manager 等系统的热重载事件。
type SecretWatcher interface {
	// Watch 启动对指定密钥的监听，返回一个接收 SecretEvent 的只读通道。
	Watch(ctx context.Context, secretName string) (<-chan SecretEvent, error)
	io.Closer
}

// SecretProvider 抽象外部密钥存储的主动读取接口。
type SecretProvider interface {
	// GetSecret 主动拉取指定密钥的当前内容。
	GetSecret(ctx context.Context, secretName string) (string, error)
}

// ChannelSecretWatcher 是基于 Go Channel 的事件驱动 SecretWatcher 实现。
// 外部适配器（如 K8s controller、Vault agent 或 webhook 回调）可通过 Push 方法无缝向 KeyStore 注入最新密钥。
type ChannelSecretWatcher struct {
	ch     chan SecretEvent
	stopCh chan struct{}
}

// NewChannelSecretWatcher 创建一个带指定缓冲区的 ChannelSecretWatcher。
func NewChannelSecretWatcher(bufferSize int) *ChannelSecretWatcher {
	if bufferSize <= 0 {
		bufferSize = 16
	}
	return &ChannelSecretWatcher{
		ch:     make(chan SecretEvent, bufferSize),
		stopCh: make(chan struct{}),
	}
}

// Watch 返回监听通道。
func (w *ChannelSecretWatcher) Watch(ctx context.Context, secretName string) (<-chan SecretEvent, error) {
	return w.ch, nil
}

// Push 向 Watcher 写入一条密钥变更事件。
func (w *ChannelSecretWatcher) Push(event SecretEvent) error {
	select {
	case <-w.stopCh:
		return errors.New("secret watcher is closed")
	default:
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	select {
	case w.ch <- event:
		return nil
	case <-w.stopCh:
		return errors.New("secret watcher closed while pushing event")
	case <-time.After(2 * time.Second):
		return errors.New("secret watcher event channel full")
	}
}

// Close 关闭 Watcher，停止后续事件注入。
//
// 只关闭 stopCh、**不关闭事件通道本身**：Push 与 Close 并发时，向已关闭的 channel
// 发送会直接 panic 并拖垮整个进程（密钥热轮转路径上的崩溃 = 可用性事故）。
// close(stopCh) 已足以让 Push 的预检与消费者的 select 收敛退出，channel 由 GC 回收。
// 幂等：重复调用返回 nil，不重复 close。
func (w *ChannelSecretWatcher) Close() error {
	select {
	case <-w.stopCh:
		return nil
	default:
		close(w.stopCh)
		return nil
	}
}
