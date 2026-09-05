# K-匿名与多维空间划分泛化算法技术指南 / K-Anonymity & Multidimensional Generalization Technical Guide

> 所属层次：隐私计算原语层（Privacy Primitives）
> 对应代码：`services/privacy-engine/sdk/kano/`（`kano.go` 371 行、`hierarchy.go` 243 行、`mondrian.go` 261 行）
> 前置阅读：`docs/learning/tech-differential-privacy.md`（聚合统计侧的保护）、`docs/learning/tech-dynamic-classification-funnel.md`（如何确定哪些字段是准标识符）

---

## 0. 阅读导航 / Reading Guide

| 章节 | 内容 |
|---|---|
| 1 | K-匿名模型、攻击者假设、扩展模型（$l$-多样性 / $t$-紧密性）与信息损失度量 |
| 2 | Go 实现全景：包结构、两套 Mondrian、调用链与参数推荐 |
| 3 | 数据集级 Mondrian 算法逐行剖析（`Anonymize` 与 `Mondrian` 的差异与输出格式） |
| 4 | 单记录层次泛化（`hierarchy.go` 内置层次表、`ChooseLevel` 启发式） |
| 5 | Distinct $l$-多样性校验 `CheckDistinctLDiversity` |
| 6 | API 手册（REST + gRPC + curl + 字段别名） |
| 7 | 参数自动推荐与画像持久化 |
| 8 | 复杂度与信息损失分析 |
| 9 | 最佳实践与反模式清单 |
| 10 | 测试与基准、故障排查 FAQ |

---

## 1. 技术简介 / Introduction

**K-匿名（K-Anonymity）** 由 Pierangela Samarati 与 Latanya Sweeney 于 1998 年提出，是数据发布（Data Publishing）场景的经典隐私保护模型。其核心思想：在发布的数据集中，任何一条记录的**准标识符（Quasi-Identifiers, QI）** 取值组合，至少与数据集中其他 $k-1$ 条记录完全相同，从而形成大小至少为 $k$ 的**等价类（Equivalence Class）**。

**形式化定义**：设 $T$ 为数据集，$QI \subseteq \text{attrs}(T)$，$T$ 关于 $QI$ 满足 $k$-匿名，当且仅当

$$\forall t \in T,\ \big|\{t' \in T : t'[QI] = t[QI]\}\big| \ge k$$

工程含义：任何掌握外部辅助知识库（选民登记册、社保名单、快递面单）的攻击者，把一条外部记录关联到发布表时，最多只能把它定位到一个大小为 $k$ 的候选集合，重识别概率上界为 $1/k$。

### 1.1 关键概念 / Core Concepts

1. **直接标识符（Direct Identifiers）**：能唯一定位个体的字段（身份证号、姓名、手机号、病历号）。发布前**必须 100% 剥离或强脱敏**（本项目走 `sdk/masking` 与 SM3/HMAC 假名化），K-匿名不负责处理它们。
2. **准标识符（Quasi-Identifiers, QI）**：单字段不足以识别、组合起来高度唯一的属性集（如 `[年龄, 性别, 邮编, 职业]`）。Sweeney 的经典结论：美国约 87% 人口可被 `[ZIP, Gender, DOB]` 三元组唯一识别。
3. **敏感属性（Sensitive Attributes, SA）**：需要保密的核心属性（诊断、HIV 状态、薪资）。K-匿名**不保护 SA 的取值**，只保证「持有某个 SA 值的人不止一个」。
4. **等价类（Equivalence Class, EC）**：QI 投影完全相同的记录子集。所有 $|EC_i| \ge k$ 即为合规。
5. **泛化（Generalization）与抑制（Suppression）**：
   - 区间泛化：`28 → [25,30]`；前缀泛化：`545001 → 545***`；集合泛化：`{男,女}`；层次泛化：`硕士 → 高等教育`。
   - 抑制：直接输出 `*`，是泛化的极限形式（信息损失最大但实现最简单）。

### 1.2 K-匿名的三类残余攻击与扩展模型

| 攻击 | 场景 | K-匿名能否防御 | 扩展模型 |
|---|---|---|---|
| 成员推断（Membership） | 判断某人是否在表中 | ✅（$1/k$ 上界） | — |
| 属性推断（Attribute） | 等价类内 SA 全部相同（如整类都是"HIV 阳性"） | ❌ | **Distinct $l$-Diversity** |
| 同质性攻击（Homogeneity） | 背景知识下等价类内 SA 分布极偏 | ❌ | **Entropy $l$-Diversity**、**$t$-Closeness** |
| 相似性攻击（Similarity） | SA 取值语义相近（"胃癌"与"胃溃疡"） | ❌ | $P$-Diversity、$p$-Sensitive $k$-Anonymity |

本项目落地的是 **Distinct $l$-Diversity 校验**（`kano.CheckDistinctLDiversity`，只校验不修复，见 §5）。$t$-Closeness（经验分布与全局分布的 Wasserstein 距离 $\le t$）与 Entropy $l$-Diversity 尚未内置，画像参数中保留了 `t` 字段（`{"k":5,"l":2,"t":0.2}`）以便后续接入。

### 1.3 信息损失度量 / Utility Metrics

泛化不可避免造成精度损失，标准度量：

- **NCP（Normalized Certainty Penalty）**，单条记录在某维度的归一化不确定性：

  $$\mathrm{NCP}_{\mathbb{Q}}(t) = \sum_{i=1}^{|QI|} w_i \cdot \frac{|t[QI_i]|}{|dom^*(QI_i)|}$$

  区间泛化时 $|t[QI_i]|$ 为区间宽度、$|dom^*|$ 为该属性全域宽度；抑制记为 1。数据集级 $\mathrm{IL} = \frac{1}{n}\sum_t \mathrm{NCP}_{\mathbb{Q}}(t)$。
- **DM（Discernibility Metric）**：$\mathrm{DM} = \sum_i |EC_i|$，惩罚「大等价类」，与 NCP 互补（NCP 看区间宽度、DM 看类大小）。
- **非同质威胁（NHT）**：结合 SA 分布的效用度量，用于对比 $l$-多样性方案。

