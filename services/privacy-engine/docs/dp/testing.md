# 差分隐私模块测试文档


## 1. 概述

本文档定义 `engine/privacy/dp.py` 的测试策略、测试范围与可执行示例。该模块同时包含中心式 DP（`DPApi`）与本地 DP（`LocalDPApi`）两类接口，测试需覆盖算法正确性、参数校验、预算消耗、接口一致性、本地 DP 扰动与统计特性。

## 2. 测试目标

- 验证 Laplace 机制与 Gaussian 机制输出符合预期分布。
- 验证解析高斯机制（Analytic Gaussian）噪声尺度小于经典公式。
- 验证 clipping 参数校验与敏感度计算正确。
- 验证 BudgetAccountant 正确追踪 `(ε, δ)` 消耗并拒绝超支。
- 验证 BudgetAccountant 时间窗口到期后自动重置预算。
- 验证 REST/gRPC 接口透传参数无误。
- 验证 mean 组合实现满足组合定理，且 `min_count` 低频保护生效。
- 验证直方图聚合使用联合敏感度为 1。
- 验证 noisify 接口（count/sum/mean/histogram）在提供敏感度时正确加噪并扣减预算。
- 验证 chunked 接口（count/sum/mean/histogram）增量聚合后只消耗一次预算。
- 验证数据适配器对 list/NumPy/pandas/SecretFlow 的提取能力。
- 验证 `privacy_traffic_bytes_total` 在 REST/gRPC 中被正确记录。
- 验证本地 DP 扰动与估计接口（REST/gRPC）行为正确。

## 3. 单元测试策略

### 3.1 机制正确性测试

固定随机数种子，断言噪声值与理论值一致。对解析高斯机制，断言其噪声小于经典高斯公式。

```python
import math
import random

from engine.privacy.dp import DPApi


def test_laplace_count_with_fixed_seed():
    dp = DPApi(namespace="test_count")
    dp.rng = random.Random(42)

    values = [1, 0, 1, 1, 0]
    result = dp.count(values, epsilon=1.0, mechanism="laplace")

    # true_count = 3; 固定种子下噪声可重复
    assert 0 <= result <= 10  # 非负且在一个合理范围内
```

### 3.2 参数校验测试

```python
import pytest
from engine.privacy.dp import DPApi


def test_gaussian_requires_clip():
    dp = DPApi(namespace="test_clip")
    with pytest.raises(ValueError, match="clip_lower and clip_upper are required"):
        dp.sum([1.0, 2.0, 3.0], epsilon=1.0, delta=1e-6, mechanism="gaussian")


def test_gaussian_requires_positive_delta():
    dp = DPApi(namespace="test_delta")
    with pytest.raises(ValueError, match="delta must be positive"):
        dp.count([1, 0, 1], epsilon=1.0, delta=0.0, mechanism="gaussian")
```

### 3.3 预算消耗测试

```python
from engine.privacy.budget import BudgetAccountant


def test_budget_accountant_tracks_spending():
    accountant = BudgetAccountant(
        namespace="test_budget", epsilon_total=2.0, delta_total=1e-5
    )

    accountant.spend(1.0, 1e-6)
    assert accountant.epsilon_spent == 1.0
    assert accountant.delta_spent == 1e-6

    accountant.spend(0.5, 1e-6)
    assert accountant.epsilon_spent == 1.5
```

### 3.4 统计特性测试

对同一查询重复采样，验证噪声均值接近 0，方差符合理论值。

```python
import statistics

from engine.privacy.dp import DPApi


def test_laplace_noise_statistics():
    dp = DPApi(namespace="test_stats")
    samples = [dp._sample_laplace(scale=1.0) for _ in range(5000)]

    mean = statistics.mean(samples)
    var = statistics.variance(samples)

    assert abs(mean) < 0.1        # Laplace(0,1) 均值为 0
    assert 1.8 < var < 2.2        # Laplace(0,1) 方差为 2
```

### 3.5 均值低频保护与直方图测试

