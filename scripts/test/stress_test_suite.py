#!/usr/bin/env python3
"""PrivShield 高并发极限性能压测与 SLA 延迟基准评估套件 (Stress & Benchmark Test Suite).

用法示例：
    # 压测 Agent 算力层 (默认 50 并发, 持续 10 秒)
    python scripts/test/stress_test_suite.py --target agent --concurrency 50 --duration 10

    # 压测 Service Hub 调度中枢
    python scripts/test/stress_test_suite.py --target hub --concurrency 30 --duration 10

    # 压测多节点网关负载均衡器
    python scripts/test/stress_test_suite.py --url http://127.0.0.1:8080/v1/privacy/mask --concurrency 100
"""

from __future__ import annotations

import argparse
import asyncio
import time
import httpx


def percentile(data: list[float], pct: float) -> float:
    """计算百分位数 (0.0 - 100.0)。"""
    if not data:
        return 0.0
    sorted_data = sorted(data)
    k = (len(sorted_data) - 1) * (pct / 100.0)
    f = int(k)
    c = f + 1
    if c < len(sorted_data):
        d0 = sorted_data[f] * (c - k)
        d1 = sorted_data[c] * (k - f)
        return d0 + d1
    return sorted_data[-1]


async def worker(
    client: httpx.AsyncClient,
    url: str,
    payload: dict,
    stop_event: asyncio.Event,
    latencies: list[float],
    status_counts: dict[int, int],
) -> None:
    """单个压测 Worker 协程。"""
    while not stop_event.is_set():
        t0 = time.perf_counter()
        try:
            resp = await client.post(url, json=payload, timeout=10.0)
            latency_ms = (time.perf_counter() - t0) * 1000.0
            latencies.append(latency_ms)
            status_counts[resp.status_code] = status_counts.get(resp.status_code, 0) + 1
        except Exception:
            latency_ms = (time.perf_counter() - t0) * 1000.0
            latencies.append(latency_ms)
            status_counts[0] = status_counts.get(0, 0) + 1


async def run_stress_test(
    url: str,
    payload: dict,
    concurrency: int,
    duration: int,
) -> None:
    """执行高并发压力测试并输出结构化统计。"""
    print(f"\n=======================================================")
    print(f"🚀 PrivShield 极限并发性能压测启动")
    print(f"目标 URL:     {url}")
    print(f"并发 Worker:  {concurrency}")
    print(f"压测持续时间: {duration} 秒")
    print(f"=======================================================\n")

    latencies: list[float] = []
    status_counts: dict[int, int] = {}
    stop_event = asyncio.Event()

    limits = httpx.Limits(max_keepalive_connections=concurrency * 2, max_connections=concurrency * 4)
    async with httpx.AsyncClient(limits=limits, timeout=10.0) as client:
        # Warmup connection
        try:
            await client.post(url, json=payload, timeout=5.0)
        except Exception:
            pass

        tasks = [
            asyncio.create_task(
                worker(client, url, payload, stop_event, latencies, status_counts)
            )
            for _ in range(concurrency)
        ]

        t_start = time.perf_counter()
        await asyncio.sleep(duration)
        stop_event.set()
        await asyncio.gather(*tasks, return_exceptions=True)
        t_total = time.perf_counter() - t_start

    total_requests = len(latencies)
    if total_requests == 0:
        print("❌ 未完成任何请求，请检查目标服务是否在线！")
        return

    qps = total_requests / t_total
    success_count = sum(cnt for code, cnt in status_counts.items() if 200 <= code < 400)
    success_rate = (success_count / total_requests) * 100.0

    p50 = percentile(latencies, 50.0)
    p90 = percentile(latencies, 90.0)
    p95 = percentile(latencies, 95.0)
    p99 = percentile(latencies, 99.0)
    max_lat = max(latencies) if latencies else 0.0

    print("📊 ── 压测性能指标统计 (Performance Benchmark SLA) ──")
    print(f"总耗时:          {t_total:.2f} 秒")
    print(f"完成请求总量:    {total_requests} 次")
    print(f"平均 QPS 吞吐:   {qps:.2f} req/s")
    print(f"请求成功率:      {success_rate:.2f}% (成功: {success_count}, 异常: {total_requests - success_count})")
    print(f"状态码分布:      {dict(sorted(status_counts.items()))}")
    print(f"\n⏱️ ── 延迟分位数 SLA 耗时 (Latency Percentiles) ──")
    print(f"P50 (中位数):    {p50:.2f} ms")
    print(f"P90:             {p90:.2f} ms")
    print(f"P95:             {p95:.2f} ms")
    print(f"P99:             {p99:.2f} ms")
    print(f"Max (最大延迟):  {max_lat:.2f} ms")
    print(f"=======================================================\n")


def main() -> None:
    parser = argparse.ArgumentParser(description="PrivShield 生产级极限性能压测套件")
    parser.add_argument("--target", choices=["agent", "hub", "gateway"], default="agent", help="预置目标服务")
    parser.add_argument("--url", default="", help="自定义目标端点 URL")
    parser.add_argument("--concurrency", type=int, default=30, help="并发 Worker 数量")
    parser.add_argument("--duration", type=int, default=5, help="持续压测时间 (秒)")

    args = parser.parse_args()

    # 默认 payload 与 url
    if args.url:
        target_url = args.url
        payload = {"data": "110101199003072345", "method": "mask"}
    elif args.target == "agent":
        target_url = "http://127.0.0.1:8079/v1/privacy/mask"
        payload = {
            "text": "患者张三，身份证号110101199003072345，电话13812345678",
            "method": "mask",
            "field_name": "id_card",
        }
    elif args.target == "hub":
        target_url = "http://127.0.0.1:8082/v1/hub/dispatch"
        payload = {
            "source": "ds_yibao",
            "operation": "mask",
            "priority": "high",
            "payload": {"name": "张三", "id_card": "110101199003072345"},
        }
    else:  # gateway
        target_url = "http://127.0.0.1:8000/v1/privacy/mask"
        payload = {"text": "测试数据", "method": "mask"}

    asyncio.run(run_stress_test(target_url, payload, args.concurrency, args.duration))


if __name__ == "__main__":
    main()
