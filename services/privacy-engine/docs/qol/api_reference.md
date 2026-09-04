# 查询混淆模块 API 参考


## 1. Python SDK

### `obfuscate_query`

位置：`engine.privacy.qol.obfuscate_query`

```python
def obfuscate_query(
    query: str,
    num_dummies: int = 3,
    domain: str = "medical",
    medical_pool: List[str] = None,
    generic_pool: List[str] = None,
    seed: Optional[int] = None,
) -> List[str]
```

对单个查询进行混淆。

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `query` | `str` | 是 | 真实查询 |
| `num_dummies` | `int` | 否 | 虚假查询数量，默认 3 |
| `domain` | `str` | 否 | `"medical"` 或 `"generic"` |
| `medical_pool` | `List[str]` | 否 | 自定义医疗 dummy 池 |
| `generic_pool` | `List[str]` | 否 | 自定义通用 dummy 池 |
| `seed` | `Optional[int]` | 否 | 随机种子 |

### `obfuscate_query_batch`

位置：`engine.privacy.qol.obfuscate_query_batch`

```python
def obfuscate_query_batch(
    queries: List[str],
    num_dummies: int = 3,
    domain: str = "medical",
    medical_pool: List[str] = None,
    generic_pool: List[str] = None,
    seed: Optional[int] = None,
) -> List[List[str]]
```

批量查询混淆。

---

## 2. REST API

### POST `/v1/privacy/qol/obfuscate`

```json
{
  "query": "糖尿病患者用药趋势",
  "num_dummies": 3,
  "domain": "medical"
}
```

响应：

```json
{
  "result": ["虚假查询1", "糖尿病患者用药趋势", "虚假查询2", "虚假查询3"]
}
```

### POST `/v1/privacy/qol/obfuscate/batch`

```json
{
  "queries": ["查询1", "查询2"],
  "num_dummies": 2,
  "domain": "generic"
}
```

响应：

```json
{
  "results": [
    ["查询1", "dummy1", "dummy2"],
    ["dummy3", "查询2", "dummy4"]
  ]
}
```

---

## 3. gRPC API

| 方法 | 请求 | 响应 | 说明 |
|---|---|---|---|
| `ObfuscateQuery` | `ObfuscateQueryRequest` | `ObfuscateQueryResponse` | 单条查询混淆 |
| `ObfuscateQueryBatch` | `ObfuscateQueryBatchRequest` | `ObfuscateQueryBatchResponse` | 批量查询混淆 |

### `ObfuscateQueryRequest` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `query` | `string` | 真实查询 |
| `num_dummies` | `int32` | 虚假查询数量 |
| `domain` | `string` | 领域 |
| `medical_pool` | `repeated string` | 自定义医疗池 |
| `generic_pool` | `repeated string` | 自定义通用池 |
| `seed` | `int32` | 随机种子 |

### `ObfuscateQueryBatchRequest` 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `queries` | `repeated string` | 真实查询列表 |
| `num_dummies` | `int32` | 虚假查询数量 |
| `domain` | `string` | 领域 |
| `medical_pool` | `repeated string` | 自定义医疗池 |
| `generic_pool` | `repeated string` | 自定义通用池 |
| `seed` | `int32` | 随机种子 |

---

## 4. 使用场景与参数建议

### 4.1 典型应用场景

#### 场景 1：医疗搜索查询隐私保护（推荐）✅

**背景**：用户在医疗平台搜索疾病、药品信息时，搜索日志可能被攻击者分析，推断用户的健康状况。

**威胁模型**：
- **查询日志分析攻击**：攻击者获取搜索日志后，通过频率分析、时序关联等手段识别敏感查询
- **重识别风险**：结合时间戳、IP地址等元数据，可能将查询关联到具体用户

**推荐配置**：
- **领域**：`domain="medical"`
- **虚假查询数量**：`num_dummies=3~5`（平衡隐私与开销）
- **自定义池**：建议使用医院内部常见病症列表

**示例**：
```python
from engine.privacy.qol import obfuscate_query

# 用户真实查询
user_query = "糖尿病患者用药趋势"

# 执行查询混淆
obfuscated_queries = obfuscate_query(
    query=user_query,
    num_dummies=4,
    domain="medical",
    seed=42  # 可复现结果（调试用），生产环境应移除
)

print(f"原始查询: {user_query}")
print(f"混淆后查询集:")
for i, q in enumerate(obfuscated_queries, 1):
    print(f"  {i}. {q}")

# 输出示例：
# 原始查询: 糖尿病患者用药趋势
# 混淆后查询集:
#   1. 高血压患者日常防治
#   2. 脑梗塞康复期护理
#   3. 糖尿病患者用药趋势  ← 真实查询混在其中
#   4. 胃溃疡饮食注意事项
#   5. 冠心病二级预防方案
```

