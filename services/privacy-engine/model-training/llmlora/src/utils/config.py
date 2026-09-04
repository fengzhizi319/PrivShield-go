# -*- coding: utf-8 -*-
"""
llmlora 全局配置模块 / Global configuration for the llmlora LoRA pipeline.

集中管理路径、训练超参、PEFT LoRA 与硬件适配配置。
Centralizes paths, training hyper-parameters, PEFT LoRA and hardware settings.

路径约定 / Path conventions:
- 所有默认路径相对于「仓库根目录」解析（llmlora/ 的上一级），
  All default paths are resolved against the repository root (parent of llmlora/),
  因此无论从哪个工作目录启动脚本都能定位到正确文件。
  so scripts work regardless of the current working directory.
"""
from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import List

_THIS_FILE = Path(__file__).resolve()
LLMLORA_DIR = _THIS_FILE.parents[2]
MODEL_TRAINING_DIR = LLMLORA_DIR.parent
ENGINE_DIR = MODEL_TRAINING_DIR.parent

# 仓库根目录 (PrivShield)
if len(_THIS_FILE.parents) >= 7 and (_THIS_FILE.parents[6] / "services").exists():
    _REPO_ROOT = _THIS_FILE.parents[6]
else:
    cur = LLMLORA_DIR
    while cur.parent != cur:
        if (cur / "go.work").exists() or (cur / "services").exists():
            break
        cur = cur.parent
    _REPO_ROOT = cur

# 自动尝试从 llmlora/.env、engine/.env 或根目录 .env 加载环境变量
# Automatically load environment variables from llmlora/.env or repo root .env
try:
    import dotenv
    if (LLMLORA_DIR / ".env").exists():
        dotenv.load_dotenv(LLMLORA_DIR / ".env")
    elif (ENGINE_DIR / ".env").exists():
        dotenv.load_dotenv(ENGINE_DIR / ".env")
    elif (_REPO_ROOT / ".env").exists():
        dotenv.load_dotenv(_REPO_ROOT / ".env")
except ImportError:
    pass


def _env(key: str, default: str) -> str:
    """读取环境变量并回退默认值 / Read env var with fallback default."""
    return os.environ.get(key, default)


def _env_int(key: str, default: int) -> int:
    """读取整数型环境变量 / Read integer env var."""
    val = os.environ.get(key)
    return int(val) if val is not None else default


def _env_float(key: str, default: float) -> float:
    """读取浮点型环境变量 / Read float env var."""
    val = os.environ.get(key)
    return float(val) if val is not None else default


def _env_bool(key: str, default: bool) -> bool:
    """读取布尔型环境变量 / Read boolean env var."""
    val = os.environ.get(key)
    if val is None:
        return default
    return val.lower() in ("true", "1", "yes", "on")


