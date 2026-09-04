# 基于 Layer-1 规则漏斗的训练数据自动生成与蒸馏方案

> 本方案旨在利用 `PrivShield` 现有的 Layer-1 规则引擎（`ConfigurableRuleEngine`，含正则表达、敏感词库、掩码策略与行业合规模板），构建一套零人工成本、自动化的高质量合成数据生成管道（Data Generation Pipeline），用于微调 Qwen3.5-0.8B 轻量大模型。

---

## 1. 数据生成整体架构

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                       Layer-1 Data Engine Pipeline                          │
└─────────────────────────────────────────────────────────────────────────────┘
                                      │
   ┌──────────────────────────────────┼──────────────────────────────────┐
   ▼                                  ▼                                  ▼
【种子文本库 Seed Corpus】      【Layer-1 规则引擎】              【Faker 伪造生成器】
包含真实无敏语料、开源文本      ConfigurableRuleEngine           生成高逼真度 PII 样本
(rules/ 目录下 YAML 驱动)       (正则/算子/校验/ICD-10)          (身份/金融/医疗/通讯)
   │                                  │                                  │
   └──────────────────┬───────────────┴──────────────────────────────────┘
                      ▼
   ┌─────────────────────────────────────────────────────────────────────┐
   │ 1. 实体注入与文本合成 (Entity Injection & Context Synthesis)        │
   │    - 将 Faker 产出的身份证、手机号、诊断结论等注入种子文本模板       │
   │    - 支持单实体/多实体/嵌套上下文三种注入模式                        │
   └─────────────────────────────────────────────────────────────────────┘
                      │
                      ▼
   ┌─────────────────────────────────────────────────────────────────────┐
   │ 2. Layer-1 自动标注 (Automatic Labeling via Layer-1 Rules)          │
   │    - 调用 ConfigurableRuleEngine.evaluate() 获取 SecurityTag 列表   │
   │    - 记录注入位置、实体类型、数据密级 (L1~L5)、安全标签               │
   └─────────────────────────────────────────────────────────────────────┘
                      │
                      ▼
   ┌─────────────────────────────────────────────────────────────────────┐
   │ 3. 无痕抹平脱敏生成 (Seamless Context Desensitization)              │
   │    - 两级策略：掩码替换 (Mask) + 上下文语义重写 (Context Rewrite)    │
   │    - 使用掩码策略表 + 重写模板库生成自然连贯的抹平文本               │
   └─────────────────────────────────────────────────────────────────────┘
                      │
                      ▼
   ┌─────────────────────────────────────────────────────────────────────┐
   │ 4. 质量校验与滤除 (QA & Zero-Leakage Validation Filter)             │
   │    - JSON 校验 + 再次运行 Layer-1 规则扫描抹平文本确认无敏感遗漏      │
   │    - 语法连贯度评分过滤（基于字符 n-gram 流畅度检测）                │
   └─────────────────────────────────────────────────────────────────────┘
                      │
                      ▼
             导出 SFT 训练集 (`dataset_sft.jsonl`)
```

---

## 2. 核心任务定义 (Task Alignment)

微调的目标是让 Qwen3.5-0.8B 模型同时具备**分类分级**与**无痕抹平脱敏**两大能力。─────┘
                      │
                      ▼
   ┌─────────────────────────────────────────────────────────────────────┐
   │ 2. Layer-1 自动标注 (Automatic Labeling via Layer-1 Rules)          │
   │    - 调用 ConfigurableRuleEngine.evaluate() 获取 SecurityTag 列表   │
   │    - 记录注入位置、实体类型、数据密级 (L1~L5)、安全标签               │
   └─────────────────────────────────────────────────────────────────────┘
                      │
                      ▼
   ┌─────────────────────────────────────────────────────────────────────┐
   │ 3. 无痕抹平脱敏生成 (Seamless Context Desensitization)              │
   │    - 两级策略：掩码替换 (Mask) + 上下文语义重写 (Context Rewrite)    │
   │    - 使用掩码策略表 + 重写模板库生成自然连贯的抹平文本               │
   └─────────────────────────────────────────────────────────────────────┘
                      │
                      ▼
   ┌─────────────────────────────────────────────────────────────────────┐
   │ 4. 质量校验与滤除 (QA & Zero-Leakage Validation Filter)             │
   │    - JSON 校验 + 再次运行 Layer-1 规则扫描抹平文本确认无敏感遗漏      │
   │    - 语法连贯度评分过滤（基于字符 n-gram 流畅度检测）                │
   └─────────────────────────────────────────────────────────────────────┘
                      │
                      ▼
            导出 SFT 训练集 (`dataset_sft.jsonl`)
```

