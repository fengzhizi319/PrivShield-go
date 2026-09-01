# 数盾 PrivShield 仓内性能基线报告（P2-8）

> 对应整改项：`docs/architecture/liuzhou_govcloud_data_security_architecture.md` 第十二章 **P2-8「性能与容量证据缺失」**。
> 本报告只覆盖**仓内可复现的 Go Benchmark 原语级采样**，用于建立回归可比对的最低限度性能证据。
> **端到端压测、真实吞吐、多副本容量、真实 NER 模型推理均未实测**，见 §6。

---

## 1. 运行环境指纹

| 项 | 值 |
|---|---|
| 采集时间（UTC） | `2026-09-01T05:14:28Z` |
| Git commit | `f429119bf9d747c4df501658966e24f6f2323b7d`（`main`） |
| 工作副本状态 | 含第十二章 P0/P1/P2 批次整改的未提交改动（`dirty_files=177`） |
| Go 版本 | `go1.27.0 linux/amd64` |
| CPU | Intel(R) Core(TM) Ultra 7 356H |
| 逻辑核数 | 16（`GOMAXPROCS=16`，输出后缀 `-16`） |
| 内核 / 虚拟化 | `6.6.114.1-microsoft-standard-WSL2`（WSL2） |

> 环境归因说明：WSL2 上的观测值受宿主 Windows 调度与节能策略影响，**绝对值只能作为趋势参考，不可直接作为柳州政务云上线容量依据**。上线前的容量核验必须在真实专区硬件上重跑（§6）。

## 2. 复现方式

```bash
# 输出含环境指纹头 + 全部 Benchmark 原始三次采样
make bench BENCH_OUT=/tmp/privshield-bench.txt
```

等价命令：

```bash
cd privacy-go-sdk && go test -run '^$' -bench . -benchmem -count=3 \
  ./ldp/... ./masking/... ./kano/... ./dp/...
cd engine-go && go test -run '^$' -bench . -benchmem -count=3 \
  ./internal/dynclassification/... ./internal/service/...
```

统计口径：**33 个 Benchmark × `-count=3` = 99 次采样**，表格取**中位数**（median-of-3），`-benchmem` 报告分配量。

## 3. 基线结果（median of 3）

### 3.1 privacy-go-sdk · 掩码与哈希原语（`masking`）

| Benchmark | ns/op | B/op | allocs/op | 单核吞吐（ops/s） |
|---|---|---|---|---|
| `BenchmarkGuessFieldType` | 15.11 | 0 | 0 | 66.2 M |
| `BenchmarkMaskPhone` | 22.10 | 16 | 1 | 45.2 M |
| `BenchmarkMaskIdCard` | 24.03 | 24 | 1 | 41.6 M |
| `BenchmarkMaskEmail` | 46.67 | 24 | 1 | 21.4 M |
| `BenchmarkMaskBankCard` | 46.12 | 40 | 2 | 21.7 M |
| `BenchmarkTruncate` | 50.64 | 8 | 1 | 19.7 M |
| `BenchmarkMaskAddress` | 116.40 | 24 | 1 | 8.6 M |
| `BenchmarkHashHMAC` | 204.60 | 152 | 4 | 4.9 M |
| `BenchmarkMaskChineseName` | 242.80 | 56 | 4 | 4.1 M |
| `BenchmarkFpeEncryptNumeric` | 422.30 | 568 | 9 | 2.4 M |
| `BenchmarkMaskRecord10Fields` | 722.10 | 288 | 17 | 1.4 M |

关键结论：10 字段整行脱敏 **722 ns/行 ≈ 72 ns/字段**；`GuessFieldType`（探查热路径）与 `MaskPhone` 均保持 **≤ 25 ns**，前者做到 **0 分配**。

### 3.2 privacy-go-sdk · 差分隐私（`dp`）

| Benchmark | ns/op | B/op | allocs/op | 单核吞吐 |
|---|---|---|---|---|
| `BenchmarkAddLaplaceNoise` | 17.34 | 0 | 0 | 57.7 M |
| `BenchmarkNoisyCount` | 17.52 | 0 | 0 | 57.1 M |
| `BenchmarkAddGaussianNoise` | 41.15 | 0 | 0 | 24.3 M |
| `BenchmarkNoisySum` | 41.98 | 0 | 0 | 23.8 M |
| `BenchmarkNoisyMean` | 81.18 | 0 | 0 | 12.3 M |
| `BenchmarkNoisyCount_Batch1000` | 17 763.00 | 0 | 0 | 56.3 K（= 17.8 ns/计数） |

关键结论：**DP 全家族 0 字节分配**，批量计数摊薄到 **17.8 ns/值**；Laplace 约为 Gaussian 开销的 42%。

### 3.3 privacy-go-sdk · LDP 与 K-匿名（`ldp` / `kano`）

| Benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| `BenchmarkAgeHierarchy` | 55.85 | 8 | 1 |
| `BenchmarkAnonymizeRecord` | 304.70 | 352 | 4 |
| `BenchmarkEstimateBinaryFrequency` | 2 761.00 | 0 | 0 |
| `BenchmarkEstimateCategoricalHistogram` | 107 367.00 | 272 | 2 |

关键结论：类别直方图估计是唯一 **百微秒级** 原语（三次采样 104.98–117.04 µs，离散度 11.5%，为本报告波动最大项），高基数类别字段的 LDP 上报需在批量任务中排程，不适合放在单请求同步链路。

