package fts

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Token represents a single token emitted by the tokenizer.
type Token struct {
	Term     string // lowercased term
	Position int    // 0-based position in the token stream
}

// Tokenize splits text into a stream of searchable tokens.
//
// The tokenizer applies the following rules in order:
//  1. Split on non-alphanumeric boundaries (whitespace, punctuation).
//  2. For each word, split camelCase into sub-tokens (e.g., "parseJSON" →
//     ["parse", "json", "parsejson"]).
//  3. Split snake_case on underscores (e.g., "error_code" →
//     ["error", "code", "error_code"]).
//  4. For CJK runs, emit overlapping bigrams (per ADR-0001).
//  5. All tokens are lowercased.
//
// Both the whole compound and its parts are emitted so that queries for
// either "parseJSON" or "parse" match.
func Tokenize(text string) []Token {
	var tokens []Token
	pos := 0

	words := splitWords(text)
	for _, w := range words {
		if len(w) == 0 {
			continue
		}

		// Check if the word contains CJK characters.
		if containsCJK(w) {
			toks := tokenizeMixed(w, &pos)
			tokens = append(tokens, toks...)
			continue
		}

		// Split camelCase and snake_case.
		parts := splitCompound(w)

		// Emit the whole word (lowered) if it differs from a single part.
		lower := strings.ToLower(w)
		if len(parts) > 1 {
			for _, p := range parts {
				tokens = append(tokens, Token{Term: strings.ToLower(p), Position: pos})
				pos++
			}
			// Emit the whole compound.
			tokens = append(tokens, Token{Term: lower, Position: pos})
			pos++
		} else {
			tokens = append(tokens, Token{Term: lower, Position: pos})
			pos++
		}
	}

	return tokens
}

// splitWords splits text on non-alphanumeric, non-CJK boundaries.
func splitWords(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		if r == '_' {
			return false // keep underscores for snake_case handling
		}
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// splitCompound splits a word on camelCase and snake_case boundaries.
// Returns the sub-parts (without underscores).
func splitCompound(word string) []string {
	// First split on underscores (snake_case).
	snakeParts := strings.Split(word, "_")

	var allParts []string
	for _, sp := range snakeParts {
		if sp == "" {
			continue
		}
		// Then split on camelCase boundaries.
		camelParts := splitCamel(sp)
		allParts = append(allParts, camelParts...)
	}

	return allParts
}

// splitCamel splits a string on camelCase boundaries.
// "parseJSON" → ["parse", "JSON"]
// "HTTPServer" → ["HTTP", "Server"]
// "simpleWord" → ["simple", "Word"]
func splitCamel(s string) []string {
	if len(s) == 0 {
		return nil
	}

	var parts []string
	runes := []rune(s)
	start := 0

	for i := 1; i < len(runes); i++ {
		prev := runes[i-1]
		curr := runes[i]

		split := false

		// Split: lower → Upper (e.g., "parseJ" at 'J')
		if unicode.IsLower(prev) && unicode.IsUpper(curr) {
			split = true
		}

		// Split: Upper → Upper + lower (e.g., "HTTPServer" — split before 'S')
		if i+1 < len(runes) && unicode.IsUpper(prev) && unicode.IsUpper(curr) && unicode.IsLower(runes[i+1]) {
			split = true
		}

		// Split: letter → digit or digit → letter
		if unicode.IsLetter(prev) && unicode.IsDigit(curr) {
			split = true
		}
		if unicode.IsDigit(prev) && unicode.IsLetter(curr) {
			split = true
		}

		if split {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}

	parts = append(parts, string(runes[start:]))
	return parts
}

// containsCJK reports whether s contains any CJK codepoint.
func containsCJK(s string) bool {
	for _, r := range s {
		if isCJK(r) {
			return true
		}
	}
	return false
}

// isCJK reports whether r is a CJK character (Han, Hiragana, Katakana, Hangul)
// or a CJK mark commonly embedded in CJK runs (e.g., the prolonged sound mark
// ー U+30FC which has Script=Common in Unicode).
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) ||
		(r >= 0x30FC && r <= 0x30FE) || // ー ヽ ヾ (prolonged sound, iteration marks)
		(r >= 0x309D && r <= 0x309E) // ゝ ゞ (Hiragana iteration marks)
}

// tokenizeMixed handles text that contains both CJK and non-CJK characters.
// CJK runs are tokenized as bigrams; non-CJK runs use the standard pipeline.
func tokenizeMixed(word string, pos *int) []Token {
	var tokens []Token
	var buf []rune

	flushNonCJK := func() {
		if len(buf) == 0 {
			return
		}
		s := string(buf)
		parts := splitCompound(s)
		lower := strings.ToLower(s)
		if len(parts) > 1 {
			for _, p := range parts {
				tokens = append(tokens, Token{Term: strings.ToLower(p), Position: *pos})
				*pos++
			}
			tokens = append(tokens, Token{Term: lower, Position: *pos})
			*pos++
		} else {
			tokens = append(tokens, Token{Term: lower, Position: *pos})
			*pos++
		}
		buf = buf[:0]
	}

	var cjkBuf []rune

	flushCJK := func() {
		if len(cjkBuf) == 0 {
			return
		}
		// Emit bigrams.
		for i := 0; i+1 < len(cjkBuf); i++ {
			bigram := string(cjkBuf[i : i+2])
			tokens = append(tokens, Token{Term: bigram, Position: *pos})
			*pos++
		}
		// If only one CJK char or trailing single, emit unigram.
		if len(cjkBuf) == 1 {
			tokens = append(tokens, Token{Term: string(cjkBuf[0]), Position: *pos})
			*pos++
		}
		cjkBuf = cjkBuf[:0]
	}

	for i := 0; i < len(word); {
		r, size := utf8.DecodeRuneInString(word[i:])
		i += size

		if isCJK(r) {
			flushNonCJK()
			cjkBuf = append(cjkBuf, r)
		} else {
			flushCJK()
			buf = append(buf, r)
		}
	}

	flushNonCJK()
	flushCJK()

	return tokens
}
