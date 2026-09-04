# 数据脱敏模块 API 参考


## 1. Python SDK

### `mask_value`

位置：`engine.privacy.masking.mask_value`

```python
def mask_value(field_name: str, value: str, context: str = "") -> str
```

根据字段名推断敏感类型并脱敏。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `field_name` | `str` | 是 | 字段名 |
| `value` | `str` | 是 | 原始值 |
| `context` | `str` | 否 | 上下文信息，预留 |

**使用场景：**
- API 响应中单个敏感字段的实时脱敏
- 日志记录前对单个字段值的脱敏处理
- 消息队列中单条消息的字段级脱敏

**参数建议：**
- `field_name`：建议使用英文命名（如 `mobile`, `id_card`），也支持中文（如 `手机号`, `身份证`）
- `value`：确保传入字符串类型，非字符串需先转换
- `context`：当前版本未使用，保留供未来扩展

**示例：**
```python
from engine.privacy.masking import mask_value

# 手机号脱敏
result = mask_value("mobile", "13812345678")
# 输出: "138****5678"

# 姓名脱敏
result = mask_value("name", "张三丰")
# 输出: "张**丰"
```

### `mask_record`

位置：`engine.privacy.masking.mask_record`

```python
def mask_record(record: Any, context: str = "") -> Any
```

对记录中的每个字符串值按字段名脱敏。**支持多种输入数据格式**（参考 DP 模块 `extract_values` 设计）：

- **dict**：直接作为记录处理
- **bytes/bytearray**：解析为 Arrow IPC Stream 并取第一行
- **PyArrow Table / RecordBatch**：提取第一行为字典
- **numpy ndarray**：1-D 数组按 `{col_0: val, ...}` 构建记录；2-D 取第一行
- **pandas Series**：调用 `to_dict()` 转换
- **Polars Series**：调用 `to_dict()` 转换

**使用场景：**
- 用户信息完整记录的脱敏（包含多个PII字段）
- 数据库查询结果单行记录的脱敏
- API 返回完整用户对象前的脱敏处理
- 从 Arrow IPC 字节流解析并脱敏单条记录
- 从 numpy 数组构建记录并脱敏

**参数建议：**
- `record`：支持多种格式，非 dict 格式会自动转换为字典
- 字典的键应为字段名，值为字符串类型；非字符串值会保持不变
- 返回新字典，不修改原记录

**示例：**
```python
from engine.privacy.masking import mask_record

record = {
    "mobile": "13812345678",
    "name": "张三丰",
    "id_card": "110101199001011234",
    "age": 30  # 非字符串，保持不变
}
masked = mask_record(record)
# 输出:
# {
#     "mobile": "138****5678",
#     "name": "张**丰",
#     "id_card": "110101********1234",
#     "age": 30
# }
```

### `mask_value_batch`

位置：`engine.privacy.masking.mask_value_batch`

```python
def mask_value_batch(
    field_names: List[str], values: List[str], context: str = ""
) -> List[str]
```

批量字段脱敏。`field_names` 与 `values` 长度必须一致。

**使用场景：**
- 同时脱敏多个独立字段值（字段间无关联）
- 表单提交时批量处理多个输入字段
- CSV/Excel 文件中多列数据的并行脱敏

**参数建议：**
- `field_names` 和 `values` 必须一一对应，长度不一致会抛出 `ValueError`
- 适合处理字段数量较多但记录数较少的场景
- 如需处理大量记录，建议使用 `mask_dataframe`

**示例：**
```python
from engine.privacy.masking import mask_value_batch

field_names = ["mobile", "name", "id_card"]
values = ["13812345678", "张三丰", "110101199001011234"]

results = mask_value_batch(field_names, values)
# 输出: ["138****5678", "张**丰", "110101********1234"]
```

### `mask_dataframe`

位置：`engine.privacy.masking.mask_dataframe`

```python
def mask_dataframe(
    df: Any,
    columns: Optional[List[str]] = None,
    context: str = "",
) -> Any
```

对 DataFrame 中的指定列脱敏。**支持多种输入数据格式**（参考 DP 模块 `extract_values` 设计）：

- **pandas DataFrame**：自动检测并使用 Pandas 向量化优化
- **PyArrow Table / RecordBatch**：自动检测并使用 `pyarrow.compute` 列式计算内核，避免 `to_pylist()` 全量物化，返回值为 `pyarrow.Table`
- **SecretFlow DataFrame / HDataFrame / VDataFrame**：联邦学习数据结构
- **numpy ndarray**（1-D 或 2-D）：按列名或自动列名构建记录
- **Arrow IPC Stream 字节流**（bytes/bytearray）：二进制传输格式
- **Polars DataFrame**：高性能 DataFrame 库
- **list of dict**：记录列表

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `df` | `Any` | 是 | 输入数据（支持多种格式） |
| `columns` | `Optional[List[str]]` | 否 | 目标列名；None 则对所有字符串列脱敏 |
| `context` | `str` | 否 | 上下文信息 |

