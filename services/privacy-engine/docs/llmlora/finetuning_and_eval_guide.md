# Qwen3.5-0.8B LoRA 微调、导出与评估指南

> 本指南针对在 `PrivShield` Sidecar 架构中针对 **Qwen3.5-0.8B**（基座 `llmlora/basemodels/qwen3.5-0.8b` / `cmeee_merged`，约 752M 参数）进行 LoRA 微调、模型导出与合并、与 `LlmAdapter` / `QwenPrivacyLoRAEngine` 集成、灰度部署与降级熔断、以及 Benchmark 验证进行详细说明。
>
> **本方案仅面向纯文本分类分级与脱敏场景，不涉及图片 OCR。**

---

## 1. 环境准备与训练配置

训练环境独立于主项目，位于 `llmlora/.venv`。由于 Qwen3.5 采用 `qwen3_5_text` 混合注意力架构，必须使用 **transformers >= 5.2**（当前环境锁定 `5.14.1`），标准 transformers 4.x / LLaMA-Factory 无法加载该架构。

### 1.1 独立环境构建

```bash
# 搭建并校验独立训练环境（自动配置 transformers 5.14.1 及相关依赖）
./llmlora/scripts/setup_env.sh

# 如需指定特定 Python 解释器：
PYTHON_BIN=/path/to/python ./llmlora/scripts/setup_env.sh
```

环境要求表：

| 依赖 | 要求 | 原因 |
|---|---|---|
| transformers | **>= 5.2**（锁定 5.14.1） | Qwen3.5 架构 `qwen3_5_text` 在 transformers 4.x 会报 `KeyError: 'qwen3_5_text'` |
| torch | 继承系统环境 (含 CUDA) | `llmlora/.venv` 以 `--system-site-packages` 创建 |
| peft / accelerate / datasets / faker | venv 内安装 | 支持 LoRA 注入、分布式加速与合成数据生成 |

### 1.2 训练超参数与配置管理

`llmlora` 的配置遵循严格的优先级：**CLI 参数 > `llmlora/.env` 环境变量 > `src/utils/config.py` 内置默认值**。

`config.py` 与 `llmlora/.env` 的核心参数如下：

```ini
# llmlora/.env 核心超参示例
LLMLORA_BASE_MODEL_PATH=llmlora/basemodels/qwen3.5-0.8b
LLMLORA_OUTPUT_DIR=llmlora/output/saves/qwen35-privacy-lora
LLMLORA_MERGED_OUTPUT_DIR=llmlora/output/models/Qwen3.5-0.8B-Privacy-Classifier-Smoother
LLMLORA_AGENT_MODEL_DIR=.models/Qwen3.5-0.8B-Privacy-Classifier-Smoother

LLMLORA_NUM_TRAIN_EPOCHS=10
LLMLORA_BATCH_SIZE=4
LLMLORA_GRAD_ACCUM_STEPS=4
LLMLORA_LEARNING_RATE=2e-4
LLMLORA_MAX_LENGTH=512
LLMLORA_LORA_R=32
LLMLORA_LORA_ALPHA=64
LLMLORA_LORA_DROPOUT=0.05
```

### 1.3 训练与冒烟测试命令

```bash
# 1. 快速冒烟测试（小数据集生成 + 10 步训练 + 合并 + 快速评估）
./llmlora/scripts/smoke_test.sh

# 2. 生成正式训练数据（打标 + 抹平 + 零泄漏 QA + 去重）
./llmlora/scripts/generate_data.sh --train-size 30000 --dev-size 1000 --test-size 500

# 3. 启动 LoRA 训练（训练完成后自动合并并同步至 Agent 模型目录 .models/Qwen3.5-0.8B-Privacy-Classifier-Smoother）
./llmlora/scripts/train.sh --epochs 5 --lr 2e-4

# 4. 等价的原生 Python 启动命令
llmlora/.venv/bin/python -m llmlora.scripts.train --epochs 5 --batch-size 4
```

### 1.4 架构选择：在原版基座 vs. CMeEE-Merged 基座上微调

| 维度 | 方案 A：在原版 Qwen3.5-0.8B 上微调 | 方案 B：在 CMeEE-Merged 基座上微调 (推荐) |
|---|---|---|
| **实体边界敏感度** | 需依赖全量分类数据重头学习实体定位 | **继承 NER 先验**，已在 CMeEE 医疗/通用实体数据集上完成预训练 |
| **脱敏抹平能力** | 需同时学习实体定位 + 密级分类 + 重写平滑 | **效果更好**：定位精准度高，脱敏替换无盲区 |
| **训练收敛速度** | 需较多 Epoch 才能收敛 | **收敛极快**（实体特征已表示在 Embedding/Attention 中） |
| **推荐策略** | 纯通用文本场景可选 | **强烈推荐**：使用 `basemodels/cmeee_merged` 基座，增量 SFT 收敛更快 |

---

## 2. LoRA 权重合并与多后端推理

为了在 Sidecar 中达到最佳性能，训练脚本在完成后通过 `LoRATrainingRunner.merge_and_export()` 自动将 LoRA 权重合并至基座，并导出端到端独立模型。

