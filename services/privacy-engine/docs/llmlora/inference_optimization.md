# Qwen3.5-0.8B 单次推理性能优化方案

> **适用模型**: `llmlora/output/models/Qwen3.5-0.8B-Privacy-Classifier-Smoother`（LoRA 微调合并模型，含 153 个 vLLM 兼容补丁的 `model.visual.*` 权重）
> **运行环境**: RTX 5060（8GB/12GB）· vLLM 0.26.0（强制 V0 引擎，`VLLM_USE_V1=0`）· torch 2.11.0 · transformers 5.14.1 · Python 3.13
> **当前基线**（`llmlora/test/benchmark_report.md`，2026-08-09 实测）:

| 引擎 | Batch=1 延迟 | Batch=1 吞吐 | Batch=4 延迟 | Batch=4 吞吐 |
|---|---|---|---|---|
| vLLM | 1628.0 ms | 37.2 t/s | 528.8 ms | 129.3 t/s |
| PyTorch (HF) | 2800.2 ms | 22.9 t/s | 571.0 ms | 112.1 t/s |

> **任务特征**: 固定 4 字段 JSON 输出（`final_level`, `confidence`, `reasoning`, `sanitized_text`），实际生成约 40-64 tokens；System Prompt 固定，实测 **177 tokens / 312 字符**（`llmlora/src/dataset/loader.py:38`），短输入时约占 prefill 的 **85%**（完整 prompt ~204 tokens）；`enable_thinking=False`，`eos_token_id=248044`。

## 0. 代码现状速览

下表是优化前必须弄清的事实（避免改错位置）：

| 配置项 | Benchmark（`test/benchmark_vllm.py`） | 生产引擎（`src/inference/vllm_engine.py`） |
|---|---|---|
| `enforce_eager` | ✅ `True`（line 60，禁用了 CUDA Graphs） | 未设置（CUDA Graphs 已默认启用） |
| `max_model_len` | 4096 | 未设置（按模型默认值，注意 0.8B 模型 `max_position_embeddings=262144`，不手动限制会按长上下文预留资源） |
| `gpu_memory_utilization` | 0.5 | 0.8 |
| `enable_prefix_caching` | 未设置 | 未设置 |
| `max_tokens` | 64 | 384（`max_new_tokens`） |
| Stop 条件 | `stop_token_ids=[eos]` | `stop_token_ids=[eos]` |
| 结构化输出 | 未使用 | 未使用 |

---

## 1. 模型量化（预期加速 ~2x，推荐优先级最高）

### 1.1 原理

将 BF16（16-bit）权重压缩为 INT4/INT8，减少内存带宽需求，加速矩阵乘法。对于 0.8B 参数量的模型，量化后仍保持极高精度。

### 1.2 方案对比

| 量化方式 | 加速预期 | 精度损失 | 显存节省 | 实施难度 |
|---|---|---|---|---|
| AWQ INT4 | ~2.0x | 极小（<1%） | ~75% | 低（但需先验证架构支持，见 1.3） |
| GPTQ INT4 | ~2.0x | 极小（<1%） | ~75% | 低 |
| FP8 (W8A8) | ~1.5x | 几乎无 | ~50% | 中 |
| GGUF Q4_K_M | ~1.8x | 小 | ~75% | 中（需 llama.cpp） |

### 1.3 前置验证（重要）

- `autoawq` **尚未安装**（`pip install autoawq`），且 Qwen3.5 是混合架构（Gated Delta Net 线性注意力 + full attention），autoawq 对 `qwen3_5` 架构的支持需先跑一个小样验证；如果不支持，备选方案为 `gptqmodel`（GPTQ）或 `llm-compressor`（产出 compressed-tensors 格式，vLLM 原生支持，见 `.venv/.../vllm/model_executor/layers/quantization/compressed_tensors/`）。
- vLLM 侧已确认可行：`vllm/model_executor/models/qwen3_5.py` 的各 Linear 层均接受 `quant_config`（line 126 起），vLLM 0.26 内置 `auto_awq` / `auto_gptq` 量化后端。
- 当前 `llmlora/output/models/` 下**尚无任何量化产物**，`Qwen3.5-0.8B-Privacy-AWQ-INT4` 为待创建路径。

### 1.4 AWQ 量化实施步骤

```bash
pip install autoawq
```