**使用场景：**
- 大规模数据集批量脱敏（万级以上记录）
- 数据导出前的整表脱敏处理
- 数据分析前的隐私保护预处理
- 机器学习训练数据的脱敏准备
- 从 Arrow IPC 字节流解析并脱敏
- 从 numpy 数组构建 DataFrame 并脱敏
- Polars DataFrame 脱敏

**参数建议：**
- **性能优化**：模块会自动检测 pandas DataFrame 并使用向量化操作，检测 PyArrow Table/RecordBatch 并使用列式计算内核，性能提升 10-100 倍
- **PyArrow 快速路径**：PyArrow 输入通过 `pyarrow.compute` UTF-8 内核直接在列式内存中脱敏，避免全量物化为 Python 对象，返回值为 `pyarrow.Table`
- **多格式支持**：非 pandas / 非 PyArrow 输入会自动转换为记录列表处理
- `columns` 参数：
  - 明确指定需要脱敏的列，避免不必要的处理
  - 设为 `None` 时自动识别所有 object/string 类型列
  - 建议显式指定敏感列以提高性能和可控性
- 大数据集（>100万行）建议分批处理，避免内存溢出

**示例：**
```python
import pandas as pd
import pyarrow as pa
from engine.privacy.masking import mask_dataframe

# 创建示例数据
df = pd.DataFrame({
    "mobile": ["13812345678", "13987654321"],
    "name": ["张三", "李四"],
    "age": [25, 30],  # 数值列，不会被脱敏
    "email": ["test@example.com", "user@test.com"]
})

# 方式1：指定列脱敏（推荐）
masked_df = mask_dataframe(df, columns=["mobile", "name"])

# 方式2：所有字符串列自动脱敏
masked_df = mask_dataframe(df)

print(masked_df)
#       mobile   name  age              email
# 0  138****5678   张*   25  t***@example.com
# 1  139****4321   李*   30      u***@test.com

# PyArrow Table 输入（列式计算快速路径，返回 pyarrow.Table）
table = pa.table({
    "mobile": ["13812345678", "13987654321"],
    "name": ["张三", "李四"],
})
result_table = mask_dataframe(table)
print(type(result_table))  # <class 'pyarrow.lib.Table'>
print(result_table.column("mobile").to_pylist())
# ['138****5678', '139****4321']
```

### `hash_value`

位置：`engine.privacy.masking.hash_value`

```python
def hash_value(value: str, salt: str) -> str
```

HMAC-SHA256 哈希，输出 16 位 base64 摘要。

**使用场景：**
- 用户ID等需要匿名化但保持唯一性的场景
- 跨系统数据关联时的隐私保护
- 审计日志中敏感信息的不可逆脱敏
- 去重统计时避免暴露原始值

**参数建议：**
- `salt`：建议使用固定盐值以保证相同输入产生相同哈希（便于关联分析）
- 盐值应妥善保管，避免泄露导致彩虹表攻击
- 输出固定为 16 字符，适合用作数据库索引或外键

**示例：**
```python
from engine.privacy.masking import hash_value

# 使用固定盐值
user_id_hash = hash_value("user_12345", "my_secret_salt")
# 输出: "aB3dE5gH7jK9mN1p" (16字符)

# 相同输入+盐值产生相同哈希（可用于关联）
assert hash_value("user_12345", "my_secret_salt") == hash_value("user_12345", "my_secret_salt")
```

### `truncate`

位置：`engine.privacy.masking.truncate`

```python
def truncate(value: str, keep_prefix: int) -> str
```

保留前 `keep_prefix` 位并追加 `***`。

**使用场景：**
- 用户名、账号等需要部分可见的场景
- 邮箱地址前缀显示（如 `zhang***@example.com`）
- 订单号、交易号的简化展示
- 搜索结果显示时的隐私保护

**参数建议：**
- `keep_prefix`：通常设置为 3-6，平衡可用性与隐私性
- 若原始字符串长度 ≤ `keep_prefix`，则原样返回
- 比 `mask_value` 更简单，适用于不需要格式保留的场景

**示例：**
```python
from engine.privacy.masking import truncate

# 用户名截断
result = truncate("zhangsan", 3)
# 输出: "zha***"

# 短字符串原样返回
result = truncate("abc", 5)
# 输出: "abc"
```

---

## 2. REST API

### POST `/v1/privacy/mask`

单字段脱敏接口。

**使用场景：**
- 前端请求单个字段脱敏（如手机号展示）
- 微服务间调用时的敏感字段处理
- 日志记录前的实时脱敏

