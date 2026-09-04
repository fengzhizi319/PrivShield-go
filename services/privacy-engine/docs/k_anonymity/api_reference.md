# K-匿名模块 API 参考


## 1. Python SDK

### `k_anonymize_table`

位置：`engine.privacy.kano_table.k_anonymize_table`

对整张表执行 Mondrian 多维分区 K-匿名泛化。

```python
def k_anonymize_table(
    rows: List[Dict[str, Any]],
    qi_cols: List[str],
    k: int = 5,
    max_depth: int = 10,
) -> List[Dict[str, Any]]
```

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `rows` | `List[Dict[str, Any]]` | 是 | 原始记录列表 |
| `qi_cols` | `List[str]` | 是 | 准标识符列名列表 |
| `k` | `int` | 否 | K-匿名阈值，每个等价组至少包含 `k` 条记录，默认 `5` |
| `max_depth` | `int` | 否 | 最大递归深度，默认 `10` |

**返回值**：泛化后的记录列表（顺序可能与输入不同）。

**异常**：
- `ValueError`: 输入记录数不足 `k`。
- `ValueError`: `qi_cols` 包含输入中不存在的列。
- `ValueError`: `qi_cols` 为空。

**泛化规则**：
- 数值型 QI：输出区间字符串，如 `[25-30]`；当组内取值全部相同时保持原值。
- 分类型 QI：输出取值集合字符串，如 `{M,F}`；当组内取值全部相同时保持原值。
- 非 QI 字段：原样保留。

---

### `k_anonymize_dataframe`

位置：`engine.privacy.kano_table.k_anonymize_dataframe`

对 DataFrame 执行 Mondrian 多维分区 K-匿名泛化。

```python
def k_anonymize_dataframe(
    df: Any,
    qi_cols: List[str],
    k: int = 5,
    max_depth: int = 10,
) -> pd.DataFrame
```

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `df` | `Any` | 是 | 输入 DataFrame（pandas 或 SecretFlow） |
| `qi_cols` | `List[str]` | 是 | 准标识符列名列表 |
| `k` | `int` | 否 | K-匿名阈值，默认 `5` |
| `max_depth` | `int` | 否 | 最大递归深度，默认 `10` |

**返回值**：泛化后的 pandas DataFrame。

---

### `anonymize_record`

位置：`engine.privacy.kano.anonymize_record`

对单条记录按内置泛化层次结构进行启发式泛化。

```python
def anonymize_record(
    record: Dict[str, Any],
    qi_cols: List[str],
    hierarchies: Dict[str, GeneralizationHierarchy],
    k: int,
) -> Dict[str, Any]
```

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `record` | `Dict[str, Any]` | 是 | 原始记录字典 |
| `qi_cols` | `List[str]` | 是 | 准标识符列名列表 |
| `hierarchies` | `Dict[str, GeneralizationHierarchy]` | 是 | 列名到泛化层次函数的映射 |
| `k` | `int` | 是 | K-匿名参数，用于决定泛化层级 |

**返回值**：泛化后的新记录字典（不修改原始字典）。

> 当前 MVP 版本主要依赖 `BUILTIN_HIERARCHIES`；自定义层次结构参数已预留，但尚未实际合并到内置层次中。

---

### `BUILTIN_HIERARCHIES`

位置：`engine.privacy.kano.BUILTIN_HIERARCHIES`

内置准标识符泛化层次结构映射表，包含 `age`、`zipcode`、`gender` 三个字段。

| 字段 | 层级 | 说明 |
|---|---|---|
| `age` | 0 | 原始值 |
| | 1 | 5 岁区间，如 `[25-30]` |
| | 2 | 10 岁区间，如 `[20-30]` |
| | 3 | 20 岁区间，如 `[20-40]` |
| | ≥4 | `*` |
| `zipcode` | 0 | 原始值 |
| | 1 | 保留前 3 位，如 `518***` |
| | 2 | 保留前 2 位，如 `51****` |
| | 3 | 保留前 1 位，如 `5*****` |
| | ≥4 或长度不足 | `*` |
| `gender` | 0 | 原始值 |
| | ≥1 | `*` |

泛化层级由 `choose_level(k, max_level)` 启发式决定：`level = max(1, min(k // 5, max_level))`。

---

## 2. REST API

### POST `/v1/privacy/k_anonymize/record`

单条记录 K-匿名泛化。

请求体：

```json
{
  "record": {"age": "28", "zipcode": "518057", "gender": "女", "disease": "胃癌"},
  "qi_cols": ["age", "zipcode", "gender"],
  "k": 5
}
```

响应体：

