# 差分隐私运维手册


## 1. 调用示例

### Laplace count

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/dp/count \
  -H "Content-Type: application/json" \
  -d '{"values":[1,0,1,1,0],"params":{"epsilon":1.0}}'
```

### Gaussian sum with clipping

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/dp/sum \
  -H "Content-Type: application/json" \
  -d '{
    "values":[1,2,3,100],
    "params":{
      "epsilon":1.0,
      "delta":1e-6,
      "mechanism":"gaussian",
      "clip_lower":0.0,
      "clip_upper":10.0
    }
  }'
```

### Mean with low-count threshold

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/dp/mean \
  -H "Content-Type: application/json" \
  -d '{
    "values":[25,34,45,52,29,61],
    "params":{
      "epsilon":2.0,
      "delta":1e-6,
      "mechanism":"gaussian",
      "clip_lower":0.0,
      "clip_upper":120.0,
      "min_count":3.0
    }
  }'
```

> `min_count` 用于防止噪声计数过小时均值结果发散。当 `noisy_count < min_count` 时接口返回 0.0。

### Histogram

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/dp/histogram \
  -H "Content-Type: application/json" \
  -d '{
    "values":["A","B","A","C"],
    "categories":["A","B","C","D"],
    "params":{
      "epsilon":10.0,
      "mechanism":"laplace"
    }
  }'
```

### Noisify sum

适用于外部引擎已完成聚合，仅需 sidecar 加噪的场景。

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/dp/noisy_sum \
  -H "Content-Type: application/json" \
  -d '{
    "true_sum": 5000000.0,
    "params": {
      "epsilon": 1.0,
      "delta": 1e-6,
      "mechanism": "gaussian",
      "sensitivity": 100000.0
    }
  }'
```

### Chunked sum

适用于数据量过大、需要分块传入的场景。

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/dp/chunked_sum \
  -H "Content-Type: application/json" \
  -d '{
    "chunks": [
      [1.0, 2.0, 3.0],
      [4.0, 5.0, 6.0]
    ],
    "params": {
      "epsilon": 1.0,
      "delta": 1e-6,
      "mechanism": "gaussian",
      "clip_lower": 0.0,
      "clip_upper": 10.0
    }
  }'
```

### Local DP perturbation (binary)

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/ldp/perturb/binary \
  -H "Content-Type: application/json" \
  -d '{"values":[1,0,1,1],"epsilon":10.0}'
```

### Local DP perturbation (categorical)

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/ldp/perturb/categorical \
  -H "Content-Type: application/json" \
  -d '{"values":["A","B","A"],"categories":["A","B","C"],"epsilon":10.0}'
```

### Local DP estimation

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/ldp/estimate/binary \
  -H "Content-Type: application/json" \
  -d '{"reported_values":[1,1,0,1],"epsilon":5.0}'

curl -X POST http://127.0.0.1:8079/v1/privacy/ldp/estimate/categorical \
  -H "Content-Type: application/json" \
  -d '{"reported_values":["A","B","C","A"],"categories":["A","B","C"],"epsilon":5.0}'
