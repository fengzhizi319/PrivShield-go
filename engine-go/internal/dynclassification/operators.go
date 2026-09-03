// Package dynclassification 提供三层动态分类分级引擎扩展。
//
// operators.go — 匹配算子注册表与 AC 自动机集成
package dynclassification

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// ──────────────────────────────────────────────
// 算子类型
// ──────────────────────────────────────────────

// OperatorType 算子类型
type OperatorType string

const (
	OpRegex          OperatorType = "regex"                 // 正则匹配
	OpContains       OperatorType = "contains"              // 子串包含
	OpEquals         OperatorType = "equals"                // 精确等于
	OpStartsWith     OperatorType = "starts_with"           // 前缀匹配
	OpEndsWith       OperatorType = "ends_with"             // 后缀匹配
	OpACAutomaton    OperatorType = "ac_automaton"          // AC 自动机多模式匹配
	OpFieldMatch     OperatorType = "field_match"           // 字段名匹配
	OpIdCardChecksum OperatorType = "id_card_checksum"      // 身份证校验
	OpMedicalCard    OperatorType = "medical_card_checksum" // 上海医保卡校验
	OpIcd10Range     OperatorType = "icd10_range"           // ICD-10 区间判定
	OpLuhnChecksum   OperatorType = "luhn_checksum"         // Luhn 银行卡校验
	OpLengthRange    OperatorType = "length_range"          // 长度范围匹配
	OpIpAddress      OperatorType = "ip_address"            // IP 地址匹配
	OpMacAddress     OperatorType = "mac_address"           // MAC 地址匹配
	OpChineseName    OperatorType = "chinese_name"          // 中文姓名匹配
	OpEmail          OperatorType = "email"                 // 邮箱匹配
)

// Operator 匹配算子接口
type Operator interface {
	Type() OperatorType
	Match(field, value string) bool
}

// ──────────────────────────────────────────────
// 具体算子实现
// ──────────────────────────────────────────────

// RegexOperator 正则匹配算子
type RegexOperator struct {
	pattern string
}

func (o *RegexOperator) Type() OperatorType { return OpRegex }
func (o *RegexOperator) Match(field, value string) bool {
	return matchRegex(o.pattern, value)
}

// ContainsOperator 子串包含算子
type ContainsOperator struct {
	substr string
}

func (o *ContainsOperator) Type() OperatorType { return OpContains }
func (o *ContainsOperator) Match(field, value string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(o.substr))
}

// EqualsOperator 精确等于算子
type EqualsOperator struct {
	target string
}

func (o *EqualsOperator) Type() OperatorType { return OpEquals }
func (o *EqualsOperator) Match(field, value string) bool {
	return strings.EqualFold(value, o.target)
}

// StartsWithOperator 前缀匹配算子
type StartsWithOperator struct {
	prefix string
}

func (o *StartsWithOperator) Type() OperatorType { return OpStartsWith }
func (o *StartsWithOperator) Match(field, value string) bool {
	return strings.HasPrefix(strings.ToLower(value), strings.ToLower(o.prefix))
}

// EndsWithOperator 后缀匹配算子
type EndsWithOperator struct {
	suffix string
}

func (o *EndsWithOperator) Type() OperatorType { return OpEndsWith }
func (o *EndsWithOperator) Match(field, value string) bool {
	return strings.HasSuffix(strings.ToLower(value), strings.ToLower(o.suffix))
}

// FieldMatchOperator 字段名匹配算子
type FieldMatchOperator struct {
	pattern string
}

func (o *FieldMatchOperator) Type() OperatorType { return OpFieldMatch }
func (o *FieldMatchOperator) Match(field, value string) bool {
	return matchRegex(o.pattern, field)
}

// AcAutomatonOperator AC 自动机多模式匹配算子。
// 基于 Aho-Corasick 算法实现 O(N+M+Z) 多模式匹配，
// 适用于高敏医学词库等大规模关键词扫描场景。
type AcAutomatonOperator struct {
	ac        *AhoCorasick
	termLevel map[string]string // 模式串(小写) → 等级
}