!!! note "本仓库未内置 NCP/DM 计算"
    `sdk/kano` 目前只输出 `GroupCount` / `EquivalenceClassesCount`（等价类数）与泛化后的数据。上面的公式给出的是**评估口径**，落地建议在离线分析层用 SQL/Go 复算：等价类大小可从 `group_count` 与行数直接估算平均类规模 $n / \text{group\_count}$，这是最易采集的一阶效用指标。

---

## 2. 在 PrivShield 中的实现全景 / Implementation Map

### 2.1 包与职责

| 文件 | 入口 | 定位 | 终止条件 |
|---|---|---|---|
| [`sdk/kano/kano.go`](services/privacy-engine/sdk/kano/kano.go) | `Anonymize(records, qiFields, k)` | **REST 默认实现**（`/k_anonymize`、`/k_anonymize/table`、`/k_anonymize/dataframe` 全部走它） | `len(data) <= k` 或 `depth >= 32` |
| [`sdk/kano/mondrian.go`](services/privacy-engine/sdk/kano/mondrian.go) | `Mondrian(rows, qiCols, k, maxDepth)` | **教科书式 Mondrian**，`maxDepth` 由调用方控制，输出区间/集合泛化；**当前未被任何 REST/gRPC 入口调用**，只能作为 SDK 直接依赖使用 | `len < 2k` 或 `depth <= 0` |
| [`sdk/kano/hierarchy.go`](services/privacy-engine/sdk/kano/hierarchy.go) | `AnonymizeRecord(record, qiCols, hierarchies, k)` | **单记录层次泛化**（流式/逐条场景，同样无协议入口） | 由 `ChooseLevel(k, 4)` 决定层级 |
| [`sdk/kano/kano.go`](services/privacy-engine/sdk/kano/kano.go) | `CheckDistinctLDiversity(...)` | Distinct $l$-多样性**合规校验**（不修改数据） | — |

!!! warning "两套 Mondrian 的输出格式不同"
    - `Anonymize` → `generalizeValue()`：数值输出 `[28, 45]`（**逗号 + 空格**），分类输出公共前缀 `545*`，无公共前缀时 `*`；
    - `Mondrian` → `generalize()`：数值输出 `[28-45]`（**短横线，无空格**），分类输出集合 `{女,男}`（按 `sort.Strings` 字典序）。

    下游若做正则解析或前端展示格式化，**必须区分调用了哪个接口**。混用是当前最容易出现的兼容性缺陷。

### 2.2 调用链

```text
 外部业务 / 控制台
        │
        ├── REST :8079  /v1/privacy/k_anonymize[/record|/table|/dataframe]
        │               internal/rest/routes.go: kAnonymizeHandler / kAnonymizeTableHandler / ...
        ├── gRPC :50051 KAnonymizeRecord / KAnonymizeTable / KAnonymizeDataFrame
        │               internal/grpcserver/typed_server.go
        └── 编排入口    service-hub 六阶段流水线 Classify→Desensitize 阶段
                                │
                                ▼
                  service.PrivacyService（internal/service/service.go:1123-1160）
                  KAnonymize / KAnonymizeTable / KAnonymizeRecord / KAnonymizeDataFrame
                                │
                                ▼
                        sdk/kano（纯函数，零状态，不消耗隐私预算）
                                ▲
                                │ 参数推荐
                  profile.Resolver.RecommendDataParams(namespace, values, rows, qiCols)
                  → {"k_anonymity": {"k": <n/10 clamp 2..10>, "max_depth": 10}}
```

### 2.3 与 DP 的分工

| 场景 | 选择 | 原因 |
|---|---|---|
| 发布明细表（行级数据共享给第三方） | **K-匿名 Mondrian** | 保留行结构与可关联性，支持后续分析 |
| 回答聚合查询（计数/求和/均值） | **差分隐私**（`sdk/dp`） | 有严格 $\epsilon$-DP 证明，可抵抗多次查询组合攻击 |
| 单条记录出域（接口返回给前端） | **层次泛化 `AnonymizeRecord`** 或字段脱敏 | 无需全局数据集视角，流式友好 |
| 合规验收（发布前检查） | `CheckDistinctLDiversity` + 分类分级引擎 | 只读校验，可反复执行 |

两者可叠加：先 Mondrian 泛化明细表，再对泛化后的表做 DP 聚合统计（明细侧的泛化能显著降低聚合侧所需 $\epsilon$）。

---

## 3. 数据集级 Mondrian 算法剖析 / Mondrian Deep Dive

### 3.1 默认实现 `Anonymize`（kano.go）

```go
// Anonymize 对数据集执行 K-匿名处理。
// qiFields 为准标识符字段列表，k 为匿名化参数。
func Anonymize(records []Record, qiFields []string, k int) (*AnonymizationResult, error) {
	if len(records) == 0 || len(qiFields) == 0 || k <= 0 {
		return &AnonymizationResult{Records: records, K: k}, nil
	}

	// 复制数据集避免修改原始数据
	data := make([]Record, len(records))
	for i, r := range records {
		data[i] = make(Record, len(r))
		for k, v := range r {
			data[i][k] = v
		}
	}

	// 执行 Mondrian 切分（带最大深度剪枝防护）
	groups := mondrian(data, qiFields, k, 0)

	// 泛化每个等价类
	result := &AnonymizationResult{
		Records:    make([]Record, 0, len(records)),
		K:          k,
		GroupCount: len(groups),
	}
	for _, group := range groups {
		generalizeGroup(group, qiFields)
		result.Records = append(result.Records, group...)
	}

	return result, nil
}
```

四个必须知道的语义：

1. **退化即透传**：`records` 为空、`qiFields` 为空或 `k <= 0` 时**原样返回**且不报错（`error` 恒为 `nil`）。REST 层已用 `INVALID_ARGUMENT` 前置拦截，但**直接 import SDK 的调用方必须自己检查**——传 `k=0` 会拿到一张完全没有泛化的表。
2. **深拷贝**：入参 `records` 不被修改（内层 `Record` 逐个复制），可安全复用原始数据；代价是一次 $O(n \times |attrs|)$ 内存拷贝。
3. **`K` 字段是「请求的 k」而非「实际达到的 k」**（结构体注释写的是"实际达到的 k 值"，与实现不一致）。由于切分终止条件保证每个分区 $\ge k$（除 `maxMondrianDepth` 触顶与整表行数 $< k$ 的情形），实际满足值通常 $\ge$ 请求值，但**不能把 `result.K` 当作合规证明**。合规校验必须显式做：把 `result.Records` 按 QI 重新分组，取最小类规模（或用 `CheckDistinctLDiversity` 的 `GroupStats[].RecordCount` 交叉核对）。
4. **`Generalizations` 恒为空切片**。字段预留但未填充（未记录每个字段的泛化层级），做 NCP 统计时需自行从泛化字符串反推区间宽度。