```json
{
  "result": {
    "age": "[25-30]",
    "zipcode": "518***",
    "gender": "*",
    "disease": "胃癌"
  }
}
```

### POST `/v1/privacy/k_anonymize/table`

整张表 K-匿名泛化。

请求体：

```json
{
  "rows": [
    {"age": 25, "zipcode": "100001", "gender": "M", "disease": "A"},
    {"age": 26, "zipcode": "100002", "gender": "M", "disease": "B"},
    {"age": 27, "zipcode": "100003", "gender": "M", "disease": "C"}
  ],
  "qi_cols": ["age", "zipcode", "gender"],
  "k": 3,
  "max_depth": 10
}
```

响应体：

```json
{
  "result": [
    {"age": "[25-27]", "zipcode": "{100001,100002,100003}", "gender": "M", "disease": "A"},
    {"age": "[25-27]", "zipcode": "{100001,100002,100003}", "gender": "M", "disease": "B"},
    {"age": "[25-27]", "zipcode": "{100001,100002,100003}", "gender": "M", "disease": "C"}
  ]
}
```

---

## 3. gRPC API

### 方法列表

| 方法 | 请求 | 响应 | 说明 |
|---|---|---|---|
| `KAnonymizeRecord` | `KAnonymizeRequest` | `KAnonymizeResponse` | 单条记录泛化 |
| `KAnonymizeTable` | `KAnonymizeTableRequest` | `KAnonymizeTableResponse` | 整张表泛化 |
| `KAnonymizeDataFrame` | `KAnonymizeDataFrameRequest` | `KAnonymizeDataFrameResponse` | DataFrame 泛化 |

### `KAnonymizeRequest` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `record` | `map<string, string>` | 原始记录 |
| `qi_cols` | `repeated string` | 准标识符列名 |
| `k` | `int32` | K-匿名阈值 |

### `KAnonymizeResponse` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `result` | `map<string, string>` | 泛化后的记录 |

### `KAnonymizeTableRequest` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `rows` | `repeated RecordEntry` | 原始记录列表 |
| `qi_cols` | `repeated string` | 准标识符列名 |
| `k` | `int32` | K-匿名阈值 |
| `max_depth` | `int32` | 最大递归深度 |

### `KAnonymizeTableResponse` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `rows` | `repeated RecordEntry` | 泛化后的记录列表 |

### `KAnonymizeDataFrameRequest` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `data` | `repeated RecordEntry` | 原始记录列表 |
| `qi_cols` | `repeated string` | 准标识符列名 |
| `k` | `int32` | K-匿名阈值 |
| `max_depth` | `int32` | 最大递归深度 |

### `KAnonymizeDataFrameResponse` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `data` | `repeated RecordEntry` | 泛化后的记录列表 |

### `RecordEntry` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `fields` | `map<string, string>` | 单条记录的字段键值对 |

---

## 4. 异常与错误码

| 异常/错误 | 触发条件 | HTTP 状态码 | gRPC 状态码 |
|---|---|---|---|
| `ValueError: Input table has N rows, but k-anonymity requires at least k` | 表级输入记录数 < `k` | 400 | `INVALID_ARGUMENT` |
| `ValueError: qi_cols must not be empty` | `qi_cols` 为空 | 400 | `INVALID_ARGUMENT` |
| `ValueError: qi_cols not found in rows: [...]` | `qi_cols` 包含不存在列 | 400 | `INVALID_ARGUMENT` |
| `TypeError: Unsupported table input type: ...` | DataFrame 输入类型不支持 | 400 | `INVALID_ARGUMENT` |

---

## 5. 使用场景与参数建议

### 5.1 典型应用场景

#### 场景 1：医疗数据发布前的脱敏处理（推荐数据集级 K-匿名）✅

**背景**：医院需要将患者病历数据共享给研究机构进行统计分析，同时保护患者隐私。

**推荐配置**：
- **实现方式**：数据集级 K-匿名（Mondrian 算法）
- **K 值**：5~10（医疗数据高度敏感）
- **准标识符**：`["age", "zipcode", "gender"]`
- **敏感属性**：`disease`（不作为 QI，保留原值用于分析）
- **max_depth**：10（默认值）

