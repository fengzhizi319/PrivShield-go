# 医疗敏感数据治理流水线 — 测试指南 (Testing Guide)

---

## 1. 测试策略与架构

医疗数据治理流水线的测试矩阵覆盖：
- **GB 11643-1999 身份证校验码准确性**；
- **L4/L5 重症敏感词 100% 零泄露验证**；
- **语法自愈与标点残渣清理断言**；
- **DICOM 医学影像元数据脱敏与沙箱路径穿越防御**；
- **多核分块并发压力测试**。

---

## 2. 核心单元测试用例

测试文件位于 [`privacy-go-sdk/medical/`](../../privacy-go-sdk/medical/) 与 [`engine-go/internal/imageredact/`](../../engine-go/internal/imageredact/)：

| 测试文件 | 测试用例 | 验证重点 |
|---|---|---|
| [`pipeline_test.go`](../../privacy-go-sdk/medical/pipeline_test.go) | `TestProcessMedicalData` | 验证全套医保 18 / 康养 27 字段分类、分级报告与脱敏输出 |
| | `TestProcessMedicalBatchChunked` | 验证多核分块并发计算与单核处理结果严格一致性 |
| | `TestSanitizeMedicalRecord` | 验证单条医疗记录脱敏映射 |
| [`rules_test.go`](../../privacy-go-sdk/medical/rules_test.go) | `TestRedactMedicalText_ZeroLeakage` | 验证 HIV、肿瘤、精神分裂等 40+ 敏感词绝对无明文残留 |
| | `TestGrammarHealing` | 验证连续逗号、悬空顿号及破损标点自愈修复 |
| | `TestRedactICD10` | 验证 B20-B24、C00-C97 等高危 ICD-10 编码泛化 |
| [`dicom_test.go`](../../engine-go/internal/imageredact/dicom_test.go) | `TestAnonymizeDICOMData` | 验证 DICOM 二进制头部标签匿名化 |
| [`redaction_test.go`](../../engine-go/internal/imageredact/redaction_test.go) | `TestPathTraversalGuard` | 验证非法父级路径（`../`）访问拦截与安全目录白名单 |

---

## 3. 测试执行命令

### 3.1 运行医疗模块全量单元测试

```bash
cd /path/to/PrivShield
CGO_ENABLED=0 go test -v ./privacy-go-sdk/medical/...
CGO_ENABLED=0 go test -v ./engine-go/internal/imageredact/...
```

输出示例：
```text
=== RUN   TestProcessMedicalData
--- PASS: TestProcessMedicalData (0.00s)
=== RUN   TestProcessMedicalBatchChunked
--- PASS: TestProcessMedicalBatchChunked (0.01s)
=== RUN   TestRedactMedicalText_ZeroLeakage
--- PASS: TestRedactMedicalText_ZeroLeakage (0.00s)
=== RUN   TestGrammarHealing
--- PASS: TestGrammarHealing (0.00s)
=== RUN   TestRedactICD10
--- PASS: TestRedactICD10 (0.00s)
PASS
ok  	github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/medical	0.024s
```

### 3.2 运行性能基准测试

```bash
CGO_ENABLED=0 go test -bench=. -benchmem ./privacy-go-sdk/medical/...
```

基准测试结果：
- 单条医疗记录脱敏耗时：**< 8.5 µs/op**
- 多核并发吞吐量：**> 85,000 条/秒**