---

## 2. 核心任务定义 (Task Alignment)

微调的目标是让 Qwen3.5-0.8B 模型同时具备**分类分级**与**无痕抹平脱敏**两大能力。

### 任务 1：分类分级 (Classification & Tagging)
- **输入**：包含敏感信息的原始语句或字段值。
- **输出**：数据密级（L1 一般数据, L2 重要数据, L3 核心数据, L4 高敏感数据, L5 极敏感数据）、敏感实体列表及其分类标签（如 `ID_CARD`, `MOBILE_PHONE`, `DIAGNOSIS`）。

### 任务 2：无痕抹平脱敏 (Desensitization & Context Smoothing)
- **输入**：原始文本及可选的脱敏策略（如 `SMOOTH_ERASE` / `MASK`）。
- **输出**：脱敏并完成上下文无痕重写后的流畅文本。不仅将敏感项替换或打码，还针对语法连贯性进行微调，做到无感抹平。

---

## 3. 合成数据模板与 SFT 样例

微调数据格式采用标准的 ChatML 格式（支持系统 System Prompt、User 指令与 Assistant 回复）：

### 3.1 System Prompt（固定）

```text
你是一个专业的隐私安全Sidecar助手。请分析输入的文本，识别敏感信息，输出分类分级结果（JSON格式），并提供语义连贯的无痕抹平脱敏重写文本。

输出格式要求（严格 JSON）：
{
  "classification": {
    "max_level": "L1~L5",
    "entities": [{"text": "...", "category": "...", "level": "L1~L5", "confidence": 0.0~1.0}]
  },
  "smoothed_text": "抹平后的自然连贯文本"
}
```

### 3.2 样例 1：综合分类分级 + 脱敏无痕抹平（单句）

**User Input**:
```text
请评估该患者的隐私风险：张伟，身份证号 110101199003072345，患有重度抑郁症，联系电话 13800138000。
```

**Assistant Output**:
```json
{
  "classification": {
    "max_level": "L4",
    "entities": [
      {"text": "张伟", "category": "NAME", "level": "L2", "confidence": 1.0},
      {"text": "110101199003072345", "category": "ID_CARD", "level": "L4", "confidence": 1.0},
      {"text": "重度抑郁症", "category": "MEDICAL_DIAGNOSIS", "level": "L4", "confidence": 0.95},
      {"text": "13800138000", "category": "PHONE", "level": "L2", "confidence": 1.0}
    ]
  },
  "smoothed_text": "请评估该患者的隐私风险：该患者近期前往医院就诊，相关诊断及联络方式已做隐私合规处理。"
}
```

### 3.3 样例 2：金融交易文本

**User Input**:
```text
客户李明（身份证：310101198512151234）申请提现50000元到卡号6222021234567890123，绑定手机13912345678。
```

**Assistant Output**:
```json
{
  "classification": {
    "max_level": "L3",
    "entities": [
      {"text": "李明", "category": "NAME", "level": "L2", "confidence": 1.0},
      {"text": "310101198512151234", "category": "ID_CARD", "level": "L4", "confidence": 1.0},
      {"text": "6222021234567890123", "category": "BANK_CARD", "level": "L3", "confidence": 1.0},
      {"text": "13912345678", "category": "PHONE", "level": "L2", "confidence": 1.0}
    ]
  },
  "smoothed_text": "某客户申请大额提现业务，相关身份及账户信息已做隐私合规处理。"
}
```

### 3.4 样例 3：无敏感信息负样本（防止过度脱敏）

**User Input**:
```text
今日天气晴朗，适合户外活动。建议市民注意防晒，多补充水分。
```