递归主体：

```go
// mondrian 递归二分数据集，直到每个分区大小 < k、达到最大深度或无法继续切分。
func mondrian(data []Record, qiFields []string, k int, depth int) [][]Record {
	const maxMondrianDepth = 32
	if len(data) <= k || depth >= maxMondrianDepth {
		return [][]Record{data}
	}

	// 找到区分度最大的字段
	bestField := findBestSplit(data, qiFields)
	if bestField == "" {
		return [][]Record{data}   // 无法继续切分
	}

	// 按中位数索引切分（确保两侧均 >= k）
	left, right := partitionByMedian(data, bestField)
	if len(left) < k || len(right) < k {
		return [][]Record{data}
	}

	groups := make([][]Record, 0, 4)
	groups = append(groups, mondrian(left, qiFields, k, depth+1)...)
	groups = append(groups, mondrian(right, qiFields, k, depth+1)...)
	return groups
}
```

- **`maxMondrianDepth = 32` 是硬性护栏**：防止「大量重复值 + 中位数切分退化」导致的无限递归/栈溢出（K-匿名实现的经典事故点）。触顶时可能返回规模 $< k$ 的分区，即**深度剪枝会以牺牲匿名性为代价换取终止**——因此 §3.3 的合规校验不可省略。
- 终止条件用 `len(data) <= k`（而非 `< 2k`）：大小为 $k$ 的分区直接成为叶子，符合定义但**泛化程度偏高**（能再切也不切）。
- `findBestSplit` 返回 `""` 表示所有 QI 在该分区内**取值完全一致**（数值 `range == 0`、分类 `unique <= 1`），此时无维度可切，整块作为等价类。

维度选择（区分度 = range）：

```go
func findBestSplit(data []Record, qiFields []string) string {
	bestField := ""
	bestRange := -1
	for _, field := range qiFields {
		// ... 收集列值
		if isNumeric(values) {
			nums := parseNumeric(values)
			sort.Float64s(nums)
			if len(nums) < 2 { continue }
			rangeVal := int(nums[len(nums)-1] - nums[0])   // 注意：截断为 int
			if rangeVal > bestRange { bestRange, bestField = rangeVal, field }
		} else {
			unique := uniqueValues(values)
			if len(unique) <= 1 { continue }
			rangeVal := len(unique)                        // 分类维度用「基数」
			if rangeVal > bestRange { bestRange, bestField = rangeVal, field }
		}
	}
	return bestField
}
```

!!! warning "数值 range 被 `int(...)` 截断，与分类基数不同量纲"
    1. **浮点年龄/血压被压成整数**：`range = 0.5` 截断为 `0`，该维度会被误判为「无区分度」而跳过，导致泛化不足；
    2. **量纲不可比**：`age` 的 range 可达 90，`zipcode` 的基数只有 5，比较结果**天然偏向数值维度**。经典 Mondrian 用**归一化跨度**（$(max-min)/(dom_{max}-dom_{min})$）消除量纲，此处未做归一化。
    实践影响：QI 中同时含数值与高基数字符串（如身份证号片段）时，切分轴可能被数值维度长期垄断，字符串维度直到最后才切。若这不可接受，应改用 §3.2 的 `Mondrian()`（同样问题但 `span()` 不截断整数）。

中位数切分（**索引切分**，而非值切分）：

```go
// partitionByMedian 按字段中位数索引将数据分为两半。
// 使用索引切分而非值比较，确保两侧分区均 >= k，
// 避免因大量重复值聚集在中位数导致一侧 < k 而违反 K-匿名保证。
func partitionByMedian(data []Record, field string) ([]Record, []Record) {
	sorted := make([]Record, len(data))
	copy(sorted, data)
	sort.SliceStable(sorted, func(i, j int) bool {
		return compareValues(sorted[i][field], sorted[j][field]) < 0
	})
	mid := len(sorted) / 2
	return sorted[:mid], sorted[mid:]
}
```

`copy(sorted, data)` 只复制 **`Record` map 的引用**，而后续 `generalizeGroup` 写的是 `r[field] = generalized`——即**同一 map 对象被就地改写**。因为该记录只属于一个叶子分区，结果仍然正确；但：

- 若调用方在 `Anonymize` 之外还持有 `data[i]` 的引用（例如自己重组过切片），会看到被改写后的值；
- 这是「深拷贝入参切片、浅拷贝 map 元素」的混合语义，理解成本较高。使用 SDK 时应把入参视为**被接管**，不要复用原始 map。

等价类泛化：

```go
func generalizeGroup(group []Record, qiFields []string) {
	if len(group) <= 1 {
		return                       // 单条记录不泛化（可能 < k！）
	}
	for _, field := range qiFields {
		minVal, maxVal := findMinMax(values)
		generalized := generalizeValue(minVal, maxVal)
		for _, r := range group {
			r[field] = generalized   // 整类同值 → 构成等价类
		}
	}
}

func generalizeValue(minVal, maxVal string) string {
	if minVal == maxVal {
		return minVal
	}
	if isNumeric([]string{minVal, maxVal}) {
		return "[" + minVal + ", " + maxVal + "]"     // 区间泛化
	}
	prefix := commonPrefix(minVal, maxVal)            // 前缀泛化
	if prefix == "" {
		return "*"
	}
	return prefix + "*"
}
```

两个值得注意的行为：