**请求示例：**
```json
{
  "field_name": "mobile",
  "value": "13812345678"
}
```

**响应示例：**
```json
{
  "result": "138****5678"
}
```

**curl 示例：**
```bash
curl -X POST http://localhost:8079/v1/privacy/mask \
  -H "Content-Type: application/json" \
  -d '{"field_name": "mobile", "value": "13812345678"}'
```

### POST `/v1/privacy/mask_record`

整记录脱敏接口。

**使用场景：**
- 用户信息完整记录的脱敏返回
- 数据库查询结果的隐私保护
- 批量字段但单条记录的脱敏

**请求示例：**
```json
{
  "record": {
    "mobile": "13812345678",
    "name": "张三丰",
    "id_card": "110101199001011234",
    "age": 30
  }
}
```

**响应示例：**
```json
{
  "result": {
    "mobile": "138****5678",
    "name": "张**丰",
    "id_card": "110101********1234",
    "age": 30
  }
}
```

### POST `/v1/privacy/mask/batch`

批量字段脱敏接口。

**使用场景：**
- 表单提交时多字段同时脱敏
- CSV 文件列级脱敏处理
- 多个独立字段值的并行处理

**请求示例：**
```json
{
  "field_names": ["mobile", "name"],
  "values": ["13812345678", "张三丰"]
}
```

**响应示例：**
```json
{
  "result": ["138****5678", "张**丰"]
}
```

**注意事项：**
- `field_names` 和 `values` 数组长度必须一致
- 长度不一致时返回 HTTP 400 错误

### POST `/v1/privacy/mask/dataframe`

DataFrame 批量脱敏接口。

**使用场景：**
- 大规模数据集批量脱敏（推荐用于 >1000 条记录）
- 数据导出前的整表处理
- 数据分析/机器学习前的隐私保护预处理
- SecretFlow 联邦学习数据准备

**请求示例：**
```json
{
  "data": [
    {"mobile": "13812345678", "name": "张三", "age": 25},
    {"mobile": "13987654321", "name": "李四", "age": 30}
  ],
  "columns": ["mobile", "name"]
}
```

**响应示例：**
```json
{
  "result": [
    {"mobile": "138****5678", "name": "张*", "age": 25},
    {"mobile": "139****4321", "name": "李*", "age": 30}
  ]
}
```

**性能建议：**
- 小数据集（<1000条）：可使用此接口或多次调用 `/v1/privacy/mask_record`
- 中等数据集（1000-10万条）：推荐使用此接口，自动启用 Pandas 向量化优化
- 大数据集（>10万条）：建议分批调用，每批 1-5 万条，避免内存溢出
- `columns` 参数：明确指定需要脱敏的列可提升性能 20-50%

### POST `/v1/privacy/hash`

HMAC 哈希接口。

**使用场景：**
- 用户ID匿名化但保持唯一性
- 跨系统数据关联的隐私保护
- 审计日志中敏感信息的不可逆处理

**请求示例：**
```json
{
  "value": "user_12345",
  "salt": "my_secret_salt"
}
```

**响应示例：**
```json
{
  "result": "aB3dE5gH7jK9mN1p"
}
```

**安全建议：**
- 盐值应通过环境变量或密钥管理系统提供，不要硬编码
- 相同盐值保证相同输入产生相同哈希，便于数据关联
- 定期轮换盐值以增强安全性

---

## 3. gRPC API

gRPC 接口提供与 REST API 相同的功能，但具有更高的性能和类型安全性。

**使用场景：**
- 高性能微服务间调用（比 REST 快 2-5 倍）
- 流式数据处理和批量传输
- 强类型约束的内部服务通信
- SecretFlow 联邦学习框架集成

### 接口列表

| 方法 | 请求 | 响应 | 说明 |
|---|---|---|---|
| `Mask` | `MaskRequest` | `MaskResponse` | 单字段脱敏 |
| `MaskRecord` | `MaskRecordRequest` | `MaskRecordResponse` | 整记录脱敏 |
| `MaskBatch` | `MaskBatchRequest` | `MaskBatchResponse` | 批量字段脱敏 |
| `MaskDataFrame` | `MaskDataFrameRequest` | `MaskDataFrameResponse` | DataFrame 脱敏 |
| `Hash` | `HashRequest` | `HashResponse` | HMAC 哈希 |

### Python gRPC 客户端示例

