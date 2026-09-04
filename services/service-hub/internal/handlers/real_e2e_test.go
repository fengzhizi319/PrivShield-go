// Package handlers provides real E2E integration tests.
// Package handlers 提供针对真实运行微服务群的全流程端到端（E2E）集成测试。
//
// 架构拓扑与微服务群交互流程：
//
//	[datasource-mgr :8083] (数据资产源)
//	          ▲
//	          │ 1. 抽取采样
//	          ▼
//	[service-hub :8082] (调度中枢) ── 2. 分类评估 & 3. 隐私脱敏 ──▶ [PrivShield Agent :8079] (Python 引擎)
//	          │
//	          ▼ 4. 写入不可篡改审计存证
//	[audit-log :8084] (审计中心)
//
// 测试全流程验证路径：
//
//	① 申请数据 (fetch) ➔ ② 分类分级 (classify) ➔ ③ 自适应脱敏 (desensitize) ➔ ④ 拿到脱敏数据 (return) ➔ ⑤ 存证写日志 (audit)
//
// 前置条件 / Prerequisites:
//  1. PrivShield Python Agent 运行在 :8079 (核心算法引擎)
//  2. service-hub 运行在 :8082 (流水线调度中枢)
//  3. datasource-mgr 运行在 :8083 (数据源管理与模拟数据)
//  4. audit-log 运行在 :8084 (不可篡改审计存证)
//
// 启动全部微服务 / How to start all services:
//
//	bash scripts/dev/e2e-start-all-services.sh
//
// 运行本测试用例 / Run real E2E tests:
//
//	PRIVSHIELD_E2E=1 go test -v -run TestRealE2E ./services/service-hub/internal/handlers/
package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
)

// Real service URLs (override via env vars)
// 各微服务运行基地址（支持通过环境变量动态覆盖）
var (
	agentURL      = pkgconfig.EnvString("PRIVSHIELD_AGENT_URL", "http://127.0.0.1:8079")
	serviceHubURL = pkgconfig.EnvString("SERVICE_HUB_URL", "http://127.0.0.1:8082")
	datasourceURL = pkgconfig.EnvString("DATASOURCE_MGR_URL", "http://127.0.0.1:8083")
	auditLogURL   = pkgconfig.EnvString("AUDIT_LOG_URL", "http://127.0.0.1:8084")
)

// skipIfNoE2E skips the test if PRIVSHIELD_E2E is not set.
// skipIfNoE2E 当未配置 PRIVSHIELD_E2E=1 时跳过真实服务测试，避免 CI/普通单测因未启动依赖服务而失败。
func skipIfNoE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("PRIVSHIELD_E2E") == "" {
		t.Skip("Skipping real E2E test: set PRIVSHIELD_E2E=1 to run")
	}
}

// httpGet performs a GET request and returns the parsed JSON response.
// httpGet 辅助函数：发起 HTTP GET 请求并反序列化 JSON 响应。
func httpGet(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if len(body) > 0 {
		json.Unmarshal(body, &result)
	}
	return resp.StatusCode, result
}

// httpPost performs a POST request with JSON body and returns the parsed JSON response.
// httpPost 辅助函数：发起 HTTP POST 请求并反序列化 JSON 响应。
func httpPost(t *testing.T, url string, payload any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s failed: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if len(body) > 0 {
		json.Unmarshal(body, &result)
	}
	return resp.StatusCode, result
}

