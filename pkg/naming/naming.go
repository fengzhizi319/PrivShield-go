// Package naming is the single source of truth for cross-service business
// identifiers: the canonical `api_code` / `datasource_id` registry and the
// inbound alias normalization rules.
//
// ==============================================================================
// 【架构设计背景与设计约束】
// Package naming 是 PrivShield 跨服务业务标识的唯一事实源 (Single Source of Truth, SSOT)：
// 权威管理 canonical `api_code` / `datasource_id` 注册表，以及入站别名的归一化规则。
//
// 设计约束（详细规范见 console/app-lz/docs/api_rename_design.md §5、§6）：
//  1. 【唯一标识原则】：一个数据源实体有且仅有一个 canonical datasource_id（如 ds_yibao）。
//     其简写 slug（yibao）、数据文件名（yibao.csv）、中文名称（医保/医保结算）、
//     业务 API 编码（api1_yibao）均为外部表现形式，只允许在系统服务边界（REST/gRPC/BFF 入口）
//     被归一化一次，内部各微服务与模块间流转一律强制使用 canonical 标识。
//  2. 【Fail-Closed 零逃逸原则】：归一化未登记的非法或未知入站值时，必须直接报错拒绝（写侧 400），
//     严禁静默回退或降级到任何默认数据源，彻底杜绝数据源错配与安全逃逸风险。
//  3. 【代码防腐原则】：全仓库业务代码中严禁出现任何裸数据源字符串字面量（如 "yibao"），
//     必须统一引用本包导出的命名常量（如 naming.DSYibao）或通过注册表方法动态查询。
// ==============================================================================

package naming

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// canonical api_code —— 业务 API 稳定唯一标识（面向外部业务方调用与协议契约）
const (
	// API1Yibao 医保结算数据接口契约标识
	API1Yibao = "api1_yibao"
	// API2Kangyang 康养健康档案接口契约标识
	API2Kangyang = "api2_kangyang"
)

// canonical datasource_id —— 数据源物理/逻辑实体唯一标识（面向内部存储、脱敏引擎与审计存证）
const (
	// DSYibao 柳州医保结算数据集（18 字段，对标 DB51/T 2989-2023）
	DSYibao = "ds_yibao"
	// DSKangyang 柳州康养体检与慢病数据集（27 字段）
	DSKangyang = "ds_kangyang"
	// DSMock3 预留政务数据源 3（占位已登记，写侧拒绝 409）
	DSMock3 = "ds_mock3"
	// DSMock4 预留企业/金融数据源 4（占位已登记，写侧拒绝 409）
	DSMock4 = "ds_mock4"
)

// Registry entry status values / 注册表条目生命周期状态。
const (
	// StatusActive 已正式上线实现，允许进行读写、派发与隐私脱敏调度
	StatusActive = "active"
	// StatusReserved 预留占位条目（尚未开放），读侧可查元数据，写侧派发时必须强制拒绝（HTTP 409 / Conflict）
	StatusReserved = "reserved"
)

// ID literal formats (api_rename_design.md §1.3).
// 正则表达式：严格限制标识符命名空间与字面格式，防止特殊字符注入与格式混淆。
var (
	// datasourceIDRe 规范：以 ds_ 开头，后接小写字母，总长度 5~34 字符（^ds_[a-z][a-z0-9_]{1,30}$）
	datasourceIDRe = regexp.MustCompile(`^ds_[a-z][a-z0-9_]{1,30}$`)
	// apiCodeRe 规范：以 api[1-9]_ 开头，后接小写字母，总长度 6~35 字符（^api[1-9]_[a-z][a-z0-9_]{1,30}$）
	apiCodeRe = regexp.MustCompile(`^api[1-9]_[a-z][a-z0-9_]{1,30}$`)
)

