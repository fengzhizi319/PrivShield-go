# 差分隐私模块使用示例


## 1. 概述

本文档提供 `engine.privacy.dp.DPApi` 与 REST API 的典型使用示例，帮助开发者快速上手 count、sum、mean 三种差分隐私聚合。

## 2. Python SDK 示例

### 2.1 计数查询（Count）

```python
from engine.privacy.dp import DPApi

# 使用独立命名空间管理预算
dp = DPApi(namespace="hospital_cohort_a")

values = [1, 0, 1, 1, 0, 1, 0, 0, 1, 1]
result = dp.count(values, epsilon=1.0, mechanism="laplace")
print(f"带噪声计数: {result}")
```

### 2.2 求和查询（Sum）

```python
from engine.privacy.dp import DPApi

dp = DPApi(namespace="hospital_cohort_a")

# 医疗费用，先截断到 [0, 100000]
charges = [1200.0, 5800.0, 300.0, 99999.0, 15000.0]
result = dp.sum(
    charges,
    epsilon=1.0,
    mechanism="laplace",
    clip_lower=0.0,
    clip_upper=100000.0,
)
print(f"带噪声总费用: {result}")
```

### 2.3 均值查询（Mean）

```python
from engine.privacy.dp import DPApi

dp = DPApi(namespace="hospital_cohort_a")

ages = [
    25.0, 34.0, 45.0, 52.0, 29.0, 61.0,
    38.0, 41.0, 27.0, 55.0, 33.0, 48.0,
    36.0, 50.0, 31.0, 44.0, 39.0, 58.0,
]
result = dp.mean(
    ages,
    epsilon=2.0,
    mechanism="gaussian",
    delta=1e-6,
    clip_lower=0.0,
    clip_upper=120.0,
    min_count=5.0,  # 低频保护阈值
)
print(f"带噪声平均年龄: {result}")
```

### 2.4 直方图查询（Histogram）

```python
from engine.privacy.dp import DPApi

dp = DPApi(namespace="hospital_cohort_a")

# 科室分布，互斥划分下联合敏感度为 1
departments = ["Cardiology"] * 100 + ["Neurology"] * 80 + ["Oncology"] * 50
result = dp.histogram(
    departments,
    categories=["Cardiology", "Neurology", "Oncology", "Other"],
    epsilon=1.0,
    mechanism="laplace",
)
print(f"带噪声科室分布: {result}")
```

### 2.4 Gaussian 机制示例

```python
from engine.privacy.dp import DPApi

dp = DPApi(namespace="financial_data")

# Gaussian 机制必须提供 delta > 0 与 clip 区间
revenues = [100.0, 200.0, 150.0, 300.0, 250.0]
result = dp.sum(
    revenues,
    epsilon=1.0,
    delta=1e-6,
    mechanism="gaussian",
    clip_lower=0.0,
    clip_upper=500.0,
)
print(f"Gaussian 带噪声总收入: {result}")
```

### 2.5 DataFrame 与 SecretFlow 输入示例

```python
from engine.privacy.dp import DPApi
import pandas as pd

api = DPApi(namespace="hr_dataset")

# pandas DataFrame
df = pd.DataFrame({
    "salary": [5000.0, 8000.0, 12000.0],
    "age": [25.0, 34.0, 45.0],
})
result = api.sum(df, column="salary", epsilon=1.0, clip_lower=0.0, clip_upper=100000.0)
print(f"带噪声总工资: {result}")
```

SecretFlow 联邦数据（需安装 `secretflow`）：

```python
from engine.privacy.dp import DPApi

api = DPApi(namespace="hr_dataset")

# VDataFrame：列分布在不同参与方，自动定位 salary 所在 partition
result = api.sum(vdf, column="salary", epsilon=1.0, clip_lower=0.0, clip_upper=100000.0)

# HDataFrame：样本水平分割，需指定参与方
result = api.sum(hdf, column="salary", party="alice", epsilon=1.0, clip_lower=0.0, clip_upper=100000.0)
```

### 2.6 Noisify 接口示例（Spark/SQL/DuckDB 工作流）

```python
from engine.privacy.dp import DPApi

api = DPApi(namespace="monthly_report")

# 假设外部 SQL 引擎已计算出真实总和
true_sum = 5_000_000.0
sensitivity = 100_000.0  # 对应 clip_upper - clip_lower

result = api.noisy_sum(
    true_sum=true_sum,
    sensitivity=sensitivity,
    epsilon=1.0,
    delta=1e-6,
    mechanism="gaussian",
)
print(f"带噪声总收入: {result}")
```