**示例**：
```python
from engine.privacy.kano_table import k_anonymize_table
import pandas as pd

# 原始患者数据
patients = [
    {"patient_id": "P001", "age": 28, "zipcode": "100001", "gender": "M", "disease": "糖尿病"},
    {"patient_id": "P002", "age": 35, "zipcode": "100002", "gender": "F", "disease": "高血压"},
    {"patient_id": "P003", "age": 42, "zipcode": "100003", "gender": "M", "disease": "冠心病"},
    # ... 更多记录（至少需要 k 条）
]

# 执行数据集级 K-匿名
anonymized_data = k_anonymize_table(
    rows=patients,
    qi_cols=["age", "zipcode", "gender"],
    k=5,
    max_depth=10
)

# 验证等价组大小
from collections import Counter
groups = Counter(
    (str(r["age"]), str(r["zipcode"]), str(r["gender"])) 
    for r in anonymized_data
)
print(f"等价组数量: {len(groups)}")
print(f"最小等价组大小: {min(groups.values())}")
assert min(groups.values()) >= 5, "不满足 5-匿名要求！"
```

**输出示例**：
```python
[
    {
        "patient_id": "P001",
        "age": "[25-35]",           # 数值型区间泛化
        "zipcode": "{100001,100002}",  # 分类型集合泛化
        "gender": "{M,F}",
        "disease": "糖尿病"         # 敏感属性保持不变
    },
    # ... 其他记录
]
```

**注意事项**：
- ✅ **严格保证**：每个等价组至少包含 5 条记录，重识别概率 ≤ 1/5 = 20%
- ⚠️ **数据量要求**：输入记录数必须 ≥ k，否则抛出异常
- ⚠️ **信息损失**：泛化后无法恢复原始值，需评估对下游分析的影响
- 💡 **最佳实践**：先在小样本上测试，调整 k 值平衡隐私与可用性

---

#### 场景 2：金融客户数据共享（合规驱动）

**背景**：银行需要向监管机构或合作伙伴提供客户统计数据，满足 GDPR/个人信息保护法要求。

**推荐配置**：
- **实现方式**：数据集级 K-匿名 + 差分隐私（纵深防御）
- **K 值**：5~8
- **准标识符**：`["age_range", "city", "income_bracket"]`
- **敏感属性**：`account_balance`, `credit_score`（可考虑额外脱敏）
- **max_depth**：12（字段较多时适当增加）

**示例**：
```python
from engine.privacy.kano_table import k_anonymize_dataframe
import pandas as pd

# 从数据库加载客户数据
df = pd.read_sql("SELECT * FROM customers WHERE active = 1", connection)

# 预处理：将连续值离散化（可选，提高泛化效果）
df["age_range"] = pd.cut(df["age"], bins=[0, 25, 35, 50, 65, 100], labels=True)
df["income_bracket"] = pd.cut(df["income"], bins=[0, 50000, 100000, 200000, 500000], labels=True)

# 执行 K-匿名
anonymized_df = k_anonymize_dataframe(
    df=df,
    qi_cols=["age_range", "city", "income_bracket"],
    k=5,
    max_depth=12
)

# 导出为 CSV 供外部使用
anonymized_df.to_csv("customers_anonymized.csv", index=False)
```

**组合差分隐私**：
```python
from engine.privacy.dp import DPApi

# 在 K-匿名基础上，对聚合统计再加一层 DP 噪声
dp = DPApi(namespace="customer_stats")

# 计算各城市的平均收入（带 DP 噪声）
for city in anonymized_df["city"].unique():
    city_data = anonymized_df[anonymized_df["city"] == city]
    noisy_avg_income = dp.mean(
        values=city_data["income"].tolist(),
        epsilon=1.0,
        delta=1e-6,
        mechanism="gaussian",
        clip_lower=0.0,
        clip_upper=500000.0
    )
    print(f"{city}: 平均收入 ≈ {noisy_avg_income:.2f}")
```

**注意事项**：
- ✅ **合规优势**：K-匿名 + DP 形成多层防护，更容易通过合规审查
- ⚠️ **性能考虑**：大数据集（>10万行）可能需要较长时间，建议使用 Pandas 向量化优化
- 💡 **字段选择**：避免将唯一性高的字段（如身份证号、手机号）放入 QI

---

#### 场景 3：实时流式数据处理（单条记录泛化）⚠️

**背景**：物联网设备或用户行为日志需要实时上报，无法等待完整数据集累积。

**推荐配置**：
- **实现方式**：单条记录启发式泛化（`anonymize_record`）
- **K 值**：仅用于控制泛化强度，无统计学意义
- **准标识符**：`["timestamp_bucket", "device_type", "location_zone"]`
- **泛化层次**：使用内置 `BUILTIN_HIERARCHIES` 或自定义