// ErrUnknownDataSource 表示入站标识无法映射到任何已登记的 canonical datasource_id。
// 语义说明：调用方传入了未知的数据源名称或别名。
// HTTP REST 应转换为 400 INVALID_DATASOURCE_ID；gRPC 应转换为 codes.InvalidArgument。
var ErrUnknownDataSource = errors.New("unknown datasource id")

// ErrReservedDataSource 表示条目虽已在注册表中登记，但属于未实现或暂未开放的预留位。
// 语义说明：写侧调度或任务创建时命中预留位应拒绝执行。
// HTTP REST 应转换为 409 RESERVED_DATASOURCE；gRPC 应转换为 codes.FailedPrecondition。
var ErrReservedDataSource = errors.New("reserved datasource")

// Entry 定义了 canonical 注册表中单个数据源的权威元数据结构。
type Entry struct {
	APICode      string            // 绑定的业务 API 契约编码（如 "api1_yibao"；预留位无绑定时为空串）
	DataSourceID string            // 权威唯一的数据源实体标识（如 "ds_yibao"）
	Seq          int               // 前端展示排序序号（仅用于 UI 列表展现，不参与底层路由判定）
	DisplayName  map[string]string // 国际化多语言展示名称（键为语言标签，如 "zh-CN" / "en-US"）
	Category     string            // 领域分类（"medical" 医疗 | "healthcare" 康养 | "reserved" 预留）
	FileName     string            // 样本/物理数据源文件名（仅作为展示、排查与日志辅助参考）
	FieldCount   int               // Schema 物理字段总数（如医保 18 字段，康养 27 字段）
	Aliases      []string          // 允许在系统边界自动归一化的入站别名池（支持 slug、文件名、中文名、分类名）
	Status       string            // 生命周期状态（StatusActive 正常服务 | StatusReserved 预留未开放）
}

// Registry 是全系统权威的数据源静态注册表切片，切片顺序即为默认展示顺序。
var Registry = []Entry{
	{
		APICode:      API1Yibao,
		DataSourceID: DSYibao,
		Seq:          1,
		DisplayName:  map[string]string{"zh-CN": "医保结算数据接口", "en-US": "Medical Insurance Settlement API"},
		Category:     "medical",
		FileName:     "yibao.csv",
		FieldCount:   18,
		Aliases:      []string{"yibao", "yibao.csv", "medical.csv", "医保", "医保数据", "医保数据库", "医保结算", "medical", "medical_insurance"},
		Status:       StatusActive,
	},
	{
		APICode:      API2Kangyang,
		DataSourceID: DSKangyang,
		Seq:          2,
		DisplayName:  map[string]string{"zh-CN": "康养健康档案接口", "en-US": "Elderly-Care Health Record API"},
		Category:     "healthcare",
		FileName:     "kangyang.csv",
		FieldCount:   27,
		Aliases:      []string{"kangyang", "kangyang.csv", "healthcare.csv", "康养", "康养数据", "康养数据库", "康养体检", "healthcare", "elderly_care"},
		Status:       StatusActive,
	},
	{
		DataSourceID: DSMock3,
		Seq:          3,
		DisplayName:  map[string]string{"zh-CN": "预留政务数据源 3", "en-US": "Reserved Municipal Dataset 3"},
		Category:     "reserved",
		FileName:     "mock3.csv",
		Aliases:      []string{"mock3", "mock3.csv", "政务", "政务数据", "政务数据源"},
		Status:       StatusReserved,
	},
	{
		DataSourceID: DSMock4,
		Seq:          4,
		DisplayName:  map[string]string{"zh-CN": "预留企业/金融数据源 4", "en-US": "Reserved Enterprise Dataset 4"},
		Category:     "reserved",
		FileName:     "mock4.csv",
		Aliases:      []string{"mock4", "mock4.csv", "企业", "金融", "企业数据", "金融数据"},
		Status:       StatusReserved,
	},
}

