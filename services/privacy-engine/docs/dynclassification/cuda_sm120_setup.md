# PyTorch CUDA (Blackwell sm_120 / RTX 50 系列) 环境构建与避坑全指南

本文档详细记录了在 **NVIDIA GeForce RTX 50 系列 (RTX 5060 / 5070 / 5080 / 5090)** 等基于 **Blackwell 架构 (`sm_120`)** 的硬件环境上，如何成功安装配置 CUDA 12.8 加速的 PyTorch 环境，并在 `PrivShield` 侧边栏中运行 PyTorch / ModelScope 版 Layer-2 Small-NER 实体识别。

---


## 1. 痛点与背景分析

### 1.1 `sm_120` 算力缺失异常

NVIDIA RTX 50 系列采用了全新的 Blackwell GPU 架构，其 Compute Capability 为 `sm_120`。
使用 PyTorch 官方 Stable 版本（如 PyTorch 2.5 / 2.6，默认带 CUDA 12.1 或 CUDA 12.4 预编译包）时：

- `torch.cuda.is_available()` 会诡异地返回 `True`；
- 但在实际运行张量运算或加载模型权重时，会抛出以下硬件 Kernel 缺失崩溃：
  ```text
  RuntimeError: CUDA error: no kernel image is available for execution on the device
  ```

### 1.2 解决方案概述

必须采用支持 **CUDA 12.8+ 且显式包含 `sm_120` 算力编译目标** 的 PyTorch Nightly 包（例如 `torch-2.12.0.dev20260408+cu128`），并补齐配套的 NVIDIA 运行时 C++ 共享库与 `torchvision` 包。

---

## 2. 步骤一：安装支持 `sm_120` 的 PyTorch Nightly 包

将预先下载好的 PyTorch `cu128` Nightly Wheel 包（存放在 `.models/` 目录下）强行重装安装至所有使用的 Python 环境（包括 Conda `pri` 环境、`.venv` 虚拟环境与 Base 环境）：

```bash
# 1. 在当前激活的 Python 环境中安装
pip install .models/torch-2.12.0.dev20260408+cu128-cp313-cp313-manylinux_2_28_x86_64.whl --force-reinstall --no-deps

# 2. 在 Conda pri 环境中安装
pip install .models/torch-2.12.0.dev20260408+cu128-cp313-cp313-manylinux_2_28_x86_64.whl --force-reinstall --no-deps

# 3. 在 .venv 虚拟环境中安装
.venv/bin/pip install .models/torch-2.12.0.dev20260408+cu128-cp313-cp313-manylinux_2_28_x86_64.whl --force-reinstall --no-deps
```

*(若无离线 Wheel 包，也可直接从 PyTorch Nightly Index 在线安装：`pip install --pre torch --index-url https://download.pytorch.org/whl/nightly/cu128`)*

---

## 3. 步骤二：配套依赖库与 C++ 共享库补齐

在仅安装 PyTorch `cu128` Wheel 后，直接 `import torch` 会触发各种系统 `.so` 共享库缺失异常。需按顺序安装以下配套依赖：

### 3.1 补充精确版本的 NVIDIA C++ 运行库

```bash
pip install "nvidia-nvshmem-cu12==3.4.5" \
            "nvidia-cusparselt-cu12==0.7.1" \
            nvidia-cuda-cupti-cu12 \
            nvidia-cufft-cu12 \
            nvidia-nccl-cu12 \
            nvidia-cublas-cu12 \
            nvidia-cudnn-cu12
```

### 3.2 匹配安装 Nightly 版 `torchvision`

避免旧版 `torchvision` (如 0.28.0+cpu) 与新 PyTorch 发生 C++ ABI 冲突（报错 `operator torchvision::nms does not exist`）：

```bash
pip install --pre torchvision --index-url https://download.pytorch.org/whl/nightly/cu128 --no-deps --force-reinstall
```

---

## 4. 关键踩坑点与代码级解决方案 (Critical Pitfalls & Solutions)

### 🚨 踩坑 1：`ImportError: libcupti.so.12: cannot open shared object file`

- **原因分析**：
  pip 安装的 `nvidia-*` 扩展包将动态链接库散落在 `site-packages/nvidia/*/lib` 和 `site-packages/triton/backends/nvidia/lib/cupti` 中。Linux 系统默认的 `dlopen` 不会自动搜索 Python `site-packages` 的深层子目录。
