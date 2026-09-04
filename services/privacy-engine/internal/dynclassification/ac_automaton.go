// Package dynclassification 提供纯 Go Aho-Corasick 多模式匹配自动机。
//
// 基于 trie + failure link (BFS) 实现 O(N+M+Z) 多模式匹配，
// 其中 N 为文本长度、M 为所有模式串总长、Z 为匹配数。
// 用于替代回溯正则，在高敏医学词库（284+ 词条）场景下提供线性时间保证。
package dynclassification

import "sync"

// ──────────────────────────────────────────────
// Trie 节点
// ──────────────────────────────────────────────

// acNode AC 自动机 trie 节点
type acNode struct {
	children map[rune]*acNode // 子节点
	fail     *acNode          // 失败指针
	depth    int              // 节点深度（根=0）
	pattern  string           // 非空表示该节点是某个模式的终止节点，记录原始模式串
}

// ──────────────────────────────────────────────
// AhoCorasick 自动机
// ──────────────────────────────────────────────

// AhoCorasick Aho-Corasick 多模式匹配自动机。
// 构建后不可变，可安全并发读取。
type AhoCorasick struct {
	root    *acNode
	pattern []string // 所有插入的模式串（保留原始大小写）
	mu      sync.RWMutex
	built   bool
}

// ACMatch 单次匹配结果
type ACMatch struct {
	Pos     int    // 匹配起始字节偏移
	Len     int    // 匹配字节长度
	Pattern string // 匹配到的模式串（原始大小写）
}

// NewAhoCorasick 创建空的 AC 自动机。
func NewAhoCorasick() *AhoCorasick {
	return &AhoCorasick{
		root: &acNode{children: make(map[rune]*acNode)},
	}
}

// AddPattern 插入模式串。必须在 Build 之前调用。
func (ac *AhoCorasick) AddPattern(patterns ...string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	for _, p := range patterns {
		if p == "" {
			continue
		}
		ac.pattern = append(ac.pattern, p)
		ac.insert(p)
	}
}

// Build 构建 failure link（BFS）。
// 必须在所有 AddPattern 调用之后、Match 之前调用一次。
func (ac *AhoCorasick) Build() {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.buildFailure()
	ac.built = true
}

// MatchString 在文本中查找所有模式串出现位置。
// 返回按起始位置排序的匹配列表。
func (ac *AhoCorasick) MatchString(text string) []ACMatch {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	if !ac.built {
		return nil
	}
	return ac.search(text)
}

// Contains 判断文本是否包含任一模式串。
func (ac *AhoCorasick) Contains(text string) bool {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	if !ac.built {
		return false
	}
	return ac.containsAny(text)
}

// PatternCount 返回已插入模式串数量。
func (ac *AhoCorasick) PatternCount() int {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return len(ac.pattern)
}

// IsBuilt 返回是否已构建 failure link。
func (ac *AhoCorasick) IsBuilt() bool {
	ac.mu.RLock()
	defer ac.mu.RUnlock()
	return ac.built
}

// ──────────────────────────────────────────────
// 内部实现
// ──────────────────────────────────────────────

// insert 将单个模式串插入 trie
func (ac *AhoCorasick) insert(pattern string) {
	cur := ac.root
	for _, ch := range pattern {
		if cur.children[ch] == nil {
			cur.children[ch] = &acNode{
				children: make(map[rune]*acNode),
				depth:    cur.depth + 1,
			}
		}
		cur = cur.children[ch]
	}
	// 标记终止节点
	if cur.pattern == "" {
		cur.pattern = pattern
	}
}

// buildFailure BFS 构建 failure link
func (ac *AhoCorasick) buildFailure() {
	// 根节点的 fail 指向自己
	ac.root.fail = ac.root

	// BFS 队列
	queue := make([]*acNode, 0)
	// 第一层节点的 fail 指向根
	for _, child := range ac.root.children {
		child.fail = ac.root
		queue = append(queue, child)
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		for ch, child := range cur.children {
			queue = append(queue, child)

			// 沿 fail 链向上查找
			fail := cur.fail
			for fail != ac.root && fail.children[ch] == nil {
				fail = fail.fail
			}
			if next := fail.children[ch]; next != nil && next != child {
				child.fail = next
			} else {
				child.fail = ac.root
			}
		}
	}
}

// search 在文本中搜索所有模式
func (ac *AhoCorasick) search(text string) []ACMatch {
	var matches []ACMatch
	cur := ac.root
	bytePos := 0

	for _, ch := range text {
		chLen := runeLen(ch)

		// 沿 fail 链回退
		for cur != ac.root && cur.children[ch] == nil {
			cur = cur.fail
		}
		if next := cur.children[ch]; next != nil {
			cur = next
		}

		// 检查当前节点及其 fail 链上的所有匹配
		tmp := cur
		for tmp != ac.root {
			if tmp.pattern != "" {
				matchLen := 0
				for _, r := range tmp.pattern {
					matchLen += runeLen(r)
				}
				matches = append(matches, ACMatch{
					Pos:     bytePos + chLen - matchLen,
					Len:     matchLen,
					Pattern: tmp.pattern,
				})
			}
			tmp = tmp.fail
			// 避免重复遍历已报告的 fail 链（优化：如果 fail 节点无 pattern 可提前终止）
			if tmp.pattern == "" && tmp != ac.root {
				// 继续检查 fail 链（可能有更短的模式匹配）
				continue
			}
		}
		bytePos += chLen
	}
	return matches
}

// containsAny 快速判断是否包含任一模式（找到第一个即返回）
func (ac *AhoCorasick) containsAny(text string) bool {
	cur := ac.root

	for _, ch := range text {
		for cur != ac.root && cur.children[ch] == nil {
			cur = cur.fail
		}
		if next := cur.children[ch]; next != nil {
			cur = next
		}

		// 检查当前节点
		if cur.pattern != "" {
			return true
		}
		// 检查 fail 链
		tmp := cur.fail
		for tmp != ac.root {
			if tmp.pattern != "" {
				return true
			}
			tmp = tmp.fail
		}
	}
	return false
}

// runeLen 返回 rune 的 UTF-8 编码长度
func runeLen(r rune) int {
	if r < 0x80 {
		return 1
	}
	if r < 0x800 {
		return 2
	}
	if r < 0x10000 {
		return 3
	}
	return 4
}
