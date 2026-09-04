# 数据脱敏（Masking）产品设计 PRD


## 1. 概述

本文档定义 `PrivShield` 数据脱敏模块的产品需求与验收标准。脱敏模块根据字段名自动识别敏感类型（手机号、身份证、姓名、银行卡等），并提供掩码、截断、HMAC 哈希等保护措施。

## 2. 设计目标

- 根据字段名自动推断敏感类型并脱敏。
- 提供单字段、整记录、批量字段、DataFrame 多种输入支持。
- 提供 HMAC-SHA256 哈希与字符串截断工具。
- 暴露一致的 REST 与 gRPC 接口。
- 暴露模块级 Prometheus 指标 `privacy_masking_operations_total`。

## 3. 功能需求

| ID | 需求 |
|---|---|
| MASK-VALUE-1 | 根据 `field_name` 自动识别 `mobile`、`id_card`、`name`、`bank_card` 类型并脱敏。 |
| MASK-VALUE-2 | 未知字段使用默认策略：保留前后 3 位，中间用 `*` 填充。 |
| MASK-RECORD-1 | 对整条记录字典中的每个字符串值按字段名脱敏，非字符串值保持不变。 |
| MASK-BATCH-1 | 提供 `mask_value_batch`，支持批量字段名与批量值一一对应脱敏。 |
| MASK-DF-1 | 提供 `mask_dataframe`，支持 pandas / SecretFlow DataFrame 输入，对指定列或所有字符串列脱敏。 |
| MASK-HASH-1 | 提供 `hash_value`，使用 HMAC-SHA256 与 salt 生成 16 位 base64 摘要。 |
| MASK-TRUNC-1 | 提供 `truncate`，保留前 `keep_prefix` 位并追加 `***`。 |
| MASK-METRIC-1 | 暴露 `privacy_masking_operations_total` Counter，按 `operation` 标签区分 `mask_value`、`mask_record`、`mask_dataframe`、`hash`、`truncate`。 |
| MASK-API-1 | 提供 REST `/v1/privacy/mask`、`/v1/privacy/mask_record`、`/v1/privacy/mask/batch`、`/v1/privacy/mask/dataframe`、`/v1/privacy/hash`、`/v1/privacy/truncate`。 |
| MASK-API-2 | 提供对应 gRPC 方法。 |

## 4. 接口定义

### 4.1 REST 请求示例

#### 单字段脱敏

```json
{
  "field_name": "mobile",
  "value": "13812345678"
}
```

#### 批量字段脱敏

```json
{
  "field_names": ["mobile", "name", "id_card"],
  "values": ["13812345678", "张三丰", "110101199001011234"]
}
```

#### DataFrame 脱敏

```json
{
  "data": [
    {"mobile": "13812345678", "name": "张三", "age": 25},
    {"mobile": "13912345678", "name": "李四", "age": 34}
  ],
  "columns": ["mobile", "name"]
}
```

### 4.2 gRPC 方法

- `Mask`
- `MaskRecord`
- `MaskBatch`
- `MaskDataFrame`
- `Hash`

## 5. 验收标准

- [x] 单字段脱敏对 `mobile`、`id_card`、`name`、`bank_card`、默认类型输出正确。
- [x] 整记录脱敏不修改原始记录。
- [x] 批量字段脱敏支持长度校验。
- [x] DataFrame 脱敏支持 pandas 输入与列选择。
- [x] HMAC 哈希输出固定长度且对 salt 敏感。
- [x] `privacy_masking_operations_total` 指标在各入口正确递增。
- [x] REST/gRPC 接口测试通过。
- [x] 文档（PRD/design/api_reference/examples/ops/testing）已更新。