**批量处理**：
```python
from engine.privacy.qol import obfuscate_query_batch

# 一批用户查询
user_queries = [
    "如何治疗高血压",
    "糖尿病并发症筛查",
    "胃癌早期症状"
]

# 批量混淆
batch_results = obfuscate_query_batch(
    queries=user_queries,
    num_dummies=3,
    domain="medical"
)

for original, obfuscated in zip(user_queries, batch_results):
    print(f"\n原始: {original}")
    print(f"混淆: {obfuscated}")
```

**REST API 调用**：
```bash
curl -X POST http://127.0.0.1:8079/v1/privacy/qol/obfuscate \
  -H "Content-Type: application/json" \
  -d '{
    "query": "糖尿病患者用药趋势",
    "num_dummies": 4,
    "domain": "medical"
  }'
```

**注意事项**：
- ✅ **语义槽位替换**：自动识别"糖尿病"等疾病词，生成高拟真度虚假查询
- ✅ **位置随机化**：真实查询在返回列表中的位置随机，攻击者无法通过位置判断
- ⚠️ **性能开销**：每个查询产生 `num_dummies + 1` 个请求，后端需能处理额外负载
- 💡 **最佳实践**：在前端发送前混淆，避免明文查询进入网络传输层

---

#### 场景 2：政务服务平台查询保护

**背景**：市民在政务平台查询社保、公积金、税务等信息时，查询内容可能泄露个人财务状况或办事意图。

**推荐配置**：
- **领域**：`domain="generic"`
- **虚假查询数量**：`num_dummies=2~4`
- **自定义池**：使用当地政务服务高频事项列表

**示例**：
```python
from engine.privacy.qol import obfuscate_query

# 用户查询公积金提取
user_query = "如何提取住房公积金"

# 通用领域混淆
obfuscated = obfuscate_query(
    query=user_query,
    num_dummies=3,
    domain="generic"
)

print(obfuscated)
# 可能输出：
# ['社保卡补办流程', '如何提取住房公积金', '个人所得税专项附加扣除', '居住证办理条件']
```

**自定义政务事项池**：
```python
government_services = [
    "身份证换领流程",
    "护照办理预约",
    "驾驶证年审",
    "婚姻登记预约",
    "户口迁移手续",
    "社保卡激活",
    "公积金提取",
    "医保报销流程",
    "不动产登记",
    "营业执照变更"
]

obfuscated = obfuscate_query(
    query="如何提取住房公积金",
    num_dummies=4,
    domain="generic",
    generic_pool=government_services  # 使用自定义池
)
```

**注意事项**：
- ✅ **长度特征防御**：自动筛选与真实查询长度相近的虚假查询，防止通过句长识别
- ⚠️ **地域差异**：不同城市的政务服务事项不同，建议定制本地化池
- 💡 **组合策略**：对特别敏感的查询（如税务、房产），可增加 `num_dummies` 到 5~8

---

#### 场景 3：企业内部知识库搜索

**背景**：员工在企业内部搜索引擎中查询薪资、绩效、晋升政策等敏感信息时，搜索日志可能被HR或管理层监控。

**推荐配置**：
- **领域**：`domain="generic"` 或自定义
- **虚假查询数量**：`num_dummies=3~6`（内部监控风险较高）
- **自定义池**：企业常见HR政策、IT支持事项

**示例**：
```python
from engine.privacy.qol import obfuscate_query

# 员工查询敏感信息
sensitive_queries = [
    "年度调薪政策",
    "绩效考核标准",
    "裁员补偿方案"
]

# 企业内部事项池
hr_policies = [
    "年假申请流程",
    "加班费计算规则",
    "差旅报销标准",
    "培训补贴申请",
    "工位调整申请",
    "办公设备申领",
    "门禁卡补办",
    "邮箱密码重置"
]

for query in sensitive_queries:
    obfuscated = obfuscate_query(
        query=query,
        num_dummies=5,
        domain="generic",
        generic_pool=hr_policies
    )
    print(f"\n原始: {query}")
    print(f"混淆: {obfuscated}")
```