```python
import json
from awq import AutoAWQForCausalLM
from transformers import AutoTokenizer

model_path = "llmlora/output/models/Qwen3.5-0.8B-Privacy-Classifier-Smoother"
quant_path = "llmlora/output/models/Qwen3.5-0.8B-Privacy-AWQ-INT4"

model = AutoAWQForCausalLM.from_pretrained(model_path)
tokenizer = AutoTokenizer.from_pretrained(model_path, trust_remote_code=True)

# dev.jsonl 每行为 {"input": ..., "output": ...}，取 input 字段做校准文本。
# 注意：autoawq 的 calib_data 接受文本列表，不能直接传 jsonl 文件路径。
with open("llmlora/data/dev.jsonl", encoding="utf-8") as f:
    calib_data = [json.loads(line)["input"] for line in f][:128]

model.quantize(
    tokenizer,
    quant_config={
        "zero_point": True,
        "q_group_size": 128,
        "w_bit": 4,
        "version": "GEMM",
    },
    calib_data=calib_data,
)
model.save_quantized(quant_path)
tokenizer.save_pretrained(quant_path)
```

### 1.5 vLLM 加载量化模型

```python
from vllm import LLM

llm = LLM(
    model="llmlora/output/models/Qwen3.5-0.8B-Privacy-AWQ-INT4",
    quantization="awq",
    trust_remote_code=True,
    max_model_len=4096,
    gpu_memory_utilization=0.5,
)
```

### 1.6 注意事项

- 量化对象是**合并后**的模型（`trainer.py` 的 `_patch_for_vllm_compatibility` 已写入 153 个 `model.visual.*` 权重），量化前确认 safetensors 中这些权重完整，否则 vLLM 加载失败。
- AWQ 校准数据使用 `dev.jsonl`（1000 条真实输入），取 128 条即可；不要用随机文本。
- 量化后必须重新验证质量（见 §9）：JSON 合法率 + 分类准确率，偏差 < 2% 才可上线。

---

## 2. 启用 CUDA Graphs（Benchmark 路径预期加速 10-20%，零成本）

### 2.1 当前问题

`enforce_eager=True` **只存在于** `llmlora/test/benchmark_vllm.py:60`，导致 benchmark 每次推理都重新 launch CUDA kernels，报告的 1628ms 延迟偏悲观。生产引擎 `src/inference/vllm_engine.py` 并未设置该参数，已经在使用 CUDA Graphs。

### 2.2 修复方式

```python
# test/benchmark_vllm.py —— 删除第 60 行 enforce_eager=True 即可
llm = LLM(
    model=model_path,
    trust_remote_code=True,
    max_model_len=4096,
    gpu_memory_utilization=0.5,
)
```

### 2.3 注意事项

- CUDA Graphs 首次执行时会有一段 warmup（捕获 graph），benchmark 应丢弃首轮计时或先跑 warmup 轮。
- 会额外占用少量显存用于 graph 缓存；RTX 5060 8GB 卡上注意与 `gpu_memory_utilization` 的联动。
- 整个项目通过 `VLLM_USE_V1=0` 强制 vLLM V0 引擎（见 `test/run_test.sh:21-23`、`vllm_engine.py:21`），V0 下 CUDA Graphs 默认开启，无需额外配置。

---

## 3. Prefix Caching（预期 TTFT 降低 ~50-80%）

### 3.1 适用场景

所有分类请求共享同一段 System Prompt（177 tokens，占短输入 prefill 的 ~85%）。缓存公共前缀的 KV cache 后，后续请求只需计算用户输入部分（~30-80 tokens）。

### 3.2 启用方式

`test/benchmark_vllm.py` 与 `src/inference/vllm_engine.py` **均未启用**，两处都可加：

```python
llm = LLM(
    model=model_path,
    trust_remote_code=True,
    max_model_len=4096,
    enable_prefix_caching=True,  # ← 启用前缀缓存
)
```

### 3.3 为什么默认是关的

vLLM 0.26 中 prefix caching 的默认值按 `is_prefix_caching_supported and not is_hybrid` 计算（`vllm/engine/arg_utils.py:2520`）——**混合架构模型默认关闭，需显式 opt-in**。Qwen3.5 是混合架构（`text_config` 中 `full_attention_interval`，线性注意力 + 每 4 层一个 full attention），所以必须手动打开。

### 3.4 注意事项

- 项目强制 V0 引擎（`VLLM_USE_V1=0`），hybrid 模型在 V0 + prefix caching 组合下的行为需实测验证；如 V0 不支持，可考虑评估切到 V1 引擎的可行性。
- 首次请求无收益（需计算完整 prefill），后续请求 TTFT 显著降低。
- 确保 system prompt 在所有请求中完全一致（包括空格和换行），否则缓存命中率下降。当前 prompt 由 `loader.py` 统一渲染，天然满足。

---

## 4. 输出长度优化（预期节省 20-40% decode 时间）

### 4.1 Stop Token 提前截断

输出是固定 JSON 格式，遇到最后一个 `}` 即可停止：