- **解决方案**：
  在 [`engine/dynclassification/base.py`](../../engine/dynclassification/base.py#L70-L115) 的 `SmallNerEngine` 基类中实现了 `_preload_nvidia_libs()` 静态方法。在 `import torch` 执行前，通过 `ctypes.CDLL(..., mode=ctypes.RTLD_GLOBAL)` 自动遍历并预加载底层的 CUDA 共享库，并实时更新 `os.environ["LD_LIBRARY_PATH"]`：

```python
@staticmethod
def _preload_nvidia_libs() -> None:
    """动态寻找并预加载 CUDA/Triton C++ 共享库，更新 LD_LIBRARY_PATH。"""
    try:
        import ctypes, os, sys
        lib_dirs, candidate_files = [], []

        for s_dir in sys.path:
            if not s_dir or not os.path.exists(s_dir):
                continue
            for base in ("nvidia", "triton"):
                p = os.path.join(s_dir, base)
                if os.path.exists(p):
                    for root, _, files in os.walk(p):
                        if "lib" in root or "cupti" in root:
                            if root not in lib_dirs:
                                lib_dirs.append(root)
                        for f in files:
                            if ".so" in f and any(k in f for k in ("cupti", "cufft", "nvshmem", "cublas", "cudnn", "cuda_runtime")):
                                candidate_files.append(os.path.join(root, f))

        if lib_dirs:
            existing = os.environ.get("LD_LIBRARY_PATH", "")
            os.environ["LD_LIBRARY_PATH"] = ":".join(lib_dirs) + (":" + existing if existing else "")

        # 保证预加载顺序：nvshmem -> cufft -> cupti
        def sort_key(path: str) -> int:
            if "nvshmem" in path: return 0
            if "cufft" in path: return 1
            if "cupti" in path: return 2
            return 3

        candidate_files.sort(key=sort_key)
        for lib_path in candidate_files:
            try: ctypes.CDLL(lib_path, mode=ctypes.RTLD_GLOBAL)
            except Exception: pass
    except Exception: pass
```

---

### 🚨 踩坑 2：LLVM 命令行选项冲突崩溃 (`LLVM ERROR: inconsistency in registered CommandLine options ('enable-fs-discriminator')`)

- **原因分析**：
  ONNX Runtime (内置 LLVM 引擎) 与 PyTorch (内置 LLVM 编译器) 若在同一个 Python 进程中同时加载 `libonnxruntime.so` 和 `torch._C`，会导致全局 LLVM 命令行标志（如 `enable-fs-discriminator`）重复注册抛出 fatal error 并强行终止进程。
- **解决方案**：
  `_preload_nvidia_libs()` 必须**严格过滤 ONNX Runtime 库文件**（`any(k in f for k in ("cupti", "cufft", "nvshmem"...))`），仅对 NVIDIA 底层驱动动态库进行预加载，切勿全局盲目 `RTLD_GLOBAL` 加载所有 `.so` 文件。

---

### 🚨 踩坑 3：PyTorch CUDA 探针误判

- **原因分析**：
  在 `sm_120` GPU 上，仅仅判断 `torch.cuda.is_available()` 会错误地返回 `True`，但在创建引擎阶段仍会崩溃。
- **解决方案**：
  在 [`ModelScopeSmallNerEngine._is_cuda_compatible(torch)`](../../engine/dynclassification/ner_engines.py#L630-L645) 中增加了真正执行微小 CUDA Kernel 计算的探测代码：

```python
@classmethod
def _is_cuda_compatible(cls, torch: Any) -> bool:
    """验证当前 PyTorch 是否真能在检测到的 CUDA 设备上执行 kernel。"""
    cls._preload_nvidia_libs()
    if not torch.cuda.is_available():
        return False
    try:
        a = torch.tensor([1.0, 2.0, 3.0], device="cuda")
        b = torch.tensor([1.0, 1.0, 1.0], device="cuda")
        _ = (a + b).sum().item()
        return True  # 只有 Kernel 矩阵加法实际成功才返回 True
    except RuntimeError:
        return False # 算力缺失时优雅降级为 CPU
```

---

## 5. 端到端功能与性能验证

### 5.1 基础 PyTorch CUDA 运算验证

运行以下命令校验 GPU 矩阵乘法与显存分配：

```bash
python -c "
from engine.dynclassification.ner_engines import ModelScopeSmallNerEngine
ModelScopeSmallNerEngine._preload_nvidia_libs()

import torch
a = torch.ones((1000, 1000), device='cuda')
b = a * 2.5
print('CUDA 设备名称:', torch.cuda.get_device_name(0))
print('矩阵求和结果:', b.sum().item())
print('已分配 VRAM (MB):', round(torch.cuda.memory_allocated()/1024/1024, 2))
"
```

**预期输出**：
```text
CUDA 设备名称: NVIDIA GeForce RTX 5060 Laptop GPU
矩阵求和结果: 2500000.0
已分配 VRAM (MB): 7.63
```

---

### 5.2 ModelScope Small-NER 医疗实体识别 CUDA 推理实测

```bash
python -c "
from engine.dynclassification.ner_engines import ModelScopeSmallNerEngine
engine = ModelScopeSmallNerEngine(device='cuda')
res = engine.extract('患者诊断为急性心肌梗死和高血压')
print('CUDA ModelScope NER 结果:', res)
"
```

**预期输出**：
```text
CUDA ModelScope NER 结果: [
  {'text': '急性心肌梗死', 'label': 'MEDICAL_DISEASE', 'confidence': 1.0}, 
  {'text': '高血压', 'label': 'MEDICAL_DISEASE', 'confidence': 1.0}
]
```

---

### 5.3 运行单元测试集

执行单元测试验证硬件级联与自动降级逻辑：

```bash
# 运行全部 Layer-2 NER 单元测试
PYTHONPATH=. pytest tests/dynclassification/test_ner_adapter.py -v

# 运行真实模型测试
PYTHONPATH=. pytest tests/dynclassification/test_real_models.py -v
```

---

## 6. Layer-3 Qwen3.5 微调模型本地 PyTorch 推理环境搭建

本节记录在 **`.venv`（项目根虚拟环境）与 `llmlora/.venv`（LoRA 训练环境）** 两个 Python 3.13 环境中，成功运行 Layer-3 微调模型 `.models/Qwen3.5-0.8B-Privacy-Classifier-Smoother`（`Qwen3Classifier` 本地 PyTorch 后端，`PRIVACY_LLM_PROVIDER=qwen3`）所需的完整依赖与踩坑记录。两个环境均已实测通过：

```bash
PYTHONPATH=. pytest 'tests/dynclassification/test_real_models.py::TestRealLlmAdapter::test_real_llm_loads_and_returns_structured_result' -v -m ''
```

### 6.1 依赖版本矩阵（已验证组合）

| 包 | 已验证版本 | 必需性 | 说明 |
|---|---|---|---|
| `torch` | `2.12.0.dev20260408+cu128` | 必需 | 见本文第 2 节（sm_120 支持） |
| `transformers` | `>=5.14.1` | 必需 | 4.x 未注册 `qwen3_5`（`qwen3_next`）架构，无法加载 |
| `huggingface_hub` | `>=1.27.0` | 必需 | transformers 5.x 硬依赖 1.x API，旧版 0.x 不兼容 |
| `einops` | `>=0.8.2` | 必需 | `Qwen3_5ForCausalLM` 加载时硬依赖 |
| `fla-core` | `>=0.5.2` | 必需 | flash-linear-attention，提供线性注意力层 triton kernel；缺失时回退有缺陷的纯 PyTorch 实现，生成可能损坏/截断 |
| `triton` | `3.6.0` | 必需（GPU） | 由 torch 管理；**3.7.1 在 WSL 下无法发现 GPU driver**，需降级 |
| `causal-conv1d` | `>=1.5.0` | 可选 | mamba/conv 层 CUDA fast-path，缺失仅影响性能不影响正确性 |
| `tokenizers` / `safetensors` | `0.22.x` / `>=0.8.0` | 随 transformers 自动安装 | 无需特殊处理 |

### 6.2 一键安装命令

```bash
# 网络较慢时先清除失效代理，并使用清华镜像源
unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY
PIP_MIRROR="-i https://pypi.tuna.tsinghua.edu.cn/simple"

# 升级 transformers 到 5.x（会自动带上 huggingface_hub>=1.x）
pip install $PIP_MIRROR "transformers>=5.14.0" "huggingface_hub>=1.27.0"

# Qwen3.5 加载与推理必备依赖
pip install $PIP_MIRROR "einops>=0.8.0" "fla-core>=0.5.2"

# 可选：fast-path 加速（mamba/conv 层 CUDA kernel）
pip install $PIP_MIRROR causal-conv1d

# WSL 下若 triton 为 3.7.x 且报 "0 active drivers"，降级到 3.6.0
pip install $PIP_MIRROR triton==3.6.0
```

> **离线/慢网络替代方案**：若两环境 Python 版本一致（本项目均为 3.13），可直接从另一个可用环境拷贝纯 Python 包目录与 `*.dist-info`，例如：
> ```bash
> SRC=llmlora/.venv/lib/python3.13/site-packages
> DST=.venv/lib/python3.13/site-packages
> rm -rf $DST/transformers $DST/transformers-*.dist-info
> cp -r $SRC/transformers $SRC/transformers-5.14.1.dist-info $DST/
> # einops / fla / triton 同理；含 C 扩展的包（如 torch、causal-conv1d）建议仍用 pip 安装
> ```

### 🚨 踩坑 4：transformers 4.x 不识别 `qwen3_5` 架构

- **现象**：`Qwen3Classifier` 初始化失败，日志 `qwen3_model_init_failed`；直接加载报 `KeyError: 'qwen3_5'`。
- **原因**：Qwen3.5 采用 linear-attention/mamba 混合架构，在 transformers 中注册为 `qwen3_next`，仅 5.x 版本支持。
- **解决**：升级 `transformers>=5.14.0`（同时会要求 `huggingface_hub>=1.x`）。

### 🚨 踩坑 5：缺失 `einops` 导致 `Qwen3_5ForCausalLM` 加载失败

- **现象**：transformers 5.x 已安装，但加载模型报 `ModuleNotFoundError: No module named 'einops'`，随后级联为 `Could not import module 'Qwen3_5ForCausalLM'`。
- **原因**：`Qwen3_5ForCausalLM` 模型代码硬导入 `einops`，但它未列入 transformers 的传递依赖。
- **解决**：`pip install einops>=0.8.0`。注意部分 venv（如 `llmlora/.venv`）可能从 conda 环境借用系统包，目标 `.venv` 无法借用时必须显式安装。

### 🚨 踩坑 6：triton 3.7.x 报 `0 active drivers` 无法发现 GPU

- **现象**：模型能加载，但 `generate()` 时抛 `RuntimeError: 0 active drivers ([]). There should only be one.`（来自 `triton/runtime/driver.py`，由 fla 的 `l2norm_fwd_kernel` 触发）；`_classify_inner` 捕获后返回 `None`，日志仅见 `llm_classify_inner_error`。
- **原因**：triton 3.7.1 在 WSL 环境下无法发现 GPU driver；同机 triton 3.6.0 正常。
- **解决**：`pip install triton==3.6.0`。
- **排查提示**：`_classify_inner` 的异常详情在 `logger.error(..., extra={"error": str(e)})` 中，pytest 默认不显示，需单独写脚本复现才能看到真实异常栈。

### 🚨 踩坑 7：缺失 fla / causal-conv1d 回退慢路径

- **现象**：启动日志出现 `[transformers] The fast path is not available because one of the required library is not installed. Falling back to torch implementation.`
- **原因**：Qwen3.5 混合架构的线性注意力/mamba 层优先使用 fla（triton kernel）与 causal-conv1d 的 CUDA fast-path。
- **影响与解决**：缺失 fla 时回退实现可能产生损坏/截断的 JSON 生成，**必须安装 `fla-core`**；缺失 causal-conv1d 仅降低速度、不影响正确性，按需安装。

### 6.3 推理 dtype 与 prompt 对齐要求

1. **必须使用 `bfloat16` 加载**：Qwen3.5 的 linear-attention/mamba 混合层在 FP16 下数值溢出，会导致 JSON 生成损坏/截断（`llm_engines.py` 中 `_resolve_cuda_dtype` 已默认优先 bf16）。
2. **微调模型 prompt 必须与训练侧严格一致**：训练侧（`llmlora/src/dataset/loader.py`）为短 SYSTEM_PROMPT + 裸文本输入；`Qwen3Classifier._classify_inner` 通过 `_is_finetuned_model()` 识别微调模型后自动采用训练同款模板（`_FINETUNED_SYSTEM_PROMPT` + `sanitize_for_prompt(text)` + `enable_thinking=False`）。切勿为微调模型附加额外前导语/包裹符，否则小模型会提前 EOS 导致 JSON 截断。

### 6.4 验证命令

```bash
# 环境自检
.venv/bin/python -c "import transformers, huggingface_hub, einops, fla, triton; \
print(transformers.__version__, huggingface_hub.__version__, einops.__version__, triton.__version__)"

# 端到端真实模型测试（需 .models/Qwen3.5-0.8B-Privacy-Classifier-Smoother 已就位）
PYTHONPATH=. .venv/bin/python -m pytest \
  'tests/dynclassification/test_real_models.py::TestRealLlmAdapter::test_real_llm_loads_and_returns_structured_result' -v -m ''
# 预期：1 passed（约 15-18s）；llmlora/.venv 同理可验证
```

---

## 7. 总结

通过上述 **`cu128` Nightly 依赖匹配 + 自动 C++ 共享库预加载 (CUPTI / CuFFT) + 运行级 CUDA 算力探针** 组合方案，成功搞定了最新的 **Blackwell (`sm_120`) 架构 RTX 50 系列 GPU** 上 PyTorch 硬件加速与 ModelScope Small-NER 的完美运行！