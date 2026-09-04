# -*- coding: utf-8 -*-
"""
LoRA 训练与权重导出执行器 / LoRA training & merge-export runner.

针对 llmlora/basemodels/qwen3.5-0.8b（Qwen3.5-0.8B CausalLM）
Tailored for llmlora/basemodels/qwen3.5-0.8b (Qwen3.5-0.8B CausalLM),
实现工业级 SFT 闭环 / implements an industrial SFT loop:

1. Tokenizer 加载与 pad 兜底 / Tokenizer loading with pad fallback.
2. JSONL 数据加载 + Prompt Labels Masking（仅 Assistant 输出计损失） /
   JSONL loading + prompt labels masking (loss only on assistant tokens).
3. 精度与设备自动适配（bf16 / fp16 / fp32） /
   Automatic dtype & device selection (bf16 / fp16 / fp32).
4. PEFT LoRA 目标层自动探查（交集 + 排除 lm_head/embed/mtp） /
   Automatic LoRA target-module probing (intersection, excluding lm_head/embed/mtp).
5. 训练 / 验证 / 最佳 checkpoint 加载 / 断点续训 /
   Train / eval / best-checkpoint reload / resume from checkpoint.
6. 训练后抽样生成自检（JSON 合法率） /
   Post-training sampled generation self-check (JSON validity).
7. Merge & Unload 导出独立合并模型 / Merge & unload into a standalone model.

依赖环境 / Required environment: llmlora/.venv (transformers>=5.2, peft, accelerate)。
"""
from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

import torch
from datasets import Dataset
from peft import LoraConfig, PeftModel, TaskType, get_peft_model
from transformers import (
    AutoModelForCausalLM,
    AutoTokenizer,
    Trainer,
    TrainingArguments,
)

from llmlora.src.dataset.data_collator import DataCollatorForSFT, IGNORE_INDEX
from llmlora.src.dataset.loader import (
    load_jsonl,
    make_tokenize_fn,
    render_prompt_text,
)
from llmlora.src.utils.config import Config
from llmlora.src.utils.logger import setup_logger
from llmlora.src.utils.metrics import extract_json_from_text

logger = setup_logger("trainer")


