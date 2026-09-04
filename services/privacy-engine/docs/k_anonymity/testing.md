# K-匿名模块测试文档


## 1. 概述

本文档定义 `engine/privacy/kano.py` 与 `engine/privacy/kano_table.py` 的测试策略、测试范围与可执行示例。K-匿名模块的测试需覆盖单记录泛化正确性、Mondrian 算法正确性、等价组大小约束、敏感字段不变性以及 REST/gRPC 接口一致性。

## 2. 测试目标

- 验证内置泛化层次函数（`age_hierarchy`、`zipcode_hierarchy`、`gender_hierarchy`）按预期输出。
- 验证 `anonymize_record` 不修改原始记录，且仅泛化 `qi_cols` 中列出的字段。
- 验证 `k_anonymize_table` 输出的每个等价组大小均 ≥ `k`。
- 验证非 QI 敏感字段在泛化后保持不变。
- 验证数值型 QI 泛化为区间、分类型 QI 泛化为取值集合。
- 验证 REST/gRPC 接口参数透传无误。
- 验证 DataFrame 输入/输出正确性。
- 验证 `privacy_kano_operations_total` 指标正确递增。
- 验证边界条件：空输入、记录数 < `k`、`qi_cols` 缺失、`max_depth=0`。

## 3. 单元测试策略

### 3.1 单记录泛化测试

```python
from engine.privacy.kano import (
    BUILTIN_HIERARCHIES,
    age_hierarchy,
    anonymize_record,
    zipcode_hierarchy,
)


def test_age_hierarchy_levels():
    assert age_hierarchy("28", 0) == "28"
    assert age_hierarchy("28", 1) == "[25-30]"
    assert age_hierarchy("28", 2) == "[20-30]"
    assert age_hierarchy("28", 3) == "[20-40]"
    assert age_hierarchy("28", 4) == "*"


def test_zipcode_hierarchy_levels():
    assert zipcode_hierarchy("518057", 0) == "518057"
    assert zipcode_hierarchy("518057", 1) == "518***"
    assert zipcode_hierarchy("518057", 2) == "51****"
    assert zipcode_hierarchy("518057", 3) == "5*****"
    assert zipcode_hierarchy("518057", 4) == "*"


def test_anonymize_record_does_not_mutate_input():
    record = {"age": "28", "zipcode": "518057", "gender": "女"}
    result = anonymize_record(record, ["age"], BUILTIN_HIERARCHIES, k=5)
    assert record["age"] == "28"
    assert result["age"] == "[25-30]"
```

### 3.2 数据集级 Mondrian 测试

```python
import pytest
from engine.privacy.kano_table import k_anonymize_table


def test_numeric_qi_generalizes_to_intervals():
    rows = [
        {"age": 25, "zipcode": "100001", "disease": "A"},
        {"age": 26, "zipcode": "100002", "disease": "B"},
        {"age": 27, "zipcode": "100003", "disease": "C"},
        {"age": 55, "zipcode": "200001", "disease": "D"},
        {"age": 56, "zipcode": "200002", "disease": "E"},
        {"age": 57, "zipcode": "200003", "disease": "F"},
    ]
    result = k_anonymize_table(rows, ["age", "zipcode"], k=3)
    assert len(result) == len(rows)
    assert {r["disease"] for r in result} == {"A", "B", "C", "D", "E", "F"}
    for r in result:
        assert "[" in str(r["age"])


def test_each_equivalence_group_size_at_least_k():
    from collections import Counter

    rows = [{"age": i, "gender": "M" if i % 2 == 0 else "F"} for i in range(20)]
    result = k_anonymize_table(rows, ["age", "gender"], k=5)
    groups = Counter((str(r["age"]), str(r["gender"])) for r in result)
    assert all(c >= 5 for c in groups.values())


def test_input_smaller_than_k_raises():
    with pytest.raises(ValueError, match="at least"):
        k_anonymize_table([{"age": 1}], ["age"], k=2)


def test_missing_qi_cols_raises():
    with pytest.raises(ValueError, match="not found"):
        k_anonymize_table([{"age": 1}], ["gender"], k=1)
```

## 4. 集成测试策略

### 4.1 REST 接口测试