**Assistant Output**:
```json
{
  "classification": {
    "max_level": "L1",
    "entities": []
  },
  "smoothed_text": "今日天气晴朗，适合户外活动。建议市民注意防晒，多补充水分。"
}
```

---

## 4. 数据管道实现机制 (Implementation Details)

### 4.1 种子模板库（20+ 模板，覆盖 5 大领域）

编写数据生成脚本 `scripts/data/generate_llm_lora_dataset.py`：

```python
import json
import random
from faker import Faker
from engine.dynclassification.engine import ConfigurableRuleEngine

fake = Faker("zh_CN")

# =========================================================================
# 种子模板库：覆盖金融、医疗、企业办公、通讯、负样本 5 大领域
# =========================================================================
TEMPLATES = {
    # --- 金融领域 (40%) ---
    "finance": [
        "客户{name}（身份证：{id_card}）申请提现{amount}元到卡号{bank_card}。",
        "用户{name}的贷款申请已审批通过，绑定还款账户{bank_card}，联系电话{phone}。",
        "交易流水：卡号{bank_card}于{date}消费{amount}元，商户：{merchant}。",
        "投资者{name}（证件号{id_card}）认购基金{fund_amount}份，收益{profit}元。",
        "客户{name}的信用评估报告：收入{salary}元/月，负债{debt}元，评分{score}。",
    ],
    # --- 医疗领域 (30%) ---
    "medical": [
        "患者{name}，性别{gender}，{age}岁，诊断为{disease}，开具处方{medication}。",
        "住院病历：患者{name}（身份证{id_card}），主诉{symptom}，检查项目{exam}。",
        "检验报告：患者{name}的{exam_item}结果为{result}，参考范围{reference}。",
        "门诊记录：{name}因{symptom}就诊，既往史{history}，过敏史{allergy}。",
    ],
    # --- 企业办公领域 (20%) ---
    "enterprise": [
        "员工{name}的绩效评估已生成，邮箱：{email}，薪资：{salary}元/月。",
        "人事部通知：{name}（工号{employee_id}）将于{date}入职，合同期限{contract_years}年。",
        "公司内部邮件：{name}（{email}）提交的报销申请{amount}元已审批通过。",
    ],
    # --- 通讯/网络领域 (5%) ---
    "telecom": [
        "系统检测到异常登录，用户{name}，绑定手机：{phone}，IP地址：{ip}。",
        "用户{name}（账号{account}）修改了登录密码，设备指纹：{device_id}。",
    ],
    # --- 负样本：无敏感信息 (5%) ---
    "negative": [
        "今日天气晴朗，适合户外活动。建议市民注意防晒，多补充水分。",
        "根据最新研究报告，全球芯片市场规模预计将在2025年达到5000亿美元。",
        "本次季度会议讨论了产品路线图、技术架构升级以及团队建设三个议题。",
    ],
}

# 领域辅助数据
DISEASES = ["急性支气管炎", "II型糖尿病", "高血压病3级", "冠状动脉粥样硬化", "重度抑郁症", "甲状腺功能减退"]
MEDICATIONS = ["阿莫西林克拉维酸钾", "二甲双胍片", "硝苯地平控释片", "舍曲林片"]
SYMPTOMS = ["持续性头痛", "胸闷气短", "反复发热", "关节肿痛"]
EXAMS = ["胸部CT", "血常规", "肝功能", "心电图"]
MERCHANTS = ["京东商城", "美团外卖", "滴滴出行", "支付宝转账"]

def generate_sample(domain: str | None = None):
    """生成单条合成样本。domain=None 时按领域比例随机选取。"""
    if domain is None:
        domain = random.choices(
            list(TEMPLATES.keys()),
            weights=[40, 30, 20, 5, 5],
            k=1
        )[0]
    template = random.choice(TEMPLATES[domain])
    
    # 生成 Faker 伪造数据
    ctx = {
        "name": fake.name(),
        "id_card": fake.ssn(),
        "phone": fake.phone_number(),
        "bank_card": fake.credit_card_number(),
        "amount": str(random.randint(1000, 50000)),
        "ip": fake.ipv4(),
        "email": fake.email(),
        "salary": str(random.randint(8000, 40000)),
        "gender": random.choice(["男", "女"]),
        "age": str(random.randint(18, 80)),
        "disease": random.choice(DISEASES),
        "medication": random.choice(MEDICATIONS),
        "symptom": random.choice(SYMPTOMS),
        "exam": random.choice(EXAMS),
        "date": fake.date(),
        "merchant": random.choice(MERCHANTS),
        "fund_amount": str(random.randint(1000, 100000)),
        "profit": str(round(random.uniform(-5000, 20000), 2)),
        "debt": str(random.randint(0, 200000)),
        "score": str(random.randint(300, 850)),
        "employee_id": f"EMP{random.randint(10000, 99999)}",
        "contract_years": str(random.choice([1, 3, 5])),
        "account": f"ACC{random.randint(100000, 999999)}",
        "device_id": fake.mac_address(),
        "exam_item": random.choice(["血糖", "血压", "胆固醇", "血红蛋白"]),
        "result": random.choice(["偏高", "正常", "偏低", "异常"]),
        "reference": random.choice(["3.9-6.1mmol/L", "90-140mmHg", "<5.2mmol/L"]),
        "history": random.choice(["无特殊", "高血压5年", "糖尿病史3年"]),
        "allergy": random.choice(["无", "青霉素过敏", "磺胺类药物过敏"]),
    }
    
    raw_text = template.format(**ctx)
    return raw_text, domain
```

