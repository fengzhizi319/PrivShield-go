# llmlora — Qwen3.5 基座 LoRA 专精微调（纯文本隐私分类分级与无痕抹平）

本工程位于 `services/privacy-engine/model-training/llmlora/`，专为 **PrivShield 核心隐私计算引擎（privacy-engine）** 的 Layer-3 仲裁与无痕抹平模块服务。

基于 `basemodels/qwen3.5-0.8b`（原版 Qwen3.5 0.8B CausalLM，约 752M 参数）做 LoRA SFT，专门面向**纯文本**场景（不考虑图片 OCR）：

- **分类分级仲裁**：L1~L5 密级裁定（Ground Truth 来自项目 Layer-1 规则引擎）
- **无痕抹平脱敏**：上下文自然重写（Natural Context Rewriting），零泄漏 QA 保证
- **产物自动就绪**：合并导出模型默认自动同步至 Agent 生产部署目录 `services/privacy-engine/.models/Qwen3.5-0.8B-Privacy-Classifier-Smoother`

完整设计方案见 [docs/llmlora/design_and_workflow.md](../../docs/llmlora/design_and_workflow.md)。

---

## 1. 环境要求（重要）

| 依赖 | 要求 | 原因 |
|---|---|---|
| transformers | **>= 5.2**（当前锁定 5.14.1） | Qwen3.5 架构 `qwen3_5_text` 在 transformers 4.x 无法加载 |
| torch | 继承系统环境（支持 CUDA） | venv 以 `--system-site-packages` 创建 |
| peft / accelerate / datasets / faker | venv 内安装 | LoRA 注入与数据蒸馏 |

训练环境独立于主项目，位于 `services/privacy-engine/model-training/llmlora/.venv`，首次使用先执行 `setup_env.sh`。

---

## 2. 目录结构

```text
services/privacy-engine/model-training/llmlora/
├── .venv/                        # 独立训练环境（transformers 5.x）
├── basemodels/
│   └── qwen3.5-0.8b/             # 原版 Qwen3.5 0.8B CausalLM 基座
├── data/                         # train.jsonl / dev.jsonl / test.jsonl
├── output/
│   ├── saves/qwen35-privacy-lora/               # LoRA adapter 权重
│   └── models/Qwen3.5-0.8B-Privacy-Classifier-Smoother/  # 合并导出端到端模型
├── scripts/                      # 常用命令 sh 脚本 + Python 入口
└── src/
    ├── dataset/                  # loader.py (Labels Masking) / data_collator.py
    ├── inference/
    │   ├── engine.py             # QwenPrivacyLoRAEngine (PyTorch 原生推理)
    │   ├── engine_vllm.py        # QwenPrivacyVLLMEngine (vLLM，evaluate --backend vllm)
    │   └── vllm_engine.py        # QwenPrivacyVLLMEngine (vLLM，vllm_evaluate 专用)
    ├── models/                   # trainer.py (LoRATrainingRunner)
    └── utils/                    # config.py / logger.py / metrics.py
```

---

## 3. 常用命令脚本（`scripts/`）

所有脚本自动定位仓库根目录与隐私引擎目录、配置 PYTHONPATH、校验独立 venv，可在任意目录执行；额外参数原样透传给对应 Python 入口。

| 脚本 | 用途 |
|---|---|
| `scripts/setup_env.sh` | 创建/更新独立训练环境（transformers 5.14.1 等依赖）并校验版本 |
| `scripts/generate_data.sh` | 生成训练/验证/测试数据（规则引擎打标 + 零泄漏 QA + 跨分割去重） |
| `scripts/train.sh` | LoRA 训练一键启动（默认训练完自动合并导出并同步复制至 `services/privacy-engine/.models/Qwen3.5-0.8B-Privacy-Classifier-Smoother`） |
| `scripts/evaluate.sh` | Benchmark 评估（JSON 合法率/密级 Acc/实体 F1/零泄漏/延迟） |
| `scripts/smoke_test.sh` | 端到端冒烟：小数据生成 → 10 步训练 + 合并 → 快速评估 |

### 3.1 首次使用：搭建环境

```bash
# 从仓库任意位置执行
./services/privacy-engine/model-training/llmlora/scripts/setup_env.sh
# 指定解释器：PYTHON_BIN=/path/to/python ./services/privacy-engine/model-training/llmlora/scripts/setup_env.sh
```

### 3.2 数据生成

```bash
./services/privacy-engine/model-training/llmlora/scripts/generate_data.sh                          # 默认 1000/100/50
./services/privacy-engine/model-training/llmlora/scripts/generate_data.sh --train-size 2000 --seed 123
```

> **双默认说明**：`generate_data.sh` 不传参时注入 `--train-size 1000 --dev-size 100 --test-size 50`（开发调试用小批量）；
> 直接调用 Python 入口 `python -m llmlora.scripts.generate_data` 的 argparse 默认是 **30000/1000/500**（正式训练用全量）。
> 二者差异是有意为之：日常使用 sh 脚本，正式跑全量数据用 Python 入口或显式传参。

数据管道要点：

- 打标：`ConfigurableRuleEngine`（general-pii + medical 规则包，default L1~L5 体系），未命中走内置兜底标签；
- 抹平：AGE 一律先做 K-匿名泛化（与密级无关），高敏疾病按范畴降级/彻底隐去，PII 按类别掩码；
- QA：零泄漏双重校验（字面残留 + 规则引擎复扫），不合格样本丢弃并补采；
- 去重：以 input 全文为键跨 train/dev/test 去重（负样本固定模板除外），杜绝相同敏感样本跨分割泄漏。

### 3.3 训练

