# 数据脱敏使用示例


## 1. Python SDK 示例

### 1.1 单字段脱敏

```python
from engine.privacy.masking import mask_value

print(mask_value("mobile", "13812345678"))
# 138****5678

print(mask_value("id_card", "110101199001011234"))
# 110101********1234

print(mask_value("name", "张三丰"))
# 张**丰
```

### 1.2 整记录脱敏

```python
from engine.privacy.masking import mask_record

record = {
    "mobile": "13812345678",
    "name": "张三丰",
    "id_card": "110101199001011234",
    "age": 30,
}
print(mask_record(record))
# {'mobile': '138****5678', 'name': '张**丰', 'id_card': '110101********1234', 'age': 30}
```

### 1.3 批量字段脱敏

```python
from engine.privacy.masking import mask_value_batch

print(mask_value_batch(
    ["mobile", "name", "id_card"],
    ["13812345678", "张三丰", "110101199001011234"],
))
# ['138****5678', '张**丰', '110101********1234']
```

### 1.4 DataFrame 脱敏

```python
import pandas as pd
from engine.privacy.masking import mask_dataframe

df = pd.DataFrame({
    "mobile": ["13812345678", "13912345678"],
    "name": ["张三", "李四"],
    "age": [25, 34],
})
result = mask_dataframe(df)
print(result)
#      mobile  name  age
# 0  138****5678   张*   25
# 1  139****5678   李*   34
```

### 1.5 多格式输入示例

```python
import numpy as np
import pyarrow as pa
from engine.privacy.masking import mask_dataframe, mask_record

# numpy ndarray 输入
arr = np.array([["13812345678", "张三"], ["13912345678", "李四"]])
result = mask_dataframe(arr, columns=["col_0", "col_1"])
print(result)
# [{'col_0': '138****5678', 'col_1': '张*'}, {'col_0': '139****5678', 'col_1': '李*'}]

# PyArrow Table 输入（列式计算快速路径，返回 pyarrow.Table）
table = pa.table({
    "mobile": ["13812345678", "13912345678"],
    "name": ["张三", "李四"],
})
result = mask_dataframe(table)
print(type(result))  # <class 'pyarrow.lib.Table'>
print(result.column("mobile").to_pylist())
# ['138****5678', '139****5678']
print(result.column("name").to_pylist())
# ['张*', '李*']

# Arrow IPC 字节流输入
import pyarrow.ipc as ipc
sink = pa.BufferOutputStream()
with ipc.new_stream(sink, table.schema) as writer:
    writer.write_table(table)
arrow_bytes = sink.getvalue().to_pybytes()

result = mask_dataframe(arrow_bytes)
print(result)
# [{'mobile': '138****5678', 'name': '张*'}, {'mobile': '139****5678', 'name': '李*'}]

# mask_record 支持 numpy ndarray 输入
arr = np.array(["13812345678", "张三"])
result = mask_record(arr)
print(result)
# {'col_0': '138****5678', 'col_1': '张*'}

# mask_record 支持 Arrow IPC 字节流输入
result = mask_record(arrow_bytes)
print(result)
# {'mobile': '138****5678', 'name': '张*'}
```

### 1.6 HMAC 哈希与截断

```python
from engine.privacy.masking import hash_value, truncate

print(hash_value("hello", "salt"))
# 16 位 base64 摘要

print(truncate("abcdefgh", 3))
# abc***
```

## 2. REST API 示例

```bash
# 单字段脱敏
curl -X POST http://127.0.0.1:8079/v1/privacy/mask \
  -H "Content-Type: application/json" \
  -d '{"field_name": "mobile", "value": "13812345678"}'

# 批量字段脱敏
curl -X POST http://127.0.0.1:8079/v1/privacy/mask/batch \
  -H "Content-Type: application/json" \
  -d '{
    "field_names": ["mobile", "name", "id_card"],
    "values": ["13812345678", "张三丰", "110101199001011234"]
  }'

# DataFrame 脱敏
curl -X POST http://127.0.0.1:8079/v1/privacy/mask/dataframe \
  -H "Content-Type: application/json" \
  -d '{
    "data": [
      {"mobile": "13812345678", "name": "张三", "age": 25}
    ],
    "columns": ["mobile", "name"]
  }'

# HMAC 哈希
curl -X POST http://127.0.0.1:8079/v1/privacy/hash \
  -H "Content-Type: application/json" \
  -d '{"value": "hello", "salt": "salt"}'
```

## 3. 最佳实践

1. **字段命名规范**：使用 `mobile`、`id_card`、`name`、`bank_card` 等标准字段名，确保自动识别准确。
2. **DataFrame 脱敏**：优先通过 `columns` 参数显式指定需要脱敏的列，避免误处理非敏感列。
3. **HMAC 盐值**：生产环境使用独立、随机、不可预测的 salt，并定期轮换。
4. **监控指标**：关注 `privacy_masking_operations_total`，发现异常调用量及时告警。