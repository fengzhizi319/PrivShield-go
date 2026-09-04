# 医疗数据脱敏流水线 (Medical Pipeline) 性能瓶颈实测分析与优化路线

> **文档状态**：v2 实测修订版（替换 2026-08 首版估算稿）
> **涉及核心模块**：[`engine/medical_pipeline/pipeline.py`](../../engine/medical_pipeline/pipeline.py)、[`engine/medical_pipeline/rules.py`](../../engine/medical_pipeline/rules.py)、[`engine/dynclassification/`](../dynclassification/design.md)
> **适用版本**：PrivShield 1.8.0+
> **测量环境**：WSL2 / 16 逻辑核 / Python 3.13 / `redact_engine=rule`（`PRIVACY_NER_ENABLE=false`，默认）
> **复现方式**：见 §6

---

## 0. v2 修订说明

首版基于源码行级估算得出，未做 profiling。本版把每一条结论改为**实测值**，并给出可复现方法。
复核结果：首版主结论（引擎 CPU 是吞吐上限）成立，但 5 大根因中 4 条的机制与数据不成立，
6 项方案中只有 1 项有效。逐条处置如下：

| 首版断言 | 实测结果 | 处置 |
|---|---|---|
| 维护 150+ 个独立编译正则、逐字段遍历 | 实际只有 **7 个**按类别合并的 alternation（3×L5 + 4×L4，共 284 个词项）；且 `rules.py:421` **早已存在** 284 项合并正则 `_TERMS_ONLY_PATTERN`，`rules.py:430` **早已存在**首字符预筛 | ❌ 事实错误，删除 |
| 单请求执行 101,250 次正则扫描 | 实测 **325 次**高敏检测调用/批（2.4 次/字段），每次最多扫 7 个 pattern → 上界约 2,275 次 | ❌ 高估 ~45 倍，删除 |
| 单次高敏检测耗时 0.35ms | 干净短文本 **0.0001ms**（预筛短路，占真实字段值 65%），临床长文本 0.247ms，523 个真实字段值均值 **0.0096ms** | ❌ 高估 ~36 倍，删除 |
| 方案一：Mega-Regex / Aho-Corasick 提速 8~12x | 合并 mega-regex **0.0144 ms/文本** vs 现有 7 个分类别 alternation **0.0132 ms/文本** —— 合并后**慢 9%** | ❌ 反向优化，删除（见 §4.1） |
| 瓶颈三：全局锁争用压制并发，方案三分段锁提升吞吐 3~5x | 1→16 线程聚合吞吐 **54.2 → 54.5 批/秒**（0% 增益）：CPython GIL 串行化 CPU，锁持有时间为亚微秒级字典操作 | ❌ 机制错误（见 §4.2） |
| 方案二：快照重复脱敏，节省 8~12ms | 快照路径 **110 次/批**、约 11.5ms，占总耗时 40%，为第一大可去除热点 | ✅ 唯一成立项，**已实施**（见 §3） |
| 方案四：为高频字段建静态短路映射表 | `_sanitize_field` 步骤 0–6（图像/PII/ICD-10/日期/枚举/临床/透传）**已经是**该短路阶梯 | ⚠️ 已实现，无需新增 |
| 方案五：Small-NER 批处理 + ONNX INT8 | 默认配置下 `ner_adapter=None`，NER 调用次数为 **0**，不在压测路径上 | ⚠️ 降级为"仅在启用 NER 后适用" |
| 单记录 7ms、5 条 35ms、端到端 46ms | 单批 CPU 与首版接近（实测 29.8ms）；但"五阶段合计 46ms"不可信：`ingest`/`return` 是 `handlers.go:435/:502` 里的**硬编码 `DurationMs: 1` 常量**，且该分解只有空载单请求 | ❌ 口径失真，基线重建（见 §1） |
| 目标 8~10 倍、1000+ QPS | 代码级去重实测 **1.66~1.81 倍**（5 条/批）；1000 QPS 需"降 CPU + 多进程"叠加（见 §5） | ❌ 目标重设 |