// NewAcAutomatonOperator 创建 AC 自动机算子。
// termsMap 的 key 为模式串，value 为对应等级（如 "L5"）。
func NewAcAutomatonOperator(termsMap map[string]string) *AcAutomatonOperator {
	ac := NewAhoCorasick()
	lowerMap := make(map[string]string, len(termsMap))
	for term, level := range termsMap {
		lw := strings.ToLower(term)
		ac.AddPattern(lw)
		lowerMap[lw] = level
	}
	ac.Build()
	return &AcAutomatonOperator{ac: ac, termLevel: lowerMap}
}

func (o *AcAutomatonOperator) Type() OperatorType { return OpACAutomaton }

// Match 对 value 执行 AC 多模式匹配。
// 返回 (是否匹配, 最高等级, 匹配到的原始词条列表)。
func (o *AcAutomatonOperator) Match(field, value string) bool {
	return o.ac.Contains(strings.ToLower(value))
}

// MatchDetail 返回详细匹配结果（是否匹配, 最高等级, 匹配词条列表）。
func (o *AcAutomatonOperator) MatchDetail(value string) (bool, string, []string) {
	lower := strings.ToLower(value)
	matches := o.ac.MatchString(lower)
	if len(matches) == 0 {
		return false, "L1", nil
	}
	maxLevel := "L1"
	var matchedTerms []string
	for _, m := range matches {
		lvl := o.termLevel[m.Pattern]
		matchedTerms = append(matchedTerms, m.Pattern)
		if lRank(lvl) > lRank(maxLevel) {
			maxLevel = lvl
		}
	}
	return true, maxLevel, matchedTerms
}

// lRank 将 "L1"-"L5" 等级字符串映射为数值（越高越敏感）。
func lRank(level string) int {
	switch level {
	case "L5":
		return 5
	case "L4":
		return 4
	case "L3":
		return 3
	case "L2":
		return 2
	case "L1":
		return 1
	default:
		return 0
	}
}

// ──────────────────────────────────────────────
// 校验算子实现（与 Python operators.py 完全对齐）
// ──────────────────────────────────────────────

// IdCardChecksumOperator 中国大陆 18 位身份证校验算子（GB 11643-1999）
type IdCardChecksumOperator struct{}

func (o *IdCardChecksumOperator) Type() OperatorType { return OpIdCardChecksum }
func (o *IdCardChecksumOperator) Match(field, value string) bool {
	return validateIdCard(value)
}

// MedicalCardChecksumOperator 上海医保卡号 9 位校验算子
type MedicalCardChecksumOperator struct{}

func (o *MedicalCardChecksumOperator) Type() OperatorType { return OpMedicalCard }
func (o *MedicalCardChecksumOperator) Match(field, value string) bool {
	return validateMedicalCard(value)
}

// Icd10RangeOperator ICD-10 编码区间判定算子
type Icd10RangeOperator struct {
	start string // 敏感区间起始编码（如 "A00"）
	end   string // 敏感区间结束编码（如 "B99"）
}

func (o *Icd10RangeOperator) Type() OperatorType { return OpIcd10Range }
func (o *Icd10RangeOperator) Match(field, value string) bool {
	code := normalizeIcd10(value)
	if code == nil {
		return false
	}
	if o.start == "" || o.end == "" {
		// 仅校验是否为合法 ICD-10 编码
		return true
	}
	return inIcd10Interval(code, o.start, o.end)
}

// LuhnChecksumOperator Luhn 算法银行卡校验算子
type LuhnChecksumOperator struct {
	minLength int
	maxLength int
}

func (o *LuhnChecksumOperator) Type() OperatorType { return OpLuhnChecksum }
func (o *LuhnChecksumOperator) Match(field, value string) bool {
	minLen := o.minLength
	maxLen := o.maxLength
	if minLen == 0 {
		minLen = 13
	}
	if maxLen == 0 {
		maxLen = 19
	}
	return validateLuhn(value, minLen, maxLen)
}