- **`len(group) <= 1` 直接返回**：单记录等价类**完全不泛化**（既不是抑制也不是区间），意味着「原值 + 只有 1 条」的组合会原样发布，这是 K-匿名的**违反态**。该分支只有在触顶 `maxMondrianDepth` 或整表行数 $< k$ 时才可达（正常路径下 `mondrian` 保证 $\ge k$），但一旦被触达就是真实泄露面。生产上应在出口做「等价类 $< k$ → 抑制为 `*`」的兜底（本项目当前未在 SDK 内实现）。
- **前缀泛化的区间边界由 `min/max` 字典序决定**：`compareValues` 对数值串走数值比较、否则走 `slices.Compare([]rune)`。因此 `{"10","9"}` 会被判定 `9 < 10`（数值口径，正确），而混合 `{"9","A"}` 走 `isNumeric` 失败 → 字典序，`commonPrefix` 得空 → `*`（安全侧回落）。

### 3.2 参考实现 `Mondrian`（mondrian.go）

```go
func Mondrian(rows []Record, qiCols []string, k int, maxDepth int) (*KAnonymizeResult, error) {
	if len(rows) == 0 {
		return &KAnonymizeResult{Records: nil, K: k, QICols: qiCols, EquivalenceClassesCount: 0}, nil
	}
	if k < 2 {
		return nil, fmt.Errorf("k must be at least 2 for meaningful anonymity, got %d", k)
	}
	if len(rows) < k {
		return nil, fmt.Errorf("input table has %d rows, but k-anonymity requires at least %d", len(rows), k)
	}
	if len(qiCols) == 0 {
		return nil, fmt.Errorf("qi_cols must not be empty")
	}
	for _, col := range qiCols {
		if _, ok := rows[0][col]; !ok {
			return nil, fmt.Errorf("qi_col %q not found in records", col)
		}
	}

	result := mondrianRecurse(rows, qiCols, k, maxDepth)
	// 计算真实等价组数（按 QI 投影去重）
	eqSet := make(map[string]struct{})
	// ...
	return &KAnonymizeResult{Records: result, K: k, QICols: qiCols, EquivalenceClassesCount: len(eqSet)}, nil
}
```

与 `Anonymize` 的关键差异（选型依据）：

| 维度 | `Anonymize`（默认） | `Mondrian`（参考实现） |
|---|---|---|
| 参数校验 | 宽松（退化透传，不报错） | **严格 fail-fast**：`k < 2`、`len(rows) < k`、`qi_cols` 缺失列均返回 error |
| 深度控制 | 常量 `32` | 调用方传入 `maxDepth`（`<= 0` 立即停止并泛化整表） |
| 终止阈值 | `len <= k` | `len < 2k` |
| 维度选择 | `int` 截断 range / 基数 | `span()` 返回 `float64`（数值 `max-min`，分类 `unique-1`），不截断 |
| 切分点 | 纯 `mid` | `splitIdx = max(k, min(mid, len-k))`，**显式保证两侧 $\ge k$** |
| 数值泛化格式 | `[28, 45]` | `[28-45]`，且 `formatNum` 把 `28.0` 归一为 `28` |
| 分类泛化格式 | 公共前缀 `545*` / `*` | 集合 `{女,男}`（字典序） |
| 数据拷贝 | 深拷贝入参 map | `generalize()` 内对每条记录新建 `Record`（**不修改入参**） |
| 等价类计数 | `GroupCount = len(groups)`（切分产物数） | `EquivalenceClassesCount` = 泛化后按 QI 去重的**真实**类数 |
| 当前调用方 | REST/gRPC 全部端点 | 仅单元测试（`TestMondrian_*`）与二次开发 |

`medianSplit` 的这行是 Mondrian 正确性的核心细节：

```go
mid := len(sorted) / 2
splitIdx := max(k, min(mid, len(sorted)-k))
if splitIdx < k || len(sorted)-splitIdx < k {
	return -1
}
```

把中位数索引**夹逼**到 $[k,\ n-k]$，确保左右两侧都不足 $k$ 的情况被提前排除；返回 `-1` 时上层退化为「整块泛化」。这比 `Anonymize` 的事后 `len(left) < k` 检查更紧凑。

!!! note "为什么两套实现并存"
    `Anonymize` 为对齐历史 Python `kano.py` 的行为（含输出格式与宽松语义）而保留，是当前 API 契约；`Mondrian` 是算法正确性更强的参考实现（对应专利与论文材料中的 Mondrian KD-Tree 描述）。新接入方若要严格 fail-fast 与可预测的集合泛化，建议显式调用 `Mondrian`（需自行在 `PrivacyService` 增加转发方法，当前 REST 层的 `max_depth` 字段**尚未接线**，见 §6.3）。

### 3.3 发布前的合规自检（必做）

由于 `result.K` 只是回显、`Generalizations` 未填充、且深度剪枝可能产生 $< k$ 的类，**任何对外发布都必须独立做一次最小等价类校验**。两种可立即使用的圆法：

```go
// 方式一：直接复用 l-多样性校验器的分组结果看最小类规模
res := kano.CheckDistinctLDiversity(generalized, qiCols, "diagnosis", 1)
minGroup := math.MaxInt
for _, g := range res.GroupStats {
    if g.RecordCount < minGroup {
        minGroup = g.RecordCount
    }
}
// minGroup 即实际达到的 k 值（应 >= 请求的 k）

// 方式二：一次 MapReduce 统计（适用于只要 k 值的场景）
sizes := map[string]int{}
for _, r := range generalized {
    key := r["age"] + "|" + r["zipcode"]
    sizes[key]++
}
```

推荐把该自检包成发布流程的**硬门禁**：`actual_k < requested_k` 则拒绝发布并告警（而不是默默抑制），否则一次数据倾斜就能把小等价类直送出去。

---

## 4. 单记录层次泛化 / Record-Level Hierarchical Generalization

源码：[`sdk/kano/hierarchy.go`](services/privacy-engine/sdk/kano/hierarchy.go)

适用场景：流式接口逐条返回（无全局数据集视角）、或者已在离线阶段算好每个字段的泛化层级。它**不保证** $k$-匿名（只看单条），仅提供同一层级的泛化函数。

### 4.1 内置层次函数与层级表

```go
// HierarchyFunc 层次泛化函数签名。
// 输入：原始值、泛化层级；输出：泛化后的值。
type HierarchyFunc func(value string, level int) string

// BuiltinHierarchies 内置准标识符泛化层次函数映射表。
var BuiltinHierarchies = map[string]HierarchyFunc{
	"age":       AgeHierarchy,
	"zipcode":   ZipcodeHierarchy,
	"gender":    GenderHierarchy,
	"salary":    SalaryHierarchy,
	"education": EducationHierarchy,
}
```

