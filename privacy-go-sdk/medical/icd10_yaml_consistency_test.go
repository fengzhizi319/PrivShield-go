package medical

// ICD-10 双路径等级一致性门禁（整改项 P1-4）。
//
// 同一诊断编码历史上存在两份事实源：rules/domains/medical.yaml 的
// RULE_MED_ICD10.intervals（规则引擎路径）与本包 ClassifyICD10Code 的硬编码区间
// （SDK 本地医疗流水线路径），两者曾对 HIV / F20-F29 给出 L4 vs L5 的不同定级。
// 本测试把 YAML 当作唯一事实源，在**全编码空间**（A-Z × 00-99）逐码双向比对：
//   - YAML 命中而 SDK 未命中 / 等级或范畴不同 → 失败；
//   - SDK 命中而 YAML 无对应区间 → 失败；
//   - 任一 YAML 区间覆盖不到任何编码（区间写反、被解析器漏掉）→ 失败。
//
// 等级词表按 pkg/naming 的口径归一后再比较（"L4" ≡ "secret"，rank 4），
// 因此任一侧改用 canonical 名称或新增等级都会被本文件第二条测试一并卡住。
//
// 注意：本包是零依赖 SDK，不得为测试引入 gopkg.in/yaml.v3 或 pkg/naming，
// 故这里用标准库做定向解析——并对解析结果强制断言非空，避免「两侧都抽到空串」的假绿。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// icd10RuleID 是 rules/domains/medical.yaml 中承载 ICD-10 区间表的规则 id。
const icd10RuleID = "RULE_MED_ICD10"

// icd10Interval 是 YAML intervals 的一条闭区间（元组字典序，与引擎 inIcd10Interval 同构）。
type icd10Interval struct {
	start    string // 形如 "B20"
	end      string // 形如 "B24"
	level    string // 显式声明的等级（L1~L5）
	category string // rules/taxonomies/default.yaml 的分类体系标识
}

// icd10LevelSpec 是本包测试侧的等级词表，必须与 pkg/naming/levels.go 完全一致
// （由 TestICD10LevelVocabularyMatchesPkgNaming 断言）。
type icd10LevelSpec struct {
	id        string
	canonical string
	rank      int
}

var icd10LevelVocabulary = []icd10LevelSpec{
	{"L1", "public", 1},
	{"L2", "internal", 2},
	{"L3", "confidential", 3},
	{"L4", "secret", 4},
	{"L5", "top_secret", 5},
}

var (
	yamlRuleIDRe     = regexp.MustCompile(`^\s*-?\s*id:\s*"([^"]+)"\s*$`)
	yamlStartRe      = regexp.MustCompile(`start:\s*"([A-Za-z][0-9]{2})"`)
	yamlEndRe        = regexp.MustCompile(`end:\s*"([A-Za-z][0-9]{2})"`)
	yamlLevelRe      = regexp.MustCompile(`(?:^|[{,\s])level:\s*"([^"]+)"`)
	yamlCategoryRe   = regexp.MustCompile(`category:\s*"([^"]+)"`)
	namingLevelsRe   = regexp.MustCompile(`\{SecurityLevel(L[0-9]),\s*"([^"]+)",\s*"[^"]*",\s*([0-9]+)\}`)
	singleLetterCode = regexp.MustCompile(`^([A-Z])([0-9]{2})$`)
)

// findRepoRoot 从测试工作目录向上回溯，定位包含 rules/domains/medical.yaml 的仓库根。
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	start := dir
	for i := 0; i < 10; i++ {
		if fi, err := os.Stat(filepath.Join(dir, "rules", "domains", "medical.yaml")); err == nil && !fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("rules/domains/medical.yaml not found walking up from %s", start)
	return ""
}

// loadICD10Intervals 定向抽取 RULE_MED_ICD10 的 intervals 列表。
// 解析失败（找不到规则、区间块为空、条目缺字段、等级词表外）一律 Fatal：
// 静默返回空列表会让逐码比对退化成恒等式（假绿）。
func loadICD10Intervals(t *testing.T, path string) []icd10Interval {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(raw), "\n")

	ruleStart := -1
	for i, ln := range lines {
		if m := yamlRuleIDRe.FindStringSubmatch(ln); m != nil && m[1] == icd10RuleID {
			ruleStart = i
			break
		}
	}
	if ruleStart < 0 {
		t.Fatalf("rule %q not found in %s", icd10RuleID, path)
	}

	blockStart := -1
	for i := ruleStart + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "- id:") || strings.HasPrefix(trimmed, "downgrade_rules:") {
			t.Fatalf("reached the end of rule %q before its intervals: block (line %d: %q) — "+
				"intervals 段落被删除或改写，本门禁需同步更新", icd10RuleID, i+1, trimmed)
		}
		if trimmed == "intervals:" {
			blockStart = i
			break
		}
	}
	if blockStart < 0 {
		t.Fatalf("no \"intervals:\" block inside rule %q of %s", icd10RuleID, path)
	}

	var out []icd10Interval
	baseIndent := len(lines[blockStart]) - len(strings.TrimLeft(lines[blockStart], " "))
	for i := blockStart + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
		if indent <= baseIndent || !strings.HasPrefix(trimmed, "- ") {
			break // 区间块结束
		}
		entry := trimmed
		start := firstGroup(t, yamlStartRe, entry, i+1, "start")
		end := firstGroup(t, yamlEndRe, entry, i+1, "end")
		level := firstGroup(t, yamlLevelRe, entry, i+1, "level")
		category := firstGroup(t, yamlCategoryRe, entry, i+1, "category")
		if _, ok := icd10LevelRank(level); !ok {
			t.Fatalf("%s line %d: level %q is not in the L1~L5 vocabulary", path, i+1, level)
		}
		if !singleLetterCode.MatchString(start) || !singleLetterCode.MatchString(end) {
			t.Fatalf("%s line %d: interval bounds %q..%q must be 1-letter + 2-digit ICD codes", path, i+1, start, end)
		}
		out = append(out, icd10Interval{
			start:    strings.ToUpper(start),
			end:      strings.ToUpper(end),
			level:    level,
			category: category,
		})
	}
	if len(out) == 0 {
		t.Fatalf("rule %q in %s declares no ICD-10 interval — the comparison below would be vacuous",
			icd10RuleID, path)
	}
	return out
}

