# 查询混淆运维说明


## 1. 运行检查

调用 REST 健康端点确认服务可用：

```bash
curl http://127.0.0.1:8079/health
```

## 2. 指标监控

查询混淆暴露 Prometheus 计数器 `privacy_qol_operations_total{domain}`。

### Prometheus 查询示例

```promql
rate(privacy_qol_operations_total[5m])
```

### 告警规则

```yaml
- alert: HighQOLQueryRate
  expr: rate(privacy_qol_operations_total[5m]) > 1000
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "Query obfuscation rate is high"
```

## 3. 日志

混淆操作会输出 `INFO` 级日志，包含 `domain` 与 `num_dummies`。

## 4. 故障排查

| 现象 | 可能原因 | 排查方法 |
|---|---|---|
| 返回结果不包含真实查询 | `query` 为空或参数错误 | 检查请求体字段 |
| dummy 查询重复率高 | pool 过小 | 提供自定义 `medical_pool` / `generic_pool` |

## 5. 最佳实践

- 生产环境建议使用自定义 dummy 池，避免攻击者识别内置池。
- 对高敏感查询结合差分隐私进一步保护。
- 开启 Prometheus 指标监控并设置合理告警阈值。