# Qwen3.5 CMeEE-Merged 基座 LoRA 专精微调与无痕抹平设计方案

> **核心说明**：
> 1. 本方案**不考虑图片 OCR**，专门面向纯文本场景下的敏感信息分类分级仲裁（L1~L5）与脱敏无痕抹平（Context Smoothing / Natural Context Rewriting）。
> 2. 底层基座采用 `llmlora/basemodels/cmeee_merged` 中已完成 CMeEE 医疗/通用 NER LoRA 合并的 **Qwen3.5 0.8B** CausalLM（`Qwen3_5ForCausalLM`，约 752M 参数）。
> 3. 所有新增/重构的代码、配置与脚本**完全收敛在 `llmlora` 目录中**，通过 `sys.path` bootstrap 复用主项目的 Layer-1 规则引擎做数据打标，不侵入主包。
> 4. 训练数据打标**以项目规则引擎裁定为准**（Ground Truth 来自 `ConfigurableRuleEngine`），而非硬编码标签。
> 5. 方案已完成端到端冒烟验证：数据生成 → LoRA 训练 → 合并导出 → Benchmark 评估全链路跑通。

---

## 1. 方案定位与架构图

在 `PrivShield` 的三层漏斗架构（Layer-1 规则 → Layer-2 Small-NER → Layer-3 LLM）中，Layer-3 负责处理复杂长文本语义理解、规则冲突仲裁、隐式敏感信息提取以及**脱敏后的文本无痕抹平**。

传统通用大模型（如 Qwen2-VL 2B/7B）显存占用高（4~14GB）、推理延迟长（300ms~2000ms）。本方案基于 **CMeEE-Merged 0.8B 基座** 进行二阶段 LoRA 专精 SFT，目标：

- **常驻显存/内存 < 2GB**（bf16 权重约 1.5GB）
- **JSON Schema 遵从率 99%+**
- **二次扫描零泄漏率 99%+**

```text
 PrivShield 纯文本三层漏斗集成
 ════════════════════════════════════════════════════════════════════════════

   [ 用户请求输入纯文本 ]
          │
          ▼
   Layer-1: ConfigurableRuleEngine
          │ ├── 高置信度匹配 → 直接打标签并输出
          │ └── 未匹配 / 低置信度 / 需无痕抹平 ──┐
          ▼                                     │
   Layer-2: ONNX Small-NER                     │
          │ ├── 识别常用实体 ───────────────────┤
          │ └── 需语义仲裁 / 上下文重写 ────────┼┐
          ▼                                     ││
   Layer-3: QwenPrivacyLoRAEngine               ││
   (加载 cmeee_merged 合并模型或 基座+LoRA)      ││
          │                                     ││
          ├─────────────────────────────────────┘│
          ▼                                      ▼
   [ 分类分级 JSON 结果 (L1~L5) ]      [ 自然连贯无痕抹平文本 ]
```

---

## 2. 运行环境（重要前置约束）

| 约束 | 说明 |
|---|---|
| **transformers >= 5.2** | 基座 `model_type=qwen3_5_text`（Qwen3.5 混合注意力架构），transformers 4.x 无法识别该架构（`KeyError: 'qwen3_5_text'`）。这是 Gemini 初版代码无法运行的根本原因。 |
| **独立虚拟环境 `llmlora/.venv`** | 为避免破坏主项目环境（pri 环境 transformers 4.57.6），训练环境用 `python -m venv --system-site-packages llmlora/.venv` 创建，继承系统 torch（2.13.x + CUDA），venv 内单独安装 `transformers==5.14.1`、`peft`、`accelerate`、`datasets`、`faker`。 |
| **脚本入口** | 一律使用 `llmlora/.venv/bin/python -m llmlora.scripts.<name>` 从仓库根目录运行；`train.py` 启动时会检测 transformers 主版本，低于 5 直接报错退出并提示。 |

---

## 3. 基座模型与二次 SFT 策略

### 3.1 基座模型分析 (`llmlora/basemodels/cmeee_merged`)

