# 三层漏斗模型 + 置信度策略设计

> 本文档与实现代码 `engine/dynclassification/funnel.py`、`models.py` 保持同步（最后对齐：2026-08）。

## 1. 背景

### 1.1 现状

`dynclassification` 模块已包含完整的三层漏斗架构（Rule → NER → LLM）。
旧模块 `privacy/classification/` 已删除（commit `ddc5b0e`），其三层漏斗逻辑已迁移至
`dynclassification`（`funnel.py`、`ner_engines.py`、`llm_engines.py` 等）。

### 1.2 目标

1. 为 `dynclassification` 增加三层漏斗模型（Rule → NER → LLM）
2. 实现置信度衰减策略（规则冲突时降低置信度）
3. 实现 LLM 仲裁能力（冲突时由 LLM 裁定）
4. 默认等级按标准独立配置（已有，无需修改）

---

## 2. 架构设计

### 2.1 三层漏斗执行流程

```
classify_field(field_name, value, sanitize=False)
  │
  ▼
┌─────────────────────────────────────────────────────────────────────┐
│  Layer-1: ConfigurableRuleEngine (确定性规则, 零延迟)                │
│    Phase 1: 普通规则评估 → normal_tags                              │
│    Phase 2: 降级规则评估 → downgrade_tags                           │
│    Phase 3: Override 压制 → 移除低等级普通标签                      │
│    Phase 4: 合并去重 → rule_tags                                    │
│    补全扫描: L5 高敏医疗模式 (confidence=0.99, 强制人工复核) /       │
│              L4 高敏医疗模式 (confidence=0.95)                      │
│    confidence = max(命中规则置信度), 未命中则为 0.0                  │
│    (规则标签恒为确定性 1.0; RuleDef 无 confidence 字段)              │
└─────────────────────────────────────────────────────────────────────┘
  │
  │ 触发条件: enable_ner=true AND NER 适配器可用
  │           AND 通过智能门禁 (_should_trigger_ner)
  │           AND (无标签 OR 当前等级 rank <= ner_trigger_max_rank)
  ▼
┌─────────────────────────────────────────────────────────────────────┐
│  Layer-2: Small-NER 实体识别 (毫秒级延迟)                           │
│    提取医疗实体: 疾病/药物/手术/身体部位/基因提示                   │
│    映射为 SecurityTag (source_engine="SMALL_NER")                   │
│    confidence = NER 模型原始 softmax 概率 (无截断/下限保障,          │
│                 多 token 实体取 min; 缺失时回退 0.8)                │
│    engine_layer 归属: 仅当 NER 实际影响决策时更新为 L2_SMALL_NER     │
│      (L1 无标签时 NER 提供首个分类结果，或 NER 等级高于 L1 结果)     │
└─────────────────────────────────────────────────────────────────────┘
  │
  │ 触发条件（三选一，按优先级短路）:
  │   场景A: 存在规则冲突 AND enable_llm_arbitration
  │   场景C: 检测到图像/影像输入 AND auto_llm_on_image
  │   场景B: confidence < llm_confidence_threshold AND enable_llm
  ▼
┌─────────────────────────────────────────────────────────────────────┐
│  Layer-3: LLM 仲裁/深度分类 (秒级延迟, 可选)                       │
│    场景A: 规则冲突仲裁 → 裁定最终等级 + 修正置信度                  │
│    场景B: 低置信度兜底 → 深度语义理解分类                           │
│    场景C: 图像多模态识别 → 视觉深度分析分级                         │
│    confidence = LLM 输出置信度 (0.0~1.0, 非数值时回退上游置信度)     │
│    reasoning = LLM 推理过程                                         │
└─────────────────────────────────────────────────────────────────────┘
  │
  ▼
┌─────────────────────────────────────────────────────────────────────┐
│  置信度策略 (Confidence Policy)                                      │
│    无冲突: confidence = 1.0, needs_human_review = false             │
│    有冲突 + 无LLM: confidence = conflict_confidence (默认 0.5)      │
│                    needs_human_review = true                         │
│    有冲突 + 有LLM: confidence = LLM 输出, 等级 = LLM 裁定          │
│      (裁定等级必须落在冲突标签等级集合内, 否则拒绝并人工复核)        │
│    LLM 返回无合法等级: 保留上游置信度/层级归属,                      │
│      needs_human_review = true (见 2.6 安全地板第 3 条)              │
└─────────────────────────────────────────────────────────────────────┘
```

#### Layer-2 NER 智能门禁 (`_should_trigger_ner`)

遵循"简单规则先行，复杂长文本才用 NER"原则，避免 NER 对结构化短字段产生无效开销：

1. **排除空值/超短文本**：去空白后长度 < 2 不触发。
2. **排除纯数字/纯英文**：必须包含 ≥2 个连续中文汉字。
3. **排除 PII 结构化短字段**：`id_card_no`、`phone`、`name`、`patient_name`、`age`、`gender`、`sex`、`medical_insurance_no`、`social_security_no`、`disability_cert_no`、`registered_address`、`house_address`、`contact_phone`、`guardian_phone` 等直接复用 L1 规则。
4. **临床非结构化文书字段强制触发**：`chief_complaint`、`present_illness`、`past_history`、`personal_history`、`family_history`、`allergic_history`、`progress_note`、`diagnosis_name`、`diagnosis`。
5. 其余通过中文检查的文本默认触发。

### 2.2 冲突检测逻辑

```python
# 冲突定义: 普通规则标签和降级规则标签同时存在，且两者最高等级不一致。
# 若两者等级相同（如均为 L2），说明无实质矛盾，不判定为冲突。
normal_rule_tags = [t for t in tags if t.source_engine == "RULE" and not t.is_downgrade]
downgrade_tags = [t for t in tags if t.is_downgrade]

if normal_rule_tags and downgrade_tags:
    normal_max = taxonomy.max_level(*(t.level for t in normal_rule_tags))
    downgrade_max = taxonomy.max_level(*(t.level for t in downgrade_tags))
    has_conflict = normal_max != downgrade_max
else:
    has_conflict = False
```