```bash
# 超参默认取 Config / .env，自动合并并同步到 services/privacy-engine/.models/
./services/privacy-engine/model-training/llmlora/scripts/train.sh
./services/privacy-engine/model-training/llmlora/scripts/train.sh --epochs 5 --lr 1e-4             # 显式传参优先于 .env
./services/privacy-engine/model-training/llmlora/scripts/train.sh --max-steps 10 --no-merge        # 冒烟快跑
./services/privacy-engine/model-training/llmlora/scripts/train.sh --resume-from-checkpoint <dir>   # 断点续训
```

> **超参优先级**：CLI 显式传参 > `llmlora/.env`（`LLMLORA_*` 环境变量）> `src/utils/config.py` 内置默认值。
> argparse 所有超参默认 `None`，未显式传参时不会覆盖 Config/.env。
> 内置默认值：epochs=10, max_steps=-1, batch_size=4, grad_accum=4, lr=2e-4, max_length=512,
> lora r=32, alpha=64, dropout=0.05, seed=42, dtype=auto。
>
> 训练保存的 `adapter_config.json` 中 `base_model_name_or_path` 一律记录**绝对路径**
> （`LoRATrainingRunner` 初始化时归一化），adapter 可在任意工作目录下加载；
> 历史产物中的相对路径不受此修复影响。

### 3.4 评估

```bash
# PyTorch 后端（默认）
./services/privacy-engine/model-training/llmlora/scripts/evaluate.sh                               # 默认评估合并模型
./services/privacy-engine/model-training/llmlora/scripts/evaluate.sh --max-samples 20

# vLLM 后端（更快，约 7x 加速）
./services/privacy-engine/model-training/llmlora/scripts/evaluate.sh --backend vllm --max-samples 20

# 基座 + LoRA adapter 模式
./services/privacy-engine/model-training/llmlora/scripts/evaluate.sh \
    --model-path services/privacy-engine/model-training/llmlora/basemodels/qwen3.5-0.8b \
    --adapter-path services/privacy-engine/model-training/llmlora/output/saves/qwen35-privacy-lora
```

> 注意：使用合并模型时默认**不再叠加** adapter（避免双重应用 LoRA 权重）；
> 如需叠加请显式传 `--adapter-path`。
>
> 零泄漏率指标依赖项目规则引擎复扫；规则引擎不可用时该指标输出 **N/A**
> （结果 JSON 中 `zero_leak_rate_available: false`），不再静默记 100%。

| 推理后端 | 首次加载 | 单条推理延迟 | 吞吐 | 适用场景 |
|---|---|---|---|---|
| PyTorch（默认） | ~5s | ~4200ms | ~0.24 条/s | 开发调试、小批量评估 |
| vLLM | ~22s（含 CUDA Graph 捕获） | ~570ms | ~1.76 条/s | 大批量评估、生产部署 |

### 3.5 端到端冒烟测试

```bash
./services/privacy-engine/model-training/llmlora/scripts/smoke_test.sh
```

---

## 4. 核心 Python 参数速查

`train.py` 所有超参默认 `None`（不覆盖 Config/.env）；下表为 `config.py` 内置默认值：

| 参数（train.py） | 内置默认 | 说明 |
|---|---|---|
| `--epochs` | 10 | 训练轮数 |
| `--max-steps` | -1 | 最大步数（-1=跑满 epoch，冒烟用） |
| `--batch-size` / `--grad-accum-steps` | 4 / 4 | 批大小 / 梯度累积 |
| `--lr` | 2e-4 | 学习率 |
| `--max-length` | 512 | 单样本最大 token 长度 |
| `--lora-r` / `--lora-alpha` / `--lora-dropout` | 32 / 64 / 0.05 | LoRA 超参 |
| `--dtype` | auto | auto / bf16 / fp16 / fp32 |
| `--agent-model-dir` | `services/privacy-engine/.models/Qwen3.5-0.8B-Privacy-Classifier-Smoother` | 训练合并后自动同步的目标路径 |
| `--no-merge` | — | 训练后不自动合并导出 |
| `--no-copy-to-agent` | — | 训练合并后不自动复制到 Agent .models 部署目录 |

---

## 5. 技术要点速览

1. **规则引擎驱动的数据打标**：`generate_data.py` 对接项目 `ConfigurableRuleEngine`（仅 general-pii + medical 规则包；finance 为 C 级体系不混入），level/category 由规则裁定，配合零泄漏双重校验（字面残留 + 规则复扫）与跨分割 input 去重。
2. **Prompt Labels Masking**：Qwen3.5 chat template 不含 `{% generation %}` 标记，官方 assistant mask 不可用；`loader.py` 采用「prompt 前缀长度定位」方案，损失仅作用于 Assistant JSON 输出。
3. **thinking 标记处理**：训练与推理统一 `enable_thinking=False`，避免模板注入空思考块污染 JSON 输出。
4. **LoRA 注入层**：自动探查全部 Linear 叶子层（含混合注意力的 `in_proj_*` 系列），排除 `lm_head`/`embed`/`mtp`，可训练参数约 1.42%。
5. **Sidecar 接入**：`src/inference/engine.py` 提供 `QwenPrivacyLoRAEngine`（线程安全、延迟加载、批处理），可替换 PrivShield Layer-3 的通用 LLM 引擎。
6. **双推理后端**：支持 PyTorch 原生推理和 vLLM 高性能推理，通过 `--backend pytorch|vllm` 切换。vLLM 后端利用 PagedAttention 和 CUDA Graphs 实现约 7x 加速；LoRA adapter 挂载走 vLLM 0.26 正确 API（构造时 `enable_lora=True`，推理时传 `LoRARequest`）。
