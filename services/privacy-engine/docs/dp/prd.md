# 差分隐私（DP）产品设计 PRD


## 1. 概述

本文档定义 `PrivShield` 差分隐私（DP）模块的产品需求与验收标准。DP 模块为 count、sum、mean 等聚合查询提供隐私化输出能力，并通过隐私预算 accountant 控制累计披露风险。

## 2. 设计目标

- 提供基于 Laplace 机制的纯 `ε-DP` 统计查询。
- 提供基于 Gaussian 机制的 `(ε, δ)-DP` 统计查询。
- 提供基于随机响应的本地差分隐私（Local DP）扰动与频率估计。
- 通过显式 clipping 控制 sum/mean 的敏感度。
- 提供 BudgetAccountant 统一追踪 `(ε, δ)` 消耗。
- 暴露一致的 REST 与 gRPC 接口。

## 3. 功能需求

| ID | 需求 |
|---|---|
| DP-COUNT-1 | count 查询敏感度固定为 1，支持 Laplace 与 Gaussian 机制。 |
| DP-SUM-1 | sum 查询必须提供 `clip_lower` / `clip_upper`，敏感度按 clip 区间计算。 |
| DP-MEAN-1 | mean 查询必须提供 `clip_lower` / `clip_upper`，敏感度按 clip 区间与记录数计算。 |
| DP-CLIP-1 | clip 参数可由请求 `params` 传入，也可从 profile 配置读取。 |
| DP-CLIP-2 | sum/mean 若未提供 clip 参数且 profile 未配置，则返回明确错误。 |
| DP-GAUSS-1 | 支持 `mechanism=gaussian`，使用解析高斯机制（Analytic Gaussian）。 |
| DP-GAUSS-2 | Gaussian 机制必须传入 `delta > 0`，并消耗对应 delta 预算。 |
| DP-GAUSS-3 | 解析高斯机制应支持任意 `epsilon > 0`，且噪声尺度不劣于经典公式。 |
| DP-LAPLACE-1 | 支持 `mechanism=laplace`，提供纯 ε-DP 保证。 |
| DP-LOCAL-1 | 提供二值随机响应（Binary Randomized Response），支持单个值与批量扰动。 |
| DP-LOCAL-2 | 提供类别型随机响应（k-ary Randomized Response），支持单个值与批量扰动。 |
| DP-LOCAL-3 | 提供基于扰动样本的二值频率估计与类别直方图估计。 |
| DP-LOCAL-4 | 本地 DP 扰动与估计能力需通过 REST 与 gRPC 暴露给外部调用方。 |
| DP-HISTO-1 | 提供差分隐私直方图查询，利用互斥划分的联合敏感度为 1。 |
| DP-HISTO-2 | 直方图仅消耗一次 `(ε, δ)` 预算，输出每个分桶的带噪计数。 |
| DP-MEAN-2 | mean 查询支持 `min_count` 参数，防止噪声计数过小时结果发散。 |
| DP-BUDGET-1 | 提供 BudgetAccountant，按 namespace 追踪总 `(ε, δ)` 消耗。 |
| DP-BUDGET-2 | 支持内存与 SQLite 两种预算存储后端。 |
| DP-BUDGET-3 | 预算一旦消耗即不可回退。 |
| DP-BUDGET-4 | 支持按时间窗口自动重置已消耗预算，避免长期运行服务预算永久耗尽。 |
| DP-DATASET-1 | DP 接口以**聚合查询**为单位，不直接对整个数据表（如 CSV）做中心式 DP 加噪；调用方需按列提取字段值作为 `values`，并显式指定查询类型（count/sum/mean）与 clip 参数。 |
| DP-NOISY-1 | 提供 `noisy_count/noisy_sum/noisy_mean/noisy_histogram`，对已由外部引擎（Spark/SQL/DuckDB）聚合好的中间结果直接加噪并扣减预算。 |
| DP-NOISY-2 | noisify 接口必须由调用方提供敏感度（`sensitivity`）或 clip 边界，因 sidecar 不再接触原始数据。 |
| DP-CHUNK-1 | 提供 `chunked_count/chunked_sum/chunked_mean/chunked_histogram`，支持以多个 chunk 分批传入数据，sidecar 增量聚合后只加一次噪、消耗一次预算。 |
| DP-CHUNK-2 | chunked sum/mean 必须显式提供 `clip_lower`/`clip_upper`。 |
| DP-ADAPTER-1 | DP `values` 支持 Python list/tuple、NumPy ndarray、pandas Series/DataFrame、SecretFlow DataFrame / HDataFrame / VDataFrame / FedNdarray。 |
| DP-ADAPTER-2 | 表格型输入通过 `column` 参数指定目标列；HDataFrame 通过 `party` 参数指定参与方；VDataFrame 自动定位包含目标列的 partition。 |
| DP-CLIP-3 | clipping 优先使用 NumPy 向量化 `np.clip`，失败时回退到纯 Python，以提升大规模数据处理效率。 |
| DP-METRIC-1 | 暴露 `privacy_traffic_bytes_total` Counter，按 `method`、`path`、`direction` 记录 REST/gRPC 请求与响应字节数。 |
| DP-ADAPTIVE-1 | 提供 `adaptive_clip` 自适应 DP 二分搜索估计 clip 上界，消耗传入的 epsilon 预算。 |
| DP-ADAPTIVE-2 | `adaptive_clip` 返回的 clip bounds 用于后续聚合调用，后续调用需额外消耗独立预算。 |
| DP-AGG-1 | 提供 `dp_aggregate` 表格级 DP 聚合编排，按列数均分预算。 |
| DP-AGG-2 | `dp_aggregate` 支持 count/sum/mean/histogram 四种聚合类型。 |
| DP-VECTOR-1 | 提供 `vector_sum` 高维向量 L₂ 范数截断 + 各向同性加噪。 |
| DP-VECTOR-2 | 提供 `vector_mean` 通过 noisy_count 归一化得到带噪均值向量。 |
| DP-GROUPBY-1 | 提供 `dp_groupby` Tau-Thresholding 差分隐私 SQL Group-By 过滤。 |
| DP-GROUPBY-2 | `dp_groupby` 预算按 (num_groups × 2) 拆分，总消耗 ≤ epsilon。 |
| DP-ACCUM-1 | 提供 `Accumulator` 分布式无噪流式累加器，支持序列化/反序列化与合并。 |
| DP-ACCUM-2 | 提供 `create_accumulator` / `finalize_dp` 分布式 Worker 无噪累加与 Master 统一加噪。 |
| DP-RDP-1 | 提供 `RDPAccountant` Rényi DP 会计，支持 Gaussian 机制下多阶预算估计。 |

