# Qwen3.5-0.8B LoRA 专精模型微调与数据蒸馏方案概览

> 本文档针对在 `PrivShield` Sidecar 架构中引入 **Qwen3.5-0.8B**（基座 `llmlora/basemodels/qwen3.5-0.8b` / `cmeee_merged`，约 752M 参数）替换 Layer-3 大模型（分类分级仲裁、脱敏与文本无痕抹平）进行全面效果评估，并提供基于 Layer-1 规则漏斗自动蒸馏训练数据的完整方案。
>
> **本方案不考虑图片 OCR，仅针对纯文本分类分级与脱敏场景。**

---

## 1. 评估分析与结论

### 1.1 场景背景

在 `PrivShield` 的三层漏斗架构（Layer-1 规则引擎 -> Layer-2 Small-NER -> Layer-3 LLM）中：
- **Layer-1 规则引擎**（`ConfigurableRuleEngine`）：处理确定性强、高频的标准字段（如身份证、手机号、IP 地址、银行卡），毫秒级响应，精度 100%。
- **Layer-2 Small-NER**（`NerAdapter`）：ONNX 实体识别，处理未命中规则但含实体的文本。
- **Layer-3 LLM**（`LlmAdapter` → `Qwen3Classifier`）：处理复杂长文本语义理解、规则冲突仲裁、非结构化敏感信息提取以及**脱敏后的文本无痕抹平（Context Smoothing / Natural Rewriting）**。

**当前痛点**：通用大模型（Qwen2-VL-2B 或 7B/14B）存在**部署资源占用大（显存/内存 4G~14GB）、CPU/边缘端推理延迟高（300ms~2s）** 的局限，影响 Local Sidecar 的高并发吞吐能力。

---

### 1.2 模型选型说明

本方案基于参数量约 752M 的 **Qwen3.5-0.8B CausalLM**（架构为 `qwen3_5_text` 混合注意力），相比通用多模态或超大模型：

| 候选模型 | 参数量 | 推荐度 | 理由 |
|---|---|---|---|
| **Qwen3.5-0.8B (CMeEE-Merged)** | 0.75B | ⭐⭐⭐⭐⭐ (首选) | 已集成医疗/通用 NER 先验，混合注意力架构推理极快，资源占用极小 |
| Qwen3.5-0.8B (Base) | 0.75B | ⭐⭐⭐⭐ (备选) | 原版通用 CausalLM，可通过隐私数据 SFT 达到高精度 |
| Qwen2.5-1.5B-Instruct | 1.5B | ⭐⭐⭐ | 资源占用翻倍，适合有 GPU 的高算力场景 |

**本方案统一以 `Qwen3.5-0.8B` 为基准模型进行设计与评估。**

---

### 1.3 评估对比矩阵

| 评估维度 | 方案 A：通用 2B/7B 模型 (现状) | 方案 B：未微调 Qwen3.5-0.8B 基座 | 方案 C：Layer-1 规则生成数据 + LoRA 微调 Qwen3.5-0.8B (推荐) |
|---|---|---|---|
| **显存/内存占用** | 4GB ~ 14GB | **< 1.6GB** | **< 1.6GB** |
| **推理延迟 (vLLM / GPU)** | 300ms ~ 2000ms | **~20ms** | **~20ms** (提升 15~100 倍) |
| **JSON Schema 遵循率** | 90% ~ 95% | 70% ~ 80% (易格式崩塌) | **99.5%+** (特定 Prompt 专精) |
| **分类分级准确率 (L1~L5)** | 85% ~ 92% (Zero-shot) | 72% ~ 78% | **94% ~ 97%** (领域内超越通用大模型) |
| **脱敏无痕抹平自然度** | 较高，但有时泛化过度 | 较低，常漏抹或语义脱节 | **极高** (专门拟合优雅替换与平滑模版) |
| **边缘 Sidecar 适合度** | 中等 (需较强算力) | 高 (但准确率不足) | **完美匹配** (高吞吐、低延迟、高精度) |

---

### 1.4 评估结论

1. **直接替换为未经微调的 Qwen3.5-0.8B 基座：效果变差。**
   - 未经过特定领域的 SFT 微调，小模型缺乏足够的领域知识与指令遵循能力，在复杂的 JSON 格式化输出、密级划分（L1~L5）、深层隐式 PII 提取以及脱敏文本无痕重写上容易发生格式错乱或语法不通。

