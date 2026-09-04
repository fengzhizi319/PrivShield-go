# 分类分级与隐私脱敏核心引擎 (privacy-engine) — 架构设计

> **组件归属**：`services/privacy-engine`  
> **服务定位**：数盾核心算力节点（无状态极简镜像 ~25MB），承载全系统敏感数据动态分类分级、44 项高阶隐私脱敏与计算保护、国密 TLCP 通信与零内存反向代理。

---

## 1. 架构总览

`privacy-engine` 采用纯 Go 1.25+ 构建，分为两大核心二进制入口：
1. **`privshield-agent`**：核心算力 Agent，暴露 REST (`:8079`) 与 gRPC (`:50051`) 接口；
2. **`privshield-gateway`**：高性能零内存反向代理与 P2C-EWMA 负载均衡网关，暴露 HTTP (`:8000`) 与 gRPC (`:50000`) 接口。

```
                    ┌────────────────────────────────────────┐
                    │  privshield-gateway (:8000 / :50000)   │
                    │  (P2C-EWMA 负载均衡 + BufferPool 反代) │
                    └───────────────────┬────────────────────┘
                                        │
                    ┌───────────────────▼────────────────────┐
                    │   privshield-agent (:8079 / :50051)    │
                    ├────────────────────────────────────────┤
                    │  1. 动态分类分级 3 层漏斗 (Funnel)     │
                    │     • Layer 1: AC自动机 + 正则规则引擎 │
                    │     • Layer 2: ONNX Runtime Small-NER  │
                    │     • Layer 3: 熔断大模型 LLM 仲裁     │
                    ├────────────────────────────────────────┤
                    │  2. 44 项纯 Go 零依赖隐私计算原语 (SDK)│
                    │     • Masking (SM3/SM4, 身份证/手机)   │
                    │     • Differential Privacy (DP / LDP)  │
                    │     • K-Anonymity / L-Diversity        │
                    │     • Query Obfuscation (QoL)          │
                    │     • DICOM 医学图像二值脱敏流水线     │
                    │     • CAS 无锁原子隐私预算会计器       │
                    └────────────────────────────────────────┘
```

---

## 2. 核心模块组成

### 2.1 三层动态分类分级漏斗 (`internal/dynclassification/`)
- **Layer 1: AC 自动机规则引擎**：以 `rules/domains/*.yaml` 为权威规则事实源，支持 Aho-Corasick 多模式串匹配与正则提取，单字段匹配开销仅在微秒级；
- **Layer 2: Small-NER 命名实体识别**：基于 ONNX Runtime，针对未被规则命中的非结构化长文本执行实体识别；
- **Layer 3: 外部 LLM 仲裁与安全降级**：具备 Closed ➔ Open ➔ Half-Open 状态的三态熔断器；外呼 Prompt 物理剥离敏感原值（仅送出字段名与形态指纹），防止明文外泄。

### 2.2 44 项纯数学隐私计算原语 (`sdk/`)
- **掩码原语 (`sdk/masking`)**：SM3 加盐杂凑、SM4-ECB/CBC 国密脱敏、中国大陆二代身份证、11 位手机号、银行卡 LHN 算法中段遮蔽；
- **差分隐私 (`sdk/dp`)**：单趟向量融合 Laplace / Gaussian 机制，支持 Count / Sum / Mean 自适应截断加噪；
- **本地差分隐私 (`sdk/ldp`)**：随机响应机制（Randomized Response）与多分类扰动，提供中心侧样本守恒无偏频次估计；
- **K-匿名与 L-多样性 (`sdk/kano`)**：Mondrian KD-tree 多维空间贪心切分，严格保障等价类容量 $k \ge 2$ 与敏感属性 $l \ge 2$；
- **查询混淆 (`sdk/qol`)**：Fisher-Yates 伪查询诱饵注入与语义置乱；
- **预算会计 (`sdk/budget`)**：基于 CAS 原子无锁循环，严格按租户命名空间隔离扣减 $(\epsilon, \delta)$ 隐私预算。

### 2.3 高性能网关与负载均衡 (`internal/gateway/`)
- 基于 P2C (Pick of Two Random Choices) 与 EWMA (指数加权移动平均时延) 算法，自动向最健康的 agent 实例分发流量；
- 使用 `sync.Pool` 缓冲池实现零内存分配的反向代理流式转发。