```

## 2. 环境变量

| 变量 | 说明 | 示例 |
|---|---|---|
| `PRIVACY_BUDGET_DB` | SQLite 持久化预算数据库路径 | `/data/budget.db` |
| `PRIVACY_BUDGET_WINDOW_SECONDS` | 隐私预算自动重置时间窗口（秒） | `86400`（每天重置） |

## 3. 参数建议

| 参数 | 建议 |
|---|---|
| `epsilon` | 1.0 为常用默认值；敏感数据建议 0.1~0.5。 |
| `delta` | 必须 `< 1/n^2`；典型值 `1e-6`。 |
| `clip_lower/upper` | 根据业务先验设置；可通过离线分位数估计。 |
| `mechanism` | 小敏感度用 Laplace；需要更小噪声且可接受 delta 时用 Gaussian。Gaussian 默认使用解析高斯机制（Analytic Gaussian）。 |
| `min_count` | mean 查询建议根据最小可接受样本量设置，默认 5.0。 |
| `sensitivity` | noisify sum/mean 必填，通常为 `clip_upper - clip_lower`；也可直接提供 `clip_lower`/`clip_upper` 由 sidecar 计算。 |
| `chunks` | chunked 接口中每个 chunk 为同类型数据列表；sum/mean 必须提供全局 clip 边界。 |
| `column` / `party` | 表格型或 SecretFlow 联邦数据输入时使用。 |
| `window_seconds` | 长期运行 Sidecar 建议设置，例如 86400（每天重置）。 |

## 4. 故障排查决策树

### 4.1 请求错误（HTTP 4xx）

```text
客户端请求失败
├─ 400 Bad Request
│  ├─ "clip_lower and clip_upper are required for gaussian"
│  │  └─ 原因：Gaussian 机制必须指定 clip 边界以计算敏感度
│  │     └─ 修复：添加 clip_lower/clip_upper 参数，或改用 mechanism="laplace"
│  │
│  ├─ "clip_lower must be < clip_upper"
│  │  └─ 原因：裁剪区间无效
│  │     └─ 修复：确保 clip_lower < clip_upper
│  │
│  ├─ "delta must be positive for gaussian"
│  │  └─ 原因：Gaussian 机制要求 delta > 0
│  │     └─ 修复：添加 delta 参数（建议 1e-6 ~ 1e-9）
│  │
│  ├─ "epsilon must be positive"
│  │  └─ 原因：epsilon ≤ 0 无意义
│  │     └─ 修复：epsilon > 0（建议 0.1 ~ 10.0）
│  │
│  ├─ "chunked_sum requires explicit clip_lower and clip_upper"
│  │  └─ 原因：chunked 接口无法自动推断 clip
│  │     └─ 修复：显式提供 clip_lower/clip_upper
│  │
│  ├─ "noisy_sum requires 'sensitivity' or both 'clip_lower' and 'clip_upper'"
│  │  └─ 原因：noisify 接口需要敏感度
│  │     └─ 修复：提供 sensitivity 或 clip_lower/clip_upper
│  │
│  └─ "values must not be empty"
│     └─ 原因：空数据无法计算
│        └─ 修复：检查数据源，确保 values 非空
│
├─ 401 Unauthorized
│  └─ 原因：API Key 缺失或无效
│     └─ 修复：添加 Authorization: Bearer <api_key> 请求头
│
├─ 403 Forbidden
│  └─ 原因：TLS 证书无效或过期
│     └─ 修复：检查证书链和有效期
│
└─ 429 Too Many Requests
   └─ 原因：触发 Rate Limit
      └─ 修复：降低请求频率，或调整 PRIVACY_RATE_LIMIT_* 配置
```

### 4.2 预算耗尽

```text
"Privacy budget exhausted"
├─ 当前窗口内累计 ε 或 δ 超限
│  ├─ 检查剩余预算：GET /v1/privacy/dp/remaining
│  ├─ 等待时间窗口重置（若配置了 window_seconds）
│  └─ 增大命名空间的 epsilon_total / delta_total
│
├─ 预算消耗过快
│  ├─ 原因：epsilon 设置过小或查询过于频繁
│  ├─ 修复：增大 epsilon_per_query 或减少查询频率
│  └─ 考虑使用 RDPAccountant 获取紧致预算估计
│
└─ 多实例部署预算不一致
   ├─ 原因：内存模式预算不跨实例同步
   └─ 修复：设置 PRIVACY_BUDGET_DB 使用 SQLite 共享存储
```

### 4.3 结果异常

```text
DP 查询结果异常
├─ mean 返回 0.0
│  ├─ 原因：noisy_count < min_count，触发低频保护
│  ├─ 检查：查看响应中的 noise_scale 和 confidence_interval
│  └─ 修复：增大数据量、降低 min_count、或增大 epsilon
│
├─ 结果方差过大
│  ├─ 原因：噪声尺度与敏感度成正比
│  ├─ 检查：确认 clip_lower/clip_upper 是否合理
│  └─ 修复：缩小 clip 区间，或增大 epsilon
│
├─ 结果出现负值（count 场景）
│  ├─ 原因：Laplace/Gaussian 噪声可能为负
│  └─ 修复：添加 round_int=true, clip_non_negative=true 参数
│
└─ 不同实例返回不同结果
   ├─ 原因：每次查询独立采样噪声（符合 DP 定义）
   └─ 说明：这是正常行为，非 bug。相同查询多次运行的结果应在置信区间内
```

### 4.4 连接与部署问题

```text
连接/部署失败
├─ REST 连接拒绝
│  ├─ 检查：服务是否启动（python -m engine.server）
│  ├─ 检查：PRIVACY_REST_HOST / PRIVACY_REST_PORT 配置
│  └─ 检查：防火墙规则是否放行端口
│
├─ gRPC 连接超时
│  ├─ 检查：proto 文件版本是否匹配
│  ├─ 检查：PRIVACY_GRPC_HOST / PRIVACY_GRPC_PORT 配置
│  └─ 检查：TLS 证书是否正确（若启用）
│
├─ SQLite 数据库锁定
│  ├─ 原因：多线程/多进程并发写入
│  ├─ 检查：thread-local 连接是否正常复用
│  └─ 修复：增加 sqlite3.connect(timeout=10.0)，或减少并发写入
│
└─ K8s Pod CrashLoopBackOff
   ├─ 检查：kubectl logs <pod> 查看错误日志
   ├─ 检查：ConfigMap/Secret 是否正确挂载
   └─ 检查：资源限制（CPU/Memory）是否充足
