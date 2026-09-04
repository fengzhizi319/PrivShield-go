# 分类分级与隐私脱敏核心引擎 (privacy-engine) — 接口契约规范

> **组件归属**：`services/privacy-engine`  
> **服务协议**：REST (Gin) `:8079` / gRPC `:50051`；网关反向代理 HTTP `:8000` / gRPC `:50000`

---

## 1. REST 接口清单

| 方法 | 路径 | 功能说明 | 核心参数/报文 |
|---|---|---|---|
| `POST` | `/v1/classification/classify` | 单条/批量敏感特征分类定级 | `{"records": [{"name": "张三", "id_card": "450201..."}]}` |
| `POST` | `/v1/privacy/mask` | 通用字段智能掩码脱敏 | `{"field_name": "id_card", "value": "450201199001011234"}` |
| `POST` | `/v1/privacy/mask/batch` | 批量记录掩码脱敏 | `{"records": [...]}` |
| `POST` | `/v1/privacy/dp/count` | 差分隐私计数加噪 (Laplace/Gaussian) | `{"count": 1000, "epsilon": 1.0, "delta": 0.0}` |
| `POST` | `/v1/privacy/dp/mean` | 差分隐私均值加噪 | `{"values": [12.5, 34.2, ...], "epsilon": 1.0}` |
| `POST` | `/v1/privacy/kano/anonymize` | Mondrian K-匿名表格泛化处理 | `{"data": [...], "quasi_identifiers": ["age", "gender"], "k": 5}` |
| `POST` | `/v1/privacy/ldp/randomized_response` | 本地差分隐私二值扰动 | `{"bit": 1, "epsilon": 0.5}` |
| `POST` | `/v1/privacy/qol/obfuscate` | 查询混淆诱饵注入 | `{"query": "高血压治疗", "num_dummies": 3}` |
| `POST` | `/v1/privacy/medical/redact` | 医疗 DICOM 二进制/结构化脱敏 | `multipart/form-data` |
| `GET`  | `/healthz` | 存活探针 (Liveness Probe) | `200 OK` |
| `GET`  | `/readyz` | 就绪探针 (Readiness Probe) | `200 OK` |
| `GET`  | `/ops/diagnostics` | 运行时三层漏斗规则与 NER 诊断信息 | 返回加载的标准、规则集版本与 NER 状态 |
| `GET`  | `/metrics` | Prometheus 格式性能与调用指标 | 文本时序指标 (QPS/时延/预算消耗) |

---

## 2. gRPC 接口契约 (`proto/privacy.proto`)

`privacy-engine` 实现了由根目录 `proto/privacy.proto` 编译生成的 `PrivacyServiceServer`：
- `ClassifyData(ClassifyRequest) returns (ClassifyResponse)`
- `MaskData(MaskRequest) returns (MaskResponse)`
- `ApplyDifferentialPrivacy(DPRequest) returns (DPResponse)`
- `AnonymizeKano(KanoRequest) returns (KanoResponse)`
- `ObfuscateQuery(QueryRequest) returns (QueryResponse)`

---

## 3. 标准错误响应信封

当发生非 2xx 异常时，接口一律通过 `pkg/middleware.AbortWithError` 输出统一 5 字段信封：

```json
{
  "code": "INVALID_PARAMS",
  "message": "field_name is required",
  "error_class": "client_error",
  "trace_id": "c1f7a048-2b8e-4a9f-8f1d-7201b17b6a12",
  "retryable": false
}
```