> **注意**：降级标签通过 `SecurityTag.is_downgrade` 标志识别（降级规则产出时打标），
> 而非依赖 `is_override` 或 `source_engine` 字符串判断。

### 2.3 置信度策略配置 (taxonomy YAML)

```yaml
# rules/taxonomies/default.yaml（与实际配置文件一致，使用 snake_case 字段名）
default_level: "L3"

confidence_policy:
  conflict_confidence: 0.7        # 规则冲突且 LLM 不可用时的降级置信度（代码默认 0.5）
  conflict_needs_review: true     # 冲突时标记人工复核
  enable_llm_arbitration: false   # 是否启用 LLM 仲裁（env: PRIVACY_LLM_ENABLE_ARBITRATION，代码默认 true，需 ML 镜像）
  llm_confidence_threshold: 0.6   # LLM 触发阈值：置信度低于此值触发低置信度兜底（env: PRIVACY_LLM_CONFIDENCE_THRESHOLD，代码默认 0.75）
  enable_ner: false               # 是否启用 NER 层（env: PRIVACY_NER_ENABLE，默认 false）
  enable_llm: false               # 是否显式启用 LLM 深度分类（env: PRIVACY_LLM_ENABLE，默认 false）
  auto_llm_on_image: true         # 检测到图像/影像时自动触发多模态 LLM（env: PRIVACY_LLM_AUTO_ON_IMAGE，默认 true）
  ner_trigger_max_rank: 3         # NER 触发阈值：当前等级 rank <= 此值才触发 NER（C1~C4/G1~G4 四级体系建议设 2）
  min_tag_confidence: 0.5         # 参与最终等级裁定的最低标签置信度（低于此值仅作审计记录）
```

字段同时支持 camelCase 别名（如 `conflictConfidence`）与 snake_case 双向填充
（`populate_by_name=True`），布尔/数值类字段支持环境变量全局运维覆盖。
当前 `rules/taxonomies/` 下共四个已发布配置：`default` / `gd_health` / `finance_jrt0197` / `sc_health_db51`
均已按 AGENTS.md §9.3 显式定义 `confidence_policy` 节，采用 `conflict_confidence: 0.7`、`llm_confidence_threshold: 0.6`、
`enable_llm_arbitration: false` 的生产保守配置；金融/广东医疗体系额外设置
`ner_trigger_max_rank: 2` 限制 NER 仅在低等级时触发。新增任何自定义 taxonomy 时亦应显式定义该节。

### 2.4 Layer-3 Qwen 触发场景详解

第三层 Qwen（`Qwen3.5-0.8B-Privacy-Classifier-Smoother`）作为高精度语义裁决与无痕抹平重写引擎，在以下 3 种场景下被触发（按代码中的分支优先级排列）：