```python
from vllm import SamplingParams

sampling_params = SamplingParams(
    temperature=0.0,
    max_tokens=64,
    stop=["}"],
    include_stop_str_in_output=True,  # 保留闭合的 }，否则输出不是合法 JSON
)
```

注意事项：

- vLLM 0.26 的参数名是 **`include_stop_str_in_output`**（默认 `False`），不是 `include_stop_str`；用默认值会把 `}` 截掉，需调用方自行补回。
- `reasoning` / `sanitized_text` 字段值中如果本身含有 `}` 字符，会提前截断。训练数据受控时风险低，但更稳妥的方案是 4.2 的结构化输出。
- Benchmark 当前已设 `stop_token_ids=[eos_token_id]`（248044），生产引擎相同；stop string 是叠加其上的额外提前退出条件。
- 生产引擎 `max_new_tokens=384` 远大于实际需要（~64），配合 stop 条件可避免无效 decode。

### 4.2 结构化输出约束（Structured Outputs）

vLLM 0.26 中旧的 `GuidedDecodingParams` / `guided_decoding=` **已移除**，新 API 为 `StructuredOutputsParams` + `structured_outputs=`（后端 xgrammar 已安装）：

```python
from vllm import SamplingParams
from vllm.sampling_params import StructuredOutputsParams

json_schema = {
    "type": "object",
    "properties": {
        "final_level": {"type": "string", "enum": ["L1", "L2", "L3", "L4", "L5"]},
        "confidence": {"type": "number"},
        "reasoning": {"type": "string"},
        "sanitized_text": {"type": "string"},
    },
    "required": ["final_level", "confidence", "reasoning", "sanitized_text"],
}

sampling_params = SamplingParams(
    temperature=0.0,
    max_tokens=128,
    structured_outputs=StructuredOutputsParams(json=json_schema),
)
```

### 4.3 收益

- 避免模型生成 JSON 闭合后的多余 token。
- token 级约束保证输出 100% 符合 Schema，消除 JSON 解析失败的回退和重试。
- 结构化输出有逐 token 的 mask 计算开销，但本任务输出仅 ~40-64 tokens，净收益为正；上线前用 §9 方法实测确认。

---

## 5. PyTorch 路径优化（适用于不使用 vLLM 的场景）

现状：`test/benchmark_pytorch.py` 与 `src/inference/engine.py` 均为朴素的 `AutoModelForCausalLM.from_pretrained` + `model.generate()`，**未使用** torch.compile / StaticCache / flash-attn。

### 5.1 torch.compile

```python
model = AutoModelForCausalLM.from_pretrained(model_path, dtype=torch.bfloat16, device_map="auto")
model = torch.compile(model, mode="reduce-overhead")  # 首次编译 ~30s，后续加速 20-40%
```

### 5.2 Flash Attention 2

- 当前环境**未安装 `flash-attn`**（已安装的 `flashinfer-python 0.6.14` 只服务于 vLLM，transformers 不会用它）。
- 安装后需**显式**指定 `attn_implementation="flash_attention_2"`，transformers 不会"自动使用"：

```bash
pip install flash-attn --no-build-isolation  # 需要编译，Python 3.13 环境注意轮子可用性
```

```python
model = AutoModelForCausalLM.from_pretrained(
    model_path, dtype=torch.bfloat16, device_map="auto",
    attn_implementation="flash_attention_2",
)
```

对 Qwen3.5 的混合注意力架构，Flash Attention 仅加速 `full_attention` 层（每 4 层一个），预期 10-15% 收益；装不上时 SDPA（transformers 默认）已是不错的基线。

### 5.3 KV Cache

transformers 5.x 对 Qwen3.5 混合架构默认使用 Hybrid Cache 管理，通常无需手工干预。手动传 `StaticCache`（`from transformers.cache_utils import StaticCache`）不一定兼容混合架构（线性注意力层的 state 不是标准 KV），如需尝试请先小规模验证，不建议作为首选优化项。

---

## 6. 部署架构优化

### 6.1 异步推理 + 请求队列

将推理引擎作为常驻服务（而非每次请求加载），通过 asyncio 队列实现请求合并：

```python
# 伪代码：请求合并 batch
async def inference_service(request_queue):
    while True:
        batch = await collect_requests(request_queue, max_batch=4, timeout_ms=5)
        results = await run_batch_inference(batch)
        dispatch_results(results)
```

实测依据：Batch=4 时 vLLM 吞吐 129.3 t/s 是 Batch=1（37.2 t/s）的 3.5 倍，微批合流收益明确。

### 6.2 模型预热

服务启动后执行一次 dummy 推理，触发 CUDA Graphs 捕获和 JIT 编译：

```python
_ = llm.generate(["warmup"], SamplingParams(max_tokens=1))
```

