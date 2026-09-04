# 医疗敏感数据治理流水线 — 使用示例 (Usage Examples)

---

## 1. Go 原生 SDK 调用示例

```go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/medical"
)

func main() {
	records := []map[string]any{
		{
			"name":               "张伟",
			"id_card_no":         "110101199003072381",
			"gender":             "男",
			"age":                34,
			"diagnosis_name":     "获得性免疫缺陷综合征(HIV)",
			"present_illness":    "患者因持续低热就医，初筛HIV抗体阳性，确诊为艾滋病",
			"registered_address": "北京市海淀区中关村南大街1号",
		},
	}

	// 1. 执行全流程医疗治理
	result, err := medical.ProcessMedicalData(records)
	if err != nil {
		panic(err)
	}

	// 2. 打印分级报告
	fmt.Printf("最高敏感级别: %s\n", result.ClassificationReport[0].MaxLevel)
	fmt.Printf("检测到的PII字段: %v\n", result.ClassificationReport[0].PIIFieldsDetected)

	// 3. 打印脱敏清洗后的数据
	cleanJSON, _ := json.MarshalIndent(result.SanitizedData, "", "  ")
	fmt.Printf("清洗后安全数据:\n%s\n", string(cleanJSON))
}
```

---

## 2. cURL REST API 调用示例

### 2.1 全流程处理 (`POST /v1/medical/process`)

```bash
curl -s -X POST http://127.0.0.1:8079/v1/medical/process \
  -H "Content-Type: application/json" \
  -d '{
    "records": [
      {
        "name": "李四",
        "id_card_no": "110101199003072381",
        "diagnosis_name": "恶性肿瘤(肺腺癌IV期)",
        "present_illness": "咳嗽伴胸痛，病理提示中分化肺腺癌"
      }
    ]
  }' | jq
```

响应示例：
```json
{
  "classification_report": [
    {
      "record_index": 0,
      "max_level": "L5",
      "pii_fields_detected": ["id_card_no", "name"],
      "high_sensitivity_detected": ["diagnosis_name:L4", "present_illness:L4"]
    }
  ],
  "sanitized_data": [
    {
      "name": "李*",
      "id_card_no": "110101********2381",
      "diagnosis_name": "[L4-MALIGNANT-NEOPLASM-MASKED]",
      "present_illness": "咳嗽伴胸痛，病理提示[L4-MALIGNANT-NEOPLASM-MASKED]"
    }
  ],
  "summary": {
    "total_records": 1,
    "guarantee_no_l4_l5_raw_data": true,
    "duration_ms": 0.85
  }
}
```

---

## 3. Python 客户端调用示例

```python
import httpx

client = httpx.Client(base_url="http://127.0.0.1:8079", timeout=5.0)

payload = {
    "records": [
        {
            "name": "王秀英",
            "id_card_no": "310101198505123456",
            "diagnosis_name": "重度抑郁发作伴精神病性症状",
            "present_illness": "情绪低落，伴幻听及自杀企图",
        }
    ]
}

resp = client.post("/v1/medical/process", json=payload)
data = resp.json()

print("最高等级:", data["classification_report"][0]["max_level"])
print("脱敏后诊断:", data["sanitized_data"][0]["diagnosis_name"])
print("脱敏后现病史:", data["sanitized_data"][0]["present_illness"])
```