`cmeee_merged` 是基于 CMeEE（Chinese Medical Entity Extraction）领域数据完成 NER LoRA 微调并 **Merge（合并）** 后的 Qwen3.5 0.8B CausalLM。关键事实（实测）：

- 架构：`Qwen3_5ForCausalLM`，752.4M 参数，hidden_size=1024，24 层；
- **混合注意力**：`linear_attention` 与 `full_attention` 交替（`full_attention_interval=4`）；
- `tie_word_embeddings=true`，vocab_size=248320；
- `pad_token=<|endoftext|>`（248044），`eos_token=<|im_end|>`（248046）。

其优势：

1. **天然具备实体边界敏感度**：对姓名、身份证、疾病诊断、处方、药物、卡号等实体拥有 Token 级先验。
2. **二次 SFT 收敛快**：只需注入 JSON 指令遵从与上下文重写能力，1~3 Epochs 即可收敛，实体感知不衰减。

### 3.2 LoRA 注入层（实测探查结果）

基座冻结，仅对全部 Linear 叶子层注入 LoRA（r=16, alpha=32, dropout=0.05）：

- full_attention 层：`q_proj` / `k_proj` / `v_proj` / `o_proj`
- linear_attention 层：`in_proj_qkv` / `in_proj_z` / `in_proj_a` / `in_proj_b` / `out_proj`
- MLP：`gate_proj` / `up_proj` / `down_proj`

`trainer.py` 会在运行时遍历 `named_modules()` 自动探查 Linear 叶子层并与配置候选求交集，同时按 `excluded_module_keywords`（`lm_head` / `embed` / `mtp`）排除。实测可训练参数约 10.8M（全参 763M 的 1.42%）。

### 3.3 Chat Template 与 thinking 标记处理

基座 chat template 在 `add_generation_prompt=True` 时：`enable_thinking=True` 注入 `<think>\n` 前缀，否则注入 `<think>\n\n</think>\n\n` 空思考块。**训练与推理统一走 `enable_thinking=False` 分支**（`loader.render_prompt_text`），保证：

- 训练/推理 prompt 分布完全一致；
- 推理输出不携带思考标记，JSON 解析不被污染。

此外，Qwen3.5 chat template **不含 `{% generation %}` 标记**，官方 `return_assistant_tokens_mask` 恒返回全 0，不可用。

---

## 4. 数据生成与蒸馏体系（规则引擎驱动）

`scripts/generate_data.py` 实现 `RuleBasedDataGenerator`：Faker 伪造工厂 + 领域文本模板注入实体 + **项目 Layer-1 规则引擎打标** + 零泄漏 QA。

### 4.1 打标规则源（体系一致性约束）

| 规则包 | 用途 | 说明 |
|---|---|---|
| `rules/domains/general-pii.yaml` | 打标 | 身份证/手机号/姓名等 → L3 `PERSONAL_BASIC` |
| `rules/domains/medical.yaml` | 打标 | 疾病诊断等 → L4 `MEDICAL_TREATMENT` |
| `rules/domains/finance.yaml` | **禁止混入** | 使用 C2~C4（jrt0197 taxonomy）分级体系，与 default L1~L5 不兼容 |

引擎构建：`ProfileLoader(rules_dir)` → `load_taxonomy("default")` + 各域 profile → `ConfigurableRuleEngine`。每个实体的 `level` / `category` 均由 `engine.evaluate(field_name, value)` 裁定（取 rank 最高的标签）；未命中规则时回退内置兜底标签并将置信度降为 0.8。

### 4.2 Prompt 与 JSON Schema

**System Prompt**（与推理引擎共享于 `loader.SYSTEM_PROMPT`）：

```text
你是一个专业的隐私安全Sidecar助手。请分析输入的文本，识别敏感信息，输出分类分级结果（JSON格式），并提供语义连贯的无痕抹平脱敏重写文本。
```

**样本格式**（JSONL，每行 `{"input": ..., "output": ...}`），`category` 为规则引擎裁定的 taxonomy 类别：

