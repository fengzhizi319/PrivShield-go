# 动态分类分级（多标准适配）产品需求文档 (PRD)

> **状态**：Layer-1 规则引擎 + Layer-2 NER + Layer-3 LLM 三层漏斗已全部实现并上线。
> 最后同步：2026-08。

## 1. 概述与背景

在原有的 `PrivShield` 分类分级实现中，分类目录（`BusinessCategory`）、敏感等级（`SensitivityLevel`）以及具体的字段模式匹配逻辑均采用 Python 代码硬编码。随着部署环境的多样化，不同行业领域（如医疗健康 DB51/T 2989、金融行业 JR/T 0197、国家标准 GB/T 43697-2024、欧盟 GDPR 等）对分类维度与敏感分级存在截然不同的标准与合规要求。

本需求旨在构建**动态分类分级标准适配引擎**，实现引擎代码与行业分类标准、分级矩阵以及匹配规则的彻底解耦。新行业接入或标准变更时，只需更新 YAML 规则配置文件，无需重构或重新发布 sidecar 引擎服务。

当前系统已演进为**三层漏斗架构**（Layer-1 规则引擎 → Layer-2 Small-NER → Layer-3 LLM），并配套图像打码、安全地板、领域策略注册表等生产级能力。

---

## 2. 产品设计目标

- **零代码接入新标准**：新增行业/标准仅需添加 YAML 配置文件，无需修改 Python 引擎代码。
- **分类体系配置化**：等级定义（L1~L5 / C1~C4 / G1~G4 / 1~4级）和分类目录树均由元数据 YAML 配置驱动。
- **匹配算子插件化**：通用算子（`regex`、身份证校验、Luhn 校验等）一次注册，多领域规则共享复用，并支持运行时自定义扩展。算子可返回 `bool`（简单命中）或 `OperatorResult`（携带动态等级/类别）。
- **三层智能漏斗**：Layer-1 确定性规则 → Layer-2 Small-NER 实体识别 → Layer-3 LLM 语义仲裁，逐层递进，置信度驱动。
- **安全地板 (Safety Floor)**：LLM 裁定等级不可低于值级证据等级，防止 Prompt 注入导致危险降级。
- **运行时动态上下文**：请求支持通过 `domain` 或 `standard` 上下文参数动态切换规则集。
- **配置热重载**：支持在不重启 Sidecar 服务的前提下进行配置热加载与规则重载（基于文件 mtime 的两阶段提交）。
- **无缝向后兼容**：兼容现有分类接口契约，旧合规模板参数（`template`）可自动平滑映射到新标准体系。
- **领域解耦 (DIP)**：通过 `DomainStrategyRegistry` 实现通用引擎内核与领域特异性脱敏策略的依赖倒置解耦。

---

## 3. 功能需求矩阵

### 3.1 Layer-1 声明式规则引擎

