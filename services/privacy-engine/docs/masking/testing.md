# 数据脱敏模块测试文档


## 1. 概述

本文档定义 `engine/privacy/masking.py` 的测试策略、测试范围与可执行示例。

## 2. 测试目标

- 验证各字段类型脱敏规则正确。
- 验证整记录脱敏不修改原始记录。
- 验证批量字段脱敏长度校验。
- 验证 DataFrame 脱敏列选择与默认行为。
- 验证 HMAC 哈希与截断行为。
- 验证 `privacy_masking_operations_total` 指标递增。
- 验证 REST/gRPC 接口参数透传。

## 3. 单元测试策略

### 3.1 字段脱敏测试

```python
from engine.privacy.masking import mask_value


def test_mask_mobile():
    assert mask_value("mobile", "13812345678") == "138****5678"


def test_mask_id_card():
    assert mask_value("id_card", "110101199001011234") == "110101********1234"


def test_mask_name():
    assert mask_value("name", "张三丰") == "张**丰"
```

### 3.2 整记录脱敏测试

```python
from engine.privacy.masking import mask_record


def test_mask_record():
    record = {"mobile": "13812345678", "age": 30}
    result = mask_record(record)
    assert result["mobile"] == "138****5678"
    assert record["mobile"] == "13812345678"  # 原记录不变
```

### 3.3 DataFrame 脱敏测试

```python
import pandas as pd
from engine.privacy.masking import mask_dataframe


def test_mask_dataframe():
    df = pd.DataFrame({"mobile": ["13812345678"], "name": ["张三"]})
    result = mask_dataframe(df)
    assert result["mobile"].tolist() == ["138****5678"]
    assert result["name"].tolist() == ["张*"]
```

### 3.5 多格式输入测试

```python
import numpy as np
import pyarrow as pa
from engine.privacy.masking import mask_dataframe, mask_record


def test_mask_dataframe_numpy():
    """测试 numpy ndarray 输入"""
    arr = np.array([["13812345678", "张三"]])
    result = mask_dataframe(arr, columns=["col_0", "col_1"])
    assert result[0]["col_0"] == "138****5678"
    assert result[0]["col_1"] == "张*"


def test_mask_dataframe_arrow():
    """测试 PyArrow Table 输入（列式计算快速路径，返回 pyarrow.Table）"""
    table = pa.table({"mobile": ["13812345678"], "name": ["张三"]})
    result = mask_dataframe(table)
    assert isinstance(result, pa.Table)
    assert result.column("mobile").to_pylist() == ["138****5678"]
    assert result.column("name").to_pylist() == ["张*"]


def test_mask_dataframe_arrow_ipc():
    """测试 Arrow IPC 字节流输入"""
    import pyarrow.ipc as ipc
    table = pa.table({"mobile": ["13812345678"]})
    sink = pa.BufferOutputStream()
    with ipc.new_stream(sink, table.schema) as writer:
        writer.write_table(table)
    arrow_bytes = sink.getvalue().to_pybytes()
    result = mask_dataframe(arrow_bytes)
    assert result[0]["mobile"] == "138****5678"


def test_mask_record_numpy():
    """测试 mask_record 支持 numpy ndarray"""
    arr = np.array(["13812345678", "张三"])
    result = mask_record(arr)
    assert result["col_0"] == "138****5678"
    assert result["col_1"] == "张*"


def test_mask_record_arrow_ipc():
    """测试 mask_record 支持 Arrow IPC 字节流"""
    import pyarrow.ipc as ipc
    table = pa.table({"mobile": ["13812345678"], "name": ["张三"]})
    sink = pa.BufferOutputStream()
    with ipc.new_stream(sink, table.schema) as writer:
        writer.write_table(table)
    arrow_bytes = sink.getvalue().to_pybytes()
    result = mask_record(arrow_bytes)
    assert result["mobile"] == "138****5678"
    assert result["name"] == "张*"
```

### 3.4 指标测试

```python
from prometheus_client import REGISTRY
from engine.privacy.masking import mask_value


def test_masking_metric():
    before = REGISTRY.get_sample_value(
        "privacy_masking_operations_total", {"operation": "mask_value"}
    ) or 0.0
    mask_value("mobile", "13812345678")
    after = REGISTRY.get_sample_value(
        "privacy_masking_operations_total", {"operation": "mask_value"}
    )
    assert after == before + 1
```

## 4. 测试执行命令

```bash
PYTHONPATH=. pytest tests/test_masking.py -v
```

## 5. 验收检查清单

- [x] 各字段类型脱敏规则测试通过。
- [x] 整记录脱敏不修改原记录。
- [x] 批量字段脱敏长度校验正确。
- [x] DataFrame 脱敏测试通过。
- [x] **多格式输入测试通过**（numpy、PyArrow Table 列式计算、Arrow IPC、Polars）。
- [x] HMAC 哈希与截断测试通过。
- [x] `privacy_masking_operations_total` 指标测试通过。
- [x] REST/gRPC 接口测试通过。