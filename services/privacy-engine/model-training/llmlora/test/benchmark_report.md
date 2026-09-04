# Qwen3.5-0.8B 隐私分类与无痕抹平模型 推理性能 Benchmark 报告

> **生成时间**: 2026-08-09 12:03:35  
> **测试模型**: `llmlora/output/models/Qwen3.5-0.8B-Privacy-Classifier-Smoother` (合并后的 0.8B Standalone 模型)  
> **精度格式**: `bfloat16`  
> **对比引擎**: PyTorch Native (`bfloat16`) vs vLLM Engine  

---

## 1. vLLM 加载根因修复与架构总结

| 维 度 | 遇到的技术问题 (Problem) | 根本原因 (Root Cause) | 修复方案 (Fix Applied) |
|---|---|---|---|
| **Config 路由** | vLLM 无法识别纯文本架构 | 导出模型架构为 `Qwen3_5ForCausalLM`，vLLM 注册表中仅注册了多模态 `Qwen3_5ForConditionalGeneration` | 复制基座完整 `config.json`（保留 `vision_config` + `text_config` 嵌套） |
| **视觉权重缺失** | 误判缺失张量无法初始化 | 合并模型仅导出 `model.language_model.*`，缺失 `model.visual.*`（153 个张量） | 动态提取基座 `visual.*` 权重并重命名为 `model.visual.*` 补全合并文件 |
| **KV Cache OOM** | 初始化阶段触发 OOM 崩溃 | `max_position_embeddings=262144` 导致分配预留近 3GB KV Cache 空间 | 设置 `max_model_len=4096` 限制注意力缓存空间 |
| **Ninja 缺失** | FlashInfer JIT 编译报错 | FlashInfer 算子热编译依赖 `ninja` 构筑器 | `pip install ninja` 并将 venv bin 注入 `PATH` 环境变量 |

---

## 2. 引擎性能对比汇总 (Batch Latency & Throughput)

| Batch Size | PyTorch 延迟 (ms) | PyTorch 吞吐 (tokens/s) | vLLM 延迟 (ms) | vLLM 吞吐 (tokens/s) | vLLM 加速比 (Speedup) |
|---|---|---|---|---|---|
| **Batch = 1** | **2,800.2 ms** | **22.9 tokens/s** | **1,628.0 ms** | **37.2 tokens/s** | **1.72x (显著提升)** |
| **Batch = 4** | **571.0 ms** | **112.1 tokens/s** | **528.8 ms** | **129.3 tokens/s** | **1.08x (高并发微升)** |

---

## 3. 部署建议 (Deployment Recommendations)

1. **单条低延迟响应 (Batch=1)**：vLLM 在单条请求场景下吞吐达到 **37.2 tokens/s**，相比原生 PyTorch 实现了 **1.72 倍加速**，显著降低了 Sidecar 交互延迟。
2. **高并发高吞吐 (Batch=4)**：在批处理并发下，vLLM 吞吐进一步提升至 **129.3 tokens/s**。
3. **架构部署结论**：在侧边栏隐私代理中，推荐优先选择 **vLLM (`max_model_len=4096`)** 作为高效率推理引擎，在小 Batch 下具备最佳响应速度。