// 内部索引表（在包 init() 时单次全量构建，提供 O(1) 复杂度的并发只读高速查找）
var (
	byDataSourceID map[string]*Entry // canonical datasource_id -> *Entry 映射索引
	byAPICode      map[string]*Entry // canonical api_code -> *Entry 映射索引
	aliasIndex     map[string]*Entry // 别名（大小写敏感及小写规范化）-> *Entry 映射索引
	aliasConflicts []string          // 记录别名冲突冲突项（初始化时检测，正常必须为空）
)

// init 在包加载时完成双向索引构建与别名冲突防御性检测。
func init() {
	byDataSourceID = make(map[string]*Entry, len(Registry))
	byAPICode = make(map[string]*Entry, len(Registry))
	aliasIndex = make(map[string]*Entry, len(Registry)*4)

	for i := range Registry {
		e := &Registry[i]
		byDataSourceID[e.DataSourceID] = e
		if e.APICode != "" {
			byAPICode[e.APICode] = e
		}
		// 别名池注册：同时检测不同数据源之间是否意外声明了同名别名，防止歧义路由
		for _, a := range e.Aliases {
			if prev, ok := aliasIndex[a]; ok && prev.DataSourceID != e.DataSourceID {
				aliasConflicts = append(aliasConflicts, fmt.Sprintf("%q→%s|%s", a, prev.DataSourceID, e.DataSourceID))
			}
			aliasIndex[a] = e
		}
	}
}

// AliasConflicts 返回被多个不同数据源条目重复占用的冲突别名列表。
// 架构保障：正常情况下切片长度必须恒等于 0；CI 单测中会严格断言此函数，防止注册表被污染。
func AliasConflicts() []string { return aliasConflicts }

// EntryByDataSourceID 按 canonical datasource_id 执行 O(1) 精确查找。
// 使用方法：当已知入参为标准 canonical ID 时调用。
// 返回值：找到则返回 Entry 拷贝与 true；未找到返回空结构体与 false。
func EntryByDataSourceID(id string) (Entry, bool) {
	if e, ok := byDataSourceID[id]; ok {
		return *e, true
	}
	return Entry{}, false
}

// EntryByAPICode 按 canonical api_code 执行 O(1) 精确查找。
// 使用方法：根据契约接口编码（如 api1_yibao）获取对应的数据源定义。
// 返回值：找到则返回 Entry 拷贝与 true；未找到返回空结构体与 false。
func EntryByAPICode(code string) (Entry, bool) {
	if e, ok := byAPICode[code]; ok {
		return *e, true
	}
	return Entry{}, false
}

// Entries 返回全量注册表条目的深拷贝切片（保持展示顺序）。
// 使用方法：供管理看板、元数据探查及控制台获取完整数据源清单使用。
func Entries() []Entry {
	out := make([]Entry, len(Registry))
	copy(out, Registry)
	return out
}

// ActiveEntries 仅返回处于已上线激活状态（status == StatusActive）的数据源条目切片。
// 使用方法：供前端 UI 下拉菜单、数据流通选择器展示可用服务列表。
func ActiveEntries() []Entry {
	out := make([]Entry, 0, len(Registry))
	for _, e := range Registry {
		if e.Status == StatusActive {
			out = append(out, e)
		}
	}
	return out
}

// ActiveDataSourceIDs 返回所有处于激活状态的 canonical 数据源 ID 列表。
// 使用方法：用于接口校验错误信息中拼接 allowed 列表，指导客户端纠错。
func ActiveDataSourceIDs() []string {
	out := make([]string, 0, len(Registry))
	for _, e := range Registry {
		if e.Status == StatusActive {
			out = append(out, e.DataSourceID)
		}
	}
	return out
}

// AllDataSourceIDs 返回所有已在册的 canonical 数据源 ID 列表（包含预留位）。
func AllDataSourceIDs() []string {
	out := make([]string, 0, len(Registry))
	for _, e := range Registry {
		out = append(out, e.DataSourceID)
	}
	return out
}