func firstGroup(t *testing.T, re *regexp.Regexp, line string, lineNo int, key string) string {
	t.Helper()
	m := re.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("interval line %d has no %q key; the parser is stale for this YAML shape:\n\t%s", lineNo, key, line)
	}
	return m[1]
}

// icd10LevelRank 把任一词表写法（"L4" / "secret"，大小写与空白无关）归一为 rank。
func icd10LevelRank(level string) (int, bool) {
	key := strings.ToLower(strings.TrimSpace(level))
	if key == "" {
		return 0, false
	}
	for _, spec := range icd10LevelVocabulary {
		if key == strings.ToLower(spec.id) || key == spec.canonical {
			return spec.rank, true
		}
	}
	return 0, false
}

// icd10IntervalCovers 与引擎 inIcd10Interval 同构的元组闭区间判定。
func icd10IntervalCovers(iv icd10Interval, letter byte, num int) bool {
	sLetter, sNum := icd10IntervalBound(iv.start)
	eLetter, eNum := icd10IntervalBound(iv.end)
	if letter < sLetter || letter > eLetter {
		return false
	}
	if letter == sLetter && num < sNum {
		return false
	}
	if letter == eLetter && num > eNum {
		return false
	}
	return true
}

// icd10IntervalBound 拆解 "B20" 为字母与序号二元组（调用方已校验格式）。
func icd10IntervalBound(code string) (byte, int) {
	m := singleLetterCode.FindStringSubmatch(code)
	n, _ := strconv.Atoi(m[2])
	return m[1][0], n
}

// TestICD10SDKAgreesWithAuthoritativeYAML 在全编码空间逐码比对 SDK 查表结果与 YAML 区间。
func TestICD10SDKAgreesWithAuthoritativeYAML(t *testing.T) {
	root := findRepoRoot(t)
	yamlPath := filepath.Join(root, "rules", "domains", "medical.yaml")
	intervals := loadICD10Intervals(t, yamlPath)
	assertICD10IntervalsWellFormed(t, intervals)

	compared := 0
	drifts := 0
	coveredByInterval := make([]int, len(intervals))

	for letter := byte('A'); letter <= byte('Z'); letter++ {
		for num := 0; num < 100; num++ {
			code := fmt.Sprintf("%c%02d", letter, num)
			wantLevel, wantCategory, wantHit, matchedIdx := expectedFromYAML(intervals, letter, num)
			gotLevel, gotCategory, gotHit := ClassifyICD10Code(code)
			if matchedIdx >= 0 {
				coveredByInterval[matchedIdx]++
			}
			if !wantHit && !gotHit {
				continue // 两侧一致地「不高危」，该码不参与比对
			}
			compared++
			switch {
			case wantHit && !gotHit:
				t.Errorf("%s: YAML says %s/%s but SDK does not classify it at all", code, wantLevel, wantCategory)
			case !wantHit && gotHit:
				t.Errorf("%s: SDK says %s/%s but rule %q declares no interval covering it",
					code, gotLevel, gotCategory, icd10RuleID)
			case gotLevel != wantLevel || gotCategory != wantCategory:
				t.Errorf("%s: drift — YAML=%s/%s SDK=%s/%s", code, wantLevel, wantCategory, gotLevel, gotCategory)
			default:
				continue
			}
			if drifts++; drifts >= 20 {
				t.Fatalf("too many ICD-10 drifts, stopping after %d", drifts)
			}
		}
	}

	if compared == 0 {
		t.Fatalf("no ICD-10 code was compared (intervals=%d) — the check is vacuous, investigate the parser",
			len(intervals))
	}
	t.Logf("compared %d ICD-10 code(s) across %d YAML interval(s)", compared, len(intervals))
	if drifts > 0 {
		t.Fatalf("%d ICD-10 code(s) drift between %s and privacy-go-sdk/medical/rules.go", drifts, yamlPath)
	}
	// 每个声明的区间都必须真实覆盖到编码，否则该区间根本没被比对（假绿来源之一）。
	for i, iv := range intervals {
		if coveredByInterval[i] == 0 {
			t.Errorf("interval %s..%s (%s/%s) matched no ICD code — it is never verified",
				iv.start, iv.end, iv.level, iv.category)
		}
	}

	// 临床扩展码形（B20.900 / C34.900）必须与裸码同级：正则或分级被改动时立即暴露。
	for _, iv := range intervals {
		for _, code := range []string{iv.start + ".900", iv.end + ".900"} {
			gotLevel, gotCategory, ok := ClassifyICD10Code(code)
			if !ok {
				t.Errorf("%s: SDK does not classify this suffixed code, want %s/%s", code, iv.level, iv.category)
				continue
			}
			if gotLevel != iv.level || gotCategory != iv.category {
				t.Errorf("%s: drift — YAML=%s/%s SDK=%s/%s", code, iv.level, iv.category, gotLevel, gotCategory)
			}
		}
	}
}

