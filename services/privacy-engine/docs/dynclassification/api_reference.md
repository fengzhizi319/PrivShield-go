# 动态分类分级（多标准适配）API 参考手册

本文档提供 `PrivShield` 动态分类分级模块的 Python SDK、REST API 以及 gRPC 接口的完整参考指南。

---


## 1. Python SDK 参考

### 1.1 `models.py` - 元数据与结果模型

#### `SensitivityLevelDef`
动态敏感度等级定义模型。

```python
class SensitivityLevelDef(BaseModel):
    id: str                    # 级别标识（如 "L1", "C4", "LEVEL_3"）
    name: str                  # 显示名称（如 "高敏感数据"）
    rank: int                  # 排序权重（用于 max_level 比较）
    description: Optional[str] = None # 等级描述说明
```

#### `CategoryDef`
动态分类类别定义模型。

```python
class CategoryDef(BaseModel):
    id: str                    # 分类 ID（如 "PERSONAL_BASIC"）
    name: str                  # 分类名称（如 "个人基本信息"）
    parent_id: Optional[str] = None # 父分类 ID（支持层级树形结构）
    description: Optional[str] = None
```

#### `ConfidencePolicy`
置信度衰减与 LLM 触发策略配置。定义多层引擎之间的冲突判定规则、置信度衰减系数，以及触发 Layer-2 NER 和 Layer-3 LLM 的置信度阈值。支持从环境变量进行全局运维配置控制。

```python
class ConfidencePolicy(BaseModel):
    conflict_confidence: float = 0.5        # 冲突时的降级置信度 (alias: conflictConfidence)
    conflict_needs_review: bool = True      # 冲突时是否标记人工复核 (alias: conflictNeedsReview)
    enable_llm_arbitration: bool = True     # 是否启用 LLM 仲裁（默认读 PRIVACY_LLM_ENABLE_ARBITRATION）
    llm_confidence_threshold: float = 0.75  # LLM 触发阈值：置信度低于此值触发（默认读 PRIVACY_LLM_CONFIDENCE_THRESHOLD）
    enable_ner: bool = False                # 是否启用 NER 层（默认读 PRIVACY_NER_ENABLE）
    enable_llm: bool = False                # 是否显式启用 LLM 层（默认读 PRIVACY_LLM_ENABLE）
    auto_llm_on_image: bool = True          # 检测到图片病例/图像字段时自动触发多模态 LLM 层（默认读 PRIVACY_LLM_AUTO_ON_IMAGE）
    ner_trigger_max_rank: int = 3           # NER 触发阈值：当前等级 rank <= 此值时触发
    min_tag_confidence: float = 0.5         # 参与最终等级裁定的最低标签置信度（低于此值的标签仅作审计记录）
```

#### `DomainTaxonomy`
完整领域分类体系定义模型。

```python
class DomainTaxonomy(BaseModel):
    domain: str                # 领域标识（如 "healthcare", "finance"）
    standard_id: str           # 标准编号（如 "DB51_T_2989", "JR_T_0197"）
    version: str = "1.0.0"     # 版本号
    levels: dict[str, SensitivityLevelDef]
    categories: dict[str, CategoryDef]
    default_level: str = "L3"
    confidence_policy: Optional[ConfidencePolicy] = None  # 置信度策略配置
    ner_entity_mapping: Optional[dict[str, str]] = None   # NER 实体类型→等级 ID 映射
    ner_sensitive_keywords: Optional[list[str]] = None    # NER 敏感关键词列表
    ner_label_mapping: Optional[dict[str, str]] = None    # NER 原始标签→标准标签映射
    ner_model_path: Optional[str] = None                  # NER 模型文件路径
    ner_vocab_path: Optional[str] = None                  # NER 词表文件路径
    llm_model_path: Optional[str] = None                  # LLM 模型目录路径
    llm_arbitration_prompt_template: Optional[str] = None # LLM 仲裁 prompt 模板
    llm_classify_prompt_template: Optional[str] = None    # LLM 分类 system prompt 模板（支持 {domain}/{standard_id}/{levels_desc}）

    def max_level(self, *level_ids: str) -> str:
        """返回给定等级列表中 rank 最高等级的 ID。"""

    def get_category_path(self, category_id: str) -> list[str]:
        """获取指定分类从根到节点的完整路径。"""
```

#### `SecurityTag`
安全标签，描述单次规则/算子命中的分类结果，用于审计追溯。