**示例**：
```python
from engine.privacy.kano import anonymize_record, BUILTIN_HIERARCHIES
import time

def process_stream_event(event):
    """处理单个流式事件"""
    record = {
        "user_id": event["user_id"],
        "age": str(event["age"]),
        "zipcode": event["zipcode"],
        "gender": event["gender"],
        "action": event["action"],
        "timestamp": int(time.time())
    }
    
    # 单条记录泛化（注意：不保证真正的 K-匿名）
    anonymized = anonymize_record(
        record=record,
        qi_cols=["age", "zipcode", "gender"],
        hierarchies=BUILTIN_HIERARCHIES,
        k=5  # 这里的 k 仅决定泛化层级
    )
    
    # 发送到消息队列
    kafka_producer.send("anonymized_events", value=anonymized)
    return anonymized

# 模拟流式处理
for event in event_stream:
    process_stream_event(event)
```

**改进方案：微批处理**（推荐）：
```python
from engine.privacy.kano_table import k_anonymize_table

class MicroBatchProcessor:
    def __init__(self, batch_size=100, timeout_seconds=10):
        self.batch_size = batch_size
        self.timeout = timeout_seconds
        self.buffer = []
        self.last_flush = time.time()
    
    def add_record(self, record):
        self.buffer.append(record)
        
        # 触发条件：达到批量大小或超时
        if len(self.buffer) >= self.batch_size or \
           time.time() - self.last_flush > self.timeout:
            self.flush()
    
    def flush(self):
        if not self.buffer:
            return
        
        # 对批次执行数据集级 K-匿名
        anonymized_batch = k_anonymize_table(
            rows=self.buffer,
            qi_cols=["age", "zipcode", "gender"],
            k=5
        )
        
        # 批量发送
        for record in anonymized_batch:
            kafka_producer.send("anonymized_events", value=record)
        
        self.buffer.clear()
        self.last_flush = time.time()

# 使用微批处理器
processor = MicroBatchProcessor(batch_size=100)
for event in event_stream:
    processor.add_record(event)
```

**注意事项**：
- ❌ **单条记录泛化的局限性**：无法验证是否真正满足 K-匿名，可能过度或不足泛化
- ✅ **微批处理优势**：在低延迟（秒级）和严格 K-匿名之间取得平衡
- ⚠️ **缓冲策略**：根据业务容忍延迟调整 `batch_size` 和 `timeout`
- 💡 **混合方案**：实时场景用单条泛化 + 定期重新运行数据集级算法修正

---

#### 场景 4：科研数据发布（学术合作）

**背景**：大学研究团队需要将调查数据公开发布，供其他学者复现研究。

**推荐配置**：
- **实现方式**：数据集级 K-匿名
- **K 值**：3~5（平衡可用性与隐私）
- **准标识符**：`["education_level", "occupation_category", "region"]`
- **敏感属性**：`income`, `political_affiliation`（考虑额外处理）
- **max_depth**：8（减少过度泛化）

**示例**：
```python
from engine.privacy.kano_table import k_anonymize_table
import json

# 加载调查数据
with open("survey_responses.json") as f:
    survey_data = json.load(f)

# 定义准标识符（排除敏感属性）
qi_cols = ["age_group", "education", "occupation", "region"]

# 执行 K-匿名
anonymized_survey = k_anonymize_table(
    rows=survey_data,
    qi_cols=qi_cols,
    k=3,
    max_depth=8
)

# 保存并发布
with open("survey_anonymized.json", "w") as f:
    json.dump(anonymized_survey, f, indent=2, ensure_ascii=False)

# 生成数据字典说明泛化规则
data_dictionary = {
    "age_group": "区间泛化，如 [25-30]",
    "education": "集合泛化，如 {Bachelor,Master}",
    "occupation": "职业类别保持不变（若组内一致）",
    "region": "地理区域集合，如 {Beijing,Shanghai}"
}
print("数据字典:", json.dumps(data_dictionary, indent=2))
```

**验证报告**：
```python
from collections import Counter

def generate_privacy_report(anonymized_data, qi_cols, k):
    """生成隐私保护报告"""
    groups = Counter(
        tuple(str(r[col]) for col in qi_cols)
        for r in anonymized_data
    )
    
    report = {
        "total_records": len(anonymized_data),
        "num_equivalence_classes": len(groups),
        "min_group_size": min(groups.values()),
        "max_group_size": max(groups.values()),
        "avg_group_size": sum(groups.values()) / len(groups),
        "k_anonymity_satisfied": min(groups.values()) >= k,
        "group_size_distribution": dict(sorted(Counter(groups.values()).items()))
    }
    
    return report

report = generate_privacy_report(anonymized_survey, qi_cols, k=3)
print(json.dumps(report, indent=2))
# {
#   "total_records": 1000,
#   "num_equivalence_classes": 150,
#   "min_group_size": 3,
#   "max_group_size": 12,
#   "avg_group_size": 6.67,
#   "k_anonymity_satisfied": true,
#   "group_size_distribution": {"3": 45, "4": 38, "5": 32, ...}
# }
```

