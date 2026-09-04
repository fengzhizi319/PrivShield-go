# Personalized Profiles — 个性化隐私参数推荐与自动保存设计方案

为了实现给定数据自动推荐隐私参数并持久化保存以备后续使用的功能，我们设计了以下方案：

---


## 1. 核心流程

1. **调用参数分析接口**：用户通过 REST API 或 gRPC 传入特定的 `namespace` 及数据（一维数值列表 `values`，或者结构化记录列表 `rows`）。
2. **自动估计与推荐**：
   - **差分隐私 (DP)**：
     - `clip_lower` / `clip_upper`：基于输入数据 `values` 的分位数（例如 5% 和 95% 分位数）进行自适应区间估计，以过滤异常值并确定敏感度。若两者相等，则自动向外扩展一个单位（`-1.0` / `+1.0`）。
     - `epsilon`：默认推荐 `1.0`。
     - `delta`：根据数据量 $n$，推荐 $1 / (10 \times n^2)$，并与保守的上限值 `1e-5` 取最小值。
   - **K-Anonymity**：
     - `k`：根据表行数 $n$，推荐 $\max(2, \min(10, n \div 10))$，即数据量大时可适当增大 $k$ 以增加隐私保护强度，数据量小时减小以降低信息损失。
     - `max_depth`：默认推荐 `10`。
3. **参数持久化存储**：
   - 系统将推荐生成的参数自动保存到项目目录下的 `config/personalized-profiles.yaml` 文件中。
   - 写入时引入线程锁（Thread Lock），读取已有的 YAML 内容，更新对应 `namespace` 下的原语参数，再写回文件。
4. **后续自动加载与应用**：
   - 由 [ParameterResolver](../../engine-go/internal/profile/resolver.go) 在合并参数时，增加对 `config/personalized-profiles.yaml` 的读取。
   - 参数合并优先级（从低到高）：
     1. 系统内置默认值（`default_params`）
     2. 静态全局 profile 配置文件（`privacy-profile.yaml`）
     3. **个性化推荐保存的参数（`config/personalized-profiles.yaml` 中对应 `namespace` 的配置）**
     4. 单次请求携带的 overrides 参数。

---

## 2. 接口设计

### 2.1 REST API

**请求**：
```http
POST /v1/privacy/profile/recommend
Content-Type: application/json

{
  "namespace": "user-salary-dataset",
  "values": [5000, 6000, 7000, 8000, 100000],
  "rows": [
    {"age": 25, "salary": 5000},
    {"age": 26, "salary": 6000},
    {"age": 35, "salary": 7000},
    {"age": 36, "salary": 8000}
  ],
  "qi_cols": ["age"]
}
```

**响应**：
```json
{
  "status": "success",
  "namespace": "user-salary-dataset",
  "recommended_params": {
    "dp": {
      "epsilon": 1.0,
      "delta": 1e-05,
      "mechanism": "laplace",
      "clip_lower": 5200.0,
      "clip_upper": 82000.0
    },
    "k_anonymity": {
      "k": 2,
      "max_depth": 10
    }
  }
}
```

### 2.2 gRPC API
在 `proto/privacy.proto` 中扩展以下定义并重新生成 stub：
```protobuf
message RecommendRequest {
  string namespace = 1;
  repeated double values = 2;
  repeated RecordEntry rows = 3;
  repeated string qi_cols = 4;
}

message RecommendResponse {
  string status = 1;
  string namespace = 2;
  string recommended_params_json = 3;
}
```
并在 `PrivacyService` 增加对应的 `RecommendParams` RPC 接口。