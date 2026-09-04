# 数盾 PrivShield 自动化运维与测试脚本全集 (scripts/)

> 本目录集中管理 **数联天下 · 数盾 (`PrivShield`)** 的全生命周期自动化脚本，涵盖开发调试、生产部署、微服务编排、数据生成、模型管理、硬件加速与压力测试。

---

## 目录结构与快速索引

```text
scripts/
├── data/                 # 测试数据集生成、多模态单据渲染与规则扩充
│   └── README.md         # 详细文档: 数据生成与规则扩充
├── dev/                  # 本地单机/Docker 开发调试、微服务联调与自动化测试
│   └── README.md         # 详细文档: 本地开发与测试运维
├── env/                  # GPU 驱动、CUDA 12.8、PyTorch 及 TensorRT 引擎编译
│   └── README.md         # 详细文档: 硬件加速与环境构建
├── models/               # 本地大模型下载、Apple MLX 转换与 vLLM 推理服务
│   └── README.md         # 详细文档: AI 模型管理与推理服务
├── prod/                 # 生产级 Docker Compose、Helm/K8s 部署、备份与健康巡检
│   └── README.md         # 详细文档: 生产部署与运维
├── test/                 # 纯 Go 与异步高并发极限压测与 SLA 延迟评估套件
│   └── README.md         # 详细文档: 性能压测与质量评估
└── replace_docs_text.py  # 全局文档批量替换与迁移重构工具
```

---

## 各子目录功能一览

| 子目录 | 核心定位与场景 | 常用代表脚本 | 详细文档导航 |
|---|---|---|---|
| [`scripts/dev/`](./dev/README.md) | **本地开发与测试**：一键启动 Go Agent + BFF + 前端 HMR、中台微服务联动、E2E 自动化测试 | `dev-engine-console.sh`<br/>`run_console_e2e_tests.sh`<br/>`dev-stop.sh` | [查看 Dev 文档](./dev/README.md) |
| [`scripts/prod/`](./prod/README.md) | **生产发布与运维**：Docker Compose 生产编排、Helm/K8s 发布、全量 SQLite 备份与生产巡检 | `deploy-docker-compose.sh`<br/>`deploy-helm.sh`<br/>`backup-sqlite-databases.sh` | [查看 Prod 文档](./prod/README.md) |
| [`scripts/data/`](./data/README.md) | **数据与规则生成**：仿真医疗/医保/康养数据生成、多模态病历图片生成、规则与词表扩充 | `generate_medical_data.py`<br/>`generate_yibao_data.py`<br/>`expand_keywords_with_llm.py` | [查看 Data 文档](./data/README.md) |
| [`scripts/env/`](./env/README.md) | **硬件加速与环境构建**：NVIDIA Blackwell (sm_120) CUDA 12.8 安装、TensorRT 引擎编译 | `install_cuda_pytorch_sm120.sh`<br/>`export_tensorrt_engine.sh` | [查看 Env 文档](./env/README.md) |
| [`scripts/models/`](./models/README.md) | **模型管理与推理**：多模态与 NER 模型下载、macOS MLX 权重转换、vLLM 推理服务启动 | `download_model.py`<br/>`download_ner_model.py`<br/>`start_vllm_server.sh` | [查看 Models 文档](./models/README.md) |
| [`scripts/test/`](./test/README.md) | **性能压测与评估**：纯 Go 原生与异步高并发极限吞吐压测、P50/P90/P99 延迟 SLA 评估 | `stress.go`<br/>`stress_test_suite.py` | [查看 Test 文档](./test/README.md) |

---

## 根目录工具脚本

### `replace_docs_text.py`
- **作用说明**: 跨平台（支持 Linux 与 Windows/WSL UNC 路径）批量查找并替换文档和代码中的关键词，支持演练模式（`--dry-run`）与自动备份（`--backup`）。
- **参数选项**:
  - `-p, --path <PATH>`: 指定要遍历处理的根路径（默认当前目录）。
  - `-f, --from-text <TEXT>`: 被替换的源文本。
  - `-t, --to-text <TEXT>`: 替换后的目标文本。
  - `--dry-run`: 演练模式（仅打印拟修改文件与行数，不实际写入磁盘）。
  - `--backup`: 写入修改前自动创建 `.bak` 备份文件。
- **执行命令**:
  ```bash
  # 1. 演练预览修改 (Dry Run，安全无害)
  python scripts/replace_docs_text.py --dry-run
  ```
  ```bash
  # 2. 正式执行默认替换
  python scripts/replace_docs_text.py
  ```
  ```bash
  # 3. 指定路径与目标词并自动创建 .bak 备份
  python scripts/replace_docs_text.py -p docs -f "OldKeyword" -t "NewKeyword" --backup
  ```