**集成到搜索客户端**：
```python
class PrivacyAwareSearchClient:
    def __init__(self, base_url, num_dummies=4):
        self.base_url = base_url
        self.num_dummies = num_dummies
    
    def search(self, query: str):
        """搜索前先混淆查询"""
        # 步骤1: 混淆查询
        obfuscated_queries = obfuscate_query(
            query=query,
            num_dummies=self.num_dummies,
            domain="generic"
        )
        
        # 步骤2: 并行发送所有查询（包括真实和虚假）
        import requests
        from concurrent.futures import ThreadPoolExecutor
        
        def send_query(q):
            return requests.post(
                f"{self.base_url}/search",
                json={"q": q}
            ).json()
        
        with ThreadPoolExecutor() as executor:
            results = list(executor.map(send_query, obfuscated_queries))
        
        # 步骤3: 只返回真实查询的结果（需要标识哪个是真实的）
        # 注意：实际实现需要跟踪哪个索引是真实查询
        true_result_idx = obfuscated_queries.index(query)
        return results[true_result_idx]

# 使用
client = PrivacyAwareSearchClient("https://internal-search.company.com")
result = client.search("年度调薪政策")
```

**注意事项**：
- ⚠️ **服务端配合**：需要修改搜索客户端以支持批量发送并识别真实结果
- ⚠️ **缓存影响**：虚假查询可能污染搜索缓存，需评估缓存策略
- 💡 **替代方案**：若无法修改客户端，可在代理层（如Nginx）注入虚假查询

---

#### 场景 4：学术研究数据收集

**背景**：研究者需要收集用户对敏感话题（如心理健康、性健康）的搜索行为数据，但需保护参与者隐私。

**推荐配置**：
- **领域**：根据研究主题选择
- **虚假查询数量**：`num_dummies=5~10`（研究场景可承受更高开销）
- **自定义池**：与研究主题相关的中性话题

**示例**：
```python
from engine.privacy.qol import obfuscate_query
import json

# 心理健康研究相关查询
mental_health_queries = [
    "抑郁症治疗方法",
    "焦虑症自我调节",
    "心理咨询预约"
]

# 中性话题池（降低敏感性）
neutral_topics = [
    "天气预报",
    "公交线路查询",
    "电影排期",
    "餐厅推荐",
    "旅游景点",
    "购物优惠",
    "体育赛事",
    "音乐排行榜"
]

# 混淆并保存
anonymized_data = []
for query in mental_health_queries:
    obfuscated = obfuscate_query(
        query=query,
        num_dummies=8,
        domain="generic",
        generic_pool=neutral_topics
    )
    anonymized_data.append({
        "obfuscated_batch": obfuscated,
        "timestamp": "2024-01-15T10:30:00Z",
        "participant_id": "P001"  # 仅用于去重，不关联真实身份
    })

# 保存到研究数据集
with open("research_dataset.json", "w") as f:
    json.dump(anonymized_data, f, indent=2, ensure_ascii=False)
```

**伦理审查要点**：
```python
# 生成隐私保护说明文档
privacy_statement = {
    "method": "Query Obfuscation (QOL)",
    "parameters": {
        "num_dummies": 8,
        "plausible_deniability": f"1/{8+1} = 11.1%"
    },
    "guarantee": "攻击者最多有11.1%的概率识别真实查询",
    "data_retention": "研究结束后删除原始查询，仅保留混淆后数据"
}
print(json.dumps(privacy_statement, indent=2))
```

**注意事项**：
- ✅ **可否认性（Plausible Deniability）**：参与者可以声称自己搜索的是虚假查询中的任何一个
- ⚠️ **IRB审查**：涉及人类受试者的研究需通过机构审查委员会批准
- 💡 **透明度**：应向参与者说明隐私保护措施，获得知情同意

---

#### 场景 5：即时通讯应用搜索历史保护

**背景**：用户在聊天应用中搜索联系人、群组或消息时，搜索历史可能被设备窃取或云端泄露。

**推荐配置**：
- **领域**：`domain="generic"`
- **虚假查询数量**：`num_dummies=2~3`（移动端性能受限）
- **实施位置**：客户端本地混淆后再上传