// LengthRangeOperator 字符串长度范围匹配算子
type LengthRangeOperator struct {
	minLen int
	maxLen int // -1 表示无上限
}

func (o *LengthRangeOperator) Type() OperatorType { return OpLengthRange }
func (o *LengthRangeOperator) Match(field, value string) bool {
	l := len(value)
	if l < o.minLen {
		return false
	}
	if o.maxLen >= 0 && l > o.maxLen {
		return false
	}
	return true
}

// IpAddressOperator IPv4/IPv6 地址匹配算子
type IpAddressOperator struct{}

func (o *IpAddressOperator) Type() OperatorType { return OpIpAddress }
func (o *IpAddressOperator) Match(field, value string) bool {
	return isIpAddress(value)
}

// MacAddressOperator MAC 地址匹配算子
type MacAddressOperator struct{}

func (o *MacAddressOperator) Type() OperatorType { return OpMacAddress }
func (o *MacAddressOperator) Match(field, value string) bool {
	return isMacAddress(value)
}

// ChineseNameOperator 中文姓名匹配算子（2~4 字 CJK 统一表意文字）
type ChineseNameOperator struct{}

func (o *ChineseNameOperator) Type() OperatorType { return OpChineseName }
func (o *ChineseNameOperator) Match(field, value string) bool {
	return isChineseName(value)
}

// EmailOperator 电子邮箱匹配算子
type EmailOperator struct{}

func (o *EmailOperator) Type() OperatorType { return OpEmail }
func (o *EmailOperator) Match(field, value string) bool {
	return isEmail(value)
}

// ──────────────────────────────────────────────
// 校验辅助函数（与 Python operators.py 完全对齐）
// ──────────────────────────────────────────────

// 身份证权重因子（GB 11643-1999）
var idCardWeights = [17]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}

// 身份证校验字符映射
var idCardChars = [11]string{"1", "0", "X", "9", "8", "7", "6", "5", "4", "3", "2"}

// 上海医保卡权重因子
var shMedicalWeights = [8]int{7, 9, 10, 5, 8, 4, 2, 1}

var (
	idCardRegex  = regexp.MustCompile(`^[1-9]\d{5}(18|19|20)\d{2}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])\d{3}[\dXx]$`)
	medicalRegex = regexp.MustCompile(`^\d{9}$`)
	icd10Regex   = regexp.MustCompile(`^([A-Z])(\d{2})(?:\.?\d{0,2})?$`)
	ipv4Regex    = regexp.MustCompile(`^(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$`)
	ipv6Regex    = regexp.MustCompile(`^(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}$`)
	macRegex     = regexp.MustCompile(`^([0-9A-Fa-f]{2}[:-]){5}([0-9A-Fa-f]{2})$`)
	emailRegex   = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
)

// validateIdCard 校验中国大陆 18 位身份证号
func validateIdCard(value string) bool {
	if len(value) != 18 {
		return false
	}
	if !idCardRegex.MatchString(value) {
		return false
	}
	total := 0
	for i := 0; i < 17; i++ {
		d, err := strconv.Atoi(string(value[i]))
		if err != nil {
			return false
		}
		total += d * idCardWeights[i]
	}
	expected := idCardChars[total%11]
	return strings.ToUpper(string(value[17])) == expected
}

// validateMedicalCard 校验上海医保卡号 9 位校验码
func validateMedicalCard(value string) bool {
	if !medicalRegex.MatchString(value) {
		return false
	}
	digits := make([]int, 9)
	for i := 0; i < 9; i++ {
		digits[i] = int(value[i] - '0')
	}
	total := 0
	for i := 0; i < 8; i++ {
		total += digits[i] * shMedicalWeights[i]
	}
	expected := (10 - total%10) % 10
	return digits[8] == expected
}

// icd10Code 归一化后的 ICD-10 编码
type icd10Code struct {
	letter byte
	num    int
}

