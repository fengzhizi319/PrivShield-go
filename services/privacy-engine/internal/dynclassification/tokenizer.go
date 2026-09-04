// Package dynclassification 提供三层动态分类分级引擎扩展。
//
// tokenizer.go — 中文 BERT WordPiece Tokenizer + Offset Mapping
package dynclassification

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ──────────────────────────────────────────────
// WordPiece Tokenizer
// ──────────────────────────────────────────────

// Tokenizer WordPiece 分词器
type Tokenizer struct {
	vocab    map[string]int // 词表：token → ID
	unkToken string
	unkID    int
	clsToken string
	clsID    int
	sepToken string
	sepID    int
	maxLen   int
}

// TokenResult 分词结果
type TokenResult struct {
	InputIDs      []int64
	AttentionMask []int64
	TokenTypeIDs  []int64
	Offsets       []TokenOffset
	Tokens        []string
}

// TokenOffset 每个 token 对应的原文偏移量
type TokenOffset struct {
	Start int // 起始字节偏移
	End   int // 结束字节偏移
}

// NewTokenizer 创建 Tokenizer 实例
func NewTokenizer(vocab map[string]int, maxLen int) *Tokenizer {
	t := &Tokenizer{
		vocab:    vocab,
		unkToken: "[UNK]",
		clsToken: "[CLS]",
		sepToken: "[SEP]",
		maxLen:   maxLen,
	}
	if id, ok := vocab["[UNK]"]; ok {
		t.unkID = id
	}
	if id, ok := vocab["[CLS]"]; ok {
		t.clsID = id
	}
	if id, ok := vocab["[SEP]"]; ok {
		t.sepID = id
	}
	return t
}

// NewSimpleTokenizer 创建简化版 Tokenizer（字符级分词）
func NewSimpleTokenizer(maxLen int) *Tokenizer {
	vocab := make(map[string]int)
	// 基础词表：ASCII + 常用中文标点
	for i := 0; i < 128; i++ {
		vocab[string(rune(i))] = i
	}
	// 特殊 token
	vocab["[UNK]"] = 100
	vocab["[CLS]"] = 101
	vocab["[SEP]"] = 102
	vocab["[PAD]"] = 0
	vocab["##"] = 5 // subword 前缀
	return NewTokenizer(vocab, maxLen)
}

// Encode 将文本编码为 token IDs
func (t *Tokenizer) Encode(text string) *TokenResult {
	text = t.preprocess(text)
	tokens := t.tokenize(text)
	return t.buildResult(tokens, text)
}

// EncodeWithOffsets 编码并返回 offset mapping
func (t *Tokenizer) EncodeWithOffsets(text string) (inputIDs, attentionMask, typeIDs []int64, offsets []TokenOffset) {
	result := t.Encode(text)
	return result.InputIDs, result.AttentionMask, result.TokenTypeIDs, result.Offsets
}

// preprocess 文本预处理
func (t *Tokenizer) preprocess(text string) string {
	// 1. Unicode 规范化（NFC）
	// 2. 小写化
	text = strings.ToLower(text)
	// 3. 去除控制字符
	var sb strings.Builder
	for _, r := range text {
		if !unicode.IsControl(r) || r == '\n' || r == '\t' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// tokenize 将文本切分为 token 列表
func (t *Tokenizer) tokenize(text string) []TokenWithOffset {
	var tokens []TokenWithOffset
	// 按 Unicode 字符边界切分
	runes := []rune(text)
	byteOffset := 0

	for _, r := range runes {
		char := string(r)
		charLen := utf8.RuneLen(r)

		if id, ok := t.vocab[char]; ok && id != t.unkID {
			tokens = append(tokens, TokenWithOffset{
				Token: char,
				ID:    id,
				Start: byteOffset,
				End:   byteOffset + charLen,
			})
		} else {
			// 尝试 subword 切分
			subTokens := t.tokenizeSubword(char)
			charStart := byteOffset
			for _, st := range subTokens {
				tokens = append(tokens, TokenWithOffset{
					Token: st.Token,
					ID:    st.ID,
					Start: charStart,
					End:   byteOffset + charLen,
				})
			}
		}
		byteOffset += charLen
	}
	return tokens
}

// tokenizeSubword 对单个字符尝试 subword 切分
func (t *Tokenizer) tokenizeSubword(char string) []TokenWithOffset {
	// 简化实现：如果字符不在词表中，返回 [UNK]
	return []TokenWithOffset{
		{Token: t.unkToken, ID: t.unkID},
	}
}

// buildResult 构建最终编码结果
func (t *Tokenizer) buildResult(tokens []TokenWithOffset, originalText string) *TokenResult {
	result := &TokenResult{
		Tokens: make([]string, 0, len(tokens)+2),
	}

	// [CLS] 开头
	result.Tokens = append(result.Tokens, t.clsToken)
	result.InputIDs = append(result.InputIDs, int64(t.clsID))
	result.AttentionMask = append(result.AttentionMask, 1)
	result.TokenTypeIDs = append(result.TokenTypeIDs, 0)
	result.Offsets = append(result.Offsets, TokenOffset{Start: 0, End: 0})

	// 实际 tokens
	for _, tok := range tokens {
		if len(result.InputIDs) >= t.maxLen-1 {
			break
		}
		result.Tokens = append(result.Tokens, tok.Token)
		result.InputIDs = append(result.InputIDs, int64(tok.ID))
		result.AttentionMask = append(result.AttentionMask, 1)
		result.TokenTypeIDs = append(result.TokenTypeIDs, 0)
		result.Offsets = append(result.Offsets, TokenOffset{Start: tok.Start, End: tok.End})
	}

	// [SEP] 结尾
	result.Tokens = append(result.Tokens, t.sepToken)
	result.InputIDs = append(result.InputIDs, int64(t.sepID))
	result.AttentionMask = append(result.AttentionMask, 1)
	result.TokenTypeIDs = append(result.TokenTypeIDs, 0)
	result.Offsets = append(result.Offsets, TokenOffset{Start: len(originalText), End: len(originalText)})

	// Padding
	for len(result.InputIDs) < t.maxLen {
		result.InputIDs = append(result.InputIDs, 0)
		result.AttentionMask = append(result.AttentionMask, 0)
		result.TokenTypeIDs = append(result.TokenTypeIDs, 0)
		result.Offsets = append(result.Offsets, TokenOffset{Start: 0, End: 0})
	}

	return result
}

// TokenWithOffset 带偏移量的 token
type TokenWithOffset struct {
	Token string
	ID    int
	Start int
	End   int
}

// DecodeTokenIDs 将 token IDs 解码回文本（调试用）
func (t *Tokenizer) DecodeTokenIDs(ids []int64) string {
	idToToken := make(map[int]string, len(t.vocab))
	for token, id := range t.vocab {
		idToToken[id] = token
	}

	var parts []string
	for _, id := range ids {
		if id == int64(t.clsID) || id == int64(t.sepID) || id == 0 {
			continue
		}
		if token, ok := idToToken[int(id)]; ok {
			parts = append(parts, token)
		}
	}
	return strings.Join(parts, "")
}

// VocabSize 返回词表大小
func (t *Tokenizer) VocabSize() int {
	return len(t.vocab)
}
