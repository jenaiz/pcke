package fts_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jenaiz/pcke/internal/kdb/index/fts"
)

func TestTokenizeBasicWords(t *testing.T) {
	tokens := fts.Tokenize("hello world")
	terms := extractTerms(tokens)

	assertContains(t, terms, "hello")
	assertContains(t, terms, "world")
}

func TestTokenizeCamelCase(t *testing.T) {
	tokens := fts.Tokenize("parseJSON")
	terms := extractTerms(tokens)

	assertContains(t, terms, "parse")
	assertContains(t, terms, "json")
	assertContains(t, terms, "parsejson")
}

func TestTokenizeHTTPAcronym(t *testing.T) {
	tokens := fts.Tokenize("HTTPServer")
	terms := extractTerms(tokens)

	assertContains(t, terms, "http")
	assertContains(t, terms, "server")
	assertContains(t, terms, "httpserver")
}

func TestTokenizeSnakeCase(t *testing.T) {
	tokens := fts.Tokenize("error_code")
	terms := extractTerms(tokens)

	assertContains(t, terms, "error")
	assertContains(t, terms, "code")
	assertContains(t, terms, "error_code")
}

func TestTokenizeMixedCamelSnake(t *testing.T) {
	tokens := fts.Tokenize("myFunc_name")
	terms := extractTerms(tokens)

	assertContains(t, terms, "my")
	assertContains(t, terms, "func")
	assertContains(t, terms, "name")
}

func TestTokenizeCJKBigrams(t *testing.T) {
	// "東京都" should produce bigrams: "東京", "京都"
	tokens := fts.Tokenize("東京都")
	terms := extractTerms(tokens)

	assertContains(t, terms, "東京")
	assertContains(t, terms, "京都")
}

func TestTokenizeCJKSingle(t *testing.T) {
	// Single CJK char → unigram.
	tokens := fts.Tokenize("猫")
	terms := extractTerms(tokens)

	assertContains(t, terms, "猫")
}

func TestTokenizeHiragana(t *testing.T) {
	// Hiragana: "さくら" → bigrams
	tokens := fts.Tokenize("さくら")
	terms := extractTerms(tokens)

	assertContains(t, terms, "さく")
	assertContains(t, terms, "くら")
}

func TestTokenizeKatakana(t *testing.T) {
	// Katakana: "サーバ" → bigrams
	tokens := fts.Tokenize("サーバ")
	terms := extractTerms(tokens)

	assertContains(t, terms, "サー")
	assertContains(t, terms, "ーバ")
}

func TestTokenizeHangul(t *testing.T) {
	// Hangul: "서울시" → bigrams
	tokens := fts.Tokenize("서울시")
	terms := extractTerms(tokens)

	assertContains(t, terms, "서울")
	assertContains(t, terms, "울시")
}

func TestTokenizeMixedCJKLatin(t *testing.T) {
	// Mixed: "hello東京world"
	tokens := fts.Tokenize("hello東京world")
	terms := extractTerms(tokens)

	assertContains(t, terms, "hello")
	assertContains(t, terms, "東京")
	assertContains(t, terms, "world")
}

func TestTokenizeLowercasing(t *testing.T) {
	tokens := fts.Tokenize("Hello WORLD FooBar")
	terms := extractTerms(tokens)

	for _, term := range terms {
		if term != strings.ToLower(term) {
			// CJK characters are already lowercase-invariant, skip them.
			for _, r := range term {
				if r >= 'A' && r <= 'Z' {
					t.Errorf("term %q not lowercased", term)
					break
				}
			}
		}
	}
}

func TestTokenizeDigits(t *testing.T) {
	tokens := fts.Tokenize("error404 http2")
	terms := extractTerms(tokens)

	assertContains(t, terms, "error")
	assertContains(t, terms, "404")
	assertContains(t, terms, "http")
	assertContains(t, terms, "2")
}

func TestTokenizeEmptyString(t *testing.T) {
	tokens := fts.Tokenize("")
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens for empty string, got %d", len(tokens))
	}
}

func TestTokenizePunctuation(t *testing.T) {
	tokens := fts.Tokenize("foo.bar(baz)")
	terms := extractTerms(tokens)

	assertContains(t, terms, "foo")
	assertContains(t, terms, "bar")
	assertContains(t, terms, "baz")
}

func TestTokenizePositionsMonotonic(t *testing.T) {
	tokens := fts.Tokenize("the quick brown fox parseJSON error_code 東京都")
	for i := 1; i < len(tokens); i++ {
		if tokens[i].Position <= tokens[i-1].Position {
			t.Errorf("position not monotonically increasing at index %d: %d <= %d",
				i, tokens[i].Position, tokens[i-1].Position)
		}
	}
}

func TestTokenizeCodeSnippet(t *testing.T) {
	code := `func handleHTTPRequest(ctx context.Context, req *http.Request) error`
	tokens := fts.Tokenize(code)
	terms := extractTerms(tokens)

	assertContains(t, terms, "handle")
	assertContains(t, terms, "http")
	assertContains(t, terms, "request")
	assertContains(t, terms, "ctx")
	assertContains(t, terms, "context")
	assertContains(t, terms, "req")
	assertContains(t, terms, "error")
}

func TestTokenizeMixedCJKCompound(t *testing.T) {
	// Mixed CJK text with camelCase compound words — triggers tokenizeMixed's
	// compound split path for non-CJK runs within CJK text.
	text := "東京handleHTTPRequest北京"
	tokens := fts.Tokenize(text)
	terms := extractTerms(tokens)

	assertContains(t, terms, "handle")
	assertContains(t, terms, "http")
	assertContains(t, terms, "request")
	// Should also have CJK bigrams.
	if len(tokens) < 5 {
		t.Errorf("expected >= 5 tokens, got %d: %v", len(tokens), terms)
	}
}

func TestTokenizeNullByte(t *testing.T) {
	// Ensure tokenizer doesn't panic on null bytes.
	tokens := fts.Tokenize("hello\x00world")
	if len(tokens) == 0 {
		t.Error("expected some tokens from null-containing string")
	}
}

func FuzzTokenizer(f *testing.F) {
	f.Add("hello world")
	f.Add("parseJSON")
	f.Add("error_code_42")
	f.Add("東京都サーバ")
	f.Add("hello東京world")
	f.Add("")
	f.Add("HTTPServer")
	f.Add("a")
	f.Add("___")
	f.Add("CamelCase_and_snake_case")

	f.Fuzz(func(t *testing.T, input string) {
		// Must not panic.
		tokens := fts.Tokenize(input)

		// All terms must be valid UTF-8.
		for _, tok := range tokens {
			if !utf8.ValidString(tok.Term) {
				t.Errorf("invalid UTF-8 in token: %q", tok.Term)
			}
		}

		// Positions must be non-negative.
		for _, tok := range tokens {
			if tok.Position < 0 {
				t.Errorf("negative position: %d", tok.Position)
			}
		}
	})
}

// --- helpers ---

func extractTerms(tokens []fts.Token) []string {
	terms := make([]string, len(tokens))
	for i, t := range tokens {
		terms[i] = t.Term
	}
	return terms
}

func assertContains(t *testing.T, terms []string, want string) {
	t.Helper()
	for _, term := range terms {
		if term == want {
			return
		}
	}
	t.Errorf("terms %v does not contain %q", terms, want)
}
