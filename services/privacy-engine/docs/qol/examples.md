# 查询混淆示例


## 1. Python 示例

### 单条混淆

```python
from engine.privacy.qol import obfuscate_query

obfuscated = obfuscate_query(
    query="张三位糖尿病患者用药趋势",
    num_dummies=3,
    domain="medical",
    seed=42,
)
print(obfuscated)
```

### 批量混淆

```python
from engine.privacy.qol import obfuscate_query_batch

results = obfuscate_query_batch(
    queries=["张三用药记录", "李四手术预约"],
    num_dummies=2,
    domain="medical",
    seed=123,
)
for r in results:
    print(r)
```

### 自定义 dummy 池

```python
obfuscated = obfuscate_query(
    query="张三病历",
    num_dummies=4,
    domain="medical",
    medical_pool=["流感疫苗预约", "健康证办理", "医保报销流程", "体检报告查询"],
    seed=0,
)
```

## 2. REST 示例

### cURL

```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/qol/obfuscate \
  -H "Content-Type: application/json" \
  -d '{
    "query": "张三糖尿病患者用药趋势",
    "num_dummies": 3,
    "domain": "medical"
  }'
```

### Python 客户端

```python
import requests

resp = requests.post(
    "http://127.0.0.1:8079/v1/privacy/qol/obfuscate/batch",
    json={
        "queries": ["张三用药记录", "李四手术预约"],
        "num_dummies": 2,
        "domain": "generic",
    },
)
print(resp.json())
```