## 4. 接口定义

### 4.1 REST 请求示例

`values` 是**单个字段（列）**的样本值列表，而不是整张数据表。对 CSV 等数据表做 DP 查询时，调用方应先按列提取数据，再针对具体聚合查询调用对应接口。

```json
{
  "values": [1.0, 2.0, 3.0],
  "params": {
    "epsilon": 1.0,
    "delta": 1e-6,
    "mechanism": "gaussian",
    "clip_lower": 0.0,
    "clip_upper": 10.0
  }
}
```

例如，对 `data.csv` 的 `salary` 列求和：

```python
import pandas as pd
import requests

df = pd.read_csv("data.csv")
resp = requests.post(
    "http://127.0.0.1:8079/v1/privacy/dp/sum",
    json={
        "values": df["salary"].tolist(),
        "params": {
            "epsilon": 1.0,
            "delta": 1e-6,
            "mechanism": "gaussian",
            "clip_lower": 0.0,
            "clip_upper": 100000.0,
        },
    },
)
```

### 4.2 Noisify 接口示例

适用于外部计算引擎已完成聚合，仅需 sidecar 注入噪声并消耗预算的场景。

```json
{
  "true_sum": 5000000.0,
  "params": {
    "epsilon": 1.0,
    "delta": 1e-6,
    "mechanism": "gaussian",
    "sensitivity": 100000.0
  }
}
```

`params` 中需提供 `sensitivity`，或同时提供 `clip_lower` 与 `clip_upper`（系统会计算 `sensitivity = clip_upper - clip_lower`）。

### 4.3 Chunked 接口示例

适用于数据量过大无法一次性加载内存的场景。调用方将数据切分为多个 chunk，sidecar 增量聚合后只注入一次噪声。

```json
{
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
}
```

### 4.4 gRPC 字段

`DPRequest` 包含 `epsilon`、`delta`、`mechanism`、`clip_lower`、`clip_upper`。

新增消息：
- `DPNoisyCountRequest` / `DPNoisySumRequest` / `DPNoisyMeanRequest` / `DPNoisyHistogramRequest`
- `DPChunkedCountRequest` / `DPChunkedSumRequest` / `DPChunkedMeanRequest` / `DPChunkedHistogramRequest`
- `DoubleChunk` / `StringChunk`

对应 gRPC 方法：
- `DPNoisyCount` / `DPNoisySum` / `DPNoisyMean` / `DPNoisyHistogram`
- `DPChunkedCount` / `DPChunkedSum` / `DPChunkedMean` / `DPChunkedHistogram`

## 5. 隐私预算设定指南

### 5.1 ε 取值参考

| 场景 | 推荐 ε |
|---|---|
| 高隐私（医疗、金融） | 0.1 ~ 1.0 |
| 通用数据发布 | 1.0 ~ 3.0 |
| 统计聚合/低敏感度 | 3.0 ~ 10.0 |

### 5.2 δ 取值参考

- 一般规则：`δ < 1 / n`，`n` 为数据集大小。
- 常见默认值：`1e-5`、`1e-6`。

### 5.3 预算分配建议

1. 确定总预算 `total_epsilon` 与 `total_delta`。
2. 按查询次数拆分或按业务重要性加权分配。
3. 纯 ε-DP 场景选择 Laplace；需要更紧致组合分析时选择 Gaussian。
4. clip 区间基于业务先验预先设定。

## 6. 验收标准

- [x] count/sum/mean 的 Laplace 与 Gaussian 机制单元测试通过。
- [x] noisify 接口（count/sum/mean/histogram）单元测试与 REST/gRPC 接口测试通过。
- [x] chunked 接口（count/sum/mean/histogram）单元测试与 REST/gRPC 接口测试通过。
- [x] 数据适配器支持 list/tuple/NumPy/pandas/SecretFlow 的测试通过。
- [x] 本地 DP 二值/类别型随机响应与频率估计测试通过。
- [x] clipping 参数校验与敏感度计算测试通过；NumPy 向量化 clip 已覆盖。
- [x] delta 预算正确消耗与超支拒绝测试通过。
- [x] REST/gRPC 接口支持新参数（含 histogram、本地 DP、noisy、chunked）。
- [x] `privacy_traffic_bytes_total` 指标在 REST 中间件与 gRPC 拦截器中已接入并通过测试。
- [x] 高级特性（adaptive_clip / dp_aggregate / vector_sum / vector_mean / dp_groupby / Accumulator / RDPAccountant）测试通过。
- [x] 文档（PRD/design/ops/examples/testing/api_reference）与 `AGENTS.md` 已更新。