**示例**：
```python
from engine.privacy.qol import obfuscate_query

# 用户搜索联系人
contact_search = "张三"

# 轻量级混淆（移动端）
obfuscated = obfuscate_query(
    query=contact_search,
    num_dummies=2,
    domain="generic",
    generic_pool=["李四", "王五", "赵六", "客服", "技术支持"]
)

# 发送混淆后的查询集到服务器
for query in obfuscated:
    send_to_server(query)  # 伪代码
```

**移动端优化**：
```python
# 预加载常用联系人作为dummy池
common_contacts = ["妈妈", "爸爸", "同事A", "同事B", "快递", "外卖"]

def mobile_obfuscate(query: str) -> list:
    """移动端优化的混淆函数"""
    return obfuscate_query(
        query=query,
        num_dummies=2,  # 移动端减少dummy数量
        domain="generic",
        generic_pool=common_contacts
    )
```

**注意事项**：
- ⚠️ **性能限制**：移动设备CPU/电池有限，`num_dummies`不宜过大
- ⚠️ **网络开销**：每个dummy查询都产生网络请求，考虑WiFi环境下才启用
- 💡 **本地存储**：混淆后的搜索历史也应加密存储

---

#### 场景 6：电商搜索行为分析

**背景**：电商平台需要分析用户搜索趋势，但用户搜索记录包含购买意图、健康状况等敏感信息。

**推荐配置**：
- **领域**：`domain="generic"`
- **虚假查询数量**：`num_dummies=3~5`
- **自定义池**：热门商品分类、促销活动

**示例**：
```python
from engine.privacy.qol import obfuscate_query_batch

# 用户搜索历史
search_history = [
    "孕妇装",
    "血糖仪",
    "减肥药"
]

# 电商商品分类池
product_categories = [
    "手机配件",
    "图书音像",
    "家居用品",
    "运动户外",
    "美妆护肤",
    "食品饮料",
    "数码家电",
    "服装鞋包"
]

# 批量混淆
obfuscated_history = obfuscate_query_batch(
    queries=search_history,
    num_dummies=4,
    domain="generic",
    generic_pool=product_categories
)

# 发送到分析系统
for original_batch in obfuscated_history:
    for query in original_batch:
        analytics_system.track_search(query)
```

**数据分析侧处理**：
```python
# 分析师只能看到混淆后的聚合数据
from collections import Counter

all_queries = []
for batch in obfuscated_history:
    all_queries.extend(batch)

# 统计热门搜索词（包含大量噪声）
top_searches = Counter(all_queries).most_common(10)
print("热门搜索:", top_searches)
# 由于噪声存在，真实趋势被模糊化，保护个体隐私
```

**注意事项**：
- ✅ **聚合分析仍有效**：虽然单个用户查询被混淆，但大规模聚合统计仍能反映整体趋势
- ⚠️ **个性化推荐影响**：混淆可能降低推荐系统准确性，需权衡隐私与体验
- 💡 **A/B测试**：对比启用混淆前后的转化率，评估业务影响

---

### 4.2 参数选择指南

#### num_dummies（虚假查询数量）

| 场景 | 推荐值 | 隐私强度 | 性能开销 | 说明 |
|-----|-------|---------|---------|------|
| 低敏感查询 | 1~2 | 弱（50%~33%不可区分） | 低（2~3倍） | 一般资讯搜索 |
| 中等敏感 | 3~5 | 中（25%~17%不可区分） | 中（4~6倍） | 医疗、政务查询 |
| 高敏感查询 | 6~10 | 强（14%~9%不可区分） | 高（7~11倍） | 心理、财务查询 |
| 极高敏感 | 10+ | 极强（≤9%不可区分） | 极高（11倍+） | 研究、法律场景 |

**选择原则**：
- **隐私强度** = `1 / (num_dummies + 1)`
- `num_dummies=3` → 攻击者最多有25%概率识别真实查询
- `num_dummies=9` → 攻击者最多有10%概率识别真实查询
- **平衡点**：大多数场景推荐 `num_dummies=3~5`

**经验公式**：
```python
def recommend_num_dummies(sensitivity_level: str) -> int:
    """根据敏感度推荐num_dummies"""
    levels = {
        "low": 2,
        "medium": 4,
        "high": 6,
        "critical": 10
    }
    return levels.get(sensitivity_level, 4)
```

---

#### domain（领域选择）

| 领域 | 内置池大小 | 适用场景 | 语义质量 |
|-----|----------|---------|---------|
| `medical` | 20种病症+15个疾病实体 | 医疗搜索、健康咨询 | 高（专业术语） |
| `generic` | 20项政务服务+10个业务实体 | 政务、电商、通用搜索 | 中（通用词汇） |

