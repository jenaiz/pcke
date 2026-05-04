package context

import "strings"

// EstimateTokens approximates the token count for a piece of text.
// Uses word_count × multiplier (default 1.3).
func EstimateTokens(text string, multiplier float64) int {
	if multiplier <= 0 {
		multiplier = 1.3
	}
	words := len(strings.Fields(text))
	return int(float64(words) * multiplier)
}

// TruncateToBudget takes ordered sections and returns a subset that fits
// within the token budget. The last included section may be truncated
// (keeping first N lines) if it would exceed the budget.
func TruncateToBudget(sections []Section, budget int, multiplier float64) ([]Section, int) {
	if multiplier <= 0 {
		multiplier = 1.3
	}
	if budget <= 0 {
		budget = 2000
	}

	var result []Section
	tokensUsed := 0

	for _, s := range sections {
		tokens := EstimateTokens(s.Content, multiplier)
		if tokensUsed+tokens <= budget {
			result = append(result, s)
			tokensUsed += tokens
			continue
		}

		// Try to fit a truncated version of this section.
		remaining := budget - tokensUsed
		if remaining <= 0 {
			break
		}

		truncated := truncateContent(s.Content, remaining, multiplier)
		if truncated != "" {
			s.Content = truncated
			result = append(result, s)
			tokensUsed += EstimateTokens(truncated, multiplier)
		}
		break
	}

	// Ensure at least 1 section is included.
	if len(result) == 0 && len(sections) > 0 {
		s := sections[0]
		s.Content = truncateContent(s.Content, budget, multiplier)
		if s.Content == "" {
			s.Content = sections[0].Content
		}
		result = append(result, s)
		tokensUsed = EstimateTokens(s.Content, multiplier)
	}

	return result, tokensUsed
}

// truncateContent keeps the first N lines of content that fit within
// the given token budget.
func truncateContent(content string, tokenBudget int, multiplier float64) string {
	lines := strings.Split(content, "\n")
	var kept []string
	tokens := 0

	for _, line := range lines {
		lineTokens := EstimateTokens(line, multiplier)
		if tokens+lineTokens > tokenBudget && len(kept) > 0 {
			break
		}
		kept = append(kept, line)
		tokens += lineTokens
	}

	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n")
}