| QI | level 0 | level 1 | level 2 | level 3 | level $\ge$ 4 |
|---|---|---|---|---|---|
| **age** | `28` | `[25-30]` | `[20-30]` | `[20-40]` | `*` |
| **zipcode** | `545001` | `545***` | `54****` | `5*****` | `*` |
| **gender** | `男` | `*` | `*` | `*` | `*` |
| **salary**（单位：K） | `23` | `[20K-25K]` | `[20K-30K]` | `[0K-50K]` | `*` |
| **education** | `硕士` | `高等教育` / `基础教育` | `*` | `*` | `*` |

实现细节与坑：

```go
func AgeHierarchy(value string, level int) string {
	age, err := strconv.Atoi(value)
	if err != nil {
		if level >= 1 {
			return "*"      // 解析失败 → 直接抑制（安全侧）
		}
		return value        // level 0 → 原样返回（可能是脏数据）
	}
	switch level {
	case 1:
		start := (age / 5) * 5
		return fmt.Sprintf("[%d-%d]", start, start+5)
	// ...
	default:
		return "*"
	}
}
```

1. **整数除法向下取整，负年龄会向 0 取整**：`-3/5 == 0`（Go 截断除），`-3` → `[0-5]`。年龄场景不构成问题，但复用该函数做其它有负数的字段需注意。
2. **区间右端重叠**：`28 → [25-30]`、`30 → [30-35]`——`30` 同时属于两个区间，语义上是**闭区间重叠**。下游解析器不能假定区间不交。历史缘由是与 Python 实现对齐（`TestAlignPython_AgeHierarchy` 固定了该行为）。
3. **解析失败即抑制**：`"三十岁"` 在 `level >= 1` 时返回 `*`，属于 fail-closed，但会让“脏数据比例”直接影响信息损失指标，应在前置清洗阶段处理。
4. **`ZipcodeHierarchy` 用 `[]rune` 切片**，中文地址串也能按字符而非字节截断；长度不足时逐级回落至 `*`（如 `"12"` 在 level 1 拿不到 3 位，直接 `*`）。
5. **`EducationHierarchy` 是白名单机制**：仅 `本科/硕士/博士/博士后/MBA/EMBA/bachelor/master/phd/doctorate` 归为 `高等教育`，**其余一律 `基础教育`**（包括“高中”“小学”“不限”）。注意 `strings.ToLower` 对中文无效，英文项必须小写匹配；`"Master"` 会被误归为 `基础教育`。

### 4.2 层级选择启发式 `ChooseLevel`

```go
// ChooseLevel 根据 k 值选择泛化层级。
//
// 采用启发式策略：level 与 k/5 成正比，但不超过 maxLevel 且至少为 1。
func ChooseLevel(k, maxLevel int) (int, error) {
	if k < 2 { return 0, fmt.Errorf("k must be at least 2, got %d", k) }
	if maxLevel < 1 { return 0, fmt.Errorf("maxLevel must be at least 1, got %d", maxLevel) }
	level := k / 5
	if level < 1 { level = 1 }
	if level > maxLevel { level = maxLevel }
	return level, nil
}
```

| $k$ | 2~4 | 5~9 | 10~14 | 15~19 | $\ge$ 20 |
|---|---|---|---|---|---|
| level（maxLevel=4） | 1 | 1 | 2 | 3 | 4 |

!!! warning "`k` 与「真实等价类大小」无关"
    `ChooseLevel` 是**纯启发式**：它把 $k$ 当作“想要多强的保护”的标尺，而**不考察数据分布**。单靠它无法保证任何 $k$-匿名性质（因为看不到全集）。它的合理用法是：由 §7 的推荐器给出 `k`，再由业务层统一决定层级；真正的合规保障必须来自 Mondrian 或发布前的 §3.3 自检。

### 4.3 记录级入口

```go
func AnonymizeRecord(record Record, qiCols []string, hierarchies map[string]HierarchyFunc, k int) (Record, error) {
	if k < 2 { return nil, fmt.Errorf("k must be at least 2, got %d", k) }
	if len(qiCols) == 0 { return record, nil }
	// 拷贝记录
	for _, col := range qiCols {
		value, ok := result[col]
		if !ok { continue }
		hierFunc, ok := hierarchies[col]
		if !ok {
			hierFunc, ok = BuiltinHierarchies[col]
			if !ok { continue }        // ⚠ 未知字段：不泛化、不报错
		}
		maxLevel := 4                  // 默认最大层级（硬编码）
		level, err := ChooseLevel(k, maxLevel)
		// ...
		result[col] = hierFunc(value, level)
	}
	return result, nil
}
```

!!! danger "未知 QI 字段被静默跳过（保留原值）"
    `hierarchies` 未传时回落 `BuiltinHierarchies`，两者都没有的列名（如 `birth_date`、`name`）**不泛化也不报错**。对高基数字段（身份证号、生日）而言这等于直接泄露。接入时必须：
    1. 校验 `qiCols` 每一项均能命中层次函数（先调 `kano.BuiltinHierarchies` 比对）；
    2. 对命中失败的字段显式配 `Suppress`（`func(string, int) string { return "*" }`）而非依赖默认行为。

    `AnonymizeRecordBatch` 只是逐个调单记录版本（串行，无分块并行），因为它面向流式小批量；大表请用 Mondrian。

---

## 5. Distinct $l$-多样性校验 / L-Diversity Verification

```go
// CheckDistinctLDiversity 校验数据集在给定准标识符与敏感属性下是否满足 Distinct L-Diversity。
// 每个等价类中敏感属性的不同取值数必须 >= l，有效防御同质性攻击 (Homogeneity Attack)。
func CheckDistinctLDiversity(records []Record, qiFields []string, sensitiveField string, l int) *LDiversityResult
```

输出结构（全部带 `json` tag，可直接序列化给控制台展示）：