### 4.2 基于 Layer-1 规则计算 Ground Truth

使用 `PrivShield` 内部的 `ConfigurableRuleEngine` 对生成的 `raw_text` 进行规则扫描，获取准确无误的实体位置、类型与数据密级：

```python
def process_ground_truth(engine: ConfigurableRuleEngine, raw_text: str) -> dict:
    """调用 Layer-1 规则引擎计算分类分级 Ground Truth。"""
    tags, _ = engine.evaluate("content", raw_text)
    
    entities = []
    max_level = "L1"
    level_order = {"L1": 1, "L2": 2, "L3": 3, "L4": 4, "L5": 5}
    
    for tag in tags:
        entities.append({
            "text": tag.matched_value,
            "category": tag.tag_id,
            "level": tag.level,
            "confidence": tag.confidence,
        })
        if level_order.get(tag.level, 1) > level_order.get(max_level, 1):
            max_level = tag.level

    return {"max_level": max_level, "entities": entities}
```

### 4.3 无痕抹平生成（两级策略）

无痕抹平不仅做简单替换，还需保证上下文语义连贯。采用**两级策略**：

```python
# 掩码策略表：定义每种实体类型的默认替换词
MASK_REPLACEMENTS = {
    "ID_CARD": "[身份证号]",
    "PHONE": "[联系电话]",
    "BANK_CARD": "[银行卡号]",
    "NAME": "[姓名]",
    "EMAIL": "[电子邮箱]",
    "IP_ADDRESS": "[IP地址]",
    "MEDICAL_DIAGNOSIS": "[相关疾病]",
    "MEDICAL_PRESCRIPTION": "[处方信息]",
    "SALARY": "[薪资信息]",
}

# 上下文重写模板：针对特定领域定义流畅的整句重写规则
CONTEXT_REWRITE_TEMPLATES = {
    "finance": [
        "某客户办理了金融业务，相关身份及账户信息已做隐私合规处理。",
        "该笔交易涉及的客户信息已完成脱敏处理。",
    ],
    "medical": [
        "该患者近期前往医院就诊，相关诊断及联络方式已做隐私合规处理。",
        "患者病历信息已做匿名化处理，具体诊疗细节已抹平。",
    ],
    "enterprise": [
        "该员工的人事信息已做脱敏处理，具体内容不再展示。",
        "相关企业内部通讯信息已完成隐私合规抹平。",
    ],
    "telecom": [
        "该网络活动日志已做匿名化处理，用户标识信息已抹平。",
    ],
    "negative": None,  # 无敏感信息，原文保留
}

def generate_smoothed_text(raw_text: str, tags: list, domain: str) -> str:
    """生成无痕抹平文本。
    
    策略：
    1. 若无敏感标签 → 原文保留
    2. 若有领域重写模板 → 使用模板整句重写（语义最连贯）
    3. 否则 → 逐实体替换为掩码词（兜底策略）
    """
    if not tags:
        return raw_text  # 无敏感信息
    
    # 优先使用领域重写模板
    rewrite_templates = CONTEXT_REWRITE_TEMPLATES.get(domain)
    if rewrite_templates:
        return random.choice(rewrite_templates)
    
    # 兜底：逐实体掩码替换
    smoothed = raw_text
    for tag in tags:
        replacement = MASK_REPLACEMENTS.get(tag.tag_id, "[已脱敏]")
        smoothed = smoothed.replace(tag.matched_value, replacement)
    
    return smoothed
```