### 2.7 Chunked 流式聚合示例

```python
from engine.privacy.dp import DPApi

api = DPApi(namespace="streaming_data")

# 模拟从文件/流中分批读取的数据
chunks = [
    [1.0, 2.0, 3.0],
    [4.0, 5.0, 6.0],
    [7.0, 8.0, 9.0],
]

result = api.chunked_sum(
    chunks=chunks,
    epsilon=1.0,
    delta=1e-6,
    mechanism="gaussian",
    clip_lower=0.0,
    clip_upper=10.0,
)
print(f"分块带噪声求和: {result}")
```

### 2.8 预算管理示例

```python
from engine.privacy.budget import default_registry

# 通过注册表初始化预算账户（推荐方式）
accountant = default_registry.get_or_create(
    namespace="monthly_report", epsilon_total=4.0, delta_total=1e-5
)

# 执行若干查询...
accountant.spend(1.0, 1e-6)
accountant.spend(1.0, 1e-6)

print(f"已用 epsilon: {accountant.epsilon_spent}")
print(f"已用 delta: {accountant.delta_spent}")
print(f"剩余 epsilon: {accountant.epsilon_total - accountant.epsilon_spent}")
```

## 3. REST API 示例

### 3.1 Count

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/dp/count \
  -H "Content-Type: application/json" \
  -d '{
    "values": [1, 0, 1, 1, 0],
    "params": {"epsilon": 1.0}
  }'
```

### 3.2 Sum with Clipping

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/dp/sum \
  -H "Content-Type: application/json" \
  -d '{
    "values": [1200, 5800, 300, 99999, 15000],
    "params": {
      "epsilon": 1.0,
      "mechanism": "laplace",
      "clip_lower": 0.0,
      "clip_upper": 100000.0
    }
  }'
```

### 3.3 Mean with Gaussian

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/dp/mean \
  -H "Content-Type: application/json" \
  -d '{
    "values": [25, 34, 45, 52, 29, 61],
    "params": {
      "epsilon": 2.0,
      "delta": 1e-6,
      "mechanism": "gaussian",
      "clip_lower": 0.0,
      "clip_upper": 120.0,
      "min_count": 3.0
    }
  }'
```

### 3.4 Histogram

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/dp/histogram \
  -H "Content-Type: application/json" \
  -d '{
    "values": ["A", "B", "A", "C"],
    "categories": ["A", "B", "C", "D"],
    "params": {
      "epsilon": 10.0,
      "mechanism": "laplace"
    }
  }'
```

### 3.5 Noisify via REST

```bash
# 对已聚合求和加噪
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

### 3.6 Chunked via REST

```bash
# 分块流式差分隐私求和
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

### 3.7 Local DP via REST

```bash
# 二值扰动
curl -X POST http://127.0.0.1:8079/v1/privacy/ldp/perturb/binary \
  -H "Content-Type: application/json" \
  -d '{"values": [1, 0, 1, 1], "epsilon": 10.0}'

# 类别扰动
curl -X POST http://127.0.0.1:8079/v1/privacy/ldp/perturb/categorical \
  -H "Content-Type: application/json" \
  -d '{"values": ["A", "B", "A"], "categories": ["A", "B", "C"], "epsilon": 10.0}'

# 二值频率估计
curl -X POST http://127.0.0.1:8079/v1/privacy/ldp/estimate/binary \
  -H "Content-Type: application/json" \
  -d '{"reported_values": [1, 1, 0, 1], "epsilon": 5.0}'

# 类别直方图估计
curl -X POST http://127.0.0.1:8079/v1/privacy/ldp/estimate/categorical \
  -H "Content-Type: application/json" \
  -d '{"reported_values": ["A", "B", "C", "A"], "categories": ["A", "B", "C"], "epsilon": 5.0}'
```

## 4. 本地差分隐私示例

本地 DP 中，每个用户在数据离开设备前完成扰动，服务器只收到扰动后的值。

### 4.1 二值随机响应

```python
from engine.privacy.dp import LocalDPApi

api = LocalDPApi(seed=42)

# 真实样本：30% 为 1（例如"患有某疾病"）
n = 2000
true_values = [1 if i < 0.30 * n else 0 for i in range(n)]

# 每个用户本地扰动
reported = api.perturb_binary_batch(true_values, epsilon=1.0)

# 服务器纠偏估计真实频率
estimated = api.estimate_binary_frequency(reported, epsilon=1.0)
print(f"真实患病率: {sum(true_values)/n:.2%}")
print(f"纠偏估计患病率: {estimated:.2%}")
```