### 3.4 engine-go · 三层漏斗第一层规则引擎（`dynclassification`）

| Benchmark | ns/op | B/op | allocs/op | 单核吞吐 |
|---|---|---|---|---|
| `BenchmarkClassify_NoMatch` | 49.64 | 24 | 1 | 20.1 M |
| `BenchmarkClassify_FieldMatch` | 58.27 | 32 | 1 | 17.2 M |
| `BenchmarkIsChineseName` | 39.44 | 0 | 0 | 25.4 M |
| `BenchmarkValidateLuhn` | 49.29 | 128 | 1 | 20.3 M |
| `BenchmarkIsIpAddress` | 135.10 | 0 | 0 | 7.4 M |
| `BenchmarkValidateIdCard` | 183.00 | 0 | 0 | 5.5 M |
| `BenchmarkACAutomaton_Search` | 406.90 | 112 | 3 | 2.5 M |
| `BenchmarkClassifyBatch_10Records` | 2 933.00 | 2 000 | 44 | 3.41 M 记录/s（293 ns/记录） |
| `BenchmarkAhoCorasick_Contains100Terms` | 9 539.00 | 0 | 0 | 104.8 K |
| `BenchmarkAhoCorasick_Match100Terms` | 11 514.00 | 0 | 0 | 86.9 K |

关键结论：单字段定级 **49–58 ns**，命中与未命中路径差 **< 10 ns**（无显著分支惩罚）；100 词条 AC 自动机全文扫描 **≈ 10 µs 且 0 分配**，支撑第一层在请求同步链路内直接执行。

### 3.5 engine-go · 流式 vs 物化文件脱敏（`service`）

输入固定为 **5 000 行 × 3 列 CSV（`phone,name,remark`，约 105 KB）**。

| Benchmark | ms/op | ns/行 | 单核行吞吐 | B/op | allocs/op |
|---|---|---|---|---|---|
| `BenchmarkProcessFileStream` | 2.032 | 406 | 2.46 M 行/s | 3 016 557 | 55 162 |
| `BenchmarkProcessFileMaterialized` | 5.366 | 1 073 | 0.93 M 行/s | 4 819 834 | 65 050 |
| **流式收益** | **2.64× 快** | — | — | **−37.4% 内存** | **−15.2% 分配次数** |

关键结论：流式路径（P2 批次落地的 CSV/JSON 流式脱敏）在同等输入下 **延迟降至 37.9%、峰值内存降至 62.6%**，验证了「大文件不整表物化」的改造方向；两侧 allocs/op 仍达 5.5 万，说明**行内解析尚未完全零分配**，是下一轮优化的明确入口。

## 4. 与整改项的对应关系

| 整改项 | 本报告提供的证据 |
|---|---|
| P2-8 性能与容量证据缺失 | 建立可复现基线（`make bench` + 环境指纹 + median-of-3），后续改动可用同一命令做 A/B 回归 |
| P2-3 主链路指纹改 SM3 | 指纹改造位于 `PrivacyService` 汇总路径，§3.5 的流式/物化基线即为该改动的回归参照 |
| P0-2 字段级脱敏默认拒绝 | §3.1 表明逐字段脱敏开销为 **72 ns/字段量级**，补齐 18/27 字段规则不构成性能风险 |
| P1-3 NER 桩 / 规则库驱动定级 | §3.4 证明第一层规则引擎 **0 分配、µs 级**，可支撑「规则库驱动的自动分类分级」的表述 |

## 5. 基线使用约定

1. **只做同环境相对比较**：跨机器（WSL2 → 政务云物理机）不可直接比较绝对值。
2. **取中位数、`-count=3` 起步**；离散度 > 10% 的项（当前仅 `EstimateCategoricalHistogram`）需提高 `-count` 或用 `benchstat` 判定显著性。
3. 新增 Benchmark 必须能被 `make bench` 覆盖（放入 `privacy-go-sdk` 或 `engine-go` 的上述包）。
4. 报告为**追加式**：新一轮基线在本文追加 §「YYYY-MM-DD 运行」并保留旧数据，禁止覆盖历史数字。

## 6. 未覆盖范围（不谎报）

以下项目**本仓库当前无法产出真实数据**，第十二章相关结论继续保持 🔴：

| 缺口 | 阻塞原因 |
|---|---|
| 端到端压测（REST/gRPC 全链路 QPS / P99） | 缺柳州政务云真实专区环境、压测客户端与局方验收基准线 |
| 多副本容量与水平扩展曲线 | 需 `pkg/store/postgres` 后端 + K8s 多副本；本地无 PG/集群 |
| NER（第二层）真实推理延迟 | 未交付模型权重，当前为规则桩；`/ops/diagnostics` 已上报 `ner_available:false` |
| GPU 加速与 `privshield_ner_inference_seconds` 分布 | 无 CUDA/ONNX 运行时环境 |
| 生产 TLS/mTLS 握手开销 | 需商用密码产品与真实证书链 |
| 存储层留存/归档实测 IO | 需政务云 NAS/对象存储与 PG 分区表 |

## 7. 原始数据

完整三次采样原始输出（149 行，含 `goos/goarch/cpu` 头）保存在 `make bench` 的 `BENCH_OUT`；
本仓库历史采样可执行 `make bench BENCH_OUT=docs/reports/benchmark_raw_<date>.txt` 重新生成并归档。