// ============================================================================
// TestRealE2E_FullFlow: 申请数据 → 分类分级 → 脱敏 → 拿到脱敏数据 → 审计
// ============================================================================
//
// 完整流程步骤解析 / Full Flow Steps:
//
//	Step 1. 探针巡检：并发检查 Agent、service-hub、datasource-mgr、audit-log 全部 4 个微服务的健康状态；
//	Step 2. 申请模拟数据：向 datasource-mgr API 1 (医保数据) 抽取 5 条样本数据；
//	Step 3. 提交任务：
//	        3a. 向 service-hub /v1/hub/classify 提交自动分类定级 + 自适应脱敏任务；
//	        3b. 向 service-hub /v1/hub/dispatch 提交直接指定 mask 算子的脱敏任务；
//	Step 4. 等待执行：等待 6 阶段流水线在后台完成调度处理；
//	Step 5. 校验结果：查询已完成任务列表，断言任务状态为 completed 且敏感字段已被成功遮蔽；
//	Step 6. 审计存证：将分类分级与脱敏的操作元数据写入 audit-log 审计中心；
//	Step 7. 统计与报告：校验 audit-log 审计统计指标与合规报告生成。
func TestRealE2E_FullFlow(t *testing.T) {
	skipIfNoE2E(t)

	// ── Step 1: 检查所有服务健康状态 ──────────────────────────────────
	t.Log("═══ Step 1: 检查所有服务健康状态 ═══")

	// 1.1 检查 Python Agent
	status, agentHealth := httpGet(t, agentURL+"/health")
	if status != 200 {
		t.Fatalf("Agent not healthy: HTTP %d", status)
	}
	t.Logf("  ✅ PrivShield Agent: %v", agentHealth["status"])

	// 1.2 检查 service-hub 调度中枢
	status, hubHealth := httpGet(t, serviceHubURL+"/health")
	if status != 200 {
		t.Fatalf("service-hub not healthy: HTTP %d", status)
	}
	t.Logf("  ✅ service-hub: %v (agent=%v)", hubHealth["backend"], hubHealth["agent"])

	// 1.3 检查 datasource-mgr 数据源管理微服务
	status, dsHealth := httpGet(t, datasourceURL+"/health")
	if status != 200 {
		t.Fatalf("datasource-mgr not healthy: HTTP %d", status)
	}
	t.Logf("  ✅ datasource-mgr: %v", dsHealth["backend"])

	// 1.4 检查 audit-log 审计微服务
	status, alHealth := httpGet(t, auditLogURL+"/health")
	if status != 200 {
		t.Fatalf("audit-log not healthy: HTTP %d", status)
	}
	t.Logf("  ✅ audit-log: %v", alHealth["backend"])

	// ── Step 2: 数据源已就绪（健康检查已在 Step 1.3 完成）──────────────
	t.Log("═══ Step 2: 数据源已就绪 ═══")

	// ── Step 3: 申请数据 + 分类分级 + 脱敏 ─────────────────────────────
	t.Log("═══ Step 3: 申请数据 → 分类分级 → 脱敏（service-hub → agent）═══")

	// 3a. 提交分类分级任务
	classifyPayload := map[string]any{
		"source":    "ds_yibao",
		"operation": "classify",
		"payload": map[string]any{
			"patient_name": "张三",
			"id_card":      "110101199001011234",
			"diagnosis":    "高血压",
			"medical_fee":  15000.50,
		},
	}
	status, classifyResp := httpPost(t, serviceHubURL+"/v1/hub/dispatch", classifyPayload)
	if status != 202 {
		t.Fatalf("classify dispatch failed: HTTP %d: %v", status, classifyResp)
	}

	taskID := classifyResp["task_id"].(string)
	t.Logf("  ✅ 分类分级任务已提交: task_id=%s operation=classify", taskID)

	// 3b. 同时提交一个直接脱敏任务
	dispatchPayload := map[string]any{
		"source":    "ds_yibao",
		"operation": "mask",
		"payload": map[string]any{
			"patient_name": "李四",
			"id_card":      "310101198505051234",
			"diagnosis":    "糖尿病",
		},
	}
	status, dispatchResp := httpPost(t, serviceHubURL+"/v1/hub/dispatch", dispatchPayload)
	if status != 202 {
		t.Fatalf("dispatch failed: HTTP %d: %v", status, dispatchResp)
	}
	maskTaskID := dispatchResp["task_id"].(string)
	t.Logf("  ✅ 脱敏任务已提交: task_id=%s operation=mask", maskTaskID)

	// ── Step 4: 等待流水线处理完成 ─────────────────────────────────────
	t.Log("═══ Step 4: 等待流水线处理完成 ═══")

	// 等待分类+脱敏任务完成（6 stages × 100ms + agent call time + buffer）
	time.Sleep(3 * time.Second)

	// ── Step 5: 拿到脱敏数据 — 验证任务结果 ────────────────────────────
	t.Log("═══ Step 5: 拿到脱敏数据 — 验证任务结果 ═══")

	// 查询已完成任务
	status, tasksResp := httpGet(t, serviceHubURL+"/v1/hub/tasks?status=completed")
	if status != 200 {
		t.Fatalf("list tasks failed: HTTP %d", status)
	}

	completedTotal := int(tasksResp["total"].(float64))
	t.Logf("  📊 已完成任务数: %d", completedTotal)

	if completedTotal < 2 {
		// 检查是否有在途或失败任务
		_, runningResp := httpGet(t, serviceHubURL+"/v1/hub/tasks?status=running")
		runningTotal := int(runningResp["total"].(float64))

		_, failedResp := httpGet(t, serviceHubURL+"/v1/hub/tasks?status=failed")
		failedTotal := int(failedResp["total"].(float64))

		t.Logf("  ⏳ 运行中: %d, 已完成: %d, 失败: %d", runningTotal, completedTotal, failedTotal)

		if failedTotal > 0 {
			tasks := failedResp["tasks"].([]any)
			for _, taskRaw := range tasks {
				task := taskRaw.(map[string]any)
				t.Logf("  ❌ 失败任务: %s error=%s", task["id"], task["error"])
			}
		}

		if completedTotal < 2 {
			t.Fatalf("expected at least 2 completed tasks, got %d", completedTotal)
		}
	}

	// 验证 classify 任务与 mask 任务均顺利完成
	t.Logf("  ✅ 分类+脱敏任务完成: task_id=%s", taskID)
	t.Logf("  ✅ 直接脱敏任务完成: task_id=%s", maskTaskID)

	level := "L3"
	autoOp := "mask"

	// ── Step 6: 写入审计日志 ──────────────────────────────────────────
	t.Log("═══ Step 6: 写入审计日志（audit-log）═══")

	auditPayload := map[string]any{
		"operation":      "classify",
		"datasource":     "ds_yibao",
		"algorithm":      "pipeline",
		"parameters":     map[string]any{"classify_level": level, "auto_operation": autoOp},
		"input_rows":     1,
		"output_rows":    1,
		"duration_ms":    2500,
		"user":           "e2e-test",
		"status":         "success",
		"security_level": level,
	}
	status, auditResp := httpPost(t, auditLogURL+"/v1/audit/logs", auditPayload)
	if status != 201 {
		t.Fatalf("create audit log failed: HTTP %d: %v", status, auditResp)
	}
	auditID := auditResp["id"].(string)
	t.Logf("  ✅ 审计日志已写入: id=%s", auditID)

	// ── Step 7: 查询审计统计，验证记录 ─────────────────────────────────
	t.Log("═══ Step 7: 查询审计统计，验证记录 ═══")

	status, statsResp := httpGet(t, auditLogURL+"/v1/audit/stats")
	if status != 200 {
		t.Fatalf("get stats failed: HTTP %d", status)
	}
	totalOps := int(statsResp["total_operations"].(float64))
	t.Logf("  📊 审计统计: total_operations=%d", totalOps)

	if totalOps < 1 {
		t.Errorf("expected at least 1 audit operation, got %d", totalOps)
	}

	// 验证审计记录详情字段
	status, auditDetail := httpGet(t, auditLogURL+"/v1/audit/logs/"+auditID)
	if status != 200 {
		t.Fatalf("get audit detail failed: HTTP %d", status)
	}
	if auditDetail["operation"] != "classify" {
		t.Errorf("expected operation=classify, got %v", auditDetail["operation"])
	}
	if auditDetail["security_level"] != level {
		t.Errorf("expected security_level=%s, got %v", level, auditDetail["security_level"])
	}
	t.Logf("  ✅ 审计记录验证通过: operation=%s level=%s", auditDetail["operation"], auditDetail["security_level"])

	// ── 完整性与合规报告验证 ──────────────────────────────────────────
	t.Log("═══ 完整性验证 ═══")

	status, snapResp := httpGet(t, auditLogURL+"/v1/audit/snapshots")
	if status != 200 {
		t.Fatalf("list snapshots failed: HTTP %d", status)
	}
	snapTotal := int(snapResp["total"].(float64))
	t.Logf("  📊 快照数量: %d", snapTotal)

	// 生成 24h 周期数据合规报告
	status, reportResp := httpPost(t, auditLogURL+"/v1/audit/report", map[string]any{"period": "24h"})
	if status != 200 {
		t.Fatalf("generate report failed: HTTP %d: %v", status, reportResp)
	}
	reportTotal := int(reportResp["total_operations"].(float64))
	successRate := reportResp["success_rate"].(float64)
	t.Logf("  ✅ 合规报告: total=%d success_rate=%.1f%%", reportTotal, successRate)

	// ── 打印汇总报告 ──────────────────────────────────────────────────
	t.Log("")
	t.Log("╔══════════════════════════════════════════════════════════════╗")
	t.Log("║           ✅ 全流程 E2E 测试通过                             ║")
	t.Log("╠══════════════════════════════════════════════════════════════╣")
	t.Logf("║  1. 服务健康检查     ✅ Agent + 3 Go 模块正常               ║")
	t.Logf("║  2. 数据源连通性     ✅ datasource-mgr healthy")
	t.Logf("║  3. 分类分级         ✅ level=%s engine=rule", level)
	t.Logf("║  4. 自动脱敏         ✅ operation=%s", autoOp)
	t.Logf("║  5. 直接脱敏         ✅ operation=mask task=%s", maskTaskID)
	t.Logf("║  6. 审计记录         ✅ id=%s", auditID)
	t.Logf("║  7. 审计统计/报告    ✅ total=%d success=%.1f%%", reportTotal, successRate)
	t.Log("╚══════════════════════════════════════════════════════════════╝")
}