### 6.3 多 Worker 进程

```bash
# vLLM 0.26 推荐 vllm serve 入口（python -m vllm.entrypoints.openai.api_server 亦可）
vllm serve <model_path> --port 8001 --gpu-memory-utilization 0.35 &
vllm serve <model_path> --port 8002 --gpu-memory-utilization 0.35 &
```

注意：RTX 5060 是**单卡** 8/12GB，多 worker 必须切分 `gpu_memory_utilization`。0.8B 模型 BF16 权重 ~1.7GB（AWQ 后 ~0.5GB），双 worker 在显存上可行，但每 worker 的 KV cache 空间减半，长输入场景需权衡；多 worker 方案更适用于多卡机器。

---

## 7. 长期优化方向

### 7.1 模型蒸馏

用当前 0.8B 模型作为 Teacher，蒸馏出 0.5B 甚至更小的 Student 模型：
- 输出空间固定（4 字段 JSON），蒸馏效率高。
- 可在保持 >95% 准确率的同时减少 40% 推理时间。

### 7.2 剪枝 MTP 层

当前基座包含 `mtp`（Multi-Token Prediction）层，但推理时不使用。已确认 vLLM 0.26 的 `qwen3_5.py` 在 `load_weights` 中通过 `AutoWeightsLoader(self, skip_prefixes=["mtp."])`（line 372、518）自动跳过，加载侧无需处理；如需减小磁盘体积，可在合并导出时剔除 `mtp.*` 权重。

### 7.3 定制化 Attention Kernel

针对 Qwen3.5 的 Gated Delta Net 线性注意力，可编写 Triton kernel 融合多个操作，减少内存访问。

---

## 8. 优化路线图（推荐实施顺序）

| 阶段 | 优化项 | 改动位置 | 预期效果 | 实施成本 |
|---|---|---|---|---|
| P0 | 去掉 `enforce_eager=True` | `test/benchmark_vllm.py:60` | benchmark 数字 -10-20%（仅影响测试，生产已启用） | 改一行代码 |
| P0 | 启用 `enable_prefix_caching` | `test/benchmark_vllm.py` + `src/inference/vllm_engine.py` | TTFT -50%+（需 V0 引擎下实测） | 两处各一行 |
| P0 | 生产引擎设 `max_model_len=4096` | `src/inference/vllm_engine.py` | 避免按 262144 上限预留资源 | 一行 |
| P1 | AWQ INT4 量化 | 新产物 `output/models/...-AWQ-INT4` | 吞吐 ~2x | 半天（含 §1.3 前置验证） |
| P1 | Stop token / Structured outputs | `vllm_engine.py` SamplingParams | decode -20-40%，JSON 合法率 100% | 1 小时 |
| P2 | torch.compile（PyTorch 路径） | `src/inference/engine.py` | +20-40% | 1 小时 |
| P2 | 异步服务 + 请求合并 | 新服务层 | 并发吞吐 3-5x（有 B4 实测支撑） | 1-2 天 |
| P3 | 模型蒸馏 | 训练流程 | 模型体积 -40% | 1 周 |

---

## 9. Benchmark 验证方法

### 9.1 性能指标（延迟 / 吞吐）

```bash
./llmlora/test/run_test.sh               # 全量对比（PyTorch vs vLLM），报告写入 test/benchmark_report.md
./llmlora/test/run_test.sh --vllm-only   # 仅 vLLM
./llmlora/test/run_test.sh --pytorch-only
```

注意：`run_test.sh` 链路**只测 `avg_latency_ms` / `tokens_per_sec`**，不含任何质量指标。

### 9.2 质量指标（JSON 合法率 / 分类准确率）

质量验证在另一套脚本，优化前后各跑一次对比：

```bash
./llmlora/scripts/vllm_evaluate.sh   # 或 python -m llmlora.scripts.vllm_evaluate
```

输出指标：`json_valid_rate`（JSON 合法率）、`level_accuracy`（分级准确率）、零泄露率、延迟 mean/p50/p95/max。另有 `llmlora/scripts/benchmark_vllm_performance.py` 内置 5 样本 JSON 解析自检，适合快速冒烟。

### 9.3 验收标准

- **单条延迟**（avg_latency_ms）：目标 < 1000ms
- **吞吐**（tokens_per_sec）：目标 > 80 t/s（Batch=1）
- **JSON 合法率**：优化后保持 100%（或不劣于基线）
- **分类准确率**：与未优化模型对比，偏差 < 2%

---

## 10. 相关文档

- [架构设计与工作流](design_and_workflow.md)
- [批量训练与推理调参](batch_training_and_inference.md)
- [推理性能 Benchmark 实测报告](../test/benchmark_report.md)
