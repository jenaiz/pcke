package event

import (
	"bytes"
	"errors"
	"sort"
	"testing"
)

func TestEscapeUnescape_RoundTrip(t *testing.T) {
	t.Parallel()
	cases := []string{
		"plain",
		"path/with/slashes.go",
		"with spaces.go",
		"unicode-αβγ-字符",
		"colons:in:id",
		`back\slashes`,
		`mixed\:both`,
		`adjacent::colons`,
		`adjacent\\backslashes`,
		`trailing\`,     // unescaped trailing slash should still round-trip
		"",              // empty
		"a:b\\c:d\\e:f", // many specials
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			esc := EscapeID(raw)
			got, err := UnescapeID(esc)
			if err != nil {
				t.Fatalf("UnescapeID(%q from %q) errored: %v", esc, raw, err)
			}
			if got != raw {
				t.Errorf("round-trip: input %q, escaped %q, got %q", raw, esc, got)
			}
		})
	}
}

func TestEscape_NoSpecialsBypass(t *testing.T) {
	t.Parallel()
	// EscapeID returns the original string unchanged when no specials are
	// present (an optimisation we rely on for the hot path).
	in := "plain/path.go"
	if got := EscapeID(in); got != in {
		t.Errorf("EscapeID(%q) = %q, want unchanged", in, got)
	}
}

func TestUnescape_Errors(t *testing.T) {
	t.Parallel()
	cases := []string{
		`bad\`,           // trailing backslash
		`bad\x`,          // unknown escape
		`leading\\\then`, // mixed valid + then trailing single backslash treated by walker
	}
	for _, esc := range cases {
		t.Run(esc, func(t *testing.T) {
			t.Parallel()
			_, err := UnescapeID(esc)
			if err == nil {
				t.Fatalf("UnescapeID(%q): want error", esc)
			}
			if !errors.Is(err, ErrInvalidKey) {
				t.Errorf("UnescapeID(%q): got %v, want wrap of ErrInvalidKey", esc, err)
			}
		})
	}
}

func TestBuildKey_Format(t *testing.T) {
	t.Parallel()
	got, err := BuildKey(KindEntity, "internal/kdb/db.go", 1)
	if err != nil {
		t.Fatalf("BuildKey: %v", err)
	}
	want := []byte("e:internal/kdb/db.go:v0000000000000001")
	if !bytes.Equal(got, want) {
		t.Errorf("BuildKey:\n got  %q\n want %q", got, want)
	}
}

func TestBuildKey_EscapesColons(t *testing.T) {
	t.Parallel()
	got, err := BuildKey(KindDecision, "weird:id", 7)
	if err != nil {
		t.Fatalf("BuildKey: %v", err)
	}
	want := []byte(`d:weird\cid:v0000000000000007`)
	if !bytes.Equal(got, want) {
		t.Errorf("BuildKey:\n got  %q\n want %q", got, want)
	}
}

func TestBuildKey_Errors(t *testing.T) {
	t.Parallel()
	if _, err := BuildKey(Kind(0), "x", 1); !errors.Is(err, ErrInvalidKind) {
		t.Errorf("invalid kind: got %v, want ErrInvalidKind", err)
	}
	if _, err := BuildKey(KindEntity, "", 1); !errors.Is(err, ErrEmptyID) {
		t.Errorf("empty id: got %v, want ErrEmptyID", err)
	}
}

func TestParseKey_RoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind Kind
		id   string
		ver  uint64
	}{
		{KindEntity, "internal/kdb/db.go", 1},
		{KindEntity, "with spaces.go", 42},
		{KindEntity, "unicode-αβγ.go", 999},
		{KindDecision, "adr-0008", 1},
		{KindDecision, `mixed\path:weird`, 12345},
		{KindObservation, "session-uuid-1", 1},
		{KindOutcome, "respect-event-1", 1},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			key, err := BuildKey(tc.kind, tc.id, tc.ver)
			if err != nil {
				t.Fatalf("BuildKey: %v", err)
			}
			parsed, err := ParseKey(key)
			if err != nil {
				t.Fatalf("ParseKey(%q): %v", key, err)
			}
			if parsed.Kind != tc.kind || parsed.ID != tc.id || parsed.Version != tc.ver {
				t.Errorf("ParseKey: got %+v, want kind=%d id=%q ver=%d",
					parsed, tc.kind, tc.id, tc.ver)
			}
		})
	}
}

func TestParseKey_Errors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  string
		want error
	}{
		{"empty", "", ErrInvalidKey},
		{"no separator", "abcdefghijklmnopqrstuv", ErrInvalidKey},
		{"unknown prefix", "z:foo:v0000000000000001", ErrInvalidKind},
		{"missing version sep", "e:foo:vXX", ErrInvalidKey},
		{"bad version digits", "e:foo:v000000000000000a", ErrInvalidKey},
		{"short version digits", "e:foo:v0001", ErrInvalidKey},
		{"empty id", "e::v0000000000000001", ErrEmptyID},
		{"reverse-link routed elsewhere", "lr:foo:imports:bar:v0000000000000001", ErrInvalidKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseKey([]byte(tc.key))
			if err == nil {
				t.Fatalf("want error for %q", tc.key)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want wrap of %v", err, tc.want)
			}
		})
	}
}

func TestKey_LexicographicEqualsNumericVersion(t *testing.T) {
	t.Parallel()
	const id = "internal/kdb/db.go"
	versions := []uint64{1, 2, 9, 10, 11, 99, 100, 999, 1000, 1_000_000_000_000_000}

	keys := make([][]byte, 0, len(versions))
	for _, v := range versions {
		k, err := BuildKey(KindEntity, id, v)
		if err != nil {
			t.Fatalf("BuildKey(v=%d): %v", v, err)
		}
		keys = append(keys, k)
	}

	// Shuffle by sorting in a deterministic but non-trivial order, then
	// verify lexicographic sort restores the natural numeric order.
	sort.Slice(keys, func(i, j int) bool { return bytes.Compare(keys[i], keys[j]) < 0 })

	for i, want := range versions {
		got, err := ParseKey(keys[i])
		if err != nil {
			t.Fatalf("ParseKey: %v", err)
		}
		if got.Version != want {
			t.Errorf("position %d: got version %d, want %d (key=%q)", i, got.Version, want, keys[i])
		}
	}
}

func TestReverseLinkKey_RoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		dst, edge, src string
	}{
		{"e:foo.go", "imports", "e:bar.go"},
		{"e:weird:path.go", "decision_link", "d:adr-0008"},
		{"e:αβγ", "linked_module", "e:δεζ"},
	}
	for _, tc := range cases {
		t.Run(tc.dst+"->"+tc.src, func(t *testing.T) {
			t.Parallel()
			key, err := BuildReverseLinkKey(tc.dst, tc.edge, tc.src)
			if err != nil {
				t.Fatalf("BuildReverseLinkKey: %v", err)
			}
			parsed, err := ParseReverseLinkKey(key)
			if err != nil {
				t.Fatalf("ParseReverseLinkKey(%q): %v", key, err)
			}
			if parsed.DstRef != tc.dst || parsed.EdgeType != tc.edge || parsed.SrcRef != tc.src {
				t.Errorf("got %+v, want dst=%q edge=%q src=%q",
					parsed, tc.dst, tc.edge, tc.src)
			}
		})
	}
}

func TestReverseLinkKey_Errors(t *testing.T) {
	t.Parallel()
	if _, err := BuildReverseLinkKey("", "imports", "e:bar.go"); !errors.Is(err, ErrEmptyID) {
		t.Errorf("empty dst: got %v, want ErrEmptyID", err)
	}
	if _, err := ParseReverseLinkKey([]byte("e:foo.go")); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("non-lr prefix: got %v, want ErrInvalidKey", err)
	}
	if _, err := ParseReverseLinkKey([]byte("lr:onlyone")); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("missing segments: got %v, want ErrInvalidKey", err)
	}
}

func TestReverseLinkPrefixForDst(t *testing.T) {
	t.Parallel()
	got, err := reverseLinkPrefixForDst("e:foo.go", "imports")
	if err != nil {
		t.Fatalf("reverseLinkPrefixForDst: %v", err)
	}
	want := []byte(`lr:e\cfoo.go:imports:`)
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if _, err := reverseLinkPrefixForDst("", "x"); !errors.Is(err, ErrEmptyID) {
		t.Errorf("empty dst: got %v, want ErrEmptyID", err)
	}
}