```go
type LDiversityResult struct {
	IsCompliant  bool             `json:"is_compliant"`
	L            int              `json:"l"`
	MinDiversity int              `json:"min_diversity"`
	Violations   int              `json:"violations"`
	GroupCount   int              `json:"group_count"`
	GroupStats   []GroupDiversity `json:"group_stats"`
}

type GroupDiversity struct {
	GroupIndex      int            `json:"group_index"`
	RecordCount     int            `json:"record_count"`
	DistinctCount   int            `json:"distinct_count"`
	SensitiveValues map[string]int `json:"sensitive_values"`
	IsCompliant     bool           `json:"is_compliant"`
}
```

实现要点：

1. **分组键是 QI 值的 `|` 拼接**：

   ```go
   var qiKey strings.Builder
   for _, q := range qiFields {
       qiKey.WriteString(r[q])
       qiKey.WriteByte('|')
   }
   ```

   注意两个后果：(a) 字段值**本身含 `|`** 时可能产生键碰撞（如 `age="30|"`），建议先清洗或确保 QI 不含分隔符；(b) `qiFields` **顺序变化会改变分组结果**（在值含分隔符时），校验与泛化应使用完全相同的列序。

2. **空敏感值不计入 distinct**（`if val != ""`）：一个全是空诊断的类，`DistinctCount = 0`，必然 `violations++`。这是正确行为（缺失不等于多样），但会让数据质量问题伪装成隐私不合规，排查时要区分。
3. **`l <= 1` 强制归一为 1**，即退化为「非空检查」；`records` 为空时返回 `IsCompliant: true`（空集合平凡满足）且 `MinDiversity: 0`。
4. **只读校验，不修改数据**，因此可安全地当作发布门禁反复调用，也不消耗隐私预算。
5. `MinDiversity` 初始为 `math.MaxInt`，遍历后若仍为 `MaxInt`（全空类情形）则回写 `0`，避免 JSON 里出现天文数字。

典型用法——在 Mondrian 泛化后串一道验收：

```go
gen, err := kano.Anonymize(records, []string{"age", "zipcode", "gender"}, 5)
if err != nil { /* ... */ }

lRes := kano.CheckDistinctLDiversity(gen.Records, []string{"age", "zipcode", "gender"}, "diagnosis", 2)
if !lRes.IsCompliant {
	slog.Warn("l-diversity violated",
		"violations", lRes.Violations, "min_diversity", lRes.MinDiversity,
		"groups", lRes.GroupCount)
	// 处置：提高 k 重新泛化，或对违规类等价类做整类抑制
}
```

---

## 6. API 使用手册 / API Handbook

### 6.1 REST 端点（前缀 `/v1/privacy`，默认 `:8079`）

| 路径 | Handler | 底层 | 响应关键字段 |
|---|---|---|---|
| `POST /k_anonymize` | `kAnonymizeHandler` | `kano.Anonymize` | `records`/`result`、`k`、`group_count` |
| `POST /k_anonymize/record` | `kAnonymizeHandler`（同上别名） | `kano.Anonymize` | 同上 |
| `POST /k_anonymize/table` | `kAnonymizeTableHandler` | `kano.Anonymize` | 同上 |
| `POST /k_anonymize_table` | `kAnonymizeTableHandler` | `kano.Anonymize` | 同上 |
| `POST /k_anonymize/dataframe` | `kAnonymizeDataFrameHandler` | `kano.Anonymize`（`map[string]interface{}` 自动 `fmt.Sprintf("%v")` 转串） | `records` |

**字段别名与默认值**（`routes.go:1040-1160`）：

- `records` ⇄ `record`（单条自动升为单元素数组）、`rows` ⇄ `records`；
- `qi_fields` ⇄ `qi_cols`；
- `/k_anonymize` 对 `k < 1` 补为 `2`，`/k_anonymize/table` 对 `k < 2` 补为 `2`；
- 两者都要求数据非空且 QI 非空，否则 `400 INVALID_ARGUMENT`；
- `group_count` 来自切分产物数（`len(groups)`），**不等于泛化后的去重等价类数**（两个不同切分叶子的泛化值可能完全相同），严格数值请用 `Mondrian` 的 `equivalence_classes_count`。

### 6.2 请求示例

```bash
# 表级 Mondrian 泛化
curl -sS -X POST http://127.0.0.1:8079/v1/privacy/k_anonymize/table \
  -H 'Content-Type: application/json' \
  -d '{"qi_cols":["age","zipcode","gender"],"k":5,"rows":[
        {"age":"28","zipcode":"545001","gender":"男","diagnosis":"高血压"},
        {"age":"31","zipcode":"545001","gender":"女","diagnosis":"糖尿病"},
        {"age":"29","zipcode":"545002","gender":"男","diagnosis":"高血压"},
        {"age":"35","zipcode":"545002","gender":"女","diagnosis":"未见异常"},
        {"age":"33","zipcode":"545003","gender":"男","diagnosis":"糖尿病"}]}'
# {"records":[{"age":"[28, 35]","zipcode":"545***","gender":"*", ...}],"k":5,"group_count":1}
# 注：敏感列 diagnosis 不在 qi_cols 中，因此保持原值

# DataFrame 形式（值可以是数字/嵌套类型）
curl -sS -X POST http://127.0.0.1:8079/v1/privacy/k_anonymize/dataframe \
  -H 'Content-Type: application/json' \
  -d '{"qi_cols":["age"],"k":3,"records":[{"age":28},{"age":31},{"age":29}]}'
```

### 6.3 已知契约限制

| 限制 | 现状 | 影响与规避 |
|---|---|---|
| `max_depth` 入参 | `/k_anonymize/table` 解析了 `max_depth` 但 **未传入服务层**（`KAnonymizeTable` 无该参数） | 递归深度固定 32（`maxMondrianDepth`）；需精确控深只能在自己代码里 import `kano.Mondrian`（见 §2.1，该函数当前无协议入口） |
| 层次泛化端点 | **REST 与 gRPC 都没有暴露 `kano.AnonymizeRecord`**：`/k_anonymize/record` 复用 `kAnonymizeHandler`（走整表 `Anonymize`），gRPC `KAnonymizeRecord` 走 `svc.MaskRecord`（掩码）；`PrivacyService.KAnonymizeRecord` 虽存在但无任何调用方 | 需要逐条层次泛化只能在 Go 代码里直接调 SDK，见 §6.4 |
| `Generalizations` | 恒为空 | NCP 统计需自行反推 |
| $l$-多样性 | 无独立 REST 端点（SDK + gRPC 可用） | 验收阶段建议内嵌到发布作业 |