| 需求 ID | 功能模块 | 需求描述 | 状态 |
|---|---|---|---|
| DYN-TAX-1 | 分类体系配置 | 支持通过 YAML 定义行业领域的 Taxonomy，包含敏感等级集合（id, name, rank）、分类树目录结构及默认等级。 | ✅ 已实现 |
| DYN-TAX-2 | 多等级体系适配 | 支持医疗（L1~L5）、金融（C1~C4）、广东医疗（G1~G4）、国标（1~4级）以及二分法等任意等级体系，并能基于 rank 自动计算最大敏感等级（`max_level`）。 | ✅ 已实现 |
| DYN-RULE-1 | 声明式规则包 | 支持以 `Domain Profile` 为单位组织领域规则库，每条规则指定匹配目标（字段名/字段值）、匹配算子（operator）及参数。`RuleProfile` 支持 `default_taxonomy` 关联校验。 | ✅ 已实现 |
| DYN-RULE-2 | 逻辑组合与优先级 | 规则支持指定 `AND` / `OR` 多匹配算子逻辑，以及 `priority` 优先级控制执行次序。 | ✅ 已实现 |
| DYN-RULE-3 | 标准组合定义 | 支持通过 `Standard Profile` 将多个领域包（如 `general-pii` + `medical`）进行组合，并支持规则级覆盖（`rule_overrides`）、追加规则（`extra_rules`）、追加降级规则（`extra_downgrade_rules`）及追加复合规则（`extra_composite_rules`）。 | ✅ 已实现 |
| DYN-RULE-4 | 降级/压制规则支持 | 支持兜底降级（`force_suppress=false`）与强制覆盖（`force_suppress=true`）两种模式。强制覆盖支持 `max_force_suppress_level` 等级上限与 `exempt_rules`（别名 `exclude_rules`）豁免例外名单（支持 `fnmatch` 通配符），实现精细压制控制。 | ✅ 已实现 |
| DYN-RULE-5 | 复合升级规则 | 支持结合多字段共存上下文（如"疾病"+"基因"）进行记录级分类升级的复合规则判定（`CompositeRuleEngine`，只升不降）。 | ✅ 已实现 |
| DYN-OP-1 | 内置匹配算子库 | 内置提供 14+ 算子：`regex`、`keyword_contains`、`prefix_match`、`suffix_match`、`id_card_checksum`、`medical_card_checksum`、`luhn_checksum`、`icd10_range`、`length_range`、`exact_match`、`ip_address`、`mac_address`、`chinese_name`、`email`。 | ✅ 已实现 |
| DYN-OP-2 | 算子注册表 | 提供 `OperatorRegistry` 类级单例，支持装饰器注册（`@OperatorRegistry.register`）与运行时动态注册（`register_func`）。线程安全（写路径 Lock 保护，读路径无锁 GIL 优化）。算子可返回 `bool` 或 `OperatorResult(hit, level, category)`。 | ✅ 已实现 |
| DYN-ENG-1 | 通用规则引擎 | 提供 `ConfigurableRuleEngine`，根据传入的 `DomainTaxonomy` 和 `RuleProfile` 动态求值，四阶段执行（普通规则→降级规则→强制覆盖→合并去重），内置线程安全 OrderedDict LRU 评估缓存（`PRIVACY_ENGINE_CACHE_MAX_SIZE`，默认 4096）。 | ✅ 已实现 |
| DYN-LOAD-1 | Profile 加载与缓存 | 提供 `ProfileLoader` 支持配置文件加载、对象解析校验与 LRU 缓存调度。支持 `fork-after-warmup` COW 优化（主进程预加载，子进程共享）。 | ✅ 已实现 |
| DYN-LOAD-2 | 规则热重载 | 提供 REST API（`POST /v1/dynclassification/profiles/reload`）及基于文件 mtime 的请求驱动两阶段提交热重载。 | ✅ 已实现 |
| DYN-VALIDATE-1 | 规则校验器 | `validator.py` 提供规则配置静态校验：算子存在性检查、等级/类别合法性验证、`force_suppress` 配置提示、模糊匹配建议。 | ✅ 已实现 |
| DYN-COMPAT-1 | 模板向下兼容 | 旧参数 `params.template` 能够自动转换映射至 `params.standard`，确保现有客户端不破坏。 | ✅ 已实现 |

### 3.2 Layer-2/3 三层漏斗与智能分类

| 需求 ID | 功能模块 | 需求描述 | 状态 |
|---|---|---|---|
| DYN-FUNNEL-1 | 三层漏斗编排 | `ClassificationFunnel` 编排 Layer-1→L2→L3 执行流，支持置信度衰减、冲突检测、LLM 仲裁。 | ✅ 已实现 |
| DYN-FUNNEL-2 | 置信度策略 | `ConfidencePolicy` 支持 `conflict_confidence`、`llm_confidence_threshold`、`enable_ner`/`enable_llm`/`enable_llm_arbitration`、`ner_trigger_max_rank`、`min_tag_confidence` 等配置，支持 YAML + 环境变量双覆盖。 | ✅ 已实现 |
| DYN-NER-1 | Layer-2 Small-NER | 支持 ONNX Runtime / TensorRT / ModelScope / MLX 四种后端，lazy-load 降级链。智能门禁（`_should_trigger_ner`）过滤 PII 短字段/纯数字文本。 | ✅ 已实现 |
| DYN-LLM-1 | Layer-3 LLM 仲裁 | 支持 Qwen3.5 本地模型 / vLLM HTTP / OpenAI HTTP / MLX 四种后端。三种触发场景：规则冲突仲裁(A)、低置信度兜底(B)、图像多模态(C)。进程级信号量并发保护 + 内存预检 + 超时降级。 | ✅ 已实现 |
| DYN-SAFETY-1 | 安全地板 (Safety Floor) | LLM 裁定等级必须落在冲突标签等级集合内（场景 A）或不低于上游等级（场景 B/C）。非法裁定拒绝并标记 `needs_human_review=True`。Prompt 注入防护（`sanitize_for_prompt`）。 | ✅ 已实现 |
| DYN-GEN-1 | 标准 Profile 自动生成 | `standard_profile_generator.py` 从 Markdown 标准文档自动提取关键词规则，生成 YAML 规则包并反向校验。 | ✅ 已实现 |

