package retrieval

import (
	"math"
	"strings"
)

// DefaultBudget is the engine-wide default approximate-token ceiling
// when Request.Budget == 0. Matches PRD v5.2 §4.3.
const DefaultBudget = 2000

// tokensPerWord is the linear approximation we use to convert word
// counts into approximate token counts. PRD v5.2 §4.3 calls for
// "word count × 1.3" — slightly conservative versus tiktoken on
// English prose, and leaves us no tokenizer dependency.
const tokensPerWord = 1.3

// TokensFor returns the approximate token cost of a body string.
// Counts are rounded up: a single-word string costs 2 tokens (1×1.3
// → ceil(1.3) = 2), an empty string costs 0.
//
// Word boundary is any whitespace run (Unicode-aware). This means
// CamelCase and snake_case identifiers count as a single word —
// that's the intended behaviour: an identifier is one "concept" in
// the prose-budget sense, even if a real tokenizer would split it.
// On English bodies the approximation is within ~10–15% of tiktoken;
// on identifier-heavy bodies it under-counts, but not by enough to
// matter for "fit N items into 2000 tokens".
func TokensFor(body string) int {
	if body == "" {
		return 0
	}
	words := len(strings.Fields(body))
	if words == 0 {
		return 0
	}
	return int(math.Ceil(float64(words) * tokensPerWord))
}

// EffectiveBudget returns the budget the engine should enforce for a
// request: the request's value if positive, otherwise DefaultBudget.
func EffectiveBudget(req Request) int {
	if req.Budget > 0 {
		return req.Budget
	}
	return DefaultBudget
}

// FitToBudget greedily admits sections in their supplied order until
// adding another would cross limit. Returns the admitted slice plus a
// flag indicating whether at least one section was excluded.
//
// Callers should pass sections sorted by descending Score so the
// highest-ranked items survive truncation. FitToBudget itself does
// not sort.
func FitToBudget(sections []Section, limit int) ([]Section, bool) {
	if limit <= 0 {
		// No room for anything — admit nothing.
		return nil, len(sections) > 0
	}
	used := 0
	out := make([]Section, 0, len(sections))
	truncated := false
	for _, s := range sections {
		if used+s.Tokens > limit {
			truncated = true
			continue
		}
		out = append(out, s)
		used += s.Tokens
	}
	return out, truncated
}