---

## 5. 质量校验机制 (QA Filter & Zero-Leakage Check)

在导出的 SFT 训练集中，所有样本必须通过三重校验：

1. **JSON 合法性校验**：输出字符串解析为 Valid JSON 的成功率必须为 100%。
2. **规则零泄漏校验 (Zero-Leakage Re-Scan)**：再次将导出的 `smoothed_text` 输入 `ConfigurableRuleEngine`，确保不包含任何匹配的 `L2/L3/L4/L5` 敏感实体。若有遗漏，则丢弃该训练样本。
3. **文本连贯度过滤**：使用字符级 n-gram 语言模型（或简单的字符重复率/乱码检测）去除因替换导致语法畸变的文本。

```python
def validate_sample(sample: dict, engine: ConfigurableRuleEngine) -> bool:
    """三重校验：JSON合法性 + 零泄漏 + 连贯度。"""
    # 1. JSON 校验
    try:
        json.loads(sample["assistant_output"])
    except json.JSONDecodeError:
        return False
    
    # 2. 零泄漏校验
    smoothed = sample["smoothed_text"]
    leak_tags, _ = engine.evaluate("content", smoothed)
    high_risk_tags = [t for t in leak_tags if t.level in ("L2", "L3", "L4", "L5")]
    if high_risk_tags:
        return False  # 存在敏感遗漏，丢弃
    
    # 3. 连贯度粗筛（去除连续特殊字符超过阈值的情况）
    import re
    if re.search(r"[^\u4e00-\u9fa5a-zA-Z0-9]{5,}", smoothed):
        return False  # 连续特殊字符过多，语法可能畸变
    
    return True
```

---

## 6. 数据集规模与领域分布规划

- **训练集 (`train.jsonl`)**：50,000 条样本
  - 40% 结构化/半结构化金融与身份字段（~20,000 条）
  - 30% 医疗健康与诊断敏感长文本（~15,000 条）
  - 20% 常见企业办公与通讯文本（~10,000 条）
  - 5% 网络/通讯日志类文本（~2,500 条）
  - 5% 零敏感无高危信息普通文段（负样本，防止过度脱敏）（~2,500 条）
- **验证集 (`val.jsonl`)**：5,000 条样本（同分布）
- **测试 Benchmark (`test.jsonl`)**：2,000 条人工二次核验的测试集

### 数据增强策略

| 增强方式 | 说明 | 比例 |
|---|---|---|
| 同义替换 | 对模板中的非敏感词进行同义词替换 | 30% |
| 多实体混合 | 在单句中注入 2~5 个不同类型的敏感实体 | 25% |
| 长文本拼接 | 将 2~3 个短模板拼接为长段落 | 15% |
| 噪声注入 | 添加错别字、全角字符、多余空格等 | 10% |
| 原始纯净 | 不做增强的原始模板样本 | 20% |

---

## 7. 完整数据生成脚本入口

```bash
# 生成完整 SFT 数据集
cd /path/to/PrivShield
python scripts/data/generate_llm_lora_dataset.py \
    --train-size 50000 \
    --val-size 5000 \
    --test-size 2000 \
    --output-dir ./data/llm_lora/ \
    --model Qwen3.5-0.8B
```

生成的文件结构：

```text
data/llm_lora/
├── train.jsonl          # 50,000 条训练样本
├── val.jsonl            # 5,000 条验证样本
├── test.jsonl           # 2,000 条测试样本
├── stats.json           # 数据集统计信息（领域分布、实体类型分布等）
└── qa_report.json       # 质量校验报告（通过率、丢弃原因统计等）
```