```json
{
  "classification": {
    "max_level": "L4",
    "entities": [
      {"text": "王丹", "category": "PERSONAL_BASIC", "level": "L3", "confidence": 1.0},
      {"text": "13696001338", "category": "PERSONAL_BASIC", "level": "L3", "confidence": 1.0},
      {"text": "II型糖尿病", "category": "MEDICAL_TREATMENT", "level": "L4", "confidence": 1.0}
    ]
  },
  "smoothed_text": "患者***，性别男，32岁，诊断为***，开具处方阿莫西林克拉维酸钾，联系电话***。"
}
```

负样本（无敏感信息）：`max_level="L1"`、`entities=[]`、`smoothed_text` 等于原文。

### 4.3 抹平与零泄漏 QA（Zero-Leakage）

1. **无痕抹平**：按 span 倒序替换为占位符（`***` / `[诊断信息已抹平]` 等），并做标点清洗（避免 `（）`、`，，` 残留）；
2. **JSON 合法性校验**：`json.loads()` 必须通过；
3. **Zero-Leakage 双重校验**：
   - 字面残留检查：原始敏感值不得出现在 `smoothed_text` 中；
   - 规则引擎复扫：`smoothed_text` 再过 Layer-1 引擎，命中 L2+ 即判定泄漏并丢弃样本。

---

## 5. Prompt Labels Masking 实现

训练时 System Prompt 与 User Input 对应 token 的 `labels` 置 `-100`，CrossEntropy 仅作用于 Assistant JSON 输出。

由于官方 assistant mask 不可用（见 3.3），`loader.tokenize_sft_sample` 采用**prompt 前缀长度定位**：

1. `apply_chat_template` 一次性编码完整对话（system + user + assistant），避免分段拼接的模板格式不一致；
2. 用 `render_prompt_text`（与推理完全一致的 prompt 文本，`enable_thinking=False`）重新编码得到前缀 token 序列，其长度即 assistant 输出起点；
3. prompt 部分不含任何 assistant 内容，两次编码严格对齐，无边界 token 漂移；
4. 对齐异常时退化为全序列计算损失（而非静默产出全掩码样本）；
5. 超过 `max_length`（默认 512）尾部截断，训练前过滤 labels 全 `-100` 的样本防 NaN 损失。

---

## 6. `llmlora` 代码结构

```text
llmlora/
├── .venv/                        # 独立训练环境（transformers 5.x，继承系统 torch）
├── basemodels/
│   └── cmeee_merged/             # Qwen3.5 0.8B CMeEE 合并基座
├── data/                         # train.jsonl / dev.jsonl / test.jsonl
├── docs/
│   └── design_and_workflow.md    # [本设计方案]
├── output/
│   ├── saves/qwen35-cmeee-privacy-lora/     # LoRA adapter 权重
│   └── models/qwen35-cmeee-privacy-merged/  # 合并导出端到端模型
├── scripts/
│   ├── generate_data.py          # 规则引擎驱动的数据蒸馏（含零泄漏 QA）
│   ├── train.py                  # 训练一键启动（环境检测 + 完整 CLI）
│   └── evaluate.py               # Benchmark（JSON合法率/密级Acc/实体F1/零泄漏/延迟）
└── src/
    ├── dataset/
    │   ├── loader.py             # ChatML 构建 + prompt 前缀定位 Labels Masking
    │   └── data_collator.py      # 动态 Batch Padding Collator
    ├── inference/
    │   └── engine.py             # QwenPrivacyLoRAEngine（线程安全/延迟加载/批处理）
    ├── models/
    │   └── trainer.py            # 工业级 LoRATrainingRunner
    └── utils/
        ├── config.py             # 全局超参与路径配置（env 可覆盖）
        ├── logger.py             # 统一日志
        └── metrics.py            # JSON 合法率 / 密级 Acc / 实体 F1 / 泄漏扫描 / 延迟统计
```

### `trainer.py`（LoRATrainingRunner）要点

