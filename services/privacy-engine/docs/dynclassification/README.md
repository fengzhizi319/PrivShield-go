# 动态分类分级（多标准适配）文档索引

本目录包含 `PrivShield` 动态分类分级（多标准适配）模块的全套 SDLC 软件生命周期文档、三层漏斗设计、硬件加速（CUDA `sm_120` / TensorRT）与配置包全套指南。

---


## 📚 文档全景图表

| 分类 | 文档 | 说明 | 目标读者 |
|---|---|---|---|
| **需求与架构** | [prd.md](./prd.md) | 产品需求文档（PRD） | 产品经理、项目经理 |
| | [design.md](./design.md) | 动态分类分级主架构设计与配置解耦方案 | 架构师、后端开发 |
| | [修改意见.md](./修改意见.md) | 架构评审修改意见与重构路线图 | 架构师、技术负责人 |
| **三层漏斗与裁决** | [three_layer_funnel_design.md](./three_layer_funnel_design.md) | 三层漏斗模型（Rule $\to$ Small-NER $\to$ LLM/VLM）架构 | 架构师、AI 工程师 |
| | [funnel_adjudication.md](./funnel_adjudication.md) | 多层命中裁决机制与置信度策略算法 | 算法工程师、后端开发 |
| | [downgrade_override_design.md](./downgrade_override_design.md) | 敏感度降级与 Override 压制规则设计 | 架构师、数据安全专家 |
| **规则与标准生成** | [rule_parsing_guide.md](./rule_parsing_guide.md) | 规则 YAML 语法结构解析与编写指南 | 接入开发者、运维人员 |
| | [standard_profile_generator.md](./standard_profile_generator.md) | 行业/地方分类分级标准文档自动生成器 | 合规人员、数据工程师 |
| **硬件加速与模型导出** | [cuda_sm120_setup.md](./cuda_sm120_setup.md) | NVIDIA Blackwell (`sm_120`) CUDA 12.8 环境避坑指南 | SRE、AI 部署工程师 |
| | [model_optimization_pipeline.md](./model_optimization_pipeline.md) | PyTorch $\to$ ONNX $\to$ TensorRT Engine 标准编译优化链路 | 性能优化工程师、AI 工程师 |
| **接口与使用** | [api_reference.md](./api_reference.md) | Python SDK / REST / gRPC API 完整参考手册 | 集成开发者、客户端开发 |
| | [examples.md](./examples.md) | 动态分类分级代码示例与常用 SDK 调用 | 接入开发者 |
| **运维与测试** | [ops.md](./ops.md) | 运维手册、热重载管理、 Prometheus 监控与故障排查 | SRE、运维工程师 |
| | [testing.md](./testing.md) | 测试策略、单元测试示例与 Schema 校验指南 | QA、测试开发工程师 |

---

## 💡 核心设计理念

动态分类分级架构旨在解决传统隐私保护产品中数据分类分级逻辑与代码深度绑定（硬编码）的痛点。核心设计原则为：

1. **标准配置化**：解耦硬编码 Enum，允许通过 YAML 动态定义分类树（Categories）与分级矩阵（Levels: L1~L5 / C1~C4 / 1~4级）。
2. **标准文档自动生成**：支持输入 Markdown 格式的行业/地方分类分级标准文档（如《四川省健康医疗大数据应用指南.md》），自动抽取并解析生成全套规则 YAML 配置文件，降低配置门槛。
3. **规则声明化**：匹配条件（字段名模式、值正则、匹配算子）完全配置化，支持按领域包（Domain Packs）与标准组合（Standards）组织。
4. **算子插件化**：内置通用匹配算子（`regex`、`id_card_checksum`、`luhn_checksum` 等），并提供单例算子注册表（`OperatorRegistry`）支持业务自定义算子扩展。
5. **执行上下文动态化**：请求时传入 `domain` 或 `standard` 上下文，引擎自动从 `ProfileLoader` 按需加载对应规则包，并支持规则热重载。
6. **高性能推理加速**：Layer-2 NER 支持 ONNX Runtime 与 TensorRT 硬件加速，在 NVIDIA Blackwell (`sm_120`) 等最新 GPU 架构上提供极低延迟响应。

---

## 🚀 推荐阅读路径

1. **功能快速了解**：阅读 [prd.md](./prd.md) 了解业务需求，接着阅读 [design.md](./design.md) 掌握通用引擎架构。
2. **算法与漏斗理解**：阅读 [three_layer_funnel_design.md](./three_layer_funnel_design.md) 与 [funnel_adjudication.md](./funnel_adjudication.md)。
3. **规则编写与生成**：参考 [rule_parsing_guide.md](./rule_parsing_guide.md) 与 [standard_profile_generator.md](./standard_profile_generator.md)。
4. **硬件加速与部署**：参考 [cuda_sm120_setup.md](./cuda_sm120_setup.md) 和 [model_optimization_pipeline.md](./model_optimization_pipeline.md)。
5. **代码接入与测试**：参考 [examples.md](./examples.md)、[api_reference.md](./api_reference.md)、[ops.md](./ops.md) 和 [testing.md](./testing.md)。