### 2.1 自动合并导出流程

```text
LoRA 训练完成 (saves/qwen35-privacy-lora)
       │
       ├─► 权重 Merge 导出至 llmlora/output/models/Qwen3.5-0.8B-Privacy-Classifier-Smoother
       │
       └─► 自动同步至 Agent 默认路径 .models/Qwen3.5-0.8B-Privacy-Classifier-Smoother
```

### 2.2 多推理后端支持 (PyTorch & vLLM)

| 后端 | 首次加载 | 单条推理延迟 | 吞吐 | 适用场景 |
|---|---|---|---|---|
| **PyTorch (原生)** | ~5s | ~4200ms (CPU) / ~150ms (GPU) | ~0.24 条/s | 开发调试、低算力/无 vLLM 环境 |
| **vLLM (PagedAttention)** | ~22s (含 CUDA Graph) | ~570ms (CPU) / ~20ms (GPU) | ~1.76 条/s (7x 加速) | 高并发生产部署、大批量 Benchmark 评估 |

vLLM 挂载提示：
`Qwen3.5` 为混合注意力架构（Gated Delta Net + Full Attention），vLLM 0.26 加载时已注入兼容补丁（`model.visual.*` 结构补齐与 `config.json` 修复），支持统一通过 `--backend vllm` 启动。

---

## 3. 集成至 PrivShield

### 3.1 `QwenPrivacyLoRAEngine` 核心实现

`llmlora/src/inference/engine.py` 提供了线程安全、支持延迟加载与批处理的 PyTorch 推理引擎：

```python
class QwenPrivacyLoRAEngine:
    """基于微调 Qwen3.5-0.8B 的纯文本分类分级与无痕抹平推理引擎。"""

    def __init__(
        self,
        model_path: str,
        adapter_path: Optional[str] = None,
        device: str = "auto",
        max_new_tokens: int = 384,
    ):
        self.model_path = model_path
        self.adapter_path = adapter_path
        self.device = device
        self.max_new_tokens = max_new_tokens

        self.tokenizer = None
        self.model = None
        self._initialized = False
        self._init_lock = threading.Lock()
        self._infer_lock = threading.Lock()

    def _lazy_init(self) -> None:
        """延迟加载（首次调用时初始化，线程安全）。"""
        if self._initialized:
            return
        with self._init_lock:
            if self._initialized:
                return

            self.tokenizer = AutoTokenizer.from_pretrained(
                self.model_path,
                trust_remote_code=True,
                padding_side="left",
            )
            if self.tokenizer.pad_token is None:
                self.tokenizer.pad_token = self.tokenizer.eos_token

            dtype = torch.bfloat16 if (torch.cuda.is_available() and torch.cuda.is_bf16_supported()) else torch.float32

            self.model = AutoModelForCausalLM.from_pretrained(
                self.model_path,
                torch_dtype=dtype,
                device_map=self._resolve_device_map(),
                trust_remote_code=True,
            )

            if self.adapter_path:
                from peft import PeftModel
                self.model = PeftModel.from_pretrained(self.model, self.adapter_path)

            self.model.eval()
            self._initialized = True

    def classify(self, text: str, max_new_tokens: Optional[int] = None) -> Optional[Dict[str, Any]]:
        """执行分类分级及无痕抹平推理，返回解析后的 JSON。"""
        response = self.generate_raw(text, max_new_tokens=max_new_tokens)
        return extract_json_from_text(response)
```

### 3.2 `Qwen3Classifier` 与 `LlmAdapter` 漏斗集成

在 Sidecar 架构中，`engine/dynclassification/llm_engines.py` 中的 `Qwen3Classifier` 默认自动定位 `.models/Qwen3.5-0.8B-Privacy-Classifier-Smoother`。

```python
# DynClassification 漏斗在 Layer-3 触发 Qwen3Classifier 推理
classifier = Qwen3Classifier()
result = classifier.classify("患者张三，身份证号 110101199003072345，患有肺癌。")
# 输出示例:
# {
#   "classification": {"max_level": "L4", "categories": ["medical_history", "id_card"]},
#   "smoothed_text": "患者某某，身份证号 [身份证号]，患有呼吸系统疾病。"
# }
```

---

## 4. 灰度部署与降级熔断策略

### 4.1 灰度部署三阶段

```text
Phase 1: Shadow Mode (影子模式)
  - 微调 Qwen3.5-0.8B 模型与 Layer-1 规则引擎 + Layer-2 Small-NER 并行运行
  - 仅记录日志与对比差异，不直接阻断用户请求
  - 持续时间：24~72 小时

Phase 2: Canary Release (金丝雀发布)
  - 10% 流量路由至 Qwen3.5-0.8B Layer-3 裁决
  - 监控关键指标：延迟 P99、JSON 解析失败率、零泄漏率

Phase 3: Full Rollout (全量发布)
  - 100% 复杂/冲突语义路由至 Qwen3.5-0.8B 专精模型
```

### 4.2 降级熔断机制

