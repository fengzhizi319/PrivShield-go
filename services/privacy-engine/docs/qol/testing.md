# 查询混淆测试说明


## 1. 测试文件

- `tests/test_qol.py`
- `tests/test_rest.py`（QOL 相关接口）
- `tests/test_grpc.py`（QOL gRPC 方法）

## 2. 测试覆盖

| 测试项 | 说明 |
|---|---|
| 单条混淆包含真实查询 | 验证 `query` 出现在返回列表中 |
| 批量混淆数量正确 | 返回 `len(queries)` 个列表，每个长度 `num_dummies + 1` |
| 自定义 pool 生效 | 仅返回自定义池中的 dummy |
| seed 可复现 | 相同 seed 得到相同结果 |
| 指标递增 | `privacy_qol_operations_total` 递增 |
| REST / gRPC 接口 | 请求响应字段与状态码 |

## 3. 运行测试

```bash
cd /path/to/PrivShield
PYTHONPATH=. pytest tests/test_qol.py -v
```

## 4. 添加新用例

在 `tests/test_qol.py` 中添加函数，保持命名 `test_*`，并验证以下至少一项：

- 返回列表包含真实查询。
- 输出数量与输入参数一致。
- 自定义 pool / seed 生效。
- 指标计数增加。