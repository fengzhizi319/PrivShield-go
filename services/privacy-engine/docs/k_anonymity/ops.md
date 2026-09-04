# 数据集级 K-匿名运维手册


## 1. 调用示例

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/k_anonymize/table \
  -H "Content-Type: application/json" \
  -d '{
    "rows": [
      {"age": 25, "zipcode": "100001", "gender": "M", "disease": "A"},
      {"age": 26, "zipcode": "100002", "gender": "M", "disease": "B"},
      {"age": 35, "zipcode": "100003", "gender": "F", "disease": "C"},
      {"age": 36, "zipcode": "100004", "gender": "F", "disease": "D"}
    ],
    "qi_cols": ["age", "zipcode", "gender"],
    "k": 2
  }'
```

## 2. 参数建议

| 参数 | 建议 |
|---|---|
| `k` | 医疗/基因数据建议 5~10，一般场景建议 3~5。 |
| `max_depth` | 默认 10，列数多或数据量大时可适当增大。 |
| `qi_cols` | 仅选择可公开组合后可能重识别的列，避免将敏感属性放入 QI。 |

## 3. 故障排查

| 现象 | 原因 |
|---|---|
| `400` | 记录数 < k，或 `qi_cols` 包含不存在列，或 DataFrame 输入类型不支持。 |
| 泛化过度 | k 过大或 `max_depth` 过小。 |
| 输出顺序变化 | 算法会按 QI 排序，输出顺序与输入不一定一致。 |

## 4. 指标监控

`privacy_kano_operations_total{operation}` 记录 K-匿名操作次数：

```text
privacy_kano_operations_total{operation="table"}
privacy_kano_operations_total{operation="dataframe"}
privacy_kano_operations_total{operation="record"}
```

可用于审计各接口调用频率与容量规划。