**注意事项**：
- ✅ **透明度**：发布时附带隐私保护报告，增强数据可信度
- ⚠️ **同质性攻击**：若等价组内敏感属性值相同（如所有成员都患同种疾病），仍需泄露风险
- 💡 **补充措施**：考虑 l-多样性或 t-紧密性（当前未实现）

---

#### 场景 5：政府统计数据公开（人口普查）

**背景**：统计局需要发布人口普查汇总数据，同时防止个体重识别。

**推荐配置**：
- **实现方式**：数据集级 K-匿名 + 分层抽样
- **K 值**：10~20（公共数据高隐私要求）
- **准标识符**：`["age_decade", "province", "ethnicity", "education"]`
- **敏感属性**：`income`, `household_size`
- **max_depth**：15（多维度复杂数据）

**示例**：
```python
from engine.privacy.kano_table import k_anonymize_dataframe
import pandas as pd

# 加载普查数据（假设已清洗）
census_df = pd.read_csv("census_2024_raw.csv")

# 预处理：创建年龄 decade
census_df["age_decade"] = (census_df["age"] // 10) * 10

# 分层 K-匿名：按省份分别处理（避免跨省泛化导致信息损失过大）
anonymized_chunks = []
for province, group_df in census_df.groupby("province"):
    if len(group_df) < 10:  # 跳过小样本省份
        continue
    
    anonymized_province = k_anonymize_dataframe(
        df=group_df,
        qi_cols=["age_decade", "ethnicity", "education"],
        k=10,
        max_depth=15
    )
    anonymized_chunks.append(anonymized_province)

# 合并结果
census_anonymized = pd.concat(anonymized_chunks, ignore_index=True)
census_anonymized.to_csv("census_2024_anonymized.csv", index=False)

print(f"总记录数: {len(census_anonymized)}")
print(f"省份数量: {census_anonymized['province'].nunique()}")
```

**注意事项**：
- ✅ **分层处理**：避免跨差异大的子群体泛化，降低信息损失
- ⚠️ **小样本问题**：某些细分群体可能记录数 < k，需特殊处理（合并或删除）
- 💡 **多级发布**：可发布不同 k 值的多个版本（k=5 供研究用，k=20 供公众用）

---

#### 场景 6：企业内部数据分析（HR 薪酬分析）

**背景**：HR 部门需要分析各部门薪酬分布，但需保护员工个人隐私。

**推荐配置**：
- **实现方式**：数据集级 K-匿名
- **K 值**：3~5（内部使用可适当降低）
- **准标识符**：`["department", "job_level", "years_of_service"]`
- **敏感属性**：`salary`, `bonus`（可作为非 QI 保留）
- **max_depth**：10

**示例**：
```python
from engine.privacy.kano_table import k_anonymize_dataframe
import pandas as pd

# HR 数据
hr_df = pd.read_excel("employee_data.xlsx")

# 选择准标识符（排除直接标识符如 employee_id, name）
qi_cols = ["department", "job_level", "age_range", "gender"]

# 执行 K-匿名
hr_anonymized = k_anonymize_dataframe(
    df=hr_df,
    qi_cols=qi_cols,
    k=5,
    max_depth=10
)

# 后续分析：各部门平均薪酬
salary_analysis = hr_anonymized.groupby("department")["salary"].agg(["mean", "median", "std"])
print(salary_analysis)

# 可视化
import matplotlib.pyplot as plt
salary_analysis["mean"].plot(kind="bar")
plt.title("Average Salary by Department (K-Anonymized)")
plt.ylabel("Salary")
plt.tight_layout()
plt.savefig("salary_by_dept.png")
```

**注意事项**：
- ✅ **实用性**：非 QI 字段（如 salary）保持原值，支持后续统计分析
- ⚠️ **内部威胁**：即使 K-匿名，内部人员仍可能结合其他信息重识别，需配合访问控制
- 💡 **动态更新**：员工入职/离职时需重新运行 K-匿名，避免增量更新破坏隐私保证

---

### 5.2 参数选择指南

#### K 值选择

| 数据类型 | 推荐 k 范围 | 重识别概率上限 | 适用场景 |
|---------|-----------|--------------|---------|
| 极高敏感（基因、心理健康） | 10~20 | ≤ 5%~10% | 医疗研究、遗传数据 |
| 高敏感（医疗诊断、金融征信） | 5~10 | ≤ 10%~20% | 病历分析、信用评分 |
| 中等敏感（用户行为、位置轨迹） | 3~5 | ≤ 20%~33% | APP 使用统计、出行模式 |
| 低敏感（公开调查、满意度） | 2~3 | ≤ 33%~50% | 市场调研、产品反馈 |

