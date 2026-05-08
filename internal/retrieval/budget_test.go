package retrieval

import "testing"

func TestTokensFor(t *testing.T) {
	t.Parallel()
	cases := map[string]int{
		"":                        0,
		"   ":                     0,
		"hello":                   2, // ceil(1*1.3) = 2
		"two words":               3, // ceil(2*1.3) = 3
		"one two three four five": 7, // ceil(5*1.3) = 7
		"line one\nline two":      5, // 4 words, ceil(4*1.3) = 6 → wait recount
	}
	// Recompute the last to be sure.
	// "line one\nline two" -> ["line", "one", "line", "two"] -> 4 words -> ceil(5.2) = 6.
	cases["line one\nline two"] = 6

	for body, want := range cases {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			if got := TokensFor(body); got != want {
				t.Errorf("TokensFor(%q) = %d, want %d", body, got, want)
			}
		})
	}
}

func TestEffectiveBudget(t *testing.T) {
	t.Parallel()
	cases := map[int]int{
		0:    DefaultBudget,
		-1:   DefaultBudget,
		500:  500,
		5000: 5000,
	}
	for in, want := range cases {
		got := EffectiveBudget(Request{Budget: in})
		if got != want {
			t.Errorf("EffectiveBudget(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestFitToBudget_AllFit(t *testing.T) {
	t.Parallel()
	sections := []Section{
		{Ref: "e:a", Tokens: 100},
		{Ref: "e:b", Tokens: 200},
		{Ref: "e:c", Tokens: 300},
	}
	out, truncated := FitToBudget(sections, 1000)
	if truncated {
		t.Error("truncated = true, want false")
	}
	if len(out) != 3 {
		t.Errorf("len = %d, want 3", len(out))
	}
}

func TestFitToBudget_TruncatesGreedy(t *testing.T) {
	t.Parallel()
	sections := []Section{
		{Ref: "e:a", Tokens: 400},
		{Ref: "e:b", Tokens: 400},
		{Ref: "e:c", Tokens: 400}, // would push us over 1000
	}
	out, truncated := FitToBudget(sections, 1000)
	if !truncated {
		t.Error("truncated = false, want true")
	}
	if len(out) != 2 {
		t.Errorf("len = %d, want 2", len(out))
	}
}

func TestFitToBudget_SkipsOversizedItem(t *testing.T) {
	t.Parallel()
	// First item is too big alone — it should be skipped, then smaller
	// items behind it admitted.
	sections := []Section{
		{Ref: "e:huge", Tokens: 5000},
		{Ref: "e:a", Tokens: 200},
		{Ref: "e:b", Tokens: 200},
	}
	out, truncated := FitToBudget(sections, 1000)
	if !truncated {
		t.Error("truncated = false, want true")
	}
	if len(out) != 2 || out[0].Ref != "e:a" {
		t.Errorf("out = %+v, want [e:a, e:b]", out)
	}
}

func TestFitToBudget_ZeroOrNegativeLimit(t *testing.T) {
	t.Parallel()
	sections := []Section{{Ref: "e:a", Tokens: 100}}
	out, truncated := FitToBudget(sections, 0)
	if !truncated || len(out) != 0 {
		t.Errorf("limit=0: got len=%d truncated=%v, want 0/true", len(out), truncated)
	}
	out, truncated = FitToBudget(sections, -1)
	if !truncated || len(out) != 0 {
		t.Errorf("limit=-1: got len=%d truncated=%v, want 0/true", len(out), truncated)
	}
}

func TestFitToBudget_EmptyInput(t *testing.T) {
	t.Parallel()
	out, truncated := FitToBudget(nil, 1000)
	if truncated {
		t.Error("empty input must not be reported as truncated")
	}
	if len(out) != 0 {
		t.Errorf("len = %d, want 0", len(out))
	}
}