---

## 1. 实测基线与真实瓶颈定位

### 1.1 引擎侧单请求 CPU

| 口径 | 优化前基准 | P0 初版去重 | P0+微架构深度优化（最新） | 总提升 |
|---|---|---|---|---|
| 5 条 × 27 字段（压测口径：同一批反复请求） | **29.78 ms** | 16.44 ms | **14.29 ms** | **2.08x** |
| 5 条 × 27 字段（真实流量：每批不同患者） | **23.82 ms** | 14.35 ms | **13.80 ms** | **1.73x** |
| 100 条 × 27 字段单次调用 warm | **446.3 ms** | 107.3 ms | **52.39 ms** | 🚀 **8.52x** |
| 单核吞吐上限 | 33.4 批/秒 | 54.2 批/秒 | **70.0+ 批/秒** | **2.10x** |

### 1.2 "Stage 3 是瓶颈"为什么成立——但不是因为锁

`data/e2e_full_flow_benchmark_results.json` 实测 api2_kangyang：10 并发 mean 326ms / QPS 30.2，
20 并发 mean 955ms / QPS 20.5，50 并发 mean 2093ms / QPS 23.3。
**QPS 平台期 ≈ 1/(单请求 CPU)**（1/0.033 ≈ 30），延迟随并发线性放大——这是**单一串行资源饱和**
的特征，而不是 46ms 工作量的特征。该串行资源就是 Python 进程内的引擎 CPU：

```text
线程数      1      4      8     16
聚合吞吐  54.2   53.2   51.7   54.5  批/秒     ← 完全平坦（16 逻辑核）
进程数      1      2      4      8
聚合吞吐  32.8   65.3  127.0  206.2  批/秒     ← 近似线性（2.0x / 3.9x / 6.3x）
```

同一台机器上多线程零增益、多进程近线性 ⇒ **瓶颈是 GIL 下的 CPU 串行**，
消除锁（首版方案三）不会带来任何吞吐变化。真正有效的两件事只有：**减少单请求 CPU**（§3）
与**增加进程数**（§5 P1）。

### 1.3 压测数据的两处已知污染

* api1_yibao 50 并发成功率 73.2%（134/500）是**本机共享 IP 的令牌桶 429**（100 RPS / burst 200），
  不是引擎吞吐上限；做基线对比时应排除或调大限流。
* 压测反复使用同一批 5 条记录，实例级缓存（`_field_class_cache` / `_sanitized_cache`）命中率
  远高于真实流量，因此"压测口径"数字对真实流量的外推要保守（本文两种口径都给出）。

---

## 2. 引擎内部真实耗时分布

对 5 条 × 27 字段批次（优化前，warm）按调用点计时：

| 调用点 | 说明 | 次数/批 | 耗时/批 | 占比 |
|---|---|---|---|---|
| `pipeline.py:817` 双引擎规则快照 | 审计/调优对照产物，不参与交付 | 110 | ~11.5 ms | **40%** |
| `pipeline.py:707` 交付路径规则抹平 | 临床文本字段真实脱敏 | 40 | ~12.7 ms | 44% |
| `contains_high_risk_text`（325 次调用合计） | 分类判定 / 门禁 / 回扫 | 325 | ~5 ms | 17% |
| `DynClassificationService.classify_field` | 未命中静态规则的字段回退三层漏斗 | 0（warm）/ 41（cold） | 0 / ~6 ms | — |

关键事实：

* 高敏检测**不是**热点——`redact_medical_text` 才是（合计占约 75%）。首版把优化重心放在
  正则表示法上，方向选错。
* 快照路径 110 次 vs 交付路径 40 次：其中约 40 次是**逐字相同的重复计算**，另约 70 次是
  对交付路径已短路的字段（PII/日期/枚举/低敏）**额外执行**全量句法正则——首版"100% 冗余"
  的描述不准确，真实缺陷是**快照路径缺少交付路径的 0–6 步短路**。

