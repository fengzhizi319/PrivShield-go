# 数据脱敏运维手册


## 1. 调用示例

```bash
# 单字段脱敏
curl -X POST http://127.0.0.1:8079/v1/privacy/mask \
  -H "Content-Type: application/json" \
  -d '{"field_name": "mobile", "value": "13812345678"}'

# 批量字段脱敏
curl -X POST http://127.0.0.1:8079/v1/privacy/mask/batch \
  -H "Content-Type: application/json" \
  -d '{
    "field_names": ["mobile", "name"],
    "values": ["13812345678", "张三丰"]
  }'

# DataFrame 脱敏
curl -X POST http://127.0.0.1:8079/v1/privacy/mask/dataframe \
  -H "Content-Type: application/json" \
  -d '{
    "data": [
      {"mobile": "13812345678", "name": "张三"}
    ],
    "columns": ["mobile", "name"]
  }'
```

## 2. 参数建议

| 参数 | 建议 |
|---|---|
| `field_name` | 使用标准字段名，如 `mobile`、`id_card`、`name`、`bank_card`。 |
| `columns` | DataFrame 脱敏时显式指定敏感列。 |
| `salt` | HMAC 哈希使用随机、独立、定期轮换的盐值。 |

## 3. 故障排查

| 现象 | 原因 |
|---|---|
| `400 field_names and values must have the same length` | 批量脱敏两个列表长度不一致。 |
| `400 Unsupported table input type` | DataFrame 输入类型不支持。 |
| 脱敏结果不符合预期 | 字段名未匹配到标准类型，走默认策略。 |

## 4. 指标监控

```text
privacy_masking_operations_total{operation="mask_value"}
privacy_masking_operations_total{operation="mask_record"}
privacy_masking_operations_total{operation="mask_dataframe"}
privacy_masking_operations_total{operation="hash"}
privacy_masking_operations_total{operation="truncate"}
```

可用于审计调用频率与容量规划。