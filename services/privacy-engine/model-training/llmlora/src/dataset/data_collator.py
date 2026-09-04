# -*- coding: utf-8 -*-
"""
llmlora DataCollator 模块 / Data collator for SFT dynamic batching.

为 CausalLM SFT 提供动态 padding：
Provides dynamic padding for CausalLM SFT:
- input_ids 用 pad_token_id 右侧填充 / input_ids right-padded with pad_token_id
- attention_mask 填充位置置 0 / attention_mask zeroed at padded positions
- labels 填充位置置 -100（不参与损失） / labels padded with -100 (ignored in loss)
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Dict, List

import torch
from transformers import PreTrainedTokenizerBase

# 与 loader.py 保持一致的损失忽略值 / Loss ignore index consistent with loader.py
IGNORE_INDEX = -100


@dataclass
class DataCollatorForSFT:
    """SFT 批数据整理器 / Batch collator for SFT samples.

    Attributes:
        tokenizer: 提供 pad_token_id / Supplies pad_token_id.
        pad_to_multiple_of: 序列长度对齐倍数（8 对 TensorCore 友好） /
            Pad length multiple (8 is TensorCore friendly).
    """

    tokenizer: PreTrainedTokenizerBase
    pad_to_multiple_of: int = 8

    def _pad_id(self) -> int:
        """解析填充 token id，缺失时回退 eos 或 0 / Resolve pad id with fallbacks."""
        if self.tokenizer.pad_token_id is not None:
            return int(self.tokenizer.pad_token_id)
        if self.tokenizer.eos_token_id is not None:
            return int(self.tokenizer.eos_token_id)
        return 0

    def __call__(self, features: List[Dict[str, Any]]) -> Dict[str, torch.Tensor]:
        """将变长样本列表整理成等长 batch / Collate variable-length samples.

        Args:
            features: tokenize 后的样本字典列表，每个含
                input_ids / attention_mask / labels。

        Returns:
            批张量字典 / Dict of batched long tensors.
        """
        if not features:
            raise ValueError("DataCollatorForSFT 收到空 batch")

        max_len = max(len(f["input_ids"]) for f in features)
        if self.pad_to_multiple_of > 0 and max_len % self.pad_to_multiple_of != 0:
            max_len = ((max_len // self.pad_to_multiple_of) + 1) * self.pad_to_multiple_of

        pad_id = self._pad_id()
        batch_size = len(features)

        batch_input_ids = torch.full((batch_size, max_len), pad_id, dtype=torch.long)
        batch_attention_mask = torch.zeros((batch_size, max_len), dtype=torch.long)
        batch_labels = torch.full((batch_size, max_len), IGNORE_INDEX, dtype=torch.long)

        for i, feature in enumerate(features):
            ids = feature["input_ids"]
            length = len(ids)
            if length == 0:
                continue
            batch_input_ids[i, :length] = torch.as_tensor(ids, dtype=torch.long)
            batch_attention_mask[i, :length] = torch.as_tensor(
                feature["attention_mask"], dtype=torch.long
            )
            batch_labels[i, :length] = torch.as_tensor(feature["labels"], dtype=torch.long)

        return {
            "input_ids": batch_input_ids,
            "attention_mask": batch_attention_mask,
            "labels": batch_labels,
        }
