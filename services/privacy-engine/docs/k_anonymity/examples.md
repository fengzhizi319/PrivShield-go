# K-匿名模块使用示例


## 1. 概述

本文档提供 `engine.privacy.kano`（单记录泛化）与 `engine.privacy.kano_table`（数据集级 Mondrian 泛化）的典型使用示例，以及对应的 REST API 调用方式。

## 2. Python SDK 示例

### 2.1 单记录 K-匿名泛化

使用内置的 `age`、`zipcode`、`gender` 泛化层次结构对单条记录进行泛化。

```python
from engine.privacy.kano import BUILTIN_HIERARCHIES, anonymize_record

record = {
    "name": "张三",
    "age": "28",
    "zipcode": "518057",
    "gender": "女",
    "disease": "胃癌",
}
qi_cols = ["age", "zipcode", "gender"]

result = anonymize_record(record, qi_cols, BUILTIN_HIERARCHIES, k=5)
print(result)
# {'name': '张三',
#  'age': '[25-30]',
#  'zipcode': '518***',
#  'gender': '*',
#  'disease': '胃癌'}
```

`k` 值越大，泛化粒度越粗：

```python
for k in [5, 12, 25]:
    print(f"k={k}: {anonymize_record(record, qi_cols, BUILTIN_HIERARCHIES, k)}")
```

### 2.2 数据集级 K-匿名泛化（Mondrian）

```python
from engine.privacy.kano_table import k_anonymize_table

rows = [
    {"age": 25, "zipcode": "100001", "gender": "M", "disease": "A"},
    {"age": 26, "zipcode": "100002", "gender": "M", "disease": "B"},
    {"age": 27, "zipcode": "100003", "gender": "M", "disease": "C"},
    {"age": 55, "zipcode": "200001", "gender": "F", "disease": "D"},
    {"age": 56, "zipcode": "200002", "gender": "F", "disease": "E"},
    {"age": 57, "zipcode": "200003", "gender": "F", "disease": "F"},
]
qi_cols = ["age", "zipcode", "gender"]

result = k_anonymize_table(rows, qi_cols, k=3, max_depth=10)
print(result)
```

输出示例：

```python
[
    {'age': '[25-27]', 'zipcode': '{100001,100002,100003}', 'gender': 'M', 'disease': 'A'},
    {'age': '[25-27]', 'zipcode': '{100001,100002,100003}', 'gender': 'M', 'disease': 'B'},
    {'age': '[25-27]', 'zipcode': '{100001,100002,100003}', 'gender': 'M', 'disease': 'C'},
    {'age': '[55-57]', 'zipcode': '{200001,200002,200003}', 'gender': 'F', 'disease': 'D'},
    {'age': '[55-57]', 'zipcode': '{200001,200002,200003}', 'gender': 'F', 'disease': 'E'},
    {'age': '[55-57]', 'zipcode': '{200001,200002,200003}', 'gender': 'F', 'disease': 'F'},
]
```

### 2.3 验证等价组大小

```python
from collections import Counter
from engine.privacy.kano_table import k_anonymize_table

result = k_anonymize_table(rows, qi_cols, k=3)
groups = Counter(
    (str(r["age"]), str(r["zipcode"]), str(r["gender"])) for r in result
)
assert all(c >= 3 for c in groups.values())
```

### 2.4 DataFrame 输入示例

```python
import pandas as pd
from engine.privacy.kano_table import k_anonymize_dataframe

df = pd.DataFrame({
    "age": [25, 26, 27, 55, 56, 57],
    "zipcode": ["100001", "100002", "100003", "200001", "200002", "200003"],
    "disease": ["A", "B", "C", "D", "E", "F"],
})

result = k_anonymize_dataframe(df, ["age", "zipcode"], k=3)
print(result)
```

SecretFlow 联邦数据（需安装 `secretflow`）：

```python
result = k_anonymize_dataframe(vdf, ["age", "zipcode"], k=3)
```

## 3. REST API 示例

### 3.1 单条记录泛化

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/k_anonymize/record \
  -H "Content-Type: application/json" \
  -d '{
    "record": {"age": "28", "zipcode": "518057", "gender": "女", "disease": "胃癌"},
    "qi_cols": ["age", "zipcode", "gender"],
    "k": 5
  }'
```

### 3.2 整张表泛化

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/k_anonymize/table \
  -H "Content-Type: application/json" \
  -d '{
    "rows": [
      {"age": 25, "zipcode": "100001", "gender": "M", "disease": "A"},
      {"age": 26, "zipcode": "100002", "gender": "M", "disease": "B"},
      {"age": 27, "zipcode": "100003", "gender": "M", "disease": "C"},
      {"age": 55, "zipcode": "200001", "gender": "F", "disease": "D"},
      {"age": 56, "zipcode": "200002", "gender": "F", "disease": "E"},
      {"age": 57, "zipcode": "200003", "gender": "F", "disease": "F"}
    ],
    "qi_cols": ["age", "zipcode", "gender"],
    "k": 3,
    "max_depth": 10
  }'
```

### 3.3 DataFrame 泛化

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/k_anonymize/dataframe \
  -H "Content-Type: application/json" \
  -d '{
    "data": [
      {"age": 25, "zipcode": "100001", "gender": "M", "disease": "A"},
      {"age": 26, "zipcode": "100002", "gender": "M", "disease": "B"},
      {"age": 27, "zipcode": "100003", "gender": "M", "disease": "C"}
    ],
    "qi_cols": ["age", "zipcode", "gender"],
    "k": 3,
    "max_depth": 10
  }'
```

## 4. 最佳实践

1. **准标识符选择**：仅将可公开组合后可能重识别的字段放入 `qi_cols`；敏感属性（如疾病、收入）通常不应作为 QI。
2. **k 值权衡**：医疗/基因数据建议 `5~10`，一般场景建议 `3~5`。k 越大隐私越强，但信息损失越大。
3. **max_depth 调优**：默认 `10` 适合大多数场景；列数多或数据量大时可适当增大，但过大会导致过度泛化。
4. **字段类型一致性**：表级接口对数值型 QI 输出区间、分类型 QI 输出集合；确保下游分析能正确解析这两种格式。
5. **敏感字段保护**：验证输出中非 QI 字段未被修改，避免意外泄露。
6. **DataFrame 工作流**：若数据已在 pandas/SecretFlow 中，可直接调用 `k_anonymize_dataframe`，无需手动转换为 records。

## 5. 常见错误

| 错误 | 原因 | 解决 |
|---|---|---|
| `Input table has N rows, but k-anonymity requires at least k` | 输入记录数少于 `k` | 增加数据量或降低 `k` |
| `qi_cols must not be empty` | 未指定准标识符 | 至少提供一个 QI 列名 |
| `qi_cols not found in rows: [...]` | 列名拼写错误或不存在于记录中 | 检查 `qi_cols` 与记录字段一致性 |
| 泛化过度 | `k` 过大或 `max_depth` 过小 | 调整参数平衡隐私与可用性 |
| 输出顺序变化 | Mondrian 算法会按 QI 排序 | 若需保持顺序，请在业务层按主键重新排序 |
| DataFrame 类型不支持 | 输入了不支持的 DataFrame 类型 | 确保使用 pandas 或 SecretFlow DataFrame |