package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// 网关 REST API 快速调用示例 (Go Client)
//
// 前置条件：
// 启动 Go Agent: go run ./engine-go/cmd/privshield-agent
// 启动 Go Gateway: bash ./scripts/dev/go-gateway-start.sh
func main() {
	gatewayBaseURL := "http://127.0.0.1:8000"
	client := &http.Client{Timeout: 5 * time.Second}

	fmt.Println("=== 1. 查询网关自身健康状态 ===")
	resp, err := client.Get(gatewayBaseURL + "/health")
	if err != nil {
		fmt.Printf("健康检查请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Status: %d, Response: %s\n\n", resp.StatusCode, string(body))

	fmt.Println("=== 2. 查询网关后端节点拓扑与 EWMA 状态 ===")
	resp, err = client.Get(gatewayBaseURL + "/gateway/backends")
	if err != nil {
		fmt.Printf("后端查询失败: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	fmt.Printf("Status: %d, Response: %s\n\n", resp.StatusCode, string(body))

	fmt.Println("=== 3. 通过网关调用后端脱敏接口 (POST /v1/privacy/mask) ===")
	maskPayload := map[string]string{
		"field": "id_card",
		"value": "110101199003072345",
		"type":  "id_card",
	}
	jsonBytes, _ := json.Marshal(maskPayload)
	resp, err = client.Post(gatewayBaseURL+"/v1/privacy/mask", "application/json", bytes.NewReader(jsonBytes))
	if err != nil {
		fmt.Printf("脱敏请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	fmt.Printf("Status: %d, Response: %s\n\n", resp.StatusCode, string(body))

	fmt.Println("=== 4. 获取网关 Prometheus 监控指标 (GET /metrics) ===")
	resp, err = client.Get(gatewayBaseURL + "/metrics")
	if err != nil {
		fmt.Printf("指标获取失败: %v\n", err)
		return
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	fmt.Printf("Status: %d, Metrics Size: %d bytes\n", resp.StatusCode, len(body))
	fmt.Println("示例调用全部完成！")
}
