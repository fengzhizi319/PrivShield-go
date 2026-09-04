# 差分隐私模块 API 参考

本文档汇总 `PrivShield` 差分隐私（DP）模块的算法设计、API 签名、REST/gRPC 接口定义与使用场景。实现细节与数学证明请参阅 [design.md](./design.md)。


## 1. 算法设计原理

本节概述差分隐私模块的核心算法原理。完整的数学推导、代码实现与证明请参阅 [design.md](./design.md)。

### 1.1 差分隐私定义

对任意两个仅相差一条记录的相邻数据集 $D$ 和 $D'$，随机化机制 $M$ 对任意输出集合 $S$ 满足：

$$(\varepsilon, \delta)\text{-DP}: \quad \Pr[M(D) \in S] \leq e^\varepsilon \cdot \Pr[M(D') \in S] + \delta$$

当 $\delta = 0$ 时为纯 $\varepsilon$-DP。

> **差分隐私是算法（运算、查询、机制）的属性，而非数据本身的属性。** 没有运算，敏感度无从定义，DP 保证失去锚点。

#### 为什么必须绑定运算

差分隐私定义中的 $M$ 是一个**随机化算法**，敏感度 $\Delta f$ 的下标是查询函数 $f$：

$$\Delta f = \max_{D \sim D'} |f(D) - f(D')|$$

- 同一数据集，不同查询有不同敏感度：count 的 $\Delta f = 1$，无截断 sum 的 $\Delta f = \infty$
- **数据本身没有敏感度，是运算赋予了数据敏感度**
- 隐私预算针对**运算序列**消耗，而非数据本身

#### 与其他隐私方法的对比

| 方法 | 层面 | 是否依赖运算 |
|------|------|------------|
| K-匿名 / 数据脱敏 | 数据层面 | 否，通过泛化/替换数据本身实现 |
| **差分隐私** | **算法层面** | **是，必须绑定具体运算** |

#### 为什么不能直接给每条记录加噪声

在中心式 DP 模型下，噪声应加在**查询结果**上，噪声尺度由查询敏感度决定。给每条记录加噪声后，攻击者可多次观测取平均抵消噪声，无法提供 DP 保证。这属于**本地差分隐私**（Local DP）或启发式扰动，与中心式 DP 是完全不同的信任模型。

- **中心式 DP**：只发布带噪声的聚合结果（count/sum/mean/histogram）
- **需要微观数据时**：使用合成数据生成算法（DP-GAN、PrivBayes）
- **本地 DP**：在数据采集端部署本地随机化机制

### 1.2 敏感度

敏感度（Sensitivity）是连接 DP 定义与机制设计的核心桥梁。DP 定义只约束输出分布在相邻数据集上的相似程度，敏感度则量化"一个人的数据变化最多能让查询结果改变多少"。

对查询函数 $f$，其全局 $L_1$ / $L_2$ 敏感度为：

$$\Delta_1 f = \max_{D \sim D'} \|f(D) - f(D')\|_1, \quad \Delta_2 f = \max_{D \sim D'} \|f(D) - f(D')\|_2$$

**作用**：

1. **标定噪声尺度**——隐私机制的"调音旋钮"：
   - Laplace 机制：$M(D) = f(D) + \text{Lap}(\Delta_1 f / \varepsilon)$
   - Gaussian 机制：$M(D) = f(D) + \mathcal{N}(0, \sigma^2)$，$\sigma \approx \Delta_2 f \sqrt{2\ln(1.25/\delta)} / \varepsilon$
2. **把抽象定义转化为可操作参数**：DP 定义给出目标，敏感度给出实现该目标所需的噪声"剂量"
3. **决定隐私与效用的权衡**：给定 $\varepsilon$，误差正比于敏感度；工程上通过裁剪（clipping）控制敏感度

| 类型 | 定义 | 特点 |
|------|------|------|
| 全局敏感度 | 在所有相邻数据集对上取最大值 | 简单、可离线校准；对某些查询过于保守 |
| 局部敏感度 | 固定当前数据集 $D$，在邻域内取最大值 | 通常更小，但直接使用会泄漏 $D$ 的信息 |

> **敏感度度量单个个体的最大影响，差分隐私通过注入与敏感度成比例的噪声来掩盖这种影响——它是定义与实现之间的量化纽带。**

### 1.3 Laplace 机制

Laplace 机制是**唯一**能提供**纯 $\varepsilon$-DP**的连续噪声机制，向查询结果添加 Laplace 分布噪声：

$$M(D) = f(D) + \text{Lap}\left(\frac{\Delta f}{\varepsilon}\right)$$

尺度参数 $b = \Delta f / \varepsilon$，概率密度 $p(x) = \frac{1}{2b}\exp(-|x|/b)$。

**噪声生成（逆变换采样）**：生成 $U \sim \text{Uniform}(0,1)$，令 $V = U - 0.5$，则 $X = -b \cdot \text{sign}(V) \cdot \ln(1 - 2|V|)$。

**示例**：count 查询，$f(D) = 1000$，$\Delta f = 1$，$\varepsilon = 1$，则 $b = 1$，发布结果 $\approx 1000 \pm \text{Lap}(1)$。

**求和示例**：1000 名患者总医疗费用 $f(D) = 5{,}000{,}000$，每人上限 100,000，$\Delta f = 100{,}000$，$\varepsilon = 2$，则 $b = 50{,}000$，相对误差仅约 1%——体现"敏感度小则噪声小"原则。

### 1.4 Gaussian 机制

Gaussian 机制提供 **$(\varepsilon, \delta)$-DP**，向查询结果添加正态分布噪声：

$$M(D) = f(D) + \mathcal{N}(0, \sigma^2), \quad \sigma = \frac{\Delta_2 f \cdot \sqrt{2\ln(1.25/\delta)}}{\varepsilon}$$

本模块默认采用 **Balle & Wang (2018) 解析高斯机制（Analytic Gaussian Mechanism）**，对任意 $\varepsilon > 0$、$\delta > 0$ 数值求解满足 $(\varepsilon, \delta)$-DP 的最小 $\sigma$，噪声通常小于经典公式且不受 $\varepsilon \leq 1$ 限制。实现位于 `engine.privacy.dp.calibrate_analytic_gaussian()`。

**与 Laplace 的核心区别**：

| 特性 | Laplace | Gaussian |
|---|---|---|
| 隐私保证 | 纯 $\varepsilon$-DP（$\delta = 0$） | $(\varepsilon, \delta)$-DP（$\delta > 0$） |
| 敏感度 | L1 敏感度 | L2 敏感度 |
| 噪声分布 | 拉普拉斯分布 | 正态分布 |
| 适用场景 | 简单查询、纯 $\varepsilon$ 保证 | 高维、多次组合、需要紧致界 |

**噪声生成（Box-Muller 变换）**：生成 $U_1, U_2 \sim \text{Uniform}(0,1)$，$Z = \sqrt{-2\ln U_1} \cdot \cos(2\pi U_2)$，$X = \sigma \cdot Z$。

**何时选择 Gaussian**：高维查询输出向量时组合界更紧致；需利用高级组合定理时分析更自然；允许极小失败概率 $\delta$ 的场景。

### 1.5 mean 组合实现与组合定理

**mean 的组合实现**：通过组合 count 与 sum 实现，将 $(\varepsilon, \delta)$ 平分为两份：

$$\text{mean} = \frac{\text{noisy\_sum}(\varepsilon/2, \delta/2)}{\text{noisy\_count}(\varepsilon/2, \delta/2)}$$

为防止噪声计数接近 0 导致均值发散（Cauchy 型长尾），引入 `min_count` 阈值：当估计计数低于阈值时返回 0.0 作为安全 fallback。

**基本组合定理**：若 $k$ 个机制分别满足 $(\varepsilon_i, \delta_i)$-DP，则整体满足 $(\sum\varepsilon_i, \sum\delta_i)$-DP。BudgetAccountant 据此拒绝超支查询。

**高级组合（Advanced Composition）**：当 $k$ 个机制均满足 $(\varepsilon, \delta)$-DP 时，对任意 $\delta' > 0$，整体满足 $(\varepsilon', k\delta + \delta')$-DP，其中 $\varepsilon' \approx \sqrt{2k \cdot \ln(1/\delta')} \cdot \varepsilon + k \cdot \varepsilon^2$。在 $k$ 较大时可将总 $\varepsilon$ 从 $k \cdot \varepsilon$ 降低到约 $\sqrt{k} \cdot \varepsilon$ 量级。

> **当前实现**：BudgetAccountant 支持基本组合（直接相加）。Rényi DP（RDP）会计已通过 `RDPAccountant` 独立实现，可用于 Gaussian 机制下更紧致的预算估计。高级组合定理的自动选择尚未集成到 BudgetAccountant 中。

### 1.6 本地差分隐私（Local DP）

本地 DP 与中心式 DP 的信任模型不同：用户数据在**离开设备前**即被随机化，服务器无法反推个体值。本地 DP 提供更强隐私保证（无需信任服务器），但相同 $\varepsilon$ 下统计效用通常低于中心式 DP。

#### 随机响应（Randomized Response）

**二值**：输入 $b \in \{0, 1\}$，以概率 $p = \frac{e^\varepsilon}{1 + e^\varepsilon}$ 保持原值，否则翻转。满足 $\varepsilon$-LDP。

纠偏估计：$n$ 个用户中报告为 1 的比例 $\hat{f}_{\text{reported}}$，真实频率估计为 $\hat{f} = \frac{\hat{f}_{\text{reported}} - (1-p)}{2p - 1}$。

**k-ary 类别**：输入 $v \in \{1, \dots, k\}$，以概率 $p = \frac{e^\varepsilon}{k - 1 + e^\varepsilon}$ 保持原类别，否则均匀随机选择。纠偏估计：$\hat{f}_j = \frac{\hat{f}_{j,\text{reported}} - q}{p - q}$，$q = \frac{1-p}{k-1}$。

#### 与中心式 DP 的对比

| 维度 | 中心式 DP | 本地 DP |
|---|---|---|
| 信任模型 | 信任数据管理者 | 不信任任何中心方 |
| 噪声位置 | 聚合结果 | 每条用户记录 |
| 典型噪声 | 较小 | 较大 |
| 代表机制 | Laplace / Gaussian | Randomized Response / RAPPOR |
| 适用场景 | 企业内部分析、可信平台 | 浏览器 telemetry、移动设备 |

**使用限制**：本地 DP 噪声远大于中心式 DP，只适合大样本下的频率/分布估计（$n \geq 1000$），不适合需要精确个体值或复杂聚合的场景。

### 1.7 模块设计概览

DP 模块包含以下核心组件：

- **`DPApi`**：中心式 DP 聚合查询（count / sum / mean / histogram）与 noisify / chunked 接口
- **`LocalDPApi`**：本地 DP 随机响应与纠偏估计
- **`BudgetAccountant`**：隐私预算追踪，支持内存 / SQLite 存储与时间窗口重置
- **`RDPAccountant`**：Rényi DP 会计，支持 Gaussian 机制下更紧致的多阶预算估计
- **`Accumulator`**：分布式流式无噪累加器，支持 Map-Reduce 模式下的 DP 聚合
- **`DPApi.dp_aggregate`**：表格级 DP 聚合编排，按列自动拆分预算
- **`DPApi.adaptive_clip`**：DP 自适应二分搜索估计 clip 上界
- **`DPApi.vector_sum` / `vector_mean`**：高维向量 / 梯度 DP 加噪（DP-SGD 基础）
- **`DPApi.dp_groupby`**：Tau-Thresholding 差分隐私 SQL Group-By 过滤
- **`DPApi.create_accumulator` / `finalize_dp`**：分布式 Worker 无噪累加与 Master 统一加噪

REST / gRPC 接口参数与 Python SDK 语义一致。数据适配器支持 pandas / NumPy / SecretFlow 等输入格式。噪声采样使用密码学安全随机数生成器（CSRPNG），测试模式支持 seed 复现。

Laplace / Gaussian 机制的完整数学推导、代码示例与实现细节请参阅 [design.md](./design.md)。

---

## 2. Python SDK

### 2.1 `DPApi`

位置：`engine.privacy.dp.DPApi`

差分隐私计算入口类，封装 Laplace/Gaussian 采样与预算扣减。

#### 构造函数

```python
DPApi(namespace: str = "default")
```

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `namespace` | `str` | 否 | 隐私预算命名空间，默认 `"default"` |

#### `count`

```python
count(
    values: Any,
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
    column: Optional[str] = None,
    party: Optional[str] = None,
) -> float
```

差分隐私计数。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `values` | `Any` | 是 | 输入值，支持 list/tuple/ndarray/Series/DataFrame/SecretFlow 格式 |
| `epsilon` | `float` | 是 | 隐私预算 ε |
| `delta` | `float` | 否 | 隐私预算 δ；Gaussian 机制必须 > 0 |
| `mechanism` | `str` | 否 | `"laplace"` 或 `"gaussian"` |
#### `count`

```python
count(
    values: Any,
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
    column: Optional[str] = None,
    party: Optional[str] = None,
    round_int: bool = False,
    clip_non_negative: bool = True,
    return_details: bool = False,
    confidence_level: float = 0.95,
    user_ids: Optional[Sequence[Any]] = None,
    max_contributions: int = 1,
    discrete: bool = False,
) -> Union[float, DPResult]
```

差分隐私计数。支持 User-Level DP 贡献下采样限定与 Discrete Laplace 离散整数格加噪。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `values` | `Any` | 是 | 输入值，支持 list / ndarray / pandas / pyarrow / scipy.sparse / SecretFlow 格式 |
| `epsilon` | `float` | 是 | 隐私预算 ε |
| `delta` | `float` | 否 | 隐私预算 δ；Gaussian 机制必须 > 0 |
| `mechanism` | `str` | 否 | `"laplace"` 或 `"gaussian"` |
| `column` | `Optional[str]` | 否 | 表格型输入的目标列名 |
| `party` | `Optional[str]` | 否 | SecretFlow HDataFrame 的参与方标识 |
| `round_int` | `bool` | 否 | 是否对输出后处理取整 |
| `clip_non_negative` | `bool` | 否 | 是否截断保障非负（默认 True） |
| `return_details` | `bool` | 否 | 是否返回 `DPResult` 结构体 |
| `confidence_level` | `float` | 否 | 置信区间水平，默认 0.95 |
| `user_ids` | `Optional[Sequence[Any]]` | 否 | 用户 ID 序列，启用 User-Level DP |
| `max_contributions` | `int` | 否 | 单个用户最多保留的记录条数，默认 1 |
| `discrete` | `bool` | 否 | 是否开启离散拉普拉斯机制（输出为整数） |

**返回值**：带噪声的计数值或 `DPResult` 结构。

---

#### `sum`

```python
sum(
    values: Any,
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
    clip_lower: Optional[float] = None,
    clip_upper: Optional[float] = None,
    column: Optional[str] = None,
    party: Optional[str] = None,
    round_int: bool = False,
    clip_non_negative: bool = False,
    return_details: bool = False,
    confidence_level: float = 0.95,
    user_ids: Optional[Sequence[Any]] = None,
    max_contributions: int = 1,
) -> Union[float, DPResult]
```

差分隐私求和。支持 User-Level DP 用户贡献限定与自动敏感度放缩。

---

#### `batch_count` & `batch_sum`

```python
batch_count(data: Any, epsilon: float, delta: float = 0.0, ...) -> Union[np.ndarray, DPResult]
batch_sum(data: Any, epsilon: float, clip_lower: Union[float, Sequence[float]], clip_upper: Union[float, Sequence[float]], ...) -> Union[np.ndarray, DPResult]
```

2D 矩阵与 DataFrame 按列批量加噪接口。

---

#### `dp_aggregate`

```python
dp_aggregate(df: Any, specs: Dict[str, Any], epsilon: float, delta: float = 0.0, mechanism: str = "laplace", return_details: bool = False) -> Dict[str, Any]
```

Table-Level 原位表格聚合接口，预算由组合定理按列切分。

---

#### `adaptive_clip`

```python
adaptive_clip(values: Any, epsilon: float, target_quantile: float = 0.95, num_iterations: int = 15, initial_clip: float = 10.0, ...) -> tuple[float, float]
```

通过差分隐私二分搜索自适应估计数据 [0, clip_upper] 截断上界。

---

#### `vector_sum`

```python
vector_sum(vectors: Any, max_norm: float, epsilon: float, delta: float = 0.0, mechanism: str = "gaussian", return_details: bool = False) -> Union[np.ndarray, DPResult]
```

高维向量 / 梯度 $L_2$ 范数截断与各向同性加噪 (DP-SGD)。

---

#### `dp_groupby`

```python
dp_groupby(df: Any, group_col: str, target_col: str, agg: str, epsilon: float, delta: float = 1e-5, ...) -> Dict[Any, Any]
```

Tau-Thresholding 私有 SQL Group-By 过滤。

---

#### `create_accumulator` & `finalize_dp`

```python
create_accumulator(values: Any, ...) -> Accumulator
finalize_dp(accumulator: Accumulator, aggregation: str, epsilon: float, ...) -> Union[float, Dict[Any, float], DPResult]
```

分布式 Worker 无噪流式累加与 Master 节点统一加噪接口。

---

#### `histogram`

```python
histogram(
    values: Any,
    categories: Sequence[Any],
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
    column: Optional[str] = None,
    party: Optional[str] = None,
) -> Dict[Any, float]
```

差分隐私直方图计数。使用联合敏感度为 1（若一个记录仅能属于一个分桶），仅消耗单次 `(epsilon, delta)` 预算。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `values` | `Any` | 是 | 原始类别特征值，支持多种数据格式 |
| `categories` | `Sequence[Any]` | 是 | 分桶的目标类别集合 |
| `epsilon` | `float` | 是 | 隐私预算 ε |
| `delta` | `float` | 否 | 隐私预算 δ；Gaussian 机制必须 > 0 |
| `mechanism` | `str` | 否 | `"laplace"` 或 `"gaussian"` |
| `column` | `Optional[str]` | 否 | 表格型输入的目标列名 |
| `party` | `Optional[str]` | 否 | SecretFlow HDataFrame 的参与方标识 |

**返回值**：类别名称到带噪计数的字典（已做 `max(0, ...)` 截断）。

**敏感度**：L1 = L2 = 1。

---

#### `noisy_count`

```python
noisy_count(
    true_count: float,
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
) -> float
```

对已经聚合好的计数结果直接注入 DP 噪声。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `true_count` | `float` | 是 | 真实计数值 |
| `epsilon` | `float` | 是 | 隐私预算 ε |
| `delta` | `float` | 否 | 隐私预算 δ；Gaussian 机制必须 > 0 |
| `mechanism` | `str` | 否 | `"laplace"` 或 `"gaussian"` |

**返回值**：带噪声的计数值（已做 `max(0, ...)` 截断）。

**敏感度**：1。

---

#### `noisy_sum`

```python
noisy_sum(
    true_sum: float,
    sensitivity: float,
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
) -> float
```

对已经聚合好的求和结果直接注入 DP 噪声。调用方必须提供敏感度。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `true_sum` | `float` | 是 | 真实求和值 |
| `sensitivity` | `float` | 是 | L1/L2 敏感度（通常为 clip_upper - clip_lower） |
| `epsilon` | `float` | 是 | 隐私预算 ε |
| `delta` | `float` | 否 | 隐私预算 δ；Gaussian 机制必须 > 0 |
| `mechanism` | `str` | 否 | `"laplace"` 或 `"gaussian"` |

**返回值**：带噪声的求和结果。

---

#### `noisy_mean`

```python
noisy_mean(
    true_sum: float,
    true_count: float,
    sensitivity: float,
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
    min_count: float = 5.0,
) -> float
```

对已经聚合好的 sum/count 分别注入 DP 噪声后得到均值。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `true_sum` | `float` | 是 | 真实求和值 |
| `true_count` | `float` | 是 | 真实计数值 |
| `sensitivity` | `float` | 是 | sum 部分的敏感度 |
| `epsilon` | `float` | 是 | 隐私预算 ε |
| `delta` | `float` | 否 | 隐私预算 δ |
| `mechanism` | `str` | 否 | `"laplace"` 或 `"gaussian"` |
| `min_count` | `float` | 否 | 低频计数阈值，默认 `5.0` |

**返回值**：带噪声的均值；当 `noisy_count < min_count` 时返回 `0.0`。

---

#### `noisy_histogram`

```python
noisy_histogram(
    true_counts: Dict[Any, float],
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
) -> Dict[Any, float]
```

对已经聚合好的直方图计数直接注入 DP 噪声。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `true_counts` | `Dict[Any, float]` | 是 | 分桶名到真实计数的字典 |
| `epsilon` | `float` | 是 | 隐私预算 ε |
| `delta` | `float` | 否 | 隐私预算 δ |
| `mechanism` | `str` | 否 | `"laplace"` 或 `"gaussian"` |

**返回值**：分桶名到带噪计数的字典。

---

#### `chunked_count`

```python
chunked_count(
    chunks: Iterable[Any],
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
    column: Optional[str] = None,
    party: Optional[str] = None,
) -> float
```

分块流式差分隐私计数。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `chunks` | `Iterable[Any]` | 是 | 数据块可迭代对象 |
| `epsilon` | `float` | 是 | 隐私预算 ε |
| `delta` | `float` | 否 | 隐私预算 δ |
| `mechanism` | `str` | 否 | `"laplace"` 或 `"gaussian"` |
| `column` | `Optional[str]` | 否 | 表格型 chunk 的目标列名 |
| `party` | `Optional[str]` | 否 | SecretFlow HDataFrame 参与方标识 |

---

#### `chunked_sum`

```python
chunked_sum(
    chunks: Iterable[Any],
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
    clip_lower: float,
    clip_upper: float,
    column: Optional[str] = None,
    party: Optional[str] = None,
) -> float
```

分块流式差分隐私求和。**必须显式提供** `clip_lower` / `clip_upper`。

---

#### `chunked_mean`

```python
chunked_mean(
    chunks: Iterable[Any],
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
    clip_lower: float,
    clip_upper: float,
    min_count: float = 5.0,
    column: Optional[str] = None,
    party: Optional[str] = None,
) -> float
```

分块流式差分隐私均值。**必须显式提供** `clip_lower` / `clip_upper`。

---

#### `chunked_histogram`

```python
chunked_histogram(
    chunks: Iterable[Any],
    categories: Sequence[Any],
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
    column: Optional[str] = None,
    party: Optional[str] = None,
) -> Dict[Any, float]
```

分块流式差分隐私直方图计数。

---

### 2.2 `extract_values`（数据适配器）

位置：`engine.privacy.data_adapters.extract_values`

```python
extract_values(
    data: Any,
    column: Optional[str] = None,
    party: Optional[str] = None,
) -> List[float]
```

将多种数据格式统一转换为 Python 列表，供 DP 模块消费。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `data` | `Any` | 是 | 输入数据 |
| `column` | `Optional[str]` | 否 | 表格型输入的目标列名 |
| `party` | `Optional[str]` | 否 | SecretFlow HDataFrame 参与方标识 |

**支持格式**：

| 类型 | 所需参数 |
|---|---|
| `list` / `tuple` | 无 |
| `np.ndarray` | 无 |
| `pd.Series` | 无 |
| `pd.DataFrame` | `column` |
| `sf.data.DataFrame` | `column` |
| `HDataFrame` | `column`，多 partition 时需提供 `party` |
| `VDataFrame` | `column`（自动定位 partition） |
| `MixDataFrame` | 不支持直接提取 |
| `FedNdarray` | 按 H/V 方式处理 |

---

### 2.3 `LocalDPApi`

位置：`engine.privacy.dp.LocalDPApi`

本地差分隐私入口类，支持单条记录的随机响应扰动与服务端的频率/直方图纠偏估计。

#### 构造函数

```python
LocalDPApi(seed: Optional[int] = None)
```

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `seed` | `Optional[int]` | 否 | 随机数种子，用于可复现测试 |

#### `perturb_binary`

```python
perturb_binary(value: int, epsilon: float) -> int
```

二值随机响应。以概率 $p = \frac{e^\varepsilon}{1 + e^\varepsilon}$ 保留真值，否则翻转为相反值。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `value` | `int` | 是 | `0` 或 `1` |
| `epsilon` | `float` | 是 | 隐私预算 ε，必须 > 0 |

**返回值**：扰动后的 `0` 或 `1`。

---

#### `perturb_binary_batch`

```python
perturb_binary_batch(values: List[int], epsilon: float) -> List[int]
```

对二值列表逐条执行随机响应。

---

#### `perturb_categorical`

```python
perturb_categorical(value: Any, categories: List[Any], epsilon: float) -> Any
```

类别型随机响应。以概率 $p = \frac{e^\varepsilon}{|C| - 1 + e^\varepsilon}$ 保留真值，否则从其余类别均匀随机选择。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `value` | `Any` | 是 | 真实类别值，必须属于 `categories` |
| `categories` | `List[Any]` | 是 | 所有可能类别 |
| `epsilon` | `float` | 是 | 隐私预算 ε，必须 > 0 |

**返回值**：扰动后的类别值。

---

#### `perturb_categorical_batch`

```python
perturb_categorical_batch(values: List[Any], categories: List[Any], epsilon: float) -> List[Any]
```

对类别列表逐条执行随机响应。

---

#### `estimate_binary_frequency`

```python
estimate_binary_frequency(reported_values: List[int], epsilon: float) -> float
```

从扰动后的二值报告中纠偏估计真实 `1` 的频率。

**返回值**：`[0, 1]` 区间内的频率估计值。

---

#### `estimate_categorical_histogram`

```python
estimate_categorical_histogram(
    reported_values: List[Any],
    categories: List[Any],
    epsilon: float,
) -> Dict[Any, float]
```

从扰动后的类别报告中纠偏估计真实分布。

**返回值**：每个类别的频率字典，频率之和近似为 1。

---

### 2.4 `BudgetAccountant` & `BudgetRegistry`

位置：`engine.privacy.budget.BudgetAccountant`

隐私预算账户，追踪命名空间级别的累计 `(ε, δ)` 消耗。推荐通过 `get_budget()` 或 `default_registry.get_or_create()` 创建与获取。

#### 推荐获取方式

```python
from engine.privacy.budget import get_budget, default_registry

# 方式 1：模块级便捷函数（推荐）
accountant = get_budget("hr_data", epsilon_total=10.0, delta_total=1e-4)

# 方式 2：使用全局注册表
accountant = default_registry.get_or_create("hr_data", epsilon_total=10.0, delta_total=1e-4)
```

> **注意**：直接构造 `BudgetAccountant("ns")` 已关闭（抛出 `TypeError`）。请使用 `default_registry.get_or_create("ns")` 或便捷函数 `get_budget("ns")`。

#### 主要属性与字符串表示

| 属性 | 类型 | 说明 |
|---|---|---|
| `epsilon_total` | `float` | epsilon 总预算 |
| `delta_total` | `float` | delta 总预算 |
| `epsilon_spent` | `float` | 已消耗 epsilon |
| `delta_spent` | `float` | 已消耗 delta |

`__repr__()` 提供清晰的预算状态表示：
```python
repr(accountant)
# -> "BudgetAccountant(namespace='hr_data', epsilon=7.5000/10.0, delta=9.00e-05/0.0001)"
```

#### 主要方法

| 方法 | 签名 | 说明 |
|---|---|---|
| `spend` | `spend(epsilon: float, delta: float = 0.0)` | 消耗预算；超支时抛出 `PrivacyBudgetExhaustedError`（兼容别名 `PrivacyBudgetExhausted`） |
| `remaining` | `remaining() -> dict[str, float]` | 返回剩余 `{"epsilon": float, "delta": float}` |

---

### 2.5 数据表 / CSV 场景下的正确使用方式

实际业务中常见的输入是一张数据表（如 CSV、数据库表、Pandas DataFrame），其中包含多个字段、多条记录。需要明确的是：**`DPApi` 的 `values` 参数不是整张表，而是单个聚合查询所针对的某一列数据。**

#### 为什么不能直接传入整张表

中心式 DP 的噪声尺度由**具体查询的敏感度**决定，而敏感度取决于查询函数 $f$。整张表本身没有唯一的敏感度：

| 查询 | 输入 | 敏感度 |
|---|---|---|
| 员工人数 | `employee_id` 列 | 1 |
| 总工资 | `salary` 列（clip 到 $[0, C]$） | $C$ |
| 平均年龄 | `age` 列（clip 到 $[0, 150]$） | 通过 count + sum 组合 |
| 部门人数分布 | `department` 列 | 每个桶 1 |

如果允许一次性传入整张表而不指定查询，系统无法确定该为哪种查询加多少噪声，也就无法给出严格的 DP 保证。

#### CSV / 数据表的正确使用流程

1. 按业务需求确定要发布的聚合查询（count / sum / mean / histogram）。
2. 从数据表中提取该查询对应的**单列**作为 `values`。
3. 根据该列的业务取值范围设定 `clip_lower` / `clip_upper`（sum / mean 必填）。
4. 调用对应接口，消耗对应命名空间的隐私预算。

**示例**：对 `data.csv` 的 `salary` 列做差分隐私求和。

```python
import pandas as pd
from engine.privacy.dp import DPApi

df = pd.read_csv("data.csv")
api = DPApi(namespace="hr_dataset")

# 方式 1：手动提取单列
result = api.sum(
    values=df["salary"].tolist(),
    epsilon=1.0,
    delta=1e-6,
    mechanism="gaussian",
    clip_lower=0.0,
    clip_upper=100000.0,
)

# 方式 2：直接传入 DataFrame，使用 column 参数指定目标列
result = api.sum(
    values=df,
    column="salary",
    epsilon=1.0,
    delta=1e-6,
    mechanism="gaussian",
    clip_lower=0.0,
    clip_upper=100000.0,
)
```

SecretFlow 联邦 DataFrame 同样支持直接传入（需安装 secretflow）：

```python
from engine.privacy.dp import DPApi

api = DPApi(namespace="hr_dataset")

# VDataFrame：列分布在不同参与方，自动定位包含 salary 的 partition
result = api.sum(
    values=vdf,
    column="salary",
    epsilon=1.0,
    delta=1e-6,
    mechanism="gaussian",
    clip_lower=0.0,
    clip_upper=100000.0,
)

# HDataFrame：样本水平分割，需指定参与方
result = api.sum(
    values=hdf,
    column="salary",
    party="alice",
    epsilon=1.0,
    delta=1e-6,
    mechanism="gaussian",
    clip_lower=0.0,
    clip_upper=100000.0,
)
```

REST 侧同理：`values` 字段只包含目标列的样本值；如需指定列/参与方，可在 `params` 中传入 `column` 和 `party`。

#### 多字段 / 多列 / 分组分析怎么办

如果需要同时分析多个字段，应该拆分为多个独立的 DP 查询，并分别消耗隐私预算。例如：

- 先对 `salary` 列做 sum（消耗 $\varepsilon_1$）
- 再对 `age` 列做 mean（消耗 $\varepsilon_2$）
- 再对 `department` 列做 histogram（消耗 $\varepsilon_3$）

总隐私消耗按组合定理累加：$\varepsilon_{\text{total}} = \varepsilon_1 + \varepsilon_2 + \varepsilon_3$。

**分组聚合**（如按部门求平均工资）属于更复杂的查询，需要额外设计：

- 每个分组的计数值是否足够大（避免 noisy_count 接近 0 导致结果爆炸）
- 是否对分组数量做限制（防止输出维度泄漏信息）
- 如何为每个分组分配预算

这些超出了 `DPApi.count/sum/mean` 的职责范围，应作为独立功能（如 `dp_histogram`、`dp_groupby`）另行设计。

#### 如果需要发布"脱敏后的整张表"

这不是中心式 DP 聚合查询能解决的问题。应使用专门的**差分隐私合成数据生成**算法，例如：

- **DP-GAN**：在训练生成模型时注入 DP 噪声，生成统计特征相似的合成记录。
- **PrivBayes**：基于贝叶斯网络建模字段间关系，逐维度加噪声后采样合成数据。

这些算法本身也是满足 DP 的"运算"，需要独立的模块、接口和隐私预算管理，不能通过扩展 `DPApi` 的 `values` 参数来实现。

#### 一句话总结

> **对数据表做差分隐私，本质是对数据表上的某个具体聚合查询做差分隐私。`DPApi` 一次只处理一个查询对应的一列数据；多列、多查询、分组聚合、合成数据发布都是更高层的能力，需要单独设计，不能混为一谈。**

---

## 3. REST API

### POST `/v1/privacy/dp/count`

请求体：

```json
{
  "values": [1, 0, 1, 1, 0],
  "params": {
    "epsilon": 1.0,
    "delta": 0.0,
    "mechanism": "laplace"
  }
}
```

响应体：

```json
{
  "result": 3.142
}
```

### POST `/v1/privacy/dp/sum`

请求体：

```json
{
  "values": [1200, 5800, 300, 99999, 15000],
  "params": {
    "epsilon": 1.0,
    "delta": 1e-6,
    "mechanism": "gaussian",
    "clip_lower": 0.0,
    "clip_upper": 100000.0
  }
}
```

响应体：

```json
{
  "result": 122345.678
}
```

### POST `/v1/privacy/dp/mean`

请求体：

```json
{
  "values": [25, 34, 45, 52, 29, 61],
  "params": {
    "epsilon": 2.0,
    "delta": 1e-6,
    "mechanism": "gaussian",
    "clip_lower": 0.0,
    "clip_upper": 120.0
  }
}
```

响应体：

```json
{
  "result": 41.234
}
```

### POST `/v1/privacy/dp/histogram`

请求体：

```json
{
  "values": ["A", "B", "A", "C"],
  "categories": ["A", "B", "C", "D"],
  "params": {
    "epsilon": 10.0,
    "mechanism": "laplace"
  }
}
```

响应体：

```json
{
  "result": {
    "A": 2.14,
    "B": 0.95,
    "C": 1.05,
    "D": 0.0
  }
}
```

### POST `/v1/privacy/dp/noisy_count`

对已由外部引擎聚合好的计数结果加噪。

请求体：
```json
{
  "true_count": 1000.0,
  "params": {
    "epsilon": 1.0,
    "mechanism": "laplace"
  }
}
```

响应体：
```json
{
  "result": 1001.5
}
```

### POST `/v1/privacy/dp/noisy_sum`

对已由外部引擎聚合好的求和结果加噪。`params` 中需提供 `sensitivity`，或同时提供 `clip_lower` 与 `clip_upper`。

请求体：
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

### POST `/v1/privacy/dp/noisy_mean`

对已由外部引擎聚合好的 sum/count 加噪后得到均值。

请求体：
```json
{
  "true_sum": 5000000.0,
  "true_count": 1000.0,
  "params": {
    "epsilon": 2.0,
    "delta": 1e-6,
    "mechanism": "gaussian",
    "sensitivity": 100000.0,
    "min_count": 5.0
  }
}
```

### POST `/v1/privacy/dp/noisy_histogram`

对已由外部引擎聚合好的直方图计数加噪。

请求体：
```json
{
  "true_counts": {
    "A": 100.0,
    "B": 80.0,
    "C": 50.0
  },
  "params": {
    "epsilon": 1.0,
    "mechanism": "laplace"
  }
}
```

### POST `/v1/privacy/dp/chunked_count`

分块流式差分隐私计数。

请求体：
```json
{
  "chunks": [
    [1, 0, 1],
    [1, 0, 1, 1]
  ],
  "params": {
    "epsilon": 1.0,
    "mechanism": "laplace"
  }
}
```

### POST `/v1/privacy/dp/chunked_sum`

分块流式差分隐私求和。`params` 中必须提供 `clip_lower` / `clip_upper`。

请求体：
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

### POST `/v1/privacy/dp/chunked_mean`

分块流式差分隐私均值。

请求体：
```json
{
  "chunks": [
    [1.0, 2.0, 3.0],
    [4.0, 5.0, 6.0]
  ],
  "params": {
    "epsilon": 2.0,
    "delta": 1e-6,
    "mechanism": "gaussian",
    "clip_lower": 0.0,
    "clip_upper": 10.0,
    "min_count": 2.0
  }
}
```

### POST `/v1/privacy/dp/chunked_histogram`

分块流式差分隐私直方图计数。

请求体：
```json
{
  "chunks": [
    ["A", "B", "A"],
    ["C", "A", "B"]
  ],
  "categories": ["A", "B", "C", "D"],
  "params": {
    "epsilon": 10.0,
    "mechanism": "laplace"
  }
}
```

### POST `/v1/privacy/ldp/perturb/binary`

二值本地差分隐私扰动。

请求体：
```json
{
  "values": [1, 0, 1, 1],
  "epsilon": 10.0
}
```

响应体：
```json
{
  "results": [1, 0, 1, 1]
}
```

### POST `/v1/privacy/ldp/perturb/categorical`

类别型本地差分隐私扰动。

请求体：
```json
{
  "values": ["A", "B", "A"],
  "categories": ["A", "B", "C"],
  "epsilon": 10.0
}
```

响应体：
```json
{
  "results": ["A", "B", "C"]
}
```

### POST `/v1/privacy/ldp/estimate/binary`

二值扰动样本频率估计。

请求体：
```json
{
  "reported_values": [1, 1, 0, 1],
  "epsilon": 5.0
}
```

响应体：
```json
{
  "estimated_frequency": 0.75
}
```

### POST `/v1/privacy/ldp/estimate/categorical`

类别型扰动样本直方图估计。

请求体：
```json
{
  "reported_values": ["A", "B", "C", "A"],
  "categories": ["A", "B", "C"],
  "epsilon": 5.0
}
```

响应体：
```json
{
  "estimated_histogram": {
    "A": 0.5,
    "B": 0.25,
    "C": 0.25
  }
}
```

---


## 4. gRPC API

### 方法列表

| 方法 | 请求 | 响应 | 说明 |
|---|---|---|---|
| `DPCount` | `DPRequest` | `DPResponse` | 差分隐私计数 |
| `DPSum` | `DPRequest` | `DPResponse` | 差分隐私求和 |
| `DPMean` | `DPRequest` | `DPResponse` | 差分隐私均值 |
| `DPHistogram` | `DPHistogramRequest` | `DPHistogramResponse` | 差分隐私直方图 |
| `DPNoisyCount` | `DPNoisyCountRequest` | `DPResponse` | 对已聚合计数加噪 |
| `DPNoisySum` | `DPNoisySumRequest` | `DPResponse` | 对已聚合求和加噪 |
| `DPNoisyMean` | `DPNoisyMeanRequest` | `DPResponse` | 对已聚合 sum/count 加噪得均值 |
| `DPNoisyHistogram` | `DPNoisyHistogramRequest` | `DPHistogramResponse` | 对已聚合直方图加噪 |
| `DPChunkedCount` | `DPChunkedCountRequest` | `DPResponse` | 分块流式计数 |
| `DPChunkedSum` | `DPChunkedSumRequest` | `DPResponse` | 分块流式求和 |
| `DPChunkedMean` | `DPChunkedMeanRequest` | `DPResponse` | 分块流式均值 |
| `DPChunkedHistogram` | `DPChunkedHistogramRequest` | `DPHistogramResponse` | 分块流式直方图 |
| `PerturbBinaryBatch` | `PerturbBinaryBatchRequest` | `PerturbBinaryBatchResponse` | 二值本地 DP 扰动 |
| `PerturbCategoricalBatch` | `PerturbCategoricalBatchRequest` | `PerturbCategoricalBatchResponse` | 类别型本地 DP 扰动 |
| `EstimateBinaryFrequency` | `EstimateBinaryFrequencyRequest` | `EstimateBinaryFrequencyResponse` | 二值扰动样本频率估计 |
| `EstimateCategoricalHistogram` | `EstimateCategoricalHistogramRequest` | `EstimateCategoricalHistogramResponse` | 类别型扰动直方图估计 |

### `DPRequest` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `values` | `repeated double` | 输入值列表 |
| `params` | `map<string, string>` | 参数映射，包含 `epsilon`、`delta`、`mechanism`、`clip_lower`、`clip_upper` |

### `DPResponse` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `result` | `double` | 带噪声的查询结果 |

### `DPHistogramRequest` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `values` | `repeated string` | 输入值列表 |
| `categories` | `repeated string` | 目标类别集合 |
| `epsilon` | `double` | 隐私预算 ε |
| `mechanism` | `string` | `"laplace"` 或 `"gaussian"` |
| `delta` | `double` | 隐私预算 δ |

### `DPHistogramResponse` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `result` | `map<string, double>` | 直方图分类计数结果 |

### Noisify 相关 Message 字段

#### `DPNoisyCountRequest`
*   `true_count` (`double`): 真实计数值。
*   `epsilon` (`double`): 隐私预算 ε。
*   `mechanism` (`string`): `"laplace"` 或 `"gaussian"`。
*   `delta` (`double`): 隐私预算 δ。

#### `DPNoisySumRequest`
*   `true_sum` (`double`): 真实求和值。
*   `epsilon` (`double`): 隐私预算 ε。
*   `mechanism` (`string`): `"laplace"` 或 `"gaussian"`。
*   `delta` (`double`): 隐私预算 δ。
*   `sensitivity` (`double`): 敏感度；若为 0 则尝试通过 `clip_lower`/`clip_upper` 推导。
*   `clip_lower` (`double`): 截断下界。
*   `clip_upper` (`double`): 截断上界。

#### `DPNoisyMeanRequest`
*   `true_sum` (`double`): 真实求和值。
*   `true_count` (`double`): 真实计数值。
*   `epsilon` (`double`): 隐私预算 ε。
*   `mechanism` (`string`): `"laplace"` 或 `"gaussian"`。
*   `delta` (`double`): 隐私预算 δ。
*   `sensitivity` (`double`): sum 部分敏感度。
*   `clip_lower` (`double`): 截断下界。
*   `clip_upper` (`double`): 截断上界。
*   `min_count` (`double`): 低频计数阈值。

#### `DPNoisyHistogramRequest`
*   `true_counts` (`map<string, double>`): 分桶名到真实计数的映射。
*   `epsilon` (`double`): 隐私预算 ε。
*   `mechanism` (`string`): `"laplace"` 或 `"gaussian"`。
*   `delta` (`double`): 隐私预算 δ。

### Chunked 相关 Message 字段

#### `DoubleChunk`
*   `values` (`repeated double`): 数值型数据块。

#### `StringChunk`
*   `values` (`repeated string`): 类别型数据块。

#### `DPChunkedCountRequest`
*   `chunks` (`repeated DoubleChunk`): 数值型数据块列表。
*   `epsilon` (`double`): 隐私预算 ε。
*   `mechanism` (`string`): `"laplace"` 或 `"gaussian"`。
*   `delta` (`double`): 隐私预算 δ。

#### `DPChunkedSumRequest`
*   `chunks` (`repeated DoubleChunk`): 数值型数据块列表。
*   `epsilon` (`double`): 隐私预算 ε。
*   `mechanism` (`string`): `"laplace"` 或 `"gaussian"`。
*   `delta` (`double`): 隐私预算 δ。
*   `clip_lower` (`double`): 截断下界。
*   `clip_upper` (`double`): 截断上界。

#### `DPChunkedMeanRequest`
*   字段同 `DPChunkedSumRequest`，额外包含：
*   `min_count` (`double`): 低频计数阈值。

#### `DPChunkedHistogramRequest`
*   `chunks` (`repeated StringChunk`): 类别型数据块列表。
*   `categories` (`repeated string`): 目标类别集合。
*   `epsilon` (`double`): 隐私预算 ε。
*   `mechanism` (`string`): `"laplace"` 或 `"gaussian"`。
*   `delta` (`double`): 隐私预算 δ。

### LDP 相关 Message 字段

#### `PerturbBinaryBatchRequest`
*   `values` (`repeated int32`): 待扰动二值列表。
*   `epsilon` (`double`): 本地隐私预算。

#### `PerturbBinaryBatchResponse`
*   `results` (`repeated int32`): 扰动后二值结果列表。

#### `PerturbCategoricalBatchRequest`
*   `values` (`repeated string`): 待扰动类别列表。
*   `categories` (`repeated string`): 所有可用类别集合。
*   `epsilon` (`double`): 本地隐私预算。

#### `PerturbCategoricalBatchResponse`
*   `results` (`repeated string`): 扰动后类别结果列表。

#### `EstimateBinaryFrequencyRequest`
*   `reported_values` (`repeated int32`): 已扰动的报告样本列表。
*   `epsilon` (`double`): 扰动时使用的本地隐私预算。

#### `EstimateBinaryFrequencyResponse`
*   `estimated_frequency` (`double`): 估计的真实频率（0~1）。

#### `EstimateCategoricalHistogramRequest`
*   `reported_values` (`repeated string`): 已扰动的报告样本列表。
*   `categories` (`repeated string`): 所有可用类别集合。
*   `epsilon` (`double`): 扰动时使用的本地隐私预算。

#### `EstimateCategoricalHistogramResponse`
*   `estimated_histogram` (`map<string, double>`): 各类别的纠偏估计频率。


---

## 5. 异常与错误码

| 异常/错误 | 触发条件 | HTTP 状态码 | gRPC 状态码 |
|---|---|---|---|
| `ValueError: clip_lower and clip_upper are required for Gaussian mechanism` | Gaussian sum/mean 缺少 clip | 400 | `INVALID_ARGUMENT` |
| `ValueError: chunked_sum requires explicit clip_lower and clip_upper` | chunked sum/mean 缺少 clip | 400 | `INVALID_ARGUMENT` |
| `ValueError: dp_noisy_sum requires 'sensitivity' or both 'clip_lower' and 'clip_upper'` | noisify sum/mean 缺少敏感度 | 400 | `INVALID_ARGUMENT` |
| `ValueError: delta must be positive for Gaussian mechanism` | Gaussian 请求 delta ≤ 0 | 400 | `INVALID_ARGUMENT` |
| `ValueError: sensitivity must be non-negative` | noisify sum/mean 敏感度为负 | 400 | `INVALID_ARGUMENT` |
| `ValueError: column must be specified when input is a pandas DataFrame` | DataFrame 未指定 `column` | 400 | `INVALID_ARGUMENT` |
| `TypeError: Unsupported data type for DP values` | 不支持的 `values` 输入类型 | 400 | `INVALID_ARGUMENT` |
| `PrivacyBudgetExhaustedError`（或别名 `PrivacyBudgetExhausted`） | 累计预算超过命名空间上限 | 429 | `RESOURCE_EXHAUSTED` |
| `ValueError: mechanism must be 'laplace' or 'gaussian'` | mechanism 参数非法 | 400 | `INVALID_ARGUMENT` |

---

## 6. 使用场景与参数建议

### 6.1 典型应用场景

#### 场景 1：医疗健康数据分析

**背景**：医院需要对患者数据进行统计分析，同时保护患者隐私。

**推荐配置**：
- **总预算**：`epsilon_total = 1.0~2.0`, `delta_total = 1e-6`
- **查询类型**：count（患者人数）、sum（医疗费用）、mean（平均年龄）
- **机制选择**：Gaussian（适合多查询组合）
- **Clip 区间**：根据业务先验设置
  - 年龄：`clip_lower=0, clip_upper=120`
  - 医疗费用：`clip_lower=0, clip_upper=100000`

**示例**：
```python
from engine.privacy.dp import DPApi

dp = DPApi(namespace="hospital_patients_2024")

# 查询1：高血压患者人数
count_result = dp.count(
    values=[1 if age > 60 else 0 for age in ages],
    epsilon=0.3,
    delta=1e-7,
    mechanism="gaussian"
)

# 查询2：总医疗费用
sum_result = dp.sum(
    values=charges,
    epsilon=0.4,
    delta=1e-7,
    mechanism="gaussian",
    clip_lower=0.0,
    clip_upper=100000.0
)

# 查询3：平均住院天数
mean_result = dp.mean(
    values=stay_days,
    epsilon=0.3,
    delta=1e-6,
    mechanism="gaussian",
    clip_lower=1.0,
    clip_upper=30.0,
    min_count=5.0
)
```

**注意事项**：
- 医疗数据高度敏感，总 ε 应控制在较低水平（≤2.0）
- 每次查询分配合理预算，避免单次消耗过多
- 使用 Gaussian 机制便于后续高级组合分析

---

#### 场景 2：金融风控统计

**背景**：银行需要发布客户交易统计数据用于风控模型训练。

**推荐配置**：
- **总预算**：`epsilon_total = 2.0~4.0`, `delta_total = 1e-6`
- **查询类型**：sum（交易总额）、histogram（交易金额分布）
- **机制选择**：Gaussian（高维直方图更适合）
- **Clip 区间**：
  - 单笔交易：`clip_lower=0, clip_upper=500000`
  - 月收入：`clip_lower=0, clip_upper=1000000`

**示例**：
```python
from engine.privacy.dp import DPApi

dp = DPApi(namespace="bank_transactions_q1")

# 查询1：信用卡消费总额
sum_result = dp.sum(
    values=credit_card_charges,
    epsilon=1.0,
    delta=5e-7,
    mechanism="gaussian",
    clip_lower=0.0,
    clip_upper=500000.0
)

# 查询2：交易金额分布直方图
bins = ["0-1k", "1k-5k", "5k-10k", "10k-50k", "50k+"]
histogram_result = dp.histogram(
    values=transaction_amounts,
    categories=bins,
    epsilon=1.0,
    delta=5e-7,
    mechanism="gaussian"
)
```

**注意事项**：
- 金融数据涉及合规要求（如 GDPR、个人信息保护法）
- 直方图联合敏感度为 1，可一次性发布多个分桶
- 考虑使用时间窗口重置预算（`PRIVACY_BUDGET_WINDOW_SECONDS=86400`）

---

#### 场景 3：用户行为分析（互联网产品）

**背景**：互联网公司需要分析用户点击、浏览等行为数据。

**推荐配置**：
- **总预算**：`epsilon_total = 4.0~8.0`, `delta_total = 1e-5`
- **查询类型**：count（活跃用户数）、histogram（功能使用分布）
- **机制选择**：Laplace（简单查询优先纯 ε-DP）
- **Clip 区间**：通常不需要（count/histogram 敏感度天然为 1）

**示例**：
```python
from engine.privacy.dp import DPApi

dp = DPApi(namespace="app_daily_active_users")

# 查询1：日活跃用户数
dau_count = dp.count(
    values=user_ids,
    epsilon=1.0,
    mechanism="laplace"
)

# 查询2：功能模块使用分布
features = ["search", "chat", "payment", "settings"]
feature_usage = dp.histogram(
    values=user_actions,
    categories=features,
    epsilon=2.0,
    mechanism="laplace"
)
```

**注意事项**：
- 用户行为数据敏感度相对较低，可使用较高 ε
- Laplace 机制提供纯 ε-DP，审计更简单
- 如需长期监控，建议按天/周划分 namespace

---

#### 场景 4：联邦学习中的梯度扰动

**背景**：在分布式训练中，对客户端上传的梯度添加噪声以满足差分隐私。

**推荐配置**：
- **总预算**：`epsilon_total = 5.0~10.0`, `delta_total = 1e-5`
- **查询类型**：noisy_sum（聚合梯度）
- **机制选择**：Gaussian（适合迭代算法）
- **Clip 区间**：梯度裁剪范数上限（如 `clip_upper=1.0`）

**示例**：
```python
from engine.privacy.dp import DPApi

dp = DPApi(namespace="federated_training_round_1")

# 假设外部聚合器已计算出梯度总和
true_gradient_sum = 12.5
gradient_norm_clip = 1.0

noisy_gradient = dp.noisy_sum(
    true_sum=true_gradient_sum,
    sensitivity=gradient_norm_clip,
    epsilon=0.5,
    delta=1e-6,
    mechanism="gaussian"
)
```

**注意事项**：
- 每轮训练消耗独立预算，需预留足够总预算
- 梯度裁剪是控制敏感度的关键步骤
- 推荐使用 RDP（Rényi Differential Privacy）进行更紧致的预算追踪（当前未实现）

---

#### 场景 5：本地差分隐私遥测收集

**背景**：浏览器/移动设备在用户端完成数据扰动后上报，服务器无法反推个体值。

**推荐配置**：
- **本地 ε**：`epsilon = 5.0~10.0`（本地 DP 需要更高 ε 以保证效用）
- **查询类型**：perturb_binary / perturb_categorical + estimate
- **机制选择**：随机响应（Randomized Response）
- **样本量要求**：至少数千条记录才能获得可靠估计

**示例**：
```python
from engine.privacy.dp import LocalDPApi

# === 客户端侧（用户设备）===
local_api = LocalDPApi()

# 用户真实偏好：是否启用某功能
user_preference = 1  # 或 0

# 本地扰动后上报
reported_value = local_api.perturb_binary(user_preference, epsilon=8.0)

# === 服务器侧（聚合分析）===
# 收集所有用户的扰动报告
reported_values = [1, 0, 1, 1, 0, ...]  # 来自大量用户

# 纠偏估计真实比例
estimated_ratio = local_api.estimate_binary_frequency(
    reported_values=reported_values,
    epsilon=8.0
)
print(f"估计启用率: {estimated_ratio:.2%}")
```

**注意事项**：
- 本地 DP 噪声远大于中心式 DP，需要大样本（n ≥ 1000）
- 适用于群体趋势分析，不适用于精确个体查询
- ε 过低会导致估计方差过大，结果不可用

---

#### 场景 6：大数据流式处理（Spark/Flink）

**背景**：海量数据在分布式引擎中预聚合，sidecar 仅负责注入噪声。

**推荐配置**：
- **总预算**：根据查询频率动态分配
- **查询类型**：noisy_count / noisy_sum / noisy_mean
- **机制选择**：Gaussian（适合批处理）
- **工作流**：Spark SQL 聚合 → sidecar 加噪 → 发布结果

**示例**：
```python
# === Spark 侧（Python/Scala）===
# df = spark.read.parquet("hdfs://...")
# aggregated = df.groupBy("department").agg(
#     count("employee_id").alias("true_count"),
#     sum("salary").alias("true_sum")
# )
# true_count = aggregated.collect()[0]["true_count"]
# true_sum = aggregated.collect()[0]["true_sum"]

# === Sidecar 侧（Python）===
from engine.privacy.dp import DPApi

dp = DPApi(namespace="hr_monthly_report")

# 对已聚合结果加噪
noisy_count = dp.noisy_count(
    true_count=1500.0,
    epsilon=0.5,
    mechanism="laplace"
)

noisy_sum = dp.noisy_sum(
    true_sum=75000000.0,
    sensitivity=100000.0,  # clip_upper - clip_lower
    epsilon=0.5,
    delta=1e-6,
    mechanism="gaussian"
)

noisy_mean = noisy_sum / max(noisy_count, 1)
print(f"带噪声平均工资: {noisy_mean:.2f}")
```

**注意事项**：
- Noisify 接口不接触原始数据，性能开销极低
- 调用方必须准确提供敏感度（基于 clip 区间）
- 适合亿级数据量的生产环境

---

#### 场景 7：内存受限的流式聚合

**背景**：数据量超过单机内存，需要分批读取并增量聚合。

**推荐配置**：
- **总预算**：单次查询消耗，无需拆分
- **查询类型**：chunked_count / chunked_sum / chunked_mean
- **机制选择**：根据敏感度选择 Laplace/Gaussian
- **Chunk 大小**：根据可用内存调整（如每批 10000 条）

**示例**：
```python
from engine.privacy.dp import DPApi
import pandas as pd

dp = DPApi(namespace="streaming_logs")

def read_chunks(file_path, chunk_size=10000):
    """生成器：分批读取 CSV 文件"""
    for chunk in pd.read_csv(file_path, chunksize=chunk_size):
        yield chunk["response_time"].tolist()

# 流式求和（只消耗一次预算）
result = dp.chunked_sum(
    chunks=read_chunks("server_logs.csv"),
    epsilon=1.0,
    delta=1e-6,
    mechanism="gaussian",
    clip_lower=0.0,
    clip_upper=5000.0  # 响应时间上限 5 秒
)
print(f"带噪声总响应时间: {result:.2f}ms")
```

**注意事项**：
- Chunked 接口内部完成增量聚合，最终只注入一次噪声
- 必须提供全局 clip 区间（所有 chunk 共用）
- 适合中等规模数据（能放入单台 sidecar 内存分批处理）

---

### 6.2 参数选择指南

#### Epsilon (ε) 选择

| 数据敏感度 | 推荐 ε 范围 | 适用场景 |
|-----------|------------|---------|
| 极高敏感 | 0.1 ~ 0.5 | 基因数据、心理健康记录 |
| 高敏感 | 0.5 ~ 2.0 | 医疗诊断、金融征信 |
| 中等敏感 | 2.0 ~ 5.0 | 用户行为、位置轨迹 |
| 低敏感 | 5.0 ~ 10.0 | 公开统计、匿名调查 |

**原则**：
- ε 越小，隐私保护越强，但噪声越大
- 总 ε 应在数据集级别严格控制（通过 BudgetAccountant）
- 单次查询 ε 可根据查询重要性动态分配

---

#### Delta (δ) 选择

| 数据集大小 n | 推荐 δ | 说明 |
|-------------|--------|------|
| n < 1,000 | 1e-8 ~ 1e-7 | 小数据集需更严格 |
| 1,000 ≤ n < 100,000 | 1e-7 ~ 1e-6 | 典型企业级数据 |
| n ≥ 100,000 | 1e-6 ~ 1e-5 | 大规模用户数据 |

**原则**：
- δ 应远小于 1/n（理想情况 δ << 1/n²）
- Gaussian 机制必须提供 δ > 0
- δ 表示隐私保证失败的概率（可理解为"例外情况"）

---

#### Mechanism 选择

| 特性 | Laplace | Gaussian |
|-----|---------|----------|
| 隐私保证 | 纯 ε-DP（δ=0） | (ε, δ)-DP |
| 噪声尺度 | b = Δf/ε | σ = Δ₂f·√(2ln(1.25/δ))/ε |
| 适用查询 | 低维、简单聚合 | 高维、多次组合 |
| 审计复杂度 | 低（直接相加） | 中（需追踪 δ） |
| 推荐场景 | count、histogram | sum、mean、梯度扰动 |

**决策流程**：
1. 是否需要纯 ε-DP？→ 是：选 Laplace
2. 是否涉及高维输出（如直方图多分桶）？→ 是：选 Gaussian
3. 是否需要高级组合定理优化预算？→ 是：选 Gaussian
4. 默认选择：简单查询用 Laplace，复杂场景用 Gaussian

---

#### Clip 区间选择

**为什么需要 Clip**：
- 限制单条记录对查询结果的最大影响（控制敏感度）
- 避免极端值导致噪声过大

**选择策略**：

1. **基于业务先验**：
   ```python
   # 年龄不可能超过 150
   clip_lower=0, clip_upper=150
   
   # 月薪通常在 0~100,000 之间
   clip_lower=0, clip_upper=100000
   ```

2. **基于分位数估计**（离线分析）：
   ```python
   import numpy as np
   
   # 取 99% 分位数作为上界
   upper_bound = np.percentile(data, 99)
   clip_lower=min(data), clip_upper=upper_bound
   ```

3. **基于截断代价评估**：
   - 计算被截断的数据比例
   - 权衡隐私保护强度与统计偏差
   - 若截断比例 > 5%，考虑提高 clip_upper

**注意事项**：
- Clip 会引入系统性偏差（低估总和/均值）
- 过宽的 clip 区间导致噪声过大
- 过窄的 clip 区间丢失极端值信息
- 必须在查询前确定，不能根据数据动态调整（否则违反 DP）

---

#### Min_count 阈值选择

**作用**：防止噪声计数接近 0 时，均值计算出现数值爆炸（Cauchy 分布长尾）。

**推荐值**：
- 保守场景：`min_count = 5.0`（默认）
- 宽松场景：`min_count = 2.0`
- 高风险场景：`min_count = 10.0`

**触发条件**：
```python
if noisy_count < min_count:
    return 0.0  # 拒绝返回不稳定结果
```

**权衡**：
- 阈值过高：频繁返回 0.0，可用性降低
- 阈值过低：可能返回极端异常值
- 建议根据最小可接受样本量设定

---

#### Namespace 设计

**原则**：不同数据集/业务域使用独立 namespace，避免预算混用。

**推荐命名规范**：
```python
# 按数据集 + 时间粒度划分
namespace = "hospital_patients_2024_q1"
namespace = "bank_transactions_monthly_2024_03"
namespace = "app_daily_active_users_2024_03_15"

# 按业务域划分
namespace = "hr_salary_analysis"
namespace = "customer_behavior_tracking"
namespace = "clinical_trial_group_a"
```

**预算管理**：
- 每个 namespace 独立追踪 ε/δ 消耗
- 可通过 `BudgetAccountant.remaining()` 查询剩余额度
- 超支时抛出 `PrivacyBudgetExhaustedError`（或别名 `PrivacyBudgetExhausted`）异常

---

#### 时间窗口重置

**适用场景**：长期运行的 Sidecar 服务，避免预算永久耗尽。

**配置方式**：
```bash
# 每天自动重置预算
export PRIVACY_BUDGET_WINDOW_SECONDS=86400

# 每周重置
export PRIVACY_BUDGET_WINDOW_SECONDS=604800
```

**工作原理**：
- 每个 namespace 维护独立的窗口起始时间
- 窗口到期后，`epsilon_spent` 和 `delta_spent` 清零
- SQLite 持久化模式下，窗口状态跨实例共享

**注意事项**：
- 窗口长度应根据业务周期设定（日/周/月）
- 窗口内的超支仍会被拒绝，不会提前重置
- 首次消费时开始计时，非固定日历周期

---

### 6.3 性能优化建议

#### 批量查询 vs 单次查询

**问题**：多次小查询会快速消耗预算，且噪声累积效应明显。

**优化方案**：
1. **合并同类查询**：
   ```python
   # ❌ 不推荐：多次 count 查询
   for dept in departments:
       count = dp.count(dept_employees[dept], epsilon=0.1)
   
   # ✅ 推荐：单次 histogram 查询
   counts = dp.histogram(all_departments, categories=departments, epsilon=1.0)
   ```

2. **利用联合敏感度**：
   - Histogram 的联合敏感度为 1（互斥分桶）
   - 一次性发布多个分桶，仅消耗一次预算

---

#### Noisify 接口性能优势

**对比**：
| 方式 | 数据传输量 | Sidecar 计算负载 | 适用数据规模 |
|------|-----------|-----------------|------------|
| 原始数据传入 | O(n) | O(n) clipping + 噪声 | < 10⁶ 条 |
| Noisify 中间结果 | O(1) | O(1) 噪声注入 | 任意规模 |

**推荐工作流**：
```
Spark/Flink/DuckDB → 聚合查询 → true_sum/true_count → Sidecar noisify → 发布
```

---

#### Chunked 接口内存优化

**适用条件**：
- 数据量：10⁶ ~ 10⁸ 条
- 可用内存：不足以一次性加载全部数据
- 网络带宽：希望降低单次传输峰值

**Chunk 大小调优**：
```python
# 根据可用内存估算
available_memory_mb = 512
bytes_per_record = 8  # float64
chunk_size = (available_memory_mb * 1024 * 1024) // bytes_per_record // 2
# 结果：约 33M 条/批（留一半余量）
```

---

### 6.4 安全注意事项

#### 浮点精度攻击（Mironov Attack）

**风险**：连续 Laplace/Gaussian 采样基于 IEEE 754 浮点数，存在理论上的精度泄漏风险。

**缓解措施**：
- 高安全场景考虑离散机制（如 Geometric 机制）
- 定期轮换密钥/种子
- 监控异常查询模式

**当前状态**：本模块使用 Python `random` 和 `numpy.random`，未实现离散机制。

---

#### 预算超支防护

**多层防护**：
1. **BudgetAccountant 强制检查**：每次查询前验证剩余预算
2. **SQLite 事务锁**：多实例并发时通过 `BEGIN IMMEDIATE` 保证原子性
3. **时间窗口重置**：避免长期运行后预算永久耗尽

**监控建议**：
```python
accountant = BudgetAccountant(namespace="critical_dataset")
remaining = accountant.remaining()
if remaining["epsilon"] < 0.5:
    logger.warning(f"Low budget remaining: {remaining}")
```

---

#### 输入数据验证

**潜在风险**：恶意构造的输入可能导致敏感度估计错误。

**防护措施**：
- Pydantic 模型自动校验参数类型
- Clip 区间必须由调用方显式提供（Gaussian）
- DataFrame 输入必须指定 `column` 参数
- SecretFlow 多 partition 时必须指定 `party`

---

### 6.5 故障排查速查表

| 现象 | 可能原因 | 解决方案 |
|------|---------|---------|
| `PrivacyBudgetExhausted` | 累计 ε/δ 超支 | 提高总预算 / 减少查询 / 等待窗口重置 |
| mean 返回 0.0 | `noisy_count < min_count` | 降低 `min_count` / 增加样本量 / 提高 ε |
| 结果负数 | count 被噪声拉低 | count 已做 `max(0, ...)` 截断；其他场景需后处理 |
| Gaussian 报错 delta=0 | 未提供 δ 或 δ≤0 | 设置 `delta > 0`（典型值 1e-6） |
| Clip bounds required | Gaussian sum/mean 缺少 clip | 提供 `clip_lower` 和 `clip_upper` |
| column must be specified | DataFrame 未指定列 | 在 `params` 中传入 `column` |
| 估计方差过大 | 本地 DP 样本量不足 | 增加用户数量（n ≥ 1000）/ 提高 ε |
| 结果偏差明显 | Clip 区间过窄 | 扩大 `clip_upper` / 重新评估分位数 |

---

### 6.6 与其他隐私技术的对比

| 技术 | 隐私保证强度 | 数据效用 | 适用场景 |
|------|------------|---------|---------|
| **差分隐私** | 最强（数学证明） | 中等（有噪声） | 统计发布、模型训练 |
| **K-匿名** | 中等（启发式） | 较高 | 数据脱敏发布 |
| **数据脱敏** | 较弱（可逆风险） | 高 | 测试数据生成 |
| **同态加密** | 强（密码学） | 无损 | 安全多方计算 |
| **联邦学习** | 依赖组合机制 | 高 | 分布式模型训练 |

**组合建议**：
- DP + K-匿名：先 K-匿名泛化，再对聚合结果加 DP 噪声
- DP + 联邦学习：在梯度聚合阶段注入 DP 噪声
- DP + 脱敏：脱敏用于微观数据，DP 用于宏观统计

---

## 7. 最佳实践总结

1. **明确隐私目标**：根据数据敏感度和合规要求确定总 ε/δ 预算
2. **合理选择机制**：简单查询用 Laplace，复杂场景用 Gaussian
3. **严格控制 Clip**：基于业务先验或离线分位数设定 clip 区间
4. **隔离命名空间**：不同数据集使用独立 namespace 管理预算
5. **优先 Noisify**：海量数据在外部引擎聚合后加噪
6. **监控预算消耗**：定期检查 `BudgetAccountant.remaining()`
7. **配置时间窗口**：长期运行服务设置 `PRIVACY_BUDGET_WINDOW_SECONDS`
8. **大样本本地 DP**：本地扰动需要 n ≥ 1000 才能获得可靠估计
9. **审计日志记录**：记录每次查询的 ε/δ 消耗，便于追溯
10. **持续评估效用**：定期对比带噪结果与真实值的误差，调整参数

---

## 8. 高级特性 API

### 8.1 `adaptive_clip`

```python
adaptive_clip(
    values: Any,
    epsilon: float,
    target_quantile: float = 0.95,
    num_iterations: int = 15,
    initial_clip: float = 10.0,
    column: Optional[str] = None,
    party: Optional[str] = None,
) -> tuple[float, float]
```

差分隐私自适应二分搜索估计 `[0.0, clip_upper]` 上下界。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `values` | `Any` | 是 | 输入数据 |
| `epsilon` | `float` | 是 | 本次自适应搜索的总隐私预算（会全部消耗） |
| `target_quantile` | `float` | 否 | 目标分位数，默认 0.95 |
| `num_iterations` | `int` | 否 | 二分搜索迭代次数，默认 15 |
| `initial_clip` | `float` | 否 | 初始 clip 上界，默认 10.0 |
| `column` | `Optional[str]` | 否 | 表格型输入的目标列名 |
| `party` | `Optional[str]` | 否 | SecretFlow 参与方 |

**返回值**：`(clip_lower, clip_upper)` 元组，`clip_lower` 固定为 0.0。

**预算消耗**：消耗 `epsilon` 预算，按 `num_iterations` 次 DP count 查询拆分。返回的 clip bounds 用于后续 sum/mean 调用，后续调用需额外消耗独立的隐私预算。

**使用示例**：

```python
api = DPApi(namespace="adaptive_demo")

# Step 1: 自适应搜索 clip 上界（消耗 epsilon=0.5）
clip_lower, clip_upper = api.adaptive_clip(data, epsilon=0.5, target_quantile=0.95)

# Step 2: 使用搜索到的 clip bounds 进行 sum 查询（额外消耗 epsilon=0.5）
result = api.sum(data, epsilon=0.5, clip_lower=clip_lower, clip_upper=clip_upper)
```

---

### 8.2 `dp_aggregate`

```python
dp_aggregate(
    df: Any,
    specs: List[Dict[str, Any]],
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "laplace",
    return_details: bool = False,
) -> Dict[str, Any]
```

表格级 DP 聚合编排：对 DataFrame 按列执行多种聚合，自动按列数拆分预算（组合定理）。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `df` | `Any` | 是 | 输入 DataFrame |
| `specs` | `List[Dict]` | 是 | 聚合规格列表，每个包含 `column`、`agg`、`clip_lower`、`clip_upper` |
| `epsilon` | `float` | 是 | 总隐私预算 ε |
| `delta` | `float` | 否 | 总隐私预算 δ |
| `mechanism` | `str` | 否 | `"laplace"` 或 `"gaussian"` |
| `return_details` | `bool` | 否 | 是否返回 DPResult 详情 |

**预算拆分**：`epsilon / num_specs`、`delta / num_specs` 均分给每个聚合规格。

**支持的 agg 类型**：`"count"`、`"sum"`、`"mean"`、`"histogram"`。

**使用示例**：

```python
api = DPApi(namespace="table_demo")
result = api.dp_aggregate(
    df,
    specs=[
        {"column": "age", "agg": "count"},
        {"column": "salary", "agg": "sum", "clip_lower": 0, "clip_upper": 100000},
        {"column": "age", "agg": "mean", "clip_lower": 0, "clip_upper": 150},
    ],
    epsilon=1.0,
)
```

---

### 8.3 `vector_sum` / `vector_mean`

```python
vector_sum(
    vectors: Any,
    max_norm: float,
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "gaussian",
    return_details: bool = False,
    confidence_level: float = 0.95,
) -> Union[np.ndarray, DPResult]

vector_mean(
    vectors: Any,
    max_norm: float,
    epsilon: float,
    delta: float = 0.0,
    mechanism: str = "gaussian",
    min_count: float = 5.0,
    return_details: bool = False,
    confidence_level: float = 0.95,
) -> Union[np.ndarray, DPResult]
```

高维向量 / 梯度 DP 加噪，用于 DP-SGD 训练。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `vectors` | `Any` | 是 | 输入向量矩阵，支持 list/ndarray/DataFrame |
| `max_norm` | `float` | 是 | L₂ 范数截断阈值 |
| `epsilon` | `float` | 是 | 隐私预算 ε |
| `delta` | `float` | 否 | 隐私预算 δ；Gaussian 机制必须 > 0 |
| `mechanism` | `str` | 否 | `"laplace"` 或 `"gaussian"`，推荐 `"gaussian"` |
| `min_count` | `float` | 否 | `vector_mean` 专用，noisy_count 低于此阈值时返回零向量 |
| `return_details` | `bool` | 否 | 是否返回 DPResult 详情 |
| `confidence_level` | `float` | 否 | 置信区间水平，默认 0.95 |

**算法流程**：

1. 对每行做 L₂ 范数截断：$\|v_i\|_2 > \text{max\_norm}$ 时缩放至 $\text{max\_norm}$
2. 对截断后的向量逐元素加噪
3. `vector_mean` 额外通过 noisy_count 归一化，防止低频数据发散

**使用示例**：

```python
api = DPApi(namespace="dpsgd_demo")

# DP-SGD 平均梯度
gradients = [[0.1, -0.3, 0.5], [0.2, 0.4, -0.1], ...]
avg_grad = api.vector_mean(gradients, max_norm=1.0, epsilon=0.5, delta=1e-5)
```

---

### 8.4 `dp_groupby`

```python
dp_groupby(
    df: Any,
    group_col: str,
    target_col: str,
    agg: str,
    epsilon: float,
    delta: float = 1e-5,
    clip_lower: Optional[float] = None,
    clip_upper: Optional[float] = None,
    mechanism: str = "laplace",
    return_details: bool = False,
) -> Dict[Any, Any]
```

Tau-Thresholding 差分隐私 SQL Group-By 过滤：自动过滤稀有分组，避免泄漏低频分组信息。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `df` | `Any` | 是 | 输入 DataFrame |
| `group_col` | `str` | 是 | 分组列名 |
| `target_col` | `str` | 是 | 聚合目标列名 |
| `agg` | `str` | 是 | 聚合类型：`"count"` / `"sum"` / `"mean"` |
| `epsilon` | `float` | 是 | 总隐私预算 ε |
| `delta` | `float` | 否 | 总隐私预算 δ |
| `clip_lower` | `Optional[float]` | 否 | sum/mean 截断下界 |
| `clip_upper` | `Optional[float]` | 否 | sum/mean 截断上界 |
| `mechanism` | `str` | 否 | `"laplace"` 或 `"gaussian"` |

**预算消耗**：将 `(epsilon, delta)` 按 `(num_groups × 2)` 拆分。每个 group 消耗 `epsilon/(num_groups*2)` 用于 count，若 `agg != "count"` 则再消耗 `epsilon/(num_groups*2)` 用于聚合。总消耗 ≤ epsilon。

**Tau 阈值**：$\tau = 1 + \ln(1/\delta_{\text{per\_query}}) / \varepsilon_{\text{per\_query}}$，noisy_count 低于 τ 的分组被自动过滤。

---

### 8.5 `Accumulator` 分布式累加器

```python
@dataclass
class Accumulator:
    count: float = 0.0
    sum: float = 0.0
    histogram: Dict[Any, float] = field(default_factory=dict)
    sensitivity: float = 1.0

    def __add__(self, other: "Accumulator") -> "Accumulator"
    def serialize(self) -> bytes
    @classmethod
    def deserialize(cls, b: bytes) -> "Accumulator"
```

分布式无噪流式累加器，用于 Map-Reduce 模式下的 DP 聚合。

**使用流程**：

1. **Worker 端**：`acc = api.create_accumulator(chunk)` 创建无噪局部累加器
2. **传输**：`acc.serialize()` 导出，发送到 Master 节点
3. **Master 端**：`merged_acc = master_acc + worker_acc` 合并
4. **Master 端**：`result = api.finalize_dp(merged_acc, epsilon, agg_type)` 统一加噪

```python
api = DPApi(namespace="distributed_demo")

# Worker 1
acc1 = api.create_accumulator([1.0, 2.0, 3.0], clip_lower=0.0, clip_upper=10.0)

# Worker 2
acc2 = api.create_accumulator([4.0, 5.0], clip_lower=0.0, clip_upper=10.0)

# Master: merge + finalize
merged = acc1 + acc2
result = api.finalize_dp(merged, epsilon=1.0, agg_type="sum")
```

---

### 8.6 `RDPAccountant`

```python
class RDPAccountant:
    def __init__(self, target_delta: float = 1e-5)
    def record_gaussian(self, sigma: float, sensitivity: float = 1.0) -> None
    def get_epsilon(self, delta: Optional[float] = None) -> float
    def reset(self) -> None
```

Rényi DP 会计：为 Gaussian 机制提供比基本组合更紧致的预算估计。

| 方法 | 说明 |
|---|---|
| `record_gaussian(sigma, sensitivity)` | 记录一次 Gaussian 机制调用 |
| `get_epsilon(delta)` | 自动搜索最优 Rényi 阶数 α，返回最小 ε |
| `reset()` | 重置所有记录 |

**Rényi 阶数**：默认搜索 `{1.5, 2.0, 3.0, 5.0, 8.0, 12.0, 16.0, 24.0, 32.0, 64.0, 128.0}`。

**使用示例**：

```python
from engine.privacy.budget import RDPAccountant

rdp = RDPAccountant(target_delta=1e-5)

# 记录 10 次 Gaussian 查询
for _ in range(10):
    rdp.record_gaussian(sigma=1.0, sensitivity=1.0)

# 获取总 ε（自动搜索最优 α）
total_epsilon = rdp.get_epsilon(delta=1e-5)
```

**与 BudgetAccountant 的关系**：`RDPAccountant` 是独立的辅助工具，不与 `BudgetAccountant` 自动集成。调用方可同时使用两者：`BudgetAccountant` 追踪基本组合下的保守上界，`RDPAccountant` 提供更紧致的参考估计。