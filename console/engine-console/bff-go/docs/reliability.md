# bff-go 可靠性能力说明

> Go 控制台后端代理（bff-go / backend-go）的崩溃恢复、自动重试、完整性校验与备份能力详解。

---

## 1. 能力总览

| 能力维度 | 支持状态 | 实现方式 |
|---|---|---|
| 崩溃恢复 | ✅ | 上游 gRPC 自动重连与指数退避重试（最多 6 次，1s→8s，覆盖 Agent 重启故障窗口） |
| gRPC 自动重试 | ✅ | gRPC 内置重试策略，环境变量可配置，默认最多 6 次，指数退避 1s→8s |
| 连接等待就绪 | ✅ | `waitForReady=true`，Agent 重启中 RPC 自动等待恢复 |
| 连接保活心跳 | ✅ | HTTP/2 PING 帧，30s 间隔，10s 超时检测 |
| HTTPS / TLS 1.3 | ✅ | 原生支持 TLS 1.3 加密，防范中间人窃听与降级攻击 |
| mTLS 双向认证 | ✅ | 入站 HTTPS/gRPC 与出站 Agent 均支持客户端证书与 SPKI 公钥固定校验 |
| 双协议服务暴露 | ✅ | 同时支持 REST/HTTPS 与原生 gRPC Server 代理网关服务 |
| 优雅停机 | ✅ | SIGINT/SIGTERM → 清理 goroutine → 关闭 gRPC Server → HTTP/HTTPS Shutdown(5s) |
| Panic 恢复 | ✅ | Gin Recovery 中间件 + securityMiddleware ticker 清理 |
| Goroutine 泄漏防护 | ✅ | Shutdown 时显式清理 securityMiddleware 定时器 |
| 备份 | ⚪ 不适用 | 无持久化状态，纯无状态网关 |

---

## 2. gRPC 自动重试（Automatic Retry）

### 2.1 重试策略配置

bff-go 通过 gRPC `DefaultServiceConfig` 配置内置重试策略，覆盖 Agent 崩溃/重启的完整故障窗口。

重试参数通过环境变量可配置（#12）：

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `PRIVACY_AGENT_RETRY_MAX_ATTEMPTS` | `6` | 最大重试次数（含首次调用） |
| `PRIVACY_AGENT_RETRY_INITIAL_BACKOFF` | `1` | 首次重试退避秒数 |
| `PRIVACY_AGENT_RETRY_MAX_BACKOFF` | `8` | 最大退避秒数 |

生成的 gRPC 重试策略如下：

```json
{
  "methodConfig": [{
    "name": [{"service": "privacy.local.PrivacyService"}],
    "waitForReady": true,
    "retryPolicy": {
      "MaxAttempts": 6,
      "InitialBackoff": "1s",
      "MaxBackoff": "8s",
      "BackoffMultiplier": 2.0,
      "RetryableStatusCodes": ["UNAVAILABLE"]
    }
  }]
}
```

### 2.2 重试参数详解（默认值）

| 参数 | 值 | 说明 |
|---|---|---|
| `MaxAttempts` | 6 | 1 次原始调用 + 5 次重试 |
| `InitialBackoff` | 1 秒 | 首次重试等待时间 |
| `MaxBackoff` | 8 秒 | 最大退避时间上限 |
| `BackoffMultiplier` | 2.0 | 指数退避乘数 |
| `RetryableStatusCodes` | `UNAVAILABLE` | 仅重试服务不可达错误 |

**退避序列**：1s → 2s → 4s → 8s → 8s（总重试窗口约 31 秒，覆盖 Agent 重启耗时）。

### 2.3 waitForReady 机制

- `waitForReady=true`：当连接不可用（如 Agent 正在重启，dial connection refused）时，RPC **不立即失败**，而是等待连接恢复后自动发送；
- 前端无需手动重试，gRPC 客户端库自动处理连接等待与重试；
- 与 `retryPolicy` 配合覆盖 Agent 崩溃/重启的完整故障窗口。

---

## 3. 连接保活（Keepalive）

### 3.1 心跳配置

```go
keepalive.ClientParameters{
    Time:                30 * time.Second,  // 每 30 秒发送 PING
    Timeout:             10 * time.Second,  // 10 秒超时判定断开
    PermitWithoutStream: true,              // 无活跃 RPC 也发送心跳
}
```

### 3.2 设计目的

