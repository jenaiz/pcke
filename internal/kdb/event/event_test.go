package event

import "testing"

func TestKind_Valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		k    Kind
		want bool
	}{
		{KindEntity, true},
		{KindDecision, true},
		{KindObservation, true},
		{KindOutcome, true},
		{KindLink, true},
		{0, false},
		{99, false},
	}
	for _, tc := range cases {
		if got := tc.k.Valid(); got != tc.want {
			t.Errorf("Kind(%d).Valid() = %v, want %v", tc.k, got, tc.want)
		}
	}
}

func TestKind_PrefixUniqueAndStable(t *testing.T) {
	t.Parallel()
	want := map[Kind]string{
		KindEntity:      "e:",
		KindDecision:    "d:",
		KindObservation: "o:",
		KindOutcome:     "x:",
		KindLink:        "l:",
	}
	seen := make(map[string]Kind, len(want))
	for k, p := range want {
		if got := k.Prefix(); got != p {
			t.Errorf("Kind(%s).Prefix() = %q, want %q", k, got, p)
		}
		if prior, dup := seen[p]; dup {
			t.Errorf("prefix %q reused by %s and %s", p, prior, k)
		}
		seen[p] = k
	}
	// Reverse-link prefix must not collide with any kind prefix.
	for _, p := range want {
		if p == ReverseLinkPrefix {
			t.Errorf("ReverseLinkPrefix %q collides with kind prefix", p)
		}
	}
}

func TestKind_PrefixInvalid(t *testing.T) {
	t.Parallel()
	if got := Kind(0).Prefix(); got != "" {
		t.Errorf("invalid kind should yield empty prefix, got %q", got)
	}
}

func TestKind_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		k    Kind
		want string
	}{
		{KindEntity, "entity"},
		{KindDecision, "decision"},
		{KindObservation, "observation"},
		{KindOutcome, "outcome"},
		{KindLink, "link"},
		{Kind(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tc.k, got, tc.want)
		}
	}
}

func TestKindFromPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		prefix string
		kind   Kind
		ok     bool
	}{
		{"e:", KindEntity, true},
		{"d:", KindDecision, true},
		{"o:", KindObservation, true},
		{"x:", KindOutcome, true},
		{"l:", KindLink, true},
		{"lr:", 0, false},
		{"e", 0, false},
		{":", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		k, ok := kindFromPrefix(tc.prefix)
		if ok != tc.ok {
			t.Errorf("kindFromPrefix(%q): ok = %v, want %v", tc.prefix, ok, tc.ok)
		}
		if k != tc.kind {
			t.Errorf("kindFromPrefix(%q): kind = %d, want %d", tc.prefix, k, tc.kind)
		}
	}
}