// NormalizeDataSourceID 将任意合法的入站表现形式映射归一化为标准的 canonical datasource_id。
//
// 使用方法：
// 适用于所有接收外部请求的入口 Handler（如 URL Path、Query Param、JSON Body）。
//
// 执行逻辑：
// 1. 调用 Normalize(raw) 解析条目；
// 2. 若解析成功返回 e.DataSourceID；若失败返回包装了 ErrUnknownDataSource 的错误。
func NormalizeDataSourceID(raw string) (string, error) {
	e, err := Normalize(raw)
	if err != nil {
		return "", err
	}
	return e.DataSourceID, nil
}

// Normalize 是 NormalizeDataSourceID 的完整条目返回形式。
//
// 使用方法：
// 当调用方在归一化的同时，还需要获取该数据源关联的 api_code、多语言展示名或 Schema 字段数时使用。
//
// 执行逻辑与优先级规则：
// 1. 去除入站字符串首尾空白字符；若为空串，触发 recordNormalizeError(ReasonEmpty) 并报错；
// 2. 【第一优先级：Canonical ID 精确匹配】：若命中 canonical ID（如 "ds_yibao"），直接返回，不触发别名指标上报；
// 3. 【第二优先级：API Code 契约匹配】：若命中 api_code（如 "api1_yibao"），触发 recordAlias(..., TargetAPICode) 并返回；
// 4. 【第三优先级：别名池不区分大小写匹配】：尝试转全小写匹配别名索引（如 "YIBAO.CSV" -> "ds_yibao"），触发 recordAlias(..., TargetDataSourceID)；
// 5. 【第四优先级：别名池精确匹配】：尝试原样匹配（支持中文别名如 "医保"、"康养"），触发 recordAlias(..., TargetDataSourceID)；
// 6. 【Fail-Closed 拦截】：若以上全部未命中，触发 recordNormalizeError(ReasonUnknown)，返回带有可用列表指引的未知错误。
func Normalize(raw string) (*Entry, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		recordNormalizeError(ReasonEmpty)
		return nil, unknownError(raw)
	}
	if e, ok := byDataSourceID[v]; ok {
		return e, nil
	}
	if e, ok := byAPICode[v]; ok {
		recordAlias(v, e.DataSourceID, TargetAPICode)
		return e, nil
	}
	// ASCII aliases are case-insensitive; non-ASCII aliases match exactly.
	lowered := strings.ToLower(v)
	if e, ok := aliasIndex[lowered]; ok {
		recordAlias(v, e.DataSourceID, TargetDataSourceID)
		return e, nil
	}
	if e, ok := aliasIndex[v]; ok {
		recordAlias(v, e.DataSourceID, TargetDataSourceID)
		return e, nil
	}
	recordNormalizeError(ReasonUnknown)
	return nil, unknownError(raw)
}

// unknownError 构建携带可用列表的标准化错误，便于外层渲染 400 错误信封详情。
func unknownError(received string) error {
	return fmt.Errorf("%w: %q (allowed: %s)", ErrUnknownDataSource, received,
		strings.Join(ActiveDataSourceIDs(), ", "))
}

// IsUnknownDataSource 判断给定 error 是否由「未登记的未知数据源」引起。
// 使用方法：在 HTTP 中间件或错误信封转换中判断是否应输出 400 INVALID_DATASOURCE_ID。
func IsUnknownDataSource(err error) bool {
	return errors.Is(err, ErrUnknownDataSource)
}

// IsReserved 判断给定 error 是否由「命中已登记但未开放的预留数据源」引起。
// 使用方法：在任务创建或调度执行前判断是否应输出 409 RESERVED_DATASOURCE。
func IsReserved(err error) bool {
	return errors.Is(err, ErrReservedDataSource)
}