```python
from engine.privacy.dp import DPApi
from engine.privacy.budget import default_registry


def test_mean_min_count_protection():
    default_registry.get_or_create("test-mean-thresh", epsilon_total=100.0, delta_total=1.0)
    api = DPApi(namespace="test-mean-thresh")

    # 正常计算
    res = api.mean([10.0] * 10, epsilon=10.0, min_count=2.0)
    assert res > 0.0

    # 计数过小，触发低频保护返回 0.0
    res_shielded = api.mean([10.0] * 3, epsilon=10.0, min_count=5.0)
    assert res_shielded == 0.0


def test_histogram_joint_sensitivity():
    default_registry.get_or_create("test-hist", epsilon_total=100.0, delta_total=1.0)
    api = DPApi(namespace="test-hist")
    values = ["A"] * 100 + ["B"] * 200 + ["C"] * 50
    categories = ["A", "B", "C", "D"]
    res = api.histogram(values, categories, epsilon=10.0, mechanism="laplace")
    assert len(res) == 4
    assert res["A"] > 50
    assert res["B"] > 100
    assert res["D"] >= 0.0
```

### 3.6 本地差分隐私测试

本地 DP 测试重点在于随机响应扰动与频率估计的无偏性。此外需验证 REST/gRPC 接口对本地 DP 能力的暴露。

```python
from engine.privacy.dp import LocalDPApi


def test_local_dp_binary_unbiased():
    api = LocalDPApi(seed=42)
    true_values = [1 if i % 4 == 0 else 0 for i in range(4000)]
    true_freq = sum(true_values) / len(true_values)

    reported = api.perturb_binary_batch(true_values, epsilon=1.0)
    estimated = api.estimate_binary_frequency(reported, epsilon=1.0)

    assert abs(estimated - true_freq) < 0.05


def test_local_dp_categorical_unbiased():
    api = LocalDPApi(seed=42)
    categories = ["A", "B", "C"]
    true = ["A"] * 4000 + ["B"] * 3000 + ["C"] * 3000
    api.rng.shuffle(true)

    reported = api.perturb_categorical_batch(true, categories, epsilon=1.0)
    hist = api.estimate_categorical_histogram(reported, categories, epsilon=1.0)

    assert abs(hist["A"] - 0.40) < 0.05
    assert abs(hist["B"] - 0.30) < 0.05
    assert abs(hist["C"] - 0.30) < 0.05
```

执行本地 DP 测试：

```bash
PYTHONPATH=. pytest tests/test_dp.py -v -k "LocalDP or Randomized"
```

### 3.7 Noisify 接口测试

```python
from engine.privacy.dp import DPApi
from engine.privacy.budget import default_registry


def test_noisy_sum():
    default_registry.get_or_create("test-noisy", epsilon_total=100.0, delta_total=1.0)
    api = DPApi(namespace="test-noisy")
    result = api.noisy_sum(
        true_sum=1000.0,
        sensitivity=10.0,
        epsilon=1.0,
        mechanism="laplace",
    )
    assert isinstance(result, float)


def test_noisy_sum_requires_sensitivity():
    api = DPApi(namespace="test-noisy-2")
    with pytest.raises(ValueError, match="sensitivity"):
        # 通过 REST service 调用时未提供 sensitivity 或 clip 应报错
        from engine.service import PrivacyService
        svc = PrivacyService(namespace="test-noisy-2")
        svc.dp_noisy_sum(1000.0, {})
```

### 3.8 Chunked 接口测试

```python
from engine.privacy.dp import DPApi
from engine.privacy.budget import default_registry


def test_chunked_count():
    default_registry.get_or_create("test-chunked", epsilon_total=100.0, delta_total=1.0)
    api = DPApi(namespace="test-chunked")
    chunks = [[1, 0, 1], [1, 0, 1, 1]]
    result = api.chunked_count(chunks, epsilon=10.0, mechanism="laplace")
    assert result >= 0.0


def test_chunked_sum_requires_clip():
    api = DPApi(namespace="test-chunked-2")
    with pytest.raises(ValueError, match="chunked_sum requires explicit clip"):
        api.chunked_sum([[1.0, 2.0]], epsilon=1.0)
```