```

## 5. 安全注意事项

- 当前 Gaussian 机制使用解析高斯机制（Analytic Gaussian），噪声界比经典公式更紧。
- 连续 Laplace/Gaussian 采样基于 Python 浮点数与伪随机数生成器，存在 Mironov 浮点精度攻击的理论风险。高安全场景应评估是否需要迁移到离散机制。
- 预算时间窗口重置可避免长期运行后预算耗尽，但窗口到期前的超支仍会被拒绝。

## 6. 流量监控

REST 中间件与 gRPC 拦截器会自动记录 `privacy_traffic_bytes_total` 指标：

```text
privacy_traffic_bytes_total{method="POST",path="/v1/privacy/dp/sum",direction="request"}
privacy_traffic_bytes_total{method="POST",path="/v1/privacy/dp/sum",direction="response"}
privacy_traffic_bytes_total{method="gRPC",path="/privacy.local.PrivacyService/DPSum",direction="request"}
```

可用于：
- 审计各接口请求/响应体量。
- 发现异常大请求或潜在滥用。
- 容量规划与带宽估算。

> gRPC stream 调用的字节数不可预知，当前实现中 request/response 字节数计为 0。

## 7. 高级特性运维指南

### 7.1 adaptive_clip 运维

- `adaptive_clip` 会消耗全部传入的 epsilon 预算，按 `num_iterations` 次 DP count 拆分。
- 建议 `num_iterations=15`，`target_quantile=0.95`，`initial_clip` 设为预期数据范围的 10 倍。
- 返回的 clip bounds 用于后续聚合调用，后续调用需额外消耗独立预算。
- 若数据范围已知，建议直接指定 clip 区间，跳过 adaptive_clip 以节省预算。

### 7.2 dp_aggregate 运维

- 预算按列数均分：`epsilon_per_col = epsilon / num_specs`。
- 若某些列重要性更高，可拆分为多次 `dp_aggregate` 调用，手动控制预算分配。
- 当前仅支持 pandas DataFrame 输入。

### 7.3 vector_sum / vector_mean 运维

- 推荐 `mechanism="gaussian"`，高维场景下噪声界更紧。
- `max_norm` 应基于梯度分布设定，过小会截断过多信息，过大会增加噪声。
- `vector_mean` 的 `min_count` 用于防止低频数据发散，默认 5.0。

### 7.4 dp_groupby 运维

- 预算按 `(num_groups × 2)` 拆分，分组数越多，每组分配的预算越少。
- 若分组数很大（>100），建议增加总 epsilon 或减少查询精度要求。
- Tau 阈值会自动过滤稀有分组，避免泄漏低频分组信息。

### 7.5 Accumulator 运维

- Worker 端 `create_accumulator` 不消耗预算，仅本地累加。
- Master 端 `finalize_dp` 消耗 epsilon 预算并注入噪声。
- 序列化格式为 JSON bytes，可跨网络传输。
- 合并操作 (`+`) 符合交换律/结合律，支持任意顺序合并。

### 7.6 RDPAccountant 运维

- `RDPAccountant` 是独立工具，不与 `BudgetAccountant` 自动集成。
- 可同时使用两者：`BudgetAccountant` 追踪保守上界，`RDPAccountant` 提供紧致参考估计。
- 默认搜索 11 个 Rényi 阶数，自动选择最优 α 使 ε 最小。

## 8. 性能调优指南

### 8.1 吞吐量基准与优化

| 场景 | 预期 QPS | 优化建议 |
|---|---|---|
| Laplace count（1K 样本） | ~500 req/s | 默认配置，无需调优 |
| Gaussian sum（10K 样本 + clip） | ~200 req/s | 解析高斯机制计算开销较大，可接受 |
| mean（组合 count + sum） | ~100 req/s | 两次查询，预算消耗翻倍 |
| histogram（10 类别） | ~300 req/s | 桶数增加时线性下降 |
| chunked sum（100 chunks） | ~50 req/s | 网络传输开销为主 |

**优化方向**：

1. **减少数据传输**：对大数据集使用 chunked 接口分块流式传入，避免单次 HTTP 请求体过大。
2. **批量查询**：使用 `dp_aggregate` 一次提交多列聚合，减少 HTTP 开销和预算拆分。
3. **连接复用**：SQLite 模式下 thread-local 连接自动复用，无需额外配置。
4. **Gunicorn/Uvicorn workers**：多进程部署时，每个 worker 独立持有内存预算，建议设置 `PRIVACY_BUDGET_DB` 使用 SQLite 共享存储保证一致性。

### 8.2 内存占用优化

| 组件 | 内存占用 | 优化建议 |
|---|---|---|
| DPApi（无 ML） | ~20 MB | 基础开销，无需优化 |
| BudgetAccountant（内存模式） | ~1 KB/namespace | 可忽略 |
| BudgetAccountant（SQLite 模式） | ~10 KB/namespace + 连接缓存 | thread-local 连接自动管理 |
| NumPy 向量化 clip | ~8 bytes/元素 | 大数据集时主要开销 |
| SecureRandom | ~1 KB | OS 级 CSPRNG，固定开销 |

**优化方向**：

1. **大数据集裁剪**：对 > 1M 样本的数据集，先通过 SQL/Spark 预聚合，再传入 sidecar。
2. **NumPy 自动回退**：若 NumPy 不可用，自动回退到纯 Python，但性能下降 10-100x。
3. **ML 模型懒加载**：分类层（NER/LLM）的模型仅在首次调用时加载，不影响 DP 模块。

### 8.3 延迟优化

| 操作 | 典型延迟 | 优化建议 |
|---|---|---|
| Laplace 噪声采样 | < 1 μs | 无需优化 |
| Gaussian 噪声采样（解析机制） | ~50-200 μs | 二分查找开销，可接受 |
| SQLite spend() | ~1-5 ms | 连接复用后接近本地磁盘 I/O |
| HMAC 审计日志写入 | ~0.1 ms | 追加写，无锁竞争 |
| 解析高斯机制（calibrate_analytic_gaussian） | ~100-500 μs | 二分查找 + 倍增法，已优化 |

**优化方向**：

1. **避免频繁预算查询**：`remaining()` 在 SQLite 模式下需要磁盘读取，可缓存结果。
2. **批量噪声采样**：对需要多次独立采样的场景，使用 NumPy 向量化采样替代循环。
3. **异步预热**：设置 `PRIVACY_WARMUP_LLM=true` 在启动时异步预热 ML 模型，避免首次请求延迟。

### 8.4 预算消耗优化

| 策略 | 节省幅度 | 适用场景 |
|---|---|---|
| 使用 RDPAccountant 紧致估计 | 30-70% | 高斯机制多次查询 |
| 并行组合（独立数据集） | 50-90% | 多数据集独立查询 |
| 减少查询维度 | 线性 | 合并相关查询 |
| 增大 epsilon_per_query | 减少总查询次数 | 精度要求不高时 |
| 使用 Laplace 替代 Gaussian | 无需 delta 预算 | 纯 ε-DP 场景 |

**最佳实践**：

1. **预算分配策略**：对重要查询分配更多预算，对探索性查询分配较少预算。
2. **时间窗口重置**：设置 `PRIVACY_BUDGET_WINDOW_SECONDS=86400` 每天自动重置，避免预算耗尽。
3. **监控预算消耗速率**：通过 Prometheus 指标 `privacy_budget_remaining` 设置告警规则。
4. **审计日志分析**：定期分析 HMAC 审计日志，识别异常消耗模式。

### 8.5 生产环境推荐配置

```bash
# 核心配置
PRIVACY_REST_HOST=0.0.0.0
PRIVACY_REST_PORT=8079
PRIVACY_GRPC_HOST=0.0.0.0
PRIVACY_GRPC_PORT=50051

