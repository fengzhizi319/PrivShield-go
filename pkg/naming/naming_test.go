// Package naming 单元测试套件
//
// ==============================================================================
// 【测试套件设计目标与架构守卫机制】
// 本测试文件包含针对 Package naming（跨服务命名唯一事实源 SSOT）的核心单元测试。
// 除了验证基本查找与归一化功能的正确性外，更重要的是作为 CI/CD 流水线中的
// 「架构防腐守卫 (Architecture Guardrails)」：
//  1. 防止数据源注册表元数据发生意外污染或缺失（自洽性断言）；
//  2. 严防别名冲突与别名遮蔽（Shadowing）引发的非确定性路由；
//  3. 强制断言未知标识必须 Fail-Closed 阻断（严禁静默降级）；
//  4. 严格校验预留数据源在写侧必须被坚决拒绝（409 保护机制）。
// ==============================================================================

package naming

import (
	"errors"
	"strings"
	"testing"
)

// TestRegistrySelfConsistency 校验数据源权威注册表（Registry）的全局自洽性与完整性。
//
// 测试目的与执行逻辑：
// 1. 判空防腐：确保注册表非空；
// 2. 别名冲突排查：调用 AliasConflicts() 断言不同数据源之间不存在同名别名竞争；
// 3. 主键与契约唯一性：确保 DataSourceID 与 APICode 全局唯一且非空；
// 4. 正则合规性：确保所有 ID 与 API 编码 100% 满足命名空间正则约束（^ds_[a-z]... 与 ^api[1-9]...）；
// 5. 状态机与 Schema 合法性：激活条目必须配置合法的字段数（FieldCount > 0）；
// 6. 国际化支持：确保中英双语展示名称（zh-CN / en-US）完整配置；
// 7. 防遮蔽断言：确保任何别名字符串绝不可与其它数据源的 Canonical ID 产生重叠，保证归一化确定性。
func TestRegistrySelfConsistency(t *testing.T) {
	if len(Registry) == 0 {
		t.Fatal("registry must not be empty")
	}
	if got := len(AliasConflicts()); got > 0 {
		t.Fatalf("registry has conflicting aliases: %v", AliasConflicts())
	}

	seenID := map[string]bool{}
	seenCode := map[string]bool{}
	for _, e := range Registry {
		if e.DataSourceID == "" {
			t.Fatalf("entry seq=%d has empty datasource_id", e.Seq)
		}
		if seenID[e.DataSourceID] {
			t.Fatalf("duplicate datasource_id %q", e.DataSourceID)
		}
		seenID[e.DataSourceID] = true

		if !ValidDataSourceIDFormat(e.DataSourceID) {
			t.Errorf("datasource_id %q violates ^ds_[a-z][a-z0-9_]{1,30}$", e.DataSourceID)
		}
		if e.APICode != "" {
			if seenCode[e.APICode] {
				t.Fatalf("duplicate api_code %q", e.APICode)
			}
			seenCode[e.APICode] = true
			if !ValidAPICodeFormat(e.APICode) {
				t.Errorf("api_code %q violates ^api[1-9]_[a-z][a-z0-9_]{1,30}$", e.APICode)
			}
		}
		if e.Status != StatusActive && e.Status != StatusReserved {
			t.Errorf("%s: unknown status %q", e.DataSourceID, e.Status)
		}
		if e.Status == StatusActive && e.FieldCount <= 0 {
			t.Errorf("%s: active entry must declare a positive field_count", e.DataSourceID)
		}
		if _, ok := e.DisplayName["zh-CN"]; !ok {
			t.Errorf("%s: missing zh-CN display name", e.DataSourceID)
		}
		if _, ok := e.DisplayName["en-US"]; !ok {
			t.Errorf("%s: missing en-US display name", e.DataSourceID)
		}
		// An alias must never collide with another entry's canonical id,
		// otherwise normalization would be order-dependent.
		for _, a := range e.Aliases {
			if other, ok := EntryByDataSourceID(a); ok && other.DataSourceID != e.DataSourceID {
				t.Errorf("%s: alias %q shadows canonical id of %s", e.DataSourceID, a, other.DataSourceID)
			}
		}
	}
}

// TestCanonicalIDsAreActive 验证核心生产级数据源（医保与康养）处于可写激活状态。
//
// 测试目的：
// 确保生产级主力数据源（DSYibao, DSKangyang）在 CheckWritable 校验下能够顺利通行。
func TestCanonicalIDsAreActive(t *testing.T) {
	for _, id := range []string{DSYibao, DSKangyang} {
		if err := CheckWritable(id); err != nil {
			t.Errorf("CheckWritable(%q) = %v, want nil", id, err)
		}
	}
}