**选择建议**：
- **医疗场景必选 `medical`**：确保虚假查询包含医学术语，提高拟真度
- **其他场景用 `generic`**：覆盖常见行政、生活类查询
- **自定义池优先**：若有特定领域需求，提供自定义池效果更佳

---

#### medical_pool / generic_pool（自定义池）

**何时使用自定义池**：
1. **内置池不适用**：如金融、教育等垂直领域
2. **需要更高拟真度**：使用业务相关的真实查询作为dummy
3. **避免池耗尽**：内置池较小，高频使用时重复率高

**池大小建议**：
| 使用频率 | 最小池大小 | 推荐池大小 |
|---------|-----------|-----------|
| 低频（<100次/天） | 10 | 20 |
| 中频（100~1000次/天） | 20 | 50 |
| 高频（>1000次/天） | 50 | 100+ |

**构建高质量池的方法**：
```python
# 方法1: 从历史日志中提取高频查询（脱敏后）
historical_queries = [
    "感冒发烧怎么办",
    "高血压饮食建议",
    "糖尿病食谱",
    # ... 至少20条
]

# 方法2: 从公开数据源获取
# - 医疗：卫健委常见病列表
# - 政务：各地政务服务事项清单
# - 电商：商品分类树

# 方法3: 人工构造（确保多样性）
custom_pool = [
    "症状A的治疗",
    "症状B的预防",
    "药物C的副作用",
    # ... 覆盖不同主题
]

# 使用自定义池
obfuscated = obfuscate_query(
    query="用户真实查询",
    num_dummies=4,
    domain="medical",
    medical_pool=historical_queries  # 传入自定义池
)
```

**池质量检查**：
```python
def validate_dummy_pool(pool: list, min_size: int = 10) -> dict:
    """验证dummy池质量"""
    issues = []
    
    # 检查1: 池大小
    if len(pool) < min_size:
        issues.append(f"池大小{len(pool)} < 最小要求{min_size}")
    
    # 检查2: 重复率
    unique_ratio = len(set(pool)) / len(pool)
    if unique_ratio < 0.9:
        issues.append(f"重复率过高: {1-unique_ratio:.1%}")
    
    # 检查3: 长度分布
    lengths = [len(q) for q in pool]
    avg_len = sum(lengths) / len(lengths)
    if max(lengths) > 3 * avg_len:
        issues.append("存在异常长的查询")
    
    return {
        "valid": len(issues) == 0,
        "pool_size": len(pool),
        "unique_ratio": unique_ratio,
        "avg_length": avg_len,
        "issues": issues
    }

# 使用
report = validate_dummy_pool(custom_pool)
if not report["valid"]:
    print("池质量问题:", report["issues"])
```

---

#### seed（随机种子）

**作用**：控制随机数生成器，使结果可复现。

**使用场景**：
- ✅ **调试/测试**：固定seed便于排查问题
- ❌ **生产环境**：不应设置seed，每次调用应真正随机

**示例**：
```python
# 测试时使用seed（结果可复现）
result1 = obfuscate_query("测试查询", num_dummies=3, seed=42)
result2 = obfuscate_query("测试查询", num_dummies=3, seed=42)
assert result1 == result2  # 两次结果相同

# 生产环境不使用seed（真正随机）
result = obfuscate_query("用户查询", num_dummies=3)  # 每次结果不同
```

---

### 4.3 性能优化建议

#### 批量处理优化

**问题**：逐条调用 `obfuscate_query` 会产生多次函数调用开销。

**解决方案**：使用 `obfuscate_query_batch`
```python
from engine.privacy.qol import obfuscate_query_batch

# ❌ 不推荐：逐条处理
queries = ["查询1", "查询2", "查询3"]
results_bad = [obfuscate_query(q, num_dummies=3) for q in queries]

# ✅ 推荐：批量处理
results_good = obfuscate_query_batch(queries, num_dummies=3)
```

**性能对比**（参考值）：
| 查询数量 | 逐条处理 | 批量处理 | 加速比 |
|---------|---------|---------|--------|
| 10 | ~0.01s | ~0.008s | 1.25x |
| 100 | ~0.1s | ~0.07s | 1.4x |
| 1000 | ~1s | ~0.6s | 1.7x |

---