```python
class SecurityTag(BaseModel):
    level: str                          # 敏感度等级 ID（如 "L3", "C4"）
    category: str                       # 分类类别 ID（如 "PII_ID_CARD", "GENOMIC"）
    confidence: float = 1.0             # 置信度 [0,1]（规则引擎确定性命中恒为 1.0）
    source_engine: str = "RULE"         # 来源引擎标识: RULE | COMPOSITE (alias: sourceEngine)
    rule_id: str = ""                   # 触发的规则 ID（审计用） (alias: ruleId)
    domain: str = ""                    # 所属领域
    standard_id: str = ""               # 所属标准 (alias: standardId)
    version: str = "1.0.0"              # 标签 schema 版本
    needs_human_review: bool = False    # 是否需人工复核（如低置信 ML 结果） (alias: needsHumanReview)
    is_override: bool = False           # 是否为覆盖型降级标签（可压制低 rank 普通标签） (alias: isOverride)
    is_downgrade: bool = False          # 是否由降级规则产生 (alias: isDowngrade)
    match_target: str = "field_name"    # 匹配目标: field_name | field_value（值级命中豁免覆盖压制） (alias: matchTarget)
```

#### `FieldClassificationResult`
单个字段（列）的完整分类结果。

```python
class FieldClassificationResult(BaseModel):
    field_name: str                     # 字段名称 (alias: fieldName)
    field_value: Optional[str] = None   # 字段示例值（展示/调试用，可能截断） (alias: fieldValue)
    tags: list[SecurityTag]             # 所有命中的安全标签列表
    final_level: str                    # 最终裁定的敏感度等级 (alias: finalLevel)
    confidence: float = 0.0             # 综合置信度（冲突衰减或 LLM 修正后） [0,1]
    needs_human_review: bool = False    # 是否需人工复核 (alias: needsHumanReview)
    engine_layer: str = "L1_RULE"       # 产出最终决策的引擎层级: L1_RULE | L2_SMALL_NER | L3_LLM (alias: engineLayer)
    reasoning: str = ""                 # 分类推理说明（LLM 层填充详细推理过程）
    sanitized_value: Optional[str] = None  # 智能抹平/脱敏后的字段值（sanitize=True 时填充） (alias: sanitizedValue)
    suppressed_tags: list[SecurityTag]  # 被 override 压制的标签列表（审计用） (alias: suppressedTags)
```

#### `RecordClassificationResult`
单条记录（多字段）的分类结果。

```python
class RecordClassificationResult(BaseModel):
    record_index: int = 0               # 记录在批次/表中的零基索引 (alias: recordIndex)
    field_results: dict[str, FieldClassificationResult]  # 各字段结果: field_name -> FieldClassificationResult (alias: fieldResults)
    aggregated_tags: list[SecurityTag]  # 全字段聚合标签 + 复合规则标签 (alias: aggregatedTags)
    final_level: str                    # 记录级最终等级（各字段 final_level 取 max + 复合升级） (alias: finalLevel)
    confidence: float = 0.0             # 记录级综合置信度 [0,1]
    needs_human_review: bool = False    # 任一字段或复合标签需人工复核 (alias: needsHumanReview)
```

#### `TableClassificationResult`
整张表/批次的分类结果。

```python
class TableClassificationResult(BaseModel):
    schema_: list[str]                  # 表列名（schema） (alias: schema)
    record_results: list[RecordClassificationResult]  # 所有行的记录级结果 (alias: recordResults)
    aggregated_tags: list[SecurityTag]  # 跨记录聚合标签 (alias: aggregatedTags)
    final_level: str                    # 表级最终敏感等级（各记录 final_level 取 max） (alias: finalLevel)
    confidence: float = 0.0             # 表级综合置信度 [0,1]
    needs_human_review: bool = False    # 任一记录需人工复核 (alias: needsHumanReview)
```

#### `AuditInfo`
审计信息，记录分类请求的执行元数据。

```python
class AuditInfo(BaseModel):
    version: str = "1.0.0"              # 审计结构 schema 版本
    domain: str = ""                    # 分类时的领域上下文
    standard_id: str = ""               # 分类时的标准上下文 (alias: standardId)
    timestamp: str = "ISO8601 UTC"      # 分类执行时间（UTC）
    rule_set_version: str = "1.0.0"     # 评估所用规则集版本 (alias: ruleSetVersion)
    rules_evaluated: int = 0            # 本次请求评估的规则总数 (alias: rulesEvaluated)
    rules_hit: int = 0                  # 实际命中的规则数 (alias: rulesHit)
    duration_ms: float = 0.0            # 执行耗时（毫秒） (alias: durationMs)
```

#### `ClassificationResponse`
分类响应包装器，根据请求粒度恰好包装 field_result / record_result / table_result 中的一个。