### 3.3 图像打码与安全治理

| 需求 ID | 功能模块 | 需求描述 | 状态 |
|---|---|---|---|
| DYN-IMG-1 | 图像打码 | `image_redaction.py` 支持 JPEG/PNG/BMP/WebP/DICOM 图像敏感区域遮挡。沙箱安全防护：路径穿越阻止（`PRIVACY_IMAGE_ALLOWED_DIRS` 白名单）、DecompressionBomb 防护、OOM 防护（像素上限）、磁盘防满。 | ✅ 已实现 |
| DYN-IMG-2 | 自动多模态触发 | `auto_llm_on_image=true` 时检测到图像/DICOM 字段自动触发 Layer-3 多模态 LLM 分类。 | ✅ 已实现 |
| DYN-DIP-1 | 领域策略注册表 | `DomainStrategyRegistry` 实现 DIP 解耦：领域模块（如 `medical_pipeline`）作为 Provider 注册脱敏回调，通用引擎作为 Consumer 动态调度。 | ✅ 已实现 |

### 3.4 运维与可观测性

| 需求 ID | 功能模块 | 需求描述 | 状态 |
|---|---|---|---|
| DYN-METRICS-1 | 可观测性监控 | 暴露规则命中计数、算子调用频率、引擎加载耗时、压制计数等 Prometheus 指标及结构化日志。 | ✅ 已实现 |
| DYN-CACHE-1 | 评估 LRU 缓存 | 规则引擎内置 OrderedDict 线程安全 LRU 缓存，`PRIVACY_ENGINE_CACHE_MAX_SIZE` 可配，`cache_info()` / `clear_cache()` API。 | ✅ 已实现 |
| DYN-PROFILE-GEN-1 | 自动生成 YAML | 从 Markdown 标准文档自动生成领域规则 YAML 与分类体系 YAML，含反向校验。 | ✅ 已实现 |

---

## 4. 接口契约与验收标准

### 4.1 REST / HTTP 接口验收标准
1. **POST `/v1/dynclassification/eval`**：
   - 当请求体传入 `{"domain": "finance", "standard": "jrt0197"}` 时，引擎应成功调用金融标准规则集并返回对应 `C1~C4` 级别的 `SecurityTag`。
   - 当请求传入旧参数 `"template": "sc_health_db51"` 时，引擎应自动映射为 `standard="sc_health_db51"` 并正常返回结果。
   - 返回结果包含 `engine_layer` 字段，标识实际产出分类结果的引擎层级（`L1_RULE` / `L2_SMALL_NER` / `L3_LLM`）。

2. **POST `/v1/dynclassification/profiles/reload`**：
   - 触发热重载后，响应应返回成功与重新载入的 Profile 数量，后续请求应立即使用更新后的 YAML 配置。

3. **GET `/v1/dynclassification/standards` 与 `/v1/dynclassification/operators`**：
   - 应能列出当前环境中已注册的所有有效标准清单及 14+ 匹配算子列表。

4. **POST `/v1/dynclassification/classify`**（record/table 级分类）：
   - 支持单字段、单记录、多记录表级分类，返回聚合的 `FieldClassificationResult` → `RecordClassificationResult` → `TableClassificationResult`。

### 4.2 性能与稳定性验收标准
- **延迟指标**：单个字段的 Layer-1 动态匹配延迟较旧版硬编码引擎增加不超过 5%（百微秒级）。LRU 缓存命中时直接返回，零算子开销。
- **内存开销**：多 Standard Profile 缓存占用增加不超过 10 MB。
- **异常隔离**：若某个自定义算子执行抛出异常，引擎应有 try-catch 拦截防护，记录错误指标并安全跳过该规则，不得引发整个服务进程崩溃。
- **LLM 过载保护**：进程级信号量（`PRIVACY_LLM_MAX_CONCURRENCY`，默认 1）+ 排队超时（`PRIVACY_LLM_SEMAPHORE_WAIT_SECONDS`，默认 30s）+ 内存预检（`PRIVACY_LLM_MIN_FREE_MEM_MB`，默认 512MB）三重保护，LLM 不可用时平滑降级到 Layer-1 结果。