```python
from fastapi.testclient import TestClient
from engine.main import app

client = TestClient(app)


def test_rest_k_anonymize_record():
    response = client.post(
        "/v1/privacy/k_anonymize/record",
        json={
            "record": {"age": "28", "zipcode": "518057", "gender": "女", "disease": "胃癌"},
            "qi_cols": ["age", "zipcode", "gender"],
            "k": 5,
        },
    )
    assert response.status_code == 200
    result = response.json()["result"]
    assert result["gender"] == "*"
    assert result["zipcode"] == "518***"


def test_rest_k_anonymize_table():
    response = client.post(
        "/v1/privacy/k_anonymize/table",
        json={
            "rows": [
                {"age": 25, "zipcode": "100001", "gender": "M", "disease": "A"},
                {"age": 26, "zipcode": "100002", "gender": "M", "disease": "B"},
                {"age": 27, "zipcode": "100003", "gender": "M", "disease": "C"},
                {"age": 55, "zipcode": "200001", "gender": "F", "disease": "D"},
                {"age": 56, "zipcode": "200002", "gender": "F", "disease": "E"},
                {"age": 57, "zipcode": "200003", "gender": "F", "disease": "F"},
            ],
            "qi_cols": ["age", "zipcode", "gender"],
            "k": 3,
        },
    )
    assert response.status_code == 200
    result = response.json()["result"]
    assert len(result) == 6
    assert {r["disease"] for r in result} == {"A", "B", "C", "D", "E", "F"}
```

### 3.3 DataFrame 测试

```python
import pandas as pd
from engine.privacy.kano_table import k_anonymize_dataframe


def test_k_anonymize_dataframe():
    df = pd.DataFrame({
        "age": [25, 26, 27, 55, 56, 57],
        "zipcode": ["100001", "100002", "100003", "200001", "200002", "200003"],
        "disease": ["A", "B", "C", "D", "E", "F"],
    })
    result = k_anonymize_dataframe(df, ["age", "zipcode"], k=3)
    assert isinstance(result, pd.DataFrame)
    assert len(result) == 6
```

### 3.4 指标测试

```python
from prometheus_client import REGISTRY
from engine.privacy.kano_table import k_anonymize_table


def test_kano_metric():
    before = REGISTRY.get_sample_value(
        "privacy_kano_operations_total", {"operation": "table"}
    ) or 0.0
    k_anonymize_table([{"age": 1}], ["age"], k=1)
    after = REGISTRY.get_sample_value(
        "privacy_kano_operations_total", {"operation": "table"}
    )
    assert after == before + 1
```

## 4. 集成测试策略

### 4.1 REST 接口测试

```python
import grpc
from engine.grpc_server import PrivacyServicer
from engine.proto import privacy_pb2, privacy_pb2_grpc


def test_grpc_k_anonymize_table():
    servicer = PrivacyServicer()
    rows = [
        privacy_pb2.RecordEntry(
            fields={"age": "25", "zipcode": "100001", "gender": "M", "disease": "A"}
        ),
        privacy_pb2.RecordEntry(
            fields={"age": "26", "zipcode": "100002", "gender": "M", "disease": "B"}
        ),
        privacy_pb2.RecordEntry(
            fields={"age": "55", "zipcode": "200001", "gender": "F", "disease": "D"}
        ),
        privacy_pb2.RecordEntry(
            fields={"age": "56", "zipcode": "200002", "gender": "F", "disease": "D"}
        ),
    ]
    request = privacy_pb2.KAnonymizeTableRequest(
        rows=rows,
        qi_cols=["age", "zipcode", "gender"],
        k=2,
    )
    response = servicer.KAnonymizeTable(request, None)
    assert len(response.rows) == 4
    diseases = {r.fields["disease"] for r in response.rows}
    assert diseases == {"A", "B", "D"}
```

## 5. 测试执行命令

```bash
# 运行所有 K-匿名相关测试
PYTHONPATH=. pytest tests/test_kano_table.py tests/test_rest.py tests/test_grpc.py -v -k kano

# 仅运行表级 Mondrian 单元测试
PYTHONPATH=. pytest tests/test_kano_table.py -v

# 运行示例脚本
PYTHONPATH=. python docs/k_anonymity/examples/kano_usage.py
```

## 6. 持续集成建议

- 每次提交前执行 `pytest tests/test_kano_table.py tests/test_rest.py tests/test_grpc.py -k kano`。
- CI 中保持 Python 版本一致，避免因字典顺序等差异导致测试抖动。
- 对等价组大小的断言应使用集合/计数器，避免依赖 Mondrian 的输出顺序。

## 7. 验收检查清单

- [ ] 单记录泛化对 `age`、`zipcode`、`gender` 的各层级输出正确。
- [ ] `anonymize_record` 不修改原始记录。
- [ ] 表级 Mondrian 输出每个等价组大小 ≥ `k`。
- [ ] 数值型 QI 泛化为区间，分类型 QI 泛化为取值集合。
- [ ] 非 QI 敏感字段保持不变。
- [ ] 记录数 < `k` 或 `qi_cols` 缺失时抛出明确异常。
- [x] REST `/v1/privacy/k_anonymize/record`、 `/v1/privacy/k_anonymize/table`、 `/v1/privacy/k_anonymize/dataframe` 接口测试通过。
- [x] gRPC `KAnonymizeRecord`、`KAnonymizeTable`、`KAnonymizeDataFrame` 接口测试通过。
- [x] DataFrame 输入/输出测试通过。
- [x] `privacy_kano_operations_total` 指标测试通过。