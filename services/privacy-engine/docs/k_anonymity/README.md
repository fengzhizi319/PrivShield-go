# K-匿名（K-Anonymity）文档索引

本目录包含 `PrivShield` K-匿名模块的全套 SDLC 文档。该模块同时提供**单记录启发式泛化**与**数据集级 Mondrian 泛化**两种能力，用于降低准标识符组合带来的重识别风险。


## 文档清单

| 文档 | 说明 | 目标读者 |
|---|---|---|
| [prd.md](./prd.md) | 产品需求文档 | 产品经理、项目经理 |
| [design.md](./design.md) | 算法原理、技术架构与实现细节 | 算法工程师、后端开发 |
| [api_reference.md](./api_reference.md) | Python SDK / REST / gRPC API 参考 | 接入开发者 |
| [examples.md](./examples.md) | Python 与 REST 使用示例 | 接入开发者 |
| [examples/kano_usage.py](./examples/kano_usage.py) | 可运行的完整示例脚本 | 接入开发者 |
| [testing.md](./testing.md) | 测试策略与测试代码示例 | QA、测试开发 |
| [ops.md](./ops.md) | 运维手册、参数建议与故障排查 | SRE、运维 |

## 快速开始

1. 阅读 [prd.md](./prd.md) 了解产品需求。
2. 阅读 [design.md](./design.md) 掌握 Mondrian 算法与泛化规则。
3. 查看 [examples.md](./examples.md) 或运行 [examples/kano_usage.py](./examples/kano_usage.py) 快速上手。
4. 开发时参考 [api_reference.md](./api_reference.md)。
5. 部署与排障参考 [ops.md](./ops.md)。
6. 编写测试参考 [testing.md](./testing.md)。

## 运行示例

```bash
cd /path/to/PrivShield
source .venv/bin/activate
PYTHONPATH=. python docs/k_anonymity/examples/kano_usage.py
```