### 6.4 gRPC

`proto/privacy.proto` 定义了 `KAnonymizeRecord`、`KAnonymizeTable`、`KAnonymizeDataFrame` 三个 RPC，但 **`KAnonymizeRecord` 名不副实**：

```go
// internal/grpcserver/typed_server.go:154
// KAnonymizeRecord K-匿名单记录（实际执行掩码脱敏，K-匿名语义由表级 KAnonymizeTable 实现）
func (s *TypedServer) KAnonymizeRecord(_ context.Context, req *pb.KAnonymizeRequest) (*pb.KAnonymizeResponse, error) {
	result := s.svc.MaskRecord(req.GetRecord())
	return &pb.KAnonymizeResponse{Result: result}, nil
}
```

旧的泛型 `Server`（`internal/grpcserver/server.go:202`）实现完全相同，同样调用 `MaskRecord`。

由此得到三个跨协议事实，切换到 gRPC 时必须逐条回归：

| RPC | 实际落到 | 与 REST 同名端点是否一致 |
|---|---|---|
| `KAnonymizeRecord` | `svc.MaskRecord`（**字段掩码**，忽略 `qi_cols`/`k`） | ❌ REST `/k_anonymize/record` 走整表 `kano.Anonymize` |
| `KAnonymizeTable` | `svc.KAnonymizeTable` → `kano.Anonymize` | ✅ 一致（输出 `[a, b]` 格式，非 `Mondrian` 的 `[a-b]`） |
| `KAnonymizeDataFrame` | `svc.KAnonymizeDataFrame` → `kano.Anonymize`（值 `fmt.Sprintf("%v")` 强转字符串） | ✅ 一致 |

`kano.AnonymizeRecord(record, qiCols, nil, k)`（层次泛化）经 `PrivacyService.KAnonymizeRecord`（`internal/service/service.go:1128`）暴露，但该方法在两个 gRPC Server 与 REST 路由中**均无调用方**——即层次泛化能力目前只能通过 Go 侧直接 import SDK 使用。若误以为 gRPC 的 `KAnonymizeRecord` 提供层次泛化，会得到一份「看起来已处理、实则只是打码」的单记录结果。

---

## 7. 参数自动推荐 / Profile Recommendation

`pkg/profile/resolver.go:RecommendDataParams` 是数据驱动的参数估计器，被 `PrivacyService.RecommendParams` 与 gRPC `RecommendParams` 调用：

```go
// 2. 推荐 K-Anonymity 参数
if len(rows) > 0 {
	n := len(rows)
	k := n / 10
	if k < 2 { k = 2 }
	if k > 10 { k = 10 }
	kanoParams := map[string]interface{}{
		"k":         k,
		"max_depth": 10,
	}
	recommendations["k_anonymity"] = kanoParams
	r.SavePersonalizedParams(namespace, "k_anonymity", kanoParams)
}
```

同一函数还推荐 DP 参数（`epsilon=1.0`、`delta = min(1e-5, 1/(10n^2))`、`clip_lower/upper` 取 $p_5/p_{95}$），供 §1.5 的组合策略直接取用。

| 项目 | 口径 |
|---|---|
| $k$ | $\mathrm{clamp}(n/10, 2, 10)$：小表（$n<20$）给 2，大表封顶 10 |
| `max_depth` | 固定 10（与 `Mondrian` 配套；`Anonymize` 不读此值） |
| 持久化 | `SavePersonalizedParams` 增量写入 `config/personalized-profiles.yaml` 的 `<namespace>.k_anonymity`（**不删除已有键**） |
| 内置默认 | `{"k":5,"l":2,"t":0.2,"max_depth":10}`（`defaultProfile()`） |
| 校验 | `profile.Validate("k_anonymity", params)` 要求 $k \ge 2$（兼容 `int` 与 YAML 解出的 `float64`） |

!!! tip "$n/10$ 只是上限直觉，不是万能公式"
    推荐器的 $k$ 随数据量增长（到 10 封顶），而真实威胁模型中需要的 $k$ 取决于**外部知识库的大小**（通常是全国人口而非本表行数）。处理万人级医疗明细时，建议按合规等级手工上调：L4 级（性病/艾滋病/HIV）建议 $k \ge 20$，L3 级（慢病诊断）建议 $k \ge 10$，并配合 $l \ge 2$。

---

## 8. 复杂度与性能 / Complexity

以 `Anonymize` 为例（$n$ 行、$m = |QI|$、$D$ 为递归深度）：

| 阶段 | 时间复杂度 | 说明 |
|---|---|---|
| 深拷贝 | $O(n \cdot a)$ | $a$ 为平均字段数 |
| 每层 `findBestSplit` | $O(m \cdot n\log n)$ | `isNumeric`+`parseNumeric`+`sort` 在每个维度重复执行 |
| 每层 `partitionByMedian` | $O(n \log n)$ | 又一次全排序（与上一步可合并，为可演进优化点） |
| 总体 | $O(D \cdot m \cdot n\log n)$ | $D \le 32$，实测方型表上 `depth` 通常 $\le \lceil\log_2(n/k)\rceil$ |
| 泛化 | $O(n \cdot m)$ | 每类一遍 `findMinMax` |

实践量级：万行×4 维在本机的 Mondrian 处理为毫秒级；`BenchmarkAgeHierarchy`、`BenchmarkAnonymizeRecord` 可直接量化单记录开销。瓶颈通常不在算法，而在 **`map[string]string` 的哈希开销**——超大表（$>10^6$ 行）建议改用列式结构并在外层分片，每片独立泛化（注意：分片会破坏全集等价类，需先按 QI 预排序再切分，否则各片独立泛化的等价类可能 $< k$）。

---

## 9. 生产最佳实践与陷阱 / Best Practices & Pitfalls