| 场景 | 保活机制的作用 |
|---|---|
| Agent OOM 崩溃 | 30s 内检测到连接断开，触发重连 |
| Agent 被 kill -9 | TCP RST 立即感知，触发重连 |
| 网络分区 | 10s 超时后判定连接失效 |
| 空闲连接 | `PermitWithoutStream=true` 确保空闲连接也被监控 |

---

## 4. 优雅停机（Graceful Shutdown）

### 4.1 停机流程

```
SIGINT/SIGTERM → Server.Shutdown()（清理 goroutine）→ HTTP Shutdown(5s) → 进程退出
```

**详细步骤：**

1. **信号捕获**：监听 `SIGINT` 和 `SIGTERM`；
2. **Goroutine 清理**：`server.Shutdown()` 调用 `secCleanup()`，停止 `securityMiddleware` 中的 ticker goroutine，防止 goroutine 泄漏；
3. **HTTP 优雅停机**：`srv.Shutdown(ctx)` 停止接收新请求，等待现有请求完成（5 秒硬上限）；
4. **gRPC 连接释放**：Go gRPC 客户端连接在进程退出时自动释放。

### 4.2 Goroutine 泄漏防护

**问题背景**（P57 fix）：
`securityMiddleware` 内部创建了定时清理 ticker goroutine，如果服务停机时不清理，会导致 goroutine 泄漏。

**解决方案**：
- `securityMiddleware` 返回 `cleanup` 函数；
- `Server.Shutdown()` 在停机时调用 `cleanup()`，显式停止 ticker；
- 确保进程退出时所有 goroutine 正常终止。

---

## 5. Panic 恢复（Panic Recovery）

### 5.1 Gin Recovery 中间件

```go
r.Use(middleware.Recovery(s.logger, "backend-go"))
```

**工作机制：**
- 捕获 HTTP handler 中的 panic；
- 记录 panic 堆栈到结构化日志；
- 返回 HTTP 500 错误响应；
- **进程不退出**，其他请求继续正常处理。

### 5.2 安全响应头

通过 `middleware.SecurityHeaders()` 添加安全响应头：
- `Strict-Transport-Security`（HSTS）
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`

---

## 6. 网络安全防护

### 6.1 HTTP 超时配置

| 参数 | 值 | 说明 |
|---|---|---|
| `ReadHeaderTimeout` | 5 秒 | 防御 Slowloris 攻击 |
| `ReadTimeout` | 30 秒 | 请求体读取超时 |
| `WriteTimeout` | 60 秒 | 响应写入超时 |
| `IdleTimeout` | 120 秒 | Keep-Alive 空闲保活 |

### 6.2 请求体限制

```go
r.Use(middleware.MaxBodySize(64 << 20)) // 64 MiB max payload
```

- 限制单请求最大 64 MiB，防止大包 DDoS；
- 支持较大的 CSV 文件上传场景。

### 6.3 gRPC 消息限制

```go
grpc.MaxCallRecvMsgSize(64 << 20)  // 64 MiB 接收上限
grpc.MaxCallSendMsgSize(64 << 20)  // 64 MiB 发送上限
```

---

## 7. 上游依赖的容错设计

bff-go 作为控制台后端代理，其可靠性依赖上游 Agent 的可用性。通过以下机制保障：

| 容错机制 | 说明 |
|---|---|
| gRPC 重试 | Agent 临时不可达时自动重试（最多 6 次，31 秒窗口，参数可通过环境变量调整） |
| waitForReady | Agent 重启中时 RPC 自动等待，前端无感知 |
| Keepalive 心跳 | Agent 崩溃后 30 秒内检测并重连 |
| 大消息配置 | 64 MiB 缓冲区，避免默认 4 MiB 导致大表/图片分类连接重置 |

---

## 8. 运维建议

### 8.1 部署建议

- 无状态服务，建议部署 **≥ 2 个副本** 实现高可用；
- 配合 K8s `readinessProbe` 使用 `/health` 端点；
- 确保 Agent 的 gRPC 端口在 bff-go 启动前可达，或利用 `waitForReady` 自动等待。

### 8.2 故障排查

| 现象 | 可能原因 | 排查方法 |
|---|---|---|
| 前端请求超时 31 秒后失败 | Agent 完全不可达 | 检查 Agent 进程状态和 gRPC 端口 |
| 偶发 UNAVAILABLE 错误 | Agent 重启中 | 正常现象，gRPC 自动重试恢复 |
| goroutine 数量持续增长 | Shutdown 未正确调用 | 确认 `server.Shutdown()` 在信号处理中被调用 |
| 大文件上传失败 | 超过 64 MiB 限制 | 调整 `MaxBodySize` 或压缩上传内容 |
