# pkg — 数盾 Go 共享核心基础库

`pkg` 是 **数联天下 · 数盾 (PrivShield)** 全平台各 Go 微服务（`services/service-hub`、`services/datasource-mgr`、`services/audit-log` 以及 `console/bff-go`）共享的核心基础库。

---

## 包含的包

| 子包 | 描述 |
|---|---|
| [`pkg/crypto`](./crypto/sm3.go) | 国密商用密码与动态信封加密：GM/T 0004-2012 SM3 哈希、HMAC-SM3、GM/T 0002-2012 SM4-GCM 信封加密（`enc:v1:...`）与随机 IV 防篡改认证 |
| [`pkg/store`](./store/store.go) | 任务、数据源与脱敏审计日志的数据模型与存储接口，提供 PostgreSQL Phase B、SQLite WAL 持久化与内存存储引擎 |
| [`pkg/store/flusher`](./store/flusher/flusher.go) | 高并发微批缓冲刷盘器（`BufferedAuditStore`）：单 Worker 串行哈希链绑定、内存读己之写索引与优雅停机零丢失排空 |
| [`pkg/store/postgres`](./store/postgres/postgres.go) | Phase B 基于 PostgreSQL 的原子任务租约存储（`FOR UPDATE SKIP LOCKED` 多副本竞争领取、令牌防脑裂、租约自愈与分区表） |
| [`pkg/store/sqlite`](./store/sqlite/sqlite.go) | 基于 `modernc.org/sqlite` 纯 Go 驱动的单机持久化引擎（WAL 模式、busy_timeout=5000、自动 DDL 迁移） |
| [`pkg/middleware`](./middleware/middleware.go) | 统一 Gin 9 层中间件：API Key 鉴权、CORS 跨域、全链路追踪（`TraceMiddleware`）、结构化日志、Panic Recovery、安全响应头、统一错误信封与 DDoS 防护（IP 令牌桶限流、大包防护、并发硬顶） |
| [`pkg/metrics`](./metrics/metrics.go) | 基于 Prometheus 的模块级指标收集器（Counter / Histogram），防路由基数爆炸打标与 `/metrics` HTTP 端点 |
| [`pkg/agent`](./agent/client.go) | 访问上游 PrivShield Agent REST API 的共享 HTTP 客户端，具备熔断器、指数退避重试、超时与 64MB 内存防护 |
| [`pkg/config`](./config/env.go) | 统一的环境变量解析工具（String/Int/Bool/Duration/Slice）与 `slog` 结构化日志器初始化 |
| [`pkg/validation`](./validation/validation.go) | 参数白名单校验、端口范围检查、字符串长度检查、抗碰撞唯一 ID 生成与安全分页解析 |
| [`pkg/naming`](./naming/naming.go) | 字段与接口命名规范器与动态观测器（snake_case 转换与参数安全清洗） |
| [`pkg/tlsutil`](./tlsutil/tlsutil.go) | 共享 TLS 配置工具：TLS 1.3 强制最低版本、mTLS 双向认证、公钥固定（SPKI Pinning）、mTLS CN 动态白名单热重载 |

---

## 快速使用

### 1. 初始化持久化与微批刷盘存储

```go
// 自动根据配置初始化底层存储并装配微批刷盘器
func initAuditStore(cfg *config.Config, logger *slog.Logger) (store.AuditStore, error) {
    var underlying store.AuditStore
    if cfg.PGDSN != "" {
        ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
        defer cancel()
        if pgStore, err := postgres.NewAuditStore(ctx, postgres.Config{DSN: cfg.PGDSN}, logger); err == nil {
            underlying = pgStore
        }
    }
    if underlying == nil && cfg.DBPath != "" {
        if db, err := sqlite.Open(cfg.DBPath, logger); err == nil {
            underlying, _ = sqlite.NewAuditStore(db)
        }
    }
    if underlying == nil {
        underlying = memory.NewAuditStore()
    }
    return flusher.NewBufferedAuditStore(underlying, flusher.DefaultConfig(), logger), nil
}
```

### 2. 注入通用中间件与 DDoS 防护链

```go
router := gin.New()
router.Use(middleware.TraceMiddleware("service-hub")) // 全链路追踪（X-Trace-ID 注入）
router.Use(middleware.StructuredLogger(logger))
router.Use(middleware.Recovery(logger))
router.Use(middleware.SecurityHeaders())
router.Use(middleware.MaxBodySize(32 << 20)) // 32MB 请求体上限，防 Payload DDoS (413)
router.Use(middleware.MaxConcurrent(1000))   // 1000 并发硬顶，超载快速失败 (503)
router.Use(middleware.RateLimit(200, 400))  // 200 RPS 令牌桶限流，防 HTTP Flood (429)
router.Use(middleware.CORS(cfg.CORSOrigins))
router.Use(middleware.Auth(cfg.APIKey))
```

### 3. 收集与暴露指标

```go
mc := metrics.NewCollector("service-hub")
router.Use(mc.HTTPMiddleware())
router.GET("/metrics", mc.Handler())
```

---

## 文档中心

详细技术文档已完整收录在 [`pkg/docs/`](./docs/)：

| 文档 | 描述 |
|---|---|
| 📖 [**系统详细设计说明书**](./docs/design.md) | 架构分层、国密算法、微批刷盘器机理、PostgreSQL Phase B 租约争抢设计 |
| 🔌 [**编程接口与 API 规约**](./docs/api.md) | 所有公共子包 Exported APIs、Structs、方法签名与代码调用范例 |
| 🛠️ [**生产运维与加固手册**](./docs/ops.md) | 环境变量表、SQLite/PG 调优、mTLS 证书轮转、Prometheus 告警规则与 Runbook |
| 📋 [**需求规格说明书 (PRD)**](./docs/prd.md) | 业务定位、功能性与非功能性需求、密评三级合规要求、性能指标 |
| 🛡️ [**高可用与一致性保障**](./docs/reliability.md) | 单 Worker 串行哈希链、优雅停机排空、读己之写、分布式令牌屏障防脑裂 |
| 🧪 [**测试规范与质量指南**](./docs/testing.md) | 单元测试、并发压测、竞态检测（`-race`）、哈希链连续性验证套件 |
| 🚀 [**开发者进阶实战指南**](./docs/learning-guide.md) | 源码走读、场景扩展 SOP（中间件/新存储/新指标）、典型反模式防范 |