**选择原则**：
- **k 越大**：隐私保护越强，但信息损失越大，数据可用性越低
- **k 越小**：数据更精确，但重识别风险更高
- **合规要求**：GDPR、HIPAA 等法规可能有最低 k 值要求
- **数据量约束**：k 不能超过数据集大小，且建议 k << n（n 为记录数）

**经验公式**：
```python
# 基于数据量的启发式选择
if n < 100:
    k = 2  # 小数据集只能做弱匿名
elif n < 1000:
    k = 3~5
elif n < 10000:
    k = 5~10
else:
    k = 10~20  # 大数据集可承受更高 k
```

---

#### 准标识符（QI）选择

**什么是准标识符**：
- 单独不足以识别个体，但组合后可唯一或接近唯一确定个体的属性
- 例如：`{年龄, 邮编, 性别}` 组合在美国可唯一识别 87% 的人口（Sweeney, 2000）

**选择标准**：
1. **可公开性**：攻击者可能从外部数据源获取的属性
2. **区分度**：能有效区分不同个体的属性
3. **稳定性**：不易频繁变化的属性

**常见准标识符**：
| 字段类型 | 示例 | 区分度 |
|---------|------|-------|
| 人口统计 | 年龄、性别、种族 | 高 |
| 地理位置 | 邮编、城市、街区 | 高 |
| 时间相关 | 出生日期、入职日期 | 中 |
| 社会经济 | 教育程度、职业、收入等级 | 中 |
| 行为特征 | 设备类型、浏览器指纹 | 高 |

**不应作为 QI 的字段**：
- ❌ **直接标识符**：姓名、身份证号、手机号、邮箱（应直接删除或哈希）
- ❌ **敏感属性**：疾病、政治倾向、宗教信仰（通常保留原值用于分析）
- ❌ **唯一值字段**：订单号、交易 ID（每个值唯一，泛化无意义）

**QI 数量影响**：
- QI 越多 → 等价组越细 → 需要更大的 k 或更深的泛化
- 建议：仅选择必要的 QI，避免过度泛化

**示例：QI 选择对比**：
```python
# 方案 A：过多 QI（可能导致过度泛化）
qi_cols_a = ["age", "zipcode", "gender", "occupation", "education", "marital_status"]
# 结果：可能产生大量小的等价组，泛化严重

# 方案 B：精简 QI（推荐）
qi_cols_b = ["age", "zipcode", "gender"]
# 结果：平衡隐私与可用性
```

---

#### Max_depth 调优

**作用**：限制 Mondrian 算法的递归深度，防止过度分割导致泛化过粗。

**推荐值**：
| 数据特征 | 推荐 max_depth | 说明 |
|---------|---------------|------|
| 简单数据（2-3 个 QI） | 8~10 | 默认值适用大多数场景 |
| 中等复杂（4-6 个 QI） | 10~15 | 需要更多分割轮次 |
| 高维数据（7+ 个 QI） | 15~20 | 谨慎使用，监控信息损失 |
| 小数据集（n < 500） | 5~8 | 避免过度分割 |

**调优方法**：
```python
# 测试不同 max_depth 的效果
for depth in [5, 8, 10, 15]:
    result = k_anonymize_table(rows, qi_cols, k=5, max_depth=depth)
    
    # 计算信息损失指标
    groups = Counter(tuple(str(r[col]) for col in qi_cols) for r in result)
    avg_group_size = len(result) / len(groups)
    
    print(f"max_depth={depth}: 等价组数={len(groups)}, 平均组大小={avg_group_size:.2f}")

# 选择原则：
# - 等价组数适中（不要太多也不要太少）
# - 平均组大小接近 k（说明泛化适度）
```

**观察现象**：
- **max_depth 过小**：等价组过大，泛化过度，信息损失严重
- **max_depth 过大**：可能产生过多小等价组，违反 k-匿名或泛化不均匀
- **理想状态**：等价组大小分布在 `[k, 2k]` 范围内

---

#### 泛化层次结构（Hierarchies）

**内置层次**（`BUILTIN_HIERARCHIES`）：

| 字段 | 层级 0 | 层级 1 | 层级 2 | 层级 3 | 层级 ≥4 |
|-----|--------|--------|--------|--------|---------|
| age | 25 | [25-30] | [20-30] | [20-40] | * |
| zipcode | 518057 | 518*** | 51**** | 5***** | * |
| gender | M/F | * | - | - | - |

