# 查询混淆文档

本目录包含查询混淆（Query Obfuscation）模块的完整文档。


## 文档索引

| 文档 | 说明 |
|---|---|
| [prd.md](prd.md) | 产品需求 |
| [design.md](design.md) | 算法与模块设计 |
| [api_reference.md](api_reference.md) | Python SDK / REST / gRPC API |
| [examples.md](examples.md) | 使用示例 |
| [ops.md](ops.md) | 运维与监控 |
| [testing.md](testing.md) | 测试说明 |

## 快速开始

```python
from engine.privacy.qol import obfuscate_query

result = obfuscate_query("张三糖尿病患者用药趋势", num_dummies=3, seed=42)
print(result)
```