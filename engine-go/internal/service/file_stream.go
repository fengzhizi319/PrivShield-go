// Package service — 流式文件处理（恒定内存）
package service

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"

	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/masking"
)

// maxProcessFileBytes 单次文件处理的字节硬上限，与 REST 层 50MB multipart 上限对齐。
// 声明为变量仅为便于测试下调阈值以校验硬上限行为；生产路径不会修改。
var maxProcessFileBytes int64 = 50 << 20

const (
	// streamBatchSize 流式脱敏的恒定内存窗口：每批最多缓存的行数。
	streamBatchSize = 2048
	// streamParallelMinRows 批次行数达到该阈值才启用多核分块，低于则单趟串行。
	streamParallelMinRows = 512
	// streamMaxWorkers 流式脱敏并发上限。
	streamMaxWorkers = 16
)

// ErrFileTooLarge 流式读取超出字节硬上限，供 REST 层映射为 413。
var ErrFileTooLarge = fmt.Errorf("file exceeds %d bytes limit", maxProcessFileBytes)

// ProcessFileStream 以流式（恒定内存）方式解析数据文件并执行脱敏。
//
// 相比 ProcessFile 的「全量物化 → 全量解析 → 全量副本」三阶内存放大（峰值可达文件
// 体积的 4~6 倍），CSV / JSON 的 mask_dataframe 组合走逐行解码、逐行脱敏的单趟流水线，
// 峰值内存仅 O(批次窗口 + 结果集)。需要全局视野的算法（k_anonymize 的 Mondrian 划分）
// 与 XLSX 仍回退到 ProcessFile 既有语义。两条路径的输出结构与字段完全一致。
func (s *PrivacyService) ProcessFileStream(r io.Reader, filename, operation string, options map[string]interface{}) (map[string]interface{}, error) {
	name := strings.ToLower(filename)
	colsFilter := extractColumnFilter(options)

	switch {
	case strings.HasSuffix(name, ".csv") && operation == "mask_dataframe":
		return streamMaskCSV(r, colsFilter)
	case strings.HasSuffix(name, ".json") && operation == "mask_dataframe":
		return streamMaskJSON(r, colsFilter)
	default:
		content, err := io.ReadAll(io.LimitReader(r, maxProcessFileBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read file error: %w", err)
		}
		if int64(len(content)) > maxProcessFileBytes {
			return nil, ErrFileTooLarge
		}
		return s.ProcessFile(content, filename, operation, options)
	}
}

// extractColumnFilter 解析 options["columns"] 为列名集合过滤器。
// JSON 反序列化得到 []interface{}，Go 侧调用方可直接传 []string。
func extractColumnFilter(options map[string]interface{}) map[string]bool {
	colsFilter := make(map[string]bool)
	switch cols := options["columns"].(type) {
	case []interface{}:
		for _, c := range cols {
			colsFilter[fmt.Sprintf("%v", c)] = true
		}
	case []string:
		for _, c := range cols {
			colsFilter[c] = true
		}
	}
	return colsFilter
}

// cappedReader 为流式读取施加字节硬上限，防止超大文件绕过物化路径的容量校验。
type cappedReader struct {
	r    io.Reader
	n    int64
	over bool
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.over {
		return 0, ErrFileTooLarge
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > maxProcessFileBytes {
		c.over = true
	}
	return n, err
}

func streamResult(rows int, result interface{}) map[string]interface{} {
	return map[string]interface{}{
		"operation": "mask_dataframe",
		"rows_in":   rows,
		"rows_out":  rows,
		"result":    result,
	}
}

// streamMaskCSV 单趟流式读取 CSV 并按列脱敏，恒定内存窗口分批多核计算。
func streamMaskCSV(r io.Reader, colsFilter map[string]bool) (map[string]interface{}, error) {
	rd := &cappedReader{r: r}
	br := bufio.NewReader(rd)
	// 剥离 UTF-8 BOM，与 ProcessFile 物化路径语义一致
	if head, err := br.Peek(3); err == nil && string(head) == "\xef\xbb\xbf" {
		_, _ = br.Discard(3)
	}

	cr := csv.NewReader(br)
	headers, err := cr.Read()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("CSV file is empty")
		}
		if rd.over {
			return nil, ErrFileTooLarge
		}
		return nil, fmt.Errorf("CSV parse error: %w", err)
	}

	masked := make([]map[string]string, 0, 512)
	batch := make([][]string, 0, streamBatchSize)
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			if rd.over {
				return nil, ErrFileTooLarge
			}
			return nil, fmt.Errorf("CSV parse error: %w", err)
		}
		batch = append(batch, row)
		if len(batch) == streamBatchSize {
			masked = append(masked, maskCSVBatch(batch, headers, colsFilter)...)
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		masked = append(masked, maskCSVBatch(batch, headers, colsFilter)...)
	}
	if rd.over {
		return nil, ErrFileTooLarge
	}
	return streamResult(len(masked), masked), nil
}

