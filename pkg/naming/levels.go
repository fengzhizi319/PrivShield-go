// levels.go — 数据安全分级词表（L1~L5）的跨服务唯一事实源。
//
// 本仓库同时存在两套等级表达：
//   - 「DB51/T 2989-2023 / rules/taxonomies/default.yaml」的 L1~L5 标识（对外契约、存证、调度）；
//   - engine「三层漏斗」内部 canonical 名称 public/internal/confidential/secret/top_secret
//     （safety floor 仲裁、privacy.yaml 配置）。
//
// 历史上两套词表各自为政：engine 分类响应只回 "confidential"，而 service-hub 的
// LevelToOperation 只认 "L3"，导致定级结果被静默丢弃并恒退化为 "mask"（P1-1）。
// 本文件把两套表达的映射、排名与中文名收敛为唯一实现，任何服务不得再自建副本。

package naming

import (
	"strings"
)

// L1~L5 等级标识（与 rules/taxonomies/default.yaml 的 levels.*.id 严格一致）
const (
	SecurityLevelL1 = "L1"
	SecurityLevelL2 = "L2"
	SecurityLevelL3 = "L3"
	SecurityLevelL4 = "L4"
	SecurityLevelL5 = "L5"
)

// securityLevelSpec 是一个等级的完整词表条目。
type securityLevelSpec struct {
	id    string // L1~L5 标识（对外契约 / 存证 security_level 字段）
	name  string // engine 内部 canonical 名称
	label string // 中文名称（与 rules/taxonomies/default.yaml levels.*.name 一致）
	rank  int    // 1..5，索引越大越敏感
}

// securityLevels 按敏感度升序排列，索引 0 即 L1。
// 任何新增/改名必须同步 rules/taxonomies/default.yaml（由 TestSecurityLevelsMatchTaxonomyYAML 断言）。
var securityLevels = []securityLevelSpec{
	{SecurityLevelL1, "public", "公开数据", 1},
	{SecurityLevelL2, "internal", "内部数据", 2},
	{SecurityLevelL3, "confidential", "敏感数据", 3},
	{SecurityLevelL4, "secret", "高敏感数据", 4},
	{SecurityLevelL5, "top_secret", "极敏感数据", 5},
}

// SecurityLevelIDs returns the L1~L5 identifiers in ascending sensitivity order.
// SecurityLevelIDs 按敏感度升序返回 L1~L5 标识。
func SecurityLevelIDs() []string {
	ids := make([]string, 0, len(securityLevels))
	for _, l := range securityLevels {
		ids = append(ids, l.id)
	}
	return ids
}

// SecurityLevelNames returns the engine canonical names in ascending sensitivity order
// (public|internal|confidential|secret|top_secret).
// SecurityLevelNames 按敏感度升序返回引擎 canonical 名称。
func SecurityLevelNames() []string {
	names := make([]string, 0, len(securityLevels))
	for _, l := range securityLevels {
		names = append(names, l.name)
	}
	return names
}

// SecurityLevelLabel returns the Chinese label of a level ("L4" / "secret" → "高敏感数据").
// 未知等级返回空串。
func SecurityLevelLabel(level string) string {
	if spec, ok := lookupSecurityLevel(level); ok {
		return spec.label
	}
	return ""
}

// SecurityLevelName maps any accepted spelling to the engine canonical name
// ("L3" → "confidential"). 未知等级返回空串。
func SecurityLevelName(level string) string {
	if spec, ok := lookupSecurityLevel(level); ok {
		return spec.name
	}
	return ""
}

// NormalizeSecurityLevelID maps any accepted spelling to the L1~L5 identifier
// ("confidential" / "l3" / " L3 " → "L3"). 未知等级返回空串（不得静默兜底为某个等级）。
func NormalizeSecurityLevelID(level string) string {
	if spec, ok := lookupSecurityLevel(level); ok {
		return spec.id
	}
	return ""
}

// SecurityLevelRank returns the 1-based sensitivity rank of a level (L1 → 1 … L5 → 5).
// 无法识别的等级返回 0，调用方据此区分「最低等级」与「词表外的脏值」。
func SecurityLevelRank(level string) int {
	if spec, ok := lookupSecurityLevel(level); ok {
		return spec.rank
	}
	return 0
}

// MaxSecurityLevelID returns the highest-ranked level among the inputs, normalized to
// L1~L5. Values that are not in the vocabulary are ignored; all unknown → "".
// MaxSecurityLevelID 返回入参中敏感度最高的等级（归一为 L1~L5）；词表外取值忽略，全部忽略时返回空串。
func MaxSecurityLevelID(levels ...string) string {
	best := 0
	for _, l := range levels {
		if r := SecurityLevelRank(l); r > best {
			best = r
		}
	}
	if best == 0 {
		return ""
	}
	return securityLevels[best-1].id
}

// SecurityLevelLabels returns a copy of the L1~L5 → 中文名称 mapping.
func SecurityLevelLabels() map[string]string {
	out := make(map[string]string, len(securityLevels))
	for _, l := range securityLevels {
		out[l.id] = l.label
	}
	return out
}

// lookupSecurityLevel resolves one spelling of the vocabulary.
func lookupSecurityLevel(level string) (securityLevelSpec, bool) {
	key := strings.ToLower(strings.TrimSpace(level))
	if key == "" {
		return securityLevelSpec{}, false
	}
	for _, l := range securityLevels {
		if key == strings.ToLower(l.id) || key == l.name {
			return l, true
		}
	}
	return securityLevelSpec{}, false
}