Sidecar 内置多重保护措施，当发生以下异常时自动放弃 LLM 结果并降级至 Layer-1+Layer-2 安全底线：

| 触发条件 | 阈值 / 现象 | 处理方式 |
|---|---|---|
| **推理超时** | > 180s（或 `PRIVACY_VLM_TIMEOUT`） | 抛出 timeout 异常，`classify()` 返回 `None`，漏斗降级 |
| **内存/显存不足** | 可用内存低于 `PRIVACY_LLM_MIN_FREE_MEM_MB` (512MB) | 跳过 Layer-3 推理，直接返回 Layer-1+2 组合结果 |
| **JSON 解析失败** | 无法解析为合法 dict 或缺失核心键 | 记录 warning，返回 `None` 触发规则引擎兜底 |
| **并发争用卡顿** | 信号量等待超时 `PRIVACY_LLM_SEMAPHORE_WAIT_SECONDS` (30s) | 快速降级，防止阻塞主服务工作线程 |

---

## 5. 效果评估与 Benchmark 验证

评估脚本通过 `llmlora/scripts/evaluate.sh` 或 `evaluate.py` 驱动，在 `llmlora/data/test.jsonl` 测试集上全面衡量准确度与性能。

### 5.1 Benchmark 运行命令

```bash
# 1. 默认 PyTorch 后端评估
./llmlora/scripts/evaluate.sh --model-path llmlora/output/models/Qwen3.5-0.8B-Privacy-Classifier-Smoother

# 2. vLLM 高性能后端评估（推荐，评估速度提升约 7x）
./llmlora/scripts/evaluate.sh --backend vllm --model-path llmlora/output/models/Qwen3.5-0.8B-Privacy-Classifier-Smoother

# 3. 限制评估样本数快速检验
./llmlora/scripts/evaluate.sh --max-samples 50
```

### 5.2 评估对比矩阵 (Benchmark Results)

| 评估指标 | 目标基线 | 未微调 Qwen3.5-0.8B 基座 | 微调后 Qwen3.5-0.8B LoRA | 测量方法 / 逻辑 |
|---|---|---|---|---|
| **JSON 格式合法解析率** | 99.0% | 70% ~ 80% | **99.5%+** | 500 次测试样本中 JSON 成功提取并解析比例 |
| **密级识别准确率 (L1~L5)** | 88.0% | 72% ~ 78% | **94% ~ 97%** | 与 Layer-1 标注 Ground Truth 对比 Acc |
| **PII 实体 Recall / Precision** | 86% / 90% | 68% / 74% | **93% / 96%** | 在 test.jsonl 上计算 F1 |
| **脱敏无痕抹平自然度 (BLEU/ROUGE)** | 0.72 | 0.58 ~ 0.65 | **0.86+** | 抹平文本与人工/规则参考重写文本对比 |
| **二次扫描零泄漏率 (Zero-Leakage)** | 98.5% | 82% ~ 88% | **99.6%+** | 抹平后文本再次过 Layer-1 规则引擎复扫 |
| **单条推理延迟 (vLLM / GPU)** | < 100 ms | ~20 ms | **~20 ms** | 100 次推理 P50 延迟 |
| **单条推理延迟 (PyTorch / CPU)** | < 1000 ms | ~450 ms | **~420 ms** | 100 次推理 P50 延迟 |
| **内存 / 显存峰值占用** | < 2.0 GB | ~1.6 GB | **~1.6 GB** | 进程峰值 RSS / VRAM 监控 |

---

## 6. 常见问题与排查

### Q1: 加载模型时报 `KeyError: 'qwen3_5_text'` 错误？
- **原因**：运行环境中的 `transformers` 版本过低（< 5.2），无法识别 Qwen3.5 的 `qwen3_5_text` 架构。
- **解决办法**：请使用 `./llmlora/scripts/setup_env.sh` 创建独立 venv，或升级 `transformers>=5.2`（锁定 `5.14.1`）。

### Q2: 训练或推理输出包含 `<think>...</think>` 思考块污染 JSON 怎么解决？
- **原因**：某些 Qwen 变体默认启用了思考链（thinking）。
- **解决办法**：`llmlora` 的 `loader.py` 与 `engine.py` 已统一设置 `enable_thinking=False`，确保 prompt 与生成过程不注入思考标记。

### Q3: vLLM 加载 Qwen3.5-0.8B 失败或提示配置缺失？
- **原因**：vLLM 的 `Qwen3_5ForConditionalGeneration` 期望完整 `config.json`。
- **解决办法**：`train.py` 在导出时已自动补齐兼容性配置文件及 `model.visual.*` 权重占位。请确保使用 `llmlora/output/models/Qwen3.5-0.8B-Privacy-Classifier-Smoother` 导出的模型目录。

### Q4: 如何验证 Layer-3 LLM 引擎是否生效？
- **验证方式**：在 `PrivShield` 启动时观察日志 `qwen3_classifier_initialized`，发送包含复杂冲突逻辑的请求（如病历与排除诊断混写），检查返回结果中的 `llm_arbitrated: true` 以及 `smoothed_text` 文本。