```python
class ClassificationResponse(BaseModel):
    field_result: Optional[FieldClassificationResult] = None   # 字段级请求 (alias: fieldResult)
    record_result: Optional[RecordClassificationResult] = None # 记录级请求 (alias: recordResult)
    table_result: Optional[TableClassificationResult] = None   # 表级请求 (alias: tableResult)
    audit_info: AuditInfo = AuditInfo()                        # 执行元数据（恒存在） (alias: auditInfo)
```

---

### 1.2 `operator_registry.py` - 算子注册表

#### `OperatorRegistry`
匹配算子单例注册表。

```python
class OperatorRegistry:
    @classmethod
    def register(cls, name: str):
        """算子注册装饰器。"""

    @classmethod
    def register_func(cls, name: str, func: MatcherOperator) -> None:
        """运行时动态注册算子。"""

    @classmethod
    def get(cls, name: str) -> MatcherOperator:
        """获取已注册算子函数，若不存在则抛出 KeyError。"""

    @classmethod
    def list_operators(cls) -> list[str]:
        """获取所有已注册的算子名称列表。"""
```

#### 内置标准算子表 (`operators.py`)

| 算子名称 (`operator`) | 描述 | 参数支持 (`params`) |
|---|---|---|
| `regex` | 正则表达式匹配算子 | `pattern` (str): 正则匹配表达式 |
| `keyword_contains` | 归一化子串包含匹配 | `keywords` (list[str]): 关键词列表；`use_word_boundaries` (bool): 是否使用单词边界匹配（默认 False） |
| `prefix_match` | 前缀匹配 | `prefixes` (list[str]): 前缀字符串列表 |
| `suffix_match` | 后缀匹配 | `suffixes` (list[str]): 后缀字符串列表 |
| `id_card_checksum` | GB 11643 身份证校验码算子 | 无 |
| `medical_card_checksum` | 医保卡号算法算子 | 无 |
| `luhn_checksum` | 银行卡 Luhn 算法校验算子 | `min_length`, `max_length` |
| `icd10_range` | ICD-10 编码区间及级别提升判定 | `default_level`, `upgrade_level`, `intervals` |
| `length_range` | 字符串长度区间匹配算子 | `min_length`, `max_length` |
| `exact_match` | 精确匹配算子 | `values` (list[str]) |
| `ip_address` | IP 地址正则匹配算子 | 无 |
| `mac_address` | MAC 地址匹配算子 | 无 |
| `chinese_name` | 中文姓名校验匹配算子 | 无 |
| `email` | 邮箱地址正则匹配算子 | 无 |

---

### 1.3 `engine.py` - 通用规则引擎

#### `ConfigurableRuleEngine`

```python
class ConfigurableRuleEngine:
    def __init__(self, taxonomy: DomainTaxonomy, profiles: list[RuleProfile], domain: str = "", standard_id: str = ""):
        """根据给定的元数据体系和规则包列表初始化引擎。"""

    def evaluate(
        self, field_name: str, value: Any, context: dict[str, Any] | None = None
    ) -> tuple[list[SecurityTag], list[SecurityTag]]:
        """评估单个字段，返回 (final_tags, suppressed_tags) 元组。
        
        final_tags: 最终生效的安全标签列表
        suppressed_tags: 被降级规则压制的标签列表（用于审计）
        """
```

---

### 1.4 `profile_loader.py` - 配置加载与管理

#### `ProfileLoader`

```python
class ProfileLoader:
    def __init__(self, rules_dir: str | Path = "rules"):
        """初始化 ProfileLoader。"""

    def get_engine(
        self, domain: Optional[str] = None, standard: Optional[str] = None
    ) -> ConfigurableRuleEngine:
        """根据 domain 或 standard 获取或构建配置化引擎实例。"""

    def invalidate_cache(self) -> None:
        """清除缓存，实现配置热重载。"""
```

---

## 2. REST API 接口定义

### 2.1 动态分类求值接口
- **Endpoint**: `POST /v1/dynclassification/eval`
- **Content-Type**: `application/json`

#### 请求体格式
```json
{
  "fieldName": "user_id_card",
  "value": "510104199003072345",
  "domain": "general-pii",
  "standard": "gbt35273"
}
```