1. **维度灾难（Curse of Dimensionality）**。QI 列数越多，高维空间越稀疏，Mondrian 被迫把区间泛到全域或 `*`，效用骤降。**只把真正用于重识别的 3~5 个字段列为 QI**，其余走字段脱敏（`sdk/masking`）。哪些字段该入 QI 由分类分级引擎（`docs/learning/tech-dynamic-classification-funnel.md`）的 `L3+` 结果给出。
2. **先剥离直接标识符**。K-匿名不处理姓/证号/手机号；把它们放进 `qi_cols` 既不能有效泛化（`BuiltinHierarchies` 无对应项→静默跳过），也不能保护它们。
3. **不要把高基数字段放进 QI**。如身份证号前缀、详细科室名——`findBestSplit` 的「唯一值个数」口径会让它们长期占据切分轴，而它们的公共前缀泛化几乎不带信息。
4. **发布后不可逆转**。泛化不可「去掉」；如果下游需要精确值，应走带审计的原始值接口（service-hub 六阶段流水线）而不是降低 $k$。
5. **小等价类要抑制，不要强泛**。当 $k$ 很大而数据稀疏时，宁可丢掉 1~2% 的记录（输出 `*`）也不要发布不合规的类。本仓库未在 SDK 内置自动抑制，需在调用方出口做（参考 §3.3）。
6. **区分两套输出格式**（`[a, b]` vs `[a-b]`，前缀 `545*` vs 集合 `{a,b}`），否则前端图表分箱与正则解析会默默失效。
7. **与 DP 组合使用**：明细发布用 Mondrian，统计查询用 DP；报表中对同一张泛化表做聚合时，仍需记预算（泛化不等于 $\epsilon$-DP）。
8. **参数变更要进审计**。`k`、`qi_cols` 列表、层次表任一变化都会改变发布结果，应与数据版本一起写入审计（`services/audit-log`）。

### 反模式清单

| 反模式 | 后果 | 正例 |
|---|---|---|
| 传 `k=0` 或空 `qi_fields` | 拿到未泛化的原表（透传） | 先参数校验；用 `Mondrian` 得 fail-fast |
| 单条记录调 `AnonymizeRecord` 就认为“已 k-匿名” | 无任何集合保护 | 单条只能称“字段已泛化” |
| QI 字段名拼错 | 该列不泛化、不报错 | 比对 `BuiltinHierarchies` 并补自定义函数 |
| 拿 `result.K` 当合规证明 | 实际 $k$ 可能更小 | §3.3 自检 |
| 只对部分表做 Mondrian（分片独立泛化） | 跨片等价类被拆散 | 全集处理，或按 QI 预排序后分片 |
| 把 `gender` 放进 QI | level 1 即全 `*`（无信息量，白丢一维） | 性别作为 QI 仅在能与其他维度共同形成大类时使用 |

---

## 10. 测试、基准与 FAQ / Testing & Troubleshooting

### 10.1 测试资产

| 文件 | 覆盖 |
|---|---|
| `sdk/kano/kano_test.go` | `TestAnonymizeEmpty` `TestAnonymizeBasic` `TestAnonymizeSmallDataset` `TestIsNumeric` `TestUniqueValues` `TestCommonPrefix` `TestCheckDistinctLDiversity` |
| `sdk/kano/mondrian_test.go` | `TestMondrian_BasicNumeric` `TestMondrian_CategoricalQI` `TestMondrian_LargeK` `TestMondrian_EmptyInput` `TestMondrian_InvalidK` `TestMondrian_InsufficientRows` `TestMondrian_MissingQICol` `TestMondrian_AlignPython_SimpleTable` |
| `sdk/kano/hierarchy_test.go` | `TestAgeHierarchy` `TestZipcodeHierarchy` `TestSalaryHierarchy` `TestEducationHierarchy` `TestChooseLevel` `TestAnonymizeRecord` `TestAnonymizeRecord_CustomHierarchy` `TestAnonymizeRecord_InvalidK` `TestAnonymizeRecordBatch` + 5 个 `TestAlignPython_*Hierarchy` |
| 服务层 | `internal/service`、`internal/rest` 中的 K-匿名表级/DataFrame 用例 |

```bash
cd services/privacy-engine
CGO_ENABLED=0 go test ./sdk/kano/... -v
CGO_ENABLED=0 go test -race ./sdk/kano/...
go test -bench=. -benchmem ./sdk/kano/...
go test -cover ./sdk/kano/...
```

新增泛化逻辑时的最小测试清单：（a）空输入；（b）$n < k$；（c）全部值相同；（d）含脏数据/空串；（e）`qi_cols` 缺列；（f）泛化后**重新分组验证最小类规模 $\ge k$**。

### 10.2 故障排查

| 症状 | 根因 | 处置 |
|---|---|---|
| 输出与输入完全一致 | `k <= 0` 或 `qiFields` 为空触发透传 | 检查入参；改调 `Mondrian` 得错误 |
| 某列一直是原值 | 列名未命中 `BuiltinHierarchies` 且未传 `hierarchies` | 补自定义 `HierarchyFunc` 或从 `qi_cols` 移除并脱敏 |
| 大量记录变 `*` | 公共前缀为空（`commonPrefix`）或维度太多 | 减少 QI；先对邮编做区划级归一 |
| `group_count` 与预期不符 | 两个叶子的泛化值相同，去重后类数更小 | 用 `Mondrian` 的 `equivalence_classes_count` |
| 区间字样的解析报错 | 两套格式 `[a, b]` / `[a-b]` 混用 | 统一接口；解析器容两种格式 |
| 年龄是浮点却没被切分 | `int(range)` 截断为 0，该维度被当作无区分度 | 改用 `Mondrian`（`span()` 保留 `float64`） |
| `l`-多样性一直不过 | 敏感列大量空值，`DistinctCount=0` | 先治理缺失值；或按缺失单独归类 |
| `KANONYMIZE_FAILED` (500) | `Anonymize` 理论上不报错，多为上层转换异常 | 看 `internal/detail`；确认 `dataframe` 入参可 `fmt.Sprintf` |
| `KANONYMIZE_TABLE_FAILED` (400) | `k`、`qi_cols` 校验失败 | 按提示修参 |

相关文档：

- 差分隐私与预算记账：`docs/learning/tech-differential-privacy.md`
- 字段脱敏与国密假名化：`docs/learning/tech-data-processing.md`
- 医疗影像与记录流水线：`docs/learning/tech-medical-informatics-pipeline.md`
- 测试工程方法论：`docs/learning/tech-testing-benchmark-qa.md`