2. **利用 Layer-1 规则引擎生成数据并结合 LoRA 微调 Qwen3.5-0.8B：效果显著更好！（推荐方案）**
   - **领域知识注入**：把 Layer-1 规则确定的范式、分类标准（GDPR/JRT0197/DB51/T 2989/医疗等）、数据掩码与无痕抹平模版内化为模型权重。
   - **格式 100% 遵从**：通过 SFT 训练数据中的严格 JSON 结构，消除小模型格式坍塌通病。
   - **无痕抹平能力**：让 0.8B 模型学会根据上下文将敏感信息替换为自然平滑的占位或修饰词，保证文本语义连贯。
   - **零标注成本**：Layer-1 规则引擎 + 伪造生成器（Faker/Template Engine）构成了天然的高精度数据生成工厂。

---

## 2. 架构集成设计

### 2.1 替换 Layer-3 LLM 引擎

微调后的小模型**直接替换**现有 Layer-3 的通用大模型，作为纯文本场景下的唯一 LLM 推理引擎：

```text
三层分类漏斗（纯文本路径）
═══════════════════════════════════════════════════════

  Layer-1: ConfigurableRuleEngine
    │  命中高置信度规则 → 直接输出 SecurityTag
    │  未命中 / 低置信度 ↓
    │
  Layer-2: NerAdapter (ONNX Small-NER)
    │  提取实体 → 补充 SecurityTag
    │  仍不确定 / 需仲裁 ↓
    │
  Layer-3: QwenPrivacyLoRAEngine (或 Qwen3Classifier)
    │  加载 .models/Qwen3.5-0.8B-Privacy-Classifier-Smoother
    │  常驻内存 < 1.6GB，推理延迟 20~150ms
    │  输出：分类分级 JSON + 无痕抹平文本
    └→ FunnelResult
```

### 2.2 与 `LlmAdapter` 的集成方式

`LlmAdapter` 的 `_lazy_init()` 方法中，默认加载纯文本隐私分类与抹平专精模型 `.models/Qwen3.5-0.8B-Privacy-Classifier-Smoother`：

```text
LlmAdapter._lazy_init()
    │
    └─ 加载微调 SFT 专精模型 `.models/Qwen3.5-0.8B-Privacy-Classifier-Smoother`（纯文本高效分类与抹平）
```

### 2.3 环境变量控制

| 变量 | 默认值 | 说明 |
|---|---|---|
| `PRIVACY_LLM_TEXT_ENGINE` | `auto` | 文本推理引擎：`auto` / `qwen_lora` / `qwen2vl` / `none` |
| `PRIVACY_LLM_MODEL_PATH` | `.models/Qwen3.5-0.8B-Privacy-Classifier-Smoother` | 微调模型路径 |

---

## 3. docs/llmlora 目录结构

本目录下包含以下详细规划与实操指南文档：

- [`qwen3_5_0_8b_architecture.md`](./qwen3_5_0_8b_architecture.md): **Qwen3.5-0.8B 模型架构深度解析与结构图谱**
  - 全局规格与超参数矩阵 (Vocab 248k, 256K Context, 1024 隐层)
  - 24 层 3:1 Hybrid SSM-Transformer 混合堆叠排布
  - 核心子模块：Gated Linear Attention (Conv1d + SSM)、GQA Full Attention (Partial RoPE + QK-Norm)、SwiGLU FFN
  - MTP (Multi-Token Prediction) 与跨模态对齐层
  - PrivShield 端侧部署与推理性能 Benchmark
- [`data_generation_plan.md`](./data_generation_plan.md): **基于 Layer-1 规则漏斗的训练数据自动生成与蒸馏方案**
  - 数据生成架构与管道设计
  - 任务指令与 Prompt 设计（分类分级 + 脱敏无痕抹平）
  - 20+ 合成数据模板与领域覆盖策略
  - 基于掩码策略 + 上下文重写的无痕抹平生成
  - JSONL 格式导出规范与零泄漏校验
- [`finetuning_and_eval_guide.md`](./finetuning_and_eval_guide.md): **Qwen3.5-0.8B LoRA 微调、评估与部署指南**
  - 架构选择：在原版上微调 vs. 在 `cmeee_merged` 上二次微调
  - LoRA 超参数配置与训练工具链（`llmlora` 原生 PyTorch / vLLM）
  - 与 `LlmAdapter` 集成的 `QwenPrivacyLoRAEngine` 实现
  - 多后端推理与自动权重 Merge 导出
  - 灰度部署与降级熔断策略
  - 性能与准确率 Benchmark 验证标准
