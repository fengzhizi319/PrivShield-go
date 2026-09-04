# 代理转发与负载均衡网关使用示例 (Usage & Integration Examples)

> 本文档提供 `PrivShield` Go 云原生网关的各种启动方式、cURL / grpcurl 交互命令以及 Go / Python 客户端调用示例。

---

## 1. 命令行启动方式

### 1.1 使用开发启动脚本（最简推荐）

```bash
cd /path/to/PrivShield
bash ./scripts/dev/go-gateway-start.sh
```

脚本将以默认配置启动网关：
- HTTP 代理：`http://127.0.0.1:8000`
- gRPC 代理：`127.0.0.1:50000`
- 后端 Agent：`127.0.0.1:8079`
- 调度策略：`p2c` (Power of Two Choices + EWMA)

---

### 1.2 使用环境变量指定多节点与策略

```bash
cd /path/to/PrivShield

# 指定多后端节点（逗号分隔）与平滑加权轮询策略
export GATEWAY_HOST=0.0.0.0
export GATEWAY_PORT=8000
export GATEWAY_GRPC_PORT=50000
export GATEWAY_BACKENDS="10.0.1.10:8079,10.0.1.11:8079,10.0.1.12:8079"
export GATEWAY_STRATEGY="p2c"
export PRIVACY_LOG_LEVEL="INFO"

# 方式 1: 直接编译运行
go run ./engine-go/cmd/privshield-gateway

# 方式 2: 使用预编译二进制
./bin/privshield-gateway
```

---

### 1.3 生产环境 systemd 守护进程配置

创建 `/etc/systemd/system/privshield-gateway.service`：

```ini
[Unit]
Description=PrivShield L7 Adaptive Gateway
After=network.target

[Service]
Type=simple
User=privshield
WorkingDirectory=/opt/privshield
Environment=GATEWAY_HOST=0.0.0.0
Environment=GATEWAY_PORT=8000
Environment=GATEWAY_GRPC_PORT=50000
Environment=GATEWAY_BACKENDS=127.0.0.1:8079,127.0.0.1:8080
Environment=GATEWAY_STRATEGY=p2c
Environment=PRIVACY_LOG_LEVEL=INFO
ExecStart=/opt/privshield/bin/privshield-gateway
Restart=always
RestartSec=5s
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

---

## 2. cURL REST API 调用示例

### 2.1 网关健康检查

```bash
curl -s http://127.0.0.1:8000/health | jq
```

响应：
```json
{
  "status": "ok",
  "component": "gateway"
}
```

---

### 2.2 查看后端拓扑与 EWMA 延迟

```bash
curl -s http://127.0.0.1:8000/gateway/backends | jq
```

响应：
```json
{
  "backends": [
    {
      "address": "127.0.0.1:8079",
      "in_flight": 0,
      "ewma_ms": 0.85,
      "cb_state": "closed"
    }
  ]
}
```

---

### 2.3 通过网关调用敏感数据脱敏

```bash
curl -s -X POST http://127.0.0.1:8000/v1/privacy/mask \
  -H "Content-Type: application/json" \
  -d '{
    "field": "id_card",
    "value": "110101199003072345",
    "type": "id_card"
  }' | jq
```

响应：
```json
{
  "field": "id_card",
  "masked": "110101********2345",
  "result": "110101********2345"
}
```

---

### 2.4 通过网关执行差分隐私加噪求和 (DP Sum)

```bash
curl -s -X POST http://127.0.0.1:8000/v1/privacy/dp/sum \
  -H "Content-Type: application/json" \
  -d '{
    "values": [10.5, 20.3, 15.8, 30.2],
    "epsilon": 1.0,
    "delta": 1e-5
  }' | jq
```

---

### 2.5 采集网关 Prometheus 指标

```bash
curl -s http://127.0.0.1:8000/metrics | grep gateway_
```

输出示例：
```text
# HELP gateway_requests_total Total number of HTTP requests processed by gateway.
# TYPE gateway_requests_total counter
gateway_requests_total{backend="127.0.0.1:8079",status="200"} 154
# HELP gateway_backend_inflight_requests Current in-flight requests to backend.
# TYPE gateway_backend_inflight_requests gauge
gateway_backend_inflight_requests{backend="127.0.0.1:8079",node="127.0.0.1:8079"} 0
# HELP gateway_backend_ewma_latency_seconds EWMA latency to backend.
# TYPE gateway_backend_ewma_latency_seconds gauge
gateway_backend_ewma_latency_seconds{backend="127.0.0.1:8079"} 0.00085
```

---

## 3. grpcurl gRPC 透明代理调用示例

网关 `:50000` 端口支持 gRPC 全双向透明流转发：

```bash
# 1. 检查后端 Agent 的 Health 状态（通过网关 :50000 转发）
grpcurl -plaintext 127.0.0.1:50000 grpc.health.v1.Health/Check

# 2. 调用 PrivacyService.Mask 进行字段脱敏
grpcurl -plaintext -d '{
  "field_name": "patient_name",
  "value": "张三"
}' 127.0.0.1:50000 proto.PrivacyService/Mask
```

---

## 4. Go 客户端调用示例

代码存放在 [`docs/gateway_balancer/examples/gateway_usage.go`](./examples/gateway_usage.go)：

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func main() {
	gatewayURL := "http://127.0.0.1:8000"
	client := &http.Client{Timeout: 5 * time.Second}

	// 1. 查询健康状态
	resp, _ := client.Get(gatewayURL + "/health")
	defer resp.Body.Close()

	// 2. 调用脱敏
	payload, _ := json.Marshal(map[string]string{
		"field": "phone",
		"value": "13812345678",
		"type":  "phone",
	})
	resp, _ = client.Post(gatewayURL+"/v1/privacy/mask", "application/json", bytes.NewReader(payload))
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("脱敏结果: %s\n", string(body))
}
```

运行：
```bash
go run docs/gateway_balancer/examples/gateway_usage.go
```

---

## 5. Python 客户端调用示例

代码存放在 [`docs/gateway_balancer/examples/gateway_usage.py`](./examples/gateway_usage.py)：

```python
import httpx

client = httpx.Client(base_url="http://127.0.0.1:8000", timeout=5.0)

# 1. 健康检查
resp = client.get("/health")
print("Health:", resp.json())

# 2. 字段脱敏
resp = client.post("/v1/privacy/mask", json={
    "field": "id_card",
    "value": "110101199003072345",
    "type": "id_card",
})
print("Masked:", resp.json())
```

运行：
```bash
python docs/gateway_balancer/examples/gateway_usage.py
```