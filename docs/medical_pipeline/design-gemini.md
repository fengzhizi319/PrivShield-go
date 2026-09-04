# 医疗敏感数据分类分级与脱敏 Pipeline 设计方案 (Medical Privacy Pipeline Design)

> **文档路径**: `docs/medical_pipeline/design.md`  
> **面向对象**: 架构师、后端/Agent 开发者、算法工程师、前端开发者  
> **功能目标**: 提供真实医疗场景数据生成、3-Layer 分类分级 (L1-L5)、L4/L5 高敏感数据脱敏剥离与双输出保障，并实现 Agent、Python/Go 控制台后端以及 Web 前端的全链路接入。

---

## 1. 概述与需求背景 (Overview & Requirements)

在医疗健康数据开放与合规共享场景中，电子病历 (EMR)、残疾人评估记录及医保结算数据包含高度敏感的个人身份标识信息 (PII，如身份证号、医保证号) 以及极高风险的医疗病史信息（如 L4 级的恶性肿瘤/传染病病史、L5 级的重度精神障碍/遗传缺陷/HIV 感染等）。

本设计方案构建了一个完整的**医疗数据合规治理 Pipeline**：
1. **多核并发切片流水线 (`privacy-go-sdk/medical/pipeline.go`)**：
   - 接入 3-Layer Funnel 规则引擎，完成 27 个字段及文本内容的 L1~L5 分级标注。
   - 接入 `privacy-go-sdk/masking` 脱敏原语，对 PII 及 L4/L5 级高敏感诊断与病历执行强脱敏与范畴化替换，强制保障输出数据中**绝对不包含任何 L4/L5 级原始敏感内容**。
   - **双重结果输出**：输出 (1) 分级报告数据 (`classification_report`) 和 (2) 脱敏后符合安全合规要求的清洗数据 (`sanitized_data`)。
2. **代理后端与前端全链路集成**：
   - 在 Go BFF (`console/bff-go`) 实现对应的代理与 gRPC/REST 通信。
   - 在 Web 前端控制台提供“医疗数据治理 (Medical Pipeline)”功能面板。

---

## 2. 整体架构设计 (System Architecture)

```mermaid
flowchart TD
    subgraph DataGen [数据样本]
        D1[data/kangyang.csv / data/yibao.csv]
    end

    subgraph AgentPipeline [privacy-go-sdk/medical & engine-go]
        D1 --> MP[MedicalPrivacyPipeline]
        MP -->|调用 dynclassification| DC[3-Layer 分类分级引擎]
        DC -->|标注 L1~L5 等级与 Tag| CR[1. 分级结果数据 (Classification Report)]
        MP -->|调用 privacy-go-sdk/masking| MS[脱敏与 L4/L5 抹平引擎]
        MS -->|PII 掩码 + L4/L5 泛化/抹平| SD[2. 脱敏清洗数据 (Sanitized Data)]
    end

    subgraph Endpoints [通信层与后端通道]
        MP --> Service[PrivacyService / MedicalRoute]
        Service --> GoBackend[Go Console Backend :8081 /v1/privacy]
        GoBackend --> WebUI[Web 控制台 :5173]
    end
```
    end

    subgraph Frontend [Web 前端控制台]
        PyBackend --> WebUI[MedicalPipelinePanel.tsx]
        GoBackend --> WebUI
    end