**自定义层次**（当前 MVP 版本预留接口，尚未完全支持）：
```python
from engine.privacy.kano import GeneralizationHierarchy

# 示例：自定义年龄泛化层次
def custom_age_hierarchy(value: str, level: int) -> str:
    age = int(value)
    if level == 0:
        return value
    elif level == 1:
        # 10 岁区间
        decade = (age // 10) * 10
        return f"[{decade}-{decade+9}]"
    elif level == 2:
        # 20 岁区间
        bracket = (age // 20) * 20
        return f"[{bracket}-{bracket+19}]"
    else:
        return "*"

# TODO: 未来版本支持传入自定义 hierarchies
# hierarchies = {"age": custom_age_hierarchy}
# result = anonymize_record(record, qi_cols, hierarchies, k=5)
```

**注意事项**：
- 当前 `anonymize_record` 主要使用内置层次
- 数据集级 K-匿名（`k_anonymize_table`）自动推导区间/集合，不依赖预定义层次
- 如需自定义泛化规则，建议在后处理阶段转换

---

### 5.3 性能优化建议

#### 大数据集处理

**性能瓶颈**：
- Mondrian 算法复杂度 O(n log² n)
- 每次递归需排序，纯 Python 实现较慢

**优化方案**：
1. **使用 Pandas 向量化**（自动启用）：
   ```python
   # 确保安装了 pandas
   pip install pandas
   
   # 直接使用 DataFrame 接口
   from engine.privacy.kano_table import k_anonymize_dataframe
   result = k_anonymize_dataframe(df, qi_cols, k=5)  # 内部自动使用向量化优化
   ```

2. **分批处理**：
   ```python
   # 对于超大数据集（>100万行），按自然分组分批
   chunks = []
   for region, chunk_df in large_df.groupby("region"):
       anonymized_chunk = k_anonymize_dataframe(chunk_df, qi_cols, k=5)
       chunks.append(anonymized_chunk)
   
   final_result = pd.concat(chunks)
   ```

3. **采样测试**：
   ```python
   # 先在 10% 样本上测试参数
   sample_df = df.sample(frac=0.1, random_state=42)
   test_result = k_anonymize_dataframe(sample_df, qi_cols, k=5)
   
   # 验证效果后，再在全量数据上运行
   full_result = k_anonymize_dataframe(df, qi_cols, k=5)
   ```

**性能对比**（参考值）：
| 数据规模 | 纯 Python | Pandas 向量化 | 加速比 |
|---------|----------|--------------|--------|
| 1,000 行 | ~0.1s | ~0.05s | 2x |
| 10,000 行 | ~2s | ~0.3s | 6x |
| 100,000 行 | ~30s | ~3s | 10x |
| 1,000,000 行 | ~5min | ~30s | 10x |

---

#### 内存优化

**内存占用估算**：
- 输入数据：O(n × m)，n 为记录数，m 为字段数
- 递归栈：O(log(n/k)) 层
- 中间结果：每层需复制部分数据

**优化技巧**：
```python
# 1. 删除不必要的列
essential_cols = qi_cols + sensitive_cols + ["id"]
df_reduced = df[essential_cols].copy()

# 2. 使用合适的数据类型
df["age"] = df["age"].astype("int16")  # 而非 int64
df["gender"] = df["gender"].astype("category")

# 3. 流式写入结果（避免一次性加载全部结果）
with open("output.csv", "w") as f:
    for chunk in process_in_chunks(large_df, chunk_size=10000):
        anonymized_chunk = k_anonymize_dataframe(chunk, qi_cols, k=5)
        anonymized_chunk.to_csv(f, mode="a", header=f.tell()==0)
```

---

### 5.4 安全注意事项

#### 同质性攻击（Homogeneity Attack）

**风险描述**：
- 若等价组内所有记录的敏感属性值相同，攻击者虽无法确定具体个体，但可推断该组所有人的敏感值
- 例如：某等价组 5 人都患"艾滋病"，则组内任何人的患病状态都被泄露

**缓解措施**：
1. **l-多样性**（当前未实现）：要求每个等价组至少有 l 个不同的敏感值
2. **手动检查**：
   ```python
   from collections import Counter
   
   def check_homogeneity(anonymized_data, qi_cols, sensitive_col, l=2):
       """检查是否存在同质性等价组"""
       groups = {}
       for record in anonymized_data:
           key = tuple(str(record[col]) for col in qi_cols)
           if key not in groups:
               groups[key] = []
           groups[key].append(record[sensitive_col])
       
       risky_groups = []
       for key, values in groups.items():
           unique_values = len(set(values))
           if unique_values < l:
               risky_groups.append({
                   "qi_combination": key,
                   "size": len(values),
                   "unique_sensitive_values": unique_values
               })
       
       return risky_groups
   
   risky = check_homogeneity(result, qi_cols, "disease", l=2)
   if risky:
       print(f"警告：发现 {len(risky)} 个存在同质性风险的等价组")
       for group in risky[:5]:  # 显示前 5 个
           print(group)
   ```

