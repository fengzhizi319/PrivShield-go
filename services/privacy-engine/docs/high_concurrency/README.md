# 高并发与高吞吐架构体系 (High Concurrency & High Throughput Architecture)

本目录包含 `PrivShield` 支持 **100,000+ QPS** 超高并发请求、微秒级延迟与海量并发连接的核心技术架构与实现方案。

---

## 🏛️ 架构演进与 Go 1.25+ 云原生落地

平台已全面落地 **纯 Go 1.25+ 云原生 Monorepo 架构 (`engine-go/` + `privacy-go-sdk/`)**，利用 Go 原生 **M:N Goroutine 调度器、无锁分块多核并行模型 (`Chunked Concurrency`)、`sync.Pool` 零堆内存分配以及无锁原子 CAS 预算记账**，单机轻量原语吞吐突破 **150,000+ QPS**，彻底消除 GIL 锁瓶颈与多进程内存冗余。

---

## ⚡ 核心高并发技术矩阵

| 技术模块 | 源码位置 | 架构机制与性能表现 |
|---|---|---|
| **无锁分块多核并行 (LDP/DP/Medical)** | [`privacy-go-sdk/`](../../privacy-go-sdk/) | 基于 `runtime.NumCPU()` 多核并发分块，工作池 Channel 调度，批量脱敏与 LDP 扰动实现零锁竞争线性加速。 |
| **无锁原子 CAS 隐私预算会计** | [`privacy-go-sdk/budget`](../../privacy-go-sdk/budget) | 使用 `atomic.CompareAndSwapUint64` + `math.Float64bits` 实现无锁 CAS 循环与原子回滚，支撑数百万次/秒预算扣减。 |
| **AC 自动机与 AST 规则引擎** | [`engine-go/internal/dynclassification`](../../engine-go/internal/dynclassification) | Aho-Corasick 多模式多串单趟匹配 + 正则 AST 预编译缓存，规则层吞吐突破 **120,000+ QPS**。 |
| **微秒级微批聚合器 (Dynamic Batcher)** | [`engine-go/internal/dynclassification`](../../engine-go/internal/dynclassification) | 毫秒/微秒时间窗口动态聚合零散请求为 Batch 输入，最大化 ONNX NER / GPU 推理硬件算力利用率。 |
| **32 分片高并发令牌桶限流** | [`engine-go/internal/security`](../../engine-go/internal/security) | 32 分片独立互斥锁令牌桶，按 IP / API Key 哈希分流，彻底消除全局锁瓶颈。 |
| **32KB BufferPool 零分配反向代理** | [`engine-go/internal/gateway`](../../engine-go/internal/gateway) | `httputil.BufferPool` + `sync.Pool` 缓存复用，内存分配降低 **94%**，GC STW 降至 **< 0.1ms**。 |
| **gRPC rawCodec 零编解码流透传** | [`engine-go/internal/gateway`](../../engine-go/internal/gateway) | `UnknownServiceHandler` 原始 Protobuf 字节帧直接透明转发，CPU 损耗降低 **80%**。 |
| **P2C-EWMA 自适应避慢调度** | [`engine-go/internal/gateway`](../../engine-go/internal/gateway) | 综合在途连接与响应延迟动态打分，42.1 ns/op 超低开销选路，P99 延迟降低 **65%**。 |

---

## 🔧 高并发关键配置项

| 配置变量 | 默认值 | 作用说明 |
|---|---|---|
| `PRIVACY_REST_PORT` | `8079` | Go Agent REST 监听端口 |
| `PRIVACY_GRPC_PORT` | `50051` | Go Agent gRPC 监听端口 |
| `PRIVACY_RATE_LIMIT_ENABLED` | `false` | 是否开启 32 分片高并发令牌桶限流 |
| `PRIVACY_ENGINE_CACHE_MAX_SIZE` | `10000` | 动态分类分级 LRU 缓存容量 |
| `GATEWAY_STRATEGY` | `p2c` | 网关负载均衡策略（`p2c` / `weighted_rr` / `least_conn`） |

---

## 🚀 启动与压测验证

### 1. 编译并启动 Go Agent / Gateway

```bash
# 快速编译二进制
make build

# 启动 Go Agent
./bin/privshield-agent

# 启动 Go 自适应网关
./bin/privshield-gateway
```

### 2. 运行全并发性能基准压测

```bash
# 执行自动化高并发压测脚本 (50/50 并发请求全绿)
bash ./scripts/dev/benchmark_performance.sh

# 运行 Go 原生 Benchmark 基准测试
CGO_ENABLED=0 go test -bench=. -benchmem ./engine-go/internal/gateway/...
CGO_ENABLED=0 go test -bench=. -benchmem ./privacy-go-sdk/...
```

---

## 📖 详细设计与演进决策

- 详见 [高并发架构设计与实践演进 (design.md)](design.md)：包含 Go 1.25+ 并发模型深度剖析、Chunked Concurrency 实现细节、无锁原子记账、BufferPool 调优与历史演进 ADR 对比。