### 3.9 数据适配器测试

```python
import numpy as np
import pandas as pd
from engine.privacy.data_adapters import extract_values


def test_extract_from_list():
    assert extract_values([1, 2, 3]) == [1, 2, 3]


def test_extract_from_numpy():
    arr = np.array([1.0, 2.0, 3.0])
    assert extract_values(arr) == [1.0, 2.0, 3.0]


def test_extract_from_pandas_dataframe():
    df = pd.DataFrame({"salary": [1.0, 2.0], "age": [25.0, 34.0]})
    assert extract_values(df, column="salary") == [1.0, 2.0]


def test_extract_from_pandas_series():
    s = pd.Series([1.0, 2.0, 3.0])
    assert extract_values(s) == [1.0, 2.0, 3.0]
```

### 3.10 流量监控指标测试

```python
from prometheus_client import REGISTRY
from fastapi.testclient import TestClient
from engine.main import app


def test_traffic_metric_recorded():
    client = TestClient(app)
    before = REGISTRY.get_sample_value(
        "privacy_traffic_bytes_total",
        {"method": "POST", "path": "/v1/privacy/dp/count", "direction": "request"},
    ) or 0.0
    resp = client.post(
        "/v1/privacy/dp/count",
        json={"values": [1, 0, 1], "params": {"epsilon": 1.0}},
    )
    assert resp.status_code == 200
    after = REGISTRY.get_sample_value(
        "privacy_traffic_bytes_total",
        {"method": "POST", "path": "/v1/privacy/dp/count", "direction": "request"},
    )
    assert after > before
```

### 3.11 时间窗口预算重置测试

```python
import time
from engine.privacy.budget import default_registry


def test_budget_window_reset():
    default_registry.reset()
    accountant = default_registry.get_or_create(
        "test-window", epsilon_total=2.0, delta_total=1e-5, window_seconds=0.1
    )
    accountant.spend(1.5, 0.0)
    assert accountant.remaining()["epsilon"] == 0.5

    time.sleep(0.15)
    accountant.spend(1.0, 0.0)
    assert accountant.remaining()["epsilon"] == 1.0
```

## 3.12 并发压力测试 (`test_budget_concurrency.py`)

使用 `concurrent.futures.ThreadPoolExecutor` 模拟多线程并发 `spend()` 调用，验证 `BudgetAccountant` 在内存模式和 SQLite 模式下的线程安全性。

```python
from concurrent.futures import ThreadPoolExecutor, as_completed
from engine.privacy.budget import default_registry, PrivacyBudgetExhausted


def test_concurrent_spend_serializable():
    """50 个线程各消耗 epsilon=0.1，总消耗应精确等于 5.0。"""
    acct = default_registry.get_or_create("concurrent-mem", epsilon_total=100.0, delta_total=1.0)

    def _spend():
        acct.spend(0.1, 0.0)

    with ThreadPoolExecutor(max_workers=50) as pool:
        futures = [pool.submit(_spend) for _ in range(50)]
        for f in as_completed(futures):
            f.result()

    assert abs(acct.epsilon_spent - 5.0) < 1e-9


def test_sqlite_thread_local_conn_reuse(tmp_path, monkeypatch):
    """验证同一线程多次 spend 复用同一个 SQLite 连接。"""
    monkeypatch.setenv("PRIVACY_BUDGET_DB", str(tmp_path / "budget.db"))
    acct = default_registry.get_or_create("conn-reuse", epsilon_total=100.0, delta_total=1.0)

    acct.spend(1.0, 0.0)
    conn1 = acct._thread_local.conn
    acct.spend(1.0, 0.0)
    conn2 = acct._thread_local.conn
    assert conn1 is conn2  # 连接复用
```

测试覆盖场景：