1. **场景 A：规则冲突仲裁 (Rule Conflict Arbitration)**
   - **触发条件**：`has_conflict = True` 且 `policy.enable_llm_arbitration = True` 且 LLM 可用。
   - **典型过程**：文本同时命中升级规则（如"肿瘤/高危病史"为 L4/L5）与降级规则（如"排除诊断/家族史"为 L2/L1），由 Qwen 从冲突等级集合中裁定最终敏感等级。
   - **约束**：裁定等级必须落在冲突标签等级集合内（见 [2.6 安全地板](#26-安全地板防御机制-safety-floor)）。
   - **短路说明**：`has_conflict = True` 时整个分支优先于场景 C/B；若仲裁未启用或 LLM 不可用，直接走置信度衰减，不会再检查图像/低置信条件。

2. **场景 C：图像/影像多模态识别 (Multimodal Image Analysis)**
   - **触发条件**：检测到图像输入且 `policy.auto_llm_on_image = True` 且 LLM 可用。图像判定（`_is_image_field_or_value`）覆盖三类信号：
     - 值以图像扩展名结尾：`.jpg .jpeg .png .bmp .webp .dcm .dicom .tiff`；
     - 值以 `data:image/` 或 `image:` 前缀开头（Base64 内联）；
     - 字段名含图像语义标识（英文 `image/photo/pic/picture/dicom/xray/ct_scan/mri/img` 词边界匹配；中文 `切片/病例图片/影像` 子串匹配），**且值长度 > 3 且非 http(s) URL**（防止把"图片链接字段说明文本"误判为图像）。
   - **典型过程**：对病例图片/DICOM 医学影像执行视觉深度分析，输出敏感分级。

3. **场景 B：低置信度兜底 (Low Confidence Fallback)**
   - **触发条件**：无冲突、非图像，且前两层累计置信度 `confidence < policy.llm_confidence_threshold`（代码默认 `0.75`）且 `policy.enable_llm = True`。
   - **典型过程**：文本未命中明确规则（如"他去拿了那个免疫靶向药"等隐晦语言），Qwen 承担深度语义理解与敏感分级。

> **关于无痕抹平 (Sanitization)**：抹平重写不是独立的 LLM 触发场景，而是贯穿场景 B/C 的
> 横切能力——调用方显式传入 `sanitize=True` 时，LLM 输出中附带 `sanitized_text`
> （如身份证号星号化 `330801********0789`、年龄 k-匿名化区间重写）；
> 图像输入则调用 `image_redaction` 模块生成打码产物，写入 `FunnelResult.sanitized_value`。

### 2.5 置信度 (Confidence Score) 计算与流转推导

置信度（取值 `0.0 ~ 1.0`）是量化评估判定确定性的核心指标，流转过程如下：

#### 1. Layer-1 规则引擎置信度
- **规则标签**（身份证号、手机号等 YAML 规则）：恒为确定性 `confidence = 1.0`
  （`RuleDef` 无 confidence 字段，`SecurityTag` 默认值）。
- **L5 高敏医疗模式**（补全扫描）：`confidence = 0.99`，且标签自带 `needs_human_review=True`。
- **L4 高敏医疗模式**（补全扫描）：`confidence = 0.95`。
- **未命中规则**：`tags` 为空，初始 `confidence = 0.0`。
- **阶段合并**：取所有命中规则的最大置信度 \(\text{confidence}_{\text{L1}} = \max(\{t.\text{confidence}\}, \text{default}=0.0)\)。

#### 2. Layer-2 Small-NER 实体识别置信度
- 提取出的实体归一化映射为 `SecurityTag`，附带 NER 模型**原始 softmax 输出概率**
  （多 token 实体取 token 概率最小值）。代码**不做任何截断/下限钳制**，
  观测值通常落在 `0.60 ~ 0.95` 区间，但这只是经验描述而非保证；
  引擎未返回置信度时回退默认 `0.8`（注意：高于 `min_tag_confidence` 默认 0.5，
  会参与最终等级计算）。
- **阶段合并（最大值覆盖策略）**：

$$\text{confidence}_{\text{L1+L2}} = \max\left(\text{confidence}_{\text{L1}}, \max(\{t.\text{confidence} \mid t \in \text{tags}_{\text{NER}}\})\right)$$

#### 3. 门限比对判定
- 若 \(\text{confidence}_{\text{L1+L2}} \ge 0.75\)（如规则直接命中 `1.0`）：说明足够确定，**直接输出，不调用 LLM**。
- 若 \(\text{confidence}_{\text{L1+L2}} < 0.75\)（如未命中规则为 `0.0`）：系统判定当前不可信，**触发场景 B，调用 Layer-3 LLM**（需 `enable_llm=true`）。

#### 4. Layer-3 LLM 置信度刷新
- Qwen 分析后导出 JSON 中的 `confidence`（如 `0.92`）。
- 经 `_safe_llm_confidence` 安全转换：LLM 可能返回 "极高" 等非数值内容（甚至经由 Prompt 注入构造），
  `float()` 失败时回退上游置信度，保证漏斗流程不崩溃。
- **前置条件**：仅当 LLM 返回了合法 `final_level`（在 taxonomy 内、场景 A 还须在冲突集合内）
  且通过降级校验后，才刷新置信度：\(\text{confidence}_{\text{final}} = \text{confidence}_{\text{LLM}}\)。
  LLM 未返回合法等级时**不刷新置信度**（见 2.6 第 3 条）。

### 2.6 安全地板防御机制 (Safety Floor)

为防止大模型幻觉或 Prompt 注入导致的危险降级放行，系统实施多重校验：

1. **场景 A 冲突集合校验**：LLM 仲裁裁定的等级必须落在冲突标签等级集合
   \(\{t.\text{level} \mid t \in \text{tags}\}\) 内，且必须是 taxonomy 合法等级。
   集合外裁定（如被注入的 LLM 返回任意低等级）一律拒绝，保留规则引擎结果，
   并打上 `needs_human_review = True` 送交人工复核工单。
2. **场景 B/C 拒绝非法降级**：若 Qwen 裁定的敏感等级 rank 低于 Layer-1/Layer-2 已确定的等级，
   系统直接拒绝该降级，保留规则引擎高等级，并打上 `needs_human_review = True`。
3. **场景 B/C 拒绝无等级结果**：LLM 返回结果但未给出合法 `final_level`
   （缺失、空值或 taxonomy 之外的伪造等级）时，视为无效裁定——**不刷新置信度、
   不归属 `L3_LLM`、不追加 LLM 审计标签**，保留上游结果并打上
   `needs_human_review = True`，同时记录 `funnel_llm_no_valid_level` 告警日志。
   该兜底防止"高置信度 + 无等级"的注入输出绕过前两条校验静默抬升整体置信度。
4. **仲裁成功后的一致性保障**：LLM 仲裁成功时，与裁定等级冲突的普通规则标签被移入
   `suppressed_tags`，确保外部对 `tags` 重算 `max_level` 的结果与 `final_level` 一致；
   且 LLM 高置信度（>= `llm_confidence_threshold`）仲裁成功时清除继承的人工复核标记，
   避免不必要的审核工单。

### 2.7 最终等级裁定优先级

```
final_level = LLM 裁定等级（若仲裁/深度分类成功裁定）
            否则 = resolve_level(有效标签)
```

`resolve_level` 的过滤规则：
1. **低置信度标签过滤**：置信度低于 `min_tag_confidence`（默认 0.5）的标签仅作审计记录，
   不参与等级计算——防止低置信度 NER 标签无条件拉高最终等级。
2. **降级标签排除**：当非降级标签存在时，降级标签（`is_downgrade=True`）不参与等级上推；
   但当 override 已压制所有普通标签、仅剩降级标签时，降级标签代表最终裁定，参与计算。
3. **无有效标签**：回退到 taxonomy 的 `default_level`。

### 2.8 Layer-2 Small-NER 分类分级原理与实现详解

本节深入阐述 Layer-2 Small-NER 如何从非结构化医疗文本中提取实体、将实体映射为安全标签、
并最终参与敏感度等级裁定的完整原理与实现链路。

#### 2.8.1 NER 模型架构与推理流程

Small-NER 基于 **CMeEE（Chinese Medical Entity Recognition）** 中文医学命名实体识别数据集
微调的 BERT 模型，采用 **BIOES / BIO 序列标注方案**，支持识别 9 类医疗实体。

```
NER 推理流水线 / NER Inference Pipeline
═══════════════════════════════════════

  输入文本 (如: "患者确诊2型糖尿病10年，口服二甲双胍治疗")
    │
    ├─① 文本预处理
    │   ├─ 短文本 (≤120字): 直接编码推理
    │   └─ 长文本 (>120字): 智能分句 (句号/分号/换行)
    │       + 滑动窗口 (120字窗口 + 20字重叠，防止截断漏检)
    │
    ├─② Tokenizer 编码 (SimpleChineseBertTokenizer)
    │   ├─ 中文: 逐字切分（每个汉字为一个 token）
    │   ├─ 英文: 逐字母切分 + 大小写折叠 (HIV→hiv)
    │   └─ 输出: [CLS] 单字token₁...tokenₙ [SEP] [PAD]...
    │       → (input_ids, attention_mask, token_type_ids)
    │
    ├─③ 模型前向推理
    │   ├─ ONNX Runtime (推荐，轻量高效，CPU/CUDA)
    │   ├─ TensorRT (NVIDIA GPU FP16 极致加速)
    │   ├─ ModelScope (PyTorch + 达摩院 RaNER 管道)
    │   └─ MLX (Apple Silicon Metal GPU)
    │   → logits (seq_len, num_labels)
    │
    ├─④ 序列标注解码
    │   ├─ BIOES 模式 (37类): B-xxx/I-xxx/E-xxx/S-xxx → 实体
    │   └─ BIO 模式 (13类):  B-xxx/I-xxx → 实体
    │   置信度: softmax → 每个 token 取 argmax 标签概率
    │          多 token 实体取 min (木桶原则)
    │
    ├─⑤ 标签归一化映射
    │   dis→MEDICAL_DISEASE, sym→MEDICAL_DISEASE,
    │   dru→MEDICATION, pro→SURGERY, bod→BODY_PART,
    │   ite→EXAMINATION, dep→DEPARTMENT, equ→EQUIPMENT,
    │   mic→MEDICAL_DISEASE, GENE→GENOMIC_HINT
    │
    └─⑥ 输出: [{"label": "MEDICAL_DISEASE", "text": "糖尿病", "confidence": 0.92},
                {"label": "MEDICATION", "text": "二甲双胍", "confidence": 0.88}]
```

**支持的实体类型（CMeEE 标准 9 类）**：

| 原始标签 | 标准标签 | 含义 | 示例 |
|---|---|---|---|
| `dis` | `MEDICAL_DISEASE` | 疾病 | 糖尿病、高血压、艾滋病 |
| `sym` | `MEDICAL_DISEASE` | 症状（归入疾病大类） | 头痛、咳嗽、胸闷 |
| `mic` | `MEDICAL_DISEASE` | 微生物（归入疾病大类） | 幽门螺杆菌、HIV病毒 |
| `dru` | `MEDICATION` | 药物 | 二甲双胍、阿司匹林 |
| `pro` | `SURGERY` | 手术/操作 | 冠脉搭桥、穿刺活检 |
| `bod` | `BODY_PART` | 身体部位 | 心脏、肝脏、膝关节 |
| `ite` | `EXAMINATION` | 检查项目 | CT扫描、血常规 |
| `dep` | `DEPARTMENT` | 科室 | 心内科、骨科 |
| `equ` | `EQUIPMENT` | 医疗设备 | 呼吸机、除颤仪 |
| `GEN` | `GENOMIC_HINT` | 基因提示（ModelScope 特有） | BRCA1、EGFR突变 |

#### 2.8.2 从 NER 实体到 SecurityTag 的等级映射

NER 提取的实体需经过 **实体→等级映射** 才能参与最终分类裁定。
映射逻辑实现在 `ClassificationFunnel._run_ner()` 中，采用三级决策策略：

```
NER 实体 → SecurityTag 等级映射决策树
══════════════════════════════════════════

  实体 {label, text, confidence}
    │
    ├─ 优先级1: taxonomy.ner_entity_mapping 配置化映射
    │   (taxonomy YAML 中显式定义 label→level，如 GENOMIC_HINT→L5)
    │   → 直接使用配置等级，needs_human_review = (level == 最高等级)
    │
    ├─ 优先级2: 内置硬编码规则（当 taxonomy 未配置时）
    │   │
    │   ├─ label == "GENOMIC_HINT"
    │   │   → level = 最高等级 (L5), needs_human_review = True
    │   │   → 基因信息属极敏感（遗传隐私）
    │   │
    │   ├─ label == "MEDICAL_DISEASE"
    │   │   │
    │   │   ├─ 文本含 L5 关键词 (hiv/aids/艾滋/精神分裂/基因/遗传缺陷)
    │   │   │   → level = 最高等级 (L5), category = HIGH_RISK_MEDICAL_L5
    │   │   │   → needs_human_review = True
    │   │   │
    │   │   ├─ 文本含敏感关键词 (肿瘤/癌症/白血病/梅毒/抑郁症...)
    │   │   │   → level = 次高等级 (L4), category = MEDICAL_SENSITIVE_DISEASE
    │   │   │   → 敏感病种关键词列表可通过 taxonomy.ner_sensitive_keywords 配置
    │   │   │
    │   │   └─ 普通疾病
    │   │       → level = 中间等级 (L3), category = MEDICAL_DISEASE
    │   │
    │   ├─ label ∈ {"MEDICATION", "SURGERY", "BODY_PART"}
    │   │   → level = 中间等级 (L3)
    │   │
    │   └─ 其他未知标签
    │       → level = 中间等级 (L3), category = 原始标签名
    │       → 防止自定义 NER 标签被静默丢弃
    │
    └─ 输出: SecurityTag(
              level=mapped_level,
              category=label,
              confidence=ner_confidence,
              source_engine="SMALL_NER",
              rule_id=f"NER_{label}",
              ...)
```

**动态等级推断**：最高等级（如 L5）、次高等级（如 L4）、中间等级（如 L3）
均从 taxonomy.levels 的 rank 排序中动态推断，而非硬编码——
这使得同一套映射逻辑可适配 L1~L5 医疗体系、C1~C4 金融体系、G1~G4 广东医疗体系等。

#### 2.8.3 NER 智能门禁机制

NER 推理有毫秒级延迟（ONNX ~5ms, ModelScope ~30ms），为避免对结构化短字段产生无效开销，
漏斗在 `_should_trigger_ner()` 中实现了多层门禁：

```
NER 智能门禁决策流程
════════════════════

  输入: (field_name, value)
    │
    ├─ 排除1: 空值/超短文本 → strip()后长度 < 2 → 拒绝
    ├─ 排除2: 纯数字/纯英文 → 无连续 ≥2 个中文汉字 → 拒绝
    ├─ 排除3: PII 结构化短字段
    │   {id_card_no, phone, name, patient_name, age, gender, sex,
    │    medical_insurance_no, social_security_no, disability_cert_no,
    │    registered_address, house_address, contact_phone, ...}
    │   → 这些字段由 L1 规则引擎完全覆盖，NER 无附加价值 → 拒绝
    │
    ├─ 强制触发: 临床非结构化文书字段
    │   {chief_complaint, present_illness, past_history,
    │    personal_history, family_history, allergic_history,
    │    progress_note, diagnosis_name, diagnosis}
    │   → 这些字段包含复杂医疗语义，NER 有最大增益 → 接受
    │
    └─ 默认: 通过中文检查的其余文本 → 接受
```

此外还有 **等级触发阈值**：即使门禁放行，NER 也仅在当前等级 rank ≤ `ner_trigger_max_rank`
时才实际执行——已经确定为高敏（如 L4/L5）的字段无需 NER 锦上添花。

#### 2.8.4 NER 置信度计算与融合

NER 层的置信度计算遵循以下规则：

1. **实体置信度**：NER 模型对每个 token 输出 softmax 概率分布，
   取 argmax 标签的概率作为该 token 的置信度。
   多 token 实体（如"二甲双胍"4个字）取所有 token 概率的**最小值**（木桶原则）：

$$\text{confidence}_{\text{entity}} = \min_{i=1}^{n} P(\text{label}_i | \text{token}_i)$$

2. **阶段融合（最大值覆盖策略）**：NER 标签追加到已有标签列表后，
   整体置信度取所有标签的最大值：

$$\text{confidence}_{\text{L1+L2}} = \max\left(\text{confidence}_{\text{L1}},\; \max_{t \in \text{tags}_{\text{NER}}}(t.\text{confidence})\right)$$

3. **参与等级裁定的门槛**：置信度低于 `min_tag_confidence`（默认 0.5）的 NER 标签
   仅作审计记录，**不参与** `resolve_level` 的等级计算——
   防止低置信度 NER 标签无条件拉高最终等级。

4. **engine_layer 归属判定**：`engine_layer` 仅在 NER **实际影响决策** 时更新为 `L2_SMALL_NER`：
   - L1 无标签时 NER 提供了首个分类结果 → 归属 L2
   - NER 等级高于 L1 结果 → 归属 L2
   - 否则保持 `L1_RULE`（NER 标签虽存在但未改变决策）

#### 2.8.5 多后端降级策略

NER 引擎采用 **四级降级链**，确保在不同硬件环境下均可提供服务：

```
降级优先级:
  0. MLXSmallNerEngine     → Apple Silicon Metal GPU (仅 macOS)
  1. TensorRTSmallNerEngine → NVIDIA GPU FP16 极致加速 (零 PyTorch 依赖)
  2. ONNXSmallNerEngine     → ONNX Runtime CPU/CUDA (推荐，轻量高效)
  3. ModelScopeSmallNerEngine → PyTorch + ModelScope 管道 (兼容性最好)
  4. 全部不可用             → NerAdapter 标记 _available=False, extract() 返回 []
```

所有后端共享同一套 `extract(text) → list[dict]` 接口，上层漏斗无需感知底层差异。
初始化使用 `threading.Lock` + double-check 保证线程安全，防止并发请求重复加载模型。

#### 2.8.6 配置化多标准适配

不同标准体系可通过 taxonomy YAML 自定义 NER 行为，无需修改代码：

```yaml
# rules/taxonomies/finance_jrt0197.yaml 示例
ner_entity_mapping:           # 实体类型→等级显式映射
  MEDICAL_DISEASE: "C3"       # 金融体系下疾病实体统一为 C3
  MEDICATION: "C2"            # 药物降为 C2
  GENOMIC_HINT: "C4"          # 基因信息升为 C4

ner_sensitive_keywords:       # 自定义敏感关键词
  - "内幕交易"
  - "账户密码"

ner_label_mapping:            # 覆盖内置 NER 原始标签映射
  dis: "FINANCE_DISEASE_PROXY"

ner_trigger_max_rank: 2       # 金融体系限制 NER 仅在 C1/C2 时触发
```

### 2.9 NER 分类分级与脱敏抹平的联动机制

NER 层不仅参与分类分级，其识别结果还直接驱动下游脱敏策略的选择与执行。
本节详述 NER → 分类 → 脱敏的完整联动链路。

#### 2.9.1 联动架构总览

```
NER 驱动的分类→脱敏联动链路
═══════════════════════════════

  原始文本 / 图像
    │
    ▼
  ┌──────────────────────────────────────────────────────┐
  │  Layer-2 NER 实体识别                                 │
  │  提取: 疾病/药物/手术/身体部位/基因/检查项目...       │
  │  输出: [{label, text, confidence}, ...]               │
  └──────────────────────────────────────────────────────┘
    │                          │
    ▼                          ▼
  ┌────────────────────┐  ┌──────────────────────────────┐
  │  分类分级路径        │  │  脱敏抹平路径                 │
  │  实体→SecurityTag   │  │  分类结果→脱敏策略选择         │
  │  →等级裁定          │  │  →字段级 masking / 图像打码    │
  │  →final_level      │  │  →sanitized_value             │
  └────────────────────┘  └──────────────────────────────┘
    │                          │
    ▼                          ▼
  FieldClassificationResult   sanitized_value (抹平产物)
  {final_level, confidence,    ├─ 文本: masking 模块字段级脱敏
   engine_layer, tags}         └─ 图像: image_redaction 打码
```

#### 2.9.2 NER 实体类型驱动的分类决策

NER 识别的实体类型通过 `_run_ner()` 映射为 `SecurityTag`，每个标签携带 `category` 属性，
该 category 直接决定下游脱敏模块的字段类型识别（`FieldType`）和脱敏操作选择：

| NER 实体类型 | SecurityTag.category | 敏感度等级 | 下游脱敏策略 |
|---|---|---|---|
| `GENOMIC_HINT` | `GENOMIC_HINT` | L5（极敏感） | 完全遮蔽 / HMAC 哈希 |
| `MEDICAL_DISEASE` (高敏病种) | `HIGH_RISK_MEDICAL_L5` | L5 | 完全遮蔽 + 人工复核 |
| `MEDICAL_DISEASE` (敏感病种) | `MEDICAL_SENSITIVE_DISEASE` | L4 | 泛化替换 / 上位词替换 |
| `MEDICAL_DISEASE` (普通) | `MEDICAL_DISEASE` | L3 | 掩码 / K-匿名泛化 |
| `MEDICATION` | `MEDICATION` | L3 | 药物类别泛化（如"二甲双胍"→"降糖药"） |
| `SURGERY` | `SURGERY` | L3 | 手术类型泛化 |
| `BODY_PART` | `BODY_PART` | L3 | 身体系统上位泛化 |
| `EXAMINATION` | `EXAMINATION` | L3 | 检查类别泛化 |

> **注意**：NER 产出的 `SecurityTag.category` 与 masking 模块的 `FieldType` 枚举
> 是两个独立的分类体系——前者用于**敏感度分级**，后者用于**字段名模式匹配脱敏**。
> 两者的联动发生在 `service.py` 编排层：分类结果决定该字段"需要多强的脱敏"，
> masking 模块根据字段名/分类 category 决定"用什么方式脱敏"。

#### 2.9.3 文本脱敏抹平 (Sanitization) 联动

当调用方传入 `sanitize=True` 时，漏斗根据分类结果执行不同粒度的文本抹平：

**1. 字段级结构化脱敏（masking 模块）**

masking 模块（`privacy/masking.py`）根据字段名自动识别类型并施加对应脱敏：

```python
# masking 模块字段类型识别与脱敏策略
FieldType.MOBILE   → 手机号掩码: 138****1234
FieldType.ID_CARD  → 身份证掩码: 330801********0789
FieldType.NAME     → 姓名掩码: 张*三
FieldType.BANK_CARD → 银行卡掩码: 6222****1234
FieldType.EMAIL    → 邮箱掩码: z***@example.com
FieldType.ADDRESS  → 地址截断: 浙江省***
FieldType.DEFAULT  → 通用掩码: 保留首尾各1字符
```

**2. LLM 融合脱敏（Layer-3 联合推断）**

当 `sanitize=True` 传入 LLM 适配器时，LLM 引擎（`Qwen3.5-0.8B-Privacy-Classifier-Smoother`）
在分类的同时输出 `sanitized_text`——将分类与脱敏合并在一次推理中完成：

- 身份证号 → 星号化: `330801********0789`
- 年龄 → K-匿名区间重写: `45岁` → `[40,50]`
- 敏感疾病名 → 上位泛化: `艾滋病` → `慢性传染病`
- 具体药物名 → 类别泛化: `二甲双胍` → `口服降糖药`

LLM 融合脱敏的优势在于能理解上下文语义：
- "他拿了那个免疫靶向药" → 识别为"靶向治疗药物"并泛化
- "既往有HIV阳性病史" → 同时处理疾病名和感染状态

**3. 脱敏结果传递**

```
FunnelResult.sanitized_value
  │
  ├─ 文本输入 + sanitize=True
  │   ├─ 图像字段 → image_redaction 打码产物 (路径/Base64)
  │   └─ 非图像字段 → 空字符串 (文本脱敏由 masking 模块在外部处理)
  │
  └─ FieldClassificationResult.sanitized_value
      → 由 REST/gRPC 响应返回给调用方
      → 调用方据此替换原始数据
```

#### 2.9.4 图像脱敏抹平 (Image Redaction) 联动

当分类结果判定输入包含图像/影像（场景 C）且 `sanitize=True` 时，
漏斗调用 `image_redaction.sanitize_image_input()` 生成打码产物：

```
图像打码执行流程
════════════════

  输入: 图像文件路径 / Base64 Data URI
    │
    ├─① 输入类型识别
    │   ├─ 文件路径 (.jpg/.png/.dcm/.dicom...) → 沙箱校验
    │   ├─ Base64 Data URI (data:image/...) → 解码
    │   └─ 不匹配 → 返回原文或失败占位符
    │
    ├─② 安全防护 (fail-closed)
    │   ├─ 路径穿越防护: resolve() + 白名单目录前缀匹配
    │   ├─ Symlink 逃逸拦截: resolve 后重新校验前缀
    │   ├─ DecompressionBomb: MAX_IMAGE_PIXELS = 25M
    │   └─ OOM 防护: 超 2048×2048 自动下采样 (LANCZOS)
    │
    ├─③ 敏感区域遮挡
    │   ├─ 默认遮挡: 头部 16% + 底部 18%
    │   │   (覆盖姓名/诊断文字/签名区域)
    │   └─ 自定义 boxes: [(ymin, xmin, ymax, xmax)] 比例/像素坐标
    │
    ├─④ 输出与清理
    │   ├─ 文件路径: sha256(文件名)[:12] 匿名命名 + 原子替换
    │   ├─ Base64: 统一输出 PNG 格式
    │   └─ 磁盘防满: 自动清理超过 200 个旧文件
    │
    └─⑤ DICOM 等无法安全派生的格式 → 返回 [IMAGE-REDACTION-FAILED]
```

**安全约束**：
- 图像打码仅在 `sanitize=True` 时执行——纯分类请求不产生文件读写副作用
- 路径白名单通过 `PRIVACY_IMAGE_ALLOWED_DIRS` 环境变量配置
- DICOM 格式因无法安全派生为普通图像，直接返回失败占位符（fail-closed）

#### 2.9.5 NER 分类结果与脱敏策略的编排关系

`service.py` / `funnel.py` 作为编排层，将 NER 分类结果与脱敏策略桥接：

```
编排层决策逻辑 (service.py 视角)
══════════════════════════════════

  classify_field(field_name, value, sanitize=True)
    │
    ├─ Step 1: 漏斗分类 → FunnelResult
    │   ├─ final_level = "L4" (NER 识别到敏感疾病)
    │   ├─ tags = [{category: "MEDICAL_SENSITIVE_DISEASE", ...}]
    │   └─ engine_layer = "L2_SMALL_NER"
    │
    ├─ Step 2: 根据 final_level 选择脱敏强度
    │   ├─ L5 (极敏感): 完全遮蔽 + HMAC 哈希 + 人工复核
    │   ├─ L4 (高敏感): 泛化替换 / LLM 融合脱敏
    │   ├─ L3 (中敏感): 掩码 / K-匿名泛化
    │   ├─ L2 (低敏感): 简单掩码
    │   └─ L1 (公开):   不脱敏
    │
    ├─ Step 3: 执行脱敏
    │   ├─ 文本字段 → masking.mask_value(field_name, value)
    │   ├─ 图像字段 → image_redaction.sanitize_image_input(value)
    │   └─ LLM sanitize=True → LLM 同时输出分类+脱敏文本
    │
    └─ Step 4: 返回 FieldClassificationResult
        ├─ final_level, confidence, tags, engine_layer
        └─ sanitized_value (脱敏后的值)
```

#### 2.9.6 NER 脱敏联动的降级与容错

NER → 分类 → 脱敏链路的每个环节均有独立的降级策略，确保整体流程不崩溃：

| 故障场景 | 降级行为 | 脱敏影响 |
|---|---|---|
| NER 后端全部不可用 | `extract()` 返回 `[]`，跳过 Layer-2 | 分类回退到 L1 规则 + L3 LLM；脱敏由 masking 模块兜底 |
| NER 门禁拦截（PII短字段） | 跳过 Layer-2 | 这些字段由 L1 规则完全覆盖，脱敏不受影响 |
| NER 置信度低于 `min_tag_confidence` | 标签仅作审计记录，不参与等级计算 | 不影响脱敏策略选择 |
| LLM 融合脱敏失败 | `sanitized_text` 为空 | 回退到 masking 模块的字段级脱敏 |
| 图像打码失败 | `sanitized_value` 留空，记录 warning | 分类结果不受影响，仅脱敏产物缺失 |
| 图像路径沙箱校验失败 | 返回 `[IMAGE-REDACTION-FAILED]` | 拒绝处理，防止任意文件读取 |

---

## 3. 新增文件

| 文件 | 职责 |
|---|---|
| `dynclassification/funnel.py` | 三层漏斗编排器（核心） |
| `dynclassification/ner_adapter.py` | NER 引擎适配器（lazy-load 本地 NER 引擎） |
| `dynclassification/llm_adapter.py` | LLM 分类器适配器（lazy-load 本地 LLM 引擎） |

相关配套文件：`ner_engines.py`（NER 引擎实现）、`llm_engines.py`（LLM 引擎实现）、
`mlx_ner_engine.py` / `mlx_llm_engine.py`（MLX 后端）、`image_redaction.py`（图像打码）。

## 4. 修改文件

| 文件 | 变更 |
|---|---|
| `dynclassification/models.py` | 新增 `ConfidencePolicy`、`EngineLayer`；`FieldClassificationResult` 增加 `engine_layer`/`reasoning` |
| `dynclassification/service.py` | `classify_field` 改为调用 funnel；置信度计算逻辑 |
| `dynclassification/__init__.py` | 导出新符号 |
| `rules/taxonomies/*.yaml` | 增加 `confidence_policy` 配置节（`sc_health_db51.yaml` 待补齐，见 2.3） |

---

## 5. 接口设计

### 5.1 ClassificationFunnel

```python
class ClassificationFunnel:
    """三层漏斗编排器。"""

    def __init__(self, engine, taxonomy, confidence_policy=None, ner_adapter=None, llm_adapter=None):
        ...

    def classify_field(self, field_name, value, sanitize: bool = False) -> Tuple[FunnelResult, list[SecurityTag]]:
        """执行三层漏斗分类，返回 (FunnelResult, suppressed_tags)。

        sanitize: 是否计算图像打码等脱敏产物（默认 False）。
            仅当调用方显式请求脱敏时才执行图像打码，
            避免纯分类请求产生不必要的文件读写副作用。
        """
        ...
```

### 5.2 FunnelResult

```python
@dataclass
class FunnelResult:
    tags: list[SecurityTag]
    final_level: str
    confidence: float
    engine_layer: str          # "L1_RULE" | "L2_SMALL_NER" | "L3_LLM"
    needs_human_review: bool
    reasoning: str
    sanitized_value: str       # 智能抹平/图像打码产物（仅 sanitize=True 时填充）
    has_conflict: bool
```

### 5.3 NER/LLM 适配器接口

```python
class NerAdapter:
    """NER 引擎适配器（lazy-load）。"""
    def extract(self, text: str) -> list[dict[str, Any]]: ...

class LlmAdapter:
    """LLM 分类器适配器（lazy-load）。"""
    def classify(self, text: str, upstream_level: str, upstream_confidence: float,
                 sanitize: bool = False) -> dict | None: ...
    def arbitrate(self, field_name: str, value: str, conflict_tags: list[SecurityTag],
                  taxonomy: DomainTaxonomy) -> dict | None: ...
```

---

## 6. 降级策略

```
NER 不可用（后端全部加载失败）→ extract() 返回 []，跳过 Layer-2，直接进入 Layer-3 判断
  （后端按 MLX → TensorRT → ONNX → ModelScope 顺序尝试，任一可用即生效）
NER 智能门禁拦截（PII 结构化短字段/纯数字文本）→ 跳过 Layer-2
LLM 并发过载 → 进程级信号量（PRIVACY_LLM_MAX_CONCURRENCY，默认 1）排队，
  等待超过 PRIVACY_LLM_SEMAPHORE_WAIT_SECONDS（默认 30s）→ 返回 None → 置信度衰减
LLM 内存不足 → 可用内存低于 PRIVACY_LLM_MIN_FREE_MEM_MB（默认 512MB）跳过推理 → 返回 None
LLM 不可用（torch 未安装/模型不存在）→ 使用 Phase 1 置信度衰减输出
LLM 超时（默认 180s，env: PRIVACY_VLM_TIMEOUT）→ 返回 None → 使用 Phase 1 置信度衰减输出
LLM JSON 解析失败 → 返回 None → 使用 Phase 1 置信度衰减输出
LLM confidence 非数值 → _safe_llm_confidence 回退上游置信度，流程不崩溃
LLM 裁定等级非法/超出冲突集合/低于上游等级 → 拒绝裁定，保留规则引擎结果 + 人工复核
LLM 未返回合法 final_level → 不刷新置信度/不归属 L3，保留上游结果 + 人工复核
图像打码失败 → 记录 warning 日志，sanitized_value 留空，不影响分类结果
```

LLM 调用侧另有两处防护（`llm_adapter.py`）：仲裁请求会先经 `sanitize_for_prompt`
清洗再进 prompt，降低注入面；所有 LLM 结果 dict 在进入漏斗后仍需通过 2.6 的
安全地板校验，适配器返回值本身不被信任。

---

## 7. 默认等级配置（已有能力）

每个 taxonomy YAML 的 `default_level` 字段独立配置：

| Taxonomy | 标准体系 | `default_level` |
|---|---|---|
| `default.yaml` | L1~L5 | `L3` |
| `sc_health_db51.yaml`（四川医疗 DB51） | L1~L5 | `L3` |
| `gd_health.yaml`（广东医疗） | G1~G4 | `G2` |
| `finance_jrt0197.yaml`（金融 JR/T 0197） | C1~C4 | `C3` |
| 未来教育/政务 | — | 可设为 `L2` 或其他 |

无需额外修改。

---

## 8. 已知局限与后续优化方向

1. **层间等级冲突静默（仲裁能力不对称）**：当前冲突检测仅覆盖"普通规则 vs 降级规则"。
   若规则判 L4（0.95）而 NER 判 L2（0.90），置信度取最大值 0.95 且不触发冲突检测，
   层间等级矛盾被静默忽略——LLM 仲裁能力只对规则**内部**冲突开放，跨层不一致没有
   裁决通道。更值得注意的是反向情形：NER 标签置信度 ≥ `min_tag_confidence` 即参与
   `resolve_level`，NER 高等级会**绕过仲裁无条件抬高最终等级**（升敏方向不受 2.6
   安全地板约束，因为安全地板只校验 LLM 输出）。后续可引入层间分歧检测
   （如 |rank差| ≥ 2 时标记 `needs_human_review`）与加权融合。
2. **长尾字段 LLM 成本**：规则未命中时 `confidence = 0.0 < 0.75`，所有长尾字段都会触发
   LLM（秒级延迟）。进程级信号量（默认并发 1）+ 30s 排队超时提供了过载保护，
   但高吞吐场景 p99 延迟仍会被 LLM 拖长；建议通过 `enable_llm=false`、调低
   `llm_confidence_threshold` 或引入请求级限流/采样控制 LLM 调用比例。
3. **置信度"最大值覆盖"策略**：多引擎置信度取 max 而非加权融合，
   无法表达引擎间意见分歧的程度；且 `engine_layer` 标记的决策来源与 confidence
   的实际来源可能不是同一层，下游审计时无法回溯置信度由谁贡献。
   可作为后续概率化融合的改进方向。
4. **医疗逻辑耦合进通用漏斗**：L5/L4 补全扫描的模式表硬编码来自
   `medical_pipeline/rules.py`，通用 `ClassificationFunnel` 对所有 domain
   （包括金融 C 体系）都会执行医疗模式扫描，领域分层不干净；且 L5 命中标签固定
   `needs_human_review=True`，高频命中场景下人工复核工单量可能失控。
   后续宜将补全扫描改为按 domain/taxonomy 可配置的插件式规则源。
5. **静默放行与"确定为中敏"不可区分**：无有效标签时回退 `default_level`（如 L3），
   下游若只看 `final_level` 无法区分"什么都没检测到"与"确定是中等等级"；
   需要结合 `confidence = 0.0` 与空 `tags` 判断，接入方应在文档中明确该约定。
