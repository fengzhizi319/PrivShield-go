# 医疗健康敏感数据治理流水线 (Medical Privacy Pipeline) 文档索引

本目录包含 `PrivShield` 医疗健康敏感数据分类分级、医保 18 / 康养 27 字段特化治理、L4/L5 级重症诊断强脱敏与 DICOM 医学影像隐私清洗模块的全套 SDLC 工程规范与技术文档。

---

## 📚 文档清单

| 文档 | 说明 | 目标读者 |
|---|---|---|
| [prd.md](./prd.md) | 医疗敏感数据治理流水线产品需求文档（PRD）与 27 核心字段规范 | 产品经理、数据合规官、架构师 |
| [design.md](./design.md) | 3-Layer 分类分级、L4/L5 重症强剥离、语法自愈与双结构输出架构设计 | 架构师、Go 核心开发 |
| [medical_data_pipeline_design.md](./medical_data_pipeline_design.md) | 医保 18 与康养 27 领域流水线多核分块与规则编排深度设计 | 算法工程师、后端开发 |
| [performance_optimization_guide.md](./performance_optimization_guide.md) | 多核并发分块、ASCII 快速路径与无锁词典性能优化指南 | 性能测试工程师、后端架构师 |
| [api_reference.md](./api_reference.md) | Go SDK / REST API (`/v1/medical/*`) / gRPC / BFF 接口参考 | 接入开发者、前端开发 |
| [examples.md](./examples.md) | Go 原生代码、cURL / HTTP REST 与 Python 客户端端到端调用示例 | 接入开发者 |
| [testing.md](./testing.md) | 校验码测试、L4/L5 零泄露测试与自动化测试指南 | QA、测试工程师 |
| [ops.md](./ops.md) | 数据生成、DICOM 影像脱敏、运维配置与排障指南 | SRE、运维工程师、DevOps |
| [医疗健康数据分类分级与隐私脱敏算法标准规范.md](./医疗健康数据分类分级与隐私脱敏算法标准规范.md) | 符合国家医疗健康标准与 JR/T 0197-2020 的权威分类脱敏规范 | 法务合规、安全审计员 |

---

## 🏥 核心能力概览

`medical_pipeline` 位于 [`privacy-go-sdk/medical`](../../privacy-go-sdk/medical/) 与 [`engine-go/internal/imageredact`](../../engine-go/internal/imageredact/)，提供医疗健康全生命周期数据合规治理能力：

1. **医保 18 与康养 27 字段特化支持**: 内置 `YibaoFields` 与 `KangyangFields` 字段级自动识别与映射（规范字段名、别名识别与自动泛化）。
2. **L4/L5 级重症诊断强剥离与标准化替换**: 对恶性肿瘤、HIV、重度精神障碍、遗传缺陷等特高敏感临床描述自动转换为合规范畴标签（如 `[L5-IMMUNODEFICIENCY-SENSITIVE-MASKED]`），**100% 确保清洗数据不含原始高危词汇**。
3. **语法自愈与断句残渣清理 (`RedactMedicalText`)**: 智能清理脱敏后残留的孤立标点（如连续逗号、悬空顿号），保障下游 NLP 与结构化分析可用性。
4. **DICOM 医学影像二进制脱敏 (`imageredact`)**: 原生解析 DICOM 头部元数据，剥离患者姓名、身份证、就诊号、检查日期与机构信息，具备路径穿越安全防护（Path Traversal Guard）。
5. **多核无锁分块并发计算 (`Chunked Concurrency`)**: 基于 `runtime.NumCPU()` 自动分块，处理十万级医疗记录吞吐突破 **80,000+ 条/秒**。
6. **双结构合规输出模型**: 同步返回 (1) 字段与记录级分类分级报告 (`classification_report`) 和 (2) 安全脱敏清洗数据集 (`sanitized_data`)。

---

## 🚀 快速开始

### 1. 运行 Go 单元测试与基准测试

```bash
cd /path/to/PrivShield

# 运行医疗流水线单元测试
CGO_ENABLED=0 go test -v ./privacy-go-sdk/medical/...
CGO_ENABLED=0 go test -v ./engine-go/internal/imageredact/...

# 运行性能压测
CGO_ENABLED=0 go test -bench=. ./privacy-go-sdk/medical/...
```

### 2. 生成高仿真医疗测试数据

```bash
# 生成 100 条包含 GB 11643 身份证与 L4/L5 真实病历的康养数据
python scripts/data/generate_medical_data.py --output data/kangyang.csv --count 100
```

### 3. 通过 REST API 调用医疗治理流水线

```bash
# 启动 Go Agent
go run ./engine-go/cmd/privshield-agent

# 发起医疗数据处理请求
curl -X POST http://127.0.0.1:8079/v1/medical/process \
  -H "Content-Type: application/json" \
  -d '{
    "records": [
      {
        "name": "张伟",
        "id_card_no": "110101199003072381",
        "diagnosis_name": "获得性免疫缺陷综合征(HIV)",
        "present_illness": "患者因反复发热就诊，检出HIV抗体阳性"
      }
    ]
  }' | jq
```