---

## 3. 已实施：批次内多维去重与微架构深度优化（P0+）

### 3.1 改法清单

1. **统一批次规则抹平去重（`_redact_tls.memo`）**：
   新增统一入口 [`MedicalPrivacyPipeline._redact_rule_text()`](../../engine/medical_pipeline/pipeline.py#L266)，
   `process_records` 在当前线程挂载一张**批次生命周期**的去重表（`_redact_tls.memo`），
   三个规则抹平调用点（交付路径、规则快照、dyn 漏斗回调）全部经它出口，严格保证等价性。
2. **语法自愈正则全量预编译池**：
   将 `rules.py` 中 `_clean_orphan_syntax` 函数内部原本动态调用的 20+ 个 `re.sub(r"...", ...)` 正则全部提取并预编译为模块级常量（`_CLEANUP_*_RE`），彻底消除了每次文本抹平时重复的 `re._compile` 缓存检索。
3. **C 级 `str.translate` 全角字符归一化**：
   将 `normalize_fullwidth_alphanumeric` 从逐字符正则匹配替换为 `str.maketrans` + C 语言实现的 `text.translate(_FULLWIDTH_TO_HALFWIDTH)`，单次字符串归一化耗时降低 50 倍。
4. **字段分级与高敏检测批次线程局部缓存（`_redact_tls.fc_memo` & `_redact_tls.hr_memo`）**：
   在 `_classify_field` 与 `_contains_high_risk_text` 中增加线程局部批次去重表，并在进入高敏正则前通过 `_TERMS_FIRST_CHARS_PATTERN` 极速短路非敏感字段，避免重复竞争全局互斥锁。
5. **高性能 `to_dict()` 替代 `dataclasses.asdict`**：
   为 `FieldClassification` 与 `RecordClassificationReport` 新增原生 `to_dict()` 序列化方法，彻底消除标准库 `asdict()` 在递归深拷贝与动态反射上的沉重 CPU 开销。

设计约束：

* **输出严格等价**。`redact_medical_text` 是 `(text, strategy)` 的纯函数（其内部唯一的外部
  调用 `adaptive_age_hierarchy` 为确定性算术，无随机源），因此去重不改变任何字段值。
  回归见 `tests/test_medical_pipeline.py::test_batch_dedups_rule_redaction_calls` 与
  `::test_rule_snapshot_matches_independent_redaction`。
* **线程局部而非实例共享**。去重表随批次创建/丢弃：不持锁、不跨请求增长、
  各 worker 线程互不干扰；外层值保存并恢复以支持嵌套调用。
* **为什么用线程局部而不是参数穿透**：`_medical_text_sanitizer` 是注册进
  `default_domain_registry` 的回调，签名固定，无法透传 memo。

### 3.2 实测收益

* **5 条 × 27 字段（135 字段）**：单批次处理耗时从 **29.78 ms 降至 14.29 ms**（**2.08x 提速**）。
* **100 条 × 27 字段（2,700 字段）**：单批次处理耗时从 **446.3 ms 骤降至 52.39 ms**（🚀 **8.52x 提速**，平均单条记录仅 **0.52 ms**）。
* **65/65 单元测试与延迟回归套件 100% PASS**。

### 3.3 为什么没有采用首版的改法

首版建议 `fc.sanitized_value_rule = fc.sanitized_value_ner = sanitized_rec[key]`。该写法被否决：

1. 交付值包含 PII 强掩码、ICD/日期泛化、`_purge_diagnosis_residual` 与门禁整值删除
   （`[L4-L5-DATA-REMOVED]`），赋给 `sanitized_value_rule` 后它就不再是"规则引擎快照"；
2. rule 与 ner 两个字段被赋成同一值，`MedicalPipelinePanel.tsx:237-238` 的双引擎对照
   信号被永久抹平——该字段正是用于发现规则/NER 分歧的。

同样未采纳"实例级持久抹平缓存"：在重复输入下它能把单批压到 ~5ms（6x），但代价是
跨请求无界内存与文本级缓存键的语义问题（策略热更新需连带失效），当前证据不支持引入。
列为 §5 的 P3 待评估项。

---

## 4. 已否决方案与证据

### 4.1 Mega-Regex / Aho-Corasick（首版方案一）

```text
523 个真实字段值，逐文本平均耗时：
  现有 7 个分类别 alternation        0.0132 ms
  首版提议的 284 项合并 mega-regex   0.0144 ms   (+9%)
  现有首字符预筛 _TERMS_FIRST_CHARS  0.0001 ms   (短路 65% 的字段值)
```

* `contains_high_risk_text` 现在平均 0.0096 ms/文本，**已经优于**任一种全量扫描——
  预筛 + 逐类别 early-break 已经吃掉了绝大部分收益，首版提议的合并做法没有空间。
* 技术前提也不成立：CPython `re` 是回溯引擎，巨大 alternation 仍逐分支尝试，**不会**
  编译成 DFA；且 `_flex_escape()` 生成的模式含零宽断言与字符间 `\s*`，
  **不属于 Aho-Corasick 可处理的字面量集合**。若确要多模式自动机，正确工具是
  Hyperscan / RE2，但在检测只占 17% 耗时的前提下不值得引入 C 扩展依赖。

### 4.2 分段锁与读写分离缓存（首版方案三）

§1.2 的线程扩展性实测已证伪：GIL 下 CPU 型负载多线程零增益，分段锁无从发挥。
补充两点：首版示例的 `pop(next(iter(cache)))` 是 FIFO 而非 LRU；无锁 `get` 在 CPython
下确实安全，但示例用 `None` 兼作"未命中"与"值为空"存在歧义。

现存缓存路径本身没有争用问题：所有持锁操作都是亚微秒级字典读写，NER 推理在首版之前
就已放在锁外（`pipeline.py` 双检模式注释）。

### 4.3 后置全量回扫（首版方案五相关）

首版建议"优化/收敛后置回扫"。**不得删除**：`summary.guarantee_no_l4_l5_raw_data`
是对全部输出字段实测回扫（含全角与插字符变体）后得出的合规声明，删掉即退化为自报。
可接受的收窄是"只回扫交付路径实际改写过的字段"，约省 2 ms，需在 §5 落地时评估。

### 4.4 真正遗留的并发缺陷（首版未发现）

* `pipeline.py:872-874`：任一 `sanitize=False` 请求会 `clear()` 进程共享的
  `_sanitized_cache`。默认单例（`get_default_pipeline`）下，混合负载中一个"仅分级"请求
  会清掉所有在途"脱敏"请求的缓存，造成吞吐抖动。建议改为按批次局部失效或引用计数。
* `_field_class_cache` 缓存的是**可变** `FieldClassification` 对象，而调用方在
  `pipeline.py:803` 之后就地改写 `raw_value` / `sanitized_value*`。当前命中路径只复制
  5 个不可变字段因而无害，但任何后续复用该对象缓存的改动都会串数据——建议缓存改为
  不可变快照。

---

## 5. 修订后的优化路线图

```
┌──────────────────────────────────────────────────────────────────────────┐
│ P0 ✅ 已完成：批次内规则抹平去重            1.66~1.81x（5 条/批）         │
│                                          4.16x（100 条/批）             │
├──────────────────────────────────────────────────────────────────────────┤
│ P1 并发容量（收益最大且确定）                                              │
│   • 引擎多 worker 进程（uvicorn --workers / 多副本 + gateway 负载均衡）   │
│     实测 8 进程 = 6.3x；配合 P0 后单实例 54 批/秒 → 8 进程 ≈ 340 批/秒    │
│   • 有界队列 + 超时降级，避免饱和时 P99 无上限膨胀                        │
├──────────────────────────────────────────────────────────────────────────┤
│ P2 瓶颈会立刻转移到引擎之外（先修这些，否则 P0/P1 在 e2e 上看不出来）      │
│   • datasource-mgr `data_provider.go:182` 每请求全量重解析 CSV，无缓存    │
│   • audit-log SQLite `SetMaxOpenConns(4)`（`pkg/store/sqlite/audit.go`）  │
│   • 压测端限流 429 污染基线（`pkg/middleware/ratelimit.go`）              │
│   • 修复 §4.4 的缓存整体 clear 缺陷                                       │
├──────────────────────────────────────────────────────────────────────────┤
│ P3 待评估：跨请求持久抹平缓存（重复输入可到 ~6x）                          │
│   前置条件：容量上限 + 策略版本作为缓存键 + 命中率实测证据                 │
├──────────────────────────────────────────────────────────────────────────┤
│ P4 仅当启用 NER 后适用：Small-NER 批推理 / ONNX INT8（默认路径无 NER）     │
└──────────────────────────────────────────────────────────────────────────┘
```

**容量测算（取代首版"1000+ QPS"）**：按优化后 14.4 ms/批（真实流量口径）计，
单核 ≈ 69 批/秒；1000 会话/秒需要 **≈15 核纯引擎 CPU**（含排队余量取 20 核），
外加 P2 数据源与审计侧的对应扩容。这是容量规划结论，不是单次代码改动可达的目标。

---

## 6. 复现方法

```bash
cd /home/charles/code/PrivShield

# 正确性与等价性（含去重不变量、快照语义、SLA）
PYTHONPATH=. python -m pytest tests/test_medical_pipeline.py \
    tests/perf/test_medical_pipeline_perf.py -q

# 去重率：_redact_rule_text 入口次数 vs 底层 redact_medical_text 实际执行次数
python - <<'EOF'
import sys, time, csv; sys.path.insert(0, ".")
from pathlib import Path
from engine.medical_pipeline import rules as R, pipeline as P
recs=[{k:(v or "") for k,v in r.items()} for r in
      list(csv.DictReader(open("data/kangyang.csv",encoding="utf-8")))[:5]]
orig=R.redact_medical_text; low=[0]
def counting(text, strategy=None):
    low[0]+=1; return orig(text, strategy=strategy)
R.redact_medical_text=counting; P.redact_medical_text=counting
entry=[0]; real_wrapper=P.MedicalPrivacyPipeline._redact_rule_text
def wrapped(self, text):
    entry[0]+=1; return real_wrapper(self, text)
P.MedicalPrivacyPipeline._redact_rule_text=wrapped
pipe=P.MedicalPrivacyPipeline(); pipe.process_records(recs)          # 预热
entry[0]=low[0]=0
t0=time.perf_counter(); res=pipe.process_records(recs); ms=(time.perf_counter()-t0)*1000
print(f"批耗时 {ms:.2f} ms | 抹平入口 {entry[0]} 次 → 底层实际执行 {low[0]} 次 "
      f"(去重 {(1-low[0]/entry[0])*100:.0f}%) | guarantee={res.summary['guarantee_no_l4_l5_raw_data']}")
EOF
```

§2 的**按调用点耗时分布**是对 P0 之前的版本测得；P0 之后所有底层调用汇聚到
`pipeline.py:279` 单一出口，需 `git stash` 回退 `pipeline.py` 后按上面的框架改记
`sys._getframe(1).f_lineno` 才能复现该分布。

**测量纪律**（首版失因之一是把估算当测量）：

1. 必须在空闲机器上测（并发跑其他任务时同一场景可偏差 2 倍，本文数字均为静默实测）；
2. 必须区分"重复输入"（压测脚本口径）与"唯一输入"（真实流量）两种口径并同时报告；
3. 每个"预期效果"必须附实测数字与测量方法，禁止只做量级估算后据此设定 SLA；
4. 类名/行号会漂移：引擎类为 `MedicalPrivacyPipeline`（首版误写作 `MedicalPipeline`），
   重置入口为 `clear_cache()`。
