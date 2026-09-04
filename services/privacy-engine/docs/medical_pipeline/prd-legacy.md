# 医疗数据分类分级与脱敏流水线 — 需求文档

> Medical Data Classification & Masking Pipeline — PRD

## 1. 背景与目标

项目当前已具备完整的隐私原语能力（脱敏、差分隐私、K-匿名、分类分级），但缺少一条**端到端的演示流水线**，能够直观展示"原始医疗数据 → 分类分级 → 脱敏输出"的完整过程。

本需求的目标：
1. 提供一批高仿真医疗数据（kangyang.csv），包含 L1-L5 各敏感度级别
2. 构建流水线模块，串联 `dynclassification` + `privacy/masking`，一键完成分级 + 脱敏
3. 在测试控制台前端增加可视化面板，支持 Python 后端和 Go 后端双通道联调

---

## 2. 功能需求

### 2.1 数据生成（需求 1）

| 编号 | 需求 | 优先级 |
|---|---|---|
| F-1.1 | 在 `scripts/` 中新建 Python 脚本，生成 CSV 文件 `kangyang.csv` | P0 |
| F-1.2 | 自动生成 20 条记录，包含 28 个字段（见 design.md 第 2 节） | P0 |
| F-1.3 | 身份证号符合 GB 11643-1999 标准（MOD 11-2 校验码正确） | P0 |
| F-1.4 | 病史字段包含 L4 级内容（详细手术记录）和 L5 级内容（基因检测、精神疾病史） | P0 |
| F-1.5 | 20 条中 3-4 条为图片病例（文字描述 + base64 占位），其余为文字病例 | P1 |
| F-1.6 | 姓名、地址、残疾证号、医保证号等格式逼真，可通过格式校验 | P1 |
| F-1.7 | 所有信息看起来是真实的（模拟真实病人信息） | P1 |

### 2.2 分类分级 + 脱敏流水线（需求 2）

| 编号 | 需求 | 优先级 |
|---|---|---|
| F-2.1 | 在 `engine/` 下新建 `pipeline/` 目录 | P0 |
| F-2.2 | 调用 `dynclassification` 对 CSV 数据进行分类分级 | P0 |
| F-2.3 | 调用 `privacy/masking` 对 L4/L5 级数据进行脱敏处理 | P0 |
| F-2.4 | 输出两部分结果：① 分级数据（各级别分布、字段明细）② 脱敏后数据 | P0 |
| F-2.5 | 保证脱敏后数据不含 L4/L5 级原始信息 | P0 |
| F-2.6 | 提供 REST API（`/v1/pipeline/process_csv` 和 `/v1/pipeline/process_records`） | P0 |
| F-2.7 | 在 `tests/` 中增加详细的单元测试 | P0 |

### 2.3 全栈集成（需求 3）

| 编号 | 需求 | 优先级 |
|---|---|---|
| F-3.1 | kangyang.csv 复制到 Python 后端和 Go 后端的合适目录 | P0 |
| F-3.2 | Python 后端新增 `/v1/pipeline/process` 端点，代理转发到 agent | P0 |
| F-3.3 | Go 后端新增 `/v1/pipeline/process` 端点，通过 gRPC 调用 agent | P0 |
| F-3.4 | 前端新增 MedicalPipelinePanel 面板，展示分级结果和脱敏数据 | P0 |
| F-3.5 | 前端面板支持「执行 kangyang.csv 分级脱敏」一键操作 | P1 |
| F-3.6 | 前端面板支持上传自定义 CSV 文件 | P2 |
| F-3.7 | 前端面板支持导出分级报告 JSON 和脱敏后 CSV | P2 |
| F-3.8 | 前端面板支持中英文国际化 | P1 |
| F-3.9 | 保证 Python 后端和 Go 后端都能跑通全流程 | P0 |

---

## 3. 非功能需求

| 编号 | 需求 | 说明 |
|---|---|---|
| NF-1 | 流水线处理 20 条记录耗时 < 5 秒 | 不含 LLM 层推理（仅 L1 规则引擎） |
| NF-2 | 数据生成脚本无外部依赖 | 仅使用 Python 标准库 |
| NF-3 | 脱敏后的数据不可逆推原始信息 | 利用现有 masking 原语保证 |
| NF-4 | 前端面板不影响现有功能 | 独立组件，新增 View 类型 |

---

## 4. 验收标准

### 4.1 数据生成验收

- [ ] `python scripts/data/generate_medical_data.py` 执行成功，输出 `data/kangyang.csv`
- [ ] CSV 包含 20 行 × 28 列
- [ ] 所有身份证号通过 MOD 11-2 校验
- [ ] 病史中至少 3 条包含 L4 级内容、至少 2 条包含 L5 级内容
- [ ] 至少 3 条记录包含图片病例标记

### 4.2 流水线验收

- [ ] `tests/test_pipeline.py` 全部通过
- [ ] `tests/test_generate_medical_data.py` 全部通过
- [ ] 分级结果覆盖 L1-L5 至少 3 个级别
- [ ] 脱敏后数据中 id_card_no、name、registered_address 等字段值已变更
- [ ] REST API `/v1/pipeline/process_records` 返回正确的分级 + 脱敏结果

### 4.3 全栈联调验收

- [ ] Python 后端 `/v1/pipeline/process` 返回正确结果
- [ ] Go 后端 `/v1/pipeline/process` 返回正确结果
- [ ] 前端面板在 Python 后端模式下正常展示分级 + 脱敏结果
- [ ] 前端面板在 Go 后端模式下正常展示分级 + 脱敏结果
- [ ] 中英文切换正常

---

## 5. 约束与假设

| 项目 | 说明 |
|---|---|
| 分类标准 | 默认使用 `jrt0197`（金融行业标准），可扩展 |
| 规则目录 | 使用项目根目录 `rules/` 下现有规则 |
| ML 模型 | 不依赖 NER/LLM 层（仅 L1 规则引擎），保证无 ML 环境也能运行 |
| 图片病例 | 使用文字描述 + base64 占位，不依赖真实图片文件 |
| 数据用途 | 仅用于测试演示，非真实病人数据 |

---

## 6. 关联文档

| 文档 | 路径 |
|---|---|
| 设计方案 | `docs/medical_pipeline/design-legacy.md` |
