# 降级规则强制覆盖能力设计


## 1. 背景与问题

### 1.1 现状

当前降级规则（Downgrade Rules）的执行逻辑为：

```
evaluate(field_name, value):
  Phase 1: 普通规则 → normal_tags = [L5, L4, L3, ...]
  Phase 2: 降级规则 → downgrade_tags = [L2, L1, ...]
  Phase 3: 合并去重 → return normal_tags + downgrade_tags

_resolve_final_level(tags):
  return max_level(*levels)  ← 取所有标签中最高
```

由于最终等级裁定采用"取最高"策略，降级规则产出的低等级标签**无法覆盖**普通规则产出的高等级标签。

### 1.2 问题场景

| 场景 | 普通规则 | 降级规则 | 最终等级 | 期望等级 | 是否符合预期 |
|---|---|---|---|---|---|
| 无规则命中 | 无 | L2 | L2 | L2 | ✅ |
| 宽泛规则误中 | L3（关键词 "report"） | L2（运营字段） | **L3** | **L2** | ❌ |
| 高敏感规则命中 | L5（基因组） | L2（运营字段） | L5 | L5 | ✅ |

核心矛盾：当普通规则因关键词过于宽泛而"误中"运营/公开字段时，降级规则无力修正。

### 1.3 设计目标

- 保持 `default_level: L3` 不变（医疗行业安全优先原则）
- 为降级规则增加**可选的强制覆盖能力**
- 完全向后兼容：`override` 默认为 `false`，不改变现有行为
- 覆盖有安全边界：不能压制超过指定等级上限的规则结果

## 2. 方案设计

### 2.1 数据模型变更

`DowngradeRuleDef` 新增两个字段：

```python
class DowngradeRuleDef(BaseModel):
    # ... 现有字段 ...
    
    # 是否启用强制覆盖：为 true 时可压制 rank <= max_override_rank 的普通规则标签
    override: bool = Field(default=False, description="是否启用强制覆盖")
    # 覆盖等级上限：仅能压制 rank <= 此值对应 rank 的普通标签（默认与 level 相同）
    # 为空时默认使用 level 字段的 rank 作为上限
    max_force_suppress_level: str = Field(default="", description="覆盖等级上限（空=使用 level）")
```

### 2.2 SecurityTag 变更

`SecurityTag` 新增标记字段：

```python
class SecurityTag(BaseModel):
    # ... 现有字段 ...
    
    # 标记此标签是否来自具有强制覆盖能力的降级规则
    is_override: bool = Field(default=False, alias="isOverride", description="是否为覆盖型降级标签")
```

### 2.3 引擎执行流程变更

```
evaluate(field_name, value):
  ┌────────────────────────────────────────────────────────────────────┐
  │ Phase 1: 普通规则评估 → normal_tags                                │
  │ Phase 2: 降级规则评估 → downgrade_tags                             │
  │                                                                    │
  │ Phase 3 (新增): 强制覆盖裁定                                       │
  │   对每条 override=true 的降级标签:                                  │
  │     计算 override_cap_rank = taxonomy.get_level_rank(max_override)  │
  │     从 normal_tags 中移除 rank <= override_cap_rank 的标签          │
  │     （被移除的标签记入 suppressed_tags 用于审计）                    │
  │                                                                    │
  │ Phase 4: 合并 normal_tags + downgrade_tags → 去重 → return         │
  └────────────────────────────────────────────────────────────────────┘
```

### 2.4 安全保障

| 保障措施 | 说明 |
|---|---|
| 默认关闭 | `override` 默认 `false`，不影响现有行为 |
| 等级上限 | 只能压制 rank ≤ 上限的标签，L5 级规则永远不会被降级 |
| 审计追踪 | 被压制的标签记录在日志中，可追溯 |
| 显式配置 | 必须在 YAML 中明确写 `override: true` 才生效 |

### 2.5 YAML 配置示例

```yaml
downgrade_rules:
  # 非覆盖型（默认行为，仅作为兜底归属）
  - id: "RULE_DOWN_PUBLIC"
    name: "公开数据降级"
    keywords: ["public_report", "annual_summary", "科普"]
    level: "L1"
    category: "PUBLIC_REPORT"

  # 覆盖型（可压制误中的普通规则）
  - id: "RULE_DOWN_OPS"
    name: "运营统计强制降级"
    keywords: ["turnover_rate", "device_usage", "inventory", "门诊人次"]
    level: "L2"
    category: "OPERATIONAL_STAT"
    override: true              # 启用强制覆盖
    max_force_suppress_level: "L3"    # 仅能压制 L3 及以下的普通规则
```

## 3. 影响范围

| 文件 | 变更类型 | 说明 |
|---|---|---|
| `rule_schema.py` | 模型扩展 | `DowngradeRuleDef` 新增 `override`、`max_force_suppress_level` |
| `validator.py` | 校验提示 | 新增对 `force_suppress=true` 未配置 `max_force_suppress_level` 的配置引导提示 |
| `models.py` | 模型扩展 | `SecurityTag` 新增 `is_override` |
| `engine.py` | 逻辑增强 | `evaluate()` 新增 Phase 3 覆盖裁定 |
| `service.py` | 无需修改 | `_resolve_final_level` 仍取 max，覆盖已在引擎层完成 |
| `rules/domains/*.yaml` | 配置更新 | 添加 `override: true` 示例 |

## 4. 测试策略

| 测试场景 | 验证点 |
|---|---|
| override=false（默认） | 行为与修改前完全一致 |
| override=true + 普通规则 L3 | L3 标签被压制，最终等级为降级目标 L2 |
| override=true + 普通规则 L5 | L5 标签不被压制（超出上限），最终等级仍为 L5 |
| override=true + 无普通规则 | 行为与兜底模式一致 |
| 多条覆盖规则同时命中 | 取最严格的覆盖（最低等级） |
| 向后兼容 | 无 override 字段的旧 YAML 正常加载 |