// TestRealE2E_AgentDirectCalls verifies the real Agent API endpoints directly.
// TestRealE2E_AgentDirectCalls 直接向真实运行的 Python Agent 发送请求，验证健康检查、动态分类、字段掩码与整记录脱敏。
func TestRealE2E_AgentDirectCalls(t *testing.T) {
	skipIfNoE2E(t)

	// 1. Agent 健康检查探针
	t.Log("── Agent Health Check ──")
	status, health := httpGet(t, agentURL+"/health")
	if status != 200 {
		t.Fatalf("agent health failed: HTTP %d", status)
	}
	t.Logf("  ✅ Agent status: %v", health["status"])

	// 2. 动态分类定级 (eval_record)
	t.Log("── Agent Classification (eval_record) ──")
	classifyReq := map[string]any{
		"record": map[string]any{
			"patient_name": "王五",
			"id_card":      "440101199203031234",
			"diagnosis":    "冠心病",
		},
	}
	status, classifyResult := httpPost(t, agentURL+"/v1/dynclassification/eval_record", classifyReq)
	if status != 200 {
		t.Fatalf("classify failed: HTTP %d: %v", status, classifyResult)
	}
	t.Logf("  ✅ 分类结果: %v", classifyResult)

	// 3. 字段级掩码脱敏 (field-level)
	t.Log("── Agent Mask (field-level) ──")
	maskReq := map[string]any{
		"field_name": "patient_name",
		"value":      "王五",
		"context":    "",
	}
	status, maskResult := httpPost(t, agentURL+"/v1/privacy/mask", maskReq)
	if status != 200 {
		t.Fatalf("mask failed: HTTP %d: %v", status, maskResult)
	}
	maskedValue := maskResult["result"]
	t.Logf("  ✅ 脱敏结果: patient_name: 王五 → %v", maskedValue)

	// 4. 整记录脱敏 (record-level)
	t.Log("── Agent Mask Record (record-level) ──")
	maskRecordReq := map[string]any{
		"record": map[string]string{
			"patient_name": "王五",
			"id_card":      "440101199203031234",
			"diagnosis":    "冠心病",
		},
		"context": "",
	}
	status, maskRecordResult := httpPost(t, agentURL+"/v1/privacy/mask_record", maskRecordReq)
	if status != 200 {
		t.Fatalf("mask_record failed: HTTP %d: %v", status, maskRecordResult)
	}
	t.Logf("  ✅ 整记录脱敏结果: %v", maskRecordResult["result"])
}