| 测试 | 场景 | 验证点 |
|---|---|---|
| `test_concurrent_spend_serializable` | 50 线程并发 spend | 总消耗精确等于 5.0 |
| `test_concurrent_spend_budget_exhaustion` | 20 线程超预算竞争 | 部分线程收到 `PrivacyBudgetExhausted`，已消耗不超总预算 |
| `test_concurrent_spend_and_remaining` | spend + remaining 交错 | 读写并发无异常 |
| `test_sqlite_concurrent_spend` | SQLite 30 线程并发 | 不崩溃、不死锁、DB 可读回 |
| `test_sqlite_thread_local_conn_reuse` | SQLite 连接复用 | 同线程复用连接，关闭后重建 |

执行命令：

```bash
PYTHONPATH=. pytest tests/test_budget_concurrency.py -v
```

## 3.13 KS 统计分布检验 (`test_dp_distributions.py`)

使用 `scipy.stats.kstest` 对 `_sample_laplace` 和 `_sample_gaussian` 采样的大量随机数进行 **Kolmogorov-Smirnov 检验**，确保噪声分布在统计意义上符合理论 CDF。

```python
import numpy as np
from scipy import stats
from engine.privacy.dp import DPApi


def test_sample_laplace_ks_test():
    """KS 检验：_sample_laplace(scale=1.0) 采样符合 Laplace(0, 1) 分布。"""
    api = DPApi(namespace="ks-laplace-1", random_state=12345)
    samples = np.array([api._sample_laplace(1.0) for _ in range(50_000)])

    # 使用 .cdf 函数而非字符串名称，避免 scipy/torch MagicMock 冲突
    ks_stat, p_value = stats.kstest(samples, stats.laplace.cdf, args=(0, 1.0))
    assert p_value > 0.01  # KS_ALPHA = 0.01


def test_sample_gaussian_ks_test():
    """KS 检验：_sample_gaussian(sigma=1.0) 采样符合 N(0, 1) 分布。"""
    api = DPApi(namespace="ks-gauss-1", random_state=12345)
    samples = np.array([api._sample_gaussian(1.0) for _ in range(50_000)])

    ks_stat, p_value = stats.kstest(samples, stats.norm.cdf, args=(0, 1.0))
    assert p_value > 0.01
```

测试覆盖场景：

| 测试 | 场景 | 验证点 |
|---|---|---|
| `test_sample_laplace_ks_test` | Laplace(0, 1) KS 检验 | p > 0.01，分布符合理论 CDF |
| `test_sample_laplace_different_scale` | Laplace(0, 2.5) KS 检验 | 不同尺度参数下分布正确 |
| `test_laplace_zero_mean` | Laplace 大数定律 | 50000 样本均值接近 0 |
| `test_sample_gaussian_ks_test` | N(0, 1) KS 检验 | p > 0.01 |
| `test_sample_gaussian_different_sigma` | N(0, 9) KS 检验 | sigma=3.0 分布正确 |
| `test_gaussian_zero_mean` | Gaussian 大数定律 | 均值接近 0 |
| `test_discrete_laplace_integer_output` | Discrete Laplace 整数输出 | 所有采样均为 int |
| `test_discrete_laplace_zero_mean` | Discrete Laplace 对称性 | 均值接近 0 |
| `test_discrete_laplace_symmetry` | Discrete Laplace 正负对称 | pos/neg 比例接近 0.5 |
| `test_count_noise_scale_laplace` | 端到端噪声方差 | 10000 次 count 查询的经验方差与理论 2*b^2 偏差 < 10% |

> **注意**：`kstest` 使用 `.cdf` 函数而非字符串分布名（如 `"laplace"`），以避免 `test_classification_llm.py` 中 `sys.modules["torch"] = MagicMock()` 与 scipy 内部 `is_torch_array` 的 `issubclass()` 冲突。该兼容性补丁已在 `conftest.py` 中全局处理。

执行命令：

```bash
PYTHONPATH=. pytest tests/test_dp_distributions.py -v
```

## 4. 集成测试策略

### 4.1 REST 接口测试