#### 池预加载与缓存

**问题**：每次调用都传递相同的自定义池，增加内存拷贝开销。

**解决方案**：预加载池到全局变量
```python
# 应用启动时加载一次
MEDICAL_POOL = load_medical_pool_from_db()  # 从数据库/配置文件加载

# 后续调用直接引用
def handle_user_query(query: str):
    return obfuscate_query(
        query=query,
        num_dummies=4,
        domain="medical",
        medical_pool=MEDICAL_POOL  # 引用全局池
    )
```

---

#### 异步处理

**场景**：高并发服务中，混淆操作不应阻塞主线程。

**示例**：
```python
import asyncio
from concurrent.futures import ThreadPoolExecutor

def obfuscate_sync(query: str, num_dummies: int):
    """同步包装器"""
    return obfuscate_query(query, num_dummies=num_dummies)

async def obfuscate_async(query: str, num_dummies: int):
    """异步版本"""
    loop = asyncio.get_event_loop()
    with ThreadPoolExecutor() as pool:
        result = await loop.run_in_executor(
            pool, 
            obfuscate_sync, 
            query, 
            num_dummies
        )
    return result

# 使用
obfuscated = await obfuscate_async("用户查询", num_dummies=3)
```

---

### 4.4 安全注意事项

#### 池暴露风险

**风险描述**：
- 若攻击者获知dummy池内容，可通过排除法识别不在池中的查询为真实查询
- 例如：池中有100个查询，返回的4个查询中有3个在池中，第4个不在池中→很可能是真实查询

**缓解措施**：
1. **使用大池**：池大小 >> num_dummies，降低碰撞概率
2. **定期轮换池**：每周/每月更新池内容
3. **动态池**：根据时间、地域等因素动态调整池
4. **不公开池内容**：不要在前端代码中硬编码池

**示例：动态池**：
```python
import datetime

def get_dynamic_pool(hour: int) -> list:
    """根据时间段返回不同的池"""
    morning_pool = ["早餐推荐", "晨练地点", "通勤路线"]
    afternoon_pool = ["午餐优惠", "下午茶", "健身课程"]
    evening_pool = ["晚餐食谱", "电影排期", "夜跑路线"]
    
    if 6 <= hour < 12:
        return morning_pool
    elif 12 <= hour < 18:
        return afternoon_pool
    else:
        return evening_pool

# 使用
current_hour = datetime.datetime.now().hour
pool = get_dynamic_pool(current_hour)
obfuscated = obfuscate_query(query, num_dummies=3, generic_pool=pool)
```

---

#### 长度特征泄漏

**风险描述**：
- 若虚假查询长度与真实查询差异明显，攻击者可通过长度识别真实查询
- 例如：真实查询5个字，虚假查询都是20个字→容易识别

**防护机制**：
- ✅ **已内置长度过滤**：模块自动筛选长度相近的查询（±6~12字符）
- ⚠️ **仍需注意**：若池过小，可能找不到长度匹配的查询

**验证长度分布**：
```python
def check_length_distribution(obfuscated_batch: list):
    """检查混淆结果的长度分布"""
    lengths = [len(q) for q in obfuscated_batch]
    avg_len = sum(lengths) / len(lengths)
    std_dev = (sum((l - avg_len)**2 for l in lengths) / len(lengths)) ** 0.5
    
    print(f"平均长度: {avg_len:.1f}")
    print(f"标准差: {std_dev:.1f}")
    print(f"长度范围: {min(lengths)}~{max(lengths)}")
    
    if std_dev > 5:
        print("⚠️ 警告：长度差异较大，可能存在泄漏风险")

# 使用
check_length_distribution(obfuscated)
```

---

#### 时序关联攻击

**风险描述**：
- 攻击者观察查询发送的时间模式
- 真实查询可能在用户输入后立即发送，虚假查询可能批量延迟发送

**缓解措施**：
1. **并行发送**：所有查询（真实+虚假）同时发送
2. **随机延迟**：每个查询添加随机延迟（0~500ms）
3. **批量提交**：累积多个用户的查询后统一发送

**示例：并行发送**：
```python
import requests
from concurrent.futures import ThreadPoolExecutor
import time
import random

def send_with_random_delay(query: str, url: str):
    """带随机延迟的发送"""
    time.sleep(random.uniform(0, 0.5))  # 随机延迟0~500ms
    return requests.post(url, json={"q": query})

def send_obfuscated_queries(obfuscated: list, url: str):
    """并行发送混淆后的查询"""
    with ThreadPoolExecutor() as executor:
        futures = [
            executor.submit(send_with_random_delay, q, url) 
            for q in obfuscated
        ]
        results = [f.result() for f in futures]
    return results
```

