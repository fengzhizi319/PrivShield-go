// Observability hook for the canonical naming registry.
// canonical 注册表的可观测性钩子（api_rename_design.md §7.2）。
//
// ==============================================================================
// 【设计背景与核心价值】
// pkg/naming 是整个 PrivShield 体系处理跨服务业务标识（如 ds_yibao, api1_yibao）
// 的「唯一事实源 (SSOT)」与「唯一收口卡点 (Choke Point)」。
// 所有入站流量在服务边界（REST/gRPC/BFF）做别名归一化与合法性校验时，都必须经由此包。
//
// 因此，本文件通过 Observer 模式提供可观测性统一扩展点，避免各个微服务（service-hub,
// datasource-mgr, app-lz 等）在各自的 Handler 或中间件中重复编写指标埋点代码。
//
// ==============================================================================
// 【使用方法 / Usage Guide】
//
// 1. 服务启动时注册指标收集器（通常在 main.go 中一次性注册）：
//    ```go
//    mc := metrics.NewCollector("service-hub")
//    naming.SetObserver(mc) // *metrics.Collector 天然实现了 naming.Observer 接口
//    ```
//
// 2. 业务处理时正常调用 naming 解析方法即可（内部会自动触发指标统计）：
//    ```go
//    // 入站归一化（自动上报别名使用或未登记错误）
//    entry, err := naming.Normalize(rawInput)
//
//    // 任务派发/落库前综合校验（自动上报预留位 409 或格式错误）
//    dsID, err := naming.ResolveInbound(rawInput)
//    ```
//
// 3. 单元测试与无监控环境：
//    默认情况下 observer 为 nil，所有内部上报逻辑均为空操作（no-op），安全无开销且不会 panic。
//    单元测试中可通过 SetObserver 注入 mock/fake 实现捕获事件并断言。
// ==============================================================================

package naming

import "sync"

// Observer receives naming-resolution events. *metrics.Collector implements
// this interface directly.
//
// Observer 接口定义了标识解析与校验过程中产生的可观测性事件回调。
// 指标收集器（如 pkg/metrics.Collector）实现此接口，用于向 Prometheus 上报度量。
type Observer interface {
	// RecordAPIAlias reports that a non-canonical representation was used.
	//
	// RecordAPIAlias 上报调用方使用了非规范别名（如旧 slug、中文名、文件名或 api_code）。
	// 参数说明：
	//   - alias: 调用方实际传入的入站原始字符串（如 "yibao.csv"、"医保"、"api1_yibao"）
	//   - canonical: 映射解析后的权威规范标识（如 "ds_yibao"）
	//   - target: 别名所指向的目标维度类型（取值为 TargetDataSourceID / TargetAPICode / TargetPath）
	RecordAPIAlias(alias, canonical, target string)

	// RecordNormalizeError reports a normalization failure or write-side validation error.
	//
	// RecordNormalizeError 上报标识归一化失败或写侧校验被拒绝的事件。
	// 参数说明：
	//   - reason: 失败原因类别（取值为 ReasonUnknown / ReasonEmpty / ReasonReserved / ReasonFormatInvalid）
	// 注意：为防止高基数维度污染 Prometheus 时间序列，入站原始脏数据仅输出到日志，不作为指标标签。
	RecordNormalizeError(reason string)
}

// Metric label value constants / 指标标签取值常量。
// 严格控制标签基数（Low Cardinality），确保 Prometheus 时序数据库存储与查询性能。
const (
	// TargetDataSourceID 表示入站别名被解析映射到了 canonical datasource_id（如 "医保" -> "ds_yibao"）。
	TargetDataSourceID = "datasource_id"

	// TargetAPICode 表示入站值是业务 API 编码别名（如 "api1_yibao" -> "ds_yibao"）。
	TargetAPICode = "api_code"

	// TargetPath 表示入站信号来源于已废弃的历史 URL 路径端点（如 /api/v1/yibao）。
	TargetPath = "path"

	// ReasonUnknown 表示入站值在注册表中完全不存在，触发 Fail-Closed 阻断（HTTP 400）。
	ReasonUnknown = "unknown"

	// ReasonEmpty 表示入站标识为空字符串或纯空白字符（HTTP 400）。
	ReasonEmpty = "empty"

	// ReasonReserved 表示命中已登记但未开放/未实现的预留数据源（如 ds_mock3），写侧拒绝（HTTP 409）。
	ReasonReserved = "reserved"

	// ReasonFormatInvalid 表示字面格式不符合 ds_[a-z0-9_]+ 规范，通常说明调用方漏了边界归一化。
	ReasonFormatInvalid = "format_invalid"
)

var (
	// observerMu 保证 observer 全局实例在多协程并发注册、读取与测试恢复时的读写安全。
	observerMu sync.RWMutex
	// observer 保存当前全局注册的观测器实例；未注册时为 nil。
	observer Observer
)

// SetObserver registers the naming Observer. Passing nil clears it.
//
// SetObserver 注册全局命名注册表观测器。
// 执行逻辑：加写锁后赋值。传入 nil 表示注销/清空观测器，后续解析将退化为空操作。
func SetObserver(o Observer) {
	observerMu.Lock()
	defer observerMu.Unlock()
	observer = o
}

// CurrentObserver returns the registered observer (may be nil).
//
// CurrentObserver 获取当前生效的全局观测器。
// 执行逻辑：加读锁安全获取，供内部上报钩子或单元测试读取状态。
func CurrentObserver() Observer {
	observerMu.RLock()
	defer observerMu.RUnlock()
	return observer
}

// recordAlias notifies the observer about an alias representation.
//
// recordAlias 内部辅助函数：上报别名解析事件。
// 执行逻辑：
// 1. 调用 CurrentObserver() 获取观测器实例；
// 2. 若实例非 nil，则执行 RecordAPIAlias 回调递增 privshield_api_alias_requests_total 计数；
// 3. 若为 nil 则直接跳过，零性能损耗。
func recordAlias(alias, canonical, target string) {
	if o := CurrentObserver(); o != nil {
		o.RecordAPIAlias(alias, canonical, target)
	}
}

// recordNormalizeError notifies the observer about a normalization failure.
//
// recordNormalizeError 内部辅助函数：上报归一化或校验失败原因。
// 执行逻辑：
// 1. 调用 CurrentObserver() 获取观测器实例；
// 2. 若实例非 nil，则执行 RecordNormalizeError 回调递增 privshield_datasource_normalize_errors_total 计数；
// 3. 标签仅记录归一化的枚举原因（reason），杜绝脏字符串引入的高基数问题。
func recordNormalizeError(reason string) {
	if o := CurrentObserver(); o != nil {
		o.RecordNormalizeError(reason)
	}
}
