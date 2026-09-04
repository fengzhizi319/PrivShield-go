# 查询混淆产品需求文档


## 1. 背景与目标

查询日志分析可能泄露用户真实查询意图。本模块通过向真实查询中注入虚假查询，降低攻击者从日志中识别真实查询的概率。

## 2. 需求列表

| ID | 需求 | 优先级 |
|---|---|---|
| QOL-1 | 提供单条查询混淆接口 | P0 |
| QOL-2 | 支持医疗和通用两类内置 dummy 查询池 | P0 |
| QOL-3 | 支持自定义 dummy 查询池 | P1 |
| QOL-4 | 支持批量查询混淆 | P1 |
| QOL-5 | 支持随机种子以复现结果 | P1 |
| QOL-METRIC-1 | 暴露 `privacy_qol_operations_total` 计数器 | P1 |
| QOL-API-1 | 提供 REST `/v1/privacy/qol/obfuscate` 与 `/v1/privacy/qol/obfuscate/batch` | P1 |
| QOL-GRPC-1 | 提供 gRPC `ObfuscateQuery` 与 `ObfuscateQueryBatch` | P1 |

## 3. 功能设计

### 3.1 单条混淆

- 输入：真实查询字符串、dummy 数量、领域。
- 输出：包含真实查询与 `num_dummies` 条虚假查询的列表。
- 真实查询位置在 `[0, num_dummies]` 范围内随机选择。

### 3.2 批量混淆

- 对每条查询独立调用单条混淆。
- 返回与输入数量相同的列表的列表。

### 3.3 Dummy 池

- 内置 medical / generic 两类池。
- 支持通过参数自定义。

## 4. 非功能需求

- 无外部依赖，纯 Python 实现。
- 响应延迟低于 10ms（单条）。
- 支持指标监控。

## 5. 接口范围

### REST

- `POST /v1/privacy/qol/obfuscate`
- `POST /v1/privacy/qol/obfuscate/batch`

### gRPC

- `ObfuscateQuery`
- `ObfuscateQueryBatch`