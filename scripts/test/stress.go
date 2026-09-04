// Package main 提供纯 Go 实现的高并发极限吞吐压测与 SLA 延迟基准评估工具。
//
// 用法示例：
//   go run scripts/test/stress.go -target agent -c 50 -d 10
//   go run scripts/test/stress.go -url http://127.0.0.1:8080/v1/privacy/mask -c 100 -d 15
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	target := flag.String("target", "agent", "压测目标: agent (算力层脱敏) 或 hub (调度中枢)")
	targetURL := flag.String("url", "", "自定义目标 HTTP 端点 URL (覆盖 target 默认值)")
	concurrency := flag.Int("c", 50, "并发 Worker 协程数")
	durationSec := flag.Int("d", 10, "压测持续时长 (秒)")
	flag.Parse()

	url := *targetURL
	var payload []byte

	if url == "" {
		switch *target {
		case "agent":
			url = "http://127.0.0.1:8079/v1/privacy/mask"
			reqBody := map[string]interface{}{
				"field_type": "phone",
				"value":      "13812345678",
			}
			payload, _ = json.Marshal(reqBody)
		case "hub":
			url = "http://127.0.0.1:8082/api/hub/dispatch"
			reqBody := map[string]interface{}{
				"api_code":      "api1_yibao",
				"datasource_id": "ds_yibao",
				"operation":     "mask",
				"payload": []map[string]string{
					{"name": "张三", "phone": "13812345678", "id_card_no": "110101199001011234"},
				},
			}
			payload, _ = json.Marshal(reqBody)
		default:
			url = "http://127.0.0.1:8079/v1/privacy/mask"
			reqBody := map[string]interface{}{"field_type": "phone", "value": "13812345678"}
			payload, _ = json.Marshal(reqBody)
		}
	} else {
		reqBody := map[string]interface{}{"field_type": "phone", "value": "13812345678"}
		payload, _ = json.Marshal(reqBody)
	}

	fmt.Printf("\n=======================================================\n")
	fmt.Printf("🚀 PrivShield Go 极限高并发压测套件启动\n")
	fmt.Printf("目标 URL:     %s\n", url)
	fmt.Printf("并发 Worker:  %d\n", *concurrency)
	fmt.Printf("压测持续时间: %d 秒\n", *durationSec)
	fmt.Printf("=======================================================\n\n")

	tr := &http.Transport{
		MaxIdleConns:        *concurrency * 4,
		MaxIdleConnsPerHost: *concurrency * 4,
		MaxConnsPerHost:     *concurrency * 4,
		IdleConnTimeout:     30 * time.Second,
		DisableKeepAlives:   false,
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   10 * time.Second,
	}

	// 预热连接
	_, _ = client.Post(url, "application/json", bytes.NewReader(payload))

	var totalSuccess int64
	var totalFailed int64
	var mu sync.Mutex
	var latencies []float64

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*durationSec)*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	startTime := time.Now()

	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localLatencies := make([]float64, 0, 1000)

			for {
				select {
				case <-ctx.Done():
					mu.Lock()
					latencies = append(latencies, localLatencies...)
					mu.Unlock()
					return
				default:
					t0 := time.Now()
					req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
					if err != nil {
						atomic.AddInt64(&totalFailed, 1)
						continue
					}
					req.Header.Set("Content-Type", "application/json")

					resp, err := client.Do(req)
					latMs := float64(time.Since(t0).Microseconds()) / 1000.0
					localLatencies = append(localLatencies, latMs)

					if err != nil {
						atomic.AddInt64(&totalFailed, 1)
					} else {
						io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
						if resp.StatusCode >= 200 && resp.StatusCode < 300 {
							atomic.AddInt64(&totalSuccess, 1)
						} else {
							atomic.AddInt64(&totalFailed, 1)
						}
					}
				}
			}
		}()
	}

	wg.Wait()
	totalElapsed := time.Since(startTime).Seconds()
	totalReqs := atomic.LoadInt64(&totalSuccess) + atomic.LoadInt64(&totalFailed)

	if totalReqs == 0 {
		fmt.Println("未完成任何有效请求")
		return
	}

	sort.Float64s(latencies)
	qps := float64(totalReqs) / totalElapsed
	succRate := float64(totalSuccess) / float64(totalReqs) * 100.0
	failRate := float64(totalFailed) / float64(totalReqs) * 100.0

	var sumLat float64
	for _, l := range latencies {
		sumLat += l
	}
	avgLat := sumLat / float64(len(latencies))

	p50 := getPercentile(latencies, 50.0)
	p90 := getPercentile(latencies, 90.0)
	p95 := getPercentile(latencies, 95.0)
	p99 := getPercentile(latencies, 99.0)

	fmt.Printf("============================================================\n")
	fmt.Printf("  PrivShield 极限压测报告 (Target: %s)\n", *target)
	fmt.Printf("============================================================\n")
	fmt.Printf("  并发连接数:   %d\n", *concurrency)
	fmt.Printf("  压测时长:     %.2f 秒\n", totalElapsed)
	fmt.Printf("  总请求数:     %d 次\n", totalReqs)
	fmt.Printf("  成功请求:     %d 次 (%.2f%%)\n", totalSuccess, succRate)
	fmt.Printf("  失败请求:     %d 次 (%.2f%%)\n", totalFailed, failRate)
	fmt.Printf("  系统吞吐率:   %.2f QPS\n", qps)
	fmt.Printf("------------------------------------------------------------\n")
	fmt.Printf("  平均耗时:     %.2f ms\n", avgLat)
	fmt.Printf("  中位数 (P50): %.2f ms\n", p50)
	fmt.Printf("  P90 延迟:     %.2f ms\n", p90)
	fmt.Printf("  P95 延迟:     %.2f ms\n", p95)
	fmt.Printf("  P99 延迟:     %.2f ms\n", p99)
	fmt.Printf("============================================================\n\n")
}

func getPercentile(data []float64, pct float64) float64 {
	if len(data) == 0 {
		return 0
	}
	idx := int(float64(len(data)-1) * (pct / 100.0))
	if idx >= len(data) {
		idx = len(data) - 1
	}
	return data[idx]
}