@dataclass
class Config:
    """llmlora 全局配置参数 / Global configuration parameters.

    字段按「路径 / 训练超参 / LoRA / 优化器与硬件」四组组织。
    Fields are grouped into paths / training hyper-params / LoRA / optimizer & hardware.
    """

    # ------------------------------------------------------------------
    # 1. 路径配置 / Path configuration
    # ------------------------------------------------------------------

    # 底层基座模型路径（原版 Qwen3.5-0.8B CausalLM）
    # Base model path (Original Qwen3.5-0.8B CausalLM)
    base_model_path: str = field(
        default_factory=lambda: _env(
            "LLMLORA_BASE_MODEL", str(LLMLORA_DIR / "basemodels" / "qwen3.5-0.8b")
        )
    )

    # 规则库目录（供数据生成管道对接项目规则引擎）
    # Rules directory (used by the data pipeline to plug in the project rule engine)
    rules_dir: str = field(
        default_factory=lambda: _env(
            "LLMLORA_RULES_DIR",
            str(ENGINE_DIR / "rules" if (ENGINE_DIR / "rules").exists() else _REPO_ROOT / "rules"),
        )
    )

    # 数据集目录 / Dataset directory
    data_dir: str = field(
        default_factory=lambda: _env("LLMLORA_DATA_DIR", str(LLMLORA_DIR / "data"))
    )

    # 训练输出目录（LoRA adapter checkpoint）
    # Output directory for LoRA adapter checkpoints
    output_dir: str = field(
        default_factory=lambda: _env(
            "LLMLORA_OUTPUT_DIR",
            str(LLMLORA_DIR / "output" / "saves" / "qwen35-privacy-lora"),
        )
    )

    # 合并导出目录（LoRA + 基座 融合后的独立模型）
    # Merged export directory (base + LoRA fused into a standalone model)
    merged_output_dir: str = field(
        default_factory=lambda: _env(
            "LLMLORA_MERGED_OUTPUT_DIR",
            str(LLMLORA_DIR / "output" / "models" / "Qwen3.5-0.8B-Privacy-Classifier-Smoother"),
        )
    )

    # Agent 主模型部署目录（训练合并后自动同步的目标路径）
    # Target deployment directory for PrivShield (services/privacy-engine/.models)
    agent_model_dir: str = field(
        default_factory=lambda: _env(
            "LLMLORA_AGENT_MODEL_DIR",
            str(
                (ENGINE_DIR / ".models" / "Qwen3.5-0.8B-Privacy-Classifier-Smoother")
                if (ENGINE_DIR / ".models").exists()
                else (_REPO_ROOT / ".models" / "Qwen3.5-0.8B-Privacy-Classifier-Smoother")
            ),
        )
    )

    # 训练合并完成后是否自动复制模型权重到 agent_model_dir
    # Whether to auto-copy merged model to agent_model_dir upon completion
    auto_copy_to_agent_dir: bool = field(
        default_factory=lambda: _env_bool("LLMLORA_AUTO_COPY_TO_AGENT_DIR", True)
    )


    @property
    def train_data_path(self) -> str:
        """训练集路径 / Training set path."""
        return str(Path(self.data_dir) / "train.jsonl")

    @property
    def dev_data_path(self) -> str:
        """验证集路径 / Validation set path."""
        return str(Path(self.data_dir) / "dev.jsonl")

    @property
    def test_data_path(self) -> str:
        """测试集路径 / Test set path."""
        return str(Path(self.data_dir) / "test.jsonl")

    # ------------------------------------------------------------------
    # 2. 训练超参数 / Training hyper-parameters
    # ------------------------------------------------------------------

    # 单条样本最大 token 长度（Prompt + Response 总和）
    # Max token length per sample (prompt + response combined)
    max_length: int = field(default_factory=lambda: _env_int("LLMLORA_MAX_LENGTH", 512))
    # 每卡训练批大小 / Per-device training batch size
    batch_size: int = field(default_factory=lambda: _env_int("LLMLORA_BATCH_SIZE", 4))
    # 梯度累积步数（有效批大小 = batch_size * grad_accum_steps * 卡数）
    # Gradient accumulation steps (effective batch = batch_size * grad_accum * n_gpu)
    grad_accum_steps: int = field(
        default_factory=lambda: _env_int("LLMLORA_GRAD_ACCUM_STEPS", 4)
    )
    # LoRA SFT 常用学习率区间 1e-4 ~ 3e-4 / Common LoRA SFT LR range
    learning_rate: float = field(
        default_factory=lambda: _env_float("LLMLORA_LEARNING_RATE", 2e-4)
    )
    weight_decay: float = field(
        default_factory=lambda: _env_float("LLMLORA_WEIGHT_DECAY", 0.01)
    )
    num_epochs: int = field(default_factory=lambda: _env_int("LLMLORA_NUM_EPOCHS", 10))
    warmup_ratio: float = field(
        default_factory=lambda: _env_float("LLMLORA_WARMUP_RATIO", 0.05)
    )
    logging_steps: int = field(
        default_factory=lambda: _env_int("LLMLORA_LOGGING_STEPS", 10)
    )
    save_steps: int = field(default_factory=lambda: _env_int("LLMLORA_SAVE_STEPS", 50))
    eval_steps: int = field(default_factory=lambda: _env_int("LLMLORA_EVAL_STEPS", 50))
    # 最多保留 checkpoint 数量 / Max checkpoints kept on disk
    save_total_limit: int = field(
        default_factory=lambda: _env_int("LLMLORA_SAVE_TOTAL_LIMIT", 3)
    )
    seed: int = field(default_factory=lambda: _env_int("LLMLORA_SEED", 42))
    # 断点续训目录（None 表示从头训练） / Resume checkpoint dir (None = fresh start)
    resume_from_checkpoint: str | None = field(
        default_factory=lambda: _env("LLMLORA_RESUME_FROM_CHECKPOINT", None)
    )
    # DataLoader worker 数 / DataLoader worker count
    dataloader_num_workers: int = field(
        default_factory=lambda: _env_int("LLMLORA_DATALOADER_NUM_WORKERS", 4)
    )
    # 最大训练步数（-1 表示跑满 epoch，冒烟测试可设小值）
    # Max training steps (-1 = full epochs; small value for smoke tests)
    max_steps: int = field(default_factory=lambda: _env_int("LLMLORA_MAX_STEPS", -1))

    # ------------------------------------------------------------------
    # 3. PEFT LoRA 配置 / PEFT LoRA configuration
    # ------------------------------------------------------------------

    lora_r: int = field(default_factory=lambda: _env_int("LLMLORA_LORA_R", 32))
    lora_alpha: int = field(default_factory=lambda: _env_int("LLMLORA_LORA_ALPHA", 64))
    lora_dropout: float = field(
        default_factory=lambda: _env_float("LLMLORA_LORA_DROPOUT", 0.05)
    )
    # 候选目标层：与基座实际 Linear 层求交集后注入 LoRA。
    # Candidate target modules: intersected with real Linear leaf names of the base.
    target_modules: List[str] = field(
        default_factory=lambda: [
            "q_proj",
            "k_proj",
            "v_proj",
            "o_proj",
            "in_proj_qkv",
            "in_proj_z",
            "in_proj_a",
            "in_proj_b",
            "out_proj",
            "gate_proj",
            "up_proj",
            "down_proj",
        ]
    )
    # 注入 LoRA 时必须排除的模块（语言模型头 / 嵌入 / MTP 草稿层）
    # Modules that must never receive LoRA (LM head / embeddings / MTP draft layers)
    excluded_module_keywords: List[str] = field(
        default_factory=lambda: ["lm_head", "embed", "mtp"]
    )

    # ------------------------------------------------------------------
    # 4. 优化器与硬件 / Optimizer & hardware
    # ------------------------------------------------------------------

    optim: str = field(default_factory=lambda: _env("LLMLORA_OPTIM", "adamw_torch"))
    # 梯度检查点显著降低显存占用（0.8B 模型单卡 24G 可开 batch 16+）
    # Gradient checkpointing greatly reduces VRAM usage
    gradient_checkpointing: bool = field(
        default_factory=lambda: _env_bool("LLMLORA_GRADIENT_CHECKPOINTING", True)
    )
    # 训练完成后是否自动合并导出 / Whether to merge & export after training
    merge_on_completion: bool = field(
        default_factory=lambda: _env_bool("LLMLORA_MERGE_ON_COMPLETION", True)
    )
    # 合并导出时的最大分片大小 / Max shard size when exporting merged weights
    max_shard_size: str = field(
        default_factory=lambda: _env("LLMLORA_MAX_SHARD_SIZE", "2GB")
    )
    # 强制指定计算精度（"auto" | "bf16" | "fp16" | "fp32"）
    # Force compute dtype ("auto" | "bf16" | "fp16" | "fp32")
    dtype: str = field(default_factory=lambda: _env("LLMLORA_DTYPE", "auto"))

    # ------------------------------------------------------------------
    # 校验 / Validation
    # ------------------------------------------------------------------

    def validate(self) -> bool:
        """校验关键路径存在性 / Validate that key paths exist.

        Raises:
            FileNotFoundError: 基座模型目录缺失 / Base model directory missing.
        """
        if not Path(self.base_model_path).exists():
            raise FileNotFoundError(f"未找到基座模型路径: {self.base_model_path}")
        if not Path(self.rules_dir).exists():
            raise FileNotFoundError(f"未找到规则库目录: {self.rules_dir}")
        return True