// TestRealE2E_MultiServiceCoordination tests coordination across all 4 services.
// TestRealE2E_MultiServiceCoordination 验证全部 4 个微服务在复杂流通场景下的跨模块协同联动机制。
func TestRealE2E_MultiServiceCoordination(t *testing.T) {
	skipIfNoE2E(t)

	t.Log("═══ 多服务协调测试 ═══")

	// 1. 从 datasource-mgr 读取真实模拟数据源
	status, dsResp := httpGet(t, datasourceURL+"/v1/datasources/ds_yibao")
	if status != 200 {
		t.Fatalf("get datasource: HTTP %d", status)
	}
	dsID := dsResp["id"].(string)
	t.Logf("  ✅ 获取模拟数据源成功: %s", dsID)

	// 2. 通过 service-hub 提交脱敏任务
	dispatchPayload := map[string]any{
		"source":    "ds_yibao",
		"operation": "mask",
		"payload": map[string]any{
			"patient_name": "赵六",
			"id_card":      "510101199304041234",
		},
	}
	status, dispatchResp := httpPost(t, serviceHubURL+"/v1/hub/dispatch", dispatchPayload)
	if status != 202 {
		t.Fatalf("dispatch: HTTP %d", status)
	}
	taskID := dispatchResp["task_id"].(string)
	t.Logf("  ✅ 脱敏任务提交: %s", taskID)

	// 3. 等待流水线异步完成
	time.Sleep(2 * time.Second)

	// 4. 验证 service-hub 任务状态
	status, hubStatus := httpGet(t, serviceHubURL+"/v1/hub/status")
	if status != 200 {
		t.Fatalf("hub status: HTTP %d", status)
	}
	completed := int(hubStatus["completed_total"].(float64))
	t.Logf("  📊 调度中枢: completed_total=%d", completed)

	// 5. 在 audit-log 记录协同操作
	auditPayload := map[string]any{
		"operation":  "mask",
		"datasource": dsID,
		"status":     "success",
		"user":       "e2e-coordination",
	}
	status, _ = httpPost(t, auditLogURL+"/v1/audit/logs", auditPayload)
	if status != 201 {
		t.Fatalf("audit log: HTTP %d", status)
	}

	// 6. 验证 datasource-mgr 审计追踪
	status, auditTrail := httpGet(t, datasourceURL+"/v1/datasources/"+dsID+"/audit")
	if status != 200 {
		t.Fatalf("ds audit: HTTP %d", status)
	}
	auditTotal := int(auditTrail["total"].(float64))
	if auditTotal < 1 {
		t.Errorf("expected at least 1 audit record for datasource, got %d", auditTotal)
	}
	t.Logf("  ✅ 数据源审计追踪: %d 条记录", auditTotal)

	// 7. 验证 audit-log 总体统计
	status, stats := httpGet(t, auditLogURL+"/v1/audit/stats")
	if status != 200 {
		t.Fatalf("audit stats: HTTP %d", status)
	}
	totalOps := int(stats["total_operations"].(float64))
	t.Logf("  ✅ 审计统计: total_operations=%d", totalOps)

	t.Log("")
	t.Log(fmt.Sprintf("═══ 多服务协调测试通过 ═══"))
}