3. **抑制高风险记录**：删除或进一步泛化存在同质性的等价组

---

#### 背景知识攻击（Background Knowledge Attack）

**风险描述**：
- 攻击者拥有额外背景知识，可缩小等价组内的候选范围
- 例如：知道目标"是男性且住在某邮编区"，可将候选从 5 人缩小到 2 人

**缓解措施**：
1. **增加 k 值**：提高等价组大小，增加不确定性
2. **减少 QI**：移除攻击者可能知道的属性
3. **添加噪声**：结合差分隐私对敏感属性加噪

---

#### 去匿名化风险

**风险场景**：
- 攻击者拥有外部数据源（如选民登记表、社交媒体），可与匿名数据交叉匹配

**防护措施**：
1. **删除直接标识符**：姓名、ID、联系方式等
2. **泛化高精度字段**：如将精确生日泛化为出生年份
3. **审计数据接收方**：签署保密协议，限制二次传播
4. **水印技术**：在数据中嵌入隐形标记，追踪泄露源头

---

### 5.5 故障排查速查表

| 现象 | 可能原因 | 解决方案 |
|------|---------|---------|
| `Input table has N rows, but k-anonymity requires at least k` | 数据量不足 | 增加数据或降低 k 值 |
| `qi_cols must not be empty` | 未指定准标识符 | 至少提供一个 QI 列名 |
| `qi_cols not found in rows: [...]` | 列名拼写错误 | 检查字段名一致性 |
| 泛化过度（所有值变为 *） | k 过大或 max_depth 过小 | 降低 k 或增加 max_depth |
| 等价组大小远大于 k | max_depth 过小或数据分布不均 | 增加 max_depth 或分层处理 |
| 输出顺序变化 | Mondrian 按 QI 排序 | 按主键重新排序 |
| 处理速度慢 | 数据量大且未使用 Pandas | 安装 pandas 启用向量化优化 |
| 内存溢出 | 数据集过大 | 分批处理或增加内存 |
| 敏感属性被泛化 | 误将敏感字段加入 qi_cols | 从 qi_cols 中移除敏感字段 |
| 单条记录泛化无效 | 期望真正的 K-匿名 | 改用数据集级 K-匿名 |

---

### 5.6 与其他隐私技术的对比

| 技术 | 隐私保证 | 数据效用 | 适用场景 | 组合建议 |
|------|---------|---------|---------|---------|
| **K-匿名** | 中等（启发式） | 较高 | 数据发布、共享 | + 差分隐私 |
| **差分隐私** | 最强（数学证明） | 中等（有噪声） | 统计查询、模型训练 | + K-匿名 |
| **数据脱敏** | 较弱（可逆风险） | 高 | 测试数据、开发环境 | 单独使用 |
| **L-多样性** | 较强（解决同质性） | 中等 | 医疗数据发布 | 替代 K-匿名 |
| **T-紧密性** | 强（解决背景知识） | 较低 | 高敏感数据 | 高级场景 |

**组合策略**：
1. **K-匿名 + 差分隐私**：
   - 先对数据进行 K-匿名泛化
   - 再对聚合统计注入 DP 噪声
   - 适用于：数据发布 + 统计分析

2. **K-匿名 + 访问控制**：
   - K-匿名处理后存储
   - 通过 RBAC/ABAC 限制访问权限
   - 适用于：企业内部数据共享

3. **微批 K-匿名 + 实时脱敏**：
   - 实时流用单条记录泛化
   - 定期（每小时/每天）重新运行数据集级 K-匿名修正
   - 适用于：IoT、用户行为日志

---

## 6. 最佳实践总结

1. **优先使用数据集级 K-匿名**：它是唯一能提供严格 K-匿名保证的实现
2. **合理选择 k 值**：根据数据敏感度和合规要求，通常在 3~10 之间
3. **精简准标识符**：仅选择必要的 QI，避免过度泛化
4. **验证等价组大小**：确保所有等价组 ≥ k，生成隐私报告
5. **检查同质性风险**：避免等价组内敏感属性单一
6. **使用 Pandas 优化**：大数据集务必安装 pandas 启用向量化
7. **分层处理异构数据**：按自然分组（如地区、部门）分别匿名化
8. **微批处理流式数据**：平衡实时性与隐私保证
9. **结合其他隐私技术**：形成纵深防御（K-匿名 + DP + 访问控制）
10. **文档化与审计**：记录参数选择理由，保存隐私保护报告