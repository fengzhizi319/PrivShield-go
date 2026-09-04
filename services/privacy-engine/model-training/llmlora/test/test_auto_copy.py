"""Test auto-copy of merged LoRA model to Agent .models directory.

干净克隆（无 basemodels/ 模型权重、无 llmlora 训练环境）下也可运行：
- torch/transformers/peft/datasets 任一缺失时整模块 pytest.skip
  （trainer 模块顶层 import 这些重依赖）；
- Config.validate() 的路径校验通过 tmp_path 伪目录满足，
  不再隐式依赖真实 basemodels/ 与 rules/ 目录。
"""

from __future__ import annotations

import pytest

# 重依赖缺失时整体跳过（干净克隆的根 .venv 通常不含训练栈）
pytest.importorskip("torch", reason="需要 llmlora 训练环境（torch）")
pytest.importorskip("transformers", reason="需要 llmlora 训练环境（transformers）")
pytest.importorskip("peft", reason="需要 llmlora 训练环境（peft）")
pytest.importorskip("datasets", reason="需要 llmlora 训练环境（datasets）")

import sys
from pathlib import Path

_THIS_DIR = Path(__file__).resolve().parent
_MODEL_TRAINING_DIR = _THIS_DIR.parent.parent
if str(_MODEL_TRAINING_DIR) not in sys.path:
    sys.path.insert(0, str(_MODEL_TRAINING_DIR))

from llmlora.src.models.trainer import LoRATrainingRunner
from llmlora.src.utils.config import Config


def test_copy_to_agent_model_dir(tmp_path):
    src_dir = tmp_path / "merged_output"
    dst_dir = tmp_path / "agent_models" / "Qwen3.5-0.8B-Privacy-Classifier-Smoother"

    src_dir.mkdir(parents=True, exist_ok=True)
    (src_dir / "config.json").write_text('{"model_type": "qwen3_5"}', encoding="utf-8")
    (src_dir / "model.safetensors").write_text("fake_weights_data", encoding="utf-8")

    sub_dir = src_dir / "sub_folder"
    sub_dir.mkdir()
    (sub_dir / "tokenizer.json").write_text('{"tokenizer": "test"}', encoding="utf-8")

    cfg = Config()
    # 用 tmp_path 伪目录满足 validate()，避免隐式依赖真实 basemodels/ 与 rules/
    fake_base = tmp_path / "basemodel"
    fake_base.mkdir()
    fake_rules = tmp_path / "rules"
    fake_rules.mkdir()
    cfg.base_model_path = str(fake_base)
    cfg.rules_dir = str(fake_rules)
    cfg.merged_output_dir = str(src_dir)
    cfg.agent_model_dir = str(dst_dir)
    cfg.auto_copy_to_agent_dir = True

    runner = LoRATrainingRunner(cfg)
    runner._copy_to_agent_model_dir()

    assert dst_dir.exists()
    assert (dst_dir / "config.json").read_text(encoding="utf-8") == '{"model_type": "qwen3_5"}'
    assert (dst_dir / "model.safetensors").read_text(encoding="utf-8") == "fake_weights_data"
    assert (dst_dir / "sub_folder" / "tokenizer.json").read_text(encoding="utf-8") == '{"tokenizer": "test"}'