// maskCSVBatch 将一批 CSV 行映射为列名→脱敏值的多核并发结果（按索引写回，无锁无竞争）。
func maskCSVBatch(rows [][]string, headers []string, colsFilter map[string]bool) []map[string]string {
	out := make([]map[string]string, len(rows))
	forEachChunked(len(rows), func(start, end int) {
		for i := start; i < end; i++ {
			row := rows[i]
			rec := make(map[string]string, len(headers))
			for j, h := range headers {
				v := ""
				if j < len(row) {
					v = row[j]
				}
				if len(colsFilter) == 0 || colsFilter[h] {
					v = masking.MaskValue(h, v)
				}
				rec[h] = v
			}
			out[i] = rec
		}
	})
	return out
}

// streamMaskJSON 以 json.Decoder 令牌流逐对象解码并脱敏，避免整档物化。
func streamMaskJSON(r io.Reader, colsFilter map[string]bool) (map[string]interface{}, error) {
	rd := &cappedReader{r: r}
	dec := json.NewDecoder(bufio.NewReader(rd))

	tok, err := dec.Token()
	if err != nil {
		if rd.over {
			return nil, ErrFileTooLarge
		}
		if err == io.EOF {
			return nil, fmt.Errorf("JSON parse error: unexpected end of JSON input")
		}
		return nil, fmt.Errorf("JSON parse error: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("JSON parse error: expected top-level array of objects")
	}

	masked := make([]map[string]string, 0, 512)
	batch := make([]map[string]interface{}, 0, streamBatchSize)
	for dec.More() {
		var raw map[string]interface{}
		if err := dec.Decode(&raw); err != nil {
			if rd.over {
				return nil, ErrFileTooLarge
			}
			return nil, fmt.Errorf("JSON parse error: %w", err)
		}
		batch = append(batch, raw)
		if len(batch) == streamBatchSize {
			masked = append(masked, maskJSONBatch(batch, colsFilter)...)
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		masked = append(masked, maskJSONBatch(batch, colsFilter)...)
	}
	// 消费闭合 ']' 并确认无尾部脏数据（与 json.Unmarshal 整档校验语义对齐）
	if _, err := dec.Token(); err != nil {
		if rd.over {
			return nil, ErrFileTooLarge
		}
		return nil, fmt.Errorf("JSON parse error: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		// 输入被硬上限截断时，长度错误优先于解析/尾部错误
		if rd.over {
			return nil, ErrFileTooLarge
		}
		return nil, fmt.Errorf("JSON parse error: unexpected trailing data")
	}
	if rd.over {
		return nil, ErrFileTooLarge
	}
	return streamResult(len(masked), masked), nil
}

// maskJSONBatch 一批 JSON 对象的多核并发字段脱敏。
func maskJSONBatch(rows []map[string]interface{}, colsFilter map[string]bool) []map[string]string {
	out := make([]map[string]string, len(rows))
	forEachChunked(len(rows), func(start, end int) {
		for i := start; i < end; i++ {
			raw := rows[i]
			rec := make(map[string]string, len(raw))
			for k, v := range raw {
				val := fmt.Sprintf("%v", v)
				if len(colsFilter) == 0 || colsFilter[k] {
					val = masking.MaskValue(k, val)
				}
				rec[k] = val
			}
			out[i] = rec
		}
	})
	return out
}

// forEachChunked 将 [0,n) 划分为至多 streamMaxWorkers 段连续区间并发执行 fn，
// 各段按索引写回互不重叠（无锁）；n 低于阈值时单趟串行，避免 goroutine 调度开销倒挂。
func forEachChunked(n int, fn func(start, end int)) {
	workers := 1
	if n >= streamParallelMinRows {
		workers = runtime.NumCPU()
		if workers > streamMaxWorkers {
			workers = streamMaxWorkers
		}
	}
	if workers <= 1 {
		fn(0, n)
		return
	}
	chunk := (n + workers - 1) / workers
	var wg sync.WaitGroup
	for start := 0; start < n; start += chunk {
		end := start + chunk
		if end > n {
			end = n
		}
		wg.Add(1)
		go func(s, e int) {
			defer wg.Done()
			fn(s, e)
		}(start, end)
	}
	wg.Wait()
}