### 4.2 类别型随机响应与本地直方图

```python
from engine.privacy.dp import LocalDPApi

api = LocalDPApi(seed=42)

categories = ["Apple", "Samsung", "Xiaomi"]
true_brands = ["Apple"] * 5000 + ["Samsung"] * 3000 + ["Xiaomi"] * 2000
api.rng.shuffle(true_brands)

reported = api.perturb_categorical_batch(true_brands, categories, epsilon=1.0)
hist = api.estimate_categorical_histogram(reported, categories, epsilon=1.0)

for c in categories:
    print(f"{c}: {hist[c]:.2%}")
```

### 4.3 运行完整示例

```bash
PYTHONPATH=. python docs/dp/examples/local_dp_usage.py
```

## 5. 最佳实践

1. **始终使用显式 clip 区间**：生产环境中不要依赖 Laplace 的向后兼容数据推断，避免违反 DP 形式化要求。
2. **为不同数据集使用不同 namespace**：防止跨数据集共享隐私预算。
3. **优先使用 Laplace 进行纯 ε-DP**：若业务不能接受 δ，选择 Laplace。
4. **Gaussian 适合大量组合查询**：在需要高级组合定理时，Gaussian 机制更紧致。
5. **本地 DP 适合大样本频率估计**：样本量较小时统计误差较大，不适合需要精确聚合的场景。
6. **海量数据优先使用 noisify 接口**：在 Spark/SQL/DuckDB 中完成聚合后，仅将中间结果发送到 sidecar 加噪。
7. **分块输入使用 chunked 接口**：当数据无法一次性加载内存时，使用 chunked 聚合降低峰值内存。
8. **为长期运行服务配置预算时间窗口**：通过 `PRIVACY_BUDGET_WINDOW_SECONDS` 避免预算永久耗尽。
9. **记录每次查询的预算消耗**：便于审计与后续预算调整。
10. **监控 `privacy_traffic_bytes_total`**：观察 REST/gRPC 流量，辅助容量规划。

## 6. 高级特性示例

### 6.1 自适应截断（Adaptive Clipping）

当数据范围未知时，先用 `adaptive_clip` 搜索 clip 上界，再执行聚合查询。

```python
import numpy as np
from engine.privacy.dp import DPApi

api = DPApi(namespace="adaptive_demo")
data = np.random.exponential(scale=5.0, size=10000).tolist()

# Step 1: 自适应搜索 clip 上界（消耗 epsilon=0.5）
clip_lower, clip_upper = api.adaptive_clip(data, epsilon=0.5, target_quantile=0.95)
print(f"搜索到的 clip 上界: {clip_upper:.2f}")

# Step 2: 使用搜索到的 clip bounds 进行 sum 查询（额外消耗 epsilon=0.5）
result = api.sum(data, epsilon=0.5, clip_lower=clip_lower, clip_upper=clip_upper)
print(f"DP sum: {result:.2f}")
```

### 6.2 表格级 DP 聚合（dp_aggregate）

```python
import pandas as pd
from engine.privacy.dp import DPApi

api = DPApi(namespace="table_demo")
df = pd.DataFrame({
    "age": [25, 30, 35, 40, 45, 50, 55],
    "salary": [30000, 45000, 60000, 75000, 90000, 50000, 65000],
})

result = api.dp_aggregate(
    df,
    specs=[
        {"column": "age", "agg": "count"},
        {"column": "salary", "agg": "sum", "clip_lower": 0, "clip_upper": 100000},
        {"column": "age", "agg": "mean", "clip_lower": 0, "clip_upper": 150},
    ],
    epsilon=1.0,
)
print(result)
```

### 6.3 DP-SGD 平均梯度（vector_mean）

```python
import numpy as np
from engine.privacy.dp import DPApi

api = DPApi(namespace="dpsgd_demo")

# 模拟 100 个样本的梯度（每个 3 维）
np.random.seed(42)
gradients = np.random.randn(100, 3).tolist()

# DP 平均梯度
avg_grad = api.vector_mean(
    gradients, max_norm=1.0, epsilon=0.5, delta=1e-5, mechanism="gaussian"
)
print(f"DP 平均梯度: {avg_grad}")
```

### 6.4 Tau-Thresholding Group-By

