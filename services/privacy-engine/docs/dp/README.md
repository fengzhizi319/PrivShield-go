# 差分隐私（DP）文档索引

本目录包含 `PrivShield` 差分隐私模块的全套 SDLC 文档。


## 文档清单

| 文档 | 说明 | 目标读者 |
|---|---|---|
| [prd.md](./prd.md) | 产品需求文档 | 产品经理、项目经理 |
| [design.md](./design.md) | 算法原理、技术架构与实现细节 | 算法工程师、后端开发 |
| [api_reference.md](./api_reference.md) | Python SDK / REST / gRPC API 参考 | 接入开发者 |
| [examples.md](./examples.md) | Python 与 REST 使用示例 | 接入开发者 |
| [examples/dp_usage.py](./examples/dp_usage.py) | 可运行的完整示例脚本 | 接入开发者 |
| [examples/local_dp_usage.py](./examples/local_dp_usage.py) | 本地差分隐私可运行示例 | 接入开发者 |
| [testing.md](./testing.md) | 测试策略与测试代码示例 | QA、测试开发 |
| [ops.md](./ops.md) | 运维手册、参数建议与故障排查 | SRE、运维 |

## 快速开始

1. 阅读 [prd.md](./prd.md) 了解产品需求。
2. 阅读 [design.md](./design.md) 掌握算法原理与敏感度控制。
3. 查看 [examples.md](./examples.md) 或运行 [examples/dp_usage.py](./examples/dp_usage.py) 快速上手。
4. 开发时参考 [api_reference.md](./api_reference.md)。
5. 部署与排障参考 [ops.md](./ops.md)。
6. 编写测试参考 [testing.md](./testing.md)。

## 运行示例

```bash
cd /path/to/PrivShield
source .venv/bin/activate
PYTHONPATH=. python docs/dp/examples/dp_usage.py
```