```python
import grpc
import privacy_pb2
import privacy_pb2_grpc

# 创建 gRPC 通道
channel = grpc.insecure_channel('localhost:50051')
stub = privacy_pb2_grpc.PrivacyServiceStub(channel)

# 示例1：单字段脱敏
response = stub.Mask(privacy_pb2.MaskRequest(
    field_name="mobile",
    value="13812345678"
))
print(response.result)  # "138****5678"

# 示例2：整记录脱敏
response = stub.MaskRecord(privacy_pb2.MaskRecordRequest(
    record={"mobile": "13812345678", "name": "张三丰"}
))
print(response.result)  # {"mobile": "138****5678", "name": "张**丰"}

# 示例3：批量字段脱敏
response = stub.MaskBatch(privacy_pb2.MaskBatchRequest(
    field_names=["mobile", "name"],
    values=["13812345678", "张三丰"]
))
print(response.results)  # ["138****5678", "张**丰"]

# 示例4：HMAC 哈希
response = stub.Hash(privacy_pb2.HashRequest(
    value="user_12345",
    salt="my_secret_salt"
))
print(response.result)  # "aB3dE5gH7jK9mN1p"
```

### gRPC vs REST 选择建议

| 维度 | gRPC | REST |
|---|---|---|
| 性能 | ⭐⭐⭐⭐⭐ (Protobuf 二进制) | ⭐⭐⭐ (JSON 文本) |
| 类型安全 | ⭐⭐⭐⭐⭐ (强类型) | ⭐⭐ (弱类型) |
| 浏览器支持 | ❌ 需要 grpc-web | ✅ 原生支持 |
| 调试便利性 | ⭐⭐ (需要工具) | ⭐⭐⭐⭐⭐ (curl/Postman) |
| 流式处理 | ✅ 原生支持 | ❌ 需额外实现 |
| 适用场景 | 内部微服务、高性能 | 前端API、公开接口 |

**推荐：**
- 内部服务间通信 → 优先使用 gRPC
- 前端/移动端调用 → 使用 REST API
- 大数据量批量处理 → 使用 gRPC + 流式

---

## 4. 异常与错误码

### 常见错误类型

| 异常/错误 | 触发条件 | HTTP 状态码 | gRPC 状态码 |
|---|---|---|---|
| `ValueError: field_names and values must have the same length` | 批量脱敏长度不一致 | 400 | `INVALID_ARGUMENT` |
| `TypeError: Unsupported table input type: ...` | DataFrame 类型不支持 | 400 | `INVALID_ARGUMENT` |
| `ValidationError` | Pydantic 模型校验失败 | 422 | `INVALID_ARGUMENT` |
| `HTTPException(401)` | 认证失败（启用 auth 时） | 401 | `UNAUTHENTICATED` |
| `HTTPException(429)` | 速率限制超限（启用 rate limit 时） | 429 | `RESOURCE_EXHAUSTED` |

### 错误处理示例

**Python SDK：**
```python
from engine.privacy.masking import mask_value_batch

try:
    # 错误示例：长度不匹配
    result = mask_value_batch(
        field_names=["mobile", "name"],
        values=["13812345678"]  # 少一个值
    )
except ValueError as e:
    print(f"参数错误: {e}")
    # 输出: 参数错误: field_names and values must have the same length
```

**REST API：**
```python
import requests

response = requests.post(
    "http://localhost:8079/v1/privacy/mask/batch",
    json={
        "field_names": ["mobile", "name"],
        "values": ["13812345678"]  # 长度不匹配
    }
)

if response.status_code == 400:
    print(f"请求错误: {response.json()['detail']}")
```

**gRPC：**
```python
import grpc

try:
    response = stub.MaskBatch(privacy_pb2.MaskBatchRequest(
        field_names=["mobile", "name"],
        values=["13812345678"]
    ))
except grpc.RpcError as e:
    print(f"gRPC 错误: {e.code()} - {e.details()}")
    # 输出: gRPC 错误: INVALID_ARGUMENT - field_names and values must have the same length
```

### 最佳实践

1. **输入校验**
   - 调用前验证字段名和值的长度一致性
   - 确保所有值为字符串类型
   - 检查 DataFrame 列名是否存在

2. **性能优化**
   - 小数据量（<100条）：使用 `mask_record` 或多次 `mask_value`
   - 中等数据量（100-1000条）：使用 `mask_value_batch`
   - 大数据量（>1000条）：使用 `mask_dataframe` + Pandas 向量化或 PyArrow 列式计算

3. **安全建议**
   - HMAC 盐值通过环境变量传递，不要硬编码
   - 生产环境启用 TLS、认证和速率限制
   - 定期审计脱敏日志，确保敏感信息未泄露

4. **错误重试**
   - 对于临时性错误（如预算耗尽），实现指数退避重试
   - 对于参数错误（400），不应重试，需修正参数
   - gRPC 连接断开时自动重连

5. **监控告警**
   - 监控 `privacy_masking_operations_total` 指标
   - 设置错误率阈值告警（如 >5%）
   - 跟踪 P99 延迟，确保 SLA