---

#### 与其他隐私技术的组合

**推荐组合**：

1. **QOL + 差分隐私**：
   ```python
   # 先混淆查询
   obfuscated = obfuscate_query(query, num_dummies=3)
   
   # 对查询结果再加DP噪声（若结果是数值型）
   from engine.privacy.dp import DPApi
   dp = DPApi(namespace="search_analytics")
   noisy_count = dp.count(search_results, epsilon=1.0)
   ```

2. **QOL + K-匿名**：
   ```python
   # 对用户属性做K-匿名
   anonymized_profile = k_anonymize_record(user_profile, qi_cols, hierarchies, k=5)
   
   # 再对查询做QOL
   obfuscated = obfuscate_query(query, num_dummies=3)
   ```

3. **QOL + 访问控制**：
   ```python
   # 基于角色的访问控制
   if user.role in ["admin", "analyst"]:
       # 管理员可查看原始查询
       log_query(query, user.id)
   else:
       # 普通用户只记录混淆后的查询
       log_query(obfuscated, user.id)
   ```

---

### 4.5 故障排查速查表

| 现象 | 可能原因 | 解决方案 |
|------|---------|---------|
| 返回结果不包含真实查询 | `query` 参数为空或格式错误 | 检查请求体字段名和类型 |
| dummy查询重复率高 | 池过小或 `num_dummies` 过大 | 增大池或减小 `num_dummies` |
| 虚假查询与真实查询风格差异大 | 未使用对应领域的池 | 选择正确的 `domain` 或提供自定义池 |
| 性能下降明显 | `num_dummies` 过大导致请求量激增 | 降低 `num_dummies` 或启用批量处理 |
| 长度特征明显 | 池中没有长度匹配的查询 | 扩充池，确保长度多样性 |
| 结果不可复现（测试时） | 未设置 `seed` | 调试时传入固定 `seed` |
| 内存占用高 | 池过大且频繁拷贝 | 预加载池到全局变量，避免重复传递 |
| 网络超时 | 并发发送过多虚假查询 | 降低 `num_dummies` 或使用连接池 |
| 槽位替换失败 | 真实查询不包含已知敏感词 | 退化为长度过滤模式，属正常行为 |
| 自定义池未生效 | 参数名错误或池格式不对 | 检查参数名为 `medical_pool`/`generic_pool`，确保是list类型 |

---

### 4.6 与其他隐私技术的对比

| 技术 | 隐私保证 | 数据效用 | 适用场景 | 开销 |
|------|---------|---------|---------|------|
| **查询混淆(QOL)** | 中等（概率性） | 高（真实查询不变） | 搜索、查询日志 | 中（3~6倍请求） |
| **差分隐私** | 最强（数学证明） | 中（有噪声） | 统计分析 | 低（仅加噪） |
| **K-匿名** | 中等（启发式） | 高 | 数据发布 | 低（离线处理） |
| **完全加密** | 强（密码学） | 低（无法分析） | 数据传输 | 中（加解密） |
| **联邦学习** | 强（数据不出域） | 高 | 模型训练 | 高（通信开销） |

**选择建议**：
- **实时查询保护**：首选 QOL
- **离线数据分析**：QOL + 差分隐私
- **数据共享发布**：K-匿名 + QOL
- **高敏感场景**：QOL + DP + 访问控制（纵深防御）

---

## 5. 最佳实践总结

1. **选择合适的 num_dummies**：大多数场景 3~5 即可平衡隐私与性能
2. **使用自定义池**：避免内置池被攻击者掌握，提高拟真度
3. **批量处理**：使用 `obfuscate_query_batch` 提升性能
4. **并行发送**：真实和虚假查询同时发送，避免时序泄漏
5. **定期轮换池**：降低池暴露风险
6. **监控指标**：关注 `privacy_qol_operations_total` 和响应时间
7. **组合防护**：与差分隐私、访问控制等技术结合使用
8. **前端混淆**：在数据离开用户设备前就进行混淆
9. **评估业务影响**：A/B测试混淆对转化率、推荐准确性的影响
10. **文档化**：记录池的来源、更新策略、参数选择理由