// assertICD10IntervalsWellFormed 校验 YAML 区间表自身可判定：
// 边界不得倒挂，重叠区间必须给出一致结论（否则同一条码会有两种定级，门禁失去意义）。
func assertICD10IntervalsWellFormed(t *testing.T, intervals []icd10Interval) {
	t.Helper()
	for i, iv := range intervals {
		sLetter, sNum := icd10IntervalBound(iv.start)
		eLetter, eNum := icd10IntervalBound(iv.end)
		if sLetter > eLetter || (sLetter == eLetter && sNum > eNum) {
			t.Fatalf("interval %d (%s..%s) has inverted bounds", i, iv.start, iv.end)
		}
		for j := i + 1; j < len(intervals); j++ {
			other := intervals[j]
			for letter := byte('A'); letter <= byte('Z'); letter++ {
				for num := 0; num < 100; num++ {
					if !icd10IntervalCovers(iv, letter, num) || !icd10IntervalCovers(other, letter, num) {
						continue
					}
					if iv.level != other.level || iv.category != other.category {
						t.Fatalf("intervals %d (%s..%s %s/%s) and %d (%s..%s %s/%s) overlap on %c%02d with different verdicts",
							i, iv.start, iv.end, iv.level, iv.category,
							j, other.start, other.end, other.level, other.category, letter, num)
					}
				}
			}
		}
	}
}

// expectedFromYAML 返回覆盖 (letter, num) 的区间结论（区间表已由上游校验为无歧义）。
func expectedFromYAML(intervals []icd10Interval, letter byte, num int) (level, category string, hit bool, idx int) {
	for i, iv := range intervals {
		if !icd10IntervalCovers(iv, letter, num) {
			continue
		}
		if hit {
			continue // 重叠已由 assertICD10IntervalsWellFormed 拒绝
		}
		level, category, hit, idx = iv.level, iv.category, true, i
	}
	return level, category, hit, idx
}

// TestICD10LevelVocabularyMatchesPkgNaming 断言等级词表（L1~L5 ↔ engine canonical ↔ rank）
// 与 pkg/naming/levels.go 逐字段一致：两条定级路径必须映射到同一套排名口径。
// SDK 不依赖 pkg/naming（零依赖约束），因此此处按文本读取而非 import。
func TestICD10LevelVocabularyMatchesPkgNaming(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "pkg", "naming", "levels.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	entries := namingLevelsRe.FindAllStringSubmatch(string(raw), -1)
	if len(entries) == 0 {
		t.Fatalf("no level entries parsed from %s — pkg/naming word table shape changed, this gate is stale", path)
	}
	if len(entries) != len(icd10LevelVocabulary) {
		t.Fatalf("pkg/naming declares %d levels, test vocabulary declares %d",
			len(entries), len(icd10LevelVocabulary))
	}
	for i, spec := range icd10LevelVocabulary {
		got := entries[i]
		if got[1] != spec.id || got[2] != spec.canonical {
			t.Errorf("level vocabulary drift at index %d: pkg/naming=%s/%s test=%s/%s",
				i, got[1], got[2], spec.id, spec.canonical)
		}
		if rank, _ := strconv.Atoi(got[3]); rank != spec.rank {
			t.Errorf("level %s rank drift: pkg/naming=%s test=%d", spec.id, got[3], spec.rank)
		}
	}
	// rank 必须严格递增，且与 SDK 返回的 "L4"/"L5" 写法可归一。
	for i := 1; i < len(icd10LevelVocabulary); i++ {
		if icd10LevelVocabulary[i].rank != icd10LevelVocabulary[i-1].rank+1 {
			t.Fatalf("test level vocabulary is not a strict ascending ranking")
		}
	}
	if r, ok := icd10LevelRank("secret"); !ok || r != 4 {
		t.Errorf(`icd10LevelRank("secret") = %d, %v; want 4, true`, r, ok)
	}
}
