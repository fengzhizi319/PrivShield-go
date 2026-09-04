"""PrivShield 网关 HTTP 转发与负载均衡使用示例 (Python Client)。

本脚本演示通过 Python httpx 客户端访问运行中的 PrivShield Go 网关 (:8000)，
涵盖网关健康检查、后端节点拓扑查询、数据脱敏代理转发及 Prometheus 指标采集。

前置条件：
    1. 启动 Go Agent:   go run ./engine-go/cmd/privshield-agent
    2. 启动 Go Gateway: bash ./scripts/dev/go-gateway-start.sh

运行方式：
    python docs/gateway_balancer/examples/gateway_usage.py
"""

import sys
import httpx


def main() -> None:
    gateway_url = "http://127.0.0.1:8000"
    client = httpx.Client(base_url=gateway_url, timeout=5.0)

    print("=== 1. 查询网关自身健康状态 (GET /health) ===")
    try:
        resp = client.get("/health")
        print(f"Status: {resp.status_code}, Body: {resp.json()}")
        assert resp.status_code == 200
        assert resp.json().get("status") == "ok"
    except Exception as e:
        print(f"⚠️ 无法连接到网关 (http://127.0.0.1:8000): {e}")
        print("请确认网关已启动: bash ./scripts/dev/go-gateway-start.sh")
        sys.exit(1)

    print("\n=== 2. 查询后端节点拓扑与 EWMA 状态 (GET /gateway/backends) ===")
    resp = client.get("/gateway/backends")
    print(f"Status: {resp.status_code}, Body: {resp.json()}")
    assert resp.status_code == 200

    print("\n=== 3. 通过网关调用后端隐私脱敏接口 (POST /v1/privacy/mask) ===")
    payload = {
        "field": "id_card",
        "value": "110101199003072345",
        "type": "id_card",
    }
    resp = client.post("/v1/privacy/mask", json=payload)
    print(f"Status: {resp.status_code}, Body: {resp.json()}")
    assert resp.status_code == 200
    result = resp.json()
    assert "masked" in result or "result" in result
    print(f"  ✅ 脱敏成功: 110101199003072345 -> {result.get('masked', result.get('result'))}")

    print("\n=== 4. 采集 Prometheus 监控指标 (GET /metrics) ===")
    resp = client.get("/metrics")
    print(f"Status: {resp.status_code}, Metric size: {len(resp.text)} bytes")
    assert resp.status_code == 200
    assert "gateway_requests_total" in resp.text or "go_goroutines" in resp.text

    print("\n🎉 所有示例步骤执行成功！")


if __name__ == "__main__":
    main()