// CheckWritable 校验写侧（如任务落库、隐私调度、流水线派发）所使用的 canonical 数据源 ID。
//
// 使用方法：
// 在底层核心服务（如 service-hub、audit-log）内部执行写操作前进行防御性校验，入参要求必须已经是 canonical ID。
//
// 执行逻辑：
// 1. 查找 byDataSourceID 索引；
// 2. 若未查到：若字面格式不符合 ds_* 则上报 ReasonFormatInvalid（提示调用方缺少边界归一化）；否则上报 ReasonUnknown；
// 3. 若查到条目：调用 checkWritableEntry 校验状态是否为 StatusActive（若为预留位则上报 ReasonReserved 并返回错误）。
func CheckWritable(datasourceID string) error {
	e, ok := byDataSourceID[datasourceID]
	if !ok {
		// 进入「只接受 canonical 字面量」的校验器却不是合法格式，
		// 说明调用方漏了边界归一化，与「格式合法但未登记」是两类修复动作。
		if !ValidDataSourceIDFormat(datasourceID) {
			recordNormalizeError(ReasonFormatInvalid)
		} else {
			recordNormalizeError(ReasonUnknown)
		}
		return unknownError(datasourceID)
	}
	return checkWritableEntry(e)
}

// checkWritableEntry 校验已查到的数据源条目是否允许写操作。
func checkWritableEntry(e *Entry) error {
	if e.Status != StatusActive {
		recordNormalizeError(ReasonReserved)
		return fmt.Errorf("%w: %s", ErrReservedDataSource, e.DataSourceID)
	}
	return nil
}

// ResolveInbound 一步到位完成「入站归一化 + 写侧合法性校验」。
//
// 使用方法：
// 推荐作为所有接收外部请求并准备落库/派发任务的 Handler 统一入口（Best Practice）。
//
// 执行逻辑：
// 1. 调用 Normalize(raw) 将任意入站形式（别名、文件名、中文、api_code）归一化为标准 Entry；
// 2. 调用 checkWritableEntry(e) 校验该数据源是否为可写活跃状态；
// 3. 校验通过返回 canonical datasource_id，否则返回对应错误。此组合调用确保预留位指标仅被精准统计一次。
func ResolveInbound(raw string) (string, error) {
	e, err := Normalize(raw)
	if err != nil {
		return "", err
	}
	if err := checkWritableEntry(e); err != nil {
		return "", err
	}
	return e.DataSourceID, nil
}

// ValidDataSourceIDFormat 校验字符串是否完全符合 datasource_id 命名格式（^ds_[a-z][a-z0-9_]{1,30}$）。
func ValidDataSourceIDFormat(s string) bool { return datasourceIDRe.MatchString(s) }

// ValidAPICodeFormat 校验字符串是否完全符合 api_code 契约命名格式（^api[1-9]_[a-z][a-z0-9_]{1,30}$）。
func ValidAPICodeFormat(s string) bool { return apiCodeRe.MatchString(s) }

// APICodeForDataSource 查询指定 canonical 数据源绑定的 api_code。
// 若数据源不存在或属于预留位未绑定 API 则返回空字符串 ""。
func APICodeForDataSource(datasourceID string) string {
	if e, ok := byDataSourceID[datasourceID]; ok {
		return e.APICode
	}
	return ""
}

// DataSourceForAPICode 查询指定 api_code 对应的 canonical 数据源 ID。
// 若匹配成功返回 (datasource_id, true)；若未登记返回 ("", false)。
func DataSourceForAPICode(apiCode string) (string, bool) {
	if e, ok := byAPICode[apiCode]; ok {
		return e.DataSourceID, true
	}
	return "", false
}

// APICodes 按注册表展示顺序返回所有已登记的业务 api_code 切片。
func APICodes() []string {
	out := make([]string, 0, len(Registry))
	for _, e := range Registry {
		if e.APICode != "" {
			out = append(out, e.APICode)
		}
	}
	return out
}
