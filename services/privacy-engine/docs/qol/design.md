# 查询混淆设计文档


## 1. 概述

本文档定义 `PrivShield` 查询混淆模块的算法原理与实现细节。查询混淆通过将真实查询与虚假查询混合，降低查询日志分析攻击导致的隐私泄露风险。

## 2. 设计目标

- 将单个真实查询混入若干虚假查询。
- 支持批量查询混淆。
- 提供可扩展的 dummy 查询池机制。
- 暴露 `privacy_qol_operations_total` 指标。
- **工业化增强**：结构化日志、输入校验、枚举类型安全、策略追踪。

## 3. 算法原理

1. **模版识别与语义槽位替换（Slot-Filling）**：
   - 提取真实查询中包含的语义敏感词（医疗领域的疾病如“高血压”，或通用领域的业务实体如“公积金”）。
   - 将敏感词替换为占位符得到语义模版（例如：“如何治疗{disease}”）。
   - 从对应的疾病词库 `DISEASES` 或通用实体词库 `ENTITIES` 中，随机选择 `num_dummies` 个不重复的实体填充该模版，动态构造高拟真度的虚假查询。
2. **长度特征防御（Fallback & Filter）**：
   - 若真实查询不包含任何已知敏感词，或使用槽位生成的数量未达标，则退化为在默认词库池中筛选与真实查询长度相近的查询（如字数差在 ±6 到 ±12 字符内），消除根据句长进行虚假流量特征分析的隐患。
3. **位置随机打散**：
   - 在 `[0, num_dummies]` 范围内随机选择插入位置，将真实查询混入，返回长度为 `num_dummies + 1` 的列表。

## 4. Dummy 查询池

### 4.1 内置池与实体库

- **medical**（医疗池）：扩展到 20 种高发病症及常见处方日常防治词条。
- **generic**（通用池）：扩展到 20 项市政、社保、公积金线上提取、个人税报销等高频行政办理词条。
- **DISEASES**（疾病实体库）：包含“脑梗塞”、“胃溃疡”等 15 个临床敏感疾病词，用于槽位分析。
- **ENTITIES**（业务实体库）：包含“社保卡”、“公积金”等 10 个高频业务术语，用于槽位分析。

### 4.2 自定义池

通过 `medical_pool` / `generic_pool` 参数传入自定义列表，覆盖内置池。

## 5. 批量混淆

`obfuscate_query_batch(queries, ...)` 对每个查询独立调用 `obfuscate_query`，返回列表的列表。

## 6. 指标

`privacy_qol_operations_total{domain}`：

| `domain` | 触发场景 |
|---|---|
| `medical` | `domain="medical"` 时 |
| `generic` | `domain="generic"` 时 |

## 7. 工业化增强特性

### 7.1 结构化日志

模块使用 `get_logger(__name__)` 创建结构化日志记录器，每次操作记录上下文信息：

```python
logger.info(
    "qol_obfuscate_query_completed",
    extra={
        "domain": domain,
        "num_dummies": num_dummies,
        "strategy": strategy_used,
        "real_query_index": pos,
    },
)
```

### 7.2 输入校验

所有公开接口均内置参数校验，快速失败并给出清晰错误信息：

- `obfuscate_query`: 校验 query 非空、num_dummies >= 1、domain 有效
- `obfuscate_query_batch`: 校验 queries 为非空列表

### 7.3 枚举类型安全

提供 `ObfuscationDomain` 和 `ObfuscationStrategy` 枚举用于类型安全：

```python
from engine.privacy.qol import ObfuscationDomain, ObfuscationStrategy

assert ObfuscationDomain.MEDICAL == "medical"
assert ObfuscationStrategy.SLOT_FILLING == "slot_filling"
```

### 7.4 策略追踪

模块自动追踪并记录使用的混淆策略：

| 策略 | 描述 |
|---|---|
| `slot_filling` | 语义槽位替换（匹配到实体词） |
| `length_similarity` | 长度相近抽样（未匹配实体） |
| `hybrid` | 混合策略（部分槽位 + 部分长度抽样） |

## 8. 模块设计

- `privacy-go-sdk/qol/qol.go`：核心混淆逻辑（Fisher-Yates 语义置乱，同构语义池注入）。
- `engine-go/internal/service/service.go`：`PrivacyService` 统一编排。
- `engine-go/internal/rest/routes.go` / `engine-go/internal/grpcserver/server.go`：REST (Gin) / gRPC (RawCodec) 接口。

## 9. 测试策略

- 单条查询混淆结果包含真实查询。
- 批量查询混淆返回数量正确。
- 自定义 pool 生效。
- `seed` 参数可复现结果。
- 指标递增测试。
- REST/gRPC 接口测试。
- **枚举类型测试**：ObfuscationDomain、ObfuscationStrategy 枚举值验证。
- **输入校验测试**：空查询、无效 num_dummies、无效 domain 等边界条件。

---