class LoRATrainingRunner:
    """LoRA 微调训练与合并导出管理类 / LoRA fine-tuning & merge-export runner.

    专门适配 llmlora/basemodels/qwen3.5-0.8b 原版基座模型。
    Tailored for the Qwen3.5-0.8B base model.

    生命周期 / Lifecycle:
        runner = LoRATrainingRunner(cfg)
        runner.train()          # 训练 + 保存 + 评估 + (可选)合并导出
        runner.merge_and_export()  # 也可单独调用
    """

    def __init__(self, cfg: Config):
        """初始化执行器并校验配置 / Initialize runner and validate config."""
        self.cfg = cfg
        # 基座路径归一化为绝对路径：peft 保存的 adapter_config.json 会原样记录
        # base_model_name_or_path，相对路径会导致 adapter 换个工作目录即失效。
        # Normalize to an absolute path: peft persists base_model_name_or_path
        # verbatim in adapter_config.json; a relative path breaks the adapter
        # when loaded from any other working directory.
        self.cfg.base_model_path = str(Path(self.cfg.base_model_path).resolve())
        self.cfg.validate()
        self.tokenizer = None
        self.model = None
        # 保留原始验证集样本用于训练后生成自检
        # Keep raw dev samples for the post-training generation self-check
        self._dev_raw: List[Dict[str, Any]] = []

    # ------------------------------------------------------------------
    # 硬件与精度 / Hardware & dtype
    # ------------------------------------------------------------------

    def _resolve_dtype(self) -> Tuple[torch.dtype, bool, bool]:
        """解析计算精度 / Resolve compute dtype.

        优先级 / Priority: cfg.dtype 显式指定 > bf16 硬件支持 > fp16 > fp32。
        Explicit cfg.dtype wins, then bf16 support, then fp16, then fp32.

        Returns:
            (model_dtype, use_bf16, use_fp16) 供模型加载与 TrainingArguments 使用。
        """
        forced = (self.cfg.dtype or "auto").lower()
        cuda_available = torch.cuda.is_available()

        if forced == "bf16":
            return torch.bfloat16, True, False
        if forced == "fp16":
            return torch.float16, False, True
        if forced == "fp32":
            return torch.float32, False, False

        # auto 模式 / Auto mode
        if cuda_available and torch.cuda.is_bf16_supported():
            return torch.bfloat16, True, False
        if cuda_available:
            return torch.float16, False, True
        return torch.float32, False, False

    # ------------------------------------------------------------------
    # Tokenizer / 数据集 / Tokenizer & datasets
    # ------------------------------------------------------------------

    def prepare_tokenizer(self):
        """加载与初始化 Tokenizer / Load and initialize the tokenizer."""
        logger.info(f"正在从 {self.cfg.base_model_path} 加载 Tokenizer...")
        self.tokenizer = AutoTokenizer.from_pretrained(
            self.cfg.base_model_path,
            trust_remote_code=True,
            padding_side="right",
        )
        # 基座 tokenizer 已带 pad_token(<|endoftext|>)，此处仅作兜底
        # The base tokenizer ships a pad token; this is only a safety net
        if self.tokenizer.pad_token is None:
            self.tokenizer.pad_token = self.tokenizer.eos_token
        logger.info(
            f"Tokenizer 加载成功 | vocab={len(self.tokenizer)} "
            f"| pad_id={self.tokenizer.pad_token_id} | eos_id={self.tokenizer.eos_token_id}"
        )

    def prepare_dataset(self) -> Tuple[Dataset, Dataset]:
        """加载 JSONL 数据集并执行 Labels Masking Tokenization。

        Load JSONL datasets and apply labels-masking tokenization.

        过滤策略 / Filtering: labels 全为 -100 的样本（截断后 assistant 内容丢失）
        Samples whose labels are all -100 (assistant content lost after truncation)
        会在训练时产生 NaN 损失，直接过滤。
        would yield NaN loss and are dropped.
        """
        logger.info("加载与预处理训练/验证数据集...")
        if not os.path.exists(self.cfg.train_data_path):
            raise FileNotFoundError(
                f"未找到训练集数据: {self.cfg.train_data_path}，"
                "请先执行 python -m llmlora.scripts.generate_data"
            )
        if not os.path.exists(self.cfg.dev_data_path):
            raise FileNotFoundError(f"未找到验证集数据: {self.cfg.dev_data_path}")

        train_raw = load_jsonl(self.cfg.train_data_path)
        dev_raw = load_jsonl(self.cfg.dev_data_path)
        self._dev_raw = dev_raw
        logger.info(f"训练集样本量: {len(train_raw)} | 验证集样本量: {len(dev_raw)}")

        tokenize_fn = make_tokenize_fn(self.tokenizer, self.cfg.max_length)

        def _build(raw: List[Dict[str, Any]], split_name: str) -> Dataset:
            ds = Dataset.from_list(raw)
            ds = ds.map(
                tokenize_fn,
                batched=False,
                remove_columns=list(ds.column_names),
                desc=f"Tokenizing {split_name} (Prompt -100 Masking)",
            )
            before = len(ds)
            ds = ds.filter(
                lambda x: any(lbl != IGNORE_INDEX for lbl in x["labels"]),
                desc=f"过滤全掩码样本 {split_name}",
            )
            dropped = before - len(ds)
            if dropped:
                logger.warning(f"{split_name} 过滤 {dropped} 条全掩码样本（截断导致）")
            return ds

        train_ds = _build(train_raw, "train")
        dev_ds = _build(dev_raw, "dev")
        return train_ds, dev_ds

    # ------------------------------------------------------------------
    # 模型与 PEFT / Model & PEFT
    # ------------------------------------------------------------------

    def _find_target_modules(self) -> List[str]:
        """探查模型中实际存在的 Linear 叶子层并与候选列表求交集。

        Probe real Linear leaf modules of the model and intersect with candidates.

        排除 lm_head / embed / mtp 等非目标层，防止破坏语言模型头与
        Excludes lm_head / embed / mtp layers so the LM head, embeddings and
        多 token 预测草稿层；若交集为空则回退到全部合法 Linear 层。
        MTP draft layers stay untouched; falls back to all valid Linear leaves.
        """
        excluded = tuple(self.cfg.excluded_module_keywords)
        leaf_names: set[str] = set()
        for name, module in self.model.named_modules():
            if isinstance(module, torch.nn.Linear):
                leaf = name.split(".")[-1]
                if any(kw in name for kw in excluded):
                    continue
                leaf_names.add(leaf)

        targets = [m for m in self.cfg.target_modules if m in leaf_names]
        if not targets:
            targets = sorted(leaf_names)
            logger.warning(
                f"候选 target_modules 与基座无交集，回退为全部 Linear 层: {targets}"
            )
        return targets

    def prepare_model_and_peft(self):
        """加载基座模型并注入 PEFT LoRA 模块 / Load base model and inject LoRA."""
        model_dtype, use_bf16, _ = self._resolve_dtype()
        cuda_available = torch.cuda.is_available()
        logger.info(
            f"加载 CausalLM 基座模型 ({self.cfg.base_model_path}) "
            f"| dtype={model_dtype} | cuda={cuda_available}"
        )

        self.model = AutoModelForCausalLM.from_pretrained(
            self.cfg.base_model_path,
            torch_dtype=model_dtype,
            trust_remote_code=True,
        )
        if cuda_available:
            self.model.to("cuda")
        # 训练必须关闭 KV cache（与 gradient checkpointing 不兼容）
        # KV cache must be off during training (incompatible with grad checkpointing)
        self.model.config.use_cache = False

        valid_targets = self._find_target_modules()
        logger.info(
            f"注入 PEFT LoRA | 目标层: {valid_targets} "
            f"| r={self.cfg.lora_r} | alpha={self.cfg.lora_alpha} "
            f"| dropout={self.cfg.lora_dropout}"
        )

        lora_config = LoraConfig(
            task_type=TaskType.CAUSAL_LM,
            r=self.cfg.lora_r,
            lora_alpha=self.cfg.lora_alpha,
            lora_dropout=self.cfg.lora_dropout,
            target_modules=valid_targets,
            bias="none",
        )

        self.model = get_peft_model(self.model, lora_config)
        # gradient checkpointing 需要输入梯度回传通路
        # gradient checkpointing requires the input-require-grads hook
        if hasattr(self.model, "enable_input_require_grads"):
            self.model.enable_input_require_grads()

        self.model.print_trainable_parameters()

    # ------------------------------------------------------------------
    # 训练主流程 / Training main flow
    # ------------------------------------------------------------------

    def train(self):
        """执行完整训练流程 / Run the full training pipeline."""
        self.prepare_tokenizer()
        train_ds, dev_ds = self.prepare_dataset()
        self.prepare_model_and_peft()

        _, use_bf16, use_fp16 = self._resolve_dtype()

        # 梯度检查点：混合注意力架构可能不完全支持，失败时自动降级关闭
        # Gradient checkpointing may not be fully supported by hybrid attention;
        # degrade gracefully on failure.
        grad_ckpt = self.cfg.gradient_checkpointing
        grad_ckpt_kwargs: Dict[str, Any] = {"use_reentrant": False}

        training_args = TrainingArguments(
            output_dir=self.cfg.output_dir,
            num_train_epochs=self.cfg.num_epochs,
            max_steps=self.cfg.max_steps,
            per_device_train_batch_size=self.cfg.batch_size,
            per_device_eval_batch_size=self.cfg.batch_size,
            gradient_accumulation_steps=self.cfg.grad_accum_steps,
            learning_rate=self.cfg.learning_rate,
            weight_decay=self.cfg.weight_decay,
            warmup_ratio=self.cfg.warmup_ratio,
            lr_scheduler_type="cosine",
            logging_steps=self.cfg.logging_steps,
            save_strategy="steps",
            save_steps=self.cfg.save_steps,
            save_total_limit=self.cfg.save_total_limit,
            eval_strategy="steps",
            eval_steps=self.cfg.eval_steps,
            load_best_model_at_end=True,
            metric_for_best_model="eval_loss",
            greater_is_better=False,
            bf16=use_bf16,
            fp16=use_fp16,
            gradient_checkpointing=grad_ckpt,
            gradient_checkpointing_kwargs=grad_ckpt_kwargs if grad_ckpt else None,
            optim=self.cfg.optim,
            report_to=[],
            dataloader_pin_memory=torch.cuda.is_available(),
            dataloader_num_workers=self.cfg.dataloader_num_workers,
            remove_unused_columns=False,
            seed=self.cfg.seed,
        )

        data_collator = DataCollatorForSFT(
            tokenizer=self.tokenizer,
            pad_to_multiple_of=8,
        )

        trainer = Trainer(
            model=self.model,
            args=training_args,
            train_dataset=train_ds,
            eval_dataset=dev_ds,
            data_collator=data_collator,
            processing_class=self.tokenizer,
        )

        logger.info("开始执行 LoRA 微调...")
        train_result = trainer.train(
            resume_from_checkpoint=self.cfg.resume_from_checkpoint
        )

        metrics = train_result.metrics
        train_loss = metrics.get("train_loss")
        logger.info(
            f"训练完成: train_loss={'N/A' if train_loss is None else round(train_loss, 4)}, "
            f"runtime={metrics.get('train_runtime', 'N/A')}s"
        )

        logger.info(f"保存 LoRA 权重及配置到: {self.cfg.output_dir}")
        trainer.save_model(self.cfg.output_dir)
        self.tokenizer.save_pretrained(self.cfg.output_dir)

        eval_results = trainer.evaluate()
        logger.info(f"验证集 eval_loss: {eval_results.get('eval_loss', 'N/A')}")

        # 训练后生成自检：验证模型能否产出合法 JSON
        # Post-training generation self-check: verify the model emits valid JSON
        self._sanity_generate()

        # 释放 trainer 与显存 / Release trainer and VRAM before optional merge
        del trainer
        if torch.cuda.is_available():
            torch.cuda.empty_cache()

        if self.cfg.merge_on_completion:
            self.merge_and_export()

    def _sanity_generate(self, num_samples: int = 3, max_new_tokens: int = 256):
        """用少量验证集样本做生成自检 / Generation self-check on a few dev samples.

        不阻断训练流程：任何异常只记录告警。
        Never blocks the pipeline: exceptions are logged as warnings only.
        """
        if not self._dev_raw:
            return
        try:
            device = next(self.model.parameters()).device
            self.model.eval()
            valid = 0
            samples = self._dev_raw[:num_samples]
            for sample in samples:
                prompt_text = render_prompt_text(self.tokenizer, sample.get("input", ""))
                inputs = self.tokenizer(prompt_text, return_tensors="pt").to(device)
                with torch.inference_mode():
                    outputs = self.model.generate(
                        **inputs,
                        max_new_tokens=max_new_tokens,
                        do_sample=False,
                        pad_token_id=self.tokenizer.pad_token_id,
                    )
                response = self.tokenizer.decode(
                    outputs[0][inputs["input_ids"].shape[1]:],
                    skip_special_tokens=True,
                )
                parsed = extract_json_from_text(response)
                if parsed is not None:
                    valid += 1
            logger.info(f"生成自检: {valid}/{len(samples)} 条输出为合法 JSON")
        except Exception as exc:  # noqa: BLE001 - 自检不应中断主流程
            logger.warning(f"生成自检失败（不影响训练产物）: {exc}")

    # ------------------------------------------------------------------
    # 合并导出 / Merge & export
    # ------------------------------------------------------------------

    def merge_and_export(self):
        """将训练好的 LoRA 权重与基座模型融合，导出独立模型。

        Merge trained LoRA weights with the base model and export as a standalone model.
        导出格式兼容 vLLM v0.26（Qwen3_5ForConditionalGeneration 多模态包装器）。
        Export format is compatible with vLLM v0.26 (Qwen3_5ForConditionalGeneration).
        """
        logger.info("开始执行 LoRA 权重与基座模型合并 (Merge & Unload)...")
        os.makedirs(self.cfg.merged_output_dir, exist_ok=True)
        if self.tokenizer is None:
            self.prepare_tokenizer()

        try:
            # 合并导出统一使用基座原始 dtype（config.json 中为 bfloat16）
            # Merge & export in the base model's native dtype (bfloat16)
            export_dtype = self._resolve_dtype()[0]

            base_model = AutoModelForCausalLM.from_pretrained(
                self.cfg.base_model_path,
                torch_dtype=export_dtype,
                device_map="cpu",
                trust_remote_code=True,
            )
            peft_model = PeftModel.from_pretrained(base_model, self.cfg.output_dir)
            merged_model = peft_model.merge_and_unload()

            logger.info(f"保存合并后的文本模型权重到: {self.cfg.merged_output_dir}")
            merged_model.save_pretrained(
                self.cfg.merged_output_dir,
                max_shard_size=self.cfg.max_shard_size,
                safe_serialization=True,
            )
            self.tokenizer.save_pretrained(self.cfg.merged_output_dir)
            # 同步生成配置，保证推理侧采样参数一致
            # Persist generation config so inference uses identical sampling params
            if getattr(base_model, "generation_config", None) is not None:
                base_model.generation_config.save_pretrained(self.cfg.merged_output_dir)

            # ------------------------------------------------------------------
            # vLLM v0.26 兼容性补丁 / vLLM v0.26 compatibility patch
            # ------------------------------------------------------------------
            # vLLM 模型注册表仅有 Qwen3_5ForConditionalGeneration（多模态），
            # 不支持独立的 Qwen3_5ForCausalLM。因此需要：
            # 1. 用原始基座的完整 config.json（含 vision_config + text_config 嵌套）
            # 2. 将原始基座的 visual.* 权重合并到 merged safetensors
            #
            # vLLM's model registry only has Qwen3_5ForConditionalGeneration
            # (multimodal), not standalone Qwen3_5ForCausalLM. So we must:
            # 1. Use the original base's full config.json (with vision_config)
            # 2. Copy visual.* weights from the original base into merged safetensors
            self._patch_for_vllm_compatibility(export_dtype)

            logger.info("权重合并并导出成功！")
            if self.cfg.auto_copy_to_agent_dir and self.cfg.agent_model_dir:
                self._copy_to_agent_model_dir()
        except Exception as exc:
            logger.error(f"合并权重时发生异常: {exc}", exc_info=True)
            raise

    def _copy_to_agent_model_dir(self) -> None:
        """将合并导出后的完整模型自动同步复制到 Agent 部署目录 (.models/Qwen3.5-0.8B-Privacy-Classifier-Smoother)。

        Auto-copy the merged model artifacts into the main Agent model deployment directory.
        """
        import shutil

        src_dir = Path(self.cfg.merged_output_dir)
        dst_dir = Path(self.cfg.agent_model_dir)

        if not src_dir.exists():
            logger.warning(f"源合并模型目录不存在，跳过自动同步复制: {src_dir}")
            return

        logger.info(f"🚀 开始将合并模型自动同步复制到 Agent 部署目录: {dst_dir}")
        dst_dir.mkdir(parents=True, exist_ok=True)

        for item in src_dir.iterdir():
            dst_item = dst_dir / item.name
            if item.is_dir():
                if dst_item.exists():
                    shutil.rmtree(dst_item)
                shutil.copytree(item, dst_item)
            else:
                shutil.copy2(item, dst_item)

        logger.info(f"✅ 模型已成功同步复制到 Agent 部署目录: {dst_dir}")


    def _patch_for_vllm_compatibility(self, export_dtype: torch.dtype) -> None:
        """补丁合并模型以兼容 vLLM 加载 / Patch merged model for vLLM compatibility.

        vLLM v0.26 仅注册了 Qwen3_5ForConditionalGeneration（多模态架构），
        需要完整的 config.json（嵌套 text_config + vision_config）和 visual.* 权重。
        """
        from safetensors.torch import load_file, save_file

        merged_dir = Path(self.cfg.merged_output_dir)
        base_dir = Path(self.cfg.base_model_path)
        config_path = merged_dir / "config.json"

        # 1. 用原始基座模型的完整 config.json 替换拍平的文本配置
        # Replace the flattened text config with the original base's full config
        base_config_path = base_dir / "config.json"
        if base_config_path.exists():
            import shutil
            shutil.copy2(str(base_config_path), str(config_path))

            # 更新 text_config 中被 LoRA 训练修改的参数（如有）
            # Update text_config params that may have changed during LoRA training
            with open(config_path, "r", encoding="utf-8") as f:
                cfg_data = json.load(f)
            cfg_data.pop("sliding_window", None)
            cfg_data.pop("use_sliding_window", None)
            with open(config_path, "w", encoding="utf-8") as f:
                json.dump(cfg_data, f, indent=2, ensure_ascii=False)
            logger.info("config.json 已替换为原始基座完整格式（含 vision_config）")

        # 2. 将原始基座的 model.visual.* 权重合并到 merged safetensors
        # Merge model.visual.* weights from the original base into merged safetensors
        base_st_files = list(base_dir.glob("*.safetensors"))
        merged_st_files = list(merged_dir.glob("*.safetensors"))

        if base_st_files and merged_st_files:
            # 加载原始基座的所有权重（含 model.visual.*）
            # Load all weights from the original base (including model.visual.*)
            visual_weights: Dict[str, torch.Tensor] = {}
            for st_file in base_st_files:
                state_dict = load_file(str(st_file), device="cpu")
                for key, tensor in state_dict.items():
                    if key.startswith("model.visual.") or key.startswith("visual."):
                        target_key = key if key.startswith("model.visual.") else f"model.{key}"
                        visual_weights[target_key] = tensor.to(export_dtype)

            if visual_weights:
                # 将 visual 权重写入 merged 的第一个 safetensors 分片
                # Write visual weights into the first merged safetensors shard
                target_file = merged_st_files[0]
                existing = load_file(str(target_file), device="cpu")
                existing.update(visual_weights)
                save_file(existing, str(target_file))
                logger.info(
                    f"已合并 {len(visual_weights)} 个 visual.* 权重到 {target_file.name}"
                )


def run_lora_training(cfg: Config) -> LoRATrainingRunner:
    """启动微调主入口 / Main entry for fine-tuning."""
    runner = LoRATrainingRunner(cfg)
    runner.train()
    return runner