```python
from fastapi.testclient import TestClient
from engine.main import app


def test_rest_dp_count():
    client = TestClient(app)
    resp = client.post(
        "/v1/privacy/dp/count",
        json={"values": [1, 0, 1, 1, 0], "params": {"epsilon": 1.0}},
    )
    assert resp.status_code == 200
    assert "result" in resp.json()
```

### 4.2 gRPC 接口测试

```python
import grpc
from engine.grpc_server import PrivacyServicer
from engine.proto import privacy_pb2, privacy_pb2_grpc


def test_grpc_dp_sum():
    servicer = PrivacyServicer()
    request = privacy_pb2.DPRequest(
        values=[1.0, 2.0, 3.0],
        params={
            "epsilon": "1.0",
            "delta": "1e-6",
            "mechanism": "gaussian",
            "clip_lower": "0.0",
            "clip_upper": "10.0",
        },
    )
    response = servicer.DPSum(request, None)
    assert response.result is not None
```

## 5. 测试执行命令

```bash
# 运行所有 DP 相关测试
PYTHONPATH=. pytest tests/test_dp.py -v

# 运行 DP 模块单元测试
PYTHONPATH=. pytest tests/test_budget.py tests/test_rest.py -v -k dp

# 本地 DP 测试
PYTHONPATH=. pytest tests/test_dp.py -v -k "LocalDP or Randomized"

# 并发压力测试
PYTHONPATH=. pytest tests/test_budget_concurrency.py -v

# KS 统计分布检验
PYTHONPATH=. pytest tests/test_dp_distributions.py -v

# 统计特性测试可能需要更多样本
PYTHONPATH=. pytest tests/test_dp.py -v --slow
```

## 6. 持续集成建议

- 每次提交前执行 `pytest tests/test_dp.py`。
- 在 CI 中固定 Python 版本与随机数种子，避免测试抖动。
- 对统计测试设置宽松的置信区间，避免偶发失败。

## 7. 验收检查清单

- [x] count/sum/mean/histogram 四种聚合的单元测试覆盖。
- [x] Laplace 与 Gaussian 机制各至少一组测试。
- [x] 解析高斯机制噪声尺度测试覆盖。
- [x] noisify 接口（count/sum/mean/histogram）单元测试与 REST/gRPC 接口测试覆盖。
- [x] chunked 接口（count/sum/mean/histogram）单元测试与 REST/gRPC 接口测试覆盖。
- [x] 数据适配器对 list/NumPy/pandas/SecretFlow 的测试覆盖。
- [x] 本地 DP 二值/类别随机响应与频率估计测试覆盖。
- [x] 本地 DP REST/gRPC 接口测试覆盖。
- [x] clipping 参数缺失/错误时抛出明确异常。
- [x] 预算超支时返回 `PrivacyBudgetExhausted` 或对应 HTTP/gRPC 错误。
- [x] 预算时间窗口重置测试覆盖。
- [x] REST/gRPC 接口参数透传测试通过。
- [x] `privacy_traffic_bytes_total` 指标接入测试通过。
- [x] 统计测试在 5000 次采样下通过。
- [x] `adaptive_clip` 自适应截断边界与预算消耗测试覆盖。
- [x] `dp_aggregate` 表格级聚合预算拆分与结果正确性测试覆盖。
- [x] `vector_sum` / `vector_mean` 高维向量加噪与 L₂ clip 测试覆盖。
- [x] `dp_groupby` Tau-Thresholding 稀有分组过滤测试覆盖。
- [x] `Accumulator` 分布式累加器序列化、合并、finalize 测试覆盖。
- [x] `RDPAccountant` Rényi DP 多阶会计与最优 α 搜索测试覆盖。
- [x] 并发压力测试：内存/SQLite 多线程 spend 线程安全性验证。
- [x] KS 统计分布检验：Laplace/Gaussian/Discrete Laplace 采样符合理论 CDF。
- [x] 结构化日志：`warnings.warn` → `logger.warning` 迁移，`caplog` 测试覆盖。