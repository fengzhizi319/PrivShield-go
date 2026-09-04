# 分类分级与隐私脱敏核心引擎 (privacy-engine) — 运维指南

> **组件归属**：`services/privacy-engine`

---

## 1. 健康检查与探针配置

在 Kubernetes 或 Docker 环境中，`privshield-agent` 提供专有轻量探针端点：

- **存活探针 (Liveness)**：`GET /healthz`  
  当 HTTP 监听处于服务状态时返回 `200 OK`，若进程崩溃或陷入死锁则连接失败。
- **就绪探针 (Readiness)**：`GET /readyz`  
  验证规则库加载状态、Profile 配置与运行时内存状态，全部正常后返回 `200 OK` 并挂载流量。

---

## 2. 运行时诊断与自检

通过请求诊断端点获取当前引擎核心状态：

```bash
curl -s http://127.0.0.1:8079/ops/diagnostics | jq .
```

返回报文包含：
- `rules_loaded`：已装载的 YAML 规则数；
- `standards_loaded`：已对齐的国标与地标列表；
- `ner_available`：ONNX 运行时与小模型就绪状态；
- `safety_floor_level`：安全底线兜底等级；
- `llm_circuit_state`：大模型仲裁熔断器状态（`Closed` / `Open` / `HalfOpen`）。

---

## 3. 指标监控与 Prometheus 抓取

引擎暴露标准 Prometheus 格式指标：

```bash
curl -s http://127.0.0.1:8079/metrics
```

关键业务监控指标：
- `privacy_classify_requests_total`：敏感数据分类分级请求总数；
- `privacy_mask_requests_total`：掩码脱敏调用频次；
- `privacy_dp_budget_consumed`：各租户差分隐私预算累计消耗值；
- `privacy_funnel_duration_seconds`：3 层漏斗各阶段流转耗时直方图。