// normalizeIcd10 解析并归一化 ICD-10 编码
func normalizeIcd10(code string) *icd10Code {
	s := strings.ToUpper(strings.TrimSpace(code))
	if s == "" {
		return nil
	}
	m := icd10Regex.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	num, _ := strconv.Atoi(m[2])
	return &icd10Code{letter: m[1][0], num: num}
}

// inIcd10Interval 判断 ICD-10 编码是否落在闭区间内
func inIcd10Interval(code *icd10Code, start, end string) bool {
	startNorm := normalizeIcd10(start)
	endNorm := normalizeIcd10(end)
	if startNorm == nil || endNorm == nil {
		return false
	}
	// 元组比较：先字母后数字
	if code.letter < startNorm.letter || code.letter > endNorm.letter {
		return false
	}
	if code.letter == startNorm.letter && code.num < startNorm.num {
		return false
	}
	if code.letter == endNorm.letter && code.num > endNorm.num {
		return false
	}
	return true
}

// validateLuhn Luhn 算法校验
func validateLuhn(value string, minLen, maxLen int) bool {
	s := strings.TrimSpace(value)
	if len(s) < minLen || len(s) > maxLen {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	digits := make([]int, len(s))
	for i, c := range s {
		digits[i] = int(c - '0')
	}
	oddSum := 0
	for i := len(digits) - 1; i >= 0; i -= 2 {
		oddSum += digits[i]
	}
	evenSum := 0
	for i := len(digits) - 2; i >= 0; i -= 2 {
		d := 2 * digits[i]
		if d > 9 {
			d -= 9
		}
		evenSum += d
	}
	return (oddSum+evenSum)%10 == 0
}

// isIpAddress 判断是否为 IPv4 或 IPv6 地址
func isIpAddress(value string) bool {
	s := strings.TrimSpace(value)
	if s == "" {
		return false
	}
	return ipv4Regex.MatchString(s) || ipv6Regex.MatchString(s)
}

// isMacAddress 判断是否为 MAC 地址
func isMacAddress(value string) bool {
	s := strings.TrimSpace(value)
	if s == "" {
		return false
	}
	return macRegex.MatchString(s)
}

// isChineseName 判断是否为中文姓名（2~4 字 CJK 统一表意文字）
func isChineseName(value string) bool {
	s := strings.TrimSpace(value)
	runes := []rune(s)
	if len(runes) < 2 || len(runes) > 4 {
		return false
	}
	for _, r := range runes {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}

// isEmail 判断是否为电子邮箱地址
func isEmail(value string) bool {
	s := strings.TrimSpace(value)
	if s == "" {
		return false
	}
	return emailRegex.MatchString(s)
}

// ──────────────────────────────────────────────
// 算子注册表
// ──────────────────────────────────────────────

// OperatorRegistry 算子注册表
type OperatorRegistry struct {
	mu        sync.RWMutex
	operators map[OperatorType]func(args ...string) Operator
}

// NewOperatorRegistry 创建算子注册表
func NewOperatorRegistry() *OperatorRegistry {
	r := &OperatorRegistry{
		operators: make(map[OperatorType]func(args ...string) Operator),
	}
	// 注册内置算子
	r.Register(OpRegex, func(args ...string) Operator {
		if len(args) == 0 {
			return nil
		}
		return &RegexOperator{pattern: args[0]}
	})
	r.Register(OpContains, func(args ...string) Operator {
		if len(args) == 0 {
			return nil
		}
		return &ContainsOperator{substr: args[0]}
	})
	r.Register(OpEquals, func(args ...string) Operator {
		if len(args) == 0 {
			return nil
		}
		return &EqualsOperator{target: args[0]}
	})
	r.Register(OpStartsWith, func(args ...string) Operator {
		if len(args) == 0 {
			return nil
		}
		return &StartsWithOperator{prefix: args[0]}
	})
	r.Register(OpEndsWith, func(args ...string) Operator {
		if len(args) == 0 {
			return nil
		}
		return &EndsWithOperator{suffix: args[0]}
	})
	r.Register(OpFieldMatch, func(args ...string) Operator {
		if len(args) == 0 {
			return nil
		}
		return &FieldMatchOperator{pattern: args[0]}
	})
	// 注册校验算子
	r.Register(OpIdCardChecksum, func(args ...string) Operator {
		return &IdCardChecksumOperator{}
	})
	r.Register(OpMedicalCard, func(args ...string) Operator {
		return &MedicalCardChecksumOperator{}
	})
	r.Register(OpIcd10Range, func(args ...string) Operator {
		if len(args) < 2 {
			return &Icd10RangeOperator{} // 仅校验合法性
		}
		return &Icd10RangeOperator{start: args[0], end: args[1]}
	})
	r.Register(OpLuhnChecksum, func(args ...string) Operator {
		minLen, maxLen := 13, 19
		if len(args) > 0 {
			if v, err := strconv.Atoi(args[0]); err == nil {
				minLen = v
			}
		}
		if len(args) > 1 {
			if v, err := strconv.Atoi(args[1]); err == nil {
				maxLen = v
			}
		}
		return &LuhnChecksumOperator{minLength: minLen, maxLength: maxLen}
	})
	r.Register(OpLengthRange, func(args ...string) Operator {
		minLen, maxLen := 0, -1
		if len(args) > 0 {
			if v, err := strconv.Atoi(args[0]); err == nil {
				minLen = v
			}
		}
		if len(args) > 1 {
			if v, err := strconv.Atoi(args[1]); err == nil {
				maxLen = v
			}
		}
		return &LengthRangeOperator{minLen: minLen, maxLen: maxLen}
	})
	r.Register(OpIpAddress, func(args ...string) Operator {
		return &IpAddressOperator{}
	})
	r.Register(OpMacAddress, func(args ...string) Operator {
		return &MacAddressOperator{}
	})
	r.Register(OpChineseName, func(args ...string) Operator {
		return &ChineseNameOperator{}
	})
	r.Register(OpEmail, func(args ...string) Operator {
		return &EmailOperator{}
	})
	return r
}

// Register 注册自定义算子
func (r *OperatorRegistry) Register(opType OperatorType, factory func(args ...string) Operator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.operators[opType] = factory
}

// Create 创建算子实例
func (r *OperatorRegistry) Create(opType OperatorType, args ...string) Operator {
	r.mu.RLock()
	defer r.mu.RUnlock()
	factory, ok := r.operators[opType]
	if !ok {
		return nil
	}
	return factory(args...)
}

// ListOperators 返回所有已注册算子类型
func (r *OperatorRegistry) ListOperators() []OperatorType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]OperatorType, 0, len(r.operators))
	for t := range r.operators {
		types = append(types, t)
	}
	return types
}

// ──────────────────────────────────────────────
// 辅助函数
// ──────────────────────────────────────────────

type boundedRegexCache struct {
	mu      sync.RWMutex
	entries map[string]*regexp.Regexp
	maxSize int
}

func newBoundedRegexCache(maxSize int) *boundedRegexCache {
	return &boundedRegexCache{
		entries: make(map[string]*regexp.Regexp, maxSize),
		maxSize: maxSize,
	}
}

func (c *boundedRegexCache) getOrCompile(pattern string) (*regexp.Regexp, error) {
	c.mu.RLock()
	re, ok := c.entries[pattern]
	c.mu.RUnlock()
	if ok {
		return re, nil
	}

	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxSize {
		// 达到上限时清理一半容量，防止无界内存增长
		count := 0
		target := c.maxSize / 2
		for k := range c.entries {
			delete(c.entries, k)
			count++
			if count >= target {
				break
			}
		}
	}
	c.entries[pattern] = compiled
	return compiled, nil
}

var globalRegexCache = newBoundedRegexCache(1024)

// matchRegex 使用有界缓存的正则匹配（线程安全、防内存泄漏）
func matchRegex(pattern, text string) bool {
	re, err := globalRegexCache.getOrCompile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(text)
}