// TestNormalizeKnownRepresentations 验证各种已知表现形式的入站归一化能力。
//
// 测试目的与执行逻辑：
// 遍历测试用例矩阵，验证 NormalizeDataSourceID 能否将以下各类表现形式正确映射为标准 canonical ID：
// 1. 标准 Canonical ID 原样直通（如 "ds_yibao"）；
// 2. 业务 API 编码（如 "api1_yibao"、"api2_kangyang"）；
// 3. 历史 URL slug、物理文件名、大写/全大写/混合大小写扩展名（如 "yibao.csv", "KANGYANG.CSV"）；
// 4. 中文关键词与领域分类名（如 "医保", "康养", "healthcare"）；
// 5. 带有前后空白字符的脏输入（如 "  ds_yibao  "）；
// 6. 预留数据源在读侧的正常可解析性（如 "mock3" -> "ds_mock3"）。
func TestNormalizeKnownRepresentations(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// canonical passes through untouched
		{"ds_yibao", DSYibao},
		{"ds_kangyang", DSKangyang},
		// api_code resolves to its datasource
		{API1Yibao, DSYibao},
		{API2Kangyang, DSKangyang},
		// URL slug / file name / Chinese keyword / category
		{"yibao", DSYibao},
		{"Yibao", DSYibao},
		{"yibao.csv", DSYibao},
		{"医保", DSYibao},
		{"medical", DSYibao},
		{"kangyang", DSKangyang},
		{"KANGYANG.CSV", DSKangyang},
		{"康养", DSKangyang},
		{"healthcare", DSKangyang},
		// surrounding whitespace is tolerated
		{"  ds_yibao  ", DSYibao},
		// reserved placeholders are still resolvable (read side)
		{"mock3", DSMock3},
		{"ds_mock4", DSMock4},
	}
	for _, c := range cases {
		got, err := NormalizeDataSourceID(c.in)
		if err != nil {
			t.Errorf("NormalizeDataSourceID(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeDataSourceID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNormalizeUnknownFailsClosed 验证未知/非法数据源标识的 Fail-Closed 安全阻断机制。
//
// 架构安全守卫：
// 当传入空字符串、纯空格、未知社保标识（"shebao"）、伪造前缀（"ds_custom"）时，
// 必须严格返回包装了 ErrUnknownDataSource 的错误，绝对不允许静默降级或回退到默认数据源。
func TestNormalizeUnknownFailsClosed(t *testing.T) {
	for _, in := range []string{"", "   ", "shebao", "ds_shebao", "api3_shebao", "ds_custom", "unknown.csv"} {
		got, err := NormalizeDataSourceID(in)
		if err == nil {
			t.Errorf("NormalizeDataSourceID(%q) = %q, want error", in, got)
			continue
		}
		if !IsUnknownDataSource(err) {
			t.Errorf("NormalizeDataSourceID(%q) error %v must wrap ErrUnknownDataSource", in, err)
		}
		if got != "" {
			t.Errorf("NormalizeDataSourceID(%q) returned %q alongside error", in, got)
		}
	}
}

// TestUnknownErrorCarriesAllowedList 验证未知数据源错误信息中是否附带可用数据源指导清单。
//
// 测试目的：
// 确保错误信息不仅提示当前错误输入（如 "shebao"），还明确输出允许调用的合法列表（如 ds_yibao, ds_kangyang），
// 方便调用方在收到 400 错误信封时快速诊断和自愈。
func TestUnknownErrorCarriesAllowedList(t *testing.T) {
	_, err := NormalizeDataSourceID("shebao")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrUnknownDataSource) {
		t.Fatalf("error must wrap ErrUnknownDataSource, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{`"shebao"`, DSYibao, DSKangyang} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q must mention %q", msg, want)
		}
	}
}

// TestResolveInboundRejectsReserved 验证写侧入口（ResolveInbound）对预留数据源的拦截。
//
// 测试目的与执行逻辑：
// 1. 验证常规活跃数据源（"yibao"）能够正常通过 ResolveInbound 并在归一化后成功返回；
// 2. 验证预留数据源（"mock3"）在进入写侧调度时被准确识别为 ErrReservedDataSource 拒绝（HTTP 409 语义）；
// 3. 验证预留位错误绝不被误判为「未知数据源 (IsUnknownDataSource)」。
func TestResolveInboundRejectsReserved(t *testing.T) {
	if _, err := ResolveInbound("yibao"); err != nil {
		t.Fatalf("ResolveInbound(yibao) = %v, want nil", err)
	}
	_, err := ResolveInbound("mock3")
	if err == nil {
		t.Fatal("ResolveInbound(mock3) must reject reserved placeholder")
	}
	if !IsReserved(err) {
		t.Errorf("ResolveInbound(mock3) error %v must wrap ErrReservedDataSource", err)
	}
	if IsUnknownDataSource(err) {
		t.Errorf("reserved error must not be reported as unknown: %v", err)
	}
}

// TestCheckWritableRejectsUnregistered 验证 CheckWritable 对非规范字面量与未登记 ID 的防御性拒绝。
//
// 测试目的：
// 1. CheckWritable 仅接受严格的 Canonical ID，传入别名（如 "yibao"）必须直接报错；
// 2. 传入未登记的合规格式 ID（如 "ds_nope"）必须返回 ErrUnknownDataSource。
func TestCheckWritableRejectsUnregistered(t *testing.T) {
	if err := CheckWritable("yibao"); err == nil {
		t.Error("CheckWritable must require canonical form, got nil for slug \"yibao\"")
	}
	if err := CheckWritable("ds_nope"); err == nil || !IsUnknownDataSource(err) {
		t.Errorf("CheckWritable(ds_nope) = %v, want ErrUnknownDataSource", err)
	}
}

// TestAPICodeDataSourceBidirectionalMapping 验证 API 编码与数据源 ID 的双向绑定映射一致性。
//
// 测试目的与执行逻辑：
// 1. 验证 APICodeForDataSource("ds_yibao") 返回 "api1_yibao"；
// 2. 验证预留数据源由于无业务 API 绑定，返回空串 ""；
// 3. 遍历注册表验证 DataSourceForAPICode 与 APICodeForDataSource 互为可逆双射。
func TestAPICodeDataSourceBidirectionalMapping(t *testing.T) {
	if got := APICodeForDataSource(DSYibao); got != API1Yibao {
		t.Errorf("APICodeForDataSource(ds_yibao) = %q, want %q", got, API1Yibao)
	}
	if got := APICodeForDataSource(DSMock3); got != "" {
		t.Errorf("reserved placeholder must have no api_code, got %q", got)
	}
	for _, e := range Registry {
		if e.APICode == "" {
			continue
		}
		id, ok := DataSourceForAPICode(e.APICode)
		if !ok || id != e.DataSourceID {
			t.Errorf("DataSourceForAPICode(%q) = %q/%v, want %q", e.APICode, id, ok, e.DataSourceID)
		}
	}
}

// TestActiveEntriesExcludeReserved 验证可用数据源过滤与全量数据源列表的隔离性。
//
// 测试目的与执行逻辑：
// 1. 验证 ActiveDataSourceIDs() 返回的列表均属于 StatusActive；
// 2. 验证 StatusReserved 预留数据源绝对不会泄漏到 Active 列表中；
// 3. 验证 Entries() 返回的条目数量与底表切片 Registry 严格一致。
func TestActiveEntriesExcludeReserved(t *testing.T) {
	active := ActiveDataSourceIDs()
	if len(active) == 0 {
		t.Fatal("expected at least one active datasource")
	}
	for _, id := range active {
		e, ok := EntryByDataSourceID(id)
		if !ok || e.Status != StatusActive {
			t.Errorf("ActiveDataSourceIDs leaked non-active id %q", id)
		}
	}
	for _, id := range AllDataSourceIDs() {
		e, _ := EntryByDataSourceID(id)
		if e.Status == StatusReserved && strings.Contains(strings.Join(active, ","), id) {
			t.Errorf("reserved id %q leaked into active list", id)
		}
	}
	if len(Entries()) != len(Registry) {
		t.Errorf("Entries() must return all %d rows, got %d", len(Registry), len(Entries()))
	}
}

// TestFormatValidators 验证命名空间字面格式校验器（ValidDataSourceIDFormat / ValidAPICodeFormat）。
//
// 测试目的：
// 覆盖合法与非法格式样本集，验证正则对下划线、前缀、长度与大小写限制的严密性。
func TestFormatValidators(t *testing.T) {
	valid := []string{"ds_yibao", "ds_kangyang", "ds_mock3", "ds_a1"}
	invalid := []string{"yibao", "ds_", "ds_Yibao", "ds-1", "d_yibao", "ds_"}
	for _, s := range valid {
		if !ValidDataSourceIDFormat(s) {
			t.Errorf("ValidDataSourceIDFormat(%q) = false", s)
		}
	}
	for _, s := range invalid {
		if ValidDataSourceIDFormat(s) {
			t.Errorf("ValidDataSourceIDFormat(%q) = true", s)
		}
	}
	if !ValidAPICodeFormat(API2Kangyang) {
		t.Error("api2_kangyang must be a valid api_code")
	}
	for _, s := range []string{"API1", "api_1", "api0_yibao", "yibao"} {
		if ValidAPICodeFormat(s) {
			t.Errorf("ValidAPICodeFormat(%q) = true", s)
		}
	}
}