# 预算持久化（多实例部署必需）
PRIVACY_BUDGET_DB=/data/budget.db
PRIVACY_BUDGET_WINDOW_SECONDS=86400

# 安全配置（生产环境强烈建议）
PRIVACY_TLS_ENABLED=true
PRIVACY_AUTH_ENABLED=true
PRIVACY_RATE_LIMIT_ENABLED=true
PRIVACY_RATE_LIMIT_RPM=1000

# 可观测性
PRIVACY_LOG_LEVEL=INFO
PRIVACY_LOG_FORMAT=json
PRIVACY_SERVICE_NAME=PrivShield
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317

# 性能调优
PRIVACY_ASYNC_MAX_WORKERS=8
PRIVACY_ASYNC_JOB_TTL_SECONDS=3600
PRIVACY_ASYNC_MAX_JOBS=1000
```

**硬件建议**：

| 部署规模 | CPU | 内存 | 存储 | 网络 |
|---|---|---|---|---|
| 小型（< 100 QPS） | 2 cores | 4 GB | 10 GB SSD | 1 Gbps |
| 中型（100-1000 QPS） | 4 cores | 8 GB | 50 GB SSD | 1 Gbps |
| 大型（> 1000 QPS） | 8+ cores | 16+ GB | 100+ GB NVMe | 10 Gbps |

> **注意**：DP 模块本身计算开销较小，主要瓶颈通常在数据传输和预算数据库 I/O。建议优先优化网络带宽和数据库存储性能。