1. **精度自适应**：`torch.cuda.is_bf16_supported()` 优先 bf16，回退 fp16/fp32，可用 `--dtype` 强制；
2. **target_modules 自动探查**：遍历 Linear 叶子层求交集并排除 `lm_head`/`embed`/`mtp`；
3. **HF Trainer 闭环**：`processing_class` 传入 tokenizer、`load_best_model_at_end`（按 eval_loss）、`gradient_checkpointing(use_reentrant=False)`、断点续训 `--resume-from-checkpoint`；
4. **训练后生成自检**：抽 3 条样本 generate 并检查 JSON 合法率（异常仅告警不中断）；
5. **merge_and_export**：CPU 上合并 LoRA 与基座防 OOM，`safe_serialization=True` 导出独立端到端模型供 Sidecar 常驻加载。

### `engine.py`（QwenPrivacyLoRAEngine）要点

- 延迟加载基座（合并模型或 基座+LoRA adapter，peft 按需导入）；
- 双锁线程安全（初始化锁 + 推理串行锁）；
- `render_prompt_text` 与训练侧共享，`enable_thinking=False`；
- `classify()` 单条解析 JSON、`classify_batch()` 左 padding 批量生成。

---

## 7. 运行指令

```bash
cd /path/to/PrivShield

# 1. 生成训练/验证/测试数据（规则引擎打标 + 零泄漏 QA，输出到 llmlora/data/）
llmlora/.venv/bin/python -m llmlora.scripts.generate_data --train-size 1000 --dev-size 100 --test-size 50

# 2. LoRA 训练 + 自动合并导出
llmlora/.venv/bin/python -m llmlora.scripts.train --epochs 3 --batch-size 4

# 冒烟测试（10 步、不合并）
llmlora/.venv/bin/python -m llmlora.scripts.train --max-steps 10 --batch-size 2 --no-merge

# 3. Benchmark 评估（合并模型；也可 --model-path 基座 --adapter-path LoRA）
llmlora/.venv/bin/python -m llmlora.scripts.evaluate \
  --model-path llmlora/output/models/qwen35-cmeee-privacy-merged --max-samples 100
```

---

## 8. Benchmark 验证标准与冒烟实测

### 8.1 验收基线

| 指标 | 目标基线 | 测量方式 |
|---|---|---|
| **JSON 合法解析率** | 99.0%+ | 测试集输出 JSON 解析成功率 |
| **数据密级准确率 (L1~L5)** | 90%+ | 与 Layer-1 规则引擎 Ground Truth 对比 |
| **实体 F1** | 90%+ | 实体 text 精确配对 micro P/R/F1 |
| **二次扫描零泄漏率** | 99%+ | smoothed_text 字面残留 + 规则引擎复扫 |
| **推理延迟** | p50/p95 统计 | evaluate 报告 mean/p50/p95/max |

### 8.2 冒烟实测（2026-08-08，仅 10 训练步）

```text
数据生成: 200/40/20 条，零泄漏 QA 丢弃 0，密级分布 L1:37 L2:25 L3:163 L4:35
训练:     train_loss=0.337, eval_loss=0.041, 可训练参数 10.8M (1.42%)
自检:     3/3 条输出为合法 JSON，合并导出成功
评估(10条): JSON 合法率 100% | 实体 P/R/F1 = 81.8/100/90.0 |
           零泄漏率 100%（规则引擎 14 条规则复扫）
```

> 说明：冒烟仅训练 10 步，密级 Accuracy 偏低属预期；正式训练（1000+ 样本 × 3 Epochs）后各项指标应向 8.1 基线收敛。

---

## 9. 已知限制与后续工作

1. **延迟**：当前未安装 flash-linear-attention / causal-conv1d，linear_attention 层回退 torch 实现，GPU 生成延迟偏高；安装后可显著加速。
2. **BLEU 自然度指标**尚未纳入自动评估，依赖人工抽检。
3. **KMS/密钥托管**不在本方案范围，HMAC 盐仍由调用方提供。
4. 正式训练后需在 Sidecar（`PrivShield` Layer-3）中做集成回归，验证 `QwenPrivacyLoRAEngine` 替换现有 LLM 引擎后的端到端行为。