#### 响应体格式
```json
{
  "fieldResult": {
    "fieldName": "user_id_card",
    "fieldValue": "510104199003072345",
    "tags": [
      {
        "level": "L3",
        "category": "PERSONAL_BASIC",
        "confidence": 1.0,
        "sourceEngine": "RULE",
        "ruleId": "RULE_PII_IDCARD",
        "domain": "general-pii",
        "standardId": "gbt35273",
        "version": "1.0.0",
        "needsHumanReview": false,
        "isOverride": false,
        "isDowngrade": false,
        "matchTarget": "field_value"
      }
    ],
    "finalLevel": "L3",
    "confidence": 1.0,
    "needsHumanReview": false,
    "engineLayer": "L1_RULE",
    "reasoning": "命中规则: RULE_PII_IDCARD",
    "sanitizedValue": null,
    "suppressedTags": []
  },
  "recordResult": null,
  "tableResult": null,
  "auditInfo": {
    "version": "1.0.0",
    "domain": "general-pii",
    "standardId": "gbt35273",
    "timestamp": "2026-08-14T00:00:00+00:00",
    "ruleSetVersion": "1.0.0",
    "rulesEvaluated": 12,
    "rulesHit": 1,
    "durationMs": 0.235
  }
}
```

> 说明：响应体为 `ClassificationResponse` 包装器，`fieldResult` / `recordResult` / `tableResult` 三者恰好返回一个（取决于请求粒度），`auditInfo` 恒存在。所有字段采用 camelCase alias 输出（如 `finalLevel`、`needsHumanReview`）。

##### 记录级求值
- **Endpoint**: `POST /v1/dynclassification/eval_record`

```json
{
  "record": { "user_id_card": "510104199003072345", "age": 35, "diagnosis": "高血压" },
  "domain": "general-pii",
  "standard": "gbt35273"
}
```

响应中 `recordResult` 填充 `RecordClassificationResult`（含 `fieldResults`、`recordIndex`、`aggregatedTags`、`finalLevel` 等）。

##### 表级求值
- **Endpoint**: `POST /v1/dynclassification/eval_table`

```json
{
  "schema": ["user_id_card", "age", "diagnosis"],
  "rows": [
    { "user_id_card": "510104199003072345", "age": 35, "diagnosis": "高血压" },
    { "user_id_card": "510104199003072346", "age": 42, "diagnosis": "糖尿病" }
  ],
  "domain": "general-pii",
  "standard": "gbt35273"
}
```

响应中 `tableResult` 填充 `TableClassificationResult`（含 `recordResults`、`schema`、`aggregatedTags`、`finalLevel` 等）。

---

### 2.2 规则配置热加载接口
- **Endpoint**: `POST /v1/dynclassification/profiles/reload`

#### 响应体格式
```json
{
  "status": "ok",
  "message": "Classification profiles and engines reloaded successfully"
}
```

---

### 2.3 获取可用的标准列表
- **Endpoint**: `GET /v1/dynclassification/standards`

#### 响应体格式
```json
{
  "standards": [
    {
      "standard_id": "sc_health_db51",
      "description": "DB51/T 2989—2023 四川省健康医疗大数据应用指南",
      "taxonomy": "default",
      "domains": ["general-pii", "medical"]
    },
    {
      "standard_id": "jrt0197",
      "description": "JR/T 0197-2020 金融数据安全分级指南",
      "taxonomy": "finance_jrt0197",
      "domains": ["general-pii", "finance"]
    }
  ]
}
```

---

### 2.4 获取可用匹配算子列表
- **Endpoint**: `GET /v1/dynclassification/operators`

#### 响应体格式
```json
{
  "operators": [
    "regex",
    "keyword_contains",
    "prefix_match",
    "suffix_match",
    "id_card_checksum",
    "medical_card_checksum",
    "luhn_checksum",
    "icd10_range",
    "length_range",
    "exact_match",
    "ip_address",
    "mac_address",
    "chinese_name",
    "email"
  ]
}
```

> 说明：以上为完整算子清单（与 `operators.py` 中注册的 14 个算子一致）；实际以接口实时返回为准。

---

## 3. gRPC 协议声明

在 `proto/privacy.proto` 中定义动态分类请求与响应结构：

```protobuf
message DynClassificationRequest {
  string field_name = 1;
  string field_value = 2;
  string domain = 3;
  string standard = 4;
}

message DynSecurityTagProto {
  string level = 1;
  string category = 2;
  string rule_id = 3;
  string source_engine = 4;
  string domain = 5;
  string standard_id = 6;
  bool is_override = 7;    // 是否为强制覆盖标签 / Whether this is a forced override tag
  bool is_downgrade = 8;   // 是否由降级规则产生 / Whether produced by a downgrade rule
  string match_target = 9; // 匹配目标: field_name | field_value / Match target
}

message DynClassificationResponse {
  repeated DynSecurityTagProto tags = 1;
  string max_level = 2;
  string audit_timestamp = 3;
  string engine_layer = 4;
}
```

gRPC RPC 方法：
```protobuf
rpc DynClassify (DynClassificationRequest) returns (DynClassificationResponse);
```
