package context

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	text := "hello world foo bar baz"
	tokens := EstimateTokens(text, 1.3)
	// 5 words × 1.3 = 6.5 → 6
	if tokens != 6 {
		t.Fatalf("expected 6 tokens, got %d", tokens)
	}
}

func TestEstimateTokens_Empty(t *testing.T) {
	tokens := EstimateTokens("", 1.3)
	if tokens != 0 {
		t.Fatalf("expected 0 tokens for empty string, got %d", tokens)
	}
}

func TestEstimateTokens_DefaultMultiplier(t *testing.T) {
	text := "one two three"
	tokens := EstimateTokens(text, 0) // 0 → default 1.3
	// 3 words × 1.3 = 3.9 → 3
	if tokens != 3 {
		t.Fatalf("expected 3 tokens, got %d", tokens)
	}
}

func TestTruncateToBudget_AllFit(t *testing.T) {
	sections := []Section{
		{Type: "constraint", Title: "Rule A", Content: "short content"},
		{Type: "history", Title: "Change B", Content: "also short"},
	}
	result, tokensUsed := TruncateToBudget(sections, 1000, 1.3)
	if len(result) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(result))
	}
	if tokensUsed <= 0 {
		t.Fatal("tokensUsed should be > 0")
	}
}

func TestTruncateToBudget_Truncation(t *testing.T) {
	// Create a section with many lines of content.
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "word word word word") // 4 words per line
	}
	longContent := strings.Join(lines, "\n") // 50 lines × 4 words = 200 words → ~260 tokens
	sections := []Section{
		{Type: "constraint", Title: "Rule A", Content: longContent},
	}
	result, tokensUsed := TruncateToBudget(sections, 50, 1.3)
	if len(result) != 1 {
		t.Fatalf("expected 1 section, got %d", len(result))
	}
	if tokensUsed > 55 { // Allow ±10%
		t.Fatalf("tokensUsed %d exceeds budget 50 by too much", tokensUsed)
	}
}

func TestTruncateToBudget_ZeroBudget(t *testing.T) {
	sections := []Section{
		{Type: "constraint", Title: "Rule", Content: "content"},
	}
	result, _ := TruncateToBudget(sections, 0, 1.3)
	// Should use default budget (2000) and include at least 1 section.
	if len(result) < 1 {
		t.Fatal("expected at least 1 section with default budget")
	}
}

func TestTruncateToBudget_Empty(t *testing.T) {
	result, tokensUsed := TruncateToBudget(nil, 1000, 1.3)
	if len(result) != 0 {
		t.Fatalf("expected 0 sections for nil input, got %d", len(result))
	}
	if tokensUsed != 0 {
		t.Fatalf("expected 0 tokens for nil input, got %d", tokensUsed)
	}
}

func TestTruncateToBudget_MultiplePartialFit(t *testing.T) {
	sections := []Section{
		{Type: "constraint", Title: "A", Content: "one two three four five"},    // 5 words → 6 tokens
		{Type: "history", Title: "B", Content: "six seven eight nine ten"},      // 5 words → 6 tokens
		{Type: "impact", Title: "C", Content: strings.Repeat("longword ", 100)}, // Won't fit in remaining.
	}
	result, tokensUsed := TruncateToBudget(sections, 15, 1.3)
	if len(result) < 2 {
		t.Fatalf("expected at least 2 sections to fit in budget 15, got %d (tokens: %d)", len(result), tokensUsed)
	}
}