```

---

## 3. 详细模块设计 (Detailed Component Design)

### 3.1 字段规范与 L1~L5 敏感分级定义

数据包含 27 个标准医疗与身份字段，其敏感分级定义遵循中国《数据安全法》、《医疗健康数据安全指南》与相关标准：

| 序号 | 中文字段 | 英文 Key (`kangyang.csv`) | 敏感等级 | 治理策略 |
|---|---|---|---|---|
| 1 | 姓名 | `name` | **L3 (高)** | 姓名掩码（如 `张*`） |
| 2 | 身份证号 | `id_card_no` | **L4 (极高)** | 身份证号保频掩码（如 `110101********1237`） |
| 3 | 户口地址 | `registered_address` | **L3 (高)** | 地址泛化到地级市 |
| 4 | 残疾证号 | `disability_cert_no` | **L3 (高)** | 证号遮蔽掩码 |
| 5 | 医保证号 | `medical_insurance_no` | **L3 (高)** | 证号遮蔽掩码 |
| 6 | 性别 | `gender` | **L1 (低)** | 保持原样 |
| 7 | 年龄 | `age` | **L1 (低)** | 保持原样 / 范围泛化 |
| 8 | 诊断名称 | `diagnosis_name` | **L4~L5 (特高)** | **L4/L5 剥离**（替换为合规范畴词，如 `[L4-普通慢性病]`） |
| 9 | 主诉 | `chief_complaint` | **L3 (高)** | 敏感文本 NER 抽取与遮蔽 |
| 10 | 现病史 | `present_illness` | **L4~L5 (特高)** | **L4/L5 剥离与 NER 掩码** |
| 11 | 既往史 | `past_history` | **L4~L5 (特高)** | **L4/L5 剥离与 NER 掩码** |
| 12 | 个人史 | `personal_history` | **L2 (中)** | 脱敏处理 |
| 13 | 是否吸烟 | `is_smoking` | **L1 (低)** | 保持原样 |
| 14 | 吸烟时长 | `smoking_duration` | **L1 (低)** | 保持原样 |
| 15 | 家族史 | `family_history` | **L3 (高)** | 遗传病敏感文本替换 |
| 16 | 过敏史 | `allergic_history` | **L2 (中)** | 保持原样/掩码 |
| 17 | 科室 | `department` | **L1 (低)** | 保持原样 |
| 18 | 身高 | `height` | **L1 (低)** | 保持原样 |
| 19 | 体重 | `weight` | **L1 (低)** | 保持原样 |
| 20 | 残疾类别 | `disability_category` | **L2 (中)** | 保持原样 |
| 21 | 残疾等级 | `disability_level` | **L2 (中)** | 保持原样 |
| 22 | 评估类型 | `assess_type_name` | **L1 (低)** | 保持原样 |
| 23 | 评估结果 | `assess_result_name` | **L2 (中)** | 保持原样 |
| 24 | 评估分数 | `assess_score` | **L1 (低)** | 保持原样 |
| 25 | 评估时间 | `assess_time` | **L1 (低)** | 格式归一化 |
| 26 | 病程记录 | `progress_note` | **L4~L5 (特高)** | **含图片病例引用，抹平 L4/L5 描述，保护图片 Hash/路径** |
| 27 | 病程记录时间 | `progress_note_time` | **L1 (低)** | 格式归一化 |

---

### 3.2 仿真数据生成器 (`scripts/data/generate_medical_data.py`)

1. **GB 11643-1999 校验码算法**：计算 ISO 7064:1983.MOD 11-2 前 17 位加权余数，生成符合校验规则的真实格式身份证号。
2. **L4/L5 级病史数据嵌入**：
   - **L4 级场景**：恶性肿瘤（肺腺癌、胃癌）、乙型肝炎、严重冠心病。
   - **L5 级场景**：HIV/艾滋病病毒感染、重度精神分裂症、遗传性亨廷顿舞蹈病。
3. **图文混合病程记录**：病程记录中嵌入类似 `[病理切片图: /medical_images/pathology_01.png]` 或 `[DICOM-CT: /radiology/ct_scan_05.dcm]` 的真实图文混合引用。

---

### 3.3 核心算法 Pipeline (`privacy-go-sdk/medical/`)

Go 模块结构定义：
```text
privacy-go-sdk/medical/
├── pipeline.go          # 医疗数据治理 Pipeline 主逻辑与多核并发分块
├── pipeline_test.go     # 单元测试与基准测试
└── rules.go             # 医疗专属分级规则与 L4/L5 关键词映射
```

#### Pipeline 处理逻辑流程：
```go
// ProcessRecord 处理单条医疗记录，返回 (classificationReport, sanitizedRecord, error)
func (p *MedicalPrivacyPipeline) ProcessRecord(record map[string]any) (map[string]any, map[string]any, error) {
    // Step 1: 分类分级
    classification := p.ClassifyRecord(record)
    
    // Step 2: L4/L5 级别扫描与全量掩码/剥离
    sanitized := p.SanitizeRecord(record, classification)
    
    return classification, sanitized, nil
}
```

---

### 3.4 代理后端与 Frontend 跑通路线

1. **Agent 接口层**：
   - REST 路由: `POST /v1/medical/process`
   - gRPC 接口: `ProcessMedical`
2. **Go 控制台后端**：
   - Go BFF: 在 `console/bff-go/internal/handlers/handlers.go` 暴露 `POST /v1/medical_pipeline`。
   - 数据源直接加载 `data/kangyang.csv` 与 `data/yibao.csv`。
3. **Web 控制台 (`console/web`)**：
   - 视图组件: `MedicalPipelinePanel.tsx`。
   - 在左侧侧边栏提供“医疗数据治理 (Medical Pipeline)”入口。
   - 支持一键载入 `kangyang.csv` 数据、一键运行 Pipeline、联动分栏展示“1. 字段与记录级分级报告”和“2. 脱敏清洗数据（已彻底消除 L4/L5 高危病史与 PII）”。

---

## 4. 单元测试设计 (Testing Plan)

在 `privacy-go-sdk/medical/pipeline_test.go` 中编写自动化测试，验证：
1. `kangyang.csv` 的字段完整性 (27 列) 与身份证号算法有效性。
2. 包含 L4/L5 级诊断与病史的数据记录经 Pipeline 处理后，`sanitized_data` 中绝对不包含原始 L4/L5 敏感字符串。
3. PII 字段（姓名、身份证、医保证、残疾证）脱敏后符合掩码规范。
4. 双重输出（分级报告与脱敏数据）格式与结构完全符合 JSON 契约。
