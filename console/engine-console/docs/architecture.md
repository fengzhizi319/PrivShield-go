# 引擎开发者与运维控制台 (engine-console) — 架构设计

> **组件归属**：`console/engine-console`  
> **定位目标**：专门面向 `services/privacy-engine` 的开发者、安全管理员调测台与功能验证界面。

---

## 1. 系统拓扑与双端架构

`engine-console` 由两个子模块组成：
- **`bff-go/` (`:8081`)**：Go 语言编写的高性能 API 网关与 BFF 代理服务；
- **`web/` (`:5173`)**：React 18 + TypeScript + Vite 现代化前端控制台界面。

```
[管理员/开发者浏览器]
         │
         ▼
[engine-console Web (:5173)]
         │
         ▼
[engine-console bff-go (:8081)]
         │ (持有 PRIVACY_AUTH_API_KEY，受限白名单代理)
         ▼
[services/privacy-engine (:8079 / :8000)]
```

---

## 2. 核心功能与测试场景

1. **隐私原语交互式调测**：
   - 字段级掩码规则演练（国密 SM3/SM4、手机号、身份证、银行卡中段遮蔽）；
   - 差分隐私 Laplace / Gaussian 加噪效果与误差对比；
   - Mondrian K-匿名表格切分与等价类数据导出；
   - 查询混淆伪诱饵注入与置乱效果查看。
2. **三层动态分类分级演练**：
   - 输入样本记录，实时观察 AC 自动机规则、ONNX NER 与大模型仲裁漏斗命中结果；
   - 在线修改与热重载 `rules/domains/*.yaml` 规则，实时验证定级变化。
3. **DICOM 医学影像脱敏看板**：
   - 上传 DICOM 影像文件，实时查看患者私密元数据遮蔽与像素级脱敏效果。
4. **引擎运行指标与健康诊断**：
   - 查看三层漏斗规则加载总数、NER 可用性、安全底线兜底等级与时延大屏。

---

## 3. 安全防护与反向代理白名单

`bff-go` 内置严格的 `isAllowedMicroserviceProxyPath` 路径与方法白名单机制：
- 仅放行脱敏算法验证、分类定级、规则概览与诊断端点；
- 严禁通过该代理直接拉取未脱敏原始记录，保障测试过程的安全受控。