```python
import pandas as pd
from engine.privacy.dp import DPApi

api = DPApi(namespace="groupby_demo")
df = pd.DataFrame({
    "department": ["Eng"]*100 + ["Sales"]*50 + ["HR"]*3 + ["Legal"]*2,
    "salary": list(range(100)) + list(range(50)) + [30000, 35000, 40000] + [50000, 55000],
})

# 自动过滤稀有分组（HR/Legal 可能被过滤）
result = api.dp_groupby(
    df, group_col="department", target_col="salary",
    agg="count", epsilon=1.0, delta=1e-5,
)
print(f"DP Group-By count: {result}")
```

### 6.5 分布式累加器（Accumulator）

```python
from engine.privacy.dp import DPApi

api = DPApi(namespace="distributed_demo")

# Worker 1: 本地无噪累加
chunk1 = [1.0, 2.0, 3.0, 4.0]
acc1 = api.create_accumulator(chunk1, clip_lower=0.0, clip_upper=10.0)

# Worker 2: 本地无噪累加
chunk2 = [5.0, 6.0, 7.0]
acc2 = api.create_accumulator(chunk2, clip_lower=0.0, clip_upper=10.0)

# Master: 合并 + 统一加噪
merged = acc1 + acc2
result = api.finalize_dp(merged, epsilon=1.0, agg_type="sum")
print(f"DP sum (distributed): {result}")
```

### 6.6 Rényi DP 会计

```python
from engine.privacy.budget import RDPAccountant

rdp = RDPAccountant(target_delta=1e-5)

# 记录 10 次 Gaussian 查询
for _ in range(10):
    rdp.record_gaussian(sigma=1.0, sensitivity=1.0)

# 获取总 ε（自动搜索最优 α）
total_epsilon = rdp.get_epsilon(delta=1e-5)
print(f"RDP 总 ε: {total_epsilon:.4f}")
print(f"基本组合总 ε: {10 * 0.5:.4f}")  # 对比基本组合

### 6.7 User-Level DP (用户级贡献绑定)

```python
import numpy as np
from engine.privacy.dp import DPApi

api = DPApi(namespace="user_dp_demo")

# 模拟 100 条日志，其中 user_A 贡献了 90 条，user_B 贡献了 10 条
salaries = np.array([5000.0] * 90 + [8000.0] * 10)
user_ids = ["user_A"] * 90 + ["user_B"] * 10

# 限制每个用户最多保留 2 条记录，敏感度自动调整为 2 * (clip_upper - clip_lower)
res = api.sum(
    salaries,
    epsilon=1.0,
    clip_lower=0.0,
    clip_upper=10000.0,
    user_ids=user_ids,
    max_contributions=2,
)
print(f"User-Level DP sum: {res}")
```

### 6.8 Discrete Laplace 整数加噪与 PyArrow Metadata 导出

```python
from engine.privacy.dp import DPApi

api = DPApi(namespace="arrow_discrete_demo")

# Step 1: 使用 Discrete Laplace 在整数格 ℤ 上精确加噪，返回 DPResult 结构体
dp_result = api.count([1.0, 2.0, 3.0, 4.0, 5.0], epsilon=1.0, discrete=True, return_details=True)

# Step 2: 导出为附带 DP Metadata 的 PyArrow Table
arrow_table = dp_result.to_arrow()
print("PyArrow Schema Metadata:")
print(arrow_table.schema.metadata[b"dp_metadata"].decode("utf-8"))
```
```

## 7. 常见错误

| 错误 | 原因 | 解决 |
|---|---|---|---|
| `ValueError: clip_lower and clip_upper are required for Gaussian mechanism` | Gaussian sum/mean 未提供 clip | 提供 `clip_lower` 与 `clip_upper` |
| `ValueError: chunked_sum requires explicit clip_lower and clip_upper` | chunked sum/mean 未提供 clip | 提供 `clip_lower` 与 `clip_upper` |
| `ValueError: dp_noisy_sum requires 'sensitivity' or both 'clip_lower' and 'clip_upper'` | noisify sum/mean 未提供敏感度 | 提供 `sensitivity` 或 `clip_lower`/`clip_upper` |
| `ValueError: delta must be positive for Gaussian mechanism` | Gaussian 请求中 `delta=0` | 设置 `delta > 0`，典型值 `1e-6` |
| `PrivacyBudgetExhausted` | 命名空间预算已用完 | 提高总预算或减少查询次数 |
| 结果出现负数 | 计数被噪声拉低 | count 已做 `max(0, ...)` 截断；其他场景可后处理 |
| mean 返回 0.0 | 噪声计数低于 `min_count` | 降低 `min_count` 或增加样本量；注意过低会导致结果不稳定 |
| `column must be specified when input is a pandas DataFrame` | DataFrame 未指定目标列 | 在 `params` 中传入 `column` |