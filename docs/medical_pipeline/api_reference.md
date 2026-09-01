# 医疗敏感数据全流程治理流水线 — API 参考指南 (API Reference)

> 本文档详细说明 `PrivShield` Go 云原生架构下医疗流水线 Package API、REST 端点、请求/响应结构体模型及控制台 BFF 代理。

---

## 1. Go SDK Package API

包路径：`github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/medical`

### 1.1 核心数据结构

```go
// 医疗流水线双结构输出结果
type MedicalDataPipelineResult struct {
    ClassificationReport []RecordClassificationReport `json:"classification_report"` // 分类分级元数据报告
    SanitizedData        []map[string]any             `json:"sanitized_data"`         // 安全脱敏清洗数据集
    Summary              MedicalPipelineSummary       `json:"summary"`                // 汇总统计指标
}

// 汇总统计指标
type MedicalPipelineSummary struct {
    TotalRecords               int     `json:"total_records"`
    L5RecordsCount             int     `json:"l5_records_count"`
    L4RecordsCount             int     `json:"l4_records_count"`
    L3RecordsCount             int     `json:"l3_records_count"`
    L1L2RecordsCount           int     `json:"l1_l2_records_count"`
    SanitizedPIIFieldsTotal    int     `json:"sanitized_pii_fields_total"`
    SanitizedPIIFieldsPerRecord float64 `json:"sanitized_pii_fields_per_record"`
    RedactionFailures          int     `json:"redaction_failures"`
    FailSafeTriggeredFields    int     `json:"fail_safe_triggered_fields"`
    GuaranteeNoL4L5RawData     bool    `json:"guarantee_no_l4_l5_raw_data"`
    DurationMs                 float64 `json:"duration_ms"`
}
```

### 1.2 核心处理方法

| 方法签名 | 说明 |
|---|---|
| `ProcessMedicalData(records []map[string]any) (*MedicalDataPipelineResult, error)` | 执行全套 3-Layer 分类分级、L4/L5 重症强脱敏与 PII 掩码，返回双结构报告 |
| `ProcessMedicalBatchChunked(records []map[string]any, chunkSize int) (*MedicalDataPipelineResult, error)` | 多核无锁分块并发处理大规模医疗数据集 |
| `SanitizeMedicalRecord(record map[string]any, domain string) (map[string]any, error)` | 单条医疗记录特化脱敏清洗 |
| `SanitizeMedicalBatch(records []map[string]any, domain string) ([]map[string]any, error)` | 批量医疗记录特化脱敏清洗 |
| `RedactMedicalText(text string) string` | 临床自由文本 L4/L5 高敏词汇强剥离与语法自愈 |
| `RedactICD10(code string) string` | ICD-10 高危疾病诊断编码分级脱敏 |
| `CanonicalizePIIField(fieldName string) string` | PII 字段中文/英文别名标准化规范映射 |

---

## 2. DICOM 医学影像清洗 API

包路径：`github.com/fengzhizi319/PrivShield-go/engine-go/internal/imageredact`

```go
// 清洗 DICOM 文件元数据并输出至目标路径
func SanitizeDICOMFile(srcPath, dstPath string, options DICOMSanitizeOptions) (*DICOMSanitizeResult, error)

// 内存中二进制数据脱敏
func AnonymizeDICOMData(data []byte) ([]byte, error)
```

---

## 3. Agent REST API 端点

### 3.1 医疗全流程处理与双结构报告输出

- **端点**：`POST /v1/medical/process`
- **别名**：`POST /medical/process`、`POST /v1/agent/process`
- **请求头**：`Content-Type: application/json`

#### 请求体示例
```json
{
  "records": [
    {
      "name": "张伟",
      "id_card_no": "110101199003072381",
      "gender": "男",
      "age": 34,
      "diagnosis_name": "获得性免疫缺陷综合征(HIV)",
      "present_illness": "患者因反复发热就诊，检出HIV抗体阳性",
      "registered_address": "北京市东城区天安门广场1号"
    }
  ]
}
```

#### 响应体示例 (200 OK)
```json
{
  "classification_report": [
    {
      "record_index": 0,
      "max_level": "L5",
      "pii_fields_detected": ["id_card_no", "name", "registered_address"],
      "high_sensitivity_detected": ["diagnosis_name:L5", "present_illness:L5"],
      "field_details": [
        {
          "field_name": "id_card_no",
          "level": "L5",
          "security_tag": "PII_ID_CARD",
          "description": "公民身份证号码",
          "rule_matched": "RULE_PII_IDCARD"
        },
        {
          "field_name": "diagnosis_name",
          "level": "L5",
          "security_tag": "CRITICAL_DIAGNOSIS",
          "description": "临床诊断名称",
          "rule_matched": "RULE_L5_HIV"
        }
      ]
    }
  ],
  "sanitized_data": [
    {
      "name": "张*",
      "id_card_no": "110101********2381",
      "gender": "男",
      "age": 34,
      "diagnosis_name": "[L5-IMMUNODEFICIENCY-SENSITIVE-MASKED]",
      "present_illness": "患者因反复发热就诊，检出[L5-IMMUNODEFICIENCY-SENSITIVE-MASKED]",
      "registered_address": "北京市东城区***"
    }
  ],
  "summary": {
    "total_records": 1,
    "l5_records_count": 1,
    "l4_records_count": 0,
    "l3_records_count": 0,
    "l1_l2_records_count": 0,
    "sanitized_pii_fields_total": 3,
    "sanitized_pii_fields_per_record": 3.0,
    "redaction_failures": 0,
    "fail_safe_triggered_fields": 0,
    "guarantee_no_l4_l5_raw_data": true,
    "duration_ms": 1.25
  }
}
```

---

### 3.2 单条医疗记录脱敏

- **端点**：`POST /v1/medical/sanitize`
- **请求体**：
  ```json
  {
    "record": {
      "name": "李四",
      "phone": "13800138000",
      "diagnosis": "原发性肝癌"
    },
    "domain": "medical"
  }
  ```
- **响应体**：
  ```json
  {
    "sanitized_record": {
      "name": "李*",
      "phone": "138****8000",
      "diagnosis": "[L4-MALIGNANT-NEOPLASM-MASKED]"
    }
  }
  ```

---

### 3.3 批量医疗记录脱敏

- **端点**：`POST /v1/medical/sanitize/batch`
- **请求体**：
  ```json
  {
    "records": [
      {"name": "王五", "id_card_no": "110101199003072381"}
    ],
    "domain": "medical"
  }
  ```

---

## 4. 控制台 BFF 代理 API (`console/bff-go`)

- **`POST /api/medical_pipeline`**: 医疗流水线代理端点。当客户端未传入 `records` 时，BFF 自动加载内置标准样本 `console/bff-go/internal/samples/kangyang.csv` 并转发至 Go Agent。
