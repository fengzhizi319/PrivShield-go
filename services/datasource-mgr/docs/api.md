# 模拟数据源服务 (datasource-mgr) — API 规范

`datasource-mgr` 是 PrivShield 体系专用的模拟数据源服务。生产环境中下游服务可直接对接真实政务或医疗机构数据底座，本项目用于开发、测试、CI 自动化测试与合规演练。模块支持 **HTTP/HTTPS REST (:8083) + gRPC (mTLS :50053)** 双协议接入与国密 SM2 / TLS 1.3 双向身份核验。

---

## 1. 通信协议与端口规划

| 协议 | 默认地址 | 认证与加密方式 | 说明 |
|---|---|---|---|
| **HTTP REST (开发模式)** | `http://127.0.0.1:8083` | API Key / Bearer Token（免 TLS） | 供本地 React 前端与 BFF 快速开发调试 |
| **HTTPS REST (生产模式)** | `https://0.0.0.0:8083` | 国密 SM2 / TLS 1.3 双向 mTLS + CN 白名单 | 供生产加固环境下前端网关与微服务安全调用 |
| **gRPC (mTLS)** | `0.0.0.0:50053` | 国密 SM2 / TLS 1.3 双向 mTLS + CN 白名单 | 供调度中枢 (service-hub) 与微服务集群高速数据流转 |
| **Prometheus** | `http://127.0.0.1:8083/metrics` | 内网隔离 / 鉴权 | 收集数据源读取 QPS 与延迟监控指标 |

---

## 2. 模拟数据接口与字段规约 (对齐 DB51/T 2989—2023)

| 接口编号 | 对应数据源 | 规范 REST 路径 (Canonical) | 字段数 | gRPC 方法 | 说明 |
|---|---|---|---|---|---|
| **API 1** | 医保数据源 (`ds_yibao`) | `GET /api/datasources/ds_yibao/records` | 18 字段 | `GetYibaoData` | 模拟医保就医、诊断与结算流水 (`yibao.csv`) |
| **API 2** | 康养数据源 (`ds_kangyang`) | `GET /api/datasources/ds_kangyang/records` | 27 字段 | `GetKangyangData` | 模拟康养中心体检、慢病随访与健康档案 (`kangyang.csv`) |
| **API 3** | 预留接口 3 (`ds_mock3`) | `GET /api/datasources/ds_mock3/records` | 自定义 | `GetMockData3` | 预留政务数据源 3 模拟数据 |
| **API 4** | 预留接口 4 (`ds_mock4`) | `GET /api/datasources/ds_mock4/records` | 自定义 | `GetMockData4` | 预留企业/金融数据源 4 模拟数据 |

---

## 3. gRPC API 规范 (`datasourcemgr.proto`)

`package datasourcemgr;`

### 3.1 服务接口定义 (`DataSourceManagerService`)

```protobuf
service DataSourceManagerService {
  // Health 健康检查
  rpc Health(HealthRequest) returns (HealthResponse);

  // API 1: 获取医保就医与结算模拟数据 (18 字段)
  rpc GetYibaoData(DataQueryRequest) returns (DataQueryResponse);

  // API 2: 获取康养体检与慢病模拟数据 (27 字段)
  rpc GetKangyangData(DataQueryRequest) returns (DataQueryResponse);

  // API 3: 预留模拟数据源扩展接口 3
  rpc GetMockData3(DataQueryRequest) returns (DataQueryResponse);

  // API 4: 预留模拟数据源扩展接口 4
  rpc GetMockData4(DataQueryRequest) returns (DataQueryResponse);

  // 通用按数据源 ID 获取模拟数据
  rpc GetDataBySource(SourceDataQueryRequest) returns (DataQueryResponse);

  // 列出所有内置模拟数据源
  rpc ListMockSources(ListMockSourcesRequest) returns (ListMockSourcesResponse);

  // 获取单个模拟数据源基本信息
  rpc GetDataSource(GetDataSourceRequest) returns (DataSourceProto);

  // 模拟数据源连通性测试
  rpc TestConnection(TestConnectionRequest) returns (TestConnectionResponse);
}
```

### 3.2 核心 Proto 消息定义

```protobuf
message DataQueryRequest {
  int32 limit = 1;         // 返回条数（默认 20，最大 1000）
  int32 offset = 2;        // 偏移量（默认 0）
}

message SourceDataQueryRequest {
  string source_id = 1;    // "ds_yibao" | "ds_kangyang" | "ds_mock3" | "ds_mock4"
  int32 limit = 2;
  int32 offset = 3;
}

message DataRowProto {
  map<string, string> fields = 1;
}

message DataQueryResponse {
  string source_id = 1;
  string source_name = 2;
  int32 total = 3;
  int32 limit = 4;
  int32 offset = 5;
  repeated DataRowProto records = 6;
  string via = 7;
}
```

---

## 4. HTTP REST API 规范

### 4.1 健康与探活端点

#### `GET /health` & `GET /readyz`
- **响应**：`{"status": "ok", "service": "datasource-mgr", "timestamp": "2026-08-31T10:00:00Z"}`

---

### 4.2 获取模拟数据集

#### `GET /api/datasources/ds_yibao/records` (API 1: 医保数据, 18 字段)
- **参数**：`limit` (默认 20), `offset` (默认 0)
- **响应示例**：
```json
{
  "source_id": "ds_yibao",
  "source_name": "医保就医与结算模拟数据库 (yibao.csv)",
  "total": 50,
  "limit": 20,
  "offset": 0,
  "records": [
    {
      "insurance_settlement_id": "YB_SETTLE_20260801_0001",
      "person_id": "510101198503151234",
      "name": "李明",
      "gender": "男",
      "age": "41",
      "phone": "13800138000",
      "id_card": "510101198503151234",
      "visit_date": "2026-08-01",
      "hospital_name": "柳州市第一人民医院",
      "dept_name": "心血管内科",
      "icd10_code": "I10.x00",
      "diagnosis_name": "原发性高血压",
      "treatment_plan": "降压药物治疗",
      "medication_list": "氨氯地平片 5mg qd",
      "total_amount": "268.50",
      "reimbursement_amount": "187.95",
      "self_pay_amount": "80.55",
      "payment_status": "已结算"
    }
  ],
  "via": "datasource-mgr"
}
```

#### `GET /api/datasources/ds_kangyang/records` (API 2: 康养数据, 27 字段)
- **参数**：`limit` (默认 20), `offset` (默认 0)
- **响应示例**：包含患者姓名、身份证、主诉病史、残疾证号及 6 维体征生理指标。

---

### 4.3 模拟数据源元数据与连通性

#### `GET /api/datasources`
- **说明**：列出所有内置模拟数据源列表。

#### `GET /api/datasources/:id`
- **说明**：获取指定模拟数据源的基本信息。

#### `POST /api/datasources/:id/test`
- **说明**：模拟连通性测试。
- **响应**：`{"datasource_id": "ds_yibao", "success": true, "latency_ms": 1, "via": "datasource-mgr"}`
